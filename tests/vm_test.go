package tests

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"neohome/internal/core"
)

// A guest must be a REAL device in the world: its own filesystem, users,
// services and addresses. If a guest were bookkeeping rows instead of a device,
// everything below would be a lie.
func TestGuestIsARealDevice(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "web", 2, 2048, 20480, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	if d == nil {
		t.Fatal("the guest has no device in the world")
	}
	if d.Profile == "" || d.FS == nil || len(d.Users) == 0 || len(d.Services) == 0 {
		t.Fatalf("guest device is not a complete machine: profile=%q fs=%v users=%d services=%d",
			d.Profile, d.FS != nil, len(d.Users), len(d.Services))
	}
	if d.FindUser("web") == nil {
		t.Fatal("the guest has no login for its own name")
	}
	// its addresses are routable like any other host's
	if _, ok := core.Reach(w.Devices["pc-alex"], d.FirstLANIP()); !ok {
		t.Fatalf("the guest should be reachable on the LAN at %s", d.FirstLANIP())
	}
	if _, _, msg := core.Dial(w.Devices["pc-alex"], d.FirstLANIP(), 22); msg != "connected" {
		t.Fatalf("ssh to the guest should connect, got %q", msg)
	}
	// and it has a real filesystem with real content
	if data, ok := d.FS.Read("/etc/os-release"); !ok || !strings.Contains(string(data), "Debian") {
		t.Fatalf("the guest has no real os-release: %q", string(data))
	}
}

// Stopping a guest must really stop it: no services, no processes, no answer.
func TestStoppingAGuestReallyStopsIt(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "web", 1, 1024, 8192, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	if d.Svc("sshd").State != "running" {
		t.Fatal("setup: sshd should be running")
	}
	if err := w.StopVM("web"); err != nil {
		t.Fatal(err)
	}
	if v.State != "stopped" {
		t.Fatalf("guest state should be stopped, got %q", v.State)
	}
	if d.Svc("sshd").State == "running" {
		t.Fatal("stopping the guest must stop its services")
	}
	for _, p := range d.Procs {
		if p.Kind == "builtin" {
			t.Fatalf("stopping the guest must remove its processes, found %s", p.Name)
		}
	}
	if _, _, msg := core.Dial(w.Devices["pc-alex"], d.FirstLANIP(), 22); msg == "connected" {
		t.Fatal("a stopped guest must not answer on its address")
	}
	// and starting it brings it all back
	if err := w.StartVM("web"); err != nil {
		t.Fatal(err)
	}
	if v.State != "running" {
		t.Fatal("the guest should be running again")
	}
}

// The spec's causal chain: "VM 内存分配太小 -> Swap -> OOM -> 服务退出".
// Each step must be real, observable state — not a warning string.
func TestRAMStarvedGuestReallySwapsAndOOMs(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "tiny", 1, 256, 8192, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	sshBefore := d.Svc("sshd").State

	// demand more memory than the guest has, continuously
	sawSwap := false
	sawOOM := false
	var killed int
	for tick := 0; tick < 12 && !sawOOM; tick++ {
		d.AddProc(&core.Proc{Name: fmt.Sprintf("worker%d", tick), User: "tiny", CPU: 120, Mem: 100, TTY: "?", State: "R"})
		w.Tick()
		if v.SwapMB > 0 {
			sawSwap = true
		}
		if v.OOMCount > 0 {
			sawOOM = true
		}
	}
	if !sawSwap {
		t.Fatalf("a 256 MiB guest under load never swapped (swap=%d)", v.SwapMB)
	}
	if !sawOOM {
		t.Fatalf("swap filled but no OOM happened (swap=%d/%d)", v.SwapMB, v.SwapMaxMB)
	}
	if killed = len(v.OomEvents); killed == 0 {
		t.Fatal("an OOM left no recorded reason")
	}
	// the kernel log inside the guest must tell the whole story
	data, ok := d.FS.Read("/var/log/syslog")
	if !ok {
		t.Fatal("the guest logged nothing")
	}
	logText := string(data)
	if !strings.Contains(logText, "swapped") {
		t.Fatalf("the guest log has no swap record:\n%s", logText)
	}
	if !strings.Contains(logText, "out of memory") {
		t.Fatalf("the guest log has no OOM record:\n%s", logText)
	}
	// and processes really died — this is the consequence, not a message
	live := 0
	for _, p := range d.Procs {
		if strings.HasPrefix(p.Name, "worker") {
			live++
		}
	}
	if live == 0 {
		t.Fatal("no worker survived, so nothing was actually killed")
	}
	// the OOM must be visible from the player's side too
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "vm top")
	if !strings.Contains(out, "oom kills") {
		t.Fatalf("vm top should report the OOM:\n%s", out)
	}
	if sshBefore != "running" {
		t.Fatal("setup changed unexpectedly")
	}
}

// A full disk must fail writes for real. A player redirecting output onto a
// full guest has to get ENOSPC, not a silent success.
func TestFullDiskReallyFailsWrites(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "tinyfs", 1, 512, 4, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	u := d.FindUser("tinyfs")
	blob := strings.Repeat("x", 700*1024)

	var lastErr error
	for i := 0; i < 20; i++ {
		if lastErr = d.WriteGuest(fmt.Sprintf("/tmp/blob%d", i), []byte(blob), u); lastErr != nil {
			break
		}
	}
	if lastErr == nil {
		t.Fatal("a 4 MiB disk accepted 14 MiB of writes")
	}
	if !strings.Contains(lastErr.Error(), "no space left") {
		t.Fatalf("the write failed for the wrong reason: %v", lastErr)
	}
	if !d.DiskFull() {
		t.Fatalf("the guest should report a full disk (used %d/%d MiB)", d.FS.DiskUsedMB(), v.VDiskM)
	}
	// and through the shell the player sees the real error
	out := run(t, w, d, "tinyfs", "echo hello > /tmp/final")
	if !strings.Contains(out, "no space left on device") {
		t.Fatalf("the shell should report ENOSPC on a full disk:\n%s", out)
	}
	// the data really was not stored
	if _, ok := d.FS.Read("/tmp/final"); ok {
		t.Fatal("a refused write was stored anyway")
	}
}

// CPU starvation is a real multiplier on what the guest achieves — and it must
// never be faked by sleeping.
func TestCPUStarvationThrottlesRealProcesses(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "busy", 1, 2048, 8192, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	// three saturated processes on one vCPU
	for i := 0; i < 3; i++ {
		d.AddProc(&core.Proc{Name: fmt.Sprintf("hog%d", i), User: "busy", CPU: 100, Mem: 40, TTY: "?", State: "R"})
	}
	if th := v.Throttle(); th >= 0.999 {
		t.Fatalf("3 saturated processes on 1 vCPU should starve, throttle=%v", th)
	}
	before := d.Procs[len(d.Procs)-1].CPU
	w.Tick()
	after := d.Procs[len(d.Procs)-1].CPU
	if after >= before {
		t.Fatalf("a starved process should achieve less per tick: %v -> %v", before, after)
	}
	// a guest with enough cores is not starved
	v.VCores = 8
	if th := v.Throttle(); th < 0.999 {
		t.Fatalf("8 vCPU should satisfy the same load, throttle=%v", th)
	}
}

// Destroying a guest must remove it from the world, not just flag it.
func TestDestroyingAGuestRemovesIt(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "temp", 1, 512, 4096, "public")
	if err != nil {
		t.Fatal(err)
	}
	ip := w.Devices[v.DeviceID].FirstLANIP()
	if err := w.DestroyVM("temp"); err != nil {
		t.Fatal(err)
	}
	if w.Devices[v.DeviceID] != nil {
		t.Fatal("the guest's device is still in the world")
	}
	if w.FindVM("temp") != nil {
		t.Fatal("the guest is still registered")
	}
	if _, ok := w.IPMap[ip]; ok {
		t.Fatal("the guest's address is still in the routing table")
	}
	if _, ok := core.Reach(w.Devices["pc-alex"], ip); ok {
		t.Fatal("a destroyed guest is still reachable")
	}
}

// printedCost extracts the cents figure from `vm create` output.
func printedCost(out string) (int64, bool) {
	i := strings.Index(out, "cost:")
	if i < 0 {
		return 0, false
	}
	rest := out[i:]
	j := strings.Index(rest, "$")
	if j < 0 {
		return 0, false
	}
	rest = rest[j+1:]
	k := 0
	for k < len(rest) && (rest[k] >= '0' && rest[k] <= '9' || rest[k] == '.') {
		k++
	}
	n, err := strconv.ParseFloat(rest[:k], 64)
	if err != nil {
		return 0, false
	}
	return int64(n*100 + 0.5), true
}

// A guest's price must sit alongside novapanel's published VPS plans. If the
// two products drift apart, "buy a VPS" and "run a guest" stop being comparable
// decisions for the player, and an economy with no internal consistency is just
// arbitrary numbers.
func TestGuestPricingIsConsistentWithVPSPlans(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	plans := w.Prov.Plans
	if len(plans) < 3 {
		t.Fatalf("expected novapanel to publish several plans, got %d", len(plans))
	}
	for _, p := range plans {
		out := run(t, w, pc, "alex", fmt.Sprintf("vm create probe-%s --cpu %d --mem %d --disk %d --ip %s",
			p.Name, p.Cores, p.RAM, p.Disk, p.IPMode))
		if !strings.Contains(out, "Domain probe-"+p.Name+" created") {
			// a plan the household cannot afford is a legitimate refusal, but
			// then the price must be the reason, not a crash
			if !strings.Contains(out, "insufficient funds") {
				t.Fatalf("plan %s: unexpected output:\n%s", p.Name, out)
			}
			continue
		}
		// the printed cost must be within 10% of the equivalent VPS price
		price, ok := printedCost(out)
		if !ok {
			t.Fatalf("plan %s: no cost printed:\n%s", p.Name, out)
		}
		if price < p.Monthly*9/10 || price > p.Monthly*11/10 {
			t.Fatalf("a %s guest costs $%.2f but the %s VPS costs $%.2f — the two products should agree",
				p.Name, float64(price)/100, p.Name, float64(p.Monthly)/100)
		}
		// and the money must actually leave the wallet
		if acc := w.Bank.Accts["alex"]; acc != nil && acc.Balance <= 0 {
			t.Fatalf("creating %s left the household wallet at %d", p.Name, acc.Balance)
		}
		_ = w.DestroyVM("probe-" + p.Name)
	}
}

// The hypervisor is the household's own server, and it must exist as a real
// machine that can genuinely be out of resources.
func TestHouseholdServerIsTheHypervisor(t *testing.T) {
	w := core.NewWorld()
	srv := w.Devices["srv-alex"]
	if srv == nil {
		t.Fatal("the household has no server to host guests on")
	}
	if srv.Profile != "server" {
		t.Fatalf("srv-alex should be a server, got %q", srv.Profile)
	}
	if srv.HW.RAMMB <= 0 || srv.HW.Cores <= 0 {
		t.Fatal("the hypervisor has no real hardware to allocate from")
	}
	hosts := w.VMHosts()
	if len(hosts) == 0 {
		t.Fatal("no hypervisor is registered")
	}
	// a non-server cannot host guests
	if _, err := w.CreateVM("pc-alex", "nope", 1, 512, 4096, "public"); err == nil {
		t.Fatal("a pc should not be able to host guests")
	}
}

// A snapshot is the hypervisor's own rollback: files deleted afterwards come
// back, and files created afterwards are gone again — on a stopped guest, the
// way a real rollback replaces the disk wholesale.
func TestVMSnapshotRollsBackAStoppedGuest(t *testing.T) {
	w := core.NewWorld()
	v, err := w.CreateVM("srv-alex", "web", 1, 1024, 8192, "public")
	if err != nil {
		t.Fatal(err)
	}
	d := w.Devices[v.DeviceID]
	srv := w.Devices["srv-alex"]

	// a live guest can be snapshotted, over the same shell a player uses
	if out := run(t, w, srv, "alex", "vm snapshot web pre-mistake"); !strings.Contains(out, "pre-mistake") {
		t.Fatalf("snapshot should be confirmed by name, got:\n%s", out)
	}
	if out := run(t, w, srv, "alex", "vm snapshots web"); !strings.Contains(out, "pre-mistake") {
		t.Fatalf("the snapshot should be listed, got:\n%s", out)
	}
	// diverge: a new file lands, a seeded one is deleted
	d.FS.Write("/home/web/after.txt", "should not survive\n", 0644, "web", "web")
	d.FS.Remove("/etc/hostname")
	if err := w.StopVM("web"); err != nil {
		t.Fatal(err)
	}

	if out := run(t, w, srv, "alex", "vm restore web pre-mistake"); !strings.Contains(out, "rolled back") {
		t.Fatalf("restore should confirm the rollback, got:\n%s", out)
	}
	if _, ok := d.FS.Read("/home/web/after.txt"); ok {
		t.Fatal("a file created after the snapshot survived the rollback")
	}
	if data, ok := d.FS.Read("/etc/hostname"); !ok || !strings.Contains(string(data), "web") {
		t.Fatalf("the snapshot's file did not come back: %q", string(data))
	}
	// restoring does not boot the guest: starting it is a separate act
	if v.State != "stopped" {
		t.Fatalf("restore must leave the guest stopped, got %q", v.State)
	}
	if err := w.StartVM("web"); err != nil {
		t.Fatalf("the restored guest should boot: %v", err)
	}
}

// Rolling back underneath a running system is refused, like the provider's
// own restore — stop it first.
func TestVMRestoreRefusesWhileRunning(t *testing.T) {
	w := core.NewWorld()
	if _, err := w.CreateVM("srv-alex", "web", 1, 1024, 8192, "public"); err != nil {
		t.Fatal(err)
	}
	srv := w.Devices["srv-alex"]
	run(t, w, srv, "alex", "vm snapshot web pre-mistake")

	if out := run(t, w, srv, "alex", "vm restore web pre-mistake"); !strings.Contains(out, "while it is running") {
		t.Fatalf("restore of a live guest must be refused, got:\n%s", out)
	}
	if out := run(t, w, srv, "alex", "vm restore web no-such-snap"); !strings.Contains(out, "while it is running") {
		t.Fatalf("the running check must come before the lookup, got:\n%s", out)
	}
	if out := run(t, w, srv, "alex", "vm snapshot web pre-mistake"); !strings.Contains(out, "already exists") {
		t.Fatalf("a duplicate snapshot name must be refused, got:\n%s", out)
	}
}

// A world with a guest must survive a save: before VM GobEncode, saving with
// a guest present recursed until the process died.
func TestGuestAndSnapshotsSurviveSave(t *testing.T) {
	w := core.NewWorld()
	if _, err := w.CreateVM("srv-alex", "web", 1, 1024, 8192, "public"); err != nil {
		t.Fatal(err)
	}
	srv := w.Devices["srv-alex"]
	run(t, w, srv, "alex", "vm snapshot web pre-mistake")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v := back.FindVM("web")
	if v == nil {
		t.Fatal("the guest did not survive the save")
	}
	if v.Host == nil || v.W == nil {
		t.Fatal("the guest's back-pointers were not re-linked after load")
	}
	if len(v.Snapshots) != 1 || v.Snapshots[0].Name != "pre-mistake" {
		t.Fatalf("the snapshot did not survive the save: %+v", v.Snapshots)
	}
	d := back.Devices[v.DeviceID]
	if d == nil || d.FindUser("web") == nil {
		t.Fatal("the guest's device did not survive the save")
	}
	// and the restored-from-save guest still rolls back for real
	d.FS.Write("/home/web/after.txt", "should not survive\n", 0644, "web", "web")
	if err := back.StopVM("web"); err != nil {
		t.Fatal(err)
	}
	if err := back.VMRestore("web", "pre-mistake"); err != nil {
		t.Fatalf("restore after load: %v", err)
	}
	if _, ok := d.FS.Read("/home/web/after.txt"); ok {
		t.Fatal("a file created after the snapshot survived the rollback")
	}
}
