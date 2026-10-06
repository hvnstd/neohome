package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §13 的 IP 系统: every address in this world has a kind, a family and an owner,
// and the kind decides what the address can do. These tests hold the world to
// that — a "public IPv6" that nothing can reach, or a carrier-grade-NAT address
// that quietly forwards, would be worse than not having the feature at all.

func dialTo(src *core.Device, ip string, port int) string {
	if ip == "" {
		return "no address"
	}
	_, _, msg := core.Dial(src, ip, port)
	return msg
}

// Every kind §13 lists is classified, and the classification is what the
// commands act on: a lookup that invented a holder for private space, or that
// answered for an address nobody announces, would be a lie a player could not
// tell from a real one.
func TestAddressKindsCoverTheSpec(t *testing.T) {
	for _, tc := range []struct {
		ip       string
		wantKind string
		scope    string
		public   bool
	}{
		{"203.0.113.7", core.KindPublicV4, "world", true},
		{"2001:db8:fbfe:10::b", core.KindPublicV6, "world", true},
		{"10.77.1.11", core.KindPrivate, "lan", false},
		{"192.168.1.5", core.KindPrivate, "lan", false},
		{"100.64.3.9", core.KindShared, "carrier", false},
		{"127.0.0.1", core.KindLoopback, "host", false},
		{"169.254.4.4", core.KindLinkLocal, "link", false},
		{"fd00:77:1::b", core.KindULA, "lan", false},
		{"fe80::11", core.KindLinkLocal, "link", false},
		{"::1", core.KindLoopback, "host", false},
		{"172.31.9.9", core.KindPrivate, "lan", false},
	} {
		info := core.ClassifyAddr(tc.ip)
		if info.Kind != tc.wantKind || info.Scope != tc.scope || info.Public != tc.public {
			t.Errorf("ClassifyAddr(%s) = %s/%s/public=%v, want %s/%s/public=%v",
				tc.ip, info.Kind, info.Scope, info.Public, tc.wantKind, tc.scope, tc.public)
		}
		if got := core.RecordType(tc.ip); (got == "AAAA") != (info.Family == 6) {
			t.Errorf("RecordType(%s) = %s but family is %d", tc.ip, got, info.Family)
		}
	}
	if core.ClassifyAddr("mirror.neohome.example").Family != 0 {
		t.Error("a hostname must not classify as an address")
	}

	// and the world's own commands agree
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	// RFC1918, loopback and ULA have no registry record — and naming the kind
	// is what makes the refusal useful instead of a dead end
	for _, tc := range []struct {
		ip, want string
	}{
		{"10.77.1.1", "private address"},
		{"127.0.0.1", "loopback address"},
		{"fd00:77:1::b", "ULA address"},
		{"fe80::11", "link-local address"},
	} {
		out := sh("whois " + tc.ip)
		if !strings.Contains(out, tc.want) {
			t.Errorf("whois %s should name the kind (%q):\n%s", tc.ip, tc.want, out)
		}
		if strings.Contains(out, "origin:") {
			t.Errorf("whois %s invented an AS for an address with no registry record:\n%s", tc.ip, out)
		}
	}

	// a public v6 address has a real owner, and still no identity. The
	// household's own address is announced by its ISP, which is the case worth
	// checking: the record names NetCrest, not Alex.
	out := sh("whois " + w.Devices["router-alex"].FirstWANv6())
	for _, want := range []string{"origin:       AS64510 NetCrest ISP", "NetCrest ISP", "range:        2001:db8:", "not a person"} {
		if !strings.Contains(out, want) {
			t.Errorf("whois on a v6 address is missing %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"alex123", "home-pc", "Pass"} {
		if strings.Contains(out, leak) {
			t.Errorf("whois leaked %q:\n%s", leak, out)
		}
	}
}

// The v6 stack is on every interface, per network: loopback ::1, a link-local,
// a ULA on the household LAN and a global address inside the /64 the router was
// delegated. None of it is decoration — the addresses are what the packet path
// resolves.
func TestEveryDeviceHasARealV6Stack(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	router := w.Devices["router-alex"]
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	cam := w.Devices["cam-alex"]

	lanPrefix := core.V6LANPrefixForTest(router)
	if !strings.HasPrefix(lanPrefix, "2001:db8:") || !strings.HasSuffix(lanPrefix, "::/64") {
		t.Fatalf("the router's LAN prefix is not a delegated /64: %q", lanPrefix)
	}
	if !strings.HasPrefix(pc.FirstWANv6(), strings.TrimSuffix(lanPrefix, "::/64")) {
		t.Fatalf("the PC's address %s must be inside the router's %s", pc.FirstWANv6(), lanPrefix)
	}
	if router.FirstWANv6() == "" || nas.FirstWANv6() == "" || cam.FirstWANv6() == "" {
		t.Fatal("every device on a public network must have a global v6 address")
	}
	// a host's address is inside its router's prefix, keyed on its v4 last
	// octet: the two families of one machine are readable against each other
	if got := pc.FirstWANv6(); !strings.HasSuffix(got, "::b") {
		t.Fatalf("the PC's global v6 address should be ::b for 10.77.1.11, got %s", got)
	}
	if got := cam.FirstWANv6(); !strings.HasSuffix(got, "::28") {
		t.Fatalf("the camera's global v6 address should be ::28 for 10.77.1.40, got %s", got)
	}
	if !core.IsV6(pc.FirstWANv6()) || core.ClassifyAddr(pc.FirstWANv6()).Kind != core.KindPublicV6 {
		t.Fatal("a LAN host's global address must classify as public IPv6")
	}

	sh := func(line string) string { return run(t, w, pc, "alex", line) }
	out := sh("ip addr")
	for _, want := range []string{"inet6 ::1/128 scope host lo", "inet6 2001:db8:", "scope link eth0", "inet6 fd00:77:1::"} {
		if !strings.Contains(out, want) {
			t.Errorf("`ip addr` is missing %q:\n%s", want, out)
		}
	}
	// only the requested family, like the real tool
	if out := sh("ip -6 addr show eth0"); strings.Contains(out, "\n    inet ") {
		t.Errorf("`ip -6 addr` must not print IPv4 addresses:\n%s", out)
	}
	if out := sh("ip -4 addr show eth0"); strings.Contains(out, "inet6") {
		t.Errorf("`ip -4 addr` must not print IPv6 addresses:\n%s", out)
	}
	// and the route table has the on-link prefix and a v6 default on the router
	rout := run(t, w, router, "root", "ip -6 route")
	if !strings.Contains(rout, "default via 2001:db8:") || !strings.Contains(rout, lanPrefix) {
		t.Errorf("the router's v6 routing table is not real:\n%s", rout)
	}
	if !strings.Contains(sh("fastfetch"), "IPv6: configured") {
		t.Error("fastfetch must report the v6 stack it actually has")
	}
}

// IPv6 has no NAT, so publishing a host is a firewall rule rather than a
// port-forward — and the host's own ruleset is still a gate. This is the whole
// §13/§14 coupling, and both halves have to be true for the lesson to hold.
func TestIPv6IsPublishedByRuleAndStillFiltered(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	net := w.Devices["mirror"]
	router := w.Devices["router-alex"]
	cam := w.Devices["cam-alex"]
	pc := w.Devices["pc-alex"]
	v, _, err := w.ProvisionVPSWithOS("alex", "small-2", "six", "debian")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	sh := func(line string) string { return run(t, w, router, "root", line) }

	// a public node answers on v6 exactly as it does on v4
	if msg := dialTo(net, v.FirstWANv6(), 22); msg != "connected" {
		t.Fatalf("a VPS must be reachable over its v6 address, got %q", msg)
	}
	// the household is closed on both families by default
	for _, d := range []*core.Device{pc, cam} {
		if msg := dialTo(net, d.FirstWANv6(), 22); !strings.Contains(msg, "filtered") {
			t.Fatalf("%s must be closed over v6 by default, got %q", d.Hostname, msg)
		}
	}
	// ICMP agrees with TCP: routing is the question, not the listener
	if msg, ok := core.Reach(net, pc.FirstWANv6()); ok || !strings.Contains(msg, "filtered") {
		t.Fatalf("ping over v6 to a closed household must be filtered, got %q/%v", msg, ok)
	}

	// step one: the router rule. There is no redirect to write, because there
	// is nothing to translate.
	for _, line := range []string{
		"uci add firewall rule cam-v6",
		"uci set firewall.cam-v6.src=wan",
		"uci set firewall.cam-v6.proto=tcp",
		"uci set firewall.cam-v6.dest_ip=" + cam.FirstWANv6(),
		"uci set firewall.cam-v6.dest_port=554",
		"uci set firewall.cam-v6.target=ACCEPT",
	} {
		sh(line)
	}
	// before the commit nothing changed
	if msg := dialTo(net, cam.FirstWANv6(), 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("an uncommitted v6 rule must change nothing, got %q", msg)
	}
	sh("uci commit firewall")
	if msg := dialTo(net, cam.FirstWANv6(), 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("the router rule alone must not pass the camera's own policy, got %q", msg)
	}
	if msg := dialTo(net, pc.FirstWANv6(), 22); !strings.Contains(msg, "filtered") {
		t.Fatalf("a rule naming one host must not publish another, got %q", msg)
	}

	// step two: the host's own v6 ruleset. A non-root user cannot write it.
	out := run(t, w, pc, "alex", "ip6tables -A INPUT -p tcp --dport 554 -j ACCEPT")
	if !strings.Contains(out, "Permission denied") {
		t.Fatalf("ip6tables must refuse a non-root user:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "ip6tables -L"); !strings.Contains(out, "Permission denied") {
		t.Fatalf("ip6tables -L must be refused to a non-root user too:\n%s", out)
	}
	run(t, w, cam, "root", "ip6tables -A INPUT -p tcp --dport 554 -j ACCEPT")
	if msg := dialTo(net, cam.FirstWANv6(), 554); msg != "connected" {
		t.Fatalf("with both gates open the camera must answer on v6, got %q", msg)
	}
	// the ruleset is a file, and it is the file the packet path reads
	data, ok := cam.FS.Read("/etc/iptables/rules.v6")
	if !ok || !strings.Contains(string(data), "--dport 554 -j ACCEPT") {
		t.Fatalf("the v6 ruleset must be a real file:\n%s", string(data))
	}
	listing := run(t, w, cam, "root", "ip6tables -L")
	if !strings.Contains(listing, "[ipv6]") || !strings.Contains(listing, "dport 554") {
		t.Fatalf("ip6tables -L must read the v6 ruleset:\n%s", listing)
	}

	// recovery: closing both gates closes the hole, and the v4 side of the
	// same camera was never involved
	run(t, w, cam, "root", "ip6tables -D INPUT -p tcp --dport 554 -j ACCEPT")
	if msg := dialTo(net, cam.FirstWANv6(), 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("removing the host rule must close it, got %q", msg)
	}
	run(t, w, cam, "root", "ip6tables -A INPUT -p tcp --dport 554 -j ACCEPT")
	sh("uci delete firewall.cam-v6")
	sh("uci commit firewall")
	if msg := dialTo(net, cam.FirstWANv6(), 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("removing the router rule must close it, got %q", msg)
	}
	if msg := dialTo(pc, cam.FirstLANIP(), 554); msg != "connected" {
		t.Fatalf("the LAN was never affected, got %q", msg)
	}
}

// Names publish both families: `dig -t AAAA` is a different question from
// `dig`, and a name with only an A record must say so rather than inventing a
// v6 address.
func TestAAAARecordsArePublishedAndBounded(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("dig -t AAAA mirror.neohome.example")
	if !strings.Contains(out, "IN\tAAAA\t"+w.Devices["mirror"].FirstWANv6()) {
		t.Fatalf("the mirror's AAAA record must resolve to its real v6 address:\n%s", out)
	}
	// the same name still has its A record, and plain dig asks for it
	if out := sh("dig mirror.neohome.example"); !strings.Contains(out, "IN\tA\t"+w.Devices["mirror"].FirstWANIP()+"/") &&
		!strings.Contains(out, "IN\tA\t") {
		t.Fatalf("the A record must still resolve:\n%s", out)
	}
	// home.alex resolves to the router's v6 address too: the household is
	// dual-stack by name
	if out := sh("nslookup -type=AAAA home.alex.neohome.example"); !strings.Contains(out, router.FirstWANv6()) {
		t.Fatalf("home.alex must have an AAAA record:\n%s", out)
	}
	// an IPv4 literal has no AAAA, and the answer says why
	if out := sh("dig -t AAAA 203.0.113.3"); !strings.Contains(out, "NXDOMAIN") {
		t.Fatalf("an IPv4 literal must not answer an AAAA query:\n%s", out)
	}
	// a name nobody publishes is NXDOMAIN in both families
	if out := sh("dig -t AAAA nosuch.neohome.example"); !strings.Contains(out, "NXDOMAIN") {
		t.Fatalf("an unpublished name must be NXDOMAIN:\n%s", out)
	}
}

// §13's carrier-grade NAT and §12's plan list meet here: the cheap plan has no
// routable IPv4, outbound traffic leaves under the provider's address, and the
// node's own address cannot be reached — which is exactly why the plan also
// ships IPv6.
func TestCGNATPlanIsOutboundOnlyAndV6IsTheWayIn(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	net := w.Devices["mirror"]
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "alex", "vps list"); !strings.Contains(out, "shared (CGNAT)") ||
		!strings.Contains(out, "IPv6 only") {
		t.Fatalf("the plan list must show what each plan's networking really is:\n%s", out)
	}

	v, creds, err := w.ProvisionVPSWithOS("alex", "nano-shared", "cheap", "debian")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	pw := v.FindUser("deploy").Pass
	shared := core.WANIPOf(v)
	if !strings.HasPrefix(shared, "100.64.") || !v.NATed {
		t.Fatalf("a shared plan must number the node inside the CGNAT pool, got %s", shared)
	}
	if v.FirstWANv6() == "" {
		t.Fatal("the shared plan must still ship a real IPv6 address")
	}

	// the address is on the interface — that is what makes it plausible — and
	// the world's own lookup says whose it is
	if out := run(t, w, v, "root", "ip addr"); !strings.Contains(out, "inet "+shared+"/32") {
		t.Fatalf("the customer sees the shared address on the interface:\n%s", out)
	}
	out := run(t, w, v, "root", "whois "+shared)
	for _, want := range []string{"SHARED-ADDRESS-SPACE", "100.64.0.0/10", "NovaPanel", "carrier-grade NAT"} {
		if !strings.Contains(out, want) {
			t.Errorf("whois on a shared address is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cheap") || strings.Contains(out, creds) {
		t.Errorf("whois leaked the customer's identity:\n%s", out)
	}

	// nothing can reach it, and no amount of opening the host changes that
	if msg := dialTo(net, shared, 22); !strings.Contains(msg, "carrier-grade NAT") {
		t.Fatalf("a shared address must not be routable, got %q", msg)
	}
	run(t, w, v, "root", "iptables -P INPUT ACCEPT")
	if msg := dialTo(net, shared, 22); !strings.Contains(msg, "carrier-grade NAT") {
		t.Fatalf("opening the host cannot make an unroutable address routable, got %q", msg)
	}
	_ = creds

	// outbound works, and the remote end sees the provider — not the customer
	if out := runWithStdin(t, w, v, "deploy", "sudo apt update", pw); !strings.Contains(out, "Get:1") {
		t.Fatalf("a CGNAT node must still reach the internet:\n%s", out)
	}
	syslog, _ := net.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), w.NATAddress(64520)) {
		t.Fatalf("the far end must log the provider's NAT address, not the customer's:\n%s", syslog)
	}
	if strings.Contains(string(syslog), shared) {
		t.Fatalf("the customer's own address must never appear at the far end:\n%s", syslog)
	}

	// and IPv6 is how this node is reachable at all
	if out := runWithStdin(t, w, v, "deploy", "sudo apt install nginx", pw); !strings.Contains(out, "Setting up nginx") {
		t.Fatalf("installing a service on the shared node:\n%s", out)
	}
	if msg := dialTo(net, v.FirstWANv6(), 80); msg != "connected" {
		t.Fatalf("the shared node must be reachable over v6, got %q", msg)
	}
}

// §13's "v6only" is a real plan: no IPv4 at all, so the node cannot reach an
// IPv4 destination and the world says why instead of pretending to route it.
func TestV6OnlyPlanHasNoIPv4(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	net := w.Devices["mirror"]
	v, _, err := w.ProvisionVPSWithOS("alex", "v6-sandbox", "sixonly", "alpine")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !v.IsV6Only() || core.WANIPOf(v) != "" {
		t.Fatalf("a v6-only plan must have no IPv4 address (got %q)", core.WANIPOf(v))
	}
	if out := run(t, w, v, "root", "ip addr"); strings.Contains(eth0Section(out), "\n    inet ") {
		t.Fatalf("a v6-only node must not claim an IPv4 address on its public interface:\n%s", out)
	}
	out := run(t, w, v, "root", "ping -c 1 10.0.0.1")
	if !strings.Contains(out, "no IPv4 address") {
		t.Fatalf("the diagnostic must name the missing family:\n%s", out)
	}
	// unreachable over IPv4 (there is nothing to route to) and reachable over
	// its own family
	if msg := dialTo(net, "192.0.2.9", 22); msg == "connected" {
		t.Fatalf("a v6-only node must not answer on a v4 address it does not have")
	}
	if msg := dialTo(net, v.FirstWANv6(), 22); msg != "connected" {
		t.Fatalf("the v6-only node must answer on v6, got %q", msg)
	}
	// and it is dual-stack's mirror image: a v4-only peer cannot be reached
	if out := run(t, w, v, "root", "ping -c 1 198.51.100.1"); !strings.Contains(out, "no IPv4 address") {
		t.Fatalf("v6-only hosts cannot reach v4 hosts:\n%s", out)
	}
}

// §13's "dynamic IP": the household's WAN address is a DHCP lease, the lease is
// a file, and an ISP renumbering moves the address *and* the name that points at
// it — while the v6 side stays where it was.
func TestDynamicAddressRenewsAndCanMove(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	router := w.Devices["router-alex"]
	pc := w.Devices["pc-alex"]
	old := router.FirstWANIP()

	out := run(t, w, router, "root", "ip -4 addr show eth0.2")
	if !strings.Contains(out, "dynamic valid_lft") {
		t.Fatalf("a DHCP address must be marked dynamic:\n%s", out)
	}
	lease, ok := router.FS.Read("/var/run/udhcpc.eth0.2.lease")
	if !ok || !strings.Contains(string(lease), "ip="+old) {
		t.Fatalf("the WAN lease must be a real file naming the address:\n%s", string(lease))
	}
	if out := run(t, w, router, "root", "udhcpc -i eth0.2"); !strings.Contains(out, old) {
		t.Fatalf("renewing the lease should report the address:\n%s", out)
	}
	if router.FirstWANIP() != old {
		t.Fatalf("a renewal must not move the address by itself (got %s)", router.FirstWANIP())
	}

	// the boundary: an address that belongs to somebody else cannot be taken
	if err := w.RenumberWAN(router, w.Devices["mirror"].FirstWANIP()); err == nil {
		t.Fatal("renumbering onto an address already in use must fail")
	}
	if router.FirstWANIP() != old {
		t.Fatal("a refused renumber must change nothing")
	}

	// the ISP renumbers the line
	v6before := router.FirstWANv6()
	if err := w.RenumberWAN(router, "198.51.100.77"); err != nil {
		t.Fatalf("renumber: %v", err)
	}
	if router.FirstWANIP() != "198.51.100.77" {
		t.Fatalf("the interface must carry the new address, got %s", router.FirstWANIP())
	}
	if w.IPMap[old] != "" {
		t.Fatal("the old address must leave the world's address map")
	}
	if msg := dialTo(w.Devices["mirror"], "198.51.100.77", 53); msg == "No route to host" {
		t.Fatalf("the new address must be reachable, got %q", msg)
	}
	// the name follows the address: a stale A record is the classic way a
	// household "goes offline" while its router is fine
	if got := core.DNSAnswerForTest(w, "home.alex.neohome.example"); !strings.Contains(got, "198.51.100.77") {
		t.Fatalf("the A record must follow the address, got %q", got)
	}
	if out := run(t, w, pc, "alex", "nslookup home.alex.neohome.example"); !strings.Contains(out, "198.51.100.77") {
		t.Fatalf("the household's own name must resolve to the new address:\n%s", out)
	}
	// v6 did not move: a renumbering is a v4 event on a dual-stack line
	if router.FirstWANv6() != v6before {
		t.Fatal("an IPv4 renumber must not touch the IPv6 address")
	}
	if log, _ := router.FS.Read("/var/log/syslog"); !strings.Contains(string(log), "changed the WAN address to 198.51.100.77") {
		t.Fatalf("the router must log its own renumbering:\n%s", log)
	}
	// recovery: put it back
	if err := w.RenumberWAN(router, old); err != nil {
		t.Fatalf("renumber back: %v", err)
	}
	if router.FirstWANIP() != old || core.DNSAnswerForTest(w, "home.alex.neohome.example") != old+" (A)" {
		t.Fatal("the address and the record must move back together")
	}
}

// §13's "virtual IP": a reserved address the provider lends to one node and can
// move to another. It is the operator's address, and the world treats it as
// such: reachable, listed, and not the node's own identity.
func TestVirtualAddressIsMovable(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	net := w.Devices["mirror"]
	pc := w.Devices["pc-alex"]
	a, _, err := w.ProvisionVPSWithOS("alex", "small-2", "alpha", "debian")
	if err != nil {
		t.Fatalf("provision a: %v", err)
	}
	b, _, err := w.ProvisionVPSWithOS("alex", "small-2", "beta", "debian")
	if err != nil {
		t.Fatalf("provision b: %v", err)
	}
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	out := sh("vps ip add alpha")
	if !strings.Contains(out, "reserved") {
		t.Fatalf("reserving an address should report it:\n%s", out)
	}
	vips := a.VirtualIPs()
	if len(vips) != 1 {
		t.Fatalf("the node should hold one reserved address, got %v", vips)
	}
	vip := a.Ifaces[len(a.Ifaces)-1].Extra[0]
	// it comes out of the provider's own block, so a lookup names the operator
	if !strings.HasPrefix(vip, "192.0.2.") {
		t.Fatalf("a reserved address should be the provider's to lend, got %s", vip)
	}
	if out := run(t, w, a, "root", "ip addr"); !strings.Contains(out, vip+"/32") || !strings.Contains(out, "secondary") {
		t.Fatalf("the reserved address must be a real secondary address:\n%s", out)
	}
	if msg := dialTo(net, vip, 22); msg != "connected" {
		t.Fatalf("the reserved address must be reachable at once, got %q", msg)
	}
	if out := sh("vps ip show alpha"); !strings.Contains(out, vip) {
		t.Fatalf("vps ip show must list it:\n%s", out)
	}

	// boundary: the address a node was born with is not a floating one
	if out := sh("vps ip del alpha " + a.FirstWANIP()); !strings.Contains(out, "own address") {
		t.Fatalf("detaching a node's own address must be refused:\n%s", out)
	}
	if out := sh("vps ip del beta " + vip); !strings.Contains(out, "not assigned") {
		t.Fatalf("detaching from a node that does not hold it must be refused:\n%s", out)
	}
	if out := sh("vps ip add beta 10.0.0.5"); !strings.Contains(out, "not a public") {
		t.Fatalf("a private address cannot be a virtual IP:\n%s", out)
	}
	if out := sh("vps ip add beta " + a.FirstWANIP()); !strings.Contains(out, "already assigned") {
		t.Fatalf("an address in use cannot be handed out twice:\n%s", out)
	}

	// moving it: same address, different machine
	if out := sh("vps ip del alpha " + vip); !strings.Contains(out, "released") {
		t.Fatalf("releasing the address:\n%s", out)
	}
	if msg := dialTo(net, vip, 22); !strings.Contains(msg, "No route to host") {
		t.Fatalf("a released address must not keep answering, got %q", msg)
	}
	if out := sh("vps ip add beta " + vip); !strings.Contains(out, "reserved") {
		t.Fatalf("attaching it to the second node:\n%s", out)
	}
	if w.IPMap[vip] != b.ID {
		t.Fatal("the address must now belong to the other node")
	}
	if _, dst, msg := core.Dial(net, vip, 22); msg != "connected" || dst != b {
		t.Fatalf("the moved address must land on beta, got %v/%q", dst, msg)
	}
	// and it shows on beta, not on alpha
	if out := run(t, w, b, "root", "ip addr"); !strings.Contains(out, vip) {
		t.Fatalf("beta must show the reserved address:\n%s", out)
	}
	if out := run(t, w, a, "root", "ip addr"); strings.Contains(out, vip) {
		t.Fatalf("alpha must no longer show it:\n%s", out)
	}
	if out := sh("vps list-mine"); !strings.Contains(out, "virtual:") {
		t.Fatalf("list-mine must report reserved addresses:\n%s", out)
	}
}

// eth0Section is the part of `ip addr` output that describes the public
// interface: loopback always has 127.0.0.1, and a node with no IPv4 is a
// statement about its public side.
func eth0Section(out string) string {
	i := strings.Index(out, ": eth0:")
	if i < 0 {
		return out
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}
