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

# key_verify.sh — the server's SSH identity must be stable across restarts, and
# must actually change when the world is new. A regenerated-per-start key is not
# a host key.
set -u
bash tools/killsrv.sh >/dev/null 2>&1
go build -o /tmp/nh_key ./cmd/neohome || exit 1

fp() { # read the server's ed25519 public key exactly as an SSH client would
  ssh-keyscan -t ed25519 -p 2222 127.0.0.1 2>/dev/null | grep -v '^#' | head -1
}

boot() {
  NEOHOME_WORLD="$1" /tmp/nh_key >/tmp/key_server.log 2>&1 &
  P=$!
  sleep 2
}

echo "########## world A, first boot ##########"
rm -f /tmp/keyA.gob
boot /tmp/keyA.gob
A1=$(fp); echo "$A1" | cut -c1-40
kill -TERM $P; sleep 1

echo
echo "########## world A, rebooted (same world file) ##########"
boot /tmp/keyA.gob
A2=$(fp); echo "$A2" | cut -c1-40
kill -TERM $P; sleep 1

echo
echo "########## world B (a different world) ##########"
rm -f /tmp/keyB.gob
boot /tmp/keyB.gob
B1=$(fp); echo "$B1" | cut -c1-40
kill -TERM $P; sleep 1

bash tools/killsrv.sh >/dev/null 2>&1
echo
echo "########## results ##########"
[ -n "$A1" ] || { echo "FAIL: no host key was served at all"; exit 1; }
if [ "$A1" = "$A2" ]; then
  echo "PASS: the server keeps its identity across a restart"
else
  echo "FAIL: the host key changed on restart — the server is a different machine"
  echo "  before: $A1"
  echo "  after:  $A2"
fi
if [ "$A1" != "$B1" ]; then
  echo "PASS: a different world gets its own identity"
else
  echo "FAIL: two separate worlds share a host key"
fi
