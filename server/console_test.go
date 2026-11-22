package main

import (
	"bytes"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startConsole serves a console on a socket in a temporary directory until the test ends. Each command it is handed
// goes to commands, and after delay the console prints "ran <command>", as the game prints what a command does.
func startConsole(t *testing.T, delay time.Duration) (c *console, path string, commands chan string) {
	path = filepath.Join(t.TempDir(), "console.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c = &console{}
	commands = make(chan string, 10)
	go c.serve(ln, func(line string) {
		commands <- line
		time.AfterFunc(delay, func() { fmt.Fprintf(c, "ran %s\n", line) })
	})
	return c, path, commands
}

func dialConsole(t *testing.T, path string) net.Conn {
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestConsoleRunsACommandAndPrintsWhatFollows(t *testing.T) {
	_, path, commands := startConsole(t, 0)
	var out bytes.Buffer
	start := time.Now()
	if code := runConsole(dialConsole(t, path), strings.NewReader("status\n"), &out, 200*time.Millisecond, 5*time.Second); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("it took %s to end after the output went quiet", took)
	}
	if got := <-commands; got != "status" {
		t.Errorf("the command was %q", got)
	}
	if out.String() != "ran status\n" {
		t.Errorf("it printed %q", out.String())
	}
}

func TestConsoleWaitsForAGameThatAnswersLate(t *testing.T) {
	_, path, _ := startConsole(t, 500*time.Millisecond) // later than quiet below
	var out bytes.Buffer
	runConsole(dialConsole(t, path), strings.NewReader("status\n"), &out, 200*time.Millisecond, 5*time.Second)
	if out.String() != "ran status\n" {
		t.Errorf("it printed %q", out.String())
	}
}

func TestConsoleEndsWithinItsLimitWhileTheServerKeepsPrinting(t *testing.T) {
	c, path, _ := startConsole(t, 0)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
				fmt.Fprintln(c, "still loading")
			}
		}
	}()
	var out bytes.Buffer
	start := time.Now()
	runConsole(dialConsole(t, path), strings.NewReader(""), &out, 200*time.Millisecond, time.Second)
	if took := time.Since(start); took < time.Second || took > 3*time.Second {
		t.Errorf("it ended after %s, not its limit of 1s", took)
	}
	if !strings.Contains(out.String(), "still loading\n") {
		t.Errorf("it printed %q", out.String())
	}
}

func TestConsoleClientThatDoesNotReadHoldsNothingUp(t *testing.T) {
	c, path, _ := startConsole(t, 0)
	conn := dialConsole(t, path) // connected, and never read
	defer conn.Close()
	waitFor(t, "client", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.clients) == 1
	})
	line := []byte(strings.Repeat("x", 99) + "\n")
	start := time.Now()
	for range 100_000 { // 10 MB, far more than the socket and the client's buffer hold
		c.Write(line)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("printing took %s", took)
	}
}

func TestConsoleSaysWhenTheServerStops(t *testing.T) {
	server, client := net.Pipe()
	go func() {
		fmt.Fprintln(server, "server: stopped")
		server.Close()
	}()
	var out bytes.Buffer
	if code := runConsole(client, blockingReader{}, &out, time.Second, 5*time.Second); code != 1 {
		t.Errorf("exit code %d", code)
	}
	if out.String() != "server: stopped\n" {
		t.Errorf("it printed %q", out.String())
	}
}

// blockingReader is input that never ends, as a terminal's.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }
