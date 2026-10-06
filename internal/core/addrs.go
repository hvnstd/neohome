package core

import (
	"fmt"
	"net/netip"
	"strings"
)

// ---------------------------------------------------------------------------
// §13 IP system
//
// The spec asks for a world where an address has a *kind*: public IPv4, public
// IPv6, RFC1918, CGNAT/shared, loopback, link-local, ULA, dynamic, shared,
// virtual. That list is not a set of features to bolt on — it is the vocabulary
// everything else needs to tell the truth:
//
//   * a NAT router's port-forward works on public IPv4 and does not work behind
//     a carrier-grade NAT, because there is no public address to forward *to*;
//   * IPv6 has no NAT, so publishing a host is a firewall rule, not a redirect;
//   * "what is this address" is a different question from "who is this person",
//     and a lookup may answer provider/ASN/region/range/abuse/rDNS and stop
//     there (十三: 「公开 IP」不等于「公开玩家真实身份」).
//
// So classification lives in one place and every builtin that shows an address
// uses it. Nothing in this file is decorative: `AddrInfo.Kind` is what
// `whois`, `ip`, recon and the firewall's fail-closed paths read.

// Address kinds, as §13 lists them. The strings are what the commands print.
const (
	KindLoopback   = "loopback"
	KindLinkLocal  = "link-local"     // 169.254.0.0/16 or fe80::/10
	KindPrivate    = "private"        // RFC1918
	KindShared     = "shared"         // 100.64.0.0/10, carrier-grade NAT
	KindPublicV4   = "public IPv4"    //
	KindPublicV6   = "public IPv6"    // a globally routable v6 address
	KindULA        = "ULA"            // fd00::/8, private v6
	KindUnassigned = "unassigned"     // routed in-world but nobody announces it
	KindUnknown    = "not an address" //
)

// AddrInfo is everything the world knows about an address that is *not* a
// person: its family, its kind, which registry would hold it, and whether the
// public internet can reach it.
type AddrInfo struct {
	IP           string
	Family       int // 4 or 6, 0 when it does not parse
	Kind         string
	Scope        string // host | link | lan | world
	Registry     string // what an RDAP/whois server would answer with, as prose
	Public       bool   // routable from the public internet
	Translatable bool   // a NAT can translate it (everything v4 can be)
}

// v4 documentation ranges this world allocates from, plus the special-purpose
// blocks §13 names.
var (
	netLoopback = netip.MustParsePrefix("127.0.0.0/8")
	netLinkLoc4 = netip.MustParsePrefix("169.254.0.0/16")
	netShared   = netip.MustParsePrefix("100.64.0.0/10")
	netULA      = netip.MustParsePrefix("fd00::/8")
	netLinkLoc6 = netip.MustParsePrefix("fe80::/10")
	netLoop6    = netip.MustParsePrefix("::1/128")
	netDoc6     = netip.MustParsePrefix("2001:db8::/32")

	// every public block the world hands out, as contiguous prefixes: a
	// classifier that only knows the four /24s would lie about a fifth.
	netPublic []netip.Prefix
)

func init() {
	for _, b := range publicBlocks {
		if p, err := netip.ParsePrefix(b.base + "0/24"); err == nil {
			netPublic = append(netPublic, p)
		}
	}
}

// ClassifyAddr answers §13's question for any address in the world.
func ClassifyAddr(ip string) AddrInfo {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return AddrInfo{IP: ip, Kind: KindUnknown, Scope: "none", Registry: "no such address"}
	}
	info := AddrInfo{IP: a.String()}
	if a.Is4() {
		info.Family = 4
		info.Translatable = true
	}
	if a.Is6() {
		info.Family = 6
	}
	switch {
	case a.IsLoopback():
		info.Kind, info.Scope = KindLoopback, "host"
		info.Registry = "loopback: this machine, always present, never registered"
	case a.IsLinkLocalUnicast():
		info.Kind, info.Scope = KindLinkLocal, "link"
		info.Registry = "link-local: valid on one segment, never routed, never registered"
	case a.Is4() && netPrivate(a):
		info.Kind, info.Scope = KindPrivate, "lan"
		info.Registry = "RFC1918 private: no registry record, meaningless on the internet"
	case a.Is4() && netShared.Contains(a):
		info.Kind, info.Scope = KindShared, "carrier"
		info.Registry = "RFC6598 shared address space: a provider's NAT egress, not a subscriber's own address"
	case a.Is6() && netULA.Contains(a):
		info.Kind, info.Scope = KindULA, "lan"
		info.Registry = "IPv6 ULA (RFC4193): globally unique, never routed on the internet"
	case a.Is6() && netDoc6.Contains(a):
		info.Kind, info.Scope, info.Public = KindPublicV6, "world", true
		info.Registry = "globally routed, announced by its provider"
	case a.Is6():
		info.Kind, info.Scope, info.Public = KindPublicV6, "world", true
		info.Registry = "globally routed"
	case a.Is4() && inAnyPrefix(a, netPublic):
		info.Kind, info.Scope, info.Public = KindPublicV4, "world", true
		info.Registry = "registry record: provider, ASN, range, abuse contact, rDNS"
	default:
		info.Kind, info.Scope = KindUnassigned, "world"
		info.Registry = "outside every block this world allocates from"
	}
	return info
}

func netPrivate(a netip.Addr) bool {
	for _, p := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return true
		}
	}
	return false
}

func inAnyPrefix(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IsV6 reports whether an address string is IPv6. The world's older helpers are
// string-based; this is the one place that decides by parsing.
func IsV6(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil && a.Is6()
}

// IsV6CIDR is the CIDR form of IsV6: interface addresses are stored with their
// prefix length, which is not itself an address.
func IsV6CIDR(cidr string) bool {
	host, _, _ := strings.Cut(cidr, "/")
	return IsV6(host)
}

// IsSharedAddr reports whether an address is inside carrier-grade NAT space —
// the check the packet path uses before promising anybody a forwarded port.
func IsSharedAddr(ip string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil && a.Is4() && netShared.Contains(a)
}

// SharedBlock is the provider's CGNAT pool.
const SharedBlock = "100.64.0.0/10"

// RecordType is what a DNS server would publish for an address: §13's address
// families have to be visible through the name system too, or a dual-stack
// world would be a v4 world with v6 decoration.
func RecordType(ip string) string {
	if IsV6(ip) {
		return "AAAA"
	}
	return "A"
}

// ---------------------------------------------------------------------------
// IPv6 allocation
//
// Every AS announces exactly one /48 of documentation space (RFC 3849), and
// hands out /64s from it. A household's router gets one /64 for its LAN plus a
// link address on its WAN; a hosted node gets a /64 and puts its own service
// address ::1 in it. That is how real v6 works: a prefix per subscriber, not an
// address per subscriber — the reason a home network that is behind CGNAT on
// v4 can still be end-to-end reachable on v6.

// v6BlockFor returns the /48 an AS announces.
func v6BlockFor(asn int) netip.Prefix {
	// 2001:db8:6450::/48 for AS64500, and so on: the third hextet is the ASN in
	// hex, so an address's owner is readable off the address itself.
	return netip.MustParsePrefix(fmt.Sprintf("2001:db8:%x::/48", asn))
}

// v6Subnet carves the idx-th /64 out of an AS's /48.
func v6Subnet(base netip.Prefix, idx int) netip.Prefix {
	b := base.Addr().As16()
	b[6] = byte(idx >> 8)
	b[7] = byte(idx)
	return netip.PrefixFrom(netip.AddrFrom16(b), 64)
}

// v6HostFor places the interface identifier `last` in a /64.
func v6HostFor(sub netip.Prefix, last int) netip.Addr {
	b := sub.Addr().As16()
	b[15] = byte(last)
	return netip.AddrFrom16(b)
}

// v6Ifaces renders an address and a prefix length the way `ip` prints them.
func v6Ifaces(a netip.Addr, bits int) string { return fmt.Sprintf("%s/%d", a, bits) }

// ULAForLAN derives a household's private v6 prefix from its LAN: 10.77.1.0/24
// becomes fd00:77:1::/64. ULA is stable per household and never routed.
func ULAForLAN(cidr string) string {
	ip, _, _ := strings.Cut(cidr, "/")
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return ""
	}
	return fmt.Sprintf("fd00:%s:%s::/64", strings.TrimLeft(parts[1], "0"), strings.TrimLeft(parts[2], "0"))
}

// v6LANAddress puts a host into its LAN's global /64 using the last octet of
// its IPv4 address, so 10.77.1.40 is ...::40 and the two families of an
// address are readable against each other.
func v6LANAddress(prefix string, lanIP string) string {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return ""
	}
	parts := strings.Split(lanIP, ".")
	if len(parts) != 4 {
		return ""
	}
	last := atoi(parts[3])
	if last < 1 || last > 254 {
		return ""
	}
	return v6HostFor(p, last).String()
}

// linkLocal6 is the machine's own address on a segment.
func linkLocal6(ipv4 string) string {
	parts := strings.Split(ipv4, ".")
	if len(parts) != 4 {
		return ""
	}
	return "fe80::" + parts[3]
}

// allocV6For hands the next free /64 in the right AS's /48 to a new device and
// returns its service address (::1 in that /64).
func (w *World) allocV6For(profile string) string {
	return w.allocV6InAS(asnForProfile(profile))
}

// allocV6InAS numbers a node out of a specific AS's /48, which is what makes a
// node's IPv6 prefix name its region.
func (w *World) allocV6InAS(asn int) string {
	base := v6BlockFor(asn)
	if w.WAN == nil {
		w.WAN = &WAN{ASes: map[int]*AS{}, Transit: map[string]bool{}}
	}
	if w.WAN.V6Issued == nil {
		w.WAN.V6Issued = map[int]int{}
	}
	idx := w.WAN.V6Issued[asn]
	if idx < 16 {
		idx = 16 // sub-16 /64s are the AS's own infrastructure
	}
	used := map[string]bool{}
	for _, d := range w.Devices {
		for _, i := range d.Ifaces {
			for _, a := range i.IP6 {
				used[a] = true
			}
		}
	}
	for n := 0; n < 4096; n++ {
		cand := v6HostFor(v6Subnet(base, idx+n), 1)
		if !used[cand.String()+"/64"] && !used[cand.String()] {
			w.WAN.V6Issued[asn] = idx + n + 1
			return v6Ifaces(cand, 64)
		}
	}
	return ""
}

// allocSharedV4 numbers a customer of a provider's carrier-grade NAT. The
// address is real, in use, and *not* routable: it never enters the world's
// address map, which is exactly why a port-forward on it cannot work.
func (w *World) allocSharedV4() string {
	used := map[string]bool{}
	for _, d := range w.Devices {
		for _, i := range d.Ifaces {
			if i.IP != "" {
				used[i.IP] = true
			}
			if i.SharedIP != "" {
				used[i.SharedIP] = true
			}
		}
	}
	for slot := 0; slot < 64; slot++ {
		for n := 1; n < 254; n++ {
			cand := fmt.Sprintf("100.64.%d.%d", slot, n)
			if !used[cand] {
				return cand
			}
		}
	}
	return "100.64.0.254"
}

// NATAddress is the public address a provider's carrier-grade NAT uses to reach
// the internet on behalf of its shared customers. It belongs to the provider,
// never to the customer: this is the honest answer to "which address did the
// remote host see", and the reason a lookup of it names an ISP.
func (w *World) NATAddress(asn int) string {
	switch asn {
	case asNova:
		return "192.0.2.254"
	case asNetCrest:
		return "198.51.100.254"
	}
	return ""
}

// RenumberWAN changes a device's public address the way an ISP lease renewal
// can: the interface, the world's address map and the device's DNS record all
// move together. Nothing about it is decorative — a stale A record really does
// break the name while the address keeps working, which is why the record is
// updated here rather than left to a caller to remember.
func (w *World) RenumberWAN(d *Device, newIP string) error {
	if d == nil || w.IPMap == nil {
		return fmt.Errorf("no such device")
	}
	if !isIPv4(newIP) && !IsV6(newIP) {
		return fmt.Errorf("not an address: %s", newIP)
	}
	old := ""
	for _, i := range d.Ifaces {
		if i.Zone == "wan" && i.IP != "" {
			old = i.IP
			break
		}
	}
	if old == newIP {
		return nil
	}
	if owner, taken := w.IPMap[newIP]; taken && owner != d.ID {
		return fmt.Errorf("%s is already in use", newIP)
	}
	for _, i := range d.Ifaces {
		if i.Zone == "wan" {
			if i.IP == old {
				delete(w.IPMap, i.IP)
				i.IP = newIP
			}
			// v6 addresses keep their prefix; a v4 renumber is a v4 event
		}
	}
	w.IPMap[newIP] = d.ID
	// the device's own name follows its address
	for i := range w.Records {
		if w.IPMap[w.Records[i].IP] == d.ID || w.Records[i].IP == old {
			if strings.HasPrefix(w.Records[i].Name, d.Hostname+".") || w.Records[i].Name == "home.alex.neohome.example" {
				w.Records[i].IP = newIP
			}
		}
	}
	d.Logf("notice", "udhcpc", "lease renewal changed the WAN address to %s (the name record was updated)", newIP)
	w.AddEvent(d.ID, "info", "dhcp", "%s's public address changed to %s", d.Hostname, newIP)
	return nil
}

// ifaceHasV6 reports whether a device holds any address of a family on an
// interface, which is what tells a command apart "no v6 stack" from "no v6
// address".
func (d *Device) ifaceHasV6() bool {
	for _, i := range d.Ifaces {
		if len(i.IP6) > 0 {
			return true
		}
	}
	return false
}

// ASFor returns the AS that announces this device's public IPv4 address, which
// is also the AS whose carrier-grade NAT a shared-address customer uses.
func (d *Device) ASFor() *AS {
	if d == nil || d.W == nil || d.W.WAN == nil {
		return nil
	}
	if as := d.W.WAN.ASFor(wanIP(d)); as != nil {
		return as
	}
	return d.W.WAN.ASFor6(d.FirstWANv6())
}

// SharedWANOf is the carrier-grade-NAT address a shared-IP plan puts on the
// interface: real, in use for outbound traffic, and unreachable from outside.
func (d *Device) SharedWANOf() string {
	for _, i := range d.Ifaces {
		if i.Zone == "wan" && i.SharedIP != "" {
			return i.SharedIP
		}
	}
	return ""
}

// FirstWANv6 is the device's globally routable IPv6 address: the one a remote
// host would see as the source of its packets. A LAN host's global address is
// just as much "its address" as a server's, so interfaces are searched WAN
// first and then LAN — never a link-local or ULA, which cannot leave the house.
func (d *Device) FirstWANv6() string {
	for _, zone := range []string{"wan", "lan", ""} {
		for _, i := range d.Ifaces {
			if (i.Zone == "") != (zone == "") || (zone != "" && i.Zone != zone) {
				continue
			}
			for _, a := range i.IP6 {
				host, _, _ := strings.Cut(a, "/")
				if info := ClassifyAddr(host); info.Kind == KindPublicV6 {
					return host
				}
			}
		}
	}
	return ""
}

// NotAnIPv4 reports whether a destination needs the v6 path in Dial/Reach.
func NotAnIPv4(dst string) bool { return !isIPv4(dst) }

// v6InAny reports whether the address falls inside any of a device's own v6
// on-link prefixes — the v6 answer to "is this on my LAN".
func (d *Device) v6InAny(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, i := range d.Ifaces {
		if !i.Up {
			continue
		}
		for _, cidr := range i.IP6 {
			p, err := netip.ParsePrefix(cidr)
			if err == nil && p.Contains(a) {
				return true
			}
		}
	}
	return false
}

// v6LinkOf returns the /64 prefix of a v6 address, which is the unit v6
// subnets are actually handed out in.
func v6LinkOf(ip string) (netip.Prefix, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, 64), true
}

// ---------------------------------------------------------------------------
// §13 seeding: every device in the world gets a v6 address on the network it is
// actually on. A household's hosts are numbered inside the /64 their router was
// delegated (that is what SLAAC is), public nodes get a /64 of their provider's
// /48, and every interface also carries its own link-local address — the one
// that exists even when nothing is configured.

// seedV6World runs once at world creation, after every device exists, so the
// addresses are consistent with the topology rather than with creation order.
func seedV6World(w *World) {
	if w.WAN == nil {
		return
	}
	// 1. household routers: a delegated prefix per LAN, a transit /64 on the WAN
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Profile != "router" {
			continue
		}
		lanPrefix := v6Delegated(w, d)
		for _, i := range d.Ifaces {
			switch i.Zone {
			case "wan":
				sub, err := netip.ParsePrefix(w.allocV6For("router"))
				if err == nil {
					// the customer's side of the link is ::2; ::1 is the ISP's
					i.IP6 = append(i.IP6, v6Ifaces(v6HostFor(sub, 2), 64))
					i.GW6 = v6HostFor(sub, 1).String()
				}
				i.IP6 = append(i.IP6, "fe80::2")
				// §13's dynamic IP: a household WAN is a DHCP lease, and the
				// lease file is a real file on the router
				if i.Mode == "" {
					i.Mode = "dhcp"
				}
				d.FS.Write("/var/run/udhcpc."+i.Name+".lease",
					"ip="+i.IP+"\nrouter="+i.GW+"\ndns="+PublicResolver+"\nlease=86400\nt0="+
						w.Now().Format("2006-01-02 15:04:05")+"\n", 0644, "root", "root")
			case "lan":
				if i.CIDR == "" {
					continue
				}
				if sub, err := netip.ParsePrefix(lanPrefix); err == nil {
					i.IP6 = append(i.IP6, v6Ifaces(v6HostFor(sub, 1), 64))
				}
				if ula := ULAForLAN(i.CIDR); ula != "" {
					if sub, err := netip.ParsePrefix(ula); err == nil {
						i.IP6 = append(i.IP6, v6Ifaces(v6HostFor(sub, 1), 64))
					}
				}
				i.IP6 = append(i.IP6, "fe80::1")
			}
		}
	}
	// 2. everything else. A device can be on a LAN and on the public internet at
	// once (an infra host has a management interface and a public one), so each
	// interface is numbered for the network it is actually on.
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Profile == "router" || d.ifaceHasV6() {
			continue
		}
		lanPrefix := ""
		if r := w.RouterFor(d); r != nil {
			lanPrefix = v6LANPrefix(r)
		}
		for _, i := range d.Ifaces {
			switch i.Zone {
			case "lan":
				if i.IP == "" {
					continue
				}
				// inside the router's delegated /64 when there is a router, and
				// from this device's own AS when the segment is a management
				// network with no household router on it
				prefix := lanPrefix
				if prefix == "" {
					if p, err := netip.ParsePrefix(w.allocV6For(d.Profile)); err == nil {
						prefix = v6Ifaces(p.Masked().Addr(), 64)
					}
				}
				if host := v6LANAddress(prefix, i.IP); host != "" {
					i.IP6 = append(i.IP6, v6Ifaces(netip.MustParseAddr(host), 64))
				}
				if ula := ULAForLAN(i.CIDR); ula != "" {
					if host := v6LANAddress(ula, i.IP); host != "" {
						i.IP6 = append(i.IP6, v6Ifaces(netip.MustParseAddr(host), 64))
					}
				}
				i.IP6 = append(i.IP6, linkLocal6(i.IP))
			case "wan":
				if cidr := w.allocV6For(d.Profile); cidr != "" {
					i.IP6 = append(i.IP6, cidr)
					if p, err := netip.ParsePrefix(cidr); err == nil {
						i.GW6 = v6HostFor(p, 1).String()
					}
				}
				i.IP6 = append(i.IP6, "fe80::1")
			}
		}
	}

	// 2b. the address map: a v6 destination has to resolve to a device, or the
	// packet path could never carry it
	for _, id := range w.Order {
		for _, i := range w.Devices[id].Ifaces {
			for _, a := range i.IP6 {
				w.AddIP6(a, id)
			}
		}
	}

	// 3. the zone. Every name that already resolves to a device now also has an
	// AAAA: a dual-stack world publishes both families, and `dig -t AAAA` is
	// how a player asks for the other one.
	for _, r := range append([]DNSRecord{}, w.Records...) {
		id := w.IPMap[r.IP]
		if id == "" {
			continue
		}
		if v6 := w.Devices[id].FirstWANv6(); v6 != "" {
			w.Records = append(w.Records, DNSRecord{Name: r.Name, IP: v6})
		}
	}
}

// v6Delegated hands a household its LAN /64 (DHCPv6 prefix delegation, in the
// real world) and remembers it on the LAN interface so hosts can number
// themselves inside it.
func v6Delegated(w *World, router *Device) string {
	for _, i := range router.Ifaces {
		if i.Zone != "lan" {
			continue
		}
		for _, a := range i.IP6 {
			if strings.HasPrefix(a, "2001:db8:") {
				return a
			}
		}
	}
	cidr := w.allocV6For("router")
	return cidr
}

// v6LANPrefix is the /64 a router's LAN hosts number themselves inside.
func v6LANPrefix(router *Device) string {
	if router == nil {
		return ""
	}
	for _, i := range router.Ifaces {
		if i.Zone != "lan" {
			continue
		}
		for _, a := range i.IP6 {
			if strings.HasPrefix(a, "2001:db8:") {
				if p, err := netip.ParsePrefix(a); err == nil {
					return v6Ifaces(p.Masked().Addr(), 64)
				}
			}
		}
	}
	return ""
}

// V6LANPrefixForTest exposes the /64 a router hands its LAN, for tests that
// need to name a host inside it.
func V6LANPrefixForTest(r *Device) string { return v6LANPrefix(r) }

// ---------------------------------------------------------------------------
// Attaching addresses to interfaces (provisioning time)

// AttachSharedWAN numbers a customer behind the provider's carrier-grade NAT.
// The address goes on the interface — that is what the customer sees — and
// deliberately NOT into the world's address map: nobody can route to it, which
// is the difference between "my router has an address" and "the internet can
// reach me".
func (w *World) AttachSharedWAN(d *Device, gw string) string {
	ip := w.allocSharedV4()
	d.NATed = true
	iface := &Iface{Name: "eth0", SharedIP: ip, MAC: macFor(d.ID + "-wan"), Zone: "wan", Up: true, GW: gw}
	d.Ifaces = append(d.Ifaces, iface)
	d.FS.MkdirAll("/var/run", 0755, "root", "root")
	d.FS.Write("/var/run/udhcpc.eth0.lease",
		"ip="+ip+"\nrouter="+gw+"\ndns="+PublicResolver+"\nlease=43200\n", 0644, "root", "root")
	d.Logf("warn", "udhcpc", "leased %s from the provider — carrier-grade NAT, no inbound reachability", ip)
	return ip
}

// AttachWAN6Only gives a node a default route and a public IPv6 address, and no
// IPv4 at all. This is a real plan: the box works, and it cannot talk to an
// IPv4-only host without a translator.
func (d *Device) AttachWAN6Only(gw string) {
	d.Ifaces = append(d.Ifaces, &Iface{Name: "eth0", MAC: macFor(d.ID + "-wan"), Zone: "wan", Up: true, GW: gw})
}

// AddWANv6 puts an allocated /64 on the node's public interface.
func (d *Device) AddWANv6(cidr string) {
	if cidr == "" {
		return
	}
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return
		}
		i.IP6 = append(i.IP6, cidr)
		i.GW6 = v6HostFor(p.Masked(), 1).String()
		i.IP6 = append(i.IP6, "fe80::1")
		if d.W != nil {
			d.W.AddIP6(cidr, d.ID)
		}
		return
	}
}

// AddIP6 registers the host part of a v6 CIDR in the world's address map, which
// is what makes a v6 destination resolvable to a device. §13's ULA and
// link-local addresses are deliberately left out: they are not routable, and a
// map that answered for them would let a packet "arrive" at an address that
// cannot leave the house.
func (w *World) AddIP6(cidr, id string) {
	if w == nil || w.IPMap == nil {
		return
	}
	host, _, _ := strings.Cut(cidr, "/")
	if !strings.HasPrefix(host, "2001:db8:") {
		return
	}
	w.IPMap[host] = id
}

// IsV6Only is true for a public node with a v6 address and no IPv4: the plan was
// bought without one, and that is a fact the packet path has to respect rather
// than paper over.
func (d *Device) IsV6Only() bool {
	hasWAN, has4, has6 := false, false, false
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		hasWAN = true
		if i.IP != "" || i.SharedIP != "" {
			has4 = true
		}
		if len(i.IP6) > 0 {
			has6 = true
		}
	}
	return hasWAN && has6 && !has4
}

// V6NetworkOf renders the on-link prefix of a v6 address the way `ip route`
// prints it: the /64, not the host address.
func V6NetworkOf(cidr string) string {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return ""
	}
	return p.Masked().String()
}

// WANIPOf is the address a device's public interface actually carries, whether
// that is a routable one or a shared (carrier-grade NAT) one. Commands that
// report "your address" must use it, or a CGNAT customer's own address would
// look like it did not exist.
func WANIPOf(d *Device) string {
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		if i.IP != "" {
			return i.IP
		}
		if i.SharedIP != "" {
			return i.SharedIP
		}
	}
	return ""
}

// DNSAnswerForTest exposes the zone's answer for a name (the world's own
// authoritative view), used by tests that need to see a record change.
func DNSAnswerForTest(w *World, name string) string {
	for _, r := range w.Records {
		if strings.EqualFold(r.Name, name) {
			return r.IP + " (" + RecordType(r.IP) + ")"
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// §13 "virtual IP": an address the provider reserves for one node. It is a
// second address on the same interface, not a second interface — and it is
// movable, which is the point: the name keeps working while the machine behind
// it is replaced.

// AttachVirtual gives a node a reserved address and registers it in the world's
// address map, so it is reachable at once and shows up in `ip addr` as a
// secondary address.
func (w *World) AttachVirtual(d *Device, ip string) error {
	if d == nil {
		return fmt.Errorf("no such device")
	}
	info := ClassifyAddr(ip)
	if !info.Public || info.Family != 4 {
		return fmt.Errorf("%s is not a public IPv4 address", ip)
	}
	if owner, taken := w.IPMap[ip]; taken && owner != d.ID {
		return fmt.Errorf("%s is already assigned", ip)
	}
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		for _, have := range i.Extra {
			if have == ip {
				return fmt.Errorf("%s is already assigned to %s", ip, d.Hostname)
			}
		}
		i.Extra = append(i.Extra, ip)
		w.IPMap[ip] = d.ID
		d.Logf("info", "network", "attached virtual address %s", ip)
		w.AddEvent(d.ID, "info", "provider", "reserved address %s now points at %s", ip, d.Hostname)
		return nil
	}
	return fmt.Errorf("%s has no public interface", d.Hostname)
}

// DetachVirtual removes a reserved address, refusing the node's own address:
// you can move a virtual IP, you cannot unplug the machine's identity.
func (w *World) DetachVirtual(d *Device, ip string) error {
	if d == nil {
		return fmt.Errorf("no such device")
	}
	if wanIP(d) == ip {
		return fmt.Errorf("%s is %s's own address, not a reserved one", ip, d.Hostname)
	}
	if !ClassifyAddr(ip).Public {
		return fmt.Errorf("%s is not a public IPv4 address", ip)
	}
	for _, i := range d.Ifaces {
		if i.Zone != "wan" {
			continue
		}
		kept := i.Extra[:0]
		found := false
		for _, have := range i.Extra {
			if have == ip {
				found = true
				continue
			}
			kept = append(kept, have)
		}
		if !found {
			return fmt.Errorf("%s is not assigned to %s", ip, d.Hostname)
		}
		i.Extra = kept
		if w.IPMap[ip] == d.ID {
			delete(w.IPMap, ip)
		}
		d.Logf("info", "network", "detached virtual address %s", ip)
		return nil
	}
	return fmt.Errorf("%s has no public interface", d.Hostname)
}

// VirtualIPs lists the reserved addresses a node holds.
func (d *Device) VirtualIPs() []string {
	var out []string
	for _, i := range d.Ifaces {
		for _, ip := range i.Extra {
			info := ClassifyAddr(ip)
			out = append(out, fmt.Sprintf("%s (public IPv4, %s)", ip, info.Scope))
		}
	}
	return out
}

// RenewWANLease renews the DHCP lease on a device's public interface the way a
// household modem does: the ISP's server answers, the address usually stays,
// and the lease file is rewritten with a fresh timer. Renumbering is the other
// outcome, and it is deliberately a separate call — a world where renewals
// silently moved addresses would make caching bugs impossible to reason about.
func (w *World) RenewWANLease(d *Device) (string, error) {
	if d == nil {
		return "", fmt.Errorf("no such device")
	}
	for _, i := range d.Ifaces {
		if i.Zone != "wan" || i.Mode != "dhcp" {
			continue
		}
		if i.IP == "" {
			return "", fmt.Errorf("%s has no address to renew", i.Name)
		}
		d.FS.Write("/var/run/udhcpc."+i.Name+".lease",
			fmt.Sprintf("ip=%s\nrouter=%s\ndns=%s\nlease=86400\nt0=%s\n",
				i.IP, i.GW, PublicResolver, w.Now().Format("2006-01-02 15:04:05")),
			0644, "root", "root")
		d.Logf("info", "udhcpc", "lease on %s renewed: %s (86400s)", i.Name, i.IP)
		return i.IP, nil
	}
	return "", fmt.Errorf("no DHCP interface to renew")
}
