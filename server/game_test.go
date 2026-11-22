package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// With FAKE_BO1_GAME set, the test binary is a stand-in for the game (fakeGame), which a test runs as its wine.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_BO1_GAME"); mode != "" {
		fakeGame(mode)
		return
	}
	watchEvery = 50 * time.Millisecond // a second in the server: the tests wait on it
	os.Exit(m.Run())
}

// fakeGame prints its pid and answers each packet on its UDP port (net_port on its command line), as the game does
// while it runs a map, until a console line "freeze" stops its answers; it carries on running, as a hung game does.
// A console line "print <text>" prints the text, as the game says something; it echoes each line, as the game does
// ("]<line>"). In mode "silent" it never answers. It prints who first asked, as "asked by <address>".
func fakeGame(mode string) {
	port := ""
	for i, arg := range os.Args[:len(os.Args)-1] {
		if arg == "net_port" {
			port = os.Args[i+1]
		}
	}
	if slices.Contains(os.Args, "t6zm") {
		fakeT6(port)
		return
	}
	conn, err := net.ListenPacket("udp4", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("pid %d\n", os.Getpid())
	fmt.Printf("args: %s\n", strings.Join(os.Args[1:], " "))
	var frozen atomic.Bool
	frozen.Store(mode == "silent")
	go func() {
		for in := bufio.NewScanner(os.Stdin); in.Scan(); {
			fmt.Printf("]%s\n", in.Text())
			if text, ok := strings.CutPrefix(in.Text(), "print "); ok {
				fmt.Println(text)
			}
			switch in.Text() {
			case "freeze":
				frozen.Store(true)
			case "quit":
				os.Exit(0)
			}
		}
	}()
	buf := make([]byte, 2048)
	for asked := false; ; asked = true {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			os.Exit(1)
		}
		if !asked {
			fmt.Printf("asked by %s\n", from)
		}
		if !frozen.Load() {
			conn.WriteTo(buf[:n], from)
		}
	}
}

// A stand-in for wine: prints its command line, takes one console command, and ends with exit code 3.
const fakeWine = `#!/bin/sh
printf 'args: %s\n' "$*"
read line
printf 'got: %s\n' "$line"
exit 3
`

func newGame(t *testing.T, wine string) *game {
	root := t.TempDir()
	return &game{
		name: "TEST", wine: wine, dir: root, home: filepath.Join(root, "data", "game"),
		engine: &t5Engine{exe: "/bo1/BO1Zombies.exe", args: []string{"+map", "zombie_theater"}},
		logDir: filepath.Join(root, "data", "logs"), port: 0, output: os.Stdout,
	}
}

func (g *game) logs() []string {
	files, _ := filepath.Glob(filepath.Join(g.logDir, "game-*.log"))
	return files
}

func (g *game) running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stdin != nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no %s", what)
		}
	}
}

// freePort is a UDP port no one listens on, for a stand-in game.
func freePort(t *testing.T) int {
	t.Helper()
	free, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer free.Close()
	return free.LocalAddr().(*net.UDPAddr).Port
}

func TestGameRunsAndLogsIt(t *testing.T) {
	wine := filepath.Join(t.TempDir(), "wine")
	writeFile(t, wine, fakeWine)
	os.Chmod(wine, 0o755)
	g := newGame(t, wine)
	var lines []string
	var mu sync.Mutex
	g.lines = func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}
	// what an earlier game on its port saved
	stale := filepath.Join(g.home, "players", "config.cfg")
	writeFile(t, stale, "seta ai_corpseCount \"10\"\n")
	ended := make(chan string)
	go func() {
		state, err := g.runOnce(context.Background())
		ended <- exitReason(state, err)
	}()

	waitFor(t, "run", g.running)
	g.command("status")
	if why := <-ended; why != "exit code 3: fatal error" {
		t.Errorf("ended: %s", why)
	}
	logs := g.logs()
	if len(logs) != 1 || !strings.HasSuffix(logs[0], "-TEST.log") {
		t.Fatalf("log files: %v, want one named after the game", logs)
	}
	text, _ := os.ReadFile(logs[0])
	for _, want := range []string{`+set fs_h Z:` + strings.ReplaceAll(g.home, "/", `\`), "+map zombie_theater", "got: status"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the log lacks %q:\n%s", want, text)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 2 || lines[1] != "got: status" {
		t.Errorf("its lines, as they came: %q", lines)
	}
	if st, err := os.Stat(g.home); err != nil || !st.IsDir() {
		t.Errorf("no game home: %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an earlier game's config is still in the game's home: %v", err)
	}
}

// syncBuffer collects the server's log.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestGameEnvLeavesOutTheServersSettings(t *testing.T) {
	env := gameEnv([]string{"HOME=/home/game", "ZOMBIES_DISCORD_CLIENT_SECRET=secret", "WINEPREFIX=/home/game/.wine",
		"ZOMBIES_MAP=zombie_theater"})
	if want := []string{"HOME=/home/game", "WINEPREFIX=/home/game/.wine"}; !slices.Equal(env, want) {
		t.Errorf("gameEnv = %q, want %q", env, want)
	}
}

func TestGameThatStopsAnsweringIsKilled(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		freeze     bool
	}{
		{name: "after an answer", mode: "answer", freeze: true},
		{name: "before any answer", mode: "silent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logged syncBuffer
			log.SetOutput(&logged)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			t.Setenv("FAKE_BO1_GAME", tc.mode)
			g := newGame(t, os.Args[0])
			g.port = freePort(t)
			g.startTimeout, g.quietTimeout = 12*watchEvery, 8*watchEvery
			if tc.freeze {
				g.startTimeout = time.Minute
			}
			ended := make(chan error, 1)
			go func() {
				_, err := g.runOnce(context.Background())
				ended <- err
			}()

			pid := 0
			waitFor(t, "a run", func() bool {
				logs := g.logs()
				if len(logs) == 0 {
					return false
				}
				text, _ := os.ReadFile(logs[0])
				m := regexp.MustCompile(`pid (\d+)`).FindSubmatch(text)
				if m != nil {
					pid, _ = strconv.Atoi(string(m[1]))
				}
				return m != nil && g.running()
			})
			if tc.freeze {
				time.Sleep(2 * watchEvery) // answers, so it runs on
				select {
				case err := <-ended:
					t.Fatalf("a game that answers ended: %v\n%s", err, logged.String())
				default:
				}
				// from the address the image's firewall lets the game answer (docker/entrypoint.sh)
				text, _ := os.ReadFile(g.logs()[0])
				if want := "asked by " + watchAddr(g.port).String() + "\n"; !strings.Contains(string(text), want) {
					t.Errorf("the game's log lacks %q:\n%s", want, text)
				}
				g.command("freeze")
			}
			var err error
			select {
			case err = <-ended:
			case <-time.After(10 * time.Second):
				t.Fatal("not killed")
			}
			if _, ok := errors.AsType[silence](err); !ok {
				t.Errorf("ended with %v, not for its silence", err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("the game (pid %d) still runs: %v", pid, err)
			}
			if !strings.Contains(logged.String(), "game TEST: no answer for ") {
				t.Errorf("the log does not say why it was killed:\n%s", logged.String())
			}
		})
	}
}
