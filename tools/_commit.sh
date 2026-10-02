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
dhcp: a lease is real state on both ends

The router now actually allocates addresses and records them; a client actually
takes the address onto its interface, and loses its route when it cannot.

- dhcp.go: server allocates from the pool declared in the router's own
  dnsmasq.conf (so editing that file changes what can be handed out), sticky
  leases, a finite pool that refuses rather than invents addresses, release
  that frees the address again
- udhcpc: real BusyBox DHCP client, fails loudly with no server
- leases: the router's own lease table
- fix ip: `ip -4 addr show eth0` errored (only args[0] was parsed, and `show`
  was not accepted after the object)
- fix live_full.sh: its `ssh root@router "cmd"` never ran — the password prompt
  consumed the command line, so the router step was silently a no-op

67/67 tests; live ssh: udhcpc leases .50, the router's table shows it by
hostname, stopping dnsmasq really breaks the next lease, starting it restores it.
MSG
git log --oneline | head -2
