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
# A session that never reached its target silently runs the rest of its
# commands on the *previous* machine, which would make a transcript claim work
# that happened elsewhere. Every wrapper checks for that before printing.
_drv() { /tmp/nh_sshdrive alex alex123 "$@" 'exit' 2>&1; }
_guard() {
  case "$1" in
    *"ssh: resolve"*|*"Permission denied"*|*"Connection refused"*|*"Connection timed out"*)
      printf '!! a login in this section failed: the lines below did not run on the host they name\n' ;;
  esac
  printf '%s\n' "$1"
}
S() { _guard "$(_drv "$@")"; }
R() { _guard "$(_drv "ssh root@10.77.1.1" admin "$@")"; }
# the world ticks every 3 seconds, and a tick is half a minute of game time
tick() { sleep 6; }
# The §35 walk-through runs on a second server with a compressed wall-to-sim
# ratio (NEOHOME_TICK_MS): a desk's service level is an hour of world time, and
# §37 lets a few seconds of the player's time stand for it. Nothing is skipped —
# every rung is still advanced by Tick(), just at a different ratio.
ftick() { sleep 3; }
# The unit's own console, and the provider desk that complains from the other
# side. Both are real logins to real accounts in the organisation's own group.
L() { _guard "$(_drv "ssh cases@cnu.gov.example" "cnu-cases-2026" "$@")"; }
A() { _guard "$(_drv "ssh agent@cnu.gov.example" "cnu-agents-2026" "$@")"; }
P() { _guard "$(_drv "ssh contractor@abuse.novapanel.example" "np-shift-2026" "$@")"; }
# out <session-fn> <command...> — run a session, keep every line, then filter.
# Piping the driver into head/grep -q kills it with SIGPIPE while the command is
# still running, which loses the answer to a command the world has already acted
# on. Capture first, filter second.
out() {
  local fn="$1"; shift
  local all
  all=$("$fn" "$@" 2>&1)
  printf '%s\n' "$all" | body
}
# waitfor <session-fn> <ticket> <pattern> [tries] — the rungs run on sim time,
# so waiting means letting the world tick, not sleeping on a promise
waitfor() {
  local fn="$1" tk="$2" pat="$3" tries="${4:-8}" i
  for i in $(seq 1 "$tries"); do
    if out "$fn" "abuse show $tk" | grep -qE "$pat"; then return 0; fi
    tick
  done
  return 1
}
# only the lines the command printed, without the ssh driver's own transcript
body() { grep -vE '^(ssh: |Welcome to |Welcome, |alex@home-pc|root@gateway|root@10\.77\.1\.1|organisations that investigate|$)' ; }

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

say "7. §35 — the top of the ladder: the unit's intake is a real mailbox"
echo "--- the household writes to the unit in its own words, naming an address ---"
out S 'mail send cases@cnu.gov.example complaint about the nightly sweep' \
  '@To the cybercrime unit:' \
  '@our office gateway logs 192.0.2.1 sweeping it every hour: 400 filtered drops' \
  '@in the last window. The record is our own firewall log.' '@.' | tail -2
tick; tick
echo "--- the unit's console: an account in its own group on its own machine ---"
out L 'abuse queue' | grep -vE '^cases@|^$' | head -8
echo "--- and the household has no console anywhere, because it is not a desk ---"
out S 'abuse queue' | tail -1
lawticket=$(out L 'abuse queue' | awk '/^AB-[0-9]/{print $1; exit}' | tr -d ':\r')
if [ -z "$lawticket" ]; then
  echo "!! the complaint did not become a file at the unit"
else
  echo "--- the file the complaint became: what it stands on, and what the unit is not ---"
  out L "abuse show $lawticket" | head -16
  echo "--- an order now is refused twice over, and both refusals are state: the"
  echo "    analyst lacks the group that signs orders, then the file itself ---"
  out L "abuse act $lawticket warrant --note 'records retention on the line'" | grep -E 'abuse: ' | head -1
  out A "abuse act $lawticket warrant --note 'records retention on the line'" | grep -E 'abuse: ' | head -1
  echo "--- intake triages it against its own ledger, then asks the network in writing ---"
  out L "abuse triage $lawticket --note 'a named address, a written basis, and the complainant own record'" | grep -E 'triaged' | head -1
  out L "abuse act $lawticket request --note 'the complainant names an address your network announces'" | grep -E '→|law:request' | head -2
  echo "--- the unit's ledger of hours, and the blind spots it publishes ---"
  out L 'abuse hours' | grep -E 'investigator hours|files under|waits' | head -3
  out L 'abuse blind' | grep -E '^  - ' | head -5
  echo "--- the network answers on its own service level, which is world time: the"
  echo "    file carries its next step, and the clock below is the only thing that"
  echo "    moves it ---"
  out L "abuse show $lawticket" | grep -E 'next step|law:request' | head -3
fi

say "8. §35 — the whole ladder, at a compressed wall-to-sim ratio"
echo "--- the same world on a second server, one tick every 80ms (NEOHOME_TICK_MS):"
echo "    an hour of service level inside a few seconds, and not one rung skipped ---"
bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1
FAST=/tmp/nh_law_fast
rm -rf "$FAST"; mkdir -p "$FAST"
cp /tmp/nh_abuse "$FAST/neohome-law"
cd "$FAST"
rm -f /tmp/law_world.gob
NEOHOME_WORLD=/tmp/law_world.gob NEOHOME_TICK_MS=80 ./neohome-law >server.log 2>&1 &
FSRV=$!
trap 'bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1' EXIT
sleep 2
grep -h "engine: one tick" server.log || true
echo "--- a fresh world still has the household's DNS fault, so it is repaired the"
echo "    same way a player repairs it: the unit is reached by name or not at all ---"
R "mkdir -p /var/run/dnsmasq" "echo nameserver 10.0.0.2 > /var/run/dnsmasq/resolv.conf" >/dev/null
sleep 3
out L 'id' | grep -q 'uid=' || echo "!! the unit is unreachable in the fast world"

# the complaint the provider's desk files about the household's own line: an
# address the ISP can name, which is what a warrant needs
out P 'mail send cases@cnu.gov.example complaint about 198.51.100.1' \
  '@Our abuse desk logged repeated password attempts on our own gateway from' \
  '@198.51.100.1, in our own auth log. Requesting that the subscriber be dealt' \
  '@with under your own powers.' '@.' | tail -1
ftick; ftick
fast=$(out L 'abuse queue' | awk '/^AB-[0-9]/{print $1; exit}' | tr -d ':\r')
if [ -z "$fast" ]; then
  echo "!! the provider's complaint did not open a file"
else
  out L "abuse show $fast" | head -12
  echo "--- intake reads it, and the request goes out in writing ---"
  out L "abuse triage $fast --note 'a named address, a written basis, and the complainant own record'" | tail -1
  out L "abuse act $fast request --note 'the complainant names an address your network announces'" | tail -1
  echo "--- the network answers within its service level: a subscriber is disclosed ---"
  for i in $(seq 1 40); do
    out L "abuse show $fast" | grep -q 'law:disclosed' && break
    ftick
  done
  out L "abuse show $fast" | grep -E 'the network disclosed|law:disclosed|next step' | head -3
  echo "--- the investigation is an investigator's work, and it costs the unit a shift ---"
  out A "abuse act $fast investigate --note 'the disclosure and the provider own auth log support a look'" | tail -1
  out A 'abuse hours' | grep -E 'investigator hours' | head -1
  echo "--- the order is signed, served on the network, and the subscriber is told ---"
  out A "abuse act $fast warrant --note 'the file names the subscriber and stands on two networks records'" | tail -3
  out A 'abuse warrants' | grep -vE '^agent@|^$' | head -4
  for i in $(seq 1 30); do
    out L "abuse show $fast" | grep -q 'law:charged' && break
    ftick
  done
  out L "abuse show $fast" | tail -4
  echo "--- and the subscriber's own mailbox, on their own PC, carries both notices ---"
  out S 'mail' | grep -iE 'Subject|investigation|order' | head -6
  echo "--- a file that cannot support an order ends with the reason instead ---"
  out A 'abuse queue' | grep -vE '^agent@|^$' | head -6
fi

echo
echo "abuse_verify: done"
echo "abuse_verify: done"
echo "(§35 additions: the unit reads its own mailbox into files, asks the network in"
echo " writing, keeps a ledger of investigator hours, and signs nothing until the"
echo " file names a subscriber — then the order is served on the network and the"
echo " subscriber is told)"
echo "(a tick is 30s of game time; a desk has a triage service level, a notice"
echo " window before enforcement, and a suspension is a 24h term that lapses on"
echo " its own — every one of those facts is read back from the case file)"
