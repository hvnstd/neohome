package shell

import (
	"errors"
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"ip", cmdIp}, {"ifconfig", cmdIfconfig}, {"route", cmdRoute},
		{"ss", cmdSs}, {"ping", cmdPing}, {"ping6", cmdPing6}, {"traceroute", cmdTraceroute},
		{"dig", cmdDig}, {"nslookup", cmdNslookup}, {"curl", cmdCurl}, {"wget", cmdWget},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdIp(s *Shell, args []string) int {
	// Real `ip` takes a family selector (-4/-6), an object, an optional `show`,
	// and an optional device. Parsing only args[0] meant that the most common
	// invocation in the world, `ip -4 addr show eth0`, just errored out.
	var sub, wantDev string
	family := 0 // 0 = both, 4 or 6 when the player asked for one family
	rest := args
	for len(rest) > 0 {
		a := rest[0]
		switch {
		case a == "-4" || a == "-6":
			if a == "-4" {
				family = 4
			} else {
				family = 6
			}
			rest = rest[1:]
			continue
		case a == "-br" || a == "-o" || a == "-brief":
			rest = rest[1:]
			continue
		case a == "show" || a == "list":
			rest = rest[1:]
			continue
		}
		break
	}
	if len(rest) > 0 {
		sub = rest[0]
		rest = rest[1:]
	}
	// accept the common one-letter abbreviations real `ip` accepts
	if alias, ok := map[string]string{"a": "addr", "l": "link", "r": "route"}[sub]; ok {
		sub = alias
	}
	// `show`/`list` may also follow the object, and anything left after it is
	// the device name: `ip -4 addr show eth0`.
	for len(rest) > 0 && (rest[0] == "show" || rest[0] == "list") {
		rest = rest[1:]
	}
	if len(rest) > 0 && rest[0] != "" {
		wantDev = rest[0]
	}

	// a named device that does not exist is a real error
	if wantDev != "" && wantDev != "lo" {
		found := false
		for _, i := range s.Dev.Ifaces {
			if i.Name == wantDev {
				found = true
			}
		}
		if !found {
			s.errf("Device \"%s\" does not exist.", wantDev)
			return 1
		}
	}
	show := func(i *core.Iface) bool { return wantDev == "" || i.Name == wantDev }
	// The loopback interface is real on every machine, and `ip addr` shows it
	// first — including its IPv6 address, which is the one v6 address every
	// device in this world is guaranteed to have.
	lo := &core.Iface{Name: "lo", IP: "127.0.0.1", CIDR: "127.0.0.0/8", Zone: "lo", Up: true,
		IP6: []string{"::1/128"}}

	switch sub {
	case "", "link":
		for n, i := range append([]*core.Iface{lo}, s.Dev.Ifaces...) {
			if !show(i) {
				continue
			}
			fmt.Fprintf(s.Out, "%d: %s: <%s> mtu 1500 qdisc pfifo_fast state %s\n",
				n+1, i.Name, updown(i.Up), updown(i.Up))
			fmt.Fprintf(s.Out, "    link/ether %s brd ff:ff:ff:ff:ff:ff\n", i.MAC)
			if i.VLAN != 0 {
				fmt.Fprintf(s.Out, "    vlan %d\n", i.VLAN)
			}
		}
	case "addr", "address":
		for n, i := range append([]*core.Iface{lo}, s.Dev.Ifaces...) {
			if !show(i) {
				continue
			}
			fmt.Fprintf(s.Out, "%d: %s: <%s> mtu 1500 qdisc pfifo_fast state %s qlen 1000\n",
				n+1, i.Name, updown(i.Up), updown(i.Up))
			fmt.Fprintf(s.Out, "    link/ether %s brd ff:ff:ff:ff:ff:ff\n", i.MAC)
			if i.VLAN != 0 {
				fmt.Fprintf(s.Out, "    vlan %d\n", i.VLAN)
			}
			isLo := i.Zone == "lo"
			if i.IP != "" && family != 6 {
				// a DHCP address is dynamic and carries a lease: §13's
				// "dynamic IP" is this line, not a flag somewhere else
				lease := ""
				if i.Mode == "dhcp" {
					lease = " dynamic valid_lft 85000sec preferred_lft 85000sec"
				}
				if isLo {
					fmt.Fprintf(s.Out, "    inet %s/%s scope host %s\n", i.IP, cidrLen(i.CIDR), i.Name)
				} else {
					fmt.Fprintf(s.Out, "    inet %s/%s brd %s scope global%s %s\n",
						i.IP, cidrLen(i.CIDR), broadcastOf(i), lease, i.Name)
				}
			}
			// §13: a carrier-grade-NAT address is on the interface exactly like
			// any other — that is what makes it tempting to believe it is
			// yours. It is printed here, and `whois` of it says whose it is.
			if i.SharedIP != "" && family != 6 && !isLo {
				fmt.Fprintf(s.Out, "    inet %s/32 brd 100.127.255.255 scope global dynamic %s\n",
					i.SharedIP, i.Name)
			}
			// §13's "virtual IP": a reserved address lent to this node.
			for _, ip := range i.Extra {
				if family == 6 || isLo {
					continue
				}
				fmt.Fprintf(s.Out, "    inet %s/32 brd %s scope global secondary %s\n",
					ip, broadcastOf(i), i.Name)
			}
			// §13's kinds speak for themselves: a link-local says "scope link",
			// a ULA and a global say "scope global", and a link-local is always
			// there even on a machine with nothing configured.
			if family != 4 {
				for _, a := range i.IP6 {
					scope := "global"
					switch {
					case strings.HasPrefix(a, "fe80:"):
						scope = "link"
					case strings.HasPrefix(a, "::1"):
						scope = "host"
					}
					fmt.Fprintf(s.Out, "    inet6 %s scope %s %s\n", a, scope, i.Name)
				}
			}
		}
	case "route":
		if family != 6 {
			for _, i := range s.Dev.Ifaces {
				if i.GW != "" && i.Zone == "wan" || i.GW != "" && s.Dev.Profile != "router" {
					fmt.Fprintf(s.Out, "default via %s dev %s proto dhcp metric %d\n", i.GW, i.Name, 100)
				}
			}
			lanNet := s.Dev.LanNet()
			if lanNet != "" && s.Dev.Profile != "router" {
				fmt.Fprintf(s.Out, "%s dev %s proto kernel scope link src %s\n", lanNet, firstIfaceName(s.Dev), s.Dev.FirstLANIP())
			}
		}
		if family != 4 {
			for _, i := range s.Dev.Ifaces {
				if i.GW6 != "" && i.Zone == "wan" {
					fmt.Fprintf(s.Out, "default via %s dev %s proto ra metric 100\n", i.GW6, i.Name)
				}
				for _, a := range i.IP6 {
					if core.IsV6CIDR(a) && !strings.HasPrefix(a, "fe80:") {
						net := core.V6NetworkOf(a)
						if net != "" {
							fmt.Fprintf(s.Out, "%s dev %s proto kernel metric 256 pref medium\n", net, i.Name)
						}
					}
				}
			}
		}
	default:
		fmt.Fprintf(s.Out, "%s: unknown arg (try: ip a, ip r, ip l)\n", sub)
		return 1
	}
	return 0
}

func updown(up bool) string {
	if up {
		return "UP"
	}
	return "DOWN"
}

func firstIfaceName(d *core.Device) string {
	if len(d.Ifaces) > 0 {
		return d.Ifaces[0].Name
	}
	return "eth0"
}

// broadcastOf derives the broadcast address from the interface's own CIDR —
// no more hardcoded 10.77.1.255 on every device in the world.
func broadcastOf(i *core.Iface) string {
	parts := strings.Split(i.IP, ".")
	if len(parts) != 4 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + ".255"
}

func cmdIfconfig(s *Shell, args []string) int {
	for _, i := range s.Dev.Ifaces {
		fmt.Fprintf(s.Out, "%s: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500\n", i.Name)
		if i.IP != "" {
			fmt.Fprintf(s.Out, "        inet %s  netmask 255.255.255.0  broadcast 10.77.1.255\n", i.IP)
			fmt.Fprintf(s.Out, "        ether %s  txqueuelen 1000  (Ethernet)\n", i.MAC)
		}
	}
	return 0
}

func cmdRoute(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "Kernel IP routing table\n")
	fmt.Fprintf(s.Out, "Destination     Gateway         Genmask         Flags Metric Ref    Use Iface\n")
	for _, i := range s.Dev.Ifaces {
		if i.CIDR != "" {
			fmt.Fprintf(s.Out, "%s %s %s U 202 0 0 %s\n", i.CIDR, "0.0.0.0", "255.255.255.0", i.Name)
		}
		if i.GW != "" {
			fmt.Fprintf(s.Out, "0.0.0.0         %s 0.0.0.0         UG    0 0 0 %s\n", i.GW, i.Name)
		}
	}
	return 0
}

func cmdSs(s *Shell, args []string) int {
	showProc := false
	for _, a := range args {
		if strings.Contains(a, "p") {
			showProc = true
		}
	}
	fmt.Fprintf(s.Out, "State  Recv-Q Send-Q  Local Address:Port   Peer Address:Port  Process\n")
	// only real listening sockets: a port-0 service (syslogd's socket) is not a
	// TCP listener and must never be printed as one
	for _, i := range s.Dev.Ifaces {
		if i.IP == "" {
			continue
		}
		for _, svc := range s.Dev.Services {
			if svc.Port <= 0 || svc.State != "running" {
				continue
			}
			if svc.Proto != "tcp" && !strings.Contains(svc.Proto, "tcp") {
				continue
			}
			// A socket bound to the LAN must not be shown as listening on the
			// WAN address. Dial already refuses a LAN-only service from the WAN,
			// so printing one on 198.51.100.1 told an attacker that the router's
			// ssh and telnet were exposed to the internet when they were not.
			if svc.Scope == "lan" && i.Zone != "lan" {
				continue
			}
			if svc.Scope == "wan" && i.Zone != "wan" {
				continue
			}
			addr := i.IP
			if svc.Scope == "any" {
				addr = "0.0.0.0"
			}
			proc := ""
			if showProc {
				proc = fmt.Sprintf("users:((\"%s\",pid=%d,fd=%d))", svc.Name, svc.PID, 3)
			}
			fmt.Fprintf(s.Out, "LISTEN 0      128     %s:%-5d     0.0.0.0:*       %s\n", addr, svc.Port, proc)
			// and the v6 side of the same listener: a program that binds all
			// addresses binds both families, and hiding that would make the
			// world's v6 stack look smaller than it is
			v6addr := ""
			switch {
			case svc.Scope == "any":
				v6addr = "[::]"
			default:
				if g := i.IP6; len(g) > 0 {
					host, _, _ := strings.Cut(g[0], "/")
					v6addr = "[" + host + "]"
				}
			}
			if v6addr != "" {
				fmt.Fprintf(s.Out, "LISTEN 0      128     %-16s   [::]:*          %s\n", v6addr+":"+fmt.Sprint(svc.Port), proc)
			}
		}
	}
	return 0
}

func cmdPing(s *Shell, args []string) int { return pingTo(s, args, 4) }

// ping6 pings over IPv6: same code path, the other family. §13's v6 addresses
// are reachable (or not) through the same firewall and routing the v4 ones go
// through, so this is a real test of the network and not a second simulator.
func cmdPing6(s *Shell, args []string) int { return pingTo(s, args, 6) }

func pingTo(s *Shell, args []string, family int) int {
	target, count := "", 4
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &count)
		case (a == "-4" || a == "-6") && family == 4:
			if a == "-6" {
				family = 6
			}
		case a == "-W" || a == "-w" || a == "-i" || a == "-s":
			i++
		case strings.HasPrefix(a, "-"):
			// ignore other flags rather than treating them as hostnames
		default:
			if target == "" {
				target = a
			}
		}
	}
	if target == "" {
		s.errf("usage: ping [-c count] HOST")
		return 1
	}
	ip, ok, how := core.DNSAnswerFamily(s.Dev, target, family)
	if !ok {
		fmt.Fprintf(s.Out, "ping: %s: Name or service not known (%s)\n", target, how)
		return 1
	}
	fmt.Fprintf(s.Out, "PING %s (%s) 56(84) bytes of data.\n", target, ip)
	// ICMP is gated by routing/NAT, not by whether a TCP port is listening
	if msg, ok := core.Reach(s.Dev, ip); !ok {
		hop := s.Dev.GatewayIP()
		if hop == "" {
			hop = "local"
		}
		fmt.Fprintf(s.Out, "From %s icmp_seq=1 %s\n", hop, msg)
		fmt.Fprintf(s.Out, "\n--- %s ping statistics ---\n%d packets transmitted, 0 received, 100%% packet loss\n", target, count)
		return 1
	}
	// the reply time is the world's own distance to the host, so pinging a
	// node in another region is visibly slower than pinging the NAS
	rtt := s.W.RTTms(s.Dev, ip)
	for i := 1; i <= count; i++ {
		jitter := float64((i*7)%5) * 0.05
		fmt.Fprintf(s.Out, "64 bytes from %s: icmp_seq=%d ttl=64 time=%.1f ms\n", ip, i, rtt+jitter)
	}
	fmt.Fprintf(s.Out, "\n--- %s ping statistics ---\n%d packets transmitted, %d received, 0%% packet loss, time %.0fms\n",
		target, count, count, rtt*float64(count))
	return 0
}

func cmdTraceroute(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: traceroute HOST")
		return 1
	}
	ip, ok, _ := core.DNSAnswer(s.Dev, args[0])
	if !ok {
		fmt.Fprintf(s.Out, "traceroute to %s (%s), 30 hops max\n", args[0], "unknown")
		return 1
	}
	// The path is the world's real device graph (local gateway -> the AS that
	// announces us -> transit -> the AS that announces them -> host), not a
	// synthesised hop list. A destination that does not exist dies at the edge.
	hops, _ := s.W.Trace(s.Dev, ip)
	if len(hops) == 0 {
		fmt.Fprintf(s.Out, "traceroute to %s (%s), 30 hops max\n", args[0], ip)
		return 1
	}
	fmt.Fprintf(s.Out, "traceroute to %s (%s), 30 hops max\n", args[0], ip)
	for i, h := range hops {
		as := ""
		if h.ASN != 0 {
			as = fmt.Sprintf(" [AS%d]", h.ASN)
		}
		if h.Device != "" {
			fmt.Fprintf(s.Out, "%2d  %-15s (%s)%s  %.3f ms\n", i+1, h.Device, h.IP, as, h.Latency)
			continue
		}
		fmt.Fprintf(s.Out, "%2d  %-15s (%s)%s  %.3f ms\n", i+1, "*", h.IP, as, h.Latency)
	}
	return 0
}

func cmdDig(s *Shell, args []string) int {
	name := ""
	resolver := ""
	family := 4
	asked := "" // the type the player requested, for the header
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "@server" && i+1 < len(args):
			resolver = args[i+1]
			i++
		case (a == "-t" || a == "--type" || a == "-query") && i+1 < len(args):
			asked = strings.ToUpper(args[i+1])
			i++
		case strings.HasPrefix(a, "-t") && len(a) > 2:
			asked = strings.ToUpper(a[2:])
		case strings.HasPrefix(a, "--type="):
			asked = strings.ToUpper(strings.TrimPrefix(a, "--type="))
		case strings.HasPrefix(a, "-query="):
			asked = strings.ToUpper(strings.TrimPrefix(a, "-query="))
		case a == "-x" && i+1 < len(args):
			// reverse lookup: dig turns the address into an arpa name itself
			rev := args[i+1]
			i++
			if arpa := core.ArpaName(rev); arpa != "" {
				name = arpa
				asked = "PTR"
			}
		case a == "-6":
			family = 6
		case a == "-4":
			family = 4
		case strings.HasPrefix(a, "@"):
			// an option this world does not model
		case strings.Contains(a, "=") && strings.HasPrefix(a, "-"):
			// other -key=value options are not this world's business
		case name == "":
			name = a
		}
	}
	switch asked {
	case "AAAA", "AAAA6":
		family = 6
	case "A":
		family = 4
	case "PTR":
		// a reverse lookup rides the same resolver chain as any other query
		family = 4
	case "", "ANY":
		// the default: whatever the name has, A first — dig's own default is A
	default:
		fmt.Fprintf(s.Out, ";; ->>HEADER<<- opcode: QUERY, status: NOTIMP, id: 12345\n")
		fmt.Fprintf(s.Out, ";; question section:\n;%s\tIN\t%s\n", name, asked)
		fmt.Fprintf(s.Out, "\n;; no records of type %s exist in this world\n", asked)
		return 1
	}
	if name == "" {
		s.errf("usage: dig [-t TYPE] [@server] HOST")
		return 1
	}
	ip, ok, how := core.DNSAnswerFamily(s.Dev, name, family)
	if core.IsArpaName(name) {
		// the answer is a name, not an address: ask for it as a PTR
		asked = "PTR"
	}
	if resolver != "" {
		if d, found := s.Dev.ResolverIPFor(resolver); found {
			ip, ok, how = core.DNSAnswerFamily(d, name, family)
		}
	}
	typ := "A"
	if family == 6 {
		typ = "AAAA"
	}
	if core.IsArpaName(name) {
		typ = "PTR"
	}
	fmt.Fprintf(s.Out, "; <<>> DiG <<>> %s @%s\n", name, resolver)
	if !ok {
		fmt.Fprintf(s.Out, ";; ->>HEADER<<- opcode: QUERY, status: %s, id: 12345\n", strings.ToUpper(how))
		fmt.Fprintf(s.Out, ";; QUESTION SECTION:\n;%s\tIN\t%s\n\n", name, typ)
		fmt.Fprintf(s.Out, ";; ANSWER SECTION:\n")
		return 1
	}
	fmt.Fprintf(s.Out, ";; QUESTION SECTION:\n;%s\tIN\t%s\n\n", name, typ)
	rtype := core.RecordType(ip)
	if core.IsArpaName(name) {
		rtype = "PTR"
	}
	fmt.Fprintf(s.Out, ";; ANSWER SECTION:\n%s\t300\tIN\t%s\t%s\n\n", name, rtype, ip)
	fmt.Fprintf(s.Out, ";; Query time: %d msec\n", 1)
	fmt.Fprintf(s.Out, ";; SERVER: %s#53(%s)\n", resolver, resolver)
	return 0
}

func cmdNslookup(s *Shell, args []string) int {
	family := 4
	var names []string
	for _, a := range args {
		switch {
		case a == "-6" || a == "-type=AAAA" || a == "-query=AAAA" || strings.EqualFold(a, "-type=aaaa"):
			family = 6
		case a == "-4" || a == "-type=A" || a == "-query=A":
			family = 4
		case strings.HasPrefix(a, "-"):
		default:
			names = append(names, a)
		}
	}
	if len(names) == 0 {
		s.errf("usage: nslookup [-type=A|AAAA] HOST")
		return 1
	}
	for _, name := range names {
		ip, ok, how := core.DNSAnswerFamily(s.Dev, name, family)
		fmt.Fprintf(s.Out, "Server:\t%s\n", s.Dev.ResolverIP())
		fmt.Fprintf(s.Out, "Address:\t%s#53\n\n", s.Dev.ResolverIP())
		if !ok {
			fmt.Fprintf(s.Out, "Server: %s\nAddress: %s\n\n", s.Dev.ResolverIP(), s.Dev.ResolverIP())
			fmt.Fprintf(s.Out, "** can't find %s: %s\n", name, how)
			return 1
		}
		fmt.Fprintf(s.Out, "Name:\t%s\nAddress: %s\n", name, ip)
	}
	return 0
}

func cmdCurl(s *Shell, args []string) int {
	return httpFetch(s, args, "curl")
}

func cmdWget(s *Shell, args []string) int {
	return httpFetch(s, args, "wget")
}

func httpFetch(s *Shell, args []string, tool string) int {
	url := ""
	output := ""
	wantOutputNext := false
	for _, a := range args {
		// real curl/wget accept both the attached (-oFILE / -OFILE) and the
		// separated (-o FILE / -O FILE) forms
		if wantOutputNext {
			output = a
			wantOutputNext = false
			continue
		}
		if a == "-O" || a == "-o" || a == "--output" {
			wantOutputNext = true
			continue
		}
		if strings.HasPrefix(a, "-o") && len(a) > 2 {
			output = a[2:]
		} else if strings.HasPrefix(a, "-O") && len(a) > 2 {
			output = a[2:]
		} else if strings.HasPrefix(a, "--output=") {
			output = strings.TrimPrefix(a, "--output=")
		} else if !strings.HasPrefix(a, "-") {
			url = a
		}
	}
	if url == "" {
		s.errf("usage: %s URL", tool)
		return 1
	}
	data, err := fetchURL(s, url)
	if err != nil {
		var tlsErr *core.TLSError
		if errors.As(err, &tlsErr) {
			fmt.Fprintf(s.Out, "%s: (%d) %s\n", tool, tlsErr.Code, tlsErr.Msg)
		} else {
			fmt.Fprintf(s.Out, "%s: (%s) %s\n", tool, "couldn't connect to host", err)
		}
		return 1
	}
	if output != "" {
		p := s.abs(output)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("%s: %s: Stale file handle", tool, output)
			return 1
		}
		if err := s.Dev.WriteGuest(p, []byte(data), s.User); err != nil {
			s.errf("%s: %s: %v", tool, output, err)
			return 1
		}
		return 0
	}
	fmt.Fprint(s.Out, string(data))
	return 0
}

// fetchURL resolves the name, dials the target service and returns the
// simulated HTTP response body. This is the causal gate: a broken DNS or a
// stopped web server produces a real failure, not a canned page. https is
// judged like a real client: the handshake fails closed on a missing,
// untrusted, mismatched or stale certificate.
func fetchURL(s *Shell, url string) (string, error) {
	scheme := "http"
	rest := url
	if i := strings.Index(url, "://"); i >= 0 {
		scheme = url[:i]
		rest = url[i+3:]
	}
	host := strings.SplitN(rest, "/", 2)[0]
	host = strings.SplitN(host, ":", 2)[0]
	urlPath := "/"
	if i := strings.Index(rest, "/"); i >= 0 {
		urlPath = rest[i:]
	}
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		return "", fmt.Errorf("could not resolve host %s: %s", host, how)
	}
	port := 80
	if scheme == "https" {
		port = 443
	}
	svc, dst, msg := core.Dial(s.Dev, ip, port)
	if svc == nil {
		return "", fmt.Errorf(msg)
	}
	if scheme == "https" {
		if _, _, err := s.W.Handshake(s.Dev, host, svc); err != nil {
			return "", err
		}
	}
	_ = dst
	return serveHTTP(s, svc, host, ip, urlPath), nil
}

func serveHTTP(s *Shell, svc *core.Service, host, ip, urlPath string) string {
	switch svc.Handler {
	case "http-mirror", "http-archive", "http-repo":
		// repositories are served from the host's real files: the same bytes
		// apt verifies, the mirror syncs and a player can read or host
		if dst, ok := s.W.Devices[s.W.IPMap[ip]]; ok {
			if body, served := core.ServeFile(dst, urlPath); served {
				return string(body)
			}
		}
		return "404 Not Found\n"
	case "http-vps":
		return "nova panel API — POST /v1/instances\n"
	case "http-bank":
		return "firstneohome bank API — balance inquiry requires auth\n"
	case "http-jobs":
		return "jobs.hiring.example — open positions\n"
	case "http-nas":
		return "<html><body>NAS admin console (login required)</body></html>\n"
	case "http-user":
		return "<html><body><h1>Welcome to nginx!</h1></body></html>\n"
	case "smtpd":
		return "220 neohome ESMTP smtpd ready\n"
	// §34's organisations: each portal says what it is for and how to file
	// with it, because a real abuse desk publishes exactly that.
	case "http-abuse":
		return "NovaPanel abuse desk\n\nFile complaints from the machine that saw the traffic:\n  abuse report <ip>\nQuote the ticket number when you reply by mail: abuse@abuse.novapanel.example\n"
	case "http-noc":
		return "NetCrest NOC — access network operations\n\nSubscriber records are released on lawful request only.\nOperational reports: noc@noc.netcrest.example\n"
	case "http-soc":
		return "Meridian Systems Security Team\n\nReports about Meridian address space: soc@soc.meridian.example\nOffice network incidents are handled internally.\n"
	case "http-dc":
		return "NovaPanel DC1 console\n\nRack, power and port state. Suspensions are executed here.\n"
	case "http-le":
		return "Cybercrime National Unit — case intake\n\nProviders and ISPs file here with a case reference.\nSubscriber data is requested under lawful process only.\n"
	case "whois-registry":
		return "NeoCore Registry Services — allocation records\n"
	}
	return svc.Banner + "\n"
}

func cidrLen(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return "32"
}
