#!/usr/bin/env bash
# scripts/verify-local.sh — single-shot local PR/branch verification.
#
# Replaces the multi-line copy-paste verification block that gets pasted
# into terminals after every CC session. Always runs from the repo root,
# always uses --no-pager for git, always emits parseable output.
#
# Usage:
#   scripts/verify-local.sh                    # current branch vs main
#   scripts/verify-local.sh --against develop  # current branch vs develop
#   scripts/verify-local.sh --skip-tests       # skip go test (faster)
#   scripts/verify-local.sh --paths 'TESTING.md|deploy/apparmor.d'
#                                              # extra grep-c sanity checks

set -euo pipefail

# ---- always run from repo root --------------------------------------------

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || echo "")
if [ -z "$REPO_ROOT" ]; then
  echo "ERROR: not in a git repo. cd into your project first." >&2
  exit 1
fi
cd "$REPO_ROOT"

# ---- arg parsing ----------------------------------------------------------

AGAINST="main"
SKIP_TESTS=0
EXTRA_PATHS=""

while [ $# -gt 0 ]; do
  case "$1" in
    --against)     AGAINST="$2"; shift 2 ;;
    --skip-tests)  SKIP_TESTS=1; shift ;;
    --paths)       EXTRA_PATHS="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,/^set -euo/p' "$0" | sed 's/^# \?//;/^set -euo/d' | head -20
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

BRANCH=$(git branch --show-current)

echo "============================================================"
echo "  VERIFY: $BRANCH (against $AGAINST)"
echo "  Repo:   $REPO_ROOT"
echo "============================================================"

# ---- 1. tree state --------------------------------------------------------

echo ""
echo "===== git status ====="
git --no-pager status --short --branch

# ---- 2. commits on this branch -------------------------------------------

echo ""
echo "===== commits on $BRANCH (since $AGAINST) ====="
COMMIT_COUNT=$(git rev-list --count "$AGAINST..HEAD" 2>/dev/null || echo "?")
echo "count: $COMMIT_COUNT"
git --no-pager log --oneline "$AGAINST..HEAD" 2>/dev/null | head -20 \
  || echo "(branch $AGAINST not found locally)"

# ---- 3. go tests ---------------------------------------------------------

if [ "$SKIP_TESTS" = "0" ]; then
  echo ""
  echo "===== go test -race ====="
  if go test -race ./... 2>&1 | tail -10; then
    :
  else
    echo "  (test failures above)"
  fi

  echo ""
  echo "===== rulelint ====="
  if [ -d tools/rulelint ]; then
    go run ./tools/rulelint 2>&1 | tail -5
  else
    echo "(no tools/rulelint in this repo)"
  fi
fi

# ---- 4. extra path sanity checks -----------------------------------------

if [ -n "$EXTRA_PATHS" ]; then
  echo ""
  echo "===== extra path checks ====="
  echo "pattern: $EXTRA_PATHS"
  echo ""
  IFS='|' read -ra PATHS_ARR <<< "$EXTRA_PATHS"
  for p in "${PATHS_ARR[@]}"; do
    echo "  path: $p"
    if [ -e "$p" ]; then
      echo "    EXISTS"
      if [ -f "$p" ]; then
        echo "    size: $(wc -l < "$p") lines"
      fi
    else
      echo "    MISSING"
    fi
  done
fi

# ---- 5. drift detection — common stale-reference check -------------------

echo ""
echo "===== drift check ====="
DRIFT_DIRS=".github scripts deploy"
DRIFT_FOUND=0

for d in $DRIFT_DIRS; do
  [ -d "$d" ] || continue
  RECENT_RENAMES=$(git --no-pager log -10 --diff-filter=R --name-status \
    "$AGAINST..HEAD" 2>/dev/null | grep -E '^R' | awk '{print $2}' \
    | xargs -n1 basename 2>/dev/null | sort -u || echo "")
  if [ -n "$RECENT_RENAMES" ]; then
    while IFS= read -r oldname; do
      [ -z "$oldname" ] && continue
      MATCHES=$(grep -rln "$oldname" "$d" 2>/dev/null || true)
      if [ -n "$MATCHES" ]; then
        echo "  WARN: '$oldname' (renamed) still referenced in:"
        echo "$MATCHES" | sed 's/^/    /'
        DRIFT_FOUND=1
      fi
    done <<< "$RECENT_RENAMES"
  fi
done

if [ "$DRIFT_FOUND" = "0" ]; then
  echo "  no stale-rename references detected in $DRIFT_DIRS"
fi

# ---- 6. summary -----------------------------------------------------------

echo ""
echo "============================================================"
echo "  SUMMARY"
echo "============================================================"
if git diff --quiet && git diff --cached --quiet; then
  echo "  tree:    clean"
else
  echo "  tree:    DIRTY (uncommitted changes)"
fi
echo "  commits: $COMMIT_COUNT ahead of $AGAINST"
if [ "$DRIFT_FOUND" = "1" ]; then
  echo "  drift:   STALE REFERENCES FOUND (see drift check above)"
else
  echo "  drift:   none detected"
fi
echo ""
echo "  If summary is clean, ready to push:"
echo "    git push -u origin $BRANCH"
echo "============================================================"
