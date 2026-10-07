package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// Builders (Phase 3) — guided setup that performs the real steps instead of
// describing them: every step is the player's own verbs run through a
// sub-shell as root, checked one by one, with rollback when a step fails.
// `server build` raises a role server (web, mail, ftp, db); `backup init`
// wires scheduled restic backups. Nothing here invents state: install,
// configure, start, verify — each against the live world.

func init() {
	builtinTable["server"] = cmdServerBuild
}

// buildSh runs one command line as root on the session device and reports
// success. The builder speaks the player's language throughout: a step that
// fails says so in the tool's own words.
func buildSh(s *Shell, line string) (string, bool) {
	root := s.Dev.FindUser("root")
	if root == nil {
		root = &core.User{Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"}
	}
	out := &strings.Builder{}
	sub := &Shell{W: s.W, Dev: s.Dev, User: root, CWD: s.CWD, Env: s.Env, Out: out,
		bufrd: s.bufrd, InTmux: s.InTmux, srcIP: s.srcIP, TTY: s.TTY, hist: s.hist}
	return out.String(), sub.execOne(line)
}

type serverRole struct {
	pkgs    []string
	service string
	port    int
	blurb   string
}

var serverRoles = map[string]serverRole{
	"web":  {pkgs: []string{"nginx"}, service: "nginx", port: 80, blurb: "web server"},
	"mail": {pkgs: []string{"opensmtpd"}, service: "smtpd", port: 25, blurb: "mail transfer agent"},
	"ftp":  {pkgs: []string{"vsftpd"}, service: "vsftpd", port: 21, blurb: "FTP daemon"},
	"db":   {pkgs: []string{"mariadb"}, service: "mariadb", port: 3306, blurb: "database server"},
}

func cmdServerBuild(s *Shell, args []string) int {
	if len(args) < 2 || args[0] != "build" {
		s.errf("usage: server build ROLE  (web|mail|ftp|db)")
		return 1
	}
	role, ok := serverRoles[args[1]]
	if !ok {
		s.errf("server: unknown role %q (web|mail|ftp|db)", args[1])
		return 1
	}
	if s.User.UID != 0 {
		s.errf("server: building services needs root")
		return 1
	}
	mgr := core.ManagerFor(s.Dev)
	if mgr == "" {
		s.errf("server: no package manager on %s", s.Dev.Hostname)
		return 1
	}
	fmt.Fprintf(s.Out, "building %s on %s:\n", role.blurb, s.Dev.Hostname)
	// what this run installed, for rollback in reverse on failure
	var mine []string
	installed := func(name string) bool { return s.Dev.Installed[name] != nil }
	fail := func(format string, a ...any) int {
		if len(mine) > 0 {
			fmt.Fprintf(s.Out, "rolling back %d package(s):\n", len(mine))
			for i := len(mine) - 1; i >= 0; i-- {
				if p := s.Dev.Installed[mine[i]]; p != nil {
					for _, a := range s.W.RemovePkg(s.Dev, p) {
						fmt.Fprintf(s.Out, "  %s\n", a)
					}
				}
			}
		}
		s.errf(format, a...)
		return 1
	}
	if out, ok := buildSh(s, mgr+" update"); !ok {
		return fail("server: cannot refresh package lists:\n%s", out)
	}
	fmt.Fprintf(s.Out, "  [1/4] package lists refreshed\n")
	for _, pkg := range role.pkgs {
		if installed(pkg) {
			fmt.Fprintf(s.Out, "  [2/4] %s already installed\n", pkg)
			continue
		}
		out, ok := buildSh(s, mgr+" install "+pkg)
		if !ok || !installed(pkg) {
			return fail("server: cannot install %s:\n%s", pkg, out)
		}
		mine = append(mine, pkg)
		fmt.Fprintf(s.Out, "  [2/4] installed %s\n", pkg)
	}
	svc := s.Dev.Svc(role.service)
	if svc == nil {
		return fail("server: %s registered no %s service", strings.Join(role.pkgs, ","), role.service)
	}
	if svc.State != "running" {
		if _, ok := buildSh(s, "systemctl start "+role.service); !ok {
			return fail("server: cannot start %s", role.service)
		}
		fmt.Fprintf(s.Out, "  [3/4] started %s\n", role.service)
	} else {
		fmt.Fprintf(s.Out, "  [3/4] %s already running\n", role.service)
	}
	// verify the way anyone else would: dial the port on our own address
	addr := s.Dev.SvcAddr()
	if _, _, msg := core.Dial(s.Dev, addr, role.port); msg != "connected" {
		return fail("server: verification failed (%s:%d: %s)", addr, role.port, msg)
	}
	fmt.Fprintf(s.Out, "  [4/4] verified: %s answers on %s:%d\n", role.service, addr, role.port)
	return 0
}

// cmdBackupInit wires scheduled restic backups: install, repository, key,
// schedule, first snapshot, verification — each step real, each failure
// honest, in the order a careful operator would do them by hand.
func cmdBackupInit(s *Shell, args []string) int {
	repo, schedule, tree := "", "0 3 * * *", "/home"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repo":
			if i+1 >= len(args) {
				s.errf("usage: backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
				return 1
			}
			i++
			repo = args[i]
		case "--schedule":
			if i+1 >= len(args) {
				s.errf("usage: backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
				return 1
			}
			i++
			schedule = args[i]
		case "--tree":
			if i+1 >= len(args) {
				s.errf("usage: backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
				return 1
			}
			i++
			tree = args[i]
		default:
			s.errf("usage: backup init [--repo SPEC] [--schedule CRON] [--tree PATH]")
			return 1
		}
	}
	if s.User.UID != 0 {
		s.errf("backup: wiring backups needs root")
		return 1
	}
	mgr := core.ManagerFor(s.Dev)
	if mgr == "" {
		s.errf("backup: no package manager on %s", s.Dev.Hostname)
		return 1
	}
	fmt.Fprintf(s.Out, "wiring backups on %s:\n", s.Dev.Hostname)
	// [1/5] the tool itself, like any other install
	if s.Dev.Installed["restic"] == nil {
		if out, ok := buildSh(s, mgr+" update"); !ok {
			s.errf("backup: cannot refresh package lists:\n%s", out)
			return 1
		}
		if out, ok := buildSh(s, mgr+" install restic"); !ok || s.Dev.Installed["restic"] == nil {
			s.errf("backup: cannot install restic:\n%s", out)
			return 1
		}
		fmt.Fprintf(s.Out, "  [1/5] installed restic\n")
	} else {
		fmt.Fprintf(s.Out, "  [1/5] restic already installed\n")
	}
	// [2/5] the repository: flag wins, installed config otherwise. Either
	// way it is a file the operator can read, not a hidden setting.
	if repo != "" {
		env := "RESTIC_REPOSITORY=" + repo + "\nRESTIC_PASSWORD_FILE=/etc/restic/password\n"
		if err := s.Dev.WriteGuest("/etc/restic/env", []byte(env), s.User); err != nil {
			s.errf("backup: %v", err)
			return 1
		}
	}
	spec, _, ok := core.ResticEnv(s.Dev)
	if !ok || spec == "" {
		s.errf("backup: no repository configured (see /etc/restic/env)")
		return 1
	}
	fmt.Fprintf(s.Out, "  [2/5] repository: %s\n", spec)
	// [3/5] initialise it (or meet the existing one): unreachable stores
	// fail here with the real reason, before anything is scheduled
	if !core.ResticInitialized(s.Dev) {
		if _, err := core.ResticInit(s.Dev); err != nil {
			s.errf("backup: cannot init repository: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "  [3/5] repository initialised\n")
	} else {
		fmt.Fprintf(s.Out, "  [3/5] repository already initialised\n")
	}
	// [4/5] the schedule, validated by the same parser cron itself uses
	line := fmt.Sprintf("%s root restic backup\n", schedule)
	if _, errs := core.ParseSystemCrontab(line); len(errs) > 0 {
		s.errf("backup: bad schedule %q: %v", schedule, errs[0])
		return 1
	}
	if err := s.Dev.WriteGuest("/etc/cron.d/restic", []byte(line), s.User); err != nil {
		s.errf("backup: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "  [4/5] scheduled (%s)\n", schedule)
	// [5/5] the first snapshot, then proof it exists
	snap, err := core.ResticBackup(s.Dev, tree)
	if err != nil {
		s.errf("backup: first snapshot failed: %v", err)
		return 1
	}
	snaps, err := core.ResticSnapshots(s.Dev)
	if err != nil || len(snaps) == 0 {
		s.errf("backup: no snapshots after backup: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "  [5/5] snapshot %s taken (%d total)\n", snap.ID, len(snaps))
	return 0
}
