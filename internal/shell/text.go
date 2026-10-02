package shell

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"find", cmdFind}, {"grep", cmdGrep}, {"head", cmdHead}, {"tail", cmdTail},
		{"sort", cmdSort}, {"uniq", cmdUniq}, {"wc", cmdWc}, {"hostname", cmdHostname},
		{"date", cmdDate}, {"which", cmdWhich}, {"file", cmdFile}, {"test", cmdTest},
		{"stat", cmdStat}, {"basename", cmdBasename}, {"dirname", cmdDirname},
		{"readlink", cmdReadlink}, {"true", cmdTrue}, {"false", cmdFalse},
		{"sed", cmdSed},
	} {
		builtinTable[e.name] = e.fn
	}
}

func cmdFind(s *Shell, args []string) int {
	p := "."
	pattern := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-name") && len(a) > 5 {
			pattern = a[5:]
		} else if strings.HasPrefix(a, "-name=") {
			pattern = strings.TrimPrefix(a, "-name=")
		} else if !strings.HasPrefix(a, "-") {
			p = a
		}
	}
	vfs, p, _ := s.ResolveVFS(s.abs(p))
	if vfs == nil {
		return 1
	}
	for k := range vfs.Nodes {
		if pattern != "" && !strings.Contains(k, pattern) {
			continue
		}
		fmt.Fprintln(s.Out, k)
	}
	return 0
}

func cmdGrep(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: grep PATTERN [FILE...]")
		return 2
	}
	pattern := args[0]
	files := args[1:]
	if len(files) == 0 {
		// read stdin (pipeline) — exit 1 when nothing matched, like real grep
		matched := false
		for _, line := range strings.Split(strings.TrimRight(s.Stdin, "\n"), "\n") {
			if s.Stdin == "" {
				break
			}
			if strings.Contains(line, pattern) {
				fmt.Fprintln(s.Out, line)
				matched = true
			}
		}
		if !matched {
			return 1
		}
		return 0
	}
	matched := false
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			s.errf("grep: %s: No such file or directory", f)
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, pattern) {
				fmt.Fprintf(s.Out, "%s:%s\n", f, line)
				matched = true
			}
		}
	}
	if !matched {
		return 1
	}
	return 0
}

// cmdSed implements the subset of sed that actually changes world state:
// s/RE/REPL/[g] substitution, in-place or to stdout. Enough to edit a config,
// which is precisely what a sysadmin does in this game.
func cmdSed(s *Shell, args []string) int {
	inPlace := false
	var script string
	var files []string
	for _, a := range args {
		switch {
		case a == "-i" || strings.HasPrefix(a, "-i"):
			inPlace = true
		case script == "" && (strings.HasPrefix(a, "s") || strings.HasPrefix(a, "y")):
			script = a
		case strings.HasPrefix(a, "-"):
			// ignore other flags (-e/-n) rather than misreading them as files
		default:
			files = append(files, a)
		}
	}
	if script == "" {
		s.errf("usage: sed [-i] 's/RE/REPL/[g]' [FILE...]")
		return 1
	}
	if !strings.HasPrefix(script, "s") {
		s.errf("sed: only s/RE/REPL/[g] substitution is supported here")
		return 1
	}
	delim := script[1:2]
	parts := strings.Split(script[2:], delim)
	if len(parts) < 2 {
		s.errf("sed: bad substitution: %s", script)
		return 1
	}
	re, repl := parts[0], parts[1]
	global := len(parts) > 2 && strings.Contains(parts[2], "g")

	if len(files) == 0 {
		out := strings.ReplaceAll(s.Stdin, re, repl)
		fmt.Fprint(s.Out, out)
		return 0
	}
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			s.errf("sed: %s: No such file or directory", f)
			return 1
		}
		data, ok := vfs.Read(p)
		if !ok {
			s.errf("sed: %s: No such file or directory", f)
			return 1
		}
		var out string
		if global {
			out = strings.ReplaceAll(string(data), re, repl)
		} else {
			out = strings.Replace(string(data), re, repl, 1)
		}
		if inPlace {
			// a real config edit must go through permission checks
			if err := s.Dev.WriteGuest(p, []byte(out), s.User); err != nil {
				s.errf("sed: %s: %v", f, err)
				return 1
			}
		} else {
			fmt.Fprint(s.Out, out)
		}
	}
	return 0
}

func cmdHead(s *Shell, args []string) int {
	n := 10
	files := args
	for i, a := range args {
		if a == "-n" && i+1 < len(args) {
			n, _ = strconv.Atoi(args[i+1])
			files = append(append([]string{}, args[:i]...), args[i+2:]...)
			break
		}
		// `head -3` short form
		if len(a) > 1 && a[0] == '-' {
			if v, err := strconv.Atoi(a[1:]); err == nil {
				n = v
				files = append(append([]string{}, args[:i]...), args[i+1:]...)
				break
			}
		}
	}
	if len(files) == 0 {
		// read the pipeline's stdin
		lines := strings.Split(strings.TrimRight(s.Stdin, "\n"), "\n")
		if s.Stdin == "" {
			return 0
		}
		for i := 0; i < n && i < len(lines); i++ {
			fmt.Fprintln(s.Out, lines[i])
		}
		return 0
	}
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i := 0; i < n && i < len(lines); i++ {
			fmt.Fprintln(s.Out, lines[i])
		}
	}
	return 0
}

func cmdTail(s *Shell, args []string) int {
	n := 10
	files := args
	for i, a := range args {
		if a == "-n" && i+1 < len(args) {
			n, _ = strconv.Atoi(args[i+1])
			files = append(append([]string{}, args[:i]...), args[i+2:]...)
			break
		}
		// real tail accepts the `-6` short form, and people type it constantly
		if len(a) > 1 && a[0] == '-' {
			if v, err := strconv.Atoi(a[1:]); err == nil {
				n = v
				files = append(append([]string{}, args[:i]...), args[i+1:]...)
				break
			}
		}
	}
	if len(files) == 0 {
		if s.Stdin == "" {
			return 0
		}
		lines := strings.Split(strings.TrimRight(s.Stdin, "\n"), "\n")
		start := len(lines) - n
		if start < 0 {
			start = 0
		}
		for _, l := range lines[start:] {
			fmt.Fprintln(s.Out, l)
		}
		return 0
	}
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			continue
		}
		lines := strings.Split(string(data), "\n")
		start := len(lines) - n
		if start < 0 {
			start = 0
		}
		for i := start; i < len(lines); i++ {
			fmt.Fprintln(s.Out, lines[i])
		}
	}
	return 0
}

func cmdSort(s *Shell, args []string) int {
	files := args
	if len(files) == 0 {
		if s.Stdin == "" {
			return 0
		}
		all := strings.Split(strings.TrimRight(s.Stdin, "\n"), "\n")
		sort.Strings(all)
		for _, l := range all {
			fmt.Fprintln(s.Out, l)
		}
		return 0
	}
	var all []string
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			continue
		}
		all = append(all, strings.Split(string(data), "\n")...)
	}
	sort.Strings(all)
	for _, l := range all {
		fmt.Fprintln(s.Out, l)
	}
	return 0
}

func cmdUniq(s *Shell, args []string) int {
	files := args
	var all []string
	if len(files) == 0 {
		if s.Stdin == "" {
			return 0
		}
		all = strings.Split(strings.TrimRight(s.Stdin, "\n"), "\n")
	}
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			continue
		}
		all = append(all, strings.Split(string(data), "\n")...)
	}
	var prev string
	for _, l := range all {
		if l != prev {
			fmt.Fprintln(s.Out, l)
			prev = l
		}
	}
	return 0
}

func cmdWc(s *Shell, args []string) int {
	files := args
	if len(files) == 0 {
		if s.Stdin == "" {
			return 0
		}
		lines := strings.Split(s.Stdin, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		words := strings.Fields(s.Stdin)
		fmt.Fprintf(s.Out, "%5d %5d %5d\n", len(lines), len(words), len(s.Stdin))
		return 0
	}
	for _, f := range files {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		data, ok := vfs.Read(p)
		if !ok {
			continue
		}
		lines := strings.Split(string(data), "\n")
		words := strings.Fields(string(data))
		fmt.Fprintf(s.Out, "%5d %5d %5d %s\n", len(lines), len(words), len(data), f)
	}
	return 0
}

func cmdDate(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, s.W.Sim.Format("Mon Jan 2 15:04:05 UTC 2006"))
	return 0
}

func cmdWhich(s *Shell, args []string) int {
	if len(args) == 0 {
		return 1
	}
	name := args[0]
	for _, dir := range []string{"/usr/bin/", "/bin/", "/usr/sbin/", "/sbin/", "/usr/local/bin/"} {
		if n, ok := s.Dev.FS.Get(dir + name); ok && n.Mode&0111 != 0 {
			fmt.Fprintln(s.Out, dir+name)
			return 0
		}
	}
	return 1
}

func cmdFile(s *Shell, args []string) int {
	for _, f := range args {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			fmt.Fprintf(s.Out, "%s: cannot open `%s' (No such file or directory)\n", f, f)
			continue
		}
		n, ok := vfs.Get(p)
		if !ok {
			fmt.Fprintf(s.Out, "%s: cannot open `%s' (No such file or directory)\n", f, f)
			continue
		}
		kind := "ASCII text"
		if len(n.Data) > 0 {
			binary := false
			for _, b := range n.Data {
				if b == 0 {
					binary = true
					break
				}
			}
			if binary {
				kind = "data"
			}
		}
		fmt.Fprintf(s.Out, "%s: %s\n", f, kind)
	}
	return 0
}

func cmdTest(s *Shell, args []string) int {
	if len(args) < 2 {
		return 1
	}
	op := args[0]
	p := s.abs(args[1])
	vfs, p, _ := s.ResolveVFS(p)
	if vfs == nil {
		return 1
	}
	switch op {
	case "-f":
		if vfs.Exists(p) {
			return 0
		}
	case "-d":
		if vfs.IsDir(p) {
			return 0
		}
	case "-e":
		if vfs.Exists(p) {
			return 0
		}
	}
	return 1
}

func cmdStat(s *Shell, args []string) int {
	if len(args) == 0 {
		return 1
	}
	for _, f := range args {
		p := s.abs(f)
		vfs, p, _ := s.ResolveVFS(p)
		if vfs == nil {
			continue
		}
		n, ok := vfs.Get(p)
		if !ok {
			fmt.Fprintf(s.Out, "stat: cannot stat `%s': No such file or directory\n", f)
			continue
		}
		fmt.Fprintf(s.Out, "  File: %s\n", f)
		fmt.Fprintf(s.Out, "  Size: %d\tType: %s\n", len(n.Data), "regular file")
		fmt.Fprintf(s.Out, "  Access: %04o\tModify: %04o\n", n.Mode.Perm(), n.Mode.Perm())
	}
	return 0
}

func cmdBasename(s *Shell, args []string) int {
	for _, a := range args {
		fmt.Fprintln(s.Out, path.Base(a))
	}
	return 0
}

func cmdDirname(s *Shell, args []string) int {
	for _, a := range args {
		fmt.Fprintln(s.Out, path.Dir(a))
	}
	return 0
}

func cmdReadlink(s *Shell, args []string) int {
	if len(args) == 0 {
		return 1
	}
	p := s.abs(args[0])
	vfs, p, _ := s.ResolveVFS(p)
	if vfs == nil {
		return 1
	}
	n, ok := vfs.Get(p)
	if !ok {
		return 1
	}
	fmt.Fprintln(s.Out, n.Symlink)
	return 0
}

func cmdTrue(s *Shell, args []string) int  { return 0 }
func cmdFalse(s *Shell, args []string) int { return 1 }

var _ = time.Now
