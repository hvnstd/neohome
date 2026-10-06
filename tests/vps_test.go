package tests

import (
	"fmt"
	"strings"
	"testing"

	"neohome/internal/core"
)

// §12 (VPS / 计算机 / 组网): a rented node's lifecycle must be the machine's own
// state. Choosing a region, stopping, rebooting, reinstalling, resizing, taking
// a snapshot and going in through the console are all operations on the same
// Device the rest of the world talks to — so a stopped node is genuinely absent
// from the network, a reinstalled one genuinely has another filesystem, and a
// restored one genuinely gets its files back.
//
// Every test below therefore checks the world, not the panel's output: the
// packet path, the filesystem, the wallet and the provider's own record.

// buyNode buys a node through the panel as the household does, and returns it
// with the account's balance before the purchase.
func buyNode(t *testing.T, w *core.World, plan, hostname string, extra ...string) (*core.Device, int64) {
	t.Helper()
	pc := w.Devices["pc-alex"]
	before := w.Bank.Accts["alex"].Balance
	cmd := fmt.Sprintf("vps create %s %s", plan, hostname)
	for _, e := range extra {
		cmd += " " + e
	}
	out := run(t, w, pc, "alex", cmd)
	for _, id := range w.Order {
		if d := w.Devices[id]; d.Hostname == hostname {
			return d, before
		}
	}
	t.Fatalf("the panel did not create %s:\n%s", hostname, out)
	return nil, 0
}

// cents renders a cent amount the way the panel does, for the assertions about
// money.
func cents(c int64) string { return fmt.Sprintf("$%d.%02d", c/100, c%100) }

// reach reports what the household's PC gets connecting to a node's SSH port.
func reach(w *core.World, d *core.Device) string {
	_, _, msg := core.Dial(w.Devices["pc-alex"], core.WANIPOf(d), 22)
	return msg
}

func TestVPSLifecycleIsTheMachinesOwnState(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "nano-1", "life1")

	if msg := reach(w, node); msg != "connected" {
		t.Fatalf("a fresh node must be reachable on 22, got %q", msg)
	}
	if !node.NetUp || core.VPSState(node) != "running" {
		t.Fatalf("a fresh node must be running")
	}

	// 关机: the machine goes dark, and the network says so because there is
	// nothing at that address any more
	out := run(t, w, pc, "alex", "vps stop life1")
	if !strings.Contains(out, "powered off") {
		t.Fatalf("stop must report the machine going down:\n%s", out)
	}
	if node.NetUp {
		t.Fatal("a stopped node still claims to be up")
	}
	if msg := reach(w, node); !strings.Contains(msg, "host is down") {
		t.Fatalf("a stopped node must be unreachable, got %q", msg)
	}
	if node.Svc("sshd").State == "running" {
		t.Fatal("sshd is still running on a powered-off machine")
	}
	// the console is closed too: there the process really is absent
	if out := run(t, w, pc, "alex", "vps console life1"); !strings.Contains(out, "powered off") {
		t.Fatalf("the console must refuse a stopped node:\n%s", out)
	}
	// and stopping twice is a refusal, not a silent success
	if out := run(t, w, pc, "alex", "vps stop life1"); !strings.Contains(out, "already stopped") {
		t.Fatalf("stopping a stopped node must say so:\n%s", out)
	}

	// 启动: it comes back with the services it was running
	if out := run(t, w, pc, "alex", "vps start life1"); !strings.Contains(out, "powered on") {
		t.Fatalf("start must report the machine coming up:\n%s", out)
	}
	if msg := reach(w, node); msg != "connected" {
		t.Fatalf("a started node must answer again, got %q", msg)
	}
	if node.Svc("sshd").State != "running" {
		t.Fatal("sshd did not come back with the machine")
	}

	// 重启: the disk survives, the uptime does not
	node.FS.Write("/root/keep.txt", "survives a reboot\n", 0644, "root", "root")
	out = run(t, w, pc, "alex", "vps reboot life1")
	if !strings.Contains(out, "rebooted") {
		t.Fatalf("reboot must report the restart:\n%s", out)
	}
	if data, ok := node.FS.Read("/root/keep.txt"); !ok || !strings.Contains(string(data), "survives") {
		t.Fatal("a reboot must not lose the disk")
	}
	if node.Uptime() > 0 && node.Uptime().Seconds() > 1 {
		t.Fatalf("a reboot must reset uptime, got %s", node.Uptime())
	}

	// the boundary: a node belongs to one account, and another account cannot
	// touch it — not even to switch it off
	if _, err := w.VPSStop("mara", "life1"); err == nil {
		t.Fatal("another account must not be able to stop somebody else's node")
	}
	if _, err := w.VPSStart("mara", "life1"); err == nil {
		t.Fatal("another account must not be able to start somebody else's node")
	}
	if node.NetUp == false {
		t.Fatal("the refused operations must have changed nothing")
	}
}

func TestReinstallChangesTheSystemNotTheAddress(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "nano-1", "six1")
	ip := core.WANIPOf(node)
	oldPass := node.Users["deploy"].Pass
	node.FS.Write("/root/before.txt", "from the old system\n", 0644, "root", "root")

	// 重装 on a running machine would be a destructive surprise, so the panel
	// refuses it — the boundary is the point
	out := run(t, w, pc, "alex", "vps reinstall six1 alpine")
	if !strings.Contains(out, "power it off first") {
		t.Fatalf("reinstalling a running node must be refused:\n%s", out)
	}
	if node.OS.Distro != "Debian" {
		t.Fatal("the refused reinstall must have changed nothing")
	}

	run(t, w, pc, "alex", "vps stop six1")
	out = run(t, w, pc, "alex", "vps reinstall six1 alpine")
	if !strings.Contains(out, "old filesystem is gone") {
		t.Fatalf("reinstall must say what it did:\n%s", out)
	}

	// the new system is real: its own release file, its own package manager,
	// its own shell — and the old files are gone
	if node.OS.Distro != "Alpine" {
		t.Fatalf("the node is still %s after being reinstalled as Alpine", node.OS.Distro)
	}
	if data, ok := node.FS.Read("/etc/os-release"); !ok || !strings.Contains(string(data), "Alpine") {
		t.Fatalf("the image's own os-release must be on disk: %q", string(data))
	}
	if _, ok := node.FS.Read("/root/before.txt"); ok {
		t.Fatal("a reinstall must wipe the old filesystem")
	}
	if _, ok := node.FS.Read("/etc/apk/repositories"); !ok {
		t.Fatal("an Alpine image must bring Alpine repositories, not Debian ones")
	}
	if _, ok := node.FS.Read("/etc/apt/sources.list"); ok {
		t.Fatal("Debian apt sources survived an Alpine reinstall")
	}
	if rec := w.NodeOf(node); rec == nil || rec.Rebuilds != 1 {
		t.Fatalf("the provider must record the rebuild, got %+v", w.NodeOf(node))
	}

	// the address, the name and the DNS record stay with the machine: that is
	// the whole point of a reinstall
	if got := core.WANIPOf(node); got != ip {
		t.Fatalf("the address moved during a reinstall: %s -> %s", ip, got)
	}
	if ans, ok, _ := core.DNSAnswer(pc, "six1.neohome.example"); !ok || ans != ip {
		t.Fatalf("the name must still resolve to the node: %q %v", ans, ok)
	}

	// and the old credentials are gone with the old system
	newPass := node.Users["deploy"].Pass
	if newPass == oldPass {
		t.Fatal("a reinstall must not leave the old password in place")
	}
	if node.Users["deploy"].CheckPassword(oldPass) {
		t.Fatal("the old password still works after a reinstall")
	}
	if !node.Users["deploy"].CheckPassword(newPass) {
		t.Fatal("the new password does not work")
	}

	// recovery: the machine boots again, and the world reaches it
	if out := run(t, w, pc, "alex", "vps start six1"); !strings.Contains(out, "powered on") {
		t.Fatalf("the reinstalled node must start:\n%s", out)
	}
	if msg := reach(w, node); msg != "connected" {
		t.Fatalf("a reinstalled, started node must answer, got %q", msg)
	}
}

func TestResizeAndDiskAreBilledAndRefusedWhileRunning(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "small-2", "big1")
	rec := w.NodeOf(node)

	// a running machine cannot change shape underneath itself
	if out := run(t, w, pc, "alex", "vps resize big1 --cpu 4 --mem 4096"); !strings.Contains(out, "power it off first") {
		t.Fatalf("resizing a running node must be refused:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "vps disk big1 10"); !strings.Contains(out, "power it off first") {
		t.Fatalf("adding a disk to a running node must be refused:\n%s", out)
	}
	if node.HW.Cores != 2 || node.HW.RAMMB != 2048 {
		t.Fatal("a refused resize changed the hardware anyway")
	}

	run(t, w, pc, "alex", "vps stop big1")
	// shrinking the disk is not something a provider can do
	if out := run(t, w, pc, "alex", "vps resize big1 --disk 1024"); !strings.Contains(out, "disk can only grow") {
		t.Fatalf("a disk shrink must be refused:\n%s", out)
	}

	before := w.Bank.Accts["alex"].Balance
	out := run(t, w, pc, "alex", "vps resize big1 --cpu 4 --mem 4096 --disk 81920")
	if !strings.Contains(out, "resized") {
		t.Fatalf("resize must report the new shape:\n%s", out)
	}
	if node.HW.Cores != 4 || node.HW.RAMMB != 4096 || node.HW.DiskMB != 81920 {
		t.Fatalf("the hardware did not change: %+v", node.HW)
	}
	// the upgrade is real money, and the panel's own price moves with it
	want := core.NodePriceCents(4, 4096, 81920, "public")
	if rec.Monthly != want {
		t.Fatalf("the monthly price should be %s, got %s", cents(want), cents(rec.Monthly))
	}
	charge := before - w.Bank.Accts["alex"].Balance
	if charge != want-1800 {
		t.Fatalf("the upgrade should charge the difference (%s), charged %s", cents(want-1800), cents(charge))
	}

	// and it really is a bigger machine: the disk the filesystem reports grows
	before = w.Bank.Accts["alex"].Balance
	if out := run(t, w, pc, "alex", "vps disk big1 20"); !strings.Contains(out, "20 GiB") {
		t.Fatalf("adding a disk must report it:\n%s", out)
	}
	if got := node.HW.DiskMB; got != 81920+20*1024 {
		t.Fatalf("the added disk is not on the machine: %d MiB", got)
	}
	if w.Bank.Accts["alex"].Balance >= before {
		t.Fatal("block storage must cost something")
	}

	// a machine the household cannot afford is refused before anything changes
	if out := run(t, w, pc, "alex", "vps disk big1 100000"); !strings.Contains(out, "insufficient funds") {
		t.Fatalf("an unaffordable disk must be refused:\n%s", out)
	}
}

func TestSnapshotRestoreRollsTheDiskBack(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "nano-1", "snap1")
	node.FS.Write("/root/keep.txt", "the good state\n", 0644, "root", "root")

	out := run(t, w, pc, "alex", "vps snapshot snap1 before-mistake")
	if !strings.Contains(out, "before-mistake") {
		t.Fatalf("the snapshot must be named in the answer:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "vps snapshots snap1"); !strings.Contains(out, "before-mistake") {
		t.Fatalf("the snapshot must be listed:\n%s", out)
	}

	// the mistake: a file is clobbered and another one added
	node.FS.Write("/root/keep.txt", "the bad state\n", 0644, "root", "root")
	node.FS.Write("/root/accident.txt", "should not survive a rollback\n", 0644, "root", "root")

	// a live machine cannot be rolled back underneath itself
	if out := run(t, w, pc, "alex", "vps restore snap1 before-mistake"); !strings.Contains(out, "power it off first") {
		t.Fatalf("restoring a running node must be refused:\n%s", out)
	}
	run(t, w, pc, "alex", "vps stop snap1")
	if out := run(t, w, pc, "alex", "vps restore snap1 nope"); !strings.Contains(out, "no snapshot") {
		t.Fatalf("the refusal must survive a power-off:\n%s", out)
	}
	if data, ok := node.FS.Read("/root/keep.txt"); !ok || !strings.Contains(string(data), "bad state") {
		t.Fatal("a refused restore must not touch the disk")
	}

	out = run(t, w, pc, "alex", "vps restore snap1 before-mistake")
	if !strings.Contains(out, "restored") {
		t.Fatalf("restore must report the rollback:\n%s", out)
	}
	if data, ok := node.FS.Read("/root/keep.txt"); !ok || !strings.Contains(string(data), "good state") {
		t.Fatalf("the snapshot's file did not come back: %q", string(data))
	}
	if _, ok := node.FS.Read("/root/accident.txt"); ok {
		t.Fatal("a file created after the snapshot survived the rollback")
	}

	// recovery: the restored machine boots
	if out := run(t, w, pc, "alex", "vps start snap1"); !strings.Contains(out, "powered on") {
		t.Fatalf("the restored node must start:\n%s", out)
	}
	if msg := reach(w, node); msg != "connected" {
		t.Fatalf("the restored node must answer, got %q", msg)
	}
}

func TestRegionChoosesTheAddressAndTheOwner(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "alex", "vps regions")
	for _, want := range []string{"eu-central", "us-east", "ap-northeast", "nl-ams-1", "jp-tyo-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the region list is missing %q:\n%s", want, out)
		}
	}

	eu, _ := buyNode(t, w, "nano-1", "eu1", "--region", "eu-central")
	us, _ := buyNode(t, w, "nano-1", "us1", "--region", "us-east")
	ap, _ := buyNode(t, w, "nano-1", "ap1", "--region", "ap-northeast")

	// the region is the network the address comes from, not a label
	if a := core.WANIPOf(us); !strings.HasPrefix(a, "198.19.0.") {
		t.Fatalf("a us-east node got %s, which is not its region's block", a)
	}
	if a := core.WANIPOf(ap); !strings.HasPrefix(a, "198.19.1.") {
		t.Fatalf("an ap-northeast node got %s, which is not its region's block", a)
	}
	if v6 := us.FirstWANv6(); !strings.HasPrefix(v6, "2001:db8:fc09:") {
		t.Fatalf("a us-east node's IPv6 must come from its own /48, got %s", v6)
	}
	if rec := w.NodeOf(us); rec.Region != "us-east" || rec.Datacenter != "us-ash-1" {
		t.Fatalf("the panel did not record the region: %+v", rec)
	}

	// whois names the region's operator, and still no person
	who := run(t, w, pc, "alex", "whois "+core.WANIPOf(us))
	if !strings.Contains(who, "NovaPanel US") || strings.Contains(who, "Alex") {
		t.Fatalf("whois must name the region's AS and no person:\n%s", who)
	}

	// and the distance is real: the same machine size in another region is
	// measurably further away
	hopsEU, _ := w.Trace(pc, core.WANIPOf(eu))
	hopsUS, _ := w.Trace(pc, core.WANIPOf(us))
	rtt := func(hops []core.Hop) float64 {
		if len(hops) == 0 {
			return 0
		}
		return hops[len(hops)-1].Latency
	}
	if rtt(hopsUS) <= rtt(hopsEU) {
		t.Fatalf("us-east should be further from the household than eu-central: %.3f vs %.3f",
			rtt(hopsUS), rtt(hopsEU))
	}

	// the boundary: an unknown region is refused before any money moves
	before := w.Bank.Accts["alex"].Balance
	if out := run(t, w, pc, "alex", "vps create nano-1 nope1 debian --region mars-1"); !strings.Contains(out, "no such region") {
		t.Fatalf("an unknown region must be refused:\n%s", out)
	}
	if w.Bank.Accts["alex"].Balance != before {
		t.Fatal("a refused purchase still charged the household")
	}
	if _, _, err := w.NodeByHostname("nope1"); err == nil {
		t.Fatal("a refused purchase created a node anyway")
	}
}

func TestReverseDNSIsPublishedAndNeverNamesAPerson(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "nano-1", "ptr1")
	ip := core.WANIPOf(node)

	// a fresh node answers for its own address with its own name
	out := run(t, w, pc, "alex", "dig -x "+ip)
	if !strings.Contains(out, "PTR") || !strings.Contains(out, "ptr1.neohome.example") {
		t.Fatalf("dig -x must find the node's PTR:\n%s", out)
	}
	if strings.Contains(out, "Alex") || strings.Contains(out, "alex") {
		t.Fatalf("a reverse lookup leaked a person:\n%s", out)
	}

	// the customer can publish a different name, and the zone follows
	if out := run(t, w, pc, "alex", "vps rdns ptr1 services.example.net"); !strings.Contains(out, "services.example.net") {
		t.Fatalf("setting rDNS must report the new name:\n%s", out)
	}
	if name, ok := core.ReverseNameForTest(w, ip); !ok || name != "services.example.net" {
		t.Fatalf("the reverse zone did not follow the panel: %q %v", name, ok)
	}
	out = run(t, w, pc, "alex", "dig -x "+ip)
	if !strings.Contains(out, "services.example.net") {
		t.Fatalf("dig -x must show the published name:\n%s", out)
	}

	// a name that is not a name is refused rather than published
	if out := run(t, w, pc, "alex", "vps rdns ptr1 not_a_name"); !strings.Contains(out, "fully qualified") {
		t.Fatalf("a malformed PTR must be refused:\n%s", out)
	}

	// addresses nobody publishes a reverse zone for have no PTR — and a
	// private address has nothing to publish at all
	if _, ok := core.ReverseNameForTest(w, "10.77.1.11"); ok {
		t.Fatal("a private address must not resolve backwards")
	}
	shared, _, err := w.ProvisionVPSWithOS("alex", "nano-shared", "cg1", "debian")
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := core.ReverseNameForTest(w, core.WANIPOf(shared)); ok {
		t.Fatalf("a carrier-grade-NAT address must not publish a PTR, got %q", name)
	}

	// the boundary that matters: the node belongs to one account, and the
	// other account cannot rename it
	if err := w.VPSSetRDNS("mara", "ptr1", "stolen.example"); err == nil {
		t.Fatal("another account must not be able to change a node's rDNS")
	}
	if name, _ := core.ReverseNameForTest(w, ip); name != "services.example.net" {
		t.Fatalf("the refused rDNS change went through anyway: %q", name)
	}
}

func TestConsoleReachesTheNodesOwnShell(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	node, _ := buyNode(t, w, "nano-1", "con1")

	// the console is the machine's own shell: the commands run there see the
	// node's own filesystem, hostname and users
	node.FS.Write("/root/proof.txt", "written on the node\n", 0644, "root", "root")
	out := runWithStdin(t, w, pc, "alex", "vps console con1", "hostname", "cat /root/proof.txt", "exit")
	if !strings.Contains(out, "con1") {
		t.Fatalf("the console did not land on the node:\n%s", out)
	}
	if !strings.Contains(out, "written on the node") {
		t.Fatalf("the console did not see the node's own filesystem:\n%s", out)
	}
	if !strings.Contains(out, "Disconnected from con1") {
		t.Fatalf("exit must leave the console:\n%s", out)
	}

	// typed input cannot escape into the host: the console's shell is the node
	out = runWithStdin(t, w, pc, "alex", "vps console con1", "cat /root/proof.txt", "exit")
	if strings.Contains(out, pc.Hostname+":") && strings.Contains(out, "/home/alex/proof") {
		t.Fatalf("the console leaked the player's own machine:\n%s", out)
	}
}
