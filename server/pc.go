package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The rooms of Black Ops II (t6.go) and of Black Ops III (t7.go) have games that their players play on their own PCs.
// Such a room is a PC room here, and its game a PC game: the title's dedicated server, which the server runs. Each
// player's own game is a client of it, a PC client, and reaches it through the relay, from the address that the
// player's page or launcher calls from (relay.go). This file has what the two titles share of a PC game: how the
// server watches it over its remote console (rconWatch), who is in it (pcStatus), what the server's script in it
// reports (pcSaid), and how its players' games get their seats and their turns to join (pcSeat, pcTurn). Much of it
// was found out on Plutonium's server, which the comments name where what they say is Plutonium's alone.

// ownPC reports whether rg is a PC game, which its players play on their own PCs and join from there: a T6 room's
// or a T7 room's.
func ownPC(rg *roomGame) bool {
	return rg != nil && rg.g != nil && (rg.g.t6() != nil || rg.g.t7() != nil)
}

// pcClient is a client in the status of a PC game: its slot, the name it plays under, the address the game
// sees it at, and whether it is in the game and no longer connecting.
type pcClient struct {
	slot    int
	name    string
	address string
	in      bool
}

// deafTimeout is how long a PC game whose level runs has to answer its remote console for the first time, before the
// server takes it for deaf to it and stops it (rconWatch). A game that answers does so within a second of its level's
// start. Tests lower it.
var deafTimeout = 30 * time.Second

// rcon is a game's remote console, as rconWatch uses it: the console of a game that its players join from their own
// PCs (t6.go, t7.go).
type rcon struct {
	password string        // the console's password, which only the server has
	send     <-chan string // lines for the console (game.command)
	// status is called with the clients that the game's status lists, each second that the game answers. A client is
	// listed by the address the game sees it at, which is a seat's address.
	status func([]pcClient)
	// parse reads a line of the game's answer to the status command (t6StatusLine).
	parse func(line string) (c pcClient, client, status bool)
	// gap is the least time between two packets to the console, for a game that drops one that comes sooner after
	// the last (t7.go).
	gap time.Duration
	// logged is set for a game that also writes each answer to its output, where the server reads it. rconWatch then
	// prints no answer but that of a status that a console line asked for, which the output leaves out.
	logged bool
}

// rconWatch watches a game over its remote console, which is also the game's console. It does so from watchAddr, the
// address the server also watches a T5 game from. rconWatch asks for the game's status every watchEvery and passes
// the clients it lists to c.status. It sends the game the lines that game.command queues, and prints the game's
// answers to them. The status also shows whether the game still answers. The game answers while it runs a map, and not
// while it loads one. When the game has not answered for startTimeout after its start, or for quietTimeout after an
// answer, rconWatch sends the length of the silence on silent and returns.
//
// startTimeout is long, because a start takes as long as the host's disk and its Wine take, and a game that is slow
// to start is not at fault. A game whose level runs has started, though, and answers within the second: the server's
// script in it says when that is (game.leveled). One that has never answered by deafTimeout after that is deaf to its
// console, however well it runs otherwise, and no player's game gets into it, because the server lets players in
// only from its first status. rconWatch reports that silence then, and the room's players are back in their room
// within the minute, where startTimeout would keep them for five.
func (g *game) rconWatch(c rcon, done <-chan struct{}, silent chan<- time.Duration) {
	conn, err := net.DialUDP("udp4", watchAddr(g.port), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: g.port})
	if err != nil {
		g.logf("no remote console, which the game is run by: %v", err)
		silent <- 0
		return
	}
	defer conn.Close()
	var mu sync.Mutex
	var answered time.Time // when the game last answered
	var clients []pcClient // the clients its status has listed since the server last asked
	listed := false        // the game has answered a status since the server last asked
	show := false          // a console line asked for the next status, so it is printed
	go func() {
		buf := make([]byte, 0x10000)
		for {
			n, err := conn.Read(buf)
			if errors.Is(err, syscall.ECONNREFUSED) {
				continue // nothing listens on the game's port yet
			}
			if err != nil {
				return
			}
			// "print", then a line break (Plutonium's server) or a space (BOIII's)
			text, ok := bytes.CutPrefix(buf[:n], []byte("\xff\xff\xff\xffprint"))
			if !ok || len(text) > 0 && text[0] != '\n' && text[0] != ' ' {
				continue
			}
			var say []string
			mu.Lock()
			answered = time.Now()
			for _, line := range strings.Split(string(text[min(len(text), 1):]), "\n") {
				line = strings.TrimRight(line, "\r\x00")
				cl, client, status := c.parse(line)
				// The status of a game that runs a map has the heading of its clients. BOIII's server answers
				// before it runs one too, with no clients to list, and is not up for its players yet.
				if status && strings.HasPrefix(line, "num score") {
					listed = true
				}
				if client {
					clients = append(clients, cl)
				}
				if line != "" && (status && show || !status && !c.logged) {
					say = append(say, line)
				}
			}
			mu.Unlock()
			for _, line := range say {
				fmt.Fprintf(g.output, "%s\n", line)
			}
		}
	}()
	var asked time.Time
	ask := func(line string) {
		if wait := c.gap - time.Since(asked); wait > 0 {
			time.Sleep(wait)
		}
		asked = time.Now()
		conn.Write(append(append([]byte{}, outOfBand...), "rcon "+c.password+" "+line...))
	}
	start := time.Now()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	ask("status")
	for {
		select {
		case <-done:
			return
		case line := <-c.send:
			mu.Lock()
			show = strings.EqualFold(strings.TrimSpace(line), "status")
			mu.Unlock()
			ask(line)
			continue
		case <-tick.C:
		}
		mu.Lock()
		now, ok, last := clients, listed, answered
		clients, listed, show = nil, false, false
		mu.Unlock()
		if ok && c.status != nil {
			c.status(now)
		}
		since, limit := start, g.startTimeout
		if leveled := g.leveled.Load(); !last.IsZero() {
			since, limit = last, g.quietTimeout
		} else if leveled != 0 && limit > deafTimeout {
			// The game runs a level and has never answered: it has had the time that any start takes.
			since, limit = time.Unix(0, leveled), deafTimeout
		}
		if quiet := time.Since(since); limit > 0 && quiet >= limit {
			silent <- quiet
			return
		}
		ask("status")
	}
}

// pcStatus handles the clients that the status of a PC room's game lists, each second that the game answers
// (t6Watch). The caller does not hold l.mu. The game is up from its first answer, and its players can join from then.
// A seat's player is in the game while the status lists a client at the seat's address, and the seat has that
// client's slot. The game has begun once everyone with a seat is in, because the game waits for that many players
// (zombies_minplayers). Its level starts round 1 some seconds after that, and the game's clock counts from the
// level's report of it (roundBegins).
func (l *lobby) pcStatus(r *room, rg *roomGame, clients []pcClient) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.game != rg {
		return
	}
	changed := false
	if rg.up.IsZero() {
		rg.up, changed = time.Now(), true
		log.Printf("server: room %s's game is up: its players join it", r.code)
	}
	// Several players can be behind one address. The seat of a game from that address is its player's by the name it
	// plays under (pcWhose). The game's connect said the name, when it had one. The status says it again here, for a
	// game whose connect did not.
	for _, c := range clients {
		i := slices.IndexFunc(rg.seats, func(s *seat) bool { return s.local.String() == c.address })
		if i >= 0 && rg.pcWhose(r.code, rg.seats[i], c.name) != rg.seats[i] {
			changed = true
		}
	}
	in, joined := 0, false
	for _, s := range rg.seats {
		i := slices.IndexFunc(clients, func(c pcClient) bool { return c.address == s.local.String() })
		slot := -1
		if i >= 0 {
			slot = clients[i].slot
		}
		joined = joined || slot >= 0 && s.slot < 0
		s.slot = slot
		if now := i >= 0 && clients[i].in; now != s.in {
			s.in, changed = now, true
			if now {
				log.Printf("server: %s is in room %s's game (as %s)", s.player.name, r.code, clients[i].name)
			} else {
				log.Printf("server: %s is out of room %s's game", s.player.name, r.code)
			}
		}
		if s.in {
			in++
		}
	}
	if !rg.started && in > 0 && !slices.ContainsFunc(rg.seats, func(s *seat) bool { return !s.in && !s.left }) {
		rg.started, rg.round1, changed = true, time.Now(), true
		log.Printf("server: room %s's game has begun", r.code)
	}
	if joined {
		l.relay.wakeUp() // the game has a client more: the next player's turn to join may have come (pcTurn)
	}
	if changed {
		l.checkGame(r)
		l.change()
	}
}

// pcWhose returns the seat of the player whose game is at the address of the seat at, going by name, the name the
// game plays under. Several players can be behind one address. A game from that address got the first of their seats
// that was free, whichever of them started it (pcSeat). The game plays under the name that its player's page gave it
// (pcView), and no one else in the room's game has that name. When the name is that of another player at the same
// address, the two seats swap their addresses from now on, and what the server knows of the clients at them. Each
// player then has the address of their own game, and with it what the game reports about them. pcWhose returns at
// when the name is its player's or no such player's. The caller holds l.mu.
func (rg *roomGame) pcWhose(code string, at *seat, name string) *seat {
	if at.left || name == "" || strings.EqualFold(at.player.name, name) {
		return at
	}
	j := slices.IndexFunc(rg.seats, func(s *seat) bool {
		return s != at && !s.left && s.player.addr == at.player.addr && strings.EqualFold(s.player.name, name)
	})
	if j < 0 {
		return at
	}
	whose := rg.seats[j]
	log.Printf("server: the game at %s in room %s is %s's, by its name, not %s's, whose address it comes from too",
		at.local, code, whose.player.name, at.player.name)
	at.local, whose.local = whose.local, at.local
	at.in, whose.in = whose.in, at.in
	at.slot, whose.slot = whose.slot, at.slot
	at.knocked, whose.knocked = whose.knocked, at.knocked
	return whose
}

// pcSaid reads what a PC room's game prints: the lines of the server's script in it (t6Script), and one line of the
// game itself. The caller does not hold l.mu.
//
// Round 1 has begun once the script reports a round, whatever the status has listed of the players. The game decides
// when round 1 begins: with everyone in, or without the rest once Plutonium's own script has waited long enough for
// them (scripts/zm/ranked.gsc). The game's clock starts at that report, also when the status had everyone in before
// it (roundBegins). A game that has begun is not called off. Each round sets its players' records, as in a T5 game
// (keepRounds). The records come from the players' lines of the scoreboard. The script prints those by client slot,
// so they count only for the clients that the status has listed by then. The end of the map's main quest sets the
// easter egg's record of the players in the game then, whom the script names by slot too (pcEgg). What a player has
// from earlier games, which the script reports as well, is an event of the game (t6Carried), and so is each GobbleGum
// of a player's pack, and each one they chew, in a T7 game (t7Gum).
//
// The game ends when the level ends: at game over, or 20 seconds after its last player left. At game over the room
// gets the game's result, which the players' pages show while the players watch the game's end. A game of Grief or
// Turned has a winner then (result), and its rounds are no records (keepRounds). Once the script
// reports that the end has played out, the server stops the game. That is a moment before the game would load the map
// again for its players, who are to be back in their room instead. The game itself prints "map_rotate..." when it
// rotates to its next map, which is the same map. That line ends the game too, for when the script did not report the
// end. The level also ends before round 1: when players come and go while they wait for the rest, and when the game
// starts. The room's game carries on then.
func (l *lobby) pcSaid(r *room, rg *roomGame, line string) {
	said, ours := strings.CutPrefix(line, "zombies_lobby: ")
	if !ours && line != "map_rotate..." {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.game != rg {
		return
	}
	verb, arg, _ := strings.Cut(said, " ")
	if ours && verb == "level" {
		// the level's scripts run: the game has started, and answers its console from here on (rconWatch)
		rg.g.leveled.Store(time.Now().UnixNano())
		return
	}
	if ours && verb == "stats" {
		// a player's line of the scoreboard, printed before "round" and "over". No page shows it yet.
		if fields := strings.Fields(arg); len(fields) > 0 {
			slot, err := strconv.Atoi(fields[0])
			for _, s := range rg.seats {
				if err == nil && slot >= 0 && s.slot == slot {
					rg.scored(s.local.String(), fields[1:])
				}
			}
		}
		return
	}
	if ours && verb == "round" && !rg.started && !rg.up.IsZero() && rg.ending == "" {
		rg.started, rg.round1 = true, time.Now()
		log.Printf("server: room %s's game has begun", r.code)
		l.checkGame(r)
		l.change()
	}
	if !rg.started {
		rg.stats = nil // the level ended while its players waited, so the lines it printed belong to no game
		return
	}
	over := "Game over."
	if !slices.ContainsFunc(rg.seats, func(s *seat) bool { return s.in }) {
		over = "Everyone left the game."
	}
	switch {
	case ours && verb == "round":
		if round, _ := strconv.Atoi(arg); rg.ending == "" {
			rg.roundBegins(round)
			l.keepRounds(r, rg, l.result(r, rg, max(round, 0)))
		}
		return // the leaderboards are read when a page opens them
	case ours && verb == "egg":
		if rg.ending == "" && hasEgg(r.mapName) {
			l.pcEgg(r, rg, strings.Fields(arg))
		}
		return
	case ours && (verb == "help" || verb == "perk"):
		if rg.ending == "" {
			l.t6Carried(r, rg, verb, strings.Fields(arg))
		}
		return
	case ours && verb == "gum":
		if rg.ending == "" {
			l.t7Gum(r, rg, strings.Fields(arg))
		}
		return
	case ours && verb == "over" && rg.ending == "" && over == "Game over.":
		// The players watch the game's end. The script reports when the end has played out ("exit"). If it doesn't, the
		// server stops the game anyway after pcEndTakes. The script reports the round, and in Grief the team that won.
		// Turned counts no rounds: its round is none.
		round, won := 0, 0
		fmt.Sscan(arg, &round, &won) // "over 12", or in Grief "over 12 2"
		versus := versusOf(r.mapName)
		if versus == turned {
			round = 0
		}
		if versus == grief && (won == 1 || won == 2) {
			rg.won = won
		}
		res := l.result(r, rg, max(round, 0))
		rg.ending = gameOverText(res)
		// The result is what the page shows from now on. It is kept only when the script printed the players' lines.
		// Otherwise the room shows only that it was game over.
		if len(res.Players) > 0 {
			rg.result = res
			l.keepRounds(r, rg, res)
		}
		log.Printf("server: room %s's game is over", r.code)
		time.AfterFunc(pcEndTakes, rg.cancel)
	case ours && verb != "over" && verb != "exit":
		return
	default: // the end has played out, or no one is left to watch it, or the game rotates its map
		l.stop(r, over)
		l.quit(rg) // for a game that was over already, which stop leaves as it is
	}
	l.change()
}

// seatIn returns the seat of rg whose client is in the slot that the script names, or nil when none is.
func (rg *roomGame) seatIn(field string) *seat {
	slot, err := strconv.Atoi(field)
	for _, s := range rg.seats {
		if err == nil && slot >= 0 && s.slot == slot {
			return s
		}
	}
	return nil
}

// pcEgg keeps the easter egg of rg's map for the players whose client is in one of the slots: the script's report
// that the map's main quest is done ("egg 2 0 1"), with the side that the players took and the players in the game
// then. A T6 game's script reports it (t6.go: t6Script), and a T7 game's (t7.go: t7Script), whose quests have no
// sides. The whole party has the one time, from round 1, as in a T5 game (t5.go: gameLine). The game has an event of
// each of those players' easter egg, with the side (gameEvent). The caller holds l.mu.
func (l *lobby) pcEgg(r *room, rg *roomGame, fields []string) {
	if len(fields) == 0 {
		return
	}
	if rg.eggSeconds == 0 {
		rg.eggSeconds = max(int64(time.Since(rg.round1).Seconds()), 1)
	}
	side := map[string]string{"1": richtofen, "2": maxis}[fields[0]]
	for _, field := range fields[1:] {
		if s := rg.seatIn(field); s != nil {
			s.player.keepEgg(r.mapName, len(rg.seats), rg.eggSeconds, gameID(r, rg))
			rg.note(rg.eggSeconds, eventEgg, s.player, side)
		}
	}
	l.keepEvents(rg) // so that the game has a report however it ends, with the easter egg in it
	l.save()
}

// pcEndTakes is how long the server gives a PC game's end to play out before it stops the game anyway. The end takes
// 20 seconds.
const pcEndTakes = time.Minute

// pcSeat returns where the relay sends a PC client that reached it from ip. verb is the first word of the client's
// first packet. The client goes to a seat in a PC room's game: game is the game's address, and local is the seat's
// address, which the game sees the client at (relay.go). A PC client can't identify its player the way a T5 client
// does with the player's key, so the server goes by the client's address. It picks the seat of a player whose page
// calls from ip (player.addr), in a game that accepts players.
//
// Several players can call from ip (a household behind one address). pcSeat then picks a seat that has no client. If
// each seat has one, it picks the seat whose client has been quiet the longest, for at least pcRequiet: that client's
// player has started their game again. Players of one room are told apart later, by the name a client plays under:
// in its connect (pcTurn), and once the room's game lists it (pcStatus). Both are clients of the one game.
//
// Players of different rooms can call from ip too, and nothing in a client's packets says which room's game it is
// to join. A client that got a seat in the wrong room's game would ask that game to let it in with the other room's
// password. So the server has the players' launchers send their games one room after the other (pcWaits), and of
// the seats that have no client, pcSeat picks the one whose player's launcher was sent the game last. A player who
// joins by hand, with the console line of their room's page, has no such order: with a player of another room at
// their address who joins in the same moments, their game can get the other's seat, and then neither joins until
// they try again.
//
// ok is false when there is no such seat. While a PC game accepts players, the server then logs the address that
// asked. This is how a player's game fails to join when it reaches the server from another address than the player's
// page.
func (l *lobby) pcSeat(ip netip.Addr, verb string) (game, local *net.UDPAddr, name, who string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var best *seat
	var room *room
	bestQuiet := time.Duration(-1)
	running := ""
	for _, r := range l.rooms {
		rg := r.game
		if !ownPC(rg) || rg.ending != "" || rg.up.IsZero() {
			continue
		}
		running = r.code
		for _, s := range rg.seats {
			if s.left || !s.player.addr.IsValid() || s.player.addr != ip {
				continue
			}
			has, quiet := l.relay.routeQuiet(s.local)
			switch {
			case !has:
				quiet = time.Duration(1 << 62)
			case quiet < pcRequiet:
				continue
			}
			if quiet > bestQuiet || quiet == bestQuiet && s.sent.After(best.sent) {
				best, room, bestQuiet = s, r, quiet
			}
		}
	}
	if best == nil {
		if running != "" && time.Since(l.t6().knocked[ip]) > time.Minute {
			if len(l.t6().knocked) > 1000 {
				clear(l.t6().knocked)
			}
			l.t6().knocked[ip] = time.Now()
			log.Printf("server: a game at %s asks to join (%.16q), where no page of room %s's players is, or their games are",
				ip, verb, running)
		}
		return nil, nil, "", "", false
	}
	if best.knocked.IsZero() {
		best.knocked = time.Now()
	}
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: room.game.port}, best.local, best.player.name,
		fmt.Sprintf("%s, room %s", best.player.name, room.code), true
}

// pcWaits reports whether p's launcher is to wait before it has p's game join their room's PC game (launcherPlay).
// It waits while the game of another player at p's address is on its way into another room's PC game: whose is that
// player and their room, for the log. The relay then has one game to expect from the address, and gives it the seat
// of the player it belongs to (pcSeat).
//
// A player's game is on its way from when their launcher was sent their room's game until a game from their address
// has a seat in it. It can get no seat before the room's game is up. From then, or from when the launcher was sent
// the game if that was later, it has pcWay to come. A game that takes longer is waited for no more, and p's game may
// then get its seat. Nor is a game waited for while its launcher is still getting its PC ready, which can take any
// time (preparing). A launcher that waits is sent no game, so the wait goes one way: of two players at one address,
// the one whose launcher was sent their game first has their game join first. The caller holds l.mu.
func (l *lobby) pcWaits(p *player) (waits bool, whose string) {
	if !p.addr.IsValid() {
		return false, ""
	}
	for _, r := range l.rooms {
		rg := r.game
		if r == p.room || !ownPC(rg) || rg.ending != "" {
			continue
		}
		for _, s := range rg.seats {
			if s.player.addr != p.addr || s.left || s.sent.IsZero() || !s.knocked.IsZero() || l.preparing(s.player) {
				continue
			}
			since := s.sent
			if rg.up.After(since) {
				since = rg.up
			}
			if rg.up.IsZero() || time.Since(since) < l.pcWay {
				return true, fmt.Sprintf("%s, room %s", s.player.name, r.code)
			}
		}
	}
	return false, ""
}

// pcRequiet is how long a PC client must have sent nothing before another client from its address can take its seat.
const pcRequiet = 3 * time.Second

// pcTurn reports whether the PC client at a seat's address may join its room's game now. The relay asks for each
// connect of a client that the game has not accepted, and again while the connect waits (relay.holds, relay.letIn).
// name is the name the client plays under, from its connect, or "". The client's seat is its player's by that name,
// when several players are behind one address (pcWhose). who is that player and their room, for the log, or "" when
// the address is no seat's.
//
// The players join in the order of their places in the room, which is the order of the game's seats, so that each
// has their room's colour in the game (the comment at the top). A client may join once no player before its own is
// awaited (pcAwaited). Once round 1 has begun, a client joins at once: the game has given its slots, and a player
// who comes back to it is not kept out of it for a player who may not come back.
func (l *lobby) pcTurn(local *net.UDPAddr, name string) (turn bool, who string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := local.String()
	for _, r := range l.rooms {
		rg := r.game
		if !ownPC(rg) {
			continue
		}
		i := slices.IndexFunc(rg.seats, func(s *seat) bool { return s.local.String() == at })
		if i < 0 {
			continue
		}
		if rg.started || rg.ending != "" {
			return true, fmt.Sprintf("%s, room %s", rg.seats[i].player.name, r.code)
		}
		whose := rg.pcWhose(r.code, rg.seats[i], name)
		if whose != rg.seats[i] {
			l.change() // the two seats' players are in the game or out of it as each other were
		}
		who = fmt.Sprintf("%s, room %s", whose.player.name, r.code)
		if whose.left || whose.slot >= 0 {
			return true, who // the relay lets a player who left in no more, and the game has this client already
		}
		for _, s := range rg.seats {
			if s == whose {
				return true, who
			}
			if l.pcAwaited(s) {
				return false, who
			}
		}
	}
	return true, "" // the seat's game has ended
}

// pcAwaited reports whether the players after s in the room wait to join their PC game until s has joined it. They
// do not wait for a player who is in the game or has left it. Nor do they wait for one whose launcher is still
// getting their PC ready, which can take any time (preparing).
//
// For anyone else, it depends on where their game is (relay.door):
//   - It does not ask to connect (relay.asks). It is yet to come, and the others wait for it for joinWait, from when
//     the first of them began to wait. A game on a slower PC, or one that its player joins by hand, comes within
//     that time or the others go first.
//   - It asks to connect, and waits for its own turn. The others wait for as long as it does. Its turn comes, because
//     the players it waits for are before it too.
//   - It asks, and the relay has passed its connect on. The game takes it within a moment, or else does not take it
//     at all (a wrong password), and the others then wait no longer.
//
// The caller holds l.mu.
func (l *lobby) pcAwaited(s *seat) bool {
	if s.left || s.slot >= 0 || l.preparing(s.player) {
		return false
	}
	taken, asking, coming := l.relay.door(s.local)
	if taken {
		return false
	}
	now := time.Now()
	if s.awaited.IsZero() {
		s.awaited = now
	}
	if !asking {
		return now.Sub(s.awaited) < l.joinWait
	}
	return coming
}

// pcLate calls off a PC room's game that has waited loadTimeout for its players, when not everyone with a seat is
// in, as for a T5 game. The wait counts from when the game was up. It starts again at the last moment that the
// launcher of a missing player was still getting what their PC lacks (preparing). A first download of the game files
// takes far longer than any wait, and the player's game then has the whole wait to start and load in. Meanwhile the
// game begins without them after its own wait (t6Prepare), and they can join at a later round.
func (l *lobby) pcLate(r *room, rg *roomGame) {
	if rg.up.IsZero() {
		return
	}
	var out []*seat
	for _, s := range rg.seats {
		if !s.in && !s.left {
			if l.preparing(s.player) {
				rg.held = time.Now()
				return
			}
			out = append(out, s)
		}
	}
	since := rg.up
	if rg.held.After(since) {
		since = rg.held
	}
	if time.Since(since) < l.loadTimeout {
		return
	}
	if len(out) > 0 {
		l.stop(r, fmt.Sprintf("Game called off: %s didn't join in time.", names(out)))
	}
}

// pageAddr returns the address a player's page calls from. That is the request's address. Behind a reverse proxy,
// which is at a private address or the server's own, it is the address the proxy reports: the last one in
// X-Forwarded-For. The player's PC game reaches the relay from the same address (pcSeat), because the page is open on
// the PC they play on.
func pageAddr(req *http.Request) netip.Addr {
	from, err := netip.ParseAddrPort(req.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr := from.Addr().Unmap()
	if addr.IsLoopback() || addr.IsPrivate() {
		if said := req.Header.Values("X-Forwarded-For"); len(said) > 0 {
			list := strings.Split(said[len(said)-1], ",")
			if a, err := netip.ParseAddr(strings.TrimSpace(list[len(list)-1])); err == nil {
				return a.Unmap()
			}
		}
	}
	return addr
}
