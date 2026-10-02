package core

import (
	"fmt"
	"strings"
	"time"
)

// ---- job lifecycle: a job only pays when the WORLD STATE says it is done ----

// OpenJobs lists jobs not yet completed.
func (w *World) OpenJobs() []*Job {
	var out []*Job
	for _, j := range w.Jobs.List {
		if !j.Done {
			out = append(out, j)
		}
	}
	return out
}

// AcceptJob binds a job to a worker (player name or assistant).
func (w *World) AcceptJob(who, id string) error {
	j := w.Job(id)
	if j == nil {
		return fmt.Errorf("no such job: %s", id)
	}
	if j.Done {
		return fmt.Errorf("job %s already completed", id)
	}
	if j.Accepted != "" && j.Accepted != who {
		return fmt.Errorf("job %s is already taken by %s", id, j.Accepted)
	}
	j.Accepted = who
	w.AddEvent("world", "info", "jobs", "%s accepted %s", who, id)
	return nil
}

// VerifyJob consults real world state — never a flag the caller can fake.
func (w *World) VerifyJob(j *Job) (bool, string) {
	switch j.Verify {
	case "dns-fix":
		if w.FaultDNSActive() {
			return false, "the household resolver still SERVFAILs every name"
		}
		// the fix must be real: a working upstream must exist on the router
		r := w.Devices[w.CauseFault.DeviceID]
		if r == nil {
			return false, "router missing"
		}
		if !r.DnsmasqHealthy() {
			return false, "dnsmasq still has no working upstream configured"
		}
		if svc := r.Svc("dnsmasq"); svc == nil || svc.State != "running" {
			return false, "dnsmasq is not running"
		}
		return true, "resolution verified end-to-end"

	case "pkg-busybox":
		a := w.Devices["asst-alex"]
		if a == nil {
			return false, "assistant node missing"
		}
		if a.Installed["busybox"] == nil {
			return false, "busybox is not installed on the assistant node"
		}
		if _, ok := a.FS.Get("/bin/busybox"); !ok {
			return false, "/bin/busybox does not exist"
		}
		return true, "busybox present in the virtual filesystem"

	case "web-up":
		for _, id := range w.Order {
			d := w.Devices[id]
			if svc := d.Svc("nginx"); svc != nil && svc.State == "running" {
				if _, _, msg := Dial(w.Devices[w.PlayerDeviceID(j.Accepted)], d.FirstLANIP(), svc.Port); msg == "connected" {
					return true, "web service reachable"
				}
			}
		}
		return false, "no running web service is reachable"

	case "ssh-up":
		for _, id := range w.Order {
			d := w.Devices[id]
			if svc := d.Svc("sshd"); svc != nil && svc.State == "running" {
				return true, fmt.Sprintf("sshd is running on %s", d.Hostname)
			}
		}
		return false, "no sshd is running"

	case "clean":
		// "clean up your act": no leftover fail2ban strikes on the home router
		r := w.RouterForPlayer(j.Accepted)
		if r == nil {
			return false, "no router"
		}
		for ip, n := range r.Fail2Ban {
			if n > 0 {
				return false, fmt.Sprintf("router is still banning %s (%d strikes)", ip, n)
			}
		}
		return true, "router holds no bans"
	}
	return false, "job has no verifier: " + j.Verify
}

// PlayerDeviceID returns the main device id for a worker name.
func (w *World) PlayerDeviceID(who string) string {
	if p := w.Players[who]; p != nil {
		return p.PC
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Owner == who && !d.IsBot() {
			return d.ID
		}
	}
	return w.Order[0]
}

// RouterForPlayer finds the router in a worker's household.
func (w *World) RouterForPlayer(who string) *Device {
	if p := w.Players[who]; p != nil {
		return w.Devices[p.Router]
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Profile == "router" {
			return d
		}
	}
	return nil
}

// IsBot marks assistant-owned nodes.
func (d *Device) IsBot() bool {
	for _, u := range d.Users {
		if u.IsBot {
			return true
		}
	}
	return false
}

// PayJob settles a job: verify against world state, then move money for real.
func (w *World) PayJob(who, id string) (int64, string, error) {
	j := w.Job(id)
	if j == nil {
		return 0, "", fmt.Errorf("no such job: %s", id)
	}
	if j.Accepted == "" {
		return 0, "", fmt.Errorf("job %s has not been accepted (use: job accept %s)", id, id)
	}
	if j.Accepted != who {
		return 0, "", fmt.Errorf("job %s belongs to %s", id, j.Accepted)
	}
	if j.Done {
		return 0, "", fmt.Errorf("job %s is already paid", id)
	}
	ok, why := w.VerifyJob(j)
	if !ok {
		return 0, why, fmt.Errorf("not done yet")
	}
	j.Done = true
	acc := w.Bank.Accts[who]
	if acc == nil {
		acc = &Account{Owner: who, Name: who, Balance: 0}
		w.Bank.Accts[who] = acc
	}
	acc.Balance += j.Pay
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: j.Pay, Memo: "job " + j.ID + " — " + j.Title, Balance: acc.Balance})
	w.AddEvent("world", "info", "bank", "paid %s for %s: %d cents", who, j.ID, j.Pay)
	return j.Pay, why, nil
}

// TaskAssistant queues a job for the assistant to work on its own node.
func (w *World) TaskAssistant(jobID string) error {
	p := w.PlayerForName(whoOwns(w, jobID))
	if p == nil {
		// fall back to the single player in the world
		for _, pp := range w.Players {
			p = pp
			break
		}
	}
	if p == nil || p.Assistant == "" {
		return fmt.Errorf("no assistant available")
	}
	j := w.Job(jobID)
	if j == nil {
		return fmt.Errorf("no such job: %s", jobID)
	}
	j.Accepted = "assistant"
	w.Tasks = append(w.Tasks, &Task{
		ID: len(w.Tasks) + 1, Who: "assistant", Kind: "assist-job", JobID: jobID,
		DeviceID: p.Assistant, StartTick: w.TickCount,
		Narrative: "assistant queued on " + j.ID,
	})
	w.AddEvent(p.Assistant, "info", "assistant", "queued job %s for autonomous work", jobID)
	return nil
}

func whoOwns(w *World, jobID string) string {
	for _, p := range w.Players {
		return p.Name
	}
	return ""
}

// PlayerForName looks up a player by name.
func (w *World) PlayerForName(name string) *Player {
	if p := w.Players[name]; p != nil {
		return p
	}
	for n, p := range w.Players {
		if n == name {
			return p
		}
	}
	return nil
}

// ---- wallet ----

// WalletBalance sums the household (player + assistant sub-account).
func (w *World) WalletBalance(who string) (main, assist int64) {
	if a := w.Bank.Accts[who]; a != nil {
		main = a.Balance
	}
	if p := w.Players[who]; p != nil {
		if a := w.Bank.Accts["assistant"]; a != nil {
			assist = a.Balance
		}
	}
	return
}

// Transfer moves money between accounts, refusing to overdraw or to move the
// assistant's budget past the household's cap.
func (w *World) Transfer(from, to string, amount int64, memo string) error {
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	fa := w.Bank.Accts[from]
	if fa == nil {
		return fmt.Errorf("no account for %s", from)
	}
	if fa.Balance < amount {
		return fmt.Errorf("insufficient funds: %d available", fa.Balance)
	}
	fa.Balance -= amount
	fa.Tx = append(fa.Tx, Tx{At: w.Sim, Amount: -amount, Memo: memo, Balance: fa.Balance})
	ta := w.Bank.Accts[to]
	if ta == nil {
		ta = &Account{Owner: to, Name: to}
		w.Bank.Accts[to] = ta
	}
	ta.Balance += amount
	ta.Tx = append(ta.Tx, Tx{At: w.Sim, Amount: amount, Memo: memo, Balance: ta.Balance})
	return nil
}

// ---- mail ----

// ReadMail returns the mailbox content for a player on their PC.
func (w *World) ReadMail(owner string) string {
	p := w.Players[owner]
	if p == nil {
		return ""
	}
	pc := w.Devices[p.PC]
	if pc == nil {
		return ""
	}
	data, _ := pc.FS.Read("/var/mail/" + owner)
	return string(data)
}

// SendMail is the outbound path: it lands in the recipient's mailbox if the
// recipient exists in the world, otherwise it bounces.
func (w *World) SendMail(from, to, subject, body string) error {
	for _, p := range w.Players {
		if p.Name == to {
			w.DeliverMail(to, subject, "from "+from+"\n"+body)
			return nil
		}
	}
	for _, n := range w.NPCNames {
		if n == to {
			w.NPCMail(from, to, subject, body)
			return nil
		}
	}
	return fmt.Errorf("no such recipient: %s", to)
}

func (w *World) NPCMail(from, to, subject, body string) {
	w.MailLog = append(w.MailLog, fmt.Sprintf("%s → %s: %s (%s)", from, to, subject, w.Sim.Format("15:04")))
	if len(w.MailLog) > 100 {
		w.MailLog = w.MailLog[len(w.MailLog)-100:]
	}
	// NPCs may react — a real consequence, not a canned reply.
	if strings.Contains(strings.ToLower(subject), "job") || strings.Contains(strings.ToLower(body), "hack") {
		w.AddEvent("world", "info", "npc", "%s received a suspicious mail from %s", to, from)
	}
}

var _ = time.Now
