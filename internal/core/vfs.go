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
	existing, has := v.Nodes[p]
	if !strings.HasPrefix(p, "/tmp") && !strings.HasPrefix(p, "/home/"+u.Name) && u.UID != 0 {
		if !has {
			return fmt.Errorf("permission denied")
		}
		if existing.Owner != u.Name && existing.Mode&0002 == 0 {
			return fmt.Errorf("permission denied")
		}
	}
	mode := uint32(0644)
	owner := u.Name
	if has {
		mode = uint32(existing.Mode.Perm())
		owner = existing.Owner
	}
	v.WriteBytes(p, data, mode, owner, owner)
	return nil
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
		if path.Dir(k) == dir {
			out = append(out, n.Path)
		} else if dir == "/" && !strings.Contains(strings.TrimPrefix(k, "/"), "/") {
			out = append(out, k)
		}
	}
	// dedupe + sort base names by full path
	seen := map[string]bool{}
	var res []string
	sort.Strings(out)
	for _, p := range out {
		if !seen[p] {
			seen[p] = true
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
