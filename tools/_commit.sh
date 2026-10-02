#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "ignore the scratch verify scripts

tools/_commit.sh was being tracked; it is a throwaway used to run the checks
before committing, not part of the project."
TOKEN=$(gh auth token)
git -c credential.helper="!f(){ echo username=x-access-token; echo password=$TOKEN; };f" push origin HEAD 2>&1 | tail -3
git log --oneline | head -1
