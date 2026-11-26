package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
)

// ownCopy puts a copy of Black Ops II on the test launcher's PC, as Steam would have it, and returns its folder.
func ownCopy(t *testing.T, tl *testLauncher) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(dir, "binkw32.dll"), "bink")
	tl.copies = func(*title) []string { return []string{dir} }
	return dir
}

// The lobby's Play local starts the game at its menu, under the player's name as the server has it, and joins no
// room's game. Any complete copy of Plutonium does: the launcher contacts Plutonium's updater only for a PC that has
// none, and not when the server's copy is of another revision.
func TestAlone(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Plutonium: 1}
	tl := newTestLauncher(t, "")
	own := ownCopy(t, tl)
	link := l.link() + "&solo=1&name=Someone"
	bootstrapper := filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	run := func(what string, asks bool) {
		t.Helper()
		tl.again("")
		tl.updater.Asked()
		if code := tl.run(context.Background(), []string{link}); code != 0 || len(tl.started) != 1 ||
			tl.started[0].line != `"`+bootstrapper+`" t6zm "`+own+`" -lan +name "Ann"` {
			t.Fatalf("%s: %d, started %+v\n%s", what, code, tl.started, tl.out)
		}
		if asked := tl.updater.Asked(); (len(asked) > 0) != asks {
			t.Errorf("%s: the launcher asked Plutonium's updater for %q", what, asked)
		}
	}
	run("a PC without Plutonium", true)
	// Plutonium's own launcher, which has not run on this PC, finds the game's folder set
	config := filepath.Join(tl.plutonium, plutoniumConfig)
	if said, _ := json.Marshal(map[string]string{"t6Path": own}); read(t, config) != string(said) {
		t.Errorf("Plutonium's settings: %s, and not %s", read(t, config), said)
	}
	*tl.revision = 2
	l.mu.Lock()
	l.play.Plutonium = 2
	l.mu.Unlock()
	run("a copy of another revision than the server's", false)
	if !strings.Contains(tl.out.String(), "Black Ops II is starting at its menu, for you to play as Ann.") {
		t.Errorf("what the launcher said:\n%s", tl.out)
	}
}

// A link of Play local from a page that has no server: the launcher calls no server and asks nothing of one, but
// starts the game alone under the link's name, from what the PC has.
func TestAloneWithNoServer(t *testing.T) {
	l := newFakeServer(t)
	tl := newTestLauncher(t, "n\n")
	own := ownCopy(t, tl)
	tl.cfg.Servers = []string{"https://other.example"} // a server that the player trusts: the link's isn't
	write(t, tl.configFile(), `{"servers": ["https://other.example"]}`)
	link := strings.Replace(l.link(), "key="+l.key, "solo=1&name=Ann+B", 1)
	if code := tl.run(context.Background(), []string{link}); code != 0 || len(tl.started) != 1 ||
		!strings.HasSuffix(tl.started[0].line, ` t6zm "`+own+`" -lan +name "Ann B"`) {
		t.Fatalf("%d, started %+v\n%s", code, tl.started, tl.out)
	}
	l.mu.Lock()
	asked := l.asked
	l.mu.Unlock()
	if len(asked) != 0 || strings.Contains(tl.out.String(), "Play on") {
		t.Errorf("the launcher called the server %q, or asked whether to play there\n%s", asked, tl.out)
	}
}

// A launcher that waits for the game of its player's room starts the player's game at once, at its menu, when they
// press the room's Launch game, and tells the server that the game runs, for as long as it does. The room's game is
// then joined from the game that runs, through its console.
func TestLaunchGameFromTheRoom(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Plutonium: 1}
	tl := newTestLauncher(t, "")
	ownCopy(t, tl)
	var lines []string
	var g *fakeGame
	starts := &atomic.Int32{}
	tl.start = func(exe, line, dir string) (game, error) {
		lines = append(lines, line)
		g = &fakeGame{}
		g.open.Store(true)
		starts.Add(1)
		return g, nil
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link()}) }()
	last := func(said func(l *fakeServer) []bool) bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(said(l)) > 0 && said(l)[len(said(l))-1]
	}
	ready := func() bool { return last(func(l *fakeServer) []bool { return l.ready }) }
	runs := func() bool { return last(func(l *fakeServer) []bool { return l.games }) }
	eventually(t, "the launcher got the PC ready", ready)
	time.Sleep(50 * time.Millisecond)
	if starts.Load() != 0 || runs() {
		t.Fatalf("the launcher started a game before the room's game, unasked: %d, said that it runs: %v", starts.Load(), runs())
	}
	tl.solo <- struct{}{}
	eventually(t, "the game started at its menu", func() bool { return starts.Load() == 1 })
	if !strings.HasSuffix(lines[0], ` -lan +name "Ann"`) {
		t.Errorf("the game started with %q", lines[0])
	}
	eventually(t, "the server heard that the game runs, on a PC that is ready", func() bool { return runs() && ready() })
	l.mu.Lock()
	if said := l.ready[slices.Index(l.ready, true):]; slices.Contains(said, false) {
		t.Errorf("the launcher took back that the PC is ready for the room's game, as it started the game: %v", said)
	}
	l.mu.Unlock()
	if typed := g.typed(); len(typed) != 0 {
		t.Errorf("the game was told %q with no room's game to join", typed)
	}
	// the room's game, which the game that runs joins from its menu
	l.mu.Lock()
	l.play.Game = &launcherapi.Game{ID: "one", Port: l.port(), Password: "pw", Name: "Ann", Up: true}
	l.mu.Unlock()
	eventually(t, "the game joined the room's game", func() bool { return len(g.typed()) == 1 })
	if starts.Load() != 1 || !strings.HasPrefix(g.typed()[0], "password pw;connect ") {
		t.Errorf("%d games started; typed %q", starts.Load(), g.typed())
	}
	g.open.Store(false)
	eventually(t, "the server heard that the game was closed", func() bool { return !runs() })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// A launcher in the tray, for its player's rooms, starts their game alone when they press Play local, and stays with
// it: a room's game that they then ready up for joins from the game that runs, through its console.
func TestAloneFromTheTray(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann"}
	tl := newTestLauncher(t, "")
	ownCopy(t, tl)
	var lines []string
	var g *fakeGame
	starts := &atomic.Int32{}
	tl.start = func(exe, line, dir string) (game, error) {
		lines = append(lines, line)
		g = &fakeGame{}
		g.open.Store(true)
		starts.Add(1)
		return g, nil
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link()}) }()
	eventually(t, "the launcher got the PC ready", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.ready) > 0 && l.ready[len(l.ready)-1]
	})
	if starts.Load() != 0 {
		t.Fatal("the launcher started a game with no room's game to join")
	}
	tl.solo <- struct{}{}
	eventually(t, "the game started alone", func() bool { return starts.Load() == 1 })
	if !strings.HasSuffix(lines[0], ` -lan +name "Ann"`) {
		t.Errorf("the game alone started with %q", lines[0])
	}
	eventually(t, "the page's button says that the game launches", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return slices.ContainsFunc(l.reports, func(r launcherapi.Report) bool { return r.Doing == "Launching the game" })
	})
	// another press of Play local while the game runs starts no second one
	tl.solo <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	// the room's game, which the game that runs joins from its menu
	l.mu.Lock()
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Plutonium: 1,
		Game: &launcherapi.Game{ID: "one", Port: l.port(), Password: "pw", Name: "Ann", Up: true}}
	l.mu.Unlock()
	eventually(t, "the game alone joined the room's game", func() bool { return len(g.typed()) == 1 })
	if starts.Load() != 1 || !strings.HasPrefix(g.typed()[0], "password pw;connect ") {
		t.Errorf("%d games started; typed %q", starts.Load(), g.typed())
	}
	g.open.Store(false)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}
