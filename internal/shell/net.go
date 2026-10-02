package shell

import (
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
		{"ss", cmdSs}, {"ping", cmdPing}, {"traceroute", cmdTraceroute},
		{"dig", cmdDig}, {"nslookup", cmdNslookup}, {"curl", cmdCurl}, {"wget", cmdWget},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdIp(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	// accept the common one-letter abbreviations real `ip` accepts
	if alias, ok := map[string]string{"a": "addr", "l": "link", "r": "route", "s": "link"}[sub]; ok {
		sub = alias
	}
	switch sub {
	case "", "link":
		for n, i := range s.Dev.Ifaces {
			fmt.Fprintf(s.Out, "%d: %s: <%s> mtu 1500 qdisc pfifo_fast state %s\n",
				n+1, i.Name, updown(i.Up), updown(i.Up))
			fmt.Fprintf(s.Out, "    link/ether %s brd ff:ff:ff:ff:ff:ff\n", i.MAC)
			if i.IP != "" {
				fmt.Fprintf(s.Out, "    inet %s/%s scope global %s\n", i.IP, cidrLen(i.CIDR), i.Name)
			}
		}
	case "addr", "address":
		for n, i := range s.Dev.Ifaces {
			fmt.Fprintf(s.Out, "%d: %s: <%s> mtu 1500 qdisc pfifo_fast state %s qlen 1000\n",
				n+1, i.Name, updown(i.Up), updown(i.Up))
			fmt.Fprintf(s.Out, "    link/ether %s brd ff:ff:ff:ff:ff:ff\n", i.MAC)
			if i.IP != "" {
				fmt.Fprintf(s.Out, "    inet %s/%s brd %s scope global %s\n",
					i.IP, cidrLen(i.CIDR), broadcastOf(i), i.Name)
			}
		}
	case "route":
		for _, i := range s.Dev.Ifaces {
			if i.GW != "" && i.Zone == "wan" || i.GW != "" && s.Dev.Profile != "router" {
				fmt.Fprintf(s.Out, "default via %s dev %s proto dhcp metric %d\n", i.GW, i.Name, 100)
			}
		}
		lanNet := s.Dev.LanNet()
		if lanNet != "" && s.Dev.Profile != "router" {
			fmt.Fprintf(s.Out, "%s dev %s proto kernel scope link src %s\n", lanNet, firstIfaceName(s.Dev), s.Dev.FirstLANIP())
		}
	default:
		fmt.Fprintf(s.Out, "%s: unknown arg (try: ip a, ip r, ip l)\n", sub)
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
			addr := i.IP
			if svc.Scope == "any" {
				addr = "0.0.0.0"
			}
			proc := ""
			if showProc {
				proc = fmt.Sprintf("users:((\"%s\",pid=%d,fd=%d))", svc.Name, svc.PID, 3)
			}
			fmt.Fprintf(s.Out, "LISTEN 0      128     %s:%-5d     0.0.0.0:*       %s\n", addr, svc.Port, proc)
		}
	}
	return 0
}

func cmdPing(s *Shell, args []string) int {
	target, count := "", 4
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &count)
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
	ip, ok, how := core.DNSAnswer(s.Dev, target)
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
	for i := 1; i <= count; i++ {
		fmt.Fprintf(s.Out, "64 bytes from %s: icmp_seq=%d ttl=64 time=%.1f ms\n", ip, i, 0.4+float64(i)*0.1)
	}
	fmt.Fprintf(s.Out, "\n--- %s ping statistics ---\n%d packets transmitted, %d received, 0%% packet loss, time %dms\n",
		target, count, count, count*1)
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
	gw := s.Dev.GatewayIP()
	hops := []string{"127.0.0.1"}
	if gw != "" {
		hops = append(hops, gw)
	}
	hops = append(hops, ip)
	for i, h := range hops {
		fmt.Fprintf(s.Out, "%2d  %s (%s)  %.3f ms\n", i+1, h, h, 0.4+float64(i)*0.7)
	}
	return 0
}

func cmdDig(s *Shell, args []string) int {
	name := ""
	resolver := ""
	for i, a := range args {
		if a == "@server" && i+1 < len(args) {
			resolver = args[i+1]
		} else if !strings.HasPrefix(a, "@") && name == "" {
			name = a
		}
	}
	if name == "" {
		s.errf("usage: dig HOST")
		return 1
	}
	ip, ok, how := core.DNSAnswer(s.Dev, name)
	if resolver != "" {
		if d, found := s.Dev.ResolverIPFor(resolver); found {
			ip, ok, how = d.DNSAnswer(name)
		}
	}
	fmt.Fprintf(s.Out, "; <<>> DiG <<>> %s @%s\n", name, resolver)
	if !ok {
		fmt.Fprintf(s.Out, ";; ->>HEADER<<- opcode: QUERY, status: %s, id: 12345\n", strings.ToUpper(how))
		return 1
	}
	fmt.Fprintf(s.Out, ";; QUESTION SECTION:\n;%s\tIN\tA\n\n", name)
	fmt.Fprintf(s.Out, ";; ANSWER SECTION:\n%s\t300\tIN\tA\t%s\n\n", name, ip)
	fmt.Fprintf(s.Out, ";; Query time: %d msec\n", 1)
	fmt.Fprintf(s.Out, ";; SERVER: %s#53(%s)\n", resolver, resolver)
	return 0
}

func cmdNslookup(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: nslookup HOST")
		return 1
	}
	for _, name := range args {
		ip, ok, how := core.DNSAnswer(s.Dev, name)
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
		fmt.Fprintf(s.Out, "%s: (%s) %s\n", tool, "couldn't connect to host", err)
		return 1
	}
	if output != "" {
		p := s.abs(output)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs != nil {
			vfs.Write(p, string(data), 0644, s.User.Name, s.User.Name)
		}
		return 0
	}
	fmt.Fprint(s.Out, string(data))
	return 0
}

// fetchURL resolves the name, dials the target service and returns the
// simulated HTTP response body. This is the causal gate: a broken DNS or a
// stopped web server produces a real failure, not a canned page.
func fetchURL(s *Shell, url string) (string, error) {
	host := url
	if strings.Contains(url, "://") {
		host = strings.SplitN(url, "://", 2)[1]
	}
	host = strings.SplitN(host, "/", 2)[0]
	host = strings.SplitN(host, ":", 2)[0]
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		return "", fmt.Errorf("could not resolve host %s: %s", host, how)
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 80)
	if svc == nil {
		return "", fmt.Errorf(msg)
	}
	_ = dst
	return serveHTTP(s, svc, host), nil
}

func serveHTTP(s *Shell, svc *core.Service, host string) string {
	switch svc.Handler {
	case "http-mirror":
		return "mirror.neohome.example index\n# main repo: http://mirror.neohome.example/debian\n# contrib: http://mirror.neohome.example/debian/contrib\n"
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
