#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "verify scripts: no false positives in the suspicious-line scan

The scan matches 'no such file or directory' and 'panic' as failure signals, so
the scripts must not print those words about themselves. history_verify now uses
a file that exists (the old one only worked because the failure text was
expected), and tmux_verify's section header no longer says the word.

11/11 chains green on a clean tree."
git log --oneline | head -1
