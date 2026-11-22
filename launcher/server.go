package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zombies/internal/launcherapi"
)

// server is the server the launcher plays on. The launcher calls it with the player's key (launcherapi).
//
// watch keeps one call open that asks the server what to play. The rest of the launcher reads the server's last
// answer (latest) and makes no such call itself. While the launcher rests (rest), no call is open, and the launcher
// sends the server nothing.
type server struct {
	base, key string
	client    *http.Client
	// ready records that the PC has what the game needs. Each call that asks what to play sends it (setReady).
	ready atomic.Bool
	// poll is how long watch waits between two calls to a server that does not hold calls. Such a server answers at
	// once, with no version (launcherapi.Play.Version).
	poll time.Duration
	// retry is how long watch waits before it tries a failed call again. The wait doubles with each failure in a
	// row, up to a minute.
	retry time.Duration
	// start starts watch, once. The launcher calls it when the player has agreed to play on this server (trust).
	// Until then the launcher sends the server nothing. started is set once watch runs.
	start   func()
	started atomic.Bool

	mu   sync.Mutex
	play *launcherapi.Play // the server's last answer, or nil before the first
	err  error             // why the last call failed, or nil
	next chan struct{}     // closed when there is another answer or error, and then replaced
	// renew ends the call that watch has open, or its wait before the next call, so that watch asks again at once.
	renew func()
	// failed is what the launcher's last try failed at, as the server was told (fail), or "".
	failed string
	// resting is set while the launcher has no call open (rest). wake is closed when watch is to ask again (resume).
	resting bool
	wake    chan struct{}
}

// askLimit is how long a call that asks what to play may take. The server holds such a call for up to 45 seconds
// (server/launcher.go: launcherHold).
const askLimit = 90 * time.Second

func newServer(l link, client *http.Client) *server {
	return &server{base: l.base, key: l.key, client: client, poll: 5 * time.Second, retry: time.Second,
		next: make(chan struct{})}
}

// newClient returns the HTTP client for the server and for Plutonium's updater. It sets no time limit on a whole
// answer, because a file takes as long as it takes. A call sets its own limit (get), and a download gives up when
// nothing arrives (sync.go).
func newClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 15 * time.Minute, // the server's first list of its game files reads them all
		MaxIdleConnsPerHost:   2,
	}}
}

func (s *server) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", launcherapi.Key+s.key)
	return req, nil
}

// errUnknown is the error for a key that the server doesn't know.
var errUnknown = errors.New("The server doesn't know you by this link any more: open its page, and join from the room there again.")

// failed returns the error for a call that the server answered with an error. It has the server's status and the
// start of its answer, in one line.
func failed(rsp *http.Response) error {
	if rsp.StatusCode == http.StatusUnauthorized {
		return errUnknown
	}
	text, _ := io.ReadAll(io.LimitReader(rsp.Body, 200))
	return fmt.Errorf("The server answered %s: %s", rsp.Status, strings.TrimSpace(string(text)))
}

// get asks the server for path and decodes the JSON answer into out. The call may take at most limit.
func (s *server) get(ctx context.Context, path string, limit time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	req, err := s.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	rsp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("The server can't be reached: %v", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return failed(rsp)
	}
	if err := json.NewDecoder(io.LimitReader(rsp.Body, 16<<20)).Decode(out); err != nil {
		return fmt.Errorf("The server's answer can't be read (is it a Zombies server?): %v", err)
	}
	return nil
}

// ask asks the server what the player's PC is to play now, and reports whether the PC has what the game needs. With
// seen, the version of the answer the launcher has, the server holds the call until it has another answer.
func (s *server) ask(ctx context.Context, seen string) (*launcherapi.Play, error) {
	q := url.Values{}
	if s.ready.Load() {
		q.Set("ready", "1")
	}
	if seen != "" {
		q.Set("seen", seen)
	}
	path := "/launcher/play"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var play launcherapi.Play
	if err := s.get(ctx, path, askLimit, &play); err != nil {
		return nil, err
	}
	return &play, nil
}

// watch asks the server what to play for as long as ctx lasts, one call after another. The server holds each call
// until its answer changes (server/launcher.go: serveLauncherPlay). The launcher so learns of a change when it
// happens, makes no call on a timer, and shows the server that it runs. watch waits before it tries a failed call
// again, longer after each failure, up to a minute. It returns when ctx is done or the server no longer knows the
// player.
func (s *server) watch(ctx context.Context) {
	s.started.Store(true)
	wait := s.retry
	for ctx.Err() == nil {
		s.mu.Lock()
		resting, wake := s.resting, s.wake
		s.mu.Unlock()
		if resting {
			select {
			case <-wake:
			case <-ctx.Done():
			}
			continue
		}
		call, renew := context.WithCancel(ctx)
		s.mu.Lock()
		s.renew = renew
		seen, failing := "", s.err != nil
		if s.play != nil && !failing {
			seen = s.play.Version
		}
		s.mu.Unlock()
		play, err := s.ask(call, seen)
		renewed := call.Err() != nil
		renew()
		if ctx.Err() != nil {
			return
		}
		if renewed && err != nil {
			continue // setReady or current ended the call, to ask again now
		}
		s.set(play, err)
		pause := time.Duration(0)
		switch {
		case errors.Is(err, errUnknown):
			return
		case err != nil:
			pause, wait = wait, min(2*wait, time.Minute)
		default:
			wait = s.retry
			if failing {
				s.again() // the server may have missed what the launcher last reported
			}
			if play.Version == "" {
				pause = s.poll
			}
		}
		if pause > 0 {
			rest, renew := context.WithTimeout(ctx, pause)
			s.mu.Lock()
			s.renew = renew
			s.mu.Unlock()
			<-rest.Done()
			renew()
		}
	}
}

// set stores the result of a call and wakes whoever waits for it.
func (s *server) set(play *launcherapi.Play, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resting {
		return // the result of a call that ended as the launcher began to rest
	}
	if err == nil {
		s.play = play
	}
	s.err = err
	close(s.next)
	s.next = make(chan struct{})
}

// latest returns the server's last answer and a channel that is closed when there is a newer result. The answer is
// nil before the first one. err is the error of the last call, when that call failed.
func (s *server) latest() (play *launcherapi.Play, next <-chan struct{}, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.play, s.next, s.err
}

// current returns the server's answer as it is now. It waits for the first answer when watch has none yet. When the
// last call failed, it makes watch ask again at once and waits for the result.
func (s *server) current(ctx context.Context) (*launcherapi.Play, error) {
	s.mu.Lock()
	play, err, next, renew := s.play, s.err, s.next, s.renew
	s.mu.Unlock()
	switch {
	case err == nil && play != nil:
		return play, nil
	case errors.Is(err, errUnknown):
		return nil, err
	case err != nil && renew != nil:
		renew()
	}
	select {
	case <-next:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	play, _, err = s.latest()
	if err != nil {
		return nil, err
	}
	return play, nil
}

// rest ends the call to the server, until resume. The launcher rests when it has had nothing to do for a while: its
// player is in no Black Ops II room, or the server can't be reached. It then sends the server nothing at all, and
// the server takes it as stopped. rest drops the server's last answer, so that the answer after resume is a new one.
func (s *server) rest() {
	s.mu.Lock()
	if s.resting {
		s.mu.Unlock()
		return
	}
	s.resting, s.wake = true, make(chan struct{})
	s.play, s.err = nil, nil
	renew := s.renew
	s.mu.Unlock()
	if renew != nil {
		renew()
	}
}

// resume opens the call to the server again after rest.
func (s *server) resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resting {
		s.resting = false
		close(s.wake)
	}
}

// rests reports whether the launcher rests (rest).
func (s *server) rests() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resting
}

// setReady records whether the PC has what the game needs, and tells the server at once: it makes watch ask again.
func (s *server) setReady(ready bool) {
	if s.ready.Swap(ready) == ready {
		return
	}
	s.mu.Lock()
	renew := s.renew
	s.mu.Unlock()
	if renew != nil {
		renew()
	}
}

// files asks for the list of Black Ops II's game files.
func (s *server) files(ctx context.Context) (*launcherapi.Files, error) {
	var files launcherapi.Files
	if err := s.get(ctx, "/launcher/t6/files", 20*time.Minute, &files); err != nil {
		return nil, err
	}
	return &files, nil
}

// report tells the server how the launcher is doing, for the player's room to show. A report that is lost does not
// matter, because the next one has more.
func (s *server) report(ctx context.Context, r launcherapi.Report) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(r)
	req, err := s.request(ctx, http.MethodPost, "/launcher/files", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if rsp, err := s.client.Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(rsp.Body, 4096))
		rsp.Body.Close()
	}
}

// fail records what the launcher's try failed at, and reports it to the server, whose page shows it. With "", it
// takes the last failure back, for a new try. It sends nothing to a server that the player has not agreed to play on
// (start).
func (s *server) fail(ctx context.Context, text string) {
	s.mu.Lock()
	s.failed = text
	s.mu.Unlock()
	if s.started.Load() {
		s.report(ctx, launcherapi.Report{Failed: text})
	}
}

// failure returns what the launcher's last try failed at, or "".
func (s *server) failure() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// again reports the last failure to the server once more. watch calls it when the server answers again after calls
// that failed: the server may have missed the report, or forgotten it.
func (s *server) again() {
	if text := s.failure(); text != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.report(ctx, launcherapi.Report{Failed: text})
	}
}
