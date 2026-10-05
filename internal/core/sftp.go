package core

// SFTP (WS-0.9): the last of spec §19's communication systems. File
// transfer over the SSH channel: a session that talks to the remote
// filesystem as the remote account, with both ends of every copy
// permission-checked and logged. There is no "sftp server object" — the
// remote files are the truth, and the account records decide what a
// session may touch.

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// SFTPEntry is one name in a remote directory listing.
type SFTPEntry struct {
	Name  string
	IsDir bool
}

// SFTPList lists dir on d the way the remote account would see it.
func (w *World) SFTPList(d *Device, u *User, dir string) ([]SFTPEntry, error) {
	if d == nil || u == nil {
		return nil, fmt.Errorf("no session")
	}
	if !d.FS.IsDir(dir) {
		return nil, fmt.Errorf("%s: no such file or directory", dir)
	}
	if !d.FS.CanRead(dir, u) {
		return nil, fmt.Errorf("%s: permission denied", dir)
	}
	var out []SFTPEntry
	for _, p := range d.FS.List(dir) {
		out = append(out, SFTPEntry{Name: path.Base(p), IsDir: d.FS.IsDir(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SFTPGet copies one file from the remote (read as the remote account) to
// the local device (written as the session account). A denied read is
// evidence on the remote, exactly like a denied read in an ssh session.
func (w *World) SFTPGet(src *Device, lu *User, dst *Device, ru *User, remotePath, localPath string) error {
	if dst == nil || ru == nil || src == nil || lu == nil {
		return fmt.Errorf("no session")
	}
	data, exists, allowed := dst.FS.ReadPathAs(remotePath, ru)
	if !exists {
		return fmt.Errorf("%s: no such file or directory", remotePath)
	}
	if !allowed {
		dst.Logf("notice", "sshd", "sftp: %s denied fetch of %s from %s", ru.Name, remotePath, src.Hostname)
		return fmt.Errorf("%s: permission denied", remotePath)
	}
	if src.FS.IsDir(localPath) {
		return fmt.Errorf("%s: is a directory", localPath)
	}
	if err := src.FS.WriteChecked(localPath, data, lu); err != nil {
		return err
	}
	dst.Logf("info", "sshd", "sftp: %s fetched %s (%dB) to %s", ru.Name, remotePath, len(data), src.Hostname)
	return nil
}

// SFTPPut copies one file from the local device (read as the session
// account) onto the remote (written as the remote account).
func (w *World) SFTPPut(src *Device, lu *User, dst *Device, ru *User, localPath, remotePath string) error {
	if src == nil || lu == nil || dst == nil || ru == nil {
		return fmt.Errorf("no session")
	}
	data, exists, allowed := src.FS.ReadPathAs(localPath, lu)
	if !exists {
		return fmt.Errorf("%s: no such file or directory", localPath)
	}
	if !allowed {
		src.Logf("notice", "audit", "%s denied sftp read of %s", lu.Name, localPath)
		return fmt.Errorf("%s: permission denied", localPath)
	}
	if dst.FS.IsDir(remotePath) {
		return fmt.Errorf("%s: is a directory", remotePath)
	}
	if err := dst.FS.WriteChecked(remotePath, data, ru); err != nil {
		return err
	}
	dst.Logf("info", "sshd", "sftp: %s stored %s (%dB) from %s", ru.Name, remotePath, len(data), src.Hostname)
	return nil
}

// SFTPCleanPath resolves a remote path the way the sftp client does:
// absolute paths stand, relative ones hang off the session's remote CWD.
func SFTPCleanPath(cwd, p string) string {
	if !strings.HasPrefix(p, "/") {
		p = cwd + "/" + p
	}
	return path.Clean(p)
}
