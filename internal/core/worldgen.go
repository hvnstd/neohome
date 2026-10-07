package core

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// World generator, part 1 (缺口 8b, §44) — new streets, not new rules
//
// Hand-seeded content stays hand-seeded; what generates is routine housing:
// an NPC household (router with public WAN, a PC, a phone, one tenant)
// on the next free 10.88.x.0/24, with working DNS, safe defaults (nothing
// forwarded — §14 holds for generated boxes exactly like seeded ones), a
// tenant handle the chat already answers to, and a BBS hello. Deterministic
// throughout: names rotate from fixed lists by counter, passwords derive
// from hashes, subnets allocate lowest-free — no dice anywhere, so the same
// world generates the same street.
//
// A household costs its developer $200: real money for real devices out of
// a finite street (four lots: .2–.5), which is the rate limit instead of a
// permission.
// ---------------------------------------------------------------------------

// genPriceCents is what wiring a new apartment costs the developer.
const genPriceCents = 10000

// genCap bounds generated households: four lots on the NPC street.
const genCap = 4

var genNames = []string{"nadia", "omar", "priya", "tomas", "yuki", "sam"}

// AllocNPCSubnet returns the next free 10.88.x.0/24 base ("10.88.3.") for a
// generated household, validated against every address in use. Subnet
// numbers have one owner (addr.go); this is its NPC-street allocator.
func (w *World) AllocNPCSubnet() (string, error) {
	for x := 2; x <= 9; x++ {
		base := fmt.Sprintf("10.88.%d.", x)
		clash := false
		for ip := range w.IPMap {
			if strings.HasPrefix(ip, base) {
				clash = true
				break
			}
		}
		if !clash {
			return base, nil
		}
	}
	return "", fmt.Errorf("no free street left (10.88.2-9 all taken)")
}

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
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
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
		num := fmt.Sprintf("555-02%02d", 10+w.GenSeq)
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
