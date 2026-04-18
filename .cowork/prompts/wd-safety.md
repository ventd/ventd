# wd-safety — T-WD-01 watchdog safety invariants

Alias: `wd-safety`. Opus 4.7. Depends on: P1-HAL-01 (merged c049a0f).

## Goal

Bring `internal/watchdog` coverage from ~23% to >80% via rule-bound invariant tests. Introduce `.claude/rules/watchdog-safety.md` and bind every rule 1:1 to a subtest in a new `internal/watchdog/safety_test.go` following the hwmon-safety pattern established in `internal/controller/safety_test.go`.

## Task ID: T-WD-01 (testplan §17, priority #3 per §19)

## What to do

1. Read: `internal/watchdog/watchdog.go`, `internal/watchdog/watchdog_test.go`, `internal/controller/safety_test.go`, `.claude/rules/hwmon-safety.md`, `internal/hal/backend.go`, `internal/hal/hwmon/backend.go`, `internal/hal/nvml/backend.go`.

2. Create `.claude/rules/watchdog-safety.md` with these 7 invariants (each exactly one `## RULE-<ID>: <one-line>` heading + 2-4 sentence paragraph + one `Bound: <file>:<subtest>` line):
   - RULE-WD-RESTORE-EXIT: Every documented exit path (Restore, RestoreOne, RestoreAll, ctx cancel, SIGTERM path, panic) restores `pwm_enable=1` for every registered channel.
   - RULE-WD-RESTORE-PANIC: Panic during restore of one channel does not prevent restore of remaining channels.
   - RULE-WD-FALLBACK-MISSING-PWMENABLE: If a registered channel's pwm_enable file is missing at restore time, watchdog logs the error but continues with remaining channels (no panic, no early return).
   - RULE-WD-NVIDIA-RESET: NVML channels have `nvmlDeviceSetDefaultAutoBoostedClocksEnabled` (or the v3 manufacturer-default equivalent) called on restore, not a zero write.
   - RULE-WD-RPM-TARGET: Channels registered as rpm_target (not pwm) write `maxRPM` on restore, not raw pwm.
   - RULE-WD-DEREGISTER: Deregistering an unregistered channel is a no-op, not a panic. Double-deregister is a no-op.
   - RULE-WD-REGISTER-IDEMPOTENT: Re-registering a channel with the same id replaces the prior entry; old pre-daemon pwm_enable captured once at first Register is preserved (not overwritten by subsequent Register).

3. Create `internal/watchdog/safety_test.go` with `TestWDSafety_Invariants` containing seven `t.Run("wd_restore_exit", ...)` etc. subtests, exact names matching the `Bound:` lines. Use `fakehwmon` from `internal/testfixture/fakehwmon` for the hwmon backend; use a small inline stub for NVML (real fakenvml is out of scope for this task).

4. Run `go run ./tools/rulelint` to confirm all 7 rules green.

5. CHANGELOG entry under `[Unreleased]/Tests`:
   - `- test: bind internal/watchdog safety invariants (#T-WD-01)`

## Definition of done

- All 7 subtests pass under `-race -count=1`.
- `go run ./tools/rulelint` reports `ok: 7 rule(s), 7 bound(s) verified` (or more if existing rules exist).
- Coverage report `go test -cover ./internal/watchdog/...` shows ≥80% statement coverage.
- No changes to `internal/watchdog/watchdog.go` production logic. If a rule cannot be verified without a production change, STOP and note it in PR description — the test task does NOT change production.

## Out of scope

- Tests outside this task's scope per testplan §18 R20 binding.
- Any change to `internal/watchdog/watchdog.go` source (test-only PR).
- New dependencies.
- Real NVML fixture.

## Branch and PR

- Branch: `claude/WD-safety-invariants-rF9xX`
- Title: `test(wd): bind watchdog safety invariants (T-WD-01)`
- Draft PR; conventional commits.

## Constraints

- `CGO_ENABLED=0` compatible.
- No production edits (test-only).
- Must pass the new `meta-lint` workflow (rulelint CI job).

## Reporting

Standard: STATUS, PR URL, SUMMARY ≤ 200 words, CONCERNS, FOLLOWUPS.

## Model

Opus 4.7 — safety-critical invariant binding, new rule file.
