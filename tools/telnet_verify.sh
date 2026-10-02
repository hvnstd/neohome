#!/usr/bin/env bash
# telnet_verify.sh — the legacy service is a real login, not a free shell.
#
# telnet used to hand out a root shell with no authentication, which made every
# privilege check pointless: an attacker could just telnet in. The router now
# carries a legacy telnetd on 23, and it demands an account and password.
#
# sshdrive args: a command to run, or "@text" to type text at a prompt.
set -u
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
cd /workspace/neohome
bash tools/killsrv.sh >/dev/null 2>&1
rm -f world.gob
go build -o neohome ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
./neohome > /tmp/neohome_telnet.log 2>&1 &
SRV=$!
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat /tmp/neohome_telnet.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
S() { go run ./tools/sshdrive/main.go alex alex123 "$@" 2>&1; }

say "1. the legacy port is open on the router and closed on the pc"
echo "--- from the router itself ---"
S 'telnet 10.77.1.1' '@root' '@admin' 'ss -tlnp' 'exit' | grep -E '23|22|LISTEN' | head -6
echo "--- from the pc (its own sshd is down, so nothing should listen) ---"
S 'ss -tlnp' | tail -4

say "2. a wrong password is refused (the old code let you straight in)"
S 'telnet 10.77.1.1' '@root' '@definitely-wrong' | tail -5

say "3. the correct credentials open a real shell on the router"
S 'telnet 10.77.1.1' '@root' '@admin' 'id' 'cat /etc/shadow' 'exit' | tail -10

say "4. both the success and the refusal are recorded for the defender"
S 'evidence' | tail -10

kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
echo "--- torn down ---"
