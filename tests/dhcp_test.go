package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// DHCP must be real on both ends: the server records the lease it handed out,
// and the client's interface really takes the address. A lease that exists only
// on one side is bookkeeping, not DHCP.
func TestDHCPLeaseIsRecordedOnBothEnds(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	l, err := pc.DHCPRenew()
	if err != nil {
		t.Fatalf("a client should be able to get a lease: %v", err)
	}
	if l.IP == "" || l.GW == "" {
		t.Fatalf("an incomplete lease: %+v", l)
	}
	// server side: the router remembers it
	mac := pc.Ifaces[0].MAC
	if got, ok := router.DHCPL[mac]; !ok {
		t.Fatalf("the router did not record a lease for %s", mac)
	} else if got.IP != l.IP {
		t.Fatalf("the router thinks %s holds %s but the client got %s", mac, got.IP, l.IP)
	}
	// client side: the address is now routed to the client
	if w.IPMap[l.IP] != pc.ID {
		t.Fatalf("leased address %s does not route to %s (maps to %q)", l.IP, pc.ID, w.IPMap[l.IP])
	}
	// and the client uses the leased gateway
	if pc.GatewayIP() == "" {
		t.Fatal("a client with a valid lease should have a default route")
	}
}

// Real DHCP is sticky: a client that renews keeps its address, and the server
// must not leak a second entry.
func TestDHCPLeaseIsSticky(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	first, err := pc.DHCPRenew()
	if err != nil {
		t.Fatal(err)
	}
	second, err := pc.DHCPRenew()
	if err != nil {
		t.Fatal(err)
	}
	if first.IP != second.IP {
		t.Fatalf("renewing changed the address: %s -> %s", first.IP, second.IP)
	}
	if len(router.DHCPL) != 1 {
		t.Fatalf("renewing leaked leases: the router holds %d", len(router.DHCPL))
	}
}

// With the DHCP server down there is no lease — and therefore no default route.
// This is the failure a player must be able to see and fix, so it cannot silently
// succeed.
func TestDHCPFailsWhenServerIsDown(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	router.StopService("dnsmasq")
	if _, err := pc.DHCPRenew(); err == nil {
		t.Fatal("renewing must fail when the DHCP server is not running")
	}
	// and with the whole router dark, equally so
	router.StartService("dnsmasq")
	w.CutPower("alex")
	if _, err := pc.DHCPRenew(); err == nil {
		t.Fatal("renewing must fail when the DHCP server has no power")
	}
}

// A pool is finite. A router must refuse rather than hand out addresses it does
// not own, and a released address must become available again.
func TestDHCPPoolIsFiniteAndReusable(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]

	// the pool is declared in the router's own config
	lo, hi, _, ok := core.DHCPPoolInfo(router)
	if !ok {
		t.Fatal("the router should have a dhcp-range configured")
	}
	size := hi - lo + 1
	if size < 2 {
		t.Fatalf("implausible pool size %d", size)
	}

	n := 0
	var firstMAC string
	for i := 0; i < size+50; i++ {
		fake := &core.Device{W: w, ID: "fake" + string(rune('a'+i%26)) + string(rune('0'+i/26)), PowerOK: true, NetUp: true}
		mac := "52:54:00:aa:bb:" + string(rune('a'+i%26)) + string(rune('0'+i/26*7%10))
		if n == 0 {
			firstMAC = mac
		}
		if _, err := router.DHCPLease(fake, mac); err != nil {
			break
		}
		n++
	}
	if n != size {
		t.Fatalf("a %d-address pool handed out %d leases", size, n)
	}

	// releasing must free exactly one
	router.DHCPRelease(firstMAC)
	if len(router.DHCPL) != size-1 {
		t.Fatalf("release did not free the address: %d leases left", len(router.DHCPL))
	}
	fake := &core.Device{W: w, ID: "reuse", PowerOK: true, NetUp: true}
	if _, err := router.DHCPLease(fake, "52:54:00:99:99:99"); err != nil {
		t.Fatalf("the freed address should be reusable: %v", err)
	}
}

// Cutting the dhcp-range out of the router's config must stop it serving leases:
// the config file is the truth, not the service list.
func TestDHCPPoolComesFromTheRouterConfig(t *testing.T) {
	w := core.NewWorld()
	router := w.Devices["router-alex"]

	if !router.DHCPServerRunning() {
		t.Fatal("the router should be serving DHCP to begin with")
	}
	data, _ := router.FS.Read("/etc/dnsmasq.conf")
	broken := strings.Replace(string(data), "dhcp-range=", "#dhcp-range=", -1)
	router.FS.Write("/etc/dnsmasq.conf", broken, 0644, "root", "root")

	if router.DHCPServerRunning() {
		t.Fatal("a router with no dhcp-range configured is not a DHCP server")
	}
	if _, err := router.DHCPLease(w.Devices["pc-alex"], "52:54:00:00:00:01"); err == nil {
		t.Fatal("it must refuse to serve leases without a configured pool")
	}
}

// udhcpc over the shell must change the world, not just print a message.
func TestUDHCPCCommandChangesTheWorld(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	out := run(t, w, pc, "alex", "udhcpc")
	if strings.Contains(out, "not found") {
		t.Fatalf("udhcpc should exist:\\n%s", out)
	}
	if len(router.DHCPL) == 0 {
		t.Fatalf("udhcpc must really take a lease:\\n%s", out)
	}

	// the router's own view must agree
	view := run(t, w, router, "root", "leases")
	if !strings.Contains(view, "10.77.1.") {
		t.Fatalf("`leases` should list the lease that was just taken:\\n%s", view)
	}

	// and with the server down it must fail loudly rather than pretend
	router.StopService("dnsmasq")
	bad := run(t, w, pc, "alex", "udhcpc")
	if !strings.Contains(bad, "no") {
		t.Fatalf("udhcpc with no server should report failure:\\n%s", bad)
	}
}
