package core

import "time"

// CronState is the world's scheduler: real timed execution of real game
// commands against real devices. Nothing here may sleep a real wall-clock
// interval to gate gameplay — schedules fire against World.Sim.
//
// OWNERSHIP: this file belongs to the "scheduler + VM" workstream. Only
// *World.Cron is referenced elsewhere, so these types may grow freely.
type CronState struct {
	Entries []*CronEntry
}

// CronEntry is one scheduled command on one device.
type CronEntry struct {
	DeviceID string
	User     string
	Spec     string    // 5-field cron spec
	Command  string    // game-DSL command line
	Next     time.Time // next fire time on the world clock
	Last     time.Time
	Runs     int
	LastRC   int
}

// CronTick fires whatever is due on the world clock.
func (w *World) CronTick() {}

// seedCron plants the world's initial scheduled work.
func seedCron(w *World) {}
