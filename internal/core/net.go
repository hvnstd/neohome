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

// allocPublicFor allocates from the AS that operates a device of this profile.
func (w *World) allocPublicFor(profile string) string {
	want := asnForProfile(profile)
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
			// lease still valid? router dhcp must be running
			r := d.W.routerFor(d)
			if r == nil || r.Svc("dnsmasq") == nil || r.Svc("dnsmasq").State != "running" {
				continue
			}
			return i.GW
		}
		return i.GW
	}
	return ""
}

// routerFor finds the LAN router serving this device (by shared lan CIDR).
func (w *World) routerFor(d *Device) *Device {
	net := ""
	for _, i := range d.Ifaces {
		if i.Zone == "lan" || i.Mode == "dhcp" {
			net = i.CIDR
		}
	}
	if net == "" {
		return nil
	}
	for _, id := range w.Order {
		dev := w.Devices[id]
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
	if isIPv4(name) {
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
					if alias == name {
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
	return answerVia(d.W, d, resolver, name, 0)
}

// answerVia walks one resolver hop; depth guards loops. A forwarder with a
// healthy resolv-file recurses to its own upstream; an authoritative server
// answers from the zone.
func answerVia(w *World, ask *Device, resolverIP, name string, depth int) (string, bool, string) {
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
					if strings.EqualFold(alias, name) {
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
		// authoritative: answer from zone or NXDOMAIN
		for _, r := range w.Records {
			if strings.EqualFold(r.Name, name) {
				return r.IP, true, fmt.Sprintf("authoritative via %s", resolverIP)
			}
		}
		return "", false, "NXDOMAIN"
	}
	// forwarder: consult its upstream list
	for _, up := range rd.upstreamServers() {
		if ip, ok2, how := answerVia(w, rd, up, name, depth+1); ok2 {
			return ip, true, how + " (forwarded by " + rd.Hostname + ")"
		}
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
		// file exists but has no nameserver line → that IS the config: broken
		return ""
	}
	// fall back to DHCP-assigned dns
	for _, i := range d.Ifaces {
		if i.Mode == "dhcp" {
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

// ---------- reachability ----------

// Reach answers the routing question without naming a service: can src send
// packets to dstIP at all? This is what ping/traceroute consume — ICMP is not
// TCP, so it must be gated by routing and NAT, not by "is there a listener".
func Reach(src *Device, dstIP string) (string, bool) {
	if dstIP == "127.0.0.1" {
		return "loopback", true
	}
	if !isIPv4(dstIP) {
		return "unknown host", false
	}
	dstID, ok := src.W.IPMap[dstIP]
	if !ok {
		return "No route to host", false
	}
	dst := src.W.Devices[dstID]

	sameLAN := false
	for _, i := range src.Ifaces {
		if i.Up && i.CIDR != "" && inNet(dstIP, i.CIDR) {
			sameLAN = true
		}
	}
	if !sameLAN {
		if src.GatewayIP() == "" && src.Profile != "core" {
			return "Network is unreachable (no default route)", false
		}
		if isPrivate(dstIP) {
			// a private address across the internet is not routable
			return "No route to host", false
		}
		// a home router only forwards WAN traffic it has a rule for; ICMP to
		// the router itself is answered
		if dst.Profile != "core" && dst.Profile != "vps" && dst.Profile != "infra" {
			r := dst.W.routerFor(dst)
			if r != nil && r.matchFwd(1) == nil {
				return "Destination Host Unreachable (filtered by " + r.Hostname + ")", false
			}
		}
	}
	if dst.FWDropInput(!sameLAN, 1) {
		return "Destination Host Unreachable (firewalled)", false
	}
	return "connected", true
}

// Dial answers: can a TCP connection from src reach dstIP:port, and which
// service would accept it. This is the ONE causal gate everything uses.
// Side effects: target-side logs (auth/conn) so forensics and IDS work.
func Dial(src *Device, dstIP string, port int) (*Service, *Device, string) {
	if port <= 0 || port > 65535 {
		return nil, nil, "invalid port"
	}

	// loopback
	if dstIP == "127.0.0.1" {
		for _, s := range src.Services {
			if s.Port == port && s.State == "running" {
				return s, src, "loopback"
			}
		}
		return nil, src, "Connection refused"
	}

	// find target device by ip
	dstID, ok := src.W.IPMap[dstIP]
	if !ok {
		// unknown public ip → routed to ISP blackhole after ttl
		if !isIPv4(dstIP) {
			return nil, nil, "unknown host"
		}
		return nil, nil, "No route to host"
	}
	dst := src.W.Devices[dstID]

	// is dst on src's own LAN?
	sameLAN := false
	for _, i := range src.Ifaces {
		if i.Up && i.CIDR != "" && inNet(dstIP, i.CIDR) {
			sameLAN = true
		}
	}

	if !sameLAN {
		// must have a working gateway (broken dhcp / down link blocks)
		if src.GatewayIP() == "" && src.Profile != "core" {
			return nil, nil, "Network is unreachable (no default route)"
		}
	}

	fromWAN := !sameLAN

	// Router in the path applies NAT/firewall for home LANs:
	// external → LAN service requires an enabled port forward (or DMZ).
	// RFC1918 must not be reachable across the public internet
	if fromWAN && isPrivate(dstIP) {
		return nil, dst, "No route to host"
	}

	// home/office router applies port-forward translation for WAN traffic
	if dst.Profile != "core" && dst.Profile != "vps" && dst.Profile != "infra" && fromWAN {
		r := dst.W.routerFor(dst)
		if r != nil {
			fwd := r.matchFwd(port)
			if fwd == nil {
				return nil, dst, "Connection timed out (filtered)"
			}
			// DNAT: land on the internal target instead
			if id, ok := src.W.IPMap[fwd.DstIP]; ok {
				dst = src.W.Devices[id]
			}
			port = fwd.DPort
		}
	}

	// target firewall INPUT
	if dst.FWDropInput(fromWAN, port) {
		return nil, dst, "Connection timed out (filtered)"
	}

	// service must exist, run, listen on that port, and allow the scope
	for _, s := range dst.Services {
		if s.Port != port {
			continue
		}
		if s.State != "running" {
			if s.State == "stopped" {
				return nil, dst, "Connection refused"
			}
			return nil, dst, "Connection refused (service " + s.State + ")"
		}
		if fromWAN && s.Scope == "lan" {
			return nil, dst, "Connection refused (service bound to LAN only)"
		}
		// success — record evidence on the target
		dst.Logf("info", strings.TrimSuffix(s.Name, ""), "connection accepted from %s (%s:%d) via ssh-session", src.Hostname, src.sourceIPFor(dst), port)
		return s, dst, "connected"
	}
	return nil, dst, "Connection refused"
}

// sourceIPFor: what ip would src appear as to dst (NAT-aware).
func (d *Device) sourceIPFor(dst *Device) string {
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

func (r *Device) matchFwd(port int) *FwdRule {
	for i := range r.PortFwd {
		f := &r.PortFwd[i]
		if f.Enable && f.WPort == port {
			return f
		}
	}
	return nil
}

// FWDrop on router: default WAN→LAN deny unless fwd opened it.
func (d *Device) FWDrop(fromWAN bool, dstIP string, port int) bool {
	for _, r := range d.Firewall {
		if r.Chain == "WAN-TO-LAN" || r.Chain == "FORWARD" {
			if r.Proto == "tcp" && r.Port == port && r.Action == "ACCEPT" {
				return false
			}
		}
	}
	return fromWAN
}

// FWDropInput on end device: rules with chain INPUT.
func (d *Device) FWDropInput(fromWAN bool, port int) bool {
	for _, r := range d.Firewall {
		if r.Chain == "INPUT" && r.Proto == "tcp" && r.Port == port {
			if r.Action == "ACCEPT" {
				return false
			}
			return true
		}
	}
	// no explicit rule: WAN input default drop on home devices; infra/vps ok
	if fromWAN && (d.Profile == "pc" || d.Profile == "nas" || d.Profile == "router") {
		return true
	}
	return false
}
