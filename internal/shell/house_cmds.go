package shell

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// nasFor finds the household's NAS: the box backups and shares live on.
func nasFor(s *Shell) *core.Device {
	for _, id := range s.W.Order {
		d := s.W.Devices[id]
		if d.Profile == "nas" && d.Owner == s.Dev.Owner {
			return d
		}
	}
	return nil
}

// §15 家庭设备: the commands that touch the wire, the printer and the laptop.
//
// None of these is a status panel. `switchctl` changes real ports, and a port
// that goes down really takes the device behind it off the network; `lp`
// submits a document that really consumes paper and comes out in the printer's
// tray file; `laptopctl` closes a lid that really suspends the machine.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"switchctl", cmdSwitchctl},
		{"swconfig", cmdSwitchctl},
		{"lp", cmdLP}, {"lpr", cmdLP}, {"lpstat", cmdLPStat},
		{"cancel", cmdCancel}, {"lpadmin", cmdLPAdmin},
		{"laptopctl", cmdLaptopctl}, {"backup", cmdBackup}, {"links", cmdLinks},
	} {
		builtinTable[e.name] = e.fn
	}
}

// ---- the switch ----

func cmdSwitchctl(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "show", "status", "list":
		sw := switchFor(s)
		if sw == nil {
			s.errf("switchctl: no managed switch on this network")
			return 1
		}
		for _, line := range core.LinkReport(sw) {
			fmt.Fprintln(s.Out, line)
		}
		return 0
	case "port":
		sw := switchFor(s)
		if sw == nil {
			s.errf("switchctl: no managed switch on this network")
			return 1
		}
		if len(args) < 3 {
			s.errf("usage: switchctl port N up|down")
			return 1
		}
		num, err := strconv.Atoi(args[1])
		if err != nil {
			s.errf("switchctl: port must be a number")
			return 1
		}
		var up bool
		switch args[2] {
		case "up", "on", "enable":
			up = true
		case "down", "off", "disable":
			up = false
		default:
			s.errf("usage: switchctl port N up|down")
			return 1
		}
		if err := sw.SetPortAdmin(num, up); err != nil {
			s.errf("switchctl: %v", err)
			return 1
		}
		if up {
			fmt.Fprintf(s.Out, "port %d is up\n", num)
		} else {
			fmt.Fprintf(s.Out, "port %d is down", num)
			if peer := portPeer(sw, num); peer != "" {
				fmt.Fprintf(s.Out, " — %s is off the network now", peer)
			}
			fmt.Fprintln(s.Out)
		}
		return 0
	case "poe":
		sw := switchFor(s)
		if sw == nil {
			s.errf("switchctl: no managed switch on this network")
			return 1
		}
		if len(args) < 3 {
			s.errf("usage: switchctl poe N on|off")
			return 1
		}
		num, err := strconv.Atoi(args[1])
		if err != nil {
			s.errf("switchctl: port must be a number")
			return 1
		}
		on := args[2] == "on" || args[2] == "enable" || args[2] == "on,"
		if err := sw.SetPortPoE(num, on); err != nil {
			s.errf("switchctl: %v", err)
			return 1
		}
		if on {
			fmt.Fprintf(s.Out, "PoE on for port %d — %s is booting\n", num, portPeer(sw, num))
		} else {
			fmt.Fprintf(s.Out, "PoE off for port %d — %s lost power\n", num, portPeer(sw, num))
		}
		s.W.LinkTick()
		return 0
	case "budget":
		sw := switchFor(s)
		if sw == nil {
			s.errf("switchctl: no managed switch on this network")
			return 1
		}
		if len(args) < 2 {
			fmt.Fprintf(s.Out, "PoE budget: %.0f W, supplying %.1f W\n", sw.Switch.BudgetW, sw.PoESuppliedW())
			return 0
		}
		watts, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			s.errf("switchctl: budget takes a number of watts")
			return 1
		}
		if err := sw.SetPoEBudget(watts); err != nil {
			s.errf("switchctl: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "PoE budget set to %.0f W\n", watts)
		return 0
	case "attach":
		sw := switchFor(s)
		if sw == nil {
			s.errf("switchctl: no managed switch on this network")
			return 1
		}
		if len(args) < 3 {
			s.errf("usage: switchctl attach N HOST [poe]")
			return 1
		}
		num, err := strconv.Atoi(args[1])
		if err != nil {
			s.errf("switchctl: port must be a number")
			return 1
		}
		peer := deviceByHostname(s, args[2])
		if peer == nil {
			s.errf("switchctl: no device called %s on this network", args[2])
			return 1
		}
		poe := len(args) > 3 && args[3] == "poe"
		if err := sw.AttachTo(peer, num, peer.Hostname); err != nil {
			s.errf("switchctl: %v", err)
			return 1
		}
		if poe && peer.PoEPowered {
			if err := sw.SetPortPoE(num, true); err != nil {
				s.errf("switchctl: %v", err)
				return 1
			}
		}
		fmt.Fprintf(s.Out, "%s is plugged into port %d\\n", peer.Hostname, num)
		s.W.LinkTick()
		return 0
	}
	s.errf("usage: switchctl [show|port N up|down|poe N on|off|budget [W]|attach N HOST [poe]]")
	return 1
}

// switchFor finds the switch this command is about: the one in front of the
// caller, or the only managed switch on the household's LAN.
func switchFor(s *Shell) *core.Device {
	if sw, _ := s.W.SwitchOf(s.Dev); sw != nil {
		return sw
	}
	if s.Dev.IsSwitch() {
		return s.Dev
	}
	for _, id := range s.W.Order {
		d := s.W.Devices[id]
		// configuration belongs to the household that owns the switch: being
		// able to reach a device is not authority over it
		if d.IsSwitch() && d.Owner == s.Dev.Owner {
			return d
		}
	}
	return nil
}

func portPeer(sw *core.Device, num int) string {
	if sw.Switch == nil {
		return ""
	}
	for _, p := range sw.Switch.Ports {
		if p.Num == num {
			if d := sw.W.Devices[p.Peer]; d != nil {
				return d.Hostname
			}
		}
	}
	return ""
}

func deviceByHostname(s *Shell, host string) *core.Device {
	for _, id := range s.W.Order {
		if d := s.W.Devices[id]; d.Hostname == host {
			return d
		}
	}
	return nil
}

// ---- the printer ----

func cmdLP(s *Shell, args []string) int {
	title := ""
	var body []string
	printer := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d" || a == "-P" || a == "--printer" || a == "--destination":
			if i+1 < len(args) {
				printer = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-d") && len(a) > 2:
			printer = a[2:]
		case a == "-t" || a == "--title":
			if i+1 < len(args) {
				title = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-"):
			// a flag this world does not model
		default:
			body = append(body, a)
		}
	}
	d, err := printerOr(s, printer)
	if err != nil {
		s.errf("lp: %v", err)
		return 1
	}
	if len(body) == 0 {
		s.errf("usage: lp [-d printer] [-t title] FILE")
		return 1
	}
	pages := 0
	for _, name := range body {
		p := s.abs(name)
		vfs, rp, ferr := s.ResolveVFS(p)
		if ferr != "" {
			s.errf("lp: %s", ferr)
			return 1
		}
		data, ok := vfs.Read(rp)
		if !ok || data == nil {
			s.errf("lp: cannot open %s", name)
			return 1
		}
		pages += countSheets(string(data))
		if title == "" {
			title = path.Base(p)
		}
	}
	// the job goes to the printer over the network, through the same packet
	// path as everything else: a printer behind a dead switch port refuses it
	// with the real reason
	if _, _, msg := core.Dial(s.Dev, d.FirstLANIP(), core.PrintPort); msg != "connected" {
		s.errf("lp: %s: %s", d.Hostname, msg)
		return 1
	}
	job, err := s.W.SubmitPrint(d, s.User.Name, title, pages)
	if err != nil {
		s.errf("lp: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "request id is %s-%d (%d file(s), %d page(s))\n", d.Hostname, job.ID, len(body), pages)
	return 0
}

// printerOr resolves the printer a job is for. With no -d, a household with one
// printer gets that one — which is how a real default queue works.
func printerOr(s *Shell, name string) (*core.Device, error) {
	var found *core.Device
	for _, id := range s.W.Order {
		d := s.W.Devices[id]
		if d.Printer == nil {
			continue
		}
		if name != "" && d.Hostname != name {
			continue
		}
		if name != "" {
			return d, nil
		}
		if d.Owner == s.Dev.Owner {
			if found != nil {
				return nil, fmt.Errorf("more than one printer — name one with -d")
			}
			found = d
		}
	}
	if found == nil {
		if name != "" {
			return nil, fmt.Errorf("no printer called %s", name)
		}
		return nil, fmt.Errorf("no printer on this network")
	}
	return found, nil
}

func cmdLPStat(s *Shell, args []string) int {
	d, err := printerOr(s, "")
	if err != nil {
		s.errf("lpstat: %v", err)
		return 1
	}
	for _, line := range core.PrintReport(d) {
		fmt.Fprintln(s.Out, line)
	}
	// where the printer really is, and whether the wire is up
	fmt.Fprintf(s.Out, "device: %s (%s)\n", d.FirstLANIP(), s.W.LinkPath(d))
	if up, why := s.W.LinkUp(d); !up {
		fmt.Fprintf(s.Out, "network: unreachable — %s\n", why)
	}
	return 0
}

func cmdCancel(s *Shell, args []string) int {
	d, err := printerOr(s, "")
	if err != nil {
		s.errf("cancel: %v", err)
		return 1
	}
	if len(args) == 0 {
		s.errf("usage: cancel <job id>")
		return 1
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		s.errf("cancel: job id must be a number")
		return 1
	}
	if err := s.W.CancelPrint(d, id, s.User.Name, s.User.UID == 0 || s.User.Name == "root"); err != nil {
		s.errf("cancel: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "job %d cancelled\n", id)
	return 0
}

func cmdLPAdmin(s *Shell, args []string) int {
	d, err := printerOr(s, "")
	if err != nil {
		s.errf("lpadmin: %v", err)
		return 1
	}
	if len(args) == 0 {
		s.errf("usage: lpadmin [status|paper N|toner P|pause|resume]")
		return 1
	}
	switch args[0] {
	case "status":
		for _, line := range core.PrintReport(d) {
			fmt.Fprintln(s.Out, line)
		}
		return 0
	case "paper", "load":
		if len(args) < 2 {
			s.errf("usage: lpadmin paper N")
			return 1
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			s.errf("lpadmin: paper takes a number of sheets")
			return 1
		}
		if err := d.LoadPaper(n); err != nil {
			s.errf("lpadmin: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "loaded %d sheet(s) — %d in the tray\n", n, d.Printer.Paper)
		return 0
	case "toner":
		if len(args) < 2 {
			s.errf("usage: lpadmin toner P")
			return 1
		}
		pct, err := strconv.Atoi(args[1])
		if err != nil {
			s.errf("lpadmin: toner takes a percentage")
			return 1
		}
		if err := d.SetToner(pct); err != nil {
			s.errf("lpadmin: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "toner replaced: %d%%\n", pct)
		return 0
	case "pause", "offline":
		if err := d.SetPrinterOffline(true); err != nil {
			s.errf("lpadmin: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, "printer paused: jobs stay queued")
		return 0
	case "resume", "online":
		if err := d.SetPrinterOffline(false); err != nil {
			s.errf("lpadmin: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, "printer resumed")
		return 0
	}
	s.errf("usage: lpadmin [status|paper N|toner P|pause|resume]")
	return 1
}

// countSheets is the printer's own page count: one sheet per 60 lines, which is
// what a plain-text page really holds.
func countSheets(text string) int {
	lines := strings.Count(text, "\n")
	if lines == 0 {
		return 1
	}
	sheets := (lines + 59) / 60
	if sheets < 1 {
		sheets = 1
	}
	return sheets
}

// ---- the laptop ----

func cmdLaptopctl(s *Shell, args []string) int {
	d := s.Dev
	if d.Battery == nil {
		// a desktop's power still has a story worth telling: the wall, the UPS
		// and the breaker
		for _, line := range s.W.PowerReport() {
			fmt.Fprintln(s.Out, line)
		}
		return 0
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status":
		for _, line := range core.BatteryReport(d) {
			fmt.Fprintln(s.Out, line)
		}
		return 0
	case "lid":
		if len(args) < 2 {
			s.errf("usage: laptopctl lid open|closed")
			return 1
		}
		open := args[1] == "open" || args[1] == "up"
		if err := d.SetLid(open); err != nil {
			s.errf("laptopctl: %v", err)
			return 1
		}
		if open {
			fmt.Fprintln(s.Out, "lid opened — the machine is waking up")
		} else {
			fmt.Fprintln(s.Out, "lid closed — suspended, and off the network")
		}
		return 0
	case "charge", "plug":
		on := true
		if len(args) > 1 && (args[1] == "off" || args[1] == "unplug") {
			on = false
		}
		if err := d.SetCharging(on); err != nil {
			s.errf("laptopctl: %v", err)
			return 1
		}
		if on {
			fmt.Fprintln(s.Out, "charger connected")
		} else {
			fmt.Fprintf(s.Out, "charger disconnected — running on battery (%d%%)\n", d.Battery.Pct)
		}
		return 0
	}
	s.errf("usage: laptopctl [status|lid open|closed|charge [on|off]]")
	return 1
}

// ---- the NAS's dependants ----

func cmdBackup(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: backup run [PATH] | backup status | backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
		return 1
	}
	if args[0] == "init" {
		return cmdBackupInit(s, args[1:])
	}
	nas := nasFor(s)
	if nas == nil {
		s.errf("backup: no NAS in this household")
		return 1
	}
	switch args[0] {
	case "run":
		path := "."
		if len(args) > 1 {
			path = args[1]
		}
		summary, err := s.W.RunBackup(s.Dev, nas, s.abs(path))
		if err != nil {
			s.errf("backup: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "backed up: %s\n", summary)
		return 0
	case "status", "list":
		data, ok := nas.FS.Read(core.BackupDir + "/index.log")
		if !ok || data == nil {
			fmt.Fprintln(s.Out, "no backups have run yet")
			return 0
		}
		fmt.Fprintf(s.Out, "backups on %s (%s):\n%s", nas.Hostname, core.BackupDir, string(data))
		return 0
	}
	s.errf("usage: backup run [PATH] | backup status | backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
	return 1
}

// cmdLinks answers the question a household network diagram should answer:
// what is physically connected to what, right now.
func cmdLinks(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "physical path: %s\n", s.W.LinkPath(s.Dev))
	if sw, port := s.W.SwitchOf(s.Dev); sw != nil && port != nil {
		fmt.Fprintf(s.Out, "plugged into:  %s port %d (%s)\n", sw.Hostname, port.Num, orDefaultS(port.Label, "free"))
	}
	if up, why := s.W.LinkUp(s.Dev); !up {
		fmt.Fprintf(s.Out, "link:          DOWN — %s\n", why)
	} else {
		fmt.Fprintln(s.Out, "link:          up")
	}
	for _, id := range s.W.Order {
		d := s.W.Devices[id]
		if d.Owner != s.Dev.Owner || d.ID == s.Dev.ID {
			continue
		}
		if !strings.HasPrefix(d.FirstLANIP(), core.LANSubnet) {
			continue // a stick or a phone has no household LAN address
		}
		state := "reachable"
		if !d.Powered() {
			state = "powered off"
		} else if up, why := s.W.LinkUp(d); !up {
			state = "link down (" + why + ")"
		}
		fmt.Fprintf(s.Out, "%-12s %-14s %s\n", d.Hostname, d.FirstLANIP(), state)
	}
	return 0
}

func orDefaultS(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
