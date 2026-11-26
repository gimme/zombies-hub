package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zombies/internal/boiii"
)

// Black Ops III (t7) is played on BOIII (https://github.com/Ezz-lol/boiii-free), as Black Ops II is on Plutonium, and
// the server integrates it as lightly. The game of a room on a T7 map is Steam's dedicated server of the game, the
// Unranked Dedicated Server, which BOIII loads and runs (internal/boiii). The server starts it like any other game
// (game.go: as the games' user, on a port that only the server reaches), and sets it up and reads who is in it over
// its remote console, as it does a T6 game (pc.go: rconWatch). Each player's own PC runs BOIII on their own copy of
// Black Ops III, and their game joins the room's game with the room's password. The server knows a player's game by
// the address it comes from, relays it to the room's game (relay.go: a PC client's route), and keeps the room's game
// as it keeps a T6 room's: with the code that the two share (pc.go).
//
// The game files are two folders (main.go: titleDir). t7 is the game's install folder, of which the server reads the
// fastfiles of Zombies alone: a dedicated server draws nothing and plays no sound, so it needs none of the game's
// 60 GB of pictures and 20 GB of sounds (t7Reads). t7-server is the install folder of Steam's server, which is a
// program of its own (the tool "Call of Duty: Black Ops III - Unranked Dedicated Server", which Steam gives to who has
// the game). It comes with the fastfiles of multiplayer and none of Zombies, so a game's fastfiles are of both
// folders.
//
// A game runs from its home, which is the game's folder as BOIII and the server see it (t7Prepare): links to the
// server's program and to the fastfiles, a link to BOIII's program, and the home's own config and script. BOIII's
// data is not in the game's folder. BOIII looks for it among its user's application data, so the games' user has a
// link there to the data of the server's copy (t7Data).
//
// The game writes its console to no stdout. Under Wine it opens a console of its own, which nothing shows. It also
// writes it to a log file in its home (logfile 2), and the server reads the game's output there (t7Tail).
//
// Steam's server wants one file more than its install has: the server's "fastfile of the day"
// (core_sv_ffotd_tu32_593.ff, 189 kB), which it downloads from the game's publisher each time it starts, and without
// which it never opens its lobby. The file is for one build of the game, so the server that has it for another build
// fetches again (t7Copy.lpc). The game files may have the file too, where Steam's server leaves it, and the server
// then fetches nothing (t7Copy.own). A game behind the image's firewall reaches no publisher (docker/entrypoint.sh). So
// the server keeps the file (t7Fetch): one run of the game fetches it, before the firewall is up, and every game
// after has it among its fastfiles and is told not to ask for it (live_useLPC 0). With the file in its home, a game
// sends nothing to anyone but its players.
//
// The server puts a script in the game that reports how the game goes (t7Script), in the lines of a T6 game's script.

const t7 = "t7"

// t7Server is the folder, among the game files, of Steam's dedicated server of Black Ops III (above), and t7Exe is
// the path of its program there.
const (
	t7Server = "t7-server"
	t7Exe    = "UnrankedServer/BlackOps3_UnrankedDedicatedServer.exe"
)

var t7MapList = []mapEntry{
	// The T7 maps are named after their zones. The game's own come first, in the order of their release, and then
	// those of Zombies Chronicles, in the order of the games they are from. The page has no pictures of them: a T7
	// map's pictures are in the game's image packs (.xpak), which the server neither has to have nor reads.
	{of: t7, name: "zm_zod", title: "Shadows of Evil"},
	{of: t7, name: "zm_factory", title: "The Giant"},
	{of: t7, name: "zm_castle", title: "Der Eisendrache"},
	{of: t7, name: "zm_island", title: "Zetsubou No Shima"},
	{of: t7, name: "zm_stalingrad", title: "Gorod Krovi"},
	{of: t7, name: "zm_genesis", title: "Revelations"},
	{of: t7, name: "zm_prototype", title: "Nacht der Untoten"},
	{of: t7, name: "zm_asylum", title: "Verrückt"},
	{of: t7, name: "zm_sumpf", title: "Shi No Numa"},
	{of: t7, name: "zm_theater", title: "Kino der Toten"},
	{of: t7, name: "zm_cosmodrome", title: "Ascension"},
	{of: t7, name: "zm_temple", title: "Shangri-La"},
	{of: t7, name: "zm_moon", title: "Moon"},
	{of: t7, name: "zm_tomb", title: "Origins"},
}

// t7Title is the title (title.go) for Black Ops III.
type t7Title struct {
	// newGame makes the game for a room on the given port. newGame is nil on a server without T7's game files, where
	// no room plays a T7 map.
	newGame func(code, mapName string, port, players int, password string) *game
	// copy is the server's copy of BOIII, which the games run. It is nil on a server without T7's game files.
	copy *t7Copy
}

// t7 returns the server's Black Ops III title.
func (l *lobby) t7() *t7Title {
	for _, t := range l.titles {
		if t, ok := t.(*t7Title); ok {
			return t
		}
	}
	return nil
}

func (*t7Title) id() string    { return t7 }
func (*t7Title) label() string { return "Black Ops III" }
func (*t7Title) inPage() bool  { return false }

func (t *t7Title) free(*lobby) error {
	switch {
	case t.newGame == nil:
		return errors.New("The server doesn't have Black Ops III.")
	case len(t.copy.fetched()) == 0:
		return errors.New("The server is still getting Black Ops III ready.")
	}
	return nil
}

// begin makes Steam's dedicated server that is r's game.
func (t *t7Title) begin(l *lobby, r *room, rg *roomGame) *game {
	g := t.newGame(r.code, r.mapName, rg.port, len(rg.seats), r.password)
	g.lines = func(line string) { l.pcSaid(r, rg, line) }
	g.t7().status = func(clients []pcClient) { l.pcStatus(r, rg, clients) }
	g.t7().changed = func() {
		l.mu.Lock()
		l.change()
		l.mu.Unlock()
	}
	return g
}

// done is called once rg's game has quit: it no longer runs from the server's copy of BOIII.
func (*t7Title) done(_ *lobby, rg *roomGame) {
	if rg.g != nil && rg.g.t7() != nil {
		rg.g.t7().leave()
	}
}

// drop removes a player who left the room from the game, as from a T6 game: it kicks their client by its slot and
// ends their route.
func (*t7Title) drop(l *lobby, r *room, s *seat) {
	if s.slot >= 0 {
		r.game.g.command("clientkick " + strconv.Itoa(s.slot))
	}
	l.relay.end(s.local)
}

func (*t7Title) late(l *lobby, r *room, rg *roomGame) { l.pcLate(r, rg) }

// lacks is false: nothing tells the server what a player's copy of Black Ops III has.
func (*t7Title) lacks(*lobby, *player) bool { return false }

func (*t7Title) showMember(*lobby, *room, *player, *memberView) {}

func (*t7Title) showRoom(l *lobby, r *room, p *player, rv *roomView) {
	rv.Password = r.password
	rv.Up = r.game != nil && !r.game.up.IsZero()
	// no one can join through a seat in a game that is ending
	if s := r.seatOf(p); s != nil && r.game.ending == "" && !s.left {
		rv.T7 = &pcView{Up: !r.game.up.IsZero(), Name: p.name}
		if rv.T7.Up {
			rv.T7.Password = r.game.g.t7().password
		}
	}
}

// t7 returns the game's engine when it is Steam's server on BOIII, or else nil.
func (g *game) t7() *t7Game {
	t, _ := g.engine.(*t7Game)
	return t
}

// t7Reads reports whether a T7 game reads the file name of a zone folder: of the game's own (server false), the
// fastfiles of Zombies, in any language; of Steam's server (server true), all but the multiplayer maps'. The scripts
// that copy the game files for Wine copy the same files (docker/host.sh: game_mounts).
func t7Reads(name string, server bool) bool {
	fastfile := strings.HasSuffix(name, ".ff") || strings.HasSuffix(name, ".fd")
	if server {
		return fastfile && !strings.Contains(name, "mp_")
	}
	return fastfile && strings.Contains(name, "zm_")
}

// t7Installed returns the T7 maps that the game files have: dir is a Black Ops III install, and server one of Steam's
// dedicated server of it. They have a map when dir has the map's zone. t7Installed returns no map when they lack a
// file that every map's game needs.
func t7Installed(dir, server string) []string {
	if !isFile(filepath.Join(server, filepath.FromSlash(t7Exe))) || !isFile(filepath.Join(dir, "zone", "zm_common.ff")) {
		return nil
	}
	var maps []string
	for _, m := range t7MapList {
		if isFile(filepath.Join(dir, "zone", m.name+".ff")) {
			maps = append(maps, m.name)
		}
	}
	return maps
}

// ---- BOIII, the server's copy ----

// t7Copy is the server's copy of BOIII (boiii.Copy, in -boiii-dir), as the T7 games share it, and with it the
// fastfiles that Steam's server fetched (t7Fetch), in the copy's folder lpc (lpc). Every game runs from the copy: its home
// links to the copy's program, and the games' user has a link to its data. So the copy changes only while no game
// runs from it, as the server's copy of Plutonium does (t6Copy).
type t7Copy struct {
	*boiii.Copy
	exe string // the program of Steam's server, where it is (t7Exe): the fetched fastfiles are for its build (lpc)

	mu   sync.Mutex   // held while a game comes to the copy (enter), so that one game at a time brings it up to date
	runs atomic.Int32 // the games that run from the copy now, each from its enter until its leave

	hashed sync.Once // sets build
	build  string    // names the build of Steam's server: the start of the hash of its program
}

// lpc returns the folder of the fastfiles that Steam's server fetched (t7Fetch). It is a folder of the copy's folder
// lpc that is named after the build of Steam's server, by the hash of its program. The game asks its publisher for the
// fastfile of its own build, and loads no other build's. So after an update of the game the folder is another one,
// which is empty, and the server fetches again (main.go: fetchT7Files).
func (c *t7Copy) lpc() string {
	c.hashed.Do(func() {
		c.build = "unknown"
		f, err := os.Open(c.exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha1.New()
		if _, err := io.Copy(h, f); err == nil {
			c.build = hex.EncodeToString(h.Sum(nil))[:12]
		}
	})
	return filepath.Join(c.Dir, "lpc", c.build)
}

// own returns the fastfiles from the game's publisher that the game files have, by their paths: those in the folder
// of Steam's server where the server leaves what it fetches as it runs from that folder (identities/<name>/LPC, as in
// a game's home: keep), or in a folder LPC there. A host who keeps them with the game files has a server that asks
// the publisher for nothing.
func (c *t7Copy) own() []string {
	dir := filepath.Dir(c.exe)
	files, _ := filepath.Glob(filepath.Join(dir, "identities", "*", "LPC", "*.ff"))
	more, _ := filepath.Glob(filepath.Join(dir, "LPC", "*.ff"))
	return append(files, more...)
}

// fetched returns the fastfiles from the game's publisher that the games load, by their paths: the game files' own
// (own), or else those that Steam's server fetched for the server (t7Fetch).
func (c *t7Copy) fetched() []string {
	if files := c.own(); len(files) > 0 {
		return files
	}
	files, _ := filepath.Glob(filepath.Join(c.lpc(), "*.ff"))
	return files
}

// enter brings the server's copy of BOIII up to date for the game, which is about to run from it (t7Prepare). It
// leaves the copy as it is when another game runs from it now. The game leaves the copy when it has quit (leave).
func (t *t7Game) enter(ctx context.Context) error {
	c := t.copy
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs.Load() == 0 {
		if err := c.Update(ctx); err != nil {
			return err
		}
	}
	c.runs.Add(1)
	t.entered.Store(true)
	return nil
}

// leave is called once the game has quit. It does nothing for a game that never came to the copy.
func (t *t7Game) leave() {
	if t.entered.Swap(false) {
		t.copy.runs.Add(-1)
	}
}

// ---- a T7 game ----

// t7Game is the engine (game.go) of a T7 room's game: Steam's dedicated server, on BOIII.
type t7Game struct {
	copy    *t7Copy
	entered atomic.Bool // it runs from the server's copy of BOIII (enter)
	server  string      // the folder of Steam's dedicated server (t7Server)
	mapName string      // one of t7MapList
	size    int         // the most players it takes (com_maxclients)
	// password is the password a client joins it with (g_password), and rcon its remote console's, which only the
	// server has.
	password, rcon string
	// status is called with the clients that the game's status lists, each second that the game answers (rconWatch).
	status func([]pcClient)
	send   chan string // lines for its remote console (game.command)
	// changed, when set, is called once the server's copy of BOIII is up to date for this game (t7Prepare).
	changed func()
	// fetch is set for the run that fetches what Steam's server downloads as it starts (t7Fetch). That run asks the
	// game's publisher, where a room's game is told not to, and loads no map.
	fetch bool
}

func newT7Game(copy *t7Copy, server, mapName string, size int, password string) *t7Game {
	return &t7Game{copy: copy, server: server, mapName: mapName, size: size, password: password, rcon: randomHex(16),
		send: make(chan string, 16)}
}

// prepare makes the game's home the game's folder (t7Prepare). The game runs from its home.
func (t *t7Game) prepare(ctx context.Context, g *game) ([]string, string, func(string) string, error) {
	if err := g.t7Prepare(ctx); err != nil {
		return nil, "", nil, err
	}
	return g.t7Args(), g.home, t7Clean(), nil
}

// watch reads the game's output from its log file (t7Tail), and watches the game over its remote console, which is
// also its console (rconWatch). The game drops a packet for its console that comes within rcon_timeout of the last
// (t7Prepare sets its least, 100 ms). It writes each answer to its output too.
func (t *t7Game) watch(g *game, done <-chan struct{}, silent chan<- time.Duration) {
	tailed := make(chan struct{})
	go func() {
		g.t7Tail(done)
		close(tailed)
	}()
	g.rconWatch(rcon{password: t.rcon, send: t.send, status: t.status, parse: t7StatusLine,
		gap: 150 * time.Millisecond, logged: true}, done, silent)
	<-done
	<-tailed
}

// say queues line for the game's remote console, which is its console (watch).
func (t *t7Game) say(g *game, line string) {
	select {
	case t.send <- line:
	default:
		g.logf("its remote console has lines waiting; %q wasn't sent", line)
	}
}

// quit sends the game its console's quit command.
func (t *t7Game) quit(g *game) bool {
	g.command("quit")
	return true
}

// t7Script is the server's script in a T7 game. BOIII compiles it and loads it with the level's own scripts, from the
// custom scripts in the game's folder (boiii/custom_scripts/zm). It prints what the server's script in a T6 game
// prints (t6Script), for the server to read (pcSaid):
//
//	zombies_lobby: stats 0 4350 1200 31 12 1 0 2 0  a player's line of the scoreboard, by their client's slot: all the
//	                                                points they earned, the points left to spend, their kills,
//	                                                headshots, downs, revives and doors bought, and 0 for no team. It
//	                                                is printed before "round" and "over".
//	zombies_lobby: round 5                          a round begins
//	zombies_lobby: over 12                          game over, and the round it ended on
//	zombies_lobby: exit                             the game's end has played out
//
// The level's scripts keep each of these numbers on the player (scripts/zm/_zm.gsc and its neighbours, as the game's
// mod tools have them). The script reports no main quest, and has none of the rooms' mods.
const t7Script = `// zombies-server's (server/t7.go): the rounds, the scoreboard at each, game over, and its end played out
#using scripts\shared\callbacks_shared;
#using scripts\shared\system_shared;

#insert scripts\shared\shared.gsh;

#namespace zombies_lobby;

REGISTER_SYSTEM("zombies_lobby", &__init__, undefined)

function __init__()
{
	callback::on_start_gametype(&init);
}

function init()
{
	level thread rounds();
	level thread game_over();
}

function number(value)
{
	if (!isdefined(value))
		return 0;

	return value;
}

// A number that the level keeps on the player, or else in what it keeps of them from map to map.
function kept(player, value, name)
{
	if (isdefined(value))
		return value;

	if (isdefined(player.pers) && isdefined(player.pers[name]))
		return player.pers[name];

	return 0;
}

function stats()
{
	players = GetPlayers();

	for (i = 0; i < players.size; i++)
	{
		player = players[i];
		line = "zombies_lobby: stats " + player GetEntityNumber();
		line = line + " " + number(player.score_total) + " " + number(player.score);
		line = line + " " + kept(player, player.kills, "kills") + " " + kept(player, player.headshots, "headshots");
		line = line + " " + kept(player, player.downs, "downs") + " " + kept(player, player.revives, "revives");
		line = line + " " + kept(player, undefined, "doors_purchased") + " 0";
		println(line);
	}
}

function rounds()
{
	level endon("end_game");

	for (;;)
	{
		level waittill("start_of_round");
		stats();
		println("zombies_lobby: round " + level.round_number);
	}
}

function game_over()
{
	level waittill("end_game");
	stats();
	println("zombies_lobby: over " + number(level.round_number));
	level waittill("stop_intermission");
	println("zombies_lobby: exit");
}
`

// t7Home lists what a T7 game's home has of Steam's server: links to its program, the libraries it loads, and the
// folder with its playlists.
var t7Home = []string{"BlackOps3_UnrankedDedicatedServer.exe", "steam_api64.dll", "steamclient64.dll", "tier0_s64.dll",
	"vstdlib_s64.dll", "codlogo.bmp", "machinecfg"}

// t7Prepare makes the game's home the game's folder, which the game runs from. It first brings the server's copy of
// BOIII up to date, unless another game runs from the copy (enter). Everything the game only reads is a link: to
// Steam's server, to the fastfiles (t7Reads, and those that the server fetched: t7Fetch), and to BOIII's program. So
// a game that is taken over can change nothing that another game runs. The home's own files are what the game
// writes (its log, in identities), its config, and the server's script (t7Script).
//
// The config, zone/lobby.cfg, sets the game up. The game's command line execs it. It sets how many players the game
// takes and with what password, and gives the remote console a password that only the server has. The game tells no
// one of itself (sv_lanOnly). It hosts a lobby of Zombies and starts the room's map at once (sv_lobby_mode,
// sv_skip_lobby, sv_maprotation: BOIII's lobby script reads them, data/lobby_scripts), so its players join a game
// that runs. It is told not to ask the game's publisher for its fastfile of the day (live_useLPC), which it has. The
// game sets live_useLPC as it starts, whatever its command line says, so only the config can.
func (g *game) t7Prepare(ctx context.Context) error {
	t := g.t7()
	if !t.fetch && !slices.ContainsFunc(t7MapList, func(m mapEntry) bool { return m.name == t.mapName }) {
		return fmt.Errorf("%s is no T7 map", t.mapName)
	}
	if err := t.enter(ctx); err != nil {
		return err
	}
	if t.changed != nil {
		t.changed()
	}
	copyDir, err := filepath.Abs(t.copy.Dir)
	if err != nil {
		return err
	}
	if err := g.t7Data(filepath.Join(copyDir, boiii.Data)); err != nil {
		return err
	}
	server := filepath.Join(t.server, "UnrankedServer")
	zone, scripts := filepath.Join(g.home, "zone"), filepath.Join(g.home, "boiii", "custom_scripts", "zm")
	for _, dir := range []string{zone, filepath.Join(g.home, "boiii"), filepath.Dir(scripts), scripts} {
		if err := g.mkdir(dir); err != nil {
			return err
		}
	}
	links := map[string]string{boiii.Exe: filepath.Join(copyDir, boiii.Exe)}
	for _, name := range t7Home {
		links[name] = filepath.Join(server, name)
	}
	for _, from := range []struct {
		dir    string
		server bool
	}{{filepath.Join(server, "zone"), true}, {filepath.Join(g.dir, "zone"), false}} {
		entries, err := os.ReadDir(from.dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if t7Reads(e.Name(), from.server) {
				links[filepath.Join("zone", e.Name())] = filepath.Join(from.dir, e.Name())
			}
		}
	}
	if !t.fetch {
		for _, file := range t.copy.fetched() {
			links[filepath.Join("zone", filepath.Base(file))] = file
		}
	}
	for name, to := range links {
		if err := os.Symlink(to, filepath.Join(g.home, name)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(scripts, "lobby.gsc"), []byte(t7Script), 0o644); err != nil {
		return err
	}
	cfg := fmt.Sprintf(`// zombies-server's, for room %s
set live_steam_server_name "Room %s"
set com_maxclients %d
set lobby_min_players 1
set g_password "%s"
set rcon_password "%s"
set rcon_timeout 100
set sv_lanOnly 1
set sv_lobby_mode "zm"
exec "gamedata/gamesettings/zm/gamesettings_default.cfg"
exec "gamedata/configs/common/default_xboxlive.cfg"
exec "gamedata/gamesettings/zm/gamesettings_zclassic.cfg"
`, g.name, g.name, t.size, t.password, t.rcon)
	if !t.fetch {
		cfg += fmt.Sprintf("set live_useLPC 0\nset sv_skip_lobby 1\nset sv_maprotation \"gametype zclassic map %s\"\n",
			t.mapName)
	}
	// The config holds the passwords. Only the server and the game, which runs in the server's group, can read it.
	return os.WriteFile(filepath.Join(zone, "lobby.cfg"), []byte(cfg), 0o640)
}

// t7Data has the games' user find BOIII's data, in the server's copy. BOIII looks for it in the folder boiii of its
// user's local application data, which under Wine is in the user's Wine prefix. Each game makes that folder's link to
// the data again, as its user: the prefix is the user's, and the server writes nothing there. The folder itself is
// the games' to write in (BOIII keeps a file or two there), and the data is only theirs to read.
func (g *game) t7Data(data string) error {
	if os.Getenv("WINEPREFIX") == "" {
		return nil // no Wine: a test's stand-in for it
	}
	out, err := g.asGame("sh", "-c", `for user in "$WINEPREFIX"/drive_c/users/*/; do
    case $user in */Public/) continue ;; esac
    mkdir -p "$user/AppData/Local/boiii" && ln -sfn "$0" "$user/AppData/Local/boiii/data" || exit 1
done`, data).CombinedOutput()
	if err != nil {
		return fmt.Errorf("can't give the games' user BOIII's data (%v): %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// t7Args returns the game's command line after the wine command. BOIII, run in the game's home, finds Steam's server
// there and runs it (-dedicated), with no window (-headless) and no dialog at a crash (-quiet-crash). It loads no
// sounds, which the server's install has none of (-nosnd), and leaves its own files as the server's copy has them
// (-noupdate). It is given a master server that is none (-master-servers), so that it looks no name up: the game
// tells no master of itself anyway (t7Prepare: sv_lanOnly). The game listens on its port, and names the address
// g.ip as its own when that is set (127.0.0.1 for a room's game).
func (g *game) t7Args() []string {
	args := []string{boiii.Exe, "-dedicated", "-headless", "-quiet-crash", "-nosnd", "-noupdate",
		"-master-servers", "127.0.0.1:9", "+set", "fs_game", "", "+set", "net_port", strconv.Itoa(g.port)}
	if g.ip != "" {
		args = append(args, "+set", "net_ip", g.ip)
	}
	return append(args, "+set", "logfile", "2", "+exec", "lobby.cfg")
}

// t7Tail reads the game's output from the log file that the game writes its console to, in its home
// (identities/<the server's name there>/console_mp.log), and passes each line on as the game's output (game.printed).
// It reads until done is closed, and then what the game wrote last.
func (g *game) t7Tail(done <-chan struct{}) {
	var f *os.File
	var r *bufio.Reader
	var line []byte
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	read := func() {
		if f == nil {
			files, _ := filepath.Glob(filepath.Join(g.home, "identities", "*", "console_mp.log"))
			if len(files) == 0 {
				return
			}
			var err error
			if f, err = os.Open(files[0]); err != nil {
				f = nil
				return
			}
			r = bufio.NewReader(f)
		}
		for {
			part, err := r.ReadBytes('\n')
			line = append(line, part...)
			if err != nil {
				return // the rest of the line is yet to be written
			}
			g.mu.Lock()
			printed := g.printed
			g.mu.Unlock()
			if printed != nil {
				printed(string(line))
			}
			line = line[:0]
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			read()
			return
		case <-tick.C:
			read()
		}
	}
}

var (
	t7Colour = regexp.MustCompile(`\^[0-9]`)
	// t7Status matches the lines of the game's answer to the status command but the clients' (t7StatusLine).
	t7Status = regexp.MustCompile(`^(-{10} [A-Za-z ]+ -{10}|ffotd version |playlist \d+ version |protocol \d+ version |` +
		`Local IP: |Public IP: |Mod Loaded: |Server Browser: |Map: |Gametype: |We are hosting a lobby |` +
		`We are not hosting a lobby|map: |num score ping |--- ----- ---- |Server is not running\.)`)
	t7StatusRow = regexp.MustCompile(`^\s*\d+\s+-?\d+\s+\S+\s+[0-9a-fA-F]+\s`)
)

// t7Clean returns what rewrites each line of a T7 game's output for the log: the text without the game's colours,
// and without the bytes that are no text (the game writes its name with a sign in Windows' own encoding). It drops the game's answers to the server's own status command, which the game writes to its output each second
// that the server asks (rconWatch). An answer starts with its first heading and ends with the line after its last
// client. Within it, a line that is none of an answer's ends it, and is kept.
func t7Clean() func(string) string {
	answer, members := false, false
	return func(line string) string {
		line = strings.TrimSpace(strings.ToValidUTF8(t7Colour.ReplaceAllString(line, ""), ""))
		switch {
		case line == "---------- Live ----------":
			answer, members = true, false
			return ""
		case !answer:
			return line
		case t7Status.MatchString(line):
			// the members of the game's lobby follow, a line each, up to the next heading
			members = strings.HasPrefix(line, "We are hosting a lobby ")
			if line == "Server is not running." {
				answer = false
			}
			return ""
		case members || t7StatusRow.MatchString(line):
			return ""
		}
		answer = false
		return line // the empty line that ends an answer is dropped as any empty line is
	}
}

// t7StatusLine parses a line of the game's answer to the status command, as t6StatusLine does a T6 game's. status
// reports whether the line is part of that answer at all. client reports whether the line lists a client, which is
// then c:
//
//	map: zm_factory
//	num score ping xuid             name             address                  qport
//	--- ----- ---- ---------------- ---------------- ------------------------ ------
//	  0  1500   32 110000112345678  Ann              127.1.0.1:28960           -1234
//
// A client's ping is a number once the client is in the game. A client's name may have spaces: the name is what is
// left between the xuid and the address.
func t7StatusLine(line string) (c pcClient, client, status bool) {
	if t7Status.MatchString(line) {
		return c, false, true
	}
	if !t7StatusRow.MatchString(line) {
		return c, false, false
	}
	f := strings.Fields(line)
	at := -1
	for i := len(f) - 1; i > 3; i-- {
		if address, err := netip.ParseAddrPort(f[i]); err == nil {
			c.address, at = address.String(), i
			break
		}
	}
	if at < 0 {
		return c, false, true // a bot's row, or a row with no address yet
	}
	c.slot, _ = strconv.Atoi(f[0])
	if ping, err := strconv.Atoi(f[2]); err == nil && ping < 999 {
		c.in = true
	}
	c.name = strings.Join(f[4:at], " ")
	return c, true, true
}

// ---- what Steam's server fetches ----

// t7Ready is the line that the game prints once it has its fastfile of the day and opens its lobby.
const t7Ready = "CreateDedicatedModsLobby: ready!"

// t7Fetch runs the game once, to fetch what Steam's server downloads from the game's publisher as it starts (the
// comment at the top), and keeps what it fetched in the server's copy of BOIII (t7Copy.fetched). g is a game as a
// room's is, whose engine is told to fetch. The run takes about ten seconds, and t7Fetch gives it a few minutes. It
// reaches the publisher only where the games' user reaches the internet: before the image's firewall is up
// (docker/entrypoint.sh), or in a container without one.
func t7Fetch(ctx context.Context, g *game) error {
	t := g.t7()
	t.fetch = true
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var once sync.Once
	var kept error
	ready := false
	g.lines = func(line string) {
		if !strings.Contains(line, t7Ready) {
			return
		}
		once.Do(func() {
			ready, kept = true, t.keep(g.home)
			cancel()
		})
	}
	state, err := g.runOnce(ctx)
	t.leave()
	os.RemoveAll(g.home)
	switch {
	case !ready:
		return fmt.Errorf("Steam's server did not fetch its files from the game's publisher (%s)", exitReason(state, err))
	case kept != nil:
		return kept
	}
	return nil
}

// keep copies the fastfiles that the game fetched into its home (identities/<name>/LPC) to the server's copy of
// BOIII (t7Copy.lpc), and returns an error when there are none. It then removes what the copy kept for another build
// of Steam's server.
func (t *t7Game) keep(home string) error {
	files, _ := filepath.Glob(filepath.Join(home, "identities", "*", "LPC", "*.ff"))
	if len(files) == 0 {
		return errors.New("Steam's server opened its lobby, and fetched no file for it")
	}
	dir := t.copy.lpc()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		to := filepath.Join(dir, filepath.Base(file))
		if err := copyFile(file, to+".part", 0o644); err != nil {
			return err
		}
		if err := os.Rename(to+".part", to); err != nil {
			return err
		}
		log.Printf("t7: Steam's server fetched %s, which the server keeps for its games (%s)", filepath.Base(file), dir)
	}
	others, _ := os.ReadDir(filepath.Dir(dir))
	for _, e := range others {
		if e.Name() != filepath.Base(dir) {
			os.RemoveAll(filepath.Join(filepath.Dir(dir), e.Name()))
		}
	}
	return nil
}

// copyFile copies the file from to the file to, with the given mode.
func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
