package shell

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"neohome/internal/core"
)

// §33 防守和安全软件, the operator's side of it.
//
// Every command here is the real tool's interface over real state:
//
//	fail2ban-client status      what the jails have counted and banned
//	suricata -T                 the rules the engine actually loaded
//	aide --init/--check         the integrity database and a comparison
//	clamscan / freshclam        a scan against the loaded database, an update
//	monit status                what the watchdog is watching and restarting
//	ausearch / auditctl         the kernel audit records and their rules
//	secstat                     the world's own roll-up of all of the above
//
// The tools are installed software: a machine that never installed fail2ban
// answers `fail2ban-client: command not found`, exactly like a real one. The
// gate is pkgBinaries below — the same virtual-binary rule the rest of the
// shell uses.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"fail2ban-client", cmdFail2BanClient}, {"suricata", cmdSuricata},
		{"aide", cmdAide}, {"clamscan", cmdClamscan}, {"freshclam", cmdFreshclam},
		{"monit", cmdMonit}, {"ausearch", cmdAusearch}, {"auditctl", cmdAuditctl},
		{"secstat", cmdSecstat},
		// backup and host monitoring are §33 tools like the rest: their
		// commands live in backup_cmds.go, their files in pkgBinaries below
	} {
		builtinTable[e.name] = e.fn
	}
}

// pkgBinaries maps a §33 command to the file its package installs. A command
// whose file is missing is not on this machine: that is what installing is for.
var pkgBinaries = map[string]string{
	"fail2ban-client": "/usr/bin/fail2ban-client",
	"suricata":        "/usr/bin/suricata",
	"aide":            "/usr/bin/aide",
	"clamscan":        "/usr/bin/clamscan",
	"freshclam":       "/usr/bin/freshclam",
	"monit":           "/usr/bin/monit",
	"ausearch":        "/usr/bin/ausearch",
	"auditctl":        "/usr/bin/auditctl",
	"restic":          "/usr/bin/restic",
	"rkhunter":        "/usr/bin/rkhunter",
	"mysql":           "/usr/bin/mysql",
}

// pkgCommandMissing reports whether a §33 command is absent from this machine
// because its package was never installed.
func (s *Shell) pkgCommandMissing(name string) bool {
	bin, ok := pkgBinaries[name]
	if !ok {
		return false
	}
	n, has := s.Dev.FS.Get(bin)
	return !has || n.Mode.Perm()&0111 == 0
}

// ---- fail2ban --------------------------------------------------------------

func cmdFail2BanClient(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("Usage: fail2ban-client [OPTIONS] <COMMAND>")
		return 1
	}
	switch args[0] {
	case "status":
		if len(args) > 1 {
			// a single jail's own report
			want := args[1]
			found := false
			for _, j := range s.Dev.Jails() {
				if j.Name != want {
					continue
				}
				found = true
				fmt.Fprintf(s.Out, "Status for the jail: %s\r\n", j.Name)
				fmt.Fprintf(s.Out, "|- Filter\r\n")
				fmt.Fprintf(s.Out, "|  |- Currently failed:\t%d\r\n", s.jailFailed(j))
				fmt.Fprintf(s.Out, "|  `- Total failed:\t%d\r\n", len(s.Dev.Sec().Fails))
				fmt.Fprintf(s.Out, "`- Actions\r\n")
				fmt.Fprintf(s.Out, "   |- Currently banned:\t%d\r\n", len(s.Dev.ActiveBans()))
				fmt.Fprintf(s.Out, "   `- Total banned:\t%d\r\n", s.Dev.Sec().BanCount)
			}
			if !found {
				s.errf("Sorry but the jail '%s' does not exist", want)
				return 1
			}
			return 0
		}
		if !s.serviceUp("fail2ban") {
			s.errf("Failed to access socket path: /var/run/fail2ban/fail2ban.sock. Is fail2ban running?")
			return 1
		}
		for _, line := range s.Dev.Fail2BanReport() {
			fmt.Fprintf(s.Out, "%s\r\n", line)
		}
		return 0
	case "set":
		// fail2ban-client set <jail> banip|unbanip <ip>
		if len(args) < 4 {
			s.errf("Usage: fail2ban-client set <JAIL> banip|unbanip <IP>")
			return 1
		}
		jail, verb, ip := args[1], args[2], args[3]
		switch verb {
		case "banip":
			ban := s.Dev.Ban(ip, "", "manual ban (fail2ban-client, jail "+jail+")", 10*time.Minute)
			fmt.Fprintf(s.Out, "%s\r\n", ban.IP)
			return 0
		case "unbanip":
			if s.Dev.Unban(ip) {
				fmt.Fprintf(s.Out, "%s\r\n", ip)
				return 0
			}
			s.errf("IP %s is not banned in jail %s", ip, jail)
			return 1
		}
		s.errf("Invalid command: %s", verb)
		return 1
	case "reload":
		if !s.serviceUp("fail2ban") {
			s.errf("Failed to access socket path: /var/run/fail2ban/fail2ban.sock. Is fail2ban running?")
			return 1
		}
		fmt.Fprintf(s.Out, "restarted jail(s): %s\r\n", strings.Join(jailNameList(s.Dev), ", "))
		return 0
	case "version":
		fmt.Fprintf(s.Out, "1.1.0\r\n")
		return 0
	}
	s.errf("Invalid command: %s", args[0])
	return 1
}

func jailNameList(d *core.Device) []string {
	var out []string
	for _, j := range d.Jails() {
		out = append(out, j.Name)
	}
	return out
}

func (s *Shell) jailFailed(j core.Jail) int {
	cut := s.W.Sim.Add(-j.FindTime)
	n := 0
	for _, at := range s.Dev.Sec().Fails {
		if !at.Before(cut) {
			n++
		}
	}
	return n
}

// ---- suricata --------------------------------------------------------------

func cmdSuricata(s *Shell, args []string) int {
	test := false
	for _, a := range args {
		if a == "-T" || a == "--test" {
			test = true
		}
	}
	if !test {
		s.errf("Usage: suricata [-T]   (rule configuration lives in /etc/suricata/rules)")
		return 1
	}
	fmt.Fprintf(s.Out, "i: Suricata version 7.0.5\r\n")
	for _, line := range s.Dev.IDSReport() {
		fmt.Fprintf(s.Out, "i: %s\r\n", line)
	}
	if bad := badIDSRules(s.Dev); len(bad) > 0 {
		for _, b := range bad {
			fmt.Fprintf(s.Out, "e: rule parse error: %s\r\n", b)
		}
		fmt.Fprintf(s.Out, "E: configuration test failed (%d bad rule(s))\r\n", len(bad))
		return 1
	}
	if !s.serviceUp("suricata") {
		s.errf("E: engine is not running (service state: %s)", s.serviceState("suricata"))
		return 1
	}
	fmt.Fprintf(s.Out, "i: engine is running\r\n")
	return 0
}

// badIDSRules reports rules the engine cannot use — the honest test a `-T` run
// performs instead of always saying OK.
func badIDSRules(d *core.Device) []string {
	var out []string
	for _, r := range d.IDSRules() {
		switch r.Kind {
		case "portscan":
			if r.Ports < 2 {
				out = append(out, r.Raw+": portscan needs ports>=2")
			}
		case "authfail":
			if r.Fails < 1 {
				out = append(out, r.Raw+": authfail needs fails>=1")
			}
		case "exploit":
		default:
			out = append(out, r.Raw+": unknown rule kind "+r.Kind)
		}
	}
	return out
}

// ---- aide ------------------------------------------------------------------

func cmdAide(s *Shell, args []string) int {
	mode := "--check"
	if len(args) > 0 {
		mode = args[0]
	}
	cfg := core.AideConfOf(s.Dev)
	if len(cfg.Dirs) == 0 {
		s.errf("Couldn't open file /etc/aide/aide.conf")
		return 1
	}
	switch mode {
	case "--init", "-i":
		files, sums := s.Dev.FileHashes(cfg.Dirs)
		sec := s.Dev.Sec()
		sec.AideDB = sums
		sec.AideReported = map[string]string{}
		fmt.Fprintf(s.Out, "Number of entries:\t%d\r\nAdded entries:\t\t%d\r\n", len(files), len(files))
		s.Dev.Logf("info", "aide", "database initialized: %d file(s)", len(files))
		return 0
	case "--check", "-c":
		if len(s.Dev.Sec().AideDB) == 0 {
			s.errf("AIDE database not initialized — run `aide --init` first")
			return 1
		}
		diffs := s.Dev.AideDiff()
		if len(diffs) == 0 {
			fmt.Fprintf(s.Out, "AIDE found NO differences between database and filesystem\r\n")
			return 0
		}
		for _, d := range diffs {
			fmt.Fprintf(s.Out, "%s  %s\r\n", d.Kind, d.Path)
		}
		fmt.Fprintf(s.Out, "AIDE found differences: %d\r\n", len(diffs))
		return 2
	case "--status", "-s":
		fmt.Fprintf(s.Out, "watched: %s\r\ninterval: %s\r\nentries: %d\r\n",
			strings.Join(cfg.Dirs, ", "), core.HumanAge(cfg.Interval), len(s.Dev.Sec().AideDB))
		return 0
	}
	s.errf("Unknown option: %s (use --init, --check or --status)", mode)
	return 1
}

// ---- clamav ----------------------------------------------------------------

func cmdFreshclam(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "ClamAV update process started at %s\r\n", s.W.Sim.Format("Mon Jan  2 15:04:05 2006"))
	body, err := s.W.FetchHTTP(s.Dev, "http://mirror.neohome.example/clamav/main.db")
	if err != nil {
		s.errf("WARNING: Can't download main.db from mirror.neohome.example: %v", err)
		return 1
	}
	s.Dev.FS.MkdirAll("/var/lib/clamav", 0755, "root", "root")
	if data, _ := s.Dev.FS.Read("/var/lib/clamav/main.db"); len(data) == len(body) && string(data) == string(body) {
		fmt.Fprintf(s.Out, "main.db database is up to date (version: %d signatures)\r\n", strings.Count(string(body), "\n")-2)
		return 0
	}
	if err := s.Dev.WriteGuest("/var/lib/clamav/main.db", body, s.User); err != nil {
		s.errf("ERROR: Can't write to /var/lib/clamav/main.db: %v", err)
		return 1
	}
	s.Dev.Logf("info", "freshclam", "virus database updated from the mirror (%d signatures)", strings.Count(string(body), "\n")-2)
	fmt.Fprintf(s.Out, "Downloading main.db [100%%]\r\nmain.db updated (version: %d signatures)\r\n",
		strings.Count(string(body), "\n")-2)
	return 0
}

func cmdClamscan(s *Shell, args []string) int {
	recursive := false
	move := ""
	var paths []string
	for _, a := range args {
		switch {
		case a == "-r":
			recursive = true
		case strings.HasPrefix(a, "--move="):
			// clamscan's own option: infected files are moved to DIRECTORY
			move = strings.TrimPrefix(a, "--move=")
		case strings.HasPrefix(a, "--"):
			// --infected, --quiet and friends change output volume only
		case strings.HasPrefix(a, "-"):
		default:
			paths = append(paths, s.abs(a))
		}
	}
	if len(paths) == 0 {
		s.errf("Usage: clamscan [-r] FILE|DIR")
		return 1
	}
	files := s.expandScanPaths(paths, recursive)
	results, scanned, err := s.Dev.ScanFiles(files)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	for _, r := range results {
		fmt.Fprintf(s.Out, "%s: %s FOUND\r\n", r.Path, r.Virus)
		if move != "" {
			dest := s.Dev.Quarantine(r, s.User.Name)
			if move != core.MalwareDir {
				// the operator asked for their own directory: move it there
				if n, ok := s.Dev.FS.Get(dest); ok {
					dest2 := s.abs(move) + "/" + path.Base(dest)
					s.Dev.FS.MkdirAll(path.Dir(dest2), 0700, "root", "root")
					s.Dev.FS.Write(dest2, string(n.Data), 0600, "root", "root")
					s.Dev.FS.Remove(dest)
					s.Dev.Sec().Quarantined = append(s.Dev.Sec().Quarantined, dest2)
				}
			}
			fmt.Fprintf(s.Out, "%s: moved to quarantine\r\n", r.Path)
		}
	}
	fmt.Fprintf(s.Out, "\r\n----------- SCAN SUMMARY -----------\r\n")
	fmt.Fprintf(s.Out, "Known viruses: %d\r\nEngine version: 1.3.1\r\nScanned files: %d\r\nInfected files: %d\r\n",
		len(s.Dev.MalwareDB()), scanned, len(results))
	if len(results) == 0 {
		return 0
	}
	return 1
}

// expandScanPaths walks the directories the scan was pointed at. A path that
// does not exist is reported, not skipped silently.
func (s *Shell) expandScanPaths(paths []string, recursive bool) []string {
	var out []string
	for _, p := range paths {
		n, ok := s.Dev.FS.Get(p)
		if !ok {
			s.errf("Can't access file %s", p)
			continue
		}
		if !n.IsDir {
			out = append(out, p)
			continue
		}
		var walk func(dir string)
		walk = func(dir string) {
			for _, child := range s.Dev.FS.List(dir) {
				if c, ok := s.Dev.FS.Get(child); ok && c.IsDir {
					if recursive {
						walk(child)
					}
					continue
				}
				out = append(out, child)
			}
		}
		walk(p)
	}
	sort.Strings(out)
	return out
}

// ---- monit -----------------------------------------------------------------

func cmdMonit(s *Shell, args []string) int {
	mode := "status"
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "summary":
		fmt.Fprintf(s.Out, "%-16s %-10s %s\r\n", "Service", "Status", "Watch")
		for _, c := range core.MonitConfOf(s.Dev) {
			state := s.serviceState(c.Service)
			fmt.Fprintf(s.Out, "%-16s %-10s maxdown=%d interval=%s action=%s\r\n",
				c.Service, state, c.MaxDown, core.HumanAge(c.Interval), c.Action)
		}
		if len(core.MonitConfOf(s.Dev)) == 0 {
			fmt.Fprintln(s.Out, "monit: no checks configured in /etc/monit/monitrc")
		}
		return 0
	case "status":
		if !s.serviceUp("monit") {
			s.errf("monit: the control file is not readable (/var/run/monit.pid missing)")
			return 1
		}
		for _, c := range core.MonitConfOf(s.Dev) {
			fmt.Fprintf(s.Out, "The service '%s' is %s\r\n", c.Service, s.serviceState(c.Service))
			if svc := s.Dev.Svc(c.Service); svc != nil && svc.MonitDown > 0 {
				fmt.Fprintf(s.Out, "  failed checks: %d (maxdown %d)\r\n", svc.MonitDown, c.MaxDown)
			}
		}
		return 0
	}
	s.errf("Usage: monit [status|summary]")
	return 1
}

func (s *Shell) serviceState(name string) string {
	if svc := s.Dev.Svc(name); svc != nil {
		return svc.State
	}
	return "not-present"
}

func (s *Shell) serviceUp(name string) bool { return s.serviceState(name) == "running" }

// ---- auditd ----------------------------------------------------------------

func cmdAusearch(s *Shell, args []string) int {
	key := ""
	limit := 20
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-k", "-w":
			if i+1 < len(args) {
				key = args[i+1]
				i++
			}
		case "-n":
			if i+1 < len(args) {
				limit, _ = strconv.Atoi(args[i+1])
				i++
			}
		}
	}
	if !s.serviceUp("auditd") {
		s.errf("ausearch: the audit daemon is not running (%s)", s.serviceState("auditd"))
		return 1
	}
	lines := s.Dev.AuditReport(0)
	var out []string
	for _, l := range lines {
		if key == "" || strings.Contains(l, "key="+key) {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		fmt.Fprintln(s.Out, "<no matches>")
		return 0
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	for _, l := range out {
		fmt.Fprintf(s.Out, "----\r\n%s\r\n", l)
	}
	return 0
}

func cmdAuditctl(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: auditctl -l | -w path -p wa -k key")
		return 1
	}
	if !s.serviceUp("auditd") {
		s.errf("Error sending add rule data request (No such file or directory)")
		return 1
	}
	if args[0] == "-l" || args[0] == "--list" {
		for _, r := range s.Dev.AuditRules() {
			line := r.Verb
			if r.Path != "" {
				line += " path=" + r.Path
			}
			if r.Key != "" {
				line += " key=" + r.Key
			}
			fmt.Fprintf(s.Out, "%s\r\n", line)
		}
		return 0
	}
	// auditctl -w PATH -p wa -k KEY : append a real rule to the rules file
	var pathArg, key, perms string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-w":
			if i+1 < len(args) {
				pathArg = args[i+1]
				i++
			}
		case "-k":
			if i+1 < len(args) {
				key = args[i+1]
				i++
			}
		case "-p":
			if i+1 < len(args) {
				perms = args[i+1]
				i++
			}
		}
	}
	if pathArg == "" || key == "" {
		s.errf("usage: auditctl -w path -p wa -k key")
		return 1
	}
	rule := "-w " + pathArg + " -p " + orDefaultS(perms, "wa") + " -k " + key + "\n"
	f := "/etc/audit/rules.d/99-manual.rules"
	if data, ok := s.Dev.FS.Read(f); ok {
		s.Dev.FS.Write(f, string(data)+rule, 0640, "root", "root")
	} else {
		s.Dev.FS.MkdirAll(path.Dir(f), 0755, "root", "root")
		s.Dev.FS.Write(f, rule, 0640, "root", "root")
	}
	s.Dev.Logf("info", "audit", "rule loaded: %s", strings.TrimSpace(rule))
	fmt.Fprintf(s.Out, "rule added: %s\r\n", strings.TrimSpace(rule))
	return 0
}

// ---- secstat: the world's own roll-up --------------------------------------

// cmdSecstat prints the defensive posture of this machine from the same state
// the tools act on. It exists because a player should be able to ask "what is
// defending this box, and what has it seen?" without reading nine tools'
// output — and because every line here is a fact with a source, never a summary
// invented for the screen.
func cmdSecstat(s *Shell, args []string) int {
	what := "status"
	if len(args) > 0 {
		what = args[0]
	}
	d := s.Dev
	sec := d.Sec()
	switch what {
	case "status":
		fmt.Fprintf(s.Out, "%s — defensive posture (tick %d, %s)\r\n\r\n", d.Hostname, s.W.TickCount, s.W.Sim.Format("2006-01-02 15:04"))
		fmt.Fprintf(s.Out, "TOOL          STATE       CONFIG\n")
		rows := []struct{ name, conf string }{
			{"fail2ban", "/etc/fail2ban/jail.conf"},
			{"suricata", "/etc/suricata/rules"},
			{"aide", "/etc/aide/aide.conf"},
			{"clamd", "/etc/clamav/clamd.conf"},
			{"monit", "/etc/monit/monitrc"},
			{"auditd", "/etc/audit/rules.d"},
			{"rkhunter", "/etc/rkhunter.conf"},
			{"rsyslog", "/etc/rsyslog.conf"},
		}
		for _, r := range rows {
			state := s.serviceState(r.name)
			if state == "not-present" {
				state = "not installed"
			}
			fmt.Fprintf(s.Out, "%-13s %-11s %s\r\n", r.name, state, r.conf)
		}
		// two of the §33 tools are not services: the firewall's state is the
		// policy in force, and a backup's is whether its repository is there
		// and readable. Both are read from the world, not remembered.
		fstate, fdetail := d.FirewallPosture()
		fmt.Fprintf(s.Out, "%-13s %-11s %s\r\n", "firewall", fstate, fdetail)
		bstate, bdetail := core.ResticPosture(d)
		fmt.Fprintf(s.Out, "%-13s %-11s %s\r\n", "restic", bstate, bdetail)
		fmt.Fprintf(s.Out, "\r\nATTACK SURFACE SEEN\r\n")
		flows := d.RecentFlows(24 * time.Hour)
		counts := map[core.FlowVerdict]int{}
		sources := map[string]int{}
		for _, f := range flows {
			counts[f.Verdict]++
			sources[f.Src]++
		}
		fmt.Fprintf(s.Out, "  connection attempts (24h): %d accepted, %d refused, %d filtered, %d banned\r\n",
			counts[core.FlowAccepted], counts[core.FlowRefused], counts[core.FlowFiltered], counts[core.FlowBanned])
		for _, src := range topSources(sources, 3) {
			fmt.Fprintf(s.Out, "  noisiest source: %s (%d attempts)\r\n", src, sources[src])
		}
		fmt.Fprintf(s.Out, "  auth failures (any age): %d\r\n", len(sec.Fails))
		fmt.Fprintf(s.Out, "  bans: %d active, %d issued in total\r\n", len(d.ActiveBans()), sec.BanCount)
		fmt.Fprintf(s.Out, "  alerts raised: %d\r\n", len(sec.Alerts))
		fmt.Fprintf(s.Out, "  quarantined files: %d\r\n", len(sec.Quarantined))
		if dst := d.LogForwardTarget(); dst != nil {
			fmt.Fprintf(s.Out, "  forwarding logs to: %s (%d line(s) from this machine held there)\r\n",
				dst.Hostname, len(dst.RemoteLogs(d.Hostname, 0)))
		} else {
			fmt.Fprintf(s.Out, "  forwarding logs to: nowhere — this machine's log dies with this machine\r\n")
		}
		return 0
	case "alerts":
		if len(sec.Alerts) == 0 {
			fmt.Fprintln(s.Out, "no alerts raised")
			return 0
		}
		limit := len(sec.Alerts)
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 && n < limit {
				limit = n
			}
		}
		for _, a := range sec.Alerts[len(sec.Alerts)-limit:] {
			ban := ""
			if a.Banned {
				ban = " [banned]"
			}
			fmt.Fprintf(s.Out, "%s %-9s %-9s %s%s\r\n", a.At.Format("15:04:05"), a.Tool, a.Level, a.Msg, ban)
		}
		return 0
	case "flows":
		flows := d.RecentFlows(2 * time.Hour)
		if len(flows) == 0 {
			fmt.Fprintln(s.Out, "no connection attempts in the last 2 hours")
			return 0
		}
		limit := 30
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil && n > 0 {
				limit = n
			}
		}
		if len(flows) > limit {
			flows = flows[len(flows)-limit:]
		}
		for _, f := range flows {
			fmt.Fprintf(s.Out, "%s %-15s %-15s :%-5d %s\r\n",
				f.At.Format("15:04:05"), f.SrcHost, f.Src, f.Port, f.Verdict)
		}
		return 0
	case "bans":
		bans := d.ActiveBans()
		if len(bans) == 0 {
			fmt.Fprintln(s.Out, "no active bans")
			return 0
		}
		for _, b := range bans {
			until := "never expires"
			if !b.Until.IsZero() {
				until = "expires " + b.Until.Format("15:04:05")
			}
			fmt.Fprintf(s.Out, "%-15s %-20s %s — %s\r\n", b.IP, b.Host, until, b.Reason)
		}
		return 0
	case "audit":
		for _, l := range d.AuditReport(20) {
			fmt.Fprintf(s.Out, "%s\r\n", l)
		}
		return 0
	case "compromise":
		// §36: what is resident on this machine, what let it in, and what —
		// if anything — noticed. `clean` removes the artifacts, one by one.
		if len(args) > 1 && args[1] == "clean" {
			host := d.ID
			if len(args) > 2 {
				host = args[2]
			}
			if host != d.ID {
				fmt.Fprintf(s.Out, "compromise: %s is not this machine; clean it from there\r\n", host)
				return 1
			}
			n, err := s.W.CleanFoothold(host)
			if err != nil {
				fmt.Fprintf(s.Out, "compromise: %v\r\n", err)
				return 1
			}
			fmt.Fprintf(s.Out, "removed %d implant(s) from %s: process, payload and persistence line\r\n", n, d.Hostname)
			return 0
		}
		for _, line := range s.W.IntrusionReport(d.ID) {
			fmt.Fprintf(s.Out, "%s\r\n", line)
		}
		return 0
	case "remote":
		host := ""
		if len(args) > 1 {
			host = args[1]
		}
		logs := d.RemoteLogs(host, 40)
		if len(logs) == 0 {
			fmt.Fprintf(s.Out, "no forwarded lines held on %s\r\n", d.Hostname)
			return 0
		}
		for _, r := range logs {
			fmt.Fprintf(s.Out, "%s", r.Line)
		}
		return 0
	case "quarantine":
		if len(sec.Quarantined) == 0 {
			fmt.Fprintln(s.Out, "quarantine is empty")
			return 0
		}
		for _, q := range sec.Quarantined {
			fmt.Fprintf(s.Out, "%s\r\n", q)
		}
		return 0
	case "firewall":
		st := d.FW()
		fmt.Fprintf(s.Out, "firewall — %s\r\n", d.Hostname)
		fmt.Fprintf(s.Out, "  files read:      %s\r\n", strings.Join(st.Files, ", "))
		fmt.Fprintf(s.Out, "  host input:      %s (v6 %s)\r\n", st.HostInput, st.HostInput6)
		fmt.Fprintf(s.Out, "  wan input:       %s\r\n", st.WANInput)
		fmt.Fprintf(s.Out, "  forward policy:  %s\r\n", st.ForwardPolicy)
		fmt.Fprintf(s.Out, "  rules:           %d (v4), %d (v6)\r\n", len(st.Rules), len(st.Rules6))
		fmt.Fprintf(s.Out, "  log drops:       %v\r\n", st.LogDrops)
		open := coreExposure(d)
		if len(open) == 0 {
			fmt.Fprintf(s.Out, "  open forwards:   none — nothing inside is published\r\n")
		} else {
			fmt.Fprintf(s.Out, "  open forwards:\r\n")
			for _, e := range open {
				fmt.Fprintf(s.Out, "    %s\r\n", e)
			}
		}
		return 0
	case "backup":
		state, detail := core.ResticPosture(d)
		fmt.Fprintf(s.Out, "backup — %s\r\n", d.Hostname)
		fmt.Fprintf(s.Out, "  repository: %s (%s)\r\n", detail, state)
		snaps, err := core.ResticSnapshots(d)
		if err != nil {
			fmt.Fprintf(s.Out, "  note: %v\r\n", err)
			return 0
		}
		if len(snaps) == 0 {
			fmt.Fprintf(s.Out, "  snapshots:  none — a repository with no snapshots protects nothing\r\n")
			return 0
		}
		fmt.Fprintf(s.Out, "  snapshots:  %d\r\n", len(snaps))
		for _, sn := range snaps {
			fmt.Fprintf(s.Out, "  %s  %s  %4d file(s)  %s\r\n", sn.ID[:8],
				sn.At.Format("2006-01-02 15:04"), sn.Files, humanBytes(sn.Bytes))
		}
		return 0
	case "hids":
		cfg := d.RkhunterConfigOf()
		fmt.Fprintf(s.Out, "host monitor — %s\r\n", d.Hostname)
		fmt.Fprintf(s.Out, "  config:   %s (interval %s)\r\n", core.RkhunterConfPath, cfg.Interval)
		base, has := d.RkhunterBaseline()
		if !has {
			fmt.Fprintf(s.Out, "  baseline: none — nothing to compare against (`rkhunter --propupd`)\r\n")
			return 0
		}
		fmt.Fprintf(s.Out, "  baseline: %d fact(s) in %s\r\n", len(base), core.RkhunterBasePath)
		findings, err := d.RkhunterCheck()
		if err != nil {
			fmt.Fprintf(s.Out, "  %v\r\n", err)
			return 0
		}
		if len(findings) == 0 {
			fmt.Fprintf(s.Out, "  warnings: none — the host matches its baseline\r\n")
			return 0
		}
		fmt.Fprintf(s.Out, "  warnings: %d\r\n", len(findings))
		for _, f := range findings {
			fmt.Fprintf(s.Out, "    %-8s %s\r\n", f.Kind, f.Msg)
		}
		return 0
	}
	s.errf("usage: secstat [status|alerts [n]|flows [n]|bans|audit|firewall|hids|backup|remote [host]|quarantine|compromise [clean]]")
	return 1
}

// coreExposure is a one-line adapter so the switch above reads like the rest:
// the firewall's own summary is the world's, never a second opinion.
func coreExposure(d *core.Device) []string { return d.ExposureSummary() }

func topSources(counts map[string]int, n int) []string {
	type kv struct {
		ip string
		n  int
	}
	var list []kv
	for ip, c := range counts {
		list = append(list, kv{ip, c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].ip < list[j].ip
	})
	var out []string
	for i := 0; i < len(list) && i < n; i++ {
		out = append(out, list[i].ip)
	}
	return out
}
