package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// §36 — the weak-credential half of the conjunction is a choice.
//
// `passwd` changes an account's password for real: the account record and
// /etc/shadow move together, so the next login, su, sudo, ssh, sftp or ftp
// attempt answers against the new one — and the world's scanner guesses
// against it too. Each test walks the happy path, a boundary and a recovery.

func shadowFor(t *testing.T, d *core.Device, user string) string {
	t.Helper()
	data, ok := d.FS.Read("/etc/shadow")
	if !ok {
		t.Fatal("no /etc/shadow on this machine")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, user+":") {
			return line
		}
	}
	t.Fatalf("no shadow entry for %s", user)
	return ""
}

func TestPasswdChangesOwnPassword(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	before := shadowFor(t, pc, "alex")

	out := runWithStdin(t, w, pc, "alex", "passwd", "alex123", "n3w-pass!", "n3w-pass!")
	if !strings.Contains(out, "password updated successfully") {
		t.Fatalf("passwd should confirm the change, got:\n%s", out)
	}
	// the account answers against the new password everywhere credentials
	// are checked, and the old one really stops working
	u := pc.FindUser("alex")
	if !u.CheckPassword("n3w-pass!") {
		t.Fatal("the new password should authenticate")
	}
	if u.CheckPassword("alex123") {
		t.Fatal("the old password must stop working")
	}
	// /etc/shadow moved with the account: there is one credential store,
	// not a file that disagrees with the login path
	if after := shadowFor(t, pc, "alex"); after == before {
		t.Fatal("the shadow entry should have been rewritten")
	}
	// and the machine's own log carries the change, like every other
	// account event (su, sudo) does
	data, _ := pc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(data), "password changed for user alex") {
		t.Fatalf("syslog should record the change:\n%s", string(data))
	}
	// a login-equivalent check against the new password succeeds, the old
	// one fails: su is the honest end-to-end proof
	if rc := statusWithStdin(t, w, pc, "guest", "su alex", "n3w-pass!"); rc != 0 {
		t.Fatal("su with the new password should succeed")
	}
	if rc := statusWithStdin(t, w, pc, "guest", "su alex", "alex123"); rc == 0 {
		t.Fatal("su with the old password must fail")
	}
}

// statusWithStdin is remoteStatus with a scripted stdin, for the password
// prompts (su, sudo) that follow a passwd change.
func statusWithStdin(t *testing.T, w *core.World, dev *core.Device, user string, line string, stdin ...string) int {
	t.Helper()
	u := dev.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, dev.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, dev, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader(strings.Join(stdin, "\n") + "\n"))
	return sh.ExecLineStatus(line)
}

func TestPasswdRefusesWrongCurrentPassword(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := runWithStdin(t, w, pc, "alex", "passwd", "not-the-password", "n3w-pass!", "n3w-pass!")
	if !strings.Contains(out, "Authentication failure") {
		t.Fatalf("a wrong current password must fail the change, got:\n%s", out)
	}
	if pc.FindUser("alex").CheckPassword("n3w-pass!") {
		t.Fatal("a refused change must not have landed")
	}
	if !pc.FindUser("alex").CheckPassword("alex123") {
		t.Fatal("the old password must still work after a refusal")
	}
}

func TestPasswdRefusesMismatchedRetype(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := runWithStdin(t, w, pc, "alex", "passwd", "alex123", "n3w-pass!", "other-pass!")
	if !strings.Contains(out, "do not match") {
		t.Fatalf("a mismatched retype must refuse the change, got:\n%s", out)
	}
	if pc.FindUser("alex").CheckPassword("n3w-pass!") {
		t.Fatal("a refused change must not have landed")
	}
}

func TestPasswdNonRootCannotChangeOthers(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	out := runWithStdin(t, w, pc, "alex", "passwd root", "x", "x")
	if !strings.Contains(out, "may not view or modify") {
		t.Fatalf("a non-root user must be refused another account, got:\n%s", out)
	}
	// and a stranger's password is not theirs to set even when they know it:
	// guest may not touch alex's account either
	out = runWithStdin(t, w, pc, "guest", "passwd alex", "x", "x")
	if !strings.Contains(out, "may not view or modify") {
		t.Fatalf("guest must be refused alex's account, got:\n%s", out)
	}
}

func TestPasswdRootChangesOthersWithoutOldPassword(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// root names the account and is asked only for the new password twice
	out := runWithStdin(t, w, pc, "root", "passwd guest", "guest-n3w", "guest-n3w")
	if !strings.Contains(out, "password updated successfully") {
		t.Fatalf("root should change any account, got:\n%s", out)
	}
	if !pc.FindUser("guest").CheckPassword("guest-n3w") {
		t.Fatal("the new password should authenticate for guest")
	}
	// root can also lock in a weak password on purpose — that is the §36
	// conjunction, built by hand: a published port plus this line is a foothold
	out = runWithStdin(t, w, pc, "root", "passwd guest", "guest", "guest")
	if !strings.Contains(out, "password updated successfully") {
		t.Fatalf("weak passwords are a choice, not a validation error, got:\n%s", out)
	}
}

func TestPasswdSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	runWithStdin(t, w, pc, "alex", "passwd", "alex123", "persist-me", "persist-me")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	u := back.Devices["pc-alex"].FindUser("alex")
	if u == nil || !u.CheckPassword("persist-me") {
		t.Fatal("the changed password must survive the save")
	}
	if _, ok := back.Devices["pc-alex"].FS.Read("/etc/shadow"); !ok {
		t.Fatal("the shadow file must survive the save")
	}
}
