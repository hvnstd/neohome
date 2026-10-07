package shell

// sftp and scp — file transfer over the SSH channel. Both authenticate the
// same way cmdSsh does (account records, password, fail2ban, the
// sshd_config gate), then move bytes between two real filesystems: every
// read is the remote account's read, every write the destination account's
// write, and both ends log the session.

import (
	"fmt"
	"path"
	"strings"

	"neohome/internal/core"
)

// scpPath splits an scp argument into its local or remote form:
//
//	file             → local
//	[user@]host:path → remote
func scpPath(s *Shell, arg string) (user, host, p string, remote bool) {
	i := strings.Index(arg, ":")
	if i < 0 {
		return "", "", arg, false
	}
	rest := arg[:i]
	p = arg[i+1:]
	user = s.User.Name
	if at := strings.Index(rest, "@"); at >= 0 {
		user = rest[:at]
		host = rest[at+1:]
	} else {
		host = rest
	}
	return user, host, p, true
}

// sshLogin performs the authentication cmdSsh performs, for the transfer
// tools: the account must exist on the target, the password must match
// (unless the target disabled password auth), and failures feed fail2ban
// and never say whether the account or the password was wrong.
func sshLogin(s *Shell, user, host, ip string, dst *core.Device) (*core.User, int) {
	u := dst.FindUser(user)
	if u == nil {
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		s.ReadPasswordLine("")
		dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "sftp password for "+user)
		fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
		return nil, 1
	}
	allowPass := true
	if data, has := dst.FS.Read("/etc/ssh/sshd_config"); has {
		if strings.Contains(string(data), "PasswordAuthentication no") {
			allowPass = false
		}
	}
	if allowPass {
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		pass := s.ReadPasswordLine("")
		if !u.CheckPassword(pass) {
			dst.NoteAuthFail(s.Dev.SourceIPFor(dst), s.Dev.Hostname, "sftp password for "+user)
			fmt.Fprintf(s.Out, "Permission denied, please try again.\n")
			return nil, 1
		}
	} else if !s.W.AssistantKeyTrusted(s.Dev, dst) {
		fmt.Fprintf(s.Out, "%s: connect to host %s port 22: Permission denied (publickey)\n", "ssh", host)
		return nil, 1
	}
	return u, 0
}

// dialSSH resolves and connects to the target's sshd, the gate every
// remote session passes through.
func dialSSH(s *Shell, host string) (string, *core.Device, *core.Service, int) {
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("ssh: Could not resolve hostname %s: %s", host, how)
		return "", nil, nil, 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 22)
	if svc == nil {
		s.errf("ssh: connect to host %s port 22: %s", host, msg)
		return "", nil, nil, 1
	}
	if svc.Handler != "ssh" {
		s.errf("ssh: %s:%d is not an ssh endpoint", host, 22)
		return "", nil, nil, 1
	}
	return ip, dst, svc, 0
}

// cmdSftp opens a file-transfer session: no shell on the remote, just the
// filesystem — which is exactly what sftp is.
func cmdSftp(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: sftp [user@]host")
		return 1
	}
	user, host := s.User.Name, args[0]
	if i := strings.Index(args[0], "@"); i >= 0 {
		user, host = args[0][:i], args[0][i+1:]
	}
	ip, dst, _, rc := dialSSH(s, host)
	if rc != 0 {
		return rc
	}
	ru, rc := sshLogin(s, user, host, ip, dst)
	if rc != 0 {
		return rc
	}
	dst.Logf("info", "sshd", "sftp session opened for %s from %s", ru.Name, s.Dev.Hostname)
	fmt.Fprintf(s.Out, "Connected to %s.\n", host)

	cwd := ru.Home
	for {
		fmt.Fprint(s.Out, "sftp> ")
		line, err := s.bufrd.ReadString('\n')
		if err != nil {
			break
		}
		fields := strings.Fields(strings.TrimRight(line, "\r\n"))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "quit", "exit", "bye":
			fmt.Fprintf(s.Out, "quit: connection closed\n")
			return 0
		case "pwd":
			fmt.Fprintf(s.Out, "Remote working directory: %s\n", cwd)
		case "lpwd":
			fmt.Fprintf(s.Out, "Local working directory: %s\n", s.CWD)
		case "cd":
			if len(fields) < 2 {
				fmt.Fprintf(s.Out, "usage: cd <path>\n")
				continue
			}
			target := core.SFTPCleanPath(cwd, fields[1])
			if !dst.FS.IsDir(target) {
				fmt.Fprintf(s.Out, "%s: no such file or directory\n", target)
				continue
			}
			cwd = target
		case "ls":
			dir := cwd
			for _, a := range fields[1:] {
				if !strings.HasPrefix(a, "-") {
					dir = core.SFTPCleanPath(cwd, a)
				}
			}
			entries, err := s.W.SFTPList(dst, ru, dir)
			if err != nil {
				fmt.Fprintf(s.Out, "%s\n", err)
				continue
			}
			for _, e := range entries {
				if e.IsDir {
					fmt.Fprintf(s.Out, "%s/\n", e.Name)
				} else {
					fmt.Fprintf(s.Out, "%s\n", e.Name)
				}
			}
		case "lls":
			dir := s.CWD
			for _, a := range fields[1:] {
				if !strings.HasPrefix(a, "-") {
					dir = s.abs(a)
				}
			}
			for _, p := range s.Dev.FS.List(dir) {
				name := path.Base(p)
				if s.Dev.FS.IsDir(p) {
					fmt.Fprintf(s.Out, "%s/\n", name)
				} else {
					fmt.Fprintf(s.Out, "%s\n", name)
				}
			}
		case "get":
			if len(fields) < 2 {
				fmt.Fprintf(s.Out, "usage: get [-r] <remote> [local]\n")
				continue
			}
			rest := fields[1:]
			recursive := false
			if len(rest) > 0 && rest[0] == "-r" {
				recursive, rest = true, rest[1:]
			}
			if len(rest) < 1 {
				fmt.Fprintf(s.Out, "usage: get [-r] <remote> [local]\n")
				continue
			}
			remote := core.SFTPCleanPath(cwd, rest[0])
			local := s.CWD + "/" + path.Base(remote)
			if len(rest) > 1 {
				local = s.abs(rest[1])
			}
			if !recursive {
				if err := s.W.SFTPGet(s.Dev, s.User, dst, ru, remote, local); err != nil {
					fmt.Fprintf(s.Out, "%s\n", err)
					continue
				}
				fmt.Fprintf(s.Out, "Fetched %s -> %s\n", remote, local)
				continue
			}
			n, b, err := s.sftpGetTree(dst, ru, remote, local)
			if err != nil {
				fmt.Fprintf(s.Out, "%s\n", err)
				continue
			}
			fmt.Fprintf(s.Out, "Fetched %d file(s), %d bytes\n", n, b)
		case "put":
			if len(fields) < 2 {
				fmt.Fprintf(s.Out, "usage: put [-r] <local> [remote]\n")
				continue
			}
			rest := fields[1:]
			recursive := false
			if len(rest) > 0 && rest[0] == "-r" {
				recursive, rest = true, rest[1:]
			}
			if len(rest) < 1 {
				fmt.Fprintf(s.Out, "usage: put [-r] <local> [remote]\n")
				continue
			}
			local := s.abs(rest[0])
			remote := cwd + "/" + path.Base(local)
			if len(rest) > 1 {
				remote = core.SFTPCleanPath(cwd, rest[1])
			}
			if !recursive {
				if err := s.W.SFTPPut(s.Dev, s.User, dst, ru, local, remote); err != nil {
					fmt.Fprintf(s.Out, "%s\n", err)
					continue
				}
				fmt.Fprintf(s.Out, "Stored %s -> %s\n", local, remote)
				continue
			}
			n, b, err := s.sftpPutTree(dst, ru, local, remote)
			if err != nil {
				fmt.Fprintf(s.Out, "%s\n", err)
				continue
			}
			fmt.Fprintf(s.Out, "Stored %d file(s), %d bytes\n", n, b)
		default:
			fmt.Fprintf(s.Out, "? Invalid command\n")
		}
	}
	return 0
}

// sftpGetTree fetches a remote tree: directories are created locally, files
// come through the same single-file gate (same permission checks, same
// evidence). A lone file behaves exactly like a plain get.
func (s *Shell) sftpGetTree(dst *core.Device, ru *core.User, remote, local string) (int, int, error) {
	remote = path.Clean(remote)
	entries, err := s.W.SFTPList(dst, ru, remote)
	if err != nil {
		if err := s.W.SFTPGet(s.Dev, s.User, dst, ru, remote, local); err != nil {
			return 0, 0, err
		}
		if data, ok := s.Dev.FS.Read(local); ok {
			return 1, len(data), nil
		}
		return 1, 0, nil
	}
	if err := s.Dev.FS.MkdirAllChecked(local, 0755, s.User); err != nil {
		return 0, 0, fmt.Errorf("%s: %v", local, err)
	}
	n, b := 0, 0
	for _, e := range entries {
		cn, cb, err := s.sftpGetTree(dst, ru, remote+"/"+e.Name, local+"/"+e.Name)
		if err != nil {
			return n, b, err
		}
		n += cn
		b += cb
	}
	return n, b, nil
}

// sftpPutTree stores a local tree on the remote: directories are created
// through SFTPMkdir, files through SFTPPut. An unreadable local directory
// fails fast instead of silently skipping its contents.
func (s *Shell) sftpPutTree(dst *core.Device, ru *core.User, local, remote string) (int, int, error) {
	local = path.Clean(local)
	if !s.Dev.FS.IsDir(local) {
		if err := s.W.SFTPPut(s.Dev, s.User, dst, ru, local, remote); err != nil {
			return 0, 0, err
		}
		if data, ok := s.Dev.FS.Read(local); ok {
			return 1, len(data), nil
		}
		return 1, 0, nil
	}
	if !s.Dev.FS.CanRead(local, s.User) {
		return 0, 0, fmt.Errorf("%s: permission denied", local)
	}
	if err := s.W.SFTPMkdir(dst, ru, remote); err != nil {
		// the directory may already exist from an earlier run: only a real
		// refusal stops the transfer
		if !dst.FS.IsDir(remote) {
			return 0, 0, err
		}
	}
	n, b := 0, 0
	for _, p := range s.Dev.FS.List(local) {
		name := path.Base(p)
		cn, cb, err := s.sftpPutTree(dst, ru, p, remote+"/"+name)
		if err != nil {
			return n, b, err
		}
		n += cn
		b += cb
	}
	return n, b, nil
}

// cmdScp copies between two filesystems: local-to-local stays cp, one
// remote endpoint makes it a real authenticated transfer.
func cmdScp(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: scp SRC DST   (either side may be [user@]host:path)")
		return 1
	}
	su, shost, spath, sremote := scpPath(s, args[0])
	du, dhost, dpath, dremote := scpPath(s, args[1])

	if !sremote && !dremote {
		return cmdCp(s, args)
	}
	if sremote && dremote {
		s.errf("scp: copying between two remote hosts is not supported")
		return 1
	}

	host, user := shost, su
	if dremote {
		host, user = dhost, du
	}
	ip, dst, _, rc := dialSSH(s, host)
	if rc != 0 {
		return rc
	}
	ru, rc := sshLogin(s, user, host, ip, dst)
	if rc != 0 {
		return rc
	}

	if sremote {
		remote := path.Clean(spath)
		local := s.abs(dpath)
		if s.Dev.FS.IsDir(local) {
			local = local + "/" + path.Base(remote)
		}
		if err := s.W.SFTPGet(s.Dev, s.User, dst, ru, remote, local); err != nil {
			s.errf("scp: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "%s -> %s\n", remote, local)
		return 0
	}
	local := s.abs(spath)
	remote := path.Clean(dpath)
	if dst.FS.IsDir(remote) {
		remote = remote + "/" + path.Base(local)
	}
	if err := s.W.SFTPPut(s.Dev, s.User, dst, ru, local, remote); err != nil {
		s.errf("scp: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "%s -> %s\n", local, remote)
	return 0
}
