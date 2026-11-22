package main

import (
	"context"
	"crypto/ed25519"
	"path/filepath"
	"strings"
	"testing"

	"zombies/internal/launcherapi"
	"zombies/internal/release"
)

// The launcher takes the server's build in its place only as a later build of its own maker: by their signed
// statement of it, which no server can make. Whatever else a server hands out, the launcher keeps the build it is, and
// goes on with that.
func TestUpdateNeedsItsMakersWord(t *testing.T) {
	public, private, _ := release.NewKey()
	key, _ := release.ParsePrivate(private)
	_, othersPrivate, _ := release.NewKey()
	others, _ := release.ParsePrivate(othersPrivate)
	const now, next = "the launcher, build 7", "the launcher, build 8"
	sign := func(k ed25519.PrivateKey, version int64, program string) *release.Signed {
		s, err := release.Sign(k, release.Statement{Version: version, Hash: release.HashOf([]byte(program))})
		if err != nil {
			t.Fatal(err)
		}
		return &s
	}
	for _, tc := range []struct {
		name    string
		release *release.Signed
		noKey   bool
		taken   bool
		says    string // why it could not take the server's build, when it says so
	}{
		{name: "a later build, signed by its maker", release: sign(key, 8, next), taken: true},
		{name: "a build with no statement", says: "not signed"},
		{name: "a build another maker signed", release: sign(others, 8, next), says: "not signed by this launcher's maker"},
		{name: "an earlier build of its maker", release: sign(key, 6, next)},
		{name: "a build of its own number", release: sign(key, 7, next)},
		{name: "another program than the one signed", release: sign(key, 8, "something else"), says: "not the build its maker signed"},
		{name: "a launcher built without a key", release: sign(key, 8, next), noKey: true, says: "without its maker's key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl := newTestLauncher(t, "")
			installed := filepath.Join(tl.home, exeName)
			write(t, installed, now)
			tl.exe, tl.version = installed, 7
			if !tc.noKey {
				tl.key, _ = release.ParsePublic(public)
			}
			own := filepath.Join(t.TempDir(), "Black Ops II")
			write(t, filepath.Join(own, "zone", "all", "patch_zm.ff"), "the patch")
			write(t, filepath.Join(own, "binkw32.dll"), "bink")
			tl.copies = func() []string { return []string{own} }
			l := newFakeServer(t)
			l.launcher = []byte(next)
			l.play = launcherapi.Play{Player: "Ann", Launcher: hashOf(next), Release: tc.release}
			if code := tl.run(context.Background(), []string{l.link()}); code != 0 {
				t.Fatalf("the run: %d\n%s", code, tl.out)
			}
			out := tl.out.String()
			if taken := read(t, installed) == next && len(tl.reran) == 1; taken != tc.taken {
				t.Errorf("the server's build was taken: %v\n%s", taken, out)
			}
			if said := strings.Contains(out, "could not take"); said != (tc.says != "") || !strings.Contains(out, tc.says) {
				t.Errorf("it said:\n%s", out)
			}
		})
	}
}
