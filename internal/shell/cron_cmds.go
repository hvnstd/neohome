package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"crontab", cmdCrontab}, {"crond", cmdCrond},
	} {
		builtinTable[e.name] = e.fn
	}

	// The scheduler lives in core (it fires from World.Tick) and core cannot
	// import this package without a cycle, so the shell hands core the one
	// thing only it has: the ability to run a command line. A scheduled job
	// therefore goes through the SAME exec machinery a player's shell uses —
	// same builtins, same VFS permission checks, same redirects, same exit
	// codes. Nothing here spawns a host process.
	core.SetCronExec(execCronLine)
}

// ---- the executor core calls ----

// execCronLine runs one scheduled command line as a real device user and
// returns its exit code plus captured output. It mirrors Shell.ExecLine's
// grammar (&& and ;) but keeps the numeric status, which a cron job needs.
func execCronLine(w *core.World, d *core.Device, u *core.User, line string) (int, string) {
	var out strings.Builder
	sh := NewShell(w, d, u, &out, "", "cron")
	// a job is not a terminal: anything that would prompt must fail fast
	// instead of blocking the world's single thread.
	sh.SetInput(strings.NewReader(""))
	sh.Env["CRON"] = "1"

	rc := 0
	for _, seg := range strings.Split(line, "&&") {
		failed := false
		for _, sub := range strings.Split(seg, ";") {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}
			before := out.Len()
			if cronExecOne(sh, sub) {
				rc = 0
				continue
			}
			// only this segment's own output decides between "failed" and
			// "no such command"
			rc = 1
			if strings.Contains(out.String()[before:], "command not found") {
				rc = 127
			}
			failed = true
			break
		}
		if failed {
			break
		}
	}
	return rc, out.String()
}

// cronExecOne runs one command segment, distinguishing "no such command" from
// "the command failed" the way /bin/sh does.
func cronExecOne(sh *Shell, cmd string) bool {
	toks := tokenize(cmd)
	if len(toks) == 0 {
		return true
	}
	if _, ok := builtinTable[toks[0]]; !ok && !sh.commandExists(toks[0]) {
		fmt.Fprintf(sh.Out, "%s: %s: command not found\r\n", sh.Dev.Hostname, toks[0])
		return false
	}
	return sh.execOne(cmd)
}

// ---- crontab ----

// cmdCrontab installs, lists and removes a user's crontab. The crontab is a
// real file in the device's VFS and the scheduler reads that file, so what the
// player edits is what the daemon will run.
func cmdCrontab(s *Shell, args []string) int {
	list, remove, edit := false, false, false
	user, file := "", ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-l" || a == "--list":
			list = true
		case a == "-r" || a == "--remove":
			remove = true
		case a == "-e" || a == "--edit":
			edit = true
		case a == "-u" || a == "--user":
			if i+1 >= len(args) {
				s.errf("crontab: option -u requires a user name")
				return 1
			}
			i++
			user = args[i]
		case a == "-":
			// `crontab -` reads stdin; there is no interactive stdin in a job
		case strings.HasPrefix(a, "-"):
			s.errf("crontab: unrecognized option: %s", a)
			return 1
		default:
			if file != "" {
				s.errf("crontab: too many files given")
				return 1
			}
			file = a
		}
	}
	if user == "" {
		user = s.User.Name
	}
	if user != s.User.Name && s.User.UID != 0 {
		s.errf("crontab: you (%s) are not allowed to edit %s's crontab", s.User.Name, user)
		return 1
	}
	if s.Dev.FindUser(user) == nil {
		s.errf("crontab: no such user: %s", user)
		return 1
	}
	if s.Dev.CronDaemon() == nil {
		// Installing a crontab where no scheduler exists would create a file
		// that can never run: refuse instead of pretending.
		s.errf("crontab: %s ships no cron daemon — nothing could run these jobs", s.Dev.OS.Distro)
		return 1
	}
	if !s.Dev.CronDaemonRunning() {
		// installing a crontab on a box whose scheduler is dead is a real
		// mistake; say it instead of silently accepting a job that never runs.
		fmt.Fprintf(s.Out, "note: %s\n", s.Dev.CronStatus())
	}

	spool := core.CrontabPath(user)
	switch {
	case list:
		text, ok := s.Dev.ReadCrontab(user)
		if !ok {
			s.errf("no crontab for %s", user)
			return 1
		}
		fmt.Fprint(s.Out, text)
		return 0
	case remove:
		if !s.Dev.RemoveCrontab(user) {
			s.errf("no crontab for %s", user)
			return 1
		}
		s.W.LogCrontabChange(s.Dev, "crontab removed for "+user)
		fmt.Fprintf(s.Out, "crontab: removed %s's crontab\n", user)
		return 0
	case edit:
		// There is no editor inside a cron-capable game shell, and pretending
		// otherwise would be a lie. Hand over the real file and the real flow.
		text, ok := s.Dev.ReadCrontab(user)
		if !ok {
			text = "# %s's crontab — 5 fields: min hour dom mon dow  command\n" + user
		}
		fmt.Fprintf(s.Out, "crontab: there is no interactive editor in this world.\n")
		fmt.Fprintf(s.Out, "the crontab is a real file — write it, then install it:\n")
		fmt.Fprintf(s.Out, "  echo '* * * * * uptime >> /tmp/uptime.log' > %s\n", spool)
		fmt.Fprintf(s.Out, "  crontab %s\n", spool)
		fmt.Fprintf(s.Out, "---- current %s ----\n%s", spool, text)
		return 0
	case file != "":
		p := s.abs(file)
		vfs, rp, err := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("crontab: %s: %v", file, err)
			return 1
		}
		data, ok := vfs.Read(rp)
		if !ok {
			s.errf("crontab: %s: No such file or directory", file)
			return 1
		}
		text := string(data)
		if errs := core.ValidateCrontab(text); len(errs) > 0 {
			// refuse the whole file, exactly like crontab(8) does
			for _, e := range errs {
				fmt.Fprintf(s.Out, "crontab: %s\n", e.Error())
			}
			s.errf("installing new crontab: bad entries, nothing changed")
			return 1
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		s.Dev.WriteCrontab(user, text)
		entries, _ := core.ParseCrontab(text)
		s.W.LogCrontabChange(s.Dev, fmt.Sprintf("crontab installed for %s (%d jobs)", user, len(entries)))
		fmt.Fprintf(s.Out, "crontab: installing new crontab for %s (%d jobs)\n", user, len(entries))
		return 0
	}
	s.errf("usage: crontab [-u user] {-l | -r | -e | FILE}")
	return 1
}

// ---- crond ----

// cmdCrond is the BusyBox applet. In this world the daemon is a service unit,
// so `crond` does exactly what `service crond start` does and then reports
// what the scheduler is now responsible for.
func cmdCrond(s *Shell, args []string) int {
	for _, a := range args {
		if a == "-f" || a == "-l" || a == "-L" || a == "-c" || a == "-b" {
			continue // accepted and ignored: the daemon is not a foreground job here
		}
		if strings.HasPrefix(a, "-d") {
			fmt.Fprintf(s.Out, "crond: debug output is not part of this world; the job log is %s\n", core.CronLogPath)
			continue
		}
		s.errf("crond: unrecognized option: %s", a)
		return 1
	}
	if s.Dev.CronDaemon() == nil {
		s.errf("crond: %s image ships no cron daemon", s.Dev.OS.Distro)
		return 1
	}
	if s.Dev.CronDaemonRunning() {
		fmt.Fprintf(s.Out, "crond: already running — %s\n", s.Dev.CronStatus())
		return 0
	}
	if _, err := s.Dev.StartCronDaemon(); err != nil {
		s.errf("crond: %v", err)
		return 1
	}
	// load the spool and arm it, exactly as a daemon start does
	s.W.ReloadCrontabs()
	s.W.CronDaemonStarted(s.Dev)
	fmt.Fprintf(s.Out, "crond: started — %s\n", s.Dev.CronStatus())
	return 0
}
