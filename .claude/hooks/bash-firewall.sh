#!/usr/bin/env bash
cmd=$(jq -r '.tool_input.command // ""')
deny=(
  'rm[[:space:]]+-rf[[:space:]]+/'
  'rm[[:space:]]+-rf[[:space:]]+~'
  'git[[:space:]]+push.*--force([[:space:]]|$)'
  'git[[:space:]]+reset[[:space:]]+--hard[[:space:]]+origin'
  'curl.*\|[[:space:]]*(ba)?sh'
  'wget.*\|[[:space:]]*(ba)?sh'
  '--no-verify'
  'sudo[[:space:]]+rm'
  'dd[[:space:]]+if=.*of=/dev/'
  ':\(\)[[:space:]]*\{.*\};[[:space:]]*:'
)
for p in "${deny[@]}"; do
  if echo "$cmd" | grep -Eq "$p"; then
    echo "BLOCKED by bash-firewall: pattern '$p'" >&2
    exit 2
  fi
done
exit 0
