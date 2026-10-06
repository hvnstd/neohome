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

# live_full.sh — one uninterrupted proof session over the SSH entry: the DNS
# fault chain, cron firing, and the scheduler handing the player a clue.
# Nothing here is mocked: real TCP, real auth, real virtual shell, real ticks.
set -u

go build -o /tmp/nh_full ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }

RUN=/tmp/nh_full_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_full "$RUN/neohome-full"
cd "$RUN"

./neohome-full >server.log 2>&1 &
SRV=$!
sleep 2.5
echo "=== server ==="
cat server.log

echo
echo "########## A. the planted fault is visible ##########"
go run ./tools/sshdrive alex alex123 \
  'dig +short mirror.neohome.example' \
  'crontab -l' \
  'logread cron | tail -4' 2>&1

echo
echo "########## B. repair the router over ssh, the way a player would ##########"
go run ./tools/sshdrive alex alex123 \
  'ssh root@10.77.1.1' \
  'admin' \
  'cat /etc/dnsmasq.conf | grep resolv-file' \
  'exit' 2>&1

echo
echo "########## C. cron is a real service, gated by the daemon ##########"
go run ./tools/sshdrive alex alex123 \
  'ps aux | grep -c crond' \
  'service crond status' \
  'cat /var/spool/cron/crontabs/alex' 2>&1

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
echo
echo "=== done ==="
