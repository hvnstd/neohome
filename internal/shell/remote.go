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
		{"ssh", cmdSsh}, {"scp", cmdScp}, {"sftp", cmdSftp},
		{"telnet", cmdTelnet},
	} {
		builtinTable[e.name] = e.fn
	}
}

// cmdSsh: connect to a remote device and drive a shell on it.
//
// Two real ssh shapes are supported: `ssh [user@]host` opens an interactive
// session on the target, and `ssh [user@]host command` runs that one command
// there and returns its status. The command form is what a session without a
// terminal (a script, a cron job, a test) must use — a nested loop with no
// input has nothing to read.
func cmdSsh(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: ssh [user@]host [command]")
		return 1
	}
	target := args[0]
	user := s.User.Name
	host := target
	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		user = parts[0]
		host = parts[1]
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("ssh: resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 22)
	if svc == nil {
		s.errf("ssh: connect to %s (%s): %s", host, ip, msg)
		return 1
	}
	if svc.State != "running" {
		s.errf("ssh: connect to %s (%s): Connection refused", host, ip)
		return 1
	}
	// auth: check credentials on the target. §33: a rejection is recorded by
	// the *target* — that is the evidence a fail2ban jail counts and an IDS
	// alerts on. Whether a brute force is cut off is the target's decision
	// (its own jail configuration), never the client's.
	u := dst.FindUser(user)
	if u == nil {
		// never leak whether the account exists — real sshd does not
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		s.ReadPasswordLine("")
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "ssh password for "+user)
		fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
		return 1
	}
	// password auth (only if the service allows it)
	allowPass := true
	if data, has := dst.FS.Read("/etc/ssh/sshd_config"); has {
		if strings.Contains(string(data), "PasswordAuthentication no") {
			allowPass = false
		}
	}
	if allowPass {
		fmt.Fprintf(s.Out, "ssh: connect to host %s port 22: %s\n", host, svc.Banner)
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		pass := s.ReadPasswordLine("")
		if !u.CheckPassword(pass) {
			dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "ssh password for "+user)
			fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
			return 1
		}
	} else {
		// key-based: accept if the target is the assistant node and trusts
		// the player's key (see seed: assistant authorized_keys has alex's key).
		if !s.W.AssistantKeyTrusted(s.Dev, dst) {
			fmt.Fprintf(s.Out, "ssh: connect to host %s port 22: Permission denied (publickey)\n", host)
			return 1
		}
	}
	// success
	fmt.Fprintf(s.Out, "Welcome to %s, %s!\n", dst.Hostname, user)
	return s.openRemoteSession("ssh", dst, u, args[1:])
}

// openRemoteSession is the far side of ssh/telnet: with a command it runs that
// command on the target and returns its status; without one it hands this
// terminal to a shell on the target. The nested shell shares the session's
// buffered reader instead of wrapping it again — a second bufio.Reader over
// the same input would starve on bytes the outer reader already holds.
func (s *Shell) openRemoteSession(name string, dst *core.Device, u *core.User, cmd []string) int {
	nested := NewShell(s.W, dst, u, s.Out, s.srcIP, s.TTY)
	if len(cmd) > 0 {
		return nested.ExecLineStatus(strings.Join(cmd, " "))
	}
	if s.bufrd == nil {
		s.errf("%s: no interactive terminal on this session (try: %s %s@%s COMMAND)",
			name, name, u.Name, dst.Hostname)
		return 1
	}
	nested.bufrd = s.bufrd
	nested.RunLoop(nil)
	return 0
}

// cmdScp and cmdSftp live in sftp_cmds.go: real authenticated transfers
// over the same channel cmdSsh opens.

func cmdTelnet(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: telnet HOST")
		return 1
	}
	host := args[0]
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("telnet: resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 23)
	if svc == nil {
		s.errf("telnet: connect to %s: %s", host, msg)
		return 1
	}
	// A telnet endpoint asks for an account and password like any other login.
	// It used to hand out a root shell with no authentication at all, which made
	// the whole privilege model moot: any attacker could simply telnet to the
	// target and be root. Real telnetd is exactly where weak credentials matter.
	if svc.State != "running" {
		s.errf("telnet: connect to %s: Connection refused", host)
		return 1
	}
	fmt.Fprintf(s.Out, svc.Banner+"\n")
	// telnetd may present its own login banner/account prompt
	loginUser := "root"
	if svc.TelnetUser != "" {
		loginUser = svc.TelnetUser
	}
	fmt.Fprintf(s.Out, "%s login: ", dst.Hostname)
	entered := strings.TrimSpace(s.ReadPasswordLine(""))
	if entered != "" {
		loginUser = entered
	}
	u := dst.FindUser(loginUser)
	// never reveal whether the account exists, and never let a service account
	// with no stored password authenticate by pressing enter
	fmt.Fprint(s.Out, "Password: ")
	pass := s.ReadPasswordLine("")
	if !u.CheckPassword(pass) {
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "telnet password for "+loginUser)
		s.W.Record("auth", s.User.Name, s.srcIP, dst.ID,
			"failed telnet login as "+loginUser, 3)
		fmt.Fprintf(s.Out, "Login incorrect\n")
		return 1
	}
	s.W.Record("auth", s.User.Name, s.srcIP, dst.ID,
		"telnet login "+loginUser+"@"+dst.Hostname, 1)
	fmt.Fprintf(s.Out, "Welcome to %s (%s)\n", dst.Hostname, dst.OS.Distro)
	return s.openRemoteSession("telnet", dst, u, nil)
}
