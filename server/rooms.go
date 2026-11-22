package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// maxRoomSize is the largest value of -max-players, the most players that a server's rooms may hold. The levels were
// made for four players. In a game of more than four, the engine changes the levels' scripts so that they accept a
// fifth and later players (bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrMorePlayers).
const maxRoomSize = 8

// defaultRoomSize is how many players a new room takes: the four that the levels were made for. A server whose rooms
// can't be of four uses the nearest size they can be (newRoomSize). The room's host can set another size (setRoom).
const defaultRoomSize = 4

// lobby holds the players the server knows, their sessions and the rooms. Players on the page group into rooms, pick
// a map and start a co-op game together. Each room's game is a separate run of a dedicated server (game.go) on its
// own port. The relay sends the room's players to that port (relay.go). A player's page joins the game with the token
// the server gave the player, in the path of its WebTransport session (/play/<token>).
//
// A room can be private. A private room is listed without its code, and can be joined only with its code or with a
// link that has the code (/?room=<code>). A new room is listed at once, but as private until it is privateFor old, so
// that its maker can make it private before anyone sees its code. The code works from the start. The player who made
// a room is its host, and the host picks the map. When the host leaves, the member who joined next becomes the host.
// A room takes room.size players, four unless its host sets another size. The size is at least the number of players
// a game needs (-min-players) and at most the number the server's rooms may hold (-max-players).
//
// Each player in a room reports in their page that they are ready (POST /lobby/ready). Once all of them are ready,
// the room's game starts after a countdown (checkReady). The countdown is called off when one of them is no longer
// ready. When a player joins or leaves, or the host picks another map, everyone in the room becomes unready and has
// to report again (unready). A player in a browser can be ready only once their page has the room's map, which means
// all of the map's game files in the browser's store (missing). The room shows its other members who lacks the map
// and how far their download is. While all of the server's games are running, no one may be ready for another game
// (full). A player plays in their page, or on Windows while their Windows client is open (onWindows). Closing the
// Windows client makes the player unready.
//
// A room's game belongs to the room's members. Each of them has a seat in it, and the room takes no one else while
// the game runs. The game waits for all of them before round 1 (bo1_players,
// bo1-zombies/src/server_mp/sv_lobby_mp.cpp). When one of them fails to load, or they are not all in within
// loadTimeout, the game is called off and everyone is back in the room. Once they are all in, round 1 waits only for
// the players who are still watching their loading movie, which ends by itself. Once round 1 has begun, the game
// takes no new player.
//
// A player plays in their page, or on Windows (Play.bat <host> <key>, with the key their page shows them). Either way
// they play from their seat. The relay gives a seat the same address at each visit (seat.local), so that the game
// recognizes the player. A player who drops or quits the game can take their character back, and closing the game
// counts as quitting it. The character waits in the game for keepCharacter (sv_timeout), and then the game removes
// it. A player who leaves the room gives up their character. The game ends at game over, or once no character is
// left in it (everyone left it, or was away from it for keepCharacter), or when the room's host ends it while no one
// plays it (endGame). Until then it keeps running, so that its players can come back to it. When it has ended,
// everyone is back in the room, and the room shows how the game ended.
//
// A room on a Black Ops II map (a T6 map: mapEntry.of) has Plutonium's server for its game. Its players join that
// game from their own copy of Black Ops II, not in their page (t6.go). Everything else works as described above,
// except for what t6.go describes.
//
// The page calls the server over HTTP with JSON, on the port that serves the page (web.go). Each call identifies the
// player by the token the server gave them, which the page stores. The token goes in an "Authorization: Bearer
// <token>" header and not in the URL. A proxy in front of the server would log the URL, and anyone who read its logs
// could then act as the player.
//
//	POST /lobby/hello   {"name"} -> {"token", "id", "name", "key"}: identifies the player by their token, or makes a
//	                    new player when the call has no token or the server doesn't know the token (players.go).
//	                    The answer's name is the name they asked for, in the form the server accepts (cleanName).
//	                    The name their room's game knows them by can differ (player.name). The key is their key
//	                    for Play.bat. Where players sign in with Discord (discord.go), the page's cookie identifies
//	                    the player instead, their name is their name on Discord, and the answer has "discord": true.
//	                    A player who is not signed in gets {"signIn": "/auth/discord"}, and the page sends them
//	                    there to sign in.
//	GET  /lobby/events  streams the state of the server as the player sees it (view), with an event each time it
//	                    changes: the Zombies maps (and which of them the server has), the pictures a player may
//	                    pick, the rooms, and the player's own room, with how its last game went when that game
//	                    ended at game over (gameResult)
//	GET  /lobby/leaderboards  the leaderboards of the players the server knows (leaderboards.go)
//	POST /lobby/create {"map", "private"}: makes a room for the player, who leaves any other room first. The room
//	                    is on the server's default map unless "map" names another.
//	POST /lobby/join    {"room"}: joins a room by its code
//	POST /lobby/leave   {}
//	POST /lobby/room    {"map", "private", "mods", "size"}: the host picks the room's map (one the server has), makes
//	                    the room private or public, sets the mods of its games (mods.go), or sets how many players
//	                    it takes
//	POST /lobby/mark    {"map"}: a player who isn't the host suggests a map for the room's next game, or none with ""
//	POST /lobby/files   {"map", "done", "running", "got", "need"}: reports which game files of a map the player's
//	                    page has. A browser plays from the files in its store (web/public/store.js, files.go). "done"
//	                    means the page has all of the map's files. "running" means it is downloading the rest,
//	                    and "got" is the number of bytes it has so far out of the "need" bytes it needs. The page
//	                    reports this for its room's map each time it changes.
//	POST /lobby/ready   {"ready"}: the player is ready to play the room's next game, or no longer ready (isReady).
//	                    Once all of the room's players are ready, the game starts after the countdown. A player can
//	                    be ready only once their page has all of the room's map, or they play on Windows (missing).
//	POST /lobby/gamepad {"gamepad"}: the player's page reads a gamepad the game can use, or no longer does. Their
//	                    room shows it on the player while a page of theirs is open (setGamepad).
//	POST /lobby/rejoin  {}: the player goes back into the room's game, to their character
//	POST /lobby/end     {}: the host ends the room's game, which no one plays (endGame)
//	POST /lobby/ended   {"why", "text"}: the player's game ended in their page, and why (web/index.html)
//	POST /lobby/waited  {"text"}: the page's report of how long it waited for pipelines before a game, for the
//	                    log (waited)
//	POST /lobby/picture {"picture"}: the player picks their picture by its id, from the pictures of the set that the
//	                    server has (pictures.go). With "", their avatar on Discord is their picture again.
//	POST /lobby/refresh {}: reads the player's name from Discord again, now, where players sign in with Discord
//	POST /lobby/signout {}: signs the page out, where players sign in with Discord
//
// The answer to a POST is {} or {"error": "..."}, and the page shows the error. The status is 401 for a token the
// server doesn't know.
type lobby struct {
	roomSize    int                 // the most players a room may take (room.size), and its game's sv_maxclients
	minPlayers  int                 // players a room's game needs, at least 1 (a player alone can play offline)
	defaultMap  string              // the map of a new room
	maps        func() []string     // returns the Zombies maps that the game files have
	mapDesc     func(string) string // returns a map's description in the game's map select, or "" (mapart.go)
	gamePorts   []int               // the games' ports on 127.0.0.1, one for each game that can run at once
	loadTimeout time.Duration       // how long a game waits for its players before it is called off
	sweepEvery  time.Duration       // how often sweep checks for players who are gone and games to end
	countdown   time.Duration       // how long a room's game waits to start once everyone in the room is ready
	privateFor  time.Duration       // how long a new room is listed as private, so that its maker can make it private
	titles      []title             // the titles the server hosts (title.go); a map it doesn't list gets the first
	files       *fileLists          // the lists of the files each map reads (files.go)
	relay       *relay              // the relay, which gives the seats their addresses
	ctx         context.Context

	discord *discordAuth // players sign in with Discord (discord.go); when nil, anyone plays under a name they pick

	mu       sync.Mutex
	players  map[string]*player     // every player the server knows, by id (players.go)
	sessions map[string]*session    // the sessions, by the hash of their token (tokenHash)
	keys     map[string]*player     // the players, by key (windowsSeat)
	rooms    map[string]*room       // the rooms, by code
	reports  map[string]*gameResult // the reports of the games that a record names, by ID (keepGame)
	busy     map[int]bool           // ports held by games, from start until they have quit; a game can outlive its room
	changed  chan struct{}          // closed and replaced at each change, to wake the event streams
	games    sync.WaitGroup
	// The pictures of the set that the server has, once it has made them (pictures.go: setPictures). pictures has
	// their ids in the set's order, and picturesShown has them in the form the page receives. picturesGiven has the
	// ones the server gives to players. picturesMade counts the calls of setPictures.
	pictures      []string
	picturesShown []pictureView
	picturesGiven []string
	picturesMade  int
	// store is the database that keeps the players (players.go), or nil for none. unsaved is set when the players
	// have changed since they were written to it. unsavedGames has the reports that it lacks, by ID (keepGame).
	store        *store
	unsaved      bool
	unsavedGames map[string]*gameResult
	writing      sync.Mutex
	expired      time.Time // when sweep last called expire, which ends the sessions unused for sessionIdle
}

const (
	nameLength = 15              // the most characters a player's name can have
	leaveAfter = 2 * time.Minute // a player away this long leaves their room, unless they have a character in its game
	// forgetUnopened is how long a player in no room can be away before the server forgets them, when their page
	// never opened its event stream. A page opens the stream as soon as it has called hello, so a hello that no stream
	// follows comes from a script. Forgetting these players keeps scripts from filling the server with players. The
	// server keeps the other players (players.go).
	forgetUnopened = time.Minute
	// keepCharacter is how long a room's game keeps the character of a player who dropped, so that they can take it
	// back. It is the game's sv_timeout (main.go).
	keepCharacter = 5 * time.Minute
	// clientAsks is how long a Windows player is not away after their client last asked for their room's game. A
	// client that waits for the game asks every 3 seconds.
	clientAsks = 10 * time.Second
	keepAlive  = 20 * time.Second // an idle event stream sends a comment this often, so that proxies keep it open
	// seatPort is the port of every seat's address. A returning player is at the same address and port, which is how
	// the game recognizes a client (bo1-zombies/src/client_mp/sv_client_mp.cpp: SV_DirectConnect).
	seatPort = 28960
	// keptStats is the most stats the server keeps for a player, as many as the game keeps. The level's scripts name
	// about 45.
	keptStats = 96
)

var (
	statName         = regexp.MustCompile(`^[A-Za-z0-9_]{1,63}$`)
	collectiblesForm = regexp.MustCompile(`^[01]{1,49}$`)
)

type player struct {
	id string // identifies the player to the other players
	// key is the player's key for Play.bat. A Windows client sends it over UDP to get the player's seat (relay.go).
	// The key is not one of the player's tokens (players.go). A token would be sent unencrypted over UDP, and anyone
	// who saw it could call the server as the player.
	key string
	// name is the name the player has in their room's game. It is their own name (asked), with a number added when
	// another player in the room has the same name, because the game accepts each name only once (rename). Only the
	// game uses it. The page shows a player by their own name.
	name string
	// asked is the player's own name: the name they asked for in their page, or their name on Discord, in the form
	// the game accepts (cleanName). Two players may have the same name.
	asked string
	made  time.Time
	// discord is the player's Discord account, when they signed in with it (discord.go).
	discord *discordUser
	// picture is the id of the player's picture of the set (pictures.go), or "" for none. picked is set when they
	// picked it themselves (setPicture), and not when the server gave it to them (givePicture).
	picture string
	picked  bool
	room    *room
	streams int       // the number of their open event streams; a page of theirs is open while it is above 0
	opened  bool      // a page of theirs opened an event stream, or they signed in with Discord; the server keeps them
	gone    time.Time // when their last event stream closed
	client  time.Time // when their Windows client last asked for their room's game (lobby.windowsSeat)
	played  time.Time // when their Windows client was last seen playing in their room's game (lobby.away)
	shown   presence  // the presence their room was last shown (updatePresence)
	// addr is the address that the player's page or launcher (launcher.go) last called the server from (t6.go:
	// pageAddr). Their T6 game reaches the relay from the same address.
	addr  netip.Addr
	files fileReport // the last report of their page about which game files of a map it has
	// launcher is how the download of T6's game files by the player's launcher goes, as the launcher last reported
	// (launcher.go).
	launcher launcherReport
	// ready is set when the player reports in their page that they are ready to play their room's next game. It
	// stays set until that game has ended, they leave the room, or their page has been closed for a while (isReady).
	ready bool
	// gamepad is set when the player's page reports that it reads a gamepad the game can use (setGamepad). It stays
	// set until no page of theirs is open.
	gamepad bool
	// mark is the map the player suggests to their room's host for its next game (setMark), or "" for none.
	mark string
	// waitedSaid is when the player's page last reported how a wait for pipelines went (waited).
	waitedSaid time.Time
	// stats and collectibles are what the room's games keep for the player from game to game, as the single-player
	// game's profile did (bo1-zombies/src/game_sp/g_sp_stats.cpp). stats has their zombie stats by name. collectibles
	// is in the game's format: '1' at index n - 1 for collectible n. Each game they play gets both when it starts
	// (keptOf) and reports each change (gameLine). The server keeps them for as long as it knows the player.
	stats        map[string]int
	collectibles string
	// rounds and eggs are the player's records for the leaderboards (leaderboards.go), one for each map and party
	// size. rounds has their best round on the map. eggs has their fastest time through the map's easter egg.
	rounds, eggs []record
}

// fileReport is a page's report of which game files of a map it has (POST /lobby/files). done is set when the page
// has all of them, and running while it downloads the rest. got is the number of bytes it has downloaded so far, out
// of the need bytes that this download fetches. need is not the map's total size: it leaves out what the browser
// already had of the map, for example from another map.
type fileReport struct {
	mapName       string
	done, running bool
	got, need     int64
}

// presence is the part of what a room shows about a player that can change as time passes, without a call from
// anyone: whether they are away, whether they play on Windows, and whether they are ready.
type presence struct{ away, windows, ready bool }

// away returns how long the player has been away, or 0 when they are not away. A player is away when no page of
// theirs is open, and no Windows client of theirs is asking for their room's game or playing in it. A client that is
// in the game no longer asks for it, so playing has to count as being present. Otherwise a Windows player with no
// page open would be away while they play. Once their game had lasted leaveAfter, they would leave their room when it
// ended (sweep), and their client would find no room for their next game. The caller holds l.mu.
func (l *lobby) away(p *player) time.Duration {
	if l.playsOnWindows(p) {
		p.played = time.Now()
		return 0
	}
	if p.streams > 0 || time.Since(p.client) < clientAsks {
		return 0
	}
	since := p.gone
	if p.client.After(since) {
		since = p.client
	}
	if p.played.After(since) {
		since = p.played
	}
	return max(time.Since(since), 1)
}

type room struct {
	code    string
	members []*player // in the order they joined; the first is the host
	mapName string
	private bool
	size    int       // players it takes: newRoomSize until its host sets it (setRoom), never fewer than it has
	mods    []string  // the IDs of the mods its games have; a mod with options is "id=value" (mods.go: modsNamed)
	made    time.Time // when it was made; it is listed as private until it is privateFor old
	note    string    // how its last game ended, shown to its players, unless the game ended at game over
	// password is what its T6 games require from the players' games that join them (t6.go). Only the room's players
	// are shown it. It belongs to the room and not to one game, so that the line that joins the room's game by hand
	// can be shown before the game exists.
	password string
	result   *gameResult // how its last game went, when it ended at game over; kept until its next game starts
	// startsAt is when the room's game starts, while everyone is ready (checkReady). It is zero when no start is
	// planned.
	startsAt time.Time
	game     *roomGame
}

// roomGame is a room's game, from its start until it ends.
type roomGame struct {
	g       *game
	title   title              // the title of the room's map
	cancel  context.CancelFunc // stops it
	port    int
	seats   []*seat
	began   time.Time // when it started
	up      time.Time // a T6 game: when Plutonium's server first answered; its players join it from then (t6Status)
	held    time.Time // a T6 game: when it last waited for a player whose launcher was getting their PC ready (t6Late)
	started bool      // round 1 has begun; a T5 game begins it once everyone is in
	round1  time.Time // when round 1 began
	// eggSeconds is when the map's easter egg was done, in seconds from round 1, or 0 when it is not done yet. The
	// whole party gets this one time, however late the level credits one of its players.
	eggSeconds int64
	ending     string // why it is being stopped, or "" while it is not
	vacant     bool   // no one plays it or is on their way into it (checkGame), so the room's host may end it
	// stats has each player's line of the scoreboard, by their seat's address ("bo1_lobby: stats"). The game reports
	// the lines of the characters that are in it before each report of a round or of game over, and keepRounds
	// empties stats after that report.
	stats  map[string]playerStats
	report *gameResult // how the game stood when its last round began (keepRounds)
	result *gameResult // how the game went, set at game over
}

// gameResult is how a room's game went: the round it was on (0 when the level counts no rounds), how long its players
// played it (from round 1), and the scoreboard line of each player whose character was in the game at that moment,
// the most points first (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: SV_Lobby_Stats). At game over, the page shows the
// result to the players when they are back in the room, until they have seen it (web/index.html). Before game over,
// the result is how the game stood when its last round began. The engine adds a final down for each player to it,
// because everyone goes down when a game ends. A game that ends in another way than game over has no later report.
// The result names the mods the game had. A game with mods counts for a record like any other game
// (leaderboards.go), and only its report shows that it had mods.
type gameResult struct {
	ID      string        `json:"id"` // the game's ID: the room's code and when the game started (gameID)
	Map     string        `json:"map"`
	Mods    []string      `json:"mods,omitempty"` // the mods the game had, in the form its room has them (room.mods)
	Round   int           `json:"round,omitempty"`
	Seconds int64         `json:"seconds"`
	Players []playerStats `json:"players"`
	// party is how many players started the game, and began is when it started. The database keeps them with the
	// report (store.go).
	party int
	began time.Time
}

type playerStats struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Score int    `json:"score"` // all the points they earned
	Cash  int    `json:"cash"`  // the points they have left to spend
	// Team is their team, 1 or 2, in a game with Grief on (teamOf). The page adds up the cash of each team.
	Team      int `json:"team,omitempty"`
	Kills     int `json:"kills"`
	Headshots int `json:"headshots"`
	Downs     int `json:"downs"`
	Revives   int `json:"revives"`
	// Doors is the number of doors they bought, including debris. It is nil when the game reported no number, as in
	// a report from a game built before the engine counted doors.
	Doors *int `json:"doors,omitempty"`
	// Avatar is their current picture, filled in when the report is sent to the page (withPlayers).
	Avatar string `json:"avatar,omitempty"`
}

// seat is a player's place in a room's game. The player keeps it until the game ends.
type seat struct {
	player *player
	local  *net.UDPAddr // the address the game sees them at, on every visit from a browser or Windows; it identifies them
	join   bool         // their page should be in the game: from the start, and after a rejoin, until it ends in the page
	in     bool         // the game has them in it
	left   bool         // they chose to leave it
	gone   bool         // the game dropped their client: they left, were kicked, or were away for keepCharacter
	slot   int          // in a T6 game, the slot Plutonium's server has their client in (t6Status), or -1 for none
}

// lost reports whether the seat's character is gone for good, so that its player can't take it back. That is the case
// when the player left the game, or when the game dropped their client after round 1 had begun. Before round 1, a
// drop is not final: a player whose page reloads while it loads is out and then in again.
func (rg *roomGame) lost(s *seat) bool {
	return s.left || rg.started && s.gone
}

func newLobby(ctx context.Context) *lobby {
	return &lobby{ctx: ctx, players: map[string]*player{}, sessions: map[string]*session{}, keys: map[string]*player{},
		rooms: map[string]*room{}, reports: map[string]*gameResult{}, unsavedGames: map[string]*gameResult{},
		titles: []title{&t5Title{}, &t6Title{knocked: map[netip.Addr]time.Time{}}},
		busy:   map[int]bool{}, changed: make(chan struct{}), roomSize: 4, minPlayers: 2,
		loadTimeout: 5 * time.Minute,
		sweepEvery:  time.Second, countdown: 4 * time.Second, privateFor: 3 * time.Second}
}

// change wakes the event streams. The caller holds l.mu.
func (l *lobby) change() {
	close(l.changed)
	l.changed = make(chan struct{})
}

func randomHex(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// codeLetters are the letters of room codes. They are consonants, so that no code spells a word, and none of them
// reads like another character (no O and 0, no I and 1).
const codeLetters = "BCDFGHJKLMNPQRSTVWXZ"

// newCode returns a code that no room has. The caller holds l.mu.
func (l *lobby) newCode() string {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		for i := range b {
			b[i] = codeLetters[int(b[i])%len(codeLetters)]
		}
		if code := string(b); l.rooms[code] == nil {
			return code
		}
	}
}

// nameUnsafe matches the characters a name may not have. A name may have letters, digits, spaces and the signs
// _ . - ' ! ? ( ) [ ] |, which are all that the game keeps of a name (SV_SanitizeName,
// bo1-zombies/src/client_mp/sv_client_mp.cpp). A name goes into the game's command line and its userinfo, which
// quotes, backslashes, semicolons and the command line's pluses would break. It also goes into the game's text, where
// a caret starts a color code.
var nameUnsafe = regexp.MustCompile(`[^A-Za-z0-9 _.\-'!?()\[\]|]`)

// foldLetters maps letters to the ASCII letters they are written as. NFKD does not take these letters apart, because
// they are not a letter with a mark.
var foldLetters = map[rune]string{'ß': "ss", 'Æ': "AE", 'æ': "ae", 'Œ': "OE", 'œ': "oe", 'Ø': "O", 'ø': "o",
	'Đ': "D", 'đ': "d", 'Ð': "D", 'ð': "d", 'Ł': "L", 'ł': "l", 'Þ': "Th", 'þ': "th", 'ı': "i"}

// clean returns a name in the form the game accepts, or "" when nothing is left of it. It removes the marks from
// letters (Zoë becomes Zoe) and turns the styled letters of a Discord name into plain ones (NFKD: ｚｏｅ and
// 𝐳𝐨𝐞 become zoe). It then removes what nameUnsafe matches and cuts the name to nameLength. A space is kept
// only after a letter or digit, as in the game. A name needs only one letter or digit in a game that the server
// runs (bo1-zombies/src/server_mp/sv_lobby_mp.cpp: SV_Lobby_MinNameLength).
func clean(name string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(name) {
		if s, ok := foldLetters[r]; ok {
			b.WriteString(s)
		} else if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	name = strings.Join(strings.Fields(nameUnsafe.ReplaceAllString(b.String(), "")), " ")
	first := strings.IndexFunc(name, isAlnum)
	if first < 0 {
		return ""
	}
	name = strings.ReplaceAll(name[:first], " ", "") + name[first:]
	if len(name) > nameLength {
		name = strings.TrimSpace(name[:nameLength])
	}
	if !strings.ContainsFunc(name, isAlnum) {
		return ""
	}
	return name
}

func isAlnum(r rune) bool { return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

// cleanName returns the name a player asked for in the form the game accepts (clean), or "Player" when nothing is
// left of it.
func cleanName(name string) string {
	if name = clean(name); name == "" {
		return "Player"
	}
	return name
}

// uniqueName returns name, or name with a number added when another player in the room has that name. The game
// accepts each name only once (SV_ValidateName).
func uniqueName(name string, r *room, p *player) string {
	if r == nil {
		return name
	}
	taken := func(n string) bool {
		return slices.ContainsFunc(r.members, func(m *player) bool { return m != p && strings.EqualFold(m.name, n) })
	}
	for n := 2; taken(name); n++ {
		suffix := " " + strconv.Itoa(n)
		base := strings.TrimRight(name, " 0123456789")
		if base == "" {
			base = "Player"
		}
		name = base[:min(len(base), nameLength-len(suffix))] + suffix
	}
	return name
}

// rename sets the name p plays under to the name they asked for, made unique in their room (uniqueName). It does
// nothing while their room's game runs, because the game knows them by the name they joined it with. The caller holds
// l.mu.
func (l *lobby) rename(p *player) {
	if p.room == nil || p.room.game == nil {
		p.name = uniqueName(p.asked, p.room, p)
	}
}

// mapName returns the map a room asks for when the game files have it. Otherwise it returns the default map, or the
// first map the game files have when they lack the default map too.
func (l *lobby) mapName(want string) string {
	have := l.maps()
	for _, m := range []string{want, l.defaultMap} {
		if slices.Contains(have, m) {
			return m
		}
	}
	if len(have) > 0 {
		return have[0]
	}
	return l.defaultMap
}

func mapTitle(name string) string {
	for _, m := range mapTitles {
		if m.name == name {
			return m.title
		}
	}
	return name
}

// ---- what the page sees ----

type view struct {
	You  youView   `json:"you"`
	Maps []mapView `json:"maps"` // all the Zombies maps, whether the server has them or not
	Mods []mod     `json:"mods"` // the mods a room's games can have
	// Pictures are the pictures a player may pick: the pictures of the set that the server has, in the set's order.
	// An event stream sends them in its first event and again when the server has other pictures, not in every event
	// (events), because they make up most of a view.
	Pictures []pictureView `json:"pictures,omitzero"`
	// DefaultMap is the map of a new room, unless its maker picks one. The page shows it while the player is in no
	// room.
	DefaultMap string     `json:"defaultMap"`
	Rooms      []roomItem `json:"rooms"`      // all the rooms; a room listed as private has no code (roomItem)
	Room       *roomView  `json:"room"`       // the player's room, or null
	Size       int        `json:"size"`       // the most players a room may take, and the largest size its host may set
	MinPlayers int        `json:"minPlayers"` // the players a room's game needs, and the smallest size its host may set
	Full       bool       `json:"full"`       // all of the server's games are running, so no one may be ready (full)
}

type mapView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Of    string `json:"of"`             // the title it belongs to: t5 or t6 (mapEntry)
	Have  bool   `json:"have,omitempty"` // the game files have it, so a room may play it
	List  bool   `json:"list,omitempty"` // the server has the list of files it reads (files.go), so a browser may play it
	Desc  string `json:"desc,omitempty"` // its description in the game's map select
}

// pictureView is a picture of the set (pictures.go): its id (the page loads GET /pictures/<id>.png), its name, and
// the heading the page lists it under.
type pictureView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
}

// youView is the player who receives the view.
type youView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"` // their picture, as a URL the page loads (avatar)
	// Picture is the id of the picture of the set that is shown for them, or "" when their avatar on Discord is shown
	// (pictureShown).
	Picture string `json:"picture,omitempty"`
	// DiscordAvatar is their avatar on Discord, when they have one. It is their picture unless they picked a picture
	// of the set.
	DiscordAvatar string `json:"discordAvatar,omitempty"`
	Windows       bool   `json:"windows,omitempty"` // they play on Windows (onWindows)
	Ready         bool   `json:"ready,omitempty"`   // they are ready to play their room's next game (isReady)
	// Launcher is set while their launcher runs (launcherOpen). Their page then does not open a launcher.
	// LauncherReady is set when the launcher reported that their PC has what the game needs. LauncherFailed is what
	// kept the launcher from getting the PC ready, or from starting the game, when it last tried.
	Launcher       bool   `json:"launcher,omitempty"`
	LauncherReady  bool   `json:"launcherReady,omitempty"`
	LauncherFailed string `json:"launcherFailed,omitempty"`
	// LauncherDoing describes in a few words what the launcher is doing to get the PC ready. It is "" when the
	// launcher has nothing to report.
	LauncherDoing string `json:"launcherDoing,omitempty"`
}

// roomItem is a room in the list. A room that is private, or less than privateFor old, is listed as private and
// without its code, unless it is the player's own room.
type roomItem struct {
	Code    string   `json:"code,omitempty"`
	Private bool     `json:"private,omitempty"`
	Map     string   `json:"map"`
	Mods    []string `json:"mods,omitempty"` // the mods its games have (room.mods)
	Host    string   `json:"host"`
	Players int      `json:"players"`
	Size    int      `json:"size"` // the number of players it takes (room.size)
	State   string   `json:"state"`
}

type roomView struct {
	Code    string       `json:"code"`
	Map     string       `json:"map"`
	Of      string       `json:"of"` // the title of its map; a page can't play a t6 room's game (t6.go)
	Private bool         `json:"private"`
	Size    int          `json:"size"`           // the number of players it takes (room.size)
	Mods    []string     `json:"mods,omitempty"` // the mods its games have (room.mods)
	State   string       `json:"state"`          // open (no game), loading (players load in), playing, ending (it quits)
	Members []memberView `json:"members"`
	Note    string       `json:"note,omitempty"`
	Host    bool         `json:"host"`           // the player is its host
	Seat    bool         `json:"seat,omitempty"` // the player has a seat in its game
	Join    bool         `json:"join,omitempty"` // the player's page should be in its game, so the page joins it
	Left    bool         `json:"left,omitempty"` // the player's character is gone from its game and can't be taken back
	// StartsIn is the number of milliseconds until its game starts, while everyone is ready (the countdown). It is 0
	// when no start is planned.
	StartsIn int64       `json:"startsIn,omitempty"`
	CanEnd   bool        `json:"canEnd,omitempty"` // the player, as host, may end its game, which no one plays (endGame)
	Result   *gameResult `json:"result,omitempty"` // how its last game went, when it ended at game over
	// T6 is set in a T6 room for a player with a seat in its game. It has what their own game needs to join it
	// (t6View).
	T6 *t6View `json:"t6,omitempty"`
	// Up is set in a T6 room once Plutonium's server is up. The room's game then waits for its players' games, which
	// can join it from that moment.
	Up bool `json:"up,omitempty"`
	// Password and As are set in a T6 room. A player's game that joins the room's game by hand needs them: the room's
	// password, and the name the player has in its games (player.name).
	Password string `json:"password,omitempty"`
	As       string `json:"as,omitempty"`
}

// t6View is a T6 room's game as shown to a player with a seat in it. Up reports whether Plutonium's server is up.
// Password is set once it is up. It is the password the player's game joins with, in the line the page builds for the
// player (web/src/windows.ts: t6Console). Name is the name the game should know the player by (player.name).
type t6View struct {
	Up       bool   `json:"up"`
	Password string `json:"password,omitempty"`
	Name     string `json:"name"`
}

type memberView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"` // their picture, as a URL the page loads (avatar)
	// Place is their place in the room, from 0 (placeOf). The room's game gives them the client slot of their place,
	// which decides the colour of their score in the game's HUD.
	Place int `json:"place"`
	// Team is their team, 1 or 2, while the room has Grief on (teamOf). A player with a seat in the room's game has
	// their team in that game. While the room has no game, Team is the team its next game would give them with the
	// room's current members.
	Team   int  `json:"team,omitempty"`
	Host   bool `json:"host,omitempty"`
	InGame bool `json:"inGame,omitempty"` // they are in the room's game
	Away   bool `json:"away,omitempty"`   // their page is closed, and no Windows client of theirs waits or plays (away)
	// Windows is set when they play on Windows (onWindows). Their page then stays out of the room's game.
	Windows bool `json:"windows,omitempty"`
	Ready   bool `json:"ready,omitempty"` // they are ready to play the room's next game (isReady)
	// Gamepad is set when their page reads a gamepad the game can use (setGamepad).
	Gamepad bool `json:"gamepad,omitempty"`
	// Missing is set when their page lacks some of the game files of the room's map (missing). Downloading is set
	// while the page downloads the rest, and Got is how much of that download it has, from 0 to 1. In a T6 room,
	// Downloading and Got are for their launcher's download of the game files (player.downloading).
	Missing     bool    `json:"missing,omitempty"`
	Downloading bool    `json:"downloading,omitempty"`
	Got         float64 `json:"got,omitempty"`
	// Mark is the map they suggest to the host (setMark).
	Mark string `json:"mark,omitempty"`
}

func (r *room) state() string {
	switch {
	case r.game == nil:
		return "open"
	case r.game.ending != "":
		return "ending"
	case !r.game.started:
		return "loading"
	default:
		return "playing"
	}
}

func (r *room) seatOf(p *player) *seat {
	if r.game == nil {
		return nil
	}
	for _, s := range r.game.seats {
		if s.player == p {
			return s
		}
	}
	return nil
}

// placeOf returns the place in r of its member p, from 0. While r's game runs, it is the place of p's seat in the
// game. The game received the places when it started (placesOf) and gives each player the client slot of their place.
// p keeps this place when other players leave the room. While r has no game, it is p's position among r's members,
// which is their place in r's next game.
func (r *room) placeOf(p *player) int {
	if r.game != nil {
		if i := slices.IndexFunc(r.game.seats, func(s *seat) bool { return s.player == p }); i >= 0 {
			return i
		}
	}
	return slices.Index(r.members, p)
}

// view returns the state of the server as p sees it. The caller holds l.mu.
func (l *lobby) view(p *player) view {
	// Windows is the value updatePresence last stored, so that it agrees with what updatePresence did about it (their
	// seat's join).
	v := view{You: youView{ID: p.id, Name: p.asked, Avatar: l.avatar(p), Picture: l.pictureShown(p),
		DiscordAvatar: p.discordAvatar(), Windows: p.shown.windows, Ready: l.isReady(p),
		Launcher: l.launcherOpen(p), LauncherReady: l.launcherOpen(p) && p.launcher.ready,
		LauncherFailed: p.launcher.Failed, LauncherDoing: p.launcher.Doing},
		Size: l.roomSize, MinPlayers: l.minPlayers, Mods: modList, Pictures: l.picturesShown, Rooms: []roomItem{},
		Full: l.full(), DefaultMap: l.mapName("")}
	if v.Pictures == nil {
		v.Pictures = []pictureView{} // none yet
	}
	have := l.maps()
	for _, m := range mapTitles {
		mv := mapView{Name: m.name, Title: m.title, Of: m.of, Have: slices.Contains(have, m.name),
			List: l.files != nil && l.files.reads[m.name] != nil}
		if l.mapDesc != nil {
			mv.Desc = l.mapDesc(m.name)
		}
		v.Maps = append(v.Maps, mv)
	}
	byCode := func(a, b *room) int { return strings.Compare(a.code, b.code) }
	for _, r := range slices.SortedFunc(maps.Values(l.rooms), byCode) {
		item := roomItem{Code: r.code, Map: r.mapName, Mods: r.mods, Host: r.members[0].asked, Players: len(r.members),
			Size: r.size, State: r.state()}
		if r != p.room && (r.private || time.Since(r.made) < l.privateFor) {
			item.Code, item.Private = "", true
		}
		v.Rooms = append(v.Rooms, item)
	}
	if r := p.room; r != nil {
		of := l.title(r.mapName)
		rv := &roomView{Code: r.code, Map: r.mapName, Of: of.id(), Private: r.private, Size: r.size,
			Mods: r.mods, State: r.state(), Note: r.note, Host: r.members[0] == p, Result: l.withPlayers(r.result)}
		if r.game != nil && r.game.result != nil {
			rv.Result = l.withPlayers(r.game.result) // game over, while the game quits
		} else if r.game != nil && r.game.ending != "" {
			rv.Note = r.game.ending // why the game ends, while it quits
		}
		rv.CanEnd = rv.Host && r.game != nil && r.game.ending == "" && r.game.vacant
		if !r.startsAt.IsZero() {
			rv.StartsIn = max(time.Until(r.startsAt).Milliseconds(), 1)
		}
		teams := slices.Contains(r.mods, "grief")
		for i, m := range r.members {
			s := r.seatOf(m)
			mv := memberView{ID: m.id, Name: m.asked, Avatar: l.avatar(m), Place: r.placeOf(m), Host: i == 0,
				InGame: s != nil && s.in, Away: l.away(m) > 0, Windows: m.shown.windows, Ready: l.isReady(m),
				Gamepad: m.gamepad, Mark: m.mark}
			if teams && r.game == nil {
				mv.Team = teamOf(i, len(r.members))
			} else if teams && s != nil {
				mv.Team = teamOf(mv.Place, len(r.game.seats))
			}
			of.showMember(l, r, m, &mv)
			rv.Members = append(rv.Members, mv)
		}
		// No one can take back or join a seat in a game that is ending.
		if s := r.seatOf(p); s != nil && r.game.ending == "" {
			rv.Seat, rv.Join, rv.Left = true, s.join, r.game.lost(s)
		}
		of.showRoom(l, r, p, rv)
		v.Room = rv
	}
	return v
}

// ---- HTTP ----

func (l *lobby) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	action := strings.TrimPrefix(req.URL.Path, "/lobby/")
	if action == "events" || action == "leaderboards" {
		if req.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if action == "events" {
			l.events(w, req)
		} else {
			l.serveLeaderboards(w, req)
		}
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name    string    `json:"name"`
		Map     string    `json:"map"`
		Private *bool     `json:"private"`
		Mods    *[]string `json:"mods"`
		Size    int       `json:"size"`
		Ready   bool      `json:"ready"`
		Room    string    `json:"room"`
		Picture string    `json:"picture"`
		Why     string    `json:"why"`
		Text    string    `json:"text"`
		Done    bool      `json:"done"`
		Got     int64     `json:"got"`
		Need    int64     `json:"need"`
		Running bool      `json:"running"`
		Gamepad bool      `json:"gamepad"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if action == "hello" {
		if l.discord == nil {
			json.NewEncoder(w).Encode(l.hello(bearer(req), in.Name))
			return
		}
		// The cookie identifies the player, and each hello renews it for as long as a session lasts unused.
		h := l.hello(sessionToken(req), in.Name)
		setSession(w, req, h.Token)
		json.NewEncoder(w).Encode(h)
		return
	}

	l.mu.Lock()
	p := l.playerOf(bearer(req))
	var err error
	refresh := false // look the player up on Discord again (checkDiscord), once l.mu is released
	if p != nil {
		p.addr = pageAddr(req)
	}
	switch {
	case p == nil:
		err = l.unknown()
	case action == "create":
		err = l.create(p, in.Map, in.Private != nil && *in.Private)
	case action == "join":
		err = l.join(p, strings.ToUpper(strings.TrimSpace(in.Room)))
	case action == "leave":
		l.leave(p)
	case action == "room":
		err = l.setRoom(p, in.Map, in.Private, in.Mods, in.Size)
	case action == "mark":
		err = l.setMark(p, in.Map)
	case action == "files":
		l.setFiles(p, fileReport{in.Map, in.Done, in.Running, in.Got, in.Need})
	case action == "ready":
		err = l.setReady(p, in.Ready)
	case action == "gamepad":
		l.setGamepad(p, in.Gamepad)
	case action == "rejoin":
		err = l.rejoin(p)
	case action == "end":
		err = l.endGame(p)
	case action == "ended":
		l.ended(p, in.Why, in.Text)
	case action == "waited":
		l.waited(p, in.Text)
	case action == "picture":
		err = l.setPicture(p, in.Picture)
	case action == "refresh" && l.discord != nil:
		refresh = l.startCheck(p, discordRefresh)
	case action == "signout" && l.discord != nil:
		// End the browser's sessions: the token's and the cookie's. A player with no other session leaves their room.
		l.endSession(p, bearer(req))
		l.endSession(p, sessionToken(req))
		if !l.hasSession(p) {
			l.leave(p)
		}
		setSession(w, req, "")
		log.Printf("server: %s signed out", p.name)
		l.change()
	default:
		l.mu.Unlock()
		http.NotFound(w, req)
		return
	}
	l.mu.Unlock()
	if refresh {
		l.checkDiscord(p)
	}
	if err == errUnknown || err == errSignedOut {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}
	answer := struct {
		Error string `json:"error,omitempty"`
	}{}
	if err != nil {
		answer.Error = err.Error()
	}
	json.NewEncoder(w).Encode(answer)
}

// errUnknown and errSignedOut are the errors for a token the server doesn't know: its session expired (players.go),
// or the player was signed out (discord.go). The page then calls hello again.
var (
	errUnknown   = errors.New("Your session expired. Try again.")
	errSignedOut = errors.New("You were signed out. Sign in again.")
)

func (l *lobby) unknown() error {
	if l.discord != nil {
		return errSignedOut
	}
	return errUnknown
}

// bearer returns the token that identifies the player of a call, from its "Authorization: Bearer <token>" header.
func bearer(req *http.Request) string {
	token, _ := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	return token
}

type hello struct {
	Token   string `json:"token,omitempty"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Key     string `json:"key,omitempty"`
	Discord bool   `json:"discord,omitempty"` // their name is their name on Discord, which they change there
	SignIn  string `json:"signIn,omitempty"`  // the URL where they have to sign in first (discord.go)
	Member  bool   `json:"member,omitempty"`  // they have to sign in as a member of a Discord server
}

// hello returns the player of the token, under the name they ask for. Where players sign in with Discord, it returns
// the player of the page's cookie (discordHello).
func (l *lobby) hello(token, name string) hello {
	if l.discord != nil {
		return l.discordHello(token)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.playerOf(token)
	if p == nil {
		// No event is needed: no one sees the new player yet, and the new player sees nothing yet.
		p = l.newPlayer()
		p.asked = cleanName(name)
		p.name = p.asked
		l.givePicture(p)
		return hello{Token: l.newSession(p), ID: p.id, Name: p.asked, Key: p.key}
	}
	l.use(token)
	if asked := cleanName(name); asked != p.asked {
		p.asked = asked
		l.save()
	}
	if l.givePicture(p) {
		l.save()
	}
	l.rename(p)
	l.change()
	return hello{Token: token, ID: p.id, Name: p.asked, Key: p.key}
}

// events streams the state of the server as the player sees it (view). It sends an event each time the view changes,
// until the page closes the stream.
func (l *lobby) events(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	token := bearer(req)
	l.mu.Lock()
	p := l.playerOf(token)
	if p == nil {
		l.mu.Unlock()
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, l.unknown().Error(), http.StatusUnauthorized)
		return
	}
	p.streams++
	p.addr = pageAddr(req)
	if !p.opened {
		p.opened = true // the server keeps the player from now on (players.go)
		l.save()
	}
	l.change()
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		if p.streams--; p.streams == 0 {
			p.gone = time.Now()
			p.gamepad = false // the gamepad belonged to that page; the next page reports its own (setGamepad)
		}
		l.change()
		l.mu.Unlock()
	}()

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Accel-Buffering", "no") // an Nginx proxy then passes each event on as it arrives
	w.WriteHeader(http.StatusOK)
	var last []byte
	told := -1 // the value of picturesMade when the pictures to pick from were last sent to the page
	for {
		l.mu.Lock()
		if l.playerOf(token) != p {
			// The player signed out. The page calls hello again, and the answer shows that they are signed out.
			l.mu.Unlock()
			return
		}
		v := l.view(p)
		made := l.picturesMade
		changed := l.changed
		l.mu.Unlock()
		// The pictures to pick from are sent in the first event, and again when the server has other pictures
		// (havePictures). The page keeps them, and an event without them means that they have not changed.
		pictures := v.Pictures
		v.Pictures = nil
		if data, _ := json.Marshal(v); string(data) != string(last) || made != told {
			last = data
			if made != told {
				v.Pictures, told = pictures, made
				data, _ = json.Marshal(v)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		} else {
			fmt.Fprint(w, ": still here\n\n")
		}
		flusher.Flush()
		select {
		case <-changed:
		case <-time.After(keepAlive):
		case <-req.Context().Done():
			return
		case <-l.ctx.Done():
			return
		}
	}
}

// ---- what players do, with l.mu held ----

func (l *lobby) create(p *player, mapName string, private bool) error {
	l.leave(p)
	r := &room{code: l.newCode(), members: []*player{p}, mapName: l.mapName(mapName), private: private,
		size: l.newRoomSize(), made: time.Now(), password: randomHex(4)}
	l.rooms[r.code] = r
	p.room = r
	log.Printf("server: %s made room %s (%s%s)", p.name, r.code, r.mapName, map[bool]string{true: ", private"}[private])
	// After privateFor, the list shows the room's code, unless the room was made private or is gone by then.
	time.AfterFunc(l.privateFor, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.rooms[r.code] == r && !r.private {
			l.change()
		}
	})
	l.change()
	return nil
}

// newRoomSize returns how many players a new room takes. That is defaultRoomSize, or the nearest size a room may have
// on a server whose games need more players than that, or whose rooms hold fewer.
func (l *lobby) newRoomSize() int {
	return min(max(defaultRoomSize, l.minPlayers), l.roomSize)
}

func (l *lobby) join(p *player, code string) error {
	r := l.rooms[code]
	switch {
	case r == nil:
		return fmt.Errorf("There is no room %s.", code)
	case p.room == r:
		return nil
	case r.game != nil:
		// The game belongs to the room's members, who each have a seat in it, and the room takes no one else.
		return fmt.Errorf("Room %s is in game.", code)
	case len(r.members) >= r.size:
		return fmt.Errorf("Room %s is full.", code)
	}
	l.leave(p)
	p.name = uniqueName(p.asked, r, p)
	r.members = append(r.members, p)
	p.room = r
	log.Printf("server: %s joined room %s", p.name, r.code)
	l.unready(r)
	l.change()
	return nil
}

// unready makes everyone in r unready, so that they have to report again that they are ready. It is called when r's
// members or its map changed, because the game they were ready for changed with them. The caller holds l.mu.
func (l *lobby) unready(r *room) {
	for _, m := range r.members {
		m.ready = false
	}
	l.checkReady(r)
}

// leave takes p out of their room, and p gives up their seat in its game. A game that is still loading is called
// off. The room is removed when its last member leaves.
func (l *lobby) leave(p *player) {
	r := p.room
	if r == nil {
		return
	}
	p.room, p.ready, p.mark = nil, false, ""
	r.members = slices.DeleteFunc(r.members, func(m *player) bool { return m == p })
	log.Printf("server: %s left room %s", p.name, r.code)
	if len(r.members) > 0 {
		r.members[0].mark = "" // this member may be the host now, and the host picks the map instead
	}
	if s := r.seatOf(p); s != nil {
		s.join = false
		if !r.game.started {
			s.left = true
			l.stop(r, fmt.Sprintf("Game called off: %s left.", p.asked))
		} else {
			l.leaveGame(r, s)
		}
		l.checkGame(r)
	}
	if len(r.members) == 0 {
		if r.game != nil {
			l.stop(r, "Everyone left the room.")
		}
		delete(l.rooms, r.code)
	}
	l.unready(r)
	l.change()
}

// setRoom changes the settings of p's room, for p as its host: its map, whether it is private, its mods, and how many
// players it takes (size, where 0 leaves it as it is). It changes only the settings that the call names. The size
// belongs to the room and not to its game, so it may change while a game runs, as private may. The players who are
// ready stay ready after such a change.
func (l *lobby) setRoom(p *player, mapName string, private *bool, mods *[]string, size int) error {
	r := p.room
	switch {
	case r == nil:
		return errors.New("You're not in a room.")
	case r.members[0] != p:
		return errors.New("Only the host picks the map.")
	}
	// Everything is checked before anything is changed, so that a call that is refused changes nothing.
	var on []string
	if mapName != "" {
		if r.game != nil {
			return errors.New("The map can't change while in game.")
		}
		if err := l.checkMap(mapName); err != nil {
			return err
		}
	}
	if mods != nil {
		if r.game != nil {
			return errors.New("The mods can't change while in game.")
		}
		var err error
		if on, err = modsNamed(*mods); err != nil {
			return err
		}
		if of := l.title(cmp.Or(mapName, r.mapName)); !of.mods() && len(on) > 0 {
			return fmt.Errorf("%s games have no mods.", of.label())
		}
	}
	if size != 0 {
		switch {
		case size < l.minPlayers || size > l.roomSize:
			return fmt.Errorf("Room size can't be %d.", size)
		case size < len(r.members):
			return fmt.Errorf("The room has %d players already.", len(r.members))
		}
	}
	if mapName != "" && mapName != r.mapName {
		r.mapName = mapName
		if of := l.title(mapName); !of.mods() && mods == nil && len(r.mods) > 0 {
			r.mods = nil
			log.Printf("server: room %s has no mods, on a %s map", r.code, of.label())
		}
		l.unready(r) // this also calls off a countdown to a game on the old map
	}
	if private != nil {
		r.private = *private
	}
	if mods != nil && !slices.Equal(on, r.mods) {
		r.mods = on
		log.Printf("server: room %s has mods %v", r.code, on)
		l.unready(r) // the game they were ready for changed, as it does with the map
	}
	if size != 0 && size != r.size {
		r.size = size
		log.Printf("server: room %s takes %d players", r.code, size)
	}
	l.change()
	return nil
}

// checkMap returns an error that explains why a room can't have the map, or nil when it can.
func (l *lobby) checkMap(mapName string) error {
	switch m, ok := mapByName(mapName); {
	case !ok:
		return fmt.Errorf("There is no map %s.", mapName)
	case !slices.Contains(l.maps(), mapName):
		return fmt.Errorf("The server doesn't have %s.", m.title)
	}
	return nil
}

// setMark stores the map that p suggests to their room's host for its next game, or no map when mapName is "". It
// changes nothing else: the players who are ready stay ready.
func (l *lobby) setMark(p *player, mapName string) error {
	r := p.room
	switch {
	case r == nil:
		return errors.New("You're not in a room.")
	case r.members[0] == p:
		return errors.New("The host picks the map.")
	case r.game != nil:
		return errors.New("The map can't change while in game.")
	}
	if mapName != "" {
		if err := l.checkMap(mapName); err != nil {
			return err
		}
	}
	p.mark = mapName
	l.change()
	return nil
}

// allReady reports whether everyone in r is ready to play its next game. The caller holds l.mu.
func (l *lobby) allReady(r *room) bool {
	return !slices.ContainsFunc(r.members, func(m *player) bool { return !l.isReady(m) })
}

// checkReady starts the countdown to r's game once everyone in r is ready, and calls it off when one of them is no
// longer ready. It is called whenever someone's readiness may have changed. The game starts when the countdown ends.
// If it can't start then (the server is stopping, for example), the room shows why, and everyone in it becomes
// unready. The caller holds l.mu.
func (l *lobby) checkReady(r *room) {
	ready := r.game == nil && len(r.members) >= l.minPlayers && l.rooms[r.code] == r && l.allReady(r)
	switch {
	case !ready && !r.startsAt.IsZero():
		r.startsAt = time.Time{}
		l.change()
	case ready && r.startsAt.IsZero():
		at := time.Now().Add(l.countdown)
		r.startsAt = at
		time.AfterFunc(l.countdown, func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if r.startsAt != at {
				return // the countdown was called off
			}
			r.startsAt = time.Time{}
			if err := l.start(r); err != nil {
				r.note = err.Error()
				l.unready(r)
			}
			l.change()
		})
		l.change()
	}
}

// start starts r's game on a free port, with a seat for each of r's members. The caller holds l.mu.
func (l *lobby) start(r *room) error {
	if l.ctx.Err() != nil {
		return errors.New("The server is stopping.")
	}
	// Pick a port that no game holds. A game that is still quitting holds its port, even when its room is gone
	// because everyone left it.
	port := 0
	for _, free := range l.gamePorts {
		if !l.busy[free] {
			port = free
			break
		}
	}
	if port == 0 {
		return errors.New("The server is full.")
	}
	of := l.title(r.mapName)
	if err := of.free(l); err != nil {
		return err
	}
	rg := &roomGame{title: of, port: port, began: time.Now()}
	for _, m := range r.members {
		local := l.relay.acquire()
		local.Port = seatPort
		// The player's page joins a game by itself when pages play the title. Otherwise the player's own game on
		// their PC joins it.
		rg.seats = append(rg.seats, &seat{player: m, local: local, join: of.inPage(), slot: -1})
	}
	rg.g = of.begin(l, r, rg)
	ctx, cancel := context.WithCancel(l.ctx)
	rg.cancel = cancel
	r.game, r.note, r.result = rg, "", nil
	for _, m := range r.members {
		m.mark = ""
	}
	l.busy[port] = true
	// When this is the last game the server can run, the players in rooms without a game become unready. They can't
	// be ready again until a game ends.
	if l.full() {
		for _, o := range l.rooms {
			if o.game == nil {
				l.unready(o)
			}
		}
	}
	// The log names the players by the names the game knows them by, as its other lines do, which tells them apart.
	log.Printf("server: room %s starts a game of %s for %s, on port %d", r.code, r.mapName,
		namesBy(rg.seats, func(p *player) string { return p.name }), port)
	l.games.Add(1)
	go l.runGame(ctx, r, rg)
	// checkGame notes whether anyone is on their way into the game. At the start of a T6 game, which the players' own
	// PCs join, no one is. The host may therefore end a T6 game from its start, until one of its players is in.
	l.checkGame(r)
	l.change()
	return nil
}

func (l *lobby) rejoin(p *player) error {
	r := p.room
	switch {
	case r == nil:
		return errors.New("You're not in a room.")
	case r.game == nil || r.game.ending != "":
		return errors.New("The game has ended.")
	}
	s := r.seatOf(p)
	switch {
	case s == nil:
		return errors.New("The game began without you.")
	case r.game.lost(s):
		return errors.New("You're out of this game.")
	case !r.game.title.inPage():
		return errors.New("Join it from your own game, with the line on the room's page.")
	}
	s.join = true
	l.checkGame(r) // the player is on their way into the game, so no one may end it
	l.change()
	return nil
}

// endGame ends the game of p's room, for p as the room's host. The host may end only a game that no one plays or is
// on their way into (inGame). The characters in such a game would otherwise wait for their players until the game
// removes them (keepCharacter). The caller holds l.mu.
func (l *lobby) endGame(p *player) error {
	r := p.room
	switch {
	case r == nil:
		return errors.New("You're not in a room.")
	case r.members[0] != p:
		return errors.New("Only the host ends the game.")
	}
	l.checkGame(r) // update who plays the game now
	switch {
	case r.game == nil || r.game.ending != "":
		return errors.New("The game has ended.")
	case !r.game.vacant:
		return errors.New("Someone is playing the game.")
	}
	l.stop(r, p.asked+" ended the game.")
	l.change()
	return nil
}

// ended handles the report of p's page that their game ended in the page. why is the reason: one of the engine's
// reasons, or "closed" when the tab that played the game closed, which another tab of theirs reports
// (web/index.html). text is the message that goes with it. A game that is still loading is called off. A game that
// has begun carries on without the player. Their character waits for them whether they dropped or quit the game
// ("left": quitGame), unless they were kicked.
func (l *lobby) ended(p *player, why, text string) {
	r := p.room
	s := r.seatOfOrNil(p)
	if s == nil {
		return
	}
	s.join = false
	if why == "kicked" {
		s.left = true
	}
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	log.Printf("server: %s's game in room %s ended (%s): %s", p.name, r.code, why, text)
	switch {
	case why == "left":
		l.quitGame(r, s)
	case !r.game.started && why != "over":
		l.stop(r, fmt.Sprintf("Game called off: %s's game failed to load. %s", p.asked, text))
	}
	l.checkGame(r)
	l.change()
}

// waited logs the report of a player's page about its wait for pipelines before a game (web/src/main.ts: onWaited).
// The wait can be seconds on a first load, where a later load does not wait. The report also has what the wait
// depended on: how fast the pipelines compiled, whether the page was hidden, the map, the GPU and the browser. The
// log then shows what made a load slow. waited logs the report as one line, and at most one report every ten seconds
// from a player. The caller holds l.mu.
func (l *lobby) waited(p *player, text string) {
	now := time.Now()
	if text = logLine(text, 700); text == "" || now.Sub(p.waitedSaid) < 10*time.Second {
		return
	}
	p.waitedSaid = now
	where := ""
	if p.room != nil {
		where = " in room " + p.room.code
	}
	log.Printf("server: %s%s waited for pipelines: %s", p.name, where, text)
}

// logLine returns text from a page as one line for the log. It replaces each character that doesn't print with a
// space, because a line break would let the page start a log line of its own. It cuts the text to max bytes.
func logLine(text string, max int) string {
	text = strings.TrimSpace(strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return ' '
		}
		return r
	}, text))
	if len(text) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "..."
	}
	return text
}

// quitGame handles the seat's player quitting the room's game, in their page or on Windows, from the game's menu or
// by closing it. Their page reports it (ended), or the game does ("bo1_lobby: quit"). A game that is still loading
// is called off. A game that has begun keeps their character for them. Their Windows game is gone at once, so its
// route ends (relay.quit), and their page offers to rejoin. The game keeps running, even when no one plays it
// (checkGame). The caller holds l.mu.
func (l *lobby) quitGame(r *room, s *seat) {
	s.join = false
	if !r.game.started {
		l.stop(r, fmt.Sprintf("Game called off: %s left.", s.player.asked))
	}
	if l.relay.quit(s.local) {
		s.player.client = time.Time{} // their client no longer asks for the game
	}
	l.updatePresence(s.player)
	l.checkGame(r)
}

func (r *room) seatOfOrNil(p *player) *seat {
	if r == nil {
		return nil
	}
	return r.seatOf(p)
}

// leaveGame gives up the seat's character, because its player left the room after round 1 had begun. A player who
// leaves the room leaves the game, and the server notifies the game in the way its title defines (title.drop). The
// caller holds l.mu.
func (l *lobby) leaveGame(r *room, s *seat) {
	s.left = true
	r.game.title.drop(l, r, s)
}

// checkGame ends a room's game when no character is left in it: everyone left it, or, once round 1 has begun, the
// game removed their characters (keepCharacter after they stopped playing). Otherwise the game keeps running, even
// when no one plays it, so that its players can come back to it. checkGame notes in vacant whether no one plays the
// game or is on their way into it (inGame), because the room's host may end such a game (endGame). The caller holds
// l.mu.
func (l *lobby) checkGame(r *room) {
	rg := r.game
	if rg == nil || rg.ending != "" {
		return
	}
	kept := slices.DeleteFunc(slices.Clone(rg.seats), rg.lost)
	switch {
	case len(kept) == 0 && !slices.ContainsFunc(rg.seats, func(s *seat) bool { return !s.left }):
		l.stop(r, "Everyone left the game.")
		return
	case len(kept) == 0:
		l.stop(r, "Game ended: no one was playing.") // everyone left it, or was away from it for keepCharacter
		return
	}
	if vacant := !slices.ContainsFunc(kept, l.inGame); vacant != rg.vacant {
		rg.vacant = vacant
		l.change()
	}
}

// stop ends a room's game, and the room shows why. The game quits, which tells its players. The caller holds l.mu.
func (l *lobby) stop(r *room, why string) {
	rg := r.game
	if rg == nil || rg.ending != "" {
		return
	}
	rg.ending = why
	log.Printf("server: room %s's game ends: %s", r.code, why)
	rg.cancel()
}

// scored stores a player's line of the scoreboard as the game reported it (stats), by their seat's address. The
// fields are all the points they earned, the points they have left to spend, their kills, headshots, downs and
// revives, and the doors they bought. A game built before the engine counted doors leaves out the last field.
func (rg *roomGame) scored(seat string, fields []string) {
	if len(fields) != 6 && len(fields) != 7 {
		return
	}
	var n [7]int
	for i, field := range fields {
		n[i], _ = strconv.Atoi(field)
	}
	if rg.stats == nil {
		rg.stats = map[string]playerStats{}
	}
	st := playerStats{Score: n[0], Cash: n[1], Kills: n[2], Headshots: n[3], Downs: n[4], Revives: n[5]}
	if len(fields) == 7 {
		st.Doors = &n[6]
	}
	rg.stats[seat] = st
}

// gameID returns the ID of r's game rg: the room's code and when the game started.
func gameID(r *room, rg *roomGame) string { return fmt.Sprintf("%s-%d", r.code, rg.began.UnixMilli()) }

// result returns how rg stands at the given round: the line of the scoreboard of each seat's player, as the game last
// reported it (stats), under the player's own name, the highest score first. In a game with Grief on, each line has
// the player's team. The first team's lines come first, and within a team the most cash comes first, because cash is
// what the teams play for. The result names the game's mods, which are the room's mods: they can't change while the
// room has a game (setRoom). The caller holds l.mu.
func (l *lobby) result(r *room, rg *roomGame, round int) *gameResult {
	res := &gameResult{ID: gameID(r, rg), Map: r.mapName, Mods: slices.Clone(r.mods), Round: round,
		Players: []playerStats{}, party: len(rg.seats), began: rg.began}
	if rg.started {
		res.Seconds = int64(time.Since(rg.round1).Seconds())
	}
	teams := slices.Contains(r.mods, "grief")
	for i, s := range rg.seats {
		if st, ok := rg.stats[s.local.String()]; ok {
			st.ID, st.Name = s.player.id, s.player.asked
			if teams {
				st.Team = teamOf(i, len(rg.seats))
			}
			res.Players = append(res.Players, st)
		}
	}
	slices.SortStableFunc(res.Players, func(a, b playerStats) int {
		if teams {
			return cmp.Or(a.Team-b.Team, b.Cash-a.Cash)
		}
		return b.Score - a.Score
	})
	return res
}

// avatar returns the URL the page loads p's picture from, or "" when they have none. It is their avatar on Discord
// while they have one and have not picked a picture of the set themselves. Otherwise it is their picture of the set,
// or the picture that is shown in its place (pictures.go: pictureShown, GET /pictures/<id>.png). The caller holds
// l.mu.
func (l *lobby) avatar(p *player) string {
	if id := l.pictureShown(p); id != "" {
		return "/pictures/" + id + ".png"
	}
	return p.discordAvatar()
}

// discordAvatar returns p's avatar on Discord (discord.go: lookUp), or "" when they have no avatar or no account
// there.
func (p *player) discordAvatar() string {
	if p.discord == nil {
		return ""
	}
	return p.discord.Avatar
}

// withPlayers returns a copy of res with its players' current names and pictures (avatar), like everything else the
// page shows about a player. The server keeps a report with the names its players had at the time, and without
// their pictures. A player whom the server has forgotten since keeps the name in the report. The caller holds l.mu.
func (l *lobby) withPlayers(res *gameResult) *gameResult {
	if res == nil {
		return res
	}
	c := *res
	c.Players = slices.Clone(res.Players)
	for i := range c.Players {
		if p := l.players[c.Players[i].ID]; p != nil {
			c.Players[i].Name, c.Players[i].Avatar = p.asked, l.avatar(p)
		}
	}
	return &c
}

// runGame runs a room's game until it ends or ctx is done. The room is open again after that.
func (l *lobby) runGame(ctx context.Context, r *room, rg *roomGame) {
	defer l.games.Done()
	defer rg.cancel()
	ended := "never started"
	if rg.g.waitForPort(ctx) {
		state, err := rg.g.runOnce(ctx)
		ended = exitReason(state, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.ctx.Err() != nil:
		r.note = "The server stopped."
	case rg.result != nil:
		r.result = rg.result // game over: the page shows how the game went
	case rg.ending != "":
		r.note = rg.ending
	case ended == "quit":
		r.note = "The game ended."
	case ended == "never started" || strings.HasPrefix(ended, "did not start"):
		r.note = "The game failed to start."
	default:
		r.note = "The game crashed." // the log has the details
	}
	log.Printf("server: room %s's game ended: %s", r.code, ended)
	for _, s := range rg.seats {
		l.relay.release(s.local)
	}
	delete(l.busy, rg.port)
	rg.title.done(l, rg)
	r.game = nil
	for _, m := range r.members {
		m.ready = false // they report again that they are ready, for the room's next game
		l.rename(m)     // use a name they asked for during the game
	}
	l.change() // the server is no longer full (full) if it was, so everyone may be ready for a game again
}

// sweep runs every sweepEvery until the server stops. It takes the players who have been away for leaveAfter out of
// their rooms, except those with a character in their room's game, who may come back to it. It forgets the players in
// no room whose page never opened its event stream, once they have been away for forgetUnopened. It ends the sessions
// unused for sessionIdle (expire). It wakes the event streams when a player's presence changes (updatePresence). It
// calls off the games that have waited for their players for longer than loadTimeout, and it notes who plays each
// game (checkGame). A game that has begun ends by itself once no character is left in it (checkGame): the game
// removes a character keepCharacter after its player stopped playing.
func (l *lobby) sweep() {
	tick := time.NewTicker(l.sweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-tick.C:
		}
		l.mu.Lock()
		for _, p := range l.players {
			if p.ready && l.away(p) >= clientAsks {
				p.ready = false // their page and any Windows client have been closed for a while
			}
			l.updatePresence(p)
			l.launcherSweep(p)
			away := l.away(p)
			if away == 0 {
				continue
			}
			if p.room != nil && away >= leaveAfter && !l.hasCharacter(p) {
				l.leave(p)
			}
			if p.room == nil && !p.opened && away >= forgetUnopened {
				l.forget(p)
			}
		}
		if time.Since(l.expired) >= time.Hour {
			l.expired = time.Now()
			l.expire()
		}
		for _, r := range l.roomList() {
			if rg := r.game; rg != nil && rg.ending == "" && !rg.started {
				rg.title.late(l, r, rg)
			}
		}
		// Note who plays each game. The host may end a game that no one plays.
		for _, r := range l.roomList() {
			l.checkGame(r)
		}
		l.mu.Unlock()
	}
}

// onWindows reports whether p plays on Windows: their Windows client asks for their room's game, or it has their seat
// in the game (a route, relay.go) and has sent something lately. Their page then stays out of the game. The caller
// holds l.mu.
func (l *lobby) onWindows(p *player) bool {
	return time.Since(p.client) < clientAsks || l.playsOnWindows(p)
}

// playsOnWindows reports whether p's Windows client has their seat in their room's game (a route, relay.go) and has
// sent something lately. The caller holds l.mu.
func (l *lobby) playsOnWindows(p *player) bool {
	s := p.room.seatOfOrNil(p)
	if s == nil {
		return false
	}
	_, windows := l.relay.connected(s.local)
	return windows
}

// inGame reports whether the seat's player plays the game or is on their way into it. That is the case when their
// page's session is open, when their page is open and should join (it reloaded, or they rejoined), or when they play
// from Windows. The caller holds l.mu.
func (l *lobby) inGame(s *seat) bool {
	session, windows := l.relay.connected(s.local)
	return session || windows || s.join && s.player.streams > 0
}

// updatePresence wakes the event streams when p's presence differs from what their room was last shown. The presence
// changes when their client starts or stops asking for the game, when their Windows game goes quiet, and when they
// have been away for long enough. A player who was on Windows and no longer is (they closed the Windows game, or it
// crashed) becomes unready. Their page then joins no game by itself: it offers to rejoin. The caller holds l.mu.
func (l *lobby) updatePresence(p *player) {
	now := presence{l.away(p) > 0, l.onWindows(p), l.isReady(p)}
	if now == p.shown {
		return
	}
	if p.shown.windows && !now.windows {
		p.ready, now.ready = false, false
		if s := p.room.seatOfOrNil(p); s != nil {
			s.join = false
		}
	}
	p.shown = now
	l.change()
	if p.room != nil {
		l.checkReady(p.room)
	}
}

// isReady reports whether p is ready to play their room's next game: they reported that they are ready, and their
// page or their Windows client is open. A page that is closed for a moment, as in a reload, still counts as open. The
// caller holds l.mu.
func (l *lobby) isReady(p *player) bool {
	return p.ready && l.away(p) < clientAsks
}

func (l *lobby) setReady(p *player, ready bool) error {
	switch {
	case p.room == nil:
		return errors.New("You're not in a room.")
	case p.room.game != nil:
		return errors.New("The game is in progress.")
	case ready && l.full():
		return errors.New("The server is full.")
	case ready && l.missing(p):
		return errors.New("You don't have the room's map yet.")
	case ready && len(p.room.members) < l.minPlayers:
		return fmt.Errorf("A game needs %d players.", l.minPlayers)
	}
	p.ready = ready
	l.change()
	l.checkReady(p.room)
	return nil
}

// has reports whether the page has all of the map's game files, according to its report.
func (f fileReport) has(mapName string) bool {
	return f.mapName == mapName && f.done
}

// missing reports whether p lacks what their room's map needs, as the map's title defines it (title.lacks). The
// caller holds l.mu.
func (l *lobby) missing(p *player) bool {
	return p.room != nil && l.title(p.room.mapName).lacks(l, p)
}

// setFiles stores the report of p's page about which game files of a map it has. A ready player who now lacks the
// room's map (their page removed its files) is no longer ready. The caller holds l.mu.
func (l *lobby) setFiles(p *player, f fileReport) {
	if f == p.files {
		return
	}
	p.files = f
	if p.room != nil && p.ready && l.missing(p) {
		p.ready = false
		l.checkReady(p.room)
	}
	l.change()
}

// setGamepad stores whether the player's page reads a gamepad the game can use (POST /lobby/gamepad). Their room
// shows it on the player. A browser shows a gamepad to a page only once one of its buttons is pressed while the page
// is in front, so the gamepad belongs to the page and not to the player. It is cleared when the player's last page
// closes (events), and a page reports it again when its event stream starts. The caller holds l.mu.
func (l *lobby) setGamepad(p *player, on bool) {
	if on == p.gamepad {
		return
	}
	p.gamepad = on
	l.change()
}

// nameOf returns the name of the player with the token, or "" when there is none.
func (l *lobby) nameOf(token string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p := l.playerOf(token); p != nil {
		return p.name
	}
	return ""
}

// full reports whether all of the server's games are running: no port is free for another game (busy). No one may
// become ready then (setReady), and the players in rooms without a game become unready when the last of these games
// starts (start). The caller holds l.mu.
func (l *lobby) full() bool {
	return !slices.ContainsFunc(l.gamePorts, func(port int) bool { return !l.busy[port] })
}

// hasCharacter reports whether p has a character in their room's game, or will have one once the game has loaded. The
// caller holds l.mu.
func (l *lobby) hasCharacter(p *player) bool {
	s := p.room.seatOfOrNil(p)
	return s != nil && !p.room.game.lost(s)
}

func (l *lobby) roomList() []*room {
	rooms := make([]*room, 0, len(l.rooms))
	for _, r := range l.rooms {
		rooms = append(rooms, r)
	}
	return rooms
}

// names lists the seats' players by their own names, for a message to a room: "Ann", "Ann and Bob", "Ann, Bob and
// Cy".
func names(seats []*seat) string {
	return namesBy(seats, func(p *player) string { return p.asked })
}

// namesBy lists the seats' players, each by the name that the name function returns for them.
func namesBy(seats []*seat, name func(*player) string) string {
	var n []string
	for _, s := range seats {
		n = append(n, name(s.player))
	}
	if len(n) < 2 {
		return strings.Join(n, "")
	}
	return strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
}

// ---- the relay's side ----

// session returns where the relay sends a browser player's session (/play/<token>): to their room's game, from their
// seat's address. It also returns the name they play under, as windowsSeat does for a Windows player, and who they
// are, for the log.
func (l *lobby) session(token string) (game, local *net.UDPAddr, name, who string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.playerOf(token)
	if p == nil {
		return nil, nil, "", "", errUnknown
	}
	s := p.room.seatOfOrNil(p)
	if s == nil || p.room.game.ending != "" || p.room.game.lost(s) || !p.room.game.title.inPage() {
		return nil, nil, "", "", errors.New("no game of theirs runs") // no game that a page plays
	}
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p.room.game.port}, s.local, p.name,
		fmt.Sprintf("%s, room %s", p.name, p.room.code), nil
}

// waiting is the error that explains why a Windows player's client has to wait and can't join yet: their room's game
// has not started. The relay sends the text to the client in a print packet. The client keeps waiting and asks again
// in 3 seconds (bo1-zombies/src/client_mp/cl_main_mp.cpp: CL_ConnectionlessPacket).
type waiting string

func (w waiting) Error() string { return string(w) }

// windowsSeat returns where the relay sends a Windows player's route (Play.bat <host> <key>): to their seat in their
// room's game, from the seat's address, as with a session (session). It also returns the name they play under, and
// who they are, for the log. Until their room's game starts, it returns a waiting error that names what the client
// waits for, so the player can start their client at any time and it waits for the game. While their client asks,
// the player stays in their room, as with an open page.
func (l *lobby) windowsSeat(key string) (game, local *net.UDPAddr, name, who string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.keys[key]
	if p != nil && l.discord != nil && (p.discord == nil || !l.hasSession(p)) {
		p = nil // not signed in with Discord (discord.go)
	}
	switch {
	case key == "":
		return nil, nil, "", "", errors.New("Play.bat needs your key. Copy the line from the server's page.")
	case p == nil:
		return nil, nil, "", "", errors.New("This key no longer works. Copy the line from the server's page again.")
	}
	p.client = time.Now()
	l.updatePresence(p) // the player is not away, and plays on Windows
	r := p.room
	switch {
	case r == nil:
		return nil, nil, "", "", waiting("Waiting for you to join a room on the server's page...")
	case r.game == nil || r.game.ending != "":
		return nil, nil, "", "", waiting("Waiting for the game to start...")
	}
	s := r.seatOf(p)
	switch {
	case !r.game.title.inPage():
		return nil, nil, "", "", fmt.Errorf("Room %s plays %s. Join it with the line on its page.", r.code,
			r.game.title.label())
	case s == nil:
		return nil, nil, "", "", fmt.Errorf("Room %s's game started without you. You can join the next one.", r.code)
	case r.game.lost(s):
		return nil, nil, "", "", fmt.Errorf("Your character in room %s's game is gone. You can join the next one.", r.code)
	}
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: r.game.port}, s.local, p.name,
		fmt.Sprintf("%s, room %s", p.name, r.code), nil
}

// knowsKey reports whether key is a player's key (windowsSeat). A getchallenge with such a key comes from a T5 client
// (relay.go: knocks).
func (l *lobby) knowsKey(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.keys[key] != nil
}

// summary returns how many rooms and running games the server has, for zombies-server probe: "2 rooms, 1 game".
func (l *lobby) summary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	games := 0
	for _, r := range l.rooms {
		if r.game != nil {
			games++
		}
	}
	plural := func(n int, what string) string {
		if n == 1 {
			return "1 " + what
		}
		return fmt.Sprintf("%d %ss", n, what)
	}
	return plural(len(l.rooms), "room") + ", " + plural(games, "game")
}

// ---- the console ----

// command sends a console line to the games. A line that starts with a room's code goes to that room's game ("KXQM
// status"), and any other line goes to every running game. "rooms" lists the rooms, and "lag 100" adds to every
// player's ping (lag.go).
func (l *lobby) command(line string) {
	if word, arg, _ := strings.Cut(line, " "); strings.EqualFold(word, "lag") {
		l.relay.setLag(strings.TrimSpace(arg))
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if line == "rooms" {
		if len(l.rooms) == 0 {
			log.Printf("server: no rooms")
		}
		for _, r := range l.roomList() {
			var who []string
			for _, m := range r.members {
				who = append(who, m.name)
			}
			game := ""
			if r.game != nil {
				game = fmt.Sprintf(", its game on port %d", r.game.port)
			}
			log.Printf("server: room %s: %s, %s (%s)%s", r.code, r.mapName, r.state(), strings.Join(who, ", "), game)
		}
		return
	}
	if first, rest, ok := strings.Cut(line, " "); ok {
		if r := l.rooms[strings.ToUpper(first)]; r != nil {
			if r.game == nil {
				log.Printf("server: room %s has no game running; %q wasn't sent", r.code, rest)
			} else {
				r.game.g.command(rest)
			}
			return
		}
	}
	sent := false
	for _, r := range l.roomList() {
		if r.game != nil {
			r.game.g.command(line)
			sent = true
		}
	}
	if !sent {
		log.Printf("server: no game is running; %q wasn't sent", line)
	}
}

// prefixed writes each line with a prefix, which marks the output of a room's game among the output of the others.
type prefixed struct {
	prefix string
	w      io.Writer
}

func (p prefixed) Write(b []byte) (int, error) {
	if _, err := p.w.Write(append([]byte(p.prefix), b...)); err != nil {
		return 0, err
	}
	return len(b), nil
}
