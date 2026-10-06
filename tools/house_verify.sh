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

# house_verify.sh — §15 over the live entry: a real SSH session against a real
# server, walking the household's physical layer. A printer that only works
# while its switch port is up, a camera that dies when PoE is switched off, a
# backup that fails when its NAS is unplugged, and a house that goes dark and
# comes back through the out-of-band controller.
set -u

go build -o /tmp/nh_house ./cmd/neohome || { echo "BUILD FAILED"; exit 1; }
go build -o /tmp/nh_sshdrive ./tools/sshdrive || { echo "DRIVER BUILD FAILED"; exit 1; }

RUN=/tmp/nh_house_run
rm -rf "$RUN"; mkdir -p "$RUN"
cp /tmp/nh_house "$RUN/neohome-house"
cd "$RUN"

rm -f /tmp/house_world.gob
NEOHOME_WORLD=/tmp/house_world.gob ./neohome-house >server.log 2>&1 &
SRV=$!
trap 'bash "$ROOT/tools/killsrv.sh" >/dev/null 2>&1' EXIT
sleep 2
kill -0 "$SRV" 2>/dev/null || { echo "SERVER DIED:"; cat server.log; exit 1; }

say() { printf '\n########## %s ##########\n' "$*"; }
# alex on the household PC, and the same player reaching the router
S() { /tmp/nh_sshdrive alex alex123 "$@" 2>&1; }
R() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.1" admin "$@" 2>&1; }
# a small pause is honest here: the world advances on its own ticks
tick() { sleep 6; }

say "1. the household's wiring, as the PC sees it"
S 'links' 'switchctl show' | tail -16

say "2. the printer is a device with paper, toner and a queue"
S 'lpstat' 'lp -t Notes /home/alex/notes.txt' | tail -6
sleep 9 # a page comes out every other tick, so this is one job, not one frame
S 'lpstat' | tail -4

say "3. port 6 is unplugged: the printer is powered but off the network"
S 'switchctl port 6 down' 'lp -t Notes /home/alex/notes.txt' | tail -6
echo "--- the failure above names the port, not a generic error ---"
S 'switchctl port 6 up' | tail -2
tick
S 'lpstat' | tail -3

say "4. PoE off on port 5: the camera loses power, the switch keeps working"
S 'switchctl poe 5 off' | tail -2
tick
S 'switchctl show' | sed -n '3,10p'
S 'ping cam-front' | tail -3

say "5. the NAS is unplugged, and the household loses what depends on it"
S 'backup run /home/alex' | tail -2
S 'power unplug nas' | tail -2
tick
S 'backup run /home/alex' | tail -2
echo "--- no NAS, no backup: the dependency is the failure ---"
S 'power plug nas' | tail -2
tick
S 'backup run /home/alex' | tail -2

# the out-of-band controller runs on its own battery and cellular backhaul, so
# it is the address that still answers when the house does not; the laptop has
# its own address because the household's DNS is the story of another chapter
B() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.250" admin "$@" 2>&1; }
L() { /tmp/nh_sshdrive alex alex123 "ssh alex@10.77.1.12" alex123 "$@" 2>&1; }
SW() { /tmp/nh_sshdrive alex alex123 "ssh root@10.77.1.2" alex123 "$@" 2>&1; }

say "6. the main breaker: the house goes dark, the controller does not"
S 'power cut' | tail -3
sleep 3
S 'hostname' 'whoami' | tail -6
echo "--- the entry above landed on the controller: a dark house is not a one-way door ---"
B 'bmc status' | tail -7
B 'power boot' | tail -4

say "7. with the house back, the laptop's lid is real state"
tick
L 'laptopctl status' | tail -6
S 'power lid closed laptop' | tail -2
echo "--- the lid closed: the machine suspends and its sessions end ---"
L 'hostname' | grep -m1 -E 'connect to|Connection' || echo "(no answer)"
S 'power lid open laptop' | tail -2
tick
L 'hostname' | tail -3

say "8. the household's own record: the queue, the backup index, and the switch's file"
S 'lpstat' | tail -4
S 'backup status' | tail -4
SW 'cat /etc/config/switch' | tail -12

echo
echo "house_verify: done"
