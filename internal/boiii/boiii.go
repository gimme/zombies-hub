// Package boiii keeps a copy of BOIII (https://github.com/Ezz-lol/boiii-free) up to date from BOIII's own updater.
// BOIII is the client that Black Ops III plays on outside Steam's servers. It is one program, boiii.exe, which loads
// the game's own program beside it and runs that: as a player's game, or, on Steam's dedicated server of the game, as
// a server. The server has a copy, which a Black Ops III room's game runs (server/t7.go).
package boiii

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zombies/internal/plutonium"
)

// Manifest is the address of the manifest that BOIII's updater serves: a list of BOIII's files, each by its name,
// with the file's size and SHA-1. The files themselves are under the manifest's address less its ".json", by their
// names. A player's BOIII brings itself up to date from the manifest each time it starts. A dedicated server never
// does, so the server does it for its copy before a Black Ops III game.
const Manifest = "https://r2.ezz.lol/boiii.json"

// Exe is the name of BOIII's program in a copy, and Data that of the folder of its data: its scripts and Lua, the
// game settings, and the names of the game's settings, which the game itself has only as hashes. BOIII looks for the
// data in its folder among the user's local application data, %LOCALAPPDATA%\boiii\data.
const (
	Exe  = "boiii.exe"
	Data = "data"
)

// agent is the User-Agent of the copy's requests. The updater's host turns away the one that Go sends by itself.
const agent = "zombies-hub"

// Copy is a copy of BOIII in Dir: its program (Exe) and its data (Data). Update downloads it from BOIII's updater
// and keeps it up to date.
type Copy struct {
	Dir    string
	URL    string // the manifest's address
	Client *http.Client
	// State names the file in Dir where Update records what it left there: each file's hash by the file's name.
	State string
	// Fetching, when set, is called before an update downloads, with the number of files and their size in bytes.
	Fetching func(files int, bytes int64)
	// Logf, when set, is called with what an update did, or with the fact that Update could not reach the updater.
	Logf func(format string, args ...any)

	mu sync.Mutex // one update at a time
	// build is the build that the copy has, as Build returns it. read makes Build read it from the state file once.
	build atomic.Pointer[string]
	read  sync.Once
}

// file is a file of BOIII's in the manifest, which lists each as [name, size, SHA-1].
type file struct {
	name string
	size int64
	hash string
}

func (f *file) UnmarshalJSON(data []byte) error {
	fields := [3]any{&f.name, &f.size, &f.hash}
	return json.Unmarshal(data, &fields)
}

// has is what the copy has, as Update left it (Dir/State).
type has struct {
	Files map[string]string `json:"files"`
}

func (c *Copy) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Build returns the build of BOIII that Update last brought the copy to: the start of the SHA-1 of its program. It
// returns "" for a copy that Update has not run on.
func (c *Copy) Build() string {
	c.read.Do(func() {
		var had has
		if data, err := os.ReadFile(filepath.Join(c.Dir, c.State)); err == nil && json.Unmarshal(data, &had) == nil {
			if build := buildOf(had.Files[Exe]); build != "" {
				c.build.CompareAndSwap(nil, &build)
			}
		}
	})
	if build := c.build.Load(); build != nil {
		return *build
	}
	return ""
}

// buildOf returns the name of the build whose program has the given SHA-1.
func buildOf(hash string) string { return hash[:min(len(hash), 12)] }

// Update brings the copy up to BOIII's current build. It downloads the files that the copy lacks or has with other
// contents. Each downloaded file is checked against the manifest's size and SHA-1 before it replaces the copy's file.
// Update removes the files of an earlier update that the build no longer has: BOIII loads every script that its data
// has, so one left behind would still run. When the updater can't be reached, the copy runs as it is, and Update
// returns an error only when there is no copy.
func (c *Copy) Update(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	files, err := c.manifest(ctx)
	if err != nil {
		if st, serr := os.Stat(filepath.Join(c.Dir, Exe)); serr == nil && st.Mode().IsRegular() {
			c.logf("boiii: no word of updates (%v): the copy runs as it is", err)
			return nil
		}
		return fmt.Errorf("can't download BOIII: %w", err)
	}
	var had has
	if data, err := os.ReadFile(filepath.Join(c.Dir, c.State)); err == nil {
		json.Unmarshal(data, &had)
	}
	now := has{Files: map[string]string{}}
	var todo []file
	var bytes int64
	for _, f := range files {
		f.hash = strings.ToLower(f.hash)
		if f.name != Exe && !strings.HasPrefix(f.name, Data+"/") {
			continue
		}
		// The name has to stay inside the folder, and the hash has to be a SHA-1 in hex.
		if !filepath.IsLocal(filepath.FromSlash(f.name)) || len(f.hash) != 40 ||
			strings.Trim(f.hash, "0123456789abcdef") != "" || f.size < 0 {
			return fmt.Errorf("can't download BOIII: its manifest names a file %q, with hash %q", f.name, f.hash)
		}
		now.Files[f.name] = f.hash
		// Skip a file that is as the last update left it. A file that Update didn't download is hashed once.
		at := filepath.Join(c.Dir, filepath.FromSlash(f.name))
		if st, err := os.Stat(at); err == nil && st.Mode().IsRegular() && st.Size() == f.size &&
			(had.Files[f.name] == f.hash || sha1Of(at) == f.hash) {
			continue
		}
		todo = append(todo, f)
		bytes += f.size
	}
	if _, ok := now.Files[Exe]; !ok {
		return errors.New("can't download BOIII: its manifest has no " + Exe)
	}
	if len(todo) > 0 && c.Fetching != nil {
		c.Fetching(len(todo), bytes)
	}
	base := strings.TrimSuffix(c.URL, ".json") + "/"
	for _, f := range todo {
		at := filepath.Join(c.Dir, filepath.FromSlash(f.name))
		if err := c.download(ctx, base+escape(f.name), at, f.size, f.hash); err != nil {
			return fmt.Errorf("can't download BOIII's %s: %w", f.name, err)
		}
	}
	removed := 0
	for name := range had.Files {
		if _, ok := now.Files[name]; !ok && filepath.IsLocal(filepath.FromSlash(name)) {
			at := filepath.Join(c.Dir, filepath.FromSlash(name))
			os.Remove(at)
			// the folders that the file leaves empty go too, up to the copy's own
			for dir := filepath.Dir(at); dir != c.Dir && os.Remove(dir) == nil; dir = filepath.Dir(dir) {
			}
			removed++
		}
	}
	build := buildOf(now.Files[Exe])
	if len(todo) > 0 || removed > 0 || len(had.Files) != len(now.Files) {
		data, _ := json.Marshal(now)
		if err := os.WriteFile(filepath.Join(c.Dir, c.State), data, 0o644); err != nil {
			return err
		}
		c.logf("boiii: build %s: downloaded %d of its %d files (%.1f MB)", build, len(todo), len(now.Files),
			float64(bytes)/1e6)
	}
	c.build.Store(&build)
	return nil
}

// escape returns a file's name as a path in an address.
func escape(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return path.Join(parts...)
}

func (c *Copy) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", agent)
	rsp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if rsp.StatusCode != http.StatusOK {
		rsp.Body.Close()
		return nil, errors.New(rsp.Status)
	}
	return rsp, nil
}

func (c *Copy) manifest(ctx context.Context) ([]file, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rsp, err := c.get(ctx, c.URL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.URL, err)
	}
	defer rsp.Body.Close()
	var files []file
	if err := json.NewDecoder(io.LimitReader(rsp.Body, 4<<20)).Decode(&files); err != nil {
		return nil, fmt.Errorf("%s: %w", c.URL, err)
	}
	return files, nil
}

// download gets address into file, by way of a .part file next to it. The .part file replaces file once it has the
// given size and hash.
func (c *Copy) download(ctx context.Context, address, file string, size int64, hash string) error {
	rsp, err := c.get(ctx, address)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	part, err := os.OpenFile(file+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer os.Remove(file + ".part")
	sum := sha1.New()
	n, err := io.Copy(io.MultiWriter(part, sum), io.LimitReader(rsp.Body, size+1))
	if cerr := part.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n != size:
		return fmt.Errorf("it is %d bytes, not the manifest's %d", n, size)
	case hex.EncodeToString(sum.Sum(nil)) != hash:
		return errors.New("its contents are not the manifest's")
	}
	return plutonium.Rename(file+".part", file)
}

// sha1Of returns a file's SHA-1 in hex, or "" when the file can't be read.
func sha1Of(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sum := sha1.New()
	if _, err := io.Copy(sum, f); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}
