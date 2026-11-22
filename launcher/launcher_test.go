package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
	"zombies/internal/plutonium/plutoniumtest"
	"zombies/internal/release"
)

// fakeServer is a server's side of the launcher's calls (launcherapi), for the player of key.
type fakeServer struct {
	srv *httptest.Server
	key string

	mu       sync.Mutex
	play     launcherapi.Play
	plays    func(n int, play *launcherapi.Play) // changes what to play, at the nth time it is asked for
	fails    func(n int) bool                    // whether the nth call for what to play gets no answer
	asked    []string                            // each call: its method and path, and its Range header where it has one
	files    map[string]string                   // T6's game files, by path
	listed   map[string]string                   // what the list says of a file's contents instead, by path
	extra    []launcherapi.File                  // more of the list
	cut      map[string]int                      // a file's answers end after this many bytes of theirs, by path
	reports  []launcherapi.Report
	ready    []bool // what each call for what to play said of the PC: it has what the game needs
	launcher []byte
}

func hashOf(text string) string {
	sum := launcherapi.NewHash()
	sum.Write([]byte(text))
	return launcherapi.HashOf(sum)
}

func newFakeServer(t *testing.T) *fakeServer {
	l := &fakeServer{key: "0123456789abcdef", files: map[string]string{}, cut: map[string]int{},
		listed: map[string]string{}}
	l.srv = httptest.NewServer(http.HandlerFunc(l.serve))
	t.Cleanup(l.srv.Close)
	return l
}

// link is the link of the server's page, for its player.
func (l *fakeServer) link() string {
	return "zombies-launcher://" + strings.TrimPrefix(l.srv.URL, "http://") + "/play?key=" + l.key + "&tls=0"
}

func (l *fakeServer) port() int {
	_, port, _ := strings.Cut(strings.TrimPrefix(l.srv.URL, "http://"), ":")
	n, _ := strconv.Atoi(port)
	return n
}

// calls is what the launcher asked for since the last call, but what to play and its reports.
func (l *fakeServer) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var calls []string
	for _, c := range l.asked {
		if c != "GET /launcher/play" && c != "POST /launcher/files" {
			calls = append(calls, c)
		}
	}
	l.asked = nil
	return calls
}

func (l *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	if rng := r.Header.Get("Range"); rng != "" {
		call += " " + rng
	}
	l.asked = append(l.asked, call)
	if r.Header.Get("Authorization") != launcherapi.Key+l.key {
		http.Error(w, "no player's key", http.StatusUnauthorized)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/launcher/t6/files/")
	switch {
	case r.URL.Path == "/launcher/play":
		n := 0
		for _, c := range l.asked {
			if c == "GET /launcher/play" {
				n++
			}
		}
		if l.fails != nil && l.fails(n) {
			panic(http.ErrAbortHandler)
		}
		l.ready = append(l.ready, r.URL.Query().Get("ready") == "1")
		if l.plays != nil {
			l.plays(n, &l.play)
		}
		json.NewEncoder(w).Encode(l.play)
	case r.URL.Path == "/launcher/files" && r.Method == http.MethodPost:
		var rep launcherapi.Report
		json.NewDecoder(r.Body).Decode(&rep)
		l.reports = append(l.reports, rep)
		w.Write([]byte("{}"))
	case r.URL.Path == "/"+launcherapi.Setup && l.launcher != nil:
		w.Write(l.launcher)
	case r.URL.Path == "/launcher/t6/files" && l.play.Downloads:
		list := launcherapi.Files{Version: "v1", Files: append([]launcherapi.File{}, l.extra...)}
		for path, text := range l.files {
			f := launcherapi.File{Path: path, Size: int64(len(text)), Hash: hashOf(text)}
			if as, ok := l.listed[path]; ok {
				f.Hash = hashOf(as)
			}
			list.Files = append(list.Files, f)
			list.Bytes += f.Size
		}
		slices.SortFunc(list.Files, func(a, b launcherapi.File) int { return strings.Compare(a.Path, b.Path) })
		json.NewEncoder(w).Encode(list)
	case ok && l.play.Downloads:
		text, ok := l.files[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"`+hashOf(text)+`"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		if n, cut := l.cut[path]; cut {
			// the connection ends after n bytes of the answer, where it has more
			cw := &cutWriter{ResponseWriter: w, left: n}
			if http.ServeContent(cw, r, "", time.Time{}, strings.NewReader(text)); cw.cut {
				panic(http.ErrAbortHandler)
			}
			return
		}
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(text))
	default:
		http.NotFound(w, r)
	}
}

// cutWriter passes the first left bytes written to it, and drops the rest.
type cutWriter struct {
	http.ResponseWriter
	left int
	cut  bool
}

func (c *cutWriter) Write(p []byte) (int, error) {
	n := min(len(p), c.left)
	c.left -= n
	if n > 0 {
		c.ResponseWriter.Write(p[:n])
		c.ResponseWriter.(http.Flusher).Flush()
	}
	if n < len(p) {
		c.cut = true
		return n, errors.New("cut")
	}
	return n, nil
}

// started is what the launcher started: the program, its command line, and its folder.
type started struct{ exe, line, dir string }

// testLauncher is a launcher on a PC of its own (a folder), which a player answers as answers says, a line a question;
// what it says is on out, and what it starts and registers on the lists.
type testLauncher struct {
	*launcher
	out        *bytes.Buffer
	started    []started
	reran      []string
	registered []string
	later      []string // what it left to delete once it has ended
	// the games it started; whether one stays open once started, as a player's does, where a test says so: else it
	// is closed at once, and the launcher ends with it
	games     []*fakeGame
	gameStays bool
	button    chan struct{} // gets a value each time the player presses the room's button
	// the stand-in for Plutonium's updater, and the revision it serves
	updater  *plutoniumtest.Updater
	revision *int
	hidden   bool // its window is gone
}

// press is the player pressing the room's button, which asks the launcher to join again.
func (tl *testLauncher) press() {
	select {
	case tl.button <- struct{}{}:
	default:
	}
}

// fakeGame is a game the launcher started.
type fakeGame struct{ open, stopped atomic.Bool }

func (g *fakeGame) running() bool { return g.open.Load() }

func (g *fakeGame) stop() {
	g.stopped.Store(true)
	g.open.Store(false)
}

var plutoniumFiles = map[string]string{
	plutonium.Bootstrapper:        "the bootstrapper",
	"games/t6zm.exe":              "T6's Zombies",
	"games/t5sp.exe":              "another game",
	"storage/t6/zone/zm_patch.ff": "a zone",
}

func newTestLauncher(t *testing.T, answers string) *testLauncher {
	t.Helper()
	revision := 1
	updater := plutoniumtest.Serve(t, plutoniumFiles, &revision)
	root := t.TempDir()
	tl := &testLauncher{out: &bytes.Buffer{}, button: make(chan struct{}, 1), updater: updater, revision: &revision}
	tl.launcher = &launcher{
		home: filepath.Join(root, appName), plutonium: filepath.Join(root, "Plutonium"), manifest: updater.URL,
		client: newClient(), in: bufio.NewReader(strings.NewReader(answers)), out: tl.out, status: io.Discard,
		start: func(exe, line, dir string) (game, error) {
			tl.started = append(tl.started, started{exe, line, dir})
			g := &fakeGame{}
			g.open.Store(tl.gameStays)
			tl.games = append(tl.games, g)
			return g, nil
		},
		pressed: tl.button,
		hide:    func() { tl.hidden = true },
		rerun: func(exe, link string) error {
			tl.reran = append(tl.reran, exe+" "+link)
			return nil
		},
		register: func(exe string) error {
			tl.registered = append(tl.registered, exe)
			return nil
		},
		unregister: func() error {
			tl.registered = nil
			return nil
		},
		later: func(exe, dir string) error {
			tl.later = append(tl.later, exe, dir)
			return nil
		},
		copies: func() []string { return nil },
		// the game's own programs, as the tests' game files have them
		programs: map[string]string{"binkw32.dll": hashOf("bink")},
		tick:     10 * time.Millisecond,
		poll:     5 * time.Millisecond, // the tests' server holds no call, so the launcher asks it this often
		retry:    time.Millisecond,
	}
	tl.notify, tl.confirm = tl.tell, tl.yes
	return tl
}

// again is the launcher's next run on the same PC, with the player's answers to it.
func (tl *testLauncher) again(answers string) {
	tl.in = bufio.NewReader(strings.NewReader(answers))
	tl.out.Reset()
	tl.started, tl.launched, tl.handed = nil, false, false
}

func read(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, file, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

var t6Files = map[string]string{
	"zone/all/patch_zm.ff":   "the patch",
	"zone/all/zm_transit.ff": strings.Repeat("TranZit's zone, a big file. ", 40000),
	"binkw32.dll":            "bink",
	"video/zm (1).webm":      "a video",
	"empty.txt":              "",
}

func TestParseLink(t *testing.T) {
	for url, want := range map[string]link{
		"zombies-launcher://zombies.example.com/play?key=0123456789abcdef":          {"https://zombies.example.com", "zombies.example.com", true, "0123456789abcdef"},
		"zombies-launcher://Zombies.Example.com:8443/play?key=0123456789abcdef":     {"https://zombies.example.com:8443", "zombies.example.com", true, "0123456789abcdef"},
		"ZOMBIES-LAUNCHER://10.0.50.11:3075/play?key=0123456789abcdef&tls=0":        {"http://10.0.50.11:3075", "10.0.50.11", false, "0123456789abcdef"},
		"zombies-launcher://10.0.50.11:3075/play/?tls=0&key=0123456789abcdef":       {"http://10.0.50.11:3075", "10.0.50.11", false, "0123456789abcdef"},
		"zombies-launcher://tower/play?key=0123456789abcdef&tls=1":                  {"https://tower", "tower", true, "0123456789abcdef"},
		"zombies-launcher://host/play":                                              {},
		"zombies-launcher://host/play?key=":                                         {},
		"zombies-launcher://host/play?key=0123456789ABCDEF":                         {},
		"zombies-launcher://host/play?key=0123456789abcdef%26x":                     {},
		"zombies-launcher://host/other?key=0123456789abcdef":                        {},
		"zombies-launcher:///play?key=0123456789abcdef":                             {},
		"zombies:play?key=0123456789abcdef":                                         {},
		"zombies-launcher://user@host/play?key=0123456789abcdef":                    {},
		"zombies-launcher://host:0/play?key=0123456789abcdef":                       {},
		"zombies-launcher://host:65536/play?key=0123456789abcdef":                   {},
		"zombies-launcher://host:03075/play?key=0123456789abcdef":                   {},
		"zombies-launcher://[::1]:3075/play?key=0123456789abcdef":                   {},
		"zombies-launcher://ho_st/play?key=0123456789abcdef":                        {},
		"zombies-launcher://host%20+set%20x/play?key=0123456789abcdef":              {},
		"zombies-launcher://-host/play?key=0123456789abcdef":                        {},
		"https://host/play?key=0123456789abcdef":                                    {},
		"zombies-launcher://host/play?key=0123456789abcdef#x":                       {},
		`zombies-launcher://host/play?key=0123456789abcdef" +set x "`:               {},
		"zombies-launcher://host/../play?key=0123456789abcdef":                      {},
		"zombies-launcher://" + strings.Repeat("a", 254) + "/play?key=0123456789ab": {},
	} {
		got, err := parseLink(url)
		if got != want || (err == nil) != (want.key != "") {
			t.Errorf("%s: %+v, %v", url, got, err)
		}
	}
}

// One launcher a player: a link's id is its player's on its server, and says nothing of their key.
func TestLinkID(t *testing.T) {
	id := func(s string) string {
		l, err := parseLink(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		return l.id()
	}
	ann := id("zombies-launcher://zombies.example/play?key=0123456789abcdef")
	if len(ann) != 16 || strings.Contains(ann, "0123") || ann != id("ZOMBIES-LAUNCHER://zombies.example/play/?key=0123456789abcdef") {
		t.Errorf("a link's id: %q", ann)
	}
	for _, other := range []string{"zombies-launcher://zombies.example/play?key=0123456789abcdee",
		"zombies-launcher://other.example/play?key=0123456789abcdef", "zombies-launcher://zombies.example:444/play?key=0123456789abcdef"} {
		if id(other) == ann {
			t.Errorf("%s has the id of another player's link", other)
		}
	}
}

func TestCommandLine(t *testing.T) {
	const exe, dir = `C:\Users\a b\AppData\Local\Plutonium\bin\plutonium-bootstrapper-win32.exe`, `D:\Games\Black Ops II`
	for _, tc := range []struct{ dir, name, password, server, want string }{
		{dir, "Ann 2", "1a2b3c4d", "zombies.example.com:3075",
			`"` + exe + `" t6zm "` + dir + `" -lan +name "Ann 2" +set password 1a2b3c4d +connect zombies.example.com:3075`},
		// a folder's last backslash would escape the quote after it; a name has no quote, and is never empty
		{dir + `\`, `A"nn" +quit`, "x", "10.0.50.11:3075", `"` + exe + `" t6zm "` + dir + `" -lan +name "Ann quit" +set password x +connect 10.0.50.11:3075`},
		{dir, `";+`, "x", "h:1", `"` + exe + `" t6zm "` + dir + `" -lan +name "Player" +set password x +connect h:1`},
		{dir, "Ann", "", "h:1", ""},
		{dir, "Ann", "x y", "h:1", ""},
		{dir, "Ann", `x" +quit`, "h:1", ""},
		{dir, "Ann", "x", "h:1 +quit", ""},
		{dir, "Ann", "x", "h", ""},
		{`D:\Ga"mes`, "Ann", "x", "h:1", ""},
		{"", "Ann", "x", "h:1", ""},
	} {
		if got, err := commandLine(exe, tc.dir, tc.name, tc.password, tc.server); got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("%+v: %q, %v", tc, got, err)
		}
	}
}

func TestPlainFolder(t *testing.T) {
	sep := string(filepath.Separator)
	plain := filepath.Join(sep+"Users", "Ann B", "AppData", "Local", appName, "t6")
	if got := plainFolder(plain); got != plain {
		t.Errorf("a plain folder: %q", got)
	}
	if got, want := plainFolder(filepath.Join(sep+"Users", "Zoë", "AppData", "Local", appName, "t6")), filepath.Join(sep+appName, "t6"); got != want {
		t.Errorf("a folder with a letter the game can't read: %q", got)
	}
}

func TestSteamLibraries(t *testing.T) {
	steam := t.TempDir()
	write(t, filepath.Join(steam, "steamapps", "libraryfolders.vdf"), `"libraryfolders"
{
	"0"
	{
		"path"		"C:\\Program Files (x86)\\Steam"
		"label"		""
		"apps"
		{
			"212910"		"9000000000"
		}
	}
	"1"
	{
		"path"		"D:\\SteamLibrary"
	}
	"2"		"E:\\Old\\Library"
}
`)
	want := []string{
		filepath.Join(steam, "steamapps", "common", t6Folder),
		filepath.Join(`C:\Program Files (x86)\Steam`, "steamapps", "common", t6Folder),
		filepath.Join(`D:\SteamLibrary`, "steamapps", "common", t6Folder),
		filepath.Join(`E:\Old\Library`, "steamapps", "common", t6Folder),
	}
	if got := steamLibraries(steam); !slices.Equal(got, want) {
		t.Errorf("the libraries: %q", got)
	}
	if got := steamLibraries(filepath.Join(steam, "none")); len(got) != 1 {
		t.Errorf("of a Steam with no list of them: %q", got)
	}
}

// A link's run on a PC with nothing yet: the player says the PC plays on the server; the launcher gets Plutonium from
// its updater, and the game files from the server, into its folder, saying how far it is; and starts the player's
// game into the room's. The next run asks nothing, and downloads nothing.
func TestPlay(t *testing.T) {
	l := newFakeServer(t)
	l.files = t6Files
	l.play = launcherapi.Play{Player: "Ann", Downloads: true, Room: "ABCD",
		Game: &launcherapi.Game{Port: 4000, Password: "1a2b3c4d", Name: "Ann 2"}}
	tl := newTestLauncher(t, "\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the run: %d\n%s", code, tl.out)
	}
	base := l.srv.URL
	if cfg := read(t, tl.configFile()); !strings.Contains(cfg, `"`+base+`"`) {
		t.Errorf("the config: %s", cfg)
	}
	for name, text := range plutoniumFiles {
		got, err := os.ReadFile(filepath.Join(tl.plutonium, filepath.FromSlash(name)))
		if needed := name != "games/t5sp.exe"; needed && string(got) != text || !needed && err == nil {
			t.Errorf("Plutonium's %s: %q, %v", name, got, err)
		}
	}
	dir := filepath.Join(tl.home, "t6")
	for path, text := range t6Files {
		if got := read(t, filepath.Join(dir, filepath.FromSlash(path))); got != text {
			t.Errorf("the game's %s is %d bytes, not %d", path, len(got), len(text))
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*", "*", "*.part")); len(parts) > 0 {
		t.Errorf("left behind: %v", parts)
	}
	exe := filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	want := started{exe, fmt.Sprintf(`"%s" t6zm "%s" -lan +name "Ann 2" +set password 1a2b3c4d +connect 127.0.0.1:4000`, exe, dir),
		tl.plutonium}
	if len(tl.started) != 1 || tl.started[0] != want || !tl.launched {
		t.Errorf("started: %+v", tl.started)
	}
	l.mu.Lock()
	reports := l.reports
	l.mu.Unlock()
	var need int64
	for _, text := range t6Files {
		need += int64(len(text))
	}
	if n := len(reports); n < 2 || !reports[0].Running || reports[n-1] != (launcherapi.Report{Got: need, Need: need}) {
		t.Errorf("the reports: %+v", reports)
	}
	if calls := l.calls(); len(calls) != 1+len(t6Files) || calls[0] != "GET /launcher/t6/files" {
		t.Errorf("the first run asked for %q", calls)
	}
	kept := read(t, filepath.Join(tl.home, "launcher.log"))
	for _, say := range []string{"You are Ann on 127.0.0.1.", "Getting Plutonium", "Downloading 5 of the game's 5 files", "joins the room's game as Ann 2"} {
		if !strings.Contains(tl.out.String(), say) || !strings.Contains(kept, say) {
			t.Errorf("the launcher didn't say %q, or didn't keep it:\n%s", say, tl.out)
		}
	}
	if strings.Contains(kept, l.key) || strings.Contains(tl.out.String(), l.key) {
		t.Error("the launcher said the player's key")
	}
	// again: nothing to ask, nothing to get
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 || tl.started[0] != want {
		t.Fatalf("the second run: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	if calls := l.calls(); len(calls) != 1 || calls[0] != "GET /launcher/t6/files" {
		t.Errorf("the second run asked for %q", calls)
	}
	if strings.Contains(tl.out.String(), "Downloading") || strings.Contains(tl.out.String(), "Checking") {
		t.Errorf("the second run:\n%s", tl.out)
	}
	// the server's game files changed: one file is another, one is gone, one is new; and a file of the PC's was
	// changed behind the launcher's back
	now := map[string]string{}
	for path, text := range t6Files {
		now[path] = text
	}
	now["binkw32.dll"] = "bink, again"
	tl.programs["binkw32.dll"] = hashOf("bink, again") // the game's own still, as this launcher knows it
	delete(now, "video/zm (1).webm")
	now["zone/all/zm_tomb.ff"] = "Origins"
	l.mu.Lock()
	l.files = now
	l.mu.Unlock()
	write(t, filepath.Join(dir, "zone", "all", "patch_zm.ff"), "the pitch")
	write(t, filepath.Join(dir, "players", "settings.cfg"), "the player's")
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 {
		t.Fatalf("the third run: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	calls := l.calls()
	slices.Sort(calls)
	if want := []string{"GET /launcher/t6/files", "GET /launcher/t6/files/binkw32.dll", "GET /launcher/t6/files/zone/all/patch_zm.ff",
		"GET /launcher/t6/files/zone/all/zm_tomb.ff"}; !slices.Equal(calls, want) {
		t.Errorf("the third run asked for %q", calls)
	}
	for path, text := range now {
		if got := read(t, filepath.Join(dir, filepath.FromSlash(path))); got != text {
			t.Errorf("the game's %s after the change: %q", path, got)
		}
	}
	if isFile(filepath.Join(dir, "video", "zm (1).webm")) || !isFile(filepath.Join(dir, "players", "settings.cfg")) {
		t.Error("the folder kept a file the server has no more, or lost one of the player's")
	}
}

// held is a window that stands still at what is written to it, until let go: a console its player selects in.
type held struct{ let chan struct{} }

func (h held) Write(p []byte) (int, error) {
	<-h.let
	return len(p), nil
}

// A window that stands still holds neither a download nor its word to the server of how it goes: its last line
// waits for the window, and the files are all there by then.
func TestDownloadGoesOnPastItsWindow(t *testing.T) {
	l := newFakeServer(t)
	l.files = t6Files
	l.play = launcherapi.Play{Player: "Ann", Downloads: true}
	tl := newTestLauncher(t, "\n")
	window := held{make(chan struct{})}
	tl.status = window
	done := make(chan int)
	go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
	dir := filepath.Join(tl.home, "t6")
	have := func() bool {
		for path := range t6Files {
			if !isFile(filepath.Join(dir, filepath.FromSlash(path))) {
				return false
			}
		}
		return true
	}
	for deadline := time.Now().Add(10 * time.Second); !have(); time.Sleep(5 * time.Millisecond) {
		select {
		case code := <-done:
			t.Fatalf("the run ended before its window was let go: %d\n%s", code, tl.out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the download stood still with its window\n%s", tl.out)
		}
	}
	l.mu.Lock()
	reports := len(l.reports)
	l.mu.Unlock()
	if reports == 0 {
		t.Error("the server heard nothing of a download whose window stood still")
	}
	close(window.let)
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("the run: %d\n%s", code, tl.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end once its window was let go")
	}
}

// The launcher waits for a room's game that isn't up yet, and starts nothing for a player who is in it already, or in
// no room.
func TestPlayWaitsForTheGame(t *testing.T) {
	l := newFakeServer(t)
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD"}
	l.plays = func(n int, play *launcherapi.Play) {
		if n >= 5 {
			play.Game = &launcherapi.Game{Port: l.port(), Password: "pw", Name: "Ann"}
		}
	}
	tl := newTestLauncher(t, "")
	tl.copies = func() []string { return []string{filepath.Join(own, "none"), own} }
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the run: %d\n%s", code, tl.out)
	}
	if len(tl.started) != 1 || !strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan +name "Ann" +set password pw`) {
		t.Errorf("started: %+v", tl.started)
	}
	if out := tl.out.String(); !strings.Contains(out, "Waiting for room ABCD's game") || strings.Count(out, "Waiting") != 1 ||
		!strings.Contains(out, "your own copy, in "+own) {
		t.Errorf("the launcher said:\n%s", out)
	}
	if calls := l.calls(); len(calls) != 0 {
		t.Errorf("with a copy of the player's own, and a server that sends no files, the launcher asked for %q", calls)
	}
	// the server hears that the PC is ready from the wait on, and not before
	l.mu.Lock()
	if said := l.ready; len(said) < 5 || said[0] || !said[len(said)-1] || !slices.IsSortedFunc(said, func(a, b bool) int {
		if a == b {
			return 0
		} else if b {
			return -1
		}
		return 1
	}) {
		t.Errorf("the calls for what to play said the PC is ready, from the wait on: %v", said)
	}
	l.mu.Unlock()
	for what, play := range map[string]launcherapi.Play{
		"in the game already": {Player: "Ann", Room: "ABCD", InGame: true},
		"in no room":          {Player: "Ann"},
	} {
		l.mu.Lock()
		l.play, l.plays = play, nil
		l.mu.Unlock()
		tl.again("")
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 0 || tl.launched {
			t.Errorf("%s: %d, started %+v\n%s", what, code, tl.started, tl.out)
		}
	}
	// a server that can't be reached for a while doesn't end the wait
	down := 0
	l.mu.Lock()
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD"}
	l.plays = func(n int, play *launcherapi.Play) {
		if n > 6 {
			play.Game = &launcherapi.Game{Port: l.port(), Password: "pw", Name: "Ann"}
		}
	}
	l.fails = func(n int) bool { return n >= 3 && n < 3+down }
	l.mu.Unlock()
	for _, tc := range []struct {
		down    int
		started int
	}{{3, 1}, {8, 1}} {
		l.mu.Lock()
		down, l.asked, l.play = tc.down, nil, launcherapi.Play{Player: "Ann", Room: "ABCD"}
		l.mu.Unlock()
		tl.again("")
		code := tl.run(context.Background(), []string{l.link()})
		if len(tl.started) != tc.started || (code == 0) != (tc.started == 1) {
			t.Errorf("a server away for %d calls of a wait: %d, started %+v\n%s", tc.down, code, tl.started, tl.out)
		}
	}
	// the wait ends with the launcher's window
	l.mu.Lock()
	l.play, l.plays, l.fails, l.asked = launcherapi.Play{Player: "Ann", Room: "ABCD"}, nil, nil, nil
	l.mu.Unlock()
	tl.again("")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if code := tl.run(ctx, []string{l.link()}); code == 0 || len(tl.started) != 0 {
		t.Errorf("a wait that was stopped: %d, started %+v", code, tl.started)
	}
}

// The first server a PC's launcher is opened for is the one it plays on. Nothing is asked of any other, nor started,
// until the player says the PC plays there too; and a server on plain HTTP is one of the player's own network.
func TestTrust(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Game: &launcherapi.Game{Port: 4000, Password: "pw", Name: "Ann"}}
	other := newFakeServer(t)
	other.play = l.play
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	// the first server a launcher is opened for asks nothing
	tl := newTestLauncher(t, "")
	tl.copies = func() []string { return []string{own} }
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		strings.Contains(tl.out.String(), "trust") || !slices.Equal(tl.cfg.Servers, []string{l.srv.URL}) {
		t.Fatalf("the first server: %d, started %+v, servers %q\n%s", code, tl.started, tl.cfg.Servers, tl.out)
	}
	// any other does, once
	for _, answers := range []string{"", "n\n", "\n", "sure\n"} {
		tl.again(answers)
		if code := tl.run(context.Background(), []string{other.link()}); code == 0 || len(tl.started) != 0 ||
			!strings.Contains(tl.out.String(), "trust") {
			t.Errorf("another server, answered %q: %d, started %+v\n%s", answers, code, tl.started, tl.out)
		}
		other.mu.Lock()
		asked := other.asked
		other.mu.Unlock()
		if len(asked) != 0 || len(tl.cfg.Servers) != 1 {
			t.Errorf("another server, answered %q: it was asked for %q; servers %q", answers, asked, tl.cfg.Servers)
		}
	}
	for _, answers := range []string{"y\n", ""} {
		tl.again(answers)
		if code := tl.run(context.Background(), []string{other.link()}); code != 0 || len(tl.started) != 1 ||
			strings.Contains(tl.out.String(), "trust") != (answers != "") {
			t.Errorf("another server, answered %q: %d, started %+v\n%s", answers, code, tl.started, tl.out)
		}
	}
	tl = newTestLauncher(t, "")
	if code := tl.run(context.Background(), []string{"zombies-launcher://8.8.8.8:3075/play?key=0123456789abcdef&tls=0"}); code == 0 ||
		!strings.Contains(tl.out.String(), "no secure page") || isFile(tl.configFile()) {
		t.Errorf("a server on plain HTTP that is not of the PC's network: %d\n%s", code, tl.out)
	}
	// a key the server doesn't know
	tl = newTestLauncher(t, "")
	if code := tl.run(context.Background(), []string{strings.Replace(l.link(), l.key, "ffffffffffffffff", 1)}); code == 0 ||
		!strings.Contains(tl.out.String(), "doesn't know you") || len(tl.started) != 0 {
		t.Errorf("a key the server doesn't know: %d\n%s", code, tl.out)
	}
	// no link of the launcher's
	tl = newTestLauncher(t, "")
	if code := tl.run(context.Background(), []string{"zombies-launcher://host/play?key=x"}); code == 0 || len(tl.started) != 0 {
		t.Errorf("a link that is none: %d\n%s", code, tl.out)
	}
}

// A download that the connection cuts goes on from where it was: in the same run while each try brings more of the
// file, and in the next run from what the last left.
func TestSyncResumes(t *testing.T) {
	l := newFakeServer(t)
	l.files = t6Files
	l.play = launcherapi.Play{Player: "Ann", Downloads: true}
	big := t6Files["zone/all/zm_transit.ff"]
	const path = "zone/all/zm_transit.ff"
	// each answer ends after 400 kB: three tries bring the file's 1.1 MB
	l.cut[path] = 400 << 10
	tl := newTestLauncher(t, "\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the run: %d\n%s", code, tl.out)
	}
	dir := filepath.Join(tl.home, "t6")
	if got := read(t, filepath.Join(dir, filepath.FromSlash(path))); got != big {
		t.Errorf("the file, after a download in three parts: %d bytes of %d", len(got), len(big))
	}
	var tries []string
	for _, c := range l.calls() {
		if strings.Contains(c, path) {
			tries = append(tries, c)
		}
	}
	if len(tries) != 3 || !strings.HasSuffix(tries[1], " bytes=409600-") || !strings.HasSuffix(tries[2], " bytes=819200-") {
		t.Errorf("the tries: %q", tries)
	}
	l.mu.Lock()
	last := l.reports[len(l.reports)-1]
	l.mu.Unlock()
	if last.Running || last.Got != last.Need {
		t.Errorf("the last report: %+v", last)
	}
	// an answer that brings nothing more, five times: the run gives up, and the next goes on from what it left
	tl = newTestLauncher(t, "\n")
	dir = filepath.Join(tl.home, "t6")
	l.mu.Lock()
	l.cut[path] = 0
	l.reports = nil
	l.mu.Unlock()
	write(t, filepath.Join(dir, filepath.FromSlash(path)+launcherapi.PartSuffix), big[:1000])
	write(t, filepath.Join(dir, launcherapi.StateFile), `{"files": {}, "parts": {"`+path+`": "`+hashOf(big)+`"}}`)
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || !strings.Contains(tl.out.String(), "The download of "+path+" failed") {
		t.Fatalf("a download that brings nothing: %d\n%s", code, tl.out)
	}
	l.mu.Lock()
	last = l.reports[len(l.reports)-1]
	delete(l.cut, path)
	l.mu.Unlock()
	if last.Running || last.Got >= last.Need {
		t.Errorf("the last report of a download that failed: %+v", last)
	}
	l.calls()
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the next run: %d\n%s", code, tl.out)
	}
	if got := read(t, filepath.Join(dir, filepath.FromSlash(path))); got != big {
		t.Errorf("the file, after the next run: %d bytes of %d", len(got), len(big))
	}
	if calls := l.calls(); !slices.Contains(calls, "GET /launcher/t6/files/"+path+" bytes=1000-") {
		t.Errorf("the next run asked for %q", calls)
	}
	// the start of a file that is another now is no start: the file comes whole
	tl = newTestLauncher(t, "\n")
	dir = filepath.Join(tl.home, "t6")
	write(t, filepath.Join(dir, filepath.FromSlash(path)+launcherapi.PartSuffix), "another file's start")
	write(t, filepath.Join(dir, launcherapi.StateFile), `{"files": {}, "parts": {"`+path+`": "`+hashOf("another file")+`"}}`)
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the run: %d\n%s", code, tl.out)
	}
	if got := read(t, filepath.Join(dir, filepath.FromSlash(path))); got != big {
		t.Errorf("the file, with another's start in its place: %d bytes of %d", len(got), len(big))
	}
}

// A file takes its place only with the contents the server's list says, and only in the launcher's folder.
func TestSyncTakesOnlyTheListsFiles(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Downloads: true}
	l.files = map[string]string{"binkw32.dll": "bink", "zone/all/patch_zm.ff": "the patch"}
	l.listed["zone/all/patch_zm.ff"] = "another patch"
	tl := newTestLauncher(t, "\n")
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || !strings.Contains(tl.out.String(), "not the file the server's list says") {
		t.Fatalf("a file that isn't the list's: %d\n%s", code, tl.out)
	}
	dir := filepath.Join(tl.home, "t6")
	patch := filepath.Join(dir, "zone", "all", "patch_zm.ff")
	if isFile(patch) || isFile(patch+launcherapi.PartSuffix) {
		t.Error("a file that isn't the list's was kept")
	}
	for _, f := range []launcherapi.File{
		{Path: "../outside.dll", Size: 1, Hash: hashOf("x")},
		{Path: "zone/../../outside.dll", Size: 1, Hash: hashOf("x")},
		{Path: `C:\Windows\outside.dll`, Size: 1, Hash: hashOf("x")},
		{Path: "/outside.dll", Size: 1, Hash: hashOf("x")},
		{Path: "BINKW32.DLL", Size: 1, Hash: hashOf("x")},
		{Path: "ok.dll", Size: 1, Hash: "not a hash"},
		{Path: "ok.dll", Size: -1, Hash: hashOf("x")},
		{Path: launcherapi.StateFile, Size: 1, Hash: hashOf("x")},
		{Path: "ok.dll" + launcherapi.PartSuffix, Size: 1, Hash: hashOf("x")},
		// a program that is not the game's own: one it doesn't know, the game's with other contents, and the game's
		// under another name
		{Path: "bin/helper.exe", Size: 1, Hash: hashOf("x")},
		{Path: "redist/setup.cab", Size: 1, Hash: hashOf("x")},
		{Path: "zone/all/binkw32.dll", Size: 4, Hash: hashOf("bink")},
	} {
		l.mu.Lock()
		l.listed, l.extra = map[string]string{}, []launcherapi.File{f}
		l.mu.Unlock()
		tl := newTestLauncher(t, "\n")
		if code := tl.run(context.Background(), []string{l.link()}); code == 0 || !strings.Contains(tl.out.String(), "doesn't take") {
			t.Errorf("a list with %+v: %d\n%s", f, code, tl.out)
		}
		if isFile(filepath.Join(tl.home, "outside.dll")) || isFile(filepath.Join(filepath.Dir(tl.home), "outside.dll")) ||
			isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
			t.Errorf("a list with %+v: a file was written", f)
		}
	}
}

// The game files, on a PC without the game: the player's own copy where they say one, or another folder for the
// server's; with no server's to have, the launcher asks for the player's.
func TestGameFilesFolder(t *testing.T) {
	l := newFakeServer(t)
	l.files = map[string]string{"binkw32.dll": "bink", "zone/all/patch_zm.ff": "the patch", "zone/all/zm_tomb.ff": "Origins"}
	l.play = launcherapi.Play{Player: "Ann", Downloads: true, Room: "ABCD",
		Game: &launcherapi.Game{Port: 4000, Password: "pw", Name: "Ann"}}
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	// their own copy, typed at the question, in quotes as Explorer copies a path: kept, and read only
	tl := newTestLauncher(t, "\""+own+"\"\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) {
		t.Fatalf("the player's own copy, typed: %d, %+v\n%s", code, tl.started, tl.out)
	}
	if isFile(filepath.Join(own, "zone", "all", "zm_tomb.ff")) || isFile(filepath.Join(own, launcherapi.StateFile)) ||
		isFile(filepath.Join(tl.home, "t6", "binkw32.dll")) {
		t.Error("the launcher wrote to the player's own copy, or downloaded another")
	}
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.out.String(), "Your copy lacks files the server's has (zm_tomb.ff)") {
		t.Errorf("the next run, with the copy kept: %d, %+v\n%s", code, tl.started, tl.out)
	}
	// "none" for their copy: the server's files, whatever the PC has
	tl.cfg.T6 = "none"
	tl.saveConfig()
	tl.copies = func() []string { return []string{own} }
	tl.again("\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+filepath.Join(tl.home, "t6")+`" -lan`) {
		t.Errorf("with no copy of their own to use: %d, %+v\n%s", code, tl.started, tl.out)
	}
	// another folder for the server's files
	other := filepath.Join(t.TempDir(), "Games", "Zombies")
	tl = newTestLauncher(t, ""+other+"\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+other+`" -lan`) || read(t, filepath.Join(other, "binkw32.dll")) != "bink" {
		t.Fatalf("another folder for the files: %d, %+v\n%s", code, tl.started, tl.out)
	}
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !strings.Contains(tl.started[0].line, `t6zm "`+other+`" -lan`) ||
		strings.Contains(tl.out.String(), "Downloading") {
		t.Errorf("the next run, with the folder kept: %d, %+v\n%s", code, tl.started, tl.out)
	}
	tl = newTestLauncher(t, "Games\n")
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 {
		t.Errorf("a folder that is no full path: %d\n%s", code, tl.out)
	}
	// a server that sends no game files: the folder the launcher downloaded stays good; a PC with none is asked for one
	l.mu.Lock()
	l.play.Downloads = false
	l.mu.Unlock()
	tl = newTestLauncher(t, ""+other+"\n")
	tl.cfg.T6Files = other
	tl.saveConfig()
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !strings.Contains(tl.started[0].line, `t6zm "`+other+`" -lan`) {
		t.Errorf("the files downloaded earlier, with a server that sends none now: %d, %+v\n%s", code, tl.started, tl.out)
	}
	tl = newTestLauncher(t, ""+own+"\n")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !strings.Contains(tl.started[0].line, `t6zm "`+own+`" -lan`) {
		t.Errorf("a copy typed, with a server that sends no files: %d, %+v\n%s", code, tl.started, tl.out)
	}
	for _, answers := range []string{"y\n\n", "y\n" + filepath.Join(own, "zone") + "\n"} {
		tl = newTestLauncher(t, answers)
		if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 ||
			!strings.Contains(tl.out.String(), "has to be on this PC first") {
			t.Errorf("no copy, and no files from the server, answered %q: %d\n%s", answers, code, tl.out)
		}
	}
}

// A launcher with no window asks before its first download, with a yes-or-no question, and asks for no folder.
func TestGameFilesWithoutAWindow(t *testing.T) {
	l := newFakeServer(t)
	l.files = map[string]string{"binkw32.dll": "bink", "zone/all/patch_zm.ff": "the patch"}
	l.play = launcherapi.Play{Player: "Ann", Downloads: true, Room: "ABCD",
		Game: &launcherapi.Game{Port: 4000, Password: "pw", Name: "Ann"}}
	tl := newTestLauncher(t, "")
	tl.boxes = true
	var asked []string
	answer := false
	tl.confirm = func(question string) bool {
		asked = append(asked, question)
		return answer
	}
	files := filepath.Join(tl.home, "t6")
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 || len(asked) != 1 ||
		!strings.Contains(asked[0], "Download its game files from") || !strings.Contains(asked[0], files) ||
		isFile(filepath.Join(files, "binkw32.dll")) || !strings.Contains(tl.out.String(), "Nothing was downloaded.") {
		t.Fatalf("answered No: %d, started %+v, asked %q\n%s", code, tl.started, asked, tl.out)
	}
	answer = true
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 || len(asked) != 2 ||
		!strings.Contains(tl.started[0].line, `t6zm "`+files+`" -lan`) || read(t, filepath.Join(files, "binkw32.dll")) != "bink" {
		t.Fatalf("answered Yes: %d, started %+v, asked %q\n%s", code, tl.started, asked, tl.out)
	}
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 || len(asked) != 2 {
		t.Errorf("the next run, with the files downloaded: %d, started %+v, asked %q\n%s", code, tl.started, asked, tl.out)
	}
	// a server that sends no game files, and a PC with none: no question, since no one can type a folder
	l.mu.Lock()
	l.play.Downloads = false
	l.mu.Unlock()
	tl = newTestLauncher(t, "")
	tl.boxes = true
	tl.confirm = func(question string) bool {
		asked = append(asked, question)
		return true
	}
	if code := tl.run(context.Background(), []string{l.link()}); code == 0 || len(tl.started) != 0 || len(asked) != 2 ||
		strings.Contains(tl.out.String(), "type its folder") || !strings.Contains(tl.out.String(), "has to be on this PC first") {
		t.Errorf("no copy, and no files from the server: %d, asked %q\n%s", code, asked, tl.out)
	}
}

// The launcher asks Plutonium's updater only when the PC's copy of Plutonium is not of the server's revision, or lacks
// a file.
func TestMatchesTheServersPlutonium(t *testing.T) {
	l := newFakeServer(t)
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Plutonium: 1,
		Game: &launcherapi.Game{Port: 4000, Password: "pw", Name: "Ann"}}
	tl := newTestLauncher(t, "")
	tl.copies = func() []string { return []string{own} }
	run := func(what string, asks bool) {
		t.Helper()
		tl.again("")
		tl.updater.Asked()
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.started) != 1 {
			t.Fatalf("%s: %d, started %+v\n%s", what, code, tl.started, tl.out)
		}
		if asked := tl.updater.Asked(); (len(asked) > 0) != asks {
			t.Errorf("%s: the launcher asked Plutonium's updater for %q", what, asked)
		}
	}
	run("a PC without Plutonium", true)
	run("a copy of the server's revision", false)
	bootstrapper := filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	if err := os.Remove(bootstrapper); err != nil {
		t.Fatal(err)
	}
	run("a copy that lacks a file", true)
	if !isFile(bootstrapper) {
		t.Error("the file that was gone is not back")
	}
	*tl.revision = 2
	l.mu.Lock()
	l.play.Plutonium = 2
	l.mu.Unlock()
	run("a server of another revision", true)
	run("a copy of the server's new revision", false)
	l.mu.Lock()
	l.play.Plutonium = 0
	l.mu.Unlock()
	run("a server that states no revision", true)
}

// The launcher keeps one call open to a server that holds calls. The server answers when something changes, and the
// launcher asks again at once. It makes no call while nothing changes, and it tells the server at once when the PC is
// ready.
func TestWatchKeepsOneCallOpen(t *testing.T) {
	var mu sync.Mutex
	play := launcherapi.Play{Player: "Ann", Version: "v1"}
	changed := make(chan struct{})
	var calls []string // the query of each call
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.RawQuery)
		for r.URL.Query().Get("seen") == play.Version {
			wait := changed
			mu.Unlock()
			select {
			case <-wait:
			case <-r.Context().Done():
				return
			}
			mu.Lock()
		}
		answer := play
		mu.Unlock()
		json.NewEncoder(w).Encode(answer)
	}))
	defer web.Close()
	set := func(p launcherapi.Play) {
		mu.Lock()
		play = p
		close(changed)
		changed = make(chan struct{})
		mu.Unlock()
	}
	asked := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}

	s := newServer(link{base: web.URL, key: "0123456789abcdef"}, newClient())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watch(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	if got, err := s.current(ctx); err != nil || got.Version != "v1" {
		t.Fatalf("the first answer: %+v, %v", got, err)
	}
	// the server holds the second call: the launcher makes no other while nothing changes
	eventually(t, "the launcher asked again, with the answer it has", func() bool { return len(asked()) == 2 })
	time.Sleep(50 * time.Millisecond)
	if c := asked(); len(c) != 2 || c[0] != "" || c[1] != "seen=v1" {
		t.Fatalf("the calls while nothing changed: %q", c)
	}
	// a change answers the held call, and the launcher has the new answer at once
	_, next, _ := s.latest()
	set(launcherapi.Play{Player: "Ann", Room: "ABCD", Version: "v2"})
	select {
	case <-next:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not hear of the change")
	}
	if got, _, err := s.latest(); err != nil || got.Room != "ABCD" {
		t.Fatalf("the answer after the change: %+v, %v", got, err)
	}
	// the PC is ready: the launcher says so at once, with a call in place of the one that was held. The server may see
	// the call that the launcher gave up after the call that replaced it, so the new call is not always the last one.
	s.setReady(true)
	eventually(t, "the launcher said the PC is ready", func() bool {
		return slices.Contains(asked(), "ready=1&seen=v2")
	})
}

// A proxy in front of the server may cut a call that the server holds. The launcher then asks again at once, however
// often that happens.
func TestWatchOutlastsCallsThatAreCut(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time // when each call came
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		if r.URL.Query().Get("seen") == "" {
			json.NewEncoder(w).Encode(launcherapi.Play{Player: "Ann", Version: "v1"})
			return
		}
		time.Sleep(30 * time.Millisecond) // held, and then cut
		panic(http.ErrAbortHandler)
	}))
	defer web.Close()
	s := newServer(link{base: web.URL, key: "0123456789abcdef"}, newClient())
	s.retry = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watch(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	calls := func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(at)
	}
	eventually(t, "the launcher asked again after each call that was cut", func() bool { return len(calls()) >= 24 })
	c := calls()
	if gap := c[23].Sub(c[21]); gap > 500*time.Millisecond {
		t.Errorf("after eleven calls that were cut, the launcher took %s to ask again", gap)
	}
}

// After calls that failed, the launcher's call tries again by itself, and then reports once more what its last try
// failed at, which the server may have missed.
func TestWatchTriesAgain(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann"}
	l.fails = func(n int) bool { return n >= 2 && n < 5 }
	s := newServer(link{base: l.srv.URL, key: l.key}, newClient())
	s.poll, s.retry = time.Millisecond, time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watch(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	if _, err := s.current(ctx); err != nil {
		t.Fatalf("the first answer: %v", err)
	}
	s.fail(ctx, "Black Ops II has to be on this PC first.")
	failures := func() int {
		l.mu.Lock()
		defer l.mu.Unlock()
		n := 0
		for _, r := range l.reports {
			if r.Failed != "" {
				n++
			}
		}
		return n
	}
	eventually(t, "the launcher reported the failure again once the server answered", func() bool { return failures() == 2 })
	time.Sleep(20 * time.Millisecond)
	if n := failures(); n != 2 {
		t.Errorf("the failure was reported %d times", n)
	}
	if _, _, err := s.latest(); err != nil {
		t.Errorf("the launcher's call after the server answered again: %v", err)
	}
}

// The launcher stays with the game it started, for as long as that runs: it starts it anew for the room's next game,
// and when the player asks to join again while the server has their game in none; and ends once it is closed.
func TestStaysWithTheGame(t *testing.T) {
	l := newFakeServer(t)
	gameOf := func(id string) *launcherapi.Game {
		return &launcherapi.Game{ID: id, Port: l.port(), Password: "pw", Name: "Ann"}
	}
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD", Game: gameOf("first")}
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	tl := newTestLauncher(t, "")
	tl.copies = func() []string { return []string{own} }
	var mu sync.Mutex
	var games []*fakeGame
	tl.start = func(exe, line, dir string) (game, error) {
		g := &fakeGame{}
		g.open.Store(true)
		mu.Lock()
		games = append(games, g)
		mu.Unlock()
		return g, nil
	}
	started := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(games)
	}
	set := func(change func(play *launcherapi.Play)) {
		l.mu.Lock()
		change(&l.play)
		l.mu.Unlock()
	}
	// stays checks that the launcher starts no other game for a while
	stays := func(what string, want int) {
		t.Helper()
		time.Sleep(100 * time.Millisecond)
		if n := started(); n != want {
			t.Fatalf("%s: it has started %d games, not %d\n%s", what, n, want, tl.out)
		}
	}
	done := make(chan int)
	go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
	eventually(t, "the launcher started the player's game", func() bool { return started() == 1 })
	stays("the game runs on, and the room's is the same", 1)
	set(func(play *launcherapi.Play) { play.Game = gameOf("second") })
	eventually(t, "the launcher started the player's game again for the room's next game", func() bool { return started() == 2 })
	stays("the room's next game", 2)
	tl.press() // the player presses Join the game, their game in none
	eventually(t, "the launcher started the player's game again as they asked", func() bool { return started() == 3 })
	set(func(play *launcherapi.Play) { play.InGame, play.Game = true, nil }) // the server has their game in the room's
	time.Sleep(50 * time.Millisecond)
	tl.press()
	stays("asked to join again, in the game", 3)
	set(func(play *launcherapi.Play) { play.InGame, play.Game = false, nil }) // game over: the room is open
	stays("no game", 3)
	mu.Lock()
	games[2].open.Store(false) // the player closes the game
	mu.Unlock()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("the run: %d\n%s", code, tl.out)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("the launcher did not end with its game\n%s", tl.out)
	}
	if !tl.hidden {
		t.Errorf("the launcher's window is not gone\n%s", tl.out)
	}
	if !games[0].stopped.Load() || !games[1].stopped.Load() || games[2].stopped.Load() {
		t.Errorf("the games it closed for another: %v, %v, %v", games[0].stopped.Load(), games[1].stopped.Load(), games[2].stopped.Load())
	}
}

// Installed, the launcher is in its folder, and Windows starts it there; and the server's build takes its place when
// it is another, in the same run.
func TestInstallAndUpdate(t *testing.T) {
	tl := newTestLauncher(t, "")
	downloaded := filepath.Join(t.TempDir(), "zombies-launcher-setup (1).exe")
	write(t, downloaded, "the launcher, build 1")
	tl.exe = downloaded
	if code := tl.run(context.Background(), nil); code != 0 {
		t.Fatalf("the install: %d\n%s", code, tl.out)
	}
	installed := filepath.Join(tl.home, exeName)
	if read(t, installed) != "the launcher, build 1" || !slices.Equal(tl.registered, []string{installed}) {
		t.Errorf("installed: %q, registered %q", read(t, installed), tl.registered)
	}
	// again, from its folder, and with a newer download over an installed one
	tl.exe = installed
	if code := tl.run(context.Background(), nil); code != 0 || read(t, installed) != "the launcher, build 1" {
		t.Errorf("the install again, from its folder: %d\n%s", code, tl.out)
	}
	write(t, downloaded, "the launcher, build 2")
	tl.exe = downloaded
	if code := tl.run(context.Background(), nil); code != 0 || read(t, installed) != "the launcher, build 2" ||
		read(t, installed+".old") != "the launcher, build 1" {
		t.Errorf("the install over an older one: %d\n%s", code, tl.out)
	}
	// a link's run, by the installed launcher: the server's build is another
	l := newFakeServer(t)
	l.launcher = []byte("the launcher, build 3")
	l.play = launcherapi.Play{Player: "Ann", Launcher: hashOf("the launcher, build 3"), Room: "ABCD",
		Game: &launcherapi.Game{Port: 4000, Password: "pw", Name: "Ann"}}
	// which is a later build of the launcher's maker, as their signed statement of it says
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	signed := func(version int64, program string) *release.Signed {
		s, _ := release.Sign(key, release.Statement{Version: version, Hash: release.HashOf([]byte(program))})
		return &s
	}
	tl.key, _ = release.ParsePublic(public)
	tl.version = 2
	l.play.Release = signed(3, "the launcher, build 3")
	tl.exe = installed
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !tl.handed || len(tl.started) != 0 {
		t.Fatalf("a run with another build at the server: %d, handed on: %v, started %+v\n%s", code, tl.handed, tl.started, tl.out)
	}
	if read(t, installed) != "the launcher, build 3" || !slices.Equal(tl.reran, []string{installed + " " + l.link()}) {
		t.Errorf("after the update: %q, and ran again: %q", read(t, installed), tl.reran)
	}
	if isFile(filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))) {
		t.Error("the launcher that handed on went on itself")
	}
	// the build that runs is the server's: on with it
	tl.again("")
	tl.reran = nil
	write(t, filepath.Join(tl.home, "t6", "placeholder"), "")
	tl.copies = func() []string { return nil }
	tl.cfg.T6 = ""
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	tl.copies = func() []string { return []string{own} }
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.reran) != 0 || len(tl.started) != 1 {
		t.Errorf("a run of the server's build: %d, ran again %q, started %+v\n%s", code, tl.reran, tl.started, tl.out)
	}
	if isFile(installed + ".old") {
		t.Error("the build that stepped aside is still there")
	}
	// what the server sends is not the launcher it named: this one stays, and goes on
	l.mu.Lock()
	l.launcher = []byte("something else")
	l.play.Launcher = hashOf("the launcher, build 4")
	l.play.Release = signed(4, "the launcher, build 4")
	l.mu.Unlock()
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.reran) != 0 || len(tl.started) != 1 ||
		read(t, installed) != "the launcher, build 3" || !strings.Contains(tl.out.String(), "could not take") {
		t.Errorf("an update that isn't the one named: %d, ran again %q, started %+v\n%s", code, tl.reran, tl.started, tl.out)
	}
	// a launcher that isn't the installed one (a build of the developer's) takes none
	l.mu.Lock()
	l.launcher = []byte("the launcher, build 4")
	l.mu.Unlock()
	tl.exe = downloaded
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || len(tl.reran) != 0 || len(tl.started) != 1 ||
		read(t, installed) != "the launcher, build 3" {
		t.Errorf("a launcher outside its folder: %d, ran again %q\n%s", code, tl.reran, tl.out)
	}
	if code := tl.run(context.Background(), []string{"something", "else"}); code != 2 {
		t.Errorf("a command line that is none: %d", code)
	}
}

// Uninstalled, the launcher is off the PC: Windows starts it no more, and its folder is gone with what it kept there.
// The game files it downloaded go when the player says so: each of them, and nothing else that is in their folder.
func TestUninstall(t *testing.T) {
	gone := func(file string) bool {
		_, err := os.Stat(file)
		return errors.Is(err, fs.ErrNotExist)
	}
	install := func(tl *testLauncher) (installed string) {
		t.Helper()
		tl.exe = filepath.Join(t.TempDir(), launcherapi.Setup)
		write(t, tl.exe, "the launcher")
		if code := tl.run(context.Background(), nil); code != 0 || len(tl.registered) != 1 {
			t.Fatalf("the install: %d, registered %q\n%s", code, tl.registered, tl.out)
		}
		// what a link's runs left: the player's answers, and what the last run said
		tl.cfg = config{Servers: []string{"https://zombies.example"}}
		if err := tl.saveConfig(); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(tl.home, "launcher.log"), "a run")
		tl.exe = filepath.Join(tl.home, exeName)
		return tl.exe
	}
	for _, c := range []struct {
		answers string
		wipe    bool
	}{{"y\n", true}, {"n\n", false}, {"", false}} {
		tl := newTestLauncher(t, "")
		installed := install(tl)
		// the game files it downloaded, one of them half; in a folder with a file the game left there too
		files := filepath.Join(tl.home, "t6")
		theirs := []string{"t6zm.exe", "zone/all/patch_zm.ff", "zone/all/zm_transit.ff.part", launcherapi.StateFile}
		for _, name := range theirs[:3] {
			write(t, filepath.Join(files, filepath.FromSlash(name)), "contents")
		}
		state, _ := json.Marshal(syncState{Files: map[string]syncHas{"t6zm.exe": {Size: 8}, "zone/all/patch_zm.ff": {Size: 8},
			"../../outside": {Size: 8}}, Parts: map[string]string{"zone/all/zm_transit.ff": hashOf("the whole of it")}})
		write(t, filepath.Join(files, launcherapi.StateFile), string(state))
		own := filepath.Join(files, "players", "config.cfg")
		write(t, own, "the player's own")
		outside := filepath.Join(tl.home, "..", "outside")
		write(t, outside, "another program's")
		tl.again(c.answers)
		if code := tl.run(context.Background(), []string{"uninstall"}); code != 0 || len(tl.registered) != 0 {
			t.Fatalf("uninstalled, answered %q: %d, registered %q\n%s", c.answers, code, tl.registered, tl.out)
		}
		for _, file := range []string{installed, tl.configFile(), filepath.Join(tl.home, "launcher.log")} {
			if !gone(file) {
				t.Errorf("uninstalled, answered %q: %s is still there", c.answers, file)
			}
		}
		for _, name := range theirs {
			if file := filepath.Join(files, filepath.FromSlash(name)); gone(file) != c.wipe {
				t.Errorf("uninstalled, answered %q: %s gone: %v", c.answers, name, !c.wipe)
			}
		}
		if gone(filepath.Join(files, "zone")) != c.wipe {
			t.Errorf("uninstalled, answered %q: the folder of the files it downloaded gone: %v", c.answers, !c.wipe)
		}
		if read(t, own) != "the player's own" || read(t, outside) != "another program's" {
			t.Errorf("uninstalled, answered %q: it took files that weren't its own", c.answers)
		}
		if out := tl.out.String(); !strings.Contains(out, "Also delete") || !strings.Contains(out, files) {
			t.Errorf("uninstalled, answered %q: it asked\n%s", c.answers, out)
		}
		if len(tl.later) != 0 || tl.atExit != nil {
			t.Errorf("uninstalled, answered %q: left for later: %q", c.answers, tl.later)
		}
	}
	// nothing downloaded: it asks nothing, and its folder goes whole; Plutonium's stays
	tl := newTestLauncher(t, "")
	install(tl)
	bootstrapper := filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	write(t, bootstrapper, "the bootstrapper")
	tl.again("")
	if code := tl.run(context.Background(), []string{"uninstall"}); code != 0 || !gone(tl.home) || gone(bootstrapper) ||
		strings.Contains(tl.out.String(), "delete") {
		t.Errorf("uninstalled, with nothing downloaded: %d\n%s", code, tl.out)
	}
	// and once more, with nothing there: as well
	tl.again("")
	if code := tl.run(context.Background(), []string{"uninstall"}); code != 0 {
		t.Errorf("uninstalled twice: %d\n%s", code, tl.out)
	}
	// a program that can't be deleted, as one that runs can't on Windows: it goes once the run has ended
	tl = newTestLauncher(t, "")
	installed := install(tl)
	os.Remove(installed)
	write(t, filepath.Join(installed, "in use"), "") // what no Remove takes: a folder with a file in it
	tl.again("")
	if code := tl.run(context.Background(), []string{"uninstall"}); code != 0 || tl.atExit == nil || len(tl.later) != 0 {
		t.Fatalf("uninstalled as it runs: %d, left for the end: %v\n%s", code, tl.atExit != nil, tl.out)
	}
	tl.atExit()
	if !slices.Equal(tl.later, []string{installed, tl.home}) {
		t.Errorf("uninstalled as it runs: left for later %q", tl.later)
	}
}
