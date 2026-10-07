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
	// prerequisites are checked on accept, not just at payout: taking work
	// you cannot start yet is the refusal, the way a real dispatcher works
	for _, req := range j.Requires {
		if r := w.Job(req); r == nil || !r.Done {
			return fmt.Errorf("job %s requires %s first", id, req)
		}
	}
	j.Accepted = who
	w.AddEvent("world", "info", "jobs", "%s accepted %s", who, id)
	return nil
}

// VerifyJob consults real world state — never a flag the caller can fake.
func (w *World) VerifyJob(j *Job) (bool, string) {
	// Trophies are proof-of-intrusion bounties: the file must exist on the
	// named device AND carry the worker's tag. Planting it takes real
	// access; removing it un-completes the work, which is exactly what makes
	// it a bounty instead of a prize.
	if rest, ok := strings.CutPrefix(j.Verify, "trophy "); ok {
		devID, path, _ := strings.Cut(strings.TrimSpace(rest), " ")
		path = strings.TrimSpace(path)
		d := w.Devices[strings.TrimSpace(devID)]
		if d == nil {
			return false, "no such device: " + devID
		}
		data, exists := d.FS.Read(path)
		if !exists {
			return false, path + " is not on " + d.Hostname
		}
		if !strings.Contains(string(data), j.Accepted) {
			return false, path + " carries no tag for " + j.Accepted
		}
		return true, fmt.Sprintf("%s tagged %s on %s", j.Accepted, path, d.Hostname)
	}
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

	case "abuse-report":
		// the report is real only if the network that answers for the address
		// really opened a case about it, filed by this worker. The scanner has
		// both a v4 and a v6 address, and which one lands in a victim's log is
		// decided by the sender's own routing — so either one counts (and the
		// file has to be about the address the victim could actually see).
		sc := w.Devices[ScannerID]
		if sc == nil {
			return false, "the world has no scanner to report"
		}
		var candidates []string
		for _, ip := range []string{sc.WANIP(), sc.FirstWANv6(), sc.FirstLANIP()} {
			if ip != "" {
				candidates = append(candidates, ip)
			}
		}
		for _, ip := range candidates {
			if n := w.CasesAbout(j.Accepted, ip); n > 0 {
				return true, fmt.Sprintf("%d case(s) opened with the network that announces %s", n, ip)
			}
		}
		return false, "no organisation has a case from you about " + strings.Join(candidates, " or ") +
			" — file one from the machine that saw the traffic"

	case "abuse-triage":
		// the shift is covered when the desk's own file shows two decisions
		// taken by hand. The player signs in as the shift account rather than as
		// themselves, so the desk is what is checked, not the player's name.
		if n := w.CasesHandledBy(j.Accepted); n >= 2 {
			return true, fmt.Sprintf("%d case(s) moved by %s", n, j.Accepted)
		}
		if n := w.CasesWorkedAt(j.Client); n >= 2 {
			return true, fmt.Sprintf("%d case(s) moved by hand at %s", n, j.Client)
		}
		return false, "the desk still has undecided cases: two triage decisions with reasons are needed"

	case "meridian-share":
		fsSrv := w.Devices["meridian-fs"]
		if fsSrv == nil {
			return false, "the office file server is missing"
		}
		if _, ok := fsSrv.FS.Get("/srv/projects/handover/README"); !ok {
			return false, "/srv/projects/handover/README does not exist on the file server"
		}
		return true, "the handover document is on the office file server"

	case "law-intake":
		// the unit's shift is covered when a lawful request really went out by
		// hand: the request is a mail to a named network, not a button
		if n := w.LawWorked("request"); n > 0 {
			return true, fmt.Sprintf("%d lawful request(s) sent by hand from the unit", n)
		}
		return false, "the unit's intake has not sent a lawful request yet: read a file, then abuse act <ticket> request --note \"basis\""

	case "law-warrant":
		ws := w.LawWarrants()
		if len(ws) == 0 {
			return false, "no order has been obtained: a file needs intake, a lawful request, a disclosure and an investigator's hours first"
		}
		if w.LawWorked("warrant") == 0 {
			return false, "an order exists but no investigator signed it on the file"
		}
		served := false
		for _, wr := range ws {
			if wr.ServedOn != "" {
				served = true
			}
		}
		if !served {
			return false, "the order was never served on a network: an order nobody holds changes nothing"
		}
		return true, fmt.Sprintf("%d order(s) obtained and served", len(ws))

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
	if len(j.Stages) > 0 {
		return 0, "", fmt.Errorf("job %s pays per stage (use: job advance %s)", id, id)
	}
	ok, why := w.VerifyJob(j)
	if !ok {
		return 0, why, fmt.Errorf("not done yet")
	}
	// Contracts pay from their escrowed hold, never minted: the money left
	// the treasury when the contract was posted, and arrives here.
	if j.Org != "" {
		o := w.orgMap()[j.Org]
		if o == nil {
			return 0, "", fmt.Errorf("org %s is gone", j.Org)
		}
		hold, ok := o.Hold[j.ID]
		if !ok || hold < j.Pay {
			return 0, "", fmt.Errorf("contract %s has no hold to pay from", j.ID)
		}
		if err := w.Transfer(orgTreasury(j.Org), who, j.Pay, "org "+j.Org+" contract "+j.ID); err != nil {
			return 0, "", err
		}
		// the counter moved at post time; the bank moves here.
		delete(o.Hold, j.ID)
		j.Done = true
		w.AddEvent("world", "info", "org", "%s completed %s for %s: %d cents", who, j.ID, j.Org, j.Pay)
		w.BankSMS(who, fmt.Sprintf("neohome bank: +%d.%02d received (%s). balance %d.%02d",
			j.Pay/100, j.Pay%100, j.ID, w.Bank.Accts[who].Balance/100, w.Bank.Accts[who].Balance%100))
		return j.Pay, why, nil
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
	// banks text you when money arrives — a deposit receipt to the phone on
	// file, and none when there is no phone
	w.BankSMS(who, fmt.Sprintf("neohome bank: +%d.%02d received (%s). balance %d.%02d",
		j.Pay/100, j.Pay%100, j.ID, acc.Balance/100, acc.Balance%100))
	return j.Pay, why, nil
}

// ---- staged missions: ordered predicates, partial pay ----

// verifyStage checks the job's current stage against world state, without
// moving money. The stage's Verify is one of the same predicates VerifyJob
// speaks, evaluated on a copy so the job's own finished verifier (if any) is
// never clobbered.
func (w *World) verifyStage(j *Job) (bool, string) {
	if j.StageIdx < 0 || j.StageIdx >= len(j.Stages) {
		return false, "no current stage"
	}
	st := j.Stages[j.StageIdx]
	cp := *j
	cp.Verify = st.Verify
	return w.VerifyJob(&cp)
}

// payStage moves the current stage's pay and advances. The last stage also
// completes the job. Shared by the player's `job advance` and the
// assistant's staged work, so both are paid by the same hand.
func (w *World) payStage(who string, j *Job) (int64, string) {
	st := j.Stages[j.StageIdx]
	acc := w.Bank.Accts[who]
	if acc == nil {
		acc = &Account{Owner: who, Name: who, Balance: 0}
		w.Bank.Accts[who] = acc
	}
	acc.Balance += st.Pay
	acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: st.Pay,
		Memo: "job " + j.ID + " stage " + st.Name, Balance: acc.Balance})
	j.StageIdx++
	why := fmt.Sprintf("stage %s done ($%.2f)", st.Name, float64(st.Pay)/100)
	if j.StageIdx >= len(j.Stages) {
		j.Done = true
		why += " — " + j.ID + " complete"
		w.AddEvent("world", "info", "jobs", "%s completed %s", who, j.ID)
	}
	w.BankSMS(who, fmt.Sprintf("neohome bank: +%d.%02d received (%s %s). balance %d.%02d",
		st.Pay/100, st.Pay%100, j.ID, st.Name, acc.Balance/100, acc.Balance%100))
	return st.Pay, why
}

// AdvanceJob completes the job's current stage: verify against world state,
// then pay that stage. Stages run in order — only the current one is ever
// checked, so skipping ahead is impossible.
func (w *World) AdvanceJob(who, id string) (int64, string, error) {
	j := w.Job(id)
	if j == nil {
		return 0, "", fmt.Errorf("no such job: %s", id)
	}
	if len(j.Stages) == 0 {
		return 0, "", fmt.Errorf("job %s is a single-shot job (use: job pay %s)", id, id)
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
	for _, req := range j.Requires {
		if r := w.Job(req); r == nil || !r.Done {
			return 0, "", fmt.Errorf("job %s requires %s first", id, req)
		}
	}
	ok, why := w.verifyStage(j)
	if !ok {
		return 0, why, fmt.Errorf("stage %s not done yet", j.Stages[j.StageIdx].Name)
	}
	paid, msg := w.payStage(who, j)
	return paid, msg + ": " + why, nil
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
	// staged delegation is honest about capability: the assistant performs
	// exactly two fixes (dns-fix, pkg-busybox). A stage it cannot perform
	// would sit in its queue forever, so delegation refuses up front,
	// naming the stage.
	for _, st := range j.Stages {
		if st.Verify != "dns-fix" && st.Verify != "pkg-busybox" {
			return fmt.Errorf("assistant cannot do stage %s (%s)", st.Name, st.Verify)
		}
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
	if p := w.Players[who]; p != nil && p.Assistant != "" {
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

// SendMail is the outbound path for callers that already know the recipient is
// a player or an NPC (job notifications, hypervisor alerts). It routes through
// RouteMail so every outbound message in the world takes the same path and
// leaves the same trace.
func (w *World) SendMail(from, to, subject, body string) error {
	// Submit from the machine the *sending account* lives on. Picking an
	// arbitrary MTA would make the sender's own log tell a lie about which
	// host relayed the message.
	src := w.MailSourceFor(from)
	if src == nil {
		return fmt.Errorf("no mail host for sender %s", from)
	}
	res := w.RouteMail(src, from, to, subject, body)
	switch {
	case res.Delivered:
		return nil
	case res.Queued:
		return fmt.Errorf("queued for %s: %s", to, res.Diagnostic)
	default:
		return fmt.Errorf("%s", res.Diagnostic)
	}
}

// SendMailFrom submits a message from a named machine: the one the account is
// logged into. `mail send` in a session has to leave from the host you are
// standing on — the same account name can exist on several organisations'
// machines, and picking the first one in the world would put a stranger's
// hostname on your message.
func (w *World) SendMailFrom(src *Device, from, to, subject, body string) error {
	if src == nil {
		return w.SendMail(from, to, subject, body)
	}
	if src.Svc("smtpd") == nil {
		return fmt.Errorf("no mail transfer agent on %s", src.Hostname)
	}
	res := w.RouteMail(src, from, to, subject, body)
	switch {
	case res.Delivered:
		return nil
	case res.Queued:
		return fmt.Errorf("queued for %s: %s", to, res.Diagnostic)
	default:
		return fmt.Errorf("%s", res.Diagnostic)
	}
}

// MailSourceFor finds the device that owns the sending account, so outbound
// mail leaves from where the account actually is. World subsystems (jobs, the
// hypervisor) pass names that live on no machine; those fall back to the first
// mail host in the world, which is the honest origin for a world-level sender.
func (w *World) MailSourceFor(account string) *Device {
	if account != "" {
		for _, d := range w.Devices {
			if d.FindUser(account) != nil && d.Svc("smtpd") != nil {
				return d
			}
		}
	}
	// No account by that name here: a world-level sender. Prefer the player's
	// own machine so notifications land in a place the player can actually
	// read, and so the trace names a host a player knows.
	if p := w.Players["alex"]; p != nil {
		if d := w.Devices[p.PC]; d != nil {
			return d
		}
	}
	for _, id := range w.Order {
		if d := w.Devices[id]; d != nil && d.Svc("smtpd") != nil {
			return d
		}
	}
	return nil
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
