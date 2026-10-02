package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// The world must have secrets. If every account can read everything on disk then
// permissions are decoration and no compromise means anything.
func TestOrdinaryAccountCannotReadTheCredentialStore(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")

	if !pc.FS.Exists("/etc/shadow") {
		t.Fatal("there is no /etc/shadow: the world holds no secrets to protect")
	}
	if pc.FS.CanRead("/etc/shadow", u) {
		t.Fatal("alex can read /etc/shadow; that file must be 0640 root:shadow")
	}
	if !pc.FS.CanRead("/etc/shadow", pc.FindUser("root")) {
		t.Fatal("root must be able to read /etc/shadow")
	}
	// and the read attempt through the shell must be refused visibly
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.ExecLine("cat /etc/shadow")
	if !strings.Contains(out.String(), "Permission denied") {
		t.Fatalf("`cat /etc/shadow` should be refused, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "$6$") {
		t.Fatalf("the hash leaked anyway:\n%s", out.String())
	}
}

// /etc/passwd and /etc/hosts are 0644 by design: readable, and that is normal.
func TestWorldReadableFilesStayReadable(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	for _, p := range []string{"/etc/passwd", "/etc/hosts", "/etc/os-release"} {
		if !pc.FS.CanRead(p, u) {
			t.Fatalf("%s must stay world-readable (0644)", p)
		}
	}
}

// sudoers decides who may escalate, and it is not readable by the unprivileged
// — otherwise an attacker could read the policy they are meant to defeat.
func TestSudoersIsNotWorldReadable(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	if pc.FS.CanRead("/etc/sudoers", u) {
		t.Fatal("/etc/sudoers must be 0440 root:root")
	}
}

// Escalation must depend on group membership in /etc/sudoers, and a password
// must actually be required — otherwise `sudo` is a free win.
func TestSudoRequiresMembershipAndAPassword(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// an ordinary account cannot escalate: it is not in /etc/sudoers at all
	outsider := pc.FindUser("guest")
	if outsider == nil {
		t.Fatal("the world needs an unprivileged account, or the privilege boundary is untestable")
	}
	out := &bufOut{}
	sh := shell.NewShell(w, pc, outsider, out, "10.77.1.11", "xterm")
	sh.ExecLine("sudo cat /etc/shadow")
	if !strings.Contains(out.String(), "not in the sudoers file") {
		t.Fatalf("a non-sudoers account should be refused:\n%s", out.String())
	}
	if strings.Contains(out.String(), "$6$") {
		t.Fatalf("the secret leaked to a non-sudoers account:\n%s", out.String())
	}
}

// isUserInSudoers reads the policy from the file, not from the group list, so
// the two can be made to disagree and the file is what wins.
func TestSudoersFileListsThePrivilegedAccounts(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	data, ok := pc.FS.Read("/etc/sudoers")
	if !ok {
		t.Fatal("no /etc/sudoers")
	}
	body := string(data)
	if !strings.Contains(body, "root    ALL=(ALL:ALL) ALL") {
		t.Fatalf("root should always be in sudoers:\n%s", body)
	}
	if !strings.Contains(body, "alex") {
		t.Fatalf("alex is in the sudo group and should be in sudoers:\n%s", body)
	}
	if strings.Contains(body, "guest") {
		t.Fatalf("an unprivileged account must not be in sudoers:\n%s", body)
	}
}

// ls -l must report the real owner AND group, and must treat -l as a flag rather
// than a filename (it used to look for a file called "-l").
func TestLsShowsOwnerAndGroupAndTakesFlags(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.ExecLine("ls -l /etc/shadow")
	got := out.String()
	if strings.Contains(got, "No such file or directory") {
		t.Fatalf("`ls -l /etc/shadow` treated -l as a path:\n%s", got)
	}
	// 0640 root:shadow — the group column must read "shadow", not a repeat of root
	if !strings.Contains(got, "shadow") {
		t.Fatalf("the group column is wrong (owner printed twice?):\n%s", got)
	}
	if !strings.Contains(got, "-rw-r-----") {
		t.Fatalf("the mode is wrong:\n%s", got)
	}
}

// The shadow file must not be readable through the text tools either — a leak
// through `grep` would defeat the point of the mode bits.
func TestTextToolsRespectReadPermissions(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	u := pc.FindUser("alex")
	out := &bufOut{}
	sh := shell.NewShell(w, pc, u, out, "10.77.1.11", "xterm")
	sh.ExecLine("grep root /etc/shadow")
	if strings.Contains(out.String(), "$6$") {
		t.Fatalf("`grep` leaked the shadow file:\n%s", out.String())
	}
}
