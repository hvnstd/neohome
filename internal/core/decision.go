package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// §21 NPC AI 架构 — the decision queue
//
// The spec's three layers are the simulation, the agent and the LLM, and the
// rule says plainly that only a moment which *needs* intelligence should wake
// the smarter layer. The simulation runs with no LLM at all; when the rule
// layer reaches a point where it would have to guess — an intrusion nothing
// noticed, a job an assistant accepted and cannot perform, an overdue case at
// a desk nobody is staffing, a ban that landed on a person's own machine — it
// does not guess. It queues a Decision and keeps ticking.
//
//	World simulation  ->  DecisionTick  ->  Decisions  ->  a smarter layer
//	                                       (MCP client / assistant / player)
//
// The queue is plain data on World, so it survives a save. The world keeps
// running exactly as before with the queue full: nothing here blocks, waits,
// or changes the packet path. A decision is only *evidence that judgement is
// needed*; resolving it is what changes state, and every resolution below
// performs the real act its action names (it deletes the implant, returns the
// job, closes the case, lifts the ban).
// ---------------------------------------------------------------------------

// Decision statuses.
const (
	DecisionPending  = "pending"
	DecisionResolved = "resolved"
	DecisionExpired  = "expired"
)

// The wake conditions the rule layer can recognise from existing state. Each
// one's exact trigger is documented on the enqueue function that raises it.
const (
	// DecisionIntrusion: a live foothold sits on a machine whose own §33
	// sensors never noticed it (an empty Foothold.Seen).
	DecisionIntrusion = "intrusion"
	// DecisionAssistantStuck: a job is held by the assistant but its current
	// verifier is not one the assistant can perform, or a queued assistant
	// task points at a job that no longer exists.
	DecisionAssistantStuck = "assistant-stuck"
	// DecisionAbuseReview: an open abuse case is past its own deadline at a
	// desk that is not operational, so the automatic ladder cannot advance.
	DecisionAbuseReview = "abuse-review"
	// DecisionNPCAnomaly: a machine holds an active ban whose source address
	// belongs to a device a person owns — the household's own automation
	// banned a human, and only judgement can say whether that was right.
	DecisionNPCAnomaly = "npc-anomaly"
)

// decisionTTL is how long a pending decision is allowed to sit before it is
// retired as stale. It is a tick count, not a wall clock: at 30 game-seconds
// per tick this is two simulated hours.
const decisionTTL = 240

// maxDecisionLog bounds the queue so a world that never answers decisions
// still has a bounded save. The oldest already-decided entries are dropped
// first; pending ones are only dropped when there is nothing else left.
const maxDecisionLog = 200

// Decision is a moment queued by the rule layer for a smarter layer (§21).
// The shape is owned by this file and is plain data, so gob carries it.
type Decision struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Subject     string `json:"subject"`
	Detail      string `json:"detail"`
	Status      string `json:"status"`
	CreatedTick int    `json:"created_tick"`
	ResolvedBy  string `json:"resolved_by,omitempty"`
	Action      string `json:"action,omitempty"`
	Result      string `json:"result,omitempty"`
}

// DecisionTick scans rule-layer outcomes for anything that needs judgement
// and queues it. Wired from World.Tick after ReplTick. It only reads state
// and appends to the queue, so a full queue never stops the world.
func (w *World) DecisionTick() {
	w.expireDecisions()
	w.enqueueIntrusionDecisions()
	w.enqueueAssistantStuckDecisions()
	w.enqueueAbuseReviewDecisions()
	w.enqueueNPCAnomalyDecisions()
	w.trimDecisions()
}

// ---- wake conditions -------------------------------------------------------

// enqueueIntrusionDecisions raises "intrusion" for every live foothold whose
// host's own §33 tools saw nothing at the moment of the intrusion
// (len(Foothold.Seen) == 0). A machine that did notice has already reacted;
// one that did not is a judgement call — clean it up quietly, or escalate and
// tell the machine's sensors what they missed.
func (w *World) enqueueIntrusionDecisions() {
	for _, f := range w.LiveFootholds() {
		if len(f.Seen) != 0 {
			continue
		}
		host := f.Host
		if d := w.Devices[f.Host]; d != nil {
			host = d.Hostname
		}
		detail := fmt.Sprintf("unnoticed implant on %s: %s (pid %d) as %s from %s, payload %s, persistence %s, %d beacon(s)",
			host, f.Proc, f.PID, f.Account, f.Actor, f.File, f.Cron, f.Beacons)
		w.enqueueDecision(DecisionIntrusion, f.Host, detail)
	}
}

// enqueueAssistantStuckDecisions raises "assistant-stuck" when the assistant
// holds work it cannot finish: a not-Done job whose Accepted is "assistant"
// and whose current stage verifier (or single-shot verifier) is outside the
// small set assistantAct performs, or a queued assist-job task whose job has
// disappeared. Both are persistent states — the task may be abandoned, but the
// job stays held with no way forward.
func (w *World) enqueueAssistantStuckDecisions() {
	for _, j := range w.Jobs.List {
		if j == nil || j.Done || j.Accepted != "assistant" {
			continue
		}
		v := currentJobVerify(j)
		if w.assistantCapable(v) {
			continue
		}
		detail := fmt.Sprintf("job %s is held by the assistant but its current verifier %q is not one it can perform",
			j.ID, v)
		w.enqueueDecision(DecisionAssistantStuck, j.ID, detail)
	}
	for _, t := range w.Tasks {
		if t.Done || t.Kind != "assist-job" || t.JobID == "" {
			continue
		}
		if w.Job(t.JobID) == nil {
			detail := fmt.Sprintf("task %d still points at job %s, which no longer exists", t.ID, t.JobID)
			w.enqueueDecision(DecisionAssistantStuck, t.JobID, detail)
		}
	}
}

// enqueueAbuseReviewDecisions raises "abuse-review" for an open case whose
// deadline has passed at a desk that is not operational (its host dark or its
// intake service stopped). AbuseTick skips a closed desk entirely, so nothing
// moves such a case; a human or a smarter layer has to decide whether to close
// it or put it up the ladder. Law-enforcement files are excluded: §35 walks
// those on the unit's own clock, not the network ladder.
func (w *World) enqueueAbuseReviewDecisions() {
	if w.Abuse == nil {
		return
	}
	for _, c := range w.Abuse.List {
		if c == nil || caseTerminal(c.Stage) || c.Due.IsZero() {
			continue
		}
		if w.Sim.Before(c.Due) {
			continue
		}
		dk := w.deskByID(c.DeskID)
		if dk == nil || dk.Kind == DeskLaw {
			continue
		}
		if w.DeskOperational(dk) {
			continue
		}
		detail := fmt.Sprintf("case %s (%s, stage %s) has been overdue since %s but %s is not operational",
			c.ID, c.SubjectIP, c.Stage, c.Due.Format("15:04"), dk.Org)
		w.enqueueDecision(DecisionAbuseReview, c.ID, detail)
	}
}

// enqueueNPCAnomalyDecisions raises "npc-anomaly" for an active ban whose
// source address resolves to a device a person owns. Automated defence bans
// strangers; a ban that lands on a known person's machine is exactly the kind
// of ambiguous outcome that needs a human/smart decision — keep it or lift it.
func (w *World) enqueueNPCAnomalyDecisions() {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || d.Security == nil {
			continue
		}
		for _, b := range d.Security.Bans {
			if !b.Active(w.Sim) {
				continue
			}
			srcID, ok := w.IPMap[b.IP]
			if !ok {
				continue
			}
			src := w.Devices[srcID]
			if src == nil || src.Owner == "" {
				continue
			}
			host := b.Host
			if host == "" {
				host = b.IP
			}
			detail := fmt.Sprintf("%s banned %s (%s, owned by %s): %s",
				d.Hostname, b.IP, host, src.Owner, b.Reason)
			w.enqueueDecision(DecisionNPCAnomaly, id+"|"+b.IP, detail)
		}
	}
}

// ---- queue mechanics -------------------------------------------------------

// enqueueDecision appends one pending decision, unless one is already pending
// for the same (Kind, Subject). Exactly-one-pending is the dedup rule: the
// condition may last many ticks, but it is one decision until someone answers.
func (w *World) enqueueDecision(kind, subject, detail string) *Decision {
	if w.hasPending(kind, subject) {
		return nil
	}
	d := &Decision{
		ID:          w.nextDecisionID(),
		Kind:        kind,
		Subject:     subject,
		Detail:      detail,
		Status:      DecisionPending,
		CreatedTick: w.TickCount,
	}
	w.Decisions = append(w.Decisions, d)
	w.AddEvent("world", "notice", "decision", "queued %s for %s (%s)", kind, subject, d.ID)
	return d
}

func (w *World) hasPending(kind, subject string) bool {
	for _, d := range w.Decisions {
		if d.Status == DecisionPending && d.Kind == kind && d.Subject == subject {
			return true
		}
	}
	return false
}

// nextDecisionID mints an id that is unique among the entries currently on
// the queue, without a counter field to persist: the highest existing number
// plus one. Because nothing is ever re-used while its entry is present, two
// live decisions cannot share an id.
func (w *World) nextDecisionID() string {
	max := 0
	for _, d := range w.Decisions {
		if n, err := strconv.Atoi(strings.TrimPrefix(d.ID, "dec-")); err == nil && n > max {
			max = n
		}
	}
	return "dec-" + strconv.Itoa(max+1)
}

// expireDecisions retires pending entries that nobody answered in time.
func (w *World) expireDecisions() {
	for _, d := range w.Decisions {
		if d.Status != DecisionPending {
			continue
		}
		if w.TickCount-d.CreatedTick < decisionTTL {
			continue
		}
		d.Status = DecisionExpired
		d.Result = fmt.Sprintf("expired after %d ticks without a decision", decisionTTL)
	}
}

// trimDecisions bounds the queue, dropping already-decided entries first and
// only the oldest pending ones if the queue is nothing but pending.
func (w *World) trimDecisions() {
	if len(w.Decisions) <= maxDecisionLog {
		return
	}
	over := len(w.Decisions) - maxDecisionLog
	keep := w.Decisions[:0]
	for _, d := range w.Decisions {
		if over > 0 && d.Status != DecisionPending {
			over--
			continue
		}
		keep = append(keep, d)
	}
	w.Decisions = keep
	if len(w.Decisions) > maxDecisionLog {
		w.Decisions = append(w.Decisions[:0], w.Decisions[len(w.Decisions)-maxDecisionLog:]...)
	}
}

// PendingDecisions lists the decisions still waiting for an answer, oldest
// first. It returns the live entries, so callers must hold the world lock
// while reading them (as every other world access does).
func (w *World) PendingDecisions() []*Decision {
	var out []*Decision
	for _, d := range w.Decisions {
		if d.Status == DecisionPending {
			out = append(out, d)
		}
	}
	return out
}

func (w *World) pendingDecision(id string) *Decision {
	for _, d := range w.Decisions {
		if d.ID == id && d.Status == DecisionPending {
			return d
		}
	}
	return nil
}

func (w *World) decisionByID(id string) *Decision {
	for _, d := range w.Decisions {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (w *World) pendingIDs() []string {
	var out []string
	for _, d := range w.Decisions {
		if d.Status == DecisionPending {
			out = append(out, d.ID)
		}
	}
	return out
}

// ---- resolving -------------------------------------------------------------

// ResolveDecision answers a pending decision, applying the real consequence
// its action names and recording who decided. An unknown id or an action the
// kind does not offer is refused with the valid choices spelled out.
func (w *World) ResolveDecision(id, action, actor string) error {
	d := w.pendingDecision(id)
	if d == nil {
		if prev := w.decisionByID(id); prev != nil {
			return fmt.Errorf("decision %s is already %s", id, prev.Status)
		}
		valid := w.pendingIDs()
		if len(valid) == 0 {
			return fmt.Errorf("no pending decision %q (the queue is empty)", id)
		}
		return fmt.Errorf("no pending decision %q (pending: %s)", id, strings.Join(valid, ", "))
	}
	if !validDecisionAction(d.Kind, action) {
		return fmt.Errorf("unknown action %q for %s (valid: %s)", action, d.Kind, strings.Join(decisionActions(d.Kind), ", "))
	}
	result, err := w.applyDecision(d, action)
	if err != nil {
		return err
	}
	d.Status = DecisionResolved
	d.ResolvedBy = actor
	d.Action = action
	d.Result = result
	w.AddEvent("world", "info", "decision", "%s: %s %s resolved by %s — %s",
		d.ID, d.Kind, d.Subject, actor, result)
	return nil
}

// decisionActions lists the actions a kind accepts. It is the one owner of
// that fact, used both to validate a resolution and to tell the operator what
// the valid choices are.
func decisionActions(kind string) []string {
	switch kind {
	case DecisionIntrusion:
		return []string{"clean", "escalate"}
	case DecisionAssistantStuck:
		return []string{"requeue", "drop"}
	case DecisionAbuseReview:
		return []string{"close", "escalate"}
	case DecisionNPCAnomaly:
		return []string{"unban", "confirm"}
	}
	return nil
}

func validDecisionAction(kind, action string) bool {
	for _, a := range decisionActions(kind) {
		if a == action {
			return true
		}
	}
	return false
}

// applyDecision is the real act behind the word: it changes world state and
// returns a sentence describing what changed. Anything that cannot be done
// any more (the implant was already cleaned, the ban already lifted) is an
// error, never a silent success.
func (w *World) applyDecision(d *Decision, action string) (string, error) {
	switch d.Kind {
	case DecisionIntrusion:
		return w.resolveIntrusion(d, action)
	case DecisionAssistantStuck:
		return w.resolveAssistantStuck(d, action)
	case DecisionAbuseReview:
		return w.resolveAbuseReview(d, action)
	case DecisionNPCAnomaly:
		return w.resolveNPCAnomaly(d, action)
	}
	return "", fmt.Errorf("decision %s has an unknown kind %q", d.ID, d.Kind)
}

func (w *World) resolveIntrusion(d *Decision, action string) (string, error) {
	dst := w.Devices[d.Subject]
	if dst == nil {
		return "", fmt.Errorf("intrusion decision %s names a host that is gone: %s", d.ID, d.Subject)
	}
	switch action {
	case "clean":
		n, err := w.CleanFoothold(d.Subject)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("removed %d live implant(s) from %s (process, payload and persistence line)", n, dst.Hostname), nil
	case "escalate":
		live := 0
		for _, f := range w.FootholdsFor(d.Subject) {
			if f.Dead {
				continue
			}
			live++
			// tell the machine's own sensors what they missed, on the record
			dst.Alertf("operator", "escalation", "high",
				"operator escalated an unnoticed implant: %s as %s from %s", f.File, f.Account, f.Actor)
			if sc := w.Devices[f.Actor]; sc != nil {
				ip := sc.sourceIPFor(dst)
				if ip == "" {
					ip = sc.WANIP()
				}
				if ip != "" {
					dst.Ban(ip, sc.Hostname, "operator escalation of an unnoticed intrusion", time.Hour)
				}
				w.Remember(dst.Owner, f.Actor, "exploit", "escalated an unnoticed intrusion", -10)
			}
		}
		if live == 0 {
			return "", fmt.Errorf("%s has no live implant left to escalate", dst.Hostname)
		}
		return fmt.Sprintf("escalated %d implant(s) on %s: the sensors were told and the actor's address is banned for 1h", live, dst.Hostname), nil
	}
	return "", fmt.Errorf("unknown action %q for %s", action, DecisionIntrusion)
}

func (w *World) resolveAssistantStuck(d *Decision, action string) (string, error) {
	j := w.Job(d.Subject)
	switch action {
	case "requeue":
		if j == nil {
			n := w.clearStuckTasks(d.Subject)
			if n == 0 {
				return "", fmt.Errorf("job %s is gone and no task references it", d.Subject)
			}
			return fmt.Sprintf("job %s no longer exists; cleared %d dangling task(s)", d.Subject, n), nil
		}
		if j.Accepted != "assistant" {
			return "", fmt.Errorf("job %s is no longer held by the assistant (accepted by %q)", j.ID, j.Accepted)
		}
		j.Accepted = ""
		n := w.clearStuckTasks(j.ID)
		return fmt.Sprintf("returned job %s to the board and closed %d stuck task(s); a person can take it", j.ID, n), nil
	case "drop":
		n := w.clearStuckTasks(d.Subject)
		if n == 0 {
			return "", fmt.Errorf("no queued task references job %s", d.Subject)
		}
		return fmt.Sprintf("closed %d stuck task(s) for job %s without returning it to the board", n, d.Subject), nil
	}
	return "", fmt.Errorf("unknown action %q for %s", action, DecisionAssistantStuck)
}

func (w *World) resolveAbuseReview(d *Decision, action string) (string, error) {
	c := w.CaseByID(d.Subject)
	if c == nil {
		return "", fmt.Errorf("no such abuse case: %s", d.Subject)
	}
	if caseTerminal(c.Stage) {
		return "", fmt.Errorf("case %s is already %s", c.ID, c.Stage)
	}
	dk := w.deskByID(c.DeskID)
	if dk == nil {
		return "", fmt.Errorf("no desk holds case %s", c.ID)
	}
	switch action {
	case "close":
		c.Action = "none"
		w.caseStage(c, StageClosed, "closed by operator review: the desk was not operational")
		return fmt.Sprintf("closed case %s (%s)", c.ID, c.SubjectIP), nil
	case "escalate":
		w.escalate(dk, c, "escalated by operator review while the desk was offline")
		return fmt.Sprintf("escalated case %s (%s) to law enforcement", c.ID, c.SubjectIP), nil
	}
	return "", fmt.Errorf("unknown action %q for %s", action, DecisionAbuseReview)
}

func (w *World) resolveNPCAnomaly(d *Decision, action string) (string, error) {
	hostID, ip, ok := strings.Cut(d.Subject, "|")
	if !ok || hostID == "" || ip == "" {
		return "", fmt.Errorf("malformed ban decision subject %q", d.Subject)
	}
	dst := w.Devices[hostID]
	if dst == nil {
		return "", fmt.Errorf("no such host: %s", hostID)
	}
	switch action {
	case "unban":
		if !dst.Unban(ip) {
			return "", fmt.Errorf("%s holds no active ban on %s", dst.Hostname, ip)
		}
		return fmt.Sprintf("lifted %s's ban on %s", dst.Hostname, ip), nil
	case "confirm":
		if !dst.IsBanned(ip) {
			return "", fmt.Errorf("%s holds no active ban on %s", dst.Hostname, ip)
		}
		b := dst.Ban(ip, "", "ban confirmed by operator review (permanent)", 0)
		return fmt.Sprintf("confirmed %s's ban on %s as permanent (strike %d)", dst.Hostname, ip, b.Count), nil
	}
	return "", fmt.Errorf("unknown action %q for %s", action, DecisionNPCAnomaly)
}

// ---- helpers ---------------------------------------------------------------

// clearStuckTasks closes the queued assistant tasks for a job, returning how
// many it closed. A task left behind by the assistant is real state; the
// operator's resolution is what removes it.
func (w *World) clearStuckTasks(jobID string) int {
	n := 0
	for _, t := range w.Tasks {
		if t.Done || t.Kind != "assist-job" || t.JobID != jobID {
			continue
		}
		t.Done = true
		t.DoneAt = w.Sim
		n++
	}
	return n
}

// currentJobVerify is the predicate the assistant would be asked to satisfy
// right now: the current stage's verifier for a staged mission, otherwise the
// job's own single-shot verifier.
func currentJobVerify(j *Job) string {
	if len(j.Stages) > 0 {
		if j.StageIdx >= 0 && j.StageIdx < len(j.Stages) {
			return j.Stages[j.StageIdx].Verify
		}
		return j.Stages[len(j.Stages)-1].Verify
	}
	return j.Verify
}

// assistantCapable reports whether the assistant can perform a verifier right
// now, given the tracks it has learned. It consults the same gate the
// delegation refusal (TaskAssistant) and the worker (assistantAct) use —
// assistantTrackGates in agents.go — so a verifier the assistant would be
// refused is exactly the verifier that queues an assistant-stuck decision
// here, and a newly trained track closes both at once.
func (w *World) assistantCapable(verify string) bool {
	track, learnable := w.assistantMissingTrack(verify)
	return learnable && track == ""
}
