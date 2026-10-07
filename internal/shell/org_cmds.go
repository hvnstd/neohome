package shell

import (
	"fmt"
	"strings"
)

// Organisations: crews with rosters, treasuries and escrowed contracts.
// Money only enters through contributions; spending shared money is the
// owner's signature; completing posted work pays from the hold, never minted.

func init() {
	builtinTable["org"] = cmdOrg
	builtinTable["player"] = cmdPlayer
}

func cmdPlayer(s *Shell, args []string) int {
	if len(args) == 0 || args[0] == "list" {
		fmt.Fprintf(s.Out, "%-12s %-12s %s\n", "PLAYER", "PC", "HOUSE")
		for _, p := range s.W.Players {
			house := p.HouseKey
			fmt.Fprintf(s.Out, "%-12s %-12s %s\n", p.Name, p.PC, house)
		}
		return 0
	}
	if args[0] == "invite" {
		if len(args) < 3 {
			s.errf("usage: player invite NAME TEMP-PASS  ($20.00 setup from your account)")
			return 1
		}
		p, err := s.W.InvitePlayer(s.User.Name, args[1], args[2])
		if err != nil {
			s.errf("player: %v", err)
			return 1
		}
		d := s.W.Devices[p.PC]
		fmt.Fprintf(s.Out, "%s vouched for $20.00 — pc %s, login and run passwd first\n", p.Name, d.FirstLANIP())
		return 0
	}
	s.errf("usage: player [list|invite NAME TEMP-PASS]")
	return 1
}

func cmdOrg(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		orgs := s.W.Orgs()
		if len(orgs) == 0 {
			fmt.Fprintln(s.Out, "no crews yet — found one with: org create NAME")
			return 0
		}
		fmt.Fprintf(s.Out, "%-14s %-10s %-7s %10s  %s\n", "ORG", "FOUNDER", "CREW", "TREASURY", "OPEN CONTRACTS")
		for _, o := range orgs {
			open := 0
			for _, j := range s.W.Jobs.List {
				if j.Org == o.Name && !j.Done {
					open++
				}
			}
			fmt.Fprintf(s.Out, "%-14s %-10s %-7d %10s  %d\n",
				o.Name, o.Founder, len(o.Members), fmtMoney(balanceOf(s, "org:"+o.Name)), open)
		}
		return 0
	case "info", "members", "show":
		return orgInfo(s, args[1:])
	case "create":
		return orgCreate(s, args[1:])
	case "invite":
		return orgInvite(s, args[1:])
	case "join":
		return orgJoin(s, args[1:])
	case "leave":
		return orgLeave(s, args[1:])
	case "kick":
		return orgKick(s, args[1:])
	case "contribute", "fund":
		return orgContribute(s, args[1:])
	case "withdraw":
		return orgWithdraw(s, args[1:])
	case "post":
		return orgPost(s, args[1:])
	case "cancel":
		return orgCancel(s, args[1:])
	}
	s.errf("usage: org [list|info ORG|create NAME|invite ORG WHO|join ORG|leave ORG|kick ORG WHO|contribute ORG $AMT|withdraw ORG $AMT|post ORG TITLE $PAY VERIFY [HELP]|cancel ORG JOB]")
	return 1
}

func orgInfo(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: org info ORG")
		return 1
	}
	o := s.W.Clans[args[0]]
	if o == nil {
		s.errf("no such org: %s", args[0])
		return 1
	}
	fmt.Fprintf(s.Out, "%s — founded by %s\n", o.Name, o.Founder)
	fmt.Fprintf(s.Out, "treasury: %s\n", fmtMoney(balanceOf(s, "org:"+o.Name)))
	fmt.Fprintf(s.Out, "crew:\n")
	for m, role := range o.Members {
		fmt.Fprintf(s.Out, "  %-12s %s\n", m, role)
	}
	if len(o.Hold) > 0 {
		fmt.Fprintf(s.Out, "escrowed:\n")
		for id, amt := range o.Hold {
			fmt.Fprintf(s.Out, "  %-8s %s\n", id, fmtMoney(amt))
		}
	}
	fmt.Fprintf(s.Out, "open contracts:\n")
	open := false
	for _, j := range s.W.Jobs.List {
		if j.Org == o.Name && !j.Done {
			fmt.Fprintf(s.Out, "  %-8s %-30s %s  %s\n", j.ID, j.Title, fmtMoney(j.Pay), j.Verify)
			open = true
		}
	}
	if !open {
		fmt.Fprintf(s.Out, "  (none)\n")
	}
	return 0
}

func balanceOf(s *Shell, acct string) int64 {
	if a := s.W.Bank.Accts[acct]; a != nil {
		return a.Balance
	}
	return 0
}

func orgCreate(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: org create NAME")
		return 1
	}
	if _, err := s.W.CreateOrg(s.User.Name, args[0]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "crew %s founded — invite with: org invite %s WHO\n", args[0], args[0])
	return 0
}

func orgInvite(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: org invite ORG WHO")
		return 1
	}
	if err := s.W.OrgInvite(s.User.Name, args[0], args[1]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s invited to %s — they join with: org join %s\n", args[1], args[0], args[0])
	return 0
}

func orgJoin(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: org join ORG")
		return 1
	}
	if err := s.W.OrgJoin(s.User.Name, args[0]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "welcome to %s\n", args[0])
	return 0
}

func orgLeave(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: org leave ORG")
		return 1
	}
	if err := s.W.OrgLeave(s.User.Name, args[0]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "left %s\n", args[0])
	return 0
}

func orgKick(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: org kick ORG WHO")
		return 1
	}
	if err := s.W.OrgKick(s.User.Name, args[0], args[1]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s kicked from %s\n", args[1], args[0])
	return 0
}

func orgContribute(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: org contribute ORG $AMT")
		return 1
	}
	amt, err := marketDollars(args[1])
	if err != nil {
		s.errf("org: bad amount %q (whole dollars)", args[1])
		return 1
	}
	if err := s.W.OrgContribute(s.User.Name, args[0], amt); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "contributed %s to %s\n", fmtMoney(amt), args[0])
	return 0
}

func orgWithdraw(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: org withdraw ORG $AMT")
		return 1
	}
	amt, err := marketDollars(args[1])
	if err != nil {
		s.errf("org: bad amount %q (whole dollars)", args[1])
		return 1
	}
	if err := s.W.OrgWithdraw(s.User.Name, args[0], amt); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "withdrew %s from %s\n", fmtMoney(amt), args[0])
	return 0
}

func orgPost(s *Shell, args []string) int {
	if len(args) < 3 {
		s.errf("usage: org post ORG TITLE $PAY VERIFY [HELP...]")
		return 1
	}
	amt, err := marketDollars(args[2])
	if err != nil {
		s.errf("org: bad pay %q (whole dollars)", args[2])
		return 1
	}
	help := "first to satisfy " + args[3] + " collects"
	if len(args) > 4 {
		help = strings.Join(args[4:], " ")
	}
	j, err := s.W.OrgPost(s.User.Name, args[0], args[1], amt, args[3], help)
	if err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "contract %s posted on %s board: %s for %s (escrowed)\n", j.ID, args[0], j.Title, fmtMoney(amt))
	return 0
}

func orgCancel(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: org cancel ORG JOB")
		return 1
	}
	if err := s.W.OrgCancel(s.User.Name, args[0], args[1]); err != nil {
		s.errf("org: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s cancelled, hold refunded\n", args[1])
	return 0
}
