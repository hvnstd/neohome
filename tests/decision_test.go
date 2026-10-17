package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
	"neohome/internal/mcpserver"
)

// §21 需决策时唤醒调度 — the decision queue.
//
// The rule layer must keep running with no LLM. When it reaches a point it
// cannot decide on its own, it queues a Decision and keeps ticking. These
// tests drive the real triggers (an unnoticed implant, an assistant holding
// work it cannot do, an overdue case at a dark desk, a ban on a person's own
// machine), then prove that resolving a decision performs the real act its
// action names and records who decided.

// pendingOfKind filters the queue to pending decisions of a kind, optionally
// narrowed to one subject.
func pendingOfKind(w *core.World, kind, subject string) []*core.Decision {
	var out []*core.Decision
	for _, d := range w.PendingDecisions() {
		if d.Kind == kind && (subject == "" || d.Subject == subject) {
			out = append(out, d)
		}
	}
	return out
}

// decisionByID is the test-side reader for a decision regardless of status.
func decisionByID(w *core.World, id string) *core.Decision {
	for _, d := range w.Decisions {
		if d.ID == id {
			return d
		}
	}
	return nil
}

// unnoticedIntrusion plants one implant on the blind box and confirms the rule
// layer queued exactly one intrusion decision for it.
func unnoticedIntrusion(t *testing.T, w *core.World) (*core.Device, *core.Decision) {
	t.Helper()
	sc := w.Devices[core.ScannerID]
	if sc == nil {
		t.Fatal("the world should have a scanner host")
	}
	dst := vulnerableBox(t, w, "le-cyber")
	if ok := core.AttackLogin(w, sc, dst, "root", "toor", "ssh", 22); !ok {
		t.Fatal("the credential guess should have worked on the blind box")
	}
	w.DecisionTick()
	got := pendingOfKind(w, core.DecisionIntrusion, dst.ID)
	if len(got) != 1 {
		t.Fatalf("one unnoticed implant should queue exactly one intrusion decision, got %d", len(got))
	}
	return dst, got[0]
}

// TestDecisionIntrusionQueuedDeduplicatedAndResolved drives the whole arc: the
// rule layer notices a machine that its own §33 sensors never flagged, queues
// it once even across several ticks, and the resolution really removes the
// implant and records who did it.
func TestDecisionIntrusionQueuedDeduplicatedAndResolved(t *testing.T) {
	w := secSetup(t)
	dst, d := unnoticedIntrusion(t, w)

	if d.Status != core.DecisionPending || d.ResolvedBy != "" || d.Action != "" {
		t.Fatalf("a fresh decision should be pending and unowned: %+v", d)
	}
	if !strings.Contains(d.Detail, dst.Hostname) {
		t.Fatalf("the decision detail should name the host, got %q", d.Detail)
	}

	// the condition lasts many ticks; it is still one decision until answered
	w.DecisionTick()
	w.DecisionTick()
	if n := len(pendingOfKind(w, core.DecisionIntrusion, dst.ID)); n != 1 {
		t.Fatalf("dedup should keep exactly one pending decision, got %d", n)
	}

	// resolve: the implant really goes away, and the record says who decided
	if err := w.ResolveDecision(d.ID, "clean", "tester"); err != nil {
		t.Fatalf("resolving with the valid action failed: %v", err)
	}
	for _, f := range w.FootholdsFor(dst.ID) {
		if !f.Dead {
			t.Fatalf("clean should have removed every implant on %s: %+v", dst.Hostname, f)
		}
	}
	rec := decisionByID(w, d.ID)
	if rec == nil || rec.Status != core.DecisionResolved || rec.ResolvedBy != "tester" || rec.Action != "clean" {
		t.Fatalf("the resolution should be recorded with actor and action: %+v", rec)
	}
	if rec.Result == "" {
		t.Fatal("a resolved decision should carry the result of the real act")
	}
}

// TestDecisionInvalidIDAndActionError pins the refusals: an unknown id and an
// action the kind does not offer are both errors, and the queue's own rules
// (a resolved decision cannot be resolved twice) hold.
func TestDecisionInvalidIDAndActionError(t *testing.T) {
	w := secSetup(t)

	if err := w.ResolveDecision("dec-999", "clean", "tester"); err == nil {
		t.Fatal("resolving an unknown id must fail on an empty queue")
	}

	_, d := unnoticedIntrusion(t, w)

	err := w.ResolveDecision(d.ID, "nonsense", "tester")
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("an invalid action must be refused by name, got %v", err)
	}

	if err := w.ResolveDecision(d.ID, "clean", "tester"); err != nil {
		t.Fatalf("the valid action should then work: %v", err)
	}
	err = w.ResolveDecision(d.ID, "clean", "tester")
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("resolving a decided decision must fail, got %v", err)
	}
}

// TestDecisionQueueSurvivesSaveAndWorldTicks proves the queue is plain gob
// state: it round-trips through a save, the world keeps ticking with items
// pending, and the dedup rule survives the reload.
func TestDecisionQueueSurvivesSaveAndWorldTicks(t *testing.T) {
	w := secSetup(t)
	dst, d := unnoticedIntrusion(t, w)

	path := filepath.Join(t.TempDir(), "world.gob")
	if err := w.Save(path); err != nil {
		t.Fatalf("saving the world: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("loading the world: %v", err)
	}
	loaded := decisionByID(back, d.ID)
	if loaded == nil || loaded.Status != core.DecisionPending || loaded.Kind != core.DecisionIntrusion {
		t.Fatalf("the pending decision did not survive the save: %+v", loaded)
	}

	// the world keeps running with the queue full — nothing blocks
	before := back.TickCount
	back.Tick()
	if back.TickCount <= before {
		t.Fatal("the world should keep ticking with decisions pending")
	}
	if n := len(pendingOfKind(back, core.DecisionIntrusion, dst.ID)); n != 1 {
		t.Fatalf("dedup should still hold after a reload and a tick, got %d", n)
	}
}

// TestDecisionAssistantStuckTrigger pins the second wake condition: a job the
// assistant holds but cannot perform. Resolving it returns the job to the
// board, which is a real change to real job state.
func TestDecisionAssistantStuckTrigger(t *testing.T) {
	w := secSetup(t)
	if w.Jobs == nil {
		t.Fatal("a fresh world should have a job board")
	}

	j := &core.Job{ID: "J-dec-stuck", Title: "unsupported work", Verify: "web-up"}
	w.Jobs.List = append(w.Jobs.List, j)
	if err := w.AcceptJob("assistant", j.ID); err != nil {
		t.Fatalf("the assistant should be able to take the job: %v", err)
	}

	w.DecisionTick()
	got := pendingOfKind(w, core.DecisionAssistantStuck, j.ID)
	if len(got) != 1 {
		t.Fatalf("a job the assistant cannot perform should queue one decision, got %d", len(got))
	}
	if !strings.Contains(got[0].Detail, "web-up") {
		t.Fatalf("the detail should name the unsupported verifier, got %q", got[0].Detail)
	}

	if err := w.ResolveDecision(got[0].ID, "requeue", "tester"); err != nil {
		t.Fatalf("requeue failed: %v", err)
	}
	if j.Accepted != "" {
		t.Fatalf("requeue should return the job to the board, accepted=%q", j.Accepted)
	}
	if rec := decisionByID(w, got[0].ID); rec == nil || rec.Status != core.DecisionResolved {
		t.Fatalf("the requeue should be recorded: %+v", rec)
	}
}

// TestDecisionAbuseReviewTrigger pins the third wake condition: a case past
// its deadline at a desk that is not operational. AbuseTick skips a closed
// desk, so the ladder cannot move it — a decision is the only way forward.
func TestDecisionAbuseReviewTrigger(t *testing.T) {
	w := secSetup(t)
	desk := w.Devices["novapanel-abuse"]
	if desk == nil {
		t.Fatal("the world should have the NovaPanel abuse desk")
	}

	c := w.OpenCaseForTest("novapanel-abuse", "203.0.113.7", "tester", "portscan", []string{"reporter log"})
	if c == nil {
		t.Fatal("the desk should have opened a case")
	}
	// past due, and the desk's own intake service is down
	c.Due = w.Sim.Add(-time.Minute)
	if _, err := desk.StopService("httpd"); err != nil {
		t.Fatalf("stopping the abuse portal: %v", err)
	}

	w.DecisionTick()
	got := pendingOfKind(w, core.DecisionAbuseReview, c.ID)
	if len(got) != 1 {
		t.Fatalf("an overdue case at an offline desk should queue one decision, got %d", len(got))
	}
	if !strings.Contains(got[0].Detail, c.SubjectIP) {
		t.Fatalf("the detail should name the subject address, got %q", got[0].Detail)
	}

	if err := w.ResolveDecision(got[0].ID, "close", "tester"); err != nil {
		t.Fatalf("closing the case failed: %v", err)
	}
	if c.Stage != core.StageClosed {
		t.Fatalf("close should have closed the case, got stage %q", c.Stage)
	}
}

// TestDecisionNPCAnomalyBanTrigger pins the fourth wake condition: the
// household's own automation banned a machine a person owns. Lifting the ban
// is a real state change on the banning device.
func TestDecisionNPCAnomalyBanTrigger(t *testing.T) {
	w := secSetup(t)
	router := w.Devices["router-alex"]
	pc := w.Devices["pc-alex"]
	if router == nil || pc == nil {
		t.Fatal("the household should have a router and a PC")
	}

	ip := pc.FirstLANIP()
	router.Ban(ip, pc.Hostname, "automated defence caught a person's machine", time.Hour)
	if !router.IsBanned(ip) {
		t.Fatal("the ban should be active before the trigger runs")
	}

	w.DecisionTick()
	subject := router.ID + "|" + ip
	got := pendingOfKind(w, core.DecisionNPCAnomaly, subject)
	if len(got) != 1 {
		t.Fatalf("a ban on an owned device should queue one decision, got %d", len(got))
	}

	if err := w.ResolveDecision(got[0].ID, "unban", "tester"); err != nil {
		t.Fatalf("unban failed: %v", err)
	}
	if router.IsBanned(ip) {
		t.Fatal("unban should have lifted the ban for real")
	}
	rec := decisionByID(w, got[0].ID)
	if rec == nil || rec.ResolvedBy != "tester" || rec.Action != "unban" {
		t.Fatalf("the unban should be recorded with actor and action: %+v", rec)
	}
}

// TestDecisionShellCommandListsAndResolves exercises the player-facing verb:
// `decisions` shows the queue and `decisions resolve` answers it, printing the
// real consequence.
func TestDecisionShellCommandListsAndResolves(t *testing.T) {
	w := secSetup(t)
	dst, d := unnoticedIntrusion(t, w)
	pc := w.Devices["pc-alex"]

	list := run(t, w, pc, "alex", "decisions")
	if !strings.Contains(list, "intrusion") || !strings.Contains(list, dst.Hostname) {
		t.Fatalf("`decisions` should list the pending intrusion decision:\n%s", list)
	}

	out := run(t, w, pc, "alex", "decisions resolve "+d.ID+" clean")
	if !strings.Contains(out, "resolved") {
		t.Fatalf("`decisions resolve` should print the real result:\n%s", out)
	}
	if rec := decisionByID(w, d.ID); rec == nil || rec.Status != core.DecisionResolved || rec.ResolvedBy != "alex" {
		t.Fatalf("the shell resolution should be recorded against the session user: %+v", rec)
	}
	if n := len(pendingOfKind(w, core.DecisionIntrusion, dst.ID)); n != 0 {
		t.Fatalf("the resolved decision should leave the pending queue, got %d", n)
	}
}

// TestMcpDecisionsListResolveAndStatus proves an external MCP client can see
// the queue, answer it as `mcp-agent`, and that `world_status` reports the
// pending count — the external half of §21's wake-on-decision design.
func TestMcpDecisionsListResolveAndStatus(t *testing.T) {
	w := secSetup(t)
	server, err := mcpserver.New(w, testMCPToken, "")
	if err != nil {
		t.Fatalf("starting the MCP server: %v", err)
	}

	dst, d := unnoticedIntrusion(t, w)
	pendingCount := len(w.PendingDecisions())

	// world_status carries the pending count
	_, status := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "world_status", "arguments": map[string]any{}},
	}, "")
	if status.Error != nil {
		t.Fatalf("world_status protocol error: %+v", status.Error)
	}
	statusText := contentText(t, status.Result)
	if want := "pending_decisions=" + itoa(pendingCount); !strings.Contains(statusText, want) {
		t.Fatalf("world_status should report %q:\n%s", want, statusText)
	}

	// list_decisions returns the pending queue as JSON
	_, listed := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "list_decisions", "arguments": map[string]any{}},
	}, "")
	if listed.Error != nil {
		t.Fatalf("list_decisions protocol error: %+v", listed.Error)
	}
	var decisions []core.Decision
	if err := json.Unmarshal([]byte(contentText(t, listed.Result)), &decisions); err != nil {
		t.Fatalf("list_decisions should return a JSON array: %v", err)
	}
	found := false
	for _, x := range decisions {
		if x.ID == d.ID && x.Kind == core.DecisionIntrusion && x.Subject == dst.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("list_decisions should include the queued intrusion decision %s: %+v", d.ID, decisions)
	}

	// resolve_decision answers it as mcp-agent, with the real consequence
	_, resolved := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "resolve_decision", "arguments": map[string]string{
			"id": d.ID, "action": "clean",
		}},
	}, "")
	if resolved.Error != nil {
		t.Fatalf("resolve_decision protocol error: %+v", resolved.Error)
	}
	if isError(t, resolved.Result) {
		t.Fatalf("resolve_decision should succeed: %s", contentText(t, resolved.Result))
	}
	rec := decisionByID(w, d.ID)
	if rec == nil || rec.Status != core.DecisionResolved || rec.ResolvedBy != "mcp-agent" || rec.Action != "clean" {
		t.Fatalf("the MCP resolution should be recorded as mcp-agent: %+v", rec)
	}
	for _, f := range w.FootholdsFor(dst.ID) {
		if !f.Dead {
			t.Fatalf("the MCP clean should have removed the implant: %+v", f)
		}
	}
}

// contentText pulls the first text block out of a tools/call result.
func contentText(t *testing.T, result map[string]json.RawMessage) string {
	t.Helper()
	raw, ok := result["content"]
	if !ok {
		t.Fatalf("tool result has no content: %v", result)
	}
	var content []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &content); err != nil || len(content) == 0 {
		t.Fatalf("tool content is not a text block (%v): %s", err, raw)
	}
	return content[0].Text
}

// isError reads the isError flag from a tools/call result.
func isError(t *testing.T, result map[string]json.RawMessage) bool {
	t.Helper()
	raw, ok := result["isError"]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("tool result isError is not a bool: %s", raw)
	}
	return b
}

// itoa is a tiny local integer formatter so the test does not add a strconv
// import just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
