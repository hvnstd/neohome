#!/usr/bin/env bash
# ftp_verify.sh — FTP end to end on a live world, found the way a player finds
# it: repair the seeded DNS fault, look up your own public range, scan it, and
# recon the address that answers on 21. Then an anonymous session that reads the
# neighbour's files, the credential export coming back over the wire, the
# account it opens — and finally the world reacting to a loud attack.
#
# Unlike the older scripts this one locates the checkout itself and picks the Go
# toolchain that exists (the workspace layout, or a local one), so it runs from
# any clone instead of only from /workspace/neohome.
set -u

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -d /workspace/go/bin ]; then
  export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache
elif [ -x "$HOME/.local/tools/go/bin/go" ]; then
  export PATH="$HOME/.local/tools/go/bin:$PATH" GOPATH="$HOME/.local/gopath"
fi

rm -f /tmp/ftp_world.gob
go build -o /tmp/neohome-ftp ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
bash tools/killsrv.sh >/dev/null 2>&1
NEOHOME_WORLD=/tmp/ftp_world.gob /tmp/neohome-ftp >/tmp/ftp_server.log 2>&1 &
SRV=$!
trap 'bash tools/killsrv.sh >/dev/null 2>&1' EXIT
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat /tmp/ftp_server.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
S() { go run ./tools/sshdrive/main.go alex alex123 "$@" 2>&1; }
# the household router prompts for a password, so "admin" is fed as the next line
R() { go run ./tools/sshdrive/main.go alex alex123 "ssh root@10.77.1.1" admin "$@" 2>&1; }

say "1. the seeded DNS fault is active: names do not resolve"
S 'dig home.alex.neohome.example' | tail -3
echo "--- the SERVFAIL above is the scripted story, not an FTP bug ---"

say "2. repair the router exactly like a player would"
R 'mkdir -p /var/run/dnsmasq' \
  'echo nameserver 10.0.0.2 > /var/run/dnsmasq/resolv.conf' \
  'cat /var/run/dnsmasq/resolv.conf' \
  'exit' | tail -4

say "3. your own WAN address and the range the registry attributes it to"
WAN=$(S 'dig home.alex.neohome.example' | grep -oE '([0-9]{1,3}\.){3}[0-9]{1,3}' | head -1)
echo "own WAN address: ${WAN:-?}"
S "whois ${WAN:-home.alex.neohome.example}" | sed -n '1,9p'
RANGE=$(S "whois ${WAN:-home.alex.neohome.example}" | awk '/^range:/{print $2}')
echo "range: ${RANGE:-?}"

say "4. scan the range: one address answers on 21 with an FTP banner"
S "scan ${RANGE:-198.51.100.0/24}" | tail -9
NPCIP=$(S "scan ${RANGE:-198.51.100.0/24}" \
  | awk '/Nmap scan report for/{line=$0} /21\/tcp +open/{print line}' \
  | grep -oE '\(([0-9.]+)\)' | tr -d '()' | head -1)
echo "FTP address: ${NPCIP:-none}"
if [ -z "$NPCIP" ]; then echo "FAIL: nothing answered on 21 in that range"; exit 1; fi

say "5. recon the address: the router forwards 21 to the machine behind it"
S "recon $NPCIP" | tail -12

say "6. an anonymous session: the daemon answers, the drop is real, a file comes back"
S "ftp -A $NPCIP" "pwd" "ls /srv/ftp/pub" "get /home/devops/deploy/notes.md" "bye" | tail -12
S 'cat /home/alex/notes.md' | tail -4

say "7. the credential export over the same anonymous session, verified by a real login"
S "exploit $NPCIP ftp-cred-file" | tail -12

say "8. the account the export names, used through the daemon"
S "ftp mara@$NPCIP" "hunter2" "ls /home/mara/Desktop" "get /home/mara/Desktop/passwords.kdbx" "bye" | tail -9

say "9. the trail so far — every session above is evidence on the target"
S 'evidence' | head -14

say "10. now be loud: the anonymous write is an alert-level event"
S "exploit $NPCIP vsftpd-anon-upload" | tail -8

say "11. the owner reacts: her router stops forwarding 21, and the drop is gone"
S "ftp -A $NPCIP" | tail -3
echo "--- the filtered connection above is the world defending itself ---"
S "recon $NPCIP" | tail -8
S 'evidence' | head -8

kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
echo
echo "--- torn down ---"
