package tests

import (
	"fmt"
	"strings"
	"testing"

	"neohome/internal/core"
)

// §15 家庭设备: the devices a household has, and the dependencies between them.
//
// The spec's example is a chain — Camera → PoE Switch → Router → NAS — and it
// then says what each failure means: a switch that goes down takes what is
// plugged into it offline, a NAS that stops takes backups with it, a laptop is
// only available while its battery lasts. Each test below therefore checks the
// *world* after the failure: the packet path, the device's own state, the
// spool file, the backup index.

func TestHouseholdDevicesAreCabledThroughTheSwitch(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sw := w.Devices["sw-alex"]
	cam := w.Devices["cam-alex"]
	prn := w.Devices["prn-alex"]
	lap := w.Devices["laptop-alex"]

	if sw == nil || sw.Switch == nil {
		t.Fatal("the household has no managed switch")
	}
	// the chain is real: the camera is behind the switch, and the switch is
	// behind the router
	if cam.Uplink != sw.ID || cam.UplinkPort != 5 {
		t.Fatalf("the camera is not plugged into the switch: uplink=%q port=%d", cam.Uplink, cam.UplinkPort)
	}
	if path := w.LinkPath(cam); path != "cam-front → sw-hall" {
		t.Fatalf("the physical path of the camera reads %q", path)
	}
	if !sw.Switch.Ports[0].Uplink {
		t.Fatal("the switch has no uplink port to the router")
	}
	if sw.Switch.Ports[0].Peer != "router-alex" {
		t.Fatalf("the uplink port is not wired to the router: %q", sw.Switch.Ports[0].Peer)
	}

	out := run(t, w, pc, "alex", "switchctl show")
	for _, want := range []string{"uplink", "gateway", "cam-front", "printer", "PoE budget 60 W"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the switch report is missing %q:\n%s", want, out)
		}
	}
	// the port file is the configuration, and it says what the report says
	conf, ok := sw.FS.Read("/etc/config/switch")
	if !ok || !strings.Contains(string(conf), "poe=on") || !strings.Contains(string(conf), "budget 60") {
		t.Fatalf("the switch's own config file does not match its state:\n%s", string(conf))
	}

	// everything behind the switch answers while it is up
	for _, d := range []*core.Device{cam, prn, lap} {
		_, _, msg := core.Dial(pc, d.FirstLANIP(), 1)
		if msg == "No route to host" {
			t.Fatalf("%s is not on the network: %s", d.Hostname, msg)
		}
		if up, why := w.LinkUp(d); !up {
			t.Fatalf("%s should be linked: %s", d.Hostname, why)
		}
	}

	// the boundary: the uplink port carries the whole switch, so cutting it is
	// refused — and nothing changes when it is refused
	if out := run(t, w, pc, "alex", "switchctl port 1 down"); !strings.Contains(out, "uplink") {
		t.Fatalf("cutting the uplink must be refused:\n%s", out)
	}
	if !sw.Switch.Ports[0].Admin {
		t.Fatal("the refused command brought the uplink down anyway")
	}
	// a port nobody is plugged into is a no-op, not an error
	if out := run(t, w, pc, "alex", "switchctl port 8 down"); !strings.Contains(out, "port 8 is down") {
		t.Fatalf("an empty port should still be administrable:\n%s", out)
	}
}

func TestSwitchPortDownTakesWhatIsPluggedInOffline(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sw := w.Devices["sw-alex"]
	prn := w.Devices["prn-alex"]

	if _, _, msg := core.Dial(pc, prn.FirstLANIP(), core.PrintPort); msg != "connected" {
		t.Fatalf("setup: the printer should be reachable, got %q", msg)
	}

	// the printer has its own plug: pulling its port takes it off the network
	// but does not switch it off. The difference is the whole point.
	out := run(t, w, pc, "alex", "switchctl port 6 down")
	if !strings.Contains(out, "printer is off the network now") {
		t.Fatalf("the port-down report must name what it disconnected:\n%s", out)
	}
	if _, _, msg := core.Dial(pc, prn.FirstLANIP(), core.PrintPort); !strings.Contains(msg, "link down") {
		t.Fatalf("a printer behind a down port must fail with the real reason, got %q", msg)
	}
	if !prn.NetUp || prn.Svc("cupsd").State != "running" {
		t.Fatal("the printer lost its own power, which a port-down must not do")
	}
	// and the printer says so from its own side
	stat := run(t, w, prn, "root", "lpstat")
	if !strings.Contains(stat, "unreachable") {
		t.Fatalf("the printer cannot see its own dead link:\n%s", stat)
	}
	// the switch logged it, so the household can find out why
	log, _ := sw.FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "port 6") {
		t.Fatalf("the switch did not log the port change:\n%s", log)
	}

	// recovery: the port comes back and so does the printer
	if out := run(t, w, pc, "alex", "switchctl port 6 up"); !strings.Contains(out, "up") {
		t.Fatalf("bringing the port back must report it:\n%s", out)
	}
	if _, _, msg := core.Dial(pc, prn.FirstLANIP(), core.PrintPort); msg != "connected" {
		t.Fatalf("the printer must be reachable again, got %q", msg)
	}

	// the camera is fed over ethernet, so its port is also its power switch
	cam := w.Devices["cam-alex"]
	if out := run(t, w, pc, "alex", "switchctl poe 5 off"); !strings.Contains(out, "cam-front lost power") {
		t.Fatalf("cutting PoE must say whose power went:\n%s", out)
	}
	w.Tick()
	if cam.Powered() || cam.NetUp {
		t.Fatal("a camera with no PoE is still running")
	}
	if cam.Svc("rtsp").State == "running" {
		t.Fatal("the camera's stream is still running without power")
	}
	if _, _, msg := core.Dial(pc, cam.FirstLANIP(), 554); !strings.Contains(msg, "host is down") {
		t.Fatalf("a camera with no PoE must be down, not merely filtered: %q", msg)
	}
	// recovery: power comes back and the camera boots
	if out := run(t, w, pc, "alex", "switchctl poe 5 on"); !strings.Contains(out, "booting") {
		t.Fatalf("restoring PoE must report it:\n%s", out)
	}
	w.Tick()
	if !cam.NetUp || cam.Svc("rtsp").State != "running" {
		t.Fatal("the camera did not come back after its power returned")
	}
	if _, _, msg := core.Dial(pc, cam.FirstLANIP(), 554); msg != "connected" {
		t.Fatalf("the camera must stream again, got %q", msg)
	}
}

func TestPoEBudgetBoundsWhatTheSwitchCanFeed(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sw := w.Devices["sw-alex"]
	cam := w.Devices["cam-alex"]

	// the camera has no plug: it draws from the switch, and the switch knows how
	// much it is giving away
	if draw := sw.PoESuppliedW(); draw <= 0 {
		t.Fatalf("the switch reports feeding nothing while the camera is running")
	}
	out := run(t, w, pc, "alex", "switchctl budget")
	if !strings.Contains(out, "PoE budget: 60 W") {
		t.Fatalf("the budget must be reported:\n%s", out)
	}

	// the boundary: a supply cannot go below what it is already delivering
	if out := run(t, w, pc, "alex", "switchctl budget 2"); !strings.Contains(out, "cannot go below") {
		t.Fatalf("a budget below the current draw must be refused:\n%s", out)
	}
	if sw.Switch.BudgetW != 60 {
		t.Fatal("the refused budget change went through anyway")
	}

	// with the camera's port off, the switch is feeding nothing — so the supply
	// really can be shrunk, and then it cannot feed a camera at all
	run(t, w, pc, "alex", "switchctl poe 5 off")
	w.Tick()
	if out := run(t, w, pc, "alex", "switchctl budget 2"); !strings.Contains(out, "budget set to 2 W") {
		t.Fatalf("with nothing plugged in the budget should shrink:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "switchctl poe 5 on"); !strings.Contains(out, "refusing to power port 5") {
		t.Fatalf("a camera the budget cannot feed must be refused:\n%s", out)
	}
	if cam.Powered() {
		t.Fatal("the camera is drawing power the budget does not allow")
	}

	// recovery: raise the budget and the camera comes back
	run(t, w, pc, "alex", "switchctl budget 60")
	if out := run(t, w, pc, "alex", "switchctl poe 5 on"); !strings.Contains(out, "booting") {
		t.Fatalf("with budget restored the port should power up:\n%s", out)
	}
	w.Tick()
	if !cam.Powered() || cam.Svc("rtsp").State != "running" {
		t.Fatal("the camera did not come back when the budget allowed it")
	}

	// a device that has its own plug cannot be powered by the switch, and being
	// asked to is an error rather than a silent nothing
	if out := run(t, w, pc, "alex", "switchctl poe 6 on"); !strings.Contains(out, "does not take power over ethernet") {
		t.Fatalf("PoE on a self-powered device must be refused:\n%s", out)
	}
}

func TestPowerCutRunsTheCamerasOnTheSwitchBattery(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	sw := w.Devices["sw-alex"]
	cam := w.Devices["cam-alex"]
	nas := w.Devices["nas-alex"]

	if sw.UPS == nil {
		t.Fatal("the switch has no battery backup to ride an outage")
	}
	w.CutPower("alex")

	// the switch rides it out, so the cameras stay up: this is the whole reason
	// a PoE switch on a UPS is worth buying
	if !sw.NetUp || !cam.NetUp || !cam.Powered() {
		t.Fatalf("with the switch on battery the camera should still be running (sw up=%v cam up=%v)", sw.NetUp, cam.NetUp)
	}
	// the PC is dark with the rest of the house, so the machine asking is the
	// out-of-band controller, which has its own battery and backhaul
	bmc := w.Devices["bmc-alex"]
	if _, _, msg := core.Dial(bmc, cam.FirstLANIP(), 554); msg != "connected" {
		t.Fatalf("the camera should still be reachable through the dark house, got %q", msg)
	}
	// the NAS is on the mains and has no battery: it is gone
	if nas.NetUp {
		t.Fatal("the NAS is still running through a power cut")
	}

	// the battery runs out, and everything on PoE goes with it
	for i := 0; i < 600 && sw.UPS.ChargePct > 0; i++ {
		w.Tick()
	}
	if sw.UPS.ChargePct != 0 {
		t.Fatalf("the switch's battery should have drained, at %d%%", sw.UPS.ChargePct)
	}
	if sw.NetUp || cam.NetUp || cam.Powered() {
		t.Fatalf("a switch whose battery is flat must take its PoE devices with it (sw=%v cam=%v)", sw.NetUp, cam.NetUp)
	}
	if _, _, msg := core.Dial(bmc, cam.FirstLANIP(), 554); !strings.Contains(msg, "host is down") {
		t.Fatalf("with everything dark the camera must be down, got %q", msg)
	}

	// recovery: the breaker closes and the house comes back, cameras included
	if out := run(t, w, w.Devices["bmc-alex"], "root", "power boot"); !strings.Contains(out, "supply on") {
		t.Fatalf("restoring power must report what booted:\n%s", out)
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if !sw.NetUp || !cam.NetUp || cam.Svc("rtsp").State != "running" || !nas.NetUp {
		t.Fatal("the household did not come back after the power was restored")
	}
	if _, _, msg := core.Dial(pc, cam.FirstLANIP(), 554); msg != "connected" {
		t.Fatalf("the camera must be reachable after the restore, got %q", msg)
	}
}

func TestPrinterSpoolsRealJobsAndRunsOutOfPaper(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	prn := w.Devices["prn-alex"]
	pc.FS.Write("/home/alex/report.txt", strings.Repeat("a line of the report\n", 70), 0644, "alex", "alex")

	// a job is a real queued object on the printer's own disk
	out := run(t, w, pc, "alex", "lp -t Report /home/alex/report.txt")
	if !strings.Contains(out, "request id is printer-") {
		t.Fatalf("submitting a job must return a request id:\n%s", out)
	}
	queued, ok := prn.FS.Read(core.SpoolDir + "/queue")
	if !ok || !strings.Contains(string(queued), "Report") {
		t.Fatalf("the job is not in the printer's spool:\n%s", string(queued))
	}

	// it prints on the world clock, using real paper, and leaves output behind
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if prn.Printer.Printed == 0 {
		t.Fatal("the job never printed")
	}
	tray, ok := prn.FS.Read(core.SpoolDir + "/tray.log")
	if !ok || !strings.Contains(string(tray), "Printed by alex: Report") {
		t.Fatalf("the printed sheet is not in the tray:\n%s", string(tray))
	}
	if out := run(t, w, pc, "alex", "lpstat"); !strings.Contains(out, "no jobs") {
		t.Fatalf("the queue should be empty after printing:\n%s", out)
	}

	// the failure a household actually hits: out of paper. The job waits with
	// the reason on it instead of vanishing.
	prn.Printer.Paper = 0 // the tray really emptied
	out = run(t, w, pc, "alex", "lp -t Blocked /home/alex/report.txt")
	if !strings.Contains(out, "request id") {
		t.Fatalf("a job submitted to an empty printer must still queue:\n%s", out)
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	stat := run(t, w, pc, "alex", "lpstat")
	if !strings.Contains(stat, "out of paper") {
		t.Fatalf("the held job must say why it is held:\n%s", stat)
	}
	if prn.Printer.Printed != 2 {
		t.Fatalf("nothing may print with an empty tray, printed=%d", prn.Printer.Printed)
	}

	// recovery: load paper and the held job finishes
	if out := run(t, w, pc, "alex", "lpadmin paper 20"); !strings.Contains(out, "loaded 20") {
		t.Fatalf("loading paper must report it:\n%s", out)
	}
	for i := 0; i < 8; i++ {
		w.Tick()
	}
	if out := run(t, w, pc, "alex", "lpstat"); !strings.Contains(out, "no jobs") {
		t.Fatalf("the held job should have printed after the tray was filled:\n%s", out)
	}

	// the boundary: cancelling somebody else's job is refused, root may
	if _, err := w.SubmitPrint(prn, "mara", "mara's file", 1); err != nil {
		t.Fatal(err)
	}
	jobID := prn.Printer.Queue[len(prn.Printer.Queue)-1].ID
	if out := run(t, w, pc, "alex", fmt.Sprintf("cancel %d", jobID)); !strings.Contains(out, "belongs to mara") {
		t.Fatalf("cancelling another user's job must be refused:\n%s", out)
	}
	if len(prn.Printer.Queue) == 0 {
		t.Fatal("the refused cancel removed the job anyway")
	}
	if out := run(t, w, prn, "root", fmt.Sprintf("cancel %d", jobID)); !strings.Contains(out, "cancelled") {
		t.Fatalf("root should be able to cancel:\n%s", out)
	}

	// and the print path needs the network: a printer behind a dead port takes
	// no jobs at all
	sw := w.Devices["sw-alex"]
	if err := sw.SetPortAdmin(6, false); err != nil {
		t.Fatal(err)
	}
	if out := run(t, w, pc, "alex", "lp -t Nope /home/alex/report.txt"); !strings.Contains(out, "link down") {
		t.Fatalf("a submission over a dead link must fail with the reason:\n%s", out)
	}
}

func TestLaptopBatteryAndLidAreRealState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	lap := w.Devices["laptop-alex"]

	if lap.Battery == nil {
		t.Fatal("the laptop has no battery")
	}
	if msg := dialMsg(pc, lap.FirstLANIP(), 1); msg == "No route to host" {
		t.Fatalf("the laptop should start on the network, got %q", msg)
	}

	// unplugged: the battery is the uptime, and the world clock drains it
	if out := run(t, w, lap, "alex", "laptopctl charge off"); !strings.Contains(out, "running on battery") {
		t.Fatalf("unplugging must report the battery:\n%s", out)
	}
	start := lap.Battery.Pct
	for i := 0; i < 6; i++ {
		w.Tick()
	}
	if lap.Battery.Pct >= start {
		t.Fatalf("the battery did not drain: %d%% -> %d%%", start, lap.Battery.Pct)
	}
	log, _ := lap.FS.Read("/var/log/syslog")
	_ = log

	// a flat battery stops the machine, and the network says so
	lap.Battery.Pct = 1
	w.Tick()
	w.Tick()
	if lap.Battery.Pct != 0 || lap.NetUp {
		t.Fatalf("a flat battery must stop the laptop (pct=%d netup=%v)", lap.Battery.Pct, lap.NetUp)
	}
	if msg := dialMsg(pc, lap.FirstLANIP(), 1); !strings.Contains(msg, "host is down") {
		t.Fatalf("a laptop with a flat battery must be down, got %q", msg)
	}
	// recovery: the charger wakes it and the battery refills
	if out := run(t, w, lap, "alex", "laptopctl charge on"); !strings.Contains(out, "battery empty") {
		t.Fatalf("a machine with an empty battery must not run commands:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "power plug laptop"); !strings.Contains(out, "plugged in") {
		t.Fatalf("plugging the charger back in must work from the house:\n%s", out)
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if !lap.NetUp || lap.Battery.Pct == 0 {
		t.Fatalf("the laptop did not come back on the charger (pct=%d netup=%v)", lap.Battery.Pct, lap.NetUp)
	}

	// the lid is a second, independent way to be off the network
	if out := run(t, w, lap, "alex", "laptopctl lid closed"); !strings.Contains(out, "suspended") {
		t.Fatalf("closing the lid must suspend:\n%s", out)
	}
	if lap.NetUp || lap.Powered() {
		t.Fatal("a closed laptop is still on the network")
	}
	if msg := dialMsg(pc, lap.FirstLANIP(), 1); !strings.Contains(msg, "host is down") {
		t.Fatalf("a suspended laptop must be down, got %q", msg)
	}
	// and the lid is a physical thing: you cannot type on a sleeping machine,
	// so the way back is the same one a person would use
	if out := run(t, w, lap, "alex", "laptopctl lid open"); !strings.Contains(out, "suspended") {
		t.Fatalf("a suspended laptop must not accept commands:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "power lid open laptop"); !strings.Contains(out, "waking up") {
		t.Fatalf("opening the lid must wake it:\n%s", out)
	}
	if !lap.NetUp {
		t.Fatal("the laptop did not wake up")
	}
	if out := run(t, w, lap, "alex", "laptopctl status"); !strings.Contains(out, "lid open") || !strings.Contains(out, "charging") {
		t.Fatalf("the laptop must report its own power state:\n%s", out)
	}

	// a desktop has no lid: the command must say so rather than pretend
	if out := run(t, w, pc, "alex", "laptopctl lid closed"); strings.Contains(out, "suspended") {
		t.Fatalf("a desktop must not suspend:\n%s", out)
	}
}

// §15: "NAS 停止: 共享 / 备份 / 相册功能失败" — the NAS is a dependency, and its
// absence has to break things the household actually relies on.
func TestNASIsADependencyOfBackupsAndRecordings(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	pc.FS.Write("/home/alex/notes.txt", "the household's own file\n", 0644, "alex", "alex")

	// a backup run really copies bytes to the NAS and leaves a record there
	out := run(t, w, pc, "alex", "backup run /home/alex")
	if !strings.Contains(out, "backed up") || !strings.Contains(out, "nas") {
		t.Fatalf("a backup must report where it went:\n%s", out)
	}
	copied, ok := nas.FS.Read(core.BackupDir + "/home-pc/home/alex/notes.txt")
	if !ok || !strings.Contains(string(copied), "the household's own file") {
		t.Fatalf("the backed-up file is not on the NAS: %q", string(copied))
	}
	if out := run(t, w, pc, "alex", "backup status"); !strings.Contains(out, "home-pc:/home/alex") {
		t.Fatalf("the NAS's own backup index must list the run:\n%s", out)
	}

	// the camera's recordings — the household's 相册 — land on the NAS, so the
	// dependency runs both ways: while it is up, an event on a household device
	// produces a clip there
	before, _ := w.Recordings()
	w.AddEvent("cam-alex", "warn", "rtsp", "motion detected at the front door")
	w.Tick()
	after, _ := w.Recordings()
	if len(after) <= len(before) {
		t.Fatalf("the camera did not record to the NAS: %d -> %d clips", len(before), len(after))
	}

	// NAS stopped: backups fail with the real network reason, not a silent skip
	nas.PlugPull()
	if nas.NetUp {
		t.Fatal("the NAS is still up after its plug was pulled")
	}
	if out := run(t, w, pc, "alex", "backup run /home/alex"); !strings.Contains(out, "unreachable") {
		t.Fatalf("a backup without its NAS must fail honestly:\n%s", out)
	}
	// the shares fail too, which is the same dependency from the other side
	if out := run(t, w, pc, "alex", "smbclient -L nas"); !strings.Contains(out, "host is down") {
		t.Fatalf("the NAS's shares must be gone with the NAS:\n%s", out)
	}
	// and the camera cannot record into a NAS that is not there: the clip count
	// stops moving and the camera says why
	w.AddEvent("cam-alex", "warn", "rtsp", "second event with the storage gone")
	for i := 0; i < 2; i++ {
		w.Tick()
	}
	frozen, _ := w.Recordings()
	if len(frozen) != len(after) {
		t.Fatalf("the camera kept recording into a NAS that is down: %d -> %d", len(after), len(frozen))
	}
	log, _ := w.Devices["cam-alex"].FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "recordings dropped") {
		t.Fatalf("the camera did not report its lost storage:\n%s", log)
	}

	// recovery: power the NAS back and the dependency works again
	nas.PlugRestore()
	if out := run(t, w, pc, "alex", "backup run /home/alex"); !strings.Contains(out, "backed up") {
		t.Fatalf("backups must work again once the NAS is back:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "smbclient -L nas"); !strings.Contains(out, "data") {
		t.Fatalf("the shares must be back with the NAS:\n%s", out)
	}
}
