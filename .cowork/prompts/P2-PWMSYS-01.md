You are Claude Code, working on the ventd repository.

## Task
ID: P2-PWMSYS-01
Track: PWMSYS
Goal: ARM SBC PWM backend via `/sys/class/pwm/pwmchipN/pwmN`. Simplest of the Phase 2 backends — straight sysfs write, no protocol. Target: Raspberry Pi 5 (pwmchip0/pwm0 + pwm1), similar SBCs.

## Care level
Low. PWM sysfs is kernel-mediated; writes are bounded to 0-period range. The main risk is writing PWM values during a shutdown that stops the PWM clock mid-cycle, but the kernel handles that.

## Context you should read first

- `internal/hal/backend.go`
- `internal/hal/hwmon/backend.go`
- `Documentation/pwm.txt` in the kernel tree or <https://docs.kernel.org/driver-api/pwm.html>.
- The sysfs interface: `/sys/class/pwm/pwmchipN/npwm` (count), `pwmN/enable` (0/1), `pwmN/period` (ns), `pwmN/duty_cycle` (ns, ≤ period).

## What to do

1. Create `internal/hal/pwmsys/backend.go`:
   - Scan `/sys/class/pwm/` for `pwmchipN` directories at Enumerate time.
   - For each chip, read `npwm` and export one `hal.Channel` per PWM index.
   - PWM must be "exported" (write N to `pwmchipN/export`) before it can be written. Handle this lazily on first Write per channel.

2. `Read(ch)`: PWM sysfs doesn't expose RPM (no tachometer feedback). Return `hal.Reading{PWM: <current duty as 0-255 scaled from duty_cycle/period>, RPM: 0}`. Document this clearly in the backend.

3. `Write(ch, pwm)`:
   - Compute duty_cycle = (pwm / 255) * period.
   - Write duty_cycle to `pwmN/duty_cycle`.
   - Ensure `pwmN/enable` is 1.

4. `Restore(ch)`: write `pwmN/enable=0` (disable PWM, hardware reverts to whatever the default state is — usually full-speed for cooling fans).

5. `Close()`: nothing to do.

6. `Name()`: `"pwmsys"`.

7. Configurable period: default 25kHz (40000 ns) which matches PC fan PWM expectations. Allow override via backend construction option.

8. Unit tests with fakepwmsys-style fixtures (create a tempdir mimicking /sys/class/pwm structure):
   - `TestEnumerate_PiLike_TwoChannels` — fake tree with one chip + 2 PWMs → 2 channels.
   - `TestEnumerate_NoChips_Empty`.
   - `TestWrite_DutyConversion` — verify PWM=128 produces duty_cycle=20000 at period=40000.
   - `TestRestore_DisablesPWM` — write 0 to enable file confirmed.

9. Register in main.go.

10. Build/vet/lint/test clean.

## Definition of done

- Package + unit tests.
- Fake-sysfs-backed tests cover the happy path.
- No-chips environments are silent (desktop systems just see 0 channels).
- CHANGELOG entry.

## Out of scope

- RPM reading (the hardware doesn't support it at this abstraction).
- Per-chip frequency configuration UI.
- fakepwmsys as a shared fixture — tests can use local tempdir setup for now; fixture lives in T-PWMSYS-01.

## Branch and PR

- Branch: `claude/P2-PWMSYS-01-arm-sbc`
- Title: `feat(hal/pwmsys): ARM SBC PWM via /sys/class/pwm (P2-PWMSYS-01)`

## Constraints

- Files: `internal/hal/pwmsys/**`, `cmd/ventd/main.go` (registration), `CHANGELOG.md`.
- No new deps.
- CGO_ENABLED=0.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
