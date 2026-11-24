package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// t5 is the id of Black Ops, which runs on the engine. A room's game is BO1Zombies.exe as a dedicated server. Its
// players join it from their page, or from Windows with their key.
const t5 = "t5"

var t5MapList = []mapEntry{
	{of: t5, name: "zombie_theater", title: "Kino der Toten", preview: "zombie_theater",
		loadscreen: "loadscreen_zombie_theater", description: "MENU_ZOMBIE_THEATER_DESC"},
	{of: t5, name: "zombie_pentagon", title: "“Five”", preview: "zombie_pentagon",
		loadscreen: "loadscreen_zombie_pentagon", description: "MENU_ZOMBIE_PENTAGON_DESC"},
	// Dead Ops Arcade is off until it runs on a dedicated server:
	// {of: t5, name: "zombietron", title: "Dead Ops Arcade", preview: "zombietron",
	//	loadscreen: "loadscreen_zombie_deadops", description: "MENU_LEVEL_DESC_ZOMBIETRON"},
	{of: t5, name: "zombie_cosmodrome", title: "Ascension", preview: "zombie_cosmo",
		loadscreen: "loadscreen_zombie_cosmodrome", description: "MENU_ZOMBIE_COSMODROME_DESC"},
	{of: t5, name: "zombie_coast", title: "Call of the Dead", preview: "zombie_coast",
		loadscreen: "loadscreen_zombie_coast", description: "MENU_ZOMBIE_COAST_DESC"},
	{of: t5, name: "zombie_temple", title: "Shangri-La", preview: "zombie_temple",
		loadscreen: "loadscreen_zombie_temple", description: "MENU_LEVEL_DESC_ZOMBIE_TEMPLE"},
	{of: t5, name: "zombie_moon", title: "Moon", preview: "zombie_moon",
		loadscreen: "loadscreen_zombie_moon", description: "MENU_LEVEL_DESC_ZOMBIE_MOON"},
	{of: t5, name: "zombie_cod5_prototype", title: "Nacht der Untoten", preview: "loadscreen_zombie_prototype_mini",
		loadscreen: "loadscreen_zombie_prototype", description: "WAW_MENU_DESC_ZOMBIE_PROTOTYPE"},
	{of: t5, name: "zombie_cod5_asylum", title: "Verrückt", preview: "loadscreen_zombie_asylum_mini",
		loadscreen: "loadscreen_zombie_asylum", description: "WAW_MENU_DESC_ZOMBIE_ASYLUM"},
	{of: t5, name: "zombie_cod5_sumpf", title: "Shi No Numa", preview: "loadscreen_zombie_sumpf_mini",
		loadscreen: "loadscreen_zombie_sumpf", description: "WAW_MENU_DESC_ZOMBIE_SUMPF"},
	{of: t5, name: "zombie_cod5_factory", title: "Der Riese", preview: "loadscreen_zombie_factory_mini",
		loadscreen: "loadscreen_zombie_factory", description: "WAW_MENU_DESC_ZOMBIE_FACTORY"},
}

// t5Title is the title (title.go) for Black Ops.
type t5Title struct {
	// newGame makes the game for a room on the given port, with the room's mods. The game waits for the given number
	// of players before round 1 (bo1_players).
	newGame func(code, mapName string, port, players int, mods []string) *game
}

// t5 returns the server's Black Ops title.
func (l *lobby) t5() *t5Title {
	for _, t := range l.titles {
		if t, ok := t.(*t5Title); ok {
			return t
		}
	}
	return nil
}

func (*t5Title) id() string    { return t5 }
func (*t5Title) label() string { return "Black Ops" }
func (*t5Title) mods() bool    { return true }
func (*t5Title) inPage() bool  { return true }

func (t *t5Title) free(*lobby) error {
	if t.newGame == nil {
		return errors.New("The server doesn't have Black Ops.")
	}
	return nil
}

func (t *t5Title) begin(l *lobby, r *room, rg *roomGame) *game {
	g := t.newGame(r.code, r.mapName, rg.port, len(rg.seats), r.mods)
	g.lines = func(line string) { l.gameLine(r, rg, line) }
	if e, ok := g.engine.(*t5Engine); ok {
		e.exec = append(placesOf(rg.seats), keptOf(rg.seats)...)
	}
	return g
}

func (*t5Title) done(*lobby, *roomGame) {}

// drop notifies the game that the seat's player left the room, which means they left the game (bo1_lobby_drop,
// bo1-zombies/src/server_mp/sv_lobby_mp.cpp). The game otherwise keeps the character of a player who quit, as it
// keeps the character of a player who dropped.
func (*t5Title) drop(l *lobby, r *room, s *seat) {
	if !s.gone {
		r.game.g.command("bo1_lobby_drop " + s.local.String())
	}
}

// late calls off a game whose players are not all in within loadTimeout of its start.
func (*t5Title) late(l *lobby, r *room, rg *roomGame) {
	if time.Since(rg.began) < l.loadTimeout {
		return
	}
	var out []*seat
	for _, s := range rg.seats {
		if !s.in {
			out = append(out, s)
		}
	}
	// Everyone is in. Round 1 only waits for the players who still watch their loading movie, and the movie ends by
	// itself (Call of the Dead's takes two minutes; bo1-zombies/src/client/cl_cin.cpp).
	if len(out) == 0 {
		return
	}
	l.stop(r, fmt.Sprintf("Game called off: %s didn't join in time.", names(out)))
}

// lacks reports whether the player's page is missing some of the map's game files. It is false for a player on
// Windows, who plays from their own copy of the game.
func (*t5Title) lacks(l *lobby, p *player) bool {
	return !p.files.has(p.room.mapName) && !p.shown.windows && !l.onWindows(p)
}

func (*t5Title) showMember(l *lobby, r *room, m *player, mv *memberView) {
	if r.game == nil && !m.shown.windows && !m.files.has(r.mapName) {
		mv.Missing = true
		if f := m.files; f.running && f.mapName == r.mapName {
			mv.Downloading = true
			if f.need > 0 {
				mv.Got = min(float64(f.got)/float64(f.need), 1)
			}
		}
	}
}

func (*t5Title) showRoom(*lobby, *room, *player, *roomView) {}

// installedMaps returns a function that lists the Zombies maps that t5's game files have. paths returns the game
// files' paths, in lowercase. The game files have a map when its fastfile is in any of zone's folders.
func installedMaps(paths func() []string) func() []string {
	return func() []string {
		have := map[string]bool{}
		for _, p := range paths() {
			if zone, ok := strings.CutPrefix(p, "zone/"); ok {
				if dir, file, ok := strings.Cut(zone, "/"); ok && dir != "" && strings.HasSuffix(file, ".ff") {
					have[strings.TrimSuffix(file, ".ff")] = true
				}
			}
		}
		var maps []string
		for _, m := range mapTitles {
			if m.of == t5 && have[m.name] {
				maps = append(maps, m.name)
			}
		}
		return maps
	}
}

// placesOf returns console lines that give the game each seat's place in its room. The game runs them when it starts
// (t5Engine.exec). The host's seat is first, then the others in the order their players joined (room.members). The
// game gives each player the client slot of their place (bo1-zombies/src/server_mp/sv_lobby_mp.cpp:
// SV_Lobby_FreeSlot), and the level gives them their spawn point and character by that slot. The host is player 1,
// as in Black Ops itself, where the host's own game hosted the others.
func placesOf(seats []*seat) []string {
	lines := make([]string, len(seats))
	for i, s := range seats {
		lines[i] = fmt.Sprintf("bo1_lobby_seat %s %d", s.local, i)
	}
	return lines
}

// keptOf returns console lines that give the game what earlier games kept of the seats' players: their stats and
// collectibles (player.stats). The game runs them when it starts (t5Engine.exec). Each line names a player by their
// seat's address (bo1-zombies/src/server_mp/sv_lobby_mp.cpp).
func keptOf(seats []*seat) []string {
	var lines []string
	for _, s := range seats {
		p := s.player
		if p.collectibles != "" {
			lines = append(lines, fmt.Sprintf("bo1_lobby_collectibles %s %s", s.local, p.collectibles))
		}
		for _, name := range slices.Sorted(maps.Keys(p.stats)) {
			lines = append(lines, fmt.Sprintf("bo1_lobby_dstat %s %s %d", s.local, name, p.stats[name]))
		}
	}
	return lines
}

// gameLine reads the "bo1_lobby: " lines that a room's game prints (bo1-zombies/src/game_mp,
// bo1-zombies/src/server_mp). They report who is in the game, when round 1 begins, and what the game keeps of its
// players.
func (l *lobby) gameLine(r *room, rg *roomGame, line string) {
	what, ok := strings.CutPrefix(line, "bo1_lobby: ")
	if !ok {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.game != rg {
		return
	}
	verb, arg, _ := strings.Cut(strings.TrimSpace(what), " ")
	switch verb {
	case "in", "out": // a client entered the game, or left it together with its character: "in 127.1.0.4:28960"
		for _, s := range rg.seats {
			if s.local.String() == arg {
				s.in, s.gone = verb == "in", verb == "out"
			}
		}
		l.checkGame(r)
	case "quit": // a client quit the game (it disconnected), and the game keeps its character: "quit 127.1.0.4:28960"
		for _, s := range rg.seats {
			if s.local.String() == arg {
				l.quitGame(r, s)
			}
		}
	case "start": // round 1 begins: everyone is in
		rg.started, rg.round1 = true, time.Now()
		log.Printf("server: room %s's game has begun", r.code)
	// A player's line of the scoreboard, printed before "round" and "over". For example,
	// "stats 127.1.0.4:28960 1250 400 14 3 0 1 2". The last number is the doors they bought, which an engine build
	// that does not count doors leaves out.
	case "stats":
		if fields := strings.Fields(arg); len(fields) > 0 {
			rg.scored(fields[0], fields[1:])
		}
		return // no page shows them yet
	case "round": // a round begins: "round 5"
		round, _ := strconv.Atoi(arg)
		l.keepRounds(r, rg, l.result(r, rg, max(round, 0)))
		return // the leaderboards are read when a page opens them
	case "over": // game over, with the round it ended on when the level counts rounds: "over 5"
		round, _ := strconv.Atoi(arg)
		rg.result = l.result(r, rg, max(round, 0))
		rg.ending = gameOverText(rg.result)
		l.keepRounds(r, rg, rg.result)
		log.Printf("server: room %s's game is over", r.code)
	case "dstat": // the level changed a player's stat: "dstat 127.1.0.4:28960 zombie_kills 412"
		fields := strings.Fields(arg)
		if len(fields) != 3 || !statName.MatchString(fields[1]) {
			return
		}
		value, err := strconv.ParseInt(fields[2], 10, 32)
		if err != nil {
			return
		}
		for _, s := range rg.seats {
			p := s.player
			if s.local.String() != fields[0] {
				continue
			}
			if _, ok := p.stats[fields[1]]; !ok && len(p.stats) >= keptStats {
				continue
			}
			if p.stats == nil {
				p.stats = map[string]int{}
			}
			p.stats[fields[1]] = int(value)
			if rg.started && slices.ContainsFunc(eggs, func(e struct{ mapName, stat string }) bool {
				return e.stat == fields[1] && e.mapName == r.mapName
			}) {
				if rg.eggSeconds == 0 {
					rg.eggSeconds = max(int64(time.Since(rg.round1).Seconds()), 1)
				}
				p.keepEgg(r.mapName, len(rg.seats), rg.eggSeconds, gameID(r, rg))
				if rg.report != nil {
					l.keepGame(rg.report) // so that the game has a report however it ends
				}
			}
			l.save()
		}
		return // no page shows them
	case "collectibles": // a player's client set a collectible; all of theirs follow: "collectibles 127.1.0.4:28960 0001"
		address, set, _ := strings.Cut(arg, " ")
		for _, s := range rg.seats {
			if s.local.String() == address && collectiblesForm.MatchString(set) {
				s.player.collectibles = set
				l.save()
			}
		}
		return
	}
	l.change()
}

// ---- the engine, as the runner runs it (game.go) ----

// t5Engine is the engine (game.go) of a T5 room's game: BO1Zombies.exe as a dedicated server, with its console on
// stdin and stdout (bo1-zombies/src/win32/win_syscon.cpp).
type t5Engine struct {
	exe  string   // the path of BO1Zombies.exe
	args []string // the rest of its command line (+set ..., +map ...)
	// exec is the console lines the game runs when it starts, before its map. They are written to main/lobby.cfg in
	// the game's home, which its command line execs. The console on stdin takes only one line a frame
	// (bo1-zombies/src/win32/win_syscon.cpp), and none while the game loads its map.
	exec []string
	// hangTimeout is how long the game runs without a frame before it ends itself, with a report of where it hung.
	// 0 disables it.
	hangTimeout time.Duration
}

// prepare writes the exec lines to main/lobby.cfg in the game's home. The game runs from the game files.
func (e *t5Engine) prepare(_ context.Context, g *game) ([]string, string, func(string) string, error) {
	if len(e.exec) > 0 {
		if err := g.mkdir(filepath.Join(g.home, "main")); err != nil {
			return nil, "", nil, err
		}
		cfg := strings.Join(e.exec, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(g.home, "main", "lobby.cfg"), []byte(cfg), 0o644); err != nil {
			return nil, "", nil, err
		}
	}
	return append([]string{e.exe}, e.commandLine(g)...), g.dir, nil, nil
}

func (*t5Engine) say(g *game, line string) { g.stdin.Write([]byte(line + "\n")) }

// quit asks nothing of the game. Wine turns the runner's signal into Ctrl+C, which quits the game like its quit
// command.
func (*t5Engine) quit(*game) bool { return false }

func (e *t5Engine) commandLine(g *game) []string {
	args := []string{
		"+set", "bo1_zombies", "1",
		"+set", "dedicated", "1",
		"+set", "fs_b", winePath(g.dir),
		"+set", "fs_h", winePath(g.home),
		"+set", "net_port", strconv.Itoa(g.port),
	}
	if g.ip != "" {
		args = append(args, "+set", "net_ip", g.ip)
	}
	if e.hangTimeout > 0 {
		args = append(args, "+set", "bo1_hangtimeout", strconv.FormatInt(e.hangTimeout.Milliseconds(), 10))
	}
	if len(e.exec) > 0 {
		args = append(args, "+exec", "lobby.cfg")
	}
	return append(args, e.args...)
}

// watch asks the game for a challenge every watchEvery, from watchAddr, until done is closed. A challenge is the
// first thing a client asks for. The game answers while it runs a map, and not while it loads one. An error that
// unloads the map ends the game (bo1-zombies/src/qcommon/common.cpp: Com_ErrorCleanup). The game's server thread
// answers, so a game whose main thread hangs still answers. Such a game ends itself (hangTimeout). When the game has
// not answered for startTimeout after its start, or for quietTimeout after an answer, watch sends the length of the
// silence on silent and returns.
func (*t5Engine) watch(g *game, done <-chan struct{}, silent chan<- time.Duration) {
	if g.quietTimeout <= 0 {
		return // unwatched
	}
	conn, err := net.DialUDP("udp4", watchAddr(g.port), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: g.port})
	if err != nil {
		g.logf("can't tell whether it answers: %v", err)
		return
	}
	defer conn.Close()
	var answered atomic.Int64 // when the game last answered (Unix nanoseconds), or 0 before its first answer
	go func() {
		buf := make([]byte, 2048)
		for {
			_, err := conn.Read(buf)
			if errors.Is(err, syscall.ECONNREFUSED) {
				continue // nothing listens on the game's port yet
			}
			if err != nil {
				return
			}
			answered.Store(time.Now().UnixNano())
		}
	}()
	start := time.Now()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		conn.Write([]byte("\xff\xff\xff\xffgetchallenge"))
		select {
		case <-done:
			return
		case <-tick.C:
		}
		since, limit := start, g.startTimeout
		if t := answered.Load(); t != 0 {
			since, limit = time.Unix(0, t), g.quietTimeout
		}
		if quiet := time.Since(since); quiet >= limit {
			silent <- quiet
			return
		}
	}
}
