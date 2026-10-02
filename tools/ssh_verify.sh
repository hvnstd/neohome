#!/usr/bin/env bash
# ssh_verify.sh — proves the SSH entry works against a live server:
# real TCP connect, real password auth against the world's own player records,
# real virtual shell. Also re-checks the telnet path is unharmed.
set -u
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath

go build -o /tmp/nh_sshcheck ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }

RUN=/tmp/nh_run_dir
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_sshcheck "$RUN/neohome-sshcheck"
cd "$RUN"

./neohome-sshcheck >server.log 2>&1 &
SRV=$!
sleep 2.5

echo "=== server log ==="
cat server.log

echo
echo "=== listeners ==="
ss -tln 2>/dev/null | grep -E ':(2024|2222)' || echo "(ss unavailable)"

echo
echo "=== SSH session (real client, password auth) ==="
cd /workspace/neohome
go run ./tools/sshdrive alex alex123 'hostname' 'whoami' 'id' 'uname -a' 2>&1 | head -40

echo
echo "=== SSH with a WRONG password (must be refused) ==="
go run ./tools/sshdrive alex WRONGPASS 'hostname' 2>&1 | head -6

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
echo
echo "=== done ==="
