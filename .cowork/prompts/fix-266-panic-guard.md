# fix-266-panic-guard

You are Claude Code, working on the ventd repository.

## Task
ID: fix-266-panic-guard
Track: HAL
Goal: Guard the `read_no_mutation` subtest in `internal/hal/contract_test.go` so it doesn't panic when run on a host with NVIDIA GPU available.

## Context you should read first

- `internal/hal/contract_test.go` — entire file, especially lines 159–187 (the `read_no_mutation` subtest) and lines 325–355 (the `write_idempotent_open` subtest for the correct pattern)
- `.claude/rules/hal-contract.md` — the invariant file this test binds to
- Issue #266 (`gh issue view 266`) — root cause analysis from Mia

## What to do

1. Open `internal/hal/contract_test.go`.
2. Inside the `read_no_mutation` subtest (starts at line 161), replace the inner skip block:
   ```go
   if !bc.fileBacked {
       if !nvidia.Available() {
           t.Skipf("backend %s: NVML not available; no file-backed state to observe mutation against", bc.name)
       }
   }
   ```
   With the pattern used by `write_idempotent_open` at line ~330:
   ```go
   if !bc.fileBacked {
       return
   }
   ```
3. This guards the `ch.Opaque.(halHwmon.State)` assertion on the line immediately following the block from panicking when `bc` is the nvml case. The NVML invariant for this rule is vacuously satisfied (NVML has no file-backed state, nothing to observe mutation against), so an early return is the correct behavior.
4. Verify:
   - `go build ./...` passes.
   - `go test ./internal/hal/... -run TestHAL_Contract -race` passes.
   - The contract_test.go diff is exactly the 4-line replacement above; no other changes.
   - Rulelint is still happy (`go run ./tools/rulelint`).
5. Update `CHANGELOG.md` under `## [Unreleased]` → `### Fixed`:
   - `hal(contract_test): guard read_no_mutation subtest from panicking on hosts with NVIDIA GPU available (#266, ultrareview-1)`

## Definition of done

- `internal/hal/contract_test.go` has exactly the 4-line block replacement above, no other edits.
- `CHANGELOG.md` has the new Fixed entry.
- `go build ./... && go test -race ./internal/hal/... -run TestHAL_Contract` both succeed.
- PR opened with `Fixes: #266` in the body.

## Out of scope for this task

- Do not touch any other test file.
- Do not add new tests. (This is a P-task-style fix; no new test is needed because ultrareview-1 already documented the bug behavior and the existing `write_idempotent_open` pattern is the reference implementation. Row R19 does not apply because #266 is labeled `bug` but has a `no-regression-test` exemption rationale: the fix pattern is already tested in a sibling subtest.)
- Do not rebase any other PR.
- Do not modify `.claude/rules/hal-contract.md` (the rule text is already correct; only the test was buggy).

## Branch and PR

- Work on branch: `claude/fix-266-contract-panic-guard-{rand5}`
- Commit style: conventional commits (`fix(hal/contract_test): ...`)
- Open a non-draft PR with title: `fix(hal/contract_test): guard read_no_mutation from panic on GPU hosts (#266)`
- PR description must include:
  - The goal verbatim
  - Bulleted files-touched list (exactly 2 files)
  - "How I verified" section with the go build + go test output
  - `Fixes: #266`

## Constraints

- Do not touch files outside this list: `internal/hal/contract_test.go`, `CHANGELOG.md`
- Do not add dependencies.
- Keep CGO_ENABLED=0 compatible.
- If blocked, push WIP, open draft PR with `[BLOCKED]` prefix.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 100 words
- CONCERNS: anything you second-guessed
- FOLLOWUPS: anything you noticed but is not in scope
