package main

import (
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

// keptSample is two players with everything the database keeps of a player between them, a session each, and the
// report of a game, which a record of each names.
func keptSample() keptPlayers {
	at := func(second int) time.Time { return time.Date(2026, 11, 21, 20, 15, second, 123456789, time.UTC) }
	doors := 3
	return keptPlayers{
		Players: []keptPlayer{
			{ID: "aaaa0001", Key: "key1", Name: "Zoë", Made: at(1), Picture: "revive", Picked: true,
				Discord: &discordUser{ID: "42", Name: "zoe", Access: "access", Refresh: "refresh", Expires: at(2),
					Checked: at(3), Avatar: "https://cdn.discordapp.com/avatars/42/a.png"},
				Stats: map[string]int{"zombie_kills": 52, "ZOMBIE_MOON_SIDEQUEST_TOTAL": 1}, Collectibles: "0011",
				Rounds: []record{{Map: "zombie_moon", Party: 2, Round: 21, At: at(4), Game: "MBVN-1795138977453"},
					{Map: "zombie_moon", Party: 3, Round: 9, At: at(5), Game: "gone"}},
				Eggs: []record{{Map: "zombie_moon", Party: 2, Seconds: 2500, At: at(6), Game: "MBVN-1795138977453"}}},
			{ID: "bbbb0002", Key: "key2", Name: "Bob", Made: at(7),
				Rounds: []record{{Map: "zombie_moon", Party: 2, Round: 21, At: at(4), Game: "MBVN-1795138977453"}}},
		},
		Sessions: []keptSession{{Hash: "hash1", Player: "aaaa0001", Used: at(8)},
			{Hash: "hash2", Player: "bbbb0002", Used: at(9)}},
		Games: []*gameResult{{ID: "MBVN-1795138977453", Map: "zombie_moon", Mods: []string{"grief", "zombiecap=32"},
			Round: 21, Seconds: 2600, party: 2, began: at(0), Players: []playerStats{
				{ID: "aaaa0001", Name: "Zoë", Score: 1250, Cash: 620, Team: 1, Kills: 14, Headshots: 3, Revives: 1,
					Doors: &doors},
				{ID: "bbbb0002", Name: "Bob", Score: 900, Cash: 450, Team: 2, Kills: 10, Headshots: 2, Downs: 1}}}},
	}
}

// What the database is given, a server that starts reads from it again, to the nanosecond: each player with their
// Discord account, stats and records, the sessions, and the reports that a record names.
func TestStoreKeeps(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	if err := st.save(keptSample()); err != nil {
		t.Fatal(err)
	}
	got, want := dbHas(t, file), keptSample()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the database has\n%+v\nwant\n%+v", got, want)
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
	if n := wrote("a player's stat", used); n != 4 { // Bob's row and stat, and his record deleted and written
		t.Errorf("rows written for a player's first stat: %d", n)
	}
	if n := count(t, st, `SELECT count(*) FROM stats`); n != 3 {
		t.Errorf("the stats the database has: %d", n)
	}
}

// A player the database is no longer given is removed from it, with their sessions, Discord account, stats and
// records. The reports of their games stay, with their lines in them.
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
	for table, want := range map[string]int{"players": 1, "discord": 0, "sessions": 1, "stats": 0, "records": 1,
		"games": 1, "game_players": 2} {
		if n := count(t, st, `SELECT count(*) FROM `+table); n != want {
			t.Errorf("rows of %s, a player forgotten: %d, want %d", table, n, want)
		}
	}
	got := dbHas(t, file)
	if len(got.Players) != 1 || got.Players[0].Name != "Bob" || len(got.Games) != 1 || len(got.Games[0].Players) != 2 ||
		got.Games[0].Players[0].Name != "Zoë" {
		t.Errorf("the database, a player forgotten: %+v", got)
	}
}

// The database keeps the report of every game, each game's last. A server that starts reads only those that a
// record names.
func TestStoreKeepsEveryGame(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	st := testStore(t, file)
	k := keptSample()
	k.Games = append(k.Games, &gameResult{ID: "no record", Map: "zombie_theater", Round: 2,
		Players: []playerStats{{ID: "bbbb0002", Name: "Bob"}}})
	if err := st.save(k); err != nil {
		t.Fatal(err)
	}
	later := keptSample()
	later.Games = []*gameResult{{ID: "no record", Map: "zombie_theater", Round: 3, Players: []playerStats{}}}
	if err := st.save(later); err != nil {
		t.Fatal(err)
	}
	if n := count(t, st, `SELECT count(*) FROM games`); n != 2 {
		t.Errorf("the games the database has: %d", n)
	}
	if round, lines := count(t, st, `SELECT round FROM games WHERE id = 'no record'`),
		count(t, st, `SELECT count(*) FROM game_players WHERE game = 'no record'`); round != 3 || lines != 0 {
		t.Errorf("the game's later report: round %d, with %d lines", round, lines)
	}
	if got := dbHas(t, file).Games; len(got) != 1 || got[0].ID != "MBVN-1795138977453" {
		t.Errorf("the reports a server reads as it starts: %+v", got)
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
	set, _ := time.Parse(time.RFC3339Nano, "2026-11-20T01:43:28.192084952Z")
	if r := lea.rounds; len(r) != 1 || r[0].Map != "zombie_moon" || r[0].Party != 3 || r[0].Round != 13 ||
		!r[0].At.Equal(set) || r[0].Game != "MBVN-1795138977453" {
		t.Errorf("Lea's rounds, imported: %+v", r)
	}
	if e := lea.eggs; len(e) != 1 || e[0].Seconds != 2500 || e[0].Party != 3 || len(ola.eggs) != 0 ||
		len(ola.rounds) != 1 {
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
