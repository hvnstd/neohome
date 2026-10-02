package shell

import (
	"strings"

	"neohome/internal/core"
)

// Privilege and secrets.
//
// Authentication previously meant "does this string equal the stored password",
// and authorisation meant "is this name in the sudo group". Both are now backed
// by the files that really decide these things on a Unix box:
//
//	/etc/shadow   the credential store; 0640 root:shadow, so an ordinary account
//	              cannot read it, and stealing it is a real objective
//	/etc/sudoers  who may use sudo; 0440 root:root
//
// The passwords in this world are simulated — no real crypt(3) hash is verified —
// but the consequence is real: an attacker on the box cannot simply read the
// credential store, and an account that is not in sudoers cannot escalate.

// verifyPassword checks a supplied password against the account's stored one.
func (s *Shell) verifyPassword(u *core.User, pw string) bool {
	if u == nil {
		return false
	}
	pw = strings.TrimSpace(pw)
	if u.Pass == "" {
		return false
	}
	return pw == u.Pass
}

// readFile reads a path honouring read permissions, reporting a refusal the way
// the calling tool would. Every text tool goes through this, because a leak
// through `grep` or `sed` defeats the file mode just as thoroughly as `cat`.
func (s *Shell) readFile(vfs *core.VFS, p, cmdName, display string) ([]byte, bool) {
	data, exists, allowed := vfs.ReadPathAs(p, s.User)
	if !exists {
		s.errf(cmdName + ": " + display + ": No such file or directory")
		return nil, false
	}
	if !allowed {
		s.errf(cmdName + ": " + display + ": Permission denied")
		s.noteDenied(display, "read")
		return nil, false
	}
	return data, true
}

// noteDenied records a refused read/write so the defensive side can see probing,
// not just successes.
func (s *Shell) noteDenied(path, what string) {
	if s.Dev == nil || s.W == nil {
		return
	}
	s.Dev.Logf("notice", "audit", "%s: %s denied on %s", s.User.Name, what, path)
	s.W.Record("denied", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
		what+" denied: "+path, 1)
}
