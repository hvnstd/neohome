package core

import (
	"fmt"
	"time"
)

// ---- assistant agent: works jobs on its own node, learns ----

func (w *World) AssistantWork() {
	for _, t := range w.Tasks {
		if t.Done || t.Kind != "assist-job" {
			continue
		}
		// one job takes 6 ticks (~3 game minutes) — visible progress, not a wall
		if w.TickCount-t.StartTick < 6 {
			continue
		}
		j := w.Job(t.JobID)
		a := w.Devices[t.DeviceID]
		if j == nil || a == nil {
			t.Done = true
			continue
		}
		owner := a.Owner
		ok := false
		switch j.Verify {
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
			if repo := w.Repos["main"]; repo != nil {
				if p := repo.Pkgs["busybox"]; p != nil {
					a.InstallPkg(p)
					ok = true
				}
			}
		}
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
			u.Pass = "X7k!pLq92mz"
			npc.Logf("info", "passwd", "password changed for user devops")
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

func (w *World) AssistantKeyTrusted(d *Device) bool {
	for _, p := range w.Players {
		if p.Assistant == d.ID || (d.Owner == p.Name && p.Assistant != "") {
			return true
		}
	}
	return false
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
