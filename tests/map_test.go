package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The topology map renders the household's real wiring: links are L2 state
// (Uplink/UplinkPort, switch ports), so the diagram agrees with the packet
// path and changes when the physical world does. Each test walks a happy
// path, a boundary and a recovery.

func TestTopoDrawsTheHousehold(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := run(t, w, pc, "alex", "topo")
	if !strings.Contains(out, "gateway") {
		t.Fatalf("the map must be centred on the router:\n%s", out)
	}
	// the seeded wiring is visible: laptop on the switch, camera PoE
	if !strings.Contains(out, "laptop") || !strings.Contains(out, "cam-front") {
		t.Fatalf("the map must show switched devices:\n%s", out)
	}
	if !strings.Contains(out, "PoE") {
		t.Fatalf("PoE ports must be marked:\n%s", out)
	}
	// and the map is reachable under its alias
	if out := run(t, w, pc, "alex", "map"); !strings.Contains(out, "gateway") {
		t.Fatalf("map alias failed:\n%s", out)
	}
}

func TestTopoFollowsTheRealWorld(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// pull the laptop's port: the map must say so, like the report does
	run(t, w, w.Devices["sw-alex"], "root", "switchctl port 3 down")
	out := run(t, w, pc, "alex", "topo")
	if !strings.Contains(out, "admin-down") {
		t.Fatalf("the map must show the admin-down port:\n%s", out)
	}
	// ...and the packet path agrees with the drawing
	if _, ok := core.Reach(pc, w.Devices["laptop-alex"].FirstLANIP()); ok {
		t.Fatal("a down port means the laptop is not reachable")
	}
	run(t, w, w.Devices["sw-alex"], "root", "switchctl port 3 up")
	if out := run(t, w, pc, "alex", "topo"); strings.Contains(out, "admin-down") {
		t.Fatalf("recovery must show in the map:\n%s", out)
	}
}
