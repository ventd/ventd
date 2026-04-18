You are Claude Code, working on the ventd repository.

## Task
ID: P2-ASAHI-01
Track: ASAHI
Goal: Apple Silicon Asahi Linux backend. Detect via `/proc/device-tree/compatible` containing `apple,t8103`, `apple,t8112`, `apple,m1`, or related. Reports fan channels via the hwmon layer (Asahi upstream exposes them there), but adds role classification and an Asahi-specific enumeration guard so this backend can ship before full Apple Silicon SMC writes are supported upstream.

## Care level
Low-medium. Read-mostly for now. Writes depend on what the Asahi kernel exposes; currently many M-series machines only surface fan RPM monitoring, not fan PWM write. The backend must gracefully report `hal.CapRead` without `hal.CapWritePWM` on those machines.

## Context you should read first

- `internal/hal/backend.go`, `internal/hal/hwmon/backend.go`.
- Asahi Linux docs on fan/thermal support: <https://asahilinux.org/> hardware support matrix.
- `/proc/device-tree/compatible` format: NUL-separated strings.

## What to do

1. Create `internal/hal/asahi/backend.go`:
   - `NewBackend(logger)` reads `/proc/device-tree/compatible`. If it contains any of `apple,t8103`, `apple,t8112`, `apple,t6000`, `apple,t6001`, `apple,t6020`, `apple,t6021`, `apple,t6022`, or `apple,m1*` — detected=true. Else silent disable.

2. `Enumerate`: when detected, walk `/sys/class/hwmon` for chips whose `name` starts with `macsmc` or equivalent Asahi fan driver name. Wrap each as a `hal.Channel`. Assign `hal.RoleCaseFan` (MacBook fans are case-like; no CPU/GPU split in the Mac physical layout).

3. `Read`: same sysfs read as hwmon backend, scoped to Asahi-managed chips.

4. `Write`: if the channel's pwm file exists and is writable, delegate to hwmon-style write. If not (common on current Asahi), return `errors.New("asahi: fan write not supported by current kernel driver")`. The channel's `hal.Caps` must not include CapWritePWM in this case.

5. `Restore`: delegate to hwmon restore if write was supported; no-op otherwise.

6. `Close`: nothing.

7. `Name()`: `"asahi"`.

8. Unit tests:
   - `TestBackend_NonApple_NotDetected` — fake `/proc/device-tree/compatible` with "linux,generic" → detected=false, Enumerate empty.
   - `TestBackend_M1_Detected` — fake compatible="apple,t8103" → detected=true.
   - `TestEnumerate_NoMacsmcChips_Empty` — detected=true but no chips → empty (no error).
   - `TestCaps_ReadOnlyWhenPWMAbsent` — channel reports CapRead but not CapWritePWM when pwm file missing.

9. Register in main.

10. Build/vet/lint/test clean.

## Definition of done

- Package + tests.
- Silent no-op on non-Apple hardware.
- Read-only Caps when writes aren't supported.
- CHANGELOG entry.

## Out of scope

- SMC writes via IOKit (that's Phase 6-MAC for bare-metal macOS; Asahi is a different path).
- PMU / power-gating logic.
- Full Apple Silicon thermal zone mapping (ship the backend skeleton; refinement comes with user reports).

## Branch and PR

- Branch: `claude/P2-ASAHI-01-apple-silicon`
- Title: `feat(hal/asahi): Apple Silicon detection + hwmon wrapping (P2-ASAHI-01)`

## Constraints

- Files: `internal/hal/asahi/**`, `cmd/ventd/main.go` (registration), `CHANGELOG.md`.
- No new deps.
- CGO_ENABLED=0.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
