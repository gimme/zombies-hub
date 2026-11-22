package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/webtransport-go"
)

// testServer is a relay, and the server it asks where players go, in front of a stand-in game (standInGame). The server
// has a room, code, whose game is the stand-in, once round 1 has begun.
type testServer struct {
	target, hash string
	lobby        *lobby
	relay        *relay
	code         string
	game         *net.UDPConn // the stand-in's socket
}

// standInGame is a game that answers each packet with the address it came from, a newline, and the packet: out of band
// when the packet was, as the game answers a client it has not taken on.
func standInGame(t *testing.T) *net.UDPConn {
	t.Helper()
	game, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { game.Close() })
	go func() {
		buf := make([]byte, maxPacket+1)
		for {
			n, from, err := game.ReadFromUDP(buf)
			if err != nil {
				return
			}
			var answer []byte
			if bytes.HasPrefix(buf[:n], outOfBand) {
				answer = append(answer, outOfBand...)
			}
			answer = append(answer, from.String()+"\n"...)
			game.WriteToUDP(append(answer, buf[:n]...), from)
		}
	}()
	return game
}

func startRelay(t *testing.T, setup ...func(*relay)) *testServer {
	t.Helper()
	game := standInGame(t)

	certs := &certificates{}
	_, hash, err := certs.current()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := newLobby(ctx)
	l.maps = func() []string { return []string{"zombie_theater"} }
	l.sweepEvery = 50 * time.Millisecond
	go l.sweep() // who is on Windows
	r := newRelay(l, certs)
	l.relay = r
	for _, f := range setup {
		f(r)
	}
	go r.serve(udp)
	t.Cleanup(func() { r.close() })

	// a room whose game is the stand-in, which has begun, with a seat for its host
	host := l.hello("", "Host")
	l.mu.Lock()
	l.create(l.playerOf(host.Token), "", false)
	room := l.playerOf(host.Token).room
	local := r.acquire()
	local.Port = seatPort
	room.game = &roomGame{title: l.title(room.mapName), port: game.LocalAddr().(*net.UDPAddr).Port, started: true, began: time.Now(), cancel: func() {},
		seats: []*seat{{player: room.members[0], local: local, join: true}}}
	l.mu.Unlock()
	return &testServer{target: udp.LocalAddr().String(), hash: hash, lobby: l, relay: r, code: room.code, game: game}
}

// seated is a new player in the room, with a seat in its game, as one there from its start (the room takes no one while
// its game runs): their token.
func (ts *testServer) seated(t *testing.T, name string) string {
	t.Helper()
	token := ts.lobby.hello("", name).Token
	l := ts.lobby
	l.mu.Lock()
	defer l.mu.Unlock()
	p, r := l.playerOf(token), l.rooms[ts.code]
	r.members = append(r.members, p)
	p.room = r
	local := l.relay.acquire()
	local.Port = seatPort
	r.game.seats = append(r.game.seats, &seat{player: p, local: local, join: true})
	return token
}

// key is the player's key, for Play.bat.
func (ts *testServer) key(token string) string {
	ts.lobby.mu.Lock()
	defer ts.lobby.mu.Unlock()
	return ts.lobby.playerOf(token).key
}

// routes is how many routes the relay has.
func (ts *testServer) routes() int {
	ts.relay.mu.Lock()
	defer ts.relay.mu.Unlock()
	return len(ts.relay.routes)
}

// dial opens the session of the player with the token.
func dial(t *testing.T, ctx context.Context, ts *testServer, token string) *webtransport.Session {
	t.Helper()
	transport, sess, err := dialWebTransport(ctx, ts.target, playPath+token, ts.hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transport.Close() })
	return sess
}

// exchange sends a packet through the session and returns the stand-in game's answer: the address the game saw it
// from, and the packet it got.
func exchange(t *testing.T, ctx context.Context, sess *webtransport.Session, packet []byte) (string, []byte) {
	t.Helper()
	var nextID uint16
	if err := framePacket(packet, &nextID, sess.SendDatagram); err != nil {
		t.Fatal(err)
	}
	return answer(received(t, ctx, sess))
}

// received is the next packet the session gets.
func received(t *testing.T, ctx context.Context, sess *webtransport.Session) []byte {
	t.Helper()
	var r reassembly
	for {
		d, err := sess.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if p := r.receive(d); p != nil {
			return p
		}
	}
}

// answer is what the stand-in game's answer says: the address it saw the packet from, and the packet.
func answer(p []byte) (string, []byte) {
	from, got, _ := bytes.Cut(bytes.TrimPrefix(p, outOfBand), []byte("\n"))
	return string(from), got
}

// windowsPlayer is a player's UDP socket, as the Windows client's, sending to the relay's port.
func windowsPlayer(t *testing.T, target string) *net.UDPConn {
	t.Helper()
	to, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// udpExchange sends a packet from a Windows player and returns the stand-in game's answer, as exchange does.
func udpExchange(t *testing.T, conn *net.UDPConn, packet []byte) (string, []byte) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, maxPacket+100)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return answer(buf[:n])
}

func TestRelayCarriesPacketsBothWays(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := dial(t, ctx, ts, ts.seated(t, "A"))
	b := dial(t, ctx, ts, ts.seated(t, "B"))

	// fragmented both ways: the longest packet a client sends takes two datagrams
	var fromA string
	for _, size := range []int{12, maxFromPlayer} {
		packet := randomPacket(size)
		from, got := exchange(t, ctx, a, packet)
		if !bytes.Equal(got, packet) {
			t.Fatalf("%d bytes: the game got %d bytes", size, len(got))
		}
		if !strings.HasPrefix(from, "127.1.") {
			t.Fatalf("the game saw the player at %s, not at a loopback address of its own", from)
		}
		if fromA != "" && from != fromA {
			t.Fatalf("one player at two addresses: %s, %s", fromA, from)
		}
		fromA = from
	}
	fromB, _ := exchange(t, ctx, b, []byte("hello"))
	if ipA, _, _ := net.SplitHostPort(fromA); strings.HasPrefix(fromB, ipA+":") {
		t.Fatalf("two players at one address: %s, %s", fromA, fromB)
	}

	// a game's packets are longer, a gamestate's up to maxPacket; not that here, since this test client (webtransport-go)
	// holds no more than 32 datagrams it has not read yet (http3's streamDatagramQueueLen), and the 60 of the largest
	// packet arrive at once
	packet := randomPacket(30 * fragmentSize)
	seat, err := net.ResolveUDPAddr("udp4", fromA)
	if err != nil {
		t.Fatal(err)
	}
	ts.game.WriteToUDP(packet, seat)
	if got := received(t, ctx, a); !bytes.Equal(got, packet) {
		t.Fatalf("%d bytes from the game: the player got %d bytes", len(packet), len(got))
	}
}

func TestRelayAnswersProbesItself(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, sess, err := dialWebTransport(ctx, ts.target, probePath, ts.hash)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	var nextID uint16
	framePacket([]byte("\xff\xff\xff\xffgetchallenge"), &nextID, sess.SendDatagram)
	datagram, err := sess.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var r reassembly
	if got := string(r.receive(datagram)); got != "\xff\xff\xff\xfflobbyResponse 1 room, 1 game" {
		t.Errorf("the probe's answer: %q", got)
	}
	if strings.Contains(logged.String(), " joined") {
		t.Errorf("a probe logged:\n%s", logged.String())
	}
}

func TestRelayGivesAReturningPlayerTheirSeat(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	token := ts.seated(t, "A")
	transport, first, err := dialWebTransport(ctx, ts.target, playPath+token, ts.hash)
	if err != nil {
		t.Fatal(err)
	}
	at, _ := exchange(t, ctx, first, []byte("hello"))
	if !strings.HasSuffix(at, fmt.Sprintf(":%d", seatPort)) {
		t.Fatalf("the game saw the player at %s, not at the seats' port", at)
	}
	// a second session while the first is open (the page reloaded): the first ends
	again := dial(t, ctx, ts, token)
	if from, _ := exchange(t, ctx, again, []byte("hello")); from != at {
		t.Fatalf("the player came back at %s, not %s", from, at)
	}
	select {
	case <-first.Context().Done():
	case <-time.After(5 * time.Second):
		t.Error("the first session outlived the second's start")
	}
	transport.Close()
	if from, _ := exchange(t, ctx, again, []byte("hello")); from != at {
		t.Fatalf("the player moved to %s", from)
	}
}

func TestRelayNamesBrowserPlayersAsTheServerDoes(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	browser := dial(t, ctx, ts, ts.seated(t, "A"))

	// a page's client the player renamed in their browser, with a NAME besides, and userinfo enough to push a name at its
	// end past what the game reads of the line (1023 characters)
	long := strings.Repeat(`\cl_voice\1`, 100)
	_, got := exchange(t, ctx, browser,
		[]byte("\xff\xff\xff\xff"+`connect "\protocol\1044\name\Mallory\NAME\Mallory`+long+`"`))
	if want := "\xff\xff\xff\xff" + `connect "\name\A\protocol\1044` + long + `"`; string(got) != want {
		t.Errorf("the game got the connect %q", got)
	}
}

func TestRelayRefusesSessionsWithNoGame(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unseated := ts.lobby.hello("", "B")
	for _, path := range []string{"/", playPath, playPath + "nobody", playPath + unseated.Token} {
		if transport, _, err := dialWebTransport(ctx, ts.target, path, ts.hash); err == nil {
			transport.Close()
			t.Errorf("a session at %s", path)
		}
	}
}

func TestRelayLogsNetchanPacketsThatTakeMoreThanOneDatagram(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess := dial(t, ctx, ts, ts.seated(t, "A"))

	// the stand-in game answers with the packet and its address before it, so a packet that fits one datagram takes
	// two on the way back when it is nearly as long as a datagram; a client's voice takes two each way
	exchange(t, ctx, sess, append([]byte("\xff\xff\xff\xffv\x00"), randomPacket(maxFromPlayer-6)...))
	exchange(t, ctx, sess, randomPacket(500))
	if strings.Contains(logged.String(), " datagrams, not one ") {
		t.Fatalf("an out-of-band packet or a small one logged:\n%s", logged.String())
	}
	exchange(t, ctx, sess, randomPacket(maxDatagram-1))
	if n := strings.Count(logged.String(), " took 2 datagrams, not one "); n != 1 {
		t.Fatalf("a packet from the game that took two datagrams logged %d times:\n%s", n, logged.String())
	}
	exchange(t, ctx, sess, randomPacket(maxDatagram))
	exchange(t, ctx, sess, randomPacket(maxDatagram))
	if !strings.Contains(logged.String(), ": a netchan packet from the player took 2 datagrams, not one (1100 bytes)") ||
		strings.Count(logged.String(), " datagrams, not one ") != 2 {
		t.Fatalf("not once each way:\n%s", logged.String())
	}
}

func TestRelayRefusesAnotherCertificate(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if transport, _, err := dialWebTransport(ctx, ts.target, playPath+ts.seated(t, "A"), strings.Repeat("00", 32)); err == nil {
		transport.Close()
		t.Fatal("a session with the wrong certificate hash")
	}
}

func TestCertificateIsWhatBrowsersAccept(t *testing.T) {
	c := &certificates{}
	cert, hash, err := c.current()
	if err != nil {
		t.Fatal(err)
	}
	x, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if key, ok := x.PublicKey.(*ecdsa.PublicKey); !ok || key.Curve != elliptic.P256() {
		t.Errorf("not an ECDSA P-256 key")
	}
	if d := x.NotAfter.Sub(x.NotBefore); d > 14*24*time.Hour {
		t.Errorf("valid for %s, more than 14 days", d)
	}
	if !x.NotBefore.Before(time.Now()) || !x.NotAfter.After(time.Now().Add(certLifetime-2*time.Hour)) {
		t.Errorf("valid %s to %s", x.NotBefore, x.NotAfter)
	}
	if sum := sha256.Sum256(cert.Certificate[0]); hex.EncodeToString(sum[:]) != hash {
		t.Errorf("the hash is not the certificate's")
	}

	c.made = c.made.Add(-certRenew)
	if _, renewed, _ := c.current(); renewed == hash {
		t.Errorf("not renewed after a week")
	}
}

// getchallenge is a Windows client's first packet, with the player's key (bo1-zombies/src/client_mp/cl_main_mp.cpp: bo1_key)
func getchallenge(key string) []byte {
	return []byte("\xff\xff\xff\xff" + `getchallenge 0 "" "0" "` + key + `"`)
}

// udpAnswer sends a packet from a Windows player and returns the relay's own answer, out of band.
func udpAnswer(t *testing.T, conn *net.UDPConn, packet []byte) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write(packet)
	buf := make([]byte, 1000)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimPrefix(buf[:n], outOfBand))
}

func TestRelayCarriesWindowsPlayersPackets(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := windowsPlayer(t, ts.target), windowsPlayer(t, ts.target)
	aToken, bToken := ts.seated(t, "WinA"), ts.seated(t, "WinB")
	browser := dial(t, ctx, ts, ts.seated(t, "A"))

	// out of band first, as a client starts, with the player's key; then the connection's packets, from the same
	// address: their seat's
	first := getchallenge(ts.key(aToken))
	fromA, got := udpExchange(t, a, first)
	if !bytes.Equal(got, first) {
		t.Fatalf("the game got %q", got)
	}
	if fromA != ts.lobby.seatAddr(aToken) {
		t.Fatalf("the game saw the player at %s, not at their seat's address %s", fromA, ts.lobby.seatAddr(aToken))
	}
	for _, size := range []int{12, maxFromPlayer} {
		packet := randomPacket(size)
		packet[0] = 0 // not out of band
		from, got := udpExchange(t, a, packet)
		if !bytes.Equal(got, packet) {
			t.Fatalf("%d bytes: the game got %d bytes", size, len(got))
		}
		if from != fromA {
			t.Fatalf("one player at two addresses: %s, %s", fromA, from)
		}
	}
	// its connect names the player as the server does, whatever its own config says
	if _, got := udpExchange(t, a, []byte("\xff\xff\xff\xff"+`connect "\protocol\1044\name\Unknown Soldier\qport\4321"`)); string(got) != "\xff\xff\xff\xff"+`connect "\name\WinA\protocol\1044\qport\4321"` {
		t.Errorf("the game got the connect %q", got)
	}
	fromB, _ := udpExchange(t, b, getchallenge(ts.key(bToken)))
	fromBrowser, _ := exchange(t, ctx, browser, []byte("hello"))
	ips := map[string]bool{}
	for _, from := range []string{fromA, fromB, fromBrowser} {
		ip, _, _ := net.SplitHostPort(from)
		ips[ip] = true
	}
	if len(ips) != 3 {
		t.Fatalf("players share addresses: %s, %s, %s", fromA, fromB, fromBrowser)
	}
}

func TestRelayTellsWindowsPlayersWhyTheyDontJoin(t *testing.T) {
	ts := startRelay(t)
	l := ts.lobby
	host, other, nowhere := l.hello("", "C"), l.hello("", "C2"), l.hello("", "D")
	l.mu.Lock()
	l.create(l.playerOf(host.Token), "", true)
	idle := l.playerOf(host.Token).room.code
	l.join(l.playerOf(other.Token), idle)
	l.mu.Unlock()
	ts.seated(t, "F")
	gone := ts.seated(t, "E")
	// the game let E's character go (they were away for too long); F is still in it
	l.gameLine(l.rooms[ts.code], l.rooms[ts.code].game, "bo1_lobby: out "+l.seatAddr(gone))
	for key, want := range map[string]string{
		"":         "error\nPlay.bat needs your key. Copy the line from the server's page.",
		"0123abcd": "error\nThis key no longer works",
		// the client waits
		host.Key:     "print\nWaiting for the game to start...",
		nowhere.Key:  "print\nWaiting for you to join a room on the server's page...",
		ts.key(gone): "error\nYour character in room " + ts.code + "'s game is gone. You can join the next one.",
	} {
		if got := udpAnswer(t, windowsPlayer(t, ts.target), getchallenge(key)); !strings.HasPrefix(got, want) {
			t.Errorf("key %q: %q, want %q", key, got, want)
		}
	}
	if n := ts.routes(); n != 0 {
		t.Errorf("%d routes, for players who don't join", n)
	}
}

func TestRelayEndsQuietRoutes(t *testing.T) {
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	ts := startRelay(t, func(r *relay) {
		r.routeIdle, r.newRouteIdle, r.windowsQuiet = 2*time.Second, time.Second, 500*time.Millisecond
	})
	token := ts.seated(t, "W")
	key := ts.key(token)
	player := windowsPlayer(t, ts.target)
	first, _ := udpExchange(t, player, getchallenge(key))
	udpExchange(t, player, []byte("connection"))
	if !strings.Contains(logged.String(), " (W, room "+ts.code+") joined over UDP, as "+first) {
		t.Fatalf("the player's joining is not logged:\n%s", logged.String())
	}
	// in the game, their client asks no more: they play on Windows as long as their route has their seat
	ts.lobby.mu.Lock()
	ts.lobby.playerOf(token).client = time.Now().Add(-clientAsks)
	ts.lobby.mu.Unlock()
	if v := ts.lobby.viewOf(token); !v.You.Windows {
		t.Errorf("the player, whose route has their seat, isn't on Windows: %+v", v.You)
	}
	// nor are they away, though no page of theirs has been open for leaveAfter: they play (and stay in their room as
	// its game ends, for the next)
	away := func() time.Duration {
		ts.lobby.mu.Lock()
		defer ts.lobby.mu.Unlock()
		return ts.lobby.away(ts.lobby.playerOf(token))
	}
	ts.lobby.mu.Lock()
	ts.lobby.playerOf(token).gone = time.Now().Add(-leaveAfter)
	ts.lobby.mu.Unlock()
	if a := away(); a != 0 {
		t.Errorf("the player, whose route has their seat, is away (%v)", a)
	}
	// it goes quiet (the Windows game crashed, say): the route waits for them, but they are on Windows no more, and
	// their page may take the seat; they are away since they were last seen playing
	time.Sleep(700 * time.Millisecond)
	if v := ts.lobby.viewOf(token); v.You.Windows || ts.routes() != 1 {
		t.Errorf("the player, whose route has been quiet for windowsQuiet, is on Windows (%d routes): %+v", ts.routes(), v.You)
	}
	if a := away(); a == 0 || a >= leaveAfter {
		t.Errorf("the player, whose route has been quiet for windowsQuiet, has been away %v", a)
	}
	time.Sleep(2800 * time.Millisecond)
	if v := ts.lobby.viewOf(token); v.You.Windows {
		t.Errorf("the player, whose route ended, is still on Windows: %+v", v.You)
	}
	if !strings.Contains(logged.String(), " (W, room "+ts.code+") left (nothing from them for 2s)") || ts.routes() != 0 {
		t.Fatalf("the player's leaving is not logged:\n%s", logged.String())
	}
	// back, at their seat's address: the game knows them again
	if from, _ := udpExchange(t, player, getchallenge(key)); from != first {
		t.Fatalf("the player came back at %s, not at their seat's %s", from, first)
	}
}

func TestRelayEndsTheRouteOfAWindowsGameThatQuit(t *testing.T) {
	ts := startRelay(t)
	l := ts.lobby
	token := ts.seated(t, "W")
	player := windowsPlayer(t, ts.target)
	at, _ := udpExchange(t, player, getchallenge(ts.key(token)))
	quit := func() {
		l.mu.Lock()
		r := l.rooms[ts.code]
		l.mu.Unlock()
		l.gameLine(r, r.game, "bo1_lobby: quit "+at)
	}
	// the game says the client at the seat quit before it took this one on: a page's, which quit before it came
	quit()
	if n := ts.routes(); n != 1 {
		t.Fatalf("%d routes, once a client before this one quit", n)
	}
	udpExchange(t, player, []byte("connection")) // the game takes it on
	if v := l.viewOf(token); !v.You.Windows {
		t.Fatalf("the player isn't on Windows: %+v", v.You)
	}
	// the player closes the Windows game, whose client says disconnect, and the game says they quit: they are on
	// Windows no more, and their page offers to rejoin, to the character the game keeps for them
	quit()
	if n := ts.routes(); n != 0 {
		t.Errorf("%d routes, once the player's Windows game quit", n)
	}
	if v := l.viewOf(token); v.You.Windows || !v.Room.Seat || v.Room.Join || v.Room.Left || v.Room.State != "playing" {
		t.Errorf("the player, once their Windows game quit: %+v, %+v", v.You, v.Room)
	}
}

func TestRelayMovesASeatBetweenBrowserAndWindows(t *testing.T) {
	ts := startRelay(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	token := ts.seated(t, "A")
	at := ts.lobby.seatAddr(token)
	transport, sess, err := dialWebTransport(ctx, ts.target, playPath+token, ts.hash)
	if err != nil {
		t.Fatal(err)
	}
	if from, _ := exchange(t, ctx, sess, []byte("hello")); from != at {
		t.Fatalf("the browser at %s, not its seat's %s", from, at)
	}
	// from Windows while the browser's session has the seat: the client waits for it to end
	player := windowsPlayer(t, ts.target)
	if got := udpAnswer(t, player, getchallenge(ts.key(token))); !strings.HasPrefix(got, "print\nYour game in a browser has your seat") {
		t.Fatalf("Windows while the browser has the seat: %q", got)
	}
	sess.CloseWithError(0, "the page closed")
	defer transport.Close()
	waitFor(t, "the session's end", func() bool {
		ts.relay.mu.Lock()
		defer ts.relay.mu.Unlock()
		return ts.relay.seats[at] == nil
	})
	if from, _ := udpExchange(t, player, getchallenge(ts.key(token))); from != at {
		t.Fatalf("Windows at %s, not the seat's %s", from, at)
	}
	// in a browser again: the route ends
	again := dial(t, ctx, ts, token)
	if from, _ := exchange(t, ctx, again, []byte("hello")); from != at {
		t.Fatalf("the browser again at %s, not the seat's %s", from, at)
	}
	if n := ts.routes(); n != 0 {
		t.Errorf("the route outlived the session's start")
	}
}

func TestRelayRoutesToTheRoomsNextGame(t *testing.T) {
	ts := startRelay(t)
	l := ts.lobby
	token := ts.seated(t, "W")
	player := windowsPlayer(t, ts.target)
	first, _ := udpExchange(t, player, getchallenge(ts.key(token)))
	// the room's game ends, and its seats' routes with it; another game may have its port next
	l.mu.Lock()
	r := l.rooms[ts.code]
	for _, s := range r.game.seats {
		l.relay.release(s.local)
	}
	r.game = nil
	l.mu.Unlock()
	ts.game.Close()
	if n := ts.routes(); n != 0 {
		t.Fatalf("%d routes outlived their game", n)
	}
	// the player's client, from the same address, waits for the room's next game
	if got := udpAnswer(t, player, getchallenge(ts.key(token))); got != "print\nWaiting for the game to start..." {
		t.Fatalf("the player's client, between games: %q", got)
	}
	// which starts, on a port of its own: the route goes there, from the player's new seat
	next := standInGame(t)
	l.mu.Lock()
	local := l.relay.acquire()
	local.Port = seatPort
	r.game = &roomGame{title: l.title(r.mapName), port: next.LocalAddr().(*net.UDPAddr).Port, started: true, began: time.Now(), cancel: func() {},
		seats: []*seat{{player: l.playerOf(token), local: local}}}
	l.mu.Unlock()
	if from, _ := udpExchange(t, player, getchallenge(ts.key(token))); from != local.String() || from == first {
		t.Fatalf("the room's next game saw the player at %s, not at their new seat's %s", from, local)
	}
}

func TestRelayLeavesTheRestToQUIC(t *testing.T) {
	ts := startRelay(t)
	player := windowsPlayer(t, ts.target)
	// not out of band, from an address with no route: no client's first packet, so QUIC's, which drops it
	player.Write([]byte("\x01\x00\x00\x00connection"))
	player.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 100)
	if n, err := player.Read(buf); err == nil {
		t.Fatalf("an answer: %q", buf[:n])
	}
}
