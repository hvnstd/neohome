package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// §33's other half: a defence tool is only real if there is something to defend
// against. This file is the world's attacker — one host on the public internet
// that actually does what internet hosts do: it sweeps ports and it tries
// passwords. Every attempt goes through the same Dial every other packet uses,
// so the flows, the logs, the failures and the bans are all real consequences
// of real traffic, not scripted events with a label.
//
// The scanner is not a random event generator:
//
//	it has an address, and a device you can traceroute, ping and report;
//	it acts on a schedule you can predict (every ScannerEvery ticks);
//	it only reaches what is actually reachable — a port forward, a public
//	VPS, a running service — and it gives up on what the firewall drops;
//	and it keeps coming back until a ban makes it stop.

// ScannerEvery is how often the scanner acts, in ticks.
const ScannerEvery = 97

// ScannerID is the device id of the scanner, so a player who traces it can
// name it and §34's abuse desk has something to act on.
const ScannerID = "scan-host"

// scanUserPass is a small credential list — the same handful of pairs every
// internet-facing box sees, in the same order.
var scanUserPass = []struct{ user, pass string }{
	{"root", "root"}, {"root", "123456"}, {"admin", "admin"},
	{"root", "password"}, {"ubuntu", "ubuntu"}, {"pi", "raspberry"},
}

// scanPorts is a real sweep: the twenty-odd ports a botnet tries first.
var scanPorts = []int{21, 22, 23, 25, 53, 80, 110, 143, 443, 445, 554, 631,
	993, 995, 2049, 3306, 3389, 5432, 5900, 6379, 8080, 8443, 8899, 2323}

// addScanner creates the world's scanner host: a rented box somewhere with one
// public address and nothing else on it. It is a device like any other, which
// is the point — §34 and §35 need a real actor, not a counter.
func (w *World) addScanner() *Device {
	d := w.addDevice(ScannerID, "scan-host", "vps", "", OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"Bulk VPS", 2, 2400, 2048, 40960, 1000, false, false}, "")
	// a real address on the routable internet, with a default route — without
	// them the "attacker" could not send a packet, which is exactly the kind
	// of decoration §33 forbids
	ip := w.allocPublicFor("vps")
	d.Ifaces = append(d.Ifaces, &Iface{Name: "eth0", IP: ip, MAC: macFor(d.ID), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[ip] = d.ID
	d.NATed = false
	d.Notes = "A rented host on the public internet. Its address is in every scanner's log and every abused owner's log; it answers ping, has no open service of its own."
	mkUsers(d, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
	})
	seedScannerFS(d)
	return d
}

// ScannerTick is the bot's own loop. Deterministic: which target it looks at
// depends on the tick number, so a player who reads the logs can predict the
// next one — the difference between a system and a slot machine.
func (w *World) ScannerTick() {
	if w.TickCount < 20 || w.TickCount%ScannerEvery != 0 {
		return
	}
	sc := w.Devices[ScannerID]
	if sc == nil || !sc.Powered() {
		return
	}
	for _, target := range w.ScannableTargets() {
		dst := w.Devices[target]
		if dst == nil || !dst.Powered() {
			continue
		}
		w.ScanTarget(sc, dst)
	}
}

// ScanTarget sweeps the ports and, where an ssh endpoint really answers,
// tries the credential list against it. The sweep is deliberately complete
// before the guessing starts: that is the order the flows arrive in, and an
// IDS's portscan rule reads exactly that shape.
//
// The login attempts go to the machine the port *landed* on, not to the
// address that was dialled: a forwarded port belongs to the box behind the
// router, and that is whose jail has to see the failures.
func (w *World) ScanTarget(sc *Device, dst *Device) {
	ip := dst.WANIP()
	if ip == "" {
		ip = dst.FirstLANIP()
	}
	if ip == "" {
		return
	}
	before := len(dst.Sec().Flows)
	sshLands := (*Device)(nil)
	for _, port := range scanPorts {
		svc, landed, msg := Dial(sc, ip, port)
		if svc == nil || msg != "connected" {
			continue
		}
		if port == 22 {
			sshLands = landed
		}
	}
	if len(dst.Sec().Flows) == before {
		return // nothing reached the machine at all: a black hole is not a target
	}
	if sshLands == nil {
		return
	}
	for _, pair := range scanUserPass {
		if sshLands.IsBannedBy(sc) {
			return // banned: the attempts stop, which is what a ban is for
		}
		AttackLogin(w, sc, sshLands, pair.user, pair.pass, "ssh", 22)
	}
}

// ScannableTargets is what the world's scanner can actually see: hosts with a
// public address on the routable internet. A household LAN behind NAT is not
// on that list — it becomes reachable only through a forward the owner
// configured, which is exactly the exposure §14 is about.
func (w *World) ScannableTargets() []string {
	var out []string
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || d.ID == ScannerID || d.Profile == "core" {
			continue
		}
		if d.WANIP() == "" {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// AttackLogin is one credential attempt against a machine, recorded the way
// sshd/ftp/telnet record them: the failure is the target's own evidence. It is
// the core-level twin of what the shell's clients do, used by the world's own
// actors so an NPC attack has the same consequences as a player's.
func AttackLogin(w *World, src, dst *Device, user, pass, proto string, port int) bool {
	svc := dst.Svc("sshd")
	if proto == "ftp" {
		svc = dst.Svc("vsftpd")
	}
	if svc == nil || svc.State != "running" {
		return false
	}
	srcIP := src.sourceIPFor(dst)
	actor := src.Owner
	if actor == "" {
		actor = src.Hostname
	}
	if u := dst.FindUser(user); u.CheckPassword(pass) {
		dst.Logf("notice", svc.Name, "accepted password for %s from %s (%s)", user, src.Hostname, srcIP)
		w.Record("auth", src.Owner, srcIP, dst.ID, fmt.Sprintf("%s login %s@%s", proto, user, dst.Hostname), 3)
		// §36: a working credential on a machine the internet reached is not a
		// log line, it is an intrusion. Only the world's own attacker acts on
		// it — a player's session is a session, and the credential-attempt
		// primitive stays a primitive for the tests.
		if src.ID == ScannerID {
			w.plantFoothold(src, dst, u, proto, port)
		}
		return true
	}
	dst.Logf("notice", svc.Name, "failed password for %s from %s (%s)", user, src.Hostname, srcIP)
	dst.NoteAuthFail(srcIP, src.Hostname, proto+" password for "+user)
	w.Record("auth", actor, srcIP, dst.ID, fmt.Sprintf("failed %s login as %s", proto, user), 3)
	return false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// WANIP is this device's public address, if it has one.
func (d *Device) WANIP() string {
	if d.NATed {
		return ""
	}
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		if ClassifyAddr(i.IP).Public {
			return i.IP
		}
	}
	return ""
}

// SvcAddr is the address a player would give someone to reach a service: the
// public one where there is one, the LAN one otherwise. It is used by the
// reports, not by the packet path (which always deals in addresses, not names).
func (d *Device) SvcAddr() string {
	if ip := d.WANIP(); ip != "" {
		return ip
	}
	return d.FirstLANIP()
}

// ScannerTrail is what the scanner has done lately, from the targets' own
// flows: the evidence §34 hands to an abuse desk.
func (w *World) ScannerTrail(limit int) []string {
	sc := w.Devices[ScannerID]
	if sc == nil {
		return nil
	}
	var out []string
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || d == sc {
			continue
		}
		for _, f := range d.RecentFlows(6 * time.Hour) {
			if f.SrcHost != sc.Hostname && !strings.Contains(f.SrcHost, "scan-host") {
				continue
			}
			out = append(out, fmt.Sprintf("%s  %s:%d  %s", f.At.Format("15:04:05"), d.Hostname, f.Port, f.Verdict))
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// seedScannerFS gives the scanner the files a real host would have, including
// the tool and the credential list it is using — the evidence a report needs.
func seedScannerFS(d *Device) {
	d.FS.MkdirAll("/root", 0700, "root", "root")
	d.FS.Write("/etc/hostname", "scan-host\n", 0644, "root", "root")
	d.FS.Write("/root/scan.sh", `#!/bin/sh
# the sweep this host runs: every port a botnet tries, then the password list
for p in 21 22 23 25 53 80 110 143 443 445 554 631 993 995 2049 3306 3389 5432 5900 6379 8080 8443 8899 2323; do
  nc -z -w1 "$1" "$p" 2>/dev/null && echo "$1:$p open"
done
`, 0755, "root", "root")
	d.FS.Write("/root/pass.txt", "root:root\nroot:123456\nadmin:admin\nroot:password\nubuntu:ubuntu\npi:raspberry\n", 0600, "root", "root")
	d.FS.Write("/var/log/syslog", "", 0640, "root", "root")
}
