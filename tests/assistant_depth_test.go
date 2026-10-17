package tests

import (
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// Assistant depth (§22–§25) and the §1 countermeasure loop.
//
// Training is not a counter: each track unlocks a named, real capability,
// delegation refuses what the tracks do not cover (naming the missing one),
// and a security-trained assistant the owner has armed walks the household's
// own §33 detections into real countermeasures — block, rotate, clean,
// report — paid for out of the owner's account, one step per cycle.

// TestAssistantTrackGatesRefusalNamesTrack pins the honesty of delegation: a
// verifier outside the learned tracks is refused with the missing track
// named, an unlearnable one is refused as simply not assistant work, and the
// §21 stuck trigger consults the same gate — a verifier the assistant would
// be refused is exactly the one that queues an assistant-stuck decision.
func TestAssistantTrackGatesRefusalNamesTrack(t *testing.T) {
	w := secSetup(t)

	// a staged job whose stage needs the networking track
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-NET", Title: "bring the ssh endpoint back", Client: "test", Pay: 800, Tier: 1, Target: "self",
		Stages: []core.MissionStage{{Name: "link", Help: "sshd answers", Verify: "ssh-up", Pay: 800}},
	})
	err := w.TaskAssistant("J-NET")
	if err == nil || !strings.Contains(err.Error(), "assistant lacks the networking track") {
		t.Fatalf("refusal must name the missing track, got %v", err)
	}
	if !strings.Contains(err.Error(), "link") {
		t.Fatalf("refusal must name the stage, got %v", err)
	}

	// a verifier that is not assistant work at all stays a plain refusal
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-WEB", Title: "serve the web", Client: "test", Pay: 500, Tier: 1, Target: "self", Verify: "web-up",
	})
	if err := w.TaskAssistant("J-WEB"); err == nil || !strings.Contains(err.Error(), "web-up") {
		t.Fatalf("an unlearnable verifier must be refused by name, got %v", err)
	}

	// the decision queue's trigger is the same gate: a held ssh-up job is
	// stuck exactly while the networking track is missing
	held := &core.Job{ID: "J-STUCKNET", Title: "held", Client: "test", Pay: 100, Verify: "ssh-up"}
	w.Jobs.List = append(w.Jobs.List, held)
	if err := w.AcceptJob("assistant", held.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	w.DecisionTick()
	if got := pendingOfKind(w, core.DecisionAssistantStuck, held.ID); len(got) != 1 {
		t.Fatalf("a held job outside the tracks should queue one stuck decision, got %d", len(got))
	}
	if _, err := w.TrainAssistant("alex", "networking"); err != nil {
		t.Fatalf("training: %v", err)
	}
	held2 := &core.Job{ID: "J-STUCKNET2", Title: "held 2", Client: "test", Pay: 100, Verify: "ssh-up"}
	w.Jobs.List = append(w.Jobs.List, held2)
	if err := w.AcceptJob("assistant", held2.ID); err != nil {
		t.Fatalf("accept 2: %v", err)
	}
	w.DecisionTick()
	if got := pendingOfKind(w, core.DecisionAssistantStuck, held2.ID); len(got) != 0 {
		t.Fatalf("a learned track must close the stuck trigger too, got %d pending", len(got))
	}
}

// TestAssistantNetworkingPerformsSSHUp: after training, the delegated kind is
// accepted and really performed — the household's stopped sshds run again,
// the verifier agrees because the world state says so, and the stage pays.
func TestAssistantNetworkingPerformsSSHUp(t *testing.T) {
	w := secSetup(t)
	if _, err := w.TrainAssistant("alex", "networking"); err != nil {
		t.Fatalf("training: %v", err)
	}
	nas := w.Devices["nas-alex"]
	pc := w.Devices["pc-alex"]
	if _, err := nas.StopService("sshd"); err != nil {
		t.Fatalf("stop sshd: %v", err)
	}
	if pc.Svc("sshd").State == "running" {
		t.Fatal("the seeded PC sshd should be stopped, so the act has work to do")
	}
	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-NET", Title: "bring the ssh endpoint back", Client: "test", Pay: 800, Tier: 1, Target: "self",
		Stages: []core.MissionStage{{Name: "link", Help: "sshd answers", Verify: "ssh-up", Pay: 800}},
	})
	before := w.Bank.Accts["alex"].Balance
	if err := w.TaskAssistant("J-NET"); err != nil {
		t.Fatalf("a learned track must make the kind delegatable: %v", err)
	}
	for i := 0; i < 10; i++ {
		w.Tick()
	}
	j := w.Job("J-NET")
	if !j.Done {
		t.Fatal("the assistant should have finished the ssh-up stage")
	}
	// the act was real: the stopped endpoints run again
	if nas.Svc("sshd").State != "running" || pc.Svc("sshd").State != "running" {
		t.Fatalf("the assistant should have started the household sshds (nas=%s, pc=%s)",
			nas.Svc("sshd").State, pc.Svc("sshd").State)
	}
	if got := w.Bank.Accts["alex"].Balance - before; got != 800 {
		t.Fatalf("the stage should pay 800, got %d", got)
	}
}

// TestAssistantNetworkingPerformsClean: the gateway-hygiene half of the
// networking track — the assistant really lifts the router's bans and zeroes
// its strike counters, which is what the verifier reads.
func TestAssistantNetworkingPerformsClean(t *testing.T) {
	w := secSetup(t)
	if _, err := w.TrainAssistant("alex", "networking"); err != nil {
		t.Fatalf("training: %v", err)
	}
	router := w.Devices["router-alex"]
	router.Ban("203.0.113.99", "evil-host", "test strike", time.Hour)
	router.Fail2Ban["203.0.113.99"] = 3

	w.Jobs.List = append(w.Jobs.List, &core.Job{
		ID: "J-CLEAN", Title: "clean up the gateway", Client: "test", Pay: 400, Tier: 1, Target: "self", Verify: "clean",
	})
	if err := w.TaskAssistant("J-CLEAN"); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	for i := 0; i < 10; i++ {
		w.Tick()
	}
	if !w.Job("J-CLEAN").Done {
		t.Fatal("the assistant should have finished the clean job")
	}
	if len(router.ActiveBans()) != 0 {
		t.Fatal("the router's bans should really be lifted")
	}
	if router.Fail2Ban["203.0.113.99"] != 0 {
		t.Fatal("the strike counter should really be zeroed")
	}
}

// armDefend trains the security track and arms the loop through the shell,
// the way the owner does it.
func armDefend(t *testing.T, w *core.World) *core.Device {
	t.Helper()
	pc := w.Devices["pc-alex"]
	if _, err := w.TrainAssistant("alex", "security"); err != nil {
		t.Fatalf("training: %v", err)
	}
	if out := run(t, w, pc, "alex", "assist defend on"); !strings.Contains(out, "armed") {
		t.Fatalf("arming should confirm, got:\n%s", out)
	}
	return pc
}

// TestAssistantDefendOffMeansNoCountermeasure: with the toggle off, a real
// attack still produces real detection — the failures are on the NAS's own
// records — and nothing answers it: no ban, no charge, no assistant log.
func TestAssistantDefendOffMeansNoCountermeasure(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	nas := w.Devices["nas-alex"]
	scIP := sc.SourceIPFor(nas)

	for i := 0; i < 4; i++ {
		core.AttackLogin(w, sc, nas, "root", "wrongpass", "ssh", 22)
	}
	if got := nas.FailsFrom(scIP, 10*time.Minute); got != 4 {
		t.Fatalf("the detection itself should exist: 4 fails, got %d", got)
	}
	before := w.Bank.Accts["alex"].Balance
	for i := 0; i < 3; i++ {
		w.Tick()
	}
	if len(nas.ActiveBans()) != 0 {
		t.Fatal("with defend off nothing may ban the attacker")
	}
	if acc := w.Bank.Accts["alex"]; acc.Balance != before {
		t.Fatalf("with defend off no budget moves: %d -> %d", before, acc.Balance)
	}
	asst := w.Devices["asst-alex"]
	if _, ok := asst.FS.Read("/home/assistant/defense.log"); ok {
		t.Fatal("with defend off the assistant must not keep a countermeasure log")
	}
}

// TestAssistantDefendBlocksAndReports is the §1 loop with an executor: the
// same attack, armed, leads to a real drop the packet path enforces, then to
// a real case at the network that answers for the source — logged, evidenced
// and charged, one step per cycle.
func TestAssistantDefendBlocksAndReports(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	nas := w.Devices["nas-alex"]
	router := w.Devices["router-alex"]
	asst := w.Devices["asst-alex"]
	scIP := sc.SourceIPFor(nas)

	for i := 0; i < 4; i++ {
		core.AttackLogin(w, sc, nas, "root", "wrongpass", "ssh", 22)
	}
	armDefend(t, w)
	bal := w.Bank.Accts["alex"].Balance

	// cycle one: block — the source is really dropped at the victim and the edge
	w.Tick()
	if !nas.IsBanned(scIP) {
		t.Fatal("cycle 1 should have banned the attacker on the victim")
	}
	if !router.IsBanned(scIP) {
		t.Fatal("cycle 1 should have banned the attacker at the household edge")
	}
	if _, _, msg := core.Dial(sc, nas.FirstLANIP(), 22); msg != "Connection timed out (filtered)" {
		t.Fatalf("the packet path must drop the banned source, got %q", msg)
	}
	flows := nas.Sec().Flows
	if last := flows[len(flows)-1]; last.Verdict != core.FlowBanned {
		t.Fatalf("the dropped attempt should be recorded as banned, got %s", last.Verdict)
	}
	if got := bal - w.Bank.Accts["alex"].Balance; got != core.DefendBlockCost {
		t.Fatalf("block should cost %d, charged %d", core.DefendBlockCost, got)
	}
	// logged on the device and evidenced in the world's graph
	log, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "assistant countermeasure") {
		t.Fatalf("the device log should carry the countermeasure:\n%s", log)
	}
	evidenced := false
	for _, e := range w.Case.Events {
		if strings.Contains(e.Detail, "assistant countermeasure (block)") {
			evidenced = true
		}
	}
	if !evidenced {
		t.Fatal("the block should be recorded in the evidence graph")
	}

	// cycle two: report — the network that answers for the source holds a case
	w.Tick()
	if got := bal - core.DefendBlockCost - w.Bank.Accts["alex"].Balance; got != core.DefendReportCost {
		t.Fatalf("report should cost %d, charged %d", core.DefendReportCost, got)
	}
	if n := w.CasesAbout("alex", scIP); n == 0 {
		t.Fatal("the assistant should have filed a real abuse case about the source")
	}

	// cycle three: nothing left to answer — no double countermeasures
	mark := w.Bank.Accts["alex"].Balance
	w.Tick()
	if w.Bank.Accts["alex"].Balance != mark {
		t.Fatal("a countered detection must not be countered twice")
	}
	// the node's own record shows what it did, in order
	done, ok := asst.FS.Read("/home/assistant/defense.log")
	if !ok {
		t.Fatal("the assistant node should keep its countermeasure record")
	}
	if !strings.Contains(string(done), "block|nas-alex|"+scIP) ||
		!strings.Contains(string(done), "report|"+scIP) {
		t.Fatalf("the processed-set should name both steps:\n%s", done)
	}
}

// TestAssistantDefendCleansAndRotates: an intrusion gets the two answers §1
// names — the implant is really removed (process, payload, persistence), and
// the credential it used is really burned: the old password fails, the new
// one works, and account and shadow agree because ChangePassword did it.
func TestAssistantDefendCleansAndRotates(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	nas := w.Devices["nas-alex"]
	nas.FindUser("root").Pass = "toor"

	if ok := core.AttackLogin(w, sc, nas, "root", "toor", "ssh", 22); !ok {
		t.Fatal("the guess should have worked on this machine")
	}
	fs := w.LiveFootholds()
	if len(fs) != 1 || fs[0].Host != nas.ID {
		t.Fatalf("expected one live implant on the nas, got %+v", fs)
	}
	f := fs[0]

	// detection without arming: the implant stays resident
	w.Tick()
	if f.Dead {
		t.Fatal("with defend off the implant should still be resident")
	}

	armDefend(t, w)
	bal := w.Bank.Accts["alex"].Balance

	// cycle one: clean
	w.Tick()
	if !f.Dead {
		t.Fatal("cycle 1 should have removed the implant")
	}
	if _, ok := nas.FS.Read(f.File); ok {
		t.Fatal("the payload should really be off the disk")
	}
	if _, ok := nas.FS.Read(f.Cron); ok {
		t.Fatal("the persistence line should really be gone")
	}
	for _, p := range nas.Procs {
		if p.PID == f.PID {
			t.Fatal("the implant process should really be dead")
		}
	}
	if got := bal - w.Bank.Accts["alex"].Balance; got != core.DefendCleanCost {
		t.Fatalf("clean should cost %d, charged %d", core.DefendCleanCost, got)
	}

	// cycle two: rotate the credential the intrusion used
	w.Tick()
	u := nas.FindUser("root")
	if u.Pass == "toor" {
		t.Fatal("the leaked password should be burned")
	}
	if u.CheckPassword("toor") {
		t.Fatal("the old password must fail now")
	}
	if !u.CheckPassword(u.Pass) {
		t.Fatal("the rotated password must work — account and shadow agree")
	}
	if got := bal - core.DefendCleanCost - w.Bank.Accts["alex"].Balance; got != core.DefendRotateCost {
		t.Fatalf("rotate should cost %d, charged %d", core.DefendRotateCost, got)
	}

	// cycle three: the intrusion is answered; nothing more happens
	mark := w.Bank.Accts["alex"].Balance
	w.Tick()
	if w.Bank.Accts["alex"].Balance != mark {
		t.Fatal("a countered intrusion must not be countered twice")
	}
}

// TestAssistantDefendArmingAndPersistence: arming is owner business, refused
// honestly while the security track is missing, shown in status, and the
// toggle survives a save.
func TestAssistantDefendArmingAndPersistence(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	p := w.Players["alex"]

	if out := run(t, w, pc, "alex", "assist defend"); !strings.Contains(out, "automatic countermeasures: off") {
		t.Fatalf("defend should report off, got:\n%s", out)
	}
	// honest refusal: no security track, no arming
	if out := run(t, w, pc, "alex", "assist defend on"); !strings.Contains(out, "lacks the security track") {
		t.Fatalf("arming without the track should be refused by name, got:\n%s", out)
	}
	if p.AssistDefend {
		t.Fatal("a refused arming must not flip the toggle")
	}
	// the catalogue shows what each track unlocks
	if out := run(t, w, pc, "alex", "assist train"); !strings.Contains(out, "unlocks:") {
		t.Fatalf("assist train should show what each track unlocks, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "assist train security"); !strings.Contains(out, "learned security") {
		t.Fatalf("training should confirm, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "assist defend on"); !strings.Contains(out, "armed") {
		t.Fatalf("arming should confirm, got:\n%s", out)
	}
	if !p.AssistDefend {
		t.Fatal("the toggle should be on")
	}
	if out := run(t, w, pc, "alex", "assist status"); !strings.Contains(out, "defend:   armed") ||
		!strings.Contains(out, "unlocks:") {
		t.Fatalf("status should show the armed loop and the track unlocks, got:\n%s", out)
	}
	// the toggle is logged on the node itself
	asst := w.Devices["asst-alex"]
	log, _ := asst.FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "automatic countermeasures armed by alex") {
		t.Fatalf("the node should log the arming:\n%s", log)
	}

	// the toggle is plain player state: it survives a save
	path := t.TempDir() + "/world.gob"
	if err := w.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatal(err)
	}
	bp := back.Players["alex"]
	if bp == nil || !bp.AssistDefend {
		t.Fatal("the armed toggle should survive a save/load")
	}
	bpc := back.Devices["pc-alex"]
	if out := run(t, back, bpc, "alex", "assist defend"); !strings.Contains(out, "automatic countermeasures: armed") {
		t.Fatalf("the loaded world should show the armed loop, got:\n%s", out)
	}
}

// TestAssistantDefendBrokeOwnerSkips: countermeasures cost real money, and a
// household that cannot pay gets none — silently, with no minting and no
// error spam, and the detection stays fresh for when the budget is back.
func TestAssistantDefendBrokeOwnerSkips(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	nas := w.Devices["nas-alex"]
	scIP := sc.SourceIPFor(nas)

	armDefend(t, w)
	acc := w.Bank.Accts["alex"]
	acc.Balance = 100 // less than any countermeasure costs

	for i := 0; i < 4; i++ {
		core.AttackLogin(w, sc, nas, "root", "wrongpass", "ssh", 22)
	}
	for i := 0; i < 3; i++ {
		w.Tick()
	}
	if nas.IsBanned(scIP) {
		t.Fatal("a broke owner buys no block")
	}
	if acc.Balance != 100 {
		t.Fatalf("a skipped step must not move money, balance %d", acc.Balance)
	}
	asst := w.Devices["asst-alex"]
	if _, ok := asst.FS.Read("/home/assistant/defense.log"); ok {
		t.Fatal("a skipped step must not be marked as countered")
	}
	// the detection is still fresh: fund the household and the loop answers
	acc.Balance = 10000
	w.Tick()
	if !nas.IsBanned(scIP) {
		t.Fatal("with budget back the assistant should answer the still-fresh detection")
	}
}
