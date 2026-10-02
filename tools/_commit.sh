#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "telnet is a real login, and ss stops lying about the WAN

telnet handed out a root shell with no authentication at all, which made every
privilege check in the world pointless: an attacker could simply telnet to the
target and be root. It now asks for an account and a password, refuses politely
(never revealing whether the account exists), bumps fail2ban, and records both
the attempt and the failure.

The router also gains a legacy telnetd on 23 — the classic weak entry point, so
finding credentials there is a genuine foothold rather than a free shell.

ss printed LAN-scoped sockets on the router's WAN address, telling an attacker
that the router's ssh and telnet were exposed to the internet. Dial already
refused them from the WAN (and the firewall drops them), so this was a display
bug that overstated the attack surface.

tools/telnet_verify.sh proves it over real ssh, without nc."
git log --oneline | head -1
