You are Claude Code, working on the ventd repository.

## Task
ID: P2-CROSEC-01
Track: CROSEC
Goal: Framework laptop + Chromebook fan control via the cros_ec kernel interface at `/dev/cros_ec`. Uses `EC_CMD_HELLO` to detect EC presence, `EC_CMD_PWM_GET_FAN_RPM` / `EC_CMD_PWM_SET_FAN_DUTY` for reads/writes, and `EC_CMD_THERMAL_GET_THRESHOLD` for temp thresholds.

## Care level
Medium-high. Writing EC commands can lock out the EC if the PWM set command is malformed. The EC typically self-heals (firmware reasserts control after a few seconds of no commands), but worst-case requires a power cycle. All writes must be gated on successful HELLO.

## Context you should read first

- `internal/hal/backend.go`
- `internal/hal/hwmon/backend.go`
- Framework's EC command documentation: <https://github.com/FrameworkComputer/EmbeddedController/blob/main/include/ec_commands.h> — specifically EC_CMD_HELLO (0x0001), EC_CMD_PWM_GET_FAN_RPM (0x0020), EC_CMD_PWM_SET_FAN_DUTY (0x0024), EC_CMD_THERMAL_GET_THRESHOLD (0x0050).
- Chromium OS EC interface: `/dev/cros_ec` uses `ioctl(CROS_EC_DEV_IOC)` with `cros_ec_command` struct.

## What to do

1. Create `internal/hal/crosec/backend.go`:
   - `type Backend struct { device string; logger *slog.Logger; present bool }`.
   - `NewBackend(logger)` opens `/dev/cros_ec` if present. If ENOENT or EC_CMD_HELLO fails, mark present=false. No error returned — just silent disable on non-Framework/Chromebook systems.
   - HELLO handshake at construction, caches version info.

2. `Enumerate(ctx)`:
   - If not present, return empty.
   - Issue `EC_CMD_PWM_GET_NUM_FANS` (or equivalent vendor-specific) to discover fan count.
   - Emit one `hal.Channel` per fan with role `hal.RoleCaseFan` (Framework and Chromebook typically have one or two case fans, no CPU/GPU distinction at EC level).

3. `Read(ch)`: EC_CMD_PWM_GET_FAN_RPM with fan index. Return `hal.Reading{RPM: <int>}`.

4. `Write(ch, pwm)`: EC_CMD_PWM_SET_FAN_DUTY with fan index + duty 0-100 (scale from 0-255).

5. `Restore(ch)`: EC_CMD_PWM_SET_FAN_DUTY with duty=255 (0xFF is the EC "return to firmware auto" magic). Alternative: EC_CMD_THERMAL_AUTO_FAN_CTRL if available.

6. `Close()`: close fd.

7. `Name()`: `"crosec"`.

8. Unit tests:
   - `TestBackend_AbsentDevice_NoError` — /dev/cros_ec missing → NewBackend returns non-nil with present=false, no error.
   - `TestEnumerate_NotPresent_Empty` — Enumerate on absent backend returns 0 channels.
   - `TestWrite_NotPresent_Error` — Write on absent backend returns clear error, does not attempt ioctl.
   - `TestDutyConversion` — 0-255 PWM → 0-100 duty rounding correct.

9. Register in `cmd/ventd/main.go`. Must not break boot on non-Chromebook systems.

10. Build/vet/lint/test clean.

## Definition of done

- Package exists, backend implements FanBackend.
- Absent-device path is silent (no error logs on desktop/server systems).
- Registration doesn't break existing backends.
- Tests pass.
- CHANGELOG entry.

## Out of scope

- ThinkPad (thinkpad_acpi), Dell (dell-smm-hwmon), HP (hp-wmi) — that's P2-CROSEC-02.
- Real hardware verification (HIL).
- Tests beyond the unit tests above.

## Branch and PR

- Branch: `claude/P2-CROSEC-01-framework-chromebook`
- Title: `feat(hal/crosec): Framework + Chromebook EC fan control (P2-CROSEC-01)`

## Constraints

- Files: `internal/hal/crosec/**`, `cmd/ventd/main.go` (registration), `CHANGELOG.md`.
- No new deps beyond `golang.org/x/sys`.
- CGO_ENABLED=0 compatible.
- Never write EC commands without first succeeding a HELLO.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.
