package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// §1 反制循环 — the assistant as the household's automatic countermeasure
// executor.
//
// Forensics without an executor stops at knowing; the spec's countermeasure
// loop ends in 对攻击者进行反制 / 封锁 / 报告. This file is that last mile,
// walked by the household assistant when the owner has armed it (`assist
// defend on`, Player.AssistDefend) and paid for the security track. While
// armed, AssistantWork calls assistantDefense once per tick, and the
// assistant performs at most ONE real countermeasure step per cycle:
//
//	clean  — a live implant on a household box is removed for real
//	         (CleanFoothold: process, payload, persistence line);
//	rotate — the credential an intrusion used is burned for real
//	         (Device.ChangePassword, so the account and /etc/shadow agree);
//	block  — the attacker's source is really dropped: a ban on the device
//	         that saw it plus one on the household's edge router. The ban
//	         mechanism is what Dial/Reach actually enforce — the firewall
//	         rule language has no per-source match, so a UCI rule claiming
//	         one would be decoration;
//	report — a real abuse case is filed through §34 (World.ReportAbuse)
//	         from the machine holding the evidence.
//
// Every step is logged on the device and on the assistant node, recorded in
// the evidence graph (World.Record, weight 0 — defending yourself is not an
// offence), and charged to the owner's own account at the prices below. A
// broke owner buys no countermeasures: the step is skipped silently and
// retried when the budget is there — nothing is minted and nothing is free.
//
// A detection is countered once per kind of answer: the executor keeps a
// bounded processed-set as a plain file on the assistant node
// (/home/assistant/defense.log — the owner can read it when they ssh in,
// §24), so a save carries it and a reboot keeps it.
// ---------------------------------------------------------------------------

// Countermeasure prices, in cents, charged to the owner's account per step.
const (
	DefendBlockCost  int64 = 300 // reading the alerts and writing the drops
	DefendRotateCost int64 = 200 // burning one leaked credential
	DefendCleanCost  int64 = 500 // forensics-grade implant removal
	DefendReportCost int64 = 150 // filing the complaint with the network
)

// defendDonePath is the assistant node's own record of what it already
// countered — plain data in the node's filesystem, so gob carries it like
// every other file.
const defendDonePath = "/home/assistant/defense.log"

// defendDoneMax bounds the processed-set; the oldest lines fall off.
const defendDoneMax = 200

// The detection shapes the executor reads. The failure threshold mirrors the
// jail fail2ban ships (3 in 10 minutes): the assistant is the household's
// fallback for the boxes that run no jail of their own.
const (
	defendFailWindow    = 10 * time.Minute
	defendFailThreshold = 3
	defendAlertWindow   = 2 * time.Hour  // alerts older than this are history
	defendBanTime       = 24 * time.Hour // a countermeasure ban is a day, not a jail's ten minutes
	defendReportWindow  = 6 * time.Hour  // how far back the filed evidence may reach
)

// defenseFinding is one detection and the step that answers it.
type defenseFinding struct {
	step    string // clean|rotate|block|report
	dev     *Device
	key     string // processed-set key; one step per key, ever
	src     string // attacker address (block/report)
	srcHost string
	account string // implicated credential (rotate)
	cost    int64
	detail  string
}

func (f *defenseFinding) subject() string {
	if f.account != "" {
		return f.account + "@" + f.dev.Hostname
	}
	if f.src != "" {
		return f.src
	}
	return f.dev.Hostname
}

// assistantDefense is the executor's entry point, called once from
// AssistantWork. It walks the players in name order and stops after the
// first step actually performed, so a cycle is one countermeasure, never a
// sweep.
func (w *World) assistantDefense() {
	names := make([]string, 0, len(w.Players))
	for n := range w.Players {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := w.Players[n]
		if !p.AssistDefend || !w.HasTrack("security") {
			continue
		}
		a := w.Devices[p.Assistant]
		if a == nil || !a.Powered() {
			continue
		}
		if f := w.defenseScan(p, defendProcessed(a)); f != nil {
			if w.defensePerform(p, a, f) {
				return
			}
		}
	}
}

// defenseScan reads the household's own §33 state — its own devices only —
// and returns the most urgent unanswered detection. Priority is the incident
// order: an implant in residence, then the credential it used, then an
// attack still coming in, then a ban worth telling the network about.
func (w *World) defenseScan(p *Player, done map[string]bool) *defenseFinding {
	var devices []*Device
	for _, id := range w.Order {
		if d := w.Devices[id]; d != nil && d.Owner == p.Name {
			devices = append(devices, d)
		}
	}
	for _, d := range devices {
		for _, f := range w.FootholdsFor(d.ID) {
			if f.Dead {
				continue
			}
			key := fmt.Sprintf("clean|%s|%s", d.ID, f.File)
			if done[key] {
				continue
			}
			return &defenseFinding{step: "clean", dev: d, key: key, cost: DefendCleanCost,
				detail: fmt.Sprintf("live implant on %s: %s running as %s (pid %d, persistence %s)",
					d.Hostname, f.File, f.Account, f.PID, f.Cron)}
		}
	}
	for _, d := range devices {
		for _, f := range w.FootholdsFor(d.ID) {
			if !f.Dead || f.Account == "" || d.FindUser(f.Account) == nil {
				continue
			}
			key := fmt.Sprintf("rotate|%s|%s", d.ID, f.Account)
			if done[key] {
				continue
			}
			return &defenseFinding{step: "rotate", dev: d, key: key, account: f.Account, cost: DefendRotateCost,
				detail: fmt.Sprintf("credential %s@%s opened this machine to an intrusion from %s; burning it",
					f.Account, d.Hostname, f.Actor)}
		}
	}
	for _, d := range devices {
		if f := w.defenseBlockFinding(d, done); f != nil {
			return f
		}
	}
	for _, d := range devices {
		for _, b := range d.ActiveBans() {
			key := "report|" + b.IP
			if done[key] {
				continue
			}
			return &defenseFinding{step: "report", dev: d, key: key, src: b.IP, srcHost: b.Host, cost: DefendReportCost,
				detail: fmt.Sprintf("%s holds an active ban on %s (%s) — telling the network that answers for it",
					d.Hostname, b.IP, b.Reason)}
		}
	}
	return nil
}

// defenseBlockFinding reads one machine's detection state for an attack that
// is still unanswered: an IDS alert naming a source the machine has not
// banned, or a brute-force burst its own logs prove. A source already banned
// here needs no block — the drop is the ban, and it is already in force.
func (w *World) defenseBlockFinding(d *Device, done map[string]bool) *defenseFinding {
	mk := func(src, host, why string) *defenseFinding {
		if src == "" || d.IsBanned(src) {
			return nil
		}
		key := fmt.Sprintf("block|%s|%s", d.ID, src)
		if done[key] {
			return nil
		}
		return &defenseFinding{step: "block", dev: d, key: key, src: src, srcHost: host,
			cost: DefendBlockCost, detail: why}
	}
	for _, al := range d.Alerts() {
		if al.Src == "" || w.Sim.Sub(al.At) > defendAlertWindow {
			continue
		}
		if f := mk(al.Src, al.SrcHost, fmt.Sprintf("%s alert on %s: %s", al.Tool, d.Hostname, al.Msg)); f != nil {
			return f
		}
	}
	seen := map[string]bool{}
	s := d.Sec()
	for i, at := range s.Fails {
		if at.Before(w.Sim.Add(-defendFailWindow)) {
			continue
		}
		ip := s.FailIP[i]
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		if n := d.FailsFrom(ip, defendFailWindow); n >= defendFailThreshold {
			if f := mk(ip, "", fmt.Sprintf("%d failed authentications from %s on %s inside %s",
				n, ip, d.Hostname, HumanAge(defendFailWindow))); f != nil {
				return f
			}
		}
	}
	return nil
}

// defensePerform carries out one finding: the real act first, then the
// charge, the logs, the evidence and the processed mark. An act that fails
// is neither charged nor marked — the detection stays fresh and the next
// cycle tries again.
func (w *World) defensePerform(p *Player, a *Device, f *defenseFinding) bool {
	acc := w.Bank.Accts[p.Name]
	if acc == nil || acc.Balance < f.cost {
		return false // a broke owner buys no countermeasures; nothing is minted
	}
	switch f.step {
	case "clean":
		if _, err := w.CleanFoothold(f.dev.ID); err != nil {
			return false
		}
		w.Record("proc", p.Name, a.FirstLANIP(), f.dev.ID, "assistant countermeasure (clean): "+f.detail, 0)
	case "rotate":
		newPass := randPass(fmt.Sprintf("defend-rotate|%s|%s|%d", f.dev.ID, f.account, w.TickCount))
		if err := f.dev.ChangePassword(f.account, newPass); err != nil {
			return false
		}
		w.Record("auth", p.Name, a.FirstLANIP(), f.dev.ID, "assistant countermeasure (rotate): "+f.detail, 0)
	case "block":
		f.dev.Ban(f.src, f.srcHost, "assistant countermeasure: "+f.detail, defendBanTime)
		if r := w.Devices[p.Router]; r != nil && r != f.dev {
			r.Ban(f.src, f.srcHost, "assistant countermeasure (edge): "+f.detail, defendBanTime)
		}
		w.Record("ids", p.Name, a.FirstLANIP(), f.dev.ID, "assistant countermeasure (block): "+f.detail, 0)
	case "report":
		if _, err := w.ReportAbuse(f.dev, p.Name, f.src, "attack",
			"automatic countermeasure report from the household assistant: "+f.detail, defendReportWindow); err != nil {
			return false
		}
		w.Record("ids", p.Name, a.FirstLANIP(), f.dev.ID, "assistant countermeasure (report): "+f.detail, 0)
	default:
		return false
	}
	acc.Balance -= f.cost
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: -f.cost,
		Memo:    "assistant defense: " + f.step + " " + f.subject(),
		Balance: acc.Balance})
	f.dev.Logf("warn", "defense", "assistant countermeasure %s: %s (charged %s $%.2f)",
		f.step, f.detail, p.Name, float64(f.cost)/100)
	a.Logf("info", "defense", "countermeasure %s for %s: %s", f.step, f.dev.Hostname, f.detail)
	w.AddEvent(f.dev.ID, "warn", "defense", "assistant performed %s on %s for %s ($%.2f)",
		f.step, f.dev.Hostname, p.Name, float64(f.cost)/100)
	w.defendMark(a, f.key, f.step+" "+f.subject())
	return true
}

// defendProcessed reads the assistant node's record of countered detections.
func defendProcessed(a *Device) map[string]bool {
	out := map[string]bool{}
	data, ok := a.FS.Read(defendDonePath)
	if !ok {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			out[f[1]] = true
		}
	}
	return out
}

// defendMark appends one line to the processed-set, bounded like any honest
// log: the oldest entries fall off the top.
func (w *World) defendMark(a *Device, key, summary string) {
	data, _ := a.FS.Read(defendDonePath)
	body := string(data) + fmt.Sprintf("%d %s %s\n", w.TickCount, key, summary)
	lines := strings.Split(body, "\n")
	if len(lines) > defendDoneMax {
		lines = lines[len(lines)-defendDoneMax:]
		body = strings.Join(lines, "\n")
	}
	a.FS.Write(defendDonePath, body, 0644, "assistant", "assistant")
}
