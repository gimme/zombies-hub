package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// mod is a setting that a room's host can choose for the room's games (the page's Mods tab). It is a dvar on the
// game's command line. A mod with options has one of their values, and it is off at the first, which is the game's
// own value. Any other mod is a switch: on or off.
type mod struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Desc    string      `json:"desc"`
	Options []modOption `json:"options,omitempty"`
	dvar    string
}

// modOption is one of the values of a mod with options. The page shows a value that is not available yet, but lets
// no one pick it.
type modOption struct {
	Value int  `json:"value"`
	Soon  bool `json:"soon,omitempty"` // not available yet: modsNamed refuses it
}

var modList = []mod{
	{ID: "uniquewallweapons", Name: "Unique Wall Weapons", Desc: "Only one player can hold each wall weapon at a time.",
		dvar: "bo1_mod_uniquewallweapons"}, // bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrUniqueWallWeapons
	{ID: "grief", Name: "Grief", Desc: "Two teams. Power-ups only help the team that takes them. " +
		"Most cash at game over wins.",
		dvar: "bo1_mod_grief"}, // s_scrGrief; teamOf returns a player's team
	// A cap above the engine's 32 needs the players' clients to know of it. The options above 32 are marked Soon.
	{ID: "zombiecap", Name: "Zombie Cap", Desc: "How many zombies can be alive at once.",
		Options: []modOption{{Value: 24}, {Value: 32}, {Value: 40, Soon: true}, {Value: 48, Soon: true}},
		dvar:    "bo1_mod_zombiecap"}, // s_scrZombieCap
}

// modsNamed returns the mods a room has, from the mods named in names. A name is a mod's ID, with "=" and a value
// for a mod with options ("zombiecap=32"). The result has each mod once, in modList's order, as it was named last.
// A mod at its first option is off, and is left out. modsNamed returns an error for a name that is no mod's, for a
// value that is not one of the mod's, and for a value that is not available yet.
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
			case m.Options[o].Soon:
				return nil, fmt.Errorf("%s can't be %s yet.", m.Name, value)
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

// modArgs returns the engine's command-line arguments for the mods a room has, as modsNamed returns them. A switch is
// set to 1, and a mod with options to its value.
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
