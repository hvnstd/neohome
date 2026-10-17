package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Households (§46) — the home as a first-class entity
//
// A household is the founder's house plus whoever was vouched into it: the
// roster, and the shared infrastructure (router, NAS) every citizen inherits
// instead of carrying literals. `Player.HouseKey` points here. LAN subnets
// stay owned by addr.go — a household is people and shared boxes, never
// addresses.
// ---------------------------------------------------------------------------

// Household is one home: founder, citizens, shared boxes.
type Household struct {
	ID      string // "house:<founder>", the same string Player.HouseKey carries
	Founder string
	Members []string
	Router  string // device id of the shared router
	NAS     string // device id of the shared NAS
	// Subnet is this home's own /24 base ("10.77.1."). The default house is
	// LANSubnet; a family founded with `player invite --own-home` gets a free
	// base from AllocHouseholdSubnet and real boxes on it.
	Subnet  string
	Created time.Time
}

// householdOf resolves the inviter's household, backfilling the founder's
// for saves written before households existed.
func (w *World) householdOf(inviter string) *Household {
	if w.Households == nil {
		w.Households = map[string]*Household{}
	}
	if p := w.Players[inviter]; p != nil {
		if h, ok := w.Households[p.HouseKey]; ok {
			return h
		}
	}
	for _, h := range w.Households {
		return h
	}
	return nil
}

// seedHousehold registers the founder's house. Members and infra are facts
// here, not literals scattered across the invite path.
func seedHousehold(w *World) {
	if w.Households == nil {
		w.Households = map[string]*Household{}
	}
	if _, ok := w.Households["house:alex"]; ok {
		return
	}
	w.Households["house:alex"] = &Household{ID: "house:alex", Founder: "alex",
		Members: []string{"alex"}, Router: "router-alex", NAS: "nas-alex", Subnet: LANSubnet, Created: w.Sim}
}

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

// playerHomeInfraFee is what a citizen's OWN home costs, in cents, on top of
// the setup fee: a router and a NAS, provisioned for real on a subnet of
// their own. Home infras are what the second family's money pays for.
const playerHomeInfraFee = 8000

// playerOwnHomeFee is the full bill for founding a separate household.
const playerOwnHomeFee = playerSetupFee + playerHomeInfraFee

var playerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,15}$`)

// checkCitizenName applies the one name rule both invite paths share, with
// the messages players already know.
func (w *World) checkCitizenName(name string) error {
	if !playerNameRe.MatchString(name) {
		return fmt.Errorf("bad player name %q (lowercase letters, digits, hyphens, 2-16 chars)", name)
	}
	for _, reserved := range []string{"root", "admin", "administrator", "system", "guest", "assistant"} {
		if name == reserved {
			return fmt.Errorf("%q is reserved", name)
		}
	}
	if _, taken := w.Players[name]; taken {
		return fmt.Errorf("player %s already exists", name)
	}
	return nil
}

// InvitePlayer vouches a new human citizen into the inviter's household LAN.
func (w *World) InvitePlayer(inviter, name, pass string) (*Player, error) {
	if err := w.checkCitizenName(name); err != nil {
		return nil, err
	}
	if pass == "" {
		return nil, fmt.Errorf("a temporary password is required (they change it with passwd)")
	}
	acc := w.Bank.Accts[inviter]
	if acc == nil || acc.Balance < playerSetupFee {
		return nil, fmt.Errorf("%s cannot cover the $20.00 setup fee", inviter)
	}
	// the citizen inherits the household's shared boxes — the router whose
	// hosts file learns their name, the NAS they share — instead of every
	// invite carrying literals for them
	hh := w.householdOf(inviter)
	routerID, nasID, houseID := "router-alex", "nas-alex", "house:"+name
	base := LANSubnet
	if hh != nil {
		routerID, nasID, houseID = hh.Router, hh.NAS, hh.ID
		if hh.Subnet != "" {
			base = hh.Subnet
		}
	}
	// the address is allocated, not carried: the pool is finite, and a full
	// house refuses honestly instead of overlapping
	address, err := w.AllocLANStaticIn(base)
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
	d.Ifaces[0].GW = lanGatewayIn(base)
	d.Ifaces[0].Mode = "dhcp"
	mkUsers(d, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		name:   {Name: name, UID: 1000, Pass: pass, Groups: []string{name, "sudo"}, Home: "/home/" + name, Shell: "/bin/bash"},
	})
	seedFS(d, "pc")
	d.FS.MkdirAll("/home/"+name, 0755, name, name)
	d.FS.Write("/etc/resolv.conf", "nameserver "+lanGatewayIn(base)+"\n", 0644, "root", "root")
	d.FS.Write("/home/"+name+"/notes.txt", "fresh machine. change your temp password first: passwd\n", 0644, name, name)
	refreshPasswd(d)
	// the LAN resolver knows the newcomer: dnsmasq reads /etc/hosts, and
	// this line is the same fact the packet path will use
	if r := w.Devices[routerID]; r != nil {
		if data, ok := r.FS.Read("/etc/hosts"); ok {
			line := address + " " + name + "-pc " + name + "-pc.lan\n"
			if !containsLine(string(data), name+"-pc") {
				r.FS.Write("/etc/hosts", string(data)+line, 0644, "root", "root")
			}
		}
	}
	w.Bank.Accts[name] = &Account{Owner: name, Name: name}
	p := &Player{Name: name, Pass: pass, PC: d.ID, Router: routerID,
		NAS: nasID, HouseKey: houseID, Created: w.Sim}
	w.Players[name] = p
	if hh != nil {
		hh.Members = append(hh.Members, name)
	}
	d.Logf("info", "player", "citizen %s vouched by %s (setup $20.00)", name, inviter)
	w.AddEvent(d.ID, "info", "player", "%s joined the household, vouched by %s", name, inviter)
	return p, nil
}

// InvitePlayerOwnHome founds a genuinely separate family: the newcomer gets a
// /24 of their own, a router and a NAS seeded on it, and their own PC, instead
// of a bed in the inviter's house. It costs the setup fee plus a real
// home-infrastructure fee, and it refuses honestly when the inviter is broke
// or when every household subnet is taken. Money is the rate limit here too.
func (w *World) InvitePlayerOwnHome(inviter, name, pass string) (*Player, error) {
	if err := w.checkCitizenName(name); err != nil {
		return nil, err
	}
	if pass == "" {
		return nil, fmt.Errorf("a temporary password is required (they change it with passwd)")
	}
	acc := w.Bank.Accts[inviter]
	if acc == nil || acc.Balance < playerOwnHomeFee {
		return nil, fmt.Errorf("%s cannot cover the $20.00 setup and $80.00 home fee ($%d.00)", inviter, playerOwnHomeFee/100)
	}
	base, err := w.AllocHouseholdSubnet()
	if err != nil {
		return nil, err
	}
	gw := lanGatewayIn(base)

	acc.Balance -= playerOwnHomeFee
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -playerOwnHomeFee,
		Memo: "own home for citizen " + name, Balance: acc.Balance})

	routerID, nasID := "router-"+name, "nas-"+name
	houseID := "house:" + name

	// the household's own router: a real edge on the new /24, with the same
	// default-deny posture every seeded router ships — nothing forwarded and
	// no exposure until the owner configures it (§14)
	router := w.addDevice(routerID, "gateway", "router", name,
		OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"},
		Hardware{"Archer C7 (stock)", 1, 800, 128, 16, 1000, true, false}, gw)
	mkUsers(router, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
	})

	// the household's own NAS, at base.2
	nas := w.addDevice(nasID, "nas", "nas", name,
		OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"NAS Box", 2, 1600, 2048, 409600, 1000, false, false}, lanIPIn(base, 2))
	mkUsers(nas, map[string]*User{
		"root":   {Name: "root", UID: 0, Pass: "nasroot", Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"nobody": {Name: "nobody", UID: 65534, Groups: []string{"nogroup"}, Home: "/nonexistent", Shell: "/usr/sbin/nologin"},
	})

	id := "pc-" + name
	d := w.addDevice(id, name+"-pc", "pc", name,
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Generic Desktop", 4, 3200, 8192, 65536, 1000, false, false}, lanIPIn(base, 11))
	d.Ifaces[0].GW = gw
	d.Ifaces[0].Mode = "dhcp"
	mkUsers(d, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		name:   {Name: name, UID: 1000, Pass: pass, Groups: []string{name, "sudo"}, Home: "/home/" + name, Shell: "/bin/bash"},
	})

	// seed each box with the normal image. The devices already exist, so the
	// router's own /etc/hosts learns the new family (and only it); every file
	// that names a gateway derives it from that device's LAN base.
	seedFS(router, "router")
	seedFS(nas, "nas")
	seedFS(d, "pc")
	seedOwnHomeServices(router, nas)
	// the new router's log collector is its own NAS, exactly like the default
	router.FS.Write("/etc/config/system",
		"config system\n\toption hostname 'gateway'\n\toption log_ip '"+nas.FirstLANIP()+"'\n\toption log_port '514'\n",
		0644, "root", "root")
	d.FS.MkdirAll("/home/"+name, 0755, name, name)
	d.FS.Write("/home/"+name+"/notes.txt", "fresh machine. change your temp password first: passwd\n", 0644, name, name)
	refreshPasswd(d)

	w.Bank.Accts[name] = &Account{Owner: name, Name: name}
	p := &Player{Name: name, Pass: pass, PC: d.ID, Router: routerID,
		NAS: nasID, HouseKey: houseID, Created: w.Sim}
	w.Players[name] = p
	if w.Households == nil {
		w.Households = map[string]*Household{}
	}
	w.Households[houseID] = &Household{ID: houseID, Founder: name, Members: []string{name},
		Router: routerID, NAS: nasID, Subnet: base, Created: w.Sim}
	d.Logf("info", "player", "citizen %s founded their own home at %s0/24, vouched by %s ($%d.00)", name, base, inviter, playerOwnHomeFee/100)
	w.AddEvent(d.ID, "info", "player", "%s founded a new household at %s0/24, vouched by %s", name, base, inviter)
	return p, nil
}

// seedOwnHomeServices gives a founded family's router and NAS the daemons a
// seeded one runs: DNS/DHCP and ssh on the edge, the file/print/log services
// on the NAS. Without these the boxes would have addresses but answer nothing.
func seedOwnHomeServices(router, nas *Device) {
	router.Services["dnsmasq"] = &Service{Name: "dnsmasq", Desc: "DHCP+DNS forwarder", Port: 53, Proto: "udp+tcp",
		Scope: "lan", State: "running", Handler: "dns-forward", Conf: "/etc/dnsmasq.conf"}
	router.Services["dropbear"] = &Service{Name: "dropbear", Desc: "SSH", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear_2024.85"}
	router.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp",
		Scope: "lan", State: "running", Handler: "syslog"}
	nas.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.2"}
	nas.Services["nginx"] = &Service{Name: "nginx", Desc: "NAS web ui", Port: 8080, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "http-nas", Banner: "nginx"}
	nas.Services["nfsd"] = &Service{Name: "nfsd", Desc: "NFS exports", Port: 2049, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "nfs"}
	nas.Services["smbd"] = &Service{Name: "smbd", Desc: "Samba file shares", Port: 445, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "smb", Banner: "Samba", Conf: "/etc/samba/smb.conf"}
	nas.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "central log server", Port: 514, Proto: "udp",
		Scope: "lan", State: "running", Handler: "syslog", Conf: "/etc/rsyslog.conf"}
}

func containsLine(s, sub string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return true
		}
	}
	return false
}
