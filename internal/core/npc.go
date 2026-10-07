package core

import (
	"fmt"
	"time"
)

// NPCs live on a schedule. They are not task dispensers: they go to work,
// come home, patch their boxes — and the world state genuinely changes.

type Routine struct {
	Nick     string
	WorkAt   int // hour of day
	HomeAt   int
	DeviceID string
	Patches  bool
}

var routines = []Routine{
	{Nick: "mara", WorkAt: 9, HomeAt: 18, DeviceID: "npc-pc", Patches: true},
	{Nick: "devops", WorkAt: 10, HomeAt: 19, DeviceID: "npc-pc", Patches: true},
	{Nick: "mira-9", WorkAt: 11, HomeAt: 20, DeviceID: "npc-pc"},
}

// NPCSchedule advances NPC behaviour on the world clock.
func (w *World) NPCSchedule() {
	hour := w.Sim.Hour()
	for _, r := range routines {
		d := w.Devices[r.DeviceID]
		if d == nil {
			continue
		}
		atWork := hour >= r.WorkAt && hour < r.HomeAt
		state := "home"
		if atWork {
			state = "at work"
		}
		// transitions produce a real, observable event and change device state
		if d.Notes != state {
			prev := d.Notes
			d.Notes = state
			if prev != "" {
				w.AddEvent(d.ID, "info", "presence", "%s is now %s", r.Nick, state)
				if state == "home" && r.Patches {
					// coming home triggers a patch cycle: real version bumps
					w.NPCPatch(d, r.Nick)
				}
			}
		}
	}
}

// NPCPatch is routine maintenance, and it closes real vulnerabilities over time.
// Neglect it, and the box drifts out of date; keep it, and old holes disappear.
func (w *World) NPCPatch(d *Device, who string) {
	if d.Installed == nil {
		d.Installed = map[string]*VPkg{}
	}
	// the router firmware is the thing that actually matters here
	r := w.Devices["npc-router"]
	if r == nil {
		return
	}
	if r.OS.Ver == "1.0" {
		r.OS.Ver = "1.0.4"
		r.Logf("info", "sysupgrade", "firmware upgraded to 1.0.4 by %s (patched 3 CVEs)", who)
		w.AddEvent(r.ID, "info", "patch", "%s upgraded the router firmware; the old remote-admin hole is gone", who)
	}
}

// ---- economy: the household is not a money printer ----

// MarketTick charges real recurring costs. Unpaid bills have real consequences
// (throttling, then disconnection), never a random disaster.
func (w *World) MarketTick() {
	// every ~30 game-minutes is one "billing tick" in this compressed sim
	if w.TickCount%20 != 0 {
		return
	}
	for _, p := range w.Players {
		router := w.Devices[p.Router]
		if router == nil {
			continue
		}
		// electricity metering is already tracked on the device; convert to cents
		cents := int64(router.MeterKWh * 30) // household draw scaled to a bill
		for _, id := range []string{p.PC, p.Router, p.NAS, p.Assistant} {
			if d := w.Devices[id]; d != nil {
				cents += int64(d.MeterKWh * 20)
			}
		}
		if cents <= 0 {
			cents = 3
		}
		acc := w.Bank.Accts[p.Name]
		if acc == nil {
			continue
		}
		acc.Balance -= cents
		acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -cents, Memo: "utilities (power + ISP)", Balance: acc.Balance})
		if acc.Balance < 0 {
			w.UtilityTrouble(router, acc)
		}
	}
}

// UtilityTrouble: overdue bills degrade service in a visible, fixable way.
func (w *World) UtilityTrouble(router *Device, acc *Account) {
	if !w.UtilitiesSuspended {
		w.UtilitiesSuspended = true
		router.Logf("warn", "isp", "service suspended: household account in arrears")
		w.AddEvent(router.ID, "warn", "isp", "ISP suspended the household uplink — the account is in arrears")
		w.ChatPost("#local", "mira-9", "is anyone else's internet down? mine dropped an hour ago")
		// A suspended supply is a real outage, not just an ISP message: the
		// house loses mains and the machines go with it.
		w.darkenHousehold()
	}
}

// PayUtilities clears a suspension.
func (w *World) PayUtilities(who string) (int64, error) {
	p := w.Players[who]
	if p == nil {
		return 0, fmt.Errorf("no such player")
	}
	acc := w.Bank.Accts[who]
	if acc == nil {
		return 0, fmt.Errorf("no account")
	}
	due := int64(2000)
	if acc.Balance < due {
		return 0, fmt.Errorf("insufficient funds: %d cents available, %d due", acc.Balance, due)
	}
	acc.Balance -= due
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -due, Memo: "arrears settlement", Balance: acc.Balance})
	if w.UtilitiesSuspended {
		w.UtilitiesSuspended = false
		router := w.Devices[p.Router]
		if router != nil {
			router.Logf("info", "isp", "service restored after payment")
			w.AddEvent(router.ID, "info", "isp", "ISP restored the uplink after arrears were settled")
		}
		// power comes back with the payment, and so does the house
		w.PowerOutTicks = 0
		for _, id := range w.Order {
			d := w.Devices[id]
			if d.IsDataCenter() || d.MainsDropped || d.NetUp {
				continue
			}
			d.setPowered(true, "")
		}
	}
	return due, nil
}

// ---- VPS provisioning: a real node joins the world ----

// ProvisionVPS creates a genuine device with a public IP and OS, so it is
// immediately addressable, SSH-able and scan-able by everything else.
// ProvisionVPS is the default image: Debian, because that is what novapanel's
// smallest plan ships with.
func (w *World) ProvisionVPS(owner, plan, hostname string) (*Device, string, error) {
	return w.ProvisionVPSWithOS(owner, plan, hostname, "debian")
}

// ProvisionVPSWithOS buys a node with a chosen distribution (§12: the player
// picks the OS, and with it the package manager, the file layout and the
// service manager the box will have).
func (w *World) ProvisionVPSWithOS(owner, plan, hostname, distro string) (*Device, string, error) {
	return w.provisionVPS(owner, plan, hostname, distro, "")
}

// provisionVPS creates a node in a chosen region ("" = the plan's default). It
// is the single path a node is born through, so the panel's record of the
// machine and the machine itself can never disagree.
func (w *World) provisionVPS(owner, plan, hostname, distro, region string) (*Device, string, error) {
	p := w.Players[owner]
	if p == nil {
		return nil, "", fmt.Errorf("no such player: %s", owner)
	}
	var chosen *Plan
	for i := range w.Prov.Plans {
		if w.Prov.Plans[i].Name == plan {
			chosen = &w.Prov.Plans[i]
		}
	}
	if chosen == nil {
		return nil, "", fmt.Errorf("no such plan: %s", plan)
	}
	if hostname == "" {
		hostname = fmt.Sprintf("vps-%d", w.Prov.Issued+1)
	}
	acc := w.Bank.Accts[owner]
	if acc == nil {
		return nil, "", fmt.Errorf("no account")
	}
	if acc.Balance < chosen.Monthly {
		return nil, "", fmt.Errorf("insufficient funds: %d cents available, first month is %d", acc.Balance, chosen.Monthly)
	}
	for _, id := range w.Order {
		if d := w.Devices[id]; d.Hostname == hostname {
			return nil, "", fmt.Errorf("hostname already taken: %s", hostname)
		}
	}
	reg, err := w.RegionByName(orDefault(region, chosen.Region))
	if err != nil {
		return nil, "", err
	}
	acc.Balance -= chosen.Monthly
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -chosen.Monthly,
		Memo: "VPS " + hostname + " (" + plan + ", " + reg.Name + ")", Balance: acc.Balance})
	w.Prov.Issued++

	os := vpsImage(distro)
	hw := Hardware{Model: "Nova " + plan, Cores: chosen.Cores, CPUMHz: 2400, RAMMB: chosen.RAM, DiskMB: chosen.Disk, NetMbps: 1000}
	id := "vps-" + hostname
	d := w.addDevice(id, hostname, "vps", owner, os, hw, "")
	d.Boot = w.Sim
	d.Resources().PowerOnTicks = 0 // a fresh node has no lifetime yet (§36)
	// §13: the plan decides what the node's networking really is. A public plan
	// gets a routable IPv4; a shared plan gets an address inside the provider's
	// carrier-grade NAT pool, which is on the interface, in use, and *not*
	// routable — `whois` of it names the provider, and nothing can reach in;
	// a v6-only plan gets no IPv4 at all, so the node lives on v6 alone.
	pub := ""
	switch chosen.IPMode {
	case "shared":
		pub = w.AttachSharedWAN(d, "10.0.0.1")
	case "v6only":
		d.AttachWAN6Only("10.0.0.1")
	default:
		// the address comes out of the region's own block, so `whois` names
		// the datacenter the node was actually bought in
		pub = w.allocInAS(reg.ASN)
		d.AttachWAN(pub, "10.0.0.1")
	}
	if chosen.V6 {
		d.AddWANv6(w.allocV6InAS(reg.ASN))
	}
	seedFS(d, "vps")
	provisionPkgImage(w, d, distro)
	refreshPasswd(d)
	// a fresh VPS is reachable but exposes only sshd until the owner adds more
	d.Services["sshd"] = &Service{Name: "sshd", Desc: "OpenSSH", Port: 22, Proto: "tcp", Scope: "any",
		State: "running", Handler: "ssh", Banner: "SSH-2.0-OpenSSH_9.7"}
	// a cloud image ships root locked, with a sudo account to escalate: no
	// password means nobody logs in as root, and sudo still works.
	d.Users["root"] = &User{Name: "root", UID: 0, Groups: []string{"root"},
		Home: "/root", Shell: os.Shell}
	d.Users["deploy"] = &User{Name: "deploy", UID: 1000, Pass: randPass(hostname), Groups: []string{"deploy", "sudo"},
		Home: "/home/deploy", Shell: os.Shell}
	refreshPasswd(d)
	d.AddProc(&Proc{Name: "sshd", User: "root", CPU: 0.2, Mem: 20, TTY: "?", State: "S", Start: w.Sim, Svc: "sshd", Kind: "builtin"})

	// DNS registration: the node is now part of the observable internet. A
	// shared-address customer still gets a name — it resolves to the address
	// the node *has*, which is exactly why resolving it does not make it
	// reachable.
	who := pub
	if who == "" {
		who = d.FirstWANv6()
	}
	if pub != "" {
		w.Records = append(w.Records, DNSRecord{Name: hostname + ".neohome.example", IP: pub})
	}
	if v6 := d.FirstWANv6(); v6 != "" {
		w.Records = append(w.Records, DNSRecord{Name: hostname + ".neohome.example", IP: v6})
	}
	w.AddEvent(id, "info", "provider", "VPS %s provisioned: %s (%s, %s, %s)", hostname, who, plan, chosen.IPMode, reg.Name)
	// the panel's own record: what was bought, where it runs and how reverse
	// DNS names it (§12's rDNS), which `vps show` and `dig -x` then read
	w.recordNode(d, &NodeRecord{DeviceID: id, Plan: plan, Image: distro, Region: reg.Name,
		Datacenter: reg.Datacenter, RDNS: hostname + ".neohome.example", Monthly: chosen.Monthly, CreatedAt: w.Sim})
	creds := fmt.Sprintf("deploy@%s password: %s", hostname, d.Users["deploy"].Pass)
	return d, creds, nil
}

// vpsImage is the OS image each distribution ships as on novapanel.
func vpsImage(distro string) OSInfo {
	switch lower(distro) {
	case "alpine":
		return OSInfo{Distro: "Alpine", Ver: "3.20", Kernel: "6.6.20", Arch: "x86_64", Shell: "/bin/ash"}
	case "arch":
		return OSInfo{Distro: "Arch", Ver: "rolling", Kernel: "6.12.7", Arch: "x86_64", Shell: "/bin/bash"}
	case "fedora":
		return OSInfo{Distro: "Fedora", Ver: "40", Kernel: "6.9.7", Arch: "x86_64", Shell: "/bin/bash"}
	case "ubuntu":
		return OSInfo{Distro: "Ubuntu", Ver: "24.04", Kernel: "6.8.0", Arch: "x86_64", Shell: "/bin/bash"}
	}
	return OSInfo{Distro: "Debian", Ver: "13", Kernel: "6.12.5", Arch: "x86_64", Shell: "/bin/bash"}
}

// provisionPkgImage makes the new node's package management real: the image
// ships the archive key and the distribution's own sources configuration, but
// no cached index lists — exactly like a fresh cloud image, where the first
// thing you do is update.
func provisionPkgImage(w *World, d *Device, distro string) {
	spec := DistroFor(d)
	if spec == nil {
		return
	}
	d.FS.Write(spec.Keyring+"/neohome-archive.asc",
		KeyFile(ArchiveKeyFP, ArchiveKeyOwner, ArchiveKeyValidUntil,
			"signed package metadata for this world"), 0644, "root", "root")
	// the image's own sources configuration, written by the provider
	seedSources(d, spec)
}

func randPass(seed string) string {
	h := uint32(2166136261)
	for i := 0; i < len(seed); i++ {
		h ^= uint32(seed[i])
		h *= 16777619
	}
	// 32 characters: the index below is a bit-mask over 5 bits, so the
	// alphabet must be a power of two or a hash with the high bits set
	// indexes out of range.
	const alphabet = "abcdefghjkmnpqrstuvwxyz234567890"
	var b []byte
	for i := 0; i < 12; i++ {
		b = append(b, alphabet[(h>>uint(i*3))%32])
	}
	return string(b)
}

// ---- assistant upgrade: skills cost money and change its real behaviour ----

var assistantTracks = []struct {
	Name string
	Cost int64
	Desc string
}{
	{"dns-troubleshoot", 1200, "resolve resolver-chain faults without being told which file to edit"},
	{"sysadmin", 2400, "read service configs, restart what is broken, verify the fix"},
	{"networking", 3600, "reason about routes, NAT and firewall rules end-to-end"},
	{"security", 6000, "spot exposed services, weak credentials and risky port-forwards"},
}

// AssistantTracks exposes the catalogue.
func AssistantTracks() []struct {
	Name string
	Cost int64
	Desc string
} {
	return assistantTracks
}

// TrainAssistant buys a capability track for the household assistant.
func (w *World) TrainAssistant(owner, track string) (string, error) {
	p := w.Players[owner]
	if p == nil {
		return "", fmt.Errorf("no such player")
	}
	var chosen *struct {
		Name string
		Cost int64
		Desc string
	}
	for i := range assistantTracks {
		if assistantTracks[i].Name == track {
			chosen = &assistantTracks[i]
		}
	}
	if chosen == nil {
		return "", fmt.Errorf("no such track: %s", track)
	}
	if w.HasTrack(track) {
		return "", fmt.Errorf("assistant already knows %s", track)
	}
	acc := w.Bank.Accts[owner]
	if acc == nil || acc.Balance < chosen.Cost {
		return "", fmt.Errorf("insufficient funds: %s costs %d cents", track, chosen.Cost)
	}
	acc.Balance -= chosen.Cost
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -chosen.Cost, Memo: "assistant training: " + track, Balance: acc.Balance})
	w.assistTracks = append(w.assistTracks, track)
	w.assistSkills++
	a := w.Devices[p.Assistant]
	if a != nil {
		path := "/home/assistant/tasks.md"
		old, _ := a.FS.Read(path)
		a.FS.Write(path, string(old)+"learned: "+track+" — "+chosen.Desc+"\n", 0644, "assistant", "assistant")
		a.Logf("info", "assistant", "acquired capability track %s", track)
	}
	// a better assistant can actually do more: register the ability
	if track == "sysadmin" {
		w.assistantCanFix = true
	}
	return chosen.Desc, nil
}

// AssistantTracksLearned exposes what the assistant has acquired.
func (w *World) AssistantTracksLearned() []string { return w.assistTracks }

// HasTrack reports whether the assistant has learned a capability.
func (w *World) HasTrack(track string) bool {
	for _, t := range w.assistTracks {
		if t == track {
			return true
		}
	}
	return false
}

// ---- IRC: NPCs and the player in one real channel ----

// IRCNicks lists everyone currently on the network.
func (w *World) IRCNicks() []string {
	out := w.NPCNames
	for n := range w.Players {
		out = append(out, n)
	}
	return out
}

// IRCSend posts a message; NPCs may answer because they are "online people".
func (w *World) IRCSend(nick, ch, text string) {
	w.ChatPost(ch, nick, text)
	w.NPCRespond(ch, nick, text)
}

// NPCRespond: keyword-driven, state-aware replies — cheap, deterministic, and
// grounded in what is actually true in the world right now.
func (w *World) NPCRespond(ch, from, text string) {
	low := lower(text)
	hour := w.Sim.Hour()
	switch {
	case containsAny(low, "dns", "resolv", "nameserver", "解析"):
		if w.FaultDNSActive() {
			w.ChatPost(ch, "sysmods", "dnsmasq's resolv-file is pointing at a path that does not exist. check /etc/dnsmasq.conf")
		} else {
			w.ChatPost(ch, "sysmods", "resolver chain looks healthy from here — check your own /etc/resolv.conf first")
		}
	case containsAny(low, "job", "work", "hire", "任务", "赚钱"):
		w.ChatPost(ch, "daemon42", "there is a board at jobs.hiring.example — the neighbour job pays the same day if you actually fix it")
	case containsAny(low, "hack", "exploit", "scan", "黑"):
		w.ChatPost(ch, "mara-bot", "careful. my logs are watched, and my ISP is not as lazy as it looks")
	case containsAny(low, "vps", "provider", "server"):
		w.ChatPost(ch, "mira-9", "novapanel is cheap but the nano plan has 512MB — do not put a database on it")
	case containsAny(low, "hi", "hello", "hey", "你好"):
		w.ChatPost(ch, "daemon42", fmt.Sprintf("evening. %d people watching this channel, apparently", len(w.IRCNicks())))
	case containsAny(low, "help"):
		w.ChatPost(ch, "sysmods", "read the logs before you ask. /var/log/syslog exists for a reason")
	default:
		if hour >= 22 || hour < 6 {
			w.ChatPost(ch, "mira-9", "it is late here, whatever it is can probably wait until morning")
		}
	}
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

var _ = time.Now
