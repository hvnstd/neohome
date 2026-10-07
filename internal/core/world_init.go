package core

import (
	"strings"
	"time"
)

// Fault describes the currently planted causal problem (debuggable, fixable).
type Fault struct {
	Active     bool
	DeviceID   string
	Story      string
	ExplainFix string
}

// NewWorld builds the opening scene: one household (PC, Router, NAS,
// Assistant node), an ISP chain, a DNS namespace, a mirror, a VPS provider,
// a bank, a job board, a chat server and one NPC neighbour — plus the
// scripted-but-causal fault the first player must debug.
func NewWorld() *World {
	w := &World{
		Started:  time.Now(),
		Sim:      time.Now(),
		Devices:  map[string]*Device{},
		IPMap:    map[string]string{},
		Players:  map[string]*Player{},
		Repos:    map[string]*Repo{},
		Chat:     &Chat{Channels: map[string]bool{"#local": true, "#help": true}},
		Bank:     &Bank{Accts: map[string]*Account{}},
		Jobs:     &Jobs{},
		NPCNames: []string{"mara", "devops", "mira-9", "daemon42", "olduser", "sysmods"},
		Case:     &Case{},
		Power:    1.0,
		Heating:  false,
	}

	// ---- infra backbone ----
	core := w.addDevice("core-gw", "core-gw", "core", "", OSInfo{"NeoCore", "1.0", "6.6.0", "x86_64", "ash"},
		Hardware{"Carrier Router", 4, 2000, 1024, 8192, 1000, false, false}, "10.0.0.1")

	ispDNS := w.addDevice("isp-dns", "dns.isp.example", "infra", "", OSInfo{"Alpine", "3.20", "6.6.20", "x86_64", "ash"},
		Hardware{"VM", 1, 1000, 512, 4096, 100, false, false}, "10.0.0.2")
	ns1 := w.addDevice("ns-a", "ns-a.neohome.example", "infra", "", OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"},
		Hardware{"VM", 1, 1000, 512, 8192, 100, false, false}, "10.0.0.3")
	mirror := w.addDevice("mirror", "mirror.neohome.example", "infra", "", OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"},
		Hardware{"VM", 2, 2000, 2048, 204800, 1000, false, false}, "10.0.0.4")
	provider := w.addDevice("prov-api", "api.novapanel.example", "infra", "", OSInfo{"Ubuntu", "24.04", "6.8.0", "x86_64", "bash"},
		Hardware{"VM", 1, 1000, 1024, 20480, 100, false, false}, "10.0.0.5")
	ircd := w.addDevice("irc", "irc.neohome.example", "infra", "", OSInfo{"Alpine", "3.20", "6.6.20", "x86_64", "ash"},
		Hardware{"VM", 1, 1000, 512, 4096, 100, false, false}, "10.0.0.6")
	bankd := w.addDevice("bank", "bank.firstneohome.example", "infra", "", OSInfo{"RHEL", "9", "5.14.0", "x86_64", "bash"},
		Hardware{"VM", 2, 2000, 2048, 40960, 1000, false, false}, "10.0.0.7")
	jobsd := w.addDevice("jobs", "jobs.hiring.example", "infra", "", OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"},
		Hardware{"VM", 1, 1000, 1024, 8192, 100, false, false}, "10.0.0.8")
	// the community board (WS-0.7): a hobby box on the public internet, the
	// way every neighbourhood had one
	bbsd := w.addDevice("bbs", "bbs.neohome.example", "infra", "", OSInfo{"Alpine", "3.20", "6.6.20", "x86_64", "ash"},
		Hardware{"VM", 1, 800, 768, 20480, 100, false, false}, "10.0.0.9")
	// the git server (WS-0.8): repositories with real history over https
	gitd := w.addDevice("git", "git.neohome.example", "infra", "", OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"},
		Hardware{"VM", 1, 1000, 1024, 20480, 100, false, false}, "10.0.0.10")
	// the official package archive (WS-1.6): the origin every mirror in this
	// world syncs from, with its own address, service and logs
	archive := w.addDevice("archive", "archive.neohome.example", "infra", "", OSInfo{"Debian", "13", "6.12.5", "x86_64", "bash"},
		Hardware{"VM", 4, 2400, 8192, 512000, 2000, false, false}, "10.0.0.11")
	// a third-party CDN that publishes its own tree with its own key (§11)
	cdn := w.addDevice("cdn", "cdn.sashimi-cdn.example", "peer", "", OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"VM", 2, 2000, 4096, 102400, 1000, false, false}, "10.0.0.12")
	_ = core

	for _, d := range []*Device{ispDNS, ns1, mirror, provider, ircd, bankd, jobsd, bbsd, gitd, archive, cdn} {
		d.Ifaces = append(d.Ifaces, &Iface{Name: "eth1", IP: w.allocPublicFor(d.Profile), MAC: macFor(d.ID + "-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	}
	pubISP := wanIP(ispDNS)
	pubNS := wanIP(ns1)
	pubMirror := wanIP(mirror)
	pubProv := wanIP(provider)
	pubIRC := wanIP(ircd)
	pubBank := wanIP(bankd)
	pubJobs := wanIP(jobsd)
	pubBBS := wanIP(bbsd)
	pubGit := wanIP(gitd)
	for _, d := range []*Device{ispDNS, ns1, mirror, provider, ircd, bankd, jobsd, bbsd, gitd, archive, cdn} {
		w.IPMap[d.Ifaces[1].IP] = d.ID
	}

	// ---- the household ----
	pc := w.addDevice("pc-alex", "home-pc", "pc", "alex", OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Generic Desktop", 4, 3200, 8192, 65536, 1000, false, false}, lanIP(11))
	router := w.addDevice("router-alex", "gateway", "router", "alex", OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"},
		Hardware{"Archer C7 (stock)", 1, 800, 128, 16, 1000, true, false}, LANGateway)
	nas := w.addDevice("nas-alex", "nas", "nas", "alex", OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"NAS Box", 2, 1600, 2048, 409600, 1000, false, false}, lanIP(30))
	asst := w.addDevice("asst-alex", "assistant", "pc", "alex", OSInfo{"Alpine", "3.20", "6.6.20", "x86_64", "ash"},
		Hardware{"Assistant Mini-PC", 2, 2000, 2048, 32768, 1000, false, false}, lanIP(20))
	asst.Notes = "assistant node"
	// the player reaches the assistant's workspace over the same ssh the rest
	// of the world uses: key-only, because the node trusts the owner's key
	SeedAssistantAccess(w, asst)

	// ---- the management controller -------------------------------------------
	// A BMC-class box wired to the mains before the breaker, on its own battery
	// and cellular backhaul. This is what makes `power cut` a playable move
	// instead of an unrecoverable one: a real operator can always get back in
	// through out-of-band management. It is also the machine that a determined
	// attacker would love to own, which is the point.
	bmc := w.addDevice("bmc-alex", "bmc", "bmc", "alex", OSInfo{"OpenBMC", "2.14", "6.6.7", "arm", "ash"},
		Hardware{"NeoBMC", 1, 1200, 512, 4096, 100, false, true}, lanIP(250))
	bmc.UPS = &UPSInfo{ChargePct: 100, LastState: "online"}
	bmc.Notes = "out-of-band management controller"
	mkUsers(bmc, map[string]*User{
		"root":  {Name: "root", UID: 0, Pass: "alex123", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
		"admin": {Name: "admin", UID: 1000, Pass: "alex123", Groups: []string{"admin"}, Home: "/home/admin", Shell: "/bin/ash"},
	})
	bmc.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH (BMC)", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	bmc.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp",
		Scope: "lan", State: "running", Handler: "syslog"}

	// every household client that reaches the internet does it through the
	// router, so each of them has a default route and a DHCP lease there. The
	// laptop belongs on this list as much as the desktop does: without it, a
	// laptop on the same LAN could not fetch a package, which is not a world
	// fact, it is an omission (and §33 needs it: freshclam updates from the
	// mirror like everything else).
	for _, d := range []*Device{pc, nas, asst} {
		if len(d.Ifaces) > 0 {
			d.Ifaces[0].GW = LANGateway
			d.Ifaces[0].Mode = "dhcp"
		}
	}

	// ---- the mail transfer agent (WS-0.2) -----------------------------------
	// A real MTA on the player's own machine, not a world-level magic: the port
	// only answers because a daemon is running, `systemctl stop smtpd` really
	// closes it, and it comes back with `start`. Installed on the PC and the
	// assistant node — the NAS is a storage box, not a mail host.
	for _, d := range []*Device{pc, asst} {
		d.Services["smtpd"] = &Service{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp",
			Scope: "lan", State: "running", Handler: "smtpd",
			Banner: "220 neohome ESMTP smtpd ready", Conf: "/etc/mail/smtpd.conf"}
		// The reading half of the mail system (WS-0.6): a mailbox on this
		// host is reachable from any other machine over IMAP, gated by the
		// account's real credentials.
		d.Services["imapd"] = &Service{Name: "imapd", Desc: "IMAP mailbox access", Port: 143, Proto: "tcp",
			Scope: "lan", State: "running", Handler: "imapd",
			Banner: "* OK IMAP4rev1 neohome imapd ready", Conf: "/etc/mail/imapd.conf"}
		d.FS.MkdirAll("/etc/mail", 0755, "root", "root")
		d.FS.Write("/etc/mail/smtpd.conf",
			"listen on lo port 25\nlisten on eth0 port 25\n\naction \"local\" mbox\naction \"relay\" relay\n\nmatch from any for local\n",
			0644, "root", "root")
		d.FS.Write("/etc/mail/imapd.conf",
			"listen on lo port 143\nlisten on eth0 port 143\nmail_location = /var/mail/%u\n",
			0644, "root", "root")
	}
	router.Ifaces = append(router.Ifaces, &Iface{Name: "eth0.2", IP: w.allocPublicFor("router"), MAC: macFor("router-alex-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	pubHome := wanIP(router)
	w.IPMap[router.Ifaces[1].IP] = router.ID

	mkUsers(pc, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"alex": {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"alex", "sudo"}, Home: "/home/alex", Shell: "/bin/bash"},
		// An ordinary account with no sudo rights. Without one, the world has no
		// privilege boundary to test — and no believable place for an attacker to
		// land after guessing a weak password.
		"guest": {Name: "guest", UID: 1001, Pass: "guest", Groups: []string{"guest"}, Home: "/home/guest", Shell: "/bin/bash"},
	})
	mkUsers(router, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
	})
	mkUsers(nas, map[string]*User{
		"root":   {Name: "root", UID: 0, Pass: "nasroot", Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"alex":   {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"alex"}, Home: "/home/alex", Shell: "/bin/bash"},
		"nobody": {Name: "nobody", UID: 65534, Groups: []string{"nogroup"}, Home: "/nonexistent", Shell: "/usr/sbin/nologin"},
	})
	mkUsers(asst, map[string]*User{
		"root":      {Name: "root", UID: 0, Pass: "assistant", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
		"assistant": {Name: "assistant", UID: 1001, Pass: "assist-pass", Groups: []string{"assistant"}, Home: "/home/assistant", Shell: "/bin/ash", IsBot: true},
	})

	w.Players["alex"] = &Player{Name: "alex", Pass: "alex123", PC: pc.ID, Router: router.ID, NAS: nas.ID,
		Assistant: asst.ID, HouseKey: "house:alex", Created: time.Now()}

	// ---- the front-door IoT pair (WS-1.0) -----------------------------------
	// Spec §15/§41/§42: a camera that really records the household's events
	// onto the NAS, and a smart lock whose state is real. Both ship with the
	// classic weak default password, because IoT does.
	cam := w.addDevice("cam-alex", "cam-front", "iot", "alex", OSInfo{"NeoIoT", "3.1", "5.15.160", "arm", "ash"},
		Hardware{"PoE Door Cam", 1, 400, 128, 1024, 10, false, false}, lanIP(40))
	lokd := w.addDevice("lock-alex", "lock-front", "iot", "alex", OSInfo{"NeoIoT", "3.1", "5.15.160", "arm", "ash"},
		Hardware{"Smart Lock", 1, 120, 64, 512, 5, false, false}, lanIP(43))
	mkUsers(cam, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
	})
	mkUsers(lokd, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
	})
	seedFS(cam, "iot")
	seedFS(lokd, "iot")
	cam.Services["rtsp"] = &Service{Name: "rtsp", Desc: "camera stream and recordings", Port: 554, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "rtsp", Banner: "RTSP/1.0 200 OK", Conf: "/etc/rtsp.conf"}
	cam.PoEPowered = true // it has no plug of its own: the switch feeds it
	cam.PoeUp = true

	// ---- §15 家庭设备: the switch, the laptop and the printer --------------
	//
	// The spec's own example is a chain — Camera → PoE Switch → Router → NAS —
	// and then says what each failure means. So the switch is a real device
	// with real ports: a port that is down carries no traffic, and a PoE port
	// that is down carries no power, which is how a frozen camera gets
	// rebooted without walking outside. Its small UPS is why a power cut does
	// not blind the house immediately.
	sw := w.addDevice("sw-alex", "sw-hall", "switch", "alex", OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"},
		Hardware{"TP-Link TL-SG108PE", 1, 500, 128, 16, 1000, false, false}, lanIP(2))
	sw.Switch = NewSwitchPorts(sw, 8, 60)
	sw.UPS = &UPSInfo{ChargePct: 100, LastState: "online"}
	sw.Notes = "managed PoE switch"
	mkUsers(sw, map[string]*User{
		"root":  {Name: "root", UID: 0, Pass: "alex123", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
		"admin": {Name: "admin", UID: 1000, Pass: "alex123", Groups: []string{"admin"}, Home: "/home/admin", Shell: "/bin/ash"},
	})
	seedFS(sw, "switch")
	sw.Services["dropbear"] = &Service{Name: "dropbear", Desc: "Dropbear ssh", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear"}

	// the household's own computer: a battery, a lid, and no guarantee of being
	// awake — which is exactly why the household also has a NAS and an
	// assistant node
	lap := w.addDevice("laptop-alex", "laptop", "laptop", "alex", OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"ThinkBook 14", 4, 2200, 8192, 262144, 1000, true, true}, lanIP(12))
	lap.Battery = &LaptopBattery{Pct: 100, Charging: true, LidOpen: true}
	// the laptop is a household client like the desktop: it has a default
	// route through the router and a DHCP lease there. Without that a laptop
	// on the same LAN could not fetch a package — an omission, not a fact
	// (and §33 needs it: freshclam updates from the mirror like everything).
	if len(lap.Ifaces) > 0 {
		lap.Ifaces[0].GW = LANGateway
		lap.Ifaces[0].Mode = "dhcp"
	}
	mkUsers(lap, map[string]*User{
		"alex":  {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"alex", "sudo"}, Home: "/home/alex", Shell: "/bin/bash"},
		"guest": {Name: "guest", UID: 1001, Pass: "guest", Groups: []string{"guest"}, Home: "/home/guest", Shell: "/bin/bash"},
	})
	seedFS(lap, "laptop")
	// a household laptop that can be reached from the PC at all — otherwise the
	// lid and the battery are only observable by sitting in front of it
	lap.Services["dropbear"] = &Service{Name: "dropbear", Desc: "Dropbear ssh", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear"}

	// a printer that really spools: jobs queue on its own disk, paper and toner
	// run out, and the queue survives a power cycle
	prn := w.addDevice("prn-alex", "printer", "printer", "alex", OSInfo{"NeoPrint", "2.4", "4.19.0", "arm", "ash"},
		Hardware{"HP LaserJet-ish", 1, 300, 256, 1024, 100, false, false}, lanIP(45))
	prn.Printer = &PrinterState{Paper: 120, Toner: 80}
	prn.Notes = "network printer"
	mkUsers(prn, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/ash"},
	})
	seedFS(prn, "printer")
	prn.Services["cupsd"] = &Service{Name: "cupsd", Desc: "CUPS print spooler", Port: 631, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "cups", Banner: "CUPS/2.4", Conf: "/etc/cups/cupsd.conf"}

	// ---- the cabling --------------------------------------------------------
	// Port 1 is the uplink to the router: the switch's ports are down and its
	// PoE is off until something is plugged in, exactly as the hardware ships.
	sw.Switch.Ports[0].Label = "uplink"
	sw.Switch.Ports[0].Peer = router.ID
	sw.Switch.Ports[0].Uplink = true
	sw.Switch.Ports[0].Speed = 1000
	sw.CableUp(3, lap, "laptop", false)
	sw.CableUp(5, cam, "cam-front", true)
	sw.CableUp(6, prn, "printer", false)
	// the port file is the configuration: it is rendered once everything is
	// plugged in, and re-rendered whenever a port changes
	sw.FS.Write("/etc/config/switch", RenderSwitchConf(sw), 0644, "root", "root")
	// the NAS stays on the router directly: §15's chain is camera → switch →
	// router → NAS, and the NAS is what the camera's recordings land on
	lokd.Services["lockd"] = &Service{Name: "lockd", Desc: "front door lock", Port: 8899, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "lockd", Banner: "NeoLock/2.1", Conf: "/etc/lockd.conf"}

	// ---- the household phone (WS-1.2) ---------------------------------------
	// Spec §15/§209: a phone is just another device profile — a battery-
	// powered pocket computer on the Wi-Fi with a terminal, a message spool,
	// and a cellular radio that keeps SMS alive when the router is not.
	phone := w.addDevice("phone-alex", "phone", "phone", "alex", OSInfo{"NeoDroid", "15", "6.6.20", "aarch64", "bash"},
		Hardware{"Pocket Computer", 8, 2400, 8192, 131072, 0, false, true}, lanIP(22))
	mkUsers(phone, map[string]*User{
		"root": {Name: "root", UID: 0, Pass: "admin", Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"alex": {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"alex"}, Home: "/home/alex", Shell: "/bin/bash"},
	})
	seedFS(phone, "phone")
	phone.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH (pocket terminal)", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}

	// ---- the household USB stick (WS-1.3) -----------------------------------
	// A stick is a device with a filesystem and no interfaces: in a drawer
	// until someone plugs it in, and the only bridge between machines that
	// never talk (§41's air gap, in miniature).
	stick := w.addDevice("usb-alex", "usb-stick", "usb", "alex",
		OSInfo{"FAT32", "1.0", "-", "none", "none"},
		Hardware{"USB Stick", 0, 0, 0, 32768, 0, false, false}, "")
	_ = stick
	// ---- NPC neighbour ----
	npcpc := w.addDevice("npc-pc", "darkden", "pc", "mara", OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Gaming rig", 8, 4800, 32768, 131072, 1000, false, false}, "10.88.1.11")
	npcr := w.addDevice("npc-router", "modem-netgearish", "router", "mara", OSInfo{"StockOS", "1.0", "4.9.0", "arm", "ash"},
		Hardware{"ISP modem", 1, 500, 64, 8, 1000, true, false}, "10.88.1.1")
	npcr.Ifaces = append(npcr.Ifaces, &Iface{Name: "eth0.2", IP: w.allocPublicFor("router"), MAC: macFor("npc-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[npcr.Ifaces[1].IP] = npcr.ID
	if len(npcpc.Ifaces) > 0 {
		npcpc.Ifaces[0].GW = "10.88.1.1"
		npcpc.Ifaces[0].Mode = "dhcp"
	}
	mkUsers(npcpc, map[string]*User{
		"root":   {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"mara":   {Name: "mara", UID: 1000, Pass: "hunter2", Groups: []string{"mara"}, Home: "/home/mara", Shell: "/bin/bash"},
		"devops": {Name: "devops", UID: 1001, Pass: "Summer2024!", Groups: []string{"devops", "sudo"}, Home: "/home/devops", Shell: "/bin/bash"},
		// the account anonymous FTP sessions actually run as (uid 21, as on a
		// real Debian box). It has no password and no login shell: it exists to
		// own the drop directory, which is why FTPLogin special-cases the name
		// "ftp"/"anonymous" before it ever consults the account records.
		"ftp": {Name: "ftp", UID: 21, Groups: []string{"ftp"}, Home: "/srv/ftp", Shell: "/usr/sbin/nologin"},
	})
	// mara forwarded her ftp to the world — a genuine attack surface she set
	// up herself, written into her router's real firewall configuration
	npcpc.Services["vsftpd"] = &Service{Name: "vsftpd", Desc: "FTP daemon", Port: 21, Proto: "tcp", Scope: "any",
		State: "running", Handler: "npc-ftp", Banner: "220 (vsFTPd 3.0.5)", Conf: "/etc/vsftpd.conf"}
	npcpc.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan",
		State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	npcpc.Users["mara"].Home = "/home/mara"
	// mara carries a phone as well: her side of the SMS conversation, on her
	// own battery and her own cellular radio
	nphone := w.addDevice("phone-mara", "mara-phone", "phone", "mara", OSInfo{"NeoDroid", "15", "6.6.20", "aarch64", "bash"},
		Hardware{"Pocket Computer", 8, 2400, 8192, 131072, 0, false, true}, "10.88.1.50")
	mkUsers(nphone, map[string]*User{
		"mara": {Name: "mara", UID: 1000, Pass: "hunter2", Groups: []string{"mara"}, Home: "/home/mara", Shell: "/bin/bash"},
	})
	seedFS(nphone, "phone")
	seedNPCFS(npcpc)

	// ---- DNS zone ----
	w.Records = []DNSRecord{
		{Name: "mirror.neohome.example", IP: pubMirror},
		{Name: "ns-a.neohome.example", IP: pubNS},
		{Name: "api.novapanel.example", IP: pubProv},
		{Name: "irc.neohome.example", IP: pubIRC},
		{Name: "bank.firstneohome.example", IP: pubBank},
		{Name: "jobs.hiring.example", IP: pubJobs},
		{Name: "bbs.neohome.example", IP: pubBBS},
		{Name: "git.neohome.example", IP: pubGit},
		{Name: "archive.neohome.example", IP: wanIP(archive)},
		{Name: "cdn.sashimi-cdn.example", IP: wanIP(cdn)},
		{Name: "dns.isp.example", IP: pubISP},
		{Name: "home.alex.neohome.example", IP: pubHome},
	}

	// ---- services ----
	ns1.Services["nsd"] = &Service{Name: "nsd", Desc: "authoritative DNS", Port: 53, Proto: "udp+tcp", Scope: "any", State: "running", Handler: "dns-auth"}
	ispDNS.Services["dnsmasq"] = &Service{Name: "dnsmasq", Desc: "ISP resolver", Port: 53, Proto: "udp+tcp", Scope: "any", State: "running", Handler: "dns-forward"}
	mirror.Services["nginx"] = &Service{Name: "nginx", Desc: "mirror web", Port: 80, Proto: "tcp", Scope: "any", State: "running", Handler: "http-mirror", Banner: "nginx", Conf: "/etc/nginx/sites-enabled/mirror"}
	provider.Services["nginx"] = &Service{Name: "nginx", Desc: "nova panel api", Port: 80, Proto: "tcp", Scope: "any", State: "running", Handler: "http-vps", Banner: "nginx"}
	ircd.Services["ircd"] = &Service{Name: "ircd", Desc: "IRC", Port: 6667, Proto: "tcp", Scope: "any", State: "running", Handler: "irc"}
	bankd.Services["httpd"] = &Service{Name: "httpd", Desc: "bank api", Port: 80, Proto: "tcp", Scope: "any", State: "running", Handler: "http-bank", Banner: "Apache"}
	jobsd.Services["httpd"] = &Service{Name: "httpd", Desc: "job board", Port: 80, Proto: "tcp", Scope: "any", State: "running", Handler: "http-jobs", Banner: "Apache"}
	bbsd.Services["bbsd"] = &Service{Name: "bbsd", Desc: "community bulletin board", Port: 2323, Proto: "tcp",
		Scope: "any", State: "running", Handler: "bbs", Banner: "NeoBBS", Conf: "/etc/bbsd.conf"}
	bbsd.FS.Write("/etc/bbsd.conf",
		"listen on eth1 port 2323\nboards = general, market, intel, hacker\nspool = /srv/bbs\n",
		0644, "root", "root")
	gitd.Services["nginx"] = &Service{Name: "nginx", Desc: "git repositories", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-git", Banner: "nginx", Conf: "/etc/nginx/sites-enabled/git"}
	gitd.FS.MkdirAll("/etc/nginx/sites-enabled", 0755, "root", "root")
	gitd.FS.Write("/etc/nginx/sites-enabled/git",
		"server {\n  listen 80;\n  listen 443 ssl;\n  location ~ /.*\\.git {\n    root /srv/git;\n  }\n}\n",
		0644, "root", "root")
	router.Services["dnsmasq"] = &Service{Name: "dnsmasq", Desc: "DHCP+DNS forwarder", Port: 53, Proto: "udp+tcp", Scope: "lan", State: "running", Handler: "dns-forward", Conf: "/etc/dnsmasq.conf"}
	router.Services["dropbear"] = &Service{Name: "dropbear", Desc: "SSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear_2024.85"}
	// The consumer router still carries a legacy telnetd on 23, which is the
	// classic weak entry point: it is a real login (see cmdTelnet), so finding
	// the credentials here is a genuine foothold rather than a free shell.
	router.Services["telnetd"] = &Service{Name: "telnetd", Desc: "BusyBox telnetd (legacy)", Port: 23, Proto: "tcp", Scope: "lan", State: "running", Handler: "telnet", Banner: "NeoWRT telnetd"}
	nas.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.2"}
	nas.Services["nginx"] = &Service{Name: "nginx", Desc: "NAS web ui", Port: 8080, Proto: "tcp", Scope: "lan", State: "running", Handler: "http-nas", Banner: "nginx"}
	nas.Services["nfsd"] = &Service{Name: "nfsd", Desc: "NFS exports", Port: 2049, Proto: "tcp", Scope: "lan", State: "running", Handler: "nfs"}
	// the Windows half of the household file sharing (WS-1.1): the shares
	// are whatever /etc/samba/smb.conf really says
	nas.Services["smbd"] = &Service{Name: "smbd", Desc: "Samba file shares", Port: 445, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "smb", Banner: "Samba", Conf: "/etc/samba/smb.conf"}
	asst.Services["sshd"] = &Service{Name: "sshd", Desc: "assistant node", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	pc.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "stopped", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	// syslog daemons: without one there is nothing for logread/journalctl to
	// read, and a fault can never be explained
	router.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp", Scope: "lan", State: "running", Handler: "syslog"}
	// §33 central logs: the NAS runs a full syslog daemon that also *listens*,
	// so it is the household's log server and the router forwards to it. A
	// collector with no port would be a file on one machine — this one holds
	// other machines' lines, which is what makes it evidence.
	nas.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "central log server", Port: 514, Proto: "udp",
		Scope: "lan", State: "running", Handler: "syslog", Conf: "/etc/rsyslog.conf"}
	pc.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}
	asst.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp", Scope: "lan", State: "running", Handler: "syslog"}
	mirror.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}
	// the package serving hosts: the archive publishes what upstream signs,
	// the mirror republishes it, the CDN publishes a third-party tree
	archive.Services["nginx"] = &Service{Name: "nginx", Desc: "archive web", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-archive", Banner: "nginx", Conf: "/etc/nginx/sites-enabled/archive"}
	archive.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}
	cdn.Services["nginx"] = &Service{Name: "nginx", Desc: "cdn web", Port: 80, Proto: "tcp",
		Scope: "any", State: "running", Handler: "http-repo", Banner: "nginx", Conf: "/etc/nginx/sites-enabled/cdn"}
	cdn.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}

	// ---- §33: the household's defensive posture as it really is ----
	//
	// Nothing is installed by fiat: the tools come from the package manager,
	// one machine at a time. What is seeded is the wiring a real household
	// already has — a NAS that collects logs, a router that forwards to it,
	// and one file on one laptop that a scanner will one day flag.
	nas.FS.MkdirAll("/var/log/remote", 0755, "root", "root")
	nas.FS.Write("/etc/rsyslog.conf",
		"# central log server\n$ModLoad imudp\n$UDPServerRun 514\n\n*.* /var/log/syslog\n*.* /var/log/remote/all.log\n",
		0644, "root", "root")
	nas.FS.Write("/var/log/remote/all.log", "", 0640, "root", "root")
	// OpenWrt keeps its log destination in /etc/config/system — that is the
	// file a real router's forward lives in, and the one this world reads.
	router.FS.Write("/etc/config/system",
		"config system\n\toption hostname 'gateway'\n\toption log_ip '"+nas.FirstLANIP()+"'\n\toption log_port '514'\n",
		0644, "root", "root")
	if lap := w.Devices["laptop-alex"]; lap != nil {
		lap.FS.MkdirAll("/home/alex/Downloads", 0755, "alex", "alex")
		lap.FS.Write("/home/alex/Downloads/invoice-2026-04.pdf",
			"%PDF-1.4\n%\xC3\xA4\xC3\xBC\xC3\xB6\nstream\n"+
				"X5O!P%@AP[4\\PZX54(P^)7CC)7}"+`$`+"EICAR-STANDARD-ANTIVIRUS-TEST-FILE!"+`$`+"H+H*\n"+
				"endstream\n%%EOF\n",
			0644, "alex", "alex")
		lap.FS.Write("/home/alex/Downloads/README-first.txt",
			"invoice-2026-04.pdf arrived as an e-mail attachment this morning.\nnobody remembers ordering anything in April.\n",
			0644, "alex", "alex")
	}

	// ---- the scripted fault: corrupt dnsmasq after power blip ----
	w.CauseFault = Fault{
		Active:     true,
		DeviceID:   router.ID,
		Story:      "dnsmasq.conf points resolv-file at /var/run/dnsmasq/resolv.conf which no longer exists after the 03:12 power blip → every query SERVFAIL",
		ExplainFix: "edit /etc/dnsmasq.conf: resolv-file=/etc/dnsmasq.upstream, write 'nameserver 10.0.0.2' there, restart dnsmasq",
	}

	seedFS(pc, "pc")
	seedFS(router, "router")
	seedFS(nas, "nas")
	seedFS(asst, "assistant")
	// infrastructure nodes need filesystems too: the ISP resolver's own
	// resolv.conf is what the home router forwards to.
	seedFS(ispDNS, "infra")
	seedFS(ns1, "infra")
	seedFS(mirror, "infra")
	archive.FS.Write("/etc/nginx/sites-enabled/archive",
		"server {\n  listen 80;\n  root /srv/www/archive;\n  autoindex on;\n}\n", 0644, "root", "root")
	cdn.FS.Write("/etc/nginx/sites-enabled/cdn",
		"server {\n  listen 80;\n  root /srv/www/repo;\n  autoindex on;\n}\n", 0644, "root", "root")
	seedFS(archive, "infra")
	seedFS(cdn, "infra")
	seedFS(provider, "infra")
	seedFS(ircd, "infra")
	seedFS(bankd, "infra")
	seedFS(jobsd, "infra")
	seedFS(bbsd, "infra")
	seedFS(gitd, "infra")
	// the git server's accounts: the daemon user, and the household identity
	// so a push is a real authenticated write
	mkUsers(gitd, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"git":  {Name: "git", UID: 112, Groups: []string{"git"}, Home: "/home/git", Shell: "/bin/bash"},
		"alex": {Name: "alex", UID: 1000, Pass: "alex123", Groups: []string{"users"}, Home: "/home/alex", Shell: "/bin/bash"},
	})
	seedFS(npcpc, "pc")
	seedFS(npcr, "router")
	// mara forwarded her ftp to the world — a genuine attack surface she set
	// up herself, written into her router's real firewall configuration (after
	// the image seed, which starts every router with nothing forwarded)
	seedMaraForward(w, npcr)

	// The fault's own history goes into the router log AFTER seeding, otherwise
	// seedFS's empty-file write would erase it. This is what makes the chain
	// investigable with `logread` instead of a guess.
	router.Logf("warn", "kernel", "watchdog: power blip detected, rebooting after 3s")
	router.Logf("info", "kernel", "NeoWRT 24.10 booting (mips/ar71xx), 128 MiB RAM")
	router.Logf("info", "syslogd", "syslogd started: BusyBox v1.36.1")
	router.Logf("info", "dnsmasq", "compiled with resolv-file=/var/run/dnsmasq/resolv.conf")
	router.Logf("err", "dnsmasq", "failed to read resolv-file /var/run/dnsmasq/resolv.conf: No such file or directory")
	router.Logf("err", "dnsmasq", "no upstream servers configured — all queries will SERVFAIL")
	router.Logf("warn", "dnsmasq", "falling through to /etc/resolv.conf, which is empty on this device")
	router.Dmesg = append(router.Dmesg, router.W.Sim.Format("Jan 2 15:04:05")+" gateway kernel: watchdog: power blip, 03:12 restart")

	w.Bank.Accts["alex"] = &Account{Owner: "alex", Name: "Alex (main)", Balance: 15000,
		Tx: []Tx{{At: time.Now().AddDate(0, 0, -20), Amount: 15000, Memo: "opening deposit", Balance: 15000}}}
	w.Bank.Accts["assistant"] = &Account{Owner: "assistant", Name: "Alex (assistant budget)", Balance: 5000}
	w.Bank.Accts["mara"] = &Account{Owner: "mara", Name: "Mara (main)", Balance: 420000}

	w.Jobs.List = []*Job{
		{ID: "J-101", Title: "修复家庭网络 DNS(邻居求助)", Client: "mira-9", Pay: 4500, Tier: 2, Target: "house:alex",
			Help: "我家里所有网站都打不开,但是按 IP 直连还可以。整栋楼只有我的网这样。50 块,修好即付。", Verify: "dns-fix", Done: false},
		{ID: "J-102", Title: "把旧笔记本重装成轻量系统", Client: "olduser", Pay: 1500, Tier: 1, Target: "self",
			Help: "给 assistant 节点装 busybox 即可交差。", Verify: "pkg-busybox", Done: false},
		// §34: the investigation workstream is playable from both ends. You
		// can be the victim who files a real report, and you can be the person
		// on shift who has to move the cases — with an account that is in the
		// desk's group and nothing more.
		{ID: "J-103", Title: "网络取证:把扫我的机器报给它的运营商", Client: "mira-9", Pay: 3500, Tier: 2, Target: "house:alex",
			Help: "我的日志里一直有同一个地址在扫端口。帮我按规矩报上去,别只在自己的防火墙上封它——" +
				"它扫的不止我一家。先看自己的记录(abuse evidence <ip>),再 abuse report。",
			Verify: "abuse-report", Done: false},
		{ID: "J-104", Title: "NovaPanel 滥用台顶班:裁定两个工单", Client: "novapanel-abuse", Pay: 3000, Tier: 2, Target: "vps",
			Help: "值班的人病了。用 contractor / np-shift-2026 登上 abuse.novapanel.example," +
				"abuse queue 看队列,abuse triage <编号> --note ... 按证据裁定两个工单。裁定要写理由。",
			Verify: "abuse-triage", Done: false},
		{ID: "J-105", Title: "Meridian 文件服务器:把项目目录交给项目组", Client: "meridian-soc", Pay: 2500, Tier: 1, Target: "self",
			Help: "办公室的文件服务器上 /srv/projects 还是空的。从公司内网登进去(admin / meridian-admin)," +
				"建 /srv/projects/handover/README 写清楚交接内容。共享已经在 smb.conf 里配好了。",
			Verify: "meridian-share", Done: false},
		// §35: the top of the ladder is work too. The unit's intake reads
		// complaints and sends lawful requests; signing an order is a further
		// permission, and the job text names the account that has it.
		{ID: "J-106", Title: "网络犯罪组顶班:把报案变成合法的调证请求", Client: "le-cyber", Pay: 4200, Tier: 3, Target: "vps",
			Help: "组里缺人。用 cases / cnu-cases-2026 登上 cnu.gov.example,abuse queue 看案卷," +
				"abuse triage <编号> --note ... 把案卷收进来,再 abuse act <编号> request --note \"法律依据\" " +
				"向持有该地址的运营商发出调证请求。请求要有理由,案卷要有盲区记录。",
			Verify: "law-intake", Done: false},
		{ID: "J-107", Title: "签出第一份搜查令", Client: "le-cyber", Pay: 6000, Tier: 4, Target: "vps",
			Help: "证据够了才能签令。用 agent / cnu-agents-2026 登上 cnu.gov.example," +
				"把一份已经披露了订户的案卷推到 law:disclosed,用 abuse act <编号> warrant --note \"依据\" 签令," +
				"令要送达持有记录的网络。文件撑不住的时候,abuse 会拒绝你——那是设计,不是故障。",
			Verify: "law-warrant", Done: false},
		// Staged missions (Phase 2): ordered stages with their own pay, a
		// prerequisite chain, and the §44 template rendered in `job show`.
		// Each stage's check is one of the verifiers the world already
		// speaks — the new part is the order, the gate and the installments.
		{ID: "J-108", Title: "分部上线:把新办公室带进网络", Client: "meridian-soc", Pay: 5000, Tier: 2, Target: "self",
			Help: "办公室隔壁租了个小房间做分部。先把 DNS 修好(J-101)再来:分三步交差,每步单独结算," +
				"用 job advance J-108 一步步交。",
			Requires: []string{"J-101"},
			Stages: []MissionStage{
				{Name: "link", Help: "分部能上网:任一 sshd 回答", Verify: "ssh-up", Pay: 1500},
				{Name: "serve", Help: "分部能提供 web:任一 nginx 可达", Verify: "web-up", Pay: 2500},
				{Name: "clean", Help: "收尾干净:家里路由上不留封禁", Verify: "clean", Pay: 1000},
			},
			Solutions: []string{"先修 DNS,再逐段验证:ssh 不通查服务,web 不通查防火墙和端口"},
			Expected:  "sshd 回答、nginx 可达、路由无封禁,三段按顺序成立",
			Evidence:  "每段交差写银行流水备注,连同世界事件一起可查",
			Done:      false},
		{ID: "J-109", Title: "滥用台认证:从报案到值班", Client: "novapanel-abuse", Pay: 5000, Tier: 3, Target: "vps",
			Help:     "J-103 报过案了,现在考值班:先确认报案记录在案,再亲手裁定两个工单。两段都要亲手做,用 job advance J-109 交。",
			Requires: []string{"J-103"},
			Stages: []MissionStage{
				{Name: "report", Help: "报案记录在案:运营商处有你的工单", Verify: "abuse-report", Pay: 2000},
				{Name: "shift", Help: "顶一班:亲手裁定两个工单并写理由", Verify: "abuse-triage", Pay: 3000},
			},
			Solutions: []string{"abuse evidence 先看记录再 report;abuse queue 看队列再 triage"},
			Expected:  "新报案工单 + 两次亲手裁定,按顺序成立",
			Evidence:  "运营商工单、裁定理由、银行流水",
			Done:      false},
	}

	w.Prov = &Provider{DeviceID: provider.ID, APIKey: "np_live_9f3c2a",
		Plans: []Plan{
			// §12 asks the player to choose a region, a spec and IPv4/IPv6, and
			// §13 gives those choices real consequences: the cheap plan is
			// cheaper because it has no public IPv4, and that is a fact about
			// what the node can host, not a line in a list.
			{Name: "nano-shared", Cores: 1, RAM: 512, Disk: 10240, Monthly: 300, Region: "ap-northeast", IPMode: "shared", V6: true},
			{Name: "nano-1", Cores: 1, RAM: 512, Disk: 10240, Monthly: 600, Region: "ap-northeast", IPMode: "public", V6: true},
			{Name: "small-2", Cores: 2, RAM: 2048, Disk: 40960, Monthly: 1800, Region: "eu-central", IPMode: "public", V6: true},
			{Name: "medium-4", Cores: 4, RAM: 4096, Disk: 81920, Monthly: 3500, Region: "us-east", IPMode: "public", V6: true},
			{Name: "v6-sandbox", Cores: 1, RAM: 1024, Disk: 20480, Monthly: 440, Region: "eu-central", IPMode: "v6only", V6: true},
		},
		FirstMonths: 2,
		Regions:     DefaultRegions(),
		Nodes:       map[string]*NodeRecord{}}

	w.ChatBots()

	for _, d := range w.Devices {
		for _, s := range d.Services {
			if s.State == "running" {
				d.AddProc(&Proc{Name: s.Name, User: "root", CPU: 0.3, Mem: 24, TTY: "?", State: "S",
					Start: d.Boot, Svc: s.Name, Kind: "builtin"})
			}
		}
	}
	// the mirror is operated by hand: root has no password (no interactive
	// login at all) and the ops account is the one the job's mail provides
	mkUsers(mirror, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"ops":  {Name: "ops", UID: 1000, Pass: "mirror-ops-2026", Groups: []string{"ops", "sudo"}, Home: "/home/ops", Shell: "/bin/bash"},
	})

	// §33: the host on the internet that actually probes and guesses. It is a
	// device with an address like any other, so a player can trace it and §34
	// has a real actor to report.
	w.addScanner()

	// §34: the organisations — registry, ISP NOC, abuse desk, datacenter,
	// enterprise, security team, law enforcement — each on a real address.
	// They are seeded before the WAN so their prefixes are announced.
	seedDesks(w)

	// workstream seeds — each implemented in its own file (wan.go/cron.go/vm.go/tls.go)
	seedWAN(w)
	seedCron(w)
	seedVMs(w)
	seedMail(w)
	seedTLS(w)
	seedBBS(w)
	seedGit(w)
	seedIoT(w)
	seedSMS(w)
	seedUSB(w)
	seedMarket(w)
	seedOrgs(w)
	SeedPackageWorld(w)

	// §13: the addresses. Every device gets its v6 addresses once the whole
	// topology exists, so a host is numbered inside its own router's /64.
	seedV6World(w)

	// the LAN plan is checked before the world can be used: a static inside
	// the DHCP band or two devices on one address panics right here
	w.ValidateLAN()

	w.AddEvent("world", "info", "engine", "world booted: %d devices", len(w.Devices))
	return w
}

// mkUsers installs an account table and makes sure every real account has
// its home directory — useradd -m, not a shell whose history file can
// never exist. System accounts (nologin, /nonexistent) get nothing.
func mkUsers(d *Device, m map[string]*User) {
	d.Users = m
	for _, u := range m {
		if u.Home == "" || u.Home == "/nonexistent" || strings.HasSuffix(u.Shell, "nologin") {
			continue
		}
		d.FS.MkdirAll(u.Home, 0755, u.Name, u.Name)
	}
}

// seedMaraForward gives the NPC's router the port-forward that the FTP
// storyline runs through: a redirect in /etc/config/firewall, readable with
// `uci show firewall` and closable with `uci set ...enabled=0`.
func seedMaraForward(w *World, router *Device) {
	f, _, ok := router.ReadUCIFile("firewall")
	if !ok {
		return
	}
	s := f.Add("redirect", "ftp-darkden")
	s.Set("target", "DNAT")
	s.Set("src", "wan")
	s.Set("proto", "tcp")
	s.Set("src_dport", "21")
	s.Set("dest_ip", "10.88.1.11")
	s.Set("dest_port", "21")
	s.Set("name", "darkden ftp")
	s.Set("enabled", "1")
	router.WriteUCIFile(f)
	router.Logf("info", "firewall", "redirect 'ftp-darkden' active: wan tcp/21 -> 10.88.1.11:21")
	_ = w
}
