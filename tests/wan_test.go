package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The world's public space is per-AS: one /24 per autonomous system, so a
// prefix has exactly one holder and attribution is a fact, not a guess.
func TestEachPrefixHasExactlyOneOwner(t *testing.T) {
	w := core.NewWorld()
	seen := map[string]int{}
	for _, as := range w.WAN.ASes {
		for _, p := range as.Prefixes {
			if other, dup := seen[p]; dup {
				t.Fatalf("prefix %s is announced by both AS%d and AS%d", p, other, as.ASN)
			}
			seen[p] = as.ASN
			if !strings.Contains(p, "/24") {
				t.Fatalf("malformed prefix %q on AS%d", p, as.ASN)
			}
		}
	}
	if len(seen) < 3 {
		t.Fatalf("expected several announced prefixes, got %v", seen)
	}
}

// Allocation must place each device inside the /24 of the AS that operates it.
func TestPublicAllocationFollowsASOwnership(t *testing.T) {
	w := core.NewWorld()
	_, _, err := w.ProvisionVPS("alex", "nano-1", "edge1")
	if err != nil {
		t.Fatal(err)
	}
	vps := w.Devices["vps-edge1"]
	if vps == nil {
		t.Fatal("vps not provisioned")
	}
	as := w.WAN.ASFor(vps.FirstWANIP())
	if as == nil {
		t.Fatalf("no AS announces the vps address %s", vps.FirstWANIP())
	}
	// §12: the plan's region decides which of the provider's datacenters the
	// node is numbered from, so the org is the provider (or its regional
	// entity) — never anything to do with the customer
	if !strings.HasPrefix(as.Org, "NovaPanel") {
		t.Fatalf("a VPS bought from novapanel should be attributed to its provider, got %s (%s)", as.Org, as.Name)
	}
	if !strings.Contains(as.Abuse, "novapanel.example") {
		t.Fatalf("a VPS's abuse contact must be the provider's, got %s", as.Abuse)
	}
	router := w.Devices["router-alex"]
	ras := w.WAN.ASFor(router.FirstWANIP())
	if ras == nil || ras.Org != "NetCrest Communications" {
		t.Fatalf("a household WAN should be attributed to its ISP, got %v", ras)
	}
}

// The spec is explicit (十三 IP 系统): a public IP must not reveal a person.
// A lookup may return provider-level data and nothing more.
func TestWhoisYieldsProviderDataAndNoIdentity(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "whois "+w.Devices["mirror"].FirstWANIP())
	for _, want := range []string{"origin:", "AS", "org:", "range:", "abuse-mailbox:", "rdns:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("whois output is missing %q:\n%s", want, out)
		}
	}
	// no player's name, password, or device may leak through a registry lookup
	for _, leak := range []string{"alex123", "alex", "Pass:", "home-pc"} {
		if strings.Contains(out, leak) {
			t.Fatalf("whois leaked %q — a registry must not expose identity:\n%s", leak, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "not a person") {
		t.Fatalf("whois should state that the record identifies a network, not a person:\n%s", out)
	}
}

// RFC1918 has no public attribution and no public route.
func TestWhoisRefusesPrivateSpace(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "whois 10.77.1.1")
	if !strings.Contains(out, "private address") {
		t.Fatalf("whois on RFC1918 should say the address is private:\n%s", out)
	}
	if strings.Contains(out, "origin:") {
		t.Fatalf("whois must not invent an AS for private space:\n%s", out)
	}
}

// A private address must stay unreachable from public space even now that the
// public internet really routes.
func TestPrivateSpaceStillUnreachableFromTheInternet(t *testing.T) {
	w := core.NewWorld()
	mirror := w.Devices["mirror"] // a public, world-facing host
	router := w.Devices["router-alex"]
	homeIP := router.FirstLANIP()
	if _, ok := core.Reach(mirror, homeIP); ok {
		t.Fatalf("a public host must not reach a household's RFC1918 address %s", homeIP)
	}
	if _, _, err := core.Dial(mirror, homeIP, 22); err == "" {
		t.Fatalf("TCP to a private address from the internet must fail")
	}
}

// traceroute must name devices that actually exist in the world, and its first
// hop must be the real default gateway — not a fabricated loopback.
func TestTracerouteFollowsRealDevices(t *testing.T) {
	w := core.NewWorld()
	_, _, err := w.ProvisionVPS("alex", "nano-1", "edge1")
	if err != nil {
		t.Fatal(err)
	}
	pc := w.Devices["pc-alex"]
	vpsIP := w.Devices["vps-edge1"].FirstWANIP()
	out := run(t, w, pc, "alex", "traceroute "+vpsIP)

	if strings.Contains(out, "127.0.0.1") {
		t.Fatalf("traceroute must not open with a fabricated loopback hop:\n%s", out)
	}
	gw := pc.GatewayIP()
	if gw == "" {
		t.Fatal("the pc has no default route to trace through")
	}
	if !strings.Contains(out, gw) {
		t.Fatalf("traceroute should pass through the real gateway %s:\n%s", gw, out)
	}
	if !strings.Contains(out, vpsIP) {
		t.Fatalf("traceroute should end at the destination %s:\n%s", vpsIP, out)
	}
	// every named hop must be a real hostname or a network hop
	hostnames := map[string]bool{}
	for _, d := range w.Devices {
		hostnames[d.Hostname] = true
	}
	// an AS edge is a network hop, not a device: its label is the AS name
	asNames := map[string]bool{}
	for _, as := range w.WAN.ASes {
		asNames[as.Name] = true
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "traceroute to") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if name == "*" {
			continue
		}
		if !hostnames[name] && !asNames[name] {
			t.Fatalf("traceroute named %q, which is neither a world device nor an AS edge", name)
		}
	}
}

// bgp must report the transit graph, and a single AS must show its prefixes.
func TestBgpReportsTheTransitGraph(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	out := run(t, w, pc, "alex", "bgp")
	if !strings.Contains(out, "AS64500") || !strings.Contains(out, "NeoCore") {
		t.Fatalf("bgp should list the transit ASes:\n%s", out)
	}
	if !strings.Contains(out, "UPLINK") {
		t.Fatalf("bgp should show uplink relationships:\n%s", out)
	}
	one := run(t, w, pc, "alex", "bgp AS64520")
	if !strings.Contains(one, "NovaPanel") {
		t.Fatalf("bgp AS64520 should describe NovaPanel:\n%s", one)
	}
	if !strings.Contains(one, "announcing:") {
		t.Fatalf("bgp AS64520 should list the prefixes it announces:\n%s", one)
	}
}

// The end-to-end payoff: a VPS is reachable from the home PC over the public
// internet, and the world says so honestly.
func TestVPSIsReachableFromHomeOverTheInternet(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if _, _, err := w.ProvisionVPS("alex", "nano-1", "edge1"); err != nil {
		t.Fatal(err)
	}
	vps := w.Devices["vps-edge1"]
	ip := vps.FirstWANIP()
	if _, ok := core.Reach(pc, ip); !ok {
		t.Fatalf("home should reach the vps public address %s", ip)
	}
	svc, dst, msg := core.Dial(pc, ip, 22)
	if svc == nil {
		t.Fatalf("ssh to the vps should succeed, got %q", msg)
	}
	if dst != vps {
		t.Fatalf("connection landed on %s, not the vps", dst.Hostname)
	}
	// and the reverse direction: the vps reaches the public internet too, but
	// must still NOT reach a household's private space
	mirror := w.Devices["mirror"]
	if _, ok := core.Reach(vps, mirror.FirstWANIP()); !ok {
		t.Fatalf("the vps should reach other public hosts over the internet")
	}
	if _, ok := core.Reach(vps, pc.GatewayIP()); ok {
		t.Fatalf("a public vps must not reach a household's RFC1918 gateway")
	}
	if _, ok := core.Reach(vps, pc.FirstLANIP()); ok {
		t.Fatalf("a public vps must not reach a household's RFC1918 host")
	}
}

// A scripted session with no interactive input must not crash ssh.
func TestSSHWithoutInteractiveInputDoesNotCrash(t *testing.T) {
	w := core.NewWorld()
	_, _, err := w.ProvisionVPS("alex", "nano-1", "edge1")
	if err != nil {
		t.Fatal(err)
	}
	pc := w.Devices["pc-alex"]
	// no SetInput: the shell has no reader at all
	out := run(t, w, pc, "alex", "ssh deploy@192.0.2.1 hostname")
	if strings.Contains(out, "panic") {
		t.Fatalf("ssh without an input stream must not panic:\n%s", out)
	}
}
