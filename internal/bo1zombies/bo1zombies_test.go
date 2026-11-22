package bo1zombies

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// archive returns a .tar.gz of the files, given as name and content pairs, and its SHA-256.
func archive(t *testing.T, files ...string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for i := 0; i < len(files); i += 2 {
		name, content := files[i], files[i+1]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// build returns the archive of a release whose version and build ID are both id, and its SHA-256.
func build(t *testing.T, id string, more ...string) ([]byte, string) {
	t.Helper()
	files := []string{"engine.json", `{"version": "` + id + `", "build": "` + id + `"}`, "bin/BO1Zombies.exe", "exe " + id,
		"web/bo1zombies.wasm", "wasm " + id, "pipelines/zombie_theater.bin", "list " + id}
	return archive(t, append(files, more...)...)
}

func read(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGetDownloadsOnce(t *testing.T) {
	data, sum := build(t, "abc1234")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/builds/bo1-zombies-abc1234.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	src := Source{Version: "abc1234", SHA256: sum, URL: srv.URL + "/builds/bo1-zombies-{version}.tar.gz"}
	dir := filepath.Join(t.TempDir(), "engine")

	for range 2 {
		e, err := src.Get(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if e.Version != "abc1234" || e.Build != "abc1234" || read(t, e.Exe()) != "exe abc1234" ||
			read(t, filepath.Join(e.Web, "bo1zombies.wasm")) != "wasm abc1234" ||
			read(t, filepath.Join(e.Pipelines, "zombie_theater.bin")) != "list abc1234" {
			t.Errorf("got %+v, want build abc1234 with its files", e)
		}
	}
	if requests != 1 {
		t.Errorf("%d downloads, want 1", requests)
	}
}

func TestGetRejectsAnotherArchive(t *testing.T) {
	data, _ := build(t, "abc1234")
	_, pinned := build(t, "def5678")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "engine")

	_, err := Source{Version: "def5678", SHA256: pinned, URL: srv.URL}.Get(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "not def5678") {
		t.Errorf("got error %v, want one saying the archive is not the build", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d files unpacked from an archive with the wrong hash", len(entries))
	}
}

func TestGetReplacesAnotherBuild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "engine")
	file := filepath.Join(t.TempDir(), "engine.tar.gz")
	first, _ := build(t, "abc1234", "bin/old.dll", "old")
	second, _ := build(t, "def5678")

	for _, step := range []struct {
		data []byte
		id   string
	}{{first, "abc1234"}, {second, "def5678"}} {
		if err := os.WriteFile(file, step.data, 0o644); err != nil {
			t.Fatal(err)
		}
		// File is used whatever the pinned hash is
		e, err := Source{Version: "pinned", SHA256: "0000", File: file}.Get(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if e.Build != step.id || read(t, e.Exe()) != "exe "+step.id {
			t.Errorf("got build %s, want %s", e.Build, step.id)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "old.dll")); !os.IsNotExist(err) {
		t.Errorf("a file of the build before is still there (%v)", err)
	}
}

func TestGetRepairsAnIncompleteFolder(t *testing.T) {
	data, sum := build(t, "abc1234")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	src := Source{Version: "abc1234", SHA256: sum, URL: srv.URL}
	dir := filepath.Join(t.TempDir(), "engine")

	e, err := src.Get(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.Exe()); err != nil {
		t.Fatal(err)
	}
	if e, err = src.Get(context.Background(), dir); err != nil || read(t, e.Exe()) != "exe abc1234" {
		t.Errorf("after the exe was deleted: %v", err)
	}
}

func TestGetRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"../outside", "/etc/passwd", "bin/../../outside"} {
		data, _ := archive(t, "engine.json", `{"version": "v1", "build": "abc1234"}`, name, "x")
		parent := t.TempDir()
		file := filepath.Join(parent, "engine.tar.gz")
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := (Source{File: file}).Get(context.Background(), filepath.Join(parent, "engine")); err == nil {
			t.Errorf("%s: unpacked", name)
		}
		if _, err := os.Stat(filepath.Join(parent, "outside")); !os.IsNotExist(err) {
			t.Errorf("%s: wrote outside the engine's folder", name)
		}
	}
}
