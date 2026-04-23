#!/usr/bin/env bash
jq -r '.tool_response.stdout // ""' | \
  awk '
    /--- FAIL|--- ERROR|^FAIL|panic:|goroutine [0-9]+ \[/ { p=1 }
    /^PASS|^ok[ \t]/ { pass++; next }
    p
    END { printf "\n=== %d packages passed ===\n", pass }
  '
