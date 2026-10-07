package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// Accounts and households (§4/§46) — useradd/userdel/usermod/groupadd/groups
// plus the home as a first-class entity. The table and the four credential
// files move together, or nothing moves. Each test walks a happy path, a
// boundary and a recovery.

func TestUseraddCreatesLockedAccount(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	if out := run(t, w, pc, "alex", "useradd dev2"); !strings.Contains(out, "Permission denied") {
		t.Fatalf("non-root useradd must be refused, got:\n%s", out)
	}
	out := run(t, w, pc, "root", "useradd -G sudo dev2")
	if !strings.Contains(out, "locked") {
		t.Fatalf("useradd should confirm a locked account, got:\n%s", out)
	}
	u := pc.FindUser("dev2")
	if u == nil || u.UID < 1000 {
		t.Fatalf("account missing or not stacked above system UIDs: %+v", u)
	}
	if u.CheckPassword("anything") {
		t.Fatal("a new account must be locked (no password authenticates)")
	}
	if _, ok := pc.FS.Read("/home/dev2"); !ok {
		t.Fatal("home directory must come along (useradd -m)")
	}
	// all four files agree about the newcomer
	for _, f := range []string{"/etc/passwd", "/etc/shadow", "/etc/sudoers", "/etc/group"} {
		data, ok := pc.FS.Read(f)
		if !ok || !strings.Contains(string(data), "dev2") {
			t.Fatalf("%s must name the newcomer:\n%s", f, string(data))
		}
	}
	if out := run(t, w, pc, "root", "useradd dev2"); !strings.Contains(out, "already exists") {
		t.Fatalf("duplicates must be refused, got:\n%s", out)
	}
	// unlock with passwd, then the account really logs in (su proves it)
	runWithStdin(t, w, pc, "root", "passwd dev2", "dev2pass", "dev2pass")
	if rc := statusWithStdin(t, w, pc, "guest", "su dev2", "dev2pass"); rc != 0 {
		t.Fatal("su with the set password should succeed")
	}
}

func TestUsermodGrantsRealSudo(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "root", "useradd dev2")
	runWithStdin(t, w, pc, "root", "passwd dev2", "dev2pass", "dev2pass")

	// without sudo, the world says no the way sudo says no
	out := statusWithStdinOut(t, w, pc, "dev2", "sudo id", "dev2pass")
	if !strings.Contains(out, "not in the sudoers file") {
		t.Fatalf("sudo without membership must be refused, got:\n%s", out)
	}
	// attaching to a phantom group is refused — groupadd is load-bearing
	if out := run(t, w, pc, "root", "usermod -aG wheel dev2"); !strings.Contains(out, "does not exist") {
		t.Fatalf("phantom groups must be refused, got:\n%s", out)
	}
	run(t, w, pc, "root", "groupadd wheel")
	if out := run(t, w, pc, "root", "usermod -aG sudo dev2"); !strings.Contains(out, "groups of dev2") {
		t.Fatalf("usermod should confirm, got:\n%s", out)
	}
	// and now sudo really escalates: same password, root result
	out = statusWithStdinOut(t, w, pc, "dev2", "sudo id", "dev2pass")
	if !strings.Contains(out, "uid=0(root)") {
		t.Fatalf("sudo after grant must escalate, got:\n%s", out)
	}
	// -G replaces secondaries but never the primary
	run(t, w, pc, "root", "usermod -G wheel dev2")
	u := pc.FindUser("dev2")
	if len(u.Groups) == 0 || u.Groups[0] != "dev2" {
		t.Fatalf("primary group must survive -G, got %v", u.Groups)
	}
	if hasSudoGroup(u) {
		t.Fatalf("sudo should be gone after -G wheel, got %v", u.Groups)
	}
}

func hasSudoGroup(u *core.User) bool {
	for _, g := range u.Groups {
		if g == "sudo" {
			return true
		}
	}
	return false
}

func statusWithStdinOut(t *testing.T, w *core.World, dev *core.Device, user string, line string, stdin ...string) string {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, dev, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader(strings.Join(stdin, "\n") + "\n"))
	sh.ExecLine(line)
	return out.String()
}

func TestGroupIDsAreStable(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	run(t, w, pc, "root", "groupadd docker")
	gid := pc.GroupID("docker")
	if gid < 1000 {
		t.Fatalf("allocated GIDs must stack above system range, got %d", gid)
	}
	// no collision with any UID on the box
	for _, u := range pc.Users {
		if u.UID == gid {
			t.Fatalf("GID %d collides with uid of %s", gid, u.Name)
		}
	}
	// join, leave, delete a member: the number never moves, the file keeps
	// the group even empty
	run(t, w, pc, "root", "useradd dev2")
	run(t, w, pc, "root", "usermod -aG docker dev2")
	run(t, w, pc, "root", "userdel -r dev2")
	if pc.GroupID("docker") != gid {
		t.Fatalf("GID shifted %d -> %d", gid, pc.GroupID("docker"))
	}
	data, _ := pc.FS.Read("/etc/group")
	line := ""
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "docker:") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("empty groups must persist in /etc/group:\n%s", string(data))
	}
	// `id` prints through the same rule, so it agrees with the file
	out := run(t, w, pc, "alex", "id")
	if !strings.Contains(out, "gid=1000(alex)") {
		t.Fatalf("id must show the private-group GID:\n%s", out)
	}
}

func TestUserdelRemovesAccount(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "root", "useradd dev2")
	run(t, w, pc, "root", "echo work > /home/dev2/notes.txt")

	// UID 0 is refused outright (shell stops self-deletion first, core refuses
	// the account itself — both layers, pinned separately)
	if out := run(t, w, pc, "root", "userdel root"); !strings.Contains(out, "your own account") {
		t.Fatalf("removing self must be refused, got:\n%s", out)
	}
	if err := pc.DelUser("root", false); err == nil || !strings.Contains(err.Error(), "UID 0") {
		t.Fatalf("core must refuse UID 0 removal, err=%v", err)
	}
	// without -r the files stay behind
	if out := run(t, w, pc, "root", "userdel dev2"); !strings.Contains(out, "removed dev2") {
		t.Fatalf("userdel failed:\n%s", out)
	}
	if pc.FindUser("dev2") != nil {
		t.Fatal("the account must be gone")
	}
	if _, ok := pc.FS.Read("/home/dev2/notes.txt"); !ok {
		t.Fatal("without -r the files must stay")
	}
	if data, _ := pc.FS.Read("/etc/passwd"); strings.Contains(string(data), "dev2") {
		t.Fatal("/etc/passwd must drop the account")
	}
}

func TestUserdelRemoveHome(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "root", "useradd dev2")
	run(t, w, pc, "root", "echo work > /home/dev2/notes.txt")

	run(t, w, pc, "root", "userdel -r dev2")
	if _, ok := pc.FS.Read("/home/dev2/notes.txt"); ok {
		t.Fatal("-r must remove the home directory")
	}
	// the private group stays behind, empty — userdel never removes groups
	data, _ := pc.FS.Read("/etc/group")
	found := false
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "dev2:x:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the private group must persist:\n%s", string(data))
	}
}

func TestHouseholdEntity(t *testing.T) {
	w := core.NewWorld()

	h := w.Households["house:alex"]
	if h == nil || h.Founder != "alex" || h.Router != "router-alex" || h.NAS != "nas-alex" {
		t.Fatalf("founder household mis-seeded: %+v", h)
	}
	found := false
	for _, m := range h.Members {
		if m == "alex" {
			found = true
		}
	}
	if !found {
		t.Fatal("founder must be a member")
	}
	// inviting adopts the citizen into the house with its shared boxes
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "player invite blake temp123")
	if len(h.Members) != 2 || h.Members[1] != "blake" {
		t.Fatalf("invite must adopt the member: %v", h.Members)
	}
	p := w.Players["blake"]
	if p.HouseKey != "house:alex" || p.Router != "router-alex" || p.NAS != "nas-alex" {
		t.Fatalf("citizen must inherit household infra: %+v", p)
	}
	// and the whole household survives a save
	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	bh := back.Households["house:alex"]
	if bh == nil || len(bh.Members) != 2 {
		t.Fatalf("household must survive the save: %+v", bh)
	}
	// while /etc/group (and its GID pins) survive too
	bpc := back.Devices["pc-alex"]
	if _, ok := bpc.FS.Read("/etc/group"); !ok {
		t.Fatal("/etc/group must survive the save")
	}
}

func TestChpasswdBatchSetsPasswords(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "root", "useradd dev2")
	run(t, w, pc, "root", "useradd dev3")

	if out := run(t, w, pc, "alex", "echo dev2:x | chpasswd"); !strings.Contains(out, "Permission denied") {
		t.Fatalf("non-root chpasswd must be refused, got:\n%s", out)
	}
	out := run(t, w, pc, "root", "printf 'dev2:batch1\\nbroken-line\\ndev3:batch2\\n' | chpasswd")
	if !strings.Contains(out, "changed 2") || !strings.Contains(out, "skipped 1") {
		t.Fatalf("batch result miscounted, got:\n%s", out)
	}
	if !pc.FindUser("dev2").CheckPassword("batch1") || !pc.FindUser("dev3").CheckPassword("batch2") {
		t.Fatal("batch-set passwords must authenticate")
	}
}
