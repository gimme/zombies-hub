package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/plutonium"
)

// The Mac's script is of the version that the launcher needs at least, and gives the launcher its version.
func TestMacScript(t *testing.T) {
	data, err := os.ReadFile("mac.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^mac_version=([0-9]+)$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("mac.sh has no mac_version")
	}
	if v, _ := strconv.Atoi(string(m[1])); v < macNeeds {
		t.Errorf("mac.sh is of version %d, and the launcher needs %d", v, macNeeds)
	}
	if !bytes.Contains(data, []byte("export "+macEnv+"=$mac_version\n")) {
		t.Errorf("mac.sh doesn't give the launcher its version in %s", macEnv)
	}
	for mac, old := range map[string]bool{"": false, strconv.Itoa(macNeeds): false, strconv.Itoa(macNeeds + 1): false,
		strconv.Itoa(macNeeds - 1): true, "two": true} {
		if macOld(mac) != old {
			t.Errorf("a script of version %q is too old: %v", mac, !old)
		}
	}
}

// A launcher that a Mac's script older than it needs started plays nothing. It reports that the player has to install
// it again, which the room shows, and ends, which it tells the server. A launcher that a script of the version it needs
// started plays.
func TestMacScriptTooOld(t *testing.T) {
	l := newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann", Room: "ABCD"}
	tl := newTestLauncher(t, "")
	tl.stays = true
	tl.mac = strconv.Itoa(macNeeds - 1)
	done := make(chan int, 1)
	go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("the run of a launcher whose script is too old: %d\n%s", code, tl.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("a launcher whose script is too old did not end\n%s", tl.out)
	}
	l.mu.Lock()
	reports, asked := slices.Clone(l.reports), slices.Clone(l.asked)
	l.mu.Unlock()
	if len(reports) == 0 || !reports[len(reports)-1].Setup || reports[len(reports)-1].Failed != sentence(errSetup) {
		t.Errorf("what the launcher reported: %+v", reports)
	}
	if !slices.Contains(asked, "POST /launcher/stopped") || slices.Contains(asked, "GET /launcher/t6/files") {
		t.Errorf("what the launcher asked the server: %q", asked)
	}
	if isFile(filepath.Join(tl.plutonium, filepath.FromSlash(plutonium.Bootstrapper))) || len(tl.started) != 0 {
		t.Errorf("the launcher got Plutonium, or started %+v", tl.started)
	}
	// the script of the version it needs, after the player pasted the room's line into Terminal: the run gets
	// Plutonium, and goes on to the game files, which this PC lacks
	tl.again("")
	tl.stays = false
	tl.mac = strconv.Itoa(macNeeds)
	if code := tl.run(context.Background(), []string{l.link()}); code != 1 ||
		!bytes.Contains(tl.out.Bytes(), []byte("Getting Plutonium")) ||
		!bytes.Contains(tl.out.Bytes(), []byte("Couldn't find Black Ops II")) {
		t.Errorf("the run of a launcher whose script is of the version it needs: %d\n%s", code, tl.out)
	}
}
