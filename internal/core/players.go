package core

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Multiplayer citizens (Phase 2) — roommates, not guests
//
// The transport was always multi-user (concurrent ssh/telnet sessions on one
// shared world, one lock); what was missing was a second human with their
// own machine, money and name on the evidence. An MCP player borrows the
// household LAN the same way: this file provisions a password-login citizen
// with a dedicated PC, a bank account and an entry landing (see
// runPlayerSession), isolated from everyone else by the same VFS
// permissions as every other account.
//
// Citizenship is vouched and paid: an existing player invites and pays the
// setup fee, the newcomer lands broke with a machine and picks their own
// password with `passwd` on first login. Money is the rate limit — a broke
// inviter cannot mint neighbours.
// ---------------------------------------------------------------------------

// playerSetupFee is what vouching costs, in cents: a real machine with an
// address out of the finite LAN pool, not a row in a table.
const playerSetupFee = 2000

var playerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,15}$`)

// InvitePlayer vouches a new human citizen into the household LAN.
func (w *World) InvitePlayer(inviter, name, pass string) (*Player, error) {
	if !playerNameRe.MatchString(name) {
		return nil, fmt.Errorf("bad player name %q (lowercase letters, digits, hyphens, 2-16 chars)", name)
	}
	for _, reserved := range []string{"root", "admin", "administrator", "system", "guest", "assistant"} {
		if name == reserved {
			return nil, fmt.Errorf("%q is reserved", name)
		}
	}
	if _, taken := w.Players[name]; taken {
		return nil, fmt.Errorf("player %s already exists", name)
	}
	if pass == "" {
		return nil, fmt.Errorf("a temporary password is required (they change it with passwd)")
	}
	acc := w.Bank.Accts[inviter]
	if acc == nil || acc.Balance < playerSetupFee {
		return nil, fmt.Errorf("%s cannot cover the $20.00 setup fee", inviter)
	}
	// the address is allocated, not carried: the pool is finite, and a full
	// house refuses honestly instead of overlapping
	address, err := w.AllocLANStatic()
	if err != nil {
		return nil, fmt.Errorf("no room on the household LAN: %v", err)
	}
	acc.Balance -= playerSetupFee
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -playerSetupFee,
		Memo: "vouched citizen " + name, Balance: acc.Balance})

	id := "pc-" + name
	d := w.addDevice(id, name+"-pc", "pc", name,
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Generic Desktop", 4, 3200, 8192, 65536, 1000, false, false}, address)
	d.Ifaces[0].GW = LANGateway
	d.Ifaces[0].Mode = "dhcp"
	mkUsers(d, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		name:   {Name: name, UID: 1000, Pass: pass, Groups: []string{name, "sudo"}, Home: "/home/" + name, Shell: "/bin/bash"},
	})
	seedFS(d, "pc")
	d.FS.MkdirAll("/home/"+name, 0755, name, name)
	d.FS.Write("/etc/resolv.conf", "nameserver "+LANGateway+"\n", 0644, "root", "root")
	d.FS.Write("/home/"+name+"/notes.txt", "fresh machine. change your temp password first: passwd\n", 0644, name, name)
	refreshPasswd(d)
	// the LAN resolver knows the newcomer: dnsmasq reads /etc/hosts, and
	// this line is the same fact the packet path will use
	if r := w.Devices["router-alex"]; r != nil {
		if data, ok := r.FS.Read("/etc/hosts"); ok {
			line := address + " " + name + "-pc " + name + "-pc.lan\n"
			if !containsLine(string(data), name+"-pc") {
				r.FS.Write("/etc/hosts", string(data)+line, 0644, "root", "root")
			}
		}
	}
	w.Bank.Accts[name] = &Account{Owner: name, Name: name}
	p := &Player{Name: name, Pass: pass, PC: d.ID, Router: "router-alex",
		NAS: "nas-alex", HouseKey: "house:" + name, Created: w.Sim}
	w.Players[name] = p
	d.Logf("info", "player", "citizen %s vouched by %s (setup $20.00)", name, inviter)
	w.AddEvent(d.ID, "info", "player", "%s joined the household, vouched by %s", name, inviter)
	return p, nil
}

func containsLine(s, sub string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return true
		}
	}
	return false
}
