package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// runVirtualScript executes a virtual binary whose body is a game-DSL shell
// script (an init script, a service unit, a player-authored helper). This is
// the "Game Shell Script" execution class from the design:
//
//	virtual binary → builtin implementation → Game Runtime → World State
//
// It is deliberately NOT a general-purpose interpreter with host access. It can
// only: read/write the virtual FS, call other virtual commands, and touch the
// simulated process/service table. There is no way to reach the host.
//
// Returns (exit code, whether it was actually run).
func (s *Shell) runVirtualScript(path string, args []string) (int, bool) {
	data, ok := s.Dev.FS.Read(path)
	if !ok {
		return 1, false
	}
	body := string(data)
	if !looksLikeScript(body) {
		return 1, false // a real binary blob: no game runtime for it
	}

	// dispatch a known init-style script: "$1 start|stop|restart|status"
	op := "start"
	if len(args) > 0 {
		op = args[0]
	}
	unit := unitFromScriptPath(path)
	if unit == "" {
		return 1, true
	}
	switch op {
	case "start":
		s.Dev.StartService(unit)
		s.Dev.Logf("info", "init", "%s: started %s", path, unit)
	case "stop":
		s.Dev.StopService(unit)
		s.Dev.Logf("info", "init", "%s: stopped %s", path, unit)
	case "restart":
		s.Dev.RestartService(unit)
		s.Dev.Logf("info", "init", "%s: restarted %s", path, unit)
		s.W.AddEvent(s.Dev.ID, "info", "init", "%s restarted via %s", unit, path)
	case "reload":
		// A reload re-reads configuration without dropping anything. For the
		// firewall that is the verb that reports what the config now says and
		// whether it is even parseable — the packet path has been reading the
		// file all along, so a reload is a check, not a switch.
		if unit == "firewall" {
			return s.reloadFirewall(), true
		}
		s.Dev.Logf("info", "init", "%s: reloaded", unit)
		fmt.Fprintf(s.Out, "%s: reloaded\n", unit)
	case "status":
		if svc := s.Dev.Svc(unit); svc != nil {
			fmt.Fprintf(s.Out, "%s is %s\n", unit, s.Dev.ServiceHealth(svc))
		} else {
			fmt.Fprintf(s.Out, "%s: not found\n", unit)
		}
	default:
		fmt.Fprintf(s.Out, "usage: %s {start|stop|restart|status}\n", path)
		return 1, true
	}
	return 0, true
}

// looksLikeScript recognises the game DSL: a shebang plus shell-ish text.
func looksLikeScript(body string) bool {
	if !strings.HasPrefix(body, "#!") {
		return false
	}
	// a shebang pointing at a shell is a script; anything else is opaque
	first := strings.SplitN(body, "\n", 2)[0]
	return strings.Contains(first, "sh")
}

// unitFromScriptPath maps /etc/init.d/dnsmasq → dnsmasq, and also
// /lib/systemd/system/foo.service → foo.
func unitFromScriptPath(path string) string {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	base = strings.TrimSuffix(base, ".service")
	return base
}

// reloadFirewall is `/etc/init.d/firewall reload`: it reports the ruleset the
// device's configuration currently describes, complains about anything it
// cannot parse, and leaves a marker with the time it last checked.
func (s *Shell) reloadFirewall() int {
	st := s.Dev.FW()
	for _, e := range st.Errors {
		s.errf("fw3: %s: %s", core.UCIPath("firewall"), e)
	}
	redirects := 0
	for _, r := range st.AllRedirects() {
		if r.Enabled {
			redirects++
		}
	}
	rules := 0
	for _, r := range st.Rules {
		if r.Src == "wan" && r.Target == "ACCEPT" {
			rules++
		}
	}
	fmt.Fprintln(s.Out, "fw3: applying configuration from "+core.UCIPath("firewall"))
	fmt.Fprintf(s.Out, "fw3: %d active redirect(s), %d WAN accept rule(s), wan_input %s\n",
		redirects, rules, st.WANInput)
	if st.UPnPEnabled {
		fmt.Fprintf(s.Out, "fw3: UPnP enabled (%d live mapping(s) from %s)\n",
			len(st.UPnPLeases), core.UPnPLeasePath)
	}
	if len(st.Errors) > 0 {
		fmt.Fprintln(s.Out, "fw3: configuration has errors — no redirects or rules are in force until they are fixed")
	}
	s.Dev.FS.Write("/var/run/firewall.applied",
		"last reload: "+s.W.Sim.Format("2006-01-02 15:04:05")+"\n", 0644, "root", "root")
	s.Dev.Logf("info", "fw3", "firewall reloaded: %d redirect(s), %d rule(s), wan_input %s",
		redirects, rules, st.WANInput)
	return 0
}
