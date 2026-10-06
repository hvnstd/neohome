package tests

import (
	"strings"
	"testing"
	"time"

	"neohome/internal/core"
)

// Package ecosystem coupling (§9 软件包系统, §10 repository/mirror, §11 软件安全).
//
// These tests drive the real path end to end: a device's own configuration
// names a repository, the manager on that device fetches metadata from the
// mirror over DNS/Dial/HTTP, verifies the signature and the index hashes,
// caches the lists, and installs the payload the index points at. Every
// assertion is about world state — a file on a filesystem, a service that is
// running, a process that exists — never about output text alone.

// Managers belong to distributions: a box has exactly one, and asking for
// another is a command that is not installed. This is the boundary that makes
// "install curl" mean five different things depending on where you are.
func TestEveryDistributionUsesItsOwnPackageManager(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)

	cases := []struct {
		dev, user, mgr, wrong, sourcesFile string
	}{
		{"pc-alex", "root", "apt", "apk", "/etc/apt/sources.list"},
		{"nas-alex", "root", "apt", "opkg", "/etc/apt/sources.list"},
		{"router-alex", "root", "opkg", "apt", "/etc/opkg/distfeeds.conf"},
		{"asst-alex", "root", "apk", "apt", "/etc/apk/repositories"},
	}
	for _, c := range cases {
		d := w.Devices[c.dev]
		if d == nil {
			t.Fatalf("%s is missing from the world", c.dev)
		}
		if got := core.ManagerFor(d); got != c.mgr {
			t.Fatalf("%s (%s %s) should manage packages with %s, got %q",
				c.dev, d.OS.Distro, d.OS.Ver, c.mgr, got)
		}
		if _, ok := d.FS.Read(c.sourcesFile); !ok {
			t.Fatalf("%s should ship its own sources file %s", c.dev, c.sourcesFile)
		}
		out := run(t, w, d, c.user, c.wrong+" update")
		if !strings.Contains(out, "command not found") {
			t.Fatalf("%s on %s should be a command that is not installed, got:\n%s", c.wrong, c.dev, out)
		}
	}

	// a device with no package management at all is not special-cased: the
	// binary simply is not there
	phone := w.Devices["phone-alex"]
	if phone == nil {
		t.Skip("no phone in this world")
	}
	out := run(t, w, phone, "alex", "apt install curl")
	if !strings.Contains(out, "command not found") {
		t.Fatalf("a phone has no package manager, got:\n%s", out)
	}
}

// The first loop a player runs: install before update has nothing to install
// from; update caches what the mirror really serves; install lands a payload
// that really came over the network; removal refuses to break the box.
func TestInstallNeedsListsThenInstallsAndRemovesRealPayload(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "root", line) }

	// --- boundary: no cached lists, nothing to install from
	out := sh("apt install htop")
	if !strings.Contains(out, "no package lists are cached") {
		t.Fatalf("install before update must say there is nothing cached:\n%s", out)
	}
	if pc.Installed["htop"] != nil {
		t.Fatal("nothing may be installed before the lists exist")
	}

	// --- boundary: a non-root user cannot touch the package database
	guestOut := run(t, w, pc, "guest", "apt update")
	if !strings.Contains(guestOut, "Permission denied") {
		t.Fatalf("a non-root update must be refused by the lock, got:\n%s", guestOut)
	}

	// --- happy path: update caches the served lists
	out = sh("apt update")
	if !strings.Contains(out, "Get:1 http://mirror.neohome.example/debian/dists/stable/InRelease") {
		t.Fatalf("apt update must fetch the mirror's metadata:\n%s", out)
	}
	if !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("apt update must report the reads it did:\n%s", out)
	}
	mirror := w.Devices["mirror"]
	if _, ok := mirror.FS.Read("/srv/www/mirror/debian/dists/stable/main/binary-amd64/Packages"); !ok {
		t.Fatal("the mirror should serve the Packages index that was just fetched")
	}

	// --- happy path: the install reads the cached index and the served payload
	out = sh("apt install htop")
	if !strings.Contains(out, "Setting up htop") {
		t.Fatalf("install should report setting up the package:\n%s", out)
	}
	if _, ok := pc.FS.Get("/usr/bin/htop"); !ok {
		t.Fatal("the payload's binary must exist on the installed device")
	}
	if pc.Installed["htop"] == nil {
		t.Fatal("the device must record the package it installed")
	}
	if got := pc.InstalledFrom["htop"]; got != "debian" {
		t.Fatalf("provenance should name the repository the box used, got %q", got)
	}
	if out = sh("apt list --installed"); !strings.Contains(out, "htop") {
		t.Fatalf("apt list --installed must read the local database:\n%s", out)
	}
	// the payload size in the index really was read from the mirror's file
	if v := pc.Installed["htop"].Version; v != "3.3.0-4" {
		t.Fatalf("the installed version should be the one the index offered, got %s", v)
	}

	// --- recovery: removing it puts the world back
	out = sh("apt remove htop")
	if !strings.Contains(out, "Removing htop") {
		t.Fatalf("remove should report what it took away:\n%s", out)
	}
	if _, ok := pc.FS.Get("/usr/bin/htop"); ok {
		t.Fatal("removing a package must remove the files it installed")
	}
	if pc.Installed["htop"] != nil {
		t.Fatal("removing a package must clear the record of it")
	}
	if pc.InstalledFrom["htop"] != "" {
		t.Fatal("removing a package must clear where it came from too")
	}
	// a second install works: the lists are still cached, as on a real box
	if out = sh("apt install htop"); !strings.Contains(out, "Setting up htop") {
		t.Fatalf("install after remove must work again:\n%s", out)
	}
}

// Dependencies are resolved from the index the box cached, and a package
// another installed package needs cannot be torn out from under it.
func TestDependencyResolutionAndRemovalRefusals(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "root", line) }

	sh("apt update")
	out := sh("apt install nginx")
	if !strings.Contains(out, "libc") {
		t.Fatalf("installing nginx must pull its libc dependency:\n%s", out)
	}
	for _, want := range []string{"/usr/sbin/nginx", "/etc/nginx/nginx.conf"} {
		if _, ok := pc.FS.Get(want); !ok {
			t.Fatalf("the nginx payload should have created %s", want)
		}
	}
	if pc.Installed["libc"] == nil {
		t.Fatal("the dependency must be recorded as installed")
	}
	if pc.Svc("nginx") == nil || pc.Svc("nginx").State != "running" {
		t.Fatalf("an autostart service from a package must be running, got %v", pc.Svc("nginx"))
	}
	if pc.FindUser("www-data") == nil {
		t.Fatal("the package's postinst should have created its system user")
	}

	// --- boundary: removing a dependency of an installed package is refused
	out = sh("apt remove libc")
	if !strings.Contains(out, "would break nginx") {
		t.Fatalf("removing a needed dependency must be refused:\n%s", out)
	}
	if pc.Installed["libc"] == nil {
		t.Fatal("a refused removal must not change the world")
	}

	// --- recovery: remove the dependent first, then the dependency
	sh("apt remove nginx")
	if pc.Svc("nginx") != nil {
		t.Fatal("removing the package must deregister its service")
	}
	out = sh("apt remove libc")
	if !strings.Contains(out, "Removing libc") {
		t.Fatalf("with nginx gone, libc can be removed:\n%s", out)
	}
	if pc.Installed["libc"] != nil {
		t.Fatal("libc should be gone from the record")
	}
}

// The seeded incident: the debian tree is stale because its sync line was
// commented out on the mirror, the client says so on every update, and running
// the sync the way the cron comment describes really fixes it.
func TestStaleTreeIsBehindAndTheSyncFixesIt(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	deb := w.Repos["debian"]

	if deb.Status != "BEHIND" {
		t.Fatalf("the seeded debian tree should be BEHIND, got %s", deb.Status)
	}
	if !strings.Contains(deb.StatusWhy, "disabled on mirror") {
		t.Fatalf("the status must name the cause, got %q", deb.StatusWhy)
	}

	out := run(t, w, pc, "root", "apt update")
	if !strings.Contains(out, "W: repository 'stable' is stale (BEHIND)") {
		t.Fatalf("a client of a stale tree must be warned:\n%s", out)
	}
	// a stale tree still installs: stale is not broken
	if out = run(t, w, pc, "root", "apt install nano"); !strings.Contains(out, "Setting up nano") {
		t.Fatalf("a stale but consistent tree must still install:\n%s", out)
	}

	// the mirror's own report says the same thing, and points at the crontab
	mir := w.Devices["mirror"]
	out = run(t, w, mir, "root", "mirror-sync")
	if !strings.Contains(out, "BEHIND") || !strings.Contains(out, "disabled") {
		t.Fatalf("mirror-sync must report why the tree is stale:\n%s", out)
	}

	// --- recovery: run the sync the commented cron line names, then tick
	out = run(t, w, mir, "root", "mirror-sync debian")
	if !strings.Contains(out, "sync started for debian") {
		t.Fatalf("the sync must start as a real process:\n%s", out)
	}
	if !isProc(mir, "mirror-sync") {
		t.Fatal("the sync must be a process on the mirror while it runs")
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if deb.Status != "SYNCED" {
		t.Fatalf("after a successful sync the tree should be SYNCED, got %s (%s)", deb.Status, deb.StatusWhy)
	}
	if deb.LastSync.Before(w.Sim.Add(-core.BehindThreshold)) {
		t.Fatal("a finished sync must move the tree's last successful sync time forward")
	}
	out = run(t, w, pc, "root", "apt update")
	if strings.Contains(out, "BEHIND") {
		t.Fatalf("a freshly synced tree must not warn about staleness:\n%s", out)
	}
}

// A mirror is only as good as the bytes it serves. Damage the served copy and
// the world says so — on the mirror and at the client, with the real reason —
// and a real sync repairs it.
func TestCorruptedMirrorRefusesInstallsUntilResynced(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	mir := w.Devices["mirror"]
	asst := w.Devices["asst-alex"]
	alp := w.Repos["alpine"]
	quietMirrorCron(t, w)

	if alp.Status != "SYNCED" {
		t.Fatalf("the alpine tree starts in sync, got %s", alp.Status)
	}
	// the disk incident: one served index is gone
	if out := run(t, w, mir, "root", "rm /srv/www/mirror/alpine/v3.20/main/x86_64/APKINDEX"); out != "" {
		t.Logf("rm said: %s", out)
	}
	w.Tick()
	if alp.Status != "CORRUPTED" {
		t.Fatalf("a mirror missing an index it signs for is CORRUPTED, got %s", alp.Status)
	}
	if !strings.Contains(alp.StatusWhy, "missing") {
		t.Fatalf("the corruption must be described in terms of the file, got %q", alp.StatusWhy)
	}

	// --- boundary: the client refuses to install from it
	out := run(t, w, asst, "root", "apk update")
	if !strings.Contains(out, "missing from this mirror") {
		t.Fatalf("the client must report the mirror's real problem:\n%s", out)
	}
	if out = run(t, w, asst, "root", "apk add htop"); !strings.Contains(out, "no package lists are cached") {
		t.Fatalf("with no verified lists there is nothing to install from:\n%s", out)
	}
	if asst.Installed["htop"] != nil {
		t.Fatal("nothing may be installed from a corrupted tree")
	}

	// --- recovery: the same sync a player would run
	run(t, w, mir, "root", "mirror-sync alpine")
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if alp.Status != "SYNCED" {
		t.Fatalf("the resync should repair the tree, got %s (%s)", alp.Status, alp.StatusWhy)
	}
	out = run(t, w, asst, "root", "apk update")
	if strings.Contains(out, "missing") || strings.Contains(out, "ERROR") {
		t.Fatalf("after the repair the update must succeed:\n%s", out)
	}
	if out = run(t, w, asst, "root", "apk add htop"); !strings.Contains(out, "Installing") {
		t.Fatalf("the repaired tree must install again:\n%s", out)
	}
	if asst.Installed["htop"] == nil {
		t.Fatal("the package must really be installed after the repair")
	}
}

// A third-party repository is a decision with consequences (§11): its key is
// not in any keyring, so nothing from it installs until the player trusts it —
// and what arrives when they do is a package that starts a background service.
func TestThirdPartyRepositoryIsASupplyChainDecision(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	sh := func(line string) string { return run(t, w, pc, "root", line) }

	sh("apt update")
	// the vendor's own source line, added the way an admin would add one
	sh(`echo 'deb http://cdn.sashimi-cdn.example/debian stable main' >> /etc/apt/sources.list`)
	out := sh("apt update")
	if !strings.Contains(out, "not available for verification") {
		t.Fatalf("an untrusted signer must stop the update:\n%s", out)
	}
	if !strings.Contains(out, "trusted.gpg.d") {
		t.Fatalf("the refusal must say where a key would go:\n%s", out)
	}
	if out = sh("apt install nettop"); !strings.Contains(out, "unable to locate") {
		t.Fatalf("without verified lists the vendor's packages are not visible:\n%s", out)
	}

	// --- the key is obtainable from the tree's own published material
	keyURL := "http://cdn.sashimi-cdn.example/debian/keys/" + core.ThirdPartyKeyFP + ".asc"
	out = sh("curl -s " + keyURL)
	if !strings.Contains(out, "Sashimi CDN Package Key") {
		t.Fatalf("the tree should publish its signing key:\n%s", out)
	}

	// --- recovery: trust the key, update, install
	sh("curl -s " + keyURL + " > /etc/apt/trusted.gpg.d/sashimi.asc")
	if _, ok := pc.FS.Get("/etc/apt/trusted.gpg.d/sashimi.asc"); !ok {
		t.Fatal("the key material must be written into the keyring")
	}
	if out = sh("apt update"); strings.Contains(out, "not available for verification") {
		t.Fatalf("a trusted key must verify:\n%s", out)
	}
	out = sh("apt install nettop")
	if !strings.Contains(out, "Setting up nettop") {
		t.Fatalf("the vendor package should install once its key is trusted:\n%s", out)
	}
	if pc.InstalledFrom["nettop"] != "sashimi" {
		t.Fatalf("the record must show where it came from, got %q", pc.InstalledFrom["nettop"])
	}

	// ...and it shipped a background process nobody asked for, which the world
	// records as what it is
	if !isProc(pc, "updater") {
		t.Fatal("the vendor package's background updater must be a real process")
	}
	if out = sh("ps"); !strings.Contains(out, "updater") {
		t.Fatalf("ps must show the process it started:\n%s", out)
	}
	found := false
	for _, e := range w.Events {
		if e.Dev == pc.ID && strings.Contains(e.Message, "third-party package") {
			found = true
		}
	}
	if !found {
		t.Fatal("starting a background service from a third-party package must be in the event log")
	}
	if _, ok := pc.FS.Get("/var/lib/nettop/README"); !ok {
		t.Fatal("the payload's own files must land, exactly as the package describes")
	}
}

// Reachability is part of the contract: if the mirror is down, the box cannot
// update and cannot install, and bringing it back fixes that.
func TestMirrorOutageBreaksInstalls(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	mir := w.Devices["mirror"]

	if _, err := mir.StopService("nginx"); err != nil {
		t.Fatalf("stopping the mirror's web server: %v", err)
	}
	out := run(t, w, pc, "root", "apt update")
	if !strings.Contains(out, "connect") && !strings.Contains(out, "refused") {
		t.Fatalf("a mirror that is down must fail the update:\n%s", out)
	}

	// --- recovery
	if _, err := mir.StartService("nginx"); err != nil {
		t.Fatalf("starting the mirror's web server: %v", err)
	}
	out = run(t, w, pc, "root", "apt update")
	if strings.Contains(out, "refused") {
		t.Fatalf("with the mirror back the update must work:\n%s", out)
	}
	if out = run(t, w, pc, "root", "apt install htop"); !strings.Contains(out, "Setting up htop") {
		t.Fatalf("installs must work again once the mirror is back:\n%s", out)
	}
}

// Provisioning is not cosmetic: a VPS bought as Alpine is Alpine, with apk,
// Alpine's sources file, and a sudo account that can actually install.
func TestProvisionedVPSRunsTheDistroItWasBoughtWith(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)

	cases := []struct {
		image, manager, sourcesFile string
	}{
		{"debian", "apt", "/etc/apt/sources.list"},
		{"alpine", "apk", "/etc/apk/repositories"},
		{"arch", "pacman", "/etc/pacman.conf"},
		{"fedora", "dnf", "/etc/yum.repos.d/neohome.repo"},
	}
	for _, c := range cases {
		v, _, err := w.ProvisionVPSWithOS("alex", "small-2", "node-"+c.image, c.image)
		if err != nil {
			t.Fatalf("provisioning a %s VPS: %v", c.image, err)
		}
		if got := core.ManagerFor(v); got != c.manager {
			t.Fatalf("a %s VPS should use %s, got %q (%s %s)", c.image, c.manager, got, v.OS.Distro, v.OS.Ver)
		}
		if _, ok := v.FS.Read(c.sourcesFile); !ok {
			t.Fatalf("a %s image must ship %s", c.image, c.sourcesFile)
		}
		if v.FindUser("root") == nil {
			t.Fatalf("a %s image must have a root account for sudo to reach", c.image)
		}
		if _, ok := v.FS.Read(core.DistroFor(v).Keyring + "/neohome-archive.asc"); !ok {
			t.Fatalf("a %s image must trust the archive key", c.image)
		}
		// a fresh image has no cached lists: update, then install over sudo
		pw := v.FindUser("deploy").Pass
		if out := runWithStdin(t, w, v, "deploy", "sudo "+updateCommand(c.manager), pw); strings.Contains(out, "command not found") {
			t.Fatalf("the %s manager must exist on a %s box:\n%s", c.manager, c.image, out)
		}
		out := runWithStdin(t, w, v, "deploy", "sudo "+installCommand(c.manager), pw)
		if !strings.Contains(out, "command not found") && len(out) == 0 {
			t.Fatalf("installing on the %s VPS produced no output", c.image)
		}
		if len(v.Installed) == 0 {
			t.Fatalf("installing on the %s VPS must really install something:\n%s", c.image, out)
		}
		if v.InstalledFrom["htop"] != c.image && v.InstalledFrom["nginx"] != c.image {
			t.Fatalf("the %s VPS must record the repository the package came from: %v", c.image, v.InstalledFrom)
		}
	}

	// and the wrong manager on that box is still a command that is not there
	v := w.Devices["vps-node-arch"]
	if out := run(t, w, v, "deploy", "apt update"); !strings.Contains(out, "command not found") {
		t.Fatalf("apt on an Arch box must not exist:\n%s", out)
	}
}

// updateCommand is what a player would type to refresh the lists.
func updateCommand(mgr string) string {
	switch mgr {
	case "pacman":
		return "pacman -Sy"
	case "dnf":
		return "dnf update"
	}
	return mgr + " update"
}

// installCommand is what a player would type to install something with each
// manager on a fresh image.
func installCommand(mgr string) string {
	switch mgr {
	case "apt":
		return "apt install htop"
	case "apk":
		return "apk add htop"
	case "pacman":
		return "pacman -S htop"
	}
	return "dnf install htop"
}

// An interrupted sync is judged by what it left on the disk. When the archive
// has published something new, half a sync really is damage — and both the
// mirror and its clients can prove it byte for byte.
func TestInterruptedSyncLeavesVerifiableDamage(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	mir := w.Devices["mirror"]
	asst := w.Devices["asst-alex"]
	alp := w.Repos["alpine"]
	quietMirrorCron(t, w)

	// upstream publishes a new package: the bytes the mirror syncs change
	err := w.PublishPackage(alp, &core.VPkg{
		Name: "wget", Version: "1.24.5-r0", Comp: "main", Desc: "retrieves files from the web",
		Size: 300, Depends: []string{"musl"},
		Files: map[string]*core.PkgFile{"/usr/bin/wget": {Mode: 0755, Binary: true}},
	})
	if err != nil {
		t.Fatalf("publishing to the archive: %v", err)
	}
	archive := w.Devices["archive"]
	if _, ok := archive.FS.Get("/srv/www/archive/alpine/v3.20/main/x86_64/APKINDEX"); !ok {
		t.Fatal("the archive must publish the index for what it carries")
	}

	// the sync starts, copies the new index, and dies before the release file
	if err := w.StartMirrorSync(alp); err != nil {
		t.Fatalf("starting the sync: %v", err)
	}
	w.Tick() // phase 2: indexes (and the pool) land on the mirror
	if !isProc(mir, "mirror-sync") {
		t.Fatal("the sync should still be running after the index phase")
	}
	if !killSyncProc(mir, syncArgs(alp)) {
		t.Fatal("the alpine sync process should be in the table")
	}
	w.Tick()

	if alp.Status != "CORRUPTED" {
		t.Fatalf("a half-synced tree is corrupted, got %s (%s)", alp.Status, alp.StatusWhy)
	}
	if !strings.Contains(alp.StatusWhy, "does not match the release file") {
		t.Fatalf("the damage must be described as a checksum, got %q", alp.StatusWhy)
	}
	// the release file on the mirror really is the old one
	rel, ok := mir.FS.Read("/srv/www/mirror/alpine/v3.20/main/x86_64/APKINDEX.sig")
	if !ok || len(rel) == 0 {
		t.Fatal("the mirror should still serve the release file it synced last time")
	}

	// --- boundary: the client sees the same mismatch and installs nothing
	out := run(t, w, asst, "root", "apk update")
	if !strings.Contains(out, "hash sum mismatch") {
		t.Fatalf("the client must verify hashes itself and refuse:\n%s", out)
	}

	// --- recovery: a real sync, then the new package is installable
	if out = run(t, w, mir, "root", "mirror-sync alpine"); !strings.Contains(out, "sync started") {
		t.Fatalf("the repair must be a sync:\n%s", out)
	}
	for i := 0; i < 4; i++ {
		w.Tick()
	}
	if alp.Status != "SYNCED" {
		t.Fatalf("the repair sync should make the tree consistent, got %s (%s)", alp.Status, alp.StatusWhy)
	}
	run(t, w, asst, "root", "apk update")
	out = run(t, w, asst, "root", "apk add wget")
	if !strings.Contains(out, "wget") || asst.Installed["wget"] == nil {
		t.Fatalf("the newly published package must install after the repair:\n%s", out)
	}
	if _, ok := asst.FS.Get("/usr/bin/wget"); !ok {
		t.Fatal("the published payload's binary must be on disk after the install")
	}
	if v := asst.Installed["wget"].Version; v != "1.24.5-r0" {
		t.Fatalf("the installed version should be the one the archive published, got %q", v)
	}
}

// The mirror keeps itself current without a player: the syncs are cron jobs on
// the mirror host, and the one tree whose line was commented out is exactly the
// one that goes stale. This is the causal story of §10, in the world's own
// scheduler rather than in a status field.
func TestTheMirrorSyncsOnItsOwnScheduleAndTheDisabledLineDoesNot(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	mir := w.Devices["mirror"]

	svc := mir.CronDaemon()
	if svc == nil || svc.State != "running" {
		t.Fatalf("the mirror must run a scheduler to be a mirror, got %v", svc)
	}
	if n := mir.CronJobCount(); n < 5 {
		t.Fatalf("the mirror should have one sync job per tree it mirrors, got %d", n)
	}
	before := map[string]time.Time{}
	for name, r := range w.Repos {
		if name != "sashimi" {
			before[name] = r.LastSync
		}
	}

	// Let the world clock cross the next */15 window — at most fifteen
	// minutes of sim time — and let the scheduler and the syncs it starts
	// finish. Nothing here waits on wall-clock time.
	for i := 0; i < 36; i++ {
		w.Tick()
	}
	if got := cronRuns(w, mir, "mirror-sync alpine"); got == 0 {
		t.Fatal("the mirror's own scheduler should have run the alpine sync")
	}

	for name, r := range w.Repos {
		if name == "sashimi" || name == "debian" {
			continue
		}
		if !r.LastSync.After(before[name]) {
			t.Fatalf("%s should have been synced by its cron line (last sync %s, now %s)",
				name, core.HumanAge(w.Sim.Sub(r.LastSync)), w.Sim.Format("15:04"))
		}
		if r.Status != "SYNCED" {
			t.Fatalf("%s should be in sync after its scheduled run, got %s (%s)", name, r.Status, r.StatusWhy)
		}
	}
	// and the tree whose line is commented out is still the stale one
	deb := w.Repos["debian"]
	if deb.LastSync.After(before["debian"]) {
		t.Fatal("a commented-out cron line must not sync anything")
	}
	if deb.Status != "BEHIND" {
		t.Fatalf("the disabled tree must stay BEHIND, got %s", deb.Status)
	}
	// the scheduler says what it ran, on the mirror's own log
	logs, _ := mir.FS.Read("/var/log/syslog")
	if !strings.Contains(string(logs), "sync started for alpine") {
		t.Fatalf("the mirror's log should show the scheduled sync it ran:\n%s", logs)
	}
}

// ---- helpers ----

// cronRuns is how many times the scheduler has run a given command line on a
// device: proof that a job really fired, not merely that it exists.
func cronRuns(w *core.World, d *core.Device, command string) int {
	total := 0
	if w.Cron == nil {
		return 0
	}
	for _, e := range w.Cron.Entries {
		if e.DeviceID == d.ID && e.Command == command {
			total += e.Runs
		}
	}
	return total
}

func isProc(d *core.Device, name string) bool {
	for _, p := range d.Procs {
		if p.Name == name {
			return true
		}
	}
	return false
}

// syncArgs identifies the sync process for one tree, the way a player would
// read it out of ps: the mirror runs one sync per tree.
func syncArgs(r *core.Repo) string { return r.Distro + "/" + r.Suite }

// killSyncProc kills the sync of one tree and reports whether it found it —
// a sync for a *different* tree must not be disturbed.
func killSyncProc(d *core.Device, args string) bool {
	for i := 0; i < len(d.Procs); i++ {
		if d.Procs[i].Name == "mirror-sync" && d.Procs[i].Args == args {
			d.Procs = append(d.Procs[:i], d.Procs[i+1:]...)
			return true
		}
	}
	return false
}

// quietMirrorCron stops the mirror's scheduler. The mirror really does sync
// its trees on its own cron schedule, and a test that wants to observe one
// specific broken state should not race the scheduler that would repair it.
func quietMirrorCron(t *testing.T, w *core.World) {
	t.Helper()
	mir := w.Devices["mirror"]
	svc := mir.CronDaemon()
	if svc == nil {
		t.Fatal("the mirror should have a scheduler")
	}
	if _, err := mir.StopService(svc.Name); err != nil {
		t.Fatalf("stopping the mirror's scheduler: %v", err)
	}
}
