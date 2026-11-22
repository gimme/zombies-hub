package main

import "slices"

// title is a Call of Duty game that the server hosts: Black Ops (t5.go) or Black Ops II (t6.go). The rooms, the
// page's view and the runner (game.go) deal with a title only through this interface. Hosting another title then
// takes a new file, and no changes to those. The caller holds l.mu for every method.
type title interface {
	id() string    // its folder among the game files, and its id for the page: "t5"
	label() string // its name in messages to players: "Black Ops II"

	mods() bool   // its games take the room's mods
	inPage() bool // a player's page plays its games; otherwise the player's own PC does

	// free returns why no game of the title can start now, or nil when one can.
	free(l *lobby) error
	// begin makes r's game. rg has the game's seats.
	begin(l *lobby, r *room, rg *roomGame) *game
	// done is called once rg's game has quit.
	done(l *lobby, rg *roomGame)
	// drop removes the character of s from the game. The player of s left the room.
	drop(l *lobby, r *room, s *seat)
	// late is called at each sweep while rg waits for its players. It calls the game off when they take too long.
	late(l *lobby, r *room, rg *roomGame)
	// lacks reports whether p is missing what their room's map needs, and so can't be ready.
	lacks(l *lobby, p *player) bool
	// showMember and showRoom add what a room of this title shows. showMember adds it for a member m. showRoom adds
	// it for the player p, who views the room.
	showMember(l *lobby, r *room, m *player, mv *memberView)
	showRoom(l *lobby, r *room, p *player, rv *roomView)
}

// title returns the title of a map. For a map the server doesn't list, it returns the first title, Black Ops.
func (l *lobby) title(mapName string) title {
	of := titleOf(mapName)
	for _, t := range l.titles {
		if t.id() == of {
			return t
		}
	}
	return l.titles[0]
}

// titleOf returns the id of a map's title. It returns t5 for a map the server doesn't list.
func titleOf(mapName string) string {
	if m, ok := mapByName(mapName); ok {
		return m.of
	}
	return t5
}

// mapEntry is a Zombies map: the id of its title (of), its name in the game, its name on the page (title), and where
// its pictures and description are in the game files (mapart.go). Those are its picture in the game's map select
// (images/<preview>.iwi), its loading screen (images/<loadscreen>.iwi), and the localized string of its description.
type mapEntry struct{ of, name, title, preview, loadscreen, description string }

// mapTitles is the Zombies maps, in the order the page lists them.
var mapTitles = slices.Concat(t5MapList, t6MapList)

func mapByName(name string) (mapEntry, bool) {
	i := slices.IndexFunc(mapTitles, func(m mapEntry) bool { return m.name == name })
	if i < 0 {
		return mapEntry{}, false
	}
	return mapTitles[i], true
}
