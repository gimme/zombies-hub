package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// an archive of the install: a zip with the entries, deflated as the image archives are (store: as the sound ones)
func writeArchive(t *testing.T, file string, entries map[string]string, store bool) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.SetComment("an archive")
	for name, data := range entries {
		method := zip.Deflate
		if store {
			method = zip.Store
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(data))
	}
	w.Close()
	writeFile(t, file, buf.String())
}

// noise is n bytes that deflate can't squeeze, so that an archive's entries are as big as their data
func noise(n int, seed byte) string {
	b := make([]byte, n)
	x := uint32(seed) + 1
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return string(b)
}

func TestFileList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zone", "Common", "Zombie_Theater.ff"), noise(pieceMax+10, 1))
	writeFile(t, filepath.Join(dir, "zone", "English", "en_zombie_theater.ff"), "its strings")
	writeFile(t, filepath.Join(dir, "localization.txt"), "english")
	writeArchive(t, filepath.Join(dir, "main", "iw_01.iwd"), map[string]string{
		"images/a.iwi": noise(3000, 2), "images/b.iwi": noise(3000, 3), "images/d.iwi": noise(3000, 4),
		"images/e.iwi": noise(pieceMax+5, 5)}, false)
	// a localized archive's images are named as any (its sounds have the language's folder, as the zones name them)
	writeArchive(t, filepath.Join(dir, "main", "localized_English_iw00.iwd"), map[string]string{
		"images/c.iwi": noise(2000, 6), "english/sound/x.wav": noise(2000, 7), "english/sound/y.wav": noise(2000, 8)}, true)
	writeArchive(t, filepath.Join(dir, "main", "iw_00.iwd"), map[string]string{"accuracy/x.accu": "table"}, false)
	// the videos: the map's own and default.bik are read, the other maps' are not, and the campaign's are not even in the
	// manifest
	for _, v := range []string{"zombie_theater_load.bik", "zombie_theater_outro.bik", "default.bik", "zombie_theater2_load.bik",
		"zombie_pentagon_load.bik", "treyarch.bik"} {
		writeFile(t, filepath.Join(dir, "main", "video", v), noise(1000, 9))
	}
	h := &web{gameDir: dir}
	files, version := h.files()
	if _, ok := files["main/video/treyarch.bik"]; ok {
		t.Errorf("the campaign's video treyarch.bik is in the manifest")
	}
	if _, ok := files["main/video/zombie_pentagon_load.bik"]; !ok {
		t.Errorf("another Zombies map's video is not in the manifest")
	}
	zones := []string{"zombie_theater", "en_zombie_theater", "nope"}
	names := []string{"images/A.iwi", "images\\c.iwi", "english\\sound\\x.wav", "images/missing.iwi", "images/a.iwi",
		"images/e.iwi", "sound/y.wav"}
	f := newFileLists(map[string]*mapReads{"zombie_theater": {Map: "zombie_theater", Zones: zones, Names: names}}, h.files)
	l := f.resolve("zombie_theater", version, files, zones, names)
	if l.Version != version || l.Map != "zombie_theater" {
		t.Errorf("version %s, map %s", l.Version, l.Map)
	}
	// sound/y.wav: the archive has english/sound/y.wav, which the engine doesn't take for it
	if want := []string{"images/missing.iwi", "nope.ff", "sound/y.wav"}; strings.Join(l.Missing, ",") != strings.Join(want, ",") {
		t.Errorf("missing %v, want %v", l.Missing, want)
	}
	byPath := map[string]listFile{}
	var bytesListed int64
	for _, lf := range l.Files {
		byPath[lf.Path] = lf
		if lf.Hash != files[strings.ToLower(lf.Path)].hash || len(lf.Hash) != 32 {
			t.Errorf("%s: hash %q, want the manifest's", lf.Path, lf.Hash)
		}
		data, _ := os.ReadFile(filepath.Join(dir, lf.Path))
		if len(lf.Sums) != len(lf.Pieces) {
			t.Errorf("%s: %d sums of %d pieces", lf.Path, len(lf.Sums), len(lf.Pieces))
			continue
		}
		for i, p := range lf.Pieces {
			bytesListed += p[1] - p[0]
			if p[1] <= p[0] || p[1]-p[0] > pieceMax || p[1] > lf.Size || i > 0 && p[0] < lf.Pieces[i-1][0] {
				t.Errorf("%s: piece %d %v, of %v", lf.Path, i, p, lf.Pieces)
			}
			// each piece's hash, which a browser importing it checks it by
			if sum := sha256.Sum256(data[p[0]:p[1]]); lf.Sums[i] != contentHash(sum[:]) {
				t.Errorf("%s: piece %d %v: sum %q, want its bytes' %s", lf.Path, i, p, lf.Sums[i], contentHash(sum[:]))
			}
		}
	}
	if bytesListed != l.Bytes {
		t.Errorf("bytes %d, the pieces sum to %d", l.Bytes, bytesListed)
	}
	// the fastfiles first, as a browser downloads the list
	for i, lf := range l.Files {
		if ff := strings.HasSuffix(lf.Path, ".ff"); ff != (i < 2) {
			t.Errorf("file %d of the list is %s, want the 2 fastfiles first", i, lf.Path)
		}
	}
	// whole, in pieces of pieceMax at most: the fastfiles, the archive without images or sounds, the map's videos and
	// default.bik, the install's other files
	for _, p := range []string{"zone/Common/Zombie_Theater.ff", "zone/English/en_zombie_theater.ff", "main/iw_00.iwd", "localization.txt",
		"main/video/zombie_theater_load.bik", "main/video/zombie_theater_outro.bik", "main/video/default.bik"} {
		lf, ok := byPath[p]
		var at int64
		for _, piece := range lf.Pieces {
			if piece[0] != at {
				break
			}
			at = piece[1]
		}
		if !ok || at != lf.Size {
			t.Errorf("%s: %+v, want it whole", p, lf.Pieces)
		}
	}
	if n := len(byPath["zone/Common/Zombie_Theater.ff"].Pieces); n != 2 {
		t.Errorf("a fastfile of pieceMax and a bit is in %d pieces, want 2", n)
	}
	for _, p := range []string{"main/video/zombie_theater2_load.bik", "main/video/zombie_pentagon_load.bik", "main/video/treyarch.bik"} {
		if _, ok := byPath[p]; ok {
			t.Errorf("%s, another map's or the campaign's video, is in the list", p)
		}
	}
	// the archives' entries named: each is in pieces of its own, with its local header and all of its data, to the next
	// entry's header; no other entry's header is in one, but for the end of the archive, where the engine looks for its
	// central directory first, which the last piece holds, to the end
	check := func(p string, wanted ...string) {
		t.Helper()
		lf, ok := byPath[p]
		if !ok || len(lf.Pieces) == 0 {
			t.Errorf("%s: %+v, want pieces", p, lf)
			return
		}
		t.Logf("%s: %v", p, lf.Pieces)
		data, _ := os.ReadFile(filepath.Join(dir, p))
		size := int64(len(data))
		covered := func(from, to int64) bool {
			for at := from; at < to; {
				i := slices.IndexFunc(lf.Pieces, func(piece [2]int64) bool { return piece[0] <= at && at < piece[1] })
				if i < 0 {
					return false
				}
				at = lf.Pieces[i][1]
			}
			return true
		}
		z, err := zip.OpenReader(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		defer z.Close()
		for _, zf := range z.File {
			dataAt, _ := zf.DataOffset()
			header := int64(bytes.LastIndex(data[:dataAt], []byte("PK\x03\x04")))
			end := dataAt + int64(zf.CompressedSize64)
			starts := slices.ContainsFunc(lf.Pieces, func(piece [2]int64) bool { return piece[0] == header })
			if slices.Contains(wanted, zf.Name) {
				if !starts || !covered(header, end) {
					t.Errorf("%s: %s's entry (header at %d, data to %d) is not in pieces of its own", p, zf.Name, header, end)
				}
			} else if covered(header, header+1) && header < size-centralDirSearch {
				t.Errorf("%s: %s's entry (header at %d), not named, is in a piece", p, zf.Name, header)
			}
		}
		directory, _, err := readZipDirectory(filepath.Join(dir, p), size)
		if err != nil {
			t.Fatal(err)
		}
		// (the piece of the end may overlap an entry's last piece, which then comes after it)
		if !covered(min(directory, size-centralDirSearch), size) ||
			!slices.ContainsFunc(lf.Pieces, func(piece [2]int64) bool { return piece[1] == size }) ||
			!bytes.HasPrefix(data[directory:], []byte("PK\x01\x02")) {
			t.Errorf("%s: the pieces %v do not hold its central directory (at %d) to its end", p, lf.Pieces, directory)
		}
	}
	check("main/iw_01.iwd", "images/a.iwi", "images/e.iwi")
	check("main/localized_English_iw00.iwd", "images/c.iwi", "english/sound/x.wav")

	// made from the map's reads once at a version, and kept; none for a map with no reads
	if got := f.lookup("zombie_theater"); got == nil || got.Bytes != l.Bytes || f.lookup("zombie_theater") != got {
		t.Errorf("lookup: %+v, want %d bytes, the same list again", got, l.Bytes)
	}
	if got := f.lookup("zombie_pentagon"); got != nil {
		t.Errorf("a list of a map with no reads: %+v", got)
	}
}

// Of a name in several archives, the list has the copy the engine reads: the first in the order it searches them, the
// archives that aren't localized first, the last by name first, then the localized ones, the last by name first.
func TestFileListTakesTheCopyTheEngineReads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zone", "Common", "zombie_theater.ff"), "the map")
	writeArchive(t, filepath.Join(dir, "main", "iw_01.iwd"), map[string]string{
		"images/a.iwi": noise(3000, 1), "images/b.iwi": noise(3000, 2)}, true)
	writeArchive(t, filepath.Join(dir, "main", "iw_02.iwd"), map[string]string{"images/a.iwi": noise(3000, 3)}, true)
	writeArchive(t, filepath.Join(dir, "main", "localized_English_iw00.iwd"), map[string]string{
		"images/b.iwi": noise(3000, 4), "english/sound/c.wav": noise(3000, 5)}, true)
	writeArchive(t, filepath.Join(dir, "main", "localized_English_iw01.iwd"), map[string]string{
		"english/sound/c.wav": noise(3000, 6)}, true)
	h := &web{gameDir: dir}
	files, version := h.files()
	l := newFileLists(nil, h.files).resolve("zombie_theater", version, files, []string{"zombie_theater"},
		[]string{"images/a.iwi", "images/b.iwi", "english\\sound\\c.wav"})
	// the entries listed, by archive: the pieces before its central directory's
	entries := map[string][]string{}
	for _, lf := range l.Files {
		if !strings.HasSuffix(lf.Path, ".iwd") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(dir, lf.Path))
		directory, _, _ := readZipDirectory(filepath.Join(dir, lf.Path), lf.Size)
		for _, p := range lf.Pieces {
			if p[0] < directory && p[0] < lf.Size-centralDirSearch {
				header := data[p[0]:p[1]]
				nameLen := int(header[26]) | int(header[27])<<8
				entries[lf.Path] = append(entries[lf.Path], string(header[30:30+nameLen]))
			}
		}
	}
	// a: iw_02's, not iw_01's; b: iw_01's, not the localized archive's; c: the last localized archive's
	want := map[string][]string{"main/iw_01.iwd": {"images/b.iwi"}, "main/iw_02.iwd": {"images/a.iwi"},
		"main/localized_English_iw01.iwd": {"english/sound/c.wav"}}
	if fmt.Sprint(entries) != fmt.Sprint(want) {
		t.Errorf("the entries listed, by archive: %v, want %v", entries, want)
	}
}

// Two maps' lists share the pieces of what both read: a browser keeps each once.
func TestFileListsSharePieces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zone", "Common", "common_zombie.ff"), "both")
	writeArchive(t, filepath.Join(dir, "main", "iw_01.iwd"), map[string]string{
		"images/a.iwi": noise(3000, 1), "images/b.iwi": noise(3000, 2), "images/c.iwi": noise(3000, 3)}, true)
	h := &web{gameDir: dir}
	files, version := h.files()
	f := newFileLists(nil, h.files)
	one := f.resolve("one", version, files, []string{"common_zombie"}, []string{"images/a.iwi", "images/b.iwi"})
	two := f.resolve("two", version, files, []string{"common_zombie"}, []string{"images/b.iwi", "images/c.iwi"})
	pieces := func(l *fileList) map[[2]int64]bool {
		out := map[[2]int64]bool{}
		for _, lf := range l.Files {
			if lf.Path == "main/iw_01.iwd" {
				for _, p := range lf.Pieces {
					out[p] = true
				}
			}
		}
		return out
	}
	shared := 0
	for p := range pieces(one) {
		if pieces(two)[p] {
			shared++
		}
	}
	// b's entry, and the archive's directory
	if shared != 2 {
		t.Errorf("the lists share %d pieces of the archive, want 2: %v and %v", shared, pieces(one), pieces(two))
	}
}

// A browser downloads the pieces of a map's list it lacks, as one stream, and the log says how it went.
func TestPack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zone", "Common", "zombie_theater.ff"), noise(pieceMax+100, 1))
	writeArchive(t, filepath.Join(dir, "main", "iw_01.iwd"), map[string]string{
		"images/a.iwi": noise(3000, 2), "images/b.iwi": noise(3000, 3)}, true)
	l := testLobby(t)
	h := &web{gameDir: dir, lobby: l, downloads: true}
	l.maps = installedMaps(h.paths)
	l.files = newFileLists(map[string]*mapReads{"zombie_theater": {Map: "zombie_theater", Zones: []string{"zombie_theater"},
		Names: []string{"images/a.iwi"}}}, h.files)
	srv := httptest.NewServer(h)
	defer srv.Close()
	listRsp, body := get(t, srv.URL+"/maps/zombie_theater/files")
	var list fileList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("the list: %v: %s", err, body)
	}
	// asked again with the list it has, a browser has a 304
	etag := listRsp.Header.Get("ETag")
	if rsp, rest := get(t, srv.URL+"/maps/zombie_theater/files", "If-None-Match", etag); etag == "" ||
		rsp.StatusCode != http.StatusNotModified || rest != "" {
		t.Errorf("the list asked again with its ETag %s: %s, %d bytes", etag, rsp.Status, len(rest))
	}
	type piece struct {
		path       string
		start, end int64
	}
	var all []piece
	for _, lf := range list.Files {
		for _, p := range lf.Pieces {
			all = append(all, piece{lf.Path, p[0], p[1]})
		}
	}
	if len(all) < 4 {
		t.Fatalf("the list's pieces: %v", all)
	}
	pack := func(version, want string) (*http.Response, string) {
		t.Helper()
		rsp, err := http.Post(srv.URL+"/maps/zombie_theater/pack", "application/json",
			strings.NewReader(`{"version":"`+version+`","want":`+want+`}`))
		if err != nil {
			t.Fatal(err)
		}
		defer rsp.Body.Close()
		data, _ := io.ReadAll(rsp.Body)
		return rsp, string(data)
	}
	// the pieces asked for, in the order asked
	rsp, got := pack(list.Version, fmt.Sprintf("[[%d,%d],[0,1]]", len(all)-2, len(all)))
	var want strings.Builder
	for _, p := range append(all[len(all)-2:], all[0]) {
		data, _ := os.ReadFile(filepath.Join(dir, p.path))
		want.WriteString(string(data[p.start:p.end]))
	}
	if rsp.StatusCode != http.StatusOK || got != want.String() || rsp.Header.Get("Content-Length") != strconv.Itoa(want.Len()) {
		t.Errorf("the pack: %s, %d bytes (Content-Length %s), want %d", rsp.Status, len(got), rsp.Header.Get("Content-Length"), want.Len())
	}
	for _, c := range []struct {
		version, want string
		status        int
	}{
		{"v0000000000000000", "[[0,1]]", http.StatusConflict},
		{list.Version, fmt.Sprintf("[[0,%d]]", len(all)+1), http.StatusBadRequest},
		{list.Version, "[[1,1]]", http.StatusBadRequest},
	} {
		if rsp, _ := pack(c.version, c.want); rsp.StatusCode != c.status {
			t.Errorf("version %s, want %s: %s, want %d", c.version, c.want, rsp.Status, c.status)
		}
	}
	// a server that sends no game files
	h.downloads = false
	if rsp, _ := pack(list.Version, "[[0,1]]"); rsp.StatusCode != http.StatusForbidden {
		t.Errorf("a pack from a server with downloads off: %s, want 403", rsp.Status)
	}
	// the game files changed: the list, asked again with the one before, is the new one
	writeFile(t, filepath.Join(dir, "zone", "Common", "zombie_theater.ff"), noise(pieceMax+200, 4))
	get(t, srv.URL+"/game/manifest.txt")
	if rsp, rest := get(t, srv.URL+"/maps/zombie_theater/files", "If-None-Match", etag); rsp.StatusCode != http.StatusOK ||
		rsp.Header.Get("ETag") == etag || !strings.Contains(rest, `"size":`+strconv.Itoa(pieceMax+200)) {
		t.Errorf("the list asked again once the game files changed: %s, ETag %s", rsp.Status, rsp.Header.Get("ETag"))
	}
}

// The pieces' hashes are kept, by their file's contents: a server started again hashes none anew.
func TestPieceSumsKept(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "zone", "Common", "zombie_theater.ff"), noise(pieceMax+100, 1))
	kept := filepath.Join(t.TempDir(), "config", "pieces.tsv")
	h := &web{gameDir: dir}
	list := func() *fileList {
		t.Helper()
		files, version := h.files()
		f := newFileLists(nil, h.files)
		f.sums.file = kept
		return f.resolve("zombie_theater", version, files, []string{"zombie_theater"}, nil)
	}
	first := list()
	if len(first.Files) != 1 || len(first.Files[0].Sums) != 2 {
		t.Fatalf("the list: %+v", first.Files)
	}
	// the file as it was, for the sums kept to say what it was: hashed anew, its pieces would have its new bytes' sums
	writeFile(t, filepath.Join(dir, "zone", "Common", "zombie_theater.ff"), noise(pieceMax+100, 2))
	again := list()
	if strings.Join(again.Files[0].Sums, ",") != strings.Join(first.Files[0].Sums, ",") {
		t.Errorf("the sums of a list made again: %v, want those kept, %v", again.Files[0].Sums, first.Files[0].Sums)
	}
	text, _ := os.ReadFile(kept)
	if n := strings.Count(string(text), "\n"); n != 2 {
		t.Errorf("pieces.tsv has %d lines, want 2 (a piece each, kept once):\n%s", n, text)
	}
}

// The pacer holds the downloads at once to its rate in all, and has them take turns.
func TestPacer(t *testing.T) {
	p := &pacer{rate: 20 * packBlock} // a block every 50 ms
	began := time.Now()
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for n := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				p.wait(context.Background(), packBlock)
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// 10 blocks: the first at once, the last 9 slots later
	if took := time.Since(began); took < 400*time.Millisecond || took > 2*time.Second {
		t.Errorf("10 blocks at 20 a second took %s, want 450 ms", took)
	}
	// turns: neither had two more than the other at any time
	ahead := 0
	for _, n := range order {
		ahead += 2*n - 1
		if ahead > 2 || ahead < -2 {
			t.Errorf("the turns: %v, want them shared", order)
			break
		}
	}
	// no rate: no wait; a cancelled wait ends
	if err := (&pacer{}).wait(context.Background(), packBlock); err != nil {
		t.Error(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := &pacer{rate: 1}
	slow.wait(context.Background(), packBlock)
	if err := slow.wait(ctx, packBlock); err == nil {
		t.Error("a wait cancelled went on")
	}
}

func TestParseRate(t *testing.T) {
	for _, c := range []struct {
		in   string
		on   bool
		rate int64
		err  bool
	}{
		{"", false, 0, false}, {"off", false, 0, false}, {"unlimited", true, 0, false}, {"Unlimited", true, 0, false},
		{"40M", true, 5_000_000, false}, {"500k", true, 62_500, false}, {"1G", true, 125_000_000, false},
		{"8000", true, 1000, false}, {"1.5M", true, 187_500, false}, {"fast", false, 0, true}, {"-5M", false, 0, true},
	} {
		on, rate, err := parseRate(c.in)
		if on != c.on || rate != c.rate || (err != nil) != c.err {
			t.Errorf("parseRate(%q): %v %d %v, want %v %d (an error: %v)", c.in, on, rate, err, c.on, c.rate, c.err)
		}
	}
}

// reads/, which the server comes with: what the game that lists each map said, its names sorted
func TestCommittedReads(t *testing.T) {
	reads, err := committedReads()
	if err != nil {
		t.Fatal(err)
	}
	r := reads["zombie_theater"]
	if r == nil || !slices.Contains(r.Zones, "zombie_theater") || len(r.Names) == 0 || !slices.IsSorted(r.Names) {
		t.Errorf("zombie_theater's reads: %v", r)
	}
}
