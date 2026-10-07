package core

import "fmt"

// ---------------------------------------------------------------------------
// Household power
//
// The house has a real supply, and everything plugged into it stops when the
// supply does: the PC goes dark, the router stops forwarding, the servers become
// unreachable. `Powered` is consulted by Reach and Dial, so an outage is not a
// message the player reads — it is the network ceasing to answer.
//
// Three things can darken the house, and they are all real world state:
//
//	UtilitiesSuspended  the account is in arrears and the utility pulled service
//	PowerCut            the player threw the breaker
//	MainsDropped        a single device's plug was pulled
//
// A UPS is the counter-play: it rides through an outage on battery, and when the
// battery runs out it shuts its charge down instead of crashing it.
// ---------------------------------------------------------------------------

// UPSInfo is a battery backup. It rides a device through an outage and, when it
// runs out, shuts the device down cleanly rather than crashing it — which is the
// whole reason a player would buy one.
type UPSInfo struct {
	ChargePct int    // 0..100
	LastState string // online | on battery | depleted
}

// OutOfBand returns the household's management controller, if it has one.
func (w *World) OutOfBand() *Device {
	for _, id := range w.Order {
		if d := w.Devices[id]; d != nil && d.IsOutOfBand() {
			return d
		}
	}
	return nil
}

// IsOutOfBand reports the machine that survives the house losing power. A BMC
// sits on the mains side of the breaker with its own battery and a cellular
// backhaul, which is exactly why cutting the power does not lock the operator
// out of their own house.
func (d *Device) IsOutOfBand() bool { return d.Profile == "bmc" }

// IsDataCenter reports whether a device's power comes from somewhere other than
// this house — a colo or a datacenter has its own feed and never darkens because
// the player's street lost power.
func (d *Device) IsDataCenter() bool {
	switch d.Profile {
	case "core", "infra", "vps":
		return true
	}
	return false
}

// HouseholdPower reports whether mains power is reaching the house at all.
func (w *World) HouseholdPower() bool {
	return !w.UtilitiesSuspended && !w.PowerCut
}

// Powered reports whether a device has power right now. A UPS keeps a device up
// on battery while the mains is out.
func (d *Device) Powered() bool {
	if d.IsDataCenter() {
		// The house cannot darken a datacenter — but the customer's own panel
		// can switch their machine off (§12 启动/关机), and then there really
		// is nothing at that address to answer.
		return d.NetUp
	}
	if d.MainsDropped {
		// a laptop with charge in it is still a running machine — that is the
		// entire point of a laptop
		if d.Battery != nil && d.Battery.LidOpen && d.Battery.Pct > 0 {
			return true
		}
		return false
	}
	// Out-of-band management keeps its own battery and a cellular backhaul, so
	// the house going dark never takes the way back in with it.
	if d.IsOutOfBand() {
		return d.UPS != nil && d.UPS.ChargePct > 0
	}
	// §15/WS-1.2: a phone's only power is its battery. At zero it is off — no
	// processes, no cron, no ssh — until someone docks it (see PhoneCharge).
	// The SMS layer already stops its sshd and its spool at zero; this is
	// what makes the rest of the machine agree with them.
	if phoneDead(d) {
		return false
	}
	// §15: a device powered over ethernet has no plug of its own. Its power is
	// the switch's business, which is why cutting a PoE port reboots a camera
	// and a switch on a UPS keeps the cameras alive through an outage.
	if d.PoEPowered && d.W != nil {
		return d.W.poePowered(d)
	}
	// §15: a laptop is the one household device whose availability is its own
	// business. A shut lid is a suspended machine and an empty battery is not
	// power, whatever the wall socket is doing.
	if d.Battery != nil && (!d.Battery.LidOpen || (d.Battery.Pct <= 0 && !d.Battery.Charging)) {
		return false
	}
	// A UPS only helps while it has charge left. An exhausted battery is not
	// power, and pretending otherwise would make the machine immortal.
	if !d.W.HouseholdPower() && (d.UPS == nil || d.UPS.ChargePct == 0) {
		return false
	}
	return true
}

// UnavailableReason says why a machine is not answering, in the words its owner
// would use: "suspended (lid closed)" is a different problem from "no power",
// and the difference is the whole reason this is a function.
func (d *Device) UnavailableReason() string {
	switch {
	case d.NetUp:
		return "up"
	case d.PoEPowered:
		if sw, port := d.W.SwitchOf(d); sw != nil && port != nil {
			if !port.PoE {
				return "no power — PoE is off on " + sw.Hostname + " port " + itoa(port.Num)
			}
			if !sw.Powered() {
				return "no power — " + sw.Hostname + " is down"
			}
		}
		return "no power over ethernet"
	case d.Battery != nil && !d.Battery.LidOpen:
		return "suspended (lid closed)"
	case d.Battery != nil && d.Battery.Pct <= 0 && !d.Battery.Charging:
		return "battery empty"
	case phoneDead(d):
		return "battery empty"
	case d.MainsDropped:
		return "no power — unplugged"
	case !d.W.HouseholdPower():
		return "no power — the household supply is off"
	}
	return "no power"
}

// phoneDead reports whether a phone's battery is spent. Only phones the SMS
// layer tracks can be dead this way: a device without a battery entry is
// assumed powered, so an unknown or future device is never bricked by a
// missing counter.
func phoneDead(d *Device) bool {
	if d == nil || d.Profile != "phone" || d.W == nil || d.W.SMS == nil {
		return false
	}
	pct, ok := d.W.SMS.Battery[d.ID]
	return ok && pct <= 0
}

// OnBattery reports whether a device is running off its UPS.
func (d *Device) OnBattery() bool {
	return d.UPS != nil && d.UPS.ChargePct > 0 && !d.W.HouseholdPower() && !d.MainsDropped
}

// setPowered is the single place a device's power changes. It records what was
// running before the lights went out so a reboot can bring back exactly that,
// which is what makes power restoration recover the machine rather than reset
// it.
func (d *Device) setPowered(on bool, reason string) {
	if on {
		if d.NetUp {
			return
		}
		d.NetUp = true
		d.PowerOK = true
		d.Boot = d.W.Sim
		d.Logf("info", "kernel", "boot: %s %s (%s)", d.OS.Distro, d.OS.Ver, d.OS.Kernel)
		// Bring back exactly what this machine was running before it went dark.
		// A machine that was never down has no recorded set, so nothing to do.
		for _, name := range d.BootSet {
			if svc := d.Svc(name); svc != nil && svc.State != "running" {
				d.StartService(name)
			}
		}
		// a hypervisor's guests come back with it
		if d.W.VMs != nil {
			for _, v := range d.W.VMs.Hosts[d.ID] {
				if g := v.Device(); g != nil {
					g.setPowered(true, "host booted")
				}
			}
		}
		return
	}

	if !d.NetUp && !d.PowerOK {
		return // already dark
	}
	// remember the boot set so restoring power restores the machine
	var boot []string
	for name, svc := range d.Services {
		if svc.State == "running" {
			boot = append(boot, name)
		}
	}
	if len(boot) > 0 {
		d.BootSet = boot
	}
	d.NetUp = false
	d.PowerOK = false
	for _, svc := range d.Services {
		svc.State = "stopped"
		svc.PID = 0
	}
	d.Procs = nil
	d.Fail2Ban = map[string]int{}
	if reason != "" {
		d.Logf("warn", "kernel", "%s — halting", reason)
	}
	// guests on this machine die with it
	if d.W.VMs != nil {
		for _, v := range d.W.VMs.Hosts[d.ID] {
			v.State = "stopped"
			v.LastState = "host powered off"
			if g := v.Device(); g != nil {
				g.setPowered(false, "hypervisor lost power")
			}
		}
	}
}

// CutPower throws the household breaker. The machines go dark immediately: a
// world where the network says a host is unreachable but `systemctl` still calls
// its services running is self-contradictory, and the player would be right to
// stop trusting either one.
func (w *World) CutPower(by string) {
	if w.PowerCut {
		return
	}
	w.PowerCut = true
	w.PowerOutTicks = 0
	if r := w.Devices["router-alex"]; r != nil {
		w.AddEvent(r.ID, "warn", "power", "%s threw the main breaker", by)
	}
	w.ChatPost("#local", "mira-9", "did the power just go out for everyone else too?")
	w.darkenHousehold()
}

// ApplyOutage powers down everything the current supply state cannot run. It is
// exported because the utility-suspension path is a second, independent way to
// lose power and it must have exactly the same physical consequence.
func (w *World) ApplyOutage() { w.darkenHousehold() }

// darkenHousehold powers down every mains-fed device that has no battery left to
// run on. A device on a charged UPS stays up.
func (w *World) darkenHousehold() {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.IsDataCenter() || d.IsOutOfBand() || d.MainsDropped || !d.NetUp {
			continue
		}
		// a PoE device is fed by its switch, and that switch is judged on its
		// own line in this same loop — so it is not dark just because the
		// mains is: `LinkTick` takes it down if the switch really goes dark
		if d.PoEPowered {
			continue
		}
		if d.UPS != nil && d.UPS.ChargePct > 0 {
			continue // riding it out on battery
		}
		w.AddEvent(d.ID, "warn", "power", "%s lost power (%s)", d.Hostname, d.Profile)
		d.setPowered(false, "mains lost")
	}
}

// RestorePower closes the breaker again and boots the house back up. Machines
// come back with the services they had before the lights went out — a router
// that loses its DNS forwarder is not the same router.
func (w *World) RestorePower(by string) []string {
	if !w.PowerCut {
		return nil
	}
	w.PowerCut = false
	w.PowerOutTicks = 0
	if r := w.Devices["router-alex"]; r != nil {
		w.AddEvent(r.ID, "info", "power", "%s restored the household power", by)
	}
	var booted []string
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.IsDataCenter() || d.IsOutOfBand() || d.MainsDropped || d.NetUp {
			continue
		}
		d.setPowered(true, "")
		booted = append(booted, d.Hostname)
	}
	return booted
}

// PlugPull cuts one device off at the socket without darkening the house.
func (d *Device) PlugPull() {
	if d.IsDataCenter() {
		return
	}
	d.MainsDropped = true
	d.W.AddEvent(d.ID, "warn", "power", "%s was unplugged", d.Hostname)
	if d.Battery != nil {
		// a laptop does not stop when the cable is pulled: it switches to its
		// battery and keeps going until that runs out
		d.Battery.Charging = false
		if d.Battery.Pct > 0 && d.Battery.LidOpen {
			d.Logf("info", "kernel", "AC adapter disconnected — running on battery (%d%%)", d.Battery.Pct)
			return
		}
	}
	d.setPowered(false, "AC adapter removed")
	// a switch that lost its own power takes its PoE ports with it
	d.W.DarkenPoE(d)
}

// PlugRestore plugs a device back in and boots it.
func (d *Device) PlugRestore() {
	if !d.MainsDropped {
		return
	}
	d.MainsDropped = false
	d.W.AddEvent(d.ID, "info", "power", "%s was plugged back in", d.Hostname)
	if d.Battery != nil {
		d.Battery.Charging = true
		if !d.Battery.LidOpen {
			d.Logf("info", "kernel", "AC adapter connected (lid closed — still suspended)")
			return
		}
	}
	d.setPowered(true, "")
}

// PowerTick advances the supply on the world clock: batteries drain, an outage
// that lasts gets noticed from outside, and guests follow their host.
func (w *World) PowerTick() {
	if w.HouseholdPower() {
		for _, id := range w.Order {
			d := w.Devices[id]
			if d.UPS == nil || d.UPS.ChargePct >= 100 {
				continue
			}
			d.UPS.ChargePct += 2
			if d.UPS.ChargePct > 100 {
				d.UPS.ChargePct = 100
			}
			if d.UPS.ChargePct == 100 && d.UPS.LastState != "online" {
				d.UPS.LastState = "online"
				d.Logf("info", "ups", "battery charged — back on mains")
				// mains is back: boot whatever the UPS is holding up
				if !d.NetUp {
					d.setPowered(true, "")
				}
			}
		}
		return
	}

	w.PowerOutTicks++

	for _, id := range w.Order {
		d := w.Devices[id]
		if d.IsDataCenter() || d.IsOutOfBand() || d.MainsDropped {
			continue
		}
		if d.UPS != nil && d.UPS.ChargePct > 0 {
			// Drain slowly enough that the runtime this file advertises is
			// true: 1% per 4 ticks × 30 game-seconds = 2 game-minutes per %,
			// which is exactly what `ups runtime` reports.
			if w.PowerOutTicks%4 == 0 {
				d.UPS.ChargePct--
			}
			d.UPS.LastState = "on battery"
			if d.UPS.ChargePct == 20 {
				d.Logf("warn", "ups", "battery at 20%% — about 40 game-minutes left, will shut down cleanly")
			}
			if d.UPS.ChargePct == 0 {
				d.UPS.LastState = "depleted"
				w.AddEvent(d.ID, "err", "power", "%s exhausted its battery and went down", d.Hostname)
				d.setPowered(false, "UPS battery depleted")
				w.DarkenPoE(d)
			}
			continue
		}
		if d.PoEPowered {
			// fed by a switch, which this same tick has already judged
			continue
		}
		if d.NetUp {
			w.AddEvent(d.ID, "warn", "power", "%s lost power (%s)", d.Hostname, d.Profile)
			d.setPowered(false, "mains lost")
		}
	}

	// An outage that lasts long enough is noticed from outside the house.
	if w.PowerOutTicks == 40 {
		who := "the household"
		w.AddEvent("core-gw", "warn", "isp", "%s has been offline for %d game-minutes — the line looks dead",
			who, w.PowerOutTicks/2)
	}
}

// DrawWatts estimates what a device is pulling, so the bill and `power status`
// have a real basis instead of a decoration number.
func (d *Device) DrawWatts() int {
	if !d.NetUp {
		return 0
	}
	w := 10 + d.HW.Cores*2 // idle board plus cores
	w += int(float64(len(d.Procs)) * 0.5)
	for _, p := range d.Procs {
		w += int(p.CPU * 0.8)
	}
	if d.HW.HasWifi {
		w += 3
	}
	if d.HW.Battery {
		w += 1
	}
	return w
}

// PowerReport is the supply state as `power status` prints it.
func (w *World) PowerReport() []string {
	supply := "online"
	switch {
	case w.PowerCut:
		supply = "CUT at the breaker"
	case w.UtilitiesSuspended:
		supply = "SUSPENDED (account in arrears)"
	}
	out := []string{fmt.Sprintf("household supply: %s   (down for %d game-minutes)",
		supply, w.PowerOutTicks/2)}
	var watts int
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.IsDataCenter() {
			continue
		}
		state := "down"
		if d.IsOutOfBand() && d.Powered() {
			state = fmt.Sprintf("out-of-band %d%%", d.UPS.ChargePct)
		}
		switch {
		case d.MainsDropped:
			state = "unplugged"
		case d.OnBattery():
			state = fmt.Sprintf("on battery %d%%", d.UPS.ChargePct)
		case d.Powered() && d.NetUp:
			state = "up"
		}
		watts += d.DrawWatts()
		line := fmt.Sprintf("  %-12s %-6s %-16s %3d W", d.Hostname, d.Profile, state, d.DrawWatts())
		if d.UPS != nil {
			line += fmt.Sprintf("  [ups %d%%]", d.UPS.ChargePct)
		}
		out = append(out, line)
	}
	out = append(out, fmt.Sprintf("  %-12s %-6s %-16s %3d W", "TOTAL", "", "", watts))
	return out
}
