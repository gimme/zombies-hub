package main

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openPage has the player's page open the server's event stream, and then close it.
func openPage(t *testing.T, l *lobby, token string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/lobby/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() {
		l.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	waitFor(t, "the page open", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.playerOf(token).streams > 0
	})
	cancel()
	<-done
}

// A player is the same one when the server starts again: the database keeps them, with what the games kept of them,
// and a hash of their token. A script's hello, which opens no page, is kept nowhere; a session unused for sessionIdle
// ends, and a player with none left is forgotten.
func TestPlayersKept(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	l := testLobby(t)
	loadFrom(t, l, file) // none yet
	ann, script := l.hello("", "Zoë"), l.hello("", "Script")
	openPage(t, l, ann.Token)
	l.mu.Lock()
	p := l.playerOf(ann.Token)
	p.stats, p.collectibles = map[string]int{"zombie_kills": 52}, "0011"
	l.save()
	l.mu.Unlock()
	if err := l.writePlayers(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, ann.Token) || !strings.Contains(text, tokenHash(ann.Token)) {
		t.Errorf("the file keeps a token, not its hash")
	}
	if strings.Contains(text, "Script") || strings.Contains(text, tokenHash(script.Token)) {
		t.Errorf("the file keeps a script's hello")
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode: %v", st.Mode())
	}

	// the server starts again
	l = testLobby(t)
	loadFrom(t, l, file)
	again := l.hello(ann.Token, "Zoe")
	if again.Token != ann.Token || again.ID != ann.ID || again.Key != ann.Key || again.Name != "Zoe" {
		t.Errorf("Zoe, once the server started again: %+v, was %+v", again, ann)
	}
	l.mu.Lock()
	p = l.playerOf(ann.Token)
	kept := maps.Equal(p.stats, map[string]int{"zombie_kills": 52}) && p.collectibles == "0011" && l.keys[ann.Key] == p
	l.mu.Unlock()
	if !kept {
		t.Errorf("what the games kept of Zoe, and her key, once the server started again: %v %q", p.stats, p.collectibles)
	}
	if l.hello(script.Token, "Script").Token == script.Token {
		t.Errorf("a script's token, once the server started again")
	}

	// her session, unused for sessionIdle, ends, and so is she forgotten
	l.mu.Lock()
	l.sessions[tokenHash(ann.Token)].used = time.Now().Add(-sessionIdle)
	l.save()
	l.mu.Unlock()
	l.writePlayers()
	l = testLobby(t)
	loadFrom(t, l, file)
	l.mu.Lock()
	gone := l.players[ann.ID] == nil && l.keys[ann.Key] == nil && len(l.sessions) == 0
	l.mu.Unlock()
	if !gone {
		t.Errorf("Zoe, her session unused for %v: still known", sessionIdle)
	}
	// and the database has her no more, once the server has written it
	if err := l.writePlayers(); err != nil {
		t.Fatal(err)
	}
	if k := dbHas(t, file); len(k.Players) != 0 || len(k.Sessions) != 0 {
		t.Errorf("the database, Zoe forgotten: %+v", k)
	}
}
