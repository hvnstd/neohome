package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// browse is the web terminal's terminal side: it resolves the host, dials
// the webd port, then speaks the one-shot HTTP protocol a browser would —
// POST /session for a token, GET /query?t=…&c=… for one command each. No
// persistent reader, no job control: that honesty is what separates this
// from a second ssh.
//
// Nothing here bypasses the world: the port is gated by the packet path
// like every other, the credentials are the device's real accounts, every
// command runs through the same builtins and permission checks, and the
// sessions show up in who/syslog/history exactly like a login should.

func init() {
	builtinTable["browse"] = cmdBrowse
	// the web terminal's line runner: satisfied here so core never imports
	// shell (the same inversion cron uses)
	core.SetWebExec(func(w *core.World, d *core.Device, u *core.User, line string) (string, int) {
		var b strings.Builder
		out, rc := runAs(w, d, u, line)
		b.WriteString(out)
		return b.String(), rc
	})
}

// runAs executes one line as a specific account on a device: the same
// builtins, permissions and history as a live session, into a buffer.
func runAs(w *core.World, d *core.Device, u *core.User, line string) (string, int) {
	out := &strings.Builder{}
	sh := &Shell{W: w, Dev: d, User: u, CWD: u.Home, Env: map[string]string{
		"HOME": u.Home, "USER": u.Name, "SHELL": u.Shell, "TERM": "xterm",
		"PWD": u.Home, "HOSTNAME": d.Hostname,
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}, Out: out, srcIP: d.SourceIPFor(d), TTY: "web"}
	rc := sh.ExecLineStatus(line)
	return out.String(), rc
}

func cmdBrowse(s *Shell, args []string) int {
	host := ""
	user := s.User.Name
	var cmds []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c":
			if i+1 >= len(args) {
				s.errf("usage: browse [user@]HOST [-c COMMAND]...")
				return 1
			}
			i++
			cmds = append(cmds, args[i])
		default:
			if strings.HasPrefix(args[i], "-") {
				s.errf("usage: browse [user@]HOST [-c COMMAND]...")
				return 1
			}
			if host != "" {
				s.errf("usage: browse [user@]HOST [-c COMMAND]...")
				return 1
			}
			t := args[i]
			if at := strings.Index(t, "@"); at >= 0 {
				user, host = t[:at], t[at+1:]
			} else {
				host = t
			}
		}
	}
	if host == "" {
		s.errf("usage: browse [user@]HOST [-c COMMAND]...")
		return 1
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("browse: resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, core.WebPort)
	if svc == nil {
		s.errf("browse: connect %s:%d: %s", host, core.WebPort, msg)
		return 1
	}
	if dst.Profile == "usb" {
		s.errf("browse: %s is storage, not a server", host)
		return 1
	}
	fmt.Fprintf(s.Out, "%s\n", s.W.WebLanding())
	pass := ""
	fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
	pass = s.ReadPasswordLine("")
	sess, err := s.W.WebOpen(dst, user, pass)
	if err != nil {
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), dst.Hostname, "web password for "+user)
		s.errf("browse: %s: %v", host, err)
		return 1
	}
	fmt.Fprintf(s.Out, "session %s open (web login for %s on %s)\n", sess.Token, user, dst.Hostname)
	run := func(line string) {
		fmt.Fprintf(s.Out, "$ %s\n%s", line, s.W.WebQuery(sess, line))
	}
	if len(cmds) == 0 {
		if s.bufrd == nil {
			s.errf("browse: no terminal on this session (use -c COMMAND)")
			return 1
		}
		for {
			fmt.Fprint(s.Out, "web> ")
			l, err := s.bufrd.ReadString('\n')
			if err != nil {
				break
			}
			l = strings.TrimRight(l, "\r\n")
			if l == "" {
				continue
			}
			if l == "quit" || l == "exit" {
				break
			}
			run(l)
		}
	} else {
		for _, c := range cmds {
			run(c)
		}
	}
	s.W.WebClose(sess.Token)
	fmt.Fprintln(s.Out, "session closed")
	return 0
}
