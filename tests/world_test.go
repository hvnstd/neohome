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
	nginx := w.Repos["main"].Pkgs["nginx"]
	actions := vps.InstallPkg(nginx)
	if _, ok := vps.FS.Get("/usr/sbin/nginx"); !ok {
		t.Fatal("install did not create the virtual binary")
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
	if len(router.PortFwd) == 0 || !router.PortFwd[0].Enable {
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
	if router.PortFwd[0].Enable {
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
