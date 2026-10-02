#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
rm -rf tools/probe
go build ./...
go vet ./...
go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -F - <<'MSG'
power: the household supply is now a real mechanic

Cutting power genuinely darkens the house: Reach/Dial refuse on a dead host,
services report stopped, processes are gone. An outage is enforced, not narrated.

- power.go: supply state (breaker / arrears / pulled plug / UPS battery),
  enforced in net.go so a dark machine cannot open or accept a socket
- bmc: out-of-band controller on its own battery, so `power cut` is recoverable
  instead of a one-way door; the ssh entry lands there when the PC is dark
- power/ups/bmc commands; a UPS is a real purchase that really runs flat
- utility arrears now has the physical consequence of an outage
- fix grep: -c/-v/-i were parsed as the pattern, breaking `ps aux | grep -c x`
- drop 7 applet names that had no implementation

60/60 tests; live ssh: cut -> session dies -> reconnect to BMC -> power boot ->
house back, and the router answers again.
MSG
git log --oneline | head -3
