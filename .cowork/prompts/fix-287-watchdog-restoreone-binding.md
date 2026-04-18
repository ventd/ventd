# fix-287-watchdog-restoreone-binding

You are Claude Code. Cassidy's issue #287: the `.claude/rules/watchdog-safety.md`
`RULE-WD-RESTORE-EXIT` prose was updated in #263 to name `RestoreOne` in its
description, but the bound subtest `wd_restore_exit_touches_all_entries` only
exercises `w.Restore()` (the loop), not `w.RestoreOne(pwmPath)` (the new
single-entry path).

The rulelint verifies `Bound:` markers point at existing subtests but cannot
check that subtest assertions cover what the rule's prose claims. This is
rule-binding drift.

## Goal

Extend `wd_restore_exit_touches_all_entries` with a sub-assertion that
calls `w.RestoreOne(pwm1)` on a stacked-register setup (LIFO top picked,
entry not removed from slice, enable file reads the most-recently-captured
origEnable). Pure test addition; no production code change.

## Context you should read first

- `.claude/rules/watchdog-safety.md` (lines 14-21 are RULE-WD-RESTORE-EXIT)
- `internal/watchdog/safety_test.go` (lines 40-90 are the bound subtest)
- `internal/watchdog/watchdog_test.go` (has `TestRestoreOne_MatchesMostRecent`
  and `TestRestoreOne_NoMatchIsNoOp` — these are standalone, NOT bound to
  the rule)

## What to do

1. In `internal/watchdog/safety_test.go`, after the existing two-entry
   `Restore` assertions in `wd_restore_exit_touches_all_entries`, append
   a `RestoreOne` leg. Approximately (match the existing style of the
   subtest):

```go
// After existing Restore assertions, re-perturb pwm1_enable and
// exercise RestoreOne(pwm1). LIFO contract: top match picked,
// entry stays in slice (RestoreOne does NOT deregister).
if err := os.WriteFile(enablePath1, []byte("2\n"), 0o600); err != nil {
    t.Fatalf("perturb ch1 for RestoreOne leg: %v", err)
}
w.RestoreOne(pwm1)
if got := readTrimmed(t, enablePath1); got != strconv.Itoa(enable1) {
    t.Errorf("RestoreOne(pwm1): enable = %q, want %q",
        got, strconv.Itoa(enable1))
}
if len(w.entries) != 2 {
    t.Errorf("RestoreOne must not deregister: len(entries) = %d, want 2",
        len(w.entries))
}
```

   Adjust variable names (`pwm1`, `enablePath1`, `enable1`, etc.) to match
   whatever the existing subtest already defined. Do not introduce new
   helpers unless the existing ones don't cover readTrimmed/file-setup.

2. Verify the rule binding is still correct after your edit. The `Bound:`
   marker in `.claude/rules/watchdog-safety.md` should still point at
   `wd_restore_exit_touches_all_entries` — you're extending this subtest,
   not splitting it. Confirm the test still runs via
   `go test -run 'TestSafety_Invariants/wd_restore_exit_touches_all_entries' ./internal/watchdog/...`.

3. If the subtest grows too long for readability (>60 lines), prefer
   splitting into two bound subtests:
   `wd_restore_touches_all_entries` and `wd_restoreone_exit_contract`,
   updating `.claude/rules/watchdog-safety.md` Bound: line to list both.
   Use judgment — if extension keeps the subtest under 60 lines,
   extension is simpler.

## Definition of done

- `internal/watchdog/safety_test.go` updated with the RestoreOne leg.
- Test passes: `go test -race -count=1 ./internal/watchdog/...`.
- If split chosen: `.claude/rules/watchdog-safety.md` Bound: line names
  both subtests; rulelint CI job passes.
- go vet / gofmt clean.
- CHANGELOG entry under `## Unreleased / ### Changed` noting
  "watchdog: RULE-WD-RESTORE-EXIT bound subtest now covers RestoreOne
  (closes #287)."
- PR references `Fixes #287`.

## Out of scope

- Production code changes.
- New rule files.
- Refactoring `watchdog_test.go`'s standalone RestoreOne tests.
- Adding any OTHER invariants.

## Branch and PR

- Branch: `claude/fix-287-watchdog-restoreone-binding`
- PR title: `test(watchdog): bind RestoreOne to RULE-WD-RESTORE-EXIT (fixes #287)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `internal/watchdog/safety_test.go`
  - `.claude/rules/watchdog-safety.md` (ONLY if split chosen; Bound: line update)
  - `CHANGELOG.md`
- No production code changes.
- No new dependencies.
- `CGO_ENABLED=0` compatible (test-only change; trivially compatible).

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: CHOSE_EXTEND_OR_SPLIT — state which approach you
  took and why.
- Additional section: TEST_RUN — paste the `go test -run` output
  showing the updated subtest passing.

## Time budget

20 minutes wall-clock.

## Final note

This is a Sonnet-eligible test-only fix. Parallel-safe with any other
non-watchdog work.
