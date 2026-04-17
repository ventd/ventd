You are Claude Code, working on the ventd repository.

## Task
ID: P1-HAL-02
Track: HAL
Goal: Migrate the calibration subsystem to drive fans via the `hal.FanBackend` interface introduced in P1-HAL-01, eliminating direct hwmon/NVML imports from the calibrate package.

## Care level
This task is safety-critical: calibration sweeps the full PWM range; wrong abstraction here can stall fans under load. Apply extra scrutiny to the safety-invariant preservation steps 3 and 4, and verify the full `internal/controller/safety_test.go` and `internal/calibrate/*_test.go` suites pass under `-race` before pushing.

Do NOT abort on model-mismatch checks. The spawn-mcp dispatch path uses whatever model the cc-runner account is authenticated for. If that is Sonnet 4.6, the detailed prompt below is written so a careful Sonnet pass is sufficient. The model policy in CLAUDE.md is advisory, not a hard gate — the hard gate is the safety invariants in the Constraints section and the test suite.

## Context you should read first

- `internal/hal/backend.go` — the `FanBackend` / `Channel` / `Reading` / `Caps` contract.
- `internal/hal/registry.go` — how backends are registered and enumerated.
- `internal/hal/hwmon/` — the reference hwmon backend wrapping.
- `internal/hal/nvml/` — the NVML wrapping.
- `internal/calibrate/*.go` — the calibration subsystem as it stands, including `ZeroPWMSentinel` and the fingerprint fence.
- `internal/calibrate/safety_test.go` (if present) — current safety-test bindings. Do NOT modify these.
- `.claude/rules/hwmon-safety.md` — read-only reference; calibration must preserve these invariants.
- `cmd/ventd/main.go` — see how `hal.Registry` is constructed and how calibrate is wired in today (still direct-to-hwmon).

## What to do

1. Audit every place `internal/calibrate/*.go` currently reads or writes PWM/RPM directly via hwmon paths or NVML calls. List them in a comment block at the top of your main edit as your working checklist.

2. Replace each direct hwmon/NVML call with the equivalent `FanBackend.Read` / `FanBackend.Write` call, using the `hal.Channel` objects from `Registry.Enumerate`. The calibration driver takes a `*hal.Registry` (or similar) passed in from `cmd/ventd/main.go`.

3. Preserve `ZeroPWMSentinel` semantics exactly: any PWM=0 command that persists >2s must escalate to a safe floor. This logic stays at the calibrate layer — backend abstraction does not change safety policy.

4. Preserve the fingerprint fence: a calibration started on fan X of chip Y must not write to fan X of any other chip even if enumeration order changes mid-calibration.

5. Update `cmd/ventd/main.go` to construct calibrate with the registry instead of direct hwmon handles.

6. Ensure `CGO_ENABLED=0 go build ./...` still passes.

7. Run `go test -race -count=1 ./internal/calibrate/...` AND `go test -race -count=1 ./internal/controller/...` — both must pass. The controller safety suite is extra insurance that you haven't accidentally broken a cross-package invariant.

8. Run `go vet ./...` and `golangci-lint run ./internal/calibrate/... ./cmd/ventd/...` — both must be clean.

## Definition of done

- No `import "...hwmon"` or `import "...nvml"` remains in any file under `internal/calibrate/`.
- Calibration writes go through `FanBackend.Write`, reads through `FanBackend.Read`.
- `ZeroPWMSentinel` and fingerprint-fence tests still pass unchanged.
- `cmd/ventd/main.go` constructs the new calibrate driver with the registry.
- All existing `internal/calibrate/` AND `internal/controller/` tests pass under `-race`.
- `go vet` and `golangci-lint` clean.
- `CGO_ENABLED=0 go build ./cmd/ventd/` succeeds.
- Binary size delta ≤ +30 KB.

## Out of scope for this task

- Tests outside the scope this task targets per the testplan catalogue. This is a P-task; add only the tests documented in testplan §18 row R19 (regression test if a `Fixes:` issue is listed in the PR — none in scope here).
- Changing calibration algorithms, thresholds, or sweep patterns.
- Adding new safety invariants (that's T-CAL-01, which this unblocks).
- Touching NVML internals or hwmon parsing code.
- Adding new dependencies.
- Modifying `.claude/rules/hwmon-safety.md` or any rule file.

## Branch and PR

- Work on branch: `claude/P1-HAL-02-calibrate-via-backend`
- Commit style: conventional commits
- Open a draft PR on completion with title: `refactor(calibrate): drive via hal.FanBackend (P1-HAL-02)`
- PR description must include: the goal verbatim, bulleted files-touched list, "How I verified" section with test output, link back to task ID: P1-HAL-02.
- CHANGELOG.md `## Unreleased` entry under `### Changed`: one line referencing P1-HAL-02.

## Constraints

- Do not touch files outside: `internal/calibrate/**`, `cmd/ventd/main.go`, `CHANGELOG.md`.
- Do not add new direct dependencies.
- Keep the main binary `CGO_ENABLED=0` compatible.
- Preserve all existing safety guarantees (watchdog restore, PWM clamping, ZeroPWMSentinel).
- If blocked, push WIP, open draft PR with `[BLOCKED]` prefix, write a `Blocker` section in the description.

## Reporting

On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: second-guessing you had while working
- FOLLOWUPS: work you noticed that isn't in scope
