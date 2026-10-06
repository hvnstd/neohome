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
	escapes := false
	var parts []string
	for _, a := range args {
		switch a {
		case "-n":
			nl = false
			continue
		case "-e":
			// -e is the flag that turns \n into a newline; without it the
			// backslash is data, which is why `echo -e` exists at all
			escapes = true
			continue
		case "-E":
			escapes = false
			continue
		}
		parts = append(parts, a)
	}
	line := strings.Join(parts, sep)
	if escapes {
		line = printfText(line)
	}
	fmt.Fprint(s.Out, line)
	if nl {
		fmt.Fprint(s.Out, "\n")
	}
	return 0
}

// cmdPrintf is BusyBox printf: FORMAT [ARG...]. Escapes are applied, the
// conversions really consume arguments, and the format repeats until every
// argument is used — which is what makes `printf '%s\n' a b c` the idiom it is,
// and what a shell script writing a config file depends on.
func cmdPrintf(s *Shell, args []string) int {
	if len(args) == 0 {
		return 0
	}
	format, rest := args[0], args[1:]
	for {
		out, used, conv := printfOnce(format, rest)
		fmt.Fprint(s.Out, out)
		if !conv || used >= len(rest) {
			break
		}
		rest = rest[used:]
	}
	return 0
}

// printfOnce renders one pass of the format. It reports how many arguments the
// pass consumed and whether the format had any conversion at all (a format
// without conversions prints once, however many arguments were given).
func printfOnce(format string, args []string) (string, int, bool) {
	var b strings.Builder
	used, conv := 0, false
	for i := 0; i < len(format); {
		c := format[i]
		if c == '\\' {
			esc, n := printfEscape(format[i:])
			b.WriteString(esc)
			i += n
			continue
		}
		if c != '%' {
			b.WriteByte(c)
			i++
			continue
		}
		// flags, width and precision travel to Go's own formatter unchanged:
		// %-5s, %08.2f and %#x mean the same thing in both.
		j := i + 1
		for j < len(format) && strings.IndexByte("-+ #0.0123456789", format[j]) >= 0 {
			j++
		}
		if j >= len(format) {
			b.WriteString(format[i:])
			break
		}
		prefix, verb := format[i:j], format[j]
		i = j + 1
		if verb == '%' {
			b.WriteByte('%')
			continue
		}
		conv = true
		arg := ""
		if used < len(args) {
			arg = args[used]
			used++
		}
		switch verb {
		case 's':
			b.WriteString(fmt.Sprintf(prefix+"s", arg))
		case 'q':
			b.WriteString(strconv.Quote(arg))
		case 'c':
			if arg != "" {
				b.WriteString(fmt.Sprintf(prefix+"c", []rune(arg)[0]))
			}
		case 'b':
			// %b is echo -e on one argument: the escapes are in the data
			b.WriteString(fmt.Sprintf(prefix+"s", printfText(arg)))
		case 'd', 'i', 'u', 'o', 'x', 'X':
			n, _ := strconv.ParseInt(strings.TrimSpace(arg), 0, 64)
			goVerb := verb
			if goVerb == 'i' || goVerb == 'u' {
				goVerb = 'd'
			}
			b.WriteString(fmt.Sprintf(prefix+string(goVerb), n))
		case 'f', 'e', 'E', 'g', 'G':
			f, _ := strconv.ParseFloat(strings.TrimSpace(arg), 64)
			b.WriteString(fmt.Sprintf(prefix+string(verb), f))
		default:
			// an unknown verb is echoed as written rather than invented
			b.WriteString(prefix + string(verb))
		}
	}
	return b.String(), used, conv
}

// printfText applies backslash escapes to a whole string. This is what a shell
// means by "the escapes are in the data" (%b), and it is the one place escapes
// are interpreted — a double-quoted argument keeps its backslash, exactly as a
// real shell does with echo.
func printfText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			esc, n := printfEscape(s[i:])
			b.WriteString(esc)
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// printfEscape reads one backslash escape and reports the bytes it consumed.
// An escape nobody knows keeps its backslash, which is what the C library does.
func printfEscape(s string) (string, int) {
	if len(s) < 2 {
		return "\\", 1
	}
	switch s[1] {
	case 'n':
		return "\n", 2
	case 't':
		return "\t", 2
	case 'r':
		return "\r", 2
	case 'a':
		return "\a", 2
	case 'b':
		return "\b", 2
	case 'f':
		return "\f", 2
	case 'v':
		return "\v", 2
	case 'e':
		return "\x1b", 2
	case '\\':
		return "\\", 2
	case '"':
		return "\"", 2
	case '\'':
		return "'", 2
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n, used := 0, 0
		for k := 1; k < len(s) && k <= 3; k++ {
			d := s[k]
			if d < '0' || d > '7' {
				break
			}
			n = n*8 + int(d-'0')
			used++
		}
		return string(rune(n)), 1 + used
	case 'x':
		n, used := 0, 0
		for k := 2; k < len(s) && k <= 3; k++ {
			d := hexDigit(s[k])
			if d < 0 {
				break
			}
			n = n*16 + d
			used++
		}
		if used == 0 {
			return "\\", 1
		}
		return string(rune(n)), 1 + used
	}
	return s[:2], 2
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
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
