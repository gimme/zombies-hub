package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// What the server takes of each folder of the game files (-game-dir). A folder is a game's install folder, which may
// have more than Zombies: the game's other modes, which Steam installs into the same folder. The server takes only
// what is named here, so what else a folder has changes nothing: it is not copied (docker/host.sh: game_mounts, on
// Windows), and not sent to a player (launcher.go: t6Files).
//
// The rules go by a file's name and place, not by a list of files, so that a file an update of a game adds is taken
// when it is named as its kind is.
//
//   - t5, Black Ops: everything. What a browser gets of it is listed for each map (files.go).
//   - t6, Black Ops II: Zombies' files (t6Takes).
//   - t7, Black Ops III: the fastfiles of Zombies, which are all a dedicated server reads of the game (t7Reads).
//   - t7-server, Steam's dedicated server of Black Ops III: the server, without its multiplayer maps (t7ServerTakes).
var gameTakes = map[string]func(rel string) bool{
	t5:       func(string) bool { return true },
	t6:       t6Takes,
	t7:       func(rel string) bool { return path.Dir(rel) == "zone" && t7Reads(path.Base(rel), false) },
	t7Server: t7ServerTakes,
}

// What marks a file of Black Ops II as Zombies', by its folder. The game's three modes are three programs with one
// install folder, and each mode's files are named for it:
//
//   - zone/<language>: the fastfiles and image packs with zm in their names (zm_transit.ff, common_zm.ff, zm.ipak,
//     dlczm1.ipak, en_patch_zm.ff), and the ones that every mode loads (base.ipak, weapons.ff).
//   - sound: Zombies' sound banks (zmb_), and the ones that every mode loads (cmn_, wpn_).
//   - video: Zombies' videos (zm_), and the ones that every mode plays (default, treyarch).
var (
	t6TakesZone  = regexp.MustCompile(`(^|_)zm[_.]|^dlczm|^([a-z]{2}_)?base\.|^weapons\.`)
	t6TakesSound = regexp.MustCompile(`^(zmb|cmn|wpn)_`)
	t6TakesVideo = regexp.MustCompile(`^(zm_|default\.|treyarch\.)`)
	// t6OtherMode matches a file in the install folder itself that is another mode's: its program, and its text.
	t6OtherMode = regexp.MustCompile(`^t6(mp|sp)\.|_(mp|sp)\.`)
)

// t6Takes reports whether the server takes the file at rel of a Black Ops II install: one of Zombies (above), one in
// the install folder itself that is not another mode's, or one of redist, which is what Steam installs for every
// mode. rel is the file's path in the folder, with slashes.
func t6Takes(rel string) bool {
	rel = strings.ToLower(rel)
	dir, name := path.Split(rel)
	switch top, _, _ := strings.Cut(dir, "/"); top {
	case "":
		return !t6OtherMode.MatchString(name)
	case "zone":
		return t6TakesZone.MatchString(name)
	case "sound":
		return t6TakesSound.MatchString(name)
	case "video":
		return t6TakesVideo.MatchString(name)
	case "redist":
		return true
	}
	return false
}

// t7ServerTakes reports whether the server takes the file at rel of the install folder of Steam's dedicated server of
// Black Ops III: one of the server (UnrankedServer), but for the fastfiles of its multiplayer maps. The folder may be
// the game's own, where Steam put the server there, so nothing else in it is taken.
func t7ServerTakes(rel string) bool {
	in, ok := strings.CutPrefix(rel, "UnrankedServer/")
	if !ok {
		return false
	}
	zone, ok := strings.CutPrefix(in, "zone/")
	return !ok || !strings.Contains(zone, "mp_")
}

// gameFilesCommand is zombies-server game-files <folder> <dir>. It prints the files that the server takes of dir, the
// folder <folder> of the game files (gameTakes): each file's path in dir, after "./" and before a NUL. A script that
// copies the game files copies those (docker/host.sh), so that the rules are here alone.
func gameFilesCommand(args []string) int {
	if len(args) != 2 || gameTakes[args[0]] == nil {
		fmt.Fprintln(os.Stderr, "usage: zombies-server game-files t5|t6|t7|t7-server <dir>")
		return 2
	}
	takes, dir := gameTakes[args[0]], args[1]
	out := bufio.NewWriter(os.Stdout)
	err := filepath.WalkDir(dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); takes(rel) {
			fmt.Fprintf(out, "./%s\x00", rel)
		}
		return nil
	})
	if err == nil {
		err = out.Flush()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "game-files: %v\n", err)
		return 1
	}
	return 0
}
