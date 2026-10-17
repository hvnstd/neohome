package core

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// World generator (§44) — content growth without new rules
//
// Hand-seeded content stays hand-seeded; what generates is routine real
// estate: an NPC household (router with public WAN, a PC, a phone, one
// tenant) on the next free NPC-street subnet, and an office (an edge router
// with two VLANs, a file/directory server and a workstation) on the next
// free business-park site. Both have working DNS, safe defaults (nothing
// forwarded — §14 holds for generated boxes exactly like seeded ones), a
// handle the chat already answers to, and a BBS hello. Deterministic
// throughout: names rotate from fixed lists by counter, passwords derive
// from hashes, subnets allocate lowest-free — no dice anywhere, so the same
// world generates the same street and the same office park.
//
// A household costs its developer $100 and an office $500: real money for
// real devices out of finite streets and plots, which is the rate limit
// instead of a permission. The subnet numbers themselves have one owner,
// addr.go — this file never spells one out.
// ---------------------------------------------------------------------------

// genPriceCents is what wiring a new apartment costs the developer.
const genPriceCents = 10000

// genCap bounds generated households: one per street the allocator owns
// (eight lots), so the cap is the allocator's real capacity, not a number
// chosen here.
const genCap = 8

// genEntPriceCents is what standing up a whole office costs: five times a
// household, because a site is an edge router, two segmented VLANs and
// three hosts, not a single PC.
const genEntPriceCents = 50000

// genEntCap bounds generated enterprise sites: the business park has the
// same footprint as the residential street — four plots.
const genEntCap = 4

var genNames = []string{"nadia", "omar", "priya", "tomas", "yuki", "sam", "lena", "kai"}

var genEntNames = []string{"kestrel", "northwind", "helios", "vantage"}

// HouseholdCapacity and EnterpriseCapacity expose the caps so the shell can
// report remaining capacity without keeping a second copy of the number.
func HouseholdCapacity() int  { return genCap }
func EnterpriseCapacity() int { return genEntCap }

// GenHousehold wires one NPC household for payer: router, PC, phone, tenant
// and hello. Every device is ordinary state in the world's tables — a
// generated box is indistinguishable from a seeded one by construction.
func (w *World) GenHousehold(payer string) (string, error) {
	acc := w.Bank.Accts[payer]
	if acc == nil || acc.Balance < genPriceCents {
		return "", fmt.Errorf("%s cannot cover the $100.00 development fee", payer)
	}
	if w.GenSeq >= genCap {
		return "", fmt.Errorf("the street is full (%d generated households max)", genCap)
	}
	base, err := w.AllocNPCSubnet()
	if err != nil {
		return "", err
	}
	name := genNames[w.GenSeq%len(genNames)]
	tag := fmt.Sprintf("%s-%d", name, w.GenSeq+1)

	// the gateway: public WAN like any edge, NAT inside, DNS that works
	// (upstream learned the way DHCP learns it), firewall defaults, and no
	// forwards — exposure is configured, never seeded
	router := w.addDevice("npc-router-"+tag, "modem-"+tag, "router", name,
		OSInfo{"StockOS", "1.0", "4.9.0", "arm", "ash"},
		Hardware{"ISP modem", 1, 500, 64, 8, 1000, true, false}, base+"1")
	router.Ifaces = append(router.Ifaces, &Iface{Name: "eth0.2", IP: w.allocPublicFor("router"),
		MAC: macFor(tag + "-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[router.Ifaces[1].IP] = router.ID
	seedFS(router, "router")
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver "+PublicResolver+"\n", 0644, "root", "root")
	router.FS.Write("/etc/dnsmasq.conf",
		fmt.Sprintf("# NeoWRT dnsmasq\ndomain-needed\nbogus-priv\nresolv-file=/etc/dnsmasq.upstream\nstrict-order\ndhcp-range=%s100,%s200,255.255.255.0,12h\ninterface=eth0\nbind-interfaces\n", base, base),
		0644, "root", "root")

	// the tenant's box: an account with a derived password (never the
	// scanner's handful — the world must not own itself at boot), a home,
	// and a notes file with a voice
	pc := w.addDevice("npc-pc-"+tag, "home-"+tag, "pc", name,
		OSInfo{"NeoOS", "13.2", "6.12.9", "x86_64", "bash"},
		Hardware{"Desktop", 4, 3200, 8192, 65536, 1000, false, false}, base+"11")
	pc.Ifaces[0].GW = base + "1"
	pc.Ifaces[0].Mode = "dhcp"
	pass := randPass(tag + "-tenant")
	mkUsers(pc, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		name:   {Name: name, UID: 1000, Pass: pass, Groups: []string{name}, Home: "/home/" + name, Shell: "/bin/bash"},
	})
	seedFS(pc, "pc")
	pc.FS.Write("/home/"+name+"/notes.txt", "new place, new network. wifi password is on the fridge.\n", 0644, name, name)

	// the tenant's phone: registered on the cellular network like the rest
	phone := w.addDevice("phone-"+tag, name+"-phone", "phone", name,
		OSInfo{"NeoDroid", "15", "6.6.20", "aarch64", "bash"},
		Hardware{"Pocket Computer", 8, 2400, 8192, 131072, 0, false, true}, base+"50")
	mkUsers(phone, map[string]*User{
		name: {Name: name, UID: 1000, Pass: pass, Groups: []string{name}, Home: "/home/" + name, Shell: "/bin/bash"},
	})
	seedFS(phone, "phone")
	if w.SMS != nil {
		// 555-0210 upward: eight lots fit the format with room to spare
		num := fmt.Sprintf("555-%04d", 210+w.GenSeq)
		w.SMS.Numbers[num] = phone.ID
		w.SMS.Battery[phone.ID] = 60 + (w.GenSeq*7)%35
		phone.FS.Write("/home/"+name+"/.contacts", "mara 555-0001\n", 0600, name, name)
	}

	// the house resolver and the house clients point at each other: the
	// router's hosts file learns the new names (seedFS ran before the pc
	// and phone existed), and their resolv.conf names their own gateway,
	// not alex's
	if data, ok := router.FS.Read("/etc/hosts"); ok {
		router.FS.Write("/etc/hosts", string(data)+
			base+"11 home-"+tag+" home-"+tag+".lan\n"+
			base+"50 "+name+"-phone "+name+"-phone.lan\n", 0644, "root", "root")
	}
	pc.FS.Write("/etc/resolv.conf", "nameserver "+base+"1\n", 0644, "root", "root")
	phone.FS.Write("/etc/resolv.conf", "nameserver "+base+"1\n", 0644, "root", "root")

	// the tenant joins the network's people: global keyword replies cover
	// them with the same answers as everyone else, and the hello post says
	// they moved in
	w.NPCNames = append(w.NPCNames, name)
	w.BBSPost("general", name, "hello from "+base+"0/24",
		"just moved in down the street. pc, phone, and a router I have not broken yet.\nbe gentle.", "")

	acc.Balance -= genPriceCents
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -genPriceCents,
		Memo: "developed " + base + "0/24 for " + name, Balance: acc.Balance})
	w.GenSeq++
	w.GenNames = append(w.GenNames, name)
	w.AddEvent(router.ID, "info", "worldgen", "%s developed %s0/24, tenant %s", payer, base, name)
	return name, nil
}

// GenEnterprise stands up one generated office on the next free enterprise
// site: an edge router with two VLANs (servers on 10, workstations on 20),
// dnsmasq/dropbear/smtpd, a real /etc/config/network with explicit
// inter-VLAN allow stanzas, and three hosts — a directory server, a file
// server and a workstation. It is modelled on the Meridian seed, so a
// generated office is indistinguishable from a hand-built one except that no
// two are alike.
func (w *World) GenEnterprise(payer string) (string, error) {
	acc := w.Bank.Accts[payer]
	if acc == nil || acc.Balance < genEntPriceCents {
		return "", fmt.Errorf("%s cannot cover the $500.00 site development fee", payer)
	}
	if w.EnterpriseSeq >= genEntCap {
		return "", fmt.Errorf("the business park is full (%d generated sites max)", genEntCap)
	}
	base, err := w.AllocEnterpriseSubnet()
	if err != nil {
		return "", err
	}
	wsBase := EnterpriseWorkstationSubnet(base)
	name := genEntNames[w.EnterpriseSeq%len(genEntNames)]
	tag := fmt.Sprintf("%s-%d", name, w.EnterpriseSeq+1)
	domain := name + ".example"
	srvGW, wsGW := base+"1", wsBase+"1"

	// the edge router: public WAN like any household edge, two tagged LANs,
	// DNS that works, and the same default-deny firewall a seeded router
	// ships — exposure is configured, never generated
	router := w.addDevice("ent-router-"+tag, "gw."+domain, "router", name,
		OSInfo{"NeoWRT", "24.10", "5.15.160", "mips", "ash"},
		Hardware{"Edge Router", 2, 1800, 2048, 8192, 500, false, false}, "")
	router.Ifaces = append(router.Ifaces,
		&Iface{Name: "eth1.10", IP: srvGW, CIDR: base + "0/24", MAC: macFor(tag + "-v10"), Zone: "lan", Up: true, VLAN: 10},
		&Iface{Name: "eth1.20", IP: wsGW, CIDR: wsBase + "0/24", MAC: macFor(tag + "-v20"), Zone: "lan", Up: true, VLAN: 20})
	wan := w.allocPublicFor("router")
	router.Ifaces = append(router.Ifaces, &Iface{Name: "eth0.2", IP: wan, MAC: macFor(tag + "-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[srvGW] = router.ID
	w.IPMap[wsGW] = router.ID
	w.IPMap[wan] = router.ID
	mkUsers(router, map[string]*User{
		"admin": {Name: "admin", UID: 1000, Pass: randPass(tag + "-admin"),
			Groups: []string{"admin", "sudo"}, Home: "/home/admin", Shell: "/bin/ash"},
	})
	// seedFS writes the default-deny firewall and a placeholder network config
	// (it renders from Ifaces[0], which the two VLANs now own); both are
	// replaced below with the office's real shaping
	seedFS(router, "router")
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver "+PublicResolver+"\n", 0644, "root", "root")
	router.FS.Write("/etc/dnsmasq.conf",
		fmt.Sprintf("# NeoWRT dnsmasq\ninterface=eth1.10\ninterface=eth1.20\nresolv-file=/etc/dnsmasq.upstream\nstrict-order\ndhcp-range=%s100,%s200,255.255.255.0,12h\ndomain=%s\n",
			wsBase, wsBase, domain), 0644, "root", "root")
	router.FS.Write(UCIPath("network"), enterpriseNetworkConfig(srvGW, wsGW, tag), 0644, "root", "root")
	router.Services["dnsmasq"] = &Service{Name: "dnsmasq", Desc: "DHCP+DNS forwarder", Port: 53, Proto: "udp+tcp",
		Scope: "lan", State: "running", Handler: "dns-forward", Conf: "/etc/dnsmasq.conf"}
	router.Services["dropbear"] = &Service{Name: "dropbear", Desc: "SSH", Port: 22, Proto: "tcp",
		Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-dropbear_2024.85"}
	router.Services["smtpd"] = &Service{Name: "smtpd", Desc: "SMTP mail transfer agent", Port: 25, Proto: "tcp",
		Scope: "wan", State: "running", Handler: "smtpd", Banner: "220 mail." + domain + " ESMTP ready",
		Conf: "/etc/mail/smtpd.conf"}
	router.FS.MkdirAll("/etc/mail", 0755, "root", "root")
	router.FS.Write("/etc/mail/smtpd.conf",
		"listen on eth0.2 port 25\nlisten on lo port 25\naction \"local\" mbox\nmatch from any for local\n",
		0644, "root", "root")
	router.Notes = domain + " office gateway. NAT for the office LAN, no inbound services published."

	// the office hosts: segmented like Meridian's — a directory server and a
	// file server on the server VLAN, one workstation on the workstation VLAN
	type entHost struct {
		id, hostname, ip, gw string
		vlan                 int
		hw                   Hardware
		os                   OSInfo
		services             map[string]*Service
	}
	hosts := []entHost{
		{"ent-dc-" + tag, "dc." + domain, base + "10", srvGW, 10,
			Hardware{"Server", 8, 3000, 16384, 204800, 500, false, false},
			OSInfo{"Windows Server", "2022", "-", "x86_64", "cmd"},
			map[string]*Service{
				"ldap": {Name: "ldap", Desc: "directory service", Port: 389, Proto: "tcp", Scope: "lan", State: "running", Handler: "ldap"},
				"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.4"}}},
		{"ent-fs-" + tag, "fs." + domain, base + "20", srvGW, 10,
			Hardware{"Server", 4, 2400, 8192, 409600, 500, false, false},
			OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
			map[string]*Service{
				"smbd": {Name: "smbd", Desc: "Samba file shares", Port: 445, Proto: "tcp", Scope: "lan", State: "running", Handler: "smb", Conf: "/etc/samba/smb.conf"},
				"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.2"}}},
		{"ent-ws-" + tag, "ws." + domain, wsBase + "31", wsGW, 20,
			Hardware{"Workstation", 4, 3200, 8192, 102400, 500, false, false},
			OSInfo{"Windows", "11", "-", "x86_64", "cmd"},
			map[string]*Service{
				"sshd": {Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "lan", State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.4"}}},
	}
	for _, h := range hosts {
		d := w.addDevice(h.id, h.hostname, "pc", name, h.os, h.hw, h.ip)
		d.Ifaces[0].GW = h.gw
		d.Ifaces[0].VLAN = h.vlan
		mkUsers(d, map[string]*User{
			"admin": {Name: "admin", UID: 1000, Pass: randPass(tag + "-admin"),
				Groups: []string{"admin", "sudo"}, Home: "/home/admin", Shell: "/bin/bash"},
		})
		seedFS(d, "infra")
		seedHostFirewall(d, d.FS, "DROP", []Rule{
			{Name: "lan-ssh", Proto: "tcp", Port: 22, Target: "ACCEPT", Src: "lan"},
			{Name: "lan-smb", Proto: "tcp", Port: 445, Target: "ACCEPT", Src: "lan"},
			{Name: "lan-ldap", Proto: "tcp", Port: 389, Target: "ACCEPT", Src: "lan"},
		})
		for _, svc := range h.services {
			d.Services[svc.Name] = svc
		}
		d.Installed["sshd"] = &VPkg{Name: "sshd", Version: "9.4"}
		d.FS.Write("/etc/resolv.conf", "nameserver "+h.gw+"\nsearch "+domain+"\n", 0644, "root", "root")
		d.FS.Write("/etc/motd", name+" office host. The security team watches this segment.\n", 0644, "root", "root")
		d.Notes = name + " office host"
	}

	// the file server really serves: the share is a file, and the admin
	// account belongs to the group the share trusts — without that the share
	// is a directory nobody inside the company can manage
	if fs := w.Devices["ent-fs-"+tag]; fs != nil {
		fs.FS.Write("/etc/samba/smb.conf",
			"[global]\n   workgroup = "+name+"\n   server string = "+domain+" file server\n   security = user\n\n[projects]\n   path = /srv/projects\n   valid users = @staff\n   read only = no\n   browseable = yes\n",
			0644, "root", "root")
		if u := fs.FindUser("admin"); u != nil && !inAnyGroup(u, "staff") {
			u.Groups = append(u.Groups, "staff")
			refreshPasswd(fs)
		}
		fs.FS.MkdirAll("/srv/projects", 0775, "root", "staff")
	}

	// the office resolver and the office clients point at each other: the
	// gateway's hosts file learns the new names (seedFS ran before the hosts
	// existed), and every client names its own gateway, not alex's
	if data, ok := router.FS.Read("/etc/hosts"); ok {
		router.FS.Write("/etc/hosts", string(data)+
			base+"10 dc."+domain+" dc\n"+
			base+"20 fs."+domain+" fs\n"+
			wsBase+"31 ws."+domain+" ws\n", 0644, "root", "root")
	}

	// the site joins the network's people and the zone gains its public name
	w.NPCNames = append(w.NPCNames, name)
	w.Records = append(w.Records, DNSRecord{Name: "gw." + domain, IP: wan})
	w.BBSPost("general", name, "office network up at "+domain,
		"small office online: servers and workstations on their own segments behind one gateway.\nno ports published, so bring a key.\n", "")

	acc.Balance -= genEntPriceCents
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -genEntPriceCents,
		Memo: "developed " + base + "0/24 and " + wsBase + "0/24 for " + name, Balance: acc.Balance})
	w.EnterpriseSeq++
	w.EnterpriseNames = append(w.EnterpriseNames, name)
	w.AddEvent(router.ID, "info", "worldgen", "%s developed %s for %s (VLAN 10/20)", payer, domain, name)
	return name, nil
}

// enterpriseNetworkConfig renders the gateway's /etc/config/network: two
// tagged interfaces, the switch port membership (a fact only readers of this
// file see) and the inter-VLAN policy — workstations may reach the servers'
// file sharing and ssh; servers initiate nothing back. That asymmetry is the
// lateral-movement lesson, and like every policy in this world the file is
// re-parsed on every packet, never cached.
func enterpriseNetworkConfig(srvGW, wsGW, tag string) string {
	f := &UCIFile{Name: "network"}
	srv := f.Add("interface", "lan10")
	srv.Set("proto", "static")
	srv.Set("ipaddr", srvGW)
	srv.Set("netmask", "255.255.255.0")
	srv.Set("vlan", "10")
	ws := f.Add("interface", "lan20")
	ws.Set("proto", "static")
	ws.Set("ipaddr", wsGW)
	ws.Set("netmask", "255.255.255.0")
	ws.Set("vlan", "20")
	v10 := f.Add("switch_vlan", "v10")
	v10.Set("id", "10")
	v10.Set("ports", "ent-dc-"+tag+" ent-fs-"+tag)
	v20 := f.Add("switch_vlan", "v20")
	v20.Set("id", "20")
	v20.Set("ports", "ent-ws-"+tag)
	smb := f.Add("allow", "ws-to-srv-smb")
	smb.Set("src", "20")
	smb.Set("dest", "10")
	smb.Set("proto", "tcp")
	smb.Set("dest_port", "445")
	ssh := f.Add("allow", "ws-to-srv-ssh")
	ssh.Set("src", "20")
	ssh.Set("dest", "10")
	ssh.Set("proto", "tcp")
	ssh.Set("dest_port", "22")
	return f.Render()
}

// GenCity is the batch wrapper: fill the remaining residential lots and, if
// there is room and money left, raise one enterprise site beside them. It is
// deliberately thin — no city-wide simulator, no shared population — it just
// calls the two single-unit generators in order, so every dollar moves
// through the same real charge path and every cap still holds.
func (w *World) GenCity(payer string) ([]string, error) {
	acc := w.Bank.Accts[payer]
	if acc == nil {
		return nil, fmt.Errorf("no such developer: %s", payer)
	}
	hhRoom := genCap - w.GenSeq
	entRoom := genEntCap - w.EnterpriseSeq
	if hhRoom <= 0 && entRoom <= 0 {
		return nil, fmt.Errorf("nothing left to develop: the street and the business park are full")
	}
	// one shared money check before any real work: the cheapest lot the batch
	// could still build is what the developer must be able to afford
	cheapest := int64(genPriceCents)
	if hhRoom <= 0 {
		cheapest = genEntPriceCents
	}
	if acc.Balance < cheapest {
		return nil, fmt.Errorf("%s cannot cover the development fee", payer)
	}
	var made []string
	for w.GenSeq < genCap && acc.Balance >= genPriceCents {
		name, err := w.GenHousehold(payer)
		if err != nil {
			break
		}
		made = append(made, name)
	}
	if w.EnterpriseSeq < genEntCap && acc.Balance >= genEntPriceCents {
		if name, err := w.GenEnterprise(payer); err == nil {
			made = append(made, name)
		}
	}
	if len(made) == 0 {
		return nil, fmt.Errorf("%s cannot cover the development fee", payer)
	}
	return made, nil
}
