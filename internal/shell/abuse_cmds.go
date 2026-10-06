package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"neohome/internal/core"
)

// ---------------------------------------------------------------------------
// §34 取证 / 网管 / ISP / Provider — the shell's half
//
// One command, `abuse`, carries the whole relationship between a player and the
// organisations that investigate: filing a report with the network that answers
// for an address, reading your own tickets, and — for an account that really
// works at a desk — the operator console. The console is not a role flag: it is
// the desk's own machine and the desk's own Unix group, checked by
// core.OnDeskStaff.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"abuse", cmdAbuseView}, {"ticket", cmdAbuseStatus},
	} {
		builtinTable[e.name] = e.fn
	}
}

// cmdAbuseView dispatches the subcommands. A bare `abuse` is the player's own
// view of their tickets; the console subcommands are gated per-desk below.
func cmdAbuseView(s *Shell, args []string) int {
	if len(args) == 0 {
		return cmdAbuseStatus(s, nil)
	}
	switch args[0] {
	case "report", "file":
		return cmdAbuseReport(s, args[1:])
	case "status", "list", "mine":
		return cmdAbuseStatus(s, args[1:])
	case "evidence":
		return cmdAbuseEvidence(s, args[1:])
	case "desks":
		return cmdAbuseDesks(s, args[1:])
	case "queue":
		return cmdAbuseQueue(s, args[1:])
	case "show":
		return cmdAbuseShow(s, args[1:])
	case "triage":
		return cmdAbuseTriage(s, args[1:])
	case "act":
		return cmdAbuseAct(s, args[1:])
	}
	s.errf("usage: abuse [report <ip>|status [ticket]|evidence <ip>|desks|queue|show <ticket>|triage <ticket> --note text|act <ticket> notify|refer|suspend|escalate|close]")
	return 1
}

// deskForDevice finds the desk whose machine this is, if any: a player who has
// ssh'd into a provider's abuse desk should be able to work it, and nowhere
// else should offer the console.
func (s *Shell) deskForDevice() *core.Desk {
	for _, id := range s.W.Order {
		if id == s.Dev.ID {
			return s.W.Desk(s.Dev.ID)
		}
	}
	return nil
}

func cmdAbuseDesks(s *Shell, args []string) int {
	if s.W.WAN == nil || len(s.W.Desks) == 0 {
		fmt.Fprintln(s.Out, "no organisations are reachable from here")
		return 1
	}
	fmt.Fprintf(s.Out, "organisations that investigate, and what they can see\n\n")
	fmt.Fprintf(s.Out, "%-24s %-15s %-9s %-14s %s\n", "ORG", "HOST", "ASN", "KIND", "REACHABLE BY")
	for _, dk := range s.W.Desks {
		d := s.W.DeskDevice(dk)
		if d == nil {
			continue
		}
		reach := "report to " + dk.Mailbox + "@" + d.Hostname
		if dk.Kind == core.DeskLaw {
			reach = "cases@" + d.Hostname + " (lawful requests only)"
		}
		state := "open"
		if !s.W.DeskOperational(dk) {
			state = "queue closed"
		}
		fmt.Fprintf(s.Out, "%-24s %-15s AS%-7d %-14s %s [%s]\n",
			trunc(dk.Org, 24), d.Hostname, dk.ASN, dk.Kind, reach, state)
	}
	return 0
}

// cmdAbuseEvidence shows what this machine can actually prove about an address:
// the same lines a report would carry. It is the honesty check that keeps a
// report from being an accusation with nothing behind it.
func cmdAbuseEvidence(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: abuse evidence <ip>")
		return 1
	}
	ip := args[0]
	if r, ok, _ := core.DNSAnswer(s.Dev, ip); ok {
		ip = r
	}
	lines := core.AbuseEvidence(s.Dev, ip, 24*time.Hour)
	fmt.Fprintf(s.Out, "what %s recorded about %s in the last 24h\n", s.Dev.Hostname, ip)
	if len(lines) == 0 {
		fmt.Fprintf(s.Out, "  nothing. A report filed from here would carry no evidence.\n")
		return 1
	}
	for _, l := range lines {
		fmt.Fprintf(s.Out, "  %s\n", l)
	}
	return 0
}

func cmdAbuseReport(s *Shell, args []string) int {
	target := ""
	kind := "scanning"
	note := ""
	hours := 24
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		case "--note":
			if i+1 < len(args) {
				note = args[i+1]
				i++
			}
		case "--hours":
			if i+1 < len(args) {
				hours = atoiSafe(args[i+1])
				i++
			}
		default:
			if target == "" {
				target = args[i]
			}
		}
	}
	if target == "" {
		s.errf("usage: abuse report <ip|host> [--kind scanning|brute-force|spam] [--hours N] [--note text]")
		return 1
	}
	ip := target
	if core.ClassifyAddr(ip).Family == 0 {
		if r, ok, how := core.DNSAnswer(s.Dev, ip); ok {
			ip = r
		} else {
			s.errf("abuse: cannot resolve %s: %s", target, how)
			return 1
		}
	}
	desk := s.W.DeskFor(ip)
	if desk == nil {
		s.errf("abuse: no organisation answers for %s", ip)
		return 1
	}
	c, err := s.W.ReportAbuse(s.Dev, s.User.Name, ip, kind, note, time.Duration(hours)*time.Hour)
	if err != nil {
		s.errf("abuse: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "filed with %s\n", desk.Org)
	fmt.Fprintf(s.Out, "ticket:   %s\n", c.ID)
	fmt.Fprintf(s.Out, "address:  %s\n", ip)
	fmt.Fprintf(s.Out, "evidence: %d item(s) from %s's own records\n", len(c.Evidence), s.Dev.Hostname)
	fmt.Fprintf(s.Out, "\nThe desk works from its own records and yours. Track it with: abuse status\n")
	return 0
}

func cmdAbuseStatus(s *Shell, args []string) int {
	cases := s.W.CasesInvolving(s.User.Name)
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	if id != "" {
		c := s.W.CaseByID(id)
		if c == nil {
			s.errf("abuse: no such ticket: %s", id)
			return 1
		}
		if !caseBelongsTo(s, c) {
			s.errf("abuse: %s is not yours to read", c.ID)
			return 1
		}
		printCase(s, c, false)
		return 0
	}
	if len(cases) == 0 {
		fmt.Fprintln(s.Out, "no tickets involve you: nothing you reported, nothing reported about you")
		fmt.Fprintln(s.Out, "file one with: abuse report <ip>")
		return 0
	}
	fmt.Fprintf(s.Out, "%-10s %-11s %-16s %-20s %s\n", "TICKET", "STAGE", "ADDRESS", "DESK", "UPDATED")
	for _, c := range cases {
		fmt.Fprintf(s.Out, "%-10s %-11s %-16s %-20s %s\n", c.ID, c.Stage, c.SubjectIP,
			trunc(deskOrg(s.W, c.DeskID), 20), c.Updated.Format("15:04"))
	}
	fmt.Fprintln(s.Out, "\ndetails: abuse status <ticket>")
	return 0
}

// caseBelongsTo is the ownership check for the reading path: you may read a
// case you filed, and a case about a machine you own. Nobody reads somebody
// else's file — that is what the staff console is for.
func caseBelongsTo(s *Shell, c *core.AbuseCase) bool {
	if c.Reporter == s.User.Name || c.Account == s.User.Name {
		return true
	}
	// a report that joined an existing case is still the reporter's report:
	// the desk merged the tickets, it did not take the complaint away
	for _, r := range c.AlsoReported {
		if r == s.User.Name {
			return true
		}
	}
	if c.SubjectDev != "" {
		if d := s.W.Devices[c.SubjectDev]; d != nil && (d.Owner == s.User.Name || d.ID == s.Dev.ID) {
			return true
		}
	}
	for _, d := range s.W.Devices {
		if d.Owner == s.User.Name {
			for _, i := range d.Ifaces {
				if i.IP == c.SubjectIP || i.SharedIP == c.SubjectIP {
					return true
				}
			}
		}
	}
	return false
}

func printCase(s *Shell, c *core.AbuseCase, staff bool) {
	fmt.Fprintf(s.Out, "case %s — %s\n", c.ID, c.Stage)
	fmt.Fprintf(s.Out, "  desk:      %s\n", deskOrg(s.W, c.DeskID))
	fmt.Fprintf(s.Out, "  address:   %s\n", c.SubjectIP)
	if c.SubjectHost != "" {
		fmt.Fprintf(s.Out, "  machine:   %s\n", c.SubjectHost)
	}
	if c.Account != "" {
		fmt.Fprintf(s.Out, "  account:   %s\n", c.Account)
	}
	fmt.Fprintf(s.Out, "  kind:      %s\n", c.Kind)
	fmt.Fprintf(s.Out, "  reporter:  %s\n", c.Reporter)
	if !c.Due.IsZero() {
		fmt.Fprintf(s.Out, "  next step: %s\n", c.Due.Format("15:04"))
	}
	if len(c.Evidence) > 0 {
		fmt.Fprintln(s.Out, "\n  evidence supplied by the reporter:")
		for _, e := range c.Evidence {
			fmt.Fprintf(s.Out, "    %s\n", e)
		}
	}
	if len(c.Findings) > 0 {
		fmt.Fprintln(s.Out, "\n  the desk's own records:")
		for _, f := range c.Findings {
			fmt.Fprintf(s.Out, "    %s\n", f)
		}
	}
	if len(c.Notices) > 0 {
		fmt.Fprintln(s.Out, "\n  notices sent:")
		for _, n := range c.Notices {
			fmt.Fprintf(s.Out, "    %s\n", n)
		}
	}
	if c.Reply != "" {
		fmt.Fprintf(s.Out, "\n  the subject answered: %q\n", c.Reply)
	}
	if c.Action != "" {
		fmt.Fprintf(s.Out, "\n  what was really done: %s\n", c.Action)
	}
	fmt.Fprintln(s.Out, "\n  history:")
	for _, h := range c.History {
		fmt.Fprintf(s.Out, "    %s\n", h)
	}
}

// ---- the operator console --------------------------------------------------

func cmdAbuseQueue(s *Shell, args []string) int {
	dk := s.requireDesk()
	if dk == nil {
		return 1
	}
	q := s.W.DeskQueue(dk.DeviceID)
	fmt.Fprintf(s.Out, "%s — %s\n", dk.Org, s.Dev.Hostname)
	for _, line := range s.W.DeskSummary(dk.DeviceID) {
		fmt.Fprintln(s.Out, line)
	}
	if len(q) == 0 {
		fmt.Fprintln(s.Out, "\nqueue empty")
		return 0
	}
	fmt.Fprintf(s.Out, "\n%-10s %-11s %-16s %-12s %-6s %s\n", "TICKET", "STAGE", "ADDRESS", "KIND", "AGE", "NEXT")
	for _, c := range q {
		next := "-"
		if !c.Due.IsZero() && c.Due.After(s.W.Sim) {
			next = c.Due.Format("15:04")
		} else if !c.Due.IsZero() {
			next = "due now"
		}
		fmt.Fprintf(s.Out, "%-10s %-11s %-16s %-12s %-6s %s\n", c.ID, c.Stage, c.SubjectIP,
			trunc(c.Kind, 12), s.W.Sim.Sub(c.Opened).Round(time.Minute), next)
	}
	fmt.Fprintln(s.Out, "\nwork it with: abuse show <ticket> | abuse triage <ticket> --note text | abuse act <ticket> notify|refer|suspend|escalate|close")
	return 0
}

// requireDesk is the gate on every console command: you are standing on the
// desk's own machine, logged in as an account in its own group.
func (s *Shell) requireDesk() *core.Desk {
	dk := s.deskForDevice()
	if dk == nil {
		s.errf("abuse: this machine is not an investigating organisation's console")
		return nil
	}
	if !s.W.OnDeskStaff(s.Dev, s.User, dk.DeviceID) {
		s.errf("abuse: %s is not in the %q group on %s", s.User.Name, dk.Staff, s.Dev.Hostname)
		return nil
	}
	return dk
}

func cmdAbuseShow(s *Shell, args []string) int {
	dk := s.requireDesk()
	if dk == nil {
		return 1
	}
	if len(args) == 0 {
		s.errf("usage: abuse show <ticket>")
		return 1
	}
	c := s.W.CaseByID(args[0])
	if c == nil || c.DeskID != dk.DeviceID {
		s.errf("abuse: %s is not a case this desk holds", args[0])
		return 1
	}
	printCase(s, c, true)
	return 0
}

func cmdAbuseTriage(s *Shell, args []string) int {
	dk := s.requireDesk()
	if dk == nil {
		return 1
	}
	id, note := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--note":
			if i+1 < len(args) {
				note = args[i+1]
				i++
			}
		default:
			if id == "" {
				id = args[i]
			}
		}
	}
	if id == "" || note == "" {
		s.errf("usage: abuse triage <ticket> --note \"what you decided\"")
		return 1
	}
	c, err := s.W.DeskTriage(id, s.User.Name, note)
	if err != nil {
		s.errf("abuse: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s triaged: %d finding(s) in the desk's own records\n", c.ID, len(c.Findings))
	return 0
}

func cmdAbuseAct(s *Shell, args []string) int {
	dk := s.requireDesk()
	if dk == nil {
		return 1
	}
	if len(args) < 2 {
		s.errf("usage: abuse act <ticket> notify|refer|suspend|escalate|close [--note text]")
		return 1
	}
	id, action, note := args[0], args[1], ""
	for i := 2; i < len(args); i++ {
		if args[i] == "--note" && i+1 < len(args) {
			note = args[i+1]
			i++
		}
	}
	c, err := s.W.DeskAct(id, s.User.Name, action, note)
	if err != nil {
		s.errf("abuse: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s → %s\n", c.ID, c.Stage)
	for _, h := range c.History {
		if strings.Contains(h, c.Stage) || strings.Contains(h, "subject") {
			fmt.Fprintf(s.Out, "  %s\n", h)
		}
	}
	return 0
}

func deskOrg(w *core.World, deskID string) string {
	if dk := w.Desk(deskID); dk != nil {
		return dk.Org
	}
	if d := w.Devices[deskID]; d != nil {
		return d.Hostname
	}
	return deskID
}

// orgDirectory is a small helper for other commands that want to name the
// organisations in a listing without reaching into world fields.
func orgDirectory(w *core.World) []string {
	var out []string
	for _, dk := range w.Desks {
		out = append(out, fmt.Sprintf("%s (AS%d)", dk.Org, dk.ASN))
	}
	sort.Strings(out)
	return out
}
