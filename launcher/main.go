// zombies-launcher is the launcher, Zombies Launcher. It starts Black Ops II on a player's PC and joins their room's
// game on a Zombies server (server/launcher.go). A browser can't do that: a Black Ops II room's game is Plutonium's
// server, and its players join it from their own PCs. The launcher's releases are on GitHub
// (.github/workflows/launcher.yml), and the room's page links to the latest release's setup program
// (zombies-launcher-setup.exe). The player runs it once by hand. It then copies itself to its folder
// (%LOCALAPPDATA%\Zombies Launcher), registers the links of its scheme with Windows, and adds itself to the PC's list
// of installed apps, all for the player's Windows user alone. From then on a link on the room's page starts it, and
// Windows' list of apps uninstalls it:
//
//	zombies-launcher.exe                 install it, so that links can start it
//	zombies-launcher.exe zombies-launcher://<host>[:<port>]/play?key=<key>[&tls=0]
//	                                     what a link runs: play the game of the player's room
//	zombies-launcher.exe uninstall       remove it from the PC
//
// Started by a link, the launcher:
//
//   - asks the player whether this PC plays on the link's server, the first time a link names that server
//     (config.Servers). It does not ask for the first server the PC plays on (trust). The server sends the launcher
//     what the game runs, so it has to be a server of someone the player trusts.
//   - replaces itself with the latest release of the launcher on GitHub, when that is a later build signed by the
//     launcher's maker (update.go). It looks once each time it goes to play, before it calls the server. A server
//     that takes only later versions (launcherapi.MinVersion) so never hears of the older one, and the player's room
//     has nothing to say of it. When the server doesn't take the launcher and there is no later release, the launcher
//     ends, and says where the latest release is.
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
//   - tells the server which of its maps the game folder has (server.setHas). A copy of the game without a map's
//     pack lacks the map, and the room then shows the player as missing it. When the player presses the room's
//     button then, and the server sends game files, the launcher asks whether to download the server's and play
//     from them (files.go: askMissing).
//   - waits for the room's game when there is none yet, and starts Plutonium's bootstrapper to join it, in LAN mode,
//     under the player's name in the game, with the game's password (launch.go).
//   - stays with the player's game for as long as it runs, with no window, as the player's one launcher. For the
//     room's next game, or when the player asks to join again, it restarts the player's game (stay).
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
	// pressed gets a value each time the player asks to join again with the room's button. The button opens a link.
	// That link starts another launcher, which finds this launcher running, signals it, and ends (main).
	pressed <-chan struct{}
	// clicked is set for a run that the player started with the room's button: the launcher's first, and each one
	// after a press. Such a run may ask the player about a copy of the game that lacks the room's map (askMissing). A
	// run that starts by itself, for the room's next game, asks nothing.
	clicked bool
	// hide hides the launcher's window, once the game it started has a window of its own.
	hide func()
	// rerun starts the launcher again with the same link, to replace this run (update).
	rerun func(exe, link string) error
	// register makes Windows start exe for links of the launcher's scheme, and adds exe to the installed apps.
	// unregister undoes both.
	register   func(exe string) error
	unregister func() error
	// later deletes the launcher's program once this run has ended, and then its folder dir if it is empty.
	later func(exe, dir string) error
	// copies returns the folders where the PC may have a copy of Black Ops II that Steam installed.
	copies func() []string
	tick   time.Duration // between two looks at whether the player's game still runs (stay)
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
	// guards the config's file (config.go). state is what they share of the game files, under stateLock (menu.go).
	cfgLock, stateLock sync.Mutex
	state              gameState
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
	// prepared records whether the PC had what the game needs when the launcher last checked.
	prepared bool
	// logged is set once a launcher that stays has started its log. Its next runs append to the log.
	logged bool
	// key is the public half of the key of the launcher's maker, or nil for a launcher built without one. version is
	// the number of this build, its release, or 0 for a dev build. The launcher replaces itself with another build
	// only when that is a later build of the same maker (update). It sends its version with each call to the server,
	// which takes only versions that its code works with (launcherapi.MinVersion).
	key     ed25519.PublicKey
	version int64
	// programs has Black Ops II's own programs, the only programs the launcher accepts from a server. It maps each
	// program's path, in lowercase, to its hash (programs.go).
	programs map[string]string
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
	var pressed <-chan struct{}
	if link {
		// Record that a launcher runs, until the program ends. Uninstalling checks for it (launchers).
		mark()
		if l, err := parseLink(args[0]); err == nil {
			var ok bool
			if turn, ok = single(l.id()); !ok {
				press(l.id()) // the player asks to join again
				return
			}
			pressed = presses(l.id())
		}
	}
	// A launcher that a link started has no window. It writes what it does to its log, reports it to the server for
	// the player's page to show, and asks its questions in Windows' message boxes. A launcher that is started from a
	// terminal prints there.
	boxes := windowless()
	la := &launcher{
		home: filepath.Join(local, appName), plutonium: filepath.Join(local, "Plutonium"),
		manifest: plutonium.Manifest, releases: launcherapi.Releases,
		client: newClient(), in: bufio.NewReader(os.Stdin), out: os.Stdout,
		status: os.Stdout,
		start:  startProgram, rerun: rerun, register: register, unregister: unregister, later: removeLater,
		copies: steamCopies,
		tick:   time.Second, retry: time.Second, away: 5 * time.Minute,
		stays: link, tray: showTray, pick: pickFolder,
	}
	la.notify, la.confirm, la.turn, la.pressed, la.hide = la.tell, la.yes, turn, pressed, func() {}
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
	if url := os.Getenv("ZOMBIES_LAUNCHER_RELEASES"); url != "" { // the launcher's releases, for tests
		la.releases = url
	}
	la.exe, _ = os.Executable()
	la.key, _ = release.ParsePublic(makerKey)
	la.version, _ = strconv.ParseInt(version, 10, 64)
	la.programs = knownPrograms(t6ProgramList)
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
		if la.stays {
			return la.serve(ctx, args[0])
		}
		return la.link(ctx, args[0])
	default:
		la.notify("zombies-launcher: the launcher of a Zombies server. Run it with nothing after its name to install it; "+
			"then a Black Ops II room's game on the server's page starts it. \"zombies-launcher uninstall\" takes it off "+
			"the PC again, as uninstalling it in Windows' Settings does.", true)
		return 2
	}
	if err != nil {
		la.notify(err.Error(), true)
		return 1
	}
	return 0
}

// link runs a link and returns the program's exit code. When the run fails, link prints the error while the run's
// output still goes to its log too (play).
func (la *launcher) link(ctx context.Context, url string) int {
	var srv *server
	if l, err := parseLink(url); err == nil {
		var stop func()
		srv, stop = la.open(ctx, l)
		defer stop()
	}
	if err := la.play(ctx, url, srv, func(err error) {
		la.say("")
		if ctx.Err() != nil { // Ctrl+C
			la.say("Stopped. A download goes on from where it was the next time.")
			return
		}
		la.say("%v", err)
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
// launcher again), or when the server doesn't take this launcher's version. The next link then starts the installed
// launcher, which may be a newer one.
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
	defer stop()
	if la.tray != nil {
		stop := la.tray(l.host, la.menu(l.host, func() { openPage(l.base) }))
		defer stop()
	}
	for {
		err := la.play(ctx, url, srv, func(err error) {
			la.say("%v", err)
			if ctx.Err() == nil {
				srv.fail(ctx, sentence(err))
			}
		})
		switch {
		case ctx.Err() != nil || la.handed:
			return 0
		case ends(err):
			return 1
		}
		if !la.idle(ctx, srv) {
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
		return false, fmt.Errorf("The launcher updated itself, but the new one couldn't be started: %v\nJoin from the room's page again.", err)
	}
	la.handed = true
	return true, nil
}

// idle waits for something to play. It makes no call: it waits for the next answer of the call that is open to the
// server (server.watch), or for the player to press the room's button (pressed). It returns true when the player is
// in a Black Ops II room whose game the player's game isn't in, or when the player presses the button. It does not
// return for the room's game that the player's game just left: the player has to ask for that one. After a try that
// failed, it returns only when the player presses the button, which then says Try again. The launcher does not try
// again by itself. It returns false when the launcher is to end, because the player quit it, or the server no longer
// knows the player or doesn't take this launcher.
//
// When the player has been in no Black Ops II room for away, or the server could not be reached for as long, the
// launcher rests: it closes its call to the server (server.rest) and sends nothing more. The server then takes the
// launcher as stopped, and a room's button says Connect. A press of that button wakes the launcher, like any
// other press.
func (la *launcher) idle(ctx context.Context, srv *server) bool {
	srv.setReady(la.prepared)
	var since time.Time // when the launcher began to have nothing to do, or zero when its player is in a room
	for {
		play, next, err := srv.latest()
		switch {
		case ends(err):
			return false
		case err != nil || play == nil || play.Room == "":
			if since.IsZero() {
				since = time.Now()
			}
		default:
			since = time.Time{}
			if srv.failure() == "" && !play.InGame && (play.Game == nil || la.left == "" || play.Game.ID != la.left) {
				return true
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
			return true
		case <-next:
		case <-rest:
			srv.rest()
		case <-ctx.Done():
			return false
		}
	}
}

// play is what a link runs. When it fails, it calls failed with its error before it returns, while its output still
// goes to its log.
func (la *launcher) play(ctx context.Context, url string, srv *server, failed func(error)) (err error) {
	defer func() {
		if err != nil {
			failed(err)
		}
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
			defer f.Close()
			screen := la.out
			la.out = both{screen, f}
			defer func() { la.out = screen }()
			fmt.Fprintf(f, "%s\n", time.Now().Format("2006-01-02 15:04:05 -0700"))
		}
	}
	la.say(appName)
	la.say("")
	if perr != nil {
		return perr
	}
	la.loadConfig()
	if err := la.trust(l); err != nil {
		return err
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
	// The launcher looks for a later release of itself before it calls the server, and hands the link to that one.
	// A server that takes only later versions than this one's then never hears of this one, and the player's room
	// has nothing to say of it.
	if again, err := la.renew(ctx, url, files, giveUp); again || err != nil {
		return err
	}
	srv.start()
	if la.stays {
		srv.fail(ctx, "") // a new try: the last try's failure is no longer reported
	}
	play, err := srv.current(ctx)
	switch {
	case errors.Is(err, errOld): // the server doesn't take this launcher, and there was no later release to take
		return la.tooOld()
	case err != nil:
		return err
	}
	files()
	la.say("You are %s on %s.", play.Player, l.host)
	// The launcher now prepares the PC, and then reports that the PC has what the game needs (setReady). A room's
	// game waits for a player whose launcher is still preparing, however long a first download of the game files
	// takes. The call that is open to the server shows it that the launcher runs through all of this.
	if err := la.matchPlutonium(ctx, srv, play.Plutonium); err != nil {
		return err
	}
	la.doing(ctx, srv, "Checking files")
	dir, err := la.gameFiles(ctx, srv, l, play)
	if err != nil {
		return err
	}
	done()
	if len(play.Zones) > 0 {
		srv.setHas(zonesIn(dir, play.Zones)) // which maps the game folder has, for the room to say (launcherapi.Play)
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
		case play.Game != nil && la.left != "" && play.Game.ID == la.left:
			return nil // the game the player's game left: the player has to ask for it (idle)
		case play.Game != nil:
			return la.stay(ctx, srv, l, play, dir)
		case play.InGame:
			la.say("Your game is in the room's game already.")
			return nil
		case play.Room == "":
			la.say("")
			la.say("This PC is ready for Black Ops II. You are in no room that plays it now.")
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
// (matchPlutonium).
func (la *launcher) launch(ctx context.Context, srv *server, l link, play *launcherapi.Play, dir string) (game, error) {
	if play.Plutonium != 0 {
		if err := la.matchPlutonium(ctx, srv, play.Plutonium); err != nil {
			return nil, err
		}
	}
	to := play.Game
	dir, err := la.startFrom(dir) // or the folder the player chose in the tray's menu since
	if err != nil {
		return nil, err
	}
	exe := filepath.Join(la.plutonium, filepath.FromSlash(plutonium.Bootstrapper))
	line, err := commandLine(exe, dir, to.Name, to.Password, l.host+":"+strconv.Itoa(to.Port))
	if err != nil {
		return nil, err
	}
	g, err := la.start(exe, line, la.plutonium)
	if err != nil {
		return nil, fmt.Errorf("Plutonium couldn't be started: %v\n\n%s", err, la.plutoniumHint())
	}
	la.started(g)
	la.launched = true
	la.say("")
	la.say("Black Ops II is starting, and joins the room's game as %s.", to.Name)
	return g, nil
}

// game is the player's game that the launcher started. running reports whether it still runs, and stop closes it.
type game interface {
	running() bool
	stop()
}

// stay starts the player's game, which joins the room's game, and stays with it for as long as it runs, so that the
// player has one game, whatever they press. The launcher has no window of its own once the game has one. stay
// returns when the game is closed. Until then this is the player's launcher, and another link of theirs finds it
// running (single). stay reads the server's answers from the call that is open to it (server.watch). It closes the
// player's game and starts it again when the room has another game than the one the player's game was started for.
// That is the room's next game: when the server ends a room's game, the player's game is back at its menu. stay also
// starts the player's game again into the same room's game when the player asks to join again (pressed) while the
// server has the player's game in no room's game.
func (la *launcher) stay(ctx context.Context, srv *server, l link, play *launcherapi.Play, dir string) error {
	to := play.Game
	g, err := la.launch(ctx, srv, l, play, dir)
	if err != nil {
		return err
	}
	la.hide()
	// The player's game runs from here until it is closed. The room's button shows that the game is starting until
	// the server has the player's game in the room's game.
	la.doing(ctx, srv, "Starting the game")
	defer la.doing(context.WithoutCancel(ctx), srv, "")
	select {
	case <-la.pressed: // drop a press that came before the game was started: it was for that start
	default:
	}
	for again := false; g.running() && ctx.Err() == nil; again = false {
		play, next, err := srv.latest()
		if err == nil && play != nil && play.Game != nil && play.Game.ID != to.ID {
			again = true // the room's next game
		} else {
			select {
			case <-la.pressed:
				again = true
			case <-next:
			case <-time.After(la.tick):
			case <-ctx.Done():
			}
			play, _, err = srv.latest()
		}
		if !g.running() {
			break
		}
		if !again || err != nil || play == nil || play.Game == nil {
			continue
		}
		g.stop()
		if g, err = la.launch(ctx, srv, l, play, dir); err != nil {
			if la.stays {
				return err // the player's page shows it
			}
			la.notify(err.Error(), true)
			return nil
		}
		to = play.Game
	}
	la.left = to.ID
	return nil
}

// doing reports to the server what a launcher that stays does now to prepare the PC, for the room's button to show.
// Such a launcher has no window of its own to show it in. A download reports how far it is instead (sync).
func (la *launcher) doing(ctx context.Context, srv *server, what string) {
	if la.stays {
		srv.report(ctx, launcherapi.Report{Doing: what})
	}
}
