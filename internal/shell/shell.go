package shell

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"

	"neohome/internal/core"
)

// Cmd is one builtin: it MUST read or write real world state.
type Cmd func(s *Shell, args []string) int

// Shell: a game shell session bound to (device, user).
type Shell struct {
	W    *core.World
	Dev  *core.Device
	User *core.User
	CWD  string
	Env  map[string]string
	Out  io.Writer
	// Stdin carries the previous pipeline stage's output. Commands that read
	// stdin (grep/head/tail/sort/uniq/wc/cat) use it when given no file.
	Stdin string

	bufrd     *bufio.Reader
	hist      []string
	InTmux    string
	srcIP     string
	TTY       string
	pendingLF bool // a \r was consumed; swallow the \n that follows it
	exitFlag  bool
	detach    bool // ^] style detach requested inside tmux
	stolen    []string
	creds     []string
}

func NewShell(w *core.World, d *core.Device, u *core.User, out io.Writer, srcIP, tty string) *Shell {
	return &Shell{
		W: w, Dev: d, User: u, CWD: u.Home, Out: out, srcIP: srcIP, TTY: tty,
		Env: map[string]string{
			"HOME": u.Home, "USER": u.Name, "SHELL": u.Shell, "TERM": "xterm-256color",
			"PATH":     "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"HOSTNAME": d.Hostname, "LANG": "C.UTF-8", "PWD": u.Home,
		},
	}
}

var builtinTable = map[string]Cmd{}

// SetInput points the shell at a scripted input stream (used by tests and by
// the in-world `script` replay path). Password reads and line reads then share
// the same CRLF handling as a live session.
func (s *Shell) SetInput(r io.Reader) { s.bufrd = bufio.NewReader(r) }

// ReadLineForTest exposes the shell's line reader for regression tests.
func (s *Shell) ReadLineForTest() string {
	line, _ := s.readLine()
	return line
}

func reg(names ...string) {
	// usage: fn := regOne("ls", cmdLs); simpler: assign directly in tables.
	_ = names
}

func registerAll() {}

// ---- prompt / loop ----

func (s *Shell) PS1() string {
	suffix := "$"
	if s.User.UID == 0 {
		suffix = "#"
	}
	host := s.Dev.Hostname
	if s.InTmux != "" {
		host = fmt.Sprintf("(tmux:%s) %s", s.InTmux, host)
	}
	return fmt.Sprintf("%s@%s:%s%s ", s.User.Name, host, s.relCWD(), suffix)
}

func (s *Shell) relCWD() string {
	if s.CWD == s.User.Home {
		return "~"
	}
	if strings.HasPrefix(s.CWD, s.User.Home+"/") {
		return "~" + strings.TrimPrefix(s.CWD, s.User.Home)
	}
	return s.CWD
}

func (s *Shell) RunLoop(r io.Reader) {
	if s.bufrd == nil {
		s.bufrd = bufio.NewReader(r)
	}
	for {
		fmt.Fprint(s.Out, s.PS1())
		line, err := s.readLine()
		if err == io.EOF {
			fmt.Fprintln(s.Out)
			return
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		s.hist = append(s.hist, line)
		s.ExecLine(line)
		if s.exitFlag || s.detach {
			return
		}
	}
}

func (s *Shell) readLine() (string, error) {
	var b []byte
	for {
		c, err := s.bufrd.ReadByte()
		if err != nil {
			if len(b) > 0 {
				return string(b), nil
			}
			return "", err
		}
		// Telnet clients send CRLF. Treat the pair as ONE line ending, or every
		// command is followed by a phantom empty line.
		if s.pendingLF {
			s.pendingLF = false
			if c == '\n' {
				continue
			}
		}
		switch c {
		case '\r':
			s.pendingLF = true
			fmt.Fprint(s.Out, "\r\n")
			return string(b), nil
		case '\n':
			fmt.Fprint(s.Out, "\r\n")
			return string(b), nil
		case 3: // ^C
			fmt.Fprint(s.Out, "^C\r\n")
			b = b[:0]
		case 4: // ^D
			if len(b) == 0 {
				return "", io.EOF
			}
			b = append(b, c)
			fmt.Fprint(s.Out, string(c))
		case 12: // ^L
			fmt.Fprint(s.Out, "\033[H\033[2J")
		case 8, 127:
			if len(b) > 0 {
				b = b[:len(b)-1]
				fmt.Fprint(s.Out, "\b \b")
			}
		default:
			b = append(b, c)
			fmt.Fprint(s.Out, string(c))
		}
	}
}

func (s *Shell) ExecLine(line string) {
	s.exitFlag = false
	for _, seg := range strings.Split(line, "&&") {
		ok := true
		for _, sub := range strings.Split(strings.TrimSpace(seg), ";") {
			sub = strings.TrimSpace(sub)
			if sub == "" {
				continue
			}
			if !s.execOne(sub) {
				ok = false
				break
			}
		}
		if !ok || s.exitFlag || s.detach {
			return
		}
	}
}

func (s *Shell) execOne(cmd string) bool {
	var redirFile string
	var appendMode bool
	rest := cmd
	if i := strings.Index(cmd, ">>"); i >= 0 {
		appendMode = true
		redirFile = strings.TrimSpace(cmd[i+2:])
		rest = strings.TrimSpace(cmd[:i])
	} else if i := strings.Index(cmd, ">"); i >= 0 {
		redirFile = strings.TrimSpace(cmd[i+1:])
		rest = strings.TrimSpace(cmd[:i])
	}
	if strings.TrimSpace(rest) == "" && redirFile == "" {
		return true
	}

	toks := tokenize(rest)
	if len(toks) == 0 {
		return true
	}
	name := toks[0]
	if name == "" {
		return true
	}
	args := toks[1:]

	// A pipeline is a real chain: each stage's stdout feeds the next stage's
	// stdin. Exit status is the LAST stage's, exactly like a real shell.
	if stages := splitPipeline(toks); len(stages) > 1 {
		return s.runPipeline(stages) == 0
	}

	fn, ok := builtinTable[name]
	if !ok {
		// A real executable file in the VFS runs as a controlled script: it is
		// a "virtual binary" whose body is a game-DSL shell script, executed in
		// a restricted child shell. Never host machine code.
		if abs := s.abs(name); abs != "" {
			if n, found := s.Dev.FS.Get(abs); found && n.Mode.Perm()&0111 != 0 {
				if rc, ran := s.runVirtualScript(abs, args); ran {
					return rc == 0
				}
			}
		}
		if !s.commandExists(name) {
			fmt.Fprintf(s.Out, "%s: %s: command not found\r\n", s.Dev.Hostname, name)
			return false
		}
		// virtual binary with no builtin: run it as a no-op that reports itself
		fn = func(sh *Shell, a []string) int {
			fmt.Fprintf(sh.Out, "%s: %s: virtual binary, no game runtime registered\r\n", sh.Dev.Hostname, name)
			return 1
		}
	}

	if redirFile != "" {
		var sb strings.Builder
		sub := &Shell{W: s.W, Dev: s.Dev, User: s.User, CWD: s.CWD, Env: s.Env, Out: &sb,
			bufrd: s.bufrd, InTmux: s.InTmux, srcIP: s.srcIP, TTY: s.TTY, hist: s.hist}
		rc := fn(sub, args)
		if sb.Len() > 0 {
			p := s.abs(redirFile)
			data := []byte(sb.String())
			if appendMode {
				if old, ok := s.Dev.FS.Read(p); ok {
					data = append(old, data...)
				}
			}
			if err := s.Dev.FS.WriteChecked(p, data, s.User); err != nil {
				fmt.Fprintf(s.Out, "%s: %s: cannot write (%v)\r\n", s.Dev.Hostname, redirFile, err)
				return false
			}
		}
		return rc == 0
	}
	rc := fn(s, args)
	return rc == 0
}

func (s *Shell) commandExists(name string) bool {
	if _, ok := builtinTable[name]; ok {
		return true
	}
	// installed virtual binaries
	for _, dir := range []string{"/usr/bin/", "/bin/", "/usr/sbin/", "/sbin/", "/usr/local/bin/"} {
		if n, ok := s.Dev.FS.Get(dir + name); ok && n.Mode.Perm()&0111 != 0 {
			return true
		}
	}
	// BusyBox applets: only the ones the game actually implements, never a
	// name listed just to inflate the command count. OSInfo.Shell is stored
	// bare ("ash"), so accept both spellings — otherwise this gate never fires.
	if core.IsBusyboxShell(s.Dev.OS.Shell) && busyboxHas(name) {
		return true
	}
	return false
}

// bbApplets is the set of BusyBox applets that have a real game implementation.
// Keep this in sync with builtinTable — a name here with no builtin is a lie.
var bbApplets = []string{
	"cat", "cp", "mv", "rm", "mkdir", "ls", "cd", "pwd", "touch", "echo", "ps", "kill",
	"top", "ip", "route", "ifconfig", "nslookup", "wget", "dmesg", "grep", "head", "tail",
	"wc", "df", "free", "uname", "hostname", "uptime", "whoami", "id", "env", "sort",
	"uniq", "date", "killall", "pidof", "sysctl", "logread", "ping", "traceroute",
	"nslookup", "dig", "ss", "systemctl", "service", "reboot", "poweroff", "ifup", "ifdown",
}

var bbSet = map[string]bool{}

func init() {
	for _, b := range bbApplets {
		if _, implemented := builtinTable[b]; !implemented {
			continue // never advertise an applet we cannot actually run
		}
		bbSet[b] = true
	}
}

func busyboxHas(name string) bool { return bbSet[name] }

// ---- pipelines ----

// splitPipeline splits a token list on "|" into stages.
func splitPipeline(toks []string) [][]string {
	var stages [][]string
	cur := []string{}
	for _, t := range toks {
		if t == "|" {
			stages = append(stages, cur)
			cur = []string{}
			continue
		}
		cur = append(cur, t)
	}
	stages = append(stages, cur)
	return stages
}

// runPipeline executes each stage with the previous stage's output as stdin.
// A stage that cannot be found fails the whole pipeline, as in POSIX sh.
func (s *Shell) runPipeline(stages [][]string) int {
	input := ""
	rc := 0
	for i, st := range stages {
		if len(st) == 0 {
			fmt.Fprintf(s.Out, "syntax error near unexpected token `|'\r\n")
			return 2
		}
		name := st[0]
		fn, ok := builtinTable[name]
		if !ok {
			if !s.commandExists(name) {
				fmt.Fprintf(s.Out, "%s: %s: command not found\r\n", s.Dev.Hostname, name)
				return 127
			}
			fmt.Fprintf(s.Out, "%s: %s: virtual binary, no game runtime registered\r\n", s.Dev.Hostname, name)
			return 1
		}
		buf := &strings.Builder{}
		sub := &Shell{W: s.W, Dev: s.Dev, User: s.User, CWD: s.CWD, Env: s.Env, Out: buf,
			bufrd: s.bufrd, InTmux: s.InTmux, srcIP: s.srcIP, TTY: s.TTY, hist: s.hist, Stdin: input}
		rc = fn(sub, st[1:])
		input = buf.String()
		if i == len(stages)-1 {
			fmt.Fprint(s.Out, input)
		}
	}
	return rc
}

// ---- path helpers ----

func (s *Shell) abs(p string) string {
	if p == "" {
		return s.CWD
	}
	if strings.HasPrefix(p, "~") {
		rest := strings.TrimPrefix(p, "~")
		if rest == "" || rest[0] == '/' {
			p = s.User.Home + rest
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(s.CWD, p)
	}
	return path.Clean(p)
}

// ResolveVFS routes paths through mounts; down NFS => honest transport error.
func (s *Shell) ResolveVFS(p string) (*core.VFS, string, string) {
	for _, m := range s.Dev.Mounts {
		if p == m.Dst || strings.HasPrefix(p, m.Dst+"/") {
			srcParts := strings.SplitN(m.Src, ":", 2)
			if len(srcParts) != 2 {
				continue
			}
			d, ok := s.W.Devices[srcParts[0]]
			if !ok {
				return s.Dev.FS, p, ""
			}
			svcName := "nfsd"
			if m.FSTy == "smb" {
				svcName = "smbd"
			}
			if sv := d.Svc(svcName); sv == nil || sv.State != "running" {
				return nil, "", "Stale file handle / transport endpoint is not connected"
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(p, m.Dst), "/")
			return d.FS, path.Join(srcParts[1], rel), "mounted"
		}
	}
	return s.Dev.FS, p, ""
}

func tokenize(cmd string) []string {
	var out []string
	var cur []byte
	var q byte
	esc := false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if esc {
			cur = append(cur, c)
			esc = false
			continue
		}
		switch {
		case c == '\\':
			esc = true
		case q != 0:
			if c == q {
				q = 0
			} else {
				cur = append(cur, c)
			}
		case c == '"' || c == '\'':
			q = c
		case c == ' ' || c == '\t':
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
		default:
			cur = append(cur, c)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func (s *Shell) errf(format string, a ...any) {
	fmt.Fprintf(s.Out, "%s: "+format+"\r\n", append([]any{s.Dev.Hostname}, a...)...)
}

func (s *Shell) printf(format string, a ...any) {
	fmt.Fprintf(s.Out, format, a...)
}

// ReadPasswordLine reads silently-ish from the session input.
func (s *Shell) ReadPasswordLine(prompt string) string {
	fmt.Fprint(s.Out, prompt)
	// A scripted/non-interactive session (cron, `ssh host cmd` from a probe, a
	// bot) may have no input stream at all. Never nil-deref on that.
	if s.bufrd == nil {
		fmt.Fprintln(s.Out)
		return ""
	}
	var b []byte
	for {
		c, err := s.bufrd.ReadByte()
		if err != nil {
			return string(b)
		}
		// same CRLF pairing as readLine: the leftover \n from "user\r\n" must
		// never be mistaken for an empty password
		if s.pendingLF {
			s.pendingLF = false
			if c == '\n' {
				continue
			}
		}
		if c == '\r' {
			s.pendingLF = true
			fmt.Fprint(s.Out, "\r\n")
			return string(b)
		}
		if c == '\n' {
			fmt.Fprint(s.Out, "\r\n")
			return string(b)
		}
		if c == 127 || c == 8 {
			if len(b) > 0 {
				b = b[:len(b)-1]
			}
			continue
		}
		b = append(b, c)
	}
}

var _ = core.Dial
