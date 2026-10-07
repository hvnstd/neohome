package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"su", cmdSu}, {"sudo", cmdSudo}, {"passwd", cmdPasswd},
		{"useradd", cmdUseradd}, {"userdel", cmdUserdel}, {"usermod", cmdUsermod},
		{"groupadd", cmdGroupadd}, {"groups", cmdGroups}, {"chpasswd", cmdChpasswd},
		{"ssh-keygen", cmdKeygen},
		{"fastfetch", cmdFastfetch}, {"neofetch", cmdFastfetch},
		{"who", cmdWho}, {"w", cmdWho}, {"clear", cmdClear}, {"history", cmdHistory},
		{"exit", cmdExit}, {"logout", cmdExit},
	} {
		builtinTable[e.name] = e.fn
	}
}

// tmux / screen: real background sessions surviving SSH disconnect.
// They are stored on the DEVICE (not the shell) so they outlive the session.

func cmdSu(s *Shell, args []string) int {
	target := "root"
	if len(args) > 0 {
		target = args[0]
	}
	u := s.Dev.FindUser(target)
	if u == nil {
		s.errf("su: unknown user %s", target)
		return 1
	}
	if u.UID == s.User.UID {
		s.User = u
		return 0
	}
	if s.User.UID != 0 {
		if !u.IsBot && u.Pass == "" {
			s.errf("su: Authentication failure")
			return 1
		}
		fmt.Fprintf(s.Out, "Password: ")
		pw := s.ReadPasswordLine("")
		if !s.verifyPassword(u, pw) {
			s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"failed su to "+u.Name, 3)
			s.errf("su: Authentication failure")
			return 1
		}
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"su "+s.User.Name+" -> "+u.Name, 1)
	s.User = u
	s.CWD = u.Home
	if s.CWD == "" {
		s.CWD = "/"
	}
	return 0
}

func cmdSudo(s *Shell, args []string) int {
	// sudo as root: just run the command as root
	if len(args) == 0 {
		s.errf("usage: sudo COMMAND")
		return 1
	}
	if !s.hasSudo() && s.User.UID != 0 {
		s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
			"sudo denied (not in sudoers)", 3)
		s.errf("sudo: %s is not in the sudoers file.  This incident will be reported.", s.User.Name)
		return 1
	}
	// sudo authenticates the INVOKING user with its own password
	if s.User.UID != 0 {
		fmt.Fprintf(s.Out, "[sudo] password for %s: ", s.User.Name)
		pw := s.ReadPasswordLine("")
		if !s.verifyPassword(s.User, pw) {
			s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"failed sudo authentication", 3)
			s.errf("Sorry, try again.")
			return 1
		}
	}
	root := s.Dev.FindUser("root")
	if root == nil {
		// uid 0 exists on every Unix even where /etc/passwd has no root line
		// (the world's laptop image is one of those). Falling back to the
		// invoking account — which is what used to happen here — made sudo
		// print the prompt and then run the command unprivileged, which is a
		// lie a player would only discover by being refused later.
		root = &core.User{Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"}
	}
	sub := &Shell{W: s.W, Dev: s.Dev, User: root, CWD: s.CWD, Env: s.Env, Out: s.Out,
		bufrd: s.bufrd, InTmux: s.InTmux, srcIP: s.srcIP, TTY: s.TTY, hist: s.hist}
	if sub.execOne(strings.Join(args, " ")) {
		return 0
	}
	return 1
}

func (s *Shell) hasSudo() bool {
	for _, g := range s.User.Groups {
		if g == "sudo" {
			return true
		}
	}
	return false
}

// Account management: useradd/userdel/usermod/groupadd/groups. All mutating
// verbs need root; the state moves through the Device mutators (users.go) so
// the table and /etc/passwd+/etc/shadow+/etc/sudoers+/etc/group move
// together. New accounts arrive locked (no password) and are unlocked with
// `passwd` — the same two-step as a real box. Home directories always come
// along, the way this world's seeds have always behaved (useradd -m).
func cmdUseradd(s *Shell, args []string) int {
	if s.User.UID != 0 {
		s.errf("useradd: Permission denied (must be root)")
		return 1
	}
	var extra []string
	shell := ""
	var names []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-G", "--groups":
			if i+1 >= len(args) {
				s.errf("usage: useradd [-G GROUPS] [-s SHELL] USER")
				return 1
			}
			i++
			extra = append(extra, splitCSV(args[i])...)
		case "-s", "--shell":
			if i+1 >= len(args) {
				s.errf("usage: useradd [-G GROUPS] [-s SHELL] USER")
				return 1
			}
			i++
			shell = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				s.errf("useradd: unknown option '%s'", args[i])
				return 1
			}
			names = append(names, args[i])
		}
	}
	if len(names) != 1 {
		s.errf("usage: useradd [-G GROUPS] [-s SHELL] USER")
		return 1
	}
	u, err := s.Dev.AddUser(names[0], extra, shell)
	if err != nil {
		s.errf("useradd: %v", err)
		return 1
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"useradd: created account "+u.Name, 1)
	fmt.Fprintf(s.Out, "useradd: created %s (UID %d, locked — set a password with passwd %s)\n",
		u.Name, u.UID, u.Name)
	return 0
}

func cmdUserdel(s *Shell, args []string) int {
	if s.User.UID != 0 {
		s.errf("userdel: Permission denied (must be root)")
		return 1
	}
	removeHome := false
	var names []string
	for _, a := range args {
		switch a {
		case "-r", "--remove":
			removeHome = true
		default:
			if strings.HasPrefix(a, "-") {
				s.errf("userdel: unknown option '%s'", a)
				return 1
			}
			names = append(names, a)
		}
	}
	if len(names) != 1 {
		s.errf("usage: userdel [-r] USER")
		return 1
	}
	if names[0] == s.User.Name {
		s.errf("userdel: cannot remove your own account while logged into it")
		return 1
	}
	if err := s.Dev.DelUser(names[0], removeHome); err != nil {
		s.errf("userdel: %v", err)
		return 1
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"userdel: removed account "+names[0], 2)
	fmt.Fprintf(s.Out, "userdel: removed %s\n", names[0])
	return 0
}

func cmdGroupadd(s *Shell, args []string) int {
	if s.User.UID != 0 {
		s.errf("groupadd: Permission denied (must be root)")
		return 1
	}
	if len(args) != 1 {
		s.errf("usage: groupadd GROUP")
		return 1
	}
	if err := s.Dev.AddGroup(args[0]); err != nil {
		s.errf("groupadd: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "groupadd: created %s (GID %d)\n", args[0], s.Dev.GroupID(args[0]))
	return 0
}

func cmdGroups(s *Shell, args []string) int {
	target := s.User
	if len(args) > 1 {
		s.errf("usage: groups [USER]")
		return 1
	}
	if len(args) == 1 {
		u := s.Dev.FindUser(args[0])
		if u == nil {
			s.errf("groups: %s: no such user", args[0])
			return 1
		}
		target = u
	}
	fmt.Fprintf(s.Out, "%s : %s\n", target.Name, strings.Join(target.Groups, " "))
	return 0
}

func cmdUsermod(s *Shell, args []string) int {
	if s.User.UID != 0 {
		s.errf("usermod: Permission denied (must be root)")
		return 1
	}
	appendMode := false
	var groups []string
	var names []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-aG":
			appendMode = true
			if i+1 >= len(args) {
				s.errf("usage: usermod -aG GROUP USER | usermod -G G1,G2 USER")
				return 1
			}
			i++
			groups = append(groups, splitCSV(args[i])...)
		case "-G":
			if i+1 >= len(args) {
				s.errf("usage: usermod -aG GROUP USER | usermod -G G1,G2 USER")
				return 1
			}
			i++
			groups = append(groups, splitCSV(args[i])...)
		default:
			if strings.HasPrefix(args[i], "-") {
				s.errf("usermod: unknown option '%s' (only -aG and -G change groups here)", args[i])
				return 1
			}
			names = append(names, args[i])
		}
	}
	if len(groups) == 0 || len(names) != 1 {
		s.errf("usage: usermod -aG GROUP USER | usermod -G G1,G2 USER")
		return 1
	}
	if err := s.Dev.UsermodGroups(names[0], groups, appendMode); err != nil {
		s.errf("usermod: %v", err)
		return 1
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"usermod: groups of "+names[0], 2)
	fmt.Fprintf(s.Out, "usermod: groups of %s now: %s\n", names[0], strings.Join(s.Dev.FindUser(names[0]).Groups, " "))
	return 0
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// chpasswd sets passwords in batch from a pipeline (`echo 'u:p' | chpasswd
// user`). Root-only like everything else that writes the shadow file; each
// line moves through ChangePassword, so the files agree afterwards. Skipped
// lines are reported, not silently dropped.
func cmdChpasswd(s *Shell, args []string) int {
	if s.User.UID != 0 {
		s.errf("chpasswd: Permission denied (must be root)")
		return 1
	}
	if len(args) > 0 {
		s.errf("usage: echo 'user:password' | chpasswd")
		return 1
	}
	if strings.TrimSpace(s.Stdin) == "" {
		s.errf("chpasswd: no input (pipe user:password lines in)")
		return 1
	}
	changed, skipped := 0, 0
	for _, line := range strings.Split(s.Stdin, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		u, pw, ok := strings.Cut(line, ":")
		u, pw = strings.TrimSpace(u), strings.TrimSpace(pw)
		if !ok || u == "" || pw == "" {
			skipped++
			continue
		}
		if err := s.Dev.ChangePassword(u, pw); err != nil {
			s.errf("chpasswd: %v", err)
			skipped++
			continue
		}
		changed++
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		fmt.Sprintf("chpasswd: changed %d password(s), skipped %d", changed, skipped), 1)
	fmt.Fprintf(s.Out, "chpasswd: changed %d password(s), skipped %d line(s)\n", changed, skipped)
	if changed == 0 {
		return 1
	}
	return 0
}

// ssh-keygen grows a keypair for the session account: the private half stays
// 0600 in ~/.ssh, the public half is printed for distribution — which the
// player does by hand (append it to the far account's authorized_keys),
// exactly like reality. Passphrases are refused rather than half-kept, and
// the key id comes from the world's PID counter, so two keypairs never
// collide the way two processes never share a PID.
func cmdKeygen(s *Shell, args []string) int {
	alg, file, comment := "ed25519", "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-t":
			if i+1 >= len(args) {
				s.errf("usage: ssh-keygen [-t ed25519] [-f PATH] [-C comment]")
				return 1
			}
			i++
			alg = args[i]
		case "-f":
			if i+1 >= len(args) {
				s.errf("usage: ssh-keygen [-t ed25519] [-f PATH] [-C comment]")
				return 1
			}
			i++
			file = args[i]
		case "-C":
			if i+1 >= len(args) {
				s.errf("usage: ssh-keygen [-t ed25519] [-f PATH] [-C comment]")
				return 1
			}
			i++
			comment = args[i]
		case "-N":
			s.errf("ssh-keygen: passphrases are not implemented (keys here are unencrypted files)")
			return 1
		default:
			s.errf("usage: ssh-keygen [-t ed25519] [-f PATH] [-C comment]")
			return 1
		}
	}
	if alg != "ed25519" {
		s.errf("ssh-keygen: only ed25519 here (got %s)", alg)
		return 1
	}
	if file == "" {
		file = s.User.Home + "/.ssh/id_ed25519"
	} else {
		file = s.abs(file)
	}
	if comment == "" {
		comment = s.User.Name + "@" + s.Dev.Hostname
	}
	pub := file + ".pub"
	if s.Dev.FS.Exists(file) || s.Dev.FS.Exists(pub) {
		s.errf("ssh-keygen: %s already exists (remove it or pick -f elsewhere)", file)
		return 1
	}
	keyid := fmt.Sprintf("%d", s.W.NewPID())
	if err := s.Dev.FS.MkdirAllChecked(sshDirOf(file), 0700, s.User); err != nil {
		s.errf("ssh-keygen: %v", err)
		return 1
	}
	priv := "-----BEGIN OPENSSH PRIVATE KEY-----\nkey-" + keyid + "-private-not-a-real-key\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := s.Dev.WriteGuest(file, []byte(priv), s.User); err != nil {
		s.errf("ssh-keygen: %v", err)
		return 1
	}
	// owner-only, always: a private key readable by the room is not private
	if n, ok := s.Dev.FS.Get(file); ok {
		n.Mode = 0600
		n.Owner = s.User.Name
	}
	if err := s.Dev.WriteGuest(pub, []byte("ssh-ed25519 AAAA"+keyid+" "+comment+"\n"), s.User); err != nil {
		s.errf("ssh-keygen: %v", err)
		return 1
	}
	s.Dev.Logf("info", "ssh-keygen", "%s generated %s", s.User.Name, file)
	fmt.Fprintf(s.Out, "Generating public/private ed25519 key pair.\n")
	fmt.Fprintf(s.Out, "Your public key has been saved in %s.\n", pub)
	return 0
}

func sshDirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// passwd changes an account's password for real: the account record and
// /etc/shadow move together (see core.ChangePassword), so the next login,
// su, sudo, ssh, sftp or ftp attempt answers against the new one — and the
// world's scanner guesses against it too. That is what makes the
// weak-credential half of §36's conjunction a choice instead of test state:
// publish a port, leave a default password, run no sensors, and the
// intrusion chain is yours.
//
// Like the real tool it is setuid-like: a user changing their own password
// does not need read access to /etc/shadow, because the call goes through
// the core state update rather than a file write. Only root may name another
// account, and only root skips the current-password check.
func cmdPasswd(s *Shell, args []string) int {
	if len(args) > 1 {
		s.errf("usage: passwd [username]")
		return 1
	}
	target := s.User.Name
	if len(args) == 1 {
		target = args[0]
	}
	u := s.Dev.FindUser(target)
	if u == nil {
		s.errf("passwd: user '%s' does not exist", target)
		return 1
	}
	self := u.Name == s.User.Name
	if !self && s.User.UID != 0 {
		s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
			"passwd denied for "+u.Name+" (not root)", 3)
		s.errf("passwd: You may not view or modify password information for %s.", u.Name)
		return 1
	}
	if self && s.User.UID != 0 {
		fmt.Fprintf(s.Out, "Current password: ")
		if cur := s.ReadPasswordLine(""); !s.verifyPassword(s.User, cur) {
			s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"failed passwd authentication", 3)
			s.errf("passwd: Authentication failure")
			return 1
		}
	}
	fmt.Fprintf(s.Out, "New password: ")
	nw := s.ReadPasswordLine("")
	fmt.Fprintf(s.Out, "Retype new password: ")
	if rt := s.ReadPasswordLine(""); rt != nw {
		fmt.Fprintf(s.Out, "Sorry, passwords do not match.\n")
		fmt.Fprintf(s.Out, "passwd: password unchanged\n")
		return 1
	}
	if nw == "" {
		fmt.Fprintf(s.Out, "No password supplied\n")
		fmt.Fprintf(s.Out, "passwd: password unchanged\n")
		return 1
	}
	if err := s.Dev.ChangePassword(u.Name, nw); err != nil {
		s.errf("passwd: %v", err)
		return 1
	}
	s.W.Record("auth", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		"passwd: password changed for "+u.Name, 1)
	fmt.Fprintf(s.Out, "passwd: password updated successfully\n")
	return 0
}

func cmdFastfetch(s *Shell, args []string) int {
	d := s.Dev
	fmt.Fprintf(s.Out, "%s@%s\n", s.User.Name, d.Hostname)
	fmt.Fprintf(s.Out, "OS: %s %s (%s)\n", d.OS.Distro, d.OS.Ver, d.OS.Arch)
	fmt.Fprintf(s.Out, "Host: %s (%s)\n", d.HW.Model, d.ID)
	fmt.Fprintf(s.Out, "Kernel: %s\n", d.OS.Kernel)
	fmt.Fprintf(s.Out, "Uptime: %s\n", d.Uptime().Round(time.Second))
	shellName := s.User.Shell
	if shellName == "" {
		shellName = s.Dev.OS.Shell
	}
	fmt.Fprintf(s.Out, "Shell: %s\n", shellName)
	fmt.Fprintf(s.Out, "Terminal: %s\n", s.TTY)
	fmt.Fprintf(s.Out, "CPU: %s (%d cores @ %d MHz)\n", d.HW.Model, d.HW.Cores, d.HW.CPUMHz)
	memUsed := d.MemUsed()
	memFree := d.HW.RAMMB - memUsed
	if memFree < 0 {
		memFree = 0
	}
	fmt.Fprintf(s.Out, "Memory: %d / %d MiB (%d MiB free)\n", memUsed, d.HW.RAMMB, memFree)
	diskUsed := d.FS.DiskUsedMB()
	diskFree := d.HW.DiskMB - diskUsed
	if diskFree < 0 {
		diskFree = 0
	}
	fmt.Fprintf(s.Out, "Disk: %d / %d MiB (%d MiB free)\n", diskUsed, d.HW.DiskMB, diskFree)
	packages := make([]string, 0, len(d.Installed))
	for name := range d.Installed {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	if len(packages) == 0 {
		fmt.Fprintln(s.Out, "Packages: none")
	} else {
		fmt.Fprintf(s.Out, "Packages: %s\n", strings.Join(packages, ", "))
	}
	fmt.Fprintf(s.Out, "Processes: %d\n", len(d.Procs))
	fmt.Fprintf(s.Out, "Services: %d running\n", runningServiceCount(d))

	var addresses, v6 []string
	linkLocal := false
	for _, iface := range d.Ifaces {
		if !iface.Up {
			continue
		}
		if iface.IP != "" {
			ifaceName := iface.Name
			if iface.Zone != "" {
				ifaceName += "[" + iface.Zone + "]"
			}
			addresses = append(addresses, ifaceName+"="+iface.IP)
		}
		// §13: the v6 addresses are read from the interface, not guessed — a
		// host that really holds a global v6 address reports it here
		for _, a := range iface.IP6 {
			if strings.HasPrefix(a, "fe80:") {
				// every v6 host has a link-local address, even when it has no
				// route to the v6 internet: it is not connectivity
				linkLocal = true
				continue
			}
			v6 = append(v6, a)
		}
	}
	if len(addresses) == 0 {
		fmt.Fprintln(s.Out, "Network: down (no active addresses)")
	} else {
		fmt.Fprintf(s.Out, "Network: %s\n", strings.Join(addresses, ", "))
	}
	switch {
	case len(v6) > 0:
		fmt.Fprintf(s.Out, "IPv6: configured (%s)\n", strings.Join(v6, ", "))
	case linkLocal:
		fmt.Fprintln(s.Out, "IPv6: link-local only (no routable address)")
	default:
		fmt.Fprintln(s.Out, "IPv6: not configured")
	}
	return 0
}

func runningServiceCount(d *core.Device) int {
	count := 0
	for _, svc := range d.Services {
		if svc.State == "running" {
			count++
		}
	}
	return count
}

func cmdWho(s *Shell, args []string) int {
	fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", s.User.Name, s.TTY, time.Now().Format("2006-01-02 15:04"), s.srcIP)
	for _, l := range s.Dev.Active {
		fmt.Fprintf(s.Out, "%-10s %-8s %-12s %s\n", l.User, l.TTY, l.At.Format("2006-01-02 15:04"), l.From)
	}
	return 0
}

func cmdClear(s *Shell, args []string) int {
	fmt.Fprint(s.Out, "\033[H\033[2J")
	return 0
}

func cmdExit(s *Shell, args []string) int {
	s.exitFlag = true
	return 0
}

var _ = time.Now
