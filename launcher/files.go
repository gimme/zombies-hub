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
func (la *launcher) ownCopy() string { return la.cfg.ownCopy(la.copies) }

func (c config) ownCopy(copies func() []string) string {
	if c.T6 == "none" {
		return ""
	}
	if hasT6(c.T6) {
		return c.T6
	}
	for _, dir := range copies() {
		if hasT6(dir) {
			return dir
		}
	}
	return ""
}

// gameIn returns the folder of the player's own copy of Black Ops II in dir, a folder that they chose, or "" when it
// has none. That is dir itself, or the game's folder in it where a Steam library has it: dir may be the folder of
// the library, or of its steamapps, or the folder above the game's. A folder with game files that the launcher
// downloaded is not the player's own copy, as in gameFiles.
func gameIn(dir string) string {
	if dir == "" {
		return ""
	}
	for _, d := range []string{dir, filepath.Join(dir, t6Folder), filepath.Join(dir, "steamapps", "common", t6Folder),
		filepath.Join(dir, "common", t6Folder)} {
		if hasT6(d) && !isFile(filepath.Join(d, syncFile)) {
			return d
		}
	}
	return ""
}

// plain reports whether the game can read its files under dir: whether dir has only plain ASCII letters. The game
// reads its files by names in the PC's old code page, and finds none under a folder with another letter, as the
// folder of a Windows user named Zoë has.
func plain(dir string) bool {
	return !strings.ContainsFunc(dir, func(r rune) bool { return r > 0x7e })
}

// plainFolder returns dir, the folder where the launcher is to keep the game files. When the game can't read files
// under dir (plain), plainFolder returns a folder at the root of the same disk instead.
func plainFolder(dir string) string {
	if plain(dir) {
		return dir
	}
	return filepath.Join(filepath.VolumeName(dir)+string(filepath.Separator), appName, "t6")
}

// notPlain says why the game can't use a folder that the player chose: its path has a letter that the game can't
// read (plain).
const notPlain = "The path has a character other than A to Z (such as é or я), which the game can't read."

// folder returns a folder that a player typed or pasted, without the quotes that Explorer's "Copy as path" puts
// around it.
func folder(answer string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(answer), `"`))
}

// downloads returns the folder where the launcher keeps the game files it downloads: t6 in its own folder, or the
// folder that the player named.
func (la *launcher) downloads() string { return la.cfg.downloads(la.home) }

func (c config) downloads(home string) string {
	if c.T6Files != "" {
		return c.T6Files
	}
	return plainFolder(filepath.Join(home, "t6"))
}

// gameFiles returns the folder of Black Ops II's game files that the player's game runs on. It is the player's own
// copy, as it is, when they have one. Otherwise, when the server sends game files, it is the launcher's folder. The
// launcher downloads that folder from the server and keeps it the same as the server's (sync). When it finds no copy,
// it asks the player before the first download. In a terminal the player can type a folder: their own copy's, or
// another folder for the download. A run with no window asks in a dialog, where the player chooses their copy's
// folder in Windows' folder picker, or has the launcher download the game files (askFiles).
func (la *launcher) gameFiles(ctx context.Context, srv *server, l link, play *launcherapi.Play) (dir string, err error) {
	la.finding()
	defer func() { la.found(dir) }()
	la.loadConfig() // the player may have chosen a folder in the tray's menu since the run began
	agreed := false // the player just agreed to the download, as their own copy lacks the room's map (askMissing)
	if own := la.ownCopy(); own != "" {
		la.say("Black Ops II: your own copy, in %s", own)
		if play.Downloads {
			la.lacks(ctx, srv, own)
		}
		if dir, err := la.askMissing(ctx, srv, l, play, own); err != nil || dir != "" {
			return dir, err
		}
		// until the download is done, the PC lacks what the room's game needs
		agreed, la.prepared = true, false
		srv.setReady(false)
	}
	dir = la.downloads()
	first := !isFile(filepath.Join(dir, launcherapi.StateFile))
	if !play.Downloads {
		if !first && hasT6(dir) {
			la.say("Black Ops II: the game files this PC downloaded, in %s", dir)
			return dir, nil
		}
		la.say("The launcher found no Black Ops II on this PC, and %s sends no game files.", l.host)
		if !la.boxes {
			if own := folder(la.ask("If this PC has it, type its folder (the one with t6zm.exe in it) and press Enter: ")); hasT6(own) {
				return own, la.change(func(c *config) { c.T6 = own })
			}
		} else if own, _, err := la.askFiles(l.host, nil, dir); err != nil || own != "" {
			return own, err
		}
		return "", errors.New("Couldn't find Black Ops II on this PC. Install it from Steam, then join from the room's page again.")
	}
	list, err := srv.files(ctx)
	if err != nil {
		return "", err
	}
	if first && !agreed {
		la.say("The launcher found no Black Ops II on this PC: its game files come from %s, %s, into", l.host, megabytes(list.Bytes))
		la.say("  %s", dir)
		if la.boxes {
			own, to, err := la.askFiles(l.host, list, dir)
			switch {
			case err != nil || own != "":
				return own, err
			case to == "":
				return "", errors.New("Nothing was downloaded.")
			}
			dir = to
		} else {
			switch answer := folder(la.ask("Press Enter to download them; or type another folder for them, or your own copy's, first: ")); {
			case answer == "":
			case hasT6(answer) && !isFile(filepath.Join(answer, launcherapi.StateFile)):
				return answer, la.change(func(c *config) { c.T6 = answer })
			case !filepath.IsAbs(answer):
				return "", errors.New("That isn't a full folder path (like D:\\Games\\Zombies). Nothing was downloaded.")
			default:
				dir = answer
				if err := la.change(func(c *config) { c.T6Files = answer }); err != nil {
					return "", err
				}
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

// hasZone reports whether the game folder dir has a zone: a game file by which the server tells that a copy of the
// game has a map (launcherapi.Play: Zones). A name that is no file's path under the folder counts as one it has, so
// that the launcher asks nothing about it.
func hasZone(dir, zone string) bool {
	return zone == "" || !launcherapi.LocalPath(zone) || isFile(filepath.Join(dir, filepath.FromSlash(zone)))
}

// zonesIn returns those of the server's zones that the game folder dir has, for the server (server.setHas).
func zonesIn(dir string, zones []string) []string {
	var has []string
	for _, zone := range zones {
		if launcherapi.LocalPath(zone) && isFile(filepath.Join(dir, filepath.FromSlash(zone))) {
			has = append(has, zone)
		}
	}
	return has
}

// askMissing asks the player what to do when their own copy of the game lacks the map of their room: a copy without
// the map's pack. The room says that the map is missing, and its button, where the server sends game files, says
// Download and starts a run. askMissing asks only in such a run, one that the player started with the room's button
// (clicked), and that has no window. A run that starts by itself, for the room's next game, asks nothing.
//
// The player has the launcher download the server's game files, which have the map, and use them from then on
// (config.T6 is "none"; the tray's menu chooses their own copy again). askMissing then returns "". Or they choose
// another copy's folder, which it returns. It returns own when the copy has the map, when it does not ask, and when
// the player cancelled.
func (la *launcher) askMissing(ctx context.Context, srv *server, l link, play *launcherapi.Play, own string) (string, error) {
	clicked := la.clicked
	la.clicked = false
	if !clicked || !la.boxes || !play.Downloads || hasZone(own, play.Needs) {
		return own, nil
	}
	list, err := srv.files(ctx)
	if err != nil {
		return "", err
	}
	dir, size := la.downloads(), megabytes(list.Bytes)
	question := fmt.Sprintf("Your Black Ops II doesn't have this map.\n\n%s", own)
	answer := -1
	switch {
	case la.choose != nil:
		answer = la.choose(question, []string{
			fmt.Sprintf("Download\n%s, to %s\nYour own copy isn't touched.", size, dir),
			"Choose another folder…\nA copy that has the map.",
		})
	case la.confirm(fmt.Sprintf("%s\n\nDownload the game files (%s) to this folder?\n\n%s", question, size, dir)):
		answer = 0
	}
	switch answer {
	case 0:
		la.say("Black Ops II: the game files come from %s from now on, %s, into", l.host, size)
		la.say("  %s", dir)
		return "", la.change(func(c *config) { c.T6 = "none" })
	case 1:
		other, err := la.pickOwn()
		if err != nil {
			return "", err
		}
		if other != "" {
			la.say("Black Ops II: your own copy, in %s", other)
			return other, la.change(func(c *config) { c.T6 = other })
		}
	}
	return own, nil
}

// askFiles asks the player what to do, in a run with no window, when the launcher found no copy of Black Ops II on
// the PC. list is the server's game files, or nil when the server sends none. The player chooses the folder of their
// own copy in Windows' folder picker, which askFiles returns as own. Or, when the server sends game files, they have
// the launcher download them, into dir or into a folder that they choose, which askFiles returns as to. It keeps
// either folder in the config. It returns neither when the player cancelled.
//
// The question is a task dialog, whose buttons say what they do (choose). A build without one asks two yes-or-no
// questions instead (confirm), and then downloads only into dir.
func (la *launcher) askFiles(host string, list *launcherapi.Files, dir string) (own, to string, err error) {
	// The dialog says what its buttons don't: that the launcher found no copy. Each button says the rest.
	const question = "Couldn't find Black Ops II on this PC."
	answers := []string{"Choose folder…\nThe one with t6zm.exe in it."}
	yes := "Choose its folder?"
	if list != nil {
		answers = append(answers, fmt.Sprintf("Download\n%s, to %s", megabytes(list.Bytes), dir), "Download to another folder…")
		yes = "Yes: choose its folder.\nNo: download the game files."
	}
	answer := -1
	switch {
	case la.choose != nil:
		answer = la.choose(question, answers)
	case la.confirm(question + "\n\n" + yes):
		answer = 0
	case list != nil && la.confirm(fmt.Sprintf("Download the game files (%s) to this folder?\n\n%s", megabytes(list.Bytes), dir)):
		answer = 1
	}
	switch answer {
	case 0:
		if own, err = la.pickOwn(); own == "" {
			return "", "", err
		}
	case 1:
		return "", dir, nil
	case 2:
		if own, to, err = la.pickDownloads(list.Bytes); err != nil {
			return "", "", err
		}
		if to != "" {
			la.say("The game files go into %s", to)
			return "", to, la.change(func(c *config) { c.T6Files = to })
		}
	}
	if own == "" {
		return "", "", nil
	}
	la.say("Black Ops II: your own copy, in %s", own)
	return own, "", la.change(func(c *config) { c.T6 = own })
}

// pickOwn asks the player for the folder of their own copy of Black Ops II, in Windows' folder picker. It takes the
// game's folder in the folder they chose (gameIn). When there is none, or the game can't start from it, it says so
// and offers to choose again. It returns "" when the player cancelled. The tray's menu asks with it too, so it writes
// nothing to the run's log.
func (la *launcher) pickOwn() (string, error) {
	for {
		dir, err := la.pick("Choose your Black Ops II folder (the one with t6zm.exe in it)")
		if err != nil {
			return "", fmt.Errorf("Windows' folder picker couldn't be opened: %v", err)
		}
		if dir == "" {
			return "", nil
		}
		own := gameIn(dir)
		problem := ""
		switch {
		case own == "":
			problem = fmt.Sprintf("Black Ops II isn't in that folder.\n\n%s\n\nPick the folder with t6zm.exe in it.", dir)
		case !plain(own):
			problem = fmt.Sprintf("Black Ops II can't run from that folder.\n\n%s\n\n%s Move the game somewhere like "+
				"D:\\Games\\Black Ops II.", own, notPlain)
		default:
			return own, nil
		}
		if !la.chooseAgain(problem) {
			return "", nil
		}
	}
}

// pickDownloads asks the player for a folder to download the game files into, of bytes, in Windows' folder picker.
// A folder with their own copy of the game in it needs no download: pickDownloads returns that copy as own.
// Otherwise it returns the folder for the files as to (into). It returns neither when the player cancelled.
func (la *launcher) pickDownloads(bytes int64) (own, to string, err error) {
	for {
		dir, err := la.pick(fmt.Sprintf("Choose where to download Black Ops II (%s)", megabytes(bytes)))
		if err != nil {
			return "", "", fmt.Errorf("Windows' folder picker couldn't be opened: %v", err)
		}
		if dir == "" {
			return "", "", nil
		}
		problem := ""
		switch own := gameIn(dir); {
		case own != "" && plain(own):
			return own, "", nil
		case !filepath.IsAbs(dir):
			problem = "That isn't a full folder path (like D:\\Games\\Zombies)."
		case !plain(dir):
			problem = fmt.Sprintf("The game can't run from that folder.\n\n%s\n\n%s", dir, notPlain)
		default:
			return "", into(dir), nil
		}
		if !la.chooseAgain(problem) {
			return "", "", nil
		}
	}
}

// into returns the folder for the game files when the player chose dir for them: dir itself when it is empty, or
// when the launcher downloaded into it before. Otherwise it is a new folder in dir. The game's files, 9 GB of them,
// then don't mix with what dir has, as a disk's root has.
func into(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 || isFile(filepath.Join(dir, syncFile)) {
		return dir
	}
	return filepath.Join(dir, t6Folder)
}

// chooseAgain tells the player what is wrong with the folder they chose, and returns whether they choose another.
func (la *launcher) chooseAgain(problem string) bool {
	if la.choose != nil {
		return la.choose(problem, []string{"Choose another folder…"}) == 0
	}
	return la.confirm(problem + "\n\nChoose another folder?")
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
