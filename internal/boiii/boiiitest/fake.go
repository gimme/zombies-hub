// Package boiiitest provides a stand-in for BOIII's updater, for tests.
package boiiitest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Updater is a stand-in for BOIII's updater. It serves files (by name), each with the value it has at the time of
// the request. URL is the address of its manifest, and Client reaches it.
type Updater struct {
	URL    string
	Client *http.Client

	mu    sync.Mutex
	asked []string
}

// Asked returns the paths that were requested from the updater since the last call of Asked.
func (u *Updater) Asked() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	asked := u.asked
	u.asked = nil
	return asked
}

// Serve starts an updater, which runs until the test ends.
func Serve(t testing.TB, files map[string]string) *Updater {
	t.Helper()
	u := &Updater{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.asked = append(u.asked, r.URL.Path)
		u.mu.Unlock()
		if strings.HasPrefix(r.Header.Get("User-Agent"), "Go-http-client") {
			http.Error(w, "no", http.StatusForbidden) // as BOIII's host answers a client that names no program
			return
		}
		if r.URL.Path == "/boiii.json" {
			list := [][]any{}
			for name, data := range files {
				sum := sha1.Sum([]byte(data))
				list = append(list, []any{name, len(data), strings.ToUpper(hex.EncodeToString(sum[:]))})
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		if data, ok := files[strings.TrimPrefix(r.URL.Path, "/boiii/")]; ok {
			io.WriteString(w, data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	u.URL, u.Client = srv.URL+"/boiii.json", srv.Client()
	return u
}
