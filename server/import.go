package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// importPlayers reads the players file of a server from before the database (players.json) into the database, when
// the database has no players. The file has what keptPlayers has, as JSON, with each player's records and the reports
// of the games that a record named. Once it is in the database, the file is renamed (players.json.imported), so that
// the next start leaves it alone. A file that can't be read is renamed too (players.json.bad), and the server starts
// with the players that the database has. When there is no file, importPlayers does nothing.
func (s *store) importPlayers(file string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var have int
	if err := s.db.QueryRow(`SELECT count(*) FROM players`).Scan(&have); err != nil {
		return err
	}
	if have > 0 {
		log.Printf("server: %s is not imported, because the database already has players", file)
		return nil
	}
	var k keptPlayers
	if err := json.Unmarshal(data, &k); err != nil {
		log.Printf("server: can't read %s (%v): it is not imported, and is now %s.bad", file, err, file)
		os.Rename(file, file+".bad")
		return nil
	}
	// Leave out what the server left out as it read the file: a player without an id or a key, or with those of a
	// player before them, and a session of none of the players.
	ids, keys, hashes, games := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	party := map[string]int{} // how many players started each game, as the records that name the game say
	in := keptPlayers{}
	for _, p := range k.Players {
		if p.ID == "" || p.Key == "" || ids[p.ID] || keys[p.Key] {
			continue
		}
		ids[p.ID], keys[p.Key] = true, true
		in.Players = append(in.Players, p)
		for _, r := range slices.Concat(p.Rounds, p.Eggs) {
			party[r.Game] = r.Party
		}
	}
	for _, ks := range k.Sessions {
		if ks.Hash != "" && !hashes[ks.Hash] && ids[ks.Player] {
			hashes[ks.Hash] = true
			in.Sessions = append(in.Sessions, ks)
		}
	}
	for _, g := range k.Games {
		if g == nil || g.ID == "" || games[g.ID] {
			continue
		}
		games[g.ID] = true
		// The file's reports lack what the database adds to a report.
		if g.party = party[g.ID]; g.party == 0 {
			g.party = len(g.Players)
		}
		g.began = idTime(g.ID)
		g.round1 = g.began
		in.Games = append(in.Games, g)
	}
	// The database keeps no records: they are made from the games. So a record of the file that its game's report
	// does not have is added to the report: the round of a player with no line there as a line of theirs, of a player
	// who had left, and an easter egg as an event of the game. A record that names a game of which the file has no
	// report is not imported.
	lost := 0
	for _, p := range in.Players {
		for _, r := range slices.Concat(p.Rounds, p.Eggs) {
			i := slices.IndexFunc(in.Games, func(g *gameResult) bool { return g.ID == r.Game })
			if i < 0 {
				lost++
				continue
			}
			g := in.Games[i]
			reached := func(line gameLine) bool { return line.ID == p.ID && line.round >= r.Round }
			switch {
			case r.Seconds > 0:
				g.events = append(g.events, gameEvent{seconds: r.Seconds, round: g.Round, kind: eventEgg, player: p.ID})
			case !slices.ContainsFunc(g.allLines(), reached):
				left := gameLine{playerStats: playerStats{ID: p.ID, Name: p.Name}, round: r.Round, gone: true}
				g.lines = append(g.allLines(), left)
			}
		}
	}
	if err := s.save(in); err != nil {
		return fmt.Errorf("can't import %s: %v", file, err)
	}
	log.Printf("server: imported %d players, %d sessions and %d games from %s, which is now %s.imported",
		len(in.Players), len(in.Sessions), len(in.Games), file, file)
	if lost > 0 {
		log.Printf("server: %d records of %s named a game that it has no report of, and are not imported", lost, file)
	}
	if err := os.Rename(file, file+".imported"); err != nil {
		log.Printf("server: %v", err)
	}
	return nil
}

// idTime returns when a game started, as its ID says (gameID), or the zero time for an ID of another form.
func idTime(id string) time.Time {
	ms, err := strconv.ParseInt(id[strings.LastIndex(id, "-")+1:], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
