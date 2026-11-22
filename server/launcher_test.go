package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zombies/internal/launcherapi"
)

// asLauncher is a call of a player's launcher, by their key, and its answer's status and body. header is the call's
// headers, a name and a value each; "from" is the address it comes from.
func asLauncher(h http.Handler, method, path, key, body string, header ...string) (*http.Response, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", launcherapi.Key+key)
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "from" { // the address the call comes from
			req.RemoteAddr = header[i+1]
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result(), rec.Body.String()
}

func playOf(t *testing.T, h http.Handler, key string) launcherapi.Play {
	t.Helper()
	rsp, body := asLauncher(h, http.MethodGet, "/launcher/play", key, "")
	var play launcherapi.Play
	if err := json.Unmarshal([]byte(body), &play); err != nil || rsp.StatusCode != http.StatusOK {
		t.Fatalf("/launcher/play: %s, %q (%v)", rsp.Status, body, err)
	}
	return play
}

// The server holds a launcher's call that has the answer already, until the answer is another one. The launcher
// counts as running for as long, whatever the time since it last called.
func TestLauncherPlayIsHeld(t *testing.T) {
	l, _ := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000}
	ann := l.hello("", "Ann")
	first := playOf(t, h, ann.Key)
	held := func(seen string) chan launcherapi.Play {
		answer := make(chan launcherapi.Play, 1)
		go func() {
			_, body := asLauncher(h, http.MethodGet, "/launcher/play?seen="+seen, ann.Key, "")
			var play launcherapi.Play
			json.Unmarshal([]byte(body), &play)
			answer <- play
		}()
		return answer
	}
	// an answer that the launcher does not have comes at once
	select {
	case play := <-held("another"):
		if play.Version != first.Version {
			t.Errorf("the answer to a launcher that has another: %+v, after %+v", play, first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server held a call whose answer the launcher did not have")
	}
	// the answer it has is held, and the launcher counts as running meanwhile
	answer := held(first.Version)
	waitFor(t, "the server holds the call", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.playerOf(ann.Token).launcher.held == 1
	})
	l.mu.Lock()
	l.playerOf(ann.Token).launcher.seen = time.Now().Add(-time.Hour)
	open := l.launcherOpen(l.playerOf(ann.Token))
	l.launcherSweep(l.playerOf(ann.Token))
	open = open && l.launcherOpen(l.playerOf(ann.Token))
	l.mu.Unlock()
	if !open {
		t.Error("a launcher whose call the server holds does not count as running")
	}
	select {
	case play := <-answer:
		t.Fatalf("the server answered a call whose answer had not changed: %+v", play)
	case <-time.After(100 * time.Millisecond):
	}
	// a change for the player answers it
	do(l, ann.Token, "create", "map", "zm_standard_town")
	select {
	case play := <-answer:
		if play.Room == "" || play.Version == first.Version {
			t.Errorf("the answer after the player made a T6 room: %+v", play)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not answer the held call when its answer changed")
	}
	l.mu.Lock()
	left := l.playerOf(ann.Token).launcher.held
	l.mu.Unlock()
	if left != 0 {
		t.Errorf("the server counts %d held calls after it answered", left)
	}
	// a call whose answer does not change is answered after launcherHold, with the same answer
	was := launcherHold
	launcherHold = 50 * time.Millisecond
	defer func() { launcherHold = was }()
	now := playOf(t, h, ann.Key)
	select {
	case play := <-held(now.Version):
		if play.Version != now.Version {
			t.Errorf("the answer after the hold: %+v, after %+v", play, now)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server held a call for longer than launcherHold")
	}
}

// What the launcher is to play: its player's T6 room's game, once its server is up, with the password and the name
// the page's line has, at the port players reach; nothing for a key the server doesn't know. Its calls say where the
// player's game is, as their page's do.
func TestLauncherPlay(t *testing.T) {
	l, target := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000, launcher: []byte("the launcher"), launcherHash: "abc"}
	ann, bob := l.hello("", "Ann"), l.hello("", "Ann")
	a, b := ann.Token, bob.Token
	for _, key := range []string{"", "0123456789abcdef", ann.Token} {
		if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", key, ""); rsp.StatusCode != http.StatusUnauthorized {
			t.Errorf("the launcher of key %q: %s", key, rsp.Status)
		}
	}
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", "", "", "Authorization", "Bearer "+ann.Key); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a key as a page's token: %s", rsp.Status)
	}
	play := playOf(t, h, ann.Key)
	if version := play.Version; version == "" || play != (launcherapi.Play{Player: "Ann", Launcher: "abc", Version: version}) {
		t.Errorf("in no room: %+v", play)
	}
	// a T5 room is the page's to play
	do(l, a, "create", "map", "zombie_theater")
	if play := playOf(t, h, ann.Key); play.Room != "" {
		t.Errorf("in a T5 room: %+v", play)
	}
	do(l, a, "room", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	if play := playOf(t, h, bob.Key); play.Room != l.viewOf(a).Room.Code || play.Game != nil || play.InGame || play.Player != "Ann" {
		t.Errorf("in a T6 room that is open: %+v", play)
	}
	pagesOpen(l, a, b)
	pagesAt(l, "203.0.113.5", a, b)
	readyAll(l, a, b)
	// the launcher hears of the game once the server's Plutonium is up to date for it, with the server's revision
	waitView(t, l, b, "the T6 game started", func(v view) bool { return v.Room.T6 != nil })
	waitFor(t, "the game, to a launcher", func() bool {
		play = playOf(t, h, bob.Key)
		return play.Game != nil
	})
	if play.Game.Password != l.viewOf(b).Room.Password || play.Plutonium != 1 {
		t.Errorf("the game, to a launcher, as it starts: %+v (%+v)", play, play.Game)
	}
	v := waitView(t, l, b, "the T6 game's server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	if !v.Room.Up {
		t.Error("the room doesn't say that its game's server is up")
	}
	play = playOf(t, h, bob.Key)
	if !l.viewOf(a).Room.CanEnd {
		t.Error("the host may not end a game no one is in yet")
	}
	if play.Game == nil || play.Game.ID == "" ||
		*play.Game != (launcherapi.Game{ID: play.Game.ID, Port: 4000, Password: v.Room.T6.Password, Name: "Ann 2"}) ||
		v.Room.T6.Name != "Ann 2" || play.InGame {
		t.Fatalf("the game, to the second Ann's launcher: %+v (%+v), where the page has %+v", play, play.Game, v.Room.T6)
	}
	// the launcher called from this PC, which the player's game then comes from: the relay takes it to their seat,
	// whatever address their page is at
	game, err := net.Dial("udp4", target)
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	conn := game.(*net.UDPConn)
	if !udpSilent(conn, []byte("\xff\xff\xff\xffconnectReq Ann 2")) {
		t.Fatal("a game from an address no call of its player came from was let in")
	}
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", bob.Key, "", "from", "127.0.0.1:5000"); rsp.StatusCode != http.StatusOK {
		t.Fatal(rsp.Status)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("\xff\xff\xff\xffconnectReq Ann 2"))
	if _, err := conn.Read(make([]byte, 2048)); err != nil {
		t.Fatalf("the game of a player whose launcher called from its address: %v", err)
	}
	waitView(t, l, b, "the second Ann in the game", func(v view) bool {
		return v.Room.Members[1].InGame
	})
	if play := playOf(t, h, bob.Key); play.Game != nil || !play.InGame {
		t.Errorf("in the game already: %+v", play)
	}
	// the first Ann left the room: no game of it is theirs
	do(l, a, "leave")
	if play := playOf(t, h, ann.Key); play.Room != "" || play.Game != nil {
		t.Errorf("out of the room: %+v", play)
	}
}

// Where players sign in with Discord, a key is good while its player has a session.
func TestLauncherNeedsASession(t *testing.T) {
	d := newFakeDiscord(t)
	d.accounts["100"] = &fakeAccount{username: "ann", member: true}
	l, h := discordLobby(t, d)
	_, cookie := signIn(t, d, h, "/", "100")
	who := helloWith(l, cookie)
	if play := playOf(t, h, who.Key); play.Player != "ann" {
		t.Errorf("signed in: %+v", play)
	}
	req := httptest.NewRequest(http.MethodPost, "/lobby/signout", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+who.Token)
	req.AddCookie(cookie)
	l.ServeHTTP(httptest.NewRecorder(), req)
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", who.Key, ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed out: %s", rsp.Status)
	}
}

// T6's game files, as the launcher downloads them: the list of what the folder has, but what a game or a launcher
// wrote there; each file whole, or from where a launcher left off, when it is still the file it has the start of; at
// the server's rate; and only where the server sends game files.
func TestLauncherFiles(t *testing.T) {
	l := testLobby(t)
	dir := filepath.Join(t.TempDir(), "t6")
	big := noise(300<<10, 3)
	files := map[string]string{
		"zone/all/patch_zm.ff": "the patch", "zone/all/zm_transit.ff": big, "binkw32.dll": "bink", "t6zm.exe": "the game",
		"video/zm (1).webm": "a video",
	}
	for name, text := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), text)
	}
	for _, name := range []string{"players/settings.cfg", "console.log", "zone/all/x.ff.part", launcherapi.StateFile,
		"sound/con.sabs", "video/a;b.bik"} {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), "not sent")
	}
	h := &web{lobby: l, t6Dir: dir, downloads: true, pace: &pacer{}}
	key := l.hello("", "Ann").Key
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/t6/files", "", ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the list, to no player: %s", rsp.Status)
	}
	rsp, body := asLauncher(h, http.MethodGet, "/launcher/t6/files", key, "")
	var list launcherapi.Files
	if err := json.Unmarshal([]byte(body), &list); err != nil || rsp.StatusCode != http.StatusOK {
		t.Fatalf("the list: %s, %q (%v)", rsp.Status, body, err)
	}
	var bytes int64
	for _, f := range list.Files {
		sum := launcherapi.NewHash()
		sum.Write([]byte(files[f.Path]))
		if text, ok := files[f.Path]; !ok || f.Size != int64(len(text)) || f.Hash != launcherapi.HashOf(sum) {
			t.Errorf("listed: %+v", f)
		}
		bytes += f.Size
	}
	if len(list.Files) != len(files) || list.Bytes != bytes || !strings.HasPrefix(list.Version, "v") {
		t.Errorf("the list has %d files of %d bytes, version %q: %+v", len(list.Files), list.Bytes, list.Version, list.Files)
	}
	// a file, whole
	sum := launcherapi.NewHash()
	sum.Write([]byte(big))
	etag := `"` + launcherapi.HashOf(sum) + `"`
	rsp, body = asLauncher(h, http.MethodGet, "/launcher/t6/files/zone/all/zm_transit.ff", key, "")
	if rsp.StatusCode != http.StatusOK || body != big || rsp.Header.Get("ETag") != etag {
		t.Errorf("a file: %s, %d bytes, ETag %s", rsp.Status, len(body), rsp.Header.Get("ETag"))
	}
	// the rest of it, for a launcher that has its start; all of it when the start is another file's
	rsp, body = asLauncher(h, http.MethodGet, "/launcher/t6/files/zone/all/zm_transit.ff", key, "", "Range", "bytes=1000-",
		"If-Range", etag)
	if rsp.StatusCode != http.StatusPartialContent || body != big[1000:] ||
		!strings.HasPrefix(rsp.Header.Get("Content-Range"), "bytes 1000-") {
		t.Errorf("the rest of a file: %s, %d bytes, %q", rsp.Status, len(body), rsp.Header.Get("Content-Range"))
	}
	rsp, body = asLauncher(h, http.MethodGet, "/launcher/t6/files/zone/all/zm_transit.ff", key, "", "Range", "bytes=1000-",
		"If-Range", `"0123456789abcdef0123456789abcdef"`)
	if rsp.StatusCode != http.StatusOK || body != big {
		t.Errorf("the rest of a file that is another now: %s, %d bytes", rsp.Status, len(body))
	}
	if rsp, body = asLauncher(h, http.MethodGet, "/launcher/t6/files/video/zm%20(1).webm", key, ""); body != "a video" {
		t.Errorf("a file with a space in its name: %s, %q", rsp.Status, body)
	}
	// nothing but the list's files
	for _, path := range []string{"players/settings.cfg", "console.log", "../t6/binkw32.dll", "zone/all", "zone/all/",
		"BINKW32.DLL", "sound/con.sabs", ""} {
		if rsp, body := asLauncher(h, http.MethodGet, "/launcher/t6/files/"+path, key, ""); rsp.StatusCode == http.StatusOK && path != "" {
			t.Errorf("%q was sent: %q", path, body)
		}
	}
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/t6/files/binkw32.dll", "", ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a file, to no player: %s", rsp.Status)
	}
	// a file that changed since the list is not the one the list names
	os.WriteFile(filepath.Join(dir, "binkw32.dll"), []byte("another bink"), 0o644)
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/t6/files/binkw32.dll", key, ""); rsp.StatusCode != http.StatusConflict {
		t.Errorf("a file that changed since the list: %s", rsp.Status)
	}
	if _, body := asLauncher(h, http.MethodGet, "/launcher/t6/files", key, ""); !strings.Contains(body, `"size":12`) {
		t.Errorf("the next list has not the file as it is now: %s", body)
	}
	// at the server's rate: 300 kB at 1 MB a second take a quarter of a second
	h.pace = &pacer{rate: 1 << 20}
	began := time.Now()
	if _, body := asLauncher(h, http.MethodGet, "/launcher/t6/files/zone/all/zm_transit.ff", key, ""); body != big {
		t.Error("a paced file is not the file")
	}
	if took := time.Since(began); took < 150*time.Millisecond {
		t.Errorf("300 kB at 1 MB/s took %s", took)
	}
	// a server that sends no game files sends none of T6's
	h.downloads = false
	for _, path := range []string{"/launcher/t6/files", "/launcher/t6/files/binkw32.dll"} {
		if rsp, _ := asLauncher(h, http.MethodGet, path, key, ""); rsp.StatusCode != http.StatusForbidden {
			t.Errorf("%s from a server that sends no game files: %s", path, rsp.Status)
		}
	}
}

// How a launcher's download goes is on its player in their T6 room, until it ends, or says nothing more.
func TestLauncherReport(t *testing.T) {
	l, _ := t6Lobby(t)
	h := &web{lobby: l}
	ann := l.hello("", "Ann")
	do(l, ann.Token, "create", "map", "zm_standard_town")
	member := func() memberView { return l.viewOf(ann.Token).Room.Members[0] }
	report := func(body string) {
		t.Helper()
		if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/files", ann.Key, body); rsp.StatusCode != http.StatusOK {
			t.Fatalf("a report %s: %s", body, rsp.Status)
		}
	}
	if m := member(); m.Downloading || m.Missing || m.Got != 0 {
		t.Errorf("before any report: %+v", m)
	}
	report(`{"running": true, "got": 250, "need": 1000}`)
	if m := member(); !m.Downloading || m.Got != 0.25 || m.Missing {
		t.Errorf("a quarter in: %+v", m)
	}
	report(`{"running": true, "got": 5000, "need": 1000}`)
	if m := member(); !m.Downloading || m.Got != 1 {
		t.Errorf("more than all of it: %+v", m)
	}
	report(`{"running": false, "got": 1000, "need": 1000}`)
	if m := member(); m.Downloading || m.Got != 0 {
		t.Errorf("done: %+v", m)
	}
	// a launcher that was closed says nothing more
	report(`{"running": true, "got": 10, "need": 1000}`)
	l.mu.Lock()
	l.playerOf(ann.Token).launcher.at = time.Now().Add(-launcherQuiet)
	l.mu.Unlock()
	waitFor(t, "the download off the player", func() bool { return !member().Downloading })
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/files", ann.Key, "no report"); rsp.StatusCode != http.StatusBadRequest {
		t.Errorf("a report that is none: %s", rsp.Status)
	}
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/files", "", "{}"); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a report of no player: %s", rsp.Status)
	}
}

// The line that joins a T6 room's game by hand is the room's: its players have its password, and their names in its
// games, from the start, and its games ask for that password.
func TestT6RoomPassword(t *testing.T) {
	l, _ := t6Lobby(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Ann").Token
	do(l, a, "create", "map", "zombie_theater")
	if v := l.viewOf(a); v.Room.Password != "" || v.Room.As != "" {
		t.Errorf("a T5 room: password %q, as %q", v.Room.Password, v.Room.As)
	}
	do(l, a, "room", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	va, vb := l.viewOf(a), l.viewOf(b)
	if len(va.Room.Password) != 8 || vb.Room.Password != va.Room.Password || va.Room.As != "Ann" || vb.Room.As != "Ann 2" {
		t.Fatalf("an open T6 room: %q as %q, and %q as %q", va.Room.Password, va.Room.As, vb.Room.Password, vb.Room.As)
	}
	if list := l.viewOf(l.hello("", "Cy").Token); strings.Contains(fmt.Sprintf("%+v", list), va.Room.Password) {
		t.Error("the room's password, to a player who is not in it")
	}
	pagesOpen(l, a, b)
	readyAll(l, a, b)
	v := waitView(t, l, a, "the T6 game's server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	if v.Room.T6.Password != va.Room.Password {
		t.Errorf("the game's password is %q, the room's %q", v.Room.T6.Password, va.Room.Password)
	}
}

// The launcher itself, where the server's build has it.
func TestLauncherServed(t *testing.T) {
	h := &web{lobby: testLobby(t), certs: &certificates{}}
	srv := httptest.NewServer(h)
	defer srv.Close()
	if rsp, _ := get(t, srv.URL+"/zombies-launcher-setup.exe"); rsp.StatusCode != http.StatusNotFound {
		t.Errorf("the launcher of a build without it: %s", rsp.Status)
	}
	if _, body := get(t, srv.URL+"/server"); strings.Contains(body, "launcher") {
		t.Errorf("a server with no launcher says %s", body)
	}
	h.launcher, h.launcherHash = []byte("MZ, the launcher"), "abc"
	rsp, body := get(t, srv.URL+"/zombies-launcher-setup.exe")
	if body != "MZ, the launcher" || !strings.Contains(rsp.Header.Get("Content-Disposition"), `filename="zombies-launcher-setup.exe"`) {
		t.Errorf("the launcher: %s, %q, %q", rsp.Status, body, rsp.Header.Get("Content-Disposition"))
	}
	if _, body := get(t, srv.URL+"/server"); !strings.Contains(body, `"launcher":true`) {
		t.Errorf("a server with the launcher says %s", body)
	}
}
