package core

import (
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// VLAN segmentation (§31) — the enterprise, divided
//
// The office is not one flat /24: servers live on VLAN 10, workstations on
// VLAN 20, and the gateway routes between them only where its configuration
// says to. Like every other policy in this world the rules are a file the
// player reads and edits (`/etc/config/network` on the gateway, re-parsed
// on every packet — never cached), while membership is topology (Iface.VLAN,
// set where the wire is plugged, not editable from a shell).
//
// What this changes in the packet path: two tagged devices on different
// VLANs are not on-link with each other even inside RFC1918 space, so the
// usual LAN shortcut does not apply. Allowed traffic is routed by the
// gateway and arrives on the target's LAN side (tagged) — the target's own
// lan-scope services and lan accepts still judge it. Anything else between
// tagged VLANs is filtered at the gateway, with the reason naming both
// VLANs. Untagged devices (0, i.e. the whole rest of the world) bypass all
// of this, and so does the gateway's own traffic.
// ---------------------------------------------------------------------------

// VLANRule is one inter-VLAN allow: src VLAN may reach dest VLAN on
// proto/port. Everything else between tagged VLANs is denied.
type VLANRule struct {
	Name  string
	Src   int
	Dest  int
	Proto string // tcp | udp | all
	Port  int    // 0 means every port
}

// vlanRules reads the gateway's inter-VLAN policy from its own
// /etc/config/network. Only `allow` sections are read — interface and
// switch_vlan stanzas belong to other readers, the way one UCI file serves
// several tools on real OpenWrt.
func vlanRules(gw *Device) []VLANRule {
	var out []VLANRule
	if gw == nil || gw.FS == nil {
		return out
	}
	f, _, ok := gw.ReadUCIFile("network")
	if !ok {
		return out
	}
	for _, s := range f.SectionsOf("allow") {
		src, err1 := strconv.Atoi(strings.TrimSpace(s.Get("src")))
		dst, err2 := strconv.Atoi(strings.TrimSpace(s.Get("dest")))
		if err1 != nil || err2 != nil {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(s.Get("proto")))
		if proto == "" {
			proto = "tcp"
		}
		port := 0
		if p := strings.TrimSpace(s.Get("dest_port")); p != "" {
			port, _ = strconv.Atoi(p)
		}
		out = append(out, VLANRule{Name: s.Name, Src: src, Dest: dst, Proto: proto, Port: port})
	}
	return out
}

// ifaceVLAN reports the VLAN tag of the interface holding ip on d, or 0 when
// the address sits on an untagged interface (or nowhere).
func ifaceVLAN(d *Device, ip string) int {
	if d == nil {
		return 0
	}
	for _, i := range d.Ifaces {
		if i.IP == ip {
			return i.VLAN
		}
	}
	return 0
}

// srcVLAN is the tag the source sends with: its first UP LAN interface's
// tag, or 0 for an untagged sender.
func srcVLAN(src *Device) int {
	if src == nil {
		return 0
	}
	for _, i := range src.Ifaces {
		if i.Up && i.Zone == "lan" {
			return i.VLAN
		}
	}
	return 0
}

// vlanGateway finds the router between two VLANs: a powered device other
// than the endpoints with UP interfaces in both tags.
func vlanGateway(w *World, src, dst *Device, sv, dv int) *Device {
	if w == nil {
		return nil
	}
	for _, id := range w.Order {
		g := w.Devices[id]
		if g == nil || g == src || g == dst || !g.Powered() {
			continue
		}
		sawS, sawD := false, false
		for _, i := range g.Ifaces {
			if !i.Up {
				continue
			}
			if i.VLAN == sv {
				sawS = true
			}
			if i.VLAN == dv {
				sawD = true
			}
		}
		if sawS && sawD {
			return g
		}
	}
	return nil
}

// vlanVerdict judges one packet between tagged VLANs. applies is false for
// everything the old path already handles (untagged either end, same tag,
// or the gateway's own traffic). Otherwise gw is the router that decides
// (possibly down), and allow names whether its config opened the door.
// proto is tcp for connections, icmp for pings: a rule matches on equality
// or all, so a TCP-only policy still answers pings with "denied" honestly.
func vlanVerdict(w *World, src, dst *Device, dstIP string, port int, proto string) (applies, allow bool, gw *Device, sv, dv int) {
	sv = srcVLAN(src)
	dv = ifaceVLAN(dst, dstIP)
	if sv == 0 || dv == 0 || sv == dv {
		return false, true, nil, sv, dv
	}
	// the gateway's own traffic bypasses its rules, like any router's
	if gatewayHas(src, sv, dv) || gatewayHas(dst, sv, dv) {
		return false, true, nil, sv, dv
	}
	gw = vlanGateway(w, src, dst, sv, dv)
	if gw == nil {
		// name the down gateway when there is one: "not routed" and
		// "router is dark" are different diagnoses with different fixes
		if w != nil {
			for _, id := range w.Order {
				g := w.Devices[id]
				if g == nil || g == src || g == dst {
					continue
				}
				if hasBothTags(g, sv, dv) {
					return true, false, g, sv, dv
				}
			}
		}
		return true, false, nil, sv, dv
	}
	for _, r := range vlanRules(gw) {
		if r.Src == sv && r.Dest == dv && (r.Proto == "all" || r.Proto == proto) && (r.Port == 0 || r.Port == port) {
			return true, true, gw, sv, dv
		}
	}
	if st := gw.FW(); st.LogDrops {
		gw.Logf("info", "firewall", "DROP vlan %d -> %d tcp/%d from %s (no allow rule)", sv, dv, port, src.Hostname)
	}
	return true, false, gw, sv, dv
}

// hasBothTags reports whether d carries UP interfaces in both VLANs,
// powered or not (the verdict uses it to tell "no router" from "dark router").
func hasBothTags(d *Device, sv, dv int) bool {
	if d == nil {
		return false
	}
	sawS, sawD := false, false
	for _, i := range d.Ifaces {
		if !i.Up {
			continue
		}
		if i.VLAN == sv {
			sawS = true
		}
		if i.VLAN == dv {
			sawD = true
		}
	}
	return sawS && sawD
}

// gatewayHas reports whether d itself routes between the two tags (used to
// exempt the gateway's own traffic from its rules).
func gatewayHas(d *Device, sv, dv int) bool {
	if d == nil || !d.Powered() {
		return false
	}
	sawS, sawD := false, false
	for _, i := range d.Ifaces {
		if !i.Up {
			continue
		}
		if i.VLAN == sv {
			sawS = true
		}
		if i.VLAN == dv {
			sawD = true
		}
	}
	return sawS && sawD
}
