package core

import "time"

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
	_ = core

	for _, d := range []*Device{ispDNS, ns1, mirror, provider, ircd, bankd, jobsd, bbsd, gitd} {
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
	for _, d := range []*Device{ispDNS, ns1, mirror, provider, ircd, bankd, jobsd, bbsd, gitd} {
		w.IPMap[d.Ifaces[1].IP] = d.ID
	}

	// ---- the household ----
	pc := w.addDevice("pc-alex", "home-pc", "pc", "alex", OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Generic Desktop", 4, 3200, 8192, 65536, 1000, false, false}, "10.77.1.11")
	router := w.addDevice("router-alex", "gateway", "router", "alex", OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"},
		Hardware{"Archer C7 (stock)", 1, 800, 128, 16, 1000, true, false}, "10.77.1.1")
	nas := w.addDevice("nas-alex", "nas", "nas", "alex", OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"NAS Box", 2, 1600, 2048, 409600, 1000, false, false}, "10.77.1.30")
	asst := w.addDevice("asst-alex", "assistant", "pc", "alex", OSInfo{"Alpine", "3.20", "6.6.20", "x86_64", "ash"},
		Hardware{"Assistant Mini-PC", 2, 2000, 2048, 32768, 1000, false, false}, "10.77.1.20")
	asst.Notes = "assistant node"

	// ---- the management controller -------------------------------------------
	// A BMC-class box wired to the mains before the breaker, on its own battery
	// and cellular backhaul. This is what makes `power cut` a playable move
	// instead of an unrecoverable one: a real operator can always get back in
	// through out-of-band management. It is also the machine that a determined
	// attacker would love to own, which is the point.
	bmc := w.addDevice("bmc-alex", "bmc", "bmc", "alex", OSInfo{"OpenBMC", "2.14", "6.6.7", "arm", "ash"},
		Hardware{"NeoBMC", 1, 1200, 512, 4096, 100, false, true}, "10.77.1.250")
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

	for _, d := range []*Device{pc, nas, asst} {
		if len(d.Ifaces) > 0 {
			d.Ifaces[0].GW = "10.77.1.1"
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
	router.Ifaces = append(router.Ifaces, &Iface{Name: "eth0", IP: w.allocPublicFor("router"), MAC: macFor("router-alex-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
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
		Hardware{"PoE Door Cam", 1, 400, 128, 1024, 10, false, false}, "10.77.1.40")
	lokd := w.addDevice("lock-alex", "lock-front", "iot", "alex", OSInfo{"NeoIoT", "3.1", "5.15.160", "arm", "ash"},
		Hardware{"Smart Lock", 1, 120, 64, 512, 5, false, false}, "10.77.1.43")
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
	lokd.Services["lockd"] = &Service{Name: "lockd", Desc: "front door lock", Port: 8899, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "lockd", Banner: "NeoLock/2.1", Conf: "/etc/lockd.conf"}

	// ---- the household phone (WS-1.2) ---------------------------------------
	// Spec §15/§209: a phone is just another device profile — a battery-
	// powered pocket computer on the Wi-Fi with a terminal, a message spool,
	// and a cellular radio that keeps SMS alive when the router is not.
	phone := w.addDevice("phone-alex", "phone", "phone", "alex", OSInfo{"NeoDroid", "15", "6.6.20", "aarch64", "bash"},
		Hardware{"Pocket Computer", 8, 2400, 8192, 131072, 0, false, true}, "10.77.1.21")
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
	npcr.Ifaces = append(npcr.Ifaces, &Iface{Name: "eth0", IP: w.allocPublicFor("router"), MAC: macFor("npc-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[npcr.Ifaces[1].IP] = npcr.ID
	if len(npcpc.Ifaces) > 0 {
		npcpc.Ifaces[0].GW = "10.88.1.1"
		npcpc.Ifaces[0].Mode = "dhcp"
	}
	mkUsers(npcpc, map[string]*User{
		"root":   {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"mara":   {Name: "mara", UID: 1000, Pass: "hunter2", Groups: []string{"mara"}, Home: "/home/mara", Shell: "/bin/bash"},
		"devops": {Name: "devops", UID: 1001, Pass: "Summer2024!", Groups: []string{"devops", "sudo"}, Home: "/home/devops", Shell: "/bin/bash"},
	})
	// mara forwarded her ftp to the world — a genuine attack surface she set up
	npcr.PortFwd = append(npcr.PortFwd, FwdRule{Proto: "tcp", WPort: 21, DstIP: "10.88.1.11", DPort: 21, Enable: true})
	npcr.Firewall = []FWRule{{Chain: "WAN-TO-LAN", Proto: "tcp", Port: 0, Action: "DROP"}}
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
	nas.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp", Scope: "lan", State: "running", Handler: "syslog"}
	pc.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}
	asst.Services["syslogd"] = &Service{Name: "syslogd", Desc: "BusyBox syslogd", Port: 0, Proto: "udp", Scope: "lan", State: "running", Handler: "syslog"}
	mirror.Services["rsyslog"] = &Service{Name: "rsyslog", Desc: "system logging", Port: 0, Proto: "udp", Scope: "any", State: "running", Handler: "syslog"}

	// ---- the scripted fault: corrupt dnsmasq after power blip ----
	w.CauseFault = Fault{
		Active:     true,
		DeviceID:   router.ID,
		Story:      "dnsmasq.conf points resolv-file at /var/run/dnsmasq/resolv.conf which no longer exists after the 03:12 power blip → every query SERVFAIL",
		ExplainFix: "edit /etc/dnsmasq.conf: resolv-file=/etc/dnsmasq.upstream, write 'nameserver 10.0.0.2' there, restart dnsmasq",
	}

	w.Repos["main"] = BuildMainRepo()
	w.Repos["contrib"] = BuildContribRepo()

	seedFS(pc, "pc")
	seedFS(router, "router")
	seedFS(nas, "nas")
	seedFS(asst, "assistant")
	// infrastructure nodes need filesystems too: the ISP resolver's own
	// resolv.conf is what the home router forwards to.
	seedFS(ispDNS, "infra")
	seedFS(ns1, "infra")
	seedFS(mirror, "infra")
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
	}

	w.Prov = &Provider{DeviceID: provider.ID, APIKey: "np_live_9f3c2a",
		Plans: []Plan{
			{Name: "nano-1", Cores: 1, RAM: 512, Disk: 10240, Monthly: 600, Region: "jp-tokyo"},
			{Name: "small-2", Cores: 2, RAM: 2048, Disk: 40960, Monthly: 1800, Region: "de-frankfurt"},
			{Name: "medium-4", Cores: 4, RAM: 4096, Disk: 81920, Monthly: 3500, Region: "us-east"},
		},
		FirstMonths: 2}

	w.ChatBots()

	for _, d := range w.Devices {
		for _, s := range d.Services {
			if s.State == "running" {
				d.AddProc(&Proc{Name: s.Name, User: "root", CPU: 0.3, Mem: 24, TTY: "?", State: "S",
					Start: d.Boot, Svc: s.Name, Kind: "builtin"})
			}
		}
	}
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

	w.AddEvent("world", "info", "engine", "world booted: %d devices", len(w.Devices))
	return w
}

func mkUsers(d *Device, m map[string]*User) { d.Users = m }
