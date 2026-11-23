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
	"time"

	"zombies/internal/launcherapi"
)

// The server's side of the launcher (launcher/: zombies-launcher.exe, on a player's PC). The package
// internal/launcherapi defines what the two send each other.
//
// The players of a Black Ops II room's game join it from their own PCs (t6.go), and a page can't do that for them. The
// launcher does. A link starts the launcher, and the room's button opens the link. The link names the server and the
// player, by the player's key. While its player is in a Black Ops II room, and for five minutes after, the launcher
// has one call open that asks the server what to play (GET /launcher/play). The server holds the call until its
// answer changes (serveLauncherPlay). The answer has
// the room's game, from when the game starts, with the game's password and the name the player has in it. The
// launcher then starts the player's own game, which joins the room's game.
//
// Before that, the launcher makes sure that the PC has what the game needs. Its copy of Plutonium has to be of the
// same revision as the server's, which the answer has. When it is not, the launcher gets Plutonium from Plutonium's
// own updater, as the server does (internal/plutonium).
//
// When the PC has no game files of Black Ops II and the server sends game files (-download-rate), the launcher
// downloads them from the server. It fetches their list
// (GET /launcher/t6/files), which has the hash of each file's contents, and then each file
// (GET /launcher/t6/files/<path>), from where an earlier download stopped. The files are sent at the rate that all the
// server's downloads share (web.pace). The launcher reports the progress of its download (POST /launcher/files). The
// player's room shows it on the player, as it shows a browser's download of a T5 map. The game waits for a player
// whose launcher is still preparing (t6Late, preparing), until the launcher reports that the PC is ready (?ready=1).
//
// The launcher's calls carry the player's key. Like a call from the player's page, a launcher's call sets the address
// where the server expects the player's game (player.addr). On a server where players sign in with Discord, the key
// works only while the player has a session.
//
// The launcher's calls also carry its version. The server takes only the versions that its code works with
// (launcherapi.MinVersion), and a dev build (version 0) only on a dev server (-dev). It refuses any other launcher's
// calls (launcherFits), and notes on the player that their launcher is too old. Their room then says so, with the
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
	disk map[string]string // each file's path on disk, by its path in the list
	body []byte            // the list as served
}

// launcherSkipDirs are the folders in a title's game files that the launcher does not download. They hold what a game
// writes there, not the game's own files.
var launcherSkipDirs = map[string]bool{"players": true, "$recycle.bin": true}

// t6Files lists T6's game files as they are now: the files in their folder, each by its path, with its size and the
// hash of its contents. The server stores the hashes (hashes.go), so only the first listing reads the files (about
// 9 GB, which takes a minute). The server sends files by this list until it makes the next one (t6List).
func (h *web) t6Files() *launcherFiles {
	var entries []*manifestEntry
	filepath.WalkDir(h.t6Dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if file != h.t6Dir && launcherSkipDirs[strings.ToLower(d.Name())] {
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
		rel, _ := filepath.Rel(h.t6Dir, file)
		if rel = filepath.ToSlash(rel); !launcherapi.LocalPath(rel) {
			log.Printf("files: Black Ops II's %q is not sent: a player's PC may not take its name", rel)
			return nil
		}
		entries = append(entries, &manifestEntry{rel: rel, file: file, size: info.Size(), modTime: info.ModTime().UnixNano()})
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
		list.Files.Files = append(list.Files.Files, launcherapi.File{Path: e.rel, Size: e.size, Hash: e.hash})
		list.Bytes += e.size
		list.disk[e.rel] = e.file
		fmt.Fprintf(sum, "%s\x00%s\x00", e.rel, e.hash)
	}
	list.Version = "v" + hex.EncodeToString(sum.Sum(nil))[:16]
	list.body, _ = json.Marshal(list.Files)
	h.t6mu.Lock()
	h.t6list = list
	h.t6mu.Unlock()
	return list
}

// t6List returns the list of T6's game files that the server last made. It makes the list when there is none yet.
func (h *web) t6List() *launcherFiles {
	h.t6mu.Lock()
	list := h.t6list
	h.t6mu.Unlock()
	if list == nil {
		list = h.t6Files()
	}
	return list
}

// sendsT6 reports whether the server sends Black Ops II's game files: it sends game files, and it has T6's.
func (h *web) sendsT6() bool { return h.downloads && h.t6Dir != "" }

// launcherFits reports whether the server takes the calls of the launcher that made the call: one of a version that
// the server's code works with (launcherapi.MinVersion), or a dev build on a dev server. When it does not, it answers
// the call with 426 Upgrade Required, and notes on the call's player that their launcher is too old
// (launcherReport.old). A launcher looks for a later release before its first call but the one that says it started
// (serveLauncherStarted), so the server refuses only one that found none it could take. That launcher no longer counts
// as running.
func (h *web) launcherFits(w http.ResponseWriter, r *http.Request) bool {
	sent := r.Header.Get(launcherapi.VersionHeader)
	version, err := strconv.ParseInt(sent, 10, 64)
	if err == nil && (version >= launcherapi.MinVersion || version == 0 && h.dev) {
		return true
	}
	l := h.lobby
	l.mu.Lock()
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
	if !h.launcherFits(w, r) {
		return
	}
	l := h.lobby
	what := strings.TrimPrefix(r.URL.Path, "/launcher/")
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	if what == "play" && get {
		h.serveLauncherPlay(w, r)
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
	case !strings.HasPrefix(what, "t6/files") || !get:
		http.NotFound(w, r)
	case !h.sendsT6():
		http.Error(w, "the server sends no game files of Black Ops II", http.StatusForbidden)
	case what == "t6/files":
		w.Header().Set("Content-Type", "application/json")
		w.Write(h.t6Files().body)
	case strings.HasPrefix(what, "t6/files/"):
		h.serveLauncherFile(w, r, strings.TrimPrefix(what, "t6/files/"))
	default:
		http.NotFound(w, r)
	}
}

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

// serveLauncherStopped answers POST /launcher/stopped, which a launcher sends as it ends because its player quit it.
// The player's room then says at once that no launcher runs, and its button opens one again. A launcher that ends
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
func (h *web) serveLauncherPlay(w http.ResponseWriter, r *http.Request) {
	l := h.lobby
	seen, ready := r.URL.Query().Get("seen"), r.URL.Query().Get("ready") == "1"
	// has is there once the launcher knows the game folder that the player's game starts from, even when the folder
	// has none of the zones
	has, copied := r.URL.Query()["has"]
	if copied {
		has = strings.FieldsFunc(has[0], func(c rune) bool { return c == ',' })
	}
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
	l.launcherCalled(p, true, ready)
	l.launcherHas(p, copied, has)
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
	play.Downloads = h.sendsT6()
	if play.Game != nil {
		play.Game.Port = h.publicPort
	}
	play.Version = ""
	data, _ := json.Marshal(play)
	sum := sha256.Sum256(data)
	play.Version = hex.EncodeToString(sum[:8])
	return play
}

// serveLauncherFile sends one of T6's game files, by its path in the list. It sends from the offset in the request's
// Range header, at the server's download rate (h.pace). The file's ETag is the hash of its contents, as the list has
// it. A launcher that asks for the rest of a file sends the hash of the part it has (If-Range). It gets the whole file
// when the file's contents now have another hash.
func (h *web) serveLauncherFile(w http.ResponseWriter, r *http.Request, rel string) {
	list := h.t6List()
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
	ready bool      // the PC has what the game needs, as of the last GET /launcher/play; the launcher waits for the game
	// waits is set while the server keeps the room's game from the launcher, until the game of another player at the
	// same address has joined another room's game (t6Waits).
	waits bool
	// old is set when the server refused a call of the player's launcher for its version (launcherFits), until a
	// launcher of theirs that the server takes calls.
	old bool
	// quit is set when the launcher said that its player quit it while the server still held a call of it
	// (launcherQuit), until that call ends, or a launcher of theirs calls again.
	quit bool
	// copied is set once the launcher has said which of the maps' zones the player's game folder has (launcherHas),
	// and has are those zones. A room on a map in another zone shows the player as missing the map (t6Title.lacks).
	copied bool
	has    []string
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

// launcherPlay returns what p's PC is to play now. That is their room's game when the room plays Black Ops II, p has a
// seat in the game, and p's own game is not in it yet. launcherPlay returns the game from its start, once the
// server's copy of Plutonium is up to date for it (t6Prepare), and before Plutonium's server is up. The launcher
// then starts the player's game at once, because it takes time to start. The player's game asks to join for half a
// minute from then, and the relay lets it in once Plutonium's server is up (t6Seat). The answer says when that is
// (Game.Up): a player's game that runs already, from the room's last game, joins only then. The answer has the
// revision of the server's copy of Plutonium, which the player's copy has to match. The caller sets the port
// (launcherAnswer). The caller holds l.mu.
//
// The launcher is sent the game a little later when another room plays Black Ops II too, and a player of that room
// is at p's address, whose game is on its way into that room's game (t6Waits). A game that the launcher has been
// sent stays sent.
//
// A player who joins by hand (setHand) has their own game join from its console. Their launcher is sent no game, as
// it isn't while the room waits for its game: it neither starts their game nor has a game of theirs that runs join.
func (l *lobby) launcherPlay(p *player) launcherapi.Play {
	play := launcherapi.Play{Player: p.asked}
	if title := l.t6(); title != nil && title.pluto != nil {
		play.Plutonium = title.pluto.Revision()
	}
	r := p.room
	if r == nil || titleOf(r.mapName) != t6 {
		return play
	}
	play.Room, play.Needs = r.code, t6Zone(r.mapName)
	// the zones of the T6 maps that the server has, each once, in the same order each time
	for _, name := range l.maps() {
		if zone := t6Zone(name); zone != "" && !slices.Contains(play.Zones, zone) {
			play.Zones = append(play.Zones, zone)
		}
	}
	slices.Sort(play.Zones)
	s, t := r.seatOf(p), t6Of(r.game)
	if s == nil || t == nil || s.left || r.game.ending != "" || !t.updated.Load() {
		return play
	}
	if play.InGame = s.in; !s.in && !p.byHand {
		if s.sent.IsZero() {
			waits, whose := l.t6Waits(p)
			if waits && !p.launcher.waits {
				log.Printf("server: %s's launcher waits to join room %s's game until the game of %s has joined theirs: both are at %s",
					p.name, r.code, whose, p.addr)
			}
			if p.launcher.waits = waits; waits {
				return play
			}
			s.sent = time.Now()
		}
		play.Game = &launcherapi.Game{ID: strconv.FormatInt(r.game.began.UnixMilli(), 36), Password: t.password, Name: p.name,
			Up: !r.game.up.IsZero()}
	}
	return play
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

// launcherHas records which of the maps' zones p's launcher says the player's game folder has (launcherPlay: Zones).
// copied is false while the launcher has not found the folder yet. A ready player whose folder now lacks the zone of
// their room's map is no longer ready, as when a page removes a map's files (setFiles). The caller holds l.mu.
func (l *lobby) launcherHas(p *player, copied bool, has []string) {
	if copied == p.launcher.copied && slices.Equal(has, p.launcher.has) {
		return
	}
	p.launcher.copied, p.launcher.has = copied, has
	if p.room != nil && p.ready && l.missing(p) {
		p.ready = false
		l.checkReady(p.room)
	}
	l.change()
}

// preparing reports whether p's launcher is running and has not yet reported that p's PC has what the game needs. The
// launcher is then getting Plutonium or downloading the game files. A T6 game waits for such a player beyond
// loadTimeout (t6Late). The caller holds l.mu.
func (l *lobby) preparing(p *player) bool {
	return l.launcherOpen(p) && !p.launcher.ready
}

// setLauncher stores the progress that p's launcher reports for its download. p's room shows it (view), and the server
// logs when the download begins and ends.
func (l *lobby) setLauncher(p *player, rep launcherapi.Report) {
	rep.Need = max(rep.Need, 0)
	rep.Got = min(max(rep.Got, 0), rep.Need)
	rep.Failed, rep.Doing = logLine(rep.Failed, 300), logLine(rep.Doing, 40)
	was := p.launcher
	if rep.Failed != "" && rep.Failed != was.Failed {
		log.Printf("server: %s's launcher failed: %s", p.asked, rep.Failed)
	}
	p.launcher.Report, p.launcher.at = rep, time.Now()
	switch {
	case rep.Running && !was.Running:
		p.launcher.began = p.launcher.at
		log.Printf("files: %s downloads Black Ops II's game files: %d MB", p.asked, rep.Need/1e6)
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
		log.Printf("files: %s downloaded Black Ops II's game files: %d MB in %s (%.0f Mbit/s)", p.asked, last.Got/1e6,
			took.Round(time.Second), rate)
	} else {
		log.Printf("files: %s stopped downloading Black Ops II's game files at %d of %d MB, after %s (%.0f Mbit/s)",
			p.asked, last.Got/1e6, last.Need/1e6, took.Round(time.Second), rate)
	}
}

// launcherQuit records that p's launcher ended because its player quit it (serveLauncherStopped). It no longer counts
// as running, where a launcher that only makes no more calls does for launcherQuiet more. The launcher may still be
// closing the call that the server holds of it: it then counts as running until that call ends (serveLauncherPlay).
// The caller holds l.mu.
func (l *lobby) launcherQuit(p *player) {
	log.Printf("server: %s's launcher was quit", p.asked)
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
	p.launcher.copied, p.launcher.has, p.launcher.quit = false, nil, false
	l.change()
}

// launcherSweep handles a launcher that has had no call open, and made none, for launcherQuiet. The launcher has
// stopped: it was closed, or its PC is gone. The player's room then no longer shows their download, or what their
// game folder has. launcherSweep also wakes the call of a launcher that waited for another player's game and no
// longer does (t6Waits), so that the call answers with the room's game. The caller holds l.mu.
func (l *lobby) launcherSweep(p *player) {
	if p.launcher.waits {
		if waits, _ := l.t6Waits(p); !waits {
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
