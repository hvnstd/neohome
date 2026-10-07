package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §36 — wear-driven disk death: 老硬盘 + 高负载 + 长期运行.
//
// The chain is thresholds on real drivers, never a dice roll: power-on
// ticks, lifetime bytes written and saturated ticks accumulate while the
// machine runs, DiskHealth derives WARNING/FAILED from them, FAILED latches
// writes off with EIO, and fsck buys a grace period without healing the wear.
// Tests build the conjunction by presetting the counters the way the
// intrusion tests preset a weak password — state is state, however it got
// there. Each test walks a happy path, a boundary and a recovery.

func wearSetup(t *testing.T) (*core.World, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	nas := w.Devices["nas-alex"]
	if nas == nil {
		t.Fatal("no nas in the world")
	}
	return w, nas
}

func TestFreshDiskPassesSmart(t *testing.T) {
	w, nas := wearSetup(t)

	if st, _ := nas.DiskHealth(); st != "PASSED" {
		t.Fatalf("a fresh disk must pass, got %s", st)
	}
	if out := run(t, w, nas, "root", "smartctl -a /dev/sda"); !strings.Contains(out, "PASSED") {
		t.Fatalf("smartctl should report PASSED:\n%s", out)
	}
	if out := run(t, w, nas, "root", "smartctl -H /dev/sda"); !strings.Contains(out, "PASSED") {
		t.Fatalf("smartctl -H should report PASSED:\n%s", out)
	}
	if out := run(t, w, nas, "root", "smartctl -a /dev/sdb"); !strings.Contains(out, "No such device") {
		t.Fatalf("a missing disk must be refused honestly, got:\n%s", out)
	}
	if out := run(t, w, nas, "root", "fsck"); !strings.Contains(out, "clean") {
		t.Fatalf("fsck on a healthy disk should report clean, got:\n%s", out)
	}
	// and the lifetime counter agrees with uptime on a fresh world: the seed
	// booted 36h ago, which is 4320 ticks of 30 sim-seconds
	if got := nas.Resources().PowerOnTicks; got < 4300 || got > 4400 {
		t.Fatalf("power-on should start at the boot offset (4320 ticks), got %d", got)
	}
}

func TestWearWarnsOnceWithoutStoppingWrites(t *testing.T) {
	w, nas := wearSetup(t)
	nas.Resources().PowerOnTicks = core.DiskAgeWarnTicks - 5

	for i := 0; i < 10; i++ {
		w.Tick()
	}
	st, why := nas.DiskHealth()
	if st != "WARNING" {
		t.Fatalf("an old disk must warn, got %s (%s)", st, why)
	}
	if !strings.Contains(why, "power-on") {
		t.Fatalf("the reason must name the driver, got %q", why)
	}
	// one-shot: ten ticks later there is still exactly one warning line
	for i := 0; i < 10; i++ {
		w.Tick()
	}
	data, _ := nas.FS.Read("/var/log/syslog")
	if n := strings.Count(string(data), "SMART warning"); n != 1 {
		t.Fatalf("the warning must be written once, got %d", n)
	}
	// a warning changes nothing: writes still land, the disk is not latched
	if out := run(t, w, nas, "root", "echo hello > /tmp/still-fine.txt"); strings.Contains(out, "error") {
		t.Fatalf("a warned disk must still take writes, got:\n%s", out)
	}
	if nas.DiskFailed() {
		t.Fatal("a warning must not latch FAILED")
	}
	if out := run(t, w, nas, "root", "smartctl -H /dev/sda"); !strings.Contains(out, "WARNING") {
		t.Fatalf("smartctl must show the warning:\n%s", out)
	}
	if out := run(t, w, nas, "root", "df"); !strings.Contains(out, "WARNING") {
		t.Fatalf("df must show the warning:\n%s", out)
	}
}

func TestFailedDiskRefusesWritesWithEIO(t *testing.T) {
	w, nas := wearSetup(t)
	nas.Resources().PowerOnTicks = core.DiskAgeFailTicks
	w.Tick()

	if !nas.DiskFailed() {
		t.Fatal("an ancient disk must latch FAILED within a tick")
	}
	st, why := nas.DiskHealth()
	if st != "FAILED" || !strings.Contains(why, "power-on") {
		t.Fatalf("the health must name the driver, got %s (%s)", st, why)
	}
	// the single gate refuses with EIO, not ENOSPC: removing files is not
	// the fix and the error must not claim it is
	if out := run(t, w, nas, "root", "echo hello > /tmp/nope.txt"); !strings.Contains(strings.ToLower(out), "input/output error") {
		t.Fatalf("writes must fail with EIO, got:\n%s", out)
	}
	if nas.FS.Exists("/tmp/nope.txt") {
		t.Fatal("a refused write must not have landed on disk")
	}
	if out := run(t, w, nas, "root", "dd if=/dev/zero of=/tmp/big bs=1M count=1"); !strings.Contains(strings.ToLower(out), "input/output error") {
		t.Fatalf("dd must report EIO, got:\n%s", out)
	}
	if out := run(t, w, nas, "root", "df"); !strings.Contains(out, "disk FAILED") {
		t.Fatalf("df must name the failure:\n%s", out)
	}
	if out := run(t, w, nas, "root", "dmesg | tail -3"); !strings.Contains(out, "I/O error") {
		t.Fatalf("the kernel ring must carry the failure:\n%s", out)
	}
	if out := run(t, w, nas, "root", "smartctl -H /dev/sda"); !strings.Contains(out, "FAILED") {
		t.Fatalf("smartctl must report FAILED:\n%s", out)
	}
	// reads still work: the data is not gone, which is what makes backup
	// the real fix and fsck worth running
	if _, ok := nas.FS.Read("/srv/media/index.txt"); !ok {
		t.Fatal("reads must still work on a failed disk")
	}
}

func TestOverloadFailsTheDiskToo(t *testing.T) {
	w, nas := wearSetup(t)
	nas.Resources().HotTicks = core.DiskHotFailTicks
	w.Tick()

	if st, why := nas.DiskHealth(); st != "FAILED" || !strings.Contains(why, "overloaded") {
		t.Fatalf("sustained overload must fail the disk, got %s (%s)", st, why)
	}
	if !nas.DiskFailed() {
		t.Fatal("the overload failure must latch")
	}
}

func TestInstallRefusesOnFailedDisk(t *testing.T) {
	w, _ := wearSetup(t)
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("setup: apt update must work while healthy: %s", out)
	}
	pc.Resources().PowerOnTicks = core.DiskAgeFailTicks
	w.Tick()

	out := run(t, w, pc, "root", "apt install curl")
	if !strings.Contains(out, "input/output error") {
		t.Fatalf("an install on a failed disk must report EIO, got:\n%s", out)
	}
	if pc.Installed["curl"] != nil {
		t.Fatal("a refused install must not half-install anything")
	}
}

func TestFsckRepairsButWearRemains(t *testing.T) {
	w, nas := wearSetup(t)
	nas.Resources().PowerOnTicks = core.DiskAgeFailTicks
	w.Tick()
	if !nas.DiskFailed() {
		t.Fatal("setup: the disk should be failed")
	}

	// the permission boundary: repairing a disk is root's job
	if out := run(t, w, nas, "alex", "fsck"); !strings.Contains(out, "must be root") {
		t.Fatalf("fsck must refuse non-root, got:\n%s", out)
	}
	if !nas.DiskFailed() {
		t.Fatal("a refused fsck must not have repaired anything")
	}

	// recovery: the filesystem answers writes again
	if out := run(t, w, nas, "root", "fsck"); !strings.Contains(out, "FILE SYSTEM WAS MODIFIED") {
		t.Fatalf("fsck should repair, got:\n%s", out)
	}
	if nas.DiskFailed() {
		t.Fatal("the latch must be cleared by fsck")
	}
	if out := run(t, w, nas, "root", "echo back > /tmp/again.txt"); strings.Contains(out, "error") || !nas.FS.Exists("/tmp/again.txt") {
		t.Fatalf("writes must work after fsck, got:\n%s", out)
	}
	// but the wear never healed: the health still fails, so the disk will
	// fail again on schedule — fsck bought time, not a new disk
	if st, _ := nas.DiskHealth(); st != "FAILED" {
		t.Fatalf("health must still be FAILED after repair, got %s", st)
	}
	for i := 0; i < 130; i++ {
		w.Tick()
	}
	if !nas.DiskFailed() {
		t.Fatal("past the grace period the same wear must latch FAILED again")
	}
	if out := run(t, w, nas, "root", "echo late > /tmp/late.txt"); !strings.Contains(strings.ToLower(out), "input/output error") {
		t.Fatalf("writes must fail again after the grace period, got:\n%s", out)
	}
}

func TestWearSurvivesSave(t *testing.T) {
	w, nas := wearSetup(t)
	nas.Resources().PowerOnTicks = core.DiskAgeFailTicks
	nas.Resources().DiskWrittenB = 123456789
	w.Tick()
	if !nas.DiskFailed() {
		t.Fatal("setup: the disk should be failed")
	}

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := back.Devices["nas-alex"].Resources()
	if r.PowerOnTicks < core.DiskAgeFailTicks {
		t.Fatal("the power-on counter must survive the save")
	}
	if r.DiskWrittenB != 123456789 {
		t.Fatalf("the lifetime write counter must survive the save, got %d", r.DiskWrittenB)
	}
	if !back.Devices["nas-alex"].DiskFailed() {
		t.Fatal("the FAILED latch must survive the save")
	}
}
