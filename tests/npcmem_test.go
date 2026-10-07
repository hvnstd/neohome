package tests

import (
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// NPC memory and relationships (§20/§21) — people remember who did what,
// and repeated acts move a pairwise standing that chat, trade and `people`
// all read back. Valence is explicit at each call site, never inferred.

// knock plants failed logins from a person-owned box at an NPC box.
func knock(t *testing.T, w *core.World, n int) {
	t.Helper()
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	for i := 0; i < n; i++ {
		core.AttackLogin(w, pc, npc, "devops", "wrong", "ftp", 21)
	}
}

func TestFailedLoginsAreRemembered(t *testing.T) {
	w := core.NewWorld()
	knock(t, w, 3)

	if got := w.Standing("mara", "alex"); got != -9 {
		t.Fatalf("three failures should stand at -9, got %d", got)
	}
	m := w.NPCMem["mara"]
	if m == nil || len(m.Events) != 3 {
		t.Fatalf("mara should remember three knocks, got %+v", m)
	}
	// a working login is a fact without a grudge: remembered, standing kept
	pc := w.Devices["pc-alex"]
	npc := w.Devices["npc-pc"]
	core.AttackLogin(w, pc, npc, "devops", "Summer2024!", "ftp", 21)
	if got := w.Standing("mara", "alex"); got != -9 {
		t.Fatalf("a clean login must not move the standing, got %d", got)
	}
	if len(w.NPCMem["mara"].Events) != 4 {
		t.Fatal("the clean login should still be remembered as a fact")
	}
}

func TestPublicVictimRemembersSweep(t *testing.T) {
	w := core.NewWorld()
	va, _, err := w.ProvisionVPS("alex", "nano-1", "alex-edge")
	if err != nil {
		t.Fatalf("vps: %v", err)
	}
	if svc := va.Svc("sshd"); svc == nil || svc.State != "running" {
		t.Fatalf("setup: sshd must answer on the node: %+v", svc)
	}
	sc := w.Devices[core.ScannerID]
	w.ScanTarget(sc, va)

	found := false
	for _, e := range w.NPCMem["alex"].Events {
		if e.Kind == "scan" {
			found = true
		}
	}
	if !found {
		t.Fatal("a swept box must remember the sweep, even from scan-host")
	}
	// scan-host is scenery, not a person: memory yes, standing no
	if got := w.Standing("alex", "scan-host"); got != 0 {
		t.Fatalf("non-persons hold no standing, got %d", got)
	}
}

func TestTradeBuildsStandingBothWays(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "market buy M-1")

	if got := w.Standing("devops", "alex"); got != 4 {
		t.Fatalf("a completed trade should stand at +4, got %d", got)
	}
	if len(w.NPCMem["devops"].Events) == 0 || len(w.NPCMem["alex"].Events) == 0 {
		t.Fatal("both sides should remember the trade")
	}
}

func TestCollectorRefusesKnownAttackers(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	bazaar := w.Devices["bazaar"]

	// clear the seeded board first: the collector must face only our
	// listing, or a legitimate purchase elsewhere proves nothing
	run(t, w, pc, "alex", "market buy M-1")
	run(t, w, pc, "alex", "market buy M-2")

	// earn the grudge the honest way: seventeen failed logins
	knock(t, w, 17)
	if got := w.Standing("mara", "alex"); got > -50 {
		t.Fatalf("setup: standing should be at most -50, got %d", got)
	}
	bazaar.FS.Write("/srv/bazaar/drops/trinket.txt", "nothing much\n", 0600, "root", "root")
	run(t, w, pc, "alex", "market sell-file /srv/bazaar/drops/trinket.txt 5")
	mr0 := w.Bank.Accts["mara"].Balance

	w.TickCount = 719
	w.Tick() // the collector's round: refusal, not purchase
	for _, l := range w.Market().Listings {
		if l.Seller == "alex" && l.Sold {
			t.Fatalf("the collector must not buy from a known attacker (%s went to %s)", l.ID, l.Buyer)
		}
	}
	if w.Bank.Accts["mara"].Balance != mr0 {
		t.Fatal("no money must move on a refusal")
	}
	data, _ := bazaar.FS.Read("/var/log/syslog")
	if !strings.Contains(string(data), "known attackers") {
		t.Fatalf("the refusal must be on the record:\n%s", string(data))
	}
}

func TestGrudgeIsSaidOutLoud(t *testing.T) {
	w := core.NewWorld()
	knock(t, w, 10) // standing -30: exactly the line
	w.Sim = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	n := len(w.Chat.History)
	w.IRCSend("alex", "#local", "zebra xylophone quartz")
	if len(w.Chat.History) != n+2 {
		t.Fatalf("a fresh grudge should get said out loud (got %d new lines)", len(w.Chat.History)-n)
	}
	last := w.Chat.History[len(w.Chat.History)-1]
	if last.Nick != "mara-bot" || !strings.Contains(last.Text, "watching you, alex") {
		t.Fatalf("the recall should come from mara-bot about alex: %+v", last)
	}
	// without a grudge the same line gets silence (daytime default branch)
	w2 := core.NewWorld()
	w2.Sim = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	n2 := len(w2.Chat.History)
	w2.IRCSend("alex", "#local", "zebra xylophone quartz")
	if len(w2.Chat.History) != n2+1 {
		t.Fatal("strangers get no recall post")
	}
}

func TestPeopleShowsStandings(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	knock(t, w, 3)

	out := run(t, w, pc, "alex", "people")
	if !strings.Contains(out, "mara") || !strings.Contains(out, "-9") {
		t.Fatalf("people must show their score toward you:\n%s", out)
	}
	if strings.Contains(out, "! mara") {
		t.Fatalf("a -9 is not yet a grudge (! needs -30):\n%s", out)
	}
	if out := run(t, w, pc, "alex", "people mara"); !strings.Contains(out, "them→you -9") {
		t.Fatalf("people NAME must detail both directions:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "people nobody"); !strings.Contains(out, "nobody by the name") {
		t.Fatalf("unknown handles must be refused, got:\n%s", out)
	}
}

func TestMemorySurvivesSave(t *testing.T) {
	w := core.NewWorld()
	knock(t, w, 3)

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := back.Standing("mara", "alex"); got != -9 {
		t.Fatalf("standing must survive the save, got %d", got)
	}
	if len(back.NPCMem["mara"].Events) != 3 {
		t.Fatal("memories must survive the save")
	}
}
