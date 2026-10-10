package shell

import (
	"fmt"
	"sort"
	"strings"

	"neohome/internal/core"
)

// topo renders the household's physical topology as a text diagram. Links
// are L2 state (Device.Uplink/UplinkPort and switch ports, read through
// core.LinkParent), so the map agrees with the packet path and changes when
// the physical world does — including which switch port went admin-down.

func init() {
	builtinTable["topo"] = cmdTopo
	builtinTable["map"] = cmdTopo
}

func cmdTopo(s *Shell, args []string) int {
	// the diagram is centred on the router this machine is behind: the
	// household's own picture, never the whole internet
	root := s.Dev.W.RouterFor(s.Dev)
	if root == nil {
		for _, id := range s.W.Order {
			if r := s.W.Devices[id]; r != nil && r.Profile == "router" {
				root = r
				break
			}
		}
	}
	if root == nil {
		s.errf("topo: no router in this world to draw")
		return 1
	}
	fmt.Fprintf(s.Out, "%s\n", root.Hostname)
	// walk the cable tree; a switch then prints its own port table so the
	// whole island's state is visible at a glance
	seen := map[string]bool{}
	var walk func(parentID string)
	walk = func(parentID string) {
		var kids []*core.Device
		for _, id := range s.W.Order {
			c := s.W.Devices[id]
			if c == nil || c.Profile == "usb" || seen[c.ID] {
				continue
			}
			if p, _ := s.W.LinkParent(c); p == parentID {
				kids = append(kids, c)
			}
		}
		sort.Slice(kids, func(i, j int) bool { return kids[i].Hostname < kids[j].Hostname })
		for _, c := range kids {
			seen[c.ID] = true
			_, port := s.W.LinkParent(c)
			via := ""
			if port > 0 {
				via = fmt.Sprintf("  (port %d)", port)
			}
			dark := ""
			if !c.Powered() {
				dark = "  [dark]"
			}
			fmt.Fprintf(s.Out, "  ├─ %s%s%s\n", c.Hostname, via, dark)
			walk(c.ID)
		}
	}
	walk(root.ID)
	// the port table of every switch whose uplink lands here
	for _, id := range s.W.Order {
		c := s.W.Devices[id]
		if c == nil || c.Switch == nil {
			continue
		}
		if p, _ := s.W.LinkParent(c); p != root.ID {
			continue
		}
		fmt.Fprintf(s.Out, "\n%s ports:\n", c.Hostname)
		for _, p := range c.Switch.Ports {
			who := p.Label
			if p.Peer != "" {
				if pd := s.W.Devices[p.Peer]; pd != nil {
					who = pd.Hostname
				}
			}
			poe := ""
			if p.PoE {
				poe = " PoE"
			}
			state := "up"
			if !p.Admin {
				state = "admin-down"
			}
			fmt.Fprintf(s.Out, "  port %d %-10s %-11s %dM%s\n", p.Num, who, state, p.Speed, poe)
		}
	}
	return 0
}

var _ = strings.Join
