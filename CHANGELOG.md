# Changelog

All notable changes to ventd are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `internal/hal`: 13 unit tests for the registry layer (`Register`, `Backend`, `Reset`, `Enumerate`, `Resolve`) with race-detector coverage; package coverage 0% → 93% (closes #267).

### Security

- Bumped Go toolchain from `go1.25.0` to `go1.25.9`, closing 17 reachable
  stdlib CVEs identified by govulncheck — including GO-2025-4012 (net/http),
  GO-2025-4008 (crypto/tls ALPN), GO-2025-4009 (encoding/pem), and
  GO-2026-4947 (crypto/x509). No code changes; govulncheck now reports zero
  reachable vulnerabilities.

### Changed

- `spawn-mcp` now invokes the Claude Code CLI in non-interactive print
  mode (`claude --dangerously-skip-permissions -p < prompt.md`) rather
  than piping the prompt as stdin into an interactive `claude`. The
  previous pattern blocked on the first-run theme picker before the CLI
  ever consumed stdin, which meant every dispatch under a fresh service
  user deadlocked at 0% progress. Print mode bypasses the theme picker
  and permission prompts by design. Belt-and-braces: `IS_DEMO=1` is set
  at the unit level to skip onboarding on fresh installs, and
  `CLAUDE_CODE_OAUTH_TOKEN` (generated once with `claude setup-token`)
  is forwarded from `/etc/spawn-mcp/env` so a fresh service user with
  no interactive login can still authenticate. Each session's stdout
  and stderr now land in `/var/log/spawn-mcp/sessions/<session>.log`
  with an exit-code marker, and `tail_session` prefers this persistent
  log over tmux capture-pane so failures survive pane scroll-back.
  `.cowork/LESSONS.md` lesson #9 covers the root cause: the #251
  user-collapse refactor was merged without running one spawn_cc()
  round-trip post-deploy.
- `spawn-mcp.service` drops `ProtectHome=read-only`. The service runs
  as `cc-runner` and `claude` legitimately needs to read and write
  `/home/cc-runner/.claude/` for session state, cache, and auth
  tokens. Since the service IS `cc-runner`, there was no user boundary
  to enforce; `ProtectHome=read-only` was only blocking the service
  from writing its own home. This directive was inherited from the
  pre-collapse hardening set and should have been dropped in #251.

- `spawn-mcp` now runs as the same user as the Claude Code sessions it
  launches (`cc-runner`). The previous two-user split had spawn-mcp
  running as its own system user and handing each prompt file off to
  `cc-runner` via `chown`/`chmod` plus a `sudo -u cc-runner tmux ...`
  hop. Each failure in that pipeline ratcheted capability grants on the
  unit — first `CAP_CHOWN`, then `CAP_FOWNER`, then `NoNewPrivileges=no`,
  then the full SETUID/SETGID/AUDIT_WRITE/DAC_READ_SEARCH set — to
  paper over a boundary that was already ornamental, because spawn-mcp
  held sudo rights to become cc-runner. Collapsing to a single user
  lets the service run with an empty ambient + bounding cap set,
  `NoNewPrivileges=yes`, and no sudoers fragment at all. Prompt files
  land 0600 in `/tmp/spawn-mcp/` natively. The `SPAWN_MCP_AS_USER`
  environment variable and all `sudo -u` subprocess prefixes are gone.
  `.cowork/LESSONS.md` lesson #6 (infra-coherence failures) is the
  class. Operators still attach from their own shell with
  `sudo -u cc-runner tmux attach -t cc-...`, since reaching another
  user's tmux server from a different login shell is a shell-side
  concern, not a service-side privilege escalation.

### Fixed

- fix(hwmon): `persistModule` now merges (append + dedup + sort) into `/etc/modules-load.d/ventd.conf` instead of overwriting, so running `--probe-modules` twice on a dual-chip board keeps both detected modules (P1-MOD-02)

### Changed

- refactor: calibration subsystem drives fans via `hal.FanBackend` — eliminates direct `internal/hwmon` and `internal/nvidia` imports from `internal/calibrate` (#P1-HAL-02)
- perf: drop `modinfo` shellouts in hwmon autoload; parse `modules.alias` directly for zero subprocess overhead on module enumeration (#P1-MOD-01)
- perf(controller): eliminate per-tick allocations in the hot loop — preallocate sensor/smoothed maps, cache compiled curve graph, one-shot config snapshot, cache fan*_max for rpm_target fans, binary-search Points curve, pool Mix.Evaluate vals slice (P1-HOT-01)

### Added

- feat(hal/asahi): Apple Silicon (Asahi Linux) fan backend — detects M-series SoCs via `/proc/device-tree/compatible`, enumerates `macsmc_hwmon` hwmon chips, classifies fan roles from labels, and delegates read/write/restore to the hwmon backend; silent no-op on non-Apple hardware; reports `CapRead` only when `pwm_enable` is absent (P2-ASAHI-01).
- feat(testfixture): `fakedt` fixture for stubbing `/proc/device-tree/compatible` in unit tests.
- feat(controller): symmetric retry+RestoreOne on PWM write failure — retries once at 50ms, triggers per-fan restore on second failure (#P1-HOT-02)
- security: Permissions-Policy header and ETag caching on embedded UI (#P10-PERMPOL-01)
- refactor: FanBackend interface (#P1-HAL-01)
- docs: add public roadmap (#P0-02)
- docs: add hardware-report issue template (#P0-03)
- test: add fixture library skeleton (#T0-INFRA-01)
- test: implement fakehwmon fixture + migrate 3 tests (#T0-INFRA-02)
- feat: fingerprint-keyed hwdb replaces substring table (#P1-FP-01)
- test: faketime fixture for deterministic timer tests (#T0-INFRA-03)
- ci: rule-to-subtest binding lint (#T0-META-01)
- ci: regression-test-per-closed-bug lint (#T0-META-02)
- test(hal): contract test T-HAL-01 binds backend invariants to .claude/rules/hal-contract.md
- feat: opt-in remote hwdb refresh with SHA-256 pin via `--refresh-hwdb` flag (#P1-FP-02)

### Tests

- test: bind internal/watchdog safety invariants (#T-WD-01)

### Fixed

- `handleSystemReboot` now refuses with `409 Conflict` and a human-readable
  body when ventd detects it is running inside a container (PID 1,
  `/.dockerenv` present, or `systemd-detect-virt --container` reports non-`none`).
  Previously the wizard would either crash the container or silently no-op.
  A new test seam on the web server lets the existing handler test exercise
  the 409 path without faking PID 1 in CI. Closes #177.
- Silent confinement downgrade is now auditable post-install. Both
  `scripts/install.sh` and `scripts/postinstall.sh` append one
  timestamped line per security-module load attempt to
  `/var/log/ventd/install.log` (mode 0640, owned `root:ventd`). The
  daemon additionally emits a `WARN` slog line at startup when an
  AppArmor profile exists on disk but `/proc/self/attr/current` reads
  `unconfined` — so an operator running `journalctl -u ventd` finds the
  signal even if the install scrollback is long gone. Closes #211.
- `scripts/install.sh` now prints a box-drawn completion block at the top
  of its final output with the dashboard URL, the one-time setup token
  read from `/run/ventd/setup-token`, and the three steps the user needs
  to reach a running fan-control daemon. Previously the output buried the
  URL on one line and omitted the token entirely — first-boot users had
  to know about `/run/ventd/setup-token` to find it. The scheme default
  is now `https` unconditionally (the daemon auto-generates a self-signed
  cert on first boot), matching what the README and the daemon actually
  do. Partial fix for #182 — the token-file persistence and
  `systemctl status` status-line surfacing remain open.
- The TLS listener now sniffs the first byte of every accepted connection
  and, for plaintext HTTP requests that arrive on the TLS port (a
  mobile browser auto-completing `host:9999` to `http://`, a human
  forgetting the `s`), responds with `301 Moved Permanently` to the
  `https://` equivalent of the same URI instead of the stdlib's
  user-hostile "client sent an HTTP request to an HTTPS server" error.
  TLS ClientHello packets (first byte `0x16`) pass through untouched via
  a peekedConn wrapper. No new port binding; no new capability. Closes #200.

### Added — Phase 3 Control Depth (Session D, v0.3 stream)

- PWM 0-255 → percent 0-100 migration across the config surface.
  `CurveConfig` now carries `MinPWMPct`, `MaxPWMPct`, and `ValuePct`
  (`*uint8`); `CurvePoint` carries `PWMPct`. On load, `Parse()` calls
  `MigrateCurvePWMFields` to reconcile the two forms — legacy YAML
  with `min_pwm: 30` migrates to `min_pwm_pct: 12`, and any config
  carrying both fields prefers `_pct` with a `slog.Warn`. On save,
  `yaml.Marshal` emits only the percent form — a Load → Save cycle
  strips `min_pwm`, `max_pwm`, `value`, and `pwm` keys from any
  pre-3f config in one pass. Round-trip rounding tolerance of ±1
  keeps successive migrations idempotent. The runtime keeps reading
  the raw fields (`MinPWM`, `MaxPWM`, `Value`, `PWM`) so
  `buildCurve` and existing tests see no behaviour change.
  `/api/config` JSON emits both forms so post-3f UI code can read
  the authoritative `_pct` value while legacy clients still see
  the raw fields. Apply-modal diff renders the percent field names
  (`min_pwm_pct: 30 → 50`) so the operator reviews exactly what the
  YAML will write. (Refs #180)
- Curve simulation preview in the editor pane. Three live rows below
  the number inputs: the PWM the curve outputs at the current sensor
  reading, the PWM at the curve's configured upper threshold
  (`max_temp` for linear, last anchor for points), and the PWM at the
  60-minute peak from `/api/history`. All three update on drag, on
  number-input change, and on each SSE status tick; client-side
  evaluation reuses the existing interpolation helpers so no extra
  daemon round-trip fires. The history row gracefully degrades to "—"
  when `/api/history` is absent — 3c (time-series sparklines) lands
  that endpoint in a separate PR and 3d does not block on it. (Refs
  #180)
- Multi-point curves. New curve type `points` interpolates PWM between
  an ascending list of `{temp, pwm}` anchors; `CurveConfig.Points` is
  the YAML surface and `internal/curve/points.go` holds the runtime.
  `validate()` requires ≥ 2 anchors, sorts them by temp, and rejects
  duplicate temps so the interpolation denominator can never collapse
  to zero. Curve editor gains a `Type` selector inside the editor pane
  that converts between Linear / Multi-point / Fixed / Mix with
  sensible field carryover (linear ↔ points round-trips through the
  first/last anchors). Multi-point SVG renders one draggable handle
  per anchor — double-click the graph to add, right-click a handle to
  remove (two-anchor minimum enforced client-side). Hysteresis +
  smoothing from 3a apply transparently because the controller layer
  owns both and needs no curve-type-specific knowledge. (Refs #180)
- Per-curve hysteresis and smoothing. `CurveConfig` grows two optional
  fields: `hysteresis` (°C deadband applied to ramp-DOWN transitions
  only, never delays ramp-up) and `smoothing` (EMA time-constant
  applied to raw sensor reads before curve evaluation). Zero values
  produce pre-3a behaviour bit-for-bit; both fields are `omitempty`
  so existing configs round-trip unchanged. Curve editor exposes
  `Hysteresis (°C)` and `Smoothing (s)` inputs on linear curves. The
  hysteresis gate only applies to curves with a single-sensor input
  (linear, future multi-point); mix and fixed curves bypass it. The
  smoothing EMA is per-controller-per-sensor and resets when the bound
  curve name changes so a hot-reload swap doesn't leak stale
  accumulator state. Clamp to `[MinPWM, MaxPWM]` remains the final
  safety gate — smoothing cannot push the written PWM below the fan's
  floor. (Refs #180)
- Time-series sparklines on every sensor and fan card. Each tile
  now carries a tiny SVG trend strip below its current value,
  coloured with the same teal/amber/red ramp the numeric value and
  duty bar already use. Backed by a per-metric ring buffer (1 hour
  of history at the 2 s sampler interval, ~58 KB total for a
  typical 8-metric config). New `GET /api/history` endpoint returns
  either a single metric's samples (`?metric=<n>`) or all metrics
  in one envelope for fresh-tab seed loads. Client appends to its
  local buffer from the existing SSE stream, so steady-state adds
  zero new network chatter. (Refs #180)

### Added — Phase 2 UI (Session C, v0.3 stream)

- Empty-state copy for every dashboard section (sensors, fans,
  curves, hwmon sidebar). (#185)
- Visual binding between fan ↔ curve ↔ sensor cards on hover /
  selection; source-curve names inside mix cards are clickable and
  open the source curve in the editor. (#190)
- Apply flow now opens a diff modal before committing;
  `POST /api/config/dryrun` returns the server-computed diff. (#195)
- Populated Settings modal with Display / System Status / About /
  Advanced sections; four new `GET /api/system/{watchdog, recovery,
  security, diagnostics}` endpoints drive the System Status rows,
  and a dashboard diagnostics banner fires when WARN/ERROR counts
  are non-zero. (#197)
- `color-scheme` declared on the dashboard and login pages plus
  `:root[data-theme="dark"|"light"] { color-scheme: only <mode>; }`
  so Brave Night Mode, Chrome Force Dark, and iOS Smart Invert stop
  re-tinting the UI when the operator has pinned a theme. Reload
  with the Auto setting now resolves via `prefers-color-scheme`
  instead of falling through to dark. Closes #199. (#203)
- Rescan Hardware button in the Hardware Monitor sidebar.
  `POST /api/hardware/rescan` re-enumerates the hwmon tree and
  returns a fan-level diff (`new_devices`, `removed_devices`,
  `elapsed_ms`); `GET /api/debug/hwmon` exposes the before / after /
  current snapshot of the most recent rescan for diagnostics. Both
  endpoints are read-only; the rescan uses the same
  `EnumerateDevices` path the periodic Watcher tick uses. (#209)
- Panic button: `POST /api/panic` pins every fan to its configured
  `MaxPWM` via a one-shot write, sets an in-memory flag, and uses
  the new `controller.PanicChecker` interface to make controller
  ticks yield while the flag is set. Server-owned timer restores
  (no PWM write during restore — the next controller tick pushes
  curve-derived values within the poll interval). `GET /api/panic/state`
  and `POST /api/panic/cancel` round out the surface. No config
  mutation: a stale tab cannot un-panic a rig on restart. (#212)
- Profiles: `config.Profiles` (`map[string]Profile` with omitempty)
  and `config.ActiveProfile` carry named fan→curve binding sets.
  `GET /api/profile` returns the active name and the full map;
  `POST /api/profile/active` rewrites `Controls[i].Curve` for every
  fan in the profile's bindings and atomically stores the new cfg
  pointer. v0.2.x YAML parses unchanged; zero-value profiles
  round-trip without emitting `profiles:` / `active_profile:`
  keys. Header profile dropdown renders only when profiles are
  configured. (#212)
- Profile scheduling: `config.Profile.Schedule` takes a grammar of
  `HH:MM-HH:MM DAYSPEC` (where DAYSPEC is `*`, `mon-fri`,
  `mon,wed,fri`, or a single day) and the daemon's scheduler
  goroutine switches the active profile on transitions in local
  time. Midnight-wrapping ranges (`22:00-07:00`) attribute the
  whole window to the starting day. Overlap tiebreak: fewer days
  beats more, shorter duration beats longer, lexical profile name
  as final tiebreak — documented so overlaps are predictable.
  When no schedule matches, the alphabetically-first profile with
  no schedule is the default fallback. Manual
  `POST /api/profile/active` flips into a manual-override state
  that sticks until the next schedule transition (to or from the
  fallback) clears it. `GET /api/schedule/status` reports
  `active_profile`, `source` (schedule/manual), and the next
  upcoming transition. `PUT /api/profile/schedule` saves a
  schedule edit to disk. Panic suppresses scheduled switches.
  Refs #180.

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
