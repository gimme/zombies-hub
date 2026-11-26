package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"zombies/internal/launcherapi"
	"zombies/internal/release"
)

// fakeReleases stands in for the launcher's releases on GitHub. Each release has a tag, launcher-v<version>, and two
// files: the launcher and its statement. As on GitHub, the release tagged launcher (launcherapi.Current) has the
// files of the latest of them.
type fakeReleases struct {
	srv *httptest.Server

	mu    sync.Mutex
	files map[string]map[string][]byte // each release's files, by tag and name
	asked []string                     // the path of each request
	// stated is called once the latest release's statement was sent, before the next request is answered
	stated func()
}

func newFakeReleases(t *testing.T) *fakeReleases {
	r := &fakeReleases{files: map[string]map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeReleases) url() string { return r.srv.URL + "/gimme/zombies-hub/releases" }

func (r *fakeReleases) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.URL.Path)
	path := strings.TrimPrefix(req.URL.Path, "/gimme/zombies-hub/releases")
	var data []byte
	if rest, ok := strings.CutPrefix(path, "/download/"); ok {
		tag, file, _ := strings.Cut(rest, "/")
		data = r.files[tag][file]
	}
	stated := r.stated
	if strings.HasSuffix(path, "/"+launcherapi.Signed) {
		r.stated = nil
	} else {
		stated = nil
	}
	r.mu.Unlock()
	if data == nil {
		http.NotFound(w, req)
		return
	}
	w.Write(data)
	if stated != nil {
		stated()
	}
}

// publish makes the latest release: the program as the launcher of version, with a statement signed by key.
func (r *fakeReleases) publish(t *testing.T, key ed25519.PrivateKey, version int64, program string) {
	t.Helper()
	signed, err := release.Sign(key, release.Statement{Version: version, Hash: release.HashOf([]byte(program))})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(signed)
	r.put(version, program, text)
}

// put makes the latest release of version with the files given.
func (r *fakeReleases) put(version int64, program string, statement []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	files := map[string][]byte{launcherapi.Setup: []byte(program), launcherapi.Signed: statement}
	r.files[launcherapi.Tag(version)], r.files[launcherapi.Current] = files, files
}

// requests returns the paths that the releases were asked for since the last call.
func (r *fakeReleases) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	asked := r.asked
	r.asked = nil
	return asked
}

// updatingLauncher is an installed launcher of version 7 of its maker, on a PC with its own copy of the game, and a
// server that has nothing for it to play. Its releases are releases.
func updatingLauncher(t *testing.T, public string, releases *fakeReleases) (tl *testLauncher, installed string, l *fakeServer) {
	t.Helper()
	tl = newTestLauncher(t, "")
	installed = filepath.Join(tl.home, exeName)
	tl.exe, tl.version, tl.releases = installed, 7, releases.url()
	tl.key, _ = release.ParsePublic(public)
	own := filepath.Join(t.TempDir(), "Black Ops II")
	write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
	write(t, filepath.Join(own, "binkw32.dll"), "bink")
	tl.copies = func(*title) []string { return []string{own} }
	l = newFakeServer(t)
	l.play = launcherapi.Play{Player: "Ann"}
	return tl, installed, l
}

// The launcher takes its latest release in its place only as a later build of its own maker: by their signed statement
// of it, which neither GitHub nor anyone between it and the PC can make. Whatever else it is handed, or when GitHub
// can't be reached, the launcher keeps the build it is, and plays on with it.
func TestUpdateNeedsItsMakersWord(t *testing.T) {
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	_, othersPrivate, _ := release.NewKey()
	others, _ := release.ParsePrivate(othersPrivate)
	releases := newFakeReleases(t)
	tl, installed, l := updatingLauncher(t, public, releases)
	const now = "the launcher, build 7"
	// try runs a link, and reports whether the launcher took a release; says is what it said of the latest release
	try := func(what string) (taken bool, says string) {
		t.Helper()
		write(t, installed, now)
		tl.again("")
		tl.reran = nil
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
			t.Fatalf("%s: the run: %d\n%s", what, code, tl.out)
		}
		out := tl.out.String()
		if !strings.Contains(out, "You are Ann") {
			t.Errorf("%s: the launcher did not play on:\n%s", what, out)
		}
		_, says, _ = strings.Cut(out, "could not take the launcher's latest release, and plays on with this build: ")
		says, _, _ = strings.Cut(says, "\n")
		return read(t, installed) != now || len(tl.reran) > 0, says
	}
	kept := func(what, says string) {
		t.Helper()
		if taken, said := try(what); taken || !strings.Contains(said, says) || (says == "") != (said == "") {
			t.Errorf("%s: taken %v, said %q", what, taken, said)
		}
	}
	// GitHub can't be reached
	tl.releases = "http://127.0.0.1:1/gimme/zombies-hub/releases"
	kept("no releases to reach", "can't be reached")
	tl.releases = releases.url()
	kept("no release yet", "404 Not Found")
	releases.publish(t, key, 7, now)
	kept("its own release", "")
	releases.publish(t, key, 6, "the launcher, build 6")
	kept("an earlier release", "")
	releases.publish(t, others, 8, "another maker's build 8")
	kept("a release another maker signed", "not signed by this launcher's maker")
	releases.put(8, "the launcher, build 8", []byte("not a statement"))
	kept("a release with no statement", "can't be read")
	releases.publish(t, key, 8, "the launcher, build 8")
	releases.mu.Lock()
	releases.files[launcherapi.Tag(8)][launcherapi.Setup] = []byte("something else")
	releases.mu.Unlock()
	kept("another program than the one signed", "not the build its maker signed")
	if asked := releases.requests(); len(asked) == 0 {
		t.Error("the launcher asked its releases nothing")
	}
	// a later release: the launcher takes the build of the version that the statement names, even when another
	// release is published in between
	releases.publish(t, key, 8, "the launcher, build 8")
	releases.mu.Lock()
	releases.stated = func() { releases.publish(t, key, 9, "the launcher, build 9") }
	releases.mu.Unlock()
	write(t, installed, now)
	tl.again("")
	tl.reran = nil
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !tl.handed || len(tl.reran) != 1 {
		t.Fatalf("a later release: %d, handed on %v, ran again %q\n%s", code, tl.handed, tl.reran, tl.out)
	}
	if got := read(t, installed); got != "the launcher, build 8" {
		t.Errorf("after the update: %q", got)
	}
	if asked := releases.requests(); len(asked) != 2 || !strings.HasSuffix(asked[0], "/download/launcher/"+launcherapi.Signed) ||
		!strings.HasSuffix(asked[1], "/download/launcher-v8/"+launcherapi.Setup) {
		t.Errorf("the launcher asked its releases for %q", asked)
	}
}

// A launcher that is no release of its maker's never replaces itself, and asks GitHub nothing: a dev build (version
// 0), a build without its maker's key, a launcher outside its folder, and a launcher that an update started.
func TestUpdateOnlyForReleases(t *testing.T) {
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	releases := newFakeReleases(t)
	releases.publish(t, key, 8, "the launcher, build 8")
	tl, installed, l := updatingLauncher(t, public, releases)
	try := func(what string) {
		t.Helper()
		write(t, installed, "the launcher, build 7")
		tl.again("")
		if code := tl.run(context.Background(), []string{l.link()}); code != 0 || tl.handed {
			t.Fatalf("%s: the run: %d, handed on %v\n%s", what, code, tl.handed, tl.out)
		}
		if asked := releases.requests(); len(asked) != 0 || read(t, installed) != "the launcher, build 7" {
			t.Errorf("%s: the launcher asked for %q, and is %q", what, asked, read(t, installed))
		}
	}
	tl.version = 0
	try("a dev build")
	tl.version, tl.key = 7, nil
	try("a build without its maker's key")
	tl.key, _ = release.ParsePublic(public)
	tl.exe = filepath.Join(t.TempDir(), "zombies-launcher.exe")
	write(t, tl.exe, "the launcher, build 7")
	try("a launcher outside its folder")
	tl.exe = installed
	t.Setenv(updatedEnv, "1")
	try("a launcher that an update started")
}

// The launcher sends its version with every call. A server that doesn't take it gets no more calls: the launcher
// looks for a later release, takes it when there is one, and else says where the latest release is, and ends.
func TestTooOldForTheServer(t *testing.T) {
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	releases := newFakeReleases(t)
	releases.publish(t, key, 7, "the launcher, build 7")
	tl, installed, l := updatingLauncher(t, public, releases)
	write(t, installed, "the launcher, build 7")
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
		t.Fatalf("the run: %d\n%s", code, tl.out)
	}
	l.mu.Lock()
	versions := l.versions
	l.versions = nil
	l.mu.Unlock()
	for _, v := range versions {
		if v != "7" {
			t.Fatalf("the launcher sent its version as %q", versions)
		}
	}
	// a server that takes launchers of version 8 and later; there is no such release yet
	l.mu.Lock()
	l.refuse = http.StatusUpgradeRequired
	l.mu.Unlock()
	tl.again("")
	setup := launcherapi.LatestFile(releases.url(), launcherapi.Setup)
	if code := tl.run(context.Background(), []string{l.link()}); code != 1 || len(tl.started) != 0 ||
		!strings.Contains(tl.out.String(), "This launcher is too old for this server. Install the new one, then try "+
			"again: "+setup) {
		t.Errorf("too old: %d, started %+v\n%s", code, tl.started, tl.out)
	}
	// a launcher that stays ends, so that the next link starts the launcher that is installed then
	tl.again("")
	tl.stays = true
	done := make(chan int, 1)
	go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("a launcher that stays, too old: %d\n%s", code, tl.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a launcher that stays, too old for the server, did not end")
	}
	l.mu.Lock()
	calls := len(l.versions)
	l.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	l.mu.Lock()
	more := len(l.versions) - calls
	l.mu.Unlock()
	if more != 0 {
		t.Errorf("the launcher called a server that doesn't take it %d more times", more)
	}
	// the release that the server needs is out: the launcher says that it started, which the server takes from any
	// launcher, then takes the release before it asks the server anything, so the server turns none of its calls away,
	// and has nothing to tell the player's room
	tl.stays = false
	releases.publish(t, key, 8, "the launcher, build 8")
	tl.again("")
	l.mu.Lock()
	l.asked = nil
	l.mu.Unlock()
	if code := tl.run(context.Background(), []string{l.link()}); code != 0 || !tl.handed ||
		read(t, installed) != "the launcher, build 8" {
		t.Errorf("too old, with a later release: %d, handed on %v, is %q\n%s", code, tl.handed, read(t, installed), tl.out)
	}
	l.mu.Lock()
	heard := l.asked
	l.mu.Unlock()
	if !slices.Equal(heard, []string{"POST /launcher/started"}) {
		t.Errorf("what the server heard of a launcher that had a later release to take: %q", heard)
	}
	// a dev build says what it is
	tl.version = 0
	tl.again("")
	if code := tl.run(context.Background(), []string{l.link()}); code != 1 || !strings.Contains(tl.out.String(),
		"This launcher is a dev build, and this server only accepts released launchers") {
		t.Errorf("a dev build: %d\n%s", code, tl.out)
	}
}

// A server that is updated while a launcher runs may take that launcher no more. The launcher then looks for a later
// release again, and hands its link to that one: while it waits for its room's game, and while it stays with nothing
// to play.
func TestTurnedAwayAsItRuns(t *testing.T) {
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	for _, room := range []string{"ABCD", ""} {
		releases := newFakeReleases(t)
		releases.publish(t, key, 7, "the launcher, build 7")
		tl, installed, l := updatingLauncher(t, public, releases)
		write(t, installed, "the launcher, build 7")
		l.play.Room = room
		tl.stays = true
		done := make(chan int, 1)
		go func() { done <- tl.run(context.Background(), []string{l.link()}) }()
		// the launcher runs: the server holds no call, so it asks again and again
		eventually(t, "the launcher called the server", func() bool {
			l.mu.Lock()
			defer l.mu.Unlock()
			return len(l.versions) >= 5
		})
		releases.publish(t, key, 8, "the launcher, build 8")
		l.mu.Lock()
		l.refuse = http.StatusUpgradeRequired
		l.mu.Unlock()
		select {
		case code := <-done:
			if code != 0 || !tl.handed || len(tl.reran) != 1 || read(t, installed) != "the launcher, build 8" {
				t.Errorf("room %q: %d, handed on %v, ran again %q, is %q\n%s", room, code, tl.handed, tl.reran,
					read(t, installed), tl.out)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("room %q: the launcher that the server turned away did not end", room)
		}
	}
}
