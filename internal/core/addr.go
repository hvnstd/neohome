package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- the household LAN plan ----
//
// One place owns the numbers. The router's address, the DHCP band it
// really hands out (its dnsmasq.conf renders from these constants), and
// every seeded static come from here; runtime provisioning (the MCP
// character) asks AllocLANStatic instead of carrying a magic address.
// validateLAN runs at boot, so a seed mistake dies loudly in NewWorld and
// in every test instead of surfacing as a mysterious address collision.

const (
	// LANSubnet is the DEFAULT household subnet (house:alex): the numbers the
	// opening scene is seeded with, kept as a constant for backwards
	// compatibility. Other households get their own 10.77.x.0/24 out of
	// AllocHouseholdSubnet — the plan is per-house, and this is house #1.
	LANSubnet = "10.77.1."
	// LANGateway is the default household's router.
	LANGateway = LANSubnet + "1"
	// householdSubnetBase + the 1..254 pool are every household /24 this
	// world can carry, one per third octet. 10.77.1 counts as in use by the
	// default house.
	householdSubnetBase  = "10.77."
	householdSubnetFirst = 1
	householdSubnetLast  = 254
	// LANDHCPFirst / LANDHCPLast are the dnsmasq dhcp-range bounds; every
	// static must stay outside this band or a lease will eventually collide.
	LANDHCPFirst = 50
	LANDHCPLast  = 200
	// PublicResolver is the world's public recursive resolver (dns.isp.example).
	// Nodes on the WAN — provisioned VPSes, VM guests — are configured with it,
	// the way a cloud image ships with a real nameserver.
	PublicResolver = "10.0.0.2"
)

// lanIPIn renders an address on an arbitrary /24 base ("10.77.2." + 11).
func lanIPIn(base string, lastOctet int) string { return fmt.Sprintf("%s%d", base, lastOctet) }

// lanGatewayIn is the router address of an arbitrary household base.
func lanGatewayIn(base string) string { return base + "1" }

// lanLastOctetIn returns the last octet for addresses inside base, -1 for
// anything else.
func lanLastOctetIn(base, ip string) int {
	if base == "" || !strings.HasPrefix(ip, base) {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ip, base))
	if err != nil || n < 0 || n > 255 {
		return -1
	}
	return n
}

// lanIP renders an address on the default household LAN from its last octet.
func lanIP(lastOctet int) string { return lanIPIn(LANSubnet, lastOctet) }

// lanLastOctet returns the last octet for the default household LAN, -1 for
// anything else.
func lanLastOctet(ip string) int { return lanLastOctetIn(LANSubnet, ip) }

// baseOfIP returns the /24 base ("10.77.1.") of an IPv4 address, or "" when
// the address is not IPv4.
func baseOfIP(ip string) string {
	if strings.Count(ip, ".") != 3 {
		return ""
	}
	i := strings.LastIndexByte(ip, '.')
	if i <= 0 {
		return ""
	}
	return ip[:i+1]
}

// LANBase is the /24 base of this device's OWN LAN ("10.77.1." for the
// default house, "10.77.2." for a second family). It is derived from the
// device's interfaces, never from a literal, which is what lets a generated
// or player-founded household render resolv.conf/dnsmasq/exports for itself.
func (d *Device) LANBase() string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.IP != "" {
			if b := baseOfIP(i.IP); b != "" {
				return b
			}
		}
	}
	for _, i := range d.Ifaces {
		if i.Mode == "dhcp" && i.IP != "" {
			if b := baseOfIP(i.IP); b != "" {
				return b
			}
		}
	}
	if c := lanNetOf(d); c != "" {
		if slash := strings.Index(c, "/"); slash > 0 {
			if b := baseOfIP(c[:slash]); b != "" {
				return b
			}
		}
	}
	return LANSubnet
}

// LANGateway is the default gateway of this device's own LAN.
func (d *Device) LANGateway() string { return d.LANBase() + "1" }

// inDHCPPool reports whether a last octet falls inside the DHCP band.
func inDHCPPool(lastOctet int) bool {
	return lastOctet >= LANDHCPFirst && lastOctet <= LANDHCPLast
}

// AllocLANStatic returns the lowest free static address on the DEFAULT
// household LAN.
func (w *World) AllocLANStatic() (string, error) { return w.AllocLANStaticIn(LANSubnet) }

// AllocLANStaticIn returns the lowest free static address on an arbitrary
// household /24 base: outside the DHCP band, not the gateway, and not used
// by any device interface or mapped address. There is no free pick: callers
// that cannot take the answer must not provision.
func (w *World) AllocLANStaticIn(base string) (string, error) {
	used := map[int]bool{}
	take := func(ip string) {
		if o := lanLastOctetIn(base, ip); o > 0 {
			used[o] = true
		}
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		for _, i := range d.Ifaces {
			take(i.IP)
		}
	}
	for ip := range w.IPMap {
		take(ip)
	}
	for o := 2; o <= 254; o++ {
		if inDHCPPool(o) || used[o] {
			continue
		}
		return lanIPIn(base, o), nil
	}
	return "", fmt.Errorf("household LAN %s has no free static address", base)
}

// AllocHouseholdSubnet returns the lowest free household /24 base
// ("10.77.2.") for a family of one's own, collision-checked against every
// address in use (10.77.1. is the default house) and against subnets a
// household already holds even before its boxes are seeded.
func (w *World) AllocHouseholdSubnet() (string, error) {
	taken := func(base string) bool {
		if w.subnetInUse(base) {
			return true
		}
		for _, h := range w.Households {
			if h != nil && h.Subnet == base {
				return true
			}
		}
		return false
	}
	for x := householdSubnetFirst; x <= householdSubnetLast; x++ {
		base := fmt.Sprintf("%s%d.", householdSubnetBase, x)
		if !taken(base) {
			return base, nil
		}
	}
	return "", fmt.Errorf("no free household subnet left (%s%d-%d all taken)",
		householdSubnetBase, householdSubnetFirst, householdSubnetLast)
}

// ValidateLAN fails the boot when the household LAN plan is broken: a
// static inside the DHCP band, two devices sharing one address, or an
// unusable address. Every household /24 is validated independently, using
// the device's own interface, so a second family cannot be skipped. Seed
// mistakes are programming errors — they should panic here, at boot and in
// every test.
func (w *World) ValidateLAN() {
	seen := map[string]string{}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		for _, i := range d.Ifaces {
			if i.IP == "" {
				continue
			}
			base := baseOfIP(i.IP)
			if !strings.HasPrefix(base, householdSubnetBase) {
				continue // another network (WAN, ISP backbone, NPC LAN)
			}
			o := lanLastOctetIn(base, i.IP)
			if o < 0 {
				continue
			}
			if o == 0 || o == 255 {
				panic(fmt.Sprintf("world seed: %s has unusable household address %s", id, i.IP))
			}
			if inDHCPPool(o) {
				panic(fmt.Sprintf("world seed: %s static %s sits inside the DHCP band (%s-%s) — a lease will collide",
					id, i.IP, lanIPIn(base, LANDHCPFirst), lanIPIn(base, LANDHCPLast)))
			}
			if prev, dup := seen[i.IP]; dup {
				panic(fmt.Sprintf("world seed: %s and %s share household address %s", prev, id, i.IP))
			}
			seen[i.IP] = id
		}
	}
}

// ---- the generated streets and the business park (worldgen, §44) ----
//
// Generated content needs its own private ranges, and those numbers belong
// here too: a generator that typed "10.88.x" or "10.89.x" itself would be a
// second owner of the address plan, which is exactly the mistake ValidateLAN
// exists to catch. Every allocator judges collisions against the whole IPMap,
// so a generated subnet can never overlap one that is really in use.

const (
	// npcSubnetBase + npcSubnetFirst..npcSubnetLast are the generated
	// household streets: eight /24s, one per lot. The residential cap is the
	// allocator's real capacity, so no cap can be raised without a street.
	npcSubnetBase  = "10.88."
	npcSubnetFirst = 2
	npcSubnetLast  = 9

	// entSubnetBase is the generated enterprise block. A site is two /24s — a
	// server VLAN and a workstation VLAN, because same-subnet tags would be a
	// lie about L2 — so the allocator steps over even third octets and the odd
	// one is the workstation side. Four sites (0,2,4,6) is the whole park.
	entSubnetBase  = "10.89."
	entSubnetFirst = 0
	entSubnetLast  = 6
)

// subnetInUse reports whether any address in the world falls inside a /24
// base such as "10.88.3.".
func (w *World) subnetInUse(base string) bool {
	for ip := range w.IPMap {
		if strings.HasPrefix(ip, base) {
			return true
		}
	}
	return false
}

// AllocNPCSubnet returns the next free 10.88.x.0/24 base ("10.88.3.") for a
// generated household, validated against every address in use.
func (w *World) AllocNPCSubnet() (string, error) {
	for x := npcSubnetFirst; x <= npcSubnetLast; x++ {
		base := fmt.Sprintf("%s%d.", npcSubnetBase, x)
		if !w.subnetInUse(base) {
			return base, nil
		}
	}
	return "", fmt.Errorf("no free street left (%s%d-%d all taken)", npcSubnetBase, npcSubnetFirst, npcSubnetLast)
}

// AllocEnterpriseSubnet returns the server-VLAN /24 base ("10.89.2.") of the
// lowest free enterprise site, checking both the server and workstation /24s
// against every address in use. The pair is allocated together, so the two
// sides of one site can never overlap.
func (w *World) AllocEnterpriseSubnet() (string, error) {
	for x := entSubnetFirst; x <= entSubnetLast; x += 2 {
		base := fmt.Sprintf("%s%d.", entSubnetBase, x)
		ws := fmt.Sprintf("%s%d.", entSubnetBase, x+1)
		if !w.subnetInUse(base) && !w.subnetInUse(ws) {
			return base, nil
		}
	}
	return "", fmt.Errorf("no free enterprise site left (%s%d-%d all taken)", entSubnetBase, entSubnetFirst, entSubnetLast)
}

// EnterpriseWorkstationSubnet is the workstation VLAN that pairs with a
// server base from AllocEnterpriseSubnet: the next /24 over.
func EnterpriseWorkstationSubnet(base string) string {
	i := strings.LastIndexByte(base, '.')
	if i <= 0 {
		return base
	}
	j := strings.LastIndexByte(base[:i], '.')
	if j < 0 {
		return base
	}
	n, err := strconv.Atoi(base[j+1 : i])
	if err != nil {
		return base
	}
	return fmt.Sprintf("%s%d.", base[:j+1], n+1)
}

// DeviceAddrs lists every address this device answers on: LAN, WAN, loopback.
func DeviceAddrs(d *Device) []string {
	out := []string{"127.0.0.1"}
	for _, i := range d.Ifaces {
		if i.IP != "" {
			out = append(out, i.IP)
		}
	}
	return out
}

// AddrInTarget reports whether addr falls inside target, where target is either
// a bare address ("10.88.1.11") or a CIDR ("10.88.1.0/24"). This is what makes
// `scan 10.88.1.0/24` behave like a real scanner instead of a hostname lookup.
func AddrInTarget(addr, target string) bool {
	if target == "" {
		return false
	}
	if target == addr {
		return true
	}
	slash := strings.Index(target, "/")
	if slash < 0 {
		return false
	}
	base, plenStr := target[:slash], target[slash+1:]
	plen, err := strconv.Atoi(plenStr)
	if err != nil || plen < 0 || plen > 32 {
		return false
	}
	a := parseIPv4(addr)
	n := parseIPv4(base)
	if a < 0 || n < 0 {
		return false
	}
	if plen == 0 {
		return true
	}
	mask := uint32(0xffffffff) << (32 - plen)
	return (uint32(a) & mask) == (uint32(n) & mask)
}

func parseIPv4(s string) int {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return -1
	}
	v := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return -1
		}
		v = v<<8 | n
	}
	return v
}
