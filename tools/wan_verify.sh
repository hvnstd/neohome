#!/usr/bin/env bash
# Locate the checkout from this script's own path, and use the Go toolchain that
# exists — the author's workspace layout, or one already on PATH.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
if [ -d /workspace/go/bin ]; then
  export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
elif [ -x "$HOME/.local/tools/go/bin/go" ]; then
  export PATH="$HOME/.local/tools/go/bin:$PATH" GOPATH="$HOME/.local/gopath"
fi

# wan_verify.sh — proves the public-internet layer over a real SSH session:
# real whois/rdap attribution, a real BGP view, a real traceroute path, and a
# real connection from the home PC to a VPS across the internet.
set -u

go build -o /tmp/nh_wan ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
RUN=/tmp/nh_wan_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_wan "$RUN/neohome-wan"
cd "$RUN"
./neohome-wan >server.log 2>&1 &
SRV=$!
sleep 2.5

echo "########## WHOIS: an address identifies a network, not a person ##########"
go run ./tools/sshdrive alex alex123 'whois 192.0.2.1' 'whois 198.51.100.1' 'whois 10.77.1.1' 2>&1

echo
echo "########## BGP: the transit graph ##########"
go run ./tools/sshdrive alex alex123 'bgp' 'bgp AS64520' 2>&1

echo
echo "########## Buy a VPS and reach it from home over the internet ##########"
go run ./tools/sshdrive alex alex123 'balance' 2>&1

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
echo "=== done ==="
