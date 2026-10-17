package tests

import (
	"fmt"
	"strings"
	"testing"

	"neohome/internal/core"
)

// Multiplayer citizens + async PvP (Phase 2) — roommates with their own
// machines, money and names on the evidence. The loop (§43) is emergent:
// every verb it needs already exists, so what is pinned here is that it
// works across two citizens with attribution both ways.

func TestInviteCitizen(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := w.Bank.Accts["alex"].Balance

	if out := run(t, w, pc, "alex", "player invite Blake temp123"); !strings.Contains(out, "bad player name") {
		t.Fatalf("uppercase names must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "player invite root temp123"); !strings.Contains(out, "reserved") {
		t.Fatalf("reserved names must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "player invite blake temp123"); !strings.Contains(out, "vouched") {
		t.Fatalf("invite failed:\n%s", out)
	}
	if got := before - w.Bank.Accts["alex"].Balance; got != 2000 {
		t.Fatalf("vouching should cost $20.00, cost %d", got)
	}
	p := w.Players["blake"]
	if p == nil || p.PC != "pc-blake" {
		t.Fatalf("no player record with a machine: %+v", p)
	}
	d := w.Devices["pc-blake"]
	if d == nil || d.Owner != "blake" || d.FindUser("blake") == nil {
		t.Fatal("the citizen has no real machine of their own")
	}
	if d.FindUser("blake").Pass != "temp123" {
		t.Fatal("the temporary password must work")
	}
	// the LAN resolver knows the newcomer, or nobody can ssh to them
	data, _ := w.Devices["router-alex"].FS.Read("/etc/hosts")
	if !strings.Contains(string(data), "blake-pc") {
		t.Fatalf("router /etc/hosts must carry the newcomer:\n%s", string(data))
	}
	// duplicates and broke inviters are refused
	if out := run(t, w, pc, "alex", "player invite blake temp123"); !strings.Contains(out, "already exists") {
		t.Fatalf("duplicate citizens must be refused, got:\n%s", out)
	}
	w.Bank.Accts["alex"].Balance = 100
	if out := run(t, w, pc, "alex", "player invite poor temp123"); !strings.Contains(out, "cannot cover") {
		t.Fatalf("a broke inviter must be refused, got:\n%s", out)
	}
}

func TestCitizenIsolation(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "player invite blake temp123")
	blake := w.Devices["pc-blake"]

	if out := run(t, w, blake, "blake", "whoami"); strings.TrimSpace(out) != "blake" {
		t.Fatalf("the citizen must land as themselves, got:\n%s", out)
	}
	// roommates on separate machines: alex's files are not on blake's disk
	// at all — isolation here is physical, enforced one layer down by the
	// same VFS permissions for the two accounts sharing one box
	run(t, w, pc, "alex", "echo secret > /home/alex/diary.txt && chmod 600 /home/alex/diary.txt")
	if out := run(t, w, blake, "blake", "cat /home/alex/diary.txt"); !strings.Contains(out, "No such file") {
		t.Fatalf("another citizen's disk must not be visible, got:\n%s", out)
	}
	if _, ok := blake.FS.Read("/home/alex/diary.txt"); ok {
		t.Fatal("the filesystems must be separate devices")
	}
	// a newcomer changes their temp password like anyone else
	out := runWithStdin(t, w, blake, "blake", "passwd", "temp123", "blake-pass", "blake-pass")
	if !strings.Contains(out, "password updated successfully") {
		t.Fatalf("passwd must work for citizens:\n%s", out)
	}
}

func TestBankTransferVerbs(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "player invite blake temp123")

	if out := run(t, w, pc, "alex", "bank transfer blake 30"); !strings.Contains(out, "transferred $30") {
		t.Fatalf("transfer failed:\n%s", out)
	}
	if w.Bank.Accts["blake"].Balance != 3000 {
		t.Fatalf("blake should hold 3000, holds %d", w.Bank.Accts["blake"].Balance)
	}
	if out := run(t, w, pc, "alex", "bank transfer nobody 1"); !strings.Contains(out, "no account") {
		t.Fatalf("typo payees must be refused, got:\n%s", out)
	}
	w.Bank.Accts["blake"].Balance = 100
	if out := run(t, w, w.Devices["pc-blake"], "blake", "bank transfer alex 30"); !strings.Contains(out, "insufficient") {
		t.Fatalf("overdrafts must be refused, got:\n%s", out)
	}
}

// Async PvP as §43 describes it, in miniature: A breaks into B's public
// node today, plants a tag, and the evidence names A; B reads it tomorrow,
// traces the origin, and tags A back. Ticks pass between the acts — that
// waiting is the "async".
func TestAsyncPvPLoopAcrossCitizens(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	repairDNS(t, w)
	run(t, w, pc, "alex", "player invite blake temp123")

	// both citizens rent public nodes and both, like real players, leave a
	// weak password on them (set directly here the way intrusion tests do)
	va, _, err := w.ProvisionVPS("alex", "nano-1", "alex-edge")
	if err != nil {
		t.Fatalf("alex vps: %v", err)
	}
	w.Transfer("alex", "blake", 10000, "seed money")
	vb, _, err := w.ProvisionVPS("blake", "nano-1", "blake-edge")
	if err != nil {
		t.Fatalf("blake vps: %v", err)
	}
	va.FindUser("deploy").Pass = "weak"
	vb.FindUser("deploy").Pass = "weak"

	// today: alex breaks into blake's node and plants a tag
	if _, _, msg := core.Dial(pc, vb.FirstWANIP(), 22); msg != "connected" {
		t.Fatalf("setup: blake's node should be reachable, got %s", msg)
	}
	if !core.AttackLogin(w, pc, vb, "deploy", "weak", "ssh", 22) {
		t.Fatal("setup: the weak password should work")
	}
	deployB := vb.FindUser("deploy")
	if err := vb.WriteGuest("/tmp/pwned-by-alex", []byte("alex was here\n"), deployB); err != nil {
		t.Fatalf("plant tag: %v", err)
	}
	// tomorrow (ticks pass): blake reads the evidence — it names alex
	for i := 0; i < 5; i++ {
		w.Tick()
	}
	namesAlex := false
	for _, e := range w.Case.Events {
		if e.Target == vb.ID && e.Actor == "alex" {
			namesAlex = true
		}
	}
	if !namesAlex {
		t.Fatal("the intrusion evidence must name the attacking citizen")
	}
	// and the origin traces to a node, never a person
	found := false
	for _, e := range w.Case.Events {
		if e.Target == vb.ID && e.Actor == "alex" && e.Origin != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("the evidence must carry the origin address for tracing")
	}
	// the counterattack: blake tags alex's node back through the same door
	if !core.AttackLogin(w, w.Devices["pc-blake"], va, "deploy", "weak", "ssh", 22) {
		t.Fatal("the counterattack login should work through the same weak door")
	}
	deployA := va.FindUser("deploy")
	if err := va.WriteGuest("/tmp/pwned-by-blake", []byte("blake was here\n"), deployA); err != nil {
		t.Fatalf("counter-tag: %v", err)
	}
	namesBlake := false
	for _, e := range w.Case.Events {
		if e.Target == va.ID && e.Actor == "blake" {
			namesBlake = true
		}
	}
	if !namesBlake {
		t.Fatal("the counterattack evidence must name blake")
	}
}

// ---- true multiple household subnets (§46) ----
//
// A citizen who wants their own home gets one: a free /24 out of the
// household pool, its own router and NAS, and a gateway that is theirs. The
// default house (house:alex, 10.77.1.0/24) is untouched, and the two families
// are isolated at the resolver and the packet path.

// ownHomeFounder provisions a second family and returns its household.
func ownHomeFounder(t *testing.T, w *core.World, name string) *core.Household {
	t.Helper()
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "alex", "player invite "+name+" temp123 --own-home"); !strings.Contains(out, "own home") {
		t.Fatalf("own-home invite failed:\n%s", out)
	}
	h := w.Households["house:"+name]
	if h == nil {
		t.Fatalf("no household registered for %s", name)
	}
	return h
}

func TestOwnHomeInviteCreatesSeparateFamily(t *testing.T) {
	w := core.NewWorld()
	before := w.Bank.Accts["alex"].Balance

	h := ownHomeFounder(t, w, "cara")

	if h.Founder != "cara" || h.Router != "router-cara" || h.NAS != "nas-cara" {
		t.Fatalf("new household mis-registered: %+v", h)
	}
	if h.Subnet == "" || h.Subnet == core.LANSubnet {
		t.Fatalf("own home must be on its own subnet, got %q", h.Subnet)
	}
	// its boxes really live on that /24, at the documented octets
	want := []struct{ id, octet string }{
		{"router-cara", "1"}, {"nas-cara", "2"}, {"pc-cara", "11"},
	}
	for _, x := range want {
		d := w.Devices[x.id]
		if d == nil {
			t.Fatalf("missing %s", x.id)
		}
		if got := d.Ifaces[0].IP; got != h.Subnet+x.octet {
			t.Fatalf("%s should be %s, got %s", x.id, h.Subnet+x.octet, got)
		}
		if got := d.LANBase(); got != h.Subnet {
			t.Fatalf("%s LANBase = %q, want %q", x.id, got, h.Subnet)
		}
	}
	// the citizen's PC routes and resolves through its own gateway
	gw := h.Subnet + "1"
	pcNew := w.Devices["pc-cara"]
	if got := pcNew.Ifaces[0].GW; got != gw {
		t.Fatalf("pc gateway = %q, want %q", got, gw)
	}
	if data, _ := pcNew.FS.Read("/etc/resolv.conf"); !strings.Contains(string(data), "nameserver "+gw) {
		t.Fatalf("pc resolv.conf must name its own gateway:\n%s", data)
	}
	// and the new router's resolver learned the newcomer
	if data, _ := w.Devices["router-cara"].FS.Read("/etc/hosts"); !strings.Contains(string(data), "cara-pc") {
		t.Fatalf("new router /etc/hosts must carry the newcomer:\n%s", data)
	}
	// the inviter's house is untouched on the default subnet
	if ah := w.Households["house:alex"]; ah == nil || ah.Subnet != core.LANSubnet {
		t.Fatalf("default house must stay on %s: %+v", core.LANSubnet, ah)
	}
	// money moved for real, and by more than a shared bed costs
	if got := before - w.Bank.Accts["alex"].Balance; got != 10000 {
		t.Fatalf("own home should cost $100.00, cost %d cents", got)
	}
}

func TestOwnHomeCrossFamilyIsolation(t *testing.T) {
	w := core.NewWorld()
	h := ownHomeFounder(t, w, "cara")
	caraGW := h.Subnet + "1"

	// the default router does not know the other family's hosts ...
	if data, _ := w.Devices["router-alex"].FS.Read("/etc/hosts"); strings.Contains(string(data), "cara-pc") {
		t.Fatalf("router-alex must not carry the other family's host:\n%s", data)
	}
	// ... and vice versa
	if data, _ := w.Devices["router-cara"].FS.Read("/etc/hosts"); strings.Contains(string(data), "home-pc") {
		t.Fatalf("router-cara must not carry the default family's host:\n%s", data)
	}
	// the same name resolves to each family's OWN box, never the other's
	if ip, ok, _ := core.DNSAnswer(w.Devices["router-alex"], "nas"); !ok || ip != w.Devices["nas-alex"].Ifaces[0].IP {
		t.Fatalf("default family must resolve its own nas, got %q ok=%v", ip, ok)
	}
	if ip, ok, _ := core.DNSAnswer(w.Devices["router-cara"], "nas"); !ok || ip != h.Subnet+"2" {
		t.Fatalf("new family must resolve its own nas, got %q ok=%v", ip, ok)
	}
	if _, ok, _ := core.DNSAnswer(w.Devices["router-cara"], "home-pc"); ok {
		t.Fatal("the new family must not resolve the default family's host")
	}
	// each family's gateway is its own
	if got := w.Devices["pc-cara"].Ifaces[0].GW; got != caraGW || got == core.LANGateway {
		t.Fatalf("new family gateway = %q, want %q", got, caraGW)
	}
	if got := w.Devices["pc-alex"].Ifaces[0].GW; got != core.LANGateway {
		t.Fatalf("default family gateway drifted: %q", got)
	}
}

func TestOwnHomeInternalResolutionAndReachability(t *testing.T) {
	w := core.NewWorld()
	h := ownHomeFounder(t, w, "cara")
	pc := w.Devices["pc-cara"]
	nas := w.Devices["nas-cara"]

	// names resolve inside the new family
	if ip, ok, _ := core.DNSAnswer(pc, "nas"); !ok || ip != nas.Ifaces[0].IP {
		t.Fatalf("nas should resolve to %s, got %q ok=%v", nas.Ifaces[0].IP, ip, ok)
	}
	// and its own boxes are reachable
	if st, ok := core.Reach(pc, h.Subnet+"1"); !ok {
		t.Fatalf("the new router should be reachable: %s", st)
	}
	if st, ok := core.Reach(pc, nas.Ifaces[0].IP); !ok {
		t.Fatalf("the new NAS should be reachable: %s", st)
	}
	// a client inside the family can open its gateway's ssh
	if _, _, msg := core.Dial(pc, h.Subnet+"1", 22); msg != "connected" {
		t.Fatalf("the new router's ssh should answer, got %s", msg)
	}
	// but the default family's gateway is a foreign private subnet: no route
	if st, ok := core.Reach(pc, core.LANGateway); ok {
		t.Fatalf("the new family must not reach the default gateway: %s", st)
	}
}

func TestValidateLANTwoFamilies(t *testing.T) {
	w := core.NewWorld()
	h := ownHomeFounder(t, w, "cara")
	// two families on two /24s is a valid plan
	w.ValidateLAN()

	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected a panic, got none", name)
			}
		}()
		fn()
	}

	// a static inside the NEW family's DHCP band must still panic
	w = core.NewWorld()
	h = ownHomeFounder(t, w, "cara")
	mustPanic("DHCP band in a second subnet", func() {
		pc := w.Devices["pc-cara"]
		pc.Ifaces = append(pc.Ifaces, &core.Iface{Name: "eth9", IP: h.Subnet + "60", Zone: "lan", Up: true, Mode: "static"})
		w.ValidateLAN()
	})

	// and a duplicate in the NEW family must panic too
	w = core.NewWorld()
	h = ownHomeFounder(t, w, "cara")
	mustPanic("duplicate in a second subnet", func() {
		pc := w.Devices["pc-cara"]
		pc.Ifaces = append(pc.Ifaces, &core.Iface{Name: "eth9", IP: h.Subnet + "11", Zone: "lan", Up: true, Mode: "static"})
		w.ValidateLAN()
	})
}

func TestOwnHomeSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	h := ownHomeFounder(t, w, "cara")
	base := h.Subnet
	// an old save has no Subnet recorded: the loader must backfill the default
	w.Households["house:alex"].Subnet = ""

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if bh := back.Households["house:cara"]; bh == nil || bh.Subnet != base {
		t.Fatalf("the new household's subnet must survive the save: %+v", bh)
	}
	if bh := back.Households["house:alex"]; bh == nil || bh.Subnet != core.LANSubnet {
		t.Fatalf("an old save must backfill the default subnet: %+v", bh)
	}
	p := back.Players["cara"]
	if p == nil || p.PC != "pc-cara" || p.Router != "router-cara" || p.NAS != "nas-cara" {
		t.Fatalf("the citizen must survive the save: %+v", p)
	}
	d := back.Devices["pc-cara"]
	if d == nil || d.Ifaces[0].IP != base+"11" {
		t.Fatalf("the citizen's address must survive the save: %+v", d)
	}
	// the reloaded world still resolves inside the new family
	if ip, ok, _ := core.DNSAnswer(back.Devices["pc-cara"], "nas"); !ok || ip != base+"2" {
		t.Fatalf("the reloaded world cannot resolve its own nas: %q ok=%v", ip, ok)
	}
}

func TestOwnHomeRefusalsMoveNoMoney(t *testing.T) {
	// a broke inviter cannot found a home — and nothing moves
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 100
	before := w.Bank.Accts["alex"].Balance
	if out := run(t, w, pc, "alex", "player invite cara temp123 --own-home"); !strings.Contains(out, "cannot cover") {
		t.Fatalf("a broke inviter must be refused, got:\n%s", out)
	}
	if w.Bank.Accts["alex"].Balance != before {
		t.Fatal("a refused own-home invite moved money")
	}
	if w.Players["cara"] != nil || w.Households["house:cara"] != nil {
		t.Fatal("a refused own-home invite created state")
	}

	// when every household /24 is taken the allocator refuses honestly
	w = core.NewWorld()
	pc = w.Devices["pc-alex"]
	w.Bank.Accts["alex"].Balance = 1 << 30
	for x := 1; x <= 254; x++ {
		w.IPMap[fmt.Sprintf("10.77.%d.9", x)] = "reserved"
	}
	before = w.Bank.Accts["alex"].Balance
	if out := run(t, w, pc, "alex", "player invite cara temp123 --own-home"); !strings.Contains(out, "no free household subnet") {
		t.Fatalf("a full subnet pool must be refused, got:\n%s", out)
	}
	if w.Bank.Accts["alex"].Balance != before {
		t.Fatal("a refused own-home invite (no subnet) moved money")
	}
}
