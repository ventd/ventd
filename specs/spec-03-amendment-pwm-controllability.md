# spec-03 amendment — PWM controllability schema (v1, comprehensive)

**Status:** DRAFT, produced 2026-04-26 in chat 2 of spec-03 PR 2 design.
**Supersedes:** the implicit `pwm_control: <string>` shape in spec-03 PR 1 schema.
**Consumed by:** PR 2a matcher (catalog read path), PR 2b calibration probe (post-probe write path), PR 2c diagnostic bundle (full dump), spec-09 NBFC integration (`requires_userspace_ec_driver` consumer), spec-05 predictive thermal (uses `polling_latency_ms_hint` for cadence).
**Source of truth for gaps:** `2026-04-hwmon-driver-controllability-map.md` §9 (10 gaps), §1 (structural realities), §12.4 (inheritance), §11 (chip catalog driving values).

**Decision recorded:** Schema v1 ships comprehensive. All 10 gaps from map §9 plus 3 fields surfaced from userspace-tool integration survey (§12.1 `exit_behaviour`, §12.2 `runtime_conflict_detection_supported`, §12.3 `firmware_curve_offload_capable`) encoded now. No v2 migration before v1.0. Phoenix directive: "if we miss something we fix it, so don't miss."

---

## 1. Schema scope and inheritance model

### 1.1 Three layers, explicit precedence

The schema has three layers. **Board overrides chip overrides driver.** Calibration-probe results override all three for runtime-discovered fields.

```
Layer 1 — driver_profile     (per kernel module, e.g. nct6775)
Layer 2 — chip_profile       (per `name` value, e.g. nct6798)
Layer 3 — board_profile      (per DMI fingerprint, e.g. ASUS ROG STRIX X670E)
Layer 4 — calibration_result (per channel, populated at runtime)
```

A board profile MAY override any field from chip or driver. A chip profile MAY override driver defaults. The matcher resolves to a single effective `EffectiveControllerProfile` that PR 2a returns and PR 2b consumes.

**Concrete example.** Driver `nct6775` says `pwm_unit: duty_0_255`, `off_behaviour: stops`. Chip `nct6798` inherits both unchanged. Board `ASUS ROG STRIX X670E` adds `cputin_floats: true` (ASUS quirk from §2.1) and overrides nothing else. Calibration probe later sets `pwm_polarity: normal` per channel based on empirical RPM response.

### 1.2 Field nullability — "required to exist, may be null pre-calibration"

Per Phoenix's (b) directive, fields are **required in the schema document** but some have legitimate `null` values at static-catalog time:

| Field | Static catalog | After calibration |
|---|---|---|
| `pwm_polarity` | `null` (unknown until probed) | `normal` or `inverted` |
| `min_responsive_pwm` | `null` | integer 0-255 |
| `stall_pwm` | `null` | integer 0-255 |
| `phantom_channel` | `null` | bool |

`null` is meaningful: "schema knows this field exists but the catalog can't predict it." Distinct from "field absent" which is a schema violation.

---

## 2. Driver profile — the new top-level entity

```yaml
schema_version: "1.0"

driver_profiles:
  - module: "nct6775"
    family: "nuvoton-superio"
    description: "Nuvoton NCT6775+ family Super I/O — modern AMD/Intel desktops"
    capability: "rw_full"            # see §3
    pwm_unit: "duty_0_255"           # see §4
    pwm_enable_modes:                # see §5
      "0": "full_speed"
      "1": "manual"
      "2": "thermal_cruise"
      "3": "fan_speed_cruise"
      "4": "smart_fan_iii"           # NCT6775F-only — chip override
      "5": "smart_fan_iv"
    off_behaviour: "stops"           # see §6
    polling_latency_ms_hint: 50      # see §10
    recommended_alternative_driver: null   # see §7
    conflicts_with_userspace: []     # see §8
    fan_control_capable: true        # see §9
    required_modprobe_args: []       # see §11
    pwm_polarity_reservation: "static_normal"   # see §12
    exit_behaviour: "force_max"                  # see §12.1
    runtime_conflict_detection_supported: true   # see §12.2
    firmware_curve_offload_capable: true         # see §12.3 (derived from pwm_enable_modes)
    citations:
      - "https://docs.kernel.org/hwmon/nct6775.html"
```

**`module`** is the *kernel module name* as it appears in `/sys/module/`. It is NOT the `name` value. Per map §1.2, those are different things and have caused confusion. This field is the matcher's first-tier key.

**`family`** is informational, used for grouping in diagnostic output and UX strings.

---

## 3. `capability` field (gap 1 — `pwm_control` semantics ambiguous)

Per map §1.1 and §9.1: same chip can have 3 different `name` values with different controllability. The current `pwm_control: <string>` cannot encode this.

**Enum:**

| Value | Meaning |
|---|---|
| `rw_full` | Driver accepts manual mode + duty writes. Standard control path works. |
| `rw_quirk` | Driver accepts writes but board-specific quirks may break control. PR 2b probe must verify. |
| `rw_step` | Driver accepts writes but PWM unit is `step_0_N` not `duty_0_255`. (This is partly captured by `pwm_unit` but `capability` flags the calibration code to take a different path.) |
| `rw_proc` | Driver writes via `/proc/acpi/...` not hwmon `pwm`. ThinkPad case. |
| `ro_sensor_only` | Driver loads, exposes sensors, no `pwm` attributes. ventd reads temps from this driver but does not control. |
| `ro_design` | Driver design refuses writes (kernel returns EPERM/EINVAL). Mainline `nct6683`, `surface_fan`, `hp_wmi`. |
| `ro_pending_oot` | Read-only mainline; OOT fork available. `recommended_alternative_driver` MUST be set. |
| `requires_userspace_ec` | Kernel does not expose fan control. Userspace EC tool needed. NBFC, fw-ectool case. |

The matcher uses `capability` to decide install action: `rw_*` → calibrate and apply curves, `ro_pending_oot` → install in monitor mode + surface DKMS recommendation, `ro_design` → install in monitor mode silently, `requires_userspace_ec` → install in monitor mode + surface userspace tool recommendation, `ro_sensor_only` → register as temperature source only.

**Why `rw_quirk` exists separately from `rw_full`:** the calibration probe needs to know it should be more skeptical of "writes succeed but RPM doesn't change" outcomes on `rw_quirk` drivers. Examples: it87 on Gigabyte boards with separate fan-control chip (map §2.3), nct6687 on MSI boards needing `msi_alt1`.

---

## 4. `pwm_unit` field (gap 2 — no PWM unit semantics)

Per map §1.3: drivers report through standard sysfs `pwm` attribute but values mean different things.

**Enum:**

| Value | Meaning | Calibration treatment |
|---|---|---|
| `duty_0_255` | Standard PWM duty cycle. Linear 0=off, 255=100%. | Continuous binary search for thresholds. |
| `step_0_N` | Fan-state index. Field `pwm_unit_max` declares N. Writing intermediate values rounds down to nearest step. | Discrete-state probe: write each integer 0..N, observe RPM, build state table. |
| `thinkpad_level` | `/proc/acpi/ibm/fan` levels 0-7 + named states (`auto`, `disengaged`, `full-speed`). | Discrete-level probe; named states never used by curves. |
| `percentage_0_100` | NVML / NBFC translation layer. Linear. | Same as `duty_0_255` but range 0-100. |
| `cooling_level` | pwm-fan with `cooling-levels` DT property. Discrete states from devicetree. | `pwm_unit_max` declares level count. Discrete-state probe. |

**Companion field `pwm_unit_max`:** integer, only meaningful when unit is `step_0_N`, `cooling_level`, or `thinkpad_level` (where it's always 7). For `duty_0_255` and `percentage_0_100` the max is implicit and `pwm_unit_max: null`.

**Schema enforcement:** `pwm_unit_max` MUST be set when `pwm_unit ∈ {step_0_N, cooling_level}`. PR 2a matcher refuses to match a profile that violates this.

---

## 5. `pwm_enable_modes` field (gap 4 — supported pwm_enable mode set)

Per map §1.5 and §9.4. Currently implicit. ventd must declare which `pwm_enable` integers a driver accepts and what each means.

**Shape:** map of integer-string keys to mode-name string values.

```yaml
pwm_enable_modes:
  "0": "full_speed"
  "1": "manual"
  "2": "thermal_cruise"
  "5": "smart_fan_iv"
```

**Required mode names (enum):**

| Mode name | Meaning |
|---|---|
| `manual` | ventd-controlled. **MUST exist** for `capability: rw_full` or `rw_quirk`. PR 2a refuses to match if absent. |
| `full_speed` | Fan forced to max. Used as safety-recovery target. |
| `bios_auto` | BIOS-defined automatic curve. Not chip-internal trip points. |
| `thermal_cruise` | Chip-internal thermal trip points (nct6775 mode 2). |
| `fan_speed_cruise` | Chip-internal RPM target (nct6775 mode 3). |
| `smart_fan_iii` | NCT6775F-only legacy auto. |
| `smart_fan_iv` | nct6775 mode 5 — chip-internal piecewise curve. Used by P4-HWCURVE offload. |
| `auto_trip_points` | f71882fg mode 2, it87 legacy auto. |
| `thermostat` | f71882fg F8000 mode 3. |
| `ec_auto` | gpd-fan mode 2. EC firmware curve. |
| `disengaged` | thinkpad_acpi only. **Dangerous, never used by ventd.** |

**Per-channel override.** Some chips have channels with restricted mode sets (f71882fg F8000 channel 3 always mode 2 — map §2.4). Channel-level override lives in the chip profile, not driver:

```yaml
chip_profiles:
  - name: "f8000"
    inherits_driver: "f71882fg"
    channel_overrides:
      "3":
        pwm_enable_modes_locked_to: "2"
```

---

## 6. `off_behaviour` field (gap 3 — off-PWM behaviour varies)

Per map §1.4. What happens when ventd writes `pwm=0` while in `manual` mode.

**Enum:**

| Value | Meaning |
|---|---|
| `stops` | Fan stops. Standard `nct6775`/`it87` manual-mode behaviour. |
| `falls_to_min` | Fan falls to `pwmN_floor` or chip-stored minimum. |
| `falls_to_auto_silently` | Driver silently reverts to BIOS auto. `ideapad-laptop` case. |
| `bios_dependent` | Behaviour depends on BIOS setting; calibration probe must observe. |
| `forces_max` | Driver forces 255 as safety default before manual takes effect. `gpd-fan` case — must immediately follow with desired value. |
| `state_off` | Step-based driver: state 0 may or may not be physical-off. `dell-smm-hwmon` case — calibration probe must verify. |

**Calibration probe consequence.** When ventd writes 0 and observes RPM > 0, it disambiguates using this field:
- `stops` + RPM > 0 → fan stall failed OR physical fan won't stop below some duty (real, common). Probe fills `min_responsive_pwm`.
- `falls_to_*` + RPM > 0 → expected, not a probe failure. Probe records the floor RPM separately.
- `bios_dependent` → probe records what was observed; ventd treats as `falls_to_min` for control purposes.

---

## 7. `recommended_alternative_driver` field (gap 5 — OOT recommendation)

Per map §1.1 and §9.5. When mainline driver is `ro_pending_oot`, schema points to the fix.

```yaml
recommended_alternative_driver:
  module: "nct6687d"                        # what to install
  source: "github.com/Fred78290/nct6687d"
  install_method: "dkms"                    # enum: dkms | manual_compile | distro_package
  package_hint: "nct6687d-dkms"             # AUR/PPA package name where it exists; null otherwise
  reason: "Mainline nct6683 is read-only by Nuvoton design. nct6687d adds RW via reverse-engineered Windows register layout."
  applies_to_boards: ["msi", "asrock", "intel-dh", "amd-bc-250"]   # vendor-keyed hint for matcher; not exhaustive
  module_args_hint: ["fan_config=msi_alt1"]  # only if needed for specific boards; usually empty
```

**Field is required to exist on every driver profile.** For drivers with no OOT alternative needed, it's `null`. For `ro_pending_oot` drivers it MUST be non-null (PR 2a matcher refuses to match a profile that violates this).

**Why `applies_to_boards` is a list of vendor keys, not specific boards:** specific boards live in `board_profiles` and can override entirely. The vendor-key hint is for the diagnostic bundle's "you probably want X" recommendation when no board profile matches.

---

## 8. `conflicts_with_userspace` field (gap 6 — userspace daemon conflicts)

Per map §1, §3.6, §3.10, §9.6. Some drivers conflict with running userspace daemons.

```yaml
conflicts_with_userspace:
  - daemon: "jupiter-fan-control"
    detection: "systemctl is-active jupiter-fan-control"
    resolution: "stop_and_disable"          # enum: stop_and_disable | coexist_warning | refuse_install
    reason: "Concurrent writers to steamdeck-hwmon pwm cause oscillation."
  - daemon: "fancontrol"
    detection: "pgrep -fa fancontrol"
    resolution: "stop_and_disable"
    reason: "lm-sensors fancontrol writes to same pwm sysfs paths."
```

**Resolution enum:**

| Value | UX |
|---|---|
| `stop_and_disable` | Installer prompts: "X is running and conflicts. Stop and disable it now?" Default yes. |
| `coexist_warning` | Installer warns but proceeds. Used when user explicitly chose ventd-monitor-only mode. |
| `refuse_install` | Installer refuses to proceed until user resolves. Used when conflict would damage hardware. |

**Where this field lives:** primarily on `driver_profile` (most conflicts are driver-keyed). Board profiles MAY add board-specific entries (e.g. an OEM utility specific to one vendor's hardware). PR 2a merges driver + chip + board lists into a single resolution set.

---

## 9. `fan_control_capable` and `fan_control_via` (gap 7 — fan tach without PWM)

Per map §1, §3.5, §3.8, §9.7.

```yaml
fan_control_capable: false
fan_control_via: "nbfc-linux"             # null if no known userspace path
```

**Boolean is required.** When `false`, ventd installs in monitor-only mode for this driver. Diagnostic bundle surfaces `fan_control_via` recommendation.

**Distinction from `capability: requires_userspace_ec`:** `fan_control_capable: false` means "this *kernel driver* doesn't control fans" — but a different code path (userspace EC, separate Super I/O chip) might. The `fan_control_via` field points there.

Example combinations:
- `asus_ec_sensors`: `fan_control_capable: false`, `fan_control_via: "nct6798"` (the board's actual Super I/O — map §3.5)
- `surface_fan`: `fan_control_capable: false`, `fan_control_via: null` (no path exists)
- `hp_wmi`: `fan_control_capable: false`, `fan_control_via: "nbfc-linux"`

---

## 10. `polling_latency_ms_hint` field (gap 8 — SMM/WMI latency)

Per map §3.1 and §9.8.

```yaml
polling_latency_ms_hint: 500              # integer milliseconds
```

**Semantics.** Hint to ventd's main loop: this driver's read/write operations may take this long. Used to:
- Set per-driver minimum polling interval (avoid SMM-induced audio dropouts on Dell).
- Adjust calibration timeouts.
- Inform the predictive thermal model (spec-05) about sample cadence floor.

**Default values from the map:**
- `nct6775`/`it87`/`nct6687`: 50ms
- `dell-smm-hwmon`: 500ms (SMM is slow + audio-disrupting)
- `hp-wmi-sensors`: 200ms (WMI overhead)
- `thinkpad_acpi` (`/proc/acpi`): 100ms
- `pwm-fan` (PWM hardware): 20ms
- `corsair-core` (USB HID): 50ms (handled by HID backend, but profile records hint)

**Required field.** Catalog must declare a value. Conservative default is 100ms when no driver-specific data exists.

---

## 11. `required_modprobe_args` field (gap 9 — board firmware quirks)

Per map §1.1, §2.3, §9.9.

```yaml
required_modprobe_args:
  - arg: "force_id=0x8689"
    reason: "Newer ITE chip ID not auto-detected pre-kernel-6.11."
    risk: "low"
  - arg: "ignore_resource_conflict=1"
    reason: "BIOS claims IO ports via ACPI; driver refuses load otherwise."
    risk: "medium"
    risk_detail: "Documented race conditions, worst case unexpected reboots."
```

**Risk enum:** `low` | `medium` | `high`. Surfaced in installer UX. `high` requires user confirmation before ventd writes a modprobe.d file.

**Where this lives:** primarily on `board_profile` (modprobe args are usually board-specific, not driver-wide). Driver profile MAY have a default empty list. PR 2a merges board + driver lists with board precedence.

**Diagnostic bundle inference (map §10.4):** PR 2c detects modprobe-args-needed patterns from dmesg and surfaces the recommendation even when no board profile matches.

---

## 12. `pwm_polarity` reservation (gap 10 — PWM polarity awareness)

Per map §2.3 (it87 polarity inversion) and §9.10.

```yaml
# In driver_profile or chip_profile (static):
pwm_polarity_reservation: "static_normal"     # enum: see below

# In calibration_result (per channel, runtime):
pwm_polarity: "normal"                         # enum: normal | inverted | unknown
```

**`pwm_polarity_reservation` enum (static, declares what calibration should expect):**

| Value | Meaning |
|---|---|
| `static_normal` | Driver guarantees normal polarity (low PWM = low RPM). No probe needed. |
| `static_inverted` | Driver guarantees inverted polarity. (Unknown if any real driver has this; reserved for completeness.) |
| `probe_required` | Calibration MUST empirically verify polarity per channel. |
| `not_applicable` | PWM unit is not duty-based (`step_0_N`, `thinkpad_level`); polarity is meaningless. |

**Driver defaults:**
- `nct6775` family: `static_normal`
- `it87`: `probe_required` (some BIOSes misconfigure → polarity inversion)
- `nct6687`/`nct6686`: `probe_required` (OOT, less battle-tested)
- `dell-smm-hwmon`: `not_applicable` (step-based)
- `thinkpad_acpi`: `not_applicable` (level-based)
- `gpd-fan`, `steamdeck-hwmon`, `pwm-fan`: `static_normal`

**Calibration consequence (PR 2b).** When `pwm_polarity_reservation: probe_required`, the probe writes a low value (e.g. 64), reads RPM, writes a high value (e.g. 192), reads RPM. If RPM(192) < RPM(64), polarity is inverted → calibration result records `pwm_polarity: inverted` and ventd's apply path inverts duty values for this channel forever after.

**Safety.** If polarity probe is inconclusive (RPMs too close to differentiate, fan didn't respond), `pwm_polarity: unknown` is recorded and the channel is marked `phantom_channel: true`.

---

### 12.1 `exit_behaviour` field — daemon-shutdown safety

Per fan2go's `controlMode` exit behaviour. When ventd shuts down (SIGTERM, service stop, crash, package upgrade), each channel transitions to a defined state. Without this declaration, behaviour is implementation-defined and may leave fans stuck at a low value while the system is under thermal load.

**Enum:**

| Value | Meaning | Recommended for |
|---|---|---|
| `force_max` | Write `pwm_enable=1, pwm=255` (or driver-equivalent max) on shutdown. Fan runs full speed until next ventd start or BIOS reclaim. **Safest default for desktop Super I/O.** | `nct6775`, `it87`, `nct6687`, `nct6686`, `f71882fg`, `w83627ehf`, add-in fan controllers |
| `restore_auto` | Switch driver to its auto mode (`pwm_enable=2/5` or driver-specific). Lets BIOS or chip-internal curve resume control. **Required for laptop drivers** where the laptop firmware will resume thermal management on its own. | `dell-smm-hwmon`, `gpd-fan`, `ideapad-laptop`, `yogafan`, `thinkpad_acpi`, `steamdeck-hwmon`, `pwm-fan` (if `cooling-levels` DT is present) |
| `preserve` | Leave the channel at its current `pwm` value. Fan continues at last-set speed. **Used when the hardware will not auto-resume** and `force_max` would be too aggressive (e.g. silent passive cooling scenarios in HTPC builds). | `pwm-fan` without thermal zone management; user-opt-in only |
| `bios_dependent` | Same as `preserve` but flagged in diagnostics — actual behaviour depends on BIOS. Calibration probe records observed behaviour on first run. | `f71882fg` RPM mode, edge cases |

**Default values for catalog seed:**
- Desktop Super I/O: `force_max`
- Laptop EC drivers: `restore_auto`
- Devicetree pwm-fan: `restore_auto` if thermal zone present, else `preserve`
- AIO/HID backends (handled separately): `restore_auto` (let pump firmware take over)

**Required field.** PR 2a matcher refuses to load profile with missing/unknown enum value (RULE-HWDB-PR2-13).

**Failure mode protection:** if ventd crashes hard (SIGKILL, kernel panic), no graceful exit runs. The watchdog daemon (post-v1.0) handles this case. For PR 2 scope, `exit_behaviour` only governs graceful shutdown.

### 12.2 `runtime_conflict_detection_supported` field — runtime-writer detection capability

Per fan2go's `sanityCheck.pwmValueChangedByThirdParty`. After ventd writes `pwm=N`, a competing daemon or BIOS may overwrite the value before ventd's next read cycle. Detecting this requires the driver to faithfully report what was *actually* written rather than echoing back ventd's last write.

**Boolean:**
- `true` — Driver's `pwm` read returns the actual current PWM register state. Runtime detection viable: compare last-written-value vs current-read-value; mismatch = third-party writer.
- `false` — Driver caches or echoes ventd's last write rather than reading hardware state. Runtime detection unreliable on this driver.

**Driver defaults from research:**
- `nct6775`, `it87`, `nct6687`, `nct6686`, `pwm-fan`, `steamdeck-hwmon`, `gpd-fan`: `true`
- `dell-smm-hwmon`: `true` but read latency is 200-500ms — practical sample rate is low
- `thinkpad_acpi` (`/proc/acpi/ibm/fan`): `true` (level-based, reports actual EC state)
- `cros_ec_lpcs` (Framework): `true`
- AIO HID backends (Corsair Core etc.): `true` (status messages report actual fan state)
- `legion_hwmon`: `true` for `pwm{N}` directly, `false` for `pwm{N}_auto_point{M}_pwm` (those are write-only fan curve trip points; reads return last write)

**PR 2 scope:** the field is recorded in the schema and exposed in `EffectiveControllerProfile`, but **runtime detection itself is not implemented in PR 2.** It lands in v0.7+ as a post-spec-03 feature. The schema-frozen-now decision means later code can consume the field without migration.

**Required field.** PR 2a matcher refuses to load profile with missing value (RULE-HWDB-PR2-14).

### 12.3 `firmware_curve_offload_capable` derived field — spec-05 P4-HWCURVE consumer

Per LenovoLegionLinux (`pwm{1,2}_auto_point{1-10}_pwm`) and nct6775 Smart Fan IV (`pwm{N}_auto_point{1-7}_pwm/_temp`). Some drivers expose chip-internal trip-point tables that the firmware applies autonomously when ventd is suspended (sleep, package upgrade, watchdog timeout). spec-05's predictive thermal layer wants to write a worst-case fallback curve to these tables so suspended ventd doesn't mean thermal runaway.

**Boolean derived field.** The matcher computes this at load time:

```
firmware_curve_offload_capable = (
    pwm_enable_modes contains any of:
        thermal_cruise        # nct6775 mode 2
        smart_fan_iv          # nct6775 mode 5
        auto_trip_points      # f71882fg mode 2, it87 legacy auto
        firmware_curve        # legion_hwmon, future driver convention
)
```

The catalog YAML *may* override this when the derived value is wrong (e.g. driver supports the mode but trip-point tables aren't writable on a specific board). Override field: `firmware_curve_offload_override: bool | null`. `null` = use derived value.

**Driver defaults:**
- `nct6775`: `true` (Smart Fan IV)
- `it87`: `true` only on legacy chips with auto_trip_points; check chip profile
- `f71882fg`: `true`
- `legion_hwmon`: `true`
- `dell-smm-hwmon`, `thinkpad_acpi`, `gpd-fan`, `steamdeck-hwmon`: `false`
- `nct6687`/`nct6686` (OOT MSI/ASRock): unknown — calibration probe verifies; default `false`
- `pwm-fan` with `cooling-levels` DT: `true` (devicetree describes the curve)

**PR 2 scope:** field is computed and exposed; **not consumed by PR 2 itself.** spec-05 P4-HWCURVE phase reads this from `EffectiveControllerProfile` and dispatches to a hardware-curve-write path.

**Required field (the override one is optional):** PR 2a matcher refuses to load if `pwm_enable_modes` is missing (which already triggers RULE-HWDB-PR2-05) — derivation is automatic.

---

## 13. Chip profile shape (Layer 2)

```yaml
chip_profiles:
  - name: "nct6798"                          # `/sys/class/hwmon/*/name` value
    inherits_driver: "nct6775"
    description: "Nuvoton NCT6798D — common on AMD X670/B650 + Intel Z690/Z790"
    overrides: {}                            # any driver_profile field can be overridden here
    channel_overrides: {}                    # per-channel restrictions, see §5
    citations:
      - "https://docs.kernel.org/hwmon/nct6775.html#nct6798"
```

**`inherits_driver` is required.** Resolves the layer-1 driver_profile this chip inherits from. PR 2a matcher refuses unknown driver references.

**`overrides` is a partial driver_profile.** Any field in §2-§12 may appear here and replaces the driver default. Unspecified fields fall through to driver.

---

## 14. Board profile shape (Layer 3)

```yaml
board_profiles:
  - id: "asus-rog-strix-x670e-e-gaming"
    dmi_fingerprint:
      board_vendor: "ASUSTeK COMPUTER INC."
      board_name: "ROG STRIX X670E-E GAMING WIFI"
      bios_version_min: "0805"               # null = any
      bios_version_max: null
    primary_controller:
      chip: "nct6798"
      sysfs_hint: "regex over /sys/class/hwmon/*/name"
    additional_controllers: []               # for boards with separate AIO pump controller, etc.
    overrides:
      cputin_floats: true                    # ASUS CPUTIN-floats quirk, map §2.1
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "ASUS ROG/Prime CPUTIN sensor returns floating values; use PECI 0/TSI 0 instead."
    citations: []
```

**`cputin_floats`** is a new boolean field (lives in `overrides` or directly on board profile) flagging the ASUS CPUTIN issue. PR 2a's resolution path filters `temp1` from this driver when this flag is true and registers PECI/TSI as primary CPU temp sources instead.

**`additional_controllers`** handles the multi-driver case from §9.1 (Super I/O for fans + adt7475 for AIO pump tach + amdgpu for GPU). Each entry is a controller-binding to a chip_profile.

---

## 15. Calibration result shape (Layer 4 — runtime)

Lives on disk in `/var/lib/ventd/calibration/<channel-key>.yaml`. Written by PR 2b probe, read by ventd apply path on every start.

```yaml
calibration_version: "1.0"
channel_key: "nct6798:pwm2"                  # stable identifier
calibrated_at: "2026-04-26T14:32:11Z"
firmware_version: "ASUS 0805"                # captured at calibration time

pwm_polarity: "normal"                        # populated from probe
min_responsive_pwm: 38                        # populated from probe
max_responsive_pwm: 255                       # populated from probe
stall_pwm: 25                                 # populated from probe
phantom_channel: false                        # populated from probe
bios_overridden: false                        # populated from probe — writes succeeded but reverted

probe_method: "binary_search_with_polarity_verify"
probe_duration_ms: 4318
probe_observations: 27                        # how many (pwm, rpm) pairs were sampled
```

**`bios_overridden`** is the critical field for diag bundle: it captures the "writes accept but BIOS reverts" pattern (Gigabyte it8689 case from map §2.3). When true, ventd refuses to apply curves to this channel and the diagnostic bundle surfaces the issue.

**`firmware_version` capture.** Calibration is firmware-specific. BIOS update invalidates calibration. ventd's startup compares current DMI BIOS version to recorded value; mismatch → recalibration triggered automatically.

---

## 16. `EffectiveControllerProfile` — what PR 2a returns

Internal struct, not on-disk schema. PR 2a's matcher resolves layers 1-3 (and overlays layer 4 if available) into one flattened struct that PR 2b and the apply path consume.

```go
// Pseudocode
type EffectiveControllerProfile struct {
    Module                       string
    Family                       string
    Capability                   Capability
    PWMUnit                      PWMUnit
    PWMUnitMax                   *int
    PWMEnableModes               map[int]ModeName
    OffBehaviour                 OffBehaviour
    PollingLatencyHint           time.Duration
    RecommendedAlternativeDriver *AlternativeDriver
    ConflictsWithUserspace       []UserspaceConflict
    FanControlCapable            bool
    FanControlVia                *string
    RequiredModprobeArgs         []ModprobeArg
    PWMPolarityReservation       PolarityReservation
    ExitBehaviour                ExitBehaviour
    RuntimeConflictDetectionSupported bool
    FirmwareCurveOffloadCapable  bool

    // From chip profile
    ChipName                     string
    ChannelOverrides             map[int]ChannelOverride

    // From board profile
    BoardID                      *string
    BoardOverrides               map[string]any
    CPUTINFloats                 bool
    AdditionalControllers        []ControllerBinding

    // From calibration (per channel; map keyed by channel index)
    CalibrationByChannel         map[int]CalibrationResult
}
```

PR 2a returns this struct. PR 2b probes against it and writes back calibration results. Apply path reads the struct + cal results and dispatches `pwm_unit`-aware writes.

---

## 17. Migration path from PR 1

PR 1 schema (per Phoenix's repo state):

```yaml
hardware:
  fan_count: 6
  pwm_control: "nct6798d"
```

PR 2 schema in this amendment:

```yaml
schema_version: "1.0"
driver_profiles: [...]
chip_profiles: [...]
board_profiles:
  - id: "..."
    dmi_fingerprint: {...}
    primary_controller:
      chip: "nct6798"
    # ... rest
```

**Migration rule:** PR 1's `pwm_control: <string>` is interpreted by the PR 2a matcher as `primary_controller.chip: <string>` if the string matches a `chip_profile.name`. If it matches a `driver_profile.module` instead, matcher logs a warning and synthesises an anonymous chip_profile with no overrides.

**Subtest binding:** RULE-HWDB-PR2-MIGRATE-01 covers the PR 1 → PR 2 fallback path. PR 2a includes this binding.

---

## 18. Invariant bindings (RULE-HWDB-* extensions for PR 2)

The following new rules MUST be added to `.claude/rules/` as part of PR 2a, with 1:1 subtest mapping:

| Rule ID | Statement |
|---|---|
| `RULE-HWDB-PR2-01` | Every `driver_profile` MUST declare all fields in §2-§12. Missing field = matcher refuses to load profile DB. |
| `RULE-HWDB-PR2-02` | `chip_profile.inherits_driver` MUST resolve to a known `driver_profile.module`. |
| `RULE-HWDB-PR2-03` | `board_profile.primary_controller.chip` MUST resolve to a known `chip_profile.name`. |
| `RULE-HWDB-PR2-04` | `pwm_unit_max` MUST be set when `pwm_unit ∈ {step_0_N, cooling_level}`. |
| `RULE-HWDB-PR2-05` | `pwm_enable_modes` MUST contain a `manual` entry when `capability ∈ {rw_full, rw_quirk, rw_step}`. |
| `RULE-HWDB-PR2-06` | `recommended_alternative_driver` MUST be non-null when `capability == ro_pending_oot`. |
| `RULE-HWDB-PR2-07` | `fan_control_capable: false` profiles MUST install in monitor-only mode (no calibration probe runs). |
| `RULE-HWDB-PR2-08` | Calibration result `bios_overridden: true` MUST cause apply path to refuse curve writes for that channel. |
| `RULE-HWDB-PR2-09` | DMI BIOS version mismatch between calibration record and current firmware MUST trigger recalibration. |
| `RULE-HWDB-PR2-10` | Layer precedence (board > chip > driver, calibration > all for runtime fields) MUST be enforced by the resolver. Invariant-test asserts on synthetic 3-layer fixture. |
| `RULE-HWDB-PR2-11` | PR 1 → PR 2 migration: a PR 1 `pwm_control: <string>` MUST resolve via the chip-name fallback path with a logged warning if the string doesn't match a chip profile. |
| `RULE-HWDB-PR2-12` | The matcher MUST refuse to match a profile that violates any of RULE-HWDB-PR2-01..05. Test fixture: invalid profile, expect refusal + diagnostic. |
| `RULE-HWDB-PR2-13` | Every `driver_profile` MUST declare `exit_behaviour` from the §12.1 enum. Missing/unknown value = matcher refuses to load. Apply path MUST execute the declared behaviour on graceful shutdown (SIGTERM, service-stop). |
| `RULE-HWDB-PR2-14` | Every `driver_profile` MUST declare `runtime_conflict_detection_supported` boolean. Field is consumed by post-PR-2 sanity-check feature; PR 2 itself only validates presence. |

Each rule maps 1:1 to a Go subtest in `internal/hwdb/profile_v1_test.go` (or sibling files). `tools/rulelint` enforces the binding.

---

## 19. Out-of-scope for this amendment

- **GPU vendor catalog.** Per map §8 and §12.3 — separate spec or amendment, not this one. NVML and AMDGPU sysfs have different shapes.
- **Per-chip catalog values.** This amendment defines the schema; the values live in `2026-04-hwmon-generic-catalog.md` (Phase 2 #3) and seed PR 2a's catalog file.
- **NBFC config DB integration.** spec-09 territory. This amendment only reserves `requires_userspace_ec_driver` flag space.
- **AIO/HID backends.** Map §6 confirms these have their own backends; not in this schema.
- **Predictive thermal interaction.** spec-05 amendment territory. This amendment ensures the `polling_latency_ms_hint` field exists for spec-05 to consume.

---

## 20. Failure modes enumerated

1. **Catalog seed misses a `name` value the user has.** Matcher returns `match=none`, ventd installs in safe-defaults mode, diag bundle surfaces unrecognised name. Recovery: contributor PRs the chip profile.

2. **Catalog declares `capability: rw_full` but driver actually `rw_quirk` on user's board.** Calibration probe detects (writes succeed, RPM doesn't change), records `bios_overridden: true`, apply path refuses curve writes. Recovery: board profile added with `capability: rw_quirk` override.

3. **Calibration result captured against firmware A, user updates BIOS to firmware B.** RULE-HWDB-PR2-09: ventd detects DMI mismatch, triggers recalibration on next start. No silent miscalibration.

4. **OOT driver recommendation points to repo that's been deleted/moved.** Diagnostic surfaces broken link. Recovery: catalog update PR. Mitigation: `recommended_alternative_driver.source` is informational only; `package_hint` is the authoritative install path where present.

5. **Polarity probe inconclusive.** `pwm_polarity: unknown` + `phantom_channel: true`. Channel registered as monitor-only. Recovery: user runs `ventd calibrate --force-channel N` after manually verifying fan responds to PWM in BIOS.

6. **User has a board with an `nct6798` chip that needs `msi_alt1`.** Catalog has driver-default but no board profile. Diagnostic surfaces "chip detected, MSI vendor in DMI, recommend trying `fan_config=msi_alt1`." Recovery: contributor PRs the board profile.

7. **Schema field added in v1.0 turns out to need additional sub-field in v1.1.** Per Phoenix directive (b): "if we miss something we fix it." Practical handling: `schema_version: "1.0" → "1.1"` is a *minor* bump; PR 2a parses both. Migration is in-place. Avoid v2 unless field semantics change incompatibly.

---

## 21. Success criteria

This amendment is correct if:

1. ☐ All 10 gaps from map §9 have explicit schema fields with declared semantics.
2. ☐ Layer precedence is unambiguous (driver < chip < board < calibration).
3. ☐ Every field is required-to-exist; nullable fields have explicit `null` semantics.
4. ☐ PR 2a matcher can be implemented from this schema without further design clarification.
5. ☐ PR 2b calibration probe knows what to write and where to record results.
6. ☐ PR 2c diagnostic bundle has a structured shape to dump (the resolved EffectiveControllerProfile + raw layers + calibration results).
7. ☐ spec-09 NBFC integration has a place to plug in (`requires_userspace_ec_driver` flag space).
8. ☐ spec-05 predictive thermal has the cadence hint it needs (`polling_latency_ms_hint`).
9. ☐ PR 1 schema migrates without data loss (RULE-HWDB-PR2-11).
10. ☐ All twelve RULE-HWDB-PR2-* invariants are bound to subtests.
11. ☐ `exit_behaviour` declared on every driver_profile; apply path executes it on graceful shutdown (RULE-HWDB-PR2-13).
12. ☐ `runtime_conflict_detection_supported` declared on every driver_profile, exposed in EffectiveControllerProfile (RULE-HWDB-PR2-14). Consumer feature lands post-PR-2.
13. ☐ `firmware_curve_offload_capable` derived from `pwm_enable_modes` correctly; spec-05 P4-HWCURVE has the flag it needs.

---

**End of amendment.**
