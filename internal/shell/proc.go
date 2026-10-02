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
	fmt.Fprintf(s.Out, "top - %s up %s, %d user, load average: 0.00, 0.01, 0.05\n",
		s.Dev.Hostname, s.Dev.Uptime().Round(time.Second), 1)
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
	procs := append([]*core.Proc{}, s.Dev.Procs...)
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	if len(procs) > 25 {
		procs = procs[:25]
	}
	fmt.Fprintf(s.Out, "Mem: %d/%d MiB\n", s.Dev.MemUsed(), s.Dev.HW.RAMMB)
	fmt.Fprintf(s.Out, "CPU: %.1f%%\n", cpuUsed(s.Dev))
	fmt.Fprintf(s.Out, "Uptime: %s\n\n", s.Dev.Uptime().Round(time.Second))
	fmt.Fprintf(s.Out, "%-8s %-8s %6s %6s %s\n", "PID", "USER", "CPU", "MEM", "CMD")
	for _, p := range procs {
		fmt.Fprintf(s.Out, "%-8d %-8s %5.1f%% %5d %s\n", p.PID, p.User, p.CPU, p.Mem, p.Name)
	}
	return 0
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
	fmt.Fprintf(s.Out, "              total        used        free      shared  buff/cache   available\n")
	fmt.Fprintf(s.Out, "Mem:      %10d %10d %10d %10d %10d %10d\n",
		s.Dev.HW.RAMMB*1024, s.Dev.MemUsed()*1024, (s.Dev.HW.RAMMB-s.Dev.MemUsed())*1024,
		0, 0, (s.Dev.HW.RAMMB-s.Dev.MemUsed())*1024)
	fmt.Fprintf(s.Out, "Swap:     %10d %10d %10d\n", s.Dev.HW.RAMMB*512, 0, s.Dev.HW.RAMMB*512)
	return 0
}

func cmdDf(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "Filesystem      Size  Used Avail Use%% Mounted on\n")
	fmt.Fprintf(s.Out, "overlay         %5dM %5dM %5dM  %d%% /\n",
		s.Dev.HW.DiskMB, s.Dev.FS.DiskUsedMB(), s.Dev.HW.DiskMB-s.Dev.FS.DiskUsedMB(),
		(s.Dev.FS.DiskUsedMB()*100)/s.Dev.HW.DiskMB)
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
	fmt.Fprintf(s.Out, " %s up %s, %d user,  load average: 0.00, 0.01, 0.05\n",
		time.Now().Format("15:04:05"), s.Dev.Uptime().Round(time.Second), 1)
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

func cpuUsed(d *core.Device) float64 {
	t := 0.0
	for _, p := range d.Procs {
		t += p.CPU
	}
	return t
}

var _ = time.Now
