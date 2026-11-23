package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

// The relay connects the players to the games. Players reach the games at the server's UDP port. The relay passes
// each of a player's packets on to their room's game (rooms.go) from their seat's address, as if the player had sent
// it from there, and passes the game's answers back.
//
// Players in a browser send WebTransport datagrams (HTTP/3 over QUIC), in a session at /play/<token>. The token is
// the one the server gave the player (bo1-zombies/web/src/library_bo1.js). Windows players send the game's own
// packets to the same port. Each of them has a route (route) to the seat of the player whose key their getchallenge
// carries (Play.bat <host> <key>). The games have no part in the relay: they see ordinary UDP clients.
//
// Each seat has its own loopback address (127.1.x.y), so a game sees every player at a different address, as over the
// internet. A game identifies its clients by address and qport (SV_FindClientByAddress), and its status lists them
// separately. A seat keeps its address and its port (seatPort) for every visit of its player, from a browser or from
// Windows, and the game recognises the player by them. A seat has one session or one route at a time. A game gets
// only the packets that a client sends it (filter.go). Each player's connect names them as the server does (named),
// whatever name their client sent.
//
// A T6 room's game (t6.go) is Plutonium's server, and its players' games are Plutonium's clients, which send no key.
// A route to a T6 game starts with any out-of-band packet from the address a player's page calls from (lobby.t6Seat).
// The route's packets go on unchanged, except those for the game's remote console, which the relay drops. The
// client's connect waits at the relay until it is the client's turn to join (holds): the players join a T6 game in
// the order of their places in the room.
//
// A session at /probe belongs to zombies-server probe (probe.go). The relay answers it itself.

// probePath is the path of the session that zombies-server probe opens (probe.go). The relay does not log these
// sessions, because Docker's health check opens one every 30 seconds (docker/Dockerfile).
const probePath = "/probe"

// playPath is the start of the path of a player's session: /play/<token>
const playPath = "/play/"

// outOfBand is the start of a packet outside a connection (getchallenge, connect, getstatus, ...). A client's first
// packet starts with it. No QUIC client sends a packet that starts with it: read as QUIC, such a packet would be a
// server's Retry.
var outOfBand = []byte("\xff\xff\xff\xff")

type relay struct {
	lobby  *lobby // finds the seat that a player's session or route goes to
	server *webtransport.Server
	conn   *net.UDPConn // the server's UDP port, which routes answer their players from

	// A route ends after routeIdle without a packet from its player. The game lets the player's character go after
	// the same time (keepCharacter). Until the game has accepted the player (route.taken), the route ends after
	// newRouteIdle instead, because a client that is connecting sends something every 3 seconds.
	routeIdle, newRouteIdle time.Duration
	// When a route's player has sent nothing for windowsQuiet, their Windows game is taken to be gone (crashed or cut
	// off). The route still waits for them for routeIdle, because a client that loads a map sends nothing for a while.
	// A client that quits, from its menu or when its window closes, notifies the game, and its route ends then (quit).
	windowsQuiet time.Duration
	lag          atomic.Int64 // the ping the relay adds to every player's ping, in nanoseconds (lag.go)

	mu     sync.Mutex
	inUse  map[uint16]bool // loopback addresses held by seats: 127.1.hi.lo
	next   uint16
	routes map[netip.AddrPort]*route // by the player's address
	seats  map[string]*seatSession   // the open session of each seat, by the seat's address
	closed chan struct{}
	// wake tells sweep that a T6 client whose connect waits for its turn may have it now (wakeUp, letIn).
	wake chan struct{}
	// asks is how long after a T6 client's connect the client still counts as asking to connect. A client sends its
	// connect every 3 seconds until the game answers, so this is two of them and a second.
	asks time.Duration
}

// seatSession is a browser player's session, which goes from their seat's address to their room's game.
type seatSession struct {
	sess *webtransport.Session
	done chan struct{} // closed once the session has ended and the seat's address is free
}

// route is a Windows player's route. The player's packets reach the server's UDP port from the player's address. The
// relay passes them on to their room's game from their seat's address, and passes the game's answers back, as it does
// for a WebTransport session. The route starts with the player's getchallenge, which carries their key. It ends when
// the player has sent nothing for a while (relay.routeIdle), when the seat's game ends, or when a getchallenge from
// the player leads to another seat or to none.
type route struct {
	name   string // the player's name, which the game is to know them by
	who    string // who the player is, for the log
	player *net.UDPAddr
	local  *net.UDPAddr // the address of the player's seat
	game   *net.UDPConn // the connection to the game, from local
	last   atomic.Int64 // when the player last sent a packet (Unix nanoseconds)
	taken  atomic.Bool  // the game accepted the player: it sent them more than out-of-band packets
	t6     bool         // a T6 client's route, to Plutonium's server (t6.go): its packets go on unchanged

	// A T6 client's connect waits at the relay for the client's turn to join its game (relay.holds). relay.mu guards
	// held, asked and passed.
	held    []byte     // the connect that waits, or nil
	asked   time.Time  // when the client last sent a connect, the game not having accepted it yet
	passed  time.Time  // when the relay first passed a connect of the client's on to the game
	sending sync.Mutex // held to put a T6 client's packet on toGame, which the relay's reader and letIn both do

	toGame, toPlayer *lagLine           // the route's packets each way, after the relay's lag (lag.go)
	end              context.CancelFunc // stops toGame and toPlayer when the route ends
	filter           *playerFilter      // which packets go on; used only by relay.route, on QUIC's one reading goroutine
}

func newRelay(l *lobby, certs *certificates) *relay {
	r := &relay{lobby: l, inUse: map[uint16]bool{}, routes: map[netip.AddrPort]*route{}, seats: map[string]*seatSession{},
		closed: make(chan struct{}), wake: make(chan struct{}, 1), asks: 7 * time.Second, routeIdle: keepCharacter,
		newRouteIdle: 10 * time.Second, windowsQuiet: time.Minute}
	r.server = &webtransport.Server{
		H3: &http3.Server{
			TLSConfig: http3.ConfigureTLSConfig(&tls.Config{GetCertificate: certs.getCertificate}),
			QUICConfig: &quic.Config{
				MaxIdleTimeout:  60 * time.Second,
				KeepAlivePeriod: 10 * time.Second, // a client loading a map sends nothing for a while
			},
			Handler: http.HandlerFunc(r.serveSession),
		},
		// The page's origin does not decide who may play: Windows players reach the game without an origin.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	return r
}

// serve serves WebTransport sessions and Windows players on conn until close is called.
func (r *relay) serve(conn *net.UDPConn) error {
	r.conn = conn
	go r.sweep()
	err := r.server.Serve(&quicPort{PacketConn: conn, conn: conn, relay: r, buf: make([]byte, maxPacket+1)})
	if errors.Is(err, context.Canceled) || errors.Is(err, quic.ErrServerClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// close ends every session and route, and stops serving.
func (r *relay) close() error {
	err := r.server.Close()
	r.mu.Lock()
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	for _, rt := range r.routes {
		r.removeRoute(rt)
	}
	r.mu.Unlock()
	return err
}

// quicPort is the server's UDP port as QUIC reads it. Windows players' packets go to their routes (relay.route), and
// QUIC reads the rest. quicPort has only net.PacketConn's methods and the two that set the buffers, so that QUIC
// reads through ReadFrom. Given the socket itself, QUIC would read from it without ReadFrom.
type quicPort struct {
	net.PacketConn
	conn  *net.UDPConn
	relay *relay
	buf   []byte
}

func (p *quicPort) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, from, err := p.conn.ReadFromUDP(p.buf)
		if err != nil {
			return 0, nil, err
		}
		if !p.relay.route(p.buf[:n], from) {
			return copy(b, p.buf[:n]), from, nil
		}
	}
}

func (p *quicPort) SetReadBuffer(n int) error  { return p.conn.SetReadBuffer(n) }
func (p *quicPort) SetWriteBuffer(n int) error { return p.conn.SetWriteBuffer(n) }

func (r *relay) serveSession(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodConnect {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch {
	case req.URL.Path == probePath:
		r.serveProbe(w, req)
	case strings.HasPrefix(req.URL.Path, playPath):
		r.servePlayer(w, req, strings.TrimPrefix(req.URL.Path, playPath))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// servePlayer relays a browser player's session to their room's game, from their seat's address. A session that the
// seat still has open ends first (the player's page reloaded, for example). So does a route to the seat (the player
// played on Windows).
func (r *relay) servePlayer(w http.ResponseWriter, req *http.Request, token string) {
	game, local, name, who, err := r.lobby.session(token)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key := local.String()
	r.mu.Lock()
	if rt := r.routeAt(key); rt != nil {
		r.removeRoute(rt)
	}
	last := r.seats[key]
	r.mu.Unlock()
	if last != nil {
		last.sess.CloseWithError(0, "the player joined again")
		select {
		case <-last.done:
		case <-time.After(5 * time.Second):
		}
	}
	conn, err := net.DialUDP("udp4", local, game)
	if err != nil {
		log.Printf("relay: %s (%s): %v", req.RemoteAddr, who, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	// A burst from the game (a gamestate is up to 64 KB, 61 datagrams) waits in this buffer while the session sends.
	conn.SetReadBuffer(1 << 20)
	sess, err := r.server.Upgrade(w, req)
	if err != nil {
		log.Printf("relay: %s (%s): %v", req.RemoteAddr, who, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	this := &seatSession{sess: sess, done: make(chan struct{})}
	r.mu.Lock()
	r.seats[key] = this
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.seats[key] == this {
			delete(r.seats, key)
		}
		r.mu.Unlock()
		close(this.done)
	}()

	log.Printf("relay: %s (%s) joined, as %s", req.RemoteAddr, who, local)
	done := make(chan struct{})
	go func() {
		r.toPlayer(sess, conn)
		close(done)
	}()
	err = r.toGame(sess, conn, name, newPlayerFilter(fmt.Sprintf("%s (%s)", req.RemoteAddr, who)))
	conn.Close()
	<-done
	log.Printf("relay: %s (%s) left (%v)", req.RemoteAddr, who, err)
}

// serveProbe answers the packets of zombies-server probe itself, with an out-of-band "lobbyResponse" and a summary of
// what the server holds (lobby.summary).
func (r *relay) serveProbe(w http.ResponseWriter, req *http.Request) {
	sess, err := r.server.Upgrade(w, req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var reasm reassembly
	var nextID uint16
	for {
		datagram, err := sess.ReceiveDatagram(sess.Context())
		if err != nil {
			return
		}
		if packet := reasm.receive(datagram); packet != nil {
			answer := append(append([]byte{}, outOfBand...), "lobbyResponse "+r.lobby.summary()...)
			framePacket(answer, &nextID, sess.SendDatagram)
		}
	}
}

// toGame sends the game the player's packets that filter passes, until the session ends. It returns why the session
// ended.
func (r *relay) toGame(sess *webtransport.Session, conn *net.UDPConn, name string, filter *playerFilter) error {
	var reasm reassembly
	split := false
	stop := make(chan struct{})
	line := newLagLine(func(packet []byte) {
		conn.Write(packet) // refused while the game is not running, as over UDP
	}, stop)
	defer func() {
		close(stop)
		<-line.ended // what the player sent, such as their disconnect, reaches the game before servePlayer closes conn
	}()
	for {
		datagram, err := sess.ReceiveDatagram(sess.Context())
		if err != nil {
			return err
		}
		if packet := reasm.receive(datagram); packet != nil && filter.pass(packet, time.Now()) {
			logSplit(sess, "the player", packet, &split)
			line.put(named(packet, name), r.oneWay())
		}
	}
}

// toPlayer sends the player the game's packets until conn is closed or the session ends.
func (r *relay) toPlayer(sess *webtransport.Session, conn *net.UDPConn) {
	buf := make([]byte, maxPacket+1)
	var nextID uint16
	tooLarge, split := false, false
	line := newLagLine(func(packet []byte) {
		err := framePacket(packet, &nextID, sess.SendDatagram)
		if _, ok := errors.AsType[*quic.DatagramTooLargeError](err); ok && !tooLarge && sess.Context().Err() == nil {
			// The packet is lost, as on a path that drops such datagrams. The player keeps the session.
			log.Printf("relay: %s: the path takes smaller datagrams than %d bytes: %v", sess.RemoteAddr(), maxDatagram, err)
			tooLarge = true
		}
	}, sess.Context().Done())
	for {
		n, err := conn.Read(buf)
		if errors.Is(err, syscall.ECONNREFUSED) {
			continue // the game was not running when the player last sent something
		}
		if err != nil {
			return
		}
		logSplit(sess, "the game", buf[:n], &split)
		line.put(buf[:n], r.oneWay())
		if sess.Context().Err() != nil {
			return
		}
	}
}

// logSplit logs a netchan packet that takes more than one datagram. It logs only the first such packet of a session
// in each direction. The game makes netchan packets to fit one datagram (MAX_PACKETLEN in
// bo1-zombies/src/qcommon/net_chan_mp.h), so that a player loses a packet only when its one datagram is lost.
// Out-of-band packets may take more.
func logSplit(sess *webtransport.Session, from string, packet []byte, logged *bool) {
	if *logged || datagrams(len(packet)) == 1 || bytes.HasPrefix(packet, outOfBand) {
		return
	}
	log.Printf("relay: %s: a netchan packet from %s took %d datagrams, not one (%d bytes)", sess.RemoteAddr(), from,
		datagrams(len(packet)), len(packet))
	*logged = true
}

// route handles a packet that reached the server's UDP port, when the packet is a Windows player's: a packet from the
// address of a route, or an out-of-band packet, such as the getchallenge that starts a route. route passes the packet
// on to the player's game or drops it, and returns true. It returns false for any other packet, which QUIC then
// reads.
func (r *relay) route(packet []byte, from *net.UDPAddr) bool {
	r.mu.Lock()
	rt := r.routes[from.AddrPort()]
	r.mu.Unlock()
	oob := bytes.HasPrefix(packet, outOfBand)
	if !oob && rt == nil {
		return false
	}
	if rt == nil && !r.knocks(packet) {
		rt = r.newT6Route(packet, from) // a T6 client's first packet, when its address is a player's; otherwise nil
	}
	if rt != nil && rt.t6 {
		if rt.filter.pass(packet, time.Now()) {
			rt.last.Store(time.Now().UnixNano())
			if !oob || !r.holds(rt, packet) {
				rt.sending.Lock()
				rt.toGame.put(packet, r.oneWay())
				rt.sending.Unlock()
			}
		}
		return true
	}
	if rt == nil && !clientSends(packet) || rt != nil && !rt.filter.pass(packet, time.Now()) {
		return true // dropped: no client sends this packet, or no client sends this many
	}
	if oob {
		args := oobArgs(packet)
		if len(args) > 0 && strings.EqualFold(args[0], "getchallenge") {
			// A client is connecting. It goes to the seat its key has now, which need not be the seat of its route
			// (the key's seat can be in the room's next game).
			rt = r.newRoute(args, from, rt)
		}
		if rt == nil {
			return true // dropped: the packet is not from a client that joins a game, or the client can't join yet
		}
		packet = named(packet, rt.name)
	}
	rt.last.Store(time.Now().UnixNano())
	rt.toGame.put(packet, r.oneWay())
	return true
}

// knocks reports whether a packet from an address with no route is a T5 client's packet that starts one: a
// getchallenge with a key the server knows (newRoute).
func (r *relay) knocks(packet []byte) bool {
	args := oobArgs(packet)
	return len(args) > 4 && strings.EqualFold(args[0], "getchallenge") && r.lobby.knowsKey(args[4])
}

// newT6Route opens the route for a T6 client at from, which sent packet and has no route. The route goes to the seat,
// in a T6 room's game, of a player whose page calls from the client's address (lobby.t6Seat). newT6Route returns nil
// when there is no such seat, or when packet is not one that a T6 client may send (t6ClientSends).
func (r *relay) newT6Route(packet []byte, from *net.UDPAddr) *route {
	if !t6ClientSends(packet) {
		return nil
	}
	verb := ""
	if args := oobArgs(packet); len(args) > 0 {
		verb = args[0]
	}
	game, local, name, who, ok := r.lobby.t6Seat(from.AddrPort().Addr().Unmap(), verb)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.open(from, game, local, name, who)
	if rt != nil {
		rt.t6, rt.filter.t6 = true, true
		log.Printf("relay: %s (%s) asks to join over UDP, from a T6 game", from, who)
	}
	return rt
}

// holds reports whether a T6 client's packet waits at the relay instead of going on to the game. That is the client's
// connect, while it is not the client's turn to join (lobby.t6Turn). The relay keeps the last connect that waits and
// passes it on when the turn comes (letIn), so the client does not wait for the next one it sends. A client sends its
// connect every 3 seconds until the game answers, and waits far longer than any turn takes (t6.go: the order the
// players join in). The connect of a client that the game has accepted goes on at once.
func (r *relay) holds(rt *route, packet []byte) bool {
	name, connect := t6Connect(packet)
	if !connect || rt.taken.Load() {
		return false
	}
	now := time.Now()
	r.mu.Lock()
	rt.asked = now
	r.mu.Unlock()
	turn, who := r.lobby.t6Turn(rt.local, name) // takes the lobby's lock, which is taken before the relay's
	if who == "" {
		who = rt.who
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if turn {
		if rt.held != nil {
			log.Printf("relay: %s (%s) joins, in their turn", rt.player, who)
		}
		if rt.held = nil; rt.passed.IsZero() {
			rt.passed = now
		}
		return false
	}
	if rt.held == nil {
		log.Printf("relay: %s (%s) waits to join until the players before them in their room have", rt.player, who)
	}
	rt.held = slices.Clone(packet) // the reader's buffer takes the next packet
	return true
}

// letIn passes on the connect of each T6 client that waited for its turn to join and has it now (holds). sweep calls
// it each second, and at once when a player before one of them has joined (wakeUp).
//
// A connect whose client has sent no other for asks is dropped, and not passed on. The client has stopped asking:
// it went back to its menu, or its packets no longer reach the server. The game would take the connect for a client
// all the same, and keep a slot for it until it timed out, a minute later.
func (r *relay) letIn() {
	r.mu.Lock()
	var waiting []*route
	for _, rt := range r.routes {
		if rt.held != nil {
			waiting = append(waiting, rt)
		}
	}
	r.mu.Unlock()
	for _, rt := range waiting {
		r.mu.Lock()
		name, _ := t6Connect(rt.held)
		gone := rt.held != nil && time.Since(rt.asked) >= r.asks
		if gone {
			rt.held = nil
		}
		r.mu.Unlock()
		if gone {
			log.Printf("relay: %s (%s) asks to join no more: the connect that waited for their turn is dropped", rt.player, rt.who)
			continue
		}
		turn, who := r.lobby.t6Turn(rt.local, name)
		if !turn {
			continue
		}
		if who == "" {
			who = rt.who
		}
		r.mu.Lock()
		packet := rt.held // nil when the client's next connect came meanwhile, and went on
		if rt.held = nil; packet != nil && rt.passed.IsZero() {
			rt.passed = time.Now()
		}
		r.mu.Unlock()
		if packet != nil {
			log.Printf("relay: %s (%s) joins, in their turn", rt.player, who)
			rt.sending.Lock()
			rt.toGame.put(packet, r.oneWay())
			rt.sending.Unlock()
		}
	}
}

// wakeUp has sweep call letIn now: a player has joined a T6 game, and the next one's turn may have come.
func (r *relay) wakeUp() {
	select {
	case r.wake <- struct{}{}:
	default: // sweep is already told
	}
}

// door reports how the T6 client of the seat at local stands at its game's door. taken: the game has accepted it
// (route.taken). asking: it asks to connect, which is that it sent a connect within asks. coming: it asks, and it
// waits for its own turn, or the relay passed its connect on within asks, and the game is about to take it. All are
// false for a seat with no route.
func (r *relay) door(local *net.UDPAddr) (taken, asking, coming bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.routeAt(local.String())
	if rt == nil {
		return false, false, false
	}
	asking = time.Since(rt.asked) < r.asks
	return rt.taken.Load(), asking, asking && (rt.passed.IsZero() || time.Since(rt.passed) < r.asks)
}

// newRoute returns the route for the player at from, who sent a getchallenge (args). The getchallenge carries the
// player's key after the arguments that the game reads (bo1-zombies/src/client_mp/cl_main_mp.cpp, bo1_key). The route
// goes to the player's seat in their room's game (lobby.windowsSeat). current is the route the player has, or nil.
// newRoute returns current when it already goes to that seat, and ends it otherwise.
//
// newRoute returns nil when the key has no seat. It then sends the player the reason, or what their client is waiting
// for. It also returns nil while a browser session has the seat: the player's page leaves the game to the Windows
// client once the page is notified of it, and the client asks again in 3 seconds. It returns nil when the relay is
// closed.
func (r *relay) newRoute(args []string, from *net.UDPAddr, current *route) *route {
	key := ""
	if len(args) > 4 {
		key = args[4]
	}
	game, local, name, who, err := r.lobby.windowsSeat(key)
	if err != nil {
		if current != nil {
			r.mu.Lock()
			r.removeRoute(current)
			r.mu.Unlock()
		}
		answer := "error\n" + err.Error()
		if _, ok := errors.AsType[waiting](err); ok {
			answer = "print\n" + err.Error() // the client keeps waiting (CL_ConnectionlessPacket)
		}
		r.conn.WriteToUDP(append(append([]byte{}, outOfBand...), answer...), from)
		return nil
	}
	seat := local.String()
	r.mu.Lock()
	defer r.mu.Unlock()
	if current != nil && current.local.String() == seat {
		return current
	}
	if current != nil {
		r.removeRoute(current)
	}
	if r.seats[seat] != nil {
		r.conn.WriteToUDP(append(append([]byte{}, outOfBand...),
			"print\nYour game in a browser has your seat: this one takes it as the browser leaves the game..."...), from)
		return nil
	}
	return r.open(from, game, local, name, who)
}

// open makes a new route for the player at from, to their seat at local in the game at game. Another route to the
// seat ends. open returns nil when the relay is closed, or when it can't open the connection to the game. The caller
// holds r.mu.
func (r *relay) open(from, game, local *net.UDPAddr, name, who string) *route {
	if other := r.routeAt(local.String()); other != nil {
		r.removeRoute(other) // the player came back from another address
	}
	select {
	case <-r.closed:
		return nil
	default:
	}
	conn, err := net.DialUDP("udp4", local, game)
	if err != nil {
		log.Printf("relay: %s (%s): %v", from, who, err)
		return nil
	}
	rt := &route{name: name, who: who, player: from, local: local, game: conn,
		filter: newPlayerFilter(fmt.Sprintf("%s (%s)", from, who))}
	var held context.Context
	held, rt.end = context.WithCancel(context.Background())
	rt.toGame = newLagLine(func(packet []byte) {
		conn.Write(packet) // refused while the game is not running, as over UDP
	}, held.Done())
	rt.toPlayer = newLagLine(func(packet []byte) { r.conn.WriteToUDP(packet, from) }, held.Done())
	rt.last.Store(time.Now().UnixNano())
	r.routes[from.AddrPort()] = rt
	go r.toWindowsPlayer(rt)
	return rt
}

// connected reports how the seat at local is connected. session is true when a browser's session is open for the
// seat. windows is true when a Windows player's route goes to the seat, and the player has sent something in the last
// windowsQuiet.
func (r *relay) connected(local *net.UDPAddr) (session, windows bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.routeAt(local.String())
	return r.seats[local.String()] != nil, rt != nil && time.Since(time.Unix(0, rt.last.Load())) < r.windowsQuiet
}

// routeQuiet reports whether a route goes to the seat at local, and for how long the route's player has sent nothing.
func (r *relay) routeQuiet(local *net.UDPAddr) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rt := r.routeAt(local.String())
	if rt == nil {
		return false, 0
	}
	return true, time.Since(time.Unix(0, rt.last.Load()))
}

// end ends the route to the seat at local, if the seat has one. It is for a seat whose player left a T6 game for good
// (lobby.leaveGame).
func (r *relay) end(local *net.UDPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.routeAt(local.String()); rt != nil {
		r.removeRoute(rt)
	}
}

// quit ends the route to the seat at local, because the client of the seat's player quit the game (lobby.quitGame).
// The server then no longer counts the player as playing on Windows. quit reports whether it ended a route. It does
// not end a route whose player the game has not accepted yet: that is a client on its way into the seat, after the
// player's page quit.
func (r *relay) quit(local *net.UDPAddr) bool {
	r.mu.Lock()
	rt := r.routeAt(local.String())
	if rt == nil || !rt.taken.Load() {
		r.mu.Unlock()
		return false
	}
	r.removeRoute(rt)
	r.mu.Unlock()
	log.Printf("relay: %s (%s) left (they quit the game)", rt.player, rt.who)
	return true
}

// routeAt returns the route that goes to the seat at the given address, or nil. The caller holds r.mu.
func (r *relay) routeAt(seat string) *route {
	for _, rt := range r.routes {
		if rt.local.String() == seat {
			return rt
		}
	}
	return nil
}

// named returns a player's packet as their seat's game gets it. A connect is changed to name the player as the server
// does (withName). Any other packet is returned as it is. A page's client already has that name (web/src/game.ts:
// joinGame), but it runs in the player's browser, where the player can rename it. A Windows client has the name in its
// own config, and another player in the room may have the same name (rooms.go: uniqueName). The game keeps the name
// its client connected with (SV_UpdateUserinfo_f).
func named(packet []byte, name string) []byte {
	if !bytes.HasPrefix(packet, outOfBand) {
		return packet
	}
	if args := oobArgs(packet); len(args) > 1 && strings.EqualFold(args[0], "connect") {
		return withName(args[1], name)
	}
	return packet
}

// withName returns a client's connect with its userinfo (info: "\protocol\1044\name\...") changed to name the
// player. The player's name comes first, so that no userinfo, however long, pushes it past the part of the line that
// the game reads (1023 characters). Every name key of the client's is removed, whatever its letter case, because the
// game reads keys without regard to case.
func withName(info, name string) []byte {
	parts := strings.Split(strings.TrimPrefix(info, `\`), `\`)
	info = `\name\` + name
	for i := 0; i+1 < len(parts); i += 2 {
		if !strings.EqualFold(parts[i], "name") {
			info += `\` + parts[i] + `\` + parts[i+1]
		}
	}
	return append(append([]byte{}, outOfBand...), `connect "`+info+`"`...)
}

// oobArgs returns the words of an out-of-band packet as the game reads them: separated by spaces, or quoted.
func oobArgs(packet []byte) []string {
	text := string(bytes.TrimPrefix(packet, outOfBand))
	if i := strings.IndexAny(text, "\n\x00"); i >= 0 {
		text = text[:i]
	}
	var args []string
	for {
		text = strings.TrimLeft(text, " \t")
		if text == "" {
			return args
		}
		if text[0] == '"' {
			arg, rest, _ := strings.Cut(text[1:], `"`)
			args = append(args, arg)
			text = rest
			continue
		}
		end := strings.IndexAny(text, " \t")
		if end < 0 {
			end = len(text)
		}
		args = append(args, text[:end])
		text = text[end:]
	}
}

// toWindowsPlayer sends a route's player the game's packets until the route ends.
func (r *relay) toWindowsPlayer(rt *route) {
	buf := make([]byte, maxPacket+1)
	for {
		n, err := rt.game.Read(buf)
		if errors.Is(err, syscall.ECONNREFUSED) {
			continue // the game was not running when the player last sent something
		}
		if err != nil {
			return
		}
		if !rt.taken.Load() && !bytes.HasPrefix(buf[:n], outOfBand) {
			rt.taken.Store(true)
			log.Printf("relay: %s (%s) joined over UDP, as %s", rt.player, rt.who, rt.local)
			if rt.t6 {
				r.wakeUp() // the next player's turn to join may have come (letIn)
			}
		}
		rt.toPlayer.put(buf[:n], r.oneWay())
	}
}

// sweep ends the routes whose players have sent nothing for too long (routeIdle, newRouteIdle). It checks every
// second until the relay closes. It also lets in the T6 clients whose turn to join has come (letIn): every second,
// and whenever it is told that one may have (wakeUp).
func (r *relay) sweep() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-r.wake:
			r.letIn()
			continue
		case <-tick.C:
		}
		r.letIn()
		var left []string
		r.mu.Lock()
		for _, rt := range r.routes {
			limit := r.newRouteIdle
			if rt.taken.Load() {
				limit = r.routeIdle
			}
			if time.Since(time.Unix(0, rt.last.Load())) >= limit {
				r.removeRoute(rt)
				if rt.taken.Load() {
					left = append(left, fmt.Sprintf("relay: %s (%s) left (nothing from them for %s)", rt.player, rt.who, limit))
				}
			}
		}
		r.mu.Unlock()
		for _, line := range left {
			log.Print(line)
		}
	}
}

// removeRoute ends a route. The route's seat keeps its address. The caller holds r.mu.
func (r *relay) removeRoute(rt *route) {
	if r.routes[rt.player.AddrPort()] == rt {
		delete(r.routes, rt.player.AddrPort())
	}
	rt.end()
	rt.game.Close()
}

// acquire returns a loopback address for a seat, one that no other seat holds. It goes through 127.1.0.1 to
// 127.1.255.254 in turn. There is always a free address: only the running games have seats, and main.go limits the
// games so that every seat can have one (-max-games).
func (r *relay) acquire() *net.UDPAddr {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		r.next++
		if r.next != 0 && r.next != 0xFFFF && !r.inUse[r.next] {
			break
		}
	}
	r.inUse[r.next] = true
	return &net.UDPAddr{IP: net.IPv4(127, 1, byte(r.next>>8), byte(r.next))}
}

// release frees a seat's address when the seat's game ends. The seat's route ends with it, so that a getchallenge
// from its player goes to their room's next game, and not to the port of this game, which another game may have by
// then. A session is not ended here. It ends when its page goes back to the room, once the game's last packets have
// reached the page.
func (r *relay) release(local *net.UDPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.routeAt(local.String()); rt != nil {
		r.removeRoute(rt)
	}
	ip := local.IP.To4()
	delete(r.inUse, uint16(ip[2])<<8|uint16(ip[3]))
}
