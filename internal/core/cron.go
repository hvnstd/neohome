package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The world's scheduler: real timed execution of real game commands against
// real devices. Nothing here may sleep a real wall-clock interval to gate
// gameplay — schedules fire because World.Sim crossed the scheduled minute,
// and World.Sim only moves in World.Tick(). There is no goroutine, no timer
// and no real OS process anywhere in this file.
//
// OWNERSHIP: this file belongs to the "scheduler + VM" workstream. Only
// *World.Cron is referenced elsewhere, so these types may grow freely.

// maxCronCatchup bounds how many missed runs one entry may execute in a single
// tick after the world clock jumped (a loaded save, a long freeze). Real cron
// does not stampede either: it logs the skip and carries on.
const maxCronCatchup = 16

// CronExecFn runs one game command line for a (device, user) pair exactly the
// way a player's shell does. core cannot import the shell package (the shell
// imports core), so the shell package registers the executor at init time:
//
//	core.SetCronExec(func(w *core.World, d *core.Device, u *core.User, line string) (int, string))
//
// If nothing registered an executor, a due entry still resolves, still logs,
// and honestly reports that no command runtime is linked.
var CronExecFn func(w *World, d *Device, u *User, line string) (int, string)

// SetCronExec installs the command executor used by scheduled jobs. The shell
// package calls it from its own init — see internal/shell/cron_cmds.go.
func SetCronExec(fn func(w *World, d *Device, u *User, line string) (int, string)) {
	CronExecFn = fn
}

// CronState is the world's scheduler: the crontabs loaded from every device's
// spool files, plus the per-job runtime bookkeeping.
type CronState struct {
	Entries []*CronEntry
	// seen records, per device, whether its cron daemon was observed running
	// on the previous tick. The 0→1 edge is what "the daemon just came up"
	// means, and it is how a boot or a `service crond start` re-arms the spool.
	// Unexported, so a save/load treats every daemon as newly booted — which is
	// exactly right: a reloaded world is a machine that just came up.
	seen map[string]bool
}

// CronEntry is one scheduled command on one device, as loaded from a real
// crontab spool file in the device's filesystem.
type CronEntry struct {
	DeviceID string
	User     string
	Spec     string    // 5-field cron spec, exactly as written in the spool file
	Command  string    // game-DSL command line
	Next     time.Time // next fire time on the world clock (zero = not armed)
	Last     time.Time
	Runs     int
	LastRC   int

	spec *CronSpec // parsed form; unexported so gob skips it (reparsed on load)
}

// Schedule returns the parsed 5-field spec, or nil when the entry is malformed.
func (e *CronEntry) Schedule() *CronSpec {
	if e.spec == nil {
		s, err := ParseCronSpec(e.Spec)
		if err != nil {
			return nil
		}
		e.spec = s
	}
	return e.spec
}

// key identifies an entry across spool reloads, so a reload preserves the
// runtime state (Next/Last/Runs) of every job that did not actually change.
func (e *CronEntry) key() string {
	return e.DeviceID + "|" + e.User + "|" + e.Spec + "|" + e.Command
}

// ---- the 5-field spec ----

// CronSpec is a parsed "min hour dom mon dow" schedule. It supports '*',
// ranges (a-b), steps (*/n and a-b/n), lists (a,b,c), three-letter month and
// weekday names, and day-of-week 7 as an alias for Sunday.
//
// Day-of-month / day-of-week follow Vixie cron: if BOTH are restricted the
// entry fires when EITHER matches; if one of them is '*' both must match.
type CronSpec struct {
	Min  [60]bool
	Hour [24]bool
	Dom  [32]bool
	Mon  [13]bool
	Dow  [7]bool

	domStar bool
	dowStar bool
	raw     string
}

// String returns the spec as written.
func (s *CronSpec) String() string { return s.raw }

var cronMonths = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var cronDays = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// ParseCronSpec parses a 5-field cron spec. It rejects everything it does not
// really implement instead of silently accepting a schedule it would not keep.
func ParseCronSpec(spec string) (*CronSpec, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("bad schedule %q: expected 5 fields (min hour dom mon dow), got %d", spec, len(fields))
	}
	s := &CronSpec{raw: strings.Join(fields, " ")}

	if err := fillField(fields[0], 0, 59, nil, s.Min[:]); err != nil {
		return nil, fmt.Errorf("bad minute field %q: %v", fields[0], err)
	}
	if err := fillField(fields[1], 0, 23, nil, s.Hour[:]); err != nil {
		return nil, fmt.Errorf("bad hour field %q: %v", fields[1], err)
	}
	if err := fillField(fields[2], 1, 31, nil, s.Dom[:]); err != nil {
		return nil, fmt.Errorf("bad day-of-month field %q: %v", fields[2], err)
	}
	if err := fillField(fields[3], 1, 12, cronMonths, s.Mon[:]); err != nil {
		return nil, fmt.Errorf("bad month field %q: %v", fields[3], err)
	}
	// day-of-week is parsed into 0..7 first because 7 is an accepted alias for
	// Sunday, then normalised down to 0..6.
	var dow [8]bool
	if err := fillField(fields[4], 0, 7, cronDays, dow[:]); err != nil {
		return nil, fmt.Errorf("bad day-of-week field %q: %v", fields[4], err)
	}
	for i := 0; i < 7; i++ {
		s.Dow[i] = dow[i]
	}
	if dow[7] {
		s.Dow[0] = true
	}
	s.domStar = isStarField(fields[2])
	s.dowStar = isStarField(fields[4])
	return s, nil
}

func isStarField(f string) bool {
	return f == "*" || strings.HasPrefix(f, "*/")
}

func fillField(field string, min, max int, names map[string]int, out []bool) error {
	if field == "" {
		return fmt.Errorf("empty field")
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("empty list element")
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step %q", part[i+1:])
			}
			step = n
			part = part[:i]
			if part == "" {
				return fmt.Errorf("missing range before step")
			}
		}
		lo, hi := min, max
		if part != "*" {
			if i := strings.Index(part, "-"); i > 0 {
				a, err := cronValue(part[:i], names)
				if err != nil {
					return err
				}
				b, err := cronValue(part[i+1:], names)
				if err != nil {
					return err
				}
				lo, hi = a, b
			} else {
				a, err := cronValue(part, names)
				if err != nil {
					return err
				}
				lo = a
				if step > 1 {
					hi = max // "5/15" means "from 5 to the end, every 15"
				} else {
					hi = a
				}
			}
		}
		if lo < min || hi > max || lo > hi {
			return fmt.Errorf("value out of range %d-%d (allowed %d-%d)", lo, hi, min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return nil
}

func cronValue(tok string, names map[string]int) (int, error) {
	tok = strings.TrimSpace(tok)
	if names != nil {
		if v, ok := names[strings.ToLower(tok)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", tok)
	}
	return v, nil
}

// Match reports whether the spec fires at the given instant (minute
// resolution — seconds are ignored, as in cron).
func (s *CronSpec) Match(t time.Time) bool {
	if s == nil {
		return false
	}
	if !s.Mon[int(t.Month())] || !s.dayMatches(t) {
		return false
	}
	return s.Hour[t.Hour()] && s.Min[t.Minute()]
}

// Next returns the first instant strictly after `after` that this spec fires
// on. It walks whole months/days/hours first, so even "0 0 29 2 *" resolves in
// a handful of steps instead of minute-by-minute scanning.
func (s *CronSpec) Next(after time.Time) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	loc := after.Location()
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(4, 0, 0) // 4 years covers Feb 29 in any calendar
	for t.Before(limit) {
		if !s.Mon[int(t.Month())] {
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if !s.Hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(time.Hour)
			continue
		}
		if !s.Min[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

func (s *CronSpec) dayMatches(t time.Time) bool {
	dom := s.Dom[t.Day()]
	dow := s.Dow[int(t.Weekday())]
	if s.domStar && s.dowStar {
		return true
	}
	if s.domStar {
		return dow
	}
	if s.dowStar {
		return dom
	}
	return dom || dow
}

// ---- crontab parsing ----

// CronLineError is one rejected line of a crontab file.
type CronLineError struct {
	Line int
	Text string
	Err  string
}

func (e CronLineError) Error() string {
	return fmt.Sprintf("line %d: %s: %s", e.Line, e.Text, e.Err)
}

// ParseCrontab parses spool-file text into entries for one device/user.
// It understands comments, blank lines and the Vixie environment assignments
// (SHELL/PATH/MAILTO/CRON_TZ, which the game runtime ignores on purpose).
func ParseCrontab(text string) ([]*CronEntry, []CronLineError) {
	var out []*CronEntry
	var errs []CronLineError
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if k := strings.Index(trimmed, "="); k > 0 && !strings.ContainsAny(trimmed[:k], " \t") {
			key := strings.ToUpper(strings.TrimSpace(trimmed[:k]))
			switch key {
			case "SHELL", "PATH", "MAILTO", "CRON_TZ":
				continue // recognised, deliberately not honoured: the game runtime
				// always runs entries as a real device user
			}
		}
		spec, cmd := splitCrontabLine(trimmed)
		if spec == "" {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: "expected 5 schedule fields"})
			continue
		}
		if cmd == "" {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: "entry has no command"})
			continue
		}
		if strings.HasPrefix(spec, "-") {
			// Vixie's overlap guard: "-" means "do not start this job while the
			// previous one is still running". A job here runs to completion
			// inside the tick that fires it, so there is never a previous run
			// to overlap with and the guard is a no-op. The entry is therefore
			// armed exactly like any other — refusing to arm it would silently
			// disagree with what a real crontab would do tonight.
			spec = strings.TrimSpace(spec[1:])
		}
		ps, err := ParseCronSpec(spec)
		if err != nil {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: err.Error()})
			continue
		}
		out = append(out, &CronEntry{Spec: ps.raw, Command: cmd, spec: ps})
	}
	return out, errs
}

// CronDotDir is where system crontabs live: one file per job set, each line
// naming the user the job runs as. This is the directory packages ship their
// schedules into — a nightly backup belongs here, not in a person's spool.
const CronDotDir = "/etc/cron.d"

// ParseSystemCrontab parses a file from /etc/cron.d: the same 5 schedule
// fields, then the user, then the command. It also honours the two file-level
// conventions system crontabs have: environment assignments and comments.
func ParseSystemCrontab(text string) ([]*CronEntry, []CronLineError) {
	var out []*CronEntry
	var errs []CronLineError
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if k := strings.Index(trimmed, "="); k > 0 && !strings.ContainsAny(trimmed[:k], " \t") {
			switch strings.ToUpper(strings.TrimSpace(trimmed[:k])) {
			case "SHELL", "PATH", "MAILTO", "CRON_TZ":
				continue
			}
		}
		spec, rest := splitCrontabLine(trimmed)
		if spec == "" {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: "expected 5 schedule fields"})
			continue
		}
		user, cmd, found := strings.Cut(strings.TrimSpace(rest), " ")
		if !found || strings.TrimSpace(cmd) == "" {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: "expected a user and a command"})
			continue
		}
		ps, err := ParseCronSpec(spec)
		if err != nil {
			errs = append(errs, CronLineError{Line: i + 1, Text: trimmed, Err: err.Error()})
			continue
		}
		out = append(out, &CronEntry{Spec: ps.raw, Command: strings.TrimSpace(cmd), User: user, spec: ps})
	}
	return out, errs
}

// CronDotFiles lists the system crontabs on a device, in the order cron reads
// them. A name containing a dot is skipped, exactly as Vixie cron skips it —
// which is why an editor's backup file is never a schedule.
func (d *Device) CronDotFiles() []string {
	var out []string
	for _, p := range d.FS.List(CronDotDir) {
		n, ok := d.FS.Get(p)
		if !ok || n.IsDir {
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if strings.Contains(name, ".") {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// splitCrontabLine separates the 5 schedule fields from the command, keeping
// the command text byte-for-byte (quoted arguments included). A leading '-' on
// the first field is the Vixie overlap guard and stays with the spec.
func splitCrontabLine(line string) (spec, cmd string) {
	i, fields := 0, 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '	') {
			i++
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '	' {
			i++
		}
		if i == start {
			break
		}
		fields++
		if fields == 5 {
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i:])
		}
	}
	return "", ""
}

// ---- spool files on real devices ----

// CronSpoolDir is where every device keeps its users' crontabs.
const CronSpoolDir = "/var/spool/cron/crontabs"

// CronLogPath is the per-device log of scheduled job output.
const CronLogPath = "/var/log/cron.log"

// CrontabPath is the spool file of one user on any device.
func CrontabPath(user string) string { return CronSpoolDir + "/" + user }

// CronUsers lists the users that have a spool file on this device, sorted.
func (d *Device) CronUsers() []string {
	var out []string
	for _, p := range d.FS.List(CronSpoolDir) {
		if n, ok := d.FS.Get(p); ok && !n.IsDir {
			out = append(out, p[len(CronSpoolDir)+1:])
		}
	}
	return out
}

// ReadCrontab returns the raw spool text for a user.
func (d *Device) ReadCrontab(user string) (string, bool) {
	data, ok := d.FS.Read(CrontabPath(user))
	if !ok {
		return "", false
	}
	return string(data), true
}

// WriteCrontab installs spool text for a user, creating the spool dir. It
// validates nothing: callers use ValidateCrontab first so a bad line is
// reported with its line number instead of being dropped at fire time.
func (d *Device) WriteCrontab(user, text string) {
	d.FS.MkdirAll(CronSpoolDir, 0700, "root", "root")
	d.FS.Write(CrontabPath(user), text, 0600, user, "root")
}

// RemoveCrontab deletes a user's spool file; reports whether one existed.
func (d *Device) RemoveCrontab(user string) bool { return d.FS.Remove(CrontabPath(user)) }

// ValidateCrontab parses spool text and returns every line it refuses, so the
// player sees "line 3: 99 * * * * ...: bad minute field" instead of a job that
// mysteriously never runs.
func ValidateCrontab(text string) []CronLineError {
	_, errs := ParseCrontab(text)
	return errs
}

// ---- the tick ----

// CronTick fires whatever is due on the world clock. It runs once per
// World.Tick(), i.e. once per 30 simulated seconds, and it fires a job only
// while that device's cron daemon is actually running.
func (w *World) CronTick() {
	if w.Cron == nil {
		w.Cron = &CronState{}
	}
	if w.Cron.seen == nil {
		w.Cron.seen = map[string]bool{}
	}
	// The spool files on disk are the source of truth: re-read them so a
	// player who just installed a crontab gets it armed on this very tick.
	w.ReloadCrontabs()

	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		running := d.CronDaemonRunning()
		if running && !w.Cron.seen[id] {
			// the daemon has just come up (a fresh boot, or the player started
			// it through service/systemctl/crond): it loads its spool and arms
			// every job from now, so a stop of an hour is not a backlog to
			// stampede.
			w.CronDaemonStarted(d)
		}
		w.Cron.seen[id] = running
	}

	now := w.Sim
	for _, e := range w.Cron.Entries {
		d := w.Devices[e.DeviceID]
		if d == nil || !d.CronDaemonRunning() {
			continue // no daemon, no schedule — exactly like a stopped crond
		}
		// A dark machine runs no jobs: power cut, dead phone battery,
		// suspended lid. Skipped runs are not backlogged — on boot the daemon
		// re-arms from now (see the seen-map above), the way a real crond
		// does not stampede after an outage.
		if !d.Powered() {
			continue
		}
		sched := e.Schedule()
		if sched == nil {
			e.Last = now
			e.LastRC = 1
			continue
		}
		if e.Next.IsZero() {
			if t, ok := sched.Next(now); ok {
				e.Next = t
			}
			continue
		}
		// fire every scheduled minute the world clock has passed. A real
		// crond does not stampede a backlog after a long freeze, so the
		// catch-up is bounded and the skip is logged, not hidden.
		for fired := 0; !e.Next.After(now); fired++ {
			w.runCronEntry(e)
			next, ok := sched.Next(e.Next)
			if !ok {
				e.Next = time.Time{}
				break
			}
			e.Next = next
			if fired >= maxCronCatchup {
				d.Logf("warn", "crond", "job %q: more than %d runs were due (world clock jumped); skipping ahead",
					e.Command, maxCronCatchup)
				if t, ok := sched.Next(now); ok {
					e.Next = t
				} else {
					e.Next = time.Time{}
				}
				break
			}
		}
	}
}

// ReloadCrontabs re-reads every device's spool files and reconciles them with
// the loaded entries: unchanged jobs keep their runtime state (next fire, run
// count, last exit code), new jobs are armed from the current world clock and
// deleted jobs disappear.
func (w *World) ReloadCrontabs() {
	if w.Cron == nil {
		w.Cron = &CronState{}
	}
	old := map[string]*CronEntry{}
	for _, e := range w.Cron.Entries {
		old[e.key()] = e
	}
	var fresh []*CronEntry
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		arm := func(entries []*CronEntry, owner string, errs []CronLineError, where string) {
			for _, e := range errs {
				d.Logf("err", "crond", "%s: %v", where, e)
			}
			for _, e := range entries {
				e.DeviceID = d.ID
				if owner != "" {
					e.User = owner
				}
				if prev, found := old[e.key()]; found {
					// unchanged job: keep its runtime state (next fire,
					// run count, last exit code) and adopt the re-parsed spec
					prev.spec = e.spec
					fresh = append(fresh, prev)
					continue
				}
				// brand new job: arm it from the current world clock so it is
				// live the moment the crontab is installed
				if sp := e.Schedule(); sp != nil {
					if t, ok := sp.Next(w.Sim); ok {
						e.Next = t
					}
				}
				fresh = append(fresh, e)
			}
		}
		for _, user := range d.CronUsers() {
			text, ok := d.ReadCrontab(user)
			if !ok {
				continue
			}
			entries, errs := ParseCrontab(text)
			arm(entries, user, errs, "crontab for "+user)
		}
		// §33: a package's own schedule (the nightly backup, a mirror sync)
		// lands in /etc/cron.d, which is where a package may write — and cron
		// reads it as a system crontab, with the user named on each line.
		for _, path := range d.CronDotFiles() {
			data, ok := d.FS.Read(path)
			if !ok {
				continue
			}
			entries, errs := ParseSystemCrontab(string(data))
			arm(entries, "", errs, path)
		}
	}
	w.Cron.Entries = fresh
}

// runCronEntry executes one job and records what actually happened: the
// device's log gets the run line and, on failure, the reason — so a broken
// scheduled job is diagnosable with logread / journalctl.
func (w *World) runCronEntry(e *CronEntry) int {
	d := w.Devices[e.DeviceID]
	if d == nil {
		return 1
	}
	now := w.Sim
	e.Last = now

	u := d.FindUser(e.User)
	if u == nil {
		e.LastRC = 1
		d.Logf("err", "crond", "job for unknown user %q: not run", e.User)
		return 1
	}

	d.Logf("info", "cron", "CMD (%s) CMD: %s", e.User, e.Command)
	rc := 0
	out := ""
	switch {
	case CronExecFn == nil:
		rc, out = 127, "crond: no game command runtime is linked into this build\n"
	case u.Shell == "/usr/sbin/nologin":
		rc, out = 1, "crond: this account has no login shell; job not run\n"
	default:
		rc, out = CronExecFn(w, d, u, e.Command)
	}

	e.Runs++
	e.LastRC = rc
	cronWriteJobLog(d, e, rc, out)

	if rc != 0 {
		d.Logf("err", "cron", "CMD (%s) CMD (%s) FAILED, exit %d: %s", e.User, e.Command, rc, firstLine(out))
		if e.Runs == 1 {
			w.AddEvent(d.ID, "warn", "cron", "scheduled job %q on %s failed: %s", e.Command, d.Hostname, firstLine(out))
		}
	} else {
		// the first line of a job's output is always logged: a command that
		// "succeeds" while printing an error must not be invisible
		d.Logf("info", "cron", "CMD (%s) CMD (%s) completed, exit 0: %s", e.User, e.Command, firstLine(out))
		if e.Runs == 1 {
			w.AddEvent(d.ID, "info", "cron", "scheduled job %q ran on %s as %s", e.Command, d.Hostname, e.User)
		}
	}
	return rc
}

// cronWriteJobLog appends the run's output to /var/log/cron.log, the same
// shape real cron produces when it mails a job's output somewhere.
func cronWriteJobLog(d *Device, e *CronEntry, rc int, out string) {
	if !d.FS.Exists(CronLogPath) {
		d.FS.Write(CronLogPath, "", 0640, "root", "adm")
	}
	head := fmt.Sprintf("%s %s CRON[%d]: (%s) CMD (%s)\n", d.W.Sim.Format("Jan 2 15:04:05"),
		d.Hostname, d.W.NewPID(), e.User, e.Command)
	body := out
	if !strings.HasSuffix(body, "\n") && body != "" {
		body += "\n"
	}
	if rc != 0 {
		head += fmt.Sprintf("%s %s CRON: job exited with status %d\n", d.W.Sim.Format("Jan 2 15:04:05"), d.Hostname, rc)
	}
	_ = d.FS.Append(CronLogPath, []byte(head+body))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		return "no output"
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ---- the world's initial scheduled work ----

// seedCron gives every device a real cron daemon unit, starts it where a real
// box of that type would have it running, and plants the household's scheduled
// work in the spool files. Every planted job does something a player can later
// observe in the world.
func seedCron(w *World) {
	if w.Cron == nil {
		w.Cron = &CronState{seen: map[string]bool{}}
	}
	if w.Cron.seen == nil {
		w.Cron.seen = map[string]bool{}
	}

	// A BusyBox image ships crond; a Debian-family image ships cron. Register
	// the unit the image really has, and leave it stopped on hosts where the
	// owner has not enabled it yet.
	running := map[string]bool{
		"router-alex": true, // the CPE always keeps time
		"npc-router":  true,
		"asst-alex":   true,
		"nas-alex":    true,
		"pc-alex":     true,
	}
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil {
			continue
		}
		d.FS.MkdirAll(CronSpoolDir, 0700, "root", "root")
		d.EnsureCronDaemon()
	}

	// 1. the PC keeps an hourly reachability probe. Its resolver is the home
	//    router, so with the planted dnsmasq fault active this job FAILS — and
	//    the reason lands in the PC's log. The scheduler is how a player first
	//    discovers the fault; repairing the router makes the next run succeed.
	w.plantCronJob("pc-alex", "alex", "# hourly check that the LAN can still reach the mirror by name\n"+
		"0 * * * * curl -s -o/home/alex/upstream-probe.log http://mirror.neohome.example/debian/Release\n")

	// 2. the assistant node keeps a heartbeat the player can read with
	//    `assist tasks` — a real file that really grows.
	w.plantCronJob("asst-alex", "assistant", "# assistant heartbeat\n"+
		"*/30 * * * * echo assistant-heartbeat >> /home/assistant/jobs.log\n")

	// 3. nightly mirror sync onto the NAS share: a real file at
	//    /srv/data/nightly-mirror.log, and a real failure — "the backups
	//    stopped" — while DNS is broken.
	w.plantCronJob("nas-alex", "root", "# nightly mirror sync onto the share\n"+
		"0 3 * * * curl -s -o/srv/data/nightly-mirror.log http://mirror.neohome.example/debian/Release\n")

	w.ReloadCrontabs()

	// Finally bring up the daemons that a real box of this type has running at
	// boot. Each one loads the spool it just found and arms its jobs.
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || !running[id] {
			continue
		}
		d.StartCronDaemon()
		w.CronDaemonStarted(d)
	}
}

// plantCronJob writes a spool file, exactly as `crontab FILE` would, and arms
// the jobs so they are live from the first tick.
func (w *World) plantCronJob(devID, user, text string) {
	d := w.Devices[devID]
	if d == nil || d.FindUser(user) == nil {
		return
	}
	entries, _ := ParseCrontab(text)
	d.WriteCrontab(user, text)
	d.Logf("info", "cron", "installed crontab for %s (%d jobs)", user, len(entries))
	w.ReloadCrontabs()
}
