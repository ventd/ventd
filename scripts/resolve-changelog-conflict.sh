#!/usr/bin/env bash
# resolve-changelog-conflict.sh — auto-merge CHANGELOG.md conflicts
# during a rebase by taking the union of bullet lines in the
# [Unreleased] / ### Added section. Outside that section, prefer the
# HEAD (rebase target) version.
#
# Stacked-PR shape: each branch adds one entry to ### Added; git's
# default 3-way merge marks the section as conflicted because the
# adjacent additions overlap. The correct resolution is an order-
# preserving union of bullets.
#
# Usage (call after `git rebase` reports a CHANGELOG conflict):
#   bash scripts/resolve-changelog-conflict.sh
#
# Also resyncs internal/web/CHANGELOG.md.embedded.
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT"

PRIMARY="CHANGELOG.md"
EMBED="internal/web/CHANGELOG.md.embedded"

if ! git ls-files --unmerged -- "$PRIMARY" | grep -q .; then
	# Embed-only conflict, or no conflict at all — just resync.
	if [[ -f "$EMBED" ]]; then
		cp "$PRIMARY" "$EMBED"
		git add "$EMBED" 2>/dev/null || true
	fi
	exit 0
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
git show ":2:$PRIMARY" > "$tmpdir/ours.md"
git show ":3:$PRIMARY" > "$tmpdir/theirs.md"

python3 "$REPO_ROOT/scripts/_resolve-changelog-conflict.py" \
	"$tmpdir/ours.md" "$tmpdir/theirs.md" "$PRIMARY"

if [[ -f "$EMBED" ]]; then
	cp "$PRIMARY" "$EMBED"
	git add "$EMBED"
fi
git add "$PRIMARY"

echo "resolve-changelog-conflict: union-merged $PRIMARY"
