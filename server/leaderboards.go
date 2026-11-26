package main

import (
	"cmp"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// The leaderboards (GET /lobby/leaderboards), which the page shows. For each map there is a board of the highest
// rounds, and a board of the players who have finished the map's easter egg, among the games of the server's rooms.
// Each board has a category for each size of party: a duo, a squad of three or four, and a big squad of five up to the
// rooms' size (-max-players, at most 8). A server whose rooms hold 4 has no big squads. A record keeps the party's
// exact size, in case a squad of three and a squad of four, or the sizes of a big squad, are ever ranked apart.
//
// There are also two boards of all the maps (allBoard). They take a player's best on each map with a party of any of
// those sizes. One board has the sum of the player's highest rounds, and the other the sum of their times through the
// easter eggs. Each row has the records that it is the sum of. The player who asks also gets their own row, which the
// page shows as a card of the maps.
//
// The boards are by title (title.go): each title that the server hosts has its own, of its own maps, because a sum of
// a player's bests is over the maps of one title. A title has boards of easter eggs where the server has a map of it
// with one. A T6 map has one in its classic mode alone: its main quest (t6.go: t6Script).
//
// The level stores a player's best round on a map as one number for all sizes of party (<map>_highestwave), which
// can't hold both a duo's best and a squad's. So the server keeps its own records (record). It records a round when
// the round begins, so the round counts however the game ends. Each record names the game that set it. The database
// keeps every game's report (gameResult). The lobby has a report for as long as a record names it (keepGame), for the
// page to show. A game with mods (mods.go) counts like any other, and its report names the mods.
//
// The records are made from the games, and the database keeps none of them. A game's report has the round that each
// of its players reached, also of a player who left before its end (gameLine), and each player's easter egg as an
// event of the game (gameEvent). A player's record is the best of those over the games of a map and size of party
// (keep). The lobby has the players' records as its games set them (keepRound, keepEgg), and a server that starts
// makes them again from the games in the database (store.go: load), which gives the same.
//
// The maps on which the players play against each other (title.go: mapEntry.versus), T6's maps of Grief and Turned,
// have no boards. Their games are won, not survived: a round of Grief is how long both teams lasted, and Turned
// counts no rounds. Their games set no records, and the database keeps no report of them.

// boardRows is how many players a board lists at most.
const boardRows = 50

// eggs are the maps with an easter egg, in no particular order. A T5 map has the stat that it adds to when a party of
// two or more finishes its easter egg (t5.go: gameLine). Ascension's stat is not the level's own: the engine's script
// fixes add it (bo1-zombies/src/clientscript/cscr_parser.cpp: s_scrSourceFixes). A T6 map has no stat: the server's
// script in the game reports when its main quest is done (t6.go: t6Script).
var eggs = []struct{ mapName, stat string }{
	{"zombie_coast", "ZOMBIE_COAST_EGG_COOP"},
	{"zombie_temple", "ZOMBIE_TEMPLE_SIDEQUEST"},
	{"zombie_moon", "ZOMBIE_MOON_SIDEQUEST_TOTAL"},
	{"zombie_cosmodrome", "ZOMBIE_COSMO_EGG"},
	{"zm_classic_transit", ""},
	{"zm_classic_rooftop", ""},
	{"zm_classic_prison", ""},
	{"zm_classic_processing", ""},
	{"zm_classic_tomb", ""},
}

// hasEgg reports whether the map has an easter egg (eggs).
func hasEgg(mapName string) bool {
	return slices.ContainsFunc(eggs, func(e struct{ mapName, stat string }) bool { return e.mapName == mapName })
}

// record is a player's record on a map with a party of one size: their best round, or their fastest time through the
// map's easter egg.
type record struct {
	Map     string    `json:"map"`
	Party   int       `json:"party"` // how many players started the game
	Round   int       `json:"round,omitempty"`
	Seconds int64     `json:"seconds,omitempty"` // for an easter egg: when it was done, in seconds from round 1
	At      time.Time `json:"at"`
	Game    string    `json:"game"` // the ID of the report of the game that set it
}

// better compares a and b for ranking. It returns a negative number when a ranks first: a has the higher round, or the
// faster easter egg, or else was set first.
func better(a, b record) int {
	return cmp.Or(b.Round-a.Round, cmp.Compare(a.Seconds, b.Seconds), a.At.Compare(b.At))
}

// keep stores now in the list as the record for its map and size of party, if now is better than the record there. It
// reports whether it stored now.
func keep(list *[]record, now record) bool {
	i := slices.IndexFunc(*list, func(r record) bool { return r.Map == now.Map && r.Party == now.Party })
	switch {
	case i < 0:
		*list = append(*list, now)
	case better(now, (*list)[i]) < 0:
		(*list)[i] = now
	default:
		return false
	}
	return true
}

// keepRound sets p's best round on the map with a party of that size to round, if round is better than the record p
// had.
func (p *player) keepRound(mapName string, party, round int, game string) {
	if keep(&p.rounds, record{Map: mapName, Party: party, Round: round, At: time.Now(), Game: game}) {
		log.Printf("server: %s's best on %s with a party of %d: round %d", p.name, mapName, party, round)
	}
}

// keepEgg sets p's time through the map's easter egg with a party of that size to seconds, if that is faster than the
// time p had.
func (p *player) keepEgg(mapName string, party int, seconds int64, game string) {
	if keep(&p.eggs, record{Map: mapName, Party: party, Seconds: seconds, At: time.Now(), Game: game}) {
		log.Printf("server: %s finished the easter egg of %s with a party of %d in %d s", p.name, mapName, party, seconds)
	}
}

// keepRounds records the round of res for each of rg's players whose character is in the game: the players that rg has
// a line of the scoreboard for. The size of the party is the number of players that started the game. keepRounds also
// keeps the report, with the part of every player who has been in the game (reach). It then clears the lines of the
// scoreboard, so that the game's next lines are those of the players who are in it then. On a map of Grief or Turned
// it keeps nothing, and only clears the lines. The caller holds l.mu.
func (l *lobby) keepRounds(r *room, rg *roomGame, res *gameResult) {
	if res.Round > 0 && rg.started && versusOf(r.mapName) == coop {
		for _, s := range rg.seats {
			if _, ok := rg.stats[s.local.String()]; ok {
				s.player.keepRound(r.mapName, len(rg.seats), res.Round, res.ID)
			}
		}
		res.lines = rg.reach(res)
		rg.report = res
		l.keepGame(res)
	}
	rg.stats = nil
}

// reach notes what res says of the players whose character is in rg's game: their line of the scoreboard as it stands,
// and the round they have reached, which is the report's when that is later than the one they had. It returns the
// part of every player who has been in the game: first those of res, in its order, then those who have left, in the
// order they came.
func (rg *roomGame) reach(res *gameResult) []gameLine {
	if rg.reached == nil {
		rg.reached = map[string]gameLine{}
	}
	lines := []gameLine{}
	for _, st := range res.Players {
		line, was := rg.reached[st.ID]
		if !was {
			rg.order = append(rg.order, st.ID)
		}
		if res.Round > line.round {
			line.round, line.seconds = res.Round, res.Seconds
		}
		line.playerStats, line.gone = st, false
		rg.reached[st.ID] = line
		lines = append(lines, line)
	}
	for _, id := range rg.order {
		if !slices.ContainsFunc(res.Players, func(st playerStats) bool { return st.ID == id }) {
			line := rg.reached[id]
			line.gone = true
			rg.reached[id] = line
			lines = append(lines, line)
		}
	}
	return lines
}

// keepGame keeps the report of a game, as it stands or as it ended: in the database, in the place of the game's
// earlier report, and in the lobby. The lobby drops the reports that no player's record names any more, including
// this one when it set no record. The caller holds l.mu.
func (l *lobby) keepGame(res *gameResult) {
	named := map[string]bool{}
	for _, p := range l.players {
		for _, r := range slices.Concat(p.rounds, p.eggs) {
			named[r.Game] = true
		}
	}
	l.reports[res.ID] = res
	maps.DeleteFunc(l.reports, func(id string, _ *gameResult) bool { return !named[id] })
	l.unsavedGames[res.ID] = res
	l.save()
}

// on returns the records in the list that are on the map.
func on(list []record, mapName string) []record {
	var out []record
	for _, r := range list {
		if r.Map == mapName {
			out = append(out, r)
		}
	}
	return out
}

// bestOf returns the best of the records with a party of least to most players, or nil when there is none.
func bestOf(list []record, least, most int) *record {
	var best *record
	for _, r := range list {
		if r.Party >= least && r.Party <= most && (best == nil || better(r, *best) < 0) {
			best = &r
		}
	}
	return best
}

// boardRow is a player's row on a board.
type boardRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Avatar  string `json:"avatar,omitempty"` // the player's picture (avatar)
	Round   int    `json:"round,omitempty"`
	Seconds int64  `json:"seconds,omitempty"`
	Party   int    `json:"party"`
	Game    string `json:"game"` // the ID of the game's report, in leaderboards.Games

	best record
}

// mapBoards are a map's boards, one for each category.
type mapBoards struct {
	Map      string     `json:"map"`
	Duo      []boardRow `json:"duo"`
	Squad    []boardRow `json:"squad"`
	BigSquad []boardRow `json:"bigSquad"`
}

// allRow is a player's row on a board of all the maps: the sum of their bests, and each of the bests.
type allRow struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Avatar  string   `json:"avatar,omitempty"`
	Round   int      `json:"round,omitempty"`   // the sum of their highest rounds
	Seconds int64    `json:"seconds,omitempty"` // the sum of their fastest times
	Bests   []record `json:"bests"`             // the records that are summed: their best on each map where they have one

	at time.Time // when they reached the sum: the time of the latest of their bests
}

// allBoard is a board of all the maps.
type allBoard struct {
	Rows []allRow `json:"rows"`
	Mine allRow   `json:"mine"` // the row of the player who asks, whether or not Rows has it
}

// kindBoards are the boards of one kind, the rounds or the easter eggs, of one title.
type kindBoards struct {
	Maps []mapBoards `json:"maps"` // a map's own, for each map of the kind that the server has, in the page's order
	All  allBoard    `json:"all"`  // the board of all of those maps
}

// titleBoards are the boards of one title: its id and its name, as the view has them (titleView), and its boards.
type titleBoards struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Rounds kindBoards  `json:"rounds"`
	Eggs   *kindBoards `json:"eggs,omitempty"` // the maps of it that have an easter egg, where the server has any
}

type leaderboards struct {
	Titles []titleBoards `json:"titles"` // the titles the server hosts (hosted), in that order
	// Games has the reports of the games that the boards name, by ID.
	Games map[string]*gameResult `json:"games"`
}

// board returns a board: each player's best of their records in parties of least to most players, ranked, and cut to
// the first boardRows. The caller holds l.mu.
func (l *lobby) board(players []*player, records func(*player) []record, least, most int) []boardRow {
	rows := []boardRow{}
	for _, p := range players {
		if best := bestOf(records(p), least, most); best != nil {
			rows = append(rows, boardRow{ID: p.id, Name: p.asked, Avatar: l.avatar(p), Round: best.Round,
				Seconds: best.Seconds, Party: best.Party, Game: best.Game, best: *best})
		}
	}
	slices.SortFunc(rows, func(a, b boardRow) int {
		return cmp.Or(better(a.best, b.best), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.ID, b.ID))
	})
	return rows[:min(len(rows), boardRows)]
}

// allRow returns p's row on a board of all the maps. The row has p's best of the records on each of mapNames, in that
// order, with a party of any size that a map's boards have, and the sum of those bests. The caller holds l.mu.
func (l *lobby) allRow(p *player, mapNames []string, records []record) allRow {
	row := allRow{ID: p.id, Name: p.asked, Avatar: l.avatar(p), Bests: []record{}}
	for _, name := range mapNames {
		if best := bestOf(on(records, name), 2, max(4, l.roomSize)); best != nil {
			row.Bests = append(row.Bests, *best)
			row.Round += best.Round
			row.Seconds += best.Seconds
			if best.At.After(row.at) {
				row.at = best.At
			}
		}
	}
	return row
}

// allBoard returns the board of all of mapNames. It has the row of each player with a record on one of them (allRow),
// ranked by rank and then by who reached their sum first, and cut to the first boardRows. It also has asker's own row.
// The caller holds l.mu.
func (l *lobby) allBoard(players []*player, asker *player, mapNames []string, records func(*player) []record,
	rank func(a, b allRow) int) allBoard {
	rows := []allRow{}
	for _, p := range players {
		if row := l.allRow(p, mapNames, records(p)); len(row.Bests) > 0 {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(a, b allRow) int {
		return cmp.Or(rank(a, b), a.at.Compare(b.at), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.ID, b.ID))
	})
	return allBoard{Rows: rows[:min(len(rows), boardRows)], Mine: l.allRow(asker, mapNames, records(asker))}
}

// leaderboards returns the boards as they are now, for asker, one of the server's players. On a server where players
// sign in with Discord, the boards leave out the players who have not, as playerOf does. The caller holds l.mu.
func (l *lobby) leaderboards(asker *player) leaderboards {
	var players []*player
	for _, p := range l.players {
		if l.discord == nil || p.discord != nil {
			players = append(players, p)
		}
	}
	b := leaderboards{Titles: []titleBoards{}, Games: map[string]*gameResult{}}
	game := func(id string) {
		if res := l.reports[id]; res != nil && b.Games[id] == nil {
			b.Games[id] = l.withPlayers(res)
		}
	}
	// kind returns the boards of one kind over mapNames, ranked by rank, and adds the reports of the games they name.
	// The reports are those of the rows of the maps' boards, and of the asker's own row of all the maps, from whose
	// bests the page opens a report too.
	kind := func(mapNames []string, records func(*player) []record, rank func(a, b allRow) int) kindBoards {
		k := kindBoards{Maps: []mapBoards{}, All: l.allBoard(players, asker, mapNames, records, rank)}
		for _, name := range mapNames {
			of := func(p *player) []record { return on(records(p), name) }
			m := mapBoards{Map: name, Duo: l.board(players, of, 2, 2), Squad: l.board(players, of, 3, 4),
				BigSquad: l.board(players, of, 5, l.roomSize)}
			k.Maps = append(k.Maps, m)
			for _, row := range slices.Concat(m.Duo, m.Squad, m.BigSquad) {
				game(row.Game)
			}
		}
		for _, r := range k.All.Mine.Bests {
			game(r.Game)
		}
		return k
	}
	for _, t := range l.hosted() {
		var mapNames, eggMaps []string
		for _, name := range l.maps() {
			if titleOf(name) != t.id() || versusOf(name) != coop {
				continue
			}
			mapNames = append(mapNames, name)
			if hasEgg(name) {
				eggMaps = append(eggMaps, name)
			}
		}
		// Rounds rank by the higher sum. Easter eggs rank by the higher number of easter eggs, then by the lower sum
		// of their times.
		tb := titleBoards{ID: t.id(), Name: t.label(),
			Rounds: kind(mapNames, func(p *player) []record { return p.rounds },
				func(x, y allRow) int { return y.Round - x.Round })}
		if len(eggMaps) > 0 {
			k := kind(eggMaps, func(p *player) []record { return p.eggs },
				func(x, y allRow) int { return cmp.Or(len(y.Bests)-len(x.Bests), cmp.Compare(x.Seconds, y.Seconds)) })
			tb.Eggs = &k
		}
		b.Titles = append(b.Titles, tb)
	}
	return b
}

// serveLeaderboards answers GET /lobby/leaderboards for one of the server's players.
func (l *lobby) serveLeaderboards(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	l.mu.Lock()
	defer l.mu.Unlock()
	asker := l.playerOf(bearer(req))
	if asker == nil {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(struct {
			Error string `json:"error"`
		}{l.unknown().Error()})
		return
	}
	json.NewEncoder(w).Encode(l.leaderboards(asker))
}
