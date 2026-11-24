package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
	"zombies/internal/plutonium/plutoniumtest"
)

// fakeT6 is a stand-in for Plutonium's T6 server, which fakeGame is when its command line starts T6's Zombies. It
// runs in its home, as the server does: it reads its remote console's password from its config there
// (storage/t6/lobby.cfg), prints as Plutonium does, in colour after its prompt, and answers on its port. Its remote
// console, to the password: status, clientkick <slot> and quit; it prints any other line it gets. Its clients, which
// are in from their "connectReq", answered with the address it sees them at, until their "leave"; "connectReq Ann" is
// one that plays under that name, where the others are Player and their slot. A client is also in from its connect,
// as a real one is (connect "\name\Ann": the name it plays under), answered with the slot it has: the first free one.
// A client's "round 3"
// is a round beginning, as the server's script in the game says it (t6Script), after each client's line of the
// scoreboard (stats, with a team: 1 in the even slots and 2 in the odd), its "egg" the map's main quest done, with
// every client in the game, and its "over" game over, on round 7, with its end played out a moment later ("over 4 2"
// is game over on round 4, and team 2 won); its "rotate" is the server rotating its map with no word of the script's.
// Any other packet comes back as it is.
func fakeT6(port string) {
	say := func(text string) {
		fmt.Printf("\x1b[38;2;255;255;255m\x1b[2K\x1b[38;2;243;111;26mPlutonium r1 > \x1b[38;2;255;255;255m%s\n", text)
	}
	cfg, err := os.ReadFile(filepath.Join("storage", "t6", "lobby.cfg"))
	found := regexp.MustCompile(`rcon_password "(\w+)"`).FindSubmatch(cfg)
	if err != nil || found == nil {
		fmt.Println("no remote console password in storage/t6/lobby.cfg:", err)
		os.Exit(1)
	}
	password := string(found[1])
	conn, err := net.ListenPacket("udp4", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	say("args: " + strings.Join(os.Args[1:], " "))
	say("map_rotate...")
	fmt.Println("\x1b[38;2;255;255;255m\x1b[2K\x1b[38;2;243;111;26mPlutonium r1 > ") // its prompt alone
	var clients []string                                                             // each slot's address, "" for a free one
	names := map[string]string{}
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			os.Exit(1)
		}
		text, oob := strings.CutPrefix(string(buf[:n]), "\xff\xff\xff\xff")
		reply := func(text string) { conn.WriteTo([]byte("\xff\xff\xff\xff"+text), from) }
		if rest, ok := strings.CutPrefix(text, "rcon "); oob && ok {
			word, line, _ := strings.Cut(rest, " ")
			if word != password {
				say("Bad rcon from " + from.String())
				continue
			}
			switch slot, kick := strings.CutPrefix(line, "clientkick "); {
			case line == "status":
				status := "print\nmap: zm_transit\n" +
					"num score bot ping guid                             name             lastmsg address               qport  rate\n" +
					"--- ----- --- ---- -------------------------------- ---------------- ------- --------------------- ------ -----\n"
				for i, address := range clients {
					if address != "" {
						name := names[address]
						if name == "" {
							name = "Player " + strconv.Itoa(i)
						}
						status += fmt.Sprintf("%3d %5d %3d %4d %-32s %-16s %7d %-21s %6d %5d\n", i, 500, 0, 30, "9240",
							name, 0, address, -1234, 25000)
					}
				}
				reply(status)
			case line == "quit":
				say("quitting")
				os.Exit(0)
			case kick:
				if i, err := strconv.Atoi(slot); err == nil && i < len(clients) {
					clients[i] = ""
				}
			default:
				say("got: " + line)
				reply("print\nUnknown command \"" + line + "\"\n")
			}
			continue
		}
		// each client's line of the scoreboard, by its slot: a thousand points a slot and a hundred a round, and so on
		stats := func(round int) {
			for slot, address := range clients {
				if address != "" {
					say(fmt.Sprintf("zombies_lobby: stats %d %d %d %d %d %d %d %d %d", slot, 1000*(slot+1)+100*round,
						500*(slot+1), 10*round+slot, round, slot, 1-slot, 2, slot%2+1))
				}
			}
		}
		round, isRound := strings.CutPrefix(text, "round ")
		over, isOver := strings.CutPrefix(text, "over")
		if name, named := strings.CutPrefix(text, "connectReq "); oob && named {
			names[from.String()], text = name, "connectReq"
		}
		_, connects := strings.CutPrefix(text, `connect "`)
		if name := regexp.MustCompile(`\\name\\([^\\"]*)`).FindStringSubmatch(text); oob && connects && name != nil {
			names[from.String()] = name[1]
		}
		switch {
		case oob && isRound:
			n, _ := strconv.Atoi(round)
			stats(n)
			say("zombies_lobby: round " + round)
		case oob && (text == "connectReq" || connects):
			if !slices.Contains(clients, from.String()) {
				if i := slices.Index(clients, ""); i >= 0 {
					clients[i] = from.String()
				} else {
					clients = append(clients, from.String())
				}
			}
			if connects {
				reply("connectResponse " + strconv.Itoa(slices.Index(clients, from.String())))
			} else {
				reply("connectRes " + from.String())
			}
		case oob && text == "leave":
			if i := slices.Index(clients, from.String()); i >= 0 {
				clients[i] = ""
			}
		case oob && text == "egg":
			line := "zombies_lobby: egg"
			for slot, address := range clients {
				if address != "" {
					line += " " + strconv.Itoa(slot)
				}
			}
			say(line)
		case oob && isOver:
			stats(7)
			say("zombies_lobby: over " + cmp.Or(strings.TrimSpace(over), "7"))
			go func() {
				time.Sleep(300 * time.Millisecond)
				say("zombies_lobby: exit")
			}()
		case oob && text == "rotate":
			say("map_rotate...")
		default:
			conn.WriteTo(buf[:n], from)
		}
	}
}

// what Plutonium's updater serves, in these tests (plutoniumtest)
var plutoniumFiles = map[string]string{
	bootstrapper:                       "the bootstrapper",
	"bin/steam_api64.dll":              "a dll",
	"games/t6zm.exe":                   "T6's Zombies",
	"games/iw5mp.exe":                  "another game",
	"storage/t6/zone/zm_patch.ff":      "a zone",
	"storage/t6/raw/scripts/zm/a.gsc":  "a script",
	"storage/demonware/zm/playlists":   "playlists",
	"storage/iw5/zone/english/huge.ff": "another game's",
}

func TestT6StatusLine(t *testing.T) {
	const row = "%3d %5d %3d %4s %-32s %-16s %7d %-21s %6d %5d"
	for _, tc := range []struct {
		line           string
		c              t6Client
		client, status bool
	}{
		{line: "map: zm_transit", status: true},
		{line: "num score bot ping guid                             name             lastmsg address", status: true},
		{line: "--- ----- --- ---- -------------------------------- ---------------- ------- -------", status: true},
		{fmt.Sprintf(row, 0, 1500, 0, "32", "9240", "Ann", 0, "127.1.0.1:28960", -1234, 25000),
			t6Client{0, "Ann", "127.1.0.1:28960", true}, true, true},
		{fmt.Sprintf(row, 3, 0, 0, "CNCT", "59567", "Ann B", 50, "127.1.0.7:28960", 4321, 25000),
			t6Client{3, "Ann B", "127.1.0.7:28960", false}, true, true},
		{fmt.Sprintf(row, 1, 0, 0, "999", "59567", "Cy", 50, "127.1.0.8:28960", 4321, 25000),
			t6Client{1, "Cy", "127.1.0.8:28960", false}, true, true},
		{line: fmt.Sprintf(row, 2, 0, 1, "0", "0", "bot2", 0, "bot", 0, 0), status: true},
		{line: `"sv_hostname" is: "Room ABCD^7" default: "BlackOpsPublic^7"`},
		{line: `Unknown command "clientkick"`},
		{line: ""},
	} {
		if c, client, status := t6StatusLine(tc.line); c != tc.c || client != tc.client || status != tc.status {
			t.Errorf("%q: %+v, %v, %v", tc.line, c, client, status)
		}
	}
}

func TestT6Line(t *testing.T) {
	white, orange, clear := "\x1b[38;2;255;255;255m", "\x1b[38;2;243;111;26m", "\x1b[2K"
	prompt := white + clear + white + orange + "Plutonium r5369 > " + white + white + clear + white
	for line, want := range map[string]string{
		prompt + "map_rotate...":                 "map_rotate...",
		prompt:                                   "",
		"client 'Ann' connected":                 "client 'Ann' connected",
		"Plutonium r5369 > Plutonium r5369 > hi": "hi",
	} {
		if got := t6Line(line); got != want {
			t.Errorf("%q: %q", line, got)
		}
	}
}

// What a T6 client's route takes: all but the server's remote console, however the packet spells it.
func TestT6ClientSends(t *testing.T) {
	for packet, want := range map[string]bool{
		"\x00\x01\x02\x03 a netchan packet":                 true,
		"\xff\xff\xff\xffconnectReq":                        true,
		"\xff\xff\xff\xffgetchallenge 0 0 0":                true,
		"\xff\xff\xff\xffrconnect":                          true,
		"\xff\xff\xff\xffrcon secret status":                false,
		"\xff\xff\xff\xffRCON secret status":                false,
		"\xff\xff\xff\xff  rcon secret status":              false,
		"\xff\xff\xff\xff\x01\trcon\tsecret status":         false,
		"\xff\xff\xff\xff\x00rcon secret status":            false,
		"\xff\xff\xff\xff\nrcon secret status":              false,
		"\xff\xff\xff\xffrcon":                              false,
		"\xff\xff\xff\xff":                                  false,
		"\xff\xff\xff\xff" + strings.Repeat("x", maxFromT6): false,
	} {
		if got := t6ClientSends([]byte(packet)); got != want {
			t.Errorf("%q: %v", packet, got)
		}
	}
}

// A T6 client's connect, which waits for its turn, and the name it plays under.
func TestT6Connect(t *testing.T) {
	type connect struct {
		name string
		ok   bool
	}
	for packet, want := range map[string]connect{
		"\xff\xff\xff\xff" + `connect "\protocol\2089\name\Ann B\qport\123"`: {"Ann B", true},
		"\xff\xff\xff\xff" + `CONNECT "\NAME\Ann"`:                           {"Ann", true},
		"\xff\xff\xff\xff\n connect\t" + `"\name\Ann"`:                       {"Ann", true},
		"\xff\xff\xff\xff" + `connect \name\Ann`:                             {"Ann", true},
		"\xff\xff\xff\xff" + `connect 1 "\name\Ann"`:                         {"Ann", true},
		"\xff\xff\xff\xff" + `connect "\protocol\2089" \name\Ann`:            {"", true},
		"\xff\xff\xff\xffconnect":                                            {"", true},
		"\xff\xff\xff\xffconnectReq":                                         {"", false},
		"\xff\xff\xff\xff" + `getchallenge 0 "offline" 5369`:                 {"", false},
		"\x05\x00\x00\x00" + `connect "\name\Ann"`:                           {"", false},
	} {
		if name, ok := t6Connect([]byte(packet)); name != want.name || ok != want.ok {
			t.Errorf("%q: %q, %v", packet, name, ok)
		}
	}
}

func TestPageAddr(t *testing.T) {
	for _, tc := range []struct{ remote, forwarded, want string }{
		{"203.0.113.9:4000", "", "203.0.113.9"},
		{"203.0.113.9:4000", "198.51.100.1", "203.0.113.9"}, // no proxy of the server's: what it says counts for nothing
		{"172.17.0.1:4000", "198.51.100.1", "198.51.100.1"},
		{"127.0.0.1:4000", "10.9.9.9, 198.51.100.7", "198.51.100.7"},
		{"192.168.1.20:4000", "", "192.168.1.20"},
		{"[::ffff:10.0.0.2]:4000", "::ffff:198.51.100.3", "198.51.100.3"},
		{"10.0.0.2:4000", "not an address", "10.0.0.2"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/lobby/events", nil)
		req.RemoteAddr = tc.remote
		if tc.forwarded != "" {
			req.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got := pageAddr(req); got != netip.MustParseAddr(tc.want) {
			t.Errorf("%s, forwarded for %q: %s", tc.remote, tc.forwarded, got)
		}
	}
}

// t6Lobby is a server whose game files have a T6 map, with its relay on a UDP port (its address), and fakeT6 as
// Plutonium's server, from a copy the server downloads from a stand-in for its updater.
func t6Lobby(t *testing.T) (*lobby, string) {
	t.Helper()
	l, target, _ := t6LobbyAndUpdater(t)
	return l, target
}

// t6LobbyAndUpdater is t6Lobby, and the stand-in for Plutonium's updater, which says what the server asked of it.
func t6LobbyAndUpdater(t *testing.T) (*lobby, string, *plutoniumtest.Updater) {
	t.Helper()
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_theater", "zm_standard_town", "zm_classic_tomb"} }
	l.gamePorts = append(l.gamePorts, freePort(t))
	revision := 1
	updater := plutoniumtest.Serve(t, plutoniumFiles, &revision)
	pluto := &t6Copy{Copy: &plutonium.Copy{Dir: filepath.Join(t.TempDir(), "plutonium"), URL: updater.URL,
		Needs: plutonium.T6, Client: updater.Client, State: "zombies-server-files.json"}}
	root := t.TempDir()
	l.t6().pluto = pluto
	l.t6().newGame = func(code, mapName string, port, players int, password string) *game {
		return &game{name: code, wine: os.Args[0], dir: root, home: filepath.Join(root, "home", strconv.Itoa(port)),
			logDir: filepath.Join(root, "logs"), ip: "127.0.0.1", port: port, output: io.Discard,
			engine:       newT6Game(pluto, mapName, players, l.sizeFor(mapName), l.loadTimeout, password),
			startTimeout: time.Minute, quietTimeout: time.Minute}
	}
	certs := &certificates{}
	if _, _, err := certs.current(); err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	l.relay = newRelay(l, certs)
	go l.relay.serve(udp)
	t.Cleanup(func() { l.relay.close() })
	return l, udp.LocalAddr().String(), updater
}

// pagesAt has the players' pages call from an address, as their last call to the server did.
func pagesAt(l *lobby, address string, tokens ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, token := range tokens {
		l.playerOf(token).addr = netip.MustParseAddr(address)
	}
}

// t6Home is the home of the T6 game of the player's room.
func t6Home(l *lobby, token string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.playerOf(token).room.game.g.home
}

// udpSilent is whether a packet from a player's game gets no answer.
func udpSilent(conn *net.UDPConn, packet []byte) bool {
	conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
	conn.Write(packet)
	_, err := conn.Read(make([]byte, 2048))
	return err != nil
}

// A room on a T6 map: its game is Plutonium's server, which its players' own games join through the relay, each at a
// seat's address, once their pages call from the address their games come from; the room shows who is in it; no one
// reaches the game's remote console through the relay; each round the server's script in the game says is its
// players' record, on T6's own boards; a player who leaves the room is kicked; and at game over the room has the
// game's report, and the server stops the game once the game's end has played out.
func TestT6Room(t *testing.T) {
	l, target := t6Lobby(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	if err := do(l, a, "create", "map", "zm_standard_town"); err != "" {
		t.Fatal(err)
	}
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	if v := l.viewOf(a); v.Room.Of != t6 || v.Room.Members[0].Missing || v.Room.T6 != nil {
		t.Fatalf("the room before its game: %+v", v.Room)
	}
	if err := setMods(l, a, "grief"); err != "Black Ops II games have no mods." {
		t.Errorf("a T6 room took a mod: %q", err)
	}
	// ready with no game files in the page: a T6 game is no page's to play
	pagesOpen(l, a, b)
	for _, token := range []string{a, b} {
		if err := do(l, token, "ready", "ready", "true"); err != "" {
			t.Fatal(err)
		}
	}
	v := waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	if v.Room.State != "loading" || len(v.Room.T6.Password) != 8 || v.Room.T6.Name != "Ann" || v.Room.Join {
		t.Fatalf("the room as its game waits for its players: %+v, %+v", v.Room, v.Room.T6)
	}
	// its home is a Plutonium folder: links to the server's copy, and the game's own config, with the password
	home := t6Home(l, a)
	dest, err := os.Readlink(filepath.Join(home, "bin"))
	if err != nil || !isFile(filepath.Join(dest, "plutonium-bootstrapper-win32.exe")) {
		t.Errorf("the home's bin: %q, %v", dest, err)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "lobby.cfg"))
	gts, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "gamesettings", "lobby_gts.cfg"))
	// (it waits for its players as long as the server does, five minutes, where Plutonium's script would wait 90 s)
	for _, line := range []string{`g_password "` + v.Room.T6.Password + `"`, "zombies_minplayers 2", "sv_connectTimeout 300",
		`sv_maprotation "execgts lobby_gts.cfg map zm_transit"`} {
		if !strings.Contains(string(cfg), line+"\n") {
			t.Errorf("the game's config lacks %s:\n%s", line, cfg)
		}
	}
	for _, line := range []string{`ui_zm_mapstartlocation "town"`, "execgts gamesettings_zstandard.cfg"} {
		if !strings.Contains(string(gts), line+"\n") {
			t.Errorf("the game's settings lack %s:\n%s", line, gts)
		}
	}
	rcon := string(regexp.MustCompile(`rcon_password "(\w+)"`).FindSubmatch(cfg)[1])

	// a game from an address no page of the room's calls from is no one's
	annsGame, bobsGame := windowsPlayer(t, target), windowsPlayer(t, target)
	if !udpSilent(annsGame, []byte("\xff\xff\xff\xffconnectReq")) {
		t.Fatal("the server answered a game from an address no player's page is at")
	}
	pagesAt(l, "127.0.0.1", a, b)
	// each game at a seat's address of its own, though both come from one address, as two players of one household
	conn := func(game *net.UDPConn) string {
		game.SetDeadline(time.Now().Add(5 * time.Second))
		game.Write([]byte("\xff\xff\xff\xffconnectReq"))
		buf := make([]byte, 2048)
		n, err := game.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(string(buf[:n]), "\xff\xff\xff\xffconnectRes ")
	}
	annAt, bobAt := conn(annsGame), conn(bobsGame)
	seats := []string{l.seatAddr(a), l.seatAddr(b)}
	if annAt == bobAt || !slices.Contains(seats, annAt) || !slices.Contains(seats, bobAt) {
		t.Fatalf("the server saw the games at %s and %s, the seats being %v", annAt, bobAt, seats)
	}
	v = waitView(t, l, a, "both in the game", func(v view) bool { return v.Room.State == "playing" })
	for _, m := range v.Room.Members {
		if !m.InGame {
			t.Errorf("%s isn't in the game: %+v", m.Name, m)
		}
	}
	// the connection's packets go both ways as they are
	if packet := []byte("\x05\x00\x00\x00 a netchan packet"); udpSilent(annsGame, packet) {
		t.Error("the game didn't answer a netchan packet")
	}
	// the game's remote console is the server's: not a player's, even with its password
	for _, word := range []string{"rcon", "RCON", "\x01rcon", "  rcon"} {
		packet := word + " " + rcon + " quit"
		if !udpSilent(annsGame, []byte("\xff\xff\xff\xff"+packet)) {
			t.Errorf("the relay passed %q", packet)
		}
	}
	// a console line of the server's goes to it
	l.command(v.Room.Code + " say hello")
	waitFor(t, "the console line at the server", func() bool {
		logs, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(home)), "logs", "game-*.log"))
		text, _ := os.ReadFile(logs[0])
		return bytes.Contains(text, []byte("got: say hello")) && !bytes.Contains(text, []byte("\x1b"))
	})
	// a round begins: both players' record on the map, as a duo, on T6's boards, and none of T5's
	annsGame.SetDeadline(time.Time{})
	annsGame.Write([]byte("\xff\xff\xff\xffround 3"))
	boards := func() leaderboards {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.leaderboards(l.playerOf(a))
	}
	duo := func() []boardRow { return boards().of(t6).Rounds.Maps[0].Duo }
	waitFor(t, "both players on the duo board", func() bool { return len(duo()) == 2 })
	b3 := boards()
	if len(b3.Titles) != 2 || b3.Titles[0].ID != t5 || b3.Titles[1].ID != t6 || b3.of(t6).Name != "Black Ops II" {
		t.Fatalf("the boards' titles: %+v", b3.Titles)
	}
	// T6's easter eggs: Origins', and none of Town, which has no main quest
	if eggs := b3.of(t6).Eggs; eggs == nil || len(eggs.Maps) != 1 || eggs.Maps[0].Map != "zm_classic_tomb" {
		t.Errorf("T6's boards of easter eggs: %+v", eggs)
	}
	t5Boards, t6Boards := b3.of(t5).Rounds, b3.of(t6).Rounds
	if len(t5Boards.Maps) != 1 || len(t6Boards.Maps) != 2 || t6Boards.Maps[0].Map != "zm_standard_town" {
		t.Fatalf("the boards' maps: %+v, T6's %+v", t5Boards.Maps, t6Boards.Maps)
	}
	if rows := t6Boards.Maps[0].Duo; rows[0].Round != 3 || rows[1].Round != 3 || rows[0].Party != 2 {
		t.Errorf("the duo board after round 3: %+v", rows)
	}
	if all := t6Boards.All; len(all.Rows) != 2 || all.Mine.Round != 3 || b3.Games[all.Mine.Bests[0].Game] == nil {
		t.Errorf("T6's board of all its maps after round 3: %+v", all)
	}
	if n := len(t5Boards.All.Rows); n != 0 {
		t.Errorf("T5's board of all its maps has %d players of a T6 game", n)
	}
	// Bob leaves the room: the server kicks his game, and the relay lets it in no more
	do(l, b, "leave")
	if !udpSilent(bobsGame, []byte("\xff\xff\xff\xffconnectReq")) {
		t.Error("the game of a player who left the room was let in again")
	}
	pagesAt(l, "127.0.0.1", a)
	// the server's script is in the game's home, where Plutonium's server loads scripts from
	script, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "scripts", "zm", "lobby.gsc"))
	if string(script) != t6Script {
		t.Errorf("the server's script in the game's home: %q", script)
	}
	// game over: the room says so, and the round, while the game's end plays, and is open again once it has
	annsGame.SetDeadline(time.Time{})
	annsGame.Write([]byte("\xff\xff\xff\xffover"))
	// game over: the room has the game's report, of who was in it then, while the game's end plays, and after
	wantAnn := playerStats{Name: "Ann", Score: 1700, Cash: 500, Kills: 70, Headshots: 7, Downs: 0, Revives: 1}
	for _, state := range []string{"ending", "open"} {
		v = waitView(t, l, a, "the room "+state, func(v view) bool { return v.Room.State == state })
		res := v.Room.Result
		if res == nil || res.Round != 7 || res.Map != "zm_standard_town" || len(res.Players) != 1 || v.Room.T6 != nil {
			t.Fatalf("the room's report, %s: %+v, %+v", state, res, v.Room.T6)
		}
		got := res.Players[0]
		if got.Doors == nil || *got.Doors != 2 {
			t.Errorf("Ann's doors: %v", got.Doors)
		}
		wantAnn.ID, got.Doors, got.Avatar = got.ID, nil, ""
		if got != wantAnn || v.Room.Note != "" {
			t.Errorf("Ann's line of the report, %s: %+v, and the room's note %q", state, got, v.Room.Note)
		}
	}
	// and its round is Ann's record, with the party that started the game: Bob's is the one he saw begin
	if rows := duo(); len(rows) != 2 || rows[0].Name != "Ann" || rows[0].Round != 7 || rows[1].Round != 3 {
		t.Errorf("the duo board after game over: %+v", rows)
	}
	if runs := l.t6().pluto.runs.Load(); runs != 0 {
		t.Errorf("the server still counts %d games that run from its copy of Plutonium", runs)
	}
}

// t6RoomUp is a room of one player on a T6 map, with its game up. The player's page calls from the address.
func t6RoomUp(t *testing.T, l *lobby, name, mapName, address string) (token string) {
	t.Helper()
	token = l.hello("", name).Token
	if err := do(l, token, "create", "map", mapName); err != "" {
		t.Fatal(err)
	}
	pagesOpen(l, token)
	if err := do(l, token, "ready", "ready", "true"); err != "" {
		t.Fatal(err)
	}
	waitView(t, l, token, name+"'s server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, address, token)
	return token
}

// t6GameAt is a player's game at an address, which has yet to send anything.
func t6GameAt(t *testing.T, address, target string) *net.UDPConn {
	t.Helper()
	to, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		t.Fatal(err)
	}
	game, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(address)}, to)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { game.Close() })
	return game
}

// t6SeenAt is the address at which a T6 room's game sees a player's game that asks to join: a seat's.
func t6SeenAt(t *testing.T, game *net.UDPConn) string {
	t.Helper()
	game.SetDeadline(time.Now().Add(5 * time.Second))
	game.Write([]byte("\xff\xff\xff\xffconnectReq"))
	buf := make([]byte, 2048)
	n, err := game.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	game.SetDeadline(time.Time{})
	return strings.TrimPrefix(string(buf[:n]), "\xff\xff\xff\xffconnectRes ")
}

// Two rooms play T6 at once, each in a game of its own, which its own players join. Both games run from the server's
// one copy of Plutonium. The copy is brought up to date for the first, and is left as it is for the second, which
// starts while the first runs from it.
func TestT6RoomsPlayAtOnce(t *testing.T) {
	l, target, updater := t6LobbyAndUpdater(t)
	a := t6RoomUp(t, l, "Ann", "zm_standard_town", "127.0.0.2")
	if asked := updater.Asked(); !slices.Contains(asked, "/info.json") {
		t.Errorf("the server asked Plutonium's updater for %q before the first game", asked)
	}
	b := t6RoomUp(t, l, "Bob", "zm_classic_tomb", "127.0.0.3")
	if asked := updater.Asked(); len(asked) > 0 {
		t.Errorf("the server asked Plutonium's updater for %q, while a game ran from its copy", asked)
	}
	if runs := l.t6().pluto.runs.Load(); runs != 2 {
		t.Errorf("%d games run from the server's copy of Plutonium", runs)
	}
	if homeA, homeB := t6Home(l, a), t6Home(l, b); homeA == homeB {
		t.Errorf("both games have the home %s", homeA)
	}
	annsGame, bobsGame := t6GameAt(t, "127.0.0.2", target), t6GameAt(t, "127.0.0.3", target)
	if at, seat := t6SeenAt(t, bobsGame), l.seatAddr(b); at != seat {
		t.Errorf("Bob's game is at %s, his seat at %s", at, seat)
	}
	if at, seat := t6SeenAt(t, annsGame), l.seatAddr(a); at != seat {
		t.Errorf("Ann's game is at %s, her seat at %s", at, seat)
	}
	for _, token := range []string{a, b} {
		v := waitView(t, l, token, "the room playing", func(v view) bool { return v.Room.State == "playing" })
		if len(v.Room.Members) != 1 || !v.Room.Members[0].InGame {
			t.Errorf("the room's members: %+v", v.Room.Members)
		}
	}
	// one game ends, and the other plays on
	annsGame.Write([]byte("\xff\xff\xff\xffover"))
	waitView(t, l, a, "Ann's room open", func(v view) bool { return v.Room.State == "open" })
	if v := l.viewOf(b); v.Room.State != "playing" {
		t.Errorf("Bob's room, as Ann's game ended: %s (%q)", v.Room.State, v.Room.Note)
	}
	if runs := l.t6().pluto.runs.Load(); runs != 1 {
		t.Errorf("%d games run from the server's copy of Plutonium, one having ended", runs)
	}
	if udpSilent(bobsGame, []byte("\x05\x00\x00\x00 a netchan packet")) {
		t.Error("Bob's game reaches its room's game no more")
	}
}

// The games' ports: none is the server's own port, and none is a port that a Plutonium server opens beside its game's.
func TestGamePorts(t *testing.T) {
	for _, tc := range []struct {
		first, games, port int
		want               []int
	}{
		{3100, 4, 3075, []int{3100, 3101, 3102, 3103}},
		{3076, 4, 3075, []int{3079, 3080, 3081, 3082}},
		{3072, 3, 3073, []int{3072, 3078, 3079}},
		{65534, 4, 3075, []int{65534, 65535}},
	} {
		if got := gamePorts(tc.first, tc.games, tc.port); !slices.Equal(got, tc.want) {
			t.Errorf("%d games from %d, the server's port being %d: %v", tc.games, tc.first, tc.port, got)
		}
	}
}

// launcherAt is the answer to a launcher's call from an address, which says its PC has what the game needs.
func launcherAt(h http.Handler, key, address, seen string) launcherapi.Play {
	_, body := asLauncher(h, http.MethodGet, "/launcher/play?ready=1&seen="+seen, key, "", "from", address+":4000")
	var play launcherapi.Play
	json.Unmarshal([]byte(body), &play)
	return play
}

// Two players at one address, in two rooms that play T6 at once. Nothing in a game's packets says which room it is
// to join. So the launcher of the second is sent their room's game once the game of the first has its seat, and each
// game joins its own room's game.
func TestT6RoomsTakeThePlayersOfOneAddressInTurn(t *testing.T) {
	l, target := t6Lobby(t)
	h := &web{lobby: l, publicPort: 4000}
	a := t6RoomUp(t, l, "Ann", "zm_standard_town", "127.0.0.1")
	b := t6RoomUp(t, l, "Bob", "zm_classic_tomb", "127.0.0.1")
	keys := map[string]string{}
	l.mu.Lock()
	for _, token := range []string{a, b} {
		keys[token] = l.playerOf(token).key
	}
	l.mu.Unlock()
	// Bob's launcher asks first, and is sent his room's game: Ann's waits for his game to have its seat
	if play := launcherAt(h, keys[b], "127.0.0.1", ""); play.Game == nil {
		t.Fatalf("the first launcher at the address was sent no game: %+v", play)
	}
	waits := launcherAt(h, keys[a], "127.0.0.1", "")
	if waits.Game != nil || waits.Room == "" {
		t.Fatalf("the second launcher at the address, as the first one's game is on its way: %+v", waits)
	}
	// (the server holds its call, and answers it with the game when the wait is over)
	held := make(chan launcherapi.Play, 1)
	go func() { held <- launcherAt(h, keys[a], "127.0.0.1", waits.Version) }()
	select {
	case play := <-held:
		t.Fatalf("the second launcher's call was answered as the first one's game was on its way: %+v", play)
	case <-time.After(500 * time.Millisecond):
	}
	// the one game the address has on its way is Bob's
	first := t6GameAt(t, "127.0.0.1", target)
	if at, seat := t6SeenAt(t, first), l.seatAddr(b); at != seat {
		t.Fatalf("the first game from the address is at %s, where the seat of the player whose launcher has the game is at %s", at, seat)
	}
	select {
	case play := <-held:
		if play.Game == nil {
			t.Fatalf("the second launcher's call was answered with no game: %+v", play)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second launcher was sent no game once the first one's game had its seat")
	}
	second := t6GameAt(t, "127.0.0.1", target)
	if at, seat := t6SeenAt(t, second), l.seatAddr(a); at != seat {
		t.Errorf("the second game from the address is at %s, its player's seat at %s", at, seat)
	}
	for _, token := range []string{a, b} {
		waitView(t, l, token, "the room playing", func(v view) bool { return v.Room.State == "playing" })
	}
}

// A game that doesn't come is waited for only so long (t6Way) by the launcher of a player of another room at its
// address. That launcher is then sent its game, and the next game from the address is its player's.
func TestT6RoomsWaitOnlySoLongForAGameOfOneAddress(t *testing.T) {
	l, target := t6Lobby(t)
	l.t6Way = 500 * time.Millisecond
	h := &web{lobby: l, publicPort: 4000}
	a := t6RoomUp(t, l, "Ann", "zm_standard_town", "127.0.0.1")
	b := t6RoomUp(t, l, "Bob", "zm_classic_tomb", "127.0.0.1")
	l.mu.Lock()
	annsKey, bobsKey := l.playerOf(a).key, l.playerOf(b).key
	l.mu.Unlock()
	if play := launcherAt(h, annsKey, "127.0.0.1", ""); play.Game == nil {
		t.Fatalf("the first launcher at the address was sent no game: %+v", play)
	}
	if play := launcherAt(h, bobsKey, "127.0.0.1", ""); play.Game != nil {
		t.Fatalf("the second launcher at the address was sent its game at once: %+v", play)
	}
	waitFor(t, "the second launcher's game, the first one's game not having come", func() bool {
		return launcherAt(h, bobsKey, "127.0.0.1", "").Game != nil
	})
	if at, seat := t6SeenAt(t, t6GameAt(t, "127.0.0.1", target)), l.seatAddr(b); at != seat {
		t.Errorf("the game from the address is at %s, where the seat of the player whose launcher was sent the game last is at %s", at, seat)
	}
}

// A T6 game whose players are not all in loadTimeout after its server was up is called off; but not while the launcher
// of one still to come gets what their PC lacks.
func TestT6RoomCalledOffWhenLate(t *testing.T) {
	l, target := t6Lobby(t)
	l.loadTimeout = 2 * time.Second
	h := &web{lobby: l}
	bob := l.hello("", "Bob")
	a, b := l.hello("", "Ann").Token, bob.Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	pagesOpen(l, a, b)
	do(l, a, "ready", "ready", "true")
	do(l, b, "ready", "ready", "true")
	waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", a)
	annsGame := windowsPlayer(t, target)
	annsGame.Write([]byte("\xff\xff\xff\xffconnectReq"))
	// Bob's launcher runs, and downloads: the game waits past its time; then it says his PC is ready, and he doesn't come
	for began := time.Now(); time.Since(began) < l.loadTimeout+1500*time.Millisecond; time.Sleep(100 * time.Millisecond) {
		asLauncher(h, http.MethodGet, "/launcher/t6/files", bob.Key, "")
	}
	if v := l.viewOf(a); v.Room.State == "open" || !l.viewOf(b).You.Launcher || v.You.Launcher {
		t.Fatalf("a game whose late player's launcher gets their PC ready: %s (%q)", v.Room.State, v.Room.Note)
	}
	asLauncher(h, http.MethodGet, "/launcher/play?ready=1", bob.Key, "")
	// (his game has the whole wait from then, to start and load in)
	time.Sleep(l.loadTimeout / 2)
	if v := l.viewOf(a); v.Room.State == "open" {
		t.Fatalf("the game was called off as the late player's PC was ready, with no time for their game: %q", v.Room.Note)
	}
	v := waitView(t, l, a, "the game called off", func(v view) bool { return v.Room.State == "open" })
	if v.Room.Note != "Game called off: Bob didn't join in time." {
		t.Errorf("the room's note: %q", v.Room.Note)
	}
}

// oneAddress is a T6 room's game up, with two players whose pages call from one address, and their games: Bob's
// comes first, and so has Ann's seat, the host's, until the server lists it under his name; then Ann's.
func oneAddress(t *testing.T) (l *lobby, a, b string, annsGame, bobsGame *net.UDPConn) {
	t.Helper()
	l, target := t6Lobby(t)
	a, b = l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	pagesOpen(l, a, b)
	do(l, a, "ready", "ready", "true")
	do(l, b, "ready", "ready", "true")
	waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", a, b)
	annsGame, bobsGame = windowsPlayer(t, target), windowsPlayer(t, target)
	// (each reads the server's answer, so that the next thing its socket has is an answer to what it sends next)
	connect := func(game *net.UDPConn, name string) {
		game.SetDeadline(time.Now().Add(5 * time.Second))
		game.Write([]byte("\xff\xff\xff\xffconnectReq " + name))
		if _, err := game.Read(make([]byte, 2048)); err != nil {
			t.Fatalf("%s's game had no answer: %v", name, err)
		}
		game.SetDeadline(time.Time{})
	}
	connect(bobsGame, "Bob")
	members := waitView(t, l, a, "a player in the game", func(v view) bool {
		return v.Room.Members[0].InGame || v.Room.Members[1].InGame
	}).Room.Members
	if members[0].InGame || !members[1].InGame {
		t.Fatalf("Bob's game is in, and the room has it Ann's: %+v", members)
	}
	connect(annsGame, "Ann")
	waitView(t, l, a, "both in the game", func(v view) bool { return v.Room.State == "playing" })
	return l, a, b, annsGame, bobsGame
}

// Two players behind one address: a game takes the first of their seats that is free, and the name it plays under,
// which its player's page gave it, says whose it is. So the one in the game is the one whose game it is, and the
// game's word of a slot is of its player.
func TestT6RoomTellsPlayersOfOneAddressApart(t *testing.T) {
	l, a, _, _, bobsGame := oneAddress(t)
	// the game's word of slot 0, the first to come, is of Bob
	bobsGame.Write([]byte("\xff\xff\xff\xffover"))
	res := waitView(t, l, a, "game over", func(v view) bool { return v.Room.Result != nil }).Room.Result
	scores := map[string]int{}
	for _, p := range res.Players {
		scores[p.Name] = p.Score
	}
	if scores["Bob"] != 1700 || scores["Ann"] != 2700 {
		t.Errorf("the report's scores, slot 0 being Bob's game: %v", scores)
	}
}

// Leaving the room takes out one's own game, of two from one address, though it came first and had the other's seat
// until the server listed it.
func TestT6RoomKicksTheGameOfThePlayerWhoLeft(t *testing.T) {
	l, a, b, annsGame, bobsGame := oneAddress(t)
	do(l, b, "leave")
	pagesAt(l, "127.0.0.1", a)
	if !udpSilent(bobsGame, []byte("\x05\x00\x00\x00 a netchan packet")) {
		t.Error("the game of the player who left the room still reaches the server")
	}
	if udpSilent(annsGame, []byte("\x05\x00\x00\x00 a netchan packet")) {
		t.Error("the game of the player who stayed reaches the server no more")
	}
	v := waitView(t, l, a, "Ann alone in the room", func(v view) bool { return len(v.Room.Members) == 1 })
	if !v.Room.Members[0].InGame {
		t.Errorf("Ann, whose game is in: %+v", v.Room.Members[0])
	}
}

// t6Up is a T6 room's game up, with the players of the names in the room in that order, the first its host. Each
// one's page calls from an address of its own (127.0.0.2 and on), and each has a game there, which has yet to ask to
// connect.
func t6Up(t *testing.T, names ...string) (l *lobby, tokens []string, games []*net.UDPConn) {
	t.Helper()
	l, target := t6Lobby(t)
	to, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		token := l.hello("", name).Token
		if tokens = append(tokens, token); len(tokens) == 1 {
			do(l, token, "create", "map", "zm_standard_town")
		} else if err := do(l, token, "join", "room", l.viewOf(tokens[0]).Room.Code); err != "" {
			t.Fatal(err)
		}
	}
	pagesOpen(l, tokens...)
	for _, token := range tokens {
		if err := do(l, token, "ready", "ready", "true"); err != "" {
			t.Fatal(err)
		}
	}
	waitView(t, l, tokens[0], "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	for i, token := range tokens {
		address := fmt.Sprintf("127.0.0.%d", i+2)
		pagesAt(l, address, token)
		game, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP(address)}, to)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { game.Close() })
		games = append(games, game)
	}
	return l, tokens, games
}

// t6Ask has a game ask to connect, under a name, as a client's connect does.
func t6Ask(game *net.UDPConn, name string) {
	game.Write([]byte("\xff\xff\xff\xff" + `connect "\protocol\2089\name\` + name + `\qport\1"`))
}

// t6Slot is the slot that the stand-in for Plutonium's server answered a game's connect with. It is -1 when the game
// got no answer within the time: its connect waits for its turn.
func t6Slot(game *net.UDPConn, within time.Duration) int {
	game.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 2048)
	n, err := game.Read(buf)
	if err != nil {
		return -1
	}
	slot, err := strconv.Atoi(strings.TrimPrefix(string(buf[:n]), "\xff\xff\xff\xffconnectResponse "))
	if err != nil {
		return -1
	}
	return slot
}

// The players of a T6 room join its game in the order of their places in the room, whatever order their games ask to
// connect in. The game gives each the first free slot, so each has the slot of their place, and the game shows them
// in the colour their place has in the room.
func TestT6RoomLetsItsPlayersInByPlace(t *testing.T) {
	l, tokens, games := t6Up(t, "Ann", "Bob", "Cy")
	t6Ask(games[2], "Cy")
	t6Ask(games[1], "Bob")
	for _, i := range []int{2, 1} {
		if slot := t6Slot(games[i], 300*time.Millisecond); slot >= 0 {
			t.Fatalf("the game at place %d was let in before the host's, to slot %d", i, slot)
		}
	}
	t6Ask(games[0], "Ann")
	for place, game := range games {
		if slot := t6Slot(game, 5*time.Second); slot != place {
			t.Errorf("the game at place %d has slot %d", place, slot)
		}
	}
	v := waitView(t, l, tokens[0], "everyone in the game", func(v view) bool { return v.Room.State == "playing" })
	for place, m := range v.Room.Members {
		if m.Place != place || !m.InGame {
			t.Errorf("%s in the room: %+v", m.Name, m)
		}
	}
	// round 1 has begun: a game that comes back joins at once, whoever is out
	games[0].Write([]byte("\xff\xff\xff\xffleave"))
	games[2].Write([]byte("\xff\xff\xff\xffleave"))
	waitView(t, l, tokens[0], "Ann and Cy out of the game", func(v view) bool {
		return !v.Room.Members[0].InGame && !v.Room.Members[2].InGame
	})
	t6Ask(games[2], "Cy")
	if slot := t6Slot(games[2], 5*time.Second); slot != 0 {
		t.Errorf("Cy's game, back in a game that has begun, Ann's being out: slot %d", slot)
	}
}

// A player whose game doesn't come is waited for only so long (joinWait). Then the others join without them, in their
// own order, and the one who comes late has the slot that is left.
func TestT6RoomWaitsOnlySoLongForAPlayersGame(t *testing.T) {
	l, _, games := t6Up(t, "Ann", "Bob", "Cy")
	l.mu.Lock()
	l.joinWait = 500 * time.Millisecond
	l.mu.Unlock()
	began := time.Now()
	t6Ask(games[2], "Cy")
	t6Ask(games[1], "Bob")
	bob := t6Slot(games[1], 5*time.Second)
	waited := time.Since(began)
	if cy := t6Slot(games[2], 5*time.Second); bob != 0 || cy != 1 || waited < 500*time.Millisecond {
		t.Errorf("Bob's and Cy's games, Ann's not having come: slots %d and %d, after %s", bob, cy, waited)
	}
	t6Ask(games[0], "Ann")
	if slot := t6Slot(games[0], 5*time.Second); slot != 2 {
		t.Errorf("Ann's game, which came late: slot %d", slot)
	}
}

// A game that asks to connect no more is not let in when its turn comes: its connect, which waited, is dropped, and
// the room's game takes in no client that is not there. When the game asks again, it joins.
func TestT6RoomDropsTheConnectOfAGameThatAsksNoMore(t *testing.T) {
	l, _, games := t6Up(t, "Ann", "Bob")
	l.mu.Lock()
	l.joinWait = time.Second
	l.mu.Unlock()
	l.relay.mu.Lock()
	l.relay.asks = 300 * time.Millisecond
	l.relay.mu.Unlock()
	t6Ask(games[1], "Bob")
	// Ann's game doesn't come, and the wait for it ends: Bob's game, which sent one connect and no more, is not let in
	if slot := t6Slot(games[1], 3*time.Second); slot >= 0 {
		t.Fatalf("the connect of Bob's game, which asked no more, was passed on: slot %d", slot)
	}
	t6Ask(games[1], "Bob")
	if slot := t6Slot(games[1], 5*time.Second); slot != 0 {
		t.Errorf("Bob's game, asking again, the wait for Ann's being over: slot %d", slot)
	}
}

// No one waits for a player whose launcher still gets their PC ready, which can take any time.
func TestT6RoomWaitsForNoPlayerWhoseLauncherIsAtWork(t *testing.T) {
	l, tokens, games := t6Up(t, "Ann", "Bob")
	l.mu.Lock()
	key := l.playerOf(tokens[0]).key
	l.mu.Unlock()
	asLauncher(&web{lobby: l}, http.MethodGet, "/launcher/t6/files", key, "")
	t6Ask(games[1], "Bob")
	if slot := t6Slot(games[1], time.Second); slot != 0 {
		t.Errorf("Bob's game, Ann's launcher downloading: slot %d", slot)
	}
}

// Two players behind one address: a game's connect says the name it plays under, so the server knows whose game it
// is before the game is in. Bob's, the first to ask, has the host's seat until then, and waits for Ann's.
func TestT6RoomLetsPlayersOfOneAddressInByPlace(t *testing.T) {
	l, target := t6Lobby(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	pagesOpen(l, a, b)
	do(l, a, "ready", "ready", "true")
	do(l, b, "ready", "ready", "true")
	waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", a, b)
	annsGame, bobsGame := windowsPlayer(t, target), windowsPlayer(t, target)
	t6Ask(bobsGame, "Bob")
	if slot := t6Slot(bobsGame, 300*time.Millisecond); slot >= 0 {
		t.Fatalf("Bob's game was let in before Ann's, the host's, to slot %d", slot)
	}
	t6Ask(annsGame, "Ann")
	if ann, bob := t6Slot(annsGame, 5*time.Second), t6Slot(bobsGame, 5*time.Second); ann != 0 || bob != 1 {
		t.Errorf("the slots of Ann's game and Bob's: %d and %d", ann, bob)
	}
	waitView(t, l, a, "both in the game", func(v view) bool { return v.Room.State == "playing" })
	// the game's word of slot 0 is of Ann, and of slot 1 of Bob
	annsGame.Write([]byte("\xff\xff\xff\xffover"))
	res := waitView(t, l, a, "game over", func(v view) bool { return v.Room.Result != nil }).Room.Result
	scores := map[string]int{}
	for _, p := range res.Players {
		scores[p.Name] = p.Score
	}
	if scores["Ann"] != 1700 || scores["Bob"] != 2700 {
		t.Errorf("the report's scores, slot 0 being Ann's game: %v", scores)
	}
}

// A T6 game ends as its server rotates its map, where no script of the server's said the game was over (a server whose
// Plutonium loads none): game over with a player in it; and once its last player has left it, everyone left.
func TestT6RoomEndsAsTheServerRotates(t *testing.T) {
	for _, tc := range []struct {
		leave bool
		note  string
	}{{false, "Game over."}, {true, "Everyone left the game."}} {
		l, target := t6Lobby(t)
		a := l.hello("", "Ann").Token
		do(l, a, "create", "map", "zm_standard_town")
		pagesOpen(l, a)
		do(l, a, "ready", "ready", "true")
		waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
		pagesAt(l, "127.0.0.1", a)
		annsGame := windowsPlayer(t, target)
		annsGame.Write([]byte("\xff\xff\xff\xffconnectReq"))
		waitView(t, l, a, "the game begun", func(v view) bool { return v.Room.State == "playing" })
		if tc.leave {
			annsGame.Write([]byte("\xff\xff\xff\xffleave"))
			waitView(t, l, a, "Ann out of the game", func(v view) bool { return !v.Room.Members[0].InGame })
		}
		annsGame.SetDeadline(time.Time{})
		annsGame.Write([]byte("\xff\xff\xff\xffrotate"))
		v := waitView(t, l, a, "the room open again", func(v view) bool { return v.Room.State == "open" })
		if v.Room.Note != tc.note {
			t.Errorf("the room's note: %q, not %q", v.Room.Note, tc.note)
		}
	}
}

// Round 1 begins when the server says it does, which Plutonium's own script has it do without the players it has
// waited for long enough: the game is not called off then for the one who isn't in, and the round is the record of
// who is.
func TestT6RoomBeginsAsItsServerSays(t *testing.T) {
	l, target := t6Lobby(t)
	l.loadTimeout = 2 * time.Second
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	pagesOpen(l, a, b)
	do(l, a, "ready", "ready", "true")
	do(l, b, "ready", "ready", "true")
	waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", a)
	annsGame := windowsPlayer(t, target)
	annsGame.Write([]byte("\xff\xff\xff\xffconnectReq"))
	waitView(t, l, a, "Ann in the game", func(v view) bool { return v.Room.Members[0].InGame })
	annsGame.Write([]byte("\xff\xff\xff\xffround 1"))
	waitView(t, l, a, "the game begun", func(v view) bool { return v.Room.State == "playing" })
	time.Sleep(l.loadTimeout + time.Second)
	if v := l.viewOf(a); v.Room.State != "playing" || v.Room.Members[1].InGame {
		t.Fatalf("the game, its server having begun it without Bob: %s, %+v", v.Room.State, v.Room.Members[1])
	}
	l.mu.Lock()
	rows := l.leaderboards(l.playerOf(a)).of(t6).Rounds.Maps[0].Duo
	l.mu.Unlock()
	if len(rows) != 1 || rows[0].Name != "Ann" || rows[0].Round != 1 || rows[0].Party != 2 {
		t.Errorf("the duo board after round 1: %+v", rows)
	}
}

// The end of the map's main quest, as the server's script in the game says it, is the easter egg's record of the
// players in the game then, with the party the game started with, on T6's boards. It names the game, whose report is
// that of the last round.
func TestT6RoomKeepsTheEasterEgg(t *testing.T) {
	l, target := t6Lobby(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_classic_tomb")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	pagesOpen(l, a, b)
	do(l, a, "ready", "ready", "true")
	do(l, b, "ready", "ready", "true")
	waitView(t, l, a, "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", a, b)
	annsGame, bobsGame := windowsPlayer(t, target), windowsPlayer(t, target)
	for _, game := range []*net.UDPConn{annsGame, bobsGame} {
		game.Write([]byte("\xff\xff\xff\xffconnectReq"))
		game.Read(make([]byte, 2048))
	}
	waitView(t, l, a, "both in the game", func(v view) bool { return v.Room.State == "playing" })
	annsGame.Write([]byte("\xff\xff\xff\xffround 3"))
	waitFor(t, "both players' round 3", func() bool { rounds, _ := recordsOf(l, b); return len(rounds) == 1 })
	bobsGame.Write([]byte("\xff\xff\xff\xffleave"))
	waitView(t, l, a, "Bob out of the game", func(v view) bool { return !v.Room.Members[1].InGame })
	annsGame.Write([]byte("\xff\xff\xff\xffegg"))
	waitFor(t, "Ann's easter egg", func() bool { _, eggs := recordsOf(l, a); return len(eggs) == 1 })
	rounds, eggs := recordsOf(l, a)
	if e := eggs[0]; e.Map != "zm_classic_tomb" || e.Party != 2 || e.Seconds < 1 || e.Game != rounds[0].Game {
		t.Errorf("Ann's easter egg: %+v", e)
	}
	if _, eggs := recordsOf(l, b); len(eggs) != 0 {
		t.Errorf("the easter egg of Bob, who had left: %+v", eggs)
	}
	l.mu.Lock()
	boards := l.leaderboards(l.playerOf(a))
	l.mu.Unlock()
	duo := boards.of(t6).Eggs.Maps[0].Duo
	if len(duo) != 1 || duo[0].Name != "Ann" || duo[0].Seconds != eggs[0].Seconds {
		t.Errorf("Origins' easter egg: %+v", duo)
	}
	if res := boards.Games[duo[0].Game]; res == nil || res.Round != 3 || len(res.Players) != 2 {
		t.Errorf("the easter egg's report: %+v", res)
	}
	if all := boards.of(t6).Eggs.All; len(all.Rows) != 1 || all.Mine.Seconds != eggs[0].Seconds {
		t.Errorf("T6's board of all its easter eggs: %+v", all)
	}
}

// A server without T6's game files plays no T6 map.
func TestT6NeedsItsGameFiles(t *testing.T) {
	if maps := t6Installed(t.TempDir()); maps != nil {
		t.Errorf("the T6 maps of no game files: %v", maps)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "zone", "all"), 0o755)
	for _, name := range []string{"binkw32.dll", "zone/all/patch_zm.ff", "zone/all/zm_transit.ff", "zone/all/zm_tomb.ff",
		"zone/all/zm_transit_dr.ff"} {
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), nil, 0o644)
	}
	// in the page's order: the main maps, then those of the survival mode alone, then those of Grief and of Turned.
	// The Diner is a zone of its own.
	want := []string{"zm_classic_transit", "zm_classic_tomb", "zm_standard_transit", "zm_standard_town", "zm_standard_farm",
		"zm_grief_transit", "zm_grief_town", "zm_grief_farm", "zm_cleansed_diner"}
	if maps := t6Installed(dir); !slices.Equal(maps, want) {
		t.Errorf("the T6 maps: %v", maps)
	}
	l := testLobby(t)
	a := l.hello("", "Ann").Token
	if err := do(l, a, "create", "map", "zm_standard_town"); err != "" || l.viewOf(a).Room.Map != "zombie_theater" {
		t.Errorf("a room on a map the server lacks: %q, on %s", err, l.viewOf(a).Room.Map)
	}
	for _, m := range mapTitles {
		if _, ok := t6Maps[m.name]; ok != (m.of == t6) {
			t.Errorf("%s is of %s, and a T6 map: %v", m.name, m.of, ok)
		}
	}
}

// t6Versus is t6Lobby, whose game files have T6 maps of co-op, Grief and Turned.
func t6Versus(t *testing.T) (*lobby, string) {
	t.Helper()
	l, target := t6Lobby(t)
	l.maps = func() []string {
		return []string{"zombie_theater", "zm_standard_town", "zm_grief_town", "zm_grief_farm", "zm_cleansed_diner"}
	}
	return l, target
}

// t6Playing has the room's members ready, and each one's game join the room's game from one address, in the order of
// the tokens: the game gives them its slots in that order. It returns their games once the room plays.
func t6Playing(t *testing.T, l *lobby, target string, tokens ...string) []*net.UDPConn {
	t.Helper()
	pagesOpen(l, tokens...)
	for _, token := range tokens {
		if err := do(l, token, "ready", "ready", "true"); err != "" {
			t.Fatal(err)
		}
	}
	waitView(t, l, tokens[0], "the server up", func(v view) bool { return v.Room.T6 != nil && v.Room.T6.Up })
	pagesAt(l, "127.0.0.1", tokens...)
	var games []*net.UDPConn
	for range tokens {
		game := windowsPlayer(t, target)
		game.SetDeadline(time.Now().Add(5 * time.Second))
		game.Write([]byte("\xff\xff\xff\xffconnectReq"))
		if _, err := game.Read(make([]byte, 2048)); err != nil {
			t.Fatal(err)
		}
		game.SetDeadline(time.Time{})
		games = append(games, game)
	}
	waitView(t, l, tokens[0], "everyone in the game", func(v view) bool { return v.Room.State == "playing" })
	return games
}

// A room's host picks how its players play against each other (its versus): the room goes to a map of Grief or of
// Turned, which the server has, and its marks of other maps go. A room of Grief takes up to 8 players on a server whose
// rooms take 4, and shows its members' teams. Back in co-op, its size is 4 again, which a room with more players than
// that can't go back to. A versus again is the map the room last had of it.
func TestRoomVersus(t *testing.T) {
	l, _ := t6Versus(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	if v := l.viewOf(a); v.Room.MaxSize != 4 {
		t.Errorf("a co-op room's largest size: %d", v.Room.MaxSize)
	}
	if err := do(l, a, "room", "size", "8"); err != "Room size can't be 8." {
		t.Errorf("a co-op room of 8, on a server of 4: %q", err)
	}
	do(l, b, "mark", "map", "zm_standard_town")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"versus", "nope"}, "There is no mode nope."},
		{[]string{"versus", "grief", "map", "zm_cleansed_diner"}, "Diner is not a Grief map."},
		{[]string{"versus", "grief", "title", "t5"}, "The server doesn't have Grief maps of Black Ops."},
	} {
		if err := do(l, a, "room", tc.args...); err != tc.want {
			t.Errorf("%v: %q, want %q", tc.args, err, tc.want)
		}
	}
	if err := do(l, a, "room", "versus", "grief"); err != "" {
		t.Fatal(err)
	}
	v := l.viewOf(a)
	if v.Room.Map != "zm_grief_town" || v.Room.MaxSize != 8 || v.Room.Members[1].Mark != "" {
		t.Fatalf("the room of Grief: %+v", v.Room)
	}
	if v.Room.Members[0].Team != 1 || v.Room.Members[1].Team != 2 {
		t.Errorf("the teams of a room of Grief: %+v", v.Room.Members)
	}
	if err := do(l, b, "mark", "map", "zm_standard_town"); err != "Town is not a Grief map." {
		t.Errorf("a mark of a co-op map in a room of Grief: %q", err)
	}
	if err := do(l, a, "room", "size", "8"); err != "" {
		t.Fatal(err)
	}
	var more []string
	for _, name := range []string{"Cid", "Dan", "Eve"} {
		token := l.hello("", name).Token
		if err := do(l, token, "join", "room", v.Room.Code); err != "" {
			t.Fatal(err)
		}
		more = append(more, token)
	}
	if err := do(l, a, "room", "versus", ""); err != "The room has 5 players, and Co-op takes 4." {
		t.Errorf("back to co-op with 5 players: %q", err)
	}
	do(l, more[2], "leave")
	if err := do(l, a, "room", "versus", ""); err != "" {
		t.Fatal(err)
	}
	if v := l.viewOf(a); v.Room.Map != "zm_standard_town" || v.Room.Size != 4 || v.Room.MaxSize != 4 {
		t.Errorf("the room back in co-op: %+v", v.Room)
	}
	do(l, a, "room", "map", "zm_grief_farm")
	do(l, a, "room", "versus", "turned")
	if err := do(l, a, "room", "versus", "grief"); err != "" || l.viewOf(a).Room.Map != "zm_grief_farm" {
		t.Errorf("Grief again: %q, %s", err, l.viewOf(a).Room.Map)
	}
}

// A room's game of Grief, on a T6 map: the game takes 8 players, and puts each one in the team of their place, by the
// name they play under; a round is no record; at game over, the room's report has the team that won, as the game says
// it, first, with the teams that the game says; and the game sets no record, nor is its report kept.
func TestT6Grief(t *testing.T) {
	l, target := t6Versus(t)
	a, b, c := l.hello("", "Ann").Token, l.hello("", "Bob").Token, l.hello("", "Cid").Token
	do(l, a, "create", "map", "zm_grief_town")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	do(l, c, "join", "room", l.viewOf(a).Room.Code)
	games := t6Playing(t, l, target, a, b, c)
	home := t6Home(l, a)
	cfg, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "lobby.cfg"))
	gts, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "gamesettings", "lobby_gts.cfg"))
	for _, line := range []string{"sv_maxclients 8", "zombies_minplayers 3", `set zombies_team_1 "Ann/Bob"`,
		`set zombies_team_2 "Cid"`, `sv_maprotation "execgts lobby_gts.cfg map zm_transit"`} {
		if !strings.Contains(string(cfg), line+"\n") {
			t.Errorf("the game's config lacks %s:\n%s", line, cfg)
		}
	}
	for _, line := range []string{"execgts gamesettings_zgrief.cfg", `ui_zm_gamemodegroup "zencounter"`,
		`g_gametype "zgrief"`, `ui_zm_mapstartlocation "town"`} {
		if !strings.Contains(string(gts), line+"\n") {
			t.Errorf("the game's settings lack %s:\n%s", line, gts)
		}
	}
	games[0].Write([]byte("\xff\xff\xff\xffround 3"))
	games[0].Write([]byte("\xff\xff\xff\xffover 4 2"))
	res := waitView(t, l, a, "the room's report", func(v view) bool { return v.Room.Result != nil }).Room.Result
	type line struct {
		name string
		team int
		won  bool
	}
	var got []line
	for _, p := range res.Players {
		got = append(got, line{p.Name, p.Team, p.Won})
	}
	// the game says that Bob, in its second slot, is in team 2, and that team 2 won
	if want := []line{{"Bob", 2, true}, {"Cid", 1, false}, {"Ann", 1, false}}; !slices.Equal(got, want) {
		t.Errorf("the report's lines: %+v, want %+v", got, want)
	}
	if res.Round != 4 {
		t.Errorf("the report's round: %d", res.Round)
	}
	waitView(t, l, a, "the room open", func(v view) bool { return v.Room.State == "open" })
	for _, token := range []string{a, b, c} {
		if rounds, eggs := recordsOf(l, token); len(rounds) != 0 || len(eggs) != 0 {
			t.Errorf("records of a game of Grief: %+v, %+v", rounds, eggs)
		}
	}
	l.mu.Lock()
	boards, reports := l.leaderboards(l.playerOf(a)), len(l.reports)
	l.mu.Unlock()
	if maps := boards.of(t6).Rounds.Maps; len(maps) != 1 || maps[0].Map != "zm_standard_town" || reports != 0 {
		t.Errorf("T6's boards, with maps of Grief and Turned: %+v, and %d reports", maps, reports)
	}
}

// A room's game of Turned, on a T6 map: at game over, the room's report has no round, and the player with the most of
// the points that the game ranks by won. The lines have no team, whatever the game says.
func TestT6Turned(t *testing.T) {
	l, target := t6Versus(t)
	a, b := l.hello("", "Ann").Token, l.hello("", "Bob").Token
	do(l, a, "create", "map", "zm_standard_town")
	do(l, a, "room", "versus", "turned")
	do(l, b, "join", "room", l.viewOf(a).Room.Code)
	if v := l.viewOf(a); v.Room.Map != "zm_cleansed_diner" || v.Room.MaxSize != 4 || v.Room.Members[0].Team != 0 {
		t.Fatalf("the room of Turned: %+v", v.Room)
	}
	games := t6Playing(t, l, target, a, b)
	home := t6Home(l, a)
	cfg, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "lobby.cfg"))
	gts, _ := os.ReadFile(filepath.Join(home, "storage", "t6", "gamesettings", "lobby_gts.cfg"))
	if !strings.Contains(string(cfg), `sv_maprotation "execgts lobby_gts.cfg map zm_transit_dr"`) ||
		strings.Contains(string(cfg), "zombies_team") {
		t.Errorf("the game's config:\n%s", cfg)
	}
	if !strings.Contains(string(gts), `g_gametype "zcleansed"`) || !strings.Contains(string(gts), `"diner"`) {
		t.Errorf("the game's settings:\n%s", gts)
	}
	games[0].Write([]byte("\xff\xff\xff\xffover 1"))
	res := waitView(t, l, a, "the room's report", func(v view) bool { return v.Room.Result != nil }).Room.Result
	if res.Round != 0 || len(res.Players) != 2 || res.Players[0].Name != "Bob" || !res.Players[0].Won ||
		res.Players[1].Won || res.Players[0].Team != 0 {
		t.Errorf("the report of a game of Turned: %+v", res)
	}
	if rounds, _ := recordsOf(l, a); len(rounds) != 0 {
		t.Errorf("Ann's records of a game of Turned: %+v", rounds)
	}
}

// How a T6 game says it ended, while its end plays out.
func TestGameOverText(t *testing.T) {
	ann := &gameResult{Players: []playerStats{{Name: "Ann"}}}
	for _, tc := range []struct {
		versus     string
		round, won int
		want       string
	}{
		{coop, 12, 0, "Game over on round 12."},
		{coop, 0, 0, "Game over."},
		{grief, 5, 2, "Team 2 won on round 5."},
		{grief, 0, 1, "Team 1 won."},
		{grief, 5, 0, "Game over on round 5."},
		{turned, 0, 0, "Ann won."},
	} {
		if got := gameOverText(tc.versus, tc.round, tc.won, ann); got != tc.want {
			t.Errorf("%q, round %d, team %d: %q, want %q", tc.versus, tc.round, tc.won, got, tc.want)
		}
	}
}
