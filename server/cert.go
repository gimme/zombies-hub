package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"sync"
	"time"
)

// The WebTransport certificate is self-signed and short-lived. Browsers accept such a certificate by its SHA-256 hash
// (WebTransport's serverCertificateHashes) when it is ECDSA and valid for at most 14 days. The server makes the
// certificate when it starts and replaces it after a week. Connections that are already open keep the certificate they
// have. Clients fetch the hash from GET /certhash (web.go).
const (
	certLifetime = 13 * 24 * time.Hour
	certRenew    = 7 * 24 * time.Hour
)

type certificates struct {
	mu   sync.Mutex
	cert *tls.Certificate
	hash string // hex
	made time.Time
}

// current returns the certificate for new connections and its hash. It makes a new certificate when the last one is a
// week old (certRenew).
func (c *certificates) current() (*tls.Certificate, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert == nil || time.Since(c.made) >= certRenew {
		cert, hash, err := newCertificate(time.Now())
		if err != nil {
			return nil, "", err
		}
		c.cert, c.hash, c.made = cert, hash, time.Now()
	}
	return c.cert, c.hash, nil
}

func (c *certificates) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, _, err := c.current()
	return cert, err
}

// newCertificate makes an ECDSA P-256 certificate that is valid for certLifetime, starting an hour before now. The
// hour allows for clocks that differ.
func newCertificate(now time.Time) (*tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		return nil, "", err
	}
	notBefore := now.Add(-time.Hour)
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "BO1 Zombies"},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
}
