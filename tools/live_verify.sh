#!/bin/bash
# live_verify.sh — build, launch, drive a real telnet session, tear down.
set -u
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
cd /workspace/neohome

# stop any previous server by port owner, not by pattern (avoids self-kill)
if command -v fuser >/dev/null 2>&1; then
  fuser -k 2024/tcp 2>/dev/null
fi
sleep 1

rm -f world.gob
go build -o neohome ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
./neohome > /tmp/neohome.log 2>&1 &
SRV=$!
sleep 2
if ! kill -0 "$SRV" 2>/dev/null; then
  echo "SERVER DIED:"; cat /tmp/neohome.log; exit 1
fi
echo "server pid=$SRV up"
go run ./tools/live_drive.go
echo "--- server log ---"
tail -20 /tmp/neohome.log
kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
echo "--- torn down ---"
