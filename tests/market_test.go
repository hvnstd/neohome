package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Black market (Phase 2) — the bazaar.
//
// Listings are verified before they go live, money moves through the same
// Transfer as everything else, and every trade files evidence. Each test
// walks a happy path, a boundary and a recovery.

func marketWorld(t *testing.T) *core.World {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w) // the bazaar is reached by name, like every other host
	return w
}

func marketBalances(w *core.World) (alex, devops, bazaar int64) {
	bal := func(name string) int64 {
		if a := w.Bank.Accts[name]; a != nil {
			return a.Balance
		}
		return 0
	}
	return bal("alex"), bal("devops"), bal("bazaar")
}

func TestMarketListsSeededGoods(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "alex", "market list")
	if !strings.Contains(out, "M-1") || !strings.Contains(out, "devops@darkden") {
		t.Fatalf("the seeded credential should be listed:\n%s", out)
	}
	if !strings.Contains(out, "M-2") || !strings.Contains(out, "clients.txt") {
		t.Fatalf("the seeded file drop should be listed:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "market info M-1"); !strings.Contains(out, "devops@darkden") {
		t.Fatalf("info should describe the goods:\n%s", out)
	}
	// the secret is not on display: info must not leak the password
	if out := run(t, w, pc, "alex", "market info M-1"); strings.Contains(out, "Summer2024!") {
		t.Fatalf("info must not reveal the password before purchase:\n%s", out)
	}
}

func TestMarketBuyCredentialEndToEnd(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	ax0, dv0, _ := marketBalances(w)

	out := run(t, w, pc, "alex", "market buy M-1")
	if !strings.Contains(out, "Summer2024!") {
		t.Fatalf("the purchase must reveal the password, got:\n%s", out)
	}
	// atomic swap at $50 with a 5% fee: buyer down 5000, seller +4750, house +250
	ax1, dv1, bz1 := marketBalances(w)
	if ax0-ax1 != 5000 {
		t.Fatalf("buyer should pay 5000 cents, paid %d", ax0-ax1)
	}
	if dv1-dv0 != 4750 {
		t.Fatalf("seller should net 4750 cents, got %d", dv1-dv0)
	}
	if bz1 != 250 {
		t.Fatalf("operator fee should be 250 cents, got %d", bz1)
	}
	// the password is live ammunition, not a souvenir
	if !w.Devices["npc-pc"].FindUser("devops").CheckPassword("Summer2024!") {
		t.Fatal("the bought password must really work on darkden")
	}
	// and the trade filed evidence under its own kind
	found := false
	for _, e := range w.Case.Events {
		if e.Kind == "market" && strings.Contains(e.Detail, "M-1") {
			found = true
		}
	}
	if !found {
		t.Fatal("the purchase must file market evidence")
	}
	// sold goods leave the board
	if out := run(t, w, pc, "alex", "market list"); strings.Contains(out, "M-1") {
		t.Fatalf("sold goods must leave the board:\n%s", out)
	}
}

func TestMarketStaleCredentialDelistsOnBuy(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	ax0, _, _ := marketBalances(w)

	// the neighbour hardens (the same rotation her heat reaction performs):
	// the listed password dies before anyone buys it
	npc := w.Devices["npc-pc"]
	if err := npc.ChangePassword("devops", "X7k!pLq92mz"); err != nil {
		t.Fatal(err)
	}
	out := run(t, w, pc, "alex", "market buy M-1")
	if !strings.Contains(out, "stale") || !strings.Contains(out, "no charge") {
		t.Fatalf("a rotated password must delist with no charge, got:\n%s", out)
	}
	ax1, _, _ := marketBalances(w)
	if ax1 != ax0 {
		t.Fatalf("no money must move on a delist: %d -> %d", ax0, ax1)
	}
	if out := run(t, w, pc, "alex", "market list"); strings.Contains(out, "M-1") {
		t.Fatalf("the stale listing must be gone:\n%s", out)
	}
}

func TestMarketSellUnverifiableIsRefused(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]

	// the household PC is NATed with no forward: the bazaar cannot reach it,
	// so the credential cannot be verified and is refused, not warehoused
	out := runWithStdin(t, w, pc, "alex", "market sell-cred alex@home-pc 20", "alex123")
	if !strings.Contains(out, "cannot verify") {
		t.Fatalf("an unreachable credential must be refused, got:\n%s", out)
	}
	// a reachable box with a wrong password fails the probe instead
	out = runWithStdin(t, w, pc, "alex", "market sell-cred devops@darkden 20 --ftp", "wrong-pass")
	if !strings.Contains(out, "verification failed") {
		t.Fatalf("a wrong password must fail the probe, got:\n%s", out)
	}
	if n := len(w.MarketLive()); n != 2 {
		t.Fatalf("refused listings must not land on the board, got %d", n)
	}
}

func TestMarketDeadDropFlowOverRealFTP(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]

	// the seller's side, with the same verbs as everything else: put the
	// file in the drop box over anonymous ftp, then list the bazaar path
	run(t, w, pc, "alex", "echo packing list > /home/alex/goods.txt")
	out := ftpExec(t, w, pc, "alex", "ftp -A bazaar.neohome.example",
		"put /home/alex/goods.txt goods.txt", "bye")
	if !strings.Contains(out, "226") && !strings.Contains(out, "complete") {
		t.Fatalf("ftp put to the drop box failed:\n%s", out)
	}
	bazaar := w.Devices["bazaar"]
	if _, ok := bazaar.FS.Read("/srv/bazaar/drops/goods.txt"); !ok {
		t.Fatal("the drop did not land on the bazaar")
	}
	out = run(t, w, pc, "alex", "market sell-file /srv/bazaar/drops/goods.txt 10")
	if !strings.Contains(out, "M-3") {
		t.Fatalf("the drop should list as M-3, got:\n%s", out)
	}
	// the buyer's side is a different account on a different machine: pay,
	// then fetch — the file flips world-readable on sale, which is what
	// makes the ftp get work
	asst := w.Devices["asst-alex"]
	out = run(t, w, asst, "assistant", "market buy M-3")
	if !strings.Contains(out, "now readable") {
		t.Fatalf("the purchase should open the drop, got:\n%s", out)
	}
	if n, _ := bazaar.FS.Get("/srv/bazaar/drops/goods.txt"); n.Mode.Perm()&0444 != 0444 {
		t.Fatalf("the sold drop must be world-readable, mode %v", n.Mode.Perm())
	}
	out = ftpExec(t, w, asst, "assistant", "ftp -A bazaar.neohome.example",
		"get goods.txt /home/assistant/bought.txt", "bye")
	if _, ok := asst.FS.Read("/home/assistant/bought.txt"); !ok {
		t.Fatalf("the buyer could not fetch the drop:\n%s", out)
	}
	// and the seller cannot buy their own goods
	if out := run(t, w, pc, "alex", "market buy M-3"); !strings.Contains(out, "no live listing") {
		t.Fatalf("sold goods must be gone, got:\n%s", out)
	}
}

func TestMarketDiesWithItsDaemon(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	bazaar := w.Devices["bazaar"]

	if out := run(t, w, pc, "alex", "market list"); !strings.Contains(out, "M-1") {
		t.Fatalf("setup: market should answer:\n%s", out)
	}
	bazaar.StopService("marketd")
	if out := run(t, w, pc, "alex", "market list"); !strings.Contains(out, "connect") {
		t.Fatalf("a stopped marketd must refuse connections, got:\n%s", out)
	}
}

func TestMarketCollectorBuysPlayerListings(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	bazaar := w.Devices["bazaar"]

	// a cheap player drop on the board
	bazaar.FS.Write("/srv/bazaar/drops/trinket.txt", "nothing much\n", 0600, "root", "root")
	out := run(t, w, pc, "alex", "market sell-file /srv/bazaar/drops/trinket.txt 5")
	if !strings.Contains(out, "M-") {
		t.Fatalf("setup: listing failed:\n%s", out)
	}
	mr0 := w.Bank.Accts["mara"].Balance
	// drive the clock to the collector's six-hour round
	w.TickCount = 719
	w.Tick()
	sold := false
	for _, l := range w.Market().Listings {
		if l.Sold && l.Buyer == "mara" && l.Seller == "alex" {
			sold = true
		}
	}
	if !sold {
		t.Fatal("the collector should have bought the cheapest player listing")
	}
	if w.Bank.Accts["mara"].Balance >= mr0 {
		t.Fatal("the collector must really pay")
	}
}

func TestMarketSurvivesSave(t *testing.T) {
	w := marketWorld(t)
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "market buy M-2")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back.Bazaar == nil || len(back.Bazaar.Listings) == 0 {
		t.Fatal("the market must survive the save")
	}
	sold := false
	for _, l := range back.Bazaar.Listings {
		if l.ID == "M-2" && l.Sold && l.Buyer == "alex" {
			sold = true
		}
		if l.ID == "M-1" && l.Pass != "Summer2024!" {
			t.Fatal("the listed secret must survive the save")
		}
	}
	if !sold {
		t.Fatal("the sale record must survive the save")
	}
}
