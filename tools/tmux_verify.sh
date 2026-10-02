#!/usr/bin/env bash
# tmux_verify.sh — a detached session is a real session, not a note.
#
# Creating a session must start a real process; attaching must show that
# session's actual output; killing it must end the process.
set -u
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
cd /workspace/neohome
bash tools/killsrv.sh >/dev/null 2>&1
rm -f world.gob
go build -o neohome ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
./neohome > /tmp/neohome_tmux.log 2>&1 &
SRV=$!
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat /tmp/neohome_tmux.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
S() { go run ./tools/sshdrive/main.go alex alex123 "$1" 2>&1; }

say "1. no sessions is reported honestly (this used to panic)"
S 'tmux ls' | tail -3

say "2. creating a detached session starts a real process"
S 'tmux new -s build -d' | tail -3
S 'ps' | grep -iE 'tmux|screen' && echo "PASS: the session is a live process" \
  || echo "FAIL: no backing process"

say "3. the session shows its own output on attach"
S 'tmux send -t build "echo built-at-\$(date +%s)"' | tail -2
S 'tmux attach -t build' | tail -6
S 'tmux ls' | tail -3

say "4. killing the session really ends it"
S 'tmux kill-session -t build' | tail -2
S 'tmux ls' | tail -3
S 'ps' | grep -icE 'tmux|screen' | sed 's/^/processes matching tmux: /'

kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
echo "--- torn down ---"
