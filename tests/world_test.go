package tests

import (
	"strconv"
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// bufOut is a simple in-memory sink for shell output.
type bufOut struct{ b strings.Builder }

func (o *bufOut) Write(p []byte) (int, error) { return o.b.Write(p) }
func (o *bufOut) String() string              { return o.b.String() }

// run builds a shell on a device for a user and executes one command line.
func run(t *testing.T, w *core.World, dev *core.Device, user string, line string) string {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, dev, u, out, "10.77.1.11", "xterm")
	sh.ExecLine(line)
	return out.String()
}

// remoteStatus executes one line and reports its exit status, for the paths
// (ssh command form, cmdSsh) where the status is part of the contract.
func remoteStatus(t *testing.T, w *core.World, dev *core.Device, user string, line string) int {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, dev, u, out, "10.77.1.11", "xterm")
	return sh.ExecLineStatus(line)
}

// runWithStdin runs one command line with the given lines available on the
// session's stdin — passwords and interactive command bodies, the way a
// live session feeds them.
func runWithStdin(t *testing.T, w *core.World, dev *core.Device, user string, line string, stdin ...string) string {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, dev, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader(strings.Join(stdin, "\n") + "\n"))
	sh.ExecLine(line)
	return out.String()
}

// tmux must not panic, and a session must be backed by a real process that
// keeps existing after you detach — otherwise "start it in tmux and log out" is
// a lie and nothing survives the session.
func TestTmuxSessionsAreRealProcesses(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	// this used to panic: Sessions was a nil map on every device
	if out := sh("tmux new work"); !strings.Contains(out, "created session work") {
		t.Fatalf("tmux new should create a session, got:\n%s", out)
	}
	if _, ok := pc.Sessions["work"]; !ok {
		t.Fatal("the session was not recorded on the device")
	}
	sess := pc.Sessions["work"]
	if sess.Proc == nil {
		t.Fatal("a session must be backed by a real process")
	}
	// the process is really in the device's process table
	found := false
	for _, p := range pc.Procs {
		if p.PID == sess.Proc.PID {
			found = true
		}
	}
	if !found {
		t.Fatal("the session's process is not in the process table")
	}

	// this used to nil-deref
	if out := sh("tmux ls"); !strings.Contains(out, "work") {
		t.Fatalf("tmux ls should list the session, got:\n%s", out)
	}

	// work sent to a detached session really runs on the device
	sh("tmux send-keys work touch /home/alex/from-tmux")
	if _, ok := pc.FS.Read("/home/alex/from-tmux"); !ok {
		t.Fatal("a command sent to the session did not actually run on the device")
	}

	// attaching shows what the session produced
	if out := sh("tmux attach work"); !strings.Contains(out, "from-tmux") {
		t.Fatalf("attach should replay the session's output, got:\n%s", out)
	}

	// killing the session kills its process
	pid := sess.Proc.PID
	sh("tmux kill-session work")
	if _, ok := pc.Sessions["work"]; ok {
		t.Fatal("the session should be gone")
	}
	for _, p := range pc.Procs {
		if p.PID == pid {
			t.Fatal("killing the session left its process running")
		}
	}
}

// A session must not outlive its own content silently: `ls` must not claim a
// session is alive after it has been killed.
// tmux spells its session target several ways and a command that ignores -t
// silently addresses a session called "-t", which looks like it worked.
func TestTmuxTargetParsing(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.ExecLine("tmux new -s build -d")
	sh.ExecLine("tmux send -t build echo from-send")
	sh.ExecLine("tmux attach -t build")
	if !strings.Contains(out.String(), "from-send") {
		t.Fatalf("`tmux send -t build ...` did not reach the session:\n%s", out.String())
	}
	// "-tbuild" (no space) is equally valid tmux syntax
	out2 := &bufOut{}
	sh2 := shell.NewShell(w, pc, u, out2, "10.77.1.11", "xterm")
	sh2.ExecLine("tmux send -tbuild echo glued-form")
	if !strings.Contains(out2.String()+strings.Join(pc.Sessions["build"].Lines, "\n"), "glued-form") {
		t.Fatalf("`tmux send -tbuild ...` did not reach the session")
	}
}

func TestTmuxListReportsNoSessionsHonestly(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }
	out := sh("tmux ls")
	if !strings.Contains(out, "no server running") && !strings.Contains(out, "no sessions") {
		if strings.Contains(out, "windows") {
			t.Fatalf("tmux ls invented a session that was never created:\n%s", out)
		}
	}
}

// `ip -4 addr show eth0` is the most common networking command in the world.
// Parsing only the first argument made it an error, which no real player would
// accept.
func TestIPAcceptsFamilyAndDevice(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	for _, cmd := range []string{"ip -4 addr show eth0", "ip addr show eth0", "ip -4 a"} {
		out := sh(cmd)
		if strings.Contains(out, "unknown arg") || strings.Contains(out, "does not exist") {
			t.Fatalf("%q should work, got:\n%s", cmd, out)
		}
		if !strings.Contains(out, "inet ") {
			t.Fatalf("%q should show the interface address, got:\n%s", cmd, out)
		}
	}
	// a device that does not exist is still a real error
	if out := sh("ip addr show eth9"); !strings.Contains(out, "does not exist") {
		t.Fatalf("a missing device should be reported, got:\n%s", out)
	}
}

// `ps aux | grep -c crond` is how every real script counts a daemon. If -c is
// parsed as the pattern, the count silently becomes an error message instead.
func TestGrepOptionsInPipeline(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	pc.StartService("sshd")

	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	if got := sh("ps aux | grep sshd"); !strings.Contains(got, "sshd") {
		t.Fatalf("plain piped grep should find sshd, got:\n%s", got)
	}
	// -c must print a bare count, not treat -c as the pattern
	out := sh("ps aux | grep -c sshd")
	if strings.Contains(out, "No such file") || strings.Contains(out, "invalid option") {
		t.Fatalf("grep -c in a pipeline is broken:\n%s", out)
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		t.Fatalf("grep -c printed nothing:\n%s", out)
	}
	if _, err := strconv.Atoi(fields[len(fields)-1]); err != nil {
		t.Fatalf("grep -c should end with a number, got %q", fields[len(fields)-1])
	}
	// -i must be case-insensitive, -v must invert
	if got := sh("echo HELLO | grep -i hello"); !strings.Contains(got, "HELLO") {
		t.Fatalf("grep -i should match case-insensitively, got:\n%s", got)
	}
	if got := sh("printf 'a\nb\n' | grep -v a"); strings.Contains(got, "\na\n") {
		t.Fatalf("grep -v should drop the matching line, got:\n%s", got)
	}
}

// printf and the quote rules are what a scripted player actually writes with:
// `printf 'a\nb\n' > /etc/...` is how a config file gets edited from a shell.
// A tokenizer that eats the backslash inside single quotes, or a printf that
// prints its format instead of formatting, turns that idiom into silent noise.
func TestPrintfAndQuotingAreReal(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	// single quotes are literal: the backslash survives the tokenizer and
	// printf is the one that turns it into a newline
	if got := sh("printf 'a\\nb\\n'"); got != "a\nb\n" {
		t.Fatalf("printf 'a\\nb\\n' should print two lines, got %q", got)
	}
	// the format repeats until the arguments run out
	if got := sh("printf '%s\\n' one two"); got != "one\ntwo\n" {
		t.Fatalf("printf should reuse its format for each argument, got %q", got)
	}
	// conversions consume their argument and keep width and flags
	if got := sh("printf '%03d-%s' 7 ok"); got != "007-ok" {
		t.Fatalf("printf %%03d should pad, got %q", got)
	}
	// %b reads the escapes from the data, echo -e does the same for a whole run
	if got := sh("printf '%b' 'x\\ty\\n'"); got != "x\ty\n" {
		t.Fatalf("printf %%b should interpret the argument's escapes, got %q", got)
	}
	if got := sh("echo -e 'p\\tq'"); got != "p\tq\n" {
		t.Fatalf("echo -e should interpret escapes, got %q", got)
	}
	// without -e the backslash is data, which is the whole reason -e exists
	if got := sh("echo 'p\\tq'"); got != "p\\tq\n" {
		t.Fatalf("plain echo must not interpret escapes, got %q", got)
	}
	// and the writable form: a real file with real lines
	if got := sh("printf 'watch = /etc\\ninterval = 1m\\n' > /home/alex/probe.conf"); strings.Contains(got, "error") {
		t.Fatalf("writing a file with printf failed: %s", got)
	}
	if got := sh("cat /home/alex/probe.conf"); got != "watch = /etc\ninterval = 1m\n" {
		t.Fatalf("the file printf wrote is not what it printed, got %q", got)
	}
}

// ---- the fault chain, end to end ----

func TestFaultChainDNS(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if pc == nil {
		t.Fatal("no household pc")
	}

	// 1. the fault is real: resolution fails
	if !w.FaultDNSActive() {
		t.Fatal("expected the planted DNS fault to be active at world start")
	}
	out := run(t, w, pc, "alex", "dig mirror.neohome.example")
	if !strings.Contains(out, "SERVFAIL") {
		t.Fatalf("expected SERVFAIL from a broken resolver, got:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "UPSTREAM") {
		t.Fatalf("SERVFAIL should explain the causality, got:\n%s", out)
	}

	// 2. IP-direct access still works — the classic symptom split
	out = run(t, w, pc, "alex", "ping 10.0.0.4")
	if strings.Contains(out, "unknown host") {
		t.Fatalf("IP-direct should not need DNS, got:\n%s", out)
	}

	// 3. the player investigates and fixes the ROUTER, not the PC
	router := w.Devices["router-alex"]
	out = run(t, w, pc, "alex", "cat /etc/dnsmasq.conf")
	if !strings.Contains(out, "/etc") {
		t.Fatalf("player should be able to read config over ssh later; local cat works: %s", out)
	}

	// do the repair on the router itself
	if _, ok := router.FS.Read("/etc/dnsmasq.conf"); !ok {
		t.Fatal("router dnsmasq.conf unreadable")
	}
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	fixed := strings.Replace(string(data), "resolv-file=/var/run/dnsmasq/resolv.conf", "resolv-file=/etc/dnsmasq.upstream", 1)
	router.FS.Write("/etc/dnsmasq.conf", fixed, 0644, "root", "root")
	if _, err := router.RestartService("dnsmasq"); err != nil {
		t.Fatalf("restart dnsmasq: %v", err)
	}

	// 4. the world really changed: resolution now succeeds from the PC
	if w.FaultDNSActive() {
		t.Fatal("fault should be cleared once the router has a working upstream")
	}
	out = run(t, w, pc, "alex", "dig mirror.neohome.example")
	if strings.Contains(out, "SERVFAIL") {
		t.Fatalf("resolution should work after the fix, got:\n%s", out)
	}
	if !strings.Contains(out, "ANSWER SECTION") {
		t.Fatalf("expected an answer section, got:\n%s", out)
	}

	// 5. curl now reaches the mirror through the resolver chain
	out = run(t, w, pc, "alex", "curl http://mirror.neohome.example/")
	if strings.Contains(out, "could not resolve") {
		t.Fatalf("curl should resolve after the fix, got:\n%s", out)
	}
}

func TestJobOnlyPaysAgainstWorldState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// accepting without fixing must fail the verifier
	if err := w.AcceptJob("alex", "J-101"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, _, err := w.PayJob("alex", "J-101"); err == nil {
		t.Fatal("job must NOT pay while the fault is still active")
	}

	// fix it for real
	router := w.Devices["router-alex"]
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	router.FS.Write("/etc/dnsmasq.conf",
		strings.Replace(string(data), "/var/run/dnsmasq/resolv.conf", "/etc/dnsmasq.upstream", 1), 0644, "root", "root")
	router.RestartService("dnsmasq")

	before := w.Bank.Accts["alex"].Balance
	paid, why, err := w.PayJob("alex", "J-101")
	if err != nil {
		t.Fatalf("pay after fix: %v (state said: %s)", err, why)
	}
	if paid <= 0 {
		t.Fatal("expected positive payout")
	}
	after := w.Bank.Accts["alex"].Balance
	if after != before+paid {
		t.Fatalf("money did not actually move: %d -> %d (paid %d)", before, after, paid)
	}

	// paying twice must be refused
	if _, _, err := w.PayJob("alex", "J-101"); err == nil {
		t.Fatal("double payout must be refused")
	}
	_ = pc
}

// ---- shell behaviour is grounded in state ----

func TestShellReadsRealDiskState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "alex", "ls /etc")
	if strings.Contains(out, "command not found") {
		t.Fatalf("ls must exist: %s", out)
	}
	if !strings.Contains(out, "hostname") {
		t.Fatalf("expected /etc contents, got:\n%s", out)
	}

	// unknown commands must fail honestly
	out = run(t, w, pc, "alex", "definitelynotacommand")
	if !strings.Contains(out, "command not found") {
		t.Fatalf("unknown command should say so, got:\n%s", out)
	}
}

// Power, DHCP leases and the host key are all world state and must survive a
// save/load cycle. If any of them silently reset, the world is not persistent —
// it would just look persistent until the first restart.
func TestPersistenceKeepsPowerLeaseAndKey(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// give the house power back first, then lease and cut, so the saved world has
	// both a real outage and a real lease in it
	l, err := pc.DHCPRenew()
	if err != nil {
		t.Fatalf("lease before save: %v", err)
	}
	nas := w.Devices["nas-alex"]
	nas.UPS = &core.UPSInfo{ChargePct: 42, LastState: "on battery"}
	w.CutPower("alex") // the saved world is mid-outage
	w.SSHHostKey = []byte("test-key-material-not-a-real-key")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatal(err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := w2.Devices["nas-alex"].UPS; got == nil || got.ChargePct != 42 {
		t.Fatalf("UPS charge did not survive the save: %+v", got)
	}
	if len(w2.SSHHostKey) == 0 {
		t.Fatal("the host key did not survive the save — the server would change identity")
	}
	// the lease must still be recorded on the router, and still route
	r2 := w2.Devices["router-alex"]
	if _, ok := r2.DHCPL[pc.Ifaces[0].MAC]; !ok {
		t.Fatalf("the DHCP lease did not survive; router holds %d lease(s)", len(r2.DHCPL))
	}
	if w2.IPMap[l.IP] != "pc-alex" {
		t.Fatalf("the leased address no longer routes to the pc after load: %q", w2.IPMap[l.IP])
	}
	// the outage survived too: a reloaded world is still dark
	if w2.HouseholdPower() {
		t.Fatal("the power cut did not survive the save")
	}
	if w2.Devices["pc-alex"].Powered() {
		t.Fatal("the pc should still be dark after reloading a world saved mid-outage")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")

	path := t.TempDir() + "/world.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := w2.Devices["router-alex"].FS.Read("/etc/dnsmasq.upstream"); !ok {
		t.Fatal("a change made before saving did not survive the reload")
	}
	if w2.Devices["router-alex"].W == nil {
		t.Fatal("back-pointer W was not re-linked after load")
	}
}

// ---- services really gate access ----

func TestServiceStateGatesAccess(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	// NFS is up: reachable from the LAN
	_, _, msg := core.Dial(pc, nas.FirstLANIP(), 2049)
	if msg != "connected" {
		t.Fatalf("nfs should be reachable on the LAN, got: %s", msg)
	}

	// stop it: now the mount must fail honestly
	nas.StopService("nfsd")
	_, _, msg = core.Dial(pc, nas.FirstLANIP(), 2049)
	if msg == "connected" {
		t.Fatal("a stopped service must not accept connections")
	}
	out := run(t, w, pc, "alex", "mount -t nfs nas:/srv/data /mnt/data")
	if !strings.Contains(out, "nfsd") && !strings.Contains(out, "Connection") {
		t.Fatalf("mount should fail with a real transport error, got:\n%s", out)
	}
}

// ---- firewall / NAT causality ----

func TestWANDoesNotSeePrivateLAN(t *testing.T) {
	w := core.NewWorld()
	mirror := w.Devices["mirror"]
	pc := w.Devices["pc-alex"]

	// a public host must not be able to reach a home LAN address
	_, _, msg := core.Dial(mirror, pc.FirstLANIP(), 22)
	if msg == "connected" {
		t.Fatal("RFC1918 address must not be reachable across the public internet")
	}
}

// ---- packages change the world ----

func TestInstallCreatesFilesAndService(t *testing.T) {
	w := core.NewWorld()
	vps, _, err := w.ProvisionVPS("alex", "small-2", "edge1")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if vps.FirstWANIP() == "" {
		t.Fatal("a provisioned VPS must have a public IP")
	}
	// nginx install must create the binary, config and a registered service
	nginx := w.Repos["debian"].Pkgs["nginx"]
	actions := vps.InstallPkg(nginx)
	if _, ok := vps.FS.Get("/usr/sbin/nginx"); !ok {
		t.Fatal("install did not create the virtual binary")
	}
	// and the box now records where the package came from, from its own sources
	if vps.InstalledFrom["nginx"] != "debian" {
		t.Fatalf("provenance should name the repository this box uses, got %q", vps.InstalledFrom["nginx"])
	}
	if vps.Svc("nginx") == nil {
		t.Fatal("install did not register the service")
	}
	if len(actions) == 0 {
		t.Fatal("install should report the world changes it made")
	}
	// and the service is genuinely reachable on its port once started
	if vps.Svc("nginx").State != "running" {
		t.Fatalf("nginx should autostart, state=%s", vps.Svc("nginx").State)
	}
}

// ---- evidence graph, not a fake heat counter ----

// Scanning your OWN lan: proves the scan is a real observable act, and that the
// devices it touches genuinely log it. (Scanning another household's RFC1918
// address from here is correctly impossible — `No route to host`.)
func TestScanLeavesEvidence(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	run(t, w, pc, "alex", "scan 10.77.1.0/24")
	if w.Case == nil || len(w.Case.Events) == 0 {
		t.Fatal("a scan must record evidence")
	}
	if w.Case.Heat <= 0 {
		t.Fatal("scanning should raise heat")
	}
	// the scanned host must have logged it too — that is what forensics reads
	data, ok := nas.FS.Read("/var/log/syslog")
	if !ok || !strings.Contains(strings.ToLower(string(data)), "scan") {
		t.Fatalf("the scanned host should have a log entry, got:\n%s", string(data))
	}
	// and a foreign private range must be honestly unreachable
	out := run(t, w, pc, "alex", "scan 10.88.1.0/24")
	if !strings.Contains(out, "No route") && !strings.Contains(out, "unreachable") &&
		!strings.Contains(out, "0 hosts up") && !strings.Contains(out, "no open ports") {
		t.Fatalf("another household's LAN must not be reachable, got:\n%s", out)
	}
}

// ---- assistant works autonomously and really learns ----

func TestAssistantCompletesDelegatedJob(t *testing.T) {
	w := core.NewWorld()
	before := w.Bank.Accts["alex"].Balance

	if err := w.TaskAssistant("J-101"); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	// advance the world clock past the job duration
	for i := 0; i < 12; i++ {
		w.Tick()
	}
	// the assistant must have fixed the real fault, not faked it
	if w.FaultDNSActive() {
		t.Fatal("assistant claimed the job but the fault is still active")
	}
	if w.Job("J-101") == nil || !w.Job("J-101").Done {
		t.Fatal("job should be marked done after the assistant finishes")
	}
	after := w.Bank.Accts["alex"].Balance
	if after <= before {
		t.Fatalf("assistant work should increase household wealth: %d -> %d", before, after)
	}
	if w.AssistantSkill() == 0 {
		t.Fatal("assistant should have grown from doing the work")
	}
}

// ---- economy has real recurring costs ----

func TestUtilitiesChargeAndSuspend(t *testing.T) {
	w := core.NewWorld()
	acc := w.Bank.Accts["alex"]
	acc.Balance = 1 // nearly broke

	for i := 0; i < 40; i++ {
		w.Tick()
	}
	if acc.Balance >= 1 {
		t.Fatalf("utilities should draw the balance down, got %d", acc.Balance)
	}
	if !w.UtilitiesSuspended {
		t.Fatal("an overdrawn household should have its uplink suspended")
	}
	// paying fixes it for real
	acc.Balance = 5000
	if _, err := w.PayUtilities("alex"); err != nil {
		t.Fatalf("pay utilities: %v", err)
	}
	if w.UtilitiesSuspended {
		t.Fatal("paying arrears should restore service")
	}
}

// ---- NPCs change the world, not just chat ----

func TestNPCHardensAfterHeat(t *testing.T) {
	w := core.NewWorld()
	npc := w.Devices["npc-pc"]
	router := w.Devices["npc-router"]

	if npc.FindUser("devops").Pass != "Summer2024!" {
		t.Fatal("precondition: expected the leaked password")
	}
	if !openForward(router, 21) {
		t.Fatal("precondition: expected an open port-forward")
	}

	// accumulate real heat
	for i := 0; i < 15; i++ {
		w.Record("auth", "alex", "10.77.1.11", npc.ID, "failed login attempt", 1)
	}
	if !w.Case.Notified {
		t.Fatal("the world should react once heat crosses the threshold")
	}
	if npc.FindUser("devops").Pass == "Summer2024!" {
		t.Fatal("NPC should have rotated the compromised credential")
	}
	if openForward(router, 21) {
		t.Fatal("NPC should have closed the exposed port-forward")
	}
}

// ---- IRC is inhabited ----

func TestIRCNPCsRespond(t *testing.T) {
	w := core.NewWorld()
	n := len(w.Chat.History)
	w.IRCSend("alex", "#local", "my dns is broken, any idea?")
	if len(w.Chat.History) <= n {
		t.Fatal("posting to IRC should append to the channel")
	}
	last := w.Chat.History[len(w.Chat.History)-1]
	if last.Nick == "alex" {
		t.Fatal("an NPC should have answered a direct technical question")
	}
	if !strings.Contains(strings.ToLower(last.Text), "dnsmasq") && !strings.Contains(strings.ToLower(last.Text), "resolv") {
		t.Fatalf("the NPC advice should be grounded in the real fault, got: %s", last.Text)
	}
}

// ---- every command must touch world state ----

func TestNoDecorativeCommands(t *testing.T) {
	// commands that only print canned text without reading state are a design
	// violation; this guards the principle mechanically for the core set.
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	for _, cmd := range []string{
		"fastfetch", "uname -a", "hostname", "whoami", "id", "env",
		"ip addr", "ip route", "ss", "df", "free", "lscpu",
		"ps", "uptime", "date", "pwd",
	} {
		out := run(t, w, pc, "alex", cmd)
		if strings.TrimSpace(out) == "" {
			t.Errorf("%q produced no output — it should read real state", cmd)
		}
		if strings.Contains(out, "command not found") {
			t.Errorf("%q is not implemented", cmd)
		}
	}
}

// ---- regression tests for the bugs the live telnet session exposed ----

// Every real telnet client sends CRLF. A shell that treats the pair as two line
// endings runs a phantom empty command after every line.
func TestCRLFIsOneLineEnding(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := &bufOut{}
	sh := shell.NewShell(w, pc, pc.FindUser("alex"), out, "10.77.1.11", "xterm")
	in := "hostname\r\npwd\r\n"
	sh.RunLoop(strings.NewReader(in))
	got := out.String()
	// two prompts, two results, and no third (phantom) command executed
	if n := strings.Count(got, "home-pc"); n < 2 {
		t.Fatalf("expected both commands to run, got:\n%s", got)
	}
	if n := strings.Count(got, "command not found"); n != 0 {
		t.Fatalf("CRLF must not produce phantom commands, got:\n%s", got)
	}
}

// A password prompt must consume exactly the typed line, never the leftover \n.
func TestPasswordPromptEatsOnlyTheLine(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := &bufOut{}
	sh := shell.NewShell(w, pc, pc.FindUser("alex"), out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader("alex123\r\nnext\r\n"))
	got := sh.ReadPasswordLine("password: ")
	if got != "alex123" {
		t.Fatalf("password should be exactly alex123, got %q", got)
	}
	// and the next line must still be readable and intact
	line := sh.ReadLineForTest()
	if line != "next" {
		t.Fatalf("the line after the password should be 'next', got %q", line)
	}
}

// Pipelines must really connect stdout to stdin and report the last exit code.
func TestPipelineConnectsStages(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "echo alpha; echo beta; echo gamma | head -2")
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("pipeline lost earlier output:\n%s", out)
	}
	// tail -1 of a known sequence
	out = run(t, w, pc, "alex", "echo one; echo two; echo three | tail -1")
	if strings.TrimSpace(strings.Split(out, "\n")[len(strings.Split(out, "\n"))-2]) != "three" {
		t.Fatalf("tail -1 should print only 'three', got:\n%s", out)
	}
	// grep exits non-zero when nothing matched: && must short-circuit
	out = run(t, w, pc, "alex", "echo hello | grep zzz && echo SHOULD_NOT_PRINT")
	if strings.Contains(out, "SHOULD_NOT_PRINT") {
		t.Fatalf("grep with no match must fail the pipeline:\n%s", out)
	}
}

// sed must really edit the file, through the same permission checks as any write.
func TestSedEditsRealFile(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "echo 'resolv-file=/var/run/x' > /tmp/t.conf")
	run(t, w, pc, "alex", "sed -i s|/var/run/x|/etc/ok| /tmp/t.conf")
	data, ok := pc.FS.Read("/tmp/t.conf")
	if !ok || strings.Contains(string(data), "/var/run/x") || !strings.Contains(string(data), "/etc/ok") {
		t.Fatalf("sed -i did not edit the file: %q", string(data))
	}
}

// logread must expose the causal chain of the planted fault, not canned text.
func TestLogreadExplainsTheFault(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	out := run(t, w, router, "root", "logread")
	if !strings.Contains(out, "dnsmasq") {
		t.Fatalf("router log should mention dnsmasq:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "resolv-file") {
		t.Fatalf("router log should name the broken resolv-file:\n%s", out)
	}
	if !strings.Contains(out, "power blip") {
		t.Fatalf("router log should record the cause (power blip):\n%s", out)
	}
}

// ss must not advertise a port-0 service as a TCP listener.
func TestSsOnlyListsRealListeners(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	out := run(t, w, router, "root", "ss -tlnp")
	if strings.Contains(out, ":0 ") || strings.Contains(out, ":0\n") {
		t.Fatalf("ss listed a port-0 socket as listening:\n%s", out)
	}
	if !strings.Contains(out, ":22") && !strings.Contains(out, ":53") {
		t.Fatalf("ss should list the router's real listeners (22/53):\n%s", out)
	}
}

// A virtual init script must perform a real service action.
func TestInitScriptRestartsService(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	router.StopService("dnsmasq")
	if router.Svc("dnsmasq").State == "running" {
		t.Fatal("setup: dnsmasq should be stopped")
	}
	out := run(t, w, router, "root", "/etc/init.d/dnsmasq restart")
	if router.Svc("dnsmasq").State != "running" {
		t.Fatalf("init script did not restart the service:\n%s", out)
	}
}

// A device with no listener must be honest about it.
func TestNFSMountFailsWhenServiceStopped(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	nas.StopService("nfsd")
	out := run(t, w, pc, "alex", "mount -t nfs nas:/srv/data /mnt/data")
	if strings.Contains(out, "mounted") {
		t.Fatalf("mount reported success with nfsd stopped:\n%s", out)
	}
}

// openForward reports whether a router's configuration currently forwards a
// WAN port — read from the config, so it is the same fact the packet path sees.
func openForward(router *core.Device, port int) bool {
	for _, r := range router.Redirects() {
		if r.Enabled && r.WPort == port {
			return true
		}
	}
	return false
}
