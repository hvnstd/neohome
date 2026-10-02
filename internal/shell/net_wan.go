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
	if !strings.Contains(ip, ".") {
		// a hostname: resolve it the way a real whois client does
		if r, ok, _ := core.DNSAnswer(s.Dev, ip); ok {
			ip = r
		} else {
			fmt.Fprintf(s.Out, "whois: no match for \"%s\"\n", target)
			return 1
		}
	}

	a, ok := s.W.Lookup(ip)
	if !ok {
		if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") ||
			strings.HasPrefix(ip, "172.16.") || strings.HasPrefix(ip, "127.") {
			fmt.Fprintf(s.Out, "whois: %s\n", ip)
			fmt.Fprintf(s.Out, "No match for \"%s\".\n", ip)
			fmt.Fprintf(s.Out, "This is a private address; no registry holds a record of it.\n")
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
	fmt.Fprintf(s.Out, "\n")
	fmt.Fprintf(s.Out, "Registrant contact is withheld. The address identifies a network,\n")
	fmt.Fprintf(s.Out, "not a person: allocation records are not published in this registry.\n")
	return 0
}

func cmdBgp(s *Shell, args []string) int {
	wan := s.W.WAN
	if wan == nil {
		s.errf("bgp: no transit information")
		return 1
	}
	if len(args) > 0 {
		return bgpOne(s, args[0])
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
