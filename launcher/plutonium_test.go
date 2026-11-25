package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The launcher notes the game's folder for Plutonium's own launcher where that names none: in a file as Plutonium's
// launcher writes it, and on a PC where it has not run. It changes nothing else of the file, and no folder that the
// player set up there.
func TestNoteGameFolder(t *testing.T) {
	const dir = `C:\Games\Tom & Jerry's <BO2>`
	const noted = `"t6Path":"C:\\Games\\Tom & Jerry's <BO2>"`
	for _, c := range []struct {
		what, had, has string // had is "" for a PC without the file, and has is "" for a file that stays as it is
	}{
		{"Plutonium's launcher has not run", "", `{` + noted + `}`},
		{"no game is set up, and the player is signed in",
			`{"iw5Path":"","lastGame":"t6zm","t4Path":"","t5Path":"","t6Path":"","token":"ab.c&d<e>\u00e9"}`,
			`{"iw5Path":"","lastGame":"t6zm","t4Path":"","t5Path":"",` + noted + `,"token":"ab.c&d<e>\u00e9"}`},
		{"the file has no folder of the game", `{"token":"abc","lastGame":"t5sp"}`,
			`{"lastGame":"t5sp",` + noted + `,"token":"abc"}`},
		{"the game's folder is nothing", `{"t6Path":null}`, `{` + noted + `}`},
		{"the player set the game up", `{"lastGame":"t6zm","t6Path":"D:\\Steam\\Black Ops II","token":"abc"}`, ""},
		{"the game's folder is no text", `{"t6Path":6}`, ""},
		{"the file is a list", `["t6Path"]`, ""},
		{"the file is nothing", `null`, ""},
		{"the file is cut off", `{"t6Path":""`, ""},
	} {
		file := filepath.Join(t.TempDir(), "Plutonium", plutoniumConfig)
		if c.had != "" {
			write(t, file, c.had)
		}
		did, err := noteGameFolder(file, dir)
		want := c.has
		if want == "" {
			want = c.had
		}
		if err != nil || did != (c.has != "") || read(t, file) != want {
			t.Errorf("%s: noted %v, %v, and the file is\n%s\nnot\n%s", c.what, did, err, read(t, file), want)
		}
	}
	// a file that can't be read is an error, and nothing is written over it
	folder := filepath.Join(t.TempDir(), plutoniumConfig)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if did, err := noteGameFolder(folder, dir); did || err == nil {
		t.Errorf("a folder in the file's place: noted %v, %v", did, err)
	}
}
