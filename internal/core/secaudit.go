package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// §33, the detectors that are not the network: auditd, an integrity monitor,
// antivirus, the service watchdog and the central log server. Same rule as
// everywhere else in this package — a tool is a service on a machine, its
// configuration is a file, and its output is real state. Nothing here is a
// flavour-text simulator: an audit rule that is not loaded produces no records,
// a stopped clamd finds no virus, and a log forward that cannot reach its
// collector says so.

// ---- auditd -----------------------------------------------------------------

// AuditRecord is one syscall-level fact an audit rule made the kernel keep.
type AuditRecord struct {
	At   time.Time
	Type string // SYSCALL|PATH
	Verb string // exec|write|open|chmod|connect
	Who  string // the account the process ran as
	Path string // file involved, when the rule has one
	Key  string // the rule's own key
	Comm string // the process name
}

// AuditRule is one real audit rule: a syscall or a path to watch, with the
// key an operator searches by. Both forms a rules file can carry are parsed:
//
//	-w /etc/shadow -p wa -k identity                 (a watched path)
//	-a always,exit -F arch=b64 -S execve -k exec     (a watched syscall)
type AuditRule struct {
	Verb string // exec|write|chmod|open|connect
	Path string // -w path, or -F path=… (empty means any path)
	Key  string // -k name
	Exe  string // -F exe=…
}

// AuditRules reads every *.rules file under /etc/audit/rules.d in name order,
// the way auditd does. A machine without that directory has no rules, so its
// audit log stays empty — which is the honest result of not configuring it.
func (d *Device) AuditRules() []AuditRule {
	var files []string
	for p, n := range d.FS.Nodes {
		if n.IsDir || !strings.HasPrefix(p, "/etc/audit/rules.d/") || !strings.HasSuffix(p, ".rules") {
			continue
		}
		files = append(files, p)
	}
	sort.Strings(files)
	var out []AuditRule
	for _, f := range files {
		data, ok := d.FS.Read(f)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			toks := strings.Fields(line)
			r := AuditRule{Verb: "open"}
			perms, syscall := "", ""
			for i := 0; i < len(toks); i++ {
				switch toks[i] {
				case "-w", "-W":
					if i+1 < len(toks) {
						r.Path = toks[i+1]
						r.Verb = "write"
						i++
					}
				case "-k":
					if i+1 < len(toks) {
						r.Key = toks[i+1]
						i++
					}
				case "-p":
					if i+1 < len(toks) {
						perms = toks[i+1]
						i++
					}
				case "-S":
					if i+1 < len(toks) {
						syscall = toks[i+1]
						i++
					}
				case "-F":
					if i+1 < len(toks) {
						field := toks[i+1]
						i++
						if v, found := strings.CutPrefix(field, "path="); found {
							r.Path = v
						}
						if v, found := strings.CutPrefix(field, "exe="); found {
							r.Exe = v
						}
						if v, found := strings.CutPrefix(field, "syscall="); found {
							syscall = v
						}
					}
				}
			}
			switch {
			case strings.Contains(syscall, "execve") || r.Exe != "":
				r.Verb = "exec"
			case strings.Contains(syscall, "connect"):
				r.Verb = "connect"
			case strings.Contains(syscall, "chmod") || strings.Contains(syscall, "chown"):
				r.Verb = "chmod"
			case r.Path != "":
				// -p wa watches writes and attribute changes; a rule with only
				// 'a' is a permission change (chmod/chown)
				switch {
				case strings.Contains(perms, "w"):
					r.Verb = "write"
				case strings.Contains(perms, "a"):
					r.Verb = "chmod"
				default:
					r.Verb = "open"
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// AuditEnabled is true when the machine runs auditd AND has rules loaded: the
// two conditions a real operator checks with `auditctl -l`.
func (d *Device) AuditEnabled() bool {
	return d.SvcRunning("auditd") && len(d.AuditRules()) > 0
}

// Audit records a syscall-level event if — and only if — a loaded rule matches
// it. This is what keeps §26 攻击行为真实后果 causal: an attacker who wants to
// leave no trace on a machine with rules has to change what they do, not just
// hope the log is decorative.
func (d *Device) Audit(verb, who, path, comm string) {
	if !d.AuditEnabled() {
		return
	}
	for _, r := range d.AuditRules() {
		if r.Verb != verb {
			continue
		}
		if r.Path != "" && r.Path != path {
			continue
		}
		if r.Exe != "" && !strings.HasSuffix(comm, r.Exe) {
			continue
		}
		rec := AuditRecord{At: d.W.Sim, Type: "SYSCALL", Verb: verb, Who: dashIfEmpty(who), Path: path, Key: r.Key, Comm: comm}
		s := d.Sec()
		s.Audit = append(s.Audit, rec)
		if len(s.Audit) > 200 {
			s.Audit = append(s.Audit[:0], s.Audit[len(s.Audit)-200:]...)
		}
		d.Logf("info", "audit", "%s %s=%s key=%s comm=%s",
			rec.Type, verb, dashIfEmpty(path), dashIfEmpty(r.Key), dashIfEmpty(comm))
		return
	}
}

// AuditReport is `ausearch`-ish over the ring the machine kept.
func (d *Device) AuditReport(limit int) []string {
	s := d.Sec()
	if len(s.Audit) == 0 {
		return []string{"<no matches>"}
	}
	if limit <= 0 || limit > len(s.Audit) {
		limit = len(s.Audit)
	}
	var out []string
	for _, rec := range s.Audit[len(s.Audit)-limit:] {
		out = append(out, fmt.Sprintf("time->%s type=%s msg=audit: %s who=%s path=%s key=%s",
			rec.At.Format("Mon Jan  2 15:04:05 2006"), rec.Type, rec.Verb, rec.Who,
			dashIfEmpty(rec.Path), dashIfEmpty(rec.Key)))
	}
	return out
}

// ---- integrity monitor (aide/tripwire-style) --------------------------------

// AideConfig is /etc/aide/aide.conf in the small shape this world supports:
// directories to watch, and whether the database is checked on every tick or
// on its own interval (as the real cron-driven tool does).
type AideConfig struct {
	Dirs     []string
	Interval time.Duration
	Fresh    bool // report a file the baseline does not know
}

// AideConfOf parses the file. No file, no configuration, no checking — the
// same fail-closed rule as the other tools.
func AideConfOf(d *Device) AideConfig {
	cfg := AideConfig{Interval: 30 * time.Minute}
	data, ok := d.FS.Read("/etc/aide/aide.conf")
	if !ok {
		return cfg
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "watch":
			cfg.Dirs = append(cfg.Dirs, v)
		case "interval":
			cfg.Interval = parseDuration(v, cfg.Interval)
		case "freshfiles":
			cfg.Fresh = !strings.EqualFold(v, "false") && v != "0"
		}
	}
	return cfg
}

// aideTick is the integrity monitor's own loop: a baseline it built itself,
// compared on its interval. The database is a *baseline* — the tool never
// quietly rewrites it, because a monitor that adopts the attacker's change is
// not a monitor. Each distinct change is reported once: the map remembers what
// has already been reported, not what the file now is.
func (d *Device) aideTick() {
	cfg := AideConfOf(d)
	if len(cfg.Dirs) == 0 {
		return
	}
	s := d.Sec()
	if s.LastAideTick != 0 && d.W.TickCount-s.LastAideTick < tickSpan(cfg.Interval) {
		return
	}
	s.LastAideTick = d.W.TickCount
	files, sums := d.fileHashes(cfg.Dirs)
	if len(s.AideDB) == 0 {
		s.AideDB = sums
		s.AideReported = map[string]string{}
		d.Logf("info", "aide", "database initialized: %d file(s) under %s", len(files), strings.Join(cfg.Dirs, ", "))
		d.W.AddEvent(d.ID, "info", "aide", "%s: integrity database initialized (%d files)", d.Hostname, len(files))
		return
	}
	report := func(kind, path, mark string) {
		if s.AideReported[path] == mark {
			return // this change has already been reported
		}
		s.AideReported[path] = mark
		d.Alertf("aide", kind, "warn", "%s %s", kind, path)
	}
	for _, f := range files {
		base, known := s.AideDB[f]
		switch {
		case !known:
			if cfg.Fresh {
				report("added", f, sums[f])
			}
		case base != sums[f]:
			report("modified", f, sums[f])
		}
	}
	for f := range s.AideDB {
		if _, still := sums[f]; !still {
			report("removed", f, "gone")
		}
	}
	if s.LastFreshTick != d.W.TickCount {
		d.Logf("info", "aide", "integrity check complete: %d file(s) verified against the database", len(files))
	}
	s.LastFreshTick = d.W.TickCount
}

// tickSpan turns a wall-clock interval into ticks. The world advances 30
// seconds per tick, which is the only clock this game has.
func tickSpan(d time.Duration) int {
	n := int(d / (30 * time.Second))
	if n < 1 {
		n = 1
	}
	return n
}

// fileHashes walks the watched trees and returns the files plus a cheap
// content digest. Dirs are matched as prefixes, like the real tool's rules.
func (d *Device) fileHashes(dirs []string) ([]string, map[string]string) {
	var files []string
	sums := map[string]string{}
	for p, n := range d.FS.Nodes {
		if n.IsDir {
			continue
		}
		if !underAny(p, dirs) {
			continue
		}
		files = append(files, p)
		sums[p] = contentHash(n.Data)
	}
	sort.Strings(files)
	return files, sums
}

func underAny(p string, dirs []string) bool {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/") {
			return true
		}
	}
	return false
}

// FileHashes is the exported form the shell's `aide --init` uses: the files
// under the watched trees, with their digests.
func (d *Device) FileHashes(dirs []string) ([]string, map[string]string) { return d.fileHashes(dirs) }

// AideDiff is what an integrity check reports: the watched files whose content
// changed since the database was built, plus additions and removals.
type AideDiff struct {
	Kind string // modified|added|removed
	Path string
}

// AideDiff compares the database against the filesystem right now.
func (d *Device) AideDiff() []AideDiff {
	cfg := AideConfOf(d)
	if len(cfg.Dirs) == 0 {
		return nil
	}
	s := d.Sec()
	if len(s.AideDB) == 0 {
		return nil
	}
	files, sums := d.fileHashes(cfg.Dirs)
	var out []AideDiff
	for _, f := range files {
		old, known := s.AideDB[f]
		switch {
		case !known:
			out = append(out, AideDiff{Kind: "added", Path: f})
		case old != sums[f]:
			out = append(out, AideDiff{Kind: "modified", Path: f})
		}
	}
	for f := range s.AideDB {
		if _, still := sums[f]; !still {
			out = append(out, AideDiff{Kind: "removed", Path: f})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// contentHash is a small, stable digest — enough to notice a change to a
// byte, which is all the comparison needs. (sha256hex already exists for
// packages; this one is for thousands of files a tick.)
func contentHash(data []byte) string {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	s := strconv.FormatUint(h, 16)
	for len(s) < 16 {
		s = "0" + s
	}
	return s
}

// ---- antivirus (clamav-style) ----------------------------------------------

// MalwareDir is where a machine's scans quarantine what they caught.
const MalwareDir = "/var/quarantine"

// MalwareSignature is one line of the virus database: a literal string the
// scanner looks for, with the name it reports.
type MalwareSignature struct {
	Pattern string
	Name    string
}

// MalwareDB parses /var/lib/clamav/main.db. A machine whose database is
// missing scans for nothing and says so (its `freshclam` has never run) —
// which is the difference between an installed scanner and a working one.
func (d *Device) MalwareDB() []MalwareSignature {
	data, ok := d.FS.Read("/var/lib/clamav/main.db")
	if !ok {
		return nil
	}
	var out []MalwareSignature
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, pat, found := strings.Cut(line, ":")
		if !found || pat == "" {
			continue
		}
		out = append(out, MalwareSignature{Name: strings.TrimSpace(name), Pattern: pat})
	}
	return out
}

// DBFresh reports whether the database was written recently enough for the
// scanner to run: the same "database age" gate a real clamd enforces (its
// default is 7 days).
func (d *Device) DBFresh() bool {
	n, ok := d.FS.Get("/var/lib/clamav/main.db")
	if !ok {
		return false
	}
	return d.W.Sim.Sub(n.MTime) < 7*24*time.Hour
}

// clamTick is clamd's idle behaviour: it says nothing when the database is
// missing or stale, and it watches the quarantine directory it was given.
func (d *Device) clamTick() {
	if _, ok := d.FS.Get("/var/lib/clamav/main.db"); !ok {
		if d.W.TickCount%20 == 3 {
			d.Logf("warn", "clamd", "virus database missing — run freshclam before scanning")
		}
		return
	}
	if !d.DBFresh() {
		if d.W.TickCount%20 == 5 {
			d.Alertf("clamav", "stale-db", "warn", "virus database is older than 7 days — signatures are behind")
		}
	}
}

// ScanResult is one file's verdict.
type ScanResult struct {
	Path  string
	Virus string
}

// ScanFiles is the honest scanner: it reads the file contents and matches the
// loaded signatures. No database (or a stale one) means no scan, not a clean
// bill of health.
func (d *Device) ScanFiles(paths []string) (results []ScanResult, scanned int, err error) {
	if !d.SvcRunning("clamd") && !d.SvcRunning("clamav") {
		return nil, 0, fmt.Errorf("clamd is not running")
	}
	db := d.MalwareDB()
	if len(db) == 0 {
		return nil, 0, fmt.Errorf("virus database missing — run freshclam")
	}
	if !d.DBFresh() {
		return nil, 0, fmt.Errorf("virus database is stale (older than 7 days) — run freshclam")
	}
	for _, p := range paths {
		n, ok := d.FS.Get(p)
		if !ok || n.IsDir {
			continue
		}
		scanned++
		body := string(n.Data)
		for _, sig := range db {
			if strings.Contains(body, sig.Pattern) {
				results = append(results, ScanResult{Path: p, Virus: sig.Name})
				break
			}
		}
	}
	return results, scanned, nil
}

// Quarantine moves an infected file into the scanner's quarantine directory
// and records the alert. This is a real move: the original path no longer
// exists, which is what makes an infection a consequence and not a message.
func (d *Device) Quarantine(res ScanResult, actor string) string {
	dest := MalwareDir + "/" + strings.ReplaceAll(strings.TrimPrefix(res.Path, "/"), "/", "_") + ".quarantined"
	d.FS.MkdirAll(MalwareDir, 0700, "root", "root")
	if n, ok := d.FS.Get(res.Path); ok {
		d.FS.Write(dest, string(n.Data), 0600, "root", "root")
		d.FS.Remove(res.Path)
	}
	s := d.Sec()
	s.Quarantined = append(s.Quarantined, dest)
	d.Alertf("clamav", "infected", "alert", "FOUND %s in %s — quarantined to %s", res.Virus, res.Path, dest)
	return dest
}

// ---- monitoring (monit-style) ----------------------------------------------

// MonitCheck is one `check` block of /etc/monit/monitrc.
type MonitCheck struct {
	Service  string
	MaxDown  int           // restart after this many consecutive failed ticks
	Interval time.Duration // how often the check runs
	Action   string        // restart|alert
}

// MonitConfOf parses the file.
func MonitConfOf(d *Device) []MonitCheck {
	data, ok := d.FS.Read("/etc/monit/monitrc")
	if !ok {
		return nil
	}
	var out []MonitCheck
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "check") {
			continue
		}
		c := MonitCheck{Service: fields[2], MaxDown: 2, Interval: 2 * time.Minute, Action: "restart"}
		for _, f := range fields[3:] {
			k, v, found := strings.Cut(f, "=")
			if !found {
				continue
			}
			switch strings.ToLower(k) {
			case "maxdown":
				c.MaxDown = parseIntDefault(v, c.MaxDown)
			case "interval":
				c.Interval = parseDuration(v, c.Interval)
			case "action":
				c.Action = strings.ToLower(v)
			}
		}
		out = append(out, c)
	}
	return out
}

// monitTick watches the services it was told to watch and really restarts the
// ones that stayed down. A watchdog that only printed "service down" would be
// the decorative version of this tool.
func (d *Device) monitTick() {
	for _, c := range MonitConfOf(d) {
		svc := d.Svc(c.Service)
		if svc == nil {
			continue
		}
		if svc.State == "running" {
			svc.MonitDown = 0
			continue
		}
		svc.MonitDown++
		if svc.MonitDown < c.MaxDown {
			continue
		}
		if c.Action != "restart" {
			d.Alertf("monit", "down", "warn", "service %s is not running (monitoring only)", c.Service)
			continue
		}
		if _, err := d.StartService(c.Service); err != nil {
			d.Alertf("monit", "restart-failed", "warn", "service %s will not start: %v", c.Service, err)
			continue
		}
		svc.MonitDown = 0
		d.Logf("info", "monit", "service %s is not running — restarted", c.Service)
		d.W.AddEvent(d.ID, "info", "monit", "%s: restarted %s (was down)", d.Hostname, c.Service)
	}
}

// ---- the central log server -------------------------------------------------

// RemoteLog is one line a device forwarded to its collector, with where it came
// from. This is what makes §34's central server possible: the collector holds
// other machines' evidence, so taking one machine does not take the record.
type RemoteLog struct {
	At   time.Time
	From string // hostname of the sending device
	Dev  string // device id
	Line string
}

// maxRemoteLog is how much a collector keeps.
const maxRemoteLog = 4000

// LogForwardTarget reads an rsyslog forward stanza and resolves it to a device:
//
//	*.* @10.77.1.5:514     (udp)
//	*.* @@logs.home:514    (tcp)
//
// A line that is commented out, or an address that does not resolve, means the
// machine forwards nothing — and its logs are only where they were written.
func (d *Device) LogForwardTarget() *Device {
	// OpenWrt keeps its log destination in /etc/config/system — the file the
	// router's own `uci set system.@system[0].log_ip=...` writes.
	if data, ok := d.FS.Read("/etc/config/system"); ok {
		host := uciOption(data, "log_ip")
		if host != "" {
			if id, ok := d.W.IPMap[host]; ok {
				return d.W.Devices[id]
			}
			if ip, ok, _ := DNSAnswer(d, host); ok {
				if id, ok := d.W.IPMap[ip]; ok {
					return d.W.Devices[id]
				}
			}
			return nil
		}
	}
	data, ok := d.FS.Read("/etc/rsyslog.conf")
	if !ok {
		data, ok = d.FS.Read("/etc/rsyslog.d/50-default.conf")
		if !ok {
			return nil
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "@")
		if i < 0 || !strings.HasPrefix(strings.TrimSpace(line[:i]), "*.") {
			continue
		}
		rest := strings.TrimPrefix(line[i:], "@")
		rest = strings.TrimPrefix(rest, "@")
		host, _, found := strings.Cut(strings.TrimSpace(rest), ":")
		if !found {
			host = strings.TrimSpace(rest)
		}
		if host == "" {
			continue
		}
		// an address is the usual form; a name goes through the machine's own
		// resolver, the same one everything else uses
		if id, ok := d.W.IPMap[host]; ok {
			return d.W.Devices[id]
		}
		if ip, ok, _ := DNSAnswer(d, host); ok {
			if id, ok := d.W.IPMap[ip]; ok {
				return d.W.Devices[id]
			}
		}
		return nil
	}
	return nil
}

// ForwardLog sends one line to the machine named in the rsyslog config, over
// the same packet path as everything else — so a collector that is down (or
// banned, or unwired) really loses the line, and the failure is visible where
// a player can find it.
func (d *Device) ForwardLog(level, src, msg string) {
	if src == "rsyslog" {
		return // the forwarder's own complaints stay local: no loops
	}
	dst := d.LogForwardTarget()
	if dst == nil || dst == d {
		return
	}
	// one syslog line, ending where a syslog line ends: both the local file and
	// the collector keep it that way, so a reader of either sees one event per
	// line rather than a run-on paragraph
	line := fmt.Sprintf("%s %s %s[%s]: %s\n", d.W.Sim.Format("Jan 2 15:04:05"), d.Hostname, src, level, msg)
	// A collector is a service: if it is stopped, nothing is served. Port 514
	// is the log port whether the daemon is syslogd or rsyslogd.
	port := 514
	svc, landed, verdict := Dial(d, dst.FirstLANIP(), port)
	if svc == nil || verdict != "connected" {
		_ = landed
		if d.W.TickCount%10 == 1 {
			d.Logf("warn", "rsyslog", "could not forward to %s: %s", dst.Hostname, verdict)
		}
		return
	}
	landed.StoreRemoteLog(d, line)
}

// StoreRemoteLog is the collector's own record.
func (d *Device) StoreRemoteLog(from *Device, line string) {
	s := d.Sec()
	s.RemoteLog = append(s.RemoteLog, RemoteLog{At: d.W.Sim, From: from.Hostname, Dev: from.ID, Line: line})
	if len(s.RemoteLog) > maxRemoteLog {
		s.RemoteLog = append(s.RemoteLog[:0], s.RemoteLog[len(s.RemoteLog)-maxRemoteLog:]...)
	}
}

// RemoteLogs returns the collector's record, oldest first, optionally filtered
// by source hostname.
func (d *Device) RemoteLogs(from string, limit int) []RemoteLog {
	out := d.Sec().RemoteLog
	if from != "" {
		out = nil
		for _, r := range d.Sec().RemoteLog {
			if r.From == from {
				out = append(out, r)
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// dashIfEmpty prints "-" where an audit field is absent, which is what
// ausearch shows for a record that does not carry the key.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// uciOption pulls one `option name 'value'` out of an OpenWrt config file.
func uciOption(data []byte, name string) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "option ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != name {
			continue
		}
		return strings.Trim(strings.Join(fields[2:], " "), "'\"")
	}
	return ""
}
