#!/usr/bin/env bash
# power_verify.sh — the household supply, over real SSH sessions.
#
# The point of this script is the middle section: the player cuts their own
# power, their session dies, and they must get back in through out-of-band
# management to bring the house up again. If that works, power is a mechanic
# rather than a message.
set -u
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOPATH=/workspace/gopath GOCACHE=/workspace/gocache

W=/tmp/power_world.gob
rm -f $W
echo "########## build ##########"
go build -o /tmp/neohome-power ./cmd/neohome || exit 1

bash tools/killsrv.sh >/dev/null 2>&1
NEOHOME_WORLD=$W /tmp/neohome-power >/tmp/power_server.log 2>&1 &
sleep 1.5
trap 'bash tools/killsrv.sh >/dev/null 2>&1' EXIT

S() { go run ./tools/sshdrive/main.go alex alex123 "$1"; }
# raw login that prints everything, to prove where a reconnect lands
L() { go run ./tools/sshdrive/main.go alex alex123 "$1" 2>&1; }

echo
echo "########## 1. the supply, and the controller that sits before the breaker ##########"
S "power status"
S "bmc"

echo
echo "########## 2. cut the power — the house goes dark ##########"
S "power cut"
echo
echo "--- the router must now be unreachable ---"
S "ping -c1 10.77.1.1"
echo "--- ssh to the router must fail ---"
S "ssh root@10.77.1.1"
echo "--- and the player's own shell is gone: reconnecting must land on the BMC ---"
L "hostname"

echo
echo "########## 3. bring the house back over out-of-band management ##########"
S "power boot"
echo
echo "--- the router answers again ---"
S "ping -c1 10.77.1.1"
echo "--- and we are back on our own machine ---"
L "hostname"

echo
echo "########## 4. a UPS is a real purchase with a real runtime ##########"
S "ups install"
S "ups"
S "balance"

echo
echo "########## 5. the controller's battery is finite too ##########"
S "bmc"

echo
echo "########## done ##########"
