package main

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// What the page shows of each Zombies map, from the game files (title.go: mapTitles names the maps):
//
//	GET /maps/<map>/preview     the map's picture in the game's menus, as a PNG. For a map without one, the map's
//	                            loading screen is served instead. A Black Ops map's is its picture in the game's map
//	                            select: images/<map>.iwi (Ascension's is images/zombie_cosmo.iwi, the name in the map
//	                            select's table in patch.ff), or for World at War's maps
//	                            images/loadscreen_zombie_<map>_mini.iwi. It is a 4:3 picture (the game's lobby menu,
//	                            xboxlive_lobby.menu, draws it at 183x135) that the game stores square (256x256, rounded
//	                            corners). It is served at 4:3 again. A Black Ops II map's is its picture on its card
//	                            in the game's lobby, as a JPEG, without the card's frame (t6Picture). A Black Ops III
//	                            map's is its picture in the game's map select, a wide one (1280x540 or 1148x484),
//	                            as a JPEG of 640x270 (t7PreviewOf). A map of that game without one has no preview.
//	GET /maps/<map>/card        a Black Ops II map's card in the game's lobby, as a JPEG: its picture under the lobby's
//	                            frame, which has a band at its foot for the card's words (t6Card). For a map without a
//	                            picture, the map's loading screen is served instead.
//	GET /maps/<map>/loadscreen  the map's loading screen, as a JPEG: a 16:9 picture that the game stores square
//	                            (1024x1024), except Origins' (1920x1080). It is served at 16:9 again. A Black Ops map's
//	                            is images/loadscreen_zombie_<map>.iwi. A Black Ops III map's is stored at 1920x1080,
//	                            except The Giant's (2048x2048).
//
// A Black Ops map's pictures are textures in the .iwd archives in main/ (zip files). The game searches the archives
// in reverse order of their names, so a later archive (iw_25.iwd) overrides an earlier one. A Black Ops II map's are
// textures in that game's image packs (ipak.go), under the names that the map's entry gives (t6.go: t6MapList). A
// Black Ops III map's are under the names that the map's entry gives too (t7.go: t7MapList): its preview is an image
// in the zones of the game's menus (t7zone.go), and its loading screen a texture in the game's image packs
// (xpak.go). A server has those only where it has more of that game than its games read (web.t7Dir). Without them
// it has no pictures of that game's maps, and tells the page so (noMapArt). Each picture is decoded once and kept.
//
// A map's description is its text in the game's map select (for example MENU_ZOMBIE_THEATER_DESC). It comes from the
// localized strings in zone/<language>/<lang>_code_post_gfx.ff and <lang>_patch.ff, in English when the game files
// have English. The game files have the descriptions of Kino, "Five", Dead Ops Arcade, Ascension and Call of the Dead.
//
// The music that the page plays (serveMusic):
//
//	GET /music  the Zombies menu's music, which the game loops in its Zombies lobby menu, as a WAV
//	            (WAVE_FORMAT_ADPCM) for the page to decode (web/src/music.ts: music). The music is
//	            sound/mus/menu/zmb/mus_zmb_main_l.wav in the archives: MS-ADPCM in the game's own container, which
//	            FFmpeg calls "boa" (boaToWAV). The answer is a 404 when the game files lack it.

// mapArtKinds are the kinds of picture that GET /maps/<map>/<kind> serves. A card is a Black Ops II map's alone.
var mapArtKinds = map[string]bool{"preview": true, "loadscreen": true, "card": true}

type mapPicture struct {
	data        []byte
	contentType string
}

// kept returns what the server made from the game files and keeps under key (web.pictures). The first call makes it
// by calling from, with h.art held. kept returns false when the game files lack the source (os.ErrNotExist) or when
// it can't be made. In the second case it logs the error, with name for what could not be made.
func (h *web) kept(key, name string, from func() (mapPicture, error)) (mapPicture, bool) {
	h.art.Lock()
	defer h.art.Unlock()
	if pic, ok := h.pictures[key]; ok {
		return pic, pic.data != nil
	}
	if h.pictures == nil {
		h.pictures = map[string]mapPicture{}
	}
	pic, err := from()
	if err != nil {
		pic = mapPicture{}
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("server: %s: %v", name, err)
		}
	}
	h.pictures[key] = pic
	return pic, pic.data != nil
}

// serveKept sends something that kept made (a picture, the music). A browser keeps it, but asks for it again before
// each use (no-cache): its ETag is the hash of the contents, so the browser gets a 304 while its copy is current. It
// gets the new contents as soon as they change: when a new server makes the picture otherwise, or the game files
// change. (Kept for a day without asking, a map's picture that a new server made otherwise showed as it was before,
// in a shape that the page no longer gave it.) When players sign in with Discord and the path is only for players
// who are signed in (gated), only the player's own browser may keep it (private). A cache between the browser and the
// server, such as a CDN's, caches a .png unasked and would otherwise serve it to anyone.
func (h *web) serveKept(w http.ResponseWriter, r *http.Request, pic mapPicture) {
	cache := "no-cache"
	if gated(r.URL.Path) && h.lobby != nil && h.lobby.discord != nil {
		cache = "private, " + cache
	}
	sum := sha256.Sum256(pic.data)
	w.Header().Set("Content-Type", pic.contentType)
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("ETag", `"`+contentHash(sum[:])+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(pic.data))
}

// pngOf encodes the image as a PNG.
func pngOf(img image.Image) (mapPicture, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return mapPicture{}, err
	}
	return mapPicture{out.Bytes(), "image/png"}, nil
}

// mapArt returns a map's picture of the given kind, encoded for a browser. The picture is made once and kept. mapArt
// returns false when the game files have no such picture. The pictures have their own lock (h.art). The lock of the
// game files (h.mu) is held only to look the files up, so that requests for the game files don't wait for a picture.
// The lobby's view of the maps doesn't wait for one either: the descriptions it needs have their own lock (h.locs).
func (h *web) mapArt(mapName, kind string) (mapPicture, bool) {
	m, ok := mapByName(mapName)
	if _, known := mapArtKinds[kind]; !ok || !known || kind == "card" && m.of != t6 || h.noMapArt(mapName) {
		return mapPicture{}, false
	}
	// A Black Ops III map's preview is kept with those of that game's other maps, apart from the other pictures.
	if m.of == t7 && kind == "preview" {
		return h.t7Preview(m.preview)
	}
	h.art.Lock()
	defer h.art.Unlock()
	key := mapName + "/" + kind
	if pic, ok := h.pictures[key]; ok {
		return pic, pic.data != nil
	}
	if h.pictures == nil {
		h.pictures = map[string]mapPicture{}
	}
	pic, err := h.encodeMapArt(m, kind)
	if err != nil {
		log.Printf("server: %s's %s: %v", mapName, kind, err)
	}
	h.pictures[key] = pic
	return pic, pic.data != nil
}

// noMapArt reports whether the server has no pictures of a map, whatever its game files have: a Black Ops III map,
// on a server without the zones of that game's menus (web.t7Dir). The lobby tells the page, which then asks for none
// (mapView.NoPictures).
func (h *web) noMapArt(mapName string) bool {
	return titleOf(mapName) == t7 && h.t7Dir == ""
}

// encodeMapArt reads a map's picture from the game files and encodes it. A Black Ops map's preview becomes a PNG,
// because its corners are transparent. A loading screen, a Black Ops II map's picture and card, and a Black Ops III
// map's preview become a JPEG. The caller holds h.art.
func (h *web) encodeMapArt(m mapEntry, kind string) (mapPicture, error) {
	if m.of == t7 {
		img, err := h.t7Image(m.loadscreen)
		if err != nil {
			return mapPicture{}, err
		}
		return jpegOf(t7Screen(img))
	}
	texture := h.iwdImage
	if m.of == t6 {
		texture = h.t6Image
	}
	if kind == "preview" || kind == "card" {
		if img, err := texture(m.preview); err == nil && kind == "card" {
			frame, err := h.t6Image(t6CardFrame)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return mapPicture{}, err
			}
			return jpegOf(t6Card(img, frame))
		} else if err == nil && m.of == t6 {
			return jpegOf(t6Picture(img))
		} else if err == nil {
			// the game draws it at 4:3 and stores it square
			return pngOf(resize(img, img.Bounds().Dy()*4/3, img.Bounds().Dy()))
		} else if !errors.Is(err, os.ErrNotExist) {
			return mapPicture{}, err
		}
	}
	img, err := texture(m.loadscreen)
	if err != nil {
		return mapPicture{}, err
	}
	// the game draws it over the whole screen, so it is a 16:9 picture
	return jpegOf(resize(img, img.Bounds().Dx(), img.Bounds().Dx()*9/16))
}

// jpegOf encodes the image as a JPEG.
func jpegOf(img image.Image) (mapPicture, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return mapPicture{}, err
	}
	return mapPicture{out.Bytes(), "image/jpeg"}, nil
}

// A Black Ops II map's card in the game's lobby (t6Card): its size, and the height of its picture at its top.
// t6CardFrame is the texture of the lobby's frame, which the game draws over each card.
const (
	t6CardWidth, t6CardHeight, t6CardTile = 480, 290, 222
	t6CardFrame                           = "menu_zm_map_frame"
)

// t6Picture returns a Black Ops II map's picture as the game draws it on the map's card in its lobby, which is the
// map's preview (above). The picture is the map's tile, a texture of 256x256, which the game draws across the card's
// width, from its top (t6Card). The game draws it opaque, though its texture has a frame of its own in its alpha,
// which fades out (the leaderboards draw that).
func t6Picture(tile *image.NRGBA) *image.NRGBA {
	opaque := image.NewNRGBA(tile.Bounds())
	copy(opaque.Pix, tile.Pix)
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255
	}
	return scaled(opaque, opaque.Bounds(), t6CardWidth, t6CardTile)
}

// t6Card returns a Black Ops II map's card in the game's lobby (above): the map's picture (t6Picture), and the
// lobby's frame over the whole card, a texture of 256x256. The frame is a metal border, grime and the cracks of a
// bullet hole, and a dark band at the card's foot, under the picture. The lobby writes the map's place and name in the
// band, and so does the page (web/src/maps.ts: mapShot). They are drawn as in a screenshot of the lobby, where the
// card is 480x290 and the picture 480x222. Without a frame (frame is nil), the card has the picture alone, on black.
func t6Card(tile, frame *image.NRGBA) *image.NRGBA {
	card := image.NewNRGBA(image.Rect(0, 0, t6CardWidth, t6CardHeight))
	for i := 3; i < len(card.Pix); i += 4 {
		card.Pix[i] = 255 // black
	}
	copy(card.Pix, t6Picture(tile).Pix)
	if frame == nil {
		return card
	}
	over := scaled(frame, frame.Bounds(), t6CardWidth, t6CardHeight)
	for i := 0; i < len(card.Pix); i += 4 {
		a := int(over.Pix[i+3])
		for c := 0; c < 3; c++ {
			card.Pix[i+c] = uint8((int(over.Pix[i+c])*a + int(card.Pix[i+c])*(255-a) + 127) / 255)
		}
	}
	return card
}

// t7Screen returns a Black Ops III map's loading screen as the game draws it, from its texture, which it changes. The
// game draws it opaque, whatever alpha its texture has (BC7 gives the texels of many blocks 254). It draws it over the
// whole screen, so it is a 16:9 picture, though the game stores The Giant's square.
func t7Screen(img *image.NRGBA) *image.NRGBA {
	opaque(img)
	return resize(img, img.Bounds().Dx(), img.Bounds().Dx()*9/16)
}

// opaque makes every texel of the picture opaque, and returns the picture.
func opaque(img *image.NRGBA) *image.NRGBA {
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

// iwdImage returns the decoded texture images/<name>.iwi from the last .iwd in main/ that has it. It returns
// os.ErrNotExist when no archive has it. The caller holds h.art.
func (h *web) iwdImage(name string) (*image.NRGBA, error) {
	data, err := h.iwdEntry("images/" + strings.ToLower(name) + ".iwi")
	if err != nil {
		return nil, err
	}
	return decodeIWI(data)
}

// menuMusic is the path in the archives of the music that the page plays (serveMusic).
const menuMusic = "sound/mus/menu/zmb/mus_zmb_main_l.wav"

// menuImages are the pictures of the game's Gamepad page (options_gamepad, in code_post_gfx.ff). The page's rebuild
// of the Gamepad page draws them (web/src/settings.ts): the controller seen from above, under the labels of each button
// layout, and over the controller the arrows of the sticks for each stick layout.
//
//	GET /images/<name>.png  one of them: images/<name>.iwi in the archives, as a PNG (they have alpha).
var menuImages = map[string]bool{"xenon_controller_top": true, "xenon_stick_move": true, "xenon_stick_turn": true,
	"xenon_stick_move_turn": true, "xenon_stick_move_look": true}

// menuImage returns the menu's picture of the given name (above) as a PNG. The picture is made once and kept.
// menuImage returns false when the game files lack it.
func (h *web) menuImage(name string) (mapPicture, bool) {
	if !menuImages[name] {
		return mapPicture{}, false
	}
	return h.kept("image/"+name, "the menu's picture "+name, func() (mapPicture, error) {
		img, err := h.iwdImage(name)
		if err != nil {
			return mapPicture{}, err
		}
		return pngOf(img)
	})
}

// serveMenuImage answers GET /images/<name>.png (above), or with a 404.
func (h *web) serveMenuImage(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), ".png")
	pic, found := h.menuImage(name)
	if !ok || !found {
		http.NotFound(w, r)
		return
	}
	h.serveKept(w, r, pic)
}

// iwdEntry returns the contents of the entry of the given lowercase name, from the last .iwd in main/ that has it.
// It knows only the entries that findEntries looks for. It returns os.ErrNotExist when no archive has the entry. The
// caller holds h.art.
func (h *web) iwdEntry(want string) ([]byte, error) {
	if h.images == nil {
		h.images = h.findEntries()
	}
	file, ok := h.images[want]
	if !ok {
		return nil, fmt.Errorf("%s: %w", want, os.ErrNotExist)
	}
	z, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.ToLower(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 64<<20))
	}
	return nil, fmt.Errorf("%s: %w", want, os.ErrNotExist)
}

// findEntries finds the entries that the server reads in the .iwd archives in main/: the pictures of Black Ops' maps,
// the menus' pictures, the players' pictures (pictures.go), the menu's music, and the menus' sounds that the game
// streams. For the sounds it takes all of menuSoundDir, because only the zone names the ones the page plays
// (sounds.go). It returns a map from each entry's lowercase path in its archive to the last archive by name that has
// the entry. It is called once.
func (h *web) findEntries() map[string]string {
	wanted := map[string]bool{menuMusic: true}
	for _, m := range mapTitles {
		if m.of == t5 {
			wanted["images/"+m.preview+".iwi"], wanted["images/"+m.loadscreen+".iwi"] = true, true
		}
	}
	for name := range menuImages {
		wanted["images/"+name+".iwi"] = true
	}
	for _, p := range pictureSet {
		wanted["images/"+p.image+".iwi"] = true
	}
	var iwds []string
	for _, rel := range h.paths() {
		if strings.HasPrefix(rel, "main/") && strings.HasSuffix(rel, ".iwd") && !strings.Contains(rel[5:], "/") {
			iwds = append(iwds, rel)
		}
	}
	slices.Sort(iwds)
	entries := map[string]string{}
	for _, rel := range iwds {
		e, ok := h.lookup(rel)
		if !ok {
			continue
		}
		z, err := zip.OpenReader(e.file)
		if err != nil {
			log.Printf("server: %s: %v", rel, err)
			continue
		}
		for _, f := range z.File {
			if name := strings.ToLower(f.Name); wanted[name] || strings.HasPrefix(name, menuSoundDir) {
				entries[name] = e.file
			}
		}
		z.Close()
	}
	return entries
}

// music returns the menu's music as a WAV (above). The WAV is made once and kept. music returns false when the game
// files lack the music.
func (h *web) music() (mapPicture, bool) {
	return h.kept("music", "the menu's music", func() (mapPicture, error) {
		data, err := h.iwdEntry(menuMusic)
		if err != nil {
			return mapPicture{}, err
		}
		wav, err := boaToWAV(data)
		return mapPicture{wav, "audio/wav"}, err
	})
}

// boaToWAV converts a sound that the game streams to a WAV. The sound is MS-ADPCM in the game's own container, which
// FFmpeg calls "boa". Its header has these little-endian fields: a version (1) at 0, the number of frames at 4, the
// rate at 8, the number of channels at 12, the offset of the data at 16, each channel's share of a block's bytes at
// 21, and the length of the data at 48. The WAV is WAVE_FORMAT_ADPCM with the standard coefficients, a fact chunk
// with the number of frames, and the data unchanged.
func boaToWAV(data []byte) ([]byte, error) {
	if len(data) < 52 || binary.LittleEndian.Uint32(data) != 1 {
		return nil, errors.New("not a streamed sound of the game's")
	}
	frames := binary.LittleEndian.Uint32(data[4:])
	rate := binary.LittleEndian.Uint32(data[8:])
	channels := binary.LittleEndian.Uint32(data[12:])
	start := binary.LittleEndian.Uint32(data[16:])
	blockAlign := binary.LittleEndian.Uint32(data[21:]) * channels
	size := binary.LittleEndian.Uint32(data[48:])
	if channels < 1 || channels > 2 || rate < 8000 || rate > 96000 || blockAlign < 7*channels+2 || blockAlign > 65535 ||
		uint64(start)+uint64(size) > uint64(len(data)) || size%blockAlign != 0 {
		return nil, fmt.Errorf("a streamed sound of %d channels at %d Hz, blocks of %d, %d bytes at %d of %d",
			channels, rate, blockAlign, size, start, len(data))
	}
	samplesPerBlock := (blockAlign-7*channels)*8/(4*channels) + 2
	var out bytes.Buffer
	put32 := func(v uint32) { binary.Write(&out, binary.LittleEndian, v) }
	put16 := func(v uint16) { binary.Write(&out, binary.LittleEndian, v) }
	out.WriteString("RIFF")
	put32(4 + 8 + 50 + 8 + 4 + 8 + size)
	out.WriteString("WAVEfmt ")
	put32(50)
	put16(2) // WAVE_FORMAT_ADPCM
	put16(uint16(channels))
	put32(rate)
	put32(rate * blockAlign / samplesPerBlock)
	put16(uint16(blockAlign))
	put16(4)
	put16(32)
	put16(uint16(samplesPerBlock))
	put16(7)
	for _, c := range [7][2]int16{{256, 0}, {512, -256}, {0, 0}, {192, 64}, {240, 0}, {460, -208}, {392, -232}} {
		binary.Write(&out, binary.LittleEndian, c)
	}
	out.WriteString("fact")
	put32(4)
	put32(frames)
	out.WriteString("data")
	put32(size)
	out.Write(data[start : start+size])
	return out.Bytes(), nil
}

// serveMusic answers GET /music (above) with the menu's music, or with a 404.
func (h *web) serveMusic(w http.ResponseWriter, r *http.Request) {
	pic, ok := h.music()
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveKept(w, r, pic)
}

// The IWI formats, with the numbers they have in Black Ops' version of the file format (13). The DXT formats have the
// same numbers in Black Ops II's version (27).
const (
	iwiARGB32 = 0x01
	iwiLA16   = 0x03
	iwiDXT1   = 0x0B
	iwiDXT3   = 0x0C
	iwiDXT5   = 0x0D
)

// decodeIWI decodes the largest level of an IWI, the game's texture file. The file is a header ("IWi", the version,
// the format, flags, width, height, depth, and more) and then its levels, the smallest first, so the largest level is
// at the end. The header has 48 bytes in Black Ops' version of the format (13) and 64 in Black Ops II's (27).
// decodeIWI reads the formats that the server's pictures use: DXT1, DXT3, DXT5, and in Black Ops' version also 32-bit
// BGRA, and luminance and alpha with a byte each (the rounds' tally marks, which the game tints: pictures.go).
func decodeIWI(data []byte) (*image.NRGBA, error) {
	if len(data) < 48 || string(data[:3]) != "IWi" {
		return nil, errors.New("not an IWI")
	}
	header := 48
	switch data[3] {
	case 13:
	case 27:
		if header = 64; len(data) < header || data[4] != iwiDXT1 && data[4] != iwiDXT3 && data[4] != iwiDXT5 {
			return nil, fmt.Errorf("a Black Ops II IWI of format %#x", data[4])
		}
	default:
		return nil, fmt.Errorf("IWI version %d, not Black Ops' (13) or Black Ops II's (27)", data[3])
	}
	format := data[4]
	w, h := int(binary.LittleEndian.Uint16(data[6:])), int(binary.LittleEndian.Uint16(data[8:]))
	if w == 0 || h == 0 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("an IWI of %dx%d", w, h)
	}
	blocks := ((w + 3) / 4) * ((h + 3) / 4)
	var size int
	switch format {
	case iwiDXT1:
		size = blocks * 8
	case iwiDXT3, iwiDXT5:
		size = blocks * 16
	case iwiARGB32:
		size = w * h * 4
	case iwiLA16:
		size = w * h * 2
	default:
		return nil, fmt.Errorf("IWI format %#x", format)
	}
	if len(data)-header < size {
		return nil, errors.New("a short IWI")
	}
	pixels := data[len(data)-size:]
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	if format == iwiARGB32 {
		for i := 0; i < w*h; i++ {
			b, g, r, a := pixels[i*4], pixels[i*4+1], pixels[i*4+2], pixels[i*4+3]
			copy(img.Pix[i*4:], []byte{r, g, b, a})
		}
		return img, nil
	}
	if format == iwiLA16 {
		for i := 0; i < w*h; i++ {
			l, a := pixels[i*2], pixels[i*2+1]
			copy(img.Pix[i*4:], []byte{l, l, l, a})
		}
		return img, nil
	}
	stride := 8
	if format != iwiDXT1 {
		stride = 16
	}
	for by := 0; by < (h+3)/4; by++ {
		for bx := 0; bx < (w+3)/4; bx++ {
			block := pixels[(by*((w+3)/4)+bx)*stride:]
			var px [16]color.NRGBA
			switch format {
			case iwiDXT1:
				decodeBC1(block, &px, true)
			case iwiDXT3:
				decodeBC1(block[8:], &px, false)
				for i := range px {
					a := block[i/2] >> (4 * (i % 2)) & 0xF
					px[i].A = a<<4 | a
				}
			case iwiDXT5:
				decodeBC1(block[8:], &px, false)
				decodeBC3Alpha(block, &px)
			}
			for i, c := range px {
				x, y := bx*4+i%4, by*4+i/4
				if x < w && y < h {
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}
	return img, nil
}

// decodeBC1 decodes the colours of a block: a DXT1 block, or the colour half of a DXT3 or DXT5 block. A block has
// two RGB565 colours, and each of its pixels is one of those two or one of two colours between them. In DXT1, when
// the first colour is not the greater, there is only one colour between them, and the fourth choice is transparent
// black.
func decodeBC1(block []byte, px *[16]color.NRGBA, dxt1 bool) {
	c0, c1 := binary.LittleEndian.Uint16(block), binary.LittleEndian.Uint16(block[2:])
	rgb := func(c uint16) [3]int {
		r, g, b := int(c>>11&31), int(c>>5&63), int(c&31)
		return [3]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2}
	}
	a, b := rgb(c0), rgb(c1)
	var palette [4]color.NRGBA
	mix := func(x, y [3]int, wx, wy, d int) color.NRGBA {
		return color.NRGBA{uint8((x[0]*wx + y[0]*wy) / d), uint8((x[1]*wx + y[1]*wy) / d), uint8((x[2]*wx + y[2]*wy) / d), 255}
	}
	palette[0], palette[1] = mix(a, b, 1, 0, 1), mix(a, b, 0, 1, 1)
	if c0 > c1 || !dxt1 {
		palette[2], palette[3] = mix(a, b, 2, 1, 3), mix(a, b, 1, 2, 3)
	} else {
		palette[2], palette[3] = mix(a, b, 1, 1, 2), color.NRGBA{}
	}
	bits := binary.LittleEndian.Uint32(block[4:])
	for i := range px {
		px[i] = palette[bits>>(2*i)&3]
	}
}

// decodeBC3Alpha decodes the alpha of a DXT5 block. A block has two values, and either six values between them, or
// four values between them plus 0 and 255.
func decodeBC3Alpha(block []byte, px *[16]color.NRGBA) {
	a0, a1 := int(block[0]), int(block[1])
	var palette [8]uint8
	palette[0], palette[1] = uint8(a0), uint8(a1)
	if a0 > a1 {
		for i := 1; i < 7; i++ {
			palette[i+1] = uint8(((7-i)*a0 + i*a1) / 7)
		}
	} else {
		for i := 1; i < 5; i++ {
			palette[i+1] = uint8(((5-i)*a0 + i*a1) / 5)
		}
		palette[6], palette[7] = 0, 255
	}
	var bits uint64
	for i := 5; i >= 0; i-- {
		bits = bits<<8 | uint64(block[2+i])
	}
	for i := range px {
		px[i].A = palette[bits>>(3*i)&7]
	}
}

// resize scales an image to w by h. Each pixel is a blend of the four pixels of the image that it falls between, or
// of two along a side whose length stays the same.
func resize(in *image.NRGBA, w, h int) *image.NRGBA {
	b := in.Bounds()
	if w <= 0 || h <= 0 || w == b.Dx() && h == b.Dy() {
		return in
	}
	// between returns where pixel i of n falls among the from pixels of the image along one side: the two pixels it
	// is between, and how far it is from the first.
	between := func(i, n, from int) (int, int, float64) {
		at := (float64(i)+0.5)*float64(from)/float64(n) - 0.5
		i0 := max(int(at), 0)
		i1, f := min(i0+1, from-1), at-float64(i0)
		if at < 0 {
			f = 0
		}
		return i0, i1, f
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1, fy := between(y, h, b.Dy())
		for x := 0; x < w; x++ {
			x0, x1, fx := between(x, w, b.Dx())
			p, q := in.Pix[y0*in.Stride+x0*4:], in.Pix[y0*in.Stride+x1*4:]
			r, s := in.Pix[y1*in.Stride+x0*4:], in.Pix[y1*in.Stride+x1*4:]
			for c := 0; c < 4; c++ {
				top := float64(p[c])*(1-fx) + float64(q[c])*fx
				bottom := float64(r[c])*(1-fx) + float64(s[c])*fx
				out.Pix[y*out.Stride+x*4+c] = uint8(top*(1-fy) + bottom*fy + 0.5)
			}
		}
	}
	return out
}

// mapDescription returns a map's description in the game's map select, or "" when the game files have none (above).
func (h *web) mapDescription(mapName string) string {
	m, ok := mapByName(mapName)
	if !ok || m.description == "" {
		return ""
	}
	return h.localized()[m.description]
}

// localized returns the game's localized strings by name, in the form the page shows (gameText). The page shows the
// maps' descriptions and the text of the options menu it rebuilds (GET /strings). The strings are read from the game
// files once.
func (h *web) localized() map[string]string {
	h.locs.Lock()
	defer h.locs.Unlock()
	if h.texts == nil {
		h.texts = h.readLocalized()
	}
	return h.texts
}

// readLocalized reads the localized strings from the zones that have them (localizedZones). When two zones have a
// string of the same name, it uses the first zone's string. A localized string is a LocalizeEntry in a zone: its two
// pointers, both inline (-1 each), then its text and its name, each ending in a 0.
func (h *web) readLocalized() map[string]string {
	texts := map[string]string{}
	for _, rel := range localizedZones(h.paths()) {
		e, ok := h.lookup(rel)
		if !ok {
			continue
		}
		zone, err := readZone(e.file)
		if err != nil {
			log.Printf("server: %s: %v", rel, err)
			continue
		}
		for name, text := range localizeEntries(zone) {
			if _, had := texts[name]; !had {
				texts[name] = text
			}
		}
	}
	return texts
}

// localizeEntries finds a zone's localized strings. Each is 8 bytes of 0xff, the text, a 0, the name (capitals,
// digits and underscores), and a 0. More 0xff bytes before the text are skipped: they belong to another inline
// pointer just before the string, and no text starts with 0xff. A string whose text is the end of an earlier string's
// text (ADS, the end of TOGGLE ADS) has a pointer to that text instead. The pointer gives where the engine loads the
// text, not where the text is in the file, so those strings are left out. They are about one in ten.
func localizeEntries(zone []byte) map[string]string {
	entries := map[string]string{}
	pointers := []byte("\xff\xff\xff\xff\xff\xff\xff\xff")
	for at := 0; ; {
		i := bytes.Index(zone[at:], pointers)
		if i < 0 {
			return entries
		}
		start := at + i + 8
		for start < len(zone) && zone[start] == 0xff {
			start++
		}
		at = start
		textEnd := bytes.IndexByte(zone[start:], 0)
		if textEnd < 0 {
			return entries
		}
		nameStart := start + textEnd + 1
		nameEnd := bytes.IndexByte(zone[nameStart:], 0)
		if nameEnd <= 0 || !localizeName.Match(zone[nameStart:nameStart+nameEnd]) {
			continue
		}
		name := string(zone[nameStart : nameStart+nameEnd])
		if _, had := entries[name]; !had {
			entries[name] = gameText(zone[start : start+textEnd])
		}
		at = nameStart + nameEnd + 1
	}
}

// localizeName matches the name of a localized string (MENU_INVERT_MOUSE_CAPS).
var localizeName = regexp.MustCompile(`^[A-Z0-9_]+$`)

// serveStrings answers GET /strings?keys=MENU_YES,KEY_MOUSE1,... with the named localized strings of the game that
// the server has, as a JSON object by name. They are the text of the options menu that the page rebuilds
// (web/src/settings.ts). The language is that of the game files, English when they have it. Names that the server lacks
// are left out, and so are keys that are not names.
func (h *web) serveStrings(w http.ResponseWriter, r *http.Request) {
	all := h.localized()
	out := map[string]string{}
	for _, name := range strings.Split(r.URL.Query().Get("keys"), ",") {
		if text, ok := all[name]; ok && len(out) < 1000 {
			out[name] = text
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(out)
}

// localizedZones returns the zones that hold the localized strings, out of the game files' lowercase paths. They are
// the code_post_gfx and patch zones of one language, which is English when the game files have it.
func localizedZones(paths []string) []string {
	byLanguage := map[string][]string{}
	for _, rel := range paths {
		dir, file, ok := strings.Cut(strings.TrimPrefix(rel, "zone/"), "/")
		if !ok || !strings.HasPrefix(rel, "zone/") || strings.Contains(file, "/") {
			continue
		}
		if _, base, ok := strings.Cut(file, "_"); ok && (base == "code_post_gfx.ff" || base == "patch.ff") {
			byLanguage[dir] = append(byLanguage[dir], rel)
		}
	}
	languages := make([]string, 0, len(byLanguage))
	for dir := range byLanguage {
		languages = append(languages, dir)
	}
	slices.Sort(languages)
	if i := slices.Index(languages, "english"); i > 0 {
		languages[0], languages[i] = languages[i], languages[0]
	}
	if len(languages) == 0 {
		return nil
	}
	zones := byLanguage[languages[0]]
	slices.Sort(zones)
	return zones
}

// readZone returns the zone of a fastfile. The file is a header ("IWffu100" and its version) and then the zone,
// zlib-compressed.
func readZone(file string) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:8]) != "IWffu100" {
		return nil, errors.New("not a fastfile")
	}
	r, err := zlib.NewReader(bytes.NewReader(data[12:]))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	zone, err := io.ReadAll(io.LimitReader(r, 256<<20))
	if len(zone) == 0 {
		return nil, err
	}
	return zone, nil // also what was read before an error at the zone's end, if any
}

// gameColour matches the colour codes in the game's text (^1), which the page does not show.
var gameColour = regexp.MustCompile(`\^[0-9]`)

// gameText converts the game's text to the form the page shows. It converts Latin-1, the encoding of the game's
// English strings, to UTF-8, removes the colour codes, and replaces each run of spaces and line breaks with one space.
func gameText(text []byte) string {
	var s strings.Builder
	for _, c := range text {
		if c < utf8.RuneSelf {
			s.WriteByte(c)
		} else {
			s.WriteRune(rune(c))
		}
	}
	return strings.Join(strings.Fields(gameColour.ReplaceAllString(s.String(), "")), " ")
}

// serveMapArt answers GET /maps/<map>/<kind> (above).
func (h *web) serveMapArt(w http.ResponseWriter, r *http.Request) {
	mapName, kind, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/maps/"), "/")
	switch kind {
	case "files":
		h.serveFileList(w, r, mapName)
		return
	case "pipelines":
		h.servePipelines(w, r, mapName)
		return
	}
	if !mapArtKinds[kind] {
		http.NotFound(w, r)
		return
	}
	h.servePicture(w, r, mapName, kind)
}

// servePicture sends the map's picture of the given kind (mapArt), or a 404.
func (h *web) servePicture(w http.ResponseWriter, r *http.Request, mapName, kind string) {
	pic, ok := h.mapArt(mapName, kind)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveKept(w, r, pic)
}
