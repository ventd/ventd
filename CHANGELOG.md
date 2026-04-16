# Changelog

All notable changes to ventd are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added — Apply confirmation modal + `/api/config/dryrun`

- The dashboard Apply button now opens a diff preview before committing
  a change. Click Apply → server-side dryrun compares the candidate
  config against what the daemon is currently running → modal renders
  a semantic diff (added / removed / modified items with per-field
  before→after). Cancel leaves the dirty state untouched; Confirm does
  the actual `PUT /api/config`. If the diff is empty (a spurious Apply
  click), the modal is skipped and a toast explains.
- New `POST /api/config/dryrun` endpoint returns a `ConfigDiff` shape
  keyed by section (sensors / fans / curves / controls / hwmon / web).
  The pairing is identity-bound (by Name / Fan) so a curve rename is
  reported as one removed + one added, not a string-level drift across
  every field. Registered via the `apiRoute` slice so `/api/v1/config/dryrun`
  resolves to the same handler. ventd controls physical hardware and
  this modal is the ergonomic belt-and-braces against an accidental
  drag-and-Apply.

### Added — visual binding between dashboard cards

- Hovering any fan / curve / sensor card on the dashboard now
  highlights every card in the binding chain — the fan's bound curve,
  the curve's source sensor, and (for mix curves) every upstream curve
  and sensor the mix reads. Non-highlighted cards dim so the
  relationship pops. `collectBindings()` walks the dependency graph
  with a depth guard against self-referential mixes; the cycle
  protection is intentional because a badly-authored config can
  legally reference itself until `config.Save` rejects it. Touch
  devices get a brief 1.2s highlight on tap without selecting the
  curve, so mobile users can still see the relationship.
- Source-curve names inside a mix curve's card are now clickable.
  Clicking a source opens that curve in the editor below the card
  grid and smooth-scrolls the editor into view. Dangling references
  (a source whose upstream was renamed or deleted) render with a
  dashed red underline and a "Source curve not found" tooltip instead
  of a live link. Closes the audit finding that `max(cpu_linear, chipset_linear)`
  read as mystery text with no affordance.

### Added — web UI empty-state copy

- Every empty dashboard section now renders explanatory copy instead of
  blank containers. Sensors shows "No sensors configured yet." with an
  "Open Hardware Monitor" button, Controls shows the setup-wizard hint,
  Curves shows the sensor-to-fan binding explainer, and the hardware
  sidebar now tells the user the kernel module probably isn't loaded
  (with `sudo ventd --probe-modules` as the terminal fallback). A new
  `.empty-state` component in `components.css` styles all four variants.
  First step of v0.3 Session C Phase 2 — IA surfacing.

### Added — hwmon topology resilience (v0.3 stream)

- `hwmon.dynamic_rebind` config flag (default `false`). When `true`,
  the daemon re-execs on an `action=added` topology change whose
  stable-device path matches a configured `hwmon_device`, so
  `ResolveHwmonPaths` binds the now-present chip on the next start.
  The rebind path itself landed in #112; this flag gates it so
  v0.2.x semantics are preserved until an operator opts in. Closes
  #95 and #98. (#125)
- `ventd-postreboot-verify.service` ships under `deploy/` alongside
  `deploy/postreboot-verify.sh`. Opt in at install time with
  `VENTD_INSTALL_POSTREBOOT_VERIFY=1` (`scripts/install.sh`) or before
  `dpkg -i` / `rpm -i` (handled by `scripts/postinstall.sh`). Runs once
  on the next boot, writes PASS/FAIL across the 4a–4i gates to
  `/var/log/ventd/postreboot-<TS>.log`. Disabled by default.
  `internal/packaging/unit_postreboot_verify_test.go` guards the
  shipping unit's section hygiene (OnFailure/After/Wants in `[Unit]`;
  Type/ExecStart/RemainAfterExit in `[Service]`; no `file:///home/…`
  path in `Documentation=`). Closes #111. (#164)

### Changed — systemd unit

- `deploy/ventd.service` drops `After=systemd-udev-settle.service`.
  `udev-settle` is deprecated on current distros and was a no-op on
  hosts whose kernel ships the hwmon drivers built in. The cold-boot
  hwmon enumeration race (#86, #103) is closed by
  `ExecStartPre=-/usr/local/sbin/ventd-wait-hwmon` (PR #105) plus the
  in-binary retry inside `config.LoadForStartup`. `scripts/check-unit-ordering.sh`
  is flipped to the new invariant: `udev-settle` MUST NOT appear
  under `[Unit].After=` and `ventd-wait-hwmon` MUST live under
  `[Service].ExecStartPre=`. (#125)

### Safety

- New `config.Fan.AllowStop` opt-in gate. The controller now refuses
  to write `PWM=0` unless the fan has both `min_pwm: 0` *and*
  `allow_stop: true`. Fixes a latent gap where `min_pwm: 0` (valid
  for Intel stock coolers and some case fans) silently permitted
  zero writes through the clamp, violating hwmon-safety rule 1.
  Existing configs without `allow_stop` keep the current behaviour
  when `min_pwm > 0`; a config with `min_pwm: 0` and no
  `allow_stop` now logs a warning and skips the write rather than
  stopping the fan. (#115, #124)
- `Controller.Run` now restores the watchdog on every exit path
  (`ctx.Done`, early error, panic), not only the panic-recover
  branch. The daemon-level `defer wd.Restore()` in
  `cmd/ventd/main.go` becomes defence-in-depth; the controller
  layer owns the hwmon-safety rule 4 invariant on its own. (#116,
  #124)

### Tests

- `internal/controller/safety_test.go` binds every rule in
  `.claude/rules/hwmon-safety.md` to a named subtest. Controller
  statement coverage: 12.0 % → 88.0 %. All 12 safety subtests pass
  under `-race`. (#118, #124)
- `internal/setup/manager_roots_test.go` covers the six hardware-
  discovery methods (`discoverCPUTempSensor`, `discoverAMDGPUTemp`,
  `discoverHwmonControls`, `readCPUModel`, `readCPUVendor`,
  `readRAPLTDPW`, `gatherProfile`) against fixture trees under
  `t.TempDir()`. Replaces four `#131` `t.Skip` placeholders with 33
  table-driven subcases. Closes #131. (#163)

### Changed — test seams

- `setup.Manager` gains `hwmonRoot`, `procRoot`, and `powercapRoot`
  fields plus a `NewWithRoots(cal, logger, hwmonRoot, procRoot,
  powercapRoot)` test constructor. `New(cal, logger)` still takes the
  production defaults; no public signature changed. Mirrors the
  pattern already used by `hwmonpkg.EnumerateDevices(root)` and
  `config.SetHwmonRootFS`. Unblocks #132 (calibrate.Manager interface)
  and #133 (wizard state-machine tests). (#163)

### Infrastructure

- CI `build-and-test` expanded to a four-distro matrix (Ubuntu 24.04,
  Fedora 41, Arch, Alpine 3.20) and all four rows are required on
  `main`. Alpine builds with `CGO_ENABLED=0` and skips `-race` to
  preserve the libc-only binary guarantee; race coverage comes from
  the other three rows. Satisfies the CI matrix acceptance gate in
  the v0.3.0 plan. (#114)
- New `.github/workflows/docker.yml` builds `packaging/docker/Dockerfile`
  on every push to `main` and PR touching `packaging/docker/**`,
  `deploy/**`, or `scripts/**`. Three jobs: `build amd64` (exports
  the image as an artifact), `build arm64` (QEMU emulation,
  `continue-on-error: true` until the cross-build flake rate is
  characterised), `smoke amd64` (loads the artifact, runs the
  container detached, polls `/api/ping` for up to 30 s, dumps logs
  on failure). No registry push. (#162, closes #144)

### Changed — Docker packaging

- `packaging/docker/Dockerfile` now accepts `--build-arg VENTD_GID=<n>`
  (default `472`), threading the override into `addgroup -S -g
  "${VENTD_GID}" ventd` so hosts whose `ventd` group landed on a
  different system GID can align without a source edit. UID stays
  fixed at 472 because only the group participates in the sysfs DAC
  check that `deploy/90-ventd-hwmon.rules` sets up.
  `packaging/docker/docker-compose.yml` interpolates `VENTD_GID`
  into both `build.args` and the runtime `user:` line, so a single
  `VENTD_GID=...` env var aligns the image group and the process
  gid. (#162, closes #143)

### Added

- `/healthz` and `/readyz` unauthenticated probes for orchestrators (#155).
- `/api/version` and `/api/v1/version` return version / commit / buildDate / go runtime (#155).
- Every `/api/*` route is now also served under `/api/v1/*` — v1 is the stable contract (#155).
- `--version` and `--version --json` flags on the binary (#155).
- GitHub issue templates (bug / feature / regression / security) and PR template (#150, #154).
- Top-level `Makefile` with `build`, `test`, `cover`, `lint`, `e2e`,
  `safety-run`, `issue-review`, `test-issue-logger` targets (#154).
- `scripts/cc-issue-logger.test.sh` self-test harness for the filing library (#152).

### Fixed

- `config.Empty()` and `config.Default()` now initialise slice-typed fields
  with empty slices rather than nil — previous nil marshalled to JSON `null`
  and crashed the web UI (#151).
- `scripts/cc-issue-logger.sh` `race_count` subshell no longer emits `"0\n0"`
  when grep finds zero matches (#152).
- Default issue-filing labels no longer reference the non-existent `v0.3.0`
  label — unlabelled by default, caller passes labels explicitly (#152).
- `moduleFromPath` recognises `nct6683` and `nct6687*` chip names (#153).
- `sdDriverRe` now matches the `Driver \`x' (should be inserted):` variant
  emitted by sensors-detect when a driver is available but not loaded (#153).

## [v0.2.0] — 2026-04-16

This release closes the daemon-hardening stream begun in v0.1.x: every
README claim now holds in code, every safety contract has a regression
test, and the install path no longer assumes any particular fan chip.
Rig re-verified on phoenix-MS-7D25 (see
`validation/phoenix-MS-7D25-v0.2.0-final-pass.md`), with a second
post-#103 cold-boot re-verify confirming the udev-race fix also holds
clean (9/9 PASS including the new 4i gate).

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
- Resolver now honours `hwmon_device` on the single-match path too.
  On dual-Super-I/O boards where one driver had not yet enumerated
  when ventd started, the resolver previously returned the sole
  candidate unconditionally and bound every fan to the wrong chip
  for the lifetime of that boot. New code validates the candidate's
  `device` symlink against the configured path and errors loudly
  when they don't match. (#86)
- Cold-boot first-boot mode fall-through closed. When the configured
  `hwmon_device` hadn't been created by udev yet, the resolver's
  ENOENT propagated through `Load()` as a wrapped `os.ErrNotExist`
  and `cmd/ventd` mis-classified it as "no config yet", wiping the
  operator's setup state until a manual restart. Three independent
  layers now close this: a sentinel `ErrHwmonDeviceNotReady` that
  terminates the error chain inside the resolver, a new
  `config.LoadForStartup` helper that discriminates first-boot via
  `os.Stat` before calling `Load()` and retries on the sentinel for
  up to 30s, and an `ExecStartPre=ventd-wait-hwmon` gate in the
  shipped systemd unit as belt-and-braces. (#103)
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
