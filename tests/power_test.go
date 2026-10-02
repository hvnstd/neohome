package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Power is only real if cutting it changes what the network does. If a dark
// machine still answers, the "outage" is a message the player reads rather than
// a world that stopped working.
func TestCuttingPowerReallyTakesTheHouseDown(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	// baseline: the house works
	if st, ok := core.Reach(pc, "10.77.1.1"); !ok {
		t.Fatalf("house should be up before the cut, got %q", st)
	}
	if _, _, msg := core.Dial(pc, "10.77.1.1", 22); msg != "connected" {
		t.Fatalf("ssh to the router should work before the cut, got %q", msg)
	}

	w.CutPower("alex")

	// the network must genuinely stop answering
	if st, ok := core.Reach(pc, "10.77.1.1"); ok {
		t.Fatalf("a dark hosts must not reach anything, got %q", st)
	}
	if _, _, msg := core.Dial(pc, "10.77.1.1", 22); msg == "connected" {
		t.Fatal("ssh to the router must fail once the power is out")
	}

	// and the world must not contradict itself: services report stopped
	if router.Services["dnsmasq"].State != "stopped" {
		t.Fatalf("dnsmasq still claims %q after the power cut", router.Services["dnsmasq"].State)
	}
	if router.NetUp {
		t.Fatal("the router still thinks it is up while the house is dark")
	}
	if len(router.Procs) != 0 {
		t.Fatalf("a dark machine should have no running processes, has %d", len(router.Procs))
	}
}

// A datacenter host is not on the household circuit. An outage that took the
// provider's servers down would make the whole public internet depend on one
// player's breaker, which is nonsense.
func TestOutageDoesNotTouchDatacenterHosts(t *testing.T) {
	w := core.NewWorld()
	w.CutPower("alex")
	for _, id := range []string{"core-gw", "isp-dns", "bank", "jobs"} {
		d := w.Devices[id]
		if !d.Powered() {
			t.Fatalf("%s (profile %s) should be unaffected by a household outage", id, d.Profile)
		}
		if d.Services["sshd"] != nil && d.Services["sshd"].State != "running" {
			t.Fatalf("%s stopped its sshd because a house lost power", id)
		}
	}
}

// Restoring power must bring the house back the way it was, not leave it in a
// half-dead state where the network works but the services do not.
func TestRestoringPowerBringsTheHouseBack(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	before := map[string]string{}
	for name, svc := range router.Services {
		before[name] = svc.State
	}

	w.CutPower("alex")
	w.RestorePower("alex")

	if !w.HouseholdPower() {
		t.Fatal("the household supply should be back")
	}
	for name, want := range before {
		if want != "running" {
			continue
		}
		if got := router.Services[name].State; got != "running" {
			t.Fatalf("service %s was running before the outage, is %q after restore", name, got)
		}
	}
	if st, ok := core.Reach(w.Devices["pc-alex"], "10.77.1.1"); !ok {
		t.Fatalf("the house should be reachable again after restore, got %q", st)
	}
}

// A UPS is the counter-play: it keeps a machine alive through an outage, and it
// must run out — otherwise power stops mattering for anyone who buys one.
func TestUPSCarriesAMachineThenRunsOut(t *testing.T) {
	w := core.NewWorld()
	nas := w.Devices["nas-alex"]
	pc := w.Devices["pc-alex"]
	nas.UPS = &core.UPSInfo{ChargePct: 100, LastState: "online"}

	w.CutPower("alex")

	if !nas.Powered() {
		t.Fatal("a UPS-backed machine should still be powered during an outage")
	}
	if !nas.OnBattery() {
		t.Fatal("a UPS-backed machine should report it is on battery")
	}
	// it stays a real host on the LAN while the PC is dark
	if pc.Powered() {
		t.Fatal("the un-backed-up PC should be dark")
	}

	// drain it: enough ticks must exhaust the battery
	for i := 0; i < 600 && nas.UPS.ChargePct > 0; i++ {
		w.PowerTick()
	}
	if nas.UPS.ChargePct != 0 {
		t.Fatalf("the battery should eventually run out, still at %d%%", nas.UPS.ChargePct)
	}
	w.PowerTick() // the tick that notices zero and shuts it down
	if nas.Powered() {
		t.Fatal("an exhausted UPS must stop holding the machine up")
	}
	if nas.NetUp {
		t.Fatal("a machine whose battery died must go down")
	}
}

// Pulling one plug must darkness that device without darkening the house — and
// plugging it back in must boot it. This is how a player works on their own
// machine without cutting everyone off.
func TestSingleDevicePlugPullDoesNotCutTheHouse(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	pc.PlugPull()

	if pc.Powered() {
		t.Fatal("an unplugged machine should be down")
	}
	if !w.HouseholdPower() {
		t.Fatal("pulling one plug must not cut the household supply")
	}
	if !router.Powered() {
		t.Fatal("the router should still be up after one PC is unplugged")
	}

	pc.PlugRestore()
	if !pc.Powered() || !pc.NetUp {
		t.Fatal("plugging the machine back in should boot it")
	}
}

// A suspended utility account is a second, independent way to lose power, and
// it must have the same physical consequence as throwing the breaker.
func TestUtilitySuspensionAlsoDarkensTheHouse(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	if !w.HouseholdPower() {
		t.Fatal("the world should start with power")
	}

	// suspension alone, with no breaker involved
	w.UtilitiesSuspended = true
	w.ApplyOutage()
	if w.HouseholdPower() {
		t.Fatal("a suspended account means no mains power")
	}
	if st, ok := core.Reach(pc, "10.77.1.1"); ok {
		t.Fatalf("a house with suspended service must not reach the router, got %q", st)
	}
}

// Cutting the power must not be a one-way door. The management controller is on
// its own battery, so the operator can always get back in and bring the house
// up — otherwise the mechanic would brick the player's own game.
func TestOutOfBandControllerSurvivesTheOutage(t *testing.T) {
	w := core.NewWorld()
	bmc := w.OutOfBand()
	if bmc == nil {
		t.Fatal("the household should have a management controller")
	}

	w.CutPower("alex")

	if !bmc.Powered() {
		t.Fatal("the controller runs on its own battery and must survive the outage")
	}
	if bmc.NetUp == false {
		t.Fatal("the controller must stay reachable during an outage")
	}
	if !bmc.OnBattery() {
		t.Fatal("the controller should report it is on battery")
	}
	// the rest of the house is genuinely down
	if w.Devices["pc-alex"].Powered() {
		t.Fatal("the household PC should be dark")
	}
	// and the way back in works
	if !w.HouseholdPower() {
		w.RestorePower("admin")
	}
	if !w.HouseholdPower() {
		t.Fatal("restoring from the controller must work")
	}
	if !w.Devices["pc-alex"].Powered() {
		t.Fatal("the PC should be back after the controller started the house")
	}
}

// An outage must not be a message plus a working network. Any service that the
// world still calls "running" on a dark machine is a contradiction the player
// would rightly stop trusting.
func TestDarkMachinesHaveNoRunningServices(t *testing.T) {
	w := core.NewWorld()
	w.CutPower("alex")
	for _, id := range w.Order {
		d := w.Devices[id]
		if d.Powered() || d.IsDataCenter() {
			continue
		}
		for name, svc := range d.Services {
			if svc.State == "running" {
				t.Fatalf("%s claims service %s is running while the machine has no power",
					d.Hostname, name)
			}
		}
		if len(d.Procs) > 0 {
			t.Fatalf("%s has %d processes while dark", d.Hostname, len(d.Procs))
		}
	}
}

// The reported supply state must agree with what the world actually does. A
// status line that says "up" while Reach fails is the bug this guards.
func TestPowerReportMatchesReality(t *testing.T) {
	w := core.NewWorld()
	w.CutPower("alex")

	report := strings.Join(w.PowerReport(), "\n")
	if !strings.Contains(report, "CUT") {
		t.Fatalf("the report should say the breaker is open:\n%s", report)
	}
	// every household machine the report calls "down" must really be unreachable
	pc := w.Devices["pc-alex"]
	if st, ok := core.Reach(pc, "10.77.1.1"); ok {
		t.Fatalf("report claims an outage but Reach succeeded (%q) — the report is lying", st)
	}
	for _, line := range w.PowerReport() {
		if strings.Contains(line, "gateway") && strings.Contains(line, " up") {
			t.Fatalf("the router is reported up during an outage:\n%s", line)
		}
	}
}
