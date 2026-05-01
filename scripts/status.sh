#!/usr/bin/env bash
# status.sh — at-a-glance project state for ventd.
#
# Prints branch, tag distance, open PRs with CI state, and any
# release-notes draft for the next tag.
#
# Designed to recover context fast after session-resume / context
# compaction. Intentionally short — under one screen of output.
#
# Usage:
#   bash scripts/status.sh
#   make status   (when wired)

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

bold() { printf "\033[1m%s\033[0m\n" "$*"; }
dim() { printf "\033[2m%s\033[0m\n" "$*"; }
green() { printf "\033[32m%s\033[0m" "$*"; }
yellow() { printf "\033[33m%s\033[0m" "$*"; }
red() { printf "\033[31m%s\033[0m" "$*"; }

bold "Branch"
echo "  $(git rev-parse --abbrev-ref HEAD)"
echo "  HEAD     $(git rev-parse --short HEAD)"
echo "  Remote   $(git rev-parse --abbrev-ref --symbolic-full-name @{u} 2>/dev/null || echo '(no upstream)')"
echo

bold "Tag distance"
last_tag=$(git describe --tags --abbrev=0 2>/dev/null || echo "(none)")
if [[ "$last_tag" != "(none)" ]]; then
	ahead=$(git rev-list --count "${last_tag}..HEAD")
	echo "  Last tag: $last_tag (HEAD is +${ahead})"
	if [[ -d release-notes ]]; then
		next_draft=$(find release-notes -name "v*.md" -newer "$(git log -1 --format=%cd --date=iso "$last_tag" 2>/dev/null || echo /dev/null)" 2>/dev/null | head -1)
		if [[ -n "$next_draft" ]]; then
			echo "  Next:     $(basename "$next_draft" .md) (draft at $next_draft)"
		fi
	fi
else
	echo "  No tags yet"
fi
echo

bold "Open PRs"
if command -v gh >/dev/null 2>&1; then
	# gh pr list with status check rollup; up to 8 most recent
	prs=$(gh pr list --limit 8 --json number,title,headRefName,statusCheckRollup --jq '.[] | "\(.number)|\(.headRefName)|\(.title)|\(.statusCheckRollup | length)|\([.statusCheckRollup[] | select(.conclusion == "SUCCESS")] | length)|\([.statusCheckRollup[] | select(.conclusion == "FAILURE")] | length)|\([.statusCheckRollup[] | select(.status == "IN_PROGRESS" or .status == "QUEUED" or .status == "PENDING")] | length)"' 2>/dev/null || echo "")
	if [[ -z "$prs" ]]; then
		dim "  (none)"
	else
		echo "$prs" | while IFS='|' read -r num branch title total pass fail pending; do
			status=""
			if [[ "$fail" -gt 0 ]]; then
				status=$(red "${fail} failed")
			elif [[ "$pending" -gt 0 ]]; then
				status=$(yellow "${pending} pending")
			elif [[ "$pass" -gt 0 ]]; then
				status=$(green "${pass}/${total} green")
			else
				status=$(dim "no checks")
			fi
			# Truncate title to 60 chars for tidy output
			short="${title:0:60}"
			[[ ${#title} -gt 60 ]] && short="${short}..."
			printf "  #%-4s %-50s %s\n" "$num" "$short" "$status"
		done
	fi
else
	dim "  (gh not installed)"
fi
echo

bold "Working tree"
if git diff-index --quiet HEAD -- 2>/dev/null && [[ -z "$(git ls-files --others --exclude-standard)" ]]; then
	echo "  $(green clean)"
else
	mod=$(git diff --name-only HEAD | wc -l)
	new=$(git ls-files --others --exclude-standard | wc -l)
	echo "  $(yellow modified): ${mod} files, ${new} untracked"
fi
