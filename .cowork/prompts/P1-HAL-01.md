# DRAFT — awaiting phase-advance decision + T0 completion gate

This prompt is pre-authored while Phase 1 is not yet dispatchable. Do not hand to a CC terminal until:
  1. Employer answers the phase-advance authority question (expected: option A — auto-advance at doc/state phase boundaries).
  2. Phase T0 is complete: T0-META-01, T0-META-02, T0-INFRA-03 all merged.
  3. Cowork has confirmed no in-flight work on tracks downstream of HAL.

---

You are Claude Code, working on the ventd repository.

## Task
ID: P1-HAL-01
Track: HAL
Goal: introduce the `FanBackend` interface under `internal/hal/` and move the existing hwmon + NVML implementations behind it, with zero observable behaviour change.

## Model
Claude Opus 4.7. This is a safety-critical refactor touching controller, watchdog, and calibrate — the strongest model is warranted.

## Context you MUST read first, carefully
- `internal/controller/controller.go` — the hot loop; all PWM writes go through here
- `internal/controller/safety_test.go` + `.claude/rules/hwmon-safety.md` — the safety invariants that must remain bit-identical after refactor
- `internal/watchdog/watchdog.go` (or the equivalent) — restore paths
- `internal/calibrate/*.go` — calibration engine
- `internal/hwmon/hwmon.go` — the existing hwmon implementation
- `internal/nvidia/nvidia.go` — the existing NVML implementation
- `cmd/ventd/main.go` — wire-up
- `internal/testfixture/fakehwmon/fakehwmon.go` — fixture that must continue to work against the new interface
- masterplan §1 (north star, safety non-negotiables) — particularly the 2-second safe-exit guarantee
- masterplan §8 P1-HAL-01 entry — your DoD

## What to do
1. **Create `internal/hal/backend.go`** defining:
   - `type FanBackend interface` with methods: `Name() string`, `Enumerate(ctx) ([]Channel, error)`, `Read(ctx, channelID) (Reading, error)`, `Write(ctx, channelID, pwm uint8) error`, `Restore(ctx, channelID) error`, `Caps(channelID) Caps`, `Close() error`.
   - `type Channel struct` carrying ID, Role (enum ChannelRole: CPUFan, CaseFan, GPUFan, Pump, AIOFan, RPMOnly, Unknown), ChipName, StablePath, and Caps.
   - `type Reading struct` carrying RPM uint32, PWM uint8, Timestamp time.Time, and a Flags bitfield for (StaleReading, SensorUnavailable, etc.).
   - `type Caps struct` carrying CanWrite bool, MinPWM uint8, MaxPWM uint8, SupportsStop bool, RestoreSemantics enum (FirmwareAuto, LastKnown, None).
   - `type ChannelRole uint8` with String() method.
   - All structs must marshal cleanly to JSON for /api/status compatibility.

2. **Create `internal/hal/registry.go`** with:
   - `func Register(name string, factory func() (FanBackend, error))`
   - `func Enumerate(ctx) []FanBackend` — instantiates each registered backend, returns the ones that successfully initialised
   - `func Resolve(channelRef string) (FanBackend, string, error)` — dispatches a "backend:channelID" reference to the right backend
   - Registry is package-global, init() sorted, deterministic order.

3. **Wrap hwmon as `HwmonBackend`** under `internal/hal/hwmon/` (not `internal/hwmon`):
   - Shim that calls into the existing `internal/hwmon` package implementations.
   - Preserve all existing paths, chip-name resolution, and pwm_enable save/restore semantics.
   - Register in an `init()` so `internal/hal` sees it.

4. **Wrap NVML as `NvmlBackend`** under `internal/hal/nvml/`:
   - Same wrapping discipline. Preserves the insufficient-permissions graceful-degrade path.

5. **Update controller, watchdog, calibrate**:
   - Controller holds `FanBackend` ref (or a slice) via constructor injection; all PWM writes go through `backend.Write`, all restores via `backend.Restore`.
   - Watchdog's Restore() path becomes a fan-out across every registered backend's Restore().
   - Calibrate reads/writes via the backend interface.
   - No direct sysfs writes should remain in controller.go / calibrate/*.go / watchdog.go. Grep to verify.

6. **CHANGELOG entry** under `## [Unreleased]` / `### Changed`:
   `- refactor(hal): introduce FanBackend interface; hwmon and NVML move behind it, no behaviour change (#P1-HAL-01)`

## Definition of done
- `go build ./...` clean on all 4 CI OS lanes.
- `go vet ./...` clean.
- `go test -race ./...` passes. Every existing test must still pass; if a test needs to be migrated to the interface, migrate it minimally (do not add new tests).
- `grep -rn "sysfs\|/sys/class/hwmon\|pwm_enable" internal/controller internal/calibrate internal/watchdog` returns zero results (all sysfs access goes through the backend now).
- PWM clamping, pwm_enable save/restore, `ZeroPWMSentinel`, and every invariant in `.claude/rules/hwmon-safety.md` remain untouched behaviourally.
- Binary size delta ≤ +100KB (check via `go build -o ventd ./cmd/ventd && stat ventd`; compare to baseline on main).
- No new direct dependencies in go.mod.

## Out of scope
- Tests beyond what already exists (no new unit tests; migration-only).
- Any new backend (IPMI/liquid/crosec/pwmsys/asahi are Phase 2 tasks).
- Any behavioural change whatsoever — this is a pure refactor.
- Changing public API of /api/*.
- Changing .claude/rules/hwmon-safety.md.

## Branch and PR
- Branch: `claude/HAL-interface-<5-char-rand>`
- Commit prefix: `refactor:`
- Draft PR: `refactor(hal): introduce FanBackend interface (P1-HAL-01)`
- PR body must include: goal verbatim; files-touched; the grep output proving no direct sysfs in controller/calibrate/watchdog; all verification outputs; task ID.

## Constraints
- Allowlist: `internal/hal/**` (new), `internal/controller/*.go`, `internal/calibrate/*.go`, `internal/watchdog/*.go`, `internal/hwmon/hwmon.go`, `internal/nvidia/nvidia.go`, `cmd/ventd/main.go`, `CHANGELOG.md`.
- Zero new direct dependencies. CGO_ENABLED=0 preserved.
- Every goroutine must have a clear lifecycle tied to ctx or a stop channel.
- Every file reader/writer must be deferred-closed or justified inline.
- No panic / log.Fatal / os.Exit outside cmd/ventd/main.go paths.

## Reporting
PR body must carry:
- STATUS / BEFORE-SHA / AFTER-SHA / POST-PUSH GIT LOG (`git log --oneline main..HEAD`)
- BUILD (`go build ./...`) — full output, not truncated
- VET (`go vet ./...`) — full output
- TEST (`go test -race ./...`) — full output
- GREP (the sysfs-absence grep above)
- SIZE: baseline binary size, new binary size, delta
- GOFMT (`gofmt -l .`) — must be empty
- CONCERNS (especially around any subtle behaviour change you had to think hard about)
- FOLLOWUPS (anything you noticed that belongs in a later task)
