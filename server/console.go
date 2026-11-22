package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// The server's console, for a shell in the container (Unraid's Console button, docker exec). It is the same console as
// the server's stdin, on a Unix socket. zombies-server console connects to the socket. It prints what the server
// prints (the server's log and the games' output) and sends each line that it reads to the server. The server treats
// the line like a line on its stdin, which goes to the games' consoles.
//
//	zombies-server console               sends the lines typed or piped in, until Ctrl+C or the end of input
//	zombies-server console <command>     sends one command, and prints what the server prints after it
//
// When its input ends, the command keeps printing the server's output. It stops when consoleQuiet passes without
// output after some output has come, or after consoleLimit in total. It waits for the first output because a game that
// is busy loading a map answers late. Leaving the command leaves the server running.

const (
	consoleBuffer = 1000 // lines a client may fall behind by; it misses the lines after that
	consoleQuiet  = time.Second
	consoleLimit  = 5 * time.Second
)

// defaultConsole is the console's socket unless ZOMBIES_CONSOLE names another one, for both the server and the console
// command.
const defaultConsole = "/tmp/zombies-console.sock"

// console sends each line that the server prints to every client connected to its socket.
type console struct {
	mu      sync.Mutex
	clients map[chan []byte]bool
}

// Write sends p, a line that the server prints, to every client. A client that has fallen consoleBuffer lines behind
// misses the line, so that no client holds up the games' output.
func (c *console) Write(p []byte) (int, error) {
	line := slices.Clone(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.clients {
		select {
		case ch <- line:
		default:
		}
	}
	return len(p), nil
}

// serve accepts clients on ln until ln is closed, and passes each line they send to command.
func (c *console) serve(ln net.Listener, command func(string)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go c.client(conn, command)
	}
}

// client serves one client until the client disconnects.
func (c *console) client(conn net.Conn, command func(string)) {
	defer conn.Close()
	ch := make(chan []byte, consoleBuffer)
	c.mu.Lock()
	if c.clients == nil {
		c.clients = map[chan []byte]bool{}
	}
	c.clients[ch] = true
	c.mu.Unlock()
	go func() {
		for line := range ch {
			conn.Write(line)
		}
	}()
	for in := bufio.NewScanner(conn); in.Scan(); {
		if line := strings.TrimSpace(in.Text()); line != "" {
			command(line)
		}
	}
	c.mu.Lock()
	delete(c.clients, ch)
	close(ch)
	c.mu.Unlock()
}

// consoleCommand runs zombies-server console [command].
func consoleCommand(args []string) int {
	conn, err := net.Dial("unix", env("ZOMBIES_CONSOLE", defaultConsole))
	if err != nil {
		fmt.Fprintf(os.Stderr, "console: no server to talk to: %v\n", err)
		return 1
	}
	var in io.Reader = os.Stdin
	if len(args) > 0 {
		in = strings.NewReader(strings.Join(args, " ") + "\n")
	} else if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "console: each line you type runs on the game's console (status, map_restart, ...). "+
			"Ctrl+C leaves; the server keeps running.")
	}
	return runConsole(conn, in, os.Stdout, consoleQuiet, consoleLimit)
}

// runConsole sends in to the server on conn and copies what the server prints to out. After in ends, it carries on
// until quiet passes without output after it has printed something, or until limit has passed in total. It then
// returns 0. It returns 1 when the server closes the connection first.
func runConsole(conn net.Conn, in io.Reader, out io.Writer, quiet, limit time.Duration) int {
	defer conn.Close()
	printed := make(chan []byte, consoleBuffer)
	go func() {
		defer close(printed)
		for r := bufio.NewReader(conn); ; {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				printed <- line
			}
			if err != nil {
				return
			}
		}
	}()
	ended := make(chan struct{})
	go func() {
		io.Copy(conn, in)
		close(ended)
	}()
	var silence, deadline <-chan time.Time // set once in ends
	someOutput := false
	for {
		select {
		case line, ok := <-printed:
			if !ok {
				fmt.Fprintln(os.Stderr, "console: the server has stopped")
				return 1
			}
			out.Write(line)
			someOutput = true
			if deadline != nil {
				silence = time.After(quiet)
			}
		case <-ended:
			ended = nil
			deadline = time.After(limit)
			if someOutput {
				silence = time.After(quiet)
			}
		case <-silence:
			return 0
		case <-deadline:
			return 0
		}
	}
}
