package core

import (
	"strconv"
	"strings"
)

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
