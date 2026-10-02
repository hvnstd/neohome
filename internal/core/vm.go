package core

// VMHost is the state of virtualisation in the world: hypervisors, the guests
// they run, and the resource accounting that makes guest pressure real
// (RAM exhaustion -> swap -> OOM -> service exits).
//
// OWNERSHIP: this file belongs to the "scheduler + VM" workstream. Only
// *World.VMs is referenced elsewhere, so these types may grow freely.
type VMHost struct {
	Ready bool
}

// VM is a virtual machine: a simulated state machine, never a real host VM.
type VM struct {
	ID       string
	Name     string
	HostID   string
	DeviceID string // the Device that represents this guest in the world
	State    string // running|stopped|paused
}

// VMTick advances guest resource pressure.
func (w *World) VMTick() {}

// seedVMs plants the world's initial virtualisation state.
func seedVMs(w *World) {}
