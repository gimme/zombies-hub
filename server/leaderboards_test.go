package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// getBoards is GET /lobby/leaderboards, as the player with that token: the answer's status, and the boards.
func getBoards(t *testing.T, l *lobby, token string) (int, leaderboards) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/lobby/leaderboards", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	var b leaderboards
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
			t.Fatalf("the leaderboards: %v\n%s", err, rec.Body)
		}
	}
	return rec.Code, b
}

// of is the boards of the title with that id, or no boards when the server hosts no such title.
func (b leaderboards) of(id string) titleBoards {
	for _, t := range b.Titles {
		if t.ID == id {
			return t
		}
	}
	return titleBoards{}
}

// playerWith is a player of that name, whom change gives their stats and records; their token.
func playerWith(l *lobby, name string, change func(p *player)) string {
	h := l.hello("", name)
	l.mu.Lock()
	defer l.mu.Unlock()
	change(l.playerOf(h.Token))
	return h.Token
}

// onBoard is who a board lists, in its order.
func onBoard(rows []boardRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// A map's rounds have a board for a duo, one for a squad of three or four and one for a big squad of five up to the
// rooms' size, each with a player's best in that category, ranked by the round, then who got there first: a player's
// best with one size of party doesn't hide their best with another. A party larger than the rooms hold now is on none.
func TestLeaderboardsRankRounds(t *testing.T) {
	l := testLobby(t)
	l.roomSize = 8
	l.maps = func() []string { return []string{"zombie_theater", "zombie_cod5_asylum"} }
	const theater = "zombie_theater"
	viewer := playerWith(l, "Viewer", func(p *player) {})
	playerWith(l, "Both", func(p *player) {
		p.keepRound(theater, 2, 12, "g")
		p.keepRound(theater, 2, 9, "g") // no better
		p.keepRound(theater, 3, 30, "g")
		p.keepRound(theater, 4, 25, "g") // a squad's best is the 30
		p.keepRound(theater, 5, 18, "g")
	})
	playerWith(l, "Duo First", func(p *player) { p.keepRound(theater, 2, 20, "g") })
	playerWith(l, "Duo Later", func(p *player) { p.keepRound(theater, 2, 20, "g") })
	playerWith(l, "Four", func(p *player) { p.keepRound(theater, 4, 31, "g") })
	playerWith(l, "Eight", func(p *player) { p.keepRound(theater, 8, 40, "g") })
	playerWith(l, "Alone", func(p *player) { p.keepRound(theater, 1, 50, "g") }) // in no category
	playerWith(l, "Asylum", func(p *player) { p.keepRound("zombie_cod5_asylum", 3, 7, "g") })

	status, b := getBoards(t, l, viewer)
	if status != http.StatusOK {
		t.Fatalf("the leaderboards answered %d", status)
	}
	if got := len(b.Titles); got != 1 || b.Titles[0].ID != t5 || b.Titles[0].Name != "Black Ops" {
		t.Fatalf("the titles: %+v", b.Titles)
	}
	rounds := b.of(t5).Rounds.Maps
	if got := []string{rounds[0].Map, rounds[1].Map}; !slices.Equal(got, []string{theater, "zombie_cod5_asylum"}) {
		t.Errorf("the maps: %v", got)
	}
	kino, asylum := rounds[0], rounds[1]
	want := []string{"Duo First", "Duo Later", "Both"}
	if got := onBoard(kino.Duo); !slices.Equal(got, want) {
		t.Errorf("Kino's duos: %v, want %v", got, want)
	}
	if r := kino.Duo[2]; r.Round != 12 || r.Party != 2 {
		t.Errorf("Both's duo: %+v", r)
	}
	if got, want := onBoard(kino.Squad), []string{"Four", "Both"}; !slices.Equal(got, want) {
		t.Errorf("Kino's squads: %v, want %v", got, want)
	}
	if r := kino.Squad[0]; r.Round != 31 || r.Party != 4 {
		t.Errorf("Four's: %+v", r)
	}
	if r := kino.Squad[1]; r.Round != 30 || r.Party != 3 {
		t.Errorf("Both's squad, three of them: %+v", r)
	}
	if r := asylum.Squad; len(r) != 1 || r[0].Name != "Asylum" || r[0].Round != 7 || r[0].Party != 3 {
		t.Errorf("Verrückt's squads: %+v", r)
	}
	if got, want := onBoard(kino.BigSquad), []string{"Eight", "Both"}; !slices.Equal(got, want) {
		t.Errorf("Kino's big squads: %v, want %v", got, want)
	}
	if r := kino.BigSquad[1]; r.Round != 18 || r.Party != 5 {
		t.Errorf("Both's big squad, five of them: %+v", r)
	}
	if r := asylum.BigSquad; r == nil || len(r) != 0 {
		t.Errorf("Verrückt's big squads: %#v", r)
	}
	for size, want := range map[int][]string{6: {"Both"}, 4: {}} {
		l.roomSize = size
		if _, b := getBoards(t, l, viewer); !slices.Equal(onBoard(b.of(t5).Rounds.Maps[0].BigSquad), want) {
			t.Errorf("Kino's big squads in rooms of %d: %v, want %v", size, onBoard(b.of(t5).Rounds.Maps[0].BigSquad),
				want)
		}
	}
	l.roomSize = 8
	if eggs := b.of(t5).Eggs; eggs != nil {
		t.Errorf("the easter eggs of maps that have none: %#v", eggs)
	}

	if status, _ := getBoards(t, l, "not a token"); status != http.StatusUnauthorized {
		t.Errorf("the leaderboards, to no one: %d", status)
	}
}

// A map's easter egg ranks the fastest: a player keeps their best time with each size of party, a slower one changing
// nothing, and a squad's is the better of three's and four's.
func TestLeaderboardsRankEggsByTime(t *testing.T) {
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_moon"} }
	const moon = "zombie_moon"
	viewer := playerWith(l, "Slow", func(p *player) { p.keepEgg(moon, 2, 900, "g") })
	playerWith(l, "Fast", func(p *player) {
		p.keepEgg(moon, 2, 300, "g")
		p.keepEgg(moon, 2, 500, "later") // no faster
	})
	playerWith(l, "Improved", func(p *player) {
		p.keepEgg(moon, 2, 800, "g")
		p.keepEgg(moon, 2, 400, "g")
	})
	playerWith(l, "Squad", func(p *player) {
		p.keepEgg(moon, 3, 700, "g")
		p.keepEgg(moon, 4, 600, "g")
	})
	_, b := getBoards(t, l, viewer)
	egg := b.of(t5).Eggs.Maps[0]
	if got, want := onBoard(egg.Duo), []string{"Fast", "Improved", "Slow"}; !slices.Equal(got, want) {
		t.Errorf("the duos: %v, want %v", got, want)
	}
	if r := egg.Duo[0]; r.Seconds != 300 || r.Game != "g" {
		t.Errorf("the fastest duo: %+v", r)
	}
	if r := egg.Squad; len(r) != 1 || r[0].Seconds != 600 || r[0].Party != 4 {
		t.Errorf("the squads: %+v", r)
	}
}

// onAll is who a board of all the maps lists, in its order.
func onAll(rows []allRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// The board of all the maps sums a player's highest round on each, with a party of any size that a map's boards have,
// and ranks the sums, then who had theirs first. A line has what it is the sum of, in the maps' order, and whoever
// asks has their own, with no record too.
func TestLeaderboardsSumRounds(t *testing.T) {
	l := testLobby(t)
	const theater, moon, asylum = "zombie_theater", "zombie_moon", "zombie_cod5_asylum"
	l.maps = func() []string { return []string{theater, moon, asylum} }
	viewer := playerWith(l, "Viewer", func(p *player) {})
	three := playerWith(l, "Three Maps", func(p *player) {
		p.keepRound(asylum, 3, 9, "asylum")
		p.keepRound(theater, 2, 12, "duo")
		p.keepRound(theater, 4, 20, "squad") // Kino's best, of the two
		p.keepRound(theater, 1, 50, "alone") // on no board
		p.keepRound(moon, 8, 30, "big")      // nor is this, in rooms of 4
		p.keepRound(moon, 2, 5, "moon")
	})
	playerWith(l, "One Map", func(p *player) { p.keepRound(moon, 2, 40, "g") })
	playerWith(l, "Tied Later", func(p *player) { p.keepRound(theater, 3, 34, "g") })
	playerWith(l, "Alone", func(p *player) { p.keepRound(theater, 1, 60, "g") }) // not on it

	_, b := getBoards(t, l, viewer)
	all := b.of(t5).Rounds.All
	if got, want := onAll(all.Rows), []string{"One Map", "Three Maps", "Tied Later"}; !slices.Equal(got, want) {
		t.Fatalf("the board of all the maps: %v, want %v", got, want)
	}
	row := all.Rows[1]
	if row.Round != 34 || len(row.Bests) != 3 {
		t.Fatalf("Three Maps's line: %+v", row)
	}
	for i, want := range []record{{Map: theater, Party: 4, Round: 20, Game: "squad"},
		{Map: moon, Party: 2, Round: 5, Game: "moon"}, {Map: asylum, Party: 3, Round: 9, Game: "asylum"}} {
		if r := row.Bests[i]; r.Map != want.Map || r.Party != want.Party || r.Round != want.Round || r.Game != want.Game {
			t.Errorf("Three Maps's best %d: %+v, want %+v", i, r, want)
		}
	}
	if m := all.Mine; m.Name != "Viewer" || m.Round != 0 || m.Bests == nil || len(m.Bests) != 0 {
		t.Errorf("the line of a player with no record: %+v", m)
	}
	if _, b := getBoards(t, l, three); b.of(t5).Rounds.All.Mine.Round != 34 ||
		len(b.of(t5).Rounds.All.Mine.Bests) != 3 {
		t.Errorf("Three Maps's own line: %+v", b.of(t5).Rounds.All.Mine)
	}
	// Moon has an easter egg, which no one has done
	if r := b.of(t5).Eggs.All; r.Rows == nil || len(r.Rows) != 0 || r.Mine.Bests == nil {
		t.Errorf("the board of all the easter eggs, with none done: %#v", r)
	}

	// in rooms of 8, the big squad's round counts
	l.roomSize = 8
	_, b = getBoards(t, l, viewer)
	all = b.of(t5).Rounds.All
	if got, want := onAll(all.Rows), []string{"Three Maps", "One Map", "Tied Later"}; !slices.Equal(got, want) {
		t.Errorf("the board of all the maps, in rooms of 8: %v, want %v", got, want)
	}
	if row := all.Rows[0]; row.Round != 59 || row.Bests[1].Party != 8 {
		t.Errorf("Three Maps's line, in rooms of 8: %+v", row)
	}
}

// The board of all the easter eggs ranks those who have done more of them first, then the lower sum of their fastest
// times, each with a party of any size.
func TestLeaderboardsSumEggs(t *testing.T) {
	l := testLobby(t)
	const cosmodrome, coast, moon = "zombie_cosmodrome", "zombie_coast", "zombie_moon"
	l.maps = func() []string { return []string{"zombie_theater", cosmodrome, coast, moon} }
	viewer := playerWith(l, "Two Slow", func(p *player) {
		p.keepEgg(coast, 2, 3000, "g")
		p.keepEgg(moon, 4, 4000, "g")
	})
	playerWith(l, "One Fast", func(p *player) { p.keepEgg(moon, 2, 600, "g") })
	playerWith(l, "Two Fast", func(p *player) {
		p.keepEgg(moon, 2, 2000, "g")
		p.keepEgg(moon, 3, 1500, "three") // Moon's fastest, of the two
		p.keepEgg(cosmodrome, 4, 1000, "g")
	})
	_, b := getBoards(t, l, viewer)
	all := b.of(t5).Eggs.All
	if got, want := onAll(all.Rows), []string{"Two Fast", "Two Slow", "One Fast"}; !slices.Equal(got, want) {
		t.Fatalf("the board of all the easter eggs: %v, want %v", got, want)
	}
	row := all.Rows[0]
	if row.Seconds != 2500 || len(row.Bests) != 2 || row.Bests[0].Map != cosmodrome || row.Bests[1].Map != moon ||
		row.Bests[1].Party != 3 || row.Bests[1].Game != "three" {
		t.Errorf("Two Fast's line: %+v", row)
	}
	if m := all.Mine; m.Name != "Two Slow" || m.Seconds != 7000 || len(m.Bests) != 2 {
		t.Errorf("Two Slow's own line: %+v", m)
	}
}

// A board of all the maps lists boardRows players at most. A player past them still has their own line, and the
// reports of its games.
func TestLeaderboardsOfAllAreCapped(t *testing.T) {
	l := testLobby(t)
	last := playerWith(l, "Last", func(p *player) { p.keepRound("zombie_theater", 2, 1, "last") })
	for i := range boardRows + 5 {
		playerWith(l, fmt.Sprintf("P%d", i), func(p *player) { p.keepRound("zombie_theater", 2, i+2, "g") })
	}
	l.mu.Lock()
	l.reports["last"] = &gameResult{ID: "last", Map: "zombie_theater", Round: 1, Players: []playerStats{}}
	l.mu.Unlock()
	_, b := getBoards(t, l, last)
	if rows := b.of(t5).Rounds.All.Rows; len(rows) != boardRows || rows[0].Round != boardRows+6 ||
		slices.Contains(onAll(rows), "Last") {
		t.Errorf("the board of all the maps: %d rows, the first %+v", len(rows), rows[0])
	}
	if m := b.of(t5).Rounds.All.Mine; m.Round != 1 || len(m.Bests) != 1 || b.Games[m.Bests[0].Game] == nil {
		t.Errorf("the line of a player past the board's rows: %+v, its games %+v", m, b.Games)
	}
}

// The server keeps a game's report while a record names it.
func TestLeaderboardsKeepGamesOfRecords(t *testing.T) {
	l := testLobby(t)
	token := playerWith(l, "Ann", func(p *player) { p.keepRound("zombie_theater", 2, 5, "first") })
	game := func(id string) *gameResult {
		return &gameResult{ID: id, Map: "zombie_theater", Round: 5, Players: []playerStats{}}
	}
	l.mu.Lock()
	l.keepGame(game("first"))
	l.keepGame(game("no record"))
	kept := slices.Sorted(maps.Keys(l.reports))
	l.mu.Unlock()
	if !slices.Equal(kept, []string{"first"}) {
		t.Errorf("the reports kept: %v", kept)
	}
	_, b := getBoards(t, l, token)
	if b.Games["first"] == nil || b.of(t5).Rounds.Maps[0].Duo[0].Game != "first" {
		t.Errorf("the report of the record: %+v, %+v", b.Games, b.of(t5).Rounds.Maps[0].Duo)
	}

	l.mu.Lock()
	l.playerOf(token).keepRound("zombie_theater", 2, 9, "better")
	l.keepGame(game("better"))
	kept = slices.Sorted(maps.Keys(l.reports))
	l.mu.Unlock()
	if !slices.Equal(kept, []string{"better"}) {
		t.Errorf("the reports kept, once the record was beaten: %v", kept)
	}
}

// A board lists boardRows players at most.
func TestLeaderboardsAreCapped(t *testing.T) {
	l := testLobby(t)
	var token string
	for i := range boardRows + 5 {
		token = playerWith(l, fmt.Sprintf("P%d", i), func(p *player) { p.keepRound("zombie_theater", 2, i+1, "g") })
	}
	_, b := getBoards(t, l, token)
	if rows := b.of(t5).Rounds.Maps[0].Duo; len(rows) != boardRows || rows[0].Round != boardRows+5 {
		t.Errorf("the duos: %d rows, the first %+v", len(rows), rows[0])
	}
}

// Where players sign in with Discord, one of the server's before it asked is no one, and isn't listed.
func TestLeaderboardsListOnlySignedIn(t *testing.T) {
	l := testLobby(t)
	playerWith(l, "Before", func(p *player) { p.keepRound("zombie_theater", 2, 9, "g") })
	signedIn := playerWith(l, "Signed", func(p *player) { p.keepRound("zombie_theater", 2, 3, "g") })
	l.mu.Lock()
	l.discord = &discordAuth{}
	for _, p := range l.players {
		if p.asked == "Signed" {
			p.discord = &discordUser{}
		}
	}
	l.mu.Unlock()
	_, b := getBoards(t, l, signedIn)
	if got := onBoard(b.of(t5).Rounds.Maps[0].Duo); !slices.Equal(got, []string{"Signed"}) {
		t.Errorf("the duos: %v", got)
	}
}

// A room's game over keeps each player whose character was in it a record of the round, with the party the game
// started with; its stat for a map's easter egg, as it changes, one of finishing it, one time for the party. Each
// names the game, whose report the boards have, and which names the mods the game had: a game with mods counts as any
// other. All of it is kept after the server starts again.
func TestRoomsKeepRecords(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_theater", "zombie_moon"} }
	loadFrom(t, l, file)
	ann, bob, cy := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy")
	openPage(t, l, ann.Token)
	openPage(t, l, bob.Token)
	openPage(t, l, cy.Token)
	do(l, ann.Token, "create", "map", "zombie_moon")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	do(l, cy.Token, "join", "room", code)
	mods := []string{"uniquewallweapons", "zombiecap=32"}
	if err := setMods(l, ann.Token, mods...); err != "" {
		t.Fatal(err)
	}
	startGame(t, l, ann.Token, bob.Token, cy.Token)
	waitRunning(t, l, ann.Token)
	annAt, bobAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	l.command(code + " print bo1_lobby: start")
	for _, line := range []string{
		"dstat " + annAt + " ZOMBIE_MOON_SIDEQUEST_TOTAL 1",
		"dstat " + bobAt + " ZOMBIE_TEMPLE_SIDEQUEST 1", // another map's, which isn't this game's
		"dstat " + bobAt + " ZOMBIE_MOON_SIDEQUEST 1",   // a step of it, not the end
		"stats " + annAt + " 1250 620 14 3 0 1",
		"stats " + bobAt + " 900 450 10 2 1 0", // and not Cy's: their character was gone by then
		"over 21",
	} {
		l.command(code + " print bo1_lobby: " + line)
	}
	waitFor(t, "Bob's round", func() bool { rounds, _ := recordsOf(l, bob.Token); return len(rounds) == 1 })
	rounds, got := recordsOf(l, ann.Token)
	if len(rounds) != 1 || rounds[0].Map != "zombie_moon" || rounds[0].Party != 3 || rounds[0].Round != 21 {
		t.Errorf("Ann's rounds: %+v", rounds)
	}
	if len(got) != 1 || got[0].Map != "zombie_moon" || got[0].Party != 3 || got[0].Seconds < 1 ||
		got[0].Game != rounds[0].Game {
		t.Errorf("Ann's easter eggs: %+v", got)
	}
	if _, eggs := recordsOf(l, bob.Token); len(eggs) != 0 {
		t.Errorf("Bob's easter eggs: %+v", eggs)
	}
	if rounds, _ := recordsOf(l, cy.Token); len(rounds) != 0 {
		t.Errorf("Cy's rounds, who wasn't there for the end: %+v", rounds)
	}
	_, b := getBoards(t, l, ann.Token)
	squad := b.of(t5).Eggs.Maps[0].Squad
	if b.of(t5).Eggs.Maps[0].Map != "zombie_moon" || len(squad) != 1 || squad[0].Name != "Ann" || squad[0].Seconds < 1 {
		t.Errorf("Moon's easter egg: %+v", b.of(t5).Eggs.Maps[0])
	}
	report := b.Games[squad[0].Game]
	if report == nil || report.Round != 21 || report.Map != "zombie_moon" || len(report.Players) != 2 ||
		!slices.Equal(report.Mods, mods) {
		t.Fatalf("the report of Ann's game: %+v", report)
	}
	// its game said no doors with its players' lines, as one built before it counted them: the report has none
	if said, _ := json.Marshal(report); strings.Contains(string(said), "doors") {
		t.Errorf("the report of a game that said no doors: %s", said)
	}

	l.mu.Lock()
	l.save()
	l.mu.Unlock()
	if err := l.writePlayers(); err != nil {
		t.Fatal(err)
	}
	l = testLobby(t)
	loadFrom(t, l, file)
	again := l.hello(ann.Token, "Ann")
	if rounds, eggs := recordsOf(l, again.Token); len(rounds) != 1 || len(eggs) != 1 {
		t.Errorf("Ann's records, once the server started again: %+v %+v", rounds, eggs)
	}
	l.mu.Lock()
	kept := l.reports[report.ID]
	l.mu.Unlock()
	if kept == nil || kept.Round != 21 || !slices.Equal(kept.Mods, mods) || kept.party != 3 || kept.began.IsZero() ||
		len(kept.Players) != 2 || kept.Players[0].Name != "Ann" || kept.Players[0].Score != 1250 {
		t.Errorf("the report, once the server started again: %+v", kept)
	}
}

// A round counts as it begins, for each player whose character the game then says is in it: a game that never reaches
// game over keeps its rounds, and its report. Game over's report takes the place of the last round's.
func TestRoomsKeepRoundsAsTheyBegin(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zombies.db")
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_theater"} }
	loadFrom(t, l, file)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	openPage(t, l, ann.Token)
	openPage(t, l, bob.Token)
	start := func() (string, string, string) {
		do(l, ann.Token, "create", "map", "zombie_theater")
		code := l.viewOf(ann.Token).Room.Code
		do(l, bob.Token, "join", "room", code)
		startGame(t, l, ann.Token, bob.Token)
		waitRunning(t, l, ann.Token)
		l.command(code + " print bo1_lobby: start")
		return code, l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	}
	code, annAt, bobAt := start()
	for _, line := range []string{
		"stats " + annAt + " 500 250 0 0 0 0 0",
		"stats " + bobAt + " 500 250 0 0 0 0 0",
		"round 1",
		"stats " + annAt + " 900 450 12 3 0 0 1",
		"stats " + bobAt + " 800 400 10 1 1 0 0",
		"round 2",
		"stats " + annAt + " 1500 750 30 9 0 2 2", // and not Bob's: his character is gone
		"round 3",
	} {
		l.command(code + " print bo1_lobby: " + line)
	}
	waitFor(t, "Ann's round 3", func() bool {
		rounds, _ := recordsOf(l, ann.Token)
		return len(rounds) == 1 && rounds[0].Round == 3
	})
	if rounds, _ := recordsOf(l, bob.Token); len(rounds) != 1 || rounds[0].Round != 2 || rounds[0].Party != 2 {
		t.Errorf("Bob's rounds, up to when his character went: %+v", rounds)
	}
	if err := do(l, ann.Token, "leave"); err != "" {
		t.Errorf("Ann leaves: %q", err)
	}
	if err := do(l, bob.Token, "leave"); err != "" {
		t.Errorf("Bob leaves: %q", err)
	}
	waitFor(t, "the game to end", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.busy) == 0 // and the server has room for the next
	})
	_, b := getBoards(t, l, ann.Token)
	row := b.of(t5).Rounds.Maps[0].Duo[0]
	report := b.Games[row.Game]
	if row.Name != "Ann" || row.Round != 3 || report == nil {
		t.Fatalf("the game no one finished: %+v, %+v", row, report)
	}
	if report.Round != 3 || len(report.Players) != 1 || report.Players[0].Score != 1500 {
		t.Errorf("its report: %+v", report)
	}
	// The database keeps no records. A server that starts makes them from the game, whose report has the round each
	// of its players reached: Bob's too, whose character was gone by its end, with his line as it last stood.
	l.mu.Lock()
	l.save()
	l.mu.Unlock()
	if err := l.writePlayers(); err != nil {
		t.Fatal(err)
	}
	started := testLobby(t)
	started.maps = l.maps
	loadFrom(t, started, file)
	for name, want := range map[string]int{ann.Token: 3, bob.Token: 2} {
		token := started.hello(name, "").Token
		if rounds, eggs := recordsOf(started, token); len(rounds) != 1 || rounds[0].Round != want ||
			rounds[0].Party != 2 || rounds[0].Game != row.Game || len(eggs) != 0 {
			t.Errorf("a record of round %d, once the server started again: %+v", want, rounds)
		}
	}
	started.mu.Lock()
	kept := started.reports[row.Game]
	started.mu.Unlock()
	if kept == nil || len(kept.Players) != 1 || kept.Players[0].Score != 1500 || len(kept.lines) != 2 ||
		!kept.lines[1].gone || kept.lines[1].round != 2 || kept.lines[1].Score != 800 || kept.lines[0].round != 3 {
		t.Errorf("the report, once the server started again: %+v", kept)
	}
	// and the database has when each of the game's three rounds began, from round 1, with which the game's clock began
	st := testStore(t, file)
	of := ` FROM game_rounds WHERE game = '` + row.Game + `'`
	if n, first, last := count(t, st, `SELECT count(*)`+of), count(t, st, `SELECT ms`+of+` AND round = 1`),
		count(t, st, `SELECT ms`+of+` AND round = 3`); n != 3 || first != 0 || last < 0 || last > 10000 {
		t.Errorf("the game's rounds in the database: %d, round 1 at %d ms and round 3 at %d ms", n, first, last)
	}

	code, annAt, bobAt = start()
	for _, line := range []string{
		"stats " + annAt + " 1 0 0 0 0 0 0",
		"stats " + bobAt + " 1 0 0 0 0 0 0",
		"round 4",
		"stats " + annAt + " 2000 1000 40 9 0 2 3",
		"stats " + bobAt + " 1800 900 30 1 1 0 1",
		"over 4",
	} {
		l.command(code + " print bo1_lobby: " + line)
	}
	waitFor(t, "the report of game over", func() bool {
		_, b := getBoards(t, l, ann.Token)
		res := b.Games[b.of(t5).Rounds.Maps[0].Duo[0].Game]
		return res != nil && len(res.Players) > 0 && res.Players[0].Score == 2000
	})
	_, b = getBoards(t, l, ann.Token)
	res := b.Games[b.of(t5).Rounds.Maps[0].Duo[0].Game]
	if res.Round != 4 || len(res.Players) != 2 || res.Players[0].Score != 2000 {
		t.Errorf("game over's report, in place of the last round's: %+v", res)
	}
}

// An easter egg done in a game that has set no record of a round still has that game's report, as its last round began.
func TestRoomsEggKeepsItsGamesReport(t *testing.T) {
	l := testLobby(t)
	l.maps = func() []string { return []string{"zombie_moon"} }
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	l.mu.Lock()
	l.playerOf(ann.Token).keepRound("zombie_moon", 2, 40, "an older game")
	l.playerOf(bob.Token).keepRound("zombie_moon", 2, 40, "an older game")
	l.mu.Unlock()
	do(l, ann.Token, "create", "map", "zombie_moon")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	annAt, bobAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	for _, line := range []string{
		"start",
		"stats " + annAt + " 900 450 12 3 0 0 1",
		"stats " + bobAt + " 800 400 10 1 1 0 0",
		"round 9",
		"dstat " + annAt + " ZOMBIE_MOON_SIDEQUEST_TOTAL 1",
	} {
		l.command(code + " print bo1_lobby: " + line)
	}
	waitFor(t, "Ann's easter egg", func() bool { _, eggs := recordsOf(l, ann.Token); return len(eggs) == 1 })
	_, eggs := recordsOf(l, ann.Token)
	_, b := getBoards(t, l, ann.Token)
	if res := b.Games[eggs[0].Game]; res == nil || res.Round != 9 || len(res.Players) != 2 {
		t.Errorf("the easter egg's report: %+v", res)
	}
}

// recordsOf is what the server keeps of the player for the leaderboards.
func recordsOf(l *lobby, token string) (rounds, eggs []record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.playerOf(token)
	return slices.Clone(p.rounds), slices.Clone(p.eggs)
}

// A room's game needs two players: one alone can't say they are ready. It starts once another has come and both have.
func TestRoomsNeedTwoPlayers(t *testing.T) {
	l := testLobby(t)
	l.minPlayers = 2
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	pagesOpen(l, ann.Token)
	haveMap(l, ann.Token)
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "A game needs 2 players." {
		t.Errorf("Ann says they are ready, alone: %q", err)
	}
	time.Sleep(4 * l.countdown)
	if v := l.viewOf(ann.Token); v.Room.State != "open" || v.Room.StartsIn != 0 || v.You.Ready || v.MinPlayers != 2 {
		t.Errorf("a room of one: %s, starting in %d, ready %v, needing %d", v.Room.State, v.Room.StartsIn, v.You.Ready,
			v.MinPlayers)
	}
	do(l, bob.Token, "join", "room", code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
}
