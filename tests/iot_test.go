package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The IoT pair is real hardware on the LAN: services, weak default
// passwords, and a lock whose PIN lives in a root-only config file on the
// device itself.
func TestIoTDevicesAreReal(t *testing.T) {
	w := core.NewWorld()
	cam := w.Devices["cam-alex"]
	lokd := w.Devices["lock-alex"]
	if cam == nil || lokd == nil {
		t.Fatal("the camera or the lock is missing from the household")
	}
	rtsp := cam.Svc("rtsp")
	if rtsp == nil || rtsp.Port != 554 || rtsp.State != "running" || rtsp.Scope != "lan" {
		t.Fatalf("the camera's rtsp service is not real: %+v", rtsp)
	}
	lockd := lokd.Svc("lockd")
	if lockd == nil || lockd.Port != 8899 || lockd.State != "running" {
		t.Fatalf("the lock's lockd service is not real: %+v", lockd)
	}
	// the classic weak default, because IoT
	if cam.FindUser("root").Pass != "admin" || lokd.FindUser("root").Pass != "admin" {
		t.Fatal("the IoT boxes do not ship their real (weak) default credentials")
	}
	// the PIN is a real file on the lock, root-only — stealing it is a goal
	conf, ok := lokd.FS.Read("/etc/lockd.conf")
	if !ok || !strings.Contains(string(conf), "pin = "+w.IoT.PIN) {
		t.Fatalf("the lock's PIN is not in its real config file:\n%s", conf)
	}
	if lokd.FS.CanRead("/etc/lockd.conf", &core.User{Name: "assistant", UID: 1001}) {
		t.Fatal("the lockd.conf is readable by non-root")
	}
	// recordings land on the NAS (§15: Camera → NAS)
	if !w.Devices["nas-alex"].FS.IsDir("/srv/recordings") {
		t.Fatal("the NAS has no recordings directory")
	}
}

// The camera records what really happens: an alert-level event on a
// household device becomes a clip on the NAS; info traffic does not.
func TestCameraRecordsRealEvents(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]

	clips := func() []string {
		files, err := w.Recordings()
		if err != nil {
			t.Fatalf("recordings listing failed: %v", err)
		}
		return files
	}
	before := len(clips())

	// a scan is an observable act on the target, and the camera sees it
	run(t, w, asst, "assistant", "scan 10.77.1.30")
	w.Tick()
	after := clips()
	if len(after) != before+1 {
		t.Fatalf("a scan did not produce a clip: %d -> %d", before, len(after))
	}
	out := run(t, w, pc, "alex", "camera view 1")
	if !strings.Contains(out, "port scan from") {
		t.Fatalf("the clip does not show the real event:\n%s", out)
	}
	out = run(t, w, pc, "alex", "camera list")
	if !strings.Contains(out, "clip-0001.txt") {
		t.Fatalf("camera list does not show the clip:\n%s", out)
	}

	// info-level events are not motion: mail delivery leaves no tape
	if err := w.DeliverLocal(pc, "world@neohome", "alex", "no motion here", "info only"); err != nil {
		t.Fatalf("delivery failed: %v", err)
	}
	w.Tick()
	if got := len(clips()); got != len(after) {
		t.Fatalf("an info event produced a clip: %d -> %d", len(after), got)
	}
}

// §15's dependency chain is real: no camera service, no tape; no NAS, no
// tape — and the drop is logged on the camera, not swallowed.
func TestCameraDependencyChain(t *testing.T) {
	w := core.NewWorld()
	asst := w.Devices["asst-alex"]
	cam := w.Devices["cam-alex"]

	if _, err := cam.StopService("rtsp"); err != nil {
		t.Fatalf("stopping rtsp failed: %v", err)
	}
	run(t, w, asst, "assistant", "scan 10.77.1.30")
	w.Tick()
	if files, _ := w.Recordings(); len(files) != 0 {
		t.Fatalf("a stopped camera still recorded: %v", files)
	}
	if _, err := cam.StartService("rtsp"); err != nil {
		t.Fatalf("starting rtsp failed: %v", err)
	}
	run(t, w, asst, "assistant", "scan 10.77.1.30")
	w.Tick()
	if files, _ := w.Recordings(); len(files) != 1 {
		t.Fatalf("the camera did not come back: %d clips", len(files))
	}

	// storage target down: the clip is dropped and the drop is logged
	nas := w.Devices["nas-alex"]
	nas.MainsDropped = true
	run(t, w, asst, "assistant", "scan 10.77.1.30")
	w.Tick()
	w.Tick()
	syslog, _ := cam.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "recordings dropped") {
		t.Fatal("the camera stayed silent about its dead storage target")
	}
}

// The lock is owner-friendly, evidence-producing, and honest about wrong
// codes: three of them buy a lockout, and every attempt is recorded.
func TestLockSemantics(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	asst := w.Devices["asst-alex"]

	out := run(t, w, pc, "alex", "lock status")
	if !strings.Contains(out, "door: locked") {
		t.Fatalf("initial status wrong:\n%s", out)
	}
	// the owner's own session unlocks without a code (the phone-app model)
	out = run(t, w, pc, "alex", "lock unlock")
	if !strings.Contains(out, "door unlocked") {
		t.Fatalf("owner unlock failed:\n%s", out)
	}
	out = run(t, w, pc, "alex", "lock lock")
	if !strings.Contains(out, "door locked") {
		t.Fatalf("re-locking failed:\n%s", out)
	}

	// a non-owner needs the PIN; wrong codes are evidence and cost attempts
	out = run(t, w, asst, "assistant", "lock unlock 0000")
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("a wrong PIN opened the door:\n%s", out)
	}
	lokd := w.Devices["lock-alex"]
	syslog, _ := lokd.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "wrong PIN from") {
		t.Fatal("the lock did not log the wrong-PIN attempt")
	}
	if w.Case == nil || len(w.Case.Events) == 0 {
		t.Fatal("the wrong-PIN attempt left no evidence")
	}
	out = run(t, w, asst, "assistant", "lock unlock "+w.IoT.PIN)
	if !strings.Contains(out, "door unlocked") {
		t.Fatalf("the correct PIN did not unlock:\n%s", out)
	}
	// three consecutive wrong codes trip the lockout
	w.IoT.WrongPIN = 0
	for i := 0; i < 3; i++ {
		run(t, w, asst, "assistant", "lock unlock 9999")
	}
	out = run(t, w, asst, "assistant", "lock unlock 9999")
	if !strings.Contains(out, "lockout") {
		t.Fatalf("three wrong codes did not trip the lockout:\n%s", out)
	}
}

func TestLockBattery(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	w.IoT.Battery = 1
	w.TickCount = 239
	w.Tick() // crosses a 240-tick boundary: one percent gone, and that was the last
	if w.IoT.Battery != 0 {
		t.Fatalf("battery did not drain: %d", w.IoT.Battery)
	}
	out := run(t, w, pc, "alex", "lock unlock")
	if !strings.Contains(out, "battery dead") {
		t.Fatalf("a dead battery must refuse actuation:\n%s", out)
	}
	out = run(t, w, pc, "alex", "lock batteries")
	if !strings.Contains(out, "100%") {
		t.Fatalf("replacing the batteries failed:\n%s", out)
	}
	out = run(t, w, pc, "alex", "lock unlock")
	if !strings.Contains(out, "door unlocked") {
		t.Fatalf("the lock did not come back after new batteries:\n%s", out)
	}
}
