package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Household network exposure (§14 家庭网络必须符合现实习惯, §28 攻击面).
//
// §14's rule is that attack surface comes from configuration the player made,
// never from an event the world scripted. These tests hold the code to that:
// the packet path reads the same files the player edits, nothing opens by
// itself, and every hole is traceable to a `uci set`, an `iptables -A` or a
// UPnP request — with a log line and a lease file to prove it.

// internet is a host out on the public internet doing the dialling.
func internet(w *core.World) *core.Device { return w.Devices["mirror"] }

// dialMsg is the message one device gets connecting to another's port.
func dialMsg(src *core.Device, ip string, port int) string {
	_, _, msg := core.Dial(src, ip, port)
	return msg
}

// reachFromInternet is the message a connection from the public internet gets.
func reachFromInternet(w *core.World, d *core.Device, port int) string {
	_, _, msg := core.Dial(internet(w), d.FirstWANIP(), port)
	return msg
}

// A household router ships closed. Every port a stranger can name is filtered,
// and the log says so rather than staying silent — the difference between "no
// service" and "no route in" is a thing a player has to be able to see.
func TestHouseholdRouterIsClosedUntilSomethingOpensIt(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]

	cfg, ok := rtr.FS.Read("/etc/config/firewall")
	if !ok {
		t.Fatal("the router must have a real firewall configuration")
	}
	if !strings.Contains(string(cfg), "option wan_input 'REJECT'") {
		t.Fatalf("the shipped configuration must reject WAN input:\n%s", cfg)
	}
	st := rtr.FW()
	if len(st.Redirects) != 0 {
		t.Fatalf("a household router ships with nothing forwarded, got %d redirects", len(st.Redirects))
	}
	if len(st.Errors) != 0 {
		t.Fatalf("the shipped configuration must parse cleanly, got %v", st.Errors)
	}

	// the router's own management, the LAN services behind it, and an IoT
	// device: all of them unreachable from the internet
	for _, port := range []int{22, 23, 80, 443, 554, 445, 2049} {
		if msg := reachFromInternet(w, rtr, port); !strings.Contains(msg, "filtered") {
			t.Fatalf("WAN port %d must be filtered on a default router, got %q", port, msg)
		}
	}
	// a LAN client is unaffected: the default is LAN→WAN accept
	pc := w.Devices["pc-alex"]
	if msg := dialMsg(pc, rtr.FirstLANIP(), 53); msg != "connected" {
		t.Fatalf("the LAN side must still work, got %q", msg)
	}
	// and the attempts were recorded on the router, because its config asks
	// for drop logging
	log, _ := rtr.FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "DROP wan 554/tcp") {
		t.Fatalf("filtered WAN packets must be logged when log_drops is on:\n%s", log)
	}
	if got := rtr.ExposureSummary(); len(got) != 0 {
		t.Fatalf("a default router exposes nothing, got %v", got)
	}
}

// `uci set` stages, `uci commit` applies. That is the whole reason the tool has
// two verbs, and it is also the safety property: an edit that has not been
// committed has not changed the packet path.
func TestUCISetStagesUntilCommit(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	cam := w.Devices["cam-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	sh("uci add firewall redirect cam-view")
	sh("uci set firewall.cam-view.target=DNAT")
	sh("uci set firewall.cam-view.src=wan")
	sh("uci set firewall.cam-view.src_dport=554")
	sh("uci set firewall.cam-view.dest_ip=" + cam.FirstLANIP())
	sh("uci set firewall.cam-view.dest_port=554")
	sh("uci set firewall.cam-view.enabled=1")

	// staged, not applied
	if _, ok := rtr.FS.Read("/etc/config/firewall"); !ok {
		t.Fatal("the config file must still be there")
	}
	if got := sh("uci changes"); !strings.Contains(got, "firewall.cam-view.src_dport=554") {
		t.Fatalf("pending changes must be listed:\n%s", got)
	}
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("an uncommitted change must not alter the packet path, got %q", msg)
	}

	// commit applies it, and the hole is real
	sh("uci commit firewall")
	if got := sh("uci changes"); strings.Contains(got, "cam-view") {
		t.Fatalf("after commit nothing may be pending:\n%s", got)
	}
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("after commit the forward must carry the connection, got %q", msg)
	}
	if svc, dst, _ := core.Dial(internet(w), rtr.FirstWANIP(), 554); svc == nil || dst != cam {
		t.Fatalf("the forwarded connection must land on the camera, got %v/%v", svc, dst)
	}
	// the config file now contains the section, readable with cat
	cfg, _ := rtr.FS.Read("/etc/config/firewall")
	if !strings.Contains(string(cfg), "config redirect 'cam-view'") {
		t.Fatalf("the committed section must be in the file:\n%s", cfg)
	}
	if got := sh("uci show firewall"); !strings.Contains(got, "firewall.cam-view.dest_ip='"+cam.FirstLANIP()+"'") {
		t.Fatalf("uci show must print the committed section:\n%s", got)
	}

	// revert discards a pending edit: the file is the truth
	sh("uci set firewall.cam-view.enabled=0")
	sh("uci revert firewall")
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("reverting a staged change must leave the committed state alone, got %q", msg)
	}
	// and committing the disable closes the hole
	sh("uci set firewall.cam-view.enabled=0")
	sh("uci commit firewall")
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("a disabled redirect must stop carrying packets, got %q", msg)
	}
}

// A port-forward is a hole somebody opened, and the world treats it as one:
// the connection is translated, the router logs the translation, recon follows
// it to the machine behind, and the log line is what a player finds.
func TestPortForwardIsAnOwnersHole(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	nas := w.Devices["nas-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	sh("uci set firewall.nas-web=redirect")
	sh("uci set firewall.nas-web.target=DNAT")
	sh("uci set firewall.nas-web.src=wan")
	sh("uci set firewall.nas-web.src_dport=8080")
	sh("uci set firewall.nas-web.dest_ip=" + nas.FirstLANIP())
	sh("uci set firewall.nas-web.dest_port=8080")
	sh("uci set firewall.nas-web.enabled=1")
	sh("uci commit firewall")

	// the NAS really serves 8080 on the LAN, so the forward has something to
	// land on
	if nas.Svc("nginx") == nil {
		t.Fatal("precondition: the NAS runs its web console")
	}
	svc, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 8080)
	if msg != "connected" || dst != nas {
		t.Fatalf("the forward must land the connection on the NAS, got %v %v", dst, msg)
	}
	if svc == nil || svc.Name != "nginx" {
		t.Fatalf("the forwarded port should reach the NAS's web service, got %v", svc)
	}
	log, _ := rtr.FS.Read("/var/log/syslog")
	if !strings.Contains(string(log), "DNAT gateway:8080 -> "+nas.FirstLANIP()+":8080") {
		t.Fatalf("the router must record the translation it performed:\n%s", log)
	}

	// recon, run from a public host, names the machine behind the hole
	out := run(t, w, internet(w), "root", "recon "+rtr.FirstWANIP())
	if !strings.Contains(out, "8080/tcp -> forwarded to nas") {
		t.Fatalf("recon must follow the forward to the real target:\n%s", out)
	}
	// and a port nobody forwarded is still filtered
	if msg := reachFromInternet(w, rtr, 8081); !strings.Contains(msg, "filtered") {
		t.Fatalf("only the forwarded port may be open, got %q", msg)
	}

	// closing it, the way an owner does, restores the default
	sh("uci set firewall.nas-web.enabled=0")
	sh("uci commit firewall")
	if msg := reachFromInternet(w, rtr, 8080); !strings.Contains(msg, "filtered") {
		t.Fatalf("closing the forward must close the hole, got %q", msg)
	}
}

// A DMZ is a redirect with no destination port: everything goes to one host.
// It is the biggest decision §14 lists, so it has to behave like one.
func TestDMZExposesEveryPortOfOneHost(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	nas := w.Devices["nas-alex"]
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	sh("uci add firewall redirect dmz-nas")
	sh("uci set firewall.dmz-nas.target=DNAT")
	sh("uci set firewall.dmz-nas.src=wan")
	sh("uci set firewall.dmz-nas.dest_ip=" + nas.FirstLANIP())
	sh("uci set firewall.dmz-nas.enabled=1")
	sh("uci commit firewall")

	// every port lands on the NAS: one that has a service, and one that does
	// not (which is the difference between "refused" and "filtered")
	if _, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 8080); dst != nas || msg != "connected" {
		t.Fatalf("the DMZ must carry any port to the NAS, got %v %v", dst, msg)
	}
	if _, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 9999); dst != nas || msg != "Connection refused" {
		t.Fatalf("a DMZ port with nothing listening is refused by the host, got %v %v", dst, msg)
	}
	if inner := core.ForwardTarget(internet(w), rtr.FirstWANIP(), 22); inner != nas {
		t.Fatalf("ForwardTarget must follow the DMZ, got %v", inner)
	}
	// the NAS's own rule for ssh says LAN, and a DMZ is a hole the owner dug,
	// so the connection is still translated
	if _, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 22); dst != nas || msg != "connected" {
		t.Fatalf("the DMZ bypasses the host's LAN-only scope, got %v %v", dst, msg)
	}
	// the household PC is untouched by the NAS's DMZ
	if _, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 5900); dst != nas && msg == "connected" {
		t.Fatalf("a DMZ targets one host only, got %v %v", dst, msg)
	}
	if got := rtr.ExposureSummary(); len(got) != 1 || !strings.Contains(got[0], "all ports") {
		t.Fatalf("the router must report the DMZ as everything, got %v", got)
	}

	// removing it (the redirect, not the host) restores the default
	sh("uci delete firewall.dmz-nas")
	sh("uci commit firewall")
	if msg := reachFromInternet(w, rtr, 8080); !strings.Contains(msg, "filtered") {
		t.Fatalf("without the DMZ the NAS is behind the router again, got %q", msg)
	}
	if msg := dialMsg(pc, nas.FirstLANIP(), 8080); msg != "connected" {
		t.Fatalf("the LAN must never have been affected, got %q", msg)
	}
}

// Remote management: publishing the router's own interface is a decision, and
// weakening its default policy exposes everything it runs — including the
// legacy telnetd the shipped image was careful to keep private.
func TestRemoteManagementAndPolicyChanges(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	// the router runs dropbear on 22 and a legacy telnetd on 23; both private
	for _, port := range []int{22, 23} {
		if msg := reachFromInternet(w, rtr, port); !strings.Contains(msg, "filtered") {
			t.Fatalf("port %d should be private by default, got %q", port, msg)
		}
	}

	// an explicit WAN accept for ssh: now a stranger reaches the router itself
	sh("uci add firewall rule wan-ssh")
	sh("uci set firewall.wan-ssh.src=wan")
	sh("uci set firewall.wan-ssh.proto=tcp")
	sh("uci set firewall.wan-ssh.dest_port=22")
	sh("uci set firewall.wan-ssh.target=ACCEPT")
	sh("uci commit firewall")
	svc, dst, msg := core.Dial(internet(w), rtr.FirstWANIP(), 22)
	if msg != "connected" || dst != rtr {
		t.Fatalf("the rule must expose the router's own ssh, got %v %v", dst, msg)
	}
	if svc == nil || svc.Name != "dropbear" {
		t.Fatalf("the exposed service should be the router's sshd, got %v", svc)
	}
	// one port, not the machine: telnet stays private
	if msg := reachFromInternet(w, rtr, 23); !strings.Contains(msg, "filtered") {
		t.Fatalf("exposing 22 must not expose 23, got %q", msg)
	}

	// the misconfiguration: flipping the default policy instead of adding a
	// rule. Everything the router runs becomes reachable.
	n := len(rtr.FW().Rules)
	sh("uci set firewall.@defaults[0].wan_input=ACCEPT")
	sh("uci commit firewall")
	if got := len(rtr.FW().Rules); got != n {
		t.Fatal("changing the policy must not add rules")
	}
	if msg := reachFromInternet(w, rtr, 23); msg != "connected" {
		t.Fatalf("WAN input ACCEPT exposes every listening service, got %q", msg)
	}
	sum := rtr.ExposureSummary()
	found := false
	for _, line := range sum {
		if strings.Contains(line, "WAN input policy is ACCEPT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the router must warn about its own policy: %v", sum)
	}

	// recovery: put the policy back and the telnetd is private again
	sh("uci set firewall.@defaults[0].wan_input=REJECT")
	sh("uci commit firewall")
	if msg := reachFromInternet(w, rtr, 23); !strings.Contains(msg, "filtered") {
		t.Fatalf("restoring the policy must close it, got %q", msg)
	}
	if msg := reachFromInternet(w, rtr, 22); msg != "connected" {
		t.Fatalf("the explicit rule must survive the policy change, got %q", msg)
	}
}

// A host's own ruleset is a different real language for the same question.
// A fresh cloud node is open (that is why a VPS is a public host); its owner
// can close it, and publish back the one port they meant to publish.
func TestHostFirewallOnAVPS(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	v, _, err := w.ProvisionVPSWithOS("alex", "small-2", "web1", "debian")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	pw := v.FindUser("deploy").Pass
	sh := func(line string) string { return run(t, w, v, "root", line) }

	// a fresh node runs what it runs and the internet can reach it
	sh("apt update")
	runWithStdin(t, w, v, "deploy", "sudo apt install nginx", pw)
	if msg := reachFromInternet(w, v, 80); msg != "connected" {
		t.Fatalf("a VPS publishes what it runs, got %q", msg)
	}

	// --- boundary: a non-root account cannot rewrite the ruleset
	out := run(t, w, v, "deploy", "iptables -A INPUT -p tcp --dport 1234 -j ACCEPT")
	if !strings.Contains(out, "Permission denied") {
		t.Fatalf("iptables must refuse a non-root user:\n%s", out)
	}
	if msg := reachFromInternet(w, v, 1234); !strings.Contains(msg, "filtered") && msg == "connected" {
		t.Fatalf("a refused rule must not take effect, got %q", msg)
	}

	// closing the box: policy DROP, with ssh still published so the owner is
	// not locked out
	sh("iptables -P INPUT DROP")
	if msg := reachFromInternet(w, v, 80); !strings.Contains(msg, "filtered") {
		t.Fatalf("with a DROP policy only published ports may pass, got %q", msg)
	}
	if msg := reachFromInternet(w, v, 22); msg != "connected" {
		t.Fatalf("the published ssh exception must survive, got %q", msg)
	}

	// publishing one port back
	sh("iptables -A INPUT -p tcp --dport 80 -j ACCEPT")
	if msg := reachFromInternet(w, v, 80); msg != "connected" {
		t.Fatalf("an accepted port must be reachable again, got %q", msg)
	}
	if msg := reachFromInternet(w, v, 443); !strings.Contains(msg, "filtered") {
		t.Fatalf("ports nobody accepted stay closed, got %q", msg)
	}

	// the file, the command and the packet path agree
	data, ok := v.FS.Read("/etc/iptables/rules.v4")
	if !ok {
		t.Fatal("the ruleset must be a file on the node")
	}
	if !strings.Contains(string(data), ":INPUT DROP") || !strings.Contains(string(data), "--dport 80 -j ACCEPT") {
		t.Fatalf("the file must hold what was applied:\n%s", data)
	}
	listing := sh("iptables -L")
	if !strings.Contains(listing, "policy DROP") || !strings.Contains(listing, "dport 80") {
		t.Fatalf("iptables -L must read the file:\n%s", listing)
	}
	// removing the rule closes it again
	sh("iptables -D INPUT -p tcp --dport 80 -j ACCEPT")
	if msg := reachFromInternet(w, v, 80); !strings.Contains(msg, "filtered") {
		t.Fatalf("a deleted rule must stop passing traffic, got %q", msg)
	}
}

// UPnP is the one hole nobody typed: a LAN program asks the router for a
// mapping, and the world keeps the receipts (lease file, router log, event
// log) so the owner can find it later. Without a daemon it fails closed.
func TestUPnPMappingLeavesEvidenceAndCloses(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	cam := w.Devices["cam-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	// the camera is reachable on the LAN and nowhere else
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("the camera must start private, got %q", msg)
	}
	if got := run(t, w, cam, "root", "camctl status"); !strings.Contains(got, "cloud viewing: off") {
		t.Fatalf("cloud viewing starts off:\n%s", got)
	}

	// the owner (or the vendor app) turns it on: a UPnP request, a lease, a
	// log line on both ends, and a real hole
	out := run(t, w, cam, "root", "camctl cloud on")
	if !strings.Contains(out, "cloud viewing on") {
		t.Fatalf("enabling cloud viewing should succeed with UPnP on:\n%s", out)
	}
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("the mapped port must carry the stream, got %q", msg)
	}
	leases, _ := rtr.FS.Read(core.UPnPLeasePath)
	if !strings.Contains(string(leases), "554 "+cam.FirstLANIP()+" 554") {
		t.Fatalf("the mapping must be in the daemon's lease file:\n%s", leases)
	}
	syslog, _ := rtr.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "mapping added: TCP 554") {
		t.Fatalf("the router must log what it opened:\n%s", syslog)
	}
	events := 0
	for _, e := range w.Events {
		if e.Dev == rtr.ID && strings.Contains(e.Message, "opened TCP/554 through UPnP") {
			events++
		}
	}
	if events == 0 {
		t.Fatal("opening a port through UPnP must be an event, like any other exposure")
	}
	// the camera's own config says it is on: the state survives, it is not a
	// flag in the world's memory
	cfg, _ := cam.FS.Read(core.CameraCloudConfig)
	if !strings.Contains(string(cfg), "cloud = on") {
		t.Fatalf("the camera's config must record the decision:\n%s", cfg)
	}
	// the forward list recon reads includes the UPnP hole
	if !strings.Contains(run(t, w, internet(w), "root", "recon "+rtr.FirstWANIP()), "opened by UPnP") {
		t.Fatal("recon must see a UPnP mapping as an open hole")
	}

	// --- boundary: the owner disables the daemon, and the lease stops being a
	// hole without being deleted (a real router does exactly this)
	sh("uci set upnpd.@upnpd[0].enabled=0")
	sh("uci commit upnpd")
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("a disabled UPnP daemon must stop carrying its mappings, got %q", msg)
	}
	if leases, _ := rtr.FS.Read(core.UPnPLeasePath); !strings.Contains(string(leases), "554") {
		t.Fatal("the lease remains on disk until something removes it")
	}
	// and with the daemon off, asking for a new hole fails closed: no config
	// change, no exposure
	run(t, w, cam, "root", "camctl cloud off")
	out = run(t, w, cam, "root", "camctl cloud on")
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("with UPnP disabled the request must fail:\n%s", out)
	}
	if cfg, _ := cam.FS.Read(core.CameraCloudConfig); strings.Contains(string(cfg), "cloud = on") {
		t.Fatalf("a failed request must not leave the camera claiming it is on:\n%s", cfg)
	}
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("a failed request must not expose anything, got %q", msg)
	}

	// --- recovery: the owner re-enables it and maps the port on purpose
	sh("uci set upnpd.@upnpd[0].enabled=1")
	sh("uci commit upnpd")
	if out = run(t, w, cam, "root", "camctl cloud on"); !strings.Contains(out, "cloud viewing on") {
		t.Fatalf("re-enabling should work again:\n%s", out)
	}
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("the mapping must work again, got %q", msg)
	}
	// the owner removes the mapping by hand from a machine inside the house.
	// From the router itself there is no gateway to discover: IGD lives
	// upstream of a client, never upstream of the gateway.
	if out = run(t, w, rtr, "root", "upnpc -d 554 tcp"); !strings.Contains(out, "no UPnP-enabled gateway") {
		t.Fatalf("upnpc on the router itself has no gateway to talk to:\n%s", out)
	}
	if out = run(t, w, w.Devices["pc-alex"], "root", "upnpc -l"); !strings.Contains(out, "554") {
		t.Fatalf("a LAN host must be able to list the mapping:\n%s", out)
	}
	if out = run(t, w, w.Devices["pc-alex"], "root", "upnpc -d 554 tcp"); !strings.Contains(out, "removed") {
		t.Fatalf("the mapping must be removable from a LAN host:\n%s", out)
	}
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("removing the mapping must close the hole, got %q", msg)
	}
	// the removal is evidence too: the lease file and the router log both
	// show when and by whom
	if leases, _ := rtr.FS.Read(core.UPnPLeasePath); strings.Contains(string(leases), "554") {
		t.Fatalf("the lease must be gone from the daemon's file:\n%s", leases)
	}
	if syslog, _ := rtr.FS.Read("/var/log/syslog"); !strings.Contains(string(syslog), "mapping removed by home-pc") {
		t.Fatalf("the router must log who closed it:\n%s", syslog)
	}
	// the camera still *thinks* it is on — a stale config is how an "off"
	// camera stays exposed — so status has to report the disagreement
	if out = run(t, w, cam, "root", "camctl status"); !strings.Contains(out, "cloud viewing: on") ||
		!strings.Contains(out, "no mapping") {
		t.Fatalf("status must separate configuration from reachability:\n%s", out)
	}
	// and the owner finishes the job: turn it off, which now also clears the
	// camera's own config
	if out = run(t, w, cam, "root", "camctl cloud off"); !strings.Contains(out, "off") {
		t.Fatalf("turning cloud viewing off:\n%s", out)
	}
	if out = run(t, w, cam, "root", "camctl status"); !strings.Contains(out, "cloud viewing: off") {
		t.Fatalf("the camera must record that it is off:\n%s", out)
	}
}

// The camera's console is not a magic button: it asks the gateway properly, so
// a world where the daemon was never enabled (or the camera is not behind this
// gateway) fails closed.
func TestCameraCloudRequiresARealGateway(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	cam := w.Devices["cam-alex"]

	// cloud viewing is off by default even though the daemon is enabled: it
	// takes a request, not a default
	if w.CameraCloudOn() {
		t.Fatal("cloud viewing must not be on before anybody asks")
	}
	if _, err := w.CameraCloud(cam, true); err != nil {
		t.Fatalf("with the daemon enabled the request should succeed: %v", err)
	}
	if !w.CameraCloudOn() {
		t.Fatal("the camera's config must record that it is on")
	}
	// the mapping belongs to the router the camera is actually behind
	st := rtr.FW()
	if len(st.UPnPLeases) != 1 || st.UPnPLeases[0].IP != cam.FirstLANIP() {
		t.Fatalf("the mapping must be on the camera's own gateway, got %v", st.UPnPLeases)
	}
	// turning it off removes both halves
	if _, err := w.CameraCloud(cam, false); err != nil {
		t.Fatalf("turning cloud viewing off: %v", err)
	}
	if w.CameraCloudOn() || len(rtr.FW().UPnPLeases) != 0 {
		t.Fatal("turning it off must clear the config and the mapping")
	}
}

// Nothing in this world opens a port by itself. §14 forbids the scripted
// exposure, so this is the test that would catch one being added.
func TestNoPortEverOpensByItself(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]

	if got := rtr.ExposureSummary(); len(got) != 0 {
		t.Fatalf("a fresh world exposes nothing on the household router, got %v", got)
	}
	// let the world run: mirror syncs, NPC routines, cron, power, IoT
	for i := 0; i < 200; i++ {
		w.Tick()
	}
	if got := rtr.ExposureSummary(); len(got) != 0 {
		t.Fatalf("no simulation step may open a port, got %v", got)
	}
	if n := len(rtr.FW().UPnPLeases); n != 0 {
		t.Fatalf("no simulation step may ask for a UPnP mapping, got %d leases", n)
	}
	for _, port := range []int{22, 23, 80, 443, 554, 445} {
		if msg := reachFromInternet(w, rtr, port); !strings.Contains(msg, "filtered") {
			t.Fatalf("port %d must stay filtered while the world runs, got %q", port, msg)
		}
	}
}

// A firewall that cannot be parsed must fail closed, not fall back to
// permissive defaults — and the reload verb has to say so out loud.
func TestMalformedFirewallConfigFailsClosed(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	rtr := w.Devices["router-alex"]
	cam := w.Devices["cam-alex"]
	sh := func(line string) string { return run(t, w, rtr, "root", line) }

	// open a hole first, so "fails closed" means something visible
	sh("uci add firewall redirect cam-view")
	sh("uci set firewall.cam-view.target=DNAT")
	sh("uci set firewall.cam-view.src=wan")
	sh("uci set firewall.cam-view.src_dport=554")
	sh("uci set firewall.cam-view.dest_ip=" + cam.FirstLANIP())
	sh("uci set firewall.cam-view.dest_port=554")
	sh("uci set firewall.cam-view.enabled=1")
	sh("uci commit firewall")
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("precondition: the forward should work, got %q", msg)
	}

	// somebody breaks the config (a bad paste, a half-written section)
	rtr.FS.Write("/etc/config/firewall", "config redirect 'cam-view'\n  forward_ip 10.77.1.40\n", 0644, "root", "root")
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("an unparseable config must not keep old holes open, got %q", msg)
	}
	out := sh("/etc/init.d/firewall reload")
	if !strings.Contains(out, "unrecognised keyword") {
		t.Fatalf("the reload must report the parse failure:\n%s", out)
	}
	if !strings.Contains(out, "no redirects or rules are in force") {
		t.Fatalf("the reload must say what failing closed means:\n%s", out)
	}

	// uci refuses to touch a file it cannot parse, so the repair is the one a
	// real owner performs: open the file and fix the line
	if out := sh("uci set firewall.cam-view.enabled=1"); !strings.Contains(out, "Parse error") {
		t.Fatalf("uci must refuse to edit a config it cannot parse:\n%s", out)
	}
	if msg := reachFromInternet(w, rtr, 554); !strings.Contains(msg, "filtered") {
		t.Fatalf("a refused edit must change nothing, got %q", msg)
	}

	// recovery: write the section back correctly, by hand, and reload
	rtr.FS.Write("/etc/config/firewall",
		"config redirect 'cam-view'\n"+
			"\toption target 'DNAT'\n"+
			"\toption src 'wan'\n"+
			"\toption src_dport '554'\n"+
			"\toption dest_ip '"+cam.FirstLANIP()+"'\n"+
			"\toption dest_port '554'\n"+
			"\toption enabled '1'\n", 0644, "root", "root")
	if msg := reachFromInternet(w, rtr, 554); msg != "connected" {
		t.Fatalf("a repaired config must take effect, got %q", msg)
	}
	if out := sh("/etc/init.d/firewall reload"); !strings.Contains(out, "1 active redirect(s)") {
		t.Fatalf("the reload should report the rule it now honours:\n%s", out)
	}
}
