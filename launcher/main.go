// zombies-launcher is the launcher, Zombies Launcher. It starts a game on a player's PC and joins their room's game on
// a Zombies server (server/launcher.go): Black Ops II, on Plutonium, or Black Ops III, on BOIII (title.go). A browser
// can't do that: such a room's game is the game's own server, and its players join it from their own PCs. What
// follows says it of Black Ops II, and is as true of Black Ops III but where it names Plutonium: that game's part is
// in boiii.go. The launcher's releases are on GitHub
// (.github/workflows/launcher.yml), and the room's page links to the latest release's setup program
// (zombies-launcher-setup.exe). The player runs it once by hand. It then copies itself to its folder
// (%LOCALAPPDATA%\Zombies Launcher), registers the links of its scheme with Windows, and adds itself to the PC's list
// of installed apps, all for the player's Windows user alone. From then on a link on the room's page starts it, and
// Windows' list of apps uninstalls it:
//
//	zombies-launcher.exe                 install it, so that links can start it
//	zombies-launcher.exe zombies-launcher://<host>[:<port>]/play?key=<key>[&of=<title>][&map=<map>][&tls=0]
//	                                     what a link runs: play the game of the player's room; or, while they are in
//	                                     no room, get the PC ready for a title, with a map of it (the lobby's Play
//	                                     local)
//	zombies-launcher.exe zombies-launcher://<host>[:<port>]/play?solo=1&name=<name>[&of=<title>][&map=<map>][&key=<key>][&tls=0]
//	                                     what Launch game runs: start the game for the player alone
//	zombies-launcher.exe uninstall       remove it from the PC
//
// Started by a link, the launcher:
//
//   - asks the player whether this PC plays on the link's server, the first time a link names that server
//     (config.Servers). It does not ask for the first server the PC plays on (trust). The server sends the launcher
//     what the game runs, so it has to be a server of someone the player trusts.
//   - tells the server that it started (launcherapi: POST /launcher/started), which the player's room shows at once.
//     The server takes that call from a launcher of any version.
//   - replaces itself with the latest release of the launcher on GitHub, when that is a later build signed by the
//     launcher's maker (update.go). It looks once each time it goes to play, before it asks the server anything. A
//     server that takes only later versions (launcherapi.MinVersion) so turns none of the older one's calls away, and
//     the player's room has nothing to say of it. When the server doesn't take the launcher and there is no later
//     release, the launcher ends, and says where the latest release is.
//   - on a Mac, ends when the script that runs it there is older than the launcher needs (mac.go). The player's room
//     then shows the line for Terminal that installs the launcher again.
//   - asks the server what to play (launcherapi: GET /launcher/play), with the player's key and its own version. It
//     keeps one such call open while the player is in a Black Ops II room, and the server answers when something
//     changes for the player (server.go: watch). The launcher makes no call on a timer.
//   - makes sure that the PC's Plutonium is of the server's revision, which the server's answer has: a room's game
//     turns away a player's game of another revision. Plutonium is in %LOCALAPPDATA%\Plutonium, where Plutonium's
//     own launcher keeps it too (plutonium.Copy). When the revisions differ, the launcher updates the PC's copy from
//     Plutonium's own updater, as the server does before each game. When they match, it does not contact Plutonium.
//   - finds Black Ops II's game files. It uses the player's own copy (the folder the player chose, or else the one
//     Steam installed), which it only reads. When it finds none, it asks the player (files.go: askFiles). They show
//     it their copy's folder in Windows' folder picker. Or, when the server sends game files, it downloads the
//     server's into a folder of its own, or one they choose, and keeps that folder the same as the server's
//     (sync.go). The menu of its icon in the tray says which folder the game starts from, chooses another, and
//     deletes the game files it downloaded (menu.go).
//   - tells the server which of its maps the game folder has, and whether the folder is the player's own copy
//     (tellHas). A copy of the game without a map's pack lacks the map, and the room then shows the player as missing
//     it. When the player presses the room's button then, and the server sends game files, the launcher asks whether
//     to download the server's and play from them (files.go: askMissing).
//   - waits for the room's game when there is none yet, and starts Plutonium's bootstrapper to join it, in LAN mode,
//     under the player's name in the game, with the game's password (launch.go). For a player in no room, it has the
//     PC ready by then for the title that the link names, with the map that it names, and starts nothing: the
//     lobby's Play local asks for that (play).
//   - stays with the player's game for as long as it runs, with no window, as the player's one launcher. The
//     player's game joins the room's next game from its menu, and the room's game again when the player asks: the
//     launcher types the line that connects it into the game's console (rejoin). When the game can't join that way,
//     the launcher restarts it (stay).
//   - after a try that failed, waits for the player to press the room's button, which then says Try again. It does
//     not try again by itself (idle).
//   - closes its call to the server five minutes after the player left their last Black Ops II room. It then stays
//     in the tray and sends nothing, until the player presses Connect in a room again (idle).
//
// The launcher is built without a console window of its own (-H=windowsgui, .github/workflows/launcher.yml).
// Installing and uninstalling show nothing but a message box at their end, or for a question. A link's run has no
// window either. It writes what it does to its log (launcher.log) and reports it to the server, whose page shows it.
// It then stays in the tray (serve).
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"zombies/internal/boiii"
	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
	"zombies/internal/release"
)

// launcher is one run of the program. It has the folders the run uses, and the functions through which the run
// reaches what is outside the program. A test replaces those functions with its own.
type launcher struct {
	home      string // its folder: the program, its config, and the game files it downloads
	plutonium string // the PC's copy of Plutonium
	manifest  string // the URL of the manifest of Plutonium's updater
	releases  string // the launcher's releases (launcherapi.Releases), which update takes later builds from
	exe       string // the path of the program that runs, or "" when it is not known
	client    *http.Client
	in        *bufio.Reader
	// out is where the launcher prints. A link's run also writes it to launcher.log in the launcher's folder. The
	// file has the last run's output, for a player to send to whoever runs the server when something went wrong.
	// status gets the line that shows how far a download is, again and again. Only the window gets it.
	out, status io.Writer
	// start runs a program with the given command line (Windows' CreateProcess), in dir, and leaves it running. The
	// program is the player's game, which the launcher stays with (stay).
	start func(exe, commandLine, dir string) (game, error)
	// pressed gets a value each time the player asks to join again with the room's button, or, in no room, asks the
	// lobby's Play local for a title or a map of it (link). The button opens a link. That link starts another
	// launcher, which finds this launcher running, signals it, and ends (main). solos gets a value each time the
	// player asks to play alone, with Launch game, in the same way.
	pressed, solos <-chan struct{}
	// launchNow is set by a run that ended because the player asked to launch a game now: with their room's Launch
	// game, while the launcher waited for the room's game (play), or with Play local for the launcher's other title,
	// while a game of this one ran (stayWith). The next run is then for the game alone, as after a press of Launch
	// game (serve). keepsReady is set for that run: the PC has what the room's game needs, as the run before it said,
	// and the room's button goes on saying so while this one starts the game. prepareNow is set by a run that ended
	// because the player asked Play local for the launcher's other title, and not to launch it, while a game of this
	// one ran (stayWith): the next run gets the PC ready for that title, as after a press.
	launchNow, keepsReady, prepareNow bool
	// kept has the games that the launcher left open as it went over to its other title, by the title's id (keep). A
	// run at a title stays with that title's game again, in place of starting one (takeKept, resume). Only the run
	// reads and changes it.
	kept map[string]*openGame
	// clicked is set for a run that the player started with the room's button: the launcher's first, and each one
	// after a press. Such a run may ask the player about a copy of the game that lacks the room's map (askMissing). A
	// run that starts by itself, for the room's next game, asks nothing.
	clicked bool
	// hide hides the launcher's window, once the game it started has a window of its own.
	hide func()
	// rerun starts the launcher again with the same link, to replace this run (update).
	rerun func(exe, link string) error
	// register makes Windows start exe for links of the launcher's scheme, and adds exe to the installed apps, with
	// its version, the number that the list shows beside it. unregister undoes both.
	register   func(exe, version string) error
	unregister func() error
	// later deletes the launcher's program once this run has ended, and then its folder dir if it is empty.
	later func(exe, dir string) error
	// of is the title that the run is at (title), under stateLock: the menu reads it too.
	of *title
	// copies returns the folders where the PC may have a copy of a title that Steam installed.
	copies func(t *title) []string
	// boiii is the folder of the launcher's copy of BOIII's program, and boiiiManifest the address of the manifest of
	// BOIII's updater (boiii.go). steam reports whether the PC has Steam, which BOIII asks for, and probe asks a game
	// on BOIII for the map it is on (probeBoiii), where a test has another. settle and retype are how long such a
	// game is given before a line is typed into its console, and before it is typed again (boiiiGame); 0 for the
	// defaults.
	boiii, boiiiManifest string
	steam                func() bool
	probe                func(port int) (mapName string, ok bool)
	settle, retype       time.Duration
	tick                 time.Duration // between two looks at whether the player's game still runs (stay)
	// joinEvery is how long a game of Black Ops II is given to get into a room's game before the launcher has it ask
	// again (stayWith); 0 for the default, ten seconds.
	joinEvery time.Duration
	// away is how long the launcher keeps its call to the server open with nothing to do: its player is in no Black
	// Ops II room, or the server can't be reached. After that it rests (idle). 0 for no limit.
	away  time.Duration
	poll  time.Duration // between two calls to a server that does not hold calls (server.poll); 0 for the default
	retry time.Duration // before a download's next try, and as long again for each try that received nothing
	cfg   config
	// launched and handed record how the run ended: it started the game, or it started another launcher to replace
	// it. The run's window then has nothing left to show.
	launched, handed bool
	// atExit, when set, is called when the program ends, after its window's last message (uninstall).
	atExit func()
	// notify shows a run's last message, and bad marks the message of a run that failed. confirm asks the player a
	// yes-or-no question. A run that has no window uses Windows' message boxes for both (box, boxAsk). A run that
	// has a window uses the window (tell, yes).
	notify  func(text string, bad bool)
	confirm func(question string) bool
	// boxes is set for a run that has no window. The player can't type an answer there (ask), so the run asks only
	// yes-or-no questions (confirm), or picks a folder in Windows' folder picker (pick) and answers in a task dialog
	// (choose).
	boxes bool
	// pick shows Windows' folder picker, with title, and returns the folder that the player chose, or "" when they
	// cancelled. choose asks question in a task dialog, whose answers are buttons that say what they do. It returns the
	// number of the answer that the player chose, or -1 when they cancelled. A build without the release's manifest
	// can't show a task dialog: choose is nil there, and the run asks yes or no instead (confirm).
	pick   func(title string) (string, error)
	choose func(question string, answers []string) int
	// The run and the tray's menu both use the config and the game files, each on a goroutine of its own. cfgLock
	// guards the config's file (config.go). states is what they share of each title's game files, by the title's id,
	// under stateLock (menu.go), as are the run's title (of) and whether an item of the menu is at work (menuBusy).
	cfgLock, stateLock sync.Mutex
	states             map[string]*gameState
	menuBusy           bool
	// turn gives up the program's turn to run its player's links (single). It is set when the program took the turn
	// at its start.
	turn func()
	// stays marks a launcher that stays in the tray once its link has run, for its player's next game (serve). tray
	// shows the launcher's icon there, with the server's host. The icon's menu has what menu returns as the player
	// opens it: it opens the server's page, shows and changes the game files that the launcher uses (menu.go), and
	// quits. quit ends the program.
	stays bool
	tray  func(host string, menu func() []menuItem) (stop func())
	quit  func()
	// left is the ID of the room's game that the player's game last ran in, once the player's game was closed. The
	// launcher starts the player's game into that room's game again only when the player asks (pressed).
	left string
	// prepared records whether the PC had what the game of the run's title needs when the launcher last checked. A run
	// at another title starts without it (play).
	prepared bool
	// sayLock has one line at a time written to the run's output (say): a map that is downloaded for a game that runs
	// writes its lines beside the run's (stayWith).
	sayLock sync.Mutex
	// logged is set once a launcher that stays has started its log. Its next runs append to the log.
	logged bool
	// key is the public half of the key of the launcher's maker, or nil for a launcher built without one. version is
	// the number of this build, its release, or 0 for a dev build. The launcher replaces itself with another build
	// only when that is a later build of the same maker (update). It sends its version with each call to the server,
	// which takes only versions that its code works with (launcherapi.MinVersion).
	key     ed25519.PublicKey
	version int64
	// programs has each title's own programs, by the title's id: the only programs the launcher accepts from a
	// server. It maps each program's path, in lowercase, to its hash (programs.go).
	programs map[string]map[string]string
	// mac is the version of the script that started the launcher on a Mac (mac.go), or "" where none did.
	mac string
}

// makerKey is the public half of the key that the launcher's maker signs its builds with (release.pub, which make
// launcher-key writes). version is the number of this build. The release workflow sets it to the number in the
// release's tag, launcher-v<version> (.github/workflows/launcher.yml). It is 0 for a build that is no release
// (docker/launcher.sh, go build).
//
//go:embed release.pub
var makerKey string

var version = "0"

func main() {
	// The program has no window when a double click, a link or Windows' list of apps starts it. A test reads its
	// output from a pipe instead. Without a window, the program shows message boxes (boxes, below).
	args := os.Args[1:]
	link := len(args) == 1 && strings.HasPrefix(strings.ToLower(args[0]), launcherapi.Scheme+":")
	// A player has one launcher on a server, which stays for as long as the game it started runs. When a link starts
	// a second launcher for the same player on the same server, the second one signals the first that the player
	// asks to join again, and ends with no window. Two players on one PC each have a launcher.
	local, err := os.UserCacheDir() // %LOCALAPPDATA%
	var turn func()
	var pressed, solos <-chan struct{}
	if link {
		// Record that a launcher runs, until the program ends. Uninstalling checks for it (launchers).
		mark()
		if l, err := parseLink(args[0]); err == nil {
			var ok bool
			if turn, ok = single(l.id()); !ok {
				// the player asks to join again, for a title to be made ready, or to play alone: the title, and
				// the map of it, that the link names are left in a note for the launcher that runs (pressed)
				notePress(filepath.Join(local, appName), l)
				press(l.id(), l.solo)
				return
			}
			pressed, solos = presses(l.id())
		}
	}
	// A launcher that a link started has no window. It writes what it does to its log, reports it to the server for
	// the player's page to show, and asks its questions in Windows' message boxes. A launcher that is started from a
	// terminal prints there.
	boxes := windowless()
	la := &launcher{
		home: filepath.Join(local, appName), plutonium: filepath.Join(local, "Plutonium"),
		manifest: plutonium.Manifest, releases: launcherapi.Releases,
		boiii: filepath.Join(local, appName, "boiii"), boiiiManifest: boiii.Manifest, steam: steamInstalled,
		client: newClient(), in: bufio.NewReader(os.Stdin), out: os.Stdout,
		status: os.Stdout,
		start:  startProgram, rerun: rerun, register: register, unregister: unregister, later: removeLater,
		copies: steamCopies,
		tick:   time.Second, retry: time.Second, away: 5 * time.Minute,
		stays: link, tray: showTray, pick: pickFolder,
	}
	la.notify, la.confirm, la.turn, la.pressed, la.solos, la.hide = la.tell, la.yes, turn, pressed, solos, func() {}
	if boxes {
		la.out, la.status, la.notify, la.confirm, la.boxes = io.Discard, io.Discard, box, boxAsk, true
		la.choose = taskDialogs()
	}
	if err != nil {
		la.notify(fmt.Sprintf("The launcher couldn't find this PC's app data folder: %v", err), true)
		pause(true)
		os.Exit(1)
	}
	if url := os.Getenv("ZOMBIES_PLUTONIUM_URL"); url != "" { // Plutonium's updater, for tests
		la.manifest = url
	}
	if url := os.Getenv("ZOMBIES_BOIII_URL"); url != "" { // BOIII's updater, for tests
		la.boiiiManifest = url
	}
	if url := os.Getenv("ZOMBIES_LAUNCHER_RELEASES"); url != "" { // the launcher's releases, for tests
		la.releases = url
	}
	la.exe, _ = os.Executable()
	la.key, _ = release.ParsePublic(makerKey)
	la.version, _ = strconv.ParseInt(version, 10, 64)
	la.programs = ownPrograms()
	la.mac = os.Getenv(macEnv)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	la.quit = cancel
	code := la.run(ctx, args)
	// When installing was run in a terminal's window, the window stays open until Enter, so that the output can be
	// read.
	if !link && ctx.Err() == nil {
		pause(true)
	}
	if la.atExit != nil {
		la.atExit()
	}
	os.Exit(code)
}

// pause waits for Enter before the window closes, when someone is there to press it.
func pause(wait bool) {
	if !wait || !interactive() {
		return
	}
	fmt.Print("\nPress Enter to close this window.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func (la *launcher) say(format string, args ...any) {
	la.sayLock.Lock()
	defer la.sayLock.Unlock()
	fmt.Fprintf(la.out, format+"\n", args...)
}

// both writes to the window and to the log file, and ignores the errors of each. A window that is gone doesn't end
// the log, and a full disk doesn't end the window's output.
type both struct{ screen, file io.Writer }

func (b both) Write(p []byte) (int, error) {
	b.screen.Write(p)
	b.file.Write(p)
	return len(p), nil
}

// ask prints a question and returns the player's answer, trimmed. It returns "" when no one is there to answer.
func (la *launcher) ask(question string) string {
	fmt.Fprint(la.out, question)
	line, err := la.in.ReadString('\n')
	if err != nil {
		fmt.Fprintln(la.out) // end the question's line, which no Enter ended
	}
	return strings.TrimSpace(line)
}

// tell and yes implement notify and confirm in the launcher's window.
func (la *launcher) tell(text string, bad bool) { la.say("%s", text) }

func (la *launcher) yes(question string) bool {
	answer := strings.ToLower(la.ask(question + "\nType y for yes, or n for no, and press Enter: "))
	return answer == "y" || answer == "yes"
}

// run does what the command line asks for, and returns the program's exit code.
func (la *launcher) run(ctx context.Context, args []string) int {
	var err error
	switch {
	case len(args) == 0:
		err = la.install()
	case len(args) == 1 && args[0] == "uninstall":
		err = la.uninstall()
	case len(args) == 1 && strings.HasPrefix(strings.ToLower(args[0]), launcherapi.Scheme+":"):
		la.clicked = true // the player's click on the room's button started the launcher with the link
		// A launcher that an update put in the place of another (updatedEnv) registers itself again: Windows' list of
		// installed apps then says its version, and not that of the launcher that the player installed by hand.
		if os.Getenv(updatedEnv) != "" && sameFile(la.exe, la.installed()) {
			la.register(la.installed(), strconv.FormatInt(la.version, 10))
		}
		// A link to play alone with no server (link.offline) has the launcher start the game, and end: it has no
		// server to stay for.
		if l, err := parseLink(args[0]); err == nil && l.offline() {
			la.stays = false
		}
		if la.stays {
			return la.serve(ctx, args[0])
		}
		return la.link(ctx, args[0])
	default:
		la.notify("zombies-launcher: the launcher of a Zombies server. Run it with nothing after its name to install it; "+
			"then a room of Black Ops II or Black Ops III on the server's page starts it. \"zombies-launcher uninstall\" "+
			"takes it off the PC again, as uninstalling it in Windows' Settings does.", true)
		return 2
	}
	if err != nil {
		la.notify(err.Error(), true)
		return 1
	}
	return 0
}

// link runs a link and returns the program's exit code. When the run fails, link prints the error while the run's
// output still goes to its log too (play). A run with no window, which a link to play alone with no server is
// (main), shows the error in a message box: no page shows it.
func (la *launcher) link(ctx context.Context, url string) int {
	var srv *server
	l, err := parseLink(url)
	if err == nil {
		var stop func()
		srv, stop = la.open(ctx, l)
		defer stop()
	}
	if err := la.play(ctx, url, srv, l.solo, l, func(err error) {
		la.say("")
		if ctx.Err() != nil { // Ctrl+C
			la.say("Stopped. A download goes on from where it was the next time.")
			return
		}
		la.say("%v", err)
		if la.boxes {
			la.notify(err.Error(), true)
		}
	}); err != nil {
		return 1
	}
	return 0
}

// open returns the link's server. The call that asks it what to play (server.watch) opens when play calls
// srv.start, and stays open until stop is called.
func (la *launcher) open(ctx context.Context, l link) (srv *server, stop func()) {
	srv = newServer(l, la.client)
	srv.version = la.version
	if la.poll > 0 {
		srv.poll = la.poll
	}
	if la.retry > 0 {
		srv.retry = la.retry
	}
	ctx, cancel := context.WithCancel(ctx)
	var done chan struct{}
	srv.start = sync.OnceFunc(func() {
		done = make(chan struct{})
		go func() {
			defer close(done)
			srv.watch(ctx)
		}()
	})
	return srv, func() {
		cancel()
		srv.start() // so that done is set, for a call that never opened: watch then returns at once
		<-done
	}
}

// serve is a link's run for a launcher that stays. It plays the link (play), then waits in the tray for its player's
// next game on the link's server, and plays again. It has no window, so it reports what goes wrong to the server,
// which shows it in the player's page (launcherapi.Report). It ends when the player quits it from the tray, when
// another build replaces it, when the server no longer knows the player (they signed out, and their page starts the
// launcher again), when the server doesn't take this launcher's version, or when the script that runs it on a Mac is
// too old for it (mac.go). The next link then starts the installed launcher, which may be a newer one. When the
// player quits it, or the script is too old, it tells the server so (server.bye), and the room's button opens a
// launcher again at once.
//
// While its player is in a Black Ops II room, it has one call open to the server (open). While nothing changes for
// its player, it does nothing else: it makes no call on a timer, to the server or to Plutonium. Once its player has
// been in no Black Ops II room for a while (away), it closes the call and sends nothing until the player presses a
// room's button again (idle).
func (la *launcher) serve(ctx context.Context, url string) int {
	l, err := parseLink(url)
	if err != nil {
		la.notify(err.Error(), true)
		return 1
	}
	srv, stop := la.open(ctx, l)
	// A launcher that its player quits tells the server so, once its call to the server is closed (stop), as does one
	// that can't play on this Mac until the player installs it again (setup).
	setup := false
	defer func() {
		stop()
		if (ctx.Err() != nil || setup) && !la.handed {
			srv.bye()
		}
	}()
	if la.tray != nil {
		stop := la.tray(l.host, la.menu(l.host, func() { openPage(l.base) }))
		defer stop()
	}
	solo := l.solo // the player asks to play alone, and not in a room
	asked := l     // what they ask for: the link's title and map, or those of their last press of its kind (pressed)
	for {
		err := la.play(ctx, url, srv, solo, asked, func(err error) {
			la.say("%v", err)
			if ctx.Err() == nil {
				srv.fail(ctx, sentence(err), errors.Is(err, errSetup))
			}
		})
		la.keepsReady = false
		setup = errors.Is(err, errSetup)
		switch {
		case ctx.Err() != nil || la.handed:
			return 0
		case ends(err) || setup:
			return 1
		}
		var again bool
		switch {
		case la.launchNow:
			la.launchNow, la.keepsReady, again, solo = false, true, true, true
		case la.prepareNow:
			la.prepareNow, again, solo = false, true, false
		default:
			again, solo = la.idle(ctx, srv)
		}
		asked.of, asked.mapName, _ = pressed(la.home, l, solo)
		if !again {
			// The server was updated while the launcher waited, and takes it no more: a later release takes the link,
			// when there is one.
			if _, _, err := srv.latest(); errors.Is(err, errOld) && ctx.Err() == nil {
				held := func() {}
				la.renew(ctx, url, func() { held = filesTurn(func() {}) }, func() {
					held()
					if la.turn != nil {
						la.turn()
					}
				})
			}
			return 0
		}
	}
}

// renew looks for a later release of the launcher (update). When it took one, it starts that launcher with the link,
// in this run's place, and returns true: the run then ends (handed). files takes the turn to change what the PC's
// launchers share, and giveUp gives up what this run holds, for the launcher that replaces it. When the PC could not
// take the release, renew says so, and the run plays on with the build it is.
func (la *launcher) renew(ctx context.Context, url string, files, giveUp func()) (bool, error) {
	again, err := la.update(ctx, files)
	switch {
	case err != nil:
		la.say("This PC could not take the launcher's latest release, and plays on with this build: %v", err)
		return false, nil
	case !again:
		return false, nil
	}
	giveUp()
	if err := la.rerun(la.installed(), url); err != nil {
		return false, fmt.Errorf("The launcher updated itself, but the new one couldn't be started: %v\nTry again from the page.", err)
	}
	la.handed = true
	return true, nil
}

// idle waits for something to play. It makes no call: it waits for the next answer of the call that is open to the
// server (server.watch), or for the player to press the room's button or ask the lobby's Play local for a title
// (pressed), or to press Launch game (solos).
// It returns true when the player is in a Black Ops II room whose game the player's game isn't in, or when the player
// presses either, and solo when they asked to play alone. It does not return for the room's game that the player's
// game just left: the player has to ask for that one. After a try that failed, it returns only when the player
// presses a button, which then says Try again. The launcher does not try again by itself. It returns false when the
// launcher is to end, because the player quit it, or the server no longer knows the player or doesn't take this
// launcher.
//
// When the player has been in no Black Ops II room for away, or the server could not be reached for as long, the
// launcher rests: it closes its call to the server (server.rest) and sends nothing more. The server then takes the
// launcher as stopped, and a room's button says Connect. A press of that button wakes the launcher, like any
// other press.
func (la *launcher) idle(ctx context.Context, srv *server) (again, solo bool) {
	srv.setReady(la.prepared)
	var since time.Time // when the launcher began to have nothing to do, or zero when its player is in a room
	for {
		play, next, err := srv.latest()
		switch {
		case ends(err):
			return false, false
		case err != nil || play == nil || play.Room == "":
			if since.IsZero() {
				since = time.Now()
			}
		default:
			since = time.Time{}
			if srv.failure() == "" && !play.InGame && (play.Game == nil || la.left == "" || play.Game.ID != la.left) {
				return true, false
			}
		}
		var rest <-chan time.Time
		if la.away > 0 && !since.IsZero() && !srv.rests() {
			rest = time.After(time.Until(since.Add(la.away)))
		}
		select {
		case <-la.pressed:
			srv.resume()
			la.left, la.clicked = "", true
			return true, false
		case <-la.solos:
			srv.resume()
			la.clicked = true
			return true, true
		case <-next:
		case <-rest:
			srv.rest()
		case <-ctx.Done():
			return false, false
		}
	}
}

// play is what a link runs: the room's game, or with solo, a game alone (alone). asked names a title, and a map of
// it: it is the link, or what the player's last press asked for. A game alone is of that title. So is a run for a
// player who is in no room, which gets the PC ready for it, with that map, and starts nothing: the lobby's Play local
// asks for it. When play fails, it calls failed with its error before it returns, while its output still goes to its
// log.
func (la *launcher) play(ctx context.Context, url string, srv *server, solo bool, asked link, failed func(error)) (err error) {
	// closeLog ends the run's log, once it has what the run failed at.
	closeLog := func() {}
	defer func() {
		if err != nil {
			failed(err)
		}
		closeLog()
	}()
	l, perr := parseLink(url)
	turn, taken := la.turn, false // taken: this run took the turn, and gives it up; one the program took is kept
	if turn == nil && perr == nil {
		var ok bool
		if turn, ok = single(l.id()); !ok {
			return errors.New("The launcher is already running for this server, and is busy.")
		}
		taken = true
	}
	release := sync.OnceFunc(func() {
		if turn != nil && (taken || !la.stays) { // a launcher that stays keeps its turn for its next run of the link
			turn()
		}
	})
	defer release()
	if err := os.MkdirAll(la.home, 0o755); err == nil {
		// The log has the last run. A launcher that stays appends each of its runs to it.
		keep := os.O_TRUNC
		if la.logged {
			keep = os.O_APPEND
		}
		if f, err := os.OpenFile(filepath.Join(la.home, "launcher.log"), os.O_CREATE|os.O_WRONLY|keep, 0o644); err == nil {
			la.logged = la.stays
			screen := la.out
			la.out = both{screen, f}
			closeLog = func() {
				la.out = screen
				f.Close()
			}
			fmt.Fprintf(f, "%s\n", time.Now().Format("2006-01-02 15:04:05 -0700"))
		}
	}
	la.say(appName)
	la.say("")
	if perr != nil {
		return perr
	}
	la.loadConfig()
	// A link to play alone with no server has the launcher call none, and start nothing that a server sent. There is
	// nothing to trust (trust).
	offline := l.offline()
	if offline {
		srv = nil
	} else if err := la.trust(l); err != nil {
		return err
	}
	if solo && !offline && !la.keepsReady {
		srv.setReady(false) // until the game starts, the page's button says what the launcher does
	}
	// Only one launcher at a time changes what the PC's launchers share: their own program, the copy of Plutonium
	// and the game files. If another player's launcher on this PC is changing them now, this launcher waits for it.
	// files takes the turn, unless the run has it, and done gives it up.
	var held func()
	files := func() {
		if held == nil {
			held = filesTurn(func() {
				la.say("Another player's launcher on this PC is getting its files: waiting for it...")
			})
		}
	}
	done := func() {
		if held != nil {
			held()
			held = nil
		}
	}
	defer done()
	// giveUp gives up what the run holds, for the launcher that replaces it (renew).
	giveUp := func() {
		done()
		release()
		if la.stays && turn != nil && !taken {
			turn() // the launcher that replaces this one takes the turn
		}
	}
	// The launcher tells the server that it started, which the player's room shows at once (hello). It then looks for
	// a later release of itself before it asks the server anything, and hands the link to that one. A server that
	// takes only later versions than this one's then turns none of this one's calls away, and the player's room has
	// nothing to say of it: the server takes the call that says the launcher started from a launcher of any version.
	if !offline {
		srv.hello(ctx)
	}
	// A launcher that left a game open, as it went over to its other title (keep), does not replace itself now: the
	// launcher that took its place would know nothing of that game, and would start it a second time. It does once
	// the server takes this build no more (below).
	keeps := la.keeping()
	if !keeps {
		if again, err := la.renew(ctx, url, files, giveUp); again || err != nil {
			return err
		}
	}
	if offline {
		if macOld(la.mac) {
			return errSetup
		}
		t := titleOf(asked.of)
		if t == nil {
			return errLink
		}
		la.setTitle(t)
		files()
		return la.alone(ctx, nil, l, &launcherapi.Play{Player: l.name}, asked.mapName, done)
	}
	srv.start()
	if la.stays {
		srv.fail(ctx, "", false) // a new try: the last try's failure is no longer reported
	}
	play, err := srv.current(ctx)
	switch {
	case errors.Is(err, errOld): // the server doesn't take this launcher, and there was no later release to take
		if keeps { // or the launcher looked for none, for the game that it left open
			if again, err := la.renew(ctx, url, files, giveUp); again || err != nil {
				return err
			}
		}
		return la.tooOld()
	case err != nil:
		return err
	case macOld(la.mac):
		// On a Mac, the script that runs the launcher may be older than this build needs: it does not update itself,
		// as the launcher just did (mac.go).
		return errSetup
	}
	// The run is at one title: the one that the player asked to play alone, or the title of their room, or, while
	// they are in no room that plays a title of the launcher's, the one that they asked for there (Black Ops II
	// where they named none). The server is told which, before anything that the launcher says of the PC (server.at).
	t := titleOf(asked.of)
	if !solo && play.Room != "" {
		t = titleOf(play.Of)
	}
	if t == nil {
		return la.tooOld() // a game that only a later launcher starts
	}
	// The map that the player asked for by its name: with a link that launches a game, or from the lobby's Play
	// local, which a player in no room is at. A room's map is the room's (play.Needs).
	picked := ""
	if solo || play.Room == "" {
		picked = asked.mapName
	}
	if la.title() != t {
		// What the PC had ready was for another title's game. Until this run has seen to this title's, a run that
		// ends tells the server nothing else (idle).
		la.prepared = false
	}
	la.setTitle(t)
	srv.at(t)
	// The title's game may be open still, from before the launcher went over to its other title: the run stays with
	// that game again, and starts none.
	if k := la.takeKept(t); k != nil {
		la.say("You are %s on %s.", play.Player, l.host)
		return la.resume(ctx, srv, l, play, k, solo, picked)
	}
	files()
	la.say("You are %s on %s.", play.Player, l.host)
	if solo {
		return la.alone(ctx, srv, l, play, picked, done)
	}
	// The launcher now prepares the PC, and then reports that the PC has what the game needs (setReady). A room's
	// game waits for a player whose launcher is still preparing, however long a first download of the game files
	// takes. The call that is open to the server shows it that the launcher runs through all of this.
	if err := la.haveClient(ctx, srv, play); err != nil {
		return err
	}
	la.doing(ctx, srv, "Checking files")
	// The game is to have the room's map, or, for a player in no room, the one that they asked for.
	n := need{zone: play.Needs}
	if play.Room == "" {
		n = need{pack: picked}
	}
	dir, err := la.gameFiles(ctx, srv, l, play, n)
	if err != nil {
		return err
	}
	done()
	if zones := info(play, t).Zones; len(zones) > 0 {
		la.tellHas(srv, dir, zones) // which maps the game folder has, for the page to say (launcherapi.Title)
	}
	srv.setReady(true)
	la.prepared = true
	la.doing(ctx, srv, "")
	// Wait for the room's game. The answers come from the call that is open to the server, which also tries again
	// by itself when the server can't be reached for a while.
	waiting := false
	for {
		play, next, err := srv.latest()
		switch {
		case errors.Is(err, errUnknown):
			return err
		case errors.Is(err, errOld): // the server was updated, and takes this launcher no more
			if again, err := la.renew(ctx, url, files, giveUp); again || err != nil {
				return err
			}
			return la.tooOld()
		case err != nil: // the server can't be reached now: the call tries again by itself
		case play.Room != "" && titleOf(play.Of) != t:
			return nil // the room plays another title now: the next run is at that one (serve)
		case play.Game != nil && la.left != "" && play.Game.ID == la.left:
			return nil // the game the player's game left: the player has to ask for it (idle)
		case play.Game != nil && t == titleT7 && !hasZone(dir, play.Needs):
			// The folder lacks the room's map, and BOIII's game would only say so. The room says that the map is
			// missing, and its button downloads it where the server sends game files (pressed, below).
		case play.Game != nil:
			return la.stay(ctx, srv, l, play, dir)
		case play.InGame:
			la.say("Your game is in the room's game already.")
			return nil
		case play.Room == "":
			la.say("")
			la.say("This PC is ready for %s. You are in no room that plays it now.", t.name)
			return nil
		case !waiting:
			waiting = true
			la.say("")
			la.say("Waiting for room %s's game to start.", play.Room)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-la.pressed:
			// While the game folder lacks the room's map, the room's button says Download. The run then starts again
			// (serve), and asks the player what to do (askMissing). Any other press has nothing to do here.
			if now, _, err := srv.latest(); err == nil && now != nil && !hasZone(dir, now.Needs) {
				la.clicked = true
				return nil
			}
		case <-la.solos:
			// The room's Launch game, or the lobby's Play local: the player's game starts now, at its menu, and joins
			// the room's game from there once that is up. The next run starts it (serve, alone).
			la.clicked, la.launchNow = true, true
			return nil
		case <-next:
		}
	}
}

// trust decides whether this PC plays on the link's server, and returns an error when it does not. That server sends
// what the launcher starts: the game files. The launcher accepts the first server it is opened for without a
// question. Its player installed the launcher from that server's page a moment ago, and their browser asked whether
// the server's page may open it. A link to any other server starts nothing until the player agrees here, once for
// each server. Any page may have such a link, and the browser's question names only the page.
// A server on plain HTTP has to be in the player's own network: anyone between the PC and a server further away
// could send those files in its place.
func (la *launcher) trust(l link) error {
	if !l.tls && !nearby(l.host) {
		return fmt.Errorf("%s isn't a secure (https) address, and isn't on your own network, so the launcher won't play there.", l.base)
	}
	if slices.Contains(la.cfg.Servers, l.base) {
		return nil
	}
	if len(la.cfg.Servers) > 0 {
		if !la.confirm(fmt.Sprintf("Play on %s?\n\nThis PC hasn't played there before, only on %s. That server can send "+
			"game files that the launcher runs, so say yes only if you trust it.", l.host, strings.Join(la.cfg.Servers, ", "))) {
			return errors.New("Nothing was started.")
		}
	}
	return la.change(func(c *config) {
		if !slices.Contains(c.Servers, l.base) {
			c.Servers = append(c.Servers, l.base)
		}
	})
}

// haveClient makes sure that the PC has what the run's title plays on, for the room's game: Plutonium of the
// server's revision (matchPlutonium), or BOIII (haveBoiii).
func (la *launcher) haveClient(ctx context.Context, srv *server, play *launcherapi.Play) error {
	if la.title() == titleT7 {
		return la.haveBoiii(ctx, srv)
	}
	return la.matchPlutonium(ctx, srv, info(play, titleT6).Plutonium)
}

// matchPlutonium makes sure that the PC's copy of Plutonium can join the server's games. A room's game accepts only
// a player's game of the server's revision of Plutonium, which the server's answer has. When the PC's copy is
// complete and of that revision, matchPlutonium does nothing, and the launcher does not contact Plutonium at all.
// Otherwise it brings the copy up to date from Plutonium's updater, which has the current revision. The server does
// the same before each game, so the two then match. When the server states no revision (0: it has no copy yet), the
// launcher has nothing to match and updates its copy.
func (la *launcher) matchPlutonium(ctx context.Context, srv *server, revision int) error {
	c := &plutonium.Copy{Dir: la.plutonium, Needs: plutonium.T6, State: "zombies-launcher-files.json"}
	if revision != 0 && c.Revision() == revision && c.Complete() {
		return nil
	}
	ready := srv.ready.Load()
	srv.setReady(false)
	la.doing(ctx, srv, "Updating Plutonium")
	if err := la.getPlutonium(ctx); err != nil {
		return err
	}
	srv.setReady(ready)
	return nil
}

// getPlutonium brings the PC's copy of Plutonium up to date, for T6's Zombies.
func (la *launcher) getPlutonium(ctx context.Context) error {
	c := &plutonium.Copy{Dir: la.plutonium, URL: la.manifest, Needs: plutonium.T6, Client: la.client,
		State: "zombies-launcher-files.json",
		Fetching: func(files int, bytes int64) {
			la.say("Getting Plutonium from its own servers: %d files, %.0f MB...", files, float64(bytes)/1e6)
		},
		Logf: func(format string, args ...any) {
			if strings.Contains(format, "no word of updates") {
				la.say("Plutonium's servers can't be reached: the game starts with the Plutonium this PC has.")
			}
		}}
	if err := c.Update(ctx); err != nil {
		return fmt.Errorf("%v\n\n%s", sentence(err), la.plutoniumHint())
	}
	return nil
}

// plutoniumHint returns advice for the player about what usually keeps the launcher from Plutonium's files.
func (la *launcher) plutoniumHint() string {
	return "If Plutonium or the game is open, close it and try again. If your antivirus removed the file, add an " +
		"exception for Plutonium's folder:\n  " + la.plutonium
}

// sentence returns an error's text as a sentence, with a capital first letter and a full stop.
func sentence(err error) string {
	s := err.Error()
	if s == "" {
		return s
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// launch starts the player's game, which joins the room's game: play.Game. The server brought its copy of Plutonium
// up to date for that game. When that gave the server another revision, the PC's copy follows first
// (matchPlutonium). launch returns the folder that the game started from, and the room's game that the player's game
// is on its way into: play.Game for Black Ops II, whose command line joins it. Black Ops III starts at its menu, and
// joins once a line is typed into its console (launchBoiii): to is nil, and stayWith has the line typed.
func (la *launcher) launch(ctx context.Context, srv *server, l link, play *launcherapi.Play, dir string) (g game, from string, to *launcherapi.Game, err error) {
	if la.title() == titleT7 {
		g, from, err = la.launchBoiii(ctx, srv, play, dir)
		return g, from, nil, err
	}
	g, from, err = la.launchT6(ctx, srv, l, play, dir)
	return g, from, play.Game, err
}

func (la *launcher) launchT6(ctx context.Context, srv *server, l link, play *launcherapi.Play, dir string) (game, string, error) {
	if rev := info(play, titleT6).Plutonium; rev != 0 {
		if err := la.matchPlutonium(ctx, srv, rev); err != nil {
			return nil, "", err
		}
	}
	to := play.Game
	dir, err := la.startFrom(dir) // or the folder the player chose in the tray's menu since
	if err != nil {
		return nil, "", err
	}
	exe := filepath.Join(la.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	line, err := commandLine(exe, dir, to.Name, to.Password, l.host+":"+strconv.Itoa(to.Port))
	if err != nil {
		return nil, "", err
	}
	g, err := la.start(exe, line, la.plutonium)
	if err != nil {
		return nil, "", fmt.Errorf("Plutonium couldn't be started: %v\n\n%s", err, la.plutoniumHint())
	}
	la.started(g)
	la.launched = true
	la.tellPlutonium(dir)
	la.say("")
	la.say("Black Ops II is starting, and joins the room's game as %s.", to.Name)
	return g, dir, nil
}

// game is the player's game that the launcher started. running reports whether it still runs, and stop closes it.
// join types a line into the game's console, as if the player typed it there (consoleLine).
type game interface {
	running() bool
	stop()
	join(line string) error
}

// joinTries is how often the launcher has a game of Black Ops II ask a room's game to let it in, at most, for as
// long as the server doesn't have the game in it (stayWith).
const joinTries = 3

// rejoins reports whether the player's game, which runs, can join the room's game, play.Game, without starting again.
// rev is the revision of Plutonium that the game started with, and from the folder it started from. A game of Black
// Ops II can't join when the room's game's Plutonium is of another revision: a room's game turns away a game of
// another revision, and the PC's copy can't be brought up to date while the game runs. No game can when the player
// chose another folder in the tray's menu than the one the game started from: the game's next start uses that folder,
// which may have the room's map where the other lacks it. A name that the player asked for since the game started
// doesn't count: the game plays under the name it started with for as long as it runs (consoleLine).
func (la *launcher) rejoins(play *launcherapi.Play, dir, from string, rev int) bool {
	next, err := la.startFrom(dir)
	if err != nil || next != from {
		return false
	}
	if la.title() == titleT7 {
		return true
	}
	now := info(play, titleT6).Plutonium
	return now != 0 && now == rev
}

// rejoin has the player's game, which runs, join the room's game, play.Game, once that is up. The game's console
// connects it there, as the player could by hand, so the game doesn't start again. When the server ends a room's
// game, it quits, and its players' games go back to their menus (server/t6.go: t6Script), where they wait for the
// room's next game. rejoin returns false when the console can't be reached: the game then has to start again.
func (la *launcher) rejoin(g game, l link, play *launcherapi.Play) bool {
	to := play.Game
	if err := la.typeJoin(g, l, to); err != nil {
		la.say("The game that runs couldn't join the room's game (%v): it starts again.", err)
		return false
	}
	la.say("")
	la.say("%s joins the room's game as %s.", la.title().name, to.Name)
	return true
}

// typeJoin types the line into the console of the player's game g that has it ask the room's game to to let it in
// (consoleLine).
func (la *launcher) typeJoin(g game, l link, to *launcherapi.Game) error {
	line, err := consoleLine(to.Password, l.host+":"+strconv.Itoa(to.Port))
	if err != nil {
		return err
	}
	return g.join(line)
}

// stay starts the player's game, which joins the room's game, and stays with it for as long as it runs, so that the
// player has one game, whatever they press. The launcher has no window of its own once the game has one. stay
// returns when the game is closed. Until then this is the player's launcher, and another link of theirs finds it
// running (single). stay reads the server's answers from the call that is open to it (server.watch).
//
// The player's game joins the room's game as it runs (rejoin): the room's next game, which has another ID than the
// one the player's game joined, and the same game when the player asks to join it again (pressed) while the server
// has the player's game in no room's game. When the server ends a room's game, the player's game is back at its menu.
// It joins a game once that is up. When the game that runs can't join (rejoins), stay closes it and starts it again,
// at once: a game that starts takes longer than the room's does.
func (la *launcher) stay(ctx context.Context, srv *server, l link, play *launcherapi.Play, dir string) error {
	g, from, to, err := la.launch(ctx, srv, l, play, dir)
	if err != nil {
		return err
	}
	return la.stayWith(ctx, srv, l, g, dir, from, to, info(play, la.title()).Plutonium, staying{})
}

// The launcher starts two titles, and a player may have a game of each open. The launcher is at one title at a time
// (title.go), and stays with that title's game (stayWith). When it goes over to the other title, because the player's
// room plays that one now, or because they asked for it with Play local, it leaves the game that runs open, and keeps
// what it knows of it (keep). When it comes back to the title, it stays with that game again, in place of starting a
// second one (takeKept, resume). So a room that goes from one title to the other and back costs its players no game
// that they had open, and none of what they had set in it.

// openGame is a game that the launcher started, as stayWith knows it: the game, the folder that the run found for
// it (dir), the folder that it started from (from), and the revision of Plutonium that it started with (rev).
type openGame struct {
	g         game
	dir, from string
	rev       int
}

// keep leaves the game k of the title t open, as the launcher goes over to its other title.
func (la *launcher) keep(t *title, k *openGame) {
	if la.kept == nil {
		la.kept = map[string]*openGame{}
	}
	la.kept[t.id] = k
	la.say("")
	la.say("%s stays open, for when you come back to it.", t.name)
}

// takeKept returns the game of the title t that the launcher left open (keep), for the run to stay with again, or nil
// when it left none, or the player has closed it since.
func (la *launcher) takeKept(t *title) *openGame {
	k := la.kept[t.id]
	delete(la.kept, t.id)
	if k == nil || !k.g.running() {
		return nil
	}
	return k
}

// keeping reports whether a game that the launcher left open still runs (keep). It forgets those that the player has
// closed.
func (la *launcher) keeping() bool {
	for id, k := range la.kept {
		if !k.g.running() {
			delete(la.kept, id)
		}
	}
	return len(la.kept) > 0
}

// resume stays with the game k of the run's title again, which the launcher left open as it went over to its other
// title (keep): for the player's room, or with alone, as they asked for the game with Play local. The PC has what the
// game needs, as the game runs, so the launcher checks nothing, and changes none of the files that the game has open.
// It says which maps the game's folder has, and that the PC is ready. A map that the player asked for by its name
// (picked) is got beside the game, as when they ask for one while it runs (stayWith).
func (la *launcher) resume(ctx context.Context, srv *server, l link, play *launcherapi.Play, k *openGame, alone bool, picked string) error {
	t := la.title()
	la.say("")
	la.say("%s is open already: the launcher stays with it.", t.name)
	if zones := info(play, t).Zones; len(zones) > 0 {
		la.tellHas(srv, k.from, zones)
	}
	la.prepared = true
	srv.setReady(true)
	return la.stayWith(ctx, srv, l, k.g, k.dir, k.from, nil, k.rev, staying{alone: alone, resumed: true, pick: picked})
}

// tellHas tells the server which of its maps the game folder dir has, by their zones, and whether the folder is the
// player's own copy of the game. The launcher downloads a map only into the folder of the game files that it
// downloaded itself (downloads), so the page offers a map's download only for that one.
func (la *launcher) tellHas(srv *server, dir string, zones []string) {
	srv.setHas(zonesIn(dir, zones), dir != la.downloads())
}

// staying is how stayWith begins with a game. alone: the game was started, or asked for, with Launch game, and for
// no room's game. resumed: the game runs already (resume), so nothing launches. pick: a map that is got for the game
// at once (getPicked), or "".
type staying struct {
	alone, resumed bool
	pick           string
}

// aloneStarts is how long the page's button says that the player's game launches, when it launches for the player
// alone (alone). No room's game says when it is in.
var aloneStarts = 15 * time.Second

// launching is what the room's button says while the player's game launches (stayWith).
const launching = "Launching the game"

// stayWith is stay, from the start of the player's game g: from the folder from, which the run found as dir, into the
// room's game to, with Plutonium of the revision rev. For a game alone (alone), to is nil: the game is at its menu,
// with the PC's Plutonium, and the player's next room's game joins from there. A game of Black Ops III that starts
// for a room's game has no to either: it joins from its menu too, once the room's game is up (launch).
//
// stayWith ends when the launcher goes over to its other title: the player's room plays that one now, or they asked
// for it with Play local. The next run sees to that title's game (serve). A launcher that stays leaves this game
// open, and stays with it again when it comes back to this title (keep). One that ends with its run closes the game:
// nothing would know it afterwards.
func (la *launcher) stayWith(ctx context.Context, srv *server, l link, g game, dir, from string, to *launcherapi.Game, rev int, how staying) error {
	t := la.title()
	alone := how.alone
	la.hide()
	// The player's game runs from here until it is closed. The room's button shows what the launcher does with it
	// until the server has it in the room's game: the game launches, or the game that runs joins the room's game. Once
	// the server has the player's game in, the launcher has nothing to report. When the player's game is out of the
	// room's game again, the button then asks to join again (pressed). The page's button for a game alone says that
	// the game launches for aloneStarts. The player's own game launches, where the room's game starts: the room says
	// that the room's starts, beside its code, as the player's launches.
	status, begun := launching, time.Now()
	if how.resumed {
		status = "" // the game runs already
	}
	show := func(what string) {
		if what != status {
			status = what
			la.doing(ctx, srv, what)
		}
	}
	la.doing(ctx, srv, status)
	defer la.doing(context.WithoutCancel(ctx), srv, "")
	// The server hears that the player's game runs, for as long as it does: the player's room offers to launch the
	// game until then, and again once it is closed.
	srv.setGame(true)
	defer srv.setGame(false)
	select {
	case <-la.pressed: // drop a press that came before the game was started: it was for that start
	default:
	}
	// A map that the player asks for while the game runs is downloaded beside this loop (getPicked), which goes on
	// seeing to the game and to the room meanwhile: a download takes minutes. picking is closed once the download has
	// ended, and is nil when there is none. endPick stops the download and waits for it. That is done before the game
	// is started again, and as stayWith ends: the next run brings the same folder up to date.
	var picking chan struct{}
	stopPick := func() {}
	endPick := func() {
		if picking != nil {
			stopPick()
			<-picking
			picking = nil
		}
	}
	defer endPick()
	// pick starts the download of a map that the player asked for, where they named one. One map is got at a time.
	pick := func(mapName string) {
		if picking != nil {
			select {
			case <-picking:
				picking = nil
			default:
			}
		}
		if picking == nil && mapName != "" {
			pickCtx, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			picking, stopPick = done, cancel
			go func(from string) {
				defer close(done)
				defer cancel()
				la.getPicked(pickCtx, srv, from, mapName)
			}(from)
		}
	}
	pick(how.pick)
	// other leaves the game as the launcher goes over to its other title. A launcher that stays leaves it open, and
	// stays with it again later (keep): every launcher that a player's link starts for a server is one (run). One
	// that ends with its run (link) closes it, as it did before the launcher kept a game.
	other := func() {
		if la.stays {
			la.keep(t, &openGame{g: g, dir: dir, from: from, rev: rev})
		} else {
			g.stop()
		}
	}
	asked := false // the player asked to join the room's game again
	// A game of Black Ops II asks a room's game once to let it in, and never again when no answer comes: Plutonium
	// sends one packet for it, which the server drops until the room's game is up. A game that started sooner than
	// the room's game did, as on a fast PC, is so not in it, however long it waits, and nor is one whose packet was
	// lost on the way. So the launcher has the game ask from its console as soon as the room's game is up, also when
	// the game's own command line asks (launchT6), and again every joinEvery for as long as the server doesn't have
	// the game in the room's game, joinTries times at most. Nothing is lost by it: a line typed into a game that is
	// still starting waits in the game's console until the game takes it, and a game that asks twice joins as one
	// that asks once. typed is when the launcher last had the game ask the room's game to, and tries how often. Once
	// the server has had the game in the room's game, the launcher has it ask no more by itself: a game that is out
	// of the room's game again joins when its player asks (asked). A game on BOIII sees to its own line
	// (boiiiGame.watch).
	every := la.joinEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	var typed time.Time
	tries := 0
	for g.running() && ctx.Err() == nil {
		play, next, err := srv.latest()
		ok := err == nil && play != nil
		if ok && play.Room != "" && titleOf(play.Of) != t {
			other()
			return nil
		}
		if ok && play.InGame {
			show("")
			asked = false
			tries = joinTries // a game that was in the room's game, and is out of it again, asks when the player does
		}
		if alone && to == nil && status == launching && time.Since(begun) >= aloneStarts {
			show("")
		}
		again := false
		if t == titleT6 && ok && play.Game != nil && to != nil && play.Game.ID == to.ID && play.Game.Up && !asked &&
			tries < joinTries && time.Since(typed) >= every {
			// The room's game is up, and the player's game is not in it. A try that the console can't be reached for
			// counts as one: the game is not started again for it, as it may be on its way in. The room's button
			// says what it said: the game launches, or joins.
			if tries > 0 {
				la.say("")
				la.say("%s is not in the room's game yet, and asks again.", t.name)
			}
			if la.rejoins(play, dir, from, rev) {
				la.typeJoin(g, l, play.Game)
			}
			typed, tries = time.Now(), tries+1
		}
		if ok && play.Game != nil && (to == nil || play.Game.ID != to.ID || asked) {
			switch {
			case t == titleT7 && !hasZone(from, play.Needs):
				// The folder lacks the room's map. Where it is the launcher's own, and the server sends game files, the
				// next run downloads the map, which the game is closed for. A player's own copy stays as it is, and
				// the room says that the map is missing.
				if from == la.downloads() && info(play, t).Downloads {
					g.stop()
					return nil
				}
			case !la.rejoins(play, dir, from, rev):
				again = true
			case !play.Game.Up: // the player's game joins it once it is up
				if status != launching {
					show("Joining the game")
				}
			case la.rejoin(g, l, play):
				show("Joining the game")
				to, asked = play.Game, false
				typed, tries = time.Now(), 1
				continue
			default:
				again = true
			}
		}
		if !again {
			select {
			case <-la.pressed:
				// In a room, the room's button: the player asks to join its game again. In no room, the lobby's Play
				// local, which asks for a title, and maybe for a map of it (pressed). A map of this game's title is
				// got for the game that runs. For the launcher's other title, a launcher that stays goes over to
				// that one, and this game stays open (keep): the next run gets the PC ready for it (serve).
				of, mapName, noted := pressed(la.home, l, false)
				switch asks := titleOf(of); {
				case !ok || play.Room != "" || !noted:
					asked = true
				case asks == t:
					pick(mapName)
				case asks != nil && la.stays:
					other()
					la.clicked, la.prepareNow = true, true
					return nil
				}
			case <-la.solos:
				// Launch game, of the lobby's Play local or of the room, while the player's game runs. A press for the
				// launcher's other title has a launcher that stays go over to that one: the next run starts its game,
				// or stays with it, as after a press while the launcher waits (play, serve), and this game stays open
				// (keep). Not while this game is in a room's game, which the launcher sees to, nor without the press's
				// note of the title that it asks for.
				of, mapName, noted := pressed(la.home, l, true)
				asks := titleOf(of)
				if la.stays && noted && !(ok && play.InGame) && asks != nil && asks != t {
					other()
					la.clicked, la.launchNow = true, true
					return nil
				}
				// A press for the game that runs: the map that it names is got for it.
				if asks == t {
					pick(mapName)
				}
			case <-next:
			case <-time.After(la.tick):
			case <-ctx.Done():
			}
			continue
		}
		asked = false
		endPick()
		g.stop()
		if g, from, to, err = la.launch(ctx, srv, l, play, dir); err != nil {
			if la.stays {
				return err // the player's page shows it
			}
			la.notify(err.Error(), true)
			return nil
		}
		rev, alone = info(play, t).Plutonium, false
		typed, tries = time.Time{}, 0
		status, begun = launching, time.Now() // also after "Updating Plutonium", which launch may have reported
		la.doing(ctx, srv, status)
	}
	if to != nil {
		la.left = to.ID
	}
	return nil
}

// getPicked downloads the map mapName, which the player asked for by its name while their game runs, from the
// folder from: a map of the run's title that the server sends in a pack of its own, into the launcher's
// own game files, where they lack it. The game then has the map without starting again. Nothing else of the folder is
// brought up to date: the game has those files open. A player's own copy is left as it is. stayWith runs it on a
// goroutine of its own, and stops it by its context.
func (la *launcher) getPicked(ctx context.Context, srv *server, from, mapName string) {
	t := la.title()
	play, _, err := srv.latest()
	if err != nil || play == nil || mapName == "" || from != la.downloads() || !info(play, t).Downloads {
		return
	}
	zones := info(play, t).Zones
	release := filesTurn(func() {})
	defer release()
	list, err := srv.files(ctx, t)
	if err != nil {
		return
	}
	n := need{pack: mapName}.resolve(list, zones)
	if n.zone == "" || hasZone(from, n.zone) {
		return
	}
	la.say("Getting %s for the game that runs.", mapName)
	if _, err := la.sync(ctx, srv, from, list, map[string]bool{n.pack: true}, zones); err != nil {
		la.say("%v", err)
		if ctx.Err() == nil {
			srv.fail(ctx, sentence(err), false)
		}
		return
	}
	la.tellHas(srv, from, zones)
	la.found(from)
}

// alone prepares the PC and starts the player's game at its menu, for them to play alone: Launch game, of the lobby's
// Play local or of their room (launcherapi). Their game joins no server's, so any revision of Plutonium does. The
// PC's copy does as it is, and the launcher contacts Plutonium only when the copy is not complete, as before the PC's
// first game (havePlutonium). A PC that has played so plays alone with no internet. The game files are found as for a
// room's game (gameFiles).
//
// srv is the link's server, or nil for a link with none (link.offline). With a server, the launcher reports what it
// does, which the page's button shows, and stays with the game as with a room's (stayWith): when the player then
// readies up in a room, their game joins the room's game from its menu. With none, it starts the game and ends. done
// gives up the turn to change the PC's files, once the launcher no longer changes them.
//
// mapName is the map of the title that the link names, or "": the room's, from a room. Where the server sends the
// title's game files a map at a time, and the game's folder is the launcher's own, the launcher gets that map's first.
func (la *launcher) alone(ctx context.Context, srv *server, l link, play *launcherapi.Play, mapName string, done func()) error {
	t := la.title()
	if t == titleT7 {
		if err := la.haveBoiii(ctx, srv); err != nil {
			return err
		}
	} else if err := la.havePlutonium(ctx, srv); err != nil {
		return err
	}
	la.doing(ctx, srv, "Checking files")
	dir, err := la.gameFiles(ctx, srv, l, play, need{pack: mapName})
	if err != nil {
		return err
	}
	done()
	if zones := info(play, t).Zones; srv != nil && len(zones) > 0 {
		la.tellHas(srv, dir, zones) // which maps the game folder has, for the page to show
	}
	from, err := la.startFrom(dir) // or the folder the player chose in the tray's menu since
	if err != nil {
		return err
	}
	var g game
	rev := 0
	if t == titleT7 {
		if g, err = la.startBoiii(from, play.Player); err != nil {
			return err
		}
	} else {
		exe := filepath.Join(la.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
		line, err := aloneLine(exe, from, play.Player)
		if err != nil {
			return err
		}
		if g, err = la.start(exe, line, la.plutonium); err != nil {
			return fmt.Errorf("Plutonium couldn't be started: %v\n\n%s", err, la.plutoniumHint())
		}
		rev = (&plutonium.Copy{Dir: la.plutonium, Needs: plutonium.T6, State: "zombies-launcher-files.json"}).Revision()
	}
	la.started(g)
	la.launched = true
	if t == titleT6 {
		la.tellPlutonium(from)
	}
	la.say("")
	la.say("%s is starting at its menu, for you to play as %s.", t.name, gameName(play.Player))
	if srv == nil {
		return nil
	}
	la.prepared = true
	srv.setReady(true)
	return la.stayWith(ctx, srv, l, g, dir, from, nil, rev, staying{alone: true})
}

// launchBoiii starts the player's Black Ops III for the room's game, play.Game, from the game files in dir, or the
// folder that the player chose in the tray's menu since, which it returns. The game starts at its menu, under the
// name that the room's game knows the player by. It joins the room's game once that is up, and the game is at its
// menu: stayWith then has the line typed that connects it (rejoin, boiii.go).
func (la *launcher) launchBoiii(ctx context.Context, srv *server, play *launcherapi.Play, dir string) (game, string, error) {
	if err := la.haveBoiii(ctx, srv); err != nil {
		return nil, "", err
	}
	dir, err := la.startFrom(dir)
	if err != nil {
		return nil, "", err
	}
	g, err := la.startBoiii(dir, play.Game.Name)
	if err != nil {
		return nil, "", err
	}
	la.started(g)
	la.launched = true
	la.say("")
	la.say("Black Ops III is starting, and joins the room's game as %s.", boiiiName(play.Game.Name))
	return g, dir, nil
}

// havePlutonium makes sure that the PC has Plutonium, for a game alone (alone): a complete copy, of any revision.
func (la *launcher) havePlutonium(ctx context.Context, srv *server) error {
	c := &plutonium.Copy{Dir: la.plutonium, Needs: plutonium.T6, State: "zombies-launcher-files.json"}
	if c.Complete() {
		return nil
	}
	la.doing(ctx, srv, "Updating Plutonium")
	return la.getPlutonium(ctx)
}

// doing reports to the server what a launcher that stays does now to prepare the PC, for the room's button to show.
// Such a launcher has no window of its own to show it in. A download reports how far it is instead (sync).
func (la *launcher) doing(ctx context.Context, srv *server, what string) {
	if la.stays && srv != nil {
		srv.report(ctx, launcherapi.Report{Doing: what})
	}
}
