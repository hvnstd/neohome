package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Builders (Phase 3) — guided setup performing the real steps: install,
// configure, start, verify, rollback. Each test walks a happy path, a
// boundary and a recovery.

func TestServerBuildWeb(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]

	if out := run(t, w, pc, "alex", "server build web"); !strings.Contains(out, "needs root") {
		t.Fatalf("non-root builds must be refused, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "server build summon"); !strings.Contains(out, "unknown role") {
		t.Fatalf("unknown roles must be refused, got:\n%s", out)
	}
	out := run(t, w, pc, "root", "server build web")
	if !strings.Contains(out, "[4/4] verified") {
		t.Fatalf("build should verify, got:\n%s", out)
	}
	if svc := pc.Svc("nginx"); svc == nil || svc.State != "running" {
		t.Fatalf("nginx must run after build: %+v", svc)
	}
	if _, _, msg := core.Dial(pc, pc.FirstLANIP(), 80); msg != "connected" {
		t.Fatalf("the built server must answer, got %s", msg)
	}
	// idempotent: rebuilding reports existing steps, changes nothing
	before := len(pc.Installed)
	out = run(t, w, pc, "root", "server build web")
	if !strings.Contains(out, "already installed") || !strings.Contains(out, "already running") {
		t.Fatalf("rebuild must be a no-op with notes, got:\n%s", out)
	}
	if len(pc.Installed) != before {
		t.Fatal("rebuild must not duplicate anything")
	}
}

func TestServerBuildRollsBack(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]

	// no room for the database: the install fails and nothing half-lands
	pc.HW.DiskMB = pc.FS.DiskUsedMB()
	out := run(t, w, pc, "root", "server build db")
	if !strings.Contains(out, "cannot install mariadb") {
		t.Fatalf("a full disk must fail the build, got:\n%s", out)
	}
	if pc.Installed["mariadb"] != nil {
		t.Fatal("a failed build must not half-install")
	}
	// room back: the same build succeeds (recovery is space, not state)
	pc.HW.DiskMB = 65536
	out = run(t, w, pc, "root", "server build db")
	if !strings.Contains(out, "[4/4] verified") {
		t.Fatalf("build should succeed with room, got:\n%s", out)
	}
}

func TestBackupInitLocal(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]

	if out := run(t, w, pc, "alex", "backup init"); !strings.Contains(out, "needs root") {
		t.Fatalf("non-root backup wiring must be refused, got:\n%s", out)
	}
	out := run(t, w, pc, "root", "backup init --repo /srv/backup --tree /home")
	if !strings.Contains(out, "[5/5]") {
		t.Fatalf("backup init should finish all steps, got:\n%s", out)
	}
	snaps, err := core.ResticSnapshots(pc)
	if err != nil || len(snaps) == 0 {
		t.Fatalf("a snapshot must exist: %v", err)
	}
	data, ok := pc.FS.Read("/etc/cron.d/restic")
	if !ok || !strings.Contains(string(data), "restic backup") {
		t.Fatalf("the schedule must be a real cron file:\n%s", string(data))
	}
	// second run meets the existing repository instead of breaking it
	if out := run(t, w, pc, "root", "backup init --repo /srv/backup"); !strings.Contains(out, "already initialised") {
		t.Fatalf("re-init must be graceful, got:\n%s", out)
	}
	// garbage schedules are refused before anything is written
	if out := run(t, w, pc, "root", "backup init --repo /srv/backup --schedule nonsense"); !strings.Contains(out, "bad schedule") {
		t.Fatalf("bad schedules must be refused, got:\n%s", out)
	}
}
