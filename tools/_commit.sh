#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "shell history is a file on the device: evidence, in both directions

Typed commands land in the account's real history file (~/.bash_history,
~/.ash_history, HISTFILE), loaded at login the way bash loads it. So:
  - the attacker's trail survives the session and is readable by a defender
  - \`history -c\` clears it, and that act is itself recorded
  - a BusyBox box gets .ash_history, not bash's file

Also fixed: /bin/bash contains the substring \"ash\", so the shell-name check
had to test bash first or every account on a bash box got ash's history file.

tools/history_verify.sh drives the whole loop over real ssh."
git log --oneline | head -1
