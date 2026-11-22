package release

import (
	"encoding/json"
	"testing"
)

func keys(t *testing.T) (public, private string) {
	t.Helper()
	public, private, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

// A statement verifies with its maker's public key, and with no other; and not once a byte of it has changed.
func TestSignAndVerify(t *testing.T) {
	public, private := keys(t)
	pub, err := ParsePublic(public + "\n")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParsePrivate(private)
	if err != nil {
		t.Fatal(err)
	}
	want := Statement{Version: 42, Hash: HashOf([]byte("the program"))}
	signed, err := Sign(key, want)
	if err != nil {
		t.Fatal(err)
	}
	// as a release has it, and a launcher reads it
	wire, _ := json.Marshal(signed)
	var got Signed
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	if st, err := got.Verify(pub); err != nil || st != want {
		t.Errorf("the maker's statement: %+v, %v", st, err)
	}

	other, _ := keys(t)
	otherPub, _ := ParsePublic(other)
	if _, err := got.Verify(otherPub); err == nil {
		t.Error("a statement verified with another maker's key")
	}
	forged := got
	forged.Statement, _ = json.Marshal(Statement{Version: 43, Hash: want.Hash})
	if _, err := forged.Verify(pub); err == nil {
		t.Error("a statement verified whose version was changed after it was signed")
	}
	if _, err := (Signed{}).Verify(pub); err == nil {
		t.Error("an empty statement verified")
	}
	if _, err := got.Verify(nil); err == nil {
		t.Error("a statement verified with no key")
	}
}

func TestParseKeys(t *testing.T) {
	public, private := keys(t)
	for _, bad := range []string{"", "not a key", private} {
		if _, err := ParsePublic(bad); err == nil {
			t.Errorf("%q read as a public key", bad)
		}
	}
	for _, bad := range []string{"", "not a key", public} {
		if _, err := ParsePrivate(bad); err == nil {
			t.Errorf("%q read as a private key", bad)
		}
	}
}
