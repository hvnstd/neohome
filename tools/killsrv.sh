#!/usr/bin/env bash
# kill stale neohome servers without matching this script's own command line
for pid in $(ps -eo pid,comm | awk '$2 ~ /^nh_|^neohome/ {print $1}'); do
  kill "$pid" 2>/dev/null && echo "killed $pid"
done
sleep 0.5
ps -eo pid,comm | awk '$2 ~ /^nh_|^neohome/ {print "STILL:", $0}'
echo "(done)"
