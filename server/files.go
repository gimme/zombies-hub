package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The lists of the game files that each map's game reads. The web client downloads those files before its game
// starts, and keeps them. The engine in a browser reads the game files only from the browser's store
// (web/public/store.js).
//
// What a map's game reads is recorded in a file that comes with the server, reads/<map>.json. docker/list-files.sh
// makes these files with zombies-server list-files (listFilesCommand). A reads file has the zones the map loads, each
// image whose pixels are in an .iwi file in the archives (main/*.iwd, which are zips), and each sound that is
// streamed from a .wav file in the archives. A game that loads the map with +set bo1_files 1 prints them
// (bo1-zombies/src/server_mp/sv_files_mp.cpp).
//
// From a reads file the server makes the map's list: the pieces of the game files that the game reads, as the store
// keeps them. The pieces are:
//
//   - The fastfiles, whole.
//   - The entry of each image and sound in its archive, as the archive stores it (compressed, for the engine to
//     inflate), from the entry's local header to the next entry's local header.
//   - Each archive's central directory, and the end of the archive, where the engine searches for the directory
//     (bo1-zombies/src/qcommon/unzip.cpp: unzlocal_SearchCentralDir). The engine reads both when it starts, whatever
//     the map.
//   - The map's videos, whole (main/video/). They are its loading movie, <map>_load.bik, and its other videos
//     (zombie_coast_outro.bik), which the client plays (bo1-zombies/src/client/cl_cin.cpp), and default.bik, which
//     the engine plays when a movie can't be opened.
//   - The other game files, whole (configs, localization). They are few and small.
//
// A piece is at most pieceMax bytes. A fastfile or a large entry is cut into pieces of that size from its start. A
// name that the game files lack is listed as missing. The list has every image and sound of every zone that the map's
// game loads, not only what one game read. A browser that has everything on the list can therefore play the map, also
// offline. A piece is the same in every map's list that has it, so a browser keeps each piece once, and a second map
// downloads only what the first map lacked. The list is made again for each version of the game files. It is served
// at /maps/<map>/files (serveFileList). The pieces that a browser lacks come from /maps/<map>/pack (servePack), or
// from the player's own copy of the game. The browser checks each piece of that copy against the piece's hash in the
// list (Sums, hashes.go: pieceSums) before it keeps the piece. A map with no reads/ file has no list, and a browser
// can't play it.

// pieceMax is the largest size of a piece, in bytes (above).
const pieceMax = 8 << 20

type listFile struct {
	Path   string     `json:"path"` // the file's path in the game files, as in the manifest (web.go)
	Hash   string     `json:"hash"` // the hash of its contents (hashes.go), which the store keeps its pieces under
	Size   int64      `json:"size"`
	Pieces [][2]int64 `json:"pieces"` // [start, end) byte ranges, sorted by start; the store keeps each range whole
	Sums   []string   `json:"sums"`   // the hash of each piece's contents (hashes.go: pieceSums), to check an import
}

type fileList struct {
	Version string     `json:"version"` // the version of the game files (web.go: manifestVersion)
	Map     string     `json:"map"`
	Bytes   int64      `json:"bytes"` // the total size of the pieces
	Files   []listFile `json:"files"`
	Missing []string   `json:"missing,omitempty"` // the names the game listed that the game files lack

	disk []string // each file's path on disk, for servePack
	body []byte   // the list as served (serveFileList)
	etag string   // the hash of body, with which a browser asks whether its copy of the list is current
}

// mapReads is the contents of reads/<map>.json: what a map's game reads, as printed by the game that lists it
// (listFiles).
type mapReads struct {
	Map   string   `json:"map"`
	Zones []string `json:"zones"` // the names of the fastfiles it loads, in the order it loads them
	Names []string `json:"names"` // the images and sounds of those zones that are in the archives, sorted
}

//go:embed reads/*.json
var readsDir embed.FS

// committedReads returns the embedded reads/ files, by map.
func committedReads() (map[string]*mapReads, error) {
	entries, err := readsDir.ReadDir("reads")
	if err != nil {
		return nil, err
	}
	reads := map[string]*mapReads{}
	for _, e := range entries {
		data, err := readsDir.ReadFile("reads/" + e.Name())
		if err != nil {
			return nil, err
		}
		var r mapReads
		if err := json.Unmarshal(data, &r); err != nil || r.Map+".json" != e.Name() {
			return nil, fmt.Errorf("reads/%s: not what %s reads (%v)", e.Name(), strings.TrimSuffix(e.Name(), ".json"), err)
		}
		reads[r.Map] = &r
	}
	return reads, nil
}

// fileLists makes the maps' lists and keeps each list until the version of the game files changes.
type fileLists struct {
	reads    map[string]*mapReads                      // by map (reads/)
	manifest func() (map[string]manifestEntry, string) // returns the game files and their version (web.go: files)
	sums     pieceSums                                 // the pieces' hashes (hashes.go)

	mu      sync.Mutex
	lists   map[string]*fileList     // by map, each at the version it was made at
	indexes map[string]*archiveIndex // by version
}

func newFileLists(reads map[string]*mapReads, manifest func() (map[string]manifestEntry, string)) *fileLists {
	return &fileLists{reads: reads, manifest: manifest, lists: map[string]*fileList{},
		indexes: map[string]*archiveIndex{}}
}

// lookup returns the map's list for the current version of the game files. It makes the list the first time the
// list is asked for at a version. It returns nil for a map with no reads/ file.
func (f *fileLists) lookup(mapName string) *fileList {
	r := f.reads[mapName]
	if r == nil {
		return nil
	}
	files, version := f.manifest()
	f.mu.Lock()
	l := f.lists[mapName]
	f.mu.Unlock()
	if l != nil && l.Version == version {
		return l
	}
	l = f.resolve(mapName, version, files, r.Zones, r.Names)
	l.body, _ = json.Marshal(l)
	sum := sha256.Sum256(l.body)
	l.etag = `"` + contentHash(sum[:]) + `"`
	log.Printf("files: %s reads %d MB of the install: %d files, %d of its names not in the install",
		mapName, l.Bytes/1e6, len(l.Files), len(l.Missing))
	f.mu.Lock()
	f.lists[mapName] = l
	f.mu.Unlock()
	return l
}

// resolve makes the map's list from what the game printed: the zones it loaded and the names it listed.
func (f *fileLists) resolve(mapName, version string, files map[string]manifestEntry, zones, names []string) *fileList {
	index := f.index(version, files)
	pieces := map[string][][2]int64{} // by lowercase path
	whole := func(key string) { pieces[key] = cut(nil, 0, files[key].size) }
	var missing []string
	// The fastfiles, from every zone directory that has them (zone/Common, and zone/English for the localized ones).
	for _, zone := range zones {
		suffix := "/" + strings.ToLower(zone) + ".ff"
		found := false
		for key := range files {
			if strings.HasPrefix(key, "zone/") && strings.HasSuffix(key, suffix) {
				whole(key)
				found = true
			}
		}
		if !found {
			missing = append(missing, zone+".ff")
		}
	}
	// The images and sounds, each from the archive that the engine reads it from (read).
	for _, name := range names {
		entries := index.entries[strings.ToLower(strings.ReplaceAll(name, "\\", "/"))]
		if len(entries) == 0 {
			missing = append(missing, name)
			continue
		}
		for _, e := range read(entries) {
			pieces[e.archive] = cut(pieces[e.archive], e.start, e.end)
		}
	}
	// The rest. Every archive's central directory, which the engine reads when it starts, with the end of the archive,
	// where the engine searches for the directory. The archives without images and sounds, whole (iw_00.iwd: configs,
	// accuracy tables, scripts). The map's own videos and the video the engine falls back on, whole. The other game
	// files, whole.
	for key, e := range files {
		switch strings.ToLower(path.Ext(key)) {
		case ".iwd":
			if a := index.archives[key]; a != nil && !a.media {
				whole(key)
			} else if a != nil {
				pieces[key] = cut(pieces[key], min(a.directory, max(0, e.size-centralDirSearch)), e.size)
			}
		case ".ff":
		case ".bik":
			if video, ok := strings.CutPrefix(key, "main/video/"); ok &&
				(video == "default.bik" || strings.HasPrefix(video, strings.ToLower(mapName)+"_")) {
				whole(key)
			}
		default:
			whole(key)
		}
	}
	l := &fileList{Version: version, Map: mapName}
	for key, ps := range pieces {
		e := files[key]
		slices.SortFunc(ps, func(a, b [2]int64) int { return cmp.Compare(a[0], b[0]) })
		ps = slices.Compact(ps)
		l.Files = append(l.Files, listFile{Path: e.rel, Hash: e.hash, Size: e.size, Pieces: ps})
		for _, p := range ps {
			l.Bytes += p[1] - p[0]
		}
	}
	sortFiles(l.Files)
	for _, lf := range l.Files {
		l.disk = append(l.disk, files[strings.ToLower(lf.Path)].file)
	}
	slices.Sort(missing)
	l.Missing = slices.Compact(missing)
	f.sums.fill(l)
	return l
}

// read returns the one entry that the engine reads for a name, out of the entries of that name in all the archives.
// It is the first entry in the order the engine searches the archives, which the engine's log prints at start (path,
// bo1_verbose 2). The engine first searches the archives that aren't localized, the last by name first. It then
// searches the archives of the player's language, again the last by name first. In
// bo1-zombies/src/universal/com_files.cpp, FS_AddIwdFilesForGameDirectory adds the archives sorted (iwdsort),
// FS_AddSearchPath puts each before the others of its kind, and FS_FOpenFileReadForThread takes the first archive
// that has the name.
func read(entries []archiveEntry) []archiveEntry {
	localized := func(e archiveEntry) bool { return strings.HasPrefix(path.Base(e.archive), "localized_") }
	first := -1
	for i, e := range entries {
		if first < 0 || localized(entries[first]) && !localized(e) ||
			localized(entries[first]) == localized(e) && e.archive > entries[first].archive {
			first = i
		}
	}
	if first < 0 {
		return nil
	}
	return entries[first : first+1]
}

// centralDirSearch is the number of bytes at an archive's end that the engine reads first when it looks for the end
// of the central directory (bo1-zombies/src/qcommon/unzip.cpp: unzlocal_SearchCentralDir, BUFREADCOMMENT + 4).
const centralDirSearch = 0x404

// cut appends the range [start, end) to pieces, cut into pieces of at most pieceMax from its start.
func cut(pieces [][2]int64, start, end int64) [][2]int64 {
	for at := start; at < end; at += pieceMax {
		pieces = append(pieces, [2]int64{at, min(end, at+pieceMax)})
	}
	return pieces
}

// sortFiles sorts the list into the order a browser downloads it in (web/public/store.js): the fastfiles first, then
// by path.
func sortFiles(files []listFile) {
	ff := func(f listFile) bool { return strings.EqualFold(path.Ext(f.Path), ".ff") }
	sort.Slice(files, func(i, j int) bool {
		if fi, fj := ff(files[i]), ff(files[j]); fi != fj {
			return fi
		}
		return files[i].Path < files[j].Path
	})
}

// ---- the archives ----

// archiveEntry is the byte range of an entry in an archive: from the entry's local header to the next entry's local
// header, or to the central directory. That range is what the engine reads from the archive for the entry: the
// entry's header and its data as stored.
type archiveEntry struct {
	archive    string // the archive's lowercase path, its key in the manifest
	start, end int64
}

type archive struct {
	media     bool  // whether it holds images or sounds (.iwi, .wav); the list has any other archive whole
	directory int64 // the offset of its central directory, which the engine reads at start with the record at the end
}

// archiveIndex has every entry of every archive of the game files, by the entry's lowercase name in its archive. The
// engine finds an entry by that name. The sounds of a localized archive have the language's folder in their names
// (english/sound/...), and the zones name them the same way.
type archiveIndex struct {
	entries  map[string][]archiveEntry
	archives map[string]*archive // by the archive's path, lowercase
}

func (f *fileLists) index(version string, files map[string]manifestEntry) *archiveIndex {
	f.mu.Lock()
	if idx := f.indexes[version]; idx != nil {
		f.mu.Unlock()
		return idx
	}
	f.mu.Unlock()
	idx := &archiveIndex{entries: map[string][]archiveEntry{}, archives: map[string]*archive{}}
	for key, e := range files {
		if strings.ToLower(path.Ext(key)) != ".iwd" {
			continue
		}
		dir, entries, err := readZipDirectory(e.file, e.size)
		if err != nil {
			log.Printf("files: %s: %v", e.rel, err)
			continue
		}
		a := &archive{directory: dir}
		for i, ze := range entries {
			end := dir
			if i+1 < len(entries) {
				end = entries[i+1].offset
			}
			name := strings.ToLower(ze.name)
			switch path.Ext(name) {
			case ".iwi", ".wav":
				a.media = true
			}
			idx.entries[name] = append(idx.entries[name], archiveEntry{archive: key, start: ze.offset, end: end})
		}
		idx.archives[key] = a
	}
	f.mu.Lock()
	f.indexes = map[string]*archiveIndex{version: idx} // the index of the earlier version is dropped
	f.mu.Unlock()
	return idx
}

type zipEntry struct {
	name   string
	offset int64 // the offset of its local header
}

// readZipDirectory reads a zip's central directory. It returns the directory's offset and the zip's entries in the
// order they are stored in the file (by the offsets of their local headers). Like the engine's unzip, it does not
// read zip64, which no archive of the game files needs.
func readZipDirectory(file string, size int64) (int64, []zipEntry, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	// The end of central directory record is 22 bytes, followed by a comment of up to 64 KB.
	tail := min(size, 22+65535)
	buf := make([]byte, tail)
	if _, err := f.ReadAt(buf, size-tail); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	at := bytes.LastIndex(buf, []byte{'P', 'K', 5, 6})
	if at < 0 {
		return 0, nil, errors.New("no end of central directory record")
	}
	eocd := buf[at:]
	if len(eocd) < 22 {
		return 0, nil, errors.New("a truncated end of central directory record")
	}
	count := int(binary.LittleEndian.Uint16(eocd[10:]))
	dirSize := int64(binary.LittleEndian.Uint32(eocd[12:]))
	dirOffset := int64(binary.LittleEndian.Uint32(eocd[16:]))
	if dirOffset+dirSize > size {
		return 0, nil, errors.New("the central directory is past the end")
	}
	dir := make([]byte, dirSize)
	if _, err := f.ReadAt(dir, dirOffset); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	entries := make([]zipEntry, 0, count)
	for p := 0; p+46 <= len(dir); {
		if !bytes.Equal(dir[p:p+4], []byte{'P', 'K', 1, 2}) {
			return 0, nil, fmt.Errorf("a bad central directory record at %d", p)
		}
		nameLen := int(binary.LittleEndian.Uint16(dir[p+28:]))
		extraLen := int(binary.LittleEndian.Uint16(dir[p+30:]))
		commentLen := int(binary.LittleEndian.Uint16(dir[p+32:]))
		offset := int64(binary.LittleEndian.Uint32(dir[p+42:]))
		if p+46+nameLen > len(dir) {
			return 0, nil, errors.New("a truncated central directory record")
		}
		name := string(dir[p+46 : p+46+nameLen])
		if !strings.HasSuffix(name, "/") { // not a directory entry
			entries = append(entries, zipEntry{name: name, offset: offset})
		}
		p += 46 + nameLen + extraLen + commentLen
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].offset < entries[j].offset })
	return dirOffset, entries, nil
}

// ---- the game that lists what a map reads: reads/ ----

// listFilesCommand runs zombies-server list-files <dir> [map...]. For each map named, or for each map of the game
// files when none is named, it runs a game that lists what the map reads. It writes what the game listed to
// <dir>/<map>.json, for reads/ (docker/list-files.sh). A map takes about 3 s.
func listFilesCommand(args []string) int {
	flags := flag.NewFlagSet("list-files", flag.ExitOnError)
	gameDir := flags.String("game-dir", env("ZOMBIES_GAME_DIR", "/game"), "the game files, a folder for each title: t5, a copy of a Black Ops install; only read (ZOMBIES_GAME_DIR)")
	engineURL := flags.String("engine-url", env("ZOMBIES_ENGINE_URL", ""), "where the Black Ops engine is downloaded from, with {version} replaced by the release's version; empty: the releases of bo1-zombies on GitHub (ZOMBIES_ENGINE_URL)")
	engineFile := flags.String("engine-file", env("ZOMBIES_ENGINE_FILE", ""), "an engine archive to run instead of the downloaded build (ZOMBIES_ENGINE_FILE)")
	dataDir := flags.String("data-dir", env("ZOMBIES_DATA_DIR", "/config"), "what the game writes: game/files/ (its home), logs/; and where the engine is kept (ZOMBIES_DATA_DIR)")
	wine := flags.String("wine", env("ZOMBIES_WINE", "wine"), "the wine command (ZOMBIES_WINE)")
	port := flags.Int("game-port", envInt("ZOMBIES_GAME_PORT", 3076), "the game's UDP port, on 127.0.0.1 (ZOMBIES_GAME_PORT)")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: zombies-server list-files [flags] <dir> [map...]")
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() < 1 {
		flags.Usage()
		return 2
	}
	out, maps := flags.Arg(0), flags.Args()[1:]
	gameUID, err := gamesUser()
	if err != nil {
		log.Printf("files: %v", err)
		return 1
	}
	if len(maps) == 0 {
		h := &web{gameDir: titleDir(*gameDir, t5)}
		maps = installedMaps(h.paths)()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	eng, err := getEngine(ctx, *dataDir, *engineURL, *engineFile)
	if err != nil {
		log.Printf("files: %v", err)
		return 1
	}
	code := 0
	for _, mapName := range maps {
		g := &game{
			name: mapName, kind: "files", wine: *wine, uid: gameUID, dir: titleDir(*gameDir, t5),
			home: filepath.Join(*dataDir, "game", "files"), logDir: filepath.Join(*dataDir, "logs"),
			ip: "127.0.0.1", port: *port,
			engine: &t5Engine{exe: eng.Exe(), hangTimeout: time.Minute,
				args: []string{"+set", "sv_maxclients", "1", "+set", "bo1_files", "1", "+map", mapName}},
			output: prefixed{"[files " + mapName + "] ", os.Stdout}, startTimeout: 5 * time.Minute,
		}
		zones, names, err := listFiles(ctx, g)
		if err != nil {
			log.Printf("files: no list of what %s reads: %v", mapName, err)
			code = 1
			continue
		}
		slices.Sort(names)
		r := mapReads{Map: mapName, Zones: distinct(zones), Names: slices.Compact(names)}
		var data bytes.Buffer
		enc := json.NewEncoder(&data)
		enc.SetEscapeHTML(false) // keep the names as the game prints them, including &
		enc.SetIndent("", "  ")  // one name per line, for diffs
		enc.Encode(r)
		if err := os.WriteFile(filepath.Join(out, mapName+".json"), data.Bytes(), 0o644); err != nil {
			log.Printf("files: %v", err)
			return 1
		}
		log.Printf("files: %s loads %d zones, with %d images and sounds in the archives", mapName, len(r.Zones), len(r.Names))
	}
	return code
}

// distinct returns the names without repeats, in the order they first occur.
func distinct(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// zoneLoaded matches the line the game prints when it loads a fastfile (bo1-zombies/src/database/db_load.cpp).
// fileNamed is the prefix of each line of the game's list (bo1-zombies/src/server_mp/sv_files_mp.cpp).
var (
	zoneLoaded = regexp.MustCompile(`^Loaded fastfile '([^']+)'`)
	fileNamed  = "bo1_files: "
)

// listFiles runs the game that lists what its map reads (+set bo1_files 1). It returns the zones the game loaded and
// the names it listed, after the game has listed them all and quit.
func listFiles(ctx context.Context, g *game) ([]string, []string, error) {
	var zones, names []string
	done := false
	g.lines = func(line string) {
		if m := zoneLoaded.FindStringSubmatch(line); m != nil {
			zones = append(zones, m[1])
		} else if name, ok := strings.CutPrefix(line, fileNamed); ok {
			if name == "end" {
				done = true
			} else {
				names = append(names, name)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if !g.waitForPort(ctx) {
		return nil, nil, ctx.Err()
	}
	state, err := g.runOnce(ctx)
	if !done {
		return nil, nil, errors.New("the game ended before it had listed: " + exitReason(state, err))
	}
	return zones, names, nil
}

// serveFileList answers GET /maps/<map>/files with the map's list. It answers 404 for a map that the game files lack
// or that has no reads/ file. The ETag is the hash of the list's contents. A browser that asks again (the page's
// store, web/public/store.js: mapList, or the page's service worker, web/public/sw.js) gets a 304 while its copy is
// current. It gets the list again only after the list changed.
func (h *web) serveFileList(w http.ResponseWriter, r *http.Request, mapName string) {
	var list *fileList
	if h.lobby != nil && h.lobby.files != nil && slices.Contains(h.lobby.maps(), mapName) {
		list = h.lobby.files.lookup(mapName)
	}
	if list == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", list.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(list.body))
}

// ---- downloads ----

// servePack answers POST /maps/<map>/pack with the pieces of the map's list that the browser lacks. The request is
// {"version", "want": [[first, end), ...]}. "want" names the pieces by their numbers in the list, where the pieces of
// the list's files are numbered in order from 0. Each item is a run of pieces, from first up to end. The answer is
// the bytes of those pieces, one after another, in the order asked. "version" is the version of the browser's list.
// When the list has another version (the game files changed since the browser fetched it), the answer is a 409, and
// the browser fetches the list again. The answer is a 403 when the server sends no game files (-download-rate).
// servePack sends as fast as the browser reads, within the server's rate for all downloads together (h.pace). It
// logs how each download went: who, how much, how fast.
func (h *web) servePack(w http.ResponseWriter, r *http.Request, mapName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.downloads {
		http.Error(w, "the server sends no game files", http.StatusForbidden)
		return
	}
	var list *fileList
	if h.lobby != nil && h.lobby.files != nil && slices.Contains(h.lobby.maps(), mapName) {
		list = h.lobby.files.lookup(mapName)
	}
	if list == nil {
		http.NotFound(w, r)
		return
	}
	var in struct {
		Version string   `json:"version"`
		Want    [][2]int `json:"want"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if in.Version != list.Version {
		http.Error(w, "the game files changed: their list is at version "+list.Version, http.StatusConflict)
		return
	}
	type piece struct {
		file       int
		start, end int64
	}
	var all []piece
	for i, f := range list.Files {
		for _, p := range f.Pieces {
			all = append(all, piece{i, p[0], p[1]})
		}
	}
	var want []piece
	var total int64
	for _, run := range in.Want {
		if run[0] < 0 || run[1] > len(all) || run[0] >= run[1] {
			http.Error(w, fmt.Sprintf("no pieces %d to %d: the list has %d", run[0], run[1], len(all)), http.StatusBadRequest)
			return
		}
		for _, p := range all[run[0]:run[1]] {
			want = append(want, p)
			total += p.end - p.start
		}
	}
	who := "a browser"
	if h.lobby != nil {
		if name := h.lobby.nameOf(bearer(r)); name != "" {
			who = name
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
	w.Header().Set("Cache-Control", "no-store")
	began := time.Now()
	sent, err := h.sendPieces(r.Context(), w, list, len(want), func(i int) (int, int64, int64) {
		return want[i].file, want[i].start, want[i].end
	})
	took := time.Since(began)
	rate := float64(sent) * 8 / 1e6 / max(took.Seconds(), 0.001)
	if err == nil {
		log.Printf("files: %s downloaded %s: %d MB in %s (%.0f Mbit/s)", who, mapName, sent/1e6, took.Round(time.Second), rate)
	} else {
		log.Printf("files: %s stopped downloading %s at %d of %d MB, after %s (%.0f Mbit/s): %v", who, mapName, sent/1e6,
			total/1e6, took.Round(time.Second), rate, err)
	}
}

// packBlock is the number of bytes a download sends at a time. The downloads take turns, a block each (pacer).
const packBlock = 64 << 10

// sendPieces writes n pieces to w one after another, a block at a time, each block when the pacer allows it. piece(i)
// returns the index of piece i's file in the list, and the piece's byte range. sendPieces returns the number of bytes
// it sent, and the error that stopped it early, if any.
func (h *web) sendPieces(ctx context.Context, w io.Writer, list *fileList, n int, piece func(int) (int, int64, int64)) (int64, error) {
	open := map[int]*os.File{}
	defer func() {
		for _, f := range open {
			f.Close()
		}
	}()
	buf := make([]byte, packBlock)
	var sent int64
	for i := range n {
		file, start, end := piece(i)
		f := open[file]
		if f == nil {
			var err error
			if f, err = os.Open(list.disk[file]); err != nil {
				return sent, err
			}
			open[file] = f
		}
		for at := start; at < end; {
			block := buf[:min(int64(len(buf)), end-at)]
			if err := h.pace.wait(ctx, len(block)); err != nil {
				return sent, err
			}
			if _, err := f.ReadAt(block, at); err != nil {
				return sent, fmt.Errorf("%s: %w", list.Files[file].Path, err)
			}
			if _, err := w.Write(block); err != nil {
				return sent, err
			}
			at += int64(len(block))
			sent += int64(len(block))
		}
	}
	return sent, nil
}

// pacer shares the server's upload among the downloads that run at the same time. Together they send at most rate
// bytes a second. A nil pacer, or a rate of 0, sets no limit. Each download waits for its turn before each block it
// sends. A turn is the next slot of the rate, and the turns are given in the order the downloads ask for them. A
// download asks for its next turn only after it has sent its last block, so the downloads take turns. A download
// whose browser reads slowly asks less often, and the other downloads get its turns.
type pacer struct {
	rate int64 // bytes a second

	mu   sync.Mutex
	next time.Time // when the next block may be sent
}

func (p *pacer) wait(ctx context.Context, n int) error {
	if p == nil || p.rate <= 0 {
		return ctx.Err()
	}
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now // turns are not saved up while no one downloads
	}
	at := p.next
	p.next = at.Add(time.Duration(float64(n) / float64(p.rate) * float64(time.Second)))
	p.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// parseRate parses the download rate setting (-download-rate). The setting is "", "off" or "0" for no downloads,
// "unlimited", or a rate in bits a second, the unit a link's speed is given in. The rate can end in k, M or G for
// thousands, millions or billions ("40M"). parseRate returns whether downloads are on, and the rate in bytes a
// second, where 0 means no limit.
func parseRate(s string) (on bool, bytesPerSecond int64, err error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "off", "0":
		return false, 0, nil
	case "unlimited":
		return true, 0, nil
	}
	scale := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		scale = 1e3
	case 'm', 'M':
		scale = 1e6
	case 'g', 'G':
		scale = 1e9
	}
	if scale != 1 {
		s = s[:len(s)-1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return false, 0, fmt.Errorf("a download rate is bits a second (40M, 500k, 1G), unlimited, or off: not %q", s)
	}
	return true, max(int64(v*scale/8), 1), nil
}
