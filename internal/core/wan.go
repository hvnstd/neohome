package core

// WAN is the public-internet fabric: transit links, ASNs, prefix ownership and
// the rules that decide whether one public address can reach another.
//
// OWNERSHIP: this file belongs to the "internet transit" workstream. The rest of
// the codebase only ever holds it as *World.WAN, so this type's shape is free to
// change without touching any other file. Other workstreams must not edit it.
type WAN struct {
	Ready bool
}

// WANTick advances the public internet: route flapping, sync state, outages.
func (w *World) WANTick() {}

// seedWAN builds the public internet at world creation.
func seedWAN(w *World) {}
