package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"zombies/internal/plutonium"
)

// config has the player's answers that the launcher keeps, in launcher.json in its folder.
type config struct {
	// Servers are the servers this PC plays on, as the player agreed (trust). Each is the address of its page,
	// https://host[:port].
	Servers []string `json:"servers,omitempty"`
	// T6 is the folder of the player's own copy of Black Ops II, when the player named one. Without one, the
	// launcher still looks where Steam installs the game. "none" makes the launcher use the server's game files,
	// whatever copy the PC has.
	T6 string `json:"t6,omitempty"`
	// T6Files is the folder where the launcher keeps the game files it downloads, when the player named another
	// folder than t6 in the launcher's own folder.
	T6Files string `json:"t6Files,omitempty"`
	// T7 and T7Files are the same two folders of Black Ops III.
	T7      string `json:"t7,omitempty"`
	T7Files string `json:"t7Files,omitempty"`
}

// own returns where the config has the folder of the player's own copy of a title (T6, T7), and files where it has
// the folder of the title's game files that the launcher downloads (T6Files, T7Files), each to read or to set.
func (c *config) own(t *title) *string {
	if t == titleT7 {
		return &c.T7
	}
	return &c.T6
}

func (c *config) files(t *title) *string {
	if t == titleT7 {
		return &c.T7Files
	}
	return &c.T6Files
}

// The run and the tray's menu (menu.go) both change the config, each on a goroutine of its own. Each change loads the
// file, changes it and saves it, under cfgLock (rewrite). So neither saves a config it read before the other's change,
// which would undo that change. la.cfg is the run's copy: only the run reads or sets it.

func (la *launcher) configFile() string { return filepath.Join(la.home, "launcher.json") }

// loadConfig reads the config into the run's copy.
func (la *launcher) loadConfig() {
	la.cfgLock.Lock()
	defer la.cfgLock.Unlock()
	la.cfg = la.readConfig()
}

func (la *launcher) readConfig() config {
	var c config
	if data, err := os.ReadFile(la.configFile()); err == nil {
		json.Unmarshal(data, &c)
	}
	return c
}

// rewrite loads the config, changes it with change, and saves it, all under cfgLock. It returns the config as it
// saved it.
func (la *launcher) rewrite(change func(c *config)) (config, error) {
	la.cfgLock.Lock()
	defer la.cfgLock.Unlock()
	c := la.readConfig()
	change(&c)
	data, _ := json.MarshalIndent(c, "", "  ")
	return c, writeFile(la.configFile(), append(data, '\n'))
}

// change is rewrite for the run, which keeps the config as it saved it.
func (la *launcher) change(change func(c *config)) error {
	c, err := la.rewrite(change)
	la.cfg = c
	return err
}

// writeFile writes a whole file by way of a temporary file next to it. A launcher that is closed while it writes
// leaves the old file as it was.
func writeFile(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file+".tmp", data, 0o644); err != nil {
		return err
	}
	return plutonium.Rename(file+".tmp", file)
}

func isFile(file string) bool {
	st, err := os.Stat(file)
	return err == nil && st.Mode().IsRegular()
}
