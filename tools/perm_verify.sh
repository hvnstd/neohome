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

# perm_verify.sh — privilege is a boundary, not a decoration.
#
# Before this work the world had no secrets on disk and reads were unchecked, so
# "compromise" meant nothing. Now:
#   - /etc/shadow exists, is 0640 root:shadow, and an ordinary account is refused
#   - a text tool cannot leak it either
#   - sudo consults /etc/sudoers and demands a password
#
# guest is a device account (unprivileged), so it is reached with `su guest`,
# the way a real attacker would land on one.
set -u
bash tools/killsrv.sh >/dev/null 2>&1
rm -f world.gob
go build -o neohome ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
./neohome > /tmp/neohome_perm.log 2>&1 &
SRV=$!
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat /tmp/neohome_perm.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# alex drives; "@" args are typed as answers to prompts
A() { go run ./tools/sshdrive/main.go alex alex123 "$@" 2>&1; }

say "1. the credential store exists, and its mode is right"
A 'ls -l /etc/shadow' 'ls -l /etc/sudoers' | tail -4

say "2. an ordinary account is refused, by cat and by grep alike"
A 'su guest' '@guest' 'cat /etc/shadow' 'grep root /etc/shadow' 'id' | tail -8

say "3. it is not a blanket block: normal files still read"
A 'su guest' '@guest' 'cat /etc/hosts' | tail -3

say "4. an account outside sudoers cannot escalate"
A 'su guest' '@guest' 'sudo cat /etc/shadow' | tail -3

say "5. a sudo member escalates, but only with the right password"
A 'sudo id' '@alex123' | tail -4
echo "--- and a wrong password is refused ---"
A 'sudo id' '@wrong-password' | tail -3

say "6. the defence side can see the probing"
A 'evidence' | tail -14

kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
echo "--- torn down ---"
