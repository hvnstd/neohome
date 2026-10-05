package core

// SMB (WS-1.1): the Windows half of "SMB / NFS: 真共享文件". A share is
// whatever the server's real /etc/samba/smb.conf says it is — parsed on
// demand, never shadowed — and mounting a non-guest share is a real
// authentication against the server's own account records, gated by the
// share's valid users list when it declares one.

import (
	"fmt"
	"strings"
)

// SMBShare is one share as the server's configuration declares it.
type SMBShare struct {
	Name       string
	Path       string
	GuestOK    bool
	ValidUsers []string
}

// SMBConfPath is where the daemon's configuration lives on a server.
const SMBConfPath = "/etc/samba/smb.conf"

// SMBShares parses the device's smb.conf into its shares. A server without
// the file shares nothing — the configuration is the truth, not a map
// somewhere else.
func (w *World) SMBShares(d *Device) []SMBShare {
	data, ok := d.FS.Read(SMBConfPath)
	if !ok {
		return nil
	}
	var out []SMBShare
	var cur *SMBShare
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := line[1 : len(line)-1]
			if name == "global" {
				cur = nil
				continue
			}
			out = append(out, SMBShare{Name: name})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		key, val := line, ""
		if i := strings.Index(line, "="); i >= 0 {
			key, val = strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
		switch key {
		case "path":
			cur.Path = val
		case "guest ok":
			cur.GuestOK = val == "yes"
		case "valid users":
			for _, u := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' }) {
				if u != "" {
					cur.ValidUsers = append(cur.ValidUsers, u)
				}
			}
		}
	}
	return out
}

// SMBShare looks up one share by name.
func (w *World) SMBShare(d *Device, name string) (*SMBShare, error) {
	shares := w.SMBShares(d)
	for i := range shares {
		if shares[i].Name == name {
			return &shares[i], nil
		}
	}
	return nil, fmt.Errorf("share %q does not exist", name)
}

// SMBAuth checks a mount request: guest shares accept anyone, everything
// else is an account check against the server plus the share's
// valid-users ACL. The failure does not say which part failed.
func (w *World) SMBAuth(d *Device, sh *SMBShare, user, pass string) error {
	if sh.GuestOK {
		return nil
	}
	u := d.FindUser(user)
	if u == nil || u.Pass == "" || pass != u.Pass {
		return fmt.Errorf("authentication failed")
	}
	if len(sh.ValidUsers) > 0 {
		allowed := false
		for _, name := range sh.ValidUsers {
			if name == user {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("access denied by server (not in valid users)")
		}
	}
	return nil
}
