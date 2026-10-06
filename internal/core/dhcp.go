package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// DHCP
//
// A lease is real state on both ends: the router records who holds which
// address, and the client's interface only works while it holds one. Both ends
// are required — if either the server or the lease is missing, the device has no
// default route, which is exactly the failure a player is meant to be able to
// diagnose.
//
// The pool comes from the router's own dnsmasq.conf, so editing that file really
// changes which addresses can be handed out.
// ---------------------------------------------------------------------------

// dhcpPool reads the allocatable range out of the router's config. A router with
// no dhcp-range configured is not a DHCP server, whatever its service list says.
func dhcpPool(r *Device) (lo, hi int, subnet string, ok bool) {
	data, exists := r.FS.Read("/etc/dnsmasq.conf")
	if !exists {
		return 0, 0, "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "dhcp-range=") {
			continue
		}
		spec := strings.TrimPrefix(line, "dhcp-range=")
		parts := strings.Split(spec, ",")
		if len(parts) < 2 {
			continue
		}
		a, n1 := parseIPv4Octet(parts[0])
		b, n2 := parseIPv4Octet(parts[1])
		if !n1 || !n2 {
			continue
		}
		if len(parts) >= 3 && parts[2] != "" {
			subnet = parts[2]
		}
		return a, b, subnet, true
	}
	return 0, 0, "", false
}

// RouterFor exposes the LAN router serving a device, for the DHCP commands.
func (w *World) RouterFor(d *Device) *Device { return w.routerFor(d) }

// DHCPPoolInfo exposes the configured pool for display.
func DHCPPoolInfo(r *Device) (lo, hi int, mask string, ok bool) {
	lo, hi, mask, ok = dhcpPool(r)
	return
}

// LANPrefix exposes the router's LAN network prefix for display.
func LANPrefix(r *Device) string { return lanPrefix(r) }

// parseIPv4Octet returns the last octet of an address.
func parseIPv4Octet(s string) (int, bool) {
	s = strings.TrimSpace(s)
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n < 0 || n > 255 {
		return 0, false
	}
	return n, true
}

// lanPrefix returns the first three octets of the router's LAN address.
func lanPrefix(r *Device) string {
	for _, i := range r.Ifaces {
		if i.Zone == "lan" && i.IP != "" {
			if j := strings.LastIndex(i.IP, "."); j > 0 {
				return i.IP[:j]
			}
		}
	}
	return ""
}

// DHCPServerRunning reports whether a router is actually able to answer DHCP:
// the daemon must be up and it must have a pool configured.
func (r *Device) DHCPServerRunning() bool {
	if !r.Powered() {
		return false
	}
	svc := r.Svc("dnsmasq")
	if svc == nil || svc.State != "running" {
		return false
	}
	_, _, _, ok := dhcpPool(r)
	return ok
}

// DHCPLease hands out an address to a client. This is the server side: it really
// records the lease in the router's table, and it refuses when the daemon is
// down or the pool is full — which is what makes "restart dnsmasq" a fix rather
// than a ritual.
func (r *Device) DHCPLease(client *Device, mac string) (*Lease, error) {
	if !r.DHCPServerRunning() {
		return nil, fmt.Errorf("no DHCP service available from %s", r.Hostname)
	}
	lo, hi, mask, _ := dhcpPool(r)
	prefix := lanPrefix(r)
	if prefix == "" {
		return nil, fmt.Errorf("router has no LAN address to serve from")
	}
	if mask == "" {
		mask = "255.255.255.0"
	}
	if r.DHCPL == nil {
		r.DHCPL = map[string]Lease{}
	}

	// a returning client keeps its address — real DHCP is sticky
	for m, l := range r.DHCPL {
		if strings.EqualFold(m, mac) {
			gw := r.FirstLANIP()
			dns := gw
			if svc := r.Svc("dnsmasq"); svc != nil {
				dns = gw
			}
			return &Lease{MAC: m, IP: l.IP, GW: gw, DNS: dns}, nil
		}
	}

	taken := map[string]bool{}
	for _, l := range r.DHCPL {
		taken[l.IP] = true
	}
	for n := lo; n <= hi; n++ {
		ip := fmt.Sprintf("%s.%d", prefix, n)
		if taken[ip] {
			continue
		}
		// never hand out an address that is already statically in use
		if _, used := r.W.IPMap[ip]; used {
			continue
		}
		gw := r.FirstLANIP()
		l := Lease{MAC: mac, IP: ip, GW: gw, DNS: gw}
		r.DHCPL[mac] = l
		r.W.IPMap[ip] = client.ID
		r.Logf("info", "dnsmasq", "DHCPACK %s %s to %s", ip, mac, client.Hostname)
		return &l, nil
	}
	return nil, fmt.Errorf("DHCP pool exhausted (%s.%d-%d)", prefix, lo, hi)
}

// DHCPRelease gives an address back when a lease ends or a client renews
// elsewhere. The address stops being routed to the device, which is what makes
// losing a lease a genuine loss of connectivity.
func (r *Device) DHCPRelease(mac string) {
	if r.DHCPL == nil {
		return
	}
	l, ok := r.DHCPL[mac]
	if !ok {
		return
	}
	if r.W.IPMap[l.IP] != "" {
		// only unbind if nothing else has claimed it since
		delete(r.W.IPMap, l.IP)
	}
	delete(r.DHCPL, mac)
	r.Logf("info", "dnsmasq", "DHCPRELEASE %s (%s)", l.IP, mac)
}

// ApplyLease puts a lease onto the client's interface: address, gateway and the
// resolver that came with it. This is the client side of DHCP.
func (d *Device) ApplyLease(l *Lease) {
	for _, i := range d.Ifaces {
		if i.Zone != "lan" && i.Mode != "dhcp" {
			continue
		}
		if i.IP != "" && i.IP != l.IP && d.W.IPMap[i.IP] == d.ID {
			delete(d.W.IPMap, i.IP)
		}
		i.IP = l.IP
		i.GW = l.GW
		i.Mode = "dhcp"
		i.Up = true
		d.W.IPMap[l.IP] = d.ID
	}
	// The DNS server that came with the lease is what the resolver already
	// consults for a dhcp interface, so applying the address is enough — the
	// client really does start resolving through the router it leased from.
}

// DHCPRenew is what `udhcpc` actually does: ask the router for an address, and
// take it. Without a reachable server the client keeps its old address and has
// no route, which is the failure mode the player has to notice.
func (d *Device) DHCPRenew() (*Lease, error) {
	r := d.W.routerFor(d)
	if r == nil {
		return nil, fmt.Errorf("no DHCP server reachable")
	}
	if !r.Powered() {
		return nil, fmt.Errorf("no DHCP server reachable (%s is down)", r.Hostname)
	}
	mac := ""
	for _, i := range d.Ifaces {
		if i.MAC != "" && i.Zone == "lan" && i.Mode == "dhcp" {
			mac = i.MAC
		}
	}
	if mac == "" {
		return nil, fmt.Errorf("no interface to lease an address for")
	}
	l, err := r.DHCPLease(d, mac)
	if err != nil {
		return nil, err
	}
	d.ApplyLease(l)
	d.Logf("info", "udhcpc", "lease obtained: %s via %s", l.IP, l.GW)
	d.W.AddEvent(d.ID, "info", "dhcp", "%s renewed its lease: %s", d.Hostname, l.IP)
	return l, nil
}
