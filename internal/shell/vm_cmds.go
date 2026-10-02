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
	case "console", "enter":
		return vmConsole(s, args)
	case "host", "hosts":
		return vmHost(s, args)
	}
	s.errf("usage: vm [list|top|create NAME [--cpu N] [--mem MB] [--disk MB]|start|stop|restart|destroy NAME|console NAME|host]")
	return 1
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
		case !strings.HasPrefix(a, "-") && name == "":
			name = a
		}
	}
	if name == "" {
		s.errf("usage: vm create NAME [--cpu N] [--mem MB] [--disk MB]")
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
	cost := vmCost(cpu, mem, disk)
	if s.W.Bank != nil {
		if acc := s.W.Bank.Accts[s.User.Name]; acc != nil {
			if acc.Balance < cost {
				fmt.Fprintf(s.Out, "insufficient funds: %d MiB RAM + %d MiB disk costs $%.2f, you have $%.2f\n",
					mem, disk, float64(cost)/100, float64(acc.Balance)/100)
				return 1
			}
		}
	}
	v, err := s.W.CreateVM(host.ID, name, cpu, mem, disk)
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
	fmt.Fprintf(s.Out, "  address: %s (lan)  %s (public)\n", d.FirstLANIP(), d.FirstWANIP())
	fmt.Fprintf(s.Out, "  cost:    $%.2f charged to the household\n", float64(cost)/100)
	fmt.Fprintf(s.Out, "\nssh in with: ssh %s@%s\n", name, d.FirstLANIP())
	fmt.Fprintf(s.Out, "watch it with: vm top\n")
	return 0
}

// vmCost prices a guest in cents per month. The rates are fitted to novapanel's
// published plans so the two products cannot drift apart:
//
//	novapanel nano-1   1 core, 512 MiB,  10 GiB  -> $6.00
//	novapanel small-2  2 core, 2 GiB,   40 GiB  -> $18.00
//	novapanel medium-4 4 core, 4 GiB,   80 GiB  -> $35.00
//
// The formula reproduces all three to within 40 cents. Memory dominates, as it
// does on a real host; provisioned disk is charged lightly because it is thin.
func vmCost(cpu float64, mem, diskMB int) int64 {
	const (
		coreCents = 400 // $4.00 per vCPU
		ramCents  = 80  // $0.80 per GiB
		diskCents = 20  // $0.20 per provisioned GiB
	)
	return int64(cpu)*coreCents +
		int64(mem/1024)*ramCents +
		int64(diskMB/1024)*diskCents
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
	fmt.Fprintf(s.Out, "Connected to %s (console). Type `exit` to return.\r\n", v.Name)
	inner := NewShell(s.W, d, u, s.Out, d.FirstLANIP(), s.TTY)
	inner.CWD = u.Home
	for {
		line := s.ReadLineForTest()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line == "exit" || line == "\x04" {
			fmt.Fprintf(s.Out, "\r\nDisconnected from %s\r\n", v.Name)
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
