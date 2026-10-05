package core

// IoT (WS-1.0): the physical layer's first citizens — the front-door
// camera and the smart lock. Spec §15 lists both as household devices,
// §41 puts them in the home topology, and §42 binds them to the digital
// layer: "摄像头 → 产生视频 / 证据状态". So the camera's recordings are
// real files on the NAS (§15's Camera → NAS dependency), and every clip is
// made of events that really happened in the world — scans, denied reads,
// failed logins, lock actuations. Nothing is invented for the tape.
//
// The shape of IoT is owned by this file; World only carries the pointer.

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// IoT is the subsystem state. Everything is gob-encodable; recordings
// themselves live in the NAS filesystem.
type IoT struct {
	CameraID string
	LockID   string
	NASID    string
	PIN      string

	Locked       bool
	Battery      int // percent; the lock stops actuating at zero
	WrongPIN     int
	LockoutUntil int // tick; three wrong codes buy a lockout

	SeenEvents int // recorder cursor into World.Events
	ClipSeq    int
	lastDropLog int // tick of the last "recordings dropped" notice
}

const iotRecordingsDir = "/srv/recordings"

// iotHousehold are the devices the front-door camera watches. They are the
// household's own machines — the camera is not a world-wide wiretap.
var iotHousehold = map[string]bool{
	"pc-alex": true, "nas-alex": true, "asst-alex": true,
	"router-alex": true, "cam-alex": true, "lock-alex": true, "bmc-alex": true,
}

// iotClipLevels are the event severities the camera records: alerts and
// denials, not every informational heartbeat.
var iotClipLevels = map[string]bool{
	"notice": true, "warn": true, "warning": true, "err": true, "error": true,
}

func (w *World) CameraDev() *Device {
	if w.IoT == nil {
		return nil
	}
	return w.Devices[w.IoT.CameraID]
}

func (w *World) LockDev() *Device {
	if w.IoT == nil {
		return nil
	}
	return w.Devices[w.IoT.LockID]
}

// seedIoT wires the subsystem into a fresh world: storage on the NAS, the
// lock's real configuration file (root-only — the PIN is stealable, as it
// is on every real device), and a boot clip so the recorder is visible.
func seedIoT(w *World) {
	nas := w.Devices["nas-alex"]
	if nas == nil {
		return
	}
	w.IoT = &IoT{
		CameraID: "cam-alex", LockID: "lock-alex", NASID: "nas-alex",
		PIN: "2846", Locked: true, Battery: 87,
	}
	nas.FS.MkdirAll(iotRecordingsDir, 0750, "camera", "camera")
	if lock := w.LockDev(); lock != nil {
		lock.FS.Write("/etc/lockd.conf",
			fmt.Sprintf("# NeoLock front door\npin = %s\nactuation = local\nbattery = %d\n",
				w.IoT.PIN, w.IoT.Battery),
			0600, "root", "root")
	}
	if cam := w.CameraDev(); cam != nil {
		cam.FS.Write("/etc/rtsp.conf",
			"listen on eth0 port 554\nstorage = nas-alex:/srv/recordings\nmotion = alert\n",
			0644, "root", "root")
	}
}

// IoTTick runs once per World.Tick: the camera turns new alert-level events
// on household devices into clips on the NAS, and the lock drains its
// battery. Both halves fail honestly when their dependencies are down.
func (w *World) IoTTick() {
	io := w.IoT
	if io == nil {
		return
	}
	w.iotRecord()
	// the lock's battery: one percent every 240 ticks (two sim hours)
	if w.TickCount%240 == 0 && io.Battery > 0 {
		io.Battery--
		if io.Battery == 15 {
			if d := w.LockDev(); d != nil {
				d.Logf("warning", "lockd", "battery low: %d%%", io.Battery)
			}
		}
		if io.Battery == 0 {
			if d := w.LockDev(); d != nil {
				d.Logf("err", "lockd", "battery dead: actuation disabled")
			}
		}
	}
}

func (w *World) iotRecord() {
	io := w.IoT
	cam := w.CameraDev()
	nas := w.Devices[io.NASID]
	if io.SeenEvents > len(w.Events) {
		io.SeenEvents = len(w.Events) // the event ring was trimmed under us
	}
	newEvents := w.Events[io.SeenEvents:]
	io.SeenEvents = len(w.Events)
	if cam == nil || !cam.Powered() {
		return
	}
	camUp := false
	if svc := cam.Svc("rtsp"); svc != nil && svc.State == "running" {
		camUp = true
	}
	if !camUp {
		return
	}
	if nas == nil || !nas.Powered() {
		// the dependency chain (§15: Camera → NAS) is real: no NAS, no tape
		// — and a camera whose storage is gone says so, event or not
		if io.lastDropLog == 0 || w.TickCount-io.lastDropLog > 20 {
			io.lastDropLog = w.TickCount
			cam.Logf("warning", "rtsp", "recordings dropped: storage target unreachable")
		}
		return
	}
	var lines []string
	for _, ev := range newEvents {
		if !iotHousehold[ev.Dev] || !iotClipLevels[ev.Level] {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] [%s] %s: %s",
			ev.At.Format("15:04:05"), ev.Dev, ev.Source, ev.Message))
	}
	if len(lines) == 0 {
		return
	}
	io.ClipSeq++
	name := fmt.Sprintf("clip-%04d.txt", io.ClipSeq)
	body := fmt.Sprintf("camera: %s\ntick: %d\nevents: %d\n\n%s\n",
		cam.Hostname, w.TickCount, len(lines), strings.Join(lines, "\n"))
	nas.FS.Write(path.Join(iotRecordingsDir, name), body, 0644, "camera", "camera")
	// keep the last 100 clips: a camera with an infinite disk is a lie
	files := nas.FS.List(iotRecordingsDir)
	if len(files) > 100 {
		sort.Strings(files)
		for _, f := range files[:len(files)-100] {
			nas.FS.Remove(f)
		}
	}
}

// Recordings lists the NAS's clips, oldest first.
func (w *World) Recordings() ([]string, error) {
	if w.IoT == nil {
		return nil, fmt.Errorf("no camera in this world")
	}
	nas := w.Devices[w.IoT.NASID]
	if nas == nil {
		return nil, fmt.Errorf("no storage target")
	}
	files := nas.FS.List(iotRecordingsDir)
	sort.Strings(files)
	return files, nil
}

// Recording reads one clip by sequence number.
func (w *World) Recording(seq int) (string, error) {
	if w.IoT == nil {
		return "", fmt.Errorf("no camera in this world")
	}
	nas := w.Devices[w.IoT.NASID]
	if nas == nil {
		return "", fmt.Errorf("no storage target")
	}
	data, ok := nas.FS.Read(path.Join(iotRecordingsDir, fmt.Sprintf("clip-%04d.txt", seq)))
	if !ok {
		return "", fmt.Errorf("no such recording: %d", seq)
	}
	return string(data), nil
}

// LockCommand runs the lockd's semantics for a session that already dialed
// in. Owner sessions actuate without a code (the phone-app model); everyone
// else needs the PIN, and wrong codes leave evidence — and a lockout.
func (w *World) LockCommand(src *Device, u *User, action, code string) (string, error) {
	io := w.IoT
	d := w.LockDev()
	if d == nil {
		return "", fmt.Errorf("no lock in this world")
	}
	// a dead battery refuses actuation; reading status and physically
	// replacing the batteries still work
	if io.Battery <= 0 && action != "batteries" && action != "status" {
		return "", fmt.Errorf("actuation failed: battery dead (replace the batteries)")
	}
	if io.WrongPIN >= 3 && w.TickCount < io.LockoutUntil {
		return "", fmt.Errorf("lock is in lockout for another %d ticks", io.LockoutUntil-w.TickCount)
	}
	switch action {
	case "status":
		state := "locked"
		if !io.Locked {
			state = "unlocked"
		}
		batt := fmt.Sprintf("%d%%", io.Battery)
		if io.Battery <= 15 {
			batt += " (low)"
		}
		return fmt.Sprintf("door: %s\nbattery: %s\nwrong PIN attempts: %d", state, batt, io.WrongPIN), nil
	case "lock", "unlock":
		if devOwner := d.Owner; devOwner != u.Name {
			// not the owner: the PIN is required, and it is the real PIN
			if code != io.PIN {
				io.WrongPIN++
				if io.WrongPIN >= 3 && w.TickCount >= io.LockoutUntil {
					io.LockoutUntil = w.TickCount + 40
				}
				d.Logf("notice", "lockd", "wrong PIN from %s (%d consecutive)", src.Hostname, io.WrongPIN)
				w.AddEvent(d.ID, "notice", "lockd", "wrong PIN from %s", src.Hostname)
				w.Record("denied", u.Name, src.SourceIPFor(d), d.ID, "wrong lock PIN", 2)
				return "", fmt.Errorf("permission denied")
			}
			io.WrongPIN = 0
		}
		io.Locked = action == "lock"
		verb := "unlocked"
		if io.Locked {
			verb = "locked"
		}
		d.Logf("notice", "lockd", "door %s by %s from %s", verb, u.Name, src.Hostname)
		w.AddEvent(d.ID, "notice", "lockd", "door %s by %s", verb, u.Name)
		return fmt.Sprintf("door %s", verb), nil
	case "batteries":
		io.Battery = 100
		d.Logf("notice", "lockd", "batteries replaced (physical access)")
		return "fresh batteries: 100%", nil
	}
	return "", fmt.Errorf("unknown action %q", action)
}
