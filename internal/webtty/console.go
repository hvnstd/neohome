package webtty

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"neohome/internal/core"
)

// ---------------------------------------------------------------------------
// The console: the HTTP entry a browser talks to.
//
//	GET  /          the terminal page (self-contained, no external asset)
//	POST /login     account + password -> a single-use ticket, 60s
//	GET  /ws?t=…    upgrade, then the world's shell over WebSocket
//	GET  /healthz   liveness for a supervisor
//
// The login is the same check the ssh entry makes (the player record and its
// password), and a failure is recorded on the player's own machine through the
// world's own counter — so a browser brute force feeds fail2ban and the IDS
// exactly like repeated ssh attempts do. Tickets are short-lived and single
// use, and a bearer ticket only ever opens a session for the account it was
// minted for.
// ---------------------------------------------------------------------------

const (
	// ticketTTL bounds the gap between logging in and the socket opening.
	ticketTTL = 60 * time.Second
	// loginWindow/loginMax limit the transport itself: five wrong passwords
	// from one address in a minute and that address waits.
	loginWindow = time.Minute
	loginMax    = 5
	// loginCooldown is how long an address that tripped the limit waits.
	loginCooldown = time.Minute
)

// Console is the browser front's HTTP handler.
type Console struct {
	W *core.World
	// Origin, when set, is the only cross-origin value accepted on /login and
	// /ws. Empty means same-origin only, which is what a browser sends anyway.
	Origin string
	// Idle is passed to each session.
	Idle time.Duration
	// Now is the clock, for tests.
	Now func() time.Time
	// Log is where the console writes its own diagnostics (never player text).
	Log *log.Logger

	mu      sync.Mutex
	tickets map[string]*ticket
	attempt map[string][]time.Time
	blocked map[string]time.Time
}

type ticket struct {
	user   string
	from   string
	at     time.Time
	origin string
}

// NewConsole returns a console bound to a world.
func NewConsole(w *core.World) *Console {
	return &Console{
		W:       w,
		tickets: map[string]*ticket{},
		attempt: map[string][]time.Time{},
		blocked: map[string]time.Time{},
	}
}

func (c *Console) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// ServeHTTP routes the front end.
func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/", "/index.html":
		c.servePage(w, r)
	case "/login":
		c.serveLogin(w, r)
	case "/ws":
		c.serveWS(w, r)
	case "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	case "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (c *Console) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// No X-Frame-Options and no frame-ancestors: the front is meant to be
	// embedded (a live preview, a kiosk tab). It is a login page, not a bank.
	fmt.Fprint(w, Page("NeoHome"))
}

// sameOrigin enforces the browser's own rule: a request that carries an Origin
// must carry this server's. Without it, another page could mint tickets
// against a console the user is already logged into.
func (c *Console) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser, or a same-origin fetch that omits it
	}
	if c.Origin != "" && strings.EqualFold(origin, c.Origin) {
		return true
	}
	u, err := parseOrigin(origin)
	if err != nil {
		return false
	}
	oh := hostOnly(u.Host)
	// The page is often reached through something in front of the server — a
	// reverse proxy, a preview tunnel — which may rewrite Host. The origin the
	// browser names must match the host the request claims to be for, however
	// that host arrived. A page on another site still names its own origin and
	// still fails, which is the point of the check.
	for _, cand := range []string{
		r.Host,
		r.Header.Get("X-Forwarded-Host"),
		r.Header.Get("X-Forwarded-Server"),
		forwardedHost(r.Header.Get("Forwarded")),
	} {
		if cand == "" {
			continue
		}
		// X-Forwarded-Host may be a list: the first entry is the original
		for _, one := range strings.Split(cand, ",") {
			if strings.EqualFold(hostOnly(strings.TrimSpace(one)), oh) {
				return true
			}
		}
	}
	return false
}

// hostOnly strips a port, so an origin and a Host header can be compared on the
// name a person would recognise.
func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// forwardedHost pulls the host out of an RFC 7239 Forwarded header.
func forwardedHost(v string) string {
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if len(part) > 5 && strings.EqualFold(part[:5], "host=") {
			return strings.Trim(strings.TrimSpace(part[5:]), "\"")
		}
	}
	return ""
}

type originParts struct{ Scheme, Host string }

func (o originParts) String() string { return o.Scheme + "://" + o.Host }

func parseOrigin(s string) (originParts, error) {
	i := strings.Index(s, "://")
	if i <= 0 {
		return originParts{}, errors.New("not an origin")
	}
	rest := s[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return originParts{}, errors.New("not an origin")
	}
	return originParts{Scheme: s[:i], Host: rest}, nil
}

type loginRequest struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type loginResponse struct {
	Ticket string `json:"ticket"`
	User   string `json:"user"`
	Device string `json:"device"`
}

func (c *Console) serveLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !c.sameOrigin(r) {
		c.writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin login refused"})
		return
	}
	host := remoteHost(r)
	if until, blocked := c.throttle(host); blocked {
		w.Header().Set("Retry-After", fmt.Sprint(int(time.Until(until).Seconds())+1))
		c.writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("too many attempts from %s; wait a minute", host)})
		return
	}
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := dec.Decode(&req); err != nil {
		c.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed login request"})
		return
	}
	req.User = strings.TrimSpace(req.User)

	c.W.Lock()
	p := c.W.Players[req.User]
	ok := p != nil && !p.MCPOnly && p.Pass != "" && p.Pass == req.Pass
	var devName, devID string
	if ok {
		if d := c.W.Devices[p.PC]; d != nil {
			devName, devID = d.Hostname, d.ID
		}
	}
	c.W.Unlock()

	if !ok {
		c.noteFailure(host)
		// the failure lands on the account's own machine, where the world's
		// own tools can see it (fail2ban, the IDS, a later investigation)
		c.recordAuthFail(req.User, host)
		c.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid account or password"})
		return
	}
	if devID == "" {
		c.writeJSON(w, http.StatusConflict, map[string]string{"error": "this account has no machine in the world"})
		return
	}
	tk := c.mint(req.User, host, r.Header.Get("Origin"))
	c.writeJSON(w, http.StatusOK, loginResponse{Ticket: tk, User: req.User, Device: devName})
}

// mint creates a single-use ticket.
func (c *Console) mint(user, from, origin string) string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is not a state to paper over with a weak token
		panic("webtty: no entropy for a ticket: " + err.Error())
	}
	tk := hex.EncodeToString(buf)
	c.mu.Lock()
	defer c.mu.Unlock()
	// opportunistic sweep so a long-lived console does not accumulate tickets
	now := c.now()
	for k, v := range c.tickets {
		if now.Sub(v.at) > ticketTTL {
			delete(c.tickets, k)
		}
	}
	c.tickets[tk] = &ticket{user: user, from: from, at: now, origin: origin}
	return tk
}

// consume redeems a ticket: one use, sixty seconds, and only for the address
// and origin it was minted for. Presenting a ticket spends it — including a
// presentation from the wrong address, so a ticket that leaked out of a shared
// browser's history is not just useless to the thief, it is useless, and the
// account holder logging in again is the only way back in.
func (c *Console) consume(tk, from, origin string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.tickets[tk]
	if !ok {
		return "", false
	}
	delete(c.tickets, tk)
	if c.now().Sub(t.at) > ticketTTL {
		return "", false
	}
	if t.from != from {
		return "", false
	}
	if t.origin != "" && origin != "" && !strings.EqualFold(t.origin, origin) {
		return "", false
	}
	return t.user, true
}

func (c *Console) serveWS(w http.ResponseWriter, r *http.Request) {
	if !IsUpgradeRequest(r) {
		// a plain GET on /ws is a browser or a probe poking the wrong door
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "neohome web terminal: connect with a websocket, after POST /login\n")
		return
	}
	// the protocol's own answers come first: a client that is not speaking
	// websocket-13 must be told that before it is asked for a ticket
	if err := CheckUpgrade(w, r); err != nil {
		http.Error(w, err.Error(), HTTPStatus(err))
		return
	}
	if !c.sameOrigin(r) {
		http.Error(w, "cross-origin websocket refused", http.StatusForbidden)
		return
	}
	host := remoteHost(r)
	user, ok := c.consume(r.URL.Query().Get("t"), host, r.Header.Get("Origin"))
	if !ok {
		http.Error(w, "invalid or expired ticket", http.StatusUnauthorized)
		return
	}

	var dev *core.Device
	var u *core.User
	var note string
	c.W.Lock()
	d, acct, landNote, err := c.W.LandPlayer(user)
	note = landNote
	dev, u = d, acct
	c.W.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	conn, err := Upgrade(w, r)
	if err != nil {
		// before the upgrade: answer with the status a browser understands
		if HTTPStatus(err) >= 500 {
			log.Printf("webtty: upgrade from %s: %v", host, err)
		}
		http.Error(w, err.Error(), HTTPStatus(err))
		return
	}
	defer conn.CloseSocket()

	if note != "" {
		_ = conn.WriteJSON(wireMsg{T: "out", D: note + "\r\n"})
	}
	if c.Log != nil {
		c.Log.Printf("webterm: session for %s on %s from %s", user, dev.Hostname, host)
	}
	if err := RunSession(c.W, dev, u, conn, SessionOptions{
		From: host,
		Idle: c.Idle,
	}); err != nil {
		log.Printf("webtty: session for %s on %s ended: %v", user, dev.ID, err)
	}
}

func (c *Console) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// recordAuthFail is the world's own evidence: the attempt lands on the
// account's machine, in the same counter an ssh attempt does.
func (c *Console) recordAuthFail(user, host string) {
	c.W.Lock()
	defer c.W.Unlock()
	p := c.W.Players[user]
	if p == nil {
		return
	}
	d := c.W.Devices[p.PC]
	if d == nil {
		d = c.W.OutOfBand()
	}
	if d == nil {
		return
	}
	d.NoteAuthFail(host, host, "web password for "+user)
	c.W.Record("auth", user, host, d.ID, "failed web login", 2)
}

// throttle is the transport's own limit, separate from the world's fail2ban:
// a wrong password costs a real login attempt, so there is no reason to let an
// offline attacker spend them at wire speed.
func (c *Console) throttle(host string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for h, until := range c.blocked {
		if now.After(until) {
			delete(c.blocked, h)
		}
	}
	if until, ok := c.blocked[host]; ok {
		return until, true
	}
	return time.Time{}, false
}

func (c *Console) noteFailure(host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	cut := now.Add(-loginWindow)
	keep := c.attempt[host][:0]
	for _, t := range c.attempt[host] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	c.attempt[host] = keep
	if len(keep) >= loginMax {
		c.blocked[host] = now.Add(loginCooldown)
		c.attempt[host] = nil
	}
}

func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
