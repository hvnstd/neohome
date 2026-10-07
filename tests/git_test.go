package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// gsession drives one persistent shell session: git needs a working
// directory that survives between commands, and stdin for push passwords.
type gsession struct {
	t  *testing.T
	w  *core.World
	d  *core.Device
	sh *shell.Shell
}

func newGSession(t *testing.T, w *core.World, dev *core.Device, user string) *gsession {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	return &gsession{t: t, w: w, d: dev, sh: shell.NewShell(w, dev, u, &bufOut{}, "10.77.1.11", "xterm")}
}

func (g *gsession) exec(line string, stdin ...string) string {
	g.t.Helper()
	out := &bufOut{}
	g.sh.Out = out
	if len(stdin) > 0 {
		g.sh.SetInput(strings.NewReader(strings.Join(stdin, "\n") + "\n"))
	}
	g.sh.ExecLine(line)
	return out.String()
}

func (g *gsession) must(need string, got string, what string) {
	g.t.Helper()
	if !strings.Contains(got, need) {
		g.t.Fatalf("%s:\nwant: %s\ngot:\n%s", what, need, got)
	}
}

func TestGitServerIsReal(t *testing.T) {
	w := core.NewWorld()
	gd := w.Devices["git"]
	if gd == nil {
		t.Fatal("no git server in the world")
	}
	svc := gd.Svc("nginx")
	if svc == nil || svc.Handler != "http-git" || svc.Port != 80 || svc.TLSCert == "" {
		t.Fatalf("the git server's web unit is not a TLS-capable git service: %+v", svc)
	}
	// repositories are object stores with real history on disk
	for _, repo := range []string{"neohome-scripts", "dotfiles"} {
		base := core.ServerRepoPath(repo)
		if !gd.FS.IsDir(base + "/objects/commit") {
			t.Fatalf("%s has no object store at %s", repo, base)
		}
		head, ok := gd.FS.Read(base + "/HEAD")
		if !ok || strings.TrimSpace(string(head)) == "" {
			t.Fatalf("%s has no HEAD commit", repo)
		}
		commits := gd.FS.List(base + "/objects/commit")
		if len(commits) < 2 {
			t.Fatalf("%s shipped with %d commits; a repo without history is a lie", repo, len(commits))
		}
	}
	// and the zone knows the host
	found := false
	for _, r := range w.Records {
		if r.Name == "git.neohome.example" {
			found = true
		}
	}
	if !found {
		t.Fatal("git.neohome.example is not in the zone")
	}
}

func TestGitCloneOverHttps(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.must("done.", g.exec("git clone https://git.neohome.example/neohome-scripts.git src"), "clone failed")
	g.must("backup.sh", g.exec("ls src"), "checked-out files missing")
	g.exec("cd src")
	g.must("init: backup and mirror probe scripts", g.exec("git log"), "history lost in the clone")
	g.must("nothing to commit, working tree clean", g.exec("git status"), "a fresh clone must be clean")
	g.must("nightly mirror of the NAS", g.exec("cat backup.sh"), "file contents did not survive the clone")

	// the same repository over plaintext http
	g.exec("cd /home/alex")
	g.must("done.", g.exec("git clone http://git.neohome.example/dotfiles.git df"), "http clone failed")
	g.must("parse_git_branch", g.exec("cat df/gitprompt.sh"), "dotfiles checkout incomplete")
}

func TestGitCommitPushAndFreshClone(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/neohome-scripts.git work")
	g.exec("cd work")
	headBefore := strings.TrimSpace(mustRead(t, w.Devices["git"], "/srv/git/neohome-scripts.git/HEAD"))

	// edit, commit, push
	g.must("modified:  backup.sh", g.exec("echo '# reviewed by alex' >> backup.sh && git status"), "status does not see the edit")
	g.must("[master", g.exec("git commit -m 'reviewed the backup script'"), "commit failed")
	g.must("nothing to commit, working tree clean", g.exec("git status"), "commit left the tree dirty")
	g.must("main -> main", g.exec("git push", "alex123"), "push failed")

	headAfter := strings.TrimSpace(mustRead(t, w.Devices["git"], "/srv/git/neohome-scripts.git/HEAD"))
	if headAfter == headBefore {
		t.Fatal("the push did not move the server's HEAD")
	}
	g.must("Everything up-to-date", g.exec("git push", "alex123"), "a second push must be a no-op")

	// a fresh clone really sees the pushed commit
	g.exec("cd /home/alex")
	g.must("done.", g.exec("git clone https://git.neohome.example/neohome-scripts.git work2"), "re-clone failed")
	g.must("reviewed by alex", g.exec("cat work2/backup.sh"), "the pushed change is not in the fresh clone")
}

func TestGitPushRequiresRealAuth(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/neohome-scripts.git work")
	g.exec("cd work")
	g.exec("echo '# x' >> backup.sh")
	g.exec("git commit -m 'attempt'")
	headBefore := strings.TrimSpace(mustRead(t, w.Devices["git"], "/srv/git/neohome-scripts.git/HEAD"))

	g.must("authentication failed", g.exec("git push", "wrongpass"), "a wrong password must not push")
	headAfter := strings.TrimSpace(mustRead(t, w.Devices["git"], "/srv/git/neohome-scripts.git/HEAD"))
	if headAfter != headBefore {
		t.Fatal("an unauthenticated push moved the server's HEAD")
	}
	// the failed attempt is evidence on the server
	syslog, _ := w.Devices["git"].FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "push authentication failed for alex") {
		t.Fatal("the failed push was not logged on the server")
	}
}

func TestGitPullFastForwardAndRefusals(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/dotfiles.git repo")
	g.exec("cd repo")

	// up to date
	g.must("Already up to date.", g.exec("git pull"), "pull with no new commits must say so")

	// a dirty tree refuses to pull — but only when there is something to
	// pull: with nothing new, even a dirty tree is told "up to date"
	orig := mustRead(t, g.d, "/home/alex/repo/tmux.conf")
	if err := core.GitSeedCommit(w, "dotfiles", map[string]string{
		"tmux.conf": "set -g default-shell /bin/bash\nset -g mouse off\n",
	}, "mira", "tmux: mouse off after all"); err != nil {
		t.Fatalf("server-side commit failed: %v", err)
	}
	g.exec("echo junk >> tmux.conf")
	g.must("would be overwritten", g.exec("git pull"), "pull must refuse a dirty tree")

	// restoring the tracked content byte-for-byte clears the refusal
	g.d.FS.Write("/home/alex/repo/tmux.conf", orig, 0644, "alex", "alex")
	g.must("Fast-forward", g.exec("git pull"), "pull refused a clean fast-forward")
	g.must("mouse off", g.exec("cat tmux.conf"), "pull did not update the working file")

	// a diverged history refuses to fast-forward
	g.exec("cd /home/alex")
	g.exec("git clone https://git.neohome.example/dotfiles.git repo2")
	g.exec("cd repo2")
	g.exec("echo note >> gitprompt.sh")
	g.exec("git commit -m 'local side change'")
	if err := core.GitSeedCommit(w, "dotfiles", map[string]string{
		"gitprompt.sh": "# rewritten on the server\n",
	}, "mira", "server side change"); err != nil {
		t.Fatalf("server-side commit failed: %v", err)
	}
	g.must("diverged", g.exec("git pull"), "a diverged history must refuse to merge silently")

	// outside a repository, git says so
	g.must("not a git repository", g.exec("cd /home/alex && git status"), "git outside a repo must say so")
}

// mustRead reads a file and fails the test if it is not there.
func mustRead(t *testing.T, d *core.Device, p string) string {
	t.Helper()
	data, ok := d.FS.Read(p)
	if !ok {
		t.Fatalf("%s is missing on %s", p, d.Hostname)
	}
	return string(data)
}

// Branches are ref files plus a symref HEAD: create, switch, diverge, merge
// fast-forward. A diverged pair is refused — true merges are out of scope,
// and the refusal says so instead of inventing a conflicted tree.
func TestGitBranchAndFastForwardMerge(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/neohome-scripts.git work")
	g.exec("cd work")
	g.must("* master", g.exec("git branch"), "a fresh clone should be on master")
	g.must("On branch master", g.exec("git status"), "status must name the real branch")

	// feature branch: new file, committed there, invisible on master
	g.must("Created branch feature", g.exec("git branch feature"), "branch create failed")
	g.must("Switched to branch 'feature'", g.exec("git checkout feature"), "checkout failed")
	g.exec("echo feature work > feat.txt")
	g.must("[feature", g.exec("git commit -m 'feature work'"), "commit should name the branch")
	if _, ok := w.Devices["pc-alex"].FS.Read("/home/alex/work/feat.txt"); !ok {
		t.Fatal("the committed file must be in the working tree")
	}
	g.must("Switched to branch 'master'", g.exec("git checkout master"), "checkout back failed")
	if _, ok := w.Devices["pc-alex"].FS.Read("/home/alex/work/feat.txt"); ok {
		t.Fatal("switching branches must materialise master's tree")
	}
	// fast-forward: master takes the branch tip, history intact
	g.must("Fast-forward", g.exec("git merge feature"), "ff merge failed")
	if _, ok := w.Devices["pc-alex"].FS.Read("/home/alex/work/feat.txt"); !ok {
		t.Fatal("the merged file must be in the working tree")
	}
	g.must("feature work", g.exec("git log"), "merged history must list the branch commit")

	// diverge: a commit on master after the branch point...
	g.exec("git checkout -b side")
	g.exec("echo side > side.txt")
	g.exec("git commit -m 'side work'")
	g.exec("git checkout master")
	g.exec("echo master > master.txt")
	g.exec("git commit -m 'master work'")
	out := g.exec("git merge side")
	if !strings.Contains(out, "diverged") {
		t.Fatalf("a diverged merge must be refused honestly, got:\n%s", out)
	}
	// ...and the refused merge changed nothing
	if _, ok := w.Devices["pc-alex"].FS.Read("/home/alex/work/side.txt"); ok {
		t.Fatal("a refused merge must not materialise the other side")
	}
}

func TestGitCheckoutRefusesDirtyTree(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/dotfiles.git df")
	g.exec("cd df")
	g.exec("git branch alt")
	g.exec("echo dirty > gitprompt.sh")
	if out := g.exec("git checkout alt"); !strings.Contains(out, "would be overwritten") {
		t.Fatalf("checkout over local changes must be refused, got:\n%s", out)
	}
	g.exec("git commit -m 'keep the change'")
	g.must("Switched to branch 'alt'", g.exec("git checkout alt"), "checkout after commit should work")
}

func TestGitDeleteProtectsHistory(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	g := newGSession(t, w, w.Devices["pc-alex"], "alex")

	g.exec("git clone https://git.neohome.example/dotfiles.git df")
	g.exec("cd df")
	g.exec("git checkout -b doomed")
	g.exec("echo doomed > doomed.txt")
	g.exec("git commit -m 'doomed work'")
	g.exec("git checkout master")
	if out := g.exec("git branch -d doomed"); !strings.Contains(out, "not merged") {
		t.Fatalf("deleting unmerged history must be refused, got:\n%s", out)
	}
	if out := g.exec("git branch -d master"); !strings.Contains(out, "checked out") {
		t.Fatalf("deleting the current branch must be refused, got:\n%s", out)
	}
	g.must("Deleted branch doomed", g.exec("git branch -D doomed"), "force delete failed")
	if out := g.exec("git branch"); strings.Contains(out, "doomed") {
		t.Fatalf("the branch must be gone:\n%s", out)
	}
	if out := g.exec("git branch 'bad name'"); !strings.Contains(out, "invalid branch name") {
		t.Fatalf("bad names must be refused, got:\n%s", out)
	}
}
