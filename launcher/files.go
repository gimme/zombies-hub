package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"zombies/internal/launcherapi"
)

// hasT6 reports whether dir is a copy of Black Ops II with its Zombies. The server tests its own copy in the same
// way (server/t6.go: t6Installed).
func hasT6(dir string) bool {
	return dir != "" && isFile(filepath.Join(dir, "zone", "all", "patch_zm.ff")) && isFile(filepath.Join(dir, "binkw32.dll"))
}

// t6Folder is the name of Black Ops II's folder among a Steam library's games.
const t6Folder = "Call of Duty Black Ops II"

// steamLibrary matches a library's line in libraryfolders.vdf. The folder on such a line has a drive or a slash in
// it. An app's line, which has had the app's id and its size since 2021, has neither.
var steamLibrary = regexp.MustCompile(`(?m)^\s*"(?:path|\d+)"\s+"([^"]*[:\\/][^"]*)"`)

// steamLibraries returns the folders that Black Ops II may be in, for a Steam that is installed in steam. They are
// the game's folder in Steam's own library, and in each other library that Steam's list of libraries names. The list
// is steamapps/libraryfolders.vdf. It has a line for each library, with "path" and the library's folder, and with
// the backslashes doubled. Before 2021 the line had a number in place of "path".
func steamLibraries(steam string) []string {
	dirs := []string{filepath.Join(steam, "steamapps", "common", t6Folder)}
	data, err := os.ReadFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return dirs
	}
	for _, m := range steamLibrary.FindAllStringSubmatch(string(data), -1) {
		library := strings.ReplaceAll(m[1], `\\`, `\`)
		dirs = append(dirs, filepath.Join(library, "steamapps", "common", t6Folder))
	}
	return dirs
}

// ownCopy returns the folder of the player's own copy of Black Ops II, or "" when there is none. It is the folder
// that the player named, or else a folder where Steam installed the game.
func (la *launcher) ownCopy() string {
	if la.cfg.T6 == "none" {
		return ""
	}
	if hasT6(la.cfg.T6) {
		return la.cfg.T6
	}
	for _, dir := range la.copies() {
		if hasT6(dir) {
			return dir
		}
	}
	return ""
}

// plainFolder returns dir, the folder where the launcher is to keep the game files. When dir has a letter that isn't
// plain ASCII, as the folder of a Windows user named Zoë has, plainFolder returns a folder at the root of the same
// disk instead. The game reads its files by names in the PC's old code page, and finds none under such a folder.
func plainFolder(dir string) string {
	if !strings.ContainsFunc(dir, func(r rune) bool { return r > 0x7e }) {
		return dir
	}
	return filepath.Join(filepath.VolumeName(dir)+string(filepath.Separator), appName, "t6")
}

// folder returns a folder that a player typed or pasted, without the quotes that Explorer's "Copy as path" puts
// around it.
func folder(answer string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(answer), `"`))
}

// downloads returns the folder where the launcher keeps the game files it downloads: t6 in its own folder, or the
// folder that the player named.
func (la *launcher) downloads() string {
	if la.cfg.T6Files != "" {
		return la.cfg.T6Files
	}
	return plainFolder(filepath.Join(la.home, "t6"))
}

// gameFiles returns the folder of Black Ops II's game files that the player's game runs on. It is the player's own
// copy, as it is, when they have one. Otherwise, when the server sends game files, it is the launcher's folder. The
// launcher downloads that folder from the server and keeps it the same as the server's (sync). It asks the player
// before the first download. In a terminal the player can also type a folder: their own copy's, or another folder
// for the download. A run with no window can only ask yes or no (boxes).
func (la *launcher) gameFiles(ctx context.Context, srv *server, l link, play *launcherapi.Play) (string, error) {
	if own := la.ownCopy(); own != "" {
		la.say("Black Ops II: your own copy, in %s", own)
		if play.Downloads {
			la.lacks(ctx, srv, own)
		}
		return own, nil
	}
	dir := la.downloads()
	first := !isFile(filepath.Join(dir, launcherapi.StateFile))
	if !play.Downloads {
		if !first && hasT6(dir) {
			la.say("Black Ops II: the game files this PC downloaded, in %s", dir)
			return dir, nil
		}
		la.say("The launcher found no Black Ops II on this PC, and %s sends no game files.", l.host)
		if !la.boxes {
			if own := folder(la.ask("If this PC has it, type its folder (the one with t6zm.exe in it) and press Enter: ")); hasT6(own) {
				la.cfg.T6 = own
				return own, la.saveConfig()
			}
		}
		return "", errors.New("Black Ops II has to be on this PC first, with its Zombies: Steam has it. Then join from the room's page again.")
	}
	list, err := srv.files(ctx)
	if err != nil {
		return "", err
	}
	if first {
		la.say("The launcher found no Black Ops II on this PC: its game files come from %s, %s, into", l.host, megabytes(list.Bytes))
		la.say("  %s", dir)
		answer := ""
		if la.boxes {
			if !la.confirm(fmt.Sprintf("The launcher found no Black Ops II on this PC.\n\nDownload its game files from %s (%s) into this folder?\n\n%s",
				l.host, megabytes(list.Bytes), dir)) {
				return "", errors.New("Nothing was downloaded. To play, join from the room's page again and answer Yes, or install Black Ops II with Steam.")
			}
		} else {
			answer = folder(la.ask("Press Enter to download them; or type another folder for them, or your own copy's, first: "))
		}
		switch {
		case answer == "":
		case hasT6(answer) && !isFile(filepath.Join(answer, launcherapi.StateFile)):
			la.cfg.T6 = answer
			return answer, la.saveConfig()
		case !filepath.IsAbs(answer):
			return "", errors.New("That is no folder's full path (as in D:\\Games\\Zombies): nothing was downloaded.")
		default:
			dir, la.cfg.T6Files = answer, answer
			if err := la.saveConfig(); err != nil {
				return "", err
			}
		}
	}
	got, err := la.sync(ctx, srv, dir, list)
	if err != nil {
		return "", err
	}
	if !got {
		la.say("Black Ops II: the game files this PC downloaded, in %s", dir)
	} else if setup := filepath.Join(dir, "redist", "DirectX", "DXSETUP.exe"); first && isFile(setup) {
		la.say("")
		la.say("Should the game say a file is missing as it starts (d3dx9_43.dll, XINPUT1_3.dll, MSVCR100.dll): it needs")
		la.say("DirectX's and Visual C++'s libraries, which these two of the game's own programs install, once:")
		la.say("  %s", setup)
		la.say("  %s", filepath.Join(dir, "redist", "vcredist_x86.exe"))
	}
	return dir, nil
}

// lacks prints which of the server's map files the player's own copy does not have: games on those maps don't load
// on that copy. It checks only the maps' own files (zone/all/), which are the same in every copy. The other files
// differ with a copy's language.
func (la *launcher) lacks(ctx context.Context, srv *server, own string) {
	list, err := srv.files(ctx)
	if err != nil {
		return
	}
	var lacks []string
	for _, f := range list.Files {
		if strings.HasPrefix(f.Path, "zone/all/") && launcherapi.LocalPath(f.Path) && !isFile(filepath.Join(own, filepath.FromSlash(f.Path))) {
			lacks = append(lacks, strings.TrimPrefix(f.Path, "zone/all/"))
		}
	}
	if len(lacks) == 0 {
		return
	}
	more := ""
	if len(lacks) > 6 {
		more = ", and " + plural(len(lacks)-6, "other")
		lacks = lacks[:6]
	}
	la.say("Your copy lacks files the server's has (%s%s): a map of a pack you don't have won't load.", strings.Join(lacks, ", "), more)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}
