package main

import "slices"

// title is a Call of Duty game that the server hosts: Black Ops (t5.go) or Black Ops II (t6.go). The rooms, the
// page's view and the runner (game.go) deal with a title only through this interface. Hosting another title then
// takes a new file, and no changes to those. The caller holds l.mu for every method.
//
// A room's title is its map's (lobby.title). The host picks the title, and the room goes to a map of it (titleMap),
// or picks a map, and the room's title follows. The page shows the titles apart: a room's maps, and the leaderboards
// (leaderboards.go), are of one title. A room's versus (co-op, Grief or Turned: mapEntry.versus) is its map's in the
// same way.
type title interface {
	id() string    // its folder among the game files, and its id for the page: "t5"
	label() string // its name in messages to players: "Black Ops II"

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
	if t := l.titleByID(titleOf(mapName)); t != nil {
		return t
	}
	return l.titles[0]
}

// titleByID returns the title whose id is id, or nil when there is none.
func (l *lobby) titleByID(id string) title {
	for _, t := range l.titles {
		if t.id() == id {
			return t
		}
	}
	return nil
}

// titleOf returns the id of a map's title. It returns t5 for a map the server doesn't list.
func titleOf(mapName string) string {
	if m, ok := mapByName(mapName); ok {
		return m.of
	}
	return t5
}

// hosted returns the titles the server hosts: those that the game files have a map of, in the order of l.titles. The
// page offers a choice of title where there are two.
func (l *lobby) hosted() []title {
	have := l.maps()
	var out []title
	for _, t := range l.titles {
		if slices.ContainsFunc(have, func(m string) bool { return titleOf(m) == t.id() }) {
			out = append(out, t)
		}
	}
	return out
}

// titleMap returns the map that r goes to when its host picks the title of (setRoom), as pickMap picks it.
func (l *lobby) titleMap(r *room, of string) string {
	return l.pickMap(r, of, func(m string) bool { return titleOf(m) == of })
}

// pickMap returns the map that r goes to of the maps that is takes: r's own map when it is one, else the map r last
// had of them (r.had[had]), else the server's default map when that is one, else the first of them that the game files
// have. It returns "" when the game files have none of them.
func (l *lobby) pickMap(r *room, had string, is func(string) bool) string {
	have := l.maps()
	for _, m := range append([]string{r.mapName, r.had[had], l.defaultMap}, have...) {
		if is(m) && slices.Contains(have, m) {
			return m
		}
	}
	return ""
}

// mapEntry is a Zombies map: the id of its title (of), its name in the game, its name on the page (title), and where
// its pictures and description are in the game files (mapart.go). Those are the names of the textures of its picture
// in the game's menus (preview) and of its loading screen, and the localized string of its description. A Black Ops
// map's textures are images/<name>.iwi in the archives, and a Black Ops II map's are in that game's image packs
// (ipak.go). A Black Ops II map's card in that game's lobby has two lines of words (t6.go: t6MapList): where the map
// is (place), over the map's name. The name is followed by the map's mode when the map has a mode alone that is not
// the story (mode): Survival, Grief or Turned.
//
// versus is how the map's players play against each other: in two teams (grief), or each for themselves (turned). It
// is "" on a co-op map, where they play together against the zombies: a map's story, and Survival. A room's host picks
// the room's versus as they pick its title, and the room goes to a map of it (versusMap). The room's grid shows the
// maps of its versus alone.
type mapEntry struct{ of, name, title, preview, loadscreen, description, place, mode, versus string }

// The kinds of versus (mapEntry.versus). A Grief room takes up to maxRoomSize players, whatever the server's rooms
// take otherwise (-max-players): four in each team, as the game was made for. The game ends a game of more than
// four players on any other mode, and of more than eight on Grief (maps/mp/zombies/_zm.gsc:
// player_too_many_players_check), and Plutonium's own script turns that off (scripts/zm/ranked.gsc).
const (
	coop   = ""
	grief  = "grief"
	turned = "turned"
)

// versusOf returns the versus of a map (mapEntry.versus): coop for a map the server doesn't list.
func versusOf(mapName string) string {
	m, _ := mapByName(mapName)
	return m.versus
}

// versusName returns the name of a versus in messages to players, or "" for none.
func versusName(versus string) string {
	return map[string]string{coop: "Co-op", grief: "Grief", turned: "Turned"}[versus]
}

// versusMap returns the map that r goes to when its host picks a versus of the title of (setRoom), as pickMap picks it
// of the maps of both.
func (l *lobby) versusMap(r *room, of, versus string) string {
	return l.pickMap(r, of+" "+versus, func(m string) bool { return titleOf(m) == of && versusOf(m) == versus })
}

// mapTitles is the Zombies maps, in the order the page lists them.
var mapTitles = slices.Concat(t5MapList, t6MapList)

func mapByName(name string) (mapEntry, bool) {
	i := slices.IndexFunc(mapTitles, func(m mapEntry) bool { return m.name == name })
	if i < 0 {
		return mapEntry{}, false
	}
	return mapTitles[i], true
}
