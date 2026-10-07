package shell

import (
	"fmt"
	"sort"
	"strings"

	"neohome/internal/core"
)

// whois / rdap / bgp expose the world's public-internet attribution layer.
//
// The spec is explicit that a public IP must not yield a real identity
// (十三 IP 系统): "「公开 IP」不等于「公开玩家真实身份」. 查询 IP 时首先可能得到:
// Provider / ASN / Region / Network Range / Abuse Contact / rDNS". These
// commands therefore answer from the AS that announces a prefix, which is
// provider-level information and nothing more.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"whois", cmdWhois},
		{"rdap", cmdWhois},
		{"bgp", cmdBgp},
		{"asn", cmdBgp},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdWhois(s *Shell, args []string) int {
	target := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			target = a
			break
		}
	}
	if target == "" {
		s.errf("usage: whois IP")
		return 1
	}
	ip := target
	if core.ClassifyAddr(ip).Family == 0 {
		// a hostname: resolve it the way a real whois client does. A v6 literal
		// contains no dot, so "does it look like an address" has to be a real
		// parse rather than a substring test.
		if r, ok, _ := core.DNSAnswer(s.Dev, ip); ok {
			ip = r
		} else if r, ok, _ := core.DNSAnswerFamily(s.Dev, ip, 6); ok {
			ip = r
		} else {
			fmt.Fprintf(s.Out, "whois: no match for \"%s\"\n", target)
			return 1
		}
	}

	// §34: the registry is an organisation with a machine, not a local table.
	// A lookup that cannot reach it says so, exactly like a real whois client.
	if core.ClassifyAddr(ip).Public {
		if err := s.registryReachable(); err != "" {
			fmt.Fprintf(s.Out, "whois: %s\n", err)
			return 1
		}
	}

	a, ok := s.W.Lookup(ip)
	if !ok {
		info := core.ClassifyAddr(ip)
		switch info.Kind {
		case core.KindPrivate, core.KindLoopback, core.KindLinkLocal, core.KindULA:
			fmt.Fprintf(s.Out, "whois: %s\n", ip)
			fmt.Fprintf(s.Out, "No match for \"%s\".\n", ip)
			fmt.Fprintf(s.Out, "This is a %s address (%s); no registry holds a record of it.\n",
				info.Kind, info.Scope)
			return 1
		}
		fmt.Fprintf(s.Out, "whois: invalid address \"%s\"\n", target)
		return 1
	}

	fmt.Fprintf(s.Out, "whois: %s\n", ip)
	fmt.Fprintf(s.Out, "\n")
	if a.Netname == "UNASSIGNED" {
		fmt.Fprintf(s.Out, "No autonomous system announces this address.\n")
		fmt.Fprintf(s.Out, "The route exists in the transit graph but no holder claims it.\n")
		return 1
	}
	fmt.Fprintf(s.Out, "netname:      %s\n", a.Netname)
	fmt.Fprintf(s.Out, "origin:       AS%d %s\n", a.ASN, a.ASName)
	fmt.Fprintf(s.Out, "org:          %s\n", a.Org)
	fmt.Fprintf(s.Out, "range:        %s\n", a.Range)
	fmt.Fprintf(s.Out, "country:      %s\n", a.Country)
	fmt.Fprintf(s.Out, "region:       %s\n", a.Region)
	fmt.Fprintf(s.Out, "status:       %s\n", a.Status)
	fmt.Fprintf(s.Out, "rdns:         %s\n", a.RDNS)
	fmt.Fprintf(s.Out, "abuse-mailbox: %s\n", a.Abuse)
	if dk := s.W.DeskFor(ip); dk != nil {
		if d := s.W.DeskDevice(dk); d != nil {
			state := "queue open"
			if !s.W.DeskOperational(dk) {
				state = "queue closed"
			}
			fmt.Fprintf(s.Out, "responsible:  %s — report to %s@%s (%s)\n", dk.Org, dk.Mailbox, d.Hostname, state)
		}
	}
	if a.Note != "" {
		fmt.Fprintf(s.Out, "note:         %s\n", a.Note)
	}
	fmt.Fprintf(s.Out, "\n")
	fmt.Fprintf(s.Out, "Registrant contact is withheld. The address identifies a network,\n")
	fmt.Fprintf(s.Out, "not a person: allocation records are not published in this registry.\n")
	if a.Kind == core.KindShared {
		fmt.Fprintf(s.Out, "A subscriber behind carrier-grade NAT has no address of their own to look up.\n")
	}
	return 0
}

func cmdBgp(s *Shell, args []string) int {
	wan := s.W.WAN
	if wan == nil {
		s.errf("bgp: no transit information")
		return 1
	}
	if len(args) > 0 {
		switch args[0] {
		case "summary", "neighbors", "neighbours":
			return bgpSummary(s)
		case "routes", "route", "rib":
			return bgpRoutes(s, args[1:])
		default:
			return bgpOne(s, args[0])
		}
	}
	asns := make([]int, 0, len(wan.ASes))
	for n := range wan.ASes {
		asns = append(asns, n)
	}
	sort.Ints(asns)
	fmt.Fprintf(s.Out, "BGP table view from %s (transit provider)\n\n", s.Dev.Hostname)
	fmt.Fprintf(s.Out, "%-8s %-24s %-12s %-9s %s\n", "ASN", "NAME", "PREFIXES", "STATUS", "UPLINK")
	for _, n := range asns {
		as := wan.ASes[n]
		up := "-"
		if len(as.Upstream) > 0 {
			parts := make([]string, 0, len(as.Upstream))
			for _, u := range as.Upstream {
				parts = append(parts, fmt.Sprintf("AS%d", u))
			}
			up = strings.Join(parts, ",")
		}
		fmt.Fprintf(s.Out, "AS%-7d %-24s %-12s %-9s %s\n",
			as.ASN, trunc(as.Name, 24), fmt.Sprintf("%d", len(as.Prefixes)), as.Status, up)
	}
	return 0
}

func bgpOne(s *Shell, arg string) int {
	wan := s.W.WAN
	var as *core.AS
	if n := atoiSafe(arg); n > 0 {
		as = wan.ASes[n]
	} else {
		for _, a := range wan.ASes {
			if a.Name == arg || a.RDNS == arg {
				as = a
			}
		}
	}
	if as == nil {
		fmt.Fprintf(s.Out, "bgp: AS%s not found in the transit graph\n", arg)
		return 1
	}
	fmt.Fprintf(s.Out, "AS%d  %s (%s)\n", as.ASN, as.Name, as.Org)
	fmt.Fprintf(s.Out, "  status:   %s\n", as.Status)
	fmt.Fprintf(s.Out, "  region:   %s (%s)\n", as.Region, as.Country)
	fmt.Fprintf(s.Out, "  abuse:    %s\n", as.Abuse)
	fmt.Fprintf(s.Out, "  upstream: %s\n", asnList(as.Upstream))
	fmt.Fprintf(s.Out, "  peers:    %s\n", asnList(as.Peers))
	fmt.Fprintf(s.Out, "  announcing:\n")
	for _, p := range as.Prefixes {
		fmt.Fprintf(s.Out, "    %s\n", p)
	}
	return 0
}

// bgpSummary is the operator's looking glass: every session of the transit
// router with its state and reason. Derived live — stopping bird or losing
// an AS flips this output with no other write.
func bgpSummary(s *Shell) int {
	if s.W.WAN == nil {
		s.errf("bgp: no transit information")
		return 1
	}
	sess := core.BGPSessions(s.W)
	fmt.Fprintf(s.Out, "BGP sessions on core-gw (bird)\n")
	fmt.Fprintf(s.Out, "%-9s %-12s %-20s %s\n", "NEIGHBOR", "STATE", "PREFIXES", "WHY")
	for _, b := range sess {
		fmt.Fprintf(s.Out, "AS%-7d %-12s %-20d %s\n", b.PeerASN, b.State, b.Prefixes, b.Why)
	}
	return 0
}

// bgpRoutes prints the RIB: originated prefixes and whether the sessions
// currently carry them. A withdrawn prefix is listed as such — the table
// shows the outage, it does not hide it.
func bgpRoutes(s *Shell, args []string) int {
	if s.W.WAN == nil {
		s.errf("bgp: no transit information")
		return 1
	}
	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}
	type row struct {
		prefix string
		origin int
		up     bool
	}
	var rows []row
	for _, as := range s.W.WAN.ASes {
		for _, p := range as.Prefixes {
			if filter != "" && !strings.Contains(p, filter) {
				continue
			}
			rows = append(rows, row{p, as.ASN, core.BGPSessionUp(s.W, as.ASN)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].prefix < rows[j].prefix })
	fmt.Fprintf(s.Out, "%-20s %-9s %s\n", "PREFIX", "ORIGIN", "STATE")
	for _, r := range rows {
		st := fmt.Sprintf("via AS%d", r.origin)
		if !r.up {
			st = "WITHDRAWN"
		}
		fmt.Fprintf(s.Out, "%-20s AS%-7d %s\n", r.prefix, r.origin, st)
	}
	return 0
}

func asnList(ns []int) string {
	if len(ns) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("AS%d", n))
	}
	return strings.Join(parts, " ")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func atoiSafe(s string) int {
	s = strings.TrimPrefix(strings.ToUpper(s), "AS")
	n := 0
	if s == "" {
		return 0
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// registryReachable performs the one query a whois client really makes: a TCP
// connection to the registry's service port. Returns "" when the registry
// answered, or the reason it did not.
func (s *Shell) registryReachable() string {
	var reg *core.Desk
	for _, dk := range s.W.Desks {
		if dk.Kind == core.DeskRegistry {
			reg = dk
			break
		}
	}
	if reg == nil {
		return "no registry is reachable from this world"
	}
	d := s.W.DeskDevice(reg)
	if d == nil {
		return "the registry has no machine"
	}
	port := 43
	if svc := d.Svc(reg.Portal); svc != nil {
		port = svc.Port
	}
	svc, _, msg := core.Dial(s.Dev, core.WANIPOf(d), port)
	if svc == nil {
		return fmt.Sprintf("cannot reach the registry %s (%s): %s", d.Hostname, core.WANIPOf(d), msg)
	}
	return ""
}
