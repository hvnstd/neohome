#!/usr/bin/env bash
# live_full.sh — one uninterrupted proof session over the SSH entry: the DNS
# fault chain, cron firing, and the scheduler handing the player a clue.
# Nothing here is mocked: real TCP, real auth, real virtual shell, real ticks.
set -u
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath

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

cd /workspace/neohome
echo
echo "########## A. the planted fault is visible ##########"
go run ./tools/sshdrive alex alex123 \
  'dig +short mirror.neohome.example' \
  'crontab -l' \
  'logread cron | tail -4' 2>&1

echo
echo "########## B. repair the router over ssh, the way a player would ##########"
go run ./tools/sshdrive alex alex123 \
  'ssh root@10.77.1.1 "cat /etc/dnsmasq.conf | grep resolv-file"' 2>&1

cd /workspace/neohome
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
