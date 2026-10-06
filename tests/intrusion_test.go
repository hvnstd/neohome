package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §36 — an intrusion is a consequence.
//
// The world's own attacker guesses credentials at machines the internet can
// reach. These tests pin what follows a guess that works: artifacts a player
// can find and remove, a persistence line that really decides whether the
// implant comes back, callbacks that are real flows on the far side, and a
// difference between a watched machine and a blind one that comes from the
// machine's own §33 tools.

// vulnerableBox gives a public machine a weak account and no monitoring — the
// combination §36 names as the risk.
func vulnerableBox(t *testing.T, w *core.World, id string) *core.Device {
	t.Helper()
	d := w.Devices[id]
	if d == nil {
		t.Fatalf("no device %s", id)
	}
	if u := d.FindUser("root"); u == nil {
		t.Fatalf("%s has no root account to guess at", id)
	}
	d.FindUser("root").Pass = "toor" // the credential list's own password
	return d
}

func intrusionsOn(w *core.World, id string) []*core.Foothold {
	return w.FootholdsFor(id)
}

func TestWorkingCredentialLeavesArtifacts(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	dst := vulnerableBox(t, w, "le-cyber")

	if ok := core.AttackLogin(w, sc, dst, "root", "toor", "ssh", 22); !ok {
		t.Fatal("the guess should have worked on this machine")
	}
	fs := intrusionsOn(w, dst.ID)
	if len(fs) != 1 {
		t.Fatalf("a working guess should leave exactly one presence, got %d", len(fs))
	}
	f := fs[0]
	if f.Account != "root" || f.Actor != core.ScannerID {
		t.Fatalf("the record should name the credential and the actor: %+v", f)
	}
	// three artifacts, all of them real state a player can look at
	if data, ok := dst.FS.Read(f.File); !ok || len(data) == 0 {
		t.Fatalf("the payload should be on disk at %s", f.File)
	}
	if _, ok := dst.FS.Read(f.Cron); !ok {
		t.Fatalf("the persistence line should be on disk at %s", f.Cron)
	}
	alive := false
	for _, p := range dst.Procs {
		if p.PID == f.PID {
			alive = true
		}
	}
	if !alive {
		t.Fatalf("the implant should be in the process table (pid %d)", f.PID)
	}
	// and the machine's own log carries the intrusion, not just the attempt
	if !strings.Contains(strings.Join(logText(dst), "\n"), f.File) {
		t.Fatalf("the machine's log should carry the execution:\n%s", strings.Join(logText(dst), "\n"))
	}
}

// syslog is the machine's own record: the VFS file the daemon writes.
func logText(d *core.Device) []string {
	data, _ := d.FS.Read("/var/log/syslog")
	return strings.Split(string(data), "\n")
}

func TestPersistenceIsTheCronLine(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	dst := vulnerableBox(t, w, "le-cyber")
	core.AttackLogin(w, sc, dst, "root", "toor", "ssh", 22)
	f := intrusionsOn(w, dst.ID)[0]

	// killing the process by hand does not clean anything: the line brings it
	// back on the machine's own schedule, which is what persistence means
	for i, p := range dst.Procs {
		if p.PID == f.PID {
			dst.Procs = append(dst.Procs[:i], dst.Procs[i+1:]...)
		}
	}
	w.TickCount++
	w.IntrusionTick()
	back := false
	for _, p := range dst.Procs {
		if p.Name == f.Proc {
			back = true
			f.PID = p.PID
		}
	}
	if !back {
		t.Fatal("the implant should come back while its persistence line is there")
	}

	// removing the line ends it, and the record says why
	dst.FS.Remove(f.Cron)
	w.TickCount++
	w.IntrusionTick()
	if !f.Dead {
		t.Fatal("without the persistence line the implant should be gone")
	}
	report := strings.Join(w.IntrusionReport(dst.ID), "\n")
	if !strings.Contains(report, "removed") {
		t.Fatalf("the report should say the presence was removed:\n%s", report)
	}
}

func TestBeaconIsARealCallback(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	dst := vulnerableBox(t, w, "le-cyber")
	core.AttackLogin(w, sc, dst, "root", "toor", "ssh", 22)
	f := intrusionsOn(w, dst.ID)[0]

	// one beacon window, in ticks, with the world clock moved by the test
	for i := 0; i < 8; i++ {
		w.TickCount++
		w.IntrusionTick()
	}
	if f.Beacons == 0 {
		t.Fatal("the implant should have called home by now")
	}
	// the evidence of the callback is on the far side: the attacker's own flows
	seen := 0
	for _, fl := range sc.Sec().Flows {
		if fl.Port == 8443 {
			seen++
		}
	}
	if seen == 0 {
		t.Fatalf("the attacker should hold the callbacks in its own flow record (beacons=%d)", f.Beacons)
	}
}

func TestMonitoringDecidesWhetherAnybodyNotices(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]

	// a blind machine: no auditd, no host monitor, no file baseline — the
	// presence is real and nothing in the world says so
	blind := vulnerableBox(t, w, "le-cyber")
	core.AttackLogin(w, sc, blind, "root", "toor", "ssh", 22)
	fb := intrusionsOn(w, blind.ID)[0]
	if len(fb.Seen) != 0 {
		t.Fatalf("a machine with no sensors should have noticed nothing, got %v", fb.Seen)
	}
	if !strings.Contains(strings.Join(w.IntrusionReport(blind.ID), "\n"), "noticed by:  nothing") {
		t.Fatal("the report should say that nobody noticed")
	}

	// the same machine, watched: the host monitor and the file baseline are
	// what turn the same intrusion into an alert
	watched := vulnerableBox(t, w, "meridian-fs")
	watched.Services["rkhunter"] = &core.Service{Name: "rkhunter", State: "running", Port: 0, Scope: "lan"}
	watched.Services["auditd"] = &core.Service{Name: "auditd", State: "running", Port: 0, Scope: "lan"}
	core.AttackLogin(w, sc, watched, "root", "toor", "ssh", 22)
	fw := intrusionsOn(w, watched.ID)[0]
	if len(fw.Seen) == 0 {
		t.Fatal("the watched machine's own tools should have seen the intrusion")
	}
	joined := strings.Join(fw.Seen, ",")
	if !strings.Contains(joined, "rkhunter") || !strings.Contains(joined, "auditd") {
		t.Fatalf("both sensors should be named, got %v", fw.Seen)
	}
	if len(watched.Alerts()) == 0 {
		t.Fatal("the sensors' findings should be in the machine's own alert ring")
	}
}

func TestUnreachableMachineIsNotOwned(t *testing.T) {
	w := secSetup(t)
	// the household's own PC is behind NAT: the world's scanner cannot even
	// reach it, so no credential guess happens and no presence exists
	pc := vulnerableBox(t, w, "pc-alex")
	targets := w.ScannableTargets()
	for _, id := range targets {
		if id == pc.ID {
			t.Fatal("a NATed household machine must not be in the scanner's target list")
		}
	}
	if len(intrusionsOn(w, pc.ID)) != 0 {
		t.Fatal("a machine the scanner never reached must have no presence")
	}
}

func TestOperatorCleanupRemovesEveryArtifact(t *testing.T) {
	w := secSetup(t)
	sc := w.Devices[core.ScannerID]
	dst := vulnerableBox(t, w, "le-cyber")
	core.AttackLogin(w, sc, dst, "root", "toor", "ssh", 22)
	f := intrusionsOn(w, dst.ID)[0]

	n, err := w.CleanFoothold(dst.ID)
	if err != nil || n != 1 {
		t.Fatalf("clean should remove one presence, got %d, %v", n, err)
	}
	if _, ok := dst.FS.Read(f.File); ok {
		t.Fatal("the payload should be gone")
	}
	if _, ok := dst.FS.Read(f.Cron); ok {
		t.Fatal("the persistence line should be gone")
	}
	for _, p := range dst.Procs {
		if p.PID == f.PID {
			t.Fatal("the process should be gone")
		}
	}
	if !f.Dead {
		t.Fatal("the record should be marked removed")
	}
	if len(w.LiveFootholds()) != 0 {
		t.Fatal("no live presences should remain")
	}
	// and the record survives a save, because a machine that was owned once is
	// worth remembering
	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(back.FootholdsFor(dst.ID)) == 0 {
		t.Fatal("the record of the intrusion should survive the save")
	}
}
