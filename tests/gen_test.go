package tests

import (
	"fmt"
	"strings"
	"testing"

	"neohome/internal/core"
)

// World generator (§44) — content growth without new rules: routine NPC
// housing on the next free street and office sites on the next free
// business-park plot. Deterministic (same world, same street and same office
// park), safe by default (nothing forwarded), paid for ($100 a lot, $500 a
// site) and capped by the allocator's real capacity. Each test walks a happy
// path, a boundary and a recovery.

func ifaceHasIP(d *core.Device, ip string) bool {
	for _, i := range d.Ifaces {
		if i.IP == ip {
			return true
		}
	}
	return false
}

// vlanOf reports the tag of a host's own (first) interface.
func vlanOf(d *core.Device) int {
	if len(d.Ifaces) == 0 {
		return -1
	}
	return d.Ifaces[0].VLAN
}

// routerCarriesVLAN reports whether a gateway has a LAN interface in a tag.
func routerCarriesVLAN(d *core.Device, vlan int) bool {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.VLAN == vlan {
			return true
		}
	}
	return false
}

func TestWorldgenHousehold(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := w.Bank.Accts["alex"].Balance

	out := run(t, w, pc, "alex", "worldgen household")
	if !strings.Contains(out, "nadia") {
		t.Fatalf("first tenant should be nadia, got:\n%s", out)
	}
	if got := before - w.Bank.Accts["alex"].Balance; got != 10000 {
		t.Fatalf("development should cost $100.00, cost %d", got)
	}
	// three devices on 10.88.2.x, the first free street
	for id, ip := range map[string]string{
		"npc-router-nadia-1": "10.88.2.1", "npc-pc-nadia-1": "10.88.2.11", "phone-nadia-1": "10.88.2.50",
	} {
		d := w.Devices[id]
		if d == nil {
			t.Fatalf("missing generated device %s", id)
		}
		if !ifaceHasIP(d, ip) {
			t.Fatalf("%s should hold %s", id, ip)
		}
	}
	// the tenant is a person of the network now
	known := false
	for _, n := range w.NPCNames {
		if n == "nadia" {
			known = true
		}
	}
	if !known {
		t.Fatal("nadia should join NPCNames")
	}
	hello := false
	for _, p := range w.BBSList("general") {
		if p.From == "nadia" {
			hello = true
		}
	}
	if !hello {
		t.Fatal("the tenant should post a hello")
	}
	// ...with DNS that works out of the box (learned upstream, no fault)
	npc := w.Devices["npc-pc-nadia-1"]
	if out := run(t, w, npc, "nadia", "dig mirror.neohome.example"); !strings.Contains(out, "ANSWER SECTION") {
		t.Fatalf("generated DNS must resolve:\n%s", out)
	}
}

func TestWorldgenIsSafeByDefault(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "worldgen household")

	r := w.Devices["npc-router-nadia-1"]
	for _, red := range r.Redirects() {
		if red.Enabled {
			t.Fatalf("generated routers forward nothing: %+v", red)
		}
	}
	// the tenant password is derived, never the scanner's handful — the
	// world must not own itself at boot
	u := w.Devices["npc-pc-nadia-1"].FindUser("nadia")
	for _, pair := range [][2]string{{"root", "root"}, {"root", "123456"}, {"admin", "admin"}, {"root", "password"}, {"ubuntu", "ubuntu"}, {"pi", "raspberry"}} {
		if u.Pass == pair[1] {
			t.Fatalf("generated password collides with the scanner list: %q", u.Pass)
		}
	}
	// SMS registry knows the new phone
	phone := w.Devices["phone-nadia-1"]
	num := ""
	for n, id := range w.SMS.Numbers {
		if id == phone.ID {
			num = n
		}
	}
	if num == "" {
		t.Fatal("the generated phone must be registered on the cellular network")
	}
	if w.SMS.Battery[phone.ID] <= 0 {
		t.Fatal("the generated phone must have charge")
	}
}

func TestWorldgenLimits(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 200000

	for i := 0; i < 8; i++ {
		if out := run(t, w, pc, "alex", "worldgen household"); !strings.Contains(out, "developed a household") {
			t.Fatalf("lot %d failed:\n%s", i+1, out)
		}
	}
	if out := run(t, w, pc, "alex", "worldgen household"); !strings.Contains(out, "street is full") {
		t.Fatalf("the ninth lot must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "worldgen list"); !strings.Contains(out, "nadia") || !strings.Contains(out, "10.88.2.0/24") {
		t.Fatalf("list must show streets:\n%s", out)
	}
	// broke developers cannot build
	w.Bank.Accts["alex"].Balance = 100
	if out := run(t, w, pc, "alex", "worldgen household"); !strings.Contains(out, "cannot cover") {
		t.Fatalf("poverty must refuse, got:\n%s", out)
	}
}

// The residential cap is the allocator's real capacity: eight streets, each
// with its own tenant, address and phone — no two alike, and identical across
// two worlds.
func TestWorldgenResidentialCapacity(t *testing.T) {
	a := core.NewWorld()
	b := core.NewWorld()
	a.Bank.Accts["alex"].Balance = 200000
	b.Bank.Accts["alex"].Balance = 200000
	for i := 0; i < 8; i++ {
		run(t, a, a.Devices["pc-alex"], "alex", "worldgen household")
		run(t, b, b.Devices["pc-alex"], "alex", "worldgen household")
	}
	if len(a.GenNames) != 8 {
		t.Fatalf("eight lots should fit the street, got %d", len(a.GenNames))
	}
	seenName, seenStreet, seenNum := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, name := range a.GenNames {
		if seenName[name] {
			t.Fatalf("tenant name %q is reused on lot %d", name, i+1)
		}
		seenName[name] = true
		base := fmt.Sprintf("10.88.%d.", i+2)
		tag := fmt.Sprintf("%s-%d", name, i+1)
		pcA, pcB := a.Devices["npc-pc-"+tag], b.Devices["npc-pc-"+tag]
		if pcA == nil || !ifaceHasIP(pcA, base+"11") {
			t.Fatalf("lot %d (%s) should live on %s11", i+1, name, base)
		}
		if seenStreet[base] {
			t.Fatalf("street %s handed out twice", base)
		}
		seenStreet[base] = true
		pa, pb := pcA.FindUser(name).Pass, pcB.FindUser(name).Pass
		if pa == "" || pa != pb {
			t.Fatalf("two worlds must generate %s's password identically (%q vs %q)", name, pa, pb)
		}
	}
	// the phone registry holds one distinct number per generated tenant
	genPhones := map[string]bool{}
	for i, name := range a.GenNames {
		genPhones[fmt.Sprintf("phone-%s-%d", name, i+1)] = true
	}
	registered := 0
	for num, id := range a.SMS.Numbers {
		if !genPhones[id] {
			continue
		}
		if seenNum[num] {
			t.Fatalf("SMS number %s reused", num)
		}
		seenNum[num] = true
		registered++
	}
	if registered != 8 {
		t.Fatalf("eight generated phones expected on the network, got %d", registered)
	}
}

func TestWorldgenIsDeterministic(t *testing.T) {
	a := core.NewWorld()
	b := core.NewWorld()
	a.Bank.Accts["alex"].Balance = 50000
	b.Bank.Accts["alex"].Balance = 50000
	run(t, a, a.Devices["pc-alex"], "alex", "worldgen household")
	run(t, b, b.Devices["pc-alex"], "alex", "worldgen household")

	pa := a.Devices["npc-pc-nadia-1"].FindUser("nadia").Pass
	pb := b.Devices["npc-pc-nadia-1"].FindUser("nadia").Pass
	if pa != pb || pa == "" {
		t.Fatal("same world must generate the same street")
	}
	// and generation continues, never reuses, across a save
	path := t.TempDir() + "/w.gob"
	if err := a.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := run(t, back, back.Devices["pc-alex"], "alex", "worldgen household")
	if !strings.Contains(out, "omar") {
		t.Fatalf("second street should be omar, got:\n%s", out)
	}
	if back.Devices["npc-pc-omar-2"] == nil {
		t.Fatal("omar's household missing after load+generate")
	}
}

func TestWorldgenEnterprise(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 1000000
	before := w.Bank.Accts["alex"].Balance

	out := run(t, w, pc, "alex", "worldgen enterprise")
	if !strings.Contains(out, "kestrel") {
		t.Fatalf("the first office should be kestrel, got:\n%s", out)
	}
	if got := before - w.Bank.Accts["alex"].Balance; got != 50000 {
		t.Fatalf("a site should cost $500.00, cost %d", got)
	}
	// the lowest free site: servers 10.89.0.0/24, workstations 10.89.1.0/24
	for id, ip := range map[string]string{
		"ent-router-kestrel-1": "10.89.0.1",
		"ent-dc-kestrel-1":     "10.89.0.10",
		"ent-fs-kestrel-1":     "10.89.0.20",
		"ent-ws-kestrel-1":     "10.89.1.31",
	} {
		d := w.Devices[id]
		if d == nil {
			t.Fatalf("missing generated device %s", id)
		}
		if !ifaceHasIP(d, ip) {
			t.Fatalf("%s should hold %s, ifaces %+v", id, ip, d.Ifaces)
		}
	}
	// VLAN tags are L2 facts, set where the wire is plugged
	router := w.Devices["ent-router-kestrel-1"]
	if !routerCarriesVLAN(router, 10) || !routerCarriesVLAN(router, 20) {
		t.Fatalf("the gateway must carry both VLANs: %+v", router.Ifaces)
	}
	if vlanOf(w.Devices["ent-dc-kestrel-1"]) != 10 || vlanOf(w.Devices["ent-fs-kestrel-1"]) != 10 {
		t.Fatal("the servers belong on VLAN 10")
	}
	if vlanOf(w.Devices["ent-ws-kestrel-1"]) != 20 {
		t.Fatal("the workstation belongs on VLAN 20")
	}
	// real services really answer: the workstation reaches the file server's
	// ssh and smb, and the servers cannot initiate back to the workstations
	ws, fs := w.Devices["ent-ws-kestrel-1"], w.Devices["ent-fs-kestrel-1"]
	if _, _, msg := core.Dial(ws, "10.89.0.20", 22); msg != "connected" {
		t.Fatalf("workstation -> file server ssh should be allowed, got %q", msg)
	}
	if _, _, msg := core.Dial(ws, "10.89.0.20", 445); msg != "connected" {
		t.Fatalf("workstation -> file server smb should be allowed, got %q", msg)
	}
	if _, _, msg := core.Dial(fs, "10.89.1.31", 22); !strings.Contains(msg, "denied") {
		t.Fatalf("servers must not initiate to workstations, got %q", msg)
	}
	// the edge is a public NetCrest WAN address, default-deny, nothing forwarded
	if len(router.Redirects()) != 0 {
		t.Fatalf("generated offices publish nothing: %+v", router.Redirects())
	}
	wan := router.FirstWANIP()
	if !strings.HasPrefix(wan, "198.51.100.") {
		t.Fatalf("the office edge must carry a NetCrest public WAN address, got %q", wan)
	}
	if _, _, msg := core.Dial(w.Devices["mirror"], wan, 22); !strings.Contains(msg, "filtered") {
		t.Fatalf("the office WAN must be default-deny, got %q", msg)
	}
	// DNS resolves the office's own names through its gateway
	if out := run(t, w, ws, "admin", "dig fs.kestrel.example"); !strings.Contains(out, "10.89.0.20") {
		t.Fatalf("the office resolver must answer its own names:\n%s", out)
	}
	// the org is a person of the network now
	known := false
	for _, n := range w.NPCNames {
		if n == "kestrel" {
			known = true
		}
	}
	if !known {
		t.Fatal("kestrel should join NPCNames")
	}
	posted := false
	for _, p := range w.BBSList("general") {
		if p.From == "kestrel" {
			posted = true
		}
	}
	if !posted {
		t.Fatal("the office should announce itself on the BBS")
	}
}

func TestWorldgenEnterpriseCapsAndMoney(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 1000
	if out := run(t, w, pc, "alex", "worldgen enterprise"); !strings.Contains(out, "cannot cover") {
		t.Fatalf("poverty must refuse, got:\n%s", out)
	}
	w.Bank.Accts["alex"].Balance = 1000000
	for i := 0; i < 4; i++ {
		if out := run(t, w, pc, "alex", "worldgen enterprise"); !strings.Contains(out, "developed an office") {
			t.Fatalf("site %d failed:\n%s", i+1, out)
		}
	}
	if out := run(t, w, pc, "alex", "worldgen enterprise"); !strings.Contains(out, "business park is full") {
		t.Fatalf("the fifth site must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "worldgen list"); !strings.Contains(out, "kestrel") || !strings.Contains(out, "10.89.0.0/24") {
		t.Fatalf("list must show the enterprise park:\n%s", out)
	}
}

func TestWorldgenEnterpriseIsDeterministic(t *testing.T) {
	a := core.NewWorld()
	b := core.NewWorld()
	a.Bank.Accts["alex"].Balance = 1000000
	b.Bank.Accts["alex"].Balance = 1000000
	run(t, a, a.Devices["pc-alex"], "alex", "worldgen enterprise")
	run(t, b, b.Devices["pc-alex"], "alex", "worldgen enterprise")

	pa := a.Devices["ent-fs-kestrel-1"].FindUser("admin").Pass
	pb := b.Devices["ent-fs-kestrel-1"].FindUser("admin").Pass
	if pa != pb || pa == "" {
		t.Fatal("the same world must build the same office")
	}
	if a.Devices["ent-router-kestrel-1"].FirstWANIP() != b.Devices["ent-router-kestrel-1"].FirstWANIP() {
		t.Fatal("the same site must get the same public WAN address")
	}
	// generation continues across a save: the next site renumbers, never reuses
	path := t.TempDir() + "/w.gob"
	if err := a.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := run(t, back, back.Devices["pc-alex"], "alex", "worldgen enterprise")
	if !strings.Contains(out, "northwind") {
		t.Fatalf("the second site should be northwind, got:\n%s", out)
	}
	if d := back.Devices["ent-router-northwind-2"]; d == nil || !ifaceHasIP(d, "10.89.2.1") {
		t.Fatal("the next site must renumber to the next free server /24")
	}
	if d := back.Devices["ent-ws-northwind-2"]; d == nil || !ifaceHasIP(d, "10.89.3.31") {
		t.Fatal("the second site's workstation side must be the sibling /24")
	}
}

func TestWorldgenCity(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 200000
	before := w.Bank.Accts["alex"].Balance

	out := run(t, w, pc, "alex", "worldgen city")
	if !strings.Contains(out, "developed 9 site(s)") {
		t.Fatalf("city should fill the street and raise one office:\n%s", out)
	}
	if w.GenSeq != 8 || w.EnterpriseSeq != 1 {
		t.Fatalf("caps after one city: households %d/8, enterprises %d/4", w.GenSeq, w.EnterpriseSeq)
	}
	if got := before - w.Bank.Accts["alex"].Balance; got != 8*10000+50000 {
		t.Fatalf("city must charge exactly the units it built, charged %d", got)
	}
	// a second city cannot add households, but still raises an office
	out = run(t, w, pc, "alex", "worldgen city")
	if !strings.Contains(out, "developed 1 site(s)") {
		t.Fatalf("the second city should raise one office:\n%s", out)
	}
	if w.GenSeq != 8 || w.EnterpriseSeq != 2 {
		t.Fatalf("households stay capped: %d/8, enterprises %d/4", w.GenSeq, w.EnterpriseSeq)
	}
	// fill the park, then there is nothing left to develop
	w.Bank.Accts["alex"].Balance = 200000
	run(t, w, pc, "alex", "worldgen city")
	w.Bank.Accts["alex"].Balance = 200000
	run(t, w, pc, "alex", "worldgen city")
	if w.EnterpriseSeq != 4 {
		t.Fatalf("the park should be full at four sites, got %d", w.EnterpriseSeq)
	}
	if out := run(t, w, pc, "alex", "worldgen city"); !strings.Contains(out, "nothing left to develop") {
		t.Fatalf("a full world must refuse a city, got:\n%s", out)
	}
	// and a broke developer cannot start one at all
	x := core.NewWorld()
	x.Bank.Accts["alex"].Balance = 100
	if out := run(t, x, x.Devices["pc-alex"], "alex", "worldgen city"); !strings.Contains(out, "cannot cover") {
		t.Fatalf("poverty must refuse a city, got:\n%s", out)
	}
}
