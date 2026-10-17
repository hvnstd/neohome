package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §29: the vulnerabilities beyond the neighbour's FTP drop are world state, not
// a list of labels. Each one is detected from the box's own live configuration
// and each exploit performs its action over the wire, through the same service
// a player would talk to. These tests drive `recon` and `exploit` as a player
// does and then assert on real state (a file on the attacker, a job in the
// printer's spool, a working credential).

// householdTarget is one seeded exposure: the box that really holds it, the
// address a player reaches it on, and the vuln id recon must name.
type householdTarget struct {
	label  string
	device string // World device id
	vulnID string
}

func householdTargets() []householdTarget {
	return []householdTarget{
		{"router telnet", "router-alex", "router-telnet-default"},
		{"nas guest smb", "nas-alex", "nas-smb-guest-share"},
		{"laptop weak account", "laptop-alex", "guest-weak-account"},
		{"printer cups", "prn-alex", "printer-cups-noauth"},
		{"bazaar drop", "bazaar", "bazaar-anon-drop"},
	}
}

// vulnProbe returns the source device/user that reaches the target and the
// address to use. A household box is reached from the family PC on its LAN
// address; the public bazaar is reached from an infrastructure host on its
// public address (the home PC's LAN view of the market is not what matters).
func vulnProbe(w *core.World, targetID string) (*core.Device, string, string) {
	if targetID == "bazaar" {
		bz := w.Devices["bazaar"]
		return w.Devices["mirror"], "root", bz.WANIP()
	}
	return w.Devices["pc-alex"], "alex", w.Devices[targetID].FirstLANIP()
}

// TestVulnReconListsHouseholdExposures drives recon against every seeded box
// that carries a new exposure and expects the matching id on its own box.
func TestVulnReconListsHouseholdExposures(t *testing.T) {
	w := core.NewWorld()
	for _, tc := range householdTargets() {
		src, user, addr := vulnProbe(w, tc.device)
		out := run(t, w, src, user, "recon "+addr)
		if !strings.Contains(out, tc.vulnID) {
			t.Fatalf("recon of %s (%s) must name %s:\n%s", tc.label, addr, tc.vulnID, out)
		}
	}
}

// TestVulnReconDropsWhenPreconditionFixed repairs each condition for real and
// checks recon stops reporting it: a fix that does not remove the finding is a
// lie about the world.
func TestVulnReconDropsWhenPreconditionFixed(t *testing.T) {
	w := core.NewWorld()

	// router: the legacy telnetd is stopped and the default root password is
	// rotated — either one is enough, both together are what an owner does.
	router := w.Devices["router-alex"]
	if _, err := router.StopService("telnetd"); err != nil {
		t.Fatalf("stopping telnetd: %v", err)
	}
	if err := router.ChangePassword("root", "notadmin"); err != nil {
		t.Fatalf("rotating router root password: %v", err)
	}

	// nas: the share daemon is off, so nothing serves the guest share
	nas := w.Devices["nas-alex"]
	if _, err := nas.StopService("smbd"); err != nil {
		t.Fatalf("stopping smbd: %v", err)
	}

	// laptop: the guest account's password is no longer its own name
	lap := w.Devices["laptop-alex"]
	if err := lap.ChangePassword("guest", "notguest"); err != nil {
		t.Fatalf("rotating guest password: %v", err)
	}

	// printer: cupsd is stopped
	prn := w.Devices["prn-alex"]
	if _, err := prn.StopService("cupsd"); err != nil {
		t.Fatalf("stopping cupsd: %v", err)
	}

	// bazaar: anonymous access is turned off in the daemon's own config file
	bazaar := w.Devices["bazaar"]
	bazaar.FS.Write("/etc/vsftpd.conf",
		"listen=YES\nanonymous_enable=NO\nlocal_enable=YES\nwrite_enable=YES\n",
		0644, "root", "root")

	for _, tc := range householdTargets() {
		src, user, addr := vulnProbe(w, tc.device)
		out := run(t, w, src, user, "recon "+addr)
		if strings.Contains(out, tc.vulnID) {
			t.Fatalf("recon still reports %s after the precondition was fixed:\n%s", tc.vulnID, out)
		}
	}
}

// TestVulnExploitSMBGuestShareReadsRealBytes follows the guest share from the
// exploit through to the bytes landing on the attacker: the effect has to read
// the NAS's own file as the account a guest session runs as.
func TestVulnExploitSMBGuestShareReadsRealBytes(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]

	out := run(t, w, pc, "alex", "exploit "+nas.FirstLANIP()+" nas-smb-guest-share")
	if !strings.Contains(out, "mounted //nas/data as guest (no credentials)") ||
		!strings.Contains(out, "fetched /srv/data/backups.tar.gz") {
		t.Fatalf("the guest-share exploit must mount and read over the wire:\n%s", out)
	}
	got, ok := pc.FS.Read("/tmp/backups.tar.gz")
	want, has := nas.FS.Read("/srv/data/backups.tar.gz")
	if !has {
		t.Fatal("precondition: the NAS backup file must exist")
	}
	if !ok || string(got) != string(want) {
		t.Fatalf("the guest read must deliver the NAS's own bytes: got %q want %q", got, want)
	}
}

// TestVulnExploitCupsQueuesRealJob checks the print effect is not a sentence:
// a real unauthorised job ends up in the printer's own spool queue.
func TestVulnExploitCupsQueuesRealJob(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	prn := w.Devices["prn-alex"]
	before := len(prn.Printer.Queue)

	out := run(t, w, pc, "alex", "exploit "+prn.FirstLANIP()+" printer-cups-noauth")
	if !strings.Contains(out, "unauthorized job queued") {
		t.Fatalf("the CUPS exploit must queue a job:\n%s", out)
	}
	if len(prn.Printer.Queue) != before+1 {
		t.Fatalf("the job must be in the printer's real spool queue: %+v", prn.Printer.Queue)
	}
	job := prn.Printer.Queue[len(prn.Printer.Queue)-1]
	if job.Title != "unauthorized page" || job.Owner != "alex" {
		t.Fatalf("the queued job does not match the declared effect: %+v", job)
	}
}

// TestVulnExploitGuestAccountLogsInForReal proves the recovered credential is
// usable: the exploit reports guest:guest, and that login opens a session on
// the laptop through the ordinary ssh path.
func TestVulnExploitGuestAccountLogsInForReal(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	lap := w.Devices["laptop-alex"]

	out := run(t, w, pc, "alex", "exploit "+lap.FirstLANIP()+" guest-weak-account")
	if !strings.Contains(out, "low-privilege credential: guest:guest") {
		t.Fatalf("the weak-account exploit must report the real password:\n%s", out)
	}
	out = runWithStdin(t, w, pc, "alex", "ssh guest@"+lap.FirstLANIP()+" echo hi", "guest")
	if !strings.Contains(out, "Welcome to laptop, guest!") {
		t.Fatalf("the recovered credential must really log in:\n%s", out)
	}
}

// TestVulnExploitBazaarDropIsReal checks the public dead drop from an
// internet-facing host: an anonymous write lands in the drop directory.
func TestVulnExploitBazaarDropIsReal(t *testing.T) {
	w := core.NewWorld()
	mirror := w.Devices["mirror"]
	bazaar := w.Devices["bazaar"]

	out := run(t, w, mirror, "root", "exploit "+bazaar.WANIP()+" bazaar-anon-drop")
	if !strings.Contains(out, "anonymous write accepted: /srv/bazaar/drops/.probe") {
		t.Fatalf("the bazaar drop exploit must upload over the wire:\n%s", out)
	}
	if !bazaar.FS.Exists("/srv/bazaar/drops/.probe") {
		t.Fatal("the anonymous upload must be a real file in the drop directory")
	}
}

// TestVulnExploitRefusalNamesTheFix runs an exploit whose precondition is false
// and expects the honest refusal plus the vuln's own hint, not a crash or a
// silent success.
func TestVulnExploitRefusalNamesTheFix(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	if _, err := nas.StopService("smbd"); err != nil {
		t.Fatalf("stopping smbd: %v", err)
	}

	out := run(t, w, pc, "alex", "exploit "+nas.FirstLANIP()+" nas-smb-guest-share")
	if !strings.Contains(out, "precondition failed") {
		t.Fatalf("an exploit without its precondition must be refused:\n%s", out)
	}
	if !strings.Contains(out, "smbclient -L nas") {
		t.Fatalf("the refusal must name the fix from the vuln's help:\n%s", out)
	}
}
