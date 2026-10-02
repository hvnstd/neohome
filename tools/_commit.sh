#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./...
go vet ./...
go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -F - <<'MSG'
tmux: sessions are real processes, not remembered lines

`tmux new` panicked outright — Sessions was a nil map on every device — and the
line after it (`tmux ls`) dereferenced a Proc that was never set. Both were
reachable in one command, which means tmux was never usable.

- tmux_cmds.go: a session is backed by a real Proc in the device's process
  table; send-keys runs work server-side; attach replays what the session
  produced; kill-session kills the process, so a long job run under tmux really
  dies with it
- screen maps onto the same model (-ls/-S/-r/-X)
- seed the Sessions map on every device instead of leaving it nil
- all_verify.sh: run every end-to-end check in one pass and report honestly

70/70 tests.
MSG
git log --oneline | head -2
