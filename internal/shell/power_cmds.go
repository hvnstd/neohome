package shell

import (
	"fmt"

	"neohome/internal/core"
)

// power — the household electrical supply.
//
// This is not a status panel. Cutting power really takes machines down, and
// they really do not answer the network until power comes back. The way to
// survive an outage is a UPS, which is bought and then dies when it runs flat.
func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"power", cmdPower}, {"ups", cmdUPS}, {"bmc", cmdBMC},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdPower(s *Shell, args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status", "st":
		for _, line := range s.W.PowerReport() {
			fmt.Fprintln(s.Out, line)
		}
		return 0

	case "boot", "up":
		// The controller's own job: close the breaker and bring the house back.
		// This is the whole reason out-of-band management exists.
		// Closing the breaker is the controller's job — that is why it exists.
		// Getting here at all means the player reached the controller.
		if !s.Dev.IsOutOfBand() && s.Dev.Profile != "bmc" {
			fmt.Fprintln(s.Out, "power: the household has no out-of-band controller to do this")
			return 1
		}
		booted := s.W.RestorePower(s.User.Name)
		if len(booted) == 0 {
			fmt.Fprintln(s.Out, "power: the supply is already on")
			return 0
		}
		fmt.Fprintf(s.Out, "supply on — %d machine(s) booting:\n", len(booted))
		for _, h := range booted {
			fmt.Fprintf(s.Out, "  %s\n", h)
		}
		return 0

	case "cut", "off":
		// It is the player's own house: anyone who owns the property can walk
		// to the panel and open the breaker. Requiring root here would make the
		// mechanic unreachable for the very player it belongs to.
		if s.W.PowerCut {
			fmt.Fprintln(s.Out, "power: the breaker is already open")
			return 1
		}
		s.W.CutPower(s.User.Name)
		fmt.Fprintln(s.Out, "main breaker opened — the household is now dark")
		if s.Dev.IsOutOfBand() {
			fmt.Fprintln(s.Out, "this controller runs on its own battery; the rest of the house does not.")
		} else {
			fmt.Fprintln(s.Out, "everything plugged in is about to stop — including this machine.")
		}
		return 0

	case "restore", "on", "reset":
		if s.User.UID != 0 {
			fmt.Fprintln(s.Out, "power: refusing to close the main breaker (need root)")
			return 1
		}
		booted := s.W.RestorePower(s.User.Name)
		if len(booted) == 0 {
			fmt.Fprintln(s.Out, "power: nothing to restore; the supply was already up")
			return 0
		}
		fmt.Fprintf(s.Out, "main breaker closed — %d machine(s) booting\n", len(booted))
		for _, h := range booted {
			fmt.Fprintf(s.Out, "  %s: services up\n", h)
		}
		return 0

	case "draw":
		for _, line := range s.W.PowerReport() {
			fmt.Fprintln(s.Out, line)
		}
		fmt.Fprintf(s.Out, "\nrate: $%.2f per kWh\n", 0.31)
		return 0

	default:
		fmt.Fprintln(s.Out, "usage: power status | cut | boot | draw")
		return 1
	}
}

// bmc — the management controller's own view. This is the machine a player
// reaches for when the house is dark, and the machine an attacker wants.
func cmdBMC(s *Shell, args []string) int {
	if len(args) == 0 || args[0] == "status" {
		d := s.W.OutOfBand()
		if d == nil {
			fmt.Fprintln(s.Out, "no management controller in this household")
			return 1
		}
		fmt.Fprintf(s.Out, "BMC: %s (%s)\n", d.Hostname, d.OS.Distro+" "+d.OS.Ver)
		fmt.Fprintf(s.Out, "  address:  %s (management LAN)\n", d.FirstLANIP())
		fmt.Fprintf(s.Out, "  battery:  %d%% (%s)\n", upsPct(d), upsState(d))
		fmt.Fprintf(s.Out, "  backhaul: cellular (independent of the household line)\n")
		if !d.Powered() {
			fmt.Fprintln(s.Out, "  state:    DOWN — the controller's own battery is spent")
			return 1
		}
		fmt.Fprintln(s.Out, "  state:    reachable")
		if !s.W.HouseholdPower() {
			fmt.Fprintln(s.Out, "  household supply: OFF — `power boot` will close the breaker")
		}
		return 0
	}
	switch args[0] {
	case "power", "boot":
		return cmdPower(s, []string{"boot"})
	case "status":
		return cmdBMC(s, nil)
	default:
		fmt.Fprintln(s.Out, "usage: bmc [status|power]")
		return 1
	}
}

func upsPct(d *core.Device) int {
	if d.UPS == nil {
		return 0
	}
	return d.UPS.ChargePct
}

func upsState(d *core.Device) string {
	if d.UPS == nil {
		return "none"
	}
	return d.UPS.LastState
}

// ups — battery backup for a machine. This is the counter-play to an outage:
// it buys time, then shuts the machine down cleanly instead of losing it.
func cmdUPS(s *Shell, args []string) int {
	dev := s.Dev
	if len(args) == 0 {
		if dev.UPS == nil {
			fmt.Fprintln(s.Out, "no UPS on this machine")
			fmt.Fprintln(s.Out, "install one with: ups install")
			return 1
		}
		fmt.Fprintf(s.Out, "UPS: %d%% charge (%s)\n", dev.UPS.ChargePct, dev.UPS.LastState)
		fmt.Fprintf(s.Out, "draw: %d W — about %d game-minutes of runtime left\n",
			dev.DrawWatts(), dev.UPS.ChargePct*2)
		return 0
	}

	switch args[0] {
	case "install", "add":
		// Plugging a battery into your own rack is not a privileged syscall.
		if s.User.UID != 0 && dev.Owner != s.User.Name {
			fmt.Fprintln(s.Out, "ups: you do not own this machine")
			return 1
		}
		if dev.UPS != nil {
			fmt.Fprintln(s.Out, "ups: this machine already has one")
			return 1
		}
		cost := int64(9000) // $90
		acc := s.W.Bank.Accts[s.User.Name]
		if acc == nil {
			fmt.Fprintln(s.Out, "ups: no household account")
			return 1
		}
		if acc.Balance < cost {
			fmt.Fprintf(s.Out, "ups: insufficient funds — $%.2f needed, $%.2f available\n",
				float64(cost)/100, float64(acc.Balance)/100)
			return 1
		}
		acc.Balance -= cost
		acc.Tx = append(acc.Tx, core.Tx{At: s.W.Sim, Amount: -cost,
			Memo: "UPS for " + dev.Hostname, Balance: acc.Balance})
		dev.UPS = &core.UPSInfo{ChargePct: 100, LastState: "online"}
		dev.Logf("info", "ups", "battery backup installed, 100%% charge")
		s.W.AddEvent(dev.ID, "info", "power", "a UPS was installed on %s", dev.Hostname)
		fmt.Fprintf(s.Out, "UPS installed on %s — $%.2f charged to the household\n",
			dev.Hostname, float64(cost)/100)
		return 0

	case "remove":
		if dev.UPS == nil {
			fmt.Fprintln(s.Out, "ups: nothing installed")
			return 1
		}
		dev.UPS = nil
		fmt.Fprintln(s.Out, "UPS removed")
		return 0

	case "runtime":
		if dev.UPS == nil {
			fmt.Fprintln(s.Out, "no UPS on this machine")
			return 1
		}
		minutes := dev.UPS.ChargePct * 2
		fmt.Fprintf(s.Out, "%d%% charge at %d W draw → ~%d game-minutes\n",
			dev.UPS.ChargePct, dev.DrawWatts(), minutes)
		return 0

	default:
		fmt.Fprintln(s.Out, "usage: ups [install|remove|runtime]")
		return 1
	}
}
