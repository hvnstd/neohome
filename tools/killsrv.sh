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

# kill stale neohome servers without matching this script's own command line
for pid in $(ps -eo pid,comm | awk '$2 ~ /^nh_|^neohome/ {print $1}'); do
  kill "$pid" 2>/dev/null && echo "killed $pid"
done
sleep 0.5
ps -eo pid,comm | awk '$2 ~ /^nh_|^neohome/ {print "STILL:", $0}'
echo "(done)"
