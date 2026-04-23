#!/usr/bin/env bash
# Wrapper around tools/rulelint with structured, parseable output.
# Usage: bash .claude/skills/ventd-rulelint/scripts/run-rulelint.sh [--root DIR]
# Exit code: 0 = clean, 1 = errors found

set -euo pipefail

REPO_ROOT="${RULELINT_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# Allow --root override
while [[ $# -gt 0 ]]; do
    case "$1" in
        --root) REPO_ROOT="$2"; shift 2 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

OUTPUT=$(go run "$REPO_ROOT/tools/rulelint" -root "$REPO_ROOT" 2>&1) || STATUS=$?
STATUS=${STATUS:-0}

# Print all output (errors and warnings go to stderr in the tool itself,
# but we capture combined for parsing convenience).
echo "$OUTPUT"

# Summarise for the caller
ERRORS=$(echo "$OUTPUT" | grep -c '^ERROR:' || true)
WARNS=$(echo "$OUTPUT"  | grep -c '^WARN:'  || true)

if [[ $STATUS -ne 0 ]]; then
    echo ""
    echo "rulelint: FAILED ($ERRORS error(s), $WARNS warning(s))"
    echo "Fix all ERROR lines before marking the task complete."
    exit 1
fi

echo ""
echo "rulelint: OK ($WARNS warning(s))"
exit 0
