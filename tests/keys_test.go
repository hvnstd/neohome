package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Publickey auth (§30) — files, not topology: the session offers whatever
// ~/.ssh holds, the server answers from the account's authorized_keys, and
// only an exact key-body match counts. Password stays the fallback, and the
// assistant's key-only directive keeps working through the same files.

// keyLogin runs one remote command and reports whether a password was asked.
func keyLogin(t *testing.T, w *core.World, dev *core.Device, user, line string) (out string, prompted bool) {
	t.Helper()
	out = run(t, w, dev, user, line)
	return out, strings.Contains(out, "password:")
}

func TestKeyLoginNeedsNoPassword(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	// the seeded trust: pc alex's key is in the assistant's authorized_keys
	out, prompted := keyLogin(t, w, pc, "alex", "ssh assistant@assistant echo key-ok")
	if prompted {
		t.Fatalf("a listed key must skip the password prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "key-ok") {
		t.Fatalf("the remote command must run:\n%s", out)
	}
	// file truth, not vibes: remove the listing and the same login falls
	// through to the topological trust (owner's node) — so prove the files
	// on a pair with no topology instead (see below)
	if !w.KeyTrusted(pc, "alex", w.Devices["asst-alex"], "assistant") {
		t.Fatal("seeded keypair should match through files")
	}
}

func TestKeyTrustIsInTheFiles(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	// no listing on nas: files say no, whatever the topology says
	if w.KeyTrusted(pc, "alex", nas, "alex") {
		t.Fatal("an unlisted key must not match")
	}
	// distribute the key the way players do: append the pub line
	pub, ok := pc.FS.Read("/home/alex/.ssh/id_ed25519.pub")
	if !ok {
		t.Fatal("no public key on the pc")
	}
	nas.FS.MkdirAll("/home/alex/.ssh", 0700, "alex", "alex")
	cur, _ := nas.FS.Read("/home/alex/.ssh/authorized_keys")
	nas.FS.Write("/home/alex/.ssh/authorized_keys", string(cur)+string(pub), 0600, "alex", "alex")
	if !w.KeyTrusted(pc, "alex", nas, "alex") {
		t.Fatal("a listed key must match through files")
	}
	// and the session rides it without a password
	out, prompted := keyLogin(t, w, pc, "alex", "ssh alex@nas echo nas-key-ok")
	if prompted {
		t.Fatalf("a listed key must skip the password prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "nas-key-ok") {
		t.Fatalf("the remote command must run:\n%s", out)
	}
	// a wrong password still fails where no key matches (guest has no key)
	out = runWithStdin(t, w, pc, "guest", "ssh alex@nas echo no", "wrong")
	if !strings.Contains(out, "Permission denied") {
		t.Fatalf("password failures must still refuse, got:\n%s", out)
	}
}

func TestKeygenGeneratesUsableKeys(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	if out := run(t, w, pc, "alex", "ssh-keygen -f /home/alex/.ssh/id_test -C testkey"); !strings.Contains(out, "saved in") {
		t.Fatalf("keygen failed:\n%s", out)
	}
	priv, ok := pc.FS.Get("/home/alex/.ssh/id_test")
	if !ok || priv.Mode.Perm()&0077 != 0 {
		t.Fatalf("private key must be 0600: %+v", priv)
	}
	pub, ok := pc.FS.Read("/home/alex/.ssh/id_test.pub")
	if !ok || !strings.Contains(string(pub), "testkey") {
		t.Fatalf("public key must carry the comment:\n%s", string(pub))
	}
	// existing files are never clobbered
	if out := run(t, w, pc, "alex", "ssh-keygen -f /home/alex/.ssh/id_test"); !strings.Contains(out, "already exists") {
		t.Fatalf("keygen must refuse occupied paths, got:\n%s", out)
	}
	if out := run(t, w, pc, "alex", "ssh-keygen -N secret"); !strings.Contains(out, "passphrases are not implemented") {
		t.Fatalf("passphrases must be refused, not half-kept, got:\n%s", out)
	}
	// distribute the fresh key and ride it — including over sftp, where a
	// consumed password line would eat the first command
	nas.FS.MkdirAll("/home/alex/.ssh", 0700, "alex", "alex")
	nas.FS.Write("/home/alex/.ssh/authorized_keys", string(pub), 0600, "alex", "alex")
	out, prompted := keyLogin(t, w, pc, "alex", "ssh alex@nas echo fresh-key-ok")
	if prompted || !strings.Contains(out, "fresh-key-ok") {
		t.Fatalf("a generated key must log in, prompted=%v:\n%s", prompted, out)
	}
	out = sftpExec(t, w, pc, "alex", "sftp alex@nas", "ls /srv/data", "quit")
	if !strings.Contains(out, "backups.tar.gz") {
		t.Fatalf("sftp must ride the key without eating the first line:\n%s", out)
	}
}
