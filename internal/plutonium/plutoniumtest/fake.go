// Package plutoniumtest provides a stand-in for Plutonium's updater, for tests.
package plutoniumtest

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

// Updater is a stand-in for Plutonium's updater. It serves files (by name) at the revision *revision, each with the
// value it has at the time of the request. URL is the address of its manifest, and Client reaches it.
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
func Serve(t testing.TB, files map[string]string, revision *int) *Updater {
	t.Helper()
	u := &Updater{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.asked = append(u.asked, r.URL.Path)
		u.mu.Unlock()
		if r.URL.Path == "/info.json" {
			info := map[string]any{"revision": *revision, "baseUrl": srv.URL + "/files/"}
			var list []map[string]any
			for name, data := range files {
				sum := sha1.Sum([]byte(data))
				list = append(list, map[string]any{"name": name, "size": len(data),
					"hash": strings.ToUpper(hex.EncodeToString(sum[:]))})
			}
			info["files"] = list
			json.NewEncoder(w).Encode(info)
			return
		}
		for _, data := range files {
			sum := sha1.Sum([]byte(data))
			if r.URL.Path == "/files/"+hex.EncodeToString(sum[:]) {
				io.WriteString(w, data)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	u.URL, u.Client = srv.URL+"/info.json", srv.Client()
	return u
}
