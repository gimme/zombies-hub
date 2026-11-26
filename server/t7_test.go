package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"zombies/internal/boiii"
	"zombies/internal/boiii/boiiitest"
)

// t7GameFiles makes a Black Ops III install and one of Steam's dedicated server of it, with the given maps, in a new
// folder, and returns the two.
func t7GameFiles(t *testing.T, maps ...string) (dir, server string) {
	t.Helper()
	root := t.TempDir()
	dir, server = filepath.Join(root, t7), filepath.Join(root, t7Server)
	files := []string{"BlackOps3.exe", "zone/base.xpak", "zone/core_common.ff", "zone/zm_common.ff", "zone/zm_common.fd",
		"zone/zm_patch.ff", "zone/zm_levelcommon.ff", "zone/zm_levelcommon.xpak", "zone/en_zm_common.ff",
		"zone/snd/all/zm_common.all.sabs"}
	for _, m := range maps {
		files = append(files, "zone/"+m+".ff", "zone/"+m+".xpak", "zone/en_"+m+".ff", "zone/"+m+"_patch.ff")
	}
	for _, name := range files {
		writeTestFile(t, filepath.Join(dir, filepath.FromSlash(name)))
	}
	for _, name := range []string{t7Exe, "UnrankedServer/steam_api64.dll", "UnrankedServer/steamclient64.dll",
		"UnrankedServer/tier0_s64.dll", "UnrankedServer/vstdlib_s64.dll", "UnrankedServer/codlogo.bmp",
		"UnrankedServer/machinecfg/playlists.info", "UnrankedServer/Launch_Server.bat",
		"UnrankedServer/zone/core_common.ff", "UnrankedServer/zone/core_common.fd", "UnrankedServer/zone/en_core_common.ff",
		"UnrankedServer/zone/mp_common.ff", "UnrankedServer/zone/mp_city.ff", "UnrankedServer/zone/en_mp_city.fd"} {
		writeTestFile(t, filepath.Join(server, filepath.FromSlash(name)))
	}
	return dir, server
}

func writeTestFile(t *testing.T, file string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(filepath.Base(file)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The game files have a T7 map when the game's install has its zone, and none without Steam's dedicated server or
// without a fastfile that every map's game loads.
func TestT7Installed(t *testing.T) {
	dir, server := t7GameFiles(t, "zm_factory", "zm_tomb", "zm_nomap")
	if maps := t7Installed(dir, server); !slices.Equal(maps, []string{"zm_factory", "zm_tomb"}) {
		t.Errorf("maps: %v", maps)
	}
	if maps := t7Installed(dir, filepath.Join(server, "none")); maps != nil {
		t.Errorf("maps without Steam's server: %v", maps)
	}
	os.Remove(filepath.Join(dir, "zone", "zm_common.ff"))
	if maps := t7Installed(dir, server); maps != nil {
		t.Errorf("maps without zm_common.ff: %v", maps)
	}
}

func TestT7Reads(t *testing.T) {
	for name, want := range map[string][2]bool{ // of the game's zone folder, of the server's
		"zm_factory.ff":          {true, true},
		"en_zm_factory_patch.ff": {true, true},
		"zm_common.fd":           {true, true},
		"zm_factory.xpak":        {false, false},
		"core_common.ff":         {false, true},
		"en_core_ui.fd":          {false, true},
		"mp_city.ff":             {false, false},
		"en_mp_common.fd":        {false, false},
		"base.xpak":              {false, false},
		"server_zm.cfg":          {false, false},
	} {
		if got := [2]bool{t7Reads(name, false), t7Reads(name, true)}; got != want {
			t.Errorf("%s: read from the game's folder %v, from the server's %v", name, got[0], got[1])
		}
	}
}

// A T7 game's home is the game's folder: links to Steam's server, to its fastfiles and the game's of Zombies, to what
// the server fetched and to BOIII's program, with the home's own config and script. A run that fetches has neither the
// fetched fastfile nor the config's line that keeps the game from asking for it.
func TestT7Prepare(t *testing.T) {
	dir, server := t7GameFiles(t, "zm_factory")
	u := boiiitest.Serve(t, map[string]string{boiii.Exe: "the program", "data/launcher/main.html": "a page"})
	copy := &t7Copy{Copy: &boiii.Copy{Dir: filepath.Join(t.TempDir(), "boiii"), URL: u.URL, Client: u.Client,
		State: "files.json"}}
	writeTestFile(t, filepath.Join(copy.lpc(), "core_sv_ffotd_tu32_593.ff"))
	t.Setenv("WINEPREFIX", "") // no Wine here, so no user of it to give BOIII's data
	newGame := func() *game {
		g := &game{name: "ABCD", dir: dir, home: filepath.Join(t.TempDir(), "home"), ip: "127.0.0.1", port: 3100,
			engine: newT7Game(copy, server, "zm_factory", 4, "secret")}
		if err := os.MkdirAll(g.home, 0o755); err != nil {
			t.Fatal(err)
		}
		return g
	}
	g := newGame()
	command, in, clean, err := g.engine.prepare(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	defer g.t7().leave()
	if in != g.home || clean == nil || command[0] != boiii.Exe || !slices.Contains(command, "-dedicated") ||
		!strings.Contains(strings.Join(command, " "), "+set net_port 3100 +set net_ip 127.0.0.1") {
		t.Errorf("command %q, run in %s", command, in)
	}
	for name, to := range map[string]string{
		boiii.Exe:                               filepath.Join(copy.Dir, boiii.Exe),
		"BlackOps3_UnrankedDedicatedServer.exe": filepath.Join(server, filepath.FromSlash(t7Exe)),
		"machinecfg":                            filepath.Join(server, "UnrankedServer", "machinecfg"),
		"zone/core_common.ff":                   filepath.Join(server, "UnrankedServer", "zone", "core_common.ff"),
		"zone/zm_factory.ff":                    filepath.Join(dir, "zone", "zm_factory.ff"),
		"zone/en_zm_factory.ff":                 filepath.Join(dir, "zone", "en_zm_factory.ff"),
		"zone/zm_common.fd":                     filepath.Join(dir, "zone", "zm_common.fd"),
		"zone/core_sv_ffotd_tu32_593.ff":        filepath.Join(copy.lpc(), "core_sv_ffotd_tu32_593.ff"),
	} {
		if got, err := os.Readlink(filepath.Join(g.home, filepath.FromSlash(name))); err != nil || got != to {
			t.Errorf("%s links to %q (%v), not to %s", name, got, err, to)
		}
	}
	for _, name := range []string{"zone/mp_city.ff", "zone/zm_factory.xpak", "zone/base.xpak", "Launch_Server.bat"} {
		if _, err := os.Lstat(filepath.Join(g.home, filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Errorf("the home has %s: %v", name, err)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(g.home, "zone", "lobby.cfg"))
	for _, line := range []string{`set g_password "secret"`, "set com_maxclients 4", "set live_useLPC 0",
		`set sv_maprotation "gametype zclassic map zm_factory"`, "set sv_lanOnly 1",
		fmt.Sprintf(`set rcon_password "%s"`, g.t7().rcon)} {
		if !strings.Contains(string(cfg), line+"\n") {
			t.Errorf("the config lacks %q:\n%s", line, cfg)
		}
	}
	if script, _ := os.ReadFile(filepath.Join(g.home, "boiii", "custom_scripts", "zm", "lobby.gsc")); string(script) != t7Script {
		t.Errorf("the home's script is not the server's: %d bytes", len(script))
	}
	if copy.Build() == "" || copy.runs.Load() != 1 {
		t.Errorf("the copy of BOIII: build %q, %d games run from it", copy.Build(), copy.runs.Load())
	}

	f := newGame()
	f.t7().fetch, f.t7().mapName = true, ""
	if _, _, _, err := f.engine.prepare(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	defer f.t7().leave()
	cfg, _ = os.ReadFile(filepath.Join(f.home, "zone", "lobby.cfg"))
	if strings.Contains(string(cfg), "live_useLPC") || strings.Contains(string(cfg), "sv_maprotation") {
		t.Errorf("the config of a run that fetches:\n%s", cfg)
	}
	if _, err := os.Lstat(filepath.Join(f.home, "zone", "core_sv_ffotd_tu32_593.ff")); !os.IsNotExist(err) {
		t.Errorf("a run that fetches has the fetched fastfile already: %v", err)
	}

	bad := newGame()
	bad.t7().mapName = "zm_classic_tomb"
	if _, _, _, err := bad.engine.prepare(context.Background(), bad); err == nil {
		t.Error("a game of a T6 map was prepared")
	}
}

// No T7 game starts before the server has what Steam's server fetches, or on a server without the game.
func TestT7Free(t *testing.T) {
	title := &t7Title{}
	if err := title.free(nil); err == nil || !strings.Contains(err.Error(), "doesn't have Black Ops III") {
		t.Errorf("a server without the game: %v", err)
	}
	title.copy = &t7Copy{Copy: &boiii.Copy{Dir: t.TempDir(), Client: &http.Client{}}}
	title.newGame = func(string, string, int, int, string) *game { return nil }
	if err := title.free(nil); err == nil || !strings.Contains(err.Error(), "getting Black Ops III ready") {
		t.Errorf("a server that has not fetched: %v", err)
	}
	writeTestFile(t, filepath.Join(title.copy.lpc(), "core_sv_ffotd_tu32_593.ff"))
	if err := title.free(nil); err != nil {
		t.Errorf("a server that has fetched: %v", err)
	}
}

func TestT7StatusLine(t *testing.T) {
	const row = "%3d %5d %4s %16s %-16s %-24s %6d"
	for _, tc := range []struct {
		line           string
		c              pcClient
		client, status bool
	}{
		{line: "---------- Live ----------", status: true},
		{line: "ffotd version 2", status: true},
		{line: "Local IP: 127.0.0.1:3100", status: true},
		{line: "We are hosting a lobby with 0 members:", status: true},
		{line: "map: zm_factory", status: true},
		{line: "num score ping xuid             name             address                  qport  ", status: true},
		{line: "--- ----- ---- ---------------- ---------------- ------------------------ ------ ", status: true},
		{line: "Server is not running.", status: true},
		{fmt.Sprintf(row, 0, 1500, "32", "110000112345678", "Ann", "127.1.0.1:28960", -1234),
			pcClient{0, "Ann", "127.1.0.1:28960", true}, true, true},
		{fmt.Sprintf(row, 3, 0, "CNCT", "1100001aabbccdd", "Ann B", "127.1.0.7:28960", 4321),
			pcClient{3, "Ann B", "127.1.0.7:28960", false}, true, true},
		{fmt.Sprintf(row, 1, 0, "999", "1100001aabbccdd", "Cy", "127.1.0.8:28960", 4321),
			pcClient{1, "Cy", "127.1.0.8:28960", false}, true, true},
		{line: fmt.Sprintf(row, 2, 0, "0", "bot0", "bot2", "bot", 0)},
		{line: "zombies_lobby: round 2"},
		{line: `Unknown command "clientkick"`},
		{line: ""},
	} {
		if c, client, status := t7StatusLine(tc.line); c != tc.c || client != tc.client || status != tc.status {
			t.Errorf("%q: %+v, %v, %v", tc.line, c, client, status)
		}
	}
}

// The log of a T7 game has the game's output without its colours, and without its answers to the server's status
// command, which the game writes there each second.
func TestT7Clean(t *testing.T) {
	answer := []string{"---------- Live ----------", "ffotd version 2", "playlist 1 version 325120000",
		"protocol 100 version 18532", "Local IP: 127.0.0.1:3100", "Public IP: 1.2.3.4:3074", "Mod Loaded: usermaps",
		"---------- Steam ----------", "Server Browser: true", "Map: zm_factory", "Gametype: zclassic",
		"---------- Lobby Info ----------", "We are hosting a lobby with 1 members:", "Ann is here, whatever a member's line is",
		"---------- Server ----------", "map: zm_factory",
		"num score ping xuid             name             address                  qport  ",
		"--- ----- ---- ---------------- ---------------- ------------------------ ------ ",
		"  0  1500   32  110000112345678 Ann              127.1.0.1:28960           -1234 ", ""}
	var in []string
	in = append(in, "^1A required game file is missing^7 (zone 'x')", "gamename: Call of Duty\xae")
	in = append(in, answer...)
	in = append(in, "zombies_lobby: round 1")
	in = append(in, answer[:8]...) // an answer that another line cuts short
	in = append(in, "zombies_lobby: over 3", "Map: said outside an answer", "---------- Live ----------",
		"ffotd version 0", "Server is not running.", "quitting...")
	clean := t7Clean()
	var out []string
	for _, line := range in {
		if line = clean(line); line != "" {
			out = append(out, line)
		}
	}
	want := []string{"A required game file is missing (zone 'x')", "gamename: Call of Duty", "zombies_lobby: round 1",
		"zombies_lobby: over 3", "Map: said outside an answer", "quitting..."}
	if !slices.Equal(out, want) {
		t.Errorf("the log has\n%q, not\n%q", out, want)
	}
}

// What Steam's server fetched is kept for its build: after an update of the game, the server has nothing fetched, and
// what it then keeps replaces the other build's.
func TestT7FetchedIsForTheBuild(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "server.exe")
	os.WriteFile(exe, []byte("build 1"), 0o644)
	first := &t7Copy{Copy: &boiii.Copy{Dir: filepath.Join(dir, "boiii")}, exe: exe}
	home := filepath.Join(dir, "home")
	writeTestFile(t, filepath.Join(home, "identities", "fetch", "LPC", "core_sv_ffotd_tu32_593.ff"))
	if err := (&t7Game{copy: first}).keep(home); err != nil || len(first.fetched()) != 1 {
		t.Fatalf("the first build's fastfile: %v, %v", first.fetched(), err)
	}
	if same := (&t7Copy{Copy: first.Copy, exe: exe}); len(same.fetched()) != 1 {
		t.Errorf("the same build, on the next start, has %v", same.fetched())
	}

	os.WriteFile(exe, []byte("build 2"), 0o644)
	second := &t7Copy{Copy: first.Copy, exe: exe}
	if got := second.fetched(); len(got) != 0 || second.lpc() == first.lpc() {
		t.Fatalf("an updated server has the other build's fastfile: %v (%s)", got, second.lpc())
	}
	if err := (&t7Game{copy: second}).keep(home); err != nil || len(second.fetched()) != 1 {
		t.Fatalf("the second build's fastfile: %v, %v", second.fetched(), err)
	}
	if left, _ := os.ReadDir(filepath.Join(first.Dir, "lpc")); len(left) != 1 {
		t.Errorf("the copy keeps %d builds' fastfiles, want the one of the server it has", len(left))
	}
}

// The game files may have what Steam's server fetches, where that server leaves it. The games then load the game
// files' own, whatever the server kept.
func TestT7OwnFetchedFiles(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "t7-server", "UnrankedServer", "server.exe")
	writeTestFile(t, exe)
	c := &t7Copy{Copy: &boiii.Copy{Dir: filepath.Join(dir, "boiii")}, exe: exe}
	if len(c.own()) != 0 || len(c.fetched()) != 0 {
		t.Fatalf("a server with nothing fetched has %v, %v", c.own(), c.fetched())
	}
	kept := filepath.Join(c.lpc(), "core_sv_ffotd_tu32_593.ff")
	writeTestFile(t, kept)
	if got := c.fetched(); len(got) != 1 || got[0] != kept {
		t.Errorf("what the server kept: %v, want %s", got, kept)
	}
	own := filepath.Join(filepath.Dir(exe), "identities", "dedicatedpc", "LPC", "core_sv_ffotd_tu32_593.ff")
	writeTestFile(t, own)
	if got := c.fetched(); len(got) != 1 || got[0] != own {
		t.Errorf("with the game files' own: %v, want %s", got, own)
	}
}
