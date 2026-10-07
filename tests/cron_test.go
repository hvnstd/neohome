package tests

import (
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// ---- helpers -------------------------------------------------------------

// cronInstall writes a crontab through the REAL player path: a file in the
// device's VFS plus the `crontab FILE` builtin. If that path ever stops
// arming real jobs, every test below fails.
func cronInstall(t *testing.T, w *core.World, dev *core.Device, user, path, body string) {
	t.Helper()
	// One tick first: on the world's very first tick the scheduler observes
	// every running daemon for the first time and arms its jobs from "now",
	// exactly as a crond that boots with the machine does. Installing before
	// that would be installing into a daemon that has not come up yet.
	w.Tick()
	if _, ok := dev.FS.Read(path); ok {
		t.Fatalf("setup: %s already exists on %s", path, dev.Hostname)
	}
	dev.FS.Write(path, body, 0644, user, user)
	out := run(t, w, dev, user, "crontab "+path)
	if strings.Contains(out, "bad entries") || strings.Contains(out, "nothing changed") {
		t.Fatalf("crontab refused a valid file:\n%s", out)
	}
	if !strings.Contains(out, "installing new crontab") {
		t.Fatalf("crontab did not report an install:\n%s", out)
	}
}

// entryFor finds a loaded entry by device/user/command substring.
func entryFor(w *core.World, devID, user, cmdSub string) *core.CronEntry {
	if w.Cron == nil {
		return nil
	}
	for _, e := range w.Cron.Entries {
		if e.DeviceID == devID && e.User == user && strings.Contains(e.Command, cmdSub) {
			return e
		}
	}
	return nil
}

// tickUntil drives the WORLD clock (30 simulated seconds per Tick — never a
// real sleep) up to, but not through, `deadline`. Stopping short matters: it
// leaves the test on the last tick BEFORE the deadline, so the next Tick is the
// one that crosses it. That is how "fires exactly when the clock crosses the
// schedule" is checked without sleeping.
func tickUntil(t *testing.T, w *core.World, deadline time.Time, limit int) {
	t.Helper()
	for i := 0; i < limit; i++ {
		if !w.Sim.Before(deadline) {
			return
		}
		if w.Sim.Add(30 * time.Second).After(deadline) {
			return // the next tick would cross the deadline: leave it to the test
		}
		w.Tick()
	}
	t.Fatalf("world clock never reached %s (stuck at %s after %d ticks)", deadline, w.Sim, limit)
}

func tickTimes(t *testing.T, w *core.World, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		w.Tick()
	}
}

// ---- the spec itself -----------------------------------------------------

// A 5-field spec must resolve to the same instants real cron resolves to, and
// must refuse what it does not implement instead of accepting a lie.
func TestCronSpecResolvesRealTimes(t *testing.T) {
	base := time.Date(2026, 3, 10, 12, 34, 0, 0, time.Local) // a Tuesday
	cases := []struct {
		spec string
		want string
	}{
		{"* * * * *", "2026-03-10 12:35:00"},
		{"*/15 * * * *", "2026-03-10 12:45:00"},
		{"0 * * * *", "2026-03-10 13:00:00"},
		{"30 2 * * *", "2026-03-11 02:30:00"},
		{"0 0 1 * *", "2026-04-01 00:00:00"},
		{"0 0 * * fri", "2026-03-13 00:00:00"},
		{"0 0 * * 7", "2026-03-15 00:00:00"}, // 7 == Sunday
		{"0 0 29 2 *", "2028-02-29 00:00:00"},
		{"5,25,45 * * * *", "2026-03-10 12:45:00"},
		{"0 9-17 * * *", "2026-03-10 13:00:00"},
		{"0 0 * mar *", "2026-03-11 00:00:00"},
		{"0 0 1 1 *", "2027-01-01 00:00:00"},
	}
	for _, c := range cases {
		sp, err := core.ParseCronSpec(c.spec)
		if err != nil {
			t.Errorf("ParseCronSpec(%q): %v", c.spec, err)
			continue
		}
		got, ok := sp.Next(base)
		if !ok {
			t.Errorf("%q: no next fire time", c.spec)
			continue
		}
		if want := c.want; got.Format("2006-01-02 15:04:05") != want {
			t.Errorf("%q: next = %s, want %s", c.spec, got.Format("2006-01-02 15:04:05"), want)
		}
	}

	// dom+dow both restricted: Vixie says EITHER matches.
	either, err := core.ParseCronSpec("0 0 13 * fri")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// March 2026: the 13th is a Friday, the 6th and the 20th are Fridays too.
	if n, _ := either.Next(time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)); n.Day() != 6 {
		t.Errorf("dom+dow union: got %s, want the 6th (first Friday)", n.Format("2006-01-02"))
	}

	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *",
		"* * * 13 *", "* * * * 8", "*/0 * * * *", "5-1 * * * *", "@daily", "- * * * * echo x"} {
		if _, err := core.ParseCronSpec(bad); err == nil {
			t.Errorf("ParseCronSpec(%q) should have been rejected", bad)
		}
	}
}

// ---- (a) it fires when the WORLD CLOCK crosses the schedule --------------

// A due entry must fire exactly when World.Sim crosses its minute, and not one
// tick earlier. The clock is driven by Tick(), never by sleeping.
func TestCronFiresWhenWorldClockCrossesSchedule(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"# my first real schedule\n* * * * * cp /etc/hostname /home/alex/cron-proof.txt\n")

	e := entryFor(w, "pc-alex", "alex", "cron-proof.txt")
	if e == nil {
		t.Fatal("the installed crontab was not armed by the scheduler")
	}
	if !e.Next.After(w.Sim) {
		t.Fatalf("entry should be armed for a future minute, got %s (now %s)", e.Next, w.Sim)
	}
	armed := e.Next
	if _, ok := pc.FS.Read("/home/alex/cron-proof.txt"); ok {
		t.Fatal("the job's target file must not exist before the job runs")
	}

	// tick up to (but not through) the armed minute
	tickUntil(t, w, armed, 10)
	if e.Runs != 0 {
		t.Fatalf("job fired early: %d run(s) at %s, armed for %s", e.Runs, w.Sim, armed)
	}
	if _, ok := pc.FS.Read("/home/alex/cron-proof.txt"); ok {
		t.Fatal("world state changed before the schedule was due")
	}

	// one more tick crosses the minute: now it must have run
	w.Tick()
	if e.Runs != 1 {
		t.Fatalf("job did not fire on the tick that crossed %s (runs=%d, sim=%s)", armed, e.Runs, w.Sim)
	}
	data, ok := pc.FS.Read("/home/alex/cron-proof.txt")
	if !ok {
		t.Fatal("firing did not change world state: no /home/alex/cron-proof.txt")
	}
	if strings.TrimSpace(string(data)) != "home-pc" {
		t.Fatalf("the scheduled command's real effect is missing: %q", string(data))
	}
	if e.LastRC != 0 {
		t.Fatalf("job should have succeeded, rc=%d", e.LastRC)
	}
	if !e.Next.After(w.Sim) {
		t.Fatalf("entry must be rescheduled after firing: next=%s sim=%s", e.Next, w.Sim)
	}
}

// ---- (b) the run is real: files, logs, services --------------------------

// Firing a job must leave the same traces a real scheduled run leaves: the
// job's own side effects, a syslog line, and a job log.
func TestCronRunLeavesRealTraces(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	// a scheduled service restart: real service lifecycle, not a printed line
	before := nas.Svc("nginx").PID
	cronInstall(t, w, nas, "root", "/root/nascron",
		"*/5 * * * * systemctl restart nginx\n* * * * * cp /etc/hostname /tmp/cron-run.log\n")
	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"* * * * * cp /etc/hostname /home/alex/cron-proof.txt\n")

	svcEntry := entryFor(w, "nas-alex", "root", "systemctl restart nginx")
	if svcEntry == nil {
		t.Fatal("the service-restart job was not armed")
	}
	// drive the world clock past both schedules
	for i := 0; i < 12; i++ {
		w.Tick()
	}
	if svcEntry.Runs == 0 {
		t.Fatalf("the */5 job never ran (sim=%s)", w.Sim)
	}
	if nas.Svc("nginx").State != "running" {
		t.Fatal("the scheduled restart left nginx down")
	}
	if nas.Svc("nginx").PID == before {
		t.Fatal("the scheduled restart did not really restart the service (same pid)")
	}

	// device log: what a sysadmin would read to find out what happened
	syslog, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "systemctl restart nginx") {
		t.Fatalf("the run is missing from the device log:\n%s", string(syslog))
	}
	if !strings.Contains(string(syslog), "service nginx started") {
		t.Fatalf("the service restart is missing from the device log:\n%s", string(syslog))
	}
	// per-job log
	cronLog, ok := nas.FS.Read(core.CronLogPath)
	if !ok || !strings.Contains(string(cronLog), "systemctl restart nginx") {
		t.Fatalf("no job log entry for the run:\n%s", string(cronLog))
	}
	// and the player-facing command sees the same thing
	out := run(t, w, nas, "root", "logread cron")
	if !strings.Contains(out, "systemctl restart nginx") {
		t.Fatalf("logread does not show the scheduled run:\n%s", out)
	}
}

// A failing job must be diagnosable: the reason, the exit code and the
// command all in the device's log, and the output in the job log.
func TestCronFailureIsVisibleInLogread(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]

	cronInstall(t, w, router, "root", "/root/mycrontab",
		"* * * * * grep resolv-file /var/run/dnsmasq/resolv.conf\n")

	e := entryFor(w, "router-alex", "root", "grep resolv-file")
	if e == nil {
		t.Fatal("job not armed")
	}
	tickTimes(t, w, 2)
	if e.Runs == 0 {
		t.Fatalf("job never ran (sim=%s)", w.Sim)
	}
	if e.LastRC == 0 {
		t.Fatal("a grep that matched nothing must fail the job, as it does in a real shell")
	}
	out := run(t, w, router, "root", "logread cron")
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("the failure is not in the log:\n%s", out)
	}
	if !strings.Contains(out, "No such file or directory") {
		t.Fatalf("the log must name the real reason:\n%s", out)
	}
	jobLog, _ := router.FS.Read(core.CronLogPath)
	if !strings.Contains(string(jobLog), "exited with status") {
		t.Fatalf("job log must record the exit status:\n%s", string(jobLog))
	}
}

// The world ships real scheduled work, in real spool files, and the household
// probe really fails while the planted DNS fault is active — the scheduler is
// how a player discovers the fault, and repairing DNS really fixes the job.
func TestSeededCronJobsAreRealAndDiagnosable(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	nas := w.Devices["nas-alex"]
	asst := w.Devices["asst-alex"]
	pc := w.Devices["pc-alex"]

	// every device's daemon is a real unit with real state
	for _, d := range []*core.Device{router, nas, asst, pc} {
		if d.CronDaemon() == nil {
			t.Fatalf("%s has no cron daemon unit at all", d.Hostname)
		}
		if !d.CronDaemonRunning() {
			t.Fatalf("%s: the scheduler should be running at world start (%s)", d.Hostname, d.CronStatus())
		}
		if !strings.Contains(d.CronStatus(), "job(s) armed") {
			t.Fatalf("%s: the status line must report the armed jobs: %q", d.Hostname, d.CronStatus())
		}
	}
	// a BusyBox CPE spells it crond, a Debian box spells it cron
	if router.CronDaemon().Name != "crond" {
		t.Fatalf("the BusyBox router should run crond, not %s", router.CronDaemon().Name)
	}
	if nas.CronDaemon().Name != "cron" {
		t.Fatalf("the Debian NAS should run cron, not %s", nas.CronDaemon().Name)
	}

	// the crontabs are real files the player can read and edit
	out := run(t, w, nas, "root", "crontab -l")
	if !strings.Contains(out, "nightly-mirror.log") {
		t.Fatalf("the NAS should ship a nightly mirror sync:\n%s", out)
	}
	out = run(t, w, pc, "alex", "crontab -l")
	if !strings.Contains(out, "mirror.neohome.example") {
		t.Fatalf("the PC should ship an hourly resolver probe:\n%s", out)
	}
	if _, ok := nas.FS.Read(core.CrontabPath("root")); !ok {
		t.Fatalf("the crontab must be a real file at %s", core.CrontabPath("root"))
	}

	// the hourly probe really runs, and really fails: DNS is broken at world
	// start, so this is the scheduler handing the player the first clue.
	probe := entryFor(w, "pc-alex", "alex", "upstream-probe")
	if probe == nil {
		t.Fatal("the planted probe job is missing")
	}
	if !w.FaultDNSActive() {
		t.Fatal("setup: the planted DNS fault should still be active")
	}
	tickUntil(t, w, probe.Next, 200) // at most one game hour away
	w.Tick()                         // this tick crosses the top of the hour
	if probe.Runs != 1 {
		t.Fatalf("the hourly probe did not fire on schedule (runs=%d, sim=%s, next=%s)",
			probe.Runs, w.Sim, probe.Next)
	}
	if probe.LastRC == 0 {
		t.Fatal("the probe should fail while the LAN cannot resolve")
	}
	logOut := run(t, w, pc, "alex", "logread cron")
	if !strings.Contains(logOut, "could not resolve") {
		t.Fatalf("the failure must trace to the real cause (broken resolver):\n%s", logOut)
	}
	// the job log keeps the command's own error text for the post-mortem
	jobLog, _ := pc.FS.Read(core.CronLogPath)
	if !strings.Contains(string(jobLog), "exited with status 1") {
		t.Fatalf("the job log must record the failure:\n%s", string(jobLog))
	}

	// and the failure is causally tied to the fault: fix DNS and the next run
	// of the very same job succeeds and writes its real output file
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	router.FS.Write("/etc/dnsmasq.conf",
		strings.Replace(string(data), "/var/run/dnsmasq/resolv.conf", "/etc/dnsmasq.upstream", 1),
		0644, "root", "root")
	if _, err := router.RestartService("dnsmasq"); err != nil {
		t.Fatalf("restart dnsmasq: %v", err)
	}
	if w.FaultDNSActive() {
		t.Fatal("setup: the DNS fault should be fixed now")
	}
	tickUntil(t, w, probe.Next, 200)
	w.Tick()
	if probe.Runs != 2 {
		t.Fatalf("the probe should have run twice by now (runs=%d)", probe.Runs)
	}
	if probe.LastRC != 0 {
		t.Fatalf("after the DNS repair the same job must succeed, got rc=%d", probe.LastRC)
	}
	if _, ok := pc.FS.Read("/home/alex/upstream-probe.log"); !ok {
		t.Fatal("a successful probe must leave its real output file")
	}
}

// ---- (c) the daemon really gates execution -------------------------------

// Stopping crond must genuinely stop scheduled execution; starting it again
// must genuinely resume — both are world state, observable through the service
// builtins and through the jobs.
func TestStoppingCrondStopsExecution(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	unit := pc.CronDaemon().Name

	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"* * * * * cp /etc/hostname /home/alex/cron-proof.txt\n")
	e := entryFor(w, "pc-alex", "alex", "cron-proof.txt")
	if e == nil {
		t.Fatal("job not armed")
	}

	// the daemon runs, so a job fires
	armed := e.Next
	tickUntil(t, w, armed, 10)
	w.Tick()
	if e.Runs != 1 {
		t.Fatalf("setup: the job should have fired once (runs=%d)", e.Runs)
	}

	// stop the daemon through the ordinary service path
	out := run(t, w, pc, "alex", "service "+unit+" stop")
	if !strings.Contains(out, "stopped") {
		t.Fatalf("stopping the scheduler failed:\n%s", out)
	}
	if pc.CronDaemonRunning() {
		t.Fatal("the daemon must be stopped after `service ... stop`")
	}
	if runProc(t, pc, unit) {
		t.Fatalf("a stopped daemon must not leave a %s process behind", unit)
	}

	// now cross several scheduled minutes: nothing may run
	runsAtStop := e.Runs
	// the log must not claim a new run while the daemon is stopped
	logBefore := run(t, w, pc, "alex", "logread cron")
	tickTimes(t, w, 8)
	if e.Runs != runsAtStop {
		t.Fatalf("a job ran with the daemon stopped: %d -> %d", runsAtStop, e.Runs)
	}
	if _, ok := pc.FS.Read("/home/alex/cron-proof.txt"); !ok {
		t.Fatal("setup: proof file should exist from the first run")
	}
	if after := run(t, w, pc, "alex", "logread cron"); after != logBefore {
		t.Fatalf("nothing may be logged as a run while the daemon is stopped:\nbefore:\n%s\nafter:\n%s", logBefore, after)
	}

	// start it again: the next scheduled minute fires for real
	out = run(t, w, pc, "alex", "service "+unit+" start")
	if !strings.Contains(out, "started") {
		t.Fatalf("starting the scheduler failed:\n%s", out)
	}
	if !pc.CronDaemonRunning() {
		t.Fatal("the daemon must be running after `service ... start`")
	}
	if runProc(t, pc, unit) == false {
		t.Fatalf("a running daemon must have a real process")
	}
	// re-arming must not stampede: the backlog of missed minutes is skipped
	if e.Runs != runsAtStop {
		t.Fatalf("restarting the daemon must not replay missed runs: %d -> %d", runsAtStop, e.Runs)
	}
	tickTimes(t, w, 4)
	if e.Runs <= runsAtStop {
		t.Fatalf("after restarting the daemon the job must fire again (runs=%d)", e.Runs)
	}
	if !strings.Contains(run(t, w, pc, "alex", "crontab -l"), "cron-proof.txt") {
		t.Fatal("the crontab should still be installed")
	}
}

// runProc reports whether the device has a live process for a service name.
func runProc(t *testing.T, d *core.Device, name string) bool {
	t.Helper()
	for _, p := range d.Procs {
		if p.Name == name && p.Svc == name {
			return true
		}
	}
	return false
}

// ---- (d) once-per-minute, no double fire ---------------------------------

// A per-minute job fires once per minute of world time — never twice for the
// same minute, even though a tick is 30 seconds.
func TestCronDoesNotDoubleFireInOneMinute(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"* * * * * cp /etc/hostname /home/alex/cron-proof.txt\n")
	e := entryFor(w, "pc-alex", "alex", "cron-proof.txt")
	if e == nil {
		t.Fatal("job not armed")
	}

	minute := func() string { return w.Sim.Format("15:04") }
	firedIn := map[string]int{}
	lastRuns := 0
	for i := 0; i < 10; i++ { // 5 simulated minutes
		w.Tick()
		if e.Runs > lastRuns {
			firedIn[minute()] += e.Runs - lastRuns
		}
		lastRuns = e.Runs
	}
	if e.Runs < 4 {
		t.Fatalf("a per-minute job should fire once per minute, got %d runs in 5 minutes (sim=%s)", e.Runs, w.Sim)
	}
	for m, n := range firedIn {
		if n != 1 {
			t.Fatalf("minute %s fired %d times — the schedule double-fired", m, n)
		}
	}
	if e.Runs != 5 {
		t.Fatalf("expected exactly 5 runs over 5 simulated minutes, got %d", e.Runs)
	}
}

// ---- the crontab command is a real editor over a real file ---------------

func TestCrontabInstallListRemove(t *testing.T) {
	w := core.NewWorld()
	nas := w.Devices["nas-alex"]

	// a bad schedule is refused with its line number, and nothing is installed
	nas.FS.Write("/tmp/badcron", "0 3 * * * ok-job\n99 * * * * bad-job\n", 0644, "alex", "alex")
	out := run(t, w, nas, "alex", "crontab /tmp/badcron")
	if !strings.Contains(out, "line 2") || !strings.Contains(out, "nothing changed") {
		t.Fatalf("a bad entry must be refused with its line number:\n%s", out)
	}
	if _, ok := nas.ReadCrontab("alex"); ok {
		t.Fatal("a refused crontab must not be installed")
	}
	if entryFor(w, "nas-alex", "alex", "ok-job") != nil {
		t.Fatal("a refused crontab must not arm any job")
	}

	// install a good one
	nas.FS.Write("/tmp/goodcron", "0 5 * * * systemctl restart nginx\n", 0644, "alex", "alex")
	run(t, w, nas, "alex", "crontab /tmp/goodcron")
	if entryFor(w, "nas-alex", "alex", "systemctl restart nginx") == nil {
		t.Fatal("an installed crontab must arm its job")
	}
	if text, ok := nas.ReadCrontab("alex"); !ok || !strings.Contains(text, "systemctl restart nginx") {
		t.Fatalf("the crontab must live in the real spool file: %q", text)
	}
	if p := core.CrontabPath("alex"); p != "/var/spool/cron/crontabs/alex" {
		t.Fatalf("unexpected spool path %s", p)
	}
	out = run(t, w, nas, "alex", "crontab -l")
	if !strings.Contains(out, "systemctl restart nginx") {
		t.Fatalf("crontab -l must show the real file:\n%s", out)
	}

	// comments, env lines and the '-' overlap guard are handled
	nas.FS.Write("/tmp/richcron", "# a comment\nSHELL=/bin/sh\nMAILTO=alex\n"+
		"0 5 * * * systemctl restart nginx\n", 0644, "alex", "alex")
	out = run(t, w, nas, "alex", "crontab /tmp/richcron")
	if strings.Contains(out, "bad entries") {
		t.Fatalf("comments and env lines must be accepted:\n%s", out)
	}

	// removing it really disarms the job
	out = run(t, w, nas, "alex", "crontab -r")
	if !strings.Contains(out, "removed") {
		t.Fatalf("crontab -r failed:\n%s", out)
	}
	if _, ok := nas.ReadCrontab("alex"); ok {
		t.Fatal("crontab -r must delete the spool file")
	}
	if entryFor(w, "nas-alex", "alex", "systemctl restart nginx") != nil {
		t.Fatal("a removed crontab must disarm its jobs")
	}
	out = run(t, w, nas, "alex", "crontab -l")
	if !strings.Contains(out, "no crontab for alex") {
		t.Fatalf("listing a removed crontab must be honest:\n%s", out)
	}

	// a non-root user may not edit someone else's crontab
	out = run(t, w, nas, "alex", "crontab -u root /tmp/goodcron")
	if !strings.Contains(out, "not allowed") {
		t.Fatalf("crontab -u must be permission-checked:\n%s", out)
	}
}

// ---- crond lifecycle through the real service builtins -------------------

func TestCrondServiceLifecycle(t *testing.T) {
	w := core.NewWorld()
	asst := w.Devices["asst-alex"]
	unit := asst.CronDaemon().Name

	if !asst.CronDaemonRunning() {
		t.Fatal("the assistant node should boot with its scheduler running")
	}
	out := run(t, w, asst, "root", "systemctl status "+unit)
	if !strings.Contains(out, "running") {
		t.Fatalf("systemctl status should report the scheduler:\n%s", out)
	}
	out = run(t, w, asst, "root", "crond")
	if !strings.Contains(out, "already running") {
		t.Fatalf("crond on a running daemon:\n%s", out)
	}

	out = run(t, w, asst, "root", "systemctl stop "+unit)
	if !strings.Contains(out, "stopped") {
		t.Fatalf("stop failed:\n%s", out)
	}
	if asst.CronDaemonRunning() {
		t.Fatal("systemctl stop must really stop the scheduler")
	}
	// installing a crontab while the daemon is dead must say so
	out = run(t, w, asst, "root", "crontab /home/assistant/../../tmp/x")
	if !strings.Contains(out, "No such file or directory") {
		t.Fatalf("installing a missing file must fail honestly:\n%s", out)
	}
	asst.FS.Write("/tmp/acron", "* * * * * echo hi\n", 0644, "root", "root")
	out = run(t, w, asst, "root", "crontab /tmp/acron")
	if !strings.Contains(out, "do not fire") {
		t.Fatalf("installing on a box with a dead scheduler must warn:\n%s", out)
	}

	// the applet starts it again
	out = run(t, w, asst, "root", "crond")
	if !strings.Contains(out, "started") {
		t.Fatalf("crond should start the scheduler:\n%s", out)
	}
	if !asst.CronDaemonRunning() {
		t.Fatal("crond must leave the unit running")
	}
	out = run(t, w, asst, "root", "logread cron")
	if !strings.Contains(out, "loaded") {
		t.Fatalf("the daemon should log the spool it loaded:\n%s", out)
	}
}

// A job's exit status is the real one, so "no such command" (127), "the
// command failed" (1) and "it worked" (0) stay distinguishable in the log —
// which is what makes a broken crontab diagnosable.
func TestCronJobExitCodesAreReal(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"* * * * * definitelynotacommand\n"+
			"* * * * * grep nosuchpattern /etc/hosts\n"+
			"* * * * * hostname\n")

	missing := entryFor(w, "pc-alex", "alex", "definitelynotacommand")
	failing := entryFor(w, "pc-alex", "alex", "grep nosuchpattern")
	working := entryFor(w, "pc-alex", "alex", "hostname")
	if missing == nil || failing == nil || working == nil {
		t.Fatal("all three jobs must be armed")
	}
	if missing.Next != failing.Next || failing.Next != working.Next {
		t.Fatalf("identical schedules must arm the same minute: %s / %s / %s",
			missing.Next, failing.Next, working.Next)
	}
	tickUntil(t, w, missing.Next, 10)
	w.Tick()

	if missing.Runs != 1 || missing.LastRC != 127 {
		t.Fatalf("a missing command must fail the job with 127 (runs=%d rc=%d)", missing.Runs, missing.LastRC)
	}
	if failing.Runs != 1 || failing.LastRC != 1 {
		t.Fatalf("a command that matched nothing must fail with 1 (runs=%d rc=%d)", failing.Runs, failing.LastRC)
	}
	if working.Runs != 1 || working.LastRC != 0 {
		t.Fatalf("a working job must report 0 (runs=%d rc=%d)", working.Runs, working.LastRC)
	}
	out := run(t, w, pc, "alex", "logread cron")
	for _, want := range []string{"exit 127", "exit 1", "exit 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the log must record %q:\n%s", want, out)
		}
	}
}

// A crontab survives a save/load round trip and keeps running afterwards: the
// world is persistent, so the schedule is part of the state, not of the process.
func TestCronSurvivesSaveLoad(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	cronInstall(t, w, pc, "alex", "/home/alex/mycron",
		"* * * * * cp /etc/hostname /home/alex/cron-proof.txt\n"+
			"-*/5 * * * * cp /etc/hostname /home/alex/guarded.txt\n")
	if entryFor(w, "pc-alex", "alex", "guarded.txt") == nil {
		t.Fatal("a '-' guarded entry must still be parsed and visible")
	}
	if guarded := entryFor(w, "pc-alex", "alex", "guarded.txt"); guarded.Schedule() == nil {
		t.Fatal("a '-' entry is a concurrency guard, not a disable: it stays armed")
	}
	if _, ok := pc.FS.Read("/home/alex/mycron"); !ok {
		t.Fatal("setup: the crontab file must exist")
	}

	path := t.TempDir() + "/world.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	pc2 := w2.Devices["pc-alex"]
	if pc2 == nil || pc2.CronDaemon() == nil || !pc2.CronDaemonRunning() {
		t.Fatal("the scheduler unit must survive the round trip")
	}
	if _, ok := pc2.ReadCrontab("alex"); !ok {
		t.Fatal("the crontab spool file must survive the round trip")
	}
	e := entryFor(w2, "pc-alex", "alex", "cron-proof.txt")
	if e == nil {
		t.Fatal("the armed job must survive the round trip")
	}
	if e.Schedule() == nil {
		t.Fatal("the schedule must be reparsable after a load (gob drops the cache)")
	}
	// and it still fires against the reloaded world's clock. The first tick
	// after a load is the daemon's first observation (gob does not carry the
	// "already seen running" set), so it re-arms the job from now, exactly as
	// a crond that just booted would.
	w2.Tick()
	tickUntil(t, w2, e.Next, 10)
	w2.Tick()
	if e.Runs == 0 {
		t.Fatalf("a reloaded job must still fire on schedule (sim=%s next=%s)", w2.Sim, e.Next)
	}
	if _, ok := pc2.FS.Read("/home/alex/cron-proof.txt"); !ok {
		t.Fatal("the reloaded job must really do its work")
	}
}

// A device with no scheduler at all must say so instead of accepting a
// crontab that would silently never run.
func TestCronIsHonestWithoutADaemon(t *testing.T) {
	w := core.NewWorld()
	vps, _, err := w.ProvisionVPS("alex", "nano-1", "cronless")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if vps.CronDaemon() != nil {
		t.Fatal("a bare VPS image should ship no cron unit")
	}
	if vps.CronDaemonRunning() {
		t.Fatal("no unit means no running scheduler")
	}
	vps.FS.Write("/tmp/c", "* * * * * hostname\n", 0644, "deploy", "deploy")
	out := run(t, w, vps, "deploy", "crontab /tmp/c")
	if !strings.Contains(out, "no cron daemon") {
		t.Fatalf("a crontab on a scheduler-less box must say so:\n%s", out)
	}
	if _, ok := vps.ReadCrontab("deploy"); ok {
		t.Fatal("nothing may be installed where no scheduler can run it")
	}
	out = run(t, w, vps, "deploy", "crond")
	if !strings.Contains(out, "no cron daemon") {
		t.Fatalf("the crond applet must be honest:\n%s", out)
	}
}

// A dark machine runs no jobs: cutting the house power must silence the
// scheduler on the dead box (and only there), and restoring power must bring
// the jobs back without a backlog stampede. Before the Powered gate, a
// power-cut PC kept running its crontab in the dark.
func TestCronDoesNotFireOnADarkMachine(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	cronInstall(t, w, pc, "alex", "/home/alex/darkcron",
		"* * * * * cp /etc/hostname /home/alex/dark-proof.txt\n")
	e := entryFor(w, "pc-alex", "alex", "dark-proof.txt")
	if e == nil {
		t.Fatal("the installed crontab was not armed by the scheduler")
	}

	// cut the house power, then tick past the armed minute: nothing fires
	w.CutPower("alex")
	if pc.Powered() {
		t.Fatal("setup: the pc should be dark after the power cut")
	}
	tickUntil(t, w, e.Next, 10)
	w.Tick()
	w.Tick()
	if e.Runs != 0 {
		t.Fatalf("a dark machine must run no jobs, got %d run(s)", e.Runs)
	}
	if _, ok := pc.FS.Read("/home/alex/dark-proof.txt"); ok {
		t.Fatal("world state changed while the machine was dark")
	}

	// restore power: the daemon comes back up and re-arms from now — the
	// missed run while dark is missed, exactly like a real outage (no
	// backlog stampede). The next minute must fire exactly once.
	w.RestorePower("alex")
	if !pc.Powered() {
		t.Fatal("setup: the pc should be back after restore")
	}
	e = entryFor(w, "pc-alex", "alex", "dark-proof.txt")
	if e == nil {
		t.Fatal("the entry should still be armed after power is back")
	}
	// the first tick after restore re-arms the daemon from now instead of
	// firing the missed run: no backlog stampede
	w.Tick()
	if e.Runs != 0 {
		t.Fatalf("the missed run must stay missed, got %d run(s)", e.Runs)
	}
	if !e.Next.After(w.Sim) {
		t.Fatalf("the daemon should have re-armed from now, next=%s sim=%s", e.Next, w.Sim)
	}
	armed := e.Next
	tickUntil(t, w, armed, 10)
	w.Tick()
	if e.Runs != 1 {
		t.Fatalf("the job should fire once on its next minute, got %d run(s)", e.Runs)
	}
	if _, ok := pc.FS.Read("/home/alex/dark-proof.txt"); !ok {
		t.Fatal("the job's effect is missing after power is back")
	}
}
