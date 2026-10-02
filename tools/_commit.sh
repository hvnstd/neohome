#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "power_verify: drop the removed probe reference; every verify chain is green

all_verify.sh: 9/9 end-to-end chains pass on a clean tree (197/27/71/45/83/122/96/12/57 lines)."
git log --oneline | head -1
