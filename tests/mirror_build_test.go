package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Mirror builder (Phase 3) — adopting a tree re-points an existing
// repository at this machine and lets the real sync fill it. Nothing is
// invented: the serving model is one tree per host, so adoption is the
// honest verb.

func TestMirrorBuildAdoptsTree(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	// give the household server a web server, then adopt a tree onto it
	srv := w.Devices["srv-alex"]
	srv.FS.Write("/etc/resolv.conf", "nameserver "+core.LANGateway+"\n", 0644, "root", "root")
	if len(srv.Ifaces) > 0 {
		srv.Ifaces[0].GW = core.LANGateway
		srv.Ifaces[0].Mode = "dhcp"
	}
	if out := run(t, w, srv, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("setup: apt update failed:\n%s", out)
	}
	run(t, w, srv, "root", "apt install nginx")
	if svc := srv.Svc("nginx"); svc == nil || svc.State != "running" {
		t.Fatal("setup: server must run nginx")
	}

	// a machine with no web server refuses with the reason
	if out := run(t, w, w.Devices["pc-alex"], "root", "mirror-sync build ubuntu"); !strings.Contains(out, "no web server") {
		t.Fatalf("webless adoption must be refused, got:\n%s", out)
	}
	// a tree another machine already serves is refused by name
	if out := run(t, w, srv, "root", "mirror-sync build ubuntu"); !strings.Contains(out, "served by another machine") {
		t.Fatalf("served trees must be refused, got:\n%s", out)
	}
	// arch is orphaned in this variant: adopt it onto this server
	arch := w.Repos["arch"]
	if arch == nil {
		t.Fatal("no arch tree")
	}
	arch.DeviceID = ""
	sashimi := arch
	out := run(t, w, srv, "root", "mirror-sync build arch")
	if !strings.Contains(out, "adopted arch") {
		t.Fatalf("adoption failed:\n%s", out)
	}
	if sashimi.DeviceID != srv.ID || sashimi.UpstreamID != "archive" {
		t.Fatalf("adoption must re-point the tree: %+v", sashimi)
	}
	// then a real sync fills it, and the tree answers from the new host
	if err := w.StartMirrorSync(sashimi); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for i := 0; i < 8 && sashimi.SyncPhase > 0; i++ {
		w.Tick()
	}
	if sashimi.Status != "SYNCED" {
		t.Fatalf("the adopted tree must sync: %s (%s)", sashimi.Status, sashimi.StatusWhy)
	}
	body, served := core.ServeFile(srv, "/"+sashimi.Path+"/"+core.ReleaseRel(sashimi))
	if !served || len(body) == 0 {
		t.Fatal("the adopted tree must be served from its new host")
	}
}
