package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// discordAuth is the sign-in with Discord. When a Discord application's client ID and secret are set
// (-discord-client-id, -discord-client-secret: the host makes an application at
// https://discord.com/developers/applications), players can play only after they have signed in with Discord, and the
// page asks them to. With -discord-server, only the members of that Discord server can play. With -discord-roles, only
// the members with one of those roles can.
//
// A player's name is their name on Discord, as the game takes it (cleanName): their nickname in the Discord server, or
// else their display name, or else their username. A player's picture is their avatar in the Discord server, or else
// their account's avatar (avatar). The page shows it next to their name until they pick one of the server's pictures.
// A player with neither avatar gets one of the server's pictures (pictures.go).
//
// The server looks a player up on Discord again (checkDiscord) when a page of theirs calls hello and discordRecheck
// has passed since the last lookup. It also does so when the player asks, with the button in their name
// (POST /lobby/refresh), once discordRefresh has passed. A new nickname then becomes the player's name, from their
// room's next game when a game is running. A player who left the Discord server, lost the role, or revoked the
// application's access (Discord's Authorized Apps) is signed out.
//
// The sign-in is OAuth2's authorization code grant. Its scopes are identify, and guilds.members.read when a Discord
// server is set. Discord sends the player back to /auth/discord/callback at the page's own host, which the application
// must list as a redirect (docs/hosting.md). The server keeps the player's tokens (players.go), because a later lookup
// needs them. A player who authorized the application before passes through Discord without a prompt (prompt=none).
//
//	GET  /auth/discord           ?then=<path>: redirects to Discord, which asks the player to authorize the
//	                             application and sends them back to the callback. The callback must bring back the
//	                             state that the cookie zombies_signin holds.
//	GET  /auth/discord/callback  ?code&state: the server uses the code to ask Discord who the player is. If the player
//	                             may play, the server signs them in: it makes a session (players.go), sets the cookie
//	                             zombies_session, and redirects to the page at <path>. If not, it redirects there
//	                             with ?signin=<why>. <why> is cancelled (the player declined), member (the player is
//	                             not in the Discord server), role (the player has none of the roles), or failed
//	                             (anything else: the log has the reason).
//	POST /lobby/refresh          {}: the server looks the player up on Discord again, now
//	POST /lobby/signout          {}: the page signs out, and its session ends
//
// The page calls hello with the cookie (rooms.go: hello), and then calls the server with the token as usual. The
// cookie also gives access to what the server serves from the game files (gated). A browser sends the cookie by itself
// with its requests for pictures, music and game files. The page and the web client are served to anyone: they are
// built from this repository and from bo1-zombies. Only a signed-in player can open a browser's session in a room's
// game (relay.go) or use a Windows player's key.
type discordAuth struct {
	clientID, secret string
	server           string   // the ID of the Discord server (guild) whose members may play, or "" for anyone
	roles            []string // the IDs of the roles in it, of which a player needs one; empty for any member
	url              string   // Discord's address: https://discord.com, or a test's server
	client           *http.Client
}

const (
	sessionCookie  = "zombies_session"
	signInCookie   = "zombies_signin"
	discordRecheck = time.Hour
	discordRefresh = 15 * time.Second
	discordTimeout = 10 * time.Second
	// discordCDN is where Discord serves its pictures. The page loads them from there with CORS, which a cross-origin
	// isolated page needs.
	discordCDN = "https://cdn.discordapp.com"
)

// discordUser is a player's Discord account, as the server keeps it (store.go).
type discordUser struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`    // the player's name on Discord, as the server last saw it (lookUp)
	Access  string    `json:"access"`  // the access token that Discord gave the server for the player
	Refresh string    `json:"refresh"` // the refresh token that came with it
	Expires time.Time `json:"expires"` // when the access token expires
	Checked time.Time `json:"checked"` // when the server last looked the player up, or tried to
	// Avatar is the player's picture on Discord, as the server last saw it: a URL on discordCDN, or "" for none.
	Avatar string `json:"avatar,omitempty"`
	// checking is set while the server looks the player up (checkDiscord).
	checking bool
}

// denied is the reason why a Discord account may not play on the server, as the page's ?signin= parameter carries it.
// "member" means that the account is not in the Discord server. "role" means that it has none of the roles.
type denied string

func (d denied) Error() string {
	if d == "role" {
		return "it has none of the roles the server asks for"
	}
	return "it is not in the server's Discord server"
}

var (
	// errRefused means that Discord refused the code of a sign-in, or the player's tokens. The player revoked the
	// application's access, or the tokens lack a scope because the host set a Discord server after the player signed in.
	errRefused  = errors.New("Discord refused the tokens")
	errNotFound = errors.New("not found")
)

// serveAuth answers /auth/... (above).
func (l *lobby) serveAuth(w http.ResponseWriter, req *http.Request) {
	if l.discord == nil || req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch req.URL.Path {
	case "/auth/discord":
		l.discordStart(w, req)
	case "/auth/discord/callback":
		l.discordCallback(w, req)
	default:
		http.NotFound(w, req)
	}
}

// discordStart redirects the player to Discord with a state that the callback must bring back. A cookie keeps the
// state, with the path of the page to return to.
func (l *lobby) discordStart(w http.ResponseWriter, req *http.Request) {
	then := req.URL.Query().Get("then")
	if !localPath(then) {
		then = "/"
	}
	state := randomHex(16)
	http.SetCookie(w, &http.Cookie{Name: signInCookie, Value: state + "." + base64.RawURLEncoding.EncodeToString([]byte(then)),
		Path: "/auth/discord", MaxAge: 600, HttpOnly: true, Secure: secure(req), SameSite: http.SameSiteLaxMode})
	scope := "identify"
	if l.discord.server != "" {
		scope += " guilds.members.read"
	}
	q := url.Values{"response_type": {"code"}, "client_id": {l.discord.clientID}, "scope": {scope}, "state": {state},
		"redirect_uri": {redirectURI(req)}, "prompt": {"none"}}
	http.Redirect(w, req, l.discord.url+"/oauth2/authorize?"+q.Encode(), http.StatusFound)
}

// discordCallback handles the player's return from Discord. It signs the player in, or sends them back to the page
// with the reason why they are not signed in.
func (l *lobby) discordCallback(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	state, then := "", "/"
	if c, err := req.Cookie(signInCookie); err == nil {
		var path string
		state, path, _ = strings.Cut(c.Value, ".")
		if b, err := base64.RawURLEncoding.DecodeString(path); err == nil && localPath(string(b)) {
			then = string(b)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: signInCookie, Path: "/auth/discord", MaxAge: -1, HttpOnly: true,
		Secure: secure(req), SameSite: http.SameSiteLaxMode})
	back := func(why string) {
		u, _ := url.Parse(then)
		v := u.Query()
		v.Set("signin", why)
		u.RawQuery = v.Encode()
		http.Redirect(w, req, u.String(), http.StatusSeeOther)
	}
	switch {
	case state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(q.Get("state"))) != 1:
		// This is not the sign-in that this browser started. It is another browser's, an old one, or one through another
		// host name.
		log.Printf("server: a Discord sign-in came back without the state it went with")
		back("failed")
		return
	case q.Get("error") == "access_denied":
		back("cancelled")
		return
	case q.Get("error") != "":
		log.Printf("server: Discord sign-in failed: %s (%s)", q.Get("error"), q.Get("error_description"))
		back("failed")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), discordTimeout)
	defer cancel()
	u, err := l.discord.signIn(ctx, q.Get("code"), redirectURI(req))
	var no denied
	switch {
	case errors.As(err, &no):
		log.Printf("server: Discord account %s (%s) may not play: %v", u.Name, u.ID, err)
		back(string(no))
		return
	case err != nil:
		log.Printf("server: Discord sign-in failed: %v", err)
		back("failed")
		return
	}
	token, name := l.signedIn(u)
	log.Printf("server: %s signed in with Discord (%s)", name, u.ID)
	setSession(w, req, token)
	http.Redirect(w, req, then, http.StatusSeeOther)
}

// signedIn starts a new session for the player with the Discord account u, who just signed in. The player is one the
// server already knew by that account, or a new one. signedIn returns the session's token and the name the player
// plays under.
func (l *lobby) signedIn(u discordUser) (token, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var p *player
	for _, q := range l.players {
		if q.discord != nil && q.discord.ID == u.ID {
			p = q
		}
	}
	if p == nil {
		p = l.newPlayer()
	}
	p.discord, p.opened, p.asked = &u, true, cleanName(u.Name)
	l.givePicture(p) // in case they have no avatar on Discord
	l.rename(p)
	l.change()
	return l.newSession(p), p.name
}

// discordHello is hello on a server where players sign in with Discord. It returns the player of the session in the
// page's cookie, under their name on Discord. When discordRecheck has passed since the last lookup, it first looks the
// player up on Discord again, which may sign them out. When the cookie's session has no player, it returns the path
// where the page sends the player to sign in.
func (l *lobby) discordHello(token string) hello {
	l.mu.Lock()
	p := l.playerOf(token)
	check := p != nil && l.startCheck(p, discordRecheck)
	l.mu.Unlock()
	if check {
		l.checkDiscord(p)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if p = l.playerOf(token); p == nil {
		return hello{SignIn: "/auth/discord", Member: l.discord.server != ""}
	}
	l.use(token)
	if l.givePicture(p) {
		l.save()
	}
	l.rename(p)
	l.change()
	return hello{Token: token, ID: p.id, Name: p.asked, Key: p.key, Discord: true}
}

// startCheck marks p's Discord account as being looked up (checkDiscord) when the last lookup was longer ago than
// after, and reports whether it did. It returns false when the last lookup was more recent, when a lookup is in
// progress, or when p has no Discord account. The caller holds l.mu.
func (l *lobby) startCheck(p *player, after time.Duration) bool {
	d := p.discord
	if l.discord == nil || d == nil || d.checking || time.Since(d.Checked) < after {
		return false
	}
	d.checking = true
	return true
}

// checkDiscord looks p up on Discord again, after startCheck has marked them. The caller does not hold l.mu. The
// player's name on Discord becomes their name (rename), and a player who may no longer play is signed out (signOut).
// When Discord gives no answer, for example because it is down, the server keeps what it knew and asks again later.
func (l *lobby) checkDiscord(p *player) {
	l.mu.Lock()
	d := p.discord
	u := *d
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(l.ctx, discordTimeout)
	defer cancel()
	checked, err := l.discord.check(ctx, u)
	l.mu.Lock()
	defer l.mu.Unlock()
	d.checking = false
	var no denied
	switch {
	case p.discord != d:
		// The player signed in again in the meantime, and the sign-in looked them up.
	case errors.As(err, &no) || errors.Is(err, errRefused):
		log.Printf("server: %s is signed out: %v", p.name, err)
		l.signOut(p)
	case err != nil:
		log.Printf("server: can't look %s up on Discord: %v", p.name, err)
		d.Checked = time.Now()
	default:
		pictured := checked.Avatar != d.Avatar
		*d = checked
		d.checking = false
		if pictured {
			l.givePicture(p) // in case they no longer have an avatar on Discord
			l.change()
		}
		if asked := cleanName(d.Name); asked != p.asked {
			log.Printf("server: %s's Discord name is now %s", p.name, asked)
			p.asked = asked
			l.rename(p)
			l.change()
		}
		l.save()
	}
}

// signOut ends p's sessions, because p may no longer play. p leaves their room, and with it their character in the
// room's game. The caller holds l.mu.
func (l *lobby) signOut(p *player) {
	l.endSessions(p)
	l.leave(p)
	l.change()
}

// gated reports whether the server serves the path only to signed-in players, on a server where players sign in with
// Discord. These paths serve content from the game files: the files themselves (/game/..., a map's pack), the maps'
// pictures and lists, the players' pictures, and the menus' pictures, music, sounds and text (web.go).
func gated(path string) bool {
	return strings.HasPrefix(path, "/maps/") || strings.HasPrefix(path, "/game/") ||
		strings.HasPrefix(path, "/pictures/") || strings.HasPrefix(path, "/images/") ||
		path == "/music" || path == "/sounds" || path == "/strings"
}

// allowed reports whether the request may get a gated path. Any request may, unless players sign in with Discord.
// Then the request must carry the cookie of a signed-in player.
func (l *lobby) allowed(req *http.Request) bool {
	if l.discord == nil {
		return true
	}
	c, err := req.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.playerOf(c.Value) != nil
}

// sessionToken returns the token in the request's session cookie, or "" when there is none.
func sessionToken(req *http.Request) string {
	if c, err := req.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// setSession stores the token in the browser's session cookie. The cookie lasts as long as an unused session does
// (sessionIdle). The token "" deletes the cookie.
func setSession(w http.ResponseWriter, req *http.Request, token string) {
	age := int(sessionIdle.Seconds())
	if token == "" {
		age = -1
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: age, HttpOnly: true,
		Secure: secure(req), SameSite: http.SameSiteLaxMode})
}

// secure reports whether the page is on HTTPS. It is on HTTPS everywhere except on localhost, because a browser runs
// the web client only on a secure page, and a reverse proxy adds HTTPS (web.go). The page's cookies are then sent only
// over HTTPS.
func secure(req *http.Request) bool {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return host != "localhost" && !(ip != nil && ip.IsLoopback())
}

// redirectURI returns the address that Discord sends a player back to: the callback, at the page's host.
func redirectURI(req *http.Request) string {
	scheme := "https"
	if !secure(req) {
		scheme = "http"
	}
	return scheme + "://" + req.Host + "/auth/discord/callback"
}

// localPath reports whether a path to return to after signing in is a path on the server's own page, not an address
// on another site.
func localPath(path string) bool {
	u, err := url.Parse(path)
	return err == nil && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") &&
		!strings.HasPrefix(path, "/\\") && u.Scheme == "" && u.Host == "" && !strings.ContainsAny(path, "\r\n")
}

// ---- Discord's API ----

// signIn exchanges the code of a sign-in for the player's tokens, and then asks Discord who the player is (lookUp).
func (d *discordAuth) signIn(ctx context.Context, code, redirect string) (discordUser, error) {
	var u discordUser
	err := d.tokens(ctx, &u, url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}})
	if err == nil {
		err = d.lookUp(ctx, &u)
	}
	return u, err
}

// check looks a player up again (lookUp). It first refreshes the player's tokens when they have expired or expire
// within a minute. When Discord refuses the tokens, it refreshes them and tries once more.
func (d *discordAuth) check(ctx context.Context, u discordUser) (discordUser, error) {
	if time.Until(u.Expires) < time.Minute {
		if err := d.refresh(ctx, &u); err != nil {
			return u, err
		}
	}
	err := d.lookUp(ctx, &u)
	if errors.Is(err, errRefused) {
		if err = d.refresh(ctx, &u); err == nil {
			err = d.lookUp(ctx, &u)
		}
	}
	return u, err
}

func (d *discordAuth) refresh(ctx context.Context, u *discordUser) error {
	return d.tokens(ctx, u, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {u.Refresh}})
}

// tokens asks Discord for a player's tokens (POST /oauth2/token), authenticated as the application.
func (d *discordAuth) tokens(ctx context.Context, u *discordUser, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url+"/api/v10/oauth2/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(d.clientID, d.secret)
	var t struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := d.do(req, &t); err != nil {
		return err
	}
	if t.Access == "" {
		return errors.New("Discord sent no access token")
	}
	u.Access, u.Expires = t.Access, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second)
	if t.Refresh != "" {
		u.Refresh = t.Refresh
	}
	return nil
}

// lookUp asks Discord who a player is, with the player's access token, and stores the answer in u: the account's ID,
// the player's name and their picture. The name is the first of these that the game keeps anything of (clean): their
// nickname in the Discord server, their display name, their username. The picture is their avatar in the Discord
// server, or else their account's avatar. lookUp returns a denied error when the player may not play.
func (d *discordAuth) lookUp(ctx context.Context, u *discordUser) error {
	var me struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
		Avatar     string `json:"avatar"`
	}
	if err := d.get(ctx, u.Access, "/users/@me", &me); err != nil {
		return err
	}
	if me.ID == "" || u.ID != "" && me.ID != u.ID {
		return fmt.Errorf("Discord answered for account %q, not %s", me.ID, u.ID)
	}
	u.ID, u.Name, u.Avatar = me.ID, me.Username, ""
	if me.Avatar != "" {
		u.Avatar = discordCDN + "/avatars/" + me.ID + "/" + url.PathEscape(me.Avatar) + ".png?size=128"
	}
	names := []string{me.GlobalName, me.Username}
	if d.server != "" {
		var m struct {
			Nick   string   `json:"nick"`
			Avatar string   `json:"avatar"`
			Roles  []string `json:"roles"`
		}
		err := d.get(ctx, u.Access, "/users/@me/guilds/"+url.PathEscape(d.server)+"/member", &m)
		switch {
		case errors.Is(err, errNotFound):
			return denied("member")
		case err != nil:
			return err
		case len(d.roles) > 0 && !slices.ContainsFunc(m.Roles, func(r string) bool { return slices.Contains(d.roles, r) }):
			return denied("role")
		}
		names = append([]string{m.Nick}, names...)
		if m.Avatar != "" {
			u.Avatar = discordCDN + "/guilds/" + url.PathEscape(d.server) + "/users/" + me.ID + "/avatars/" +
				url.PathEscape(m.Avatar) + ".png?size=128"
		}
	}
	if i := slices.IndexFunc(names, func(n string) bool { return clean(n) != "" }); i >= 0 {
		u.Name = names[i]
	}
	u.Checked = time.Now()
	return nil
}

// get requests a path of Discord's API as the player that the access token belongs to.
func (d *discordAuth) get(ctx context.Context, access, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url+"/api/v10"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	return d.do(req, v)
}

// do sends a request to Discord and decodes the answer into v. It returns errRefused for a 400, 401 or 403 (a code or
// a token that is invalid or lacks a scope), errNotFound for a 404, and another error for any other status but 200.
func (d *discordAuth) do(req *http.Request, v any) error {
	rsp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(rsp.Body, 1<<20))
	if err != nil {
		return err
	}
	switch rsp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(body, v)
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w (%s to %s: %.200s)", errRefused, rsp.Status, req.URL.Path, body)
	case http.StatusNotFound:
		return errNotFound
	default:
		return fmt.Errorf("Discord answered %s to %s", rsp.Status, req.URL.Path)
	}
}
