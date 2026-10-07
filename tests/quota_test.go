package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Assistant quota (§24) — fewer cores and less RAM than the hardware
// carries, enforced where the consequences live and reported where the
// operator looks. Each test walks a happy path, a boundary and a recovery.

func quotaNode(t *testing.T, w *core.World) *core.Device {
	t.Helper()
	a := w.Devices["asst-alex"]
	if a == nil {
		t.Fatal("no assistant node")
	}
	return a
}

// quotaAs runs node-policy verbs from the owner's PC: alex has no account
// on the assistant node itself, so quota is managed remotely like any owner
// policy (power, firewall), not from a local login.
func quotaAs(t *testing.T, w *core.World, line string) string {
	t.Helper()
	return run(t, w, w.Devices["pc-alex"], "alex", line)
}

func TestQuotaCapsMemoryAndReportsIt(t *testing.T) {
	w := core.NewWorld()
	asst := quotaNode(t, w)

	if out := quotaAs(t, w, "assist quota"); !strings.Contains(out, "uncapped") {
		t.Fatalf("a fresh node must be uncapped, got:\n%s", out)
	}
	if out := quotaAs(t, w, "assist quota --cpu 2 --mem 512"); !strings.Contains(out, "capped at") {
		t.Fatalf("setting quota failed:\n%s", out)
	}
	// the displays agree with the enforcement: 512, not 2048
	if out := run(t, w, asst, "root", "free"); !strings.Contains(out, "quota:") {
		t.Fatalf("free must name the quota:\n%s", out)
	}
	if out := run(t, w, asst, "root", "htop"); !strings.Contains(out, "[quota]") {
		t.Fatalf("htop must mark the cap:\n%s", out)
	}
	// 700 MiB on a 512 box: over RAM, into 256 swap, then the OOM killer —
	// the same chain as hardware, at the quota's numbers
	if out := run(t, w, asst, "root", "stress --vm 1 --vm-bytes 700M --timeout 40"); !strings.Contains(out, "700 MiB resident") {
		t.Fatalf("setup: stress must start:\n%s", out)
	}
	w.Tick()
	if asst.Resources().OOMCount == 0 {
		t.Fatalf("600 MiB on a 512-capped box must OOM (swap is %d)", asst.SwapTotalMB())
	}
	if asst.SwapTotalMB() != 256 {
		t.Fatalf("swap must be half the quota (256), got %d", asst.SwapTotalMB())
	}
	// recovery: clear returns the full hardware
	run(t, w, asst, "root", "pkill stress")
	if out := quotaAs(t, w, "assist quota clear"); !strings.Contains(out, "cleared") {
		t.Fatalf("clear failed:\n%s", out)
	}
	if asst.EffRAMMB() != asst.HW.RAMMB || asst.SwapTotalMB() != 1024 {
		t.Fatalf("clear must restore hardware numbers: %d/%d", asst.EffRAMMB(), asst.SwapTotalMB())
	}
}

func TestQuotaCapsCPUShare(t *testing.T) {
	w := core.NewWorld()
	asst := quotaNode(t, w)

	run(t, w, asst, "root", "stress --cpu 4 --timeout 60")
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	uncapped := asst.Resources().Share // 2 cores, 4 workers -> 0.5
	run(t, w, asst, "root", "pkill stress")

	quotaAs(t, w, "assist quota --cpu 1 --mem 2048")
	run(t, w, asst, "root", "stress --cpu 4 --timeout 60")
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if got := asst.Resources().Share; got != 0.25 {
		t.Fatalf("one capped core with four workers must share a quarter, got %.2f (uncapped %.2f)", got, uncapped)
	}
	run(t, w, asst, "root", "pkill stress")
}

func TestQuotaValidationAndAuthority(t *testing.T) {
	w := core.NewWorld()
	asst := quotaNode(t, w)
	pc := w.Devices["pc-alex"]

	// cannot allocate what does not exist
	if out := run(t, w, pc, "alex", "assist quota --cpu 99 --mem 1024"); !strings.Contains(out, "exceeds hardware") {
		t.Fatalf("over-hardware CPU must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "assist quota --cpu 1 --mem 99"); !strings.Contains(out, "too small") {
		t.Fatalf("tiny RAM must be refused, got:\n%s", out)
	}
	if asst.Quota != nil {
		t.Fatal("refused quotas must not land")
	}
	// a stranger's quota is not yours to set (blake has no assistant node
	// at all — either refusal proves the gate)
	run(t, w, pc, "alex", "player invite blake temp123")
	blake := w.Devices["pc-blake"]
	if out := run(t, w, blake, "blake", "assist quota --cpu 1 --mem 512"); !strings.Contains(out, "assist") {
		t.Fatalf("stranger quota must be refused, got:\n%s", out)
	}
}

func TestQuotaSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "assist quota --cpu 1 --mem 512")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := back.Devices["asst-alex"]
	if b.Quota == nil || b.Quota.RAMMB != 512 || b.EffRAMMB() != 512 {
		t.Fatalf("quota must survive the save: %+v", b.Quota)
	}
}

func TestQuotaGeneralizedAcrossHousehold(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// cap the household PC itself: 1 core of 4, 1 GiB of 8
	if out := run(t, w, pc, "alex", "quota home-pc --cpu 1 --mem 1024"); !strings.Contains(out, "capped at") {
		t.Fatalf("set failed:\n%s", out)
	}
	// someone else's box is not yours to cap
	run(t, w, pc, "alex", "player invite blake temp123")
	blake := w.Devices["pc-blake"]
	if out := run(t, w, blake, "blake", "quota home-pc --cpu 1 --mem 1024"); !strings.Contains(out, "only alex caps") {
		t.Fatalf("foreign caps must be refused, got:\n%s", out)
	}
	// the household table shows caps and load side by side
	if out := run(t, w, pc, "alex", "quota list"); !strings.Contains(out, "1.0/4") || !strings.Contains(out, "1024/8192M") {
		t.Fatalf("list must show the cap:\n%s", out)
	}
	// enforcement is the same machinery: 1500 MiB on a 1024-capped box pages
	// into its 512 swap and then kills
	if out := run(t, w, pc, "root", "stress --vm 1 --vm-bytes 1500M --timeout 40"); !strings.Contains(out, "1500 MiB resident") {
		t.Fatalf("setup: stress must start:\n%s", out)
	}
	w.Tick()
	if pc.Resources().OOMCount == 0 {
		t.Fatal("a capped box must OOM at its quota, not its hardware")
	}
	run(t, w, pc, "root", "pkill stress")
	// clear by name from anywhere owned
	if out := run(t, w, pc, "alex", "quota clear home-pc"); !strings.Contains(out, "cleared") {
		t.Fatalf("clear failed:\n%s", out)
	}
	if pc.EffRAMMB() != pc.HW.RAMMB {
		t.Fatal("clear must restore hardware")
	}
}
