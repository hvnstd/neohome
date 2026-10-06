package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// §34 取证 / 网管 / ISP / Provider: the organisations that investigate.
//
// The tests below pin the properties the spec asks for. None of them asserts on
// a printed sentence: a desk is reachable software on a real address, a report
// is refused unless the reporter's own records back it, a case moves only when
// the world clock passes its deadline, an address is attributed to a network
// and never to a person, and a provider's suspension really takes a machine off
// the air.

// sweepGateway runs the world until the scanner has swept the household's
// public address and returns the source address the gateway really saw. The
// scanner acts on its own schedule, so the test waits on the world's clock.
func sweepGateway(t *testing.T, w *core.World) (string, int) {
	t.Helper()
	router := w.Devices["router-alex"]
	for i := 0; i < 200; i++ {
		w.Tick()
	}
	best, n := "", 0
	seen := map[string]int{}
	for _, f := range router.Sec().Flows {
		seen[f.Src]++
	}
	for src, c := range seen {
		if c > n {
			best, n = src, c
		}
	}
	if best == "" {
		t.Fatal("the world's scanner never reached the gateway in 200 ticks")
	}
	return best, n
}

// runTicks advances the world by whole ticks, the only way time may pass.
func runTicks(w *core.World, n int) {
	for i := 0; i < n; i++ {
		w.Tick()
	}
}

// ladder walks a case up its rungs by moving the world clock to the deadline
// the case itself carries: one rung per pass, exactly as a real day on the desk
// would do it. Nothing here invents a stage; it only spends time, and it lets
// the caller keep the world moving between rungs.
func ladder(t *testing.T, w *core.World, id string, cond func(*core.AbuseCase) bool) *core.AbuseCase {
	return ladderWith(t, w, id, nil, cond)
}

// ladderHot is the same, with the world's own attacker still sweeping the
// household while the case runs: a desk closes a case when the traffic really
// stopped, and acts when it did not.
func ladderHot(t *testing.T, w *core.World, id string, cond func(*core.AbuseCase) bool) *core.AbuseCase {
	return ladderWith(t, w, id, func() {
		sc, gw := w.Devices[core.ScannerID], w.Devices["router-alex"]
		if sc != nil && gw != nil {
			w.ScanTarget(sc, gw)
		}
	}, cond)
}

func ladderWith(t *testing.T, w *core.World, id string, beat func(), cond func(*core.AbuseCase) bool) *core.AbuseCase {
	t.Helper()
	prev, stalls := -1, 0
	for i := 0; i < 60; i++ {
		c := w.CaseByID(id)
		if c == nil {
			t.Fatalf("case %s vanished from the world", id)
		}
		if cond(c) {
			return c
		}
		if c.Due.After(w.Sim) {
			w.Sim = c.Due.Add(time.Minute)
		} else {
			w.Sim = w.Sim.Add(2 * time.Hour)
		}
		w.TickCount++
		if beat != nil {
			beat()
		}
		w.AbuseTick()
		if n := len(w.CaseByID(id).History); n == prev {
			if stalls++; stalls > 2 {
				break
			}
		} else {
			stalls = 0
		}
		prev = len(w.CaseByID(id).History)
	}
	return w.CaseByID(id)
}

// fastForward spends sim time in two-hour steps with the desks working, which is
// how a week passes on an abuse desk without a week passing in the test.
func fastForward(w *core.World, d time.Duration) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += 2 * time.Hour {
		w.Sim = w.Sim.Add(2 * time.Hour)
		w.TickCount++
		w.AbuseTick()
	}
}

func isSuspended(c *core.AbuseCase) bool { return c.Stage == core.StageSuspended }
func isReferred(c *core.AbuseCase) bool  { return c.Stage == core.StageReferred }
func isLaw(c *core.AbuseCase) bool       { return strings.HasPrefix(c.Stage, "law:") }
func isClosed(c *core.AbuseCase) bool {
	return c.Stage == core.StageClosed || c.Stage == core.StageLEClosed
}

func TestReportNeedsTheReportersOwnRecords(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]

	// the desk that answers for an address is a real organisation with a real
	// address, and it is open for business
	desk := w.DeskFor("192.0.2.1")
	if desk == nil || desk.ASN != 64520 {
		t.Fatalf("192.0.2.1 should be answered for by NovaPanel, got %+v", desk)
	}
	if !w.DeskOperational(desk) {
		t.Fatal("a freshly seeded desk should be open for business")
	}

	// this machine has never seen the address: a report from here would be a
	// guess, and a guess is not a record
	if _, err := w.ReportAbuse(pc, "alex", "192.0.2.99", "scanning", "", 24*time.Hour); err == nil {
		t.Fatal("a report with no evidence behind it must be refused")
	} else if !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("the refusal should say what is missing, got %v", err)
	}
	// and a note is not evidence: an opinion does not become a record
	if _, err := w.ReportAbuse(pc, "alex", "192.0.2.99", "scanning", "I am sure it was them", 24*time.Hour); err == nil {
		t.Fatal("a note alone must not pass the evidence check")
	}
	if out := run(t, w, pc, "alex", "abuse report 192.0.2.99"); !strings.Contains(out, "evidence") {
		t.Fatalf("the shell should refuse it for the same reason:\n%s", out)
	}

	// what the machine really saw is what a report carries
	src, attempts := sweepGateway(t, w)
	router := w.Devices["router-alex"]
	ev := core.AbuseEvidence(router, src, 24*time.Hour)
	if len(ev) < 2 || !strings.Contains(strings.Join(ev, "\n"), fmt.Sprintf("%d connection attempt", attempts)) {
		t.Fatalf("the gateway's own records should describe the sweep, got %q", ev)
	}
	c, err := w.ReportAbuse(router, "alex", src, "scanning", "hourly sweep of my public address", 24*time.Hour)
	if err != nil {
		t.Fatalf("a report with evidence should be accepted: %v", err)
	}
	if c.SubjectIP != src {
		t.Fatalf("the case should be filed about %s, got %s", src, c.SubjectIP)
	}
	if c.Evidence == nil {
		t.Fatal("the evidence the report carried must be kept with the case")
	}
	// the reporter can find their own ticket, and it is the one that was filed
	own := w.CasesInvolving("alex")
	if len(own) == 0 || own[0].ID != c.ID {
		t.Fatalf("the reporter should be able to find their own ticket, got %+v", own)
	}

	// a second report from the same reporter about the same address is the same
	// incident: it merges instead of opening a parallel ticket
	same, err := w.ReportAbuse(router, "alex", src, "scanning", "still sweeping", 24*time.Hour)
	if err != nil {
		t.Fatalf("second report: %v", err)
	}
	if same.ID != c.ID {
		t.Fatalf("a repeat report should merge into %s, got %s", c.ID, same.ID)
	}
	if !strings.Contains(strings.Join(same.History, "\n"), "merged with a new report from alex") {
		t.Fatalf("the merge should be visible in the case file: %v", same.History)
	}
}

func TestDeskLadderRunsOnTheWorldClock(t *testing.T) {
	w := secSetup(t)
	router := w.Devices["router-alex"]
	sc := w.Devices[core.ScannerID]
	src, _ := sweepGateway(t, w)

	c, err := w.ReportAbuse(router, "alex", src, "scanning", "sweeping the gateway", 24*time.Hour)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// the report is a file, and the file says who filed it and when the desk
	// must answer by — it does not say what the desk decided
	history := strings.Join(w.CaseByID(c.ID).History, "\n")
	if !strings.Contains(history, "alex") {
		t.Fatalf("the case file should record who reported it:\n%s", history)
	}
	if c.Due.Before(w.Sim) {
		t.Fatalf("the desk's deadline %s is not in the world's future", c.Due)
	}

	// the finding is the desk's own record about a machine on its own network,
	// and only machines on its own network contribute
	c = ladder(t, w, c.ID, func(cc *core.AbuseCase) bool { return len(cc.Findings) > 0 })
	found := false
	for _, f := range c.Findings {
		if strings.Contains(f, "novapanel.example") {
			found = true
		}
		if strings.Contains(f, "router-alex") {
			t.Fatalf("the desk must not be able to see inside the complaining household: %q", f)
		}
	}
	if !found {
		t.Fatalf("the desk's finding should come from its own network: %q", c.Findings)
	}

	// the notice: the deadline passes, the customer is told, and the notice is
	// an artifact on the file. The sweep keeps coming, because that is what makes
	// the next rung the desk's real decision rather than a timer.
	c = ladderHot(t, w, c.ID, func(cc *core.AbuseCase) bool { return cc.Stage == core.StageNotified })
	if c.Stage != core.StageNotified {
		t.Fatalf("after the triage deadline the customer should have been notified, got %s", c.Stage)
	}
	if len(c.Notices) == 0 {
		t.Fatal("the case must record the notice it sent")
	}

	// enforcement: the traffic did not stop, and the consequence is real
	c = ladderHot(t, w, c.ID, isSuspended)
	if c.Stage != core.StageSuspended {
		t.Fatalf("an unresolved case should suspend the customer's port, got %s", c.Stage)
	}
	if sc.Powered() {
		t.Fatal("a suspended machine must really be powered off")
	}
	if _, _, msg := core.Dial(router, sc.WANIP(), 22); !strings.Contains(msg, "down") {
		t.Fatalf("a suspended machine must not answer, got %q", msg)
	}

	// and a suspension is a term, not a life sentence: it lapses on its own
	c = ladder(t, w, c.ID, isClosed)
	if c.Action != "port suspended, then restored" {
		t.Fatalf("the suspension term should lapse into a restore, got %s/%s", c.Stage, c.Action)
	}
	if !sc.Powered() {
		t.Fatal("the machine should be back on the network after the term")
	}
}

func TestAddressIsANetworkNotAPerson(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	// the household's public address resolves to an ISP, an ASN, a range and an
	// abuse contact — and to nobody's name
	out := run(t, w, pc, "alex", "whois "+router.WANIP())
	for _, want := range []string{"AS64510", "abuse@netcrest.example", "NetCrest", "responsible"} {
		if !strings.Contains(out, want) {
			t.Fatalf("whois should produce %q for the household's public address:\n%s", want, out)
		}
	}
	if strings.Contains(out, "alex") {
		t.Fatalf("a registry record must not name the subscriber:\n%s", out)
	}
	// the lookup is real work: with the registry unreachable it says so and
	// says on which address it failed
	pc.FS.Write("/etc/resolv.conf", "nameserver 192.0.2.222\n", 0644, "root", "root")
	if out := run(t, w, pc, "alex", "whois 192.0.2.1"); !strings.Contains(out, "whois:") {
		t.Fatalf("an unreachable registry must be reported, not invented:\n%s", out)
	}

	// the ISP holds the subscriber record for a household line, and a lawful
	// request is what turns an address into a subscriber
	if rec := w.SubscriberRecordForTest(router.WANIP()); rec == "" {
		t.Fatal("the ISP should hold a subscriber record for a household line")
	}

	// a customer on a shared plan sends traffic out under the provider's NAT
	// egress, and their own address names nobody
	cg, _, err := w.ProvisionVPSWithOS("alex", "nano-shared", "cg-abuse", "debian")
	if err != nil {
		t.Fatalf("provision a shared-plan node: %v", err)
	}
	mine := core.WANIPOf(cg)
	if !cg.NATed || !strings.HasPrefix(mine, "100.64.") {
		t.Fatalf("a shared plan should number the node inside the CGNAT pool, got %q", mine)
	}
	if rec := w.SubscriberRecordForTest(mine); rec != "" {
		t.Fatalf("a shared address must not resolve to a subscriber, got %q", rec)
	}
	if dk := w.DeskFor(mine); dk == nil || !strings.Contains(dk.Org, "NovaPanel") {
		t.Fatalf("a CGNAT address is answered for by the provider that runs the NAT, got %+v", dk)
	}

	// what a remote victim logs is the provider's NAT egress, never the
	// customer: the customer really connects, and the desk's machine is where
	// the record lands
	deskDev := w.Devices["novapanel-abuse"]
	egress := w.NATAddress(64520)
	if _, _, msg := core.Dial(cg, deskDev.WANIP(), 22); msg == "" {
		t.Fatal("a shared-plan customer should still reach the internet")
	}
	seen := false
	for _, f := range deskDev.Sec().Flows {
		if f.Src == egress {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the victim should see %s, the provider's NAT egress, not %s", egress, mine)
	}

	// the last rung of the ladder stops there, and the file says why: a case
	// about the egress is one the provider cannot act on, so it goes up
	provider := w.DeskFor(egress)
	if provider == nil {
		t.Fatalf("somebody should answer for %s", egress)
	}
	c := w.OpenCaseForTest(provider.DeviceID, egress, "world", "brute-force", []string{"victim records: 40 failed logins"})
	if c == nil {
		t.Fatal("the provider should be able to open a case about its own egress")
	}
	// the customer keeps sending while the case runs, which is what stops the
	// desk from closing it as "the traffic stopped"
	beat := func() { core.AttackLogin(w, cg, deskDev, "root", "toor", "ssh", 22) }
	c = ladderWith(t, w, c.ID, beat, isLaw)
	if !isLaw(c) {
		t.Fatalf("a case the provider cannot act on should go up the ladder, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	var lawCase *core.AbuseCase
	for _, lc := range w.Cases("le-cyber") {
		if lc.SubjectIP == egress {
			lawCase = lc
		}
	}
	if lawCase == nil {
		t.Fatalf("law enforcement should hold a file about %s", egress)
	}
	lawCase = ladderWith(t, w, lawCase.ID, beat, isClosed)
	history := strings.Join(lawCase.History, "\n")
	if !strings.Contains(history, "carrier-grade NAT") {
		t.Fatalf("the lawful request should end honestly at the NAT, got %s:\n%s", lawCase.Stage, history)
	}
	if lawCase.Account != "" {
		t.Fatalf("nobody may be named behind a shared address, got %q", lawCase.Account)
	}
}

func TestDeskShiftIsRealWork(t *testing.T) {
	w := secSetup(t)
	desk := w.Desk("novapanel-abuse")
	if desk == nil {
		t.Fatal("the world should have a hosting provider's abuse desk")
	}
	d := w.DeskDevice(desk)
	contractor := d.FindUser("contractor")
	if contractor == nil {
		t.Fatal("the desk's shift account should exist on the desk's machine")
	}
	// the account is a real account and the console gate is its group
	if !w.OnDeskStaff(d, contractor, desk.DeviceID) {
		t.Fatal("an account in the desk's own group should be allowed to work the queue")
	}
	if w.OnDeskStaff(d, d.FindUser("root"), "netcrest-noc") {
		t.Fatal("staff of one organisation must not hold another organisation's console")
	}
	if w.OnDeskStaff(w.Devices["pc-alex"], contractor, desk.DeviceID) {
		t.Fatal("the console must only exist on the desk's own machine")
	}

	// the queue is the cases the organisation really holds
	router := w.Devices["router-alex"]
	src, _ := sweepGateway(t, w)
	c, err := w.ReportAbuse(router, "alex", src, "scanning", "", 24*time.Hour)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c = ladder(t, w, c.ID, func(cc *core.AbuseCase) bool { return cc.Stage == core.StageTriaged })
	if c.Stage != core.StageTriaged {
		t.Fatalf("the case should have been triaged, got %s", c.Stage)
	}
	queue := w.DeskQueue(desk.DeviceID)
	if len(queue) == 0 {
		t.Fatal("a filed report should appear in the desk's queue")
	}
	target := queue[len(queue)-1]
	if _, err := w.DeskTriage("", "contractor", "read it"); err == nil {
		t.Fatal("triage without a case id must fail")
	}
	// a decision needs a reason, and a case already triaged by the automatic
	// desk is not re-triaged by hand
	if _, err := w.DeskTriage(target.ID, "contractor", ""); err == nil {
		t.Fatal("a triage decision without a note must be refused")
	}
	if _, err := w.DeskTriage(target.ID, "contractor", "confirmed against our own flow records"); err == nil {
		t.Fatalf("%s was already triaged; a second triage must be refused", target.ID)
	}
	// the operator's real work: decide and act on a case, with a note
	if _, err := w.DeskAct(target.ID, "contractor", "close", "notice sent, traffic stopped"); err != nil {
		t.Fatalf("an operator should be able to act on a case in the queue: %v", err)
	}
	if n := w.CasesHandledBy("contractor"); n != 1 {
		t.Fatalf("the world should count the operator's work, got %d", n)
	}
	if n := w.CasesHandledBy("contractor"); n != 1 {
		t.Fatalf("the world should count the operator's work, got %d", n)
	}
	// being named as a reporter is not desk work
	if n := w.CasesHandledBy("alex"); n != 0 {
		t.Fatalf("filing a report is not desk work, got %d", n)
	}
	if s := strings.Join(w.DeskSummary(desk.DeviceID), " "); !strings.Contains(s, "1 closed") {
		t.Fatalf("the desk's roll-up should count the closed case, got %q", s)
	}
}

func TestReferredCaseIsOpenedByTheOwningNetwork(t *testing.T) {
	w := secSetup(t)

	// the scanner is a NovaPanel customer; the household is NetCrest's. A case
	// about the household's address must leave the provider's desk and arrive at
	// the ISP, which opens a file of its own.
	mine := w.Devices["pc-alex"].FirstWANv6()
	before := len(w.Cases("netcrest-noc"))
	c := w.OpenCaseForTest("novapanel-abuse", mine, "world", "brute-force", []string{"our own records: 12 failed logins"})
	c = ladder(t, w, c.ID, isReferred)
	if c.Stage != core.StageReferred || c.ReferredTo != "netcrest-noc" {
		t.Fatalf("a case about an ISP's address should be referred to the ISP, got %s → %s", c.Stage, c.ReferredTo)
	}
	after := w.Cases("netcrest-noc")
	if len(after) <= before {
		t.Fatal("the receiving desk must open its own case, with its own number")
	}
	// the new case is the ISP's, and it knows which complaint it came from
	var got *core.AbuseCase
	for _, cand := range after {
		if cand.SubjectIP == mine {
			got = cand
		}
	}
	if got == nil || got.DeskID != "netcrest-noc" {
		t.Fatalf("the ISP should hold a case about %s, got %+v", mine, got)
	}
	if !strings.Contains(strings.Join(got.History, "\n"), "("+c.ID+")") {
		t.Fatalf("the new case should record which referral created it, got %v", got.History)
	}
	if len(got.Evidence) == 0 {
		t.Fatal("a referral must carry the evidence that justified it")
	}
	// the ISP's own finding is about its own customer, not the provider's
	for _, f := range got.Findings {
		if strings.Contains(f, "novapanel") {
			t.Fatalf("the ISP must investigate from its own records, got %q", f)
		}
	}
}

func TestISPWarnsBeforeItEscalates(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	desk := w.Desk("netcrest-noc")

	// a sustained brute force against a provider's own desk: the provider's
	// records notice it, hand it to the household's ISP, and the ISP warns the
	// subscriber rather than cutting the line off or calling the police. The
	// attacker keeps going, because a desk only acts when the conduct did not
	// stop.
	beat := func() {
		runWithStdin(t, w, pc, "alex", "ssh root@abuse.novapanel.example", "toor")
	}
	for i := 0; i < 8; i++ {
		beat()
		runTicks(w, 4)
	}
	for i := 0; i < 24; i++ {
		beat()
		runTicks(w, 2)
		fastForward(w, 2*time.Hour)
	}

	var warned *core.AbuseCase
	for _, c := range w.Cases(desk.DeviceID) {
		if c.SubjectDev == "pc-alex" || c.Account == "alex" {
			warned = c
		}
	}
	if warned == nil {
		t.Fatal("the ISP should hold a case about the household's line")
	}
	if len(warned.Notices) == 0 {
		t.Fatal("the customer should have been notified before anything else")
	}
	// first offence: a warning on the record, and no police file
	if warned.Stage != core.StageClosed || warned.Action != "formal warning recorded" {
		t.Fatalf("a first case against a subscriber should end in a warning, got %s/%s", warned.Stage, warned.Action)
	}
	// the notice is a real message in the subscriber's real mailbox
	if subs := core.InboxSubjects(pc, "alex"); len(subs) == 0 {
		t.Fatal("the notice should be in the customer's mailbox, not only in the case file")
	}
	// and when the desk acts on a line it can name, the file says who was told
	if !strings.Contains(strings.Join(warned.Notices, "\n"), "alex") {
		t.Fatalf("the notice log should name the subscriber it was sent to, got %v", warned.Notices)
	}
	// the desk's own file is what a repeat would read
	if s := strings.Join(w.DeskSummary(desk.DeviceID), " "); !strings.Contains(s, "closed") {
		t.Fatalf("the desk's roll-up should reflect the closed case, got %q", s)
	}
}

func TestCaseFileIsTheAuditTrail(t *testing.T) {
	w := secSetup(t)
	router := w.Devices["router-alex"]
	src, _ := sweepGateway(t, w)
	c, err := w.ReportAbuse(router, "alex", src, "scanning", "", 24*time.Hour)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// the world moves while the case is open — the scanner keeps sweeping, the
	// desk keeps working — and the file keeps recording
	c = ladderHot(t, w, c.ID, isSuspended)
	history := strings.Join(c.History, "\n")
	for _, want := range []string{"alex", "triaged", "notified", "suspended"} {
		if !strings.Contains(history, want) {
			t.Fatalf("the case file should record %q:\n%s", want, history)
		}
	}
	// every entry is stamped from the world clock, in order
	if c.Opened.After(c.Updated) {
		t.Fatal("a case cannot be updated before it was opened")
	}
	// the file names artifacts, not a mood: the records the desk worked from and
	// the notices it sent
	if len(c.Evidence) == 0 {
		t.Fatal("the case should hold the evidence it was filed with")
	}
	if len(c.Findings) == 0 {
		t.Fatal("the case should hold what the desk found in its own records")
	}
	if !strings.Contains(strings.Join(c.Notices, "\n"), c.ID) {
		t.Fatalf("the notice log should reference the case, got %v", c.Notices)
	}
	// and the same file is what the reporter reads back from their own machine
	out := run(t, w, w.Devices["pc-alex"], "alex", "abuse status "+c.ID)
	for _, want := range []string{c.ID, "suspended", "the desk's own records", "history:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("abuse status should print %q:\n%s", want, out)
		}
	}
}

// jobByVerify finds the open job the world checks with a given verifier, which
// is how a test asks "is this piece of work really done".
func jobByVerify(t *testing.T, w *core.World, key string) *core.Job {
	t.Helper()
	for _, j := range w.OpenJobs() {
		if j.Verify == key {
			return j
		}
	}
	t.Fatalf("no open job verified by %q", key)
	return nil
}

func TestAbuseJobsAreRealWork(t *testing.T) {
	w := secSetup(t)

	// J-103, report the scanner: the job is only done when the network that
	// answers for the address really holds a case filed by this worker
	report := jobByVerify(t, w, "abuse-report")
	if err := w.AcceptJob("alex", report.ID); err != nil {
		t.Fatalf("accept %s: %v", report.ID, err)
	}
	report = jobByVerify(t, w, "abuse-report")
	if ok, why := w.VerifyJob(report); ok {
		t.Fatalf("the report job must not pass before the report: %s", why)
	}
	router := w.Devices["router-alex"]
	src, _ := sweepGateway(t, w)
	if _, err := w.ReportAbuse(router, "alex", src, "scanning", "sweeping the gateway", 24*time.Hour); err != nil {
		t.Fatalf("report: %v", err)
	}
	if ok, why := w.VerifyJob(report); !ok {
		t.Fatalf("a report backed by the machine's own records should finish the job: %s", why)
	}

	// J-104, cover the desk shift: two cases moved by hand at that desk, by the
	// shift account the job names — and the world counts the work, not the
	// acceptance
	shift := jobByVerify(t, w, "abuse-triage")
	if err := w.AcceptJob("alex", shift.ID); err != nil {
		t.Fatalf("accept %s: %v", shift.ID, err)
	}
	shift = jobByVerify(t, w, "abuse-triage")
	if ok, why := w.VerifyJob(shift); ok {
		t.Fatalf("the shift job must not pass before the shift: %s", why)
	}
	desk := w.Desk(shift.Client)
	if desk == nil {
		t.Fatalf("the job names a desk the world does not have: %s", shift.Client)
	}
	for i := 0; i < 4 && len(w.DeskQueue(desk.DeviceID)) < 2; i++ {
		// the desk needs cases to decide: the scanner's sweep is one, and a
		// complaint from the network next door is another
		fastForward(w, 6*time.Hour)
		w.ScannerTick()
		if len(w.Cases(desk.DeviceID)) == 0 {
			t.Fatal("the desk has no cases at all to work")
		}
		if c := w.OpenCaseForTest(desk.DeviceID, fmt.Sprintf("192.0.2.%d", 30+i), "world", "brute-force",
			[]string{"our own records: 40 failed logins"}); c == nil {
			t.Fatal("the desk should file a case about an address in its own network")
		}
	}
	queue := w.DeskQueue(desk.DeviceID)
	if len(queue) < 2 {
		t.Fatalf("the desk should have work to do, got %d", len(queue))
	}
	moved := 0
	for _, c := range queue {
		if moved == 2 {
			break
		}
		if _, err := w.DeskAct(c.ID, "contractor", "close", "evidence read, customer told"); err != nil {
			continue // a case that already closed is not work to do
		}
		moved++
	}
	if moved < 2 {
		t.Fatalf("only %d case(s) could be moved by hand", moved)
	}
	if ok, why := w.VerifyJob(shift); !ok {
		t.Fatalf("two decisions by hand should finish the shift: %s", why)
	}

	// J-105, the handover document: done from inside the office, on the file
	// server, with the account the job names
	share := jobByVerify(t, w, "meridian-share")
	if err := w.AcceptJob("alex", share.ID); err != nil {
		t.Fatalf("accept %s: %v", share.ID, err)
	}
	share = jobByVerify(t, w, "meridian-share")
	if ok, why := w.VerifyJob(share); ok {
		t.Fatalf("the file job must not pass before the file exists: %s", why)
	}
	ws := w.Devices["meridian-ws"]
	out := runWithStdin(t, w, ws, "admin", "ssh admin@fs.meridian.example", "meridian-admin",
		"mkdir -p /srv/projects/handover", "echo handover notes for the project team > /srv/projects/handover/README", "exit")
	if !w.Devices["meridian-fs"].FS.Exists("/srv/projects/handover/README") {
		t.Fatalf("the handover document should be on the file server:\n%s", out)
	}
	if ok, why := w.VerifyJob(share); !ok {
		t.Fatalf("a document on the file server should finish the job: %s", why)
	}
}
