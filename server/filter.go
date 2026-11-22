package main

import (
	"bytes"
	"log"
	"slices"
	"strings"
	"time"
)

// The relay passes a player's packet on to their game only when it is a packet that a client sends. A game would
// read more kinds of packet (rcon, ...), and it would parse them with code that no one has audited (docs/hosting.md:
// Security). A client sends netchan packets and three kinds of out-of-band packet. None of them is long, and a client
// sends only so many a second.

// maxFromPlayer is the length of the longest packet a client sends its game. That is a voice packet, which is out of
// band: the 4 bytes that start it, then tempVoicePacketBuf (bo1-zombies/src/client/cl_voice.cpp). A client's netchan
// packets fit a datagram (MAX_PACKETLEN), and its getchallenge and connect are at most about 1 KB.
const maxFromPlayer = 4 + 1260

// playerRate is how many packets a second a player may send their game. A client sends at most one packet a frame,
// at up to 1000 frames a second (com_maxfps), and its voice packets. The relay drops the rest, like a network path
// that carries no more.
const playerRate = 2000

// clientCommands are the out-of-band packets a client sends its game: getchallenge and connect to connect, and v for
// its voice.
var clientCommands = []string{"getchallenge", "connect", "v"}

// clientSends reports whether packet is one that a client sends its game: a netchan packet or one of clientCommands,
// no longer than maxFromPlayer.
func clientSends(packet []byte) bool {
	if len(packet) > maxFromPlayer {
		return false
	}
	if !bytes.HasPrefix(packet, outOfBand) {
		return true
	}
	args := oobArgs(packet)
	if len(args) == 0 {
		return false
	}
	return slices.ContainsFunc(clientCommands, func(c string) bool { return strings.EqualFold(args[0], c) })
}

// maxFromT6 is the length of the longest packet the relay passes on a T6 client's route: what fits a datagram on
// Ethernet. A T6 client's packets are Plutonium's, and there is no source code to read their longest length from.
const maxFromT6 = 1472

// t6ClientSends reports whether a T6 client may send packet to its game (t6.go). It may send any netchan packet, and
// any out-of-band packet except one for the game's remote console. Only the server uses the remote console, whatever
// password a packet carries. A T6 client's handshake is Plutonium's own, and the relay has no list of its packets,
// so the relay passes every packet it has no rule against. t6ClientSends finds the first word the way the game does
// (t6Word).
func t6ClientSends(packet []byte) bool {
	if len(packet) > maxFromT6 {
		return false
	}
	word, _, oob := t6Word(packet)
	return !oob || len(word) > 0 && !bytes.EqualFold(word, []byte("rcon"))
}

// t6Word returns the first word of an out-of-band packet, and what follows the word, the way a T6 game finds them:
// the game's tokenizer skips all leading bytes with a value up to that of a space, and the word ends at the next such
// byte. The game reads the word in any letter case. oob is false for a packet that is not out of band.
func t6Word(packet []byte) (word, rest []byte, oob bool) {
	text, oob := bytes.CutPrefix(packet, outOfBand)
	if !oob {
		return nil, nil, false
	}
	blank := func(r rune) bool { return r <= ' ' }
	text = bytes.TrimLeftFunc(text, blank)
	end := bytes.IndexFunc(text, blank)
	if end < 0 {
		end = len(text)
	}
	return text[:end], text[end:], true
}

// t6Connect reports whether a T6 client's packet is its connect, the packet that takes the client a slot in the game
// (t6.go: the order the players join in). name is the name the client plays under, from the packet's userinfo
// (connect "\protocol\2089\name\Ann\..."), or "" when the userinfo has none. The userinfo is what the packet has in
// quotes, or else all that follows the word.
func t6Connect(packet []byte) (name string, ok bool) {
	word, rest, oob := t6Word(packet)
	if !oob || !bytes.EqualFold(word, []byte("connect")) {
		return "", false
	}
	info := strings.TrimSpace(string(rest))
	if _, quoted, found := strings.Cut(info, `"`); found {
		info, _, _ = strings.Cut(quoted, `"`)
	}
	parts := strings.Split(strings.TrimPrefix(info, `\`), `\`)
	for i := 0; i+1 < len(parts); i += 2 {
		if strings.EqualFold(parts[i], "name") {
			return strings.TrimSpace(parts[i+1]), true
		}
	}
	return "", true
}

// playerFilter decides which of a player's packets go on to their game. It passes the packets that a client sends
// (clientSends, or t6ClientSends for a T6 client), and no more of them than a client sends (playerRate). It logs the
// first packet it drops for each of the two reasons. One goroutine at a time uses it.
type playerFilter struct {
	who       string    // the player, for the log
	t6        bool      // the player's client is a T6 client
	allowance float64   // how many packets the player may send now, at most a second's worth
	last      time.Time // when allowance was last updated
	odd, fast bool      // whether a drop was logged: of a packet no client sends, of a packet over the rate
}

func newPlayerFilter(who string) *playerFilter {
	return &playerFilter{who: who, allowance: playerRate, last: time.Now()}
}

// pass reports whether the player's packet, which reached the relay at now, goes on to their game.
func (f *playerFilter) pass(packet []byte, now time.Time) bool {
	f.allowance = min(playerRate, f.allowance+now.Sub(f.last).Seconds()*playerRate)
	f.last = now
	switch {
	case f.t6 && !t6ClientSends(packet) || !f.t6 && !clientSends(packet):
		if !f.odd {
			log.Printf("relay: %s: dropped a packet no client sends (%d bytes: %.32q), and drops the rest like it",
				f.who, len(packet), packet)
			f.odd = true
		}
		return false
	case f.allowance < 1:
		if !f.fast {
			log.Printf("relay: %s: sent more than %d packets a second, which no client does: the rest are dropped",
				f.who, playerRate)
			f.fast = true
		}
		return false
	}
	f.allowance--
	return true
}
