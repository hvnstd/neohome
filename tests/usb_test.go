package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// A stick is a device with a filesystem and no interfaces: unattached it
// exists but nothing sees it, and the household stick starts in a drawer.
func TestUSBStickIsWorldState(t *testing.T) {
	w := core.NewWorld()
	stick := w.USBStick("usb-alex")
	if stick == nil {
		t.Fatal("the household stick is missing")
	}
	if len(stick.Ifaces) != 0 {
		t.Fatalf("a usb stick must not have network interfaces: %+v", stick.Ifaces)
	}
	if w.AttachedTo("usb-alex") != "" {
		t.Fatal("the stick should start unattached")
	}
	if data, ok := stick.FS.Read("/readme.txt"); !ok || !strings.Contains(string(data), "emergency stick") {
		t.Fatalf("the stick's seeded contents are missing:\n%s", data)
	}
}

// The full journey: plug, mount, read, write, unplug, carry, plug in
// elsewhere — the files travel, and every break in the chain is honest.
func TestUSBPlugMountAndAirGap(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	nas := w.Devices["nas-alex"]
	g := newGSession(t, w, pc, "alex")

	// nothing is attached: mounting the node must say so
	g.must("does not exist", g.exec("mount -t vfat /dev/sda1 /mnt/usb"), "mount without a stick must fail")

	// plug in on the PC: a real device node appears, kernel and camera see it
	g.must("storage device at /dev/sda1", g.exec("usb plug usb-alex"), "plug failed")
	if node, ok := pc.FS.Read("/dev/sda1"); !ok || !strings.Contains(string(node), "usb:usb-alex") {
		t.Fatalf("the device node is not a real, self-describing file:\n%s", node)
	}
	syslog, _ := pc.FS.Read("/var/log/syslog")
	if !strings.Contains(string(syslog), "usb usb-stick attached by alex") {
		t.Fatal("the attach left no kernel log")
	}

	// mount and use it: the seeded files are there, writes stick
	g.must("mounted /dev/sda1 on /mnt/usb (type vfat)",
		g.exec("mount -t vfat /dev/sda1 /mnt/usb"), "mount failed")
	g.must("emergency stick", g.exec("cat /mnt/usb/readme.txt"), "seeded file missing")
	g.exec("echo backup-2026 > /mnt/usb/backup.txt")
	if data, ok := stickFS(t, w).Read("/backup.txt"); !ok || strings.TrimSpace(string(data)) != "backup-2026" {
		t.Fatalf("the write did not land on the stick:\n%q", data)
	}

	// unplug while mounted: stale, not fake
	g.exec("usb unplug")
	if _, ok := pc.FS.Read("/dev/sda1"); ok {
		t.Fatal("the device node survived the unplug")
	}
	g.must("Stale file handle", g.exec("cat /mnt/usb/readme.txt"), "an unplugged stick must not serve files")

	// carry it to the NAS: everything written on the PC is there
	g2 := newGSession(t, w, nas, "alex")
	g2.must("attached: storage device at /dev/sda1", g2.exec("usb plug usb-alex"), "plug on nas failed")
	g2.must("mounted /dev/sda1 on /mnt/usb (type vfat)",
		g2.exec("mount -t vfat /dev/sda1 /mnt/usb"), "mount on nas failed")
	g2.must("backup-2026", g2.exec("cat /mnt/usb/backup.txt"), "the air-gap bridge lost the write")
	g2.must("emergency stick", g2.exec("cat /mnt/usb/readme.txt"), "the stick lost its own contents")

	// physical rules: the stick is in the NAS, the PC cannot claim it
	out := g.exec("usb plug usb-alex")
	if !strings.Contains(out, "unplug it there first") {
		t.Fatalf("a stick cannot be in two machines:\n%s", out)
	}
	// and the NAS's port is now occupied
	g2.exec("usb unplug")
	if err := w.USBPlug(nas, "usb-alex", "alex"); err != nil {
		t.Fatalf("replug after unplug should work: %v", err)
	}
	w.USBUnplug(nas, "usb-alex", "alex")
	if err := w.USBPlug(nas, "usb-alex", "alex"); err != nil {
		t.Fatalf("one port must be reusable after eject: %v", err)
	}
}

func stickFS(t *testing.T, w *core.World) *core.VFS {
	t.Helper()
	d := w.USBStick("usb-alex")
	if d == nil {
		t.Fatal("stick vanished")
	}
	return d.FS
}

// The stick's contents are world state: they survive a save/load like
// everything else that matters.
func TestUSBPersistence(t *testing.T) {
	w := core.NewWorld()
	if err := w.USBPlug(w.Devices["pc-alex"], "usb-alex", "alex"); err != nil {
		t.Fatalf("plug failed: %v", err)
	}
	stick := w.USBStick("usb-alex")
	stick.FS.Write("/secret.txt", "air-gapped\n", 0600, "alex", "alex")

	path := t.TempDir() + "/usb_world.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	w2, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if w2.USB == nil {
		t.Fatal("loaded world lost its USB registry")
	}
	if w2.AttachedTo("usb-alex") != "pc-alex" {
		t.Fatalf("the attachment did not survive: %q", w2.AttachedTo("usb-alex"))
	}
	if data, ok := w2.USBStick("usb-alex").FS.Read("/secret.txt"); !ok || string(data) != "air-gapped\n" {
		t.Fatalf("the stick's files did not survive the round trip:\n%q", data)
	}
}
