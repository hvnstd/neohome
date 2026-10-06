package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// §34 取证 / 网管 / ISP / Provider — the organisations that investigate
//
// The spec asks for a world that contains a home net admin, an enterprise net
// admin, an ISP, a hosting provider, a datacenter admin, a security team, an
// abuse desk and law enforcement — and says plainly that they are not
// omniscient NPCs. They work from real state: logs, traffic, accounts, time,
// addresses, providers, evidence, one step at a time.
//
// So every one of them here is a *device in the world*, with an address, an
// account, a mailbox, a portal service, a firewall and a blind spot. What they
// can see is exactly what their own position in the world lets them see:
//
//   - a desk only sees inside its own AS, plus whatever a reporter sent it;
//   - the registry sees allocation records and nothing else — an address maps
//     to a provider, an ASN, a range and an abuse contact, never to a person;
//   - a hosting provider names its *customer*, which is a billing relationship;
//   - an ISP will not name a residential subscriber to a complainant. That is
//     what a lawful request is for, and it is why the ladder has a real
//     referral and a real subpoena stage instead of a lookup that returns a
//     human being;
//   - a machine behind carrier-grade NAT cannot be attributed at all, and the
//     case says so and stops.
//
// The ladder runs on the world clock, gated by each step's own deadline, and
// every step leaves the artifacts a real one would: a mail in the customer's
// mailbox, a log line on the desk's own host, an entry in the case file, and —
// when the provider finally acts — a machine that really stops answering.

// ---- the organisations -----------------------------------------------------

// Desk kinds. Each is a different job with different powers: a security team
// protects one company's network, an ISP's NOC runs the access network, a
// provider's abuse desk answers for its customers' machines, and only law
// enforcement may compel a subscriber's identity out of an ISP.
const (
	DeskRegistry   = "registry"
	DeskISP        = "isp-noc"
	DeskAbuse      = "abuse-desk"
	DeskDatacenter = "datacenter"
	DeskEnterprise = "enterprise"
	DeskSOC        = "security-team"
	DeskLaw        = "law-enforcement"
)

// Desk is one organisation's investigating arm. It is anchored on a real
// device: the desk's machine must be powered and its intake service running, or
// nothing it owns moves.
type Desk struct {
	DeviceID string
	Kind     string
	Org      string
	ASN      int    // the network whose records it can read
	Mailbox  string // the account complaints and replies land in
	Portal   string // the service a report is filed through
	Staff    string // the group an account must be in to run the console
	SLA      int    // sim-minutes to the first triage
	Notice   int    // sim-minutes a customer gets to answer a notice
	Lawful   bool   // may compel subscriber records (law enforcement only)
	Notes    string
}

// AbuseCase is one investigation, from the first report to whatever the world
// could actually prove. Its fields are the artifacts, not a score: the evidence
// the reporter supplied, what the desk found in its own records, what it mailed
// and to whom, what the subject answered, and what was really done.
type AbuseCase struct {
	ID           string
	DeskID       string
	Opened       time.Time
	Updated      time.Time
	Stage        string
	SubjectIP    string
	SubjectHost  string
	SubjectDev   string   // once the organisation's own records name a machine
	Account      string   // what the provider's account record calls the holder
	Reporter     string   // who filed it: a player, a device id, or the desk itself
	AlsoReported []string // later reporters whose complaint was merged into this case
	Kind         string
	Evidence     []string // supplied by the reporter (their own logs)
	Findings     []string // found in this organisation's own records
	Notices      []string // mails sent about this case, with the delivery result
	Reply        string   // the subject's own answer, if one arrived
	TriagedBy    string   // the operator whose decision started the ladder
	Action       string   // what was really done: suspended | filtered | none
	ReferredTo   string   // desk id this was handed to
	Law          bool     // now in law enforcement's hands
	Due          time.Time
	History      []string
}

// AbuseLog is the world's case file: every investigation every organisation
// ever opened, in order.
type AbuseLog struct {
	Seq  int
	List []*AbuseCase
}

// Case stages, in the order the ladder walks them.
const (
	StageFiled      = "filed"
	StageRejected   = "rejected"
	StageTriaged    = "triaged"
	StageReferred   = "referred"
	StageNotified   = "notified"
	StageClosed     = "closed"
	StageSuspended  = "suspended"
	StageEscalated  = "escalated"
	StageLEOpened   = "law:opened"
	StageLERequest  = "law:request"
	StageLEDisclose = "law:disclosed"
	StageLEClosed   = "law:closed"
)

func caseTerminal(stage string) bool {
	switch stage {
	case StageRejected, StageReferred, StageClosed, StageLEOpened, StageLEClosed:
		return true
	}
	return false
}

// suspensionTerm is how long a provider's port suspension lasts before the
// customer's machine is put back on the network. A suspension is a term, not a
// death sentence: the provider acted, the term lapses, and the machine returns
// — which is also what keeps the world's own scanner part of the world instead
// of a one-off event.
const suspensionTerm = 24 * time.Hour

// ---- seeding ---------------------------------------------------------------

// seedDesks builds §34's organisations, each one a machine with an address.
// It is idempotent: a world saved before §34 existed gets the desks by calling
// this from LoadWorld, and a fresh world gets them here.
func seedDesks(w *World) {
	if len(w.Desks) > 0 {
		return
	}
	mk := func(id, hostname, kind, org string, asn int, hw Hardware, os OSInfo) *Device {
		d := w.Devices[id]
		if d == nil {
			d = w.addDevice(id, hostname, "infra", "", os, hw, "")
			ip := w.allocInAS(asn)
			d.Ifaces = append(d.Ifaces, &Iface{Name: "eth0", IP: ip, MAC: macFor(id + "-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
			w.IPMap[ip] = id
			d.Installed["sshd"] = &VPkg{Name: "sshd", Version: "9.7"}
			d.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp",
				Scope: "wan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
		}
		_ = kind
		_ = org
		return d
	}
	// every public host in this world runs an MTA: mail is how organisations
	// that do not share a LAN talk to each other, and an abuse desk without one
	// could not answer a complaint
	mail := func(d *Device, domain string) {
		d.Services["smtpd"] = &Service{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp",
			Scope: "wan", State: "running", Handler: "smtpd", Banner: "220 " + d.Hostname + " ESMTP ready",
			Conf: "/etc/mail/smtpd.conf"}
		d.FS.MkdirAll("/etc/mail", 0755, "root", "root")
		d.FS.Write("/etc/mail/smtpd.conf",
			"listen on eth0 port 25\nlisten on lo port 25\naction \"local\" mbox\nmatch from any for local\n", 0644, "root", "root")
		_ = domain
	}
	host := func(d *Device, users map[string]*User) {
		mkUsers(d, users)
		seedFS(d, "infra")
		d.FS.Write("/etc/motd", d.Notes+"\n", 0644, "root", "root")
	}
	add := func(dk *Desk, users map[string]*User) {
		d := w.Devices[dk.DeviceID]
		host(d, users)
		mail(d, "example")
		w.Desks = append(w.Desks, dk)
	}
	// an office box is not a household PC: its resolver is the office
	// gateway, its firewall drops what arrives from the WAN side, and it lets
	// its own LAN in on the services it actually offers
	lanHost := func(d *Device, users map[string]*User, accepts []Rule) {
		mkUsers(d, users)
		seedFS(d, "infra")
		d.FS.Write("/etc/resolv.conf", "nameserver 10.90.0.1\nsearch meridian.example\n", 0644, "root", "root")
		d.FS.Write("/etc/motd", d.Notes+"\n", 0644, "root", "root")
		seedHostFirewall(d, d.FS, "DROP", accepts)
	}

	// 1. the registry: allocation records, and nothing else. Port 43 is what a
	// whois client connects to, and its answers are the world's Lookup().
	reg := mk("registry", "rdap.neocore.example", DeskRegistry, "NeoCore Registry Services", asCore,
		Hardware{"VM", 2, 2000, 1024, 20480, 100, false, false}, OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"})
	reg.Notes = "Registry services. Publishable allocation records only: the address identifies a network, not a person."
	reg.Services["whoisd"] = &Service{Name: "whoisd", Desc: "registry lookup", Port: 43, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "whois-registry", Banner: "NeoCore Registry Services"}
	add(&Desk{DeviceID: "registry", Kind: DeskRegistry, Org: "NeoCore Registry Services", ASN: asCore,
		Mailbox: "registry", Portal: "whoisd", Staff: "registry", SLA: 240, Notice: 1440},
		map[string]*User{"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"}})

	// 2. the ISP's network operations centre: it runs the access network, it
	// sees its own routers' logs, and it will not hand a subscriber to a
	// stranger. Complaints against a residential line end in a notice to the
	// subscriber, or in law enforcement asking properly.
	noc := mk("netcrest-noc", "noc.netcrest.example", DeskISP, "NetCrest Communications", asNetCrest,
		Hardware{"VM", 4, 2400, 4096, 102400, 500, false, false}, OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"})
	noc.Notes = "NetCrest NOC. Access network operations; subscriber records are confidential and released on lawful request only."
	noc.Services["httpd"] = &Service{Name: "httpd", Desc: "NOC portal", Port: 80, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "http-noc", Banner: "nginx"}
	// an ISP exports the flows of its access network: it can see that one of
	// its lines is noisy before anybody complains, and it still cannot say who
	// the subscriber is without lawful process
	noc.Services["netflowd"] = &Service{Name: "netflowd", Desc: "flow export (access network)", Port: 2055, Proto: "udp",
		Scope: "lan", State: "running", Handler: "netflow"}
	add(&Desk{DeviceID: "netcrest-noc", Kind: DeskISP, Org: "NetCrest Communications", ASN: asNetCrest,
		Mailbox: "abuse", Portal: "httpd", Staff: "noc", SLA: 120, Notice: 180},
		map[string]*User{
			"root":       {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"abuse":      {Name: "abuse", UID: 1000, Pass: "noc-abuse-2026", Groups: []string{"noc"}, Home: "/home/abuse", Shell: "/bin/bash"},
			"contractor": {Name: "contractor", UID: 1001, Pass: "nc-shift-2026", Groups: []string{"noc"}, Home: "/home/contractor", Shell: "/bin/bash"},
		})

	// 3. the hosting provider's abuse desk and the datacenter it sits in. The
	// desk answers for what its customers' machines do; the DC console is where
	// a machine is racked, ported and eventually suspended.
	ab := mk("novapanel-abuse", "abuse.novapanel.example", DeskAbuse, "NovaPanel Hosting BV", asNova,
		Hardware{"VM", 2, 2000, 4096, 51200, 200, false, false}, OSInfo{"Ubuntu", "24.04", "6.8.0", "x86_64", "bash"})
	ab.Notes = "NovaPanel abuse desk. Terms of service enforcement, customer notices and suspensions."
	ab.Services["httpd"] = &Service{Name: "httpd", Desc: "abuse portal", Port: 80, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "http-abuse", Banner: "nginx"}
	add(&Desk{DeviceID: "novapanel-abuse", Kind: DeskAbuse, Org: "NovaPanel Hosting BV", ASN: asNova,
		Mailbox: "abuse", Portal: "httpd", Staff: "abuse", SLA: 60, Notice: 180},
		map[string]*User{
			"root":  {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"abuse": {Name: "abuse", UID: 1000, Pass: "np-abuse-2026", Groups: []string{"abuse"}, Home: "/home/abuse", Shell: "/bin/bash"},
			// a shift contractor: a real account, in the desk's own group, with
			// its own password the job board hands over. Console access is this
			// account — there is no other way in.
			"contractor": {Name: "contractor", UID: 1001, Pass: "np-shift-2026", Groups: []string{"abuse"}, Home: "/home/contractor", Shell: "/bin/bash"},
		})

	dc := mk("novapanel-dc", "dc1.novapanel.example", DeskDatacenter, "NovaPanel DC1 Amsterdam", asNova,
		Hardware{"VM", 4, 2400, 8192, 204800, 500, false, false}, OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"})
	dc.Notes = "NovaPanel DC1. Rack, power and port-level control of the customer machines in this hall."
	dc.Services["httpd"] = &Service{Name: "httpd", Desc: "DC console", Port: 80, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "http-dc", Banner: "nginx"}
	// the datacenter exports the flows of the machines in its hall: this is
	// what lets a provider notice its own customers' abuse without waiting for
	// somebody else's complaint. Turn the exporter off and the desk goes back
	// to only knowing what it is told.
	dc.Services["netflowd"] = &Service{Name: "netflowd", Desc: "flow export (egress visibility)", Port: 2055, Proto: "udp",
		Scope: "lan", State: "running", Handler: "netflow"}
	add(&Desk{DeviceID: "novapanel-dc", Kind: DeskDatacenter, Org: "NovaPanel DC1", ASN: asNova,
		Mailbox: "ops", Portal: "httpd", Staff: "ops", SLA: 30, Notice: 120},
		map[string]*User{
			"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"ops":  {Name: "ops", UID: 1000, Pass: "np-dc-2026", Groups: []string{"ops"}, Home: "/home/ops", Shell: "/bin/bash"},
		})

	// 4. the enterprise: Meridian Systems runs its own AS, its own office LAN
	// and its own security team. The SOC is on the public internet because that
	// is how it receives reports; the servers behind the gateway are not.
	mh := mk("meridian-hq", "gw.meridian.example", DeskEnterprise, "Meridian Systems", asPeer,
		Hardware{"Edge Router", 2, 1800, 2048, 8192, 500, false, false}, OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"})
	mh.Notes = "Meridian Systems office gateway. NAT for the office LAN, no inbound services published."
	mh.Ifaces = append(mh.Ifaces, &Iface{Name: "eth1", IP: "10.90.0.1", CIDR: "10.90.0.0/24", MAC: macFor("meridian-lan"), Zone: "lan", Up: true})
	w.IPMap["10.90.0.1"] = "meridian-hq"
	mh.Services["dnsmasq"] = &Service{Name: "dnsmasq", Desc: "DHCP+DNS", Port: 53, Proto: "udp+tcp",
		Scope: "lan", State: "running", Handler: "dns-forward", Conf: "/etc/dnsmasq.conf"}
	mh.Services["dropbear"] = &Service{Name: "dropbear", Desc: "SSH", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear_2024.85"}
	mh.Services["smtpd"] = &Service{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "smtpd", Banner: "220 mail.meridian.example ESMTP ready"}
	host(mh, map[string]*User{"admin": {Name: "admin", UID: 1000, Pass: "meridian-admin", Groups: []string{"admin", "sudo"}, Home: "/home/admin", Shell: "/bin/ash"}})
	mh.FS.Write("/etc/dnsmasq.conf", "interface=eth1\ndhcp-range=10.90.0.100,10.90.0.200,12h\ndomain=meridian.example\n", 0644, "root", "root")
	mh.FS.Write("/etc/config/firewall", RenderUCIFirewall(&FirewallState{WANInput: "REJECT", ForwardPolicy: "REJECT", LogDrops: true}), 0644, "root", "root")

	for _, ent := range []struct {
		id, hostname string
		ip           string
		hw           Hardware
		os           OSInfo
		services     map[string]*Service
	}{
		{"meridian-dc", "dc.meridian.example", "10.90.0.10", Hardware{"Server", 8, 3000, 16384, 204800, 500, false, false},
			OSInfo{"Windows Server", "2022", "-", "x86_64", "cmd"},
			map[string]*Service{"ldap": {Name: "ldap", Desc: "directory service", Port: 389, Proto: "tcp", Scope: "lan", State: "running", Handler: "ldap"},
				"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.4"}}},
		{"meridian-fs", "fs.meridian.example", "10.90.0.20", Hardware{"Server", 4, 2400, 8192, 409600, 500, false, false},
			OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
			map[string]*Service{"smbd": {Name: "smbd", Desc: "Samba file shares", Port: 445, Proto: "tcp", Scope: "lan", State: "running", Handler: "smb", Conf: "/etc/samba/smb.conf"},
				"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.2"}}},
		{"meridian-ws", "ws-07.meridian.example", "10.90.0.31", Hardware{"Workstation", 4, 3200, 8192, 102400, 500, false, false},
			OSInfo{"Windows", "11", "-", "x86_64", "cmd"},
			map[string]*Service{"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.4"}}},
	} {
		d := w.Devices[ent.id]
		if d == nil {
			d = w.addDevice(ent.id, ent.hostname, "pc", "meridian", ent.os, ent.hw, ent.ip)
		}
		for _, svc := range ent.services {
			d.Services[svc.Name] = svc
		}
		d.Ifaces[0].GW = "10.90.0.1"
		d.Installed["sshd"] = &VPkg{Name: "sshd", Version: "9.4"}
		d.Notes = "Meridian Systems office host. The security team watches this segment."
		lanHost(d, map[string]*User{
			"root":  {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"admin": {Name: "admin", UID: 1000, Pass: "meridian-admin", Groups: []string{"admin", "sudo"}, Home: "/home/admin", Shell: "/bin/bash"},
		}, []Rule{
			{Name: "lan-ssh", Proto: "tcp", Port: 22, Target: "ACCEPT", Src: "lan"},
			{Name: "lan-smb", Proto: "tcp", Port: 445, Target: "ACCEPT", Src: "lan"},
			{Name: "lan-ldap", Proto: "tcp", Port: 389, Target: "ACCEPT", Src: "lan"},
		})
	}
	w.Devices["meridian-fs"].FS.Write("/etc/samba/smb.conf",
		"[global]\n   workgroup = MERIDIAN\n   server string = Meridian file server\n   security = user\n\n[projects]\n   path = /srv/projects\n   valid users = @staff\n   read only = no\n   browseable = yes\n", 0644, "root", "root")
	// the office admin runs the file server, so the admin account belongs to the
	// group the share trusts; without that, the share is a directory nobody
	// inside the company can actually manage
	if fs := w.Devices["meridian-fs"]; fs != nil {
		if u := fs.FindUser("admin"); u != nil && !inAnyGroup(u, "staff") {
			u.Groups = append(u.Groups, "staff")
		}
	}
	w.Devices["meridian-fs"].FS.MkdirAll("/srv/projects", 0775, "root", "staff")

	soc := mk("meridian-soc", "soc.meridian.example", DeskSOC, "Meridian Systems Security Team", asPeer,
		Hardware{"VM", 4, 2400, 8192, 102400, 500, false, false}, OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"})
	soc.Notes = "Meridian SOC. Watches the office network, answers reports about Meridian's own address space."
	soc.Services["httpd"] = &Service{Name: "httpd", Desc: "SOC portal", Port: 80, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "http-soc", Banner: "nginx"}
	soc.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "central log server", Port: 514, Proto: "udp",
		Scope: "lan", State: "running", Handler: "syslog", Conf: "/etc/rsyslog.conf"}
	add(&Desk{DeviceID: "meridian-soc", Kind: DeskSOC, Org: "Meridian Systems", ASN: asPeer,
		Mailbox: "soc", Portal: "httpd", Staff: "soc", SLA: 90, Notice: 480},
		map[string]*User{
			"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"soc":  {Name: "soc", UID: 1000, Pass: "meridian-soc-2026", Groups: []string{"soc"}, Home: "/home/soc", Shell: "/bin/bash"},
		})

	// 5. law enforcement: a cybercrime unit with a real portal, its own case
	// numbers, and the only desk in the world that may compel a subscriber
	// record. It does not teleport: it reads the file it was handed, asks the
	// ISP officially, and waits for the answer.
	le := mk("le-cyber", "cnu.gov.example", DeskLaw, "Cybercrime National Unit", asGov,
		Hardware{"VM", 4, 2400, 4096, 51200, 500, false, false}, OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"})
	le.Notes = "Cybercrime National Unit. Case intake from providers and ISPs; subscriber data on lawful request only."
	le.Services["httpd"] = &Service{Name: "httpd", Desc: "case portal", Port: 80, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "http-le", Banner: "nginx"}
	add(&Desk{DeviceID: "le-cyber", Kind: DeskLaw, Org: "Cybercrime National Unit", ASN: asGov,
		Mailbox: "cases", Portal: "httpd", Staff: "le", SLA: 60, Notice: 240, Lawful: true},
		map[string]*User{
			"root":  {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
			"cases": {Name: "cases", UID: 1000, Pass: "cnu-cases-2026", Groups: []string{"le"}, Home: "/home/cases", Shell: "/bin/bash"},
		})

	// the zone gains the names an outsider would actually use
	w.Records = append(w.Records,
		DNSRecord{Name: "rdap.neocore.example", IP: wanIP(reg)},
		DNSRecord{Name: "noc.netcrest.example", IP: wanIP(noc)},
		DNSRecord{Name: "abuse.novapanel.example", IP: wanIP(ab)},
		DNSRecord{Name: "dc1.novapanel.example", IP: wanIP(dc)},
		DNSRecord{Name: "soc.meridian.example", IP: wanIP(soc)},
		DNSRecord{Name: "gw.meridian.example", IP: wanIP(mh)},
		DNSRecord{Name: "cnu.gov.example", IP: wanIP(le)},
		DNSRecord{Name: "dc.meridian.example", IP: "10.90.0.10"},
		DNSRecord{Name: "fs.meridian.example", IP: "10.90.0.20"},
	)
}

// ---- what a desk can see ---------------------------------------------------

// DeskDevice is the machine the organisation works from.
func (w *World) DeskDevice(dk *Desk) *Device {
	if dk == nil {
		return nil
	}
	return w.Devices[dk.DeviceID]
}

// DeskOperational is the precondition for every action a desk takes: someone
// has to be at work. A desk whose host is dark, or whose intake service is
// stopped, opens nothing and answers nothing — the queue really waits.
func (w *World) DeskOperational(dk *Desk) bool {
	d := w.DeskDevice(dk)
	if d == nil || !d.Powered() {
		return false
	}
	if svc := d.Svc(dk.Portal); svc == nil || svc.State != "running" {
		return false
	}
	return true
}

// DeskForASN finds the organisation that speaks for a network.
func (w *World) DeskForASN(asn int) *Desk {
	best := (*Desk)(nil)
	for _, dk := range w.Desks {
		if dk.ASN != asn {
			continue
		}
		// a law desk does not answer for traffic: it investigates it
		if dk.Kind == DeskLaw {
			continue
		}
		if best == nil || rankDesk(dk) > rankDesk(best) {
			best = dk
		}
	}
	return best
}

// rankDesk orders desks by how close they are to the traffic they answer for:
// the customer-facing desk of the network is the one that acts, the datacenter
// is where it is enforced.
func rankDesk(dk *Desk) int {
	switch dk.Kind {
	case DeskAbuse, DeskISP:
		return 3
	case DeskSOC, DeskEnterprise:
		return 2
	case DeskDatacenter:
		return 1
	}
	return 0
}

// DeskFor returns the organisation that answers for an address. This is the
// whole of §34's "IP 查到谁": a lookup yields the network, and the network has
// a desk — never a person.
func (w *World) DeskFor(ip string) *Desk {
	info := ClassifyAddr(ip)
	if info.Kind == KindShared {
		return w.DeskForASN(asNova) // a provider's NAT egress: the provider answers
	}
	if w.WAN == nil {
		return nil
	}
	var asn int
	if info.Family == 6 {
		if as := w.WAN.ASFor6(ip); as != nil {
			asn = as.ASN
		}
	} else if as := w.WAN.ASFor(ip); as != nil {
		asn = as.ASN
	}
	if asn == 0 {
		return nil
	}
	return w.DeskForASN(asn)
}

// deskFindings is what an organisation can honestly say about an address from
// its OWN records: the flows its own machines saw, the alerts they raised and
// the log lines their collectors still hold. It reads nothing else — which is
// why a report from outside has to carry the reporter's evidence with it.
func (w *World) deskFindings(dk *Desk, ip string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || d.ID == dk.DeviceID {
			continue
		}
		if as := d.ASFor(); as == nil || as.ASN != dk.ASN {
			continue // not this organisation's network
		}
		if !d.Powered() {
			continue // a machine that is off has no logs to contribute
		}
		_ = id
		n, ports := 0, map[int]bool{}
		for _, f := range d.RecentFlows(24 * time.Hour) {
			if f.Src != ip {
				continue
			}
			switch f.Verdict {
			case FlowFiltered, FlowRefused, FlowBanned:
				n++
				ports[f.Port] = true
			}
		}
		if n > 0 {
			pl := make([]int, 0, len(ports))
			for p := range ports {
				pl = append(pl, p)
			}
			sort.Ints(pl)
			line := fmt.Sprintf("%s: %d rejected attempt(s) from %s to %d port(s) in the last 24h",
				d.Hostname, n, ip, len(pl))
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
		for _, a := range d.Sec().Alerts {
			if a.Src != ip {
				continue
			}
			line := fmt.Sprintf("%s: %s alert %q at %s", d.Hostname, a.Tool, a.Rule, a.At.Format("15:04"))
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
		for _, rl := range d.Sec().RemoteLog {
			if !strings.Contains(rl.Line, ip) {
				continue
			}
			line := fmt.Sprintf("central log (%s): %s", rl.From, strings.TrimSpace(rl.Line))
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
			break
		}
	}
	sort.Strings(out)
	return out
}

// AbuseEvidence is what one machine can say about an address from its own
// state. A report is a claim about the reporter's records: without them the
// desk has nothing to work from, and a report that invents evidence is exactly
// what the spec forbids. Returns nil when this machine never saw the address.
func AbuseEvidence(d *Device, ip string, window time.Duration) []string {
	var out []string
	flows := 0
	ports := map[int]bool{}
	verdicts := map[FlowVerdict]int{}
	var first, last time.Time
	for _, f := range d.RecentFlows(window) {
		if f.Src != ip {
			continue
		}
		flows++
		ports[f.Port] = true
		verdicts[f.Verdict]++
		if first.IsZero() || f.At.Before(first) {
			first = f.At
		}
		if f.At.After(last) {
			last = f.At
		}
	}
	if flows > 0 {
		var vs []string
		for _, v := range []FlowVerdict{FlowFiltered, FlowRefused, FlowBanned, FlowAccepted} {
			if verdicts[v] > 0 {
				vs = append(vs, fmt.Sprintf("%d %s", verdicts[v], v))
			}
		}
		out = append(out, fmt.Sprintf("%d connection attempt(s) from %s to %d distinct port(s), %s, %s–%s",
			flows, ip, len(ports), strings.Join(vs, ", "),
			first.Format("15:04"), last.Format("15:04")))
	}
	for _, a := range d.Sec().Alerts {
		if a.Src == ip {
			out = append(out, fmt.Sprintf("IDS alert at %s: %s", a.At.Format("15:04"), a.Msg))
		}
	}
	fails := 0
	for i, fip := range d.Sec().FailIP {
		if fip == ip && i < len(d.Sec().Fails) {
			fails++
		}
	}
	if fails > 0 {
		out = append(out, fmt.Sprintf("%d failed authentication attempt(s) from %s recorded by the login service", fails, ip))
	}
	if data, ok := d.FS.Read("/var/log/syslog"); ok {
		shown := 0
		for _, line := range strings.Split(string(data), "\n") {
			if shown >= 3 || !strings.Contains(line, ip) {
				continue
			}
			out = append(out, "log: "+strings.TrimSpace(line))
			shown++
		}
	}
	return out
}

// ---- filing a report -------------------------------------------------------

// ReportAbuse files a complaint about an address with the organisation that
// answers for it. The evidence is the reporter's own, and the report only
// leaves the machine if the desk's intake service really answers — a report
// into a black hole would be a lie the player could not see.
func (w *World) ReportAbuse(rep *Device, actor, ip, kind, note string, window time.Duration) (*AbuseCase, error) {
	if rep == nil {
		return nil, fmt.Errorf("no reporting machine")
	}
	if !rep.Powered() {
		return nil, fmt.Errorf("%s has no power", rep.Hostname)
	}
	evidence := AbuseEvidence(rep, ip, window)
	if len(evidence) == 0 {
		// a note is the reporter's opinion; it is not a record. The desk
		// refuses a complaint with nothing behind it, which is what keeps
		// reporting an act with consequences rather than a button.
		return nil, fmt.Errorf("your own records hold nothing about %s in the last %s — a report without evidence is a guess",
			ip, window)
	}
	if note != "" {
		evidence = append(evidence, "reporter's note: "+note)
	}
	desk := w.DeskFor(ip)
	if desk == nil {
		return nil, fmt.Errorf("no organisation answers for %s: the address is not announced by any network we can name", ip)
	}
	d := w.DeskDevice(desk)
	if d == nil {
		return nil, fmt.Errorf("no such organisation")
	}
	port := 80
	if svc := d.Svc(desk.Portal); svc != nil {
		port = svc.Port
	}
	svc, _, msg := Dial(rep, wanIP(d), port)
	if svc == nil {
		return nil, fmt.Errorf("%s (%s) is unreachable: %s", desk.Org, d.Hostname, msg)
	}
	// A complaint belongs to the household whose machine made it: the gateway is
	// the account holder's equipment, so a report filed from it is the account
	// holder's complaint (the operator who typed it is named in the file). This
	// is what lets a player read back the ticket they filed from their router.
	who := actor
	if rep.Owner != "" {
		who = rep.Owner
	}
	c := w.openOrMerge(desk, ip, who, kind, evidence, desk.SLA)
	via := rep.Hostname
	if who != actor {
		via += " as " + actor
	}
	c.History = append(c.History, fmt.Sprintf("%s report accepted over %s from %s", w.Sim.Format("15:04"), svc.Name, via))
	d.Logf("info", "abuse", "ticket %s opened: %s reported by %s (%s)", c.ID, ip, who, kind)
	w.AddEvent(d.ID, "notice", "abuse", "%s opened ticket %s about %s (%s)", desk.Org, c.ID, ip, kind)
	return c, nil
}

// mergeInto folds a new complaint about an address into the desk's existing
// open case about the same address, attaching the new evidence. This is what a
// real ticket queue does with its third report of the same scanner, and it is
// why a busy desk has a queue instead of a stack of duplicates.
func (w *World) mergeInto(c *AbuseCase, reporter string, evidence []string, kind string) bool {
	if reporter != c.Reporter {
		for _, r := range c.AlsoReported {
			if r == reporter {
				goto added
			}
		}
		c.AlsoReported = append(c.AlsoReported, reporter)
	}
added:
	c.Evidence = append(c.Evidence, evidence...)
	if len(c.Evidence) > 40 {
		c.Evidence = c.Evidence[len(c.Evidence)-40:]
	}
	c.History = append(c.History, fmt.Sprintf("%s merged with a new report from %s (%s)", w.Sim.Format("15:04"), reporter, kind))
	c.Updated = w.Sim
	return true
}

// openOrMerge is the single entry point for a complaint arriving at a desk: an
// existing open case absorbs it, otherwise a new case is opened.
func (w *World) openOrMerge(dk *Desk, ip, reporter, kind string, evidence []string, sla int) *AbuseCase {
	for _, c := range w.Cases(dk.DeviceID) {
		if c.SubjectIP == ip && !caseTerminal(c.Stage) {
			w.mergeInto(c, reporter, evidence, kind)
			return c
		}
	}
	return w.openCase(dk, ip, reporter, kind, evidence, sla)
}

// openCase creates the case record and sets its first deadline.
func (w *World) openCase(desk *Desk, ip, reporter, kind string, evidence []string, sla int) *AbuseCase {
	w.ensureAbuse()
	w.Abuse.Seq++
	c := &AbuseCase{
		ID:        fmt.Sprintf("AB-%06d", w.Abuse.Seq),
		DeskID:    desk.DeviceID,
		Opened:    w.Sim,
		Updated:   w.Sim,
		Stage:     StageFiled,
		SubjectIP: ip,
		Reporter:  reporter,
		Kind:      kind,
		Evidence:  evidence,
		Due:       w.Sim.Add(time.Duration(sla) * time.Minute),
	}
	if kind == "" {
		c.Kind = "abuse"
	}
	c.History = append(c.History, fmt.Sprintf("%s filed by %s (%s)", w.Sim.Format("15:04"), reporter, c.Kind))
	w.Abuse.List = append(w.Abuse.List, c)
	return c
}

func (w *World) ensureAbuse() {
	if w.Abuse == nil {
		w.Abuse = &AbuseLog{}
	}
}

// Cases returns the cases a desk holds, newest first.
func (w *World) Cases(deskID string) []*AbuseCase {
	if w.Abuse == nil {
		return nil
	}
	var out []*AbuseCase
	for i := len(w.Abuse.List) - 1; i >= 0; i-- {
		c := w.Abuse.List[i]
		if deskID == "" || c.DeskID == deskID {
			out = append(out, c)
		}
	}
	return out
}

// CaseByID finds a case by its ticket number, accepting a unique prefix.
func (w *World) CaseByID(id string) *AbuseCase {
	if w.Abuse == nil {
		return nil
	}
	for _, c := range w.Abuse.List {
		if c.ID == id || (len(id) >= 4 && strings.HasPrefix(c.ID, id)) {
			return c
		}
	}
	return nil
}

// CasesInvolving lists the cases a person is part of, on either side: as the
// one who reported, or as the holder of the machine being investigated. A
// player should be able to read their own file — and nothing else, which is
// what the staff console is for.
func (w *World) CasesInvolving(name string) []*AbuseCase {
	if w.Abuse == nil {
		return nil
	}
	var out []*AbuseCase
	for i := len(w.Abuse.List) - 1; i >= 0; i-- {
		c := w.Abuse.List[i]
		if c.Reporter == name || c.Account == name {
			out = append(out, c)
			continue
		}
		merged := false
		for _, r := range c.AlsoReported {
			if r == name {
				merged = true
				break
			}
		}
		if merged {
			out = append(out, c)
			continue
		}
		if c.SubjectDev != "" {
			if d := w.Devices[c.SubjectDev]; d != nil && (d.Owner == name || (name == "alex" && d.Owner == "alex")) {
				out = append(out, c)
			}
		}
	}
	return out
}

// ---- the ladder ------------------------------------------------------------

// AbuseTick runs the organisations' own clocks. It is called after the world's
// scanner and its defensive tools, so a desk reads the flows and alerts those
// produced in this same tick rather than last tick's leftovers.
func (w *World) AbuseTick() {
	w.ensureAbuse()
	for _, dk := range w.Desks {
		if !w.DeskOperational(dk) {
			continue
		}
		w.deskReadInbox(dk)
		w.deskOpenOwnCases(dk)
		for _, c := range w.Cases(dk.DeviceID) {
			if caseTerminal(c.Stage) {
				continue
			}
			w.advanceCase(dk, c)
		}
	}
}

// deskEgressVisibility reports whether an organisation can see the traffic its
// own customers send *out*: a collector that exports flows out of its own
// address space. Without one it only knows what is reported to it, which is the
// honest difference between an ISP that gets a complaint and one that watches.
func (w *World) deskEgressVisibility(dk *Desk) bool {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !d.Powered() {
			continue
		}
		if as := d.ASFor(); as == nil || as.ASN != dk.ASN {
			continue
		}
		if svc := d.Svc("netflowd"); svc != nil && svc.State == "running" {
			return true
		}
	}
	return false
}

// ownAddressSpace lists the addresses an organisation's network holds.
func (w *World) ownAddressSpace(dk *Desk) map[string]bool {
	out := map[string]bool{}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		if as := d.ASFor(); as == nil || as.ASN != dk.ASN {
			continue
		}
		for _, ip := range DeviceAddrs(d) {
			if ip == "127.0.0.1" || ClassifyAddr(ip).Kind == KindLoopback {
				continue
			}
			out[ip] = true
		}
		if v6 := d.FirstWANv6(); v6 != "" {
			out[v6] = true
		}
	}
	return out
}

// deskOpenOwnCases is the world's own source of investigations: a desk opens a
// case when its own network's records show a source that keeps coming back.
// The threshold is the same kind of count a real NOC pages on, and a source is
// only re-opened on after the previous case about it is done.
func (w *World) deskOpenOwnCases(dk *Desk) {
	if dk.Kind == DeskLaw {
		return // the police react to a file, they do not watch packets
	}
	// when one organisation has two desks on one network (a provider's abuse
	// desk and its datacenter), the customer-facing desk runs the queue: the
	// datacenter enforces, it does not investigate
	for _, other := range w.Desks {
		if other.ASN == dk.ASN && rankDesk(other) > rankDesk(dk) {
			return
		}
	}
	count := map[string]int{}
	hosts := map[string]map[string]bool{}
	why := map[string]string{}
	egress := w.deskEgressVisibility(dk)
	mine := map[string]bool{}
	if egress {
		mine = w.ownAddressSpace(dk)
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !d.Powered() {
			continue
		}
		own := false
		if as := d.ASFor(); as != nil && as.ASN == dk.ASN {
			own = true
		}
		// two ways a desk learns of abuse from its own network: a machine of
		// its own is being hammered (inbound), or the flow exporter shows one
		// of its addresses hammering somebody else (outbound)
		if !own && !egress {
			continue
		}
		for _, f := range d.RecentFlows(6 * time.Hour) {
			if f.Verdict != FlowFiltered && f.Verdict != FlowRefused && f.Verdict != FlowBanned {
				continue
			}
			if !own && !mine[f.Src] {
				continue
			}
			if own && egress && mine[f.Src] {
				continue // our own machine talking to our own machine
			}
			count[f.Src]++
			if hosts[f.Src] == nil {
				hosts[f.Src] = map[string]bool{}
			}
			hosts[f.Src][d.Hostname] = true
			if why[f.Src] == "" {
				why[f.Src] = "rejected connections"
			}
		}
		if !own {
			continue // an IDS alert matters on the network that owns the machine
		}
		for _, a := range d.Sec().Alerts {
			if a.Src == "" || mine[a.Src] {
				continue
			}
			count[a.Src] += 4 // an IDS alert is worth more than one dropped packet
			if hosts[a.Src] == nil {
				hosts[a.Src] = map[string]bool{}
			}
			hosts[a.Src][d.Hostname] = true
			why[a.Src] = "IDS alert: " + a.Rule
		}
		for i, fip := range d.Sec().FailIP {
			if i >= len(d.Sec().Fails) || fip == "" || mine[fip] {
				continue
			}
			count[fip] += 2
			if hosts[fip] == nil {
				hosts[fip] = map[string]bool{}
			}
			hosts[fip][d.Hostname] = true
			why[fip] = "failed logins"
		}
	}
	for ip, n := range count {
		if n < 8 {
			continue
		}
		if w.openCaseAbout(dk, ip) {
			continue
		}
		// a desk that has just dealt with an address does not open another
		// ticket about it every tick: the cooldown is what makes the second
		// case mean "it happened again", not "the clock moved"
		if w.recentCase(dk, ip, 6*time.Hour) {
			continue
		}
		ev := []string{fmt.Sprintf("our own records: %d point(s) of evidence against %s across %d host(s) in 6h (%s)",
			n, ip, len(hosts[ip]), why[ip])}
		if egress && mine[ip] {
			ev = append(ev, fmt.Sprintf("the address is inside our own network (AS%d): a customer, per the flow exporter", dk.ASN))
		}
		c := w.openOrMerge(dk, ip, dk.DeviceID, why[ip], ev, dk.SLA)
		c.History = append(c.History, w.Sim.Format("15:04")+" opened from our own flow records, no report needed")
		if d := w.DeskDevice(dk); d != nil {
			d.Logf("notice", "abuse", "opened %s on our own initiative: %s (%d rejected attempts in 6h)", c.ID, ip, n)
		}
	}
}

// openCaseAbout reports whether this desk already has an open case about an
// address, so one scanner does not become one case per tick. A *closed* case
// does not block a new one: a repeat offence is a new ticket, and that is what
// makes "the second time" mean something.
func (w *World) openCaseAbout(dk *Desk, ip string) bool {
	for _, c := range w.Cases(dk.DeviceID) {
		if c.SubjectIP != ip {
			continue
		}
		if !caseTerminal(c.Stage) {
			return true // one open case per address; reports merge into it
		}
	}
	return false
}

// recentCase reports whether this desk touched any case about an address
// inside the window. Self-initiated work waits for it; a human complaint never
// does, because a person who files a report is entitled to a ticket.
func (w *World) recentCase(dk *Desk, ip string, window time.Duration) bool {
	for _, c := range w.Cases(dk.DeviceID) {
		if c.SubjectIP == ip && w.Sim.Sub(c.Updated) < window {
			return true
		}
	}
	return false
}

// advanceCase moves one case one step, if its own deadline has come.
func (w *World) advanceCase(dk *Desk, c *AbuseCase) {
	d := w.DeskDevice(dk)
	switch c.Stage {
	case StageFiled:
		// the desk works with what it has: its own records, plus whatever the
		// reporter could show it. Neither means no case.
		found := w.deskFindings(dk, c.SubjectIP)
		c.Findings = append(c.Findings, found...)
		if len(found) == 0 && len(c.Evidence) == 0 {
			w.caseStage(c, StageRejected, "no records of this address in our network, and the report carried no evidence")
			return
		}
		w.caseStage(c, StageTriaged, fmt.Sprintf("triaged: %d finding(s) in our own records, %d item(s) from the reporter",
			len(found), len(c.Evidence)))
		c.Due = w.Sim.Add(time.Duration(dk.SLA) * time.Minute)

	case StageTriaged:
		if w.Sim.Before(c.Due) {
			return
		}
		asn := w.attributionASN(c.SubjectIP)
		if asn == 0 {
			w.caseStage(c, StageClosed, "no network announces this address; there is nobody to act")
			return
		}
		if asn == dk.ASN {
			w.notifySubject(dk, c)
			return
		}
		other := w.DeskForASN(asn)
		if other == nil || other.DeviceID == dk.DeviceID {
			w.caseStage(c, StageClosed, fmt.Sprintf("AS%d announces this address and its desk is not reachable", asn))
			return
		}
		w.referCase(dk, c, other)

	case StageNotified:
		if w.Sim.Before(c.Due) {
			return
		}
		if w.subjectQuiet(c) {
			w.caseStage(c, StageClosed, "the traffic stopped after the notice, within the deadline")
			if d != nil {
				d.Logf("info", "abuse", "%s closed: %s stopped after the notice", c.ID, c.SubjectIP)
			}
			return
		}
		switch dk.Kind {
		case DeskAbuse, DeskDatacenter:
			// enforcement is only possible against a machine this network
			// answers for. A carrier-grade NAT has no such machine: the
			// provider cannot identify or cut off one subscriber behind it, so
			// the matter goes up the ladder instead of being dropped.
			if c.SubjectDev == "" {
				c.SubjectDev, c.Account = w.subjectDevice(c.SubjectIP)
			}
			if c.SubjectDev == "" && w.SharedAddress(c.SubjectIP) {
				w.escalate(dk, c, "the address is carrier-grade NAT: this network cannot identify or suspend one subscriber behind it")
				return
			}
			w.suspendSubject(dk, c)
		case DeskISP:
			// an ISP does not cut a household's line off a single report, and
			// it does not send the police either: the first case is a warning
			// on the subscriber's record, and the repeat is what escalates
			if w.priorCases(dk, c) > 0 {
				w.escalate(dk, c, "a second case about this address, after a formal warning")
			} else {
				c.Action = "formal warning recorded"
				w.caseStage(c, StageClosed, "notice issued and a formal warning recorded against the subscriber; a repeat escalates")
				if d := w.DeskDevice(dk); d != nil {
					d.Logf("notice", "abuse", "%s closed with a warning: %s", c.ID, c.SubjectIP)
				}
			}
		default:
			w.escalate(dk, c, "the conduct continued past the deadline and this network does not suspend a line on a complaint")
		}

	case StageSuspended:
		if w.Sim.Before(c.Due) && c.Reply == "" {
			return
		}
		w.restoreSubject(dk, c)

	case StageEscalated:
		if w.Sim.Before(c.Due) {
			return
		}
		le := w.lawDesk()
		if le == nil || !w.DeskOperational(le) {
			w.caseStage(c, StageClosed, "law enforcement's intake is not reachable; the case stays open in name only")
			return
		}
		w.openLawCase(le, c)

	case StageLERequest:
		if w.Sim.Before(c.Due) {
			return
		}
		// the provider answers, or explains why it cannot: this is the step
		// where an address becomes a subscriber record, and the step where a
		// shared address becomes a dead end
		sub := w.subscriberRecord(c.SubjectIP)
		if sub == "" {
			// the honest end of the road, and the reason §13 draws the
			// distinction in the first place: a shared address belongs to
			// many subscribers, so there is nobody to name
			if w.SharedAddress(c.SubjectIP) {
				c.History = append(c.History, fmt.Sprintf("%s the network answered: carrier-grade NAT, no single subscriber", w.Sim.Format("15:04")))
				w.caseStage(c, StageLEClosed, "the address is carrier-grade NAT: the provider cannot name one subscriber")
				return
			}
			w.caseStage(c, StageLEClosed, "the network answered: no subscriber record for this address in the retention window")
			return
		}
		c.Account = sub
		c.History = append(c.History, fmt.Sprintf("%s provider disclosed subscriber %q under lawful request", w.Sim.Format("15:04"), sub))
		w.caseStage(c, StageLEDisclose, "subscriber record obtained under lawful request")

	case StageLEDisclose:
		if w.Sim.Before(c.Due) {
			return
		}
		w.lawOutcome(dk, c)
	}
}

// ---- the steps -------------------------------------------------------------

// notifySubject is the first response: the organisation tells whoever holds the
// address what it saw and what has to stop. Delivery is attempted for real
// through the world's own MTA, and a notice that cannot be delivered is
// recorded as such — a provider that claims to have emailed a residential line
// would be lying about its own records.
func (w *World) notifySubject(dk *Desk, c *AbuseCase) {
	dev, account := w.subjectDevice(c.SubjectIP)
	c.SubjectDev = dev
	c.Account = account
	who := account
	if who == "" {
		who = "the holder of " + c.SubjectIP
	}
	to := ""
	if dev != "" {
		if d := w.Devices[dev]; d != nil {
			c.SubjectHost = d.Hostname
			if d.Owner != "" {
				to = d.Owner + "@" + d.Hostname
			}
		}
	}
	body := fmt.Sprintf("This is an automated notice from %s's abuse desk.\n\nCase:     %s\nAddress:  %s\nReported: %s\n\nWhat our records show:\n  %s\n\nWhat the reporter showed us:\n  %s\n\nPlease stop the traffic and tell us what you did, quoting the case number.\nYou have %d minutes before this case is escalated under our terms of service.\n\n%s\n",
		dk.Org, c.ID, c.SubjectIP, c.Kind, listOrNone(c.Findings), listOrNone(c.Evidence), dk.Notice, dk.Org)
	delivery := "no address on file, notice posted to the case portal only"
	if to != "" {
		res := w.RouteMail(w.DeskDevice(dk), dk.Mailbox, to, "["+c.ID+"] abuse notice for "+c.SubjectIP, body)
		switch {
		case res.Delivered:
			delivery = "mailed to " + to + " (" + res.Via + ")"
		case res.Queued:
			delivery = "queued for " + to + ": " + res.Diagnostic
		default:
			delivery = "not deliverable to " + to + ": " + res.Diagnostic
		}
	}
	// a provider mails the account contact, not the server: the customer's
	// registered address is where a real notice lands, and it is often the
	// only way a household ever learns its line was reported
	if account != "" {
		if pc := w.Devices[w.PlayerDeviceID(account)]; pc != nil {
			if err := w.DeliverLocal(pc, dk.Mailbox+"@"+hostnameOf(w, dk), account,
				"["+c.ID+"] abuse notice for "+c.SubjectIP, body); err == nil {
				delivery += "; copy to the account's registered mailbox"
			}
		}
	} else if dev != "" {
		if d := w.Devices[dev]; d != nil && d.Owner == "" {
			delivery += "; no account holder on file for " + d.Hostname
		}
	}
	c.Notices = append(c.Notices, fmt.Sprintf("%s %s — %s", w.Sim.Format("15:04"), c.ID, delivery))
	w.caseStage(c, StageNotified, "notice issued to "+who+" ("+delivery+")")
	c.Due = w.Sim.Add(time.Duration(dk.Notice) * time.Minute)
	if d := w.DeskDevice(dk); d != nil {
		d.Logf("notice", "abuse", "%s notified %s about %s: deadline %s", c.ID, who, c.SubjectIP, c.Due.Format("15:04"))
	}
	w.AddEvent(dk.DeviceID, "notice", "abuse", "%s notified %s about %s (%s)", dk.Org, who, c.SubjectIP, c.ID)
}

// referCase hands a case to the network that really answers for the address.
// The referral is mail; the desk that receives it opens its own case with the
// evidence attached, because an organisation's records are its own.
func (w *World) referCase(from *Desk, c *AbuseCase, to *Desk) {
	c.ReferredTo = to.DeviceID
	body := fmt.Sprintf("Referral from %s.\n\nCase:     %s\nAddress:  %s\nThe address is announced by AS%d (%s), which is your network, not ours.\n\nOur records:\n%s\nThe reporter's evidence:\n%s\n\nThe traffic continues. Please handle it under your own policy.\n",
		from.Org, c.ID, c.SubjectIP, to.ASN, to.Org, listOrNone(c.Findings), listOrNone(c.Evidence))
	res := w.RouteMail(w.DeskDevice(from), from.Mailbox, to.Mailbox+"@"+hostnameOf(w, to), "referral "+c.ID+" — "+c.SubjectIP, body)
	delivery := "mailed to " + to.Mailbox + "@" + hostnameOf(w, to)
	if !res.Delivered {
		delivery = "could not be delivered to " + to.Org + ": " + res.Diagnostic
	}
	w.caseStage(c, StageReferred, "handed to "+to.Org+" ("+delivery+")")
	if res.Delivered {
		// the receiving desk opens its own case: same address, its own file
		ev := append([]string{}, c.Findings...)
		ev = append(ev, c.Evidence...)
		nc := w.openOrMerge(to, c.SubjectIP, from.DeviceID, c.Kind, ev, to.SLA)
		nc.History = append(nc.History, fmt.Sprintf("%s referral from %s (%s) attached", w.Sim.Format("15:04"), from.Org, c.ID))
		if d := w.DeskDevice(to); d != nil {
			d.Logf("notice", "abuse", "case %s: referral from %s about %s", nc.ID, from.Org, c.SubjectIP)
		}
	}
}

// suspendSubject is the provider enforcing its own terms: the machine that is
// doing the abusing really stops answering. This is the consequence the whole
// §33/§34 loop hangs on — a ban is a filter, a suspension is a port pulled by
// the people who own the rack.
func (w *World) suspendSubject(dk *Desk, c *AbuseCase) {
	d := w.Devices[c.SubjectDev]
	if d == nil {
		w.caseStage(c, StageClosed, "the address no longer maps to a machine in this network")
		return
	}
	reason := fmt.Sprintf("%s: abuse case %s not resolved", dk.Org, c.ID)
	d.setPowered(false, reason)
	d.Logf("warn", "abuse", "port suspended by %s (%s)", dk.Org, c.ID)
	c.Action = "port suspended"
	w.caseStage(c, StageSuspended, "port suspended: "+d.Hostname+" is offline")
	c.Due = w.Sim.Add(suspensionTerm)
	if dd := w.DeskDevice(dk); dd != nil {
		dd.Logf("warn", "abuse", "%s: suspended %s (%s) after the deadline", c.ID, d.Hostname, c.SubjectIP)
	}
	w.AddEvent(d.ID, "warn", "abuse", "%s suspended %s after %s", dk.Org, d.Hostname, c.ID)
	w.News = append(w.News, fmt.Sprintf("%s suspended a customer machine (%s) after an unresolved abuse case", dk.Org, d.Hostname))
	if d.Owner != "" {
		if pc := w.Devices[w.PlayerDeviceID(d.Owner)]; pc != nil {
			w.DeliverLocal(pc, "abuse@"+hostnameOf(w, dk), d.Owner, "your service was suspended ("+c.ID+")",
				fmt.Sprintf("Case %s is still open and the reported traffic continued past the deadline.\nThe port on %s has been suspended under the terms of service.\nReply to the desk to have it restored.\n", c.ID, d.Hostname))
		}
	}
}

// restoreSubject puts a suspended machine back on the network: because the
// term lapsed, or because the customer answered the notice. Both are real
// reasons a provider restores a port, and both are read from state rather than
// assumed.
func (w *World) restoreSubject(dk *Desk, c *AbuseCase) {
	d := w.Devices[c.SubjectDev]
	if d == nil {
		w.caseStage(c, StageClosed, "the machine is no longer in this network")
		return
	}
	why := "the suspension term lapsed"
	if c.Reply != "" {
		why = "the customer answered the notice"
	}
	d.setPowered(true, "")
	d.Logf("notice", "abuse", "port restored by %s (%s): %s", dk.Org, c.ID, why)
	c.Action = "port suspended, then restored"
	w.caseStage(c, StageClosed, "port restored on "+d.Hostname+" — "+why)
	if dd := w.DeskDevice(dk); dd != nil {
		dd.Logf("info", "abuse", "%s closed: restored %s (%s)", c.ID, d.Hostname, why)
	}
	w.AddEvent(d.ID, "info", "abuse", "%s restored %s: %s", dk.Org, d.Hostname, why)
}

// priorCases counts the finished cases this desk has already had about an
// address. It is the difference between a first complaint and a pattern, and it
// is read from the desk's own file rather than from a counter.
func (w *World) priorCases(dk *Desk, c *AbuseCase) int {
	n := 0
	for _, old := range w.Cases(dk.DeviceID) {
		if old.ID == c.ID || old.SubjectIP != c.SubjectIP {
			continue
		}
		if caseTerminal(old.Stage) {
			n++
		}
	}
	return n
}

// escalate hands a case to law enforcement: a network that will not suspend a
// line on a complaint says so, and puts the file where it belongs.
func (w *World) escalate(dk *Desk, c *AbuseCase, why string) {
	c.Law = true
	w.caseStage(c, StageEscalated, why)
	c.Due = w.Sim.Add(time.Duration(dk.SLA) * time.Minute)
	if d := w.DeskDevice(dk); d != nil {
		d.Logf("notice", "abuse", "%s escalated to law enforcement: %s", c.ID, why)
	}
}

// openLawCase is the police desk's own intake: a new file, its own number, its
// own deadlines, and only the material it was actually sent.
func (w *World) openLawCase(le *Desk, from *AbuseCase) {
	w.ensureAbuse()
	ev := append([]string{}, from.Findings...)
	ev = append(ev, from.Evidence...)
	ev = append(ev, fmt.Sprintf("referred by %s (case %s)", orgOf(w, from.DeskID), from.ID))
	nc := w.openCase(le, from.SubjectIP, from.DeskID, from.Kind, ev, le.SLA)
	nc.Law = true
	nc.History = append(nc.History, fmt.Sprintf("%s case opened from %s's referral %s", w.Sim.Format("15:04"), orgOf(w, from.DeskID), from.ID))
	w.caseStage(nc, StageLERequest, "lawful request for subscriber records sent to the network that holds the address")
	w.caseStage(from, StageLEOpened, "law enforcement opened a file ("+nc.ID+")")
	if d := w.DeskDevice(le); d != nil {
		d.Logf("notice", "cases", "%s: lawful request for subscriber records for %s", nc.ID, nc.SubjectIP)
	}
	if isp := w.DeskFor(nc.SubjectIP); isp != nil {
		if dd := w.DeskDevice(isp); dd != nil {
			dd.Logf("notice", "compliance", "%s: lawful request from %s received; subscriber records under review", nc.ID, le.Org)
		}
	}
}

// lawOutcome is what the file can actually support. A world where every report
// ends in a raid would be as dishonest as one where nothing has consequences:
// a unit acts on what the file shows, and says so when it is thin.
func (w *World) lawOutcome(le *Desk, c *AbuseCase) {
	support := len(c.Findings) + len(c.Evidence) + len(c.Account)
	if support < 3 {
		w.caseStage(c, StageLEClosed, "no further action: the file does not support a warrant")
		return
	}
	if c.SubjectDev != "" {
		if d := w.Devices[c.SubjectDev]; d != nil && d.Owner != "" {
			if pc := w.Devices[w.PlayerDeviceID(d.Owner)]; pc != nil {
				w.DeliverLocal(pc, "cases@"+hostnameOf(w, le), d.Owner, "investigation "+c.ID,
					fmt.Sprintf("%s has opened an investigation (%s) into conduct attributed to this household's connection.\nNothing has been seized. If you have something to say about it, answer this message and quote the case number.\n", le.Org, c.ID))
			}
			w.AddEvent(le.DeviceID, "warn", "cases", "%s opened an investigation into %s (%s)", le.Org, d.Owner, c.ID)
			w.News = append(w.News, fmt.Sprintf("%s opened an investigation into conduct from %s", le.Org, c.SubjectIP))
		}
	}
	w.caseStage(c, StageLEClosed, "subscriber identified; matter recorded and closed for now")
}

// ---- helpers ---------------------------------------------------------------

// caseStage moves a case and writes the line that says why. Every stage change
// in the world goes through here, so the case file is the audit trail.
func (w *World) caseStage(c *AbuseCase, stage, why string) {
	if c == nil {
		return
	}
	c.Stage = stage
	c.Updated = w.Sim
	c.History = append(c.History, fmt.Sprintf("%s %s — %s", w.Sim.Format("15:04"), stage, why))
	if len(c.History) > 60 {
		c.History = c.History[len(c.History)-60:]
	}
}

// subjectQuiet reports whether the reported traffic really stopped: no rejected
// attempts from the subject since the notice went out. This is the evidence a
// provider closes a case on, and it is read from the same flow record everyone
// else reads.
func (w *World) subjectQuiet(c *AbuseCase) bool {
	since := w.Sim.Add(-1 * time.Duration(noticeWindowMinutes) * time.Minute)
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !d.Powered() {
			continue
		}
		for i, fip := range d.Sec().FailIP {
			if fip == c.SubjectIP && i < len(d.Sec().Fails) {
				for _, at := range d.Sec().Fails[i : i+1] {
					if !at.Before(since) {
						return false
					}
				}
			}
		}
		for _, f := range d.Sec().Flows {
			if f.Src != c.SubjectIP || f.At.Before(since) {
				continue
			}
			switch f.Verdict {
			case FlowFiltered, FlowRefused, FlowBanned:
				return false
			}
		}
	}
	return true
}

// noticeWindowMinutes is how far back "since the notice" looks when a desk
// decides whether the traffic stopped. A real desk reads its own window.
const noticeWindowMinutes = 180

// deskReadInbox is how a subject answers: they mail the desk, the desk reads
// its own mailbox, and the answer lands in the case file. No new plumbing — a
// reply is a real message in a real mailbox.
func (w *World) deskReadInbox(dk *Desk) {
	d := w.DeskDevice(dk)
	if d == nil {
		return
	}
	data, ok := d.FS.Read(MailboxPath(dk.Mailbox))
	if !ok || len(data) == 0 {
		return
	}
	text := string(data)
	for _, c := range w.Cases(dk.DeviceID) {
		if c.Reply != "" || caseTerminal(c.Stage) {
			continue
		}
		idx := strings.Index(text, c.ID)
		if idx < 0 {
			continue
		}
		tail := text[idx:]
		if end := strings.Index(tail, "\n\n"); end > 0 {
			tail = tail[:end]
		}
		line := strings.TrimSpace(strings.ReplaceAll(tail, "\n", " "))
		if line == "" {
			continue
		}
		if len(line) > 240 {
			line = line[:240] + "…"
		}
		c.Reply = line
		c.History = append(c.History, fmt.Sprintf("%s answer from the subject received by mail", w.Sim.Format("15:04")))
		d.Logf("info", "abuse", "%s: subject answered", c.ID)
	}
}

// attributionASN is the one step a lookup honestly performs: which network
// announces this address.
func (w *World) attributionASN(ip string) int {
	if info := ClassifyAddr(ip); info.Kind == KindShared {
		return asNova
	}
	if w.WAN == nil {
		return 0
	}
	if IsV6(ip) {
		if as := w.WAN.ASFor6(ip); as != nil {
			return as.ASN
		}
		return 0
	}
	if as := w.WAN.ASFor(ip); as != nil {
		return as.ASN
	}
	return 0
}

// subscriberRecord is the provider's answer to a lawful request: the account
// that held the address. A carrier-grade NAT address has no single subscriber
// and returns nothing, which is the honest end of that road.
func (w *World) subscriberRecord(ip string) string {
	if w.SharedAddress(ip) {
		return ""
	}
	dev, account := w.subjectDevice(ip)
	if dev == "" {
		return ""
	}
	if account != "" {
		return account
	}
	// a machine in a provider's hall with no account on file: the provider can
	// name the rack, and that is all it can name
	return "unassigned (" + w.Devices[dev].Hostname + ")"
}

// subjectDevice resolves an address to the machine that holds it and the
// account on file for it.
func (w *World) subjectDevice(ip string) (devID, account string) {
	id, ok := w.IPMap[ip]
	if !ok {
		return "", ""
	}
	d := w.Devices[id]
	if d == nil {
		return "", ""
	}
	return id, d.Owner
}

// lawDesk finds the law enforcement organisation, if the world has one.
func (w *World) lawDesk() *Desk {
	for _, dk := range w.Desks {
		if dk.Kind == DeskLaw {
			return dk
		}
	}
	return nil
}

func orgOf(w *World, deskID string) string {
	for _, dk := range w.Desks {
		if dk.DeviceID == deskID {
			return dk.Org
		}
	}
	if d := w.Devices[deskID]; d != nil {
		return d.Hostname
	}
	return deskID
}

func hostnameOf(w *World, dk *Desk) string {
	if d := w.DeskDevice(dk); d != nil {
		return d.Hostname
	}
	return dk.DeviceID
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "  (nothing)\n"
	}
	var b strings.Builder
	for _, s := range items {
		b.WriteString("  - " + s + "\n")
	}
	return b.String()
}

// ---- support for tests and the live verify scripts -------------------------
//
// These are the same reads the tools use, exported so a test can drive a case
// without reaching into unexported state. They change nothing by themselves.

// OpenCaseForTest files a case at a desk directly, the way a report does when
// it arrives over the wire. It is what the sweep scripts use to work the ladder
// without a full client.
func (w *World) OpenCaseForTest(deskID, ip, reporter, kind string, evidence []string) *AbuseCase {
	dk := w.deskByID(deskID)
	if dk == nil {
		return nil
	}
	return w.openCase(dk, ip, reporter, kind, evidence, dk.SLA)
}

// SubscriberRecordForTest exposes the provider's answer to a lawful request.
func (w *World) SubscriberRecordForTest(ip string) string { return w.subscriberRecord(ip) }

// LawCases counts the files that reached law enforcement, open or closed.
func (w *World) LawCases() int {
	n := 0
	for _, c := range w.Cases("") {
		if c.Law || strings.HasPrefix(c.Stage, "law:") {
			n++
		}
	}
	return n
}

// ---- the staff console -----------------------------------------------------

// OnDeskStaff is the permission model for the console: the account must be in
// the organisation's own group on the organisation's own machine. There is no
// remote admin socket and no magic role — a security team member is a Unix
// account in a group, exactly as in the real thing.
func (w *World) OnDeskStaff(d *Device, u *User, deskID string) bool {
	dk := w.deskByID(deskID)
	if dk == nil || d == nil || u == nil || d.ID != dk.DeviceID {
		return false
	}
	if u.UID == 0 {
		return true
	}
	for _, g := range u.Groups {
		if g == dk.Staff {
			return true
		}
	}
	return false
}

// deskByID finds a desk by its device id.
func (w *World) deskByID(id string) *Desk {
	for _, dk := range w.Desks {
		if dk.DeviceID == id {
			return dk
		}
	}
	return nil
}

// Desk is exported for callers that need the organisation rather than the case:
// the shell prints the desk's own summary.
func (w *World) Desk(id string) *Desk { return w.deskByID(id) }

// CasesHandledBy counts the cases a named operator actually moved. It is the
// verifier behind the desk-shift work: the world checks who did the work, not
// that a job was accepted.
func (w *World) CasesHandledBy(name string) int {
	if name == "" || w.Abuse == nil {
		return 0
	}
	n := 0
	for _, c := range w.Abuse.List {
		if c.TriagedBy == name {
			n++
			continue
		}
		for _, h := range c.History {
			if strings.Contains(h, "operator "+name+":") {
				n++
				break
			}
		}
	}
	return n
}

// CasesWorkedAt counts the cases at a desk that carry a human operator's mark,
// whoever the operator was. A shift is covered by the account that worked it,
// and a player covering a shift signs in as that account rather than as
// themselves; this is how the world sees the shift rather than the name.
func (w *World) CasesWorkedAt(deskID string) int {
	if w.Abuse == nil {
		return 0
	}
	n := 0
	for _, c := range w.Abuse.List {
		if c.DeskID != deskID {
			continue
		}
		if c.TriagedBy != "" {
			n++
			continue
		}
		for _, h := range c.History {
			if strings.Contains(h, " operator ") {
				n++
				break
			}
		}
	}
	return n
}

// CasesAbout counts the reports a person filed about an address. The
// victim-side verifier reads it: filing is only real if a case exists.
func (w *World) CasesAbout(reporter, ip string) int {
	if w.Abuse == nil {
		return 0
	}
	n := 0
	for _, c := range w.Abuse.List {
		if c.SubjectIP != ip {
			continue
		}
		if c.Reporter == reporter {
			n++
			continue
		}
		for _, r := range c.AlsoReported {
			if r == reporter {
				n++
				break
			}
		}
	}
	return n
}

// DeskQueue is the console's work list: everything not finished.
func (w *World) DeskQueue(deskID string) []*AbuseCase {
	var out []*AbuseCase
	for _, c := range w.Cases(deskID) {
		if !caseTerminal(c.Stage) {
			out = append(out, c)
		}
	}
	return out
}

// DeskTriage is a human doing the desk's first step deliberately, with a note:
// the same transition the tick performs, but recorded as the operator's
// decision. It is what the desk-shift job is checked against.
func (w *World) DeskTriage(caseID, actor, note string) (*AbuseCase, error) {
	c := w.CaseByID(caseID)
	if c == nil {
		return nil, fmt.Errorf("no such case: %s", caseID)
	}
	if c.Stage != StageFiled {
		return nil, fmt.Errorf("%s is in stage %s, not %s", c.ID, c.Stage, StageFiled)
	}
	dk := w.deskByID(c.DeskID)
	if dk == nil {
		return nil, fmt.Errorf("no desk holds %s", c.ID)
	}
	if note == "" {
		return nil, fmt.Errorf("a triage decision needs a note explaining it")
	}
	c.Findings = append(c.Findings, w.deskFindings(dk, c.SubjectIP)...)
	c.TriagedBy = actor
	w.caseStage(c, StageTriaged, "triaged by "+actor+": "+note)
	c.Due = w.Sim.Add(time.Duration(dk.SLA) * time.Minute)
	if d := w.DeskDevice(dk); d != nil {
		d.Logf("info", "abuse", "%s triaged by %s: %s", c.ID, actor, note)
	}
	return c, nil
}

// DeskAct is the operator's own hand on a case: notify, refer, suspend, close.
// Each action performs the real work the tick would have performed later; it is
// the difference between watching the ladder and running it.
func (w *World) DeskAct(caseID, actor, action, note string) (*AbuseCase, error) {
	c := w.CaseByID(caseID)
	if c == nil {
		return nil, fmt.Errorf("no such case: %s", caseID)
	}
	if caseTerminal(c.Stage) {
		return nil, fmt.Errorf("%s is already %s", c.ID, c.Stage)
	}
	dk := w.deskByID(c.DeskID)
	if dk == nil {
		return nil, fmt.Errorf("no desk holds %s", c.ID)
	}
	switch action {
	case "notify":
		if c.Stage != StageTriaged {
			return nil, fmt.Errorf("%s must be triaged before a notice goes out (it is %s)", c.ID, c.Stage)
		}
		w.notifySubject(dk, c)
	case "refer":
		if c.Stage != StageTriaged {
			return nil, fmt.Errorf("%s must be triaged before it can be referred (it is %s)", c.ID, c.Stage)
		}
		other := w.DeskForASN(w.attributionASN(c.SubjectIP))
		if other == nil {
			return nil, fmt.Errorf("no organisation answers for %s", c.SubjectIP)
		}
		w.referCase(dk, c, other)
	case "suspend":
		if c.SubjectDev == "" {
			w.subjectDevice(c.SubjectIP)
			c.SubjectDev, c.Account = w.subjectDevice(c.SubjectIP)
		}
		if c.SubjectDev == "" {
			return nil, fmt.Errorf("%s does not map to a machine in this network", c.SubjectIP)
		}
		w.suspendSubject(dk, c)
	case "escalate":
		w.escalate(dk, c, "escalated by "+actor)
	case "close":
		w.caseStage(c, StageClosed, "closed by "+actor+": "+note)
		c.Action = "none"
	default:
		return nil, fmt.Errorf("unknown action %q (notify, refer, suspend, escalate, close)", action)
	}
	c.History = append(c.History, fmt.Sprintf("%s operator %s: %s — %s", w.Sim.Format("15:04"), actor, action, note))
	if d := w.DeskDevice(dk); d != nil {
		d.Logf("info", "abuse", "%s %s by %s", c.ID, action, actor)
	}
	return c, nil
}

// DeskSummary is the public roll-up a desk publishes about itself — the numbers
// an organisation actually knows about its own work.
func (w *World) DeskSummary(deskID string) []string {
	var out []string
	open, closed, suspended, law := 0, 0, 0, 0
	for _, c := range w.Cases(deskID) {
		switch c.Stage {
		case StageSuspended:
			suspended++
			closed++
		case StageClosed, StageRejected:
			closed++
		case StageLEOpened, StageLEClosed:
			law++
			closed++
		default:
			open++
		}
	}
	out = append(out, fmt.Sprintf("%d open, %d closed, %d suspended, %d with law enforcement", open, closed, suspended, law))
	return out
}
