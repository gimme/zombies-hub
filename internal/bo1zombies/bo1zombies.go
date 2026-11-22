// Package bo1zombies provides the build of bo1-zombies, the Black Ops engine, that the server runs.
//
// The engine is not part of this repository. It is published as a release archive of
// https://github.com/gimme/bo1-zombies. The server downloads the pinned build once and unpacks it into its data
// folder.
package bo1zombies

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Version and SHA256 pin the engine build that this version of the server runs: a release of bo1-zombies and the
// SHA-256 of its archive. To move to a new release, change both. The release's notes have the SHA-256.
const (
	Version = "v1"
	SHA256  = "f29b73c9043c5a846b4350473441ed3b36bdf3de1f2b213213145218c0469d8e"
)

// URL is the default download address. "{version}" is replaced with the release's version.
const URL = "https://github.com/gimme/bo1-zombies/releases/download/{version}/bo1-zombies-{version}.tar.gz"

// maxSize limits the size of the archive and of its unpacked files. A build is about 15 MB packed and 45 MB unpacked.
const maxSize = 1 << 30

// marker is the file in the engine's folder that holds the SHA-256 of the archive unpacked there. It is written
// last, so a folder without it is incomplete.
const marker = "archive.sha256"

// Source says where the engine comes from.
type Source struct {
	Version string // the release to download
	SHA256  string // the hash the downloaded archive must have
	URL     string // the archive's address; "{version}" is replaced with Version
	// File is the path of a local archive to use instead of a download. It is not checked against SHA256: it is for
	// running a build that has not been released.
	File   string
	Client *http.Client                     // nil: http.DefaultClient
	Logf   func(format string, args ...any) // nil: no log
}

// Engine is an unpacked engine build.
type Engine struct {
	Version   string // the release it is; for a build that was not released, its build ID
	Build     string // its build ID: a client can only join a server of the same build
	Bin       string // the folder with BO1Zombies.exe and its DLLs
	Web       string // the folder with the web client: bo1zombies.js and bo1zombies.wasm
	Pipelines string // the folder with each map's list of pipelines, <map>.bin
}

// Exe is the path of BO1Zombies.exe.
func (e *Engine) Exe() string { return filepath.Join(e.Bin, "BO1Zombies.exe") }

// Get returns the engine in dir. If dir does not hold the wanted archive's contents, Get first replaces what is
// there: it downloads the archive (or reads File), checks its hash and unpacks it.
func (s Source) Get(ctx context.Context, dir string) (*Engine, error) {
	want := s.SHA256
	if s.File != "" {
		sum, err := hashFile(s.File)
		if err != nil {
			return nil, fmt.Errorf("engine: %w", err)
		}
		want = sum
	}
	if have, err := os.ReadFile(filepath.Join(dir, marker)); err == nil && strings.TrimSpace(string(have)) == want {
		if e, err := open(dir); err == nil {
			return e, nil
		}
	}
	if err := s.install(ctx, dir, want); err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	e, err := open(dir)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	s.logf("engine: unpacked %s (build %s) into %s", e.Version, e.Build, dir)
	return e, nil
}

func (s Source) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// install replaces the contents of dir with those of the archive whose SHA-256 is want.
func (s Source) install(ctx context.Context, dir, want string) error {
	archive := s.File
	if archive == "" {
		tmp, err := s.download(ctx, want)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		archive = tmp
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// dir can be a mount point, so it is emptied, not replaced
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	if err := unpack(archive, dir); err != nil {
		return fmt.Errorf("%s: %w", archive, err)
	}
	return os.WriteFile(filepath.Join(dir, marker), []byte(want+"\n"), 0o644)
}

// download saves the archive at s.URL to a temporary file and returns its path. It fails if the file's SHA-256 is not
// want.
func (s Source) download(ctx context.Context, want string) (string, error) {
	url := strings.ReplaceAll(s.URL, "{version}", s.Version)
	s.logf("engine: downloading %s from %s", s.Version, url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	rsp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("can't download %s: %w", s.Version, err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("can't download %s: %s: %s", s.Version, url, rsp.Status)
	}
	tmp, err := os.CreateTemp("", "engine-*.tar.gz")
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(rsp.Body, maxSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch got := hex.EncodeToString(sum.Sum(nil)); {
	case err != nil:
		err = fmt.Errorf("can't download %s: %w", s.Version, err)
	case n > maxSize:
		err = fmt.Errorf("%s: larger than %d MB", url, maxSize>>20)
	case got != want:
		err = fmt.Errorf("%s: not %s: its SHA-256 is %s, not %s", url, s.Version, got, want)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// unpack extracts a .tar.gz archive of regular files and folders into dir.
func unpack(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	left := int64(maxSize)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe file name %q", hdr.Name)
		}
		target := filepath.Join(dir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if left -= hdr.Size; left < 0 {
				return fmt.Errorf("unpacks to more than %d MB", maxSize>>20)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// The games run as another user, in the server's group: readable, and writable only by the server.
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("%q is not a file or a folder", hdr.Name)
		}
	}
}

// open reads the engine build unpacked in dir.
func open(dir string) (*Engine, error) {
	data, err := os.ReadFile(filepath.Join(dir, "engine.json"))
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Version string `json:"version"`
		Build   string `json:"build"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version == "" || manifest.Build == "" {
		return nil, fmt.Errorf("%s: no version and build ID in engine.json", dir)
	}
	e := &Engine{Version: manifest.Version, Build: manifest.Build, Bin: filepath.Join(dir, "bin"), Web: filepath.Join(dir, "web"),
		Pipelines: filepath.Join(dir, "pipelines")}
	if st, err := os.Stat(e.Exe()); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: no bin/BO1Zombies.exe", dir)
	}
	return e, nil
}

func hashFile(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
