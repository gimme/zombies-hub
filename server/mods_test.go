package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// setMods is the page of the player with that token saying which mods their room has: the error it answers, or "".
func setMods(l *lobby, token string, ids ...string) string {
	if ids == nil {
		ids = []string{}
	}
	text, _ := json.Marshal(map[string]any{"mods": ids})
	req := httptest.NewRequest(http.MethodPost, "/lobby/room", strings.NewReader(string(text)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	var answer struct {
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &answer)
	return answer.Error
}

func TestRoomMods(t *testing.T) {
	const mod = "uniquewallweapons"
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	if v := l.viewOf(bob.Token); len(v.Mods) != len(modList) || v.Mods[0].ID != mod || v.Room.Mods != nil {
		t.Errorf("a new room's mods: %+v, of %+v", v.Room.Mods, v.Mods)
	}
	// the list of rooms says which each has, to those in no room too: none yet, which it leaves out
	cy := l.hello("", "Cy")
	if data, _ := json.Marshal(l.viewOf(cy.Token).Rooms); strings.Contains(string(data), "mods") {
		t.Errorf("a new room in the list: %s", data)
	}

	if err := setMods(l, bob.Token, mod); err == "" {
		t.Errorf("a player who isn't the host set the mods")
	}
	if err := setMods(l, ann.Token, "nosuchmod"); err != "There is no mod nosuchmod." {
		t.Errorf("a mod that isn't: %q", err)
	}
	if got := l.viewOf(ann.Token).Room.Mods; got != nil {
		t.Errorf("the mods after those it can't have: %v", got)
	}
	// a call that is refused changes nothing else it asked for
	private := true
	l.mu.Lock()
	err := l.setRoom(l.playerOf(ann.Token), "", "", &private, &[]string{"nosuchmod"}, 0)
	l.mu.Unlock()
	if v := l.viewOf(ann.Token); err == nil || v.Room.Private {
		t.Errorf("visibility and a mod that isn't: %v, private %v", err, v.Room.Private)
	}

	// another mod has everyone say again that they are ready, as another map does; the same mods don't
	readyAll(l, ann.Token, bob.Token)
	do(l, ann.Token, "room", "private", "true")
	if err := setMods(l, ann.Token); err != "" {
		t.Fatal(err)
	}
	if v := l.viewOf(ann.Token); !v.You.Ready || !v.Room.Members[1].Ready {
		t.Errorf("readiness, the mods the same: %+v", v.Room)
	}
	if err := setMods(l, ann.Token, mod, mod); err != "" {
		t.Fatal(err)
	}
	if v := l.viewOf(bob.Token); !slices.Equal(v.Room.Mods, []string{mod}) || v.You.Ready || v.Room.Members[0].Ready ||
		v.Room.StartsIn != 0 {
		t.Errorf("the room, a mod turned on: %+v", v.Room)
	}
	// and in the list, though the room is private, and listed without its code
	if rooms := l.viewOf(cy.Token).Rooms; len(rooms) != 1 || rooms[0].Code != "" || !slices.Equal(rooms[0].Mods, []string{mod}) {
		t.Errorf("the room in the list, a mod turned on: %+v", rooms)
	}

	// its game has it, on the engine's command line before the map loads, and it stays as the game runs
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	l.mu.Lock()
	args := l.playerOf(ann.Token).room.game.g.engine.(*t5Engine).args
	l.mu.Unlock()
	set, load := slices.Index(args, "bo1_mod_uniquewallweapons"), slices.Index(args, "+map")
	if set < 1 || args[set-1] != "+set" || args[set+1] != "1" || load < set {
		t.Errorf("the game's command line: %v", args)
	}
	if err := setMods(l, ann.Token); err != "The mods can't change while in game." {
		t.Errorf("the mods as the game ran: %q", err)
	}
	if got := l.viewOf(ann.Token).Room.Mods; !slices.Equal(got, []string{mod}) {
		t.Errorf("the mods after a change in game: %v", got)
	}
}

func TestNoModsNoArgs(t *testing.T) {
	if args := modArgs(nil); len(args) != 0 {
		t.Errorf("no mods, command line: %v", args)
	}
}

// Several mods on at once: each once, in the list's order, whatever order the page names them in, and a dvar each,
// at 1 for a switch and at its value for a mod with options.
func TestSeveralMods(t *testing.T) {
	on, err := modsNamed([]string{"zombiecap=32", "grief", "uniquewallweapons", "grief"})
	if err != nil || !slices.Equal(on, []string{"uniquewallweapons", "grief", "zombiecap=32"}) {
		t.Fatalf("the mods named: %v, %v", on, err)
	}
	want := []string{"+set", "bo1_mod_uniquewallweapons", "1", "+set", "bo1_mod_grief", "1",
		"+set", "bo1_mod_zombiecap", "32"}
	if args := modArgs(on); !slices.Equal(args, want) {
		t.Errorf("their command line: %v", args)
	}
}

// A mod with options has one of their values: the first, the game's own, is the mod off; one yet to come, or that
// isn't one of them, is refused, and so is a name that is neither a switch's nor an option's.
func TestModOptions(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  []string
		err   string
	}{
		{[]string{"zombiecap=32"}, []string{"zombiecap=32"}, ""},
		{[]string{"zombiecap=24"}, nil, ""},
		{[]string{"zombiecap=32", "zombiecap=24"}, nil, ""}, // as it was named last
		{[]string{"zombiecap=24", "grief", "zombiecap=32"}, []string{"grief", "zombiecap=32"}, ""},
		{[]string{"zombiecap=40"}, nil, "Zombie Cap can't be 40 yet."},
		{[]string{"zombiecap=48"}, nil, "Zombie Cap can't be 48 yet."},
		{[]string{"zombiecap=30"}, nil, "Zombie Cap can't be 30."},
		{[]string{"zombiecap="}, nil, "Zombie Cap can't be ."},
		{[]string{"zombiecap"}, nil, "There is no mod zombiecap."},
		{[]string{"grief=1"}, nil, "There is no mod grief=1."},
	} {
		on, err := modsNamed(c.names)
		said := ""
		if err != nil {
			said = err.Error()
		}
		if said != c.err || !slices.Equal(on, c.want) {
			t.Errorf("the mods named %v: %v, %q; want %v, %q", c.names, on, said, c.want, c.err)
		}
	}
	// the page is told of each option, and of those it can't offer yet
	zombies := modList[slices.IndexFunc(modList, func(m mod) bool { return m.ID == "zombiecap" })]
	if text, _ := json.Marshal(zombies.Options); string(text) !=
		`[{"value":24},{"value":32},{"value":40,"soon":true},{"value":48,"soon":true}]` {
		t.Errorf("the options the page is told: %s", text)
	}
}

// Grief's teams: the first half of a game's players, rounded up, against the rest, as the game's scripts have them. A
// room with it on says each member's, as its next game would have them, and then as its game has them, by their
// seats; and the game's report has each player's line with their team, the first team's first, the most cash first.
func TestGriefTeams(t *testing.T) {
	for players, want := range map[int][]int{1: {1}, 2: {1, 2}, 3: {1, 1, 2}, 4: {1, 1, 2, 2}, 5: {1, 1, 1, 2, 2},
		8: {1, 1, 1, 1, 2, 2, 2, 2}} {
		var got []int
		for place := range players {
			got = append(got, teamOf(place, players))
		}
		if !slices.Equal(got, want) {
			t.Errorf("the teams of %d players: %v, want %v", players, got, want)
		}
	}

	l := testLobby(t)
	ann, bob, cy, dee := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy"), l.hello("", "Dee")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	for _, p := range []hello{bob, cy, dee} {
		do(l, p.Token, "join", "room", code)
	}
	teams := func() []int {
		var teams []int
		for _, m := range l.viewOf(bob.Token).Room.Members {
			teams = append(teams, m.Team)
		}
		return teams
	}
	if got := teams(); !slices.Equal(got, []int{0, 0, 0, 0}) {
		t.Errorf("the teams of a room without Grief: %v", got)
	}
	if err := setMods(l, ann.Token, "grief"); err != "" {
		t.Fatal(err)
	}
	if got := teams(); !slices.Equal(got, []int{1, 1, 2, 2}) {
		t.Errorf("the teams of a room of four: %v", got)
	}
	do(l, dee.Token, "leave")
	if got := teams(); !slices.Equal(got, []int{1, 1, 2}) {
		t.Errorf("the teams of a room of three: %v", got)
	}

	startGame(t, l, ann.Token, bob.Token, cy.Token)
	waitRunning(t, l, ann.Token)
	annAt, bobAt, cyAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token), l.seatAddr(cy.Token)
	for _, at := range []string{annAt, bobAt, cyAt} {
		l.command(code + " print bo1_lobby: in " + at)
	}
	l.command(code + " print bo1_lobby: start")
	waitView(t, l, ann.Token, "the game playing", func(v view) bool { return v.Room.State == "playing" })
	// a member who leaves the room leaves the others in the teams the game has them in
	do(l, bob.Token, "leave")
	var got []int
	for _, m := range l.viewOf(ann.Token).Room.Members {
		got = append(got, m.Team)
	}
	if !slices.Equal(got, []int{1, 2}) {
		t.Errorf("the teams of Ann and Cy in the game, Bob gone from the room: %v", got)
	}
	l.command(code + " print bo1_lobby: stats " + annAt + " 5000 300 40 12 1 2 1")
	l.command(code + " print bo1_lobby: stats " + bobAt + " 2140 900 31 9 2 1 0")
	l.command(code + " print bo1_lobby: stats " + cyAt + " 9000 50 60 20 0 0 4")
	l.command(code + " print bo1_lobby: over 3")
	room := waitView(t, l, ann.Token, "the game ending", func(v view) bool { return v.Room.State == "ending" }).Room
	var lines []string
	if room.Result != nil {
		for _, p := range room.Result.Players {
			lines = append(lines, fmt.Sprintf("%s team %d cash %d", p.Name, p.Team, p.Cash))
		}
	}
	if want := []string{"Bob team 1 cash 900", "Ann team 1 cash 300", "Cy team 2 cash 50"}; !slices.Equal(lines, want) {
		t.Errorf("the report's lines: %q, want %q", lines, want)
	}
	// and the report names the game's mods
	if room.Result == nil || !slices.Equal(room.Result.Mods, []string{"grief"}) {
		t.Errorf("the report's mods: %+v", room.Result)
	}
}
