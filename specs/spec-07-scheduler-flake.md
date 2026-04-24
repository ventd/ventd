# Spec 07 — TestScheduler_TickSwitchesProfileAtBoundary flake

**Masterplan IDs this covers:** CI hygiene for v0.4.1. Root-causes the Fedora-matrix flake surfaced during v0.4.0 release cycle.
**Target release:** v0.4.1 (co-ships with spec-06).
**Estimated session cost:** Sonnet, 1 session, $3–6 once flake is reproduced. Reproduction is the long pole; fix is usually <50 LOC.
**Dependencies already green:** Scheduler + profile code exists from prior phases.

---

## Why this ships v0.4.1

`TestScheduler_TickSwitchesProfileAtBoundary` flakes intermittently on the Fedora CI matrix (observed during v0.4.0 release cycle, exact frequency unknown — no flake tracker yet). Green CI is the pre-condition for trusting the tag-candidate SHA before cutting a release. A known-flaky test on a release-gating matrix row means every green run could be noise.

Project rule: `A feature cannot regress a tier it previously passed` (`ventdtestmasterplan.md §1`). A flake in Tier 2 on Fedora violates this spirit even if the test is "only occasionally" failing.

## Scope — what this session produces

One PR. Three phases, all in the same session.

### Phase A — Reproduction (the actual work)

**Files touched:** none in code; run-log captured.

Before any code changes, reproduce the flake deterministically. Flakes in scheduler tests have 4 common causes in Go:

1. **Wall-clock dependency** — test uses `time.Now()` or `time.Sleep` instead of an injected clock. Fedora runners may have different scheduler latency than Ubuntu/Alpine.
2. **Goroutine race on shared state** — `go test -race` passes locally but masks a race that only manifests under Fedora's kernel scheduling profile.
3. **Channel buffering assumption** — test assumes a buffered channel delivery ordering that holds on most kernels but not all.
4. **`t.Parallel()` interaction** — running with siblings causes resource contention (goroutine count, GC pressure) that stretches a boundary check past its implicit deadline.

Reproduction steps (run from a fresh CC session):

```bash
# Try the obvious failure modes in order:
cd ~/ventd

# 1. Stress loop locally — 100 runs, detect any failure.
go test -race -run TestScheduler_TickSwitchesProfileAtBoundary \
  -count=100 ./internal/scheduler/... 2>&1 | tee /tmp/flake-local.log

# 2. If (1) is clean, reproduce in Fedora container matching CI.
docker run --rm -v $PWD:/work -w /work fedora:latest bash -c '
  dnf install -y golang make
  go test -race -run TestScheduler_TickSwitchesProfileAtBoundary \
    -count=100 ./internal/scheduler/... 2>&1
' | tee /tmp/flake-fedora.log

# 3. If (2) fires, use -cpu=1 and -cpu=8 to expose scheduling sensitivity.
docker run --rm -v $PWD:/work -w /work fedora:latest bash -c '
  go test -race -cpu=1 -count=100 -run TestScheduler_... ./internal/scheduler/...
  go test -race -cpu=8 -count=100 -run TestScheduler_... ./internal/scheduler/...
'
```

**Stopping condition for Phase A:** a reliable, deterministic repro under <60s. If the flake doesn't reproduce in 500 runs across 3 configurations, the test is probably not *flaky* but *wrong on Fedora only* — and fix changes accordingly. Commit your findings into a note before proceeding to Phase B.

### Phase B — Fix

**Files (modified — depends on root cause):**
- `internal/scheduler/scheduler.go` — if the bug is wall-clock dependency
- `internal/scheduler/scheduler_test.go` — if the bug is test-side (likely)
- `internal/testfixture/faketime/*` — extend if test needs clock injection

**Fix patterns, matched to Phase A diagnosis:**

- **Wall-clock dependency:** inject a clock via existing `faketime` fixture (already in `ventdtestmasterplan.md §3` fixture list). Test becomes deterministic.
- **Goroutine race:** add sync.WaitGroup or channel coordination; `-race` should have caught this, so more likely a happens-before violation that's latent.
- **Channel buffering:** make the test's channel unbuffered OR add an explicit synchronisation point before the assertion.
- **`t.Parallel()`:** remove it from this test specifically (document why in a comment); the test is cheap enough to run serial.

**Invariant binding — new rule:**

Only add a rule file if the root cause is an invariant that deserves protection. Example: if the root cause is "scheduler transitions at profile boundary require monotonic clock, not wall clock," that's a RULE-SCHED-01 worth binding. If the root cause is a test-side fix (race in the test harness, not the code), no rule is needed — just fix the test.

**Default decision: no new rule file unless a production-code invariant emerges.** Keeps scope tight.

### Phase C — Regression test

**File (new if rule added):**
- `.claude/rules/scheduler-stability.md`

**Pattern:** regression test named `TestRegression_FedoraFlake_<short-description>` per `ventdtestmasterplan.md §11`. The reproduction harness from Phase A becomes the regression test body.

## Definition of done

- [ ] Phase A: flake reproduced locally or in Fedora container with <60s wall time per failure, commit the repro script to `/tmp/` note — do not commit to repo.
- [ ] Phase B: fix applied; `go test -race -count=1000 -run TestScheduler_TickSwitchesProfileAtBoundary ./internal/scheduler/...` passes.
- [ ] Phase C: regression test added; rule file added only if production invariant emerged.
- [ ] CI green on Fedora matrix row — re-run the matrix 3× to confirm stability before merge.
- [ ] CHANGELOG v0.4.1 entry mentions the fix under "Fixed."

## Explicit non-goals

- No broader scheduler refactor. The bug is one test; the fix stays scoped.
- No new flake-tracking infrastructure. If this is the first time flake-tracking comes up as a need, it's a v0.5.0 conversation (separate spec).
- No `t.Parallel()` crusade. Serial-only decision applies to this test only.
- No migration to a new test framework. `testing` stdlib is fine.

## Red flags — stop and page me

- Phase A runs >30 minutes with no reproduction → flake is rarer than expected; pivot to "analyse the CI failure logs" instead of "reproduce locally."
- CC proposes adding retry-on-flake logic to the test (`for i := 0; i < 5; i++ { ... }`) → absolutely not; that hides the bug.
- CC suggests the fix is "add `time.Sleep(100ms)` before the assertion" → that's not a fix, it's a different flake. Reject and re-root-cause.
- Fix requires refactoring >100 LOC of scheduler → scope creep; stop and re-spec in v0.5.0.
- Fix touches `internal/scheduler/scheduler.go` signature (public API) → out of scope for v0.4.1 patch release.

## CC session prompt — copy/paste this

```
Read /home/claude/specs/spec-07-scheduler-flake.md end to end. Then read:
- internal/scheduler/scheduler.go
- internal/scheduler/scheduler_test.go (find TestScheduler_TickSwitchesProfileAtBoundary)
- internal/testfixture/faketime/ (if it exists; see ventdtestmasterplan.md §3)

This is a Phase A → B → C spec. Phase A is the actual work. Do Phase A first
and commit a note of findings BEFORE writing any code. If Phase A doesn't
reproduce the flake in 500 runs across 3 configurations, stop and surface —
the bug may require CI-side reproduction I can provide.

Phase A workflow:
  cd ~/ventd
  go test -race -run TestScheduler_TickSwitchesProfileAtBoundary \
    -count=500 ./internal/scheduler/... 2>&1 | tee /tmp/flake-local.log
  # If clean locally:
  docker run --rm -v $PWD:/work -w /work fedora:latest bash -c '
    dnf install -y golang
    go test -race -run TestScheduler_TickSwitchesProfileAtBoundary \
      -count=500 ./internal/scheduler/... 2>&1
  ' | tee /tmp/flake-fedora.log

Commit at boundaries:
- phase-a: no commit, just a note in /tmp describing the reproduction
- fix(scheduler): <one-line description of root cause and fix>
- test(scheduler): regression test for TestRegression_FedoraFlake_*
- docs(rules): only if a new invariant emerged

Success condition:
  go test -race -count=1000 -run TestScheduler_TickSwitchesProfileAtBoundary ./internal/scheduler/...
  # Zero failures.

Stop and surface if:
- Flake does not reproduce after 500 runs in any configuration.
- Root cause is outside internal/scheduler/ (e.g., in internal/controller/).
- Fix requires changing public API or shared types.
- Fix requires a new dependency.
- The test was masking a real race that the fix exposes — that's a bigger bug.
```

## Why this is cheap (conditionally)

- Single test, single package, bounded scope.
- Reproduction is the long pole; once it reproduces, the fix is usually obvious.
- Go's `-count=N` flag is a free stress-tester.
- If reproduction fails, we learn something valuable (the bug is CI-environment-specific) at low token cost.

## What to do if Phase A never reproduces

Escalation path — not in scope for the first session, but documented for continuity:

1. Pull last 10 CI failure logs from GitHub Actions for the Fedora matrix row.
2. Check if failures correlate with specific git SHAs or test-order seeds (`-shuffle=on`).
3. If failures are purely nondeterministic under identical input, the bug is in the test's assumption about goroutine scheduling — re-read the test with that lens.
4. Worst case: mark the test `t.Skip("flaky on Fedora CI, tracked in issue #XXX")` with an explicit tracking issue. This is a *last-resort* skip, not a fix, and must be gated on an issue with a reproduction plan.

Document the escalation decision in `/tmp/flake-fedora.md` before taking it.
