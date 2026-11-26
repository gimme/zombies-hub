package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
)

// The game files that the launcher downloads are in a folder of the launcher's own, one for each title. sync keeps
// such a folder the same as the server's copy of the title, file by file: all of the copy, or where the server sends
// the title in packs (launcherapi.File), the packs that the run wants. Each file is downloaded into a .part file next
// to its place.
// The download goes on from where an earlier run stopped, when the server still has the same contents. The .part
// file replaces the file only once its contents have the hash in the server's list. A file in the folder records
// what the folder has (syncState): each file's size and modification time as the launcher left it, and the hash it
// had then. With that record, a run that has nothing to do reads no game file.

// syncFile is the name of the file in the folder that records what the folder has.
const syncFile = launcherapi.StateFile

type syncState struct {
	Files map[string]syncHas `json:"files"` // by path
	// Parts has the files that a download left unfinished. It maps each file's path to the hash of the contents that
	// its .part file has the start of.
	Parts map[string]string `json:"parts,omitempty"`
}

type syncHas struct {
	Size int64  `json:"size"`
	Time int64  `json:"time"` // its modification time, in nanoseconds
	Hash string `json:"hash"`
}

var syncHash = regexp.MustCompile(`^[0-9a-f]{32}$`)

// stalled is how long a download may receive nothing before the launcher asks again, from where the download is.
const stalled = time.Minute

// syncer is a run of sync.
type syncer struct {
	la     *launcher
	ctx    context.Context // the run's context
	server *server
	of     *title // the title whose game files it downloads
	dir    string
	state  syncState

	got, need int64     // the bytes it has and the bytes it needs, of the files this run downloads
	received  int64     // the bytes the server sent in this run
	said      time.Time // when it last reported how far it is
	saidGot   int64
	// line guards the window's line that shows how far the download is. One write to it runs at a time, and the
	// download waits for none (progress).
	line      sync.Mutex
	reporting chan struct{} // holds a token while no report is being sent
}

// sync makes dir the same as the server's game files of the run's title, which list lists. It downloads the files
// that the folder lacks or has with other contents, and removes the files of an earlier sync that the server no
// longer has. It reports how far it is, in the window and to the server. got reports whether it downloaded anything.
//
// packs names the packs of the list to download (launcherapi.File), or is nil for all of them. The files of the other
// packs stay as the folder has them: maps that the player has not played, or parts of them. zones are the title's
// maps' zones, by which the launcher tells that the folder has a map (hasZone). A map's zone is the last of its pack
// to be downloaded, so that a folder has it only once it has the rest of the map.
func (la *launcher) sync(ctx context.Context, srv *server, dir string, list *launcherapi.Files, packs map[string]bool,
	zones []string) (got bool, err error) {
	t := la.title()
	s := &syncer{la: la, ctx: ctx, server: srv, of: t, dir: dir, reporting: make(chan struct{}, 1)}
	s.reporting <- struct{}{}
	if data, err := os.ReadFile(filepath.Join(dir, syncFile)); err == nil {
		json.Unmarshal(data, &s.state)
	}
	if s.state.Files == nil {
		s.state.Files = map[string]syncHas{}
	}
	if s.state.Parts == nil {
		s.state.Parts = map[string]string{}
	}
	// Check the list, because its paths are used under dir as they are.
	seen := map[string]bool{}
	for _, f := range list.Files {
		if !launcherapi.LocalPath(f.Path) || !syncHash.MatchString(f.Hash) || f.Size < 0 || seen[strings.ToLower(f.Path)] ||
			strings.EqualFold(f.Path, syncFile) || strings.HasSuffix(strings.ToLower(f.Path), launcherapi.PartSuffix) {
			return false, fmt.Errorf("The server's list of game files has a name the launcher won't accept: %q.", f.Path)
		}
		seen[strings.ToLower(f.Path)] = true
		// The launcher accepts a program from a server only when it is one of the game's own (programs.go).
		if isProgram(f.Path) && la.programs[t.id][strings.ToLower(f.Path)] != f.Hash {
			return false, fmt.Errorf("The server's game files include a program that isn't part of %s, which the "+
				"launcher won't download: %q. With the game installed from Steam, nothing needs downloading.", t.name, f.Path)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	// Find the files of the list, of the packs to download, that the folder lacks or has with other contents.
	var todo []launcherapi.File
	unknown, wanted := 0, 0
	for _, f := range list.Files {
		if packs != nil && !packs[f.Pack] {
			continue
		}
		wanted++
		st, err := os.Stat(s.path(f.Path))
		if err != nil || !st.Mode().IsRegular() || st.Size() != f.Size {
			todo = append(todo, f)
			continue
		}
		if has, ok := s.state.Files[f.Path]; ok && has.Size == f.Size && has.Time == st.ModTime().UnixNano() {
			if has.Hash != f.Hash {
				todo = append(todo, f)
			}
			continue
		}
		// The file has the right size, but no sync left it as it is now. It is hashed once.
		if unknown++; unknown == 1 {
			la.say("Checking the game files this PC has...")
		}
		if hash, err := hashFile(ctx, s.path(f.Path)); err != nil {
			return false, err
		} else if hash != f.Hash {
			todo = append(todo, f)
			continue
		}
		s.state.Files[f.Path] = syncHas{f.Size, st.ModTime().UnixNano(), f.Hash}
	}
	// Remove what an earlier sync left that the server no longer has.
	for path := range s.state.Files {
		if !seen[strings.ToLower(path)] && launcherapi.LocalPath(path) {
			os.Remove(s.path(path))
			delete(s.state.Files, path)
		}
	}
	for path := range s.state.Parts {
		if !seen[strings.ToLower(path)] && launcherapi.LocalPath(path) {
			os.Remove(s.path(path) + ".part")
			delete(s.state.Parts, path)
		}
	}
	if err := s.save(); err != nil {
		return false, err
	}
	if len(todo) == 0 {
		return false, nil
	}
	for _, f := range todo {
		s.need += f.Size
	}
	// a map's zone comes last, after the rest of what the run downloads
	last := func(f launcherapi.File) bool { return f.Pack != "" && slices.Contains(zones, f.Path) }
	slices.SortStableFunc(todo, func(a, b launcherapi.File) int {
		switch {
		case last(a) == last(b):
			return 0
		case last(b):
			return -1
		}
		return 1
	})
	if free, ok := freeBytes(dir); ok && uint64(s.need) > free {
		return false, fmt.Errorf("The game files need %.1f GB, but the drive of %s has only %.1f GB free.", float64(s.need)/1e9,
			dir, float64(free)/1e9)
	}
	la.say("Downloading %d of the game's %d files: %s.", len(todo), wanted, megabytes(s.need))
	defer func() {
		// Send the last report, once the report before it has been sent. It shows the server that no download runs
		// now, however this one went.
		<-s.reporting
		srv.report(context.WithoutCancel(ctx), launcherapi.Report{Got: s.got, Need: s.need})
	}()
	for _, f := range todo {
		if err := s.fetch(ctx, f); err != nil {
			s.endLine()
			return true, err
		}
	}
	s.progress(true)
	s.endLine()
	la.say("The game files are complete.")
	return true, nil
}

// endLine ends the window's line that shows how far the download is, once the last write to it is done.
func (s *syncer) endLine() {
	s.line.Lock()
	fmt.Fprintln(s.la.status)
	s.line.Unlock()
}

func (s *syncer) path(rel string) string { return filepath.Join(s.dir, filepath.FromSlash(rel)) }

func (s *syncer) save() error {
	data, _ := json.Marshal(s.state)
	return writeFile(filepath.Join(s.dir, syncFile), data)
}

// synced returns the record that the launcher's syncs left in dir, and the size in bytes of the files that the
// launcher downloaded there. ok is false when dir has no record.
func synced(dir string) (state syncState, bytes int64, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, syncFile))
	if err != nil {
		return state, 0, false
	}
	json.Unmarshal(data, &state)
	for path, has := range state.Files {
		if launcherapi.LocalPath(path) && isFile(filepath.Join(dir, filepath.FromSlash(path))) {
			bytes += has.Size
		}
	}
	for path := range state.Parts {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path)) + launcherapi.PartSuffix); err == nil && launcherapi.LocalPath(path) {
			bytes += st.Size()
		}
	}
	return state, bytes, true
}

// unsync deletes what the launcher downloaded into dir. It deletes the files that the launcher's syncs left there,
// each by name, and the folders that are empty without them, including dir. Anything else in the folder stays,
// because a folder that the player named may have had files of theirs before. left is the number of the launcher's
// files that are still there because Windows wouldn't delete them: a running game has them open.
func unsync(dir string, state syncState) (left int) {
	dir = filepath.Clean(dir)
	folders := map[string]bool{}
	remove := func(path, suffix string) {
		if !launcherapi.LocalPath(path) {
			return
		}
		file := filepath.Join(dir, filepath.FromSlash(path)) + suffix
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left++
		}
		for d := filepath.Dir(file); len(d) > len(dir); d = filepath.Dir(d) {
			folders[d] = true
		}
	}
	for path := range state.Files {
		remove(path, "")
	}
	for path := range state.Parts {
		remove(path, launcherapi.PartSuffix)
	}
	if left > 0 {
		return left // the record of what is there stays, for another try
	}
	os.Remove(filepath.Join(dir, syncFile))
	os.Remove(filepath.Join(dir, syncFile) + ".tmp")
	// Remove the deepest folders first: a folder can be removed once the folders in it are gone.
	deep := slices.SortedFunc(maps.Keys(folders), func(a, b string) int { return len(b) - len(a) })
	for _, d := range append(deep, dir) {
		os.Remove(d) // removes only an empty folder
	}
	return 0
}

func megabytes(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

// progress reports how far the download is, on the window's last line and to the server. It does so at most once a
// second, and always when last is set.
func (s *syncer) progress(last bool) {
	now := time.Now()
	if !last && now.Sub(s.said) < time.Second {
		return
	}
	rate := 0.0
	if !s.said.IsZero() && now.After(s.said) {
		rate = float64(s.got-s.saidGot) * 8 / 1e6 / now.Sub(s.said).Seconds()
	}
	s.said, s.saidGot = now, s.got
	percent := 100.0
	if s.need > 0 {
		percent = float64(s.got) / float64(s.need) * 100
	}
	// The window's line is written on a goroutine of its own, so that a window that is slow to accept a write holds
	// up neither the download nor the report to the server. A console holds what is written to it for as long as its
	// player selects text in it. A line is left out when the line before it is still being written: the next line
	// has newer numbers.
	text := fmt.Sprintf("\r  %s of %s (%.0f%%), %.0f Mbit/s        ", megabytes(s.got), megabytes(s.need), percent, rate)
	if last {
		s.line.Lock()
		fmt.Fprint(s.la.status, text)
		s.line.Unlock()
		return
	}
	if s.line.TryLock() {
		go func() {
			defer s.line.Unlock()
			fmt.Fprint(s.la.status, text)
		}()
	}
	select {
	case <-s.reporting:
		go func(r launcherapi.Report) {
			s.server.report(s.ctx, r)
			s.reporting <- struct{}{}
		}(launcherapi.Report{Running: true, Got: s.got, Need: s.need})
	default: // the last report is still being sent
	}
}

// fetch downloads one file into its place. When the connection fails, it asks again from where the download is, for
// as long as each try receives some of the file.
func (s *syncer) fetch(ctx context.Context, f launcherapi.File) error {
	file := s.path(f.Path)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	for failures, others := 0, 0; ; {
		before := s.received
		err := s.try(ctx, f, file)
		if err == nil {
			return nil
		}
		var fatal fatalError
		if ctx.Err() != nil || errors.As(err, &fatal) {
			return err
		}
		switch {
		case errors.Is(err, errOther):
			// Download the whole file once more, in case the start it had was bad. A file that has other contents
			// twice is not the file in the list.
			if others++; others >= 2 {
				return fmt.Errorf("Downloading %s failed: it doesn't match the server's list.", f.Path)
			}
		case s.received > before:
			failures = 0
		default:
			if failures++; failures >= 5 {
				return fmt.Errorf("Downloading %s failed: %v", f.Path, err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(failures+1) * s.la.retry):
		}
	}
}

// fatalError is an error that a further try would not get past.
type fatalError struct{ error }

// errOther is the error of a download whose contents are not the contents in the server's list.
var errOther = errors.New("not the file the server's list says")

// try makes one request for the file, or for the rest of it.
func (s *syncer) try(ctx context.Context, f launcherapi.File, file string) error {
	part := file + ".part"
	sum := launcherapi.NewHash()
	var have int64
	// An earlier try left the start of the same contents. Hash that start again, because the hash is of the whole
	// file.
	if s.state.Parts[f.Path] == f.Hash {
		if st, err := os.Stat(part); err == nil && st.Mode().IsRegular() && st.Size() <= f.Size {
			in, err := os.Open(part)
			if err == nil {
				have, err = io.Copy(sum, &ctxReader{ctx, in})
				in.Close()
			}
			if err != nil {
				have = 0
				sum = launcherapi.NewHash()
			}
		}
	}
	if s.state.Parts[f.Path] != f.Hash {
		s.state.Parts[f.Path] = f.Hash
		if err := s.save(); err != nil {
			return fatalError{err}
		}
	}
	// counted is what this run counts as got of the file. It includes the start that an earlier run left.
	counted := have
	s.got += have
	undo := func() { s.got -= counted }
	if have < f.Size || f.Size == 0 && !isFile(part) {
		if err := s.request(ctx, f, part, &have, &counted, sum); err != nil {
			undo() // what was received stays in the .part file for the next try, which counts it again
			return err
		}
	}
	if have < f.Size {
		undo()
		return io.ErrUnexpectedEOF // the connection ended early: the next try asks for the rest
	}
	if have != f.Size || launcherapi.HashOf(sum) != f.Hash {
		// The contents are not the contents in the list. Nothing of them is kept, and the next try starts the file
		// again.
		os.Remove(part)
		delete(s.state.Parts, f.Path)
		undo()
		return errOther
	}
	if err := plutonium.Rename(part, file); err != nil {
		undo()
		return fatalError{err}
	}
	st, err := os.Stat(file)
	if err != nil {
		undo()
		return fatalError{err}
	}
	delete(s.state.Parts, f.Path)
	s.state.Files[f.Path] = syncHas{f.Size, st.ModTime().UnixNano(), f.Hash}
	if err := s.save(); err != nil {
		return fatalError{err}
	}
	return nil
}

// request asks the server for the file, from the have bytes that its .part file has, and writes what it receives to
// the .part file. It updates have, counted and sum as it writes. The server sends the rest of the file when it still
// has the contents that the start belongs to (the file's hash is its ETag), and the whole file otherwise.
func (s *syncer) request(ctx context.Context, f launcherapi.File, part string, have, counted *int64, sum hash.Hash) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchdog := time.AfterFunc(stalled, cancel)
	defer watchdog.Stop()
	req, err := s.server.request(ctx, http.MethodGet, "/launcher/"+s.of.id+"/files/"+f.Path, nil)
	if err != nil {
		return fatalError{err}
	}
	if *have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", *have))
		req.Header.Set("If-Range", `"`+f.Hash+`"`)
	}
	rsp, err := s.server.client.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case rsp.StatusCode == http.StatusPartialContent && *have > 0 &&
		strings.HasPrefix(rsp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", *have)):
		flags |= os.O_APPEND
	case rsp.StatusCode == http.StatusOK:
		// The server sends the whole file. It is written from its start, whatever the folder had of it.
		s.got -= *counted
		*have, *counted = 0, 0
		sum.Reset()
		flags |= os.O_TRUNC
	case rsp.StatusCode == http.StatusUnauthorized || rsp.StatusCode == http.StatusForbidden ||
		rsp.StatusCode == http.StatusNotFound || rsp.StatusCode == http.StatusConflict:
		return fatalError{fmt.Errorf("Downloading %s failed: %v", f.Path, failed(rsp))}
	default:
		return failed(rsp)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return fatalError{err}
	}
	defer out.Close()
	body := io.LimitReader(rsp.Body, f.Size-*have+1) // a byte past the file's end shows a file that is too long
	buf := make([]byte, 256<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			watchdog.Reset(stalled)
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fatalError{werr}
			}
			sum.Write(buf[:n])
			*have += int64(n)
			*counted += int64(n)
			s.got += int64(n)
			s.received += int64(n)
			s.progress(false)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if err := out.Close(); err != nil {
		return fatalError{err}
	}
	return nil
}

// ctxReader reads from r until ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// hashFile returns the hash of a file's contents, in the form the server's list has.
func hashFile(ctx context.Context, file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := launcherapi.NewHash()
	if _, err := io.Copy(sum, &ctxReader{ctx, f}); err != nil {
		return "", err
	}
	return launcherapi.HashOf(sum), nil
}
