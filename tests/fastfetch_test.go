package tests

import (
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
			want:     []string{"NeoOS 13.2", "Generic Desktop", "x86_64", "eth0[lan]=10.77.1.11", "IPv6: not configured"},
		},
		{
			deviceID: "router-alex",
			user:     "root",
			want:     []string{"NeoWRT 24.10", "Archer C7 (stock)", "mips", "eth0[lan]=10.77.1.1", "eth0[wan]="},
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
	for _, fragment := range []string{"probe", "Processes: 3", "Services: 3 running", "eth1=2001:db8::41", "IPv6: configured"} {
		if !strings.Contains(out, fragment) {
			t.Errorf("fastfetch did not report current simulated state %q:\n%s", fragment, out)
		}
	}
	if strings.Contains(out, "IPv6: not configured") {
		t.Fatalf("configured IPv6 was reported missing:\n%s", out)
	}
}
