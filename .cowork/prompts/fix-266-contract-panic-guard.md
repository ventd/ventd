# fix-266-contract-panic-guard

You are Claude Code. One-line fix for #266: the `read_no_mutation`
subtest in `internal/hal/contract_test.go` will panic on a host with
a real NVIDIA GPU available, because the type assertion to
`halHwmon.State` at line ~171 isn't gated by `bc.fileBacked`.

## Task

ID: fix-266-contract-panic-guard
Track: TEST
Goal: Guard `read_no_mutation` subtest's type assertion behind `bc.fileBacked` check.

## Context

Read first:
- `internal/hal/contract_test.go` (the full file, ~400 lines)
- `.claude/rules/hal-contract.md` (invariant bindings — confirm `RULE-HAL-002 read_no_mutation` is still bound to this subtest)

Specifically compare:
- Lines ~161-200 (`read_no_mutation` subtest — currently unsafe)
- Lines ~320-340 (`write_idempotent_open` subtest — has the correct pattern)

## Steps

1. In `read_no_mutation` subtest, replace the inner `if !bc.fileBacked { if !nvidia.Available() { t.Skipf(...) } }` block with the pattern from `write_idempotent_open`:

   ```go
   if !bc.fileBacked {
       return
   }
   ```

   This matches the write_idempotent_open pattern. The assertion to `halHwmon.State` at line ~171 is only safe for file-backed backends (hwmon). NVML opaque state has a different type.

2. Do not change the outer logic of the subtest, just the gate.

3. Run tests locally to confirm:
   ```
   go test -race ./internal/hal/...
   ```

4. Commit with message `fix(hal/contract_test): guard read_no_mutation type assertion with fileBacked check (closes #266)`.

## Branch and PR

- Branch: `claude/fix-266-contract-panic-guard-<rand5>`
- Commit style: conventional commits
- Open PR (not draft, ready-for-review — this is trivially small)
- Title: `fix(hal/contract_test): guard read_no_mutation type assertion with fileBacked check`
- Body: "Closes #266. One-line gate fix — mirror the pattern from write_idempotent_open. See ultrareview-1 §ULTRA-01 finding 1."

## Constraints

- Allowlist: `internal/hal/contract_test.go` only. Do NOT touch production code.
- No new tests (the existing subtest structure is correct; just needs the gate).
- Do not modify `.claude/rules/hal-contract.md` — the rule binding is correct, only the implementation was drifting.

## Reporting

STATUS: done | partial | blocked
PR: <url>
DIFF_LINES: <just the number of line changes>
TIME_SPENT: <minutes>

## Out of scope

- Tests beyond the single-subtest gate fix.
- Refactoring the contract_test.go structure.
- Adding a pwmsys/asahi/crosec row to the contract test table (that's a Phase 2 follow-up, out of scope here).

## Model

Sonnet 4.6. One-line gate fix, matches an existing pattern in the same file.

## Time budget

15 minutes wall-clock.
