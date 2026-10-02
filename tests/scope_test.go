package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// A socket bound to the LAN must not be advertised as listening on the router's
// WAN address. Dial already refuses a LAN-only service from the WAN, so printing
// one on 198.51.100.1 told an attacker the router's ssh and telnet were exposed
// to the internet when they were not.
func TestSsDoesNotAdvertiseLanSocketsOnTheWan(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]
	root := router.FindUser("root")
	if root == nil {
		t.Fatal("router has no root")
	}
	out := &bufOut{}
	sh := shell.NewShell(w, router, root, out, "10.77.1.1", "xterm")
	sh.ExecLine("ss -tlnp")
	got := out.String()

	if !strings.Contains(got, "10.77.1.1:23") {
		t.Fatalf("the LAN-scoped telnetd should listen on the LAN address:\n%s", got)
	}
	if strings.Contains(got, "198.51.100.1:23") {
		t.Fatalf("a LAN-only telnetd must not appear on the WAN address:\n%s", got)
	}
	if strings.Contains(got, "198.51.100.1:22") {
		t.Fatalf("a LAN-only ssh must not appear on the WAN address:\n%s", got)
	}
}

// The scope is enforced for real, not just hidden from ss: a LAN-only service
// must refuse a connection that arrives from the WAN.
func TestLanOnlyServiceRefusesWanClients(t *testing.T) {
	w := core.NewWorld()
	vps := w.Devices["isp-dns"]
	router := w.Devices["router-alex"]
	if vps == nil || router == nil {
		t.Skip("world lacks an internet-side device or a router")
	}
	// isp-dns sits on the public internet; the router's telnetd is LAN-only
	svc, _, msg := core.Dial(vps, "198.51.100.1", 23)
	if svc != nil {
		t.Fatalf("a LAN-only service accepted a WAN connection: %s", msg)
	}
	// Either the router's firewall drops it ("filtered") or the service's scope
	// refuses it ("LAN only"). Both are correct; silence would not be.
	if !strings.Contains(msg, "filtered") && !strings.Contains(msg, "LAN only") {
		t.Fatalf("the WAN connection should be refused, got: %q", msg)
	}
}
