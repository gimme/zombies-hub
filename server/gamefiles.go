package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// What the server takes of each folder of the game files (-game-dir). A folder is a game's install folder, which may
// have more than Zombies: the game's other modes, which Steam installs into the same folder. The server takes only
// what is named here, so what else a folder has changes nothing: it is not copied (docker/host.sh: game_mounts, on
// Windows), and not sent to a player (gameSends).
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

// gameSends has, for each title whose game files the server sends to a launcher (launcher.go), what it sends of the
// title's folder: the files that a player's game needs to play Zombies. sends reports whether it sends the file at rel,
// the file's path in the folder, with slashes. pack then names the part of the download that the file is of
// (launcherapi.File): a map, by its name, for a file that only that map needs, or "" for one that every map needs. A
// launcher downloads the packs of the maps that its player plays.
//
//   - t6, Black Ops II: what the server takes of it, all in one pack. Its maps share most of their files.
//   - t7, Black Ops III: Zombies' files, a pack for each map that the game does not come with (t7Sends). That is far
//     more than its server reads: the pictures and the sounds, which a server has no use for.
var gameSends = map[string]func(rel string) (pack string, sends bool){
	t6: func(rel string) (string, bool) { return "", t6Takes(rel) },
	t7: t7Sends,
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

// What marks a file of Black Ops III as one that a player's game needs for Zombies. The game's three modes are one
// program with one install folder, and a mode's files are named for it, as its maps' are for them:
//
//   - the install folder itself: the game's program and the two libraries it loads (t7Programs), and its loose text and
//     pictures. Nothing else there is sent: BOIII's own files are there too, with a player's settings (boiii_players),
//     and a launcher takes no program from a server but the game's own (launcher/programs.go).
//   - zone: the fastfiles and image packs of the families that every mode loads (core_, base, initial) and of
//     Zombies (zm_), in each language. A language's files have its two letters first (en_zm_factory.ff).
//   - zone/snd/<language>: the sound banks of the same two kinds (core_, zm_).
//   - video: Zombies' videos (zm_), and the ones of every mode (bo3_, core_).
//
// A file of a map is named after the map: zm_factory.ff, zm_factory_patch.ff, en_zm_factory.xpak,
// zm_factory.all.sabs, zm_factory_load_factoryloadingmovie.mkv. It is of the map's pack. What every map loads is not
// named after one (zm_common, zm_levelcommon, zm_patch), and is of the pack that every map needs. So are the files of
// a map that the game itself comes with (t7OwnMaps): a PC that downloads the game then has a map to play, before its
// player gets another.
var (
	t7Programs = map[string]bool{"blackops3.exe": true, "steam_api64.dll": true, "d3dcompiler_46.dll": true}
	t7Language = regexp.MustCompile(`^[a-z]{2}_`)
	t7Family   = regexp.MustCompile(`^(core_|base|initial|zm_)`)
	t7MapOf    = regexp.MustCompile(`^zm_[a-z0-9]+`)
)

// t7OwnMaps are the maps that Black Ops III itself comes with: Shadows of Evil. Each other map is of a map pack that
// is bought apart from the game.
var t7OwnMaps = []string{"zm_zod"}

// t7Sends reports whether a player's game needs the file at rel of a Black Ops III install to play Zombies, and the
// pack it is of: its map's, or "" (gameSends), as it is for a map that the game comes with (t7OwnMaps).
func t7Sends(rel string) (pack string, sends bool) {
	rel = strings.ToLower(rel)
	dir, name := path.Split(rel)
	ext := path.Ext(name)
	switch {
	case dir == "":
		return "", t7Programs[name] || ext == ".txt" || ext == ".bmp"
	case dir == "zone/":
		if ext != ".ff" && ext != ".fd" && ext != ".xpak" {
			return "", false
		}
		// zm_, mp_ and cp_ are a mode's first letters, and no language's
		if lang := name[:min(len(name), 3)]; t7Language.MatchString(name) && lang != "zm_" && lang != "mp_" && lang != "cp_" {
			name = name[3:]
		}
		sends = t7Family.MatchString(name)
	case path.Dir(path.Dir(dir)) == "zone/snd" && (ext == ".sabs" || ext == ".sabl"):
		sends = strings.HasPrefix(name, "core_") || strings.HasPrefix(name, "zm_")
	case dir == "video/" && ext == ".mkv":
		sends = strings.HasPrefix(name, "zm_") || strings.HasPrefix(name, "bo3_") || strings.HasPrefix(name, "core_")
	}
	if !sends {
		return "", false
	}
	of := t7MapOf.FindString(name)
	if slices.ContainsFunc(t7MapList, func(m mapEntry) bool { return m.name == of }) && !slices.Contains(t7OwnMaps, of) {
		pack = of
	}
	return pack, true
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
