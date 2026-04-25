#!/usr/bin/env bash
set -u

# ventd preflight validation script
# Usage: preflight.sh <spec-id>
# Validates: correct remote, on a feature branch, clean tree, spec committed, rulelint green

spec_id="${1:-unknown}"
header="ventd preflight — spec=${spec_id}"

echo "$header"
echo "$(printf '=%.0s' {1..60})"

# Collect results
declare -A results
pass=0 fail=0 warn=0

# Check 1: correct remote
if git remote -v | grep -q "ventd/ventd"; then
  results["remote"]="✓"
  ((pass++))
else
  results["remote"]="✗ wrong remote"
  ((fail++))
fi

# Check 2: not on main
current_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
if [[ "$current_branch" != "main" ]]; then
  results["branch"]="✓ (${current_branch})"
  ((pass++))
else
  results["branch"]="✗ on main, expected feature branch"
  ((fail++))
fi

# Check 3: working tree clean
if [[ -z "$(git status --porcelain 2>/dev/null)" ]]; then
  results["tree"]="✓"
  ((pass++))
else
  results["tree"]="⚠ dirty"
  ((warn++))
fi

# Check 4: display recent tags
echo ""
echo "Recent tags:"
git tag --sort=-v:refname 2>/dev/null | head -5 | sed 's/^/  /'

# Check 5: spec exists
if git ls-files "specs/${spec_id}.md" 2>/dev/null | grep -q .; then
  results["spec"]="✓"
  ((pass++))
else
  results["spec"]="✗ spec not committed: specs/${spec_id}.md"
  ((fail++))
fi

# Check 6: rulelint passes
if command -v rulelint >/dev/null 2>&1; then
  lint_cmd="rulelint"
elif [[ -f tools/rulelint ]] && [[ -x tools/rulelint ]]; then
  lint_cmd="tools/rulelint"
elif [[ -d tools/rulelint ]]; then
  lint_cmd="go run ./tools/rulelint"
else
  results["rulelint"]="⚠ rulelint not found (build: go build -o tools/rulelint ./tools/rulelint)"
  ((warn++))
  lint_cmd=""
fi

if [[ -n "$lint_cmd" ]] && $lint_cmd >/dev/null 2>&1; then
  results["rulelint"]="✓"
  ((pass++))
elif [[ -z "$lint_cmd" ]]; then
  # Already marked as warning above
  :
else
  results["rulelint"]="✗ rulelint failed"
  ((fail++))
fi

# Check 7: display PR for current branch
echo ""
echo "PRs for this branch:"
gh pr list --head "$current_branch" --json number,state 2>/dev/null | head -3 | sed 's/^/  /' || echo "  (none or gh error)"

# Summary
echo ""
echo "Summary:"
for check in "remote" "branch" "tree" "spec" "rulelint"; do
  echo "  ${check}: ${results[$check]:-?}"
done

echo ""
echo "$(printf '=%.0s' {1..60})"

# Exit code
if (( fail > 0 )); then
  exit 1
elif (( warn > 0 )); then
  exit 2
else
  exit 0
fi
