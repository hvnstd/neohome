package shell

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"job", cmdJob}, {"jobs", cmdJob}, {"bank", cmdBank}, {"irc", cmdIRC},
		{"mail", cmdMail}, {"assist", cmdAssist}, {"scan", cmdScan},
		{"exploit", cmdExploit}, {"mount", cmdMount}, {"umount", cmdUmount},
		{"recon", cmdRecon}, {"trace", cmdTrace}, {"evidence", cmdEvidence},
		{"vps", cmdVps}, {"help", cmdHelp}, {"motd", cmdMotd},
		{"nmap", cmdScan}, {"hydra", cmdExploit},
		{"balance", cmdBank}, {"wallet", cmdBank},
	} {
		builtinTable[e.name] = e.fn
	}
}

const money = 100 // cents per unit

func fmtMoney(cents int64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// ---- job board ----

func cmdJob(s *Shell, args []string) int {
	if len(args) == 0 || args[0] == "list" {
		fmt.Fprintf(s.Out, "%-8s %-42s %-10s %-9s %s\n", "ID", "TITLE", "CLIENT", "PAY", "STATUS")
		for _, j := range s.W.Jobs.List {
			status := "open"
			if j.Done {
				status = "paid"
			} else if j.Accepted != "" {
				status = "taken by " + j.Accepted
			}
			fmt.Fprintf(s.Out, "%-8s %-42s %-10s %-9s %s\n", j.ID, j.Title, j.Client, fmtMoney(j.Pay), status)
		}
		return 0
	}
	sub := args[0]
	switch sub {
	case "show", "info":
		if len(args) < 2 {
			s.errf("usage: job show ID")
			return 1
		}
		j := s.W.Job(args[1])
		if j == nil {
			s.errf("no such job: %s", args[1])
			return 1
		}
		fmt.Fprintf(s.Out, "%s — %s\n", j.ID, j.Title)
		fmt.Fprintf(s.Out, "client:   %s\n", j.Client)
		fmt.Fprintf(s.Out, "pay:      %s\n", fmtMoney(j.Pay))
		fmt.Fprintf(s.Out, "tier:     %d\n", j.Tier)
		fmt.Fprintf(s.Out, "hint:     %s\n", j.Help)
		if j.Verify != "" {
			fmt.Fprintf(s.Out, "verifier: world-state check (%s)\n", j.Verify)
		}
		return 0
	case "accept", "take":
		if len(args) < 2 {
			s.errf("usage: job accept ID")
			return 1
		}
		if err := s.W.AcceptJob(s.User.Name, args[1]); err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "accepted %s. fix the actual problem, then run: job pay %s\n", args[1], args[1])
		return 0
	case "pay", "claim", "done":
		if len(args) < 2 {
			s.errf("usage: job pay ID")
			return 1
		}
		paid, why, err := s.W.PayJob(s.User.Name, args[1])
		if err != nil {
			fmt.Fprintf(s.Out, "cannot settle %s: %v\n", args[1], err)
			if why != "" {
				fmt.Fprintf(s.Out, "  world state says: %s\n", why)
			}
			return 1
		}
		fmt.Fprintf(s.Out, "verified: %s\n", why)
		fmt.Fprintf(s.Out, "paid %s to your account\n", fmtMoney(paid))
		return 0
	case "delegate", "assist":
		if len(args) < 2 {
			s.errf("usage: job delegate ID")
			return 1
		}
		if err := s.W.TaskAssistant(args[1]); err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "queued %s for the assistant — it works on its own node\n", args[1])
		return 0
	}
	s.errf("usage: job [list|show ID|accept ID|pay ID|delegate ID]")
	return 1
}

// ---- bank ----

func cmdBank(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "balance", "bal":
		main, assist := s.W.WalletBalance(s.User.Name)
		fmt.Fprintf(s.Out, "household wallet — %s\n", s.User.Name)
		fmt.Fprintf(s.Out, "  main account      %s\n", fmtMoney(main))
		if s.User.Name != "assistant" {
			fmt.Fprintf(s.Out, "  assistant budget  %s\n", fmtMoney(assist))
			fmt.Fprintf(s.Out, "  total             %s\n", fmtMoney(main+assist))
		}
		if s.W.UtilitiesSuspended {
			fmt.Fprintf(s.Out, "  ⚠ utilities suspended — the ISP cut the uplink\n")
		}
		return 0
	case "history", "log":
		acc := s.W.Bank.Accts[s.User.Name]
		if acc == nil {
			fmt.Fprintln(s.Out, "no transactions")
			return 0
		}
		fmt.Fprintf(s.Out, "%-20s %10s %-24s %10s\n", "TIME", "AMOUNT", "MEMO", "BALANCE")
		start := len(acc.Tx) - 20
		if start < 0 {
			start = 0
		}
		for _, t := range acc.Tx[start:] {
			fmt.Fprintf(s.Out, "%-20s %10s %-24s %10s\n",
				t.At.Format("2006-01-02 15:04"), fmtMoney(t.Amount), t.Memo, fmtMoney(t.Balance))
		}
		return 0
	case "pay":
		paid, err := s.W.PayUtilities(s.User.Name)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "settled utilities: %s\n", fmtMoney(paid))
		return 0
	}
	s.errf("usage: bank [balance|history|pay]")
	return 1
}

// ---- IRC ----

func cmdIRC(s *Shell, args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(s.Out, "channels: ")
		var chs []string
		for c := range s.W.Chat.Channels {
			chs = append(chs, c)
		}
		sort.Strings(chs)
		fmt.Fprintln(s.Out, strings.Join(chs, "  "))
		fmt.Fprintln(s.Out, "online:   "+strings.Join(s.W.IRCNicks(), "  "))
		fmt.Fprintln(s.Out, "\nusage: irc read [#chan] | irc say <message> [#chan]")
		return 0
	}
	switch args[0] {
	case "read", "log", "tail":
		ch := "#local"
		if len(args) > 1 {
			ch = args[1]
		}
		for _, m := range s.W.Chat.History {
			if m.Chan != ch {
				continue
			}
			fmt.Fprintf(s.Out, "[%s] <%s> %s\n", m.At.Format("15:04"), m.Nick, m.Text)
		}
		return 0
	case "say", "msg":
		if len(args) < 2 {
			s.errf("usage: irc say MESSAGE [#chan]")
			return 1
		}
		ch := "#local"
		body := args[1:]
		if len(body) > 1 && strings.HasPrefix(body[len(body)-1], "#") {
			ch = body[len(body)-1]
			body = body[:len(body)-1]
		}
		s.W.IRCSend(s.User.Name, ch, strings.Join(body, " "))
		for _, m := range s.W.Chat.History[len(s.W.Chat.History)-3:] {
			fmt.Fprintf(s.Out, "[%s] <%s> %s\n", m.At.Format("15:04"), m.Nick, m.Text)
		}
		return 0
	}
	s.errf("usage: irc [read|say]")
	return 1
}

// ---- mail ----

func cmdMail(s *Shell, args []string) int {
	if len(args) == 0 {
		body, exists, permitted := s.W.ReadInbox(s.Dev, s.User.Name, s.User)
		if !exists {
			fmt.Fprintln(s.Out, "no mail")
			return 0
		}
		if !permitted {
			fmt.Fprintln(s.Out, "permission denied")
			return 1
		}
		fmt.Fprint(s.Out, body)
		return 0
	}
	switch args[0] {
	case "send":
		if len(args) < 3 {
			s.errf("usage: mail send TO SUBJECT  (body on following lines until '.')")
			return 1
		}
		to := args[1]
		subject := strings.Join(args[2:], " ")
		fmt.Fprintln(s.Out, "enter body, end with a single '.':")
		var lines []string
		for {
			l, err := s.bufrd.ReadString('\n')
			if err != nil {
				break
			}
			l = strings.TrimRight(l, "\r\n")
			if l == "." {
				break
			}
			lines = append(lines, l)
		}
		if err := s.W.SendMail(s.User.Name, to, subject, strings.Join(lines, "\n")); err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "message sent to %s\n", to)
		return 0
	case "log":
		for _, l := range s.W.MailLog {
			fmt.Fprintln(s.Out, l)
		}
		return 0
	}
	s.errf("usage: mail [send TO SUBJECT|log]")
	return 1
}

// ---- assistant ----

func cmdAssist(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status":
		a := s.W.AssistantNodeFor(s.W.Players[s.User.Name])
		fmt.Fprintf(s.Out, "assistant — skill level %d\n", s.W.AssistantSkill())
		if a != nil {
			fmt.Fprintf(s.Out, "node:     %s (%s, %d MiB RAM, %s uptime)\n", a.Hostname, a.ID, a.HW.RAMMB, a.Uptime().Round(1e9))
			fmt.Fprintf(s.Out, "load:     %d processes, %d MiB used\n", len(a.Procs), a.MemUsed())
			if data, ok := a.FS.Read("/home/assistant/tasks.md"); ok {
				fmt.Fprintf(s.Out, "notes:\n%s", string(data))
			}
		}
		if len(s.W.AssistantTracksLearned()) > 0 {
			fmt.Fprintf(s.Out, "tracks:   %s\n", strings.Join(s.W.AssistantTracksLearned(), ", "))
		} else {
			fmt.Fprintf(s.Out, "tracks:   none — it can only follow explicit instructions\n")
		}
		return 0
	case "guide":
		return cmdAssistGuide(s)
	case "tasks", "log":
		a := s.W.AssistantNodeFor(s.W.Players[s.User.Name])
		if a != nil {
			if data, ok := a.FS.Read("/home/assistant/jobs.log"); ok {
				fmt.Fprint(s.Out, string(data))
			}
			if data, ok := a.FS.Read("/var/log/syslog"); ok {
				fmt.Fprint(s.Out, tailLines(string(data), 12))
			}
		}
		return 0
	case "train":
		if len(args) < 2 {
			fmt.Fprintln(s.Out, "available tracks:")
			for _, t := range core.AssistantTracks() {
				known := ""
				if s.W.HasTrack(t.Name) {
					known = "  [learned]"
				}
				fmt.Fprintf(s.Out, "  %-18s %-9s %s%s\n", t.Name, fmtMoney(t.Cost), t.Desc, known)
			}
			fmt.Fprintln(s.Out, "\nusage: assist train TRACK")
			return 0
		}
		desc, err := s.W.TrainAssistant(s.User.Name, args[1])
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "assistant learned %s: %s\n", args[1], desc)
		return 0
	}
	s.errf("usage: assist [status|guide|tasks|train TRACK]")
	return 1
}

func cmdAssistGuide(s *Shell) int {
	pc := s.Dev
	if player := s.W.Players[s.User.Name]; player != nil {
		if playerPC := s.W.Devices[player.PC]; playerPC != nil {
			pc = playerPC
		}
	}
	if pc == nil {
		s.errf("cannot inspect the current device")
		return 1
	}

	host := "mirror.neohome.example"
	ip, resolved, detail := pc.DNSAnswer(host)
	if !resolved {
		fmt.Fprintf(s.Out, "I checked %s from %s: %s.\n", host, pc.Hostname, detail)
		router := s.W.RouterForPlayer(s.User.Name)
		if router != nil {
			fmt.Fprintf(s.Out, "Compare `ping %s` with `dig %s` to separate connectivity from name resolution.\n", router.FirstLANIP(), host)
			fmt.Fprintf(s.Out, "Then inspect the router: `ssh root@%s` and `cat /etc/dnsmasq.conf`.\n", router.FirstLANIP())
		} else {
			fmt.Fprintf(s.Out, "Check `ip route` and `dig %s` to separate connectivity from name resolution.\n", host)
		}
		return 0
	}

	fmt.Fprintf(s.Out, "I checked %s from %s: it resolves to %s (%s).\n", host, pc.Hostname, ip, detail)
	for _, job := range s.W.Jobs.List {
		if job.Verify != "dns-fix" {
			continue
		}
		if job.Done {
			fmt.Fprintf(s.Out, "%s is already completed. Use `job list` to choose the next task.\n", job.ID)
			return 0
		}
		verified, reason := s.W.VerifyJob(job)
		if !verified {
			fmt.Fprintf(s.Out, "Name resolution works, but %s is not verified yet: %s. Inspect the router service and configuration, then retry `job show %s`.\n", job.ID, reason, job.ID)
			return 0
		}
		if job.Accepted == "" {
			fmt.Fprintf(s.Out, "The DNS repair is verified. Review and accept it with `job show %s` and `job accept %s`, then claim payment with `job pay %s`.\n", job.ID, job.ID, job.ID)
			return 0
		}
		if job.Accepted == s.User.Name {
			fmt.Fprintf(s.Out, "The DNS repair is verified and %s is yours. Claim payment with `job pay %s`.\n", job.ID, job.ID)
			return 0
		}
		fmt.Fprintf(s.Out, "The DNS repair is verified, but %s is assigned to %s. Use `job list` to find available work.\n", job.ID, job.Accepted)
		return 0
	}
	fmt.Fprintln(s.Out, "Name resolution is working. Use `job list` to find available work.")
	return 0
}

// ---- recon ----

func cmdScan(s *Shell, args []string) int {
	target := "10.77.1.0/24"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}
	ports := []int{22, 21, 23, 53, 80, 443, 2049, 6667, 8080}
	fmt.Fprintf(s.Out, "starting nmap (world-aware scan of %s)\n", target)

	// A real scanner takes an address or a CIDR, not just a hostname. Match
	// candidate devices by any of their interface addresses; Dial() below then
	// decides honest reachability (routing, NAT, firewall) for each port.
	var targets []*core.Device
	if ip, ok, _ := core.DNSAnswer(s.Dev, target); ok {
		if id, found := s.W.IPMap[ip]; found {
			targets = append(targets, s.W.Devices[id])
		}
	}
	if len(targets) == 0 {
		for _, id := range s.W.Order {
			d := s.W.Devices[id]
			if d.ID == s.Dev.ID {
				continue
			}
			for _, a := range core.DeviceAddrs(d) {
				if core.AddrInTarget(a, target) {
					targets = append(targets, d)
					break
				}
			}
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(s.Out, "Nmap done: 0 hosts up — nothing in that range is reachable from here.")
		fmt.Fprintln(s.Out, "  (check your route first: `ip route`, `ping <gateway>`)")
		return 1
	}

	foundAny := false
	for _, d := range targets {
		open := []string{}
			for _, p := range ports {
				svc, dst, msg := core.Dial(s.Dev, d.FirstLANIP(), p)
				if svc != nil && msg == "connected" {
					open = append(open, fmt.Sprintf("%d/tcp open  %s  %s", p, svc.Name, svc.Banner))
					// scanning is an observable act — it leaves evidence on the
					// target, in the syslog and in the world's event stream
					// (which is what the front-door camera records)
					d.Logf("notice", "scan", "port scan from %s (%s)", s.User.Name, s.Dev.SourceIPFor(dst))
					s.W.AddEvent(d.ID, "notice", "scan", "port scan from %s (%s)", s.User.Name, s.Dev.SourceIPFor(dst))
				}
			}
		if len(open) > 0 {
			foundAny = true
			fmt.Fprintf(s.Out, "\nNmap scan report for %s (%s)\n", d.Hostname, d.FirstLANIP())
			fmt.Fprintf(s.Out, "Host is up (0.00%ds latency).\n", 1)
			for _, o := range open {
				fmt.Fprintf(s.Out, "  %s\n", o)
			}
		}
	}
	if !foundAny {
		fmt.Fprintln(s.Out, "Nmap done: hosts up, but no open ports reachable from your position.")
		return 1
	}
	// scanning raises real heat
	s.W.Record("net", s.User.Name, s.Dev.SourceIPFor(s.Dev), targets[0].ID, "port scan", 1)
	fmt.Fprintf(s.Out, "\nNmap done: %d host(s) scanned.\n", len(targets))
	fmt.Fprintln(s.Out, "note: every scan you run is logged on the target. `scan` is not free.")
	return 0
}

func inSameNet(ip, net string) bool {
	parts := strings.Split(net, "/")
	if len(parts) != 2 {
		return false
	}
	return strings.HasPrefix(ip, strings.TrimSuffix(parts[0], ".0")+".")
}

func cmdExploit(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: exploit <target> <vuln-id>   (list them with: recon <target>)")
		return 1
	}
	host := args[0]
	vulnID := args[1]
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("cannot resolve %s: %s", host, how)
		return 1
	}
	dstID, found := s.W.IPMap[ip]
	if !found {
		s.errf("no route to %s (%s)", host, ip)
		return 1
	}
	dst := s.W.Devices[dstID]

	var chosen *core.Vuln
	for _, v := range core.Vulns() {
		if v.ID == vulnID {
			vv := v
			chosen = &vv
		}
	}
	if chosen == nil {
		s.errf("unknown vuln id: %s", vulnID)
		return 1
	}

	// the precondition is checked against real device state, not a flag
	var svc *core.Service
	for _, p := range portsOf(dst) {
		if p == chosen.Port {
			svc = dst.Svc(svcNameFor(dst, chosen.Port))
		}
	}
	if chosen.Port == 21 {
		svc = dst.Svc("vsftpd")
	}
	if chosen.Port == 22 {
		if svc = dst.Svc("dropbear"); svc == nil {
			svc = dst.Svc("sshd")
		}
	}
	if !chosen.Detect(dst, svc) {
		fmt.Fprintf(s.Out, "exploit %s against %s: precondition failed\n", vulnID, host)
		fmt.Fprintf(s.Out, "  the world does not have the condition this exploit needs.\n")
		fmt.Fprintf(s.Out, "  hint: %s\n", chosen.Help)
		return 1
	}

	// it worked — apply the DECLARED EFFECT to world state, honestly
	fmt.Fprintf(s.Out, "[*] %s\n", chosen.Name)
	fmt.Fprintf(s.Out, "[*] target %s (%s) satisfies the precondition\n", dst.Hostname, ip)
	effect := s.applyEffect(dst, chosen)
	for _, l := range effect {
		fmt.Fprintf(s.Out, " * %s\n", l)
	}
	// success still leaves evidence, proportional to how noisy the vuln is
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(dst), dst.ID,
		"exploit "+chosen.ID+" succeeded", chosen.Detection)
	fmt.Fprintf(s.Out, "[!] this was logged on the target (detection weight %d)\n", chosen.Detection)
	return 0
}

func portsOf(d *core.Device) []int {
	var out []int
	for _, s := range d.Services {
		out = append(out, s.Port)
	}
	return out
}

func svcNameFor(d *core.Device, port int) string {
	for n, s := range d.Services {
		if s.Port == port {
			return n
		}
	}
	return ""
}

// applyEffect turns the vuln's declared outcome into real state changes.
func (s *Shell) applyEffect(dst *core.Device, v *core.Vuln) []string {
	var out []string
	switch {
	case strings.HasPrefix(v.Effect, "ftp-access"):
		// anonymous write lands a real file in /srv/ftp/pub
		dst.FS.MkdirAll("/srv/ftp/pub", 0777, "nobody", "nogroup")
		dst.FS.Write("/srv/ftp/pub/.probe", "uploaded "+s.W.Sim.Format("15:04:05")+"\n", 0644, "nobody", "nogroup")
		out = append(out, "anonymous write accepted: /srv/ftp/pub/.probe created")
		out = append(out, "now readable: `ftp` the tree, or from here: cat the paths below")
		// list what an attacker would actually now see
		if data, ok := dst.FS.Read("/home/devops/deploy/notes.md"); ok {
			s.stolen = append(s.stolen, "devops/notes.md")
			out = append(out, "readable: /home/devops/deploy/notes.md")
			_ = data
		}
	case strings.HasPrefix(v.Effect, "cred-leak"):
		user := strings.TrimPrefix(v.Effect, "cred-leak:")
		u := dst.FindUser(user)
		if u != nil {
			s.creds = append(s.creds, user+"@"+dst.Hostname+":"+u.Pass)
			out = append(out, fmt.Sprintf("credential recovered: %s:%s", user, u.Pass))
		}
	case strings.HasPrefix(v.Effect, "root-shell"):
		s.creds = append(s.creds, "root@"+dst.Hostname+":"+dst.FindUser("root").Pass)
		out = append(out, "root credentials: "+dst.FindUser("root").Pass)
		out = append(out, "you can now: ssh root@"+dst.Hostname)
	}
	return out
}

func cmdRecon(s *Shell, args []string) int {
	host := ""
	if len(args) > 0 {
		host = args[0]
	}
	var target *core.Device
	if host != "" {
		ip, ok, how := core.DNSAnswer(s.Dev, host)
		if !ok {
			s.errf("cannot resolve %s: %s", host, how)
			return 1
		}
		if id, found := s.W.IPMap[ip]; found {
			target = s.W.Devices[id]
		}
	}
	if target == nil {
		fmt.Fprintln(s.Out, "usage: recon <host>    (start with your own LAN: recon gateway)")
		return 1
	}

	fmt.Fprintf(s.Out, "recon on %s (%s)\n", target.Hostname, target.FirstLANIP())
	fmt.Fprintf(s.Out, "  profile:  %s\n", target.Profile)
	fmt.Fprintf(s.Out, "  os:       %s %s (%s, %s)\n", target.OS.Distro, target.OS.Ver, target.OS.Kernel, target.OS.Arch)
	if target.FirstWANIP() != "" {
		fmt.Fprintf(s.Out, "  wan ip:   %s\n", target.FirstWANIP())
	}
	fmt.Fprintf(s.Out, "  uptime:   %s\n", target.Uptime().Round(1e9))

	// services actually reachable from where the player stands right now
	fmt.Fprintf(s.Out, "\nreachable services from your position:\n")
	any := false
	for _, svc := range target.Services {
		if svc.State != "running" {
			continue
		}
		_, _, msg := core.Dial(s.Dev, target.FirstLANIP(), svc.Port)
		if msg == "connected" {
			any = true
			fmt.Fprintf(s.Out, "  %-8s %d/tcp  %-12s %s\n", svc.Name, svc.Port, svc.State, svc.Banner)
		}
	}
	if !any {
		fmt.Fprintln(s.Out, "  (nothing reachable — check local firewall/scope rules)")
	}

	fmt.Fprintf(s.Out, "\nknown vulnerabilities matching this host:\n")
	anyV := false
	for _, v := range core.Vulns() {
		var svc *core.Service
		if v.Port == 21 {
			svc = target.Svc("vsftpd")
		} else if v.Port == 22 {
			if svc = target.Svc("dropbear"); svc == nil {
				svc = target.Svc("sshd")
			}
		}
		if v.Detect(target, svc) {
			anyV = true
			fmt.Fprintf(s.Out, "  [%s] %s\n      %s\n      hint: %s\n", v.ID, v.Name, v.Desc, v.Help)
		}
	}
	if !anyV {
		fmt.Fprintln(s.Out, "  none — this host is not vulnerable to anything you know about")
	}
	return 0
}

func cmdTrace(s *Shell, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(s.Out, "usage: trace <ip>      (find an ip in /var/log/syslog, then trace it)")
		return 1
	}
	fmt.Fprintln(s.Out, s.W.TraceOrigin(args[0]))
	return 0
}

func cmdEvidence(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, s.W.HeatSummary())
	fmt.Fprintln(s.Out)
	limit := 15
	for _, e := range s.W.EvidenceFor("", limit) {
		fmt.Fprintf(s.Out, "  %s %-5s %-9s %-16s %s\n",
			e.At.Format("15:04:05"), e.Kind, e.Actor, e.Origin, e.Detail)
	}
	return 0
}

// ---- mounts (NFS/SMB really work) ----

func cmdMount(s *Shell, args []string) int {
	if len(args) == 0 {
		if len(s.Dev.Mounts) == 0 {
			fmt.Fprintln(s.Out, "(no mounts)")
			return 0
		}
		for _, m := range s.Dev.Mounts {
			fmt.Fprintf(s.Out, "%s on %s type %s (rw)\n", m.Src, m.Dst, m.FSTy)
		}
		return 0
	}
	fsTy := ""
	src := ""
	dst := ""
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-t":
			if i+1 >= len(args) {
				s.errf("usage: mount -t nfs nas:/srv/data /mnt/data | mount -t cifs //nas/data /mnt/data [-o user=NAME]")
				return 1
			}
			i++
			fsTy = args[i]
		case "-o":
			if i+1 >= len(args) {
				s.errf("usage: mount -o user=NAME ...")
				return 1
			}
			i++
			for _, kv := range strings.Split(args[i], ",") {
				if j := strings.Index(kv, "="); j >= 0 {
					opts[kv[:j]] = kv[j+1:]
				}
			}
		default:
			if src == "" {
				src = args[i]
			} else if dst == "" {
				dst = args[i]
			}
		}
	}
	if src == "" || dst == "" {
		s.errf("usage: mount -t nfs nas:/srv/data /mnt/data | mount -t cifs //nas/data /mnt/data [-o user=NAME]")
		return 1
	}
	// //host/share is cifs by construction, the way mount.cifs is picked
	if strings.HasPrefix(src, "//") {
		fsTy = "cifs"
	}
	if fsTy == "" {
		fsTy = "nfs"
	}
	if fsTy == "cifs" || fsTy == "smb" {
		return mountSMB(s, src, dst, opts)
	}

	ip, ok, how := core.DNSAnswer(s.Dev, strings.SplitN(src, ":", 2)[0])
	if !ok {
		s.errf("cannot resolve %s: %s", strings.SplitN(src, ":", 2)[0], how)
		return 1
	}
	id, found := s.W.IPMap[ip]
	if !found {
		s.errf("no route to %s", strings.SplitN(src, ":", 2)[0])
		return 1
	}
	host, path := strings.SplitN(src, ":", 2)[0], strings.SplitN(src, ":", 2)[1]
	svc, _, msg := core.Dial(s.Dev, ip, 2049)
	if svc == nil || msg != "connected" {
		s.errf("mount: %s:%s: %s", host, path, msg)
		fmt.Fprintf(s.Out, "  is nfsd running on %s? try: ssh %s then systemctl status nfsd\n", host, host)
		return 1
	}
	s.Dev.Mounts = append(s.Dev.Mounts, core.Mount{Src: id + ":" + path, Dst: s.abs(dst), FSTy: fsTy})
	fmt.Fprintf(s.Out, "mounted %s:%s on %s (type %s)\n", host, path, s.abs(dst), fsTy)
	return 0
}

func cmdUmount(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: umount PATH")
		return 1
	}
	p := s.abs(args[0])
	var keep []core.Mount
	removed := false
	for _, m := range s.Dev.Mounts {
		if m.Dst == p {
			removed = true
			continue
		}
		keep = append(keep, m)
	}
	if !removed {
		s.errf("umount: %s: not mounted", args[0])
		return 1
	}
	s.Dev.Mounts = keep
	return 0
}

// ---- VPS ----

func cmdVps(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		fmt.Fprintf(s.Out, "novapanel — plans\n")
		for _, p := range s.W.Prov.Plans {
			fmt.Fprintf(s.Out, "  %-10s %d vCPU  %5d MiB  %6d MiB  %-9s  %s/mo\n",
				p.Name, p.Cores, p.RAM, p.Disk, p.Region, fmtMoney(p.Monthly))
		}
		fmt.Fprintln(s.Out, "\nusage: vps create <plan> [hostname]")
		return 0
	case "create", "buy", "new":
		if len(args) < 2 {
			s.errf("usage: vps create <plan> [hostname]")
			return 1
		}
		hostname := ""
		if len(args) > 2 {
			hostname = args[2]
		}
		d, creds, err := s.W.ProvisionVPS(s.User.Name, args[1], hostname)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "provisioning %s...\n", d.Hostname)
		fmt.Fprintf(s.Out, "public ip: %s\n", d.FirstWANIP())
		fmt.Fprintf(s.Out, "dns:       %s.neohome.example\n", d.Hostname)
		fmt.Fprintf(s.Out, "%s\n", creds)
		fmt.Fprintf(s.Out, "\nssh in: ssh deploy@%s\n", d.Hostname)
		fmt.Fprintln(s.Out, "the node is now addressable by everything else in the world.")
		return 0
	case "list-mine":
		found := false
		for _, id := range s.W.Order {
			d := s.W.Devices[id]
			if d.Profile == "vps" && d.Owner == s.User.Name {
				found = true
				fmt.Fprintf(s.Out, "%-16s %-16s %d vCPU %d MiB\n", d.Hostname, d.FirstWANIP(), d.HW.Cores, d.HW.RAMMB)
			}
		}
		if !found {
			fmt.Fprintln(s.Out, "you have no VPS")
		}
		return 0
	}
	s.errf("usage: vps [list|create PLAN [hostname]|list-mine]")
	return 1
}

// ---- help ----

func cmdHelp(s *Shell, args []string) int {
	fmt.Fprint(s.Out, `NeoHome — a world that keeps running whether you are watching or not.

Start here:                assist guide (state-aware help)   job list
Work on the machine:      ls cd cat cp mv rm mkdir touch echo find grep head tail
                          sort uniq wc du df chmod chown stat file which ln
Processes:                ps top htop kill pkill nice
Network:                  ip ifconfig route ss ping traceroute dig nslookup curl wget openssl
Services & packages:      systemctl service apt apk pacman dnf
Remote:                   ssh scp sftp telnet        Sessions: tmux screen
System info:              fastfetch uname hostname uptime whoami id env free lscpu lsblk dmesg

The world layer:
  job [list|show ID|accept ID|pay ID|delegate ID]   job board — pays only against real world state
  bank [balance|history|pay]                        household wallet + assistant budget
  irc [read|say]                                    #local and #help are inhabited by real NPCs
  bbs [boards|read <board> [N]|post <board> <sub>]  bbs.neohome.example — the community board answers
  git clone|status|log|commit|pull|push             real repositories on git.neohome.example, https with push auth
  camera [list|view N]                              front-door clips, recorded from real household events
  lock [status|lock|unlock [PIN]|batteries]         the front door: owner app or PIN; wrong codes are evidence
  sms [list|read N|send WHO TEXT]                   on your phone — cellular, survives a dead router
  phone [status|charge]                             battery and the charger dock
  mail [send TO SUBJECT|log]                        mail actually lands in mailboxes
  mutt [-f mailbox [N]]                             open any mailbox: local mbox or imap://user@host/INBOX
  assist [status|guide|tasks|train TRACK]            the assistant works its own node
  mount -t nfs host:/path /mnt/x                    NFS/SMB really resolve to a device
  mount -t cifs //host/share /mnt/x [-o user=U]     SMB: real smb.conf shares, guest or authenticated
  smbclient -L host                                 what the server really shares
  vps [list|create PLAN [hostname]]                 buy a real node; it joins the internet

  recon <host>      what is actually reachable + what is actually vulnerable
  scan <host|net>   port scan (logged on the target — it is not free)
  exploit <host> <vuln-id>   applies the vuln's declared effect to world state
  evidence          the evidence graph behind your heat
  trace <ip>        walks the evidence back to a provider, not a person
`)
	return 0
}

func cmdMotd(s *Shell, args []string) int {
	if data, ok := s.Dev.FS.Read("/etc/motd"); ok {
		fmt.Fprint(s.Out, string(data))
	}
	return 0
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

var _ = strconv.Atoi
