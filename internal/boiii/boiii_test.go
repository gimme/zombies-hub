package boiii

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zombies/internal/boiii/boiiitest"
)

var files = map[string]string{
	Exe:                                   "the program",
	"data/launcher/main.html":             "the launcher's page",
	"data/lookup_tables/dvar_list.txt":    "the settings' names",
	"data/scripts/zm/_zm.gsh":             "a script",
	"data/ui_scripts/party/__init__.lua":  "a menu's script",
	"data/custom scripts/zm/with space.x": "a name with a space",
}

type tamper func(*http.Request) (*http.Response, error)

func (f tamper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A copy of BOIII: Update downloads what its updater serves, each file checked against the manifest; again only what
// changed, and what the next build has no more is removed, with the folders it leaves empty; and the copy runs as it
// is when the updater can't be reached.
func TestUpdate(t *testing.T) {
	now := map[string]string{}
	for name, data := range files {
		now[name] = data
	}
	u := boiiitest.Serve(t, now)
	var fetched []int64
	c := &Copy{Dir: filepath.Join(t.TempDir(), "boiii"), URL: u.URL, Client: u.Client, State: "files.json",
		Fetching: func(n int, bytes int64) { fetched = append(fetched, int64(n), bytes) }}
	ctx := context.Background()
	if c.Build() != "" {
		t.Errorf("a copy that is not there has build %q", c.Build())
	}
	if err := c.Update(ctx); err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for name, data := range now {
		if got, err := os.ReadFile(filepath.Join(c.Dir, filepath.FromSlash(name))); string(got) != data {
			t.Errorf("%s: %q, %v", name, got, err)
		}
		bytes += int64(len(data))
	}
	if len(fetched) != 2 || fetched[0] != int64(len(now)) || fetched[1] != bytes {
		t.Errorf("fetching: %v, not %d files of %d bytes", fetched, len(now), bytes)
	}
	first := c.Build()
	if len(first) != 12 {
		t.Errorf("build %q", first)
	}
	if again := (&Copy{Dir: c.Dir, State: c.State}); again.Build() != first {
		t.Errorf("the copy's build, read again: %q, not %q", again.Build(), first)
	}

	// nothing changed: the manifest alone is asked for
	u.Asked()
	if err := c.Update(ctx); err != nil {
		t.Fatal(err)
	}
	if asked := u.Asked(); len(asked) != 1 {
		t.Errorf("an update of a copy that is up to date asked for %v", asked)
	}

	// the next build: another program, a script more, and one no more
	now[Exe] = "the next program"
	now["data/scripts/zm/new.gsc"] = "a new script"
	delete(now, "data/ui_scripts/party/__init__.lua")
	if err := c.Update(ctx); err != nil {
		t.Fatal(err)
	}
	if asked := u.Asked(); len(asked) != 3 {
		t.Errorf("the next build asked for %v", asked)
	}
	if c.Build() == first {
		t.Errorf("the next build is %q too", first)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "data", "ui_scripts")); !os.IsNotExist(err) {
		t.Errorf("the folder of a script that the build has no more is still there: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(c.Dir, Exe)); string(got) != now[Exe] {
		t.Errorf("the program is %q", got)
	}

	// the updater is gone: the copy runs as it is
	gone := &Copy{Dir: c.Dir, URL: "http://127.0.0.1:1/boiii.json", Client: &http.Client{}, State: c.State}
	if err := gone.Update(ctx); err != nil {
		t.Errorf("a copy whose updater is gone: %v", err)
	}
	none := &Copy{Dir: filepath.Join(t.TempDir(), "none"), URL: gone.URL, Client: &http.Client{}, State: c.State}
	if err := none.Update(ctx); err == nil {
		t.Error("no copy and no updater: no error")
	}
}

// A file that is not what the manifest says is not kept, and neither is a manifest that names a file outside the
// copy's folder.
func TestUpdateChecks(t *testing.T) {
	u := boiiitest.Serve(t, files)
	ctx := context.Background()
	client := &http.Client{Transport: tamper(func(req *http.Request) (*http.Response, error) {
		rsp, err := u.Client.Transport.RoundTrip(req)
		if err == nil && strings.HasSuffix(req.URL.Path, "/"+Exe) {
			rsp.Body = io.NopCloser(strings.NewReader("another one!"))
		}
		return rsp, err
	})}
	c := &Copy{Dir: filepath.Join(t.TempDir(), "boiii"), URL: u.URL, Client: client, State: "files.json"}
	if err := c.Update(ctx); err == nil || !strings.Contains(err.Error(), Exe) {
		t.Errorf("a program with other contents: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, Exe)); !os.IsNotExist(err) {
		t.Errorf("the program with other contents was kept: %v", err)
	}

	out := boiiitest.Serve(t, map[string]string{Exe: "the program", "data/../../out.txt": "outside"})
	c = &Copy{Dir: filepath.Join(t.TempDir(), "boiii"), URL: out.URL, Client: out.Client, State: "files.json"}
	if err := c.Update(ctx); err == nil || !strings.Contains(err.Error(), "names a file") {
		t.Errorf("a manifest with a file outside the folder: %v", err)
	}
}
