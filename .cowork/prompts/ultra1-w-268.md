You are Claude Code, working on the ventd repository.

## Task
ID: ULTRA1-W-268
Track: CLEAN
Goal: Prune 8 unreachable exported functions from `internal/hwmon` per ultrareview-1 ULTRA-04 (issue #268). Dead API surface that confuses contributors and inflates the uncovered-statement count. Pruning improves package coverage ratio toward the ultrareview 50% threshold for free.

## Care level
LOW-MEDIUM. Deletions only. Main risk: one of these is actually called from somewhere deadcode missed (e.g., reflection, string-indexed dispatch). Must run `grep -r '<name>' internal/ cmd/` to confirm zero references before deleting each.

## Context

- `internal/hwmon/hwmon.go` — core package.
- `internal/hwmon/autoload.go` — module autoload.
- `internal/hwmon/modulesalias.go` — kernel module alias parser.
- `internal/hwmon/watcher.go` — uevent watcher.
- Ultrareview evidence at `.cowork/reviews/ultrareview-1.md` ULTRA-04 findings 1 and 2.

## The dead eight

Verify each before deleting. For every function, run:
```
grep -rn "<funcname>\b" internal/ cmd/ | grep -v '_test.go' | grep -v hwmon.go | grep -v autoload.go | grep -v modulesalias.go | grep -v watcher.go
```
(exclude the defining file itself). Zero results → safe to delete. Non-zero → flag and leave it.

1. `internal/hwmon/hwmon.go:19`  — `ReadTemp`
2. `internal/hwmon/hwmon.go:90`  — `WritePWMSafe`
3. `internal/hwmon/hwmon.go:222` — `ReadFanMinRPM`
4. `internal/hwmon/autoload.go:731` — `FindPWMPaths`
5. `internal/hwmon/modulesalias.go:52` — `parseModulesBuiltinModinfo`
6. `internal/hwmon/watcher.go:168` — `WithEnumerator`
7. `internal/hwmon/watcher.go:174` — `WithUeventSubscriber`
8. `internal/hwmon/watcher.go:180` — `WithRescanPeriod`
9. `internal/hwmon/watcher.go:186` — `WithDebounce`
10. `internal/hwmon/watcher.go:207` — `WithRebindMinInterval`

(10 items — issue #268 says 8 but counts the watcher `With*` group as one cluster; they're 5 separate functions. Treat each individually.)

## What to do

1. For each function above, run the grep described. Confirm zero references (excluding the defining file + test files).

2. If zero refs: delete the function. Also delete any associated private helpers that become dead as a result — run a second pass of `go vet` + `staticcheck` to confirm.

3. If non-zero refs (deadcode was wrong): leave the function, note it in the PR body "Retained because grep shows references at <path>:<line>."

4. Run `go build ./...` after each deletion to confirm no callers broke. If something breaks, revert that specific deletion and flag it.

5. Also delete any test functions that exclusively test the deleted functions (e.g., `TestReadTemp_*`). Test files are in `internal/hwmon/*_test.go`.

6. Full test suite under -race:
   ```
   CGO_ENABLED=0 go test -race -count=1 ./...
   ```

7. Measure package coverage before and after:
   ```
   go test -cover ./internal/hwmon/
   ```
   Expected: coverage ratio improves (denominator shrinks even if numerator holds).

8. Close #268 in PR body.

## Definition of done

- Each of the 10 functions either deleted (confirmed zero refs) or retained with explicit justification.
- `go build ./...` passes.
- Full test suite passes under `-race`.
- `internal/hwmon` coverage ratio recorded in PR body, before and after.
- `Closes #268` in PR body.
- CHANGELOG `## Unreleased` / `### Removed` entry with the function names.

## Out of scope

- Adding NEW tests to lift hwmon coverage further (that's a followup).
- Any production logic changes.
- Deletions in other packages.

## Branch and PR

- Branch: `claude/dead-hwmon-prune`
- Title: `dead(hwmon): prune unreachable exported functions (closes #268, ultrareview-1)`

## Constraints

- Files: `internal/hwmon/hwmon.go`, `internal/hwmon/autoload.go`, `internal/hwmon/modulesalias.go`, `internal/hwmon/watcher.go`, `internal/hwmon/*_test.go` (only if tests become orphaned), `CHANGELOG.md`.
- No new dependencies.
- No public API addition (only removal).
- CGO_ENABLED=0 compatible.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
- Additional fields: DELETED_COUNT, RETAINED_COUNT (with per-function justification for retained), COVERAGE_BEFORE / COVERAGE_AFTER on `internal/hwmon`.
