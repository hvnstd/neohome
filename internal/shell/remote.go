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
func cmdSsh(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: ssh [user@]host")
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
	// auth: check credentials on the target
	u := dst.FindUser(user)
	if u == nil {
		// never leak whether the account exists — real sshd does not
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		s.ReadPasswordLine("")
		s.Dev.Fail2Ban[ip]++
		fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
		return 1
	}
	// fail2ban
	if s.Dev.Fail2Ban == nil {
		s.Dev.Fail2Ban = map[string]int{}
	}
	if s.Dev.Fail2Ban[ip] > 3 {
		fmt.Fprintf(s.Out, "ssh: connect to host %s port 22: Connection timed out\n", host)
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
		if pass != u.Pass {
			s.Dev.Fail2Ban[ip]++
			fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
			return 1
		}
	} else {
		// key-based: accept if the target is the assistant node and trusts
		// the player's key (see seed: assistant authorized_keys has alex's key).
		if !s.W.AssistantKeyTrusted(dst) {
			fmt.Fprintf(s.Out, "ssh: connect to host %s port 22: Permission denied (publickey)\n", host)
			return 1
		}
	}
	// success: spawn a nested shell on the target
	fmt.Fprintf(s.Out, "Welcome to %s, %s!\n", dst.Hostname, user)
	nested := NewShell(s.W, dst, u, s.Out, s.srcIP, s.TTY)
	nested.RunLoop(s.bufrd)
	return 0
}

func cmdScp(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: scp SRC DST")
		return 1
	}
	return cmdCp(s, args)
}

func cmdSftp(s *Shell, args []string) int {
	fmt.Fprintln(s.Out, "sftp> use scp for file transfer in this build")
	return 0
}

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
	fmt.Fprintf(s.Out, svc.Banner+"\n")
	nested := NewShell(s.W, dst, dst.FindUser("root"), s.Out, s.srcIP, s.TTY)
	if nested.User == nil {
		nested.User = &core.User{Name: "nobody", UID: 65534, Home: "/nonexistent", Shell: "/usr/sbin/nologin"}
	}
	nested.RunLoop(s.bufrd)
	return 0
}
