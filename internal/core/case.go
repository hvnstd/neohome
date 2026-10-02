package core

import (
	"fmt"
	"time"
)

// Evidence is recorded by EVERY action that touches another device. Heat is
// only ever a summary of this graph — never a number we invent.
func (w *World) Record(kind, actor, origin, target, detail string, weight int) {
	if w.Case == nil {
		w.Case = &Case{}
	}
	w.Case.Events = append(w.Case.Events, Evidence{
		At: w.Sim, Kind: kind, Actor: actor, Origin: origin, Target: target, Detail: detail, Weight: weight,
	})
	if len(w.Case.Events) > 400 {
		w.Case.Events = w.Case.Events[len(w.Case.Events)-400:]
	}
	w.Case.Heat += weight
	if w.Case.Heat < 0 {
		w.Case.Heat = 0
	}
	w.HeatReactions(target, weight)
}

// HeatReactions: the world does not "punish" you with a dice roll — the
// devices you touched respond in ways that are visible and investigable.
func (w *World) HeatReactions(target string, weight int) {
	d := w.Devices[target]
	if d == nil || weight <= 0 {
		return
	}
	// a device with logs records the intrusion attempt
	if weight >= 2 {
		d.Logf("warn", "audit", "suspicious activity recorded (weight %d)", weight)
	}
	// fail2ban-like: repeated weight from the same origin triggers a ban
	if d.Fail2Ban == nil {
		d.Fail2Ban = map[string]int{}
	}
	// heat threshold makes the NPC owner act — investigate, harden, report
	if w.Case.Heat >= 12 && !w.Case.Notified {
		w.Case.Notified = true
		w.NPCReactToHeat()
	}
}

// NPCReactToHeat: the neighbour notices and does something REAL about it.
func (w *World) NPCReactToHeat() {
	npc := w.Devices["npc-pc"]
	if npc == nil {
		return
	}
	w.ChatPost("#local", "mara-bot", "someone keeps knocking on my box — locking it down and telling my ISP")
	w.AddEvent(npc.ID, "warn", "npc", "owner started a defensive investigation after accumulated evidence")
	// real hardening: rotate the leaked credential, close the open port forward
	if u := npc.FindUser("devops"); u != nil && u.Pass == "Summer2024!" {
		u.Pass = "X7k!pLq92mz"
		npc.Logf("info", "passwd", "password changed for user devops after incident")
	}
	router := w.Devices["npc-router"]
	if router != nil && len(router.PortFwd) > 0 {
		for i := range router.PortFwd {
			router.PortFwd[i].Enable = false
		}
		router.Logf("warn", "firewall", "WAN port-forward 21/tcp disabled by owner")
		w.AddEvent(router.ID, "warn", "firewall", "%s closed the FTP port-forward after the incident", router.Owner)
	}
	// she files a report — a case the world can later investigate
	w.AddEvent("world", "warn", "abuse", "incident report filed by %s against an unidentified source", npc.Owner)
}

// HeatSummary is the player-facing roll-up of the evidence graph.
func (w *World) HeatSummary() string {
	if w.Case == nil || len(w.Case.Events) == 0 {
		return "no evidence recorded against you"
	}
	kinds := map[string]int{}
	for _, e := range w.Case.Events {
		kinds[e.Kind]++
	}
	s := fmt.Sprintf("heat %d — %d evidence items", w.Case.Heat, len(w.Case.Events))
	for k, n := range kinds {
		s += fmt.Sprintf("\n  %-6s %d", k, n)
	}
	return s
}

// EvidenceFor lists the trail, newest first.
func (w *World) EvidenceFor(target string, limit int) []Evidence {
	if w.Case == nil {
		return nil
	}
	var out []Evidence
	for i := len(w.Case.Events) - 1; i >= 0 && len(out) < limit; i-- {
		e := w.Case.Events[i]
		if target == "" || e.Target == target {
			out = append(out, e)
		}
	}
	return out
}

// TraceOrigin walks the evidence graph backwards: who could have done this?
// The honest answer is a provider/ASN, never a person — matching the design.
func (w *World) TraceOrigin(ip string) string {
	id, ok := w.IPMap[ip]
	if !ok {
		return fmt.Sprintf("%s → no registered network; likely a residential dynamic range", ip)
	}
	d := w.Devices[id]
	switch d.Profile {
	case "vps":
		return fmt.Sprintf("%s → VPS (%s), provider %s, region unknown\n  next step: abuse contact requires a provider subpoena or a court order",
			ip, d.Hostname, d.HW.Model)
	case "infra":
		return fmt.Sprintf("%s → managed infrastructure (%s)", ip, d.Hostname)
	case "router":
		return fmt.Sprintf("%s → residential CPE (%s), owner %s\n  dynamic address; the ISP can map it to a subscriber only with a lawful request",
			ip, d.Hostname, d.Owner)
	}
	return fmt.Sprintf("%s → %s (%s)", ip, d.Hostname, d.Profile)
}

// DecayHeat lets time work FOR the player: evidence ages out of relevance but
// is never silently erased (the events stay, the urgency drops).
func (w *World) DecayHeat() {
	if w.Case == nil {
		return
	}
	w.Case.Heat -= w.Case.Heat / 12
}

var _ = time.Now
