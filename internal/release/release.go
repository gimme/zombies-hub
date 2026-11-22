// Package release lets a launcher recognise another build of itself as its maker's. The maker signs a statement of
// the build with the maker's key, and every launcher of that maker has the public half of the key built in. A server
// sends the statement with the build, and can make neither. So whoever runs a server, or breaks into one, cannot put
// a program of their own in a launcher's place.
package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// Statement identifies a build of a program.
type Statement struct {
	// Version orders the builds: a later build has a larger version. A launcher accepts only a build later than
	// itself, so that no one can put an old build, with a fault that was fixed since, in its place.
	Version int64 `json:"version"`
	// Hash is the SHA-256 of the program's contents, in hex.
	Hash string `json:"hash"`
}

// Signed is a statement as it was signed, with its signature.
type Signed struct {
	Statement []byte `json:"statement"` // the statement's JSON
	Signature []byte `json:"signature"`
}

// domain is the line that comes before the statement in the text that is signed. With it, a signature that the key
// made for anything else is not a signature of a build.
const domain = "zombies-launcher release\n"

// NewKey makes a maker's key. It returns the public half, for the launchers, and the private half, which signs their
// builds and which only the maker has. Each half is a line of text.
func NewKey() (public, private string, err error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return publicPrefix + base64.StdEncoding.EncodeToString(pub), privatePrefix + base64.StdEncoding.EncodeToString(key.Seed()), nil
}

// publicPrefix and privatePrefix are what the line of each half starts with, so that neither half is mistaken for
// the other. The private half must never be where the public half goes, in the repository.
const (
	publicPrefix  = "zombies-release-public:"
	privatePrefix = "zombies-release-private:"
)

// ParsePublic and ParsePrivate parse a half of a key, in the form that NewKey returns.
func ParsePublic(text string) (ed25519.PublicKey, error) {
	text, ok := strings.CutPrefix(strings.TrimSpace(text), publicPrefix)
	b, err := base64.StdEncoding.DecodeString(text)
	if !ok || err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not a release key's public half")
	}
	return ed25519.PublicKey(b), nil
}

func ParsePrivate(text string) (ed25519.PrivateKey, error) {
	text, ok := strings.CutPrefix(strings.TrimSpace(text), privatePrefix)
	b, err := base64.StdEncoding.DecodeString(text)
	if !ok || err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("not a release key's private half")
	}
	return ed25519.NewKeyFromSeed(b), nil
}

// PublicOf returns the public half of a private key, in the form that NewKey returns. It is the line that a launcher
// of that maker has built in.
func PublicOf(key ed25519.PrivateKey) string {
	return publicPrefix + base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

// HashOf returns the hash that a statement names a program's contents by.
func HashOf(program []byte) string {
	sum := sha256.Sum256(program)
	return hex.EncodeToString(sum[:])
}

// Sign returns the signed statement of a build.
func Sign(key ed25519.PrivateKey, s Statement) (Signed, error) {
	text, err := json.Marshal(s)
	if err != nil {
		return Signed{}, err
	}
	return Signed{Statement: text, Signature: ed25519.Sign(key, append([]byte(domain), text...))}, nil
}

// Verify returns the statement when the maker with the public key signed it, and an error otherwise.
func (s Signed) Verify(public ed25519.PublicKey) (Statement, error) {
	var st Statement
	if len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, append([]byte(domain), s.Statement...), s.Signature) {
		return st, errors.New("not signed by this launcher's maker")
	}
	if err := json.Unmarshal(s.Statement, &st); err != nil || st.Hash == "" {
		return Statement{}, errors.New("a signed statement that says no build")
	}
	return st, nil
}
