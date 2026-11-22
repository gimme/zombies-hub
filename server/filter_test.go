package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientSendsOnlyAClientsPackets(t *testing.T) {
	netchan := randomPacket(maxFromPlayer)
	netchan[0] = 0
	for packet, want := range map[string]bool{
		string(netchan[:12]):     true,
		string(netchan):          true,
		string(netchan) + "\x00": false, // longer than any a client sends
		"\xff\xff\xff\xff" + `getchallenge 0 "" "0" "0123abcd"`:           true,
		"\xff\xff\xff\xff" + `connect "\protocol\1044\name\A\qport\4321"`: true,
		"\xff\xff\xff\xffv\x00\x12\x34\x01\x02ab":                         true,
		"\xff\xff\xff\xff  CONNECT \"\"":                                  true, // as the game reads it
		"\xff\xff\xff\xff\"v\"\x00":                                       true,
		"\xff\xff\xff\xffrcon secret status":                              false,
		"\xff\xff\xff\xffsteamauth\x00\x00\x00\x00":                       false,
		"\xff\xff\xff\xffgetstatus":                                       false,
		"\xff\xff\xff\xffconnectx":                                        false,
		"\xff\xff\xff\xff\x01v":                                           false, // the game reads "v": no client sends it
		"\xff\xff\xff\xff":                                                false,
		"\xff\xff\xff\xffv " + strings.Repeat("a", maxFromPlayer):         false,
	} {
		if got := clientSends([]byte(packet)); got != want {
			t.Errorf("%.40q (%d bytes): %v, want %v", packet, len(packet), got, want)
		}
	}
}

func TestPlayerFilterPassesAsManyPacketsAsAClientSends(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := newPlayerFilter("1.2.3.4:5 (A)")
	now := f.last
	packet := []byte("hello")
	passed := 0
	for range 3 * playerRate {
		if f.pass(packet, now) {
			passed++
		}
	}
	if passed != playerRate {
		t.Fatalf("%d packets at once passed, not a second's, %d", passed, playerRate)
	}
	// a second's packets, a millisecond apart, pass
	for i := range playerRate {
		now = now.Add(time.Second / playerRate)
		if !f.pass(packet, now) {
			t.Fatalf("packet %d at %d a second: dropped", i, playerRate)
		}
	}
	later := now.Add(time.Second)
	if f.pass([]byte("\xff\xff\xff\xffrcon x"), later) || f.pass([]byte("\xff\xff\xff\xffrcon y"), later) {
		t.Fatal("rcon passed")
	}
	if !f.pass(packet, later) {
		t.Fatal("a packet after a dropped one: dropped")
	}
	if got := logged.String(); strings.Count(got, "relay: 1.2.3.4:5 (A): ") != 2 ||
		!strings.Contains(got, ": sent more than 2000 packets a second, which no client does: the rest are dropped") ||
		!strings.Contains(got, `: dropped a packet no client sends (10 bytes: "\xff\xff\xff\xffrcon x"), and drops the`) {
		t.Fatalf("not once each:\n%s", got)
	}
}

func TestRelayHandsGamesOnlyWhatClientsSend(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess := dial(t, ctx, ts, ts.seated(t, "A"))
	win := windowsPlayer(t, ts.target)
	udpExchange(t, win, getchallenge(ts.key(ts.seated(t, "WinA"))))

	// the stand-in game answers every packet it gets: a dropped one would be answered before the one after it
	dropped := [][]byte{[]byte("\xff\xff\xff\xffrcon secret status"), append(randomPacket(maxFromPlayer+1), 0)}
	dropped[1][0] = 0
	voice := []byte("\xff\xff\xff\xffv\x00\x12\x34\x01\x02ab")
	var nextID uint16
	for _, packet := range dropped {
		if err := framePacket(packet, &nextID, sess.SendDatagram); err != nil {
			t.Fatal(err)
		}
		win.Write(packet)
	}
	if _, got := exchange(t, ctx, sess, voice); !bytes.Equal(got, voice) {
		t.Errorf("the game got %q from the browser", got)
	}
	if _, got := udpExchange(t, win, voice); !bytes.Equal(got, voice) {
		t.Errorf("the game got %q from Windows", got)
	}
	rcon := `: dropped a packet no client sends (22 bytes: "\xff\xff\xff\xffrcon secret status")`
	if got := logged.String(); strings.Count(got, ": dropped a packet no client sends (") != 2 ||
		!strings.Contains(got, " (A, room "+ts.code+")"+rcon) || !strings.Contains(got, " (WinA, room "+ts.code+")"+rcon) {
		t.Fatalf("not once for each player:\n%s", got)
	}
}
