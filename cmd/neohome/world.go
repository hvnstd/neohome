package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neohome/internal/core"
)

// The world file lives beside the binary unless overridden, and the engine
// really saves it. A world that only ever lives in memory is not persistent, and
// "your machine keeps its state between sessions" is part of the premise.
func worldPath() string {
	if p := os.Getenv("NEOHOME_WORLD"); p != "" {
		return p
	}
	return "world.gob"
}

// --- ssh host identity ---------------------------------------------------
//
// The host key is generated once and kept with the world. Regenerating it on
// every start would mean the server is a different machine each time it boots,
// which is exactly the thing a host key exists to rule out.

func hostKey(w *core.World) ([]byte, error) {
	if len(w.SSHHostKey) > 0 {
		return w.SSHHostKey, nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	w.SSHHostKey = der
	return der, nil
}

// saveInterval is how often the engine commits the world to disk.
const saveInterval = 12 // ticks (≈36s at 3s/tick)

// tickInterval is the wall-clock cost of one half-minute of world time. §37
// compresses real minutes into game seconds, and NEOHOME_TICK_MS exists so a
// live walk-through of a ladder measured in sim-hours (a desk's service level, a
// law file's rungs) can be watched in one sitting. State is still only moved by
// Tick(): the knob changes the ratio, never the rules.
func tickInterval() time.Duration {
	if v := os.Getenv("NEOHOME_TICK_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 20 {
			return time.Duration(ms) * time.Millisecond
		}
		log.Printf("NEOHOME_TICK_MS=%q is not a usable interval (>=20); using 3000", v)
	}
	return 3 * time.Second
}

func engine(w *core.World, path string) {
	tick := 0
	interval := tickInterval()
	log.Printf("engine: one tick = 30s of world time every %s", interval)
	for {
		w.Lock()
		w.Tick()
		tick++
		due := tick%saveInterval == 0
		var err error
		if due {
			err = w.Save(path)
		}
		w.Unlock()
		if due {
			if err != nil {
				log.Println("world save failed:", err)
			}
		}
		time.Sleep(interval)
	}
}

// loadOrCreate loads the saved world, or makes a fresh one. Either way the
// world's SSH identity is established before the listener signs anything.
func loadOrCreate(path string) *core.World {
	w, err := core.LoadWorld(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Println("world load failed, starting fresh:", err)
		}
		w = core.NewWorld()
		// make sure the file's directory exists
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			_ = os.MkdirAll(dir, 0755)
		}
	} else {
		log.Printf("world loaded from %s", path)
	}
	if _, err := hostKey(w); err != nil {
		log.Fatalf("host key: %v", err)
	}
	return w
}

var _ = fmt.Sprintf
var _ = strings.TrimSpace
