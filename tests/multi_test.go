package tests

import (
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
