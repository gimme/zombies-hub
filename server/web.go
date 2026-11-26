package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"zombies/internal/launcherapi"
)

// web is the server's HTTP side. It listens on TCP at the same port number as the relay. Browsers run the web client
// only on a secure page: at localhost, or over HTTPS. A reverse proxy in front of this port adds HTTPS (Nginx Proxy
// Manager, for example). The page plays on the server that served it, and asks that server, through the proxy, how to
// join:
//
//	GET /                   the web client: the page (web/index.html) and the engine's web build (bo1zombies.js,
//	                        bo1zombies.wasm, and xwma.js, the page's decoder for the menus' sounds). The files are
//	                        sent gzipped to a browser that accepts gzip (the module is 9 MB, or 3.4 MB compressed).
//	                        The browser revalidates them each time the page loads.
//	GET /assets/<file>      the page's script and style sheet, which Vite builds from web/src (web/vite.config.ts):
//	                        index.js, with its source map, and index.css. The server serves the built page, the
//	                        folder web/dist of a checkout.
//	GET /server             {"port": n, "web": "v..."}. "port" is the UDP port where players reach the relay. The page
//	                        joins at that port of its own host name. A port forward or mapping can make it differ
//	                        from this port (-public-port). "web" is the version of the web client that the server
//	                        serves (webVersion). The page asks for it when it loads and again before it starts the
//	                        game. A page that was left open while the server was updated reloads instead of starting
//	                        the game. "dev" is true on a dev run (-dev): the page then lets the engine blink what it
//	                        leaves out of the picture instead of drawing it black. "downloads" is true when the
//	                        server sends game files (-download-rate), which a map's button downloads. "launcher" is
//	                        the address of the launcher's latest release on GitHub, for the page to link to. The
//	                        launcher joins a Black Ops II room's game from the player's PC (launcher.go).
//	                        "launcherMac" is the address there of the script that installs the launcher on a Mac,
//	                        for the line that the page shows a Mac (launcherapi.MacSetup).
//	/launcher/...           the launcher's calls (launcher.go).
//	GET /player.js          the engine's host (web/public/player.js), an ES module that the page runs the game with.
//	                        It starts and stops the engine, and keeps the game files that a map needs in the
//	                        browser's store.
//	GET /store.js           the store of the game files in the browser (web/public/store.js). The engine's file system
//	                        backend loads it, and the host runs it as a worker that downloads a map's files before
//	                        the map's game.
//	GET /sw.js              the page's service worker (web/public/sw.js). It keeps the page and the engine for when
//	                        the server can't be reached. /manifest.webmanifest and /icon.png let the browser install
//	                        the page. /icon.png is the page's icon, a Z (web/public/icon.png), and /icon.svg is the
//	                        icon of the page's tab.
//	GET /oswald.woff2       the page's display font (web/public/oswald.woff2: the Latin letters of Oswald, under the
//	                        SIL Open Font License, web/public/oswald-OFL.txt). The server serves it itself, because a
//	                        font service may be out of reach of a server on a LAN.
//	GET /inter.woff2        the font of the page's text (web/public/inter.woff2: the Latin letters of Inter at weights
//	                        400 to 700, under the same license, web/public/inter-OFL.txt). The server serves it for
//	                        the same reason, and so that the page looks the same on every system.
//	GET /music              the music of the Zombies menu, from the game files. The page plays it (mapart.go).
//	GET /sounds             the game's menu sounds, from the game files. The page plays them where the game's menus
//	                        and the game's own lobby play them. Most are xWMA, which the page decodes with the web
//	                        client's decoder (xwma.js). The sounds that the game streams are MS-ADPCM, like the music
//	                        (sounds.go).
//	GET /strings?keys=...   the game's localized strings with those names, from the game files, as a JSON object.
//	                        They are the text of the game's options menu, which the page rebuilds
//	                        (web/src/settings.ts, mapart.go). A player changes their settings there outside a game.
//	                        The settings are the player's config in the browser's store.
//	GET /images/<name>.png  the pictures of the options menu's Gamepad page, from the game files (mapart.go).
//	GET /pictures/<id>.png  the players' pictures, from the game files (pictures.go). The page shows a player's
//	                        picture next to their name.
//	GET /maps/<map>/...     what the page shows of a Zombies map, from the game files: its pictures (mapart.go).
//	                        /maps/<map>/files is the list of what the map's game reads of the game files, split into
//	                        the pieces that a browser's store keeps. It has the hash of each piece, for a browser
//	                        that imports the pieces from the player's own copy of the game (files.go). POST
//	                        /maps/<map>/pack sends the pieces of the list that a browser lacks, when the server sends
//	                        game files (-download-rate). /maps/<map>/pipelines is the list of the pipelines that the
//	                        web client draws the map with. The web client compiles them while the map loads
//	                        (bo1-zombies/web/pipelines/<map>.bin, docker/pipelines.sh).
//	GET /certhash           the SHA-256 of the WebTransport certificate, in hex. The page fetches it before it opens a
//	                        session (bo1-zombies/web/src/library_bo1.js).
//	GET /game/manifest.txt  the game files that the web client may open. The first line is their version. Each line
//	                        after it has a file's path, its size and the hash of its contents, separated by tabs.
//	                        Lookups ignore case, as Windows does. The manifest lists only the game files. The base
//	                        data files that an install of the game lacks (data/main) come with the web client's
//	                        module (bo1-zombies/web/CMakeLists.txt), as the Windows build puts them next to the exe.
//	                        The version is made from the files' contents (hashes.go), not their times, so a copy of
//	                        the game files has the same version. The engine reads the files only from the browser's
//	                        store, which a map's download or import fills. It never reads them by path from the
//	                        server.
//	/lobby/...              the rooms (rooms.go).
//	/auth/...               the sign-in with Discord, on a server where players sign in with Discord (discord.go).
//	                        On such a server, what the server serves from the game files goes only to signed-in
//	                        players (gated): the game files, the maps' pictures and lists, the players' pictures, and
//	                        the menus' pictures, music, sounds and text.
//
// The page needs cross-origin isolation, because the engine's threads use SharedArrayBuffer. The server therefore sets
// the COOP and COEP headers on everything the page loads.
type web struct {
	certs      *certificates
	publicPort int    // GET /server
	pageDir    string // the folder of the built page, with index.html
	// buildDir is the folder of the engine's web build (bo1zombies.js, bo1zombies.wasm). pipelinesDir is the folder of
	// the engine's lists of pipelines, <map>.bin (internal/bo1zombies).
	buildDir     string
	pipelinesDir string
	gameDir      string // t5's game files (main.go: titleDir)
	dev          bool   // GET /server
	lobby        *lobby // /lobby/...
	// downloads is whether a browser may download game files (/maps/<map>/pack). It is off unless -download-rate is
	// set. pace limits all the downloads together to its rate.
	downloads bool
	pace      *pacer
	// The launcher (launcher.go). t6Dir is the folder of T6's game files, which the launcher downloads, or "" on a
	// server without them. t7Send is the folder that it downloads T7's from, or "" on a server that has none to send
	// (main.go). sent has the list of each title's files that the server last made (sending).
	t6Dir  string
	t7Send string
	sentMu sync.Mutex
	sent   map[string]*sentFiles

	hashes   fileHashes // the hashes of the game files' contents (hashes.go)
	building sync.Mutex // one manifest is made at a time (buildManifest)
	mu       sync.Mutex
	manifest map[string]manifestEntry // lowercase relative path -> file
	version  string                   // the manifest's version (manifestVersion)

	gz      sync.Mutex
	gzipped map[string]gzipFile // the compressed files of the page and the build, by path on disk (serveGzipped)

	// The maps' pictures, the players' pictures, the menu's music and sounds, and the game's localized strings (the
	// maps' descriptions and the text of the options menu). Each is read from the game files once (mapart.go,
	// pictures.go, sounds.go).
	art    sync.Mutex
	images map[string]string // the archive with each picture, the music and each menu sound, by the entry's path in it
	// packs has where each of Black Ops II's images is in its image packs, by the hash of its name (ipak.go).
	packs map[uint32]packEntry
	// pictures is keyed by "<map>/<kind>", "image/<name>", "picture/<id>", "music" and "sounds". An entry has no data
	// when the game files lack the file.
	pictures map[string]mapPicture
	locs     sync.Mutex
	texts    map[string]string // by name
}

type manifestEntry struct {
	rel     string // relative path, in its real case
	file    string // path on disk
	size    int64
	modTime int64  // ns
	hash    string // the hash of its contents (hashes.go: contentHash), which is its key in a browser's store
}

// The manifest lists what the engine may read from the game files: zones, iwds, configs, localization, and the videos
// of the Zombies maps, but not the campaign's videos. The Zombies videos are main/video/zombie*.bik (the maps' loading
// movies and Call of the Dead's outro, which a client plays, bo1-zombies/src/client/cl_cin.cpp) and default.bik, which
// the engine plays when a movie can't be opened. skipDirs and skipExts are the folders and file types left out.
var (
	// Soundtrack is the Zombies soundtrack: mp3s that came with the map packs, which no game reads.
	skipDirs = map[string]bool{"players": true, "redist": true, "soundtrack": true, "$recycle.bin": true}
	skipExts = map[string]bool{".exe": true, ".dll": true, ".pdb": true, ".log": true, ".vdf": true, ".bmp": true}
	// buildFile matches a file of the build: a plain file name with one of these extensions.
	buildFile = regexp.MustCompile(`^[\w.-]+\.(html|js|wasm|map|css|png|ico|json)$`)
	// pageAsset matches the path of a file that Vite built for the page (web/vite.config.ts).
	pageAsset = regexp.MustCompile(`^/assets/\w[\w.-]*$`)
)

func (h *web) buildManifest() map[string]manifestEntry {
	h.building.Lock()
	defer h.building.Unlock()
	files := map[string]*manifestEntry{}
	filepath.WalkDir(h.gameDir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if file != h.gameDir && skipDirs[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		ext, name := strings.ToLower(filepath.Ext(file)), strings.ToLower(d.Name())
		if !d.Type().IsRegular() || skipExts[ext] ||
			ext == ".bik" && !strings.HasPrefix(name, "zombie") && name != "default.bik" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(h.gameDir, file)
		rel = filepath.ToSlash(rel)
		files[strings.ToLower(rel)] = &manifestEntry{rel: rel, file: file, size: info.Size(), modTime: info.ModTime().UnixNano()}
		return nil
	})
	unhashed := make([]*manifestEntry, 0, len(files))
	for _, e := range files {
		unhashed = append(unhashed, e)
	}
	h.hashes.fill(unhashed)
	out := make(map[string]manifestEntry, len(files))
	for key, e := range files {
		if e.hash != "" { // a file that could not be read is left out
			out[key] = *e
		}
	}
	return out
}

// manifestVersion returns the version of the files in the manifest. The version changes when the path or the contents
// of any file change.
func manifestVersion(files map[string]manifestEntry) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	sum := sha256.New()
	for _, k := range keys {
		e := files[k]
		fmt.Fprintf(sum, "%s\x00%s\x00", e.rel, e.hash)
	}
	return "v" + hex.EncodeToString(sum.Sum(nil))[:16]
}

// webVersion returns the version of the web client. It is made from the sizes and times of the web client's files:
// the page, the engine's host that the page runs the engine with (player.js), and the build's script and module. An
// update of the server or a new build changes them.
func (h *web) webVersion() string {
	hash := fnv.New64a()
	for _, file := range []string{filepath.Join(h.pageDir, "index.html"), filepath.Join(h.pageDir, "player.js"),
		filepath.Join(h.buildDir, "bo1zombies.js"), filepath.Join(h.buildDir, "bo1zombies.wasm")} {
		if st, err := os.Stat(file); err == nil {
			fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", file, st.Size(), st.ModTime().UnixNano())
		}
	}
	return fmt.Sprintf("v%016x", hash.Sum64())
}

// files returns the game files as the manifest last listed them, and their version.
func (h *web) files() (map[string]manifestEntry, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.manifest == nil {
		h.manifest = h.buildManifest()
		h.version = manifestVersion(h.manifest)
	}
	return h.manifest, h.version
}

// paths returns the paths of the game files, in lowercase. installedMaps finds the Zombies maps among them (t5.go).
func (h *web) paths() []string {
	files, _ := h.files()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	return paths
}

func (h *web) lookup(rel string) (manifestEntry, bool) {
	files, _ := h.files()
	e, ok := files[strings.ToLower(rel)]
	return e, ok
}

func (h *web) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Cross-Origin-Opener-Policy", "same-origin")
	hdr.Set("Cross-Origin-Embedder-Policy", "require-corp")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
	if strings.HasPrefix(r.URL.Path, "/lobby/") && h.lobby != nil {
		h.lobby.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/auth/") && h.lobby != nil {
		h.lobby.serveAuth(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/launcher/") && h.lobby != nil {
		h.serveLauncherCall(w, r)
		return
	}
	if gated(r.URL.Path) && h.lobby != nil && !h.lobby.allowed(r) {
		hdr.Set("Cache-Control", "no-store")
		http.Error(w, "Sign in first.", http.StatusUnauthorized)
		return
	}
	if m, ok := strings.CutPrefix(r.URL.Path, "/maps/"); ok && strings.HasSuffix(m, "/pack") {
		h.servePack(w, r, strings.TrimSuffix(m, "/pack"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p := r.URL.Path
	switch {
	case p == "/" || p == "/index.html":
		h.serveGzipped(w, r, filepath.Join(h.pageDir, "index.html"), "no-cache")
	case p == "/player.js" || p == "/store.js":
		h.serveGzipped(w, r, filepath.Join(h.pageDir, path.Base(p)), "no-cache")
	case pageAsset.MatchString(p):
		h.serveGzipped(w, r, filepath.Join(h.pageDir, "assets", path.Base(p)), "no-cache")
	case p == "/sw.js":
		h.serveGzipped(w, r, filepath.Join(h.pageDir, "sw.js"), "no-cache")
	case p == "/manifest.webmanifest":
		serveFile(w, r, filepath.Join(h.pageDir, "manifest.webmanifest"), "application/manifest+json", "no-cache")
	case p == "/icon.svg" || p == "/icon.png":
		// The page's icon, a Z in the logo's red (web/public/icon.svg). The SVG is the icon of a tab, and the PNG is
		// the icon of an installed page.
		serveFile(w, r, filepath.Join(h.pageDir, path.Base(p)), "", "no-cache")
	case p == "/oswald.woff2" || p == "/inter.woff2":
		serveFile(w, r, filepath.Join(h.pageDir, path.Base(p)), "font/woff2", "max-age=86400")
	case p == "/music":
		h.serveMusic(w, r)
	case p == "/sounds":
		h.serveSounds(w, r)
	case strings.HasPrefix(p, "/maps/"):
		h.serveMapArt(w, r)
	case strings.HasPrefix(p, "/images/"):
		h.serveMenuImage(w, r)
	case strings.HasPrefix(p, "/pictures/"):
		h.servePlayerPicture(w, r)
	case p == "/strings":
		h.serveStrings(w, r)
	case p == "/server":
		hdr.Set("Content-Type", "application/json")
		hdr.Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			Port        int    `json:"port"`
			Web         string `json:"web"`
			Dev         bool   `json:"dev,omitempty"`
			Downloads   bool   `json:"downloads,omitempty"`
			Launcher    string `json:"launcher"`
			LauncherMac string `json:"launcherMac"`
		}{h.publicPort, h.webVersion(), h.dev, h.downloads, launcherapi.LatestFile(launcherapi.Releases, launcherapi.Setup),
			launcherapi.LatestFile(launcherapi.Releases, launcherapi.MacSetup)})
	case p == "/certhash":
		_, hash, err := h.certs.current()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hdr.Set("Content-Type", "text/plain")
		hdr.Set("Cache-Control", "no-store")
		w.Write([]byte(hash))
	case p == "/game/manifest.txt":
		files := h.buildManifest() // the game files may have changed
		version := manifestVersion(files)
		h.mu.Lock()
		h.manifest, h.version = files, version
		h.mu.Unlock()
		rels := make([]string, 0, len(files))
		for _, e := range files {
			rels = append(rels, e.rel+"\t"+strconv.FormatInt(e.size, 10)+"\t"+e.hash)
		}
		slices.Sort(rels)
		var text strings.Builder
		text.WriteString(version + "\n")
		for _, rel := range rels {
			text.WriteString(rel + "\n")
		}
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		hdr.Set("Cache-Control", "no-cache")
		w.Write([]byte(text.String()))
	case h.buildDir != "" && buildFile.MatchString(path.Base(p)):
		h.serveGzipped(w, r, filepath.Join(h.buildDir, path.Base(p)), "no-cache")
	default:
		http.NotFound(w, r)
	}
}

// serveFile sends a file and supports range requests. When contentType is "", the type is chosen by the file's
// extension.
func serveFile(w http.ResponseWriter, r *http.Request, file, contentType, cacheControl string) {
	f, err := os.Open(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, file, st.ModTime(), f)
}

// servePipelines answers GET /maps/<map>/pipelines with the map's list of pipelines
// (bo1-zombies/web/pipelines/<map>.bin). It answers 404 for a map without a list, or one that the page doesn't list.
func (h *web) servePipelines(w http.ResponseWriter, r *http.Request, mapName string) {
	if _, ok := mapByName(mapName); !ok || h.pipelinesDir == "" {
		http.NotFound(w, r)
		return
	}
	h.serveGzipped(w, r, filepath.Join(h.pipelinesDir, mapName+".bin"), "no-cache")
}

// gzipExts are the extensions of the files that serveGzipped compresses: text and the module. Pictures are compressed
// already.
var gzipExts = map[string]bool{".html": true, ".js": true, ".wasm": true, ".map": true, ".css": true, ".json": true, ".bin": true}

type gzipFile struct {
	size, modTime int64
	data          []byte
}

// serveGzipped sends a file of the page or the build like serveFile. It sends the file gzipped when the browser
// accepts gzip and asks for the whole file. Each file is compressed once, and again when it changes.
func (h *web) serveGzipped(w http.ResponseWriter, r *http.Request, file, cacheControl string) {
	ext := strings.ToLower(filepath.Ext(file))
	if !gzipExts[ext] {
		serveFile(w, r, file, "", cacheControl)
		return
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Add("Vary", "Accept-Encoding")
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	var data []byte
	if r.Header.Get("Range") == "" && acceptsGzip(r) {
		data = h.compressed(file, st)
	}
	if data == nil {
		serveFile(w, r, file, contentType, cacheControl)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, file, st.ModTime(), bytes.NewReader(data))
}

// compressed returns the gzipped contents of the file as it is on disk (st). It returns nil when the file can't be
// read.
func (h *web) compressed(file string, st os.FileInfo) []byte {
	h.gz.Lock()
	defer h.gz.Unlock()
	if g, ok := h.gzipped[file]; ok && g.size == st.Size() && g.modTime == st.ModTime().UnixNano() {
		return g.data
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(raw)
	zw.Close()
	if h.gzipped == nil {
		h.gzipped = map[string]gzipFile{}
	}
	h.gzipped[file] = gzipFile{st.Size(), st.ModTime().UnixNano(), buf.Bytes()}
	return buf.Bytes()
}

// acceptsGzip reports whether the request's Accept-Encoding header accepts gzip.
func acceptsGzip(r *http.Request) bool {
	for _, coding := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(coding, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if _, q, ok := strings.Cut(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v == 0 {
				return false
			}
		}
		return true
	}
	return false
}

func isFile(file string) bool {
	st, err := os.Stat(file)
	return err == nil && st.Mode().IsRegular()
}
