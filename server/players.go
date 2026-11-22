package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"maps"
	"slices"
	"time"
)

// The players that the server knows are kept in its database (store.go), so that a player is the same player after
// the server restarts, for as long as they come back. The database has each player's id, name, key for Play.bat and
// picture (pictures.go), what the games keep of them (player.stats), their records (leaderboards.go), and their
// Discord account when they signed in with Discord (discord.go). A page identifies its player with the token of one
// of the player's sessions (rooms.go: bearer). The database has only a hash of each token (tokenHash), so that
// someone who reads it can't act as a player. A player whose page never opened the server's event stream (a hello
// from a script) is not kept, and is forgotten after forgetUnopened. A session that is unused for sessionIdle ends. A
// player with no session left is then forgotten, unless they signed in with Discord: their next sign-in finds the
// same player again.
//
// The lobby has the players in memory. It writes what changed to the database a moment after a change (saveEvery).
const (
	sessionIdle = 180 * 24 * time.Hour
	saveEvery   = 2 * time.Second
	// usedEvery is how far a session's last use must move before it is written again. Otherwise every hello of a
	// page would write to the database.
	usedEvery = time.Hour
)

// session is a player's session: a token that a page of theirs keeps. A player has one session for each browser they
// play in.
type session struct {
	player *player
	used   time.Time // when a page last called hello with it
}

// tokenHash returns what the server stores of a token: its SHA-256, in hex. A token is 128 random bits, so no one can
// guess it from its hash.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// playerOf returns the player whose session the token belongs to, or nil when there is none. On a server where players
// sign in with Discord (discord.go), it returns only a player who did: it returns nil for a player from before the
// server asked for the sign-in. The caller holds l.mu.
func (l *lobby) playerOf(token string) *player {
	s := l.sessions[tokenHash(token)]
	if token == "" || s == nil || l.discord != nil && s.player.discord == nil {
		return nil
	}
	return s.player
}

// newPlayer adds a new player, with an id of their own. The caller holds l.mu.
func (l *lobby) newPlayer() *player {
	p := &player{id: randomHex(4), key: randomHex(8), made: time.Now(), gone: time.Now()}
	for l.players[p.id] != nil {
		p.id = randomHex(4)
	}
	l.players[p.id] = p
	l.keys[p.key] = p
	return p
}

// newSession starts a session for p and returns its token, for a page of theirs. The caller holds l.mu.
func (l *lobby) newSession(p *player) string {
	token := randomHex(16)
	l.sessions[tokenHash(token)] = &session{player: p, used: time.Now()}
	l.save()
	return token
}

// use records that a page called hello with the token. The caller holds l.mu.
func (l *lobby) use(token string) {
	if s := l.sessions[tokenHash(token)]; s != nil {
		if time.Since(s.used) >= usedEvery {
			l.save()
		}
		s.used = time.Now()
	}
}

// endSessions ends p's sessions. The caller holds l.mu.
func (l *lobby) endSessions(p *player) {
	for hash, s := range l.sessions {
		if s.player == p {
			delete(l.sessions, hash)
			l.save()
		}
	}
}

// endSession ends the session of the token, if the session is p's. The caller holds l.mu.
func (l *lobby) endSession(p *player, token string) {
	if s := l.sessions[tokenHash(token)]; token != "" && s != nil && s.player == p {
		delete(l.sessions, tokenHash(token))
		l.save()
	}
}

// hasSession reports whether p has a session. The caller holds l.mu.
func (l *lobby) hasSession(p *player) bool {
	for _, s := range l.sessions {
		if s.player == p {
			return true
		}
	}
	return false
}

// forget removes p from the players that the server knows. p is in no room. The caller holds l.mu.
func (l *lobby) forget(p *player) {
	l.endSessions(p)
	delete(l.players, p.id)
	delete(l.keys, p.key)
	if p.opened {
		l.save()
	}
}

// expire ends the sessions that have been unused for sessionIdle. It then forgets the players in no room who have no
// session left, except those who signed in with Discord. The caller holds l.mu.
func (l *lobby) expire() {
	has := map[*player]bool{}
	for hash, s := range l.sessions {
		if time.Since(s.used) >= sessionIdle {
			delete(l.sessions, hash)
			l.save()
		} else {
			has[s.player] = true
		}
	}
	for _, p := range l.players {
		if !has[p] && p.discord == nil && p.room == nil {
			l.forget(p)
		}
	}
}

// save marks the players as changed, so that keepPlayers writes them a moment from now. The caller holds l.mu.
func (l *lobby) save() { l.unsaved = true }

// kept returns what the database is to have: the players whose page has opened the server's event stream, their
// sessions, and the reports that it lacks (keepGame). It shares nothing with the lobby. The caller holds l.mu.
func (l *lobby) kept() keptPlayers {
	k := keptPlayers{Players: []keptPlayer{}, Sessions: []keptSession{}}
	for _, id := range slices.Sorted(maps.Keys(l.players)) {
		p := l.players[id]
		if !p.opened {
			continue
		}
		kp := keptPlayer{ID: p.id, Key: p.key, Name: p.asked, Made: p.made, Discord: p.discord, Picture: p.picture,
			Picked: p.picked, Stats: p.stats, Collectibles: p.collectibles, Rounds: p.rounds, Eggs: p.eggs}.clone()
		if kp.Discord != nil {
			kp.Discord.checking = false
		}
		k.Players = append(k.Players, kp)
	}
	for hash, s := range l.sessions {
		if s.player.opened {
			k.Sessions = append(k.Sessions, keptSession{Hash: hash, Player: s.player.id, Used: s.used})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(l.unsavedGames)) {
		k.Games = append(k.Games, l.unsavedGames[id])
	}
	return k
}

// loadPlayers reads the players from the database, before the server serves anyone. The lobby writes its changes to
// that database from then on.
func (l *lobby) loadPlayers(st *store) error {
	k, err := st.load()
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = st
	for _, kp := range k.Players {
		p := &player{id: kp.ID, key: kp.Key, asked: kp.Name, name: kp.Name, made: kp.Made, discord: kp.Discord,
			picture: kp.Picture, picked: kp.Picked && kp.Picture != "", stats: kp.Stats, collectibles: kp.Collectibles,
			rounds: kp.Rounds, eggs: kp.Eggs, opened: true}
		if p.name == "" {
			p.name, p.asked = "Player", "Player"
		}
		l.players[p.id], l.keys[p.key] = p, p
	}
	for _, g := range k.Games {
		l.reports[g.ID] = g
	}
	for _, ks := range k.Sessions {
		if p := l.players[ks.Player]; p != nil {
			l.sessions[ks.Hash] = &session{player: p, used: ks.Used}
		}
	}
	l.expire()
	discord := 0
	for _, p := range l.players {
		if p.discord != nil {
			discord++
		}
	}
	log.Printf("server: the database has %d players (%d signed in with Discord), with %d sessions", len(l.players),
		discord, len(l.sessions))
	return nil
}

// keepPlayers writes the players to the database a moment after each change (save), until the server stops. main
// then calls writePlayers once more, after the games have ended.
func (l *lobby) keepPlayers() {
	tick := time.NewTicker(saveEvery)
	defer tick.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-tick.C:
		}
		if err := l.writePlayers(); err != nil {
			log.Printf("server: %v", err)
		}
	}
}

// writePlayers writes the players to the database, if anything changed since the last write. A lobby that has loaded
// none (loadPlayers) has no database, and writes nothing.
func (l *lobby) writePlayers() error {
	l.writing.Lock() // so that the changes are written in the order they were made
	defer l.writing.Unlock()
	l.mu.Lock()
	st := l.store
	write := st != nil && l.unsaved
	var k keptPlayers
	if write {
		k = l.kept()
		l.unsaved, l.unsavedGames = false, map[string]*gameResult{}
	}
	l.mu.Unlock()
	if !write {
		return nil
	}
	if err := st.save(k); err != nil {
		l.mu.Lock()
		l.unsaved = true // the next tick tries again
		for _, g := range k.Games {
			if l.unsavedGames[g.ID] == nil { // unless the game has a later report by now
				l.unsavedGames[g.ID] = g
			}
		}
		l.mu.Unlock()
		return fmt.Errorf("can't keep the players: %v", err)
	}
	return nil
}
