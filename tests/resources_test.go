package tests

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"neohome/internal/core"
)

// §17 系统状态 / 资源管理: resources really affect the machine, and the monitoring
// commands read the same state the consequences come from. Each test walks a
// happy path, a boundary and a recovery.

// The printer is the smallest machine the household owns that people notice:
// one core, 256 MiB of RAM, and a spooler whose pace is visible in `lpstat`.
// A load on it is therefore both measurable and consequential.
func TestCPUPressureIsSharedAndPasses(t *testing.T) {
	w := core.NewWorld()
	prn := w.Devices["prn-alex"]
	pc := w.Devices["pc-alex"]

	idle := run(t, w, prn, "root", "uptime")
	if !strings.Contains(idle, "load average: 0.00, 0.00, 0.00") {
		t.Fatalf("an idle machine must report no load:\n%s", idle)
	}
	if out := run(t, w, prn, "root", "stress --cpu 4 --timeout 40"); !strings.Contains(out, "dispatching hogs: 4 cpu") {
		t.Fatalf("stress must start real workers:\n%s", out)
	}
	// one core, four workers: each of them gets a quarter and says so
	for i := 0; i < 6; i++ {
		w.Tick()
	}
	htop := run(t, w, prn, "root", "htop")
	if !strings.Contains(htop, "WANT%") {
		t.Fatalf("htop must show what processes asked for:\n%s", htop)
	}
	if got := prn.Resources().Share; got != 0.25 {
		t.Fatalf("four workers on one core must share a quarter each, got %.2f", got)
	}
	if strings.Contains(htop, "50.0%") {
		t.Fatalf("htop must not claim a starved process got half the core:\n%s", htop)
	}
	// the load average is the machine's own, and it really climbed
	load := prn.Resources().Load1
	if load < 3 {
		t.Fatalf("four workers on one core must show a load near 4, got %.2f", load)
	}
	if uptime := run(t, w, prn, "root", "uptime"); !strings.Contains(uptime, "load average: 3.") {
		t.Fatalf("uptime must print the measured load average:\n%s", uptime)
	}

	// a real consumer slows down: one sheet takes two ticks when idle, and it
	// is still not printed after six ticks of a quarter of the CPU
	prn.Printer.Credit = 0 // the spooler was idle when the job arrived
	run(t, w, pc, "alex", "lp -t Notes /home/alex/notes.txt")
	for i := 0; i < 6; i++ {
		w.Tick()
	}
	if out := run(t, w, prn, "root", "lpstat"); !strings.Contains(out, "printed: 0 sheet(s)") {
		t.Fatalf("a starved printer should not have finished a sheet yet:\n%s", out)
	}

	// the boundary: the process table is finite, and a fork past it is refused
	out := run(t, w, prn, "root", "stress --cpu 40 --timeout 40")
	if !strings.Contains(out, "Resource temporarily unavailable") || !strings.Contains(out, "process limit") {
		t.Fatalf("the process limit must refuse the fork with the kernel's own reason:\n%s", out)
	}
	if prn.Resources().ForksRefused == 0 {
		t.Fatal("the refusal must be counted")
	}
	if out := run(t, w, prn, "root", "vmstat"); !strings.Contains(out, "refused forks:") {
		t.Fatalf("vmstat must report the refusals:\n%s", out)
	}

	// recovery: the load goes away and the machine is itself again
	if out := run(t, w, prn, "root", "pkill stress"); strings.Contains(out, "no process matches") {
		t.Fatalf("pkill must find the load:\n%s", out)
	}
	if n := stressProcs(prn); n != 0 {
		t.Fatalf("pkill left %d load process(es) behind", n)
	}
	for i := 0; i < 12; i++ {
		w.Tick()
	}
	if load := prn.Resources().Load1; load > 0.2 {
		t.Fatalf("the load average must decay once the work is gone, got %.2f", load)
	}
	if prn.Resources().Share != 1 {
		t.Fatalf("an idle machine must not be sharing anything, got %.2f", prn.Resources().Share)
	}
	if out := run(t, w, pc, "alex", "lpstat"); !strings.Contains(out, "no jobs") {
		t.Fatalf("the held job must finish once the machine is free again:\n%s", out)
	}
}

// RAM → swap → OOM, on a machine whose owner never expected to need either.
func TestMemoryPressurePagesThenKillsTheHog(t *testing.T) {
	w := core.NewWorld()
	prn := w.Devices["prn-alex"]

	if out := run(t, w, prn, "root", "stress --vm 1 --vm-bytes 300M --timeout 40"); !strings.Contains(out, "300 MiB resident") {
		t.Fatalf("stress must claim the memory it asked for:\n%s", out)
	}
	if got, want := prn.MemUsed(), 300+24; got < want {
		t.Fatalf("the resident set must be real: %d MiB, expected at least %d", got, want)
	}
	// over RAM, so the kernel pages — and it has room, so it does not kill
	w.Tick()
	if r := prn.Resources(); r.SwapUsedMB == 0 {
		t.Fatalf("the machine is %d MiB over RAM and did not page", prn.MemUsed()-prn.HW.RAMMB)
	}
	if out := run(t, w, prn, "root", "logread"); !strings.Contains(out, "swapped out") {
		t.Fatalf("the kernel must say what it paged:\n%s", out)
	}
	if prn.Resources().OOMCount != 0 {
		t.Fatal("the OOM killer must not fire while swap still has room")
	}
	swapped := prn.Resources().SwapUsedMB

	// a second hog takes it past swap: now something has to die, and it is the
	// biggest resident process — the one the player started
	if out := run(t, w, prn, "root", "stress --vm 1 --vm-bytes 200M --timeout 40"); !strings.Contains(out, "200 MiB resident") {
		t.Fatalf("the second hog must start:\n%s", out)
	}
	w.Tick()
	r := prn.Resources()
	if r.OOMCount == 0 {
		t.Fatalf("with %d MiB used, %d MiB of RAM and %d MiB of swap, something must be killed",
			prn.MemUsed(), prn.HW.RAMMB, prn.SwapTotalMB())
	}
	if r.SwapUsedMB != prn.SwapTotalMB() {
		t.Fatalf("the kill must wait for swap to fill: %d/%d", r.SwapUsedMB, prn.SwapTotalMB())
	}
	log := run(t, w, prn, "root", "logread")
	if !strings.Contains(log, "Out of memory: Killed process") {
		t.Fatalf("the kill must be in the log with the kernel's own words:\n%s", log)
	}
	if !strings.Contains(log, "stress") {
		t.Fatalf("the biggest resident process should be the victim:\n%s", log)
	}
	if idx := strings.Index(log, "swapped out"); idx < 0 || idx > strings.Index(log, "Out of memory") {
		t.Fatalf("swap must be tried before the kill:\n%s", log)
	}
	if out := run(t, w, prn, "root", "vmstat"); !strings.Contains(out, "OOM incidents: 1") {
		t.Fatalf("vmstat must report the incident:\n%s", out)
	}
	// the daemons survived and still work: it was the hog that died
	if svc := prn.Svc("cupsd"); svc == nil || svc.State != "running" {
		t.Fatalf("a hog must not take the machine's own daemon with it: %+v", svc)
	}
	if out := run(t, w, prn, "root", "lpstat"); !strings.Contains(out, "paper:") {
		t.Fatalf("the spooler must still answer after the OOM kill:\n%s", out)
	}

	// recovery: with the pressure gone the kernel pages back in
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if now := prn.Resources().SwapUsedMB; now >= swapped {
		t.Fatalf("swap must start draining once the pressure is gone: %d (was %d)", now, swapped)
	}
	run(t, w, prn, "root", "pkill stress")
	for i := 0; i < 30; i++ {
		w.Tick()
	}
	if r := prn.Resources(); r.SwapUsedMB != 0 {
		t.Fatalf("swap must drain once the memory is free, %d MiB still out", r.SwapUsedMB)
	}
	if out := run(t, w, prn, "root", "free"); !strings.Contains(out, "Swap:") {
		t.Fatalf("free must print its swap row:\n%s", out)
	}
}

// A full filesystem is not a warning: the writes really fail, the log really
// loses lines, and removing the file really fixes it.
func TestDiskFullStopsWritesAndLogsTheGap(t *testing.T) {
	w := core.NewWorld()
	sw := w.Devices["sw-alex"] // 16 MiB of flash — the honest place to run out

	if out := run(t, w, sw, "root", "df"); strings.Contains(out, "warning") {
		t.Fatalf("this machine is not full yet:\n%s", out)
	}
	out := run(t, w, sw, "root", "dd if=/dev/zero of=/tmp/big bs=1M count=64")
	if !strings.Contains(out, "64+0 records in") {
		t.Fatalf("dd must report the records it was asked for:\n%s", out)
	}
	if !strings.Contains(out, "No space left on device") {
		t.Fatalf("dd must fail like dd when the disk fills:\n%s", out)
	}
	if !sw.DiskFull() {
		t.Fatalf("the filesystem should be full now (%d/%d MiB)", sw.FS.DiskUsedMB(), sw.DiskLimitMB())
	}
	// the next tick is when the kernel notices, and its ring buffer keeps the
	// warning even though the filesystem it would be logged to cannot
	w.Tick()
	if out := run(t, w, sw, "root", "dmesg | tail -3"); !strings.Contains(out, "no space left on device") {
		t.Fatalf("the kernel must say the filesystem is full:\n%s", out)
	}

	// a normal write now fails with the filesystem's own error, and nothing
	// is silently lost
	before := sw.FS.DiskUsedMB()
	if out := run(t, w, sw, "root", "echo hello > /tmp/later.txt"); !strings.Contains(out, "no space left on device") {
		t.Fatalf("a write to a full disk must fail honestly:\n%s", out)
	}
	if sw.FS.Exists("/tmp/later.txt") {
		t.Fatal("a refused write must not have landed on disk")
	}
	if out := run(t, w, sw, "root", "df"); !strings.Contains(out, "no space left on device") {
		t.Fatalf("df must warn that writes are failing:\n%s", out)
	}
	if sw.FS.DiskUsedMB() != before {
		t.Fatalf("the refused write changed the disk: %d -> %d", before, sw.FS.DiskUsedMB())
	}

	// §17's third consequence: the log cannot be written either. rsyslog really
	// loses those lines, and really says how many once there is room.
	run(t, w, sw, "root", "switchctl port 3 down")
	run(t, w, sw, "root", "switchctl port 3 up")
	if sw.Resources().LogDropped == 0 {
		t.Fatal("the dropped log lines must be counted")
	}
	log := run(t, w, sw, "root", "logread")
	if strings.Contains(log, "port 3 (laptop) admin down") {
		t.Fatalf("a full disk cannot have logged that:\n%s", log)
	}

	// recovery: remove the file you made, and everything comes back
	if out := run(t, w, sw, "root", "rm /tmp/big"); out != "" {
		t.Fatalf("rm should be quiet: %s", out)
	}
	if sw.DiskFull() {
		t.Fatalf("the disk should have room again (%d/%d MiB)", sw.FS.DiskUsedMB(), sw.DiskLimitMB())
	}
	w.Tick()
	log = run(t, w, sw, "root", "logread")
	if !strings.Contains(log, "space reclaimed on /") {
		t.Fatalf("the kernel must note the recovery:\n%s", log)
	}
	if !strings.Contains(log, "message(s) dropped while / was full") {
		t.Fatalf("rsyslog must account for what it lost:\n%s", log)
	}
	if out := run(t, w, sw, "root", "echo hello > /tmp/later.txt"); strings.Contains(out, "cannot write") || !sw.FS.Exists("/tmp/later.txt") {
		t.Fatalf("writes must work again:\n%s", out)
	}
	if out := run(t, w, sw, "root", "df"); strings.Contains(out, "warning") {
		t.Fatalf("df must stop warning once there is room:\n%s", out)
	}
}

// An install needs room before it needs anything else, and each package manager
// says so in its own voice.
func TestInstallOnAFullDiskFailsWithTheManagersOwnError(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("the machine needs its lists before it can unpack anything:\n%s", out)
	}
	full := pc.FS.DiskUsedMB() // the space the machine is really using
	limit := pc.HW.DiskMB

	// make the machine's disk exactly as large as its contents: the model's
	// disk size is the one knob, so this is a full disk, not a mocked one
	pc.HW.DiskMB = full
	out := run(t, w, pc, "root", "apt install curl")
	// the dependency is unpacked first, which is where a full disk bites
	if !strings.Contains(out, "E: cannot unpack") || !strings.Contains(out, "no space left on device") {
		t.Fatalf("apt must report the filesystem's error in its own voice:\n%s", out)
	}
	if pc.Installed["curl"] != nil || pc.Installed["libc"] != nil {
		t.Fatal("a refused install must not half-install anything")
	}
	if out := run(t, w, pc, "root", "df"); !strings.Contains(out, "no space left on device") {
		t.Fatalf("df must agree that the disk is full:\n%s", out)
	}

	// boundary: a machine with room takes the very same package, from the same
	// cached lists, over the same wire
	pc.HW.DiskMB = limit
	out = run(t, w, pc, "root", "apt install curl")
	if strings.Contains(out, "no space left on device") {
		t.Fatalf("a machine with room should have taken it:\n%s", out)
	}
	if pc.Installed["curl"] == nil {
		t.Fatalf("the install should have succeeded:\n%s", out)
	}
}

// The monitoring commands are not a second source of truth: every number they
// print is read from the same state the consequences come from.
func TestMonitoringCommandsReadTheSameState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "stress --cpu 2 --timeout 40")
	for i := 0; i < 3; i++ {
		w.Tick()
	}
	r := pc.Resources()

	df := run(t, w, pc, "alex", "df")
	if !strings.Contains(df, strconv.Itoa(pc.DiskLimitMB())) {
		t.Fatalf("df must print the real disk size:\n%s", df)
	}
	free := run(t, w, pc, "alex", "free")
	if !strings.Contains(free, strconv.Itoa(pc.MemUsed()*1024)) {
		t.Fatalf("free must print the real memory in use (%d MiB):\n%s", pc.MemUsed(), free)
	}
	htop := run(t, w, pc, "alex", "htop")
	if !strings.Contains(htop, "load average") || !strings.Contains(htop, "Tasks:") {
		t.Fatalf("htop must print the machine's own summary:\n%s", htop)
	}
	if !strings.Contains(htop, strconv.Itoa(len(pc.Procs)+1)) {
		t.Fatalf("htop must count the real process table (%d entries):\n%s", len(pc.Procs), htop)
	}
	if demand := pc.CPUDemand(); !strings.Contains(htop, "Cpu :") || demand < 200 {
		t.Fatalf("htop must show the real CPU demand (%.1f):\n%s", demand, htop)
	}
	vmstat := run(t, w, pc, "alex", "vmstat")
	if !strings.Contains(vmstat, "swpd") || !strings.Contains(vmstat, "load") {
		t.Fatalf("vmstat must print a real sample:\n%s", vmstat)
	}
	if want := fmt.Sprintf("load %.2f", r.Load1); !strings.Contains(vmstat, want) {
		t.Fatalf("vmstat must print the measured load average (%s):\n%s", want, vmstat)
	}

	// and the machine is really busy: two workers on two cores is not
	// starvation, so the share stays 1 and the numbers still move
	if pc.Resources().Share > 1 || pc.Resources().Share <= 0 {
		t.Fatalf("a share must be a fraction, got %.2f", pc.Resources().Share)
	}
	if out := run(t, w, pc, "alex", "pkill stress"); strings.Contains(out, "no process matches") {
		t.Fatalf("the load must be findable:\n%s", out)
	}
	if n := stressProcs(pc); n != 0 {
		t.Fatalf("pkill left %d load process(es) behind", n)
	}
}

// stressProcs counts the running load processes on a device.
func stressProcs(d *core.Device) int {
	n := 0
	for _, p := range d.Procs {
		if p.Kind == "load" {
			n++
		}
	}
	return n
}

// Bulk work takes ticks on a slow machine — the same bytes over a slower link
// and disk finish later, which is what makes "Network bandwidth" and "Disk I/O"
// mean something.
func TestBulkWorkSlowsOnASlowMachine(t *testing.T) {
	w := core.NewWorld()
	quietMirrorCron(t, w)
	mir := w.Devices["mirror"]
	repo := w.Repos["debian"]
	if repo == nil {
		t.Fatal("the debian mirror should be seeded")
	}
	fast := mir.WorkRate()
	if fast < 0.99 {
		t.Fatalf("a gigabit mirror with a fast disk is the reference machine, got %.2f", fast)
	}

	// a mirror with a 200 MB/s disk: two fifths of the reference throughput,
	// so a phase that took one tick takes three
	mir.Resources().DiskMBpsBench = 200
	mir.HW.NetMbps = 1000
	if rate := mir.WorkRate(); rate != 0.4 {
		t.Fatalf("a 200 MB/s disk and a gigabit link should be 0.4 work per tick, got %.2f", rate)
	}
	if err := w.StartMirrorSync(repo); err != nil {
		t.Fatalf("starting the sync: %v", err)
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if repo.SyncPhase == 0 {
		t.Fatalf("the sync finished in fewer ticks than the link allows (%s)", repo.StatusWhy)
	}

	// recovery: give the machine its throughput back and the work catches up
	mir.Resources().DiskMBpsBench = 0 // back to the profile's own 500 MB/s
	for i := 0; i < 8 && repo.SyncPhase > 0; i++ {
		w.Tick()
	}
	if repo.SyncPhase != 0 || repo.Status != "SYNCED" {
		t.Fatalf("the sync should complete once the machine is fast again: %s (%s)", repo.Status, repo.StatusWhy)
	}
}
