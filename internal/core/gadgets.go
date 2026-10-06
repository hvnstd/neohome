package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// §15 家庭设备: the laptop, the printer and the NAS's dependants
//
// The spec lists the devices a household has (PC, laptop, phone, router, NAS,
// camera, lock, printer, switch, UPS, server, VM host) and then states what the
// dependencies between them mean: a laptop on battery that runs flat stops, a
// printer queues real jobs and runs out of paper, and a NAS that stops takes
// backups with it.
//
// Each of those is state here, not a sentence in a manual.
// ---------------------------------------------------------------------------

// SpoolDir is where a printer's queue lives. The queue is a file on the
// printer's own disk, so a power cycle does not lose the documents people
// already sent — which is exactly what a real spooler is for.
const SpoolDir = "/var/spool/cups"

// PrintPort is the IPP port a client submits a job to.
const PrintPort = 631

// ---- the printer ----

// PrinterOf returns a device's printer state, or nil if it is not a printer.
func (d *Device) PrinterOf() *PrinterState { return d.Printer }

// LaptopBatteryOf returns a device's battery, or nil if it is not a laptop.
func (d *Device) LaptopBatteryOf() *LaptopBattery { return d.Battery }

// BatteryLow is the level at which a laptop warns its owner, as laptops do.
const BatteryLow = 15

// SubmitPrint queues a document. The job is written to the printer's spool
// directory, so `lpstat` reads the queue off the disk and a reload keeps it.
func (w *World) SubmitPrint(d *Device, owner, title string, pages int) (*PrintJob, error) {
	if d.Printer == nil {
		return nil, fmt.Errorf("%s is not a printer", d.Hostname)
	}
	if pages <= 0 {
		pages = 1
	}
	if title == "" {
		title = "(stdin)"
	}
	d.Printer.NextID++
	job := PrintJob{ID: d.Printer.NextID, Owner: owner, Title: title, Pages: pages, State: "queued"}
	d.Printer.Queue = append(d.Printer.Queue, job)
	w.writeSpool(d)
	d.Logf("info", "cupsd", "job %d queued by %s: %s (%d page%s)", job.ID, owner, title, pages, pageS(pages))
	return &d.Printer.Queue[len(d.Printer.Queue)-1], nil
}

// CancelPrint removes a queued job. Cancelling something already printed is not
// possible, and saying so is better than pretending.
func (w *World) CancelPrint(d *Device, id int, owner string, isRoot bool) error {
	if d.Printer == nil {
		return fmt.Errorf("%s is not a printer", d.Hostname)
	}
	for i := range d.Printer.Queue {
		j := &d.Printer.Queue[i]
		if j.ID != id {
			continue
		}
		if j.Owner != owner && !isRoot {
			return fmt.Errorf("job %d belongs to %s — you cannot cancel it", id, j.Owner)
		}
		if j.State == "done" {
			return fmt.Errorf("job %d has already printed", id)
		}
		d.Printer.Queue = append(d.Printer.Queue[:i], d.Printer.Queue[i+1:]...)
		w.writeSpool(d)
		d.Logf("info", "cupsd", "job %d cancelled by %s", id, owner)
		return nil
	}
	return fmt.Errorf("no job %d in the queue", id)
}

// PrintTick advances the spooler: one job at a time, using real paper and real
// toner. A printer that is out of paper holds the job instead of losing it, and
// the reason is visible in `lpstat` — which is what a player needs to see.
func (w *World) PrintTick() {
	for _, id := range w.Order {
		d := w.Devices[id]
		p := d.Printer
		if p == nil || len(p.Queue) == 0 {
			continue
		}
		if !d.NetUp {
			continue // a powered-off printer prints nothing; the queue waits
		}
		job := &p.Queue[0]
		if job.State == "done" {
			p.Queue = p.Queue[1:]
			w.writeSpool(d)
			continue
		}
		switch {
		case p.Offline:
			job.Blocked("printer is offline")
			continue
		case p.Paper <= 0:
			job.Blocked("out of paper")
			if p.Paper == 0 {
				d.Logf("warn", "cupsd", "job %d held: out of paper", job.ID)
			}
			continue
		case p.Toner <= 0:
			job.Blocked("out of toner")
			continue
		}
		// print as much as this tick can: a sheet costs pageWork units and the
		// printer earns its own CPU share per tick, so an idle machine prints a
		// page every other tick and a loaded one prints more slowly (§17's
		// "进程变慢" applied to the one process a household notices)
		p.Credit += d.CPUShare()
		if p.Credit < pageWork {
			continue
		}
		p.Credit -= pageWork
		job.State = "printing"
		p.Paper--
		p.Printed++
		job.Pages--
		if job.Pages <= 0 {
			job.State = "done"
			p.Queue = p.Queue[1:]
			d.Logf("info", "cupsd", "job %d (%s) completed", job.ID, job.Title)
			w.AddEvent(d.ID, "info", "printer", "job %d (%s) printed", job.ID, job.Title)
			// A sheet of text is real output: it lands in the printer's tray
			// file, which is what a player finds when they look.
			tray := fmt.Sprintf("Printed by %s: %s\n", job.Owner, job.Title)
			if _, ok := d.FS.Read(SpoolDir + "/tray.log"); !ok {
				d.FS.Write(SpoolDir+"/tray.log", "", 0644, "root", "lp")
			}
			d.FS.Append(SpoolDir+"/tray.log", []byte(tray))
		} else {
			job.State = "printing"
		}
		// toner: about 1% per 40 sheets, as a cartridge really behaves
		if p.Printed%40 == 0 {
			p.Toner--
			if p.Toner == 10 {
				d.Logf("warn", "cupsd", "toner low: %d%%", p.Toner)
			}
		}
		w.writeSpool(d)
	}
}

// Blocked records why a job is not moving, on the job itself.
func (j *PrintJob) Blocked(reason string) {
	j.State = "held"
	j.Reason = reason
}

// LoadPaper refills the tray. This is the fix for the commonest printer
// problem, and it makes `lpstat`'s held job start moving again.
func (d *Device) LoadPaper(sheets int) error {
	if d.Printer == nil {
		return fmt.Errorf("%s is not a printer", d.Hostname)
	}
	if sheets <= 0 {
		return fmt.Errorf("load a positive number of sheets")
	}
	d.Printer.Paper += sheets
	d.Printer.Offline = false
	for i := range d.Printer.Queue {
		if d.Printer.Queue[i].State == "held" {
			d.Printer.Queue[i].State = "queued"
			d.Printer.Queue[i].Reason = ""
		}
	}
	d.Logf("info", "cupsd", "%d sheet(s) loaded", sheets)
	d.W.writeSpool(d)
	return nil
}

// SetToner replaces the cartridge.
func (d *Device) SetToner(pct int) error {
	if d.Printer == nil {
		return fmt.Errorf("%s is not a printer", d.Hostname)
	}
	if pct < 0 || pct > 100 {
		return fmt.Errorf("toner is a percentage")
	}
	d.Printer.Toner = pct
	d.Logf("info", "cupsd", "toner cartridge replaced (%d%%)", pct)
	return nil
}

// SetPrinterOffline pauses the printer without powering it off — real printers
// have that switch, and it is the reason a job can sit in a queue forever.
func (d *Device) SetPrinterOffline(off bool) error {
	if d.Printer == nil {
		return fmt.Errorf("%s is not a printer", d.Hostname)
	}
	d.Printer.Offline = off
	return nil
}

func (w *World) writeSpool(d *Device) {
	var b strings.Builder
	for _, j := range d.Printer.Queue {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%d\t%s", j.ID, j.Owner, j.Title, j.Pages, j.State)
		if j.Reason != "" {
			fmt.Fprintf(&b, "\t%s", j.Reason)
		}
		b.WriteByte('\n')
	}
	d.FS.Write(SpoolDir+"/queue", b.String(), 0644, "root", "lp")
}

// PrintReport renders the printer's state for `lpstat`.
func PrintReport(d *Device) []string {
	p := d.Printer
	if p == nil {
		return nil
	}
	out := []string{fmt.Sprintf("printer %s is %s", d.Hostname, map[bool]string{true: "offline", false: "idle"}[p.Offline])}
	out = append(out, fmt.Sprintf("paper: %d sheet(s), toner: %d%%, printed: %d sheet(s)", p.Paper, p.Toner, p.Printed))
	if len(p.Queue) == 0 {
		out = append(out, "no jobs")
		return out
	}
	for _, j := range p.Queue {
		line := fmt.Sprintf("%-4d %-8s %-9s %3d page(s)  %s", j.ID, j.Owner, j.State, j.Pages, j.Title)
		if j.Reason != "" {
			line += "  (" + j.Reason + ")"
		}
		out = append(out, line)
	}
	return out
}

func pageS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- the laptop ----

// BatteryTick drains a laptop's battery when it is not charging, and stops the
// machine when it runs flat. A laptop is the one household device whose power
// state is its own business rather than the wall's.
func (w *World) BatteryTick() {
	for _, id := range w.Order {
		d := w.Devices[id]
		b := d.Battery
		if b == nil {
			continue
		}
		if b.Charging {
			if b.Pct < 100 {
				b.Pct += 2
				if b.Pct > 100 {
					b.Pct = 100
				}
			}
			if b.Pct == 100 && !d.NetUp {
				// docked and charged: a closed laptop still does not run
				if b.LidOpen {
					d.setPowered(true, "")
				}
			}
			continue
		}
		if !d.NetUp {
			continue // a dark laptop is not draining, it is off
		}
		if w.TickCount%2 == 0 {
			b.Pct--
		}
		if b.Pct == BatteryLow {
			d.Logf("warn", "kernel", "battery low: %d%% — plug in the charger", b.Pct)
			d.W.AddEvent(d.ID, "warn", "power", "%s battery at %d%%", d.Hostname, b.Pct)
		}
		if b.Pct <= 0 {
			b.Pct = 0
			d.W.AddEvent(d.ID, "err", "power", "%s ran out of battery", d.Hostname)
			d.setPowered(false, "battery empty")
		}
	}
}

// SetLid opens or closes a laptop's lid. A closed lid suspends the machine: its
// services stop and it leaves the network, exactly as a real one does — unless
// it is docked to a charger and told to stay awake, which is why `LidMode`
// matters to somebody running a home server off a laptop.
func (d *Device) SetLid(open bool) error {
	if d.Battery == nil {
		return fmt.Errorf("%s is not a laptop", d.Hostname)
	}
	if d.Battery.LidOpen == open {
		return nil
	}
	d.Battery.LidOpen = open
	if !open {
		d.W.DarkenPoE(d)
		d.setPowered(false, "lid closed — suspended")
		return nil
	}
	if d.Battery.Pct > 0 || d.Battery.Charging {
		d.setPowered(true, "")
	}
	return nil
}

// SetCharging plugs or unplugs the laptop's charger. It is the same state as
// the physical act `power plug|unplug`: one is done at the machine, the other
// at the socket, and both leave the laptop knowing which it is on.
func (d *Device) SetCharging(on bool) error {
	if d.Battery == nil {
		return fmt.Errorf("%s is not a laptop", d.Hostname)
	}
	if d.Battery.Charging == on && d.MainsDropped == !on {
		return nil
	}
	d.Battery.Charging = on
	d.MainsDropped = !on
	if on {
		d.Logf("info", "kernel", "AC adapter connected")
		if !d.NetUp && d.Battery.LidOpen {
			d.setPowered(true, "")
		}
		return nil
	}
	d.Logf("info", "kernel", "AC adapter disconnected — on battery (%d%%)", d.Battery.Pct)
	if d.Battery.Pct <= 0 {
		d.setPowered(false, "battery empty")
	}
	return nil
}

// BatteryReport is what `laptopctl status` prints.
func BatteryReport(d *Device) []string {
	b := d.Battery
	if b == nil {
		return nil
	}
	state := "discharging"
	if b.Charging {
		state = "charging"
	}
	lid := "open"
	if !b.LidOpen {
		lid = "closed"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: %d%% (%s), lid %s", d.Hostname, b.Pct, state, lid))
	lines = append(lines, fmt.Sprintf("power: %s, uptime %s", map[bool]string{true: "on", false: "off"}[d.NetUp], d.Uptime().Round(1e9)))
	if !b.Charging && b.Pct < 100 {
		lines = append(lines, fmt.Sprintf("estimated runtime: about %d game-minutes", b.Pct*4))
	}
	return lines
}

// ---- the NAS's dependants: backups ----

// BackupDir is where the household's backups live on the NAS.
const BackupDir = "/srv/backups"

// RunBackup copies a directory tree from one device to the NAS over the
// network. It goes through the same packet path everything else does, so a NAS
// that is down, or a switch port that is down, fails the backup for the real
// reason — and the failure is what the player sees, not a retry loop.
func (w *World) RunBackup(src *Device, nas *Device, path string) (string, error) {
	svc, _, msg := Dial(src, nas.FirstLANIP(), 445)
	if msg != "connected" {
		return "", fmt.Errorf("backup target unreachable: %s", msg)
	}
	if svc == nil {
		return "", fmt.Errorf("the NAS is not serving shares")
	}
	files := 0
	bytes := 0
	for p, n := range src.FS.Nodes {
		if n.IsDir || !strings.HasPrefix(p, path) {
			continue
		}
		dest := BackupDir + "/" + src.Hostname + p
		nas.FS.MkdirAll(dirOf(dest), 0750, "root", "root")
		nas.FS.Write(dest, string(n.Data), 0640, "root", "root")
		files++
		bytes += len(n.Data)
	}
	if files == 0 {
		return "", fmt.Errorf("nothing to back up under %s", path)
	}
	src.BackupIndex++
	nas.BackupIndex = src.BackupIndex
	stamp := w.Sim.Format("2006-01-02 15:04")
	nas.FS.MkdirAll(BackupDir, 0750, "root", "root")
	if _, ok := nas.FS.Read(BackupDir + "/index.log"); !ok {
		nas.FS.Write(BackupDir+"/index.log", "", 0640, "root", "root")
	}
	nas.FS.Append(BackupDir+"/index.log",
		[]byte(fmt.Sprintf("%s  %s:%s  %d file(s), %d bytes\n", stamp, src.Hostname, path, files, bytes)))
	src.Logf("info", "backup", "%d file(s) copied to %s:%s", files, nas.Hostname, BackupDir)
	nas.Logf("info", "smbd", "backup from %s received (%d file(s))", src.Hostname, files)
	w.AddEvent(nas.ID, "info", "backup", "%s backed up %s to %s", src.Hostname, path, nas.Hostname)
	return fmt.Sprintf("%d file(s), %d bytes → %s:%s", files, bytes, nas.Hostname, BackupDir), nil
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}

// atoiOr is a small helper for the shell layer's numeric flags.
func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
