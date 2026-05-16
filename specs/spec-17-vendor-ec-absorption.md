# spec-17 — Vendor EC + Super-I/O absorption (laptop + desktop catalogue widening)

**Status:** draft. Targets v0.8.x cycle, work-tree opens after spec-09 NBFC backend lands.

**Scope:** widen ventd's recognised-hardware surface by absorbing 15 sibling open-source vendor-EC projects, on the same shape as spec-09's nbfc-linux absorption. Every Linux user with one of the targeted vendors should see their laptop or desktop recognised in `ventd doctor` and (where the upstream driver permits writes) controllable via the existing HAL pipeline.

The seed for this work was the `BeardOverflow/msi-ec` mining — the canonical out-of-tree vendor-EC kernel module that exposes MSI laptop fans + perf-mode + battery-charge-threshold as hwmon / platform_profile / sysfs attrs. We already ship a catalog row + a `propose msi-ec` doctor hint (PR #1120). spec-17 generalises that pattern across the remaining vendors.

## 1. Background — what's already absorbed vs what's missing

The pre-spec-17 catalogue covers roughly half the surface. As of v0.7.1:

| Vendor / project | Driver row | Board row | HAL backend | Status |
|---|---|---|---|---|
| `legion_laptop` (johnfanv2) | ✅ `legion_hwmon.yaml` | ✅ `lenovo-legion.yaml` | ❌ catalog-only | Gap: dedicated HAL backend (mirror `thinkpad`) |
| Framework `cros_ec_fan` / kmod-impostor | ❌ | ✅ `framework.yaml` | ⚠️ via `internal/hal/crosec/` | Gap: dedicated driver row + `fw-fanctrl` corpus |
| `tuxedo-drivers` (Clevo/Uniwill OEM) | ❌ | ✅ `clevo-system76.yaml` | ❌ | Gap: driver row + HAL |
| `asus-wmi` / `asusctl` | ⚠️ `asus-wmi-sensors.yaml` (RO sensors only) | ✅ `asus.yaml` | ❌ | Gap: writable variant + g-helper curve corpus |
| `hp-wmi` Omen / Victus | ⚠️ `hp-wmi-sensors.yaml` (RO sensors only) | ✅ `hp.yaml` | ❌ | Gap: writable variant + omen-fan-control backport |
| `gigabyte-laptop-wmi` (AERO/AORUS) | ❌ | ⚠️ `gigabyte.yaml` (desktop only) | ❌ | Gap: laptop driver row + HAL |
| `razer-laptop-control` (Blade) | ❌ | ❌ | ❌ | Gap: full slice |
| `alienware-wmi` (Dell G + Alienware) | ❌ | ⚠️ `dell.yaml` (desktop) | ❌ | Gap: laptop driver row + HAL |
| `uniwill-laptop` (mainline-bound) | ❌ | ❌ | ❌ | Gap: full slice |
| `tongfang-fan-controller` (PF5NU1G) | ❌ | ❌ | ❌ | Gap: full slice |
| `MControlCenter` (MSI gap-fill) | n/a — uses existing `msi-ec` | n/a | n/a | Gap: catalogue rows for MSI laptops msi-ec doesn't carry yet |
| `YAMDCC` (MSI Windows gap-fill) | n/a | n/a | n/a | Gap: MSI register-map extraction script + catalogue rows |
| `surface-aggregator-module` | ❌ | ❌ | ❌ | Honorable mention — deferred to v0.9 (Surface fleet not in scope yet) |
| `Wer-Wolf/i8kutils` (Dell i8k) | ⚠️ via mainline `dell-smm-hwmon.yaml` | ✅ `dell.yaml` | ❌ | Gap: i8k module-param surface for older Dell |
| `pelrun/hp-omen-linux-module` | ❌ | ❌ | ❌ | Gap: forms part of HP slice |

Total: **15 distinct integration tasks** (some grouped under one PR, see §3).

## 2. Integration mode per source

Each upstream falls into one of three absorption modes, chosen by what the upstream provides:

### Mode A — driver-row only (catalogue + doctor surface, no HAL)
The upstream is a kernel module ventd doesn't drive directly today, but recognising it lets the wizard propose the right install + the doctor surface report capability. Pattern: `internal/hwdb/catalog/drivers/<module>.yaml`. No write code.

### Mode B — full HAL backend (driver row + `internal/hal/<vendor>/` + RULE-HAL-<VENDOR>-*)
The upstream exposes a stable userspace surface (sysfs / procfs / `/dev/<dev>`) ventd can drive end-to-end. Pattern: mirror `internal/hal/thinkpad/` exactly — one Go package per vendor, contract-test conformance via `internal/hal/contract_test.go`, restore-on-exit through the watchdog, EBUSY handling, sentinel rejection. Each backend ships with a `.claude/rules/RULE-HAL-<VENDOR>.md` binding ≥10 subtests to enforce the contract.

### Mode C — config-corpus vendoring (mirror `internal/hwdb/nbfc/`)
The upstream is a config corpus (JSON / YAML / C# dictionary) covering per-model fan-curve byte arrays or named strategies. Pattern: `internal/hwdb/<vendor>/{embed,schema,match,allowlists}.go` + `configs/*.json` + `UPSTREAM` manifest + `LICENSE.upstream`. Loader follows `nbfc.LoadCatalog` shape exactly. JSONC tolerance (RULE-NBFC-CATALOG-JSONC-01) re-used where the upstream uses non-strict JSON.

## 3. PR phases — ordered by safety-impact-per-line

Each PR is self-contained: it lands one mode-A row, or one mode-B backend with its full RULE family, or one mode-C corpus with its `nbfc`-shape loader. CI green on each PR independently. No cross-PR dependencies on master.

### PR-1: Legion HAL backend (Mode B) — state-switcher + curve-upload

Legion's write surface is structurally different from every other Mode B target. The `legion_laptop` driver exposes three control axes, none of which is a per-tick PWM byte:

1. **`platform_profile`** — three-state ACPI enum (`quiet` / `balanced` / `performance`)
2. **`powermode`** — four-state (0/1/2/3 ≈ quiet / balanced / performance / custom)
3. **`fancurve`** (debugfs, 10 points + commit) — batch curve upload; the EC then drives per-tick using its own loop

The `hal.FanBackend` contract (RULE-HAL-001..008) is shaped around a `Write(ch, uint8)` per-tick semantic. Legion needs both shapes — this PR ships them both:

**PR-1a — state-switcher (per-tick PWM → platform_profile state)**: coalesce the controller's per-tick PWM byte into a platform_profile state via three bucket thresholds (PWM<85 → quiet, 85-170 → balanced, ≥170 → performance). Mirrors the contract every other Mode B backend honours. Ships with `RULE-HAL-LEGION-STATE-*` (≥8 bound subtests covering bucket boundaries + restore + EBUSY + idempotent write).

**PR-1b — curve-upload (Apply-time batch via new `hal.CurveSink`)**: a new HAL interface extension `CurveSink interface { WriteCurve(Channel, []CurvePoint) error }` that backends opt into. Controller's apply path queries the backend via `_, ok := backend.(CurveSink)`; when ok, calls `WriteCurve` once per applied config + suppresses per-tick `Write` calls for that channel. The state-switcher path remains the fallback when `CurveSink` is not used (operator config explicitly chooses).

Deliverables (single PR):
- `internal/hal/legion/{backend,backend_test,curve,curve_test}.go` — implements both `hal.FanBackend` (state-switcher) and `hal.CurveSink` (10-point debugfs `/sys/kernel/debug/legion/fancurve` upload). Restore writes `level: 0` (BIOS auto resumes) on every exit path via the watchdog.
- `internal/hal/curve_sink.go` — defines the `CurveSink` interface + `CurvePoint{TempC, PWM}` struct + the controller-side dispatch helper `CurveOrPerTick(backend, channel, curve, pwm)` so the apply path picks the right primitive without each call site re-implementing the type-assertion dance.
- `.claude/rules/RULE-HAL-LEGION.md` with ≥12 bound subtests covering: state-switcher bucket boundaries, curve-upload payload format, debugfs write atomicity (single open + write + commit), `force=1` modparam absence graceful-degrade, EC chip-id mismatch tolerance, restore on every exit path, CurveSink interface conformance, write-during-no-curve fallback to state-switcher.
- `.claude/rules/RULE-HAL-CURVE-SINK.md` — defines the controller-side contract: a backend that implements CurveSink MUST honour per-tick Write calls too (state-switcher fallback); Apply MUST call WriteCurve before any per-tick Writes; a backend that returns nil from WriteCurve has committed the curve and the controller MAY skip per-tick Writes for that channel.
- Controller-side wiring in `internal/controller/blended.go` (apply path) to dispatch via CurveSink when supported.
- Driver row at `internal/hwdb/catalog/drivers/legion_hwmon.yaml` — upgrade from the spec-03-scope-C shape to PR-2 schema `driver_profiles[]` v1.2 + add `fan_control_via: "curve_sink"` to declare the backend.
- Wire into `cmd/ventd/calresolver.go::registerHALBackends`.

This is a ~900 LoC PR (backend ~350, curve_sink interface ~80, RULE files ~300, controller wiring ~120, tests ~250) but the resulting CurveSink primitive is then reusable for PR-7 (HP Omen fan-curve via WMI), PR-8 (Razer Blade fan-curve via HID), and PR-9 (Alienware AWCC `Thermal_Control` curve). Worth the upfront investment.

### PR-2: Framework absorption (Mode A driver row + Mode C fw-fanctrl corpus)
- `internal/hwdb/catalog/drivers/framework-laptop.yaml` — Mode A row for the Framework cros_ec hwmon surface (kernel 6.7+ `cros_ec_fan` mainline + DHowett/framework-laptop-kmod-impostor for older kernels)
- `internal/hwdb/framework/` — Mode C ingest of `TamtamHero/fw-fanctrl` strategy JSON catalogue (lazy / aggressive / quiet / medium / agile) AND `Bill-git1/fw-fanctrl-AMD` AC-vs-battery dual-curve. Schema is the union — `strategies: []FwStrategy` with `name`, `temp_points[]`, `pwm_points[]`, `power_state: "ac"|"battery"|"either"`.
- Doctor surface: when Framework board matches AND `framework-laptop-kmod` is not loaded, propose the kmod install (mirrors PR #1120's `propose msi-ec` shape).

### PR-3: ASUS absorption (Mode B asus-wmi writable + Mode C g-helper curve corpus)
- `internal/hwdb/catalog/drivers/asus-wmi.yaml` — writable variant (capability `rw_proc`) for asus-wmi's `fan_curve_*` sysfs nodes (separate from the existing `asus-wmi-sensors.yaml` RO row; the underlying kernel module is the same but the writable surface only landed in kernel 6.4+)
- `internal/hal/asuswmi/{backend,backend_test}.go` — Mode B HAL writing `fan_curve_*` per channel
- `internal/hwdb/asus/configs/` — Mode C ingest of `seerge/g-helper`'s per-model fan-curve byte-array dictionary. C# → JSON via `scripts/ingest/asus-g-helper/main.go` (one-time generator). Cross-validated against `utajum/g-helper-linux`'s Linux-translated WMI calls.
- Covers ROG / TUF / Strix / Scar / Flow / Z13 / Ally / Zenbook / Vivobook / ProArt — the richest per-model corpus in the open-source ASUS world.
- License caveat: `asusctl` is **MPL-2.0**, fine for binary linkage + algorithm study but **must not be source-copy-merged** into ventd (GPL-3). g-helper itself is GPL-3 — clean ingest. Cite both in the manifest.

### PR-4: Clevo / Uniwill / Tongfang OEM-rebadge consolidation (Mode A × 3 + shared HAL)
The three projects all drive the same family of rebadged whitebooks (Tuxedo / Schenker / Mechrevo / System76):
- `internal/hwdb/catalog/drivers/tuxedo-drivers.yaml` — Mode A row for `tuxedo_io` + `clevo-wmi` + `clevo-acpi` + `uniwill-wmi`
- `internal/hwdb/catalog/drivers/uniwill-laptop.yaml` — Mode A row for `Wer-Wolf/uniwill-laptop` (mainline-bound 6.19+)
- `internal/hwdb/catalog/drivers/tongfang-pf5nu1g.yaml` — Mode A row for `methyl/tongfang-fan-controller`'s PF5NU1G register map
- Shared `internal/hal/clevoid/{backend,backend_test}.go` — Mode B HAL targeting the consolidated `/sys/devices/platform/tuxedo_io/` write surface. Tuxedo's monorepo at `gitlab.com/tuxedocomputers/development/packages/tuxedo-drivers` is the canonical source.
- Mine the consolidated GUID list (biggest OEM-rebadge catalogue publicly available) for the Mode A rows.

### PR-5: MSI catalogue gap-fill (Mode C MControlCenter+YAMDCC)
- `internal/hwdb/msi-ec/configs/` — Mode C ingest of `dmitry-s93/MControlCenter`'s tested-devices cross-reference AND `Sparronator9999/YAMDCC`'s per-model EC register maps for MSI laptops `msi-ec` upstream doesn't carry yet (GE / GP / GS / GT modern). YAMDCC is C# (Windows); one-time generator at `scripts/ingest/msi-yamdcc/main.go`.
- No new driver row — `msi-ec` is already in mainline; this PR fills the per-model `internal/hwdb/catalog/boards/msi.yaml` rows.

### PR-6: Gigabyte AERO / AORUS laptops (Mode B `gigabyte-laptop-wmi`)
- `internal/hwdb/catalog/drivers/gigabyte-laptop-wmi.yaml` — Mode A row for `tangalbert919/gigabyte-laptop-wmi`'s WMBC/WMBD WMI surface
- Extend `internal/hwdb/catalog/boards/gigabyte.yaml` with laptop entries (AERO 14/15/17 + all AORUS gen via WMI method names)
- Mine `rcassani/p37-ec-aorus15g`'s Aorus 15G EC register map (MIT-licensed, clean ingest) into the board catalog as additional model rows.
- `internal/hal/gigabytewmi/{backend,backend_test}.go` — Mode B HAL

### PR-7: HP Omen / Victus (Mode B with the upstream-6.20 backport)
- `internal/hwdb/catalog/drivers/hp-omen.yaml` — Mode A row for `pelrun/hp-omen-linux-module`'s `hp-wmi` extension exposing Omen perf + RGB
- `internal/hwdb/catalog/drivers/hp-wmi-fan-backport.yaml` — Mode A row for `arfelious/omen-fan-control`'s DKMS shim backporting the upcoming Linux 6.20 hp-wmi manual-fan patch (Omen Max / Victus). Marked `mainline_status: backport-pending-6.20` so the catalog tracks the upstream landing.
- `internal/hal/hpomen/{backend,backend_test}.go` — Mode B HAL

### PR-8: Razer Blade (Mode B `razer-laptop-control`)
- `internal/hwdb/catalog/drivers/razer-laptop-control.yaml` — Mode A row for `rnd-ash/razer-laptop-control` (archived Jun 2022, Meetem fork active). Catalogue the Blade HID command map for fan RPM 0/auto/1-5300 + power tiers (35/55W) — only public source for Blade EC.
- `internal/hwdb/catalog/boards/razer.yaml` (new file) — per-model rows
- `internal/hal/razer/{backend,backend_test}.go` — Mode B HAL over hidraw (re-use `internal/hal/usbbase/hidraw/` substrate from the Corsair AIO backend)

### PR-9: Dell + Alienware (Mode B `alienware-wmi` backport + Mode A i8kutils refresh)
- `internal/hwdb/catalog/drivers/alienware-wmi.yaml` — Mode A row for `kuu-rt/alienware-wmi`'s backport of the not-yet-released mainline `alienware-wmi-wmax` (kernel 6.14+) for HWMON manual fan + AWCC platform_profile. Catalogue the AWCC GUID `A70591CE-A997-11DA-B012-B622A1EF5492` + `Thermal_Information` / `Thermal_Control` method names.
- `internal/hwdb/catalog/drivers/i8kutils.yaml` — Mode A row for `Wer-Wolf/i8kutils`'s modern successor to dell-bios-fan-control (`ignore_dmi` / `force` / `fan_mult` / `fan_max` module params)
- Extend `internal/hwdb/catalog/boards/dell.yaml` with Alienware + G-series rebadge entries
- `internal/hal/alienware/{backend,backend_test}.go` — Mode B HAL

## 4. Cross-cutting invariants

Every PR adheres to the existing rule families. Spec-17 introduces no new safety primitives — the catalog + matcher + HAL contract + watchdog already cover everything:

- **Closed-set allowlist** — every new HAL backend's write surface is gated by a per-backend `allowlists.go` parallel to `internal/hwdb/nbfc/allowlists.go`. Vendor-WMI method names, register offsets, and procfs paths are the closed set; corruption of a config cannot escape into a wild write.
- **Restore-on-exit** — every new backend's `Restore()` is wired into the watchdog via `RegisterIPMI`-shape registration; the existing `RULE-WD-RESTORE-EXIT` envelope covers it for free.
- **EBUSY + sentinel handling** — backends that talk to hwmon paths re-use `internal/hal/hwmon`'s primitives; backends that talk to procfs or hidraw replicate the EBUSY-reacquire pattern (`RULE-HWMON-MODE-REACQUIRE`).
- **License manifest** — every Mode C ingest carries `UPSTREAM` (commit SHA + sync date) and `LICENSE.upstream` (verbatim upstream license file). The `nbfc` package is the canonical exemplar.
- **No CGO, no panics in control loop, slog only, errors wrapped** — the existing project-wide invariants in CLAUDE.md.
- **Default writes ON** — per `feedback-dont-default-writes-off`, new HAL write paths ship default-on. The closed-set allowlist + idle/battery/container refuses are the safety mechanism, not a per-model opt-in flag. The v0.6.1 NBFC + Corsair sweep is the precedent.

## 5. Schema impact

No new schema versions required. Spec-17 is a pure catalog-and-implementation widening exercise that lives entirely inside v1.3 (the current schema), which already has:
- `chip_probe.hwmon_name` fallback for `Default string` DMI boards (RULE-HWDB-PR2-18)
- `kernel_version: {min,max}` per-driver gates (RULE-HWDB-PR2-17)
- `blacklist_before_install` for in-tree conflict resolution (RULE-HWDB-PR2-16)
- `pwm_groups` for firmware-mirrored channel families (RULE-HWDB-PR2-15)
- `bios_version` per-generation DMI dispatch (RULE-FINGERPRINT-04)
- `dt_fingerprint` for Pi-class hardware (RULE-FINGERPRINT-06/07)
- `experimental:` block for opt-in features

If a future upstream surfaces a constraint the schema can't express (e.g. a USB VID/PID match for the Razer Blade HID path), the constraint is added as a schema v1.4 amendment in the same PR that introduces it. No big-bang schema bump.

## 6. Doctor surface

For every Mode A row, the existing `NBFCMatchDetector` template (`internal/doctor/detectors/nbfc_match_d.go`) is mirrored:
- Severity OK when the kernel module is loaded
- Severity Warning + actionable card when the catalog matches but the module isn't loaded (propose-install pattern from PR #1120)
- Quiet when the host is not on the catalogue (no false positives)

The PR-7 HP backport variant carries an extra surface: when the running kernel is < 6.20 AND the host is HP Omen, surface "install the omen-fan-control DKMS backport" with a one-click apply (`/api/hwdiag/dkms-install-write` extension).

## 7. Test posture

Every Mode B HAL backend ships with:
1. ≥10 unit subtests in `internal/hal/<vendor>/backend_test.go` covering: enumerate idempotent, read no-mutation, write faithful (or vendor-quantised), restore safe on unopened, caps stable, role deterministic, close idempotent, write-second-call no-op, EBUSY handling, sentinel rejection. The thinkpad backend is the template.
2. Contract-test conformance via `internal/hal/contract_test.go`'s table — add a new entry per backend.
3. RULE family of ≥10 rules in `.claude/rules/RULE-HAL-<VENDOR>.md`, each `Bound:` to a subtest above. `tools/rulelint` enforces 1:1.

Every Mode C corpus ingest ships with:
1. The `nbfc`-shape `LoadCatalog` + `Match` + `classifyControlMode` triple, each with subtests pinning the exhaustive coverage.
2. A "loads-all-embedded-configs-cleanly" smoke test that fails on the next upstream sync if a new config introduces an unhandled construct (mirrors `TestLoadCatalog_EmbeddedFS_ParsesAllConfigs`).

## 8. Ship gate

Spec-17 has no single ship gate. Each PR-N ships independently once:
- CI green on the merge commit
- `make ci-local` sweep passes
- rulelint passes (every new RULE-* has a bound subtest, no orphan subtests)
- The corpus ingest's `UPSTREAM` manifest names a specific upstream SHA + sync date

When all 9 PRs are merged, the cycle moves to **v0.9.0 release with vendor-EC absorption headline**.

## 9. Out of scope

- **NixOS** — `/etc/modprobe.d/` drop-ins are ignored on NixOS (per the `usability.md` carve-out). PR-7 / PR-9 surface a doctor card when an `omen-fan-control` / `i8kutils` install path is needed on NixOS, directing the operator to copy the modprobe options into their `configuration.nix`.
- **Surface Aggregator (`linux-surface/surface-aggregator-module`)** — Surface fleet has no representation in the HIL set today; deferred to v0.9.
- **MPL-2.0 source merge** — `asusctl` source is studied for algorithm reference, never copied. PR-3 specifically uses g-helper (GPL-3) for source-merge ingest.

## 10. References

- Seed query: `BeardOverflow/msi-ec` mining session 2026-05-16 (the 18-source survey)
- Existing absorption template: spec-09 (NBFC backend) + the v0.7.0 "Tiers 1-4 absorption ship"
- Hardware survey methodology: `feedback_design_handoff_scope` (implement only where backing code exists; do not invent handlers to satisfy mockups)
- Default-writes-on rationale: `feedback-dont-default-writes-off`, v0.6.1 NBFC + Corsair sweep
- License compatibility table: §3 per-PR notes; all targets are GPL-2 / GPL-3 / MIT / MPL-2 (study-only)
