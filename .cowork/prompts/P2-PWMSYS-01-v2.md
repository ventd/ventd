# P2-PWMSYS-01 — ARM SBC sysfs PWM backend

**Care level:** MEDIUM. The protocol is simple, but the edge cases (exporting pwm channels, period vs duty semantics, enable vs duty ordering) need care. Work the task however your session is configured — no model-gated abort.

## Task

- **ID:** P2-PWMSYS-01
- **Track:** PWMSYS (Phase 2)
- **Goal:** `/sys/class/pwm/pwmchipN/pwmN` backend for ARM SBCs (Raspberry Pi 5 primary target, also covers most Rockchip / Allwinner / Amlogic SBCs with PWM GPIOs).

## Context

1. `ventdmasterplan.mkd` §8 P2-PWMSYS-01 entry.
2. Kernel doc: `Documentation/ABI/testing/sysfs-class-pwm` — canonical description of the export/period/duty_cycle/enable sequence.
3. `internal/hal/hwmon/backend.go` — reference FanBackend impl; PWMSYS follows similar shape but with distinct sysfs paths.

## What to do

1. `internal/hal/pwmsys/pwmsys.go`:
   - Enumerate: walk `/sys/class/pwm/pwmchip*/npwm`, export each channel idx 0..npwm-1 by writing to `pwmchipN/export`.
   - State struct embedded in Channel.Opaque: period_ns, max_duty_cycle (= period).
   - Read: duty_cycle / period * 255 → PWM byte. No RPM readback (these fans have no tach on most SBCs); Reading.RPM = 0.
   - Write: duty_cycle = pwm / 255 * period, enable = 1.
   - Restore: enable = 0 (hand control back to kernel / unexport).
2. `internal/testfixture/fakepwmsys/fakepwmsys.go`: temp-dir with pwmchip0/ tree stubbed.
3. `internal/hal/pwmsys/pwmsys_test.go`:
   - Enumerate on fake RPi 5 tree (2 pwmchips, 2 channels each).
   - Write translates PWM to duty correctly (round-trip within 1 step).
   - Restore disables channel.
   - Re-enumerate after unexport: channel reappears correctly.
4. Wire into `cmd/ventd/main.go` registry.
5. CHANGELOG.

## Definition of done

- CGO-off build clean.
- Tests `-race` pass.
- Fake RPi 5 tree enumerates correctly, writes land in fake sysfs with expected semantics.
- Round-trip PWM precision: writing 128 and reading back yields 128 ± 1.
- Non-SBC systems: `/sys/class/pwm/` may be absent or empty → Enumerate returns `[]` cleanly.
- CHANGELOG one line.
- vet/fmt clean.

## Out of scope

- pwm_enable mapping to hwmon semantics (this is a different sysfs interface entirely).
- RPM readback — most SBC fans have no tach; treat as unsupported.
- Non-Linux PWM (macOS, BSD have their own paths; those are Phase 6).
- Tests outside the scope this task targets per the testplan catalogue.

## Branch and PR

- Branch: `claude/P2-PWMSYS-01-sbc-backend`.
- Title: `feat(hal/pwmsys): ARM SBC sysfs PWM backend (P2-PWMSYS-01)`.
- Open as ready-for-review (NOT draft).

## Allowlist

- `internal/hal/pwmsys/**` (new)
- `internal/testfixture/fakepwmsys/**` (new)
- `cmd/ventd/main.go` (registry line)
- `CHANGELOG.md`

## Reporting

Standard block.
