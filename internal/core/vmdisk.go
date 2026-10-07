package core

import (
	"errors"
	"strings"
)

// ErrNoSpace is what a real kernel returns when a write hits a full filesystem.
var ErrNoSpace = errors.New("no space left on device")

// ErrIO is what a real kernel returns when a write hits a failing disk. It is
// a different errno from ENOSPC on purpose: the fix is not "remove a file" but
// "back up what you can and run fsck" (see DiskHealth).
var ErrIO = errors.New("input/output error")

// DiskFull reports whether this device's own storage is exhausted. It is one
// rule for every machine (§17): a guest's limit is its virtual disk, a host's is
// its own disk — see DiskLimitMB. A guest sized too small runs out on its own
// even when the host has terabytes free, and a PC with a full disk fails the
// same way, which is the whole point of "资源不是装饰".
func (d *Device) DiskFull() bool {
	limit := d.DiskLimitMB()
	return limit > 0 && d.FS.DiskUsedMB() >= limit
}

// WriteGuest performs a user write on a device, refusing it when the disk is
// full. This is the single gate that makes "磁盘满 -> 写入失败" a real
// consequence instead of a warning message: the data is not stored and the
// command reports a real ENOSPC error.
func (d *Device) WriteGuest(p string, data []byte, u *User) error {
	if d.DiskFailed() {
		return ErrIO
	}
	if d.DiskFull() {
		return ErrNoSpace
	}
	if err := d.FS.WriteChecked(p, data, u); err != nil {
		return err
	}
	// §33 auditd: a machine with a rule watching this path keeps a record of
	// the write. The rule is the switch; without one nothing is recorded.
	who := "?"
	if u != nil {
		who = u.Name
	}
	d.Audit("write", who, p, "ash")
	// §17: the disk counters are fed by the writes that really happen, so a
	// `vmstat` sample and `df` can never disagree about them
	d.NoteDiskWrite(len(data))
	return nil
}

// guestOf finds the guest a device is, if that device is a guest.
func (h *VMHost) guestOf(deviceID string) *VM {
	for _, list := range h.Hosts {
		for _, v := range list {
			if v.DeviceID == deviceID {
				return v
			}
		}
	}
	return nil
}

// VMRow is the shared row behind `vm list`: every value is read from world
// state, never invented, so the display can never disagree with the
// consequence.
func VMRow(v *VM) []string {
	th := v.Throttle()
	pri := "ok"
	switch {
	case v.LastState == "oom" || v.OOMCount > 0:
		pri = "OOM"
	case v.DiskFull():
		pri = "DISK-FULL"
	case v.SwapMB > 0:
		pri = "SWAP"
	case th < 0.999:
		pri = "CPU-STARVED"
	}
	return []string{
		v.Name,
		v.State,
		trimNum(v.VRAMMB),
		trimNum(v.MemUsedMB()),
		trimNum(v.SwapMB) + "/" + trimNum(v.SwapMaxMB),
		fmtPct(v.DiskPressure()),
		trimNum2(v.VCores),
		trimNum2(th),
		pri,
	}
}

func trimNum(n int) string {
	if n >= 1024 {
		return itoa(n/1024) + "G"
	}
	return itoa(n) + "M"
}

func trimNum2(f float64) string {
	return ftoa(f)
}

// ftoa formats a float with two decimals, trimming trailing zeros.
func ftoa(f float64) string {
	whole := int(f)
	frac := int((f-float64(whole))*100 + 0.5)
	if frac >= 100 {
		whole++
		frac = 0
	}
	s := itoa(whole)
	if frac == 0 {
		return s
	}
	fs := pad2(frac)
	fs = strings.TrimRight(fs, "0")
	return s + "." + fs
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func fmtPct(f float64) string { return ftoa(f*100) + "%" }
