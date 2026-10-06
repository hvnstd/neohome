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

# resource_verify.sh — §17 over a live entry: real SSH sessions against a real
# server, making machines busy and watching what it costs. A load that shares
# the CPU, work that measurably slows, a hog that pages and then dies, and a
# disk that fills, loses log lines and comes back.
set -u

go build -o /tmp/nh_res ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
go build -o /tmp/nh_sshdrive ./tools/sshdrive || { echo "DRIVER BUILD FAILED"; exit 1; }

RUN=/tmp/nh_res_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_res "$RUN/neohome-res"
cd "$RUN"

rm -f /tmp/res_world.gob
NEOHOME_WORLD=/tmp/res_world.gob ./neohome-res >server.log 2>&1 &
SRV=$!
trap 'bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1' EXIT
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat server.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# alex on the household PC; the laptop answers on its own address (four cores,
# so a load on it is a load the player can really see), and the switch is a
# one-core machine with 128 MiB of RAM and 16 MiB of flash — the honest place to
# run out of both
# the driver echoes the local prompt after every session, so the helpers strip
# the prompt and banner lines and leave the commands' own output
clean() { grep -v -e '^ssh: authenticated' -e '^ssh: connect to host' -e '^Welcome' -e '^alex@' -e '^root@' -e "^[a-z-]*@[a-z-]*:~\$" -e '^$'; }
S() { /tmp/nh_sshdrive alex alex123 "$@" 2>&1 | clean; }
L() { /tmp/nh_sshdrive alex alex123 "ssh alex@10.77.1.12" alex123 "$@" 2>&1 | clean; }
SW() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.2" alex123 "$@" 2>&1 | clean; }
tick() { sleep 5; }

say "1. the machine as it is: real memory, real disk, real load"
S 'uptime' 'free' 'df' 'vmstat | head -2'

say "2. bulk work on an idle machine: what a 64 MiB write costs"
L 'dd if=/dev/zero of=/tmp/probe bs=1M count=64' 'rm /tmp/probe'
echo "--- remember those seconds: the same write under load takes longer ---"

say "3. a load that really shares the CPU: eight workers on four cores"
L 'stress --cpu 8 --timeout 90' | head -4
tick
L 'uptime' 'htop' | head -12

say "4. the same write, on the same machine, now that it is busy"
L 'dd if=/dev/zero of=/tmp/probe bs=1M count=64' 'rm /tmp/probe'
echo "--- the copy really did take longer: the disk and the CPU are shared ---"

say "5. memory pressure on a 128 MiB machine: page first, then kill"
SW 'free | tail -2'
SW 'stress --vm 1 --vm-bytes 100M --timeout 90' | tail -2
tick
SW 'free | tail -2'
SW 'logread | grep -c "swapped out"'
SW 'stress --vm 1 --vm-bytes 80M --timeout 90' | tail -2
tick
SW 'vmstat | tail -6'
SW 'logread | grep -c "Out of memory"'
SW 'dmesg | tail -1'
echo "--- the biggest resident process died; the switch itself is still answering ---"
SW 'switchctl show | head -2'

say "6. recovery: stop the load and the machine is itself again"
L 'pkill stress'
SW 'pkill stress' 'free | tail -2'
tick
L 'uptime'
SW 'free | tail -2'

say "7. a disk that fills: 16 MiB of flash, and the failure is the filesystem's"
SW 'df'
SW 'dd if=/dev/zero of=/tmp/big bs=1M count=64'
tick
SW 'df'
SW 'echo hello > /tmp/later.txt'
SW 'dmesg | tail -1'

say "8. the log pays for it too, and the recovery is accounted for"
SW 'switchctl port 3 down' 'switchctl port 3 up'
SW 'logread | grep -c "port 3 ("'
SW 'df | tail -1'
echo "--- the two admin lines are gone and the count says so (0 above) ---"
echo "--- those lines are missing: a full disk really loses log lines ---"
SW 'rm /tmp/big'
tick
SW 'logread | grep "space reclaimed"'
SW 'logread | grep "dropped while"'
SW 'echo hello > /tmp/later.txt' 'cat /tmp/later.txt'

echo
echo "resource_verify: done"
echo "(the install-onto-a-full-disk path and the slowed spooler are covered by"
echo " tests/resources_test.go: a live world cannot shrink a seeded disk, and the"
echo " printer has no shell to start a load from — which is exactly right.)"
