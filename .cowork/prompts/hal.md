# CC prompt — P1-HAL-01 (hal)

This is the full specification for P1-HAL-01 as dispatched via the `hal` alias. Preserved on cowork/state for respinability after the original session (#247) completed and escalated.

## Task
ID: P1-HAL-01
Track: HAL
Goal: put the existing hwmon and NVML fan code behind a shared `FanBackend` interface so Phase 2 backends (IPMI, liquidctl, cros_ec, pwmsys, asahi) can slot in without touching the controller or the watchdog. Behaviour-preserving refactor.

## Context you should read first
- `internal/controller/controller.go`
- `internal/watchdog/watchdog.go`
- `internal/hwmon/hwmon.go`
- `internal/nvidia/nvidia.go`
- `cmd/ventd/main.go`
- `.claude/rules/hwmon-safety.md`
- `.claude/rules/go-conventions.md`
- `ventdmasterplan.mkd` §8 P1-HAL-01 entry

## What to do
1. Create `internal/hal/backend.go` defining the `FanBackend` interface plus `Channel`, `Reading`, `Caps`, and `ChannelRole` types.
2. Create `internal/hal/registry.go` with `Register`, `Enumerate`, `Resolve`, `Backend`, `Reset`.
3. Create `internal/hal/hwmon/backend.go` implementing `FanBackend` by wrapping `internal/hwmon` (never re-implement sysfs primitives).
4. Create `internal/hal/nvml/backend.go` implementing `FanBackend` by wrapping `internal/nvidia`.
5. Refactor `internal/controller/controller.go` to hold a `FanBackend` ref and dispatch Writes through it. Remove direct sysfs / NVML writes from the hot path. Remove `pwm_enable` references from the control path.
6. Refactor `internal/watchdog/watchdog.go` so `restoreOne` delegates to the hwmon / nvml backends. `entry` fields + Register/Deregister signatures unchanged so existing restore-matrix tests still pin byte-level behaviour.
7. Wire backend registration in `cmd/ventd/main.go`.
8. Add one line to `CHANGELOG.md` under `## [Unreleased]` / `### Added`.

## Definition of done
- `CGO_ENABLED=0 go build ./...` clean.
- `CGO_ENABLED=0 go vet ./...` clean.
- `gofmt -l` on touched files empty.
- `golangci-lint run --timeout=5m` clean.
- `go test -race ./internal/controller/... ./internal/watchdog/... ./internal/hwmon/... ./internal/calibrate/... ./internal/nvidia/... ./internal/hal/...` — pass.
- `grep -r "pwm_enable" internal/controller/ --include="*.go" --exclude="*_test.go"` empty.
- CHANGELOG has one-line entry.
- PR is draft, titled `refactor(hal): FanBackend interface (P1-HAL-01)`.

## Out of scope for this task
- Tests outside the scope this task targets per the testplan catalogue. Test coverage for `internal/hal/**` is T-HAL-01, a separate task. This PR adds no test files.
- Routing `internal/calibrate/calibrate.go` through the HAL — calibration sweep has its own mode-file discipline; out of scope here.
- Phase 2 backends.

## Branch and PR
- Branch: claude/fan-backend-interface-<rand5>
- Commit style: conventional commits
- Open draft PR on completion with title: `refactor(hal): FanBackend interface (P1-HAL-01)`
- PR description must include: the goal verbatim, bulleted files-touched list, "How I verified" section, link back to task ID: P1-HAL-01.

## Constraints
- Do not touch files outside: `internal/hal/**`, `internal/controller/*.go`, `internal/calibrate/*.go`, `internal/watchdog/*.go`, `internal/hwmon/hwmon.go`, `internal/nvidia/nvidia.go`, `cmd/ventd/main.go`, `CHANGELOG.md`.
- No new direct dependencies.
- Keep the main binary CGO_ENABLED=0 compatible.
- Preserve all existing safety guarantees (watchdog restore, PWM clamping, pwm_enable save/restore semantics — they move into the backend but the observable behaviour must not shift).

## Reporting
On completion:
- STATUS: done | partial | blocked
- PR: <url>
- SUMMARY: <= 200 words
- CONCERNS: any second-guessing
- FOLLOWUPS: work noticed but out of scope

## Model
Opus 4.7 (HAL is safety-critical).
