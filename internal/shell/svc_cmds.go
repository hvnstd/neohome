package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"systemctl", cmdSystemctl}, {"service", cmdService}, {"apt", cmdApt},
		{"apk", cmdApk}, {"pacman", cmdPacman}, {"dnf", cmdDnf},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdSystemctl(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: systemctl START|STOP|RESTART unit")
		return 1
	}
	op := args[0]
	name := args[1]
	svc := s.Dev.Svc(name)
	if svc == nil {
		s.errf("Unit %s.service not found.", name)
		return 1
	}
	switch op {
	case "start":
		msg, err := s.Dev.StartService(name)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "● %s.service - %s\n", name, svc.Desc)
		fmt.Fprintf(s.Out, "     Active: %s\n", svc.State)
		fmt.Fprintf(s.Out, "   Main PID: %d (%s)\n", svc.PID, msg)
	case "stop":
		msg, err := s.Dev.StopService(name)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "● %s.service - %s\n", name, svc.Desc)
		fmt.Fprintf(s.Out, "     Active: %s\n", svc.State)
		fmt.Fprintf(s.Out, "    (stopped: %s)\n", msg)
	case "restart":
		msg, err := s.Dev.RestartService(name)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "● %s.service - %s\n", name, svc.Desc)
		fmt.Fprintf(s.Out, "     Active: %s\n", svc.State)
		fmt.Fprintf(s.Out, "    (restarted: %s)\n", msg)
	case "status":
		fmt.Fprintf(s.Out, "● %s.service - %s\n", name, svc.Desc)
		fmt.Fprintf(s.Out, "     Active: %s (%s)\n", svc.State, s.Dev.ServiceHealth(svc))
		fmt.Fprintf(s.Out, "   Main PID: %d\n", svc.PID)
	default:
		s.errf("unknown operation: %s", op)
		return 1
	}
	return 0
}

func cmdService(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: service UNIT {start|stop|restart}")
		return 1
	}
	return cmdSystemctl(s, []string{args[1], args[0]})
}

func cmdApt(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "update":
		fmt.Fprintln(s.Out, "Get:1 http://mirror.neohome.example/debian stable InRelease")
		fmt.Fprintln(s.Out, "Get:2 http://mirror.neohome.example/debian stable-updates InRelease")
		fmt.Fprintln(s.Out, "Fetched 0 B in 1s (0 B/s)")
		fmt.Fprintln(s.Out, "Reading package lists... Done")
		return 0
	case "install":
		if len(args) < 2 {
			s.errf("usage: apt install PKG")
			return 1
		}
		for _, pkgName := range args[1:] {
			pkg := findPkg(s.W, pkgName)
			if pkg == nil {
				fmt.Fprintf(s.Out, "E: Unable to locate package %s\n", pkgName)
				continue
			}
			fmt.Fprintf(s.Out, "Reading package lists... Done\n")
			fmt.Fprintf(s.Out, "Building dependency tree... Done\n")
			fmt.Fprintf(s.Out, "The following NEW packages will be installed:   %s\n", pkgName)
			fmt.Fprintf(s.Out, "0 upgraded, %d newly installed, 0 to remove and 0 not to upgrade.\n", 1)
			fmt.Fprintf(s.Out, "Need to get 0 B of archives.\n")
			fmt.Fprintf(s.Out, "After this operation, %d kB of additional disk space will be used.\n", pkg.Size)
			fmt.Fprintf(s.Out, "Selecting previously unselected package %s.\n", pkgName)
			fmt.Fprintf(s.Out, "(Reading database ... %d files and directories currently installed.)\n", 0)
			s.Dev.Installed[pkgName] = pkg
			for _, a := range s.Dev.InstallPkg(pkg) {
				fmt.Fprintf(s.Out, " * %s\n", a)
			}
		}
		return 0
	case "list":
		for _, r := range s.W.Repos {
			for n := range r.Pkgs {
				fmt.Fprintln(s.Out, n)
			}
		}
		return 0
	default:
		fmt.Fprintln(s.Out, "apt: unknown subcommand:", sub)
		return 1
	}
}

func cmdApk(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "update":
		fmt.Fprintln(s.Out, "fetch http://mirror.neohome.example/alpine/v3.20/main/APKINDEX.tar.gz")
		return 0
	case "add":
		if len(args) < 2 {
			s.errf("usage: apk add PKG")
			return 1
		}
		pkg := findPkg(s.W, args[1])
		if pkg == nil {
			fmt.Fprintf(s.Out, "ERROR: not found: %s\n", args[1])
			return 1
		}
		fmt.Fprintf(s.Out, "Downloading %s-%s\n", pkg.Name, pkg.Version)
		s.Dev.Installed[pkg.Name] = pkg
		for _, a := range s.Dev.InstallPkg(pkg) {
			fmt.Fprintf(s.Out, " * %s\n", a)
		}
		return 0
	default:
		fmt.Fprintln(s.Out, "apk: unknown subcommand:", sub)
		return 1
	}
}

func cmdPacman(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "-S":
		if len(args) < 2 {
			return 1
		}
		pkg := findPkg(s.W, args[1])
		if pkg == nil {
			fmt.Fprintf(s.Out, "error: package '%s' not found\n", args[1])
			return 1
		}
		fmt.Fprintf(s.Out, " :: Synchronizing package cores...\n :: Starting core/pacman...\n")
		fmt.Fprintf(s.Out, " (%s) New Version:  %s-%s\n", pkg.Name, pkg.Name, pkg.Version)
		fmt.Fprintf(s.Out, " :: Full Version Upgrade: there is nothing to do\n")
		s.Dev.Installed[pkg.Name] = pkg
		for _, a := range s.Dev.InstallPkg(pkg) {
			fmt.Fprintf(s.Out, " * %s\n", a)
		}
		return 0
	default:
		fmt.Fprintln(s.Out, "pacman: unknown subcommand:", sub)
		return 1
	}
}

func cmdDnf(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "install":
		if len(args) < 2 {
			return 1
		}
		pkg := findPkg(s.W, args[1])
		if pkg == nil {
			fmt.Fprintf(s.Out, "Error: Unable to find package %s\n", args[1])
			return 1
		}
		fmt.Fprintf(s.Out, "Last metadata expiration check: 0:00:00 ago\n")
		fmt.Fprintf(s.Out, "Package %s-%s.%s will be installed\n", pkg.Name, pkg.Version, pkg.Arch)
		s.Dev.Installed[pkg.Name] = pkg
		for _, a := range s.Dev.InstallPkg(pkg) {
			fmt.Fprintf(s.Out, " * %s\n", a)
		}
		return 0
	default:
		fmt.Fprintln(s.Out, "dnf: unknown subcommand:", sub)
		return 1
	}
}

func findPkg(w *core.World, name string) *core.VPkg {
	for _, r := range w.Repos {
		if p := r.Pkgs[name]; p != nil {
			return p
		}
	}
	return nil
}

var _ = strings.Contains
