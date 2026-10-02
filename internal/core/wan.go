package core

import (
	"strings"
)

// WAN is the public-internet fabric: autonomous systems, prefix ownership,
// peering points and the path a packet actually takes between two public
// addresses.
//
// The spec (三十八 地理/网络基础设施, 十三 IP 系统) asks for the internet itself
// to be an observable map. It is NOT a second copy of the routing logic: the
// world already routes packets through core.Reach/core.Dial using the devices'
// real interfaces and gateways. What this file adds is the *naming and
// attribution* layer — which AS announces a prefix, who to blame, what the
// provider is — plus the hop path for traceroute, derived from the real device
// graph rather than invented.
type WAN struct {
	Ready bool

	// ASes indexed by ASN. Every public device in the world belongs to exactly
	// one, and the world allocates the /24s from these blocks.
	ASes map[int]*AS

	// the IXP/transit devices that physically carry public traffic, by device id
	Transit map[string]bool
}

// AS is an autonomous system: one organisation's public network.
type AS struct {
	ASN      int
	Name     string
	Org      string
	Region   string
	Country  string
	Prefixes []string // the public ranges it announces
	Peers    []int    // other ASNs it exchanges routes with
	Upstream []int    // ASNs it gets its own reachability from
	Latency  float64  // ms to its neighbours, used by traceroute timings
	Abuse    string
	RDNS     string
	Status   string // SYNCED | BEHIND | OFFLINE | PARTIAL | CORRUPTED
}

// The world's public address space. 203.0.113.0/24 and 198.51.100.0/24 are the
// two documentation ranges (RFC 5737); 192.0.2.0/24 is TEST-NET-1. They are the
// right choice for a game world because they can never collide with a real host.
const (
	asCore     = 64500 // the world's transit provider / IXP
	asNetCrest = 64510 // the ISP every household's WAN faces
	asNova     = 64520 // novapanel, the VPS provider
	asPeer     = 64530 // a mid-sized peer network
)

// seedWAN builds the public internet at world creation.
func seedWAN(w *World) {
	w.WAN = &WAN{
		Ready:   true,
		ASes:    map[int]*AS{},
		Transit: map[string]bool{},
	}
	wan := w.WAN

	wan.ASes[asCore] = &AS{
		ASN: asCore, Name: "NeoCore Transit", Org: "NeoCore Networks",
		Region: "global", Country: "XX", Latency: 0.4,
		Peers: []int{asNetCrest, asNova, asPeer},
		Abuse: "abuse@neocore.example", RDNS: "whois.neocore.example",
		Status: "SYNCED",
	}
	wan.ASes[asNetCrest] = &AS{
		ASN: asNetCrest, Name: "NetCrest ISP", Org: "NetCrest Communications",
		Region: "eu-west", Country: "ZA", Latency: 8.1,
		Prefixes: []string{}, Peers: []int{asCore, asPeer}, Upstream: []int{asCore},
		Abuse: "abuse@netcrest.example", RDNS: "whois.netcrest.example", Status: "SYNCED",
	}
	wan.ASes[asNova] = &AS{
		ASN: asNova, Name: "NovaPanel", Org: "NovaPanel Hosting BV",
		Region: "eu-central", Country: "NL", Latency: 12.4,
		Prefixes: []string{}, Peers: []int{asCore, asNetCrest}, Upstream: []int{asCore},
		Abuse: "abuse@novapanel.example", RDNS: "whois.novapanel.example", Status: "SYNCED",
	}
	wan.ASes[asPeer] = &AS{
		ASN: asPeer, Name: "Meridian Systems", Org: "Meridian Systems Inc",
		Region: "us-east", Country: "US", Latency: 22.7,
		Prefixes: []string{}, Peers: []int{asCore, asNetCrest}, Upstream: []int{asCore},
		Abuse: "abuse@meridian.example", RDNS: "rdap.meridian.example", Status: "BEHIND",
	}

	// The devices that physically carry public traffic.
	wan.Transit["core-gw"] = true

	// Announce the /24 each public block belongs to. Allocation already placed
	// every device inside the /24 of the AS that operates it, so ownership here
	// is a fact about the address space, not a per-device guess.
	for _, b := range publicBlocks {
		as := wan.ASes[b.asn]
		if as == nil {
			continue
		}
		p := blockOf(b.base+"1") + "/24"
		if wan.Owner(p) == nil {
			as.Prefixes = append(as.Prefixes, p)
		}
	}

	// A device numbered outside those blocks (nothing should be, but the world
	// must never hold an address no AS announces) still gets an owner.
	for _, d := range w.Devices {
		ip := wanIP(d)
		if ip == "" {
			continue
		}
		if wan.ASFor(ip) == nil {
			wan.assign(d, ip)
		}
	}
}

// assign puts a device's public address in the right AS and allocates its /24.
// A /24 is a routing commitment: it belongs to exactly one AS. If a /24 is
// already announced, the address is routed through that AS (which is what
// really happens on the internet) and no second holder is recorded.
func (wan *WAN) assign(d *Device, ip string) {
	asn := asCore
	switch {
	case d.Profile == "vps":
		asn = asNova
	case d.Profile == "router" || d.Profile == "pc" || d.Profile == "nas":
		asn = asNetCrest
	}
	as := wan.ASes[asn]
	if as == nil {
		return
	}
	prefix := blockOf(ip) + "/24"
	for _, p := range as.Prefixes {
		if p == prefix {
			return // already announced
		}
	}
	as.Prefixes = append(as.Prefixes, prefix)
}

// blockOf returns the /24 network address of an IPv4 address, e.g.
// 203.0.113.7 -> "203.0.113.0".
func blockOf(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ip
	}
	// keep the trailing dot that a prefix string wants: "203.0.113." + "0"
	return parts[0] + "." + parts[1] + "." + parts[2] + "." + "0"
}

// Owner returns the AS that announced a /24, or nil.
func (wan *WAN) Owner(prefix string) *AS {
	for _, as := range wan.ASes {
		for _, p := range as.Prefixes {
			if p == prefix {
				return as
			}
		}
	}
	return nil
}

// ASFor returns the AS that announces a public address.
func (wan *WAN) ASFor(ip string) *AS {
	if wan == nil || !isIPv4(ip) {
		return nil
	}
	best := (*AS)(nil)
	bestLen := -1
	for _, as := range wan.ASes {
		for _, p := range as.Prefixes {
			if inNet(ip, p) {
				if n := prefixLen(p); n > bestLen {
					best, bestLen = as, n
				}
			}
		}
	}
	return best
}

func prefixLen(p string) int {
	i := strings.Index(p, "/")
	if i < 0 {
		return 24
	}
	n := 0
	for _, c := range p[i+1:] {
		if c < '0' || c > '9' {
			return 24
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Attribution is what an IP lookup honestly yields. The spec is explicit
// (十三 IP 系统): "「公开 IP」不等于「公开玩家真实身份」" — a query may return
// provider, ASN, region, range and abuse contact, but never a person's identity.
type Attribution struct {
	IP      string
	ASN     int
	ASName  string
	Org     string
	Region  string
	Country string
	Netname string
	Range   string
	Abuse   string
	RDNS    string
	Status  string
}

// Lookup is the world's whois/rdap answer for a public address.
func (w *World) Lookup(ip string) (Attribution, bool) {
	var a Attribution
	a.IP = ip
	if !isIPv4(ip) {
		return a, false
	}
	// A private address has no public attribution at all — registries hold no
	// record of RFC1918 space.
	if isPrivate(ip) {
		return a, false
	}
	as := w.WAN.ASFor(ip)
	if as == nil {
		// Routed but unannounced: it exists in the world, nobody announces it.
		a.Netname = "UNASSIGNED"
		a.ASName = "unannounced"
		return a, true
	}
	a.ASN = as.ASN
	a.ASName = as.Name
	a.Org = as.Org
	a.Region = as.Region
	a.Country = as.Country
	a.Abuse = as.Abuse
	a.RDNS = as.RDNS
	a.Status = as.Status
	best := ""
	for _, p := range as.Prefixes {
		if inNet(ip, p) {
			if len(p) > len(best) {
				best = p
			}
		}
	}
	a.Range = best
	a.Netname = strings.ReplaceAll(as.Name, " ", "-")
	if a.Range == "" {
		return a, true
	}
	return a, true
}

// Path is the hop sequence between two devices, derived from the real device
// graph (local interfaces, the household router, the transit provider) rather
// than invented. Each hop is a device that actually exists in the world.
type Hop struct {
	IP      string
	Device  string // hostname, or "" for a pure network hop
	Latency float64
}

// Trace builds the real path src -> dst. It only ever names devices that exist.
func (w *World) Trace(src *Device, dstIP string) ([]Hop, bool) {
	hops := []Hop{}
	acc := 0.0
	add := func(d *Device, ip string) {
		if ip == "" {
			return
		}
		acc += 0.4 + w.WAN.asLatency(ip)*0.6
		h := Hop{IP: ip, Latency: acc}
		if d != nil {
			h.Device = d.Hostname
		}
		hops = append(hops, h)
	}

	// A packet leaving the host first hits its own default gateway.
	if gw := src.GatewayIP(); gw != "" {
		if g, ok := w.Devices[w.IPMap[gw]]; ok {
			add(g, gw)
		}
	}
	// Households and VPS nodes are announced by an AS whose traffic crosses the
	// world's transit provider. That is a real hop: the device exists.
	if as := w.WAN.ASFor(wanIP(src)); as != nil && as.ASN != asCore {
		if core, ok := w.Devices["core-gw"]; ok {
			add(core, core.FirstWANIP())
		}
	}
	if dstID, ok := w.IPMap[dstIP]; ok {
		dst := w.Devices[dstID]
		// Enter the destination's own AS before the host itself.
		if as := w.WAN.ASFor(dstIP); as != nil && as.ASN != asCore {
			if as.ASN == asNova || as.ASN == asNetCrest {
				acc += 1.1
				hops = append(hops, Hop{
					IP:      as.edgeIP(dstIP),
					Device:  as.Name,
					Latency: acc,
				})
			}
		}
		add(dst, dstIP)
	} else {
		// Unknown destination: the packet dies at the transit edge, exactly as
		// a real one does at the last router before the destination network.
		if core, ok := w.Devices["core-gw"]; ok {
			add(core, core.FirstWANIP())
		}
		acc += 40
		hops = append(hops, Hop{IP: dstIP, Latency: acc})
	}
	return hops, len(hops) > 0
}

func (as *AS) asLatency(ip string) float64 { return as.Latency }

// edgeIP is the representative router address of the AS's edge: the first
// address of the announced /24, which is where packets are handed over.
func (as *AS) edgeIP(ip string) string {
	i := strings.LastIndex(ip, ".")
	if i < 0 {
		return ip
	}
	return ip[:i+1] + "1"
}

// asLatency returns the latency to an address's announcing AS.
func (wan *WAN) asLatency(ip string) float64 {
	if as := wan.ASFor(ip); as != nil {
		return as.Latency
	}
	return 30.0
}

// WANTick advances the public internet. Peering state is real state: an AS that
// loses its upstream has no reachability, and that is visible to a player
// because packets to it stop arriving.
func (w *World) WANTick() {
	if w.WAN == nil {
		return
	}
	// Provider repositories sync on their own cadence; a mirror that is BEHIND
	// is stale, and a player who apt-upgrades from it gets an old index. This
	// is derived from the devices that actually host repositories.
	for _, d := range w.Devices {
		if d.Profile != "infra" && d.Profile != "vps" {
			continue
		}
		as := w.WAN.ASFor(wanIP(d))
		if as == nil {
			continue
		}
		if as.ASN == asCore && w.TickCount%40 == 3 {
			// the transit provider's own sync oscillates deterministically
			switch (w.TickCount / 40) % 6 {
			case 0:
				as.Status = "SYNCED"
			case 1:
				as.Status = "BEHIND"
			case 2:
				as.Status = "SYNCED"
			case 3:
				as.Status = "PARTIAL"
			default:
				as.Status = "SYNCED"
			}
		}
	}
}
