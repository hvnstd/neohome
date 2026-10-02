package shell

import (
	"fmt"
	"strings"
)

// logread is BusyBox's log reader. On OpenWrt-style devices syslog lives in a
// ring buffer served by syslogd; on full systems the same events land in
// /var/log/syslog. Both are real game state — this is the command that turns a
// fault into an explainable causal chain.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"logread", cmdLogread}, {"journalctl", cmdJournalctl},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdLogread(s *Shell, args []string) int {
	return printLogs(s, args, "logread")
}

func cmdJournalctl(s *Shell, args []string) int {
	return printLogs(s, args, "journalctl")
}

func printLogs(s *Shell, args []string, tool string) int {
	var filter string
	follow := false
	n := 0
	for _, a := range args {
		switch {
		case a == "-f" || a == "--follow":
			follow = true
		case a == "-n" || a == "--lines":
			// handled by the following bare number below
		case strings.HasPrefix(a, "-"):
			// ignore other flags rather than treating them as a filter
		default:
			if n, _ = parseIntOr(a, n); a != "" && isAllDigits(a) {
				continue
			}
			if filter == "" {
				filter = a
			}
		}
	}

	if s.Dev.Svc("syslogd") == nil && s.Dev.Svc("rsyslog") == nil && s.Dev.Svc("systemd-journald") == nil {
		// not every device runs a syslog daemon; say so honestly
		if _, ok := s.Dev.FS.Read("/var/log/syslog"); !ok {
			fmt.Fprintf(s.Out, "%s: no syslog daemon running on %s\n", tool, s.Dev.Hostname)
			return 1
		}
	}
	data, ok := s.Dev.FS.Read("/var/log/syslog")
	if !ok || len(strings.TrimSpace(string(data))) == 0 {
		fmt.Fprintf(s.Out, "%s: log is empty\n", tool)
		return 0
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, l := range lines {
		if filter != "" && !strings.Contains(strings.ToLower(l), strings.ToLower(filter)) {
			continue
		}
		fmt.Fprintln(s.Out, l)
	}
	if follow {
		fmt.Fprintf(s.Out, "-- following %s (game time advances; re-run to refresh) --\n", s.Dev.Hostname)
	}
	return 0
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseIntOr(s string, def int) (int, bool) {
	if !isAllDigits(s) {
		return def, false
	}
	v := 0
	for _, c := range s {
		v = v*10 + int(c-'0')
	}
	return v, true
}
