package main

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

// The relay can add lag, to play a game as if over a slower path than the real one. The console command "lag 100"
// adds 100 ms to every player's ping: half on the way to the game and half on the way back. The lag applies to every
// kind of packet, from a browser or from Windows. "lag 0" removes the lag, and "lag" logs what it is. The server
// does not save the lag: a restart starts it at 0.

// maxLag is the most ping the console command can add.
const maxLag = time.Second

// lagHeld is the most packets a lagLine holds, for one direction of one player's packets. It covers a second of a
// client that sends one packet a frame at 250 frames a second, or a second of a game's snapshots with the 61
// datagrams of a gamestate. When a lagLine is full, a further packet waits until there is room.
const lagHeld = 1024

// setLag handles the console command "lag [ms]". When arg is not empty, it sets the ping the relay adds. It then logs
// that ping.
func (r *relay) setLag(arg string) {
	if arg != "" {
		ms, err := strconv.Atoi(arg)
		if err != nil || ms < 0 || time.Duration(ms)*time.Millisecond > maxLag {
			log.Printf("server: lag takes the milliseconds to add to every player's ping, 0 to %d, not %q",
				maxLag.Milliseconds(), arg)
			return
		}
		r.lag.Store(int64(time.Duration(ms) * time.Millisecond))
	}
	log.Print("server: " + lagText(time.Duration(r.lag.Load())))
}

func lagText(lag time.Duration) string {
	if lag == 0 {
		return "the relay adds no lag"
	}
	return fmt.Sprintf("the relay adds %d ms to every player's ping, %d ms each way", lag.Milliseconds(),
		(lag / 2).Milliseconds())
}

// oneWay returns the lag the relay adds to a packet, which is half of the ping it adds.
func (r *relay) oneWay() time.Duration {
	return time.Duration(r.lag.Load()) / 2
}

// lagLine passes on one direction of a player's packets (send) after the relay's lag. It keeps the packets in the
// order they came, as a network path does, because a game drops a packet that arrives after a later one (the
// netchan's sequence). With no lag and no packet held, a packet goes on at once, as it would with no lagLine.
//
// Once done is closed, the lagLine still sends the packets it holds, each when it is due, and then closes ended. A
// network path also delivers what is on its way after its sender has gone. A page's last packet is its client's
// disconnect, sent when the page leaves the game, and the game waits for it at game over (SV_Lobby_TellOver,
// bo1-zombies/src/server_mp/sv_lobby_mp.cpp). send runs on one goroutine at a time.
type lagLine struct {
	send  func([]byte)
	held  chan lagged
	count atomic.Int32 // the packets held and not yet sent
	done  <-chan struct{}
	ended chan struct{}
}

type lagged struct {
	due    time.Time
	packet []byte
}

func newLagLine(send func([]byte), done <-chan struct{}) *lagLine {
	l := &lagLine{send: send, held: make(chan lagged, lagHeld), done: done, ended: make(chan struct{})}
	go l.run()
	return l
}

// put sends packet after lag. It copies a packet that it holds, so the caller can reuse its buffer.
func (l *lagLine) put(packet []byte, lag time.Duration) {
	if lag <= 0 && l.count.Load() == 0 {
		l.send(packet)
		return
	}
	l.count.Add(1)
	select {
	case l.held <- lagged{time.Now().Add(lag), slices.Clone(packet)}:
	case <-l.ended:
	}
}

func (l *lagLine) run() {
	defer close(l.ended)
	for {
		select {
		case p := <-l.held:
			l.handOn(p)
		case <-l.done:
			for {
				select {
				case p := <-l.held:
					l.handOn(p)
				default:
					return
				}
			}
		}
	}
}

func (l *lagLine) handOn(p lagged) {
	time.Sleep(time.Until(p.due))
	l.send(p.packet)
	l.count.Add(-1)
}
