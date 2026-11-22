package main

import (
	"bytes"
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

	"zombies/internal/plutonium"
	"zombies/internal/plutonium/plutoniumtest"
)

// fakeT6 is a stand-in for Plutonium's T6 server, which fakeGame is when its command line starts T6's Zombies. It
// runs in its home, as the server does: it reads its remote console's password from its config there
// (storage/t6/lobby.cfg), prints as Plutonium does, in colour after its prompt, and answers on its port. Its remote
// console, to the password: status, clientkick <slot> and quit; it prints any other line it gets. Its clients, which
// are in from their "connectReq", answered with the address it sees them at, until their "leave"; "connectReq Ann" is
// one that plays under that name, where the others are Player and their slot. A client's "round 3"
// is a round beginning, as the server's script in the game says it (t6Script), after each client's line of the
// scoreboard (stats), and its "over" game over, on round 7, with its end played out a moment later; its "rotate" is
// the server rotating its map with no word of the script's. Any other packet comes back as it is.
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
					say(fmt.Sprintf("zombies_lobby: stats %d %d %d %d %d %d %d %d", slot, 1000*(slot+1)+100*round,
						500*(slot+1), 10*round+slot, round, slot, 1-slot, 2))
				}
			}
		}
		round, isRound := strings.CutPrefix(text, "round ")
		if name, named := strings.CutPrefix(text, "connectReq "); oob && named {
			names[from.String()], text = name, "connectReq"
		}
		switch {
		case oob && isRound:
			n, _ := strconv.Atoi(round)
			stats(n)
			say("zombies_lobby: round " + round)
		case oob && text == "connectReq":
			if !slices.Contains(clients, from.String()) {
				if i := slices.Index(clients, ""); i >= 0 {
					clients[i] = from.String()
				} else {
					clients = append(clients, from.String())
				}
			}
			reply("connectRes " + from.String())
		case oob && text == "leave":
			if i := slices.Index(clients, from.String()); i >= 0 {
				clients[i] = ""
			}
		case oob && text == "over":
			stats(7)
			say("zombies_lobby: over 7")
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
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_theater", "zm_standard_town", "zm_classic_tomb"} }
	l.gamePorts = append(l.gamePorts, freePort(t))
	revision := 1
	updater := plutoniumtest.Serve(t, plutoniumFiles, &revision)
	pluto := &plutonium.Copy{Dir: filepath.Join(t.TempDir(), "plutonium"), URL: updater.URL, Needs: plutonium.T6,
		Client: updater.Client, State: "zombies-server-files.json"}
	root := t.TempDir()
	l.t6().pluto = pluto
	l.t6().newGame = func(code, mapName string, port, players int, password string) *game {
		return &game{name: code, wine: os.Args[0], dir: root, home: filepath.Join(root, "home", strconv.Itoa(port)),
			logDir: filepath.Join(root, "logs"), ip: "127.0.0.1", port: port, output: io.Discard,
			engine:       newT6Game(pluto, mapName, players, l.roomSize, l.loadTimeout, password),
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
	return l, udp.LocalAddr().String()
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
	duo := func() []boardRow { return boards().T6Rounds[0].Duo }
	waitFor(t, "both players on the duo board", func() bool { return len(duo()) == 2 })
	b3 := boards()
	if len(b3.Rounds) != 1 || len(b3.T6Rounds) != 2 || b3.T6Rounds[0].Map != "zm_standard_town" {
		t.Fatalf("the boards' maps: %+v, T6's %+v", b3.Rounds, b3.T6Rounds)
	}
	if rows := b3.T6Rounds[0].Duo; rows[0].Round != 3 || rows[1].Round != 3 || rows[0].Party != 2 {
		t.Errorf("the duo board after round 3: %+v", rows)
	}
	if all := b3.AllT6Rounds; len(all.Rows) != 2 || all.Mine.Round != 3 || b3.Games[all.Mine.Bests[0].Game] == nil {
		t.Errorf("T6's board of all its maps after round 3: %+v", all)
	}
	if n := len(b3.AllRounds.Rows); n != 0 {
		t.Errorf("T5's board of all its maps has %d players of a T6 game", n)
	}
	// another room can't play T6 meanwhile
	c := l.hello("", "Cy").Token
	do(l, c, "create", "map", "zm_classic_tomb")
	pagesOpen(l, c)
	do(l, c, "ready", "ready", "true")
	second := waitView(t, l, c, "the second T6 game refused", func(v view) bool { return v.Room.Note != "" }).Room
	if !strings.Contains(second.Note, "one such game at a time") || second.State != "open" {
		t.Errorf("a second T6 room: %q, %s", second.Note, second.State)
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
	l.mu.Lock()
	busy := l.t6().busy
	l.mu.Unlock()
	if busy {
		t.Error("the server still counts a T6 game")
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
	rows := l.leaderboards(l.playerOf(a)).T6Rounds[0].Duo
	l.mu.Unlock()
	if len(rows) != 1 || rows[0].Name != "Ann" || rows[0].Round != 1 || rows[0].Party != 2 {
		t.Errorf("the duo board after round 1: %+v", rows)
	}
}

// A server without T6's game files plays no T6 map.
func TestT6NeedsItsGameFiles(t *testing.T) {
	if maps := t6Installed(t.TempDir()); maps != nil {
		t.Errorf("the T6 maps of no game files: %v", maps)
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "zone", "all"), 0o755)
	for _, name := range []string{"binkw32.dll", "zone/all/patch_zm.ff", "zone/all/zm_transit.ff", "zone/all/zm_tomb.ff"} {
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), nil, 0o644)
	}
	want := []string{"zm_classic_transit", "zm_standard_transit", "zm_standard_town", "zm_standard_farm", "zm_classic_tomb"}
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
