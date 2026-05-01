#!/usr/bin/env bash
# install-git-hooks.sh — wire local pre-commit and pre-push hooks for ventd
#
# Hooks installed:
#   pre-commit: rule-index --check, gofmt -l, go vet ./...
#   pre-push:   gofmt -l, go vet ./...
#
# Run from the repo root. Idempotent — safe to re-run.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
if [[ -z "$repo_root" ]]; then
	echo "install-git-hooks: not in a git repo" >&2
	exit 1
fi

# Resolve the hooks dir (handles git-worktrees and core.hooksPath overrides).
hooks_dir=$(git rev-parse --git-path hooks)
mkdir -p "$hooks_dir"

write_hook() {
	local name=$1
	local body=$2
	local path="$hooks_dir/$name"
	printf '%s' "$body" >"$path"
	chmod +x "$path"
	echo "installed: $path"
}

pre_commit_body='#!/usr/bin/env bash
# ventd pre-commit hook (managed by scripts/install-git-hooks.sh)
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

# Only check Go-related staged paths; skip when staged set is purely docs/etc.
staged=$(git diff --cached --name-only --diff-filter=ACMR)
go_changed=false
rule_changed=false
if echo "$staged" | grep -E "\.go$|^go\.(mod|sum)$" >/dev/null; then
	go_changed=true
fi
if echo "$staged" | grep -E "^\.claude/rules/" >/dev/null; then
	rule_changed=true
fi

# rule-index regen check
if [[ "$rule_changed" == true ]]; then
	echo "pre-commit: rule-index --check"
	if ! go run ./tools/rule-index --check; then
		echo "pre-commit: rule-index is stale; run: go run ./tools/rule-index" >&2
		exit 1
	fi
fi

if [[ "$go_changed" == true ]]; then
	echo "pre-commit: gofmt -l ."
	bad=$(gofmt -l . 2>/dev/null || true)
	if [[ -n "$bad" ]]; then
		echo "pre-commit: gofmt issues:" >&2
		echo "$bad" >&2
		exit 1
	fi

	echo "pre-commit: go vet ./..."
	if ! go vet ./...; then
		echo "pre-commit: go vet failed" >&2
		exit 1
	fi
fi
'

pre_push_body='#!/usr/bin/env bash
# ventd pre-push hook (managed by scripts/install-git-hooks.sh)
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

echo "pre-push: gofmt -l ."
bad=$(gofmt -l . 2>/dev/null || true)
if [[ -n "$bad" ]]; then
	echo "pre-push: gofmt issues:" >&2
	echo "$bad" >&2
	exit 1
fi

echo "pre-push: go vet ./..."
if ! go vet ./...; then
	echo "pre-push: go vet failed" >&2
	exit 1
fi
'

write_hook pre-commit "$pre_commit_body"
write_hook pre-push "$pre_push_body"

echo "ok: hooks installed under $hooks_dir"
