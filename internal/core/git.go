package core

// Git (WS-0.8): repositories as real world state. The truth lives in files —
// a repository is a directory of loose objects (blobs, trees, commits named
// by their SHA-1) plus a HEAD, on the server under /srv/git/<name>.git and
// locally under <dir>/.git. A clone really copies objects over the network;
// a push really writes them back; every gate (DNS, route, power, service
// state, TLS, file permissions) applies on the way.
//
// The shape of the git model is owned by this file; World carries no
// pointer — repositories are filesystem state, not a subsystem object.

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// GitEntry is one path in a tree, pointing at its blob.
type GitEntry struct {
	Blob string
	Path string
}

// GitCommitInfo is a commit object's metadata.
type GitCommitInfo struct {
	ID      string
	Parent  string // "" for the root commit
	Author  string
	Date    string
	Message string
	Tree    string
}

// GitDelta is one working-tree difference against HEAD.
type GitDelta struct {
	Path  string
	State string // added|modified|deleted
}

// ServerRepoPath is where the git server keeps its repositories.
func ServerRepoPath(repo string) string { return "/srv/git/" + repo + ".git" }

func gitHash(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// gitWriteFile writes one object file. On the server the actor is the git
// daemon ("git"); locally it is the account that owns the clone, and the
// write goes through the permission checks — creating the parent directory
// first, the way checked-in subdirectories need.
func gitWriteFile(d *Device, base, rel, content, owner string, u *User) error {
	p := base + "/" + rel
	if u != nil {
		if err := d.FS.MkdirAllChecked(path.Dir(p), 0755, u); err != nil {
			return err
		}
		return d.FS.WriteChecked(p, []byte(content), u)
	}
	d.FS.Write(p, content, 0644, owner, "git")
	return nil
}

func gitReadFile(d *Device, base, rel string) (string, bool) {
	data, ok := d.FS.Read(base + "/" + rel)
	return string(data), ok
}

// writeGitBlob stores content as a blob and returns its SHA-1.
func writeGitBlob(d *Device, base, content, owner string, u *User) (string, error) {
	sha := gitHash(content)
	if _, exists := gitReadFile(d, base, "objects/blob/"+sha); exists {
		return sha, nil
	}
	return sha, gitWriteFile(d, base, "objects/blob/"+sha, content, owner, u)
}

// writeGitTree stores a tree manifest (lines of "<blob-sha> <path>") and
// returns its SHA-1.
func writeGitTree(d *Device, base string, entries []GitEntry, owner string, u *User) (string, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s %s\n", e.Blob, e.Path)
	}
	sha := gitHash(b.String())
	if _, exists := gitReadFile(d, base, "objects/tree/"+sha); exists {
		return sha, nil
	}
	return sha, gitWriteFile(d, base, "objects/tree/"+sha, b.String(), owner, u)
}

func readGitTree(d *Device, base, sha string) []GitEntry {
	raw, ok := gitReadFile(d, base, "objects/tree/"+sha)
	if !ok {
		return nil
	}
	var out []GitEntry
	for _, line := range strings.Split(raw, "\n") {
		f := strings.SplitN(line, " ", 2)
		if len(f) == 2 && f[0] != "" {
			out = append(out, GitEntry{Blob: f[0], Path: f[1]})
		}
	}
	return out
}

// writeGitCommit stores a commit object and returns its SHA-1.
func writeGitCommit(d *Device, base string, c GitCommitInfo, owner string, u *User) (string, error) {
	parent := c.Parent
	if parent == "" {
		parent = "none"
	}
	raw := fmt.Sprintf("tree %s\nparent %s\nauthor %s\ndate %s\nmessage %s\n",
		c.Tree, parent, c.Author, c.Date, c.Message)
	sha := gitHash(raw)
	if _, exists := gitReadFile(d, base, "objects/commit/"+sha); exists {
		return sha, nil
	}
	return sha, gitWriteFile(d, base, "objects/commit/"+sha, raw, owner, u)
}

func readGitCommit(d *Device, base, sha string) (GitCommitInfo, bool) {
	raw, ok := gitReadFile(d, base, "objects/commit/"+sha)
	if !ok {
		return GitCommitInfo{}, false
	}
	c := GitCommitInfo{ID: sha}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "tree "):
			c.Tree = strings.TrimPrefix(line, "tree ")
		case strings.HasPrefix(line, "parent "):
			if p := strings.TrimPrefix(line, "parent "); p != "none" {
				c.Parent = p
			}
		case strings.HasPrefix(line, "author "):
			c.Author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "date "):
			c.Date = strings.TrimPrefix(line, "date ")
		case strings.HasPrefix(line, "message "):
			c.Message = strings.TrimPrefix(line, "message ")
		}
	}
	return c, true
}

// gitLog walks the parent chain from HEAD, oldest last.
func gitLog(d *Device, base string) []GitCommitInfo {
	head, ok := gitReadFile(d, base, "HEAD")
	if !ok || strings.TrimSpace(head) == "" {
		return nil
	}
	var out []GitCommitInfo
	for id := strings.TrimSpace(head); id != ""; {
		c, ok := readGitCommit(d, base, id)
		if !ok {
			break
		}
		out = append(out, c)
		id = c.Parent
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// gitTreeManifest flattens a tree into path→blob, recursing into paths that
// end with "/" (directories are just paths with a trailing slash — no tree
// objects for subdirectories, one flat manifest per commit).
func gitTreeManifest(d *Device, base, tree string) map[string]string {
	out := map[string]string{}
	for _, e := range readGitTree(d, base, tree) {
		if strings.HasSuffix(e.Path, "/") {
			for k, v := range gitTreeManifest(d, base, e.Blob) {
				out[e.Path+k] = v
			}
			continue
		}
		out[e.Path] = e.Blob
	}
	return out
}

// gitWalkFS collects every file under dir (skipping .git), keyed by path
// relative to dir. Unreadable files are reported, never silently skipped.
func gitWalkFS(d *Device, dir string, u *User) (map[string]string, []string, error) {
	files := map[string]string{}
	var unreadable []string
	var walk func(p, rel string)
	walk = func(p, rel string) {
		for _, name := range d.FS.List(p) {
			r := strings.TrimPrefix(name, p+"/")
			if r == ".git" || r == "" {
				continue
			}
			if d.FS.IsDir(name) {
				walk(name, rel+r+"/")
				continue
			}
			data, exists, allowed := d.FS.ReadPathAs(name, u)
			if exists && !allowed {
				unreadable = append(unreadable, rel+r)
				continue
			}
			files[rel+r] = gitHash(string(data))
		}
	}
	walk(dir, "")
	sort.Strings(unreadable)
	return files, unreadable, nil
}

// GitLog returns the commit history of the repository containing dir,
// oldest first.
func (w *World) GitLog(d *Device, dir string) ([]GitCommitInfo, error) {
	repo, ok := findGitRepo(d, dir)
	if !ok {
		return nil, fmt.Errorf("fatal: not a git repository: %s", dir)
	}
	log := gitLog(d, repo+"/.git")
	if len(log) == 0 {
		return nil, fmt.Errorf("fatal: your current branch 'main' does not have any commits yet")
	}
	return log, nil
}

// ---- local repository operations (status / commit) ----

// findGitRepo walks up from dir looking for a .git directory, like real git.
func findGitRepo(d *Device, dir string) (repo string, ok bool) {
	p := dir
	for {
		if d.FS.IsDir(p + "/.git") {
			return p, true
		}
		if p == "/" || p == "." || p == "" {
			return "", false
		}
		p = filepath.Dir(p)
	}
}

// GitStatus compares the working tree with HEAD. It only answers for a
// directory that really is a repository.
func (w *World) GitStatus(d *Device, dir string, u *User) ([]GitDelta, error) {
	repo, ok := findGitRepo(d, dir)
	if !ok {
		return nil, fmt.Errorf("fatal: not a git repository: %s", dir)
	}
	base := repo + "/.git"
	work, unreadable, _ := gitWalkFS(d, repo, u)
	if len(unreadable) > 0 {
		return nil, fmt.Errorf("cannot read tracked candidate: %s", unreadable[0])
	}
	head, _ := gitReadFile(d, base, "HEAD")
	headTree := map[string]string{}
	if hc := strings.TrimSpace(head); hc != "" {
		c, ok := readGitCommit(d, base, hc)
		if !ok {
			return nil, fmt.Errorf("fatal: repository is corrupt: HEAD points nowhere")
		}
		headTree = gitTreeManifest(d, base, c.Tree)
	}
	var out []GitDelta
	seen := map[string]bool{}
	for path, sha := range work {
		seen[path] = true
		old, tracked := headTree[path]
		switch {
		case !tracked:
			out = append(out, GitDelta{Path: path, State: "added"})
		case old != sha:
			out = append(out, GitDelta{Path: path, State: "modified"})
		}
	}
	for path := range headTree {
		if !seen[path] {
			out = append(out, GitDelta{Path: path, State: "deleted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// GitCommit commits the whole working tree (the honest equivalent of
// `commit -a`: this world does not pretend to model the index). Refuses
// nothing-to-commit and unreadable files.
func (w *World) GitCommit(d *Device, dir string, u *User, msg string) (GitCommitInfo, error) {
	repo, ok := findGitRepo(d, dir)
	if !ok {
		return GitCommitInfo{}, fmt.Errorf("fatal: not a git repository: %s", dir)
	}
	base := repo + "/.git"
	if strings.TrimSpace(msg) == "" {
		return GitCommitInfo{}, fmt.Errorf("abort: empty commit message")
	}
	deltas, err := w.GitStatus(d, repo, u)
	if err != nil {
		return GitCommitInfo{}, err
	}
	if len(deltas) == 0 {
		return GitCommitInfo{}, fmt.Errorf("nothing to commit, working tree clean")
	}
	// build the tree from the current working tree contents
	work, _, _ := gitWalkFS(d, repo, u)
	entries := make([]GitEntry, 0, len(work))
	for path, sha := range work {
		entries = append(entries, GitEntry{Blob: sha, Path: path})
	}
	tree, err := writeGitTree(d, base, entries, u.Name, u)
	if err != nil {
		return GitCommitInfo{}, err
	}
	// blobs must exist too: rewrite every file's content into the object store
	for path := range work {
		data, _, allowed := d.FS.ReadPathAs(repo+"/"+path, u)
		if !allowed {
			return GitCommitInfo{}, fmt.Errorf("cannot read %s", path)
		}
		if _, err := writeGitBlob(d, base, string(data), u.Name, u); err != nil {
			return GitCommitInfo{}, err
		}
	}
	head, _ := gitReadFile(d, base, "HEAD")
	parent := strings.TrimSpace(head)
	stamp := w.Sim.Format("2006-01-02 15:04")
	c := GitCommitInfo{Parent: parent, Author: u.Name, Date: stamp, Message: strings.TrimSpace(msg), Tree: tree}
	id, err := writeGitCommit(d, base, c, u.Name, u)
	if err != nil {
		return GitCommitInfo{}, err
	}
	c.ID = id
	if err := gitWriteFile(d, base, "HEAD", id+"\n", u.Name, u); err != nil {
		return GitCommitInfo{}, err
	}
	d.Logf("info", "git", "committed %s on %s by %s", id[:8], repo, u.Name)
	return c, nil
}

// ---- transport ----

type gitURL struct {
	Scheme string // http | https
	User   string
	Pass   string
	Host   string
	Port   int
	Repo   string
}

// parseGitURL accepts the forms this world's client advertises:
//   https?://[user[:pass]@]host[:port]/<name>.git
func parseGitURL(raw string) (gitURL, error) {
	g := gitURL{Scheme: "http", Port: 80}
	rest := raw
	switch {
	case strings.HasPrefix(rest, "https://"):
		g.Scheme, g.Port, rest = "https", 443, rest[8:]
	case strings.HasPrefix(rest, "http://"):
		rest = rest[7:]
	default:
		return g, fmt.Errorf("git: only http(s) remotes are supported: %s", raw)
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		hostpart := rest[:i]
		pathpart := rest[i+1:]
		rest = hostpart
		repo := strings.TrimSuffix(pathpart, ".git")
		// one path segment, no traversal: <name>.git and nothing else
		if repo == "" || filepath.Base(pathpart) != pathpart || strings.Contains(repo, "..") {
			return g, fmt.Errorf("git: bad repository path %q", pathpart)
		}
		g.Repo = repo
	} else {
		return g, fmt.Errorf("git: no repository in url %s", raw)
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		g.Host = rest[i+1:]
		cred := rest[:i]
		if j := strings.Index(cred, ":"); j >= 0 {
			g.User, g.Pass = cred[:j], cred[j+1:]
		} else {
			g.User = cred
		}
	} else {
		g.Host = rest
	}
	if i := strings.LastIndex(g.Host, ":"); i >= 0 {
		n := 0
		if _, err := fmt.Sscanf(g.Host[i+1:], "%d", &n); err == nil && n > 0 {
			g.Port = n
			g.Host = g.Host[:i]
		}
	}
	if g.Host == "" || g.Repo == "" {
		return g, fmt.Errorf("git: incomplete url %s", raw)
	}
	return g, nil
}

// gitRemote resolves and dials a remote's URL, returning the server device
// and its repository base path. The connection is the gate — DNS, route,
// power, service state and, for https, the whole TLS handshake apply.
func (w *World) gitRemote(src *Device, raw string) (*Device, string, gitURL, error) {
	g, err := parseGitURL(raw)
	if err != nil {
		return nil, "", g, err
	}
	ip, ok, how := DNSAnswer(src, g.Host)
	if !ok {
		return nil, "", g, fmt.Errorf("git: could not resolve host %s: %s", g.Host, how)
	}
	svc, dst, msg := Dial(src, ip, g.Port)
	if svc == nil {
		return nil, "", g, fmt.Errorf("git: connect to %s (%s): %s", g.Host, ip, msg)
	}
	if svc.Handler != "http-git" {
		return nil, "", g, fmt.Errorf("git: %s:%d is not a git server", g.Host, g.Port)
	}
	if g.Scheme == "https" {
		if _, _, err := w.Handshake(src, g.Host, svc); err != nil {
			return nil, "", g, err
		}
	}
	base := ServerRepoPath(g.Repo)
	if !dst.FS.IsDir(base) {
		return nil, "", g, fmt.Errorf("git: repository %s not found on %s", g.Repo, g.Host)
	}
	return dst, base, g, nil
}

// gitFetchObjects copies every object the local store is missing from the
// server (dumb-http style: clone and pull are object copies, honestly).
func (w *World) gitFetchObjects(dst *Device, serverBase string, d *Device, localBase string, u *User) error {
	for _, kind := range []string{"blob", "tree", "commit"} {
		for _, p := range dst.FS.List(serverBase + "/objects/" + kind) {
			sha := filepath.Base(p)
			if _, exists := gitReadFile(d, localBase, "objects/"+kind+"/"+sha); exists {
				continue
			}
			data, ok := dst.FS.Read(p)
			if !ok {
				continue
			}
			if err := gitWriteFile(d, localBase, "objects/"+kind+"/"+sha, string(data), u.Name, u); err != nil {
				return err
			}
		}
	}
	return nil
}

// gitCheckout materialises a tree into the working directory as the user,
// removing tracked files that the new tree no longer has.
func gitCheckout(d *Device, repo, base, tree string, old map[string]string, u *User) error {
	newTree := gitTreeManifest(d, base, tree)
	for path := range old {
		if _, keep := newTree[path]; !keep {
			d.FS.Remove(repo + "/" + path)
		}
	}
	for path, sha := range newTree {
		content, _ := gitReadFile(d, base, "objects/blob/"+sha)
		full := repo + "/" + path
		if i := strings.LastIndex(full, "/"); i > 0 {
			d.FS.MkdirAllChecked(full[:i], 0755, u)
		}
		if err := d.FS.WriteChecked(full, []byte(content), u); err != nil {
			return err
		}
	}
	return nil
}

// GitRepoNameFrom extracts the repository name a clone would use by default
// (the "<name>" in <name>.git).
func GitRepoNameFrom(raw string) (string, error) {
	g, err := parseGitURL(raw)
	if err != nil {
		return "", err
	}
	return g.Repo, nil
}

// GitClone copies a remote repository into target (the checkout directory,
// exactly as the caller names it) and checks it out.
func (w *World) GitClone(src *Device, raw, target string, u *User) (string, error) {
	dst, serverBase, g, err := w.gitRemote(src, raw)
	if err != nil {
		return "", err
	}
	target = strings.TrimSuffix(target, "/")
	if target == "" || target == "." {
		return "", fmt.Errorf("fatal: bad clone target")
	}
	if src.FS.Exists(target) {
		return "", fmt.Errorf("fatal: destination path %q already exists", target)
	}
	if err := src.FS.MkdirAllChecked(target+"/.git/objects", 0755, u); err != nil {
		return "", err
	}
	localBase := target + "/.git"
	head, _ := gitReadFile(dst, serverBase, "HEAD")
	if strings.TrimSpace(head) == "" {
		return "", fmt.Errorf("git: remote repository %s is empty", g.Repo)
	}
	if err := w.gitFetchObjects(dst, serverBase, src, localBase, u); err != nil {
		return "", err
	}
	if err := gitWriteFile(src, localBase, "HEAD", head, u.Name, u); err != nil {
		return "", err
	}
	if err := gitWriteFile(src, localBase, "remote", raw+"\n", u.Name, u); err != nil {
		return "", err
	}
	c, ok := readGitCommit(src, localBase, strings.TrimSpace(head))
	if !ok {
		return "", fmt.Errorf("git: remote repository is corrupt")
	}
	if err := gitCheckout(src, target, localBase, c.Tree, nil, u); err != nil {
		return "", err
	}
	src.Logf("info", "git", "cloned %s from %s into %s", g.Repo, g.Host, target)
	return target, nil
}

// gitRemoteHead returns the remote's HEAD commit id.
func gitRemoteHead(dst *Device, serverBase string) string {
	head, _ := gitReadFile(dst, serverBase, "HEAD")
	return strings.TrimSpace(head)
}

// GitPull fetches the remote and fast-forwards the local checkout. It
// refuses honestly: dirty trees, non-fast-forwards, no remote.
func (w *World) GitPull(d *Device, dir string, u *User) (string, error) {
	repo, ok := findGitRepo(d, dir)
	if !ok {
		return "", fmt.Errorf("fatal: not a git repository: %s", dir)
	}
	base := repo + "/.git"
	raw, _ := gitReadFile(d, base, "remote")
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("git: no remote configured")
	}
	dst, serverBase, g, err := w.gitRemote(d, strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	head, _ := gitReadFile(d, base, "HEAD")
	local := strings.TrimSpace(head)
	remote := gitRemoteHead(dst, serverBase)
	if local == remote {
		return "Already up to date.", nil
	}
	deltas, err := w.GitStatus(d, repo, u)
	if err != nil {
		return "", err
	}
	if len(deltas) > 0 {
		return "", fmt.Errorf("error: your local changes would be overwritten by pull; commit them first")
	}
	rc, ok := readGitCommit(d, base, remote)
	if !ok {
		if err := w.gitFetchObjects(dst, serverBase, d, base, u); err != nil {
			return "", err
		}
		rc, ok = readGitCommit(d, base, remote)
		if !ok {
			return "", fmt.Errorf("git: remote repository is corrupt")
		}
	} else if err := w.gitFetchObjects(dst, serverBase, d, base, u); err != nil {
		return "", err
	}
	// fast-forward only: the remote HEAD's parent chain must contain the
	// local HEAD — a diverged history needs a merge, which this world does
	// not pretend to do
	ff := false
	for id := rc.Parent; id != ""; {
		if id == local {
			ff = true
			break
		}
		pc, ok := readGitCommit(d, base, id)
		if !ok {
			break
		}
		id = pc.Parent
	}
	if !ff && local != "" {
		return "", fmt.Errorf("fatal: not possible to fast-forward, %s and %s diverged", local[:8], remote[:8])
	}
	oldTree := map[string]string{}
	if lc, ok := readGitCommit(d, base, local); ok {
		oldTree = gitTreeManifest(d, base, lc.Tree)
	}
	if err := gitWriteFile(d, base, "HEAD", remote+"\n", u.Name, u); err != nil {
		return "", err
	}
	if err := gitCheckout(d, repo, base, rc.Tree, oldTree, u); err != nil {
		return "", err
	}
	d.Logf("info", "git", "pulled %s from %s: HEAD is now %s", g.Repo, g.Host, remote[:8])
	return fmt.Sprintf("Updating %s..%s\nFast-forward", local[:8], remote[:8]), nil
}

// GitPush uploads local commits to the remote. Pushing is a write: the
// server requires the account's real credentials (anonymous push is not a
// thing on any real server), and only fast-forwards.
func (w *World) GitPush(d *Device, dir string, u *User, user, pass string) (string, error) {
	repo, ok := findGitRepo(d, dir)
	if !ok {
		return "", fmt.Errorf("fatal: not a git repository: %s", dir)
	}
	base := repo + "/.git"
	raw, _ := gitReadFile(d, base, "remote")
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("git: no remote configured")
	}
	dst, serverBase, g, err := w.gitRemote(d, strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	// authentication is against the server's own account records — the same
	// simulated check ssh, sudo and imapd perform
	acc := dst.FindUser(user)
	if !acc.CheckPassword(pass) {
		dst.Logf("notice", "git", "push authentication failed for %s from %s", user, d.SourceIPFor(dst))
		return "", fmt.Errorf("fatal: authentication failed for %s", g.Host)
	}
	local := strings.TrimSpace(headOf(d, base))
	if local == "" {
		return "", fmt.Errorf("error: nothing to push (no commits)")
	}
	server := gitRemoteHead(dst, serverBase)
	if server == local {
		return "Everything up-to-date", nil
	}
	lc, ok := readGitCommit(d, base, local)
	if !ok {
		return "", fmt.Errorf("git: local repository is corrupt")
	}
	if server != "" && lc.Parent != server {
		return "", fmt.Errorf("to git.neohome.example:%s.git\n ! [rejected] %s -> %s (fetch first)", g.Repo, g.Repo, g.Repo)
	}
	if err := pushObjects(d, base, dst, serverBase); err != nil {
		return "", err
	}
	if err := gitWriteFile(dst, serverBase, "HEAD", local+"\n", "git", nil); err != nil {
		return "", err
	}
	dst.Logf("info", "git", "%s pushed %s to %s (%s)", u.Name, local[:8], g.Repo, g.Host)
	return fmt.Sprintf("To %s/%s.git\n   %s..%s  main -> main", g.Host, g.Repo, shortOrEmpty(server), local[:8]), nil
}

func headOf(d *Device, base string) string {
	h, _ := gitReadFile(d, base, "HEAD")
	return h
}

func shortOrEmpty(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// pushObjects copies missing objects from the local store to the server,
// written as the git daemon user (the daemon owns its repository).
func pushObjects(src *Device, localBase string, dst *Device, serverBase string) error {
	for _, kind := range []string{"blob", "tree", "commit"} {
		for _, p := range src.FS.List(localBase + "/objects/" + kind) {
			sha := filepath.Base(p)
			if _, exists := gitReadFile(dst, serverBase, "objects/"+kind+"/"+sha); exists {
				continue
			}
			data, ok := src.FS.Read(p)
			if !ok {
				continue
			}
			dst.FS.Write(serverBase+"/objects/"+kind+"/"+sha, string(data), 0644, "git", "git")
		}
	}
	return nil
}

// ---- server-side seeding helpers ----

// seedGit ships the world's public repositories with real history: each
// commit below is a real object on disk, and the messages tell the same
// story the rest of the world tells. Called from world_init after seedTLS
// (the nginx unit needs its certificate first).
func seedGit(w *World) {
	d := w.Devices["git"]
	if d == nil {
		return
	}
	d.FS.MkdirAll("/srv/git", 0755, "root", "root")
	for _, repo := range []string{"neohome-scripts", "dotfiles"} {
		d.FS.MkdirAll(ServerRepoPath(repo)+"/objects/blob", 0755, "git", "git")
		d.FS.MkdirAll(ServerRepoPath(repo)+"/objects/tree", 0755, "git", "git")
		d.FS.MkdirAll(ServerRepoPath(repo)+"/objects/commit", 0755, "git", "git")
		d.FS.Write(ServerRepoPath(repo)+"/description", repo+" — public repository\n", 0644, "git", "git")
		d.FS.Write(ServerRepoPath(repo)+"/HEAD", "\n", 0644, "git", "git")
	}
	GitSeedCommit(w, "neohome-scripts", map[string]string{
		"backup.sh":      "#!/bin/sh\n# nightly mirror of the NAS onto the assistant node\ntar -czf /srv/backup/home-$(date +%F).tgz /home\n",
		"mirror-probe.sh": "#!/bin/sh\ncurl -s -o /tmp/probe.log http://mirror.neohome.example/debian/Release\n",
	}, "alex", "init: backup and mirror probe scripts")
	GitSeedCommit(w, "neohome-scripts", map[string]string{
		"backup.sh": "#!/bin/sh\n# nightly mirror of the NAS onto the assistant node\ntar -czf /srv/backup/home-$(date +%F).tgz /home\nlogger -t backup \"home mirror done\"\n",
	}, "alex", "log a line to syslog when the backup finishes")
	GitSeedCommit(w, "dotfiles", map[string]string{
		"gitprompt.sh": "# show the branch in the prompt\nparse_git_branch() { echo \" ($1)\"; }\n",
		"tmux.conf":    "set -g default-shell /bin/bash\nset -g status-style bg=black\n",
	}, "alex", "init: prompt and tmux config")
	GitSeedCommit(w, "dotfiles", map[string]string{
		"tmux.conf": "set -g default-shell /bin/bash\nset -g status-style bg=blue\nset -g mouse on\n",
	}, "alex", "tmux: status blue, mouse on")
}

// GitSeedCommit appends a commit to a server repository and moves its HEAD,
// starting from the repository's current tree. Used by seedGit so the world
// ships repositories with real history; the actor is the git daemon.
func GitSeedCommit(w *World, repo string, files map[string]string, author, msg string) error {
	d := w.Devices["git"]
	if d == nil {
		return fmt.Errorf("git: no server in this world")
	}
	base := ServerRepoPath(repo)
	parent := gitRemoteHead(d, base)
	work := map[string]string{}
	// start from the current tree so a new commit extends, not replaces
	if parent != "" {
		if c, ok := readGitCommit(d, base, parent); ok {
			for path, sha := range gitTreeManifest(d, base, c.Tree) {
				if content, ok := gitReadFile(d, base, "objects/blob/"+sha); ok {
					work[path] = content
				}
			}
		}
	}
	for path, content := range files {
		work[path] = content
	}
	entries := make([]GitEntry, 0, len(work))
	blobs := map[string]string{}
	for path, content := range work {
		sha := gitHash(content)
		blobs[sha] = content
		entries = append(entries, GitEntry{Blob: sha, Path: path})
	}
	for sha, content := range blobs {
		if err := gitWriteFile(d, base, "objects/blob/"+sha, content, "git", nil); err != nil {
			return err
		}
	}
	tree, err := writeGitTree(d, base, entries, "git", nil)
	if err != nil {
		return err
	}
	c := GitCommitInfo{Parent: parent, Author: author, Date: w.Sim.Format("2006-01-02 15:04"), Message: msg, Tree: tree}
	id, err := writeGitCommit(d, base, c, "git", nil)
	if err != nil {
		return err
	}
	return gitWriteFile(d, base, "HEAD", id+"\n", "git", nil)
}
