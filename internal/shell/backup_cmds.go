package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// §33 防守和安全软件, the backup and host-monitoring side of it.
//
//	restic init|backup|snapshots|check|restore|forget|stats|cat
//	rkhunter --propupd|--check|--status
//
// Both are installed software with real configuration files, and both act on
// real bytes: a snapshot holds the files that were there, a check re-hashes
// them, a restore puts them back, and the host monitor's baseline is a file a
// player can read. Neither of them reports success it did not achieve.

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"restic", cmdRestic}, {"rkhunter", cmdRkhunter},
	} {
		builtinTable[e.name] = e.fn
	}
}

// ---- restic ----------------------------------------------------------------

func cmdRestic(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("restic: usage: restic [init|backup|snapshots|check|restore|forget|stats|cat] ...")
		return 1
	}
	switch args[0] {
	case "init":
		msg, err := core.ResticInit(s.Dev)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, msg)
		return 0

	case "backup":
		tree := ""
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "-") {
				tree = a
			}
		}
		if tree == "" {
			tree = s.User.Home
		}
		tree = s.abs(tree)
		snap, err := core.ResticBackup(s.Dev, tree)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "scanning %s\n", tree)
		if snap.Parent != "" {
			fmt.Fprintf(s.Out, "using parent snapshot %s\n", snap.Parent[:8])
		} else {
			fmt.Fprintln(s.Out, "no parent snapshot found, will read all files")
		}
		fmt.Fprintf(s.Out, "added to repository: %d file(s), %s, %d new blob(s)\n",
			snap.Files, humanBytes(snap.Bytes), snap.Added)
		if snap.Removed > 0 {
			fmt.Fprintf(s.Out, "%d file(s) are gone since the parent snapshot\n", snap.Removed)
		}
		fmt.Fprintf(s.Out, "snapshot %s saved\n", snap.ID[:8])
		return 0

	case "snapshots":
		snaps, err := core.ResticSnapshots(s.Dev)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		if len(snaps) == 0 {
			fmt.Fprintln(s.Out, "no snapshots — nothing is backed up yet")
			return 0
		}
		fmt.Fprintf(s.Out, "%-10s %-19s %-12s %8s %10s %s\n", "ID", "TIME", "HOST", "FILES", "SIZE", "PATH")
		for _, sn := range snaps {
			host := sn.Host
			if host == "" {
				host = "-"
			}
			paths := strings.Join(sn.Paths, ",")
			if paths == "" {
				paths = "-"
			}
			fmt.Fprintf(s.Out, "%-10s %-19s %-12s %8d %10s %s\n", sn.ID[:8],
				sn.At.Format("2006-01-02 15:04:05"), host, sn.Files, humanBytes(sn.Bytes), paths)
		}
		return 0

	case "check":
		checked, problems, err := core.ResticCheck(s.Dev)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		for _, p := range problems {
			fmt.Fprintf(s.Out, "error: %s\n", p)
		}
		if len(problems) == 0 {
			fmt.Fprintf(s.Out, "no errors found: %d blob(s) verified against their hashes\n", checked)
			return 0
		}
		fmt.Fprintf(s.Out, "found %d error(s)\n", len(problems))
		return 1

	case "restore":
		if len(args) < 2 {
			s.errf("usage: restic restore SNAPSHOT [--target DIR] [--overwrite]")
			return 1
		}
		id, target, overwrite := args[1], "", false
		for i := 2; i < len(args); i++ {
			switch {
			case args[i] == "--overwrite":
				overwrite = true
			case strings.HasPrefix(args[i], "--target="):
				target = s.abs(strings.TrimPrefix(args[i], "--target="))
			case args[i] == "--target" && i+1 < len(args):
				i++
				target = s.abs(args[i])
			}
		}
		restored, skipped, err := core.ResticRestore(s.Dev, id, target, overwrite, s.User)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		where := target
		if where == "" {
			where = "the original paths"
		}
		fmt.Fprintf(s.Out, "restoring snapshot %s to %s\n", id, where)
		fmt.Fprintf(s.Out, "%d file(s) restored\n", restored)
		if skipped > 0 {
			fmt.Fprintf(s.Out, "%d file(s) already existed and were left alone (use --overwrite)\n", skipped)
		}
		return 0

	case "forget":
		if len(args) < 2 {
			s.errf("usage: restic forget SNAPSHOT [--prune]")
			return 1
		}
		prune := false
		for _, a := range args[2:] {
			if a == "--prune" {
				prune = true
			}
		}
		msg, err := core.ResticForget(s.Dev, args[1], prune)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, msg)
		return 0

	case "stats":
		snaps, err := core.ResticSnapshots(s.Dev)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		files, bytes := 0, 0
		last := ""
		for _, sn := range snaps {
			files += sn.Files
			bytes += sn.Bytes
			last = sn.At.Format("2006-01-02 15:04:05")
		}
		state, detail := core.ResticPosture(s.Dev)
		fmt.Fprintf(s.Out, "repository: %s (%s)\n", detail, state)
		fmt.Fprintf(s.Out, "snapshots: %d\n", len(snaps))
		fmt.Fprintf(s.Out, "total files across snapshots: %d (%s)\n", files, humanBytes(bytes))
		if last != "" {
			fmt.Fprintf(s.Out, "newest snapshot: %s\n", last)
		}
		return 0

	case "cat":
		if len(args) < 2 {
			s.errf("usage: restic cat SNAPSHOT")
			return 1
		}
		snaps, err := core.ResticSnapshots(s.Dev)
		if err != nil {
			s.errf("restic: %v", err)
			return 1
		}
		for _, sn := range snaps {
			if sn.ID == args[1] || strings.HasPrefix(sn.ID, args[1]) {
				fmt.Fprint(s.Out, sn.Body)
				return 0
			}
		}
		s.errf("restic: snapshot %s not found", args[1])
		return 1
	}
	s.errf("restic: unknown command %q", args[0])
	return 1
}

// ---- rkhunter (host-based intrusion detection) ------------------------------

func cmdRkhunter(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("rkhunter: usage: rkhunter --propupd | --check [--sk] | --status")
		return 1
	}
	fmt.Fprintf(s.Out, "[ Rootkit Hunter version 1.4.6 ]\n")
	switch args[0] {
	case "--propupd":
		n, err := s.Dev.RkhunterPropupd(s.User)
		if err != nil {
			s.errf("rkhunter: cannot write the baseline: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "The file properties have been updated\n")
		fmt.Fprintf(s.Out, "baseline written to %s (%d facts)\n", core.RkhunterBasePath, n)
		return 0

	case "--check":
		quiet := false
		for _, a := range args[1:] {
			if a == "--sk" || a == "--skip-keypress" {
				quiet = true
			}
		}
		findings, err := s.Dev.RkhunterCheck()
		if err != nil {
			s.errf("rkhunter: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, "Checking system commands...")
		fmt.Fprintln(s.Out, "Checking listening ports...")
		fmt.Fprintln(s.Out, "Checking user accounts...")
		fmt.Fprintln(s.Out, "Checking for suspicious files...")
		suspects := 0
		for _, f := range findings {
			suspects++
			fmt.Fprintf(s.Out, "Warning: %s\n", f.Msg)
			// a check run by a person does the same thing the periodic check
			// does: it puts the finding where the household can see it
			s.Dev.Alertf("rkhunter", f.Kind, "alert", "%s", f.Msg)
		}
		fmt.Fprintln(s.Out, "\nSystem checks summary")
		fmt.Fprintln(s.Out, "=====================")
		base, has := s.Dev.RkhunterBaseline()
		if has {
			fmt.Fprintf(s.Out, "Baseline facts: %d\n", len(base))
		}
		fmt.Fprintf(s.Out, "Suspect files: %d\n", suspects)
		if suspects == 0 {
			fmt.Fprintln(s.Out, "No warnings — this host still matches its baseline")
			return 0
		}
		fmt.Fprintf(s.Out, "Rootkit checks: %d warning(s) — run `rkhunter --propupd` only after you have explained them\n", suspects)
		_ = quiet
		return 1

	case "--status":
		base, has := s.Dev.RkhunterBaseline()
		if !has {
			fmt.Fprintf(s.Out, "No baseline taken yet — run `rkhunter --propupd`\n")
			return 0
		}
		fmt.Fprintf(s.Out, "Baseline: %s\n", core.RkhunterBasePath)
		fmt.Fprintf(s.Out, "Facts: %d\n", len(base))
		cfg := s.Dev.RkhunterConfigOf()
		fmt.Fprintf(s.Out, "Interval: %s\n", cfg.Interval)
		if findings, err := s.Dev.RkhunterCheck(); err == nil {
			fmt.Fprintf(s.Out, "Current warnings: %d\n", len(findings))
		}
		return 0
	}
	s.errf("rkhunter: unknown option %q", args[0])
	return 1
}
