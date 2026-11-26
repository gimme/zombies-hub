// Command standin stands in for the games in the page tests (e2e/). The server runs it as its wine command (-wine),
// as "standin <program> <args...>", where the program is what the server runs under Wine: BO1Zombies.exe for a Black
// Ops room's game (server/t5.go), or Plutonium's bootstrapper with t6zm for a Black Ops II room's game
// (server/t6.go). It plays the game's part towards the server and the players' clients: it answers the server's
// checks, says who is in the game, and reports rounds and game over when a test asks for them on the server's
// console (zombies-server's stdin: "<room code> <line>").
//
// It prints its command line first ("stand-in: args ...").
//
// Black Ops: the console is stdin and stdout. The players' seats are in main/lobby.cfg in the game's home
// (bo1_lobby_seat). Its answer to a getchallenge names its build, the one in the engine's engine.json, as the engine's
// does. A player is in once a connect comes from their seat's address, and round 1 begins once everyone is in. A
// client that sends "disconnect" quit the game, and its character waits. Console lines:
//
//	round <n>               each player's line of the scoreboard, then the round
//	over <n> [s]            the same for game over; then the end scoreboard shows for s seconds (half a second
//	                        without), each client still in the game hears "standin over <n>", and the game quits
//	quit                    quits, and each client hears "standin quit" first, as from a server that quits
//	bo1_lobby_drop <addr>   the server's: the player left the room, and their character goes
//	exit <code>             ends with that exit code, as a game that fails does
//
// Black Ops II: the console is the remote console, with the password in storage/t6/lobby.cfg. A client joins with an
// out-of-band "connect <name>". Remote console lines: status, round <n>, egg (the map's main quest done, with every
// client in the game), over <n> (the end plays out for a moment, then "exit"), clientkick <slot>, quit.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const oob = "\xff\xff\xff\xff"

// quit ends the game as its quit command does. A Black Ops game tells its clients first (runT5).
var quit = func() { os.Exit(0) }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: standin <program> <args...>")
		os.Exit(1)
	}
	// The server stops a game with SIGINT, which a game takes as its quit command.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		fmt.Println("stand-in: quit")
		quit()
	}()
	args := os.Args[2:]
	fmt.Println("stand-in: args " + strings.Join(args, " "))
	conn, err := net.ListenPacket("udp4", "127.0.0.1:"+arg(args, "net_port"))
	if err != nil {
		fmt.Println("stand-in:", err)
		os.Exit(1)
	}
	if slices.Contains(args, "t6zm") {
		runT6(conn)
	} else {
		runT5(conn, args, build(os.Args[1]))
	}
}

// arg returns the value of a setting on the game's command line ("+set net_port 3076").
func arg(args []string, name string) string {
	for i, a := range args[:max(len(args)-1, 0)] {
		if a == name {
			return args[i+1]
		}
	}
	return ""
}

// build returns the build ID in the engine's engine.json, next to the folder of BO1Zombies.exe (internal/bo1zombies).
func build(exe string) string {
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "engine.json"))
	if m := regexp.MustCompile(`"build":\s*"([^"]*)"`).FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return "unknown"
}

// stats returns the line of the scoreboard of the player in place i at round n: score, cash, kills, headshots, downs,
// revives and doors bought.
func stats(i, n int) string {
	return fmt.Sprintf("%d %d %d %d %d %d %d", 1000*(i+1)+100*n, 500*(i+1), 10*n+i, n, i, 1, 2)
}

// ---- Black Ops ----

type t5Game struct {
	conn    net.PacketConn
	mu      sync.Mutex
	seats   []string // the seats' addresses, in their places
	in      map[string]bool
	started bool
}

func runT5(conn net.PacketConn, args []string, build string) {
	g := &t5Game{conn: conn, in: map[string]bool{}}
	home := strings.ReplaceAll(strings.TrimPrefix(arg(args, "fs_h"), "Z:"), `\`, "/")
	cfg, _ := os.ReadFile(filepath.Join(home, "main", "lobby.cfg"))
	for _, m := range regexp.MustCompile(`(?m)^bo1_lobby_seat (\S+) \d+$`).FindAllStringSubmatch(string(cfg), -1) {
		g.seats = append(g.seats, m[1])
	}
	fmt.Printf("stand-in: Black Ops on %s for %d players\n", conn.LocalAddr(), len(g.seats))
	quit = func() { g.end("standin quit") }
	go g.console()
	buf := make([]byte, 0x10000)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			os.Exit(1)
		}
		text, ok := strings.CutPrefix(string(buf[:n]), oob)
		switch {
		case ok && strings.HasPrefix(text, "getchallenge"):
			conn.WriteTo([]byte(oob+"challengeResponse 1 "+build), from)
		case ok && strings.HasPrefix(text, "connect "):
			// then a netchan packet, as the game's gamestate follows: the relay takes the client as the game's then
			conn.WriteTo([]byte(oob+"connectResponse"), from)
			conn.WriteTo([]byte("gamestate"), from)
			name := regexp.MustCompile(`\\name\\([^\\"]*)`).FindStringSubmatch(text)
			if name != nil {
				fmt.Printf("stand-in: %s connected as %s\n", from, name[1])
			}
			g.enter(from.String())
		case !ok && text == "disconnect":
			g.say("quit " + from.String())
		}
	}
}

func (g *t5Game) say(line string) { fmt.Println("bo1_lobby: " + line) }

// tell sends each client in the game an out-of-band packet. The caller holds g.mu.
func (g *t5Game) tell(text string) {
	for seat := range g.in {
		addr, _ := net.ResolveUDPAddr("udp4", seat)
		g.conn.WriteTo([]byte(oob+text), addr)
	}
}

// end tells each client in the game how it ended, as the engine's server does as it quits, and quits.
func (g *t5Game) end(text string) {
	g.mu.Lock()
	g.tell(text)
	g.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	os.Exit(0)
}

// enter lets the client at addr in, when addr is a seat's. Round 1 begins once every seat's client is in.
func (g *t5Game) enter(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.Contains(g.seats, addr) {
		return
	}
	if !g.in[addr] {
		g.in[addr] = true
		g.say("in " + addr)
	}
	if !g.started && len(g.in) == len(g.seats) {
		g.started = true
		g.say("start")
	}
}

func (g *t5Game) console() {
	for in := bufio.NewScanner(os.Stdin); in.Scan(); {
		verb, rest, _ := strings.Cut(strings.TrimSpace(in.Text()), " ")
		n, _ := strconv.Atoi(rest)
		g.mu.Lock()
		switch verb {
		case "round", "over":
			round, _, _ := strings.Cut(rest, " ")
			n, _ = strconv.Atoi(round)
			for i, seat := range g.seats {
				if g.in[seat] {
					g.say("stats " + seat + " " + stats(i, n))
				}
			}
			g.say(verb + " " + round)
			if verb == "over" {
				// the end scoreboard, then the level's end, which the clients still in the game hear
				shown, _ := strconv.ParseFloat(strings.TrimPrefix(rest, round+" "), 64)
				time.AfterFunc(time.Duration(max(shown, 0.5)*float64(time.Second)), func() {
					g.end("standin over " + round)
				})
			}
		case "bo1_lobby_drop":
			if g.in[rest] {
				delete(g.in, rest)
				g.say("out " + rest)
			}
		case "exit":
			os.Exit(n)
		case "quit":
			go quit()
		default:
			fmt.Printf("stand-in: unknown command %q\n", in.Text())
		}
		g.mu.Unlock()
	}
}

// ---- Black Ops II ----

type t6Client struct{ address, name string }

func runT6(conn net.PacketConn) {
	// Plutonium's server prints in colour, after its prompt.
	say := func(text string) { fmt.Printf("\x1b[38;2;243;111;26mPlutonium r1 > \x1b[38;2;255;255;255m%s\n", text) }
	cfg, err := os.ReadFile(filepath.Join("storage", "t6", "lobby.cfg"))
	found := regexp.MustCompile(`rcon_password "(\w+)"`).FindSubmatch(cfg)
	if err != nil || found == nil {
		fmt.Println("stand-in: no remote console password in storage/t6/lobby.cfg:", err)
		os.Exit(1)
	}
	password := string(found[1])
	say(fmt.Sprintf("stand-in: Black Ops II on %s", conn.LocalAddr()))
	say("map_rotate...")
	var clients []t6Client // by slot; a free slot has no address
	lines := func(n int) {
		for slot, c := range clients {
			if c.address != "" {
				say(fmt.Sprintf("zombies_lobby: stats %d %s", slot, stats(slot, n)))
			}
		}
	}
	buf := make([]byte, 0x10000)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			os.Exit(1)
		}
		text, ok := strings.CutPrefix(string(buf[:n]), oob)
		reply := func(text string) { conn.WriteTo([]byte(oob+text), from) }
		if name, join := strings.CutPrefix(text, "connect "); ok && join {
			slot := slices.IndexFunc(clients, func(c t6Client) bool { return c.address == from.String() })
			if slot < 0 {
				slot = slices.IndexFunc(clients, func(c t6Client) bool { return c.address == "" })
			}
			if slot < 0 {
				slot = len(clients)
				clients = append(clients, t6Client{})
			}
			clients[slot] = t6Client{from.String(), name}
			say(fmt.Sprintf("stand-in: %s connected as %s", from, name))
			reply("connectResponse")
			continue
		}
		rest, rcon := strings.CutPrefix(text, "rcon "+password+" ")
		if !ok || !rcon {
			continue
		}
		verb, value, _ := strings.Cut(rest, " ")
		number, _ := strconv.Atoi(value)
		switch verb {
		case "status":
			status := "print\nmap: zm_transit\n" +
				"num score bot ping guid                             name             lastmsg address               qport  rate\n" +
				"--- ----- --- ---- -------------------------------- ---------------- ------- --------------------- ------ -----\n"
			for slot, c := range clients {
				if c.address != "" {
					status += fmt.Sprintf("%3d %5d %3d %4d %-32s %-16s %7d %-21s %6d %5d\n", slot, 500, 0, 30, "9240",
						c.name, 0, c.address, -1234, 25000)
				}
			}
			reply(status)
		case "round":
			lines(number)
			say("zombies_lobby: round " + value)
		case "egg": // the map's main quest is done, with every client in the game: a quest with no sides (0)
			line := "zombies_lobby: egg 0"
			for slot, c := range clients {
				if c.address != "" {
					line += " " + strconv.Itoa(slot)
				}
			}
			say(line)
		case "over":
			lines(number)
			say("zombies_lobby: over " + value)
			time.AfterFunc(time.Second, func() { say("zombies_lobby: exit") })
		case "clientkick":
			if number >= 0 && number < len(clients) {
				clients[number] = t6Client{}
			}
		case "quit":
			say("quitting")
			os.Exit(0)
		default:
			reply("print\nUnknown command \"" + verb + "\"\n")
		}
	}
}
