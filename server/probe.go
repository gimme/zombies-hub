package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
)

// probe reaches a server the way a web client does, to check that players can reach it. It fetches the certificate's
// hash from http://host:port/certhash and opens a WebTransport session to https://host:port/probe. The relay answers
// that session itself and leaves it out of its log. probe sends getchallenge through the session once a second and
// prints the answer, a summary of what the server holds ("2 rooms, 1 game"). It returns exit code 0 when the server
// answered.
func probe(args []string) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	timeout := flags.Duration("timeout", 10*time.Second, "how long to try")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: zombies-server probe [-timeout 10s] <host:port>")
		flags.PrintDefaults()
	}
	if flags.Parse(args) != nil || flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	target := flags.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	answer, err := probeServer(ctx, target)
	if err != nil {
		log.Printf("probe: %s: %v", target, err)
		return 1
	}
	log.Printf("probe: %s: %s", target, answer)
	return 0
}

func probeServer(ctx context.Context, target string) (string, error) {
	hash, err := fetchCertHash(ctx, target)
	if err != nil {
		return "", fmt.Errorf("no certificate hash: %w", err)
	}
	transport, sess, err := dialWebTransport(ctx, target, probePath, hash)
	if err != nil {
		return "", err
	}
	defer transport.Close()
	defer sess.CloseWithError(0, "")

	go func() {
		var nextID uint16
		for ctx.Err() == nil {
			framePacket([]byte("\xff\xff\xff\xffgetchallenge"), &nextID, sess.SendDatagram)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()
	var reasm reassembly
	for {
		datagram, err := sess.ReceiveDatagram(ctx)
		if err != nil {
			return "", fmt.Errorf("no answer from the server: %w", err)
		}
		if packet := reasm.receive(datagram); bytes.HasPrefix(packet, outOfBand) {
			answer := strings.TrimSpace(string(packet[len(outOfBand):]))
			if holds, ok := strings.CutPrefix(answer, "lobbyResponse "); ok {
				return "the server answers (" + holds + ")", nil
			}
			return answer, nil
		}
	}
}

func fetchCertHash(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+"/certhash", nil)
	if err != nil {
		return "", err
	}
	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer rsp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(rsp.Body, 1024))
	if err != nil {
		return "", err
	}
	if rsp.StatusCode != http.StatusOK {
		return "", errors.New(rsp.Status)
	}
	return strings.TrimSpace(string(body)), nil
}

// dialWebTransport opens a session to https://target<path>. It accepts the certificate whose SHA-256 is hash (in hex),
// as browsers do with serverCertificateHashes. Closing the transport ends the session.
func dialWebTransport(ctx context.Context, target, path, hash string) (*webtransport.Transport, *webtransport.Session, error) {
	transport := &webtransport.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // VerifyConnection checks the hash instead
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 {
					return errors.New("no certificate")
				}
				if sum := sha256.Sum256(state.PeerCertificates[0].Raw); !strings.EqualFold(hex.EncodeToString(sum[:]), hash) {
					return errors.New("the certificate's hash is not the one from /certhash")
				}
				return nil
			},
		},
		QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
	rsp, sess, err := transport.Dial(ctx, "https://"+target+path, nil)
	if err == nil && rsp.StatusCode != http.StatusOK {
		err = errors.New(rsp.Status)
	}
	if err != nil {
		transport.Close()
		return nil, nil, err
	}
	return transport, sess, nil
}
