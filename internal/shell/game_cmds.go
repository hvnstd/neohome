package shell

import (
	"fmt"
	"strings"
	"time"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"tmux", cmdTmux}, {"screen", cmdScreen}, {"su", cmdSu}, {"sudo", cmdSudo},
		{"fastfetch", cmdFastfetch}, {"neofetch", cmdFastfetch},
		{"who", cmdWho}, {"w", cmdWho}, {"history", cmdHistory}, {"clear", cmdClear},
		{"exit", cmdExit}, {"logout", cmdExit},
	} {
		builtinTable[e.name] = e.fn
	}
}

// tmux / screen: real background sessions surviving SSH disconnect.
// They are stored on the DEVICE (not the shell) so they outlive the session.

func cmdTmux(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "new":
		name := "session"
		if len(args) > 1 {
			name = args[1]
		}
		if _, ok := s.Dev.Sessions[name]; ok {
			fmt.Fprintf(s.Out, "sessions should be unique: %s\n", name)
			return 1
		}
		s.Dev.Sessions[name] = &core.TermSession{
			Name: name, Lines: []string{}, CWD: s.CWD, User: s.User.Name, Alive: true,
		}
		fmt.Fprintf(s.Out, "[tmux] created session %s\n", name)
		return 0
	case "ls":
		for n, sess := range s.Dev.Sessions {
			fmt.Fprintf(s.Out, "%s: %d windows (created %s)\n", n, 1, sess.Proc.Start.Format("15:04:05"))
		}
		return 0
	case "attach":
		name := "session"
		if len(args) > 1 {
			name = args[1]
		}
		sess, ok := s.Dev.Sessions[name]
		if !ok {
			fmt.Fprintf(s.Out, "no sessions\n")
			return 1
		}
		for _, l := range sess.Lines {
			fmt.Fprintln(s.Out, l)
		}
		fmt.Fprintf(s.Out, "[tmux] attached to %s\n", name)
		s.InTmux = name
		return 0
	case "kill-session":
		name := "session"
		if len(args) > 1 {
			name = args[1]
		}
		delete(s.Dev.Sessions, name)
		fmt.Fprintf(s.Out, "[tmux] killed session %s\n", name)
		return 0
	default:
		fmt.Fprintln(s.Out, "usage: tmux new|ls|attach|kill-session [name]")
		return 1
	}
}

func cmdScreen(s *Shell, args []string) int {
	return cmdTmux(s, args)
}

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
		fmt.Fprintf(s.Out, "Password: ")
		s.ReadPasswordLine("")
		s.errf("su: Authentication failure")
		return 1
	}
	s.User = u
	return 0
}

func cmdSudo(s *Shell, args []string) int {
	// sudo as root: just run the command as root
	if len(args) == 0 {
		s.errf("usage: sudo COMMAND")
		return 1
	}
	if !s.hasSudo() && s.User.UID != 0 {
		s.errf("sudo: a terminal is required to read the password; try su first")
		return 1
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
	fmt.Fprintf(s.Out, "        %s\n", d.Hostname)
	fmt.Fprintf(s.Out, "-----------------")
	fmt.Fprintf(s.Out, "OS: %s %s\n", d.OS.Distro, d.OS.Ver)
	fmt.Fprintf(s.Out, "Host: %s (%s)\n", d.HW.Model, d.ID)
	fmt.Fprintf(s.Out, "Kernel: %s\n", d.OS.Kernel)
	fmt.Fprintf(s.Out, "Uptime: %s\n", d.Uptime().Round(time.Second))
	fmt.Fprintf(s.Out, "Terminal: %s\n", s.TTY)
	fmt.Fprintf(s.Out, "CPU: %s (%d cores @ %d MHz)\n", d.HW.Model, d.HW.Cores, d.HW.CPUMHz)
	fmt.Fprintf(s.Out, "Memory: %d MiB in use (%d MiB free)\n", d.MemUsed(), d.HW.RAMMB-d.MemUsed())
	fmt.Fprintf(s.Out, "Disk: %d MiB / %d MiB\n", d.FS.DiskUsedMB(), d.HW.DiskMB)
	fmt.Fprintf(s.Out, "Packages: %d\n", len(d.Installed))
	fmt.Fprintf(s.Out, "Processes: %d\n", len(d.Procs))
	if ip := d.FirstLANIP(); ip != "" {
		fmt.Fprintf(s.Out, "IP: %s\n", ip)
	}
	if wan := d.FirstWANIP(); wan != "" {
		fmt.Fprintf(s.Out, "IPv6: ::\n")
		fmt.Fprintf(s.Out, "Public: %s\n", wan)
	}
	return 0
}

func cmdWho(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", s.User.Name, s.TTY, time.Now().Format("2006-01-02 15:04"), s.srcIP)
	for _, l := range s.Dev.Active {
		fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", l.User, l.TTY, l.At.Format("2006-01-02 15:04"), l.From)
	}
	return 0
}

func cmdHistory(s *Shell, args []string) int {
	for i, h := range s.hist {
		fmt.Fprintf(s.Out, "%4d  %s\n", i+1, h)
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
