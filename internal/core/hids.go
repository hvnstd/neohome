package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// §33 防守和安全软件 — HIDS (host-based intrusion detection).
//
// The IDS reads what arrives on the wire; a HIDS reads the machine itself.
// What it watches is exactly the state an intruder has to change to stay:
// which ports are listening, which accounts exist, which binaries are on disk
// and whether anything executable has appeared where executables have no
// business being. aide answers "did this file change"; this answers "does this
// machine still look like the machine I built".
//
// The shape is rkhunter's, because that is a real tool with a real config file
// and a real baseline update verb:
//
//	/etc/rkhunter.conf   WATCH_PORTS, CHECK_ACCOUNTS, BINARY_WATCH,
//	                     SUSPICIOUS_DIRS, CHECK_SERVICES, interval
//	/var/lib/rkhunter/baseline   what the machine looked like at --propupd
//	rkhunter --propupd   take that picture, deliberately
//	rkhunter --check     compare the machine with it and say what moved
//
// Nothing here is a heuristic about intent: every finding names a fact and
// where it was observed. A finding that stops being true stops being reported,
// and an alert is raised once per finding per window (Alertf dedupes), so a
// long-running intruder does not drown the log.

const (
	RkhunterConfPath = "/etc/rkhunter.conf"
	RkhunterBasePath = "/var/lib/rkhunter/baseline"
	// rkhunterMaxBinaryDir bounds how many binaries one watch directory
	// contributes, so the baseline stays a file a person can read.
	rkhunterMaxBinaryDir = 400
)

// RkhunterConf is the parsed configuration. Absent values are the defaults a
// fresh install ships.
type RkhunterConf struct {
	WatchPorts     []int
	CheckAccounts  bool
	CheckServices  bool
	CheckBinaries  bool
	BinaryWatch    []string
	SuspiciousDirs []string
	Interval       time.Duration
	Raw            string
}

// RkhunterConfigOf re-reads the file on every call: editing the configuration
// is how an operator changes what the host monitor looks at.
func (d *Device) RkhunterConfigOf() RkhunterConf {
	c := RkhunterConf{
		CheckAccounts:  true,
		CheckServices:  true,
		CheckBinaries:  true,
		BinaryWatch:    []string{"/usr/bin", "/usr/sbin", "/usr/local/bin"},
		SuspiciousDirs: []string{"/tmp", "/var/tmp", "/dev/shm"},
		Interval:       2 * time.Minute,
	}
	data, ok := d.FS.Read(RkhunterConfPath)
	if !ok {
		return c
	}
	c.Raw = string(data)
	c.BinaryWatch = nil
	c.SuspiciousDirs = nil
	for _, line := range strings.Split(c.Raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k, v = strings.ToUpper(strings.TrimSpace(k)), strings.Trim(strings.TrimSpace(v), "\"'")
		switch k {
		case "WATCH_PORTS":
			c.WatchPorts = nil
			for _, p := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
					c.WatchPorts = append(c.WatchPorts, n)
				}
			}
		case "CHECK_ACCOUNTS":
			c.CheckAccounts = v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
		case "CHECK_SERVICES":
			c.CheckServices = v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
		case "CHECK_BINARIES":
			c.CheckBinaries = v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
		case "BINARY_WATCH":
			c.BinaryWatch = strings.Fields(v)
		case "SUSPICIOUS_DIRS":
			c.SuspiciousDirs = strings.Fields(v)
		case "INTERVAL":
			c.Interval = parseDuration(v, c.Interval)
		}
	}
	if len(c.BinaryWatch) == 0 {
		c.BinaryWatch = []string{"/usr/bin", "/usr/sbin", "/usr/local/bin"}
	}
	return c
}

// RkhunterFacts is a picture of the machine: one sorted list of the things the
// monitor watches. It is the same function used to build the baseline and to
// compare against it — a baseline built by one rule and checked by another is
// how a monitor ends up reporting permanent noise.
func (d *Device) RkhunterFacts() []string {
	c := d.RkhunterConfigOf()
	var facts []string
	// listening services: a port with something actually serving it
	for _, s := range d.Services {
		if s == nil || s.Port <= 0 || s.State != "running" {
			continue
		}
		facts = append(facts, fmt.Sprintf("port %d %s", s.Port, s.Name))
	}
	if c.CheckAccounts {
		names := make([]string, 0, len(d.Users))
		for name := range d.Users {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			facts = append(facts, fmt.Sprintf("account %s uid %d", name, d.Users[name].UID))
		}
	}
	if c.CheckServices {
		names := make([]string, 0, len(d.Services))
		for name := range d.Services {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			facts = append(facts, fmt.Sprintf("service %s", name))
		}
	}
	if c.CheckBinaries {
		seen := 0
		paths := make([]string, 0, 64)
		for p := range d.FS.Nodes {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if seen >= rkhunterMaxBinaryDir*len(c.BinaryWatch) {
				break
			}
			n := d.FS.Nodes[p]
			if n == nil || n.IsDir || n.Mode.Perm()&0111 == 0 {
				continue
			}
			if !underAny(p, c.BinaryWatch) {
				continue
			}
			facts = append(facts, fmt.Sprintf("binary %s %s", p, contentHash(n.Data)))
			seen++
		}
	}
	sort.Strings(facts)
	return facts
}

// RkhunterPropupd writes the baseline. It is a privileged act, so it goes
// through the same write gate everything else uses: an unprivileged account
// gets the filesystem's refusal, not a quiet success.
func (d *Device) RkhunterPropupd(actor *User) (int, error) {
	// the baseline lives in a directory the package does not ship: taking one
	// for the first time is what creates it, and doing so is privileged like
	// every other write into /var
	if err := d.FS.MkdirAllChecked(dirOfPath(RkhunterBasePath), 0755, actor); err != nil {
		return 0, err
	}
	facts := d.RkhunterFacts()
	body := "# rkhunter baseline — written by --propupd\n" +
		fmt.Sprintf("# host %s, tick %d, %s\n", d.Hostname, d.W.TickCount, d.W.Sim.Format("2006-01-02 15:04:05")) +
		strings.Join(facts, "\n") + "\n"
	if err := d.WriteGuest(RkhunterBasePath, []byte(body), actor); err != nil {
		return 0, err
	}
	d.Logf("info", "rkhunter", "baseline written: %d facts", len(facts))
	d.W.AddEvent(d.ID, "info", "rkhunter", "%s wrote a host baseline (%d facts)", d.Hostname, len(facts))
	return len(facts), nil
}

// dirOfPath is the directory part of an absolute path (path.Dir would clean a
// trailing name we do not want changed).
func dirOfPath(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}

// RkhunterBaseline reads the stored picture. An empty slice with ok=false
// means nobody ever took one.
func (d *Device) RkhunterBaseline() ([]string, bool) {
	data, ok := d.FS.Read(RkhunterBasePath)
	if !ok {
		return nil, false
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, true
}

// RkhunterFinding is one thing that moved, with the fact behind it.
type RkhunterFinding struct {
	Kind  string // port|account|service|binary|file
	Where string // the path or the port
	Msg   string
}

// RkhunterCheck compares the machine with its baseline. Every finding is a
// fact: a listening port that is not in the picture, an account that did not
// exist, a binary whose bytes differ, or an executable sitting in a directory
// where executables do not belong.
func (d *Device) RkhunterCheck() ([]RkhunterFinding, error) {
	base, has := d.RkhunterBaseline()
	if !has {
		return nil, fmt.Errorf("no baseline on this host — run `rkhunter --propupd` first")
	}
	known := map[string]bool{}
	for _, f := range base {
		known[f] = true
	}
	baseBinary := map[string]string{}
	for _, f := range base {
		if rest, ok := strings.CutPrefix(f, "binary "); ok {
			p, h, found := strings.Cut(rest, " ")
			if found {
				baseBinary[p] = h
			}
		}
	}

	var out []RkhunterFinding
	for _, f := range d.RkhunterFacts() {
		if known[f] {
			continue
		}
		switch {
		case strings.HasPrefix(f, "port "):
			rest := strings.TrimPrefix(f, "port ")
			port, name, _ := strings.Cut(rest, " ")
			out = append(out, RkhunterFinding{Kind: "port", Where: port,
				Msg: fmt.Sprintf("unknown listening port %s (%s) — not in the baseline", port, name)})
		case strings.HasPrefix(f, "account "):
			rest := strings.TrimPrefix(f, "account ")
			name, _, _ := strings.Cut(rest, " ")
			out = append(out, RkhunterFinding{Kind: "account", Where: name,
				Msg: fmt.Sprintf("account %q exists but is not in the baseline", name)})
		case strings.HasPrefix(f, "service "):
			name := strings.TrimPrefix(f, "service ")
			out = append(out, RkhunterFinding{Kind: "service", Where: name,
				Msg: fmt.Sprintf("service %q was not installed when the baseline was taken", name)})
		case strings.HasPrefix(f, "binary "):
			rest := strings.TrimPrefix(f, "binary ")
			p, h, _ := strings.Cut(rest, " ")
			if was, existed := baseBinary[p]; existed {
				out = append(out, RkhunterFinding{Kind: "binary", Where: p,
					Msg: fmt.Sprintf("%s changed on disk (baseline %s, now %s)", p, was[:8], h[:8])})
			} else {
				out = append(out, RkhunterFinding{Kind: "binary", Where: p,
					Msg: fmt.Sprintf("new executable %s in a watched directory", p)})
			}
		}
	}
	// the classic hiding places, independent of the baseline: an executable in
	// /tmp is worth naming whatever it is
	c := d.RkhunterConfigOf()
	for p, n := range d.FS.Nodes {
		if n == nil || n.IsDir || n.Mode.Perm()&0111 == 0 {
			continue
		}
		if !underAny(p, c.SuspiciousDirs) {
			continue
		}
		out = append(out, RkhunterFinding{Kind: "file", Where: p,
			Msg: fmt.Sprintf("executable file in a world-writable directory: %s", p)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Where < out[j].Where
	})
	return out, nil
}

// hidsTick is the periodic check: while the service runs, the machine compares
// itself with its baseline every interval and raises one alert per finding.
func (d *Device) hidsTick() {
	c := d.RkhunterConfigOf()
	s := d.Sec()
	if s.LastHidsTick != 0 && d.W.TickCount-s.LastHidsTick < tickSpan(c.Interval) {
		return
	}
	if _, has := d.RkhunterBaseline(); !has {
		return // nothing to compare against; --propupd is a deliberate act
	}
	s.LastHidsTick = d.W.TickCount
	findings, err := d.RkhunterCheck()
	if err != nil {
		return
	}
	for _, f := range findings {
		d.Alertf("rkhunter", f.Kind, "alert", "%s", f.Msg)
	}
}
