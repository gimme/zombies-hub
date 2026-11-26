package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"zombies/internal/launcherapi"
)

// hasT6 reports whether dir is a copy of Black Ops II with its Zombies (title.has).
func hasT6(dir string) bool { return titleT6.has(dir) }

// steamLibrary matches a library's line in libraryfolders.vdf. The folder on such a line has a drive or a slash in
// it. An app's line, which has had the app's id and its size since 2021, has neither.
var steamLibrary = regexp.MustCompile(`(?m)^\s*"(?:path|\d+)"\s+"([^"]*[:\\/][^"]*)"`)

// steamLibraries returns the folders that a game may be in, for a Steam that is installed in steam: folder is the
// name of the game's folder among a library's games (title.folder). They are the game's folder in Steam's own
// library, and in each other library that Steam's list of libraries names. The list is steamapps/libraryfolders.vdf.
// It has a line for each library, with "path" and the library's folder, and with the backslashes doubled. Before 2021
// the line had a number in place of "path".
func steamLibraries(steam, folder string) []string {
	dirs := []string{filepath.Join(steam, "steamapps", "common", folder)}
	data, err := os.ReadFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return dirs
	}
	for _, m := range steamLibrary.FindAllStringSubmatch(string(data), -1) {
		library := strings.ReplaceAll(m[1], `\\`, `\`)
		dirs = append(dirs, filepath.Join(library, "steamapps", "common", folder))
	}
	return dirs
}

// ownCopy returns the folder of the player's own copy of the run's title, or "" when there is none. It is the folder
// that the player named, or else a folder where Steam installed the game.
func (la *launcher) ownCopy() string { return la.cfg.ownCopy(la.title(), la.copies) }

func (c config) ownCopy(t *title, copies func(*title) []string) string {
	named := *c.own(t)
	if named == "none" {
		return ""
	}
	if t.has(named) {
		return named
	}
	for _, dir := range copies(t) {
		if t.has(dir) {
			return dir
		}
	}
	return ""
}

// gameIn returns the folder of the player's own copy of the title t in dir, a folder that they chose, or "" when it
// has none. That is dir itself, or the game's folder in it where a Steam library has it: dir may be the folder of
// the library, or of its steamapps, or the folder above the game's. A folder with game files that the launcher
// downloaded is not the player's own copy, as in gameFiles.
func gameIn(t *title, dir string) string {
	if dir == "" {
		return ""
	}
	for _, d := range []string{dir, filepath.Join(dir, t.folder), filepath.Join(dir, "steamapps", "common", t.folder),
		filepath.Join(dir, "common", t.folder)} {
		if t.has(d) && !isFile(filepath.Join(d, syncFile)) {
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

// plainFolder returns dir, the folder where the launcher is to keep the game files of the title t. When the game
// can't read files under dir (plain), plainFolder returns a folder at the root of the same disk instead.
func plainFolder(t *title, dir string) string {
	if plain(dir) {
		return dir
	}
	return filepath.Join(filepath.VolumeName(dir)+string(filepath.Separator), appName, t.id)
}

// notPlain says why the game can't use a folder that the player chose: its path has a letter that the game can't
// read (plain).
const notPlain = "The path has a character other than A to Z (such as é or я), which the game can't read."

// folder returns a folder that a player typed or pasted, without the quotes that Explorer's "Copy as path" puts
// around it.
func folder(answer string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(answer), `"`))
}

// downloads returns the folder where the launcher keeps the game files it downloads of the run's title: the folder
// named after the title in its own folder (t6, t7), or the folder that the player named.
func (la *launcher) downloads() string { return la.cfg.downloads(la.title(), la.home) }

func (c config) downloads(t *title, home string) string {
	if named := *c.files(t); named != "" {
		return named
	}
	return plainFolder(t, filepath.Join(home, t.id))
}

// need is what the game is to have of the title's maps for what the player plays now: the map of their room, or the
// one that they picked to play alone. zone is the map's zone among the title's (launcherapi.Title), and pack the part
// of the server's game files that only that map needs (launcherapi.File), or "" where the server sends the title's
// files in one. Either may be "": a run knows the zone of a room's map, and the pack of a map that the player picked,
// and the server's list of its game files gives the other (resolve).
type need struct{ zone, pack string }

// resolve completes n from the server's list of the title's game files, of which zones are the title's maps' zones:
// a zone's pack is its file's in the list, and a pack's zone is the title's zone that is of the pack.
func (n need) resolve(list *launcherapi.Files, zones []string) need {
	for _, f := range list.Files {
		switch {
		case n.zone != "" && f.Path == n.zone:
			n.pack = f.Pack
		case n.zone == "" && n.pack != "" && f.Pack == n.pack && slices.Contains(zones, f.Path):
			n.zone = f.Path
		}
	}
	return n
}

// packsFor returns the packs of the server's list that the folder dir is to have, and their size: the pack that every
// map needs (""), the pack of each map that the folder has already (the maps whose zones it has, of zones), which a
// sync keeps the same as the server's, and the pack that the player needs now. A list in one pack is wanted whole.
func packsFor(list *launcherapi.Files, zones []string, dir string, n need) (packs map[string]bool, bytes int64) {
	packs = map[string]bool{"": true, n.pack: true}
	for _, f := range list.Files {
		if f.Pack != "" && slices.Contains(zones, f.Path) && hasZone(dir, f.Path) {
			packs[f.Pack] = true
		}
	}
	for _, f := range list.Files {
		if packs[f.Pack] {
			bytes += f.Size
		}
	}
	return packs, bytes
}

// gameFiles returns the folder of the game files of the run's title that the player's game runs on. It is the
// player's own copy, as it is, when they have one. Otherwise, when the server sends the title's game files, it is the
// launcher's folder. The launcher downloads that folder from the server and keeps it the same as the server's (sync):
// all of a title that the server sends in one, and of one that it sends in packs, what every map needs, with the maps
// that the folder has and the one that the player needs now (n). When it finds no copy, it asks the player before
// the first download. In a terminal the player can type a folder: their own copy's, or another folder for the
// download. A run with no window asks in a dialog, where the player chooses their copy's folder in Windows' folder
// picker, or has the launcher download the game files (askFiles).
func (la *launcher) gameFiles(ctx context.Context, srv *server, l link, play *launcherapi.Play, n need) (dir string, err error) {
	t := la.title()
	says := info(play, t)
	la.finding()
	defer func() { la.found(dir) }()
	la.loadConfig() // the player may have chosen a folder in the tray's menu since the run began
	agreed := false // the player just agreed to the download, as their own copy lacks the map (askMissing)
	if own := la.ownCopy(); own != "" {
		la.say("%s: your own copy, in %s", t.name, own)
		if says.Downloads {
			la.lacks(ctx, srv, own, says.Zones)
		}
		if dir, err := la.askMissing(ctx, srv, l, says, own, n); err != nil || dir != "" {
			return dir, err
		}
		// until the download is done, the PC lacks what the game needs
		agreed, la.prepared = true, false
		srv.setReady(false)
	}
	dir = la.downloads()
	first := !isFile(filepath.Join(dir, launcherapi.StateFile))
	if !says.Downloads {
		if !first && t.has(dir) {
			la.say("%s: the game files this PC downloaded, in %s", t.name, dir)
			return dir, nil
		}
		la.say("The launcher found no %s on this PC, and %s sends no game files.", t.name, l.host)
		if !la.boxes {
			if own := folder(la.ask(fmt.Sprintf("If this PC has it, type its folder (the one with %s in it) and press Enter: ", t.exe))); t.has(own) {
				return own, la.change(func(c *config) { *c.own(t) = own })
			}
		} else if own, _, err := la.askFiles(l.host, 0, false, dir); err != nil || own != "" {
			return own, err
		}
		return "", fmt.Errorf("Couldn't find %s on this PC. Install it from Steam, then try again.", t.name)
	}
	list, err := srv.files(ctx, t)
	if err != nil {
		return "", err
	}
	n = n.resolve(list, says.Zones)
	_, bytes := packsFor(list, says.Zones, dir, n)
	if first && !agreed {
		la.say("The launcher found no %s on this PC: its game files come from %s, %s, into", t.name, l.host, megabytes(bytes))
		la.say("  %s", dir)
		if la.boxes {
			own, to, err := la.askFiles(l.host, bytes, true, dir)
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
			case t.has(answer) && !isFile(filepath.Join(answer, launcherapi.StateFile)):
				return answer, la.change(func(c *config) { *c.own(t) = answer })
			case !filepath.IsAbs(answer):
				return "", errors.New("That isn't a full folder path (like D:\\Games\\Zombies). Nothing was downloaded.")
			default:
				dir = answer
				if err := la.change(func(c *config) { *c.files(t) = answer }); err != nil {
					return "", err
				}
			}
		}
	}
	packs, _ := packsFor(list, says.Zones, dir, n)
	got, err := la.sync(ctx, srv, dir, list, packs, says.Zones)
	if err != nil {
		return "", err
	}
	if !got {
		la.say("%s: the game files this PC downloaded, in %s", t.name, dir)
	} else if setup := filepath.Join(dir, "redist", "DirectX", "DXSETUP.exe"); first && t == titleT6 && isFile(setup) {
		la.say("")
		la.say("Should the game say a file is missing as it starts (d3dx9_43.dll, XINPUT1_3.dll, MSVCR100.dll): it needs")
		la.say("DirectX's and Visual C++'s libraries, which these two of the game's own programs install, once:")
		la.say("  %s", setup)
		la.say("  %s", filepath.Join(dir, "redist", "vcredist_x86.exe"))
	}
	return dir, nil
}

// hasZone reports whether the game folder dir has a zone: a game file by which the server tells that a copy of the
// game has a map (launcherapi.Title: Zones). A name that is no file's path under the folder counts as one it has, so
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

// askMissing asks the player what to do when their own copy of the game lacks the map that they need now (n): a copy
// without the map's pack. A room says that the map is missing, and its button, where the server sends game files,
// says Download and starts a run. askMissing asks only in such a run, one that the player started with a button of
// the page (clicked), and that has no window. A run that starts by itself, for the room's next game, asks nothing.
//
// The player has the launcher download the server's game files, which have the map, and use them from then on (the
// config's folder of their own copy is "none"; the tray's menu chooses their own copy again). askMissing then returns
// "". Or they choose another copy's folder, which it returns. It returns own when the copy has the map, when it does
// not ask, and when the player cancelled.
func (la *launcher) askMissing(ctx context.Context, srv *server, l link, says launcherapi.Title, own string, n need) (string, error) {
	t := la.title()
	clicked := la.clicked
	la.clicked = false
	if !clicked || !la.boxes || !says.Downloads || n.zone != "" && hasZone(own, n.zone) || n.zone == "" && n.pack == "" {
		return own, nil
	}
	list, err := srv.files(ctx, t)
	if err != nil {
		return "", err
	}
	if n = n.resolve(list, says.Zones); hasZone(own, n.zone) {
		return own, nil
	}
	dir := la.downloads()
	_, bytes := packsFor(list, says.Zones, dir, n)
	size := megabytes(bytes)
	question := fmt.Sprintf("Your %s doesn't have this map.\n\n%s", t.name, own)
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
		la.say("%s: the game files come from %s from now on, %s, into", t.name, l.host, size)
		la.say("  %s", dir)
		return "", la.change(func(c *config) { *c.own(t) = "none" })
	case 1:
		other, err := la.pickOwn(t)
		if err != nil {
			return "", err
		}
		if other != "" {
			la.say("%s: your own copy, in %s", t.name, other)
			return other, la.change(func(c *config) { *c.own(t) = other })
		}
	}
	return own, nil
}

// askFiles asks the player what to do, in a run with no window, when the launcher found no copy of the run's title on
// the PC. sends says whether the server sends the title's game files, and bytes is then the size of the download.
// The player chooses the folder of their own copy in Windows' folder picker, which askFiles returns as own. Or, when
// the server sends game files, they have the launcher download them, into dir or into a folder that they choose,
// which askFiles returns as to. It keeps either folder in the config. It returns neither when the player cancelled.
//
// The question is a task dialog, whose buttons say what they do (choose). A build without one asks two yes-or-no
// questions instead (confirm), and then downloads only into dir.
func (la *launcher) askFiles(host string, bytes int64, sends bool, dir string) (own, to string, err error) {
	t := la.title()
	// The dialog says what its buttons don't: that the launcher found no copy. Each button says the rest.
	question := fmt.Sprintf("Couldn't find %s on this PC.", t.name)
	answers := []string{fmt.Sprintf("Choose folder…\nThe one with %s in it.", t.exe)}
	yes := "Choose its folder?"
	if sends {
		answers = append(answers, fmt.Sprintf("Download\n%s, to %s", megabytes(bytes), dir), "Download to another folder…")
		yes = "Yes: choose its folder.\nNo: download the game files."
	}
	answer := -1
	switch {
	case la.choose != nil:
		answer = la.choose(question, answers)
	case la.confirm(question + "\n\n" + yes):
		answer = 0
	case sends && la.confirm(fmt.Sprintf("Download the game files (%s) to this folder?\n\n%s", megabytes(bytes), dir)):
		answer = 1
	}
	switch answer {
	case 0:
		if own, err = la.pickOwn(t); own == "" {
			return "", "", err
		}
	case 1:
		return "", dir, nil
	case 2:
		if own, to, err = la.pickDownloads(t, bytes); err != nil {
			return "", "", err
		}
		if to != "" {
			la.say("The game files go into %s", to)
			return "", to, la.change(func(c *config) { *c.files(t) = to })
		}
	}
	if own == "" {
		return "", "", nil
	}
	la.say("%s: your own copy, in %s", t.name, own)
	return own, "", la.change(func(c *config) { *c.own(t) = own })
}

// pickOwn asks the player for the folder of their own copy of the title t, in Windows' folder picker. It takes the
// game's folder in the folder they chose (gameIn). When there is none, or the game can't start from it, it says so
// and offers to choose again. It returns "" when the player cancelled. The tray's menu asks with it too, so it writes
// nothing to the run's log.
func (la *launcher) pickOwn(t *title) (string, error) {
	for {
		dir, err := la.pick(fmt.Sprintf("Choose your %s folder (the one with %s in it)", t.name, t.exe))
		if err != nil {
			return "", fmt.Errorf("Windows' folder picker couldn't be opened: %v", err)
		}
		if dir == "" {
			return "", nil
		}
		own := gameIn(t, dir)
		problem := ""
		switch {
		case own == "":
			problem = fmt.Sprintf("%s isn't in that folder.\n\n%s\n\nPick the folder with %s in it.", t.name, dir, t.exe)
		case !plain(own):
			problem = fmt.Sprintf("%s can't run from that folder.\n\n%s\n\n%s Move the game somewhere like "+
				"%s.", t.name, own, notPlain, t.example)
		default:
			return own, nil
		}
		if !la.chooseAgain(problem) {
			return "", nil
		}
	}
}

// pickDownloads asks the player for a folder to download the game files of the title t into, of bytes, in Windows'
// folder picker. A folder with their own copy of the game in it needs no download: pickDownloads returns that copy as
// own. Otherwise it returns the folder for the files as to (into). It returns neither when the player cancelled.
func (la *launcher) pickDownloads(t *title, bytes int64) (own, to string, err error) {
	for {
		dir, err := la.pick(fmt.Sprintf("Choose where to download %s (%s)", t.name, megabytes(bytes)))
		if err != nil {
			return "", "", fmt.Errorf("Windows' folder picker couldn't be opened: %v", err)
		}
		if dir == "" {
			return "", "", nil
		}
		problem := ""
		switch own := gameIn(t, dir); {
		case own != "" && plain(own):
			return own, "", nil
		case !filepath.IsAbs(dir):
			problem = "That isn't a full folder path (like D:\\Games\\Zombies)."
		case !plain(dir):
			problem = fmt.Sprintf("The game can't run from that folder.\n\n%s\n\n%s", dir, notPlain)
		default:
			return "", into(t, dir), nil
		}
		if !la.chooseAgain(problem) {
			return "", "", nil
		}
	}
}

// into returns the folder for the game files of the title t when the player chose dir for them: dir itself when it is
// empty, or when the launcher downloaded into it before. Otherwise it is a new folder in dir. The game's files, 9 GB
// of them and more, then don't mix with what dir has, as a disk's root has.
func into(t *title, dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 || isFile(filepath.Join(dir, syncFile)) {
		return dir
	}
	return filepath.Join(dir, t.folder)
}

// chooseAgain tells the player what is wrong with the folder they chose, and returns whether they choose another.
func (la *launcher) chooseAgain(problem string) bool {
	if la.choose != nil {
		return la.choose(problem, []string{"Choose another folder…"}) == 0
	}
	return la.confirm(problem + "\n\nChoose another folder?")
}

// lacks prints which of the server's map files the player's own copy does not have: games on those maps don't load
// on that copy. It checks only the maps' own files, which are the same in every copy: the files of Black Ops II's
// folder of them (zone/all/), and the zones of another title's maps. The other files differ with a copy's language.
func (la *launcher) lacks(ctx context.Context, srv *server, own string, zones []string) {
	t := la.title()
	list, err := srv.files(ctx, t)
	if err != nil {
		return
	}
	var lacks []string
	for _, f := range list.Files {
		of := t == titleT6 && strings.HasPrefix(f.Path, "zone/all/") || t != titleT6 && slices.Contains(zones, f.Path)
		if of && launcherapi.LocalPath(f.Path) && !isFile(filepath.Join(own, filepath.FromSlash(f.Path))) {
			lacks = append(lacks, filepath.Base(f.Path))
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
