package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The household LAN plan has one owner: the dhcp-range the router hands out
// is rendered from the same constants the validator enforces, and every
// seeded static stays outside the band.
func TestLANPlanHasSingleSource(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	conf, ok := router.FS.Read("/etc/dnsmasq.conf")
	if !ok {
		t.Fatal("router has no dnsmasq.conf")
	}
	confText := string(conf)
	// the conf currently points at the (broken) resolv-file — the fault is
	// active — but the range line is the world's own constants either way
	want := "dhcp-range=" + core.LANSubnet + "50," + core.LANSubnet + "200,255.255.255.0,12h"
	if !strings.Contains(confText, want) {
		t.Fatalf("the dnsmasq range is not rendered from the LAN plan:\nwant %q\nconf:\n%s", want, confText)
	}
	// every seeded static on the household LAN must sit outside the band,
	// with no duplicates — which is what ValidateLAN panics on, so NewWorld
	// having returned at all proves it. Assert the shape anyway:
	for _, id := range w.Order {
		d := w.Devices[id]
		for _, i := range d.Ifaces {
			if !strings.HasPrefix(i.IP, core.LANSubnet) {
				continue
			}
			oct := i.IP[len(core.LANSubnet):]
			if oct == "" || oct == "0" || oct == "255" {
				t.Fatalf("%s has unusable household address %s", id, i.IP)
			}
		}
	}
	// the gateway constant is what the clients were seeded with
	resolv, _ := w.Devices["pc-alex"].FS.Read("/etc/resolv.conf")
	if !strings.Contains(string(resolv), "nameserver "+core.LANGateway) {
		t.Fatalf("pc resolv.conf does not point at the plan's gateway:\n%s", resolv)
	}
}

// The MCP device does not carry a magic address: it asks the allocator,
// which cannot hand out an address that is taken or inside the DHCP band.
func TestMCPAddressIsAllocatedNotHardcoded(t *testing.T) {
	w := core.NewWorld()
	d, _, err := w.EnsureMCPPlayer()
	if err != nil {
		t.Fatalf("provisioning failed: %v", err)
	}
	ip := d.Ifaces[0].IP
	// the allocator's answer is the lowest free static: the plan takes
	// .1 (gateway), .11, .20, .21, .22, .30, .40, .43, .250 — so .2 is free
	if ip != core.LANSubnet+"2" {
		t.Fatalf("the MCP device did not get the allocator's lowest free static: %s", ip)
	}
	// idempotent: a second call returns the same device
	d2, _, err := w.EnsureMCPPlayer()
	if err != nil || d2 != d {
		t.Fatalf("second provisioning changed the world: %v %p vs %p", err, d2, d)
	}
	// if something else claims .2, the next allocation moves along — it
	// never collides and never enters the DHCP band
	pc := w.Devices["pc-alex"]
	pc.Ifaces = append(pc.Ifaces, &core.Iface{Name: "eth9", IP: core.LANSubnet + "2", Zone: "lan", Up: true, Mode: "static"})
	next, err := w.AllocLANStatic()
	if err != nil {
		t.Fatalf("allocator failed with .2 taken: %v", err)
	}
	if next != core.LANSubnet+"3" {
		t.Fatalf("with .2 taken the allocator must hand .3, got %s", next)
	}
}

// The validator converts a silent address collision into a loud boot-time
// failure: this is how the .41 MCP const and the DHCP-band phone collided
// twice, undetected until a test noticed a pool was one lease short.
func TestValidateLANPanicsOnBadStatics(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected a panic, got none", name)
			}
		}()
		fn()
	}

	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	mustPanic("static inside the DHCP band", func() {
		pc.Ifaces = append(pc.Ifaces, &core.Iface{Name: "eth9", IP: core.LANSubnet + "60", Zone: "lan", Up: true, Mode: "static"})
		w.ValidateLAN()
	})

	w = core.NewWorld()
	pc = w.Devices["pc-alex"]
	mustPanic("duplicate static", func() {
		pc.Ifaces = append(pc.Ifaces, &core.Iface{Name: "eth9", IP: core.LANSubnet + "11", Zone: "lan", Up: true, Mode: "static"})
		w.ValidateLAN()
	})

	// the untainted world passes, of course
	core.NewWorld().ValidateLAN()
}
