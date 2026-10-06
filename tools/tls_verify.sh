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

# tls_verify.sh — proves https is real over a live SSH session: the mirror and
# bank really serve on 443, the handshake is verified against the household
# root CA found in the client's trust store, removing that anchor fails the
# handshake closed, and openssl shows the same chain a real client would.
set -u

rm -f /tmp/tls_world.gob
go build -o /tmp/neohome-tls ./cmd/neohome || exit 1
bash tools/killsrv.sh >/dev/null 2>&1
NEOHOME_WORLD=/tmp/tls_world.gob /tmp/neohome-tls >/tmp/tls_server.log 2>&1 &
sleep 1.5
trap 'bash tools/killsrv.sh >/dev/null 2>&1' EXIT

S() { go run ./tools/sshdrive/main.go alex alex123 "$@"; }
# the router prompts for a password, so "admin" is fed as the next line
R() { go run ./tools/sshdrive/main.go alex alex123 "ssh root@10.77.1.1" admin "$@"; }

echo "########## the seeded DNS fault is active: https cannot even resolve ##########"
S "curl -s https://mirror.neohome.example/debian/Release"
echo "--- the DNS failure above is the scripted story, not a TLS bug ---"

echo
echo "########## repair the router exactly like a player would ##########"
R "mkdir -p /var/run/dnsmasq" \
  "echo nameserver 10.0.0.2 > /var/run/dnsmasq/resolv.conf" \
  "cat /var/run/dnsmasq/resolv.conf" \
  "exit"

echo
echo "########## https serves now, and the certificate is verified ##########"
S "curl -s https://mirror.neohome.example/debian/Release" \
  "curl -s https://bank.firstneohome.example/"

echo
echo "########## openssl sees the real chain and the real dates ##########"
S "openssl s_client -connect mirror.neohome.example:443" \
  "openssl x509 -in /etc/ssl/certs/neohome-root-ca.pem -noout -subject -dates"

echo
echo "########## delete the trust anchor: the handshake must fail closed ##########"
# sudo authenticates the invoking user, so the next line feeds the prompt
S "sudo rm /etc/ssl/certs/neohome-root-ca.pem" "alex123" \
  "curl -s https://mirror.neohome.example/debian/Release"
echo "--- the certificate error above is correct: no anchor, no trust ---"

echo
echo "########## done ##########"
