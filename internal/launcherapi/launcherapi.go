// Package launcherapi defines what the launcher (launcher/, zombies-launcher.exe on a player's PC) and the server
// send each other, over the server's HTTP port. The server's side is server/launcher.go. A link on the room's page
// starts the launcher:
//
//	zombies-launcher://<host>[:<port>]/play?key=<key>[&of=<title>][&map=<map>][&tls=0]
//	zombies-launcher://<host>[:<port>]/play?solo=1&name=<name>[&of=<title>][&map=<map>][&key=<key>][&tls=0]
//
// The first has the launcher get the player's PC ready for a game, and start none until a room's game does: the game
// of the title of the player's room, on the room's map. A room's button opens it, and names neither. While the player
// is in no room, the lobby's Play local opens it, and the PC is made ready for the title of (T6 when the link names
// none): where the server sends that title's game files a map at a time, the launcher gets the map that the link
// names, which is how Play local downloads a map.
//
// The second launches a game: Play local's Launch game, and a room's. The launcher starts the game at its menu, for
// the player to play alone under their name, and joins no room's game. The game is the title of (T6 when the link
// names none), and map is a map of it that the launcher gets first, where the server sends game files a map at a
// time: the room's map, from a room. Its key is there when the page could reach its server, and the
// launcher then calls the server as for a room, which shows how far the launcher is. Without it the launcher calls
// no server: the page was the browser's copy, and the PC plays from what it has. An older launcher takes the link for
// the first, and updates itself before it asks the server anything, so the release it takes plays alone.
//
// The launcher starts the titles that a player's own PC plays (T6, T7). The server takes a launcher for a title from
// the version that plays it (MinVersionOf).
//
// The link names the page's server (tls=0 for a page on plain HTTP) and the player, by their key (server/rooms.go).
// The launcher sends the key with every call, to identify its player. The key goes in a header, "Authorization: Key
// <key>", and not in the URL, because a proxy in front of the server would log the URL. Every call also carries the
// launcher's version, in the header Zombies-Launcher-Version (VersionHeader). The server answers a launcher older than
// MinVersion, or one that sends no version, with 426 Upgrade Required and a sentence for the player, at every call
// but the first two below. It answers the same to a launcher that is too old for the title of its player's room.
//
//	POST /launcher/started          the launcher started a try, as the player pressed the room's button. It sends this
//	                                first, before it looks for a later release of itself (launcher/update.go), and
//	                                the server takes it from a launcher of any version. The player's room then shows
//	                                at once that the launcher starts, where its next call may come only after the
//	                                launcher has updated itself
//	GET  /launcher/play[?of=<title>][&ready=1][&game=1][&has=<zones>][&own=1][&seen=<version>]
//	                                Play: what the player's PC is to play now. of is the title that the launcher is
//	                                at, which ready and has are of: the title of the player's room, or the one that
//	                                the player asked for while in none. A launcher that sends none is at T6. The
//	                                launcher sends ready=1 once the PC has what that title's game needs (Plutonium or
//	                                BOIII, and the game files) and it waits for the room's game. Once it knows the
//	                                game folder that the player's game starts from, it sends has: those of the
//	                                title's Zones that the folder has, with commas between them. A room on a map in
//	                                another zone then shows that the player's copy lacks the map. With has, own=1
//	                                says that the folder is the player's own copy of the game, which the launcher
//	                                only reads: it downloads a map only into game files that it downloaded itself,
//	                                and the page offers a map's download only for those. It sends game=1 while the
//	                                player's game runs, which it started: the player's room offers to launch the
//	                                game, at its menu, only while it doesn't. With seen, the version of the
//	                                answer the launcher has (Play.Version), the server holds the call until its
//	                                answer is another one, for up to 45 seconds.
//	                                The launcher asks again as soon as it has an answer, so it has one call open.
//	                                That call shows the server that the launcher runs, and the launcher learns of a
//	                                change at once without asking on a timer. A launcher whose player has been in no
//	                                Black Ops II room for five minutes closes the call, until the player presses a
//	                                room's button again
//	POST /launcher/stopped          the launcher ends because its player quit it, or because it can't play until the
//	                                player installs it again (Report.Setup). The player's room then shows at once
//	                                that no launcher runs, where the server otherwise waits a while for a launcher's
//	                                next call. The server takes it from a launcher of any version, as it does the first
//	POST /launcher/files            Report: how the launcher is doing, such as how far its download of the game files
//	                                is. The room shows it
//	GET  /launcher/<title>/files    Files: a title's game files, as the server has them, when it sends them
//	                                (Title.Downloads)
//	GET  /launcher/<title>/files/<path>
//	                                one of these files, from the offset in a Range header. Its ETag is its hash
//	                                (File.Hash)
//
// The server does not hand out the launcher. Its builds are releases of the project on GitHub (Releases), which
// .github/workflows/launcher.yml publishes. Each is tagged launcher-v<version> (Tag) and has two files: the launcher
// (Setup) and its maker's signed statement of it (Signed, internal/release). One more release, tagged launcher
// (Current), always has the files of the latest of them. The room's page links to the launcher there (LatestFile),
// and an installed launcher reads the statement there and replaces itself with a later release (launcher/update.go).
// Neither goes by the release that GitHub calls the latest, so the project may release other things too.
package launcherapi

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"regexp"
	"strconv"
	"strings"
)

// Scheme is the scheme of the links that start the launcher (above). The launcher registers it on the player's PC.
const Scheme = "zombies-launcher"

// Key is the text that comes before the player's key in the launcher's Authorization header.
const Key = "Key "

// VersionHeader is the header in which the launcher sends its version with every call: the number of its release
// (main.version in launcher/), or 0 for a build that is no release.
const VersionHeader = "Zombies-Launcher-Version"

// MinVersion is the lowest version of the launcher that this server's code works with. A server refuses the calls of
// an older launcher, and of one that sends no version. Raise it when the server needs something that older launchers
// do not do, once a release that does it is out. Version 0 is a dev build, which only a dev server takes.
const MinVersion = 1

// The titles that a launcher starts, by their ids on the server (server/title.go): Black Ops II, on Plutonium, and
// Black Ops III, on BOIII.
const (
	T6 = "t6"
	T7 = "t7"
)

// MinVersionOf returns the lowest version of the launcher that plays a title: MinVersion, or the version that
// first started the title when that is later. A server refuses the calls of an older launcher while its player is in
// a room of the title, as it refuses one older than MinVersion everywhere. The launcher then takes its latest
// release, as it does before each try.
func MinVersionOf(title string) int64 {
	if title == T7 {
		return max(MinVersion, 4)
	}
	return MinVersion
}

// Releases is where the launcher's releases are: the releases of the project on GitHub.
const Releases = "https://github.com/gimme/zombies-hub/releases"

// Setup is the file name of the launcher in a release, for a player to run once. Run by hand, the program installs
// itself. It is the same program as zombies-launcher.exe in the launcher's folder. Signed is the file name of the
// maker's signed statement of the launcher in the same release (internal/release), as JSON.
const (
	Setup  = "zombies-launcher-setup.exe"
	Signed = "zombies-launcher.sig"
)

// MacSetup is the file name, in a release, of the script that installs the launcher on a Mac and runs it there
// (launcher/mac.sh). The room's page shows a Mac the line that fetches the script and runs it, for the player to
// paste into Terminal.
const MacSetup = "zombies-launcher-mac.sh"

// Tag returns the tag of the launcher's release of a version.
func Tag(version int64) string { return "launcher-v" + strconv.FormatInt(version, 10) }

// Current is the tag of the release that always has the files of the launcher's latest release. The release workflow
// puts them there with each release.
const Current = "launcher"

// LatestFile returns the address of a file of the launcher's latest release, among the releases at releases (Releases,
// or a test's stand-in): the file as the release tagged Current has it. The room's page links to the Setup there.
func LatestFile(releases, file string) string { return releases + "/download/" + Current + "/" + file }

// ReleaseFile returns the address of a file of the release of a version.
func ReleaseFile(releases string, version int64, file string) string {
	return releases + "/download/" + Tag(version) + "/" + file
}

// Play is what a player's PC is to play now.
type Play struct {
	Player string `json:"player"` // the player's name, as their page shows it
	// Room is the code of the player's room, when it plays a title that the launcher starts, and Of is that title. A
	// server from before it hosted Black Ops III sends no Of: its rooms play T6.
	Room   string `json:"room,omitempty"`
	Of     string `json:"of,omitempty"`
	InGame bool   `json:"inGame,omitempty"` // the player's game is in the room's game already
	// Game is the room's game, from its start, when the game has a seat for the player and the player's game is not
	// in it yet. It is nil while the room waits for its players, and when the game has no seat for the player.
	Game *Game `json:"game,omitempty"`
	// Needs is the zone of the room's map, among the Zones of the room's title (Title).
	Needs string `json:"needs,omitempty"`
	// Titles has what the server says of each title that it hosts and a launcher starts, by the title's id.
	Titles map[string]Title `json:"titles,omitempty"`
	// Downloads, Plutonium and Zones are T6's (Title), as a launcher from before the launcher started Black Ops III
	// reads them. They go once the server takes no such launcher (MinVersion).
	Downloads bool     `json:"downloads,omitempty"`
	Plutonium int      `json:"plutonium,omitempty"`
	Zones     []string `json:"zones,omitempty"`
	// Version identifies this answer: it changes when anything else in the answer changes. A launcher sends it back
	// as seen, and the server then answers only once it has another answer. A server that does not hold calls sends
	// no version.
	Version string `json:"version,omitempty"`
}

// Title is what the server says of a title that a launcher starts.
type Title struct {
	// Downloads is set when the server sends the title's game files (Files), to a PC that has none, or lacks a map.
	Downloads bool `json:"downloads,omitempty"`
	// Zones are the game files that tell which of the server's maps of the title a copy of the game has: each map is
	// in one of them, and a copy without a map's pack lacks its zone. Each is a path in the game's folder, with
	// slashes (LocalPath). The launcher says which of them the player's game folder has (has, above).
	Zones []string `json:"zones,omitempty"`
	// Plutonium is the revision of the server's copy of Plutonium, for T6, or 0 when the server has none yet. A room's
	// game accepts only a player's game of the same revision. The server brings its copy up to date before each game,
	// so the revision that comes with Game is the game's.
	Plutonium int `json:"plutonium,omitempty"`
}

// Game is a room's game, for the player's own game to join.
type Game struct {
	// ID identifies the game among the room's games. A launcher whose player's game runs for another of the room's
	// games has the player's game join this one.
	ID       string `json:"id"`
	Port     int    `json:"port"`     // the UDP port where the game is reached, on the server's host
	Password string `json:"password"` // the game's password
	Name     string `json:"name"`     // the name that the game knows the player by
	// Up is set once the room's game answers the server, and a player's game that asks to join gets in. A game that
	// starts takes longer than the room's does, so the launcher starts one before. A player's game that runs already,
	// from the room's last game, asks to join only once the game is up.
	Up bool `json:"up,omitempty"`
}

// Report is how the launcher is doing, for the player's page. The page is where a launcher in the tray shows things.
type Report struct {
	Running bool  `json:"running"` // the launcher downloads the game files now
	Got     int64 `json:"got"`     // the bytes it has so far, of the bytes this download needs
	Need    int64 `json:"need"`
	// Failed is what kept the launcher from preparing the PC or starting the game, when it last tried. It is "" once
	// the launcher tries again.
	Failed string `json:"failed,omitempty"`
	// Doing is what the launcher does now to prepare the PC, other than downloading, in a few words for the room's
	// button ("Updating Plutonium"). It is "" when there is nothing to show.
	Doing string `json:"doing,omitempty"`
	// Setup is set when the launcher can't play because what installed it on the player's computer is older than the
	// launcher needs: on a Mac, the script that runs it in Wine (launcher/mac.sh), which does not update itself as the
	// launcher does. The launcher then ends, and Failed says so for a server that doesn't know this field. A server
	// that does shows the line that installs the launcher again in the player's room, as it does for a launcher that
	// it doesn't take, until the next report of a launcher of theirs.
	Setup bool `json:"setup,omitempty"`
}

// Files is the list of a title's game files, as the server has them.
type Files struct {
	Version string `json:"version"` // changes with any file's path or contents
	Bytes   int64  `json:"bytes"`
	Files   []File `json:"files"`
}

// File is one of the game files: its path in the game's folder, with slashes, and the hash of its contents (HashOf).
// Pack names the part of the game that the file is of, where the server sends a title's files in parts: a map, by
// its name on the server, for a file that only that map needs, or "" for a file that every map needs. A launcher
// downloads the files that every map needs, and those of the maps that its player plays (the zone of a map, among a
// title's Zones, is of the map's pack).
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"`
	Pack string `json:"pack,omitempty"`
}

// StateFile is the name of the file that a launcher keeps in a folder of game files it downloaded. The file records
// what the folder has. It is not a game file, and the server leaves it out of its list, in case the server's own
// game files are such a downloaded copy. The server also leaves out a file that a download left unfinished, which
// has a name that ends in PartSuffix.
const (
	StateFile  = "zombies-launcher-files.json"
	PartSuffix = ".part"
)

// NewHash returns the hash function that names the contents of files: SHA-256.
func NewHash() hash.Hash { return sha256.New() }

// HashOf returns the name of the contents that were written to h: the first 32 hex digits of their SHA-256. The
// server names the contents of its game files this way everywhere (server/hashes.go).
func HashOf(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil))[:32] }

// pathPart matches a part of a path that Windows uses as it is: letters, digits and a few signs. LocalPath also
// rejects a part that ends in a dot or a space, which Windows drops. pathDevice matches a part that is a device's
// name, which Windows opens whatever the folder and the extension.
var (
	pathPart   = regexp.MustCompile(`^[A-Za-z0-9_$()\-][A-Za-z0-9_$() .\-]*$`)
	pathDevice = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\.|$)`)
)

// LocalPath reports whether a file's path is one that a launcher can write under the game's folder as it is: parts
// with slashes between them, each a plain name. The server lists only such files, and the launcher accepts no list
// with any other path.
func LocalPath(path string) bool {
	if path == "" || len(path) > 200 {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if !pathPart.MatchString(part) || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") ||
			pathDevice.MatchString(part) {
			return false
		}
	}
	return true
}
