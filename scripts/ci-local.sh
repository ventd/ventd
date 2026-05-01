#!/usr/bin/env sh
# ci-local.sh — run every CI gate locally before pushing.
#
# Mirrors the gates that have caught real failures during the
# v0.5.x → v0.6.0 patch sequence:
#
#   1. go mod tidy drift (go get adds new deps as `// indirect`
#      until source files import them; CI's `git diff --exit-code
#      go.mod go.sum` catches the mismatch).
#   2. gofmt drift.
#   3. go vet.
#   4. golangci-lint.
#   5. rule-index drift (after editing .claude/rules/*.md).
#   6. rulelint with --suggest --check-binding-uniqueness.
#   7. go test -count=1 -short ./...
#   8. shellcheck on every script (when available).
#
# Designed to run from a clean working tree. Use after staging
# changes; the script does not modify state on success. On
# failure it prints the offending diff or test name and exits
# non-zero.
#
# Usage:
#   bash scripts/ci-local.sh
#   make ci-local         (when wired into the Makefile)
#
# To enforce on every push, install the git hooks:
#   bash scripts/install-git-hooks.sh
# The pre-push hook will invoke this script.

set -eu

cd "$(git rev-parse --show-toplevel)"

red() { printf "\033[31m%s\033[0m\n" "$*"; }
green() { printf "\033[32m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }

step() { printf "\n=== %s ===\n" "$*"; }

# 1. go mod tidy drift
step "go mod tidy"
go mod tidy
if ! git diff --quiet go.mod go.sum 2>/dev/null; then
  red "FAIL: go.mod / go.sum drifted; commit the result of \`go mod tidy\`"
  git --no-pager diff go.mod go.sum | head -40
  exit 1
fi
green "ok"

# 2. gofmt
step "gofmt -l ."
fmt_diff=$(gofmt -l . 2>&1 || true)
if [ -n "$fmt_diff" ]; then
  red "FAIL: gofmt -l found unformatted files:"
  echo "$fmt_diff"
  exit 1
fi
green "ok"

# 3. go vet
step "go vet ./..."
go vet ./...
green "ok"

# 4. golangci-lint (if installed)
step "golangci-lint run"
if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run --timeout=5m
  green "ok"
elif [ -x "$HOME/go/bin/golangci-lint" ]; then
  "$HOME/go/bin/golangci-lint" run --timeout=5m
  green "ok"
else
  yellow "skip: golangci-lint not installed (install with go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)"
fi

# 5. rule-index
step "rule-index --check"
if ! go run ./tools/rule-index --check; then
  red "FAIL: .claude/RULE-INDEX.md is stale; run: go run ./tools/rule-index"
  exit 1
fi
green "ok"

# 6. rulelint
step "rulelint --suggest --check-binding-uniqueness"
go run ./tools/rulelint --suggest --check-binding-uniqueness
green "ok"

# 7. go test -short
step "go test -count=1 -short ./..."
go test -count=1 -short ./...
green "ok"

# 8. shellcheck
step "shellcheck"
if command -v shellcheck >/dev/null 2>&1; then
  # Let shellcheck auto-detect dialect from each script's shebang;
  # passing -s sh would false-positive bash-only scripts. Error-only
  # severity matches the existing CI gate; style warnings are
  # surfaced by `make lint-shell` but don't fail the build.
  scripts_to_check=$(find . \( -path ./.git -o -path ./node_modules -o -path ./vendor \) -prune -o -name '*.sh' -type f -print 2>/dev/null)
  if [ -n "$scripts_to_check" ]; then
    # shellcheck disable=SC2086
    shellcheck --severity=error $scripts_to_check
  fi
  green "ok"
else
  yellow "skip: shellcheck not installed (apt install shellcheck)"
fi

green "
=== ALL CHECKS PASSED ==="
