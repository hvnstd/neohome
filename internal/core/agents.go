package core

import (
	"fmt"
	"strings"
	"time"
)

// ---- assistant agent: works jobs on its own node, learns ----

func (w *World) AssistantWork() {
	for _, t := range w.Tasks {
		if t.Done || t.Kind != "assist-job" {
			continue
		}
		// one job takes 6 ticks (~3 game minutes) of the node's own CPU —
		// visible progress, not a wall. §17: a busy node is a slow node, so the
		// job advances by the share its processes actually get.
		if a := w.Devices[t.DeviceID]; a != nil {
			t.Progress += a.CPUShare()
		} else {
			t.Progress += 1
		}
		if t.Progress < 6 {
			continue
		}
		j := w.Job(t.JobID)
		a := w.Devices[t.DeviceID]
		if j == nil || a == nil {
			t.Done = true
			continue
		}
		owner := a.Owner
		// Staged missions go through one stage per work cycle, paid by the
		// same hand as the player's `job advance` — see payStage. Only
		// stages the assistant can actually perform are ever delegated
		// (TaskAssistant refuses the rest up front).
		if len(j.Stages) > 0 {
			if j.StageIdx >= len(j.Stages) {
				t.Done = true
				continue
			}
			st := j.Stages[j.StageIdx]
			if w.assistantAct(a, st.Verify) {
				if ok, _ := w.verifyStage(j); ok {
					paid, msg, err := w.payStage(owner, j)
					if err != nil {
						t.Done = true
						w.AddEvent(a.ID, "warn", "assistant", "assistant gave up on %s: %v", j.ID, err)
						continue
					}
					a.Logf("info", "assistant", "job %s stage %s finished, invoiced $%.2f",
						j.ID, st.Name, float64(paid)/100)
					w.AddEvent(a.ID, "info", "assistant", "assistant finished %s stage %s", j.ID, st.Name)
					_ = msg
					t.Progress = 0
					if j.Done {
						w.assistSkills++
						w.AddEvent(a.ID, "info", "assistant", "assistant finished %s (+skill)", j.Title)
					}
					continue
				}
			}
			t.Done = true
			t.DoneAt = w.Sim
			w.AddEvent(a.ID, "warn", "assistant", "assistant gave up on %s stage %s (blocked)", j.ID, st.Name)
			continue
		}
		ok := w.assistantAct(a, j.Verify)
		t.Done = true
		t.DoneAt = w.Sim
		j.Done = ok
		if ok {
			acc := w.Bank.Accts[owner]
			if acc != nil {
				acc.Balance += j.Pay
				acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: j.Pay, Memo: "assistant completed " + j.ID, Balance: acc.Balance})
			}
			// growth: real recorded skill, reflected in assistant status text
			w.assistSkills++
			a.Logf("info", "assistant", "job %s finished, invoiced $%.2f", j.ID, float64(j.Pay)/100)
			w.AddEvent(a.ID, "info", "assistant", "assistant finished %s (+skill)", j.Title)
		} else {
			w.AddEvent(a.ID, "warn", "assistant", "assistant gave up on %s (blocked)", j.ID)
		}
	}
}

// assistantAct performs one stage the assistant knows how to do: the same
// two fixes the legacy single-shot path always had. Anything else is not a
// failure of effort but of capability — TaskAssistant refuses such stages
// before they are ever delegated.
func (w *World) assistantAct(a *Device, verify string) bool {
	ok := false
	switch verify {
	case "dns-fix":
		ok = !w.FaultDNSActive()
		if !ok {
			// assistant fixes it itself: writes a valid resolv-file and restarts dnsmasq
			r := w.Devices[w.CauseFault.DeviceID]
			if r != nil {
				r.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
				if data, has := r.FS.Read("/etc/dnsmasq.conf"); has {
					s := string(data)
					s = replaceOnce(s, "resolv-file=/var/run/dnsmasq/resolv.conf", "resolv-file=/etc/dnsmasq.upstream")
					r.FS.Write("/etc/dnsmasq.conf", s, 0644, "root", "root")
				}
				r.RestartService("dnsmasq")
				w.CauseFault.Active = false
				ok = true
			}
		}
	case "pkg-busybox":
		// the assistant installs from the catalogue it can reach, and even
		// it goes through the payload format (InstallRendered)
		if p := w.FindPkg("busybox"); p != nil {
			for _, r := range w.Repos {
				if r.Pkgs[p.Name] != nil && r.Pkgs[p.Name] == p {
					if _, err := w.InstallRendered(a, r, p); err == nil {
						ok = true
					}
					break
				}
			}
		}
	}
	return ok
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func (w *World) Job(id string) *Job {
	for _, j := range w.Jobs.List {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// NPCPatrol: mara notices scanning, updates chat — world reacts to player
// actions with real evidence (her ids logs), not scripted disasters.
func (w *World) NPCPatrol() {
	npc := w.Devices["npc-pc"]
	if npc == nil {
		return
	}
	data, ok := npc.FS.Read("/var/log/syslog")
	if !ok {
		return
	}
	recent := string(data)
	if countOccurrences(recent, "scan") > 2 {
		w.ChatPost("#local", "mara-bot", "someone has been poking my box... changing all my passwords tonight")
		// she hardens: rotating the weak devops password — closing the vuln honestly
		if u := npc.FindUser("devops"); u != nil && u.Pass == "Summer2024!" {
			_ = npc.ChangePassword("devops", "X7k!pLq92mz")
			w.AddEvent(npc.ID, "info", "npc", "mara rotated credentials after repeated scans")
		}
	}
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i < len(s); {
		j := indexOf(s[i:], sub)
		if j < 0 {
			break
		}
		n++
		i += j + len(sub)
	}
	return n
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---- chat ----

func (w *World) ChatBots() {
	w.Chat.History = append(w.Chat.History,
		ChatMsg{At: w.Sim.Add(-40 * time.Minute), Chan: "#local", Nick: "mira-9", Text: "anyone knows a hacker kid? my wifi is dead again"},
		ChatMsg{At: w.Sim.Add(-35 * time.Minute), Chan: "#local", Nick: "daemon42", Text: "post on the job board, pay them properly"},
		ChatMsg{At: w.Sim.Add(-30 * time.Minute), Chan: "#help", Nick: "sysmods", Text: "welcome to #help — read /etc/dnsmasq.conf before asking, we are not a helpdesk"},
		ChatMsg{At: w.Sim.Add(-11 * time.Minute), Chan: "#local", Nick: "alex", Text: "(that's me. something's wrong with MY dns. sites error, IPs ping fine)"},
	)
}

func (w *World) ChatPost(ch, nick, text string) {
	m := ChatMsg{At: w.Sim, Chan: ch, Nick: nick, Text: text}
	w.Chat.History = append(w.Chat.History, m)
	if len(w.Chat.History) > 300 {
		w.Chat.History = w.Chat.History[len(w.Chat.History)-300:]
	}
	chatBroadcast(m)
}

// ---- assistant trust ----

// AssistantKeyTrusted answers the only question sshd can answer here: does
// the session that is connecting belong to the owner of THIS assistant? The
// source device identifies the player — a stranger's box is not the owner's,
// so the seeded key is not a skeleton key for the whole world.
func (w *World) AssistantKeyTrusted(src, dst *Device) bool {
	if src == nil || dst == nil || src.Owner == "" {
		return false
	}
	p := w.Players[src.Owner]
	return p != nil && p.Assistant == dst.ID
}

// KeyTrusted answers with files what AssistantKeyTrusted answers with
// topology: does the connecting account hold a private key whose public half
// is listed in the target account's authorized_keys? Both files are read as
// the daemons would read them — the client offering its own pubkey, the
// server consulting its own authorized_keys as root — and only an exact key
// body match counts (comments may differ, keys may not).
func (w *World) KeyTrusted(src *Device, srcUser string, dst *Device, dstUser string) bool {
	if src == nil || dst == nil || srcUser == "" || dstUser == "" {
		return false
	}
	su := src.FindUser(srcUser)
	du := dst.FindUser(dstUser)
	if su == nil || du == nil || su.Home == "" || du.Home == "" {
		return false
	}
	var bodies []string
	for _, p := range src.FS.List(su.Home + "/.ssh") {
		if !strings.HasSuffix(p, ".pub") {
			continue
		}
		data, exists, allowed := src.FS.ReadPathAs(p, su)
		if !exists || !allowed {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(strings.TrimSpace(line)); len(f) >= 2 {
				bodies = append(bodies, f[1])
			}
		}
	}
	if len(bodies) == 0 {
		return false
	}
	data, ok := dst.FS.Read(du.Home + "/.ssh/authorized_keys")
	if !ok {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && containsKeyBody(bodies, f[1]) {
			return true
		}
	}
	return false
}

func containsKeyBody(bodies []string, body string) bool {
	for _, b := range bodies {
		if b == body {
			return true
		}
	}
	return false
}

// SeedAssistantAccess makes the assistant's machine reachable the way the
// spec describes it (§24: the player can SSH in and look at the assistant's
// working environment). The trust side is already seeded — the assistant's
// authorized_keys holds the owner's key — but a host with no sshd_config is a
// host that still asks for a password, so the key path was unreachable and
// the node demanded a password nobody has. Key-only access is the directive
// this world actually enforces, so it is the only one written.
func SeedAssistantAccess(w *World, d *Device) {
	if d == nil {
		return
	}
	d.FS.Write("/etc/ssh/sshd_config", "Port 22\nPasswordAuthentication no\n", 0644, "root", "root")
}

// PlayerFor device owner
func (w *World) PlayerFor(d *Device) *Player {
	for _, p := range w.Players {
		if d.Owner == p.Name {
			return p
		}
	}
	return nil
}

func (w *World) AssistantNodeFor(p *Player) *Device {
	if p == nil {
		return nil
	}
	return w.Devices[p.Assistant]
}

// SimT is kept for compatibility of old call sites; ticks are the clock.
func SimT(t time.Time) int { return int(t.Unix()) }

var _ = fmt.Sprint
