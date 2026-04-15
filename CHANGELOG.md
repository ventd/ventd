# Changelog

All notable changes to ventd are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.0]

**Status:** unreleased — blocked by [#103](https://github.com/ventd/ventd/issues/103).

This release closes the daemon-hardening stream begun in v0.1.x: every
README claim now holds in code, every safety contract has a regression
test, and the install path no longer assumes any particular fan chip.
Rig re-verified on phoenix-MS-7D25 (see
`validation/phoenix-MS-7D25-v0.2.0-final-pass.md`).

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

### Changed — web UI

- Phase 0 hardening: Content-Security-Policy tightened (no
  `'unsafe-inline'` on `script-src` or `style-src`), first-boot probe
  + lockout UI unblocked so the setup wizard is reachable when the
  daemon has no config yet. (#26)
- Phase 0.5 refactor: `ui/styles/app.css` split into
  `tokens.css` / `base.css` / `layout.css` / `components.css`, and
  `ui/scripts/app.js` split into five defer-loaded modules
  (`state`, `api`, `render`, `curve-editor`, `setup`). No behaviour
  change; edits now scoped to the module that owns the feature. (#30)
- Typography: body now renders in the system sans-serif stack with
  monospace reserved for numeric readouts (PWM %, RPM, temperatures).
  Readability on every platform without shipping a webfont. (#39)
- Iconography: Unicode glyph placeholders replaced by a bundled
  Lucide SVG sprite served from `ui/icons/sprite.svg`. Consistent
  rendering across browsers; no external requests. (#48)
- Live updates: fan state now streams over Server-Sent Events
  (`GET /api/events`) with the polling code path retained as a
  fallback. The dashboard reflects PWM / RPM / temperature changes
  without the old poll cadence. (#47)

### Fixed

- Resolver now walks `/sys/class/hwmon` symlinks instead of stopping
  at `DirEntry.IsDir()==false`. The pre-fix behaviour returned an
  empty chip map on real sysfs and the daemon crash-looped on
  `no hwmon device with chip_name <...>` for every config. Regression
  test builds a real-symlink tempdir to keep the next refactor
  honest. (#31)
- Resolver now consumes the `hwmon_device` config field as a
  tiebreaker when two hwmon chips report the same `name`
  (e.g. dual-`nct6687` on MSI MAG Z790). Single-match chips ignore
  the field; empty `hwmon_device` on an ambiguous chip still errors
  loudly and names both candidate `hwmonN` entries. (#42)
- `config.writeFileSync` chown-matches the atomic `.tmp` to the
  parent config dir's owner before the rename when the writer's
  euid is 0, so any `sudo ventd ...` invocation stops silently
  leaving `root:root` files in `/etc/ventd` that the `User=ventd`
  systemd unit can no longer read. `install.sh`'s no-init-system
  fallback hint switched from `sudo ventd` to `sudo -u ventd ventd`
  so operators stop hitting the same footgun. (#38)
- Setup wizard now round-trips the generated config through
  `config.Parse` before the Review screen, so any emitter
  regression fails as a wizard error instead of an Apply-time dead
  end that the zero-terminal UX can't escape. (#32)
- `case_curve` emission now gated on `len(gpuFans) > 0` so rigs
  with a detected GPU sensor but no controllable GPU fan
  (NVML insufficient-permissions — common on the default hardened
  unit) stop generating configs that reference an undeclared
  `gpu_curve`. Case fans fall back to `cpu_curve`. (#33)

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
  templated by `validation/phoenix-MS-7D25-PR21-PR25.md`. (#27)
- v0.2.0-specific runbook at
  `validation/phoenix-MS-7D25-v0.2.0-final.md` adds three sections
  covering the F2 fixes (config ownership under systemd, hwmon
  resolution on dual-nct6687, NVIDIA docs walkthrough). (#44)
- Fresh-VM install smoke harness at `validation/fresh-vm-smoke/`
  runs the curl-pipe-bash installer against pristine Debian/Ubuntu/
  Fedora/Arch images, confirming the install path before every
  release tag. (#45)
- _(TBD: replace with link to the populated phoenix-MS-7D25 results
  document once the rig run completes.)_

### Chores

- Gitignore `AGENTS.md` so locally-scoped agent notes stay out of
  the tree. (#29)
- CI: resolved the `actions/checkout@v6` + `govulncheck-action`
  duplicate `Authorization` header that was 400-ing vuln scans. (#41)
- Test hygiene: `TestLoad_NoChipNameNoOpsBackwardCompat` decoupled
  from host `/sys` — uses a guaranteed-missing `hwmon999` reference
  so `EnrichChipName` ENOENTs cleanly on any rig regardless of
  local hwmon enumeration. (#49)
- Dependencies: bumped `actions/setup-go` to v6 and
  `goreleaser/goreleaser-action` to v7. (#22, #23)

### Known issues

- **NVIDIA GPU fan control requires manual setup.** `ventd` reads GPU
  temperature and RPM out of the box on any system with a working NVIDIA
  driver, but `nvmlDeviceSetFanSpeed_v2` is gated by the driver under
  the daemon's default hardened systemd posture and returns
  `Insufficient Permissions`. A one-time udev rule (or cool-bits /
  capability alternatives) unblocks GPU fan writes. See
  [docs/nvidia-fan-control.md](docs/nvidia-fan-control.md) for the
  three options and their trade-offs. Pure-hwmon rigs (CPU, chassis,
  AIO fans — no NVIDIA fan control) are unaffected. (#34)
- **Setup wizard doesn't remediate missing kernel modules.** If a
  sensor the wizard wants to reference requires a kernel module that
  isn't loaded at scan time (e.g. `coretemp` not loaded on some
  stripped-down images), the wizard currently writes a config the
  resolver will reject on the next boot instead of surfacing a
  "[Attempt load]" remediation card. The fatal daemon path is closed
  by PR #32's pre-validate check; the remediation UI is deferred to
  a later session. Tracked as
  [#36](https://github.com/ventd/ventd/issues/36).
