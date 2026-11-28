package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"zombies/internal/launcherapi"
)

// The server's side of the launcher (launcher/: zombies-launcher.exe, on a player's PC). The package
// internal/launcherapi defines what the two send each other.
//
// The players of a PC room's game join it from their own PCs (pc.go: a Black Ops II room's, or a Black Ops III
// room's), and a page can't do that for them. The
// launcher does. A link starts the launcher, and the room's button opens the link. The link names the server and the
// player, by the player's key. While its player is in a PC room, and for five minutes after, the launcher
// has one call open that asks the server what to play (GET /launcher/play). The server holds the call until its
// answer changes (serveLauncherPlay). The answer has
// the room's game, from when the game starts, with the game's password and the name the player has in it. The
// launcher then starts the player's own game, which joins the room's game.
//
// Before that, the launcher makes sure that the PC has what the game needs. For Black Ops II, its copy of Plutonium
// has to be of the same revision as the server's, which the answer has. When it is not, the launcher gets Plutonium
// from Plutonium's own updater, as the server does (internal/plutonium). For Black Ops III it gets BOIII from BOIII's
// updater in the same way (internal/boiii).
//
// When the PC has no game files of the room's title, or lacks the room's map, and the server sends that title's game
// files (-download-rate, sending), the launcher downloads them from the server. It fetches their list
// (GET /launcher/<title>/files), which has the hash of each file's contents, and then each file
// (GET /launcher/<title>/files/<path>), from where an earlier download stopped. Black Ops III's list is in packs, one
// for each map and one that every map needs, which has the map that the game comes with (gameSends), and a launcher
// downloads the packs of the maps that its player plays, or asks for in the lobby's Play local. The files are sent at
// the rate that all the
// server's downloads share (web.pace). The launcher reports the progress of its download (POST /launcher/files). The
// player's room shows it on the player, as it shows a browser's download of a T5 map. The game waits for a player
// whose launcher is still preparing (pcLate, preparing), until the launcher reports that the PC is ready (?ready=1).
//
// The launcher's calls carry the player's key. Like a call from the player's page, a launcher's call sets the address
// where the server expects the player's game (player.addr). On a server where players sign in with Discord, the key
// works only while the player has a session.
//
// The launcher's calls also carry its version. The server takes only the versions that its code works with
// (launcherapi.MinVersion), and a dev build (version 0) only on a dev server (-dev). It refuses any other launcher's
// calls (launcherFits), as it does those of a launcher that is too old for the title of its player's room
// (launcherapi.MinVersionOf), and notes on the player that their launcher is too old. Their room then says so, with the
// link to the launcher's latest release: the launcher that was refused can't report it. It takes two calls of any
// version: the one that says the launcher started (POST /launcher/started), which comes before the launcher looks for
// a later release of itself (serveLauncherStarted), and the one that says its player quit it (POST
// /launcher/stopped, serveLauncherStopped).
//
// The server does not hand out the launcher. Its releases are on GitHub (launcherapi.Releases), and the room's page
// links to the latest one (GET /server: "launcher"). An installed launcher updates itself from there.

// launcherFiles is the list of a title's game files as the launcher downloads them (launcherapi.Files), with each
// file's place on disk.
type launcherFiles struct {
	launcherapi.Files
	disk  map[string]string // each file's path on disk, by its path in the list
	body  []byte            // the list as served
	packs map[string]int64  // the size of each pack, once sentPacks has added them up
}

// launcherSkipDirs are the folders in a title's game files that the launcher does not download. They hold what a game
// writes there, not the game's own files.
var launcherSkipDirs = map[string]bool{"players": true, "boiii_players": true, "$recycle.bin": true}

// sentFiles is a title's game files as the server sends them to launchers: the title, by its id and its name in the
// log, the folder that it sends them from, and what of the folder a player's game needs (gameSends). list is the list
// of them that the server last made.
type sentFiles struct {
	of, label string
	dir       string
	sends     func(rel string) (pack string, sends bool)
	mu        sync.Mutex
	list      *launcherFiles
}

// sending returns the game files of a title that the server sends to launchers, or nil when it sends none of the
// title: it sends no game files (-download-rate), or has none of the title's to send. That is T6's folder, and for T7
// the folder that has what a player's game needs of it (t7Send).
func (h *web) sending(of string) *sentFiles {
	dir := map[string]string{t6: h.t6Dir, t7: h.t7Send}[of]
	if !h.downloads || dir == "" {
		return nil
	}
	h.sentMu.Lock()
	defer h.sentMu.Unlock()
	s := h.sent[of]
	if s == nil || s.dir != dir {
		if h.sent == nil {
			h.sent = map[string]*sentFiles{}
		}
		s = &sentFiles{of: of, label: map[string]string{t6: "Black Ops II", t7: "Black Ops III"}[of], dir: dir,
			sends: gameSends[of]}
		h.sent[of] = s
	}
	return s
}

// listFiles lists the title's game files as they are now: the files in its folder that a player's game needs (gameSends),
// each by its path, with its size, the hash of its contents, and its pack. The server stores the hashes (hashes.go),
// so only the first listing reads the files (about 9 GB of Black Ops II, which takes a minute, and 87 GB of Black Ops
// III, which takes several). The server sends files by this list until it makes the next one (listOf).
func (h *web) listFiles(s *sentFiles) *launcherFiles {
	var entries []*manifestEntry
	packs := map[string]string{}
	filepath.WalkDir(s.dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if file != s.dir && launcherSkipDirs[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		name := strings.ToLower(d.Name())
		if err != nil || !d.Type().IsRegular() || strings.HasSuffix(name, ".log") || name == launcherapi.StateFile ||
			strings.HasSuffix(name, launcherapi.PartSuffix) {
			return nil
		}
		rel, _ := filepath.Rel(s.dir, file)
		rel = filepath.ToSlash(rel)
		pack, sends := s.sends(rel)
		if !sends {
			return nil // another mode's, which the same folder has where Steam installed it too, or not the game's
		} else if !launcherapi.LocalPath(rel) {
			log.Printf("files: %s's %q is not sent: a player's PC may not take its name", s.label, rel)
			return nil
		}
		entries = append(entries, &manifestEntry{rel: rel, file: file, size: info.Size(), modTime: info.ModTime().UnixNano()})
		packs[rel] = pack
		return nil
	})
	h.hashes.fill(entries)
	slices.SortFunc(entries, func(a, b *manifestEntry) int { return strings.Compare(a.rel, b.rel) })
	list := &launcherFiles{disk: map[string]string{}}
	list.Files.Files = []launcherapi.File{}
	sum := sha256.New()
	for _, e := range entries {
		if e.hash == "" { // a file that could not be read is left out
			continue
		}
		list.Files.Files = append(list.Files.Files, launcherapi.File{Path: e.rel, Size: e.size, Hash: e.hash, Pack: packs[e.rel]})
		list.Bytes += e.size
		list.disk[e.rel] = e.file
		fmt.Fprintf(sum, "%s\x00%s\x00", e.rel, e.hash)
		if pack := packs[e.rel]; pack != "" {
			fmt.Fprintf(sum, "%s\x00", pack)
		}
	}
	list.Version = "v" + hex.EncodeToString(sum.Sum(nil))[:16]
	list.body, _ = json.Marshal(list.Files)
	s.mu.Lock()
	s.list = list
	s.mu.Unlock()
	return list
}

// listOf returns the list of the title's game files that the server last made. It makes the list when there is none
// yet.
func (h *web) listOf(s *sentFiles) *launcherFiles {
	s.mu.Lock()
	list := s.list
	s.mu.Unlock()
	if list == nil {
		list = h.listFiles(s)
	}
	return list
}

// sentPacks reports whether the server sends a title's game files to launchers, and returns the size of each pack of
// them, by the pack's name (launcherapi.File), as the list has them that the server last made. It returns no sizes
// until the server has made a list: it never waits for one. The lobby's view has them (lobby.sends).
func (h *web) sentPacks(of string) (downloads bool, packs map[string]int64) {
	s := h.sending(of)
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.list != nil && s.list.packs == nil {
		s.list.packs = map[string]int64{}
		for _, f := range s.list.Files.Files {
			s.list.packs[f.Pack] += f.Size
		}
	}
	if s.list != nil {
		packs = s.list.packs
	}
	return true, packs
}

// launcherFits reports whether the server takes the calls of the launcher that made the call: one of a version that
// the server's code works with (launcherapi.MinVersion), or a dev build on a dev server. The version also has to be
// one that plays the title of (launcherapi.MinVersionOf), when the call is about one, and the title of its player's
// room, when that is a PC room. When the server does not take it, it answers
// the call with 426 Upgrade Required, and notes on the call's player that their launcher is too old
// (launcherReport.old). A launcher looks for a later release before its first call but the one that says it started
// (serveLauncherStarted), so the server refuses only one that found none it could take. That launcher no longer counts
// as running.
func (h *web) launcherFits(w http.ResponseWriter, r *http.Request, of string) bool {
	sent := r.Header.Get(launcherapi.VersionHeader)
	version, err := strconv.ParseInt(sent, 10, 64)
	l := h.lobby
	l.mu.Lock()
	needs := launcherapi.MinVersionOf(of)
	if p := l.launcherOf(r); p != nil && p.room != nil {
		needs = max(needs, launcherapi.MinVersionOf(titleOf(p.room.mapName)))
	}
	if err == nil && (version >= needs || version == 0 && h.dev) {
		l.mu.Unlock()
		return true
	}
	if p := l.launcherOf(r); p != nil {
		if p.launcher.held == 0 && !p.launcher.seen.IsZero() {
			p.launcher.seen = time.Time{}
			l.change()
		}
		if !p.launcher.old {
			p.launcher.old = true
			l.change()
			log.Printf("server: %s's launcher is too old for this server (version %q): their room links to its latest release",
				p.asked, sent)
		}
	}
	l.mu.Unlock()
	said := "This launcher is too old for this server: install the new one from the room's page."
	if err == nil && version == 0 {
		said = "This launcher is a dev build, which only a dev server takes: install the launcher from the room's page."
	}
	http.Error(w, said, http.StatusUpgradeRequired)
	return false
}

// serveLauncherCall answers the launcher's calls (/launcher/..., above). It answers 401 for a key that the server
// doesn't know, and refuses a launcher whose version it doesn't take (launcherFits).
func (h *web) serveLauncherCall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/launcher/started" && r.Method == http.MethodPost { // of any version
		h.serveLauncherStarted(w, r)
		return
	}
	if r.URL.Path == "/launcher/stopped" && r.Method == http.MethodPost { // of any version
		h.serveLauncherStopped(w, r)
		return
	}
	l := h.lobby
	what := strings.TrimPrefix(r.URL.Path, "/launcher/")
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	// the title that the call is about: the one whose files it asks for, or the one that the launcher is at
	of, files, titled := strings.Cut(what, "/")
	if !titled || !pcTitle(of) {
		of = ""
	}
	if what == "play" && get {
		if of = r.URL.Query().Get("of"); !pcTitle(of) {
			of = t6 // a launcher from before it started any other title says none
		}
	}
	if !h.launcherFits(w, r, of) {
		return
	}
	if what == "play" && get {
		h.serveLauncherPlay(w, r, of)
		return
	}
	var report launcherapi.Report
	if what == "files" && r.Method == http.MethodPost {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&report); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	l.mu.Lock()
	p := l.launcherOf(r)
	if p != nil {
		p.addr = pageAddr(r)
		l.launcherCalled(p, false, false)
		if what == "files" && r.Method == http.MethodPost {
			l.setLauncher(p, report)
		}
	}
	l.mu.Unlock()
	switch {
	case p == nil:
		launcherUnknown(w)
	case what == "files" && r.Method == http.MethodPost:
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}\n"))
	case of == "" || !get || files != "files" && !strings.HasPrefix(files, "files/"):
		http.NotFound(w, r)
	case h.sending(of) == nil:
		http.Error(w, "the server sends no game files of "+l.titleByID(of).label(), http.StatusForbidden)
	case files == "files":
		w.Header().Set("Content-Type", "application/json")
		w.Write(h.listFiles(h.sending(of)).body)
	default:
		h.serveLauncherFile(w, r, h.sending(of), strings.TrimPrefix(files, "files/"))
	}
}

// pcTitle reports whether of is the id of a title that its players play on their own PCs, which a launcher starts.
func pcTitle(of string) bool { return of == t6 || of == t7 }

// serveLauncherStarted answers POST /launcher/started, which a launcher sends as it starts a try, before it looks for
// a later release of itself (launcher/update.go). Its first call that asks what to play may come a second or more
// later, after a download of the release. The player's page hears of the launcher at once instead: their room's
// button says that it starts, and their page knows that the click started a launcher (web/src/windows.ts:
// openLauncher). The server takes this call from a launcher of any version, because it is all the call does: the
// launcher's next calls show whether the server takes it (launcherFits).
func (h *web) serveLauncherStarted(w http.ResponseWriter, r *http.Request) {
	l := h.lobby
	l.mu.Lock()
	p := l.launcherOf(r)
	if p != nil {
		l.launcherStarted(p, r.Header.Get(launcherapi.VersionHeader))
	}
	l.mu.Unlock()
	if p == nil {
		launcherUnknown(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}\n"))
}

// serveLauncherStopped answers POST /launcher/stopped, which a launcher sends as it ends because its player quit it,
// or because it has to be installed again (launcherapi.Report: Setup). The player's room then says at once that no
// launcher runs, and its button opens one again. A launcher that ends
// without the call, as one that was killed does, counts as running for launcherQuiet more. Like the call that says a
// launcher started, the server takes this one from a launcher of any version.
func (h *web) serveLauncherStopped(w http.ResponseWriter, r *http.Request) {
	l := h.lobby
	l.mu.Lock()
	p := l.launcherOf(r)
	if p != nil {
		l.launcherQuit(p)
	}
	l.mu.Unlock()
	if p == nil {
		launcherUnknown(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{}\n"))
}

// launcherUnknown answers a launcher's call that carries a key the server doesn't know.
func launcherUnknown(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Key")
	http.Error(w, "The server doesn't know this player: open its page, and join from there again.", http.StatusUnauthorized)
}

// launcherHold is how long the server holds a launcher's call whose answer has not changed (serveLauncherPlay). It is
// shorter than the 60 seconds after which an Nginx proxy gives up on a request. Tests lower it.
var launcherHold = 45 * time.Second

// serveLauncherPlay answers GET /launcher/play: what the player's PC is to play now. A launcher that has an answer
// sends the answer's version (seen). The server then holds the call until it has another answer, or for launcherHold,
// and the launcher asks again at once. The launcher so has one call open for as long as it runs. It learns of a
// change when the change happens, and makes no call on a timer. While the server holds its call, the launcher counts
// as running (launcherOpen).
func (h *web) serveLauncherPlay(w http.ResponseWriter, r *http.Request, of string) {
	l := h.lobby
	seen, ready := r.URL.Query().Get("seen"), r.URL.Query().Get("ready") == "1"
	game := r.URL.Query().Get("game") == "1"
	// has is there once the launcher knows the game folder that the player's game starts from, even when the folder
	// has none of the zones. own says that the folder is the player's own copy of the game.
	has, copied := r.URL.Query()["has"]
	if copied {
		has = strings.FieldsFunc(has[0], func(c rune) bool { return c == ',' })
	}
	own := copied && r.URL.Query().Get("own") == "1"
	limit := time.NewTimer(launcherHold)
	defer limit.Stop()
	l.mu.Lock()
	p := l.launcherOf(r)
	if p == nil {
		l.mu.Unlock()
		launcherUnknown(w)
		return
	}
	p.addr = pageAddr(r)
	l.launcherAt(p, of)
	l.launcherCalled(p, true, ready)
	if p.launcher.game != game {
		p.launcher.game = game
		l.change()
	}
	l.launcherHas(p, copied, has, own)
	play := h.launcherAnswer(l.launcherPlay(p))
	gone, unknown := false, false
	if seen != "" && seen == play.Version {
		p.launcher.held++
		for held := true; held; {
			changed, over := l.changed, false
			l.mu.Unlock()
			select {
			case <-changed:
			case <-limit.C:
				over = true
			case <-r.Context().Done():
				gone = true
			case <-l.ctx.Done():
				gone = true
			}
			l.mu.Lock()
			switch {
			case gone:
				held = false
			case l.launcherOf(r) != p:
				unknown, held = true, false
			default:
				play = h.launcherAnswer(l.launcherPlay(p))
				held = !over && play.Version == seen
			}
		}
		p.launcher.held--
		if p.launcher.quit && p.launcher.held == 0 {
			l.launcherGone(p) // the launcher said that it was quit, and this was the call it had open (launcherQuit)
		} else {
			p.launcher.seen = time.Now() // the launcher counts as running for launcherQuiet more, in which it asks again
		}
	}
	l.mu.Unlock()
	switch {
	case gone:
	case unknown:
		launcherUnknown(w)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(play)
	}
}

// launcherAnswer completes the lobby's answer to GET /launcher/play with what the web side knows, and sets the
// answer's version: a hash of the rest of the answer.
func (h *web) launcherAnswer(play launcherapi.Play) launcherapi.Play {
	for of, t := range play.Titles {
		t.Downloads = h.sending(of) != nil
		play.Titles[of] = t
	}
	play.Downloads = play.Titles[t6].Downloads
	if play.Game != nil {
		play.Game.Port = h.publicPort
	}
	play.Version = ""
	data, _ := json.Marshal(play)
	sum := sha256.Sum256(data)
	play.Version = hex.EncodeToString(sum[:8])
	return play
}

// serveLauncherFile sends one of a title's game files, by its path in the list. It sends from the offset in the request's
// Range header, at the server's download rate (h.pace). The file's ETag is the hash of its contents, as the list has
// it. A launcher that asks for the rest of a file sends the hash of the part it has (If-Range). It gets the whole file
// when the file's contents now have another hash.
func (h *web) serveLauncherFile(w http.ResponseWriter, r *http.Request, s *sentFiles, rel string) {
	list := h.listOf(s)
	i, ok := slices.BinarySearchFunc(list.Files.Files, rel, func(f launcherapi.File, rel string) int {
		return strings.Compare(f.Path, rel)
	})
	if !ok {
		http.NotFound(w, r)
		return
	}
	listed := list.Files.Files[i]
	f, err := os.Open(list.disk[rel])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// Send the file only as the list has it. A file that changed since is another file, which the next list names.
	if st, err := f.Stat(); err != nil || st.Size() != listed.Size {
		http.Error(w, "the game files changed: ask for their list again", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+listed.Hash+`"`)
	http.ServeContent(w, r, "", time.Time{}, &pacedFile{f: f, pace: h.pace, ctx: r.Context()})
}

// pacedFile reads a file one block at a time, and waits for the pacer before each block (files.go), as the sending of
// a pack's pieces does.
type pacedFile struct {
	f    *os.File
	pace *pacer
	ctx  context.Context
}

func (p *pacedFile) Read(b []byte) (int, error) {
	b = b[:min(len(b), packBlock)]
	if err := p.pace.wait(p.ctx, len(b)); err != nil {
		return 0, err
	}
	return p.f.Read(b)
}

func (p *pacedFile) Seek(offset int64, whence int) (int64, error) { return p.f.Seek(offset, whence) }

// ---- the lobby's part, called with l.mu held ----

// launcherReport is what the server knows about a player's launcher: whether it is running, whether the player's PC
// has what the game needs, and the progress of its download of the game files, as the launcher last reported it.
type launcherReport struct {
	launcherapi.Report
	at    time.Time // when the launcher sent the report
	began time.Time // when the current download began
	seen  time.Time // when the launcher last made any call; zero for a launcher that is not running
	held  int       // how many of the launcher's calls the server holds now (serveLauncherPlay)
	// of is the title that the launcher is at, as its last GET /launcher/play said (launcherAt): the title of its
	// player's room, or the one that the player plays alone. ready, copied and has are of that title.
	of    string
	ready bool // the PC has what the game needs, as of the last GET /launcher/play; the launcher waits for the game
	game  bool // the player's game runs, which the launcher started, as of the last GET /launcher/play
	// waits is set while the server keeps the room's game from the launcher, until the game of another player at the
	// same address has joined another room's game (pcWaits).
	waits bool
	// old is set when the server refused a call of the player's launcher for its version (launcherFits), until a
	// launcher of theirs that the server takes calls.
	old bool
	// quit is set when the launcher said that its player quit it while the server still held a call of it
	// (launcherQuit), until that call ends, or a launcher of theirs calls again.
	quit bool
	// copied is set once the launcher has said which of the maps' zones the player's game folder has (launcherHas),
	// and has are those zones. A room on a map in another zone shows the player as missing the map (pcLacks). own is
	// set when the folder is the player's own copy of the game, which the launcher only reads: it downloads a map
	// only into a folder of game files that it downloaded itself.
	copied bool
	has    []string
	own    bool
}

// launcherQuiet is how long a launcher may have no call open, and make none, before the server considers it stopped.
// A running launcher has a call that the server holds, and asks again as soon as that call is answered.
const launcherQuiet = 15 * time.Second

// launcherOf returns the player whose key the launcher's call carries. It returns nil for a key that the server
// doesn't know. On a server where players sign in with Discord, it also returns nil when the player has no session
// left: they signed out, or were signed out.
func (l *lobby) launcherOf(req *http.Request) *player {
	key, ok := strings.CutPrefix(req.Header.Get("Authorization"), launcherapi.Key)
	if !ok || key == "" {
		return nil
	}
	p := l.keys[key]
	if p == nil || l.discord != nil && !l.hasSession(p) {
		return nil
	}
	return p
}

// launcherPlay returns what p's PC is to play now. That is their room's game when the room is a PC room, p has a
// seat in the game, and p's own game is not in it yet. launcherPlay returns the game from its start, once the
// server's copy of Plutonium or BOIII is up to date for it (pcJoins), and before the game is up. The launcher
// then starts the player's game at once, because it takes time to start. The relay lets a player's game in once the
// room's game is up (pcSeat), and drops what it sends before. The answer says when that is (Game.Up): the launcher
// has the player's game ask to join from then, also a game that it has just started, because a game on Plutonium
// asks once, as it starts, and not again when no answer comes (launcher/main.go: stayWith). The answer also has what
// the server says of each PC title that it hosts (launcherapi.Title): the zones of its maps, and for Black Ops II the
// revision of the server's copy of Plutonium, which the player's copy has to match. The caller sets the port, and
// which titles' game files the server sends (launcherAnswer). The caller holds l.mu.
//
// The launcher is sent the game a little later when another room plays Black Ops II too, and a player of that room
// is at p's address, whose game is on its way into that room's game (pcWaits). A game that the launcher has been
// sent stays sent.
//
// A player who joins by hand (setHand) has their own game join from its console. Their launcher is sent no game, as
// it isn't while the room waits for its game: it neither starts their game nor has a game of theirs that runs join.
func (l *lobby) launcherPlay(p *player) launcherapi.Play {
	play := launcherapi.Play{Player: p.asked}
	// the zones of each PC title's maps that the server has, each once, in the same order each time
	for _, name := range l.maps() {
		of, zone := titleOf(name), pcZone(name)
		if zone == "" {
			continue
		}
		if play.Titles == nil {
			play.Titles = map[string]launcherapi.Title{}
		}
		if t := play.Titles[of]; !slices.Contains(t.Zones, zone) {
			t.Zones = append(t.Zones, zone)
			slices.Sort(t.Zones)
			play.Titles[of] = t
		}
	}
	if title := l.t6(); title != nil && title.pluto != nil {
		play.Plutonium = title.pluto.Revision()
		if t, ok := play.Titles[t6]; ok {
			t.Plutonium = play.Plutonium
			play.Titles[t6] = t
		}
	}
	r := p.room
	if r == nil || !pcTitle(titleOf(r.mapName)) {
		return play
	}
	play.Room, play.Of, play.Needs = r.code, titleOf(r.mapName), pcZone(r.mapName)
	if play.Of == t6 {
		play.Zones = play.Titles[t6].Zones // as a launcher from before the answer had titles reads them
	}
	s := r.seatOf(p)
	password, joins := pcJoins(r.game)
	// The room's game quits in a moment, with p's game in it: the launcher has p's game leave it first (quit).
	play.Leave = s != nil && r.game.leaving && s.in && !s.left && !p.byHand
	if s == nil || !joins || s.left || r.game.ending != "" {
		return play
	}
	if play.InGame = s.in; !s.in && !p.byHand {
		if s.sent.IsZero() {
			waits, whose := l.pcWaits(p)
			if waits && !p.launcher.waits {
				log.Printf("server: %s's launcher waits to join room %s's game until the game of %s has joined theirs: both are at %s",
					p.name, r.code, whose, p.addr)
			}
			if p.launcher.waits = waits; waits {
				return play
			}
			s.sent = time.Now()
		}
		play.Game = &launcherapi.Game{ID: strconv.FormatInt(r.game.began.UnixMilli(), 36), Password: password, Name: p.name,
			Up: !r.game.up.IsZero()}
	}
	return play
}

// pcZone returns the game file that the zone of a PC title's map is, by its path in the game's folder, with slashes: a
// copy of the game has the map when it has this file. It returns "" for a map of no PC title.
func pcZone(mapName string) string {
	if zone := t6Zone(mapName); zone != "" {
		return zone
	}
	return t7Zone(mapName)
}

// pcJoins returns the password that a player's game joins the PC game rg with. joins is false until their launchers
// may be sent the game: the server's copy of what the game runs on, Plutonium or BOIII, is up to date for it
// (t6Prepare, t7Prepare). A launcher matches Plutonium's revision before it starts its player's game.
func pcJoins(rg *roomGame) (password string, joins bool) {
	if rg == nil || rg.g == nil {
		return "", false
	}
	if t := rg.g.t6(); t != nil {
		return t.password, t.updated.Load()
	}
	if t := rg.g.t7(); t != nil {
		return t.password, t.entered.Load()
	}
	return "", false
}

// pcLacks reports whether p's game folder lacks the zone of their room's map, as their launcher says (launcherHas): a
// copy of the game without the map's pack. The launcher gets the rest of what the PC lacks. pcLacks is false while
// the launcher has said nothing of the folder of the room's title: it is not running, it is still getting the game
// files, it is at another title, or it is a launcher from before it said. A player who joins by hand is not checked
// either.
func (l *lobby) pcLacks(p *player) bool {
	zone := pcZone(p.room.mapName)
	return zone != "" && p.launcher.of == titleOf(p.room.mapName) && p.launcher.copied &&
		!slices.Contains(p.launcher.has, zone)
}

// pcShowMember adds what a PC room shows of its member m: their launcher's download of the game files, and whether
// their copy of the game lacks the room's map (pcLacks).
func (l *lobby) pcShowMember(r *room, m *player, mv *memberView) {
	if !mv.InGame {
		mv.Downloading, mv.Got = m.downloading()
	}
	mv.Missing = r.game == nil && !mv.Downloading && l.missing(m)
}

// launcherAt records the title that p's launcher is at (GET /launcher/play: of). What it said of the PC and of the
// game folder while it was at another title is not of this one: it goes. The caller holds l.mu.
func (l *lobby) launcherAt(p *player, of string) {
	if p.launcher.of == of {
		return
	}
	if p.launcher.of != "" {
		p.launcher.ready, p.launcher.copied, p.launcher.has, p.launcher.own = false, false, nil, false
	}
	p.launcher.of = of
	l.change()
}

// launcherReady reports whether p's launcher runs and has said that the PC has what the game needs: the game of the
// title of p's room, when that is a PC room. A launcher that is at another title has yet to see to this one's. The
// caller holds l.mu.
func (l *lobby) launcherReady(p *player) bool {
	if !l.launcherOpen(p) || !p.launcher.ready {
		return false
	}
	return p.room == nil || !pcTitle(titleOf(p.room.mapName)) || p.launcher.of == titleOf(p.room.mapName)
}

// launcherGame reports whether p's launcher runs and has said that it started the player's game, which runs: the
// game of the title of p's room, when that is a PC room, as for launcherReady. Their room then offers no launch of
// the game. The caller holds l.mu.
func (l *lobby) launcherGame(p *player) bool {
	if !l.launcherOpen(p) || !p.launcher.game {
		return false
	}
	return p.room == nil || !pcTitle(titleOf(p.room.mapName)) || p.launcher.of == titleOf(p.room.mapName)
}

// launcherMaps returns the maps that the game folder of p's launcher has, of the title that the launcher is at, as it
// said (launcherHas). known is false while it has said nothing of the folder. The caller holds l.mu.
func (l *lobby) launcherMaps(p *player) (maps []string, known bool) {
	if !l.launcherOpen(p) || !p.launcher.copied {
		return nil, false
	}
	maps = []string{}
	for _, name := range l.maps() {
		if titleOf(name) == p.launcher.of && slices.Contains(p.launcher.has, pcZone(name)) {
			maps = append(maps, name)
		}
	}
	return maps, true
}

// launcherOpen reports whether p's launcher is running: the server holds a call of it, or it called less than
// launcherQuiet ago. The caller holds l.mu.
func (l *lobby) launcherOpen(p *player) bool {
	return p.launcher.held > 0 || !p.launcher.seen.IsZero() && time.Since(p.launcher.seen) < launcherQuiet
}

// launcherStarted records that p's launcher started a try (serveLauncherStarted), of the version it says. It counts as
// running from then on, for launcherQuiet, in which its next call comes unless a download of a later release takes
// longer. The last try's failure goes, as the launcher takes it back once it calls again. The caller holds l.mu.
func (l *lobby) launcherStarted(p *player, version string) {
	log.Printf("server: %s's launcher started (version %q)", p.asked, version)
	was := l.launcherOpen(p)
	p.launcher.seen, p.launcher.quit = time.Now(), false
	if !was || p.launcher.Failed != "" {
		p.launcher.Failed = ""
		l.change()
	}
}

// launcherCalled records a call from p's launcher, which shows that the launcher is running. For a call to
// /launcher/play, it also records whether the PC has what the game needs (ready). The caller holds l.mu.
func (l *lobby) launcherCalled(p *player, play, ready bool) {
	was, wasReady, wasOld := l.launcherOpen(p), p.launcher.ready, p.launcher.old
	p.launcher.seen, p.launcher.old, p.launcher.quit = time.Now(), false, false
	if play {
		p.launcher.ready = ready
	}
	if !was || wasReady != p.launcher.ready || wasOld {
		l.change()
	}
}

// launcherHas records which of the maps' zones p's launcher says the player's game folder has (launcherPlay: Zones),
// and whether the folder is the player's own copy of the game (own). copied is false while the launcher has not found
// the folder yet. A ready player whose folder now lacks the zone of their room's map is no longer ready, as when a
// page removes a map's files (setFiles). The caller holds l.mu.
func (l *lobby) launcherHas(p *player, copied bool, has []string, own bool) {
	if copied == p.launcher.copied && slices.Equal(has, p.launcher.has) && own == p.launcher.own {
		return
	}
	p.launcher.copied, p.launcher.has, p.launcher.own = copied, has, own
	if p.room != nil && p.ready && l.missing(p) {
		p.ready = false
		l.checkReady(p.room)
	}
	l.change()
}

// preparing reports whether p's launcher is running and has not yet reported that p's PC has what the game of their
// room's title needs (launcherReady). The launcher is then getting Plutonium or BOIII, or downloading the game files.
// A PC game waits for such a player beyond loadTimeout (pcLate). The caller holds l.mu.
func (l *lobby) preparing(p *player) bool {
	return l.launcherOpen(p) && !l.launcherReady(p)
}

// setLauncher stores the progress that p's launcher reports for its download. p's room shows it (view), and the server
// logs when the download begins and ends.
func (l *lobby) setLauncher(p *player, rep launcherapi.Report) {
	rep.Need = max(rep.Need, 0)
	rep.Got = min(max(rep.Got, 0), rep.Need)
	rep.Failed, rep.Doing = logLine(rep.Failed, 300), logLine(rep.Doing, 40)
	was := p.launcher
	// A launcher that has to be installed again says why in Failed, for a server that doesn't know Setup. This one
	// shows the room the line that installs it instead (youView: LauncherOld), as for a launcher that it doesn't take.
	// The room has it until the next report of a launcher of the player's: the launcher ends.
	if rep.Setup {
		if !was.Setup {
			log.Printf("server: %s's launcher has to be installed again on their Mac: %s", p.asked, rep.Failed)
		}
		rep.Failed = ""
	}
	if rep.Failed != "" && rep.Failed != was.Failed {
		log.Printf("server: %s's launcher failed: %s", p.asked, rep.Failed)
	}
	p.launcher.Report, p.launcher.at = rep, time.Now()
	switch {
	case rep.Running && !was.Running:
		p.launcher.began = p.launcher.at
		log.Printf("files: %s downloads %s game files: %d MB", p.asked, l.launcherTitle(p), rep.Need/1e6)
	case !rep.Running && was.Running:
		l.launcherStopped(p, rep)
	}
	if rep != was.Report {
		l.change()
	}
}

// launcherStopped logs the end of p's download. last is the launcher's last report, and the download is taken to have
// ended when the launcher sent it.
func (l *lobby) launcherStopped(p *player, last launcherapi.Report) {
	took := p.launcher.at.Sub(p.launcher.began)
	rate := float64(last.Got) * 8 / 1e6 / max(took.Seconds(), 0.001)
	if last.Got >= last.Need {
		log.Printf("files: %s downloaded %s game files: %d MB in %s (%.0f Mbit/s)", p.asked, l.launcherTitle(p), last.Got/1e6,
			took.Round(time.Second), rate)
	} else {
		log.Printf("files: %s stopped downloading %s game files at %d of %d MB, after %s (%.0f Mbit/s)",
			p.asked, l.launcherTitle(p), last.Got/1e6, last.Need/1e6, took.Round(time.Second), rate)
	}
}

// launcherTitle names the title that p's launcher is at, for the log of its download: "Black Ops II's".
func (l *lobby) launcherTitle(p *player) string {
	if t := l.titleByID(p.launcher.of); t != nil {
		return t.label() + "'s"
	}
	return "the"
}

// launcherQuit records that p's launcher ended because its player quit it, or because it has to be installed again
// (serveLauncherStopped). It no longer counts
// as running, where a launcher that only makes no more calls does for launcherQuiet more. The launcher may still be
// closing the call that the server holds of it: it then counts as running until that call ends (serveLauncherPlay).
// The caller holds l.mu.
func (l *lobby) launcherQuit(p *player) {
	log.Printf("server: %s's launcher stopped", p.asked)
	if p.launcher.held > 0 {
		p.launcher.quit = true
		return
	}
	l.launcherGone(p)
}

// launcherGone forgets what p's launcher said of the player's PC, once the launcher has stopped. The player's room
// then no longer shows that their launcher runs, their download, or what their game folder has. The caller holds
// l.mu.
func (l *lobby) launcherGone(p *player) {
	p.launcher.seen, p.launcher.ready, p.launcher.Failed, p.launcher.Doing = time.Time{}, false, "", ""
	p.launcher.copied, p.launcher.has, p.launcher.quit, p.launcher.of = false, nil, false, ""
	p.launcher.game, p.launcher.own = false, false
	l.change()
}

// launcherSweep handles a launcher that has had no call open, and made none, for launcherQuiet. The launcher has
// stopped: it was closed, or its PC is gone. The player's room then no longer shows their download, or what their
// game folder has. launcherSweep also wakes the call of a launcher that waited for another player's game and no
// longer does (pcWaits), so that the call answers with the room's game. The caller holds l.mu.
func (l *lobby) launcherSweep(p *player) {
	if p.launcher.waits {
		if waits, _ := l.pcWaits(p); !waits {
			p.launcher.waits = false
			l.change()
		}
	}
	if p.launcher.held == 0 && !p.launcher.seen.IsZero() && time.Since(p.launcher.seen) >= launcherQuiet {
		l.launcherGone(p)
	}
	if !p.launcher.Running || time.Since(p.launcher.at) < launcherQuiet {
		return
	}
	p.launcher.Running = false
	l.launcherStopped(p, p.launcher.Report)
	l.change()
}

// downloading reports whether p's launcher is downloading the game files now, and its progress from 0 to 1.
func (p *player) downloading() (bool, float64) {
	if !p.launcher.Running {
		return false, 0
	}
	if p.launcher.Need <= 0 {
		return true, 0
	}
	return true, min(float64(p.launcher.Got)/float64(p.launcher.Need), 1)
}
