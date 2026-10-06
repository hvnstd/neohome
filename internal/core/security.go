package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// §33 防守和安全软件 — security tools that really do something.
//
// Three facts live here, and every tool in this package's §33 files is one of
// them:
//
//	flows  — the packet path's own record of who tried to reach this machine.
//	         This world has no packet capture, so flows ARE the sensor an IDS
//	         reads: every Dial, whatever its verdict, leaves one.
//	fails  — rejected authentication attempts, recorded by the *target*. This
//	         is what a fail2ban jail counts, and no client can fake it away.
//	bans   — a ban is a drop, enforced inside Dial/Reach. A banned source
//	         really cannot open a socket or ping the machine, and the ban
//	         expires on the world clock because it is a time, not a flag.
//
// The tools are services: a stopped `suricata` raises no alerts, a stopped
// `fail2ban` bans no one. Configuration is files on the device
// (/etc/fail2ban/jail.conf, /etc/suricata/rules, /etc/audit/rules.d/*.rules,
// /etc/aide/aide.conf, /var/lib/clamav/main.db, /etc/monit/monitrc, an
// rsyslog forward stanza) parsed on every tick — the same rule the firewall,
// the switch ports and dnsmasq follow: the file IS the configuration.

// FlowVerdict is what the packet path decided about a connection attempt.
type FlowVerdict string

const (
	FlowAccepted FlowVerdict = "accepted"
	FlowRefused  FlowVerdict = "refused"
	FlowFiltered FlowVerdict = "filtered"
	FlowBanned   FlowVerdict = "banned"
)

// Flow is one connection attempt as the *destination* saw it.
type Flow struct {
	At      time.Time
	Src     string // source address as it arrived (NAT-aware)
	SrcHost string
	Port    int
	Proto   string
	Verdict FlowVerdict
}

// Ban is a real drop: while it is active the source cannot reach the machine.
type Ban struct {
	IP     string
	Host   string
	Reason string
	At     time.Time
	Until  time.Time // zero means it never expires
	Count  int       // how many times this source has been banned
	Ended  bool      // expired on its own (the log line is written once)
}

// Active reports whether the ban is in force at t.
func (b Ban) Active(t time.Time) bool {
	return !b.Ended && (b.Until.IsZero() || t.Before(b.Until))
}

// Alert is one thing a detector noticed, with the evidence it noticed it from.
type Alert struct {
	At      time.Time
	Tool    string // suricata|aide|clamav|monit|audit
	Rule    string
	Src     string
	SrcHost string
	Port    int
	Msg     string
	Level   string // info|warn|alert
	Banned  bool   // the rule's action included a ban, and it happened
}

// SecState is a machine's own security state: what tried to reach it, what
// failed to authenticate, what is banned and what has been noticed.
type SecState struct {
	Flows  []Flow
	Fails  []time.Time // one per rejected attempt
	FailIP []string    // the source of each failure, parallel to Fails
	Bans   []Ban
	Alerts []Alert
	Audit  []AuditRecord // §33 auditd: syscall-level records
	AideDB map[string]string
	// AideReported remembers, per path, the content mark already reported, so
	// one change is one alert while a second change to the same file is a new
	// one. The baseline (AideDB) is never rewritten by the check itself.
	AideReported map[string]string
	// Exploits are attack attempts other machines reported to this one; the
	// IDS consumes them on its next tick. They are part of the state so a
	// save between the attack and the tick does not lose the evidence.
	Exploits []Flow
	// RemoteLog is what this machine received as a log server (§33 central
	// logs): other devices' lines, with their source, held where a player can
	// read them even if the sender is gone.
	RemoteLog []RemoteLog

	BanCount      int
	AlertSeen     map[string]time.Time
	LastAideTick  int
	LastFreshTick int
	// LastHidsTick is when the host monitor last compared this machine with
	// its own baseline (§33 HIDS); it is a tick number, not a wall clock.
	LastHidsTick int
	Quarantined  []string
}

// Sec returns the device's security state, creating it on first use. A save
// written before §33 therefore stays loadable.
func (d *Device) Sec() *SecState {
	if d.Security == nil {
		d.Security = &SecState{}
	}
	if d.Security.AlertSeen == nil {
		d.Security.AlertSeen = map[string]time.Time{}
	}
	if d.Security.AideDB == nil {
		d.Security.AideDB = map[string]string{}
	}
	if d.Security.AideReported == nil {
		d.Security.AideReported = map[string]string{}
	}
	return d.Security
}

// There is no lock on this state because the live server holds the world lock
// around every command and tick (see cmd/neohome/main.go): the same discipline
// every other world structure follows.

// ---- the packet path's record ---------------------------------------------

// maxFlows is how much of the recent past a machine remembers. A real
// conntrack table is bigger; a list that grows forever is not a record, it is
// a leak.
const maxFlows = 400

// NoteFlow records a connection attempt the destination really saw.
func (d *Device) NoteFlow(f Flow) {
	if f.At.IsZero() {
		f.At = d.W.Sim
	}
	if f.Proto == "" {
		f.Proto = "tcp"
	}
	s := d.Sec()
	s.Flows = append(s.Flows, f)
	if len(s.Flows) > maxFlows {
		s.Flows = append(s.Flows[:0], s.Flows[len(s.Flows)-maxFlows:]...)
	}
}

// RecentFlows returns the flows inside a window, oldest first.
func (d *Device) RecentFlows(window time.Duration) []Flow {
	cut := d.W.Sim.Add(-window)
	var out []Flow
	for _, f := range d.Sec().Flows {
		if !f.At.Before(cut) {
			out = append(out, f)
		}
	}
	return out
}

// ---- bans ------------------------------------------------------------------

// failWindow is the longest a jail may look back; failures older than this are
// pruned so a machine's memory of them is bounded like a real one's.
const failWindow = 24 * time.Hour

// NoteAuthFail records a rejected authentication attempt against this machine,
// from the source that made it. The log line and the record are written
// together, so `fail2ban-client status` and `grep syslog` can never disagree.
func (d *Device) NoteAuthFail(srcIP, srcHost, what string) {
	if srcIP == "" {
		return
	}
	s := d.Sec()
	cut := d.W.Sim.Add(-failWindow)
	keepF, keepI := s.Fails[:0], s.FailIP[:0]
	for i, at := range s.Fails {
		if !at.Before(cut) {
			keepF = append(keepF, at)
			keepI = append(keepI, s.FailIP[i])
		}
	}
	s.Fails, s.FailIP = keepF, keepI
	s.Fails = append(s.Fails, d.W.Sim)
	s.FailIP = append(s.FailIP, srcIP)
	if what != "" {
		d.Logf("warn", "auth", "authentication failure from %s (%s, %s)", srcHost, srcIP, what)
	}
}

// FailsFrom counts a source's failures inside a window.
func (d *Device) FailsFrom(srcIP string, window time.Duration) int {
	cut := d.W.Sim.Add(-window)
	n := 0
	s := d.Sec()
	for i, at := range s.Fails {
		if at.Before(cut) {
			continue
		}
		if s.FailIP[i] == srcIP {
			n++
		}
	}
	return n
}

// IsBanned reports whether a source address is currently refused by this
// machine. Nothing else needs to know why: a ban is a drop.
func (d *Device) IsBanned(srcIP string) bool {
	if srcIP == "" {
		return false
	}
	for _, b := range d.Sec().Bans {
		if b.IP == srcIP && b.Active(d.W.Sim) {
			return true
		}
	}
	return false
}

// IsBannedBy reports whether this machine currently refuses a given source
// device. It checks both addresses the source is known by: the one this
// machine would log (its v6 address when both ends are dual-stack, which is
// the §13 convention) and its v4 address, so a ban written by hand from a
// firewall log line — the address an operator really reads — also lands.
func (d *Device) IsBannedBy(src *Device) bool {
	if src == nil {
		return false
	}
	if d.IsBanned(src.sourceIPFor(d)) {
		return true
	}
	if v4 := wanIP(src); v4 != "" && d.IsBanned(v4) {
		return true
	}
	return false
}

// Ban adds a ban (or refreshes one) for a source address. It is the one way a
// ban enters the world, so the log, the counter and the enforcement can never
// disagree.
func (d *Device) Ban(srcIP, srcHost, reason string, bantime time.Duration) Ban {
	s := d.Sec()
	var until time.Time
	if bantime > 0 {
		until = d.W.Sim.Add(bantime)
	}
	if srcHost == "" {
		srcHost = srcIP
	}
	for i := range s.Bans {
		if s.Bans[i].IP == srcIP && s.Bans[i].Active(d.W.Sim) {
			// an existing ban is extended, not duplicated — which is what a
			// real jail does when the offender comes back
			s.Bans[i].Until = until
			s.Bans[i].Ended = false
			s.Bans[i].Reason = reason
			s.Bans[i].Count++
			d.Logf("warn", "fail2ban", "ban extended for %s (%s): %s", srcHost, srcIP, reason)
			return s.Bans[i]
		}
	}
	b := Ban{IP: srcIP, Host: srcHost, Reason: reason, At: d.W.Sim, Until: until, Count: 1}
	s.Bans = append(s.Bans, b)
	s.BanCount++
	d.Logf("warn", "fail2ban", "ban %s (%s) for %s", srcHost, srcIP, reason)
	d.W.AddEvent(d.ID, "warn", "security", "%s banned %s: %s", d.Hostname, srcHost, reason)
	return b
}

// Unban lifts a ban by hand — the operator's own decision, which is why it is
// logged as such.
func (d *Device) Unban(srcIP string) bool {
	s := d.Sec()
	for i := range s.Bans {
		if s.Bans[i].IP == srcIP && s.Bans[i].Active(d.W.Sim) {
			s.Bans[i].Ended = true
			d.forgetFails(srcIP)
			d.Logf("info", "fail2ban", "ban lifted for %s by the operator", srcIP)
			return true
		}
	}
	return false
}

// forgetFails drops a source's recorded failures. Real fail2ban keeps its
// counter per (jail, address) and removes it when the ban ends — which is why
// an unbanned host gets its next chance rather than being banned again by the
// same old mistakes.
func (d *Device) forgetFails(srcIP string) {
	s := d.Sec()
	keepF, keepI := s.Fails[:0], s.FailIP[:0]
	for i, at := range s.Fails {
		if s.FailIP[i] == srcIP {
			continue
		}
		keepF = append(keepF, at)
		keepI = append(keepI, s.FailIP[i])
	}
	s.Fails, s.FailIP = keepF, keepI
}

// ActiveBans lists the bans currently in force.
func (d *Device) ActiveBans() []Ban {
	var out []Ban
	for _, b := range d.Sec().Bans {
		if b.Active(d.W.Sim) {
			out = append(out, b)
		}
	}
	return out
}

// ---- fail2ban --------------------------------------------------------------

// Jail is one filter+action pair from /etc/fail2ban/jail.conf.
type Jail struct {
	Name     string
	Enabled  bool
	Port     int
	MaxRetry int
	FindTime time.Duration
	BanTime  time.Duration
}

// Jails parses the machine's own jail.conf on every call: the file is the
// configuration, and a player who edits it expects the next tick to obey it.
// A file that does not parse yields no jails (fail closed, as everywhere else).
func (d *Device) Jails() []Jail {
	data, ok := d.FS.Read("/etc/fail2ban/jail.conf")
	if !ok {
		return nil
	}
	var out []Jail
	var cur *Jail
	defRetry, defFind, defBan, defPort := 3, 10*time.Minute, 10*time.Minute, 22
	flush := func() {
		if cur != nil && cur.Name != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			cur = &Jail{Name: strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"),
				Enabled: true, Port: defPort, MaxRetry: defRetry, FindTime: defFind, BanTime: defBan}
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "bantime":
			defBan = parseDuration(v, defBan)
			if cur != nil {
				cur.BanTime = defBan
			}
		case "findtime":
			defFind = parseDuration(v, defFind)
			if cur != nil {
				cur.FindTime = defFind
			}
		case "maxretry":
			defRetry = parseIntDefault(v, defRetry)
			if cur != nil {
				cur.MaxRetry = defRetry
			}
		case "port":
			defPort = parseIntDefault(v, defPort)
			if cur != nil {
				cur.Port = defPort
			}
		case "enabled":
			if cur != nil {
				cur.Enabled = !strings.EqualFold(v, "false") && v != "0" && !strings.EqualFold(v, "no")
			}
		}
	}
	flush()
	// only the sections that name a filter are jails; DEFAULT is settings
	var real []Jail
	for _, j := range out {
		if strings.EqualFold(j.Name, "default") {
			continue
		}
		real = append(real, j)
	}
	sort.Slice(real, func(i, k int) bool { return real[i].Name < real[k].Name })
	return real
}

func parseDuration(v string, def time.Duration) time.Duration {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return def
	}
	mult := time.Second
	switch v[len(v)-1] {
	case 's':
		v = v[:len(v)-1]
	case 'm':
		mult, v = time.Minute, v[:len(v)-1]
	case 'h':
		mult, v = time.Hour, v[:len(v)-1]
	case 'd':
		mult, v = 24*time.Hour, v[:len(v)-1]
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return def
	}
	return time.Duration(n) * mult
}

func parseIntDefault(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// ---- suricata --------------------------------------------------------------

// IDSRule is one line of /etc/suricata/rules.
type IDSRule struct {
	Kind    string // portscan|authfail|exploit|flood
	Ports   int    // portscan: distinct destination ports that make a scan
	Fails   int    // authfail: attempts that make a brute force
	Conns   int    // flood: connection attempts from one source that make a flood
	Window  time.Duration
	BanTime time.Duration
	Ban     bool
	Alert   bool
	Level   string
	Raw     string
}

// IDSRules parses the machine's rule file on every call, for the same reason
// the jails are parsed: the file is the configuration.
func (d *Device) IDSRules() []IDSRule {
	data, ok := d.FS.Read("/etc/suricata/rules")
	if !ok {
		return nil
	}
	var out []IDSRule
	for _, line := range strings.Split(string(data), "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}
		r := IDSRule{Kind: strings.ToLower(fields[0]), Ports: 15, Fails: 5, Conns: 200,
			Window: 60 * time.Second, BanTime: 10 * time.Minute, Alert: true, Level: "warn", Raw: raw}
		for _, f := range fields[1:] {
			k, v, found := strings.Cut(f, "=")
			if !found {
				continue
			}
			k, v = strings.ToLower(k), strings.TrimSpace(v)
			switch k {
			case "ports":
				r.Ports = parseIntDefault(v, r.Ports)
			case "fails":
				r.Fails = parseIntDefault(v, r.Fails)
			case "conns":
				r.Conns = parseIntDefault(v, r.Conns)
			case "window":
				r.Window = parseDuration(v, r.Window)
			case "bantime":
				r.BanTime = parseDuration(v, r.BanTime)
			case "level":
				r.Level = v
			case "action":
				r.Ban, r.Alert = false, false
				for _, a := range strings.Split(strings.ToLower(v), ",") {
					switch strings.TrimSpace(a) {
					case "ban":
						r.Ban = true
					case "alert":
						r.Alert = true
					}
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// floodSource is one source's connection count inside a window.
type floodSource struct {
	ip    string
	host  string
	count int
}

func (f floodSource) named() string {
	if f.host != "" {
		return f.host + " (" + f.ip + ")"
	}
	return f.ip
}

// floodSources counts what each source tried inside the rule's window. Port
// scanning also produces many flows, which is why the portscan rule exists
// separately: this one fires on volume, whatever the ports are.
func floodSources(d *Device, r IDSRule) []floodSource {
	counts := map[string]int{}
	hosts := map[string]string{}
	for _, f := range d.RecentFlows(r.Window) {
		if f.Src == "" {
			continue
		}
		counts[f.Src]++
		if f.SrcHost != "" {
			hosts[f.Src] = f.SrcHost
		}
	}
	var out []floodSource
	for ip, n := range counts {
		if n >= r.Conns {
			out = append(out, floodSource{ip: ip, host: hosts[ip], count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ip < out[j].ip })
	return out
}

// NoteExploit tells a machine that an attack against it was attempted (or
// succeeded) — the exploit paths call this, so an `exploit ... action=alert`
// rule has something real to react to.
func (d *Device) NoteExploit(srcIP, srcHost, kind string) {
	s := d.Sec()
	s.Exploits = append(s.Exploits, Flow{Src: srcIP, SrcHost: srcHost, Verdict: FlowVerdict(kind), At: d.W.Sim})
	if len(s.Exploits) > 32 {
		s.Exploits = s.Exploits[len(s.Exploits)-32:]
	}
}

// ---- the tick --------------------------------------------------------------

// SecurityTick runs the §33 tools on every machine: jails count what failed,
// suricata reads the flows, auditd is passive (its records are written by the
// hooks that see the syscalls), aide compares hashes on its own interval,
// clamav watches its database age and monit watches the services. A stopped
// service does nothing, which is the whole point of them being services.
func (w *World) SecurityTick() {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !d.Powered() {
			continue
		}
		d.expireBans()
		if d.SvcRunning("fail2ban") {
			d.fail2banTick()
		}
		if d.SvcRunning("suricata") {
			d.idsTick()
		}
		if d.SvcRunning("aide") {
			d.aideTick()
		}
		if d.SvcRunning("clamd") || d.SvcRunning("clamav") {
			d.clamTick()
		}
		if d.SvcRunning("monit") {
			d.monitTick()
		}
		if d.SvcRunning("rkhunter") {
			d.hidsTick()
		}
	}
}

// SvcRunning reports whether a named service is running on this device.
func (d *Device) SvcRunning(name string) bool {
	s := d.Svc(name)
	return s != nil && s.State == "running"
}

func (d *Device) expireBans() {
	s := d.Sec()
	for i := range s.Bans {
		b := &s.Bans[i]
		if b.Ended || b.Until.IsZero() || b.Until.After(d.W.Sim) {
			continue
		}
		b.Ended = true
		d.forgetFails(b.IP)
		d.Logf("info", "fail2ban", "ban expired for %s (%s) after %s",
			b.Host, b.IP, HumanAge(d.W.Sim.Sub(b.At)))
	}
}

// fail2banTick applies the jail configuration to the failures the machine has
// recorded. This is the tool the spec names: several failed logins, then the
// source is really cut off.
func (d *Device) fail2banTick() {
	for _, j := range d.Jails() {
		if !j.Enabled || j.MaxRetry <= 0 {
			continue
		}
		seen := map[string]bool{}
		for i, at := range d.Sec().Fails {
			if at.Before(d.W.Sim.Add(-j.FindTime)) {
				continue
			}
			ip := d.Sec().FailIP[i]
			if ip == "" || seen[ip] {
				continue
			}
			seen[ip] = true
			if d.IsBanned(ip) {
				continue
			}
			if n := d.FailsFrom(ip, j.FindTime); n >= j.MaxRetry {
				d.Ban(ip, "", fmt.Sprintf("%d failures in %s (jail %s)", n, HumanAge(j.FindTime), j.Name), j.BanTime)
			}
		}
	}
}

// idsTick is suricata's own loop: rules over the flow table, the failure list
// and the attack attempts reported to this machine. It detects what the world
// really carries — a source walking ports, a source hammering authentication,
// an exploit attempt.
func (d *Device) idsTick() {
	s := d.Sec()
	for _, r := range d.IDSRules() {
		switch r.Kind {
		case "portscan":
			for _, hit := range scanSources(d, r) {
				d.raise(Alert{Tool: "suricata", Rule: r.Kind, Src: hit.ip, SrcHost: hit.host, Port: hit.port,
					Msg:   fmt.Sprintf("%d distinct ports from %s in %s", hit.ports, hit.srcName(), HumanAge(r.Window)),
					Level: r.Level}, r)
			}
		case "authfail":
			seen := map[string]bool{}
			for i, at := range s.Fails {
				ip := s.FailIP[i]
				if ip == "" || seen[ip] || at.Before(d.W.Sim.Add(-r.Window)) {
					continue
				}
				seen[ip] = true
				if n := d.FailsFrom(ip, r.Window); n >= r.Fails {
					d.raise(Alert{Tool: "suricata", Rule: r.Kind, Src: ip, Port: 22,
						Msg:   fmt.Sprintf("%d failed authentications from %s in %s", n, ip, HumanAge(r.Window)),
						Level: r.Level}, r)
				}
			}
		case "flood":
			// §33's 异常流量: one source opening (or trying to open) an
			// unusual number of connections inside a window, whatever ports
			// it picks. This is the shape a SYN flood and a hammering script
			// both leave in the flow table.
			for _, hit := range floodSources(d, r) {
				d.raise(Alert{Tool: "suricata", Rule: r.Kind, Src: hit.ip, SrcHost: hit.host,
					Msg: fmt.Sprintf("%d connection attempts from %s in %s",
						hit.count, hit.named(), HumanAge(r.Window)),
					Level: r.Level}, r)
			}
		case "exploit":
			for _, ev := range s.Exploits {
				d.raise(Alert{Tool: "suricata", Rule: r.Kind, Src: ev.Src, SrcHost: ev.SrcHost,
					Msg: fmt.Sprintf("exploit attempt against %s: %s", d.Hostname, ev.Verdict), Level: r.Level}, r)
			}
		}
	}
	s.Exploits = nil
}

type scanHit struct {
	src, host, ip string
	ports         int
	port          int
}

func (h scanHit) srcName() string {
	if h.host != "" && h.host != h.ip {
		return h.host + " (" + h.ip + ")"
	}
	return h.ip
}

// scanSources looks for a source that touched many distinct ports inside the
// window — the definition of a port scan, and the thing an IDS exists to see.
func scanSources(d *Device, r IDSRule) []scanHit {
	ports := map[string]map[int]bool{}
	host := map[string]string{}
	last := map[string]int{}
	for _, f := range d.RecentFlows(r.Window) {
		if f.Src == "" {
			continue
		}
		if ports[f.Src] == nil {
			ports[f.Src] = map[int]bool{}
		}
		ports[f.Src][f.Port] = true
		if f.SrcHost != "" {
			host[f.Src] = f.SrcHost
		}
		if f.Port > last[f.Src] {
			last[f.Src] = f.Port
		}
	}
	var out []scanHit
	for src, set := range ports {
		if len(set) >= r.Ports {
			out = append(out, scanHit{src: src, host: host[src], ip: src, ports: len(set), port: last[src]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ip < out[j].ip })
	return out
}

// raise records one detection: the dedupe window keeps a scan from producing an
// alert per packet, exactly like a real IDS's suppression.
func (d *Device) raise(a Alert, r IDSRule) {
	if a.Src == "" {
		return
	}
	if a.At.IsZero() {
		a.At = d.W.Sim
	}
	s := d.Sec()
	key := a.Tool + "|" + a.Rule + "|" + a.Src
	if at, ok := s.AlertSeen[key]; ok && !at.Before(d.W.Sim.Add(-r.Window)) {
		return
	}
	s.AlertSeen[key] = d.W.Sim
	if len(s.AlertSeen) > 256 {
		cut := d.W.Sim.Add(-24 * time.Hour)
		for k, at := range s.AlertSeen {
			if at.Before(cut) {
				delete(s.AlertSeen, k)
			}
		}
	}
	if r.Ban {
		d.Ban(a.Src, a.SrcHost, fmt.Sprintf("%s %s: %s", a.Tool, a.Rule, a.Msg), r.BanTime)
		a.Banned = true
	}
	if !r.Alert {
		return
	}
	s.Alerts = append(s.Alerts, a)
	if len(s.Alerts) > 200 {
		s.Alerts = append(s.Alerts[:0], s.Alerts[len(s.Alerts)-200:]...)
	}
	d.Logf("warn", a.Tool, "[%s] %s", strings.ToUpper(a.Level), a.Msg)
	d.W.AddEvent(d.ID, "warn", a.Tool, "%s: %s", d.Hostname, a.Msg)
}

// Alertf is the one entry point the non-network detectors (aide, clamav,
// monit, auditd) use, so every alert in `secstat alerts` came through the same
// door and carries the same shape.
func (d *Device) Alertf(tool, rule, level, format string, a ...any) {
	a2 := Alert{Tool: tool, Rule: rule, Level: level, Msg: fmt.Sprintf(format, a...), At: d.W.Sim}
	s := d.Sec()
	key := tool + "|" + rule + "|" + a2.Msg
	if at, ok := s.AlertSeen[key]; ok && !at.Before(d.W.Sim.Add(-10*time.Minute)) {
		return
	}
	s.AlertSeen[key] = d.W.Sim
	s.Alerts = append(s.Alerts, a2)
	if len(s.Alerts) > 200 {
		s.Alerts = append(s.Alerts[:0], s.Alerts[len(s.Alerts)-200:]...)
	}
	d.Logf("warn", tool, "[%s] %s", strings.ToUpper(level), a2.Msg)
	d.W.AddEvent(d.ID, "warn", tool, "%s: %s", d.Hostname, a2.Msg)
}

// Alerts returns the alert ring, newest last.
func (d *Device) Alerts() []Alert { return d.Sec().Alerts }

// ---- operator reports ------------------------------------------------------

// Fail2BanReport is `fail2ban-client status` in the tool's own shape.
func (d *Device) Fail2BanReport() []string {
	var out []string
	jails := d.Jails()
	out = append(out, "Status")
	out = append(out, fmt.Sprintf("|- Number of jail:\t%d", len(jails)))
	out = append(out, "`- Jail list:\t\t"+jailNames(jails))
	for _, j := range jails {
		out = append(out, "")
		out = append(out, "Status for the jail: "+j.Name)
		out = append(out, "|- Filter")
		out = append(out, fmt.Sprintf("|  |- Currently failed:\t%d", d.failCount(j)))
		out = append(out, fmt.Sprintf("|  `- Total failed:\t%d", len(d.Sec().Fails)))
		out = append(out, "`- Actions")
		out = append(out, fmt.Sprintf("   |- Currently banned:\t%d", len(d.ActiveBans())))
		out = append(out, fmt.Sprintf("   `- Total banned:\t%d", d.Sec().BanCount))
		if bans := d.ActiveBans(); len(bans) > 0 {
			var ips []string
			for _, b := range bans {
				ips = append(ips, fmt.Sprintf("%s (%s)", b.IP, b.Host))
			}
			out = append(out, "   banned IP list:\t"+strings.Join(ips, ", "))
		}
	}
	return out
}

func jailNames(jails []Jail) string {
	if len(jails) == 0 {
		return "(none configured)"
	}
	var names []string
	for _, j := range jails {
		names = append(names, j.Name)
	}
	return strings.Join(names, ", ")
}

func (d *Device) failCount(j Jail) int {
	cut := d.W.Sim.Add(-j.FindTime)
	n := 0
	for _, at := range d.Sec().Fails {
		if !at.Before(cut) {
			n++
		}
	}
	return n
}

// IDSReport is `suricata-update`-ish: the rule set a machine is really running.
func (d *Device) IDSReport() []string {
	var out []string
	out = append(out, "suricata rules loaded from /etc/suricata/rules")
	for _, r := range d.IDSRules() {
		action := "alert"
		if r.Ban {
			action = "ban,alert"
		}
		extra := ""
		switch r.Kind {
		case "portscan":
			extra = fmt.Sprintf(" ports>=%d window=%s", r.Ports, HumanAge(r.Window))
		case "authfail":
			extra = fmt.Sprintf(" fails>=%d window=%s", r.Fails, HumanAge(r.Window))
		}
		if r.Ban {
			extra += " bantime=" + HumanAge(r.BanTime)
		}
		out = append(out, fmt.Sprintf("  %-8s action=%-10s level=%s%s", r.Kind, action, r.Level, extra))
	}
	if len(d.IDSRules()) == 0 {
		out = append(out, "  (no rules — /etc/suricata/rules is empty)")
	}
	return out
}
