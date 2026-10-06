package shell

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// §17 系统状态 / 资源管理: the tools a player uses to *make* a machine busy, and
// the one that reads the numbers back. Nothing here sleeps and nothing here
// pretends: `stress` really adds processes to the table, `dd` really writes
// bytes into the filesystem, and `vmstat` prints what the last tick did.
func init() {
	builtinTable["stress"] = cmdStress
	builtinTable["dd"] = cmdDd
	builtinTable["vmstat"] = cmdVmstat
}

// ---- stress ----------------------------------------------------------------

// stress puts real load on the machine. This world has no job control to wait
// on, so a load runs in the background with a real end: `--timeout` ticks, or
// `pkill stress` — the same two ways a real operator finishes one off.
func cmdStress(s *Shell, args []string) int {
	cpu, vm, vmBytes, timeout := 1, 0, 256, 0
	usage := "usage: stress [--cpu N] [--vm N] [--vm-bytes SIZE] [--timeout TICKS]"
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, bool) {
			if eq := strings.IndexByte(a, '='); eq >= 0 {
				return a[eq+1:], true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "--cpu" || strings.HasPrefix(a, "--cpu="):
			v, ok := val()
			if !ok {
				s.errf("stress: option '--cpu' requires an argument")
				return 1
			}
			cpu, _ = strconv.Atoi(v)
		case a == "--vm" || strings.HasPrefix(a, "--vm="):
			v, ok := val()
			if !ok {
				s.errf("stress: option '--vm' requires an argument")
				return 1
			}
			vm, _ = strconv.Atoi(v)
		case a == "--vm-bytes" || strings.HasPrefix(a, "--vm-bytes="):
			v, ok := val()
			if !ok {
				s.errf("stress: option '--vm-bytes' requires an argument")
				return 1
			}
			n, err := parseSizeMB(v)
			if err != nil {
				s.errf("stress: invalid --vm-bytes '%s'", v)
				return 1
			}
			vmBytes = n
		case a == "--timeout" || strings.HasPrefix(a, "--timeout="):
			v, ok := val()
			if !ok {
				s.errf("stress: option '--timeout' requires an argument")
				return 1
			}
			timeout, _ = strconv.Atoi(v)
		case a == "--help":
			fmt.Fprintln(s.Out, usage)
			return 0
		default:
			s.errf("stress: unrecognized option '%s'\n%s", a, usage)
			return 1
		}
	}
	if cpu < 0 || vm < 0 || cpu+vm == 0 {
		s.errf("stress: nothing to do (give at least one --cpu or --vm worker)")
		return 1
	}
	end := 0
	if timeout > 0 {
		end = s.W.TickCount + timeout
	}
	d := s.Dev
	started := 0
	fmt.Fprintf(s.Out, "stress: info: [%d] dispatching hogs: %d cpu, %d vm\n", d.W.NewPID(), cpu, vm)
	for i := 0; i < cpu; i++ {
		p := &core.Proc{Name: "stress", Args: fmt.Sprintf("--cpu %d", i+1), User: s.User.Name,
			CPU: 100, WantCPU: 100, Mem: 4, TTY: s.TTY, State: "R", Start: s.W.Sim,
			StartTick: s.W.TickCount, EndTick: end, Kind: "load"}
		if err := s.W.Spawn(d, p); err != nil {
			// the fork is refused, like a kernel refusing it: the load that was
			// already started keeps running, and the reason is the limit
			s.errf("stress: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "stress: info: [%d] (cpu) <%d> started\n", p.PID, i+1)
		started++
	}
	for i := 0; i < vm; i++ {
		p := &core.Proc{Name: "stress", Args: fmt.Sprintf("--vm %d --vm-bytes %dM", i+1, vmBytes),
			User: s.User.Name, CPU: 100, WantCPU: 100, Mem: vmBytes, TTY: s.TTY, State: "R",
			Start: s.W.Sim, StartTick: s.W.TickCount, EndTick: end, Kind: "load"}
		if err := s.W.Spawn(d, p); err != nil {
			s.errf("stress: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "stress: info: [%d] (vm) <%d> started (%d MiB resident)\n", p.PID, i+1, vmBytes)
		started++
	}
	if end > 0 {
		fmt.Fprintf(s.Out, "stress: info: %d worker(s) running for %d tick(s) (world time)\n", started, timeout)
	} else {
		fmt.Fprintf(s.Out, "stress: info: %d worker(s) running — `pkill stress` stops them\n", started)
	}
	fmt.Fprintf(s.Out, "stress: info: watch it with `uptime`, `htop` and `free`\n")
	return 0
}

// parseSizeMB reads the sizes people actually type at stress: 256M, 1G, 512.
func parseSizeMB(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("empty")
	}
	mult := 1
	switch v[len(v)-1] {
	case 'k', 'K':
		mult, v = 1, v[:len(v)-1]
		kb, err := strconv.Atoi(v)
		if err != nil {
			return 0, err
		}
		mb := kb / 1024
		if mb < 1 {
			mb = 1
		}
		return mb, nil
	case 'm', 'M':
		mult, v = 1, v[:len(v)-1]
	case 'g', 'G':
		mult, v = 1024, v[:len(v)-1]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}

// ---- dd --------------------------------------------------------------------

// maxFileMB is the largest content this world's file model can hold. It is a
// documented model limit, not an errno: `dd` says so instead of pretending the
// filesystem refused it, and a disk still fills the way real disks do — with
// many files.
const maxFileMB = 64

// dd writes real bytes into the filesystem. A disk that is full really stops
// it, and the partial count is what really landed on disk.
func cmdDd(s *Shell, args []string) int {
	in, out, ifile := "/dev/zero", "", ""
	bs, count := 512, -1
	statusNone := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "if="):
			ifile = a[3:]
		case strings.HasPrefix(a, "of="):
			out = a[3:]
		case strings.HasPrefix(a, "bs="):
			if n, err := parseSizeBytes(a[3:]); err == nil {
				bs = n
			} else {
				s.errf("dd: invalid number '%s'", a[3:])
				return 1
			}
		case strings.HasPrefix(a, "count="):
			n, err := strconv.Atoi(a[6:])
			if err != nil {
				s.errf("dd: invalid number '%s'", a[6:])
				return 1
			}
			count = n
		case a == "status=none":
			statusNone = true
		case strings.HasPrefix(a, "if") || strings.HasPrefix(a, "of"):
			s.errf("dd: unrecognized operand '%s'", a)
			return 1
		}
	}
	if out == "" {
		s.errf("dd: missing of=FILE")
		return 1
	}
	if bs < 1 {
		bs = 1
	}
	dst := s.abs(out)
	var payload []byte
	if ifile == "/dev/zero" || ifile == "" {
		if count < 0 {
			// a real dd with if=/dev/zero and no count never returns; this
			// world has no Ctrl-C to reach for, so it asks instead
			s.errf("dd: if=/dev/zero needs count=N (this world never returns from an endless write)")
			return 1
		}
	} else {
		src := s.abs(ifile)
		vfs, rp, err := s.ResolveVFS(src)
		if vfs == nil {
			s.errf("dd: failed to open '%s': %s", ifile, err)
			return 1
		}
		data, ok := vfs.Read(rp)
		if !ok {
			s.errf("dd: failed to open '%s': No such file or directory", ifile)
			return 1
		}
		s.Dev.NoteDiskRead(len(data))
		if count < 0 {
			count = 1
			bs = len(data)
			if bs == 0 {
				bs = 1
			}
			payload = data
		} else {
			for i := 0; i < count; i++ {
				payload = append(payload, data...)
			}
		}
		in = ifile
	}
	if payload == nil {
		total := bs * count
		if total > maxFileMB*1048576 {
			s.errf("dd: '%s': File too large (this world's files hold at most %d MiB; the disk reports %d MiB free because a disk fills with many files)",
				out, maxFileMB, s.Dev.DiskFreeMB())
			return 1
		}
		payload = make([]byte, total) // if=/dev/zero: real zero bytes
	}
	_ = in

	// what the disk can take: a real dd writes until the write fails, and what
	// landed on disk is what the counters and the error report
	free := s.Dev.DiskFreeMB() * 1048576
	written := len(payload)
	refused := false
	if free < written {
		written = free - (free % bs)
		if written < 0 {
			written = 0
		}
		refused = true
	}
	var werr error
	if written > 0 {
		werr = s.Dev.WriteGuest(dst, payload[:written], s.User)
		if werr != nil {
			refused = true
			written = 0
		}
	} else if s.Dev.DiskFull() {
		refused = true
	}
	records := written / bs
	secs := 0.0
	if rate := s.Dev.DiskMBps() * s.Dev.CPUShare(); rate > 0 {
		secs = float64(written) / 1048576 / rate
	}
	wantRecords := len(payload) / bs
	partial := 0
	if written%bs > 0 {
		partial = 1
	}
	if !statusNone {
		fmt.Fprintf(s.Out, "%d+%d records in\n", wantRecords, 0)
		fmt.Fprintf(s.Out, "%d+%d records out\n", records, partial)
		fmt.Fprintf(s.Out, "%d bytes (%s, %s) copied, %s, %s/s\n",
			written, humanBytes(written), humanBytesBinary(written),
			humanSecs(secs), humanBytes(int(s.Dev.DiskMBps()*1048576)))
	}
	if refused {
		s.errf("dd: error writing '%s': No space left on device (%d MiB free, %d MiB asked for)",
			out, s.Dev.DiskFreeMB(), len(payload)/1048576)
		return 1
	}
	return 0
}

func parseSizeBytes(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("empty")
	}
	mult := 1
	switch v[len(v)-1] {
	case 'c':
		mult, v = 1, v[:len(v)-1]
	case 'k', 'K':
		mult, v = 1024, v[:len(v)-1]
	case 'm', 'M':
		mult, v = 1048576, v[:len(v)-1]
	case 'g', 'G':
		mult, v = 1073741824, v[:len(v)-1]
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}

func humanBytes(n int) string {
	switch {
	case n >= 1073741824:
		return fmt.Sprintf("%.1f GB", float64(n)/1073741824)
	case n >= 1048576:
		return fmt.Sprintf("%.1f MB", float64(n)/1048576)
	case n >= 1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	}
	return fmt.Sprintf("%d bytes", n)
}

func humanBytesBinary(n int) string {
	if n >= 1048576 {
		return fmt.Sprintf("%d MiB", n/1048576)
	}
	if n >= 1024 {
		return fmt.Sprintf("%d KiB", n/1024)
	}
	return fmt.Sprintf("%d B", n)
}

func humanSecs(sec float64) string {
	if sec < 1 {
		return fmt.Sprintf("%.2f s", sec)
	}
	return fmt.Sprintf("%.2f s", sec)
}

// ---- vmstat ----------------------------------------------------------------

// vmstat reports one sample of the machine as it is now. A real vmstat waits a
// second between samples; this world's clock only moves when the engine ticks,
// so the sample is labelled for what it is instead of sleeping for it.
func cmdVmstat(s *Shell, args []string) int {
	if len(args) > 0 {
		if _, err := strconv.Atoi(args[0]); err == nil {
			// `vmstat 1 5` style: sample counts are meaningless without a
			// clock to wait on; say so rather than printing the same row twice
			fmt.Fprintf(s.Out, "vmstat: the world clock moves on engine ticks (one line = the last tick)\n")
		}
	}
	d := s.Dev
	r := d.Resources()
	running, blocked := d.LoadProcs()
	for _, b := range d.Procs {
		if b.State == "D" {
			blocked++
		}
	}
	used := d.MemUsed()
	free := d.HW.RAMMB - used
	if free < 0 {
		free = 0
	}
	us := 0
	if cap := d.CPUCapacity(); cap > 0 {
		us = int(d.CPUDemand() / cap * 100)
	}
	if us > 100 {
		us = 100
	}
	sy := 3
	id := 100 - us - sy
	if id < 0 {
		id = 0
	}
	running -= blocked
	if running < 0 {
		running = 0
	}
	si := 0
	if r.SwapUsedMB > 0 && len(d.Procs) > 0 && used > d.HW.RAMMB {
		si = 1
	}
	fmt.Fprintf(s.Out, "procs -----------memory---------- ---swap-- -----io---- --system-- -----cpu------\n")
	fmt.Fprintf(s.Out, " r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st\n")
	fmt.Fprintf(s.Out, "%2d %2d %6d %6d %6d %6d %4d %4d %5d %5d %4d %4d %2d %2d %2d %2d %2d\n",
		running, blocked, r.SwapUsedMB, free, 0, 0, si, si,
		int(r.DiskReadMBps), int(r.DiskWriteMBps), 120, 240, us, sy, id, 0, 0)
	fmt.Fprintf(s.Out, "\nthis tick: disk %s read, %s written; net %s in, %s out; load %.2f %.2f %.2f\n",
		humanBytes(int(r.DiskReadMBps*1048576)), humanBytes(int(r.DiskWriteMBps*1048576)),
		humanBytes(int(r.NetRxMBps*1048576)), humanBytes(int(r.NetTxMBps*1048576)),
		r.Load1, r.Load5, r.Load15)
	if used > d.HW.RAMMB {
		fmt.Fprintf(s.Out, "warning: %d MiB over RAM — swapping (%d/%d MiB)\n", used-d.HW.RAMMB, r.SwapUsedMB, d.SwapTotalMB())
	}
	if r.OOMCount > 0 {
		fmt.Fprintf(s.Out, "OOM incidents: %d — last: %s\n", r.OOMCount, r.LastOOM)
	}
	if d.DiskFull() {
		fmt.Fprintf(s.Out, "warning: filesystem full (%d/%d MiB) — writes are failing\n", d.FS.DiskUsedMB(), d.DiskLimitMB())
	}
	if r.ForksRefused > 0 {
		fmt.Fprintf(s.Out, "refused forks: %d (process limit %d)\n", r.ForksRefused, d.ProcLimit())
	}
	return 0
}
