#!/usr/bin/env bash
set -u

# ventd release validation script
# Usage: release-validate.sh <version-tag>
# Validates: tag does not exist, no orphan release, cosign --bundle= form, action SHA pinning

target_version="${1:-v0.0.0}"
current_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
header="ventd release validate — target=${target_version}"

echo "$header"
echo "$(printf '=%.0s' {1..60})"

# Collect results
declare -A results
pass=0 fail=0 warn=0

# Check 1: run preflight with detected or fallback spec-id
spec_id=$(echo "$current_branch" | grep -oE 'spec-[0-9]+' || echo "spec-unknown")
echo ""
echo "Running preflight with spec_id=${spec_id}..."
if .claude/scripts/preflight.sh "$spec_id"; then
  results["preflight"]="✓"
  ((pass++))
else
  results["preflight"]="✗ preflight failed (exit code: $?)"
  ((fail++))
fi

# Check 2: tag does not exist
if git tag --sort=-v:refname | grep -qx "$target_version"; then
  results["tag_collision"]="✗ tag $target_version already exists"
  ((fail++))
else
  results["tag_collision"]="✓"
  ((pass++))
fi

# Check 3: no orphan release
if gh release list --limit 20 2>/dev/null | grep -q "$target_version"; then
  results["orphan_release"]="✗ orphan release exists"
  ((fail++))
else
  results["orphan_release"]="✓"
  ((pass++))
fi

# Check 4: display recent release runs
echo ""
echo "Recent release.yml runs:"
gh run list --workflow=release.yml --limit 5 2>/dev/null | head -3 | sed 's/^/  /' || echo "  (error or none)"

# Check 5: cosign uses --bundle= form
if grep -q "cosign.*--bundle=" .github/workflows/release.yml 2>/dev/null; then
  results["cosign_bundle"]="✓"
  ((pass++))
else
  results["cosign_bundle"]="✗ cosign not using --bundle= form"
  ((fail++))
fi

# Check 6: action SHA pinning
echo ""
echo "Checking action SHA pinning in release.yml..."
bad_actions=()
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  # Extract the ref part (after @)
  ref="${line##*@}"
  ref="${ref%%[[:space:]#]*}"
  # Check if it's a proper v-tag (v[0-9]+) or 40-char SHA
  if ! [[ "$ref" =~ ^v[0-9] ]] && ! [[ "$ref" =~ ^[a-f0-9]{40}$ ]]; then
    bad_actions+=("$line")
  fi
done < <(grep "uses:" .github/workflows/release.yml | grep -v "^[[:space:]]*#")

if [[ ${#bad_actions[@]} -gt 0 ]]; then
  results["sha_pinning"]="✗ unpinned actions:"
  for action in "${bad_actions[@]}"; do
    echo "    $action"
  done
  ((fail++))
else
  results["sha_pinning"]="✓"
  ((pass++))
fi

# Check 7: cyclonedx version range (just warn)
if grep -E "spec[_-]?(version|min)" .github/workflows/release.yml | grep -qv "1\.[56]"; then
  results["cyclonedx"]="⚠ version range may not cover 1.5+1.6"
  ((warn++))
else
  results["cyclonedx"]="✓"
  ((pass++))
fi

# Summary
echo ""
echo "Summary:"
for check in "preflight" "tag_collision" "orphan_release" "cosign_bundle" "sha_pinning" "cyclonedx"; do
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
