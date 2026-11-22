// Package plutonium keeps a copy of Plutonium (https://plutonium.pw) up to date from Plutonium's own updater. The
// server has a copy, which a Black Ops II room's game runs (server/t6.go). A player's PC has a copy, which the
// launcher starts the player's game from (launcher/).
package plutonium

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manifest is the address of the manifest that Plutonium's updater serves. The manifest has Plutonium's revision,
// and each of its files by name, with the file's size and SHA-1. The SHA-1 is the file's name under baseUrl.
// Plutonium's clients update themselves from the manifest when Plutonium's own launcher starts, and a Plutonium
// server of another revision turns them away ("Your game is outdated"). So the server requests the manifest before
// each T6 game. The launcher requests it only when the PC's copy is not of the server's revision (Revision).
const Manifest = "https://cdn.plutoniummod.com/updater/prod/info.json"

// Bootstrapper is the path, in Plutonium's folder, of the program that starts a Plutonium game.
const Bootstrapper = "bin/plutonium-bootstrapper-win32.exe"

// T6 lists the files of Plutonium that a T6 Zombies game needs, on a server and on a player's PC alike: Plutonium's
// programs, T6's Zombies game, and their data.
var T6 = []string{"bin/", "games/t6zm.exe", "storage/t6/", "storage/demonware/"}

// Copy is a copy of Plutonium in Dir. Update downloads it from Plutonium's updater and keeps it up to date.
type Copy struct {
	Dir    string
	URL    string   // the manifest's address
	Needs  []string // the files it keeps, by the start of their names (T6)
	Client *http.Client
	// State names the file in Dir where Update records what it left there: the revision, and each file's hash by
	// the file's name. A copy that Plutonium's own launcher keeps too has both.
	State string
	// Fetching, when set, is called before an update downloads, with the number of files and their size in bytes.
	Fetching func(files int, bytes int64)
	// Logf, when set, is called with what an update did, or with the fact that Update could not reach the updater.
	Logf func(format string, args ...any)

	mu sync.Mutex // one update at a time
	// revision is the revision that the copy has, as Revision returns it. read makes Revision read it from the
	// state file once.
	revision atomic.Int64
	read     sync.Once
}

type info struct {
	Revision int    `json:"revision"`
	BaseURL  string `json:"baseUrl"`
	Files    []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Hash string `json:"hash"`
	} `json:"files"`
}

// has is what the copy has, as Update left it (Dir/State).
type has struct {
	Revision int               `json:"revision"`
	Files    map[string]string `json:"files"`
}

func (c *Copy) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Revision returns the revision of Plutonium that Update last brought the copy to. It returns 0 for a copy that
// Update has not run on. Two copies of the same revision have the same files, so a player's copy of the server's
// revision can join the server's game.
func (c *Copy) Revision() int {
	c.read.Do(func() {
		var had has
		if data, err := os.ReadFile(filepath.Join(c.Dir, c.State)); err == nil && json.Unmarshal(data, &had) == nil {
			c.revision.CompareAndSwap(0, int64(had.Revision))
		}
	})
	return int(c.revision.Load())
}

// Complete reports whether the copy still has every file that Update last left in it. A file can go missing after
// an update: an antivirus may remove it.
func (c *Copy) Complete() bool {
	var had has
	data, err := os.ReadFile(filepath.Join(c.Dir, c.State))
	if err != nil || json.Unmarshal(data, &had) != nil || len(had.Files) == 0 {
		return false
	}
	for name := range had.Files {
		file := filepath.FromSlash(name)
		if !filepath.IsLocal(file) {
			return false
		}
		if st, err := os.Stat(filepath.Join(c.Dir, file)); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// Update brings the copy up to Plutonium's current revision. It downloads the files it needs that the copy lacks or
// has with other contents. Each downloaded file is checked against the manifest's size and SHA-1 before it replaces
// the copy's file. Update removes the files of an earlier update that the revision no longer has. When the updater
// can't be reached, the copy runs as it is, and Update returns an error only when there is no copy.
func (c *Copy) Update(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, err := c.manifest(ctx)
	if err != nil {
		if st, serr := os.Stat(filepath.Join(c.Dir, filepath.FromSlash(Bootstrapper))); serr == nil && st.Mode().IsRegular() {
			c.logf("plutonium: no word of updates (%v): the copy runs as it is", err)
			return nil
		}
		return fmt.Errorf("can't download Plutonium: %w", err)
	}
	var had has
	if data, err := os.ReadFile(filepath.Join(c.Dir, c.State)); err == nil {
		json.Unmarshal(data, &had)
	}
	now := has{Revision: manifest.Revision, Files: map[string]string{}}
	type fetch struct {
		name, hash string
		size       int64
	}
	var todo []fetch
	var bytes int64
	for _, f := range manifest.Files {
		hash := strings.ToLower(f.Hash)
		if !slices.ContainsFunc(c.Needs, func(need string) bool { return strings.HasPrefix(f.Name, need) }) {
			continue
		}
		// The name has to stay inside the folder, and the hash has to be a SHA-1 in hex: both go into paths.
		if !filepath.IsLocal(filepath.FromSlash(f.Name)) || len(hash) != 40 || strings.Trim(hash, "0123456789abcdef") != "" {
			return fmt.Errorf("can't download Plutonium: its manifest names a file %q, with hash %q", f.Name, f.Hash)
		}
		file := filepath.Join(c.Dir, filepath.FromSlash(f.Name))
		now.Files[f.Name] = hash
		// Skip a file that is as the last update left it. A file that Update didn't download (in a copy made by
		// hand, or by Plutonium's launcher) is hashed once.
		if st, err := os.Stat(file); err == nil && st.Mode().IsRegular() && st.Size() == f.Size &&
			(had.Files[f.Name] == hash || sha1Of(file) == hash) {
			continue
		}
		todo = append(todo, fetch{f.Name, hash, f.Size})
		bytes += f.Size
	}
	if _, ok := now.Files[Bootstrapper]; !ok {
		return errors.New("can't download Plutonium: its manifest has no " + Bootstrapper)
	}
	if len(todo) > 0 && c.Fetching != nil {
		c.Fetching(len(todo), bytes)
	}
	for _, f := range todo {
		file := filepath.Join(c.Dir, filepath.FromSlash(f.name))
		if err := c.download(ctx, manifest.BaseURL+f.hash, file, f.size, f.hash); err != nil {
			return fmt.Errorf("can't download Plutonium's %s: %w", f.name, err)
		}
	}
	for name := range had.Files {
		if _, ok := now.Files[name]; !ok && filepath.IsLocal(filepath.FromSlash(name)) {
			os.Remove(filepath.Join(c.Dir, filepath.FromSlash(name)))
		}
	}
	if len(todo) > 0 || had.Revision != now.Revision || len(had.Files) != len(now.Files) {
		data, _ := json.Marshal(now)
		if err := os.WriteFile(filepath.Join(c.Dir, c.State), data, 0o644); err != nil {
			return err
		}
		c.logf("plutonium: revision %d: downloaded %d of its %d files (%.1f MB)", manifest.Revision, len(todo),
			len(now.Files), float64(bytes)/1e6)
	}
	c.revision.Store(int64(now.Revision))
	return nil
}

func (c *Copy) manifest(ctx context.Context) (*info, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	rsp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", c.URL, rsp.Status)
	}
	var m info
	if err := json.NewDecoder(io.LimitReader(rsp.Body, 4<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", c.URL, err)
	}
	if !strings.HasPrefix(m.BaseURL, "https://") && !strings.HasPrefix(m.BaseURL, "http://127.0.0.1:") {
		return nil, fmt.Errorf("%s: its files are at %q", c.URL, m.BaseURL)
	}
	return &m, nil
}

// download gets url into file, by way of a .part file next to it. The .part file replaces file once it has the given
// size and hash.
func (c *Copy) download(ctx context.Context, url, file string, size int64, hash string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	rsp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return errors.New(rsp.Status)
	}
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
	return Rename(file+".part", file)
}

// Rename is os.Rename, tried again for a few seconds on Windows. An antivirus reads a file that was just written,
// above all a program. For that moment Windows lets no one move the file or put another file in its place.
func Rename(from, to string) error {
	err := os.Rename(from, to)
	for try := 0; err != nil && runtime.GOOS == "windows" && try < 25; try++ {
		if _, serr := os.Stat(from); serr != nil {
			break // from is gone: no further try moves it
		}
		time.Sleep(200 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
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
