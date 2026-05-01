#!/usr/bin/env bash
# release.sh — tag + push + create GitHub release for ventd vX.Y.Z.
#
# Reads `release-notes/vX.Y.Z.md`, derives the title from the first
# `## ` line, then runs the three-step ritual atomically:
#
#   git tag -a vX.Y.Z -m "<title>"
#   git push origin vX.Y.Z
#   gh release create vX.Y.Z --title "<title>" --notes-file <path> --verify-tag
#
# Refuses to run when:
#   - argument missing or not in vX.Y.Z[.W] form
#   - release-notes/<version>.md is absent
#   - the tag already exists (locally or on remote)
#   - working tree is not clean
#   - HEAD is not main
#
# Usage:
#   bash scripts/release.sh v0.5.6
#
# To bypass any single check (rare; document why):
#   ALLOW_DIRTY=1 bash scripts/release.sh ...     # skip clean-tree check
#   ALLOW_NONMAIN=1 bash scripts/release.sh ...   # skip main-branch check

set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: $0 vX.Y.Z[.W]" >&2
	exit 2
fi
ver=$1

if ! [[ "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
	echo "release: '$ver' is not a valid semver tag (expected vX.Y.Z or vX.Y.Z.W)" >&2
	exit 2
fi

cd "$(git rev-parse --show-toplevel)"

notes="release-notes/${ver}.md"
if [[ ! -f "$notes" ]]; then
	echo "release: release-notes/${ver}.md is missing" >&2
	echo "release: draft it first; the script reads the first '## ' line as the release title" >&2
	exit 1
fi

# First H2 line is the canonical release title. Strip the leading '## '.
title=$(grep -m1 -E '^## ' "$notes" | sed -E 's/^##\s+//')
if [[ -z "$title" ]]; then
	echo "release: could not extract a title (no '## ' heading) from $notes" >&2
	exit 1
fi

if git rev-parse "$ver" >/dev/null 2>&1; then
	echo "release: tag $ver already exists locally" >&2
	exit 1
fi
if git ls-remote --tags origin "refs/tags/$ver" 2>/dev/null | grep -q .; then
	echo "release: tag $ver already exists on origin" >&2
	exit 1
fi

if [[ "${ALLOW_NONMAIN:-0}" != "1" ]]; then
	branch=$(git rev-parse --abbrev-ref HEAD)
	if [[ "$branch" != "main" ]]; then
		echo "release: HEAD is on '$branch', not main" >&2
		echo "release: bypass with ALLOW_NONMAIN=1 (rare)" >&2
		exit 1
	fi
fi

if [[ "${ALLOW_DIRTY:-0}" != "1" ]]; then
	if ! git diff-index --quiet HEAD -- 2>/dev/null; then
		echo "release: working tree has uncommitted changes" >&2
		echo "release: bypass with ALLOW_DIRTY=1 (rare)" >&2
		exit 1
	fi
fi

echo "release: tagging $ver"
echo "release: title    = $title"
echo "release: notes    = $notes"
echo "release: HEAD     = $(git rev-parse --short HEAD)"
echo
echo "Press Enter to continue, Ctrl-C to abort..."
read -r

git tag -a "$ver" -m "$title"
git push origin "$ver"
gh release create "$ver" --title "$title" --notes-file "$notes" --verify-tag

echo
echo "release: $ver published — https://github.com/ventd/ventd/releases/tag/${ver}"
