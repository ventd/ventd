You are Claude Code, working on the ventd repository.

## Task
ID: P1-HOT-02
Track: HOT
Goal: Add symmetric error handling on PWM write failures in the controller. On a Write error: retry once at 50ms; if the retry also fails, call `wd.RestoreOne(id)` on the watchdog and emit a structured event. This requires first exporting `RestoreOne` on the watchdog, which today only exposes `Restore()` (all-entries) and an unexported `restoreOne(entry)`.

## Care level
MEDIUM. Touches the controller write path AND adds a new exported watchdog method — both safety-adjacent. The retry+restore path must preserve every existing safety invariant (clamp, pwm_enable save/restore, allow_stop gate, ENOENT/EIO skip). The new RestoreOne must obey the same per-fan panic-recovery envelope the existing Restore loop uses.

## Context you should read first

- `internal/watchdog/watchdog.go` — study the existing `Restore()` loop and unexported `restoreOne(entry)`. New exported `RestoreOne(pwmPath string)` must look up the entry by pwmPath (same matching rule as Deregister: most-recent match) and dispatch via the same `restoreOne(entry)` method to inherit the panic envelope.
- `internal/controller/controller.go` — find the PWM write site (backend.Write call inside tick()). Current error handling logs + returns; new handling must retry once at 50ms, then on second failure call `wd.RestoreOne(pwmPath)` and emit a structured slog.Error event.
- `internal/watchdog/safety_test.go` if present, or `internal/watchdog/watchdog_test.go` — pattern-match the test style for adding a test on RestoreOne.
- `internal/controller/safety_test.go` — existing write-failure tests will need to survive unchanged. Your retry path must not break them.

## What to do

1. **Add `RestoreOne(pwmPath string)` to `internal/watchdog/watchdog.go`:**
   - Takes a single pwmPath argument.
   - Finds the most-recent matching entry (same loop-backwards pattern as Deregister).
   - If no match, no-op (do NOT error; a controller whose fan was deregistered concurrently shouldn't panic).
   - If matched, calls `w.restoreOne(entry)` — inheriting the existing per-entry panic-recovery envelope.
   - Holds the mutex only long enough to copy the matched entry; release before calling restoreOne so restoreOne can take it again if needed.

2. **Wire the retry + RestoreOne path in `internal/controller/controller.go`:**
   - At the Write call site inside tick(), on error:
     - Log a WARN with `event="write_retry"` and the error.
     - Sleep 50ms (use `time.Sleep`; do NOT introduce a ticker or goroutine).
     - Retry the Write once.
     - If the retry also fails:
       - Emit a structured `slog.Error` with `event="write_failed_restore_triggered"`, pwmPath, fanName, both error values.
       - Call `wd.RestoreOne(pwmPath)`.
       - Return (skip the rest of tick() for this cycle; next tick resumes normally).
   - If the retry succeeds:
     - Emit a structured `slog.Info` with `event="write_retry_succeeded"`.
     - Continue tick() as normal.

3. **Add a test that proves the retry+restore path:**
   - In `internal/watchdog/watchdog_test.go` (or create if none exists for this scope): `TestRestoreOne_MatchesMostRecent` and `TestRestoreOne_NoMatchIsNoOp`.
   - In `internal/controller/controller_test.go`: test the fake backend returning error twice triggers RestoreOne (use an existing fake pattern if present, or a minimal fake). The test should assert:
     - RestoreOne was called exactly once for the failing pwmPath.
     - A structured `write_failed_restore_triggered` event was emitted.

4. **Verify:**
   - `CGO_ENABLED=0 go build ./...` — clean
   - `go test -race -count=1 ./internal/controller/... ./internal/watchdog/...` — pass
   - `go vet ./...` — clean
   - `gofmt -l internal/controller/controller.go internal/watchdog/watchdog.go` — empty
   - Existing safety subtests under `.claude/rules/hwmon-safety.md` still pass.

5. **CHANGELOG:** one line under `## Unreleased / ### Added` referencing P1-HOT-02.

## Definition of done

- `internal/watchdog/watchdog.go` exports `RestoreOne(pwmPath string)`.
- Controller's PWM write site retries once at 50ms on error.
- On second failure: RestoreOne is called, a structured event is emitted, tick() returns cleanly.
- All prior controller safety subtests still pass.
- New tests prove both new behaviours (watchdog matching + controller retry/restore path).
- No change to existing API signatures other than adding the new RestoreOne method.

## Out of scope for this task

- Refactoring the existing `restoreOne(entry)` method.
- Changing `Restore()` loop semantics.
- Changing PWM clamping, pwm_enable save/restore, allow_stop, or panic-mode logic.
- Adding new fan backends.
- Bumping any Go/linter version or adding dependencies.
- Retries with more than one attempt (spec is explicitly "retry once").

## Branch and PR

- Work on branch: `claude/P1-HOT-02-symmetric-write-retry`
- Commit style: conventional commits (`feat(controller):` or `feat(watchdog):` depending on which change you lead with — the controller is the user-visible change so lead with `feat(controller):`).
- Open a draft PR on completion with title: `feat(controller): symmetric retry+RestoreOne on PWM write failure (P1-HOT-02)`
- PR description must include: the goal verbatim, bulleted files-touched list, "How I verified" section showing test output, link back to task ID: P1-HOT-02.

## Constraints

- Do not touch files outside: `internal/controller/controller.go`, `internal/controller/controller_test.go`, `internal/watchdog/watchdog.go`, `internal/watchdog/watchdog_test.go` (or a new test file in that package), `CHANGELOG.md`.
- Do not add new direct dependencies.
- Keep the main binary `CGO_ENABLED=0` compatible.
- Preserve all existing safety guarantees.
- If blocked, push WIP, open draft PR with `[BLOCKED]` prefix, write a `Blocker` section in the description.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: second-guessing you had while working
- FOLLOWUPS: work you noticed that isn't in scope
