# fix-304-regresslint-annotations

You are Claude Code. Implement the retroactive annotation sweep described in issue #304.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-304-regresslint-annotations origin/main
```

Abort if `.cowork/prompts/` is in your working tree; you're on the wrong worktree. Run `git status` first.

## Task

Add `// regresses #<N>` magic-comment annotations (recognised by `tools/regresslint/` since PR #299) to 7 existing covering tests. The mapping is in issue #304's body; verify each location by reading the test file before editing.

### Per-issue annotation map

| Issue | Expected test file | Suggested covering test |
|---|---|---|
| #59  | `internal/config/migrate_test.go` | migration tests for TLS auto-populate (both-present, neither-present, etc.) |
| #86  | `internal/config/resolve_hwmon_test.go` | multi-candidate disambiguation tests landed in PR #93 |
| #103 | `cmd/ventd/main_test.go` or `internal/config/` startup-retry tests | first-boot discrimination |
| #140 | `internal/hwmon/autoload_test.go` | `TestModuleFromPath` for nct6683 |
| #177 | `internal/web/setup_handlers_test.go` | `TestHandleSystemReboot_RefusedInContainer` |
| #200 | `internal/web/http_redirect_test.go` | redirect-on-port-9999 tests |
| #208 | `internal/web/e2e_test.go` | `TestE2E_SettingsModal_PopulatedSections` |

For each:

1. Open the suggested file. If it doesn't exist or doesn't contain the described test, search the tree for a matching test (use grep on the issue topic keywords).
2. If the covering test is found: add `// regresses #<N>` on the line IMMEDIATELY above `func TestXxx(t *testing.T)`.
3. If the binding is subtest-level (the test has multiple `t.Run` blocks and only one is bug-relevant), add the comment INSIDE the relevant `t.Run("...", func(t *testing.T) {` block on the first line, or immediately above the `t.Run(` call.
4. If the covering test does NOT exist: do NOT add an annotation. Record the gap in your PR body under `UNRESOLVED` and move on.

## Allowlist

- Any `_test.go` file under `internal/` or `cmd/` that requires annotation.
- `CHANGELOG.md` if annotations are added (single Unreleased entry).

**Do NOT edit any non-test file. Do NOT edit any `.go` file that doesn't end in `_test.go`.**

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/... ./cmd/... ./tools/regresslint/...
gofmt -l .
# Run the regresslint binary against the tree itself to verify it recognises the annotations:
go run ./tools/regresslint/
```

`go run ./tools/regresslint/` exit code should be 0 with the annotations applied. If it still reports any of #59, #86, #103, #140, #177, #200, #208 as "no regression test", the annotation didn't bind — re-read the annotation placement rules in `tools/regresslint/main.go`.

## PR

Open the PR ready (not draft). Title: `test: bind existing regression tests to 7 closed bug issues via magic comments (closes #304)`.

PR body:
- Closes `#304`
- BRANCH_CLEANLINESS block
- PER_ISSUE_RESULT table: for each of the 7 issues, show which file:line got annotated and what the test name was
- UNRESOLVED section (if any): issues where the covering test didn't exist
- CHANGELOG entry under `## [Unreleased] / ### Changed` if you added annotations, or omit if nothing was annotated

## Constraints

- Do NOT merge.
- Do NOT rename any test.
- Do NOT create new tests to cover the gap — if a test is missing, report it unresolved.
- Do NOT modify `tools/regresslint/` source.
- Single commit per logical grouping (OK to split into 2-3 commits if the 7 annotations span distinct packages; the PR is squashed on merge).

## Reporting

- STATUS: done | blocked
- PR URL.
- Count: X/7 annotations applied, Y/7 UNRESOLVED.
- Tail of `go run ./tools/regresslint/` stdout.
