package main

import (
	"path/filepath"

	"zombies/internal/launcherapi"
)

// A title is a game that the launcher starts: Black Ops II, which plays on Plutonium, or Black Ops III, which plays on
// BOIII. The server names a title by its id (launcherapi.T6, T7), and so do the launcher's config and its folders. A
// run of the launcher is at one title at a time (launcher.of): the title of its player's room, or the one that the
// player plays alone. What it does for a title is the same for both, but for what the game plays on, and how the
// player's game is started into a room's game (launch.go, boiii.go).
type title struct {
	id      string
	name    string   // as the launcher writes it to the player: "Black Ops II"
	folder  string   // the name of the game's folder among a Steam library's games
	exe     string   // the game's own program in its folder, by which a player knows the folder
	example string   // a folder for the game, in advice to the player
	apps    []string // Steam's ids of the game's apps, which the registry has an install folder for
	// marks are files that a copy of the game with its Zombies has, by their paths in the game's folder, with slashes
	// (has).
	marks []string
}

var (
	titleT6 = &title{id: launcherapi.T6, name: "Black Ops II", folder: "Call of Duty Black Ops II", exe: "t6zm.exe",
		example: `D:\Games\Black Ops II`, apps: []string{"212910", "202970", "202990"},
		marks: []string{"zone/all/patch_zm.ff", "binkw32.dll"}}
	titleT7 = &title{id: launcherapi.T7, name: "Black Ops III", folder: "Call of Duty Black Ops III",
		exe: "BlackOps3.exe", example: `D:\Games\Black Ops III`, apps: []string{"311210"},
		marks: []string{"zone/zm_common.ff", "BlackOps3.exe"}}
	// titles are the titles that the launcher starts, in the order of its menu.
	titles = []*title{titleT6, titleT7}
)

// titleOf returns the title of an id. A server from before the launcher started any other title names none: its
// games are Black Ops II's. titleOf returns nil for an id that the launcher doesn't know.
func titleOf(id string) *title {
	for _, t := range titles {
		if t.id == id || id == "" && t == titleT6 {
			return t
		}
	}
	return nil
}

// has reports whether dir is a copy of the title with its Zombies. The server tests its own copy in the same way
// (server/t6.go: t6Installed, server/t7.go: t7Installed).
func (t *title) has(dir string) bool {
	if dir == "" {
		return false
	}
	for _, mark := range t.marks {
		if !isFile(filepath.Join(dir, filepath.FromSlash(mark))) {
			return false
		}
	}
	return true
}

// title returns the title that the run is at: Black Ops II, until a run says another (setTitle). The tray's menu
// reads it too, on a goroutine of its own.
func (la *launcher) title() *title {
	la.stateLock.Lock()
	defer la.stateLock.Unlock()
	if la.of != nil {
		return la.of
	}
	return titleT6
}

func (la *launcher) setTitle(t *title) {
	la.stateLock.Lock()
	defer la.stateLock.Unlock()
	la.of = t
}

// info returns what the server says of a title in its answer play (launcherapi.Title). A server from before its
// answer had titles says what it says of Black Ops II in the answer itself.
func info(play *launcherapi.Play, t *title) launcherapi.Title {
	if got, ok := play.Titles[t.id]; ok {
		return got
	}
	if t == titleT6 && play.Titles == nil {
		return launcherapi.Title{Downloads: play.Downloads, Zones: play.Zones, Plutonium: play.Plutonium}
	}
	return launcherapi.Title{}
}

// roomTitle returns the title of the room in the server's answer play, or nil when the player is in no room that
// plays a title the launcher starts, or the room plays one that this launcher doesn't know.
func roomTitle(play *launcherapi.Play) *title {
	if play == nil || play.Room == "" {
		return nil
	}
	return titleOf(play.Of)
}
