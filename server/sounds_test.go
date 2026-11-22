package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
)

// zoneAlias is an alias as a zone keeps it with its loaded sound (sounds.go): its name, the alias, its SoundFile, the
// LoadedSound, the sound's name, its seek table, its packets.
func zoneAlias(name string, volMin, volMax uint16, rate, channels, format uint32, decoded []uint32,
	data []byte) []byte {
	var b bytes.Buffer
	b.WriteString("\x00" + name + "\x00")
	alias := make([]byte, aliasSize)
	binary.LittleEndian.PutUint32(alias[aliasSoundFile:], zoneInline)
	binary.LittleEndian.PutUint16(alias[aliasVolume:], volMin)
	binary.LittleEndian.PutUint16(alias[aliasVolume+2:], volMax)
	b.Write(alias)
	b.Write([]byte{0xff, 0xff, 0xff, 0xff, soundLoaded, 1, 0, 0})
	put := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	put(zoneInline) // the LoadedSound's name
	frames := decoded[len(decoded)-1] / (2 * channels)
	for _, v := range []uint32{1, frames, rate, channels, 0, 0, 0, format, 3, 0, uint32(len(decoded)), zoneInline,
		uint32(len(data)), zoneInline} {
		put(v)
	}
	b.WriteString("sound\\uin\\" + name + ".wav\x00")
	for _, v := range decoded {
		put(v)
	}
	b.Write(data)
	return b.Bytes()
}

// zoneStreamedAlias is an alias as a zone keeps it when the game streams its sound (sounds.go): its name, the alias,
// its SoundFile, the StreamedSound, the file's name.
func zoneStreamedAlias(name string, volMin, volMax uint16, file string) []byte {
	var b bytes.Buffer
	b.WriteString("\x00" + name + "\x00")
	alias := make([]byte, aliasSize)
	binary.LittleEndian.PutUint32(alias[aliasSoundFile:], zoneInline)
	binary.LittleEndian.PutUint16(alias[aliasVolume:], volMin)
	binary.LittleEndian.PutUint16(alias[aliasVolume+2:], volMax)
	b.Write(alias)
	b.Write([]byte{0xff, 0xff, 0xff, 0xff, soundStreamed, 1, 0, 0})
	b.Write([]byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0})
	b.WriteString(file + "\x00")
	return b.Bytes()
}

// zoneGroup is a sound group as a zone keeps it (sounds.go): its name, its parent's, an ID, its parent's place, its
// category, and its volume in the single player game and in multiplayer.
func zoneGroup(name, parent string, parentAt int32, volumeSp, volumeMp uint16) []byte {
	g := make([]byte, groupSize)
	copy(g, name)
	copy(g[32:], parent)
	binary.LittleEndian.PutUint32(g[groupParent:], uint32(parentAt))
	binary.LittleEndian.PutUint16(g[groupVolume:], volumeSp)
	binary.LittleEndian.PutUint16(g[groupVolume+2:], volumeMp)
	return g
}

// inGroup puts the alias (zoneAlias, zoneStreamedAlias) of that name in a group, by the group's place.
func inGroup(alias []byte, name string, group uint32) []byte {
	binary.LittleEndian.PutUint32(alias[len(name)+2+aliasFlags:], group<<16|0x4008)
	return alias
}

func TestMenuSounds(t *testing.T) {
	over := bytes.Repeat([]byte{1, 2, 3, 4}, 4096/4)
	click := bytes.Repeat([]byte{5, 6}, 2*2230/2)
	var zone bytes.Buffer
	zone.WriteString("zone header")
	// the menus' scripts name the aliases too, and a name may come before something that is no alias of a loaded sound
	zone.WriteString("play \"uin_navigation_over\" ;\x00\x00uin_navigation_over\x00" + string(make([]byte, 200)))
	// the groups: the menus' sounds are of ui, under master; a group's name is in the menus' scripts too
	zone.WriteString("setLocalVarString ui_choicegroup master\x00")
	zone.Write(zoneGroup("reference", "", -1, 65535, 65535))
	zone.Write(zoneGroup("master", "", -1, 41349, 65535))
	zone.Write(zoneGroup("ui", "master", 1, 20723, 29273))
	zone.WriteString("zone between")
	zone.Write(inGroup(zoneAlias("uin_navigation_over", 32845, 32845, 48000, 2, sndFormatWMA, []uint32{16384}, over),
		"uin_navigation_over", 2))
	zone.Write(zoneAlias("uin_navigation_click", 0, 65535, 44100, 1, sndFormatWMA, []uint32{8192, 20000}, click))
	// not xWMA: left out
	zone.Write(zoneAlias("uin_navigation_submenu_over", 65535, 65535, 48000, 2, 6, []uint32{16384}, over))
	// those the game streams, the zone naming each one's file in the archives; one whose file they lack: left out
	zone.Write(inGroup(zoneStreamedAlias("uin_lobby_join", 65535, 65535, `sound\uin\lobby\join\lobby_join.wav`),
		"uin_lobby_join", 2))
	zone.Write(zoneStreamedAlias("uin_lobby_leave", 20723, 20723, `sound\uin\lobby\leave\lobby_leave.wav`))
	zone.WriteString("zone end")
	var ff bytes.Buffer
	ff.WriteString("IWffu100\xd9\x01\x00\x00")
	z := zlib.NewWriter(&ff)
	z.Write(zone.Bytes())
	z.Close()
	gameDir := t.TempDir()
	writeFile(t, filepath.Join(gameDir, "zone", "Common", "code_post_gfx.ff"), ff.String())
	join := boa(1024, 47966, 2, 262, bytes.Repeat([]byte{1, 2, 3, 4}, 524*2/4))
	writeIWD(t, filepath.Join(gameDir, "main", "iw_23.iwd"), map[string][]byte{
		"sound/uin/lobby/join/lobby_join.wav": join})
	joinWAV, err := boaToWAV(join)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&web{gameDir: gameDir})
	defer srv.Close()

	rsp, body := get(t, srv.URL+"/sounds")
	var got map[string]menuSound
	if err := json.Unmarshal([]byte(body), &got); err != nil || rsp.StatusCode != http.StatusOK ||
		rsp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("/sounds: %s %s %v %.200q", rsp.Status, rsp.Header.Get("Content-Type"), err, body)
	}
	// a sound of ui plays at ui's volume times master's, in the single player game; one of a group with no parent, at
	// that group's
	uiVolume, masterVolume := 20723.0/65535, 41349.0/65535
	ui := uiVolume * masterVolume // as float64s: each of the two is rounded first
	want := map[string]menuSound{
		"uin_navigation_over": {Volume: [2]float64{32845.0 / 65535, 32845.0 / 65535}, Group: ui, Rate: 48000,
			Channels: 2, BlockAlign: 4096, BytesPerSecond: 12000, Decoded: []uint32{16384}, Data: over},
		"uin_navigation_click": {Volume: [2]float64{0, 1}, Group: 1, Rate: 44100, Channels: 1, BlockAlign: 2230,
			BytesPerSecond: 6000, Decoded: []uint32{8192, 20000}, Data: click},
		"uin_lobby_join": {Volume: [2]float64{1, 1}, Group: ui, Wav: joinWAV},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("/sounds: %d sounds, want %d", len(got), len(want))
		for name, s := range got {
			w := want[name]
			s.Data, w.Data = s.Data[:min(len(s.Data), 8)], w.Data[:min(len(w.Data), 8)]
			s.Wav, w.Wav = s.Wav[:min(len(s.Wav), 8)], w.Wav[:min(len(w.Wav), 8)]
			t.Errorf("  %s: %+v, want %+v", name, s, w)
		}
	}
	// a browser that has them asks with their ETag, and has no more than a 304
	etag := rsp.Header.Get("ETag")
	if rsp, body = get(t, srv.URL+"/sounds", "If-None-Match", etag); etag == "" ||
		rsp.StatusCode != http.StatusNotModified || body != "" {
		t.Errorf("/sounds again, with its ETag %q: %s, %d bytes, want a 304", etag, rsp.Status, len(body))
	}

	// cut short, or packets the alias's sound lacks: none
	whole := zoneAlias("uin_navigation_over", 1, 1, 48000, 2, sndFormatWMA, []uint32{16384}, over)
	if _, _, ok := zoneSound(whole[:len(whole)-1], "uin_navigation_over"); ok {
		t.Errorf("a sound cut short was read")
	}
	if _, _, ok := zoneSound(zoneAlias("uin_navigation_over", 1, 1, 48000, 2, sndFormatWMA, []uint32{16384, 32768}, over),
		"uin_navigation_over"); ok {
		t.Errorf("a sound of 2 packets with the bytes of 1 was read")
	}
	// a streamed sound: its volume and its file, as an archive names it; none for a file that is no sound of the menus'
	s, file, ok := zoneSound(zoneStreamedAlias("uin_lobby_leave", 20723, 65535, `Sound\UIN\lobby\leave\lobby_leave.wav`),
		"uin_lobby_leave")
	if !ok || file != "sound/uin/lobby/leave/lobby_leave.wav" || s.Volume != [2]float64{20723.0 / 65535, 1} {
		t.Errorf("a streamed sound: %v %q %+v", ok, file, s)
	}
	if _, _, ok := zoneSound(zoneStreamedAlias("uin_lobby_leave", 1, 1, `..\players\config.cfg`), "uin_lobby_leave"); ok {
		t.Errorf("a streamed sound outside the menus' was read")
	}

	// a zone with no groups: its sounds at their aliases' volumes; a group the zone lacks likewise
	if g := zoneGroups(whole); g != nil || groupVolumes(g, 18) != 1 {
		t.Errorf("a zone with no groups has %v, and its group 18 a volume of %v", g, groupVolumes(g, 18))
	}
	if g := zoneGroups(zone.Bytes()); len(g) != 3 || groupVolumes(g, 2) != ui || groupVolumes(g, 5) != 1 {
		t.Errorf("the zone's groups: %v, ui at %v, a group it lacks at %v", g, groupVolumes(g, 2), groupVolumes(g, 5))
	}

	// an install without the zone: none
	srv2 := httptest.NewServer(&web{gameDir: t.TempDir()})
	defer srv2.Close()
	if rsp, body = get(t, srv2.URL+"/sounds"); rsp.StatusCode != http.StatusOK || body != "{}" {
		t.Errorf("/sounds of an install without them: %s %q", rsp.Status, body)
	}
}
