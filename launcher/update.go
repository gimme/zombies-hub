package main

import (
	"context"
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

// exeName is the name of the launcher's file in its folder. The server offers the same program under another name,
// which describes what a player does with it (launcherapi.Setup).
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
		return errors.New("The launcher can't tell where its own file is, to install it.")
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
			return fmt.Errorf("The launcher could not be put in its folder, %s: %v", la.home, err)
		}
	}
	if err := la.register(to); err != nil {
		return fmt.Errorf("Windows didn't take the launcher's links: %v", err)
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
		return errors.New(appName + " is running: quit it from its icon in the tray, close the game it started, then uninstall it again.")
	}
	la.loadConfig()
	files := la.downloads()
	state, bytes, downloaded := synced(files)
	wipe := downloaded && la.confirm(fmt.Sprintf("Also delete the Black Ops II game files that %s downloaded (%s)?\n\n%s",
		appName, megabytes(bytes), files))
	if err := la.unregister(); err != nil {
		return fmt.Errorf("Windows kept the launcher's entries: %v", err)
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
			return fmt.Errorf("The launcher's program could not be deleted, %s: %v", exe, err)
		}
		la.atExit = func() { la.later(exe, la.home) }
	} else {
		os.Remove(la.home)
	}
	la.say("%s is uninstalled.", appName)
	if left {
		la.notify(fmt.Sprintf("%s is uninstalled, but some of the game files could not be deleted (is the game open?). "+
			"Their folder:\n\n%s", appName, files), false)
	}
	return nil
}

// update replaces this launcher with the server's build of it, without asking the player, when that is a later build
// of this launcher's maker. The server sends the maker's signed statement of the build with it (internal/release),
// and can't make a statement itself. So whoever runs a server, or breaks into one, can't put a program of their own
// in the launcher's place: the launcher keeps the build it is, and plays on with it. The launcher doesn't accept an
// earlier build of the maker's either, which may have a fault that was fixed since. update acts only for the
// installed launcher, which links run, and once a run. It returns true when the server's build is in place, for the
// caller to start it instead of this one.
func (la *launcher) update(ctx context.Context, srv *server, play *launcherapi.Play) (bool, error) {
	to := la.installed()
	os.Remove(to + ".old") // left by an earlier update
	if play.Launcher == "" || la.exe == "" || os.Getenv(updatedEnv) != "" || !sameFile(la.exe, to) {
		return false, nil
	}
	if own, err := hashFile(ctx, la.exe); err != nil || own == play.Launcher {
		return false, err
	}
	switch {
	case la.key == nil:
		return false, errors.New("this launcher was built without its maker's key, and takes no other build by itself")
	case play.Release == nil:
		return false, errors.New("the server's build is not signed")
	}
	st, err := play.Release.Verify(la.key)
	if err != nil {
		return false, err
	}
	if st.Version <= la.version {
		return false, nil // the server's build is an earlier one, or this one: the launcher keeps its own
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := srv.request(ctx, http.MethodGet, "/"+launcherapi.Setup, nil)
	if err != nil {
		return false, err
	}
	rsp, err := srv.client.Do(req)
	if err != nil {
		return false, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return false, failed(rsp)
	}
	data, err := io.ReadAll(io.LimitReader(rsp.Body, 64<<20))
	if err != nil {
		return false, err
	}
	if release.HashOf(data) != st.Hash {
		return false, errors.New("what the server sent is not the build its maker signed")
	}
	if err := writeFile(to+".new", data); err != nil {
		return false, err
	}
	if err := la.place(to + ".new"); err != nil {
		return false, err
	}
	return true, nil
}
