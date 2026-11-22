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
}

func (la *launcher) configFile() string { return filepath.Join(la.home, "launcher.json") }

func (la *launcher) loadConfig() {
	la.cfg = config{}
	if data, err := os.ReadFile(la.configFile()); err == nil {
		json.Unmarshal(data, &la.cfg)
	}
}

func (la *launcher) saveConfig() error {
	data, _ := json.MarshalIndent(la.cfg, "", "  ")
	return writeFile(la.configFile(), append(data, '\n'))
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
