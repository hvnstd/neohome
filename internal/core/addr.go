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
	// LANSubnet is the household subnet all these numbers live in.
	LANSubnet = "10.77.1."
	// LANGateway is the household router.
	LANGateway = LANSubnet + "1"
	// LANDHCPFirst / LANDHCPLast are the dnsmasq dhcp-range bounds; every
	// static must stay outside this band or a lease will eventually collide.
	LANDHCPFirst = 50
	LANDHCPLast  = 200
)

// lanIP renders a household address from its last octet.
func lanIP(lastOctet int) string { return fmt.Sprintf("%s%d", LANSubnet, lastOctet) }

// lanLastOctet returns the last octet for household-LAN addresses, -1 for
// anything else (WAN addresses, the ISP backbone, the NPC LAN).
func lanLastOctet(ip string) int {
	if !strings.HasPrefix(ip, LANSubnet) {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ip, LANSubnet))
	if err != nil || n < 0 || n > 255 {
		return -1
	}
	return n
}

// inDHCPPool reports whether a last octet falls inside the DHCP band.
func inDHCPPool(lastOctet int) bool {
	return lastOctet >= LANDHCPFirst && lastOctet <= LANDHCPLast
}

// AllocLANStatic returns the lowest free static address on the household
// LAN: outside the DHCP band, not the gateway, and not used by any device
// interface or mapped address. There is no free pick: callers that cannot
// take the answer must not provision.
func (w *World) AllocLANStatic() (string, error) {
	used := map[int]bool{}
	take := func(ip string) {
		if o := lanLastOctet(ip); o > 0 {
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
		return lanIP(o), nil
	}
	return "", fmt.Errorf("household LAN has no free static address")
}

// ValidateLAN fails the boot when the household LAN plan is broken: a
// static inside the DHCP band, two devices sharing one address, or an
// unusable address. Seed mistakes are programming errors — they should
// panic here, at boot and in every test.
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
			o := lanLastOctet(i.IP)
			if o < 0 {
				continue // another network (WAN, ISP backbone, NPC LAN)
			}
			if o == 0 || o == 255 {
				panic(fmt.Sprintf("world seed: %s has unusable household address %s", id, i.IP))
			}
			if inDHCPPool(o) {
				panic(fmt.Sprintf("world seed: %s static %s sits inside the DHCP band (%s-%s) — a lease will collide",
					id, i.IP, lanIP(LANDHCPFirst), lanIP(LANDHCPLast)))
			}
			if prev, dup := seen[i.IP]; dup {
				panic(fmt.Sprintf("world seed: %s and %s share household address %s", prev, id, i.IP))
			}
			seen[i.IP] = id
		}
	}
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
