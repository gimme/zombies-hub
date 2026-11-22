package main

import "testing"

func TestMapMarks(t *testing.T) {
	const pentagon, theater = "zombie_pentagon", "zombie_theater"
	l := testLobby(t)
	ann, bob, cy := l.hello("", "Ann"), l.hello("", "Bob"), l.hello("", "Cy")
	do(l, ann.Token, "create")
	code := l.viewOf(ann.Token).Room.Code
	do(l, bob.Token, "join", "room", code)
	do(l, cy.Token, "join", "room", code)
	marks := func() []string {
		var got []string
		for _, m := range l.viewOf(ann.Token).Room.Members {
			got = append(got, m.Mark)
		}
		return got
	}
	want := func(what string, expected ...string) {
		t.Helper()
		got := marks()
		if len(got) != len(expected) {
			t.Fatalf("%s: marks %q, want %q", what, got, expected)
		}
		for i := range got {
			if got[i] != expected[i] {
				t.Errorf("%s: marks %q, want %q", what, got, expected)
				return
			}
		}
	}

	if err := do(l, ann.Token, "mark", "map", pentagon); err != "The host picks the map." {
		t.Errorf("the host's mark: %q", err)
	}
	if err := do(l, bob.Token, "mark", "map", "mp_nuked"); err != "There is no map mp_nuked." {
		t.Errorf("a map that isn't: %q", err)
	}
	if err := do(l, bob.Token, "mark", "map", "zombie_moon"); err != "The server doesn't have Moon." {
		t.Errorf("a map the server lacks: %q", err)
	}
	want("after marks it can't have", "", "", "")

	// a mark, which the player can take off or move, changes nothing else: who is ready stays so
	pagesOpen(l, bob.Token)
	haveMap(l, bob.Token)
	do(l, bob.Token, "ready", "ready", "true")
	if err := do(l, bob.Token, "mark", "map", pentagon); err != "" {
		t.Fatal(err)
	}
	want("Bob's mark", "", pentagon, "")
	if v := l.viewOf(bob.Token); !v.You.Ready || v.Room.Map != theater {
		t.Errorf("the room after a mark: %+v", v.Room)
	}
	do(l, bob.Token, "mark", "map", "")
	want("Bob's mark taken off", "", "", "")

	// the host picking a map leaves the marks as they are, those on it too
	do(l, bob.Token, "mark", "map", pentagon)
	do(l, cy.Token, "mark", "map", pentagon)
	do(l, ann.Token, "room", "map", pentagon)
	want("the host picked the map they marked", "", pentagon, pentagon)
	if err := do(l, bob.Token, "mark", "map", ""); err != "" {
		t.Fatal(err)
	}
	want("Bob's mark taken off the room's map", "", "", pentagon)

	// a player who becomes the host has no mark, and one who leaves takes theirs along
	do(l, bob.Token, "mark", "map", theater)
	do(l, cy.Token, "mark", "map", theater)
	do(l, ann.Token, "leave")
	if got := l.viewOf(bob.Token).Room.Members; !got[0].Host || got[0].Mark != "" || got[1].Mark != theater {
		t.Errorf("Bob the host now, the marks: %+v", got)
	}
	do(l, cy.Token, "leave")
	if v := l.viewOf(cy.Token); v.Room != nil {
		t.Errorf("Cy left: %+v", v.Room)
	}
	do(l, cy.Token, "join", "room", code)
	if got := l.viewOf(bob.Token).Room.Members; got[1].Mark != "" {
		t.Errorf("Cy, back, kept a mark: %+v", got)
	}

	// the game starts: the marks go, and none can be made until it ends
	do(l, cy.Token, "mark", "map", theater)
	startGame(t, l, bob.Token, cy.Token)
	if got := l.viewOf(bob.Token).Room.Members; got[1].Mark != "" {
		t.Errorf("the game began, Cy's mark: %+v", got)
	}
	if err := do(l, cy.Token, "mark", "map", theater); err != "The map can't change while in game." {
		t.Errorf("a mark in game: %q", err)
	}
}
