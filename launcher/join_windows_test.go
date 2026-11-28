//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A line typed into the console of a program that the launcher started runs there, as a line typed into the
// player's game's console does (process.join). The program here is Windows' Command Prompt, which reads its console
// as the game does. The typing is done by the test program run again, for TestJoinHelper alone, without a console:
// a program can attach to another's console only without one of its own, as a launcher that a link starts is.
//
// The Command Prompt's console is hidden, where the launcher shows the game's (startConsole): a line is typed into a
// console that shows and into one that doesn't in the same way, and no window comes up on the desktop of whoever
// runs the tests.
func TestJoinTypesIntoTheConsole(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	g, err := startConsole(shell, `"`+shell+`" /q /k`, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer g.stop()
	// The program's console may not be there yet as it starts, where the player's game has long had one.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		typer := exec.Command(os.Args[0], "-test.run=^TestJoinHelper$")
		typer.Env = append(os.Environ(), "ZOMBIES_TEST_JOIN_PID="+strconv.Itoa(int(g.(*process).pid)),
			`ZOMBIES_TEST_JOIN_LINE=echo name "Ann";password pw> typed.txt`)
		typer.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
		out, err := typer.CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("typing: %v\n%s", err, out)
		}
	}
	var typed []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if typed, err = os.ReadFile(filepath.Join(dir, "typed.txt")); err == nil && len(typed) > 0 {
			break
		}
	}
	if got := strings.TrimSpace(string(typed)); got != `name "Ann";password pw` {
		t.Errorf("the line typed into the console ran as %q", got)
	}
}

// TestJoinHelper types the line ZOMBIES_TEST_JOIN_LINE into the console of the process ZOMBIES_TEST_JOIN_PID, for
// TestJoinTypesIntoTheConsole, which runs it.
func TestJoinHelper(t *testing.T) {
	pid, err := strconv.Atoi(os.Getenv("ZOMBIES_TEST_JOIN_PID"))
	if err != nil {
		t.Skip("TestJoinTypesIntoTheConsole runs it")
	}
	if err := (&process{pid: uint32(pid)}).join(os.Getenv("ZOMBIES_TEST_JOIN_LINE")); err != nil {
		t.Fatal(err)
	}
}
