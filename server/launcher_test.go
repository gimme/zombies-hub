package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"zombies/internal/launcherapi"
)

// asLauncher is a call of a player's launcher, by their key, and its answer's status and body. The launcher is of the
// lowest version that the server takes. header is the call's headers, a name and a value each, where "" leaves a
// header out; "from" is the address it comes from.
func asLauncher(h http.Handler, method, path, key, body string, header ...string) (*http.Response, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", launcherapi.Key+key)
	}
	req.Header.Set(launcherapi.VersionHeader, strconv.Itoa(launcherapi.MinVersion))
	for i := 0; i+1 < len(header); i += 2 {
		switch {
		case header[i] == "from": // the address the call comes from
			req.RemoteAddr = header[i+1]
		case header[i+1] == "":
			req.Header.Del(header[i])
		default:
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

// What the launcher is to play: its player's T6 room's game, once the server's Plutonium is up to date for it, with
// the password and the name the page's line has, at the port players reach, and whether Plutonium's server is up yet;
// no game for a player who joins by hand; nothing for a key the server doesn't know. Its calls say where the player's
// game is, as their page's do.
func TestLauncherPlay(t *testing.T) {
	l, target := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000}
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
	// in no room, the answer has what the server says of each title that a launcher starts: its maps' zones
	titles := map[string]launcherapi.Title{t6: {Zones: []string{"zone/all/zm_tomb.ff", "zone/all/zm_transit.ff"}}}
	if version := play.Version; version == "" ||
		!reflect.DeepEqual(play, launcherapi.Play{Player: "Ann", Titles: titles, Version: version}) {
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
		*play.Game != (launcherapi.Game{ID: play.Game.ID, Port: 4000, Password: v.Room.T6.Password, Name: "Ann 2", Up: true}) ||
		v.Room.T6.Name != "Ann 2" || play.InGame {
		t.Fatalf("the game, to the second Ann's launcher: %+v (%+v), where the page has %+v", play, play.Game, v.Room.T6)
	}
	// a player who joins by hand: their launcher is sent no game, until they no longer do
	do(l, b, "hand", "hand", "true")
	if play := playOf(t, h, bob.Key); play.Game != nil || play.Room == "" {
		t.Errorf("to the launcher of a player who joins by hand: %+v", play)
	}
	do(l, b, "hand", "hand", "false")
	if play := playOf(t, h, bob.Key); play.Game == nil {
		t.Errorf("to the launcher of a player who no longer joins by hand: %+v", play)
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

// A launcher says that it started before it looks for a later release of itself, whatever its version: it counts as
// running from then on, and its last failure goes, until a call of it that the server doesn't take.
func TestLauncherStarted(t *testing.T) {
	l := testLobby(t)
	h := &web{lobby: l}
	ann := l.hello("", "Ann")
	l.mu.Lock()
	l.playerOf(ann.Token).launcher.Failed = "The last try failed."
	l.mu.Unlock()
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/started", ann.Key, "", launcherapi.VersionHeader, ""); rsp.StatusCode != http.StatusOK {
		t.Fatalf("a launcher of no version, started: %s", rsp.Status)
	}
	if you := l.viewOf(ann.Token).You; !you.Launcher || you.LauncherFailed != "" || you.LauncherOld {
		t.Errorf("a launcher that started: %+v", you)
	}
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", ann.Key, "", launcherapi.VersionHeader, ""); rsp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("its next call: %s", rsp.Status)
	}
	if you := l.viewOf(ann.Token).You; you.Launcher || !you.LauncherOld {
		t.Errorf("a launcher that the server doesn't take, once it called: %+v", you)
	}
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/started", "0123456789abcdef", ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the launcher of a key the server doesn't know, started: %s", rsp.Status)
	}
}

// A launcher that its player quits says so, whatever its version, and then no longer counts as running: at once when
// the server holds no call of it, and else as soon as the call it had open ends. One that starts again runs again.
func TestLauncherStopped(t *testing.T) {
	l, _ := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000}
	ann := l.hello("", "Ann")
	running := func() bool { return l.viewOf(ann.Token).You.Launcher }
	stopped := func() {
		t.Helper()
		rsp, _ := asLauncher(h, http.MethodPost, "/launcher/stopped", ann.Key, "", launcherapi.VersionHeader, "")
		if rsp.StatusCode != http.StatusOK {
			t.Fatalf("a launcher of no version, stopped: %s", rsp.Status)
		}
	}
	first := playOf(t, h, ann.Key)
	if !running() {
		t.Fatal("a launcher that called does not count as running")
	}
	stopped()
	if running() {
		t.Error("a launcher that said its player quit it still counts as running")
	}
	// while the server holds its call, it runs until the call ends
	first = playOf(t, h, ann.Key)
	req := httptest.NewRequest(http.MethodGet, "/launcher/play?seen="+first.Version, nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", launcherapi.Key+ann.Key)
	req.Header.Set(launcherapi.VersionHeader, strconv.Itoa(launcherapi.MinVersion))
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	waitFor(t, "the server holds the call", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.playerOf(ann.Token).launcher.held == 1
	})
	stopped()
	if !running() {
		t.Error("a launcher that was quit does not count as running while the server still holds its call")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server held the call of a launcher that closed it")
	}
	if running() {
		t.Error("a launcher that was quit counts as running after its call ended")
	}
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/started", ann.Key, ""); rsp.StatusCode != http.StatusOK || !running() {
		t.Errorf("a launcher that started again: %s, running %v", rsp.Status, running())
	}
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/stopped", "0123456789abcdef", ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the launcher of a key the server doesn't know, stopped: %s", rsp.Status)
	}
}

// A launcher that has to be installed again on its player's Mac says so, and ends. The room then shows the line that
// installs it, as for a launcher too old for the server, and not the failure: until the next report of a launcher of
// the player's.
func TestLauncherSetup(t *testing.T) {
	l, _ := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000}
	ann := l.hello("", "Ann")
	playOf(t, h, ann.Key)
	report := func(body string) {
		t.Helper()
		if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/files", ann.Key, body); rsp.StatusCode != http.StatusOK {
			t.Fatalf("a report: %s", rsp.Status)
		}
	}
	report(`{"failed":"This Mac's Zombies Launcher needs an update.","setup":true}`)
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/stopped", ann.Key, ""); rsp.StatusCode != http.StatusOK {
		t.Fatalf("stopped: %s", rsp.Status)
	}
	if you := l.viewOf(ann.Token).You; you.Launcher || !you.LauncherOld || you.LauncherFailed != "" {
		t.Errorf("a launcher that has to be installed again, once it ended: %+v", you)
	}
	// the next launcher of hers
	if rsp, _ := asLauncher(h, http.MethodPost, "/launcher/started", ann.Key, ""); rsp.StatusCode != http.StatusOK {
		t.Fatalf("started: %s", rsp.Status)
	}
	playOf(t, h, ann.Key)
	report(`{}`)
	if you := l.viewOf(ann.Token).You; !you.Launcher || you.LauncherOld {
		t.Errorf("the launcher after one that had to be installed again: %+v", you)
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

// T6's game files, as the launcher downloads them: the list of what the folder has of Zombies, but what a game or a launcher
// wrote there; each file whole, or from where a launcher left off, when it is still the file it has the start of; at
// the server's rate; and only where the server sends game files.
func TestLauncherFiles(t *testing.T) {
	l := testLobby(t)
	dir := filepath.Join(t.TempDir(), "t6")
	big := noise(300<<10, 3)
	files := map[string]string{
		"zone/all/patch_zm.ff": "the patch", "zone/all/zm_transit.ff": big, "binkw32.dll": "bink", "t6zm.exe": "the game",
		"video/zm_tomb (1).webm": "a video",
	}
	for name, text := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), text)
	}
	for _, name := range []string{"players/settings.cfg", "console.log", "zone/all/x.ff.part", launcherapi.StateFile,
		"sound/con.sabs", "video/zm_a;b.webm", "t6mp.exe", "zone/all/mp_nuketown_2020.ff", "sound/mpl_nuketown.all.sabs"} {
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
	if rsp, body = asLauncher(h, http.MethodGet, "/launcher/t6/files/video/zm_tomb%20(1).webm", key, ""); body != "a video" {
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

// The line that joins a T6 room's game by hand is the room's: its players have its password from the start, and its
// games ask for that password.
func TestT6RoomPassword(t *testing.T) {
	l, _ := t6Lobby(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Ann").Token
	do(l, a, "create", "map", "zombie_theater")
	if v := l.viewOf(a); v.Room.Password != "" {
		t.Errorf("a T5 room: password %q", v.Room.Password)
	}
	do(l, a, "room", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	va, vb := l.viewOf(a), l.viewOf(b)
	if len(va.Room.Password) != 8 || vb.Room.Password != va.Room.Password {
		t.Fatalf("an open T6 room: %q, and %q", va.Room.Password, vb.Room.Password)
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

// The server takes the calls of a launcher of a version that its code works with, and of a dev build on a dev server.
// It refuses the others, each with a sentence for the player, and the player's page says that their launcher is too
// old, until a launcher of theirs that the server takes calls. The page links to the launcher's latest release.
func TestLauncherVersion(t *testing.T) {
	l, _ := t6Lobby(t)
	h := &web{lobby: l, certs: &certificates{}}
	ann := l.hello("", "Ann")
	do(l, ann.Token, "create", "map", "zm_standard_town")
	old := func() bool { return l.viewOf(ann.Token).You.LauncherOld }
	// a launcher that sends no version
	rsp, body := asLauncher(h, http.MethodGet, "/launcher/play", ann.Key, "", launcherapi.VersionHeader, "")
	if rsp.StatusCode != http.StatusUpgradeRequired || !strings.Contains(body, "too old") || !old() {
		t.Errorf("a launcher with no version: %s, %q; the page says it is too old: %v", rsp.Status, body, old())
	}
	if v := l.viewOf(ann.Token).You; v.Launcher {
		t.Errorf("a launcher that was refused runs, to the page: %+v", v)
	}
	// a launcher that the server takes
	playOf(t, h, ann.Key)
	if old() {
		t.Error("the page still says that the player's launcher is too old")
	}
	// one of a version below the lowest, a dev build on a server that is no dev server, and a version that is none
	for _, version := range []string{strconv.Itoa(launcherapi.MinVersion - 1), "0", "-3", "seven"} {
		for _, call := range [][2]string{{http.MethodGet, "/launcher/play"}, {http.MethodPost, "/launcher/files"}} {
			rsp, body := asLauncher(h, call[0], call[1], ann.Key, `{"running": true, "need": 100}`,
				launcherapi.VersionHeader, version)
			if rsp.StatusCode != http.StatusUpgradeRequired || !strings.HasSuffix(strings.TrimSpace(body), ".") || !old() {
				t.Errorf("%s %s by a launcher of version %q: %s, %q; the page says it is too old: %v", call[0], call[1],
					version, rsp.Status, body, old())
			}
		}
	}
	if m := l.viewOf(ann.Token).Room.Members[0]; m.Downloading {
		t.Errorf("the report of a launcher that was refused: %+v", m)
	}
	// a dev server takes a dev build, and nothing older than the lowest version
	h.dev = true
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", ann.Key, "", launcherapi.VersionHeader, "0"); rsp.StatusCode != http.StatusOK || old() {
		t.Errorf("a dev build on a dev server: %s; the page says it is too old: %v", rsp.Status, old())
	}
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/play", ann.Key, "", launcherapi.VersionHeader, "-3"); rsp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("a launcher of version -3 on a dev server: %s", rsp.Status)
	}
	// the page links to the launcher's latest release
	srv := httptest.NewServer(h)
	defer srv.Close()
	if _, body := get(t, srv.URL+"/server"); !strings.Contains(body,
		`"launcher":"https://github.com/gimme/zombies-hub/releases/download/launcher/zombies-launcher-setup.exe"`) {
		t.Errorf("the server says %s", body)
	}
}

// A player's launcher says which of the maps' zones the game folder has that their game starts from. A room on a map
// in another zone shows the player as missing the map, and takes them as ready only once the folder has it. A
// launcher that says nothing of the folder, as while it still gets the game files, is taken to have every map.
func TestLauncherSaysWhatTheCopyHas(t *testing.T) {
	l, _ := t6Lobby(t)
	l.minPlayers = 1
	h := &web{lobby: l, certs: &certificates{}}
	ann := l.hello("", "Ann")
	do(l, ann.Token, "create", "map", "zm_classic_tomb")
	missing := func() bool { return l.viewOf(ann.Token).Room.Members[0].Missing }
	const tomb, transit = "zone/all/zm_tomb.ff", "zone/all/zm_transit.ff"
	// the answer names the zones of the server's maps, and the one of the room's map
	if play := playOf(t, h, ann.Key); !slices.Equal(play.Zones, []string{tomb, transit}) || play.Needs != tomb {
		t.Errorf("the zones: %q, of which the room's map is in %q", play.Zones, play.Needs)
	}
	if missing() {
		t.Error("a launcher that said nothing of the game folder: the player misses the map")
	}
	says := func(has string) {
		t.Helper()
		rsp, body := asLauncher(h, http.MethodGet, "/launcher/play?ready=1&has="+has, ann.Key, "")
		if rsp.StatusCode != http.StatusOK {
			t.Fatalf("/launcher/play?has=%s: %s, %q", has, rsp.Status, body)
		}
	}
	// a copy of the game without the pack of the room's map
	says(transit)
	if !missing() {
		t.Error("a game folder without the zone of the room's map: the player does not miss the map")
	}
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "You don't have the room's map yet." {
		t.Errorf("ready without the room's map: %q", err)
	}
	// the host picks a map that the copy has
	do(l, ann.Token, "room", "map", "zm_standard_town")
	if play := playOf(t, h, ann.Key); play.Needs != transit {
		t.Errorf("the room's map is in %q", play.Needs)
	}
	says(transit)
	if missing() {
		t.Error("the player misses a map whose zone the game folder has")
	}
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "" {
		t.Errorf("ready with the room's map: %q", err)
	}
	do(l, ann.Token, "ready", "ready", "false")
	// back on the map it lacks; the launcher then plays from the server's game files, which have every map
	do(l, ann.Token, "room", "map", "zm_classic_tomb")
	if !missing() {
		t.Error("back on the map that the game folder lacks: the player does not miss it")
	}
	says(tomb + "," + transit)
	if missing() {
		t.Error("a game folder with every zone: the player misses the map")
	}
	// a folder with none of the zones is not one that the launcher said nothing of
	says("")
	if !missing() {
		t.Error("a game folder with none of the zones: the player does not miss the map")
	}
	// a ready player whose folder no longer has the map is not ready
	says(tomb)
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "" {
		t.Fatalf("ready: %q", err)
	}
	says(transit)
	if l.viewOf(ann.Token).You.Ready || !missing() {
		t.Error("a ready player whose game folder lost the room's map is still ready")
	}
}

// A launcher starts the titles that a player's PC plays. The server tells it of each one that it hosts, takes it for
// a title only from the version that plays the title, and keeps what it says of a title's game folder apart from
// another title's.
func TestLauncherTitles(t *testing.T) {
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_theater", "zm_standard_town", "zm_factory", "zm_tomb"} }
	dir, _ := t7GameFiles(t, "zm_factory", "zm_tomb")
	h := &web{lobby: l, publicPort: 4000, t7Send: dir, downloads: true, pace: &pacer{}}
	ann := l.hello("", "Ann")
	a := ann.Token
	fits := strconv.FormatInt(launcherapi.MinVersionOf(t7), 10)
	old := strconv.FormatInt(launcherapi.MinVersionOf(t7)-1, 10)
	asked := func(path, version string) (int, launcherapi.Play) {
		rsp, body := asLauncher(h, http.MethodGet, path, ann.Key, "", launcherapi.VersionHeader, version)
		var play launcherapi.Play
		json.Unmarshal([]byte(body), &play)
		return rsp.StatusCode, play
	}
	// each title's zones, and whether the server sends its game files: T7's here, and none of T6, which it has no
	// folder of
	_, play := asked("/launcher/play", fits)
	want := map[string]launcherapi.Title{
		t6: {Zones: []string{"zone/all/zm_transit.ff"}},
		t7: {Zones: []string{"zone/zm_factory.ff", "zone/zm_tomb.ff"}, Downloads: true},
	}
	if !reflect.DeepEqual(play.Titles, want) || play.Downloads || play.Room != "" {
		t.Errorf("the titles, to a launcher: %+v", play)
	}
	// a launcher from before the launcher started Black Ops III is taken in a T6 room, and when it asks of T6, and
	// refused in a T7 room, and when it asks of T7: its room then says that it is too old
	do(l, a, "create", "map", "zm_standard_town")
	if status, play := asked("/launcher/play", old); status != http.StatusOK || play.Of != t6 || play.Room == "" ||
		!slices.Equal(play.Zones, want[t6].Zones) {
		t.Errorf("an older launcher, in a T6 room: %d, %+v", status, play)
	}
	if status, _ := asked("/launcher/play?of=t7", old); status != http.StatusUpgradeRequired {
		t.Errorf("an older launcher that asks of T7: %d", status)
	}
	if status, _ := asked("/launcher/t7/files", old); status != http.StatusUpgradeRequired {
		t.Errorf("T7's game files, to an older launcher: %d", status)
	}
	asked("/launcher/play", old) // the launcher is taken again
	do(l, a, "room", "title", t7, "map", "zm_tomb")
	if status, _ := asked("/launcher/play", old); status != http.StatusUpgradeRequired || !l.viewOf(a).You.LauncherOld {
		t.Errorf("an older launcher, in a T7 room: %d, old %v", status, l.viewOf(a).You.LauncherOld)
	}
	status, play := asked("/launcher/play?of=t7&ready=1&has=zone/zm_factory.ff", fits)
	if status != http.StatusOK || play.Of != t7 || play.Needs != "zone/zm_tomb.ff" || play.Zones != nil || play.Game != nil {
		t.Errorf("a launcher, in a T7 room: %d, %+v", status, play)
	}
	// the launcher said that the game folder lacks the room's map: the room shows it, and what the folder has
	v := l.viewOf(a)
	if !v.Room.Members[0].Missing || !v.You.LauncherReady || v.You.LauncherOf != t7 || !v.You.LauncherFolder ||
		!slices.Equal(v.You.LauncherMaps, []string{"zm_factory"}) {
		t.Errorf("the room of a player whose copy lacks its map: %+v, %+v", v.Room.Members[0], v.You)
	}
	if err := l.setReady(l.playerOf(a), true); err == nil {
		t.Error("a player whose copy lacks the room's map is ready")
	}
	// a launcher that is at T6 has said nothing of T7's folder, and has the PC ready for another game than the room's
	asked("/launcher/play?ready=1&has=zone/all/zm_transit.ff", fits)
	if v := l.viewOf(a); v.Room.Members[0].Missing || v.You.LauncherReady || v.You.LauncherOf != t6 {
		t.Errorf("the room of a player whose launcher is at another title: %+v, %+v", v.Room.Members[0], v.You)
	}
	asked("/launcher/play?of=t7&ready=1&has=zone/zm_factory.ff,zone/zm_tomb.ff", fits)
	if v := l.viewOf(a); v.Room.Members[0].Missing || !v.You.LauncherReady || len(v.You.LauncherMaps) != 2 || v.You.LauncherGame {
		t.Errorf("the room of a player whose copy has its map: %+v, %+v", v.Room.Members[0], v.You)
	}
	// the launcher says that it started the player's game, which runs: of the room's title, and of another, and that
	// the game is closed
	asked("/launcher/play?of=t7&ready=1&game=1&has=zone/zm_factory.ff,zone/zm_tomb.ff", fits)
	if v := l.viewOf(a); !v.You.LauncherGame || !v.You.LauncherReady {
		t.Errorf("the room of a player whose game runs: %+v", v.You)
	}
	asked("/launcher/play?ready=1&game=1", fits)
	if v := l.viewOf(a); v.You.LauncherGame {
		t.Errorf("the room of a player whose game of another title runs: %+v", v.You)
	}
	asked("/launcher/play?of=t7&ready=1&has=zone/zm_factory.ff,zone/zm_tomb.ff", fits)
	if v := l.viewOf(a); v.You.LauncherGame || !v.You.LauncherReady {
		t.Errorf("the room of a player whose game was closed: %+v", v.You)
	}
}

// The server sends Black Ops III's game files in packs: one for each map, and one that every map needs. The page has
// their sizes once the server has listed the files. A server whose folder has only what a server reads sends none.
func TestLauncherPacks(t *testing.T) {
	l := testLobby(t)
	l.maps = func() []string { return []string{"zm_factory", "zm_tomb"} }
	dir, _ := t7GameFiles(t, "zm_factory", "zm_tomb")
	for _, name := range []string{"boiii.exe", "boiii_players/properties.json", "zone/mp_common.ff", "CrashUploader.exe"} {
		writeTestFile(t, filepath.Join(dir, filepath.FromSlash(name)))
	}
	if !t7Sendable(dir) {
		t.Fatal("a whole install is not one to send")
	}
	h := &web{lobby: l, t7Send: dir, downloads: true, pace: &pacer{}}
	l.sends = h.sentPacks
	key := l.hello("", "Ann")
	if downloads, packs := h.sentPacks(t7); !downloads || packs != nil {
		t.Errorf("before the server listed the files: sends %v, packs %v", downloads, packs)
	}
	rsp, body := asLauncher(h, http.MethodGet, "/launcher/t7/files", key.Key, "", launcherapi.VersionHeader,
		strconv.FormatInt(launcherapi.MinVersionOf(t7), 10))
	var list launcherapi.Files
	if err := json.Unmarshal([]byte(body), &list); err != nil || rsp.StatusCode != http.StatusOK {
		t.Fatalf("the list: %s, %q (%v)", rsp.Status, body, err)
	}
	packs := map[string][]string{}
	for _, f := range list.Files {
		packs[f.Pack] = append(packs[f.Pack], f.Path)
	}
	want := map[string][]string{
		"": {"BlackOps3.exe", "zone/base.xpak", "zone/core_common.ff", "zone/en_zm_common.ff", "zone/snd/all/zm_common.all.sabs",
			"zone/zm_common.fd", "zone/zm_common.ff", "zone/zm_levelcommon.ff", "zone/zm_levelcommon.xpak", "zone/zm_patch.ff"},
		"zm_factory": {"zone/en_zm_factory.ff", "zone/zm_factory.ff", "zone/zm_factory.xpak", "zone/zm_factory_patch.ff"},
		"zm_tomb":    {"zone/en_zm_tomb.ff", "zone/zm_tomb.ff", "zone/zm_tomb.xpak", "zone/zm_tomb_patch.ff"},
	}
	if !reflect.DeepEqual(packs, want) {
		t.Errorf("the packs: %v", packs)
	}
	rsp, body = asLauncher(h, http.MethodGet, "/launcher/t7/files/zone/zm_tomb.xpak", key.Key, "", launcherapi.VersionHeader,
		strconv.FormatInt(launcherapi.MinVersionOf(t7), 10))
	if rsp.StatusCode != http.StatusOK || body != "zm_tomb.xpak" {
		t.Errorf("a file of a pack: %s, %q", rsp.Status, body)
	}
	// the page has the size of what every map needs on the title, and each map's own on the map
	v := l.viewOf(key.Token)
	title := v.Titles[slices.IndexFunc(v.Titles, func(t titleView) bool { return t.ID == t7 })]
	var shared int64
	for _, path := range want[""] {
		shared += int64(len(filepath.Base(path)))
	}
	if !title.Downloads || title.Bytes != shared {
		t.Errorf("the title, to the page: %+v, where every map needs %d bytes", title, shared)
	}
	for _, m := range v.Maps {
		if m.Name == "zm_tomb" && m.Bytes != int64(len("en_zm_tomb.ffzm_tomb.ffzm_tomb.xpakzm_tomb_patch.ff")) {
			t.Errorf("the map, to the page: %+v", m)
		}
	}
	// a folder with only what a server reads of the game is none to send
	read, _ := t7GameFiles(t, "zm_factory")
	os.Remove(filepath.Join(read, "zone", "base.xpak"))
	if t7Sendable(read) {
		t.Error("a folder without the game's image packs is one to send")
	}
	h.t7Send = ""
	if rsp, _ := asLauncher(h, http.MethodGet, "/launcher/t7/files", key.Key, "", launcherapi.VersionHeader,
		strconv.FormatInt(launcherapi.MinVersionOf(t7), 10)); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("the list, from a server that sends none of T7: %s", rsp.Status)
	}
	if v := l.viewOf(key.Token); v.Titles[0].Downloads || v.Titles[0].Bytes != 0 {
		t.Errorf("the title, to the page of a server that sends none of it: %+v", v.Titles[0])
	}
}
