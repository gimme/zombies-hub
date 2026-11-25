package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
	"zombies/internal/release"
)

// exeName is the name of the launcher's file in its folder. A release has the same program under another name, which
// describes what a player does with it (launcherapi.Setup).
const exeName = "zombies-launcher.exe"

// appName is the launcher's name on a player's PC: in Windows' list of installed apps, in a browser's question
// whether a link may open it, and as the name of its folder.
const appName = "Zombies Launcher"

// updatedEnv is the environment variable that is set for a launcher that replaced another in the same run (update).
// Such a launcher does not replace itself with another build.
const updatedEnv = "ZOMBIES_LAUNCHER_UPDATED"

// installed returns the path of the installed launcher, which is what links run.
func (la *launcher) installed() string { return filepath.Join(la.home, exeName) }

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// place moves the file from to the installed launcher's path. Windows lets no one write to a running program or
// remove it, but the program can be renamed. The installed program is renamed to .old, and the next run removes it.
func (la *launcher) place(from string) error {
	to := la.installed()
	os.Remove(to + ".old")
	if isFile(to) {
		if err := plutonium.Rename(to, to+".old"); err != nil {
			return err
		}
	}
	if err := plutonium.Rename(from, to); err != nil {
		os.Rename(to+".old", to)
		return err
	}
	return nil
}

// install copies the launcher to its folder. It makes Windows start the launcher for links of its scheme, and adds
// it to the PC's installed apps, where the player uninstalls it. It shows nothing until it is done.
func (la *launcher) install() error {
	if la.exe == "" {
		return errors.New("The launcher couldn't find its own file, so it can't install itself.")
	}
	to := la.installed()
	if !sameFile(la.exe, to) {
		data, err := os.ReadFile(la.exe)
		if err == nil {
			err = writeFile(to+".new", data)
		}
		if err == nil {
			err = la.place(to + ".new")
		}
		if err != nil {
			return fmt.Errorf("The launcher couldn't be copied to its folder, %s: %v", la.home, err)
		}
	}
	if err := la.register(to); err != nil {
		return fmt.Errorf("Windows wouldn't register the launcher's links: %v", err)
	}
	la.notify(appName+" successfully installed.", false)
	return nil
}

// uninstall removes the launcher from the PC. Windows runs it from its list of installed apps (register). It removes
// the launcher's links, its entry in that list, and its folder with what the launcher kept there. It deletes the
// game files that the launcher downloaded when the player agrees: each file it downloaded, and nothing else in their
// folder. Plutonium stays. It is in Plutonium's own folder, and the player's other games on Plutonium run from that
// folder too. uninstall asks that one question, when there are downloaded game files, and shows nothing more unless
// something is left to report.
func (la *launcher) uninstall() error {
	if launchers() {
		return errors.New(appName + " is running. Quit it from the tray, close the game, then uninstall again.")
	}
	la.loadConfig()
	files := la.downloads()
	state, bytes, downloaded := synced(files)
	wipe := downloaded && la.confirm(fmt.Sprintf("Also delete the downloaded Black Ops II files (%s)?\n\n%s", megabytes(bytes), files))
	if err := la.unregister(); err != nil {
		return fmt.Errorf("Windows wouldn't remove the launcher's entries: %v", err)
	}
	left := false
	if wipe {
		if left = unsync(files, state) > 0; !left && la.cfg.T6Files == "" {
			os.Remove(filepath.Dir(files)) // the folder the launcher made for them at a disk's root, if it made one
		}
	}
	// Empty the launcher's own folder: first what it kept there, then the program, then the folder if nothing else
	// is in it.
	exe := la.installed()
	for _, file := range []string{la.configFile(), la.configFile() + ".tmp", filepath.Join(la.home, "launcher.log"),
		exe + ".old", exe + ".new", exe + ".new.tmp"} {
		os.Remove(file)
	}
	if err := os.Remove(exe); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// Windows deletes no running program. This program is deleted once it has ended.
		if !sameFile(la.exe, exe) {
			return fmt.Errorf("The launcher's program couldn't be deleted, %s: %v", exe, err)
		}
		la.atExit = func() { la.later(exe, la.home) }
	} else {
		os.Remove(la.home)
	}
	la.say("%s is uninstalled.", appName)
	if left {
		la.notify(fmt.Sprintf("%s is uninstalled, but couldn't delete some of the game files (is the game open?).\n\n%s",
			appName, files), false)
	}
	return nil
}

// update replaces this launcher with its latest release, without asking the player, when that is a later build of
// this launcher's maker. The releases are on GitHub (launcherapi.Releases), and each has the maker's signed statement
// of its build (internal/release). update first downloads the latest release's statement, from the release that
// always has it (launcherapi.Current), and checks its signature with the public half of the maker's key, which is
// built into the launcher. It goes on only for a statement of a later version than its own. It then downloads the
// build from the release of that version, by the version's tag, so that a release published between the two
// downloads can't put another build in its place. It takes the build only when the build's hash is the one the
// statement names.
//
// Neither whoever serves the download, nor anyone between GitHub and the PC, nor a server that the launcher plays on
// can make a statement. So none of them can put a program of their own in the launcher's place. Nor can they put an
// earlier build of the maker's there, which may have a fault that was fixed since. When anything is wrong, or GitHub
// can't be reached in time, the launcher keeps the build it is and plays on with it.
//
// update acts only for the installed launcher, which links run, and once a run: a launcher that an update started
// (updatedEnv) does not update again. A build without its maker's key, or with version 0 (a dev build,
// docker/launcher.sh), never replaces itself and asks GitHub nothing. update returns true when the later build is in
// place, for the caller to start it instead of this one (renew).
//
// A run looks for a later release before it asks the server anything (play), so the look is one small download with a
// short limit (checkLimit). turn takes the turn to change what the PC's launchers share (filesTurn). update calls it
// only once there is a later release to take, so a launcher that has none waits for no other launcher.
func (la *launcher) update(ctx context.Context, turn func()) (bool, error) {
	to := la.installed()
	os.Remove(to + ".old") // left by an earlier update
	if la.key == nil || la.version <= 0 || la.exe == "" || os.Getenv(updatedEnv) != "" || !sameFile(la.exe, to) {
		return false, nil
	}
	text, err := la.download(ctx, launcherapi.LatestFile(la.releases, launcherapi.Signed), checkLimit, 64<<10)
	if err != nil {
		return false, err
	}
	var signed release.Signed
	if err := json.Unmarshal(text, &signed); err != nil {
		return false, errors.New("the latest release's statement can't be read")
	}
	st, err := signed.Verify(la.key)
	if err != nil {
		return false, err
	}
	if st.Version <= la.version {
		return false, nil // an earlier release, or this one: the launcher keeps its own
	}
	turn()
	data, err := la.download(ctx, launcherapi.ReleaseFile(la.releases, st.Version, launcherapi.Setup), 5*time.Minute, 64<<20)
	if err != nil {
		return false, err
	}
	if release.HashOf(data) != st.Hash {
		return false, errors.New("what was downloaded is not the build its maker signed")
	}
	if err := writeFile(to+".new", data); err != nil {
		return false, err
	}
	if err := la.place(to + ".new"); err != nil {
		return false, err
	}
	return true, nil
}

// checkLimit is how long the launcher waits for the latest release's statement (update). A link's run waits for it
// before the server, and so the player's room, hears of the launcher, so it is short.
const checkLimit = 8 * time.Second

// download returns the file at url, of the launcher's releases. It gives up after wait, and on a file of more than
// limit bytes.
func (la *launcher) download(ctx context.Context, url string, wait time.Duration, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	rsp, err := la.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the launcher's releases can't be reached: %v", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, rsp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(rsp.Body, limit+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("the launcher's releases can't be reached: %v", err)
	case int64(len(data)) > limit:
		return nil, fmt.Errorf("%s is larger than it can be", url)
	}
	return data, nil
}

// tooOld returns the error for a server that doesn't take this launcher (errOld). It says where the launcher's latest
// release is, for the player to install by hand. The launcher looked for a later release before (update), and took
// none.
func (la *launcher) tooOld() error {
	setup := launcherapi.LatestFile(la.releases, launcherapi.Setup)
	if la.version <= 0 {
		return oldError("This launcher is a dev build, and this server only accepts released launchers. Install the " +
			"latest release, then try again: " + setup)
	}
	return oldError(errOld.Error() + " Install the new one, then try again: " + setup)
}

// oldError is errOld, with what the player can do about it.
type oldError string

func (e oldError) Error() string        { return string(e) }
func (e oldError) Is(target error) bool { return target == errOld }
