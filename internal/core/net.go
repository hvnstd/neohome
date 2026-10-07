package core

import (
	"fmt"
	"strings"
)

// ---------- virtual network ----------

// publicBlocks is the world's public address space, one /24 per autonomous
// system. Real allocation is per-prefix, and a prefix has exactly one holder —
// that is what makes `whois` attribution truthful instead of a guess. The
// ranges are RFC 5737 / RFC 2544 documentation space, so a game world can
// never collide with a real host.
var publicBlocks = []struct {
	base string
	asn  int
}{
	{"203.0.113.", asCore},      // NeoCore Transit — infra / the world's own nets
	{"198.51.100.", asNetCrest}, // NetCrest ISP — household WAN space
	{"192.0.2.", asNova},        // NovaPanel — VPS customers
	{"198.18.0.", asPeer},       // Meridian — the peer network
	{"198.19.0.", asNovaUS},     // NovaPanel us-east
	{"198.19.1.", asNovaAP},     // NovaPanel ap-northeast
}

// asnForProfile picks which AS's address space a new device is numbered from.
func asnForProfile(profile string) int {
	switch profile {
	case "vps":
		return asNova
	case "router", "pc", "nas":
		return asNetCrest
	case "peer":
		return asPeer
	}
	return asCore
}

// allocPublic hands out a public IP from the address space of the AS that
// operates the given profile. The next free host in that /24 is found by
// looking at what the world already has, so allocation works both before and
// after the WAN is seeded.
func (w *World) allocPublic() string {
	return w.allocPublicFor("")
}

// blockBase is the /24 base a given AS numbers its hosts from.
func blockBase(asn int) string {
	for _, b := range publicBlocks {
		if b.asn == asn {
			return b.base
		}
	}
	return publicBlocks[0].base
}

// allocPublicFor allocates from the AS that operates a device of this profile.
func (w *World) allocPublicFor(profile string) string {
	return w.allocInAS(asnForProfile(profile))
}

// allocInAS allocates the next free address in an AS's own block. Every device
// in the world is checked, so two regions can never hand out one address.
func (w *World) allocInAS(want int) string {
	block := publicBlocks[0]
	for _, b := range publicBlocks {
		if b.asn == want {
			block = b
			break
		}
	}
	// every address already in use, anywhere in the world
	used := map[string]bool{}
	for _, d := range w.Devices {
		for _, i := range d.Ifaces {
			if i.IP != "" {
				used[i.IP] = true
			}
			for _, ip := range i.Extra {
				used[ip] = true
			}
		}
	}
	for n := 1; n <= 254; n++ {
		cand := block.base + itoa(n)
		if !used[cand] {
			return cand
		}
	}
	// that block is full: fall through to the next one rather than hand out a
	// duplicate. A full AS is a real condition, and the address still works.
	for _, b := range publicBlocks {
		for n := 1; n <= 254; n++ {
			cand := b.base + itoa(n)
			if !used[cand] {
				return cand
			}
		}
	}
	return block.base + "255"
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func lanNetOf(d *Device) string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.CIDR != "" {
			return i.CIDR
		}
	}
	return ""
}

func wanIP(d *Device) string {
	for _, i := range d.Ifaces {
		if i.Zone == "wan" && i.IP != "" {
			return i.IP
		}
	}
	return ""
}

// Compute a prefix-aware network match for /8../32.
func inNet(ip, net string) bool {
	parts := strings.Split(net, "/")
	if len(parts) != 2 {
		return false
	}
	o := strings.Split(parts[0], ".")
	if len(o) != 4 {
		return false
	}
	limit := 3
	switch parts[1] {
	case "8":
		limit = 1
	case "16":
		limit = 2
	case "24":
		limit = 3
	default:
		limit = 3
	}
	pre := strings.Join(o[:limit], ".")
	return strings.HasPrefix(ip, pre+".")
}

func isPrivate(ip string) bool {
	return strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") ||
		strings.HasPrefix(ip, "172.16.") || strings.HasPrefix(ip, "127.")
}

func isIPv4(s string) bool {
	p := strings.Split(s, ".")
	if len(p) != 4 {
		return false
	}
	for _, x := range p {
		if len(x) == 0 || len(x) > 3 {
			return false
		}
		for _, c := range x {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// GatewayIP returns src's default gateway, or "" if the device has no route
// to anything beyond its own subnets (broken DHCP / link down).
func (d *Device) GatewayIP() string {
	for _, i := range d.Ifaces {
		if !i.Up || i.GW == "" {
			continue
		}
		if i.Mode == "dhcp" {
			// A DHCP address works while its lease is valid. A LAN client's
			// lease lives on the router (dnsmasq must be running); a household
			// router's own WAN lease is written by its udhcpc client, and that
			// file is the evidence §13's "dynamic IP" is built on.
			if leaseValid(d, i) {
				return i.GW
			}
			continue
		}
		return i.GW
	}
	return ""
}

// routerFor finds the LAN router serving this device (by shared lan CIDR).
func (w *World) routerFor(d *Device) *Device {
	net := ""
	for _, i := range d.Ifaces {
		if i.CIDR == "" {
			// a WAN lease has no LAN prefix: a household router's own WAN is
			// DHCP too, and it must not be mistaken for the LAN it serves
			continue
		}
		if i.Zone == "lan" || i.Mode == "dhcp" {
			net = i.CIDR
		}
	}
	if net == "" {
		return nil
	}
	for _, id := range w.Order {
		dev := w.Devices[id]
		if dev == d {
			// a router is not its own upstream: its LAN is the one it serves
			continue
		}
		if dev.Profile == "router" && lanNetOf(dev) == net {
			return dev
		}
	}
	return nil
}

// DNSAnswer resolves name from the perspective of device d, honoring the
// real resolver chain: d's resolver → router forwarder → authoritative ns.
// Returns (ip, ok, how) where how is diagnostics text for dig.
func DNSAnswer(d *Device, name string) (string, bool, string) {
	return DNSAnswerFamily(d, name, 4)
}

// DNSAnswerFamily is the same resolution with §13's families honoured: family 6
// asks for AAAA (and answers a v6 literal), family 4 asks for A. There is no
// "happy eyeballs" client here — the world's own tools pick a family
// explicitly, the way `ping -6` and `dig -t AAAA` do.
func DNSAnswerFamily(d *Device, name string, family int) (string, bool, string) {
	if IsV6(name) {
		if family == 6 {
			return name, true, "inline address"
		}
		return "", false, "NXDOMAIN (no A record for an IPv6 literal)"
	}
	if isIPv4(name) {
		if family == 6 {
			return "", false, "NXDOMAIN (no AAAA record for an IPv4 literal)"
		}
		return name, true, "inline address"
	}
	if name == d.Hostname || name == d.Hostname+".lan" || name == d.Hostname+".local" {
		return "127.0.0.1", true, "hosts-file"
	}
	// per-device /etc/hosts
	if data, ok := d.FS.Read("/etc/hosts"); ok {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				for _, alias := range f[1:] {
					if alias == name && familyOfRecord(f[0]) == family {
						return f[0], true, "hosts-file"
					}
				}
			}
		}
	}
	resolver := d.ResolverIP()
	if resolver == "" {
		return "", false, "no resolver configured (resolv.conf empty and no DHCP)"
	}
	return answerVia(d.W, d, resolver, name, family, 0)
}

// familyOfRecord classifies a DNS record's address: 6 for AAAA, 4 for A.
func familyOfRecord(ip string) int {
	if IsV6(ip) {
		return 6
	}
	return 4
}

// answerVia walks one resolver hop; depth guards loops. A forwarder with a
// healthy resolv-file recurses to its own upstream; an authoritative server
// answers from the zone.
func answerVia(w *World, ask *Device, resolverIP, name string, family, depth int) (string, bool, string) {
	if depth > 4 {
		return "", false, "SERVFAIL (loop detected)"
	}
	rdID, ok := w.IPMap[resolverIP]
	if !ok {
		return "", false, fmt.Sprintf("resolver %s unreachable", resolverIP)
	}
	rd := w.Devices[rdID]
	// dnsmasq serves its own /etc/hosts before recursing — that is why typing
	// `nas` at home works even when the upstream is broken.
	if data, ok := rd.FS.Read("/etc/hosts"); ok {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				for _, alias := range f[1:] {
					if strings.EqualFold(alias, name) && familyOfRecord(f[0]) == family {
						return f[0], true, fmt.Sprintf("local (dnsmasq on %s)", rd.Hostname)
					}
				}
			}
		}
	}
	if rd.hasNoUpstream() {
		return "", false, fmt.Sprintf("SERVFAIL (%s on %s has no working upstream)", firstSvc(rd, "dnsmasq", "nsd"), rd.Hostname)
	}
	if svc := rd.Svc("nsd"); svc != nil {
		// a reverse lookup is answered by whoever announces the block (§12:
		// every node has rDNS), not by the forward zone
		if IsArpaName(name) {
			return ptrAnswer(w, name)
		}
		// authoritative: answer from zone or NXDOMAIN — and only with a record
		// of the family that was asked for, so a v4-only name really has no AAAA
		for _, r := range w.Records {
			if strings.EqualFold(r.Name, name) && familyOfRecord(r.IP) == family {
				return r.IP, true, fmt.Sprintf("authoritative via %s", resolverIP)
			}
		}
		return "", false, "NXDOMAIN"
	}
	// forwarder: consult its upstream list. A definitive negative answer from
	// the authoritative server (NXDOMAIN) is an answer, not a failure: a
	// forwarder that swallowed it and reported SERVFAIL would turn "this name
	// does not exist" into "DNS is broken", which is a different diagnosis.
	negative := ""
	for _, up := range rd.upstreamServers() {
		if ip, ok2, how := answerVia(w, rd, up, name, family, depth+1); ok2 {
			return ip, true, how + " (forwarded by " + rd.Hostname + ")"
		} else if strings.HasPrefix(how, "NXDOMAIN") {
			negative = how + " (forwarded by " + rd.Hostname + ")"
		}
	}
	if negative != "" {
		return "", false, negative
	}
	return "", false, fmt.Sprintf("SERVFAIL (%s could not reach upstream)", rd.Hostname)
}

// ResolverIP: what does this device ask to resolve names?
func (d *Device) ResolverIP() string {
	// explicit /etc/resolv.conf wins
	if data, ok := d.FS.Read("/etc/resolv.conf"); ok {
		for _, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "nameserver" {
				return f[1]
			}
		}
		// A box running its own resolver asks itself: dnsmasq on the router
		// is what a real /etc/resolv.conf points at. The query still goes
		// through the resolver chain, so a router with a broken upstream
		// fails its own lookups exactly like its clients do.
		if svc := d.Svc(firstSvc(d, "dnsmasq", "nsd")); svc != nil && svc.State == "running" {
			if ip := d.resolverSelfIP(); ip != "" {
				return ip
			}
		}
		// file exists but has no nameserver line → that IS the config: broken
		return ""
	}
	// fall back to DHCP-assigned dns
	for _, i := range d.Ifaces {
		if i.Mode == "dhcp" && i.Zone == "lan" {
			if r := d.W.routerFor(d); r != nil && r.Svc("dnsmasq") != nil && r.Svc("dnsmasq").State == "running" {
				for _, x := range r.Ifaces {
					if x.Zone == "lan" {
						return x.IP
					}
				}
			}
		}
		if i.GW != "" && d.Profile == "router" {
			return "8.8.8.8" // routers hardwire upstream by default
		}
	}
	return ""
}

// resolverSelfIP is the address this device's own resolver listens on: the LAN
// address for a router, otherwise any address of ours that the resolver maps
// back to this device.
func (d *Device) resolverSelfIP() string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.IP != "" {
			return i.IP
		}
	}
	for _, i := range d.Ifaces {
		if i.IP != "" && d.W != nil && d.W.IPMap[i.IP] == d.ID {
			return i.IP
		}
	}
	return ""
}

// ---------- reachability ----------

// Reach answers the routing question without naming a service: can src send
// packets to dstIP at all? This is what ping/traceroute consume — ICMP is not
// TCP, so it must be gated by routing and NAT, not by "is there a listener".
func Reach(src *Device, dstIP string) (string, bool) {
	if dstIP == "127.0.0.1" {
		return "loopback", true
	}
	if !isIPv4(dstIP) && !IsV6(dstIP) {
		return "unknown host", false
	}
	if IsV6(dstIP) && !src.ifaceHasV6() {
		return "Network is unreachable (no IPv6 address)", false
	}
	// A machine with no power has no link. This is why an outage really breaks
	// the network instead of merely being reported.
	if !src.Powered() {
		return "Network is unreachable (no power)", false
	}
	// §15: and a machine with no *cable* has no link either. The switch in
	// between is a real device with real ports, so pulling one takes the
	// devices behind it offline while leaving their state intact. Traffic that
	// stays inside the switch's island does not need the uplink — only traffic
	// that has to cross the router does.
	toLAN := src.onLink(dstIP)
	if up, why := src.W.linkUpVia(src, !toLAN); !up {
		return "Network is unreachable (link down: " + why + ")", false
	}
	dstID, ok := src.W.IPMap[dstIP]
	if !ok {
		return "No route to host", false
	}
	dst := src.W.Devices[dstID]
	if !dst.Powered() {
		return "Destination Host Unreachable (host is down)", false
	}
	if up, why := src.W.linkUpVia(dst, !toLAN); !up {
		return "Destination Host Unreachable (link down: " + why + ")", false
	}
	// BGP RIB, same as dial(): withdrawn public space is unreachable with
	// the session's name on it
	if ok, why := bgpReachable(src.W, dstIP); !ok {
		return why, false
	}

	sameLAN := src.onLink(dstIP)
	if !sameLAN && !IsV6(dstIP) {
		// §31 for pings too: a denied VLAN pair is unreachable with the
		// gateway's name on it, an allowed one proceeds LAN-side
		if dstID, ok := src.W.IPMap[dstIP]; ok {
			if dst2 := src.W.Devices[dstID]; dst2 != nil {
				if applies, allow, gw, sv, dv := vlanVerdict(src.W, src, dst2, dstIP, 0, "icmp"); applies {
					if !allow {
						if gw == nil {
							return fmt.Sprintf("No route to host (VLAN %d and %d are not routed)", sv, dv), false
						}
						return fmt.Sprintf("Destination Host Unreachable (filtered by %s: VLAN %d to %d denied)", gw.Hostname, sv, dv), false
					}
					sameLAN = true
				}
			}
		}
	}
	if !sameLAN {
		if src.IsV6Only() {
			return "Network is unreachable (no IPv4 address: this host is IPv6-only)", false
		}
		if src.GatewayIP() == "" && src.Profile != "core" {
			return "Network is unreachable (no default route)", false
		}
		if isPrivate(dstIP) || ClassifyAddr(dstIP).Kind == KindULA {
			// a private address across the internet is not routable
			return "No route to host", false
		}
		if IsV6(dstIP) {
			// §13: v6 does not translate, so a LAN host is reachable only
			// where the router in front has a rule naming its address
			if r := src.W.routerFor(dst); r != nil && r != dst && !r.PermitsWAN6(dstIP, 1) {
				return "Destination Host Unreachable (filtered by " + r.Hostname + ")", false
			}
		} else if dst.Profile != "core" && dst.Profile != "vps" && dst.Profile != "infra" {
			// a home router only forwards WAN traffic it has a rule for; ICMP
			// to the router itself is judged by its own WAN policy
			r := dst.W.routerFor(dst)
			if r == nil && dst.Profile == "router" {
				r = dst
			}
			if r != nil && r.matchFwd(1) == nil {
				return "Destination Host Unreachable (filtered by " + r.Hostname + ")", false
			}
		}
	}
	// §33: a ban drops the machine's traffic, ICMP included. A banned host
	// that could still ping would not be banned.
	if dst.IsBannedBy(src) {
		return "Destination Host Unreachable (host is down)", false
	}
	if IsV6(dstIP) {
		if dst.FWDropInput6(!sameLAN, 1) {
			return "Destination Host Unreachable (firewalled)", false
		}
		return "connected", true
	}
	if dst.FWDropInput(!sameLAN, 1) {
		return "Destination Host Unreachable (firewalled)", false
	}
	return "connected", true
}

// onLink reports whether an address is reachable without a router: inside one
// of this device's own subnets, v4 or v6.
func (d *Device) onLink(ip string) bool {
	for _, i := range d.Ifaces {
		if !i.Up {
			continue
		}
		if i.CIDR != "" && isIPv4(ip) && inNet(ip, i.CIDR) {
			return true
		}
		if IsV6(ip) && d.v6InAny(ip) {
			return true
		}
	}
	return false
}

// Dial answers: can a TCP connection from src reach dstIP:port, and which
// service would accept it. This is the ONE causal gate everything uses.
//
// §33: it is also the place where a connection attempt becomes a fact on the
// destination — every attempt, whatever the verdict, is recorded as a flow
// (that is what this world's IDS reads, because there is no packet capture),
// and a ban is enforced here, before any gate, so a banned source really
// cannot reach the machine. Side effects: target-side logs and flows.
func Dial(src *Device, dstIP string, port int) (*Service, *Device, string) {
	if port <= 0 || port > 65535 {
		return nil, nil, "invalid port"
	}
	// A ban at the *named* destination is the fast path and the honest one:
	// the source never gets as far as a service list. A forwarded connection
	// is judged by the machine it lands on, which dial() does below.
	if d := src.W.Devices[src.W.IPMap[dstIP]]; d != nil && d != src {
		if srcIP := src.sourceIPFor(d); d.IsBannedBy(src) {
			d.NoteFlow(Flow{Src: srcIP, SrcHost: src.Hostname, Port: port, Verdict: FlowBanned})
			d.Logf("warn", "fail2ban", "connection from %s (%s:%d) refused: still banned", src.Hostname, srcIP, port)
			return nil, d, "Connection timed out (filtered)"
		}
	}
	svc, dst, msg := dial(src, dstIP, port)
	// The destination's own record. A banned source that got this far was
	// banned at a machine further in (a port-forward's inner host).
	if dst != nil && dst != src {
		srcIP := src.sourceIPFor(dst)
		verdict := flowVerdictOf(msg)
		if dst.IsBannedBy(src) {
			verdict = FlowBanned
			dst.Logf("warn", "fail2ban", "connection from %s (%s:%d) refused: still banned", src.Hostname, srcIP, port)
			msg = "Connection timed out (filtered)"
			svc = nil
		}
		dst.NoteFlow(Flow{Src: srcIP, SrcHost: src.Hostname, Port: port, Verdict: verdict})
	}
	return svc, dst, msg
}

// flowVerdictOf maps the packet path's own words onto the record's four kinds.
// Anything that is not an accept or an explicit refusal was dropped somewhere,
// which is what "filtered" means to the host that was watching.
func flowVerdictOf(msg string) FlowVerdict {
	switch {
	case msg == "connected" || msg == "loopback":
		return FlowAccepted
	case strings.HasPrefix(msg, "Connection refused"):
		return FlowRefused
	default:
		return FlowFiltered
	}
}

func dial(src *Device, dstIP string, port int) (*Service, *Device, string) {
	if port <= 0 || port > 65535 {
		return nil, nil, "invalid port"
	}

	// loopback
	if dstIP == "127.0.0.1" {
		for _, s := range src.Services {
			if svcListensOn(s, port) && s.State == "running" {
				return s, src, "loopback"
			}
		}
		return nil, src, "Connection refused"
	}

	// find target device by ip
	dstID, ok := src.W.IPMap[dstIP]
	if !ok {
		switch {
		case IsSharedAddr(dstIP):
			// §13's carrier-grade NAT: the address is real and in use, and it
			// is not routable — that is the whole point of sharing it
			return nil, nil, "No route to host (carrier-grade NAT: the address is not routable from the internet)"
		case IsV6(dstIP) && ClassifyAddr(dstIP).Kind == KindULA:
			return nil, nil, "No route to host (ULA is never routed)"
		case !isIPv4(dstIP) && !IsV6(dstIP):
			// unknown public ip → routed to ISP blackhole after ttl
			return nil, nil, "unknown host"
		}
		return nil, nil, "No route to host"
	}
	dst := src.W.Devices[dstID]

	// BGP RIB: a withdrawn public prefix is no route at all, before power,
	// firewall or anything else gets a vote. Private space never consults
	// the RIB — it was never announced.
	if ok, why := bgpReachable(src.W, dstIP); !ok {
		return nil, dst, why
	}

	// Power is the first gate: a dark machine cannot open a socket, and a dark
	// target cannot accept one.
	if !src.Powered() {
		return nil, nil, "Network is unreachable (no power)"
	}
	if !dst.Powered() {
		return nil, dst, "Connection timed out (host is down)"
	}

	// is dst on src's own LAN?
	sameLAN := src.onLink(dstIP)

	// §15: the physical path is checked before anything above it. A device
	// behind a switch port that is down is not "filtered" or "down" — the
	// cable is down, and that is a different diagnosis with a different fix.
	if up, why := src.W.linkUpVia(src, !sameLAN); !up {
		return nil, nil, "Network is unreachable (link down: " + why + ")"
	}
	if up, why := src.W.linkUpVia(dst, !sameLAN); !up {
		return nil, dst, "Connection timed out (link down: " + why + ")"
	}

	if !sameLAN {
		// §13's v6-only plan: a node with no IPv4 address cannot reach an IPv4
		// destination, and saying so is the honest answer — NAT64 is a thing
		// this world does not have.
		if src.IsV6Only() {
			return nil, nil, "Network is unreachable (no IPv4 address: this host is IPv6-only)"
		}
		// must have a working gateway (broken dhcp / down link blocks)
		if src.GatewayIP() == "" && src.Profile != "core" {
			return nil, nil, "Network is unreachable (no default route)"
		}
	}

	fromWAN := !sameLAN

	// §31 inter-VLAN traffic is gateway-routed, never LAN-direct: two
	// tagged devices on different VLANs are not on-link with each other.
	// Allowed traffic arrives on the target's LAN side (tagged), so the rest
	// of the path judges it like LAN traffic; denied traffic stops here
	// naming both VLANs. v6 keeps its existing publication rules.
	if fromWAN && !IsV6(dstIP) {
		if applies, allow, gw, sv, dv := vlanVerdict(src.W, src, dst, dstIP, port, "tcp"); applies {
			if !allow {
				if gw == nil {
					return nil, dst, fmt.Sprintf("No route to host (VLAN %d and %d are not routed)", sv, dv)
				}
				if !gw.Powered() {
					return nil, dst, fmt.Sprintf("Connection timed out (%s is down: VLAN %d to %d has no router)", gw.Hostname, sv, dv)
				}
				return nil, dst, fmt.Sprintf("Connection timed out (filtered by %s: VLAN %d to %d denied)", gw.Hostname, sv, dv)
			}
			fromWAN = false
		}
	}

	// §13: IPv6 does not translate. Nothing is DNATed, so the whole NAT half
	// of the v4 path below does not apply; what decides is a rule on the
	// router in front, and then the target's own v6 ruleset.
	if IsV6(dstIP) {
		return dialV6(src, dst, dstIP, port, fromWAN)
	}

	// Router in the path applies NAT/firewall for home LANs:
	// external → LAN service requires an enabled port forward (or DMZ).
	// RFC1918 must not be reachable across the public internet
	if fromWAN && isPrivate(dstIP) {
		return nil, dst, "No route to host"
	}

	// The router in front of a home network applies NAT: WAN traffic lands on
	// an inner host only where a redirect says to translate it (§14). A
	// redirect with no destination port is a DMZ and matches every port; a
	// UPnP lease is the same thing with a shorter life. Nothing else opens the
	// door — a permissive zone rule alone cannot, because without a
	// translation rule there is nowhere to send the packet, which is why the
	// misconfiguration people actually hit is a forward or a WAN accept.
	forwarded := false
	outer := dst
	if dst.Profile != "core" && dst.Profile != "vps" && dst.Profile != "infra" && dst.Profile != "peer" && dst.Profile != "server" && fromWAN {
		r := dst.W.routerFor(dst)
		if r == nil && dst.Profile == "router" {
			// the destination *is* the household's edge: the packet arrives
			// on its own WAN, and a silent drop there is exactly the kind of
			// thing its owner goes looking for in the log
			r = dst
		}
		if r != nil {
			via := r.matchFwd(port)
			if via == nil {
				// a packet with no translation rule can only be answered by
				// the router itself, and only if its own WAN policy allows it
				if r != dst || !dst.PermitsWAN(port) {
					r.logFiltered(src, port, dst == r)
					return nil, dst, "Connection timed out (filtered)"
				}
				forwarded = true
			} else {
				// DNAT: land on the internal target instead
				if id, ok := src.W.IPMap[via.DstIP]; ok {
					dst = src.W.Devices[id]
				}
				if via.DPort > 0 {
					port = via.DPort
				}
				forwarded = true
				r.logForward(src, via, port)
			}
		}
	}

	// A forward lands the connection on a different machine, and every gate
	// below judges THAT machine: a dark box behind a powered router must still
	// time out, or the world would answer from a host it reports as off.
	if dst != outer && !dst.Powered() {
		return nil, dst, "Connection timed out (host is down)"
	}

	// §33: a ban is not a rule and not a policy — it is a refusal this machine
	// remembered from evidence, and it is checked against the v4 firewall rules
	// it sits beside (fail2ban writes real drops). Doing it after the DNAT
	// means a forwarded connection is judged by the machine it lands on.
	if dst.IsBannedBy(src) {
		return nil, dst, "Connection timed out (filtered)"
	}

	// target firewall INPUT. An explicit rule always applies; the default-deny
	// WAN policy applies to traffic that arrives on its own. A port-forward is a
	// hole the owner opened, and the reason exposed home services are this
	// world's attack surface (§14/§28) — without this, every forward would be a
	// dead rule, because a home PC's default policy drops WAN input.
	if dst.FWDropInput(fromWAN && !forwarded, port) {
		return nil, dst, "Connection timed out (filtered)"
	}

	// service must exist, run, listen on that port, and allow the scope
	for _, s := range dst.Services {
		if !svcListensOn(s, port) {
			continue
		}
		if s.State != "running" {
			if s.State == "stopped" {
				return nil, dst, "Connection refused"
			}
			return nil, dst, "Connection refused (service " + s.State + ")"
		}
		// A forwarded connection has already been translated by the owner's
		// router, which is the accept that makes a LAN-bound service reachable
		// — that is what forwarding a port to the NAS or the camera really
		// does. Everything else must have been allowed by the target's own
		// configuration.
		if fromWAN && s.Scope == "lan" && !forwarded && !dst.PermitsWAN(port) {
			return nil, dst, "Connection refused (service bound to LAN only)"
		}
		// success — record evidence on the target
		// one honest line per accepted connection, whatever the protocol: this
		// is the record forensics and the IDS later read
		dst.Logf("info", s.Name, "connection accepted from %s (%s:%d)", src.Hostname, src.sourceIPFor(dst), port)
		return s, dst, "connected"
	}
	return nil, dst, "Connection refused"
}

// leaseValid answers whether a DHCP interface still holds its lease: from the
// router for a LAN client, or from the client's own udhcpc lease file.
func leaseValid(d *Device, i *Iface) bool {
	if r := d.W.routerFor(d); r != nil && r.Svc("dnsmasq") != nil && r.Svc("dnsmasq").State == "running" {
		return true
	}
	data, ok := d.FS.Read("/var/run/udhcpc." + i.Name + ".lease")
	if !ok {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, found := strings.CutPrefix(line, "ip="); found {
			return strings.TrimSpace(v) == i.IP || (i.IP == "" && strings.TrimSpace(v) == i.SharedIP)
		}
	}
	return false
}

// dialV6 is IPv6's half of the §13/§14 packet path. v6 has no NAT: a service
// behind a home router is published by a firewall rule that names the host's
// own address, never by a port-forward, and the host's own rules6 file is the
// second gate. A world that modelled v6 as "v4 with longer addresses" would
// teach exactly the wrong lesson, so it is a separate path.
func dialV6(src, dst *Device, dstIP string, port int, fromWAN bool) (*Service, *Device, string) {
	if !src.ifaceHasV6() {
		return nil, nil, "Network is unreachable (no IPv6 address)"
	}
	if dst.IsBannedBy(src) {
		return nil, dst, "Connection timed out (filtered)"
	}
	permitted := !fromWAN
	if fromWAN {
		if ClassifyAddr(dstIP).Kind == KindULA || isPrivate(dstIP) {
			return nil, dst, "No route to host"
		}
		if r := src.W.routerFor(dst); r != nil && r != dst {
			if !r.PermitsWAN6(dstIP, port) {
				r.logFiltered6(src, port, dstIP)
				return nil, dst, "Connection timed out (filtered)"
			}
			permitted = true
		}
	}
	if dst.FWDropInput6(fromWAN, port) {
		return nil, dst, "Connection timed out (filtered)"
	}
	for _, s := range dst.Services {
		if !svcListensOn(s, port) {
			continue
		}
		if s.State != "running" {
			if s.State == "stopped" {
				return nil, dst, "Connection refused"
			}
			return nil, dst, "Connection refused (service " + s.State + ")"
		}
		if fromWAN && s.Scope == "lan" && !permitted {
			return nil, dst, "Connection refused (service bound to LAN only)"
		}
		dst.Logf("info", s.Name, "connection accepted from %s (%s:%d)", src.Hostname, src.sourceIPFor(dst), port)
		return s, dst, "connected"
	}
	return nil, dst, "Connection refused"
}

// logFiltered6 records a dropped v6 packet the same way v4 drops are recorded:
// on the router, with the address that was refused.
func (r *Device) logFiltered6(src *Device, port int, dstIP string) {
	st := r.FW()
	if !st.LogDrops {
		return
	}
	r.Logf("info", "firewall", "DROP wan6 [%s]:%d/tcp from %s (no v6 rule names that host)",
		dstIP, port, src.sourceIPFor(r))
}

// ForwardTarget answers which device a connection to (ip, port) really lands on:
// the device that owns the address, unless an enabled port-forward on the router
// in front of it DNATs that port to a machine behind it. Recon, scanning and the
// exploit paths must follow this, or a forwarded service would look like a
// service on the router and its vulnerabilities would be invisible — the same
// reason Dial() lands the connection on the inner host.
func ForwardTarget(src *Device, ip string, port int) *Device {
	if src == nil || src.W == nil {
		return nil
	}
	id, ok := src.W.IPMap[ip]
	if !ok {
		return nil
	}
	d := src.W.Devices[id]
	if d == nil {
		return d
	}
	r := src.W.routerFor(d)
	if r == nil && d.Profile == "router" {
		// the address belongs to the household's edge itself: its own WAN
		// policy and its own redirects are what the packet meets
		r = d
	}
	if r == nil {
		return d
	}
	via := r.matchFwd(port)
	if via == nil {
		return d
	}
	if iid, ok := src.W.IPMap[via.DstIP]; ok {
		if inner := src.W.Devices[iid]; inner != nil {
			return inner
		}
	}
	return d
}

// svcListensOn reports whether the service accepts a connection on port:
// its own port, plus the standard https port when the unit carries TLS
// material (a web server with a certificate binds both sockets from one
// unit, so stopping the unit takes https down with it).
func svcListensOn(s *Service, port int) bool {
	if s.Port == port {
		return true
	}
	return port == 443 && s.TLSCert != ""
}

// sourceIPFor: what ip would src appear as to dst (NAT-aware).
func (d *Device) sourceIPFor(dst *Device) string {
	// §13: a customer behind carrier-grade NAT never appears under its own
	// address. What the remote host logs is the provider's NAT egress, which is
	// why a lookup of it names an ISP and not a person.
	if d.NATed {
		if as := d.ASFor(); as != nil {
			if ip := d.W.NATAddress(as.ASN); ip != "" {
				return ip
			}
		}
		return d.W.NATAddress(asNova)
	}
	if dst != nil && dst.ifaceHasV6() {
		if v6 := d.FirstWANv6(); v6 != "" {
			return v6
		}
	}
	if wan := wanIP(d); wan != "" {
		for _, i := range d.Ifaces {
			if i.Zone == "wan" {
				return i.IP
			}
		}
	}
	for _, i := range d.Ifaces {
		if i.Up && i.IP != "" && i.Zone == "lan" {
			return i.IP
		}
	}
	return "127.0.0.1"
}

// matchFwd finds the redirect that carries this WAN port inside: a configured
// forward, a DMZ, or a live UPnP mapping. It reads the router's real
// configuration every time, so a player who edits `/etc/config/firewall` (or
// runs `uci set`) changes what the next packet does.
func (r *Device) matchFwd(port int) *Redirect {
	return r.FW().RedirectFor(port)
}

// PermitsWAN reports whether this device's own configuration lets a WAN packet
// reach one of its ports: an explicit ACCEPT rule for that port, or a policy
// that accepts WAN input outright.
func (d *Device) PermitsWAN(port int) bool {
	st := d.FW()
	if st.WANAllowsPort(port) {
		return true
	}
	if d.Profile == "router" {
		return strings.EqualFold(st.WANInput, "ACCEPT")
	}
	return strings.EqualFold(st.HostInput, "ACCEPT")
}

// FWDropInput on an end device: would its own ruleset discard this packet?
func (d *Device) FWDropInput(fromWAN bool, port int) bool {
	return fromWAN && !d.PermitsWAN(port)
}

// logFiltered records a dropped WAN packet when the router's config asks for
// drop logging — the setting that turns "the internet is scanning me" into
// something a player can read in logread instead of guess.
func (r *Device) logFiltered(src *Device, port int, toSelf bool) {
	st := r.FW()
	if !st.LogDrops {
		return
	}
	where := "lan"
	if toSelf {
		where = "router"
	}
	r.Logf("info", "firewall", "DROP wan %d/tcp from %s -> %s (no rule permits it)",
		port, src.sourceIPFor(r), where)
}

// logForward records the translation a WAN packet really received. This is the
// evidence that a forwarded port is a hole somebody opened, and the line a
// player finds when they go looking for why a stranger reached the NAS.
func (r *Device) logForward(src *Device, via *Redirect, dport int) {
	why := "redirect " + via.Name
	if via.UPnP {
		why = "UPnP mapping " + via.Desc
	} else if via.DMZ {
		why = "DMZ to " + via.DstIP
	}
	r.Logf("info", "firewall", "DNAT %s:%d -> %s:%d (%s, from %s)",
		r.Hostname, via.WPort, via.DstIP, dport, why, src.sourceIPFor(r))
}
