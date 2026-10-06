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

# persistence_verify.sh — proves the world really is saved and reloaded, and that
# the server keeps the same host identity across restarts.
set -u

W=/tmp/persist_world.gob
rm -f "$W"
go build -o /tmp/neohome-persist ./cmd/neohome || exit 1
bash tools/killsrv.sh >/dev/null 2>&1

start() {
  NEOHOME_WORLD="$W" /tmp/neohome-persist >/tmp/persist_server.log 2>&1 &
  SРV=$!
  sleep 2
}
# accepts several lines, so an interactive router login can be driven too
S() { go run ./tools/sshdrive/main.go alex alex123 "$@"; }

echo "########## run 1: change the world, then shut it down cleanly ##########"
NEOHOME_WORLD="$W" /tmp/neohome-persist >/tmp/persist_server.log 2>&1 &
SRV=$!
sleep 2
S "hostname"
echo "--- make a durable change: leave a file and take a DHCP lease ---"
S "echo persisted-marker > /home/alex/persist.txt"
S "udhcpc"
echo "--- host key fingerprint while running ---"
ssh-keyscan -t ed25519 -p 2222 127.0.0.1 2>/dev/null | tail -1 | awk '{print $2, $3}' | cut -c1-48
kill -TERM "$SRV" 2>/dev/null
sleep 2
echo "--- is the world on disk? ---"
ls -la "$W" 2>/dev/null || echo "NO WORLD FILE — persistence is broken"

echo
echo "########## run 2: the world must come back ##########"
NEOHOME_WORLD="$W" /tmp/neohome-persist >/tmp/persist_server2.log 2>&1 &
SRV2=$!
sleep 2
S "cat /home/alex/persist.txt"
S "ssh root@10.77.1.1" admin "leases" "exit"
echo "--- host key after restart (must be identical) ---"
ssh-keyscan -t ed25519 -p 2222 127.0.0.1 2>/dev/null | tail -1 | awk '{print $2, $3}' | cut -c1-48

kill -TERM "$SRV2" 2>/dev/null
sleep 1
bash tools/killsrv.sh >/dev/null 2>&1
echo
echo "########## server log run 1 ##########"
grep -iE 'world|save' /tmp/persist_server.log
echo "########## server log run 2 ##########"
grep -iE 'world|save|loaded' /tmp/persist_server2.log
echo
echo "########## done ##########"
