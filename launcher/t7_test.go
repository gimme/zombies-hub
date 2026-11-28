package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zombies/internal/boiii"
	"zombies/internal/boiii/boiiitest"
	"zombies/internal/launcherapi"
)

// boiiiFiles is what the tests' stand-in for BOIII's updater has: the program, and data that a launcher leaves there.
var boiiiFiles = map[string]string{boiii.Exe: "BOIII's program", "data/launcher/main.html": "BOIII's own launcher"}

// t7Zones are the zones of the T7 maps that the tests' server has.
var t7Zones = []string{"zone/zm_factory.ff", "zone/zm_tomb.ff"}

// t7Launcher is a test launcher on a PC with Steam, where BOIII's updater is the tests' stand-in. A game on BOIII
// that it starts stays open, answers at once that it is at its menu, and takes a line a moment later: at says
// otherwise, where a test sets it. games are the games it started, and lines what was typed into them.
type t7Launcher struct {
	*testLauncher
	updater *boiiitest.Updater
	at      atomic.Value // the map that a game says it is on; "" for a game that doesn't answer
	mu      sync.Mutex
	games   []*fakeGame
}

func newT7Launcher(t *testing.T, answers string) *t7Launcher {
	t.Helper()
	tl := &t7Launcher{testLauncher: newTestLauncher(t, answers), updater: boiiitest.Serve(t, boiiiFiles)}
	tl.at.Store(boiiiMenu)
	tl.boiii, tl.boiiiManifest = filepath.Join(tl.home, "boiii"), tl.updater.URL
	tl.steam = func() bool { return true }
	tl.probe = func(int) (string, bool) {
		at := tl.at.Load().(string)
		return at, at != ""
	}
	tl.settle, tl.retype = 20*time.Millisecond, time.Hour
	tl.programs[titleT7.id] = map[string]string{"blackops3.exe": hashOf("the game")}
	tl.start = func(exe, line, dir string) (game, error) {
		g := &fakeGame{}
		g.open.Store(true)
		tl.mu.Lock()
		defer tl.mu.Unlock()
		tl.started = append(tl.started, started{exe, line, dir})
		tl.games = append(tl.games, g)
		return g, nil
	}
	return tl
}

// game returns the nth game that the launcher started, from 0, or nil when it has started fewer.
func (tl *t7Launcher) game(n int) *fakeGame {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	if n >= len(tl.games) {
		return nil
	}
	return tl.games[n]
}

// runUntilSaid runs the link, and closes the game that the run starts once the server has heard that the game folder
// has the zones has: the run ends with its game, and runUntilSaid returns its exit code. The launcher says what the
// folder has with its next call for what to play, and the end of a run cuts that call off. A game that closed as it
// started would so end the run before the call was made, now and then.
func (tl *t7Launcher) runUntilSaid(t *testing.T, l *fakeServer, link, has string) int {
	t.Helper()
	done := make(chan int, 1)
	go func() { done <- tl.run(context.Background(), []string{link}) }()
	eventually(t, "the game started, and the launcher said that the folder has "+has, func() bool {
		return tl.game(0) != nil && l.said() == has
	})
	tl.game(0).open.Store(false) // the player closes the game
	select {
	case code := <-done:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as its game was closed")
		return -1
	}
}

// t7Copy makes a copy of Black Ops III with the given maps in dir, as Steam installs it, and returns dir.
func t7Copy(t *testing.T, dir string, maps ...string) string {
	t.Helper()
	write(t, filepath.Join(dir, "BlackOps3.exe"), "the game")
	write(t, filepath.Join(dir, "zone", "zm_common.ff"), "what every map loads")
	for _, m := range maps {
		write(t, filepath.Join(dir, "zone", m+".ff"), m)
	}
	return dir
}

// t7Play is the server's answer to the launcher of a player in a T7 room on the map of the zone needs.
func t7Play(l *fakeServer, needs string, game *launcherapi.Game) launcherapi.Play {
	return launcherapi.Play{Player: "Ann", Room: "ABCD", Of: launcherapi.T7, Needs: needs, Game: game,
		Titles: map[string]launcherapi.Title{launcherapi.T7: {Zones: t7Zones}}}
}

// A link names a title, and a map of it: to play alone, or to have the PC made ready for. A launcher that runs is
// told both of the player's next press in a note, and the presses of the two kinds of link each have their own.
func TestLinkOf(t *testing.T) {
	l, err := parseLink("zombies-launcher://host/play?solo=1&name=Ann&of=t7&map=zm_tomb&key=0123456789abcdef")
	if err != nil || l.of != "t7" || l.mapName != "zm_tomb" || !l.solo {
		t.Fatalf("a link to play Black Ops III alone: %+v, %v", l, err)
	}
	for _, bad := range []string{"of=T7", "of=t7%20x", "map=zm_tomb%22", "map=..%2Fx", "of=" + strings.Repeat("a", 65)} {
		if l, err := parseLink("zombies-launcher://host/play?solo=1&name=Ann&key=0123456789abcdef&" + bad); err == nil {
			t.Errorf("a link with %s: %+v", bad, l)
		}
	}
	if titleOf("") != titleT6 || titleOf("t6") != titleT6 || titleOf("t7") != titleT7 || titleOf("t8") != nil {
		t.Error("the titles of the ids")
	}
	home := t.TempDir()
	first, _ := parseLink("zombies-launcher://host/play?key=0123456789abcdef")
	if of, mapName, noted := pressed(home, first, true); of != "" || mapName != "" || noted {
		t.Errorf("with no note: %q, %q, %v", of, mapName, noted)
	}
	notePress(home, l)
	if of, mapName, noted := pressed(home, first, true); of != "t7" || mapName != "zm_tomb" || !noted {
		t.Errorf("the note of a press: %q, %q, %v", of, mapName, noted)
	}
	// the other kind of link: one that has the PC made ready, as the lobby's Play local asks for a title, and a map of it
	ready, err := parseLink("zombies-launcher://host/play?key=0123456789abcdef&of=t7&map=zm_factory")
	if err != nil || ready.of != "t7" || ready.mapName != "zm_factory" || ready.solo || ready.id() != first.id() {
		t.Fatalf("a link that asks for Black Ops III, with a map: %+v, %v", ready, err)
	}
	if of, mapName, noted := pressed(home, first, false); of != "" || mapName != "" || noted {
		t.Errorf("of the presses that launch nothing, with a note of the other kind alone: %q, %q, %v", of, mapName, noted)
	}
	if of, mapName, noted := pressed(home, ready, false); of != "t7" || mapName != "zm_factory" || noted {
		t.Errorf("with no note, the link's own: %q, %q, %v", of, mapName, noted)
	}
	notePress(home, ready)
	if of, mapName, noted := pressed(home, first, false); of != "t7" || mapName != "zm_factory" || !noted {
		t.Errorf("the note of a press that launches nothing: %q, %q, %v", of, mapName, noted)
	}
	if of, mapName, _ := pressed(home, first, true); of != "t7" || mapName != "zm_tomb" {
		t.Errorf("the note of the press that launched, after a press that did not: %q, %q", of, mapName)
	}
	os.WriteFile(pressNote(home, first, true), []byte("t7\n../x\n"), 0o644)
	if of, mapName, noted := pressed(home, first, true); of != "" || mapName != "" || noted {
		t.Errorf("a note that is none: %q, %q, %v", of, mapName, noted)
	}
}

// A game on BOIII plays under the name in BOIII's settings in the game's folder. The launcher writes the name there
// and leaves the rest of the file.
func TestBoiiiName(t *testing.T) {
	for name, want := range map[string]string{"Ann": "Ann", "": "Player", "Ann \"Zed\" %~": "Ann Zed",
		"A name of twenty chars": "A name of twenty"} {
		if got := boiiiName(name); got != want {
			t.Errorf("boiiiName(%q) = %q, not %q", name, got, want)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "boiii_players", "properties.json")
	members := func() map[string]string {
		var got map[string]string
		if err := json.Unmarshal([]byte(read(t, file)), &got); err != nil {
			t.Fatalf("BOIII's settings: %v: %s", err, read(t, file))
		}
		return got
	}
	// a file as BOIII writes it, with a byte order mark
	write(t, file, "\xef\xbb\xbf"+`{"playerName":"Gimmeh","selectedVersion":"latest"}`)
	err := setBoiiiName(dir, "Ann")
	if got := members(); err != nil || got["playerName"] != "Ann" || got["selectedVersion"] != "latest" {
		t.Errorf("the name, in a file with more: %v, %v", got, err)
	}
	// no file yet, and one that is no JSON
	os.RemoveAll(filepath.Dir(file))
	err = setBoiiiName(dir, "Ann")
	if got := members(); err != nil || len(got) != 1 || got["playerName"] != "Ann" {
		t.Errorf("the name, in a new file: %v, %v", got, err)
	}
	write(t, file, "not JSON")
	if err := setBoiiiName(dir, "Ann"); err != nil || members()["playerName"] != "Ann" {
		t.Errorf("the name, over a file that is no JSON: %v, %v", members(), err)
	}
}

// In a Black Ops III room, the launcher gets BOIII's program for a PC without it, and starts it in the folder of the
// player's own copy, at its menu, under the name that the room's game knows the player by. It types the line that
// joins the room's game into the game's console once the room's game is up, and for the room's next game. This
// launcher ends with its run, so it closes the game when the room plays another title: one that stays leaves it open
// (TestRoomOfTheOtherTitleKeepsTheGameOpen).
func TestBoiiiRoom(t *testing.T) {
	l := newFakeServer(t)
	gameOf := func(id string, up bool) *launcherapi.Game {
		return &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann 2", Up: up}
	}
	l.play = t7Play(l, "zone/zm_factory.ff", gameOf("first", false))
	tl := newT7Launcher(t, "")
	own := t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III"), "zm_factory")
	settings := filepath.Join(own, "boiii_players", "properties.json")
	write(t, settings, `{"playerName":"Gimmeh"}`)
	tl.copies = func(of *title) []string {
		if of != titleT7 {
			return nil
		}
		return []string{own}
	}
	set := func(change func(play *launcherapi.Play)) {
		l.mu.Lock()
		change(&l.play)
		l.mu.Unlock()
	}
	done := make(chan int)
	go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
	eventually(t, "the launcher started the player's game", func() bool { return tl.game(0) != nil })
	exe := filepath.Join(tl.boiii, boiii.Exe)
	tl.mu.Lock()
	first := tl.started[0]
	tl.mu.Unlock()
	if first.exe != exe || first.dir != own || !strings.HasPrefix(first.line, `"`+exe+`" -launch +set net_port `) {
		t.Fatalf("the game's start: %+v", first)
	}
	if port, err := strconv.Atoi(strings.TrimPrefix(first.line, `"`+exe+`" -launch +set net_port `)); err != nil || port < 1024 {
		t.Errorf("the game's port, in %q: %v", first.line, err)
	}
	// BOIII's program is from its updater, and nothing else of BOIII's: the game gets its data itself
	if read(t, exe) != boiiiFiles[boiii.Exe] || isFile(filepath.Join(tl.boiii, "data", "launcher", "main.html")) {
		t.Errorf("the launcher's copy of BOIII: %q", read(t, exe))
	}
	if !strings.Contains(read(t, settings), `"playerName":"Ann 2"`) {
		t.Errorf("BOIII's settings, as the game starts: %s", read(t, settings))
	}
	// the launcher said which title it is at, and which of its maps the folder has
	l.mu.Lock()
	ofs, has := slices.Clone(l.ofs), slices.Clone(l.has)
	l.mu.Unlock()
	if !slices.Contains(ofs, launcherapi.T7) || !slices.Contains(has, "zone/zm_factory.ff") {
		t.Errorf("what the launcher said: at %q, has %q", ofs, has)
	}
	time.Sleep(100 * time.Millisecond)
	if typed := tl.game(0).typed(); len(typed) != 0 {
		t.Fatalf("the game was told %q before the room's game was up", typed)
	}
	join := `password pw;connect 127.0.0.1:` + strconv.Itoa(l.port())
	set(func(play *launcherapi.Play) { play.Game = gameOf("first", true) })
	eventually(t, "the player's game joined the room's game", func() bool {
		return slices.Equal(tl.game(0).typed(), []string{join})
	})
	tl.at.Store("zm_factory") // the game is on its way into the map
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	time.Sleep(100 * time.Millisecond)
	set(func(play *launcherapi.Play) { play.InGame, play.Game = false, nil }) // game over: the room is open
	tl.at.Store(boiiiMenu)
	set(func(play *launcherapi.Play) { play.Game = gameOf("second", true) })
	eventually(t, "the player's game joined the room's next game", func() bool {
		return slices.Equal(tl.game(0).typed(), []string{join, join})
	})
	if tl.game(1) != nil {
		t.Fatalf("the launcher started the game again for the room's next game\n%s", tl.out)
	}
	// the room goes to a map of Black Ops II: this game is closed, with its console's quit, by a launcher that ends
	// here
	set(func(play *launcherapi.Play) { play.Of, play.Needs, play.Game = launcherapi.T6, "", nil })
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the launcher stayed with a game of another title than its room's\n%s", tl.out)
	}
	if g := tl.game(0); !g.stopped.Load() || !slices.Equal(g.typed(), []string{join, join, "quit"}) {
		t.Errorf("the game, as the room plays another title: stopped %v, told %q", g.stopped.Load(), g.typed())
	}
	// the name stays in BOIII's settings: the launcher puts nothing back
	if !strings.Contains(read(t, settings), `"playerName":"Ann 2"`) {
		t.Errorf("BOIII's settings, once the game is closed: %s", read(t, settings))
	}
}

// A line typed before a game on BOIII runs its menu is lost, so the launcher types it only once the game has answered
// for a moment. A game that is still at its menu a while after the line has not joined: the line is typed again, a
// few times in all.
func TestBoiiiTypesWhenTheGameTakesIt(t *testing.T) {
	tl := newT7Launcher(t, "")
	tl.at.Store("") // the game is starting, and answers nothing
	tl.retype = 60 * time.Millisecond
	g, err := tl.startBoiii(t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III")), "Ann")
	if err != nil {
		t.Fatal(err)
	}
	g.join("the line")
	time.Sleep(150 * time.Millisecond)
	if typed := tl.game(0).typed(); len(typed) != 0 {
		t.Fatalf("a game that answers nothing was told %q", typed)
	}
	tl.at.Store(boiiiMenu)
	eventually(t, "the line was typed, and again for a game that stayed at its menu", func() bool {
		return len(tl.game(0).typed()) == boiiiTries
	})
	time.Sleep(200 * time.Millisecond)
	if typed := tl.game(0).typed(); len(typed) != boiiiTries {
		t.Errorf("the line was typed %d times", len(typed))
	}
	// another line, for a game that goes into a map: typed once
	g.join("another line")
	eventually(t, "the next line was typed", func() bool { return len(tl.game(0).typed()) == boiiiTries+1 })
	tl.at.Store("zm_tomb")
	time.Sleep(200 * time.Millisecond)
	tl.at.Store(boiiiMenu)
	time.Sleep(200 * time.Millisecond)
	if typed := tl.game(0).typed(); len(typed) != boiiiTries+1 || typed[boiiiTries] != "another line" {
		t.Errorf("after a line that took the game into a map: %q", typed)
	}
	g.stop()
	if !tl.game(0).stopped.Load() {
		t.Error("the game was not closed")
	}
}

// A PC without Steam starts no Black Ops III, and no Mac does: the launcher says so, and gets nothing.
func TestBoiiiNeedsSteam(t *testing.T) {
	l := newFakeServer(t)
	l.play = t7Play(l, "zone/zm_factory.ff", &launcherapi.Game{ID: "first", Port: l.port(), Password: "pw", Name: "Ann"})
	tl := newT7Launcher(t, "")
	tl.steam = func() bool { return false }
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 ||
		!strings.Contains(tl.out.String(), "needs Steam") || len(tl.updater.Asked()) != 0 {
		t.Errorf("on a PC without Steam: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	tl = newT7Launcher(t, "")
	tl.mac = "1"
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 ||
		!strings.Contains(tl.out.String(), "doesn't run on a Mac") {
		t.Errorf("on a Mac: %d, started %+v\n%s", code, tl.started, tl.out)
	}
}

// The server sends Black Ops III in packs. A PC without the game downloads what every map needs and the map of the
// player's room, with the map's zone last, and later the next room's map alone. The maps that the folder has stay
// the same as the server's, and the others are not downloaded.
func TestBoiiiDownloadsByMap(t *testing.T) {
	l := newFakeServer(t)
	l.t7 = map[string]string{
		"BlackOps3.exe": "the game", "zone/base.xpak": "pictures", "zone/zm_common.ff": "what every map loads",
		"zone/zm_factory.ff": "The Giant", "zone/zm_factory.xpak": "The Giant's pictures",
		"zone/snd/all/zm_factory.all.sabs": "The Giant's sounds",
		"zone/zm_tomb.ff":                  "Origins", "zone/zm_tomb.xpak": "Origins' pictures",
	}
	l.packs = map[string]string{
		"zone/zm_factory.ff": "zm_factory", "zone/zm_factory.xpak": "zm_factory",
		"zone/snd/all/zm_factory.all.sabs": "zm_factory", "zone/zm_tomb.ff": "zm_tomb", "zone/zm_tomb.xpak": "zm_tomb",
	}
	l.play = t7Play(l, "zone/zm_factory.ff", &launcherapi.Game{ID: "first", Port: l.port(), Password: "pw", Name: "Ann", Up: true})
	l.play.Titles[launcherapi.T7] = launcherapi.Title{Zones: t7Zones, Downloads: true}
	run := func(answers string) *t7Launcher {
		t.Helper()
		tl := newT7Launcher(t, answers)
		tl.start = func(exe, line, dir string) (game, error) {
			tl.started = append(tl.started, started{exe, line, dir})
			return &fakeGame{}, nil // closed at once: the run ends with it
		}
		return tl
	}
	// the first run says which maps the folder has: the room's, and no other
	tl := newT7Launcher(t, "\n")
	if code := tl.runUntilSaid(t, l, l.link(), "zone/zm_factory.ff"); code != 0 || len(tl.started) != 1 {
		t.Fatalf("a PC without the game: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	dir := filepath.Join(tl.home, "t7")
	if tl.started[0].dir != dir {
		t.Errorf("the game started in %s, not in the launcher's folder", tl.started[0].dir)
	}
	var got []string
	for _, call := range l.calls() {
		if path, ok := strings.CutPrefix(call, "GET /launcher/t7/files/"); ok {
			got = append(got, path)
		}
	}
	want := []string{"BlackOps3.exe", "zone/base.xpak", "zone/snd/all/zm_factory.all.sabs", "zone/zm_common.ff",
		"zone/zm_factory.xpak", "zone/zm_factory.ff"}
	if !slices.Equal(got, want) {
		t.Errorf("downloaded, in this order: %q\n%s", got, tl.out)
	}
	if isFile(filepath.Join(dir, "zone", "zm_tomb.ff")) || isFile(filepath.Join(dir, "zone", "zm_tomb.xpak")) {
		t.Error("the launcher downloaded a map that the player does not play")
	}
	if !strings.Contains(tl.out.String(), "Downloading 6 of the game's 6 files") {
		t.Errorf("what the launcher said of the download:\n%s", tl.out)
	}
	// the next room's map, and a file of the first map that the server has another of since: both, and nothing else
	l.mu.Lock()
	l.play.Needs = "zone/zm_tomb.ff"
	l.t7["zone/zm_factory.xpak"] = "The Giant's pictures, patched"
	l.mu.Unlock()
	next := run("")
	next.home, next.boiii = tl.home, tl.boiii
	if code := next.run(context.Background(), []string{l.link()}); code != 0 || len(next.started) != 1 {
		t.Fatalf("the next room's map: %d, started %+v\n%s", code, next.started, next.out)
	}
	got = nil
	for _, call := range l.calls() {
		if path, ok := strings.CutPrefix(call, "GET /launcher/t7/files/"); ok {
			got = append(got, path)
		}
	}
	if !slices.Equal(got, []string{"zone/zm_factory.xpak", "zone/zm_tomb.xpak", "zone/zm_tomb.ff"}) {
		t.Errorf("downloaded for the next map: %q\n%s", got, next.out)
	}
	if read(t, filepath.Join(dir, "zone", "zm_factory.xpak")) != "The Giant's pictures, patched" ||
		read(t, filepath.Join(dir, "zone", "zm_tomb.ff")) != "Origins" {
		t.Error("the folder is not as the server's")
	}
	// a program that is not the game's own is refused, whatever its pack
	l.mu.Lock()
	l.t7["ext.dll"] = "someone's library"
	l.mu.Unlock()
	last := run("")
	last.home, last.boiii = tl.home, tl.boiii
	if code := last.run(context.Background(), []string{l.link()}); code == 0 || len(last.started) != 0 ||
		!strings.Contains(last.out.String(), "isn't part of Black Ops III") {
		t.Errorf("a server with a program of its own: %d\n%s", code, last.out)
	}
}

// The lobby's Play local starts Black Ops III at its menu, with nothing typed into it. With the map that the player
// picked in the page, a PC whose game files are the launcher's gets that map's first.
func TestBoiiiAlone(t *testing.T) {
	l := newFakeServer(t)
	l.t7 = map[string]string{"BlackOps3.exe": "the game", "zone/zm_common.ff": "what every map loads",
		"zone/zm_factory.ff": "The Giant", "zone/zm_tomb.ff": "Origins", "zone/zm_tomb.xpak": "Origins' pictures"}
	l.packs = map[string]string{"zone/zm_factory.ff": "zm_factory", "zone/zm_tomb.ff": "zm_tomb", "zone/zm_tomb.xpak": "zm_tomb"}
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{
		launcherapi.T7: {Zones: t7Zones, Downloads: true}}}
	tl := newT7Launcher(t, "\n")
	link := l.link() + "&solo=1&name=Ann&of=t7&map=zm_tomb"
	// the launcher says that the folder has the map that the player picked, and no other
	if code := tl.runUntilSaid(t, l, link, "zone/zm_tomb.ff"); code != 0 || len(tl.started) != 1 ||
		tl.started[0].dir != filepath.Join(tl.home, "t7") {
		t.Fatalf("Black Ops III alone: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	dir := filepath.Join(tl.home, "t7")
	if read(t, filepath.Join(dir, "zone", "zm_tomb.xpak")) != "Origins' pictures" || isFile(filepath.Join(dir, "zone", "zm_factory.ff")) {
		t.Errorf("the folder has not the map that the player picked alone\n%s", tl.out)
	}
	if typed := tl.game(0).typed(); len(typed) != 0 {
		t.Errorf("a game alone was told %q", typed)
	}
	if !strings.Contains(tl.out.String(), "Black Ops III is starting at its menu, for you to play as Ann.") {
		t.Errorf("what the launcher said:\n%s", tl.out)
	}
	l.mu.Lock()
	ofs := slices.Clone(l.ofs)
	l.mu.Unlock()
	if !slices.Contains(ofs, launcherapi.T7) {
		t.Errorf("the launcher said that it is at %q", ofs)
	}
	if tl.title() != titleT7 {
		t.Errorf("the run was at %s", tl.title().name)
	}
	// while the game runs, the next map that the player asks for is downloaded for it, and nothing starts again
	first, _ := parseLink(link)
	l.calls()
	tl.getPicked(context.Background(), newServer(first, tl.client), dir, "zm_factory")
	if isFile(filepath.Join(dir, "zone", "zm_factory.ff")) {
		t.Error("a launcher that has no answer of the server downloaded a map")
	}
	srv, stop := tl.open(context.Background(), first)
	defer stop()
	srv.start()
	if _, err := srv.current(context.Background()); err != nil {
		t.Fatal(err)
	}
	tl.getPicked(context.Background(), srv, dir, "zm_factory")
	if read(t, filepath.Join(dir, "zone", "zm_factory.ff")) != "The Giant" || len(tl.started) != 1 {
		t.Errorf("the map that the player picked while the game ran: started %+v\n%s", tl.started, tl.out)
	}
	var got []string
	for _, call := range l.calls() {
		if path, ok := strings.CutPrefix(call, "GET /launcher/t7/files/"); ok {
			got = append(got, path)
		}
	}
	if !slices.Equal(got, []string{"zone/zm_factory.ff"}) {
		t.Errorf("downloaded for the map: %q", got)
	}
}

// The lobby's Play local has the PC made ready for a title, for a player who is in no room. For Black Ops III, a PC
// without the game downloads what every map needs, which has the map that the game comes with, and starts nothing. A
// map that the player then asks for by its name is downloaded alone, and still nothing starts. The launcher says that
// the folder is none of the player's own.
func TestBoiiiReadyWithNoRoom(t *testing.T) {
	l := newFakeServer(t)
	l.t7 = map[string]string{"BlackOps3.exe": "the game", "zone/zm_common.ff": "what every map loads",
		"zone/zm_zod.ff": "Shadows of Evil", "zone/zm_factory.ff": "The Giant", "zone/zm_tomb.ff": "Origins",
		"zone/zm_tomb.xpak": "Origins' pictures"}
	// Shadows of Evil is of no pack of its own (server/gamefiles.go: t7OwnMaps)
	l.packs = map[string]string{"zone/zm_factory.ff": "zm_factory", "zone/zm_tomb.ff": "zm_tomb", "zone/zm_tomb.xpak": "zm_tomb"}
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{launcherapi.T7: {
		Zones: []string{"zone/zm_factory.ff", "zone/zm_tomb.ff", "zone/zm_zod.ff"}, Downloads: true}}}
	tl := newT7Launcher(t, "\n")
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link() + "&of=t7"}) }()
	// said is what the launcher last told the server: the title it is at, that the PC is ready, and of the folder
	said := func() (of string, ready bool, has string, own bool) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if n := len(l.ofs); n > 0 {
			return l.ofs[n-1], l.ready[n-1], l.has[n-1], l.owns[n-1]
		}
		return "", false, "", false
	}
	eventually(t, "the PC is ready for Black Ops III, with the map that the game comes with", func() bool {
		of, ready, has, _ := said()
		return of == launcherapi.T7 && ready && has == "zone/zm_zod.ff"
	})
	dir := filepath.Join(tl.home, "t7")
	if _, _, _, own := said(); own || isFile(filepath.Join(dir, "zone", "zm_factory.ff")) || isFile(filepath.Join(dir, "zone", "zm_tomb.ff")) {
		t.Errorf("the launcher's own folder: said to be the player's own %v, or it has a map that no one asked for\n%s", own, tl.out)
	}
	// the map that the player asks for in the page: the press's note names it
	ask, err := parseLink(l.link() + "&of=t7&map=zm_tomb")
	if err != nil {
		t.Fatal(err)
	}
	notePress(tl.home, ask)
	l.calls()
	tl.press()
	eventually(t, "the map that the player asked for was downloaded", func() bool {
		_, ready, has, _ := said()
		return ready && has == "zone/zm_tomb.ff,zone/zm_zod.ff"
	})
	var got []string
	for _, call := range l.calls() {
		if path, ok := strings.CutPrefix(call, "GET /launcher/t7/files/"); ok {
			got = append(got, path)
		}
	}
	if !slices.Equal(got, []string{"zone/zm_tomb.xpak", "zone/zm_tomb.ff"}) {
		t.Errorf("downloaded for the map: %q\n%s", got, tl.out)
	}
	if tl.game(0) != nil {
		t.Errorf("a launcher that was asked to get the PC ready started a game\n%s", tl.out)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// A PC with the player's own copy of Black Ops III: the launcher says that the folder is theirs, and downloads no map
// into it, whatever the page asked for.
func TestBoiiiOwnCopyWithNoRoom(t *testing.T) {
	l := newFakeServer(t)
	l.t7 = map[string]string{"BlackOps3.exe": "the game", "zone/zm_common.ff": "what every map loads",
		"zone/zm_factory.ff": "The Giant", "zone/zm_tomb.ff": "Origins"}
	l.packs = map[string]string{"zone/zm_factory.ff": "zm_factory", "zone/zm_tomb.ff": "zm_tomb"}
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{
		launcherapi.T7: {Zones: t7Zones, Downloads: true}}}
	tl := newT7Launcher(t, "")
	own := t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III"), "zm_factory")
	tl.copies = func(*title) []string { return []string{own} }
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link() + "&of=t7&map=zm_tomb"}) }()
	eventually(t, "the PC is ready for Black Ops III, from the player's own copy", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := len(l.ofs)
		return n > 0 && l.ofs[n-1] == launcherapi.T7 && l.ready[n-1] && l.has[n-1] == "zone/zm_factory.ff" && l.owns[n-1]
	})
	if tl.game(0) != nil || isFile(filepath.Join(own, "zone", "zm_tomb.ff")) || isFile(filepath.Join(tl.home, "t7", "BlackOps3.exe")) {
		t.Errorf("on a PC with the player's own copy, the launcher started a game, or downloaded game files\n%s", tl.out)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// Play local asks for the launcher's other title, and not to launch it, while the player's game runs: the launcher
// goes over to that title and gets the PC ready for it. It starts nothing, and leaves the game that runs open.
func TestPlayLocalAsksForTheOtherGame(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{launcherapi.T7: {Zones: t7Zones}}}
	tl := newT7Launcher(t, "")
	t6dir := ownCopy(t, tl.testLauncher)
	t7dir := t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III"), "zm_factory")
	tl.copies = func(of *title) []string {
		if of == titleT7 {
			return []string{t7dir}
		}
		return []string{t6dir}
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link() + "&solo=1&name=Ann&of=t7"}) }()
	eventually(t, "Black Ops III started", func() bool { return tl.game(0) != nil })
	// the press that asks for Black Ops II, which a link names by no word
	other, err := parseLink(l.link())
	if err != nil {
		t.Fatal(err)
	}
	notePress(tl.home, other)
	tl.press()
	eventually(t, "the PC is ready for Black Ops II, with no game of it", func() bool {
		l.mu.Lock()
		n := len(l.ofs)
		said := n > 0 && l.ofs[n-1] == "" && l.ready[n-1] && !l.games[n-1]
		l.mu.Unlock()
		return said && tl.title() == titleT6
	})
	if g := tl.game(0); tl.game(1) != nil || !g.running() || g.stopped.Load() {
		t.Errorf("as the player asked for the other game: a second game started %v; Black Ops III runs %v, stopped %v\n%s",
			tl.game(1) != nil, g.running(), g.stopped.Load(), tl.out)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// What the launcher says of the PC is of one title's game. A launcher that had the PC ready for Black Ops II, and
// then fails to get it ready for Black Ops III as its player's room goes over to that game, does not tell the server
// that the PC is ready for it.
func TestReadyIsOfOneTitle(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Plutonium: 1}
	tl := newT7Launcher(t, "")
	ownCopy(t, tl.testLauncher)
	tl.steam = func() bool { return false } // no Black Ops III starts on this PC
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link()}) }()
	said := func() (ofs []string, ready []bool, reports []launcherapi.Report) {
		l.mu.Lock()
		defer l.mu.Unlock()
		return slices.Clone(l.ofs), slices.Clone(l.ready), slices.Clone(l.reports)
	}
	eventually(t, "the launcher got the PC ready for Black Ops II", func() bool {
		_, ready, _ := said()
		return len(ready) > 0 && ready[len(ready)-1]
	})
	l.mu.Lock()
	l.play = t7Play(l, "zone/zm_factory.ff", nil)
	l.mu.Unlock()
	eventually(t, "the launcher failed at Black Ops III", func() bool {
		_, _, reports := said()
		return slices.ContainsFunc(reports, func(r launcherapi.Report) bool { return strings.Contains(r.Failed, "Steam") })
	})
	time.Sleep(200 * time.Millisecond) // the calls that the launcher makes as it waits, after the try that failed
	ofs, ready, _ := said()
	if !slices.Contains(ofs, launcherapi.T7) {
		t.Fatalf("the launcher never said that it is at Black Ops III: %q", ofs)
	}
	for i, of := range ofs {
		if of == launcherapi.T7 && ready[i] {
			t.Fatalf("the launcher said that the PC is ready for Black Ops III, which it never was: call %d of %q, %v", i, ofs, ready)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// A press of Play local for the launcher's other game, while the player's game runs, starts the other one and leaves
// the first open. The launcher stays with a game that is open again when the player asks for it, and starts no
// second one of it.
func TestPlayLocalOfTheOtherGame(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{launcherapi.T7: {Zones: t7Zones}}}
	tl := newT7Launcher(t, "")
	t6dir := ownCopy(t, tl.testLauncher)
	t7dir := t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III"), "zm_factory")
	tl.copies = func(of *title) []string {
		if of == titleT7 {
			return []string{t7dir}
		}
		return []string{t6dir}
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link() + "&solo=1&name=Ann&of=t7"}) }()
	starts := func() []started {
		tl.mu.Lock()
		defer tl.mu.Unlock()
		return slices.Clone(tl.started)
	}
	eventually(t, "Black Ops III started", func() bool { return len(starts()) == 1 })
	if got := starts()[0]; filepath.Base(got.exe) != boiii.Exe || got.dir != t7dir {
		t.Fatalf("the first game started as %+v", got)
	}
	// a press with no note of what it asks for closes nothing
	tl.solo <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	if len(starts()) != 1 || !tl.game(0).running() {
		t.Fatalf("a press that named no game: %d games started, the first runs: %v", len(starts()), tl.game(0).running())
	}
	// the press of Play local for Black Ops II, which leaves its note first (main)
	first, _ := parseLink(l.link() + "&solo=1&name=Ann&of=t7")
	other, err := parseLink(l.link() + "&solo=1&name=Ann")
	if err != nil {
		t.Fatal(err)
	}
	notePress(tl.home, other)
	if first.id() != other.id() {
		t.Fatalf("the two links are of two launchers: %q, %q", first.id(), other.id())
	}
	tl.solo <- struct{}{}
	eventually(t, "Black Ops II started beside it", func() bool { return len(starts()) == 2 })
	if got := starts()[1]; !strings.HasSuffix(got.line, ` -lan +name "Ann"`) {
		t.Errorf("the second game started with %q", got.line)
	}
	// at reports whether the launcher last told the server that it is at the title of, with its game running. It
	// names Black Ops II by no word.
	at := func(of string) bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := len(l.ofs)
		return n > 0 && l.ofs[n-1] == of && l.games[n-1]
	}
	eventually(t, "the launcher is at Black Ops II, whose game runs", func() bool { return tl.title() == titleT6 && at("") })
	if g := tl.game(0); !g.running() || g.stopped.Load() || len(g.typed()) != 0 {
		t.Fatalf("Black Ops III, as the player asked for the other game: runs %v, stopped %v, told %q", g.running(),
			g.stopped.Load(), g.typed())
	}
	// Play local for Black Ops III again: the launcher stays with the game that is open, and starts none
	notePress(tl.home, first)
	tl.solo <- struct{}{}
	eventually(t, "the launcher is at Black Ops III again, whose game runs", func() bool {
		return tl.title() == titleT7 && at(launcherapi.T7)
	})
	time.Sleep(50 * time.Millisecond)
	if len(starts()) != 2 || !tl.game(0).running() || !tl.game(1).running() || tl.game(1).stopped.Load() {
		t.Fatalf("with both games open: %d started, Black Ops III runs %v, Black Ops II runs %v", len(starts()),
			tl.game(0).running(), tl.game(1).running())
	}
	if !strings.Contains(tl.out.String(), "Black Ops III is open already") {
		t.Errorf("what the launcher said:\n%s", tl.out)
	}
	// the player closes Black Ops III, and asks for Black Ops II, which is open still
	tl.game(0).open.Store(false)
	eventually(t, "the server heard that Black Ops III was closed", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return !l.games[len(l.games)-1]
	})
	notePress(tl.home, other)
	tl.solo <- struct{}{}
	eventually(t, "the launcher is at Black Ops II again, whose game runs", func() bool { return tl.title() == titleT6 && at("") })
	// and Black Ops III once more, which is closed: it starts
	notePress(tl.home, first)
	tl.solo <- struct{}{}
	eventually(t, "Black Ops III started again", func() bool { return len(starts()) == 3 })
	if got := starts()[2]; filepath.Base(got.exe) != boiii.Exe {
		t.Errorf("the third game started as %+v", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// A map that the player asks for in Play local while their game runs is downloaded beside the game, which runs on:
// the launcher starts nothing again, and ends when it is quit, with the download behind it.
func TestBoiiiGetsAPickedMapBesideTheGame(t *testing.T) {
	l := newFakeServer(t)
	l.t7 = map[string]string{"BlackOps3.exe": "the game", "zone/zm_common.ff": "what every map loads",
		"zone/zm_factory.ff": "The Giant", "zone/zm_tomb.ff": "Origins", "zone/zm_tomb.xpak": "Origins' pictures"}
	l.packs = map[string]string{"zone/zm_factory.ff": "zm_factory", "zone/zm_tomb.ff": "zm_tomb", "zone/zm_tomb.xpak": "zm_tomb"}
	l.play = launcherapi.Play{Player: "Ann", Titles: map[string]launcherapi.Title{
		launcherapi.T7: {Zones: t7Zones, Downloads: true}}}
	tl := newT7Launcher(t, "\n")
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link() + "&solo=1&name=Ann&of=t7&map=zm_tomb"}) }()
	eventually(t, "Black Ops III started", func() bool { return tl.game(0) != nil })
	dir := filepath.Join(tl.home, "t7")
	if isFile(filepath.Join(dir, "zone", "zm_factory.ff")) {
		t.Fatal("the folder has a map that the player did not pick")
	}
	// the page asks for the map with a link that launches nothing, whose note names it
	other, err := parseLink(l.link() + "&of=t7&map=zm_factory")
	if err != nil {
		t.Fatal(err)
	}
	notePress(tl.home, other)
	tl.press()
	eventually(t, "the map that the player asked for was downloaded", func() bool {
		return isFile(filepath.Join(dir, "zone", "zm_factory.ff"))
	})
	if !tl.game(0).running() || tl.game(1) != nil {
		t.Errorf("the game that ran: runs %v, and another started: %v", tl.game(0).running(), tl.game(1) != nil)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
	if read(t, filepath.Join(dir, "zone", "zm_factory.ff")) != "The Giant" {
		t.Error("the map that was downloaded is not the server's")
	}
}

// A room that goes over to the launcher's other title costs its players no game that they have open. The launcher
// leaves the game of the first title open, sees to the other title's, and stays with the first again when the room
// comes back to it: the game joins the room's next game from where it is.
func TestRoomOfTheOtherTitleKeepsTheGameOpen(t *testing.T) {
	l := newFakeServer(t)
	gameOf := func(id string) *launcherapi.Game {
		return &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann", Up: true}
	}
	l.play = t7Play(l, "zone/zm_factory.ff", gameOf("first"))
	tl := newT7Launcher(t, "")
	t6dir := ownCopy(t, tl.testLauncher)
	t7dir := t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III"), "zm_factory")
	tl.copies = func(of *title) []string {
		if of == titleT7 {
			return []string{t7dir}
		}
		return []string{t6dir}
	}
	tl.stays = true
	set := func(change func(play *launcherapi.Play)) {
		l.mu.Lock()
		change(&l.play)
		l.mu.Unlock()
	}
	starts := func() []started {
		tl.mu.Lock()
		defer tl.mu.Unlock()
		return slices.Clone(tl.started)
	}
	// at reports whether the launcher last told the server that it is at the title of, and whether its game runs.
	at := func(of string, runs bool) bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := len(l.ofs)
		return n > 0 && l.ofs[n-1] == of && l.games[n-1] == runs
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link()}) }()
	join := `password pw;connect 127.0.0.1:` + strconv.Itoa(l.port())
	eventually(t, "Black Ops III started, and joined the room's game", func() bool {
		return tl.game(0) != nil && slices.Equal(tl.game(0).typed(), []string{join})
	})
	tl.at.Store("zm_factory")
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	time.Sleep(50 * time.Millisecond)
	tl.at.Store(boiiiMenu)
	// game over, and the room's host picks a map of Black Ops II
	set(func(play *launcherapi.Play) {
		play.InGame, play.Of, play.Needs, play.Plutonium = false, launcherapi.T6, "", 1
		play.Titles[launcherapi.T6] = launcherapi.Title{Plutonium: 1}
	})
	eventually(t, "the launcher is at Black Ops II, with the PC ready and no game of it", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := len(l.ofs)
		return n > 0 && l.ofs[n-1] == "" && l.ready[n-1] && !l.games[n-1]
	})
	if g := tl.game(0); !g.running() || g.stopped.Load() || !slices.Equal(g.typed(), []string{join}) {
		t.Fatalf("Black Ops III, as the room plays the other title: runs %v, stopped %v, told %q", g.running(),
			g.stopped.Load(), g.typed())
	}
	// the room's game of Black Ops II, which the launcher starts the player's game for
	set(func(play *launcherapi.Play) { play.Game = gameOf("second") })
	eventually(t, "Black Ops II started for the room's game", func() bool { return len(starts()) == 2 && at("", true) })
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	time.Sleep(50 * time.Millisecond)
	// game over, and the room is back at Black Ops III: the launcher stays with the game that is open
	set(func(play *launcherapi.Play) {
		play.InGame, play.Of, play.Needs = false, launcherapi.T7, "zone/zm_factory.ff"
	})
	eventually(t, "the launcher is at Black Ops III again, whose game runs", func() bool { return at(launcherapi.T7, true) })
	if got := starts(); len(got) != 2 || !tl.game(1).running() || tl.game(1).stopped.Load() {
		t.Fatalf("back at Black Ops III: %d games started, Black Ops II runs %v", len(got), tl.game(1).running())
	}
	l.mu.Lock()
	n := len(l.ofs)
	ready, has := l.ready[n-1], l.has[n-1]
	l.mu.Unlock()
	if !ready || has != "zone/zm_factory.ff" {
		t.Errorf("what the launcher said of the PC, back at Black Ops III: ready %v, has %q", ready, has)
	}
	// the room's next game, which the game that is open joins
	set(func(play *launcherapi.Play) { play.Game = gameOf("third") })
	eventually(t, "Black Ops III joined the room's next game", func() bool {
		return slices.Equal(tl.game(0).typed(), []string{join, join})
	})
	if len(starts()) != 2 {
		t.Errorf("%d games started\n%s", len(starts()), tl.out)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// Uninstalled, the launcher takes what it has for Black Ops III with it too: its copy of BOIII's program, with what
// BOIII left beside it, and the notes of the player's presses. It asks once about the game files that it downloaded,
// of both titles together, with their size in all, and deletes them when the player says so.
func TestUninstallWithBlackOpsIII(t *testing.T) {
	gone := func(file string) bool {
		_, err := os.Stat(file)
		return os.IsNotExist(err)
	}
	for _, c := range []struct {
		answers string
		wipe    bool
	}{{"y\n", true}, {"n\n", false}} {
		tl := newT7Launcher(t, "")
		tl.exe = filepath.Join(tl.home, exeName)
		write(t, tl.exe, "the launcher")
		// the game files that it downloaded of each title, with the record of them, which has their sizes
		var downloaded, folders []string
		for of, size := range map[*title]int64{titleT6: 9e9, titleT7: 51e9} {
			dir := filepath.Join(tl.home, of.id)
			write(t, filepath.Join(dir, "zone", "a.ff"), "contents")
			state, _ := json.Marshal(syncState{Files: map[string]syncHas{"zone/a.ff": {Size: size}}})
			write(t, filepath.Join(dir, launcherapi.StateFile), string(state))
			downloaded, folders = append(downloaded, filepath.Join(dir, "zone", "a.ff")), append(folders, dir)
		}
		// BOIII's program, the record of it, and the program that BOIII set aside as it brought itself up to date
		for _, name := range []string{boiii.Exe, boiii.Exe + ".old", "zombies-launcher-files.json"} {
			write(t, filepath.Join(tl.boiii, name), "BOIII's")
		}
		// the notes of the player's presses, of both kinds
		press, err := parseLink("zombies-launcher://host/play?key=0123456789abcdef&of=t7")
		if err != nil {
			t.Fatal(err)
		}
		notePress(tl.home, press)
		press.solo = true
		notePress(tl.home, press)
		tl.again(c.answers)
		if code := tl.run(context.Background(), []string{"uninstall"}); code != 0 {
			t.Fatalf("uninstalled, answered %q: %d\n%s", c.answers, code, tl.out)
		}
		out := tl.out.String()
		if strings.Count(out, "Also delete") != 1 ||
			!strings.Contains(out, "Also delete the downloaded Black Ops II and Black Ops III files (60.0 GB)?") ||
			!strings.Contains(out, folders[0]) || !strings.Contains(out, folders[1]) {
			t.Errorf("uninstalled, answered %q: what it asked\n%s", c.answers, out)
		}
		if gone(downloaded[0]) != c.wipe || gone(downloaded[1]) != c.wipe {
			t.Errorf("uninstalled, answered %q: the downloaded files gone: %v, %v", c.answers, gone(downloaded[0]),
				gone(downloaded[1]))
		}
		if !gone(tl.boiii) {
			t.Errorf("uninstalled, answered %q: BOIII's folder is still there", c.answers)
		}
		// what is left of the launcher's folder: the game files that the player kept, and nothing else
		var left []string
		if kept, err := os.ReadDir(tl.home); err == nil {
			for _, file := range kept {
				left = append(left, file.Name())
			}
		}
		if want := map[bool][]string{true: nil, false: {titleT6.id, titleT7.id}}[c.wipe]; !slices.Equal(left, want) {
			t.Errorf("uninstalled, answered %q: the launcher's folder has %q, not %q", c.answers, left, want)
		}
	}
}

// A game on BOIII leaves the game it is in with its console's disconnect, typed at once, and a line that was yet to be
// typed is typed no more.
func TestBoiiiLeaves(t *testing.T) {
	tl := newT7Launcher(t, "")
	tl.at.Store("") // the game answers nothing: a line for it waits
	g, err := tl.startBoiii(t7Copy(t, filepath.Join(t.TempDir(), "Black Ops III")), "Ann")
	if err != nil {
		t.Fatal(err)
	}
	g.join("password pw;connect host:1")
	if err := g.leave(); err != nil || !slices.Equal(tl.game(0).typed(), []string{leaveLine}) {
		t.Fatalf("the game, told to leave: %v, typed %q", err, tl.game(0).typed())
	}
	tl.at.Store(boiiiMenu) // the game is at its menu, where it would take the line that waited
	time.Sleep(200 * time.Millisecond)
	if typed := tl.game(0).typed(); !slices.Equal(typed, []string{leaveLine}) {
		t.Errorf("the game, after it left: typed %q", typed)
	}
	g.stop()
}
