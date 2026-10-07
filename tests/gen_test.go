package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// World generator, part 1 (§44) — routine NPC housing on the next free
// street: deterministic (same world, same street), safe by default
// (nothing forwarded), paid for ($200 a lot). Each test walks a happy path,
// a boundary and a recovery.

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
		found := false
		for _, i := range d.Ifaces {
			if i.IP == ip {
				found = true
			}
		}
		if !found {
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
	w.Bank.Accts["alex"].Balance = 50000

	for i := 0; i < 4; i++ {
		if out := run(t, w, pc, "alex", "worldgen household"); !strings.Contains(out, "developed a household") {
			t.Fatalf("lot %d failed:\n%s", i+1, out)
		}
	}
	if out := run(t, w, pc, "alex", "worldgen household"); !strings.Contains(out, "street is full") {
		t.Fatalf("the fifth lot must be refused, got:\n%s", out)
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
