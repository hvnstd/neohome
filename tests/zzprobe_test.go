package tests

import (
	"fmt"
	"testing"

	"neohome/internal/core"
)

func TestZZProbe(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	mirror := w.Devices["mirror"]
	router := w.Devices["router-alex"]
	fmt.Println("mirror lan:", mirror.FirstLANIP(), "wan:", mirror.FirstWANIP())
	fmt.Println("router lan:", router.FirstLANIP(), "wan:", router.FirstWANIP())
	fmt.Println("core-gw lan:", w.Devices["core-gw"].FirstLANIP(), "wan:", w.Devices["core-gw"].FirstWANIP())

	fmt.Println("-- PC -> mirror PUBLIC:80")
	_, _, msg := core.Dial(pc, mirror.FirstWANIP(), 80)
	fmt.Println("   ", msg)

	fmt.Println("-- PC -> mirror LAN(10.0.0.4):80")
	_, _, msg = core.Dial(pc, "10.0.0.4", 80)
	fmt.Println("   ", msg)

	fmt.Println("-- Reach PC -> mirror PUBLIC")
	m, ok := core.Reach(pc, mirror.FirstWANIP())
	fmt.Println("   ", m, ok)

	vps, creds, err := w.ProvisionVPS("alex", "small-2", "edge1")
	fmt.Println("-- VPS:", vps.ID, vps.FirstWANIP(), creds, err)
	fmt.Println("-- PC -> VPS public:22")
	_, _, msg = core.Dial(pc, vps.FirstWANIP(), 22)
	fmt.Println("   ", msg)
	fmt.Println("-- Reach PC -> VPS public")
	m, ok = core.Reach(pc, vps.FirstWANIP())
	fmt.Println("   ", m, ok)

	fmt.Println("-- VPS -> PC lan 10.77.1.11:22")
	_, _, msg = core.Dial(vps, "10.77.1.11", 22)
	fmt.Println("   ", msg)
	fmt.Println("-- VPS -> router wan (public of home)")
	_, _, msg = core.Dial(vps, router.FirstWANIP(), 22)
	fmt.Println("   ", msg)
	fmt.Println("-- npc-router wan ->", w.Devices["npc-router"].FirstWANIP())
	fmt.Println("-- IPs allocated: pubcounter", w.PublicCounter)
}
