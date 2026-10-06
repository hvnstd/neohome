package core

import (
	"fmt"
	"math"
	"time"
)

// §17 系统状态 / 资源管理 — resources are not decoration.
//
// Everything in this file is read back by the monitoring commands (`htop`,
// `top`, `free`, `df`, `uptime`, `vmstat`) *and* does something: CPU demand is
// shared out so a hog really slows its neighbours, memory pressure really pages
// into swap and then really kills a process, a full disk really refuses writes,
// and the process table really has a limit. The display and the consequence
// come from the same numbers — there is no second, prettier copy.

// Rsrc is a device's kernel-side accounting: the quantities a real machine
// keeps that cannot be derived from the files or the process table alone.
//
// Deliberately *not* here: memory used, disk used, process count — those are
// derived from the real state every time they are asked for, so they can never
// disagree with it.
type Rsrc struct {
	Load1  float64
	Load5  float64
	Load15 float64

	SwapUsedMB  int
	SwapInTotal int
	SwapOutTot  int

	OOMCount    int
	LastOOMTick int
	LastOOM     string

	ForksRefused int

	// LogDropped counts syslog lines that could not be written because the disk
	// was full. rsyslog really loses messages this way, and really says so once
	// the space comes back.
	LogDropped int

	// DiskFullAt is when the filesystem filled up (zero while there is room),
	// which is how the one-shot warning stays one-shot.
	DiskFullAt time.Time

	// Share is the last measured CPU share (1.0 = nothing had to contend), and
	// WorkRate the last measured work multiplier from the machine's link and
	// disk. Both are consequences of the process table, not inputs.
	Share    float64
	WorkRate float64

	// DiskMBpsBench overrides the throughput this machine is assumed to have;
	// zero means "derive it from the profile" (see DiskMBps).
	DiskMBpsBench float64

	// Per-tick rates, in MiB: what actually moved during the last tick. They
	// decay every tick (see ResourceTick) instead of accumulating, so a
	// monitoring sample never claims traffic that is not happening. The bulk
	// paths that know their byte counts feed them: user writes, dd, an FTP
	// transfer and a mirror sync.
	DiskWriteMBps float64
	DiskReadMBps  float64
	NetRxMBps     float64
	NetTxMBps     float64
}

// NoteDiskWrite is called by the paths that really move bytes onto a disk, so
// `vmstat`'s io columns describe traffic that happened instead of a constant.
func (d *Device) NoteDiskWrite(bytes int) {
	if bytes <= 0 {
		return
	}
	d.rsrc().DiskWriteMBps = round2(d.rsrc().DiskWriteMBps + float64(bytes)/1048576)
}

func (d *Device) NoteDiskRead(bytes int) {
	if bytes <= 0 {
		return
	}
	d.rsrc().DiskReadMBps = round2(d.rsrc().DiskReadMBps + float64(bytes)/1048576)
}

// NoteNetTx and NoteNetRx are the same for the wire: a transfer of N bytes on a
// machine's link. A path that does not know its byte count does not call them.
func (d *Device) NoteNetTx(bytes int) {
	if bytes <= 0 {
		return
	}
	d.rsrc().NetTxMBps = round2(d.rsrc().NetTxMBps + float64(bytes)/1048576)
}

func (d *Device) NoteNetRx(bytes int) {
	if bytes <= 0 {
		return
	}
	d.rsrc().NetRxMBps = round2(d.rsrc().NetRxMBps + float64(bytes)/1048576)
}

// LoadProcs is the runnable set (state R) — what `vmstat`'s r column and `top`
// consider busy.
func (d *Device) LoadProcs() (running, blocked int) {
	for _, p := range d.Procs {
		switch p.State {
		case "R", "D":
			running++
		case "S":
			blocked++
		}
	}
	return
}

// Resources returns the device's resource accounting, creating it on first use.
// An older save has none, and a machine must never panic over bookkeeping.
func (d *Device) Resources() *Rsrc {
	if d.Rsrc == nil {
		d.Rsrc = &Rsrc{Share: 1, WorkRate: 1}
	}
	if d.Rsrc.Share == 0 {
		d.Rsrc.Share = 1
	}
	if d.Rsrc.WorkRate == 0 {
		d.Rsrc.WorkRate = 1
	}
	return d.Rsrc
}

func (d *Device) rsrc() *Rsrc { return d.Resources() }

// ---- CPU --------------------------------------------------------------------

// CPUCapacity is what the machine can execute at once, in the same unit the
// process table uses: one core is 100.
func (d *Device) CPUCapacity() float64 {
	c := d.HW.Cores
	if c < 1 {
		c = 1
	}
	return float64(c) * 100
}

// CPUDemand is the CPU the process table is asking for. A process that has not
// declared a want yet wants what it currently has — which keeps older states
// (and older saves) honest instead of dropping them to zero.
func (d *Device) CPUDemand() float64 {
	total := 0.0
	for _, p := range d.Procs {
		if p.WantCPU > 0 {
			total += p.WantCPU
		} else {
			total += p.CPU
		}
	}
	return total
}

// CPUShare is the fraction of its request each process gets. Above one core of
// demand on a one-core machine the share drops below 1 and every process — the
// player's own daemons included — really gets less CPU per tick.
func (d *Device) CPUShare() float64 {
	capacity := d.CPUCapacity()
	demand := d.CPUDemand()
	if demand <= capacity || demand <= 0 {
		return 1
	}
	share := capacity / demand
	if share < 0.05 {
		share = 0.05 // a real scheduler never quite stops the queue
	}
	return round2(share)
}

// CPULoad is the runnable demand as a multiple of capacity: 1.0 means the
// machine is exactly busy, 4.0 means four times as much work as it can do.
func (d *Device) CPULoad() float64 {
	capacity := d.CPUCapacity()
	if capacity <= 0 {
		return 0
	}
	return d.CPUDemand() / capacity
}

// ---- memory -----------------------------------------------------------------

// SwapTotalMB is this machine's swap: half its RAM, which is the same rule the
// `free` output has always used, now the one owner of that fact.
func (d *Device) SwapTotalMB() int { return d.HW.RAMMB / 2 }

// ---- disk -------------------------------------------------------------------

// DiskLimitMB is the storage this device can actually write into: a guest's
// virtual disk when it is a guest, otherwise the machine's own disk. One rule,
// so a guest sized too small and a host with a full disk fail the same way.
func (d *Device) DiskLimitMB() int {
	if d.W != nil && d.W.VMs != nil {
		if v := d.W.VMs.guestOf(d.ID); v != nil && v.VDiskM > 0 {
			return v.VDiskM
		}
	}
	return d.HW.DiskMB
}

// DiskFreeMB is what is left, never negative.
func (d *Device) DiskFreeMB() int {
	free := d.DiskLimitMB() - d.FS.DiskUsedMB()
	if free < 0 {
		return 0
	}
	return free
}

// DiskMBps is the throughput this machine is assumed to have. A NAS is built to
// move bytes, a router's flash is not — and it is what makes "Disk I/O" in the
// spec a number rather than a word. A device (or a test) can override it.
func (d *Device) DiskMBps() float64 {
	if d.Rsrc != nil && d.Rsrc.DiskMBpsBench > 0 {
		return d.Rsrc.DiskMBpsBench
	}
	switch d.Profile {
	case "pc", "laptop", "server", "infra", "vps", "core":
		return 500
	case "nas":
		return 200
	case "phone":
		return 80
	case "iot":
		return 40
	}
	return 20 // router, switch, printer: the slow flash that actually ships in them
}

// NetMbps is this machine's link speed, defaulted the way a network stack does
// rather than left at zero.
func (d *Device) NetMbps() float64 {
	if d.HW.NetMbps <= 0 {
		return 100
	}
	return float64(d.HW.NetMbps)
}

// WorkRate is how much bulk work this machine completes per tick: link and disk
// throughput, reduced by CPU starvation. The reference is "one gigabit and a
// 500 MB/s disk does a unit of work per tick", which is what every "one phase
// per tick" system already assumed — this is where that assumption is written
// down, so a NAS with a slow link really does take longer than a fast mirror.
func (d *Device) WorkRate() float64 {
	rate := math.Min(d.NetMbps(), d.DiskMBps()) / 500
	if rate > 1 {
		rate = 1
	}
	if rate < 0.05 {
		rate = 0.05
	}
	return round2(rate * d.CPUShare())
}

// ---- process table ---------------------------------------------------------

// ProcLimit is how many processes this machine can hold. A real small Linux box
// hits RLIMIT_NPROC long before pid_max, and a 128 MiB camera has no business
// running a hundred processes — the limit is what makes "Process Count" in the
// spec matter.
func (d *Device) ProcLimit() int {
	lim := d.HW.RAMMB / 8
	if lim < 24 {
		lim = 24
	}
	return lim
}

// Spawn adds a process if the machine has room for it. Forking past the limit
// is refused the way a kernel refuses it, with the same errno text — and the
// refusal is counted, because an operator hitting it needs to see why.
func (w *World) Spawn(d *Device, p *Proc) error {
	if lim := d.ProcLimit(); lim > 0 && len(d.Procs) >= lim {
		d.rsrc().ForksRefused++
		return fmt.Errorf("Resource temporarily unavailable (process limit %d reached)", lim)
	}
	if p.Kind == "" {
		p.Kind = "task"
	}
	d.AddProc(p)
	return nil
}

// ---- the tick --------------------------------------------------------------

// ResourceTick is where the accounting is charged and the consequences happen.
// It runs after every other system has moved so the numbers describe the world
// as it now is.
func (w *World) ResourceTick() {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		d.resourceTick()
	}
}

func (d *Device) resourceTick() {
	r := d.rsrc()
	if !d.Powered() {
		// a dark machine has no load, no swap and no throughput
		r.Share = 1
		r.WorkRate = 1
		r.Load1 = round2(r.Load1 * 0.7)
		r.Load5 = round2(r.Load5 * 0.86)
		r.Load15 = round2(r.Load15 * 0.95)
		return
	}

	// ---- CPU: what each process asked for, times the share it could get
	share := d.CPUShare()
	for _, p := range d.Procs {
		if p.WantCPU <= 0 {
			p.WantCPU = p.CPU
		}
		if p.WantCPU > 0 {
			p.CPU = round2(p.WantCPU * share)
		}
		if p.State == "R" && p.CPU == 0 {
			p.State = "S" // it wanted nothing this tick; it is sleeping again
		}
	}
	r.Share = share

	// ---- load average: the kernel's own exponentially weighted average
	load := d.CPULoad()
	r.Load1 = round2(r.Load1*0.7 + load*0.3)
	r.Load5 = round2(r.Load5*0.86 + load*0.14)
	r.Load15 = round2(r.Load15*0.95 + load*0.05)

	d.reapLoad()

	// ---- the per-tick rates decay: a sample describes the last tick, and a
	// tick with no traffic reports none
	r.DiskWriteMBps = round2(r.DiskWriteMBps * 0.5)
	r.DiskReadMBps = round2(r.DiskReadMBps * 0.5)
	r.NetRxMBps = round2(r.NetRxMBps * 0.5)
	r.NetTxMBps = round2(r.NetTxMBps * 0.5)

	// ---- memory: RAM, then swap, then the OOM killer
	d.memoryTick()

	// ---- disk: the full latch and the dropped-log count
	d.diskTick()

	r.WorkRate = d.WorkRate()
}

// reapLoad retires load processes whose timeout has run out: `stress --timeout`
// is a background load with a real end, and a machine that is never relieved
// would make the player's own workload unfixable.
func (d *Device) reapLoad() {
	keep := d.Procs[:0]
	for _, p := range d.Procs {
		if p.Kind == "load" && p.EndTick > 0 && d.W.TickCount >= p.EndTick {
			d.Logf("info", "kernel", "%d (%s) finished after %d tick(s)", p.PID, p.Name, d.W.TickCount-p.StartTick)
			if svc := d.Svc(p.Svc); svc != nil && svc.PID == p.PID {
				svc.State = "stopped"
				svc.PID = 0
			}
			continue
		}
		keep = append(keep, p)
	}
	d.Procs = keep
}

// memoryTick is §17's causal chain for RAM, and it is the same chain the
// hypervisor already implements for guests: over RAM → swap → out of memory →
// a process dies (or, with nothing left to kill, the services fail). The
// difference is only who owns the limit — here it is the machine itself.
func (d *Device) memoryTick() {
	r := d.rsrc()
	ram := d.HW.RAMMB
	if ram <= 0 {
		return
	}
	used := d.MemUsed()
	if used <= ram {
		if r.SwapUsedMB > 0 {
			back := r.SwapUsedMB / 4
			if back < 1 {
				back = 1
			}
			if back > r.SwapUsedMB {
				back = r.SwapUsedMB
			}
			r.SwapUsedMB -= back
			r.SwapInTotal += back
			if r.SwapUsedMB == 0 {
				d.Logf("info", "kernel", "swap paging complete: anonymous pages are back in RAM")
			}
		}
		return
	}
	excess := used - ram
	swapMax := d.SwapTotalMB()
	if free := swapMax - r.SwapUsedMB; free > 0 {
		move := excess
		if move > free {
			move = free
		}
		r.SwapUsedMB += move
		r.SwapOutTot += move
		d.Logf("warn", "kernel", "swapped out %d MiB (swap %d/%d MiB, %d MiB over RAM)",
			move, r.SwapUsedMB, swapMax, excess)
		if r.SwapUsedMB < swapMax {
			return
		}
	}
	d.oomKill(excess, swapMax)
}

// oomKill is the kernel's last resort, and it really removes a process. One
// over-limit period is one incident: a real kernel kills repeatedly while the
// condition holds, and counting each kill would make the number meaningless.
func (d *Device) oomKill(excess, swapMax int) {
	r := d.rsrc()
	incident := r.OOMCount == 0 || r.LastOOMTick < d.W.TickCount-3
	r.LastOOMTick = d.W.TickCount

	var victim *Proc
	for _, p := range d.Procs {
		if p.Kind == "builtin" {
			continue // daemons are not the workload that got the machine here
		}
		if victim == nil || p.Mem > victim.Mem {
			victim = p
		}
	}
	if victim == nil {
		if !incident {
			return
		}
		// nothing but daemons left: this machine is out of memory, and the
		// services are what fails next — the honest end state of an
		// undersized box
		r.OOMCount++
		r.LastOOM = "out of memory: no victim left, services failed"
		for _, svc := range d.Services {
			if svc.State == "running" {
				svc.State = "failed"
				svc.PID = 0
				d.Logf("err", "kernel", "service %s failed — out of memory (%d MiB RAM, %d MiB swap)",
					svc.Name, d.HW.RAMMB, swapMax)
			}
		}
		d.W.AddEvent(d.ID, "err", "kernel", "%s is out of memory: every service failed", d.Hostname)
		return
	}

	keep := d.Procs[:0]
	for _, p := range d.Procs {
		if p != victim {
			keep = append(keep, p)
		}
	}
	d.Procs = keep
	if svc := d.Svc(victim.Svc); svc != nil && svc.PID == victim.PID {
		svc.State = "failed"
		svc.PID = 0
	}
	why := fmt.Sprintf("Out of memory: Killed process %d (%s) total-vm:%dMB, anon-rss:%dMB, swap:%d/%dMB",
		victim.PID, victim.Name, victim.Mem*2, victim.Mem, r.SwapUsedMB, swapMax)
	d.Logf("err", "kernel", "%s", why)
	if incident {
		r.OOMCount++
		r.LastOOM = why
		d.W.AddEvent(d.ID, "warn", "kernel", "%s OOM-killed %s (%d MiB over RAM)", d.Hostname, victim.Name, excess)
	}
}

// diskTick latches "the filesystem is full" so the warning is written once and
// the recovery is written once — and so the dropped-log count can be reported
// when there is space for the report again.
func (d *Device) diskTick() {
	r := d.rsrc()
	limit := d.DiskLimitMB()
	if limit <= 0 {
		return
	}
	used := d.FS.DiskUsedMB()
	if used >= limit {
		if r.DiskFullAt.IsZero() {
			r.DiskFullAt = d.W.Sim
			// the kernel ring buffer still has it (dmesg), even when the
			// filesystem it would be logged to cannot take the line
			d.Logf("err", "kernel", "EXT4-fs warning (device sda1): %s is full (%d/%d MiB) — no space left on device",
				d.fsMount(), used, limit)
			d.W.AddEvent(d.ID, "warn", "kernel", "%s: disk full (%d/%d MiB) — writes will fail",
				d.Hostname, used, limit)
		}
		return
	}
	if r.DiskFullAt.IsZero() {
		return
	}
	r.DiskFullAt = time.Time{}
	d.Logf("info", "kernel", "space reclaimed on %s (%d/%d MiB)", d.fsMount(), used, limit)
	if r.LogDropped > 0 {
		dropped := r.LogDropped
		r.LogDropped = 0
		if err := d.FS.Append("/var/log/syslog", []byte(fmt.Sprintf(
			"%s %s rsyslogd[info]: %d message(s) dropped while %s was full\n",
			d.W.Sim.Format("Jan 2 15:04:05"), d.Hostname, dropped, d.fsMount()))); err == nil {
			r.LogDropped = 0
		}
	}
}

func (d *Device) fsMount() string { return "/" }
