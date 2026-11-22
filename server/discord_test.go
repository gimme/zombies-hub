package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDiscord is Discord's OAuth2 and API, as the server uses them (discord.go), for accounts the test makes.
type fakeDiscord struct {
	*httptest.Server
	mu       sync.Mutex
	accounts map[string]*fakeAccount // by id
	codes    map[string]string       // a sign-in's code: the account's id
	access   map[string]string       // an access token: the account's id
	refresh  map[string]string       // a refresh token: the account's id
	asked    map[string]int          // how often each path was asked for
}

type fakeAccount struct {
	username, global, nick string
	avatar, guildAvatar    string   // their account's, and theirs in the Discord server
	member                 bool     // of the Discord server
	roles                  []string // in it
	revoked                bool     // they took the application's access away
}

const (
	testClientID = "1234"
	testSecret   = "s3cret"
	testGuild    = "777"
)

func newFakeDiscord(t *testing.T) *fakeDiscord {
	d := &fakeDiscord{accounts: map[string]*fakeAccount{}, codes: map[string]string{}, access: map[string]string{},
		refresh: map[string]string{}, asked: map[string]int{}}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.Close)
	return d
}

// code is the code Discord sends a player back with, once they authorized the application as the account.
func (d *fakeDiscord) code(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	code := randomHex(8)
	d.codes[code] = id
	return code
}

func (d *fakeDiscord) account(id string) *fakeAccount {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.accounts[id]
}

func (d *fakeDiscord) set(id string, change func(a *fakeAccount)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	change(d.accounts[id])
}

func (d *fakeDiscord) times(path string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked[path]
}

func (d *fakeDiscord) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked[r.URL.Path]++
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	if r.URL.Path == "/api/v10/oauth2/token" {
		if id, secret, _ := r.BasicAuth(); id != testClientID || secret != testSecret {
			http.Error(w, `{"error": "invalid_client"}`, http.StatusUnauthorized)
			return
		}
		r.ParseForm()
		var id string
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			id = d.codes[r.PostForm.Get("code")]
			delete(d.codes, r.PostForm.Get("code"))
		case "refresh_token":
			id = d.refresh[r.PostForm.Get("refresh_token")]
			delete(d.refresh, r.PostForm.Get("refresh_token"))
		}
		if id == "" || d.accounts[id].revoked {
			http.Error(w, `{"error": "invalid_grant"}`, http.StatusBadRequest)
			return
		}
		access, refresh := randomHex(8), randomHex(8)
		d.access[access], d.refresh[refresh] = id, id
		reply(map[string]any{"access_token": access, "refresh_token": refresh, "expires_in": 604800, "token_type": "Bearer"})
		return
	}
	id := d.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	a := d.accounts[id]
	if a == nil || a.revoked {
		http.Error(w, `{"message": "401: Unauthorized", "code": 0}`, http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/api/v10/users/@me":
		reply(map[string]any{"id": id, "username": a.username, "global_name": a.global, "avatar": nullable(a.avatar)})
	case "/api/v10/users/@me/guilds/" + testGuild + "/member":
		if !a.member {
			http.Error(w, `{"message": "Unknown Guild", "code": 10004}`, http.StatusNotFound)
			return
		}
		reply(map[string]any{"nick": nullable(a.nick), "avatar": nullable(a.guildAvatar), "roles": a.roles})
	default:
		http.NotFound(w, r)
	}
}

// nullable is s as Discord sends it: null for none.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// discordLobby is a lobby whose players sign in with the fake Discord, and the server's pages around it.
func discordLobby(t *testing.T, d *fakeDiscord) (*lobby, *web) {
	l := testLobby(t)
	l.discord = &discordAuth{clientID: testClientID, secret: testSecret, server: testGuild, url: d.URL,
		client: d.Client()}
	return l, &web{lobby: l, gameDir: t.TempDir()}
}

// getAt asks the server's pages for the path, at https://zombies.example.com, with the cookies.
func getAt(h http.Handler, path string, cookies ...*http.Cookie) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "zombies.example.com"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func cookieOf(rsp *http.Response, name string) *http.Cookie {
	for _, c := range rsp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// signIn is a player's sign-in, from the page at then, as the account; it answers where it sends the player back to,
// and their session's cookie.
func signIn(t *testing.T, d *fakeDiscord, h http.Handler, then, id string) (string, *http.Cookie) {
	t.Helper()
	rsp := getAt(h, "/auth/discord?then="+url.QueryEscape(then))
	to, _ := url.Parse(rsp.Header.Get("Location"))
	state := cookieOf(rsp, signInCookie)
	if rsp.StatusCode != http.StatusFound || state == nil {
		t.Fatalf("GET /auth/discord: %s, %v", rsp.Status, rsp.Header)
	}
	q := to.Query()
	callback := "/auth/discord/callback?" + url.Values{"code": {d.code(id)}, "state": {q.Get("state")}}.Encode()
	rsp = getAt(h, callback, state)
	if rsp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the callback: %s", rsp.Status)
	}
	return rsp.Header.Get("Location"), cookieOf(rsp, sessionCookie)
}

// helloWith is the page's hello, with its cookie.
func helloWith(l *lobby, cookie *http.Cookie) hello {
	req := httptest.NewRequest(http.MethodPost, "/lobby/hello", strings.NewReader(`{"name": "Whatever"}`))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	var h hello
	json.NewDecoder(rec.Body).Decode(&h)
	return h
}

// Players sign in with Discord, members of its server alone, under their name there: their nickname in the server,
// which the server looks up again as they come back. What the server serves of the game files is theirs alone.
func TestDiscordSignIn(t *testing.T) {
	d := newFakeDiscord(t)
	d.accounts["100"] = &fakeAccount{username: "emile_99", global: "Émile", nick: "Milo [ZM]", member: true,
		avatar: "a1b2", guildAvatar: "c3d4"}
	d.accounts["200"] = &fakeAccount{username: "stranger", member: false}
	l, h := discordLobby(t, d)

	// not signed in: the page has them sign in, and the game files are no one's
	if got := helloWith(l, nil); got.SignIn != "/auth/discord" || !got.Member || got.Token != "" {
		t.Errorf("hello, not signed in: %+v", got)
	}
	for _, path := range []string{"/music", "/sounds", "/strings?keys=X", "/game/manifest.txt", "/maps/zombie_theater/preview",
		"/maps/zombie_theater/files", "/pictures/juggernog.png", "/images/xenon_controller_top.png"} {
		if rsp := getAt(h, path); rsp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s, not signed in: %s", path, rsp.Status)
		}
	}

	// to Discord, and back
	rsp := getAt(h, "/auth/discord?then="+url.QueryEscape("/?room=KXQM"))
	to, _ := url.Parse(rsp.Header.Get("Location"))
	q := to.Query()
	if to.Path != "/oauth2/authorize" || q.Get("client_id") != testClientID || q.Get("scope") != "identify guilds.members.read" ||
		q.Get("redirect_uri") != "https://zombies.example.com/auth/discord/callback" || q.Get("prompt") != "none" ||
		len(q.Get("state")) != 32 {
		t.Errorf("to Discord: %s", to)
	}
	if c := cookieOf(rsp, signInCookie); c == nil || !c.Secure || !c.HttpOnly || c.Path != "/auth/discord" ||
		!strings.HasPrefix(c.Value, q.Get("state")+".") {
		t.Errorf("the sign-in's cookie: %+v", c)
	}
	back, cookie := signIn(t, d, h, "/?room=KXQM", "100")
	if back != "/?room=KXQM" || cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("signed in: back to %q, %+v", back, cookie)
	}
	milo := helloWith(l, cookie)
	if milo.Token != cookie.Value || milo.Name != "Milo [ZM]" || !milo.Discord || milo.Key == "" {
		t.Errorf("hello, signed in: %+v", milo)
	}
	if err := do(l, milo.Token, "create"); err != "" {
		t.Errorf("Milo makes a room: %q", err)
	}
	// his picture is his avatar in the Discord server, by his name in his room
	guildAvatar := "https://cdn.discordapp.com/guilds/777/users/100/avatars/c3d4.png?size=128"
	if v := l.viewOf(milo.Token); v.You.Avatar != guildAvatar || v.Room.Members[0].Avatar != guildAvatar {
		t.Errorf("Milo's picture: %q, in his room %q", v.You.Avatar, v.Room.Members[0].Avatar)
	}
	if rsp := getAt(h, "/game/manifest.txt", cookie); rsp.StatusCode != http.StatusOK {
		t.Errorf("GET /game/manifest.txt, signed in: %s", rsp.Status)
	}

	// another, not in the Discord server, and one who says no, and one who comes back with another's state
	if back, cookie := signIn(t, d, h, "/?room=KXQM", "200"); back != "/?room=KXQM&signin=member" || cookie != nil {
		t.Errorf("a stranger, signing in: back to %q, %+v", back, cookie)
	}
	start := getAt(h, "/auth/discord")
	q = url.Values{"error": {"access_denied"}, "state": {strings.Split(cookieOf(start, signInCookie).Value, ".")[0]}}
	if rsp := getAt(h, "/auth/discord/callback?"+q.Encode(), cookieOf(start, signInCookie)); rsp.Header.Get("Location") != "/?signin=cancelled" {
		t.Errorf("a player who said no: back to %q", rsp.Header.Get("Location"))
	}
	q = url.Values{"code": {d.code("100")}, "state": {"0123456789abcdef0123456789abcdef"}}
	if rsp := getAt(h, "/auth/discord/callback?"+q.Encode(), cookieOf(start, signInCookie)); rsp.Header.Get("Location") != "/?signin=failed" ||
		cookieOf(rsp, sessionCookie) != nil {
		t.Errorf("a callback with another state: back to %q", rsp.Header.Get("Location"))
	}
	if rsp := getAt(h, "/auth/discord?then="+url.QueryEscape("//evil.example/")); rsp.StatusCode != http.StatusFound ||
		!strings.HasSuffix(cookieOf(rsp, signInCookie).Value, ".Lw") { // "/"
		t.Errorf("a sign-in to go back to another site: %+v", cookieOf(rsp, signInCookie))
	}

	// a new nickname: theirs as they come back, once an hour has passed since the server last looked
	d.set("100", func(a *fakeAccount) { a.nick = "" })
	if got := helloWith(l, cookie); got.Name != "Milo [ZM]" {
		t.Errorf("hello, within the hour: %+v", got)
	}
	l.mu.Lock()
	l.playerOf(milo.Token).discord.Checked = time.Now().Add(-discordRecheck)
	l.mu.Unlock()
	if got := helloWith(l, cookie); got.Name != "Emile" || got.ID != milo.ID {
		t.Errorf("hello, once Milo's nickname went (his display name, Émile, is his name): %+v", got)
	}
	// and at once when they ask, but not again within discordRefresh; and with his avatar in the Discord server gone,
	// his picture is his account's
	d.set("100", func(a *fakeAccount) { a.nick, a.guildAvatar = "Milo", "" })
	l.mu.Lock()
	p := l.playerOf(milo.Token)
	p.discord.Checked = time.Now().Add(-discordRefresh)
	l.mu.Unlock()
	if err := do(l, milo.Token, "refresh"); err != "" || l.viewOf(milo.Token).You.Name != "Milo" ||
		l.viewOf(milo.Token).You.Avatar != "https://cdn.discordapp.com/avatars/100/a1b2.png?size=128" {
		t.Errorf("Milo, refreshing his name: %q, %+v", err, l.viewOf(milo.Token).You)
	}
	asked := d.times("/api/v10/users/@me")
	d.set("100", func(a *fakeAccount) { a.nick = "Andrew" })
	if do(l, milo.Token, "refresh"); d.times("/api/v10/users/@me") != asked || l.viewOf(milo.Token).You.Name != "Milo" {
		t.Errorf("Milo, refreshing his name again at once: Discord asked, or %+v", l.viewOf(milo.Token).You)
	}
	d.set("100", func(a *fakeAccount) { a.nick = "Milo" })

	// an expired access token is refreshed
	asked = d.times("/api/v10/oauth2/token")
	l.mu.Lock()
	p.discord.Checked, p.discord.Expires = time.Time{}, time.Now()
	l.mu.Unlock()
	if got := helloWith(l, cookie); got.Name != "Milo" || d.times("/api/v10/oauth2/token") != asked+1 {
		t.Errorf("hello, with Milo's access token expired: %+v", got)
	}

	// Discord down: the server keeps what it knew
	l.discord.url = "http://127.0.0.1:1"
	l.mu.Lock()
	p.discord.Checked = time.Time{}
	l.mu.Unlock()
	if got := helloWith(l, cookie); got.Name != "Milo" {
		t.Errorf("hello, with Discord down: %+v", got)
	}
	l.discord.url = d.URL

	// out of the Discord server: signed out, out of his room, and the game files are his no more
	d.set("100", func(a *fakeAccount) { a.member = false })
	l.mu.Lock()
	p.discord.Checked = time.Time{}
	l.mu.Unlock()
	if got := helloWith(l, cookie); got.SignIn == "" || got.Token != "" {
		t.Errorf("hello, out of the Discord server: %+v", got)
	}
	l.mu.Lock()
	out := p.room == nil && l.playerOf(milo.Token) == nil
	l.mu.Unlock()
	if !out {
		t.Errorf("Milo, out of the Discord server: still in his room, or signed in")
	}
	if rsp := getAt(h, "/game/manifest.txt", cookie); rsp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /game/manifest.txt, signed out: %s", rsp.Status)
	}
	if _, _, _, _, err := l.session(milo.Token); err != errUnknown {
		t.Errorf("a game's session, signed out: %v", err)
	}

	// back in it: the same player, with the same key
	d.set("100", func(a *fakeAccount) { a.member = true })
	_, cookie = signIn(t, d, h, "/", "100")
	if again := helloWith(l, cookie); again.ID != milo.ID || again.Key != milo.Key || again.Token == milo.Token {
		t.Errorf("Milo, signed in again: %+v, was %+v", again, milo)
	}

	// Discord's access taken away: signed out
	d.set("100", func(a *fakeAccount) { a.revoked = true })
	l.mu.Lock()
	l.playerOf(cookie.Value).discord.Checked = time.Time{}
	l.mu.Unlock()
	if got := helloWith(l, cookie); got.SignIn == "" {
		t.Errorf("hello, once Milo took the application's access away: %+v", got)
	}
}

// With roles, only a member with one of them plays.
func TestDiscordRoles(t *testing.T) {
	d := newFakeDiscord(t)
	d.accounts["100"] = &fakeAccount{username: "ann", member: true, roles: []string{"5", "9"}}
	d.accounts["200"] = &fakeAccount{username: "bob", member: true, roles: []string{"5"}}
	l, h := discordLobby(t, d)
	l.discord.roles = []string{"9", "10"}
	if back, cookie := signIn(t, d, h, "/", "100"); back != "/" || cookie == nil {
		t.Errorf("Ann, with role 9: back to %q", back)
	}
	if back, cookie := signIn(t, d, h, "/", "200"); back != "/?signin=role" || cookie != nil {
		t.Errorf("Bob, without: back to %q", back)
	}
}

// Signing out ends the browser's session, and its cookie.
func TestDiscordSignOut(t *testing.T) {
	d := newFakeDiscord(t)
	d.accounts["100"] = &fakeAccount{username: "ann", member: true}
	l, h := discordLobby(t, d)
	_, cookie := signIn(t, d, h, "/", "100")
	_, other := signIn(t, d, h, "/", "100") // in another browser
	ann := helloWith(l, cookie)
	req := httptest.NewRequest(http.MethodPost, "/lobby/signout", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+ann.Token)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if c := cookieOf(rec.Result(), sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("the cookie, signed out: %+v", c)
	}
	if got := helloWith(l, cookie); got.SignIn == "" {
		t.Errorf("hello, signed out: %+v", got)
	}
	if got := helloWith(l, other); got.ID != ann.ID {
		t.Errorf("hello in the other browser: %+v", got)
	}
}

// Once players sign in with Discord, a player of the server from before is no one, nor is their key anyone's.
func TestDiscordAfterAnonymous(t *testing.T) {
	d := newFakeDiscord(t)
	l, _ := discordLobby(t, d)
	l.discord, d.accounts["100"] = nil, &fakeAccount{username: "ann", member: true}
	old := l.hello("", "Ann")
	l.discord = &discordAuth{clientID: testClientID, secret: testSecret, server: testGuild, url: d.URL, client: d.Client()}
	if err := do(l, old.Token, "create"); err != errSignedOut.Error() {
		t.Errorf("a player from before, making a room: %q", err)
	}
	if _, _, _, _, err := l.windowsSeat(old.Key); err == nil || !strings.Contains(err.Error(), "no longer works") {
		t.Errorf("a key from before: %v", err)
	}
}

func TestLocalPath(t *testing.T) {
	for path, want := range map[string]bool{"/": true, "/?room=KXQM": true, "": false, "//evil.example/": false,
		"/\\evil.example": false, "https://evil.example/": false, "room": false, "/\r\nSet-Cookie: x": false} {
		if localPath(path) != want {
			t.Errorf("localPath(%q): %v", path, !want)
		}
	}
	for host, want := range map[string]string{"zombies.example.com": "https", "localhost:3075": "http",
		"127.0.0.1:3075": "http", "[::1]:3075": "http", "192.168.1.5:3075": "https"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if got := redirectURI(req); got != want+"://"+host+"/auth/discord/callback" {
			t.Errorf("redirectURI at %s: %s", host, got)
		}
	}
}
