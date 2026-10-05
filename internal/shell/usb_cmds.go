package shell

// usb — the pocket storage. A stick is a real device with no network:
// `usb plug` is a physical act that gives the host a block device node,
// `mount -t vfat` makes its filesystem usable, and everything on the stick
// travels with it to the next machine. That is the air gap, as a tool.

import (
	"fmt"
	"sort"

	"neohome/internal/core"
)

func init() {
	builtinTable["usb"] = cmdUsb
}

func cmdUsb(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		return usbList(s)
	case "plug":
		if len(args) < 2 {
			s.errf("usage: usb plug STICK")
			return 1
		}
		if err := s.W.USBPlug(s.Dev, args[1], s.User.Name); err != nil {
			s.errf("usb: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "attached: storage device at %s\n  mount it: mount -t vfat %s /mnt/usb\n",
			core.USBDeviceNode, core.USBDeviceNode)
		return 0
	case "unplug", "eject":
		stickID := ""
		if len(args) > 1 {
			stickID = args[1]
		} else {
			// one port, one stick: the node names it
			stick, err := s.W.USBStickFromNode(s.Dev, core.USBDeviceNode)
			if err != nil {
				s.errf("usb: %v", err)
				return 1
			}
			stickID = stick.ID
		}
		if err := s.W.USBUnplug(s.Dev, stickID, s.User.Name); err != nil {
			s.errf("usb: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, "detached — the stick keeps its files")
		return 0
	}
	s.errf("usage: usb [list|plug STICK|unplug]")
	return 1
}

func usbList(s *Shell) int {
	var ids []string
	for id, d := range s.W.Devices {
		if d.Profile == "usb" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(s.Out, "no usb storage in the world")
		return 0
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := s.W.Devices[id]
		where := s.W.AttachedTo(id)
		if where == "" {
			fmt.Fprintf(s.Out, "%-16s %-12s unattached (in a drawer somewhere)\n", id, d.Hostname)
			continue
		}
		host := s.W.Devices[where]
		name := where
		node := ""
		if host != nil {
			name = host.Hostname
			if where == s.Dev.ID {
				node = " — device node " + core.USBDeviceNode
			}
		}
		fmt.Fprintf(s.Out, "%-16s %-12s attached to %s%s\n", id, d.Hostname, name, node)
	}
	return 0
}
