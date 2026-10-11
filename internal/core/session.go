package core

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Sessions: one owner for "somebody is logged in"
//
// A login is a `Login` record on the machine it happened on plus a syslog line
// — that is what `who` reads, what a central log collector forwards, and what
// §34's investigators find later. Every transport that opens a session goes
// through here (the telnet and ssh entries, the in-world web terminal, and the
// browser front), so the four of them cannot drift apart in what a login looks
// like.
// ---------------------------------------------------------------------------

// BeginSession records a login on a device and returns the function that ends
// it. The closer is idempotent: a session that ends twice (the shell exited,
// the socket dropped) leaves one record and one pair of log lines.
func (w *World) BeginSession(d *Device, user, tty, from string) func() {
	if d == nil {
		return func() {}
	}
	rec := Login{User: user, TTY: tty, From: from, At: w.Sim}
	d.Active = append(d.Active, rec)
	d.Logf("info", "auth", "session opened for %s on %s from %s", user, tty, from)
	w.AddEvent(d.ID, "info", "auth", "%s: %s logged in on %s", d.Hostname, user, tty)
	done := false
	return func() {
		if done {
			return
		}
		done = true
		for i, l := range d.Active {
			if l.User == rec.User && l.TTY == rec.TTY && l.From == rec.From && l.At.Equal(rec.At) {
				d.Active = append(d.Active[:i], d.Active[i+1:]...)
				break
			}
		}
		d.Logf("info", "auth", "session closed for %s on %s", user, tty)
	}
}

// EndSession removes a login record without having kept its closer — used when
// a session is ended from somewhere else (a world reload, an operator).
func (w *World) EndSession(d *Device, user, tty string) {
	if d == nil {
		return
	}
	var keep []Login
	for _, l := range d.Active {
		if l.User == user && l.TTY == tty {
			continue
		}
		keep = append(keep, l)
	}
	d.Active = keep
}

// SessionsAt lists the logins recorded on a machine, newest last.
func (w *World) SessionsAt(d *Device) []Login {
	if d == nil {
		return nil
	}
	out := append([]Login(nil), d.Active...)
	return out
}

// LandPlayer resolves where a player's transport session should land: their own
// machine and account, or — when the house is dark — the out-of-band controller
// on its own battery. It is the one place that decides this, so telnet, ssh and
// the browser front cannot land the same player in different places.
func (w *World) LandPlayer(who string) (*Device, *User, string, error) {
	p := w.Players[who]
	if p == nil {
		return nil, nil, "", fmt.Errorf("no such player: %s", who)
	}
	pc := w.Devices[p.PC]
	if pc == nil {
		return nil, nil, "", fmt.Errorf("%s has no machine (%s)", who, p.PC)
	}
	note := ""
	// A dead house is not a one-way door: the management controller has its own
	// battery and backhaul, and that is the door the operator opens.
	if !pc.Powered() {
		if bmc := w.OutOfBand(); bmc != nil && bmc.Powered() {
			note = fmt.Sprintf("%s is down (%s) — connecting to %s over out-of-band management",
				pc.Hostname, pc.UnavailableReason(), bmc.Hostname)
			pc = bmc
		}
	}
	u := pc.FindUser(who)
	if u == nil {
		u = pc.FindUser("alex")
	}
	if note != "" {
		if bu := pc.FindUser("admin"); bu != nil {
			u = bu
		}
	}
	if u == nil {
		u = &User{Name: "alex", UID: 1000, Home: "/home/alex", Shell: "/bin/bash"}
	}
	return pc, u, note, nil
}

// SessionUptime is a session record's age in world time.
func (w *World) SessionUptime(l Login) time.Duration { return w.Sim.Sub(l.At) }
