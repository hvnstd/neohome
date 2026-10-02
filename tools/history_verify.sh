#!/usr/bin/env bash
# history_verify.sh — the trail is real, in both directions.
#
# Attack side:  commands typed on a box land in that account's history FILE.
# Defence side: another session can read the trail back, and clearing it is a
#               recorded act rather than a free win.
set -u
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
cd /workspace/neohome
bash tools/killsrv.sh >/dev/null 2>&1
rm -f world.gob
go build -o neohome ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
./neohome > /tmp/neohome_hist.log 2>&1 &
SRV=$!
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat /tmp/neohome_hist.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# a full interactive ssh session, so the shell loop (and its history) really runs
S() { go run ./tools/sshdrive/main.go alex alex123 "$1" 2>&1; }

say "1. an operator works on the pc and leaves a trail"
S 'cat /etc/hosts > /tmp/loot.txt' | tail -3

say "2. the trail is a file, and a later session sees it"
S 'cat /home/alex/.bash_history' | grep -E 'loot|shadow' \
  && echo "PASS: the trail is on disk and readable from a new login" \
  || echo "FAIL: no trail on disk"

say "3. clearing the history is a deliberate act"
S 'history -c' | tail -4

say "4. the act of clearing it was recorded in the evidence graph"
S 'evidence' | tail -16

kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
echo "--- torn down ---"
