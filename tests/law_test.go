package tests

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// §35 Law Enforcement: the top of the ladder.
//
// The tests below pin the two things the spec is explicit about. First, nobody
// teleports: a file has to arrive through intake, be read, have a lawful basis,
// be sent to the network that holds the address, come back with whatever that
// network will say, and then be worked by an investigator — with the warrant as
// the last rung and a disposition on the record. Second, "IP 查到谁" stops where
// the world says it stops: an address resolves to a network, and to a
// subscriber only when a network discloses one, under a request that is written
// down.

// lawFile opens a file at the unit the way a referral does, with a complaint
// worth working.
func lawFile(t *testing.T, w *core.World, ip, from string, evidence []string) *core.AbuseCase {
	t.Helper()
	c := w.OpenCaseForTest("le-cyber", ip, from, "referred conduct", evidence)
	if c == nil {
		t.Fatalf("the unit should take a file about %s", ip)
	}
	return c
}

// lawWalk moves the world's clock in one-hour steps with both ticks, which is
// how a file moves when nobody is standing at the console.
func lawWalk(w *core.World, hours int) {
	for i := 0; i < hours; i++ {
		w.Sim = w.Sim.Add(1 * time.Hour)
		w.TickCount++
		w.AbuseTick()
		w.LawTick()
	}
}

func householdAddress(w *core.World) string {
	r := w.Devices["router-alex"]
	if ip := r.WANIP(); ip != "" {
		return ip
	}
	return r.FirstWANv6()
}

func TestLawFileCannotTeleportToAWarrant(t *testing.T) {
	w := secSetup(t)
	c := lawFile(t, w, householdAddress(w), "netcrest-noc",
		[]string{"ISP records: eight failed logins at a provider desk", "provider report: the same address"})

	// a file starts as a complaint somebody has to read, not as an investigation
	if c.Stage != core.StageFiled && c.Stage != core.StageLEOpened {
		t.Fatalf("a new file starts at intake, got %s", c.Stage)
	}
	if _, err := w.DeskAct(c.ID, "agent", "investigate", "let us have a look"); err == nil {
		t.Fatal("an investigation must not open before a subscriber has been disclosed")
	} else if !strings.Contains(err.Error(), "disclosed") {
		t.Fatalf("the refusal should say what is missing, got %v", err)
	}
	if _, err := w.DeskAct(c.ID, "agent", "warrant", "sign it"); err == nil {
		t.Fatal("a warrant must not be signed before the file has been worked")
	}
	// the unit's powers are its own: it does not suspend, refer or filter
	for _, action := range []string{"suspend", "notify", "refer", "escalate"} {
		if _, err := w.DeskAct(c.ID, "cases", action, "do it"); err == nil {
			t.Fatalf("the unit must not have the %q power", action)
		}
	}

	// and the ladder, when it is walked, is walked in order
	lawWalk(w, 1)
	c = w.CaseByID(c.ID)
	if c.Stage != core.StageTriaged {
		t.Fatalf("intake should read the file first, got %s", c.Stage)
	}
	lawWalk(w, 2)
	c = w.CaseByID(c.ID)
	if c.Stage != core.StageLERequest && c.Stage != core.StageLEDisclose {
		t.Fatalf("a lawful request should follow intake, got %s", c.Stage)
	}
	if c.Requested == "" {
		t.Fatal("the file must record which network the request went to")
	}
	history := strings.Join(c.History, "\n")
	for _, want := range []string{"intake read the complaint", "lawful request sent to"} {
		if !strings.Contains(history, want) {
			t.Fatalf("the file should record %q:\n%s", want, history)
		}
	}
	// no rung was skipped: the request line comes after the intake line, and
	// the disclosure line, if it is there, comes after both
	if strings.Index(history, "intake read the complaint") > strings.Index(history, "lawful request sent to") {
		t.Fatalf("the file was worked out of order:\n%s", history)
	}
}

func TestLawfulRequestBecomesASubscriber(t *testing.T) {
	w := secSetup(t)
	ip := householdAddress(w)
	c := lawFile(t, w, ip, "netcrest-noc", []string{"ISP records: the line hammered a provider desk"})

	// the request goes to the network that answers for the address, and it is a
	// real message in that network's mailbox
	lawWalk(w, 1)
	if err := w.LawRequest(w.CaseByID(c.ID), "the complaint names an address this network announces"); err != nil {
		t.Fatalf("the request should go out: %v", err)
	}
	c = w.CaseByID(c.ID)
	if c.Requested != "netcrest-noc" {
		t.Fatalf("the request should go to the ISP that announces %s, got %q", ip, c.Requested)
	}
	isp := w.Devices["netcrest-noc"]
	if subs := core.InboxSubjects(isp, "abuse"); len(subs) == 0 {
		t.Fatal("the lawful request should be a real message in the ISP's mailbox")
	}

	// the network answers with the subscriber on file, and only then does the
	// address become a person
	lawWalk(w, 3)
	c = w.CaseByID(c.ID)
	if c.Account != "alex" {
		t.Fatalf("the ISP should disclose the account on file, got %q (stage %s)", c.Account, c.Stage)
	}
	if !strings.Contains(strings.Join(c.History, "\n"), "lawful request") {
		t.Fatalf("the file must record the basis it was worked on:\n%v", c.History)
	}
	// the unit's own blind spots are state, not a comment: it begins by saying
	// it has no network of its own
	if !strings.Contains(strings.Join(c.Findings, "\n"), "no network of its own") {
		t.Fatalf("the file should carry what the unit cannot see:\n%v", c.Findings)
	}
}

func TestUnitPermissionsAreGroups(t *testing.T) {
	w := secSetup(t)
	d := w.Devices["le-cyber"]
	if d == nil {
		t.Fatal("the world should have a law enforcement unit")
	}
	analyst, agent := d.FindUser("cases"), d.FindUser("agent")
	if analyst == nil || agent == nil {
		t.Fatal("the unit should have an intake analyst and an investigator account")
	}
	if !w.OnDeskStaff(d, analyst, "le-cyber") {
		t.Fatal("the intake analyst should hold the unit's console")
	}
	if !w.OnLawAgent(d, agent) || w.OnLawAgent(d, analyst) {
		t.Fatal("the agents group is what separates an investigator from an analyst")
	}

	c := lawFile(t, w, householdAddress(w), "netcrest-noc", []string{"ISP records: the line hammered a provider desk"})
	// a request needs a basis on the file, whoever sends it
	lawWalk(w, 1)
	if _, err := w.DeskAct(c.ID, "cases", "request", ""); err == nil {
		t.Fatal("a lawful request without a stated basis must be refused")
	}
	if _, err := w.DeskAct(c.ID, "cases", "request", "the complaint names an address this network announces"); err != nil {
		t.Fatalf("an analyst should be able to send a lawful request: %v", err)
	}
	if n := w.LawWorked("request"); n != 1 {
		t.Fatalf("the unit should count the request as work done by hand, got %d", n)
	}
	// the analyst may not investigate or sign; the investigator may
	lawWalk(w, 3)
	if _, err := w.DeskAct(c.ID, "cases", "investigate", "have a look"); err == nil {
		t.Fatal("an analyst must not open an investigation: that is the agents group's work")
	} else if !strings.Contains(err.Error(), "agents") {
		t.Fatalf("the refusal should name the permission it needs, got %v", err)
	}
	if _, err := w.DeskAct(c.ID, "cases", "warrant", "sign"); err == nil {
		t.Fatal("an analyst must not sign an order")
	}
	if _, err := w.DeskAct(c.ID, "agent", "investigate", "the file supports a look"); err != nil {
		t.Fatalf("an investigator should be able to open the investigation: %v", err)
	}
	if c := w.CaseByID(c.ID); c.InvestigatedBy != "agent" {
		t.Fatalf("the file should name the investigator, got %q", c.InvestigatedBy)
	}
}

func TestWarrantIsServedOnTheNetwork(t *testing.T) {
	w := secSetup(t)
	ip := householdAddress(w)
	c := lawFile(t, w, ip, "netcrest-noc",
		[]string{"ISP records: the line hammered a provider desk", "provider report: the same address at 08:12"})

	// a file that has not been worked cannot produce an order, and the refusal
	// says what it stands on
	if _, err := w.DeskAct(c.ID, "agent", "warrant", "sign it"); err == nil {
		t.Fatal("a warrant must not be signed against an unworked file")
	}

	lawWalk(w, 8)
	c = w.CaseByID(c.ID)
	if c.Stage != core.StageLEInvestigation && c.Stage != core.StageLEWarrant && c.Stage != core.StageLECharged {
		t.Fatalf("the file should have reached the investigation, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	// the investigation reads the network's own records about the line
	if !strings.Contains(strings.Join(c.Findings, "\n"), "connection records obtained from") {
		t.Fatalf("the investigation should work from the network's own records:\n%v", c.Findings)
	}
	// and it cannot seize anything: that is the unit's stated blind spot
	if !strings.Contains(strings.Join(c.Findings, "\n"), "cannot seize") {
		t.Fatalf("the file should say what the unit cannot do:\n%v", c.Findings)
	}

	ws := w.LawWarrants()
	if len(ws) == 0 {
		// the auto ladder may still be on its rung: force it, as an investigator
		// standing at the console would
		if _, err := w.DeskAct(c.ID, "agent", "warrant", "the file names the subscriber and holds two networks' records"); err != nil {
			t.Fatalf("a supported file should yield an order: %v", err)
		}
		ws = w.LawWarrants()
	}
	if len(ws) == 0 {
		t.Fatal("a supported file should yield an order")
	}
	wr := ws[0]
	if wr.Case != c.ID || wr.Subject != "alex" || wr.ServedOn != "netcrest-noc" {
		t.Fatalf("the order should name the case, the subscriber and the network served, got %+v", wr)
	}
	// served means served: the network holds the order in its own mailbox
	if subs := core.InboxSubjects(w.Devices["netcrest-noc"], "abuse"); !containsSubject(subs, "order "+wr.ID) {
		t.Fatalf("the order should be in the network's mailbox, got %v", subs)
	}
	// and the subscriber is told, in their own mailbox
	pc := w.Devices["pc-alex"]
	if subs := core.InboxSubjects(pc, "alex"); !containsSubject(subs, "order for your connection's records") {
		t.Fatalf("the subscriber should be told about the order, got %v", subs)
	}

	lawWalk(w, 2)
	c = w.CaseByID(c.ID)
	if c.Stage != core.StageLECharged {
		t.Fatalf("the matter should end charged, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	if !strings.Contains(strings.Join(c.History, "\n"), wr.ID) {
		t.Fatalf("the disposition should reference the order:\n%s", strings.Join(c.History, "\n"))
	}
}

func TestUnitHoursAreAResource(t *testing.T) {
	w := secSetup(t)
	c := lawFile(t, w, householdAddress(w), "netcrest-noc", []string{"ISP records: the line hammered a provider desk"})

	// a unit with no hours left cannot work a file, and says so: the clock moves
	// half a minute, so the refill cannot cover a rung's cost
	u := w.LawUnit()
	u.Hours = 0
	u.Last = w.Sim
	c.Due = w.Sim // the file is due; only the hours are missing
	w.Sim = w.Sim.Add(30 * time.Second)
	w.TickCount++
	w.AbuseTick()
	w.LawTick()
	c = w.CaseByID(c.ID)
	if c.Stage != core.StageFiled && c.Stage != core.StageLEOpened {
		t.Fatalf("a file must wait when there is no investigator time, got %s", c.Stage)
	}
	if !c.Due.After(w.Sim) {
		t.Fatalf("the file should be pushed to the next shift, due %s (now %s)", c.Due, w.Sim)
	}
	if !strings.Contains(strings.Join(w.LawUnit().Notes, "\n"), "waits:") {
		t.Fatalf("the unit's log should say the file is waiting for hours: %v", w.LawUnit().Notes)
	}

	// the refill is the world clock: a shift's worth of hours comes back
	before := w.LawHours()
	lawWalk(w, 3)
	if after := w.LawHours(); after <= before {
		t.Fatalf("hours should come back on the world clock: %.2f then %.2f", before, after)
	}
	if w.LawHours() > float64(core.LawHoursCap) {
		t.Fatalf("hours must not exceed a shift: %.1f", w.LawHours())
	}
	// and with hours, the same file moves
	if c := w.CaseByID(c.ID); c.Stage == core.StageFiled || c.Stage == core.StageLEOpened {
		t.Fatalf("with investigator time the file should move, still %s (hours %.1f)", c.Stage, w.LawHours())
	}
}

func TestIntakeReadsItsOwnMailbox(t *testing.T) {
	w := secSetup(t)
	was := len(w.Cases("le-cyber"))
	pc := w.Devices["pc-alex"]
	ip := householdAddress(w)

	// a complaint that names an address becomes a file
	res := w.RouteMail(pc, "alex", "cases@cnu.gov.example",
		"complaint about "+ip,
		"Someone at "+ip+" has been trying passwords on our office gateway all week.\n")
	if !(res.Delivered || res.Queued) {
		t.Fatalf("the complaint should reach the unit: %+v", res)
	}
	w.LawUnit().Intake = 0 // the test posted the message directly, not through the postman
	w.Sim = w.Sim.Add(30 * time.Minute)
	w.TickCount++
	w.LawTick()
	now := w.Cases("le-cyber")
	if len(now) <= was {
		t.Fatalf("a complaint naming an address should open a file (was %d, now %d)", was, len(now))
	}
	var opened *core.AbuseCase
	for _, c := range now {
		if c.SubjectIP == ip {
			opened = c
		}
	}
	if opened == nil {
		t.Fatalf("the file should be about %s", ip)
	}
	if !strings.HasPrefix(opened.Reporter, "alex@") {
		t.Fatalf("the file should name the complainant, got %q", opened.Reporter)
	}
	if len(opened.Evidence) == 0 || !strings.Contains(strings.Join(opened.Evidence, "\n"), "complaint received") {
		t.Fatalf("the complaint itself should be the evidence:\n%v", opened.Evidence)
	}

	// a complaint that names no address is not turned into a guess
	w.DeliverLocal(w.Devices["le-cyber"], "alex@home-pc", "cases", "a bad feeling", "I think someone is being mean online.\n")
	w.Sim = w.Sim.Add(30 * time.Minute)
	w.TickCount++
	w.LawTick()
	count := len(w.Cases("le-cyber"))
	w.Sim = w.Sim.Add(30 * time.Minute)
	w.TickCount++
	w.LawTick()
	if len(w.Cases("le-cyber")) != count {
		t.Fatal("a complaint naming no address must not become a file")
	}
	if !strings.Contains(strings.Join(w.LawUnit().Notes, "\n"), "names no address") {
		t.Fatalf("the intake should say why the complaint was not filed: %v", w.LawUnit().Notes)
	}
}

func TestUnitCannotAskForWhatNobodyAnnounces(t *testing.T) {
	w := secSetup(t)
	// a file about an allocated address stops at the registry: there is no
	// subscriber there to ask, and the file says which network does hold it
	c := lawFile(t, w, "203.0.113.250", "alex", []string{"gateway log: probes from this address"})
	lawWalk(w, 2)
	c = w.CaseByID(c.ID)
	if !caseEnded(c) {
		t.Fatalf("a file with nobody to ask should end honestly, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	reason := strings.Join(c.History, "\n")
	if !strings.Contains(reason, "registry") || !strings.Contains(reason, "allocation records only") {
		t.Fatalf("the file should say why it ended at the registry:\n%s", reason)
	}

	// an address no network announces at all: nothing to ask, nothing invented
	c2 := lawFile(t, w, "10.99.99.99", "alex", []string{"gateway log: probes from this address"})
	lawWalk(w, 2)
	c2 = w.CaseByID(c2.ID)
	if !caseEnded(c2) {
		t.Fatalf("a file about an unannounced address should end, got %s", c2.Stage)
	}
	if !strings.Contains(strings.Join(c2.History, "\n"), "no network announces") {
		t.Fatalf("the file should record that no network announces the address:\n%s", strings.Join(c2.History, "\n"))
	}
}

// caseEnded reports whether a file reached one of the unit's dispositions.
func caseEnded(c *core.AbuseCase) bool {
	switch c.Stage {
	case core.StageLEClosed, core.StageLECharged, core.StageLEDropped:
		return true
	}
	return false
}

func containsSubject(subs []string, want string) bool {
	for _, s := range subs {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

func TestLawJobsAreRealWork(t *testing.T) {
	w := secSetup(t)
	job := func(key string) *core.Job {
		for _, j := range w.OpenJobs() {
			if j.Verify == key {
				return j
			}
		}
		t.Fatalf("no open job verified by %q", key)
		return nil
	}
	intake := job("law-intake")
	if err := w.AcceptJob("alex", intake.ID); err != nil {
		t.Fatalf("accept %s: %v", intake.ID, err)
	}
	intake = job("law-intake")
	if ok, why := w.VerifyJob(intake); ok {
		t.Fatalf("the intake job must not pass before the shift: %s", why)
	}
	order := job("law-warrant")
	if err := w.AcceptJob("alex", order.ID); err != nil {
		t.Fatalf("accept %s: %v", order.ID, err)
	}
	order = job("law-warrant")
	if ok, why := w.VerifyJob(order); ok {
		t.Fatalf("the warrant job must not pass before an order exists: %s", why)
	}

	// the shift: read a file and send the request by hand
	c := lawFile(t, w, householdAddress(w), "netcrest-noc",
		[]string{"ISP records: the line hammered a provider desk", "provider report: the same address"})
	lawWalk(w, 1)
	if _, err := w.DeskAct(c.ID, "cases", "request", "the complaint names an address this network announces"); err != nil {
		t.Fatalf("request: %v", err)
	}
	if ok, why := w.VerifyJob(intake); !ok {
		t.Fatalf("a lawful request sent by hand should finish the shift: %s", why)
	}

	// the investigator's work: disclosure, investigation, order
	lawWalk(w, 2)
	if c := w.CaseByID(c.ID); c.Stage != core.StageLEDisclose {
		t.Fatalf("the network should have disclosed by now, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	if err := w.LawInvestigate(w.CaseByID(c.ID), "agent"); err != nil {
		t.Fatalf("investigate: %v", err)
	}
	if _, err := w.DeskAct(c.ID, "agent", "warrant", "the file names the subscriber and holds two networks' records"); err != nil {
		t.Fatalf("warrant: %v", err)
	}
	if ok, why := w.VerifyJob(order); !ok {
		t.Fatalf("an order obtained and served should finish the job: %s", why)
	}
	fmt.Println("law jobs done:", intake.ID, order.ID)
}

// A hosting platform can name the rack and not the customer. That is not a
// subscriber, and §35 says so: the file ends with the honest reason instead of
// producing an order against a machine that belongs to nobody in the world.
func TestPlatformThatNamesNobodyProducesNoOrder(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	if sc == nil {
		t.Fatal("the world should have its scanner")
	}
	if sc.Owner != "" {
		t.Fatalf("this test needs a host with no account on file, %s has %q", sc.ID, sc.Owner)
	}
	c := lawFile(t, w, sc.WANIP(), "netcrest-noc", []string{"provider report: sustained probing", "gateway log: 400 filtered drops"})
	lawWalk(w, 12)
	c = w.CaseByID(c.ID)
	if len(w.LawWarrants()) != 0 {
		t.Fatal("a machine with no customer on file must not yield an order")
	}
	if c.Stage != core.StageLEClosed && c.Stage != core.StageLEDropped {
		t.Fatalf("the file should end honestly, got %s:\n%s", c.Stage, strings.Join(c.History, "\n"))
	}
	why := strings.Join(c.History, "\n")
	if !strings.Contains(why, "no customer on file") && !strings.Contains(why, "no customer") {
		t.Fatalf("the file should record what the platform could not name:\n%s", why)
	}
}
