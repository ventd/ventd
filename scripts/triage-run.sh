#!/usr/bin/env bash
# scripts/triage-run.sh — single-shot diagnosis of a failed GitHub Actions run.
#
# Replaces the iterative `gh run view` / `gh run view --log-failed` /
# `gh run view --json jobs` loop with one command that produces all the
# context Phoenix and Claude need to diagnose a failure.
#
# Usage:
#   scripts/triage-run.sh                # auto-pick latest failed run on current branch
#   scripts/triage-run.sh <run-id>       # specific run
#   scripts/triage-run.sh --pr <num>     # latest failed run on a PR's head branch
#   scripts/triage-run.sh --tag <tag>    # latest failed run for a tag (release pipelines)
#
# Output sections (in order):
#   1. Run metadata (workflow, branch, event, status, conclusion, URL)
#   2. Job-state matrix (every job with its conclusion)
#   3. Per-failed-job: error-line greps + last 40 lines of failed-step log
#   4. Release state (if tag-push event) — assets count, draft/prerelease flags
#   5. Hint block: pattern-matched suggestions for known failure shapes
#
# The output is intended to be pasted whole into a Claude.ai chat for
# triage. It is bounded — one run = one paste, no iterative log-fishing.

set -euo pipefail

# ---- arg parsing -----------------------------------------------------------

RUN_ID=""
PR_NUM=""
TAG=""

while [ $# -gt 0 ]; do
  case "$1" in
    --pr)  PR_NUM="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,/^set -euo/p' "$0" | sed 's/^# \?//;/^set -euo/d' | head -30
      exit 0
      ;;
    *) RUN_ID="$1"; shift ;;
  esac
done

# ---- resolve which run we're triaging --------------------------------------

if [ -z "$RUN_ID" ]; then
  if [ -n "$PR_NUM" ]; then
    BRANCH=$(gh pr view "$PR_NUM" --json headRefName --jq '.headRefName')
    RUN_ID=$(gh run list --branch "$BRANCH" --status failure --limit 1 \
              --json databaseId --jq '.[0].databaseId // empty')
  elif [ -n "$TAG" ]; then
    RUN_ID=$(gh run list --branch "$TAG" --status failure --limit 1 \
              --json databaseId --jq '.[0].databaseId // empty')
  else
    BRANCH=$(git branch --show-current 2>/dev/null || echo "")
    if [ -z "$BRANCH" ]; then
      echo "ERROR: not in a git repo and no run id given" >&2
      exit 1
    fi
    RUN_ID=$(gh run list --branch "$BRANCH" --status failure --limit 1 \
              --json databaseId --jq '.[0].databaseId // empty')
  fi
fi

if [ -z "$RUN_ID" ]; then
  echo "ERROR: no failed run found. Try: gh run list --limit 10" >&2
  exit 1
fi

# ---- 1. run metadata -------------------------------------------------------

echo "============================================================"
echo "  TRIAGE: run $RUN_ID"
echo "============================================================"
gh run view "$RUN_ID" \
  --json status,conclusion,headBranch,event,workflowName,url,createdAt \
  --jq '"workflow:    \(.workflowName)
branch:      \(.headBranch)
event:       \(.event)
status:      \(.status)
conclusion:  \(.conclusion // "n/a")
created:     \(.createdAt)
url:         \(.url)"'

# ---- 2. job-state matrix ---------------------------------------------------

echo ""
echo "===== JOB STATE MATRIX ====="
gh run view "$RUN_ID" --json jobs --jq '
  .jobs[]
  | "\(.conclusion // .status // "?")\t\(.name)"
' | column -t -s "$(printf '\t')"

# ---- 3. per-failed-job logs -----------------------------------------------

FAILED_JOB_IDS=$(gh run view "$RUN_ID" --json jobs \
  --jq '.jobs[] | select(.conclusion=="failure") | .databaseId')

LOG=""

if [ -z "$FAILED_JOB_IDS" ]; then
  echo ""
  echo "===== NO FAILED JOBS ====="
  echo "Run conclusion is failure but no individual job conclusion=failure."
  echo "This is usually a workflow-level issue (cancelled, timed out, or"
  echo "an aggregator job inheriting a sibling failure). Check the URL above."
else
  for JOB_ID in $FAILED_JOB_IDS; do
    JOB_NAME=$(gh run view "$RUN_ID" --json jobs \
      --jq ".jobs[] | select(.databaseId==$JOB_ID) | .name")
    echo ""
    echo "===== FAILED JOB: $JOB_NAME ====="
    echo "  job id: $JOB_ID"

    # Pull the failed-step log once, grep + tail it without re-fetching.
    LOG=$(gh run view --job="$JOB_ID" --log-failed 2>/dev/null || echo "")

    if [ -z "$LOG" ]; then
      echo "  (no failed-step log available — job may have failed during setup)"
      continue
    fi

    echo ""
    echo "  --- error/FAIL lines ---"
    echo "$LOG" | grep -iE 'error[: ]|##\[error\]|FAIL[: ]|exit code|fatal:|panic:' \
      | sed 's/^/    /' | tail -25 || echo "    (no error lines matched)"

    echo ""
    echo "  --- last 40 lines of failed-step log ---"
    echo "$LOG" | tail -40 | sed 's/^/    /'
  done
fi

# ---- 4. release state (tag pushes) ----------------------------------------

REF=$(gh run view "$RUN_ID" --json headBranch --jq '.headBranch')
if [[ "$REF" =~ ^v[0-9]+\.[0-9]+ ]]; then
  echo ""
  echo "===== RELEASE STATE ($REF) ====="
  if gh release view "$REF" --json isDraft,isPrerelease,assets >/tmp/_rel.json 2>/dev/null; then
    jq -r '"draft:        \(.isDraft)
prerelease:   \(.isPrerelease)
asset_count:  \(.assets | length)"' /tmp/_rel.json
    echo ""
    echo "  --- asset names ---"
    jq -r '.assets[].name' /tmp/_rel.json | sed 's/^/    /'
  else
    echo "no release exists for tag $REF"
  fi
  rm -f /tmp/_rel.json
fi

# ---- 5. hint block --------------------------------------------------------

echo ""
echo "===== HINTS ====="

# Hint 1: aggregator-only failure (everything succeeded except final)
TOTAL_FAILED=$(echo "$FAILED_JOB_IDS" | grep -c . || echo 0)
if [ "$TOTAL_FAILED" = "1" ]; then
  ONLY_FAILED=$(gh run view "$RUN_ID" --json jobs \
    --jq '.jobs[] | select(.conclusion=="failure") | .name')
  if echo "$ONLY_FAILED" | grep -qE 'final|outcome|aggregat'; then
    echo "- Only an aggregator/outcome job failed. Check release state above:"
    echo "  if all assets present, this is likely COSMETIC. Don't reroll the tag."
  fi
fi

# Hint 2: stale path references (matches the spec-06 PR 2 + post-tag class)
if [ -n "$LOG" ] && echo "$LOG" | grep -q "not found, skipping"; then
  echo "- 'not found, skipping' in log: workflow references a path that"
  echo "  doesn't exist. Likely stale path after a git mv. Run:"
  echo "    grep -rn '<old-path>' .github/ scripts/ deploy/"
fi

# Hint 3: SBOM / spec version drift
if [ -n "$LOG" ] && echo "$LOG" | grep -qE "specVersion='1\.[0-9]'"; then
  echo "- SBOM specVersion mismatch detected. Generator likely auto-bumped"
  echo "  (e.g. CycloneDX 1.5->1.6). Update the validator in release.yml to"
  echo "  accept the new version."
fi

# Hint 4: permissions
if [ -n "$LOG" ] && echo "$LOG" | grep -qE "Resource not accessible|403|permission"; then
  echo "- Permissions error in log. Check the workflow YAML's"
  echo "  'permissions:' block. SLSA jobs need contents:write + id-token:write."
fi

# Hint 5: directory mismatch
if [ -n "$LOG" ] && echo "$LOG" | grep -q "not a git repository"; then
  echo "- Log shows 'not a git repository'. Did the script cd into ~/ventd?"
fi

echo ""
echo "============================================================"
echo "  Paste the entire output above into your Claude.ai chat."
echo "============================================================"
