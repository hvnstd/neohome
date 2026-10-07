package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// Community packages (Phase 3) — installed software renders to a shareable
// payload file, and importing parses and installs it unsigned through the
// same apply path. Money is not involved; trust is: dependencies must be
// present, provenance names the file, and the risk is logged out loud.

func communitySetup(t *testing.T) (*core.World, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("setup: apt update failed:\n%s", out)
	}
	if out := run(t, w, pc, "root", "apt install nano"); !strings.Contains(out, "Setting up nano") {
		t.Fatalf("setup: install failed:\n%s", out)
	}
	return w, pc
}

func TestPkgExportRendersShareableFile(t *testing.T) {
	w, pc := communitySetup(t)

	if out := run(t, w, pc, "alex", "pkg export nano /tmp/nano.npkg"); !strings.Contains(out, "shareable") {
		t.Fatalf("export failed:\n%s", out)
	}
	data, ok := pc.FS.Read("/tmp/nano.npkg")
	if !ok || !strings.Contains(string(data), "Package: nano") {
		t.Fatalf("payload must name the package:\n%s", string(data))
	}
	// exporting what is not installed is refused, not invented
	if out := run(t, w, pc, "alex", "pkg export nosuchpkg"); !strings.Contains(out, "not installed") {
		t.Fatalf("missing packages must be refused, got:\n%s", out)
	}
}

func TestPkgImportInstallsUnsigned(t *testing.T) {
	w, pc := communitySetup(t)
	nas := w.Devices["nas-alex"]
	run(t, w, pc, "alex", "pkg export nano /tmp/nano.npkg")
	data, _ := pc.FS.Read("/tmp/nano.npkg")
	nas.FS.Write("/tmp/nano.npkg", string(data), 0644, "root", "root")
	run(t, w, nas, "root", "apt update")
	run(t, w, nas, "root", "apt install libc")

	if out := run(t, w, nas, "alex", "pkg import /tmp/nano.npkg"); !strings.Contains(out, "needs root") {
		t.Fatalf("non-root import must be refused, got:\n%s", out)
	}
	out := run(t, w, nas, "root", "pkg import /tmp/nano.npkg")
	if !strings.Contains(out, "unsigned") {
		t.Fatalf("unsigned risk must be said out loud, got:\n%s", out)
	}
	if nas.Installed["nano"] == nil {
		t.Fatal("the package must be installed")
	}
	if nas.InstalledFrom["nano"] != "community:/tmp/nano.npkg" {
		t.Fatalf("provenance must name the file, got %q", nas.InstalledFrom["nano"])
	}
	// byte-identical files on both ends: the payload carried the software
	a, _ := pc.FS.Read("/usr/bin/nano")
	b, _ := nas.FS.Read("/usr/bin/nano")
	if string(a) != string(b) {
		t.Fatal("imported files must match the export")
	}
	syslog, _ := nas.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "unsigned community package") {
		t.Fatal("the risk must be in the log")
	}
	// reinstalling is refused, not duplicated
	if out := run(t, w, nas, "root", "pkg import /tmp/nano.npkg"); !strings.Contains(out, "already installed") {
		t.Fatalf("reinstalls must be refused, got:\n%s", out)
	}
}

func TestPkgImportRefusesHonestly(t *testing.T) {
	w, pc := communitySetup(t)

	// garbage is not a package
	pc.FS.Write("/tmp/junk.npkg", "hello world\n", 0644, "root", "root")
	if out := run(t, w, pc, "root", "pkg import /tmp/junk.npkg"); !strings.Contains(out, "not a package") {
		t.Fatalf("garbage must be refused, got:\n%s", out)
	}
	// missing dependencies are named, like dpkg -i
	pc.FS.Write("/tmp/needy.npkg", "Package: needy\nVersion: 1\nDepends: nosuchdep\n", 0644, "root", "root")
	if out := run(t, w, pc, "root", "pkg import /tmp/needy.npkg"); !strings.Contains(out, "nosuchdep") {
		t.Fatalf("missing deps must be named, got:\n%s", out)
	}
	if pc.Installed["needy"] != nil {
		t.Fatal("a refused install must not half-install")
	}
}
