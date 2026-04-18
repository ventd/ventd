# P2-CROSEC-01 — Framework + Chromebook EC backend

**Model:** Opus 4.7. Laptop EC writes can lock the EC into an unresponsive state requiring a hardware reset. Protocol correctness is non-negotiable.
**Care level:** HIGH.

## Task

- **ID:** P2-CROSEC-01
- **Track:** CROSEC (Phase 2)
- **Goal:** `/dev/cros_ec` backend for Framework laptops + Chromebooks. Read fan RPM, set fan duty, read thermal zone thresholds. Gate by presence of `/dev/cros_ec` AND successful EC_CMD_HELLO response.

## Context

1. `ventdmasterplan.mkd` §8 P2-CROSEC-01 entry.
2. Kernel source reference: `include/linux/mfd/cros_ec_commands.h` defines every command number and payload shape. Review EC_CMD_HELLO (0x0001), EC_CMD_PWM_GET_FAN_RPM (0x0020), EC_CMD_PWM_SET_FAN_DUTY (0x0024), EC_CMD_THERMAL_GET_THRESHOLD (0x0051).
3. `internal/hal/backend.go` — FanBackend contract.

## What to do

1. `internal/hal/crosec/crosec.go`:
   - `Backend` struct.
   - ioctl wrapper for `CROS_EC_DEV_IOCXCMD_V2`.
   - EC_CMD_HELLO gate: Enumerate returns empty if hello fails or `/dev/cros_ec` missing.
   - `Read`: EC_CMD_PWM_GET_FAN_RPM returns RPM; also populates Reading.PWM via EC_CMD_PWM_GET_FAN_DUTY.
   - `Write`: EC_CMD_PWM_SET_FAN_DUTY with duty 0-100.
   - `Restore`: EC_CMD_THERMAL_AUTO_FAN_CTRL (0x0052) — puts EC back in firmware auto mode.
2. `internal/testfixture/fakecrosec/fakecrosec.go`: char-dev stub with named pipe + ioctl dispatcher.
3. `internal/hal/crosec/crosec_test.go`:
   - HELLO gate: fake dev without hello handler → empty Enumerate.
   - Happy path Read.
   - Happy path Write.
   - Restore hands back to auto.
   - Lockout handling: consecutive write failures → Restore + structured event.
4. Wire into `cmd/ventd/main.go` registry.
5. CHANGELOG line.

## Definition of done

- CGO-off build clean.
- All tests pass `-race`.
- Non-Framework/non-Chromebook systems: `/dev/cros_ec` absent → silent no-op at enumerate.
- Kernel header command numbers match exactly (review kernel v6.7 header as canonical; values have been stable for years).
- CHANGELOG one-line.
- vet/fmt clean.

## Out of scope

- ThinkPad (`thinkpad_acpi`), Dell (`dell-smm-hwmon`), HP (`hp-wmi`) — those are P2-CROSEC-02.
- Battery / charger commands (EC does those too but not this PR).
- AP-EC mailbox on non-standard chips.
- Tests outside the scope this task targets per the testplan catalogue.

## Branch and PR

- Branch: `claude/P2-CROSEC-01-framework-ec`.
- Title: `feat(hal/crosec): Framework + Chromebook EC backend (P2-CROSEC-01)`.

## Allowlist

- `internal/hal/crosec/**` (new)
- `internal/testfixture/fakecrosec/**` (new)
- `cmd/ventd/main.go` (registry line)
- `CHANGELOG.md`

## Reporting

Standard block.
