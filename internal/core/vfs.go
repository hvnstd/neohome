package core

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// VFS is a per-device virtual filesystem. Pure state: maps absolute paths to
// inodes. Directories are implied by children plus explicit dir inodes.
type VFS struct {
	Nodes map[string]*INode
}

type INode struct {
	Path    string
	IsDir   bool
	Data    []byte
	Mode    fs.FileMode
	UID     int
	GID     int
	Owner   string
	Group   string
	MTime   time.Time
	Symlink string // target path if mode has symlink bit
}

func NewVFS() *VFS {
	v := &VFS{Nodes: map[string]*INode{}}
	v.MkdirAll("/", 0755, "root", "root")
	return v
}

func (v *VFS) MkdirAll(p string, mode uint32, owner, group string) {
	p = path.Clean(p)
	for cur := p; cur != "/" && cur != ""; cur = path.Dir(cur) {
		if _, ok := v.Nodes[cur]; !ok {
			v.Nodes[cur] = &INode{Path: cur, IsDir: true, Mode: fs.FileMode(mode), Owner: owner, Group: group, MTime: time.Now()}
		}
	}
	if _, ok := v.Nodes["/"]; !ok {
		v.Nodes["/"] = &INode{Path: "/", IsDir: true, Mode: 0755, Owner: "root", Group: "root"}
	}
}

func (v *VFS) Write(p, content string, mode uint32, owner, group string) {
	p = path.Clean(p)
	v.MkdirAll(path.Dir(p), 0755, owner, group)
	n := &INode{Path: p, Mode: fs.FileMode(mode), Owner: owner, Group: group, MTime: time.Now()}
	n.Data = []byte(content)
	v.Nodes[p] = n
}

// WriteChecked enforces permissions for a real user (redirects go through it).
func (v *VFS) WriteChecked(p string, data []byte, u *User) error {
	p = path.Clean(p)
	if u == nil {
		return fmt.Errorf("permission denied")
	}
	existing, has := v.Nodes[p]
	if has && existing.IsDir {
		return fmt.Errorf("is a directory")
	}
	if !has {
		parent, ok := v.Get(path.Dir(p))
		if !ok || !parent.IsDir {
			return fmt.Errorf("no such file or directory")
		}
	}
	if u.UID != 0 {
		if !v.AccessiblePath(p, u) {
			return fmt.Errorf("permission denied")
		}
		if has {
			if !v.canWriteMode(existing, u) {
				return fmt.Errorf("permission denied")
			}
		} else if !v.CanWriteDir(path.Dir(p), u) {
			return fmt.Errorf("permission denied")
		}
	}
	mode := uint32(0644)
	owner := u.Name
	group := u.Name
	if has {
		mode = uint32(existing.Mode.Perm())
		owner = existing.Owner
		group = existing.Group
	}
	v.WriteBytes(p, data, mode, owner, group)
	return nil
}

// CanWriteDir reports whether u may create or remove direct children of p.
func (v *VFS) CanWriteDir(p string, u *User) bool {
	if u == nil {
		return false
	}
	if u.UID == 0 {
		return true
	}
	p = path.Clean(p)
	n, ok := v.Get(p)
	if !ok || !n.IsDir || !v.AccessiblePath(p, u) {
		return false
	}
	return v.canWriteMode(n, u) && v.canExecMode(n, u)
}

// CanWriteFile reports whether u may update an existing regular file.
func (v *VFS) CanWriteFile(p string, u *User) bool {
	if u == nil {
		return false
	}
	if u.UID == 0 {
		return true
	}
	n, ok := v.Get(p)
	return ok && !n.IsDir && v.AccessiblePath(p, u) && v.canWriteMode(n, u)
}

// CanRemove reports whether u may remove p from its parent directory.
func (v *VFS) CanRemove(p string, u *User) bool {
	if u == nil {
		return false
	}
	if u.UID == 0 {
		return true
	}
	p = path.Clean(p)
	n, ok := v.Get(p)
	if !ok || !v.AccessiblePath(p, u) || !v.CanWriteDir(path.Dir(p), u) {
		return false
	}
	parent, ok := v.Get(path.Dir(p))
	if ok && parent.Mode&01000 != 0 && n.Owner != u.Name {
		return false
	}
	return true
}

// MkdirAllChecked creates directories only when u can modify the nearest
// existing parent directory. Existing directory trees are left unchanged.
func (v *VFS) MkdirAllChecked(p string, mode uint32, u *User) error {
	if u == nil {
		return fmt.Errorf("permission denied")
	}
	p = path.Clean(p)
	if n, ok := v.Get(p); ok {
		if n.IsDir {
			return nil
		}
		return fmt.Errorf("file exists")
	}
	parent := path.Dir(p)
	for {
		n, ok := v.Get(parent)
		if ok {
			if !n.IsDir || !v.CanWriteDir(parent, u) {
				return fmt.Errorf("permission denied")
			}
			break
		}
		next := path.Dir(parent)
		if next == parent {
			return fmt.Errorf("no such file or directory")
		}
		parent = next
	}
	v.MkdirAll(p, mode, u.Name, u.Name)
	return nil
}

func (v *VFS) canWriteMode(n *INode, u *User) bool {
	perm := n.Mode.Perm()
	switch {
	case n.Owner == u.Name:
		return perm&0200 != 0
	case inAnyGroup(u, n.Group):
		return perm&0020 != 0
	default:
		return perm&0002 != 0
	}
}

func (v *VFS) canExecMode(n *INode, u *User) bool {
	perm := n.Mode.Perm()
	switch {
	case n.Owner == u.Name:
		return perm&0100 != 0
	case inAnyGroup(u, n.Group):
		return perm&0010 != 0
	default:
		return perm&0001 != 0
	}
}

func (v *VFS) WriteBytes(p string, data []byte, mode uint32, owner, group string) {
	v.Write(p, "", mode, owner, group)
	v.Nodes[p].Data = data
}

func (v *VFS) Append(p string, data []byte) error {
	p = path.Clean(p)
	n, ok := v.Nodes[p]
	if !ok {
		return fs.ErrNotExist
	}
	n.Data = append(n.Data, data...)
	n.MTime = time.Now()
	return nil
}

func (v *VFS) Read(p string) ([]byte, bool) {
	p = path.Clean(p)
	for i := 0; i < 8; i++ { // resolve symlinks
		n, ok := v.Nodes[p]
		if !ok {
			return nil, false
		}
		if n.Mode&fs.ModeSymlink != 0 {
			p = path.Clean(path.Join(path.Dir(p), n.Symlink))
			continue
		}
		return n.Data, true
	}
	return nil, false
}

// CanRead answers whether u may read p, using real Unix semantics: root reads
// everything, the owner uses the user bits, a group member uses the group bits,
// everyone else uses the other bits. Reading is checked because a world where
// only writes are enforced has no confidentiality at all — /etc/shadow would be
// readable by any account, and every "compromise" would be a fiction.
func (v *VFS) CanRead(p string, u *User) bool {
	if u == nil || u.UID == 0 {
		return true
	}
	n, ok := v.Get(p)
	if !ok {
		return false
	}
	if n.IsDir {
		return v.CanExec(p, u)
	}
	perm := n.Mode.Perm()
	switch {
	case n.Owner == u.Name:
		return perm&0400 != 0
	case inAnyGroup(u, n.Group):
		return perm&0040 != 0
	default:
		return perm&0004 != 0
	}
}

// CanExec answers whether u may traverse a directory (the x bit), which is what
// gates reaching the paths inside it.
func (v *VFS) CanExec(p string, u *User) bool {
	if u == nil || u.UID == 0 {
		return true
	}
	n, ok := v.Get(p)
	if !ok {
		return true // a missing component is handled as "no such file" elsewhere
	}
	perm := n.Mode.Perm()
	switch {
	case n.Owner == u.Name:
		return perm&0100 != 0
	case inAnyGroup(u, n.Group):
		return perm&0010 != 0
	default:
		return perm&0001 != 0
	}
}

// AccessiblePath reports whether u may reach p at all: every directory on the
// way must grant x. A path whose components are unreadable is "permission
// denied"; a path that simply is not there must still say "no such file", so
// this is deliberately separate from whether the file exists.
func (v *VFS) AccessiblePath(p string, u *User) bool {
	if u == nil || u.UID == 0 {
		return true
	}
	p = path.Clean(p)
	for cur := path.Dir(p); cur != "/" && cur != "."; cur = path.Dir(cur) {
		if !v.CanExec(cur, u) {
			return false
		}
	}
	return true
}

// ReadPathAs reads p as u, walking every component so a directory's x bit really
// gates the files inside it. Returns (data, exists, permitted) so a caller can
// tell "you may not read this" from "there is nothing there".
func (v *VFS) ReadPathAs(p string, u *User) ([]byte, bool, bool) {
	data, ok := v.Read(p)
	if !ok {
		return nil, false, false // no such file — NOT a permission problem
	}
	if !v.AccessiblePath(p, u) || !v.CanRead(p, u) {
		return nil, true, false
	}
	return data, true, true
}

func inAnyGroup(u *User, group string) bool {
	if group == "" || group == u.Name {
		return true
	}
	for _, g := range u.Groups {
		if g == group {
			return true
		}
	}
	return false
}

func (v *VFS) Get(p string) (*INode, bool) {
	n, ok := v.Nodes[path.Clean(p)]
	return n, ok
}

func (v *VFS) Exists(p string) bool { _, ok := v.Get(p); return ok }

func (v *VFS) IsDir(p string) bool {
	n, ok := v.Get(p)
	return ok && n.IsDir
}

func (v *VFS) Symlink(target, link string) {
	link = path.Clean(link)
	v.MkdirAll(path.Dir(link), 0755, "root", "root")
	v.Nodes[link] = &INode{Path: link, Mode: 0777 | fs.ModeSymlink, Owner: "root", Group: "root", Symlink: target, MTime: time.Now()}
}

func (v *VFS) Remove(p string) bool {
	p = path.Clean(p)
	if _, ok := v.Nodes[p]; !ok {
		return false
	}
	delete(v.Nodes, p)
	// remove children of dirs (recursive)
	for k := range v.Nodes {
		if strings.HasPrefix(k, p+"/") {
			delete(v.Nodes, k)
		}
	}
	return true
}

// List returns direct children names sorted.
func (v *VFS) List(dir string) []string {
	dir = path.Clean(dir)
	var out []string
	for k, n := range v.Nodes {
		if k == dir {
			continue
		}
		parent := "."
		if slash := strings.LastIndexByte(k, '/'); slash >= 0 {
			parent = k[:slash]
			if parent == "" {
				parent = "/"
			}
		}
		if parent == dir {
			out = append(out, n.Path)
		}
	}
	// Dedupe and sort returned paths.
	sort.Strings(out)
	res := make([]string, 0, len(out))
	for _, p := range out {
		if len(res) == 0 || p != res[len(res)-1] {
			res = append(res, p)
		}
	}
	return res
}

func (v *VFS) DiskUsedMB() int {
	total := 0
	for _, n := range v.Nodes {
		if !n.IsDir {
			total += len(n.Data)
		}
	}
	return total/1024/1024 + 1
}

var _ = fmt.Sprint
