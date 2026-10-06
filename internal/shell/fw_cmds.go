package shell

// Firewall and exposure management (§14 家庭网络, §28 攻击面).
//
// Every command here edits a real file that the packet path reads:
//
//	uci set firewall.<section>.enabled=1 && /etc/init.d/firewall reload
//	iptables -A INPUT -p tcp --dport 443 -j ACCEPT
//	upnpc -a 10.77.1.40 554 554 tcp cloud-cam
//
// Nothing is cached and nothing is decorative: after `uci commit` the next
// connection the world makes is judged by the new text, and the same text is
// what `cat /etc/config/firewall` shows.

import (
	"fmt"
	"sort"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"uci", cmdUCI}, {"iptables", cmdIptables}, {"ip6tables", cmdIp6tables}, {"upnpc", cmdUpnpc},
	} {
		builtinTable[e.name] = e.fn
	}
}

// ---- uci ----

// cmdUCI implements the real shape of the tool: show / get / set / add /
// add_list / delete / commit / revert / changes. Writes go to what OpenWrt
// calls the "changes" area first; commit applies them to the config file.
// Here that is one step (the file is the only state), but the two verbs behave
// differently, exactly as a player expects: a `set` without `commit` says so
// and the packet path is unchanged.
func cmdUCI(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: uci [show|get|set|add|add_list|delete|commit|revert|changes] [config[.section[.option]]] [value]")
		return 1
	}
	if s.User.UID != 0 {
		s.errf("uci: I/O error: permission denied (are you root?)")
		return 1
	}
	switch args[0] {
	case "show":
		return uciShow(s, args[1:])
	case "get":
		return uciGet(s, args[1:])
	case "set":
		return uciSet(s, args[1:])
	case "add":
		return uciAdd(s, args[1:])
	case "add_list":
		return uciAddList(s, args[1:])
	case "delete":
		return uciDelete(s, args[1:])
	case "commit":
		return uciCommit(s, args[1:])
	case "changes":
		return uciChanges(s)
	case "revert":
		return uciRevert(s, args[1:])
	}
	s.errf("uci: unknown command '%s'", args[0])
	return 1
}

// uciRef splits `firewall.wan-ssh.src` into its three parts.
func uciRef(ref string) (config, section, option string) {
	parts := strings.SplitN(ref, ".", 3)
	config = parts[0]
	if len(parts) > 1 {
		section = parts[1]
	}
	if len(parts) > 2 {
		option = parts[2]
	}
	return
}

// uciLoad reads the config a reference names, or complains the way uci does.
// Pending changes win: a second `uci set` has to build on the first one, not
// silently start again from the file.
func uciLoad(s *Shell, config string) (*core.UCIFile, bool) {
	if f, ok := s.W.UCIStaged(s.Dev, config); ok {
		return f, true
	}
	f, errs, ok := s.Dev.ReadUCIFile(config)
	if !ok {
		s.errf("uci: Entry not found")
		return nil, false
	}
	// Real uci refuses to operate on a file it cannot parse, so a broken
	// config cannot be silently rewritten from a lossy parse: the owner has
	// to fix the file. This is also what keeps fail-closed honest.
	if len(errs) > 0 {
		s.errf("uci: Parse error (%s:%s)", config, errs[0])
		return nil, false
	}
	return f, true
}

func uciShow(s *Shell, args []string) int {
	// `uci show` with no argument shows every config on the box.
	var configs []string
	if len(args) > 0 {
		configs = []string{uciRefName(args[0])}
	} else {
		names := map[string]bool{}
		for _, p := range core.UCIKnownConfigs {
			if s.Dev.FS.Exists(core.UCIPath(p)) {
				names[p] = true
			}
		}
		for _, f := range s.Dev.FS.List("/etc/config") {
			base := f[strings.LastIndex(f, "/")+1:]
			if base != "" {
				names[base] = true
			}
		}
		for n := range names {
			configs = append(configs, n)
		}
		sort.Strings(configs)
	}
	rc := 0
	for _, c := range configs {
		f, ok := uciLoad(s, c)
		if !ok {
			rc = 1
			continue
		}
		for i, sec := range f.Sections {
			ref := sec.Name
			if ref == "" {
				ref = fmt.Sprintf("@%s[%d]", sec.Type, i)
			}
			fmt.Fprintf(s.Out, "%s.%s=%s\n", c, ref, sec.Type)
			for _, o := range sec.Options {
				key := o.Key
				if o.List {
					fmt.Fprintf(s.Out, "%s.%s.%s+='%s'\n", c, ref, key, o.Val)
					continue
				}
				fmt.Fprintf(s.Out, "%s.%s.%s='%s'\n", c, ref, o.Key, o.Val)
			}
		}
	}
	// a config with sections but no name: index refs need care, so print the
	// real file shape too when asked for a specific section
	return rc
}

func uciGet(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: uci get <config>.<section>[.<option>]")
		return 1
	}
	config, section, option := uciRef(args[0])
	f, ok := uciLoad(s, config)
	if !ok {
		return 1
	}
	sec := f.Find(section)
	if sec == nil {
		return 1 // uci prints nothing and exits 1 for a missing entry
	}
	if option == "" {
		fmt.Fprintln(s.Out, sec.Type)
		return 0
	}
	if v := sec.Get(option); v != "" {
		fmt.Fprintln(s.Out, v)
		return 0
	}
	return 1
}

// uciSet applies a change and leaves it pending until commit — the distinction
// that makes `uci changes` mean something.
func uciSet(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: uci set <config>.<section>[.<option>]=<value>")
		return 1
	}
	assign := args[0]
	if len(args) > 1 {
		assign += " " + strings.Join(args[1:], " ")
	}
	ref, val, ok := strings.Cut(assign, "=")
	if !ok {
		s.errf("uci: parse error (expecting '=')")
		return 1
	}
	val = strings.Trim(val, "'\"")
	config, section, option := uciRef(strings.TrimSpace(ref))
	if section == "" {
		s.errf("uci: parse error (need a section)")
		return 1
	}
	f, ok := uciLoad(s, config)
	if !ok {
		return 1
	}
	sec := f.Find(section)
	created := false
	if option == "" {
		// `uci set <config>.<name>=<type>`: create a named section of that
		// type, or rename an existing one. This is the documented way to add
		// a section without `uci add`, and players use it constantly.
		if sec == nil {
			if val == "" {
				s.errf("uci: Invalid argument")
				return 1
			}
			for _, other := range f.Sections {
				if other.Name == section {
					sec = other
					break
				}
			}
			if sec == nil {
				sec = f.Add(val, section)
				created = true
			}
		} else {
			sec.Type = val
		}
	} else {
		if sec == nil {
			s.errf("uci: Invalid argument")
			return 1
		}
		sec.Set(option, val)
	}
	s.W.StageUCIChange(s.Dev, config, f, assign)
	if created {
		fmt.Fprintf(s.Out, "uci: section '%s' of type '%s' created (pending commit: run 'uci commit %s')\n", section, val, config)
		return 0
	}
	if option == "" {
		fmt.Fprintf(s.Out, "uci: section '%s' renamed to '%s' (pending commit)\n", section, val)
		return 0
	}
	fmt.Fprintf(s.Out, "%s.%s.%s='%s' (pending commit: run 'uci commit %s')\n", config, section, option, val, config)
	return 0
}

func uciAdd(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: uci add <config> <type> [name]")
		return 1
	}
	config := uciRefName(args[0])
	f, ok := uciLoad(s, config)
	if !ok {
		return 1
	}
	name := ""
	if len(args) > 2 {
		name = args[2]
		if f.Find(name) != nil {
			s.errf("uci: section '%s' already exists", name)
			return 1
		}
	}
	f.Add(args[1], name)
	s.W.StageUCIChange(s.Dev, config, f, "add "+args[1]+" "+name)
	ref := name
	if ref == "" {
		ref = "@" + args[1] + "[0]"
	}
	fmt.Fprintf(s.Out, "config %s '%s' (pending commit: run 'uci commit %s')\n", args[1], ref, config)
	return 0
}

func uciAddList(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: uci add_list <config>.<section>.<option>=<value>")
		return 1
	}
	ref, val, ok := strings.Cut(args[0], "=")
	if !ok {
		s.errf("uci: parse error (expecting '=')")
		return 1
	}
	val = strings.Trim(val, "'\"")
	config, section, option := uciRef(ref)
	f, ok2 := uciLoad(s, config)
	if !ok2 {
		return 1
	}
	sec := f.Find(section)
	if sec == nil || option == "" {
		s.errf("uci: Invalid argument")
		return 1
	}
	sec.AddList(option, val)
	s.W.StageUCIChange(s.Dev, config, f, strings.TrimSpace(ref)+"="+val)
	return 0
}

func uciDelete(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: uci delete <config>.<section>[.<option>]")
		return 1
	}
	config, section, option := uciRef(args[0])
	f, ok := uciLoad(s, config)
	if !ok {
		return 1
	}
	sec := f.Find(section)
	if sec == nil {
		s.errf("uci: Entry not found")
		return 1
	}
	if option == "" {
		f.Delete(sec)
	} else if !sec.Del(option) {
		s.errf("uci: Entry not found")
		return 1
	}
	s.W.StageUCIChange(s.Dev, config, f, strings.TrimSpace(args[0]))
	return 0
}

// uciCommit applies pending changes to the config file. This is the moment the
// packet path starts behaving differently.
func uciCommit(s *Shell, args []string) int {
	if len(args) == 0 {
		n := s.W.CommitUCIChanges(s.Dev)
		if n == 0 {
			fmt.Fprintln(s.Out, "uci: no changes to commit")
			return 0
		}
		fmt.Fprintf(s.Out, "uci: committed %d change(s)\n", n)
		return 0
	}
	config := uciRefName(args[0])
	if !s.W.CommitUCIChange(s.Dev, args[0]) {
		fmt.Fprintf(s.Out, "uci: no changes to commit for %s\n", config)
		return 0
	}
	fmt.Fprintf(s.Out, "uci: committed changes to %s\n", config)
	return 0
}

func uciChanges(s *Shell) int {
	changes := s.W.UCIChanges(s.Dev)
	if len(changes) == 0 {
		fmt.Fprintln(s.Out, "uci: no pending changes")
		return 0
	}
	for _, c := range changes {
		fmt.Fprintln(s.Out, c)
	}
	return 0
}

// uciRevert throws pending changes away.
func uciRevert(s *Shell, args []string) int {
	n := s.W.RevertUCIChanges(s.Dev, args)
	if n == 0 {
		fmt.Fprintln(s.Out, "uci: no changes to revert")
		return 0
	}
	fmt.Fprintf(s.Out, "uci: reverted %d change(s)\n", n)
	return 0
}

// ---- iptables ----

// cmdIptables edits the persistent ruleset of a host: the same file the packet
// path reads for that machine's own ports. Only the filter table and the
// INPUT chain are modelled, because they are the ones that decide whether a
// service the player publishes is reachable from the internet.
func cmdIptables(s *Shell, args []string) int { return iptablesRun(s, args, false) }

// cmdIp6tables is the same tool for §13's other family. It edits the v6 ruleset
// the packet path reads, so a host can be published (or closed) on v6 alone —
// which is what a dual-stack world has to allow.
func cmdIp6tables(s *Shell, args []string) int { return iptablesRun(s, args, true) }

func iptablesRun(s *Shell, args []string, v6 bool) int {
	tool := "iptables"
	if v6 {
		tool = "ip6tables"
	}
	if s.User.UID != 0 {
		s.errf("%s v1.8.10 (legacy): can't initialize iptables table `filter': Permission denied (you must be root)", tool)
		return 1
	}
	if len(args) == 0 {
		s.errf("%s v1.8.10: no command specified", tool)
		return 1
	}
	path := iptablesPath(s, v6)
	if args[0] == "-S" || args[0] == "--list-rules" {
		if data, ok := s.Dev.FS.Read(path); ok {
			fmt.Fprint(s.Out, string(data))
		} else {
			fmt.Fprintf(s.Out, "-P INPUT %s\n-P FORWARD %s\n-P OUTPUT ACCEPT\n", defaultInputPolicy(s), defaultInputPolicy(s))
		}
		return 0
	}
	if args[0] == "-L" || args[0] == "--list" {
		st := s.Dev.FW()
		rules, policy := st.Rules, st.HostInput
		family := "ipv4"
		if v6 {
			rules, policy, family = st.Rules6, st.HostInput6, "ipv6"
		}
		fmt.Fprintf(s.Out, "Chain INPUT (policy %s) [%s]\n", orD(policy, "ACCEPT"), family)
		fmt.Fprintf(s.Out, "%-8s %-16s %-8s %s\n", "target", "prot", "opt", "source/destination")
		if len(rules) == 0 {
			fmt.Fprintln(s.Out, "(no rules: the policy alone decides)")
		}
		for _, r := range rules {
			dst := "anywhere"
			if r.Port != 0 {
				dst = fmt.Sprintf("dport %d", r.Port)
			}
			src := "anywhere"
			if r.Src != "wan" {
				src = "lan"
			}
			fmt.Fprintf(s.Out, "%-8s %-16s %-8s %s -> %s\n", r.Target, orD(r.Proto, "all"), "--", src, dst)
		}
		fmt.Fprintln(s.Out, "Chain FORWARD (policy DROP)")
		fmt.Fprintln(s.Out, "Chain OUTPUT (policy ACCEPT)")
		return 0
	}
	// -P sets the default policy: the verb that decides whether an unlisted
	// port is reachable at all
	if args[0] == "-P" || args[0] == "--policy" {
		if len(args) < 3 {
			s.errf("iptables v1.8.10: -P requires a chain and a policy")
			return 1
		}
		policy := strings.ToUpper(args[2])
		if policy != "ACCEPT" && policy != "DROP" && policy != "REJECT" {
			s.errf("iptables v1.8.10: invalid policy %q", args[2])
			return 1
		}
		rules := readHostRules(s, v6)
		s.Dev.FS.Write(path, core.RenderIPTables(policy, rules), 0644, "root", "root")
		s.Dev.Logf("warn", "firewall", "%s INPUT policy set to %s by %s", tool, policy, s.User.Name)
		s.W.AddEvent(s.Dev.ID, "info", "firewall", "%s set the default INPUT policy on %s to %s",
			s.User.Name, s.Dev.Hostname, policy)
		return 0
	}

	// mutating verbs: -A/-I/-D/-F
	verb := args[0]
	rest := args[1:]
	chain, rule, ok := parseIPTableArgs(rest)
	if !ok {
		s.errf("iptables v1.8.10: %s requires a chain and a rule", verb)
		return 1
	}
	rules := readHostRules(s, v6)
	switch verb {
	case "-A", "--append", "-I", "--insert":
		if chain != "INPUT" {
			s.errf("iptables: this world models the INPUT chain of the filter table; %s is not writable here", chain)
			return 1
		}
		rules = append(rules, rule)
	case "-D", "--delete":
		idx := -1
		for i, r := range rules {
			if rule.Proto == r.Proto && rule.Port == r.Port && rule.Target == r.Target {
				idx = i
				break
			}
		}
		if idx < 0 {
			s.errf("iptables: Bad rule (does a matching rule exist in that chain?)")
			return 1
		}
		rules = append(rules[:idx], rules[idx+1:]...)
	case "-F", "--flush":
		rules = nil
	default:
		s.errf("iptables v1.8.10: unknown option %q", verb)
		return 1
	}
	s.Dev.FS.Write(path, core.RenderIPTables(currentPolicy(s, path, v6), rules), 0644, "root", "root")
	s.Dev.Logf("info", "firewall", "%s ruleset changed by %s: %s %s", tool, s.User.Name, verb, strings.Join(args[1:], " "))
	s.W.AddEvent(s.Dev.ID, "info", "firewall", "%s changed the firewall on %s", s.User.Name, s.Dev.Hostname)
	return 0
}

// parseIPTableArgs reads the subset of the real syntax a player types.
func parseIPTableArgs(args []string) (chain string, r core.Rule, ok bool) {
	if len(args) == 0 {
		return "", r, false
	}
	chain = args[0]
	r = core.Rule{Target: "ACCEPT", Src: "wan"}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-p", "--protocol":
			if i+1 < len(args) {
				r.Proto = strings.ToLower(args[i+1])
				i++
			}
		case "--dport", "--destination-port":
			if i+1 < len(args) {
				r.Port = atoiSafe(args[i+1])
				i++
			}
		case "-j", "--jump":
			if i+1 < len(args) {
				r.Target = strings.ToUpper(args[i+1])
				i++
			}
		case "-i", "--in-interface":
			if i+1 < len(args) {
				if !strings.HasPrefix(args[i+1], "lo") {
					r.Src = "wan"
				}
				i++
			}
		case "-s", "--source":
			i++
			r.Src = "lan" // scoped to an address, not a general WAN accept
		}
	}
	return chain, r, true
}

// readHostRules is the ruleset the file currently describes — every rule, not
// just the WAN ones: rewriting the file must not silently drop the LAN accepts
// the distribution shipped with.
func readHostRules(s *Shell, v6 bool) []core.Rule {
	st := s.Dev.FW()
	if v6 {
		return append([]core.Rule{}, st.Rules6...)
	}
	return append([]core.Rule{}, st.Rules...)
}

func currentPolicy(s *Shell, path string, v6 bool) string {
	if _, ok := s.Dev.FS.Read(path); !ok {
		return defaultInputPolicy(s)
	}
	st := s.Dev.FW()
	if v6 {
		return orD(st.HostInput6, "ACCEPT")
	}
	return orD(st.HostInput, "ACCEPT")
}

// iptablesPath mirrors core's per-distribution location, per family.
func iptablesPath(s *Shell, v6 bool) string {
	distro := strings.ToLower(s.Dev.OS.Distro)
	switch {
	case v6 && (distro == "fedora" || distro == "rhel" || distro == "centos"):
		return "/etc/sysconfig/ip6tables"
	case v6:
		return "/etc/iptables/rules.v6"
	case distro == "fedora" || distro == "rhel" || distro == "centos":
		return "/etc/sysconfig/iptables"
	}
	return "/etc/iptables/rules.v4"
}

func defaultInputPolicy(s *Shell) string {
	if s.Dev.PermitsWAN(0) || s.Dev.Profile == "vps" || s.Dev.Profile == "infra" {
		return "ACCEPT"
	}
	return "DROP"
}

func orD(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// uciRefName is the config name from a `config.section.option` reference.
func uciRefName(ref string) string {
	if i := strings.Index(ref, "."); i > 0 {
		return ref[:i]
	}
	return ref
}

// ---- upnpc ----

// cmdUpnpc is the client half of miniupnpd: a LAN program asking the router to
// forward a port. It is the §14 "UPnP" surface in its honest form — nothing is
// exposed until something asks, and the asking is logged on both devices.
func cmdUpnpc(s *Shell, args []string) int {
	router := s.W.RouterFor(s.Dev)
	if router == nil || router == s.Dev {
		s.errf("upnpc: no UPnP-enabled gateway on this network")
		return 1
	}
	if len(args) == 0 {
		s.errf("usage: upnpc -l | upnpc -a <internal ip> <internal port> <external port> <proto> [desc] | upnpc -d <external port> <proto>")
		return 1
	}
	switch args[0] {
	case "-l", "--list":
		st := router.FW()
		if !st.UPnPEnabled {
			fmt.Fprintf(s.Out, "upnpc: UPnP is disabled on %s\n", router.Hostname)
			return 1
		}
		fmt.Fprintf(s.Out, "upnpc: gateway %s (%s) — IGD v2, UPnP enabled\n", router.Hostname, router.FirstLANIP())
		if len(st.UPnPLeases) == 0 {
			fmt.Fprintln(s.Out, "no active mappings")
			return 0
		}
		fmt.Fprintf(s.Out, "%-6s %-10s %-16s %-10s %s\n", "proto", "external", "internal", "port", "description")
		for _, l := range st.UPnPLeases {
			fmt.Fprintf(s.Out, "%-6s %-10d %-16s %-10d %s\n", strings.ToUpper(l.Proto), l.EPort, l.IP, l.IPort, l.Desc)
		}
		return 0
	case "-a", "--add":
		if len(args) < 5 {
			s.errf("usage: upnpc -a <internal ip> <internal port> <external port> <proto> [desc]")
			return 1
		}
		desc := "libupnp"
		if len(args) > 5 {
			desc = strings.Join(args[5:], " ")
		}
		lease := core.UPnPLease{
			Proto: strings.ToLower(args[4]), EPort: atoiSafe(args[3]), IP: args[1],
			IPort: atoiSafe(args[2]), Desc: desc,
		}
		if err := s.W.UPnPMap(router, s.Dev, lease); err != nil {
			s.errf("upnpc: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "upnpc: external %d %s is now mapped to %s:%d for %d seconds\n",
			lease.EPort, strings.ToUpper(lease.Proto), lease.IP, lease.IPort, 3600)
		return 0
	case "-d", "--delete":
		if len(args) < 3 {
			s.errf("usage: upnpc -d <external port> <proto>")
			return 1
		}
		if !s.W.UPnPUnmap(router, atoiSafe(args[1]), strings.ToLower(args[2]), s.Dev) {
			s.errf("upnpc: no such mapping")
			return 1
		}
		fmt.Fprintf(s.Out, "upnpc: mapping for external port %s removed\n", args[1])
		return 0
	}
	s.errf("upnpc: unknown option %q", args[0])
	return 1
}
