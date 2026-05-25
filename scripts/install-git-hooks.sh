#!/usr/bin/env bash
# install-git-hooks.sh — wire local pre-commit and pre-push hooks for ventd
#
# Hooks installed:
#   pre-commit: auto-mirror CHANGELOG.md + scripts/install.sh into their
#               internal/web/*.embedded copies (so a single edit yields a
#               consistent commit), rule-index --check, gofmt -l,
#               go vet ./..., go mod tidy drift
#   pre-push:   bash scripts/ci-local.sh (full CI gate sweep)
#
# Run from the repo root. Idempotent — safe to re-run.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
if [[ -z "$repo_root" ]]; then
	echo "install-git-hooks: not in a git repo" >&2
	exit 1
fi

# golangci-lint version. Must match .github/workflows/ci.yml so the local
# gate catches the same things CI catches. Bumping here without bumping
# the workflow (or vice versa) is the local-vs-remote drift class.
GOLANGCI_LINT_VERSION="v2.1.6"

# Install golangci-lint at the CI-pinned version if missing. Without this
# step, scripts/ci-local.sh silently skipped the lint step and lint-class
# bugs reached CI (cf. PR #1340 SA4003). Idempotent: skips when the right
# version is already on PATH or in $GOPATH/bin.
install_golangci_lint() {
	local want="$GOLANGCI_LINT_VERSION"
	local gopath_bin
	gopath_bin=$(go env GOPATH 2>/dev/null)/bin

	local current=""
	if command -v golangci-lint >/dev/null 2>&1; then
		current=$(golangci-lint --version 2>/dev/null | head -1 || true)
	elif [[ -x "$gopath_bin/golangci-lint" ]]; then
		current=$("$gopath_bin/golangci-lint" --version 2>/dev/null | head -1 || true)
	fi

	if [[ "$current" == *"$want"* ]]; then
		echo "golangci-lint $want already installed"
		return 0
	fi

	if ! command -v go >/dev/null 2>&1; then
		echo "install-git-hooks: go toolchain not found; install Go before re-running" >&2
		return 1
	fi

	echo "installing golangci-lint $want (matching .github/workflows/ci.yml)..."
	GOFLAGS= go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$want"
	echo "installed: $gopath_bin/golangci-lint"
	if [[ ":$PATH:" != *":$gopath_bin:"* ]]; then
		echo "note: $gopath_bin is not on PATH. scripts/ci-local.sh looks in \$GOPATH/bin as a fallback, so this is fine for the pre-push gate. Add it to PATH if you want to run \`golangci-lint\` directly."
	fi
}

install_golangci_lint

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
mod_changed=false
map_relevant=false
if echo "$staged" | grep -E "\.go$" >/dev/null; then
	go_changed=true
fi
if echo "$staged" | grep -E "^go\.(mod|sum)$" >/dev/null; then
	mod_changed=true
fi
if echo "$staged" | grep -E "^\.claude/rules/" >/dev/null; then
	rule_changed=true
fi
# mapcheck is relevant when a structural surface or the map itself is touched.
if echo "$staged" | grep -E "^(cmd|internal)/|^\.github/workflows/|^docs/codebase-map\.md$" >/dev/null; then
	map_relevant=true
fi

# Auto-mirror sync for files that Go embed cannot reach outside its own
# package directory. Whenever the canonical source is staged, copy it
# into the in-package mirror and re-stage so a single edit produces a
# consistent commit. Without this, every CHANGELOG.md or install.sh
# touch had to be followed by a manual `cp` step, and the embed-drift
# tests caught the omission on CI.
sync_embed() {
	local src=$1
	local dst=$2
	if echo "$staged" | grep -qE "^${src//./\\.}$"; then
		if [[ ! -f "$src" ]]; then
			return 0
		fi
		if ! cmp -s "$src" "$dst" 2>/dev/null; then
			cp "$src" "$dst"
			git add "$dst"
			echo "pre-commit: auto-synced $dst from $src"
		fi
	fi
}
sync_embed CHANGELOG.md internal/web/CHANGELOG.md.embedded
sync_embed scripts/install.sh internal/web/install.sh.embedded

# rule-index regen check
if [[ "$rule_changed" == true ]]; then
	echo "pre-commit: rule-index --check"
	if ! go run ./tools/rule-index --check; then
		echo "pre-commit: rule-index is stale; run: go run ./tools/rule-index" >&2
		exit 1
	fi
fi

# codebase-map surface coverage check
if [[ "$map_relevant" == true ]]; then
	echo "pre-commit: mapcheck"
	if ! go run ./tools/mapcheck; then
		echo "pre-commit: docs/codebase-map.md is missing a surface (see above)" >&2
		exit 1
	fi
fi

# go mod tidy drift check (the siphash indirect→direct case from PR #730).
if [[ "$go_changed" == true || "$mod_changed" == true ]]; then
	echo "pre-commit: go mod tidy drift"
	# Snapshot, run tidy, compare. Restore mod state regardless.
	mod_before=$(sha256sum go.mod | cut -d" " -f1)
	sum_before=$(sha256sum go.sum | cut -d" " -f1)
	go mod tidy
	mod_after=$(sha256sum go.mod | cut -d" " -f1)
	sum_after=$(sha256sum go.sum | cut -d" " -f1)
	if [[ "$mod_before" != "$mod_after" || "$sum_before" != "$sum_after" ]]; then
		echo "pre-commit: go.mod or go.sum drifted; commit the result of \`go mod tidy\`" >&2
		git --no-pager diff go.mod go.sum | head -20 >&2
		exit 1
	fi
fi

if [[ "$go_changed" == true ]]; then
	# Resolve gofmt explicitly via the Go toolchain so the check
	# does not silently no-op when gofmt is missing from PATH
	# (the v0.8.x drift class — gofmt was outside PATH on the dev
	# box, gofmt -l 2>/dev/null swallowed the not-found error,
	# CI then caught the drift). Per scripts/install-git-hooks.sh
	# remediation note attached to PR #1357.
	echo "pre-commit: gofmt -l ."
	goroot=$(go env GOROOT 2>/dev/null || true)
	gofmt_bin=""
	if [[ -n "$goroot" && -x "$goroot/bin/gofmt" ]]; then
		gofmt_bin="$goroot/bin/gofmt"
	elif command -v gofmt >/dev/null 2>&1; then
		gofmt_bin="gofmt"
	fi
	if [[ -z "$gofmt_bin" ]]; then
		echo "pre-commit: gofmt not found (checked GOROOT/bin and PATH)" >&2
		exit 1
	fi
	bad=$("$gofmt_bin" -l .)
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
#
# Delegates to scripts/ci-local.sh which mirrors every CI gate.
# Failures here mean CI would also fail; fix locally before pushing.
#
# To bypass (e.g. emergency fix): git push --no-verify
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

if [[ -x "scripts/ci-local.sh" ]]; then
	exec bash scripts/ci-local.sh
fi

# Fallback: minimal sweep if ci-local.sh is unavailable (e.g. older
# branch without the script).
echo "pre-push: gofmt -l ."
goroot=$(go env GOROOT 2>/dev/null || true)
gofmt_bin=""
if [[ -n "$goroot" && -x "$goroot/bin/gofmt" ]]; then
	gofmt_bin="$goroot/bin/gofmt"
elif command -v gofmt >/dev/null 2>&1; then
	gofmt_bin="gofmt"
fi
if [[ -z "$gofmt_bin" ]]; then
	echo "pre-push: gofmt not found (checked GOROOT/bin and PATH)" >&2
	exit 1
fi
bad=$("$gofmt_bin" -l .)
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
echo
echo "next: run \`bash scripts/ci-local.sh\` to verify all CI gates pass locally"
