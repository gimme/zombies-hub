package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zombies/internal/boiii"
)

// Black Ops III plays on BOIII (https://github.com/Ezz-lol/boiii-free): one program, which loads the game's own
// program from the game's folder and runs it. The launcher keeps BOIII's program in a folder of its own, and starts it
// in the game's folder, past BOIII's own launcher window (-launch). BOIII then brings itself and its data up to date
// from its updater, as it does for a player who starts it by hand, and keeps the data among its user's application
// data (%LOCALAPPDATA%\boiii). So the launcher downloads the program once, on a PC that has none (haveBoiii).
//
// A game on BOIII plays under the name in a file of BOIII's in the game's folder (boiii_players/properties.json), and
// takes none on its command line. The launcher writes the name that the room's game knows the player by there before
// it starts the game, and puts the player's own back once the game is closed (boiiiName).
//
// Nothing on BOIII's command line joins a game safely. A password set there is replaced as the game reads its saved
// settings, and a connect there is made the moment the game's menu loads, where the game then crashed as it loaded
// the map. What joins is what a player does by hand: the line "password <password>;connect <host>:<port>", typed into
// the game's console once the game is at its menu. BOIII has a console window of its own beside the game's, and the
// launcher types into that (process.join, on Windows). A line typed before the game runs its menu is lost, or runs
// before the game is ready for it. So the launcher asks the game whether it is there first: a game on BOIII answers
// getInfo on its own UDP port, as a server does, from when its menu runs, and says the map it is on (probeBoiii). The
// launcher gives the game a port of its own for that (net_port). It types the line once the game has answered for a
// moment, and again when the game is still at its menu a while later: the room's game may not have been up.

// boiiiMenu is the map that a game on BOIII says it is on while it is at its menu.
const boiiiMenu = "core_frontend"

// errNoSteam is the error for a PC without Steam, where BOIII starts no game.
var errNoSteam = errors.New("Black Ops III needs Steam on this PC: BOIII, which runs the game, starts only where Steam " +
	"is installed. Install Steam (it needs no account for this), then try again.")

// haveBoiii makes sure that the PC has what starts Black Ops III: Steam, which BOIII asks for whoever owns the game,
// and BOIII's program, in the launcher's folder. A PC that has the program is left as it is, and the launcher
// contacts no one: BOIII brings itself up to date as it starts.
func (la *launcher) haveBoiii(ctx context.Context, srv *server) error {
	if la.mac != "" {
		return errors.New("Black Ops III doesn't run on a Mac.")
	}
	if la.steam != nil && !la.steam() {
		return errNoSteam
	}
	if isFile(filepath.Join(la.boiii, boiii.Exe)) {
		return nil
	}
	la.doing(ctx, srv, "Getting BOIII")
	c := &boiii.Copy{Dir: la.boiii, URL: la.boiiiManifest, Client: la.client, State: "zombies-launcher-files.json",
		Program: true,
		Fetching: func(files int, bytes int64) {
			la.say("Getting BOIII from its own servers: %.0f MB...", float64(bytes)/1e6)
		}}
	if err := c.Update(ctx); err != nil {
		return fmt.Errorf("%v\n\nIf your antivirus removed the file, add an exception for BOIII's folder:\n  %s",
			sentence(err), la.boiii)
	}
	return nil
}

// boiiiName returns the name that a game on BOIII plays under, for the name that the server knows the player by:
// at most 16 characters of those that the server leaves in a name, which BOIII takes too.
func boiiiName(name string) string {
	name = gameName(name)
	if len(name) > 16 {
		name = strings.TrimSpace(name[:16])
	}
	return name
}

// setBoiiiName writes the name that the game plays under into BOIII's settings in the game folder dir
// (boiii_players/properties.json: one JSON object, which BOIII writes with a byte order mark). Each other member of
// the file stays as it is. It returns what puts the name back that the file had: the player's own, in a copy of the
// game that they also start by hand.
func setBoiiiName(dir, name string) (restore func(), err error) {
	file := filepath.Join(dir, "boiii_players", "properties.json")
	set := func(value json.RawMessage) error {
		members := map[string]json.RawMessage{}
		switch data, err := os.ReadFile(file); {
		case err == nil:
			if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &members) != nil || members == nil {
				members = map[string]json.RawMessage{} // BOIII starts over from a file that it can't read, too
			}
		case !os.IsNotExist(err):
			return err
		}
		if value == nil {
			delete(members, "playerName")
		} else {
			members["playerName"] = value
		}
		data, err := json.Marshal(members)
		if err != nil {
			return err
		}
		return writeFile(file, data)
	}
	var had json.RawMessage
	if data, err := os.ReadFile(file); err == nil {
		members := map[string]json.RawMessage{}
		if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &members) == nil {
			had = members["playerName"]
		}
	}
	value, _ := json.Marshal(name)
	if err := set(value); err != nil {
		return func() {}, err
	}
	return func() { set(had) }, nil
}

// freeUDPPort returns a UDP port that nothing on the PC has now, for the game to take as its own.
func freeUDPPort() (int, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port, nil
}

// probeBoiii asks the game on the PC's UDP port for its info, as a game's server browser asks a server (getInfo), and
// returns the map that the game says it is on. ok is false while the game doesn't answer: it is starting, or it has
// been closed.
func probeBoiii(port int) (mapName string, ok bool) {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		return "", false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(400 * time.Millisecond))
	if _, err := conn.Write([]byte("\xff\xff\xff\xffgetInfo zombies")); err != nil {
		return "", false
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return "", false
	}
	// "infoResponse", a line break, and then the info: \key\value\key\value
	word, said, _ := bytes.Cut(bytes.TrimPrefix(buf[:n], []byte("\xff\xff\xff\xff")), []byte("\n"))
	if string(bytes.TrimSpace(word)) != "infoResponse" {
		return "", false
	}
	fields := strings.Split(string(said), `\`)
	for i := 1; i+1 < len(fields); i += 2 {
		if fields[i] == "mapname" {
			return fields[i+1], true
		}
	}
	return "", true
}

// startBoiii starts the player's Black Ops III on the game files in dir, at its menu, to play under name. It returns
// the game, which joins a room's game when a line is typed into its console (boiiiGame.join).
func (la *launcher) startBoiii(dir, name string) (game, error) {
	dir = strings.TrimRight(dir, `\/`)
	exe := filepath.Join(la.boiii, boiii.Exe)
	if strings.ContainsAny(exe+dir, "\"\r\n") || dir == "" {
		return nil, fmt.Errorf("The game can't be started from the folder %q.", dir)
	}
	restore, err := setBoiiiName(dir, boiiiName(name))
	if err != nil {
		la.say("The game plays under the name it has: its settings couldn't be written (%v).", err)
	}
	port, err := freeUDPPort()
	if err != nil {
		restore()
		return nil, fmt.Errorf("Black Ops III couldn't be started: no free UDP port for it (%v).", err)
	}
	g, err := la.start(exe, fmt.Sprintf(`"%s" -launch +set net_port %d`, exe, port), dir)
	if err != nil {
		restore()
		return nil, fmt.Errorf("BOIII couldn't be started: %v\n\nIf your antivirus removed the file, add an exception "+
			"for BOIII's folder:\n  %s", err, la.boiii)
	}
	b := &boiiiGame{game: g, port: port, probe: la.probe, tick: la.tick, settle: la.settle, retype: la.retype,
		restore: restore}
	if b.probe == nil {
		b.probe = probeBoiii
	}
	go b.watch()
	return b, nil
}

// boiiiGame is a game on BOIII that the launcher started: the program, and how a line reaches its console at a
// moment when the game takes it (the comment at the top).
type boiiiGame struct {
	game      // the program: join types a line into its console window at once
	port  int // the game's own UDP port, where it answers once its menu runs
	probe func(port int) (mapName string, ok bool)
	tick  time.Duration // between two looks at the game
	// settle is how long the game has to have answered before a line is typed, and retype how long the game may stay
	// at its menu after a line that was to take it into a room's game, before the line is typed again.
	settle, retype time.Duration
	restore        func() // puts the player's own name back in BOIII's settings, once the game is closed

	mu      sync.Mutex
	line    string // the line to type, or "" when there is none to type
	tries   int    // how often it has been typed
	typed   time.Time
	stopped bool
	once    sync.Once // restore runs once
}

// boiiiTries is how often a line is typed at most: once, and twice more for a game that stayed at its menu.
const boiiiTries = 3

// join has the line typed into the game's console once the game takes it. It returns at once: the game may still be
// starting. A line that is yet to be typed is replaced.
func (b *boiiiGame) join(line string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.line, b.tries, b.typed = line, 0, time.Time{}
	return nil
}

// watch types the game's line when the game takes it, for as long as the game runs. The game takes a line once it
// has answered on its port for settle: its menu runs then, or a game that it is in. After a line that was to take it
// into a room's game, a game that is still at its menu again later has not joined, so the line is typed again. watch
// puts the player's name back once the game is closed.
func (b *boiiiGame) watch() {
	tick, settle, retype := b.tick, b.settle, b.retype
	if tick <= 0 {
		tick = time.Second
	}
	if settle <= 0 {
		settle = 4 * time.Second
	}
	if retype <= 0 {
		retype = 20 * time.Second
	}
	var since time.Time // when the game began to answer
	for b.game.running() {
		time.Sleep(tick)
		mapName, ok := b.probe(b.port)
		if !ok {
			since = time.Time{}
			continue
		}
		if since.IsZero() {
			since = time.Now()
		}
		b.mu.Lock()
		line, due := b.line, false
		switch {
		case line == "" || b.stopped || time.Since(since) < settle:
		case b.tries == 0:
			due = true
		case mapName != boiiiMenu && time.Since(b.typed) >= settle:
			b.line = "" // the game is on its way into a map: the line did its work
		case mapName == boiiiMenu && time.Since(b.typed) >= retype && b.tries < boiiiTries:
			due = true
		}
		if due {
			b.tries, b.typed = b.tries+1, time.Now()
		}
		b.mu.Unlock()
		if due {
			b.game.join(line)
		}
	}
	b.once.Do(b.restore)
}

// stop closes the game: with the quit command of its console, which lets the game save its settings, and as any
// program is closed when that has not closed it.
func (b *boiiiGame) stop() {
	b.mu.Lock()
	b.line, b.stopped = "", true
	b.mu.Unlock()
	if _, ok := b.probe(b.port); ok && b.game.join("quit") == nil {
		step := max(b.tick, 10*time.Millisecond) / 10 // 8 seconds in all, for a launcher that looks each second
		for i := 0; i < 80 && b.game.running(); i++ {
			time.Sleep(step)
		}
	}
	b.game.stop()
	b.once.Do(b.restore)
}
