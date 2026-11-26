package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// testStore opens the database in the file, until the test ends.
func testStore(t *testing.T, file string) *store {
	t.Helper()
	st, err := openStore(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return st
}

// loadFrom has the lobby take its players from the database in the file, as a server does when it starts.
func loadFrom(t *testing.T, l *lobby, file string) {
	t.Helper()
	if err := l.loadPlayers(testStore(t, file)); err != nil {
		t.Fatal(err)
	}
}

// dbHas is what the database in the file has, as a server that starts reads it.
func dbHas(t *testing.T, file string) keptPlayers {
	t.Helper()
	st, err := openStore(file)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	k, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// count is the number a query of one number gives.
func count(t *testing.T, st *store, query string) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// sampleAt is a moment of the samples' day, to the nanosecond.
func sampleAt(second int) time.Time {
	return time.Date(2026, 11, 21, 20, 15, second, 123456789, time.UTC)
}

// The samples' games: a game of two on Moon that both players finished, in which Zoë finished the easter egg, and a
// game of three there that Zoë left in round 9, which Bob played on to round 14.
const sampleGame, sampleLeft = "MBVN-1795138977453", "QJXK-1795139999000"

// keptSample is what the database is given: two players with everything it keeps of a player between them, a session
// each, and the reports of their two games, with every player's line, the games' events, and when some of their
// rounds began.
func keptSample() keptPlayers {
	at := sampleAt
	doors := 3
	zoe := playerStats{ID: "aaaa0001", Name: "Zoë", Score: 1250, Cash: 620, Team: 1, Kills: 14, Headshots: 3,
		Revives: 1, Doors: &doors}
	bob := playerStats{ID: "bbbb0002", Name: "Bob", Score: 900, Cash: 450, Team: 2, Kills: 10, Headshots: 2, Downs: 1}
	zoeLeft := playerStats{ID: "aaaa0001", Name: "Zoë", Score: 400, Cash: 150, Kills: 6}
	bobOn := playerStats{ID: "bbbb0002", Name: "Bob", Score: 2100, Cash: 300, Kills: 40, Headshots: 9}
	return keptPlayers{
		Players: []keptPlayer{
			{ID: "aaaa0001", Key: "key1", Name: "Zoë", Made: at(1), Picture: "revive", Picked: true,
				Discord: &discordUser{ID: "42", Name: "zoe", Access: "access", Refresh: "refresh", Expires: at(2),
					Checked: at(3), Avatar: "https://cdn.discordapp.com/avatars/42/a.png"},
				Stats: map[string]int{"zombie_kills": 52, "ZOMBIE_MOON_SIDEQUEST_TOTAL": 1}, Collectibles: "0011"},
			{ID: "bbbb0002", Key: "key2", Name: "Bob", Made: at(7)},
		},
		Sessions: []keptSession{{Hash: "hash1", Player: "aaaa0001", Used: at(8)},
			{Hash: "hash2", Player: "bbbb0002", Used: at(9)}},
		Games: []*gameResult{
			{ID: sampleGame, Map: "zombie_moon", Mods: []string{"grief", "zombiecap=32"}, Round: 21, Seconds: 2600,
				party: 2, began: at(0), round1: at(30), Players: []playerStats{zoe, bob},
				lines: []gameLine{{playerStats: zoe, round: 21, seconds: 2540},
					{playerStats: bob, round: 21, seconds: 2540}},
				events: []gameEvent{{kind: eventPerk, player: "aaaa0001", what: "jugg"},
					{seconds: 310, round: 4, kind: eventBank, player: "bbbb0002"},
					{seconds: 2500, round: 20, kind: eventEgg, player: "aaaa0001"}},
				rounds: []gameRound{{round: 1}, {round: 2, ms: 38250}, {round: 21, ms: 2540870}}},
			{ID: sampleLeft, Map: "zombie_moon", Round: 14, Seconds: 1500, party: 3, began: at(40), round1: at(50),
				Players: []playerStats{bobOn},
				lines: []gameLine{{playerStats: bobOn, round: 14, seconds: 1400},
					{playerStats: zoeLeft, round: 9, seconds: 700, gone: true}},
				rounds: []gameRound{{round: 9, ms: 700010}, {round: 14, ms: 1400990}}},
		},
	}
}

// keptRead is what a server that starts reads of the database that was given keptSample: the same, and each player's
// records, which it makes from the games. Zoë's round in the game she left is the round she reached in it. It reads
// no game's rounds.
func keptRead() keptPlayers {
	k := keptSample()
	set := func(game int, seconds int64) time.Time {
		return k.Games[game].round1.Add(time.Duration(seconds) * time.Second)
	}
	for _, g := range k.Games {
		g.rounds = nil
	}
	k.Players[0].Rounds = []record{{Map: "zombie_moon", Party: 2, Round: 21, At: set(0, 2540), Game: sampleGame},
		{Map: "zombie_moon", Party: 3, Round: 9, At: set(1, 700), Game: sampleLeft}}
	k.Players[0].Eggs = []record{{Map: "zombie_moon", Party: 2, Seconds: 2500, At: set(0, 2500), Game: sampleGame}}
	k.Players[1].Rounds = []record{{Map: "zombie_moon", Party: 2, Round: 21, At: set(0, 2540), Game: sampleGame},
		{Map: "zombie_moon", Party: 3, Round: 14, At: set(1, 1400), Game: sampleLeft}}
	return k
}

// What the database is given, a server that starts reads from it again, to the nanosecond: each player with their
// Discord account and stats, the sessions, and the reports that a record names. It keeps no records: the server makes
// them from the games, also the round of a player who left a game before its end.
func TestStoreKeeps(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	got, want := dbHas(t, file), keptRead()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the database has\n%+v\nwant\n%+v", got, want)
	}
	// the players' records are no part of what the database is given: the same with them writes nothing
	before := count(t, st, `SELECT total_changes()`)
	with := keptRead()
	with.Games = nil
	if err := st.save(with); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT total_changes()`) - before; n != 0 {
		t.Errorf("rows written for the same players with their records: %d", n)
	}
	// a time of another zone is the same moment
	local := time.Date(2026, 11, 21, 21, 15, 1, 5, time.FixedZone("CET", 3600))
	k := keptSample()
	k.Players[1].Made, k.Games = local, nil
	if err := st.save(k); err != nil {
		t.Fatal(err)
	}
	if made := dbHas(t, file).Players[1].Made; !made.Equal(local) {
		t.Errorf("a time, read again: %v, want %v", made, local)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode: %v", info.Mode())
	}
}

// The database is written only where it differs from what it is given: nothing for the same again, one row for a
// session that was used, and for a player who changed, theirs alone.
func TestStoreWritesChanges(t *testing.T) {
	st := testStore(t, filepath.Join(t.TempDir(), "zombies.db"))
	if _, err := st.load(); err != nil {
		t.Fatal(err)
	}
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	wrote := func(what string, k keptPlayers) int {
		t.Helper()
		before := count(t, st, `SELECT total_changes()`)
		if err := st.save(k); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return count(t, st, `SELECT total_changes()`) - before
	}
	same := keptSample()
	same.Games = nil
	if n := wrote("the same again", same); n != 0 {
		t.Errorf("rows written for the same again: %d", n)
	}
	used := keptSample()
	used.Games = nil
	used.Sessions[1].Used = used.Sessions[1].Used.Add(time.Hour)
	if n := wrote("a session used", used); n != 1 {
		t.Errorf("rows written for a session that was used: %d", n)
	}
	used.Players[1].Stats = map[string]int{"zombie_kills": 1}
	if n := wrote("a player's stat", used); n != 2 { // Bob's row and stat
		t.Errorf("rows written for a player's first stat: %d", n)
	}
	if n := count(t, st, `SELECT count(*) FROM stats`); n != 3 {
		t.Errorf("the stats the database has: %d", n)
	}
}

// A player the database is no longer given is removed from it, with their sessions, Discord account and stats. The
// reports of their games stay, with their lines in them, of which the server makes no records any more.
func TestStoreForgets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	k := keptSample()
	k.Players, k.Sessions, k.Games = k.Players[1:], k.Sessions[1:], nil
	if err := st.save(k); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"players": 1, "discord": 0, "sessions": 1, "stats": 0, "games": 2,
		"game_players": 4, "game_events": 3, "game_rounds": 5} {
		if n := count(t, st, `SELECT count(*) FROM `+table); n != want {
			t.Errorf("rows of %s, a player forgotten: %d, want %d", table, n, want)
		}
	}
	got := dbHas(t, file)
	if len(got.Players) != 1 || got.Players[0].Name != "Bob" || len(got.Players[0].Rounds) != 2 ||
		len(got.Games) != 2 || len(got.Games[0].Players) != 2 || got.Games[0].Players[0].Name != "Zoë" ||
		len(got.Games[1].lines) != 2 || got.Games[1].lines[1].Name != "Zoë" {
		t.Errorf("the database, a player forgotten: %+v", got)
	}
}

// The database keeps the report of every game, each game's last. A server that starts reads only those that a
// record names: not that of a game in which no player did better than in another.
func TestStoreKeepsEveryGame(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	worse := func(round int) *gameResult {
		return &gameResult{ID: "worse", Map: "zombie_moon", Round: round, party: 2, began: sampleAt(20),
			Players: []playerStats{{ID: "bbbb0002", Name: "Bob"}}}
	}
	k := keptSample()
	k.Games = append(k.Games, worse(2))
	if err := st.save(k); err != nil {
		t.Fatal(err)
	}
	later := keptSample()
	later.Games = []*gameResult{worse(3)}
	if err := st.save(later); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM games`); n != 3 {
		t.Errorf("the games the database has: %d", n)
	}
	// its line is that of its one player, who reached its round, and its round 1 began as it did, for all it says
	if round, lines, reached := count(t, st, `SELECT round FROM games WHERE id = 'worse'`),
		count(t, st, `SELECT count(*) FROM game_players WHERE game = 'worse'`),
		count(t, st, `SELECT round FROM game_players WHERE game = 'worse'`); round != 3 || lines != 1 || reached != 3 {
		t.Errorf("the game's later report: round %d, with %d lines, of round %d", round, lines, reached)
	}
	if n := count(t, st, `SELECT count(*) FROM games WHERE id = 'worse' AND round1 = began`); n != 1 {
		t.Errorf("the game's round 1, which its report had no time of, began as the game did: %d", n)
	}
	if got, want := dbHas(t, file), keptRead(); !reflect.DeepEqual(got, want) {
		t.Errorf("a server that starts reads\n%+v\nwant\n%+v", got, want)
	}
}

// The database keeps when each round of a game began, to the millisecond from round 1. A later report of the game
// adds the rounds since. A round's time stays as it was first written, and a report without rounds, as a server
// that starts reads one, takes none away.
func TestStoreKeepsRounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	rounds := func(game string) (got []gameRound) {
		t.Helper()
		err := st.query(`SELECT round, ms FROM game_rounds WHERE game = '`+game+`' ORDER BY round`,
			func(scan func(...any) error) error {
				var gr gameRound
				err := scan(&gr.round, &gr.ms)
				got = append(got, gr)
				return err
			})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, g := range keptSample().Games {
		if got := rounds(g.ID); !slices.Equal(got, g.rounds) {
			t.Errorf("the rounds of %s: %+v, want %+v", g.ID, got, g.rounds)
		}
	}
	later := keptSample()
	later.Games = later.Games[:1]
	later.Games[0].Round = 22
	later.Games[0].rounds = []gameRound{{round: 1, ms: 5}, {round: 2, ms: 38250}, {round: 21, ms: 2540870},
		{round: 22, ms: 2700100}}
	before := count(t, st, `SELECT total_changes()`)
	if err := st.save(later); err != nil {
		t.Fatal(err)
	}
	want := append(keptSample().Games[0].rounds, gameRound{round: 22, ms: 2700100})
	if got := rounds(sampleGame); !slices.Equal(got, want) {
		t.Errorf("the game's rounds, a round later: %+v, want %+v", got, want)
	}
	// the game's row, its two lines and three events, written again, and the one round more
	if n := count(t, st, `SELECT total_changes()`) - before; n != 1+2*2+2*3+1 {
		t.Errorf("rows written for the game's next report: %d", n)
	}
	read := dbHas(t, file)
	if len(read.Games) != 2 || read.Games[0].ID != sampleGame || read.Games[0].rounds != nil {
		t.Fatalf("the games that a server reads: %+v", read.Games)
	}
	read.Games = read.Games[:1]
	if err := st.save(read); err != nil {
		t.Fatal(err)
	}
	if got := rounds(sampleGame); !slices.Equal(got, want) {
		t.Errorf("the game's rounds, its report written as it was read: %+v, want %+v", got, want)
	}
}

// versionOne is a database of version 1, which kept each player's records in a table of their own: Ann and Bob, who
// played Kino to round 12 together, and a game on Five that Ann played to round 7 and Bob left in round 4.
const versionOne = `
CREATE TABLE players (id TEXT PRIMARY KEY, key TEXT NOT NULL UNIQUE, name TEXT NOT NULL, made TEXT NOT NULL,
    picture TEXT NOT NULL, picked INTEGER NOT NULL, collectibles TEXT NOT NULL) STRICT;
CREATE TABLE discord (player TEXT PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE, id TEXT NOT NULL,
    name TEXT NOT NULL, avatar TEXT NOT NULL, access TEXT NOT NULL, refresh TEXT NOT NULL, expires TEXT NOT NULL,
    checked TEXT NOT NULL) STRICT;
CREATE TABLE sessions (hash TEXT PRIMARY KEY, player TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    used TEXT NOT NULL) STRICT;
CREATE TABLE stats (player TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE, name TEXT NOT NULL,
    value INTEGER NOT NULL, PRIMARY KEY (player, name)) STRICT, WITHOUT ROWID;
CREATE TABLE records (player TEXT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('round', 'egg')), map TEXT NOT NULL, party INTEGER NOT NULL,
    round INTEGER NOT NULL, seconds INTEGER NOT NULL, at TEXT NOT NULL, game TEXT NOT NULL,
    PRIMARY KEY (player, kind, map, party)) STRICT, WITHOUT ROWID;
CREATE TABLE games (id TEXT PRIMARY KEY, map TEXT NOT NULL, party INTEGER NOT NULL, mods TEXT NOT NULL,
    began TEXT NOT NULL, round INTEGER NOT NULL, seconds INTEGER NOT NULL) STRICT;
CREATE TABLE game_players (game TEXT NOT NULL REFERENCES games (id) ON DELETE CASCADE, place INTEGER NOT NULL,
    player TEXT NOT NULL, name TEXT NOT NULL, score INTEGER NOT NULL, cash INTEGER NOT NULL, team INTEGER NOT NULL,
    kills INTEGER NOT NULL, headshots INTEGER NOT NULL, downs INTEGER NOT NULL, revives INTEGER NOT NULL,
    doors INTEGER, PRIMARY KEY (game, place)) STRICT, WITHOUT ROWID;
PRAGMA user_version = 1;

INSERT INTO players VALUES ('aaaa0001', 'key1', 'Ann', '2026-11-21T20:15:01.000000000Z', '', 0, ''),
    ('bbbb0002', 'key2', 'Bob', '2026-11-21T20:15:02.000000000Z', '', 0, '');
INSERT INTO sessions VALUES ('hash1', 'aaaa0001', '2026-11-21T20:15:03.000000000Z');
INSERT INTO stats VALUES ('aaaa0001', 'zombie_kills', 52);
INSERT INTO games VALUES ('KINO-1795138977453', 'zombie_theater', 2, '["zombiecap=32"]',
    '2026-11-21T19:00:00.000000000Z', 12, 1500), ('FIVE-1795139999000', 'zombie_pentagon', 2, '[]',
    '2026-11-21T21:00:00.000000000Z', 7, 800);
INSERT INTO game_players VALUES ('KINO-1795138977453', 0, 'aaaa0001', 'Ann', 9000, 400, 0, 120, 30, 2, 1, 5),
    ('KINO-1795138977453', 1, 'bbbb0002', 'Bob', 7000, 300, 0, 90, 20, 3, 2, NULL),
    ('FIVE-1795139999000', 0, 'aaaa0001', 'Ann', 3000, 200, 0, 40, 10, 1, 0, 2);
INSERT INTO records VALUES
    ('aaaa0001', 'round', 'zombie_theater', 2, 12, 0, '2026-11-21T19:20:00.000000000Z', 'KINO-1795138977453'),
    ('bbbb0002', 'round', 'zombie_theater', 2, 12, 0, '2026-11-21T19:20:00.000000000Z', 'KINO-1795138977453'),
    ('aaaa0001', 'round', 'zombie_pentagon', 2, 7, 0, '2026-11-21T21:10:00.000000000Z', 'FIVE-1795139999000'),
    ('bbbb0002', 'round', 'zombie_pentagon', 2, 4, 0, '2026-11-21T21:05:00.000000000Z', 'FIVE-1795139999000');
`

// A database of version 1, which kept the players' records in a table, becomes one of this version as a server
// opens it: its players and games stay, each line of a game's report reached the report's round, and the server makes
// the records from the games from then on. A record that no game's report has is gone: Bob's of the game he had left.
func TestStoreMakesVersion1ThisVersion(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(versionOne); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st := testStore(t, file)
	if version := count(t, st, `PRAGMA user_version`); version != schemaVersion {
		t.Errorf("its version, opened: %d, want %d", version, schemaVersion)
	}
	if n := count(t, st, `SELECT count(*) FROM sqlite_master WHERE name = 'records'`); n != 0 {
		t.Errorf("its table of records is still there")
	}
	if n := count(t, st, `SELECT count(*) FROM game_rounds`); n != 0 {
		t.Errorf("the rounds it has, whose games had no times of them: %d", n)
	}
	k, err := st.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Players) != 2 || k.Players[0].Name != "Ann" || k.Players[0].Stats["zombie_kills"] != 52 ||
		len(k.Sessions) != 1 || len(k.Games) != 2 {
		t.Fatalf("the database, opened: %+v", k)
	}
	began := func(hour int) time.Time { return time.Date(2026, 11, 21, hour, 0, 0, 0, time.UTC) }
	ann := []record{
		{Map: "zombie_pentagon", Party: 2, Round: 7, At: began(21).Add(800 * time.Second), Game: "FIVE-1795139999000"},
		{Map: "zombie_theater", Party: 2, Round: 12, At: began(19).Add(1500 * time.Second), Game: "KINO-1795138977453"}}
	if got := k.Players[0]; !reflect.DeepEqual(got.Rounds, ann) || len(got.Eggs) != 0 {
		t.Errorf("Ann's records, made from the games:\n%+v\nwant\n%+v", got.Rounds, ann)
	}
	if got := k.Players[1].Rounds; !reflect.DeepEqual(got, ann[1:]) {
		t.Errorf("Bob's records, made from the games:\n%+v\nwant\n%+v", got, ann[1:])
	}
	kino := k.Games[1]
	if kino.ID != "KINO-1795138977453" || kino.Round != 12 || !kino.round1.Equal(kino.began) ||
		!slices.Equal(kino.Mods, []string{"zombiecap=32"}) || len(kino.Players) != 2 || kino.Players[0].Score != 9000 ||
		kino.Players[1].Doors != nil || len(kino.lines) != 2 || kino.lines[1].round != 12 ||
		kino.lines[1].seconds != 1500 || kino.lines[1].gone {
		t.Errorf("the report of the game on Kino: %+v", kino)
	}
	// and it is written as any database of this version is: the games of the samples' players, who have the ids of
	// these two, are theirs besides
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	got := dbHas(t, file)
	if n := count(t, st, `SELECT count(*) FROM games`); n != 4 || len(got.Games) != 4 ||
		len(got.Players[0].Rounds) != 4 || len(got.Players[0].Eggs) != 1 || len(got.Players[1].Rounds) != 3 {
		t.Errorf("with two more games written to it, it has %d games, and a server reads %+v", n, got)
	}
}

// A database whose schema has another version is not opened, and is left as it is.
func TestStoreRefusesAnotherVersion(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if other, err := openStore(file); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Errorf("a database of version 99, opened: %v, %v", other, err)
	}
	if n := count(t, st, `SELECT count(*) FROM players`); n != 2 {
		t.Errorf("its players, after that: %d", n)
	}
}

// playersJSON is a players file as a server from before the database wrote it: two players with a record each, their
// sessions, and the report of their game. It also has a player and a session that such a server left out as it read
// the file. USED stands for when the sessions were last used.
const playersJSON = `{
	"players": [
		{
			"id": "8343ee44",
			"key": "39fcd76c8c58cf81",
			"name": "Lea",
			"made": "2026-11-20T01:42:42.957750387Z",
			"discord": {
				"id": "42",
				"name": "lea",
				"access": "access",
				"refresh": "refresh",
				"expires": "2026-11-27T01:42:42.9Z",
				"checked": "2026-11-20T01:42:42.9Z",
				"avatar": "https://cdn.discordapp.com/avatars/42/a.png"
			},
			"picture": "meteorite",
			"picked": true,
			"stats": {
				"ZOMBIE_MOON_SIDEQUEST_TOTAL": 1,
				"zombie_downs": 1
			},
			"collectibles": "0011",
			"rounds": [
				{
					"map": "zombie_moon",
					"party": 3,
					"round": 13,
					"at": "2026-11-20T01:43:28.192084952Z",
					"game": "MBVN-1795138977453"
				}
			],
			"eggs": [
				{
					"map": "zombie_moon",
					"party": 3,
					"seconds": 2500,
					"at": "2026-11-20T02:24:28.1Z",
					"game": "MBVN-1795138977453"
				}
			]
		},
		{
			"id": "e8f0b1f4",
			"key": "925f3962a017684c",
			"name": "Ola",
			"made": "2026-11-20T01:42:48.56583001Z",
			"picture": "englund",
			"rounds": [
				{
					"map": "zombie_moon",
					"party": 3,
					"round": 13,
					"at": "2026-11-20T01:43:28.192115394Z",
					"game": "MBVN-1795138977453"
				},
				{
					"map": "zombie_moon",
					"party": 2,
					"round": 8,
					"at": "2026-11-20T03:10:00Z",
					"game": "DUOX-1795140000000"
				},
				{
					"map": "zombie_moon",
					"party": 4,
					"round": 30,
					"at": "2026-11-19T03:10:00Z",
					"game": "GONE-1795100000000"
				}
			]
		},
		{
			"id": "e8f0b1f4",
			"key": "0000000000000000",
			"name": "Ola again"
		}
	],
	"sessions": [
		{
			"hash": "135d7ad29438492b9f96c1022692c4d4f068d70107c76bf6354868515375e300",
			"player": "e8f0b1f4",
			"used": "USED"
		},
		{
			"hash": "1be2a1cb605a5d0572983d7cb68e0d68b0ae3f33bf5ab6df98aa2f4e2cdd0150",
			"player": "8343ee44",
			"used": "USED"
		},
		{
			"hash": "of no one",
			"player": "00000000",
			"used": "USED"
		}
	],
	"games": [
		{
			"id": "MBVN-1795138977453",
			"map": "zombie_moon",
			"mods": [
				"zombiecap=32"
			],
			"round": 13,
			"seconds": 60,
			"players": [
				{
					"id": "8343ee44",
					"name": "Lea",
					"score": 500,
					"cash": 470,
					"kills": 0,
					"headshots": 0,
					"downs": 1,
					"revives": 0,
					"doors": 0
				},
				{
					"id": "e8f0b1f4",
					"name": "Ola",
					"score": 500,
					"cash": 470,
					"kills": 0,
					"headshots": 0,
					"downs": 1,
					"revives": 0
				}
			]
		},
		{
			"id": "DUOX-1795140000000",
			"map": "zombie_moon",
			"round": 20,
			"seconds": 3000,
			"players": [
				{
					"id": "8343ee44",
					"name": "Lea",
					"score": 9000,
					"cash": 1200,
					"kills": 300,
					"headshots": 90,
					"downs": 4,
					"revives": 0
				}
			]
		}
	]
}
`

// A server's first start with the database takes its players from the players file of the server before it: the
// players as they were, their sessions, and the reports, each with the size of its party and when it began. The file
// is renamed, and the next start imports nothing.
func TestPlayersFileImported(t *testing.T) {
	dir := t.TempDir()
	file, db := filepath.Join(dir, "players.json"), filepath.Join(dir, "zombies.db")
	text := strings.ReplaceAll(playersJSON, "USED", time.Now().UTC().Format(time.RFC3339Nano))
	os.WriteFile(file, []byte(text), 0o600)
	st := testStore(t, db)
	if err := st.importPlayers(file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("the players file is still there")
	}
	if b, _ := os.ReadFile(file + ".imported"); string(b) != text {
		t.Errorf("the players file, renamed: %q", b)
	}
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_moon"} }
	if err := l.loadPlayers(st); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	lea, ola := l.players["8343ee44"], l.players["e8f0b1f4"]
	players, sessions := len(l.players), len(l.sessions)
	report := l.reports["MBVN-1795138977453"]
	l.mu.Unlock()
	if players != 2 || sessions != 2 || lea == nil || ola == nil || ola.asked != "Ola" {
		t.Fatalf("the players, imported: %d with %d sessions, Lea %+v, Ola %+v", players, sessions, lea, ola)
	}
	made, _ := time.Parse(time.RFC3339Nano, "2026-11-20T01:42:42.957750387Z")
	if lea.asked != "Lea" || lea.key != "39fcd76c8c58cf81" || !lea.made.Equal(made) || lea.picture != "meteorite" ||
		!lea.picked || lea.collectibles != "0011" || lea.stats["ZOMBIE_MOON_SIDEQUEST_TOTAL"] != 1 ||
		lea.stats["zombie_downs"] != 1 || len(lea.stats) != 2 {
		t.Errorf("Lea, imported: %+v", lea)
	}
	if d := lea.discord; d == nil || d.ID != "42" || d.Name != "lea" || d.Access != "access" ||
		d.Refresh != "refresh" || d.Expires.Sub(d.Checked) != 7*24*time.Hour ||
		d.Avatar != "https://cdn.discordapp.com/avatars/42/a.png" {
		t.Errorf("Lea's Discord account, imported: %+v", d)
	}
	// The records are made from the games, as the database keeps none. A round is set as its report says: 60 seconds
	// from when the game began. Lea has the round of the game of two as well, which the file had no record of.
	set := time.UnixMilli(1795138977453).Add(60 * time.Second)
	if r := on(lea.rounds, "zombie_moon"); len(r) != 2 || r[1].Party != 3 || r[1].Round != 13 || !r[1].At.Equal(set) ||
		r[1].Game != "MBVN-1795138977453" || r[0].Party != 2 || r[0].Round != 20 || r[0].Game != "DUOX-1795140000000" {
		t.Errorf("Lea's rounds, imported: %+v", lea.rounds)
	}
	// Ola's round of that game, which its report has no line of, is a line of it now, of a player who had left. The
	// record that names a game of which the file has no report is not imported.
	if r := ola.rounds; len(r) != 2 || r[0].Party != 2 || r[0].Round != 8 || r[0].Game != "DUOX-1795140000000" ||
		r[1].Party != 3 || r[1].Round != 13 {
		t.Errorf("Ola's rounds, imported: %+v", r)
	}
	if n := count(t, st, `SELECT count(*) FROM game_players WHERE game = 'DUOX-1795140000000' AND gone = 1 AND
		player = 'e8f0b1f4' AND round = 8`); n != 1 {
		t.Errorf("Ola's line of the game they had left: %d", n)
	}
	// an easter egg of the file is an event of its game now
	if e := lea.eggs; len(e) != 1 || e[0].Seconds != 2500 || e[0].Party != 3 || len(ola.eggs) != 0 {
		t.Errorf("the easter eggs, imported: Lea's %+v, Ola's %+v", e, ola.eggs)
	}
	if report == nil || report.Map != "zombie_moon" || report.Round != 13 || report.Seconds != 60 ||
		!slices.Equal(report.Mods, []string{"zombiecap=32"}) || report.party != 3 ||
		report.began.UnixMilli() != 1795138977453 || len(report.Players) != 2 || report.Players[0].Name != "Lea" ||
		report.Players[0].Doors == nil || report.Players[1].Doors != nil || report.Players[1].Cash != 470 {
		t.Errorf("the report, imported: %+v", report)
	}
	// a session of Lea's is hers still
	l.mu.Lock()
	s := l.sessions["1be2a1cb605a5d0572983d7cb68e0d68b0ae3f33bf5ab6df98aa2f4e2cdd0150"]
	l.mu.Unlock()
	if s == nil || s.player != lea {
		t.Errorf("Lea's session, imported: %+v", s)
	}

	// the next start, with a players file there again: the database's players stay
	os.WriteFile(file, []byte(`{"players": [{"id": "ffffffff", "key": "ffffffffffffffff", "name": "New"}]}`), 0o600)
	again := testStore(t, db)
	if err := again.importPlayers(file); err != nil {
		t.Fatal(err)
	}
	if k, err := again.load(); err != nil || len(k.Players) != 2 {
		t.Errorf("the players, a players file there again: %+v, %v", k.Players, err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the players file that was not imported: %v", err)
	}
}

// A players file the server can't read is moved aside, and the server starts with no one.
func TestPlayersFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	file, db := filepath.Join(dir, "players.json"), filepath.Join(dir, "zombies.db")
	os.WriteFile(file, []byte("{not json"), 0o600)
	st := testStore(t, db)
	if err := st.importPlayers(file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file + ".bad"); err != nil {
		t.Errorf("the file, moved aside: %v", err)
	}
	l := testLobby(t)
	if err := l.loadPlayers(st); err != nil {
		t.Fatal(err)
	}
	if len(l.players) != 0 {
		t.Errorf("players from a file that isn't JSON: %d", len(l.players))
	}
	ann := l.hello("", "Ann")
	openPage(t, l, ann.Token)
	if err := l.writePlayers(); err != nil {
		t.Fatal(err)
	}
	if k := dbHas(t, db); len(k.Players) != 1 || k.Players[0].ID != ann.ID {
		t.Errorf("the database, after that: %+v", k.Players)
	}
}
