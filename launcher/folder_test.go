package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zombies/internal/launcherapi"
)

// dialogs is the player at a launcher with no window: they answer its dialogs as the lists say, in turn. picks are
// the folders they choose in the folder picker ("" cancels), choices their answers in task dialogs (-1 cancels), and
// confirms their answers to yes-or-no questions. A list that has run out cancels. asked has each question, titles
// each picker's title, and told each message. The menu's dialogs run beside the run, so the lists have a lock.
type dialogs struct {
	mu       sync.Mutex
	picks    []string
	choices  []int
	confirms []bool
	asked    []string
	answers  [][]string // each task dialog's answers
	titles   []string
	told     []string
}

// answer makes tl a launcher with no window, whose dialogs d answers. tasks says whether its build has task dialogs.
func answer(tl *testLauncher, tasks bool) *dialogs {
	d := &dialogs{}
	tl.boxes = true
	tl.pick = func(title string) (string, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.titles = append(d.titles, title)
		if len(d.picks) == 0 {
			return "", nil
		}
		dir := d.picks[0]
		d.picks = d.picks[1:]
		return dir, nil
	}
	tl.choose = nil
	if tasks {
		tl.choose = func(question string, answers []string) int {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.asked, d.answers = append(d.asked, question), append(d.answers, answers)
			if len(d.choices) == 0 {
				return -1
			}
			n := d.choices[0]
			d.choices = d.choices[1:]
			return n
		}
	}
	tl.confirm = func(question string) bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.asked = append(d.asked, question)
		if len(d.confirms) == 0 {
			return false
		}
		yes := d.confirms[0]
		d.confirms = d.confirms[1:]
		return yes
	}
	tl.notify = func(text string, bad bool) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.told = append(d.told, text)
	}
	return d
}

// seen returns how many questions, pickers and messages the player has seen.
func (d *dialogs) seen() (asked, picked, told int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.asked), len(d.titles), len(d.told)
}

func (d *dialogs) lastTold() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.told) == 0 {
		return ""
	}
	return d.told[len(d.told)-1]
}

// copyOf makes a copy of Black Ops II in dir, as gameIn and hasT6 know one.
func copyOf(t *testing.T, dir string) string {
	t.Helper()
	write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(dir, "binkw32.dll"), "bink")
	return dir
}

// configOf returns the config as the launcher's file has it.
func configOf(tl *testLauncher) config {
	tl.cfgLock.Lock()
	defer tl.cfgLock.Unlock()
	return tl.readConfig()
}

// downloading is a server that sends game files, and has a game in room ABCD. The game has an ID only where a test
// says so: a run doesn't start the player's game again into the game it left (play).
func downloading(t *testing.T, id string) *fakeServer {
	l := newFakeServer(t)
	l.files = map[string]string{"binkw32.dll": "bink", "zone/all/patch_zm.ff": "the patch", "zone/all/zm_tomb.ff": "Origins"}
	l.play = launcherapi.Play{Player: "Ann", Downloads: true, Room: "ABCD",
		Game: &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann"}}
	return l
}

// A folder that the player chose has the game in it, or in the places where a Steam library has it.
func TestGameIn(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "SteamLibrary")
	game := copyOf(t, filepath.Join(library, "steamapps", "common", titleT6.folder))
	games := filepath.Join(root, "Games")
	copied := copyOf(t, filepath.Join(games, titleT6.folder))
	downloaded := copyOf(t, filepath.Join(root, "Downloaded"))
	write(t, filepath.Join(downloaded, syncFile), "{}")
	for dir, want := range map[string]string{
		game:                                game,
		library:                             game,
		filepath.Join(library, "steamapps"): game,
		filepath.Join(library, "steamapps", "common"): game,
		games:                       copied,
		copied:                      copied,
		root:                        "",
		filepath.Join(game, "zone"): "",
		downloaded:                  "", // the launcher's own download is no copy of the player's
		"":                          "",
	} {
		if got := gameIn(titleT6, dir); got != want {
			t.Errorf("gameIn(%q) = %q, not %q", dir, got, want)
		}
	}
}

// On a PC where the launcher finds the game, as Steam installed it, the launcher asks nothing, shows no picker and no
// message: its run is as it was. The tray's menu says which folder the game starts from, and does nothing until the
// player clicks one of its items. Its first line has the launcher's version.
func TestFoundCopyAsksNothing(t *testing.T) {
	l := downloading(t, "one") // a game with an ID: the launcher starts the player's game into it once, and not again
	own := copyOf(t, filepath.Join(t.TempDir(), "SteamLibrary", "steamapps", "common", titleT6.folder))
	tl := newTestLauncher(t, "")
	tl.version = 4
	d := answer(tl, true)
	tl.copies = func(*title) []string { return []string{own} }
	g, menu, quit := stayWithTray(t, tl, l.link(), false)
	eventually(t, "the game started", func() bool { return g.count() == 1 })
	if line := g.line(0); !strings.Contains(line, `t6zm "`+own+`" -lan`) {
		t.Errorf("the game started with %s", line)
	}
	items := menu()
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.text
	}
	if !slices.Contains(texts, "Black Ops II: your copy, "+short(own)) || !slices.Contains(texts, "Choose Black Ops II folder…") ||
		slices.ContainsFunc(texts, func(s string) bool { return strings.HasPrefix(s, "Delete") }) {
		t.Errorf("the menu: %q", texts)
	}
	if !strings.HasPrefix(texts[0], "Zombies Launcher v4: ") {
		t.Errorf("the menu's first line: %q", texts[0])
	}
	if asked, picked, told := d.seen(); asked+picked+told != 0 {
		t.Errorf("on a PC with the game, the launcher asked %d questions, showed %d pickers and %d messages: %q %q",
			asked, picked, told, d.asked, d.told)
	}
	if isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
		t.Error("the launcher downloaded game files for a PC with the game")
	}
	quit()
}

// The player chooses their copy's folder in the question, as the launcher found none. The launcher keeps the folder,
// starts the game from it and downloads nothing; the next run asks nothing. A folder above the game's own, as a Steam
// library has it, is as good.
func TestChooseOwnFolder(t *testing.T) {
	l := downloading(t, "")
	root := t.TempDir()
	library := filepath.Join(root, "SteamLibrary")
	own := copyOf(t, filepath.Join(library, "steamapps", "common", titleT6.folder))
	for _, picked := range []string{own, library} {
		tl := newTestLauncher(t, "")
		d := answer(tl, true)
		d.choices, d.picks = []int{0}, []string{picked}
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
			!strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) {
			t.Fatalf("chose %s: %d, started %+v\n%s", picked, code, tl.started, tl.out)
		}
		if len(d.answers) != 1 || len(d.answers[0]) != 3 || !strings.HasPrefix(d.answers[0][0], "Choose folder…") ||
			!strings.HasPrefix(d.answers[0][1], "Download\n") || !strings.HasPrefix(d.answers[0][2], "Download to another folder…") {
			t.Errorf("chose %s: the question's answers were %q", picked, d.answers)
		}
		if c := configOf(tl); c.T6 != own || isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
			t.Errorf("chose %s: the config has %+v, or the launcher downloaded the game files", picked, c)
		}
		tl.again("")
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
			!strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) || len(d.asked) != 1 || len(d.titles) != 1 {
			t.Errorf("chose %s, then the next run: %d, started %+v, asked %q\n%s", picked, code, tl.started, d.asked, tl.out)
		}
	}
	// a server that sends no game files asks for the folder alone
	l.mu.Lock()
	l.play.Downloads = false
	l.mu.Unlock()
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices, d.picks = []int{0}, []string{own}
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) || len(d.answers) != 1 ||
		len(d.answers[0]) != 1 || !strings.HasPrefix(d.answers[0][0], "Choose folder…\n") {
		t.Errorf("a server that sends no files: %d, started %+v, answers %q\n%s", code, tl.started, d.answers, tl.out)
	}
}

// A folder without the game, or with a path the game can't read, gets one message that says what the folder needs,
// and the player chooses again, or cancels. Cancelling changes nothing.
func TestChooseWrongFolder(t *testing.T) {
	l := downloading(t, "")
	root := t.TempDir()
	wrong := filepath.Join(root, "Documents")
	write(t, filepath.Join(wrong, "notes.txt"), "notes")
	zoe := copyOf(t, filepath.Join(root, "Zoë", titleT6.folder))
	own := copyOf(t, filepath.Join(root, "Games", titleT6.folder))
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices, d.picks = []int{0, -1}, []string{wrong}
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 ||
		!strings.Contains(tl.out.String(), "Nothing was downloaded") {
		t.Fatalf("a wrong folder, then Cancel: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	if len(d.asked) != 2 || !strings.HasPrefix(d.asked[1], "Black Ops II isn't in that folder.\n\n"+wrong) ||
		!strings.Contains(d.asked[1], "t6zm.exe") || !slices.Equal(d.answers[1], []string{"Choose another folder…"}) {
		t.Errorf("a wrong folder: asked %q, %q", d.asked, d.answers)
	}
	if _, err := os.Stat(tl.configFile()); err == nil {
		if c := configOf(tl); c.T6 != "" || c.T6Files != "" {
			t.Errorf("a wrong folder, then Cancel: the config has %+v", c)
		}
	}
	if isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
		t.Error("a wrong folder, then Cancel: the launcher downloaded the game files")
	}
	// a folder whose path the game can't read, then a wrong one, then the game's
	tl.again("")
	d.asked, d.answers = nil, nil
	d.choices, d.picks = []int{0, 0, 0}, []string{zoe, wrong, own}
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) {
		t.Fatalf("chose again: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	if len(d.asked) != 3 || !strings.Contains(d.asked[1], "can't run from that folder") || !strings.Contains(d.asked[1], "A to Z") ||
		!strings.Contains(d.asked[2], "isn't in that folder") || configOf(tl).T6 != own {
		t.Errorf("chose again: asked %q, config %+v", d.asked, configOf(tl))
	}
}

// The player has the launcher download the game files: into its own folder, or into a folder they choose, which the
// launcher keeps. A folder with other things in it gets a folder of the game's own in it. A folder with the game in it
// needs no download.
func TestDownloadAsChosen(t *testing.T) {
	l := downloading(t, "")
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices = []int{1}
	files := filepath.Join(tl.home, "t6")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+files+`" -lan`) || read(t, filepath.Join(files, "zone", "all", "zm_tomb.ff")) != "Origins" ||
		len(d.titles) != 0 || !strings.Contains(d.answers[0][1], files) {
		t.Fatalf("Download: %d, started %+v, pickers %q, answers %q\n%s", code, tl.started, d.titles, d.answers, tl.out)
	}
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 || len(d.asked) != 1 ||
		strings.Contains(tl.out.String(), "Downloading") {
		t.Errorf("Download, then the next run: %d, asked %q\n%s", code, d.asked, tl.out)
	}
	root := t.TempDir()
	empty := filepath.Join(root, "Zombies")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	busy := filepath.Join(root, "Games")
	write(t, filepath.Join(busy, "another game.exe"), "another game")
	own := copyOf(t, filepath.Join(root, "Mine", titleT6.folder))
	for _, c := range []struct{ picked, files string }{
		{empty, empty},
		{busy, filepath.Join(busy, titleT6.folder)},
		{filepath.Dir(own), ""}, // the game is there: nothing to download
	} {
		tl := newTestLauncher(t, "")
		d := answer(tl, true)
		d.choices, d.picks = []int{2}, []string{c.picked}
		want := c.files
		if want == "" {
			want = own
		}
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
			!strings.Contains(tl.started[0].line, `t6zm "`+want+`" -lan`) || len(d.titles) != 1 || !strings.Contains(d.titles[0], "where to download") {
			t.Fatalf("Download into %s: %d, started %+v, pickers %q\n%s", c.picked, code, tl.started, d.titles, tl.out)
		}
		cfg := configOf(tl)
		if c.files != "" && (cfg.T6Files != c.files || read(t, filepath.Join(c.files, "binkw32.dll")) != "bink") ||
			c.files == "" && (cfg.T6 != own || isFile(filepath.Join(own, syncFile))) {
			t.Errorf("Download into %s: the config has %+v", c.picked, cfg)
		}
		tl.again("")
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
			!strings.Contains(tl.started[0].line, `t6zm "`+want+`" -lan`) || len(d.asked) != 1 || strings.Contains(tl.out.String(), "Downloading") {
			t.Errorf("Download into %s, then the next run: %d, started %+v, asked %q\n%s", c.picked, code, tl.started, d.asked, tl.out)
		}
	}
	if read(t, filepath.Join(busy, "another game.exe")) != "another game" {
		t.Error("the download changed a file of the folder it was put in")
	}
}

// A change of the config from the tray's menu stays: a later change by the run, made from what it read before, keeps
// it, and the run's next search for the game files finds it.
func TestConfigChangesKeepEachOther(t *testing.T) {
	tl := newTestLauncher(t, "")
	if err := tl.change(func(c *config) { c.Servers = []string{"https://one.example"} }); err != nil {
		t.Fatal(err)
	}
	tl.loadConfig() // the run read the config
	own := copyOf(t, filepath.Join(t.TempDir(), "Mine"))
	if _, err := tl.rewrite(func(c *config) { c.T6 = own }); err != nil { // the menu changed it since
		t.Fatal(err)
	}
	if tl.cfg.T6 != "" {
		t.Fatal("the menu's change went into the run's copy of the config")
	}
	if err := tl.change(func(c *config) { c.Servers = append(c.Servers, "https://two.example") }); err != nil {
		t.Fatal(err)
	}
	if c := configOf(tl); c.T6 != own || !slices.Equal(c.Servers, []string{"https://one.example", "https://two.example"}) {
		t.Errorf("after both changes, the config has %+v", c)
	}
}

// The menu's "Choose Black Ops II folder…" points the launcher at the player's copy while the game runs. The game
// that runs goes on, the room's next game starts from the folder chosen, and so does the next run. The game files
// that the launcher downloaded stay.
func TestMenuChoosesTheFolder(t *testing.T) {
	l := downloading(t, "one")
	l.play.Plutonium = 1 // the revision of the tests' updater: a game that runs could join the next game, but for its folder
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices = []int{1} // download them
	files := filepath.Join(tl.home, "t6")
	g, menu, quit := stayWithTray(t, tl, l.link(), true)
	eventually(t, "the game started from the files downloaded", func() bool { return g.count() == 1 })
	if line := g.line(0); !strings.Contains(line, `t6zm "`+files+`" -lan`) {
		t.Fatalf("the game started with %s", line)
	}
	own := copyOf(t, filepath.Join(t.TempDir(), "Games", titleT6.folder))
	d.mu.Lock()
	d.picks = []string{filepath.Dir(own)}
	d.mu.Unlock()
	item(t, menu, "Choose Black Ops II folder…").do()
	if told := d.lastTold(); !strings.Contains(told, "next time") || !strings.Contains(told, own) {
		t.Errorf("the menu said: %q", told)
	}
	if c := configOf(tl); c.T6 != own {
		t.Errorf("the config has %+v", c)
	}
	if !strings.HasPrefix(item(t, menu, "Black Ops II: ").text, "Black Ops II: your copy, ") || !hasItem(menu, "Delete downloaded Black Ops II files") ||
		read(t, filepath.Join(files, "binkw32.dll")) != "bink" {
		t.Error("the files downloaded went, or the menu doesn't say the folder chosen")
	}
	// the room's next game, which the game that runs would join, were it not for the folder chosen
	l.mu.Lock()
	l.play.Game = &launcherapi.Game{ID: "two", Port: l.port(), Password: "pw", Name: "Ann", Up: true}
	l.mu.Unlock()
	eventually(t, "the game started for the room's next game", func() bool { return g.count() == 2 })
	if line := g.line(1); !strings.Contains(line, `t6zm "`+own+`" -lan`) {
		t.Errorf("the room's next game started with %s", line)
	}
	g.mu.Lock()
	first := g.all[0]
	g.mu.Unlock()
	if typed := first.typed(); len(typed) != 0 || !first.stopped.Load() {
		t.Errorf("the game from the files downloaded was told %q, or not closed", typed)
	}
	// the player closes the game, and joins again: the run finds the game files anew
	g.stays.Store(false)
	g.closeAll()
	time.Sleep(100 * time.Millisecond) // many times what the launcher waits between two looks at the game
	tl.press()
	eventually(t, "the game started again as the player asked", func() bool { return g.count() == 3 })
	if line := g.line(2); !strings.Contains(line, `t6zm "`+own+`" -lan`) {
		t.Errorf("the next run started the game with %s", line)
	}
	if asked, picked, _ := d.seen(); asked != 1 || picked != 1 {
		t.Errorf("asked %d questions and showed %d pickers: %q", asked, picked, d.asked)
	}
	quit()
}

// The menu's "Delete downloaded Black Ops II files…" deletes nothing while the game runs. Once the game is closed,
// it asks, and deletes them; the menu then offers nothing to delete.
func TestMenuDeletesTheDownload(t *testing.T) {
	l := downloading(t, "one")
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices = []int{1}
	files := filepath.Join(tl.home, "t6")
	g, menu, quit := stayWithTray(t, tl, l.link(), true)
	eventually(t, "the game started", func() bool { return g.count() == 1 })
	d.mu.Lock()
	d.confirms = []bool{true, true}
	d.mu.Unlock()
	del := item(t, menu, "Delete downloaded Black Ops II files (")
	del.do()
	if told := d.lastTold(); !strings.HasPrefix(told, "Close Black Ops II first") {
		t.Errorf("with the game running, the menu said %q", told)
	}
	if asked, _, _ := d.seen(); asked != 1 || read(t, filepath.Join(files, "binkw32.dll")) != "bink" {
		t.Fatalf("with the game running, the menu asked %q, or deleted files", d.asked)
	}
	g.stays.Store(false)
	g.closeAll()
	del.do()
	if asked, _, _ := d.seen(); asked != 2 || !strings.Contains(d.asked[1], "Delete the downloaded Black Ops II files") ||
		!strings.Contains(d.asked[1], files) || !strings.Contains(d.asked[1], "will ask where the game is") {
		t.Errorf("with the game closed, the menu asked %q", d.asked)
	}
	if _, err := os.Stat(files); err == nil || !strings.Contains(d.lastTold(), "Deleted") {
		t.Errorf("the files downloaded are still there, or the menu said %q", d.lastTold())
	}
	if hasItem(menu, "Delete downloaded Black Ops II files") || item(t, menu, "Black Ops II: ").text != "Black Ops II: no game files yet" {
		t.Error("the menu still has the files downloaded")
	}
	quit()
}

// Game files that the menu deleted while the launcher waited for the room's game start no game: the launcher tells
// the player's page why.
func TestMenuDeletesWhileTheLauncherWaits(t *testing.T) {
	l := downloading(t, "")
	l.play.Game = nil // the room's game is not up yet
	tl := newTestLauncher(t, "")
	d := answer(tl, true)
	d.choices = []int{1}
	g, menu, quit := stayWithTray(t, tl, l.link(), false)
	eventually(t, "the PC is ready, with the files downloaded", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.ready) > 0 && l.ready[len(l.ready)-1]
	})
	d.mu.Lock()
	d.confirms = []bool{true}
	d.mu.Unlock()
	item(t, menu, "Delete downloaded Black Ops II files (").do()
	if told := d.lastTold(); !strings.Contains(told, "Deleted") {
		t.Fatalf("the menu said %q", told)
	}
	l.mu.Lock()
	l.play.Game = &launcherapi.Game{Port: l.port(), Password: "pw", Name: "Ann"}
	l.mu.Unlock()
	eventually(t, "the player's page heard why no game started", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return slices.ContainsFunc(l.reports, func(r launcherapi.Report) bool { return strings.Contains(r.Failed, "were deleted") })
	})
	if n := g.count(); n != 0 {
		t.Errorf("the launcher started %d games from the files deleted", n)
	}
	quit()
}

// games is what a launcher that stays started: each game's command line. Its games stay open while stays is set, as
// a player's does; else they close as they start.
type games struct {
	mu    sync.Mutex
	lines []string
	all   []*fakeGame
	stays atomic.Bool
}

func (g *games) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.lines)
}

func (g *games) line(i int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lines[i]
}

func (g *games) closeAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, game := range g.all {
		game.open.Store(false)
	}
}

// stayWithTray runs tl as a launcher that stays, with a tray whose menu the test opens with menu. Its games stay open
// as they start when stays is set. quit ends it.
func stayWithTray(t *testing.T, tl *testLauncher, link string, stays bool) (g *games, menu func() []menuItem, quit func()) {
	t.Helper()
	g = &games{}
	g.stays.Store(stays)
	tl.start = func(exe, line, dir string) (game, error) {
		fg := &fakeGame{}
		fg.open.Store(g.stays.Load())
		g.mu.Lock()
		g.lines, g.all = append(g.lines, line), append(g.all, fg)
		g.mu.Unlock()
		return fg, nil
	}
	var mu sync.Mutex
	var shown func() []menuItem
	tl.tray = func(host string, m func() []menuItem) func() {
		mu.Lock()
		shown = m
		mu.Unlock()
		return func() {}
	}
	tl.stays = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tl.run(ctx, []string{link}) }()
	menu = func() []menuItem {
		eventually(t, "the tray's icon is there", func() bool { mu.Lock(); defer mu.Unlock(); return shown != nil })
		mu.Lock()
		defer mu.Unlock()
		return shown()
	}
	return g, menu, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the launcher did not end as it was quit")
		}
	}
}

// item returns the menu's item whose text begins with text.
func item(t *testing.T, menu func() []menuItem, text string) menuItem {
	t.Helper()
	for _, it := range menu() {
		if strings.HasPrefix(it.text, text) {
			return it
		}
	}
	t.Fatalf("the menu has no %q", text)
	return menuItem{}
}

func hasItem(menu func() []menuItem, text string) bool {
	return slices.ContainsFunc(menu(), func(it menuItem) bool { return strings.HasPrefix(it.text, text) })
}
