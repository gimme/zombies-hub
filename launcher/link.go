package main

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"zombies/internal/launcherapi"
)

// link has what a link on the server's page names (launcherapi): the server, and the player's key on that server. A
// link of the page's Play offline asks to play alone (solo), under the player's name. Such a link has no key when the
// page has no server: it was the browser's copy of the page. The launcher then calls no server.
type link struct {
	base string // the address of the server's page: https://host[:port], or http:// for a server on plain HTTP
	host string // the server's host name, where the room's games are reached too
	tls  bool
	key  string
	solo bool
	name string
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
)

var errLink = errors.New("The launcher doesn't recognise this link. Join from the room's page again.")

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
		name: q.Get("name")}
	if !linkKey.MatchString(l.key) && !(l.solo && l.key == "") || len(l.name) > 64 || !utf8.ValidString(l.name) {
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
