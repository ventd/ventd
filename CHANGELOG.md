# Changelog

All notable changes to ventd are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.0] — DRAFT pending rig verification

This release closes the daemon-hardening stream begun in v0.1.x: every
README claim now holds in code, every safety contract has a regression
test, and the install path no longer assumes any particular fan chip.

> **DRAFT status.** This entry will be finalised after `validation/run-
> rig-checks.sh` is executed on phoenix-MS-7D25 and the results are
> committed to `validation/phoenix-MS-7D25-PR21-PR25.md`. Until then,
> none of the items below are guaranteed against real hardware.

### Added — installation path

- Single chip-agnostic udev rule fires on every `hwmon` subsystem device,
  removing the previous chip-name allowlist that excluded boards added after
  release time. (#21)
- Install-time module probing via `ventd --probe-modules` writes the
  detected fan-controller module names to `/etc/modules-load.d/ventd.conf`
  so they survive reboots without manual intervention. (#21)
- Hardware diagnostics surfaced at every daemon start, not only at first-
  boot setup. (#21)
- `EnrichChipName` populates `chip_name` on every hwmon `Sensor` and `Fan`
  entry on save, so the path resolver has the data it needs to re-anchor
  after kernel renumbering. (#21)

### Added — safety

- Hardware watchdog via `sd_notify` with `WatchdogSec=2s`. The daemon pings
  the watchdog on every successful sensor read; a hung main loop now
  triggers a systemd-driven restart instead of leaving fans at the last
  commanded speed indefinitely. (#25)
- `ventd-recover.service` — a tiny one-shot unit that runs on
  `OnFailure=ventd.service` and writes `pwm*_enable=1` to every hwmon
  channel, restoring software control after `SIGKILL`, OOM, or segfault.
  Closes the "fan stuck at last PWM" failure mode. (#25)
- Per-fan `ZeroPWMSentinel` in the calibrate package: any sweep that
  commands PWM=0 for more than 2 seconds escalates back to a safe floor.
  Prevents a hung calibration from stalling a fan under load. (#25)
- Periodic hwmon rescan tightened from 5 minutes to 10 seconds, so a
  freshly plugged fan or `modprobe` cycle is picked up promptly. (#25)
- SELinux + AppArmor policies for hardened distros, with a graceful
  no-op when neither LSM is present. (#25)

### Changed — config

- Config paths now self-heal across `hwmonN` renumbering via chip-name
  binding. The resolver looks up `<stable_device>/hwmon/hwmonN` at startup
  and rebases stored paths onto the current numbering. Existing v0.1.x
  configs continue to load unchanged. (#21)

### Tests

- `internal/setup` coverage 5.9% → 56.0%. New tests cover the wizard's
  state machine, the buildConfig safety contracts (clamp, pump floor,
  case-curve emission rules), the chip-name resolution path, and every
  documented diag-emitter mapping. (PR-F1.1)
- `internal/controller` coverage 12.0% → 85.9%. New tests pin the
  per-fan write loop's clamp enforcement, manual override semantics,
  rpm_target write paths, and — load-bearing — the cooperation contract
  with the calibrate `ZeroPWMSentinel` (controller yields its tick while
  a calibration sweep owns the channel). (PR-F1.2)

### Verification

- New rig-side verification harness at `validation/run-rig-checks.sh`
  drives every PR #21 / PR #25 reproducer in one command, with results
  templated by `validation/phoenix-MS-7D25-PR21-PR25.md`. (PR-F1.3)
- _(TBD: replace with link to the populated phoenix-MS-7D25 results
  document once the rig run completes.)_

### Known issues

- _(TBD by the rig run.)_
