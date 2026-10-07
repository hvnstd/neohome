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

	// ---- §36 磨损: the drivers behind "老硬盘 + 高负载 + 长期运行" ----
	//
	// All three are cumulative counters, never sampled values, so a disk that
	// was old stays old across a reboot: Boot moves, these do not. Thresholds
	// below are the only rule that reads them (see DiskHealth), and a test
	// builds the conjunction by presetting them the way the intrusion tests
	// preset a weak password — state is state, however it got there.
	DiskWrittenB int64 // every byte WriteGuest really stored, for the disk's life
	PowerOnTicks int   // ticks this machine has spent powered
	HotTicks     int   // ticks spent powered while CPU demand exceeded capacity
	// DiskFailAt is when the disk latched FAILED (zero while it answers
	// writes); DiskWarnAt the same for the first WARNING. One-shot warnings
	// stay one-shot the way DiskFullAt does. DiskGraceUntil is the tick until
	// which a repaired disk may not re-latch: fsck buys time, not a new disk.
	DiskFailAt     time.Time
	DiskWarnAt     time.Time
	DiskGraceUntil int
}

// §36's wear model, stated as limits rather than dressed up as errnos (see
// maxFileMB in the shell): a disk's endurance is its own size times write
// cycles, and the age/load limits are in ticks (120 ticks = 1 sim-hour).
const (
	DiskWriteCycles    = 300
	DiskWearWarnFrac   = 0.6
	DiskAgeWarnTicks   = 1440000 // 12,000 sim-hours
	DiskAgeFailTicks   = 2880000 // 24,000 sim-hours
	DiskHotWarnTicks   = 6000    // 50 sim-hours spent overloaded
	DiskHotFailTicks   = 24000   // 200 sim-hours spent overloaded
	DiskFailGraceTicks = 120     // fsck buys one sim-hour before a re-failure
)

// DiskEnduranceMB is the lifetime writes this disk is rated for: its own size
// times the write cycles. A guest's limit is its virtual disk, like every
// other disk rule here.
func (d *Device) DiskEnduranceMB() int { return d.DiskLimitMB() * DiskWriteCycles }

// DiskHealth derives the SMART state from the three wear drivers. Worst wins,
// and the reason always names the driver and its numbers — a failing disk is
// a diagnosis, never "I/O error" with no history behind it.
func (d *Device) DiskHealth() (status, why string) {
	r := d.Resources()
	writtenMB := int(r.DiskWrittenB / 1048576)
	endMB := d.DiskEnduranceMB()
	fail := ""
	switch {
	case r.PowerOnTicks >= DiskAgeFailTicks:
		fail = fmt.Sprintf("power-on %dh exceeds the %dh fail limit", r.PowerOnTicks/120, DiskAgeFailTicks/120)
	case endMB > 0 && writtenMB >= endMB:
		fail = fmt.Sprintf("lifetime writes %d MiB exhausted the %d MiB endurance (%d full-disk writes)", writtenMB, endMB, DiskWriteCycles)
	case r.HotTicks >= DiskHotFailTicks:
		fail = fmt.Sprintf("overloaded %dh exceeds the %dh fail limit", r.HotTicks/120, DiskHotFailTicks/120)
	}
	if fail != "" {
		return "FAILED", fail
	}
	warn := ""
	switch {
	case r.PowerOnTicks >= DiskAgeWarnTicks:
		warn = fmt.Sprintf("power-on %dh past the %dh warning limit", r.PowerOnTicks/120, DiskAgeWarnTicks/120)
	case endMB > 0 && float64(writtenMB) >= float64(endMB)*DiskWearWarnFrac:
		warn = fmt.Sprintf("lifetime writes %d MiB past %.0f%% of the %d MiB endurance", writtenMB, DiskWearWarnFrac*100, endMB)
	case r.HotTicks >= DiskHotWarnTicks:
		warn = fmt.Sprintf("overloaded %dh past the %dh warning limit", r.HotTicks/120, DiskHotWarnTicks/120)
	}
	if warn != "" {
		return "WARNING", warn
	}
	return "PASSED", fmt.Sprintf("power-on %dh, %d MiB written of %d MiB endurance, overloaded %dh",
		r.PowerOnTicks/120, writtenMB, endMB, r.HotTicks/120)
}

// DiskFailed reports whether the disk has latched FAILED: writes really stop
// with EIO until someone runs fsck, the way a dying disk stops answering.
func (d *Device) DiskFailed() bool { return d.Rsrc != nil && !d.Rsrc.DiskFailAt.IsZero() }

// NoteDiskWrite is called by the paths that really move bytes onto a disk, so
// `vmstat`'s io columns describe traffic that happened instead of a constant.
func (d *Device) NoteDiskWrite(bytes int) {
	if bytes <= 0 {
		return
	}
	d.rsrc().DiskWriteMBps = round2(d.rsrc().DiskWriteMBps + float64(bytes)/1048576)
	// §36: the same funnel feeds the lifetime counter behind DiskHealth. Every
	// byte that landed counts toward the endurance, which is what makes "高负载
	// → 磨损" a number instead of a story.
	d.rsrc().DiskWrittenB += int64(bytes)
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

// Quota caps what a machine may use: fewer cores and less RAM than its
// hardware carries. Both bounds are enforced where the consequences live
// (CPUCapacity, memoryTick, SwapTotalMB), never as a display filter.
type Quota struct {
	Cores float64
	RAMMB int
}

// EffCores is the CPU the scheduler divides: the quota when capped.
func (d *Device) EffCores() float64 {
	if d.Quota != nil && d.Quota.Cores > 0 && d.Quota.Cores < float64(d.HW.Cores) {
		return d.Quota.Cores
	}
	return float64(d.HW.Cores)
}

// EffRAMMB is the memory the kernel accounts against: the quota when capped.
func (d *Device) EffRAMMB() int {
	if d.Quota != nil && d.Quota.RAMMB > 0 && d.Quota.RAMMB < d.HW.RAMMB {
		return d.Quota.RAMMB
	}
	return d.HW.RAMMB
}

// SetQuota installs a cap: both bounds must fit inside the hardware, because
// allocating what does not exist is a lie the scheduler would have to keep.
func (d *Device) SetQuota(cores float64, ramMB int) error {
	if cores < 0.5 || ramMB < 128 {
		return fmt.Errorf("quota too small (min 0.5 cores, 128 MiB)")
	}
	if cores > float64(d.HW.Cores) || ramMB > d.HW.RAMMB {
		return fmt.Errorf("quota exceeds hardware (%d cores, %d MiB)",
			d.HW.Cores, d.HW.RAMMB)
	}
	d.Quota = &Quota{Cores: cores, RAMMB: ramMB}
	d.Logf("info", "quota", "capped at %.1f cores, %d MiB RAM", cores, ramMB)
	return nil
}

// ---- CPU --------------------------------------------------------------------

// CPUCapacity is what the machine can execute at once, in the same unit the
// process table uses: one core is 100. A quota narrows it — the share every
// process gets is divided from what is allocated, not what is installed.
func (d *Device) CPUCapacity() float64 {
	c := d.EffCores()
	if c < 0.5 {
		c = 1
	}
	return c * 100
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
// `free` output has always used, now the one owner of that fact. Quota RAM,
// not hardware RAM — swap is sized to what the kernel may use.
func (d *Device) SwapTotalMB() int { return d.EffRAMMB() / 2 }

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

	// ---- §36 wear: power-on and overload accumulate while the machine runs,
	// and the health latch is evaluated from them last of all
	r.PowerOnTicks++
	if d.CPULoad() > 1 {
		r.HotTicks++
	}
	d.diskWearTick()

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
	ram := d.EffRAMMB()
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
					svc.Name, d.EffRAMMB(), swapMax)
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
	d.reckonDroppedLogs(d.fsMount() + " was full")
}

func (d *Device) fsMount() string { return "/" }

// RepairDisk is what fsck does to a FAILED disk: it clears the error latch
// and grants a grace period before the wear can latch again, because the wear
// itself never heals. The health stays whatever the counters say — a repaired
// old disk is still an old disk, and smartctl keeps saying so, which is why
// the repair log tells the operator to back up now.
func (d *Device) RepairDisk() {
	r := d.Resources()
	if r.DiskFailAt.IsZero() {
		return
	}
	r.DiskFailAt = time.Time{}
	r.DiskGraceUntil = d.W.TickCount + DiskFailGraceTicks
	_, why := d.DiskHealth()
	d.Logf("warn", "fsck", "repaired /: writes answer again, but the disk is still failing (%s) — back up now", why)
	d.W.AddEvent(d.ID, "warn", "fsck", "%s: filesystem repaired, disk still failing (%s)", d.Hostname, why)
	d.reckonDroppedLogs("the disk was failing writes")
}

// reckonDroppedLogs writes the "N message(s) dropped" reckoning once logging
// is possible again — the shared tail of every recovery that restores writes.
func (d *Device) reckonDroppedLogs(while string) {
	r := d.rsrc()
	if r.LogDropped == 0 {
		return
	}
	dropped := r.LogDropped
	r.LogDropped = 0
	if err := d.FS.Append("/var/log/syslog", []byte(fmt.Sprintf(
		"%s %s rsyslogd[info]: %d message(s) dropped while %s\n",
		d.W.Sim.Format("Jan 2 15:04:05"), d.Hostname, dropped, while))); err != nil {
		r.LogDropped = dropped
	}
}

// diskWearTick latches the SMART WARNING once and the FAILED state until fsck
// clears it. It runs inside ResourceTick so the numbers describe the world as
// it now is, after every other system has moved.
func (d *Device) diskWearTick() {
	r := d.rsrc()
	status, why := d.DiskHealth()
	if status == "WARNING" && r.DiskWarnAt.IsZero() && r.DiskFailAt.IsZero() {
		r.DiskWarnAt = d.W.Sim
		d.Logf("warn", "kernel", "SMART warning on sda: %s — back up while reads still work (see smartctl)", why)
		d.W.AddEvent(d.ID, "warn", "kernel", "%s: disk health WARNING (%s)", d.Hostname, why)
		return
	}
	if status == "FAILED" && r.DiskFailAt.IsZero() && d.W.TickCount >= r.DiskGraceUntil {
		r.DiskFailAt = d.W.Sim
		d.Logf("err", "kernel", "I/O error on sda: %s — the filesystem is failing writes", why)
		d.W.AddEvent(d.ID, "err", "kernel", "%s: disk FAILED (%s) — writes fail until fsck", d.Hostname, why)
	}
}
