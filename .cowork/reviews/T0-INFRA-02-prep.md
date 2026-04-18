# T0-INFRA-02 Review Prep

Staged while T0-INFRA-02 (Sonnet 4.6) is in-flight so that when CC
reports, the §5 + §18 review pass is zero-latency.

## Task recap

- Track: INFRA
- Task: T0-INFRA-02
- Model: Claude Sonnet 4.6 (developer override from Haiku)
- Branch: `claude/INFRA-fakehwmon-b5e7a`
- Base: `main` at merge commit `e0dcef6` (T0-INFRA-01 merge)
- Goal: real fakehwmon fixture + migrate ≥3 existing tests
- Allowlist (per CC prompt, expanded from testplan §17 entry):
  * `internal/testfixture/fakehwmon/**` (new, primary)
  * `internal/controller/safety_test.go` (migration)
  * `internal/watchdog/restore_matrix_test.go` (migration)
  * `internal/calibrate/detect_test.go` (migration)
  * `CHANGELOG.md` (Unreleased/Added entry)

## Specific assertions (before generic R1-R23)

### Fixture shape (R7, R17)

- Constructor signature matches the T0-INFRA-01 skeleton: accepts
  `*testing.T`, returns `*Fakehwmon` or equivalent, calls
  `t.Helper()` and `t.Cleanup()`.
- Uses `t.TempDir()` for its sysfs root — no process-wide state,
  no `os.MkdirTemp` without registered cleanup.
- Exposes a stable public surface: methods to set fan RPM, PWM
  floor/ceiling, chip `name`, and to inject ENOENT / EACCES / EIO
  on specific reads + writes. Naming consistent with the skeleton
  (`SetRPM`, `InjectReadError`, `InjectWriteError`, etc. — verify
  exact names against skeleton).
- Constructor takes zero options by default and panics `t.Fatalf`
  on filesystem-setup failure; no error return.
- Fixture files under `internal/testfixture/fakehwmon/` have
  `fakehwmon_test.go` that exercises its own happy path and each
  injected error path.

### Test migrations (R7, R17)

- Each migrated test stops constructing ad-hoc hwmon roots by hand
  (check for disappearance of local helpers like `writeFile(t, path,
  "value")` or inline `os.WriteFile` chains inside `_test.go`).
- Each migrated test uses the fixture for its sysfs setup; the
  test body reads clean.
- Rule-to-subtest bindings in `safety_test.go` unchanged — every
  rule listed in `.claude/rules/hwmon-safety.md` still has a
  matching subtest name. A silent rename here is a regression
  (R17).
- The restore_matrix in `watchdog/restore_matrix_test.go` must
  retain identical rows and cell expectations; only the scaffolding
  is allowed to change.
- The detect matrix in `calibrate/detect_test.go` must retain
  identical chip-name ↔ module mappings; only the scaffolding is
  allowed to change.

### Known hazards

- Stale base: T0-INFRA-02 branched from `e0dcef6` (T0-INFRA-01
  merge). T0-META-03 (#240) merging to main before T0-INFRA-02
  adds a one-line `.github/pull_request_template.md` change. This
  does NOT touch CHANGELOG.md, so no conflict on Sonnet's
  CHANGELOG edit. Rebase is optional; only required if the PR
  template change affects any CI that gates the PR (it does not).
- `go vet ./...` on a freshly added `internal/testfixture/fakehwmon/`
  package must be clean. Verify the CC build report includes the
  `go vet` line.
- `go test -race ./...` on the migrated tests must pass.
- gofmt: the CC reporting contract requires a gofmt verification
  line — check the report for it; reject if absent.
- Regression-test checkbox (new on main post-T0-META-03 merge):
  T0-INFRA-02 is not closing a `type: bug` issue, so R19 is
  vacuous. If the PR body doesn't list any `Fixes: #N`, the
  checkbox is moot.

### CC reporting contract recheck

The prompt required:
  * BEFORE-SHA and AFTER-SHA of the branch tip
  * POST-PUSH `git log --oneline` showing the new commit on top
    of the base
  * `go build ./...` output (full, not truncated)
  * `go test -race ./...` output (full, not truncated)
  * `gofmt -l .` output (should be empty)
  * Self-assessment of each R1-R23 row — free to say 'n/a' but
    must say something per row

If any required section is missing from the CC report, automatic
R2 `revise` verdict (phantom-push guard or under-reporting).

## R1-R23 fallback checklist

- R1: PR body has task ID, branch named per convention
- R2: branch tip matches CC-reported AFTER-SHA and single new
  commit since base
- R3: conventional-commits subject; scope indicates fixture/test
- R4: only allowlist files touched (4 paths + CHANGELOG); any
  stray file is R4 fail
- R5: tests added (the fixture's own test file) + tests modified
  (three migrations)
- R6: CI all-green on the PR head; no flake exceptions without
  CI-FLAKE-CHECK
- R7: content matches the task description (see fixture shape +
  test migration assertions above)
- R8: no new dependencies in go.mod
- R9: no new calls to real hwmon paths under `/sys/...` outside
  fixtures
- R10: no secrets, no tokens
- R11: CHANGELOG entry present under `## [Unreleased]` / `### Added`
  naming T0-INFRA-02; prefix `test:`; references the fixture and
  mentions migration count
- R12: no public API change outside `internal/testfixture/**`
- R13: single track (INFRA)
- R14: binary sizes: n/a (test-only)
- R15: compat: n/a (test-only)
- R16: no goroutine adds outside test scope
- R17: rule bindings intact (see safety_test.go assertion above)
- R18: no new I/O outside fixture boundary
- R19: no bug-issue fixes claimed — vacuous
- R20: no rule file edits
- R21: no masterplan edits
- R22: no testplan edits
- R23: no cross-repo edits

## First-PR gates

- PHASE-T0/FIRST-PR: already cleared by T0-INFRA-01
- PHASE-T0-INFRA-SUBSTANTIVE/FIRST-PR: not a declared gate, but
  T0-INFRA-02 is the first real fixture implementation (vs
  skeleton). Apply an accept-pending hold unless the developer
  has pre-cleared it, so a human eyeball lands on the first
  non-skeleton fixture before it sets the pattern for T0-INFRA-03
  (faketime).
