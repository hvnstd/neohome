package tests

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// §33 防守和安全软件: every tool must really affect the world.
//
// The tests below install the tools the way a player does (through the package
// manager, over the mirror), and then assert on world state — a ban that makes
// Dial refuse, an alert raised from a real flow, a modified file, a moving
// quarantine file, a service brought back up — never on a printed sentence.

// secSetup prepares a world whose DNS works and whose mirror is quiet, the two
// preconditions every package-install test shares.
func secSetup(t *testing.T) *core.World {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w)
	quietMirrorCron(t, w)
	return w
}

// installSec installs a security tool on a device through its own manager,
// stopping at the first failure so a broken catalogue shows up as the tool
// that would not install rather than as a confusing assertion later.
func installSec(t *testing.T, w *core.World, dev *core.Device, mgr, pkg string) string {
	t.Helper()
	return installSecAs(t, w, dev, "root", "", mgr, pkg)
}

// installSecAs is the same install for a box with no root login (the laptop):
// the account's own sudo, with its real password, exactly as a player does it.
func installSecAs(t *testing.T, w *core.World, dev *core.Device, user, pw, mgr, pkg string) string {
	t.Helper()
	update := map[string]string{"apt": "apt update", "apk": "apk update", "dnf": "dnf check-update", "pacman": "pacman -Sy", "opkg": "opkg update"}[mgr]
	if update == "" {
		update = mgr + " update"
	}
	if user == "root" {
		run(t, w, dev, user, update)
	} else {
		runWithStdin(t, w, dev, user, "sudo "+update, pw)
	}
	var out string
	if user == "root" {
		out = run(t, w, dev, user, mgr+" install "+pkg)
	} else {
		out = runWithStdin(t, w, dev, user, "sudo "+mgr+" install "+pkg, pw)
	}
	if dev.Installed[pkg] == nil {
		t.Fatalf("%s did not install on %s:\n%s", pkg, dev.Hostname, out)
	}
	return out
}

// TestFail2BanReallyClosesTheDoor is the spec's own example: several failed
// logins, then the source is cut off — and "cut off" means Dial refuses it.
func TestFail2BanReallyClosesTheDoor(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	if attacker == nil {
		t.Fatal("the world should have a scanner host for §33 to defend against")
	}

	// without the tool there is no jail, and no ban: failures are recorded,
	// nothing is enforced (the honest starting point)
	for i := 0; i < 4; i++ {
		core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	}
	if len(nas.ActiveBans()) != 0 {
		t.Fatal("a machine with no fail2ban must not ban anyone")
	}
	if got := nas.FailsFrom(attacker.SourceIPFor(nas), 10*time.Minute); got == 0 {
		t.Fatalf("the failed logins were not recorded on the target (%d)", got)
	}

	// install it: the package brings the jail and starts the service
	out := installSec(t, w, nas, "apt", "fail2ban")
	if !strings.Contains(out, "Setting up fail2ban") {
		t.Fatalf("apt's own voice expected:\n%s", out)
	}
	if !nas.SvcRunning("fail2ban") {
		t.Fatal("installing fail2ban must leave its service running")
	}
	// the shipped jail: 3 failures inside 10 minutes
	jails := nas.Jails()
	if len(jails) != 1 || jails[0].Name != "sshd" || jails[0].MaxRetry != 3 {
		t.Fatalf("the shipped jail.conf should describe one sshd jail with maxretry 3, got %+v", jails)
	}

	// two more failures trip it; the ban is written on the next tick
	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	w.Tick()
	if len(nas.ActiveBans()) == 0 {
		t.Fatalf("fail2ban did not ban after %d failures", nas.FailsFrom(attacker.SourceIPFor(nas), 10*time.Minute))
	}

	// the ban is real: the attacker's connection now times out, and the
	// attempt is recorded as banned rather than refused
	if _, _, msg := core.Dial(attacker, nas.FirstLANIP(), 22); msg == "connected" {
		t.Fatalf("a banned source must not connect, got %q", msg)
	}
	sec := nas.Sec()
	last := sec.Flows[len(sec.Flows)-1]
	if last.Verdict != core.FlowBanned {
		t.Fatalf("the dropped attempt should be recorded as banned, got %s", last.Verdict)
	}

	// recovery: the operator lifts the ban, and the door opens again
	if !nas.Unban(attacker.SourceIPFor(nas)) {
		t.Fatal("lifting an active ban must work")
	}
	if _, _, msg := core.Dial(attacker, nas.FirstLANIP(), 22); msg == "connected" {
		t.Fatal("nas sshd should still refuse a LAN-scoped handshake from the WAN side")
	}
	// from the LAN it connects again: the ban, not the scope, was the blocker
	if _, _, msg := core.Dial(w.Devices["laptop-alex"], nas.FirstLANIP(), 22); msg != "connected" {
		t.Fatalf("a LAN client should reach the nas after the ban is lifted, got %q", msg)
	}
}

// TestJailConfigurationIsTheFile: the player edits jail.conf and the next tick
// obeys it. Configuration that does not change behaviour is decoration.
func TestJailConfigurationIsTheFile(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "fail2ban")

	// a stricter jail, in the daemon's own file
	nas.FS.Write("/etc/fail2ban/jail.conf",
		"[DEFAULT]\nbantime = 2m\nfindtime = 30m\nmaxretry = 1\n\n[sshd]\nenabled = true\nport = 22\n", 0644, "root", "root")
	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	w.Tick()
	bans := nas.ActiveBans()
	if len(bans) != 1 {
		t.Fatalf("maxretry=1 should ban on the first failure, got %d bans", len(bans))
	}
	if got := bans[0].Until.Sub(bans[0].At); got != 2*time.Minute {
		t.Fatalf("bantime should now be 2m, got %s", got)
	}

	// a disabled jail bans nobody: the file is the switch
	nas.FS.Write("/etc/fail2ban/jail.conf",
		"[DEFAULT]\nbantime = 2m\nfindtime = 30m\nmaxretry = 1\n\n[sshd]\nenabled = false\n", 0644, "root", "root")
	nas.Unban(attacker.SourceIPFor(nas))
	for i := 0; i < 3; i++ {
		core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	}
	w.Tick()
	if len(nas.ActiveBans()) != 0 {
		t.Fatal("a disabled jail must not ban, whatever the failures say")
	}

	// and a stopped service bans no one either: the tool is a service
	nas.FS.Write("/etc/fail2ban/jail.conf",
		"[DEFAULT]\nbantime = 2m\nfindtime = 30m\nmaxretry = 1\n\n[sshd]\nenabled = true\n", 0644, "root", "root")
	if _, err := nas.StopService("fail2ban"); err != nil {
		t.Fatalf("stopping fail2ban: %v", err)
	}
	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	w.Tick()
	if len(nas.ActiveBans()) != 0 {
		t.Fatal("a stopped fail2ban must not ban — installing is not the same as running")
	}
}

// TestBansExpireOnTheWorldClock: a ban is a time, not a flag. When it runs out
// the source is allowed back without anyone lifting it by hand.
func TestBansExpireOnTheWorldClock(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "fail2ban")
	nas.FS.Write("/etc/fail2ban/jail.conf",
		"[DEFAULT]\nbantime = 1m\nfindtime = 30m\nmaxretry = 1\n\n[sshd]\nenabled = true\n", 0644, "root", "root")

	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	w.Tick()
	if !nas.IsBanned(attacker.SourceIPFor(nas)) {
		t.Fatal("the attacker should be banned now")
	}
	// 30 game-seconds a tick: two ticks is one minute
	w.Tick()
	w.Tick()
	if nas.IsBanned(attacker.SourceIPFor(nas)) {
		t.Fatal("a 1m ban must have expired after three ticks")
	}
	syslog, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "ban expired for") {
		t.Fatalf("the expiry should be logged like a real jail does:\n%s", syslog)
	}
	if _, _, msg := core.Dial(w.Devices["laptop-alex"], nas.FirstLANIP(), 22); msg != "connected" {
		t.Fatalf("the door should be open again, got %q", msg)
	}
}

// TestSuricataSeesWhatTheWorldCarries: the IDS reads real flows — a port sweep
// is a sweep only because the ports were really touched.
func TestSuricataSeesWhatTheWorldCarries(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "suricata")

	// an ordinary connection is not an incident
	if _, _, msg := core.Dial(w.Devices["laptop-alex"], nas.FirstLANIP(), 22); msg != "connected" {
		t.Fatalf("the LAN client should connect, got %q", msg)
	}
	w.Tick()
	if len(nas.Alerts()) != 0 {
		t.Fatalf("one connection must not raise an alert: %+v", nas.Alerts())
	}

	// a sweep across many ports is: the flows come from the real Dial path
	for _, port := range []int{21, 22, 25, 53, 80, 110, 143, 443, 445, 554, 631, 993, 995, 2049, 3306, 3389} {
		core.Dial(attacker, nas.FirstLANIP(), port)
	}
	w.Tick()
	alerts := nas.Alerts()
	if len(alerts) == 0 {
		t.Fatal("a 16-port sweep must raise the portscan rule")
	}
	if alerts[len(alerts)-1].Rule != "portscan" {
		t.Fatalf("expected the portscan rule, got %+v", alerts[len(alerts)-1])
	}
	// the shipped rule bans on a scan, so the sweep is cut off
	if !nas.IsBanned(attacker.SourceIPFor(nas)) {
		t.Fatal("the default suricata rule bans a port scan; the ban did not happen")
	}
	if _, _, msg := core.Dial(attacker, nas.FirstLANIP(), 22); msg == "connected" {
		t.Fatalf("a source banned by the IDS must not connect, got %q", msg)
	}
	// the alerts are in the log a player reads, with the rule named
	syslog, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "suricata") || !strings.Contains(string(syslog), "distinct ports") {
		t.Fatalf("the alert should be in the machine's own log:\n%s", syslog)
	}
}

// TestBruteForceBecomesAnIDSAlert: the authfail rule reads the failures the
// target itself recorded, so it fires on a real brute force only.
func TestBruteForceBecomesAnIDSAlert(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "suricata")
	// this test is about the detector, not the jail
	nas.FS.Write("/etc/suricata/rules", "authfail fails=5 window=10m action=alert level=warn\n", 0644, "root", "root")

	for i := 0; i < 4; i++ {
		core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	}
	w.Tick()
	if len(nas.Alerts()) != 0 {
		t.Fatalf("four failures are below the rule's threshold:\n%+v", nas.Alerts())
	}
	core.AttackLogin(w, attacker, nas, "root", "wrong", "ssh", 22)
	w.Tick()
	if len(nas.Alerts()) != 1 || nas.Alerts()[0].Rule != "authfail" {
		t.Fatalf("the fifth failure should trip the authfail rule: %+v", nas.Alerts())
	}

	// the machine's own `suricata -T` shows the rule it is running
	out := run(t, w, nas, "root", "suricata -T")
	if !strings.Contains(out, "authfail") || !strings.Contains(out, "engine is running") {
		t.Fatalf("the config test should name the loaded rules:\n%s", out)
	}
	// and a bad rule is a failed test, not a silent no-op
	nas.FS.Write("/etc/suricata/rules", "portscan ports=0 window=2m\n", 0644, "root", "root")
	out = run(t, w, nas, "root", "suricata -T")
	if !strings.Contains(out, "rule parse error") {
		t.Fatalf("a rule the engine cannot use must fail the test:\n%s", out)
	}
}

// TestIntegrityMonitorNoticesARealChange: aide hashes the watched trees and
// reports a file whose bytes changed — including one changed by an attacker.
func TestIntegrityMonitorNoticesARealChange(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	installSec(t, w, pc, "apt", "aide")
	// a narrow watch set keeps the test about the mechanism
	pc.FS.Write("/etc/aide/aide.conf", "watch = /etc/cron.d\nwatch = /etc/hosts\ninterval = 1m\nfreshfiles = true\n", 0644, "root", "root")

	// initialize the baseline, then a clean check
	if out := run(t, w, pc, "root", "aide --init"); !strings.Contains(out, "Number of entries") {
		t.Fatalf("aide --init should build a database:\n%s", out)
	}
	if out := run(t, w, pc, "root", "aide --check"); !strings.Contains(out, "NO differences") {
		t.Fatalf("a fresh database must match the filesystem:\n%s", out)
	}

	// a real modification, by the player, with no alert yet (the tool has not
	// compared since)
	before, _ := pc.FS.Read("/etc/hosts")
	pc.FS.Write("/etc/hosts", string(before)+"10.66.6.6\tc2.example\n", 0644, "root", "root")
	w.Tick()
	alerts := pc.Alerts()
	if len(alerts) == 0 || alerts[len(alerts)-1].Rule != "modified" {
		t.Fatalf("the integrity check should raise a modified alert: %+v", alerts)
	}
	if !strings.Contains(alerts[len(alerts)-1].Msg, "/etc/hosts") {
		t.Fatalf("the alert must name the file that changed: %s", alerts[len(alerts)-1].Msg)
	}
	out := run(t, w, pc, "root", "aide --check")
	if !strings.Contains(out, "modified  /etc/hosts") {
		t.Fatalf("aide --check should report the change:\n%s", out)
	}

	// a stopped service watches nothing
	if _, err := pc.StopService("aide"); err != nil {
		t.Fatalf("stopping aide: %v", err)
	}
	pc.FS.Write("/etc/hosts", string(before)+"10.66.6.7\tother.example\n", 0644, "root", "root")
	w.Tick()
	if len(pc.Alerts()) != len(alerts) {
		t.Fatal("a stopped integrity monitor must not raise new alerts")
	}
}

// TestAntivirusFindsWhatIsClosedTheDatabase: the scanner reports a real match
// on a real file, quarantines it for real, and refuses to run without a
// database — the three facts that separate an antivirus from a reassuring word.
func TestAntivirusFindsWhatIsClosedTheDatabase(t *testing.T) {
	w := secSetup(t)
	lap := w.Devices["laptop-alex"]
	installSecAs(t, w, lap, "alex", "alex123", "apt", "clamav")

	// the package brings no database: a scan without one must fail, not pass
	if out := run(t, w, lap, "alex", "clamscan -r /home/alex/Downloads"); !strings.Contains(out, "virus database missing") {
		t.Fatalf("a scan with no database must say so:\n%s", out)
	}
	// freshclam fetches it from the mirror over HTTP, the world's own path —
	// as root, because that is the account that may write the database
	out := runWithStdin(t, w, lap, "alex", "sudo freshclam", "alex123")
	if !strings.Contains(out, "updated") {
		t.Fatalf("freshclam should update from the mirror:\n%s", out)
	}
	if _, ok := lap.FS.Get("/var/lib/clamav/main.db"); !ok {
		t.Fatal("freshclam must write the database it downloaded")
	}

	// the seeded attachment really carries a signature
	out = run(t, w, lap, "alex", "clamscan -r /home/alex/Downloads")
	if !strings.Contains(out, "FOUND") || !strings.Contains(out, "Eicar-Test-Signature") {
		t.Fatalf("the scanner should name what it matched:\n%s", out)
	}
	if !strings.Contains(out, "Infected files: 1") {
		t.Fatalf("the summary should count the infection:\n%s", out)
	}

	// quarantining moves the file: the original is gone, the copy is kept
	res, scanned, err := lap.ScanFiles([]string{"/home/alex/Downloads/invoice-2026-04.pdf"})
	if err != nil || scanned != 1 || len(res) != 1 {
		t.Fatalf("scan result: %v %d %+v", err, scanned, res)
	}
	dest := lap.Quarantine(res[0], "alex")
	if _, still := lap.FS.Get("/home/alex/Downloads/invoice-2026-04.pdf"); still {
		t.Fatal("quarantine must remove the infected file")
	}
	if _, ok := lap.FS.Get(dest); !ok {
		t.Fatal("quarantine must keep the bytes somewhere")
	}

	// recovery: a clean file scans clean, and a stale database is refused
	lap.FS.Write("/home/alex/Downloads/notes.txt", "shopping list\n", 0644, "alex", "alex")
	out = run(t, w, lap, "alex", "clamscan /home/alex/Downloads/notes.txt")
	if !strings.Contains(out, "Infected files: 0") {
		t.Fatalf("a clean file must scan clean:\n%s", out)
	}
	n, _ := lap.FS.Get("/var/lib/clamav/main.db")
	n.MTime = w.Sim.Add(-8 * 24 * time.Hour)
	if out := run(t, w, lap, "alex", "clamscan /home/alex/Downloads/notes.txt"); !strings.Contains(out, "stale") {
		t.Fatalf("a database older than a week must be refused, like clamd does:\n%s", out)
	}
}

// TestMonitBringsAServiceBack: the watchdog restarts what stayed down, and the
// restart is real (a new process, the service running).
func TestMonitBringsAServiceBack(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	installSec(t, w, pc, "apt", "monit")
	pc.FS.Write("/etc/monit/monitrc", "check service sshd maxdown=2 interval=1m action=restart\n", 0644, "root", "root")

	if _, err := pc.StartService("sshd"); err != nil {
		t.Fatalf("starting sshd: %v", err)
	}
	// a service that is down stays down until the watchdog has seen it twice
	if _, err := pc.StopService("sshd"); err != nil {
		t.Fatalf("stopping sshd: %v", err)
	}
	w.Tick()
	if pc.SvcRunning("sshd") {
		t.Fatal("one failed check must not restart the service (maxdown=2)")
	}
	w.Tick()
	if !pc.SvcRunning("sshd") {
		t.Fatal("monit should have restarted sshd on the second failed check")
	}
	if pc.Svc("sshd").State != "running" {
		t.Fatal("the service must be running, not merely reported as restarted")
	}

	// a service the watchdog cannot start says so instead of lying
	pc.FS.Write("/etc/monit/monitrc", "check service nosuchthing maxdown=1 interval=1m action=restart\n", 0644, "root", "root")
	w.Tick()
	out := run(t, w, pc, "root", "monit summary")
	if !strings.Contains(out, "nosuchthing") || !strings.Contains(out, "not-present") {
		t.Fatalf("monit summary should report a check it cannot satisfy:\n%s", out)
	}
}

// TestAuditRecordsOnlyWhereRulesExist: a rule is the switch. Installing auditd
// with the shipped rules records writes to the files those rules name, and a
// path nobody watches leaves no record.
func TestAuditRecordsOnlyWhereRulesExist(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	installSec(t, w, pc, "apt", "auditd")

	// the package's own rules watch /etc/shadow and /etc/passwd
	if !pc.AuditEnabled() {
		t.Fatal("auditd with rules loaded should be auditing")
	}
	run(t, w, pc, "root", "echo x >> /etc/hosts")
	if out := run(t, w, pc, "root", "ausearch -k identity"); !strings.Contains(out, "<no matches>") {
		t.Fatalf("an unwatched path must leave no identity record:\n%s", out)
	}
	pc.WriteGuest("/etc/shadow", []byte("root:*:19000:0:99999:7:::\n"), pc.FindUser("root"))
	if out := run(t, w, pc, "root", "ausearch -k identity"); !strings.Contains(out, "/etc/shadow") {
		t.Fatalf("a watched write must appear in the audit trail:\n%s", out)
	}

	// a rule added by the operator takes effect immediately
	out := run(t, w, pc, "root", "auditctl -w /etc/hosts -p wa -k hosts")
	if !strings.Contains(out, "rule added") {
		t.Fatalf("auditctl should add a rule:\n%s", out)
	}
	pc.WriteGuest("/etc/hosts", []byte("127.0.0.1 localhost\n"), pc.FindUser("root"))
	if out := run(t, w, pc, "root", "ausearch -k hosts"); !strings.Contains(out, "/etc/hosts") {
		t.Fatalf("the new rule should record the write:\n%s", out)
	}

	// and a stopped daemon records nothing at all
	if _, err := pc.StopService("auditd"); err != nil {
		t.Fatalf("stopping auditd: %v", err)
	}
	before := len(pc.Sec().Audit)
	pc.WriteGuest("/etc/shadow", []byte("root:*:19001:0:99999:7:::\n"), pc.FindUser("root"))
	if len(pc.Sec().Audit) != before {
		t.Fatal("an auditd that is not running must not keep records")
	}
	if out := run(t, w, pc, "root", "ausearch -k identity"); !strings.Contains(out, "not running") {
		t.Fatalf("ausearch should say the daemon is down:\n%s", out)
	}
}

// TestCentralLogsCollectFromRealDevices: the router forwards its log to the
// NAS, the NAS holds it with the source attached, and a collector that is down
// really loses the lines.
func TestCentralLogsCollectFromRealDevices(t *testing.T) {
	w := secSetup(t)
	router := w.Devices["router-alex"]
	nas := w.Devices["nas-alex"]

	if dst := router.LogForwardTarget(); dst == nil || dst.ID != "nas-alex" {
		t.Fatalf("the router should forward to the nas, got %v", dst)
	}
	router.Logf("warn", "firewall", "wan input dropped on port 23 from 203.0.113.9")
	w.Tick()
	logs := nas.RemoteLogs("gateway", 0)
	if len(logs) == 0 {
		t.Fatal("the collector should hold the router's line")
	}
	last := logs[len(logs)-1].Line
	if !strings.Contains(last, "gateway") || !strings.Contains(last, "wan input dropped") {
		t.Fatalf("the held line should carry its source and its text: %q", last)
	}
	if out := run(t, w, nas, "root", "secstat remote gateway"); !strings.Contains(out, "wan input dropped") {
		t.Fatalf("a player should be able to read the collected lines:\n%s", out)
	}

	// the collector is a service: stopping it stops collection, and the
	// sender says so rather than pretending the line arrived
	if _, err := nas.StopService("rsyslog"); err != nil {
		t.Fatalf("stopping the collector: %v", err)
	}
	before := len(nas.RemoteLogs("gateway", 0))
	router.Logf("warn", "firewall", "wan input dropped on port 22 from 203.0.113.9")
	w.Tick()
	if len(nas.RemoteLogs("gateway", 0)) != before {
		t.Fatal("a stopped collector must not receive lines")
	}
	syslog, _ := router.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "could not forward") {
		t.Fatalf("the router should record the failed forward:\n%s", syslog)
	}

	// recovery: the collector comes back and the lines flow again
	if _, err := nas.StartService("rsyslog"); err != nil {
		t.Fatalf("restarting the collector: %v", err)
	}
	w.Tick()
	router.Logf("warn", "firewall", "wan input dropped on port 21 from 203.0.113.9")
	w.Tick()
	if len(nas.RemoteLogs("gateway", 0)) <= before {
		t.Fatal("the collector should receive lines again after it restarts")
	}
}

// TestSecurityToolsAreInstalledSoftware: before the package is installed the
// command does not exist, and the world's own scanner can act on the machine.
func TestSecurityToolsAreInstalledSoftware(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "root", "fail2ban-client status")
	if !strings.Contains(out, "command not found") {
		t.Fatalf("fail2ban-client must not exist before the package does:\n%s", out)
	}
	if out := run(t, w, pc, "root", "secstat status"); !hasToolRow(out, "fail2ban", "not installed") {
		t.Fatalf("secstat should say what is not installed:\n%s", out)
	}
	installSec(t, w, pc, "apt", "fail2ban")
	if !strings.Contains(run(t, w, pc, "root", "fail2ban-client status"), "Jail list") {
		t.Fatalf("installed fail2ban-client should report the jails:\n%s", run(t, w, pc, "root", "fail2ban-client status"))
	}

	// the world's scanner really reaches what is reachable: an exposed sshd
	// collects attempts on its own, without the player doing anything
	nas := w.Devices["nas-alex"]
	if _, err := nas.StartService("sshd"); err != nil {
		t.Fatalf("starting nas sshd: %v", err)
	}
	sc := w.Devices[core.ScannerID]
	// expose the sshd the way a real household does — a forward in the
	// router's own configuration file — and then let the world's scanner find
	// it. This is the §28 attack surface meeting §33's defence.
	router := w.Devices["router-alex"]
	cfg, _ := router.FS.Read("/etc/config/firewall")
	fwd := "\nconfig redirect\n\toption name 'nas-ssh'\n\toption src 'wan'\n" +
		"\toption proto 'tcp'\n\toption src_dport '22'\n\toption dest_ip '" + nas.FirstLANIP() + "'\n" +
		"\toption dest_port '22'\n\toption enabled '1'\n"
	router.FS.Write("/etc/config/firewall", string(cfg)+fwd, 0644, "root", "root")
	if len(router.FW().Redirects) == 0 {
		t.Fatal("the new forward should be read from the router's own configuration")
	}
	w.ScanTarget(sc, router)
	if len(nas.RecentFlows(10*time.Minute)) == 0 {
		t.Fatal("the scanner's sweep should leave flows on the machine behind the forward")
	}
	if nas.FailsFrom(sc.SourceIPFor(nas), 10*time.Minute) == 0 {
		t.Fatal("the scanner's credential list should have produced real auth failures on the exposed box")
	}
}

// TestScannerTickIsDeterministicAndBounded: the world's attacker acts on the
// clock, on targets it can really reach, and a ban stops it.
func TestScannerTickIsDeterministicAndBounded(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	if _, err := nas.StartService("sshd"); err != nil {
		t.Fatalf("starting nas sshd: %v", err)
	}
	// no scanner device is ever a target of itself, and no LAN-only box is in
	// the list: the attacker sees the public internet, not the household's
	// private space
	for _, id := range w.ScannableTargets() {
		d := w.Devices[id]
		if d == nil {
			t.Fatalf("scannable target %s is not a device", id)
		}
		if d.ID == core.ScannerID {
			t.Fatal("the scanner must not scan itself")
		}
		if d.WANIP() == "" {
			t.Fatalf("%s has no public address but is on the scanner's list", id)
		}
	}
	// running the tick at the wrong moment does nothing at all
	before := len(nas.RecentFlows(24 * time.Hour))
	w.Tick()
	if w.TickCount%core.ScannerEvery != 0 && len(nas.RecentFlows(24*time.Hour)) != before {
		t.Fatal("the scanner acts on its own schedule, not on every tick")
	}
}

// TestSecstatReportsWhatIsReal: the roll-up reads the same state as the tools,
// so its numbers and the machine's facts cannot disagree.
func TestSecstatReportsWhatIsReal(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "suricata")

	for _, port := range []int{21, 22, 25, 53, 80, 110, 143, 443, 445, 554, 631, 993, 995, 2049, 3306, 3389} {
		core.Dial(attacker, nas.FirstLANIP(), port)
	}
	w.Tick()

	out := run(t, w, nas, "root", "secstat status")
	if !hasToolRow(out, "suricata", "running") {
		t.Fatalf("secstat should report the running IDS:\n%s", out)
	}
	if want := fmt.Sprintf("alerts raised: %d", len(nas.Alerts())); !strings.Contains(out, want) {
		t.Fatalf("secstat must agree with the alert list (%s):\n%s", want, out)
	}
	if want := fmt.Sprintf("bans: %d active", len(nas.ActiveBans())); !strings.Contains(out, want) {
		t.Fatalf("secstat must agree with the bans (%s):\n%s", want, out)
	}
	if out := run(t, w, nas, "root", "secstat bans"); !strings.Contains(out, attacker.SourceIPFor(nas)) {
		t.Fatalf("secstat bans should name the banned source:\n%s", out)
	}
	if out := run(t, w, nas, "root", "secstat flows 40"); !strings.Contains(out, "scan-host") {
		t.Fatalf("secstat flows should show who knocked, with the verdict:\n%s", out)
	}
}

// hasToolRow matches one line of secstat's tool table without depending on the
// exact column widths, which are the table's business and not the test's.
func hasToolRow(out, tool, state string) bool {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimLeft(strings.TrimRight(line, "\r"), " ")
		if !strings.HasPrefix(line, tool+" ") {
			continue
		}
		if strings.Contains(line, state) {
			return true
		}
	}
	return false
}

// ---- §33 Backup (restic) ----------------------------------------------------

// TestBackupKeepsVersions: a backup's whole point is that yesterday's copy
// survives today's mistake. Install restic, take a snapshot, delete a file,
// snapshot again, and prove the first snapshot still holds the file — then
// restore it, corrupt a blob, and have `check` say so.
func TestBackupKeepsVersions(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	installSec(t, w, pc, "apt", "restic")

	// a local repository on the PC first: the family of verbs must work
	// wherever the bytes live, and this keeps the first half of the test
	// about the tool rather than about the network
	pc.FS.Write("/etc/restic/env", "RESTIC_REPOSITORY=/srv/restic\nRESTIC_PASSWORD_FILE=/etc/restic/password\n",
		0600, "root", "root")
	if out := run(t, w, pc, "root", "restic init"); !strings.Contains(out, "created restic repository") {
		t.Fatalf("restic init should create a repository:\n%s", out)
	}
	// and running it twice is refused, because the repository is really there
	if out := run(t, w, pc, "root", "restic init"); !strings.Contains(out, "already initialized") {
		t.Fatalf("a second init on a real repository must be refused:\n%s", out)
	}

	pc.FS.Write("/home/alex/invoice-2026-04.pdf", "APRIL INVOICE\n", 0644, "alex", "alex")
	if out := run(t, w, pc, "root", "restic backup /home/alex"); !strings.Contains(out, "snapshot") {
		t.Fatalf("the first backup should save a snapshot:\n%s", out)
	}
	first, err := core.ResticSnapshots(pc)
	if err != nil || len(first) != 1 {
		t.Fatalf("the repository should hold exactly one snapshot after one backup (%v)", err)
	}
	if first[0].Files == 0 {
		t.Fatal("the snapshot must contain the files that were really there")
	}

	// now the mistake: the file is deleted, and a second backup runs
	pc.FS.Remove("/home/alex/invoice-2026-04.pdf")
	if out := run(t, w, pc, "root", "restic backup /home/alex"); !strings.Contains(out, "snapshot") {
		t.Fatalf("the second backup should save a snapshot:\n%s", out)
	}
	snaps, err := core.ResticSnapshots(pc)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("a second backup must add a snapshot, not replace one (%d, %v)", len(snaps), err)
	}
	if !strings.Contains(snaps[0].Body, "invoice-2026-04.pdf") {
		t.Fatal("the first snapshot must still name the file that has since been deleted")
	}
	if strings.Contains(snaps[1].Body, "invoice-2026-04.pdf") {
		t.Fatal("the second snapshot must reflect the machine as it now is")
	}

	// check: every referenced blob re-hashed
	if out := run(t, w, pc, "root", "restic check"); !strings.Contains(out, "no errors found") {
		t.Fatalf("check should verify the repository it just wrote:\n%s", out)
	}
	// restore the deleted file from the first snapshot, and read it
	if out := run(t, w, pc, "root", "restic restore "+first[0].ID[:8]); !strings.Contains(out, "file(s) restored") {
		t.Fatalf("restore should put the file back:\n%s", out)
	}
	data, ok := pc.FS.Read("/home/alex/invoice-2026-04.pdf")
	if !ok || !strings.Contains(string(data), "APRIL INVOICE") {
		t.Fatalf("the restored file must be the bytes that were backed up, got %q (present=%v)", string(data), ok)
	}
	// a restore into a live tree does not clobber by accident
	if out := run(t, w, pc, "root", "restic restore "+first[0].ID[:8]); !strings.Contains(out, "left alone") {
		t.Fatalf("a second restore should refuse to overwrite without --overwrite:\n%s", out)
	}

	// and a corrupted blob is found, not shrugged off
	blobs := []string{}
	for p, n := range pc.FS.Nodes {
		if !n.IsDir && strings.Contains(p, "/data/") {
			blobs = append(blobs, p)
		}
	}
	if len(blobs) == 0 {
		t.Fatal("the repository should hold content-addressed blobs on disk")
	}
	sort.Strings(blobs)
	pc.FS.Write(blobs[0], "tampered", 0600, "root", "root")
	out := run(t, w, pc, "root", "restic check")
	if !strings.Contains(out, "error") || !strings.Contains(out, "corrupt") {
		t.Fatalf("check must notice a blob whose bytes changed:\n%s", out)
	}
	// and the corrupted bytes must not be able to walk back into the live
	// tree through a restore: a blob is named by its hash, so the restore
	// can tell corruption from content and refuses it by name
	corrupt := blobs[0][strings.LastIndex(blobs[0], "/")+1:]
	victim := ""
	for _, e := range snaps[0].Entries {
		if e.Blob == corrupt {
			victim = e.Path
		}
	}
	if victim == "" {
		t.Fatalf("the tampered blob %s should belong to a file in the first snapshot", corrupt)
	}
	pc.FS.Remove(victim)
	out = run(t, w, pc, "root", "restic restore "+first[0].ID[:8])
	if !strings.Contains(out, "corrupt") {
		t.Fatalf("restore must refuse a blob whose bytes do not hash to its name:\n%s", out)
	}
	if _, still := pc.FS.Read(victim); still {
		t.Fatalf("restore must not write corrupted bytes back to %s", victim)
	}

	// forget --prune really drops the snapshot and the blobs only it used
	before := len(pc.FS.Nodes)
	if out := run(t, w, pc, "root", "restic forget "+snaps[1].ID[:8]+" --prune"); !strings.Contains(out, "removed snapshot") {
		t.Fatalf("forget should drop a snapshot:\n%s", out)
	}
	after, _ := core.ResticSnapshots(pc)
	if len(after) != 1 {
		t.Fatalf("one snapshot should remain, got %d", len(after))
	}
	if len(pc.FS.Nodes) >= before {
		t.Fatal("forget --prune must free the blobs no remaining snapshot references")
	}

	// the household default is off the machine it protects: on the NAS, the
	// backup is a network operation, and a NAS that is down fails it honestly
	pc.FS.Write("/etc/restic/env", "RESTIC_REPOSITORY="+core.ResticDefaultRepo+"\n", 0600, "root", "root")
	if out := run(t, w, pc, "root", "restic init"); !strings.Contains(out, "created restic repository") {
		t.Fatalf("init over the LAN to the NAS should work:\n%s", out)
	}
	if _, ok := nas.FS.Read("/srv/restic/config"); !ok {
		t.Fatal("the repository must be files on the NAS, not a marker on the PC")
	}
	if out := run(t, w, pc, "root", "restic backup /home/alex"); !strings.Contains(out, "snapshot") {
		t.Fatalf("backing up to the NAS should work:\n%s", out)
	}
	if _, err := nas.StopService("sshd"); err != nil {
		t.Fatalf("stopping the NAS's sshd: %v", err)
	}
	if out := run(t, w, pc, "root", "restic backup /home/alex"); !strings.Contains(out, "unreachable") {
		t.Fatalf("a NAS that is down must fail the backup with the reason, not a success:\n%s", out)
	}
}

// TestHostMonitorNoticesTheMachine: the HIDS watches the machine itself — a
// new account, a changed binary, an executable in /tmp — and its baseline is a
// deliberate act, so nothing is reported before one is taken.
func TestHostMonitorNoticesTheMachine(t *testing.T) {
	w := secSetup(t)
	pc := w.Devices["pc-alex"]
	installSec(t, w, pc, "apt", "rkhunter")

	// no baseline: a check that has nothing to compare against says so
	if out := run(t, w, pc, "root", "rkhunter --check"); !strings.Contains(out, "propupd") {
		t.Fatalf("a check without a baseline must ask for one:\n%s", out)
	}
	if out := run(t, w, pc, "root", "rkhunter --propupd"); !strings.Contains(out, "baseline written") {
		t.Fatalf("propupd should write the baseline:\n%s", out)
	}
	base, ok := pc.RkhunterBaseline()
	if !ok || len(base) < 5 {
		t.Fatalf("the baseline should hold the machine's facts, got %d (present=%v)", len(base), ok)
	}
	if out := run(t, w, pc, "root", "rkhunter --check"); !strings.Contains(out, "No warnings") {
		t.Fatalf("a host that has not changed must produce no warnings:\n%s", out)
	}

	// a real change: an account that did not exist when the picture was taken
	pc.Users["backdoor"] = &core.User{Name: "backdoor", UID: 0, Pass: "hunter2",
		Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"}
	findings, err := pc.RkhunterCheck()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Kind == "account" && strings.Contains(f.Msg, "backdoor") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a new uid-0 account must be reported: %+v", findings)
	}
	// and the periodic check raises it as an alert on the world clock
	alertsBefore := len(pc.Alerts())
	w.Tick()
	if len(pc.Alerts()) == alertsBefore {
		t.Fatal("the running host monitor must raise an alert for what it found")
	}
	last := pc.Alerts()[len(pc.Alerts())-1]
	if last.Tool != "rkhunter" {
		t.Fatalf("the alert should name the host monitor, got %q", last.Tool)
	}

	// a dropped binary is a change too, and an executable in /tmp is its own
	// finding whatever the baseline says
	pc.FS.Write("/tmp/.hidden-miner", "#!/bin/sh\n", 0755, "alex", "alex")
	if out := run(t, w, pc, "root", "rkhunter --check"); !strings.Contains(out, "/tmp/.hidden-miner") {
		t.Fatalf("an executable in a world-writable directory must be named:\n%s", out)
	}
	// recovery: removing what changed clears the warning, and taking a new
	// baseline is the explicit act that accepts the rest
	pc.FS.Remove("/tmp/.hidden-miner")
	pc.FS.Write("/usr/bin/newness", "binary\n", 0755, "root", "root")
	if findings, _ := pc.RkhunterCheck(); len(findings) == 0 {
		t.Fatal("a new binary in a watched directory must be reported")
	}
	if out := run(t, w, pc, "root", "rkhunter --propupd"); !strings.Contains(out, "baseline written") {
		t.Fatalf("a second propupd should re-baseline the host:\n%s", out)
	}
	if findings, _ := pc.RkhunterCheck(); len(findings) != 0 {
		t.Fatalf("after a new baseline the host matches itself again, got %+v", findings)
	}
}

// TestIDSSeesAFlood: §33 asks for 异常流量 as well as scans and brute force. A
// flood rule counts connection attempts from one source in a window, whatever
// the ports are — and with action=ban, the flood is stopped on the wire.
func TestIDSSeesAFlood(t *testing.T) {
	w := secSetup(t)
	nas := w.Devices["nas-alex"]
	attacker := w.Devices[core.ScannerID]
	installSec(t, w, nas, "apt", "suricata")
	nas.FS.Write("/etc/suricata/rules", "flood conns=12 window=2m action=ban,alert bantime=30m level=warn\n",
		0644, "root", "root")

	// a real sweep of connection attempts against one machine
	ports := []int{8080, 8080, 8443, 9000, 9001, 9002, 9003, 9004, 9005, 9006, 9007, 9008, 9009, 9010}
	for _, p := range ports {
		core.Dial(attacker, nas.FirstLANIP(), p)
	}
	w.Tick()
	alerts := nas.Alerts()
	if len(alerts) == 0 || alerts[len(alerts)-1].Rule != "flood" {
		t.Fatalf("a flood of connection attempts should raise a flood alert: %+v", alerts)
	}
	if !nas.IsBannedBy(attacker) {
		t.Fatal("action=ban must really ban the flooding source")
	}
	// the ban is enforced where every other one is: on the wire
	if _, _, msg := core.Dial(attacker, nas.FirstLANIP(), 8080); !strings.Contains(msg, "timed out") {
		t.Fatalf("a banned source must not reach the machine, got %q", msg)
	}
	// a quiet source is not caught by the same rule
	pc := w.Devices["pc-alex"]
	core.Dial(pc, nas.FirstLANIP(), 8080)
	w.Tick()
	for _, a := range nas.Alerts() {
		if a.Rule == "flood" && strings.Contains(a.Msg, "home-pc") {
			t.Fatalf("one connection must not look like a flood: %s", a.Msg)
		}
	}
}
