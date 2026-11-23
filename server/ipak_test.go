package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// t6IWI is the IWI as Black Ops II has it (version 27, a header of 64 bytes), from Black Ops' (iwi).
func t6IWI(iwi []byte) []byte {
	head := make([]byte, 64)
	copy(head, iwi[:12])
	head[3] = 27
	return append(head, iwi[48:]...)
}

// t6TileIWI is a tile of Black Ops II's leaderboards, of 32x32 in DXT5: a frame a block wide that is transparent,
// around an opaque picture in blue.
func t6TileIWI() []byte {
	var data []byte
	for by := 0; by < 8; by++ {
		for bx := 0; bx < 8; bx++ {
			a := byte(255)
			if bx == 0 || by == 0 || bx == 7 || by == 7 {
				a = 0
			}
			data = append(data, a, a, 0, 0, 0, 0, 0, 0)           // alpha: every texel a
			data = binary.LittleEndian.AppendUint16(data, 0x001F) // blue, which every texel takes
			data = append(data, 0, 0, 0, 0, 0, 0)
		}
	}
	return t6IWI(iwi(iwiDXT5, 32, 32, data))
}

// lzoLiterals is LZO1X data of the bytes as literals, 238 at most: the first byte says how many, and the data ends with
// a match that goes back no distance.
func lzoLiterals(b []byte) []byte {
	return append(append([]byte{byte(len(b) + 17)}, b...), 0x11, 0, 0)
}

// imageBlock is a block of an image's data: its header, with the offset of its output and its commands (each its kind
// and its data), and then the commands' data, padded to a multiple of 128 bytes.
func imageBlock(offset int, kinds []byte, commands ...[]byte) []byte {
	block := binary.LittleEndian.AppendUint32(nil, uint32(len(commands))<<24|uint32(offset))
	for i, c := range commands {
		block = binary.LittleEndian.AppendUint32(block, uint32(kinds[i])<<24|uint32(len(c)))
	}
	block = append(block, make([]byte, max(128-len(block), 0))...) // a header of more than 31 commands is longer
	for _, c := range commands {
		block = append(block, c...)
	}
	for len(block)%128 != 0 {
		block = append(block, 0xA7)
	}
	return block
}

// writePack writes an image pack with the images, by name. Each image is in three blocks: up to three commands of
// LZO1X of 100 bytes each, then padding, then the rest as it is.
func writePack(t *testing.T, file string, images map[string][]byte) {
	t.Helper()
	var data, index []byte
	for name, img := range images {
		var compressed [][]byte
		rest := img
		for len(compressed) < 3 && len(rest) > 0 {
			n := min(100, len(rest))
			compressed, rest = append(compressed, lzoLiterals(rest[:n])), rest[n:]
		}
		e := imageBlock(0, []byte{1, 1, 1}, compressed...)
		e = append(e, imageBlock(0, []byte{0xCF}, bytes.Repeat([]byte{0xA7}, 64))...)
		if len(rest) > 0 {
			e = append(e, imageBlock(len(img)-len(rest), []byte{0}, rest)...)
		}
		index = binary.LittleEndian.AppendUint32(index, 0x12345678) // the hash of the data, which the server ignores
		index = binary.LittleEndian.AppendUint32(index, ipakHash(name))
		index = binary.LittleEndian.AppendUint32(index, uint32(len(data)))
		index = binary.LittleEndian.AppendUint32(index, uint32(len(e)))
		data = append(data, e...)
	}
	at := 128
	pack := append([]byte("KAPI"), 0, 0, 5, 0)
	pack = binary.LittleEndian.AppendUint32(pack, uint32(at+len(data)+len(index)))
	pack = binary.LittleEndian.AppendUint32(pack, 3)
	for _, s := range [][4]int{{4, 0, 0, 0}, {2, at, len(data), len(images)}, {1, at + len(data), len(index), len(images)}} {
		for _, v := range s {
			pack = binary.LittleEndian.AppendUint32(pack, uint32(v))
		}
	}
	pack = append(pack, make([]byte, at-len(pack))...)
	writeFile(t, file, string(append(append(pack, data...), index...)))
}

// The hash of an image's name in the packs: the game's, as the packs' indexes have it, in any case.
func TestIpakHash(t *testing.T) {
	for name, want := range map[string]uint32{"menu_zm_map_nuketown": 0xe3baae90,
		"menu_zm_transit_zclassic_transit": 0xe13cb6ba, "MENU_ZM_Transit_zclassic_transit": 0xe13cb6ba} {
		if got := ipakHash(name); got != want {
			t.Errorf("ipakHash(%q): %#x, want %#x", name, got, want)
		}
	}
}

func TestLZO1X(t *testing.T) {
	long := bytes.Repeat([]byte("0123456789abcdef"), 18)[:274]
	for _, c := range []struct{ name, in, want string }{
		{"literals", "\x15abcd\x11\x00\x00", "abcd"},
		// 8 bytes from 4 back, with no literals after them
		{"a match", "\x15abcd\x26\x0c\x00\x11\x00\x00", "abcdabcdabcd"},
		// 3 bytes from 2 back, with a literal after them
		{"a short match and a literal", "\x15abcd\x45\x00x\x11\x00\x00", "abcdcdcx"},
		// a run of 274 literals: 0 is a long run, 255 for each 0 after it, then 1 and 18 more
		{"a long run of literals", "\x00\x00\x01" + string(long) + "\x11\x00\x00", string(long)},
	} {
		got, err := lzo1x([]byte("xy"), []byte(c.in), 1000)
		if err != nil || string(got) != "xy"+c.want {
			t.Errorf("%s: %q, %v", c.name, got, err)
		}
	}
	for name, in := range map[string]string{"cut short": "\x15ab", "with no end": "\x15abcd",
		"a match back past the start": "\x15abcd\x26\x10\x00\x11\x00\x00", // 5 back: not to what was before it
		"more after the end":          "\x15abcd\x11\x00\x00z", "nothing": ""} {
		if got, err := lzo1x([]byte("xy"), []byte(in), 1000); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got, err := lzo1x(nil, []byte("\x15abcd\x26\x0c\x00\x11\x00\x00"), 11); err == nil {
		t.Errorf("12 bytes where 11 may be: %q", got)
	}
}

// An image's file in a pack: its blocks' commands one after another, past the padding; a block must go where the file
// is, and has 31 commands at most.
func TestPackEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.ipak")
	img := bytes.Repeat([]byte("0123456789"), 40)
	writePack(t, file, map[string][]byte{"img": img})
	entries := map[uint32]packEntry{}
	if err := readPackIndex(file, entries); err != nil || len(entries) != 1 {
		t.Fatalf("the index: %v %v", entries, err)
	}
	if got, err := entries[ipakHash("img")].read(); err != nil || !bytes.Equal(got, img) {
		t.Errorf("the image: %q, %v", got, err)
	}
	for name, blocks := range map[string][]byte{
		"a block out of place":   imageBlock(5, []byte{0}, []byte("abcd")),
		"32 commands":            imageBlock(0, bytes.Repeat([]byte{0}, 32), slices.Repeat([][]byte{[]byte("a")}, 32)...),
		"a command past the end": imageBlock(0, []byte{0}, []byte("abcd"))[:130],
		"bad LZO1X":              imageBlock(0, []byte{1}, []byte("\x15ab")),
	} {
		bad := filepath.Join(dir, "bad.ipak")
		writeFile(t, bad, string(blocks))
		if got, err := (packEntry{bad, 0, int64(len(blocks))}).read(); err == nil {
			t.Errorf("%s: %q", name, got)
		}
	}
	if err := readPackIndex(filepath.Join(dir, "bad.ipak"), entries); err == nil {
		t.Errorf("a file that is no pack had an index")
	}
}

// t6FrameIWI is a frame of the lobby's cards, of 32x32 in DXT5: transparent, with an opaque black band in its last
// 8 rows.
func t6FrameIWI() []byte {
	var data []byte
	for by := 0; by < 8; by++ {
		for bx := 0; bx < 8; bx++ {
			a := byte(0)
			if by >= 6 {
				a = 255
			}
			data = append(data, a, a, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0) // every texel a, and black
		}
	}
	return t6IWI(iwi(iwiDXT5, 32, 32, data))
}

// colourIs reports whether c is near enough to the colour want, as a JPEG has it.
func colourIs(c color.Color, want color.NRGBA) bool {
	got := color.NRGBAModel.Convert(c).(color.NRGBA)
	near := func(a, b uint8) bool { return max(int(a)-int(b), int(b)-int(a)) < 48 }
	return near(got.R, want.R) && near(got.G, want.G) && near(got.B, want.B)
}

// Black Ops II's maps' pictures, from its packs: the picture on the map's card in the game's lobby (the tile, opaque,
// at 480x222), the card (the picture across the top of the card, under the lobby's frame), and the loading screen at
// 16:9. A patch's pack overrides the others, and a later one by name an earlier one. Without a frame, the card is the
// picture alone; without the tile, the loading screen stands in.
func TestT6MapArt(t *testing.T) {
	t6Dir := t.TempDir()
	packs := filepath.Join(t6Dir, "zone", "all")
	writePack(t, filepath.Join(packs, "base.ipak"), map[string][]byte{
		"loadscreen_transit_classic": t6IWI(dxt1(16, 16, 0x07E0)), // green, which the patch's overrides
		"menu_zm_map_frame":          t6FrameIWI(),
	})
	writePack(t, filepath.Join(packs, "patch_zm.ipak"), map[string][]byte{
		"loadscreen_transit_classic": t6IWI(dxt1(16, 16, 0xF800)), // red
	})
	writePack(t, filepath.Join(packs, "zm.ipak"), map[string][]byte{
		"menu_zm_transit_zclassic_transit": t6TileIWI(),
		"loadscreen_transit_classic":       t6IWI(dxt1(16, 16, 0x07E0)), // green, last by name but no patch's
		"loadscreen_zm_nuketown":           t6IWI(dxt1(32, 32, 0x001F)),
	})
	writeFile(t, filepath.Join(packs, "notes.txt"), "not a pack")
	srv := httptest.NewServer(&web{gameDir: t.TempDir(), t6Dir: t6Dir})
	defer srv.Close()

	// the picture: the tile in blue, even where the tile's own frame is transparent
	rsp, body := get(t, srv.URL+"/maps/zm_classic_transit/preview")
	img, err := jpeg.Decode(bytes.NewReader([]byte(body)))
	if err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" || img.Bounds() != image.Rect(0, 0, 480, 222) {
		t.Fatalf("TranZit's picture: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	for _, at := range []image.Point{{4, 4}, {240, 110}, {475, 217}} {
		if c := img.At(at.X, at.Y); !colourIs(c, color.NRGBA{0, 0, 255, 255}) {
			t.Errorf("TranZit's picture at %v: %v, want blue", at, c)
		}
	}
	// the card: the picture across its top 222 rows, and the frame's band at its foot
	rsp, body = get(t, srv.URL+"/maps/zm_classic_transit/card")
	if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" ||
		img.Bounds() != image.Rect(0, 0, 480, 290) {
		t.Fatalf("TranZit's card: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	for at, want := range map[image.Point]color.NRGBA{{4, 4}: {0, 0, 255, 255}, {240, 110}: {0, 0, 255, 255},
		{475, 200}: {0, 0, 255, 255}, {240, 250}: {0, 0, 0, 255}} {
		if c := img.At(at.X, at.Y); !colourIs(c, want) {
			t.Errorf("TranZit's card at %v: %v, want %v", at, c, want)
		}
	}
	rsp, body = get(t, srv.URL+"/maps/zm_classic_transit/loadscreen")
	if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" ||
		img.Bounds() != image.Rect(0, 0, 16, 9) {
		t.Fatalf("TranZit's loading screen: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	if c := img.At(8, 4); !colourIs(c, color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("TranZit's loading screen is not the patch's (red): %v", c)
	}
	// a card without the lobby's frame: the tile, and black under it
	noFrame := t.TempDir()
	writePack(t, filepath.Join(noFrame, "zone", "all", "zm.ipak"), map[string][]byte{
		"menu_zm_transit_zclassic_transit": t6TileIWI()})
	if pic, ok := (&web{t6Dir: noFrame}).mapArt("zm_classic_transit", "card"); !ok {
		t.Errorf("a card without a frame: none")
	} else if img, err = jpeg.Decode(bytes.NewReader(pic.data)); err != nil || !colourIs(img.At(240, 110), color.NRGBA{0, 0, 255, 255}) ||
		!colourIs(img.At(240, 260), color.NRGBA{0, 0, 0, 255}) {
		t.Errorf("a card without a frame: %v, %v %v", err, img.At(240, 110), img.At(240, 260))
	}
	// Nuketown has no tile: its loading screen, for each
	for _, kind := range []string{"preview", "card", "loadscreen"} {
		rsp, body = get(t, srv.URL+"/maps/zm_standard_nuked/"+kind)
		if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || img.Bounds() != image.Rect(0, 0, 32, 18) {
			t.Errorf("Nuketown's %s: %s %s, %v %v", kind, rsp.Status, rsp.Header.Get("Content-Type"), err, img)
		}
	}
	// a map that the packs have no picture of, a server without Black Ops II, and a Black Ops map's card, which it has
	// none of
	srv2 := httptest.NewServer(&web{gameDir: t.TempDir()})
	defer srv2.Close()
	for _, url := range []string{srv.URL + "/maps/zm_classic_tomb/preview", srv.URL + "/maps/zm_classic_tomb/card",
		srv.URL + "/maps/zm_classic_tomb/loadscreen", srv2.URL + "/maps/zm_classic_transit/preview",
		srv2.URL + "/maps/zm_classic_transit/card", srv2.URL + "/maps/zombie_theater/card"} {
		if rsp, _ = get(t, url); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", url, rsp.Status)
		}
	}
	if _, err := (&web{}).t6Image("loadscreen_transit_classic"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an image of a server without Black Ops II: %v", err)
	}
}
