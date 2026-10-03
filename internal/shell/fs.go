package shell

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// ---- filesystem builtins ----

func init() {
	// registration happens at package init via the table below.
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"ls", cmdLs}, {"cd", cmdCd}, {"pwd", cmdPwd}, {"cat", cmdCat}, {"cp", cmdCp},
		{"mv", cmdMv}, {"rm", cmdRm}, {"mkdir", cmdMkdir}, {"touch", cmdTouch},
		{"echo", cmdEcho}, {"printf", cmdPrintf}, {"ln", cmdLn}, {"chmod", cmdChmod},
		{"chown", cmdChown}, {"id", cmdId}, {"whoami", cmdWhoami}, {"hostname", cmdHostname},
		{"uname", cmdUname}, {"env", cmdEnv}, {"set", cmdSet}, {"unset", cmdUnset},
		{"pwd", cmdPwd}, {"pwd", cmdPwd},
	} {
		builtinTable[e.name] = e.fn
	}
}

func (s *Shell) out(s_ string) { fmt.Fprint(s.Out, s_) }

func cmdLs(s *Shell, args []string) int {
	// flags come first: `ls -l /etc/shadow` names the file, not "-l"
	p := "."
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			p = s.abs(a)
			break
		}
	}
	vfs, p, _ := s.ResolveVFS(p)
	if vfs == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[0], "Stale file handle")
		return 1
	}
	isDir := vfs.IsDir(p)
	if !isDir {
		n, ok := vfs.Get(p)
		if !ok {
			s.errf("%s: %s: No such file or directory", s.Dev.Hostname, p)
			return 1
		}
		fmt.Fprintf(s.Out, "%s\n", lsLine(n))
		return 0
	}
	long := false
	for _, a := range args {
		if strings.HasPrefix(a, "-l") {
			long = true
		}
	}
	entries := vfs.List(p)
	sort.Strings(entries)
	for _, e := range entries {
		n, ok := vfs.Get(e)
		if !ok {
			continue
		}
		if long {
			fmt.Fprintf(s.Out, "%s\n", lsLine(n))
		} else {
			fmt.Fprintf(s.Out, "%s\n", path.Base(e))
		}
	}
	return 0
}

func lsLine(n *core.INode) string {
	perm := n.Mode.Perm().String()
	if n.IsDir {
		perm = "d" + perm
	} else if n.Mode&fs.ModeSymlink != 0 {
		perm = "l" + perm
	} else {
		perm = "-" + perm
	}
	owner := n.Owner
	if owner == "" {
		owner = "root"
	}
	group := n.Group
	if group == "" {
		group = owner
	}
	return fmt.Sprintf("%s %4d %-8s %-8s %8d %s %s", perm, 4096, owner, group, len(n.Data), n.MTime.Format("Jan 2 15:04"), path.Base(n.Path))
}

func cmdCd(s *Shell, args []string) int {
	if len(args) == 0 {
		s.CWD = s.User.Home
		return 0
	}
	p := s.abs(args[0])
	vfs, p, err := s.ResolveVFS(p)
	if vfs == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[0], err)
		return 1
	}
	if !vfs.IsDir(p) {
		s.errf("%s: %s: Not a directory", s.Dev.Hostname, args[0])
		return 1
	}
	s.CWD = p
	return 0
}

func cmdPwd(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, s.CWD)
	return 0
}

func cmdCat(s *Shell, args []string) int {
	if len(args) == 0 {
		return 0
	}
	for _, a := range args {
		p := s.abs(a)
		vfs, p, err := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("%s: %s: %s", s.Dev.Hostname, a, err)
			continue
		}
		data, exists, allowed := vfs.ReadPathAs(p, s.User)
		if !exists {
			s.errf("cat: %s: No such file or directory", a)
			continue
		}
		if !allowed {
			s.errf("cat: %s: Permission denied", a)
			s.noteDenied(a, "read")
			continue
		}
		fmt.Fprint(s.Out, string(data))
	}
	return 0
}

func cmdCp(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: cp SRC DST")
		return 1
	}
	src := s.abs(args[0])
	vfs, src, err := s.ResolveVFS(src)
	if vfs == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[0], err)
		return 1
	}
	data, ok := s.readFile(vfs, src, fmt.Sprintf("%s: %s", s.Dev.Hostname, args[0]), args[0])
	if !ok {
		return 1
	}
	dst := s.abs(args[1])
	vfs2, dst, err2 := s.ResolveVFS(dst)
	if vfs2 == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[1], err2)
		return 1
	}
	if err := s.Dev.WriteGuest(dst, data, s.User); err != nil {
		s.errf("%s: %s: %v", s.Dev.Hostname, args[1], err)
		return 1
	}
	return 0
}

func cmdMv(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: mv SRC DST")
		return 1
	}
	src := s.abs(args[0])
	vfs, src, err := s.ResolveVFS(src)
	if vfs == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[0], err)
		return 1
	}
	n, ok := vfs.Get(src)
	if !ok {
		s.errf("%s: %s: No such file or directory", s.Dev.Hostname, args[0])
		return 1
	}
	data, ok := s.readFile(vfs, src, fmt.Sprintf("%s: %s", s.Dev.Hostname, args[0]), args[0])
	if !ok {
		return 1
	}
	if !vfs.CanRemove(src, s.User) {
		s.errf("%s: %s: Permission denied", s.Dev.Hostname, args[0])
		return 1
	}
	dst := s.abs(args[1])
	vfs2, dst, err2 := s.ResolveVFS(dst)
	if vfs2 == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, args[1], err2)
		return 1
	}
	if err := s.Dev.WriteGuest(dst, data, s.User); err != nil {
		s.errf("%s: %s: %v", s.Dev.Hostname, args[1], err)
		return 1
	}
	vfs.Remove(src)
	_ = n
	return 0
}

func cmdRm(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: rm FILE")
		return 1
	}
	failed := false
	for _, a := range args {
		p := s.abs(a)
		vfs, p, err := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("%s: %s: %s", s.Dev.Hostname, a, err)
			failed = true
			continue
		}
		if !vfs.CanRemove(p, s.User) {
			s.errf("%s: %s: Permission denied", s.Dev.Hostname, a)
			failed = true
			continue
		}
		if !vfs.Remove(p) {
			s.errf("%s: %s: No such file or directory", s.Dev.Hostname, a)
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

func cmdMkdir(s *Shell, args []string) int {
	for _, a := range args {
		p := s.abs(a)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("%s: %s: %s", s.Dev.Hostname, a, "Stale file handle")
			continue
		}
		if err := vfs.MkdirAllChecked(p, 0755, s.User); err != nil {
			s.errf("%s: %s: %v", s.Dev.Hostname, a, err)
			return 1
		}
	}
	return 0
}

func cmdTouch(s *Shell, args []string) int {
	if len(args) == 0 {
		return 1
	}
	for _, a := range args {
		p := s.abs(a)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		if n, exists := vfs.Get(p); exists {
			if n.IsDir || !vfs.CanWriteFile(p, s.User) {
				s.errf("%s: %s: Permission denied", s.Dev.Hostname, a)
				return 1
			}
			n.MTime = s.W.Now()
			continue
		}
		if err := s.Dev.WriteGuest(p, nil, s.User); err != nil {
			s.errf("%s: %s: %v", s.Dev.Hostname, a, err)
			return 1
		}
	}
	return 0
}

func cmdEcho(s *Shell, args []string) int {
	sep := " "
	nl := true
	var parts []string
	for _, a := range args {
		if a == "-n" {
			nl = false
			continue
		}
		parts = append(parts, a)
	}
	fmt.Fprint(s.Out, strings.Join(parts, sep))
	if nl {
		fmt.Fprint(s.Out, "\n")
	}
	return 0
}

func cmdPrintf(s *Shell, args []string) int {
	if len(args) == 0 {
		return 0
	}
	fmt.Fprintf(s.Out, "%s %s\n", args[0], strings.Join(args[1:], " "))
	return 0
}

func cmdLn(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: ln [-s] TARGET LINK")
		return 1
	}
	symlink := false
	rest := args
	if rest[0] == "-s" {
		symlink = true
		rest = rest[1:]
	}
	if len(rest) < 2 {
		s.errf("usage: ln [-s] TARGET LINK")
		return 1
	}
	target := rest[0]
	link := rest[1]
	if symlink {
		vfs, lp, _ := s.ResolveVFS(s.abs(link))
		if vfs == nil || !vfs.CanWriteDir(path.Dir(lp), s.User) {
			s.errf("%s: %s: Permission denied", s.Dev.Hostname, link)
			return 1
		}
		vfs.Symlink(target, lp)
		return 0
	}
	// hard link: copy
	src := s.abs(target)
	vfs, src, _ := s.ResolveVFS(src)
	if vfs == nil {
		s.errf("%s: %s: No such file or directory", s.Dev.Hostname, target)
		return 1
	}
	data, ok := s.readFile(vfs, src, fmt.Sprintf("%s: %s", s.Dev.Hostname, target), target)
	if !ok {
		return 1
	}
	dst := s.abs(link)
	vfs2, dst, _ := s.ResolveVFS(dst)
	if vfs2 == nil {
		s.errf("%s: %s: %s", s.Dev.Hostname, link, "Stale file handle")
		return 1
	}
	if err := s.Dev.WriteGuest(dst, data, s.User); err != nil {
		s.errf("%s: %s: %v", s.Dev.Hostname, link, err)
		return 1
	}
	return 0
}

func cmdChmod(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: chmod MODE FILE")
		return 1
	}
	mode, err := strconv.ParseInt(args[0], 8, 32)
	if err != nil {
		s.errf("%s: invalid mode: %s", s.Dev.Hostname, args[0])
		return 1
	}
	for _, a := range args[1:] {
		p := s.abs(a)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		n, ok := vfs.Get(p)
		if !ok {
			s.errf("%s: %s: No such file or directory", s.Dev.Hostname, a)
			return 1
		}
		if s.User.UID != 0 && (n.Owner != s.User.Name || !vfs.AccessiblePath(p, s.User)) {
			s.errf("%s: %s: Operation not permitted", s.Dev.Hostname, a)
			return 1
		}
		n.Mode = (n.Mode &^ fs.FileMode(0777)) | fs.FileMode(mode)
	}
	return 0
}

func cmdChown(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: chown OWNER:GROUP FILE")
		return 1
	}
	owner := args[0]
	if s.User.UID != 0 {
		s.errf("%s: changing ownership: Operation not permitted", s.Dev.Hostname)
		return 1
	}
	for _, a := range args[1:] {
		p := s.abs(a)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		n, ok := vfs.Get(p)
		if !ok {
			s.errf("%s: %s: No such file or directory", s.Dev.Hostname, a)
			continue
		}
		parts := strings.Split(owner, ":")
		if parts[0] != "" {
			n.Owner = parts[0]
		}
		if len(parts) > 1 && parts[1] != "" {
			n.Group = parts[1]
		}
	}
	return 0
}

func cmdId(s *Shell, args []string) int {
	// `id` with no args reports the current account
	if s.User == nil {
		return 1
	}
	u := s.User
	gname := u.Name
	if len(u.Groups) > 0 {
		gname = u.Groups[0]
	}
	fmt.Fprintf(s.Out, "uid=%d(%s) gid=%d(%s)", u.UID, u.Name, u.UID, gname)
	if len(u.Groups) > 1 {
		fmt.Fprintf(s.Out, " groups=%d(%s)", u.UID, gname)
		for _, g := range u.Groups[1:] {
			fmt.Fprintf(s.Out, ",%d(%s)", u.UID, g)
		}
	} else {
		fmt.Fprintf(s.Out, " groups=%d(%s)", u.UID, gname)
	}
	fmt.Fprintln(s.Out)
	return 0
}

func cmdWhoami(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, s.User.Name)
	return 0
}

func cmdHostname(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, s.Dev.Hostname)
	return 0
}

func cmdUname(s *Shell, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(s.Out, s.Dev.OS.Kernel)
		return 0
	}
	for _, a := range args {
		switch a {
		case "-a", "--all":
			fmt.Fprintf(s.Out, "%s %s %s %s %s GNU/Linux\n",
				"Linux", s.Dev.Hostname, s.Dev.OS.Kernel, "#1 SMP", s.Dev.OS.Arch)
		case "-s":
			fmt.Fprint(s.Out, "Linux ")
		case "-n":
			fmt.Fprint(s.Out, s.Dev.Hostname+" ")
		case "-r":
			fmt.Fprint(s.Out, s.Dev.OS.Kernel+" ")
		case "-m":
			fmt.Fprint(s.Out, s.Dev.OS.Arch+" ")
		case "-o":
			fmt.Fprint(s.Out, "GNU/Linux ")
		}
	}
	fmt.Fprintln(s.Out)
	return 0
}

func (s *Shell) OSInfoString(kind string) string { return s.Dev.OS.Kernel }

func cmdEnv(s *Shell, args []string) int {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(s.Out, "%s=%s\n", k, s.Env[k])
	}
	return 0
}

func cmdSet(s *Shell, args []string) int {
	for _, a := range args {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) == 2 {
			s.Env[parts[0]] = parts[1]
		}
	}
	return 0
}

func cmdUnset(s *Shell, args []string) int {
	for _, a := range args {
		delete(s.Env, a)
	}
	return 0
}

var _ = os.Args
