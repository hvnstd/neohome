package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// udhcpc — the DHCP client. BusyBox's client, which is what an OpenWrt router or
// a minimal Linux box actually runs.
//
// This is a real client: it asks the router for an address and takes it. When
// the server is down or the pool is empty there is no address and no default
// route, and the player can see exactly that.
func init() {
	builtinTable["udhcpc"] = cmdUDHCPC
	builtinTable["leases"] = cmdLeases
}

func cmdUDHCPC(s *Shell, args []string) int {
	iface := ""
	quiet := false
	release := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i" && i+1 < len(args):
			iface = args[i+1]
			i++
		case strings.HasPrefix(a, "-i") && len(a) > 2:
			iface = a[2:]
		case a == "-q":
			quiet = true
		case a == "-R":
			release = true
		case a == "-n" || a == "-b" || a == "-f":
			// -n exit-if-no-lease, -b background, -f foreground: accepted
		}
	}

	d := s.Dev
	if iface != "" {
		found := false
		for _, i := range d.Ifaces {
			if i.Name == iface {
				found = true
			}
		}
		if !found {
			s.errf("udhcpc: no such interface: %s", iface)
			return 1
		}
	}
	if d.IsDataCenter() {
		s.errf("udhcpc: %s has a provisioned address from its provider, not DHCP", d.Hostname)
		return 1
	}

	r := s.W.RouterFor(d)
	if r == nil {
		s.errf("udhcpc: no DHCP server found")
		return 1
	}

	if release {
		mac := ""
		for _, i := range d.Ifaces {
			if i.MAC != "" && (i.Zone == "lan" || i.Mode == "dhcp") {
				mac = i.MAC
			}
		}
		r.DHCPRelease(mac)
		for _, i := range d.Ifaces {
			if i.Zone == "lan" && i.Mode == "dhcp" {
				if d.W.IPMap[i.IP] == d.ID {
					delete(d.W.IPMap, i.IP)
				}
				i.IP = ""
				i.GW = ""
			}
		}
		fmt.Fprintln(s.Out, "udhcpc: lease released")
		return 0
	}

	l, err := d.DHCPRenew()
	if err != nil {
		// the classic failure: the server is not answering and the box has no address
		if !quiet {
			s.errf("udhcpc: %v", err)
			s.errf("udhcpc: no lease, interface left without an address")
		}
		return 1
	}
	if !quiet {
		fmt.Fprintf(s.Out, "udhcpc: leased %s for %s\n", l.IP, ifaceName(d))
		fmt.Fprintf(s.Out, "udhcpc: router %s\n", l.GW)
		fmt.Fprintf(s.Out, "udhcpc: dns %s\n", l.DNS)
		fmt.Fprintf(s.Out, "udhcpc: lease of %s obtained, lease time 43200\n", l.IP)
	}
	return 0
}

func ifaceName(d *core.Device) string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" || i.Mode == "dhcp" {
			return i.Name
		}
	}
	return "eth0"
}

// leases — the server's own view of who holds which address.
func cmdLeases(s *Shell, args []string) int {
	r := s.Dev
	if s.Dev.Profile != "router" {
		if rr := s.W.RouterFor(s.Dev); rr != nil {
			r = rr
		}
	}
	if r.DHCPL == nil || len(r.DHCPL) == 0 {
		fmt.Fprintf(s.Out, "no active leases on %s\n", r.Hostname)
		return 0
	}
	fmt.Fprintf(s.Out, "leases on %s:\n", r.Hostname)
	fmt.Fprintf(s.Out, "%-20s %-16s %-16s\n", "MAC", "IP", "HOST")
	names := map[string]string{}
	for _, id := range s.W.Order {
		dev := s.W.Devices[id]
		for _, i := range dev.Ifaces {
			names[i.MAC] = dev.Hostname
		}
	}
	type row struct{ mac, ip, host string }
	var rows []row
	for mac, l := range r.DHCPL {
		rows = append(rows, row{mac, l.IP, names[mac]})
	}
	// stable order
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].ip < rows[i].ip {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	for _, x := range rows {
		fmt.Fprintf(s.Out, "%-20s %-16s %-16s\n", x.mac, x.ip, x.host)
	}
	lo, hi, mask, ok := core.DHCPPoolInfo(r)
	if ok {
		fmt.Fprintf(s.Out, "\npool: %s.%d-%d (%s), %d in use\n",
			core.LANPrefix(r), lo, hi, mask, len(r.DHCPL))
	} else {
		fmt.Fprintln(s.Out, "\nno dhcp-range configured — this router is not serving leases")
	}
	return 0
}
