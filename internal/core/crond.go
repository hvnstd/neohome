package core

import "fmt"

// The crond side of the scheduler: the daemon is a REAL service unit on a REAL
// device, so its lifecycle runs through the same `service` / `systemctl`
// builtins every other service uses — and stopping it genuinely stops
// scheduled execution, which is world state you can observe.
//
// OWNERSHIP: part of the scheduler workstream (see cron.go).

// cronUnitName is the daemon's unit name for this image: BusyBox spells it
// crond, Debian-family spells it cron. A device gets the one its /bin/sh says
// it has, so `service crond start` on a router and `systemctl start cron` on a
// NAS are both the truth for their box.
func (d *Device) cronUnitName() string {
	if isBusyboxShell(d.OS.Shell) {
		return "crond"
	}
	return "cron"
}

// IsBusyboxShell recognises the BusyBox shell. World data stores it bare
// ("ash"); hand-written profiles may use the full path, and one agent wrote
// "busybox" — all three name the same applet, so all three are accepted.
func IsBusyboxShell(sh string) bool {
	return sh == "ash" || sh == "/bin/ash" || sh == "busybox" || sh == "busybox ash"
}

func isBusyboxShell(sh string) bool { return IsBusyboxShell(sh) }

// EnsureCronDaemon registers the cron daemon unit if this device's image does
// not already have one, and returns it.
func (d *Device) EnsureCronDaemon() *Service {
	if svc := d.CronDaemon(); svc != nil {
		return svc
	}
	name := d.cronUnitName()
	desc := "periodic command scheduler"
	if name == "crond" {
		desc = "BusyBox cron daemon"
	}
	d.Services[name] = &Service{
		Name:  name,
		Desc:  desc,
		Port:  0, // no network socket: it never listens, and ss must not claim it does
		Proto: "local",
		Scope: "lan",
		State: "stopped",
		// "cron" is the handler: the engine's CronTick() consults the unit's
		// state, so the daemon and the scheduler are the same fact.
		Handler: "cron",
		Conf:    "/etc/" + name + ".conf",
	}
	return d.Services[name]
}

// CronDaemon returns the device's cron daemon unit, or nil on an image that
// ships no cron at all.
func (d *Device) CronDaemon() *Service {
	for _, n := range []string{d.cronUnitName(), "crond", "cron"} {
		if svc := d.Svc(n); svc != nil {
			return svc
		}
	}
	return nil
}

// CronDaemonRunning is the gate on scheduled execution: no running daemon, no
// jobs — the same causal rule a real box has.
func (d *Device) CronDaemonRunning() bool {
	svc := d.CronDaemon()
	return svc != nil && svc.State == "running"
}

// CronJobCount is how many jobs are currently armed on this device (loaded
// from the user's real spool files).
func (d *Device) CronJobCount() int {
	if w := d.W; w != nil && w.Cron != nil {
		n := 0
		for _, e := range w.Cron.Entries {
			if e.DeviceID == d.ID {
				n++
			}
		}
		return n
	}
	return 0
}

// StartCronDaemon starts the scheduler through the ordinary service
// machinery, so the pid, the log line and the process table are all real.
func (d *Device) StartCronDaemon() (string, error) {
	svc := d.EnsureCronDaemon()
	return d.StartService(svc.Name)
}

// CronDaemonStarted is what a scheduler does when it comes up: read every
// spool file it owns, arm each job from the current world clock (so a daemon
// that was down for an hour does not stampede an hour of backlog), and say so
// in the log. Every path that brings a daemon up goes through here, whichever
// builtin the player used to do it.
func (w *World) CronDaemonStarted(d *Device) {
	if d == nil || w.Cron == nil {
		return
	}
	if w.Cron.seen == nil {
		w.Cron.seen = map[string]bool{}
	}
	w.Cron.seen[d.ID] = true
	n := 0
	for _, e := range w.Cron.Entries {
		if e.DeviceID != d.ID {
			continue
		}
		n++
		if sp := e.Schedule(); sp != nil {
			if t, ok := sp.Next(w.Sim); ok {
				e.Next = t
			}
		}
	}
	svc := d.CronDaemon()
	name := "cron"
	if svc != nil {
		name = svc.Name
	}
	d.Logf("info", "cron", "%s started: loaded %d job(s) from %s", name, n, CronSpoolDir)
}

// LogCrontabChange records a crontab installation/removal on the device log
// (where `logread cron` finds it) and in the world event feed, then re-reads
// the spool so the change is armed by the very next tick. Stopping the daemon
// needs no helper: `service <unit> stop` already goes through StopService, and
// the scheduler watches that unit's state.
func (w *World) LogCrontabChange(d *Device, what string) {
	if d == nil {
		return
	}
	d.Logf("info", "cron", "%s", what)
	w.AddEvent(d.ID, "info", "cron", "%s on %s", what, d.Hostname)
	w.ReloadCrontabs()
}

// CronStatus is the one-line honest answer a player gets from
// `crontab -l` / `systemctl status crond` on this device.
func (d *Device) CronStatus() string {
	svc := d.CronDaemon()
	if svc == nil {
		return "no cron daemon on this image"
	}
	if svc.State != "running" {
		return svc.Name + " is " + svc.State + " — scheduled jobs do not fire"
	}
	return fmt.Sprintf("%s running (pid %d), %d job(s) armed", svc.Name, svc.PID, d.CronJobCount())
}
