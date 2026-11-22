package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// launchName matches the characters that the server removes from a name (server/rooms.go: nameUnsafe). A name
	// then has no double quote, which would end the name on the command line.
	launchName     = regexp.MustCompile(`[^A-Za-z0-9 _.\-'!?()\[\]|]`)
	launchPassword = regexp.MustCompile(`^[0-9A-Za-z]{1,32}$`)
	launchServer   = regexp.MustCompile(`^[A-Za-z0-9.-]+:[0-9]{1,5}$`)
)

// commandLine returns the command line that starts the player's game and joins it to a room's game. Plutonium's
// bootstrapper starts T6's Zombies on the game files in dir, in LAN mode, under the name that the room's game knows
// the player by. It connects the player's game to the room's game at the address server, with the game's password.
// LAN mode fits because the room's game is a LAN server: it asks Plutonium's own servers for nothing. The room's
// page shows the same name, password and address in a line for the game's console, for joining by hand
// (web/src/windows.ts: t6Console). The game parses the command line itself, so the values that the server and the
// player's PC put into it are checked: a quote or a plus in the wrong place would be another command.
func commandLine(exe, dir, name, password, server string) (string, error) {
	dir = strings.TrimRight(dir, `\/`) // a backslash before the closing quote would escape it
	if name = strings.TrimSpace(launchName.ReplaceAllString(name, "")); name == "" {
		name = "Player"
	}
	switch {
	case strings.ContainsAny(exe+dir, "\"\r\n") || dir == "":
		return "", fmt.Errorf("The game can't be started from the folder %q.", dir)
	case !launchPassword.MatchString(password) || !launchServer.MatchString(server):
		return "", errors.New("The server's answer isn't one the launcher can start a game from.")
	}
	return fmt.Sprintf(`"%s" t6zm "%s" -lan +name "%s" +set password %s +connect %s`, exe, dir, name, password, server), nil
}
