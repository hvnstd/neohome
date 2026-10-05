package core

// USB (WS-1.3): storage you can hold. A stick is a real device with a
// filesystem and no network interfaces — unattached, it exists in the
// world but no machine sees it; plugged in, it is the one bridge between
// machines that never talk to each other. That is §41's air gap in
// miniature: honest offline backup, and the classic delivery vector.
//
// The shape of USB is owned by this file; World only carries the pointer.

import (
	"fmt"
	"strings"
)

// USB tracks where each stick currently is. The sticks themselves are real
// devices (profile "usb") in w.Devices — their filesystems are the truth.
type USB struct {
	Attached map[string]string // stick device id -> host device id
}

// USBDeviceNode is the block-device file a host gets when a stick is
// plugged in. Its content names the backing device, so `mount` reads the
// truth from the node instead of guessing.
const USBDeviceNode = "/dev/sda1"

func (w *World) USBStick(id string) *Device {
	if w.Devices[id] != nil && w.Devices[id].Profile == "usb" {
		return w.Devices[id]
	}
	return nil
}

// AttachedTo returns the host a stick is plugged into, or "".
func (w *World) AttachedTo(stickID string) string {
	if w.USB == nil {
		return ""
	}
	return w.USB.Attached[stickID]
}

// seedUSB puts one stick in the household: alex's emergency stick, sitting
// unattached — in a drawer, effectively — with its old contents intact.
func seedUSB(w *World) {
	stick := w.Devices["usb-alex"]
	if stick == nil {
		return
	}
	if w.USB == nil {
		w.USB = &USB{Attached: map[string]string{}}
	}
	// FAT32 has no ownership model: the stick's filesystem belongs to its
	// owner, so a mounted stick is writable by them (the VFS defaults its
	// root to root:root, which would make every mount read-only)
	if n, ok := stick.FS.Get("/"); ok {
		n.Owner, n.Group = stick.Owner, stick.Owner
	}
	stick.FS.MkdirAll("/old-photos", 0755, "alex", "alex")
	stick.FS.Write("/readme.txt",
		"alex's emergency stick.\nthe offline backup routine lives in neohome-scripts on the git server.\n",
		0644, "alex", "alex")
	stick.FS.Write("/old-photos/vacation-2019.txt", "96 photos (placeholder)\n", 0644, "alex", "alex")
}

// USBPlug attaches a stick to the host the session runs on. The physical
// rules hold: a stick is in one place at a time, a host has one stick
// port, and both ends log the act.
func (w *World) USBPlug(host *Device, stickID, by string) error {
	if w.USB == nil {
		w.USB = &USB{Attached: map[string]string{}}
	}
	stick := w.USBStick(stickID)
	if stick == nil {
		return fmt.Errorf("no such stick: %s", stickID)
	}
	if where := w.USB.Attached[stickID]; where != "" {
		dst := w.Devices[where]
		name := where
		if dst != nil {
			name = dst.Hostname
		}
		return fmt.Errorf("the stick is attached to %s — unplug it there first", name)
	}
	for id, hostID := range w.USB.Attached {
		if hostID == host.ID {
			other := w.Devices[id]
			name := id
			if other != nil {
				name = other.Hostname
			}
			return fmt.Errorf("%s already has a stick attached (%s) — one port, one stick", host.Hostname, name)
		}
	}
	w.USB.Attached[stickID] = host.ID
	// the block device node is a real file whose content names the stick
	host.FS.MkdirAll("/dev", 0755, "root", "root")
	host.FS.Write(USBDeviceNode, "usb:"+stickID+"\n", 0660, "root", "disk")
	host.Logf("notice", "kernel", "usb %s attached by %s: storage device at %s", stick.Hostname, by, USBDeviceNode)
	w.AddEvent(host.ID, "notice", "kernel", "usb storage attached by %s", by)
	return nil
}

// USBUnplug detaches the stick from the host the session runs on. Mounts
// pointing at it go stale through the normal path, and the stick keeps
// every byte — that is the whole point of it.
func (w *World) USBUnplug(host *Device, stickID, by string) error {
	if w.USB == nil {
		return fmt.Errorf("no stick attached")
	}
	if w.USB.Attached[stickID] != host.ID {
		return fmt.Errorf("%s is not attached to %s", stickID, host.Hostname)
	}
	stick := w.USBStick(stickID)
	name := stickID
	if stick != nil {
		name = stick.Hostname
	}
	delete(w.USB.Attached, stickID)
	host.FS.Remove(USBDeviceNode)
	host.Logf("notice", "kernel", "usb %s detached by %s", name, by)
	w.AddEvent(host.ID, "notice", "kernel", "usb storage detached by %s", by)
	return nil
}

// USBStickFromNode reads a host's block device node and returns the stick
// it points at, or an error a real mount would give.
func (w *World) USBStickFromNode(host *Device, node string) (*Device, error) {
	data, ok := host.FS.Read(node)
	if !ok {
		return nil, fmt.Errorf("special device %s does not exist", node)
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "usb:") {
		return nil, fmt.Errorf("%s is not a usb storage device", node)
	}
	stick := w.USBStick(strings.TrimPrefix(line, "usb:"))
	if stick == nil {
		return nil, fmt.Errorf("%s names a device the world does not have", node)
	}
	if w.AttachedTo(stick.ID) != host.ID {
		return nil, fmt.Errorf("special device %s is not attached", node)
	}
	return stick, nil
}
