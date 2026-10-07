package shell

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// `vm` drives the world's virtualisation. Every number it prints is read from
// world state — allocation, live swap, real disk usage, the kernel's own view —
// so the display and the consequence can never disagree.

func init() {
	builtinTable["vm"] = cmdVM
	builtinTable["worldgen"] = cmdWorldgen
}

func cmdVM(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "", "list", "ls":
		return vmList(s)
	case "top", "stats", "stat":
		return vmTop(s)
	case "create", "new":
		return vmCreate(s, args)
	case "start":
		return vmStart(s, args)
	case "stop", "shutdown":
		return vmStop(s, args)
	case "restart", "reboot":
		return vmRestart(s, args)
	case "destroy", "delete", "rm", "undefine":
		return vmDestroy(s, args)
	case "snapshot", "snap":
		return vmSnapshot(s, args)
	case "snapshots", "snaps", "snap-list":
		return vmSnapshots(s, args)
	case "restore", "rollback":
		return vmRestore(s, args)
	case "console", "enter":
		return vmConsole(s, args)
	case "host", "hosts":
		return vmHost(s, args)
	}
	s.errf("usage: vm [list|top|create NAME [--cpu N] [--mem MB] [--disk MB]|start|stop|restart|destroy NAME|snapshot NAME [SNAP]|snapshots NAME|restore NAME SNAP|console NAME|host]")
	return 1
}

// worldgen develops the world itself: routine NPC housing on the next free
// street, paid by the developer. Generated boxes are ordinary devices —
// same DNS, same firewall defaults, same safety (nothing forwarded).
func cmdWorldgen(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list", "ls":
		if len(s.W.GenNames) == 0 {
			fmt.Fprintln(s.Out, "no generated streets yet — develop one with: worldgen household ($100)")
			return 0
		}
		fmt.Fprintf(s.Out, "%-10s %-14s %s\n", "TENANT", "STREET", "ROUTER")
		for _, name := range s.W.GenNames {
			for _, id := range s.W.Order {
				d := s.W.Devices[id]
				if d != nil && d.Owner == name && d.Profile == "router" {
					fmt.Fprintf(s.Out, "%-10s %-14s %s\n", name, wanBaseOf(d), d.Hostname)
				}
			}
		}
		return 0
	case "household", "house", "new":
		name, err := s.W.GenHousehold(s.User.Name)
		if err != nil {
			s.errf("worldgen: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "developed a household for %s ($100) — router, pc, phone, all wired\n", name)
		return 0
	}
	s.errf("usage: worldgen [list|household]")
	return 1
}

func wanBaseOf(d *core.Device) string {
	for _, i := range d.Ifaces {
		if i.Zone == "lan" && i.CIDR != "" {
			return i.CIDR
		}
	}
	return "-"
}

// hypervisorFor picks the machine a guest should be created on: the one the
// player is standing on if it can host guests, otherwise the household server.
func hypervisorFor(s *Shell) *core.Device {
	if s.Dev.Services != nil && s.Dev.Services["libvirtd"] != nil {
		return s.Dev
	}
	if h := s.W.Devices["srv-alex"]; h != nil {
		return h
	}
	for _, d := range s.W.Devices {
		if d.Profile == "server" && d.Owner == s.User.Name {
			return d
		}
	}
	return nil
}

func vmList(s *Shell) int {
	guests := s.W.Guests()
	if len(guests) == 0 {
		fmt.Fprintf(s.Out, "no guests defined\n")
		if h := hypervisorFor(s); h != nil {
			fmt.Fprintf(s.Out, "hypervisor: %s (%s)\n", h.Hostname, h.HW.Model)
			fmt.Fprintf(s.Out, "create one with: vm create <name> --mem 2048 --disk 20480\n")
		} else {
			fmt.Fprintf(s.Out, "no hypervisor available on this network\n")
		}
		return 0
	}
	fmt.Fprintf(s.Out, "%-14s %-9s %-7s %-8s %-11s %-7s %-6s %-6s %s\n",
		"NAME", "STATE", "RAM", "USED", "SWAP", "DISK", "CPU", "SPEED", "NOTE")
	for _, v := range guests {
		r := core.VMRow(v)
		fmt.Fprintf(s.Out, "%-14s %-9s %-7s %-8s %-11s %-7s %-6s %-6s %s\n",
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8])
	}
	return 0
}

func vmTop(s *Shell) int {
	guests := s.W.Guests()
	if len(guests) == 0 {
		fmt.Fprintf(s.Out, "no guests running\n")
		return 0
	}
	for _, v := range guests {
		d := v.Device()
		fmt.Fprintf(s.Out, "%-14s %-9s addr %-15s %s\n", v.Name, v.State, d.FirstLANIP(), d.FirstWANIP())
		if v.State != "running" {
			fmt.Fprintf(s.Out, "    (not running — no processes, no services)\n")
			continue
		}
		fmt.Fprintf(s.Out, "    ram %d/%d MiB   swap %d/%d MiB   disk %d/%d MiB (%.0f%%)   cpu %.0f%% of %d core(s) → running at %.0f%%\n",
			v.MemUsedMB(), v.VRAMMB, v.SwapMB, v.SwapMaxMB,
			v.DiskUsedM, v.VDiskM, v.DiskPressure()*100,
			v.CPUWantPct(), int(v.VCores), v.Throttle()*100)
		if v.OOMCount > 0 {
			fmt.Fprintf(s.Out, "    oom kills: %d\n", v.OOMCount)
			for _, e := range v.OomEvents {
				fmt.Fprintf(s.Out, "      %s\n", e)
			}
		}
	}
	return 0
}

func vmCreate(s *Shell, args []string) int {
	name := ""
	cpu := 1.0
	mem := 1024
	disk := 8192
	ipMode := "public"
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (int, bool) {
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err == nil {
					i++
					return n, true
				}
			}
			return 0, false
		}
		switch {
		case strings.HasPrefix(a, "--cpu"):
			if n, ok := val(); ok {
				cpu = float64(n)
			}
		case strings.HasPrefix(a, "--mem"):
			if n, ok := val(); ok {
				mem = n
			}
		case strings.HasPrefix(a, "--disk"):
			if n, ok := val(); ok {
				disk = n
			}
		case strings.HasPrefix(a, "--ip"):
			if i+1 < len(args) {
				switch m := args[i+1]; m {
				case "public", "shared", "v6only":
					ipMode = m
					i++
				default:
					s.errf("vm create: unknown ip mode %q (public, shared, v6only)", m)
					return 1
				}
			}
		case !strings.HasPrefix(a, "-") && name == "":
			name = a
		}
	}
	if name == "" {
		s.errf("usage: vm create NAME [--cpu N] [--mem MB] [--disk MB] [--ip public|shared|v6only]")
		return 1
	}
	host := hypervisorFor(s)
	if host == nil {
		s.errf("no hypervisor on this network")
		return 1
	}
	// Cost is real money out of the household budget, like a VPS purchase.
	// It is scaled to be comparable with novapanel's plans: a small guest must
	// be affordable early, and a large one must actually cost something.
	cost := vmCost(cpu, mem, disk, ipMode)
	if s.W.Bank != nil {
		if acc := s.W.Bank.Accts[s.User.Name]; acc != nil {
			if acc.Balance < cost {
				fmt.Fprintf(s.Out, "insufficient funds: %d MiB RAM + %d MiB disk costs $%.2f, you have $%.2f\n",
					mem, disk, float64(cost)/100, float64(acc.Balance)/100)
				return 1
			}
		}
	}
	v, err := s.W.CreateVM(host.ID, name, cpu, mem, disk, ipMode)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	// the allocation is really billed to the household, not just displayed
	if s.W.Bank != nil {
		if acc := s.W.Bank.Accts[s.User.Name]; acc != nil {
			acc.Balance -= cost
			acc.Tx = append(acc.Tx, core.Tx{At: s.W.Sim, Amount: -cost,
				Memo: "vm " + name + " (allocation)", Balance: acc.Balance})
		}
	}
	d := v.Device()
	fmt.Fprintf(s.Out, "Domain %s created\n", name)
	fmt.Fprintf(s.Out, "  host:    %s (%d cores, %d MiB)\n", host.Hostname, host.HW.Cores, host.HW.RAMMB)
	fmt.Fprintf(s.Out, "  alloc:   %g vCPU, %d MiB RAM, %d MiB disk (swap %d MiB)\n", v.VCores, v.VRAMMB, v.VDiskM, v.SwapMaxMB)
	fmt.Fprintf(s.Out, "  address: %s (lan)  %s\n", d.FirstLANIP(), wanSummary(d, ipMode))
	fmt.Fprintf(s.Out, "  cost:    $%.2f charged to the household\n", float64(cost)/100)
	fmt.Fprintf(s.Out, "\nssh in with: ssh %s@%s\n", name, d.FirstLANIP())
	fmt.Fprintf(s.Out, "watch it with: vm top\n")
	return 0
}

// vmCost prices a guest in cents per month. The rates are fitted to novapanel's
// published plans so the two products cannot drift apart:
//
//	novapanel nano-shared 1 core,  512 MiB,  10 GiB, shared  -> $3.00
//	novapanel nano-1      1 core,  512 MiB,  10 GiB, public  -> $6.00
//	novapanel v6-sandbox  1 core,    1 GiB,  20 GiB, v6 only -> $4.40
//	novapanel small-2     2 core,    2 GiB,  40 GiB, public  -> $18.00
//	novapanel medium-4    4 core,    4 GiB,  80 GiB, public  -> $35.00
//
// Memory dominates, as it does on a real host; provisioned disk is charged
// lightly because it is thin. A node with no dedicated public IPv4 is billed
// half of that, because on a small plan most of the price *is* the address —
// which is exactly why the shared and v6-only plans are the cheap ones.
func vmCost(cpu float64, mem, diskMB int, ipMode string) int64 {
	const (
		coreCents = 400 // $4.00 per vCPU
		ramCents  = 80  // $0.80 per GiB
		diskCents = 20  // $0.20 per provisioned GiB
	)
	base := int64(cpu)*coreCents +
		int64(mem/1024)*ramCents +
		int64(diskMB/1024)*diskCents
	if ipMode == "shared" || ipMode == "v6only" {
		base /= 2
	}
	return base
}

// wanSummary says what the guest's WAN side really is, in the product's own
// words, so the receipt and the interface cannot disagree.
func wanSummary(d *core.Device, ipMode string) string {
	switch ipMode {
	case "shared":
		return d.SharedWANOf() + " (shared, outbound only)"
	case "v6only":
		return d.FirstWANv6() + " (v6 only, no IPv4)"
	}
	return d.FirstWANIP() + " (public)"
}

func vmOne(s *Shell, args []string, fn func(name string) error) int {
	if len(args) == 0 {
		s.errf("usage: vm <sub> NAME")
		return 1
	}
	if err := fn(args[0]); err != nil {
		s.errf("%v", err)
		return 1
	}
	return 0
}

func vmStart(s *Shell, args []string) int {
	return vmOne(s, args, func(n string) error {
		if err := s.W.StartVM(n); err != nil {
			return err
		}
		fmt.Fprintf(s.Out, "Domain %s started\n", n)
		return nil
	})
}

func vmStop(s *Shell, args []string) int {
	return vmOne(s, args, func(n string) error {
		if err := s.W.StopVM(n); err != nil {
			return err
		}
		fmt.Fprintf(s.Out, "Domain %s stopped\n", n)
		return nil
	})
}

func vmRestart(s *Shell, args []string) int {
	return vmOne(s, args, func(n string) error {
		if err := s.W.StopVM(n); err != nil {
			return err
		}
		if err := s.W.StartVM(n); err != nil {
			return err
		}
		fmt.Fprintf(s.Out, "Domain %s restarted\n", n)
		return nil
	})
}

func vmDestroy(s *Shell, args []string) int {
	return vmOne(s, args, func(n string) error {
		if err := s.W.DestroyVM(n); err != nil {
			return err
		}
		fmt.Fprintf(s.Out, "Domain %s destroyed\n", n)
		return nil
	})
}

// Snapshots are the hypervisor's own rollback: the guest's disk, users and
// packages copied at this moment, restored onto a stopped guest. Same
// contract as the provider's `vps snapshot` — a live guest can be
// snapshotted, only a stopped one can be rolled back.
func vmSnapshot(s *Shell, args []string) int {
	if len(args) == 0 || len(args) > 2 {
		s.errf("usage: vm snapshot NAME [SNAP]")
		return 1
	}
	snap := ""
	if len(args) == 2 {
		snap = args[1]
	}
	v := s.W.FindVM(args[0])
	if v == nil {
		s.errf("no such guest: %s", args[0])
		return 1
	}
	got, err := s.W.VMSnapshot(args[0], snap)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "Snapshot %s taken on %s\n", got.Name, v.Name)
	return 0
}

func vmSnapshots(s *Shell, args []string) int {
	if len(args) != 1 {
		s.errf("usage: vm snapshots NAME")
		return 1
	}
	v := s.W.FindVM(args[0])
	if v == nil {
		s.errf("no such guest: %s", args[0])
		return 1
	}
	if len(v.Snapshots) == 0 {
		fmt.Fprintf(s.Out, "no snapshots on %s\n", v.Name)
		return 0
	}
	for _, sn := range v.Snapshots {
		fmt.Fprintf(s.Out, "%-16s %s\n", sn.Name, sn.At.Format("2006-01-02 15:04"))
	}
	return 0
}

func vmRestore(s *Shell, args []string) int {
	if len(args) != 2 {
		s.errf("usage: vm restore NAME SNAP")
		return 1
	}
	v := s.W.FindVM(args[0])
	if v == nil {
		s.errf("no such guest: %s", args[0])
		return 1
	}
	if err := s.W.VMRestore(args[0], args[1]); err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "Domain %s rolled back to %s (still stopped — start it to boot)\n", v.Name, args[1])
	return 0
}

// vmConsole drops into the guest's own shell. The guest is a real device with
// its own users and filesystem, so this is the same shell a player gets from
// ssh — not a simulation of one.
func vmConsole(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: vm console NAME")
		return 1
	}
	v := s.W.FindVM(args[0])
	if v == nil {
		s.errf("no such guest: %s", args[0])
		return 1
	}
	if v.State != "running" {
		s.errf("Domain %s is not running (start it first)", v.Name)
		return 1
	}
	d := v.Device()
	u := d.FindUser(v.Name)
	if u == nil {
		u = d.FindUser("root")
	}
	if u == nil {
		s.errf("no user to log in as on %s", d.Hostname)
		return 1
	}
	return enterConsole(s, d, u, v.Name)
}

// enterConsole drops the player into a machine's own shell. A VPS console (§12)
// and a hypervisor guest console are the same thing, because in both cases the
// machine is a real Device: the console is not a simulation of one.
func enterConsole(s *Shell, d *core.Device, u *core.User, label string) int {
	fmt.Fprintf(s.Out, "Connected to %s (console). Type `exit` to return.\r\n", label)
	inner := NewShell(s.W, d, u, s.Out, d.FirstLANIP(), s.TTY)
	inner.CWD = u.Home
	for {
		line := s.ReadLineForTest()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line == "exit" || line == "\x04" {
			fmt.Fprintf(s.Out, "\r\nDisconnected from %s\r\n", label)
			return 0
		}
		inner.ExecLine(line)
	}
}

func vmHost(s *Shell, args []string) int {
	hosts := s.W.VMHosts()
	if len(hosts) == 0 {
		fmt.Fprintf(s.Out, "no hypervisors in this world\n")
		return 0
	}
	fmt.Fprintf(s.Out, "%-14s %-10s %-9s %-9s %s\n", "HOST", "PROFILE", "CORES", "RAM", "GUESTS")
	for _, h := range hosts {
		fmt.Fprintf(s.Out, "%-14s %-10s %-9d %-9d %d\n",
			h.Hostname, h.Profile, h.HW.Cores, h.HW.RAMMB, len(s.W.GuestsOn(h.ID)))
	}
	return 0
}
