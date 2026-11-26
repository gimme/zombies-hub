package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testLobby is a server whose rooms' games are the stand-in game (fakeGame), which answers on its port and prints what
// it is told to ("print ..."): as the game says who is in it, say.
func testLobby(t *testing.T) *lobby {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l := newLobby(ctx)
	l.maps = func() []string { return []string{"zombie_theater", "zombie_pentagon"} }
	l.defaultMap = "zombie_theater"
	l.relay = newRelay(l, &certificates{})
	l.gamePorts = []int{freePort(t)}
	l.sweepEvery = 50 * time.Millisecond
	l.countdown = 50 * time.Millisecond
	l.privateFor = 0 // a public room is listed with its code as soon as it is made (TestNewRoomsListedPrivate)
	l.minPlayers = 1 // most tests have a room of one (TestRoomsNeedTwoPlayers)
	root := t.TempDir()
	t.Setenv("FAKE_BO1_GAME", "answer")
	l.t5().newGame = func(code, mapName string, port, players int, mods []string) *game {
		args := append([]string{"+set", "bo1_players", strconv.Itoa(players)}, modArgs(mods)...)
		return &game{name: code, wine: os.Args[0], dir: root, home: filepath.Join(root, "home"),
			engine: &t5Engine{exe: "BO1Zombies.exe", args: append(args, "+map", mapName)},
			logDir: filepath.Join(root, "logs"), port: port, output: io.Discard}
	}
	go l.sweep()
	t.Cleanup(func() {
		cancel()
		l.games.Wait()
	})
	return l
}

// do is what a player's page asks of the server, as the player with that token: the error it answers, or "".
func do(l *lobby, token, action string, args ...string) string {
	body := map[string]any{}
	for i := 0; i+1 < len(args); i += 2 {
		if args[i+1] == "true" || args[i+1] == "false" {
			body[args[i]] = args[i+1] == "true"
		} else if n, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
			body[args[i]] = n
		} else {
			body[args[i]] = args[i+1]
		}
	}
	text, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/lobby/"+action, strings.NewReader(string(text)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	var answer struct {
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &answer)
	return answer.Error
}

// pagesOpen has the players' pages open.
func pagesOpen(l *lobby, tokens ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, token := range tokens {
		l.playerOf(token).streams = 1
	}
}

// haveMap has the players' pages say they have all of their room's map's game files, in the browser's store.
func haveMap(l *lobby, tokens ...string) {
	for _, token := range tokens {
		if v := l.viewOf(token); v.Room != nil {
			do(l, token, "files", "map", v.Room.Map, "done", "true")
		}
	}
}

// readyAll has the players say they are ready, in their pages (Ready), which have the room's map.
func readyAll(l *lobby, tokens ...string) {
	pagesOpen(l, tokens...)
	haveMap(l, tokens...)
	for _, token := range tokens {
		do(l, token, "ready", "ready", "true")
	}
}

// playsAs is the name the game of the player's room knows them by, or is to: theirs, unique in the room.
func (l *lobby) playsAs(token string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.playerOf(token).name
}

func (l *lobby) viewOf(token string) view {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.view(l.playerOf(token))
}

// waitRunning waits until the game of the player's room runs, and takes console commands.
func waitRunning(t *testing.T, l *lobby, token string) {
	t.Helper()
	waitFor(t, "the game running", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		rg := l.playerOf(token).room.game
		return rg != nil && rg.g.running()
	})
}

// startGame has the players say they are ready (readyAll), and waits for the game of the first one's room to start,
// once its countdown has ended.
func startGame(t *testing.T, l *lobby, tokens ...string) {
	t.Helper()
	readyAll(l, tokens...)
	waitView(t, l, tokens[0], "the game started", func(v view) bool { return v.Room.State != "open" })
}

// waitView waits until the player's view is as ok wants it, and returns it.
func waitView(t *testing.T, l *lobby, token, what string, ok func(v view) bool) view {
	t.Helper()
	var v view
	waitFor(t, what, func() bool {
		v = l.viewOf(token)
		return ok(v)
	})
	return v
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"Ann":                          "Ann",
		`  Bob   "the" +Builder;  `:    "Bob the Builder",
		"^1red\\name":                  "1redname",
		"a very long name indeed here": "a very long nam",
		"+;\"":                         "Player",
		"%s^7\\":                       "s7",
		// the signs the game keeps, as Discord's names have them
		"-=Ann=-":          "-Ann-",
		"anders.m_99":      "anders.m_99",
		"[ABC] O'Neil!":    "[ABC] O'Neil!",
		"(Who?) | Zed":     "(Who?) | Zed",
		"  ( Ann":          "(Ann", // a space comes after a letter or digit, as in the game
		"!!!":              "Player",
		"!!!!!!!!!!!!!!!a": "Player",
		// letters without their marks, and styled ones plain
		"Zoë":       "Zoe",
		"Søren Ærø": "Soren AEro",
		"Łukasz":    "Lukasz",
		"Ｚｏｅ":       "Zoe",
		"𝐙𝐨𝐞 🔥":     "Zoe",
		"🔥🔥":        "Player",
		"Александр": "Player",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstalledMaps(t *testing.T) {
	maps := installedMaps(func() []string {
		return []string{"zone/common/zombie_pentagon.ff", "zone/common/zombie_theater.ff", "zone/english/en_zombie_moon.ff",
			"zone/common/zombie_moon_patch.ff", "zone/common/mp_array.ff", "main/zombie_cosmodrome.ff"}
	})()
	if strings.Join(maps, " ") != "zombie_theater zombie_pentagon" {
		t.Errorf("maps: %v", maps)
	}
}

func TestRooms(t *testing.T) {
	l := testLobby(t)
	ann, bob, cy, dee, eve := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy"), l.hello("", "Dee"), l.hello("", "Eve")
	if again := l.hello(ann.Token, "Annie"); again.Token != ann.Token || again.Name != "Annie" {
		t.Errorf("hello again: %+v", again)
	}
	if again := l.hello("forgotten", "Ann"); again.Token == "forgotten" {
		t.Errorf("a token the server doesn't know was taken")
	}
	// a name the room has already, in any case, takes a number in the room's game, which takes each name once; the
	// page shows each player by their own, which two may share
	twin, other := l.hello("", "annie"), l.hello("", "Annie")
	do(l, ann.Token, "create")
	do(l, twin.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	do(l, other.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	if names := l.viewOf(ann.Token).Room.Members; names[1].Name != "annie" || names[2].Name != "Annie" {
		t.Errorf("names in a room: %+v", names)
	}
	if a, b, c := l.playsAs(ann.Token), l.playsAs(twin.Token), l.playsAs(other.Token); a != "Annie" || b != "annie 2" || c != "Annie 3" {
		t.Errorf("the names a room's game is to know its players by: %q, %q, %q", a, b, c)
	}
	if again := l.hello(twin.Token, "Annie"); again.Name != "Annie" || l.viewOf(twin.Token).You.Name != "Annie" ||
		l.playsAs(twin.Token) != "Annie 2" {
		t.Errorf("renamed in the room: %+v, playing as %q", again, l.playsAs(twin.Token))
	}
	do(l, ann.Token, "leave")
	do(l, twin.Token, "leave")
	do(l, other.Token, "leave")

	if err := do(l, ann.Token, "create", "map", "zombie_pentagon"); err != "" {
		t.Fatal(err)
	}
	if err := do(l, bob.Token, "create", "map", "mp_nuked", "private", "true"); err != "" {
		t.Fatal(err)
	}
	public, private := l.viewOf(ann.Token).Room, l.viewOf(bob.Token).Room
	if public.Map != "zombie_pentagon" || !public.Host || public.State != "open" || private.Map != "zombie_theater" ||
		!private.Private {
		t.Fatalf("rooms: %+v, %+v", public, private)
	}
	got := listed(l.viewOf(cy.Token).Rooms)
	if !maps.Equal(got, map[string]string{"Annie": public.Code, "Bob": "private"}) {
		t.Errorf("Cy sees rooms %v, want Bob's without its code: it is private", got)
	}
	got = listed(l.viewOf(bob.Token).Rooms)
	if !maps.Equal(got, map[string]string{"Annie": public.Code, "Bob": private.Code}) {
		t.Errorf("Bob sees rooms %v, want his own with its code", got)
	}

	// a private room by its code, in any case
	if err := do(l, cy.Token, "join", "room", strings.ToLower(private.Code)); err != "" {
		t.Fatal(err)
	}
	if err := do(l, cy.Token, "room", "map", "zombie_pentagon"); !strings.Contains(err, "host") {
		t.Errorf("Cy picked Bob's room's map: %q", err)
	}
	if err := do(l, dee.Token, "join", "room", "ZZZZ"); err != "There is no room ZZZZ." {
		t.Errorf("a room that isn't: %q", err)
	}
	// a room as its host sets its size: of three
	if err := do(l, bob.Token, "room", "size", "3"); err != "" {
		t.Fatal(err)
	}
	do(l, dee.Token, "join", "room", private.Code)
	if err := do(l, eve.Token, "join", "room", private.Code); err != "Room "+private.Code+" is full." {
		t.Errorf("a full room: %q", err)
	}

	// its host leaves: the next to have joined is
	do(l, bob.Token, "leave")
	room := l.viewOf(cy.Token).Room
	if !room.Host || room.Members[0].Name != "Cy" || len(room.Members) != 2 {
		t.Errorf("after its host left: %+v", room)
	}
	if err := do(l, cy.Token, "room", "map", "zombie_pentagon", "private", "false"); err != "" {
		t.Fatal(err)
	}
	// a map the server lacks (one the page shows greyed out), and one there isn't, leave the room's as it is
	if err := do(l, cy.Token, "room", "map", "zombie_moon"); err != "The server doesn't have Moon." {
		t.Errorf("a map the server lacks: %q", err)
	}
	if err := do(l, cy.Token, "room", "map", "mp_nuked"); err != "There is no map mp_nuked." {
		t.Errorf("a map that isn't: %q", err)
	}
	if room := l.viewOf(cy.Token).Room; room.Map != "zombie_pentagon" {
		t.Errorf("the room's map after maps it can't have: %s", room.Map)
	}
	got = listed(l.viewOf(eve.Token).Rooms)
	if !maps.Equal(got, map[string]string{"Annie": public.Code, "Cy": private.Code}) {
		t.Errorf("Eve sees rooms %v, want Cy's with its code, now public", got)
	}
	// the last to leave ends it
	do(l, cy.Token, "leave")
	do(l, dee.Token, "join", "room", public.Code)
	if got := listed(l.viewOf(eve.Token).Rooms); !maps.Equal(got, map[string]string{"Annie": public.Code}) {
		t.Errorf("Eve sees rooms %v, after everyone left Cy's", got)
	}
	if err := do(l, "nobody", "leave"); err != errUnknown.Error() {
		t.Errorf("an unknown token: %q", err)
	}
}

// listed is the rooms in a player's list, by host: each one's code, or "private" for one listed without it.
func listed(rooms []roomItem) map[string]string {
	m := map[string]string{}
	for _, r := range rooms {
		switch {
		case r.Private && r.Code == "":
			m[r.Host] = "private"
		case !r.Private && r.Code != "":
			m[r.Host] = r.Code
		default:
			m[r.Host] = fmt.Sprintf("neither: %+v", r)
		}
	}
	return m
}

// A room's game is the title of its map. The view lists the titles the server hosts, and the list of rooms says each
// room's. The host picks the title, and the room goes to a map of it: the map it had of it last, else the server's
// default map where that is of it, else the first of it that the server has. That is a change of the map, allowed and
// refused as one is: the players who were ready are no longer, and the marks of the other title's maps go. A mark is
// for a map of the room's title.
func TestRoomTitles(t *testing.T) {
	l := testLobby(t)
	const theater, pentagon, town, tomb = "zombie_theater", "zombie_pentagon", "zm_standard_town", "zm_classic_tomb"
	l.maps = func() []string { return []string{theater, pentagon, town, tomb} }
	ann, bob, cy := l.hello("", "Ann").Token, l.hello("", "Bob").Token, l.hello("", "Cy").Token
	if got := l.viewOf(ann).Titles; !slices.Equal(got, []titleView{{ID: t5, Name: "Black Ops"}, {ID: t6, Name: "Black Ops II"}}) {
		t.Errorf("the titles: %+v", got)
	}
	do(l, ann, "create")
	code := l.viewOf(ann).Room.Code
	do(l, bob, "join", "room", code)
	do(l, cy, "join", "room", code)
	room := func(token string) (map[string]string, *roomView) {
		v := l.viewOf(token)
		marks := map[string]string{}
		for _, m := range v.Room.Members {
			marks[m.Name] = m.Mark
		}
		if len(v.Rooms) != 1 || v.Rooms[0].Map != v.Room.Map || v.Rooms[0].Of != v.Room.Of {
			t.Errorf("the room in the list: %+v, the room %+v", v.Rooms, v.Room)
		}
		return marks, v.Room
	}
	if _, r := room(cy); r.Map != theater || r.Of != t5 {
		t.Fatalf("a new room: %+v", r)
	}
	if err := do(l, cy, "mark", "map", town); err != "Town is not a Black Ops map." {
		t.Errorf("a mark of a map of the other title: %q", err)
	}
	do(l, bob, "mark", "map", pentagon)
	readyAll(l, bob)

	// Black Ops II: its first map, the server's default map being Black Ops'; Bob is no longer ready, and his mark of
	// Five is gone
	if err := do(l, ann, "room", "title", t6); err != "" {
		t.Fatal(err)
	}
	marks, r := room(bob)
	if r.Map != town || r.Of != t6 || marks["Bob"] != "" || l.viewOf(bob).You.Ready {
		t.Fatalf("the room on Black Ops II: %+v, marks %v", r, marks)
	}
	if err := do(l, bob, "mark", "map", theater); err != "Kino der Toten is not a Black Ops II map." {
		t.Errorf("a mark of a Black Ops map in a Black Ops II room: %q", err)
	}
	do(l, cy, "mark", "map", tomb)
	do(l, bob, "mark", "map", town)

	// Origins, then Black Ops again: back to Kino der Toten, and the marks of Black Ops II's maps go
	do(l, ann, "room", "map", tomb)
	if err := do(l, ann, "room", "title", t5); err != "" {
		t.Fatal(err)
	}
	if marks, r := room(cy); r.Map != theater || marks["Bob"] != "" || marks["Cy"] != "" {
		t.Errorf("the room on Black Ops again: %+v, marks %v", r, marks)
	}
	// Five, and Black Ops II and back: each time the map the room had last of the title
	do(l, ann, "room", "map", pentagon)
	do(l, ann, "room", "title", t6)
	if _, r := room(ann); r.Map != tomb {
		t.Errorf("Black Ops II again: %s, want the room's last map of it", r.Map)
	}
	do(l, ann, "room", "title", t6, "map", town) // a title, and a map of it
	do(l, ann, "room", "title", t5)
	if _, r := room(ann); r.Map != pentagon {
		t.Errorf("Black Ops again: %s, want the room's last map of it", r.Map)
	}

	// the title the room has already changes nothing: who is ready stays so
	readyAll(l, bob)
	if err := do(l, ann, "room", "title", t5); err != "" || !l.viewOf(bob).You.Ready {
		t.Errorf("the title the room has: %q, Bob ready %v", err, l.viewOf(bob).You.Ready)
	}
	for _, c := range []struct{ token, title, mapName, want string }{
		{bob, t6, "", "Only the host picks the map."},
		{ann, "t9", "", "There is no game t9."},
		{ann, t7, "", "The server doesn't have Black Ops III."},
		{ann, t6, theater, "Kino der Toten is not a Black Ops II map."},
		{ann, t6, "zm_classic_prison", "The server doesn't have Mob of the Dead."},
	} {
		args := []string{"title", c.title}
		if c.mapName != "" {
			args = append(args, "map", c.mapName)
		}
		if err := do(l, c.token, "room", args...); err != c.want {
			t.Errorf("room %v: %q, want %q", args, err, c.want)
		}
	}
	if _, r := room(ann); r.Map != pentagon {
		t.Errorf("the room's map after the refusals: %s", r.Map)
	}

	// a new room of the game that its maker last picked, which their page remembers: on a map of it, unless they name
	// a map; a game that the server doesn't host leaves the room on the server's default map
	for _, c := range []struct{ title, mapName, want string }{{t6, "", town}, {t6, theater, theater}, {t7, "", theater}} {
		args := []string{"title", c.title}
		if c.mapName != "" {
			args = append(args, "map", c.mapName)
		}
		if err := do(l, cy, "create", args...); err != "" {
			t.Fatalf("create %v: %s", args, err)
		}
		if r := l.viewOf(cy).Room; r.Map != c.want {
			t.Errorf("create %v: a room on %s, want %s", args, r.Map, c.want)
		}
	}
	do(l, cy, "leave")

	// a new room goes to the server's default map when that is of the title it picks
	l.mu.Lock()
	l.defaultMap = tomb
	l.mu.Unlock()
	do(l, cy, "create", "map", theater)
	if err := do(l, cy, "room", "title", t6); err != "" {
		t.Fatal(err)
	}
	if r := l.viewOf(cy).Room; r.Map != tomb {
		t.Errorf("a room on Black Ops II, the server's default map being Origins: %s", r.Map)
	}
	do(l, cy, "leave")

	// in game, the title can't change, as the map can't
	startGame(t, l, ann, bob)
	if err := do(l, ann, "room", "title", t6); err != "The map can't change while in game." {
		t.Errorf("the title in game: %q", err)
	}

	// a server without Black Ops II's maps hosts Black Ops alone
	l.mu.Lock()
	l.maps = func() []string { return []string{theater, pentagon} }
	l.mu.Unlock()
	if got := l.viewOf(cy).Titles; !slices.Equal(got, []titleView{{ID: t5, Name: "Black Ops"}}) {
		t.Errorf("the titles of a server of Black Ops: %+v", got)
	}
	do(l, cy, "create")
	if err := do(l, cy, "room", "title", t6); err != "The server doesn't have Black Ops II." {
		t.Errorf("Black Ops II on a server without it: %q", err)
	}
}

// A room takes four players until its host says how many: as many as a game needs at least and as the server's rooms
// hold at most, and no fewer than it has. The room and the list of rooms say how many; who is ready stays so, and the
// size may change while the room's game runs.
func TestRoomSize(t *testing.T) {
	l := testLobby(t)
	l.minPlayers, l.roomSize = 2, 6
	var tokens []string
	for _, name := range []string{"Ann", "Bob", "Cy", "Dee", "Eve", "Fay", "Gil"} {
		tokens = append(tokens, l.hello("", name).Token)
	}
	ann, bob, eve, fay, gil := tokens[0], tokens[1], tokens[4], tokens[5], tokens[6]
	do(l, ann, "create")
	code := l.viewOf(ann).Room.Code
	full := "Room " + code + " is full."
	if v := l.viewOf(gil); l.viewOf(ann).Room.Size != 4 || v.Rooms[0].Size != 4 || v.MinPlayers != 2 || v.Size != 6 {
		t.Errorf("a new room takes %d players, %d in the list, of %d to %d", l.viewOf(ann).Room.Size, v.Rooms[0].Size,
			v.MinPlayers, v.Size)
	}
	for _, token := range tokens[1:4] {
		if err := do(l, token, "join", "room", code); err != "" {
			t.Fatal(err)
		}
	}
	if err := do(l, eve, "join", "room", code); err != full {
		t.Errorf("a fifth player in a room of four: %q", err)
	}

	// the host's to set, among the sizes a room on this server may have, and no fewer than are in it
	if err := do(l, bob, "room", "size", "5"); !strings.Contains(err, "host") {
		t.Errorf("a player who isn't the host set the room's size: %q", err)
	}
	for _, size := range []string{"1", "7", "-1"} {
		if err := do(l, ann, "room", "size", size); err != "Room size can't be "+size+"." {
			t.Errorf("a room of %s: %q", size, err)
		}
	}
	if err := do(l, ann, "room", "size", "3"); err != "The room has 4 players already." {
		t.Errorf("a room of three with four in it: %q", err)
	}
	// a call that is refused changes nothing else it asked for
	if err := do(l, ann, "room", "size", "7", "private", "true"); err == "" || l.viewOf(ann).Room.Private {
		t.Errorf("visibility and a size it can't have: %q, private %v", err, l.viewOf(ann).Room.Private)
	}
	if size := l.viewOf(ann).Room.Size; size != 4 {
		t.Errorf("the room's size after those it can't have: %d", size)
	}

	// a bigger room takes more players, and whoever was ready stays so: the game they are ready for is the same
	pagesOpen(l, ann)
	haveMap(l, ann)
	do(l, ann, "ready", "ready", "true")
	if err := do(l, ann, "room", "size", "5"); err != "" {
		t.Fatal(err)
	}
	if v := l.viewOf(ann); v.Room.Size != 5 || !v.You.Ready {
		t.Errorf("the room, set to five: of %d, its host ready %v", v.Room.Size, v.You.Ready)
	}
	if err := do(l, eve, "join", "room", code); err != "" {
		t.Errorf("a fifth player in a room of five: %q", err)
	}
	if err := do(l, fay, "join", "room", code); err != full {
		t.Errorf("a sixth player in a room of five: %q", err)
	}
	do(l, ann, "room", "size", "6")
	if err := do(l, fay, "join", "room", code); err != "" {
		t.Errorf("a sixth player in a room of six: %q", err)
	}
	if err := do(l, gil, "join", "room", code); err != full {
		t.Errorf("a seventh player in a room of six: %q", err)
	}
	if rooms := l.viewOf(gil).Rooms; len(rooms) != 1 || rooms[0].Players != 6 || rooms[0].Size != 6 {
		t.Errorf("the room in the list: %+v, want 6 players of 6", rooms)
	}

	// a smaller one once players have left, down to those a game needs; it keeps its size as its host changes
	for _, token := range tokens[2:6] {
		do(l, token, "leave")
	}
	if err := do(l, ann, "room", "size", "2"); err != "" {
		t.Fatal(err)
	}
	if err := do(l, gil, "join", "room", code); err != full {
		t.Errorf("a third player in a room of two: %q", err)
	}
	do(l, ann, "leave")
	if v := l.viewOf(bob); !v.Room.Host || v.Room.Size != 2 {
		t.Errorf("the room after its host left: %+v", v.Room)
	}
	// while its game runs too, which is its players': the room's size is who may join it next
	do(l, bob, "room", "size", "3")
	do(l, gil, "join", "room", code)
	startGame(t, l, bob, gil)
	if err := do(l, bob, "room", "size", "4"); err != "" || l.viewOf(gil).Room.Size != 4 {
		t.Errorf("the room's size as its game ran: %q, %d", err, l.viewOf(gil).Room.Size)
	}
}

// A new room's size is four where the server's rooms may be of four, and otherwise the nearest to it they may be.
func TestNewRoomSize(t *testing.T) {
	l := testLobby(t)
	for _, c := range []struct{ min, max, want int }{{2, 4, 4}, {1, 8, 4}, {2, 3, 3}, {1, 1, 1}, {5, 8, 5}, {6, 6, 6}} {
		l.minPlayers, l.roomSize = c.min, c.max
		ann := l.hello("", "Ann")
		do(l, ann.Token, "create")
		if size := l.viewOf(ann.Token).Room.Size; size != c.want {
			t.Errorf("a new room on a server with rooms of %d to %d takes %d players, want %d", c.min, c.max, size, c.want)
		}
		do(l, ann.Token, "leave")
	}
}

// A new room is listed at once, but as private for a while, without its code, for its maker to make it private before
// anyone sees its code; the pages hear of it once its code is listed. Its code joins it all along.
func TestNewRoomsListedPrivate(t *testing.T) {
	l := testLobby(t)
	l.privateFor = 200 * time.Millisecond
	ann, bob, cy, dee := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy"), l.hello("", "Dee")
	pagesOpen(l, ann.Token, bob.Token, cy.Token, dee.Token) // so that nothing else changes as they wait
	made := time.Now()
	do(l, ann.Token, "create")
	do(l, bob.Token, "create")
	do(l, bob.Token, "room", "private", "true") // public for a moment, whose code no one saw
	code, bobs := l.viewOf(ann.Token).Room.Code, l.viewOf(bob.Token).Room.Code
	if got := listed(l.viewOf(cy.Token).Rooms); !maps.Equal(got, map[string]string{"Ann": "private", "Bob": "private"}) {
		t.Errorf("Cy sees rooms %v as soon as they are made, want both without their codes", got)
	}
	if got := listed(l.viewOf(ann.Token).Rooms); !maps.Equal(got, map[string]string{"Ann": code, "Bob": "private"}) {
		t.Errorf("Ann sees rooms %v, want her own with its code at once", got)
	}
	if err := do(l, dee.Token, "join", "room", code); err != "" {
		t.Errorf("joining a room listed as private by its code: %q", err)
	}
	l.mu.Lock()
	changed := l.changed
	l.mu.Unlock()
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change as Ann's room's code was listed")
	}
	rooms := l.viewOf(cy.Token).Rooms
	if got := listed(rooms); time.Since(made) < l.privateFor ||
		!maps.Equal(got, map[string]string{"Ann": code, "Bob": "private"}) {
		t.Errorf("Cy sees rooms %v, %s after they were made: want Ann's with its code", got, time.Since(made))
	}
	if i := slices.IndexFunc(rooms, func(r roomItem) bool { return r.Code == code }); i < 0 || rooms[i].Players != 2 {
		t.Errorf("Ann's room in Cy's list: %+v, want 2 players", rooms)
	}
	// one made public later has its code listed at once
	do(l, bob.Token, "room", "private", "false")
	if got := listed(l.viewOf(cy.Token).Rooms); !maps.Equal(got, map[string]string{"Ann": code, "Bob": bobs}) {
		t.Errorf("Cy sees rooms %v, want Bob's with its code too, now public", got)
	}
}

// seatOf is the address of the player's seat in their room's game.
func (l *lobby) seatAddr(token string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.playerOf(token)
	return p.room.seatOf(p).local.String()
}

// gameGot waits until the game of the player's room got the console line: the stand-in game echoes each ("]<line>").
func gameGot(t *testing.T, l *lobby, line string) {
	t.Helper()
	waitFor(t, "the game told "+line, func() bool {
		logs, _ := filepath.Glob(filepath.Join(l.t5().newGame("", "", 0, 0, nil).logDir, "game-*.log"))
		for _, f := range logs {
			if b, _ := os.ReadFile(f); strings.Contains(string(b), "]"+line+"\n") {
				return true
			}
		}
		return false
	})
}

func TestRoomsGame(t *testing.T) {
	l := testLobby(t)
	ann, bob, cy, dee := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy"), l.hello("", "Dee")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	do(l, cy.Token, "join", "room", code)
	startGame(t, l, ann.Token, bob.Token, cy.Token)
	room := l.viewOf(bob.Token).Room
	if room.State != "loading" || !room.Seat || !room.Join {
		t.Fatalf("Bob's room as its game loads: %+v", room)
	}
	if err := do(l, ann.Token, "room", "map", "zombie_pentagon"); err == "" {
		t.Errorf("the map changed while the game ran")
	}
	// the server's games are all running (one, here): no one may be ready for another
	other := l.hello("", "Other")
	do(l, other.Token, "create")
	pagesOpen(l, other.Token)
	if err := do(l, other.Token, "ready", "ready", "true"); !strings.HasPrefix(err, "The server is full") {
		t.Errorf("ready for a game past the server's games at once: %q", err)
	}
	if v := l.viewOf(other.Token); !v.Full || v.You.Ready || v.Room.State != "open" {
		t.Errorf("as the server's games are all running: %+v, %+v", v, v.Room)
	}
	// nor does the name the game knows a player by change, the one they joined it with: their own does, on the page
	if again := l.hello(bob.Token, "Robert"); again.Name != "Robert" || l.playsAs(bob.Token) != "Bob" ||
		l.viewOf(ann.Token).Room.Members[1].Name != "Robert" {
		t.Errorf("Bob renamed himself as the game ran: %+v, playing as %q", again, l.playsAs(bob.Token))
	}
	// the room takes no one while its game runs: not as it loads, nor once it has begun
	locked := "Room " + code + " is in game."
	if err := do(l, dee.Token, "join", "room", code); err != locked {
		t.Errorf("Dee joined a room whose game loads: %q", err)
	}

	// the game says who is in it, and when round 1 begins
	annAt, bobAt, cyAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token), l.seatAddr(cy.Token)
	if annAt == bobAt || !strings.HasSuffix(annAt, ":28960") {
		t.Errorf("seats at %s and %s", annAt, bobAt)
	}
	waitRunning(t, l, ann.Token)
	// Ann's page reloads as she loads: out, and in again, still to play
	l.command(code + " print bo1_lobby: in " + annAt)
	l.command(code + " print bo1_lobby: out " + annAt)
	l.command(code + " print bo1_lobby: in " + annAt)
	l.command(strings.ToLower(code) + " print bo1_lobby: in " + bobAt)
	l.command(code + " print bo1_lobby: in " + cyAt)
	l.command(code + " print bo1_lobby: start")
	room = waitView(t, l, ann.Token, "round 1", func(v view) bool { return v.Room.State == "playing" }).Room
	if !room.Members[0].InGame || !room.Members[1].InGame || !room.Members[2].InGame || room.Left {
		t.Errorf("who is in the game: %+v, %+v", room.Members, room)
	}
	if err := do(l, dee.Token, "join", "room", code); err != locked {
		t.Errorf("Dee joined a room whose game had begun: %q", err)
	}
	if rooms := l.viewOf(dee.Token).Rooms; !slices.ContainsFunc(rooms, func(r roomItem) bool {
		return r.Code == code && r.State == "playing"
	}) {
		t.Errorf("the rooms Dee sees: %+v", rooms)
	}

	// Bob drops, and rejoins, to his character; Ann quits the game, and may as much; Cy leaves the room, and his
	// character with him: the server tells the game to let it go
	do(l, bob.Token, "ended", "why", "dropped", "text", "Server connection timed out.")
	if room := l.viewOf(bob.Token).Room; room.State != "playing" || !room.Seat || room.Join || room.Left {
		t.Errorf("Bob's room after he dropped: %+v", room)
	}
	do(l, bob.Token, "rejoin")
	if room := l.viewOf(bob.Token).Room; !room.Join {
		t.Errorf("Bob's room after he rejoined: %+v", room)
	}
	do(l, ann.Token, "ended", "why", "left")
	if room := l.viewOf(ann.Token).Room; room.State != "playing" || room.Join || room.Left {
		t.Errorf("Ann's room after she quit the game: %+v", room)
	}
	if err := do(l, ann.Token, "rejoin"); err != "" {
		t.Errorf("Ann, rejoining after she quit the game: %q", err)
	}
	do(l, cy.Token, "leave")
	gameGot(t, l, "bo1_lobby_drop "+cyAt)
	// the game lets Bob's character go (he was away for too long, say): he can't take it back
	do(l, bob.Token, "ended", "why", "dropped")
	l.command(code + " print bo1_lobby: out " + bobAt)
	waitView(t, l, bob.Token, "Bob's character gone", func(v view) bool { return v.Room.Left })
	if err := do(l, bob.Token, "rejoin"); !strings.Contains(err, "You're out of this game") {
		t.Errorf("Bob rejoined after the game let his character go: %q", err)
	}
	if room := l.viewOf(ann.Token).Room; room.State != "playing" {
		t.Errorf("Ann's game ended, with Ann in it: %+v", room)
	}

	// game over: the game says its players' lines of the scoreboard (the fewest points first, here), each with the
	// doors they bought, and on what round, and quits; as it does, it takes no one back (Ann rejoined it), and then the
	// room takes players again, saying how the game went
	l.command(code + " print bo1_lobby: stats " + bobAt + " 2140 380 31 9 2 1 0")
	l.command(code + " print bo1_lobby: stats " + annAt + " 3270 1150 40 12 1 2 3")
	l.command(code + " print bo1_lobby: over 3")
	room = waitView(t, l, ann.Token, "the game ending", func(v view) bool { return v.Room.State == "ending" }).Room
	doors := func(n int) *int { return &n }
	want := gameResult{Map: "zombie_theater", Round: 3, Players: []playerStats{
		{ID: l.viewOf(ann.Token).You.ID, Name: "Ann", Score: 3270, Cash: 1150, Kills: 40, Headshots: 12, Downs: 1,
			Revives: 2, Doors: doors(3)},
		// (under his name now, as the page shows every player: the game knew him as Bob)
		{ID: l.viewOf(bob.Token).You.ID, Name: "Robert", Score: 2140, Cash: 380, Kills: 31, Headshots: 9, Downs: 2,
			Revives: 1, Doors: doors(0)}}}
	sameResult := func(got *gameResult) bool {
		return got != nil && got.ID != "" && got.Map == want.Map && got.Round == want.Round && got.Mods == nil &&
			reflect.DeepEqual(got.Players, want.Players)
	}
	if room.Note != "" || !sameResult(room.Result) || room.Seat || room.Join {
		said, _ := json.Marshal(room.Result)
		t.Errorf("as the game ends: %+v, %s", room, said)
	}
	// the page is told of a player who bought no door, as it is not of one whose game counted none
	if said, _ := json.Marshal(room.Result); !strings.Contains(string(said), `"revives":1,"doors":0`) {
		t.Errorf("the report, of Bob, who bought no door: %s", said)
	}
	if err := do(l, ann.Token, "rejoin"); err != "The game has ended." {
		t.Errorf("Ann, rejoining a game that is ending: %q", err)
	}
	l.command(code + " quit")
	room = waitView(t, l, ann.Token, "the room open again", func(v view) bool { return v.Room.State == "open" }).Room
	if room.Note != "" || !sameResult(room.Result) || room.Seat {
		t.Errorf("after game over: %+v, %+v", room, room.Result)
	}
	if again := l.hello(bob.Token, "Robert"); again.Name != "Robert" || l.playsAs(bob.Token) != "Robert" {
		t.Errorf("Bob, once the game was over: %+v, playing as %q", again, l.playsAs(bob.Token))
	}
	if err := do(l, dee.Token, "join", "room", code); err != "" {
		t.Errorf("Dee, once the game was over: %q", err)
	}
	startGame(t, l, ann.Token, bob.Token, dee.Token)
	if room := l.viewOf(ann.Token).Room; room.Result != nil {
		t.Errorf("the last game's result, once the next has started: %+v", room.Result)
	}
}

func TestRoomsReady(t *testing.T) {
	l := testLobby(t)
	ann, bob, cy := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy")
	pagesOpen(l, ann.Token, bob.Token, cy.Token)
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	haveMap(l, ann.Token, bob.Token)
	state := func() string { return l.viewOf(ann.Token).Room.State }
	// the room's game starts once everyone is ready, the host as much as anyone
	do(l, bob.Token, "ready", "ready", "true")
	if v := l.viewOf(ann.Token); v.Room.State != "open" || v.You.Ready || !v.Room.Members[1].Ready {
		t.Errorf("Bob ready, and Ann not: %+v, %+v", v.You, v.Room)
	}
	// a page closed a while is ready no more, nor once it is open again
	l.mu.Lock()
	l.playerOf(bob.Token).streams = 0
	l.playerOf(bob.Token).gone = time.Now().Add(-clientAsks)
	l.mu.Unlock()
	waitFor(t, "Bob ready no more", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return !l.playerOf(bob.Token).ready
	})
	pagesOpen(l, bob.Token)
	if v := l.viewOf(ann.Token); v.Room.Members[1].Ready {
		t.Errorf("Bob, back from a page closed a while, is ready: %+v", v.Room.Members)
	}
	// a player joining or leaving has everyone say again that they are ready: no one leaving starts the game
	do(l, ann.Token, "ready", "ready", "true")
	do(l, cy.Token, "join", "room", code)
	if v := l.viewOf(ann.Token); v.You.Ready {
		t.Errorf("Ann, still ready as Cy joined: %+v", v.Room.Members)
	}
	haveMap(l, cy.Token)
	do(l, ann.Token, "ready", "ready", "true")
	do(l, cy.Token, "ready", "ready", "true")
	do(l, cy.Token, "leave")
	if v := l.viewOf(ann.Token); v.Room.State != "open" || v.You.Ready {
		t.Errorf("as Cy left, with Ann ready and Bob not: %+v, %+v", v.You, v.Room)
	}
	// everyone ready: the game starts after a countdown, which anyone no longer ready calls off
	l.mu.Lock()
	l.countdown = time.Second
	l.mu.Unlock()
	do(l, ann.Token, "ready", "ready", "true")
	do(l, bob.Token, "ready", "ready", "true")
	if v := l.viewOf(ann.Token); v.Room.State != "open" || v.Room.StartsIn <= 0 || v.Room.StartsIn > 1000 {
		t.Errorf("the countdown, everyone being ready: %+v", v.Room)
	}
	do(l, bob.Token, "ready", "ready", "false")
	if v := l.viewOf(ann.Token); v.Room.StartsIn != 0 {
		t.Errorf("the countdown, Bob unready: %+v", v.Room)
	}
	time.Sleep(1500 * time.Millisecond)
	if state() != "open" {
		t.Errorf("the game, its countdown called off: %q", state())
	}
	// the host picking another map has everyone say again that they are ready, and calls the countdown off; the room
	// going private, or the host picking the map it has, doesn't
	do(l, bob.Token, "ready", "ready", "true")
	do(l, ann.Token, "room", "private", "true")
	do(l, ann.Token, "room", "map", "zombie_theater")
	if v := l.viewOf(ann.Token); !v.You.Ready || !v.Room.Members[1].Ready || v.Room.StartsIn == 0 {
		t.Errorf("the countdown, the room private and its map the same: %+v", v.Room)
	}
	do(l, ann.Token, "room", "map", "zombie_pentagon")
	if v := l.viewOf(ann.Token); v.You.Ready || v.Room.Members[1].Ready || v.Room.StartsIn != 0 {
		t.Errorf("the countdown, another map picked: %+v", v.Room)
	}
	haveMap(l, ann.Token, bob.Token)
	time.Sleep(1500 * time.Millisecond)
	if state() != "open" {
		t.Errorf("the game, its countdown called off by another map: %q", state())
	}
	do(l, ann.Token, "ready", "ready", "true")
	do(l, bob.Token, "ready", "ready", "true")
	waitView(t, l, ann.Token, "the game, after the countdown", func(v view) bool { return v.Room.State == "loading" })
	if err := do(l, bob.Token, "ready", "ready", "false"); err != "The game is in progress." {
		t.Errorf("ready as the game ran: %q", err)
	}
	// for the room's next game, they say so again
	waitRunning(t, l, ann.Token)
	l.command(code + " quit")
	v := waitView(t, l, ann.Token, "the room open again", func(v view) bool { return v.Room.State == "open" })
	if v.You.Ready || v.Room.Members[1].Ready {
		t.Errorf("ready after the game: %+v", v.Room.Members)
	}
}

// A player in a browser is ready only once their page has the room's map: the room shows the others who lacks it, and
// how far their download is. One on Windows has the install.
func TestRoomsReadyWithTheMap(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	pagesOpen(l, ann.Token, bob.Token)
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	member := func(token string, i int) memberView { return l.viewOf(token).Room.Members[i] }
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "You don't have the room's map yet." {
		t.Errorf("ready, the page having said nothing of the map: %q", err)
	}
	if m := member(bob.Token, 0); !m.Missing || m.Downloading || m.Got != 0 {
		t.Errorf("Ann, her page having said nothing: %+v", m)
	}
	// downloading: that it does, from its start, and how far of what this download needs, for the others to see;
	// another map's files are not the room's
	do(l, ann.Token, "files", "map", "zombie_theater", "running", "true", "need", "240")
	if m := member(bob.Token, 0); !m.Missing || !m.Downloading || m.Got != 0 {
		t.Errorf("Ann, her download begun: %+v", m)
	}
	do(l, ann.Token, "files", "map", "zombie_theater", "running", "true", "got", "60", "need", "240")
	if m := member(bob.Token, 0); !m.Missing || !m.Downloading || m.Got != 0.25 {
		t.Errorf("Ann, a quarter downloaded: %+v", m)
	}
	do(l, ann.Token, "files", "map", "zombie_pentagon", "running", "true", "got", "60", "need", "240")
	if m := member(bob.Token, 0); !m.Missing || m.Downloading || m.Got != 0 {
		t.Errorf("Ann, downloading another map: %+v", m)
	}
	do(l, ann.Token, "files", "map", "zombie_pentagon", "done", "true")
	if err := do(l, ann.Token, "ready", "ready", "true"); err == "" {
		t.Errorf("ready with another map's files")
	}
	do(l, ann.Token, "files", "map", "zombie_theater", "done", "true")
	if m := member(bob.Token, 0); m.Missing || m.Downloading || m.Got != 0 {
		t.Errorf("Ann, with all of the map: %+v", m)
	}
	if err := do(l, ann.Token, "ready", "ready", "true"); err != "" {
		t.Errorf("ready with the map: %q", err)
	}
	// the page removed the files: ready no more
	do(l, ann.Token, "files", "map", "zombie_theater", "need", "240")
	if v := l.viewOf(ann.Token); v.You.Ready || !v.Room.Members[0].Missing {
		t.Errorf("Ann, her files removed: %+v", v.Room.Members[0])
	}
	// the host picking another map: everyone lacks it until their page says
	do(l, ann.Token, "files", "map", "zombie_theater", "done", "true")
	do(l, ann.Token, "room", "map", "zombie_pentagon")
	if m := member(bob.Token, 0); !m.Missing {
		t.Errorf("Ann, with the map before: %+v", m)
	}
	// a player on Windows has the install
	l.mu.Lock()
	l.playerOf(bob.Token).client = time.Now()
	l.mu.Unlock()
	if err := do(l, bob.Token, "ready", "ready", "true"); err != "" {
		t.Errorf("Bob, ready on Windows: %q", err)
	}
	waitView(t, l, ann.Token, "Bob on Windows, missing nothing", func(v view) bool {
		return v.Room.Members[1].Windows && !v.Room.Members[1].Missing
	})
}

// A room shows on a player that their page reads a gamepad, as the page says (POST /lobby/gamepad): until it says it
// reads none, or no page of theirs is open, the gamepad having been that page's to read.
func TestRoomsGamepad(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	gamepads := func() [2]bool { // Ann's and Bob's, as Bob's page shows them
		m := l.viewOf(bob.Token).Room.Members
		return [2]bool{m[0].Gamepad, m[1].Gamepad}
	}
	if got := gamepads(); got != [2]bool{} {
		t.Errorf("before a page said anything: %v", got)
	}
	do(l, ann.Token, "gamepad", "gamepad", "true")
	if got := gamepads(); got != [2]bool{true, false} {
		t.Errorf("Ann's page reads a gamepad: %v", got)
	}
	do(l, ann.Token, "gamepad", "gamepad", "false")
	if got := gamepads(); got != [2]bool{} {
		t.Errorf("Ann's page reads none any more: %v", got)
	}
	// her page closes, which read one: it goes with the page, and Bob's stays
	do(l, ann.Token, "gamepad", "gamepad", "true")
	do(l, bob.Token, "gamepad", "gamepad", "true")
	openPage(t, l, ann.Token)
	if got := gamepads(); got != [2]bool{false, true} {
		t.Errorf("Ann's page closed: %v", got)
	}
}

func TestRoomsServerFull(t *testing.T) {
	l := testLobby(t) // one game at once
	ann, bob, cy := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy")
	pagesOpen(l, ann.Token, bob.Token, cy.Token)
	do(l, bob.Token, "create")
	do(l, cy.Token, "join", "room", l.viewOf(bob.Token).Room.Code)
	haveMap(l, bob.Token, cy.Token)
	do(l, bob.Token, "ready", "ready", "true")
	// Ann's game is the server's last: Bob is ready no more, nor may anyone be
	do(l, ann.Token, "create")
	startGame(t, l, ann.Token)
	if v := l.viewOf(bob.Token); !v.Full || v.You.Ready || v.Room.Members[0].Ready {
		t.Errorf("Bob, as the server's last game started: %+v, %+v", v, v.Room)
	}
	if err := do(l, cy.Token, "ready", "ready", "true"); !strings.HasPrefix(err, "The server is full") {
		t.Errorf("Cy, ready as the server's games are all running: %q", err)
	}
	// once it ends, they may be again, and the room's game starts once they are
	waitRunning(t, l, ann.Token)
	l.command("quit")
	waitView(t, l, bob.Token, "a game free", func(v view) bool { return !v.Full })
	if room := l.viewOf(bob.Token).Room; room.State != "open" || room.StartsIn != 0 {
		t.Errorf("Bob's room, a game free, before anyone in it is ready again: %+v", room)
	}
	startGame(t, l, bob.Token, cy.Token)
}

func TestRoomsGameCalledOff(t *testing.T) {
	for _, tc := range []struct {
		name, note string
		how        func(l *lobby, ann, bob hello)
	}{
		{"a player fails to load", "Game called off: Bob's game failed to load. Out of memory.",
			func(l *lobby, ann, bob hello) {
				do(l, bob.Token, "ended", "why", "error", "text", "Out of memory.")
			}},
		{"a player leaves the room", "Game called off: Bob left.",
			func(l *lobby, ann, bob hello) {
				do(l, bob.Token, "leave")
			}},
		{"a player quits it on Windows", "Game called off: Bob left.",
			func(l *lobby, ann, bob hello) {
				l.command("print bo1_lobby: quit " + l.seatAddr(bob.Token))
			}},
		{"a player does not get in in time", "Game called off: Bob didn't join in time.",
			func(l *lobby, ann, bob hello) {
				l.command("print bo1_lobby: in " + l.seatAddr(ann.Token))
				l.mu.Lock()
				l.loadTimeout = time.Second
				l.mu.Unlock()
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := testLobby(t)
			ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
			do(l, ann.Token, "create")
			do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
			startGame(t, l, ann.Token, bob.Token)
			waitRunning(t, l, ann.Token)
			tc.how(l, ann, bob)
			room := waitView(t, l, ann.Token, "the room open again", func(v view) bool { return v.Room.State == "open" }).Room
			if room.Note != tc.note {
				t.Errorf("note: %q, want %q", room.Note, tc.note)
			}
		})
	}
}

// A game everyone is in by loadTimeout, round 1 yet to begin (it waits for those still watching their loading movie,
// which ends), is not called off.
func TestRoomsGameAllInWaitsForRound1(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	l.command("print bo1_lobby: in " + l.seatAddr(ann.Token))
	l.command("print bo1_lobby: in " + l.seatAddr(bob.Token))
	waitView(t, l, ann.Token, "both in the game", func(v view) bool {
		return len(v.Room.Members) == 2 && v.Room.Members[0].InGame && v.Room.Members[1].InGame
	})
	l.mu.Lock()
	l.loadTimeout = time.Second
	l.mu.Unlock()
	time.Sleep(time.Second + 3*l.sweepEvery)
	if room := l.viewOf(ann.Token).Room; room.State != "loading" {
		t.Fatalf("a game everyone is in, round 1 yet to begin: %s (%q)", room.State, room.Note)
	}
}

func TestRoomsGameEnd(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	l.command("print bo1_lobby: start")
	waitView(t, l, ann.Token, "round 1", func(v view) bool { return v.Room.State == "playing" })
	// their pages are open, and join the game: they are on their way into it, as a page that reloads is, and no one may
	// end it
	time.Sleep(3 * l.sweepEvery)
	if room := l.viewOf(ann.Token).Room; room.CanEnd {
		t.Errorf("a game its players' pages join may be ended: %+v", room)
	}
	if err := do(l, ann.Token, "end"); err != "Someone is playing the game." {
		t.Errorf("Ann, ending a game its players' pages join: %q", err)
	}
	// Ann quits it, and Bob drops, his page open in the room: no one plays it, and it runs on, for them to come back to
	do(l, ann.Token, "ended", "why", "left")
	do(l, bob.Token, "ended", "why", "dropped")
	waitView(t, l, ann.Token, "a game that may be ended", func(v view) bool { return v.Room.CanEnd })
	time.Sleep(3 * l.sweepEvery)
	if room := l.viewOf(ann.Token).Room; room.State != "playing" || !room.CanEnd {
		t.Fatalf("a game no one plays: %+v", room)
	}
	// Bob rejoins: he is on his way into it, and no one may end it; then he drops again, and Ann ends it
	do(l, bob.Token, "rejoin")
	if room := l.viewOf(ann.Token).Room; room.CanEnd {
		t.Errorf("a game Bob rejoins may be ended: %+v", room)
	}
	do(l, bob.Token, "ended", "why", "dropped")
	// only the host ends it: Ann
	if room := l.viewOf(bob.Token).Room; room.CanEnd {
		t.Errorf("Bob, who is not the host, may end the game: %+v", room)
	}
	if err := do(l, bob.Token, "end"); err != "Only the host ends the game." {
		t.Errorf("Bob, ending the game: %q", err)
	}
	if err := do(l, ann.Token, "end"); err != "" {
		t.Errorf("Ann, ending a game no one plays: %q", err)
	}
	room := waitView(t, l, ann.Token, "the room open again", func(v view) bool { return v.Room.State == "open" }).Room
	if room.Note != "Ann ended the game." {
		t.Errorf("note: %q", room.Note)
	}
}

// A room's game says what it keeps of its players as it changes (their stats, their collectibles), and the server gives
// it to their next game as it starts, in a config its command line execs.
func TestRoomsKeepPlayersStats(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	annAt, bobAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	collectibles := "0000000000000000000000000000000000000000001100000"
	for _, line := range []string{
		"dstat " + annAt + " zombie_kills 40",
		"dstat " + annAt + " zombie_coast_highestwave 103",
		"dstat " + annAt + " zombie_kills 52", // changed again
		"collectibles " + annAt + " " + collectibles,
		"dstat " + bobAt + " zombie_downs 2",
		"dstat " + bobAt + " zombie;quit 1",     // no stat's name
		"dstat " + bobAt + " zombie_gibs lots",  // nor value
		"collectibles " + bobAt + " 01x",        // nor collectibles
		"dstat 127.9.9.9:28960 zombie_kills 99", // no one's seat
	} {
		l.command(code + " print bo1_lobby: " + line)
	}
	kept := func(token string) (map[string]int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		p := l.playerOf(token)
		return maps.Clone(p.stats), p.collectibles
	}
	waitFor(t, "Bob's stats", func() bool { stats, _ := kept(bob.Token); return len(stats) > 0 })
	if stats, set := kept(ann.Token); !maps.Equal(stats, map[string]int{"zombie_kills": 52,
		"zombie_coast_highestwave": 103}) || set != collectibles {
		t.Errorf("what the server keeps of Ann: %v, %q", stats, set)
	}
	if stats, set := kept(bob.Token); !maps.Equal(stats, map[string]int{"zombie_downs": 2}) || set != "" {
		t.Errorf("what the server keeps of Bob: %v, %q", stats, set)
	}

	l.command(code + " print bo1_lobby: over 3")
	l.command(code + " quit")
	waitView(t, l, ann.Token, "the room open again", func(v view) bool { return v.Room.State == "open" })
	startGame(t, l, ann.Token, bob.Token)
	annAt, bobAt = l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	cfg := filepath.Join(l.t5().newGame("", "", 0, 0, nil).home, "main", "lobby.cfg")
	var b []byte
	// the first game's is there until the next game's takes its place
	waitFor(t, "the next game's lobby.cfg", func() bool { b, _ = os.ReadFile(cfg); return strings.Contains(string(b), annAt) })
	if want := "bo1_lobby_seat " + annAt + " 0\n" +
		"bo1_lobby_seat " + bobAt + " 1\n" +
		"bo1_lobby_collectibles " + annAt + " " + collectibles + "\n" +
		"bo1_lobby_dstat " + annAt + " zombie_coast_highestwave 103\n" +
		"bo1_lobby_dstat " + annAt + " zombie_kills 52\n" +
		"bo1_lobby_dstat " + bobAt + " zombie_downs 2\n"; string(b) != want {
		t.Errorf("the next game's lobby.cfg:\n%s\nwant:\n%s", b, want)
	}
	waitFor(t, "the next game to exec it", func() bool {
		logs, _ := filepath.Glob(filepath.Join(l.t5().newGame("", "", 0, 0, nil).logDir, "game-*.log"))
		for _, f := range logs {
			if b, _ := os.ReadFile(f); strings.Contains(string(b), "+exec lobby.cfg +set bo1_players") {
				return true
			}
		}
		return false
	})
}

// The server tells a room's game each player's place in the room as it starts, in the config its command line execs,
// and the game gives each the client slot of their place (bo1-zombies/src/server_mp/sv_lobby_mp.cpp), which the level gives spawn
// points and characters by: the host's place is the first, then the others' in the order they joined. Ann made the
// room and left it, which made Bob its host, first; Dee joined after Cee.
func TestRoomsPlaceHostFirst(t *testing.T) {
	l := testLobby(t)
	ann, bob, cee, dee := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cee"), l.hello("", "Dee")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	do(l, cee.Token, "join", "room", code)
	do(l, ann.Token, "leave")
	do(l, dee.Token, "join", "room", code)
	startGame(t, l, dee.Token, cee.Token, bob.Token)
	cfg := filepath.Join(l.t5().newGame("", "", 0, 0, nil).home, "main", "lobby.cfg")
	waitFor(t, "the game's lobby.cfg", func() bool { _, err := os.Stat(cfg); return err == nil })
	b, _ := os.ReadFile(cfg)
	if want := "bo1_lobby_seat " + l.seatAddr(bob.Token) + " 0\n" +
		"bo1_lobby_seat " + l.seatAddr(cee.Token) + " 1\n" +
		"bo1_lobby_seat " + l.seatAddr(dee.Token) + " 2\n"; string(b) != want {
		t.Errorf("the game's lobby.cfg:\n%s\nwant:\n%s", b, want)
	}
}

// The room tells the page each member's place: where they are among its members, the host first, and while its game
// runs, the place of their seat in it, whose client slot the game gave them. It stays theirs as another leaves the
// room, until the game has ended.
func TestRoomsPlaceShown(t *testing.T) {
	l := testLobby(t)
	ann, bob, cee, dee := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cee"), l.hello("", "Dee")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	do(l, cee.Token, "join", "room", code)
	places := func() map[string]int {
		at := map[string]int{}
		for _, m := range l.viewOf(bob.Token).Room.Members {
			at[m.Name] = m.Place
		}
		return at
	}
	if got := places(); !maps.Equal(got, map[string]int{"Ann": 0, "Bob": 1, "Cee": 2}) {
		t.Errorf("the places in a room of three: %v", got)
	}
	// the host leaves, and the others move up
	do(l, ann.Token, "leave")
	do(l, dee.Token, "join", "room", code)
	if got := places(); !maps.Equal(got, map[string]int{"Bob": 0, "Cee": 1, "Dee": 2}) {
		t.Errorf("the places, the host gone and Dee in: %v", got)
	}
	// the host's is 0, not left out; the player themself has none but as a member of their room
	v := l.viewOf(bob.Token)
	member, _ := json.Marshal(v.Room.Members[0])
	you, _ := json.Marshal(v.You)
	if !strings.Contains(string(member), `"place":0`) || strings.Contains(string(you), "place") {
		t.Errorf("the host's place, as the page reads it: %s; and theirs as themself: %s", member, you)
	}

	startGame(t, l, bob.Token, cee.Token, dee.Token)
	waitRunning(t, l, bob.Token)
	l.command("print bo1_lobby: start")
	waitView(t, l, bob.Token, "round 1", func(v view) bool { return v.Room.State == "playing" })
	if got := places(); !maps.Equal(got, map[string]int{"Bob": 0, "Cee": 1, "Dee": 2}) {
		t.Errorf("the places in the room's game: %v", got)
	}
	// Cee leaves the room, and its game with it, which runs on: Dee's place is her seat's still
	do(l, cee.Token, "leave")
	if v := l.viewOf(bob.Token); v.Room.State != "playing" || !maps.Equal(places(), map[string]int{"Bob": 0, "Dee": 2}) {
		t.Errorf("the places, Cee having left the room as its game ran (%s): %v", v.Room.State, places())
	}
	// once the game has ended, it is her place among the room's members, which the next game gives her
	l.command(code + " quit")
	waitView(t, l, bob.Token, "the room open again", func(v view) bool { return v.Room.State == "open" })
	if got := places(); !maps.Equal(got, map[string]int{"Bob": 0, "Dee": 1}) {
		t.Errorf("the places, the game over: %v", got)
	}
}

func TestRoomsGameEndsWhenNoOneIsLeft(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	annAt, bobAt := l.seatAddr(ann.Token), l.seatAddr(bob.Token)
	l.command("print bo1_lobby: in " + annAt)
	l.command("print bo1_lobby: in " + bobAt)
	l.command("print bo1_lobby: start")
	waitView(t, l, ann.Token, "round 1", func(v view) bool { return v.Room.State == "playing" })
	// both drop; the game lets Ann's character go, after keepCharacter: Bob may still come back to his
	do(l, ann.Token, "ended", "why", "dropped")
	do(l, bob.Token, "ended", "why", "dropped")
	l.command("print bo1_lobby: out " + annAt)
	room := waitView(t, l, bob.Token, "Ann out", func(v view) bool { return !v.Room.Members[0].InGame }).Room
	if room.State != "playing" {
		t.Fatalf("a game Bob may come back to ended: %+v", room)
	}
	// and then his
	l.command("print bo1_lobby: out " + bobAt)
	room = waitView(t, l, bob.Token, "the room open again", func(v view) bool { return v.Room.State == "open" }).Room
	if room.Note != "Game ended: no one was playing." {
		t.Errorf("note: %q", room.Note)
	}
}

func TestRoomsGamePortsWaitForTheirGamesToQuit(t *testing.T) {
	l := testLobby(t) // one game at once
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	startGame(t, l, ann.Token)
	waitRunning(t, l, ann.Token)
	do(l, bob.Token, "create")
	pagesOpen(l, bob.Token)
	// everyone leaves Ann's room: it is gone at once, and its game quits, holding its port until it has
	l.mu.Lock()
	l.leave(l.playerOf(ann.Token))
	err := l.start(l.playerOf(bob.Token).room)
	l.mu.Unlock()
	if err == nil || !strings.HasPrefix(err.Error(), "The server is full") {
		t.Errorf("a game on the port of one still quitting: %v", err)
	}
	// once it has quit, Bob may be ready for his room's
	waitView(t, l, bob.Token, "Ann's room's game quit", func(v view) bool { return !v.Full })
	startGame(t, l, bob.Token)
}

func TestRoomsWindowsPlayers(t *testing.T) {
	l := testLobby(t)
	ann, win := l.hello("", "Ann"), l.hello("", "Win")
	waits := func(key, want string) {
		t.Helper()
		_, _, _, _, err := l.windowsSeat(key)
		if _, ok := errors.AsType[waiting](err); !ok || err.Error() != want {
			t.Errorf("key %q: %v, want to wait: %q", key, err, want)
		}
	}
	for key, want := range map[string]string{"": "needs your key", "0123abcd": "no longer works"} {
		if _, _, _, _, err := l.windowsSeat(key); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("key %q: %v", key, err)
		}
	}
	// the player's client waits: for them to join a room, then for its host to start its game
	waits(win.Key, "Waiting for you to join a room on the server's page...")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, win.Token, "join", "room", code)
	waits(win.Key, "Waiting for the game to start...")
	// as it asks, its player plays on Windows (their page is to stay out of the game), and is not away, though no page
	// of theirs is open
	l.mu.Lock()
	l.playerOf(win.Token).gone = time.Now().Add(-leaveAfter)
	l.mu.Unlock()
	if v := l.viewOf(ann.Token); v.Room.Members[1].Away || !v.Room.Members[1].Windows || v.Room.Members[1].Ready ||
		v.You.Windows {
		t.Errorf("Win, whose client asks for the game, as Ann sees them: %+v", v.Room.Members)
	}
	if v := l.viewOf(win.Token); !v.You.Windows {
		t.Errorf("Win, whose client asks for the game, as they see themselves: %+v", v.You)
	}
	time.Sleep(3 * l.sweepEvery)
	if room := l.viewOf(ann.Token).Room; len(room.Members) != 2 {
		t.Errorf("Win left the room as their client waited: %+v", room.Members)
	}
	// they say they are ready, as everyone does (their page closed: their client keeps them in); then their client
	// stops asking, and they are on Windows no more, and unready: the room hears so, with no word from anyone
	do(l, win.Token, "ready", "ready", "true")
	if v := l.viewOf(ann.Token); !v.Room.Members[1].Ready {
		t.Errorf("Win, ready on Windows: %+v", v.Room.Members)
	}
	l.mu.Lock()
	l.playerOf(win.Token).gone = time.Now()
	l.playerOf(win.Token).client = time.Now().Add(-clientAsks)
	changed := l.changed
	l.mu.Unlock()
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Error("no event as Win's client stopped asking")
	}
	if v := l.viewOf(win.Token); v.You.Windows || v.You.Ready {
		t.Errorf("Win, whose client stopped asking: %+v", v.You)
	}
	waits(win.Key, "Waiting for the game to start...")
	do(l, win.Token, "ready", "ready", "true")

	// the game starts, everyone being ready: Win's client to their seat, from its address, as a session from their page
	// would be
	startGame(t, l, ann.Token)
	game, local, name, who, err := l.windowsSeat(win.Key)
	if err != nil || local.String() != l.seatAddr(win.Token) || name != "Win" || who != "Win, room "+code {
		t.Fatalf("Win's seat: %v, %v, %q, %q, %v", game, local, name, who, err)
	}
	if sessionGame, sessionAt, _, _, err := l.session(win.Token); err != nil || sessionAt != local || sessionGame.Port != game.Port {
		t.Errorf("Win's session: %v, %v, %v; the route's: %v, %v", sessionGame, sessionAt, err, game, local)
	}
	// their Windows game closes, or crashes: their page, should it be open, offers to rejoin rather than join by itself
	l.mu.Lock()
	l.playerOf(win.Token).client = time.Now().Add(-clientAsks)
	l.mu.Unlock()
	if v := waitView(t, l, win.Token, "Win no longer on Windows", func(v view) bool { return !v.You.Windows }); v.Room.Join {
		t.Errorf("Win's page, their Windows game gone, is to join by itself: %+v", v.Room)
	}
	// once round 1 has begun, the game lets their character go (they were away for too long): they can't come back
	waitRunning(t, l, ann.Token)
	l.command("print bo1_lobby: in " + local.String())
	l.command("print bo1_lobby: in " + l.seatAddr(ann.Token))
	l.command("print bo1_lobby: start")
	l.command("print bo1_lobby: out " + local.String())
	waitView(t, l, win.Token, "Win's character gone", func(v view) bool { return v.Room.Left })
	if _, _, _, _, err := l.windowsSeat(win.Key); err == nil || err.Error() != "Your character in room "+code+"'s game is gone. You can join the next one." {
		t.Errorf("Win, whose character is gone: %v", err)
	}
}

func TestRoomsLeavingTakesTheCharacter(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	startGame(t, l, ann.Token, bob.Token)
	waitRunning(t, l, ann.Token)
	bobAt := l.seatAddr(bob.Token)
	l.command("print bo1_lobby: start")
	waitView(t, l, ann.Token, "round 1", func(v view) bool { return v.Room.State == "playing" })
	// Bob drops, and leaves the room: the character the game keeps for him goes too
	do(l, bob.Token, "ended", "why", "dropped")
	do(l, bob.Token, "leave")
	gameGot(t, l, "bo1_lobby_drop "+bobAt)
	if room := l.viewOf(ann.Token).Room; room.State != "playing" {
		t.Errorf("Ann's game ended as Bob left the room: %+v", room)
	}
}

func TestRoomsEvents(t *testing.T) {
	l := testLobby(t)
	srv := httptest.NewServer(&web{lobby: l})
	defer srv.Close()
	// as the player with that token (its Authorization header), what the server answers a call
	call := func(method, path, token, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return rsp
	}
	hi := func(token, name string) (h hello) {
		t.Helper()
		rsp := call(http.MethodPost, "/lobby/hello", token, `{"name":"`+name+`"}`)
		defer rsp.Body.Close()
		json.NewDecoder(rsp.Body).Decode(&h)
		return h
	}
	// a new player, and the same one again, by their token
	ann := hi("", "Ann")
	if again := hi(ann.Token, "Anna"); again.Token != ann.Token || again.Key != ann.Key || again.Name != "Anna" {
		t.Errorf("Ann, saying hello again: %+v, and before: %+v", again, ann)
	}
	// the token is the header's: not the URL's, which a proxy would log
	if rsp := call(http.MethodGet, "/lobby/events?token="+ann.Token, "", ""); rsp.StatusCode != http.StatusUnauthorized ||
		rsp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("events for a token in the URL: %s, %v", rsp.Status, rsp.Header)
	}
	rsp := call(http.MethodGet, "/lobby/events", ann.Token, "")
	defer rsp.Body.Close()
	if rsp.Header.Get("Content-Type") != "text/event-stream" || rsp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("headers: %v", rsp.Header)
	}
	views := make(chan view)
	go func() {
		for in := bufio.NewScanner(rsp.Body); in.Scan(); {
			if data, ok := strings.CutPrefix(in.Text(), "data: "); ok {
				var v view
				json.Unmarshal([]byte(data), &v)
				views <- v
			}
		}
	}()
	next := func() view {
		select {
		case v := <-views:
			return v
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
			return view{}
		}
	}
	// every Zombies map, those the server lacks too
	if v := next(); v.You.Name != "Anna" || v.Room != nil || len(v.Maps) != len(mapTitles) || v.DefaultMap != "zombie_theater" ||
		v.Maps[0] != (mapView{Name: "zombie_theater", Title: "Kino der Toten", Of: t5, Have: true}) || !v.Maps[1].Have || v.Maps[6].Have {
		t.Errorf("first event: %+v", v)
	}
	do(l, ann.Token, "create")
	if v := next(); v.Room == nil || v.Room.Members[0].Away {
		t.Errorf("after making a room: %+v", v.Room)
	}
	if rsp := call(http.MethodGet, "/lobby/events", "nobody", ""); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an unknown token's events: %s", rsp.Status)
	}
	if rsp := call(http.MethodPost, "/lobby/create", "", "{}"); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a call with no token: %s", rsp.Status)
	}
}

func TestRoomsAwayPlayersLeave(t *testing.T) {
	l := testLobby(t)
	ann, bob := l.hello("", "Ann"), l.hello("", "Bob")
	do(l, ann.Token, "create")
	do(l, bob.Token, "join", "room", l.viewOf(ann.Token).Room.Code)
	if room := l.viewOf(ann.Token).Room; !room.Members[1].Away {
		t.Errorf("Bob, with no page open, isn't away: %+v", room.Members)
	}
	l.mu.Lock()
	l.playerOf(bob.Token).gone = time.Now().Add(-leaveAfter)
	l.playerOf(ann.Token).streams = 1
	l.mu.Unlock()
	waitView(t, l, ann.Token, "Bob gone", func(v view) bool { return len(v.Room.Members) == 1 })
}

func TestRoomsForgetsPlayersNoPageOpened(t *testing.T) {
	l := testLobby(t)
	// a new player makes no event: no one sees them yet
	l.mu.Lock()
	changed := l.changed
	l.mu.Unlock()
	script, ann := l.hello("", "Script"), l.hello("", "Ann")
	select {
	case <-changed:
		t.Error("an event for new players")
	default:
	}
	// both away a minute, in no room: Ann's page opened its event stream, and she stays; the script's never did
	l.mu.Lock()
	l.playerOf(ann.Token).opened = true
	for _, token := range []string{script.Token, ann.Token} {
		l.playerOf(token).gone = time.Now().Add(-forgetUnopened)
	}
	l.mu.Unlock()
	waitFor(t, "the script's player forgotten", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.playerOf(script.Token) == nil && l.keys[script.Key] == nil
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.playerOf(ann.Token) == nil || l.keys[ann.Key] == nil {
		t.Error("Ann, whose page was open, was forgotten after a minute")
	}
}

// A page says how its wait for pipelines went, and the server logs it: one line, whatever the page sent, and no more
// than one every ten seconds from a player.
func TestWaited(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	l := testLobby(t)
	ann := l.hello("", "Ann")
	do(l, "no token of the server's", "waited", "text", "zombie_theater: waited 9.0 s")
	if strings.Contains(logged.String(), "waited for pipelines") {
		t.Fatalf("the server logged a wait of no player's:\n%s", logged.String())
	}
	do(l, ann.Token, "waited", "text", "  zombie_theater: waited 30.4 s\nserver: a line of the page's own\x00 ")
	said := "server: Ann waited for pipelines: zombie_theater: waited 30.4 s server: a line of the page's own\n"
	if !strings.HasSuffix(logged.String(), said) {
		t.Fatalf("the log does not end with %q:\n%s", said, logged.String())
	}
	if strings.Contains(logged.String(), "\nserver: a line of the page's own") {
		t.Errorf("the page's text began a line of the log:\n%s", logged.String())
	}
	// again at once: not logged; in a room, ten seconds on: logged with the room, and cut where it is long
	do(l, ann.Token, "waited", "text", "zombie_theater: waited 31.0 s")
	if strings.Contains(logged.String(), "31.0 s") {
		t.Errorf("the server logged a second wait of Ann's at once:\n%s", logged.String())
	}
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	l.mu.Lock()
	l.playerOf(ann.Token).waitedSaid = time.Now().Add(-11 * time.Second)
	l.mu.Unlock()
	do(l, ann.Token, "waited", "text", "zombie_theater: "+strings.Repeat("é", 400))
	want := "server: Ann in room " + code + " waited for pipelines: zombie_theater: " + strings.Repeat("é", 342) + "...\n"
	if !strings.HasSuffix(logged.String(), want) {
		t.Errorf("the log does not end with Ann's wait in her room, cut at 700 bytes:\n%s", logged.String())
	}
	do(l, ann.Token, "waited", "text", " \n ")
	if !strings.HasSuffix(logged.String(), want) {
		t.Errorf("the server logged a wait with nothing said:\n%s", logged.String())
	}
}

func TestRoomsConsole(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	l := testLobby(t)
	l.command("status")
	ann := l.hello("", "Ann")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	l.command("rooms")
	l.command(code + " status")
	for _, want := range []string{`server: no game is running; "status" wasn't sent`,
		"server: room " + code + ": zombie_theater, open (Ann)",
		"server: room " + code + ` has no game running; "status" wasn't sent`} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("the log lacks %q:\n%s", want, logged.String())
		}
	}
}
