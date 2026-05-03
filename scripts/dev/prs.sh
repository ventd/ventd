#!/usr/bin/env bash
# prs.sh — one-line summary of every open PR you authored.
#
# Use:
#   bash scripts/dev/prs.sh               # current user
#   bash scripts/dev/prs.sh phoenixdnb    # named author
#
# Output (one PR per line, fixed-width columns):
#   #NUMBER  STATE       FAILS  PEND  TITLE
#
# STATE is gh's mergeStateStatus (CLEAN / BLOCKED / BEHIND / DIRTY / UNKNOWN).
# FAILS counts CI checks that conclude FAILURE / CANCELLED.
# PEND counts checks not yet COMPLETED.

set -euo pipefail
who="${1:-@me}"

gh pr list \
  --author "$who" \
  --state open \
  --json number,title,mergeStateStatus,statusCheckRollup \
  --jq '
    .[] | {
      n: .number,
      s: .mergeStateStatus,
      f: ([.statusCheckRollup[]? | select(.conclusion=="FAILURE" or .conclusion=="CANCELLED")] | length),
      p: ([.statusCheckRollup[]? | select(.status!="COMPLETED")] | length),
      t: .title
    } | "#\(.n)  \(.s | tostring | (. + "          ")[:10])  \(.f)     \(.p)     \(.t)"
  '
