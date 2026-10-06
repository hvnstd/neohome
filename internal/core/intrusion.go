package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// §36 现实网络中的因果关系 — the intrusion half
//
// A world where "公网开放 + 弱认证 + 无监控 → 入侵风险上升" is only real if an
// intrusion has consequences. Before this file, a credential guess that worked
// left one log line and nothing else: no process, no file, no persistence, and
// nothing for §33's tools or §34's desks to find. Here is what actually follows
// a working guess against a machine the internet can reach:
//
//   - a process on the box (the implant),
//   - a payload on disk the process runs from,
//   - a persistence line in /etc/cron.d, which is the real mechanism that
//     brings the implant back — delete the process and it returns in a tick,
//     delete the line and the implant dies,
//   - a beacon the machine dials out to every few ticks: a real flow to the
//     world's attacker, visible in the flow table like any other traffic.
//
// Whether anyone notices is not a story: it is the target's own §33 sensors.
// auditd sees the execution, aide sees the new file where it watches, the IDS
// sees the beacon. A machine with none of them is owned in silence, and the
// player finds it by looking: `ps`, `ls /tmp`, `/etc/cron.d`, `secstat flows`.
// ---------------------------------------------------------------------------

// Foothold is one intrusive presence on one machine, with the artifacts that
// can be seen and removed individually.
type Foothold struct {
	Host    string    `json:"host"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`   // the device that owns the implant
	Account string    `json:"account"` // the credential that worked
	Proto   string    `json:"proto"`
	Port    int       `json:"port"`
	PID     int       `json:"pid"`
	Proc    string    `json:"proc"`   // process name as `ps` shows it
	File    string    `json:"file"`   // the payload on disk
	Cron    string    `json:"cron"`   // the persistence line
	Beacon  string    `json:"beacon"` // where it dials out
	// Seen is what noticed, from the target's own sensors. Empty means nobody
	// did: the machine has no auditd, no host monitor, no file baseline and no
	// IDS, and that is a fact about the machine, not about the attacker.
	Seen []string `json:"seen"`
	// Beacons counts the callbacks that really left the machine, and Last is
	// the tick of the most recent one.
	Beacons int `json:"beacons"`
	Last    int `json:"last_tick"`
	// Dead is set when the artifacts are gone; the record stays, because a
	// machine that was owned once is worth remembering.
	Dead bool `json:"dead"`
}

// beaconEvery is how often an implant phones home, in ticks.
const beaconEvery = 7

// beaconPort is the port the implant dials on the attacker. It is not a service
// the attacker offers: the beacon is one-way, like the real thing, and the
// destination only appears in the flow record and in the attacker's log.
const beaconPort = 8443

// FootholdsFor returns the intrusive presences recorded on a device, newest
// last, whether or not they are still alive.
func (w *World) FootholdsFor(host string) []*Foothold {
	var out []*Foothold
	for _, f := range w.Footholds {
		if host == "" || f.Host == host {
			out = append(out, f)
		}
	}
	return out
}

// LiveFootholds returns the ones that still have artifacts on the machine.
func (w *World) LiveFootholds() []*Foothold {
	var out []*Foothold
	for _, f := range w.Footholds {
		if !f.Dead {
			out = append(out, f)
		}
	}
	return out
}

// plantFoothold is what follows a credential that worked on a machine the
// internet can reach. It is called by the world's own attacker, never by a
// player's session: a login is a login, an intrusion is an actor with intent.
func (w *World) plantFoothold(sc, dst *Device, u *User, proto string, port int) *Foothold {
	if w.FootholdsFor(dst.ID) != nil {
		// already resident: a second guess on the same box is not a second
		// intrusion, and real implants do not install themselves twice
		for _, f := range w.FootholdsFor(dst.ID) {
			if !f.Dead {
				return f
			}
		}
	}
	name := ".kworkq"
	if u != nil && u.UID == 0 {
		name = ".cache-helper"
	}
	bin := "/tmp/" + name
	dir := "/tmp"
	dst.FS.MkdirAll(dir, 01777, "root", "root")
	payload := fmt.Sprintf("#!%s\n# %s\nwhile true; do sleep 1; done\n", "/bin/sh", "beacon")
	dst.FS.Write(bin, payload, 0755, u.Name, u.Name)
	proc := name
	if u != nil && u.UID == 0 {
		proc = "[kworker/0:1]"
	}
	p := &Proc{Name: proc, Args: bin, User: u.Name, TTY: "?", State: "S", Start: w.Sim,
		StartTick: w.TickCount, Kind: "shell"}
	dst.AddProc(p)
	cron := "/etc/cron.d/0" + name
	dst.FS.MkdirAll("/etc/cron.d", 0755, "root", "root")
	dst.FS.Write(cron, fmt.Sprintf("*/7 * * * * root %s >/dev/null 2>&1\n", bin), 0644, "root", "root")
	f := &Foothold{
		Host: dst.ID, At: w.Sim, Actor: sc.ID, Account: u.Name, Proto: proto, Port: port,
		PID: p.PID, Proc: proc, File: bin, Cron: cron,
		Beacon: fmt.Sprintf("%s:%d", sc.WANIP(), beaconPort),
	}
	// what the machine's own sensors see at the moment of the intrusion
	f.Seen = w.sensorsSawIntrusion(dst, f)
	w.Footholds = append(w.Footholds, f)
	dst.Logf("warn", "sshd", "session opened for %s by (uid=0): %s executed", u.Name, bin)
	w.AddEvent(dst.ID, "warn", "intrusion", "%s: an implant is resident (%s as %s)", dst.Hostname, bin, u.Name)
	return f
}

// sensorsSawIntrusion asks the target's installed §33 tools what they can see.
// Each answer is the tool's own mechanism, not a checklist: auditd watches
// execution, aide compares a file against its baseline, the host monitor looks
// for new executables in /tmp, and the IDS reads the flow table (the beacon
// reaches it on the next callback).
func (w *World) sensorsSawIntrusion(dst *Device, f *Foothold) []string {
	var seen []string
	if svc := dst.Svc("auditd"); svc != nil && svc.State == "running" {
		dst.Alertf("auditd", "exec", "high", "execve(%s) by uid=%d", f.File, 0)
		seen = append(seen, "auditd")
	}
	if db := dst.Sec().AideDB; len(db) > 0 {
		for path := range db {
			if strings.HasPrefix(f.Cron, strings.TrimSuffix(path, "/")) && path != "" {
				dst.Alertf("aide", "integrity", "high", "%s: new file", f.Cron)
				seen = append(seen, "aide")
				break
			}
		}
	}
	if svc := dst.Svc("rkhunter"); svc != nil && svc.State == "running" {
		dst.Alertf("rkhunter", "hidden", "high", "suspicious executable in /tmp: %s", f.File)
		seen = append(seen, "rkhunter")
	}
	if svc := dst.Svc("suricata"); svc != nil && svc.State == "running" {
		seen = append(seen, "suricata")
	}
	sort.Strings(seen)
	return seen
}

// IntrusionTick runs the implants: the persistence line decides whether the
// process is there, and every beaconEvery ticks the machine dials home for
// real, through the same Dial every other packet uses.
func (w *World) IntrusionTick() {
	for _, f := range w.Footholds {
		if f.Dead {
			continue
		}
		dst := w.Devices[f.Host]
		if dst == nil || !dst.Powered() {
			continue
		}
		// persistence is the cron line: remove it and the implant does not come
		// back; kill the process and it does, which is the difference between
		// cleaning up and believing you did
		if _, ok := dst.FS.Read(f.Cron); !ok {
			w.killImplant(dst, f, "the persistence line is gone")
			continue
		}
		if !w.procAlive(dst, f.PID) {
			p := &Proc{Name: f.Proc, Args: f.File, User: f.Account, TTY: "?", State: "S",
				Start: w.Sim, StartTick: w.TickCount, Kind: "shell"}
			dst.AddProc(p)
			f.PID = p.PID
			dst.Logf("warn", "cron", "(%s) CMD (%s) — restarting", f.Account, f.File)
		}
		if w.TickCount-f.Last < beaconEvery {
			continue
		}
		f.Last = w.TickCount
		sc := w.Devices[f.Actor]
		if sc == nil {
			continue
		}
		// the callback is a packet path, not a message: the machine really
		// dials the attacker, and the flow lands in the attacker's own record
		// — a host cannot see its own egress any more than a real one can, so
		// the evidence of the beacon is on the far side
		if svc, _, _ := Dial(dst, sc.WANIP(), beaconPort); svc != nil || w.beaconSeen(sc, dst) {
			f.Beacons++
			dst.Logf("notice", "net", "callback to %s:%d (%d since intrusion)", sc.WANIP(), beaconPort, f.Beacons)
			sc.Logf("info", "sink", "beacon from %s (%d)", dst.Hostname, f.Beacons)
		}
	}
}

// beaconSeen reads the attacker's own flow record for a callback from a host.
func (w *World) beaconSeen(sc, dst *Device) bool {
	for _, fl := range sc.Sec().Flows {
		if fl.Port == beaconPort && (fl.SrcHost == dst.Hostname || fl.Src == dst.WANIP() || fl.Src == dst.FirstLANIP()) {
			return true
		}
	}
	return false
}

func (w *World) procAlive(dst *Device, pid int) bool {
	for _, p := range dst.Procs {
		if p.PID == pid {
			return true
		}
	}
	return false
}

// killImplant ends a presence and says why, keeping the record.
func (w *World) killImplant(dst *Device, f *Foothold, why string) {
	for i, p := range dst.Procs {
		if p.PID == f.PID {
			dst.Procs = append(dst.Procs[:i], dst.Procs[i+1:]...)
			break
		}
	}
	f.Dead = true
	dst.Logf("info", "intrusion", "%s is gone: %s", f.Proc, why)
	w.AddEvent(dst.ID, "info", "intrusion", "%s: implant removed (%s)", dst.Hostname, why)
}

// CleanFoothold is the operator's own hand: remove the implant's artifacts in
// one motion. It is not magic — it deletes the same three things a player would
// delete by hand, and it refuses when they are not all present.
func (w *World) CleanFoothold(host string) (int, error) {
	dst := w.Devices[host]
	if dst == nil {
		return 0, fmt.Errorf("no such host: %s", host)
	}
	n := 0
	for _, f := range w.FootholdsFor(host) {
		if f.Dead {
			continue
		}
		dst.FS.Remove(f.File)
		dst.FS.Remove(f.Cron)
		w.killImplant(dst, f, "removed by the operator: process, payload and persistence line")
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("%s has no live implant on record", host)
	}
	return n, nil
}

// IntrusionReport is what `secstat compromise` prints: the state of every
// presence on a machine, and what — if anything — noticed it.
func (w *World) IntrusionReport(host string) []string {
	var out []string
	fs := w.FootholdsFor(host)
	if len(fs) == 0 {
		return []string{"no intrusive presence on record here"}
	}
	for _, f := range fs {
		dst := w.Devices[f.Host]
		state := "resident"
		if f.Dead {
			state = "removed"
		}
		out = append(out, fmt.Sprintf("%s  %s  since %s (%s as %s on %s/%d)",
			f.Host, state, f.At.Format("2006-01-02 15:04"), f.Actor, f.Account, f.Proto, f.Port))
		out = append(out, fmt.Sprintf("    process:     %s (pid %d)", f.Proc, f.PID))
		out = append(out, fmt.Sprintf("    payload:     %s", f.File))
		out = append(out, fmt.Sprintf("    persistence: %s", f.Cron))
		out = append(out, fmt.Sprintf("    beacon:      %s every %d ticks (%d sent)", f.Beacon, beaconEvery, f.Beacons))
		if len(f.Seen) == 0 {
			out = append(out, "    noticed by:  nothing — this machine runs no auditd, no host monitor and no IDS")
		} else {
			out = append(out, "    noticed by:  "+strings.Join(f.Seen, ", "))
		}
		if dst != nil {
			if _, ok := dst.FS.Read(f.File); ok {
				out = append(out, "    on disk:     yes")
			} else {
				out = append(out, "    on disk:     gone")
			}
			if w.procAlive(dst, f.PID) {
				out = append(out, fmt.Sprintf("    running:     yes (pid %d in `ps`)", f.PID))
			} else {
				out = append(out, "    running:     no")
			}
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
