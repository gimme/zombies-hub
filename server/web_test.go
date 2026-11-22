package main

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string, header ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	body, _ := io.ReadAll(rsp.Body)
	return rsp, string(body)
}

func TestWeb(t *testing.T) {
	root := t.TempDir()
	gameDir := filepath.Join(root, "game")
	pageDir, buildDir := filepath.Join(root, "page"), filepath.Join(root, "build")
	writeFile(t, filepath.Join(gameDir, "zone", "English", "Zombie_Theater.ff"), "0123456789")
	writeFile(t, filepath.Join(gameDir, "main", "iw_00.iwd"), "iwd")
	writeFile(t, filepath.Join(gameDir, "players", "profile.cfg"), "secret")
	writeFile(t, filepath.Join(gameDir, "BlackOps.exe"), "exe")
	writeFile(t, filepath.Join(gameDir, "Soundtrack", "115.mp3"), "music")
	writeFile(t, filepath.Join(pageDir, "index.html"), "<html>")
	writeFile(t, filepath.Join(pageDir, "player.js"), "export {}")
	writeFile(t, filepath.Join(pageDir, "assets", "index.js"), "script")
	writeFile(t, filepath.Join(pageDir, "assets", "index.css"), "sheet")
	writeFile(t, filepath.Join(pageDir, "assets", ".hidden.js"), "hidden")
	writeFile(t, filepath.Join(pageDir, "assets", "more", "index.js"), "deeper")
	writeFile(t, filepath.Join(pageDir, "bo1zombies.js"), "old")
	writeFile(t, filepath.Join(pageDir, "build.ps1"), "powershell")
	writeFile(t, filepath.Join(buildDir, "bo1zombies.js"), "new")
	writeFile(t, filepath.Join(buildDir, "build.ninja"), "ninja")

	certs := &certificates{}
	_, hash, _ := certs.current()
	srv := httptest.NewServer(&web{certs: certs, publicPort: 4075, pageDir: pageDir, buildDir: buildDir, gameDir: gameDir})
	defer srv.Close()

	if _, body := get(t, srv.URL+"/certhash"); body != hash {
		t.Errorf("/certhash: %q, want %s", body, hash)
	}
	server := func() (s struct {
		Port int    `json:"port"`
		Web  string `json:"web"`
		Dev  bool   `json:"dev"`
	}) {
		t.Helper()
		_, body := get(t, srv.URL+"/server")
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatalf("/server: %q: %v", body, err)
		}
		return s
	}
	first := server()
	if first.Port != 4075 || !regexp.MustCompile(`^v[0-9a-f]{16}$`).MatchString(first.Web) || first.Dev {
		t.Errorf("/server: %+v, want the public port and the web client's version", first)
	}
	// the web client's version changes with its page, the engine's host or its build, as an update of the server
	// changes them
	for _, file := range []string{filepath.Join(pageDir, "index.html"), filepath.Join(pageDir, "player.js"),
		filepath.Join(buildDir, "bo1zombies.js")} {
		before := server().Web
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(file, later, later); err != nil {
			t.Fatal(err)
		}
		if now := server().Web; now == before {
			t.Errorf("/server kept the web client's version %s after %s changed", now, filepath.Base(file))
		}
	}

	rsp, body := get(t, srv.URL+"/")
	if body != "<html>" || rsp.Header.Get("Cross-Origin-Embedder-Policy") != "require-corp" ||
		rsp.Header.Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Errorf("/: %q (%s), want the page, cross-origin isolated", body, rsp.Header)
	}
	if _, body = get(t, srv.URL+"/bo1zombies.js"); body != "new" {
		t.Errorf("/bo1zombies.js: %q, want the build's", body)
	}
	// the page's script and style sheet, which Vite built
	for p, want := range map[string]string{"/assets/index.js": "script", "/assets/index.css": "sheet"} {
		if rsp, body = get(t, srv.URL+p); body != want || rsp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %q (%s), want the page's built file, revalidated each time", p, body, rsp.Header)
		}
	}
	for _, p := range []string{"/build.ninja", "/build.ps1", "/game/players/profile.cfg", "/game/BlackOps.exe", "/game/../page/index.html",
		"/assets/", "/assets/..", "/assets/.hidden.js", "/assets/more/index.js", "/assets/missing.js", "/index.js"} {
		if rsp, _ = get(t, srv.URL+p); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", p, rsp.Status)
		}
	}

	// the manifest: each file of the install the engine may open, with its size and its contents' hash (the base data
	// files the install lacks come with the web client's module); the version names them all
	hashOf := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return contentHash(sum[:])
	}
	_, body = get(t, srv.URL+"/game/manifest.txt")
	version, paths, _ := strings.Cut(body, "\n")
	want := "main/iw_00.iwd\t3\t" + hashOf("iwd") + "\nzone/English/Zombie_Theater.ff\t10\t" + hashOf("0123456789") + "\n"
	if !regexp.MustCompile(`^v[0-9a-f]{16}$`).MatchString(version) || paths != want {
		t.Errorf("manifest:\n%s\nwant\n%s", body, want)
	}
	// the game files are the store's, which a map's pack fills (files.go): the server serves none of them by path
	if rsp, _ = get(t, srv.URL+"/game/"+version+"/zone/English/Zombie_Theater.ff"); rsp.StatusCode != http.StatusNotFound {
		t.Errorf("a game file by its path: %s, want 404", rsp.Status)
	}
	// a file's new time keeps the version, as a copy of the install makes; new contents don't
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(gameDir, "main", "iw_00.iwd"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, body = get(t, srv.URL+"/game/manifest.txt"); !strings.HasPrefix(body, version+"\n") {
		t.Errorf("the manifest changed its version from %s after a file's time did:\n%s", version, body)
	}
	writeFile(t, filepath.Join(gameDir, "main", "iw_00.iwd"), "iwd 2")
	if _, body = get(t, srv.URL+"/game/manifest.txt"); strings.HasPrefix(body, version+"\n") ||
		!strings.Contains(body, "main/iw_00.iwd\t5\t"+hashOf("iwd 2")+"\n") {
		t.Errorf("the manifest after a game file changed (it was at version %s):\n%s", version, body)
	}
}

// The game files' hashes are kept, and a file is hashed again only when its size or time changes.
func TestFileHashesKept(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "game", "zone", "a.ff")
	writeFile(t, file, "zone")
	kept := filepath.Join(root, "config", "hashes.tsv")
	h := &web{gameDir: filepath.Join(root, "game"), hashes: fileHashes{file: kept}}
	files := h.buildManifest()
	sum := sha256.Sum256([]byte("zone"))
	if files["zone/a.ff"].hash != contentHash(sum[:]) {
		t.Fatalf("the hash: %+v", files["zone/a.ff"])
	}
	// another server, as one started again, takes the hash kept: a file whose contents changed with its size and time as
	// they were still has its old hash, which it would not were it hashed again
	st, _ := os.Stat(file)
	os.WriteFile(file, []byte("ZONE"), 0o644)
	os.Chtimes(file, st.ModTime(), st.ModTime())
	again := &web{gameDir: filepath.Join(root, "game"), hashes: fileHashes{file: kept}}
	if got := again.buildManifest()["zone/a.ff"].hash; got != contentHash(sum[:]) {
		t.Errorf("a file as it was (its size and time): hashed again, %s", got)
	}
	later := st.ModTime().Add(time.Hour)
	os.Chtimes(file, later, later)
	sum = sha256.Sum256([]byte("ZONE"))
	if got := again.buildManifest()["zone/a.ff"].hash; got != contentHash(sum[:]) {
		t.Errorf("a file of a new time: %s, want its contents' hash", got)
	}
}

// the page and the build go gzipped to a browser that takes it and asks for all of a file, and as they are otherwise
func TestGzip(t *testing.T) {
	pageDir, buildDir := t.TempDir(), t.TempDir()
	module := strings.Repeat("\x00asm module ", 1000)
	writeFile(t, filepath.Join(pageDir, "index.html"), "<html>"+strings.Repeat(" ", 1000))
	writeFile(t, filepath.Join(buildDir, "bo1zombies.wasm"), module)
	writeFile(t, filepath.Join(buildDir, "favicon.png"), "png")
	srv := httptest.NewServer(&web{pageDir: pageDir, buildDir: buildDir})
	defer srv.Close()

	gunzip := func(body string) string {
		t.Helper()
		zr, err := gzip.NewReader(strings.NewReader(body))
		if err != nil {
			t.Fatalf("not gzip: %v", err)
		}
		data, _ := io.ReadAll(zr)
		return string(data)
	}
	rsp, body := get(t, srv.URL+"/bo1zombies.wasm", "Accept-Encoding", "gzip, deflate, br, zstd")
	if rsp.Header.Get("Content-Encoding") != "gzip" || rsp.Header.Get("Content-Type") != "application/wasm" ||
		rsp.Header.Get("Vary") != "Accept-Encoding" || len(body) >= len(module) || gunzip(body) != module {
		t.Errorf("the module, to a browser taking gzip: %s, %d bytes", rsp.Header, len(body))
	}
	// as it changes
	module = strings.Repeat("\x00asm module 2 ", 1000)
	writeFile(t, filepath.Join(buildDir, "bo1zombies.wasm"), module)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(buildDir, "bo1zombies.wasm"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, body = get(t, srv.URL+"/bo1zombies.wasm", "Accept-Encoding", "gzip"); gunzip(body) != module {
		t.Errorf("the module after it changed: not the new one")
	}
	// revalidated
	rsp, _ = get(t, srv.URL+"/bo1zombies.wasm", "Accept-Encoding", "gzip", "If-Modified-Since", later.Add(time.Second).UTC().Format(http.TimeFormat))
	if rsp.StatusCode != http.StatusNotModified {
		t.Errorf("the module, unchanged since: %s, want 304", rsp.Status)
	}
	for _, header := range [][]string{{"Accept-Encoding", "identity"}, {"Accept-Encoding", "gzip;q=0"}, {"Accept-Encoding", "gzip", "Range", "bytes=0-4"}} {
		rsp, body = get(t, srv.URL+"/bo1zombies.wasm", header...)
		if rsp.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(module, body) || body == "" {
			t.Errorf("the module with %q: %s %q, want it as it is", header, rsp.Header.Get("Content-Encoding"), body[:min(len(body), 20)])
		}
	}
	if rsp, body = get(t, srv.URL+"/", "Accept-Encoding", "gzip"); rsp.Header.Get("Content-Encoding") != "gzip" ||
		!strings.HasPrefix(gunzip(body), "<html>") || !strings.HasPrefix(rsp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("the page, to a browser taking gzip: %s", rsp.Header)
	}
	if rsp, body = get(t, srv.URL+"/favicon.png", "Accept-Encoding", "gzip"); rsp.Header.Get("Content-Encoding") != "" || body != "png" {
		t.Errorf("a picture: %s %q, want it as it is", rsp.Header.Get("Content-Encoding"), body)
	}
}

// the page's icon: web/public/icon.svg for its tab, web/public/icon.png for the page installed, with no game files
func TestIcon(t *testing.T) {
	pageDir := t.TempDir()
	writeFile(t, filepath.Join(pageDir, "icon.svg"), "<svg/>")
	writeFile(t, filepath.Join(pageDir, "icon.png"), "\x89PNG")
	srv := httptest.NewServer(&web{pageDir: pageDir})
	defer srv.Close()
	for file, want := range map[string]string{"icon.svg": "image/svg+xml", "icon.png": "image/png"} {
		rsp, body := get(t, srv.URL+"/"+file)
		if rsp.StatusCode != http.StatusOK || rsp.Header.Get("Content-Type") != want || len(body) < 4 {
			t.Errorf("/%s: %s %s %q", file, rsp.Status, rsp.Header.Get("Content-Type"), body)
		}
	}
}

func TestPipelinesAndDev(t *testing.T) {
	pageDir, lists := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(lists, "zombie_theater.bin"), "BO1P list")
	writeFile(t, filepath.Join(lists, "zombietron.bin"), "BO1P list")
	srv := httptest.NewServer(&web{pageDir: pageDir, pipelinesDir: lists, publicPort: 4075, dev: true})
	defer srv.Close()

	if _, body := get(t, srv.URL+"/server"); !regexp.MustCompile(`^\{"port":4075,"web":"v[0-9a-f]{16}","dev":true,"launcher":"https://[^"]+"\}\n$`).MatchString(body) {
		t.Errorf("/server on a dev run: %q", body)
	}
	for _, coding := range []string{"gzip", "identity"} {
		rsp, body := get(t, srv.URL+"/maps/zombie_theater/pipelines", "Accept-Encoding", coding)
		if coding == "gzip" {
			zr, err := gzip.NewReader(strings.NewReader(body))
			if err != nil {
				t.Fatalf("Kino's pipelines, gzipped: %v", err)
			}
			data, _ := io.ReadAll(zr)
			body = string(data)
		}
		if body != "BO1P list" || rsp.Header.Get("Cache-Control") != "no-cache" || rsp.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("Kino's pipelines (%s): %q (%s), want its list, revalidated", coding, body, rsp.Header)
		}
	}
	// a map with no list, one the page doesn't list (Dead Ops Arcade is off), one that is no map, and the list of
	// before maps had their own
	for _, path := range []string{"/maps/zombie_pentagon/pipelines", "/maps/zombietron/pipelines", "/maps/..%2Fpipelines%2Fzombie_theater/pipelines", "/pipelines.bin"} {
		if rsp, _ := get(t, srv.URL+path); rsp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404", path, rsp.Status)
		}
	}
	if rsp, _ := http.Post(srv.URL+"/maps/zombie_theater/pipelines", "application/octet-stream", strings.NewReader("BO1P")); rsp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("a post: %s, want 405", rsp.Status)
	}
}
