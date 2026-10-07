package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Staged missions (Phase 2 + §44) — ordered predicates, prerequisites and
// installments on top of the verifiers the world already speaks. Each test
// walks a happy path, a boundary and a recovery; legacy one-shot jobs are
// covered by the existing job tests and must not change behaviour.

func fixDNS(t *testing.T, w *core.World) {
	t.Helper()
	router := w.Devices["router-alex"]
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	router.FS.Write("/etc/dnsmasq.conf",
		strings.Replace(string(data), "/var/run/dnsmasq/resolv.conf", "/etc/dnsmasq.upstream", 1), 0644, "root", "root")
	router.RestartService("dnsmasq")
}

func TestMissionRequiresGateAccept(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// J-108 needs J-101 first: taking it early is the refusal
	if out := run(t, w, pc, "alex", "job accept J-108"); !strings.Contains(out, "requires J-101") {
		t.Fatalf("accept before the prerequisite must name it, got:\n%s", out)
	}
	// complete the prerequisite for real, then the gate opens
	if err := w.AcceptJob("alex", "J-101"); err != nil {
		t.Fatalf("accept J-101: %v", err)
	}
	fixDNS(t, w)
	if _, _, err := w.PayJob("alex", "J-101"); err != nil {
		t.Fatalf("pay J-101: %v", err)
	}
	if out := run(t, w, pc, "alex", "job accept J-108"); !strings.Contains(out, "accepted J-108") {
		t.Fatalf("accept after the prerequisite should work, got:\n%s", out)
	}
	// a staged job never pays lump-sum
	if out := run(t, w, pc, "alex", "job pay J-108"); !strings.Contains(out, "job advance") {
		t.Fatalf("lump-sum pay of a staged job must redirect to advance, got:\n%s", out)
	}
	// three ordered installments, each verified against live state
	before := w.Bank.Accts["alex"].Balance
	for i, want := range []int64{1500, 2500, 1000} {
		out := run(t, w, pc, "alex", "job advance J-108")
		if strings.Contains(out, "cannot advance") {
			t.Fatalf("stage %d should advance in a healthy world, got:\n%s", i+1, out)
		}
		if got := w.Bank.Accts["alex"].Balance - before; got <= 0 {
			t.Fatalf("stage %d paid nothing", i+1)
		}
		before = w.Bank.Accts["alex"].Balance
		_ = want
	}
	j := w.Job("J-108")
	if !j.Done || j.StageIdx != 3 {
		t.Fatalf("the mission should be complete at stage 3, done=%v idx=%d", j.Done, j.StageIdx)
	}
	if out := run(t, w, pc, "alex", "job show J-108"); !strings.Contains(out, "stage 3 [x]") {
		t.Fatalf("show must mark finished stages:\n%s", out)
	}
}

func TestMissionStagesRunInOrder(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-T1", Title: "test mission", Client: "test", Pay: 3000, Tier: 1, Target: "self",
		Stages: []core.MissionStage{
			{Name: "tidy", Help: "no bans on the router", Verify: "clean", Pay: 1000},
			{Name: "link", Help: "an sshd answers", Verify: "ssh-up", Pay: 2000},
		},
	})
	// plant the failure the first stage checks: a live ban on the router
	router.Fail2Ban["9.9.9.9"] = 2
	if err := w.AcceptJob("alex", "J-T1"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	before := w.Bank.Accts["alex"].Balance

	out := run(t, w, pc, "alex", "job advance J-T1")
	if !strings.Contains(out, "cannot advance") || !strings.Contains(out, "banning") {
		t.Fatalf("a failing stage must refuse with the world's reason, got:\n%s", out)
	}
	if w.Bank.Accts["alex"].Balance != before {
		t.Fatal("a refused advance must move no money")
	}
	// recovery: clear the ban for real, and the stage pays exactly its share
	delete(router.Fail2Ban, "9.9.9.9")
	out = run(t, w, pc, "alex", "job advance J-T1")
	if strings.Contains(out, "cannot advance") {
		t.Fatalf("the stage should advance once fixed, got:\n%s", out)
	}
	if got := w.Bank.Accts["alex"].Balance - before; got != 1000 {
		t.Fatalf("stage one should pay 1000, paid %d", got)
	}
	// the second stage is checked only now — order is enforced by the index,
	// not by asking
	out = run(t, w, pc, "alex", "job advance J-T1")
	if strings.Contains(out, "cannot advance") {
		t.Fatalf("stage two should advance, got:\n%s", out)
	}
	if got := w.Bank.Accts["alex"].Balance - before; got != 3000 {
		t.Fatalf("both stages should total 3000, paid %d", got)
	}
	j := w.Job("J-T1")
	if !j.Done || j.StageIdx != 2 {
		t.Fatalf("the mission should be done, done=%v idx=%d", j.Done, j.StageIdx)
	}
	if _, _, err := w.PayJob("alex", "J-T1"); err == nil {
		t.Fatal("double payout through the legacy path must be refused")
	}
}

func TestMissionAssistantWorksStages(t *testing.T) {
	w := core.NewWorld()
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-T2", Title: "assistant test mission", Client: "test", Pay: 2000, Tier: 1, Target: "self",
		Stages: []core.MissionStage{
			{Name: "tools", Help: "busybox on the assistant node", Verify: "pkg-busybox", Pay: 500},
			{Name: "net", Help: "the resolver fixed", Verify: "dns-fix", Pay: 1500},
		},
	})
	// a stage the assistant cannot perform is refused before it is ever
	// delegated, naming the stage
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-T3", Title: "undoable", Client: "test", Pay: 999, Tier: 9, Target: "self",
		Stages: []core.MissionStage{
			{Name: "serve", Help: "web up", Verify: "web-up", Pay: 999},
		},
	})
	if err := w.TaskAssistant("J-T3"); err == nil || !strings.Contains(err.Error(), "serve") {
		t.Fatalf("delegation of an undoable stage must name it, err=%v", err)
	}

	before := w.Bank.Accts["alex"].Balance
	skills := w.AssistantSkill()
	if err := w.TaskAssistant("J-T2"); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	// two stages need two work cycles of six ticks each
	for i := 0; i < 16; i++ {
		w.Tick()
	}
	j := w.Job("J-T2")
	if !j.Done || j.StageIdx != 2 {
		t.Fatalf("the assistant should finish both stages, done=%v idx=%d", j.Done, j.StageIdx)
	}
	if got := w.Bank.Accts["alex"].Balance - before; got != 2000 {
		t.Fatalf("staged assistant pay should total 2000, paid %d", got)
	}
	if w.AssistantSkill() != skills+1 {
		t.Fatal("finishing a mission should grow the assistant once, not per stage")
	}
	if w.FaultDNSActive() {
		t.Fatal("the dns-fix stage should have repaired the fault for real")
	}
}

func TestMissionProgressSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-T1", Title: "test mission", Client: "test", Pay: 3000, Tier: 1, Target: "self",
		Stages: []core.MissionStage{
			{Name: "tidy", Help: "no bans", Verify: "clean", Pay: 1000},
			{Name: "link", Help: "sshd", Verify: "ssh-up", Pay: 2000},
		},
	})
	if err := w.AcceptJob("alex", "J-T1"); err != nil {
		t.Fatal(err)
	}
	run(t, w, pc, "alex", "job advance J-T1")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	j := back.Job("J-T1")
	if j == nil || j.StageIdx != 1 || j.Accepted != "alex" {
		t.Fatalf("stage progress must survive the save: %+v", j)
	}
	before := back.Bank.Accts["alex"].Balance
	out := run(t, back, back.Devices["pc-alex"], "alex", "job advance J-T1")
	if strings.Contains(out, "cannot advance") {
		t.Fatalf("advance after load should work, got:\n%s", out)
	}
	if got := back.Bank.Accts["alex"].Balance - before; got != 2000 {
		t.Fatalf("final stage should pay 2000, paid %d", got)
	}
}
