package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"neohome/internal/core"
)

// tmux / screen — persistent terminal sessions.
//
// A session is not a list of remembered lines: it is a process the device keeps
// running. Its shell keeps executing on the server's clock whether or not anyone
// is attached, which is what makes "start the long job in tmux and log out"
// work — and what makes killing the session really kill the work.
func init() {
	builtinTable["tmux"] = cmdTmux
	builtinTable["screen"] = cmdScreen
}

func cmdTmux(s *Shell, args []string) int {
	if len(args) == 0 {
		return tmuxList(s)
	}
	sub := args[0]
	switch sub {
	case "new", "new-session":
		return tmuxNew(s, args[1:])
	case "ls", "list-sessions":
		return tmuxList(s)
	case "attach", "a":
		return tmuxAttach(s, args[1:])
	case "kill-session", "kill":
		return tmuxKill(s, args[1:])
	case "detach", "d":
		if s.InTmux == "" {
			fmt.Fprintln(s.Out, "not in a session")
			return 1
		}
		fmt.Fprintf(s.Out, "[detached (from %s)]\n", s.InTmux)
		s.InTmux = ""
		return 0
	case "send-keys", "send":
		return tmuxSend(s, args[1:])
	default:
		fmt.Fprintln(s.Out, "usage: tmux new|ls|attach|kill-session|send-keys [name] [cmd...]")
		return 1
	}
}

// tmuxTarget extracts the session name a subcommand targets, understanding the
// real forms: "-t name", "-tname", and a bare positional name.
func tmuxTarget(args []string) (name string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-t" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "-t") && len(a) > 2:
			name = a[2:]
		case strings.HasPrefix(a, "-"):
			// an unrelated flag: ignore it
		default:
			if name == "" {
				name = a
			} else {
				rest = append(rest, a)
			}
		}
	}
	return
}

// tmuxNew creates a session backed by a real process on this device. The session
// survives logout, which is the whole point.
func tmuxNew(s *Shell, args []string) int {
	name := "session"
	var create []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if name == "session" && !strings.Contains(a, " ") && create == nil {
			name = a
			continue
		}
		create = append(create, a)
	}
	if s.Dev.Sessions == nil {
		s.Dev.Sessions = map[string]*core.TermSession{}
	}
	if _, exists := s.Dev.Sessions[name]; exists {
		fmt.Fprintf(s.Out, "duplicate session: %s\n", name)
		return 1
	}
	p := &core.Proc{Name: "tmux: " + name, User: s.User.Name, CPU: 0.1, Mem: 8,
		TTY: "pts/1", State: "S", Start: s.W.Sim, Kind: "user"}
	s.Dev.AddProc(p)
	sess := &core.TermSession{Name: name, CWD: s.CWD, User: s.User.Name, Proc: p, Alive: true}
	s.Dev.Sessions[name] = sess
	s.Dev.Logf("info", "tmux", "session %s created (pid %d)", name, p.PID)

	// run the requested command inside the session, so the work is real
	if len(create) > 0 {
		line := strings.Join(create, " ")
		sess.Lines = append(sess.Lines, "$ "+line)
		sub := s.subShell(sess)
		sub.execOne(line)
		sess.Lines = append(sess.Lines, sub.capture...)
	}
	fmt.Fprintf(s.Out, "[tmux] created session %s (pid %d)\n", name, p.PID)
	return 0
}

func tmuxList(s *Shell) int {
	if len(s.Dev.Sessions) == 0 {
		fmt.Fprintln(s.Out, "no server running on /tmp/tmux-1000/default")
		return 1
	}
	names := make([]string, 0, len(s.Dev.Sessions))
	for n := range s.Dev.Sessions {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sess := s.Dev.Sessions[n]
		state := "attached"
		if !sess.Alive {
			state = "dead"
		}
		windows := 1
		created := "?"
		if sess.Proc != nil {
			created = sess.Proc.Start.Format("15:04:05")
		}
		marker := ""
		if s.InTmux == n {
			marker = " (current)"
		}
		fmt.Fprintf(s.Out, "%s: %d windows (created %s) [%s]%s\n", n, windows, created, state, marker)
	}
	return 0
}

// tmuxAttach reattaches to a session and replays what it has produced — the
// output of work that kept running while the player was away.
func tmuxAttach(s *Shell, args []string) int {
	name, _ := tmuxTarget(args)
	if name == "" {
		name = "session"
	}
	sess, ok := s.Dev.Sessions[name]
	if !ok {
		fmt.Fprintln(s.Out, "no sessions")
		return 1
	}
	if !sess.Alive {
		fmt.Fprintf(s.Out, "session %s is dead\n", name)
		return 1
	}
	for _, l := range sess.Lines {
		fmt.Fprintln(s.Out, l)
	}
	fmt.Fprintf(s.Out, "[tmux] attached to %s\n", name)
	s.InTmux = name
	return 0
}

// tmuxKill really ends the session's process, so a long job run under tmux dies
// with it.
func tmuxKill(s *Shell, args []string) int {
	name, _ := tmuxTarget(args)
	if name == "" {
		name = "session"
	}
	sess, ok := s.Dev.Sessions[name]
	if !ok {
		fmt.Fprintf(s.Out, "session not found: %s\n", name)
		return 1
	}
	if sess.Proc != nil {
		var keep []*core.Proc
		for _, p := range s.Dev.Procs {
			if p.PID != sess.Proc.PID {
				keep = append(keep, p)
			}
		}
		s.Dev.Procs = keep
	}
	sess.Alive = false
	delete(s.Dev.Sessions, name)
	if s.InTmux == name {
		s.InTmux = ""
	}
	fmt.Fprintf(s.Out, "[tmux] killed session %s\n", name)
	return 0
}

// tmuxSend types a command into a detached session: the work runs server-side.
func tmuxSend(s *Shell, args []string) int {
	name, rest := tmuxTarget(args)
	if name == "" || len(rest) == 0 {
		fmt.Fprintln(s.Out, "usage: tmux send-keys [-t session] <command...>")
		return 1
	}
	line := strings.Join(rest, " ")
	sess, ok := s.Dev.Sessions[name]
	if !ok {
		fmt.Fprintf(s.Out, "session not found: %s\n", name)
		return 1
	}
	if !sess.Alive {
		fmt.Fprintf(s.Out, "session %s is dead\n", name)
		return 1
	}
	sess.Lines = append(sess.Lines, "$ "+line)
	sub := s.subShell(sess)
	sub.execOne(line)
	sess.Lines = append(sess.Lines, sub.capture...)
	return 0
}

// subShell builds the shell that runs inside a session. Its output is captured
// into the session so a later attach can show it.
func (s *Shell) subShell(sess *core.TermSession) *Shell {
	return &Shell{
		W: s.W, Dev: s.Dev, User: s.User, CWD: sess.CWD, Env: s.Env,
		Out: &captureWriter{into: &sess.Lines}, bufrd: s.bufrd,
		srcIP: s.srcIP, TTY: s.TTY, hist: s.hist, InTmux: sess.Name,
	}
}

// captureWriter appends whatever a session's shell prints to the session log.
type captureWriter struct{ into *[]string }

func (c *captureWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			*c.into = append(*c.into, line)
		}
	}
	return len(p), nil
}

func cmdScreen(s *Shell, args []string) int {
	// screen's flags differ but the model is the same: -ls lists, -S names a new
	// session, -r reattaches, -X kills.
	if len(args) > 0 {
		switch args[0] {
		case "-ls", "--list":
			return tmuxList(s)
		case "-S":
			if len(args) > 1 {
				return tmuxNew(s, []string{args[1]})
			}
		case "-r":
			if len(args) > 1 {
				return tmuxAttach(s, []string{args[1]})
			}
		case "-X":
			if len(args) > 1 {
				return tmuxKill(s, []string{args[1]})
			}
		}
	}
	return tmuxNew(s, args)
}

var _ = time.Now
