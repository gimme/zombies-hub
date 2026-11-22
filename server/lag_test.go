package main

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

// roundTrip is how long a browser player's packet takes to the stand-in game and its answer back.
func roundTrip(t *testing.T, ctx context.Context, ts *testServer, token string) time.Duration {
	t.Helper()
	sess := dial(t, ctx, ts, token)
	exchange(t, ctx, sess, []byte("first")) // the session's start out of the way
	start := time.Now()
	exchange(t, ctx, sess, []byte("hello"))
	return time.Since(start)
}

func TestRelayAddsTheConsolesLagToEveryPlayersPing(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const lag = 300 * time.Millisecond

	ts.lobby.command("lag 300")
	if got := roundTrip(t, ctx, ts, ts.seated(t, "A")); got < lag || got > lag+200*time.Millisecond {
		t.Errorf("a browser's packet and its answer took %s with lag 300", got)
	}
	win := windowsPlayer(t, ts.target)
	token := ts.seated(t, "WinA")
	start := time.Now()
	udpExchange(t, win, getchallenge(ts.key(token)))
	if got := time.Since(start); got < lag || got > lag+200*time.Millisecond {
		t.Errorf("a Windows player's packet and its answer took %s with lag 300", got)
	}

	ts.lobby.command("lag 0")
	if got := roundTrip(t, ctx, ts, ts.seated(t, "B")); got > 100*time.Millisecond {
		t.Errorf("a browser's packet and its answer took %s with lag 0", got)
	}
	start = time.Now()
	udpExchange(t, win, []byte("\x00hello"))
	if got := time.Since(start); got > 100*time.Millisecond {
		t.Errorf("a Windows player's packet and its answer took %s with lag 0", got)
	}
}

func TestLagTakesMillisecondsUpToASecond(t *testing.T) {
	r := &relay{}
	r.setLag("120")
	for _, bad := range []string{"-5", "x", "1001", "50ms"} {
		r.setLag(bad)
		if got := time.Duration(r.lag.Load()); got != 120*time.Millisecond {
			t.Errorf("lag %s made it %s, not left at 120 ms", bad, got)
		}
	}
	r.setLag("1000")
	r.setLag("")
	if got := r.oneWay(); got != 500*time.Millisecond {
		t.Errorf("lag 1000 is %s each way, not 500 ms", got)
	}
}

// A path keeps a stream's packets in order, and so does the lag: a game drops a packet that comes after a later one.
func TestLagLineKeepsPacketsInOrderAsTheLagChanges(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	var mu sync.Mutex
	var sent []byte
	all := make(chan struct{})
	line := newLagLine(func(p []byte) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, p[0])
		if len(sent) == 6 {
			close(all)
		}
	}, done)

	buf := []byte{0}
	for i, lag := range []time.Duration{200, 100, 0, 0, 50, 0} {
		buf[0] = byte(i)
		line.put(buf, lag*time.Millisecond) // the caller reuses its buffer
	}
	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Fatal("not every packet was sent")
	}
	if want := []byte{0, 1, 2, 3, 4, 5}; !slices.Equal(sent, want) {
		t.Errorf("sent %v, not %v", sent, want)
	}

	// none held and no lag: at once, before put returns, as with no lag line
	line.put([]byte{6}, 0)
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 7 {
		t.Errorf("a packet with no lag and none held was not sent at once")
	}
}

// What is on its way arrives though its sender has gone: a page's last packet, its game's disconnect, which the game
// waits for at game over, comes as the page leaves.
func TestLagLineHandsOnWhatItHoldsOnceItsSenderHasGone(t *testing.T) {
	done := make(chan struct{})
	var sent [][]byte
	line := newLagLine(func(p []byte) { sent = append(sent, slices.Clone(p)) }, done)
	line.put([]byte("last"), 100*time.Millisecond)
	start := time.Now()
	close(done)
	<-line.ended
	if len(sent) != 1 || string(sent[0]) != "last" {
		t.Fatalf("sent %q", sent)
	}
	if waited := time.Since(start); waited < 80*time.Millisecond {
		t.Errorf("handed on after %s, not when it was due", waited)
	}
}

func TestRelayHandsAPagesLastPacketToTheGameAfterItLeaves(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// a game that says what it gets
	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	heard := make(chan string, 10)
	go func() {
		buf := make([]byte, maxPacket+1)
		for {
			n, _, err := game.ReadFromUDP(buf)
			if err != nil {
				return
			}
			heard <- string(buf[:n])
		}
	}()
	ts.lobby.mu.Lock()
	ts.lobby.rooms[ts.code].game.port = game.LocalAddr().(*net.UDPAddr).Port
	ts.lobby.mu.Unlock()

	ts.lobby.command("lag 400")
	sess := dial(t, ctx, ts, ts.seated(t, "A"))
	var nextID uint16
	if err := framePacket([]byte("disconnect"), &nextID, sess.SendDatagram); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // the relay has it, and holds it for 200 ms
	sess.CloseWithError(0, "the page left the game")
	select {
	case got := <-heard:
		if got != "disconnect" {
			t.Errorf("the game got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("the game never got the packet the page sent as it left")
	}
}
