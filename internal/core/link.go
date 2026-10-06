package core

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// §15 家庭设备: the wire between the devices
//
// The spec's example is a chain:
//
//	Camera → PoE Switch → Router → NAS
//
// and it lists what each failure means: a switch that goes down takes what is
// plugged into it offline, a camera whose PoE port is cut loses power (not just
// packets), and a NAS that stops takes shares, backups and albums with it.
//
// That chain is not decoration here. `Uplink` names the physical port a device
// is plugged into, and every packet path (`Reach`, `Dial`) asks `linkUp` first.
// A device behind a dark switch is unreachable because there is no wire, not
// because the game decided to hide it — it keeps its filesystem, its services
// and its uptime, and the player can see exactly that through the console when
// the link comes back.
// ---------------------------------------------------------------------------

// SwitchPort is one physical port of a managed switch. Everything a player can
// change about it is a field here, and everything the rest of the world sees
// (link state, PoE power) is derived from those fields.
type SwitchPort struct {
	Num    int
	Label  string // what is plugged in: "uplink", "cam-front", "patch-3"
	Peer   string // device ID attached to this port ("" = free)
	Admin  bool   // administratively up (a human turned it off)
	PoE    bool   // power over ethernet enabled
	Uplink bool   // this port carries the switch's own traffic upstream
	// Speed is the negotiated link rate. A 100 Mbit port is enough for a
	// camera and not for a NAS, which is a real reason to look at the report.
	Speed int
}

// SwitchState is a switch's own configuration and counters.
type SwitchState struct {
	Ports   []SwitchPort
	BudgetW float64 // the PoE power budget the power supply can deliver
	// Dropped counts frames the switch threw away because the PoE budget was
	// exhausted: the honest symptom of an over-subscribed switch.
	Dropped int
}

// LaptopBattery is a laptop's battery: a machine that keeps working for a while
// when the plug is pulled, and then stops.
type LaptopBattery struct {
	Pct      int
	Charging bool
	LidOpen  bool
}

// PrintJob is one document in the printer's spool. A job is a real queued
// object: it survives a power cycle on disk.
type PrintJob struct {
	ID     int
	Owner  string
	Title  string
	Pages  int
	State  string // queued | printing | done | held
	Reason string
}

// PrinterState is a printer's real state: what it has queued, how much paper is
// left and how much toner. The queue is persisted in the printer's own
// filesystem by the spooler, so both survive a reload.
type PrinterState struct {
	Queue   []PrintJob
	Paper   int // sheets left
	Toner   int // percent
	Printed int // sheets printed since it was installed
	Offline bool
	NextID  int // the spooler's job counter, persisted with the queue
}

// IsSwitch reports whether a device is a network switch.
func (d *Device) IsSwitch() bool { return d.Profile == "switch" }

// SwitchOf returns the switch a device is plugged into, and the port. A device
// with no uplink is not behind one.
func (w *World) SwitchOf(d *Device) (*Device, *SwitchPort) {
	if d == nil || d.Uplink == "" {
		return nil, nil
	}
	sw := w.Devices[d.Uplink]
	if sw == nil || sw.Switch == nil {
		return nil, nil
	}
	for i := range sw.Switch.Ports {
		if sw.Switch.Ports[i].Peer == d.ID {
			return sw, &sw.Switch.Ports[i]
		}
	}
	return sw, nil
}

// linkUp answers whether a device has a wire to the network.
func (w *World) linkUp(d *Device) (bool, string) { return w.linkUpVia(d, true) }

// linkUpVia walks the physical path between a device and the network. Every hop
// is checked against real state: the switch has power, the port is up, and — for
// traffic that has to leave the switch's island — the switch's own uplink is
// alive. That distinction is real: two devices on a switch can still talk to
// each other while the router in front is dark, which is why a household's
// cameras keep streaming during an outage but nothing reaches the internet.
func (w *World) linkUpVia(d *Device, crossUplink bool) (bool, string) {
	seen := map[string]bool{}
	cur := d
	for cur != nil && cur.Uplink != "" {
		if seen[cur.ID] {
			return false, "a loop in the cabling"
		}
		seen[cur.ID] = true
		sw := w.Devices[cur.Uplink]
		if sw == nil {
			return false, fmt.Sprintf("%s is plugged into %s, which is not in this world", cur.Hostname, cur.Uplink)
		}
		if !sw.Powered() {
			return false, fmt.Sprintf("switch %s has no power", sw.Hostname)
		}
		if sw.Switch == nil {
			// the upstream device is not a switch (a router, say): the wire is
			// only as good as that device's own power, checked above
			return true, ""
		}
		LoadSwitchConfig(sw) // the file is the configuration, as everywhere else
		var port *SwitchPort
		for i := range sw.Switch.Ports {
			if sw.Switch.Ports[i].Peer == cur.ID {
				port = &sw.Switch.Ports[i]
			}
		}
		if port == nil {
			return false, fmt.Sprintf("%s is plugged into %s but no port has it", cur.Hostname, sw.Hostname)
		}
		if !port.Admin {
			return false, fmt.Sprintf("port %d (%s) on %s is down", port.Num, port.Label, sw.Hostname)
		}
		// traffic that has to leave the switch's island needs the uplink: a
		// switch whose uplink died is an island, and its own devices can still
		// reach each other inside it
		for _, up := range sw.Switch.Ports {
			if !up.Uplink || !crossUplink {
				continue
			}
			if !up.Admin {
				return false, fmt.Sprintf("the uplink port on %s is down", sw.Hostname)
			}
			if peer := w.Devices[up.Peer]; peer != nil && !peer.Powered() {
				return false, fmt.Sprintf("the uplink to %s is dead (%s has no power)", peer.Hostname, peer.Hostname)
			}
		}
		cur = sw
	}
	return true, ""
}

// LinkUp is the exported question the shell and tests ask.
func (w *World) LinkUp(d *Device) (bool, string) { return w.linkUp(d) }

// PoESuppliedW is what the switch is delivering right now: the sum of the
// ports whose PoE is on and whose peer is actually drawing power.
func (sw *Device) PoESuppliedW() float64 {
	if sw.Switch == nil {
		return 0
	}
	total := 0.0
	for _, p := range sw.Switch.Ports {
		if !p.PoE || p.Peer == "" {
			continue
		}
		peer := sw.W.Devices[p.Peer]
		if peer == nil || !peer.Powered() {
			continue
		}
		total += peer.PoEDrawW()
	}
	return total
}

// PoEDrawW is what a PoE-powered device asks the switch for. It is a property
// of the device's hardware, which is why an access point and a door camera draw
// different amounts from the same budget.
func (d *Device) PoEDrawW() float64 {
	if !d.PoEPowered {
		return 0
	}
	w := 3.0 + float64(d.HW.Cores)*1.2
	if d.Services != nil {
		if _, ok := d.Services["rtsp"]; ok {
			w += 4.0 // a camera with a stream running is the hungry one
		}
		if _, ok := d.Services["wifi"]; ok {
			w += 6.0 // a radio is the other hungry one
		}
	}
	return w
}

// PoEExceeded reports whether the switch is being asked for more power than its
// supply can deliver. A real switch refuses the *new* port rather than dropping
// power to everything, which is what `SetPortPoE` does.
func (sw *Device) PoEExceeded() bool {
	return sw.Switch != nil && sw.PoESuppliedW() > sw.Switch.BudgetW
}

// poePowered answers whether a PoE device is being powered right now: the
// switch must have power, the port must have PoE on, and the budget must not
// already be blown.
func (w *World) poePowered(d *Device) bool {
	sw, port := w.SwitchOf(d)
	if sw == nil || port == nil {
		return false
	}
	if !sw.Powered() || !port.Admin || !port.PoE {
		return false
	}
	// the budget is spent in port order, as a real switch does: the ports that
	// were up first keep their power
	draw := 0.0
	for _, p := range sw.Switch.Ports {
		if !p.PoE || p.Peer == "" {
			continue
		}
		peer := w.Devices[p.Peer]
		if peer == nil {
			continue
		}
		draw += peer.PoEDrawW()
		if draw > sw.Switch.BudgetW && peer.ID == d.ID {
			return false
		}
	}
	return true
}

// SetPortAdmin brings a switch port up or down. A port that is down carries no
// traffic, and a PoE port that is down carries no power either — which is how a
// player reboots a frozen camera without walking outside.
func (sw *Device) SetPortAdmin(num int, up bool) error {
	if sw.Switch == nil {
		return fmt.Errorf("%s is not a managed switch", sw.Hostname)
	}
	for i := range sw.Switch.Ports {
		p := &sw.Switch.Ports[i]
		if p.Num != num {
			continue
		}
		if p.Uplink && !up {
			return fmt.Errorf("port %d carries the uplink to %s — cutting it would take the whole switch off the network", num, sw.Uplink)
		}
		if p.Admin == up {
			return nil
		}
		p.Admin = up
		state := "down"
		if up {
			state = "up"
		}
		sw.Logf("info", "switchd", "port %d (%s) admin %s", p.Num, orDefault(p.Label, "free"), state)
		sw.W.AddEvent(sw.ID, "info", "switch", "port %d (%s) is %s", p.Num, orDefault(p.Label, "free"), state)
		sw.FS.Write("/etc/config/switch", RenderSwitchConf(sw), 0644, "root", "root")
		return nil
	}
	return fmt.Errorf("%s has no port %d", sw.Hostname, num)
}

// SetPortPoE turns power over ethernet on or off for one port. Turning it off
// really removes power from the device on the other end of the cable.
func (sw *Device) SetPortPoE(num int, on bool) error {
	if sw.Switch == nil {
		return fmt.Errorf("%s is not a managed switch", sw.Hostname)
	}
	for i := range sw.Switch.Ports {
		p := &sw.Switch.Ports[i]
		if p.Num != num {
			continue
		}
		if p.PoE == on {
			return nil
		}
		if on {
			peer := sw.W.Devices[p.Peer]
			if peer == nil {
				return fmt.Errorf("nothing is plugged into port %d", num)
			}
			if !peer.PoEPowered {
				return fmt.Errorf("the device on port %d (%s) does not take power over ethernet — it has its own plug", num, peer.Hostname)
			}
			if sw.PoESuppliedW()+peer.PoEDrawW() > sw.Switch.BudgetW {
				return fmt.Errorf("refusing to power port %d: %s draws %.1f W and only %.1f W of the %.0f W budget is left",
					num, peer.Hostname, peer.PoEDrawW(), sw.Switch.BudgetW-sw.PoESuppliedW(), sw.Switch.BudgetW)
			}
		}
		p.PoE = on
		state := "off"
		if on {
			state = "on"
		}
		sw.Logf("info", "switchd", "port %d PoE %s", num, state)
		sw.FS.Write("/etc/config/switch", RenderSwitchConf(sw), 0644, "root", "root")
		return nil
	}
	return fmt.Errorf("%s has no port %d", sw.Hostname, num)
}

// SetPoEBudget changes what the switch's power supply can deliver. Lowering it
// below what is already being drawn is refused, because a real switch cannot
// un-spend power it has already promised.
func (sw *Device) SetPoEBudget(watts float64) error {
	if sw.Switch == nil {
		return fmt.Errorf("%s is not a managed switch", sw.Hostname)
	}
	if watts < sw.PoESuppliedW() {
		return fmt.Errorf("port%s are drawing %.1f W — the budget cannot go below that",
			plural(len(sw.Switch.Ports)), sw.PoESuppliedW())
	}
	sw.Switch.BudgetW = watts
	sw.Logf("info", "switchd", "PoE budget set to %.0f W", watts)
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "s"
	}
	return "s"
}

// AttachTo puts a device on a switch port. This is cabling, not a permission
// grant: the device's address does not change, and nothing about it becomes
// reachable that was not already.
func (sw *Device) AttachTo(peer *Device, port int, label string) error {
	if sw.Switch == nil {
		return fmt.Errorf("%s is not a managed switch", sw.Hostname)
	}
	for i := range sw.Switch.Ports {
		p := &sw.Switch.Ports[i]
		if p.Num != port {
			continue
		}
		if p.Peer != "" && p.Peer != peer.ID {
			old := sw.W.Devices[p.Peer]
			who := p.Peer
			if old != nil {
				who = old.Hostname
			}
			return fmt.Errorf("port %d is already carrying %s", port, who)
		}
		p.Peer = peer.ID
		if label != "" {
			p.Label = label
		}
		peer.Uplink = sw.ID
		peer.UplinkPort = port
		sw.Logf("info", "switchd", "port %d: %s linked at %d Mbit/s", port, peer.Hostname, p.Speed)
		sw.FS.Write("/etc/config/switch", RenderSwitchConf(sw), 0644, "root", "root")
		return nil
	}
	return fmt.Errorf("%s has no port %d", sw.Hostname, port)
}

// Detach removes whatever is on a port.
func (sw *Device) Detach(port int) error {
	if sw.Switch == nil {
		return fmt.Errorf("%s is not a managed switch", sw.Hostname)
	}
	for i := range sw.Switch.Ports {
		p := &sw.Switch.Ports[i]
		if p.Num != port {
			continue
		}
		if p.Peer == "" {
			return fmt.Errorf("port %d is already empty", port)
		}
		if peer := sw.W.Devices[p.Peer]; peer != nil {
			peer.Uplink, peer.UplinkPort = "", 0
		}
		p.Peer = ""
		sw.FS.Write("/etc/config/switch", RenderSwitchConf(sw), 0644, "root", "root")
		return nil
	}
	return fmt.Errorf("%s has no port %d", sw.Hostname, port)
}

// LinkTick is the physical layer advancing: a device that lost PoE goes dark,
// one that got it back boots, and the switch says so in its log. It runs every
// tick, so the world repairs itself the way real hardware does — by noticing.
func (w *World) LinkTick() {
	for _, id := range w.Order {
		d := w.Devices[id]
		if !d.PoEPowered {
			continue
		}
		powered := w.poePowered(d)
		if powered == d.PoeUp {
			continue
		}
		d.PoeUp = powered
		if powered {
			d.setPowered(true, "")
			w.AddEvent(d.ID, "info", "switch", "%s has power again (port %d)", d.Hostname, d.UplinkPort)
			continue
		}
		w.AddEvent(d.ID, "warn", "switch", "%s lost power over ethernet (port %d)", d.Hostname, d.UplinkPort)
		d.setPowered(false, "PoE power removed")
	}
}

// DarkenPoE takes power away from everything a switch was feeding. It is what a
// switch losing its own power does to the devices that have no plug of their
// own.
func (w *World) DarkenPoE(sw *Device) {
	if sw.Switch == nil {
		return
	}
	for _, p := range sw.Switch.Ports {
		peer := w.Devices[p.Peer]
		if peer == nil || !peer.PoEPowered || !peer.NetUp {
			continue
		}
		peer.PoeUp = false
		w.AddEvent(peer.ID, "warn", "switch", "%s lost power over ethernet (%s went down)", peer.Hostname, sw.Hostname)
		peer.setPowered(false, "PoE power removed")
	}
}

// LinkReport is the switch's own report: what is plugged in, what is up, what
// each port is delivering.
func LinkReport(sw *Device) []string {
	if sw.Switch == nil {
		return nil
	}
	var out []string
	out = append(out, fmt.Sprintf("%s (%s) — %d ports, PoE budget %.0f W, drawing %.1f W",
		sw.Hostname, sw.HW.Model, len(sw.Switch.Ports), sw.Switch.BudgetW, sw.PoESuppliedW()))
	out = append(out, fmt.Sprintf("%-5s %-14s %-9s %-6s %-7s %s", "PORT", "LABEL", "LINK", "SPEED", "POE", "DEVICE"))
	for _, p := range sw.Switch.Ports {
		peer := sw.W.Devices[p.Peer]
		dev := "—"
		if peer != nil {
			dev = peer.Hostname
			switch {
			case !peer.Powered():
				// a PoE-powered device that lost its feed has no PHY left to
				// light, so its port drops — and the row should say why
				dev += "  (" + peer.UnavailableReason() + ")"
			default:
				if up, why := sw.W.linkUp(peer); !up {
					dev += "  (" + why + ")"
				}
			}
		}
		link := "down"
		if peer != nil && p.Admin && peer.Powered() {
			link = "up"
		}
		speed := "—"
		if link == "up" {
			speed = fmt.Sprintf("%dM", p.Speed)
		}
		poe := "—"
		if p.PoE {
			poe = fmt.Sprintf("%.1fW", peerDraw(peer))
		}
		up := "up"
		if !p.Admin {
			up = "down"
		}
		out = append(out, fmt.Sprintf("%-5d %-14s %-9s %-6s %-7s %s", p.Num,
			orDefault(p.Label, "free"), link+"/"+up, speed, poe, dev))
	}
	if sw.PoEExceeded() {
		out = append(out, fmt.Sprintf("warning: PoE over budget (%.1f W of %.0f W)", sw.PoESuppliedW(), sw.Switch.BudgetW))
	}
	if sw.Switch.Dropped > 0 {
		out = append(out, fmt.Sprintf("%d frame(s) dropped by the PoE budget", sw.Switch.Dropped))
	}
	return out
}

func peerDraw(peer *Device) float64 {
	if peer == nil {
		return 0
	}
	return peer.PoEDrawW()
}

// RenderSwitchConf writes a switch's port configuration as the file its own
// service reads. It is the same fact the packet path uses, rendered the way the
// device would store it — never a second source of truth.
func RenderSwitchConf(sw *Device) string {
	var b strings.Builder
	if sw.Switch == nil {
		return ""
	}
	fmt.Fprintf(&b, "# %s port configuration\n", sw.Hostname)
	fmt.Fprintf(&b, "poe budget %0.f\n", sw.Switch.BudgetW)
	for _, p := range sw.Switch.Ports {
		admin := "down"
		if p.Admin {
			admin = "up"
		}
		poe := "off"
		if p.PoE {
			poe = "on"
		}
		peer := p.Peer
		if d := sw.W.Devices[peer]; d != nil {
			peer = d.Hostname
		}
		kind := "access"
		if p.Uplink {
			kind = "uplink"
		}
		fmt.Fprintf(&b, "port %d %s admin=%s poe=%s speed=%d\n", p.Num, kind, admin, poe, p.Speed)
		if peer != "" {
			fmt.Fprintf(&b, "  label %s\n", orDefault(p.Label, peer))
			fmt.Fprintf(&b, "  device %s\n", peer)
		}
	}
	return b.String()
}

// LoadSwitchConfig re-reads a switch's port file into live state. Nothing here
// is cached on purpose: `switchctl` writes the file and a hand edit to
// /etc/config/switch takes effect the same way a hand-edited firewall config
// does, because the file is the configuration and the state is only its
// in-memory image.
func LoadSwitchConfig(sw *Device) {
	if sw.Switch == nil {
		return
	}
	v, ok := sw.FS.Read("/etc/config/switch")
	if !ok || v == nil {
		return // a switch with no config file keeps the config it is running
	}
	for _, line := range strings.Split(string(v), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "port" {
			continue
		}
		num := atoiOr(f[1], 0)
		for i := range sw.Switch.Ports {
			p := &sw.Switch.Ports[i]
			if p.Num != num {
				continue
			}
			for _, kv := range f[2:] {
				k, val, found := strings.Cut(kv, "=")
				if !found {
					continue
				}
				switch k {
				case "admin":
					p.Admin = val == "up"
				case "poe":
					p.PoE = val == "on"
				case "speed":
					p.Speed = atoiOr(val, p.Speed)
				}
			}
		}
	}
}

// NewSwitchPorts builds a switch's port list from pairs of (peer, poe).
func NewSwitchPorts(sw *Device, count int, budgetW float64) *SwitchState {
	st := &SwitchState{BudgetW: budgetW}
	for n := 1; n <= count; n++ {
		st.Ports = append(st.Ports, SwitchPort{Num: n, Label: "free", Admin: true, Speed: 1000})
	}
	return st
}

// CableUp is a convenience for the seed and for tests: it attaches devices to
// switch ports in the order given.
func (sw *Device) CableUp(port int, peer *Device, label string, poe bool) {
	if sw.Switch == nil {
		return
	}
	if err := sw.AttachTo(peer, port, label); err != nil {
		panic(fmt.Sprintf("cabling %s port %d: %v", sw.Hostname, port, err))
	}
	for i := range sw.Switch.Ports {
		if sw.Switch.Ports[i].Num == port {
			sw.Switch.Ports[i].PoE = poe && peer.PoEPowered
			sw.Switch.Ports[i].Speed = 1000
			if peer.HW.NetMbps > 0 && peer.HW.NetMbps < 1000 {
				sw.Switch.Ports[i].Speed = peer.HW.NetMbps
			}
		}
	}
}

// LinkPath describes the physical chain a device sits on, for reports:
// "cam-front → sw-alex → gateway".
func (w *World) LinkPath(d *Device) string {
	var parts []string
	parts = append(parts, d.Hostname)
	seen := map[string]bool{}
	cur := d
	for cur != nil && cur.Uplink != "" && !seen[cur.ID] {
		seen[cur.ID] = true
		up := w.Devices[cur.Uplink]
		if up == nil {
			parts = append(parts, "?")
			break
		}
		parts = append(parts, up.Hostname)
		cur = up
	}
	return strings.Join(parts, " → ")
}
