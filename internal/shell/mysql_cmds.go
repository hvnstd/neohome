package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// mysql speaks to a MariaDB server: locally through the running daemon,
// remotely over TCP/3306 through the same Dial every other client uses.
// Access is the device's own accounts (prompted where there is no socket
// trust) plus per-database ownership (creator or root) — the same rule ssh,
// sftp and imap already enforce, so there is no second credential store.

func init() {
	builtinTable["mysql"] = cmdMysql
}

func cmdMysql(s *Shell, args []string) int {
	if s.pkgCommandMissing("mysql") {
		s.errf("mysql: command not found (install mariadb first)")
		return 1
	}
	host, user, pass, batch, db := "", "", "", "", ""
	needPass := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--host":
			if i+1 >= len(args) {
				s.errf("usage: mysql [-h HOST] [-u USER [-p]] [-e SQL] [DB]")
				return 1
			}
			i++
			host = args[i]
		case "-u", "--user":
			if i+1 >= len(args) {
				s.errf("usage: mysql [-h HOST] [-u USER [-p]] [-e SQL] [DB]")
				return 1
			}
			i++
			user = args[i]
		case "-p", "--password":
			needPass = true
		case "-e", "--execute":
			if i+1 >= len(args) {
				s.errf("usage: mysql [-h HOST] [-u USER [-p]] [-e SQL] [DB]")
				return 1
			}
			i++
			batch = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				s.errf("mysql: unknown option '%s'", args[i])
				return 1
			}
			if db != "" {
				s.errf("usage: mysql [-h HOST] [-u USER [-p]] [-e SQL] [DB]")
				return 1
			}
			db = args[i]
		}
	}
	if user == "" {
		user = s.User.Name
	}
	if host == "" {
		return mysqlLocal(s, user, pass, needPass, batch, db)
	}
	return mysqlRemote(s, host, user, pass, needPass, batch, db)
}

// mysqlLocal runs against this machine's own daemon: no daemon, no database.
func mysqlLocal(s *Shell, user, pass string, needPass bool, batch, db string) int {
	if svc := s.Dev.Svc("mariadb"); svc == nil || svc.State != "running" {
		s.errf("mysql: can't connect to local server (mariadb is not running here)")
		return 1
	}
	u := s.Dev.FindUser(user)
	if u == nil {
		s.errf("mysql: unknown user %q", user)
		return 1
	}
	// socket trust covers the session's own account; anyone else proves it
	if user != s.User.Name || needPass {
		if pass == "" {
			fmt.Fprintf(s.Out, "Enter password for %s: ", user)
			pass = s.ReadPasswordLine("")
		}
		if !u.CheckPassword(pass) {
			s.Dev.NoteAuthFail("local", s.User.Name, "mysql password for "+user)
			s.errf("mysql: Access denied for user '%s'@'localhost'", user)
			return 1
		}
	}
	return mysqlSession(s, s.Dev, u, batch, db, "localhost")
}

// mysqlRemote runs against another machine's daemon: resolved, dialed,
// firewall-judged and password-proved like every other remote client.
func mysqlRemote(s *Shell, host, user, pass string, needPass bool, batch, db string) int {
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("mysql: resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 3306)
	if svc == nil {
		s.errf("mysql: connect to %s (%s): %s", host, ip, msg)
		return 1
	}
	u := dst.FindUser(user)
	if u == nil {
		fmt.Fprintf(s.Out, "Enter password for %s: ", user)
		s.ReadPasswordLine("")
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "mysql password for "+user)
		s.errf("mysql: Access denied for user '%s'@'%s'", user, host)
		return 1
	}
	if pass == "" {
		fmt.Fprintf(s.Out, "Enter password for %s: ", user)
		pass = s.ReadPasswordLine("")
	}
	_ = needPass
	if !u.CheckPassword(pass) {
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "mysql password for "+user)
		s.errf("mysql: Access denied for user '%s'@'%s'", user, host)
		return 1
	}
	dst.Logf("info", "mariadb", "%s connected from %s", user, s.Dev.Hostname)
	return mysqlSessionRemote(s, dst, u, batch, db, host)
}

// mysqlSession runs statements: one -e batch, or a REPL reading lines until
// quit. USE persists across lines of one session, like a real client.
func mysqlSession(s *Shell, d *core.Device, u *core.User, batch, db, where string) int {
	if batch != "" {
		out, _, err := core.MysqlExec(d, u, db, batch)
		fmt.Fprint(s.Out, out)
		if err != nil {
			fmt.Fprintf(s.Out, "%v\n", err)
			return 1
		}
		return 0
	}
	if s.bufrd == nil {
		s.errf("usage: mysql [-e SQL] [DB] (or run interactively with a terminal)")
		return 1
	}
	cur := db
	fmt.Fprintf(s.Out, "Welcome to the MariaDB monitor. Connected to %s.\n", where)
	for {
		fmt.Fprint(s.Out, "MariaDB> ")
		line, err := s.bufrd.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		low := strings.ToLower(strings.TrimSpace(line))
		if low == "quit" || low == "exit" || low == "\\q" {
			break
		}
		out, next, err := core.MysqlExec(d, u, cur, line)
		cur = next
		fmt.Fprint(s.Out, out)
		if err != nil {
			fmt.Fprintf(s.Out, "%v\n", err)
		}
	}
	fmt.Fprintln(s.Out, "Bye")
	return 0
}

// mysqlSessionRemote is the same session against another machine's daemon.
// Split out so the connect logging above stays with the transport.
func mysqlSessionRemote(s *Shell, dst *core.Device, u *core.User, batch, db, host string) int {
	return mysqlSession(s, dst, u, batch, db, host)
}
