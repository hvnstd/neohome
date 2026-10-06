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
		{"systemctl", cmdSystemctl}, {"service", cmdService},
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

func findPkg(w *core.World, name string) *core.VPkg {
	for _, r := range w.Repos {
		if p := r.Pkgs[name]; p != nil {
			return p
		}
	}
	return nil
}

var _ = strings.Contains
