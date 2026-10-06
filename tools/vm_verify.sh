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

# vm_verify.sh — proves virtualisation over a real SSH session: a guest is a
# real machine, it is reachable, and its resource pressure has real effects.
set -u

go build -o /tmp/nh_vm ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
RUN=/tmp/nh_vm_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_vm "$RUN/neohome-vm"
cd "$RUN"
./neohome-vm >server.log 2>&1 &
SRV=$!
sleep 2.5

echo "########## the household's own server is the hypervisor ##########"
go run ./tools/sshdrive alex alex123 'vm host' 'vm list' 2>&1

echo
echo "########## create a guest: a real machine with an address ##########"
go run ./tools/sshdrive alex alex123 'vm create web --cpu 2 --mem 2048 --disk 20480' 'vm top' 2>&1

echo
echo "########## it answers on the network like any other host ##########"
go run ./tools/sshdrive alex alex123 'ping -c2 10.77.1.2' 'balance' 2>&1

echo
echo "########## a guest sized far too small really dies ##########"
go run ./tools/sshdrive alex alex123 \
  'vm create doomed --cpu 1 --mem 256 --disk 64' \
  'vm list' 2>&1

echo
echo "########## stop / start are real state changes ##########"
go run ./tools/sshdrive alex alex123 'vm stop web' 'vm list' 'vm start web' 'vm list' 2>&1

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
echo "=== done ==="