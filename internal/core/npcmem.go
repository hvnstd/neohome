package core

import (
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// NPC memory and relationships (§20/§21) — people remember
//
// Evidence is global; memory is personal. Every hostile or friendly act that
// names someone lands in the people involved's own memory (capped, oldest
// dropped), and repeated acts move a pairwise standing (-100..100). Memory
// is read back in three places, which is what makes it real instead of a
// diary: NPCs reference recent grudges in chat, the market collector will
// not trade with a known attacker, and `people` shows every standing.
//
// Valence is explicit at the call site, never inferred from text: a failed
// login is -3, a sweep is -5, a completed trade is +4, a finished job for an
// NPC client is +5, an exploit is -10, a collected bounty is +6. Who counts
// as a person is NPCNames plus Players, with chat nicks normalized.
// ---------------------------------------------------------------------------

// MemEvent is one thing someone did that someone else remembers.
type MemEvent struct {
	At     time.Time
	Kind   string // auth-fail | auth-ok | scan | trade | job | exploit | bounty
	Actor  string // who did it (person handle or host name)
	Detail string
	Delta  int
}

// NPCMem is one handle's memory, newest last.
type NPCMem struct {
	Events []MemEvent
}

// memCap bounds each handle's memory: old news falls off, deterministically.
const memCap = 100

// grudgeLine is how far a standing must fall before an NPC says so in chat,
// and recallWindow is how fresh the worst event must be to be worth saying.
const grudgeLine = -30

const recallWindow = 24 * time.Hour

// tradeFloor is the standing below which the market collector refuses a
// seller: attacked once too often, and mara stops buying your drops.
const tradeFloor = -50

// personOf normalizes a handle: chat nicks lose their -bot, and only known
// NPCs and players count. Anything else (scan-host, an IP) is scenery that
// can be remembered as an actor but never holds a standing.
func (w *World) personOf(name string) (string, bool) {
	name = strings.TrimSpace(strings.TrimSuffix(name, "-bot"))
	if name == "" {
		return "", false
	}
	for _, n := range w.NPCNames {
		if n == name {
			return name, true
		}
	}
	if _, ok := w.Players[name]; ok {
		return name, true
	}
	return "", false
}

// memFor returns a handle's memory, creating it on first use so an older
// save without one never panics.
func (w *World) memFor(who string) *NPCMem {
	if w.NPCMem == nil {
		w.NPCMem = map[string]*NPCMem{}
	}
	m := w.NPCMem[who]
	if m == nil {
		m = &NPCMem{}
		w.NPCMem[who] = m
	}
	return m
}

// pairKey names the directed score of how a feels about b. Relationships
// are asymmetric on purpose: fearing someone is not the same as being
// feared by them, so each direction moves only when its holder remembers.
func pairKey(a, b string) string {
	return a + "\x00" + b
}

// Standing reports the pairwise score, -100..100, 0 for strangers.
func (w *World) Standing(a, b string) int {
	if w.Standings == nil {
		return 0
	}
	return w.Standings[pairKey(a, b)]
}

// Remember files what actor did to (or for) who: an event in who's memory
// and, when both sides are people, a move of their standing. Self-actions
// are facts without a relationship and move nothing.
func (w *World) Remember(who, actor, kind, detail string, delta int) {
	who, ok := w.personOf(who)
	if !ok || actor == "" {
		return
	}
	m := w.memFor(who)
	m.Events = append(m.Events, MemEvent{At: w.Sim, Kind: kind, Actor: actor, Detail: detail, Delta: delta})
	if len(m.Events) > memCap {
		m.Events = m.Events[len(m.Events)-memCap:]
	}
	actorHandle, ok := w.personOf(actor)
	if !ok || actorHandle == who {
		return
	}
	if w.Standings == nil {
		w.Standings = map[string]int{}
	}
	v := w.Standings[pairKey(who, actorHandle)] + delta
	if v > 100 {
		v = 100
	}
	if v < -100 {
		v = -100
	}
	w.Standings[pairKey(who, actorHandle)] = v
}

// LastMemory returns the newest event in a handle's memory, if any.
func (w *World) LastMemory(who string) (MemEvent, bool) {
	if m := w.memFor(who); len(m.Events) > 0 {
		return m.Events[len(m.Events)-1], true
	}
	return MemEvent{}, false
}

// People lists every handle the world knows by name: NPCs first, then
// players, each sorted.
func (w *World) People() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range w.NPCNames {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	var players []string
	for n := range w.Players {
		if !seen[n] {
			seen[n] = true
			players = append(players, n)
		}
	}
	sort.Strings(players)
	return append(out, players...)
}

// grudgeRecall finds what the speaker owes an apology for: the worst NPC
// standing at or below the line, but only when its worst event is fresh
// enough to still sting. Empty means nothing to say.
func (w *World) grudgeRecall(speaker string) (nick, line string) {
	who, ok := w.personOf(speaker)
	if !ok {
		return "", ""
	}
	worst, worstWith := 0, ""
	for _, p := range w.People() {
		if p == who {
			continue
		}
		// directed the other way: what p holds against the speaker is
		// what p might say out loud
		if v := w.Standing(p, who); v < worst {
			worst, worstWith = v, p
		}
	}
	if worst > grudgeLine || worstWith == "" {
		return "", ""
	}
	last, ok := w.LastMemory(worstWith)
	if !ok || w.Sim.Sub(last.At) > recallWindow {
		return "", ""
	}
	nick = worstWith
	if nick == "mara" {
		nick = "mara-bot"
	}
	return nick, "still watching you, " + who + ". my logs remember " + last.Detail
}
