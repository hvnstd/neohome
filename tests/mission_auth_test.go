package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Player-authored missions (§44 community format) — post with escrow,
// complete against world state, cancel with refund, share as text. Money is
// conserved at every step: escrow deducts first, payouts release it.

func TestJobNewPaysFromEscrow(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := orgMoney(w)
	alexBefore := w.Bank.Accts["alex"].Balance

	// broke authors cannot post: the promise needs funding first
	w.Bank.Accts["alex"].Balance = 100
	if out := run(t, w, pc, "alex", "job new --title survey --pay 50 --verify ssh-up"); !strings.Contains(out, "cannot escrow") {
		t.Fatalf("unfunded posts must be refused, got:\n%s", out)
	}
	// unknown verifiers are refused at authoring, never as dead promises
	w.Bank.Accts["alex"].Balance = alexBefore
	if out := run(t, w, pc, "alex", "job new --title survey --pay 50 --verify teleport-home"); !strings.Contains(out, "unknown verifier") {
		t.Fatalf("unknown verifiers must be refused, got:\n%s", out)
	}
	out := run(t, w, pc, "alex", "job new --title survey --pay 50 --verify ssh-up --help 'find an sshd'")
	if !strings.Contains(out, "J-111") || !strings.Contains(out, "escrowed") {
		t.Fatalf("post should escrow as J-111, got:\n%s", out)
	}
	if got := before - orgMoney(w); got != 5000 {
		t.Fatalf("posting must hold 5000 out of circulation, moved %d", got)
	}
	if j := w.Job("J-111"); j.Hold != 5000 || j.Funder != "alex" {
		t.Fatalf("the hold must record funder and amount: %+v", j)
	}
	// sshd runs on several boxes already: accept and collect from the hold
	run(t, w, pc, "alex", "job accept J-111")
	out = run(t, w, pc, "alex", "job pay J-111")
	if strings.Contains(out, "cannot settle") {
		t.Fatalf("escrowed pay should work, got:\n%s", out)
	}
	if got := orgMoney(w) - before; got != 0 {
		t.Fatalf("escrow payout must conserve money, moved %d", got)
	}
	j := w.Job("J-111")
	if j.Hold != 0 || !j.Done {
		t.Fatalf("the hold must release on payout: %+v", j)
	}
}

func TestJobCancelRefunds(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := orgMoney(w)

	run(t, w, pc, "alex", "job new --title survey --pay 40 --verify ssh-up")
	if out := run(t, w, pc, "alex", "job cancel J-111"); !strings.Contains(out, "refunded") {
		t.Fatalf("cancel should refund, got:\n%s", out)
	}
	if got := orgMoney(w) - before; got != 0 {
		t.Fatalf("cancel must conserve money, moved %d", got)
	}
	if out := run(t, w, pc, "alex", "job list"); !strings.Contains(out, "cancelled") {
		t.Fatalf("cancelled missions must read cancelled:\n%s", out)
	}
	// taken work cannot be cancelled out from under its worker
	run(t, w, pc, "alex", "job new --title survey2 --pay 10 --verify ssh-up")
	run(t, w, pc, "alex", "job accept J-112")
	if out := run(t, w, pc, "alex", "job cancel J-112"); !strings.Contains(out, "taken by") {
		t.Fatalf("cancelling taken work must be refused, got:\n%s", out)
	}
}

func TestJobExportImportRoundTrip(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "alex", "job show J-108")
	if !strings.Contains(out, "Requires") && !strings.Contains(out, "requires") {
		t.Fatalf("show must render the template:\n%s", out)
	}
	exp, err := w.JobExport("J-108")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(exp, "stage: link | ssh-up | 15 |") {
		t.Fatalf("export must carry stages:\n%s", exp)
	}
	// import funds a fresh copy under a new ID, paid by the importer
	n := len(w.Jobs.List)
	got := runWithStdin(t, w, pc, "alex", "job import", strings.Split(exp, "\n")...)
	if !strings.Contains(got, "imported and posted") {
		t.Fatalf("import failed:\n%s", got)
	}
	if len(w.Jobs.List) != n+1 {
		t.Fatal("import must add exactly one mission")
	}
	cp := w.Jobs.List[len(w.Jobs.List)-1]
	if len(cp.Stages) != 3 || cp.Hold != 5000 || cp.Funder != "alex" {
		t.Fatalf("imported mission must mirror the template with escrow: %+v", cp)
	}
	// garbage in, refusal out — never a half-parsed mission
	bad := runWithStdin(t, w, pc, "alex", "job import", "mission: x", "pay: nope", ".")
	if !strings.Contains(bad, "bad pay") {
		t.Fatalf("bad pay must be refused, got:\n%s", bad)
	}
	if len(w.Jobs.List) != n+1 {
		t.Fatal("a refused import must add nothing")
	}
	bad = runWithStdin(t, w, pc, "alex", "job import", "mission: x", "pay: 5", "verify: teleport-home", ".")
	if !strings.Contains(bad, "unknown verifier") {
		t.Fatalf("bad verifier must be refused, got:\n%s", bad)
	}
}
