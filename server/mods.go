package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// mod is a setting that a room's host can choose for the room's games (the page's Mods tab). A mod with options has
// one of their values, and it is off at the first, which is the game's own value. Any other mod is a switch: on or
// off. A mod is for the games of some titles (Titles). In a Black Ops game it is a dvar on the engine's command line
// (dvar, modArgs), and the engine implements it. In a Black Ops II game it is a dvar in the game's config, and the
// server's script implements it (t6.go: t6ModDvar, t6Script).
type mod struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Desc    string      `json:"desc"`
	Options []modOption `json:"options,omitempty"`
	Titles  []string    `json:"titles"` // the IDs of the titles whose games can have it
	dvar    string      // the engine's dvar of it, in a Black Ops game
}

// modOption is one of the values of a mod with options.
type modOption struct {
	Value int `json:"value"`
}

var modList = []mod{
	{ID: "uniquewallweapons", Name: "Unique Wall Weapons", Desc: "Only one player can hold each wall weapon at a time.",
		Titles: []string{t5, t6},
		dvar:   "bo1_mod_uniquewallweapons"}, // bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrUniqueWallWeapons
	// Black Ops II has Grief as a mode of its own, on that mode's maps (mapEntry.versus).
	{ID: "grief", Name: "Grief", Desc: "Two teams. Power-ups only help the team that takes them. " +
		"Most cash at game over wins.",
		Titles: []string{t5},
		dvar:   "bo1_mod_grief"}, // s_scrGrief; teamOf returns a player's team
	// Both engines have 32 actors at most, so the cap goes no higher.
	{ID: "zombiecap", Name: "Zombie Cap", Desc: "How many zombies can be alive at once.",
		Options: []modOption{{Value: 24}, {Value: 32}},
		Titles:  []string{t5, t6},
		dvar:    "bo1_mod_zombiecap"}, // s_scrZombieCap
}

// modsOf returns those of the mods in on, as modsNamed returns them, that the games of the title can have, and the
// name of the first that they can't have, or "".
func modsOf(title string, on []string) (kept []string, not string) {
	for _, name := range on {
		id, _, _ := strings.Cut(name, "=")
		m := modList[slices.IndexFunc(modList, func(m mod) bool { return m.ID == id })]
		if slices.Contains(m.Titles, title) {
			kept = append(kept, name)
		} else if not == "" {
			not = m.Name
		}
	}
	return kept, not
}

// modsNamed returns the mods a room has, from the mods named in names. A name is a mod's ID, with "=" and a value
// for a mod with options ("zombiecap=32"). The result has each mod once, in modList's order, as it was named last.
// A mod at its first option is off, and is left out. modsNamed returns an error for a name that is no mod's, and for
// a value that is not one of the mod's.
func modsNamed(names []string) ([]string, error) {
	values := map[string]string{} // the mods that are on, by ID: a mod's value, or "" for a switch
	for _, name := range names {
		id, value, valued := strings.Cut(name, "=")
		i := slices.IndexFunc(modList, func(m mod) bool { return m.ID == id })
		if i < 0 || valued != (modList[i].Options != nil) {
			return nil, fmt.Errorf("There is no mod %s.", name)
		}
		m := modList[i]
		if valued {
			o := slices.IndexFunc(m.Options, func(o modOption) bool { return strconv.Itoa(o.Value) == value })
			switch {
			case o < 0:
				return nil, fmt.Errorf("%s can't be %s.", m.Name, value)
			case o == 0:
				delete(values, id)
				continue
			}
		}
		values[id] = value
	}
	var on []string
	for _, m := range modList {
		if value, ok := values[m.ID]; ok && m.Options != nil {
			on = append(on, m.ID+"="+value)
		} else if ok {
			on = append(on, m.ID)
		}
	}
	return on, nil
}

// modArgs returns the engine's command-line arguments for the mods a Black Ops room has, as modsNamed returns them. A
// switch is set to 1, and a mod with options to its value.
func modArgs(on []string) []string {
	var args []string
	for _, m := range modList {
		for _, name := range on {
			if id, value, valued := strings.Cut(name, "="); id == m.ID {
				if !valued {
					value = "1"
				}
				args = append(args, "+set", m.dvar, value)
			}
		}
	}
	return args
}

// teamOf returns the team, 1 or 2, of the player at place in a game of so many players with Grief on. The first half
// of the players, rounded up, is team 1, and the rest is team 2. The game's scripts decide it the same way
// (bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrGrief's bo1_team). They go by a player's client slot, which is
// their place (placesOf), and by bo1_players.
func teamOf(place, players int) int {
	if place >= (players+1)/2 {
		return 2
	}
	return 1
}

// teams reports whether the room's players play in two teams: with Grief on (a T5 room's mod), or on a T6 map of
// Grief.
func (r *room) teams() bool {
	return slices.Contains(r.mods, "grief") || versusOf(r.mapName) == grief
}
