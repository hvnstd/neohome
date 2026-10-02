#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "privilege is a boundary: /etc/shadow, /etc/sudoers, and enforced reads

The world had no secrets on disk and reads were never permission-checked, so
every account could read every file and 'compromise' was a fiction.

World:
  /etc/shadow   0640 root:shadow, deterministic simulated SHA-512 entries
  /etc/sudoers  0440 root:root, listing the accounts that may escalate
  a 'guest' account with no sudo rights, so the boundary is testable at all

VFS gains CanRead/CanExec + ReadPathAs, walking every path component so a
directory's x bit really gates what is inside. Existence and permission are
reported distinctly, so a missing file still says 'No such file' rather than
'Permission denied'.

Every reader goes through it: cat, grep, sed, head/tail, sort, uniq, wc, cp, mv.
The grep leak was found by the new test, not by inspection.

sudo now reads /etc/sudoers and authenticates the invoking user; su
authenticates the target account. Both record refusals, so the defensive side
sees probing rather than only successes.

Also fixed: 'ls -l FILE' treated -l as the path, and ls -l printed the owner in
the group column. tools/sshdrive can now answer in-session prompts ('@text'),
which is what makes any privileged path testable.

tools/perm_verify.sh drives all of it over real ssh."
git log --oneline | head -1
