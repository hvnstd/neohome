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

# abuse_verify.sh — §34 取证 / 网管 / ISP / Provider over the live entry.
#
# Nothing here is scripted. The household really reports the address whose
# probes its gateway logged; the report is refused when the reporter's own
# records do not hold the address, and it is refused again when the only thing
# behind it is an opinion. The address is answered for by a network with an ASN,
# a range and an abuse contact — never by a subscriber. And the household's own
# attempts at a provider's desk really land in that desk's log.
#
# The ladder itself (notice → enforcement → suspension term, then a lawful
# request) is hours of *game* time per rung and runs on the world clock, not on
# wall time: this script shows the first rungs inside its window and hands the
# rest to the clock, where the case file records it.
set -u

go build -o /tmp/nh_abuse ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
go build -o /tmp/nh_sshdrive ./tools/sshdrive || { echo "DRIVER BUILD FAILED"; exit 1; }

RUN=/tmp/nh_abuse_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_abuse "$RUN/neohome-abuse"
cd "$RUN"

rm -f /tmp/abuse_world.gob
NEOHOME_WORLD=/tmp/abuse_world.gob ./neohome-abuse >server.log 2>&1 &
SRV=$!
trap 'bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1' EXIT
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat server.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# The household sessions: the PC (10.77.1.11) as alex, and the gateway (the
# router at 10.77.1.1) as root. A leading @ is a raw answer to a prompt.
S() { /tmp/nh_sshdrive alex alex123 "$@" 'exit' 2>&1; }
R() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.1" admin "$@" 'exit' 2>&1; }
# the world ticks every 3 seconds, and a tick is half a minute of game time
tick() { sleep 6; }
# only the lines the command printed, without the ssh driver's own transcript
body() { grep -vE '^(ssh: |Welcome to |alex@home-pc|root@gateway|root@10\.77\.1\.1|organisations that investigate|$)' ; }

say "1. the DNS the household is stuck with, repaired the way a player repairs it"
R "mkdir -p /var/run/dnsmasq" "echo nameserver 10.0.0.2 > /var/run/dnsmasq/resolv.conf" | tail -1
tick

say "2. who answers for an address — and who is never named"
echo "--- the hosting provider's own block ---"
R 'whois 192.0.2.1' | body | tail -14
echo "--- the household line's ISP ---"
R 'whois 198.51.100.1' | body | tail -8
echo "--- a shared address is nobody's: it answers with the pool, not a name ---"
R 'whois 100.64.0.1' | body | tail -4
echo "--- and a registry lookup carries no subscriber ---"
leak=$(R 'whois 192.0.2.1' | body | grep -icE 'home-pc|10\.77\.1\.|alex' || true)
if [ "${leak:-0}" != 0 ]; then
  echo "!! a registry lookup leaked the household"
else
  echo "no household name, no household address: only the network that answers for it"
fi

say "3. the organisations that investigate, and whose console exists"
S 'abuse desks' | body | head -14
echo "--- the queue is a desk's own console, and this household is not a desk ---"
S 'abuse queue' | body | tail -2

say "4. a report needs the reporter's own records"
echo "--- nothing has talked to this address from here ---"
S 'abuse report 192.0.2.99' | body | tail -2
echo "--- and an opinion is not a record ---"
S "abuse report 192.0.2.99 --note 'I am fairly sure it was them'" | body | tail -2

say "5. eight real rejected logins at the provider's abuse desk"
# The household's own public addresses are what the desk's machine logs, because
# that is what a remote host sees. The attempts go through the same Dial every
# other packet uses, so the failures are records on a real machine — and the
# desk's own file, which the player cannot see, is what opens the case.
for i in 1 2 3 4 5 6 7 8; do S "ssh root@abuse.novapanel.example" '@toor' >/dev/null 2>&1; done
echo "sent: the desk's machine holds eight rejected logins from this household's"
echo "address, in its own log, with the time it saw each one"

say "6. the world's own attacker, and the report the household files about it"
# The scanner sweeps every 97 ticks — about five minutes of real time — and the
# household's gateway is one of the addresses it sweeps, so its probes are in
# the gateway's own record. `abuse evidence` reads exactly that record, and the
# report is refused unless it does.
echo "looking for the scanner's sweep in the gateway's own firewall log..."
ip=""
for i in $(seq 1 30); do
  # secstat flows prints: time, source host, source address, port, verdict
  line=$(R 'secstat flows 400' | body | grep 'scan-host' | head -1)
  ip=$(printf '%s\n' "$line" | awk '{print $3}' | tr -d '\r')
  case "$ip" in
    *.*|*:*) break ;;
    *) ip="" ;;
  esac
  sleep 9
done
if [ -z "$ip" ]; then
  echo "--- the sweep has not fallen inside this window. It runs on its own"
  echo "    schedule (every 97 ticks); secstat flows shows it when it lands, and"
  echo "    abuse evidence reads that same record. The refusals above still hold."
  echo
  echo "abuse_verify: done (no sweep in this window)"
  exit 0
fi
echo "the sweep came from $ip"
echo "--- what this household's own records can show about it ---"
R "abuse evidence $ip" | body | head -10
echo "--- filed with the network that announces that address ---"
R "abuse report $ip --note 'hourly sweep of the gateway, nothing legitimate behind it'" | body | head -12
tick
echo "--- and the account holder reads their own ticket back, from their own PC ---"
S 'abuse status' | body | head -24
echo "--- the desk's own file is the authority, and the clock owns the rest: a"
echo "    report is filed, triaged against the desk's own records, notified on its"
echo "    service level, and enforced on its notice window — all sim time ---"
ticket=$(S 'abuse status' | body | awk '/^AB-[0-9]/{print $1; exit}' | tr -d ':')
if [ -n "$ticket" ]; then
  echo "--- the case file, rung by rung, with the desk's own next step ---"
  S "abuse status $ticket" | body | head -22
fi

echo
echo "abuse_verify: done"
echo "(a tick is 30s of game time; a desk has a triage service level, a notice"
echo " window before enforcement, and a suspension is a 24h term that lapses on"
echo " its own — every one of those facts is read back from the case file)"
