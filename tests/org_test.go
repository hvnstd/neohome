package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Organisations / clans (Phase 2) — a name, a roster and a real treasury.
// Money only enters through contributions; spending it is the owner's
// signature; contracts escrow real funds and pay from the hold, never
// minted. Each test walks a happy path, a boundary and a recovery.

func TestOrgLifecycle(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// a second citizen to crew with (vouched by alex, who pays the setup)
	if out := run(t, w, pc, "alex", "player invite blake temp123"); !strings.Contains(out, "vouched") {
		t.Fatalf("invite failed:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "org create redcell"); !strings.Contains(out, "founded") {
		t.Fatalf("create failed:\n%s", out)
	}
	// joining without an invitation is refused
	if out := run(t, w, w.Devices["pc-blake"], "blake", "org join redcell"); !strings.Contains(out, "no invitation") {
		t.Fatalf("join without invite must be refused, got:\n%s", out)
	}
	run(t, w, pc, "alex", "org invite redcell blake")
	if out := run(t, w, w.Devices["pc-blake"], "blake", "org join redcell"); !strings.Contains(out, "welcome") {
		t.Fatalf("join with invite should work, got:\n%s", out)
	}
	// money: the inviter funds the newcomer, members fund the crew,
	// only the owner spends
	run(t, w, pc, "alex", "bank transfer blake 10")
	if out := run(t, w, w.Devices["pc-blake"], "blake", "org contribute redcell 3"); strings.Contains(out, "insufficient") {
		t.Fatalf("contribution should work after funding, got:\n%s", out)
	}
	if out := run(t, w, w.Devices["pc-blake"], "blake", "org withdraw redcell 1"); !strings.Contains(out, "only the owner") {
		t.Fatalf("member withdrawal must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "org withdraw redcell 1"); !strings.Contains(out, "withdrew") {
		t.Fatalf("owner withdrawal should work, got:\n%s", out)
	}
	// kicking is owner-only and never the owner
	if out := run(t, w, w.Devices["pc-blake"], "blake", "org kick redcell alex"); !strings.Contains(out, "only the owner") {
		t.Fatalf("member kick must be refused, got:\n%s", out)
	}
	run(t, w, pc, "alex", "org kick redcell blake")
	if out := run(t, w, pc, "alex", "org info redcell"); strings.Contains(out, "blake") && strings.Contains(out, "member") {
		t.Fatalf("kicked member must be gone:\n%s", out)
	}
	// the owner cannot abandon a crew with members in it
	run(t, w, pc, "alex", "org invite redcell blake")
	run(t, w, w.Devices["pc-blake"], "blake", "org join redcell")
	if out := run(t, w, pc, "alex", "org leave redcell"); !strings.Contains(out, "while members remain") {
		t.Fatalf("owner leaving a crewed org must be refused, got:\n%s", out)
	}
}

func TestOrgContractPaysFromHold(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "org create redcell")

	// broke crews cannot post: the hold must exist before the promise
	if out := run(t, w, pc, "alex", "org post redcell survey 50 ssh-up"); !strings.Contains(out, "treasury holds") {
		t.Fatalf("posting without funds must be refused, got:\n%s", out)
	}
	run(t, w, pc, "alex", "org contribute redcell 150")
	before := orgMoney(w)

	out := run(t, w, pc, "alex", "org post redcell survey 50 ssh-up")
	if !strings.Contains(out, "escrowed") {
		t.Fatalf("post should escrow, got:\n%s", out)
	}
	// conservation: posting moves counter to hold, the bank keeps the funds
	o := w.Clans["redcell"]
	if o.Hold["J-111"] != 5000 {
		t.Fatalf("hold should be 5000, got %d", o.Hold["J-111"])
	}
	// sshd runs on several boxes already, so the survey completes — paid
	// from the hold, not minted: total money is conserved
	run(t, w, pc, "alex", "job accept J-111")
	beforePay := orgMoney(w)
	out = run(t, w, pc, "alex", "job pay J-111")
	if strings.Contains(out, "cannot settle") {
		t.Fatalf("contract should pay, got:\n%s", out)
	}
	if after := orgMoney(w); after != beforePay {
		t.Fatalf("contract pay must conserve money: %d -> %d", beforePay, after)
	}
	if _, ok := o.Hold["J-111"]; ok {
		t.Fatal("the hold must be released on payout")
	}
	_ = before
}

func orgMoney(w *core.World) int64 {
	var total int64
	for _, a := range w.Bank.Accts {
		total += a.Balance
	}
	return total
}

func TestOrgCancelRefunds(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "org create redcell")
	run(t, w, pc, "alex", "org contribute redcell 100")

	run(t, w, pc, "alex", "org post redcell survey 50 ssh-up")
	o := w.Clans["redcell"]
	if o.Treasury != 5000 {
		t.Fatalf("treasury counter should be 5000 after escrow, got %d", o.Treasury)
	}
	if out := run(t, w, pc, "alex", "org cancel redcell J-111"); !strings.Contains(out, "refunded") {
		t.Fatalf("cancel should refund, got:\n%s", out)
	}
	if o.Treasury != 10000 {
		t.Fatalf("treasury should be whole again, got %d", o.Treasury)
	}
	if out := run(t, w, pc, "alex", "job list"); !strings.Contains(out, "cancelled") {
		t.Fatalf("cancelled contracts must read cancelled, never paid:\n%s", out)
	}
	// taken work cannot be cancelled out from under its worker
	run(t, w, pc, "alex", "org post redcell survey2 10 ssh-up")
	run(t, w, pc, "alex", "job accept J-112")
	if out := run(t, w, pc, "alex", "org cancel redcell J-112"); !strings.Contains(out, "taken by") {
		t.Fatalf("cancelling taken work must be refused, got:\n%s", out)
	}
}

func TestOrgTrophyContractEndToEnd(t *testing.T) {
	w := marketWorld(t)
	// the seeded midnight contract: tag /tmp/pwned on darkden, reachable
	// today over anonymous ftp
	if out := run(t, w, w.Devices["pc-alex"], "alex", "job show J-110"); !strings.Contains(out, "trophy npc-pc /tmp/pwned") {
		t.Fatalf("seeded trophy contract missing:\n%s", out)
	}
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "job accept J-110")
	if out := run(t, w, pc, "alex", "job pay J-110"); !strings.Contains(out, "cannot settle") {
		t.Fatalf("an untagged box must fail the bounty, got:\n%s", out)
	}
	// plant the tag the way an intruder would: anonymous ftp into the drop
	// box's own /tmp (world-writable, like every /tmp)
	run(t, w, pc, "alex", "echo alex was here > /home/alex/tag.txt")
	pub := npcPublicIP(w)
	out := ftpExec(t, w, pc, "alex", "ftp -A "+pub, "put /home/alex/tag.txt /tmp/pwned", "bye")
	if !strings.Contains(out, "226") && !strings.Contains(out, "complete") {
		t.Fatalf("tag plant failed:\n%s", out)
	}
	mr0 := w.Bank.Accts["org:midnight"].Balance
	ax0 := w.Bank.Accts["alex"].Balance
	out = run(t, w, pc, "alex", "job pay J-110")
	if strings.Contains(out, "cannot settle") {
		t.Fatalf("a tagged box must pay the bounty, got:\n%s", out)
	}
	if got := w.Bank.Accts["alex"].Balance - ax0; got != 10000 {
		t.Fatalf("bounty should pay 10000 from hold, paid %d", got)
	}
	if got := mr0 - w.Bank.Accts["org:midnight"].Balance; got != 10000 {
		t.Fatalf("treasury bank must fund the bounty, moved %d", got)
	}
	// removing the tag un-completes the work — but the payout stands,
	// because money moved when the world said so
	npc := w.Devices["npc-pc"]
	npc.FS.Remove("/tmp/pwned")
	_ = out
}
