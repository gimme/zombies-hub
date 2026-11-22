package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// dxt5 is a DXT5 texture of across by down blocks, of 4x4 texels each, every block in one colour (RGB565) and alpha,
// as block says.
func dxt5(across, down int, block func(x, y int) (colour uint16, alpha byte)) []byte {
	var data []byte
	for y := 0; y < down; y++ {
		for x := 0; x < across; x++ {
			colour, alpha := block(x, y)
			data = append(data, alpha, 0, 0, 0, 0, 0, 0, 0) // every texel the first alpha
			data = binary.LittleEndian.AppendUint16(data, colour)
			data = append(data, 0, 0, 0, 0, 0, 0) // and the first colour
		}
	}
	return iwi(iwiDXT5, across*4, down*4, data)
}

// la16 is a texture of luminance and alpha, a byte each, of w by h texels, as texel says.
func la16(w, h int, texel func(x, y int) (luminance, alpha byte)) []byte {
	var data []byte
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			luminance, alpha := texel(x, y)
			data = append(data, luminance, alpha)
		}
	}
	return iwi(iwiLA16, w, h, data)
}

// dxt1Lines is a DXT1 texture of w by h texels, opaque: every other column of texels white, from the first, on black.
func dxt1Lines(w, h int) []byte {
	var data []byte
	for i := 0; i < (w/4)*(h/4); i++ {
		data = binary.LittleEndian.AppendUint16(data, 0xFFFF)
		data = binary.LittleEndian.AppendUint16(data, 0)
		data = append(data, 0x44, 0x44, 0x44, 0x44) // each row of four: the first colour, the second, and again
	}
	return iwi(iwiDXT1, w, h, data)
}

// The players' pictures, from the game files, 128x128, each framed as its kind says: a map's is the middle square of
// its picture in the map select, which the game keeps square; an icon is whole, in its own shape, in the middle of a
// transparent square, as large as fits the round the page shows; a chalk drawing is what shows of it, in the page's
// cream, framed as an icon; a crop is the part of its texture that it says, as a calling card's square is. The server
// has those it could make.
func TestPictures(t *testing.T) {
	const red, green, blue = 0xF800, 0x07E0, 0x001F
	cream := func(alpha uint8) color.NRGBA { return color.NRGBA{0xec, 0xe7, 0xdf, alpha} }
	within := func(x, y, x0, y0, x1, y1 int) bool { return x >= x0 && x < x1 && y >= y0 && y < y1 }
	opaque := func(colour uint16) func(x, y int) (uint16, byte) {
		return func(x, y int) (uint16, byte) { return colour, 255 }
	}
	gameDir := t.TempDir()
	writeIWD(t, filepath.Join(gameDir, "main", "iw_07.iwd"), map[string][]byte{
		// Kino's picture in the map select, 64x64: a frame of nothing above and below it, green under it; the picture, of
		// 32 rows, is 24 wide to the game's 4:3, which is blue, and red at its sides
		"images/zombie_theater.iwi": dxt5(16, 16, func(x, y int) (uint16, byte) {
			switch {
			case y < 4 || y >= 12:
				return green, 0
			case x < 5 || x > 10:
				return red, 255
			}
			return blue, 255
		}),
		// Juggernog's icon, 32x8: two red squares with a gap between them, and room at either end; where it is
		// transparent, green is under it
		"images/specialty_juggernaut_zombies.iwi": dxt5(8, 2, func(x, y int) (uint16, byte) {
			if x == 1 || x == 2 || x == 5 || x == 6 {
				return red, 255
			}
			return green, 0
		}),
		"images/specialty_quickrevive_zombies.iwi": []byte("not an IWI"),
		"images/zombie_cosmo.iwi":                  dxt5(2, 2, opaque(red)),
		// the tally marks of five rounds, 32x32: as bright throughout, their alpha is the chalk, 16 wide and 8 high and a
		// third there, in a little noise
		"images/chalkmarks_5.iwi": la16(32, 32, func(x, y int) (byte, byte) {
			if within(x, y, 8, 12, 24, 20) {
				return 254, 80
			}
			return 254, 10
		}),
		// a chalk drawing, 256x128, opaque: lines a texel wide, as far apart, white on black
		"images/chalk_weapon_bowie_knife.iwi": dxt1Lines(256, 128),
		// and one of two drawings, 512x256, which two pictures are made from
		"images/chalk_weapon_explosives_c.iwi": dxt1Lines(512, 256),
		// a calling card, 256x64: the square around its texel 159 (its 127 to 190) blue but for its first column, the
		// last of the red left of it; red right of it
		"images/emblem_bg_teddy_bear.iwi": dxt5(64, 16, func(x, y int) (uint16, byte) {
			if x < 32 || x > 47 {
				return red, 255
			}
			return blue, 255
		}),
	})
	// a later .iwd has the last word
	writeIWD(t, filepath.Join(gameDir, "main", "iw_25.iwd"), map[string][]byte{
		"images/hud_thundergun.iwi": dxt5(1, 1, opaque(green)),
		"images/zombie_cosmo.iwi":   dxt5(2, 2, opaque(blue)),
	})
	h := &web{gameDir: gameDir}
	srv := httptest.NewServer(h)
	defer srv.Close()
	picture := func(id string) func(x, y int) color.NRGBA {
		t.Helper()
		rsp, body := get(t, srv.URL+"/pictures/"+id+".png")
		img, err := png.Decode(bytes.NewReader([]byte(body)))
		if err != nil || rsp.Header.Get("Content-Type") != "image/png" || rsp.Header.Get("Cache-Control") != "max-age=86400" ||
			rsp.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" || img.Bounds() != image.Rect(0, 0, 128, 128) {
			t.Fatalf("the picture %s: %s %s, %v %v", id, rsp.Status, rsp.Header, err, img)
		}
		return func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) }
	}

	// a map's: the middle square of the 4:3 picture, so none of its sides, nor of the frame
	at := picture("zombie_theater")
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			if c := at(x, y); c != (color.NRGBA{0, 0, 255, 255}) {
				t.Fatalf("Kino's picture at %d, %d: %v, want the blue of its middle", x, y, c)
			}
		}
	}
	// from the last .iwd that has it; its id is the map's name, not its image's
	if c := picture("zombie_cosmodrome")(64, 64); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("Ascension's picture: %v, want the last .iwd's (blue)", c)
	}

	// an icon: what shows of it (24x8, without the room at its ends), as large as fits the round, which for one three
	// times as long as high is 112 long, in its own shape (112x37), in the middle of a transparent square
	at = picture("juggernog")
	for _, p := range []image.Point{{8, 45}, {8, 81}, {30, 64}, {119, 64}} {
		if c := at(p.X, p.Y); c != (color.NRGBA{255, 0, 0, 255}) {
			t.Errorf("Juggernog's icon at %v: %v, want its red", p, c)
		}
	}
	for _, p := range []image.Point{{0, 0}, {7, 64}, {30, 44}, {30, 82}, {64, 64}, {120, 64}} {
		if c := at(p.X, p.Y); c.A != 0 {
			t.Errorf("Juggernog's icon at %v: %v, want nothing", p, c)
		}
	}
	// where it fades out, into its gap, it is the red still: none of the green the texture has where it is transparent
	if c := at(45, 64); c.A == 0 || c.A == 255 {
		t.Errorf("Juggernog's icon at its red's edge: %v, want it fading out", c)
	}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			if c := at(x, y); c.A != 0 && (c.R != 255 || c.G != 0 || c.B != 0) {
				t.Fatalf("Juggernog's icon at %d, %d: %v, want red alone", x, y, c)
			}
		}
	}
	if c := picture("thundergun")(64, 64); c != (color.NRGBA{0, 255, 0, 255}) {
		t.Errorf("the Thundergun: %v", c)
	}

	// chalk: there as it is bright and opaque both (254 x 80 / 255), in the page's cream, and three times as strong as
	// that; what shows of the tally marks (16x8) as large as fits, which for one twice as long as high is 110 long
	// (110x55), in the middle of a transparent square, and none of the noise around them
	at = picture("tally5")
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			want := color.NRGBA{}
			if within(x, y, 9, 36, 119, 91) {
				want = cream(3 * 79)
			}
			if c := at(x, y); c != want && (c.A != 0 || want.A != 0) {
				t.Fatalf("the tally marks at %d, %d: %v, want %v", x, y, c, want)
			}
		}
	}
	// a chalk drawing, there where it is bright. Shrunk to less than half (255x128 of it to 110x55), each pixel has the
	// average of the lines and the black it covers, about half, and none falls between two lines; chalk being three
	// times as strong as that, it is all there
	at = picture("bowie")
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			want := color.NRGBA{}
			if within(x, y, 9, 36, 119, 91) {
				want = cream(255)
			}
			if c := at(x, y); c != want && (c.A != 0 || want.A != 0) {
				t.Fatalf("the Bowie knife's drawing at %d, %d: %v, want %v", x, y, c, want)
			}
		}
	}
	// a calling card: the square of the strip that its picture says, each texel two pixels wide
	at = picture("teddy")
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			want := color.NRGBA{0, 0, 255, 255}
			if x == 0 {
				want = color.NRGBA{255, 0, 0, 255}
			}
			if c := at(x, y); c != want && x != 1 && x != 2 {
				t.Fatalf("the teddy bear's card at %d, %d: %v, want %v", x, y, c, want)
			}
		}
	}

	// one the game files lack, one they have that is no picture, and what the set has none of
	for _, p := range []string{"/pictures/speedcola.png", "/pictures/quickrevive.png", "/pictures/nosuch.png",
		"/pictures/juggernog", "/pictures/juggernog.png/", "/pictures/specialty_juggernaut_zombies.png",
		"/pictures/zombie_cosmo.png", "/pictures/../images/zombie_theater.png", "/pictures/"} {
		if rsp, _ := get(t, srv.URL+p); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", p, rsp.Status)
		}
	}
	// which the server has, in the set's order
	var want []string
	for _, p := range pictureSet {
		if slices.Contains([]string{"zombie_theater", "zombie_cosmodrome", "juggernog", "thundergun", "tally5", "bowie",
			"claymore", "grenades", "teddy"}, p.id) {
			want = append(want, p.id)
		}
	}
	if got := h.playerPictures(); !slices.Equal(got, want) {
		t.Errorf("the pictures the server has: %v, want %v", got, want)
	}
	// made all at once, as the server has them made as it starts, each texture read once for all of its pictures, they
	// are as they are made one by one, as a page asks for them
	atOnce, oneByOne := &web{gameDir: gameDir}, &web{gameDir: gameDir}
	if got := atOnce.playerPictures(); !slices.Equal(got, want) {
		t.Errorf("the pictures a server has, made at once: %v, want %v", got, want)
	}
	for _, id := range want {
		one, ok := oneByOne.playerPicture(id)
		all, had := atOnce.playerPicture(id)
		if !ok || !had || len(one.data) == 0 || !bytes.Equal(one.data, all.data) {
			t.Errorf("the picture %s, made at once (%v, %d bytes) and alone (%v, %d bytes)", id, had, len(all.data), ok,
				len(one.data))
		}
	}
	if got := (&web{gameDir: t.TempDir()}).playerPictures(); got == nil || len(got) != 0 {
		t.Errorf("the pictures of a server without game files: %#v", got)
	}
}

// The set: the maps' pictures in the map select first, in the page's order; each picture with an id of its own, a name,
// a kind and a group, a group's pictures together. Its ids are what the database keeps.
func TestPictureSet(t *testing.T) {
	i := 0
	for _, m := range mapTitles {
		if m.of != t5 {
			continue // T6's maps have no picture the server reads
		}
		if p := pictureSet[i]; p != (picture{group: "Maps", id: m.name, name: m.title, image: m.preview, kind: "map"}) {
			t.Errorf("picture %d: %+v, want %s's", i, p, m.name)
		}
		i++
	}
	kinds := []string{"map", "crop", "icon", "shield", "chalk"}
	parts := []string{"packapunch", "power", "mysterybox", "element115", "hut", "claymore", "grenades"}
	seen, groups, order := map[string]bool{}, map[string]bool{}, []string{}
	for i, p := range pictureSet {
		if seen[p.id] || p.id == "" || p.name == "" || p.group == "" || p.image == "" || p.image != strings.ToLower(p.image) ||
			!slices.Contains(kinds, p.kind) || p.part != p.part.Canon() || p.part.Min.X < 0 || p.part.Min.Y < 0 ||
			p.part.Empty() != (p.kind != "crop" && !slices.Contains(parts, p.id)) {
			t.Errorf("picture %d: %+v", i, p)
		}
		if groups[p.group] && pictureSet[i-1].group != p.group {
			t.Errorf("picture %d, %s, is apart from the others of its group, %s", i, p.id, p.group)
		}
		if !groups[p.group] {
			order = append(order, p.group)
		}
		seen[p.id], groups[p.group] = true, true
		if got, ok := pictureByID(p.id); !ok || got != p {
			t.Errorf("picture %d by its id, %s: %+v", i, p.id, got)
		}
	}
	if want := []string{"Maps", "Characters", "Zombies", "Comics", "Perks", "Power-ups", "Items", "Weapons",
		"Wall weapons", "Chalk"}; !slices.Equal(order, want) {
		t.Errorf("the groups: %v, want %v", order, want)
	}
	for _, want := range slices.Concat(
		// a perk's icon and a power-up's are shields
		group("Perks", picture{id: "juggernog", name: "Juggernog", image: "specialty_juggernaut_zombies", kind: "shield"},
			picture{id: "jugglabel", name: "Juggernog Label", image: "~-gzombie_perkbottle_jugg_c", kind: "crop",
				part: image.Rect(113, 235, 265, 405)}),
		group("Power-ups", shield("bullets", "Bullets", "zom_icon_bullets")),
		group("Weapons", icon("crossbow", "Crossbow", "hud_crossbow"),
			icon("monkeybomb", "Monkey Bomb", "hud_cymbal_monkey")),
		// with a card's square, as high as its strip
		group("Items", icon("stopwatch", "Stopwatch", "zombie_stopwatch"), icon("revive", "Revive", "waypoint_revive"),
			picture{id: "teddy", name: "Teddy Bear", image: "emblem_bg_teddy_bear", kind: "crop",
				part: image.Rect(127, 0, 191, 64)}),
		group("Chalk", chalk("tally5", "Round 5", "chalkmarks_5"), picture{id: "power", name: "Power",
			image: "chalk_writing_powerpack", kind: "chalk", part: image.Rect(0, 256, 512, 512)}),
		group("Wall weapons", chalk("bowie", "Bowie Knife", "chalk_weapon_bowie_knife"),
			chalk("olympia", "Olympia", "chalk_weapon_rottweil72_c"), picture{id: "claymore", name: "Claymore",
				image: "chalk_weapon_explosives_c", kind: "chalk", part: image.Rect(0, 0, 256, 256)}),
		// a part of a map's picture, 3 texels wide for every 4 high; a part of a loading screen, 9 for every 16
		group("Zombies", picture{id: "nachtzombie", name: "Nacht Zombie", image: "loadscreen_zombie_prototype_mini",
			kind: "crop", part: image.Rect(41, 10, 131, 130)}),
		group("Characters", picture{id: "richtofen", name: "Richtofen", image: "loadscreen_zombie_sumpf", kind: "crop",
			part: image.Rect(127, 298, 250, 518)}, onScreen("romero", "George A. Romero", "coast", 266, 273, 250)),
		group("Comics", picture{id: "eclipse", name: "Eclipse", image: "loadscreen_zombie_temple", kind: "crop",
			part: image.Rect(624, 69, 732, 262)}, onScreen("crew", "The Crew", "theater", 291, 719, 171),
			onScreen("audience", "The Audience", "prototype", 288, 694, 171)),
	) {
		if p, _ := pictureByID(want.id); p != want {
			t.Errorf("the picture %s: %+v, want %+v", want.id, p, want)
		}
	}
	// none by its image's name, and none of those the set has no more: what is not of Zombies (Dead Ops Arcade, the
	// emblems, the other calling cards), and what a wall sells but as its chalk has it
	for _, id := range []string{"hud_thundergun", "zombietron", "molotov", "coffins", "nuke", "skull", "zombie", "skull1",
		"crescent", "card1", "greeneye", "redskulls", "frag", "semtex", "chalkm14", "trenchguns", "stencil935",
		"mulekicklabel"} {
		if _, ok := pictureByID(id); ok {
			t.Errorf("the set has %s", id)
		}
	}
	for _, id := range []string{"m14", "mp40", "kar98k", "trenchgun", "betty", "claymore"} {
		if p, _ := pictureByID(id); p.kind != "chalk" || p.group != "Wall weapons" {
			t.Errorf("the picture %s: %+v, want a wall's chalk", id, p)
		}
	}
}

// A crop is the part of its texture that it says, which fills the picture, whatever its shape; a picture of another
// kind that says a part is made from that part alone. A part reaches no further than the texture does, and one the
// texture has none of is all of it.
func TestPicturesCropped(t *testing.T) {
	// 16x8, each texel of its own colour, and opaque
	texture := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			texture.SetNRGBA(x, y, color.NRGBA{uint8(x * 16), uint8(y * 32), 0, 255})
		}
	}
	texel := func(c color.NRGBA) image.Point { return image.Pt(int(c.R)/16, int(c.G)/32) }
	for _, c := range []struct{ part, want image.Rectangle }{
		{image.Rect(4, 2, 8, 6), image.Rect(4, 2, 8, 6)},
		{image.Rect(3, 0, 6, 4), image.Rect(3, 0, 6, 4)}, // 3 wide for 4 high, as a square of a map's picture is
		{image.Rect(12, 4, 20, 12), image.Rect(12, 4, 16, 8)},
		{image.Rect(16, 0, 20, 4), image.Rect(0, 0, 16, 8)},
	} {
		out := framed(texture, crop("crop", "Crop", "texture", c.part))
		first, last := texel(out.NRGBAAt(0, 0)), texel(out.NRGBAAt(127, 127))
		if out.Bounds() != image.Rect(0, 0, 128, 128) || first != c.want.Min || last != c.want.Max.Sub(image.Pt(1, 1)) {
			t.Errorf("a crop of %v: its texels %v to %v, want those of %v", c.part, first, last, c.want)
		}
	}
	// a card's square is as high as its strip, around the texel of its width that it says; a square of a picture the
	// game keeps squeezed is as much narrower than high
	if p := card("card", "Card", "strip", 100); p.kind != "crop" || p.part != image.Rect(68, 0, 132, 64) {
		t.Errorf("a card around its texel 100: %+v", p)
	}
	if p := inMap("zombie", "Zombie", "map", 100, 60, 80); p.kind != "crop" || p.part != image.Rect(70, 20, 130, 100) {
		t.Errorf("80 texels of a map's picture around 100, 60: %+v", p)
	}
	if p := onScreen("face", "Face", "sumpf", 500, 400, 160); p.kind != "crop" || p.image != "loadscreen_zombie_sumpf" ||
		p.part != image.Rect(455, 320, 545, 480) {
		t.Errorf("160 texels of a loading screen around 500, 400: %+v", p)
	}

	// two drawings in a texture of 32x16, opaque: one of 4x8 in its left half, one of 8x4 in its right. The chalk of
	// each half is its own drawing, as large as fits (55x110, 110x55), and none of the other's
	drawings := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			c := color.NRGBA{0, 0, 0, 255}
			if x >= 6 && x < 10 && y >= 4 && y < 12 || x >= 20 && x < 28 && y >= 2 && y < 6 {
				c = color.NRGBA{255, 255, 255, 255}
			}
			drawings.SetNRGBA(x, y, c)
		}
	}
	for _, c := range []struct{ part, want image.Rectangle }{
		{image.Rect(0, 0, 16, 16), image.Rect(36, 9, 91, 119)},
		{image.Rect(16, 0, 32, 16), image.Rect(9, 36, 119, 91)},
	} {
		out := framed(drawings, chalk("half", "Half", "drawings").of(c.part))
		for y := 0; y < 128; y++ {
			for x := 0; x < 128; x++ {
				if a := out.NRGBAAt(x, y).A; (a == 255) != image.Pt(x, y).In(c.want) || a != 0 && a != 255 {
					t.Fatalf("the chalk of %v at %d, %d: %d of 255, want it all in %v, and nothing around it", c.part, x, y, a,
						c.want)
				}
			}
		}
	}
}

// A shield is an icon in the middle of the square by its weight, not by its box: where it would balance is the square's
// middle, where an icon of its shape has it well above. It is the icon's picture, lower, its sides where the icon's
// are. One that is the same above its middle as below is where the icon is.
func TestPicturesShield(t *testing.T) {
	// 32x32, opaque: square for its top 20 rows, then narrowing to a point at its foot; nothing either side of that
	texture := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		half := 16
		if y >= 20 {
			half = 16 * (32 - y) / 12
		}
		for x := 16 - half; x < 16+half; x++ {
			texture.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 255})
		}
	}
	anIcon, aShield := icon("icon", "Icon", "texture"), shield("shield", "Shield", "texture")
	asIcon, asShield := framed(texture, anIcon), framed(texture, aShield)
	box, lower := shown(asIcon, asIcon.Bounds()), shown(asShield, asShield.Bounds())
	if x, y := weightOf(asIcon); box != image.Rect(16, 16, 112, 112) || math.Abs(x-64) > 0.5 || y > 58 {
		t.Fatalf("the shape as an icon: %v, its weight's middle at %.1f, %.1f, want its box in the middle and its weight "+
			"above it", box, x, y)
	}
	if x, y := weightOf(asShield); lower.Min.X != 16 || lower.Size() != box.Size() || math.Abs(x-64) > 0.5 ||
		math.Abs(y-64) > 0.5 {
		t.Errorf("the shape as a shield: %v, its weight's middle at %.1f, %.1f, want that in the middle of the square, and "+
			"its sides where the icon's are", lower, x, y)
	}
	down := lower.Min.Y - box.Min.Y
	for y := 0; y < 128-down; y++ {
		for x := 0; x < 128; x++ {
			if asShield.NRGBAAt(x, y+down) != asIcon.NRGBAAt(x, y) {
				t.Fatalf("the shield at %d, %d is not the icon at %d, %d: %v, %v", x, y+down, x, y, asShield.NRGBAAt(x, y+down),
					asIcon.NRGBAAt(x, y))
			}
		}
	}
	// 32x16, all of it opaque, and one of a single texel: as an icon is
	for _, size := range []image.Point{{32, 16}, {1, 1}} {
		same := image.NewNRGBA(image.Rectangle{Max: size})
		for i := 3; i < len(same.Pix); i += 4 {
			same.Pix[i] = 255
		}
		if !bytes.Equal(framed(same, aShield).Pix, framed(same, anIcon).Pix) {
			t.Errorf("a shield of %v that is the same either way up is not where the icon is", size)
		}
	}
	// the middle of a picture's weight, from its corner: of nothing, the picture's middle
	spots := image.NewNRGBA(image.Rect(10, 10, 30, 40))
	if x, y := weightOf(spots); x != 10 || y != 15 {
		t.Errorf("the weight of nothing in 20x30: at %v, %v, want its middle", x, y)
	}
	// of one pixel, that pixel's middle; of two, halfway between them; of three, a third of the way from the two to the
	// third; and nearer the others than one that is a third there
	for _, c := range []struct {
		at    image.Point
		there uint8
		x, y  float64
	}{{image.Pt(12, 13), 255, 2.5, 3.5}, {image.Pt(27, 13), 255, 10, 3.5}, {image.Pt(12, 33), 255, 7.5, 10.17},
		{image.Pt(27, 13), 85, 4.64, 12.07}} {
		spots.SetNRGBA(c.at.X, c.at.Y, color.NRGBA{255, 255, 255, c.there})
		if x, y := weightOf(spots); math.Abs(x-c.x) > 0.01 || math.Abs(y-c.y) > 0.01 {
			t.Errorf("the weight's middle, with %v %d there: at %.2f, %.2f, want %.2f, %.2f", c.at, c.there, x, y, c.x, c.y)
		}
	}
}

// A picture shrunk to less than half along a side has, in each pixel, the average of the texels it covers along it; the
// rest is resize's. Its colours are weighed by their alpha.
func TestPicturesScaled(t *testing.T) {
	// 8x2: a column of white, then three of nothing, green under it, and again
	in := image.NewNRGBA(image.Rect(0, 0, 8, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			in.SetNRGBA(x, y, color.NRGBA{0, 255, 0, 0})
			if x%4 == 0 {
				in.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
			}
		}
	}
	// to 2x2: each pixel covers a white texel of four, so a quarter is there, and it is white
	out := scaled(in, in.Bounds(), 2, 2)
	for i := 0; i < len(out.Pix); i += 4 {
		if c := (color.NRGBA{out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3]}); out.Bounds() != image.Rect(0, 0, 2, 2) ||
			c != (color.NRGBA{255, 255, 255, 64}) {
			t.Fatalf("8x2 to 2x2, pixel %d of %v: %v", i/4, out.Bounds(), c)
		}
	}
	// a part of it alone: the second white column and the nothing after it, to a pixel
	if out = scaled(in, image.Rect(4, 0, 7, 2), 1, 1); out.NRGBAAt(0, 0) != (color.NRGBA{255, 255, 255, 85}) {
		t.Errorf("3x2 of it to 1x1: %v", out.NRGBAAt(0, 0))
	}
	// to no less than half, it is as resize has it; and as it is at its own size
	if out = scaled(in, in.Bounds(), 4, 2); out.NRGBAAt(0, 0) != (color.NRGBA{255, 255, 255, 128}) || out.NRGBAAt(1, 0).A != 0 {
		t.Errorf("8x2 to 4x2: %v %v", out.NRGBAAt(0, 0), out.NRGBAAt(1, 0))
	}
	if out = scaled(in, in.Bounds(), 8, 2); out.NRGBAAt(0, 0) != (color.NRGBA{255, 255, 255, 255}) || out.NRGBAAt(1, 0).A != 0 {
		t.Errorf("8x2 at its own size: %v %v", out.NRGBAAt(0, 0), out.NRGBAAt(1, 0))
	}
}

// withPictures has the lobby have those pictures of the set, as if the server had made them.
func withPictures(l *lobby, ids ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.setPictures(ids)
}

// pictureOf is the picture of the player with that token, and whether they picked it.
func pictureOf(l *lobby, token string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.playerOf(token)
	return p.picture, p.picked
}

// The lobby gives each player a picture of those the server has: one no one has while there is any, then any of them.
// The page is told which there are, and each player's is by their name wherever it shows them.
func TestPicturesGiven(t *testing.T) {
	l := testLobby(t)
	// a server with none: no one has any
	early := l.hello("", "Early")
	v := l.viewOf(early.Token)
	if data, _ := json.Marshal(v); v.You.Avatar != "" || v.You.Picture != "" ||
		!strings.Contains(string(data), `"pictures":[]`) {
		t.Errorf("a player of a server with no pictures: %+v, %s", v.You, data)
	}
	withPictures(l, "zombie_theater", "juggernog", "revive")
	want := []pictureView{{"zombie_theater", "Kino der Toten", "Maps"}, {"juggernog", "Juggernog", "Perks"},
		{"revive", "Revive", "Items"}}
	if v = l.viewOf(early.Token); !slices.Equal(v.Pictures, want) || v.You.Avatar != "" {
		t.Errorf("the pictures the page is told of: %+v, want %+v; Early's, before they come back: %q", v.Pictures, want,
			v.You.Avatar)
	}
	// a player from before has one as they come back, and new ones as they first say hello: each one no one has yet
	l.hello(early.Token, "Early")
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	var given []string
	for _, h := range []hello{early, ann, bob} {
		id, picked := pictureOf(l, h.Token)
		if v := l.viewOf(h.Token); id == "" || picked || v.You.Avatar != "/pictures/"+id+".png" || v.You.Picture != id ||
			v.You.DiscordAvatar != "" {
			t.Errorf("%s's picture: %q, picked %v; the page is told %+v", h.Name, id, picked, v.You)
		}
		given = append(given, id)
	}
	if slices.Sort(given); !slices.Equal(given, []string{"juggernog", "revive", "zombie_theater"}) {
		t.Errorf("the first three players' pictures: %v, want each of the three once", given)
	}
	// which they keep as they come back
	anns, _ := pictureOf(l, ann.Token)
	l.hello(ann.Token, "Ann")
	if id, _ := pictureOf(l, ann.Token); id != anns {
		t.Errorf("Ann's picture as she came back: %q, was %q", id, anns)
	}

	// it is by their name in their room, in a game's report and on the leaderboards
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	bobs, _ := pictureOf(l, bob.Token)
	if m := l.viewOf(ann.Token).Room.Members[1]; m.Avatar != "/pictures/"+bobs+".png" {
		t.Errorf("Bob, in Ann's room: %+v", m)
	}
	l.mu.Lock()
	l.playerOf(bob.Token).keepRound("zombie_theater", 2, 5, "g")
	res := l.withPlayers(&gameResult{Players: []playerStats{{ID: bob.ID}, {ID: "forgotten"}}})
	l.mu.Unlock()
	if res.Players[0].Avatar != "/pictures/"+bobs+".png" || res.Players[1].Avatar != "" {
		t.Errorf("Bob's picture in a game's report: %+v", res.Players)
	}
	if _, b := getBoards(t, l, ann.Token); len(b.Rounds[0].Duo) != 1 || b.Rounds[0].Duo[0].Avatar != "/pictures/"+bobs+".png" {
		t.Errorf("Bob's picture on the leaderboards: %+v", b.Rounds[0].Duo)
	}

	// everyone has Early's now: the next two have the two no one has; each is someone's then, and those after them
	// have any of the three
	earlys, _ := pictureOf(l, early.Token)
	for _, h := range []hello{ann, bob} {
		if err := do(l, h.Token, "picture", "picture", earlys); err != "" {
			t.Fatal(err)
		}
	}
	var next []string
	for _, name := range []string{"Cy", "Dee"} {
		id, _ := pictureOf(l, l.hello("", name).Token)
		next = append(next, id)
	}
	if slices.Contains(next, earlys) || slices.Contains(next, "") || next[0] == next[1] {
		t.Errorf("the next two players' pictures: %v, want the two that aren't %s", next, earlys)
	}
	later := map[string]bool{}
	for i := 0; i < 60; i++ {
		id, picked := pictureOf(l, l.hello("", "Late").Token)
		if !slices.Contains([]string{"zombie_theater", "juggernog", "revive"}, id) || picked {
			t.Fatalf("a player's picture, each being someone's: %q, picked %v", id, picked)
		}
		later[id] = true
	}
	if len(later) != 3 {
		t.Errorf("the pictures of 60 players after each was someone's: %v, want any of the three", later)
	}

	// the server has Early's no more, which the lobby gave them and Ann picked: it is theirs still as they come back, and
	// another shows in its place, one the server gives, the same each time
	all := []string{"zombie_theater", "juggernog", "revive"}
	rest := slices.DeleteFunc(slices.Clone(all), func(id string) bool { return id == earlys })
	withPictures(l, rest...)
	for _, h := range []hello{early, ann} {
		l.hello(h.Token, h.Name)
		id, picked := pictureOf(l, h.Token)
		you := l.viewOf(h.Token).You
		if id != earlys || picked != (h.Token == ann.Token) || !slices.Contains(rest, you.Picture) ||
			you.Avatar != "/pictures/"+you.Picture+".png" || l.viewOf(h.Token).You.Picture != you.Picture {
			t.Errorf("%s's picture, the server having theirs no more: %q, picked %v, want %q still; the page is told %+v, "+
				"want one of %v", h.Name, id, picked, earlys, you, rest)
		}
	}
	// and theirs shows again once the server has it
	withPictures(l, all...)
	if you := l.viewOf(ann.Token).You; you.Picture != earlys || you.Avatar != "/pictures/"+earlys+".png" {
		t.Errorf("Ann's picture, the server having hers again: %+v, want %q", you, earlys)
	}
	// one the set has no more is no one's: its player has another as they come back, the server's to give
	l.mu.Lock()
	l.playerOf(ann.Token).picture = "nosuch"
	l.mu.Unlock()
	l.hello(ann.Token, "Ann")
	if id, picked := pictureOf(l, ann.Token); !slices.Contains(all, id) || picked {
		t.Errorf("Ann's picture, the set having hers no more: %q, picked %v, want one of %v, given", id, picked, all)
	}
}

// The server gives no one a weapon's picture, which is theirs to pick: not while the others are each someone's, and
// only on a server that has the weapons alone.
func TestPicturesGivenNoWeapon(t *testing.T) {
	for _, id := range []string{"m14", "thundergun", "monkeybomb", "bowie"} {
		if p, ok := pictureByID(id); !ok || !slices.Contains(picturesPickedOnly, p.group) {
			t.Errorf("the picture %s is in a group the server gives: %+v", id, p)
		}
	}
	l := testLobby(t)
	withPictures(l, "m14", "juggernog", "thundergun", "revive", "bowie")
	given := map[string]int{}
	for i := 0; i < 40; i++ {
		id, _ := pictureOf(l, l.hello("", "New").Token)
		if given[id]++; i == 1 && len(given) != 2 {
			t.Errorf("the first two players' pictures: %v, want one each of the two that are no weapon's", given)
		}
	}
	if len(given) != 2 || given["juggernog"] == 0 || given["revive"] == 0 {
		t.Errorf("the pictures of 40 players: %v, want Juggernog's and Revive alone", given)
	}
	// they pick one as any other
	ann := l.hello("", "Ann")
	if err := do(l, ann.Token, "picture", "picture", "m14"); err != "" {
		t.Fatal(err)
	}
	if id, picked := pictureOf(l, ann.Token); id != "m14" || !picked {
		t.Errorf("Ann's picture, having picked the M14: %q, picked %v", id, picked)
	}

	l = testLobby(t)
	withPictures(l, "m14")
	if id, _ := pictureOf(l, l.hello("", "Only").Token); id != "m14" {
		t.Errorf("a player's picture on a server with a weapon's alone: %q", id)
	}
}

// A player picks their picture among those the server has. With no avatar on Discord, there is none to go back to.
func TestPicturesPicked(t *testing.T) {
	l := testLobby(t)
	withPictures(l, "zombie_theater", "juggernog")
	ann := l.hello("", "Ann")
	given, _ := pictureOf(l, ann.Token)
	other := map[string]string{"zombie_theater": "juggernog", "juggernog": "zombie_theater"}[given]
	l.mu.Lock()
	changed := l.changed
	l.unsaved = false
	l.mu.Unlock()
	if err := do(l, ann.Token, "picture", "picture", other); err != "" {
		t.Fatalf("Ann, picking %s: %q", other, err)
	}
	select {
	case <-changed:
	default:
		t.Errorf("no event as Ann picked a picture")
	}
	l.mu.Lock()
	unsaved := l.unsaved
	l.mu.Unlock()
	if id, picked := pictureOf(l, ann.Token); id != other || !picked || !unsaved {
		t.Errorf("Ann's picture: %q, picked %v, to be saved %v", id, picked, unsaved)
	}
	if v := l.viewOf(ann.Token); v.You.Avatar != "/pictures/"+other+".png" || v.You.Picture != other {
		t.Errorf("Ann's picture, as the page is told: %+v", v.You)
	}
	// one of the set that the server lacks, one that is none, and no picture at all, with none on Discord: hers stays
	for id, want := range map[string]string{"revive": "There is no picture revive.",
		"../maps": "There is no picture ../maps.", "": "You have no avatar on Discord."} {
		if err := do(l, ann.Token, "picture", "picture", id); err != want {
			t.Errorf("Ann, picking %q: %q, want %q", id, err, want)
		}
	}
	if id, picked := pictureOf(l, ann.Token); id != other || !picked {
		t.Errorf("Ann's picture, after those she can't have: %q, picked %v", id, picked)
	}
	// the one the server gave her, picked: hers to keep
	if err := do(l, ann.Token, "picture", "picture", given); err != "" {
		t.Errorf("Ann, picking %s again: %q", given, err)
	}
	if id, picked := pictureOf(l, ann.Token); id != given || !picked {
		t.Errorf("Ann's picture: %q, picked %v", id, picked)
	}
	if err := do(l, "nobody", "picture", "picture", "juggernog"); err != errUnknown.Error() {
		t.Errorf("an unknown token, picking a picture: %q", err)
	}
}

// Where players sign in with Discord, a player's avatar there is their picture while they have one, until they pick one
// of the set, and again once they pick none. One with no avatar there has a picture of the set, as anyone.
func TestPicturesWithDiscord(t *testing.T) {
	d := newFakeDiscord(t)
	d.accounts["100"] = &fakeAccount{username: "ann", member: true, avatar: "a1b2"}
	d.accounts["200"] = &fakeAccount{username: "bob", member: true}
	l, h := discordLobby(t, d)
	writeIWD(t, filepath.Join(h.gameDir, "main", "iw_07.iwd"), map[string][]byte{
		"images/specialty_juggernaut_zombies.iwi": dxt5(1, 1, func(x, y int) (uint16, byte) { return 0xF800, 255 }),
	})
	withPictures(l, "zombie_theater", "juggernog")
	_, annCookie := signIn(t, d, h, "/", "100")
	_, bobCookie := signIn(t, d, h, "/", "200")
	ann, bob := helloWith(l, annCookie), helloWith(l, bobCookie)
	const annOnDiscord = "https://cdn.discordapp.com/avatars/100/a1b2.png?size=128"
	const bobOnDiscord = "https://cdn.discordapp.com/avatars/200/c3d4.png?size=128"

	// Ann's is her avatar there, and the server gave her none; Bob, with none there, has one of the set
	if id, picked := pictureOf(l, ann.Token); id != "" || picked {
		t.Errorf("Ann, with an avatar on Discord, was given %q (picked %v)", id, picked)
	}
	if v := l.viewOf(ann.Token); v.You.Avatar != annOnDiscord || v.You.Picture != "" || v.You.DiscordAvatar != annOnDiscord {
		t.Errorf("Ann's picture: %+v", v.You)
	}
	bobs, picked := pictureOf(l, bob.Token)
	if v := l.viewOf(bob.Token); bobs == "" || picked || v.You.Avatar != "/pictures/"+bobs+".png" || v.You.Picture != bobs ||
		v.You.DiscordAvatar != "" {
		t.Errorf("Bob, with no avatar on Discord: %q, picked %v; the page is told %+v", bobs, picked, v.You)
	}

	// Ann picks one of the set, which is hers though she has an avatar; then none, and her avatar is again
	if err := do(l, ann.Token, "picture", "picture", "juggernog"); err != "" {
		t.Errorf("Ann, picking a picture: %q", err)
	}
	if v := l.viewOf(ann.Token); v.You.Avatar != "/pictures/juggernog.png" || v.You.Picture != "juggernog" ||
		v.You.DiscordAvatar != annOnDiscord {
		t.Errorf("Ann's picture, one she picked: %+v", v.You)
	}
	// and still as she comes back, and signs in again
	_, annCookie = signIn(t, d, h, "/", "100")
	if ann = helloWith(l, annCookie); l.viewOf(ann.Token).You.Avatar != "/pictures/juggernog.png" {
		t.Errorf("Ann's picture, signed in again: %+v", l.viewOf(ann.Token).You)
	}
	if err := do(l, ann.Token, "picture", "picture", ""); err != "" {
		t.Errorf("Ann, going back to her avatar: %q", err)
	}
	if v := l.viewOf(ann.Token); v.You.Avatar != annOnDiscord || v.You.Picture != "" || v.You.DiscordAvatar != annOnDiscord {
		t.Errorf("Ann's picture, back to her avatar: %+v", v.You)
	}
	if id, picked := pictureOf(l, ann.Token); id != "" || picked {
		t.Errorf("Ann's picture of the set, back to her avatar: %q, picked %v", id, picked)
	}
	// Bob has none to go back to
	if err := do(l, bob.Token, "picture", "picture", ""); err != "You have no avatar on Discord." {
		t.Errorf("Bob, going back to an avatar he doesn't have: %q", err)
	}
	if id, _ := pictureOf(l, bob.Token); id != bobs {
		t.Errorf("Bob's picture after that: %q, was %q", id, bobs)
	}

	// Ann's avatar goes: the server gives her a picture as it looks her up. Bob has one there now, which is his picture,
	// as he picked none
	d.set("100", func(a *fakeAccount) { a.avatar = "" })
	d.set("200", func(a *fakeAccount) { a.avatar = "c3d4" })
	for _, token := range []string{ann.Token, bob.Token} {
		l.mu.Lock()
		l.playerOf(token).discord.Checked = time.Now().Add(-discordRefresh)
		l.mu.Unlock()
		if err := do(l, token, "refresh"); err != "" {
			t.Fatal(err)
		}
	}
	anns, picked := pictureOf(l, ann.Token)
	if v := l.viewOf(ann.Token); anns == "" || picked || v.You.Avatar != "/pictures/"+anns+".png" || v.You.Picture != anns ||
		v.You.DiscordAvatar != "" {
		t.Errorf("Ann, her avatar on Discord gone: %q, picked %v; the page is told %+v", anns, picked, v.You)
	}
	if v := l.viewOf(bob.Token); v.You.Avatar != bobOnDiscord || v.You.Picture != "" || v.You.DiscordAvatar != bobOnDiscord {
		t.Errorf("Bob, with an avatar on Discord now: %+v", v.You)
	}
	// which he may pick a picture over, as Ann did
	if err := do(l, bob.Token, "picture", "picture", "zombie_theater"); err != "" ||
		l.viewOf(bob.Token).You.Avatar != "/pictures/zombie_theater.png" {
		t.Errorf("Bob, picking a picture: %q, %+v", err, l.viewOf(bob.Token).You)
	}

	// the pictures are from the game files: a player's who signed in alone, and for their browser alone to keep, not a
	// cache between it and the server, which would hand them to anyone
	if rsp := getAt(h, "/pictures/juggernog.png"); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /pictures/juggernog.png, not signed in: %s", rsp.Status)
	}
	if rsp := getAt(h, "/pictures/juggernog.png", annCookie); rsp.StatusCode != http.StatusOK ||
		rsp.Header.Get("Cache-Control") != "private, max-age=86400" {
		t.Errorf("GET /pictures/juggernog.png, signed in: %s, Cache-Control %q", rsp.Status, rsp.Header.Get("Cache-Control"))
	}
	if rsp := getAt(h, "/pictures/revive.png", annCookie); rsp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /pictures/revive.png, signed in, which the game files lack: %s", rsp.Status)
	}
}

// An event stream tells its page of the pictures to pick from in its first event, and again when the server has
// others: the events between have none, and the page keeps those it was told of.
func TestPicturesToldOnce(t *testing.T) {
	l := testLobby(t)
	srv := httptest.NewServer(&web{lobby: l})
	defer srv.Close()
	withPictures(l, "juggernog", "revive")
	ann := l.hello("", "Ann")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/lobby/events", nil)
	req.Header.Set("Authorization", "Bearer "+ann.Token)
	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	events := make(chan string)
	go func() {
		for in := bufio.NewScanner(rsp.Body); in.Scan(); {
			if data, ok := strings.CutPrefix(in.Text(), "data: "); ok {
				events <- data
			}
		}
	}()
	// the next event: the ids of the pictures it tells of, and whether it tells of any
	next := func() ([]string, bool) {
		t.Helper()
		select {
		case data := <-events:
			var v struct {
				Pictures []pictureView
			}
			json.Unmarshal([]byte(data), &v)
			ids := []string{}
			for _, p := range v.Pictures {
				ids = append(ids, p.ID)
			}
			return ids, strings.Contains(data, `"pictures":`)
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
			return nil, false
		}
	}
	if ids, told := next(); !told || !slices.Equal(ids, []string{"juggernog", "revive"}) {
		t.Errorf("the pictures of the first event: %v (told %v)", ids, told)
	}
	do(l, ann.Token, "create")
	if ids, told := next(); told {
		t.Errorf("the pictures of the next event, as they were: %v", ids)
	}
	l.havePictures([]string{"juggernog"})
	if ids, told := next(); !told || !slices.Equal(ids, []string{"juggernog"}) {
		t.Errorf("the pictures of the event after the server made others: %v (told %v)", ids, told)
	}
	do(l, ann.Token, "leave")
	if ids, told := next(); told {
		t.Errorf("the pictures of the event after that, as they were: %v", ids)
	}
	// a server that has none any more says so
	l.havePictures([]string{})
	if ids, told := next(); !told || len(ids) != 0 {
		t.Errorf("the pictures of the event after the server had none: %v (told %v)", ids, told)
	}
}

// The database keeps each player's picture, and whether they picked it. As the server starts, it has no pictures until
// the server has made them: no one is shown or given any, and everyone keeps theirs. Once they are made, the pages
// hear, and everyone who needs one has one: a player the database kept from before there were pictures, one who said
// hello meanwhile. One whose picture the server has no more keeps it, and is shown another until the server has it
// again.
func TestPicturesKeptAndMadeLater(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	l := testLobby(t)
	loadFrom(t, l, file)
	cy := l.hello("", "Cy") // before there were pictures
	withPictures(l, "juggernog", "revive")
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	for _, h := range []hello{ann, bob, cy} {
		openPage(t, l, h.Token)
	}
	if err := do(l, ann.Token, "picture", "picture", "revive"); err != "" {
		t.Fatal(err)
	}
	bobs, _ := pictureOf(l, bob.Token)
	// what the database keeps of the three, by their ids
	kept := func() map[string]keptPlayer {
		t.Helper()
		if err := l.writePlayers(); err != nil {
			t.Fatal(err)
		}
		players := map[string]keptPlayer{}
		for _, kp := range dbHas(t, file).Players {
			players[kp.ID] = kp
		}
		return players
	}
	players := kept()
	if a, b, c := players[ann.ID], players[bob.ID], players[cy.ID]; a.Picture != "revive" || !a.Picked ||
		b.Picture != bobs || b.Picked || c.Picture != "" || c.Picked {
		t.Errorf("the pictures the database keeps: Ann's %q (picked %v), Bob's %q (%v), want %q, Cy's %q (%v)",
			a.Picture, a.Picked, b.Picture, b.Picked, bobs, c.Picture, c.Picked)
	}

	// the server starts again, and the lobby has no pictures until the server has made them: the page is told of none, Ann's is not
	// shown, nor can she pick one, and Dee, who is new, is given none
	l = testLobby(t)
	loadFrom(t, l, file)
	l.hello(ann.Token, "Ann")
	dee := l.hello("", "Dee")
	openPage(t, l, dee.Token)
	v := l.viewOf(ann.Token)
	if data, _ := json.Marshal(v); !strings.Contains(string(data), `"pictures":[]`) || v.You.Avatar != "" || v.You.Picture != "" {
		t.Errorf("Ann's page, before the pictures are made: %s", data)
	}
	if err := do(l, ann.Token, "picture", "picture", "juggernog"); err != "There is no picture juggernog." {
		t.Errorf("Ann, picking a picture before they are made: %q", err)
	}
	if id, picked := pictureOf(l, dee.Token); id != "" || picked {
		t.Errorf("Dee's picture, before they are made: %q, picked %v", id, picked)
	}
	// everyone keeps theirs meanwhile, in the database too
	l.mu.Lock()
	l.save()
	l.mu.Unlock()
	if players = kept(); players[ann.ID].Picture != "revive" || !players[ann.ID].Picked ||
		players[bob.ID].Picture != bobs {
		t.Errorf("the pictures the database keeps, before they are made: Ann's %+v, Bob's %+v", players[ann.ID],
			players[bob.ID])
	}

	// they are made: the pages hear, theirs are as they were, and Cy and Dee have one each, the server's to give, for the
	// database to keep
	l.mu.Lock()
	changed := l.changed
	l.unsaved = false
	l.mu.Unlock()
	l.havePictures([]string{"juggernog", "revive"})
	select {
	case <-changed:
	default:
		t.Errorf("no event as the pictures were made")
	}
	if v := l.viewOf(ann.Token); len(v.Pictures) != 2 || v.You.Avatar != "/pictures/revive.png" ||
		v.You.Picture != "revive" {
		t.Errorf("Ann's page, once the pictures are made: %+v, %+v", v.You, v.Pictures)
	}
	if id, picked := pictureOf(l, ann.Token); id != "revive" || !picked {
		t.Errorf("Ann's picture, once they are made: %q, picked %v", id, picked)
	}
	if id, picked := pictureOf(l, bob.Token); id != bobs || picked {
		t.Errorf("Bob's picture, once they are made: %q, picked %v, want %q, given", id, picked, bobs)
	}
	l.mu.Lock()
	unsaved := l.unsaved
	l.mu.Unlock()
	for _, h := range []hello{cy, dee} {
		if id, picked := pictureOf(l, h.Token); id == "" || picked || !unsaved ||
			l.viewOf(h.Token).You.Avatar != "/pictures/"+id+".png" {
			t.Errorf("%s's picture, once they are made: %q, picked %v, to be saved %v", h.Name, id, picked, unsaved)
		}
	}

	// the server starts again and has Ann's no more (an archive it couldn't read, say): another shows in its
	// place, and hers is as it was, in the database too, for when the server has it again
	if players = kept(); players[cy.ID].Picture == "" || players[dee.ID].Picture == "" {
		t.Errorf("the pictures the database keeps, once they are made: Cy's %+v, Dee's %+v", players[cy.ID],
			players[dee.ID])
	}
	l = testLobby(t)
	loadFrom(t, l, file)
	l.havePictures([]string{"juggernog", "zombie_theater"})
	if id, picked := pictureOf(l, ann.Token); id != "revive" || !picked {
		t.Errorf("Ann's picture, the server having hers no more: %q, picked %v, want hers still", id, picked)
	}
	if you := l.viewOf(ann.Token).You; you.Picture != "juggernog" && you.Picture != "zombie_theater" ||
		you.Avatar != "/pictures/"+you.Picture+".png" {
		t.Errorf("Ann's picture as it shows, the server having hers no more: %+v", you)
	}
	l.mu.Lock()
	l.save()
	l.mu.Unlock()
	if players = kept(); players[ann.ID].Picture != "revive" || !players[ann.ID].Picked {
		t.Errorf("the picture the database keeps of Ann, the server having hers no more: %+v", players[ann.ID])
	}
	l = testLobby(t)
	loadFrom(t, l, file)
	l.havePictures([]string{"juggernog", "revive"})
	if you := l.viewOf(ann.Token).You; you.Picture != "revive" || you.Avatar != "/pictures/revive.png" {
		t.Errorf("Ann's picture, the server having hers again: %+v", you)
	}
	// and with one that has none at all: no one is shown any, and she keeps hers, for when it does
	l = testLobby(t)
	loadFrom(t, l, file)
	l.havePictures([]string{})
	if id, picked := pictureOf(l, ann.Token); id != "revive" || !picked || l.viewOf(ann.Token).You.Avatar != "" {
		t.Errorf("Ann's picture, the server having none: %q, picked %v, shown as %q", id, picked, l.viewOf(ann.Token).You.Avatar)
	}
}

// The page's call to pick a picture, and what it is told of the pictures, as it reads it: by these names.
func TestPicturesAsThePageReadsThem(t *testing.T) {
	l := testLobby(t)
	srv := httptest.NewServer(&web{lobby: l})
	defer srv.Close()
	withPictures(l, "juggernog")
	// the page's calls, as it makes them
	post := func(path, token, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer rsp.Body.Close()
		var answer map[string]any
		json.NewDecoder(rsp.Body).Decode(&answer)
		return answer
	}
	token, _ := post("/lobby/hello", "", `{"name": "Ann"}`)["token"].(string)
	if answer := post("/lobby/picture", token, `{"picture": "nosuch"}`); answer["error"] != "There is no picture nosuch." {
		t.Errorf("POST /lobby/picture, of none: %v", answer)
	}
	if answer := post("/lobby/picture", token, `{"picture": "juggernog"}`); len(answer) != 0 {
		t.Errorf("POST /lobby/picture: %v", answer)
	}
	data, _ := json.Marshal(l.viewOf(token))
	var v struct {
		You      map[string]any
		Pictures []map[string]string
	}
	json.Unmarshal(data, &v)
	if v.You["avatar"] != "/pictures/juggernog.png" || v.You["picture"] != "juggernog" || v.You["discordAvatar"] != nil ||
		len(v.Pictures) != 1 || v.Pictures[0]["id"] != "juggernog" || v.Pictures[0]["name"] != "Juggernog" ||
		v.Pictures[0]["group"] != "Perks" {
		t.Errorf("the view, as the page reads it: %s", data)
	}
}
