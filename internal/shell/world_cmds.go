package shell

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"job", cmdJob}, {"jobs", cmdJob}, {"bank", cmdBank},
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
			if j.Cancelled {
				status = "cancelled"
			}
			if !j.Done && len(j.Stages) > 0 {
				status += fmt.Sprintf(" (stage %d/%d)", j.StageIdx+1, len(j.Stages))
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
		if len(j.Requires) > 0 {
			fmt.Fprintf(s.Out, "requires: %s\n", strings.Join(j.Requires, ", "))
		}
		if j.Verify != "" {
			fmt.Fprintf(s.Out, "verifier: world-state check (%s)\n", j.Verify)
		}
		for i, st := range j.Stages {
			mark := " "
			if j.Done || i < j.StageIdx {
				mark = "x"
			} else if i == j.StageIdx && j.Accepted != "" {
				mark = ">"
			}
			fmt.Fprintf(s.Out, "stage %d [%s] %s — %s (%s)\n", i+1, mark, st.Name, st.Help, fmtMoney(st.Pay))
		}
		if len(j.Solutions) > 0 {
			fmt.Fprintf(s.Out, "solutions:\n")
			for _, sol := range j.Solutions {
				fmt.Fprintf(s.Out, "  - %s\n", sol)
			}
		}
		if j.Expected != "" {
			fmt.Fprintf(s.Out, "expected: %s\n", j.Expected)
		}
		if j.Evidence != "" {
			fmt.Fprintf(s.Out, "evidence: %s\n", j.Evidence)
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
	case "advance", "next", "stage":
		if len(args) < 2 {
			s.errf("usage: job advance ID")
			return 1
		}
		paid, why, err := s.W.AdvanceJob(s.User.Name, args[1])
		if err != nil {
			fmt.Fprintf(s.Out, "cannot advance %s: %v\n", args[1], err)
			if why != "" {
				fmt.Fprintf(s.Out, "  world state says: %s\n", why)
			}
			return 1
		}
		fmt.Fprintf(s.Out, "advanced: %s\n", why)
		fmt.Fprintf(s.Out, "paid %s to your account\n", fmtMoney(paid))
		return 0
	}
	s.errf("usage: job [list|show ID|accept ID|pay ID|advance ID|delegate ID]")
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
	case "transfer", "send":
		// roommates settle debts and crews get funded: money between people
		// moves here (the market moves it for trades).
		if len(args) < 3 {
			s.errf("usage: bank transfer TO $AMT")
			return 1
		}
		amt, err := marketDollars(args[2])
		if err != nil {
			s.errf("bank: bad amount %q (whole dollars)", args[2])
			return 1
		}
		if s.W.Bank.Accts[args[1]] == nil {
			s.errf("bank: no account for %s", args[1])
			return 1
		}
		if err := s.W.Transfer(s.User.Name, args[1], amt, "transfer to "+args[1]); err != nil {
			s.errf("bank: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "transferred %s to %s\n", fmtMoney(amt), args[1])
		return 0
	}
	s.errf("usage: bank [balance|history|pay|transfer TO $AMT]")
	return 1
}

// ---- IRC ----

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
		// the mailbox is text; Fprint on a []byte would print a byte array
		fmt.Fprint(s.Out, string(body))
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
		if err := s.W.SendMailFrom(s.Dev, s.User.Name, to, subject, strings.Join(lines, "\n")); err != nil {
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
	target := core.LANSubnet + "0/24"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
	}
	ports := []int{22, 21, 23, 53, 80, 443, 2049, 6667, 8080}
	fmt.Fprintf(s.Out, "starting nmap (world-aware scan of %s)\n", target)

	// A real scanner takes an address or a CIDR, not just a hostname. Match
	// candidate devices by any of their interface addresses; Dial() below then
	// decides honest reachability (routing, NAT, firewall) for each port.
	// The address a host is scanned on is the address that matched the target:
	// scanning a public range must probe the public address, not the host's LAN
	// address, or a forwarded service behind a home router could never be seen.
	var targets []*core.Device
	addrOf := map[string]string{}
	if ip, ok, _ := core.DNSAnswer(s.Dev, target); ok {
		if id, found := s.W.IPMap[ip]; found {
			targets = append(targets, s.W.Devices[id])
			addrOf[id] = ip
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
					addrOf[id] = a
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
		addr := addrOf[d.ID]
		if addr == "" {
			addr = d.FirstLANIP()
		}
		open := []string{}
		for _, p := range ports {
			svc, dst, msg := core.Dial(s.Dev, addr, p)
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
			fmt.Fprintf(s.Out, "\nNmap scan report for %s (%s)\n", d.Hostname, addr)
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

	// A forwarded port belongs to the machine behind the router, not to the
	// router: exploit the address the player reached, against the host that
	// really answers it. Exploiting the router would "succeed" against a
	// service the router merely forwards.
	if inner := core.ForwardTarget(s.Dev, ip, chosen.Port); inner != nil {
		dst = inner
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
	effect := s.applyEffect(dst, ip, chosen)
	for _, l := range effect {
		fmt.Fprintf(s.Out, " * %s\n", l)
	}
	// §33: the attempt is reported to the target, so a `suricata` running
	// there raises its exploit rule on real evidence rather than a label.
	dst.NoteExploit(s.Dev.SourceIPFor(dst), s.Dev.Hostname, chosen.ID)
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

// applyEffect turns the vuln's declared outcome into real state changes — over
// the wire, with the world's own protocol code, so an effect can only succeed
// where the real service would really have accepted it. The FTP effects used to
// write files straight into the target's VFS and read credentials out of the
// account records; both now perform an anonymous session through Dial() and
// read the credential out of the file the vuln names. Nothing here is a
// shortcut around a service, a permission or a network gate.
func (s *Shell) applyEffect(dst *core.Device, ip string, v *core.Vuln) []string {
	var out []string
	switch {
	case strings.HasPrefix(v.Effect, "ftp-access"):
		drop := strings.TrimPrefix(v.Effect, "ftp-access:")
		sess, err := s.ftpEffectSession(dst, ip)
		if err != nil {
			out = append(out, "anonymous access failed: "+err.Error())
			break
		}
		payload := "uploaded " + s.W.Sim.Format("15:04:05") + " from " + s.Dev.Hostname + "\n"
		if err := sess.Stor(drop, []byte(payload)); err != nil {
			sess.Close()
			out = append(out, "anonymous upload refused: "+err.Error())
			break
		}
		out = append(out, "anonymous write accepted: "+drop+" created on "+dst.Hostname)
		out = append(out, "the drop directory is world-writable; the file is real and the daemon logged it")
		if entries, err := sess.List(path.Dir(drop)); err == nil {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name)
			}
			out = append(out, "visible there now: "+strings.Join(names, " "))
		}
		sess.Close()
	case strings.HasPrefix(v.Effect, "cred-leak"):
		user := strings.TrimPrefix(v.Effect, "cred-leak:")
		sess, err := s.ftpEffectSession(dst, ip)
		if err != nil {
			out = append(out, "anonymous access failed: "+err.Error())
			break
		}
		file := v.File
		data, err := sess.Retr(file)
		sess.Close()
		if err != nil {
			out = append(out, "read of "+file+" refused: "+err.Error())
			break
		}
		local := "/tmp/" + path.Base(file)
		if err := s.Dev.WriteGuest(local, data, s.User); err != nil {
			out = append(out, "could not keep the copy locally: "+err.Error())
			break
		}
		s.stolen = append(s.stolen, file)
		out = append(out, "fetched "+file+" ("+fmt.Sprintf("%dB", len(data))+") -> "+local)
		out = append(out, strings.TrimRight(string(data), "\n"))
		// the credential is read out of the retrieved file, not out of the
		// account record: if the file does not hold it, the exploit fails
		pw := credentialFor(string(data), user)
		if pw == "" {
			out = append(out, "no credential for "+user+" in that file")
			break
		}
		// and it must really work: a real authenticated login proves it
		auth, err := s.W.FTPLogin(s.Dev, s.User.Name, dst, user, pw)
		if err != nil {
			out = append(out, "credential for "+user+" did not authenticate: "+err.Error())
			break
		}
		auth.Close()
		s.creds = append(s.creds, user+"@"+dst.Hostname+":"+pw)
		out = append(out, "credential recovered and verified: "+user+":"+pw)
		out = append(out, "use it yourself: ftp "+dst.Hostname+" then `user "+user+"`")
	case strings.HasPrefix(v.Effect, "root-shell"):
		s.creds = append(s.creds, "root@"+dst.Hostname+":"+dst.FindUser("root").Pass)
		out = append(out, "root credentials: "+dst.FindUser("root").Pass)
		out = append(out, "you can now: ssh root@"+dst.Hostname)
	}
	return out
}

// ftpEffectSession opens an anonymous FTP session for an exploit effect: DNS
// was already resolved by the caller, and Dial() still has to let the
// connection through (power, routing, the router's port-forward, the firewall
// and the daemon's service state), so an exploit cannot succeed against a host
// the player cannot actually reach.
func (s *Shell) ftpEffectSession(dst *core.Device, ip string) (*core.FTPSession, error) {
	svc, port := core.FTPDaemon(dst)
	if svc == nil || svc.State != "running" {
		return nil, fmt.Errorf("no FTP daemon is running on %s", dst.Hostname)
	}
	got, _, msg := core.Dial(s.Dev, ip, port)
	if got == nil || got.Name != svc.Name {
		return nil, fmt.Errorf("cannot reach %s:%d (%s)", dst.Hostname, port, msg)
	}
	return s.W.FTPLogin(s.Dev, s.User.Name, dst, "anonymous", "anonymous@")
}

// credentialFor reads "user:password" out of a leaked export, ignoring
// comments — the same thing a player does with their eyes.
func credentialFor(export, user string) string {
	for _, line := range strings.Split(export, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if u, pw, ok := strings.Cut(line, ":"); ok && u == user && pw != "" {
			return pw
		}
	}
	return ""
}

func cmdRecon(s *Shell, args []string) int {
	host := ""
	if len(args) > 0 {
		host = args[0]
	}
	var target *core.Device
	ip := ""
	if host != "" {
		var ok bool
		var how string
		ip, ok, how = core.DNSAnswer(s.Dev, host)
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

	fmt.Fprintf(s.Out, "recon on %s (%s)\n", target.Hostname, ip)
	fmt.Fprintf(s.Out, "  profile:  %s\n", target.Profile)
	fmt.Fprintf(s.Out, "  os:       %s %s (%s, %s)\n", target.OS.Distro, target.OS.Ver, target.OS.Kernel, target.OS.Arch)
	if target.FirstWANIP() != "" {
		fmt.Fprintf(s.Out, "  wan ip:   %s\n", target.FirstWANIP())
	}
	fmt.Fprintf(s.Out, "  uptime:   %s\n", target.Uptime().Round(1e9))

	// The address the player reached is the one probed: on a public address that
	// is the WAN side, on a LAN address the LAN side.
	// services actually reachable from where the player stands right now
	fmt.Fprintf(s.Out, "\nreachable services from your position:\n")
	any := false
	for _, svc := range target.Services {
		if svc.State != "running" {
			continue
		}
		_, _, msg := core.Dial(s.Dev, ip, svc.Port)
		if msg == "connected" {
			any = true
			fmt.Fprintf(s.Out, "  %-8s %d/tcp  %-12s %s\n", svc.Name, svc.Port, svc.State, svc.Banner)
		}
	}
	if !any {
		fmt.Fprintln(s.Out, "  (nothing reachable — check local firewall/scope rules)")
	}

	// A home router's port-forwards are owner-opened holes: recon has to name
	// the machine each one lands on, because that machine — not the router — is
	// what the player can actually attack. The list comes from the router's
	// own configuration and its UPnP leases, so a mapping a program opened an
	// hour ago shows up here the way it shows up in the router's log.
	var behind []*core.Device
	for _, f := range target.FW().AllRedirects() {
		if !f.Enabled {
			continue
		}
		landing := f.WPort
		label := fmt.Sprintf("%d/tcp", f.WPort)
		if f.DMZ {
			landing = 1 // the DMZ answers any port; probe one to find out
			label = "all ports (DMZ)"
		}
		svc, inner, msg := core.Dial(s.Dev, ip, landing)
		if svc == nil || msg != "connected" {
			fmt.Fprintf(s.Out, "  %s -> %s (unreachable now: %s)\n", label, f.DstIP, msg)
			continue
		}
		behind = append(behind, inner)
		how := "forwarded to"
		if f.UPnP {
			how = "opened by UPnP ->"
		}
		fmt.Fprintf(s.Out, "  %s -> %s %s (%s): %s running\n",
			label, how, inner.Hostname, inner.FirstLANIP(), svc.Name)
	}
	// and the router's own management, if its config publishes it
	if target.Profile == "router" {
		for _, port := range []int{22, 23, 80, 443} {
			if target.PermitsWAN(port) {
				fmt.Fprintf(s.Out, "  %d/tcp -> the router's own management is exposed to the internet\n", port)
			}
		}
	}

	// vulnerabilities, checked against the host that really answers the port
	check := []*core.Device{target}
	check = append(check, behind...)
	fmt.Fprintf(s.Out, "\nknown vulnerabilities matching this host:\n")
	anyV := false
	svcFor := func(d *core.Device, v core.Vuln) *core.Service {
		switch v.Port {
		case 21:
			return d.Svc("vsftpd")
		case 22:
			if svc := d.Svc("dropbear"); svc != nil {
				return svc
			}
			return d.Svc("sshd")
		}
		return nil
	}
	for _, d := range check {
		for _, v := range core.Vulns() {
			if !v.Detect(d, svcFor(d, v)) {
				continue
			}
			anyV = true
			where := d.Hostname
			if d.ID == target.ID {
				where = "here"
			}
			fmt.Fprintf(s.Out, "  [%s] %s (%s)\n      %s\n      hint: %s\n", v.ID, v.Name, where, v.Desc, v.Help)
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
	// a vfat source is a locally plugged usb stick, via its device node
	if fsTy == "vfat" || strings.HasPrefix(src, "/dev/") {
		if !strings.HasPrefix(src, "/dev/") {
			s.errf("usage: mount -t vfat /dev/sda1 /mnt/usb")
			return 1
		}
		stick, err := s.W.USBStickFromNode(s.Dev, src)
		if err != nil {
			s.errf("mount: %v", err)
			return 1
		}
		s.Dev.Mounts = append(s.Dev.Mounts, core.Mount{Src: stick.ID + ":/", Dst: s.abs(dst), FSTy: "vfat"})
		fmt.Fprintf(s.Out, "mounted %s on %s (type vfat)\n", src, s.abs(dst))
		return 0
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
		fmt.Fprintf(s.Out, "  %-12s %-6s %-7s %-13s %s\n", "PLAN", "vCPU", "RAM", "IP", "PRICE")
		for _, p := range s.W.Prov.Plans {
			ip := "public IPv4"
			switch p.IPMode {
			case "shared":
				ip = "shared (CGNAT)"
			case "v6only":
				ip = "IPv6 only"
			}
			if p.V6 {
				ip += " + v6"
			}
			fmt.Fprintf(s.Out, "  %-12s %-6d %-7s %-13s %s/mo  (%s)\n",
				p.Name, p.Cores, fmt.Sprintf("%d MiB", p.RAM), ip, fmtMoney(p.Monthly), p.Region)
		}
		fmt.Fprintln(s.Out, "\nimages: debian alpine ubuntu fedora arch")
		var regions []string
		for _, r := range s.W.NodeRegions() {
			regions = append(regions, r.Name)
		}
		fmt.Fprintf(s.Out, "regions: %s\n", strings.Join(regions, ", "))
		fmt.Fprintln(s.Out, "note: a shared-address plan has no inbound IPv4 — host on IPv6 or buy a public plan")
		fmt.Fprintln(s.Out, "usage: vps create <plan> [hostname] [image] [--region R]")
		fmt.Fprintln(s.Out, "manage a node: vps show|start|stop|reboot|reinstall|console|resize|disk|snapshot|snapshots|restore|rdns <hostname>")
		return 0
	case "create", "buy", "new":
		region := ""
		rest := []string{}
		for i := 1; i < len(args); i++ {
			if (args[i] == "--region" || args[i] == "-r") && i+1 < len(args) {
				region = args[i+1]
				i++
				continue
			}
			if strings.HasPrefix(args[i], "--region=") {
				region = strings.TrimPrefix(args[i], "--region=")
				continue
			}
			rest = append(rest, args[i])
		}
		if len(rest) < 1 {
			s.errf("usage: vps create <plan> [hostname] [image] [--region NAME]")
			return 1
		}
		hostname := ""
		if len(rest) > 1 {
			hostname = rest[1]
		}
		image := "debian"
		if len(rest) > 2 {
			image = rest[2]
		}
		d, creds, err := s.W.ProvisionVPSInRegion(s.User.Name, rest[0], hostname, image, region)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "provisioning %s...\n", d.Hostname)
		fmt.Fprintf(s.Out, "image:     %s %s\n", d.OS.Distro, d.OS.Ver)
		if rec := s.W.NodeOf(d); rec != nil {
			country := "??"
			if r, err := s.W.RegionByName(rec.Region); err == nil {
				country = r.Country
			}
			fmt.Fprintf(s.Out, "region:    %s (%s, %s, AS%d)\n", rec.Region, rec.Datacenter, country, regionASN(s, rec.Region))
		}
		if d.NATed {
			fmt.Fprintf(s.Out, "ipv4:      %s (shared — carrier-grade NAT, no inbound)\n", core.WANIPOf(d))
		} else if d.FirstWANIP() != "" {
			fmt.Fprintf(s.Out, "public ip: %s\n", d.FirstWANIP())
		} else {
			fmt.Fprintf(s.Out, "ipv4:      none (IPv6-only plan)\n")
		}
		if v6 := d.FirstWANv6(); v6 != "" {
			fmt.Fprintf(s.Out, "public ip6: %s\n", v6)
		}
		fmt.Fprintf(s.Out, "dns:       %s.neohome.example\n", d.Hostname)
		fmt.Fprintf(s.Out, "%s\n", creds)
		fmt.Fprintf(s.Out, "\nssh in: ssh deploy@%s\n", d.Hostname)
		fmt.Fprintf(s.Out, "packages: %s\n", provisionPkgHint(d))
		fmt.Fprintln(s.Out, "the node is now addressable by everything else in the world.")
		return 0
	case "list-mine":
		found := false
		for _, id := range s.W.Order {
			d := s.W.Devices[id]
			if d.Profile == "vps" && d.Owner == s.User.Name {
				found = true
				addr := d.FirstWANIP()
				switch {
				case d.NATed:
					addr = core.WANIPOf(d) + " (shared)"
				case addr == "":
					addr = d.FirstWANv6()
					if addr != "" {
						addr += " (v6)"
					}
				}
				fmt.Fprintf(s.Out, "%-16s %-22s %-14s %d vCPU %d MiB\n",
					d.Hostname, addr, d.OS.Distro+" "+d.OS.Ver, d.HW.Cores, d.HW.RAMMB)
				if vips := d.VirtualIPs(); len(vips) > 0 {
					fmt.Fprintf(s.Out, "%-16s virtual: %s\n", "", strings.Join(vips, ", "))
				}
			}
		}
		if !found {
			fmt.Fprintln(s.Out, "you have no VPS")
		}
		return 0
	case "ip", "address", "floating":
		// §13's "virtual IP": a reserved address the provider lends to one of
		// the customer's nodes. Real providers call these floating/reserved
		// addresses, and the whole point is that they can move between nodes
		// while the name in DNS keeps resolving to them.
		if len(args) < 3 {
			s.errf("usage: vps ip add <hostname> [address] | vps ip show <hostname> | vps ip del <hostname> <address>")
			return 1
		}
		verb, host := args[1], args[2]
		var dev *core.Device
		for _, id := range s.W.Order {
			if d := s.W.Devices[id]; d.Hostname == host && d.Profile == "vps" && d.Owner == s.User.Name {
				dev = d
			}
		}
		if dev == nil {
			s.errf("vps: no node %q on your account", host)
			return 1
		}
		switch verb {
		case "add", "attach", "reserve":
			ip := ""
			if len(args) > 3 {
				ip = args[3]
			}
			if ip == "" {
				// out of the provider's own block: a reserved address is the
				// operator's to lend, which is what `whois` will say about it
				ip = s.W.AllocPublicFor("vps")
			}
			if err := s.W.AttachVirtual(dev, ip); err != nil {
				s.errf("vps: %v", err)
				return 1
			}
			fmt.Fprintf(s.Out, "reserved %s -> %s\n", ip, dev.Hostname)
			fmt.Fprintf(s.Out, "point a DNS record at it and the name survives moving the service to another node\n")
			return 0
		case "show", "list":
			vips := dev.VirtualIPs()
			if len(vips) == 0 {
				fmt.Fprintf(s.Out, "%s holds no reserved addresses (its own address is %s)\n", dev.Hostname, core.WANIPOf(dev))
				return 0
			}
			for _, v := range vips {
				fmt.Fprintf(s.Out, "%-16s reserved for %s\n", v, dev.Hostname)
			}
			return 0
		case "del", "detach", "release":
			if len(args) < 4 {
				s.errf("usage: vps ip del <hostname> <address>")
				return 1
			}
			if err := s.W.DetachVirtual(dev, args[3]); err != nil {
				s.errf("vps: %v", err)
				return 1
			}
			fmt.Fprintf(s.Out, "released %s from %s\n", args[3], dev.Hostname)
			return 0
		}
		s.errf("usage: vps ip add <hostname> [address] | vps ip show <hostname> | vps ip del <hostname> <address>")
		return 1
	case "regions", "region":
		fmt.Fprintf(s.Out, "novapanel — regions\n")
		fmt.Fprintf(s.Out, "  %-14s %-11s %-8s %-9s %s\n", "REGION", "DATACENTER", "COUNTRY", "NETWORK", "BLOCK")
		for _, r := range s.W.NodeRegions() {
			fmt.Fprintf(s.Out, "  %-14s %-11s %-8s %-9s %s0/24\n",
				r.Name, r.Datacenter, r.Country, fmt.Sprintf("AS%d", r.ASN), r.Block)
		}
		return 0
	case "show", "info", "status":
		if len(args) < 2 {
			s.errf("usage: vps show <hostname>")
			return 1
		}
		d, rec, err := s.W.NodeFor(s.User.Name, args[1])
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		dc := rec.Datacenter
		if r, err := s.W.RegionByName(rec.Region); err == nil {
			dc = fmt.Sprintf("%s (%s, %s, AS%d)", r.Datacenter, r.Name, r.Country, r.ASN)
		}
		fmt.Fprintf(s.Out, "%s\n", d.Hostname)
		fmt.Fprintf(s.Out, "  plan:      %s (%d vCPU, %d MiB RAM, %d MiB disk)\n", rec.Plan, d.HW.Cores, d.HW.RAMMB, d.HW.DiskMB)
		fmt.Fprintf(s.Out, "  image:     %s %s\n", d.OS.Distro, d.OS.Ver)
		fmt.Fprintf(s.Out, "  region:    %s\n", dc)
		fmt.Fprintf(s.Out, "  state:     %s\n", core.VPSState(d))
		fmt.Fprintf(s.Out, "  ipv4:      %s\n", orNone(core.WANIPOf(d)))
		fmt.Fprintf(s.Out, "  ipv6:      %s\n", orNone(d.FirstWANv6()))
		if vips := d.VirtualIPs(); len(vips) > 0 {
			fmt.Fprintf(s.Out, "  reserved:  %s\n", strings.Join(vips, ", "))
		}
		fmt.Fprintf(s.Out, "  rDNS:      %s\n", orNone(rec.RDNS))
		fmt.Fprintf(s.Out, "  price:     %s/mo\n", fmtMoney(rec.Monthly))
		fmt.Fprintf(s.Out, "  rebuilds:  %d\n", rec.Rebuilds)
		if len(rec.Snapshots) > 0 {
			var snaps []string
			for _, sn := range rec.Snapshots {
				snaps = append(snaps, sn.Name)
			}
			fmt.Fprintf(s.Out, "  snapshots: %s\n", strings.Join(snaps, ", "))
		}
		fmt.Fprintf(s.Out, "  console:   vps console %s\n", d.Hostname)
		return 0
	case "start", "boot", "poweron":
		return vpsOne(s, args, func(host string) error {
			d, err := s.W.VPSStart(s.User.Name, host)
			if err != nil {
				return err
			}
			fmt.Fprintf(s.Out, "%s powered on (%d services running)\n", d.Hostname, runningServiceCount(d))
			return nil
		})
	case "stop", "poweroff", "shutdown":
		return vpsOne(s, args, func(host string) error {
			d, err := s.W.VPSStop(s.User.Name, host)
			if err != nil {
				return err
			}
			fmt.Fprintf(s.Out, "%s powered off — nothing answers at %s now\n", d.Hostname, firstAddr(d))
			return nil
		})
	case "reboot", "restart":
		return vpsOne(s, args, func(host string) error {
			d, err := s.W.VPSReboot(s.User.Name, host)
			if err != nil {
				return err
			}
			fmt.Fprintf(s.Out, "%s rebooted (uptime %s)\n", d.Hostname, d.Uptime().Round(time.Second))
			return nil
		})
	case "reinstall", "rebuild":
		if len(args) < 2 {
			s.errf("usage: vps reinstall <hostname> [image]")
			return 1
		}
		image := "debian"
		if len(args) > 2 {
			image = args[2]
		}
		_, creds, err := s.W.ReinstallVPS(s.User.Name, args[1], image)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "%s reinstalled — the old filesystem is gone\n", args[1])
		fmt.Fprintf(s.Out, "%s\n", creds)
		fmt.Fprintf(s.Out, "start it with: vps start %s\n", args[1])
		return 0
	case "console":
		if len(args) < 2 {
			s.errf("usage: vps console <hostname>")
			return 1
		}
		d, _, err := s.W.NodeFor(s.User.Name, args[1])
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		if !d.NetUp {
			s.errf("%s is powered off (start it first)", d.Hostname)
			return 1
		}
		u := d.FindUser("deploy")
		if u == nil {
			u = d.FindUser("root")
		}
		if u == nil {
			s.errf("no user to log in as on %s", d.Hostname)
			return 1
		}
		return enterConsole(s, d, u, d.Hostname)
	case "resize", "grow":
		if len(args) < 2 {
			s.errf("usage: vps resize <hostname> [--cpu N] [--mem MB] [--disk MB]")
			return 1
		}
		d, rec, err := s.W.NodeFor(s.User.Name, args[1])
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		cores, mem, disk := float64(d.HW.Cores), d.HW.RAMMB, d.HW.DiskMB
		for i := 2; i < len(args); i++ {
			num := func() (int, bool) {
				if i+1 < len(args) {
					if n, err := strconv.Atoi(args[i+1]); err == nil {
						i++
						return n, true
					}
				}
				return 0, false
			}
			switch {
			case strings.HasPrefix(args[i], "--cpu"):
				if n, ok := num(); ok {
					cores = float64(n)
				}
			case strings.HasPrefix(args[i], "--mem"):
				if n, ok := num(); ok {
					mem = n
				}
			case strings.HasPrefix(args[i], "--disk"):
				if n, ok := num(); ok {
					disk = n
				}
			}
		}
		price, delta, err := s.W.VPSResize(s.User.Name, args[1], cores, mem, disk)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		_ = rec
		fmt.Fprintf(s.Out, "%s resized: %g vCPU, %d MiB RAM, %d MiB disk — now %s/mo\n",
			d.Hostname, cores, mem, disk, fmtMoney(price))
		if delta > 0 {
			fmt.Fprintf(s.Out, "charged %s for the upgrade\n", fmtMoney(delta))
		} else if delta < 0 {
			fmt.Fprintf(s.Out, "a downgrade is not refunded (%s/mo from now on)\n", fmtMoney(price))
		}
		return 0
	case "disk":
		if len(args) < 3 {
			s.errf("usage: vps disk <hostname> <GiB>")
			return 1
		}
		gib, err := strconv.Atoi(args[2])
		if err != nil {
			s.errf("vps: disk size must be a number of GiB")
			return 1
		}
		monthly, err := s.W.VPSAddDisk(s.User.Name, args[1], gib)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "+%d GiB attached to %s — %s/mo now\n", gib, args[1], fmtMoney(monthly))
		return 0
	case "snapshot", "snap":
		if len(args) < 2 {
			s.errf("usage: vps snapshot <hostname> [name]")
			return 1
		}
		name := ""
		if len(args) > 2 {
			name = args[2]
		}
		snap, err := s.W.VPSnapshot(s.User.Name, args[1], name)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "snapshot %s taken from %s (%s)\n", snap.Name, args[1], snap.At.Format("2006-01-02 15:04"))
		return 0
	case "snapshots", "snaps":
		if len(args) < 2 {
			s.errf("usage: vps snapshots <hostname>")
			return 1
		}
		_, rec, err := s.W.NodeFor(s.User.Name, args[1])
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		if len(rec.Snapshots) == 0 {
			fmt.Fprintf(s.Out, "%s has no snapshots\n", args[1])
			return 0
		}
		for _, sn := range rec.Snapshots {
			fmt.Fprintf(s.Out, "%-16s %s\n", sn.Name, sn.At.Format("2006-01-02 15:04"))
		}
		return 0
	case "restore", "rollback":
		if len(args) < 3 {
			s.errf("usage: vps restore <hostname> <snapshot>")
			return 1
		}
		if err := s.W.VPSRestore(s.User.Name, args[1], args[2]); err != nil {
			s.errf("%v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "%s restored from %s (start it to boot the restored system)\n", args[1], args[2])
		return 0
	case "rdns", "ptr":
		if len(args) < 2 {
			s.errf("usage: vps rdns <hostname> [name]")
			return 1
		}
		ptr := ""
		if len(args) > 2 {
			ptr = args[2]
		}
		if err := s.W.VPSSetRDNS(s.User.Name, args[1], ptr); err != nil {
			s.errf("%v", err)
			return 1
		}
		d, rec, _ := s.W.NodeFor(s.User.Name, args[1])
		fmt.Fprintf(s.Out, "%s reverse DNS: %s\n", d.Hostname, rec.RDNS)
		fmt.Fprintf(s.Out, "check it with: dig -x %s\n", core.WANIPOf(d))
		return 0
	}
	s.errf("usage: vps [list|regions|create PLAN [hostname] [image] [--region R]|list-mine|show|start|stop|reboot|reinstall|console|resize|disk|snapshot|snapshots|restore|rdns|ip add|show|del]")
	return 1
}

// vpsOne runs a one-hostname panel verb.
func vpsOne(s *Shell, args []string, fn func(host string) error) int {
	if len(args) < 2 {
		s.errf("usage: vps %s <hostname>", args[0])
		return 1
	}
	if err := fn(args[1]); err != nil {
		s.errf("vps: %v", err)
		return 1
	}
	return 0
}

// regionASN is the AS behind a region name, for the receipt.
func regionASN(s *Shell, name string) int {
	if r, err := s.W.RegionByName(name); err == nil {
		return r.ASN
	}
	return 0
}

func firstAddr(d *core.Device) string {
	if a := core.WANIPOf(d); a != "" {
		return a
	}
	if a := d.FirstWANv6(); a != "" {
		return a
	}
	return d.FirstLANIP()
}

func orNone(v string) string {
	if v == "" {
		return "(none)"
	}
	return v
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
Remote:                   ssh scp sftp ftp telnet     Sessions: tmux screen
System info:              fastfetch uname hostname uptime whoami id env free lscpu lsblk dmesg
Accounts & disks:         passwd smartctl fsck

The world layer:
  job [list|show ID|accept ID|pay ID|advance ID|delegate ID]   job board — pays only against real world state
  bank [balance|history|pay|transfer TO $AMT]      household wallet + assistant budget + people
  player [list|invite NAME TEMP-PASS]             citizens: vouched roommates with their own machines
  org [list|info|create|invite|join|leave|kick|contribute|withdraw|post|cancel]   crews, treasuries, escrowed contracts
  irc [read|say]                                    #local and #help are inhabited by real NPCs
  bbs [boards|read <board> [N]|post <board> <sub>]  bbs.neohome.example — the community board answers
  market [list|info|sell-cred|sell-file|buy]      bazaar.neohome.example — listings, atomic swaps, a fee, evidence
  git clone|status|log|commit|pull|push             real repositories on git.neohome.example, https with push auth
  camera [list|view N]                              front-door clips, recorded from real household events
  lock [status|lock|unlock [PIN]|batteries]         the front door: owner app or PIN; wrong codes are evidence
  sms [list|read N|send WHO TEXT]                   on your phone — cellular, survives a dead router
  phone [status|charge]                             battery and the charger dock
  usb [list|plug STICK|unplug] + mount -t vfat      the stick and its files travel between machines
  mail [send TO SUBJECT|log]                        mail actually lands in mailboxes
  mutt [-f mailbox [N]]                             open any mailbox: local mbox or imap://user@host/INBOX
  assist [status|guide|tasks|train TRACK]            the assistant works its own node
  mount -t nfs host:/path /mnt/x                    NFS/SMB really resolve to a device
  mount -t cifs //host/share /mnt/x [-o user=U]     SMB: real smb.conf shares, guest or authenticated
  smbclient -L host                                 what the server really shares
  ftp [-A] [user@]host[:port]                        real FTP session (ls/get/put); -A is anonymous
  vps [list|regions|create|show|start|stop|reboot]  rent a real node: choose a region and an
  vps [reinstall|console|resize|disk]               OS, then power it, rebuild it, resize it,
  vps [snapshot|snapshots|restore|rdns|ip]          snapshot it or roll it back

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

// provisionPkgHint tells the buyer which command actually manages packages on
// the image they just booted.
func provisionPkgHint(d *core.Device) string {
	mg := core.ManagerFor(d)
	if mg == "" {
		return "none"
	}
	return mg
}
