package shell

// SMB (WS-1.1): the client side. mountSMB carries the //host/share form of
// mount, with real authentication for non-guest shares; smbclient lists
// what a server really declares — the recon step before the mount.

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["smbclient"] = cmdSmbclient
}

// mountSMB mounts a //host/share source: the share must exist in the
// server's real smb.conf, and non-guest shares authenticate against the
// server's own account records with the share's valid-users ACL on top.
func mountSMB(s *Shell, src, dst string, opts map[string]string) int {
	rest := strings.TrimPrefix(src, "//")
	i := strings.Index(rest, "/")
	if i <= 0 {
		s.errf("mount: bad source %s (want //host/share)", src)
		return 1
	}
	host, shareName := rest[:i], rest[i+1:]
	if shareName == "" || strings.Contains(shareName, "/") {
		s.errf("mount: bad source %s (want //host/share)", src)
		return 1
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("mount: cannot resolve %s: %s", host, how)
		return 1
	}
	id, found := s.W.IPMap[ip]
	if !found {
		s.errf("mount: no route to %s", host)
		return 1
	}
	srv := s.W.Devices[id]
	svc, _, msg := core.Dial(s.Dev, ip, 445)
	if svc == nil {
		s.errf("mount: //%s/%s: %s", host, shareName, msg)
		fmt.Fprintf(s.Out, "  is smbd running on %s? try: ssh %s then systemctl status smbd\n", host, host)
		return 1
	}
	sh, err := s.W.SMBShare(srv, shareName)
	if err != nil {
		s.errf("mount: //%s/%s: does not exist (see: smbclient -L %s)", host, shareName, host)
		return 1
	}
	user := s.User.Name
	if u := opts["user"]; u != "" {
		user = u
	}
	if !sh.GuestOK {
		fmt.Fprintf(s.Out, "Password for %s@//%s/%s: ", user, host, shareName)
		pass := s.ReadPasswordLine("")
		if err := s.W.SMBAuth(srv, sh, user, pass); err != nil {
			s.errf("mount error(13): Permission denied (%v)", err)
			return 1
		}
	}
	s.Dev.Mounts = append(s.Dev.Mounts, core.Mount{Src: id + ":" + sh.Path, Dst: s.abs(dst), FSTy: "cifs"})
	fmt.Fprintf(s.Out, "mounted //%s/%s on %s (type cifs)\n", host, shareName, s.abs(dst))
	return 0
}

// cmdSmbclient lists the shares a server really declares, parsed from its
// own configuration.
func cmdSmbclient(s *Shell, args []string) int {
	host := ""
	for i, a := range args {
		if a == "-L" && i+1 < len(args) {
			host = args[i+1]
		}
	}
	if host == "" {
		s.errf("usage: smbclient -L HOST")
		return 1
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("smbclient: cannot resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, 445)
	if svc == nil {
		s.errf("smbclient: connect to %s: %s", host, msg)
		return 1
	}
	shares := s.W.SMBShares(dst)
	fmt.Fprintln(s.Out, "\tSharename       Type")
	fmt.Fprintln(s.Out, "\t---------       ----")
	if len(shares) == 0 {
		fmt.Fprintln(s.Out, "\t(the server shares nothing)")
		return 0
	}
	for _, sh := range shares {
		fmt.Fprintf(s.Out, "\t%-16s Disk\n", sh.Name)
	}
	fmt.Fprintf(s.Out, "\nmount one: mount -t cifs //%s/<share> /mnt/x [-o user=NAME]\n", host)
	return 0
}
