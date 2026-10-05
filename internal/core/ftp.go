package core

// FTP (WS-1.4): the last system of spec §19's communication list, and the
// protocol behind a seeded attack surface that had no service to speak to.
//
// A session here is real world state in the same sense an ssh session is:
//
//   - the control connection is opened through Dial(), so DNS, routing, the
//     router's port-forward, the firewall, power and the daemon's own service
//     state all gate it;
//   - authentication is against the server's real account records (or the
//     anonymous policy the daemon's real /etc/vsftpd.conf declares);
//   - every listing, read and write is the *remote account's* read and write
//     on the *server's* filesystem, checked by the VFS permissions;
//   - the daemon's configuration is parsed on demand, never shadowed, so
//     editing /etc/vsftpd.conf changes what the next command may do;
//   - every session, refusal and upload lands in the server's log, because a
//     successful intrusion that leaves no trace would break §32.
//
// There is no "FTP server object": the conf file, the filesystem and the
// account records are the truth, and this file only models the protocol.

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// FTPConf is the daemon's configuration as parsed from /etc/vsftpd.conf.
// vsftpd's compiled-in defaults apply for anything the file does not set, so a
// device that ships the package without touching the file gets exactly what
// Debian's default gives: local logins, no anonymous access, no writes.
type FTPConf struct {
	Anonymous      bool // anonymous_enable
	AnonUpload     bool // anon_upload_enable
	AnonMkdirWrite bool // anon_mkdir_write_enable
	AnonOtherWrite bool // anon_other_write_enable (delete/rename)
	LocalEnable    bool // local_enable
	WriteEnable    bool // write_enable
	AnonRoot       string
	LocalRoot      string
}

// FTPConfOf parses the real /etc/vsftpd.conf on a device. The file is re-read
// on every call: there is no cached config anywhere, so a player who edits the
// daemon's configuration sees the effect immediately, and a player who reads it
// sees exactly what the daemon enforces.
//
// It owns no port: a unit's port is world state every other subsystem already
// agrees on (ss, scan, firewall rules, port-forwards), and a listen_port line
// that contradicts it would give the same number two owners. See the workstream
// notes for why that directive is deliberately not honoured.
func FTPConfOf(d *Device) FTPConf {
	c := FTPConf{LocalEnable: true}
	if d == nil || d.FS == nil {
		return c
	}
	data, ok := d.FS.Read("/etc/vsftpd.conf")
	if !ok {
		return c
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "anonymous_enable":
			c.Anonymous = ftpYes(v)
		case "anon_upload_enable":
			c.AnonUpload = ftpYes(v)
		case "anon_mkdir_write_enable":
			c.AnonMkdirWrite = ftpYes(v)
		case "anon_other_write_enable":
			c.AnonOtherWrite = ftpYes(v)
		case "local_enable":
			c.LocalEnable = ftpYes(v)
		case "write_enable":
			c.WriteEnable = ftpYes(v)
		case "anon_root":
			c.AnonRoot = v
		case "local_root":
			c.LocalRoot = v
		}
	}
	return c
}

func ftpYes(v string) bool {
	switch strings.ToLower(v) {
	case "yes", "true", "1", "on":
		return true
	}
	return false
}

// FTPDaemon finds a device's FTP daemon and the port its own configuration says
// it listens on. The seeded NPC box registers the handler as "npc-ftp", an
// apt-installed package as "ftp-user"; both are this world's vsftpd.
func FTPDaemon(d *Device) (*Service, int) {
	if d == nil {
		return nil, 0
	}
	names := make([]string, 0, len(d.Services))
	for n := range d.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := d.Services[n]
		if s.Handler == "ftp-user" || s.Handler == "npc-ftp" || n == "vsftpd" {
			return s, s.Port
		}
	}
	return nil, 0
}

// FTPEntry is one name in a directory listing, with the real type and size.
type FTPEntry struct {
	Name  string
	IsDir bool
	Size  int
}

// FTPSession is one live control connection. It carries who the session acts
// as and where it may go; everything else is read from the world on each
// command, so a session cannot outlive the daemon's state or its own policy.
type FTPSession struct {
	W        *World
	Srv      *Device
	SvcName  string // the daemon unit, re-checked on every operation
	Actor    string // the connecting account (for evidence)
	Origin   string // the client's address as the server sees it
	As       *User  // the account the session acts as
	Anon     bool
	Root     string // the session's visible root; anonymous may not leave it
	CWD      string
	ClientIP string
}

// FTPLogin opens an authenticated session against srv. The account name
// "anonymous" (or "ftp", as real vsftpd treats it) selects the anonymous
// policy; anything else is a local login against the server's account records.
// The returned error carries the daemon's own reply text, so a caller can print
// the protocol honestly and a player cannot tell "no such account" from "wrong
// password" — the daemon does not tell them either.
func (w *World) FTPLogin(client *Device, actor string, srv *Device, user, pass string) (*FTPSession, error) {
	if srv == nil || client == nil {
		return nil, fmt.Errorf("421 Service not available, remote server has closed connection")
	}
	svc, _ := FTPDaemon(srv)
	if svc == nil {
		return nil, fmt.Errorf("421 Service not available, remote server has closed connection")
	}
	if svc.State != "running" {
		return nil, fmt.Errorf("421 Service not available, remote server has closed connection")
	}
	c := FTPConfOf(srv)
	origin := client.SourceIPFor(srv)
	anon := user == "anonymous" || user == "ftp"

	if anon {
		if !c.Anonymous {
			srv.Logf("notice", "vsftpd", "anonymous login refused from %s (anonymous_enable=NO)", origin)
			w.Record("auth", actor, origin, srv.ID, "anonymous FTP login refused", 2)
			return nil, fmt.Errorf("530 Permission denied.")
		}
		acc, root, err := ftpAnonAccount(srv, c)
		if err != nil {
			srv.Logf("notice", "vsftpd", "anonymous login refused from %s: %v", origin, err)
			w.Record("auth", actor, origin, srv.ID, "anonymous FTP login refused", 2)
			return nil, fmt.Errorf("530 Permission denied.")
		}
		srv.Logf("info", "vsftpd", "anonymous login ok from %s (root %s)", origin, root)
		w.Record("auth", actor, origin, srv.ID, "anonymous FTP login", 1)
		return &FTPSession{W: w, Srv: srv, SvcName: svc.Name, Actor: actor, Origin: origin,
			As: acc, Anon: true, Root: root, CWD: root, ClientIP: origin}, nil
	}

	if !c.LocalEnable {
		srv.Logf("notice", "vsftpd", "local login for %s refused from %s (local_enable=NO)", user, origin)
		w.Record("auth", actor, origin, srv.ID, "FTP login refused for "+user, 2)
		return nil, fmt.Errorf("530 Permission denied.")
	}
	acc := srv.FindUser(user)
	if acc == nil || !acc.CheckPassword(pass) {
		srv.Logf("notice", "vsftpd", "failed login for %s from %s", user, origin)
		w.Record("auth", actor, origin, srv.ID, "failed FTP login as "+user, 3)
		return nil, fmt.Errorf("530 Login incorrect.")
	}
	root := "/"
	if c.LocalRoot != "" && srv.FS.IsDir(c.LocalRoot) {
		root = path.Clean(c.LocalRoot)
	}
	srv.Logf("info", "vsftpd", "%s logged in from %s (root %s)", acc.Name, origin, root)
	w.Record("auth", actor, origin, srv.ID, "FTP login "+acc.Name+"@"+srv.Hostname, 1)
	return &FTPSession{W: w, Srv: srv, SvcName: svc.Name, Actor: actor, Origin: origin,
		As: acc, Root: root, CWD: root, ClientIP: origin}, nil
}

// ftpAnonAccount resolves the account anonymous sessions run as and the
// directory they are confined to: anon_root when the configuration names one,
// else the `ftp` account's home, else /srv/ftp. In each case the directory must
// really exist — a daemon whose drop directory is missing refuses the login
// instead of inventing one.
func ftpAnonAccount(d *Device, c FTPConf) (*User, string, error) {
	acc := d.FindUser("ftp")
	if acc == nil {
		acc = d.FindUser("nobody")
	}
	if acc == nil {
		return nil, "", fmt.Errorf("no anonymous account (ftp/nobody) on this image")
	}
	root := c.AnonRoot
	if root == "" {
		root = acc.Home
	}
	if root == "" || root == "/nonexistent" {
		root = "/srv/ftp"
	}
	root = path.Clean(root)
	if !d.FS.IsDir(root) {
		return nil, "", fmt.Errorf("anonymous root %s does not exist", root)
	}
	return acc, root, nil
}

// Closed reports whether the daemon has gone away under a live session: real
// ftpd answers 421 to every command once the service dies, and stopping
// vsftpd must really end the session (§33: service lifecycle is world state).
func (s *FTPSession) Closed() error {
	if s.Srv == nil || s.W == nil {
		return fmt.Errorf("421 Service not available, remote server has closed connection")
	}
	svc := s.Srv.Svc(s.SvcName)
	if svc == nil || svc.State != "running" {
		return fmt.Errorf("421 Service not available, remote server has closed connection")
	}
	return nil
}

// Path cleans a client path against the session's CWD and confines it to the
// session root. Anonymous sessions may not leave the anonymous root, which is
// what real vsftpd's implicit chroot does; a local session sees "/" unless the
// configuration set local_root.
func (s *FTPSession) Path(p string) (string, error) {
	if p == "" {
		return s.CWD, nil
	}
	if !strings.HasPrefix(p, "/") {
		p = s.CWD + "/" + p
	}
	p = path.Clean(p)
	if p != s.Root && !strings.HasPrefix(p, s.Root+"/") && s.Root != "/" {
		return "", fmt.Errorf("550 Permission denied.")
	}
	return p, nil
}

// Cwd changes the session's working directory, as the session account.
func (s *FTPSession) Cwd(p string) (string, error) {
	if err := s.Closed(); err != nil {
		return "", err
	}
	target, err := s.Path(p)
	if err != nil {
		return "", err
	}
	if !s.Srv.FS.IsDir(target) {
		return "", fmt.Errorf("550 Failed to change directory.")
	}
	if !s.Srv.FS.CanRead(target, s.As) || !s.Srv.FS.CanExec(target, s.As) {
		s.denied("CWD", target)
		return "", fmt.Errorf("550 Failed to change directory.")
	}
	s.CWD = target
	return target, nil
}

// List lists a directory the way the remote account sees it.
func (s *FTPSession) List(p string) ([]FTPEntry, error) {
	if err := s.Closed(); err != nil {
		return nil, err
	}
	dir, err := s.Path(p)
	if err != nil {
		return nil, err
	}
	if !s.Srv.FS.IsDir(dir) {
		return nil, fmt.Errorf("550 Failed to open directory.")
	}
	if !s.Srv.FS.CanRead(dir, s.As) || !s.Srv.FS.CanExec(dir, s.As) {
		s.denied("LIST", dir)
		return nil, fmt.Errorf("550 Permission denied.")
	}
	var out []FTPEntry
	for _, child := range s.Srv.FS.List(dir) {
		n, ok := s.Srv.FS.Get(child)
		if !ok {
			continue
		}
		e := FTPEntry{Name: path.Base(child), IsDir: n.IsDir}
		if !n.IsDir {
			e.Size = len(n.Data)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Retr reads one file as the session account. A refused read is evidence on the
// server, exactly like a denied read in an ssh session.
func (s *FTPSession) Retr(p string) ([]byte, error) {
	if err := s.Closed(); err != nil {
		return nil, err
	}
	target, err := s.Path(p)
	if err != nil {
		return nil, err
	}
	if s.Srv.FS.IsDir(target) {
		return nil, fmt.Errorf("550 Failed to open file.")
	}
	data, exists, allowed := s.Srv.FS.ReadPathAs(target, s.As)
	if !exists {
		return nil, fmt.Errorf("550 Failed to open file.")
	}
	if !allowed {
		s.denied("RETR", target)
		return nil, fmt.Errorf("550 Permission denied.")
	}
	s.Srv.Logf("info", "vsftpd", "%s downloaded %s (%dB) from %s",
		s.logName(), target, len(data), s.Origin)
	return data, nil
}

// Stor writes one file as the session account. The daemon's own policy decides
// whether writing is allowed at all (write_enable for local accounts,
// anon_upload_enable for anonymous ones), and the server's filesystem decides
// where it may land — a 0777 drop directory accepts an anonymous upload, an
// /etc directory does not.
func (s *FTPSession) Stor(p string, data []byte) error {
	if err := s.Closed(); err != nil {
		return err
	}
	c := FTPConfOf(s.Srv)
	if s.Anon {
		if !c.AnonUpload {
			s.refused("STOR", p, "anon_upload_enable=NO")
			return fmt.Errorf("550 Permission denied.")
		}
	} else if !c.WriteEnable {
		s.refused("STOR", p, "write_enable=NO")
		return fmt.Errorf("550 Permission denied.")
	}
	target, err := s.Path(p)
	if err != nil {
		return err
	}
	if s.Srv.FS.IsDir(target) {
		return fmt.Errorf("553 Could not create file.")
	}
	if err := s.Srv.WriteGuest(target, data, s.As); err != nil {
		s.refused("STOR", target, err.Error())
		return fmt.Errorf("553 Could not create file.")
	}
	s.Srv.Logf("info", "vsftpd", "%s uploaded %s (%dB) from %s",
		s.logName(), target, len(data), s.Origin)
	if s.Anon {
		// an anonymous write from the outside is an alert-level world event:
		// the camera records it, the owner can find it, and it raises heat
		s.W.AddEvent(s.Srv.ID, "warn", "vsftpd",
			"anonymous upload %s from %s", target, s.Origin)
		s.W.Record("file", s.Actor, s.Origin, s.Srv.ID,
			"anonymous FTP upload "+target, 3)
	}
	return nil
}

// Mkd creates a directory, gated by anon_mkdir_write_enable for anonymous
// sessions and by write_enable (plus real directory permissions) for local ones.
func (s *FTPSession) Mkd(p string) error {
	if err := s.Closed(); err != nil {
		return err
	}
	c := FTPConfOf(s.Srv)
	if s.Anon {
		if !c.AnonMkdirWrite {
			s.refused("MKD", p, "anon_mkdir_write_enable=NO")
			return fmt.Errorf("550 Permission denied.")
		}
	} else if !c.WriteEnable {
		s.refused("MKD", p, "write_enable=NO")
		return fmt.Errorf("550 Permission denied.")
	}
	target, err := s.Path(p)
	if err != nil {
		return err
	}
	if s.Srv.FS.Exists(target) {
		return fmt.Errorf("550 Create directory operation failed.")
	}
	if err := s.Srv.FS.MkdirAllChecked(target, 0755, s.As); err != nil {
		s.refused("MKD", target, err.Error())
		return fmt.Errorf("550 Create directory operation failed.")
	}
	s.Srv.Logf("info", "vsftpd", "%s created %s from %s", s.logName(), target, s.Origin)
	return nil
}

// Dele removes a file: anon_other_write_enable for anonymous sessions (vsftpd's
// default is NO, so a plain anonymous drop directory is write-only in the
// create sense and cannot delete anything), write_enable for local accounts.
func (s *FTPSession) Dele(p string) error {
	if err := s.Closed(); err != nil {
		return err
	}
	c := FTPConfOf(s.Srv)
	if s.Anon {
		if !c.AnonOtherWrite {
			s.refused("DELE", p, "anon_other_write_enable=NO")
			return fmt.Errorf("550 Permission denied.")
		}
	} else if !c.WriteEnable {
		s.refused("DELE", p, "write_enable=NO")
		return fmt.Errorf("550 Permission denied.")
	}
	target, err := s.Path(p)
	if err != nil {
		return err
	}
	if !s.Srv.FS.Exists(target) {
		return fmt.Errorf("550 Delete operation failed.")
	}
	if !s.Srv.FS.CanRemove(target, s.As) {
		s.denied("DELE", target)
		return fmt.Errorf("550 Permission denied.")
	}
	s.Srv.FS.Remove(target)
	s.Srv.Logf("info", "vsftpd", "%s deleted %s from %s", s.logName(), target, s.Origin)
	return nil
}

// Size reports a file's real byte count.
func (s *FTPSession) Size(p string) (int, error) {
	data, err := s.Retr(p)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// Close logs the end of a session, the way vsftpd logs "FTP session closed".
func (s *FTPSession) Close() {
	if s == nil || s.Srv == nil {
		return
	}
	s.Srv.Logf("info", "vsftpd", "FTP session closed: %s from %s", s.logName(), s.Origin)
}

func (s *FTPSession) logName() string {
	if s.Anon {
		return "anonymous/" + s.As.Name
	}
	return s.As.Name
}

// denied and refused are the two halves of §32's "success is not the same as
// leaving no evidence": a permission refusal on a path, and a policy refusal.
func (s *FTPSession) denied(op, target string) {
	s.Srv.Logf("notice", "vsftpd", "%s denied %s for %s from %s", op, target, s.logName(), s.Origin)
	s.W.Record("denied", s.Actor, s.Origin, s.Srv.ID, "FTP "+op+" denied: "+target, 1)
}

func (s *FTPSession) refused(op, target, why string) {
	s.Srv.Logf("notice", "vsftpd", "%s refused for %s from %s (%s)", op+" "+target, s.logName(), s.Origin, why)
	s.W.Record("denied", s.Actor, s.Origin, s.Srv.ID, "FTP "+op+" refused: "+why, 1)
}
