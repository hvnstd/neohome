package shell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"neohome/internal/core"
)

// ---- process / system info ----

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"ps", cmdPs}, {"top", cmdTop}, {"htop", cmdHtop}, {"kill", cmdKill},
		{"pkill", cmdPkill}, {"nice", cmdNice}, {"free", cmdFree}, {"df", cmdDf},
		{"du", cmdDu}, {"uptime", cmdUptime}, {"lscpu", cmdLscpu}, {"lsblk", cmdLsblk},
		{"dmesg", cmdDmesg}, {"uname", cmdUname},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdPs(s *Shell, args []string) int {
	// ps aux / ps -ef / ps: all read the same real process table
	wide := false
	for _, a := range args {
		if strings.ContainsAny(a, "auxef") {
			wide = true
		}
	}
	if wide {
		fmt.Fprintf(s.Out, "%-8s %-8s %5s %5s %8s %8s %s\n", "USER", "PID", "%CPU", "%MEM", "VSZ", "RSS", "COMMAND")
	} else {
		fmt.Fprintf(s.Out, "%8s %-8s %8s %s\n", "PID", "TTY", "TIME", "CMD")
	}
	// always show what the device is actually running: pid 1 + services
	procs := append([]*core.Proc{}, s.Dev.Procs...)
	if wide {
		init := &core.Proc{Name: "init", User: "root", PID: 1, CPU: 0.0, Mem: 4, TTY: "?", State: "S"}
		procs = append([]*core.Proc{init}, procs...)
	}
	if len(procs) == 0 {
		return 0
	}
	for _, p := range procs {
		if wide {
			memPct := 0.0
			if s.Dev.HW.RAMMB > 0 {
				memPct = float64(p.Mem) / float64(s.Dev.HW.RAMMB)
			}
			fmt.Fprintf(s.Out, "%-8s %-8d %5.1f %5.1f %8d %8d %s\n",
				p.User, p.PID, p.CPU, memPct, p.Mem*1024, p.Mem*1024, p.Name)
		} else {
			fmt.Fprintf(s.Out, "%8d %-8s %7.1f %s\n", p.PID, p.TTY, p.CPU, p.Name)
		}
	}
	return 0
}

func cmdTop(s *Shell, args []string) int {
	procs := append([]*core.Proc{}, s.Dev.Procs...)
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	tr := s.Dev.Resources()
	fmt.Fprintf(s.Out, "top - %s up %s, %d user, load average: %.2f, %.2f, %.2f\n",
		s.Dev.Hostname, s.Dev.Uptime().Round(time.Second), len(s.Dev.Users), tr.Load1, tr.Load5, tr.Load15)
	fmt.Fprintf(s.Out, "Tasks: %d total, %d running, %d sleeping | Mem: %s/%d MiB | Swap: %s/%d MiB\n",
		len(s.Dev.Procs)+1, runnable(s.Dev)+1, sleeping(s.Dev), fmtMem(s.Dev.MemUsed()),
		s.Dev.HW.RAMMB, fmtMem(tr.SwapUsedMB), s.Dev.SwapTotalMB())
	fmt.Fprintf(s.Out, "%-8s %6s %6s %6s %s\n", "PID", "USER", "PR", "NI", "VIRT")
	for _, p := range procs {
		if len(procs) > 20 {
			procs = procs[:20]
		}
		_ = p
	}
	for _, p := range procs {
		fmt.Fprintf(s.Out, "%-8d %-6s %6d %6d %6d %s\n",
			p.PID, p.User, 20, p.Nice, p.Mem, p.Name)
	}
	return 0
}

func cmdHtop(s *Shell, args []string) int {
	d := s.Dev
	r := d.Resources()
	procs := append([]*core.Proc{}, d.Procs...)
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	shown := procs
	if len(shown) > 25 {
		shown = shown[:25]
	}
	// the percentages are of the whole machine, the way htop's bars are
	capacity := d.CPUCapacity()
	cpuPct := 0.0
	if capacity > 0 {
		cpuPct = cpuUsed(d) / capacity * 100
	}
	running, _ := d.LoadProcs()
	fmt.Fprintf(s.Out, "%s — up %s, %d user(s), load average: %.2f %.2f %.2f\n",
		d.Hostname, d.Uptime().Round(time.Second), len(d.Users), r.Load1, r.Load5, r.Load15)
	fmt.Fprintf(s.Out, "Tasks: %d total, %d running | %d cores, %d MHz each\n",
		len(d.Procs)+1, running+1, d.HW.Cores, d.HW.CPUMHz)
	used := d.MemUsed()
	fmt.Fprintf(s.Out, "Mem : %s/%d MiB (%.1f%%)%s\n", fmtMem(used), d.EffRAMMB(), pctOf(used, d.EffRAMMB()), quotaTag(d))
	fmt.Fprintf(s.Out, "Swp : %s/%d MiB\n", fmtMem(r.SwapUsedMB), d.SwapTotalMB())
	fmt.Fprintf(s.Out, "Cpu : %.1f%% total", cpuPct)
	if r.Share < 1 {
		fmt.Fprintf(s.Out, "  (%.0f%% of what the processes asked for — the queue is over capacity)", r.Share*100)
	}
	fmt.Fprintln(s.Out)
	fmt.Fprintf(s.Out, "%-8s %-8s %6s %6s %6s %s\n", "PID", "USER", "CPU%", "WANT%", "MEM", "CMD")
	for _, p := range shown {
		want := ""
		if p.WantCPU > p.CPU {
			want = fmt.Sprintf("%.1f", p.WantCPU)
		} else {
			want = fmt.Sprintf("%.1f", p.CPU)
		}
		fmt.Fprintf(s.Out, "%-8d %-8s %5.1f%% %5s%% %5dM %s\n", p.PID, p.User, p.CPU, want, p.Mem, p.Name)
	}
	if used > d.EffRAMMB() {
		fmt.Fprintf(s.Out, "\n! %d MiB over RAM: the kernel pages to swap, and kills the biggest process when that runs out\n", used-d.EffRAMMB())
	}
	if d.DiskFull() {
		fmt.Fprintf(s.Out, "! filesystem full (%d/%d MiB): writes are failing\n", d.FS.DiskUsedMB(), d.DiskLimitMB())
	}
	// §36: a dying disk gets its own line, because its fix is not `rm`
	if h, why := d.DiskHealth(); h != "PASSED" {
		fmt.Fprintf(s.Out, "! disk health %s: %s\n", h, why)
	}
	return 0
}

func fmtMem(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", float64(mb)/1024)
	}
	return fmt.Sprintf("%dM", mb)
}

// quotaTag marks capped machines in monitoring output, so a limit is never
// mistaken for hardware.
func quotaTag(d *core.Device) string {
	if d.Quota != nil {
		return " [quota]"
	}
	return ""
}

func pctOf(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func cmdKill(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: kill PID")
		return 1
	}
	for _, a := range args {
		pid, err := strconv.Atoi(a)
		if err != nil {
			s.errf("%s: invalid pid: %s", s.Dev.Hostname, a)
			continue
		}
		if !s.killPID(pid) {
			s.errf("%s: %s: no such process", s.Dev.Hostname, a)
		}
	}
	return 0
}

func cmdPkill(s *Shell, args []string) int {
	if len(args) == 0 {
		return 1
	}
	name := args[0]
	n := 0
	for _, p := range s.Dev.Procs {
		if p.Name == name || strings.Contains(p.Name, name) {
			n++
		}
	}
	if n == 0 {
		s.errf("%s: %s: no process matches", s.Dev.Hostname, name)
		return 1
	}
	var keep []*core.Proc
	for _, p := range s.Dev.Procs {
		if p.Name == name || strings.Contains(p.Name, name) {
			continue
		}
		keep = append(keep, p)
	}
	s.Dev.Procs = keep
	return 0
}

func cmdNice(s *Shell, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(s.Out, "0")
		return 0
	}
	n, _ := strconv.Atoi(args[0])
	fmt.Fprintf(s.Out, "%d\n", n)
	return 0
}

func cmdFree(s *Shell, args []string) int {
	d := s.Dev
	r := d.Resources()
	used := d.MemUsed()
	// the total is what the kernel may use: a capped box reports its quota,
	// the way a container-aware free reports the cgroup limit
	total := d.EffRAMMB()
	free := total - used
	if free < 0 {
		free = 0
	}
	// a kb-first view like procps, but every number is the machine's own state
	fmt.Fprintf(s.Out, "              total        used        free      shared  buff/cache   available\n")
	fmt.Fprintf(s.Out, "Mem:      %10d %10d %10d %10d %10d %10d\n",
		total*1024, used*1024, free*1024, 0, 0, free*1024)
	swapTotal, swapUsed := d.SwapTotalMB(), r.SwapUsedMB
	fmt.Fprintf(s.Out, "Swap:     %10d %10d %10d\n", swapTotal*1024, swapUsed*1024, (swapTotal-swapUsed)*1024)
	if used > total {
		fmt.Fprintf(s.Out, "\nwarning: %d MiB over RAM — the kernel is paging (%d MiB in swap, %d MiB max)\n",
			used-total, swapUsed, swapTotal)
	}
	if d.Quota != nil {
		fmt.Fprintf(s.Out, "quota: %.1f cores, %d MiB RAM of %d cores, %d MiB hardware\n",
			d.Quota.Cores, d.Quota.RAMMB, d.HW.Cores, d.HW.RAMMB)
	}
	if r.OOMCount > 0 {
		fmt.Fprintf(s.Out, "OOM incidents: %d — %s\n", r.OOMCount, r.LastOOM)
	}
	return 0
}

func cmdDf(s *Shell, args []string) int {
	d := s.Dev
	limit, used := d.DiskLimitMB(), d.FS.DiskUsedMB()
	pct := 0
	if limit > 0 {
		pct = used * 100 / limit
	}
	fmt.Fprintf(s.Out, "Filesystem      Size  Used Avail Use%% Mounted on\n")
	fmt.Fprintf(s.Out, "overlay         %5dM %5dM %5dM  %d%% /\n", limit, used, d.DiskFreeMB(), pct)
	if d.DiskFull() {
		fmt.Fprintf(s.Out, "\nwarning: no space left on device — writes fail until something is removed\n")
		fmt.Fprintf(s.Out, "  find what is big: du -a / | sort -n | tail   (or remove a file you made)\n")
	}
	// §36: wear is a different errno with a different fix — say which one it is.
	// A latched FAILED disk is failing writes right now; a bare WARNING (or a
	// FAILED inside its fsck grace) is the health, not the current verdict.
	if d.DiskFailed() {
		_, why := d.DiskHealth()
		fmt.Fprintf(s.Out, "\nwarning: disk FAILED (%s) — writes fail with I/O errors\n", why)
		fmt.Fprintf(s.Out, "  back up first, then fsck (it only buys time; see smartctl)\n")
	} else if h, why := d.DiskHealth(); h != "PASSED" {
		fmt.Fprintf(s.Out, "\ndisk health: %s (%s; see smartctl)\n", h, why)
	}
	if r := d.Resources(); r.LogDropped > 0 {
		fmt.Fprintf(s.Out, "syslog dropped %d line(s) while the disk was full\n", r.LogDropped)
	}
	return 0
}

func cmdDu(s *Shell, args []string) int {
	p := "."
	if len(args) > 0 {
		p = s.abs(args[0])
	}
	vfs, p, _ := s.ResolveVFS(p)
	if vfs == nil {
		return 1
	}
	fmt.Fprintf(s.Out, "%d\t%s\n", vfs.DiskUsedMB(), p)
	return 0
}

func cmdUptime(s *Shell, args []string) int {
	// §17: the load average is the machine's own, measured from its process
	// table — not the host's, and not a constant
	r := s.Dev.Resources()
	fmt.Fprintf(s.Out, " %s up %s, %d user,  load average: %.2f, %.2f, %.2f\n",
		s.W.Sim.Format("15:04:05"), s.Dev.Uptime().Round(time.Second), len(s.Dev.Users),
		r.Load1, r.Load5, r.Load15)
	return 0
}

func cmdLscpu(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "Architecture:        %s\n", s.Dev.OS.Arch)
	fmt.Fprintf(s.Out, "CPU op-mode(s):      32-bit, 64-bit\n")
	fmt.Fprintf(s.Out, "Byte Order:          Little Endian\n")
	fmt.Fprintf(s.Out, "CPU(s):              %d\n", s.Dev.HW.Cores)
	fmt.Fprintf(s.Out, "Model name:          %s\n", s.Dev.HW.Model)
	fmt.Fprintf(s.Out, "CPU MHz:             %d\n", s.Dev.HW.CPUMHz)
	fmt.Fprintf(s.Out, "L1d cache:           32K\n")
	fmt.Fprintf(s.Out, "L1i cache:           32K\n")
	fmt.Fprintf(s.Out, "L2 cache:            256K\n")
	fmt.Fprintf(s.Out, "L3 cache:            8192K\n")
	return 0
}

func cmdLsblk(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "NAME   MAJ:MIN RM   SIZE TYPE MOUNTPOINT\n")
	fmt.Fprintf(s.Out, "sda    8:0    0  %5dG  disk\n", s.Dev.HW.DiskMB/1024)
	fmt.Fprintf(s.Out, "sr0    11:0    0   100M  rom\n")
	return 0
}

func cmdDmesg(s *Shell, args []string) int {
	for _, l := range s.Dev.Dmesg {
		fmt.Fprintln(s.Out, l)
	}
	return 0
}

func (s *Shell) killPID(pid int) bool {
	var keep []*core.Proc
	for _, p := range s.Dev.Procs {
		if p.PID == pid {
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == len(s.Dev.Procs) {
		return false
	}
	s.Dev.Procs = keep
	return true
}

func runnable(d *core.Device) int {
	n := 0
	for _, p := range d.Procs {
		if p.State == "R" || p.State == "D" {
			n++
		}
	}
	return n
}

func sleeping(d *core.Device) int {
	n := 0
	for _, p := range d.Procs {
		if p.State == "S" {
			n++
		}
	}
	return n
}

func cpuUsed(d *core.Device) float64 {
	t := 0.0
	for _, p := range d.Procs {
		t += p.CPU
	}
	return t
}

var _ = time.Now
