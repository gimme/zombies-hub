package plutonium

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zombies/internal/plutonium/plutoniumtest"
)

var files = map[string]string{
	Bootstrapper:                       "the bootstrapper",
	"bin/steam_api64.dll":              "a dll",
	"games/t6zm.exe":                   "T6's Zombies",
	"games/iw5mp.exe":                  "another game",
	"storage/t6/zone/zm_patch.ff":      "a zone",
	"storage/t6/raw/scripts/zm/a.gsc":  "a script",
	"storage/demonware/zm/playlists":   "playlists",
	"storage/iw5/zone/english/huge.ff": "another game's",
}

func isFile(file string) bool {
	st, err := os.Stat(file)
	return err == nil && st.Mode().IsRegular()
}

type tamper func(*http.Request) (*http.Response, error)

func (f tamper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A copy of Plutonium: Update downloads what a T6 game needs of what its updater serves, each file checked against
// the manifest; again only what changed, and what the next revision has no more is removed; and the copy runs as it is
// when the updater can't be reached.
func TestUpdate(t *testing.T) {
	now, revision := map[string]string{}, 7
	for name, data := range files {
		now[name] = data
	}
	u := plutoniumtest.Serve(t, now, &revision)
	var fetched []int64
	c := &Copy{Dir: filepath.Join(t.TempDir(), "plutonium"), URL: u.URL, Needs: T6, Client: u.Client, State: "files.json",
		Fetching: func(n int, bytes int64) { fetched = append(fetched, int64(n), bytes) }}
	ctx := context.Background()
	if err := c.Update(ctx); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for name, data := range now {
		got, err := os.ReadFile(filepath.Join(c.Dir, filepath.FromSlash(name)))
		needed := !strings.Contains(name, "iw5")
		if needed && string(got) != data || !needed && err == nil {
			t.Errorf("%s: %q, %v", name, got, err)
		}
		if needed {
			bytes += int64(len(data))
		}
	}
	if asked := u.Asked(); len(asked) != 7 { // the manifest, and the six files a T6 game needs
		t.Errorf("the first update asked for %v", asked)
	}
	if len(fetched) != 2 || fetched[0] != 6 || fetched[1] != bytes {
		t.Errorf("the first update said it fetches %v, not 6 files of %d bytes", fetched, bytes)
	}
	if err := c.Update(ctx); err != nil {
		t.Errorf("an update with nothing new: %v", err)
	}
	if asked := u.Asked(); len(asked) != 1 || len(fetched) != 2 {
		t.Errorf("an update with nothing new asked for %v, and said it fetches %v", asked, fetched)
	}
	// the next revision: one file changed, one gone, one new
	revision = 8
	now[Bootstrapper] = "the next bootstrapper"
	delete(now, "storage/t6/raw/scripts/zm/a.gsc")
	now["storage/t6/raw/scripts/zm/b.gsc"] = "another script"
	if err := c.Update(ctx); err != nil {
		t.Errorf("the next revision: %v", err)
	}
	if asked := u.Asked(); len(asked) != 3 {
		t.Errorf("the next revision asked for %v", asked)
	}
	if got, _ := os.ReadFile(filepath.Join(c.Dir, filepath.FromSlash(Bootstrapper))); string(got) != "the next bootstrapper" {
		t.Errorf("the bootstrapper after the update: %q", got)
	}
	scripts := filepath.Join(c.Dir, "storage", "t6", "raw", "scripts", "zm")
	if isFile(filepath.Join(scripts, "a.gsc")) || !isFile(filepath.Join(scripts, "b.gsc")) {
		t.Error("the scripts are not the next revision's")
	}
	// a file of the copy that no update put there stays, whatever the revision: a copy that Plutonium's launcher
	// keeps too has its other games, and the player's settings
	other := filepath.Join(c.Dir, "storage", "t6", "players", "settings.cfg")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("the player's"), 0o644)
	revision = 9
	if err := c.Update(ctx); err != nil || !isFile(other) {
		t.Errorf("an update of a copy with a file of the player's: %v; the file is there: %v", err, isFile(other))
	}
	u.Asked()
	// a file that isn't the manifest's is not kept: the copy stays as it was
	now["games/t6zm.exe"] = "T6's Zombies, again"
	good := c.Client
	c.Client = &http.Client{Transport: tamper(func(req *http.Request) (*http.Response, error) {
		rsp, err := good.Transport.RoundTrip(req)
		if err == nil && strings.Contains(req.URL.Path, "/files/") {
			rsp.Body = io.NopCloser(strings.NewReader("T6's Zombies, tampered"))
		}
		return rsp, err
	})}
	if err := c.Update(ctx); err == nil {
		t.Error("an update took a file that isn't the manifest's")
	}
	if got, _ := os.ReadFile(filepath.Join(c.Dir, "games/t6zm.exe")); string(got) != "T6's Zombies" {
		t.Errorf("the game after a refused download: %q", got)
	}
	// the updater unreachable: the copy runs as it is; with none, there is nothing to run
	var said []string
	c.Client, c.URL = good, "http://127.0.0.1:1/info.json"
	c.Logf = func(format string, args ...any) { said = append(said, format) }
	if err := c.Update(ctx); err != nil || len(said) != 1 {
		t.Errorf("an update with no updater, and a copy: %v, saying %q", err, said)
	}
	c.Dir = filepath.Join(t.TempDir(), "none")
	if err := c.Update(ctx); err == nil {
		t.Error("an update with no updater and no copy went well")
	}
}

// A manifest can't name a file outside the copy.
func TestUpdateStaysInItsFolder(t *testing.T) {
	revision := 1
	u := plutoniumtest.Serve(t, map[string]string{Bootstrapper: "x", "bin/../../outside": "y"}, &revision)
	c := &Copy{Dir: filepath.Join(t.TempDir(), "plutonium"), URL: u.URL, Needs: T6, Client: u.Client, State: "files.json"}
	if err := c.Update(context.Background()); err == nil {
		t.Error("an update took a file outside its folder")
	}
	if isFile(filepath.Join(filepath.Dir(c.Dir), "outside")) {
		t.Error("a file outside the copy was written")
	}
}
