package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fileHashes has the hash of each game file's contents. A browser's store keeps a file under that hash
// (web/public/store.js): the first 32 hex digits of the SHA-256 of the file's contents (contentHash). The manifest
// gives each file's hash (web.go), and a map's list names the pieces of each file under the file's hash (files.go). A
// browser therefore keeps a piece once, whichever map listed it. A file that changed has another hash, so to a
// browser it is a new file that it has no pieces of yet. Hashing the game files reads all of them (some 7 GB), so the
// hashes are kept in the data directory, in hashes.tsv. Each line is one file: its path on disk, size, modification
// time and hash. A file is hashed again only when its size or time changes, so each file is normally hashed once.
type fileHashes struct {
	file string // the file the hashes are kept in, or "" to keep none (tests)

	mu     sync.Mutex
	known  map[string]knownHash // by path on disk
	loaded bool
}

type knownHash struct {
	size, modTime int64
	hash          string
}

// contentHash returns the hash the store keeps contents under: the first 32 hex digits of sum, their SHA-256.
func contentHash(sum []byte) string {
	return hex.EncodeToString(sum)[:32]
}

func hashFile(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return contentHash(sum.Sum(nil)), nil
}

// fill sets each entry's hash. It uses the kept hash of a file whose size and time still match. It hashes the
// contents of the other files, a few files at a time, and keeps the new hashes. When there is much to hash (over
// 100 MB), it logs how long the hashing took.
func (h *fileHashes) fill(entries []*manifestEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.load()
	var todo []*manifestEntry
	var bytes int64
	for _, e := range entries {
		if k, ok := h.known[e.file]; ok && k.size == e.size && k.modTime == e.modTime {
			e.hash = k.hash
		} else {
			todo = append(todo, e)
			bytes += e.size
		}
	}
	if len(todo) == 0 {
		return
	}
	began := time.Now()
	if bytes > 100<<20 {
		log.Printf("files: hashing %d game files (%.1f GB); this happens once", len(todo),
			float64(bytes)/1e9)
	}
	jobs := make(chan *manifestEntry)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), 4) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				hash, err := hashFile(e.file)
				if err != nil {
					log.Printf("files: %s: %v", e.rel, err)
				}
				e.hash = hash
			}
		}()
	}
	for _, e := range todo {
		jobs <- e
	}
	close(jobs)
	wg.Wait()
	for _, e := range todo {
		if e.hash != "" {
			h.known[e.file] = knownHash{e.size, e.modTime, e.hash}
		}
	}
	if bytes > 100<<20 {
		log.Printf("files: hashed %d files in %s", len(todo), time.Since(began).Round(time.Second))
	}
	h.save()
}

// load reads the kept hashes from the file, the first time it is called. The caller holds h.mu.
func (h *fileHashes) load() {
	if h.loaded {
		return
	}
	h.loaded = true
	h.known = map[string]knownHash{}
	if h.file == "" {
		return
	}
	f, err := os.Open(h.file)
	if err != nil {
		return
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		fields := strings.Split(lines.Text(), "\t")
		if len(fields) != 4 || len(fields[3]) != 32 {
			continue
		}
		size, err1 := strconv.ParseInt(fields[1], 10, 64)
		modTime, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 == nil && err2 == nil {
			h.known[fields[0]] = knownHash{size, modTime, fields[3]}
		}
	}
}

// save rewrites the file with all the kept hashes. The caller holds h.mu.
func (h *fileHashes) save() {
	if h.file == "" {
		return
	}
	var text strings.Builder
	for file, k := range h.known {
		fmt.Fprintf(&text, "%s\t%d\t%d\t%s\n", file, k.size, k.modTime, k.hash)
	}
	tmp := h.file + ".tmp"
	if err := os.MkdirAll(filepath.Dir(h.file), 0o755); err == nil {
		if err = os.WriteFile(tmp, []byte(text.String()), 0o644); err == nil {
			err = os.Rename(tmp, h.file)
		}
		if err == nil {
			return
		}
		log.Printf("files: the game files' hashes could not be kept: %v", err)
	}
}

// pieceSums has the hash of each piece's contents, which a map's list gives in listFile.Sums (files.go): the
// contentHash of the piece's bytes. A browser can take the pieces from the player's own copy of the game instead of
// downloading them from the server (web/public/store.js: importing). It checks each such piece against the piece's
// hash. A copy of another version or language of the game has other bytes at a path that the list names. The browser
// does not keep those bytes and leaves the download to the server. Hashing the pieces reads every piece of every list
// (3.7 GB for the eleven maps), so the hashes are kept in the data directory too, in pieces.tsv. Each line is one
// piece: its file's hash, its start and end, and its own hash. A piece is known by the hash of its file's contents, so
// each piece is hashed once.
type pieceSums struct {
	file string // the file the hashes are kept in, or "" to keep none (tests)

	mu     sync.Mutex
	known  map[pieceKey]string
	loaded bool
}

type pieceKey struct {
	hash       string // the hash of the piece's file
	start, end int64
}

// fill sets the hash of each piece of the list (listFile.Sums). It uses the kept hash of a piece when there is one.
// It hashes the contents of the other pieces, a few files at a time, and keeps the new hashes. A piece that could not
// be read gets "", which matches no piece that a browser reads. When there is much to hash (over 100 MB), fill logs
// how long the hashing took.
func (s *pieceSums) fill(l *fileList) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	var todo []int // the indexes of the list's files that have a piece with no kept hash
	var bytes int64
	for i := range l.Files {
		lf := &l.Files[i]
		lf.Sums = make([]string, len(lf.Pieces))
		known := true
		for j, p := range lf.Pieces {
			if sum, ok := s.known[pieceKey{lf.Hash, p[0], p[1]}]; ok {
				lf.Sums[j] = sum
			} else {
				known = false
				bytes += p[1] - p[0]
			}
		}
		if !known {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return
	}
	began := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), 4) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lf := &l.Files[i]
				f, err := os.Open(l.disk[i])
				if err != nil {
					log.Printf("files: %s: %v", lf.Path, err)
					continue
				}
				for j, p := range lf.Pieces {
					if lf.Sums[j] != "" {
						continue
					}
					sum := sha256.New()
					if _, err := io.Copy(sum, io.NewSectionReader(f, p[0], p[1]-p[0])); err != nil {
						log.Printf("files: %s: %v", lf.Path, err)
						break
					}
					lf.Sums[j] = contentHash(sum.Sum(nil))
				}
				f.Close()
			}
		}()
	}
	for _, i := range todo {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var lines strings.Builder
	for _, i := range todo {
		lf := &l.Files[i]
		for j, p := range lf.Pieces {
			key := pieceKey{lf.Hash, p[0], p[1]}
			if _, ok := s.known[key]; !ok && lf.Sums[j] != "" {
				s.known[key] = lf.Sums[j]
				fmt.Fprintf(&lines, "%s\t%d\t%d\t%s\n", key.hash, key.start, key.end, lf.Sums[j])
			}
		}
	}
	if bytes > 100<<20 {
		log.Printf("files: hashed the pieces of %s's list (%.1f GB) in %s, which a browser importing them from an "+
			"install of its own checks them by: once", l.Map, float64(bytes)/1e9, time.Since(began).Round(time.Second))
	}
	s.save(lines.String())
}

// load reads the kept hashes from the file, the first time it is called. The caller holds s.mu.
func (s *pieceSums) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.known = map[pieceKey]string{}
	if s.file == "" {
		return
	}
	f, err := os.Open(s.file)
	if err != nil {
		return
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		fields := strings.Split(lines.Text(), "\t")
		if len(fields) != 4 || len(fields[3]) != 32 {
			continue // for example a line that was cut short when it was written
		}
		start, err1 := strconv.ParseInt(fields[1], 10, 64)
		end, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 == nil && err2 == nil {
			s.known[pieceKey{fields[0], start, end}] = fields[3]
		}
	}
}

// save appends the lines of the new hashes to the file. The caller holds s.mu. The kept lines are never rewritten: a
// line names its piece by the hash of the file's contents, so the line's hash never changes.
func (s *pieceSums) save(lines string) {
	if s.file == "" || lines == "" {
		return
	}
	err := os.MkdirAll(filepath.Dir(s.file), 0o755)
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(s.file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	}
	if err == nil {
		_, err = f.WriteString(lines)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		log.Printf("files: the pieces' hashes could not be kept: %v", err)
	}
}
