package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"zombies/internal/launcherapi"
)

// link has what a link on the server's page names (launcherapi): the server, and the player's key on that server. A
// link that launches a game asks to play alone (solo), under the player's name: the title of (Black Ops II when the
// link names none), and a map of it to have first, if any. Such a link has no key when the page has no server: it was
// the browser's copy of the page. The launcher then calls no server. Any other link has the PC made ready: for the
// game of the player's room, or, while they are in no room, for the title of, with the map that it names.
type link struct {
	base    string // the address of the server's page: https://host[:port], or http:// for a server on plain HTTP
	host    string // the server's host name, where the room's games are reached too
	tls     bool
	key     string
	solo    bool
	name    string
	of      string // the id of the title that the link asks for, or ""
	mapName string // the map of it that the launcher is to get, or ""
}

// offline reports whether the link has no server for the launcher to call: a link to play alone, from a page that
// could not reach its server.
func (l link) offline() bool { return l.key == "" }

// id returns a name for the link's player on the link's server, for the one launcher they have at a time (single).
// The name is a hash, and shows nothing of the key.
func (l link) id() string {
	sum := launcherapi.NewHash()
	sum.Write([]byte(l.base + " " + l.key))
	return launcherapi.HashOf(sum)[:16]
}

var (
	linkHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	linkKey  = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
	// linkWord matches a title's id and a map's name, as a link names them and as the launcher keeps them (notePress).
	linkWord = regexp.MustCompile(`^[a-z0-9_]{0,64}$`)
)

var errLink = errors.New("The launcher doesn't recognise this link. Try again from the page.")

// parseLink parses a link strictly, because what the link names goes into the addresses that the launcher calls and
// onto the game's command line. Any page can have a link of the launcher's scheme, and Windows passes the launcher
// whatever follows the scheme.
func parseLink(s string) (link, error) {
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, launcherapi.Scheme) || u.User != nil || u.Opaque != "" || u.Fragment != "" ||
		strings.Trim(u.Path, "/") != "play" {
		return link{}, errLink
	}
	host, port := u.Hostname(), u.Port()
	if !linkHost.MatchString(host) || len(host) > 253 {
		return link{}, errLink
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return link{}, errLink
		}
	}
	q := u.Query()
	l := link{host: strings.ToLower(host), tls: q.Get("tls") != "0", key: q.Get("key"), solo: q.Get("solo") == "1",
		name: q.Get("name"), of: q.Get("of"), mapName: q.Get("map")}
	if !linkKey.MatchString(l.key) && !(l.solo && l.key == "") || len(l.name) > 64 || !utf8.ValidString(l.name) ||
		!linkWord.MatchString(l.of) || !linkWord.MatchString(l.mapName) {
		return link{}, errLink
	}
	l.base = "https://" + l.host
	if !l.tls {
		l.base = "http://" + l.host
	}
	if port != "" {
		l.base += ":" + port
	}
	return l, nil
}

// A player has one launcher on a server (single). When they press a button of the page while it runs, the launcher
// that the press's link starts only signals the one that runs (press), and a signal carries nothing. So it first
// leaves a note of what the link asks for, the title and the map, in the launcher's folder, for the launcher that
// runs to read as the signal reaches it (pressed). The presses of the two kinds of link (link.solo) each have a
// note: a press of one kind says nothing of what the last press of the other asked for.

// pressNote is the file of the note for the launcher of a link's player on its server: of their last press that
// asked to play alone (solo), or of their last press of the other kind.
func pressNote(home string, l link, solo bool) string {
	if solo {
		return filepath.Join(home, "press-"+l.id()+".txt")
	}
	return filepath.Join(home, "press-"+l.id()+"-ready.txt")
}

// notePress leaves the note of what the link l asks for.
func notePress(home string, l link) {
	os.WriteFile(pressNote(home, l, l.solo), []byte(l.of+"\n"+l.mapName+"\n"), 0o644)
}

// pressed returns what the player's last press asked for, for the launcher of l: of their presses that asked to play
// alone (solo), or of the others. It is the title's id and the map, as the note has them. noted is false where there
// is no note that reads as one: what pressed returns is then the link's own, where l is of that kind, and nothing
// otherwise.
func pressed(home string, l link, solo bool) (of, mapName string, noted bool) {
	data, err := os.ReadFile(pressNote(home, l, solo))
	lines := strings.Split(string(data), "\n")
	if err != nil || len(lines) < 2 || !linkWord.MatchString(lines[0]) || !linkWord.MatchString(lines[1]) {
		if l.solo != solo {
			return "", "", false
		}
		return l.of, l.mapName, false
	}
	return lines[0], lines[1], true
}

// nearby reports whether host is in the PC's own network: every address of its name is a private or link-local
// address, or the PC's own.
func nearby(host string) bool {
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}
