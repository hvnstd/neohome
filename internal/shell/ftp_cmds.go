package shell

// ftp — the client half of spec §19's "FTP / SFTP: 真传文件". The server half
// lives in internal/core/ftp.go: a session is opened through Dial, so DNS,
// routing, the router's port-forward, the firewall, power and the daemon's
// service state all gate it, and every byte moves through the *remote*
// account's permissions on the *server's* filesystem.
//
// The session speaks the real dialogue a player would see: the 220 banner, the
// 331/230 login exchange, 150/226 around each transfer and honest 5xx
// refusals. Nothing here prints a reply the server did not produce.

import (
	"fmt"
	"path"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["ftp"] = cmdFtp
}

// ftpTarget parses the argument forms a real client accepts:
// host, host:port, user@host, user@host:port.
func ftpTarget(arg, defUser string) (user, host string, port int) {
	user, port = defUser, 21
	rest := arg
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		user, rest = rest[:at], rest[at+1:]
	}
	host = rest
	if i := strings.LastIndex(rest, ":"); i > 0 {
		if n := atoiSafe(rest[i+1:]); n > 0 {
			host, port = rest[:i], n
		}
	}
	return user, host, port
}

// ftpDial resolves the host from the client's own resolver and opens the
// control connection, printing the same failures ssh does.
func ftpDial(s *Shell, host string, port int) (*core.Device, *core.FTPSession, *core.Service, int) {
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("ftp: %s: Name or service not known (%s)", host, how)
		return nil, nil, nil, 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, port)
	if svc == nil {
		s.errf("ftp: connect to %s port %d: %s", host, port, msg)
		return nil, nil, nil, 1
	}
	// only an FTP daemon speaks this protocol: Dial() found a service on that
	// port, and the handler says whether it is one we can talk to
	if svc.Handler != "ftp-user" && svc.Handler != "npc-ftp" {
		s.errf("ftp: %s:%d is not an FTP endpoint", host, port)
		return nil, nil, nil, 1
	}
	if svc.State != "running" {
		s.errf("ftp: connect to %s port %d: Connection refused", host, port)
		return nil, nil, nil, 1
	}
	if svc.Banner != "" {
		fmt.Fprintf(s.Out, "%s\n", svc.Banner)
	}
	return dst, nil, svc, 0
}

// ftpLogin performs the USER/PASS exchange on an open connection.
func ftpLogin(s *Shell, dst *core.Device, host, user, pass string, anonymous bool) (*core.FTPSession, int) {
	if anonymous {
		user = "anonymous"
		pass = "anonymous@"
	}
	fmt.Fprintf(s.Out, "Name (%s:%s): %s\n", dst.Hostname, s.User.Name, user)
	if !anonymous {
		fmt.Fprint(s.Out, "331 Please specify the password.\n")
		if pass == "" {
			fmt.Fprint(s.Out, "Password: ")
			pass = s.ReadPasswordLine("")
		}
	}
	sess, err := s.W.FTPLogin(s.Dev, s.User.Name, dst, user, pass)
	if err != nil {
		fmt.Fprintf(s.Out, "%v\n", err)
		return nil, 1
	}
	fmt.Fprint(s.Out, "230 Login successful.\n")
	if sess.Anon {
		fmt.Fprintf(s.Out, "Remote system type is UNIX. Anonymous session, root %s.\n", sess.Root)
	} else {
		fmt.Fprintf(s.Out, "Remote system type is UNIX.\n")
	}
	return sess, 0
}

// cmdFtp opens an interactive FTP session. `ftp -A host` logs in anonymously;
// `ftp [user@]host[:port]` prompts for a password like the real client.
func cmdFtp(s *Shell, args []string) int {
	anonymous := false
	var target string
	for _, a := range args {
		switch {
		case a == "-A":
			anonymous = true
		case a == "-p" || a == "-i" || a == "-v":
			// passive mode, prompt-suppression and verbose output are this
			// client's only behaviour, so these are already-default flags
		case strings.HasPrefix(a, "-"):
			s.errf("ftp: unknown option %s", a)
			return 1
		default:
			target = a
		}
	}

	var (
		dst   *core.Device
		sess  *core.FTPSession
		host  string
		pass  string
		user  string
		port  int
		login bool
	)
	if target != "" {
		user, host, port = ftpTarget(target, s.User.Name)
		var rc int
		dst, _, _, rc = ftpDial(s, host, port)
		if rc != 0 {
			return rc
		}
		sess, rc = ftpLogin(s, dst, host, user, pass, anonymous)
		if rc != 0 {
			return rc
		}
		login = true
	} else {
		fmt.Fprintln(s.Out, "ftp> open a host first: `open HOST`, or run `ftp HOST`")
	}

	for {
		fmt.Fprint(s.Out, "ftp> ")
		line, err := s.bufrd.ReadString('\n')
		if err != nil {
			break
		}
		fields := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(fields) == 0 {
			continue
		}
		cmd := fields[0]
		arg := func(i int) string {
			if len(fields) > i {
				return fields[i]
			}
			return ""
		}

		switch cmd {
		case "bye", "quit", "exit":
			if sess != nil {
				sess.Close()
			}
			fmt.Fprintln(s.Out, "221 Goodbye.")
			return 0

		case "open":
			if len(fields) < 2 {
				fmt.Fprintln(s.Out, "usage: open [user@]host[:port]")
				continue
			}
			user, host, port = ftpTarget(fields[1], s.User.Name)
			var rc int
			dst, _, _, rc = ftpDial(s, host, port)
			if rc != 0 {
				continue
			}
			sess, rc = ftpLogin(s, dst, host, user, "", anonymous)
			login = rc == 0

		case "close", "disconnect":
			if sess != nil {
				sess.Close()
			}
			sess, dst, login = nil, nil, false
			fmt.Fprintln(s.Out, "221 Goodbye.")

		case "user":
			if sess == nil {
				fmt.Fprintln(s.Out, "Not connected.")
				continue
			}
			if len(fields) < 2 {
				fmt.Fprintln(s.Out, "usage: user NAME [PASSWORD]")
				continue
			}
			pw := arg(2)
			if pw == "" {
				fmt.Fprint(s.Out, "Password: ")
				pw = s.ReadPasswordLine("")
			}
			// re-authenticate on the open control connection, as FTP allows
			newSess, err := s.W.FTPLogin(s.Dev, s.User.Name, dst, fields[1], pw)
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			sess = newSess
			login = true
			fmt.Fprintln(s.Out, "230 Login successful.")

		case "ls", "dir", "nlist":
			if !s.ftpReady(sess, login) {
				continue
			}
			fmt.Fprintln(s.Out, "150 Here comes the directory listing.")
			entries, err := sess.List(arg(1))
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			for _, e := range entries {
				if e.IsDir {
					fmt.Fprintf(s.Out, "%s/\n", e.Name)
				} else if cmd == "dir" {
					fmt.Fprintf(s.Out, "%-10d %s\n", e.Size, e.Name)
				} else {
					fmt.Fprintf(s.Out, "%s\n", e.Name)
				}
			}
			fmt.Fprintln(s.Out, "226 Directory send OK.")

		case "cd":
			if !s.ftpReady(sess, login) {
				continue
			}
			if _, err := sess.Cwd(arg(1)); err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintln(s.Out, "250 Directory successfully changed.")

		case "pwd":
			if !s.ftpReady(sess, login) {
				continue
			}
			fmt.Fprintf(s.Out, "257 %q is the current directory\n", sess.CWD)

		case "get", "recv":
			if !s.ftpReady(sess, login) {
				continue
			}
			if arg(1) == "" {
				fmt.Fprintln(s.Out, "usage: get REMOTE [LOCAL]")
				continue
			}
			remote, err := sess.Path(arg(1))
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			local := s.abs(arg(2))
			if arg(2) == "" {
				local = s.abs(path.Base(remote))
			}
			fmt.Fprintf(s.Out, "150 Opening BINARY mode data connection for %s.\n", remote)
			data, err := sess.Retr(remote)
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			if err := s.Dev.WriteGuest(local, data, s.User); err != nil {
				fmt.Fprintf(s.Out, "local: %v\n", err)
				continue
			}
			fmt.Fprintln(s.Out, "226 Transfer complete.")
			fmt.Fprintf(s.Out, "%d bytes received -> %s\n", len(data), local)

		case "put", "send":
			if !s.ftpReady(sess, login) {
				continue
			}
			if arg(1) == "" {
				fmt.Fprintln(s.Out, "usage: put LOCAL [REMOTE]")
				continue
			}
			local := s.abs(arg(1))
			data, exists, allowed := s.Dev.FS.ReadPathAs(local, s.User)
			if !exists {
				fmt.Fprintf(s.Out, "local: %s: No such file or directory\n", arg(1))
				continue
			}
			if !allowed {
				s.noteDenied(local, "read")
				fmt.Fprintf(s.Out, "local: %s: Permission denied\n", arg(1))
				continue
			}
			remote := arg(2)
			if remote == "" {
				remote = path.Base(local)
			}
			remote, err := sess.Path(remote)
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintf(s.Out, "150 Ok to send data.\n")
			if err := sess.Stor(remote, data); err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintln(s.Out, "226 Transfer complete.")
			fmt.Fprintf(s.Out, "%d bytes sent -> %s\n", len(data), remote)

		case "mkdir":
			if !s.ftpReady(sess, login) {
				continue
			}
			if arg(1) == "" {
				fmt.Fprintln(s.Out, "usage: mkdir PATH")
				continue
			}
			if err := sess.Mkd(arg(1)); err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintf(s.Out, "257 %q created\n", arg(1))

		case "delete", "del":
			if !s.ftpReady(sess, login) {
				continue
			}
			if arg(1) == "" {
				fmt.Fprintln(s.Out, "usage: delete PATH")
				continue
			}
			if err := sess.Dele(arg(1)); err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintln(s.Out, "250 Delete operation successful.")

		case "size":
			if !s.ftpReady(sess, login) {
				continue
			}
			n, err := sess.Size(arg(1))
			if err != nil {
				fmt.Fprintf(s.Out, "%v\n", err)
				continue
			}
			fmt.Fprintf(s.Out, "213 %d\n", n)

		case "lcd":
			if arg(1) != "" {
				if !s.Dev.FS.IsDir(s.abs(arg(1))) {
					fmt.Fprintf(s.Out, "local: %s: No such file or directory\n", arg(1))
					continue
				}
				s.CWD = s.abs(arg(1))
			}
			fmt.Fprintf(s.Out, "Local directory now %s\n", s.CWD)

		case "lpwd", "!pwd":
			fmt.Fprintf(s.Out, "Local directory %s\n", s.CWD)

		case "help", "?":
			s.ftpHelp()

		default:
			fmt.Fprintf(s.Out, "? Invalid command\n")
		}
	}
	if sess != nil {
		sess.Close()
	}
	return 0
}

func (s *Shell) ftpReady(sess *core.FTPSession, login bool) bool {
	if sess == nil || !login {
		fmt.Fprintln(s.Out, "Not connected.")
		return false
	}
	if err := sess.Closed(); err != nil {
		fmt.Fprintf(s.Out, "%v\n", err)
		return false
	}
	return true
}

func (s *Shell) ftpHelp() {
	fmt.Fprint(s.Out, `Commands:
  open [user@]host[:port]   connect   close   bye        end the session
  user NAME [PASS]          re-authenticate on this connection
  ls [PATH]   dir [PATH]    remote listing   cd PATH   pwd
  get REMOTE [LOCAL]        download      put LOCAL [REMOTE]   upload
  mkdir PATH   delete PATH   size PATH
  lcd PATH    lpwd          local side
  help                      this list
`)
}
