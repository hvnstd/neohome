package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// §33 防守和安全软件 — Backup, as a tool that really keeps versions.
//
// The household already had `backup run` (gadgets.go): one copy of a tree,
// on the NAS, overwritten by the next run. That is a copy, not a backup — a
// deletion or a ransomware rewrite propagates straight through it. The tool
// here is restic-shaped, because that shape is the honest one:
//
//	the repository is REAL FILES on the machine that holds it
//	  config, keys/, data/<xx>/<blob>, snapshots/<id>, index/
//	every blob is content-addressed — its name IS the hash of its bytes
//	every snapshot is a manifest naming the files, their modes and their blobs
//	`check` re-hashes every referenced blob, so a corrupted repository fails
//	`restore` puts the bytes back, and refuses to overwrite without being told
//
// Nothing is cached: the repository's own directory listing is the snapshot
// list, so a snapshot that is not there does not exist, and a file the player
// edits with `nano` changes what `check` says. The configuration lives in
// /etc/restic/env (RESTIC_REPOSITORY, RESTIC_PASSWORD_FILE) like the real
// program, and the repository may be local or on another device — resolved
// through the same packet path as everything else, so a NAS that is down
// fails the backup with the real reason rather than a reassuring message.

// ResticEnvPath is where a machine's restic is pointed at its repository.
const ResticEnvPath = "/etc/restic/env"

// ResticDefaultRepo is the household's own repository on the NAS, which is
// where a backup belongs: off the machine it protects.
const ResticDefaultRepo = "root@nas:/srv/restic"

// ResticRepo is a resolved repository: what the config said, and where that
// turned out to be.
type ResticRepo struct {
	Spec  string  // verbatim from /etc/restic/env
	Host  *Device // nil for a local path
	Path  string
	Local bool
}

// Describe is the one-line identity of a repository, used in reports.
func (r *ResticRepo) Describe() string {
	if r.Local || r.Host == nil {
		return r.Path
	}
	return r.Host.Hostname + ":" + r.Path
}

// ResticEnv reads the machine's restic configuration. A machine that never
// configured a repository has no repository — the same starting point the
// other §33 tools have.
func ResticEnv(d *Device) (repo, passFile string, ok bool) {
	data, has := d.FS.Read(ResticEnvPath)
	if !has {
		return "", "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), "\"'")
		switch strings.TrimSpace(k) {
		case "RESTIC_REPOSITORY":
			repo = v
		case "RESTIC_PASSWORD_FILE":
			passFile = v
		}
	}
	return repo, passFile, repo != ""
}

// ResticRepositoryOf resolves the configured repository against the world.
// A spec with no host is a path on this machine; anything else names a device
// by hostname or address, resolved with the machine's own resolver.
func ResticRepositoryOf(d *Device) (*ResticRepo, error) {
	spec, pass, ok := ResticEnv(d)
	if !ok {
		return nil, fmt.Errorf("no repository configured in %s (set RESTIC_REPOSITORY)", ResticEnvPath)
	}
	spec = strings.TrimPrefix(spec, "sftp:")
	spec = strings.TrimPrefix(spec, "sftp://")
	if i := strings.Index(spec, ":"); i > 0 && !strings.HasPrefix(spec, "/") {
		target := spec[:i]
		path := spec[i+1:]
		host := target
		if j := strings.LastIndex(target, "@"); j >= 0 {
			host = target[j+1:]
		}
		ip, found, how := DNSAnswerFamily(d, host, 4)
		if !found {
			return nil, fmt.Errorf("repository host %s does not resolve (%s)", host, how)
		}
		owner := d.W.Devices[d.W.IPMap[ip]]
		if owner == nil {
			return nil, fmt.Errorf("repository host %s (%s) is not a machine in this world", host, ip)
		}
		if path == "" || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("repository path %q must be absolute", path)
		}
		_ = pass
		return &ResticRepo{Spec: spec, Host: owner, Path: path}, nil
	}
	if !strings.HasPrefix(spec, "/") {
		return nil, fmt.Errorf("repository %q must be an absolute path or host:/path", spec)
	}
	return &ResticRepo{Spec: spec, Host: d, Path: spec, Local: true}, nil
}

// resticReach answers whether the repository can be reached *now*. A local
// repository is on this machine; a remote one is a connection over the ssh
// path, which means a NAS that is down, unplugged or banned really fails.
func (r *ResticRepo) reach(src *Device) error {
	if r.Local || r.Host == nil || r.Host == src {
		return nil
	}
	svc, _, msg := Dial(src, r.Host.FirstLANIP(), 22)
	if svc == nil || msg != "connected" {
		return fmt.Errorf("repository %s unreachable: %s", r.Describe(), msg)
	}
	return nil
}

// store is the device whose filesystem holds the repository.
func (r *ResticRepo) store(src *Device) *Device {
	if r.Local || r.Host == nil {
		return src
	}
	return r.Host
}

// actorFor resolves the account the repository is written with: the ssh user
// named in the spec (root for the household default), or the local operator.
// A repository whose directory the writer cannot write is a real error, and it
// surfaces as one because every write below goes through the filesystem's own
// permission and free-space gate.
func (r *ResticRepo) actorFor(src *Device) (*User, error) {
	name := ""
	if i := strings.Index(r.Spec, "@"); i > 0 {
		name = r.Spec[:i]
	}
	st := r.store(src)
	if name == "" {
		if u := st.FindUser("root"); u != nil {
			return u, nil
		}
		for _, u := range st.Users {
			return u, nil
		}
		return &User{Name: "root", UID: 0}, nil
	}
	u := st.FindUser(name)
	if u == nil {
		return nil, fmt.Errorf("repository user %s does not exist on %s", name, st.Hostname)
	}
	return u, nil
}

// mkdir and put are the two acts that create a repository, and both are gated.
func (r *ResticRepo) mkdir(st *Device, p string, actor *User) error {
	return st.FS.MkdirAllChecked(p, 0700, actor)
}

func (r *ResticRepo) put(st *Device, p string, data []byte, actor *User) error {
	return st.WriteGuest(p, data, actor)
}

// ---- the repository's own files -------------------------------------------

func (r *ResticRepo) p(parts ...string) string {
	return strings.TrimRight(r.Path, "/") + "/" + strings.Join(parts, "/")
}

const (
	resticConfig    = "config"
	resticSnapDir   = "snapshots"
	resticDataDir   = "data"
	resticKeysDir   = "keys"
	resticIndexDir  = "index"
	resticBlobLimit = 1 << 20 // one blob per file, up to 1 MiB (stated model limit)
)

// ResticInitialized reports whether the repository on disk looks initialized.
func ResticInitialized(d *Device) bool {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return false
	}
	if err := repo.reach(d); err != nil {
		return false
	}
	_, ok := repo.store(d).FS.Read(repo.p(resticConfig))
	return ok
}

// ResticInit creates a repository: the config file, a key, and the three
// directories blobs, snapshots and the index live in. Running it twice is an
// error, exactly as the real program says.
func ResticInit(d *Device) (string, error) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return "", err
	}
	if err := repo.reach(d); err != nil {
		return "", err
	}
	actor, err := repo.actorFor(d)
	if err != nil {
		return "", err
	}
	st := repo.store(d)
	if _, ok := st.FS.Read(repo.p(resticConfig)); ok {
		return "", fmt.Errorf("repository %s is already initialized", repo.Describe())
	}
	for _, dir := range []string{"", resticSnapDir, resticDataDir, resticKeysDir, resticIndexDir} {
		if err := repo.mkdir(st, repo.p(dir), actor); err != nil {
			return "", fmt.Errorf("cannot create %s: %v", repo.p(dir), err)
		}
	}
	id := contentHash([]byte(repo.Describe() + d.ID + d.W.Sim.Format(time.RFC3339)))
	if err := repo.put(st, repo.p(resticConfig),
		[]byte(fmt.Sprintf("# restic repository\nversion 2\nid %s\ncreated %s\nchunker_polynomial 3da3358b4dc173\n",
			id, d.W.Sim.Format("2006-01-02 15:04:05"))), actor); err != nil {
		return "", fmt.Errorf("cannot write %s: %v", repo.p(resticConfig), err)
	}
	if err := repo.put(st, repo.p(resticKeysDir, id),
		[]byte("# a key file: the repository is password-protected, so losing this loses the backups\n"),
		actor); err != nil {
		return "", fmt.Errorf("cannot write the repository key: %v", err)
	}
	d.Logf("info", "restic", "repository %s initialized (%s)", repo.Describe(), id[:8])
	d.W.AddEvent(d.ID, "info", "restic", "%s initialized a backup repository at %s", d.Hostname, repo.Describe())
	return fmt.Sprintf("created restic repository %s at %s", id[:8], repo.Describe()), nil
}

// ResticSnapshot is one backup: who made it, when, what it contains.
type ResticSnapshot struct {
	ID      string
	At      time.Time
	Host    string
	Paths   []string
	Parent  string
	Entries []ResticEntry
	Files   int
	Bytes   int
	Added   int
	Removed int
	// Seq orders snapshots taken inside the same tick. The world clock moves
	// in 30-second steps, so two backups can honestly share a timestamp; the
	// sequence is what makes "the newest snapshot" a fact rather than a tie.
	Seq  int
	Body string // the manifest verbatim, for anyone who wants to read it
}

// ResticEntry is one file inside a snapshot: where it was, its mode, and the
// blob holding its bytes.
type ResticEntry struct {
	Path  string
	Mode  uint32
	Size  int
	Blob  string
	IsDir bool
}

// ResticSnapshots lists a repository's snapshots, oldest first. The repository
// is the source of truth: a snapshot that was pruned is not in this list
// because its file is gone.
func ResticSnapshots(d *Device) ([]ResticSnapshot, error) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return nil, err
	}
	if err := repo.reach(d); err != nil {
		return nil, err
	}
	return resticSnapshotsOf(repo.store(d), repo.Path), nil
}

// resticSnapshotsOf is the real reader: it takes the device that holds the
// repository so both the local and the remote case go through one path.
func resticSnapshotsOf(st *Device, path string) []ResticSnapshot {
	dir := strings.TrimRight(path, "/") + "/" + resticSnapDir
	var out []ResticSnapshot
	for _, p := range st.FS.List(dir) { // List returns full paths
		name := p[strings.LastIndex(p, "/")+1:]
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		data, ok := st.FS.Read(p)
		if !ok {
			continue
		}
		out = append(out, parseResticSnapshot(name, string(data)))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// parseResticSnapshot reads a manifest. Header lines are `key value`; entries
// are `file <mode> <size> <blob> <path>` and `dir <mode> <path>`.
func parseResticSnapshot(id, body string) ResticSnapshot {
	s := ResticSnapshot{ID: id, Body: body}
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "file ") && !strings.HasPrefix(line, "dir ") {
			k, v, found := strings.Cut(line, " ")
			if !found {
				continue
			}
			switch k {
			case "time":
				if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
					s.At = t
				} else if t, err := time.Parse(time.RFC3339, v); err == nil {
					s.At = t
				}
			case "host":
				s.Host = v
			case "parent":
				s.Parent = v
			case "paths":
				s.Paths = strings.Fields(v)
			case "files":
				s.Files, _ = strconv.Atoi(v)
			case "bytes":
				s.Bytes, _ = strconv.Atoi(v)
			case "added":
				s.Added, _ = strconv.Atoi(v)
			case "removed":
				s.Removed, _ = strconv.Atoi(v)
			case "seq":
				s.Seq, _ = strconv.Atoi(v)
			}
			continue
		}
		isDir := strings.HasPrefix(line, "dir ")
		rest := strings.TrimPrefix(strings.TrimPrefix(line, "file "), "dir ")
		if isDir {
			mode, p := cutField(rest)
			m, _ := strconv.ParseUint(mode, 8, 32)
			s.Entries = append(s.Entries, ResticEntry{Path: p, Mode: uint32(m), IsDir: true})
			continue
		}
		mode, rest := cutField(rest)
		size, rest := cutField(rest)
		blob, p := cutField(rest)
		m, _ := strconv.ParseUint(mode, 8, 32)
		n, _ := strconv.Atoi(size)
		s.Entries = append(s.Entries, ResticEntry{Path: p, Mode: uint32(m), Size: n, Blob: blob})
	}
	return s
}

// cutField splits the first space-delimited field off a line.
func cutField(s string) (string, string) {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// ResticBackup copies a tree into the repository as a new snapshot. The files
// come from the source device's own filesystem, the blobs are content
// addressed, and a blob that is already there is not written again — which is
// what makes the second backup of an unchanged tree cost nothing.
func ResticBackup(d *Device, tree string) (ResticSnapshot, error) {
	var zero ResticSnapshot
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return zero, err
	}
	if err := repo.reach(d); err != nil {
		return zero, err
	}
	st := repo.store(d)
	actor, err := repo.actorFor(d)
	if err != nil {
		return zero, err
	}
	if _, ok := st.FS.Read(repo.p(resticConfig)); !ok {
		return zero, fmt.Errorf("repository %s is not initialized (run `restic init`)", repo.Describe())
	}
	tree = strings.TrimRight(tree, "/")
	if tree == "" {
		tree = "/"
	}
	if _, ok := d.FS.Get(tree); !ok {
		return zero, fmt.Errorf("%s: no such file or directory", tree)
	}

	existing := resticSnapshotsOf(st, repo.Path)
	seq := 1
	parent := ""
	if len(existing) > 0 {
		newest := existing[len(existing)-1]
		parent = newest.ID
		for _, s := range existing {
			if s.Seq >= seq {
				seq = s.Seq + 1
			}
		}
	}
	var entries []ResticEntry
	var paths []string
	addPath := func(p string, n *INode) {
		if n.IsDir {
			entries = append(entries, ResticEntry{Path: p, Mode: uint32(n.Mode.Perm()), IsDir: true})
			return
		}
		if len(n.Data) > resticBlobLimit {
			// stated model limit: one blob per file, and a blob is held in
			// memory, so a giant file is refused rather than silently truncated
			return
		}
		blob := contentHash(n.Data)
		entries = append(entries, ResticEntry{Path: p, Mode: uint32(n.Mode.Perm()), Size: len(n.Data), Blob: blob})
	}
	if n, ok := d.FS.Get(tree); ok {
		addPath(tree, n)
	}
	for p, n := range d.FS.Nodes {
		if p == tree || !strings.HasPrefix(p, tree+"/") {
			continue
		}
		addPath(p, n)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	added := 0
	for i, e := range entries {
		if e.IsDir || e.Blob == "" {
			continue
		}
		if _, ok := st.FS.Read(repo.p(resticDataDir, e.Blob[:2], e.Blob)); ok {
			continue
		}
		n, _ := d.FS.Get(e.Path)
		data := []byte(nil)
		if n != nil {
			data = n.Data
		}
		if err := repo.mkdir(st, repo.p(resticDataDir, e.Blob[:2]), actor); err != nil {
			return zero, fmt.Errorf("cannot write into %s: %v", repo.Describe(), err)
		}
		if err := repo.put(st, repo.p(resticDataDir, e.Blob[:2], e.Blob), data, actor); err != nil {
			return zero, fmt.Errorf("cannot write blob %s into %s: %v", e.Blob[:12], repo.Describe(), err)
		}
		entries[i].Size = len(data)
		added++
	}

	files, bytes := 0, 0
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		files++
		bytes += e.Size
	}
	removed := 0
	if parent != "" {
		for _, s := range existing {
			if s.ID != parent {
				continue
			}
			have := map[string]string{}
			for _, e := range s.Entries {
				if !e.IsDir {
					have[e.Path] = e.Blob
				}
			}
			for _, e := range entries {
				if e.IsDir {
					continue
				}
				if old, ok := have[e.Path]; ok && old == e.Blob {
					delete(have, e.Path)
				}
			}
			removed = len(have)
		}
	}
	snapTime := d.W.Sim

	var b strings.Builder
	fmt.Fprintf(&b, "time %s\n", snapTime.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "host %s\n", d.Hostname)
	fmt.Fprintf(&b, "paths %s\n", tree)
	if parent != "" {
		fmt.Fprintf(&b, "parent %s\n", parent)
	}
	fmt.Fprintf(&b, "seq %d\nfiles %d\nbytes %d\nadded %d\nremoved %d\n", seq, files, bytes, added, removed)
	for _, e := range entries {
		if strings.ContainsAny(e.Path, "\n") {
			continue
		}
		if e.IsDir {
			fmt.Fprintf(&b, "dir %04o %s\n", e.Mode, e.Path)
			continue
		}
		fmt.Fprintf(&b, "file %04o %d %s %s\n", e.Mode, e.Size, e.Blob, e.Path)
	}
	body := b.String()
	id := contentHash([]byte(body))
	if err := repo.mkdir(st, repo.p(resticSnapDir), actor); err != nil {
		return zero, fmt.Errorf("cannot write into %s: %v", repo.Describe(), err)
	}
	if err := repo.mkdir(st, repo.p(resticIndexDir), actor); err != nil {
		return zero, fmt.Errorf("cannot write into %s: %v", repo.Describe(), err)
	}
	if err := repo.put(st, repo.p(resticSnapDir, id), []byte(body), actor); err != nil {
		return zero, fmt.Errorf("cannot save the snapshot: %v", err)
	}
	if err := repo.put(st, repo.p(resticIndexDir, id),
		[]byte(fmt.Sprintf("snapshot %s %s\n", id, snapTime.Format("2006-01-02 15:04:05"))), actor); err != nil {
		return zero, fmt.Errorf("cannot save the index entry: %v", err)
	}

	paths = append(paths, tree)
	snap := ResticSnapshot{ID: id, At: snapTime, Host: d.Hostname, Paths: paths, Parent: parent,
		Entries: entries, Files: files, Bytes: bytes, Added: added, Removed: removed, Seq: seq, Body: body}
	d.Logf("info", "restic", "snapshot %s saved: %d file(s), %d bytes, %d new blob(s) (%s)",
		id[:8], files, bytes, added, repo.Describe())
	d.W.AddEvent(d.ID, "info", "restic", "%s backed up %s to %s: %d file(s), %d new blob(s)",
		d.Hostname, tree, repo.Describe(), files, added)
	return snap, nil
}

// ResticCheck re-hashes every blob the snapshots reference. This is the check
// that separates 'the backup ran' from 'the backup is readable': a blob that
// went missing, or whose bytes changed under it, is an error naming both.
func ResticCheck(d *Device) (checked int, problems []string, err error) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return 0, nil, err
	}
	if err := repo.reach(d); err != nil {
		return 0, nil, err
	}
	st := repo.store(d)
	if _, ok := st.FS.Read(repo.p(resticConfig)); !ok {
		return 0, nil, fmt.Errorf("repository %s is not initialized", repo.Describe())
	}
	snaps := resticSnapshotsOf(st, repo.Path)
	if len(snaps) == 0 {
		return 0, []string{"no snapshots in this repository — nothing is backed up yet"}, nil
	}
	seen := map[string]bool{}
	for _, s := range snaps {
		for _, e := range s.Entries {
			if e.IsDir || e.Blob == "" || seen[e.Blob] {
				continue
			}
			seen[e.Blob] = true
			data, ok := st.FS.Read(repo.p(resticDataDir, e.Blob[:2], e.Blob))
			if !ok {
				problems = append(problems, fmt.Sprintf("blob %s is missing (snapshot %s, %s)", e.Blob[:12], s.ID[:8], e.Path))
				continue
			}
			checked++
			if got := contentHash(data); got != e.Blob {
				problems = append(problems, fmt.Sprintf("blob %s is corrupt: hashes to %s (snapshot %s, %s)",
					e.Blob[:12], got[:12], s.ID[:8], e.Path))
			}
		}
	}
	sort.Strings(problems)
	return checked, problems, nil
}

// ResticRestore writes a snapshot's files back, as the given account (a restore
// into a live tree is a privileged act, and the filesystem says so). Without
// overwrite it refuses to clobber a file that still exists — a restore that
// silently replaces the live tree is how people lose the data they were trying
// to save.
func ResticRestore(d *Device, id, target string, overwrite bool, actor *User) (restored, skipped int, err error) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return 0, 0, err
	}
	if err := repo.reach(d); err != nil {
		return 0, 0, err
	}
	st := repo.store(d)
	if actor == nil {
		actor = d.FindUser("root")
	}
	snaps := resticSnapshotsOf(st, repo.Path)
	if len(snaps) == 0 {
		return 0, 0, fmt.Errorf("no snapshots in %s", repo.Describe())
	}
	snap := &snaps[len(snaps)-1]
	for i := range snaps {
		if snaps[i].ID == id || strings.HasPrefix(snaps[i].ID, id) {
			snap = &snaps[i]
			break
		}
	}
	if id != "latest" && snap.ID != id && !strings.HasPrefix(snap.ID, id) {
		return 0, 0, fmt.Errorf("snapshot %s not found", id)
	}
	target = strings.TrimRight(target, "/")
	for _, e := range snap.Entries {
		p := e.Path
		if target != "" {
			p = target + "/" + strings.TrimPrefix(e.Path, "/")
		}
		if e.IsDir {
			d.FS.MkdirAll(p, e.Mode&0777, "root", "root")
			continue
		}
		if e.Blob == "" {
			continue // a manifest line that names no content is not a file
		}
		if _, exists := d.FS.Get(p); exists && !overwrite {
			skipped++
			continue
		}
		data, ok := st.FS.Read(repo.p(resticDataDir, e.Blob[:2], e.Blob))
		if !ok {
			return restored, skipped, fmt.Errorf("blob %s for %s is missing", e.Blob[:12], e.Path)
		}
		// A blob is named by its own hash, so a restore can tell corruption
		// from content: bytes that do not hash to the name are not the bytes
		// that were backed up, and writing them into the live tree would
		// spread the damage the backup exists to survive.
		if got := contentHash(data); got != e.Blob {
			return restored, skipped, fmt.Errorf("blob %s for %s is corrupt: hashes to %s",
				e.Blob[:12], e.Path, got[:12])
		}
		if dir := dirOf(p); dir != "" && dir != "/" {
			d.FS.MkdirAll(dir, 0755, "root", "root")
		}
		if err := d.WriteGuest(p, data, actor); err != nil {
			return restored, skipped, fmt.Errorf("cannot restore %s: %v", p, err)
		}
		restored++
	}
	d.Logf("info", "restic", "restored snapshot %s: %d file(s) to %s (%d skipped)", snap.ID[:8], restored, target, skipped)
	d.W.AddEvent(d.ID, "info", "restic", "%s restored snapshot %s (%d file(s))", d.Hostname, snap.ID[:8], restored)
	return restored, skipped, nil
}

// ResticForget drops a snapshot, and with prune also every blob no remaining
// snapshot references — the space really comes back.
func ResticForget(d *Device, id string, prune bool) (string, error) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return "", err
	}
	if err := repo.reach(d); err != nil {
		return "", err
	}
	st := repo.store(d)
	snaps := resticSnapshotsOf(st, repo.Path)
	var victim *ResticSnapshot
	for i := range snaps {
		if snaps[i].ID == id || strings.HasPrefix(snaps[i].ID, id) {
			victim = &snaps[i]
			break
		}
	}
	if victim == nil {
		return "", fmt.Errorf("snapshot %s not found", id)
	}
	st.FS.Remove(repo.p(resticSnapDir, victim.ID))
	st.FS.Remove(repo.p(resticIndexDir, victim.ID))
	freed := 0
	if prune {
		keep := map[string]bool{}
		var remain []ResticSnapshot
		for _, s := range snaps {
			if s.ID == victim.ID {
				continue
			}
			remain = append(remain, s)
			for _, e := range s.Entries {
				if e.Blob != "" {
					keep[e.Blob] = true
				}
			}
		}
		seen := map[string]bool{}
		for _, e := range victim.Entries {
			if e.Blob == "" || keep[e.Blob] || seen[e.Blob] {
				continue
			}
			seen[e.Blob] = true
			if data, ok := st.FS.Read(repo.p(resticDataDir, e.Blob[:2], e.Blob)); ok {
				freed += len(data)
			}
			st.FS.Remove(repo.p(resticDataDir, e.Blob[:2], e.Blob))
		}
	}
	msg := fmt.Sprintf("removed snapshot %s", victim.ID[:8])
	if prune {
		msg += fmt.Sprintf(" and pruned %d unreferenced bytes", freed)
	}
	d.Logf("info", "restic", "%s", msg)
	return msg, nil
}

// ResticPosture is what `secstat` prints for backup: where the repository is,
// how many snapshots it holds and how old the newest one is. A repository that
// cannot be reached says so — a backup nobody can read is not a backup.
func ResticPosture(d *Device) (state, detail string) {
	repo, err := ResticRepositoryOf(d)
	if err != nil {
		return "not configured", ResticEnvPath
	}
	if err := repo.reach(d); err != nil {
		return "unreachable", repo.Describe()
	}
	st := repo.store(d)
	if _, ok := st.FS.Read(repo.p(resticConfig)); !ok {
		return "no repository", repo.Describe() + " (run restic init)"
	}
	snaps := resticSnapshotsOf(st, repo.Path)
	if len(snaps) == 0 {
		return "empty", repo.Describe() + " (no snapshots yet)"
	}
	newest := snaps[len(snaps)-1]
	age := d.W.Sim.Sub(newest.At)
	word := HumanAge(age.Round(time.Second))
	if age > 24*time.Hour {
		word += " ago — older than a day"
	} else {
		word += " ago"
	}
	return "ok", fmt.Sprintf("%s (%d snapshot(s), newest %s)", repo.Describe(), len(snaps), word)
}
