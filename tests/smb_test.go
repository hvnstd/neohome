package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The NAS shares files over SMB for real: the shares are what its own
// smb.conf declares, a guest share mounts with no credentials, and a
// protected share is an authenticated, ACL-checked mount.
func TestSMBServerIsReal(t *testing.T) {
	w := core.NewWorld()
	nas := w.Devices["nas-alex"]
	svc := nas.Svc("smbd")
	if svc == nil || svc.Port != 445 || svc.State != "running" || svc.Scope != "lan" {
		t.Fatalf("smbd is not a real LAN service: %+v", svc)
	}
	shares := w.SMBShares(nas)
	if len(shares) != 2 {
		t.Fatalf("expected data and media shares, got %+v", shares)
	}
	if shares[0].Name != "data" || !shares[0].GuestOK || shares[0].Path != "/srv/data" {
		t.Fatalf("the guest share is wrong: %+v", shares[0])
	}
	if shares[1].Name != "media" || shares[1].GuestOK || shares[1].Path != "/srv/media" {
		t.Fatalf("the protected share is wrong: %+v", shares[1])
	}
	if len(shares[1].ValidUsers) != 1 || shares[1].ValidUsers[0] != "alex" {
		t.Fatalf("the media share's ACL is wrong: %+v", shares[1].ValidUsers)
	}
	if data, ok := nas.FS.Read("/srv/media/index.txt"); !ok || !strings.Contains(string(data), "media share") {
		t.Fatal("the media share's files are not real")
	}
}

func TestSMBMountGuestShareAndUseIt(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	g := newGSession(t, w, pc, "alex")

	// discovery first, like a real user would
	out := g.exec("smbclient -L nas")
	if !strings.Contains(out, "data") || !strings.Contains(out, "media") {
		t.Fatalf("smbclient does not list the real shares:\n%s", out)
	}

	g.must("mounted //nas/data on /mnt/data (type cifs)",
		g.exec("mount -t cifs //nas/data /mnt/data"), "guest mount failed")
	g.must("vacation.txt", g.exec("ls /mnt/data/photos"), "mounted share does not show the real files")

	// writing through the mount writes the NAS
	g.exec("echo through smb >> /mnt/data/photos/vacation.txt")
	data, _ := nas.FS.Read("/srv/data/photos/vacation.txt")
	if !strings.Contains(string(data), "through smb") {
		t.Fatalf("the write did not land on the NAS:\n%q", data)
	}
	g.must("through smb", g.exec("cat /mnt/data/photos/vacation.txt"), "read-back through the mount failed")

	g.exec("umount /mnt/data")
	if len(pc.Mounts) != 0 {
		t.Fatalf("umount did not remove the mount: %+v", pc.Mounts)
	}
}

func TestSMBAuthAndACL(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	g := newGSession(t, w, pc, "alex")

	// no password: denied
	g.must("mount error(13): Permission denied",
		g.exec("mount -t cifs //nas/media /mnt/m", "wrongpass"), "wrong password mounted the share")
	// the real password, but a session that is not a NAS account: denied
	g.must("Permission denied",
		g.exec("mount -t cifs //nas/media /mnt/m -o user=assistant", "assist-pass"), "a foreign account mounted the share")
	// a real NAS account outside the share's valid users: denied by ACL
	g.must("not in valid users",
		g.exec("mount -t cifs //nas/media /mnt/m -o user=root", "nasroot"), "root mounted the alex-only share")
	// the honest way in
	g.must("mounted //nas/media on /mnt/m (type cifs)",
		g.exec("mount -t cifs //nas/media /mnt/m -o user=alex", "alex123"), "alex's own mount failed")
	g.must("family media share", g.exec("cat /mnt/m/index.txt"), "the mounted media share is empty")
	if len(pc.Mounts) != 1 {
		t.Fatalf("denied attempts left mounts behind: %+v", pc.Mounts)
	}

	// the unknown share is a real 404
	g.must("does not exist", g.exec("mount -t cifs //nas/nope /mnt/n"), "a bogus share mounted anyway")
}

func TestSMBGatedByServiceState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	g := newGSession(t, w, pc, "alex")

	if _, err := nas.StopService("smbd"); err != nil {
		t.Fatalf("stopping smbd failed: %v", err)
	}
	out := g.exec("smbclient -L nas")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped smbd must refuse smbclient:\n%s", out)
	}
	out = g.exec("mount -t cifs //nas/data /mnt/d")
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("a stopped smbd must refuse the mount:\n%s", out)
	}

	// mount while it is up, stop the daemon: the share goes stale, not fake
	if _, err := nas.StartService("smbd"); err != nil {
		t.Fatalf("starting smbd failed: %v", err)
	}
	g.must("mounted //nas/data on /mnt/d (type cifs)", g.exec("mount -t cifs //nas/data /mnt/d"), "guest mount failed")
	if _, err := nas.StopService("smbd"); err != nil {
		t.Fatalf("stopping smbd failed: %v", err)
	}
	g.must("Stale file handle", g.exec("cat /mnt/d/photos/vacation.txt"), "a dead smbd must not serve files")
	if _, err := nas.StartService("smbd"); err != nil {
		t.Fatalf("starting smbd failed: %v", err)
	}
	g.must("42 photos", g.exec("cat /mnt/d/photos/vacation.txt"), "the share did not recover with smbd")
	g.exec("umount /mnt/d")
	if len(pc.Mounts) != 0 {
		t.Fatalf("cleanup failed: %+v", pc.Mounts)
	}
}
