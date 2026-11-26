package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// lz4Literals is an LZ4 block of the bytes as literals: the number of them, continued past 14, and the bytes.
func lz4Literals(b []byte) []byte {
	if len(b) < 15 {
		return append([]byte{byte(len(b) << 4)}, b...)
	}
	block := []byte{0xF0}
	n := len(b) - 15
	for ; n >= 255; n -= 255 {
		block = append(block, 255)
	}
	return append(append(block, byte(n)), b...)
}

// xpakBlock is a block of an item's data in Black Ops III's packs: its header, with the number of its commands, the
// offset of its output, and its commands (each its kind and its data), and then the commands' data, padded to a
// multiple of 128 bytes.
func xpakBlock(offset int, kinds []byte, commands ...[]byte) []byte {
	block := binary.LittleEndian.AppendUint32(nil, uint32(len(commands)))
	block = binary.LittleEndian.AppendUint32(block, uint32(offset))
	for i, c := range commands {
		block = binary.LittleEndian.AppendUint32(block, uint32(kinds[i])<<24|uint32(len(c)))
	}
	block = append(block, make([]byte, max(128-len(block), 0))...) // a header of more than 30 commands is longer
	for _, c := range commands {
		block = append(block, c...)
	}
	for len(block)%128 != 0 {
		block = append(block, 0x93)
	}
	return block
}

// xpakItemData is an item's data in a pack, of the bytes: in three blocks, which are up to three commands of LZ4 of
// 100 bytes each, then padding, then the rest as it is.
func xpakItemData(item []byte) []byte {
	var compressed [][]byte
	rest := item
	for len(compressed) < 3 && len(rest) > 0 {
		n := min(100, len(rest))
		compressed, rest = append(compressed, lz4Literals(rest[:n])), rest[n:]
	}
	data := xpakBlock(0, []byte{3, 3, 3}, compressed...)
	data = append(data, xpakBlock(0, []byte{0xCF}, bytes.Repeat([]byte{0x93}, 64))...)
	if len(rest) > 0 {
		data = append(data, xpakBlock(len(item)-len(rest), []byte{0}, rest)...)
	}
	return data
}

// xpakListed is an image as a pack lists it: its key and the lines of its text.
type xpakListed struct {
	key  uint64
	text string
}

// listedBC7 is an image in BC7 of w by h texels, as a pack lists it under the key: the field called width is its
// height.
func listedBC7(key uint64, name string, w, h int) xpakListed {
	return xpakListed{key, fmt.Sprintf("width: %d\nheight: %d\nlevels: 1\nmanual: 1\nformat: BC7\nsemantic: 2d\n"+
		"initial: all\nname: %s\ntype: image\ninitial: all\noffset0: 0\nsize0: %d\ncompress: lz4hc", h, w, name,
		(w+3)/4*((h+3)/4)*16)}
}

// writeXpak writes a pack of Black Ops III's that lists the images, and has the data of the items, by key.
func writeXpak(t *testing.T, file string, listed []xpakListed, items map[uint64][]byte) {
	t.Helper()
	var data, table, list []byte
	for _, key := range slices.Sorted(func(yield func(uint64) bool) {
		for key := range items {
			if !yield(key) {
				return
			}
		}
	}) {
		blocks := xpakItemData(items[key])
		table = binary.LittleEndian.AppendUint64(table, key)
		table = binary.LittleEndian.AppendUint64(table, uint64(len(data)))
		table = binary.LittleEndian.AppendUint64(table, uint64(len(blocks)))
		data = append(data, blocks...)
	}
	for _, l := range listed {
		list = binary.LittleEndian.AppendUint64(list, l.key)
		list = binary.LittleEndian.AppendUint64(list, uint64(len(l.text)))
		list = append(list, l.text...)
	}
	const at = 256
	head := make([]byte, at)
	copy(head, "KAPI\x00\x00\x0a\x00")
	for i, v := range map[int]int{8: 1, 16: at + len(data) + len(table) + len(list), 24: len(items), 32: at, 40: len(data),
		48: len(items), 56: at + len(data), 64: len(table), 96: len(listed), 104: at + len(data) + len(table),
		112: len(list)} {
		binary.LittleEndian.PutUint64(head[i:], uint64(v))
	}
	writeFile(t, file, string(slices.Concat(head, data, table, list)))
}

func TestLZ4(t *testing.T) {
	long := bytes.Repeat([]byte("0123456789abcdef"), 18)[:274]
	for _, c := range []struct{ name, in, want string }{
		{"literals", "\x40abcd", "abcd"},
		// 8 bytes from 4 back, and then a literal
		{"a match", "\x44abcd\x04\x00\x10e", "abcdabcdabcde"},
		// 5 bytes from 1 back: the byte before them, again and again
		{"a match over itself", "\x11a\x01\x00\x10b", "aaaaaab"},
		// 274 literals: 15, 255 more, and 4 more
		{"a long run of literals", "\xf0\xff\x04" + string(long), string(long)},
		// 280 bytes from 2 back: 4, 15, 255 more, and 6 more
		{"a long match", "\x2fab\x02\x00\xff\x06\x10c", string(bytes.Repeat([]byte("ab"), 141)) + "c"},
	} {
		got, err := lz4([]byte("xy"), []byte(c.in), 1000)
		if err != nil || string(got) != "xy"+c.want {
			t.Errorf("%s: %q, %v", c.name, got, err)
		}
	}
	for name, in := range map[string]string{"cut short": "\x40ab", "a match at its end": "\x44abcd\x04\x00",
		"a match back past its start": "\x44abcd\x05\x00\x10e", // 5 back: not to what was before it
		"a match back no distance":    "\x44abcd\x00\x00\x10e", "a match without its distance": "\x44abcd\x04",
		"a run of literals without its length": "\xf0\xff", "nothing": ""} {
		if got, err := lz4([]byte("xy"), []byte(in), 1000); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got, err := lz4(nil, []byte("\x44abcd\x04\x00\x10e"), 12); err == nil {
		t.Errorf("13 bytes where 12 may be: %q", got)
	}
}

// An item's data in a pack: its blocks' commands one after another, past the padding; a block must go where the item
// is, and has 30 commands at most. A pack's list names its images, and its table has where the items are.
func TestXpakItem(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "base.xpak")
	item := bytes.Repeat([]byte("0123456789"), 40)
	writeXpak(t, file, []xpakListed{listedBC7(7, "img", 8, 4), {8, "name: a_model\ntype: xmodel\noffset0: 0\nsize0: 4"},
		{9, "name: no_size\ntype: image\nformat: BC7\noffset0: 0\nsize0: 4"}}, map[uint64][]byte{7: item, 8: []byte("abcd")})
	images, err := xpakImages(file)
	if want := map[string]xpakImage{"img": {7, 8, 4, "BC7", 0, 32}}; err != nil || fmt.Sprint(images) != fmt.Sprint(want) {
		t.Fatalf("the images that the pack lists: %v, %v, want %v", images, err, want)
	}
	items := map[uint64]xpakItem{}
	if err := xpakItems(file, map[uint64]bool{7: true}, items); err != nil || len(items) != 1 {
		t.Fatalf("where the pack's items are: %v, %v", items, err)
	}
	if got, err := items[7].read(); err != nil || !bytes.Equal(got, item) {
		t.Errorf("the item: %q, %v", got, err)
	}
	for name, blocks := range map[string][]byte{
		"a block out of place":   xpakBlock(5, []byte{0}, []byte("abcd")),
		"31 commands":            xpakBlock(0, bytes.Repeat([]byte{0}, 31), slices.Repeat([][]byte{[]byte("a")}, 31)...),
		"a command past the end": xpakBlock(0, []byte{0}, []byte("abcd"))[:130],
		"bad LZ4":                xpakBlock(0, []byte{3}, []byte("\x40ab")),
	} {
		bad := filepath.Join(dir, "bad.xpak")
		writeFile(t, bad, string(blocks))
		if got, err := (xpakItem{bad, 0, int64(len(blocks))}).read(); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	if _, err := xpakImages(filepath.Join(dir, "bad.xpak")); err == nil {
		t.Errorf("a file that is no pack listed images")
	}
	if err := xpakItems(filepath.Join(dir, "bad.xpak"), map[uint64]bool{7: true}, items); err == nil {
		t.Errorf("a file that is no pack had items")
	}
}

// bc7Texture is a BC7 texture of w by h texels in one colour: each block's first endpoint, whose red, green and blue
// are 7 bits each, with a lowest bit of 0. Its alpha is then 254, as that of a block of this mode with such a bit is.
func bc7Texture(t *testing.T, w, h int, r, g, b int) []byte {
	block := bc7Block(t, 6, 0, 0, 0, [][4]int{{r, g, b, 127}, {}}, []int{0, 0}, texels(), nil)
	return bytes.Repeat(block, (w+3)/4*((h+3)/4))
}

// Black Ops III's maps' pictures. A map's loading screen is from the game's packs: by the name that the pack of the
// zone core_patch lists it under, from whichever pack has the data of its key. It is opaque and at 16:9. A map's
// preview is its picture in the game's map select, from the zones of the game's menus: a fastfile's zone, with what
// its delta adds. A map without one has no preview, whatever loading screen it has. A server without the zones has
// no pictures of that game's maps, and its lobby says so of each.
func TestT7MapArt(t *testing.T) {
	t7Dir := t.TempDir()
	zone := filepath.Join(t7Dir, "zone")
	writeXpak(t, filepath.Join(zone, "core_patch.xpak"), []xpakListed{
		listedBC7(1, "t7_menu_zm_loadscreen_zod", 32, 16),
		listedBC7(2, "img_loadscreen_zm_factory", 32, 32),
		listedBC7(3, "t7_menu_zm_loadscreen_castle", 16, 16), // no pack has its data
		{4, "width: 4\nheight: 4\nformat: BC1_SRGB\nname: t7_menu_zm_loadscreen_island\ntype: image\noffset0: 0\nsize0: 8"},
		// two levels, where a loading screen has one
		{6, "width: 8\nheight: 8\nlevels: 2\nformat: BC7\nname: t7_menu_zm_loadscreen_genesis\ntype: image\noffset0: 0\nsize0: 80"},
		listedBC7(7, "t7_menu_zm_loadscreen_moon", 16, 16),
	}, nil)
	writeXpak(t, filepath.Join(zone, "base.xpak"), []xpakListed{listedBC7(1, "t7_menu_zm_loadscreen_zod", 32, 16)},
		map[uint64][]byte{1: bc7Texture(t, 32, 16, 127, 0, 0), 7: bc7Texture(t, 16, 16, 127, 127, 127),
			9: []byte("another item")})
	// The previews. The fastfile of the menus' zone has Shadows of Evil's, in red, and only names Moon's. Its delta
	// adds Der Eisendrache's, in green. The zone of the updates' menus has The Giant's, in blue, and Zetsubou No
	// Shima's in a format that the server doesn't read.
	menus := slices.Concat(filler(1, 900),
		zoneImageOf("img_t7_menu_zm_preview_zod", 32, 16, 98, bc7Texture(t, 32, 16, 127, 0, 0)), filler(2, 300),
		[]byte(",img_t7_menu_zm_preview_moon\x00"))
	fastfile := t7FastfileOf(menus, 500, -1)
	writeFile(t, filepath.Join(zone, "core_frontend.ff"), string(fastfile))
	updated, windows := deltaBetween(menus, 1000, deltaPart{from: 0, to: 400},
		deltaPart{add: zoneImageOf("img_t7_menu_zm_preview_castle", 16, 8, 99, bc7Texture(t, 16, 8, 0, 127, 0))},
		deltaPart{from: 400, to: len(menus)})
	writeFile(t, filepath.Join(zone, "core_frontend.fd"), string(t7DeltaOf(fastfile, len(updated), windows...)))
	writeFile(t, filepath.Join(zone, "core_frontend_patch.ff"), string(t7FastfileOf(slices.Concat(filler(3, 200),
		zoneImageOf("img_mapvote_zm_factory", 32, 16, 98, bc7Texture(t, 32, 16, 0, 0, 127)),
		zoneImageOf("img_t7_menu_zm_preview_island", 4, 4, 71, make([]byte, 8))), 500, -1)))
	// a pack of an update lists the picture that the game had before it, under another key: the zone's is the one
	writeXpak(t, filepath.Join(zone, "base_patch_tu5.xpak"), []xpakListed{listedBC7(5, "t7_menu_zm_loadscreen_zod", 32, 16)},
		map[uint64][]byte{2: bc7Texture(t, 32, 32, 0, 0, 127), 4: make([]byte, 8), 5: bc7Texture(t, 32, 16, 0, 127, 0),
			6: make([]byte, 80)})
	writeFile(t, filepath.Join(zone, "zm_zod.xpak"), "not a pack")
	h := &web{gameDir: t.TempDir(), t7Dir: t7Dir}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// the loading screen as it is, in red, and opaque: not darkened by the alpha of its texture
	rsp, body := get(t, srv.URL+"/maps/zm_zod/loadscreen")
	img, err := jpeg.Decode(bytes.NewReader([]byte(body)))
	if err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" || img.Bounds() != image.Rect(0, 0, 32, 18) {
		t.Fatalf("Shadows of Evil's loading screen: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	if c := img.At(16, 9); !colourIs(c, color.NRGBA{254, 0, 0, 255}) {
		t.Errorf("Shadows of Evil's loading screen: %v, want red", c)
	}
	// The Giant's, which the game stores square: at 16:9, from the pack of an update
	rsp, body = get(t, srv.URL+"/maps/zm_factory/loadscreen")
	if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || img.Bounds() != image.Rect(0, 0, 32, 18) ||
		!colourIs(img.At(16, 9), color.NRGBA{0, 0, 254, 255}) {
		t.Errorf("The Giant's loading screen: %s, %v %v", rsp.Status, err, img)
	}
	// the previews, each a JPEG of 640x270, and opaque: from the menus' fastfile, from its delta, and from the zone
	// of the updates' menus
	for name, want := range map[string]color.NRGBA{"zm_zod": {254, 0, 0, 255}, "zm_castle": {0, 254, 0, 255},
		"zm_factory": {0, 0, 254, 255}} {
		rsp, body = get(t, srv.URL+"/maps/"+name+"/preview")
		if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" ||
			img.Bounds() != image.Rect(0, 0, 640, 270) || !colourIs(img.At(320, 135), want) {
			t.Errorf("%s's preview: %s %s, %v %v", name, rsp.Status, rsp.Header.Get("Content-Type"), err, img)
		}
	}
	if etag := rsp.Header.Get("ETag"); rsp.Header.Get("Cache-Control") != "no-cache" || len(etag) != 34 {
		t.Errorf("a preview: Cache-Control %q, ETag %q", rsp.Header.Get("Cache-Control"), etag)
	}
	// Moon has a loading screen and no preview: its loading screen does not stand in
	if rsp, _ = get(t, srv.URL+"/maps/zm_moon/loadscreen"); rsp.StatusCode != 200 {
		t.Errorf("Moon's loading screen: %s", rsp.Status)
	}
	// a map whose loading screen no pack has, one whose is in a format the server doesn't read, one whose has more
	// than one level; a map whose preview the zones only name, one whose is in a format the server doesn't read, and
	// one that the zones have nothing of; and a card, which only a Black Ops II map has
	for _, p := range []string{"/maps/zm_castle/loadscreen", "/maps/zm_island/loadscreen", "/maps/zm_genesis/loadscreen",
		"/maps/zm_moon/preview", "/maps/zm_island/preview", "/maps/zm_tomb/preview", "/maps/zm_zod/card"} {
		if rsp, _ = get(t, srv.URL+p); rsp.StatusCode != 404 {
			t.Errorf("%s: %s, want 404", p, rsp.Status)
		}
	}
	if _, err := h.t7Image("t7_menu_zm_loadscreen_castle"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an image that no pack has: %v", err)
	}
	if _, err := h.t7Image("t7_menu_zm_loadscreen_island"); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("an image in another format: %v", err)
	}

	// a server without the packs, and one whose folder lacks the zone's pack: no pictures of the game's maps
	if h.noMapArt("zm_zod") || !(&web{}).noMapArt("zm_zod") || (&web{}).noMapArt("zombie_theater") ||
		(&web{}).noMapArt("zm_classic_transit") {
		t.Errorf("which maps a server has no pictures of")
	}
	if _, ok := (&web{}).mapArt("zm_zod", "loadscreen"); ok {
		t.Errorf("a server without the packs has a picture")
	}
	if _, err := (&web{}).t7Image("t7_menu_zm_loadscreen_zod"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an image of a server without the packs: %v", err)
	}
	lacks := t.TempDir()
	writeXpak(t, filepath.Join(lacks, "zone", "base.xpak"), []xpakListed{listedBC7(1, "t7_menu_zm_loadscreen_zod", 32, 16)},
		map[uint64][]byte{1: bc7Texture(t, 32, 16, 127, 0, 0)})
	if _, ok := (&web{t7Dir: lacks}).mapArt("zm_zod", "loadscreen"); ok {
		t.Errorf("a server without the zone's pack has a picture")
	}
	if _, ok := (&web{t7Dir: lacks}).mapArt("zm_zod", "preview"); ok {
		t.Errorf("a server without the menus' zones has a preview")
	}
	if _, ok := (&web{}).mapArt("zm_zod", "preview"); ok {
		t.Errorf("a server without Black Ops III's pictures has a preview")
	}

	// the lobby says which maps the server has no pictures of
	l := testLobby(t)
	ann := l.hello("", "Ann").Token
	noPictures := func() []string {
		var maps []string
		for _, m := range l.viewOf(ann).Maps {
			if m.NoPictures {
				maps = append(maps, m.Name)
			}
		}
		return maps
	}
	if got := noPictures(); got != nil {
		t.Errorf("a lobby that is told nothing of the pictures says that it lacks those of %v", got)
	}
	l.noArt = (&web{}).noMapArt
	if got := noPictures(); len(got) != len(t7MapList) || got[0] != "zm_zod" {
		t.Errorf("the maps without pictures, on a server without Black Ops III's packs: %v", got)
	}
	l.noArt = h.noMapArt
	if got := noPictures(); got != nil {
		t.Errorf("the maps without pictures, on a server with Black Ops III's packs: %v", got)
	}
}
