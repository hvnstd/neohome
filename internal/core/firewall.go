package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Packet filtering (§14 家庭网络 and §28 攻击面).
//
// The world's exposures are configuration, and this file is the only thing
// that decides what a packet may reach. Two kinds of device, two real config
// languages, one state:
//
//	router (OpenWrt family)  /etc/config/firewall  (+ /etc/config/upnpd,
//	                          /var/run/miniupnpd.leases for runtime mappings)
//	host   (everything else) /etc/iptables/rules.v4, or the RHEL path
//	                          /etc/sysconfig/iptables on Fedora
//
// Nothing is cached: `FW()` re-reads the files it is given, exactly like a
// real kernel re-reads an applied ruleset only when something applies it — and
// like `FTPConfOf` in this world, editing the file is meant to be the way a
// player changes behaviour.
//
// The defaults are the household defaults §14 states: WAN→LAN blocked,
// LAN→WAN allowed, and a consumer router that does not expose its own
// management services to the internet. Every hole is a redirect, a rule or a
// UPnP mapping somebody asked for, and each of those is visible in a file the
// player can `cat`.

// ---- state ----

// Redirect is one WAN→LAN port forward (a `/etc/config/firewall` redirect, or
// a UPnP lease, which is the same thing with a shorter life).
type Redirect struct {
	Name    string
	Proto   string // tcp|udp|all
	WPort   int    // 0 means every port (DMZ, or a whole-host forward)
	DPort   int    // 0 means "same as WPort"; -1 means "every port on the target"
	DstIP   string
	Enabled bool
	// DMZ marks a redirect that sends everything to one host. Real firmware
	// implements it as a redirect with no destination port.
	DMZ bool
	// UPnP marks a mapping a LAN program asked for at runtime rather than one
	// an administrator configured.
	UPnP bool
	Desc string
}

// Rule is an ACCEPT/REJECT rule for traffic arriving at a device itself.
type Rule struct {
	Name   string
	Src    string // wan|lan
	Proto  string
	Port   int // 0 = any port
	Target string
	// File names where the rule was read from, so a refusal can explain itself.
	File string
}

// UPnPLease is one mapping a LAN program requested through the router's UPnP
// daemon. Real miniupnpd keeps its live mappings in a leases file, which is
// exactly why a player can read it and find out who opened what.
type UPnPLease struct {
	Proto string
	EPort int
	IP    string
	IPort int
	Desc  string
}

// FirewallState is what a device's configuration currently says.
type FirewallState struct {
	Files []string // every file consulted, in order

	// WANInput: policy for packets addressed to the ROUTER itself.
	WANInput string // REJECT|DROP|ACCEPT
	// ForwardPolicy: policy for WAN traffic the router would forward to the LAN.
	ForwardPolicy string
	// HostInput: policy for a non-router device's own ports.
	HostInput string

	Redirects []Redirect
	Rules     []Rule

	LogDrops bool

	UPnPEnabled bool
	UPnPLeases  []UPnPLease

	// Errors are parse failures, reported verbatim to whoever asks (and to the
	// device's log when a reload runs).
	Errors []string
}

// FW reads the device's real configuration and returns what it says. Safe on a
// device with no firewall config at all: it falls back to the defaults for
// that profile, which is what a device with no ruleset does.
func (d *Device) FW() *FirewallState {
	st := &FirewallState{
		WANInput:      "REJECT",
		ForwardPolicy: "REJECT",
		LogDrops:      false,
	}
	if d == nil {
		return st
	}
	if d.Profile == "router" {
		st.HostInput = "REJECT"
	} else if d.isInfraHost() {
		// infrastructure and VPS images ship open: that is why everything a
		// player publishes on one of them is reachable, and why closing it
		// down is a real, worthwhile action
		st.HostInput = "ACCEPT"
	} else {
		st.HostInput = "DROP"
	}

	if cf := d.readFirewallConfig(st); cf != nil && len(st.Errors) == 0 {
		st.applyUCIFirewall(cf)
	}
	if d.Profile != "router" {
		d.applyHostRules(st)
	}
	if d.Profile == "router" {
		d.readUPnP(st)
	}
	return st
}

func (d *Device) isInfraHost() bool {
	switch d.Profile {
	case "core", "vps", "infra", "peer", "server":
		return true
	}
	return false
}

// A note on timing: the packet path reads the configuration every time rather
// than caching an applied ruleset, so there is exactly one source of truth and
// no way for the world and the file to disagree. The price is that a config
// edit takes effect without `/etc/init.d/firewall reload` — which is why the
// reload verb exists as a *check* (it reports parse errors and what is in
// force) and why a file that does not parse is treated as "no rules": fail
// closed, with the error available in `uci changes`-style reporting, rather
// than silently keeping the last good holes open.

// ---- uci firewall (routers) ----

func (d *Device) readFirewallConfig(st *FirewallState) *UCIFile {
	f, errs, ok := d.ReadUCIFile("firewall")
	st.Files = append(st.Files, UCIPath("firewall"))
	st.Errors = append(st.Errors, errs...)
	if !ok {
		return nil
	}
	return f
}

func (st *FirewallState) applyUCIFirewall(f *UCIFile) {
	for _, s := range f.SectionsOf("defaults") {
		if v := s.Get("wan_input"); v != "" {
			st.WANInput = strings.ToUpper(v)
		}
		if v := s.Get("forward"); v != "" {
			st.ForwardPolicy = strings.ToUpper(v)
		}
		if v := s.Get("log_drops"); v != "" {
			st.LogDrops = uciOptionTrue(v)
		}
	}
	for _, s := range f.SectionsOf("redirect") {
		r := Redirect{
			Name:    s.Name,
			Proto:   strings.ToLower(orDefault(s.Get("proto"), "tcp")),
			DstIP:   s.Get("dest_ip"),
			Desc:    s.Get("name"),
			Enabled: true,
		}
		if v := s.Get("enabled"); v != "" {
			r.Enabled = uciOptionTrue(v)
		}
		if p := s.Get("src_dport"); p != "" {
			r.WPort = firstPort(p)
		}
		if p := s.Get("dest_port"); p != "" {
			r.DPort = firstPort(p)
		} else if r.WPort != 0 {
			r.DPort = r.WPort
		}
		// A redirect with no source port sends everything to that host: that
		// is what DMZ means in practice, on this world's routers and on real
		// ones.
		r.DMZ = r.WPort == 0 && r.DstIP != ""
		if uciOptionTrue(s.Get("dmz")) {
			r.DMZ = true
			r.WPort = 0
		}
		if r.DstIP != "" {
			st.Redirects = append(st.Redirects, r)
		}
	}
	for _, s := range f.SectionsOf("rule") {
		r := Rule{
			Name:   s.Name,
			Src:    strings.ToLower(orDefault(s.Get("src"), "lan")),
			Proto:  strings.ToLower(orDefault(s.Get("proto"), "tcp")),
			Target: strings.ToUpper(orDefault(s.Get("target"), "ACCEPT")),
			File:   UCIPath("firewall"),
		}
		if v := s.Get("dest_port"); v != "" {
			r.Port = firstPort(v)
		}
		if v := s.Get("dest_ip"); v != "" {
			r.Name = orDefault(r.Name, v)
		}
		if v := s.Get("enabled"); v != "" && !uciOptionTrue(v) {
			continue
		}
		st.Rules = append(st.Rules, r)
	}
}

// ---- iptables (hosts) ----

// iptablesPath is where a distribution's persistent ruleset lives. Debian and
// Alpine use the netfilter-persistent path; Fedora and RHEL use their own.
func (d *Device) iptablesPath() string {
	switch strings.ToLower(d.OS.Distro) {
	case "fedora", "rhel", "centos":
		return "/etc/sysconfig/iptables"
	}
	return "/etc/iptables/rules.v4"
}

func (d *Device) applyHostRules(st *FirewallState) {
	path := d.iptablesPath()
	st.Files = append(st.Files, path)
	data, ok := d.FS.Read(path)
	if !ok {
		return // no ruleset: the profile default stands
	}
	table := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "*") {
			table = strings.TrimPrefix(line, "*")
			continue
		}
		if table != "filter" {
			continue
		}
		fields := strings.Fields(line)
		// iptables-save writes the policy as `:INPUT DROP [0:0]`; the command
		// writes it as `-P INPUT DROP`. Both mean the same thing, and a file
		// edited by either has to be read the same way.
		if strings.HasPrefix(fields[0], ":") && len(fields) >= 2 {
			if strings.EqualFold(strings.TrimPrefix(fields[0], ":"), "INPUT") {
				st.HostInput = strings.ToUpper(fields[1])
			}
			continue
		}
		switch fields[0] {
		case "-P":
			if len(fields) >= 3 && strings.EqualFold(fields[1], "INPUT") {
				st.HostInput = strings.ToUpper(fields[2])
			}
		case "-A":
			r, ok := parseIPTablesRule(fields, path)
			if !ok {
				continue
			}
			st.Rules = append(st.Rules, r)
		}
	}
}

// parseIPTablesRule reads the subset of iptables-save syntax this world's
// hosts actually use: `-A INPUT -p tcp --dport 80 -j ACCEPT`, optionally with
// `-i eth0` (wan) or `-s <addr>`.
func parseIPTablesRule(fields []string, file string) (Rule, bool) {
	r := Rule{Target: "ACCEPT", File: file, Src: "lan"}
	if len(fields) < 2 || fields[1] != "INPUT" {
		return r, false
	}
	for i := 2; i < len(fields); i++ {
		switch fields[i] {
		case "-p":
			if i+1 < len(fields) {
				r.Proto = strings.ToLower(fields[i+1])
				i++
			}
		case "--dport":
			if i+1 < len(fields) {
				r.Port = firstPort(fields[i+1])
				i++
			}
		case "-j":
			if i+1 < len(fields) {
				r.Target = strings.ToUpper(fields[i+1])
				i++
			}
		case "-i":
			if i+1 < len(fields) {
				if strings.HasPrefix(fields[i+1], "eth1") || strings.Contains(fields[i+1], "wan") {
					r.Src = "wan"
				}
				i++
			}
		case "-s":
			// a rule scoped to a source address is not a general WAN accept
			i++
			r.Src = "lan"
		default:
			r.Name = fields[i]
		}
	}
	return r, true
}

// ---- upnp ----

// readUPnP loads the router's UPnP daemon configuration and its live leases.
func (d *Device) readUPnP(st *FirewallState) {
	if cfg, errs, ok := d.ReadUCIFile("upnpd"); ok {
		st.Files = append(st.Files, UCIPath("upnpd"))
		st.Errors = append(st.Errors, errs...)
		for _, s := range cfg.SectionsOf("upnpd") {
			st.UPnPEnabled = uciOptionTrue(orDefault(s.Get("enabled"), "0"))
		}
	}
	leases, _ := d.FS.Read(UPnPLeasePath)
	st.Files = append(st.Files, UPnPLeasePath)
	st.UPnPLeases = ParseUPnPLeases(string(leases))
}

// UPnPLeasePath is where the daemon keeps the mappings LAN programs asked for.
// Real miniupnpd does the same, which is why a mapping can be found by reading
// a file rather than by guessing.
const UPnPLeasePath = "/var/run/miniupnpd.leases"

// ParseUPnPLeases reads the lease file: one mapping per line,
// `proto eport iaddr iport desc`.
func ParseUPnPLeases(body string) []UPnPLease {
	var out []UPnPLease
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "===") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		desc := ""
		if len(f) > 4 {
			desc = strings.Trim(f[4], "\"")
		}
		out = append(out, UPnPLease{
			Proto: strings.ToLower(f[0]),
			EPort: atoi(f[1]),
			IP:    f[2],
			IPort: atoi(f[3]),
			Desc:  desc,
		})
	}
	return out
}

// RenderUPnPLeases writes the lease file back out.
func RenderUPnPLeases(leases []UPnPLease) string {
	var b strings.Builder
	b.WriteString("# miniupnpd lease file: proto eport iaddr iport desc\n")
	for _, l := range leases {
		fmt.Fprintf(&b, "%s %d %s %d \"%s\"\n", strings.ToUpper(l.Proto), l.EPort, l.IP, l.IPort, l.Desc)
	}
	return b.String()
}

// UPnPMappings turns the live leases into redirects: a mapping is a port
// forward with no administrator behind it.
func (st *FirewallState) UPnPMappings() []Redirect {
	var out []Redirect
	for _, l := range st.UPnPLeases {
		out = append(out, Redirect{
			Name: "upnp-" + itoa(l.EPort), Proto: l.Proto, WPort: l.EPort, DPort: l.IPort,
			DstIP: l.IP, Enabled: st.UPnPEnabled, UPnP: true, Desc: l.Desc,
		})
	}
	return out
}

// AllRedirects is everything that can carry a WAN packet to a LAN host:
// configured forwards plus runtime UPnP mappings.
func (st *FirewallState) AllRedirects() []Redirect {
	out := append([]Redirect{}, st.Redirects...)
	out = append(out, st.UPnPMappings()...)
	return out
}

// ---- matching ----

// protoMatches: a redirect for "all" covers tcp, and vice versa.
func protoMatches(rule, want string) bool {
	if rule == "" || rule == "all" || want == "" {
		return true
	}
	return rule == want
}

// RedirectFor finds the redirect that carries this WAN port to an inner host.
// A DMZ redirect matches every port, which is exactly why a DMZ is a decision
// worth thinking about.
func (st *FirewallState) RedirectFor(port int) *Redirect {
	all := st.AllRedirects()
	for i := range all {
		r := &all[i]
		if !r.Enabled {
			continue
		}
		if r.WPort == 0 || r.WPort == port {
			return r
		}
	}
	return nil
}

// WANAllowsPort reports whether an explicit rule lets this port in from the
// WAN. Such a rule is what turns a router's own management interface, or a
// host's listening service, into something the internet can reach — both the
// "Remote Management" and the "Misconfigured Firewall" entries of §14.
func (st *FirewallState) WANAllowsPort(port int) bool {
	for _, r := range st.Rules {
		if r.Src != "wan" || r.Target != "ACCEPT" {
			continue
		}
		if r.Port == 0 || r.Port == port {
			return true
		}
	}
	return false
}

// HostAcceptsWAN reports whether the device's own ruleset lets this port
// arrive from outside.
func (st *FirewallState) HostAcceptsWAN(port int) bool { return st.WANAllowsPort(port) }

// ForwardAllowsEverything is the misconfiguration §14 names: a rule that
// permits WAN→LAN forwarding for every port, so the router's own default deny
// stops meaning anything.
func (st *FirewallState) ForwardAllowsEverything() bool {
	for _, r := range st.Rules {
		if r.Target == "ACCEPT" && r.Port == 0 && (r.Src == "wan" || r.Src == "all") {
			return true
		}
	}
	return false
}

// ---- rendering (seeding and the commands) ----

// RenderIPTables writes a ruleset in iptables-save format: what the `iptables`
// command edits and what `iptables-restore` would load.
func RenderIPTables(policy string, accepts []Rule) string {
	var b strings.Builder
	b.WriteString("# generated by neohome; edit with iptables(8)\n")
	b.WriteString("*filter\n")
	fmt.Fprintf(&b, ":INPUT %s [0:0]\n:FORWARD %s [0:0]\n:OUTPUT ACCEPT [0:0]\n", policy, policy)
	for _, r := range accepts {
		line := "-A INPUT"
		if r.Proto != "" && r.Proto != "all" {
			line += " -p " + r.Proto
		}
		if r.Src == "wan" {
			line += " -i eth1"
		}
		if r.Port != 0 {
			line += " --dport " + itoa(r.Port)
		}
		line += " -j " + orDefault(r.Target, "ACCEPT")
		if r.Name != "" {
			line += " # " + r.Name
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

// RenderUCIFirewall writes a router's firewall config from the same facts the
// packet path reads.
func RenderUCIFirewall(st *FirewallState) string {
	f := &UCIFile{Name: "firewall"}
	def := f.Add("defaults", "")
	def.Set("input", "ACCEPT")
	def.Set("output", "ACCEPT")
	def.Set("forward", orDefault(st.ForwardPolicy, "REJECT"))
	def.Set("wan_input", orDefault(st.WANInput, "REJECT"))
	if st.LogDrops {
		def.Set("log_drops", "1")
	}
	zone := f.Add("zone", "lan")
	zone.Set("name", "lan")
	zone.Set("input", "ACCEPT")
	zone.Set("forward", "ACCEPT")
	zone.Set("output", "ACCEPT")
	for _, r := range st.Redirects {
		s := f.Add("redirect", r.Name)
		s.Set("target", "DNAT")
		s.Set("src", "wan")
		s.Set("proto", orDefault(r.Proto, "tcp"))
		if r.WPort != 0 {
			s.Set("src_dport", itoa(r.WPort))
		}
		s.Set("dest_ip", r.DstIP)
		if r.DPort != 0 {
			s.Set("dest_port", itoa(r.DPort))
		}
		if r.Enabled {
			s.Set("enabled", "1")
		} else {
			s.Set("enabled", "0")
		}
	}
	for _, r := range st.Rules {
		s := f.Add("rule", r.Name)
		s.Set("src", orDefault(r.Src, "wan"))
		s.Set("proto", orDefault(r.Proto, "tcp"))
		if r.Port != 0 {
			s.Set("dest_port", itoa(r.Port))
		}
		s.Set("target", orDefault(r.Target, "ACCEPT"))
	}
	return f.Render()
}

// EnableRedirect turns one configured forward on or off, in the file. It is
// the mechanism the NPC hardening path and the tests use; players use `uci`.
func (d *Device) EnableRedirect(name string, on bool) bool {
	f, _, ok := d.ReadUCIFile("firewall")
	if !ok {
		return false
	}
	s := f.Find(name)
	if s == nil || s.Type != "redirect" {
		return false
	}
	if on {
		s.Set("enabled", "1")
	} else {
		s.Set("enabled", "0")
	}
	d.WriteUCIFile(f)
	return true
}

// Redirects is the configured forwards (not the UPnP leases).
func (d *Device) Redirects() []Redirect { return d.FW().Redirects }

// ---- helpers ----

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// firstPort parses an OpenWrt port specification: "80", "80-90", "80 443",
// "21,22". The range forms resolve to their first port, which is all a single
// dial needs and all this world's configs use.
func firstPort(v string) int {
	v = strings.TrimSpace(v)
	for _, sep := range []string{" ", ",", "-", ":"} {
		if i := strings.Index(v, sep); i > 0 {
			v = v[:i]
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

// sortedRedirectNames is used by reporting so lists are stable.
func sortedRedirectNames(rs []Redirect) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

// ---- UPnP: a LAN program asking the router for a hole ----

// UPnPMap records a mapping a LAN program requested through the gateway's UPnP
// daemon. It is the §14 hazard in its real shape: the request comes from
// software the owner ran, not from an administrator, and it leaves the
// router's own configuration untouched — so the only traces are the lease file
// and the log line.
func (w *World) UPnPMap(router, requester *Device, l UPnPLease) error {
	if router == nil || router.Profile != "router" {
		return fmt.Errorf("no gateway to ask")
	}
	st := router.FW()
	if !st.UPnPEnabled {
		return fmt.Errorf("UPnP is disabled on %s (see %s)", router.Hostname, UCIPath("upnpd"))
	}
	if l.EPort <= 0 || l.EPort > 65535 || l.IPort <= 0 || l.IPort > 65535 {
		return fmt.Errorf("bad port in mapping %d -> %d", l.EPort, l.IPort)
	}
	if l.Proto != "tcp" && l.Proto != "udp" {
		return fmt.Errorf("bad protocol %q", l.Proto)
	}
	// the daemon only maps hosts on the network it protects
	if ipID, ok := w.IPMap[l.IP]; !ok || w.Devices[ipID] == nil {
		return fmt.Errorf("%s is not a host on this network", l.IP)
	} else if r := w.routerFor(w.Devices[ipID]); r != router {
		return fmt.Errorf("%s is not behind %s", l.IP, router.Hostname)
	}
	leases := []UPnPLease{}
	for _, have := range st.UPnPLeases {
		if have.EPort == l.EPort && have.Proto == l.Proto {
			continue // an IGD replaces the mapping it already holds
		}
		leases = append(leases, have)
	}
	leases = append(leases, l)
	router.FS.Write(UPnPLeasePath, RenderUPnPLeases(leases), 0644, "root", "root")
	who := "a local program"
	if requester != nil {
		who = requester.Hostname + " (" + requester.ID + ")"
	}
	router.Logf("info", "miniupnpd", "mapping added: %s %d -> %s:%d \"%s\", requested by %s",
		strings.ToUpper(l.Proto), l.EPort, l.IP, l.IPort, l.Desc, who)
	w.AddEvent(router.ID, "info", "miniupnpd", "%s opened %s/%d through UPnP for %s:%d (%s)",
		who, strings.ToUpper(l.Proto), l.EPort, l.IP, l.IPort, orDefault(l.Desc, "no description"))
	return nil
}

// UPnPUnmap removes a mapping, by whomever: the program that asked, or the
// owner who found it. It reports whether anything was removed.
func (w *World) UPnPUnmap(router *Device, eport int, proto string, by *Device) bool {
	if router == nil {
		return false
	}
	st := router.FW()
	kept := []UPnPLease{}
	removed := ""
	for _, l := range st.UPnPLeases {
		if l.EPort == eport && (proto == "" || l.Proto == proto) {
			removed = fmt.Sprintf("%s/%d -> %s:%d", strings.ToUpper(l.Proto), l.EPort, l.IP, l.IPort)
			continue
		}
		kept = append(kept, l)
	}
	if removed == "" {
		return false
	}
	router.FS.Write(UPnPLeasePath, RenderUPnPLeases(kept), 0644, "root", "root")
	actor := "the owner"
	if by != nil {
		actor = by.Hostname
	}
	router.Logf("info", "miniupnpd", "mapping removed by %s: %s", actor, removed)
	w.AddEvent(router.ID, "info", "miniupnpd", "%s closed UPnP mapping %s", actor, removed)
	return true
}

// ExposureSummary is the router's own answer to "what is open right now?",
// which recon prints instead of guessing from memory.
func (d *Device) ExposureSummary() []string {
	st := d.FW()
	var out []string
	for _, r := range st.AllRedirects() {
		if !r.Enabled {
			continue
		}
		port := fmt.Sprintf("%d", r.WPort)
		switch {
		case r.DMZ:
			port = "all ports"
		case r.WPort == 0:
			port = "all ports"
		}
		how := "forward"
		if r.UPnP {
			how = "upnp"
		}
		target := r.DstIP
		if r.DPort > 0 {
			target = fmt.Sprintf("%s:%d", r.DstIP, r.DPort)
		}
		out = append(out, fmt.Sprintf("%s %s/%s -> %s", how, port, orDefault(r.Proto, "tcp"), target))
	}
	if d.Profile == "router" && strings.EqualFold(st.WANInput, "ACCEPT") {
		out = append(out, "warning: the router's own WAN input policy is ACCEPT")
	}
	for _, r := range st.Rules {
		if r.Src == "wan" && r.Target == "ACCEPT" {
			port := "all"
			if r.Port != 0 {
				port = itoa(r.Port)
			}
			out = append(out, fmt.Sprintf("rule allow wan %s/%s", port, orDefault(r.Proto, "tcp")))
		}
	}
	sort.Strings(out)
	return out
}
