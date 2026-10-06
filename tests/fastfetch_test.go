package tests

import (
	"fmt"
	"strings"
	"testing"

	"neohome/internal/core"
)

func TestFastfetchReportsDeviceSpecificState(t *testing.T) {
	w := core.NewWorld()
	for _, tc := range []struct {
		deviceID string
		user     string
		want     []string
	}{
		{
			deviceID: "pc-alex",
			user:     "alex",
			// §13: the household has real v6 — the host reports both its
			// global address and its ULA, and not the link-local one every
			// v6 interface has whether or not it can reach anything
			want: []string{"NeoOS 13.2", "Generic Desktop", "x86_64", "eth0[lan]=10.77.1.11", "IPv6: configured (2001:db8:fbfe:10::b/64", "fd00:77:1::b/64"},
		},
		{
			deviceID: "router-alex",
			user:     "root",
			want:     []string{"NeoWRT 24.10", "Archer C7 (stock)", "mips", "eth0[lan]=10.77.1.1", "eth0.2[wan]="},
		},
		{
			deviceID: "asst-alex",
			user:     "assistant",
			want:     []string{"Alpine 3.20", "Assistant Mini-PC", "x86_64", "eth0[lan]=10.77.1.20"},
		},
	} {
		t.Run(tc.deviceID, func(t *testing.T) {
			d := w.Devices[tc.deviceID]
			out := run(t, w, d, tc.user, "fastfetch")
			for _, fragment := range tc.want {
				if !strings.Contains(out, fragment) {
					t.Errorf("fastfetch output does not contain %q:\n%s", fragment, out)
				}
			}
			if strings.Contains(out, "IPv6: ::") {
				t.Fatalf("fastfetch fabricated an IPv6 address:\n%s", out)
			}
		})
	}
}

func TestFastfetchReflectsChangesToSimulatedState(t *testing.T) {
	w := core.NewWorld()
	d := w.Devices["pc-alex"]
	d.Installed["probe"] = &core.VPkg{Name: "probe"}
	d.AddProc(&core.Proc{Name: "probe-daemon", User: "alex", Mem: 128, CPU: 4})
	d.Ifaces = append(d.Ifaces, &core.Iface{Name: "eth1", IP: "2001:db8::41", Up: true})
	d.Services["probe"] = &core.Service{Name: "probe", State: "running"}

	out := run(t, w, d, "alex", "fastfetch")

	// Derive the expected counts from the device instead of hardcoding them.
	// The point of this test is that fastfetch reports *current* state, so the
	// expectation has to be current too: seeding a device with another daemon
	// must move the number, not silently desync the assertion from reality.
	wantProcs, wantSvcs := 0, 0
	for _, p := range d.Procs {
		if p.Name != "" {
			wantProcs++
		}
	}
	for _, s := range d.Services {
		if s.State == "running" {
			wantSvcs++
		}
	}

	for _, fragment := range []string{
		"probe",
		fmt.Sprintf("Processes: %d", wantProcs),
		fmt.Sprintf("Services: %d running", wantSvcs),
		"eth1=2001:db8::41",
		"IPv6: configured",
	} {
		if !strings.Contains(out, fragment) {
			t.Errorf("fastfetch did not report current simulated state %q:\n%s", fragment, out)
		}
	}
	if strings.Contains(out, "IPv6: not configured") {
		t.Fatalf("configured IPv6 was reported missing:\n%s", out)
	}
}
