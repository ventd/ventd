#!/usr/bin/env bash
# wait-and-merge.sh — auto-merge a PR once CI greens.
#
# Use:
#   bash scripts/dev/wait-and-merge.sh 861
#
# Repeatedly polls `gh pr view` every 60 s until either:
#   - mergeStateStatus is CLEAN (then squash-merge with --delete-branch)
#   - any check fails (then exit 1, do NOT merge)
#
# Equivalent to GitHub's --auto flag, but visible from a terminal so
# you can watch for failures without leaving the shell.

set -euo pipefail
pr="${1:?usage: $0 <PR_NUMBER>}"

while true; do
  state=$(gh pr view "$pr" --json mergeStateStatus,statusCheckRollup --jq '
    {
      m: .mergeStateStatus,
      f: ([.statusCheckRollup[]? | select(.conclusion=="FAILURE" or .conclusion=="CANCELLED")] | length),
      p: ([.statusCheckRollup[]? | select(.status!="COMPLETED")] | length)
    } | "\(.m) f=\(.f) p=\(.p)"
  ')
  echo "[$(date -u +%H:%M:%S)] PR #$pr: $state"
  case "$state" in
    "CLEAN "*)
      echo "CI green — merging."
      gh pr merge "$pr" --squash --delete-branch
      exit 0
      ;;
  esac
  if echo "$state" | awk -F' f=' '{print $2}' | awk -F' ' '{exit ($1==0)?1:0}'; then
    echo "FAILURE detected — refusing to merge."
    exit 1
  fi
  sleep 60
done
