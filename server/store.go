package main

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	_ "modernc.org/sqlite"
)

// The server's database is a SQLite file in the data folder (zombies.db). It has the players with their sessions,
// stats and records (players.go), and the report of every game that began a round (leaderboards.go), with what
// happened in the game besides its rounds (rooms.go: gameEvent) and when each of its rounds began (gameRound). The
// lobby works from its own memory: it reads the database once, as the server starts (load), and writes what changed a
// moment after each change (save).
//
// Only the server opens the database, with one connection. The journal is SQLite's default, a rollback journal, so
// the data folder can be on any file system, and the database is the one file while the server is stopped. Only the
// file's owner can read it (0600), because it has the players' Discord tokens.
//
// A time is text in UTC with nanoseconds, such as "2026-11-21T20:15:04.123456789Z". Text of that form sorts by time.
const schema = `
CREATE TABLE players (
    id           TEXT PRIMARY KEY,
    key          TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    made         TEXT NOT NULL,
    picture      TEXT NOT NULL,
    picked       INTEGER NOT NULL,
    collectibles TEXT NOT NULL
) STRICT;

-- The Discord account of a player who signed in with one.
CREATE TABLE discord (
    player  TEXT PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    id      TEXT NOT NULL,
    name    TEXT NOT NULL,
    avatar  TEXT NOT NULL,
    access  TEXT NOT NULL,
    refresh TEXT NOT NULL,
    expires TEXT NOT NULL,
    checked TEXT NOT NULL
) STRICT;

-- hash is the SHA-256 of the session's token.
CREATE TABLE sessions (
    hash   TEXT PRIMARY KEY,
    player TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    used   TEXT NOT NULL
) STRICT;

-- What the games keep of a player from game to game.
CREATE TABLE stats (
    player TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    name   TEXT NOT NULL,
    value  INTEGER NOT NULL,
    PRIMARY KEY (player, name)
) STRICT, WITHOUT ROWID;

-- A game's report: how it stood when its last round began, or how it ended. party is how many players started it,
-- mods is a JSON array, round1 is when its round 1 began, and seconds counts from then.
CREATE TABLE games (
    id      TEXT PRIMARY KEY,
    map     TEXT NOT NULL,
    party   INTEGER NOT NULL,
    mods    TEXT NOT NULL,
    began   TEXT NOT NULL,
    round   INTEGER NOT NULL,
    seconds INTEGER NOT NULL,
    round1  TEXT NOT NULL
) STRICT;

-- The part of each player who was in a game (rooms.go: gameLine): their line of the scoreboard as it last stood, the
-- round they reached, and when that round began, in seconds from round 1. The lines of the report's scoreboard come
-- first, in its order. gone is 1 on the line of a player who had left by then, which the scoreboard does not have.
-- player is the player's id, also when the server has forgotten the player since, and name is the name they had
-- then. doors is NULL when the game reported none. The players' records are made from these lines: a player's best
-- round on a map with a party of one size is the highest they reached in such a game.
CREATE TABLE game_players (
    game      TEXT NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    place     INTEGER NOT NULL,
    player    TEXT NOT NULL,
    name      TEXT NOT NULL,
    score     INTEGER NOT NULL,
    cash      INTEGER NOT NULL,
    team      INTEGER NOT NULL,
    kills     INTEGER NOT NULL,
    headshots INTEGER NOT NULL,
    downs     INTEGER NOT NULL,
    revives   INTEGER NOT NULL,
    doors     INTEGER,
    round     INTEGER NOT NULL,
    seconds   INTEGER NOT NULL,
    gone      INTEGER NOT NULL,
    PRIMARY KEY (game, place)
) STRICT, WITHOUT ROWID;
` + addedTables

// addedTables are the tables that version 2 of the schema added: the games' events and rounds. A player's fastest
// time through a map's easter egg, with a party of one size, is made from the events of the kind 'egg'.
const addedTables = `
-- What happened in a game besides its rounds, in its order (rooms.go: gameEvent). seconds counts from round 1, and
-- round is the round the game was on. kind is 'egg' (the player finished the map's easter egg: what is the side of its
-- main quest, 'richtofen' or 'maxis', or '' where the quest has none), 'bank' or 'locker' (the player took out of the
-- bank, or of their weapon locker, what they had there from an earlier game), 'perk' (the player came to the game with
-- the persistent upgrade that what names) or 'perk-earned' (they earned it in the game). player is the player's id,
-- as in game_players.
CREATE TABLE game_events (
    game    TEXT NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    place   INTEGER NOT NULL,
    seconds INTEGER NOT NULL,
    round   INTEGER NOT NULL,
    kind    TEXT NOT NULL,
    player  TEXT NOT NULL,
    what    TEXT NOT NULL,
    PRIMARY KEY (game, place)
) STRICT, WITHOUT ROWID;

-- When each round of a game began, as far as the game got (rooms.go: gameRound). ms counts from round 1 too, in
-- milliseconds, where the other tables count whole seconds. A game from before the server noted the rounds has none.
CREATE TABLE game_rounds (
    game  TEXT NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round INTEGER NOT NULL,
    ms    INTEGER NOT NULL,
    PRIMARY KEY (game, round)
) STRICT, WITHOUT ROWID;
`

// schemaVersion is the version of the schema, which the database keeps as its user_version. Version 1 kept each
// player's records in a table of their own. Version 2 makes them from the games: a game has its events, the round
// that each of its players reached, when its round 1 began, and when each round after it did. A database of version 1
// becomes one of version 2 as it is opened (migrate).
const schemaVersion = 2

// fromVersion1 makes a database of version 1 one of version 2. Each line of a game's report reached the report's
// round, and round 1 is taken to have begun as the game did. Its games have no times of their rounds. The table of
// records goes: a record that a game's report does not have, such as one of a player who had left by the report, goes
// with it.
const fromVersion1 = `
ALTER TABLE games ADD COLUMN round1 TEXT NOT NULL DEFAULT '';
UPDATE games SET round1 = began;
ALTER TABLE game_players ADD COLUMN round INTEGER NOT NULL DEFAULT 0;
ALTER TABLE game_players ADD COLUMN seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE game_players ADD COLUMN gone INTEGER NOT NULL DEFAULT 0;
UPDATE game_players SET round = (SELECT round FROM games WHERE id = game),
    seconds = (SELECT seconds FROM games WHERE id = game);
DROP TABLE records;
` + addedTables

type store struct {
	db *sql.DB
	// players and sessions are what the database has of them, by id and by hash, as load read them or save last
	// wrote them. save writes only what differs from them.
	players  map[string]keptPlayer
	sessions map[string]keptSession
}

// keptPlayers is what the server keeps: the players, their sessions, and games' reports.
type keptPlayers struct {
	Players  []keptPlayer  `json:"players"`
	Sessions []keptSession `json:"sessions"`
	Games    []*gameResult `json:"games,omitempty"`
}

type keptPlayer struct {
	ID           string         `json:"id"`
	Key          string         `json:"key"`
	Name         string         `json:"name"` // the name they asked for (player.asked)
	Made         time.Time      `json:"made"`
	Discord      *discordUser   `json:"discord,omitempty"`
	Picture      string         `json:"picture,omitempty"` // the id of their picture in the set (pictures.go)
	Picked       bool           `json:"picked,omitempty"`  // they picked the picture themselves
	Stats        map[string]int `json:"stats,omitempty"`
	Collectibles string         `json:"collectibles,omitempty"`
	// Rounds and Eggs are the player's records, which load makes from the games. The database keeps none, and save
	// writes none. The players file of a server from before the database has them (import.go).
	Rounds []record `json:"rounds,omitempty"`
	Eggs   []record `json:"eggs,omitempty"`
}

type keptSession struct {
	Hash   string    `json:"hash"` // the hash of its token (tokenHash)
	Player string    `json:"player"`
	Used   time.Time `json:"used"`
}

// dbTime is a time as the database has it.
type dbTime time.Time

const timeForm = "2006-01-02T15:04:05.000000000Z"

func (t dbTime) Value() (driver.Value, error) { return time.Time(t).UTC().Format(timeForm), nil }

func (t *dbTime) Scan(value any) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("a time that is %T, not text", value)
	}
	parsed, err := time.ParseInLocation(timeForm, text, time.UTC)
	*t = dbTime(parsed)
	return err
}

// openStore opens the database in the file. It makes the file and its tables when they don't exist.
func openStore(file string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	// SQLite would make the file readable by everyone. It gives its journal the mode of the file.
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err := os.Chmod(file, 0o600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, players: map[string]keptPlayer{}, sessions: map[string]keptSession{}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	return s, nil
}

// migrate makes the tables of a new database, and makes a database of version 1 one of this version (fromVersion1).
// It refuses a database with any other version of the schema.
func (s *store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	switch version {
	case schemaVersion:
		return nil
	case 0:
		return s.write(func(w *writer) {
			w.exec(schema)
			w.exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
		})
	case 1:
		return s.write(func(w *writer) {
			w.exec(fromVersion1)
			w.exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
		})
	}
	return fmt.Errorf("its schema has version %d, and this server reads version %d", version, schemaVersion)
}

func (s *store) close() error { return s.db.Close() }

// writer runs statements in a transaction, until one fails.
type writer struct {
	tx  *sql.Tx
	err error
}

func (w *writer) exec(query string, args ...any) {
	if w.err == nil {
		_, w.err = w.tx.Exec(query, args...)
	}
}

// write runs what the function writes in one transaction: all of it is written, or none.
func (s *store) write(statements func(w *writer)) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	w := &writer{tx: tx}
	statements(w)
	if w.err != nil {
		tx.Rollback()
		return w.err
	}
	return tx.Commit()
}

// query runs a query and calls row with the Scan of each row of the result.
func (s *store) query(query string, row func(scan func(...any) error) error) error {
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

// load reads the players and their sessions from the database, makes each player's records from the games that the
// database has, and reads the reports of the games that a record names. The players are ordered by id. It reads no
// game's rounds (game_rounds), which nothing uses yet: a report that load read has none.
func (s *store) load() (keptPlayers, error) {
	players := map[string]*keptPlayer{}
	var ids []string
	err := s.query(`SELECT id, key, name, made, picture, picked, collectibles FROM players ORDER BY id`,
		func(scan func(...any) error) error {
			p := &keptPlayer{}
			err := scan(&p.ID, &p.Key, &p.Name, (*dbTime)(&p.Made), &p.Picture, &p.Picked, &p.Collectibles)
			players[p.ID], ids = p, append(ids, p.ID)
			return err
		})
	if err != nil {
		return keptPlayers{}, err
	}
	err = s.query(`SELECT player, id, name, avatar, access, refresh, expires, checked FROM discord`,
		func(scan func(...any) error) error {
			var player string
			d := &discordUser{}
			err := scan(&player, &d.ID, &d.Name, &d.Avatar, &d.Access, &d.Refresh, (*dbTime)(&d.Expires),
				(*dbTime)(&d.Checked))
			if p := players[player]; p != nil {
				p.Discord = d
			}
			return err
		})
	if err != nil {
		return keptPlayers{}, err
	}
	err = s.query(`SELECT player, name, value FROM stats`, func(scan func(...any) error) error {
		var player, name string
		var value int
		err := scan(&player, &name, &value)
		if p := players[player]; p != nil {
			if p.Stats == nil {
				p.Stats = map[string]int{}
			}
			p.Stats[name] = value
		}
		return err
	})
	if err != nil {
		return keptPlayers{}, err
	}
	s.players, s.sessions = map[string]keptPlayer{}, map[string]keptSession{}
	for _, id := range ids {
		s.players[id] = players[id].clone()
	}
	k := keptPlayers{Players: []keptPlayer{}, Sessions: []keptSession{}}
	err = s.query(`SELECT hash, player, used FROM sessions ORDER BY hash`, func(scan func(...any) error) error {
		var ks keptSession
		err := scan(&ks.Hash, &ks.Player, (*dbTime)(&ks.Used))
		k.Sessions = append(k.Sessions, ks)
		s.sessions[ks.Hash] = ks
		return err
	})
	if err != nil {
		return keptPlayers{}, err
	}
	// Every game, for the records: each player's best of what the games have of them (keep), as the lobby keeps it
	// while the games run (leaderboards.go).
	games := map[string]*gameResult{}
	var gameIDs []string
	err = s.query(`SELECT id, map, party, mods, began, round1, round, seconds FROM games ORDER BY id`,
		func(scan func(...any) error) error {
			g := &gameResult{Players: []playerStats{}}
			var mods string
			if err := scan(&g.ID, &g.Map, &g.party, &mods, (*dbTime)(&g.began), (*dbTime)(&g.round1), &g.Round,
				&g.Seconds); err != nil {
				return err
			}
			games[g.ID], gameIDs = g, append(gameIDs, g.ID)
			if mods == "[]" {
				return nil // none, as a report says it
			}
			return json.Unmarshal([]byte(mods), &g.Mods)
		})
	if err != nil {
		return keptPlayers{}, err
	}
	at := func(g *gameResult, seconds int64) time.Time {
		return g.round1.Add(time.Duration(seconds) * time.Second)
	}
	err = s.query(`SELECT game, player, name, score, cash, team, kills, headshots, downs, revives, doors, round,
			seconds, gone FROM game_players ORDER BY game, place`,
		func(scan func(...any) error) error {
			var game string
			var line gameLine
			err := scan(&game, &line.ID, &line.Name, &line.Score, &line.Cash, &line.Team, &line.Kills, &line.Headshots,
				&line.Downs, &line.Revives, &line.Doors, &line.round, &line.seconds, &line.gone)
			g := games[game]
			if g == nil {
				return err
			}
			if g.lines = append(g.lines, line); !line.gone {
				g.Players = append(g.Players, line.playerStats)
			}
			if p := players[line.ID]; p != nil && line.round > 0 {
				keep(&p.Rounds, record{Map: g.Map, Party: g.party, Round: line.round, At: at(g, line.seconds),
					Game: g.ID})
			}
			return err
		})
	if err != nil {
		return keptPlayers{}, err
	}
	err = s.query(`SELECT game, seconds, round, kind, player, what FROM game_events ORDER BY game, place`,
		func(scan func(...any) error) error {
			var game string
			var e gameEvent
			err := scan(&game, &e.seconds, &e.round, &e.kind, &e.player, &e.what)
			g := games[game]
			if g == nil {
				return err
			}
			g.events = append(g.events, e)
			if p := players[e.player]; p != nil && e.kind == eventEgg {
				keep(&p.Eggs, record{Map: g.Map, Party: g.party, Seconds: e.seconds, At: at(g, e.seconds), Game: g.ID})
			}
			return err
		})
	if err != nil {
		return keptPlayers{}, err
	}
	// the reports that a record names
	named := map[string]bool{}
	for _, id := range ids {
		k.Players = append(k.Players, *players[id])
		for _, r := range slices.Concat(players[id].Rounds, players[id].Eggs) {
			named[r.Game] = true
		}
	}
	for _, id := range gameIDs {
		if named[id] {
			k.Games = append(k.Games, games[id])
		}
	}
	return k, nil
}

// clone returns a copy of p that shares nothing with it.
func (p keptPlayer) clone() keptPlayer {
	if p.Discord != nil {
		d := *p.Discord
		p.Discord = &d
	}
	p.Stats, p.Rounds, p.Eggs = maps.Clone(p.Stats), slices.Clone(p.Rounds), slices.Clone(p.Eggs)
	return p
}

// save makes the database's players and sessions those of k, and writes the reports of k's games, each in the place
// of the game's earlier report. It writes the players and sessions that differ from what the database has, and
// removes the ones that k lacks, each player with everything of theirs but the lines of reports. It writes no
// records: they are made from the games (load). The store keeps k's players, so the caller must not change them
// afterwards. Either all of it is written or, when save returns an error, none of it.
func (s *store) save(k keptPlayers) error {
	players, sessions := map[string]keptPlayer{}, map[string]keptSession{}
	for _, p := range k.Players {
		p.Rounds, p.Eggs = nil, nil
		players[p.ID] = p
	}
	for _, ks := range k.Sessions {
		sessions[ks.Hash] = ks
	}
	err := s.write(func(w *writer) {
		for id := range s.players {
			if _, ok := players[id]; !ok {
				w.exec(`DELETE FROM players WHERE id = ?`, id)
			}
		}
		for id, p := range players {
			if old, ok := s.players[id]; !ok || !reflect.DeepEqual(old, p) {
				w.player(p)
			}
		}
		for hash := range s.sessions {
			if _, ok := sessions[hash]; !ok {
				w.exec(`DELETE FROM sessions WHERE hash = ?`, hash)
			}
		}
		for hash, ks := range sessions {
			if old, ok := s.sessions[hash]; !ok || old.Player != ks.Player || !old.Used.Equal(ks.Used) {
				w.exec(`INSERT INTO sessions (hash, player, used) VALUES (?, ?, ?)
					ON CONFLICT (hash) DO UPDATE SET player = excluded.player, used = excluded.used`,
					hash, ks.Player, dbTime(ks.Used))
			}
		}
		for _, g := range k.Games {
			w.game(g)
		}
	})
	if err != nil {
		return err
	}
	s.players, s.sessions = players, sessions
	return nil
}

// player writes a player, with their Discord account and stats, in the place of what the database had of them.
func (w *writer) player(p keptPlayer) {
	w.exec(`INSERT INTO players (id, key, name, made, picture, picked, collectibles) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET key = excluded.key, name = excluded.name, made = excluded.made,
			picture = excluded.picture, picked = excluded.picked, collectibles = excluded.collectibles`,
		p.ID, p.Key, p.Name, dbTime(p.Made), p.Picture, p.Picked, p.Collectibles)
	w.exec(`DELETE FROM discord WHERE player = ?`, p.ID)
	if d := p.Discord; d != nil {
		w.exec(`INSERT INTO discord (player, id, name, avatar, access, refresh, expires, checked)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, d.ID, d.Name, d.Avatar, d.Access, d.Refresh, dbTime(d.Expires), dbTime(d.Checked))
	}
	w.exec(`DELETE FROM stats WHERE player = ?`, p.ID)
	for name, value := range p.Stats {
		w.exec(`INSERT INTO stats (player, name, value) VALUES (?, ?, ?)`, p.ID, name, value)
	}
}

// game writes a game's report, with the lines of its players and its events, in the place of the report that the
// database had of the game. A report whose round 1 has no time is taken to have begun it as the game began. The
// report's rounds are added to those that the database has of the game. A round began when it did, whatever a later
// report says, so a report without rounds, as load reads one, takes none away.
func (w *writer) game(g *gameResult) {
	mods, _ := json.Marshal(append([]string{}, g.Mods...))
	round1 := g.round1
	if round1.IsZero() {
		round1 = g.began
	}
	w.exec(`INSERT INTO games (id, map, party, mods, began, round1, round, seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET map = excluded.map, party = excluded.party, mods = excluded.mods,
			began = excluded.began, round1 = excluded.round1, round = excluded.round, seconds = excluded.seconds`,
		g.ID, g.Map, g.party, string(mods), dbTime(g.began), dbTime(round1), g.Round, g.Seconds)
	w.exec(`DELETE FROM game_players WHERE game = ?`, g.ID)
	for place, line := range g.allLines() {
		w.exec(`INSERT INTO game_players (game, place, player, name, score, cash, team, kills, headshots, downs,
				revives, doors, round, seconds, gone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.ID, place, line.ID, line.Name, line.Score, line.Cash, line.Team, line.Kills, line.Headshots, line.Downs,
			line.Revives, line.Doors, line.round, line.seconds, line.gone)
	}
	w.exec(`DELETE FROM game_events WHERE game = ?`, g.ID)
	for place, e := range g.events {
		w.exec(`INSERT INTO game_events (game, place, seconds, round, kind, player, what) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			g.ID, place, e.seconds, e.round, e.kind, e.player, e.what)
	}
	for _, gr := range g.rounds {
		w.exec(`INSERT INTO game_rounds (game, round, ms) VALUES (?, ?, ?) ON CONFLICT (game, round) DO NOTHING`,
			g.ID, gr.round, gr.ms)
	}
}
