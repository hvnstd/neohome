package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// VLAN segmentation (§31) — servers on 10, workstations on 20, routed only
// where the gateway's /etc/config/network allows. Same-VLAN traffic never
// needs the router; cross-VLAN without a rule is filtered at the gateway
// with both VLANs named; the config is re-read per packet, never cached.

func vlanWorld(t *testing.T) (*core.World, *core.Device, *core.Device, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	ws, dc, fs := w.Devices["meridian-ws"], w.Devices["meridian-dc"], w.Devices["meridian-fs"]
	if ws == nil || dc == nil || fs == nil {
		t.Fatal("no meridian office in the world")
	}
	return w, ws, dc, fs
}

func TestVLANAllowsAndDeniesByRule(t *testing.T) {
	_, ws, dc, fs := vlanWorld(t)

	// seeded allows: workstations reach the servers' smb and ssh
	if _, _, msg := core.Dial(ws, "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("ws -> fs smb should be allowed, got %s", msg)
	}
	if _, _, msg := core.Dial(ws, "10.90.10.20", 22); msg != "connected" {
		t.Fatalf("ws -> fs ssh should be allowed, got %s", msg)
	}
	// default deny: no rule names ldap, and nothing flows back
	if _, _, msg := core.Dial(ws, "10.90.10.10", 389); !strings.Contains(msg, "VLAN 20 to 10 denied") {
		t.Fatalf("ws -> dc ldap must name the denial, got %s", msg)
	}
	if _, _, msg := core.Dial(fs, "10.90.20.31", 445); !strings.Contains(msg, "VLAN 10 to 20 denied") {
		t.Fatalf("servers must not initiate to workstations, got %s", msg)
	}
	// same VLAN needs no router and no rule
	if _, _, msg := core.Dial(dc, "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("same-VLAN traffic must pass, got %s", msg)
	}
}

func TestVLANConfigIsLive(t *testing.T) {
	w, ws, _, _ := vlanWorld(t)
	mh := w.Devices["meridian-hq"]

	data, ok := mh.FS.Read("/etc/config/network")
	if !ok || !strings.Contains(string(data), "ws-to-srv-smb") {
		t.Fatalf("gateway must carry the allow rules:\n%s", string(data))
	}
	// pull the smb rule out of the live file: no restart, no daemon, the
	// next packet already answers differently
	stripped := ""
	for _, line := range strings.Split(string(data), "\n") {
		stripped += line + "\n"
	}
	start := strings.Index(stripped, "config allow 'ws-to-srv-smb'")
	end := strings.Index(stripped, "config allow 'ws-to-srv-ssh'")
	stripped = stripped[:start] + stripped[end:]
	mh.FS.Write("/etc/config/network", stripped, 0644, "root", "root")

	if _, _, msg := core.Dial(ws, "10.90.10.20", 445); !strings.Contains(msg, "denied") {
		t.Fatalf("removing the rule must close the door, got %s", msg)
	}
	// ssh rule untouched: still open (rules are per-port, not per-pair)
	if _, _, msg := core.Dial(ws, "10.90.10.20", 22); msg != "connected" {
		t.Fatalf("other rules must survive the edit, got %s", msg)
	}
	// restore and the door reopens
	mh.FS.Write("/etc/config/network", string(data), 0644, "root", "root")
	if _, _, msg := core.Dial(ws, "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("restoring the rule must reopen, got %s", msg)
	}
}

func TestVLANNeedsItsGateway(t *testing.T) {
	w, ws, dc, _ := vlanWorld(t)
	mh := w.Devices["meridian-hq"]

	mh.NetUp = false
	if _, _, msg := core.Dial(ws, "10.90.10.20", 445); !strings.Contains(msg, "is down") {
		t.Fatalf("a dark gateway must be diagnosed, got %s", msg)
	}
	// ...while switched traffic on one VLAN never noticed
	if _, _, msg := core.Dial(dc, "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("same-VLAN traffic needs no router, got %s", msg)
	}
	mh.NetUp = true
	if _, _, msg := core.Dial(ws, "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("recovery must restore routing, got %s", msg)
	}
}

func TestVLANPingParity(t *testing.T) {
	w, ws, _, _ := vlanWorld(t)

	if out := run(t, w, w.Devices["meridian-dc"], "admin", "ping 10.90.10.20"); !strings.Contains(out, "bytes from") && !strings.Contains(out, "reply") {
		t.Fatalf("same-VLAN ping must work, got:\n%s", out)
	}
	if out := run(t, w, ws, "admin", "ping 10.90.10.20"); !strings.Contains(out, "VLAN 20 to 10 denied") {
		t.Fatalf("cross-VLAN ping must name the denial, got:\n%s", out)
	}
}

func TestVLANSurvivesSave(t *testing.T) {
	w, _, _, _ := vlanWorld(t)
	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mh := back.Devices["meridian-hq"]
	tags := map[string]int{}
	for _, i := range mh.Ifaces {
		if i.VLAN != 0 {
			tags[i.Name] = i.VLAN
		}
	}
	if tags["eth1.10"] != 10 || tags["eth1.20"] != 20 {
		t.Fatalf("gateway tags must survive: %v", tags)
	}
	if back.Devices["meridian-ws"].Ifaces[0].VLAN != 20 {
		t.Fatal("host tags must survive")
	}
	if _, _, msg := core.Dial(back.Devices["meridian-ws"], "10.90.10.20", 445); msg != "connected" {
		t.Fatalf("segmentation must work after load, got %s", msg)
	}
}
