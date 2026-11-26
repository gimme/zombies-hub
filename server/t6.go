package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"zombies/internal/plutonium"
)

// Black Ops II (t6) is played on Plutonium (https://plutonium.pw), which the server integrates only lightly. The game
// of a room on a T6 map is a Plutonium dedicated server in LAN mode. In LAN mode it needs no server key and no
// account, and it accepts the clients that Plutonium starts in LAN mode. The server starts Plutonium's server like any
// other game (game.go: as the games' user, on a port that only the server reaches). It sets the game up, and reads who
// is in it each second, over the game's remote console (t6Watch). It relays the players to the game on the server's
// one port (relay.go: a T6 route).
//
// Each player's own PC runs Plutonium on their own copy of Black Ops II. The launcher starts it for them
// (launcher.go, launcher/), or they join by hand with a console line that their room's page shows them
// (web/index.html). The server identifies a player's game by the address it comes from: the address the player's
// launcher or page calls the server from (lobby.t6Seat). The player's game joins the room's game with the room's
// password (room.password).
//
// The server puts a script in the game that reports how the game goes (t6Script). It reports each round, and the end
// of the map's main quest, which set the players' records on the leaderboards. At game over it reports each player's
// line of the scoreboard, which becomes the room's report of the game. The script also implements the room's mods
// (mods.go). Compared with a T5 room's game, a T6 room's game has no stats or collectibles kept by the server, and no
// browser to play it in.
//
// Several rooms play T6 at once, each on a Plutonium server of its own, and all from the server's one copy of
// Plutonium (t6Copy). Besides its game's port, each Plutonium server opens a UDP port on every address: 3074, or the
// next port above it that it can open. Nothing sets that port (net_broadcast_port does not). With the games' own
// ports there, a second Plutonium server took a port of one of the games, and that game then listened on another
// port, where the server never reached it. So no game has a port there (gamePorts). Three Plutonium servers at once
// opened 3074, 3076 and 3077, the server's own port being 3075.
//
// The players join a T6 game in the order of their places in the room (t6Turn), so that the game shows each of them
// in the colour their place has in the room (web/src/page.css: .avatar[data-place]). The game's HUD colours a
// player's points by their position among the players in the game, the first white, then blue, yellow and green
// (ui_mp/t6/zombie/hudcompetitivescoreboardzombie.lua in the game's files). Plutonium's server picks each client's
// slot itself: nothing tells it a player's place, as the server tells a T5 game (t5.go: placesOf), and setting
// sv_privateClients on a running game does not move its next client to a later slot. It gives a new client the first
// free slot, as a T5 game does (bo1-zombies/src/client_mp/sv_client_mp.cpp: SV_DirectConnect), and the HUD orders the
// players by their slots. Two games that joined a room's game in the order of their places had slots 0 and 1, and
// the second showed the first's points in white and its own in blue. So a player has their room's colour while
// everyone before them in the room is in the game. While one of those is yet to join, or once one has left, everyone
// after them is a colour up.
//
// A client joins in steps. Plutonium's server logs the first three, and https://github.com/Alextechgamer/bo2-ios
// sends them all (ios/PlutoniumLAN.cpp): Plutonium's own connectReq, connectGTSReq and moddl_list, then the game's
// getchallenge and connect. The connect takes the slot, so it is the packet that waits for the client's turn
// (relay.holds). A client sends its connect again every 3 seconds for as long as the game does not answer: one that
// was left to wait still sent it after three and a half minutes. A T5 client does the same, and gives up when the
// game has sent it nothing for 200 seconds (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_CheckForResend,
// CL_CheckTimeout). A turn comes far sooner (lobby.joinWait).

const t6 = "t6"

// plutoniumPort is the first of the UDP ports that Plutonium's servers open besides their games' ports (the comment at
// the top).
const plutoniumPort = 3074

var t6MapList = []mapEntry{
	// The T6 maps are named after their game settings files in Plutonium's server configs (t6Maps). The main maps come
	// first, in the order of their release, then the maps that have the survival mode alone: three parts of TranZit's
	// map, and Nuketown. Then come the maps of the two modes in which the players play against each other (versus):
	// Grief's, in two teams, and Turned's, each for themselves, each in the order of their release. Their pictures are
	// textures in T6's image packs (ipak.go), by the names of the textures that their materials draw. The preview is
	// the map's tile on its card in the game's lobby, which its leaderboards show too: the material
	// menu_zm_<map>_<mode>_<location> (in patch_ui_zm.ff, and ui_zm.ff for TranZit's maps). The loading screen is the
	// material loadscreen_<map>_<mode>_<location> (in code_post_gfx_zm.ff, or the map pack's dlc<n>_load_zm.ff). Bus
	// Depot's is the same picture as TranZit's.
	//
	// A part of a map has one tile for Grief and Turned both, menu_zm_<map>_zencounter_<location>, after the modes'
	// group (zencounter). Cell Block's is menu_zm_prison_select_grief_cellblock, after Mob of the Dead's own, and the
	// Diner's is menu_zm_transit_zsurvival_diner, as if it were a part of TranZit to survive on. Their loading screens
	// are not in the packs, by any of two million names that were tried, so the two have the loading screens of their
	// maps' own stories.
	//
	// The place is the card's words over the map's name, the localized string ZMUI_<MAP>_CAPS (in en_patch_zm.ff), and
	// the mode, after the name, is ZMUI_SURVIVAL_CAPS, as the page writes them. The page has no description of the T6
	// maps, because T6 keeps its localized strings in zones that the server doesn't read.
	{of: t6, name: "zm_classic_transit", title: "TranZit", place: "Green Run",
		preview: "menu_zm_transit_zclassic_transit", loadscreen: "loadscreen_transit_classic"},
	{of: t6, name: "zm_classic_rooftop", title: "Die Rise", place: "Great Leap Forward",
		preview: "menu_zm_highrise_zclassic_rooftop", loadscreen: "loadscreen_dierise"},
	{of: t6, name: "zm_classic_prison", title: "Mob of the Dead", place: "Alcatraz Island",
		preview: "menu_zm_prison_select_mob", loadscreen: "loadscreen_zm_hellcatraz"},
	{of: t6, name: "zm_classic_processing", title: "Buried", place: "Resolution 1295",
		preview: "menu_zm_buried_zclassic_processing", loadscreen: "loadscreen_buried_zclassic_processing"},
	{of: t6, name: "zm_classic_tomb", title: "Origins", place: "Excavation Site 64",
		preview: "menu_zm_tomb_zclassic_tomb", loadscreen: "loadscreen_zombie_le_tombeau"},
	{of: t6, name: "zm_standard_transit", title: "Bus Depot", place: "Green Run", mode: "Survival",
		preview: "menu_zm_transit_zsurvival_transit", loadscreen: "loadscreen_transit_standard_busdepot"},
	{of: t6, name: "zm_standard_town", title: "Town", place: "Green Run", mode: "Survival",
		preview: "menu_zm_transit_zsurvival_town", loadscreen: "loadscreen_transit_standard_town"},
	{of: t6, name: "zm_standard_farm", title: "Farm", place: "Green Run", mode: "Survival",
		preview: "menu_zm_transit_zsurvival_farm", loadscreen: "loadscreen_transit_standard_farm"},
	{of: t6, name: "zm_standard_nuked", title: "Nuketown", place: "Nevada, U.S.A.", mode: "Survival",
		preview: "menu_zm_nuketown_select", loadscreen: "loadscreen_zm_nuketown"},
	{of: t6, name: "zm_grief_transit", title: "Bus Depot", place: "Green Run", mode: "Grief", versus: grief,
		preview: "menu_zm_transit_zencounter_transit", loadscreen: "loadscreen_transit_grief_busdepot"},
	{of: t6, name: "zm_grief_town", title: "Town", place: "Green Run", mode: "Grief", versus: grief,
		preview: "menu_zm_transit_zencounter_town", loadscreen: "loadscreen_transit_grief_town"},
	{of: t6, name: "zm_grief_farm", title: "Farm", place: "Green Run", mode: "Grief", versus: grief,
		preview: "menu_zm_transit_zencounter_farm", loadscreen: "loadscreen_transit_grief_farm"},
	{of: t6, name: "zm_grief_cellblock", title: "Cell Block", place: "Alcatraz Island", mode: "Grief", versus: grief,
		preview: "menu_zm_prison_select_grief_cellblock", loadscreen: "loadscreen_zm_hellcatraz"},
	{of: t6, name: "zm_grief_street", title: "Borough", place: "Resolution 1295", mode: "Grief", versus: grief,
		preview: "menu_zm_buried_zencounter_street", loadscreen: "loadscreen_buried_zgrief_street"},
	{of: t6, name: "zm_cleansed_diner", title: "Diner", place: "Green Run", mode: "Turned", versus: turned,
		preview: "menu_zm_transit_zsurvival_diner", loadscreen: "loadscreen_transit_classic"},
	{of: t6, name: "zm_cleansed_street", title: "Borough", place: "Resolution 1295", mode: "Turned", versus: turned,
		preview: "menu_zm_buried_zencounter_street", loadscreen: "loadscreen_buried_zcleansed_street"},
}

// t6Title is the title (title.go) for Black Ops II.
type t6Title struct {
	// newGame makes the game for a room on the given port, with the room's mods. The game waits for the given number of
	// players. newGame is nil on a server without T6's game files, where no room plays a T6 map.
	newGame func(code, mapName string, port, players int, password string, mods []string) *game
	// pluto is the server's copy of Plutonium, which the games run. The players' launchers match its revision
	// (launcherPlay). It is nil on a server without T6's game files.
	pluto *t6Copy
	// knocked maps an address to the time the server last logged that a game at that address asked to join and
	// matched no player's seat (t6Seat).
	knocked map[netip.Addr]time.Time
}

// t6 returns the server's Black Ops II title.
func (l *lobby) t6() *t6Title {
	for _, t := range l.titles {
		if t, ok := t.(*t6Title); ok {
			return t
		}
	}
	return nil
}

func (*t6Title) id() string    { return t6 }
func (*t6Title) label() string { return "Black Ops II" }
func (*t6Title) inPage() bool  { return false }

func (t *t6Title) free(*lobby) error {
	if t.newGame == nil {
		return errors.New("The server doesn't have Black Ops II.")
	}
	return nil
}

// begin makes the Plutonium server that is r's game. In Grief, the server's script puts each player in the team of
// their place (teamOf), as a Black Ops game with Grief on does, and as the room shows them.
func (t *t6Title) begin(l *lobby, r *room, rg *roomGame) *game {
	g := t.newGame(r.code, r.mapName, rg.port, len(rg.seats), r.password, r.mods)
	if r.teams() { // a T6 room has no mod of Grief: its map is one of Grief
		for i, s := range rg.seats {
			team := teamOf(i, len(rg.seats)) - 1
			g.t6().teams[team] = append(g.t6().teams[team], s.player.name)
		}
	}
	g.lines = func(line string) { l.t6Said(r, rg, line) }
	g.t6().status = func(clients []t6Client) { l.t6Status(r, rg, clients) }
	g.t6().changed = func() {
		l.mu.Lock()
		l.change()
		l.mu.Unlock()
	}
	return g
}

// done is called once rg's game has quit: it no longer runs from the server's copy of Plutonium.
func (*t6Title) done(_ *lobby, rg *roomGame) { t6Of(rg).leave() }

// drop removes a player who left the room from the game. A T6 game keeps no character for them. drop kicks their
// client by its slot and ends their route, and the relay no longer lets their game in.
func (*t6Title) drop(l *lobby, r *room, s *seat) {
	if s.slot >= 0 {
		r.game.g.command("clientkick " + strconv.Itoa(s.slot))
	}
	l.relay.end(s.local)
}

func (*t6Title) late(l *lobby, r *room, rg *roomGame) { l.t6Late(r, rg) }

// lacks reports whether p's game folder lacks the zone of their room's map, as their launcher says (launcherHas): a
// copy of the game without the map's pack. The launcher gets the rest of what the PC lacks. lacks is false while the
// launcher has said nothing of the folder: it is not running, it is still getting the game files, or it is a launcher
// from before it said. A player who joins by hand is not checked either.
func (*t6Title) lacks(l *lobby, p *player) bool {
	zone := t6Zone(p.room.mapName)
	return zone != "" && p.launcher.copied && !slices.Contains(p.launcher.has, zone)
}

func (*t6Title) showMember(l *lobby, r *room, m *player, mv *memberView) {
	if !mv.InGame {
		mv.Downloading, mv.Got = m.downloading() // their launcher's download of T6's game files (launcher.go)
	}
	mv.Missing = r.game == nil && !mv.Downloading && l.missing(m)
}

func (*t6Title) showRoom(l *lobby, r *room, p *player, rv *roomView) {
	rv.Password = r.password
	rv.Up = r.game != nil && !r.game.up.IsZero()
	// no one can join through a seat in a game that is ending
	if s := r.seatOf(p); s != nil && r.game.ending == "" && !s.left {
		rv.T6 = &t6View{Up: !r.game.up.IsZero(), Name: p.name}
		if rv.T6.Up {
			rv.T6.Password = t6Of(r.game).password
		}
	}
}

// t6 returns the game's engine when it is Plutonium's server, or else nil.
func (g *game) t6() *t6Game {
	t, _ := g.engine.(*t6Game)
	return t
}

// t6Of returns the T6 engine of rg's game. It returns nil for another title's game, or when there is no game.
func t6Of(rg *roomGame) *t6Game {
	if rg == nil || rg.g == nil {
		return nil
	}
	return rg.g.t6()
}

// ---- the engine, as the runner runs it (game.go) ----

// prepare makes the game's home a Plutonium folder (t6Prepare). The game runs from its home.
func (t *t6Game) prepare(ctx context.Context, g *game) ([]string, string, func(string) string, error) {
	if err := g.t6Prepare(ctx); err != nil {
		return nil, "", nil, err
	}
	return g.t6Args(), g.home, t6Line, nil
}

// watch runs t6Watch, which is also the game's console. It therefore runs even when the game has no timeouts.
func (t *t6Game) watch(g *game, done <-chan struct{}, silent chan<- time.Duration) {
	g.t6Watch(done, silent)
}

// say queues line for the game's remote console, which is its console (t6Watch).
func (t *t6Game) say(g *game, line string) {
	select {
	case t.send <- line:
	default:
		g.logf("its remote console has lines waiting; %q wasn't sent", line)
	}
}

// quit sends the game its console's quit command, which notifies its clients first. On Ctrl+C, Plutonium's server
// ends at once without notifying its clients.
func (t *t6Game) quit(g *game) bool {
	g.command("quit")
	return true
}

// t6Map describes a T6 map: the zone it loads, the group and the type of its game mode, and where in the zone it
// starts. t6Maps has the T6 maps of mapTitles by their names there. A map's name is the name of its game settings
// file in Plutonium's server configs.
type t6Map struct{ zone, group, gametype, location string }

var t6Maps = map[string]t6Map{
	"zm_classic_transit":    {"zm_transit", "zclassic", "zclassic", "transit"},
	"zm_standard_transit":   {"zm_transit", "zsurvival", "zstandard", "transit"},
	"zm_standard_town":      {"zm_transit", "zsurvival", "zstandard", "town"},
	"zm_standard_farm":      {"zm_transit", "zsurvival", "zstandard", "farm"},
	"zm_standard_nuked":     {"zm_nuked", "zsurvival", "zstandard", "nuked"},
	"zm_classic_rooftop":    {"zm_highrise", "zclassic", "zclassic", "rooftop"},
	"zm_classic_prison":     {"zm_prison", "zclassic", "zclassic", "prison"},
	"zm_classic_processing": {"zm_buried", "zclassic", "zclassic", "processing"},
	"zm_classic_tomb":       {"zm_tomb", "zclassic", "zclassic", "tomb"},
	"zm_grief_transit":      {"zm_transit", "zencounter", "zgrief", "transit"},
	"zm_grief_town":         {"zm_transit", "zencounter", "zgrief", "town"},
	"zm_grief_farm":         {"zm_transit", "zencounter", "zgrief", "farm"},
	"zm_grief_cellblock":    {"zm_prison", "zencounter", "zgrief", "cellblock"},
	"zm_grief_street":       {"zm_buried", "zencounter", "zgrief", "street"},
	"zm_cleansed_diner":     {"zm_transit_dr", "zencounter", "zcleansed", "diner"}, // the Diner is a zone of its own
	"zm_cleansed_street":    {"zm_buried", "zencounter", "zcleansed", "street"},
}

// t6Zone returns the game file that a T6 map's zone is, by its path in the game's folder, with slashes: a copy of the
// game has the map when it has this file. It returns "" for a map that is none of T6's.
func t6Zone(mapName string) string {
	if m, ok := t6Maps[mapName]; ok {
		return "zone/all/" + m.zone + ".ff"
	}
	return ""
}

// t6Installed returns the T6 maps that the game files in dir have. dir is a copy of a Black Ops II install. It has a
// map when it has the map's zone. t6Installed returns no map when dir lacks a file that every map loads.
func t6Installed(dir string) []string {
	if !isFile(filepath.Join(dir, "zone", "all", "patch_zm.ff")) || !isFile(filepath.Join(dir, "binkw32.dll")) {
		return nil
	}
	var maps []string
	for _, m := range mapTitles {
		if t, ok := t6Maps[m.name]; ok && m.of == t6 && isFile(filepath.Join(dir, "zone", "all", t.zone+".ff")) {
			maps = append(maps, m.name)
		}
	}
	return maps
}

// ---- Plutonium, the server's copy ----

// The server keeps one copy of Plutonium (plutonium.Copy, in -plutonium-dir). It downloads the copy from Plutonium's
// updater and brings it up to date before a T6 game (t6Prepare), when no other game runs from the copy. The games only
// read the copy.

// bootstrapper is the program that starts a Plutonium game, as a path in the Plutonium folder.
const bootstrapper = plutonium.Bootstrapper

// t6Copy is the server's copy of Plutonium, as the T6 games share it. Every game runs from the copy: its home links
// to the copy's files (t6Prepare). So the copy changes only while no game runs from it. An update replaces the files
// of the revision before, which a game that runs still loads from. The players' launchers also match the copy's
// revision (launcherPlay), and a Plutonium server turns away a client of another revision: the launcher of a player
// who comes back to a game that runs would bring their PC to a revision that the game is not of.
type t6Copy struct {
	*plutonium.Copy
	mu   sync.Mutex   // held while a game comes to the copy (enter), so that one game at a time brings it up to date
	runs atomic.Int32 // the games that run from the copy now, each from its enter until its leave
}

// enter brings the server's copy of Plutonium up to date for the game, which is about to run from it (t6Prepare). It
// leaves the copy as it is when another game runs from it now. The game then runs the revision that the other game
// has, and a later game brings the copy up to date, once none runs. The game leaves the copy when it has quit
// (leave).
func (t *t6Game) enter(ctx context.Context) error {
	c := t.pluto
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

// leave is called once the game has quit. It does nothing for a game that never came to the copy: one that did not
// start.
func (t *t6Game) leave() {
	if t.entered.Swap(false) {
		t.pluto.runs.Add(-1)
	}
}

// ---- a T6 game ----

// t6Game is the engine (game.go) of a T6 room's game: Plutonium's server, not BO1Zombies.exe.
type t6Game struct {
	pluto   *t6Copy
	entered atomic.Bool // it runs from the server's copy of Plutonium (enter)
	mapName string      // one of t6Maps
	players int         // how many players it waits for before round 1 (zombies_minplayers)
	// waits is how long the game waits for the other players once the first is in, before it starts without them
	// (t6Prepare: sv_connectTimeout). The server waits as long before it calls the game off (loadTimeout), so the
	// server's deadline comes first.
	waits    time.Duration
	size     int    // the most players it takes (sv_maxclients)
	password string // the password a client joins it with (g_password)
	rcon     string // its remote console's password, which only the server has
	// teams has the names that the players of each team play under, in a game of Grief, for the server's script to put
	// them in their teams (t6Script). It is empty in a game of another mode.
	teams [2][]string
	mods  []string // the mods it has, as its room has them (room.mods), for the server's script (t6ModDvar)
	// status is called with the clients that the game's status lists, each second that the game answers. A client is
	// listed by the address the game sees it at, which is a seat's address.
	status func([]t6Client)
	send   chan string // lines for its remote console (game.command)
	// updated is set once the server's copy of Plutonium is up to date for this game (t6Prepare). Until then the
	// launchers are not sent the game (launcherPlay), because they match the revision of the server's copy. changed,
	// when set, is called then, to wake the launchers' calls.
	updated atomic.Bool
	changed func()
}

// t6Client is a client in the status of Plutonium's server: its slot, the name it plays under, the address the game
// sees it at, and whether it is in the game and no longer connecting.
type t6Client struct {
	slot    int
	name    string
	address string
	in      bool
}

func newT6Game(pluto *t6Copy, mapName string, players, size int, waits time.Duration, password string,
	mods []string) *t6Game {
	return &t6Game{pluto: pluto, mapName: mapName, players: players, size: size, waits: waits, password: password,
		mods: mods, rcon: randomHex(16), send: make(chan string, 16)}
}

// t6ModDvar returns the dvar that tells the server's script of a mod that a T6 game has, and the dvar's value: 1 for
// a switch, and its value for a mod with options. name is the mod as a room has it (modsNamed).
func t6ModDvar(name string) (dvar, value string) {
	id, value, valued := strings.Cut(name, "=")
	if !valued {
		value = "1"
	}
	return "zombies_mod_" + id, value
}

// t6Own lists what a T6 game's home has of its own in storage/t6. Everything else in the home is a link to the
// server's copy of Plutonium (t6Prepare).
var t6Own = []string{"main", "players", "demos", "gamesettings", "scripts", "lobby.cfg"}

// t6Script is the server's script in a T6 game. Plutonium's server loads it from the scripts in its folder
// (storage/t6/scripts/zm), together with the level's own scripts. As far as a script can, it prints what the engine
// prints in a T5 game ("bo1_lobby: ...", t5.go: gameLine), for the server to read (t6Said):
//
//	zombies_lobby: stats 0 4350 1200 31 12 1 0 2 1  a player's line of the scoreboard, by their client's slot: all the
//	                                                points they earned, the points left to spend, their kills,
//	                                                headshots, downs, revives and doors bought, and their team in
//	                                                Grief (1 or 2, else 0). It is printed before "round" and "over".
//	zombies_lobby: round 5                          a round begins
//	zombies_lobby: egg 2 0 1                        the map's main quest is done: the side the players took (1
//	                                                Richtofen's, 2 Maxis's, 0 where the quest has none), then the
//	                                                slots of the players in the game then. It is printed once a game.
//	zombies_lobby: help bank 1                      the player in the slot had help from an earlier game: points out
//	zombies_lobby: help locker 0                    of the bank, or the gun out of their weapon locker. Each is
//	                                                printed once for a player.
//	zombies_lobby: perk had jugg 1                  the player in the slot came to the game with a persistent upgrade
//	zombies_lobby: perk got nube 0                  of the game's, by its name in the game's scripts, or earned one
//	                                                in the game. Each is printed once for a player and upgrade.
//	zombies_lobby: over 12 1                        game over, the round it ended on, and in Grief the team that won
//	zombies_lobby: exit                             the game's end has played out (the words on the screen, then the
//	                                                view over the map: 20 seconds). The game loads the map again a
//	                                                second and a half later.
//	zombies_lobby: mod zombiecap 32                 the game has a mod, at that value
//	zombies_lobby: wall m14_zm Bob                  Unique Wall Weapons hid a wall buy's prompt from a player, whom the
//	                                                script names: the first time in the game, for each gun
//
// The server makes nothing of the last two, which are for the game's log.
//
// The server stops the game on "exit". The players' games then go back to their menus from the end of the game, and
// not from the loading screen of the next game. The level's scripts (maps/mp/zombies/_zm.gsc and its neighbours) keep
// each of these numbers on the player. They count the doors in the stats they keep of the player. Turned counts no
// rounds, and its points left to spend are the points it ranks its players by.
//
// Grief has two teams: team 1 is the game's team A (its "axis"), and team 2 is team B ("allies"). A team wins once
// every player of the other is down and it has lived to the end of that round (maps/mp/zombies/_zm_game_module.gsc:
// wait_for_team_death_and_round_end), so a team that no one is in loses at once. Left to itself, the game puts each
// player in a team as they connect (maps/mp/gametypes_zm/_globallogic_ui.gsc: menuautoassign). In Zombies that is team
// B unless the game's party gives them a team (party_autoteams, getassignedteam), which a dedicated server's game may
// not have. So the script puts each player in the team that the server gives them (t6Game.teams), in place of the
// game's menuautoassign: the config names the players of each team (zombies_team_1 and zombies_team_2), by the names
// they play under. A player who is in neither list, such as one who joins by hand under another name, goes to the team
// with fewer players.
//
// A map's main quest is done when the level gives its achievement, to everyone in the game. The map's quest notifies
// the level then, and the map's script of achievements waits for that notify (maps/mp/zm_<map>_achievement.gsc in the
// game's scripts, as https://github.com/plutoniummod/t6-scripts has them):
//
//	TranZit           transit_sidequest_achieved     ZM_TRANSIT_SIDEQUEST, either side (zm_transit_sq.gsc)
//	Die Rise          highrise_sidequest_achieved    ZM_DLC1_HIGHRISE_SIDEQUEST, either side (zm_highrise_sq.gsc)
//	Mob of the Dead   pop_goes_the_weasel_achieved   ZM_DLC2_POP_GOES_THE_WEASEL, as the showdown on the bridge
//	                                                 begins, whoever wins it (zm_prison_sq_final.gsc)
//	Buried            sq_richtofen_complete,         ZM_DLC3_BURIED_SIDEQUEST, one for each side (zm_buried_sq.gsc)
//	                  sq_maxis_complete
//	Origins           tomb_sidequest_complete        ZM_DLC4_TOMB_SIDEQUEST, as the quest's last step begins
//	                                                 (zm_tomb_ee_main_step_8.gsc)
//
// The script waits for each of them on any map, and reports the first. A quest runs in its map's classic mode alone,
// and not on Easy, so another map or mode never notifies one.
//
// The quests of TranZit, Die Rise and Buried have two sides, Richtofen's and Maxis's, and the script reports which
// one the players took. Buried notifies the level of each side by name. TranZit and Die Rise have one notify for
// both. Just before it, they save the side as the last that each player finished, in the player's saved stats
// (update_sidequest_stats: sq_transit_last_completed and sq_highrise_last_completed, 1 for Richtofen's and 2 for
// Maxis's), for each player who was in the game when the quest started. The script reads the side from the first of
// those players. A player who joined later keeps the side of an earlier game there, and is passed over.
//
// A player's saved stats are their own PC's: Plutonium's game keeps them in the player's Plutonium folder
// (storage/demonware/18397/user), and sends them to the room's game as it joins. The room's game counts as an online
// game (sessionmodeisonlinegame), so on TranZit, Die Rise and Buried the bank has what the player put there in
// earlier games, and their weapon locker has the gun they left there. The three maps share the one bank and the one
// locker (level.banking_map and level.weapon_locker_map are zm_transit on each). The script reports the first time
// that a player has such help: when their bank balance (account_value, which maps/mp/zombies/_zm_banking.gsc sets as
// they connect) is less than it was when they joined, and when their weapon locker no longer has the gun that it had
// then (the name of the gun in their saved stats, which maps/mp/zombies/_zm_weapon_locker.gsc keeps). Points that a
// player banks and takes back in the one game are no help, and neither is a gun that they stored in it.
//
// The saved stats also earn a player the game's persistent upgrades ("perma-perks"), on those three maps' classic
// modes: more health, a Ray Gun for the Olympia off the wall, and so on (maps/mp/zombies/_zm_pers_upgrades.gsc
// registers them, by names such as jugg and nube). The level gives a player the ones their stats have earned as they
// first spawn, and gives and takes them as the stats change (_zm_pers_upgrades_system.gsc: pers_upgrades_monitor,
// which keeps them in the player's pers_upgrades_awarded). The script reports each upgrade that a player has when it
// first looks, as one they came with, and each one they have later, as one they earned in the game. It first looks a
// moment after it first sees the player playing, when the level has given them theirs.
//
// The server keeps each of these as an event of the game (rooms.go: gameEvent).
//
// The script implements the room's mods (mods.go), as the engine does in a T5 game. The config sets a dvar for each
// mod that the game has (t6ModDvar).
//
// Zombie Cap (zombies_mod_zombiecap 32): the round's zombies spawn while fewer are alive than level.zombie_ai_limit
// (maps/mp/zombies/_zm.gsc: round_spawning), which the level sets to 24. A map sets it again as it starts, and
// Origins lowers it for a while, to keep that many actors for something else: by 2 while three robots walk, by 1 for
// a step of its quest, and by the zombies of a generator's capture. Each is so many less than 24, so the mod adds
// its 8 to each, and the zombies and what the map keeps room for are 32. The script watches the limit, and adds the
// 8 to each value that the level sets. It tells the level's value from its own by the value being 24 or less. A
// value above is its own, or its own that the level counted up or down (Origins' quest), and it leaves that.
//
// A generator's capture is not left to the watching, which is a frame late: as the capture starts, the map sets the
// limit and at once deletes the round's zombies above it, one each network frame (maps/mp/zm_tomb_capture_zones.gsc:
// capture_event_handle_ai_limit), and it would delete one or two above 24 less the capture's before the script had
// added its 8. On Origins the script takes that function's place, and counts down from the mod's cap. It calls the
// map's functions by name (GetFunction), because another map has no such script for it to refer to.
//
// The level also waits while it has 31 actors and corpses (level.zombie_actor_limit), one less
// than the engine's 32 actors, and the script leaves that as it is: the zombies and every other actor are 31 at most,
// so that an actor that the level spawns outside the round's zombies, such as a boss, still has room.
//
// Unique Wall Weapons (zombies_mod_uniquewallweapons 1): a gun off the wall has one holder at a time. As in a T5
// game, the wall buy of a gun that another player holds shows a player no prompt, and can't be used. The level
// decides each tenth of a second who sees a wall buy's prompt (maps/mp/zombies/_zm_magicbox.gsc:
// decide_hide_show_hint, which a wall buy's trigger runs: _zm_weapons.gsc: weapon_spawn_think). The script takes that
// function's place, with what it does and the mod's check besides: the function is also the box's. A gun is
// another's when a player who is in the game holds it or its upgrade, or a gun that shares its ammo, or when it is in
// Pack-a-Punch. A player who bled out holds nothing. Only guns: every player starts with grenades. A player with the
// gun still sees the prompt, for its ammo. The purchase itself has no check, so two players who buy the same gun
// within that tenth of a second both get it.
const t6Script = `// zombies-server's (server/t6.go): the rounds, the scoreboard at each, the main quest done and its
// side, help from earlier games, game over, its end played out, in Grief each player's team, and the room's mods
main()
{
	if ( getdvar( "g_gametype" ) == "zgrief" )
		replaceFunc( GetFunction( "maps/mp/gametypes_zm/_globallogic_ui", "menuautoassign" ), ::lobby_autoassign, -1 );

	if ( getdvarint( "zombies_mod_uniquewallweapons" ) > 0 )
	{
		replaceFunc( GetFunction( "maps/mp/zombies/_zm_magicbox", "decide_hide_show_hint" ), ::lobby_wall_hint, -1 );
		printf( "zombies_lobby: mod uniquewallweapons 1" );
	}

	if ( getdvarint( "zombies_mod_zombiecap" ) > 24 && getdvar( "mapname" ) == "zm_tomb" )
		replaceFunc( GetFunction( "maps/mp/zm_tomb_capture_zones", "capture_event_handle_ai_limit" ), ::lobby_capture_limit, -1 );
}

init()
{
	level thread lobby_rounds();
	level thread lobby_egg();
	level thread lobby_help();
	level thread lobby_game_over();

	if ( getdvarint( "zombies_mod_zombiecap" ) > 24 )
	{
		level thread lobby_zombie_cap( getdvarint( "zombies_mod_zombiecap" ) - 24 );
		printf( "zombies_lobby: mod zombiecap " + getdvarint( "zombies_mod_zombiecap" ) );
	}
}

// Zombie Cap: adds more to each limit that the level sets for the zombies alive at once, which is 24 or less. The
// limit that the script set last is level.lobby_cap.
lobby_zombie_cap( more )
{
	level endon( "end_game" );

	for ( ;; )
	{
		if ( isdefined( level.zombie_ai_limit ) && level.zombie_ai_limit <= 24 )
		{
			if ( !isdefined( level.lobby_cap ) || level.zombie_ai_limit != level.lobby_cap )
				level.zombie_ai_limit = level.zombie_ai_limit + more;
		}

		level.lobby_cap = level.zombie_ai_limit;
		wait 0.05;
	}
}

// Zombie Cap, on Origins: in place of the map's capture_event_handle_ai_limit (maps/mp/zm_tomb_capture_zones.gsc),
// which does the same from a limit of 24.
lobby_capture_limit()
{
	script = "maps/mp/zm_tomb_capture_zones";
	needed = GetFunction( script, "calculate_capture_event_zombies_needed" );
	to_delete = GetFunction( script, "get_zombie_to_delete" );
	delete_it = GetFunction( script, "delete_zombie_for_capture_event" );
	level.zombie_ai_limit = getdvarint( "zombies_mod_zombiecap" ) - [[ needed ]]();
	level.lobby_cap = level.zombie_ai_limit;

	while ( maps\mp\zombies\_zm_utility::get_current_zombie_count() > level.zombie_ai_limit )
	{
		zombie = [[ to_delete ]]();

		if ( isdefined( zombie ) )
			zombie thread [[ delete_it ]]();

		maps\mp\zombies\_zm_utility::wait_network_frame();
	}
}

// Unique Wall Weapons: whether the player holds the weapon, its upgrade, or a weapon that shares its ammo.
lobby_wall_holds( weapon )
{
	if ( self maps\mp\zombies\_zm_weapons::has_weapon_or_upgrade( weapon ) )
		return true;

	if ( isdefined( level.weapons_using_ammo_sharing ) && level.weapons_using_ammo_sharing )
		return isdefined( self maps\mp\zombies\_zm_weapons::get_shared_ammo_weapon( weapon ) );

	return false;
}

// Unique Wall Weapons: whether the gun of a wall buy is another's than the player's: a player's who is in the game,
// or in Pack-a-Punch.
lobby_wall_taken( weapon, player )
{
	if ( !isdefined( weapon ) || weapontype( weapon ) != "bullet" )
		return false;

	if ( player lobby_wall_holds( weapon ) )
		return false;

	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		if ( players[i] != player && players[i].sessionstate == "playing" && players[i] lobby_wall_holds( weapon ) )
			return true;
	}

	machines = getentarray( "specialty_weapupgrade", "script_noteworthy" );

	for ( i = 0; i < machines.size; i++ )
	{
		held = maps\mp\zombies\_zm_weapons::get_base_weapon_name( machines[i].current_weapon, 1 );

		if ( isdefined( held ) && held == weapon )
			return true;
	}

	return false;
}

// Unique Wall Weapons: reports the first time that a wall buy of the gun is hidden from a player.
lobby_wall_hid( weapon, player )
{
	if ( !isdefined( level.lobby_wall_hid ) )
		level.lobby_wall_hid = [];

	if ( isdefined( level.lobby_wall_hid[weapon] ) )
		return;

	level.lobby_wall_hid[weapon] = 1;
	printf( "zombies_lobby: wall " + weapon + " " + player.name );
}

// Unique Wall Weapons: in place of the game's decide_hide_show_hint (maps/mp/zombies/_zm_magicbox.gsc), which does
// the same without lobby_wall_taken.
lobby_wall_hint( endon_notify, second_endon_notify, onlyplayer )
{
	self endon( "death" );

	if ( isdefined( endon_notify ) )
		self endon( endon_notify );

	if ( isdefined( second_endon_notify ) )
		self endon( second_endon_notify );

	if ( !isdefined( level._weapon_show_hint_choke ) )
		level thread maps\mp\zombies\_zm_magicbox::weapon_show_hint_choke();

	use_choke = 0;

	if ( isdefined( level._use_choke_weapon_hints ) && level._use_choke_weapon_hints == 1 )
		use_choke = 1;

	for ( ;; )
	{
		last_update = gettime();

		if ( isdefined( self.chest_user ) && !isdefined( self.box_rerespun ) )
		{
			user = self.chest_user;

			if ( maps\mp\zombies\_zm_utility::is_placeable_mine( user getcurrentweapon() ) || user maps\mp\zombies\_zm_utility::hacker_active() )
				self setinvisibletoplayer( user );
			else
				self setvisibletoplayer( user );
		}
		else
		{
			players = [];

			if ( isdefined( onlyplayer ) )
				players[0] = onlyplayer;
			else
				players = getplayers();

			for ( i = 0; i < players.size; i++ )
			{
				if ( lobby_wall_taken( self.zombie_weapon_upgrade, players[i] ) )
				{
					self setinvisibletoplayer( players[i], 1 );
					lobby_wall_hid( self.zombie_weapon_upgrade, players[i] );
				}
				else if ( players[i] maps\mp\zombies\_zm_magicbox::can_buy_weapon() )
					self setinvisibletoplayer( players[i], 0 );
				else
					self setinvisibletoplayer( players[i], 1 );
			}
		}

		if ( use_choke )
		{
			while ( level._weapon_show_hint_choke > 4 && gettime() < last_update + 150 )
				wait 0.05;
		}
		else
			wait 0.1;

		level._weapon_show_hint_choke++;
	}
}

lobby_number( value )
{
	if ( !isdefined( value ) )
		return 0;

	return value;
}

// A player's Grief team, by its letter in the game: 1 for A, 2 for B, and 0 for none or outside Grief.
lobby_team_number( team )
{
	if ( getdvar( "g_gametype" ) != "zgrief" || !isdefined( team ) )
		return 0;

	if ( team == "A" )
		return 1;

	if ( team == "B" )
		return 2;

	return 0;
}

lobby_stats()
{
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		player = players[i];
		doors = 0;

		if ( isdefined( player.pers ) && isdefined( player.pers["doors_purchased"] ) )
			doors = player.pers["doors_purchased"];

		line = "zombies_lobby: stats " + player getentitynumber();
		line = line + " " + lobby_number( player.score_total ) + " " + lobby_number( player.score );
		line = line + " " + lobby_number( player.kills ) + " " + lobby_number( player.headshots );
		line = line + " " + lobby_number( player.downs ) + " " + lobby_number( player.revives ) + " " + doors;
		line = line + " " + lobby_team_number( player._encounters_team );
		printf( line );
	}
}

// The game's team of the player in Grief: "axis" (A, team 1) or "allies" (B, team 2), as the server's config names
// them, or else the team with fewer players, the first when the two are even.
lobby_team()
{
	for ( team = 1; team <= 2; team++ )
	{
		names = strtok( getdvar( "zombies_team_" + team ), "/" );

		for ( i = 0; i < names.size; i++ )
		{
			if ( tolower( names[i] ) == tolower( self.name ) )
			{
				if ( team == 1 )
					return "axis";

				return "allies";
			}
		}
	}

	axis = 0;
	allies = 0;
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		if ( players[i] == self || !isdefined( players[i].pers["team"] ) )
			continue;

		if ( players[i].pers["team"] == "axis" )
			axis++;
		else if ( players[i].pers["team"] == "allies" )
			allies++;
	}

	if ( allies < axis )
		return "allies";

	return "axis";
}

// In Grief, in place of the game's menuautoassign (maps/mp/gametypes_zm/_globallogic_ui.gsc), which does the same
// with the team that it picks itself.
lobby_autoassign( comingfrommenu )
{
	team = self lobby_team();
	self maps\mp\gametypes_zm\_globallogic_ui::closemenus();

	if ( team == self.pers["team"] && ( self.sessionstate == "playing" || self.sessionstate == "dead" ) )
	{
		self maps\mp\gametypes_zm\_globallogic_ui::beginclasschoice();
		return;
	}

	if ( team != self.pers["team"] && ( self.sessionstate == "playing" || self.sessionstate == "dead" ) )
	{
		self.switching_teams = 1;
		self.joining_team = team;
		self.leaving_team = self.pers["team"];
		self suicide();
	}

	self.pers["team"] = team;
	self.team = team;
	self.pers["class"] = undefined;
	self.class = undefined;
	self.pers["weapon"] = undefined;
	self.pers["savedmodel"] = undefined;
	self maps\mp\gametypes_zm\_globallogic_ui::updateobjectivetext();
	self.sessionteam = team;

	if ( !isalive( self ) )
		self.statusicon = "hud_status_dead";

	self notify( "joined_team" );
	level notify( "joined_team" );
	self notify( "end_respawn" );
	self maps\mp\gametypes_zm\_globallogic_ui::beginclasschoice();
	self setclientscriptmainmenu( game["menu_class"] );
}

lobby_rounds()
{
	level endon( "end_game" );

	for ( ;; )
	{
		level waittill( "start_of_round" );
		lobby_stats();
		printf( "zombies_lobby: round " + level.round_number );
	}
}

lobby_egg()
{
	level endon( "end_game" );
	level thread lobby_egg_on( "transit_sidequest_achieved", 0, "sq_transit_last_completed" );
	level thread lobby_egg_on( "highrise_sidequest_achieved", 0, "sq_highrise_last_completed" );
	level thread lobby_egg_on( "pop_goes_the_weasel_achieved", 0 );
	level thread lobby_egg_on( "sq_richtofen_complete", 1 );
	level thread lobby_egg_on( "sq_maxis_complete", 2 );
	level thread lobby_egg_on( "tomb_sidequest_complete", 0 );
	level waittill( "lobby_egg", side );
	line = "zombies_lobby: egg " + side;
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
		line = line + " " + players[i] getentitynumber();

	printf( line );
}

// Waits for a map's main quest to be done. The side is the one given, or else the one that the map has just saved
// as its players' last, in the stat given.
lobby_egg_on( done, side, stat )
{
	level endon( "end_game" );
	level waittill( done );

	if ( isdefined( stat ) )
		side = lobby_side( stat );

	level notify( "lobby_egg", side );
}

// The side of a main quest that the map has saved as the last of the players who were in the game as the quest
// started: 1 for Richtofen's, 2 for Maxis's, or 0 when it saved none.
lobby_side( stat )
{
	players = getplayers();

	for ( i = 0; i < players.size; i++ )
	{
		if ( !isdefined( players[i].transit_sq_started ) && !isdefined( players[i].highrise_sq_started ) )
			continue;

		side = players[i] maps\mp\zombies\_zm_stats::get_global_stat( stat );

		if ( side == 1 || side == 2 )
			return side;
	}

	return 0;
}

// What the players have from earlier games, on a map with a bank, a weapon locker or persistent upgrades: watches
// each player in the game. The map has set the bank and the locker up by round 1 (TranZit as its rounds start:
// maps/mp/zm_transit_classic.gsc).
lobby_help()
{
	level endon( "end_game" );
	level waittill( "start_of_round" );

	if ( !isdefined( level.banking_map ) && !isdefined( level.weapon_locker_map ) && !isdefined( level.pers_upgrades_keys ) )
		return;

	for ( ;; )
	{
		wait 0.25;
		players = getplayers();

		for ( i = 0; i < players.size; i++ )
			players[i] lobby_help_check();
	}
}

// The gun in the player's weapon locker, as their saved stats have it, or "" for none. Outside an online game the
// locker keeps a gun for the one game, and has none from an earlier one.
lobby_locker_gun()
{
	if ( !isdefined( level.weapon_locker_map ) || !isdefined( level.weapon_locker_online ) || !level.weapon_locker_online )
		return "";

	gun = self maps\mp\zombies\_zm_stats::get_map_weaponlocker_stat( "name", level.weapon_locker_map );

	if ( !isdefined( gun ) || !isstring( gun ) )
		return "";

	return gun;
}

// Reports the persistent upgrades that the player has and the script has not reported of them yet: as ones they came
// to the game with ("had"), or as ones they earned in it ("got").
lobby_perks_report( how )
{
	if ( !isdefined( level.pers_upgrades_keys ) || !isdefined( self.pers_upgrades_awarded ) )
		return;

	for ( i = 0; i < level.pers_upgrades_keys.size; i++ )
	{
		name = level.pers_upgrades_keys[i];

		if ( isdefined( self.lobby_perks[name] ) )
			continue;

		if ( !isdefined( self.pers_upgrades_awarded[name] ) || !self.pers_upgrades_awarded[name] )
			continue;

		self.lobby_perks[name] = 1;
		printf( "zombies_lobby: perk " + how + " " + name + " " + self getentitynumber() );
	}
}

// Notes what the player has from earlier games a moment after the script first sees them playing, once the bank has
// their balance and the level has given them their persistent upgrades. From then on it reports the first time they
// have less of it, fewer points in the bank, or another gun or none in their weapon locker, and each persistent
// upgrade that they earn.
lobby_help_check()
{
	if ( !isdefined( self.lobby_bank ) )
	{
		if ( self.sessionstate != "playing" )
			return;

		if ( isdefined( level.banking_map ) && !isdefined( self.account_value ) )
			return;

		if ( !isdefined( self.lobby_seen ) )
		{
			self.lobby_seen = 1;
			return;
		}

		self.lobby_bank = lobby_number( self.account_value );
		self.lobby_gun = self lobby_locker_gun();
		self.lobby_perks = [];
		self lobby_perks_report( "had" );
		return;
	}

	self lobby_perks_report( "got" );

	if ( self.lobby_bank > 0 && lobby_number( self.account_value ) < self.lobby_bank )
	{
		self.lobby_bank = 0;
		printf( "zombies_lobby: help bank " + self getentitynumber() );
	}

	if ( self.lobby_gun != "" && self lobby_locker_gun() != self.lobby_gun )
	{
		self.lobby_gun = "";
		printf( "zombies_lobby: help locker " + self getentitynumber() );
	}
}

lobby_game_over()
{
	level waittill( "end_game" );
	lobby_stats();
	line = "zombies_lobby: over " + lobby_number( level.round_number );

	if ( isdefined( level.gamemodulewinningteam ) )
		line = line + " " + lobby_team_number( level.gamemodulewinningteam );

	printf( line );
	level waittill( "stop_intermission" );
	printf( "zombies_lobby: exit" );
}
`

// t6Prepare makes the game's home a Plutonium folder, which the game runs from. It first brings the server's copy of
// Plutonium up to date, unless another game runs from the copy (enter). Everything the game only reads is a link to
// that copy, so that a game that is taken over can change nothing that another game runs. The home's own files are
// what the game writes (its logs, in main), its two config files, and the server's script (t6Script).
//
// The first config, lobby.cfg, sets the game up. The game's command line execs it. It sets how many players join the
// game and with what password. It gives the remote console a password that only the server has. The remote console
// has no rate limit, because the server asks for the status each second, and its use is not printed (rcon_prints 0).
// The config turns off votes and recording. It also sets how long the game waits for its players. Plutonium's script
// (scripts/zm/ranked.gsc) starts the game without the missing players sv_connectTimeout after the first is in, and
// they then watch round 1 as spectators. sv_connectTimeout is 90 seconds unless set. The config sets it to the
// server's own wait (t6Game.waits), so that the game starts with everyone or the server calls it off. The value is
// at least those 90 seconds, because the game also drops a client that loads for longer, and at most 1800, the most
// the game accepts.
//
// In Grief, the config also names the players of each team (t6Game.teams), for the server's script (t6Script). The
// names are separated by slashes, which no name has (rooms.go: nameUnsafe). The config also sets a dvar for each mod
// that the game has (t6ModDvar), which the script reads.
//
// The second config, lobby_gts.cfg, has the game settings of the map, as Plutonium's server configs have them.
func (g *game) t6Prepare(ctx context.Context) error {
	t := g.t6()
	m, ok := t6Maps[t.mapName]
	if !ok {
		return fmt.Errorf("%s is no T6 map", t.mapName)
	}
	if err := t.enter(ctx); err != nil {
		return err
	}
	if t.updated.Store(true); t.changed != nil {
		t.changed()
	}
	copyDir, err := filepath.Abs(t.pluto.Dir)
	if err != nil {
		return err
	}
	storage := filepath.Join(g.home, "storage", "t6")
	scripts := filepath.Join(storage, "scripts", "zm")
	for _, dir := range []string{filepath.Join(g.home, "storage"), storage, filepath.Join(storage, "gamesettings"),
		filepath.Dir(scripts), scripts} {
		if err := g.mkdir(dir); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(scripts, "lobby.gsc"), []byte(t6Script), 0o644); err != nil {
		return err
	}
	links := []string{"bin", "games", "storage/demonware"}
	entries, err := os.ReadDir(filepath.Join(copyDir, "storage", "t6"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !slices.Contains(t6Own, e.Name()) {
			links = append(links, "storage/t6/"+e.Name())
		}
	}
	for _, name := range links {
		name = filepath.FromSlash(name)
		if err := os.Symlink(filepath.Join(copyDir, name), filepath.Join(g.home, name)); err != nil {
			return err
		}
	}
	cfg := fmt.Sprintf(`// zombies-server's, for room %s
sv_maxclients %d
zombies_minplayers %d
sv_connectTimeout %d
g_password "%s"
rcon_password "%s"
rcon_rate_limit 0
rcon_prints 0
g_allowVote 0
demo_enabled 0
sv_hostname "Room %s"
sv_maprotation "execgts lobby_gts.cfg map %s"
`, g.name, t.size, t.players, min(max(int(t.waits.Seconds()), 90), 1800), t.password, t.rcon, g.name, m.zone)
	for i, names := range t.teams {
		if len(names) > 0 {
			cfg += fmt.Sprintf("set zombies_team_%d \"%s\"\n", i+1, strings.Join(names, "/"))
		}
	}
	for _, name := range t.mods {
		dvar, value := t6ModDvar(name)
		cfg += fmt.Sprintf("set %s \"%s\"\n", dvar, value)
	}
	gts := fmt.Sprintf(`execgts gamesettings_default.cfg
execgts gamesettings_%s.cfg
ui_zm_gamemodegroup "%s"
ui_gametype "%s"
g_gametype "%s"
ui_zm_mapstartlocation "%s"
`, m.gametype, m.group, m.gametype, m.gametype, m.location)
	// The configs hold the passwords. Only the server and the game, which runs in the server's group, can read them.
	if err := os.WriteFile(filepath.Join(storage, "lobby.cfg"), []byte(cfg), 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(storage, "gamesettings", "lobby_gts.cfg"), []byte(gts), 0o640)
}

// t6Args returns the game's command line after the wine command. Plutonium's bootstrapper, run from the game's home,
// starts T6's Zombies on the game files as a dedicated server in LAN mode. The game listens on its port, and only on
// the address g.ip when that is set (127.0.0.1 for a room's game).
func (g *game) t6Args() []string {
	args := []string{filepath.FromSlash(bootstrapper), "t6zm", winePath(g.dir), "-dedicated", "-lan",
		"+set", "net_port", strconv.Itoa(g.port)}
	if g.ip != "" {
		args = append(args, "+set", "net_ip", g.ip)
	}
	return append(args, "+exec", "lobby.cfg", "+map_rotate")
}

// Plutonium's server prints in colour, and it prints its prompt before each line.
var (
	ansiCode    = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	t6Prompt    = regexp.MustCompile(`^(Plutonium r\d+ > ?)+`)
	t6StatusRow = regexp.MustCompile(`^\s*\d+\s+-?\d+\s`)
)

// t6Line returns a line of the game's output as the log has it: the text without the colours and the prompt.
func t6Line(line string) string {
	return strings.TrimSpace(t6Prompt.ReplaceAllString(strings.TrimSpace(ansiCode.ReplaceAllString(line, "")), ""))
}

// t6StatusLine parses a line of the game's answer to the status command. status reports whether the line is part of
// that answer at all. client reports whether the line lists a client, which is then c:
//
//	map: zm_transit
//	num score bot ping guid                             name             lastmsg address               qport  rate
//	--- ----- --- ---- -------------------------------- ---------------- ------- --------------------- ------ -----
//	  0  1500   0   32 9240                             Ann                    0 127.1.0.1:28960        -1234 25000
//
// A client's ping is a number once the client is in the game. Until then the column shows how far the client is
// (CNCT), and it shows ZMBI for a client that is leaving. A client's name may have spaces: the name is what is left
// between the guid and the last four columns.
func t6StatusLine(line string) (c t6Client, client, status bool) {
	switch {
	case strings.HasPrefix(line, "map:"), strings.HasPrefix(line, "num score"), strings.HasPrefix(line, "--- ---"):
		return c, false, true
	case !t6StatusRow.MatchString(line):
		return c, false, false
	}
	f := strings.Fields(line)
	if len(f) < 8 {
		return c, false, false
	}
	address, err := netip.ParseAddrPort(f[len(f)-3])
	if err != nil {
		return c, false, true // a bot's row, or a row with no address yet
	}
	c.slot, _ = strconv.Atoi(f[0])
	c.address = address.String()
	if ping, err := strconv.Atoi(f[3]); err == nil && ping < 999 {
		c.in = true
	}
	if len(f) > 9 {
		c.name = strings.Join(f[5:len(f)-4], " ")
	}
	return c, true, true
}

// t6Watch is a T6 game's watch (engine.watch) and also its console. Plutonium's server reads no console on stdin
// under Wine, so the server talks to the game's remote console. It does so from watchAddr, the address it also
// watches a T5 game from. t6Watch asks for the game's status every watchEvery and passes the clients it lists to
// t6Game.status. It sends the game the lines that game.command queues, and prints the game's answers to them. The
// status also shows whether the game still answers. The game answers while it runs a map, and not while it loads one.
// When the game has not answered for startTimeout after its start, or for quietTimeout after an answer, t6Watch sends
// the length of the silence on silent and returns.
func (g *game) t6Watch(done <-chan struct{}, silent chan<- time.Duration) {
	t := g.t6()
	conn, err := net.DialUDP("udp4", watchAddr(g.port), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: g.port})
	if err != nil {
		g.logf("no remote console, which a T6 game is run by: %v", err)
		silent <- 0
		return
	}
	defer conn.Close()
	var mu sync.Mutex
	var answered time.Time // when the game last answered
	var clients []t6Client // the clients its status has listed since the server last asked
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
			text, ok := bytes.CutPrefix(buf[:n], []byte("\xff\xff\xff\xffprint\n"))
			if !ok {
				continue
			}
			var say []string
			mu.Lock()
			answered = time.Now()
			for _, line := range strings.Split(string(text), "\n") {
				line = strings.TrimRight(line, "\r\x00")
				c, client, status := t6StatusLine(line)
				if status {
					listed = true
				}
				if client {
					clients = append(clients, c)
				}
				if line != "" && (!status || show) {
					say = append(say, line)
				}
			}
			mu.Unlock()
			for _, line := range say {
				fmt.Fprintf(g.output, "%s\n", line)
			}
		}
	}()
	ask := func(line string) {
		conn.Write(append(append([]byte{}, outOfBand...), "rcon "+t.rcon+" "+line...))
	}
	start := time.Now()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	ask("status")
	for {
		select {
		case <-done:
			return
		case line := <-t.send:
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
		if ok && t.status != nil {
			t.status(now)
		}
		since, limit := start, g.startTimeout
		if !last.IsZero() {
			since, limit = last, g.quietTimeout
		}
		if quiet := time.Since(since); limit > 0 && quiet >= limit {
			silent <- quiet
			return
		}
		ask("status")
	}
}

// ---- a T6 room's game. The caller holds l.mu unless a comment says otherwise. ----

// t6Status handles the clients that the status of a T6 room's game lists, each second that the game answers
// (t6Watch). The caller does not hold l.mu. The game is up from its first answer, and its players can join from then.
// A seat's player is in the game while the status lists a client at the seat's address, and the seat has that
// client's slot. The game has begun once everyone with a seat is in, because the game waits for that many players
// (zombies_minplayers). Its level starts round 1 some seconds after that, and the game's clock counts from the
// level's report of it (roundBegins).
func (l *lobby) t6Status(r *room, rg *roomGame, clients []t6Client) {
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
	// plays under (t6Whose). The game's connect said the name, when it had one. The status says it again here, for a
	// game whose connect did not.
	for _, c := range clients {
		i := slices.IndexFunc(rg.seats, func(s *seat) bool { return s.local.String() == c.address })
		if i >= 0 && rg.t6Whose(r.code, rg.seats[i], c.name) != rg.seats[i] {
			changed = true
		}
	}
	in, joined := 0, false
	for _, s := range rg.seats {
		i := slices.IndexFunc(clients, func(c t6Client) bool { return c.address == s.local.String() })
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
		l.relay.wakeUp() // the game has a client more: the next player's turn to join may have come (t6Turn)
	}
	if changed {
		l.checkGame(r)
		l.change()
	}
}

// t6Whose returns the seat of the player whose game is at the address of the seat at, going by name, the name the
// game plays under. Several players can be behind one address. A game from that address got the first of their seats
// that was free, whichever of them started it (t6Seat). The game plays under the name that its player's page gave it
// (t6View), and no one else in the room's game has that name. When the name is that of another player at the same
// address, the two seats swap their addresses from now on, and what the server knows of the clients at them. Each
// player then has the address of their own game, and with it what the game reports about them. t6Whose returns at
// when the name is its player's or no such player's. The caller holds l.mu.
func (rg *roomGame) t6Whose(code string, at *seat, name string) *seat {
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

// t6Said reads what a T6 room's game prints: the lines of the server's script in it (t6Script), and one line of the
// game itself. The caller does not hold l.mu.
//
// Round 1 has begun once the script reports a round, whatever the status has listed of the players. The game decides
// when round 1 begins: with everyone in, or without the rest once Plutonium's own script has waited long enough for
// them (scripts/zm/ranked.gsc). The game's clock starts at that report, also when the status had everyone in before
// it (roundBegins). A game that has begun is not called off. Each round sets its players' records, as in a T5 game
// (keepRounds). The records come from the players' lines of the scoreboard. The script prints those by client slot,
// so they count only for the clients that the status has listed by then. The end of the map's main quest sets the
// easter egg's record of the players in the game then, whom the script names by slot too (t6Egg). What a player has
// from earlier games, which the script reports as well, is an event of the game (t6Carried).
//
// The game ends when the level ends: at game over, or 20 seconds after its last player left. At game over the room
// gets the game's result, which the players' pages show while the players watch the game's end. A game of Grief or
// Turned has a winner then (result), and its rounds are no records (keepRounds). Once the script
// reports that the end has played out, the server stops the game. That is a moment before the game would load the map
// again for its players, who are to be back in their room instead. The game itself prints "map_rotate..." when it
// rotates to its next map, which is the same map. That line ends the game too, for when the script did not report the
// end. The level also ends before round 1: when players come and go while they wait for the rest, and when the game
// starts. The room's game carries on then.
func (l *lobby) t6Said(r *room, rg *roomGame, line string) {
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
			l.t6Egg(r, rg, strings.Fields(arg))
		}
		return
	case ours && (verb == "help" || verb == "perk"):
		if rg.ending == "" {
			l.t6Carried(r, rg, verb, strings.Fields(arg))
		}
		return
	case ours && verb == "over" && rg.ending == "" && over == "Game over.":
		// The players watch the game's end. The script reports when the end has played out ("exit"). If it doesn't, the
		// server stops the game anyway after t6EndTakes. The script reports the round, and in Grief the team that won.
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
		time.AfterFunc(t6EndTakes, rg.cancel)
	case ours && verb != "over" && verb != "exit":
		return
	default: // the end has played out, or no one is left to watch it, or the game rotates its map
		l.stop(r, over)
		rg.cancel()
	}
	l.change()
}

// t6Egg keeps the easter egg of rg's map for the players whose client is in one of the slots: the script's report
// that the map's main quest is done ("egg 2 0 1"), with the side that the players took and the players in the game
// then. The whole party has the one time, from round 1, as in a T5 game (t5.go: gameLine). The game has an event of
// each of those players' easter egg, with the side (gameEvent). The caller holds l.mu.
func (l *lobby) t6Egg(r *room, rg *roomGame, fields []string) {
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

// t6Perk is the name of a persistent upgrade as the game's scripts have it, such as jugg or multikill_headshots.
var t6Perk = regexp.MustCompile(`^[a-z_]{1,32}$`)

// t6Carried notes what a player of rg's game has from earlier games, as the script reports it, as an event of the
// game (gameEvent). "help bank 1" and "help locker 1": the player in the slot took points out of the bank, or the gun
// out of their weapon locker. "perk had jugg 1" and "perk got jugg 1": they came to the game with the persistent
// upgrade, or earned it in the game. The caller holds l.mu.
func (l *lobby) t6Carried(r *room, rg *roomGame, verb string, fields []string) {
	var kind, what, slot string
	switch {
	case verb == "help" && len(fields) == 2 && (fields[0] == eventBank || fields[0] == eventLocker):
		kind, slot = fields[0], fields[1]
	case verb == "perk" && len(fields) == 3 && fields[0] == "had" && t6Perk.MatchString(fields[1]):
		kind, what, slot = eventPerk, fields[1], fields[2]
	case verb == "perk" && len(fields) == 3 && fields[0] == "got" && t6Perk.MatchString(fields[1]):
		kind, what, slot = eventPerkEarned, fields[1], fields[2]
	default:
		return
	}
	s := rg.seatIn(slot)
	if s == nil || !rg.note(rg.sinceRound1(), kind, s.player, what) {
		return
	}
	log.Printf("server: in room %s's game, %s: %s", r.code, s.player.name, strings.TrimSpace(kind+" "+what))
	l.keepEvents(rg)
}

// t6EndTakes is how long the server gives a T6 game's end to play out before it stops the game anyway. The end takes
// 20 seconds.
const t6EndTakes = time.Minute

// t6Seat returns where the relay sends a T6 client that reached it from ip. verb is the first word of the client's
// first packet. The client goes to a seat in a T6 room's game: game is the game's address, and local is the seat's
// address, which the game sees the client at (relay.go). A T6 client can't identify its player the way a T5 client
// does with the player's key, so the server goes by the client's address. It picks the seat of a player whose page
// calls from ip (player.addr), in a game that accepts players.
//
// Several players can call from ip (a household behind one address). t6Seat then picks a seat that has no client. If
// each seat has one, it picks the seat whose client has been quiet the longest, for at least t6Requiet: that client's
// player has started their game again. Players of one room are told apart later, by the name a client plays under:
// in its connect (t6Turn), and once the room's game lists it (t6Status). Both are clients of the one game.
//
// Players of different rooms can call from ip too, and nothing in a client's packets says which room's game it is
// to join. A client that got a seat in the wrong room's game would ask that game to let it in with the other room's
// password. So the server has the players' launchers send their games one room after the other (t6Waits), and of
// the seats that have no client, t6Seat picks the one whose player's launcher was sent the game last. A player who
// joins by hand, with the console line of their room's page, has no such order: with a player of another room at
// their address who joins in the same moments, their game can get the other's seat, and then neither joins until
// they try again.
//
// ok is false when there is no such seat. While a T6 game accepts players, the server then logs the address that
// asked. This is how a player's game fails to join when it reaches the server from another address than the player's
// page.
func (l *lobby) t6Seat(ip netip.Addr, verb string) (game, local *net.UDPAddr, name, who string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var best *seat
	var room *room
	bestQuiet := time.Duration(-1)
	running := ""
	for _, r := range l.rooms {
		rg := r.game
		if t6Of(rg) == nil || rg.ending != "" || rg.up.IsZero() {
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
			case quiet < t6Requiet:
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

// t6Waits reports whether p's launcher is to wait before it has p's game join their room's T6 game (launcherPlay).
// It waits while the game of another player at p's address is on its way into another room's T6 game: whose is that
// player and their room, for the log. The relay then has one game to expect from the address, and gives it the seat
// of the player it belongs to (t6Seat).
//
// A player's game is on its way from when their launcher was sent their room's game until a game from their address
// has a seat in it. It can get no seat before the room's game is up. From then, or from when the launcher was sent
// the game if that was later, it has t6Way to come. A game that takes longer is waited for no more, and p's game may
// then get its seat. Nor is a game waited for while its launcher is still getting its PC ready, which can take any
// time (preparing). A launcher that waits is sent no game, so the wait goes one way: of two players at one address,
// the one whose launcher was sent their game first has their game join first. The caller holds l.mu.
func (l *lobby) t6Waits(p *player) (waits bool, whose string) {
	if !p.addr.IsValid() {
		return false, ""
	}
	for _, r := range l.rooms {
		rg := r.game
		if r == p.room || t6Of(rg) == nil || rg.ending != "" {
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
			if rg.up.IsZero() || time.Since(since) < l.t6Way {
				return true, fmt.Sprintf("%s, room %s", s.player.name, r.code)
			}
		}
	}
	return false, ""
}

// t6Requiet is how long a T6 client must have sent nothing before another client from its address can take its seat.
const t6Requiet = 3 * time.Second

// t6Turn reports whether the T6 client at a seat's address may join its room's game now. The relay asks for each
// connect of a client that the game has not accepted, and again while the connect waits (relay.holds, relay.letIn).
// name is the name the client plays under, from its connect, or "". The client's seat is its player's by that name,
// when several players are behind one address (t6Whose). who is that player and their room, for the log, or "" when
// the address is no seat's.
//
// The players join in the order of their places in the room, which is the order of the game's seats, so that each
// has their room's colour in the game (the comment at the top). A client may join once no player before its own is
// awaited (t6Awaited). Once round 1 has begun, a client joins at once: the game has given its slots, and a player
// who comes back to it is not kept out of it for a player who may not come back.
func (l *lobby) t6Turn(local *net.UDPAddr, name string) (turn bool, who string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at := local.String()
	for _, r := range l.rooms {
		rg := r.game
		if t6Of(rg) == nil {
			continue
		}
		i := slices.IndexFunc(rg.seats, func(s *seat) bool { return s.local.String() == at })
		if i < 0 {
			continue
		}
		if rg.started || rg.ending != "" {
			return true, fmt.Sprintf("%s, room %s", rg.seats[i].player.name, r.code)
		}
		whose := rg.t6Whose(r.code, rg.seats[i], name)
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
			if l.t6Awaited(s) {
				return false, who
			}
		}
	}
	return true, "" // the seat's game has ended
}

// t6Awaited reports whether the players after s in the room wait to join their T6 game until s has joined it. They
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
func (l *lobby) t6Awaited(s *seat) bool {
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

// t6Late calls off a T6 room's game that has waited loadTimeout for its players, when not everyone with a seat is
// in, as for a T5 game. The wait counts from when the game was up. It starts again at the last moment that the
// launcher of a missing player was still getting what their PC lacks (preparing). A first download of the game files
// takes far longer than any wait, and the player's game then has the whole wait to start and load in. Meanwhile the
// game begins without them after its own wait (t6Prepare), and they can join at a later round.
func (l *lobby) t6Late(r *room, rg *roomGame) {
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
// X-Forwarded-For. The player's T6 game reaches the relay from the same address (t6Seat), because the page is open on
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
