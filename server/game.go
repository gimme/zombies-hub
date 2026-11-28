package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// game is one run of a title's dedicated server under Wine, for a room (rooms.go). The server starts it, prints its
// output and writes it to a log file, sends it console commands, and reads its output for what it reports about its
// players (lines). A game ends at game over, when the server stops it, or when it fails. A game that stops answering
// is killed (engine.watch). Its engine decides what runs and how the server talks to it: BO1Zombies.exe (t5.go) or
// Plutonium's server (t6.go).
type game struct {
	name string // the room's code, which is in the game's log lines and in its log file's name
	// kind is the purpose of the run, and the start of its log file's name: "" for a room's game ("game"), "files" for
	// a run that lists what a map reads (files.go).
	kind   string
	wine   string       // the wine command
	engine engine       // what it runs
	dir    string       // the game files
	home   string       // the folder it writes in, emptied before each run
	logDir string       // the folder of the log files, one per run
	ip     string       // the address it listens on, or "" for every interface
	port   int          // its UDP port
	output io.Writer    // where its output is printed: the server's stdout and the console's clients
	lines  func(string) // called with each line of its output, or nil
	// uid is the user the game runs as, with the server's group, or 0 for the server's own user (gamesUser). A game
	// that is taken over through its network code then can't read the server's files (its database) or environment.
	uid int

	// The server kills the game when it has not answered for startTimeout after its start, or for quietTimeout after
	// an answer. 0 disables either.
	startTimeout, quietTimeout time.Duration
	// leveled is when the game's level last began to run, as the server's script in a PC game says (pcSaid), in
	// nanoseconds since 1970, or 0 before it has. A game that runs a level answers its console: one that never has
	// by deafTimeout after is stopped then, and not as late as startTimeout (rconWatch).
	leveled atomic.Int64

	mu    sync.Mutex
	stdin *os.File // the running game's console, or nil when it is not running
	// printed takes a line of the running game's output that came another way than its stdout: a T7 game writes its
	// console to a file, which its engine reads (t7.go). It is nil when the game is not running.
	printed func(line string)
}

// engine is what a game runs. There is one for each title.
type engine interface {
	// prepare sets up the game's home. It returns the command for wine to run, the folder to run it in, and clean, a
	// function that rewrites each line of the game's output for the log. clean can be nil. A line that it returns as
	// "" is dropped.
	prepare(ctx context.Context, g *game) (command []string, dir string, clean func(string) string, err error)
	// watch checks that the game still answers. When the game has not answered for too long, watch sends the length
	// of the silence on silent and returns. It also returns when done is closed.
	watch(g *game, done <-chan struct{}, silent chan<- time.Duration)
	// say sends a line to the running game's console. The caller holds g.mu.
	say(g *game, line string)
	// quit asks the running game to quit in a way that tells its players, for a game that would not tell them when it
	// gets the runner's signal. It reports whether it asked.
	quit(g *game) bool
}

const (
	keepLogs  = 20
	stopGrace = 8 * time.Second // docker stop kills after 10
)

// watchEvery is how often the server checks that a game still answers. Tests lower it.
var watchEvery = time.Second

// quitEvery is how often the server asks a game to quit again, for as long as the game still runs (stop).
const quitEvery = 300 * time.Millisecond

// silence is the error of a run that the server killed because the game stopped answering.
type silence struct{ quiet time.Duration }

func (s silence) Error() string { return fmt.Sprintf("no answer for %s", s.quiet.Round(time.Second)) }

// logf logs a message about the game, prefixed with its kind and name.
func (g *game) logf(format string, args ...any) {
	log.Printf(g.logKind()+" %s: "+format, append([]any{g.name}, args...)...)
}

func (g *game) logKind() string {
	if g.kind == "" {
		return "game"
	}
	return g.kind
}

// command sends a line to the running game's console.
func (g *game) command(line string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stdin == nil {
		g.logf("not running; %q wasn't sent", line)
		return
	}
	g.engine.say(g, line)
}

// runOnce runs the game until it ends or ctx is done. When ctx is done, it stops the game, which tells its players.
// It returns how the game ended.
func (g *game) runOnce(ctx context.Context) (*os.ProcessState, error) {
	g.leveled.Store(0)
	// The home folder belongs to the game's port, and an earlier game used it. It is emptied so that nothing the
	// earlier game saved carries over (players/config.cfg has the recommended settings it picked and what was set on
	// its console), and so that the engine's logs in main/ don't grow from game to game. What a game writes stays
	// there until the next game on its port starts.
	if err := os.RemoveAll(g.home); err != nil {
		return nil, err
	}
	if err := g.mkdir(g.home); err != nil {
		return nil, err
	}
	command, dir, clean, err := g.engine.prepare(ctx, g)
	if err != nil {
		return nil, err
	}
	command = append([]string{g.wine}, command...)
	logFile, err := g.openLog()
	if err != nil {
		g.logf("no log file: %v", err)
	}
	defer logFile.Close()

	// The game's stdout and stderr share one pipe, so its report of a fatal error (stderr) stays in order with the
	// lines before it. The server holds the read end itself, not exec, so the last lines are read after the game is
	// gone.
	out, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	in, inW, err := os.Pipe()
	if err != nil {
		out.Close()
		outW.Close()
		return nil, err
	}
	cmd := g.asGame(command...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, outW, outW
	// The game gets its own process group, so that a terminal's Ctrl+C reaches only the server. The server then
	// stops the game with one Ctrl+C of its own: a second one would end the game at once. Pdeathsig kills the game
	// when the server dies, however it dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	err = cmd.Start()
	in.Close()
	outW.Close()
	if err != nil {
		out.Close()
		inW.Close()
		return nil, err
	}
	if logFile != nil {
		g.logf("started (log: %s)", logFile.Name())
	}
	said := outputTo(g.output, logFile, g.lines, clean)
	copied := make(chan struct{})
	go func() {
		copyOutput(out, said)
		close(copied)
	}()
	g.mu.Lock()
	g.stdin, g.printed = inW, func(line string) { said([]byte(line)) }
	g.mu.Unlock()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	silent := make(chan time.Duration, 1)
	watching := make(chan struct{})
	var watch sync.WaitGroup
	watch.Go(func() { g.engine.watch(g, watching, silent) })
	var killed error
	select {
	case err = <-exited:
	case <-ctx.Done():
		err = g.stop(cmd, exited)
	case quiet := <-silent:
		killed = silence{quiet}
		g.logf("%v, so killing it", killed)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		err = <-exited
	}
	close(watching)
	watch.Wait() // the watch's address is free for the next game on this port

	g.mu.Lock()
	g.stdin, g.printed = nil, nil
	g.mu.Unlock()
	inW.Close()
	// Read what is left in the pipe. Wine's own processes (services.exe, ...) can hold it open a few seconds longer.
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		out.Close()
		<-copied
	}
	out.Close()
	if cmd.ProcessState != nil {
		err = killed
	}
	return cmd.ProcessState, err
}

// mkdir makes dir and its parents, for the game to write in. When the game runs as another user (uid), dir is also
// writable by its group.
func (g *game) mkdir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil || g.uid == 0 {
		return err
	}
	return os.Chmod(dir, 0o770)
}

// asGame returns command as the game's user runs it: as the user uid (asUser), or as the server's own when the games
// have no user of their own. It has the server's environment without the server's settings (gameEnv).
func (g *game) asGame(command ...string) *exec.Cmd {
	cmd := exec.Command(command[0], command[1:]...)
	if g.uid != 0 {
		cmd = exec.Command("setpriv", asUser(g.uid, command)...)
	}
	cmd.Env = gameEnv(os.Environ())
	return cmd
}

// asUser returns setpriv's arguments to run command as uid, with the server's group and no other group, no
// capabilities and no way to gain one, and umask 007. The umask lets the server, which is in that group, delete what
// the game wrote in its home when the next game starts. "--pdeathsig keep" keeps the parent-death signal, which a
// change of user would clear, so the game still dies with the server.
func asUser(uid int, command []string) []string {
	return append([]string{"--reuid", strconv.Itoa(uid), "--regid", strconv.Itoa(os.Getgid()), "--clear-groups",
		"--inh-caps=-all", "--ambient-caps=-all", "--no-new-privs", "--pdeathsig", "keep",
		"--", "sh", "-c", `umask 007 && exec "$0" "$@"`}, command...)
}

// gameUser is the uid of the image's user game (docker/Dockerfile, target server), which owns the games' Wine prefix.
const gameUser = 3075

// gamesUser returns the uid the games run as. That is gameUser when the server can switch to it: it then holds
// CAP_SETUID, which the image's entrypoint gives it for this. Otherwise it is 0, the server's own user (docker run
// --user). gamesUser returns an error when the server holds the capability but can't run a command as gameUser.
func gamesUser() (int, error) {
	if os.Getuid() == gameUser || !canSetUID() {
		return 0, nil
	}
	out, err := exec.Command("setpriv", asUser(gameUser, []string{"true"})...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("can't run the games as user %d (%v): %s", gameUser, err, strings.TrimSpace(string(out)))
	}
	return gameUser, nil
}

// canSetUID reports whether the server's effective capabilities include CAP_SETUID (/proc/self/status: CapEff).
func canSetUID() bool {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(status), "\n") {
		if caps, ok := strings.CutPrefix(line, "CapEff:"); ok {
			bits, err := strconv.ParseUint(strings.TrimSpace(caps), 16, 64)
			return err == nil && bits&(1<<7) != 0 // CAP_SETUID
		}
	}
	return false
}

// gameEnv returns the server's environment without its own settings (ZOMBIES_...), which include the Discord client
// secret. A game needs none of them, and the engine's getenv command prints any variable.
func gameEnv(environ []string) []string {
	return slices.DeleteFunc(environ, func(v string) bool { return strings.HasPrefix(v, "ZOMBIES_") })
}

// watchAddr is the local address the server watches the game on the given port from: 127.2.x.y, where x.y is the
// port, at port seatPort, like a seat's address (relay.go: acquire). The image's firewall lets the games send only
// to seat addresses and watch addresses (docker/entrypoint.sh).
func watchAddr(port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(127, 2, byte(port>>8), byte(port)), Port: seatPort}
}

// stop asks the game to quit and waits for it. It kills the game if that takes too long.
//
// A game that is asked on its console (engine.quit) is asked again every quitEvery until it has quit. The console of
// a PC game is a packet away, and a T7 game drops a packet for its console that comes too soon after the last
// (t7.go: watch), so one request can be lost. A game that missed it ran on until the runner's signal, seconds later.
// A PC game whose end has played out has a second and a half until its level exits (pc.go: pcSaid): it then loaded
// its map again, with its players in it, who saw the map's loading movie start before the game was gone.
func (g *game) stop(cmd *exec.Cmd, exited <-chan error) error {
	g.logf("stopping")
	if g.engine.quit(g) {
		again := time.NewTicker(quitEvery)
		defer again.Stop()
		grace := time.After(stopGrace / 2)
	asking:
		for {
			select {
			case err := <-exited:
				return err
			case <-again.C:
				g.engine.quit(g)
			case <-grace:
				break asking
			}
		}
	}
	cmd.Process.Signal(syscall.SIGINT) // Wine turns it into Ctrl+C, which quits the game like its quit command
	select {
	case err := <-exited:
		return err
	case <-time.After(stopGrace):
		g.logf("still running after %s, so killing it", stopGrace)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return <-exited
	}
}

// outputTo returns what takes a line of a game's output. It prints the line to w, writes it to the log file with a
// timestamp, and passes it to lines without its line ending. If clean is set, the line is first replaced by
// clean(line), and a line that becomes "" is dropped. A T6 game's lines lose their colours this way (t6Line), and a
// line with only its prompt is dropped. The lines are taken one at a time, also when they come from two places: the
// game's stdout (copyOutput) and its engine (game.printed).
func outputTo(w io.Writer, logFile *os.File, lines func(string), clean func(string) string) func(line []byte) {
	var mu sync.Mutex
	return func(line []byte) {
		mu.Lock()
		defer mu.Unlock()
		if clean != nil && len(line) > 0 {
			line = []byte(clean(strings.TrimRight(string(line), "\r\n")))
		}
		if len(line) == 0 {
			return
		}
		if line[len(line)-1] != '\n' {
			line = append(line, '\n')
		}
		w.Write(line)
		if logFile != nil {
			logFile.Write(append([]byte(time.Now().Format("2006-01-02 15:04:05.000 ")), line...))
		}
		if lines != nil {
			lines(strings.TrimRight(string(line), "\r\n"))
		}
	}
}

// copyOutput reads the game's output line by line, and passes each line to said (outputTo).
func copyOutput(out *os.File, said func(line []byte)) {
	r := bufio.NewReader(out)
	for {
		line, err := r.ReadBytes('\n')
		said(line)
		if err != nil {
			return
		}
	}
}

// openLog creates this run's log file, logs/<kind>-<date>-<time>-<name>.log, and deletes all but the newest keepLogs
// log files of its kind.
func (g *game) openLog() (*os.File, error) {
	if err := os.MkdirAll(g.logDir, 0o755); err != nil {
		return nil, err
	}
	name := filepath.Join(g.logDir, g.logKind()+"-"+time.Now().Format("20060102-150405"))
	if g.name != "" {
		name += "-" + g.name
	}
	f, err := os.OpenFile(name+".log", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	for n := 2; errors.Is(err, fs.ErrExist); n++ { // two runs in the same second
		f, err = os.OpenFile(fmt.Sprintf("%s-%d.log", name, n), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	}
	if err != nil {
		return nil, err
	}
	logs, _ := filepath.Glob(filepath.Join(g.logDir, g.logKind()+"-*.log"))
	slices.Sort(logs) // by date and time
	for _, old := range logs[:max(0, len(logs)-keepLogs)] {
		os.Remove(old)
	}
	return f, nil
}

// waitForPort waits until the game's UDP port is free. If the port were taken, the engine would listen on the next
// one (NET_OpenIP), where the relay would not find it. waitForPort returns false if ctx is done first.
func (g *game) waitForPort(ctx context.Context) bool {
	addr := &net.UDPAddr{IP: net.ParseIP(g.ip), Port: g.port}
	for waiting := false; ; waiting = true {
		if ctx.Err() != nil {
			return false
		}
		if c, err := net.ListenUDP("udp4", addr); err == nil {
			c.Close()
			return true
		}
		if !waiting {
			g.logf("UDP port %d is taken; waiting for it to be free", g.port)
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// exitReason describes how a run ended. The engine's exit codes are: 0 quit; 2 a fatal error (Sys_Error) or an error
// that unloaded the map; 3 another fatal error (out of memory, no free files, DirectX); 4 hung, with no frame for the
// hang timeout. Any other code is a crash: under Wine it is the low byte of the exception code (5 is an access
// violation).
func exitReason(state *os.ProcessState, err error) string {
	if s, ok := errors.AsType[silence](err); ok {
		return fmt.Sprintf("killed after %v", s)
	}
	if state == nil {
		return fmt.Sprintf("did not start: %v", err)
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return "killed by " + ws.Signal().String()
	}
	switch code := state.ExitCode(); code {
	case 0:
		return "quit"
	case 2:
		return "exit code 2: fatal error (Sys_Error)"
	case 3:
		return "exit code 3: fatal error"
	case 4:
		return "exit code 4: hung"
	case 5:
		return "exit code 5: crashed (access violation)"
	default:
		return fmt.Sprintf("exit code %d", code)
	}
}

// winePath converts a Linux path to the path the game sees under Wine, on drive Z:.
func winePath(p string) string {
	return `Z:` + strings.ReplaceAll(p, "/", `\`)
}
