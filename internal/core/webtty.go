package core

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Web terminal (Phase 3) — the xterm.js side of the world
//
// The server side of a web client is a real service: `webd` on port 8080 of
// the household assistant, speaking a one-shot HTTP protocol that hands out
// session tokens and accepts commands. `/` explains the client and prints
// the same credentials an ssh login takes, `/session` mints a short-lived
// token bound to one account and one address (same record as an ssh login),
// and `/query?t=…` runs one line of shell as that account on that machine.
//
// It is deliberately NOT a shell protocol: no persistent reader, no job
// control, no pipes. A browser is one-shot HTTP; pretending otherwise would
// break the world's rules about how a terminal actually works. Sessions
// carry the same evidence (Active logins, syslog, auth failures) as every
// other transport, because it is the same world.
// ---------------------------------------------------------------------------

const (
	// webPort is the web terminal's port.
	webPort = 8080
	// webTokenTTL bounds a session token: minutes of world time, because a
	// browser that never logs out is a browser anyone can walk past.
	webTokenTTL = 15 * time.Minute
)

// WebExecFn is how one browser line becomes one real command run: the shell
// package satisfies this at init (the same inversion cron uses), so core
// never imports shell while a browser still runs the world's builtins.
var WebExecFn func(w *World, d *Device, u *User, line string) (string, int)

// SetWebExec registers the web terminal's command runner.
func SetWebExec(fn func(w *World, d *Device, u *User, line string) (string, int)) {
	WebExecFn = fn
}

// WebSession is one browser session: the account it runs as, the machine it
// drives, when it was minted and whether it has logged out. Plain data, so
// it survives a save like every other login record.
type WebSession struct {
	Token  string
	User   string
	DevID  string
	At     time.Time
	Closed bool
}

// webSessions returns the world's live session table, creating it on first
// use so an older save never panics.
func (w *World) webSessions() map[string]*WebSession {
	if w.WebSessions == nil {
		w.WebSessions = map[string]*WebSession{}
	}
	return w.WebSessions
}

// WebSessionFor resolves a token to a live session: expired or closed
// tokens are gone, not merely stale.
func (w *World) WebSessionFor(token string) (*WebSession, bool) {
	for id, s := range w.webSessions() {
		if s.Token != token {
			continue
		}
		if s.Closed || w.Sim.Sub(s.At) > webTokenTTL {
			delete(w.WebSessions, id)
			return nil, false
		}
		return s, true
	}
	return nil, false
}

// WebOpen mints a session for user on the given webd device, authenticating
// with the account's real password. Refusals are as honest as ssh's: the
// failure is recorded by the target, and nothing names whether the account
// or the password was wrong.
func (w *World) WebOpen(host *Device, user, pass string) (*WebSession, error) {
	u := host.FindUser(user)
	if u == nil || !u.CheckPassword(pass) {
		host.NoteAuthFail(host.SourceIPFor(host), host.Hostname, "web password for "+user)
		return nil, fmt.Errorf("invalid credentials")
	}
	token := "wt" + itoa(int(webSessionSeed(host, user)))
	id := fmt.Sprintf("web-%s-%s", user, token)
	if _, dup := w.WebSessions[id]; dup {
		token = token + "-r"
	}
	s := &WebSession{Token: token, User: user, DevID: host.ID, At: w.Sim}
	w.webSessions()[id] = s
	host.Active = append(host.Active, Login{User: user, TTY: "web", From: "web", At: w.Sim})
	host.Logf("info", "webd", "web session opened for %s", user)
	return s, nil
}

// WebClose ends a session and removes its active-login row.
func (w *World) WebClose(token string) {
	for id, s := range w.webSessions() {
		if s.Token != token {
			continue
		}
		if _, live := w.WebSessionFor(token); live {
			s.Closed = true
			if host := w.Devices[s.DevID]; host != nil {
				var keep []Login
				for _, l := range host.Active {
					if l.User == s.User && l.TTY == "web" {
						continue
					}
					keep = append(keep, l)
				}
				host.Active = keep
				host.Logf("info", "webd", "web session for %s closed", s.User)
			}
		}
		delete(w.WebSessions, id)
	}
}

// webSessionSeed derives a per-world-per-user token seed so sessions are
// recognisable and unique without a global counter.
func webSessionSeed(d *Device, user string) int64 {
	var sum int64
	for i, c := range d.ID + "|" + user {
		sum += int64(c) * int64(i+31)
	}
	return sum % 1000000
}

// WebLanding is the web terminal's document: what the client is, how to open
// a session, and how to run one line. It is the HTTP endpoint this world
// serves on webd like every other — same accounts, same gates.
func (w *World) WebLanding() string {
	return "NeoHome web terminal (xterm.js)\n" +
		"POST /session  user=ACCOUNT&pass=PASSWORD   -> session token\n" +
		"GET  /query?t=TOKEN&c=COMMAND               -> output\n" +
		"GET  /logout?t=TOKEN                        -> closed\n" +
		"Every session is a real login: it lands in who, syslog and history.\n"
}

// WebPort is the web terminal's port: webd listens here like any service.
const WebPort = 8080

// WebQuery runs one command line as the session's user on its device: the
// same builtins, the same permission checks, the same history file. With no
// runtime linked (a bare build) it says so instead of pretending.
func (w *World) WebQuery(s *WebSession, line string) string {
	host := w.Devices[s.DevID]
	if host == nil {
		return "session lost: the machine it drove is gone\n"
	}
	u := host.FindUser(s.User)
	if u == nil {
		return "session lost: the account is gone\n"
	}
	if !host.Powered() {
		return "machine is dark — nothing runs\n"
	}
	if WebExecFn == nil {
		return "webd: no command runtime linked into this build\n"
	}
	out, rc := WebExecFn(w, host, u, line)
	var b strings.Builder
	b.WriteString(out)
	if rc != 0 {
		fmt.Fprintf(&b, "(exit %d)\n", rc)
	}
	return b.String()
}
