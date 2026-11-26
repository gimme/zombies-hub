package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	for name, want := range map[string]string{"lobby.gsc": t7Script, "lobby_names.gsc": t7NameScript} {
		if script, _ := os.ReadFile(filepath.Join(g.home, "boiii", "custom_scripts", "zm", name)); string(script) != want {
			t.Errorf("the home's script %s is not the server's: %d bytes", name, len(script))
		}
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

// A T7 game shows a client under the name that the server tells its script, by the client's slot. The server tells
// it the name of a client that is in the game under another name than its room knows its player by, once, and takes
// the name back when the slot has no such client. It names no client while a game from the client's address is yet
// to come: the names tell those games apart.
func TestT7Names(t *testing.T) {
	eng := newT7Game(nil, "", "zm_factory", 4, "pw")
	g := &game{engine: eng, stdin: os.Stdin, output: io.Discard}
	l, r := &lobby{}, &room{code: "ROOM"}
	home, away := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")
	seatOf := func(name string, addr netip.Addr, n int) *seat {
		return &seat{player: &player{name: name, addr: addr}, local: &net.UDPAddr{IP: net.IPv4(127, 1, 0, byte(n)), Port: 28960},
			in: true, slot: n}
	}
	ann, bob, cy := seatOf("Ann", home, 1), seatOf("Bob of the long name", home, 2), seatOf("Cy", away, 3)
	rg := &roomGame{g: g, seats: []*seat{ann, bob, cy}}
	r.game = rg
	client := func(s *seat, name string) pcClient { return pcClient{s.slot, name, s.local.String(), true} }
	told := func(clients ...pcClient) []string {
		t.Helper()
		l.t7Names(r, rg, clients)
		var lines []string
		for {
			select {
			case line := <-eng.send:
				lines = append(lines, line)
			default:
				return lines
			}
		}
	}
	// each game plays under its player's name, of which a game has 16 characters
	if lines := told(client(ann, "Ann"), client(bob, "Bob of the long"), client(cy, "Cy")); len(lines) != 0 {
		t.Fatalf("the game was told %q, with each client under its player's name", lines)
	}
	// Cy renamed themselves since their game started, and Ann's game is one that she started by hand. The game's
	// status lists both by their own names still, once the game shows them by their players'.
	own := []pcClient{client(ann, "Gimmeh"), client(bob, "Bob of the long"), client(cy, "Tester")}
	if lines := told(own...); !slices.Equal(lines, []string{`set zombies_name_1 "Ann"`, `set zombies_name_3 "Cy"`}) {
		t.Fatalf("the game was told %q", lines)
	}
	if lines := told(own...); len(lines) != 0 {
		t.Fatalf("the game was told %q again", lines)
	}
	// Ann renames herself, and Cy's game leaves: the slot is the next client's, who keeps its name
	ann.player.name, cy.in = "Annie", false
	if lines := told(own[0], own[1]); !slices.Equal(lines, []string{`set zombies_name_1 "Annie"`, `set zombies_name_3 ""`}) {
		t.Fatalf("the game was told %q", lines)
	}
	// Bob's game is on its way again, from the address that Ann's comes from: the names tell the two apart, so the
	// server has the game show no client from that address under another name until Bob's is in
	cy.left, bob.in, ann.player.name = true, false, "Ann"
	if lines := told(own[0]); !slices.Equal(lines, []string{`set zombies_name_1 ""`}) {
		t.Fatalf("the game was told %q, with another game from the client's address yet to come", lines)
	}
	bob.in = true
	if lines := told(own[0], own[1]); !slices.Equal(lines, []string{`set zombies_name_1 "Ann"`}) {
		t.Fatalf("the game was told %q, with each game from the client's address in", lines)
	}
	// no game that ends is told a name
	ann.player.name, rg.ending = "Annie", "quit"
	if lines := told(own[0], own[1]); len(lines) != 0 {
		t.Fatalf("a game that ends was told %q", lines)
	}
}

// The server's script in a T7 game reports each player's GobbleGums: the gums of their pack, and each gum that they
// chew. The server keeps each as an event of the game, whose kind says whether the gum is a Mega one, once for a
// player and gum. It makes nothing of a line before round 1, nor of one that the script would not print.
func TestT7Gums(t *testing.T) {
	l := newLobby(context.Background())
	ann, bob := &player{id: "aaaa0001", name: "Ann"}, &player{id: "bbbb0002", name: "Bob"}
	seatOf := func(p *player, slot int) *seat {
		return &seat{player: p, local: &net.UDPAddr{IP: net.IPv4(127, 1, 0, byte(slot+1)), Port: 28960}, in: true, slot: slot}
	}
	r := &room{code: "ROOM", mapName: "zm_factory"}
	rg := &roomGame{seats: []*seat{seatOf(ann, 0), seatOf(bob, 1)}}
	r.game = rg
	said := func(lines ...string) {
		for _, line := range lines {
			l.pcSaid(r, rg, "zombies_lobby: "+line)
		}
	}
	said("gum pack zm_bgb_stock_option 0 0")
	if len(rg.events) != 0 {
		t.Fatalf("events before round 1: %+v", rg.events)
	}
	rg.started, rg.round1, rg.report = true, time.Now(), &gameResult{ID: "ROOM-1", Round: 1}
	// Ann's pack has a classic gum, a whimsical one and a Mega one, and a place without a gum, which the script leaves
	// out. Bob chews a gum of each rarity that is a Mega's, and Ann a classic one twice.
	said("gum pack zm_bgb_stock_option 0 0", "gum pack zm_bgb_eye_candy 4 0", "gum pack zm_bgb_perkaholic 3 0",
		"gum chew zm_bgb_stock_option 0 0", "gum chew zm_bgb_reign_drops 1 1")
	rg.report = &gameResult{ID: "ROOM-1", Round: 2}
	said("gum chew zm_bgb_shopping_free 2 1", "gum chew zm_bgb_perkaholic 3 1", "gum chew zm_bgb_stock_option 0 0")
	// what the script would not print: no such report, no such rarity, no name of a gum, no client in the slot
	said("gum had zm_bgb_stock_option 0 1", "gum chew zm_bgb_stock_option 5 1", "gum chew zm_bgb_stock_option x 1",
		"gum chew Stock! 0 1", "gum chew zm_bgb_stock_option 0 2", "gum chew zm_bgb_stock_option 0", "gum")
	want := []gameEvent{{round: 1, kind: eventPack, player: ann.id, what: "zm_bgb_stock_option"},
		{round: 1, kind: eventPack, player: ann.id, what: "zm_bgb_eye_candy"},
		{round: 1, kind: eventPackMega, player: ann.id, what: "zm_bgb_perkaholic"},
		{round: 1, kind: eventGum, player: ann.id, what: "zm_bgb_stock_option"},
		{round: 1, kind: eventGumMega, player: bob.id, what: "zm_bgb_reign_drops"},
		{round: 2, kind: eventGumMega, player: bob.id, what: "zm_bgb_shopping_free"},
		{round: 2, kind: eventGumMega, player: bob.id, what: "zm_bgb_perkaholic"}}
	if !slices.Equal(rg.events, want) {
		t.Errorf("the game's events:\n%+v\nwant\n%+v", rg.events, want)
	}
	if kept := l.unsavedGames["ROOM-1"]; kept == nil || !slices.Equal(kept.events, want) {
		t.Errorf("the report that the lobby keeps: %+v", kept)
	}
	// a game that ends takes no more
	rg.ending = "quit"
	said("gum chew zm_bgb_alchemical_antithesis 0 0")
	if len(rg.events) != len(want) {
		t.Errorf("a game that ends took an event: %+v", rg.events[len(want):])
	}
}

// The server's script in a T7 game reports that the map's main quest is done, with the players in the game then, as
// a T6 game's does. Each of them has the map's easter egg, at the one time from round 1, and the game has an event of
// each, with no side. Ten of T7's maps have a main quest, and a game on another has no easter egg.
func TestT7Egg(t *testing.T) {
	none := []string{"zm_prototype", "zm_asylum", "zm_sumpf", "zm_theater"}
	for _, m := range t7MapList {
		if hasEgg(m.name) == slices.Contains(none, m.name) {
			t.Errorf("%s has an easter egg: %v", m.title, hasEgg(m.name))
		}
	}
	for _, mapName := range []string{"zm_castle", "zm_theater"} {
		l := newLobby(context.Background())
		ann, bob := &player{id: "aaaa0001", name: "Ann"}, &player{id: "bbbb0002", name: "Bob"}
		seatOf := func(p *player, slot int) *seat {
			return &seat{player: p, local: &net.UDPAddr{IP: net.IPv4(127, 1, 0, byte(slot+1)), Port: 28960}, in: true, slot: slot}
		}
		r := &room{code: "ROOM", mapName: mapName}
		rg := &roomGame{seats: []*seat{seatOf(ann, 0), seatOf(bob, 1)}, started: true,
			round1: time.Now().Add(-90 * time.Second), report: &gameResult{ID: "ROOM-1", Round: 9}}
		r.game = rg
		// the players in slots 0 and 1, and a client in a slot that is no player's seat
		l.pcSaid(r, rg, "zombies_lobby: egg 0 0 1 3")
		if mapName == "zm_theater" {
			if len(rg.events) != 0 || len(ann.eggs) != 0 {
				t.Errorf("%s, which has no main quest: events %+v, Ann's easter eggs %+v", mapName, rg.events, ann.eggs)
			}
			continue
		}
		seconds := rg.eggSeconds
		if seconds < 90 || seconds > 100 {
			t.Errorf("%s: the easter egg was done %d s from round 1, of a game 90 s in", mapName, seconds)
		}
		want := []gameEvent{{seconds: seconds, round: 9, kind: eventEgg, player: ann.id},
			{seconds: seconds, round: 9, kind: eventEgg, player: bob.id}}
		if !slices.Equal(rg.events, want) {
			t.Errorf("%s: the game's events:\n%+v\nwant\n%+v", mapName, rg.events, want)
		}
		if kept := l.unsavedGames["ROOM-1"]; kept == nil || !slices.Equal(kept.events, want) {
			t.Errorf("%s: the report that the lobby keeps: %+v", mapName, kept)
		}
		for _, p := range []*player{ann, bob} {
			if len(p.eggs) != 1 || p.eggs[0].Map != mapName || p.eggs[0].Party != 2 || p.eggs[0].Seconds != seconds {
				t.Errorf("%s: %s's easter eggs: %+v", mapName, p.name, p.eggs)
			}
		}
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
