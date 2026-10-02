#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./...
go vet ./...
go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -F - <<'MSG'
persistence actually works, and the server keeps its identity

The world was never being saved. Two independent bugs hid it: nothing ever
called Save (the engine's comment claimed it did), and the shutdown handler sat
after listen calls that block forever, so a clean exit silently lost everything.
The world file is now written on a timer and on exit.

- worldPath(): honour NEOHOME_WORLD, so the verify scripts stop scribbling on
  the repo's own world.gob
- engine() really saves; shutdown save moved into its own goroutine where it can
  actually be reached; logs "world saved to <path>"
- SSH host key is generated once and kept in the world, so the server is the
  same machine after a restart instead of a new one every boot
- loadOrCreate() reports whether it loaded or started fresh

68/68 tests. Live: run 1 leaves a file and takes a lease, is killed, saves;
run 2 loads it, the marker file is there and the router still shows the lease.
key_verify.sh: the key is identical across a restart and differs between worlds.
MSG
git log --oneline | head -2
