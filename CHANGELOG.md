# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases predating v0.5.0 are archived in
[docs/changelog/v0.4-and-earlier.md](docs/changelog/v0.4-and-earlier.md).

## [Unreleased]

### Headline

Non-hwmon HAL backends are now actually usable end-to-end. v0.7.1 → v0.7.3 fixed the install + verification dead-ends specifically for msi-ec on HudsonPH's MSI Thin GF63 12UDX (#1116 / #1154), but live-debugging on that box uncovered a deeper class of bug: three hardcoded `"is nvidia? → halnvml, else → halhwmon"` branches in code paths the wizard, controller, and dashboard all share. Every non-hwmon HAL backend ventd ships — **msiec, thinkpad, ipmi, nbfc, crosec, asahi, pwmsys, legion, corsair** (9 of 11 backends) — would hit at least one of these dead-ends. This release switches all three to registry-driven dispatch so any registered `hal.FanBackend` works without per-backend special-casing.

### Fixed

- `internal/config/config.go::validate` — fan-type whitelist rejected anything other than `hwmon` / `nvidia` with `unknown type %q (want: hwmon, nvidia)`. The wizard could never produce a loadable config for non-hwmon hardware: even after #1158 + #1159 got msi-ec built, signed, and installed, the wizard's config-write step then tripped over its own daemon refusing to load the result. Now: any non-empty fan type with a non-empty `pwm_path` passes the validator; the runtime backend lookup in `controller.New` + the dashboard's fan-read path are the actual gates that verify the type names a registered HAL backend. Tested across all 9 non-hwmon backend names in `TestValidateAcceptsHALBackendFanTypes`.
- `internal/controller/controller.go::New` — backend dispatch was a binary `if "nvidia" { halnvml.NewBackend } else { halhwmon.NewBackend }`. Every non-nvidia fan was routed into the hwmon backend, which then tried to write the PWM byte to whatever path the config gave as a file — for msi-ec that's `/sys/devices/platform/msi-ec/` (a directory), failing every tick with `open: is a directory`. Now uses a `switch` with `case "nvidia"` / `case "hwmon", ""` for the legacy in-tree backends + a `default` branch that looks the type up in the HAL registry via `hal.Backend(name)` so any registered backend (msiec, thinkpad, ipmi, …) wins dispatch. Falls back to hwmon with a WARN when the type names an unregistered backend, so legacy configs and typos surface a clear error at first read rather than nil-panic-ing the controller goroutine.
- `internal/controller/controller.go::channelFor` — `hal.Channel` construction also hardcoded the nvidia / hwmon dichotomy, embedding `halhwmon.State{PWMPath: c.pwmPath}` opaque for every non-nvidia fan. For msi-ec, the backend's `State` carries a per-board `WritableModes` slice derived from `/sys/devices/platform/msi-ec/available_fan_modes`; ThinkPad's carries the procfs path; NBFC's embeds the EC transport + matched catalogue config. None of those can be constructed inline from `(pwm_path, fan_type)` alone. Now defers to `c.backend.Enumerate` for any non-hwmon, non-nvidia type and looks up the channel by `ID == c.pwmPath`. Per-tick cost is one stat + one short ReadFile on most backends; negligible at the controller's 2 s tick.
- `internal/web/server.go::buildLiveStatus` — fan-read path on the dashboard had the same `nvidia vs hwmon` switch. For msi-ec users this surfaced as a `web: fan read failed: hwmon: read pwm /sys/devices/platform/msi-ec: is a directory` WARN every 2 s in the journal even while the controller drove the fan correctly (different code path). Now dispatches through `hal.Backend(fan.Type)` for non-legacy types; the backend's `Read` returns the centred-PWM equivalent of the current state (msi-ec's silent → 64, advanced → 192, etc.). RPM stays 0 for backends that don't expose tach — backends-that-don't-measure-RPM render "—" in the UI rather than fabricating a number (no-theatre rule).

### Refs

Closes the validator + dispatch trap behind #1116 / #1154. The same trap would have fired for every ThinkPad / Chromebook / IPMI server / Apple Silicon Mac / Lenovo Legion / Corsair user the moment they tried to configure ventd; this PR is a prerequisite for the wizard-side fan-control integration on any of those classes.

## [v0.7.3] - 2026-05-17

### Fixed

- `internal/hwmon/install_steps.go::stepVerify` — the post-modprobe install verification only polled `/sys/class/hwmon/` for `pwm*` files. For drivers whose control surface lives outside hwmon (msi-ec → `/sys/devices/platform/msi-ec/fan_mode`), this always failed, returned `ErrNoPWMChannelsAppeared`, and the wizard's chip-mismatch handler unloaded the (working) module. Net effect for HudsonPH and every other MSI laptop user on v0.7.2: msi-ec built and signed correctly, modprobed successfully, then got immediately unloaded by ventd before Phase 4 HAL discovery could see it. `stepVerify` now also consults `hal.Backend(c.Driver.HALBackend).Enumerate` when the new `DriverNeed.HALBackend` field is set, accepting any `CapWritePWM` channel as a successful install. The msi-ec catalog entry sets `HALBackend: "msiec"` to opt in; hwmon-shaped drivers (it87, nct6687d) leave the field empty and keep their existing hwmon-only verification unchanged. Closes the third dead-end in the #1154 chain (after #1156 fixed DKMS placeholder substitution and #1158 added the HAL backend itself).

### Headline

MSI laptops can now actually have their fans driven by ventd. The v0.7.1 routing work (#1120) + #1156 (DKMS placeholder fix) got the out-of-tree BeardOverflow/msi-ec module installed and loaded; this release closes the second dead-end in the chain — the calibration "no controllable fans found" monitor-only fallback that fired on every MSI laptop because msi-ec exposes mode-switching at `/sys/devices/platform/msi-ec/fan_mode` rather than the hwmon-style `pwm1` surface the setup wizard's discovery walked. Hudson's MSI Thin GF63 12UDX / MS-16R8 (issue #1154) is the canonical reproducer; CONF_G2_6 firmware group `E16R8IMS.117` exposes the `auto/silent/advanced` mode set this backend now drives.

### Added

- `internal/hal/msiec/` — new HAL backend implementing the `hal.FanBackend` interface over `/sys/devices/platform/msi-ec/` (the sysfs surface registered by [BeardOverflow/msi-ec](https://github.com/BeardOverflow/msi-ec), GPL-2.0). Same architectural shape as `internal/hal/thinkpad/` (which quantises 0-255 PWM to the firmware's 0..7 level grid): the backend reads `available_fan_modes` at probe time to discover the per-board mode set (CONF_G* groups expose different subsets), filters out `auto` as the Restore target rather than a daemon-driven mode, and quantises each 0-255 PWM Write into a mode-name command by placing band boundaries at the midpoints of the remaining set. Two-mode boards (auto/silent/advanced — Hudson's CONF_G2_6) split at PWM 128; three-mode boards (auto/silent/basic/advanced — CONF_G1) split at PWM 86/171. The inverse `modeToPWM` returns each mode's band centre so write→read→compare round-trips are stable. `Restore` writes `auto` to hand control back to the BIOS curve, matching the thinkpad backend's `level auto` semantics and the hwmon backend's `pwm_enable=2`. The msi-ec `realtime_fan_speed` sysfs entries are intentionally NOT mapped to `Reading.RPM` — the upstream driver exposes them as a 0..100/0..150 percent reading rather than a tachometer count, and the in-kernel `msi_wmi_platform` hwmon device provides canonical tach RPM on the same machines; fabricating RPM from a percentage would violate the no-theatre rule (#1031).
- `internal/hal/msiec/backend_test.go` — exhaustive table-driven tests covering `available_fan_modes` parsing (both newline- and space-separated, duplicates, forward-compat for unknown modes), 2-mode and 3-mode PWM quantisation boundaries, a 256-value write→read round-trip stability sweep, sysfs-failure error wrapping, opaque-state validation, and absent-sysfs/context-cancel paths in `Enumerate`. Hermetic via `t.TempDir()` fixtures — no `/sys` touch.
- `internal/setup/setup.go` Phase 4 — discovery now augments the hwmon walk + nvidia loop with a `hal.Enumerate()` sweep that picks up HAL-backend channels (msi-ec, future thinkpad/nbfc integration) that don't appear under `/sys/class/hwmon`. Channels from backends other than `hwmon` and `nvml` (which are surfaced through their dedicated paths) with at least one `CapWritePWM` capability get appended to the initial fan list with `DetectPhase: "found"` — same pattern as the NVIDIA branch immediately above it. Without this branch, a host with msi-ec loaded but no in-kernel PWM (Hudson's MSI Thin GF63 12UDX — `msi_wmi_platform` exposes RPM only) hit the silent monitor-only dead-end immediately after a successful OOT driver install. The existing Phase 5 RPM-detect and Phase 5b polarity-probe steps already gate on `f.Type == "hwmon"` so non-hwmon channels skip those phases naturally — the HAL backend reports RPM directly through `hal.Reading` per `RULE-HAL-002`. Enumeration failures demote to warn-and-continue so one broken backend never breaks discovery for the rest of the host's surfaces.
- `internal/watchdog/watchdog.go` — `Register` and `restoreOne` gain a `msiec` branch matching the existing `nvidia` dispatch shape: skip the `pwm_enable` read at Register time (msi-ec has no such file at `/sys/devices/platform/msi-ec_enable`), set `origEnable = -1`, and at restore time construct an `halmsiec.State{SysfsRoot: …}` channel for `Backend.Restore` to write `auto` against. The cross-cutting `RULE-WD-RESTORE-EXIT` safety contract now covers msi-ec channels on every documented shutdown path; without this branch the default-case hwmon dispatch would have tried to write `pwm_enable=2` to a path that doesn't exist and logged an error every graceful shutdown.

### Changed

- `cmd/ventd/calresolver.go::registerHALBackends` — registers the new `msiec` backend in the global HAL registry alongside `hwmon`, `nvml`, `thinkpad`, etc. The existing `newChannelResolver` closure resolves `msiec:/sys/devices/platform/msi-ec` IDs through the same `hal.Resolve` plumbing that already drives every other backend's calibration; no per-backend dispatch in the calibration pipeline.

### Refs

- Refs #1116 + #1154 (HudsonPH's two-issue chain on the MSI Thin GF63 12UDX). #1156 fixed the DKMS install path; this release closes the calibration dead-end behind it so the user actually gets fan control on their hardware, end-to-end.

## [v0.7.1] - 2026-05-16

### Headline

MSI laptops with `msi_wmi_platform` (tach-only, no in-kernel PWM) now get the out-of-tree [BeardOverflow/msi-ec](https://github.com/BeardOverflow/msi-ec) driver proposed automatically. Closes the silent "no fan controllers found" dead-end PR #1104 left behind on hosts like the MSI Thin GF63 12U (MS-16R8) reported in #1116.

### Fixed

- `internal/hwmon/autoload.go::identifyDriverNeeds` — new dispatch branch: when `msi_wmi_platform` is in the hwmon set AND the board vendor matches `micro-star` / `msi`, propose the new `msi_ec` `DriverNeed`. The trigger is narrow on purpose: `msi_wmi_platform` is laptop-specific (the in-kernel driver registers only for MSI WMI GUIDs on laptop firmware), so the "platform driver present + MSI vendor" combination is a strong "MSI laptop with no in-kernel PWM" signal. MSI desktop boards never expose `msi_wmi_platform` and stay on the existing `nct6687d` / `it8688e` routes unchanged.

### Added

- `msi_ec` entry in `knownDriverNeeds` pointing at `https://github.com/BeardOverflow/msi-ec` (main branch, GPL-2.0, DKMS-ready). The existing install pipeline handles build / sign / DKMS-register / modprobe / verify uniformly — no per-driver code path. On unsupported EC firmware revisions `modprobe` binds but exposes no `pwm1`; the wizard's retry-loop in `setup.go` treats that as `ErrNoPWMChannelsAppeared`, surfaces a clean monitor-only outcome, and logs the chip mismatch — an honest dead-end with a log trail instead of the v0.5.x / v0.7.0 silent 17-minute trap.
- Three new table-test rows in `internal/hwmon/autoload_test.go` pinning the gates: MSI short-vendor admits, non-MSI vendor with `msi_wmi_platform` does NOT trigger, MSI desktop MAG without `msi_wmi_platform` still routes to `nct6687d`. The existing post-#1104 row flips from `wantKeys: []` to `wantKeys: ["msi_ec"]` to lock in the new correct outcome.

### Refs

- Closes #1119, refs #1116. PR #1120.

## [v0.7.0] - 2026-05-16

### Headline

`internal/hal/thinkpad/` — new HAL backend driving Lenovo ThinkPad fans via the `thinkpad_acpi` procfs surface (`/proc/acpi/ibm/fan`). Completes the runtime half of the existing v1.3 catalogue work (the `thinkpad_acpi` driver profile + `lenovo-thinkpad.yaml` board entries were already shipped); previously the `rw_proc` capability had no HAL backend to honour it. Quantises every uint8 PWM input to the firmware's 0..7 level grid via round-half-up integer arithmetic, wraps the kernel's silent EPERM-on-`fan_control=0` as `ErrFanControlDisabled` so the existing modprobe-options-write recovery endpoint (`RULE-MODPROBE-OPTIONS-01`) dispatches on operator click, and restores via `"level auto"` per the driver profile's `exit_behaviour: restore_auto`. Writes are unconditional once `Enumerate` returns a channel — no per-backend opt-in flag, matching the v0.6.1 NBFC / Corsair posture.

### Added

- `internal/hal/thinkpad/` package: `Backend{Enumerate, Read, Write, Restore, Close, Name}` over `/proc/acpi/ibm/fan`, including:
  - `pwmToLevel(pwm uint8) uint8` — round-half-up quantisation `(pwm * 7 + 127) / 255` over the closed firmware grid `[0, 7]`.
  - `levelToPWM(level uint8) uint8` — inverse, centred on each level's quantisation band so write→read→compare round-trips are stable.
  - `parseProcFan` — multi-line `key:\tvalue` parser tolerant of missing speed; rejects out-of-range / non-numeric / non-keyword levels with `ErrInvalidProcFanResponse`.
  - Sentinels: `ErrFanControlDisabled`, `ErrProcFanAbsent`, `ErrInvalidProcFanResponse`.
- `docs/rules/RULE-HAL-THINKPAD.md` — 11 bound rules covering pwm/level quantisation, parseProcFan, Read empty-by-construction, enable-then-level write sequence, EPERM-as-typed-error wrap, level-auto-with-disable-fallback restore, Enumerate idempotence + ctx-cancel, Close idempotence, opaque-type guards.
- `internal/hal/thinkpad/backend_test.go` — exhaustive 256-value PWM sweep + boundary tables, hermetic via `t.TempDir()` procfs fixtures.

### Changed

- `cmd/ventd/calresolver.go::registerHALBackends` — registers the new `thinkpad` backend alongside `hwmon`, `nvml`, `ipmi`, etc.

### Tiers 1-4 absorption ship (squishy-sparking-pearl plan)

Twelve absorption targets from the catalog-sources plan land in one cycle. No HIL-gating, no per-backend opt-in flags — closed-set safety primitives (register allowlists, polarity-aware writes, watchdog Restore-on-exit, hard idle/container refusals) are the real protection layer per `feedback-dont-default-writes-off`.

- **T1.2 ASUS ROG laptop catalog** — three ROG board profiles (Strix G15 2022, Zephyrus G14 2023, TUF Gaming A15 2023) in `internal/hwdb/catalog/boards/asus.yaml`. Read-side via `asus-ec-sensors` mainline driver; control deferred to the `asusctl` userspace tool.
- **T1.3 Hysteresis** — confirmed existing implementation (`Curve.Hysteresis` field + `internal/controller/hysteresis_test.go`) is the canonical safety form (ramp-up-free, ramp-down-delayed). No additional ship.
- **T1.4 `ventd import-sensors-conf <path>` subcommand** — new `internal/hwdb/lmsensors` package parses upstream `sensors.conf` format (label / ignore / chip directives; compute/set deferred) and emits a ventd hwdb chip-overlay YAML. Wired through `cmd/ventd/import_sensors_conf.go`; `--out <path>` lands in `/var/lib/ventd/profiles-pending/`.
- **T2.1 Framework catalog + doctor card** — four Framework board profiles (13 Intel 11th-gen, 13 AMD 7040, 13 AMD AI 300, Framework 16) in `internal/hwdb/catalog/boards/framework.yaml`. Doctor branch in `vendor_remediation_d.go` points at `cros_ec_fan` (kernel 6.7+) + `fw-fanctrl` userspace.
- **T2.2 Dell laptop catalog** — five additional Dell entries (XPS 15 9570, Precision 7510, Latitude E6440, G3 3590) covering the `i8k_whitelist_fan_control` set in `dell-smm-hwmon`.
- **T2.3 liquidctl device catalog + integration** — new `internal/doctor/detectors/liquid_devices.go` with VID/PID metadata for NZXT (Kraken X3/Z3, Smart Device V1/V2, RGB & Fan Controller, Grid+ V3, H1 V2), Aquacomputer (D5 Next / Octo / Quadro), Corsair (Commander Core/ST/Pro/XT), Gigabyte (Waterforce). Consumed by `vendor_remediation_d.go::renderLiquidDeviceList` — each USB-vendor card includes the matched device list. Lookup helpers `LookupLiquidDeviceByVID` + `LookupLiquidDevice`.
- **T2.4 Clevo / System76 catalog + doctor card** — four board profiles (Oryx Pro, Lemur Pro, Clevo X170KM-G, Tongfang GM5) in `internal/hwdb/catalog/boards/clevo-system76.yaml`. Doctor branch points at `clevo-indicator` / `clevo-fancontrol` userspace + ventd's existing `internal/ec/dev_port` transport (the planned backend bedrock).
- **T3.1 HP Omen doctor card** — new `internal/doctor/detectors/hp_omen_d.go` recognises HP Omen / Victus DMI and emits an Info card pointing at `omen-fan` / `omen-fan-control`. Three Omen / Victus board profiles in `internal/hwdb/catalog/boards/hp.yaml` (Omen 16 2024 Intel, Omen 15 2023 AMD, Victus 16 2024) marked `overrides.unsupported: true`.
- **T3.2 Intel Mac catalog** — four `apple-mac.yaml` board profiles (MacBook Pro 15" 2018, 16" 2019, iMac 27" 2020, Mac Pro 2019). Doctor branch in `vendor_remediation_d.go` points at `mbpfan` + the kernel `applesmc` RPM-target write path. Apple Silicon excluded by virtue of no SMC equivalent.
- **T3.3 NZXT / Aquacomputer / Gigabyte device cards** — folded into T2.3's combined detector. Per-vendor doctor branches for each, with the device-catalog `renderLiquidDeviceList` consumed by every USB-vendor branch.
- **T4.1 fan2go competitive analysis** — `docs/research/2026-05-fan2go-competitive-analysis.md` documents the architectural delta vs fan2go (AGPL-3.0 — study-only). Key gap: ventd has no Prometheus exporter today; everything else ventd is at or ahead. Future spec slot: `spec-prometheus`.
- **T4.2 pwmconfig parity test** — `internal/calibrate/pwmconfig_parity_test.go` pins ventd's `DetectRPMSensor` against the canonical lm-sensors `pwmconfig` algorithm via two assertions: (a) RPM correlation wins over trailing-digit index alignment on boards with non-aligned tach wiring; (b) "no fan crossed the noise floor" maps cleanly to the `(empty RPMPath, nil)` no-winner contract.

### Added (Tiers 1-4 plumbing)

- `internal/doctor/detectors/hp_omen_d.go` + tests — HP Omen / Victus recognition.
- `internal/doctor/detectors/vendor_remediation_d.go` + tests — combined Apple Intel / Clevo / NZXT / Aquacomputer / Gigabyte / Corsair / Framework recognition; uses `liquid_devices.go` for PID-level naming in card detail.
- `internal/doctor/detectors/liquid_devices.go` + tests — vendored liquidctl device metadata table.
- `internal/hwdb/lmsensors/parser.go` + `overlay.go` + tests — sensors.conf parser + YAML overlay emitter.
- `cmd/ventd/import_sensors_conf.go` — new `ventd import-sensors-conf` subcommand wired into the argv router in `main.go`.
- `internal/hwdb/catalog/boards/apple-mac.yaml`, `framework.yaml`, `clevo-system76.yaml` — new vendor catalog files.
- `internal/calibrate/pwmconfig_parity_test.go` — parity assertions against lm-sensors `pwmconfig`.
- `docs/research/2026-05-fan2go-competitive-analysis.md` — fan2go vs ventd architectural delta.

### Changed (Tiers 1-4 plumbing)

- `cmd/ventd/doctor.go::runDoctor` — registers `NewHPOmenDetector()` + `NewVendorRemediationDetector()` in the cli detector slice.
- `cmd/ventd/main.go` — adds `ventd import-sensors-conf <path>` dispatch.
- `internal/hwdb/catalog/boards/asus.yaml`, `dell.yaml`, `hp.yaml` — extended with new entries.

## [v0.6.1] - 2026-05-16

### Headline

Two HIL-evidence gates that had been shipping write paths default-off "until field-validation accumulates" are removed: the v0.6.0 NBFC backend's `--enable-nbfc-write` flag and the v0.4.0 Corsair backend's firmware allowlist + `--unsafe-corsair-writes` flag. Both were "ship the code, wait for HIL to flip" patterns that produced zero operator value — every laptop user / Corsair user saw "your hardware is recognised but you can't actually use it". Per `feedback-dont-default-writes-off`: the closed-set primitives (register allowlist, pump-minimum floor, USB-reconnect floor, restore-on-panic, serialised writes, kernel-driver yield) are the real safety mechanism; per-model opt-in flags pending HIL are not.

### Changed

- **NBFC backend (`internal/hal/nbfc/`) writes default-on.** Removed `Backend.writeEnabled` field, `ProbeOpts.WriteEnabled`, `Probe(dmi, enableWrite)` parameter, `ErrNBFCWriteGated` sentinel, and the gate branches in `Write` / `Restore`. The HAL Backend now constructs a writable instance whenever the upstream catalogue resolves a non-Lua config and the EC transport opens. Rule renamed: `RULE-NBFC-HAL-WRITE-GATE` → `RULE-NBFC-HAL-DEFAULT-WRITES-ON`. Test renamed: `TestRULE_NBFC_HAL_WriteGated` → `TestRULE_NBFC_HAL_DEFAULT_WRITES_ON` (now pins the post-removal contract: writes succeed without any flag).
- **Corsair backend (`internal/hal/liquid/corsair/`) writes default-on.** Removed the `liveDevice` / `unknownFirmwareDevice` / `probeClass` type split, the empty `firmwareAllowList` map, and the `ProbeOptions.UnsafeCorsairWrites` field. `Probe` now returns a writable `corsairBackend` for any successfully-handshaken Commander Core / ST device regardless of firmware version. The `ErrReadOnlyUnvalidatedFirmware` sentinel is retained at the Write boundary as defence-in-depth (no production path constructs `writable=false` post-removal). Rules amended: `RULE-LIQUID-03` is now "defence-in-depth refusal at the Write boundary"; `RULE-LIQUID-06` is now "Probe returns writable unconditionally". Bound subtests preserved (same names; rewritten contents).
- **spec-09 doc + `docs/nbfc.md`** amended: the "Safety gate" section in `docs/nbfc.md` becomes "Safety mechanism" and names the load-bearing primitives (register allowlist, idle refuses, watchdog restore). The "default-off behind `--enable-nbfc-write`" framing in spec-09 is gone.
- **`internal/hal/gpu/registry.go` comment** clarifies that `--enable-gpu-write` is retained for the genuine NVIDIA driver-version constraint (`RULE-POLARITY-06`: R515+), distinct from the v0.6.1-removed HIL gates.

### Internals

- Auto-memory: `feedback-dont-default-writes-off.md` codifies the rule for future HAL backend work.
- Rule files touched: `docs/rules/liquid-safety.md` (RULE-LIQUID-03 + RULE-LIQUID-06 rewritten), `docs/rules/RULE-NBFC-B2.md` (gate prose deleted, rule renamed), `docs/rules/gpu-pr2d-01.md` (clarifies non-HIL framing).
- v0.6.0 ship-trail in `internal/web/CHANGELOG.md.embedded` is preserved verbatim — the v0.6.0 release-notes still describe what shipped then. v0.6.1 is an Added/Changed entry on top.

### Senior review pass

The kept gates (`--enable-gpu-write` for genuine R515+ driver constraints; `--allow-server-probe` for BMC fan-curve conflicts; `--enable-amd-overdrive` for `amdgpu.ppfeaturemask` kernel-taint; `--enable-soak-excitation` for synthetic Δpwm driver) are NOT HIL-evidence gates — each protects against a real underlying constraint distinct from "hardware tested in fleet yet?". Their flags stay. The audit covered `internal/`, `cmd/`, `specs/`, and `docs/rules/` for any HIL-evidence-gated path.

## [v0.6.0] - 2026-05-16

### Headline

Two load-bearing wins land in one tag: (1) smart-mode field-validation closes — Layer-B converged on the MSI Z690-A / NCT6687 (the original RFC #1024 failure host) with 6 channels carrying non-zero θ across thousands of samples, persisting across daemon restarts; (2) spec-09 NBFC backend ships across four PRs (A: catalogue match + doctor card; B1: pure-Go EC transport; B2: HAL backend; B3: ACPI bridge), bringing fan control to 311 laptop models from the upstream nbfc-linux community catalogue. The NBFC EC-write paths are default-off behind `--enable-nbfc-write` until per-model HIL evidence accumulates.

### Added

- **spec-09 PR A — NBFC catalogue + DMI matcher + doctor card.** 311 JSON configs vendored from `nbfc-linux/nbfc-linux@0.5.2` (GPL-3.0, license-compatible) under `internal/hwdb/nbfc/configs/`. The matcher resolves a live DMI tuple to a config via three tiers (exact, wildcard-glob, substring); the doctor detector emits one Fact per matched DMI naming the upstream `NotebookModel`, the source filename, and the control mode (`register-only`, `register-only (16-bit)`, `ACPI-method`, or `Lua-driven`). Hosts whose DMI doesn't match get a Warning with the upstream-contribution URL. The 311-config corpus parses cleanly under Go's strict `encoding/json` via an in-loader normaliser pipeline (`stripJSONComments` + `stripTrailingCommas` + `rewriteHexLiterals` + `stripLeadingZeros`) — five upstream configs use JSON5-style quirks the C parser tolerates. Rules: `RULE-NBFC-CATALOG-01..03`, `RULE-NBFC-CATALOG-JSONC-01`, `RULE-NBFC-DOCTOR-01`. New: `internal/hwdb/nbfc/`, `internal/doctor/detectors/nbfc_match_d.go`, `docs/nbfc.md`, `scripts/sync-nbfc-configs.sh`, Makefile `sync-nbfc-configs` target.
- **spec-09 PR B1 — pure-Go EC transport (`internal/ec/`).** Two transports: `ec_sys` debugfs (preferred — kernel handles OBF/IBF; one syscall per byte) and `/dev/port` direct I/O with the ACPI 4.0 §12.3 OBF/IBF handshake (fallback). Both CGO-free. `WithAllowlist(t, allowed)` wraps a raw transport in a closed-set register gate; every Read / Write / Read16 / Write16 validates the address against the active nbfc config's `RegistersUsed()` and refuses with `ErrECRegisterNotInConfig` without touching the EC. The `dev_port` transport's busy-wait loops honour a 1 ms per-step deadline and surface `ErrECBusy` on timeout — a wedged EC cannot busy-spin the daemon. Modprobe allowlist gains `ec_sys → write_support=1` so the existing `/api/hwdiag/modprobe-options-write` endpoint dispatches the remediation when the kernel module is loaded read-only. Rules: `RULE-NBFC-EC-01..06`.
- **spec-09 PR B2 — `internal/hal/nbfc/` HAL backend.** Satisfies the full `hal.FanBackend` contract over an EC transport + matched nbfc config. Enumerate (one channel per `FanConfiguration`), Read (8-bit or 16-bit register / ACPI dispatch + percentage-scaling), Write (clamp + scale via `pwmToRegister` + dispatch), Restore (per-fan `FanSpeedResetValue` + per-RegisterWriteConfiguration ResetValue with `Set` / `And` / `Or` / `Call` mode semantics), Close. Lua-using configs are refused at construction; ACPI configs require the bridge (PR B3); register configs require the EC transport. Default-off — `--enable-nbfc-write` gates Write / Restore. Rules: `RULE-NBFC-HAL-01..05`, `RULE-NBFC-HAL-WRITE-GATE`.
- **spec-09 PR B3 — ACPI method bridge (`internal/acpi/`).** Pure-Go writer to `/proc/acpi/call` (provided by the GPL-2.0+ `acpi_call` DKMS module). `Bridge.Call(method, args...)` formats requests as `"<method> [arg1] [arg2]..."` (matches `nbfc-linux/src/ec_acpi.c`) and parses both legacy-decimal and `0x`-hex response formats. Closed-set discipline via per-host allowlist drawn from `Config.AcpiMethodsUsed()`. Wired into the HAL backend's Read / Write / Restore dispatch so the 7 catalogue configs that drive fans via ACPI methods (HP Pavilion 17 Notebook PC, HP 250 G8 Notebook PC, Acer TravelMate P253, ASUSTeK X551CA, Acer Aspire E1-570G, plus two others) become controllable. `acpi.Available()` distinguishes module-not-loaded (ENOENT) from runtime-failure so the doctor surface dispatches the correct remediation. Rules: `RULE-NBFC-ACPI-01..05`.
- **spec-09 design document** at `specs/spec-09-nbfc-backend.md` — 7-section spec covering motivation, honest framing (Lua refused, ACPI requires acpi_call DKMS, EC writes default-off until HIL), PR breakdown, trigger conditions, the RULE-NBFC-* family skeleton, file map, and upstream citations.
- **Smart-mode soak observer** — `cmd/ventd-soak snapshot` and `ventd-soak watch` are the operator-facing tools for inspecting persisted Layer-B shard state without standing up a daemon. Read-only (RULE-SOAK-EXCITATION-OPT-IN); the excite subcommand remains gated behind `--enable-soak-excitation` and is documentation-only in this release.

### Changed

- **NVML laptop-dGPU error message.** `ErrLaptopDgpuRequiresEC` (`RULE-GPU-PR2D-06`) now points at `ventd doctor` rather than a bare "spec-09 NBFC backend" reference. Operators on laptop dGPUs see actionable guidance directing them at the new doctor card.
- **Doctor detector wiring.** The new `nbfc_match` detector registers alongside `ec_locked_laptop`; both surface complementary facts (one explains `platform_profile`, the other names the upstream nbfc config). Both fire on monitor-only hosts; the operator sees whichever applies.

### Field validation

- **RFC #1024 closed.** v0.5.37's soft-idle gate + group-aware OAT fixes are confirmed converged on the original failure host (MSI Z690-A / 13900K / NCT6687, post-migration to Proxmox role). Layer-B `Snapshot.Theta` carries non-zero values across all 6 channels with `n_samples` ranging from 1755 to 16872, `tr(P)` near zero, persisting across daemon restarts. Four of six channels (pwm1/3/5/6) share identical θ values — the firmware-mirrored fan group the v0.5.37 release notes predicted for NCT668x. The smart-mode pipeline is the v0.6.0 ship-gate question; it's empirically answered.

### Internals

- New rule files: `docs/rules/RULE-NBFC-A.md`, `docs/rules/RULE-NBFC-B1.md`, `docs/rules/RULE-NBFC-B2.md`, `docs/rules/RULE-NBFC-B3.md`. Twenty-two new bound subtests across the four files.
- Rulelint passes at 401 rule(s), 683 bound(s) verified — zero errors; only pre-existing unclaimed-subtest warnings remain.
- Modprobe options allowlist extended: `ec_sys → write_support=1` joins `thinkpad_acpi → fan_control=1`. Three new bound subtests in `internal/hwmon/modprobe_options_test.go`.

### Deferred

- `acpi_call` DKMS catalogue row in `internal/hwdb/profiles-v1.yaml`. The bridge ships; ventd's existing DKMS install pipeline (`legion_laptop`, `nct6687d`) handles the install path manually for v0.6.0 GA. The catalogue row + automated install wiring lands when a HIL operator on one of the 7 ACPI-config laptop models reports back.
- Operator-facing `--enable-nbfc-write` flag plumbing into `cmd/ventd/main.go`. The HAL Backend's `WriteEnabled` gate is in place; the daemon-level flag wiring lands alongside the first HIL run on a real nbfc-supported laptop.
- Wizard / setup integration for NBFC-matched hardware. The doctor card is the v0.6.0 surface; the wizard's first-boot flow that runs Probe + presents enable-write opt-in is a v0.6.x follow-up.

### Senior review pass

The largest single release since v0.5.0 — both the smart-mode convergence question (the original v0.6.0 ship gate) and the spec-09 NBFC backend (originally targeted for v0.8.0) land together. The justification for pulling spec-09 forward: smart-mode convergence was the load-bearing v0.6.0 question, and answering it empirically opens headroom in the v0.5.x → v0.6.0 tag's scope. The NBFC catalogue surface unlocks fan control on 311 laptop models the previous architecture couldn't reach; the write paths ship default-off so HIL evidence accumulates before the gate flips per-model.

## [v0.5.39] - 2026-05-16

### Headline

Polarity probe rewritten as a bipolar low/high pulse pair — fans whose BIOS auto-curve held them at high baseline PWM going into the wizard no longer misclassify as inverted (and then cascade into phantom via the post-cal sanity check). The controller's hot path also gains a safety net: on the first polarity refusal per channel, the watchdog hands the channel back to BIOS auto so a misclassified fan never sits at PWM=0 waiting for the next daemon restart. Closes the 2026-05-15 incident on a 13900K / NCT6687 box where every controlled channel got refused and stalled at PWM=0 for nearly an hour.

### Fixed

- **Bipolar polarity probe (RULE-POLARITY-13).** `HwmonProber` and `NVMLProber` now drive `BipolarLowPWM` (51 ≈ 20%) followed by `BipolarHighPWM` (204 ≈ 80%), each held for `BipolarPulseHold` (2 s), and classify on the delta between the two pulses. The pre-fix algorithm wrote a single midpoint (128 / 50%) and compared against the pre-write baseline RPM — that misclassified every normal fan whose baseline PWM was already above midpoint, which is the common case when BIOS auto-curves are running fans at 70-100% going into the wizard. A fan at PWM=255 / 2300 RPM slowed to ~1500 RPM under PWM=128, producing `delta = -800` and a false-inverted label. The bipolar replacement is baseline-PWM-invariant. Mirrors the pattern `internal/validity/` already uses (RULE-CALIB-PR2B-01). Rules POLARITY-01 / POLARITY-02 amended to match.
- **Controller safety handback on polarity refusal (RULE-POLARITY-12).** When `polarity.WritePWM` returns `ErrChannelNotControllable` (phantom) or `ErrPolarityNotResolved` (unknown), the controller now dispatches `wd.RestoreOne(pwmPath)` on the first refusal per controller lifetime — handing the channel back to BIOS auto rather than leaving it at whatever PWM the calibration sweep last committed (typically 0). The per-controller `polarityHandedBack` flag silences subsequent refusals so journald isn't filled at controller poll-rate. Critical for AIO pumps on misclassified channels where a stuck PWM=0 means no coolant circulation.
- **Polarity-aware phantom-verify.** Phase 6b's `verifyHwmonChannelSpins` now writes raw 0 for "inverted" channels and raw 255 for normal channels (i.e. effective 100% in both cases). Pre-fix, the verify wrote raw 255 unconditionally, which on a genuinely-inverted channel (NCT6683 on MSI, IT87 on some Gigabyte) is 0% effective duty → 0 RPM → false re-classification as phantom. The previously-detected "inverted" classification would then be lost and a perfectly working channel excluded from `controls:`. The `RULE-SETUP-PHANTOM-VERIFY` rule text in `setup.md` is amended to cover the polarity-aware path.

### Internals

- New rules: `RULE-POLARITY-12.md` (controller refused-handback) and `RULE-POLARITY-13.md` (bipolar probe). Amended `RULE-POLARITY-01.md`, `RULE-POLARITY-02.md`, and the `RULE-SETUP-PHANTOM-VERIFY` block in `setup.md` to reflect the new algorithm + polarity-awareness.
- `polarity.HwmonProber.ProbeChannel` now ignores baseline RPM for classification (used only for the restore). Baseline PWM is still captured and restored on every exit path (RULE-POLARITY-04 preserved).
- `polarity.NVMLProber.ProbeChannel` follows the same shape: set manual policy → LOW pulse → HIGH pulse → restore → classify.
- New constants in `internal/polarity/polarity.go`: `BipolarLowPWM`, `BipolarHighPWM`, `BipolarLowPct`, `BipolarHighPct`, `BipolarPulseHold`.
- New bound tests: `RULE-POLARITY-13_bipolar_baseline_invariant/hwmon_normal_fan_high_baseline_classifies_normal`, `polarity_refused_phantom_hands_back_to_bios_auto_once`, `inverted_polarity_writes_raw_zero_not_255`.

### Senior review pass

The NCT6687 chip's per-channel `pwm_enable` EINVAL on value 2 (it accepts only {1, 5}) is independent of this fix — the watchdog's `RestoreOne` dispatches the captured `origEnable`, and the chip-specific EINVAL fallback chain (RULE-HWMON-ENABLE-EINVAL-FALLBACK) lives in `setup.go`'s `restoreExcludedChannels`, not `restoreOne`. A separate PR should hoist the EINVAL probe-and-fallback into the watchdog path so the safety handback works uniformly on every chip; filed as a follow-up issue.

## [v0.5.38] - 2026-05-15

### Headline

MSI laptops with `msi_wmi_platform` no longer trigger a wasted IT8688E driver-install cycle. Cleaner journal output on this hardware; the correct end-state (monitor-only) is unchanged.

### Fixed

- **MSI laptop IT8688E vendor fallback (#1103, #1104).** `identifyDriverNeeds()` in `internal/hwmon/autoload.go` proposed `it8688e` for any MSI board whose `board_name` didn't match the `nct6687d` DMI triggers — but MSI gaming laptops (MS-16R8, MS-17K2, etc.) expose `msi_wmi_platform` in hwmon for RPM readings only and have no ISA ITE Super-I/O chip for `it87` to bind to. Pre-fix sequence: wizard compiled + DKMS-registered IT87 → `modprobe it87` found nothing to bind → host fell through to monitor-only mode after a misleading "Installing IT8688E fan controller driver — this may take a minute..." line in the journal. The fix mirrors the existing ASUS `asus_ec` / `asus_ec_sensors` guard: when `msi_wmi_platform` is in the hwmon set, suppress the MSI fallback entirely. ASRock and Biostar branches are left alone — neither ships laptops behind WMI platform drivers. Surfaced via the diag bundle from an Ubuntu 25.10 MSI MS-16R8 (Intel i5-12450H).

### Internals

- No new rule files. The fix is a one-line guard mirroring an existing pattern; coverage lands as a new subtest in the table-driven `TestIdentifyDriverNeeds` in `internal/hwmon/autoload_test.go` (now 18 cases, exercising the exact MS-16R8 + `msi_wmi_platform` signature from the diag bundle).

### Senior review pass

The host's correct end-state on this hardware remains monitor-only — full fan *control* on MSI laptops needs a userspace EC backend (nbfc / clevo-style), which is v0.7+ scope and an upstream/firmware reality rather than something a kernel module can patch around. This release just stops the wasted install cycle + the misleading journal line on the affected hardware class.

## [v0.5.37] - 2026-05-11

### Headline

Smart-mode field-validation fixes — both halves of the RFC #1024 closure: soft-idle gate (time domain) + group-aware OAT (topology domain). Phase C5 HIL evidence on Phoenix's MSI Z690-A (desktop, 26.5 h) and Proxmox host (9.6 h) confirmed v0.5.x's smart-mode pipeline structurally cannot advance under realistic workload: the 600 s sustained-idle durability + tight PSI thresholds closed the opportunistic gate > 99 % of ticks; on the desktop, even with probes firing, the firmware-mirrored 4-fan group would have OAT-rejected every Layer-C admission. v0.5.37 ships both fixes so re-soak can demonstrate convergence on either / both failure modes.

### Changed

- **Default `OpportunisticGate` flips from strict to soft (#1030).** New `IdleGateMode` enum on `OpportunisticGateConfig`; `ModeSoftIdle` is the zero value and the v0.6.0+ default. Single-shot evaluation against relaxed PSI thresholds (`cpu.some avg60 < 10.0 %`, `io.some avg60 < 10.0 %`, mem unchanged at `0.5 %`) and a relaxed loadavg fallback (`0.50 × ncpus` vs strict `0.10 × ncpus`). The 600 s durability loop is dropped — the scheduler's 60 s tick cadence supplies the temporal envelope. Cross-tick IRQ delta detection moves to a caller-owned `IRQBaseline *IRQCounters` that the scheduler initialises once per scheduler-lifetime and passes on every tick. All hard guards remain unchanged in soft mode: hard preconditions (battery / container / scrub / blocked-process / post-resume warmup per RULE-OPP-IDLE-04); process blocklist (RULE-IDLE-06); input-IRQ delta (RULE-OPP-IDLE-02, now uses caller-owned baseline); active-SSH (RULE-OPP-IDLE-03). `ModeStrictIdle` preserves the legacy v0.5.x evaluator. Operator escape hatch via `--strict-idle-gate` on the daemon CLI for hosts where the soft thresholds prove too permissive.

- **`marginal.Runtime.SetPWMGroups([][]string)` makes the Layer-C OAT gate group-aware (#1031).** Declares operationally co-moving channel sets (firmware-mirrored siblings or single-PWM-register fan-out). The OAT gate `oatGate(channelID)` exits early when the candidate other-channel's group key matches the admitting channel's group key — intra-group movement is excluded from the quiet-window check. Channels outside the group still gate normally; cross-channel-interference protection is preserved for genuinely-independent channels. Ungrouped channels act as size-1 groups via the `groupKey()` fallback; the empty-map default preserves exact v0.5.x semantics. The catalog-to-runtime plumbing (`hwdb.BoardProfile.PWMGroups` → `Runtime.SetPWMGroups` via the matcher) lands in a follow-on PR once HIL polarity data on the Z690-A confirms which sysfs channels actually co-move; today the infrastructure is dormant but tested (six dedicated subtests bound to amended RULE-CMB-OAT-01).

### Internals

- New `RULE-OPP-IDLE-SOFT-MODE` in `docs/rules/opportunistic.md` — bound to six subtests in `internal/idle/opportunistic_test.go` covering the load-bearing single-shot guarantee (< 500 ms wall-clock vs ~600 s strict loop), the soft PSI ceiling refusal, the canonical RFC #1024 "soft admits where strict refuses" case at `cpu.some avg60 = 3 %`, mode-constant pinning (zero-value must stay soft), and the nil-baseline first-call admit branch.
- `RULE-OPP-IDLE-01` amended — durability constant applies in `ModeStrictIdle` only; constant and strict-mode behaviour preserved as operator escape-hatch contract. `RULE-OPP-IDLE-02` amended + second binding added (soft and strict modes both enforce input-IRQ delta refusal; soft uses caller-owned baseline).
- `RULE-CMB-OAT-01` amended for the v0.6.0 group-aware form; pre-v0.6 "j ≠ i" form replaced with "j NOT IN i's PWM group". Five new binding subtests under the amended rule cover intra-group co-movement admit, extra-group movement still rejects, ungrouped channels behave as size-1 groups (v0.5.x semantics preserved), single-member groups silently dropped, and SetPWMGroups idempotency.

### Senior review pass

These two pieces land together as Phase A's reference fixes for the v0.6.0 ship-plan smart-mode-convergence question (#1024). Path A unblocks the time domain (probes can fire during workload lulls); Path B unblocks the topology domain (grouped fans don't OAT-reject each other). Either alone leaves one of the two known field hosts stuck. The catalog-side `pwm_groups` data for the Z690-A is the next surface — gated on HIL polarity verdict to author correctly. Re-soak runs against v0.5.37 alone; if convergence happens on Path A alone, Path B's infrastructure stays dormant in production (no behaviour change). If re-soak shows persistent OAT rejection on the Z690-A, the catalog-data follow-on activates Path B's machinery immediately.

## [v0.5.36] - 2026-05-10

### Headline

Setup-wizard hardening pass — closes two HIL-discovered bugs that landed phantom channels in `controls:` on Phoenix's IT8688 host (#1025, #1026). The CLI standalone wizard (`ventd -setup`) now actually works (it never did since the calibrate.Manager refactor); the web wizard now classifies phantom channels correctly (RULE-POLARITY-03 was structurally dead code).

### Fixed

- **`ventd -setup` CLI fails with "no fans detected" — wizard wiring missing on the standalone setup path (#1025, #1027).** `runSetup` constructed a `calibrate.Manager` but never called `SetChannelResolver`, and never registered any HAL backends with the package-level registry. Every channel-resolution attempt returned `"detect: no channel resolver set for <pwm_path>"`, the wizard logged five rpm-detect failures, classified all five channels as `detect_failed`, handed them back to BIOS auto, and aborted with the no-fans-detected fatal. Surfaced on Phoenix's Proxmox host (192.168.7.10, IT8688, 5 hwmon3 channels) during a path-2 reset; the web UI wizard worked correctly on the same host because it goes through a different code path. The fix extracts the wiring into two shared helpers in a new `cmd/ventd/calresolver.go` (`newChannelResolver()` + `registerHALBackends(logger, enableGPUWrite)`); `runDaemon` and `runSetup` both call them now. Tests pin the `nvidia → nvml` dispatch remap and the hwmon pass-through; a regression that drops the remap silently breaks NVIDIA GPU calibration on the daemon path AND the CLI path simultaneously.

- **Wizard includes phantom channels in `controls:` even though polarity probe should classify them phantom (#1026, #1028).** Three-layer defense-in-depth fix:
  - **Layer 1: wire `SetPolarityProber` in runDaemon + runSetup.** `SetPolarityProber` was never called by any production code path; the wizard's Phase 5b polarity-probe block (`internal/setup/setup.go:1097-1161`) was `if prober != nil { ... }` and prober was always nil. RULE-POLARITY-03's |delta| < 150 RPM phantom-classification rule was dead code in production. `polarity.HwmonProber{}` zero-valued is production-ready; one-line wiring in `cmd/ventd/main.go` (covers daemon-spawned web wizard since `web.New` is constructed with the daemon's `setupMgr`) and `cmd/ventd/runsetup.go` (covers CLI standalone).
  - **Layer 2: pre-ramp stability gate fronting `DetectRPMSensor` (RULE-CAL-DETECT-STABILITY).** Three baseline samples per `fan*_input` spaced 200 ms apart; refuse the sweep if any tach's stddev exceeds 50 RPM. Catches the chip-residue / pwm_enable-transition case where a tach's first read after a chip mode change returns a transient nonzero value, fooling the post-ramp delta check on phantom channels. New `stdDevInt([]int) float64` helper kept local to `internal/calibrate`.
  - **Layer 3: post-calibration phantom-verification pass (RULE-SETUP-PHANTOM-VERIFY).** New `verifyHwmonChannelSpins` helper drives PWM=255 (full speed) for 3 s, takes three RPM samples spaced 200 ms apart, and re-classifies the channel as phantom (`CalPhase = "skipped"`, `PolarityPhase = "phantom"`) if every sample reads 0. The deferred restore writes the captured `origPWM` byte back on every exit path. Sysfs IO errors → admit (graceful degrade per RULE-DOCTOR-04 pattern). Cost: +3 s per `done` hwmon channel; +15 s on a typical 5-fan host's once-per-install wizard runtime.

### Internals

- New `RULE-CAL-DETECT-STABILITY` in `docs/rules/calibration-safety.md` — bound to `internal/calibrate/stddev_test.go` (3 subtests: known-values, below-threshold-admits, above-threshold-refuses).
- New `RULE-SETUP-PHANTOM-VERIFY` in `docs/rules/setup.md` — bound to `internal/setup/phantom_verify_test.go` (2 subtests: full coverage of admit/refuse/error/ctx-cancel + origPWM-restored-on-all-exit-paths).

### Senior review pass

These two issues were surfaced during Phase C5 HIL field-validation on Phoenix's Proxmox host (the parallel-soak run from issue #1024) — the kind of "discoverable only with real hardware in real conditions" bug class that was the load-bearing reason Phase C exists in the v0.6.0 ship plan. Both are fixed in v0.5.36; v0.6.0 stays gated on the smart-mode convergence question (issue #1024).

## [v0.5.35] - 2026-05-08

### Headline

Item B1 of the v0.6.0 ship plan (#1021) — closes the senior review's H10 finding on the three-package naming collision (`calibrate/` / `calibration/` / `probe/`). The package's job is to validate whether a hwmon channel can be controlled at all (polarity correct? not stalling? not BIOS-overridden?); calling it "calibration" obscured that distinction from `internal/calibrate` (the legacy V-model PWM sweep).

### Changed

- **`internal/calibration` renamed to `internal/validity` (#1021).** The three-package boundary is now self-documenting: `internal/calibrate/` for the legacy V-model PWM sweep + curve fitting, `internal/validity/` for the PR-2b channel-validity probe (polarity / stall / BIOS-override), `internal/probe/` for the catalog-less smart-mode primary path. Mechanical scope: 6 .go files + 5 testdata YAMLs moved via `git mv`; package decl rewritten in 5 source files + the external test package; import paths rewritten in `cmd/ventd/main.go`, `cmd/ventd/calibrate.go`, and `internal/validity/probe_test.go`; the `calibstore` import alias in main.go dropped (the package is now imported plainly as `validity` since there's no collision with `calibrate`); 9 `RULE-CALIB-PR2B-*.md` rule files had their `Bound:` paths rewritten from `internal/calibration/probe_test.go` to `internal/validity/probe_test.go`. `tools/rulelint` after the rewrite: `ok: 361 rule(s), 556 bound(s) verified` — zero unclaimed bindings.

### Internals

- New `RULE-PKG-VALIDITY-PROBE-BOUNDARY.md` documents the three-package taxonomy + the v0.6.x deprecation gate where `calibrate/` shrinks to a fallback once smart-mode field-validation completes (Phase C of the v0.6.0 ship plan).
- `CLAUDE.md` gains an **"Architectural Lens — Smart-Mode Pivot (Fork D)"** section spelling out the three-package boundary + a link to `docs/research/r-bundle/smart-mode-handoff.md` (closes the senior review's M22 medium-tier item — "Document smart-mode-handoff explicitly in CLAUDE.md").
- `.gitignore` adds `/screenshots/` so the locally-rendered screenshot dir (`feedback_keep_screenshots.md` — Phoenix uploads to README manually) doesn't accidentally land in commits.

### Senior review pass

This wraps Phase A + B of the v0.6.0 ship plan. Phase C — smart-mode HIL field-validation across the 5-host fleet — is the actual v0.6.0 ship gate per the smart-mode handoff doc. Phase A items closed across v0.5.30–v0.5.34 (item 4-H8 schema migrator, item 2 fresh-install probe gate, items 2/3 CSRF + body-size + SameSite=Strict, item 4 fakehwmon canonical chip-quirk helpers, item 6 server.go split A5.1, item 6 main.go split A5.2). Phase B item B1 closes here. The remaining server.go splits (calibrate / setup / status / config handler clusters) are deferred — server.go is now at 2064 LOC, down from 2488 in v0.5.32; further splits are nice-to-have but not v0.6.0-blocking.

## [v0.5.34] - 2026-05-08

### Headline

Second slice of the v0.6.0 ship plan's A5 file-split refactor (#1019). Pure mechanical move with zero behaviour change; the call sites in `main.go`'s `run()` / `runDaemonInternal()` still invoke these functions directly (same package).

### Changed

- **`cmd/ventd/main.go` reduced from 2072 → 1640 LOC (−21%, #1019).** Smart-mode builders extracted into new `cmd/ventd/smart_builders.go` (472 LOC): `buildOpportunisticScheduler` (v0.5.5 Layer-A gap-fill probe), `buildCouplingRuntime` (v0.5.7 Layer-B coupling RLS), `buildMarginalRuntime` (v0.5.8 Layer-C marginal RLS), `buildLayerAEstimator` (v0.5.9 conf_A four-term), `buildAggregator` (v0.5.9 R12-locked confidence aggregator), `buildBlendedController` (v0.5.9 IMC-PI blended), `buildBlendFn` (v0.5.9 BlendFn closure), and `captureLoadFraction` (`/proc/loadavg` → load fraction bridge). Imports trimmed to just what the new file needs: `slog`, `atomic`, `time`, plus the smart-mode subsystem packages (aggregator, layer_a, controller, coupling, signguard, fallback, idle, marginal, observation, probe, opportunistic, state, sysclass, config). main.go drops the now-unused `internal/fallback` import.

### Senior review pass

Remaining A5 work — further server.go splits (calibrate / setup / status / config handler clusters) — and B1 (internal/calibration → internal/validity rename + RULE-CALIB-PR2B-* binding-path rewrites) deferred to v0.5.35. Per the user's "no more ignoring CIs if they fail we fix" directive, smaller well-bounded slices keep the risk surface low.

## [v0.5.33] - 2026-05-08

### Headline

First slice of the v0.6.0 ship plan's A5 file-split refactor (#1017). Pure mechanical move with zero behaviour change; the route table in `server.go`'s `registerAPIRoutes()` still points at the same methods on the same Server struct.

### Changed

- **`internal/web/server.go` reduced from 2488 → 2064 LOC (−17%, #1017).** Smart-mode HTTP handlers extracted into new `internal/web/smart_handlers.go` (446 LOC): the `confidenceChannel` + `confidenceStatus` JSON wire types, `handleConfidenceStatus` (GET /api/v1/confidence/status), `handleConfidencePreset` (GET/PUT /api/v1/confidence/preset), `handleSmartStatus` (GET /api/v1/smart/status), `handleSmartChannels` (GET /api/v1/smart/channels). Imports trimmed to what the new file needs: `encoding/json`, `net/http`, `internal/config`, `internal/coupling`, `internal/marginal`. Aggregator + controller imports stay in server.go where the Server struct fields live.

### Senior review pass

The remaining A5 work — further server.go splits (calibrate / setup / status / config handler clusters) and `cmd/ventd/main.go` split (daemon body + smart-mode builders) — is deferred to v0.5.34. Per the user's "no more ignoring CIs if they fail we fix" directive, shipping smaller well-bounded slices reduces the risk surface vs one big mechanical refactor that could surface subtle test regressions.

## [v0.5.32] - 2026-05-08

### Headline

Item A4 of the v0.6.0 ship plan — the test-fixture infrastructure the senior review flagged as the highest-leverage test improvement available. `internal/testfixture/fakehwmon` gains four canonical chip-quirk helpers (`InjectSentinelRPM`, `SimulateBIOSRevert`, `SimulateFanResponse` with normal/inverted polarity, `ReassertPWMEnable`) plus four matching opt-in `PWMOptions` fields documenting the firing cadence. Tests can now exercise rules end-to-end through synthetic chips that misbehave like the real ones (nct6687 sentinel, it8689e BIOS-revert, inverted-polarity fans, Gigabyte Q-Fan reassertion).

### Internals

- **`Fake.InjectSentinelRPM(chipIndex, fanIndex)`** writes the new `SentinelRPMValue=65535` constant (the 0xFFFF nct6687 sentinel) to `fan*_input`. Exercises `RULE-HWMON-SENTINEL-FAN`, `RULE-SENTINEL-FAN-IMPLAUSIBLE`, `RULE-HWMON-INVALID-CURVE-SKIP`, `RULE-HWMON-PROLONGED-INVALID-RESTORE` end-to-end through `controller.tick` rather than just the backend boundary.
- **`Fake.SimulateBIOSRevert(chipIndex, pwmIndex, originalValue)`** writes `originalValue` back to `pwm*`, modelling the it8689e accept-then-revert pattern. Tests sequence: backend-write → first-readback → SimulateBIOSRevert → second-readback. Exercises `RULE-CALIB-PR2B-06`, `RULE-ENVELOPE-14` against a real read-write-readback path.
- **`Fake.SimulateFanResponse(chipIndex, pwmIndex, fanIndex, maxRPM, inverted)`** reads `pwm*` and writes the corresponding RPM (linear when `inverted=false`, inverse-mapped when `inverted=true`). Lets closed-loop tests exercise `RULE-POLARITY-02`, `RULE-CALIB-PR2B-02`, `RULE-OPP-PROBE-04` against synthetic chips whose fan reading actually responds to the daemon's PWM writes.
- **`Fake.ReassertPWMEnable(chipIndex, pwmIndex, value)`** writes `value` to `pwm*_enable`, modelling Gigabyte Q-Fan / Smart Fan Control reassertion. Tests pair with their own EBUSY-injecting stub on `writePWMFn` to exercise `RULE-HWMON-MODE-REACQUIRE`'s single-retry contract against a stateful fixture.
- **Four matching `PWMOptions` fields** (`EmitSentinelRPMEvery`, `BIOSRevertAfter`, `InvertedPolarity`, `EBUSYReassertEvery`) document the intended firing cadence so v0.6+ wiring can drive helpers automatically.

Helpers are explicit (test calls them between backend operations) because the fake is file-backed and the production hwmon backend reads/writes via `os.ReadFile`/`os.WriteFile` directly — there is no interception point on the read or write path.

Bound to new `RULE-FAKEHWMON-QUIRK-HELPERS` in `docs/rules/hwmon-sentinel.md` with 8 leaf subtests in `internal/testfixture/fakehwmon/quirks_test.go`.

### Fixed

- **`TestManager_StartTransitionsToRunningThenDone` race fix (#1015 follow-up).** The test asserted `Running=true` immediately after `Start()` returned, which is racy on fast GHA runners — the wizard's goroutine can complete before the test observes Progress. Caught on `build-and-test-ubuntu-22-04` per the user's "no more ignoring CIs if they fail we fix" directive. The test now accepts `Running=true OR Done=true` as valid post-Start states; the contract is "Start scheduled the work" and both observable states satisfy that.

### Senior review pass

The follow-up "migrate ~40 existing rule subtests to consume the helpers" deliverable (the senior review's "exercises lines → exercises behaviour" step) and the systematic `goleak.VerifyTestMain` rollout to `internal/web` / `controller` / `observation` / `marginal` / `coupling` are deferred to subsequent patches. v0.5.32 ships the FOUNDATION; the migration is mechanical but voluminous and benefits from being a separate review. The goleak rollout will surface real leaks in long-running goroutine packages (web server, schedulers, runtimes) and needs separate cleanup work.

## [v0.5.31] - 2026-05-08

### Headline

Web-security hardening item A3 of the v0.6.0 ship plan. Three layered CSRF defences land together — per-request CSRF token, `SameSite=Strict` on the session cookie, and a uniform 1 MiB body cap on every authenticated state-changing route. A forged cross-origin request now needs all three layers to fail simultaneously to succeed.

### Fixed (security)

- **Per-session CSRF token (#1011, RULE-WEB-CSRF-TOKEN-REQUIRED-ON-STATE-CHANGE).** `sessionStore.create` now generates a random 32-byte CSRF token alongside the session token and pairs them under a new `sessionData` struct. `requireCSRF` middleware in `internal/web/csrf.go` constant-time compares the `X-CSRF-Token` header against the session's bound token via `subtle.ConstantTimeCompare`. Safe methods (GET / HEAD / OPTIONS) bypass; POST / PUT / PATCH / DELETE without a valid header → 403. `handleLogin` and `handleFirstBootLogin` set the `ventd_csrf` cookie (non-HttpOnly so JS can read it) AND return `csrf_token` in the response JSON; `handleLogout` clears both cookies. The fetch monkey-patch in `web/shared/brand.js` (loaded by every HTML page) reads the cookie and injects the header automatically — zero per-page JS changes.
- **`SameSite=Strict` on session + CSRF cookies (#1011, RULE-WEB-COOKIE-SAMESITE-STRICT).** Pre-v0.5.31 the session cookie was `SameSiteLaxMode`, which permitted cross-site form-POST navigations to carry the session. Strict refuses to attach the cookie to any cross-site navigation — including top-level link clicks, form submits from another tab, and `window.open` redirects. The CSRF cookie matches.
- **1 MiB body cap on every authed state-changing route (#1011, RULE-WEB-BODY-SIZE-CAP-1MIB).** New `requireMaxBody(defaultMaxBody, h)` middleware wraps every `apiRoute` in `registerAPIRoutes` so oversized POST / PUT / PATCH / DELETE bodies surface as `MaxBytesError` → handler emits 413 via the existing `isMaxBytesErr` check. Today only `/api/v1/config` and `/login` had body caps applied directly in the handler; now every state-changing endpoint has the cap uniformly via the route table.

### Internals

- New `internal/web/csrf.go` with `csrfCookie`, `setCSRFCookie`, `clearCSRFCookie`, `requireCSRF`, `requireMaxBody`.
- `sessionStore` API: `csrfFor(token)` returns the session's bound CSRF token; `valid()` and `reap()` updated to read from the new `sessionData` struct.
- Middleware composition in `registerAPIRoutes`: `requireMaxBody → requireAuth → requireCSRF → handler`. CSRF check fires before the auth check looks up the session — the auth gate is the outer wrapper so the session cookie is read first; CSRF then compares the header against the bound token.
- Client wiring (`web/shared/brand.js`): a fetch monkey-patch at the top of the file reads the `ventd_csrf` cookie and injects `X-CSRF-Token` on every state-changing fetch. Existing `fetch()` calls in 12+ page-specific JS files work transparently.
- Test-side helper `authAndCSRF(t, req, srv, sessionTok)` in `internal/web/csrf_test_helpers_test.go` sets both the session cookie and the X-CSRF-Token header in one call so impacted tests (8 across 6 files) change a single line each.

### CI

- `internal/marginal:TestRuntime_OnObservationNonBlocking` flaked on `build-and-test-ubuntu-arm64` under race detector + arm64 GHA runner pressure — a 1 ms timing assertion in RULE-CMB-RUNTIME-02. Tracked at #1012; not gating per `feedback_surgical_merge.md` (canonical 5 lanes green).

## [v0.5.30] - 2026-05-08

### Headline

First half of the v0.6.0 ship-plan Phase A. Two unrelated mechanical changes that ship together because both are tiny: a state-schema migrator slot reservation for v0.6, and the fresh-install opportunistic-probe gate dropped from 24 h to 0. The probe-gate change is operator-visible — fresh installs now start opportunistic probing as soon as the standard idle preconditions are met, not 24 h later.

### Changed

- **Fresh-install opportunistic-probe gate dropped (#1009).** `FirstInstallDelay = 0` in `internal/probe/opportunistic/install_marker.go`. Prior: `RULE-OPP-PROBE-07` refused every scheduler tick within 24 h of `/var/lib/ventd/.first-install-ts` mtime, with `LastReason = ReasonOpportunisticBootWindow`. Today's evidence on Phoenix's MSI Z690-A: 8 Layer B coupling shards persisted with `theta=[0,0]` for 5 days because static-PWM workload didn't satisfy `RULE-CMB-OAT-01`'s Δpwm excitation requirement; the 24 h gate further delayed Layer B convergence on every fresh install. The hard idle preconditions (idle gate's 600 s durability, no active SSH, no battery, no container, no scrub, no blocked process, ≥ 24 h post-resume warmup — `RULE-OPP-IDLE-01` through `-04`) are unchanged and remain the load-bearing protection against probing during real workload. `FirstInstallDelay`, `PastFirstInstallDelay`, and `ReasonOpportunisticBootWindow` are kept (not removed) so a future operator-tunable knob has a slot to hang on. Bound to rewritten `RULE-OPP-PROBE-07` with 5 subtests covering: constant is zero, zero-age marker passes, aged marker passes, empty path passes, scheduler does NOT refuse with `ReasonOpportunisticBootWindow`. Existing hosts on v0.5.29 ≤ N see no behaviour change on upgrade (their marker file's mtime is already ≥ 24 h old). A genuinely fresh install on v0.5.30+ starts opportunistic probing within minutes of install rather than 24 h later.

### Internals

- **State schema slot v2 reserved with no-op v1→v2 migrator (#1009).** `internal/state/version.go` bumps `currentVersion` from 1 to 2 and registers `migrations[[2]int{1,2}] = noopV1ToV2`. The v2 schema is identical to v1 on disk; the slot exists for the v0.6.0 broker-namespace migration (and any other v0.6 breaking shape change) without triggering `RULE-STATE-05`'s "treat as missing" path. A registered no-op is structurally distinct from a missing migrator — missing causes the upgrade loop to break out and the caller's state is effectively wiped on next access. Registered no-op keeps existing calibration / polarity / smart-mode shards intact across the version bump while exercising the migration mechanism end-to-end. Bound to new `RULE-STATE-MIGRATION-V1-V2-NOOP.md` with 4 subtests pinning: migrator registered, end-to-end run + sentinel bump, no-op does not mutate sibling files, `currentVersion >= 2`.

### Senior review pass

This is the first delivery in the v0.6.0 ship plan documented at `/root/.claude/plans/you-are-a-30-vivid-pascal.md`. Phase A continues with v0.5.31 (CSRF + body-size + SameSite=Strict), v0.5.32 (fakehwmon four canonical chip quirks + goleak), v0.5.33 (web/server.go + cmd/ventd/main.go file splits), then Phase B (calibrate/calibration/probe rename → internal/validity/), then Phase C (smart-mode HIL field-validation across the 5-host fleet) → tag v0.6.0.

## [v0.5.29] - 2026-05-08

### Headline

The in-UI updater actually works. v0.5.x had a latent two-stage bug — the staged install.sh was unreachable from the spawned transient unit, AND when that unit failed the API still replied 202 "scheduled" — so operators clicking "Update" in the dashboard saw a green ack and watched nothing happen. Both halves are closed in this release; the upgrade path from any prior v0.5.x to v0.5.29+ is now end-to-end functional and an upgrade failure surfaces an actionable message in the next /api/v1/update/check response.

### Fixed (operator-visible)

- **`writeInstallShBytes` stages install.sh under `/run/ventd`, not `/tmp`** (#1006). ventd.service ships `PrivateTmp=yes`, so the daemon's view of `/tmp` is a per-unit kernel namespace. The transient `ventd-update.service` spawned via `systemd-run` runs in the host namespace; a script staged in the daemon's PrivateTmp `/tmp` is not at that path on the host, and bash returns `exit 127` / ENOENT. systemd journal records `ventd-update.service: Main process exited, code=exited, status=127/n/a` but the API caller saw a successful 202 because `realUpdateRun`'s `cmd.Run()` observed a successful systemd-run *queue*, not the unit's runtime exit. Diagnosed end-to-end on the MSI Z690-A desktop on 2026-05-08; latent since the systemd-run pattern landed. Fix stages under `/run/ventd` (host-shared, not namespaced under PrivateTmp, already in the unit's `ReadWritePaths`, ephemeral so no orphan litter). Falls back to `os.CreateTemp("", ...)` when `/run/ventd` is unavailable (dev-tree invocation, non-systemd hosts) so existing dev workflows keep working. Bound to new `RULE-WEB-UPDATE-STAGE-PATH-OUTSIDE-PRIVATETMP` with four subtests.

- **Failed transient unit surfaces via `/api/v1/update/check.last_apply_error`** (#1007). Even with the staging path fixed, `POST /api/v1/update/apply` previously replied 202 regardless of whether the spawned unit would actually succeed at startup. New bounded watcher goroutine (`watchUpdateApplyOutcome` in `internal/web/update_outcome.go`) polls the transient unit for up to 60 s at 1 s intervals; on `Result != "success"` and `SubState ∈ {exited, dead, failed}`, captures the result + last 30 journal lines into a package-level `atomic.Pointer[LastApplyOutcome]`. The next `GET /update/check` includes `last_apply_error: {at, version, status, detail, journal_tail}` so the operator sees the actionable failure cause without leaving the dashboard. Success is silent (the daemon's restart is the success surface; recording success would persist a stale "last failed" indicator after the next successful install). Timeout is silent (operator can re-poll). The `omitempty` tag preserves backward compatibility — older UIs that don't know about the field see no behaviour change. Bound to new `RULE-WEB-UPDATE-STATUS-FIDELITY` with six subtests.

### Internals

- New package-level seams in `internal/web/update.go` and `internal/web/update_outcome.go`: `installStagingDir`, `systemctlIsFailedFn`, `journalctlTailFn`, `updateOutcomeWatchTimeout`, `updateOutcomePollInterval`. All overridable from tests so the test suite is hermetic and doesn't sleep for real.
- `LastApplyOutcome` struct is JSON-shaped via standard tags (no manual marshal); `omitempty` drives the back-compat shim.
- Watcher only fires when `systemd-run` was the spawn primitive; the nohup fallback path is unaffected.

### Senior review pass

The two fixes were diagnosed live during a "test it, validate it works, not what doesn't" run on Phoenix's desktop (Ubuntu 24.04, kernel 6.8.0-111, MSI Z690-A, nct6687, ventd v0.5.26). Smart-mode validation on that same run confirmed Layer B coupling shards persist for all 8 fans (`/var/lib/ventd/smart/shard-B/_sys_class_hwmon_hwmon5_pwm{1..8}.cbor`), 718,815 observation records over 5 days, ControllerState=COLD_START throughout (Layer B in warmup pending sustained Δpwm excitation per RULE-CMB-OAT-01) — all consistent with documented behaviour. The smart-mode invariants are real; only the update-feature plumbing was broken.

## [v0.5.28] - 2026-05-08

### Headline

A senior-review pass on the v0.5.26 codebase surfaced five real safety/correctness gaps, all closed in this release. Headline change: the README's "every exit path restores firmware control within two seconds" promise is now upheld by ventd's own restore path, not just by systemd's `WatchdogSec=2s` SIGKILL + `OnFailure=ventd-recover` belt-and-braces. Plus the Go toolchain catches up to 1.25.10 to clear two stdlib CVEs that landed in the vuln database mid-cycle and started failing govulncheck on every PR.

### Fixed (safety)

- **Watchdog Restore is now budget-bounded and parallelised** (#1002). `Restore()` previously walked entries sequentially with no per-write deadline; a hung sysfs ioctl on one fan blocked every subsequent fan's restore, while the heartbeat goroutine kept pinging systemd happily — defeating the SIGKILL backstop. New `RestoreCtx(ctx)` launches one goroutine per registered entry via the swappable `restoreOneImpl` seam, selects on WaitGroup-drain vs `ctx.Done()`. On deadline-exceeded a `restoreGracePeriod` (100 ms) lets in-flight goroutines finish their log emit, then snapshots the abandoned set, sorts deterministically, emits one WARN naming the abandoned channels + cancellation cause, and returns. The legacy `Restore()` now wraps `RestoreCtx(context.WithTimeout(_, DefaultRestoreBudget))` (1.8 s — 200 ms headroom under typical systemd `TimeoutStopSec=2s`); existing call sites in `cmd/ventd/main.go`, `cmd/ventd/runsetup.go`, and `internal/controller/controller.go` pick up the budget without per-caller code changes. Per-entry panic recovery (`RULE-WD-RESTORE-PANIC`) and every-entry-touched (`RULE-WD-RESTORE-EXIT`) contracts hold verbatim. Bound to new `RULE-WD-RESTORE-BUDGET`.

- **NVML init bounded at 2 s to prevent startup hang on partial driver installs** (#1000). A wedged `purego.Dlopen` on `libnvidia-ml.so.1` — caused by mismatched DKMS, stale `.so` symbols, or a hung kernel module — would block daemon startup indefinitely past systemd's `TimeoutStartSec`, leaving the operator with "ventd failed to start" and no actionable diagnostic. New `nvidia.InitWithDeadline(ctx, logger, timeout)` wraps `Init` in a goroutine with a select on done-channel, `time.After(timeout)`, and `ctx.Done()`. On timeout fire: returns wrapped `ErrLibraryUnavailable` with `"timed out"` in the message; emits one WARN. The orphan dlopen goroutine continues running because `purego.Dlopen` is uncancellable — once a timeout fires, NVML is permanently disabled for this process lifetime, by design. `cmd/ventd` and `cmd/ventd-nvml-helper` use the deadline-bounded variant with a 2 s budget; `nonvidia` build tag gets a matching stub. Bound to new `RULE-GPU-PR2D-09`.

### Fixed (correctness)

- **KV writes refuse before mutating in-memory state when the state directory is critically low on disk** (#1003). Prior behaviour: `KVDB.Set` mutated `db.data[ns][key] = value` first (`kv.go:100`) and called `persist()` second (`kv.go:101`). On a full `/var/lib/ventd`, `persist` returned ENOSPC to the caller but the in-memory map was already advanced. `Get` returned the new value while a daemon restart loaded the OLD on-disk value back — silent in-memory/on-disk divergence for `wizard.initial_outcome`, calibration state, polarity records, and the smart-mode shard root. New `iox.EnsureFreeSpace(path, minBytes)` is a `statfs(2)`-based pre-flight gate; `iox.MinFreeBytesForState` (1 MiB) is the canonical state-class threshold. Plumbed into `KVDB.Set` / `KVDB.Delete` / `KVDB.WithTransaction` via the `ensureFreeSpaceFn` package-level seam (production points at `iox.EnsureFreeSpace`; tests inject a refusing stub). The gate fires before the mutex acquire and before any in-memory mutation, so refusal never advances the in-memory state ahead of the on-disk file. `WithTransaction`'s gate fires before the caller's `fn` closure even runs. Bound to new `RULE-IOX-02` and `RULE-STATE-12`.

### Fixed (supply chain)

- **Go toolchain bumped 1.25.9 → 1.25.10 to clear two stdlib CVEs** (#1001). govulncheck on `pre-release-check.yml` started failing on every PR after the vuln database picked up two Go 1.25.9 fixes:
  - `GO-2026-4971` — Panic in `net.Dial` / `LookupPort` on NUL byte (Windows). Reachable via `sdnotify.Notify`, `hwdb.refreshFromURL`, `web.Server.ListenAndServe`, `redactor.NewP1HostnameFrom` (`LookupAddr`/`LookupHost`).
  - `GO-2026-4918` — Infinite loop in HTTP/2 transport on bad `SETTINGS_MAX_FRAME_SIZE`. Reachable via `hwdb.refreshFromURL` → `http.Client.Do`.

  Both fixed in `net@go1.25.10` and `net/http@go1.25.10`. Updated all twelve `go-version:` pins across `.github/workflows/*.yml` to exact `1.25.10` (the floating `1.25.x` form was resolving to 1.25.9 on runners with cached `setup-go` manifests, conflicting with go.mod's `1.25.10` minimum under `GOTOOLCHAIN=local`).

### CI / hygiene

- **`.golangci.yml` pins golangci-lint v2.1.6's standard linter set explicitly** (#1004). CI's lint job (`ci.yml:347`) installs golangci-lint v2.1.6 from source and runs `golangci-lint run --timeout=5m` against the implicit default linter set: `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`. With no `.golangci.yml` checked in, a future v2.x release that adds or removes a default-on linter would silently widen or narrow CI's coverage without a code-review trail. Pin the linter set explicitly via `linters.default: none` + `enable:` list. Same effective behaviour against current main (`0 issues`). Test files get an errcheck carve-out — `defer file.Close()` in tests is the canonical Go pattern; flagging it is noise. `scripts/ci-local.sh` already runs `golangci-lint` when installed; the new config is picked up automatically — local↔CI parity is now documented rather than implicit.

### Internals

- New `internal/iox/freespace.go` with `EnsureFreeSpace` + `MinFreeBytesForState` + `ErrInsufficientFreeSpace` sentinel.
- New `internal/watchdog.DefaultRestoreBudget` exported constant for callers with non-default systemd `TimeoutStopSec`.
- New `restoreOneImpl` and `ensureFreeSpaceFn` package-level seams in `internal/watchdog` and `internal/state` for unit-test injection.
- `nvidia.InitWithDeadline` is the operator-facing API; `initWithDeadline` is the testable inner core that accepts an arbitrary loader function so tests cover every branch (timeout, fast path, ctx cancel, zero-timeout disable, nil logger) without touching the package-level `loadOnce` state.
- Three new bound rules: `RULE-WD-RESTORE-BUDGET` (watchdog), `RULE-GPU-PR2D-09` (NVML deadline), `RULE-IOX-02` + `RULE-STATE-12` (free-space gate).

### Senior review pass

A 12-item senior-review plan was assembled and verified against the actual code; 5 items shipped (above), 7 items withdrawn after careful spot-checks revealed they were either already-correct code, already-shipped fixes (C4 errCh buffering landed in v0.5.27), or stylistic refinements rather than safety. Final false-positive rate on the original review: ~50% — useful calibration data for the next senior-review pass on v0.6.0. The 5 items that were genuinely real got shipped here.

### Deferred

- **B9 from v0.5.27** — NVML controllable `channel_id` is bare `"0"` on the wire (#998). Still cosmetic; deferred again.
- Same long-running CI lane gaps still tracked: #812 (scheduler race on ubuntu-arm64), #815 (SLSA provenance flake on release.yml), #901 (opensuse-tumbleweed CI lane), #978 (config hot-reload race audit).

## [v0.5.27] - 2026-05-08

### Headline

A live read-only probe of Phoenix's Desktop on v0.5.26 surfaced a thirteen-item bug floor (B1–B12 + a deferred B9). v0.5.27 ships the user-visible nine of those plus three pre-existing issues: the `/doctor` page stops crashing, the patch-notes modal stops being silently empty on tarball installs, and the in-UI updater stops being able to wedge the daemon. Every fix carries its own regression test; every UI change is honest about what it's surfacing (see #931's no-theatre rule).

### Fixed (operator-visible)

- **Hardware page surfaces every fan header on Phoenix's MSI Z690-A NCT6687** instead of dropping seven of eight (#988). `internal/monitor/monitor.go::scanInputs` was silently filtering `fan*_input == 0` readings on the theory that "0 RPM means dead fan, hide it" — but on Phoenix's rig only the CPU Fan was physically connected; the other seven were valid kernel-exposed headers reading 0. Operators couldn't tell from the inventory which headers existed. The new behaviour surfaces every header; the UI badges any fan reading 0 RPM as "no fan connected".
- **GPU sensors on the Hardware page no longer all label themselves "gpu_temp"** (#990). `internal/web/hardware_inventory.go` keyed the alias map by `SensorPath` alone — for NVML, that's the bare `gpuIdx` (`"0"`) for every metric. With Phoenix's typical config (`{name: gpu_temp, type: nvidia, path: "0", metric: temp}`), every reading on gpu0 (fan_pct, power, util, clock_gpu, clock_mem, mem_util) inherited the temp sensor's alias. Now the alias map is keyed by `(path, metric)` for NVML; hwmon paths still use bare-path keys.
- **Smart-mode card stops rendering "Conf min: 0.00 / Conf max: 0.00" during pre-warmup** (#991). `/api/v1/smart/status` now emits JSON null for `confidence_min` / `confidence_max` whenever no channel has positive `Wpred` (the cold-start window per RULE-AGG-COLDSTART-01, monitor-only mode, or all channels refused). The UI's existing `val == null` branch in `sysRow` renders the dim "—" automatically.
- **Topbar smart-mode pill counts runtime control state, not config flags** (#992 / closes #979). `web/dashboard.js::pollSmartMode` was reading `/api/v1/config` and counting boolean toggles (`acoustic_optimisation`, `!signature_learning_disabled`, etc.) — saying "smart · 4 active" even on monitor-only systems writing zero PWMs. Now reads `/api/v1/smart/status`: `enabled=false → "smart · monitor-only"`, `channels=0 → "smart · idle"`, otherwise `"smart · {converged}/{channels} converged"` with the dot colour following `global_state`.
- **Opportunistic-probe pill shows elapsed/total seconds while in flight** (#993 / closes #980). Pre-fix the pill said only "probing PWM 100" — operators couldn't tell hung from progressing through the locked 30 s window. Now reads "probing PWM 100 · 12s of 30s" computed client-side from `started_at`. Tooltip clarifies what the probe does and that it auto-aborts on busy host.
- **Disconnected sensors get a "no sensor connected" badge instead of rendering 8.5°C as if real** (#997 / closes the last #923 minor item). NCT6687 reports 8.5°C on Phoenix's "PCIe x1" temp6_input — a header the chip exposes but no sensor is wired to. Real degraded readings (Framework 13 AMD 7040 EC's −17°C I2C underflow) still pass through, just flagged. Sub-absolute-zero stays a hard reject. Bound to a new `RULE-SENTINEL-TEMP-DISCONNECT` invariant.
- **`ventd version` (positional, not flag) now works for unprivileged operators** (#994). Pre-fix the subcommand fell through to daemon-startup which tried to load `/etc/ventd/config.yaml` (mode 0600 since the v0.5.8.1 root-flip), so `ventd version` as the `phoenix` user fataled with `permission denied`. Now short-circuits before any subsystem init — works regardless of config permissions, hwmon access, or NVML availability.

### Fixed (security / hygiene)

- **Setupbroker rejects leading-hyphen module names** (#995 / closes #973). Previous regex `^[A-Za-z0-9_-]{1,64}$` allowed any character — including hyphen — as the leading character. A request with `module: "-rfdkms"` would pass validation, then `modprobe -rfdkms` would interpret the entire string as flags. Tightened to `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` so the first character must be alphanumeric.
- **In-UI updater retries transient GitHub fetches** (#996 / closes #974). `fetchLatestRelease` was a single-shot HTTP call; a 503/502 or transient network blip surfaced immediately as a fatal error in the Update modal. Now wraps in a 3-attempt retry loop with exponential backoff (1s → 2s). Network errors and HTTP 5xx / 429 are retried; HTTP 4xx is terminal so we don't spin on permanent failures.
- **In-UI updater caps install.sh runtime at 10 minutes** (#996 / closes #975). `buildUpdateCmd`'s systemd-run path had no `RuntimeMaxSec`, so a wedged install.sh (DNS hang, dpkg lock) would leave the daemon offline indefinitely. Adds `--property=RuntimeMaxSec=600`. The nohup fallback path on OpenRC/runit hosts wraps install.sh in `timeout 600 bash …` for parity.

### Fixed (regression test gates)

- **Patch-notes modal stops being silently empty on tarball installs** (#989). `internal/web/CHANGELOG.md.embedded` had drifted from `CHANGELOG.md`: the v0.5.25 + v0.5.26 entries existed in the canonical but not the embed. Every install path with no on-disk `/usr/share/doc/ventd/CHANGELOG.md` (curl-pipe-bash, container, custom install) was silently rendering an empty modal. The pre-existing `TestChangelogEmbedded_SyncedWithRepoCopy` gate caught this; refreshed via `cp CHANGELOG.md internal/web/CHANGELOG.md.embedded`. Worth a follow-up to make the gate block the merge queue.

### Pre-existing fix

- **`/doctor` page no longer crashes on Severity-as-int** (#985). `internal/doctor/severity.go` Severity now marshals as a string (`"ok"|"warning"|"blocker"|"error"`) rather than an int — `web/doctor.js` was calling `.toLowerCase()` on what was actually `0|1|2|3`. Operators on the doctor page saw a runtime error in the browser console and a blank panel.

### Deferred

- **B9** — NVML controllable `channel_id` is bare `"0"` on the wire (`#998` filed). Cosmetic; the GPU still works under control. Deferred to v0.5.28.
- **#812** scheduler race flake on ubuntu-arm64.
- **#815** SLSA provenance flake on release.yml.
- **#901** opensuse-tumbleweed CI lane PATH corruption.
- **#978** config hot-reload race audit.

### Internals

- New `internal/hal/hwmon.LowTempAmbientFloorCelsius` constant (10 °C) + `IsLowTempLikelyDisconnected` helper, bound to `RULE-SENTINEL-TEMP-DISCONNECT`.
- `monitor.Reading` gains a `LikelyDisconnected bool` field (omitempty) propagated through `InventorySensor`.
- `smartStatusResponse.ConfidenceMin/Max` are now `*float64` (omitempty-on-nil → JSON null on the wire).
- `cmd/ventd/main.go` adds a positional `version` subcommand alongside `diag`, `doctor`, `preflight`, `calibrate`.
- `internal/web/update.go` factors retry behaviour into `fetchLatestReleaseOnce` + `fetchLatestRelease` with `fetchRetryAttempts = 3`, `fetchRetryBaseBackoff = 1s`, and an `isTransientFetchErr` classifier.

## [v0.5.26] - 2026-05-05

### Headline
- **In-UI Update button now actually upgrades the binary** (#982, closes #983) — the third (and final) fix in the in-UI updater chain. v0.5.18 (#950/#951) embedded the install.sh + CHANGELOG so `findInstallScript` always had something. v0.5.19+ (#955/#960) added the two-phase commit + preflight skip env so install.sh would *succeed in principle*. But every click on Phoenix's HIL still left the daemon stuck on the old version. Root cause this release closes: **`ventd.service` ships with `PrivateTmp=yes` + `ProtectSystem=strict` + a `ReadWritePaths=` list that excludes `/usr/local/bin` and `/var/log`**. The naive `nohup`-fork in `realUpdateRun` put `install.sh` inside the daemon's cgroup and inherited the entire sandbox. install.sh failed the moment it tried to write the staged binary at `/usr/local/bin/.ventd.new`, redirect to `/var/log/ventd-update.log`, or run `dpkg -i`. Diagnosis evidence on Phoenix's Desktop: daemon mtime `2026-05-04 11:29:23 UTC`, last update click `18:51:56 UTC` for v0.5.24, gap unexplained for hours — sandbox was silently blocking every privileged write.

### Fix
- Spawn install.sh via `systemd-run --no-block --collect --unit=ventd-update --service-type=oneshot --property=KillMode=process --setenv=VENTD_VERSION=… --setenv=VENTD_SKIP_PREFLIGHT_CHECKS=… bash <script>`. This creates a transient SERVICE unit **outside** ventd.service's cgroup with its own clean root sandbox view. `KillMode=process` keeps install.sh alive when it triggers `systemctl try-restart ventd`, which would otherwise SIGTERM the entire spawning cgroup. `--collect` frees the unit on completion so successive update attempts don't accumulate failed transient units.
- Non-systemd hosts (Alpine OpenRC, Void runit) fall back to the original `nohup` path — those hosts don't impose a service-unit sandbox to begin with.

### Tests
- `TestBuildUpdateCmd_PrefersSystemdRun` pins the load-bearing flag set when systemd is the running init.
- `TestBuildUpdateCmd_FallsBackToNohup` covers all three "systemd not usable" edges (init not running, binary missing, both).

### Why this wasn't caught before
The chicken-and-egg work in #950/#951/#955/#960 fixed install.sh's *contents* — assuming install.sh would run. The test harness invokes install.sh directly, never via the daemon's `exec.Cmd`. The sandbox restrictions were correct for the daemon's normal operation; they only break this one privileged-write path. Operators who ran `curl … | bash` directly never hit it because curl-pipe-bash spawns from the user's shell, not from inside ventd.service. The bug was specifically the in-UI button — invisible until you tried it on a host that already had the daemon installed.

## [v0.5.25] - 2026-05-05

### Headline
- **Race condition in observation.Writer fixed** (#977) — bug-hunt iteration 2 caught a real production race. The `Writer` struct's docstring said "NOT safe for concurrent use", but the actual wiring violated that contract: both the controller tick goroutine AND the opportunistic-probe scheduler goroutine call `Writer.Append` against the same instance. The unsynchronised `bytesWritten` / `activeDay` / `headerWritten` fields would race on the rotation-trigger check (50 MiB cap and midnight crossings) — two goroutines could both observe the rotate trigger, both call Rotate(), and the second's Header write would land in the FIRST one's brand-new file. Fix: add `sync.Mutex` to the Writer; split Rotate into public + lock-held inner so Append's auto-rotate path can call it without recursive-lock dance. New `TestWriter_ConcurrentAppendRaceSafe` runs 8 goroutines × 1000 Appends; `go test -race` clean.

### Filed for follow-up
3 lower-priority iteration 2 findings: #978 (Config struct fields not protected from concurrent read/write — latent), #979 (smart pill counts configs not control state), #980 (opp pill missing progress context).

### Bug-hunt summary
Iteration 2 surfaced 18 raw findings from 3 parallel Explore agents. Validation pass: ~10 hallucinated (controller goroutine pile-up that doesn't exist; DecisionCache.slot race that's properly locked; etc.), 1 fixed in this release (Writer race), 3 filed as issues for follow-up, the rest design-by-choice or already addressed in earlier PRs. The agent track record holds: ~50 % hallucination rate, but the 50 % that's real is worth chasing.

## [v0.5.24] - 2026-05-04

### Headline
- **Bug-hunt iteration 1: security + UI honesty batches** (#971 + #972) — Phoenix asked for an extensive bug hunt after the v0.6 work landed; this release ships the first batch of confirmed-real findings.

### Security (#971)
- **\`ventd-setup.service\` RuntimeDirectoryMode=0755 → 0750** — original declaration would have publicised the wizard's request file (operator-supplied module names, package selectors, audit metadata) to every user on the system if ventd-setup happened to win the create race against ventd.service. 0750 matches ventd.service and keeps the dir tight (root + ventd group).
- **JSON request decoder cap** — \`MaxRequestBytes = 64 KiB\` via \`io.LimitReader\` + post-decode \`dec.More()\` check. Without the cap, a malicious 10 GiB request file would let \`json.Decoder.Decode\` allocate the giant string field into the \`Request\` struct before any type validation short-circuits. ventd-setup OOMs before reaching the wizard's result-file reader.
- New install-contract tests (`TestVentdSetupUnit_*`) pin the unit's RuntimeDirectoryMode + Type=oneshot + User=root.

### UI honesty (#972)
- **\`/settings\` no longer renders demo placeholder values on \`/api/v1/config\` GET failure** — operators on a host where the daemon was unreachable used to see a fully-rendered settings page with \"0.0.0.0:9999 (demo)\" / \"Quiet\" / 5 curves / 14 fans, which masked the broken state. Now every readout shows \"—\".
- **\`/health\` topbar pill turns warn on partial endpoint failure** — silent per-endpoint catch-to-null had made \"Hottest: —\" indistinguishable from \"system has zero sensors\". Pill now reads \`partial · N endpoints failed\` with the per-endpoint cause in the meta footer.
- **\`/doctor\` \"Info\" severity gets its own visual section** — informational findings (active experimental flags, etc.) used to roll into the OK group, making them visually invisible. New blue-tagged \"Info\" section sits between OK and Detector errors with its own count badge.

### Filed for follow-up
3 lower-priority findings filed as issues for tracking: #973 (tighten module name regex to reject leading hyphen), #974 (in-UI updater retry on transient GitHub 5xx), #975 (in-UI updater timeout on detached install.sh hang).

### Honest framing
The bug hunt's no-theatre-rule extension to error states is the load-bearing change in this release: when an endpoint isn't reachable or a fact is informational, the UI MUST say so — not paper over the gap with placeholder values that look authoritative.

## [v0.5.23] - 2026-05-04

### Headline
- **Monitor-only systems default to /health on root navigation** (#967, closes #784 Branch B) — operators on EC-locked laptops, mini-PCs, BMC-managed servers no longer land on `/dashboard` (which is mostly empty for them) when they hit the root URL. Detection: `setup.IsApplied() && len(cfg.Controls) == 0`. Pre-wizard hosts still land on `/calibration`; control-mode hosts still land on `/dashboard`. Direct URL navigation to `/dashboard` remains available — the redirect is just default landing.
- **Split-daemon Phase B: first two real handlers in setupbroker** (#968 load_module, #969 unload_module) — the broker scaffold from #962 has its first concrete operations. Both are tightly-scoped privileged surfaces with strict module-name validation (`^[A-Za-z0-9_-]{1,64}$`), shell-metacharacter rejection on every modprobe arg, optional persistence to `/etc/modules-load.d/ventd-<module>.conf`, partial-success reporting (modprobe OK but persistence failed), and idempotence on already-absent persistence files. 15 unit tests cover happy paths, validation rejection cases, modprobe failure surfacing, and decoder strictness.

### v0.6 plan status
4 of 5 prereqs fully done; 5th (catalog harvest) has spec. Split-daemon architecture done at the broker layer; per-operation handlers continue iteratively across follow-up PRs (3 of 7 done, 4 to go: install_dependency, patch_kernel_param, nvml_write, run_sensors_detect, install_oot_driver — minus the unimplemented count of 4 since 3 are done after this release: load_module, unload_module, and the dispatch fallthrough behaviour).

The "User=ventd flip + AppArmor restore" Phase B finalisation is gated on HIL re-test across the full grid (Phoenix's three boxes + the 2-laptop and Steam Deck additions); release-engineering rather than architectural design.

### Honest framing
v0.5.23 doesn't change runtime behaviour for any operator on a control-mode system — every fan / sensor / pill / spark renders the same. The visible change is for monitor-only operators (the redirect) and for ventd-setup smoke testers (the broker now actually does load/unload module operations on real /sbin/modprobe). The wizard does not yet ROUTE through ventd-setup; that wiring lands in the next Phase B sub-PR.

## [v0.5.22] - 2026-05-04

### Headline
- **Consolidated /health view for monitor-only systems** (#964, **closes #793; v0.6 prereq #2 completion**) — single page that aggregates every sensor / fan / voltage / power reading the daemon enumerated, surfaces a doctor-finding count badge, and gives operators on monitor-only fallback systems (BIOS-locked PWMs, server BMCs, EC-locked laptops) a useful default view instead of the dashboard's mostly-empty control affordances. 4 summary cards (hottest sensor, spinning fans count, doctor findings count, total sensor count) + 4 grouped sections (Temperatures / Fans / Voltages / Power) with per-sensor cards (friendly name + current value + 60-sample trend spark + chip name + trend arrow). 2 s poll cadence; pulls from existing `/api/v1/hardware/inventory` + `/api/v1/doctor` + `/api/v1/version` so no new backend endpoints land. Sidebar gains a Health entry under Diagnostics.

### Honest framing
v0.5.22 is presentation-only: every value on /health is a real sensor reading with a chip-name backing it, zero fabricated metrics. The MVP scope explicitly defers (per Phoenix's "ship the consolidation, not new sub-systems" framing on the issue): SMART-parsing endpoint, anomaly detection, wizard auto-redirect for monitor-only outcomes (#784 follow-up), PSI / loadavg surfacing.

This closes 4 of Phoenix's 5 v0.6 prerequisites:
- #1 Zero-terminal install UX — covered by the in-UI updater fixes (v0.5.18, v0.5.20)
- #2 Full health monitoring even when fans can't be controlled — closed by this release (#793 + the doctor surface from #957)
- #3 AI-quality control — closed by Layer-C forecast + acoustic toggle (v0.5.19, #945 + #956 + #958)
- #4 Friendly auto-detected hardware names — still open (#791 catalog harvest, needs spec)
- #5 Beat competitors — implicit in #1-#4

Remaining v0.6 work: split-daemon Phase B (flip ventd to User=ventd + restore sandbox + actually route privileged steps through ventd-setup) + the catalog harvest spec + harvest implementation.

## [v0.5.21] - 2026-05-04

### Headline
- **Split-daemon Phase A scaffolding** (#962, **closes #787 PR-A; v0.6 prereq architectural**) — first concrete step toward `spec-v0_6_0-split-daemon`'s "long-running unprivileged control loop + oneshot root setup service" architecture. Ships the new `cmd/ventd-setup` binary + `internal/setupbroker` package + `deploy/ventd-setup.service` systemd unit ALONGSIDE the existing root `ventd.service` — wizard behaviour is unchanged (every operation falls through to the broker's `ErrOperationNotImpl` stub). Operators on v0.5.21 can smoke-test the binary in isolation via `sudo systemctl start ventd-setup` after staging a request file under `/run/ventd/`. Phase B (a future PR) flips `ventd.service` to `User=ventd` + restores the AppArmor sandbox + actually routes the wizard's privileged steps through this binary.
- **Wire format pinned** at `SchemaVersion=1` so Phase B's per-operation handlers can land incrementally without breaking the envelope. 7 operation constants reserved: `install_oot_driver`, `install_dependency`, `load_module`, `unload_module`, `patch_kernel_param`, `nvml_write`, `run_sensors_detect`. Each new operation adds (a) a constant, (b) a `Params*` struct, (c) a `dispatcher.Register` call.
- **First validation of the in-UI updater chicken-and-egg fix** (v0.5.20 #960): operators on a v0.5.20 daemon can update to v0.5.21 entirely through the Settings → Update button on hosts that previously hit the DKMS / build-tools preflight blocker. Phoenix's three HILs (Proxmox, Desktop, MiniPC) all on v0.5.20 at tag time; this is the first release that actually exercises the network-fetch + skip-checks paths end-to-end.

### CI / chore
- 15 new tests for the broker package + setup binary (envelope schema, dispatch fallthrough, request file handling, result file mode 0600).
- `ventd-setup` binary added to the default + musl tarball archives + .deb / .rpm `nfpms.contents`.

### Honest framing
v0.5.21 doesn't change runtime behaviour for any operator — Phase A is "ship but don't use". Its strategic value is twofold: it lays the architectural foundation for v0.6's security story (control loop dropping privileges), and it gives the v0.5.20 in-UI updater fix its first chance to prove itself end-to-end on Phoenix's HIL grid.

## [v0.5.20] - 2026-05-04

### Headline
- **In-UI updater chicken-and-egg loop closed for good** (#960) — two stacked root causes were blocking Phoenix's HIL fleet (Proxmox + MiniPC + Desktop) from self-updating via the dashboard's Update button. Both fixed in one PR because either alone leaves the loop open.
  - **Bug 1 (embedded install.sh frozen at build time)**: every daemon binary embeds the install.sh that was current AT BUILD TIME. Any later install.sh fix (e.g. v0.5.19's two-phase commit) only reaches operators whose binary was built AFTER the fix — older daemons forever spawn the buggy embedded copy. Fix: the daemon's apply handler now fetches `install.sh` from the target release tag's GitHub assets (`scripts/install.sh` is now a top-level release asset via `release.extra_files`), falls back to on-disk + embedded only when the network fetch fails. Every in-UI update from v0.5.20+ uses `install.sh@<target_version>`, picking up every fix that landed in [running, target]. Hardening: 1 MiB cap on the fetched body, sub-256-byte rejection (catches HTML 404 pages), `#!` shebang prefix check.
  - **Bug 2 (preflight blocks on dkms_missing for in-tree-driver hosts)**: hosts running only in-tree hwmon drivers (Phoenix's Proxmox + MiniPC) don't have DKMS / GCC / kernel headers / make / Secure Boot signing tools — and shouldn't have to grow them just to update the daemon binary. Preflight unconditionally required all of these, leaving operators permanently stuck. Fix: the daemon's apply handler now sets `VENTD_SKIP_PREFLIGHT_CHECKS=dkms_missing,gcc_missing,kernel_headers_missing,make_missing,signfile_missing,mokutil_missing,mok_keypair_missing,mok_not_enrolled` when spawning install.sh. install.sh threads the env through to `ventd preflight --skip <names>`. The orchestrator (RULE-PREFLIGHT-ORCH-06) excludes the named checks from both the run AND the BlockerCount tally. Wizard-driven first-install paths still run the full preflight chain — only the in-UI binary-update path narrows it.

### Operator-facing impact
v0.5.20 is the **last manual install needed for any operator** on a pre-v0.5.20 build. Once on this binary, every future tag rolls forward through the in-UI Update button cleanly:
- Network fetch always uses the latest install.sh from the target tag — future install.sh fixes are a release-asset away, no binary update required.
- Preflight skips the build-tools chain when the operator just wants a binary swap.
- The two-phase commit (v0.5.19) ensures any preflight failure leaves zero on-disk state behind.

The chicken-and-egg loop that made every install.sh fix require an external manual install is closed.

## [v0.5.19] - 2026-05-04

### Headline
- **Doctor surface, live in the web UI** (#957, spec-10, **closes v0.6 prereq #2**) — the runtime detector pack that's been shipping in `internal/doctor/detectors/` since v0.5.10 (12+ detectors, all spec-bound under `docs/rules/doctor.md`) finally gets a web view. New `/doctor` page + `/api/v1/doctor` endpoint render Severity-grouped Fact cards (Blocker / Warning / Error / OK) plus a separate panel for detector-level errors. MVP detector set covers `container_postboot`, `dkms_status`, `battery_transition`, `gpu_readiness`, `permissions`, `experimental_flags`. Per-Server cache (5 s TTL) so a multi-tab dashboard doesn't fan out into N detector re-runs per poll. Topbar rollup pill colours by worst severity. Sidebar gains a new "Diagnostics" section with the Doctor entry. Baseline-requiring detectors (kernel_update, hwmon_swap, apparmor_profile_drift, dmi_fingerprint, calibration_freshness, modules_load, kmod_loaded, ec_locked_laptop, polarity_flip) land in a follow-up.
- **Smart-mode quietness preset + dBA override on /settings** (#956, **closes v0.6 prereq #3 surface**) — operators can finally pick Silent / Balanced / Performance from the UI instead of hand-editing `config.yaml`. The cost-gate primitives (`PresetDBATargets`, `EvalDBABudget`) have been live since v0.5.12; this PR closes the operator-facing gap. Also exposes a numeric dBA budget override (range 10–80 dBA, server-side validated) so an operator can pick Balanced controller behaviour while capping noise at a custom budget. Round-trips through the existing `/api/v1/config` PUT path; no new endpoint needed.
- **Predicted next-tick ΔT on the dashboard hero forecast** (#958, **closes v0.6 prereq #3 wiring**) — the dashboard hero forecast (shipped in #945) showed Layer-C's per-+1-PWM marginal rate, a useful but abstract signal. v0.5.19 wires the controller's actual decision pipeline through to the dashboard: every tick, the BlendedResult (output PWM + reactive PWM + refusal flags) lands in a per-channel `DecisionCache`, the `/api/v1/smart/channels` API exposes it, and the dashboard sub-line now reads `↓ 0.45 °C predicted · ΔPWM +12 · last 60 s` — what the controller is actually about to do, what the model thinks will happen. Falls back to the per-rate display when no decision exists yet (fresh daemon / monitor-only / channel just admitted).

### Fixed
- **Patch-notes modal was see-through** (#954) — the `.rn-card` background referenced a `var(--surface-1)` token that was never defined in `tokens.css`. Without a fallback, the `background:` shorthand resolved to `transparent` and the entire modal showed only the 65 % black backdrop with the page bleeding through underneath. Defined `--surface-1` and `--surface-2` as semantic aliases for `--bg2` / `--bg3`, added explicit hex fallbacks on every `var(--surface-*)` reference, bumped backdrop opacity 0.65 → 0.78 + added a 4 px backdrop blur, lifted body text from `--fg2` (faded secondary) to `--fg` (primary), and bumped the body font-size for comfortable readability.
- **install.sh restart-then-copy ordering left a stale daemon** (#955, closes #953) — Phoenix HIL repro: three boxes manually updated to v0.5.18, two of them ended up with the new binary on disk but the running daemon still on v0.5.15 (`/proc/PID/exe → /usr/local/bin/ventd (deleted)`). Root cause: install.sh did `install -m 755 …` BEFORE the preflight check, then preflight bailed out on the DKMS/build-tools blocker, so the post-bailout `systemctl restart ventd` never fired. Fix: two-phase commit — stage the new binary at `$PREFIX/.ventd.new`, run preflight against the staged path (no on-disk swap yet), atomic-rename only on preflight pass. EXIT trap wipes the staged copy on any bailout path, so a failed install leaves zero on-disk state behind. Also adds a post-restart `/api/v1/version` check that warns + names the remediation command if the running daemon doesn't match the on-disk binary.

### CI / chore
- All 19 canonical CI lanes green on the doctor-surface merge commit (#957) including `golangci-lint` + the full distro matrix.
- 5 closed bug-hunt issues (#920, #921, #922, #924, #931) and a comprehensive update on the v0.5.14 tracking issue (#923).

### Honest framing
v0.5.19 is the first release where every dashboard signal traces to a real backend signal AND the operator can act on every smart-mode tunable from the web UI. Three of Phoenix's five v0.6 prerequisites (acoustic-aware control, doctor surface, workload-prediction wired to forecast) advanced materially in this release. Remaining v0.6 work: the split-daemon refactor (`spec-v0_6_0-split-daemon`, #787), the consolidated health-monitor view for monitor-only systems (#793), and the agent-driven hardware catalog harvest (#791).

## [v0.5.18] - 2026-05-04

### Headline
- **Self-bootstrapping in-UI updater + patch-notes modal** (#950, #951) — closes the chicken-and-egg dead-end Phoenix hit on a v0.5.15 daemon: the in-UI Update button's apply handler returned **HTTP 503** with `install.sh not found` because the .deb / .rpm packagers' `nfpms.contents` shipping `install.sh` + `CHANGELOG.md` to `/usr/share/` landed in #942 — *after* v0.5.15 was tagged. Worse, the curl-pipe-bash `install.sh` path (the documented operator entry point) only extracts the binary from the .tar.gz; it doesn't unpack auxiliary files into `/usr/share/`. Fix: bake both `scripts/install.sh` and `CHANGELOG.md` into the daemon binary via `go:embed`, with on-disk candidates checked first (preserves dev workflow + .deb / .rpm package paths) and a temp-file write fallback for `install.sh` (mode 0755). Every binary now carries its own bootstrap; no dependency on the package shipping anything beyond `ventd` itself.

### CI / chore
- New `make sync-install-sh`, `make sync-changelog`, `make sync-embeds` targets refresh the embedded copies after editing the canonical sources.
- `TestInstallShEmbedded_SyncedWithScriptsCopy` + `TestChangelogEmbedded_SyncedWithRepoCopy` fail CI if either embed drifts.
- `TestFindInstallScript_EmbedFallback` + `TestLoadChangelog_EmbedFallback` verify the fallback paths work end-to-end.

### Honest framing
v0.5.18 is the last release that requires a manual install for operators on pre-v0.5.18 builds — once they're on this binary, every future tag rolls forward through the in-UI Update button regardless of how the daemon was originally installed. The embed bootstrap means a fresh install via curl-pipe-bash, a docker-style binary copy, or a partial package install all behave identically: the daemon always knows how to update itself.

## [v0.5.17] - 2026-05-04

### Headline
- **Real Layer-C predicted ΔT forecast on the dashboard hero cards** (#945, closes #43, P0) — the v0.5.15 dashboard removed the fake 12-sample client-side linear-regression forecast and left a forecast-shaped hole. v0.5.17 fills it with the real model output: the daemon's existing `marginal.Snapshot.MarginalSlope` (`β_0 + β_1·load`, the Path-A formula from RULE-CMB-SAT-01) is plumbed through `/api/v1/smart/channels` and the dashboard renders it as an arrow-and-magnitude beneath each hero spark (`↓ 0.042 °C/PWM · last 60 s`). Below the saturation floor (2°C across the full 0-255 ramp) the badge reads `· saturated · last 60 s`; with no usable shard yet (warming up, no samples) it reads only `last 60 s` — never a fabricated number, no theatre.
- **Patch-notes-on-first-login modal** (#942, closes #48) — after the in-UI Update button (#934) rolls the daemon to a new tag, the operator's first post-update page load surfaces a dismissible modal containing the CHANGELOG section(s) for everything between their last-seen version and the current daemon version. Backend parses `/usr/share/doc/ventd/CHANGELOG.md`, splits on `

## [vX.Y.Z]` headings, returns sections newer than the `since` query param. Cached after first parse; invalidated only by daemon restart, which matches the install + restart cycle exactly. Frontend renders safe markdown-to-DOM (textContent only — no innerHTML, RULE-UI-01).
- **First-visit walkthrough banners on Dashboard, Hardware, and Smart** (#947, closes #36) — small dismissible card injected at the top of each of the three pages a first-time operator is most likely to land on. 2-3 plain-text paragraphs explain what they're looking at, why the signals are shaped the way they are, and where to look next. Persists `ventd-walkthrough-<page>` in localStorage so subsequent loads skip. Content stays honest — it describes what's actually on the screen and traces each signal back to its real backend source.

### Fixed
- **`is-just-changed` decision flash freshness gate** (#946, closes #35) — the yellow halo on a fan tile is supposed to fire in lock-step with a new entry being unshifted onto the decision feed. The flash flag was a bare bool, so any stale flag that survived a render cycle without being consumed could re-trigger the keyframe later when the tile next re-rendered. The corresponding decision entry by then may have been pushed below the visible 8-row window — operator sees a phantom flash with nothing to explain it. Stamp the flash with an epoch-ms timestamp and only consume it when ≤ 3 s old; stale flashes drop silently.
- **/calibration page CSP regression** (#944, closes #42) — the system-card body had `style="padding-top: 6px;"` inline, which violates the daemon's strict CSP (`style-src 'self'`, no `unsafe-inline`). The browser silently dropped the override and emitted a console warning since the CSP shipped. Move the override to a `.tight-top` modifier class on `.v2-card-body`.
- **Pre-existing bug in `aliveModeWorkloadLabel`** (in #945) — was walking `aliveState.channels.channels` (object-style) when `/api/v1/smart/channels` actually returns a bare JSON array. The workload-mode label has been silently empty since #39 landed; now correctly shows the modal signature label across channels.

### CI / chore
- **gofmt drift cleanup** (#943) — `golangci-lint`'s gofmt step caught two drifts that local pre-push missed (`golangci-lint` isn't in the local sweep): per-key whitespace alignment in `monitor.ecMirrorChips` (added in #939) and doc-comment bullet chars + struct-tag column alignment in `release_notes.go` (added in #942). Pure cosmetic; no behaviour change.
- **Dead-code removal** (#948) — drop unused `changelogCachePath` var and `resetChangelogCacheForTest` func from `release_notes.go`; both were introduced in #942 but never read by any caller.

### Honest framing
v0.5.17 closes the largest remaining P0 from the v0.5.14 dashboard bug-hunt (the missing real forecast — Phoenix's frustrated "WHY AREN'T WE USING THE MODEL we spent hours researching" feedback) plus three operator-facing UX completions: the first-visit walkthrough that explains each new page, the patch-notes modal that tells operators what just changed after they click Update, and the freshness gate that stops phantom flashes on the dashboard. With v0.5.17 every visible signal on the dashboard, hardware, and smart pages traces to a real backend signal — the no-theatre rule is now uniformly enforced.

## [v0.5.16] - 2026-05-04

### Headline
- **NCT6687 desktop super-I/O dedup fix** (#939, closes #40) — the v0.5.14+ `dedupMirrorFans` pass collapsed real distinct fans on Phoenix's MSI Z690-A NCT6687 chip whose idle RPMs coincidentally fell within ±10 RPM of each other. Outcome: 1 fan visible despite the board having 7 PWM headers. Tag the chips that genuinely mirror tachs (thinkpad_acpi family + applesmc + macsmc-hwmon + surface_fan + dell-smm-hwmon + asus-ec-sensors / asus-wmi-sensors + hp-wmi-sensors) and only apply dedup to those. Desktop super-I/O (nct6687, nct6798, it8688, etc.) get all their fan_input readings preserved.
- **Hero spark Y-axis: slow-EMA auto-fit** (#940) — the v0.5.15 fixed-pin to 20-100 °C made small idle deltas (2-3 °C variance, normal at idle) render ~1 px tall on the 48 px card — visually flat. Switch to a slow-EMA-smoothed auto-fit (alpha = 0.05; ~30 s lag at 1.5 s tick) so the axis range moves continuously but slowly. Single-poll outliers don't visibly rescale; sustained workload changes shift the bounds within ~30 s. Padded ±2 °C from the smoothed bounds; range floored at 4 °C to avoid divide-by-zero on a perfectly stable temp. The line now shows real variance at useful resolution while preserving the per-poll layout stability that was the original rationale for pinning.

### Honest framing
v0.5.16 is a tight follow-up to v0.5.15's no-theatre sweep — two HIL-driven fixes landed in the post-tag verification on Phoenix's MSI Z690-A desktop. The in-UI Update button (v0.5.15 #934) makes this kind of rapid follow-up zero-friction: tag → click Apply on the settings page → daemon rolls forward without restart resetting calibration progress.

## [v0.5.15] - 2026-05-04

### Headline
- **No-theatre web UI sweep + in-UI update button.** A single-day audit + bug-squash session driven by Phoenix's UX feedback that the v0.5.14 dashboard was "constantly flicking" and "doesnt seem like its actually real". Outcome: every cosmetic animation that didn't trace to a real backend signal removed, the dashboard hero spark stabilised + smoothed, the renderer fight that produced the flat→jagged→flat alternation killed at its source, and a settings-page Update affordance so operators can roll forward without losing calibration progress.
- **In-UI update button** (#934) — new \`/api/v1/update/check\` polls GitHub releases-latest; new \`/api/v1/update/apply\` spawns \`scripts/install.sh\` detached with \`VENTD_VERSION\` set, daemon dies during the install's \`systemctl restart\` and comes back under the new binary. \`/var/lib/ventd\` state (calibration JSON, smart shards under \`smart/shard-{B,C}/\`, \`.signature_salt\`, \`state.yaml\`) persists across the restart; RULE-ENVELOPE-09 confirms in-flight calibration sweeps resume from the last completed step. Settings page Update section shows installed + latest, Check button + Apply button, polls \`/healthz\` for re-up and reloads the page.

### No-theatre cleanup
The v0.5.14 dashboard / smart / hardware pages shipped a number of cosmetic animations that animated continuously regardless of any real backend event. Phoenix's rule (saved as auto-memory feedback): *every visible signal must trace to real backend research, not cheap client-side fakes*. All of the below removed in this release:
- **Smart Bridge "continuous loop" rotating spotlight** (#932) — was rotating every 600 ms through 6 sub-steps without being tied to any actual sub-step tick events.
- **Smart Scope tach-wobble** (#932) — was a synthetic Lissajous wobble keyed off \`opp.tick_count\`, not real RPM data. Replaced with the real PWM hold line + a caption that the daemon doesn't surface probe-time tach samples yet.
- **Hardware Topology view animateMotion packets** (#932) — daemon→chip and chip→sensor edges had decorative packet animations with no real bus events behind them.
- **Hardware Topology daemon-glow pulse** (#932) — fixed-cycle pulse unrelated to real daemon activity.
- **Hardware Inventory rail CouplingMini packet pulses** (#932).
- **Dashboard coupling-map active-edge pulses** (#932) — the active-class colour change already communicates "fan is running"; the moving packet implied a per-event data-flow signal we don't actually have.

### Dashboard hero card stabilisation
The v0.5.14 dashboard hero CPU/GPU cards were the most visually broken element — Phoenix described them as flicking constantly and showing fabricated forecasts (e.g. \`+138.6 °C / 30 s\`). Sequence of fixes:
- **Decision detector** (#925, #926) — the v0.5.14 dashboard inferred "decisions" from any 2 pp duty change between consecutive \`/api/v1/status\` polls, but the controller's natural curve micro-recompute jitter swung PWM ~2-3 pp every poll. 30-s sample on Phoenix's MSI Z690-A: daemon journal had ZERO controller log lines while the dashboard would have emitted 30 fake decisions. Replaced with a windowed-delta detector (3-poll window, 10 pp threshold, 6 s per-fan rate limit). Real ramps now fire one event each; sensor-noise micro-recomputes are silenced.
- **Hero forecast badge** (#928, #930) — the v0.5.14 forecast was a 12-sample client-side linear regression on raw sensor history. Removed entirely. The daemon-backed predicted ΔT from Layer-C marginal RLS lands via #43 (P0 follow-up).
- **Hero spark Y-axis pinned** (#930) to 20–100 °C so the line evolves left-to-right with new samples instead of rescaling per poll.
- **EMA-smoothed hero spark line** (#933) — alpha = 0.4 — to suppress per-poll sensor jitter that rendered as visual chaos within a single frame. Big number above the spark is still the raw current reading.
- **Killed dual-renderer fight** (commit 67ae370) — the OLD \`/api/v1/status\` 1 Hz writer and the NEW alive-overlay 1.5 s writer were both setting the same \`hero-cpu-path\` SVG d-attribute with different data. Disabled the old writer; alive overlay owns the hero spark now. This was the source of Phoenix's "every poll the line resets to flat then back to jagged".
- **Pinned narrator strip** (#929) — was rotating through past decisions every 6 s, giving the impression of constant new activity. Now pins to the most-recent decision until a newer one arrives, transitions to "system idle — no decisions in N s" after 12 s of silence.
- **Smart-mode "Last probe" formatter** (#929) — was rendering Go's zero-time as "17753741h ago"; now shows "—" / never (closes #921).
- **\`/api/v1/profile/active\` GET branch** (#929) — was POST-only; dashboard.js polled it as GET and got 405 silently. Added the GET branch returning \`{"name": "<active>"}\`.

### Backend correctness
- **NVML enumeration fix** (#927) — the v0.5.14 \`/api/v1/hardware/inventory\` returned 7 sensors all with \`id="0"\` and \`kind="temp"\` for the GPU chip regardless of unit. Two readings (210 MHz, 405 MHz GPU clocks) were classified as temperatures, which is why the Hardware page "Hottest" cell read "405°" (#920, #922). Fixed by composing per-NVML sensor IDs as \`gpu<idx>:<metric>\` and classifying \`kind\` from the Metric field rather than the unit string. NVML metrics that don't fit the four-kind enum (util / clocks) drop out of inventory rather than being mislabeled.

### Honest framing
v0.5.15 closes out the visible-flicker / fake-forecast / dual-renderer-fight class of bugs in the v0.5.14 design handoff. The DAEMON's actual smart-mode predictions (Layer-C marginal RLS) still aren't surfaced anywhere on the UI — that's the P0 follow-up (#43) and requires a new daemon endpoint exposing per-sensor predicted ΔT. Today's UI honestly says "—" / "no recent decisions" / "warming" when the model isn't yet contributing, instead of fabricating numbers. The in-UI update button (#934) makes it possible for the smart-mode stack to accumulate days of real samples without losing them to snapshot rebuilds.

## [v0.5.14] - 2026-05-04

### Headline
- **Hardware page** (#912) — second of the cal.ai design-handoff trio. Devices and Sensors collapse into a single `/hardware` page with three views: Inventory (chip → sensor tree, sparklines from a 60-sample per-channel history ring, side rail showing which curves consume the selected sensor), Topology (daemon ← chip ← sensor wiring with animated packet flow), and Heatmap (case-shaped layout with sensors at their operator-supplied (x, y) positions; renders a clean empty state directing the operator to declare positions in YAML when none are set).
- **Smart-mode page** (#913) — new `/smart` surface for the v0.5.5+ smart-mode stack. ventd has been quietly running a continuous learning stack since v0.5.5 (opportunistic probing, Layer A/B/C confidence aggregation, marginal-benefit RLS per workload signature, predictive-vs-reactive PWM blending) but operators had no surface to see any of it. This page makes the AI visible. Reuses the calibration v2 chrome (header, bridge pipeline, scope, fan strips, system card, log card) but rewires every signal to real backend endpoints. Phoenix's framing: *ventd IS an AI constantly improving and calibrating your fans in the background, not a page that pretends to be one.*
- **Dashboard alive overlay** (#914) — third and final cal.ai handoff. Layered on top of the existing `/dashboard` to turn it from a status page into a storyteller: hero spark forecast bands (linear extrapolation of last 12 samples × 30 s), sensor / fan tile intent arrows + flash-on-decision, narrator strip rotating real decisions ("ramped pump_fan from 35% → 42% — cpu_pkg trending up"), insight rail (coupling map with `<animateMotion>` packets when fan duty > 30 %, decision feed, AI brief). Existing polling intact — new 1500 ms tick adds `/api/v1/hardware/inventory` + smart endpoints in parallel without double-fetching `/status`.
- **Runtime-probe `pwm_enable` enum** (#911) — refines #910's hardcoded `pwm_enable = 5` EINVAL fallback. HIL on Phoenix's MSI PRO Z690-A surfaced the in-tree-nct6687 case where the chip rejects `5` as well; the driver build accepts only `{0, 1}`. Replaced with a runtime probe of `{2..7}` on the first EINVAL per pwm path, picking the highest-numbered accepted value (richer-mode-wins on conventional drivers). Probe runs once per pwm path per daemon lifetime (cached in `probedPWMEnableModes`). When the probe finds nothing, surfaces a distinct INFO line ("driver supports only manual control") before falling through to the safe-PWM floor. RULE-HWMON-ENABLE-EINVAL-FALLBACK refined; 3 mode-5-specific tests replaced with 4 probe-based tests.

### Added
- `GET /api/v1/hardware/inventory` composes `monitor.Scan()` with the live config to surface bus / kind / alias / used_by per sensor plus a top-level `curves[]` coupling array. Per-sensor history ring (cap 60 samples, in-process, appended on each call) so sparklines have chronological history without client-side state across reloads.
- `config.Sensor` and `config.Fan` gain an optional `Position{X, Y}` field for the Heatmap view. Operator-supplied via YAML; nil when unset.
- New web pages: `web/hardware.{html,css,js}` (75 / 647 / 1023 lines), `web/smart.{html,css,js}` (72 / 575 / 942 lines).
- Sidebar canonical updated twice: Devices + Sensors collapse into a single Hardware entry (#912); Smart mode entry added under Control between Dashboard and Curves (#913). Propagated byte-for-byte across all 8 sidebar-bearing pages (RULE-UI-03).

### Changed
- `web/dashboard.{html,css,js}` extended with the alive overlay (192 → 261 / 538 → 1009 / 746 → 1749 lines). Existing `/api/v1/status` polling wrapped not replaced; demo-mode banner pathway untouched.

### Fixed
- Excluded-channel handback no longer strands NCT6687D-driven channels at the calibration sweep's last byte when the in-tree driver build rejects both `pwm_enable = 2` (standard auto) and `5` (SmartFan); the runtime probe correctly finds the empty set and falls through to the safe-PWM floor with an explicit INFO line (#911).

### CI / chore
- Five contract tests in `internal/web/hardware_inventory_test.go` cover the load-bearing inventory paths (empty config returns well-formed envelope, alias mapping from config, used_by populated from curves, history ring accumulates chronologically, position propagates).

### Honest framing
v0.5.14 closes out the cal.ai design-handoff trio that v0.5.13 set up — Hardware + Smart-mode + Dashboard alive all land in this tag. Plus the in-tree-nct6687 EINVAL refinement that surfaced from Phoenix's MSI PRO Z690-A HIL run. **No fake AI theatre on any of the new pages**: every visible signal traces to a real endpoint or a real client-side computation over real history. Where data isn't computable from existing endpoints (acoustic dBA estimate, next-probe ETA, per-sensor physical positions on first ship), the affordance is omitted or shows an honest empty state rather than a fabricated number.

## [v0.5.13] - 2026-05-04

### Headline
- **Calibration v2** (#906) — operator-facing calibration takeover replaced with the claude.ai/design v2 layout. Command bridge with phase pipeline + live channels/sps/total/fans-ready stats, oscilloscope with PWM/tach/ADC ribbon, per-fan strips with sparklines + live PWM%/RPM cells, system card, thermal preview, and the climactic compute hero. Vanilla JS (no React/JSX/CDN per RULE-UI-01), token-only colours (RULE-UI-02), demo mode (`?demo=1`) for screenshots and offline preview. Per-fan strips read `FanProbe.CurrentPWM` / `FanProbe.CurrentRPM` directly so the sweep shows real numbers, not em-dashes.
- **Live activity feed via SSE** (#907) — new `GET /api/v1/setup/events` streams the structured `{ts, level, tag, text}` event log that `setup.Manager` appends on every phase transition. Frontend opens an `EventSource`, renders one row per event with colored level glyph + tag pill in the calibration narrator card. "Ring + cursor poll" transport (250 ms tick, 500-entry bounded ring) avoids per-subscriber goroutine plumbing for a write-rare/read-rare workload. `setPhase` is the only emit hook today; per-fan transition emits drop in via the same `EmitEvent` surface in a follow-up.
- **Calibration recovers from BIOS Q-Fan contention** (#905, closes #904) — Backend.Write now detects `EBUSY` on the duty-cycle write (BIOS reasserted `pwm_enable=2` mid-sweep, classic Gigabyte Q-Fan / Smart Fan Control behaviour on IT8xxx chips), drops the cached acquired-state for the channel, re-writes `pwm_enable=1`, and retries the original write exactly once. Single retry only — never spin. New `RULE-HWMON-MODE-REACQUIRE` documents the sustain contract; `RULE-HWMON-ENABLE-MODE` covered the first-write contract previously.

### Changed
- `setup.Manager` gains an in-memory event ring buffer (`events []Event`) plus `EmitEvent` / `EventsSince(cursor)` accessors. Public surface for any future emit hook (per-fan transitions, recovery actions, etc.).
- `internal/hal/hwmon/Backend.Write` split into Write + private `writeDuty` so the EBUSY-retry path can re-invoke the duty write after re-acquiring manual mode without duplicating the rpm-target / pwm dispatch.

### Fixed
- Empty "Instructions" modal, "Calibration could not complete" error banner, and "Calibration finished" done banner all showed on page load on the new calibration takeover (#906) because author CSS rules for those elements set `display: flex|grid` which beats the UA stylesheet's `[hidden] → display: none` default. Added explicit `[hidden] { display: none !important; }` to `web/calibration.css`. Caught from Phoenix's HIL screenshots before ship.

### CI / chore
- Backend test fixtures (`internal/hal/hwmon/export_test.go`) gain `NewBackendForTestWithDuty` so tests can inject the duty-cycle write. Required for the EBUSY-retry binding tests; production callers leave `writeDutyFn` nil and use `NewBackend` unchanged.

### Honest framing
The handoff from claude.ai/design landed with three pages — Calibration (primary), Hardware, and Dashboard. v0.5.13 ships the **Calibration** half (PR-1 layout + PR-2 live feed) plus the **EBUSY-retry** backend fix that surfaced from the first HIL run on the Proxmox host (Gigabyte B550M AORUS PRO + IT8688). The activity feed currently emits one event per phase transition (~7 per run); per-fan transition hooks (`cal.start` / `cal.minspin` / `cal.done`, etc.) are scoped for the next round and drop in via the same `EmitEvent` surface without touching the SSE transport. Hardware page redesign + Dashboard restyle are queued but unstarted; they ride in their own tags once they land.

## [v0.5.12] - 2026-05-04

### Headline
- **R30 acoustic capture + calibration** — `ventd calibrate --acoustic <mic_device>` CLI subcommand wires up mic capture → IEC 61672-1 A-weighting → K_cal reference-tone offset. Split across #886 (A-weighting filter coefficients), #892 (runner extracted to `internal/acoustic/runner`), #887 (`calibrate_acoustic` PhaseGate constructor), #893 (Manager.run gate wiring), #894 (`--mic` flag + adapter). Privacy contract: WAV temp files architecturally denylisted from diag bundles (RULE-DIAG-PR2C-11), no audio device opened by the daemon, only the operator-invoked CLI.
- **R32 quietness-target preset** (#888, #891) — operator-typed dBA cap (Silent=25 / Balanced=32 / Performance=45 dBA per R32 user-perception thresholds, with explicit-value override via the new `dba_target` config field). Cost gate refuses ramps where predicted dBA exceeds the budget; refusal cascade applies after Path-A and benefit-vs-cost (RULE-CTRL-PRESET-04). Wired into `BlendedController.Compute` via the `AcousticBudget` struct from per-fan R33 proxy + per-host R30 K_cal.
- **R31 fan-stall detector — advisory only** (#889) — 2-of-3 detector during calibration soak (broadband rise ≥6 dB / crest factor excess ≥2 / kurtosis excess ≥1.5), triggered when at least 2 cross threshold within a 3 s window. Output is a flag (`AcousticStallSuspected`) propagated to the polarity classifier; **never refuses fan writes**. 5× RULE-STALL-* invariants bound to synthetic-fixture subtests; MIMII real-world validation deferred to a follow-up PR.
- **R36 IT5570 chip-probe fallback** (#885) — schema v1.3's `chip_probe: {hwmon_name}` field for mini-PCs whose BIOS authors leave DMI as the literal string `"Default string"` (Beelink / Minisforum / GMKtec / AceMagic). Tier-1.5 matcher walks `/sys/class/hwmon/*/name` when DMI fingerprint is empty / default, binds to the matching board profile via the `chip_probe` anchor. Confidence 0.85 (vs DMI tier-1 0.9). Existing 16 board rows unaffected.

### Changed
- **Acoustic calibration runner extracted to internal package** (#892) — calibration logic from `cmd/ventd/calibrate_acoustic.go` moved into a reusable internal package so both the CLI subcommand and the wizard PhaseGate drive the same code path without duplication.

### Fixed
- **A-weighting filter coefficients** (#886) — IEC 61672-1:2013 Class 1 cascade now correctly sized for fs=48 kHz (3-stage biquad via canonical bilinear transform). Matches the standard's tolerance band (1 kHz ≈ 0 dB, 100 Hz ≈ -19 dB, 10 kHz ≈ -2.5 dB) within the wider error envelope absorbed for bilinear roll-off near Nyquist.

### CI / chore
- **errcheck discards on six pre-existing offenses** (#890) — explicit `_ =` discards keep the per-package errcheck floor at zero so future violations stand out.
- **CI unblock + diag-send flake registration** (#895, #897, #899, #900, #902) — fixes that were exposed by the v0.5.12 acoustic stack landing on main:
  - `scripts/retry-flaky.sh` rewritten in pure POSIX awk (drops the `python3: command not found` failure on Fedora / Ubuntu 22.04 / Debian 12 / Arch minimal containers).
  - `gcc libc6-dev` added to ubuntu-22.04 + debian-12 prereqs and `gcc` added to opensuse-tumbleweed prereqs (#897). The race detector requires CGO and the previous python3-blocked path masked the missing-compiler error on three distros that don't ship gcc by default. Closes the `-race+CGO` half of the project's known-red CI memory.
  - `defaults.run.shell: bash` set at the build matrix job level (#899) so opensuse-style `/bin/sh` brittleness can't take down post-prereqs steps; `shell: sh -e {0}` override added back to the prereqs step itself (#900) so Alpine's pre-bash-install path keeps working.
  - `docs/binary_size_baseline` BYTES bumped 9441572 → 11821348 (acoustic-stack growth, +25%).
  - `TestHandleDiagSend_IngestRejects_Returns502` registered in `.github/flaky-tests.yaml` (issue #883). Roadmap fix is to inject `diag.Generate` via a `Server` field so the test mocks bundle generation.
  - E2E (browser) job routed through `retry-flaky.sh` so registry entries actually fire on that lane.
  - `build-and-test-opensuse-tumbleweed` lane disabled in the matrix (#902, tracking #901). Every step after `Install prerequisites` fails with `OCI runtime exec failed: exec failed: unable to start container process: exec: "<shell>": executable file not found in $PATH` — `sh`, `bash` (PATH lookup), and absolute `/usr/bin/bash` all hit the same wall even though the binary is verifiably installed. None of the four upstream fixes resolved it. The same `actions/setup-go` runs cleanly on every other distro in the matrix. The matching context in `.github/rulesets/main.json` is left in place (modifying branch protection requires explicit operator OK).

### Honest framing
v0.5.12 closes the R28-R36 acoustic implementation arc that began in v0.5.11. The R30/R31/R33 research bundles synthesise into one operator-visible behaviour: type `25 dBA` in the Silent preset, the daemon opens the mic on demand for one-time calibration, the cost gate refuses ramps that would exceed the budget. Privacy-sensitive surfaces (mic capture, ALSA device opening) are gated behind an explicit opt-in (`--mic` flag); the WAV temp files are architecturally denylisted from diag bundles; the only persisted artefacts are the K_cal scalar offset + dBA-vs-PWM curve in the calibration JSON. R31's stall detector is intentionally advisory-only this release — it surfaces a flag but never blocks fan writes; refusal-on-stall is deferred until MIMII validation lands in a follow-up PR. The PR-3 cost-gate work in #888 also lifts the v0.5.11 `CostFactorBalanced=0.01°C/PWM` synthetic constant out of the controller in favour of the per-fan R33 `CostRate` measurement, which is the load-bearing simplification that makes the dBA budget refusal computationally cheap on the hot path.

## [v0.5.11] - 2026-05-03

### Headline
- **R33 no-mic acoustic proxy** (#867) — new `internal/acoustic/proxy/` package estimates per-fan and per-host loudness from PWM/RPM/blade-pass heuristics alone (no microphone, no audio library linked). Four-term sum (S_tip + S_tone + S_motor + S_pump) over 9 fan classes, 13 R33-LOCK-* invariants bound 1:1 to subtests. Score is dimensionless (`au`), within-host comparable; absolute dBA conversion still requires R30 mic calibration. Foundation for v0.5.12's per-fan cost-gate refactor.
- **Schema v1.3** (#866) — board profiles gain optional `pwm_groups: [{channel, fans}]` for the energetic-sum penalty when one PWM channel drives N fans (R29's Z690-A finding); driver profiles gain `blacklist_before_install: [module]` (generalises the MS-7D25 nct6683 blacklist) and `kernel_version: {min, max}` (R36's per-row kernel gates). Three new RULE-HWDB-PR2-15/16/17 validators. Existing v1.2 catalog rows pass through unchanged.
- **CostRate helper** (#868) — exposes `acoustic.proxy.CostRate(class, rpm, ..., rpmPerPWM, preset)` returning marginal acoustic cost in au per PWM unit, with preset multipliers Silent=3.0 / Balanced=1.0 / Performance=0.2. Wired in v0.5.12 PR-E to replace the synthetic 0.01°C/PWM `CostFactorBalanced` constant in the blended controller's cost gate.

### Changed — workflow hygiene + collaboration discipline
- **Full friction audit** (#864) — concurrency cancel-in-progress on every workflow, `paths-ignore` for docs/.claude/CHANGELOG to skip CI on doc-only PRs, `make pre-push` target wrapping `scripts/ci-local.sh`, branch-cleanup workflow sweeping merged branches >7 days, attribution-lint CI gate (`.github/workflows/no-ai-attribution.yml`) blocking AI footers at the line-anchored regex level, HIL smoke workflow scaffolded, RULE-INDEX.md un-tracked (now generated locally; was a serial rebase-conflict source), `release-changelog.yml` auto-CHANGELOG workflow + `cliff.toml` retired in favour of manual CHANGELOG entries per release.
- **Collaboration audit** (#862) — `docs/rules/collaboration.md` rewritten end-to-end: standing-delegations section pre-authorises `git tag` / `goreleaser release` / branch deletion / rebase / flake-rerun, CI-flake threshold raised 20→45 min (5-distro × race matrix takes 25-40 min on green), design-conflict-based rebase-escalation replaces count-based, `gh pr merge --auto` + `scripts/dev/{prs,wait-and-merge}.sh` documented, GraphQL `updatePullRequest` workaround for the `gh pr edit --body` projects-classic deprecation captured.
- **Rule staleness pass** (#865) — removed setup-token from web-ui.md + usability.md (eliminated in v0.5.8.1 #765/#794, first-boot is password-set-on-empty-auth.json now), removed NixOS from supported-distros pending modprobe.d-fragment work, attribution.md adds the CI gate as the enforcement reference.

### Honest framing
- v0.5.11 ships PR-A/B/C-1/C-2 from `tingly-twirling-duckling.md` — the catalog/schema/proxy half of the R28-R36 implementation arc. The v0.5.12 acoustic trio (PR-D `ventd calibrate --acoustic`, PR-E quietness-target preset, PR-F acoustic stall verification) is the larger commitment and ships under its own tag because the privacy-sensitive surfaces (mic capture, ALSA device opening) deserve a release-notes pass + documentation focus separate from this catalog/cost-gate work.

## [v0.5.10] - 2026-05-03

### Headline
- **Doctor surface** (#813) — runtime issue-detection CLI + 14 detectors covering preflight regression, polarity flip, DKMS status, userspace-daemon conflict, modules-load drift, battery transition, AppArmor profile drift, kmod-loaded check, experimental flags, container post-boot, calibration freshness, hwmon-index swap, DMI fingerprint match parity, permissions audit, GPU readiness, kernel-update transition. Severity rolls up to exit code (0 OK / 1 Warning / 2 Blocker / 3 Error). KV-backed suppression with TTL + acknowledge-forever.
- **Probe-then-pick driver selection** (#824) — replaces the static catalog→chip→driver pick with a candidate loop that tries each driver and trusts the kernel's chip-ID rejection as the authoritative signal. A stale catalog entry now costs ~30s of compile time, not 12 hours of debugging. Was the load-bearing fix from the MS-7D25 IT8688E→NCT6687D incident.
- **R36 OEM mini-PC catalog** — 20 board entries × 7 vendors (Beelink, MINISFORUM, GMKtec, AceMagic, Topton/CWWK, GEEKOM, AOOSTAR) (#861) closing R28 §3 long-tail gap. Three motherboard supply pools collapse the 8-vendor list: AMD Phoenix/Hawk Point + ITE IT5570; Intel Alder Lake-N + ITE IT8613E; AMD Rembrandt + ASUS PN53.
- **Preflight orchestrator** (#816) — terminal-first interactive auto-fixes for DKMS / Secure Boot / kernel-headers / build-tools / module conflicts. Replaces the legacy implicit-precondition install path with explicit predict-then-fix-or-abort.

### Added — research bundle (~5500 lines, foundation for v0.5.12 acoustic features)
- R28 master synthesis + 9-agent failure-mode survey (#842, #843, #846)
- R29 — acoustic capture analysis (Phoenix's MSI Z690-A under Tdarr) (#860)
- R30 — microphone calibration procedure (#856)
- R31 — fan-stall acoustic signatures (bearing wear / blade flutter / pump cavitation) (#855)
- R32 — user-perception thresholds (Whisper/Office/Performance preset rationale) (#854)
- R33 — no-mic acoustic proxy (four-term sum + 15 LOCK invariants) (#857)
- R36 — OEM mini-PC EC firmware survey (#859)

### Added — recovery + classifier (R28 Stage 1)
- ThinkPad fan_control classifier rule (RULE-WIZARD-RECOVERY-10) (#831)
- Vendor-daemon + NixOS probes (RULE-WIZARD-RECOVERY-11/12) (#832)
- Laptop-class FailureClass extensions (#830)
- AMD OverDrive bit detection probe (RULE-WIZARD-RECOVERY-13) (#835)
- DetectVendorDaemon wiring into wizard preflight (#840)
- /api/setup/apply-monitor-only endpoint for vendor-daemon deferral (#838)
- /api/hwdiag/modprobe-options-write endpoint for ThinkPad fan_control (#841)
- Reboot-prompt UX for cards whose effect needs next boot (#818) (#828)

### Added — observation / diag / iox
- iox canonical atomic-write helper + 6 call-site migrations (#848)
- diag export-observations subcommand activates internal/ndjson (#845)
- Cpuinfo hypervisor flag as 4th virt-detect source (#851)

### Changed
- README honesty pass per github-page-audit + Sponsors button (#829)
- Sign-guard wired into opportunistic prober + marginal runtime (#844)
- Sub-absolute-zero temperature now rejected as sentinel/underflow (#837)
- PlausibleRPMMax raised 10000→25000 to admit server-class fans (Delta/Sanyo Denki 12k-22k) (#850)
- Container detection adds /run/.containerenv + /proc/1/environ sources (#836)

### Calibration / smart-mode
- Calibration UX rework: min phase duration, in-grid done banner, finalising spinner (#826)
- Clock injection on ZeroPWMSentinel — sub-millisecond tests (was 14s of real-time sleep) (#853)

### Fixed
- MS-7D25 (PRO Z690-A DDR4) selects nct6687d, not it8688e (#822) — the 12-hour-debug catalog defect
- Dashboard demo-mode false positive + recovery + visible signal + logout (#820) (#827)
- Dashboard shows duty % when fan has no tach (NVIDIA, etc.) (#825)
- Topbar popover stacking renders above dash-hero cards (#823)
- thinkpad_acpi catalog: experimental=1 → fan_control=1 (canonical name) (#847)

### CI / chore
- R28 catalog audit P0/P1 fixes (#847)
- Dead packages + 3 unused exports removed (R28 codebase audit) (#849)
- Issue466_ReloadFailureIsNonFatal registered as arm64 timing flake (#852)
- CHANGELOG backfill for missing v0.5.8.1 + v0.5.9 sections (#858)

## [v0.5.9] - 2026-05-02

### Added
- Wizard recovery — diag bundle button on calibration error banner (#799)- V1.x amendment — extend pwm_control allowlist for modern hardware (#801)- Aggregator (R12 chain) — v0.5.9 PR-A.2 (#802)- IMC-PI confidence-gated blended controller — v0.5.9 PR-A.3 (#803)- Smart-mode preset enum + setpoints map — v0.5.9 PR-A.4 (#804)- Wire confidence-gated controller into hot path — v0.5.9 PR-B (#807)- Per-failure-class wizard + doctor classifier (#800) (#810)- Predictive preflight + controlled install pipeline + gated wizard (v0.5.9 PR-D) (#811)
### CI
- Switch Ubuntu host AppArmor step to canonical mirror + retry (#806)
### Documentation
- V0.5.9 — confidence-gated controller + predictive install (#814)
### Fixed
- IPv6 regex over-matched ISO-8601 time-of-day (#808)- Restore pwm_enable=2 on channels excluded from doneFans (#753) (#758)

## [v0.5.8.1] - 2026-05-01

### Added
- Layer-A confidence estimator (conf_A) — v0.5.9 PR-A.1 (#760)- One-line curl-pipe-bash installer (#764)- SUID-root helper for unprivileged NVML writes (#771)
### Documentation
- V0.5.9 confidence-gated controller design (#752)
### Hwmon/install
- Fail-fast on missing kernel version + 5min install timeout (#775)
### Setup
- Re-run daemon probe + persist outcome after driver install / load-module (#776)
### V0.5.8.1
- Plumb SensorReadings into the observation log (#756)- Flip daemon to root, drop layered-elevation theatre (#794)- Rc4 follow-ups from HIL — apparmor syntax + dashboard sparklines + ReadWritePaths (#798)

## [v0.5.8] - 2026-05-01

### Added
- v0.5.8 PR-A — Layer-C per-(channel, signature) marginal-benefit RLS estimator: `internal/marginal/` package implementing `d_C = 2` model `ΔT_per_+1_PWM = β_0 + β_1·load` per R10 §10.1, dual-path saturation per R11 §0 (Path A predicted + Path B observed), three-condition warmup gate with parent Layer-B clearance, R12 bounded-covariance clamp, weighted-LRU shard eviction (cap 32/channel), κ-deferred activation (τ_retry=1h), OAT cross-channel gate (R28 mitigation), persistence at `smart/shard-C/<channel>-<sig>.cbor` per R15 §104. Plus `internal/coupling/signguard/` — 5-of-7 sign-vote consuming v0.5.5 opportunistic-probe ground-truth (R27 wrong-prior detection). 22 RULE-CMB-* + 3 RULE-SGD-* bindings. (#741)
- v0.5.8 PR-B — daemon wiring: `cmd/ventd/main.go` launches `marginal.Runtime` alongside `coupling.Runtime`; `Config.SmartMarginalBenefitDisabled` toggle. RULE-CMB-WIRING-01 + RULE-CMB-WIRING-03. (#742)
- `internal/idle.CaptureLoadAvg` exported for v0.5.8 Layer-C load proxy.

## [v0.5.7] - 2026-05-01

### Added
- v0.5.7 PR-A — Layer-B thermal coupling estimator: `internal/coupling/` package implementing per-channel RLS estimator with rank-1 Sherman-Morrison updates (`gonum mat.SymRankOne`), bounded-covariance directional forgetting (R12 ceiling `tr(P) ≤ 100`), three-condition warmup gate (`n_samples ≥ 5·d²` AND `tr(P) ≤ 0.5·tr(P_0)` AND `κ ≤ 10⁴`), three-way κ classification (R9 §9.4), signed Pearson co-varying fan detection (`ρ > 0.999`), spec-16 KV persistence with `hwmon_fingerprint` invalidation. Lock-free Snapshot.Read via `atomic.Pointer` for the controller hot loop. Determinism replay test, observability via structured slog, privacy invariants (no comm names in Snapshot). 13 RULE-CPL-* bindings. (#736)
- v0.5.7 PR-B — daemon wiring: `cmd/ventd/main.go` launches `coupling.Runtime` alongside controllers; one shard per controllable channel; `N_coupled = 0` (R9 §U4 well-posed reduced model); `hwmon_fingerprint = dmiFingerprint`. RULE-CPL-WIRING-01..03. (#738)

## [v0.5.6] - 2026-05-01

### Added
- v0.5.6 PR-A — workload signature library + integration tooling: `internal/signature/` package (SipHash-2-4 keyed by per-install salt, EWMA-multiset with K-stable promotion, 128-bucket weighted-LRU, spec-16 KV persistence), `internal/proc/walker.go` shared process walker, `internal/idle/blocklist_export.go` re-exports R5 maintenance-class names. Plus integration tooling: `tools/rulelint --suggest --check-binding-uniqueness`, `internal/testfixture/fakeprocsys`, `internal/smartmode/` cross-spec integration tests, `scripts/ci-local.sh`, `scripts/install-git-hooks.sh`, `scripts/retry-flaky.sh`, `.github/flaky-tests.yaml` (#730)
- v0.5.6 PR-B — controller wiring + Settings toggle + closes the v0.5.4 obsWriter gap. Every successful PWM write emits an observation record stamped with the current signature label. (#731)

### Changed
- `internal/idle/idle_test.go` and `internal/probe/opportunistic/helpers_test.go` migrated to the shared `fakeprocsys` test fixture, removing duplicated inline `makeIdleProcRoot` helpers.

## [v0.5.5] - 2026-05-01

### Added
- v0.5.5 PR-A — Layer A gap-fill probe library: `internal/idle/OpportunisticGate`, `internal/probe/opportunistic` (Detector / Scheduler / Prober / install_marker), schema bump 1→2 with `EventFlag_OPPORTUNISTIC_PROBE`, `Config.NeverActivelyProbeAfterInstall` toggle, 18 RULE-OPP-* invariants (#725)
- v0.5.5 PR-B — daemon wiring + Settings "Smart mode" toggle + dashboard probe-in-flight pill + `/api/v1/probe/opportunistic/status` endpoint (#726)

## [v0.5.4] - 2026-05-01

### Added
- Passive observation log (v0.5.4 smart-mode) (#693)
- v0.5.5 opportunistic probing spec (#724)

### Changed
- Redesign every page on the new design system (#704)
- CC sessions now load `docs/rules/INDEX.md` instead of fanning out across all rule files. Rules are read on demand via `go run ./tools/rule-index`. Reduces session preload by ~24-48k tokens. (#686)

### Fixed
- First-boot apparmor + listen + sd_notify so fresh installs actually work (#702)
- Stop upper-casing the first-boot setup token (#712)
- Defer service start until prerequisites are in place (#711)
- CSP style-src violations + /api/v1/profile/active 405 polling (#713)
- Remove dead /logs link from every sidebar (#715)
- Drop inline <script> from / redirect helper (#714)
- No-fan-host escape from /calibration; persistent applied marker (#717)
- Inline change-password form + JSON body matching the API (#720)
- Progress.Applied honours marker; idle preconditions go fork-free (#723)

### Documentation
- HIL soak log + open-items list for fix(first-boot) (#703)

### Tests
- Stop scheduler goroutine race and context leaks in test helpers (#687)

## [v0.5.3] - 2026-04-29

### Added
- Envelope C/D probe + idle gate wiring (v0.5.3 PR-B) (#685)

## [v0.5.2.1] - 2026-04-29

### Added
- Sysclass + idle foundation (PR-A) (#682)
### Chore
- Apply Thariq 9-rule audit to all SKILL.md files and add cost-routing reference (#683)
### Documentation
- Research archive + spec-16 post-ship cleanup + untracked spec drafts (#677)
### Fixed
- Serve ui/index.html at root instead of web mockups index (#674)- Remove dead savedFan code and harden Alpine torn-read test (#676)- Add GetFanControlPolicy to nonvidia stub (#681)

## [v0.5.2] - 2026-04-27

### Added
- Cross-backend polarity probe + wizard screen (spec-v0_5_2) (#673)

## [v0.5.1] - 2026-04-27

### Added
- Install design system foundation and new landing page (#661)- Schema v1.2 — typed experimental block with Levenshtein forward-compat (#662)- Framework scaffolding (spec-15 PR 1) (#663)- Amd_overdrive F1 — precondition, HAL gate, RDNA4 kernel check (#664)- Persistent state foundation + smart-mode README (spec-16 PR 1) (#669)- Catalog-less hardware probe and three-state wizard fork (spec-v0_5_1) (#670)
### Chore
- Ignore worktrees, log spec-12 PR 1 cost calibration (.41)- Ignore worktrees, log spec-15 PR 1 cost calibration ($9.78)- Remove leaked CC prompt files (#671)
### Documentation
- Spec-14a + spec-14b — hardware-profiles repo design + submission flow (#658)- Add v0.6.0 mockup screenshots and HTML preview (#665)- Update screenshots to v0.6.0 UI and add page gallery (#666)- Add spec-16, smart-mode, v0.5.1 probe, and amendment specs (#667)- Remove superseded spec-04 pi-autotune and amendment (#668)
### Fixed
- Wire os.DirFS defaults for SysFS/ProcFS/RootFS in New() (#672)

## [v0.5.0] - 2026-04-26

### Added
- Freeze v1 schema (spec-03 PR 1) (#629)- Add CC tooling bundle (preflight, release-validate, templates) (#632)- Spec-03 PR 2a — three-tier matcher and chip-family catalog (#635)- Spec-03 PR 2b — runtime probe + unified types + apply-path enforcement (#636)- Spec-03 PR 2c - diagnostic bundle, redactor, NDJSON substrate (#639)- Spec-03 PR 2d — GPU vendor catalog (NVIDIA/AMD/Intel) (#643)- Spec-03 PR 3 — board catalog seed (15 entries) (#644)- Spec-03 PR 4 - schema v1.1 (bios_version, dt_fingerprint, unsupported) (#646)- Spec-03 PR 5 — scope-C catalog seed + spec-12/13 docs (#647)- Capture pipeline — pending profiles after calibration (spec-03) (#649)
### Chore
- Add triage-run.sh + verify-local.sh (#628)- Post-spec-03 PR 4/5/gaps/capture cleanup (#653)
### Documentation
- Add spec-03 PR 1 working document- Log PR #632 cost + spend velocity tracking (#634)- V0.5.0 spec batch + GPU catalog research (#641)- Predictive thermal thesis + v0.4.0 Corsair / v0.3.1 IPMI shipped (#642)- PR 4/5 gaps - hwdb-schema v1.1 section, amendment status, changelog (#648)- V0.5.0 hardware database, runtime probe, ventd diag (#654)

