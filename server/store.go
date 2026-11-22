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
// stats and records (players.go), and the report of every game that began a round (leaderboards.go). The lobby works
// from its own memory: it reads the database once, as the server starts (load), and writes what changed a moment
// after each change (save).
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

-- A player's best on a map with a party of one size: their highest round (kind 'round'), or their fastest time
-- through the map's easter egg (kind 'egg'). game is the id of the game that set it.
CREATE TABLE records (
    player  TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    kind    TEXT NOT NULL CHECK (kind IN ('round', 'egg')),
    map     TEXT NOT NULL,
    party   INTEGER NOT NULL,
    round   INTEGER NOT NULL,
    seconds INTEGER NOT NULL,
    at      TEXT NOT NULL,
    game    TEXT NOT NULL,
    PRIMARY KEY (player, kind, map, party)
) STRICT, WITHOUT ROWID;

-- A game's report: how it stood when its last round began, or how it ended. party is how many players started it,
-- mods is a JSON array, and seconds counts from round 1.
CREATE TABLE games (
    id      TEXT PRIMARY KEY,
    map     TEXT NOT NULL,
    party   INTEGER NOT NULL,
    mods    TEXT NOT NULL,
    began   TEXT NOT NULL,
    round   INTEGER NOT NULL,
    seconds INTEGER NOT NULL
) STRICT;

-- The lines of a report's scoreboard, in its order. player is the player's id, also when the server has forgotten the
-- player since, and name is the name they had then. doors is NULL when the game reported none.
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
    PRIMARY KEY (game, place)
) STRICT, WITHOUT ROWID;
`

// schemaVersion is the version of the schema, which the database keeps as its user_version.
const schemaVersion = 1

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
	Rounds       []record       `json:"rounds,omitempty"`
	Eggs         []record       `json:"eggs,omitempty"`
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

// migrate makes the tables of a new database. It refuses a database with another version of the schema.
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

// load reads the players and their sessions from the database, and the reports of the games that a record names. The
// players are ordered by id.
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
	err = s.query(`SELECT player, kind, map, party, round, seconds, at, game FROM records
		ORDER BY player, kind, map, party`,
		func(scan func(...any) error) error {
			var player, kind string
			var r record
			err := scan(&player, &kind, &r.Map, &r.Party, &r.Round, &r.Seconds, (*dbTime)(&r.At), &r.Game)
			if p := players[player]; p != nil && kind == "egg" {
				p.Eggs = append(p.Eggs, r)
			} else if p != nil {
				p.Rounds = append(p.Rounds, r)
			}
			return err
		})
	if err != nil {
		return keptPlayers{}, err
	}
	k := keptPlayers{Players: []keptPlayer{}, Sessions: []keptSession{}}
	s.players, s.sessions = map[string]keptPlayer{}, map[string]keptSession{}
	for _, id := range ids {
		k.Players = append(k.Players, *players[id])
		s.players[id] = players[id].clone()
	}
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
	games := map[string]*gameResult{}
	err = s.query(`SELECT id, map, party, mods, began, round, seconds FROM games
		WHERE id IN (SELECT game FROM records) ORDER BY id`,
		func(scan func(...any) error) error {
			g := &gameResult{Players: []playerStats{}}
			var mods string
			if err := scan(&g.ID, &g.Map, &g.party, &mods, (*dbTime)(&g.began), &g.Round, &g.Seconds); err != nil {
				return err
			}
			games[g.ID] = g
			k.Games = append(k.Games, g)
			if mods == "[]" {
				return nil // none, as a report says it
			}
			return json.Unmarshal([]byte(mods), &g.Mods)
		})
	if err != nil {
		return keptPlayers{}, err
	}
	err = s.query(`SELECT game, player, name, score, cash, team, kills, headshots, downs, revives, doors
		FROM game_players WHERE game IN (SELECT game FROM records) ORDER BY game, place`,
		func(scan func(...any) error) error {
			var game string
			var line playerStats
			err := scan(&game, &line.ID, &line.Name, &line.Score, &line.Cash, &line.Team, &line.Kills, &line.Headshots,
				&line.Downs, &line.Revives, &line.Doors)
			if g := games[game]; g != nil {
				g.Players = append(g.Players, line)
			}
			return err
		})
	return k, err
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
// removes the ones that k lacks, each player with everything of theirs but the lines of reports. The store keeps k's
// players, so the caller must not change them afterwards. Either all of it is written or, when save returns an
// error, none of it.
func (s *store) save(k keptPlayers) error {
	players, sessions := map[string]keptPlayer{}, map[string]keptSession{}
	for _, p := range k.Players {
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

// player writes a player, with their Discord account, stats and records, in the place of what the database had of
// them.
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
	w.exec(`DELETE FROM records WHERE player = ?`, p.ID)
	for kind, list := range map[string][]record{"round": p.Rounds, "egg": p.Eggs} {
		for _, r := range list {
			// OR REPLACE: a players file of an earlier server, edited by hand, may have two for a map and party
			w.exec(`INSERT OR REPLACE INTO records (player, kind, map, party, round, seconds, at, game)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				p.ID, kind, r.Map, r.Party, r.Round, r.Seconds, dbTime(r.At), r.Game)
		}
	}
}

// game writes a game's report, in the place of the report that the database had of the game.
func (w *writer) game(g *gameResult) {
	mods, _ := json.Marshal(append([]string{}, g.Mods...))
	w.exec(`INSERT INTO games (id, map, party, mods, began, round, seconds) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET map = excluded.map, party = excluded.party, mods = excluded.mods,
			began = excluded.began, round = excluded.round, seconds = excluded.seconds`,
		g.ID, g.Map, g.party, string(mods), dbTime(g.began), g.Round, g.Seconds)
	w.exec(`DELETE FROM game_players WHERE game = ?`, g.ID)
	for place, line := range g.Players {
		w.exec(`INSERT INTO game_players (game, place, player, name, score, cash, team, kills, headshots, downs,
				revives, doors) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			g.ID, place, line.ID, line.Name, line.Score, line.Cash, line.Team, line.Kills, line.Headshots, line.Downs,
			line.Revives, line.Doors)
	}
}
