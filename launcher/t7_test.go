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

// A link names the title to play alone, and the map of it that the player picked. A launcher that runs is told both
// of the player's next press in a note.
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
	if of, mapName := pressedSolo(home, first); of != "" || mapName != "" {
		t.Errorf("with no note: %q, %q", of, mapName)
	}
	notePress(home, l)
	if of, mapName := pressedSolo(home, first); of != "t7" || mapName != "zm_tomb" {
		t.Errorf("the note of a press: %q, %q", of, mapName)
	}
	os.WriteFile(pressNote(home, first), []byte("t7\n../x\n"), 0o644)
	if of, mapName := pressedSolo(home, first); of != "" || mapName != "" {
		t.Errorf("a note that is none: %q, %q", of, mapName)
	}
}

// A game on BOIII plays under the name in BOIII's settings in the game's folder. The launcher writes the name there
// and leaves the rest of the file, and puts back what the file had.
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
	restore, err := setBoiiiName(dir, "Ann")
	if got := members(); err != nil || got["playerName"] != "Ann" || got["selectedVersion"] != "latest" {
		t.Errorf("the name, in a file with more: %v, %v", got, err)
	}
	restore()
	if got := members(); got["playerName"] != "Gimmeh" || got["selectedVersion"] != "latest" {
		t.Errorf("the file, with the player's own name back: %v", got)
	}
	// no file yet, and one that is no JSON
	os.RemoveAll(filepath.Dir(file))
	restore, err = setBoiiiName(dir, "Ann")
	if got := members(); err != nil || got["playerName"] != "Ann" {
		t.Errorf("the name, in a new file: %v, %v", got, err)
	}
	restore()
	if got := members(); len(got) != 0 {
		t.Errorf("the new file, without the name: %v", got)
	}
	write(t, file, "not JSON")
	if _, err := setBoiiiName(dir, "Ann"); err != nil || members()["playerName"] != "Ann" {
		t.Errorf("the name, over a file that is no JSON: %v, %v", members(), err)
	}
}

// In a Black Ops III room, the launcher gets BOIII's program for a PC without it, and starts it in the folder of the
// player's own copy, at its menu, under the name that the room's game knows the player by. It types the line that
// joins the room's game into the game's console once the room's game is up, and for the room's next game. It closes
// the game when the room plays another title.
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
	// the room goes to a map of Black Ops II: this game is closed, with its console's quit, and the name put back
	set(func(play *launcherapi.Play) { play.Of, play.Needs, play.Game = launcherapi.T6, "", nil })
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the launcher stayed with a game of another title than its room's\n%s", tl.out)
	}
	if g := tl.game(0); !g.stopped.Load() || !slices.Equal(g.typed(), []string{join, join, "quit"}) {
		t.Errorf("the game, as the room plays another title: stopped %v, told %q", g.stopped.Load(), g.typed())
	}
	eventually(t, "the player's own name is back", func() bool {
		return strings.Contains(read(t, settings), `"playerName":"Gimmeh"`)
	})
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
	tl := run("\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 {
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
	l.mu.Lock()
	has := l.has[len(l.has)-1]
	l.mu.Unlock()
	if has != "zone/zm_factory.ff" {
		t.Errorf("the maps that the launcher said the folder has: %q", has)
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
	tl.start = func(exe, line, dir string) (game, error) {
		tl.started = append(tl.started, started{exe, line, dir})
		g := &fakeGame{}
		tl.mu.Lock()
		tl.games = append(tl.games, g)
		tl.mu.Unlock()
		return g, nil
	}
	link := l.link() + "&solo=1&name=Ann&of=t7&map=zm_tomb"
	if code := tl.run(context.Background(), []string{link}); code != 0 || len(tl.started) != 1 ||
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
	ofs, has := slices.Clone(l.ofs), l.has[len(l.has)-1]
	l.mu.Unlock()
	if !slices.Contains(ofs, launcherapi.T7) || has != "zone/zm_tomb.ff" {
		t.Errorf("what the launcher said: at %q, has %q", ofs, has)
	}
	if tl.title() != titleT7 {
		t.Errorf("the run was at %s", tl.title().name)
	}
	// while the game runs, the next map that the player picks is downloaded for it, and nothing starts again
	first, _ := parseLink(link)
	other, _ := parseLink(l.link() + "&solo=1&name=Ann&of=t7&map=zm_factory")
	notePress(tl.home, other)
	l.calls()
	tl.getPicked(context.Background(), newServer(first, tl.client), first, dir)
	if isFile(filepath.Join(dir, "zone", "zm_factory.ff")) {
		t.Error("a launcher that has no answer of the server downloaded a map")
	}
	srv, stop := tl.open(context.Background(), first)
	defer stop()
	srv.start()
	if _, err := srv.current(context.Background()); err != nil {
		t.Fatal(err)
	}
	tl.getPicked(context.Background(), srv, first, dir)
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
