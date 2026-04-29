# 2026-04 hwmon generic catalog — per-chip entries

**Status:** Research artifact. Produced 2026-04-26 in chat 2 of spec-03 PR 2 design.
**Purpose:** One catalog entry per `name` value from controllability map §11. Schema-aware per spec-03 amendment v1.0. Seeds PR 2a's `internal/hwdb/catalog/*.yaml` files.
**Consumed by:** PR 2a matcher (catalog read), PR 2c diagnostic bundle (vendor-recommendation lookup).
**Schema reference:** `spec-03-amendment-pwm-controllability.md` §2-§12 (driver_profile fields).

This document is structured input for catalog YAML generation. Each entry below maps 1:1 to a `chip_profile` plus its inherited `driver_profile` defaults. PR 2a's catalog seed is generated from this document.

**Scope notes:**
- Tier-A desktop Super I/O drivers: comprehensive (the bulk target).
- Tier-B laptop drivers: comprehensive (gap coverage matters).
- Tier-C add-in fan controllers: short entries (low traffic, conservative defaults sufficient).
- Tier-D ARM SBC: short entries (devicetree-driven, generic curve fine).
- Tier-E AIO/HID: NOT in this catalog — handled by spec-02 series HID backends.
- Tier-F read-only sensor sources: appendix only (catalog entries with `fan_control_capable: false`).

**Conservative default curve.** Where this document declares a `conservative_curve`, the values are designed to be **safe for any fan a user might plug in** at calibration time before per-channel `min_responsive_pwm` / `stall_pwm` is known. Curves err toward higher RPM; calibration probe later reduces noise where safe. The shape is `[(temp_C, pwm_0_255_or_unit_max)]`.

---

## A. Tier-A: Desktop Super I/O — controllable via mainline

### A.1 nct6775 family (Nuvoton — mainline)

**Driver profile:**

```yaml
driver_profile:
  module: "nct6775"
  family: "nuvoton-superio"
  description: "Nuvoton NCT6775+ family Super I/O — modern AMD/Intel desktops, ~75% of consumer market"
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"
    "1": "manual"
    "2": "thermal_cruise"
    "3": "fan_speed_cruise"
    "5": "smart_fan_iv"
  off_behaviour: "stops"
  polling_latency_ms_hint: 50
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true
  citations:
    - "https://docs.kernel.org/hwmon/nct6775.html"
```

**Chip profiles** (each inherits from `nct6775` driver, adds chip-specific overrides):

| `name` | inherits_driver | fan_count | Override notes |
|---|---|---|---|
| `nct6106` | nct6775 | 5 | Lower-end; `pwm_enable_modes` adds `"4": "smart_fan_iii"` (NCT6775F-equivalent legacy) |
| `nct6116` | nct6775 | 5 | Similar to 6106 |
| `nct6775` | nct6775 | 5 | Channel range explicitly fan1-5 in mainline docs (older quote); modern chips up to 7. NCT6775F-only adds `pwm_enable=4` Smart Fan III. |
| `nct6776` | nct6775 | 5 | No Smart Fan III |
| `nct6779` | nct6775 | 5 | NCT5532D shares prefix |
| `nct6791` | nct6775 | 6 | |
| `nct6792` | nct6775 | 6 | |
| `nct6793` | nct6775 | 6 | |
| `nct6795` | nct6775 | 6 | |
| `nct6796` | nct6775 | 6 | |
| `nct6797` | nct6775 | 6 | |
| `nct6798` | nct6775 | 7 | **Most common modern variant — AMD X670/B650, Intel Z690/Z790** |
| `nct6799` | nct6775 | 7 | NCT5585D adjacent on some boards (LHM issue #1993 — board may have multiple Nuvoton chips) |
| `nct6701` | nct6775 | 6 | Newer (ASUS Prime X870-P per LHM PR #1565). Inherits as `nct6798` until kernel adds explicit support. |

**Conservative_curve (driver default):**

```yaml
conservative_curve:
  - {temp: 30, pwm: 77}    # ~30%
  - {temp: 50, pwm: 102}   # ~40%
  - {temp: 65, pwm: 153}   # ~60%
  - {temp: 75, pwm: 204}   # ~80%
  - {temp: 85, pwm: 255}   # 100%
```

**Known quirks (board-level, not chip):**
- ASUS boards: `CPUTIN` floats — `cputin_floats: true` board override required. Use PECI 0 / TSI 0 instead.
- Some MSI boards expose `nct6798` in BIOS but actually route fan control through a separate chip (rare; capture in board profile).

### A.2 nct6687 (OOT Fred78290 — DKMS)

**Driver profile:**

```yaml
driver_profile:
  module: "nct6687d"                       # DKMS module name (registers as `nct6687`)
  family: "nuvoton-superio"
  description: "Out-of-tree Fred78290/nct6687d — RW driver for Nuvoton NCT6687-R chip on MSI/ASUS B550+ boards"
  capability: "rw_quirk"                   # board-specific quirks (msi_alt1, brute_force) often needed
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"
    "1": "manual"
    "2": "thermal_cruise"                  # behaviour varies per fan_config; needs board verify
  off_behaviour: "stops"                   # most boards; verify via probe (rw_quirk implies skepticism)
  polling_latency_ms_hint: 50
  recommended_alternative_driver: null    # this IS the alternative
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []                # `fan_config=msi_alt1` auto-detected on 36+ boards
  pwm_polarity_reservation: "probe_required"   # OOT, less battle-tested
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false   # not exposed by OOT driver
  citations:
    - "https://github.com/Fred78290/nct6687d"
```

**Chip profile:** single `name: nct6687`. Inherits driver above.

**Module parameters (board-level, not chip):**
- `fan_config=msi_alt1` — required for 36+ MSI boards; **auto-detected** by recent Fred78290 versions per upstream README.
- `manual=1` — required for accurate voltage readings on some boards.
- `msi_fan_brute_force=1` — experimental, for boards where standard PWM writes don't take effect.

**Known quirks:**
- BIOS must have "Fan Type Auto Detect = Enabled" and "Smart Fan Mode = Disabled" for PWM writes to take effect on some MSI boards. **Not modellable in schema** — surfaces as calibration `bios_overridden: true`.
- Voltage register layout differs Intel vs AMD platforms — `manual=1` module param affects only voltage reads, not fan control.

**Diagnostic recommendation** (when `name=nct6683` mainline RO is detected on an MSI board):

```yaml
recommended_alternative_driver:
  module: "nct6687d"
  source: "github.com/Fred78290/nct6687d"
  install_method: "dkms"
  package_hint: "nct6687d-dkms"            # available on AUR; varies by distro
  reason: "Mainline nct6683 is read-only by Nuvoton design. nct6687d adds RW via reverse-engineered Windows register layout (LibreHardwareMonitor)."
  applies_to_boards: ["msi", "asus", "intel-dh", "intel-db87", "amd-bc-250"]
```

### A.3 nct6686 (OOT s25g5d4 — DKMS)

**Driver profile:** as `nct6687d` above with these differences:

```yaml
driver_profile:
  module: "nct6686d"
  family: "nuvoton-superio"
  description: "Out-of-tree s25g5d4/nct6686d — fork of Fred78290 targeting newer ASRock AMD boards (A620I Lighting WiFi etc.)"
  capability: "rw_quirk"
  # ... rest identical to nct6687d
  citations:
    - "https://github.com/s25g5d4/nct6686d"
```

**Chip profile:** single `name: nct6686`. Inherits driver above.

**Diagnostic recommendation** (when `name=nct6683` mainline RO is detected on an ASRock A620 board):

```yaml
recommended_alternative_driver:
  module: "nct6686d"
  source: "github.com/s25g5d4/nct6686d"
  install_method: "dkms"
  package_hint: null                       # Not in major distro repos as of 2026-04
  reason: "nct6686d is forked from Fred78290's nct6687d; targets ASRock newer AM5 boards where Fred's driver doesn't quite match."
  applies_to_boards: ["asrock-a620", "asrock-x670e-late"]
```

### A.4 nct6683 (mainline — read-only)

**Driver profile:**

```yaml
driver_profile:
  module: "nct6683"
  family: "nuvoton-superio"
  description: "Mainline read-only driver for Nuvoton NCT6683/6686/6687 chips. RW control requires OOT fork."
  capability: "ro_pending_oot"
  pwm_unit: "duty_0_255"                   # for the few attrs it does expose
  pwm_unit_max: null
  pwm_enable_modes: {}                     # no manual mode supported
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 50
  recommended_alternative_driver:
    module: "nct6687d"                     # or nct6686d depending on board vendor
    source: "github.com/Fred78290/nct6687d"
    install_method: "dkms"
    package_hint: "nct6687d-dkms"
    reason: "Mainline nct6683 is read-only by Nuvoton design (Intel firmware NDA prevents register-write mainlining). nct6687d / nct6686d add RW via reverse-engineered Windows register layout. Choice depends on board vendor — see board profile."
    applies_to_boards: ["msi", "asus", "asrock", "intel-dh", "intel-db87", "amd-bc-250"]
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: "nct6687d-dkms or nct6686d-dkms (DKMS install)"
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "preserve"               # nothing to write; fan stays in BIOS auto
  runtime_conflict_detection_supported: false   # nothing to detect
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/hwmon/nct6683.html"
```

**Chip profile:** single `name: nct6683`. Inherits driver above.

**ventd behaviour when matched:** install in monitor-only mode. Surface OOT install recommendation. Diagnostic bundle includes "install nct6687d-dkms" hint with vendor-keyed picker.

### A.5 it87 family (ITE — mainline)

**Driver profile:**

```yaml
driver_profile:
  module: "it87"
  family: "ite-superio"
  description: "ITE IT87xx Super I/O family — common on ASRock pre-2022, some Gigabyte / MSI Intel"
  capability: "rw_quirk"                   # Gigabyte BIOS-locked case + polarity inversion possible
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"                      # NOT supported on IT8603E — chip override
    "1": "manual"
    "2": "auto_trip_points"                # Older chips only (IT8705F<rev F, IT8712F<rev G)
  off_behaviour: "stops"
  polling_latency_ms_hint: 100              # IT87xx updates every 1.5s per docs; conservative
  recommended_alternative_driver: null      # Mainline is RW-capable
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "probe_required"   # Some BIOSes misconfigure; calibration must verify
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true       # Older chips with auto trip points
  citations:
    - "https://docs.kernel.org/hwmon/it87.html"
```

**Chip profiles** (mainline coverage):

| `name` | fan_count | Override notes |
|---|---|---|
| `it8603` | 3 | **`pwm_enable_modes` removes `"0": "full_speed"`** — IT8603E does not support full-speed mode. |
| `it8612` | 3 | (rare) |
| `it8620` | 6 | Custom design; 16-bit fan mode mandatory |
| `it8628` | 6 | Custom design; 16-bit fan mode mandatory |
| `it8665` | 5 | (frankcrawford OOT extends — see A.6) |
| `it8686` | 5 | Common on Gigabyte B450/B550. **Some Gigabyte boards: PWM writes accepted but ignored** — surfaces as calibration `bios_overridden: true`. Board profiles needed. |
| `it8688` | 5 | Similar to it8686 |
| `it8689` | 5 | Newer Gigabyte X570/B650/X670 Aorus. **Often requires `force_id=0x8689 ignore_resource_conflict=1` modprobe args** until mainline kernel adds detection. |
| `it8705` | 3 | Old; FEAT_OLD_AUTOPWM (legacy auto with `pwm_enable=2`) |
| `it8712` | 3 | Old; VID inputs; legacy auto |
| `it8716` | 5 | Mid-era; 16-bit tachometers |
| `it8718` | 5 | |
| `it8720` | 5 | |
| `it8721` | 5 | (it8758 alias) |
| `it8728` | 5 | |
| `it8732` | 5 | Closed-loop fan control mode (not yet implemented in driver) |
| `it8771` | 3 | |
| `it8772` | 3 | |
| `it8781` | 3 | |
| `it8782` | 3 | |
| `it8783` | 3 | |
| `it8786` | 5 | NEWER_AUTOPWM feature flag |
| `it8790` | 3 | NEWER_AUTOPWM, 12mV ADC |
| `it8792` | 3 | NEWER_AUTOPWM, 10.9mV ADC, IT8795E shares prefix |
| `it87952` | 3 | NEWER_AUTOPWM |

**Conservative_curve:** same as nct6775 (duty_0_255, similar thermal characteristics).

**Known modprobe args** (board-level overrides):

```yaml
# Per board profile — Gigabyte B650 Aorus example
required_modprobe_args:
  - arg: "force_id=0x8689"
    reason: "Newer ITE chip ID not auto-detected in older kernels."
    risk: "low"
  - arg: "ignore_resource_conflict=1"
    reason: "BIOS claims IO ports via ACPI; driver refuses load otherwise."
    risk: "medium"
    risk_detail: "Documented race conditions with ACPI; worst case unexpected reboots."
  - arg: "fix_pwm_polarity=1"
    reason: "Some BIOSes misconfigure PWM polarity (rare). Calibration probe should detect first; only set if probe-confirmed."
    risk: "high"
    risk_detail: "Marked DANGEROUS in driver source. ventd must NOT auto-set this."
```

**Special case — Gigabyte boards with separate fan-control chip:**

Some Gigabyte boards (newer Aorus models) have a **second physical chip** that handles fan curves; the IT87 only reports tachometer. PWM writes are accepted by the driver but the actual fan does not change. **Board profile override:**

```yaml
# Per affected board profile
fan_control_capable: false
fan_control_via: "Gigabyte custom fan controller (no Linux driver — fan curve must be set in BIOS)"
notes: "frankcrawford/it87 README documents this as 'known issue, fix is known but complex.'"
```

### A.6 it87 (frankcrawford OOT — DKMS)

**Driver profile:** identical to mainline `it87` above except **chip coverage is extended**:

```yaml
driver_profile:
  module: "it87"                           # Same name; OOT installs replace mainline
  family: "ite-superio-oot"
  description: "Out-of-tree frankcrawford/it87 fork — adds support for newer ITE chips not yet in mainline"
  # ... rest identical to A.5 driver profile
  citations:
    - "https://github.com/frankcrawford/it87"
    - "https://docs.kernel.org/hwmon/it87.html"
```

**Chip profiles added beyond mainline:**

| `name` | fan_count | Notes |
|---|---|---|
| `it8606` | 3 | Newer entry-level |
| `it8607` | 3 | |
| `it8613` | 3 | |
| `it8622` | 3 | |
| `it8625` | 5 | |
| `it8528` | 5 | |
| `it8655` | 5 | |
| `it8696` | 5 | |
| `it8698` | 5 | |
| `it8528` | 5 | Embedded |

**ventd behaviour:** when calibration detects a chip name in this list AND mainline `it87` is loaded but doesn't expose `pwm`, surface "frankcrawford/it87 DKMS" install recommendation.

### A.7 f71882fg family (Fintek)

**Driver profile:**

```yaml
driver_profile:
  module: "f71882fg"
  family: "fintek-superio"
  description: "Fintek F71xxx Super I/O — older Asus/Foxconn/Jetway boards (~2009-2014), some embedded SBCs"
  capability: "rw_full"                    # Mode-1 (manual) is broadly supported
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
    "2": "auto_trip_points"
    "3": "thermostat"                      # F8000 only when in duty-cycle mode
  off_behaviour: "stops"                   # In PWM mode; in RPM mode = 0% of fanN_full_speed
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true
  citations:
    - "https://docs.kernel.org/hwmon/f71882fg.html"
```

**Chip profiles:**

| `name` | fan_count | Override notes |
|---|---|---|
| `f71808e` | 4 | |
| `f71808a` | 4 | |
| `f71858fg` | 3 | Mode-1 manual only available when fan channel is in RPM mode (BIOS-set) |
| `f71862fg` | 4 | |
| `f71869` | 4 | |
| `f71869a` | 4 | |
| `f71882fg` | 4 | |
| `f71889fg` | 4 | |
| `f71889ed` | 4 | |
| `f71889a` | 4 | |
| `f8000` | 4 | **Channel 3 always mode 2 (auto)** — channel_overrides locks `pwm_enable_modes_locked_to: "2"` for channel 3. Manual mode requires RPM-mode-via-BIOS for channels 1,2,4. |
| `f81801u` | 3 | |
| `f81865f` | 3 | |

### A.8 w83627ehf (Winbond — legacy)

**Driver profile:**

```yaml
driver_profile:
  module: "w83627ehf"
  family: "winbond-superio"
  description: "Winbond W83627EHF/EHG/DHG/UHG/667HG — pre-2014 boards. Newer Nuvoton chips moved to nct6775 driver."
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"
    "1": "manual"
    "5": "smart_fan_iv"
  off_behaviour: "stops"
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null     # nct6775 supersedes for newer chips, but legacy chips stay here
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true
  citations:
    - "https://docs.kernel.org/hwmon/w83627ehf.html"
```

**Chip profiles:** `w83627ehf`, `w83627dhg`, `w83627uhg`, `w83667hg`, `w83667hg_b`. All inherit driver above; fan_count typically 4-5.

### A.9 Old / EOL Super I/O drivers

Single short entries each. Conservative defaults sufficient — coverage for "a contributor will eventually report one and we shouldn't ship `match=none`."

```yaml
# Pattern for each: w83627hf, smsc47m1, pc87427, vt1211, dme1737, lm63, lm85, lm93
driver_profile:
  module: "<name>"
  family: "<vendor>-superio-legacy"
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"                          # All have manual mode
  off_behaviour: "stops"
  polling_latency_ms_hint: 200             # Conservative — old chips, slow
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false    # Don't trust legacy auto modes by default
  citations:
    - "https://docs.kernel.org/hwmon/<name>.html"
```

`name` values from controllability map §11.1:
- `w83627hf`: w83627hf, w83697hf, w83781d, w83782d, w83783s
- `smsc47m1`: smsc47m1
- `pc87427`: pc87360, pc87427
- `vt1211`: vt1211
- `dme1737`: dme1737, sch5027, sch311x
- `lm63`: lm63
- `lm85`: lm85
- `lm93`: lm93

---

## B. Tier-B: Laptop platform drivers

### B.1 dell-smm-hwmon (Dell laptops + some desktops)

**Driver profile:**

```yaml
driver_profile:
  module: "dell-smm-hwmon"
  family: "dell-smm"
  description: "Dell SMM-based fan control — Latitude / XPS / Inspiron / Precision / OptiPlex on whitelisted models"
  capability: "rw_step"                    # PWM unit is fan-state index, not duty cycle
  pwm_unit: "step_0_N"
  pwm_unit_max: 2                          # Most Dell laptops; some are 0-3 — chip override
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
  off_behaviour: "state_off"               # State 0 may or may not be physical-off; calibration verifies
  polling_latency_ms_hint: 500             # SMM is slow + audio-disrupting; documented bug 201097
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "i8kmon"
      detection: "pgrep -fa i8kmon"
      resolution: "stop_and_disable"
      reason: "i8kutils' i8kmon writes to /proc/i8k; conflicts with ventd's hwmon path."
  fan_control_capable: true                # When whitelisted; chip override sets false otherwise
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "restore_auto"           # Let BIOS resume — laptop firmware is the safety net
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false    # No accessible chip-internal trip-points
  citations:
    - "https://docs.kernel.org/hwmon/dell-smm-hwmon.html"
    - "https://bugzilla.kernel.org/show_bug.cgi?id=201097"
```

**Chip profile:** single `name: dell_smm`. Inherits driver above.

**Per-board overrides important:**
- `pwm_unit_max` varies — some Dells expose 0-2 (Latitude 7280, Inspiron 7000-class), some 0-3 (Precision workstations). Calibration probe detects.
- Many models: `pwmX_enable` attribute does not appear due to whitelist gap. Board profiles for non-whitelisted models set `fan_control_capable: false` with `fan_control_via: "kernel recompile with i8k_whitelist_fan_control entry, or dellfan utility"`.
- `pwm1_enable` global vs per-fan SMM commands: heuristic-detected. Schema captures via `channel_overrides` if needed.

**Known issue:** SMM calls cause kernel-level stalls / audio dropouts on some Dell models (kernel bug 201097). ventd's `polling_latency_ms_hint: 500` mitigates but doesn't eliminate; spec-05 should treat this driver as a low-cadence input.

### B.2 hp-wmi-sensors (HP business class — read-only)

**Driver profile:**

```yaml
driver_profile:
  module: "hp-wmi-sensors"
  family: "hp-wmi"
  description: "HP WMI sensor readout for business-class HP laptops (EliteBook, ZBook, ProBook). Read-only by design."
  capability: "ro_design"
  pwm_unit: "duty_0_255"                   # Doesn't expose pwm; field is reservation
  pwm_unit_max: null
  pwm_enable_modes: {}
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 200             # WMI overhead
  recommended_alternative_driver: null    # No OOT alternative; NBFC is consumer-HP only
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: null                    # No path on business-class HP
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "preserve"
  runtime_conflict_detection_supported: false
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/hwmon/hp-wmi-sensors.html"
```

**Chip profile:** single `name: hp_wmi`. Inherits driver above.

### B.3 hp-wmi (consumer HP — typically read-only, NBFC fills gap)

**Driver profile:** as `hp-wmi-sensors` but `fan_control_via: "nbfc-linux"`.

```yaml
fan_control_capable: false
fan_control_via: "nbfc-linux (Option B integration via spec-09 unlocks fan control on consumer HP Pavilion / EliteBook / Victus)"
```

### B.4 ideapad-laptop (Lenovo IdeaPad/Yoga/Slim — pre-7.1)

**Driver profile:**

```yaml
driver_profile:
  module: "ideapad-laptop"
  family: "lenovo-ideapad"
  description: "Lenovo IdeaPad / Yoga / Slim platform driver — limited fan_mode toggle pre-kernel-7.1. Use yogafan on 7.1+."
  capability: "rw_step"                    # fan_mode 0/1/2/4 — discrete states
  pwm_unit: "step_0_N"
  pwm_unit_max: 4                          # Or 2 on older models; per-model override
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
  off_behaviour: "falls_to_auto_silently"   # write may be silently ignored on some BIOSes
  polling_latency_ms_hint: 200
  recommended_alternative_driver:
    module: "yogafan"
    source: "kernel.org (mainlined in 7.1)"
    install_method: "distro_package"
    package_hint: null
    reason: "yogafan (mainline 7.1+) replaces ideapad-laptop fan control with a richer hwmon path. Recommend kernel >= 7.1 if user can upgrade."
    applies_to_boards: ["lenovo-yoga", "lenovo-flex", "lenovo-slim", "lenovo-ideapad"]
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "kernel source drivers/platform/x86/ideapad-laptop.c"
```

**Chip profile:** various — driver may register multiple `name` values per model; commonly no hwmon at all, just `/sys/bus/platform/drivers/ideapad/` attrs. Schema treats this as a degraded `rw_step` source.

### B.5 yogafan (kernel 7.1+)

**Driver profile:**

```yaml
driver_profile:
  module: "yogafan"
  family: "lenovo-modern"
  description: "Modern Lenovo Yoga/Flex/Slim/IdeaPad/Legion-class fan control (mainline 7.1+)"
  capability: "rw_full"                    # TBD: needs verification on real hardware in chat 3 / Phoenix's flex5
  pwm_unit: "duty_0_255"                   # Most likely; chat 1 controllability map flagged as TBD-verify
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
  off_behaviour: "stops"                   # TBD-verify
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "probe_required"   # New driver, less battle-tested
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false    # TBD-verify; some Lenovo firmware exposes auto-points
  citations:
    - "https://www.phoronix.com/news/Linux-7.1-HWMON"
    - "kernel source (TBD-pin-on-merge-commit)"
```

**Chip profile:** single `name: yogafan`. Phoenix's IdeaPad Flex5 (Pentium 7505) is a confirmed test target for verifying these defaults.

**Schema note:** marked `pwm_unit: duty_0_255` and `capability: rw_full` provisionally. Calibration probe on Phoenix's flex5 will confirm/refute. If it turns out yogafan is `step_0_N`, override at chip profile level (no driver-default change needed).

### B.6 thinkpad_acpi (ThinkPads — proc/acpi interface)

**Driver profile:**

```yaml
driver_profile:
  module: "thinkpad_acpi"
  family: "lenovo-thinkpad"
  description: "ThinkPad fan control via /proc/acpi/ibm/fan. Distinct from hwmon — uses thinkpad_level PWM unit."
  capability: "rw_proc"
  pwm_unit: "thinkpad_level"
  pwm_unit_max: 7
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
    # Note: "disengaged" and "full-speed" string commands are NOT in this enum — they're never used by ventd's curves
  off_behaviour: "stops"                    # level 0 = fan off (if BIOS allows)
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "thinkfan"
      detection: "systemctl is-active thinkfan"
      resolution: "stop_and_disable"
      reason: "thinkfan and ventd both write to /proc/acpi/ibm/fan; concurrent writes cause oscillation."
    - daemon: "tpfancod"
      detection: "systemctl is-active tpfancod"
      resolution: "stop_and_disable"
      reason: "Dead-ish but some users still run it."
  fan_control_capable: true
  required_modprobe_args:
    - arg: "fan_control=1"
      reason: "Required to enable write access. Default is read-only."
      risk: "low"
    - arg: "experimental=1"
      reason: "Required on T440+ generation. Without it, write attempts return EPERM."
      risk: "low"
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "restore_auto"            # ThinkPad firmware will resume control
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/admin-guide/laptops/thinkpad-acpi.html"
    - "https://github.com/vmatare/thinkfan"
```

**Chip profile:** ThinkPads typically don't expose fan via hwmon — the catalog entry is keyed on driver presence + DMI vendor "LENOVO" + product name match. Schema captures via DMI tier-2 board matcher rather than `name` value.

**Critical safety note:** ThinkPad firmware resets fan-mode after suspend/resume on some BIOS revisions. ventd must subscribe to suspend/resume events and re-apply.

**Mode set notes:**
- "disengaged" = fan runs unregulated; can damage fan with prolonged use; **never used by ventd curves**.
- "full-speed" = level 7 force; available as safety target.

### B.7 asus-nb-wmi / asus_ec_sensors

#### asus-nb-wmi (laptop hotkeys + occasional fan)

**Driver profile:**

```yaml
driver_profile:
  module: "asus-nb-wmi"
  family: "asus-laptop"
  description: "ASUS laptop platform driver — hotkeys, keyboard backlight; fan control on some models only"
  capability: "rw_quirk"                   # Per-model verification needed
  pwm_unit: "duty_0_255"                   # When it works
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 200
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true                # Per-model; many boards override to false
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "kernel source drivers/platform/x86/asus-nb-wmi.c"
```

**Chip profiles:** model-specific. Most ASUS laptops where fan control works will appear under DMI board match rather than `name` value.

#### asus_ec_sensors (ROG/Prime/ProArt/TUF — read-only)

**Driver profile:**

```yaml
driver_profile:
  module: "asus_ec_sensors"
  family: "asus-ec"
  description: "ASUS ROG/Prime/ProArt/TUF EC sensors (~50 board allowlist). Read-only — fan control via Super I/O."
  capability: "ro_sensor_only"
  pwm_unit: "duty_0_255"                   # Reservation
  pwm_unit_max: null
  pwm_enable_modes: {}
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: "nct6798 / nct6799 (board's actual Super I/O — see board profile primary_controller)"
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "preserve"
  runtime_conflict_detection_supported: false
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/hwmon/asus_ec_sensors.html"
```

**Chip profile:** single `name: asus_ec`. Inherits driver above.

**Critical use:** strong DMI-tier-2 fingerprint signal — when `asus_ec` is present, board is in the ~50-allowlist of ROG/Prime/ProArt/TUF and ventd should look for `nct6798`/`nct6799` as the actual fan controller.

### B.8 gpd-fan (GPD handhelds)

**Driver profile:**

```yaml
driver_profile:
  module: "gpd-fan"
  family: "gpd-handheld"
  description: "GPD Win/Pocket/Max/MicroPC handheld fan control"
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"
    "1": "manual"
    "2": "ec_auto"
  off_behaviour: "forces_max"               # Driver forces pwm=255 as safety default on entering manual mode; userspace must follow up immediately
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"            # ec_auto mode = device firmware resumes
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/hwmon/gpd-fan.html"
```

**Chip profile:** single `name: gpd_fan`. Per-board quirk via module param (model-detection).

**Apply path note:** when ventd writes `pwm_enable=1` on this driver, the very next operation MUST be a write to `pwm` with the desired value — otherwise the fan runs at 100% briefly. PR 2b probe handles via "write enable, write target, verify" sequence.

### B.9 surface_fan (Microsoft Surface Pro 9 — read-only)

**Driver profile:**

```yaml
driver_profile:
  module: "surface_fan"
  family: "microsoft-surface"
  description: "Microsoft Surface Pro 9 fan tachometer. Read-only — Surface firmware controls speed."
  capability: "ro_design"
  pwm_unit: "duty_0_255"                   # Reservation
  pwm_unit_max: null
  pwm_enable_modes: {}
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: null
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "preserve"
  runtime_conflict_detection_supported: false
  firmware_curve_offload_capable: false
  citations:
    - "https://docs.kernel.org/hwmon/surface_fan.html"
```

**Chip profile:** single `name: surface_fan`.

### B.10 steamdeck-hwmon (Steam Deck LCD + OLED)

**Driver profile:**

```yaml
driver_profile:
  module: "steamdeck-hwmon"
  family: "valve-steamdeck"
  description: "Steam Deck LCD + OLED fan control. Single fan. Conflicts with jupiter-fan-control."
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"                       # SteamOS 3.2+ OS-controlled mode
  off_behaviour: "stops"
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "jupiter-fan-control"
      detection: "systemctl is-active jupiter-fan-control"
      resolution: "stop_and_disable"
      reason: "Concurrent writers to steamdeck-hwmon pwm cause oscillation."
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"            # Let SteamOS resume control on shutdown
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "kernel source drivers/hwmon/steamdeck-hwmon.c (Valve, mainline 6.x+)"
```

**Chip profile:** single `name: steamdeck_hwmon`. Single fan, single PWM channel, 0-255 duty.

**Distro context:** SteamOS ships jupiter-fan-control by default. Bazzite, HoloISO, Manjaro on Deck typically don't. Phoenix's SteamDeck OLED test target is Sephiroth (jupiter custom kernel) — must verify jupiter-fan-control behaviour on that specific build.

### B.11 cros_ec_lpcs (Framework via Chrome OS EC)

**Driver profile:**

```yaml
driver_profile:
  module: "cros_ec_lpcs"
  family: "google-cros-ec"
  description: "Chromium OS EC over LPC bus — Framework Laptop 13/16, ChromeOS devices"
  capability: "rw_full"                     # When kernel exposes hwmon attrs
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
  off_behaviour: "stops"
  polling_latency_ms_hint: 50
  recommended_alternative_driver: null     # Mainline since 5.19 (Framework 13 Intel) / 6.10 (Framework 13 AMD + 16)
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false
  citations:
    - "https://www.howett.net/posts/2021-12-framework-ec/"
    - "https://github.com/FrameworkComputer/EmbeddedController"
```

**Chip profile:** `name` varies by Framework board generation; capture in chip profile.

**Fallback:** older kernels need `fw-ectool` userspace bridge (post-v1.0 optional package).

### B.12 acer-wmi, msi-ec, framework-laptop, etc.

Single short entries — most are RO sensor sources. Coverage notes:

| Module | `name` | Capability | Notes |
|---|---|---|---|
| `acer-wmi` | varies | `ro_sensor_only` | Mostly hotkeys + sensors |
| `msi-ec` | varies (often `/sys/devices/platform/msi-ec/`) | `rw_quirk` on supported firmwares; whitelisted | DKMS for older models |
| `framework-laptop` | (no fan hwmon) | `requires_userspace_ec` (or use cros_ec_lpcs) | RGB LED + hotkey only |
| `legion_hwmon` (legion-laptop) | `legion_hwmon` | `rw_full` | Mainline since ~6.5; 10-point firmware curve via `pwm{1,2}_auto_point{1-10}_pwm`. **First-class spec-05 P4-HWCURVE target.** |

**legion_hwmon driver profile** (the special one — direct relevance to spec-05):

```yaml
driver_profile:
  module: "legion-laptop"
  family: "lenovo-legion"
  description: "Lenovo Legion family — kernel module exposes 10-point firmware fan curves via standard hwmon attrs"
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
    "5": "firmware_curve"                  # 10-point auto curve in EC
  off_behaviour: "stops"
  polling_latency_ms_hint: 100
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "legion_cli"
      detection: "pgrep -f 'legion_cli|legion_gui'"
      resolution: "coexist_warning"        # Legion GUI may run alongside ventd if user wants
      reason: "Both ventd and legion_cli/gui write to legion's hwmon attrs; coordinate or pick one."
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true     # The 10-point curve is the exact P4-HWCURVE target
  citations:
    - "https://github.com/johnfanv2/LenovoLegionLinux"
    - "https://github.com/johnfanv2/LenovoLegionLinux/blob/main/kernel_module/legion-laptop.c"
```

**Chip profile:** `name: legion_hwmon`. Inherits driver above.

**spec-05 hookup:** when ventd's predictive thermal layer is suspended (sleep, watchdog, package upgrade), it writes a worst-case fallback curve to `pwm{1,2}_auto_point{1-10}_pwm` and the EC takes over autonomously. legion-laptop is direct prior art — ventd's apply path for P4-HWCURVE on this driver is well-defined.

**Power-mode coupling caveat:** Legion EC may load default fan curve values when user presses Fn+Q (power-mode change). ventd should subscribe to `platform_profile` events and re-apply on change.

### B.13 NBFC-Linux userspace EC (consumer laptops without kernel write path)

Not a kernel driver — handled by spec-09. Catalog entry shape:

```yaml
driver_profile:
  module: "<userspace>"                    # placeholder; matcher uses NBFC config name as key
  family: "nbfc-linux"
  description: "NBFC-Linux userspace EC backend. Used when no kernel write path exists for the laptop."
  capability: "requires_userspace_ec"
  pwm_unit: "percentage_0_100"             # NBFC normalises internally
  pwm_unit_max: 100
  pwm_enable_modes:
    "1": "manual"
    "2": "bios_auto"
  off_behaviour: "bios_dependent"          # Per-config; some configs override
  polling_latency_ms_hint: 200             # EC polling
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "nbfc_service"
      detection: "systemctl is-active nbfc_service"
      resolution: "refuse_install"
      reason: "ventd's NBFC backend (spec-09) controls the EC directly; running NBFC's own service simultaneously = two writers, EC inconsistency."
  fan_control_capable: true
  required_modprobe_args:
    - arg: "ec_sys.write_support=1"
      reason: "Kernel parameter required for ec_sys-based EC writes. Some distros default to disabled."
      risk: "medium"
      risk_detail: "EC writes can brick laptops if config is wrong. spec-09 ships read-only default."
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "restore_auto"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false    # NBFC writes fan speed directly; firmware doesn't have user-visible curve
  citations:
    - "https://github.com/nbfc-linux/nbfc-linux"
    - "https://github.com/nbfc-linux/nbfc-linux/blob/main/doc/nbfc_service.json.5.md"
```

spec-09 details the per-laptop config selection.

---

## C. Tier-C: Add-in fan controllers (short entries)

All RW, all `pwm_unit: duty_0_255`, all `pwm_enable_modes: { "0": "full_speed", "1": "manual", "2": "thermal_cruise" }`, `off_behaviour: stops`. Per-driver:

| Module | `name` | Channels | Notes |
|---|---|---|---|
| `adt7475` | adt7473, adt7475, adt7476, adt7490 | 3 | Workstation/server. May overlap with IPMI on Supermicro. |
| `max31790` | max31790 | 6 | Aquacomputer Octo, chassis fan splitters. |
| `emc2305` | emc2305 | 5 | Servers, embedded (Mellanox switches, NVIDIA Jetson). |
| `amc6821` | amc6821 | 1 | Embedded boards. |
| `nct7363` | nct7363 | varies | BMC-adjacent server fan management. |
| `nct7802` | nct7802 | varies | Server. |
| `nct7904` | nct7904 | varies | Server. |
| `lm63` | lm63 | 1 | Older AMD/server. |
| `lm85` | lm85 | 4 | Older. |
| `lm93` | lm93 | varies | Older AMD server. |

**Common driver profile** (with chip-specific `pwm_enable_modes` and `fan_count` overrides):

```yaml
driver_profile:
  module: "<chip>"
  family: "addin-fan-controller"
  capability: "rw_full"
  pwm_unit: "duty_0_255"
  pwm_unit_max: null
  pwm_enable_modes:
    "0": "full_speed"
    "1": "manual"
    "2": "thermal_cruise"                  # Verify per chip; some don't support
  off_behaviour: "stops"
  polling_latency_ms_hint: 50
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "force_max"
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: false    # Most don't expose chip-internal trip-points
  citations:
    - "https://docs.kernel.org/hwmon/<chip>.html"
```

**ADT7475 special case:** `pwm_enable_modes` includes `"2": "auto_temp_mapped"` — chip can drive PWM from a configurable temp source. Mode 0 is "off" (stops fan), distinct from full_speed. Override at chip profile.

---

## D. Tier-D: ARM SBC / devicetree

### D.1 pwm-fan (RPi 5, NanoPi, OrangePi, Rockchip/Allwinner SBCs)

**Driver profile:**

```yaml
driver_profile:
  module: "pwm-fan"
  family: "devicetree-pwm"
  description: "Generic devicetree PWM-fan driver. RPi 5, NanoPi, OrangePi, NXP i.MX, Rockchip, Allwinner."
  capability: "rw_full"
  pwm_unit: "duty_0_255"                    # Default; cooling-levels DT property switches to cooling_level
  pwm_unit_max: null
  pwm_enable_modes:
    "1": "manual"
  off_behaviour: "stops"                    # PWM signal goes to 0% duty
  polling_latency_ms_hint: 20                # PWM hardware is fast
  recommended_alternative_driver: null
  conflicts_with_userspace:
    - daemon: "thermal_governor"             # Kernel thermal zone driving cooling-levels
      detection: "/sys/class/thermal/thermal_zone*/policy reads as 'step_wise' AND /sys/class/thermal/cooling_device*/type contains 'pwm-fan'"
      resolution: "stop_and_disable"
      reason: "Kernel thermal governor drives pwm-fan via cooling-levels; ventd must claim the zone or coordinate."
  fan_control_capable: true
  required_modprobe_args: []
  pwm_polarity_reservation: "static_normal"
  exit_behaviour: "restore_auto"            # Let thermal zone resume if claimed
  runtime_conflict_detection_supported: true
  firmware_curve_offload_capable: true     # When cooling-levels DT is set
  citations:
    - "https://docs.kernel.org/hwmon/pwm-fan.html"
```

**Chip profile:** `name` varies by DT (`pwmfan`, `cooling-fan@0`, etc.). Single fan typical.

**Critical:** if board's DT has `cooling-levels` property, ventd's catalog override sets `pwm_unit: cooling_level` and `pwm_unit_max: <count from DT>`. Detection: read `/sys/class/thermal/cooling_device*/cur_state` count. PR 2c diagnostic captures.

**`pulses-per-revolution` DT property** defaults to 2; some fans need 1 or 4. Wrong value = 2x or 0.5x RPM readings. ventd's calibration probe should sanity-check observed RPM range.

### D.2 raspberrypi-hwmon (RPi voltage monitor only)

```yaml
driver_profile:
  module: "raspberrypi-hwmon"
  family: "raspberrypi"
  description: "Raspberry Pi voltage / undervoltage monitor. No fan control."
  capability: "ro_sensor_only"
  fan_control_capable: false
  fan_control_via: "pwm-fan (Pi 5 fan goes via devicetree pwm-fan, not this driver)"
  # ... rest = ro_sensor_only defaults
  citations:
    - "kernel source drivers/hwmon/raspberrypi-hwmon.c"
```

### D.3 qnap-mcu-hwmon, gxp-fan-ctrl, aspeed-pwm-tacho, npcm750-pwm-fan

NAS/BMC drivers. Out of typical homelab scope but catalog entries for completeness:

| Module | `name` | Notes |
|---|---|---|
| `qnap-mcu-hwmon` | qnap_mcu | QNAP NAS — RW |
| `gxp-fan-ctrl` | gxp_fan | HP ProLiant Gen10 BMC adjacent — RW |
| `aspeed-pwm-tacho` / `aspeed-g6-pwm-tach` | aspeed_pwm | ASPEED BMC chips themselves |
| `npcm750-pwm-fan` | npcm750_pwm_fan | Nuvoton NPCM BMC chips themselves |

All `capability: rw_full`, `pwm_unit: duty_0_255`, conservative defaults.

**ventd boundary:** ASPEED + NPCM are typically *the BMC* — server hardware where ventd would run on the host side, not on the BMC. Catalog entry exists for "if anyone runs ventd directly on a BMC" but mainstream use is host-side IPMI to talk to the BMC, which is spec-01 territory.

---

## F. Tier-F: Read-only signal sources (catalog entries with `fan_control_capable: false`)

These appear as `name` values when scanning `/sys/class/hwmon/` but never expose `pwm`. ventd reads them for temperature/power input. **All inherit a single `read_only_sensor_source` driver profile:**

```yaml
driver_profile:
  module: "_sensor_source"
  family: "read-only"
  description: "Sensor source — read-only temperature/power input, no fan control"
  capability: "ro_sensor_only"
  pwm_unit: "duty_0_255"                    # Reservation
  pwm_unit_max: null
  pwm_enable_modes: {}
  off_behaviour: "bios_dependent"
  polling_latency_ms_hint: 50
  recommended_alternative_driver: null
  conflicts_with_userspace: []
  fan_control_capable: false
  fan_control_via: null
  required_modprobe_args: []
  pwm_polarity_reservation: "not_applicable"
  exit_behaviour: "preserve"
  runtime_conflict_detection_supported: false
  firmware_curve_offload_capable: false
  citations: []
```

Catalog `name` allowlist (per controllability map §11.7):

```
coretemp, k10temp, k8temp, nvme, drivetemp, acpitz,
peci_cputemp, peci_dimmtemp, iwlwifi, amdgpu (read-only via hwmon),
nouveau, acpi_power_meter, BAT0, AC, corsair_psu, fsp3y,
jc42, lm75, tmp102, ds620, sht3x
```

**Note on `amdgpu`:** the read-only flag here applies to the standard hwmon `pwm` attribute. AMD GPU fan control goes through a *separate* sysfs path with explicit `pwm1_enable=1` toggle — handled by the GPU vendor catalog (Phase 2 #4, separate document).

---

## G. AIO/HID — explicitly not in this catalog

Per controllability map §6 and spec-02 series. These have dedicated ventd backends, not hwmon:

| Driver / device family | ventd backend | Status |
|---|---|---|
| `corsair-cpro` (Commander Pro) | spec-02a | Planned |
| Corsair Commander Core / Core XT / ST (USB HID) | shipped v0.4.0 | Shipped |
| `nzxt-kraken2`, `nzxt-kraken3` | future spec | TBD |
| `nzxt-smart2` | future spec | TBD |
| `aquacomputer-d5next` | future spec | TBD |
| `gigabyte_waterforce` | future spec | TBD |
| `asus_rog_ryujin` | future spec | TBD |
| iCUE LINK (USB HID) | v0.4.1 | Planned |

**Diagnostic note:** when these kernel drivers are loaded *alongside* ventd's HID backends, conflict — already documented in spec-02. Diagnostic bundle PR 2c detects.

---

## H. Verification matrix (Phoenix's hardware coverage)

For each catalog entry above, what real hardware in Phoenix's matrix validates it?

| Entry | Test target | Phoenix box | Calibration confidence |
|---|---|---|---|
| `nct6798` | desktop 13900K + RTX4090 | Box 1 | High — primary dev box |
| `nct6775` (older) | Proxmox 5800X + RTX3060 | Box 2 (192.168.7.10) | Medium |
| `it8689` (Gigabyte Aorus quirk) | none in matrix | — | **GAP — needs contributor** |
| `nct6687` (MSI OOT) | none in matrix | — | **GAP — needs contributor** |
| `dell_smm` | Latitude 7280 | Box 5 | High |
| `hp_wmi` | HP Pavilion x360 | Box 6 | Medium (RO mostly) |
| `ideapad-laptop` (pre-7.1) | IdeaPad Flex5 | Box 7 (partner machine) | Medium — preserve-Win constraint |
| `yogafan` (7.1+) | IdeaPad Flex5 dual-boot | Box 7 | Medium — TBD-verify is the priority probe |
| `thinkpad_acpi` | none in matrix | — | **GAP — common contributor request** |
| `steamdeck_hwmon` | SteamDeck OLED Sephiroth | Box 4 | High |
| `cros_ec_lpcs` (Framework) | none in matrix | — | **GAP — Framework laptop is on the wishlist per memory** |
| `gpd-fan` | none in matrix | — | **GAP** |
| `surface_fan` | none in matrix | — | **GAP — RO design, not critical** |
| `legion_hwmon` | none in matrix | — | **GAP — important for spec-05 P4-HWCURVE validation** |
| `pwm-fan` (RPi 5) | none in matrix | — | **GAP — ARM coverage hole per memory** |
| Various add-in fan controllers | none in matrix | — | Conservative defaults sufficient |

**Recommendation:** Phoenix's `cc-prompt-spec03-pr2a.md` should include a "verified-hardware" table that tracks which catalog entries are validated vs schema-only, and mark unvalidated entries with a `validation_status: schema_only | partial | full` field. Per spec-05-prep notes, predictive thermal Phase 0 needs a wider hardware spread anyway.

---

## I. Catalog seed file structure (PR 2a deliverable)

PR 2a's CC prompt should create:

```
internal/hwdb/catalog/
├── drivers/
│   ├── nct6775.yaml
│   ├── nct6687d.yaml
│   ├── nct6686d.yaml
│   ├── nct6683.yaml
│   ├── it87.yaml
│   ├── f71882fg.yaml
│   ├── w83627ehf.yaml
│   ├── legacy.yaml         # combined w83627hf, smsc47m1, pc87427, vt1211, dme1737, lm63/85/93
│   ├── dell-smm.yaml
│   ├── hp-wmi.yaml
│   ├── ideapad.yaml
│   ├── yogafan.yaml
│   ├── thinkpad-acpi.yaml
│   ├── asus.yaml           # asus-nb-wmi + asus_ec_sensors + asus_wmi_sensors
│   ├── gpd-fan.yaml
│   ├── surface-fan.yaml
│   ├── steamdeck.yaml
│   ├── cros-ec.yaml
│   ├── legion.yaml
│   ├── nbfc.yaml           # spec-09 placeholder; full content deferred
│   ├── addin.yaml          # adt7475, max31790, emc2305, amc6821, nct73xx, nct78xx, nct79xx
│   ├── pwm-fan.yaml
│   ├── nas-bmc.yaml        # qnap-mcu, gxp, aspeed, npcm
│   └── sensor-only.yaml    # all Tier-F entries
└── chips/
    ├── nct6775-family.yaml      # nct6106..nct6799 chip variants
    ├── it87-family.yaml         # it8603..it87952 + frankcrawford additions
    ├── f71882fg-family.yaml     # f71808..f81865
    └── ... (one per driver family)
```

PR 2a's success criterion: every `name` listed in this document has a chip_profile entry; every driver here has a driver_profile entry; matcher resolves PR 1's existing `pwm_control: <string>` against this catalog with the migration fallback.

---

## J. Summary statistics

- **Driver profiles defined:** ~25 (counting tier-A/B/C/D + sensor-only)
- **Chip profiles defined:** ~80 `name` values mapped to driver profiles (most density in nct6775/it87 families)
- **`name` allowlist for `pwm_control`:** ~50 controllable + ~25 sensor-only = ~75 total
- **Recommended OOT alternatives:** 3 (nct6687d, nct6686d, frankcrawford-it87-DKMS)
- **Drivers needing `probe_required` polarity:** 3 (it87 mainline + frankcrawford OOT, nct6687, nct6686, yogafan)
- **Drivers with `firmware_curve_offload_capable: true`:** 6 (nct6775, it87, f71882fg, legion-laptop, pwm-fan with cooling-levels DT, w83627ehf)
- **Drivers with `runtime_conflict_detection_supported: false`:** 3 (nct6683 RO, hp-wmi-sensors, surface-fan, asus_ec_sensors — all RO drivers)
- **Drivers with userspace conflicts to detect:** 7 (steamdeck → jupiter, dell-smm → i8kmon, thinkpad → thinkfan/tpfancod, legion → legion_cli/gui, pwm-fan → thermal_governor, nbfc → nbfc_service, plus generic fancontrol detection on any hwmon)

---

## K. Open items for chat 3 / hardware verification

Items this document declares but cannot confirm without real hardware:

1. **`yogafan` defaults.** `pwm_unit: duty_0_255`, `capability: rw_full`, `pwm_polarity_reservation: probe_required` — all provisional. Phoenix's IdeaPad Flex5 with kernel ≥7.1 verifies. **Priority for spec-05-prep trace harness.**
2. **`nct6687d` `off_behaviour`** marked `stops` with `rw_quirk` skepticism. MSI board owner needs to confirm via calibration probe.
3. **`legion_hwmon` 10-point auto curve trip-point semantics.** spec-05's P4-HWCURVE needs concrete trip-point write order behaviour. Lenovo Legion owner needed.
4. **`it8689` Gigabyte BIOS-locked case** — schema captures via `fan_control_capable: false` board profile, but specific board list isn't known. Frankcrawford README + GitHub issues needed for enumeration.
5. **`dell_smm` `pwm_unit_max`** varies 2 vs 3 across models. Phoenix's Latitude 7280 confirms one data point (likely 2).

**spec-05-prep trace harness scope:** validating these defaults across Phoenix's 7-box matrix is exactly what the harness exists for. Catalog entries with `validation_status: schema_only` are first-priority capture targets.

---

## L. Citations consolidated

Kernel docs:
- nct6775: https://docs.kernel.org/hwmon/nct6775.html
- nct6683: https://docs.kernel.org/hwmon/nct6683.html
- it87: https://docs.kernel.org/hwmon/it87.html
- f71882fg: https://docs.kernel.org/hwmon/f71882fg.html
- w83627ehf: https://docs.kernel.org/hwmon/w83627ehf.html
- dell-smm-hwmon: https://docs.kernel.org/hwmon/dell-smm-hwmon.html
- hp-wmi-sensors: https://docs.kernel.org/hwmon/hp-wmi-sensors.html
- asus_ec_sensors: https://docs.kernel.org/hwmon/asus_ec_sensors.html
- gpd-fan: https://docs.kernel.org/hwmon/gpd-fan.html
- surface_fan: https://docs.kernel.org/hwmon/surface_fan.html
- pwm-fan: https://docs.kernel.org/hwmon/pwm-fan.html
- nct7363: https://docs.kernel.org/hwmon/nct7363.html
- nct7802: https://docs.kernel.org/hwmon/nct7802.html
- nct7904: https://docs.kernel.org/hwmon/nct7904.html
- adt7475: https://docs.kernel.org/hwmon/adt7475.html
- thinkpad-acpi: https://docs.kernel.org/admin-guide/laptops/thinkpad-acpi.html

Out-of-tree drivers:
- nct6687d (Fred78290): https://github.com/Fred78290/nct6687d
- nct6686d (s25g5d4): https://github.com/s25g5d4/nct6686d
- it87 (frankcrawford): https://github.com/frankcrawford/it87
- LenovoLegionLinux: https://github.com/johnfanv2/LenovoLegionLinux

Userspace tools and references:
- NBFC-Linux: https://github.com/nbfc-linux/nbfc-linux
- thinkfan: https://github.com/vmatare/thinkfan
- liquidctl/liquidtux: https://github.com/liquidctl/liquidtux
- LibreHardwareMonitor: https://github.com/LibreHardwareMonitor/LibreHardwareMonitor (register source for nct6687d)

Issue trackers consulted:
- LHM #1993 (NCT5585D + NCT6799D coexistence on AsRock X870E Nova)
- LHM #1565 (ASUS PRIME X870-P NCT6701D added)
- LHM #1523 (ASUS ROG STRIX X670E-E GAMING WIFI Fan Sensors And Control)
- frankcrawford/it87 #68 (Gigabyte B650 Aorus Elite AX BIOS-locked)
- nct6687d #119 (Proxmox 6.11 nct6683 perm denied → install nct6687d)
- bugzilla.kernel.org #201097 (Dell SMM audio dropouts)

News / context:
- Linux 7.1 yogafan: https://www.phoronix.com/news/Linux-7.1-HWMON
- ArchWiki Fan speed control: https://wiki.archlinux.org/title/Fan_speed_control
- Howett's Framework EC writeup: https://www.howett.net/posts/2021-12-framework-ec/

---

**End of catalog.** Per-chip schema-aware data ready for PR 2a's catalog seed YAML generation. Validation gaps (§H, §K) feed back into spec-05-prep trace harness scope.
