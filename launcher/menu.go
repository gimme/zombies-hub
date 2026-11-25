package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The launcher's icon in the tray has a menu (serve), which the tray shows when the player clicks the icon. Besides
// opening the server's page and quitting, the menu is where the player sees and changes the game files that the
// launcher uses. A greyed line says which folder the game starts from. "Choose Black Ops II folder…" points the
// launcher at the player's own copy in Windows' folder picker, with the same check as the run's question (pickOwn).
// "Delete downloaded Black Ops II files…" deletes the files that the launcher downloaded, as uninstalling does.
// Nothing of this runs until the player clicks an item, so a PC where the launcher finds the game sees nothing new.
//
// Each line of the group names its game. The launcher starts one game, Black Ops II, and keeps its folders under
// that game's names in the config (config.T6, config.T6Files). A game that the launcher may start one day has its
// own folders there, and its own lines here.
//
// The group is in the menu of a launcher with no window (boxes), which is what a link starts. A launcher in a
// terminal asks there for folders. The menu runs beside the run, on goroutines of its own. It changes the config only
// with rewrite (config.go), and it shares with the run what gameState has, under stateLock.

// menuItem is a line of the tray icon's menu. A line with no text is a separator. A line with no do is greyed: it
// only says something.
type menuItem struct {
	text string
	do   func()
}

// gameState is what the run and the tray's menu know of the game files.
type gameState struct {
	known bool   // the run, or a look at the PC, set dir
	dir   string // the folder that the game starts from, or "" when the launcher has none yet
	own   bool   // dir is the player's own copy, not the game files the launcher downloaded
	// chosen marks a dir that the player chose in the menu, or a dir the menu deleted (""), since the run last found
	// the game files. The game's next start uses it (startFrom).
	chosen bool
	files  string // the folder of the game files that the launcher downloaded, or "" when it has none
	bytes  int64  // their size
	game   game   // the game that the launcher started last
	// getting is set while the run finds the game files, which may take a download. busy is set while one of the
	// menu's items is at work: a second click does nothing then.
	getting, busy bool
}

func (la *launcher) share(f func(s *gameState)) {
	la.stateLock.Lock()
	defer la.stateLock.Unlock()
	f(&la.state)
}

// menu returns what the tray's menu has, for a launcher that stays: open opens the server's page. The tray calls it
// each time the player opens the menu. It works out the menu from what the run and the menu last found, which it
// reads at once, so the menu opens without a wait.
func (la *launcher) menu(host string, open func()) func() []menuItem {
	if la.boxes {
		go la.look()
	}
	return func() []menuItem {
		items := []menuItem{{text: appName + ": " + host}, {}, {text: "Open the page", do: open}, {}}
		if la.boxes {
			var s gameState
			la.share(func(now *gameState) { s = *now })
			items = append(items, menuItem{text: s.line()}, menuItem{text: "Choose Black Ops II folder…", do: la.chooseFolder})
			if s.files != "" {
				items = append(items, menuItem{text: fmt.Sprintf("Delete downloaded Black Ops II files (%s)…", megabytes(s.bytes)),
					do: la.deleteFiles})
			}
			items = append(items, menuItem{})
		}
		return append(items, menuItem{text: "Quit", do: la.quit})
	}
}

// line is the menu's line that says which game files the game starts from.
func (s gameState) line() string {
	switch {
	case s.dir != "" && s.own:
		return "Black Ops II: your copy, " + short(s.dir)
	case s.dir != "":
		return fmt.Sprintf("Black Ops II: downloaded, %s, %s", megabytes(s.bytes), short(s.dir))
	}
	return "Black Ops II: no game files yet"
}

// short returns a folder's path for a line of the menu: whole, or without its middle when it is long.
func short(path string) string {
	r := []rune(path)
	if len(r) <= 60 {
		return path
	}
	return string(r[:20]) + "…" + string(r[len(r)-39:])
}

// look finds the game files as a run does (gameFiles), but changes nothing and asks nothing. It is for the menu to
// show before the run has found them.
func (la *launcher) look() {
	la.cfgLock.Lock()
	c := la.readConfig()
	la.cfgLock.Unlock()
	dir, files := c.ownCopy(la.copies), c.downloads(la.home)
	_, bytes, ok := synced(files)
	if dir == "" && ok && hasT6(files) {
		dir = files
	}
	la.share(func(s *gameState) {
		if !s.known {
			s.known, s.dir, s.own = true, dir, dir != files
			s.files, s.bytes = "", 0
			if ok {
				s.files, s.bytes = files, bytes
			}
		}
	})
}

// finding and found mark the run's search for the game files (gameFiles), for the menu. found records the folder
// that the game starts from, dir, or "" when the run found none, and what the launcher has downloaded. A folder that
// the player chose in the menu meanwhile stays the one the game's next start uses.
func (la *launcher) finding() {
	la.share(func(s *gameState) { s.getting, s.chosen = true, false })
}

func (la *launcher) found(dir string) {
	files := la.downloads()
	_, bytes, ok := synced(files)
	la.share(func(s *gameState) {
		s.getting, s.known = false, true
		if !s.chosen {
			s.dir, s.own = dir, dir != files
		}
		s.files, s.bytes = "", 0
		if ok {
			s.files, s.bytes = files, bytes
		}
	})
}

// startFrom returns the folder that the game starts from: dir, which the run found, or the folder that the player
// chose in the menu since. started records the game that the launcher started (launch).
func (la *launcher) startFrom(dir string) (string, error) {
	la.stateLock.Lock()
	defer la.stateLock.Unlock()
	switch {
	case !la.state.chosen:
		return dir, nil
	case la.state.dir == "":
		return "", errors.New("The downloaded game files were deleted. Try again from the page.")
	}
	return la.state.dir, nil
}

func (la *launcher) started(g game) {
	la.share(func(s *gameState) { s.game = g })
}

// running reports whether the game that the launcher started runs.
func (s gameState) running() bool { return s.game != nil && s.game.running() }

// menuTurn takes the turn of one of the menu's items, which shows dialogs. It returns false while another item has
// it. menuDone gives it up.
func (la *launcher) menuTurn() (ok bool) {
	la.share(func(s *gameState) { ok, s.busy = !s.busy, true })
	return ok
}

func (la *launcher) menuDone() {
	la.share(func(s *gameState) { s.busy = false })
}

// chooseFolder is the menu's "Choose Black Ops II folder…". The player points the launcher at their own copy of the
// game, which the game starts from from its next start. The game files that the launcher downloaded stay: the menu
// deletes them only when the player asks for that (deleteFiles). The menu's line then names the folder, and that is
// all the launcher says of it, unless the game runs: the player then hears that the game that runs is not affected.
func (la *launcher) chooseFolder() {
	if !la.menuTurn() {
		return
	}
	defer la.menuDone()
	own, err := la.pickOwn()
	if err != nil {
		la.notify(sentence(err), true)
		return
	}
	if own == "" {
		return
	}
	if _, err := la.rewrite(func(c *config) { c.T6 = own }); err != nil {
		la.notify(fmt.Sprintf("Couldn't save the folder: %v", err), true)
		return
	}
	running := false
	la.share(func(s *gameState) {
		s.known, s.dir, s.own, s.chosen = true, own, true, true
		running = s.running()
	})
	if running {
		la.notify(fmt.Sprintf("Black Ops II starts from here next time.\n\n%s", own), false)
	}
}

// deleteFiles is the menu's "Delete downloaded Black Ops II files…". It deletes what the launcher downloaded, as
// uninstalling does (synced, unsync), once the player agrees. It deletes nothing while the game runs, or while a
// launcher gets game files.
func (la *launcher) deleteFiles() {
	if !la.menuTurn() {
		return
	}
	defer la.menuDone()
	la.cfgLock.Lock()
	c := la.readConfig()
	la.cfgLock.Unlock()
	files := c.downloads(la.home)
	state, bytes, ok := synced(files)
	if !ok {
		la.share(func(s *gameState) { s.files, s.bytes = "", 0 }) // gone already
		return
	}
	if why := la.notNow(); why != "" {
		la.notify(why, true)
		return
	}
	var now gameState
	la.share(func(s *gameState) { now = *s })
	question := fmt.Sprintf("Delete the downloaded Black Ops II files (%s)?\n\n%s", megabytes(bytes), files)
	if now.dir == files {
		question += "\n\nThe game uses them now. The launcher will ask where the game is next time."
	}
	if !la.confirm(question) {
		return
	}
	// Another launcher on the PC may be getting files into the folder: it has the turn then. The run of this one has
	// the turn while it finds the game files, too. The turn is given up before any message, which may stay open.
	release, ok := tryFilesTurn()
	if !ok {
		la.notify("Another user's launcher on this PC is using the game files. Try again when it's done.", true)
		return
	}
	why := la.notNow() // the run may have started on the game files while the player read the question
	left := 0
	if why == "" {
		left = unsync(files, state)
		if left == 0 && c.T6Files == "" {
			os.Remove(filepath.Dir(files)) // the folder the launcher made for them at a disk's root, if it made one
		}
	}
	release()
	if why != "" {
		la.notify(why, true)
		return
	}
	_, bytesLeft, kept := synced(files)
	la.share(func(s *gameState) {
		s.files, s.bytes = "", 0
		if kept {
			s.files, s.bytes = files, bytesLeft
		}
		if !kept && s.dir == files {
			s.dir, s.chosen = "", true
		}
	})
	if left > 0 {
		la.notify(fmt.Sprintf("Couldn't delete some of the files (is the game open?).\n\n%s", files), true)
		return
	}
	la.notify(fmt.Sprintf("Deleted the downloaded files (%s).", megabytes(bytes)), false)
}

// notNow returns why the menu deletes no game files now, or "" when it may: it deletes none while the game runs, or
// while the run finds the game files.
func (la *launcher) notNow() string {
	var now gameState
	la.share(func(s *gameState) { now = *s })
	switch {
	case now.running():
		return "Close Black Ops II first."
	case now.getting:
		return "The launcher is using the game files. Try again when it's done."
	}
	return ""
}
