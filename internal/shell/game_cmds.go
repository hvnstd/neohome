package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"su", cmdSu}, {"sudo", cmdSudo},
		{"fastfetch", cmdFastfetch}, {"neofetch", cmdFastfetch},
		{"who", cmdWho}, {"w", cmdWho}, {"clear", cmdClear}, {"history", cmdHistory},
		{"exit", cmdExit}, {"logout", cmdExit},
	} {
		builtinTable[e.name] = e.fn
	}
}

// tmux / screen: real background sessions surviving SSH disconnect.
// They are stored on the DEVICE (not the shell) so they outlive the session.

func cmdSu(s *Shell, args []string) int {
	target := "root"
	if len(args) > 0 {
		target = args[0]
	}
	u := s.Dev.FindUser(target)
	if u == nil {
		s.errf("su: unknown user %s", target)
		return 1
	}
	if u.UID == s.User.UID {
		s.User = u
		return 0
	}
	if s.User.UID != 0 {
		if !u.IsBot && u.Pass == "" {
			s.errf("su: Authentication failure")
			return 1
		}
		fmt.Fprintf(s.Out, "Password: ")
		pw := s.ReadPasswordLine("")
		if !s.verifyPassword(u, pw) {
			s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"failed su to "+u.Name, 3)
			s.errf("su: Authentication failure")
			return 1
		}
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"su "+s.User.Name+" -> "+u.Name, 1)
	s.User = u
	s.CWD = u.Home
	if s.CWD == "" {
		s.CWD = "/"
	}
	return 0
}

func cmdSudo(s *Shell, args []string) int {
	// sudo as root: just run the command as root
	if len(args) == 0 {
		s.errf("usage: sudo COMMAND")
		return 1
	}
	if !s.hasSudo() && s.User.UID != 0 {
		s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
			"sudo denied (not in sudoers)", 3)
		s.errf("sudo: %s is not in the sudoers file.  This incident will be reported.", s.User.Name)
		return 1
	}
	// sudo authenticates the INVOKING user with its own password
	if s.User.UID != 0 {
		fmt.Fprintf(s.Out, "[sudo] password for %s: ", s.User.Name)
		pw := s.ReadPasswordLine("")
		if !s.verifyPassword(s.User, pw) {
			s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"failed sudo authentication", 3)
			s.errf("Sorry, try again.")
			return 1
		}
	}
	root := s.Dev.FindUser("root")
	if root == nil {
		root = s.User
	}
	sub := &Shell{W: s.W, Dev: s.Dev, User: root, CWD: s.CWD, Env: s.Env, Out: s.Out,
		bufrd: s.bufrd, InTmux: s.InTmux, srcIP: s.srcIP, TTY: s.TTY, hist: s.hist}
	if sub.execOne(strings.Join(args, " ")) {
		return 0
	}
	return 1
}

func (s *Shell) hasSudo() bool {
	for _, g := range s.User.Groups {
		if g == "sudo" {
			return true
		}
	}
	return false
}

func cmdFastfetch(s *Shell, args []string) int {
	d := s.Dev
	fmt.Fprintf(s.Out, "%s@%s\n", s.User.Name, d.Hostname)
	fmt.Fprintf(s.Out, "OS: %s %s (%s)\n", d.OS.Distro, d.OS.Ver, d.OS.Arch)
	fmt.Fprintf(s.Out, "Host: %s (%s)\n", d.HW.Model, d.ID)
	fmt.Fprintf(s.Out, "Kernel: %s\n", d.OS.Kernel)
	fmt.Fprintf(s.Out, "Uptime: %s\n", d.Uptime().Round(time.Second))
	shellName := s.User.Shell
	if shellName == "" {
		shellName = s.Dev.OS.Shell
	}
	fmt.Fprintf(s.Out, "Shell: %s\n", shellName)
	fmt.Fprintf(s.Out, "Terminal: %s\n", s.TTY)
	fmt.Fprintf(s.Out, "CPU: %s (%d cores @ %d MHz)\n", d.HW.Model, d.HW.Cores, d.HW.CPUMHz)
	memUsed := d.MemUsed()
	memFree := d.HW.RAMMB - memUsed
	if memFree < 0 {
		memFree = 0
	}
	fmt.Fprintf(s.Out, "Memory: %d / %d MiB (%d MiB free)\n", memUsed, d.HW.RAMMB, memFree)
	diskUsed := d.FS.DiskUsedMB()
	diskFree := d.HW.DiskMB - diskUsed
	if diskFree < 0 {
		diskFree = 0
	}
	fmt.Fprintf(s.Out, "Disk: %d / %d MiB (%d MiB free)\n", diskUsed, d.HW.DiskMB, diskFree)
	packages := make([]string, 0, len(d.Installed))
	for name := range d.Installed {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	if len(packages) == 0 {
		fmt.Fprintln(s.Out, "Packages: none")
	} else {
		fmt.Fprintf(s.Out, "Packages: %s\n", strings.Join(packages, ", "))
	}
	fmt.Fprintf(s.Out, "Processes: %d\n", len(d.Procs))
	fmt.Fprintf(s.Out, "Services: %d running\n", runningServiceCount(d))

	var addresses []string
	var hasIPv6 bool
	for _, iface := range d.Ifaces {
		if !iface.Up || iface.IP == "" {
			continue
		}
		ifaceName := iface.Name
		if iface.Zone != "" {
			ifaceName += "[" + iface.Zone + "]"
		}
		addresses = append(addresses, ifaceName+"="+iface.IP)
		if strings.Contains(iface.IP, ":") {
			hasIPv6 = true
		}
	}
	if len(addresses) == 0 {
		fmt.Fprintln(s.Out, "Network: down (no active addresses)")
	} else {
		fmt.Fprintf(s.Out, "Network: %s\n", strings.Join(addresses, ", "))
	}
	if hasIPv6 {
		fmt.Fprintln(s.Out, "IPv6: configured")
	} else {
		fmt.Fprintln(s.Out, "IPv6: not configured")
	}
	return 0
}

func runningServiceCount(d *core.Device) int {
	count := 0
	for _, svc := range d.Services {
		if svc.State == "running" {
			count++
		}
	}
	return count
}

func cmdWho(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", s.User.Name, s.TTY, time.Now().Format("2006-01-02 15:04"), s.srcIP)
	for _, l := range s.Dev.Active {
		fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", l.User, l.TTY, l.At.Format("2006-01-02 15:04"), l.From)
	}
	return 0
}

func cmdClear(s *Shell, args []string) int {
	fmt.Fprint(s.Out, "\033[H\033[2J")
	return 0
}

func cmdExit(s *Shell, args []string) int {
	s.exitFlag = true
	return 0
}

var _ = time.Now
