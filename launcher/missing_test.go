package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"zombies/internal/launcherapi"
)

// The zones of the server's maps, in these tests: Origins comes with a map pack, and TranZit with the game.
const tomb, transit = "zone/all/zm_tomb.ff", "zone/all/zm_transit.ff"

// packs is a server that sends game files with both maps, and has a game in room ABCD on the map in the zone needs.
// game is the game's ID, or "-" for a room that still waits for its players.
func packs(t *testing.T, needs, game string) *fakeServer {
	l := downloading(t, game)
	l.files[transit] = "TranZit"
	l.play.Zones, l.play.Needs = []string{tomb, transit}, needs
	if game == "-" {
		l.play.Game = nil
	}
	return l
}

// said returns which zones the launcher last said the game folder has: "-" when it said nothing of the folder.
func (l *fakeServer) said() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.has) == 0 {
		return "-"
	}
	return l.has[len(l.has)-1]
}

// withoutPack makes a copy of the game that has TranZit, and not Origins: a copy without the map's pack.
func withoutPack(t *testing.T, dir string) string {
	t.Helper()
	copyOf(t, dir)
	write(t, filepath.Join(dir, filepath.FromSlash(transit)), "TranZit")
	return dir
}

// A copy of the game without a map's pack lacks the map's zone. The launcher tells the server which of its zones the
// game folder has, for the room to say. In a run that the player started, on a server that sends game files, it asks
// what to do about a copy that lacks the room's map. The player leaves things as they are, or shows it another copy,
// or has it download the server's game files, which it then plays from.
func TestCopyWithoutTheRoomsMap(t *testing.T) {
	root := t.TempDir()
	own := withoutPack(t, filepath.Join(root, "Games", t6Folder))
	whole := withoutPack(t, filepath.Join(root, "Whole", t6Folder))
	write(t, filepath.Join(whole, filepath.FromSlash(tomb)), "Origins")
	l := packs(t, transit, "")
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	tl.copies = func() []string { return []string{own} }
	run := func(what, from string, asked int) {
		t.Helper()
		tl.again("")
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
			!strings.Contains(tl.started[0].line, `t6zm "`+from+`" -lan`) {
			t.Fatalf("%s: %d, started %+v\n%s", what, code, tl.started, tl.out)
		}
		if n, _, _ := d.seen(); n != asked {
			t.Fatalf("%s: the launcher asked %d questions, not %d: %q", what, n, asked, d.asked)
		}
	}
	needs := func(zone string) {
		l.mu.Lock()
		l.play.Needs = zone
		l.mu.Unlock()
	}

	// a room on a map that the copy has: nothing is asked. (What the server hears of the folder is in
	// TestMissingMapAsksWhenPressed: a run that ends with its game may end before the call that says it.)
	run("a map the copy has", own, 0)
	// a room on a map that the copy lacks: the player cancels, and things are as they were
	needs(tomb)
	d.choices = []int{-1}
	run("a map the copy lacks, then Cancel", own, 1)
	if !strings.HasPrefix(d.asked[0], "Your Black Ops II doesn't have this map.\n\n"+own) || len(d.answers[0]) != 2 ||
		!strings.HasPrefix(d.answers[0][0], "Download\n") || !strings.HasPrefix(d.answers[0][1], "Choose another folder…") {
		t.Errorf("the question: %q, with the answers %q", d.asked[0], d.answers[0])
	}
	if c := configOf(tl); c.T6 != "" || isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
		t.Errorf("Cancel: the config has %+v, or the launcher downloaded the game files", c)
	}
	// they show the launcher another copy, which has the map
	d.choices, d.picks = []int{1}, []string{whole}
	run("another copy", whole, 2)
	if c := configOf(tl); c.T6 != whole {
		t.Errorf("another copy: the config has %+v", c)
	}
	// back on their first copy, they have the launcher download the server's game files: it uses them, from
	// then on, and their copy is as it was
	if _, err := tl.rewrite(func(c *config) { c.T6 = "" }); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(tl.home, "t6")
	d.choices = []int{0}
	run("Download", files, 3)
	if c := configOf(tl); c.T6 != "none" || read(t, filepath.Join(files, filepath.FromSlash(tomb))) != "Origins" ||
		isFile(filepath.Join(own, filepath.FromSlash(tomb))) {
		t.Errorf("Download: the config has %+v, or the player's own copy was changed", c)
	}
	if !strings.Contains(d.answers[2][0], files) {
		t.Errorf("Download's answer does not say where the files go: %q", d.answers[2][0])
	}
	run("Download, then the next run", files, 3)

	// a build without task dialogs asks yes or no
	tl = newTestLauncher(t, "")
	d = answer(tl, false)
	tl.copies = func() []string { return []string{own} }
	d.confirms = []bool{true}
	run("yes, in a build without task dialogs", filepath.Join(tl.home, "t6"), 1)
	if c := configOf(tl); c.T6 != "none" {
		t.Errorf("yes: the config has %+v", c)
	}
	// a server that sends no game files has none to offer: the launcher asks nothing, and the room says the rest
	l.mu.Lock()
	l.play.Downloads = false
	l.mu.Unlock()
	tl = newTestLauncher(t, "")
	d = answer(tl, true)
	tl.copies = func() []string { return []string{own} }
	run("a server that sends no game files", own, 0)
}

// While the player's copy lacks the room's map, the room's button says Download. A press of it starts the launcher's
// run again, and that run asks. A run that the launcher starts by itself, for the room's next game, asks nothing.
func TestMissingMapAsksWhenPressed(t *testing.T) {
	own := withoutPack(t, filepath.Join(t.TempDir(), "Games", t6Folder))
	l := packs(t, tomb, "-")
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	tl.copies = func() []string { return []string{own} }
	d.choices = []int{-1, 0} // Cancel in the run that the player's link started, then Download at their press
	_, _, quit := stayWithTray(t, tl, l.link(), false)
	eventually(t, "the launcher said that the folder has TranZit alone", func() bool { return l.said() == transit })
	if asked, _, _ := d.seen(); asked != 1 {
		t.Fatalf("the launcher asked %d questions in the run that the player started", asked)
	}
	tl.button <- struct{}{}
	files := filepath.Join(tl.home, "t6")
	eventually(t, "the launcher downloaded the server's game files, and said that the folder has both maps", func() bool {
		return l.said() == tomb+","+transit && isFile(filepath.Join(files, filepath.FromSlash(tomb)))
	})
	if asked, _, _ := d.seen(); asked != 2 || configOf(tl).T6 != "none" {
		t.Errorf("after the press: %d questions, config %+v", asked, configOf(tl))
	}
	quit()

	// the room's next game starts the launcher's run by itself
	l = packs(t, tomb, "one")
	tl = newTestLauncher(t, "")
	d = answer(tl, true)
	tl.copies = func() []string { return []string{own} }
	g, _, quit := stayWithTray(t, tl, l.link(), false)
	eventually(t, "the game started", func() bool { return g.count() == 1 })
	l.mu.Lock()
	l.play.Game = &launcherapi.Game{ID: "two", Port: l.port(), Password: "pw", Name: "Ann"}
	l.mu.Unlock()
	eventually(t, "the game started for the room's next game", func() bool { return g.count() == 2 })
	if asked, _, _ := d.seen(); asked != 1 {
		t.Errorf("the launcher asked %d questions: one in the run that the player started, and none after", asked)
	}
	quit()
}
