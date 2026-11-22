package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zombies/internal/release"
)

// A build is signed only with its own key's public half inside: the launchers that take it in their place take the
// build after it by the key it has, and with none, or another's, they would take no build again.
func TestSignsOnlyABuildWithItsKeyInside(t *testing.T) {
	dir := t.TempDir()
	private, public := filepath.Join(dir, "launcher.key"), filepath.Join(dir, "release.pub")
	if err := run([]string{"key", private, public}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"key", private, public}); err == nil {
		t.Error("it made a key over the one there")
	}
	text, _ := os.ReadFile(public)
	key, err := release.ParsePublic(string(text))
	if err != nil {
		t.Fatal(err)
	}
	othersPublic, _, _ := release.NewKey()
	for _, tc := range []struct {
		name, launcher string
		signed         bool
	}{
		{name: "a build with the key inside", launcher: "a program " + strings.TrimSpace(string(text)) + " and more", signed: true},
		{name: "a build with no key inside", launcher: "a program and more"},
		{name: "a build with another's key inside", launcher: "a program " + othersPublic + " and more"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher, statement := filepath.Join(t.TempDir(), "launcher.exe"), filepath.Join(t.TempDir(), "launcher.sig")
			if err := os.WriteFile(launcher, []byte(tc.launcher), 0o644); err != nil {
				t.Fatal(err)
			}
			err := run([]string{"sign", private, "8", launcher, statement})
			if signed := err == nil; signed != tc.signed {
				t.Fatalf("signed: %v (%v)", signed, err)
			}
			out, readErr := os.ReadFile(statement)
			if !tc.signed {
				if readErr == nil {
					t.Errorf("it left a statement of a build it would not sign: %s", out)
				}
				if !strings.Contains(err.Error(), "launcher/release.pub") {
					t.Errorf("it said: %v", err)
				}
				return
			}
			var signed release.Signed
			if err := json.Unmarshal(out, &signed); err != nil {
				t.Fatal(err)
			}
			want := release.Statement{Version: 8, Hash: release.HashOf([]byte(tc.launcher))}
			if st, err := signed.Verify(key); err != nil || st != want {
				t.Errorf("the statement: %+v, %v", st, err)
			}
		})
	}
}
