package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/shell"
)

// The basement vault (§41) — an offline server: no interfaces, dark until
// plugged, entered only by crash cart, fed only by sneakernet. Each test
// walks a happy path, a boundary and a recovery.

// vaultConsole runs one command through the crash cart.
func vaultConsole(t *testing.T, w *core.World, from *core.Device, user, cmd string) string {
	t.Helper()
	return run(t, w, from, user, "console vault "+cmd)
}

// consoleExec drives an interactive crash-cart session: every line runs on
// the vault itself, so redirects and pipes are parsed there — a `console
// vault echo x > f` one-liner would redirect on the caller's machine, the
// way `ssh host echo x > f` does.
func consoleExec(t *testing.T, w *core.World, from *core.Device, user string, lines ...string) string {
	t.Helper()
	u := from.FindUser(user)
	if u == nil {
		t.Fatalf("user %s not found on %s", user, from.Hostname)
	}
	out := &bufOut{}
	sh := shell.NewShell(w, from, u, out, "10.77.1.11", "xterm")
	sh.SetInput(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	sh.ExecLine("console vault")
	return out.String()
}

func TestVaultIsUnreachableRemotely(t *testing.T) {
	w := core.NewWorld()
	vault := w.Devices["vault-alex"]
	if vault == nil {
		t.Fatal("no vault in the world")
	}
	if len(vault.Ifaces) != 0 {
		t.Fatal("the vault must have no network interfaces at all")
	}
	for ip, id := range w.IPMap {
		if id == vault.ID {
			t.Fatalf("the vault must have no routable address, found %s", ip)
		}
	}
	for _, id := range w.ScannableTargets() {
		if id == vault.ID {
			t.Fatal("the scanner must not see the vault")
		}
	}
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "alex", "ssh vault"); !strings.Contains(out, "resolve") {
		t.Fatalf("ssh must not resolve the vault, got:\n%s", out)
	}
	// ...and dark: the cart needs power first
	if out := run(t, w, pc, "alex", "console vault whoami"); !strings.Contains(out, "plug") {
		t.Fatalf("a dark vault must refuse the cart, got:\n%s", out)
	}
}

func TestVaultConsoleFlow(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]

	if out := run(t, w, pc, "alex", "power plug vault"); !strings.Contains(out, "coming back") {
		t.Fatalf("plug failed:\n%s", out)
	}
	vault := w.Devices["vault-alex"]
	if !vault.Powered() {
		t.Fatal("the vault must boot on plug (BootSet carries syslogd)")
	}
	if out := vaultConsole(t, w, pc, "alex", "whoami"); !strings.Contains(out, "root") {
		t.Fatalf("physical access lands as root, got:\n%s", out)
	}
	// only boxes without addresses take the cart: the NAS refuses it
	if out := run(t, w, pc, "alex", "console nas whoami"); !strings.Contains(out, "crash cart") && !strings.Contains(out, "ssh") {
		t.Fatalf("addressed boxes must refuse the cart, got:\n%s", out)
	}
	// the visit is evidence, like every other login
	data, _ := vault.FS.Read("/var/log/syslog")
	if !strings.Contains(string(data), "physical access") {
		t.Fatalf("console logins must be logged:\n%s", string(data))
	}
	// air-gapped work: no route out, reads and writes inside
	if out := vaultConsole(t, w, pc, "alex", "ping 10.0.0.1"); !strings.Contains(strings.ToLower(out), "unreachable") && !strings.Contains(out, "No route") {
		t.Fatalf("the vault must have no route anywhere, got:\n%s", out)
	}
	if out := consoleExec(t, w, pc, "alex", "echo offline-data > /srv/vault/ledger.txt", "cat /srv/vault/ledger.txt"); !strings.Contains(out, "offline-data") {
		t.Fatalf("local writes must work:\n%s", out)
	}
	// recovery is unplugging: dark again, data kept
	run(t, w, pc, "alex", "power unplug vault")
	if vault.Powered() {
		t.Fatal("unplug must darken the vault")
	}
	if _, ok := vault.FS.Read("/srv/vault/ledger.txt"); !ok {
		t.Fatal("dark storage keeps its bytes")
	}
}

func TestVaultSneakernetBridge(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "power plug vault")

	// out: secret onto the stick on the online box
	run(t, w, pc, "alex", "echo ferry-secret > /home/alex/ferry.txt")
	run(t, w, pc, "alex", "usb plug usb-alex")
	run(t, w, pc, "alex", "mount -t vfat /dev/sda1 /mnt/usb")
	run(t, w, pc, "alex", "cp /home/alex/ferry.txt /mnt/usb/ferry.txt")
	run(t, w, pc, "alex", "usb unplug usb-alex")
	// across the gap: plug the same stick into the vault, mount, copy in
	if out := vaultConsole(t, w, pc, "alex", "usb plug usb-alex"); !strings.Contains(out, "attached") {
		t.Fatalf("plug into vault failed:\n%s", out)
	}
	out := consoleExec(t, w, pc, "alex",
		"mount -t vfat /dev/sda1 /mnt/usb",
		"cp /mnt/usb/ferry.txt /srv/vault/ferry.txt",
		"cat /srv/vault/ferry.txt")
	if !strings.Contains(out, "ferry-secret") {
		t.Fatalf("the air gap must carry bytes:\n%s", out)
	}
}

func TestVaultIsOwnerScoped(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "power plug vault")
	run(t, w, pc, "alex", "player invite blake temp123")
	blake := w.Devices["pc-blake"]

	if out := run(t, w, blake, "blake", "console vault whoami"); !strings.Contains(out, "not this household") {
		t.Fatalf("another household's hands must be refused, got:\n%s", out)
	}
}

func TestVaultSurvivesSave(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	run(t, w, pc, "alex", "power plug vault")
	consoleExec(t, w, pc, "alex", "echo keepme > /srv/vault/keep.txt")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	bv := back.Devices["vault-alex"]
	if bv == nil || !bv.Powered() {
		t.Fatal("vault power must survive the save")
	}
	if data, ok := bv.FS.Read("/srv/vault/keep.txt"); !ok || !strings.Contains(string(data), "keepme") {
		t.Fatal("vault data must survive the save")
	}
	if len(bv.Ifaces) != 0 {
		t.Fatal("the vault must stay interface-free across the save")
	}
}
