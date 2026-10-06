#!/usr/bin/env bash
# all_verify.sh — run every end-to-end verification and report honestly.
set -u
cd "$(dirname "$0")/.."
fail=0
for t in live_verify ssh_verify wan_verify live_full vm_verify power_verify dhcp_verify key_verify persistence_verify history_verify tmux_verify perm_verify tls_verify ftp_verify house_verify resource_verify security_verify; do
  bash tools/killsrv.sh >/dev/null 2>&1
  out=/tmp/verify_$t.out
  if bash tools/$t.sh >"$out" 2>&1; then
    status=ok
  else
    status=EXIT_NONZERO
    fail=1
  fi
  problems=$(grep -ciE 'command not found|panic|no such file or directory|invalid option' "$out" 2>/dev/null || true)
  # expected refusals are not defects: ssh_verify's wrong password is an
  # intentional refusal, and security_verify opens by showing that a fresh
  # machine really has no fail2ban-client and no suricata ("command not found"
  # twice, which is exactly the state §33 starts from)
  expected=0
  [ "$t" = ssh_verify ] && expected=0
  [ "$t" = security_verify ] && expected=2
  problems=$(( ${problems:-0} - expected ))
  [ "$problems" -lt 0 ] && problems=0
  printf "%-22s exit=%-12s lines=%-5s suspicious=%s\n" "$t" "$status" "$(wc -l <"$out")" "$problems"
  grep -iE 'command not found|panic' "$out" | head -2 | sed 's/^/    /'
done
echo
echo "suspicious = lines matching expected-error patterns, minus the"
echo "deliberate refusals listed above (review any remaining count)"
exit $fail
