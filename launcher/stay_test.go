package main

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zombies/internal/launcherapi"
)

// eventually waits for ok, a few seconds at most.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
	}
}

// stayingLauncher is a launcher that stays (serve) on a PC with its own copy of the game, whose games close as they
// start: starts counts them. quit ends it, and code is what its run returned.
func stayingLauncher(t *testing.T, l *fakeServer, link string, own bool) (tl *testLauncher, starts *atomic.Int32, quit func() int) {
	t.Helper()
	tl = newTestLauncher(t, "")
	if own {
		dir := filepath.Join(t.TempDir(), "Black Ops II")
		write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
		write(t, filepath.Join(dir, "binkw32.dll"), "bink")
		tl.copies = func(*title) []string { return []string{dir} }
	}
	starts = &atomic.Int32{}
	tl.start = func(exe, line, dir string) (game, error) {
		starts.Add(1)
		return &fakeGame{}, nil
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{link}) }()
	return tl, starts, func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(5 * time.Second):
			t.Fatal("the launcher did not end as it was quit")
			return -1
		}
	}
}

// A launcher that stays starts the player's game into their room's, and is there for the next: not into the game
// their own just left, until they ask for it with the room's button, and by itself into the room's next game. It
// ends as it is quit.
func TestLauncherStaysForTheNextGame(t *testing.T) {
	l := newFakeServer(t)
	game := func(id string) *launcherapi.Game {
		return &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann"}
	}
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Game: game("one")}
	tl, starts, quit := stayingLauncher(t, l, l.link(), true)
	eventually(t, "the game started into the room's", func() bool { return starts.Load() == 1 })
	time.Sleep(100 * time.Millisecond) // many times what it waits between two looks
	if n := starts.Load(); n != 1 {
		t.Fatalf("the game the player closed was started %d times, into the game it left", n)
	}
	tl.press()
	eventually(t, "the game started again as the player asked", func() bool { return starts.Load() == 2 })
	l.mu.Lock()
	l.play.Game = game("two")
	l.mu.Unlock()
	eventually(t, "the game started into the room's next game", func() bool { return starts.Load() == 3 })
	l.mu.Lock()
	l.play = launcherapi.Play{Player: "Ann"} // they left the room
	asked := len(l.ready)
	l.mu.Unlock()
	eventually(t, "the launcher still tells the server that it runs, and that the PC is ready", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.ready) > asked+2 && l.ready[len(l.ready)-1]
	})
	if code := quit(); code != 0 || starts.Load() != 3 {
		t.Errorf("quit: %d, after %d starts", code, starts.Load())
	}
	// it told the server that it was quit, as the last thing it said
	l.mu.Lock()
	said := slices.Clone(l.asked)
	l.mu.Unlock()
	if len(said) == 0 || said[len(said)-1] != "POST /launcher/stopped" {
		t.Errorf("the launcher's last calls as it was quit: %q", said[max(0, len(said)-3):])
	}
}

// What a launcher that stays fails at is the server's to show, in the player's page; a new try says it no more. The
// launcher stays through it, for the player to try again with the room's button.
func TestStayingLauncherTellsTheServerWhatFailed(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD"} // a server that sends no game files, to a PC with none
	failures := func() (n int, last launcherapi.Report) {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, r := range l.reports {
			if r.Failed != "" {
				n++
			}
		}
		if len(l.reports) > 0 {
			last = l.reports[len(l.reports)-1]
		}
		return n, last
	}
	// tries counts the launcher's tries: each begins with a report that it is updating Plutonium
	tries := func() int {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := 0
		for _, r := range l.reports {
			if r.Doing == "Updating Plutonium" {
				n++
			}
		}
		return n
	}
	tl, starts, quit := stayingLauncher(t, l, l.link(), false)
	eventually(t, "the server was told what failed", func() bool { n, _ := failures(); return n == 1 })
	if _, last := failures(); !strings.Contains(last.Failed, "Black Ops II") || strings.Contains(last.Failed, "\n\n") {
		t.Errorf("what failed, as the server was told: %q", last.Failed)
	}
	// the launcher does not try again by itself, though its player is still in the room
	time.Sleep(200 * time.Millisecond)
	if n, _ := failures(); n != 1 || tries() != 1 {
		t.Fatalf("with nothing pressed, the launcher tried %d times and reported %d failures", tries(), n)
	}
	tl.press()
	eventually(t, "it tried again as the player asked, and failed again", func() bool { n, _ := failures(); return n == 2 })
	l.mu.Lock()
	cleared := false
	for i, r := range l.reports {
		if r.Failed != "" && i+1 < len(l.reports) && l.reports[i+1].Failed == "" {
			cleared = true
		}
	}
	l.mu.Unlock()
	if !cleared {
		t.Error("the new try did not take back what the last one failed at")
	}
	if code := quit(); code != 0 || starts.Load() != 0 {
		t.Errorf("quit: %d, after %d starts", code, starts.Load())
	}
}

// A launcher whose player has been in no Black Ops II room for a while closes its call to the server, and sends
// nothing until the player presses a room's button. In a room it keeps the call open.
func TestStayingLauncherRestsOutsideARoom(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann"}
	asked := func() int {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.ready)
	}
	// quiet reports whether the launcher made no call for a while
	quiet := func() bool {
		n := asked()
		time.Sleep(100 * time.Millisecond)
		return asked() == n
	}
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	tl := newTestLauncher(t, "")
	tl.copies = func(*title) []string { return []string{own} }
	tl.stays, tl.away = true, 50*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{l.link()}) }()
	eventually(t, "the launcher rests, its player in no room", quiet)
	// the player is in a room now, which a launcher that rests does not hear of
	l.mu.Lock()
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD"}
	l.mu.Unlock()
	if !quiet() {
		t.Fatal("the launcher called the server while it rested")
	}
	// the room's button wakes it, and in the room it does not rest
	n := asked()
	tl.press()
	eventually(t, "the launcher asks the server again", func() bool { return asked() > n })
	time.Sleep(200 * time.Millisecond)
	if quiet() {
		t.Error("the launcher rested with its player in a room")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("quit: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not end as it was quit")
	}
}

// A server that knows the player no more ends the launcher: their page starts it again.
func TestStayingLauncherEndsForAnUnknownPlayer(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann"}
	tl := newTestLauncher(t, "")
	tl.stays = true
	done := make(chan int, 1)
	go func() {
		done <- tl.run(context.Background(), []string{strings.Replace(l.link(), l.key, "ffffffffffffffff", 1)})
	}()
	select {
	case code := <-done:
		if code == 0 {
			t.Errorf("the run: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher stayed for a server that doesn't know its player")
	}
}

// The room's button says what a launcher that stays does with the player's game: the game launches, or the game that
// runs joins the room's game. Once the server has the game in, the launcher says nothing, and when the game is out of
// the room's game again, the button asks to join again, which has the game that runs join it.
func TestStayingLauncherSaysWhatItDoes(t *testing.T) {
	l := newFakeServer(t)
	gameOf := func(id string, up bool) *launcherapi.Game {
		return &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann", Up: up}
	}
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Game: gameOf("one", true), Plutonium: 1}
	tl := newTestLauncher(t, "")
	dir := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(dir, "binkw32.dll"), "bink")
	tl.copies = func(*title) []string { return []string{dir} }
	g, _, quit := stayWithTray(t, tl, l.link(), true)
	set := func(change func(play *launcherapi.Play)) {
		l.mu.Lock()
		change(&l.play)
		l.mu.Unlock()
	}
	// says waits until what the launcher last reported doing is what
	says := func(when, what string) {
		t.Helper()
		eventually(t, when+": the launcher says "+strconv.Quote(what), func() bool {
			l.mu.Lock()
			defer l.mu.Unlock()
			return len(l.reports) > 0 && l.reports[len(l.reports)-1].Doing == what
		})
	}
	eventually(t, "the game started", func() bool { return g.count() == 1 })
	g.mu.Lock()
	first := g.all[0]
	g.mu.Unlock()
	// the room's game is up: the game is asked to join it from its console too, and the button says what it said
	eventually(t, "the game asked its room's game to let it in", func() bool { return len(first.typed()) == 1 })
	says("the game launches", "Launching the game")
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	says("the game is in", "")
	set(func(play *launcherapi.Play) { play.InGame = false }) // game over: the room is open
	set(func(play *launcherapi.Play) { play.Game = gameOf("two", false) })
	says("the room's next game starts", "Joining the game")
	set(func(play *launcherapi.Play) { play.Game = gameOf("two", true) })
	eventually(t, "the game joined the next game", func() bool { return len(first.typed()) == 2 })
	says("the game joins the next game", "Joining the game")
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	says("the game is in the next game", "")
	set(func(play *launcherapi.Play) { play.InGame, play.Game = false, gameOf("two", true) }) // it went back to its menu
	time.Sleep(100 * time.Millisecond)
	says("the game is out of the room's game", "")
	tl.press() // Join the game
	eventually(t, "the game joined again", func() bool { return len(first.typed()) == 3 })
	says("the game joins again", "Joining the game")
	if g.count() != 1 || first.stopped.Load() {
		t.Errorf("the launcher started %d games, and closed the first: %v", g.count(), first.stopped.Load())
	}
	quit()
}

// A game of Black Ops II asks its room's game once to let it in, as it starts, and never again: when the room's game
// is not up by then, the game is not in it, however long it waits. So the launcher has the game ask from its console
// once the room's game is up, and again a while later for as long as the server doesn't have the game in it, a few
// times in all. Once the server has the game in, the launcher has it ask no more.
func TestAsksAgainUntilTheGameIsIn(t *testing.T) {
	for _, c := range []struct {
		what string
		in   bool // the server has the game in after its first ask
		asks int
	}{{"a game that never gets in", false, joinTries}, {"a game that is in after its first ask", true, 1}} {
		l := newFakeServer(t)
		gameOf := func(up bool) *launcherapi.Game {
			return &launcherapi.Game{ID: "one", Port: l.port(), Password: "pw", Name: "Ann", Up: up}
		}
		l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Game: gameOf(false), Plutonium: 1}
		tl := newTestLauncher(t, "")
		tl.joinEvery = 150 * time.Millisecond
		dir := filepath.Join(t.TempDir(), "Black Ops II")
		write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
		write(t, filepath.Join(dir, "binkw32.dll"), "bink")
		tl.copies = func(*title) []string { return []string{dir} }
		g, _, quit := stayWithTray(t, tl, l.link(), true)
		set := func(change func(play *launcherapi.Play)) {
			l.mu.Lock()
			change(&l.play)
			l.mu.Unlock()
		}
		eventually(t, c.what+": the game started", func() bool { return g.count() == 1 })
		g.mu.Lock()
		first := g.all[0]
		g.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		if typed := first.typed(); len(typed) != 0 {
			t.Fatalf("%s: it was told %q before the room's game was up", c.what, typed)
		}
		set(func(play *launcherapi.Play) { play.Game = gameOf(true) })
		eventually(t, c.what+": the game asked the room's game, once that was up", func() bool { return len(first.typed()) >= 1 })
		if c.in {
			set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
		} else {
			eventually(t, c.what+": the game asked again", func() bool { return len(first.typed()) == joinTries })
		}
		time.Sleep(500 * time.Millisecond) // more than three times what the launcher waits before it asks again
		typed := first.typed()
		if len(typed) != c.asks || slices.ContainsFunc(typed, func(line string) bool {
			return line != "password pw;connect 127.0.0.1:"+strconv.Itoa(l.port())
		}) {
			t.Errorf("%s: it was told %q, not the line that joins %d times", c.what, typed, c.asks)
		}
		if g.count() != 1 || first.stopped.Load() {
			t.Errorf("%s: the launcher started %d games, and closed the first: %v", c.what, g.count(), first.stopped.Load())
		}
		quit()
	}
}

// When a room's game ends with the player's game in it, the server says so a moment before it quits the game
// (Play.Leave), and the launcher has the player's game leave first, with its console's disconnect: once for a game.
func TestLeavesTheRoomsGameThatEnds(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Plutonium: 1,
		Game: &launcherapi.Game{ID: "one", Port: l.port(), Password: "pw", Name: "Ann"}}
	tl := newTestLauncher(t, "")
	dir := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(dir, "binkw32.dll"), "bink")
	tl.copies = func(*title) []string { return []string{dir} }
	g, _, quit := stayWithTray(t, tl, l.link(), true)
	set := func(change func(play *launcherapi.Play)) {
		l.mu.Lock()
		change(&l.play)
		l.mu.Unlock()
	}
	eventually(t, "the game started", func() bool { return g.count() == 1 })
	g.mu.Lock()
	first := g.all[0]
	g.mu.Unlock()
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil })
	time.Sleep(100 * time.Millisecond)
	if typed := first.typed(); len(typed) != 0 {
		t.Fatalf("the game was told %q while it was in the room's game", typed)
	}
	set(func(play *launcherapi.Play) { play.Leave = true }) // game over, and its end has played out
	eventually(t, "the game left the room's game", func() bool { return slices.Equal(first.typed(), []string{leaveLine}) })
	set(func(play *launcherapi.Play) { play.InGame, play.Leave = false, false }) // the room's game is gone
	time.Sleep(100 * time.Millisecond)
	// the room's next game ends the same way
	set(func(play *launcherapi.Play) { play.InGame, play.Leave = true, true })
	eventually(t, "the game left the room's next game", func() bool {
		return slices.Equal(first.typed(), []string{leaveLine, leaveLine})
	})
	if g.count() != 1 || first.stopped.Load() {
		t.Errorf("the launcher started %d games, and closed the first: %v", g.count(), first.stopped.Load())
	}
	quit()
}
