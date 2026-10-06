package core

import (
	"fmt"
	"strings"
	"time"
)

// §35: the unit at the top of the ladder.
//
// Law enforcement is not a faster desk and it is not omniscient. It has one
// resource a network does not: the power to compel a *subscriber record* under
// a lawful request — and almost no ability to see for itself. Everything in a
// file at the unit came from somewhere else: a victim's report, a network's
// records, a disclosure. That is why the ladder is long:
//
//	行为 → 受害者 → 报案 → 网管 / ISP / Provider → 日志 / 证据 → 案件 →
//	调查 → 更高等级调查
//
// and why nobody teleports to a house the day after the behaviour: a file has
// to arrive through intake, be read, have a lawful basis, be sent to the
// network that holds the address, come back with whatever that network is
// willing and able to say, and only then can an investigator spend hours on it
// — with the warrant as the last rung, and the disposition on the record.
//
// The unit's own blind spots are part of the state, not a comment: it cannot
// read any network's traffic, it cannot touch a machine without the network
// that carries it, and an address resolves to a subscriber only when a network
// discloses one.

// Case stages the unit adds to the ladder. They are terminal states of their
// own: a file that ends "charged" ended with a disposition, and a file that
// ends "dropped" ended with a reason.
const (
	StageLEInvestigation = "law:investigation"
	StageLEWarrant       = "law:warrant"
	StageLECharged       = "law:charged"
	StageLEDropped       = "law:dropped"
)

// Investigator hours are the unit's real resource: work costs hours, hours come
// back on the world clock, and a file that cannot pay waits in the queue.
const (
	lawHoursCap = 24.0 // a shift's worth of investigator time
	lawRefill   = 1.0  // hours restored per hour of sim time
	lawOpenCap  = 3    // files the unit can carry at once
)

// LawHoursCap is a shift's worth of investigator time, exported for the console.
const LawHoursCap = lawHoursCap

// lawNoNetwork is the first thing every file at the unit says about itself.
const lawNoNetwork = "the unit has no network of its own: this file holds what the complainant and the networks disclosed, and nothing else"

type lawPrice struct{ triage, request, investigate, warrant, close float64 }

var lawCost = lawPrice{triage: 1, request: 2, investigate: 8, warrant: 4, close: 1}

// Warrant is an order the unit obtained and served on the network that keeps
// the records. It is the difference between "the network told us what it chose
// to tell us" and "the network is required to keep and hand over the line's
// records" — and it is issued against a file that supports it, never on
// request.
type Warrant struct {
	ID       string
	Case     string
	Subject  string
	Address  string
	ServedOn string // the desk it was served on
	Issued   time.Time
	Service  string // what was ordered
}

// LawUnit is the state of the organisation that works the top of the ladder.
type LawUnit struct {
	DeviceID string
	Hours    float64
	Cap      int
	Last     time.Time
	Intake   int // complaints the unit has read out of its own mailbox
	Warrants []*Warrant
	Seq      int
	Blind    []string // what this unit cannot see, in its own words
	Notes    []string // the unit's log, for the console
}

// ensureLaw creates the unit's state on first use.
func (w *World) ensureLaw() *LawUnit {
	if w.Law == nil {
		le := w.lawDesk()
		id := ""
		if le != nil {
			id = le.DeviceID
		}
		w.Law = &LawUnit{DeviceID: id, Hours: lawHoursCap, Cap: lawOpenCap, Last: w.Sim,
			Blind: []string{
				"the unit has no network of its own: every record in a file came from somebody else",
				"an address resolves to a subscriber only if a network discloses one",
				"a carrier-grade NAT address resolves to nobody at all, and the unit cannot make it",
				"nothing can be seized from a machine: the unit cannot reach one",
				"a machine inside an organisation's network answers to that organisation's records, not the unit's",
			}}
	}
	return w.Law
}

// Law returns the unit's state, creating it if the world predates it.
func (w *World) LawUnit() *LawUnit { return w.ensureLaw() }

// LawEnforcement returns the unit's organisation, if the world has one.
func (w *World) LawEnforcement() *Desk { return w.lawDesk() }

// lawDeskAct is the unit's console: the actions a court may take, and the
// refusals for the actions a court may not. Read the switch as the unit's
// charter — it requests records under a lawful basis, works a file, obtains an
// order and closes with a disposition; it does not suspend a line, hand work to
// another desk, or act on a network's behalf.
func (w *World) lawDeskAct(le *Desk, c *AbuseCase, actor, action, note string) (*AbuseCase, error) {
	d := w.DeskDevice(le)
	var u *User
	if d != nil {
		u = d.FindUser(actor)
	}
	switch action {
	case "request":
		if note == "" {
			return nil, fmt.Errorf("a lawful request needs its basis on the file: --note \"what the request stands on\"")
		}
		if err := w.LawRequest(c, note); err != nil {
			return nil, err
		}
	case "investigate":
		if !w.OnLawAgent(d, u) {
			return nil, fmt.Errorf("opening an investigation needs the agents group on %s; %s is not in it", le.DeviceID, actor)
		}
		if err := w.LawInvestigate(c, actor); err != nil {
			return nil, err
		}
	case "warrant":
		if !w.OnLawAgent(d, u) {
			return nil, fmt.Errorf("signing an order needs the agents group on %s; %s is not in it", le.DeviceID, actor)
		}
		if _, err := w.LawWarrant(c, actor); err != nil {
			return nil, err
		}
	case "close":
		if note == "" {
			return nil, fmt.Errorf("a file closes with a disposition: --note \"charged\" or \"no further action: why\"")
		}
		if err := w.LawClose(c, note, actor); err != nil {
			return nil, err
		}
	case "notify", "refer", "suspend", "escalate":
		return nil, fmt.Errorf("%s is not a power the unit has: it asks a network for records and for action, it does not act on a line itself", action)
	default:
		return nil, fmt.Errorf("unknown action %q (request, investigate, warrant, close)", action)
	}
	c.History = append(c.History, fmt.Sprintf("%s operator %s: %s — %s", w.Sim.Format("15:04"), actor, action, note))
	if d != nil {
		d.Logf("info", "cases", "%s %s by %s", c.ID, action, actor)
	}
	return c, nil
}

// LawHours reports the investigator time left.
func (w *World) LawHours() float64 { return w.ensureLaw().Hours }

// LawWarrants lists the orders the unit has obtained.
func (w *World) LawWarrants() []*Warrant { return w.ensureLaw().Warrants }

// LawBlindSpots returns the unit's own list of what it cannot see.
func (w *World) LawBlindSpots() []string { return w.ensureLaw().Blind }

// OnLawAgent reports whether an account may do an investigator's work: issuing
// a warrant is a bigger power than reading the intake queue, and the world
// checks the account's groups rather than a name.
func (w *World) OnLawAgent(d *Device, u *User) bool {
	if d == nil || u == nil {
		return false
	}
	return inAnyGroup(u, "agents")
}

// ---- the tick --------------------------------------------------------------

// LawTick refills the unit's hours, reads its intake, and moves every file one
// rung if that file's own deadline has come.
func (w *World) LawTick() {
	u := w.ensureLaw()
	if u.Last.IsZero() {
		u.Last = w.Sim
	}
	if hours := w.Sim.Sub(u.Last).Hours(); hours > 0 {
		u.Hours += hours * lawRefill
		if u.Hours > lawHoursCap {
			u.Hours = lawHoursCap
		}
		u.Last = w.Sim
	}
	w.lawIntake()
	le := w.lawDesk()
	if le == nil || !w.DeskOperational(le) {
		return
	}
	for _, c := range w.Cases(le.DeviceID) {
		if caseTerminal(c.Stage) {
			continue
		}
		w.advanceLaw(le, c)
	}
}

// lawIntake reads the unit's own mailbox. A complaint that names an address
// becomes a file; one that names none is written into the unit's log instead of
// being turned into a guess.
func (w *World) lawIntake() {
	u := w.ensureLaw()
	le := w.lawDesk()
	if le == nil || u.DeviceID == "" {
		return
	}
	d := w.DeskDevice(le)
	if d == nil {
		return
	}
	msgs := inboxMessages(d, le.Mailbox)
	if len(msgs) <= u.Intake {
		return
	}
	for _, m := range msgs[u.Intake:] {
		u.Intake++
		ip := firstAddress(m.Subject + " " + m.Body)
		if ip == "" {
			u.Notes = appendNote(u.Notes, fmt.Sprintf("%s complaint from %s names no address; nothing to open", w.Sim.Format("15:04"), m.From))
			continue
		}
		if w.CaseAboutAt(le.DeviceID, ip) {
			continue // already a file about this address
		}
		if len(w.Cases(le.DeviceID)) >= u.Cap {
			u.Notes = appendNote(u.Notes, fmt.Sprintf("%s complaint from %s about %s waits: the unit is carrying %d file(s)", w.Sim.Format("15:04"), m.From, ip, u.Cap))
			continue
		}
		c := w.openCase(le, ip, m.From, "complaint by mail", []string{"complaint received by " + le.Mailbox + ": " + complaintLine(m.Body)}, le.SLA)
		c.History = append(c.History, fmt.Sprintf("%s the unit's intake read the complaint into a file", w.Sim.Format("15:04")))
		d.Logf("notice", "cases", "%s: complaint from %s about %s", c.ID, m.From, ip)
	}
}

// inboxMessage is one message as it sits in /var/mail/<user>.
type inboxMessage struct {
	From    string
	Subject string
	Body    string
}

func inboxMessages(d *Device, user string) []inboxMessage {
	data, ok := d.FS.Read(MailboxPath(user))
	if !ok {
		return nil
	}
	var out []inboxMessage
	var cur inboxMessage
	inBody := false
	flush := func() {
		if cur.From != "" || cur.Subject != "" {
			out = append(out, cur)
		}
		cur = inboxMessage{}
	}
	// the mbox format: a message opens with "From <sender> <date>", headers run
	// to the first blank line, and a body line that looks like a separator is
	// escaped as ">From " so a message can never swallow the next one
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "From ") && !strings.HasPrefix(line, "From: "):
			flush()
			f := strings.Fields(line)
			if len(f) > 1 {
				cur.From = f[1]
			}
			inBody = false
		case !inBody && strings.HasPrefix(line, "Subject: "):
			cur.Subject = strings.TrimSpace(strings.TrimPrefix(line, "Subject: "))
		case !inBody && strings.HasPrefix(line, "Date: "):
			// kept for the reader, not needed to file
		case !inBody && strings.TrimSpace(line) == "":
			inBody = true
		case inBody:
			cur.Body += line + "\n"
		}
	}
	flush()
	return out
}

func complaintLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return s
}

// firstAddress pulls the first address out of a complaint. It reads only what
// is written: no completion, no guessing at hostnames.
func firstAddress(s string) string {
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == ';' || r == '(' || r == ')'
	}) {
		f := strings.Trim(field, ".,:<>\"'[]")
		if f == "" {
			continue
		}
		info := ClassifyAddr(f)
		if info.Kind == KindUnknown || info.Kind == KindUnassigned {
			continue
		}
		if strings.Contains(f, ":") || strings.Count(f, ".") == 3 {
			return f
		}
	}
	return ""
}

func appendNote(list []string, s string) []string {
	list = append(list, s)
	if len(list) > 40 {
		list = list[len(list)-40:]
	}
	return list
}

// ---- the ladder ------------------------------------------------------------

// LawSupport counts the artifacts a file is standing on: what the networks
// disclosed, what a desk found in its own records, and what the complainant
// showed. It is what a warrant is granted against, in this world as in a real
// one, and it is a count of records — not of accusations.
func (w *World) LawSupport(c *AbuseCase) int {
	if c == nil {
		return 0
	}
	n := len(c.Evidence) + len(c.Findings)
	if c.Account != "" {
		n++
	}
	if c.SubjectDev != "" {
		n++
	}
	return n
}

// namedSubscriber reports whether what a network disclosed is a person on file,
// not a machine with nobody's name on it. A platform can name the rack and stop
// there; an order cannot be signed against a rack.
func namedSubscriber(sub string) bool {
	return sub != "" && !strings.HasPrefix(sub, "unassigned")
}

// CanWarrant is the supervisor's rule, in one place: a warrant needs a named
// subscriber and a file that stands on more than one network's records.
func (w *World) CanWarrant(c *AbuseCase) (bool, string) {
	if !namedSubscriber(c.Account) {
		return false, "no subscriber has been disclosed: there is nobody to name on a warrant"
	}
	if n := w.LawSupport(c); n < 5 {
		return false, fmt.Sprintf("the file stands on %d artifact(s); a warrant needs the subscriber, the network's records and the complainant's own", n)
	}
	return true, ""
}

// lawSpend takes hours from the unit, or reports that the work has to wait.
func (w *World) lawSpend(c *AbuseCase, cost float64, why string) bool {
	u := w.ensureLaw()
	if u.Hours < cost {
		c.Due = w.Sim.Add(30 * time.Minute)
		u.Notes = appendNote(u.Notes, fmt.Sprintf("%s %s waits: %.0f of %.0f investigator hours left", w.Sim.Format("15:04"), c.ID, u.Hours, lawHoursCap))
		return false
	}
	u.Hours -= cost
	_ = why
	return true
}

// advanceLaw walks one file at the unit one rung, when that file is due.
func (w *World) advanceLaw(le *Desk, c *AbuseCase) {
	switch c.Stage {
	case StageFiled, StageLEOpened:
		// intake: what the file is, and what the unit cannot see for itself
		if w.Sim.Before(c.Due) {
			return
		}
		if !w.lawSpend(c, lawCost.triage, "intake") {
			return
		}
		c.Findings = append(c.Findings, lawNoNetwork)
		w.caseStage(c, StageTriaged, "intake read the complaint: a lawful basis and an address to ask about")
		c.Due = w.Sim.Add(time.Duration(le.SLA) * time.Minute)

	case StageTriaged:
		if w.Sim.Before(c.Due) {
			return
		}
		if err := w.LawRequest(c, "the file was read by intake and a network answers for the address"); err != nil {
			w.caseStage(c, StageLEDropped, err.Error())
			return
		}

	case StageLERequest:
		if w.Sim.Before(c.Due) {
			return
		}
		sub := w.subscriberRecord(c.SubjectIP)
		if !namedSubscriber(sub) {
			if sub != "" {
				c.History = append(c.History, fmt.Sprintf("%s the network answered: %s, no customer on file", w.Sim.Format("15:04"), sub))
				w.caseStage(c, StageLEClosed, "the network named the machine but no customer: there is nobody to name on an order")
				return
			}
			if w.SharedAddress(c.SubjectIP) {
				c.History = append(c.History, fmt.Sprintf("%s the network answered: carrier-grade NAT, no single subscriber", w.Sim.Format("15:04")))
				w.caseStage(c, StageLEClosed, "the address is carrier-grade NAT: the provider cannot name one subscriber")
				return
			}
			w.caseStage(c, StageLEClosed, "the network answered: no subscriber record for this address inside the retention window")
			return
		}
		c.Account = sub
		c.History = append(c.History, fmt.Sprintf("%s the network disclosed subscriber %q under the lawful request", w.Sim.Format("15:04"), sub))
		// an organisation that holds the record says so: the people inside it
		// are that organisation's records, which the unit can ask for and
		// cannot read for itself
		if dev, _ := w.subjectDevice(c.SubjectIP); dev != "" {
			if d := w.Devices[dev]; d != nil && d.Owner != "" {
				c.Findings = append(c.Findings, fmt.Sprintf("the disclosed subscriber is %s: the users inside that network are its own records", d.Owner))
			}
		}
		w.caseStage(c, StageLEDisclose, "subscriber record obtained under a lawful request")

	case StageLEDisclose:
		if w.Sim.Before(c.Due) {
			return
		}
		if w.LawSupport(c) < 3 {
			w.caseStage(c, StageLEClosed, "no further action: the file does not stand on enough records to investigate")
			return
		}
		if err := w.LawInvestigate(c, ""); err != nil {
			w.caseStage(c, StageLEClosed, err.Error())
		}

	case StageLEInvestigation:
		if w.Sim.Before(c.Due) {
			return
		}
		if ok, why := w.CanWarrant(c); !ok {
			w.caseStage(c, StageLEClosed, "no further action: "+why)
			return
		}
		if _, err := w.LawWarrant(c, ""); err != nil {
			w.caseStage(c, StageLEClosed, err.Error())
		}

	case StageLEWarrant:
		if w.Sim.Before(c.Due) {
			return
		}
		w.caseStage(c, StageLECharged, "the matter is charged: the file carries the disclosure, the records and the order")
	}
}

// LawRequest sends the lawful request to the network that answers for the
// address, and records what was asked. Sending it costs investigator time and
// is a real mail to a real desk.
func (w *World) LawRequest(c *AbuseCase, basis string) error {
	le := w.lawDesk()
	if le == nil {
		return fmt.Errorf("the unit has no desk")
	}
	if c.DeskID != le.DeviceID {
		return fmt.Errorf("%s is not a file the unit holds", c.ID)
	}
	if c.Stage != StageTriaged {
		return fmt.Errorf("%s must be triaged before a request goes out (it is %s)", c.ID, c.Stage)
	}
	target := w.DeskFor(c.SubjectIP)
	if target == nil {
		return fmt.Errorf("no network announces %s: there is nobody to ask", c.SubjectIP)
	}
	if target.DeviceID == le.DeviceID {
		return fmt.Errorf("%s is inside the unit's own address space", c.SubjectIP)
	}
	if target.Kind == DeskRegistry {
		return fmt.Errorf("the registry publishes allocation records only: it can name the network that holds %s, not the subscriber, and it is not the network to ask", c.SubjectIP)
	}
	if !w.lawSpend(c, lawCost.request, "the request") {
		return fmt.Errorf("no investigator hours left for the request")
	}
	body := fmt.Sprintf("Lawful request from %s.\n\nCase:     %s\nAddress:  %s\nBasis:    %s\n\nThe unit is asking for the subscriber record on file for this address,\nand for the records your network keeps about it. Nothing else.\n",
		le.Org, c.ID, c.SubjectIP, basis)
	res := w.RouteMail(w.DeskDevice(le), le.Mailbox, target.Mailbox+"@"+hostnameOf(w, target), "lawful request "+c.ID+" — "+c.SubjectIP, body)
	delivery := "mailed to " + target.Mailbox + "@" + hostnameOf(w, target)
	if !res.Delivered {
		delivery = "could not be delivered to " + target.Org + ": " + res.Diagnostic
	}
	if !res.Delivered {
		return fmt.Errorf("the request to %s could not be delivered: %s", target.Org, res.Diagnostic)
	}
	c.Requested = target.DeviceID
	w.caseStage(c, StageLERequest, fmt.Sprintf("lawful request sent to %s (%s)", target.Org, delivery))
	c.Due = w.Sim.Add(time.Duration(target.SLA) * time.Minute)
	if d := w.DeskDevice(target); d != nil {
		d.Logf("notice", "compliance", "%s: lawful request from %s received; subscriber records under review", c.ID, le.Org)
	}
	return nil
}

// LawInvestigate opens the investigation rung: the unit spends a shift's worth
// of hours, asks the network that holds the address for the records it keeps
// about the line, and tells the subscriber that their connection's records are
// under an investigation. Nothing is seized, because the unit cannot reach a
// machine — that is its blind spot, stated on the file.
func (w *World) LawInvestigate(c *AbuseCase, actor string) error {
	le := w.lawDesk()
	if le == nil {
		return fmt.Errorf("the unit has no desk")
	}
	switch c.Stage {
	case StageLEDisclose, StageLEInvestigation:
	default:
		return fmt.Errorf("%s cannot be investigated from %s: a subscriber has to be disclosed first", c.ID, c.Stage)
	}
	if !namedSubscriber(c.Account) {
		return fmt.Errorf("%s has no disclosed subscriber", c.ID)
	}
	if !w.lawSpend(c, lawCost.investigate, "the investigation") {
		return fmt.Errorf("no investigator hours left: the file waits for the next shift")
	}
	// the network's own connection records, read from its own state
	holder := w.DeskFor(c.SubjectIP)
	if holder == nil {
		return fmt.Errorf("no network announces %s", c.SubjectIP)
	}
	recs := w.deskFindings(holder, c.SubjectIP)
	src := "connection records obtained from " + holder.Org
	if len(recs) == 0 {
		recs = []string{"the network holds no records about this address inside its retention window"}
	}
	for _, r := range recs {
		c.Findings = append(c.Findings, src+": "+r)
	}
	c.Findings = append(c.Findings, "the unit cannot seize anything: it has no path to a machine, only to the records the network keeps")
	if actor != "" {
		c.InvestigatedBy = actor
	}
	if w.Sim.Before(c.Due) {
		// a rung that was forced from the console still has to have happened
		c.Due = w.Sim
	}
	w.caseStage(c, StageLEInvestigation, fmt.Sprintf("investigation opened: %.0f investigator hours, records obtained from %s", lawCost.investigate, holder.Org))
	c.Due = w.Sim.Add(time.Duration(le.SLA) * time.Minute)
	w.serveSubjectNotice(c, le, "an investigation into this connection",
		fmt.Sprintf("%s has opened an investigation (%s) into conduct attributed to this connection.\nNothing has been seized. If you have something to say about it, answer this message and quote the case number.\n", le.Org, c.ID))
	if d := w.DeskDevice(le); d != nil {
		d.Logf("warn", "cases", "%s: investigation opened into %s (%s)", c.ID, c.SubjectIP, c.Account)
	}
	w.News = append(w.News, fmt.Sprintf("%s opened an investigation into conduct from %s", le.Org, c.SubjectIP))
	return nil
}

// LawWarrant is the last rung: an order for the records the network keeps,
// served on the network, with the subscriber told. It is refused unless the
// file supports it.
func (w *World) LawWarrant(c *AbuseCase, actor string) (*Warrant, error) {
	le := w.lawDesk()
	if le == nil {
		return nil, fmt.Errorf("the unit has no desk")
	}
	if ok, why := w.CanWarrant(c); !ok {
		return nil, fmt.Errorf("the file does not support a warrant: %s", why)
	}
	if !w.lawSpend(c, lawCost.warrant, "the warrant") {
		return nil, fmt.Errorf("no investigator hours left for the warrant")
	}
	u := w.ensureLaw()
	u.Seq++
	holder := w.DeskFor(c.SubjectIP)
	wr := &Warrant{ID: fmt.Sprintf("WR-%06d", u.Seq), Case: c.ID, Subject: c.Account,
		Address: c.SubjectIP, Issued: w.Sim, Service: "subscriber details and records retention on the line"}
	if holder != nil {
		wr.ServedOn = holder.DeviceID
		body := fmt.Sprintf("Order %s from %s.\n\nCase:     %s\nAddress:  %s\nSubscriber: %s\nOrdered:  %s\n\nThe network is required to preserve the records it keeps about this line from this date,\nand to hand them over when the unit asks. No other data is covered.\n",
			wr.ID, le.Org, c.ID, c.SubjectIP, c.Account, wr.Service)
		res := w.RouteMail(w.DeskDevice(le), le.Mailbox, holder.Mailbox+"@"+hostnameOf(w, holder), "order "+wr.ID+" — "+c.SubjectIP, body)
		if res.Delivered {
			c.History = append(c.History, fmt.Sprintf("%s order %s served on %s", w.Sim.Format("15:04"), wr.ID, holder.Org))
			if d := w.DeskDevice(holder); d != nil {
				d.Logf("warn", "compliance", "%s: order %s received; records retention on %s", c.ID, wr.ID, c.SubjectIP)
			}
		} else {
			c.History = append(c.History, fmt.Sprintf("%s order %s could not be served: %s", w.Sim.Format("15:04"), wr.ID, res.Diagnostic))
		}
	}
	u.Warrants = append(u.Warrants, wr)
	c.Action = "order " + wr.ID
	w.serveSubjectNotice(c, le, "an order for your connection's records",
		fmt.Sprintf("%s obtained order %s for the records %s keeps about this connection.\nNothing has been taken from your machines. The records are preserved and the file carries the order.\n", le.Org, wr.ID, orgOf(w, c.DeskID)))
	if actor != "" {
		c.History = append(c.History, fmt.Sprintf("%s investigator %s signed the order", w.Sim.Format("15:04"), actor))
	}
	w.caseStage(c, StageLEWarrant, fmt.Sprintf("warrant %s served: records retention on %s", wr.ID, c.SubjectIP))
	c.Due = w.Sim.Add(time.Duration(le.SLA) * time.Minute)
	if d := w.DeskDevice(le); d != nil {
		d.Logf("warn", "cases", "%s: order %s issued against subscriber %q", c.ID, wr.ID, c.Account)
	}
	w.AddEvent(le.DeviceID, "warn", "cases", "%s issued order %s (%s)", le.Org, wr.ID, c.Account)
	return wr, nil
}

// LawClose ends a file with a disposition and a reason, whoever ends it.
func (w *World) LawClose(c *AbuseCase, why, actor string) error {
	le := w.lawDesk()
	if le == nil {
		return fmt.Errorf("the unit has no desk")
	}
	if caseTerminal(c.Stage) {
		return fmt.Errorf("%s is already %s", c.ID, c.Stage)
	}
	if why == "" {
		return fmt.Errorf("a file closes with a disposition, not silently: give a reason")
	}
	w.lawSpend(c, lawCost.close, "closing")
	c.Action = "closed: " + why
	if strings.Contains(strings.ToLower(why), "charge") {
		w.caseStage(c, StageLECharged, why)
	} else {
		w.caseStage(c, StageLEDropped, why)
	}
	if actor != "" {
		c.History = append(c.History, fmt.Sprintf("%s investigator %s closed the file: %s", w.Sim.Format("15:04"), actor, why))
	}
	if d := w.DeskDevice(le); d != nil {
		d.Logf("info", "cases", "%s closed: %s", c.ID, why)
	}
	return nil
}

// serveSubjectNotice tells the subscriber whose connection is being looked at,
// through the account's own machine. If no account is on file the notice goes
// to the case file only, and the file says so — the unit cannot write to
// somebody it cannot name.
func (w *World) serveSubjectNotice(c *AbuseCase, le *Desk, subject, body string) {
	dev := c.SubjectDev
	account := c.Account
	if dev == "" || account == "" {
		// the machine that holds the address and the account on file for it:
		// what a network disclosed stays on the file, and this only fills in
		// what was missing
		d2, a2 := w.subjectDevice(c.SubjectIP)
		if dev == "" {
			dev = d2
		}
		if account == "" {
			account = a2
		}
		if c.SubjectDev == "" {
			c.SubjectDev = d2
		}
		if c.Account == "" {
			c.Account = a2
		}
	}
	if account == "" || dev == "" {
		c.Notices = append(c.Notices, "no subscriber on file to serve: notice recorded on the file only")
		return
	}
	d := w.Devices[dev]
	if d == nil {
		c.Notices = append(c.Notices, "the disclosed subscriber no longer maps to a machine in the world")
		return
	}
	// the notice goes to the account, wherever that account really reads mail:
	// the machine that holds the address may be the subscriber's router, which
	// has no mailbox of its own
	target := d
	if target.FindUser(account) == nil {
		// the account's own machine, then any machine that account owns
		if pc := w.Devices[w.PlayerDeviceID(account)]; pc != nil && pc.FindUser(account) != nil {
			target = pc
		} else {
			for _, id := range w.Order {
				if dd := w.Devices[id]; dd != nil && dd.Owner == account && dd.FindUser(account) != nil {
					target = dd
					break
				}
			}
		}
	}
	to := account + "@" + target.Hostname
	res := w.RouteMail(w.DeskDevice(le), le.Mailbox, to, subject+" ("+c.ID+")", body)
	note := ""
	switch {
	case res.Delivered:
		note = "served on " + to + " (" + res.Via + ")"
	case res.Queued:
		note = "queued for " + to + ": " + res.Diagnostic
	default:
		note = "not deliverable over the network to " + to + ": " + res.Diagnostic
	}
	// whatever the post does, the account's own mailbox is where its holder
	// reads: the unit cannot pretend a notice was received because it was sent
	if pc := w.Devices[w.PlayerDeviceID(account)]; pc != nil && !res.Delivered {
		if err := w.DeliverLocal(pc, le.Mailbox+"@"+hostnameOf(w, le), account, subject+" ("+c.ID+")", body); err == nil {
			note += "; copy left in the account's mailbox on " + pc.Hostname
		}
	}
	c.Notices = append(c.Notices, note)
	// and the household's own device sees it, because in this world the
	// subscriber and the player are the same account
	if d.Owner != "" {
		if pc := w.Devices[w.PlayerDeviceID(d.Owner)]; pc != nil && pc != d && pc != target {
			w.DeliverLocal(pc, le.Mailbox+"@"+hostnameOf(w, le), d.Owner, subject+" ("+c.ID+")", body)
		}
	}
}
