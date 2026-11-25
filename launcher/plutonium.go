package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// Plutonium's own launcher, which a player signs in to for Plutonium's online servers, keeps its settings in
// config.json in Plutonium's folder. The file is one JSON object: the folder of each game ("t6Path" is Black Ops
// II's, and "" for a game that the player has not set up there), the game they picked last, and the token of their
// sign-in. Plutonium's launcher asks for a game's folder before it first starts the game (Setup).
//
// This launcher plays on the same copy of Plutonium, and knows where the game is. So when it starts the game, it
// notes the game's folder there for Plutonium's launcher, where that has none: a player who signs in to Plutonium
// later finds Black Ops II set up. A folder that the file names already stays: it is the player's own choice there.

// plutoniumConfig is the settings file of Plutonium's own launcher, in Plutonium's folder.
const plutoniumConfig = "config.json"

// noteGameFolder notes dir as Black Ops II's folder in the settings of Plutonium's own launcher (file), when they
// name none, and reports whether it did. It makes the file on a PC where Plutonium's launcher has not run. Each
// other member of the file stays as it is, byte for byte: the player's token is among them. A file that is not a
// JSON object stays as it is too.
func noteGameFolder(file, dir string) (bool, error) {
	members := map[string]json.RawMessage{}
	switch data, err := os.ReadFile(file); {
	case err == nil:
		if json.Unmarshal(data, &members) != nil || members == nil {
			return false, nil
		}
	case !os.IsNotExist(err):
		return false, err
	}
	if had, ok := members["t6Path"]; ok {
		var folder string
		if json.Unmarshal(had, &folder) != nil || folder != "" {
			return false, nil
		}
	}
	// As Plutonium's launcher writes the file: on one line, the members by name, and no character of a text written
	// as its number but those that JSON asks for (Go would write an & of a path or of the token so).
	plain := func(v any) ([]byte, error) {
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		err := enc.Encode(v)
		return bytes.TrimRight(out.Bytes(), "\n"), err
	}
	folder, err := plain(dir)
	if err != nil {
		return false, err
	}
	members["t6Path"] = folder
	data, err := plain(members)
	if err != nil {
		return false, err
	}
	return true, writeFile(file, data)
}

// tellPlutonium notes the folder that the game started from for Plutonium's own launcher (noteGameFolder). It is a
// favour to a player who signs in there later, so a file that can't be read or written only goes to the log. What
// is in the file never does.
func (la *launcher) tellPlutonium(dir string) {
	switch noted, err := noteGameFolder(filepath.Join(la.plutonium, plutoniumConfig), dir); {
	case err != nil:
		la.say("Plutonium's own launcher was not told where Black Ops II is: %v", err)
	case noted:
		la.say("Plutonium's own launcher now has Black Ops II's folder too: %s", dir)
	}
}
