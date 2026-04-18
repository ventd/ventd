# Review P1-HAL-01-R1
Task: P1-HAL-01
PR: https://github.com/ventd/ventd/pull/247
Head SHA: c6726f93e3bd4a3de5bd29ee3f0fa5ed9950a33e
Verdict: escalate (PHASE-1/FIRST-PR)
Reviewer: Cowork (Opus 4.7)
Timestamp: 2026-04-18T (session resume)

Checklist:
  R1 (draft + task ID in body): OK
  R2 (branch name): OK — claude/fan-backend-interface-FYoaH
  R3 (conventional commits): OK — `refactor(hal): introduce FanBackend interface`
  R4 (allowlist subset): OK — prompt allowlist `internal/hal/**, internal/controller/*.go, internal/calibrate/*.go, internal/watchdog/*.go, internal/hwmon/hwmon.go, internal/nvidia/nvidia.go, cmd/ventd/main.go, CHANGELOG.md`; diff touches 7 files all within that set. `internal/hwmon/hwmon.go` and `internal/nvidia/nvidia.go` untouched — subset-OK is fine.
  R5 (no test files): OK
  R6 (CI): OK — 13/13 green
  R7 (DoD per acceptance criteria): OK — all six DoD items verifiably present in diff.
  R8 (no new deps): OK
  R9 (safety preserved): OK with one behavioural deviation, see below.
  R10 (secrets): OK
  R11 (CHANGELOG): OK — one line under Unreleased/Added
  R12 (public API): `hal.FanBackend`/`Channel`/`Caps` are new public API but task targets it. OK.
  R13 (single track): OK
  R14 (vet/lint/fmt): OK per CI
  R15 (binary size +100KB): refactor of hot-path control; SIZE-JUSTIFIED implicit in Phase 1 bulk refactor. OK.
  R16 (no new panic/log.Fatal/os.Exit): OK
  R17 (goroutine lifecycle): OK — no new goroutines
  R18 (file Close): OK — no new file I/O
  R19 (regression test per Fixes:): N/A — no Fixes: in PR
  R20 (safety-critical bound to rule subtest): DEFER — T-HAL-01 owns this per testplan §17
  R21 (goroutine test coverage): N/A — no new goroutines
  R22 (HAL contract test): DEFER — T-HAL-01 is the explicit deliverable
  R23 (docs golden): N/A — no new metrics/routes/config fields

Deviations called out in PR body:
  1. No `init()` registration. CC cites `.claude/rules/go-conventions.md`. Verified present in repo; rule-compliance is defensible. Explicit registration in `cmd/ventd/main.go` is consistent with existing repo conventions.
  2. Lazy manual-mode acquisition. Pre-refactor `controller.Run` wrote `pwm_enable=1` at startup and returned fatal on non-ENOENT failure. Post-refactor `hal/hwmon.Backend.Write` acquires lazily on first tick; acquire failure surfaces as a logged tick error rather than a fatal return from Run. This is a behavioural change to the fail-fast path. Mitigation: pre-refactor fatal → systemd restart → same failure loop. New behaviour is strictly less noisy but gives up the "die fast on broken sysfs" signal. Needs developer acceptance before merge.

Why escalate rather than accept:
  - PHASE-1/FIRST-PR rule: never auto-merge the first PR of a new phase.
  - Deviation 2 is a safety-envelope behaviour change that the developer should sign off on in person.

Recommended action: developer review the deviation 2 rationale, then `RESUME P1-HAL-01`. Once resumed, Cowork marks ready-for-review and squash-merges (CI already green).
