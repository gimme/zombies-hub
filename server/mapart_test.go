package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// iwi is an IWI of one level: the header, then the blocks (or pixels) as they are.
func iwi(format byte, w, h int, data []byte) []byte {
	head := make([]byte, 48)
	copy(head, "IWi")
	head[3], head[4] = 13, format
	binary.LittleEndian.PutUint16(head[6:], uint16(w))
	binary.LittleEndian.PutUint16(head[8:], uint16(h))
	binary.LittleEndian.PutUint16(head[10:], 1)
	return append(head, data...)
}

// dxt1 is a DXT1 texture of w x h in one colour (RGB565): each block's first colour, which every texel takes.
func dxt1(w, h int, c uint16) []byte {
	var data []byte
	for i := 0; i < (w/4)*(h/4); i++ {
		data = binary.LittleEndian.AppendUint16(data, c)
		data = binary.LittleEndian.AppendUint16(data, 0)
		data = append(data, 0, 0, 0, 0) // every texel the first colour
	}
	return iwi(iwiDXT1, w, h, data)
}

func writeIWD(t *testing.T, file string, images map[string][]byte) {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, data := range images {
		w, _ := z.Create(name)
		w.Write(data)
	}
	z.Close()
	writeFile(t, file, out.String())
}

// fastfile is a fastfile whose zone holds the localized strings: each its two pointers inline, its text, its name.
func fastfile(entries ...string) string {
	var zone bytes.Buffer
	zone.WriteString("zone header")
	for i := 0; i+1 < len(entries); i += 2 {
		zone.WriteString("\xff\xff\xff\xff\xff\xff\xff\xff" + entries[i] + "\x00" + entries[i+1] + "\x00")
	}
	var out bytes.Buffer
	out.WriteString("IWffu100\xd9\x01\x00\x00")
	z := zlib.NewWriter(&out)
	z.Write(zone.Bytes())
	z.Close()
	return out.String()
}

func TestDecodeIWI(t *testing.T) {
	// DXT5: its colours (DXT1's four-colour mode, whichever is the greater) and its alpha, from two values and those
	// between them
	block := []byte{255, 0}                                   // alpha: 255, 0, and six between
	block = append(block, 0x88, 0x88, 0x88, 0x88, 0x88, 0x88) // index 0 and 1 by turns: 255, 0
	block = binary.LittleEndian.AppendUint16(block, 0x001F)   // blue, the lesser
	block = binary.LittleEndian.AppendUint16(block, 0xF800)   // red
	block = append(block, 0x01, 0, 0, 0)                      // the first texel red, the rest blue
	img, err := decodeIWI(iwi(iwiDXT5, 4, 4, block))
	if err != nil {
		t.Fatal(err)
	}
	if c := img.NRGBAAt(0, 0); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("DXT5's first texel: %v", c)
	}
	if c := img.NRGBAAt(1, 0); c != (color.NRGBA{0, 0, 255, 0}) {
		t.Errorf("DXT5's second texel: %v", c)
	}
	// DXT1 in three-colour mode: its fourth is transparent black
	block = binary.LittleEndian.AppendUint16(nil, 0x001F)
	block = binary.LittleEndian.AppendUint16(block, 0xF800)
	block = append(block, 0x03, 0, 0, 0)
	if img, err = decodeIWI(iwi(iwiDXT1, 4, 4, block)); err != nil || img.NRGBAAt(0, 0) != (color.NRGBA{}) ||
		img.NRGBAAt(1, 0) != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("DXT1's three-colour mode: %v %v, %v", img.NRGBAAt(0, 0), img.NRGBAAt(1, 0), err)
	}
	// luminance and alpha, a byte each: as they are, whichever of the two has the picture
	if img, err = decodeIWI(iwi(iwiLA16, 2, 1, []byte{200, 0, 254, 77})); err != nil || img.Bounds() != image.Rect(0, 0, 2, 1) ||
		img.NRGBAAt(0, 0) != (color.NRGBA{200, 200, 200, 0}) || img.NRGBAAt(1, 0) != (color.NRGBA{254, 254, 254, 77}) {
		t.Errorf("luminance and alpha: %v, %v", img, err)
	}
	for name, data := range map[string][]byte{"not an IWI": []byte("PNG..."), "a short one": iwi(iwiDXT1, 8, 8, nil),
		"a short one of luminance and alpha": iwi(iwiLA16, 2, 2, make([]byte, 7)),
		"another format":                     iwi(0x7F, 4, 4, make([]byte, 64))} {
		if _, err := decodeIWI(data); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
}

// boa is a streamed sound of the game's (mapart.go: boaToWAV): its header, then the data
func boa(frames, rate, channels, blockAlignPerChannel int, data []byte) []byte {
	head := make([]byte, 2096)
	binary.LittleEndian.PutUint32(head, 1)
	binary.LittleEndian.PutUint32(head[4:], uint32(frames))
	binary.LittleEndian.PutUint32(head[8:], uint32(rate))
	binary.LittleEndian.PutUint32(head[12:], uint32(channels))
	binary.LittleEndian.PutUint32(head[16:], uint32(len(head)))
	binary.LittleEndian.PutUint32(head[21:], uint32(blockAlignPerChannel))
	binary.LittleEndian.PutUint16(head[25:], uint16(len(head)))
	binary.LittleEndian.PutUint32(head[48:], uint32(len(data)))
	return append(head, data...)
}

func TestMapArt(t *testing.T) {
	gameDir := t.TempDir()
	main := filepath.Join(gameDir, "main")
	// a later .iwd has the last word
	writeIWD(t, filepath.Join(main, "iw_04.iwd"), map[string][]byte{
		"images/zombie_theater.iwi":            dxt1(8, 8, 0x001F),
		"images/loadscreen_zombie_theater.iwi": dxt1(32, 32, 0x07E0),
	})
	music := boa(1024, 48000, 2, 262, bytes.Repeat([]byte{1, 2, 3, 4}, 524*2/4))
	writeIWD(t, filepath.Join(main, "iw_19.iwd"), map[string][]byte{"sound/mus/menu/zmb/mus_zmb_main_l.wav": music})
	writeIWD(t, filepath.Join(main, "iw_07.iwd"), map[string][]byte{"images/xenon_controller_top.iwi": dxt1(16, 16, 0x001F)})
	writeIWD(t, filepath.Join(main, "iw_25.iwd"), map[string][]byte{
		"images/Zombie_Theater.iwi":               dxt1(8, 8, 0xF800),
		"images/zombie_cosmo.iwi":                 dxt1(8, 8, 0x07E0),
		"images/loadscreen_zombie_cosmodrome.iwi": dxt1(16, 16, 0xF800),
	})
	writeFile(t, filepath.Join(gameDir, "zone", "English", "en_code_post_gfx.ff"), fastfile(
		"Battle the ^1undead\nin a theatre.  ", "MENU_ZOMBIE_THEATER_DESC", "Not this one", "MENU_ZOMBIE_THEATER_DESC_2"))
	writeFile(t, filepath.Join(gameDir, "zone", "English", "en_patch.ff"), fastfile("Verr\xfcckt's", "WAW_MENU_DESC_ZOMBIE_ASYLUM"))
	writeFile(t, filepath.Join(gameDir, "zone", "French", "fr_code_post_gfx.ff"), fastfile("Combattez", "MENU_ZOMBIE_THEATER_DESC"))
	h := &web{gameDir: gameDir}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// the preview, from the last .iwd that has it, back at 4:3: a PNG
	rsp, body := get(t, srv.URL+"/maps/zombie_theater/preview")
	img, err := png.Decode(bytes.NewReader([]byte(body)))
	if err != nil || rsp.Header.Get("Content-Type") != "image/png" || img.Bounds() != image.Rect(0, 0, 10, 8) {
		t.Fatalf("Kino's preview: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	if r, g, b, _ := img.At(3, 3).RGBA(); r>>8 != 255 || g != 0 || b != 0 {
		t.Errorf("Kino's preview is not the last .iwd's (red): %v", img.At(3, 3))
	}
	if rsp.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("Kino's preview, for a cross-origin isolated page: %s", rsp.Header)
	}
	// the loading screen, back at 16:9: a JPEG
	rsp, body = get(t, srv.URL+"/maps/zombie_theater/loadscreen")
	if img, err = jpeg.Decode(bytes.NewReader([]byte(body))); err != nil || rsp.Header.Get("Content-Type") != "image/jpeg" ||
		img.Bounds() != image.Rect(0, 0, 32, 18) {
		t.Errorf("Kino's loading screen: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	// Ascension's preview is images/zombie_cosmo.iwi
	if rsp, body = get(t, srv.URL+"/maps/zombie_cosmodrome/preview"); rsp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Ascension's preview: %s %s", rsp.Status, rsp.Header.Get("Content-Type"))
	} else if img, err = png.Decode(bytes.NewReader([]byte(body))); err != nil || img.Bounds() != image.Rect(0, 0, 10, 8) {
		t.Errorf("Ascension's preview: %v %v", err, img)
	}
	// a map with no preview: its loading screen stands in (an install with that alone)
	gameDir2 := t.TempDir()
	writeIWD(t, filepath.Join(gameDir2, "main", "iw_26.iwd"), map[string][]byte{"images/loadscreen_zombie_moon.iwi": dxt1(16, 16, 0xF800)})
	h2 := &web{gameDir: gameDir2}
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	if rsp, _ = get(t, srv2.URL+"/maps/zombie_moon/preview"); rsp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("Moon's preview, with no picture of its own: %s %s", rsp.Status, rsp.Header.Get("Content-Type"))
	}
	for _, p := range []string{"/maps/zombie_moon/preview", "/maps/zombie_theater/icon", "/maps/mp_nuked/preview",
		"/maps/../main/iw_04.iwd", "/maps/zombie_theater/../preview"} {
		if rsp, _ = get(t, srv.URL+p); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", p, rsp.Status)
		}
	}

	// the Gamepad page's pictures, as they are: PNGs; no other picture, nor another path
	rsp, body = get(t, srv.URL+"/images/xenon_controller_top.png")
	if img, err = png.Decode(bytes.NewReader([]byte(body))); err != nil || rsp.Header.Get("Content-Type") != "image/png" ||
		img.Bounds() != image.Rect(0, 0, 16, 16) {
		t.Errorf("the controller's picture: %s %s, %v %v", rsp.Status, rsp.Header.Get("Content-Type"), err, img)
	}
	for _, p := range []string{"/images/xenon_stick_move.png", "/images/zombie_theater.png", "/images/xenon_controller_top",
		"/images/../maps/zombie_theater/preview.png"} {
		if rsp, _ = get(t, srv.URL+p); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", p, rsp.Status)
		}
	}

	// the menu's music: a WAV of the archive's MS-ADPCM, in blocks of 524 (262 a channel), 512 frames each
	rsp, body = get(t, srv.URL+"/music")
	if rsp.StatusCode != http.StatusOK || rsp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("the music: %s %s", rsp.Status, rsp.Header.Get("Content-Type"))
	}
	wav := []byte(body)
	if len(wav) != 12+8+50+8+4+8+1048 || string(wav[:4]) != "RIFF" || string(wav[8:16]) != "WAVEfmt " ||
		binary.LittleEndian.Uint32(wav[4:]) != uint32(len(wav)-8) || binary.LittleEndian.Uint16(wav[20:]) != 2 ||
		binary.LittleEndian.Uint16(wav[22:]) != 2 || binary.LittleEndian.Uint32(wav[24:]) != 48000 ||
		binary.LittleEndian.Uint16(wav[32:]) != 524 || binary.LittleEndian.Uint16(wav[34:]) != 4 ||
		binary.LittleEndian.Uint16(wav[38:]) != 512 || binary.LittleEndian.Uint16(wav[40:]) != 7 ||
		string(wav[70:74]) != "fact" || binary.LittleEndian.Uint32(wav[78:]) != 1024 || string(wav[82:86]) != "data" ||
		binary.LittleEndian.Uint32(wav[86:]) != 1048 || !bytes.Equal(wav[90:], music[2096:]) {
		t.Errorf("the music's WAV: %d bytes, %q", len(wav), wav[:min(len(wav), 96)])
	}
	if rsp, _ = get(t, srv2.URL+"/music"); rsp.StatusCode != http.StatusNotFound {
		t.Errorf("the music of an install without it: %s, want 404", rsp.Status)
	}
	if _, err := boaToWAV(boa(1024, 48000, 2, 262, make([]byte, 1000))); err == nil {
		t.Errorf("a sound whose data is not whole blocks made a WAV")
	}

	// descriptions, in English when there is: one line, without colour codes, Latin-1 as UTF-8
	for name, want := range map[string]string{"zombie_theater": "Battle the undead in a theatre.",
		"zombie_cod5_asylum": "Verrückt's", "zombie_moon": ""} {
		if got := h.mapDescription(name); got != want {
			t.Errorf("%s's description: %q, want %q", name, got, want)
		}
	}

	// the game's localized strings by name, for the page's options menu (GET /strings): the same text, the first
	// zone's where two have a name; names it lacks, or that are none, are left out
	rsp, body = get(t, srv.URL+"/strings?keys=MENU_ZOMBIE_THEATER_DESC,WAW_MENU_DESC_ZOMBIE_ASYLUM,MENU_MISSING,../x,")
	var texts map[string]string
	if err := json.Unmarshal([]byte(body), &texts); err != nil || rsp.Header.Get("Content-Type") != "application/json" ||
		!maps.Equal(texts, map[string]string{"MENU_ZOMBIE_THEATER_DESC": "Battle the undead in a theatre.",
			"WAW_MENU_DESC_ZOMBIE_ASYLUM": "Verrückt's"}) {
		t.Errorf("/strings: %s %s %q", rsp.Status, rsp.Header.Get("Content-Type"), body)
	}
	if _, body = get(t, srv2.URL+"/strings?keys=MENU_ZOMBIE_THEATER_DESC"); body != "{}\n" {
		t.Errorf("/strings of an install without them: %q", body)
	}
}

// A zone's localized strings: a pointer inline before an entry's is not its text's; what is no entry (a name that
// is none) is skipped.
func TestLocalizeEntries(t *testing.T) {
	ff := "\xff\xff\xff\xff\xff\xff\xff\xff"
	zone := "header\xff\xff\xff\xff" + ff + "Yes\x00MENU_YES\x00" + "junk" + ff + "not one\x00lower_case\x00" +
		ff + "No\x00MENU_NO\x00" + ff + "Again\x00MENU_YES\x00" + ff + "cut off"
	got := localizeEntries([]byte(zone))
	if want := map[string]string{"MENU_YES": "Yes", "MENU_NO": "No"}; !maps.Equal(got, want) {
		t.Errorf("localizeEntries: %q, want %q", got, want)
	}
}

// The page's fonts are the server's own files: Oswald, its display font, and Inter, its text's.
func TestFonts(t *testing.T) {
	pageDir := t.TempDir()
	writeFile(t, filepath.Join(pageDir, "oswald.woff2"), "wOF2 Oswald")
	writeFile(t, filepath.Join(pageDir, "inter.woff2"), "wOF2 Inter")
	srv := httptest.NewServer(&web{pageDir: pageDir})
	defer srv.Close()
	for name, want := range map[string]string{"oswald": "wOF2 Oswald", "inter": "wOF2 Inter"} {
		rsp, body := get(t, srv.URL+"/"+name+".woff2")
		kind, cache := rsp.Header.Get("Content-Type"), rsp.Header.Get("Cache-Control")
		if body != want || kind != "font/woff2" || cache != "max-age=86400" {
			t.Errorf("/%s.woff2: %q (%s, %s)", name, body, kind, cache)
		}
	}
	if rsp, _ := get(t, srv.URL+"/other.woff2"); rsp.StatusCode != http.StatusNotFound {
		t.Errorf("/other.woff2: %s, want 404", rsp.Status)
	}
}
