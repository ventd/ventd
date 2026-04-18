# fix-289-scheduler-race

You are Claude Code. Fix the scheduler↔manual-override race that
Cassidy filed as issue #289 concern #1, and that is actively failing
`TestScheduler_ManualOverrideStaysUntilTransition` on Ubuntu amd64 for
every Wave 1 PR.

This is the critical-path fix — all four Phase 2 Wave 1 PRs (#277,
#281, #282, #285) are blocked on this.

## Root cause (from diag-wave1-ci-failures)

In `internal/web/profiles.go:handleProfileActive`, two non-atomic
operations happen in wrong order:

```go
next, err := s.applyProfile(req.Name)       // step A: atomic.Store
if err != nil { ... return }
s.schedState.markManualOverride()           // step B: sets flag
```

If `runScheduler` tick fires between A and B, the scheduler sees the
new cfg.ActiveProfile but manualOverride is still false — so the
scheduler clobbers the operator's pick with the scheduled winner.

Failure signature: `TestScheduler_ManualOverrideStaysUntilTransition`
fails with `"post-transition: active = 'daytime' want silent"` on
Ubuntu amd64 (the only matrix lane running `go test -race`).

## Fix

Swap the order. Set override flag BEFORE the cfg swap. This is a
2-line reorder plus a comment explaining why.

## Context to read first

- `internal/web/profiles.go` — the `handleProfileActive` function
- `internal/web/schedule.go` — `runScheduler`, `scheduleTick`, and
  `scheduleState.observe` / `markManualOverride` (to confirm the
  override-set + cfg-Load ordering contract)
- `internal/web/schedule_test.go` — the failing test
  `TestScheduler_ManualOverrideStaysUntilTransition`

## What to do

1. Open `internal/web/profiles.go`.
2. Locate `handleProfileActive` (search for the function name).
3. Swap the order: move `s.schedState.markManualOverride()` to BEFORE
   the `s.applyProfile(req.Name)` call. Rationale comment:

   ```go
   // Set override flag before the cfg swap so a scheduler tick
   // firing between the two operations sees override=true and
   // suppresses its winner. Reversed order (original) had a race
   // window (issue #289 concern 1).
   s.schedState.markManualOverride()
   next, err := s.applyProfile(req.Name)
   if err != nil {
       // Override was set speculatively; it will clear at the next
       // scheduled transition, which is no worse than pre-refactor.
       http.Error(w, err.Error(), http.StatusBadRequest)
       return
   }
   ```

4. Check: does any call to `markManualOverride` in the same function
   need to stay put (e.g., a second one for undo semantics)? If yes,
   leave it; only reorder the single pre-applyProfile call.

5. Build + test:
   ```
   CGO_ENABLED=0 go build ./...
   go test -race -count=1 ./internal/web/...
   go test -race -count=1 ./...
   gofmt -l .
   ```

6. Commit:
   ```
   feat(web): fix scheduler↔manual-override race (fixes #289 concern 1)

   handleProfileActive had a race window where a scheduler tick firing
   between applyProfile and markManualOverride would see the new cfg
   but not the override flag, and could clobber the operator's pick
   with the scheduled winner.

   Reorder: set override flag before the cfg swap. Scheduler tick in
   the window now observes override=true and suppresses.

   Cassidy audit: issue #289 concern 1.
   Diagnosis: scheduler test was failing on Ubuntu amd64 for every
   Phase 2 Wave 1 PR after the rebase onto main.

   Closes #289 (concern 1 only; concerns 2 and 3 remain — option (c)
   documentation-only for concern 2, deferred refactor for concern 3).
   ```

7. Open PR:
   - Branch: `claude/fix-289-scheduler-race-<rand5>`
   - Title: `fix(web): scheduler↔manual-override race (fixes #289 concern 1)`
   - Draft: false
   - Body: include the commit message + a note that this unblocks
     Wave 1 CI on Ubuntu amd64

## Scope

- Allowlist: `internal/web/profiles.go`, `CHANGELOG.md`
- Do NOT touch `internal/web/schedule.go` (concern 2 is documentation-only,
  concern 3 is a separate refactor)
- Do NOT add a new test (the existing
  `TestScheduler_ManualOverrideStaysUntilTransition` will pass after
  the fix; that's the regression test)
- Add one CHANGELOG entry under `## Unreleased` / `### Fixed`

## Out of scope

- Concerns 2 and 3 from #289 (separate follow-ups)
- Persisting manualOverride across daemon restart (concern 2 option a)
- Any `mutateConfig` refactor (concern 3)

## Model

Sonnet 4.6. Two-line production change + one CHANGELOG line + one
commit-message block. No safety surface touched, no new rules.

## Reporting

- STATUS: done | partial | blocked
- PR: <url>
- SCHEDULER_TEST_RESULT: pass | fail + details
- FULL_TEST_RESULT: pass | fail + details
- SUMMARY: ≤100 words

## Time budget

20 minutes.
