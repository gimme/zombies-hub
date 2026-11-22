package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// The game's menu sounds. The page plays them where the game's menus and the game's own lobby menu play them
// (web/src/sounds.ts: sounds, web/src/settings.ts).
//
//	GET /sounds  the sounds of the aliases in menuSounds, from the game files, as a JSON object by alias. Each alias
//	             has its volume, its group's volume and its sound. "volume" is the alias's least and greatest
//	             volume, 0 to 1. The sound plays at a volume between the two. "group" is the factor, 0 to 1, by which
//	             the game turns down every sound of the alias's group (below). A sound that the zone holds is sent as
//	             the zone holds it, as xWMA (WMA version 2). It has its rate and channels, the size of its packets
//	             and the bytes a second that XAudio2 is given for it (both as the engine sets them,
//	             bo1-zombies/src/sound/snd_driver_xaudio2.cpp), the number of bytes it has decoded to by the end of
//	             each packet (XAudio2's pDecodedPacketCumulativeBytes; the last number is its decoded length), and
//	             the packets (base64). The page decodes them with the web client's decoder
//	             (bo1-zombies/web/src/xwma.cpp, built as xwma.js). A sound that the game streams from an archive is
//	             sent the way the menu's music is (mapart.go: boaToWAV). Its "wav" is a WAV of its MS-ADPCM (base64),
//	             which the page decodes like the music. An alias that the game files lack is left out. The answer is
//	             {} when the game files lack zone/Common/code_post_gfx.ff. The ETag is the hash of the answer's
//	             contents. A browser that asks again gets a 304 while its copy is current. It gets the sounds again
//	             only after they changed.
//
// The aliases are in code_post_gfx.ff. Most have their sound in the zone. Such a sound is a loaded sound, which the
// engine plays from memory (bo1-zombies/src/sound/snd.h: LoadedSound). The game streams the two sounds of a player
// joining and leaving its lobby from an .iwd, as it does the menus' music (GET /music). For those the zone names the
// file (StreamedSound).
//
// The game plays a sound at its alias's volume times its group's volume, times the volume of the group's parent, and
// so on up (bo1-zombies/src/sound/snd.cpp: SND_GetBaseLevel). The groups are in the same zone (zoneGroups). The
// menus' sounds are in the group ui, at -10 dB, which is under master, at -4 dB. They therefore play at a fifth of
// their aliases' volumes. Zombies is part of the single player game, so it uses the single player column of the
// groups' two columns (bo1-zombies/src/sound/snd_globals.cpp: SND_GroupAttenuationRaw).

// menuSounds are the aliases the page plays. The menus' sounds are those of a row or a button under the mouse, of
// the click of a tab or a button, and of a popup's answer (web/src/settings.ts has which sound the game's options menu
// plays where). A large menu has a sound for when it opens and one for when it closes (the Zombies map select, the
// leaderboards). The last three are the sounds of the game's lobby, which the game's party code plays: the tick of
// the countdown, and a player joining and leaving.
var menuSounds = []string{"uin_navigation_over", "uin_navigation_click", "uin_navigation_submenu_over",
	"uin_navigation_menu_lg_open", "uin_navigation_menu_lg_close", "uin_timer", "uin_lobby_join", "uin_lobby_leave"}

// menuZone is the zone with the menus and their sounds.
const menuZone = "zone/common/code_post_gfx.ff"

// menuSoundDir is the folder in the archives with the menus' sounds that the game streams (findEntries).
const menuSoundDir = "sound/uin/"

type menuSound struct {
	Volume [2]float64 `json:"volume"`
	Group  float64    `json:"group"`
	group  int        // the alias's group, as its index among the zone's groups
	// for a sound that the zone holds
	Rate           uint32   `json:"rate,omitempty"`
	Channels       uint32   `json:"channels,omitempty"`
	BlockAlign     uint32   `json:"blockAlign,omitempty"`
	BytesPerSecond uint32   `json:"bytesPerSecond,omitempty"`
	Decoded        []uint32 `json:"decoded,omitempty"`
	Data           []byte   `json:"data,omitempty"`
	// for a sound that the game streams
	Wav []byte `json:"wav,omitempty"`
}

// The layout of an alias in the zone. The alias's name comes first, with a 0 on each side. After it come the alias
// (bo1-zombies/src/sound/snd.h: snd_alias_t), its SoundFile (a pointer, its type, whether it exists), and its one
// sound. A loaded sound is the LoadedSound (a pointer to its name, then its snd_asset), the name, the seek table (a
// uint32 for each packet), then the data. A streamed sound is the StreamedSound (a pointer to its file's name, and a
// pointer to the part of its start that the game holds in memory), then the name. A pointer of -1 points to the data
// that follows.
const (
	zoneInline     = 0xffffffff
	aliasSize      = 0x54 // snd_alias_t
	aliasSoundFile = 16   // .soundFile
	aliasFlags     = 20   // .flags: the alias's group in bits 16 to 21
	aliasVolume    = 44   // .volMin, .volMax: each out of 65535
	soundFileSize  = 8
	soundLoaded    = 1 // SoundFile.type
	soundStreamed  = 2
	loadedSize     = 0x3c // LoadedSound: a pointer to its name, and its snd_asset at 4
	streamedSize   = 8    // StreamedSound
	sndFormatWMA   = 7    // snd_asset_format: SND_ASSET_FORMAT_WMA
)

// zoneSound finds the alias of the given name in the zone. When the zone holds the alias's sound (loaded, whole, as
// xWMA), zoneSound returns the alias with its sound. When the game streams the sound, zoneSound returns the alias
// with only its volume and group, and file is the sound's path as an archive names it. ok is false when the zone has
// no alias of that name of either kind.
func zoneSound(zone []byte, alias string) (s menuSound, file string, ok bool) {
	key := []byte("\x00" + alias + "\x00")
	for at := 0; ; {
		i := bytes.Index(zone[at:], key)
		if i < 0 {
			return menuSound{}, "", false
		}
		start := at + i + len(key)
		at = start - 1
		if s, ok := loadedSoundAt(zone, start); ok {
			return s, "", true
		}
		if s, file, ok := streamedSoundAt(zone, start); ok {
			return s, file, true
		}
	}
}

// aliasVolumes returns the least and the greatest volume of the alias at offset a, each 0 to 1.
func aliasVolumes(zone []byte, a int) [2]float64 {
	volume := func(at int) float64 { return float64(binary.LittleEndian.Uint16(zone[at:])) / 65535 }
	return [2]float64{volume(a + aliasVolume), volume(a + aliasVolume + 2)}
}

// aliasGroup returns the group of the alias at offset a, as its index among the zone's groups.
func aliasGroup(zone []byte, a int) int {
	return int(binary.LittleEndian.Uint32(zone[a+aliasFlags:]) >> 16 & 0x3f)
}

// The layout of a sound group in the zone (bo1-zombies/src/sound/snd.h: snd_group, in the zone's SndDriverGlobals).
// It has the group's name and its parent's name, each in 32 bytes, an ID, the index of its parent among the groups
// (-1 when it has none), its category, and the factor it turns its sounds down by, out of 65535. The factor is there
// twice: for the single player game and for multiplayer.
const (
	groupSize   = 80
	groupParent = 68
	groupVolume = 76 // .attenuationSp
)

type soundGroup struct {
	parent int
	volume float64
}

// zoneGroups returns the zone's sound groups, in their order. It finds them by searching for master, the group with
// no parent that the other groups are under. From master it steps back to the first group, and then reads on to the
// last. It returns nil when the zone has no groups.
func zoneGroups(zone []byte) []soundGroup {
	name := func(b []byte) bool { // whether b is a name, or no name, followed only by zeros
		n := bytes.IndexByte(b, 0)
		if n < 0 {
			return false
		}
		for _, c := range b[:n] {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
		return len(bytes.Trim(b[n:], "\x00")) == 0
	}
	group := func(at int) bool {
		if at < 0 || at+groupSize > len(zone) || zone[at] == 0 || !name(zone[at:at+32]) || !name(zone[at+32:at+64]) {
			return false
		}
		parent := int32(binary.LittleEndian.Uint32(zone[at+groupParent:]))
		return parent >= -1 && parent < 64
	}
	master := make([]byte, 64)
	copy(master, "master")
	first := -1
	for at := 0; first < 0; {
		i := bytes.Index(zone[at:], master)
		if i < 0 {
			return nil
		}
		if at += i; group(at) {
			first = at
		}
		at++
	}
	for group(first - groupSize) {
		first -= groupSize
	}
	var groups []soundGroup
	for at := first; group(at) && len(groups) < 64; at += groupSize {
		groups = append(groups, soundGroup{int(int32(binary.LittleEndian.Uint32(zone[at+groupParent:]))),
			float64(binary.LittleEndian.Uint16(zone[at+groupVolume:])) / 65535})
	}
	return groups
}

// groupVolumes returns the factor, 0 to 1, by which the game turns down a sound of the given group: the group's
// volume times its parent's volume, and so on up. It returns 1 for a group that the zone doesn't have.
func groupVolumes(groups []soundGroup, group int) float64 {
	volume := 1.0
	for n := 0; group >= 0 && group < len(groups) && n < len(groups); n++ {
		volume *= groups[group].volume
		group = groups[group].parent
	}
	return volume
}

// streamedSoundAt reads the alias at offset a (above) when the game streams its sound. It returns the alias's volume
// and group, and the sound's file. The zone names the file as a Windows path (sound\uin\lobby\join\lobby_join.wav).
// streamedSoundAt returns the name an archive has for it: lowercase, with slashes.
func streamedSoundAt(zone []byte, a int) (menuSound, string, bool) {
	file := a + aliasSize
	streamed := file + soundFileSize
	name := streamed + streamedSize
	if name > len(zone) {
		return menuSound{}, "", false
	}
	u32 := func(at int) uint32 { return binary.LittleEndian.Uint32(zone[at:]) }
	if u32(a+aliasSoundFile) != zoneInline || u32(file) != zoneInline || zone[file+4] != soundStreamed ||
		u32(streamed) != zoneInline {
		return menuSound{}, "", false
	}
	end := bytes.IndexByte(zone[name:min(len(zone), name+256)], 0)
	if end < 0 {
		return menuSound{}, "", false
	}
	path := strings.ToLower(strings.ReplaceAll(string(zone[name:name+end]), `\`, "/"))
	if !strings.HasPrefix(path, menuSoundDir) || !strings.HasSuffix(path, ".wav") {
		return menuSound{}, "", false
	}
	return menuSound{Volume: aliasVolumes(zone, a), group: aliasGroup(zone, a)}, path, true
}

// loadedSoundAt reads the alias at offset a (above) and its loaded sound, in the format the engine plays it in
// (bo1-zombies/src/sound/snd_driver_xaudio2.cpp): xWMA in packets of 4096 bytes in stereo or 2230 bytes in mono, at
// 6000 bytes a second for each channel.
func loadedSoundAt(zone []byte, a int) (menuSound, bool) {
	file := a + aliasSize
	loaded := file + soundFileSize
	name := loaded + loadedSize
	if name > len(zone) {
		return menuSound{}, false
	}
	u32 := func(at int) uint32 { return binary.LittleEndian.Uint32(zone[at:]) }
	if u32(a+aliasSoundFile) != zoneInline || u32(file) != zoneInline || zone[file+4] != soundLoaded ||
		u32(loaded) != zoneInline {
		return menuSound{}, false
	}
	// The snd_asset has version, frame_count, frame_rate, channel_count, ..., format at 28, ..., seek_table_count at 40.
	asset := loaded + 4
	rate, channels, format := u32(asset+8), u32(asset+12), u32(asset+28)
	packets, table, size, data := u32(asset+40), u32(asset+44), u32(asset+48), u32(asset+52)
	if u32(asset) != 1 || format != sndFormatWMA || channels < 1 || channels > 2 || rate < 8000 || rate > 50000 ||
		packets < 1 || packets > 1024 || table != zoneInline || data != zoneInline {
		return menuSound{}, false
	}
	blockAlign := uint32(2230)
	if channels == 2 {
		blockAlign = 4096
	}
	end := bytes.IndexByte(zone[name:min(len(zone), name+256)], 0)
	if end < 0 || size < packets*blockAlign {
		return menuSound{}, false
	}
	seek := name + end + 1
	start := seek + 4*int(packets)
	if start+int(packets*blockAlign) > len(zone) {
		return menuSound{}, false
	}
	s := menuSound{
		Volume:         aliasVolumes(zone, a),
		group:          aliasGroup(zone, a),
		Rate:           rate,
		Channels:       channels,
		BlockAlign:     blockAlign,
		BytesPerSecond: 6000 * channels,
		Data:           zone[start : start+int(packets*blockAlign)],
	}
	for i := 0; i < int(packets); i++ {
		s.Decoded = append(s.Decoded, u32(seek+4*i))
		if i > 0 && s.Decoded[i] < s.Decoded[i-1] {
			return menuSound{}, false
		}
	}
	return s, true
}

// sounds returns the answer to GET /sounds (above). It makes the answer once, with h.art held, and keeps it.
func (h *web) sounds() mapPicture {
	h.art.Lock()
	defer h.art.Unlock()
	if h.pictures == nil {
		h.pictures = map[string]mapPicture{}
	}
	if pic, ok := h.pictures["sounds"]; ok {
		return pic
	}
	out := map[string]menuSound{}
	var err error
	if e, ok := h.lookup(menuZone); !ok {
		err = os.ErrNotExist
	} else if zone, zerr := readZone(e.file); zerr != nil {
		err = zerr
	} else {
		groups := zoneGroups(zone)
		if len(groups) == 0 {
			log.Printf("server: %s has no sound groups: the menu's sounds play at their aliases' volumes", e.rel)
		}
		for _, alias := range menuSounds {
			s, file, ok := zoneSound(zone, alias)
			if !ok {
				log.Printf("server: the menu's sound %s is not in %s", alias, e.rel)
				continue
			}
			s.Group = groupVolumes(groups, s.group)
			if file != "" {
				// The game streams this sound. It is read from the archives, like the music.
				data, ferr := h.iwdEntry(file)
				if ferr == nil {
					s.Wav, ferr = boaToWAV(data)
				}
				if ferr != nil {
					log.Printf("server: the menu's sound %s: %v", alias, ferr)
					continue
				}
			}
			out[alias] = s
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("server: the menu's sounds: %v", err)
	}
	data, _ := json.Marshal(out)
	pic := mapPicture{data, "application/json"}
	h.pictures["sounds"] = pic
	return pic
}

// serveSounds answers GET /sounds (above).
func (h *web) serveSounds(w http.ResponseWriter, r *http.Request) {
	pic := h.sounds()
	sum := sha256.Sum256(pic.data)
	w.Header().Set("Content-Type", pic.contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+contentHash(sum[:])+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(pic.data))
}
