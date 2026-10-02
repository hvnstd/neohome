#!/usr/bin/env bash
# dhcp_verify.sh — proves DHCP is real over a live SSH session: a client takes a
# lease, the router records it, and stopping the server really breaks it.
set -u
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache

rm -f /tmp/dhcp_world.gob
go build -o /tmp/neohome-dhcp ./cmd/neohome || exit 1
bash tools/killsrv.sh >/dev/null 2>&1
NEOHOME_WORLD=/tmp/dhcp_world.gob /tmp/neohome-dhcp >/tmp/dhcp_server.log 2>&1 &
sleep 1.5
trap 'bash tools/killsrv.sh >/dev/null 2>&1' EXIT

S() { go run ./tools/sshdrive/main.go alex alex123 "$1"; }

echo "########## the client asks for an address ##########"
S "ip -4 addr show eth0"
S "udhcpc"
S "ip -4 addr show eth0"

# Logging in to the router is interactive: the password prompt consumes the next
# line, so the remote shell is driven by feeding a line at a time.
# the router prompts for a password, so "admin" is fed as the next line
R() { go run ./tools/sshdrive/main.go alex alex123 "ssh root@10.77.1.1" admin "$@"; }

echo
echo "########## the router's own lease table agrees ##########"
R "leases" "exit"

echo
echo "########## stop the DHCP server: the next lease must fail ##########"
R "/etc/init.d/dnsmasq stop" "exit"
S "udhcpc"
echo "--- the failure above is correct: no server, no lease ---"

echo
echo "########## bring it back ##########"
R "/etc/init.d/dnsmasq start" "exit"
S "udhcpc"
R "leases" "exit"

echo
echo "########## done ##########"
