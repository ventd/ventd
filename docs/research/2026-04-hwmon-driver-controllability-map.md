# hwmon driver controllability map — research input for spec-03 PR 2

**Status:** Research artifact. Produced 2026-04-26 in claude.ai chat 1 of spec-03 PR 2 design.
**Purpose:** Map every Linux kernel module that ventd might encounter when scanning `/sys/class/hwmon/*/name` and answer: can ventd actually control fans through this driver, what does the schema need to encode, what does the diagnostic bundle need to detect.
**Consumed by:** chat 2 (per-chip PWM range research, structural insights for spec-03 amendments, three CC prompts for PR 2a/2b/2c).

This document is the load-bearing artifact. The PWM curve catalog comes second — and is much cheaper to produce once the schema can express what it needs to.

---

## 1. Executive findings

Six structural realities the current spec-03 schema does not encode:

### 1.1 Mainline driver != controllable driver

The kernel module name reported in `/sys/class/hwmon/*/name` says nothing about whether ventd can write a PWM. Three orthogonal conditions decide controllability:

1. **Driver writability.** Some mainline drivers are read-only by design — `nct6683` (Intel firmware NDA), `hp-wmi-sensors` (consumer HP), `surface_fan` (Surface Pro 9), `asus_ec_sensors` (extra ASUS sensors only — fan control still goes via Super I/O). Others have writable `pwm` attributes.
2. **Out-of-tree fork availability.** When the mainline driver is read-only or absent, an OOT DKMS fork may exist — `nct6687d` (Fred78290), `nct6686d` (s25g5d4), `frankcrawford/it87`, `LenovoLegionLinux`. These typically register a *different* module name (`nct6687`, `nct6686`) than the mainline driver they replace.
3. **Per-board firmware quirks.** Even when the driver is writable, specific motherboards may have BIOS that ignores writes (some Gigabyte it8689 boards), require module parameters (`force_id`, `ignore_resource_conflict`), or need register-layout overrides (`fan_config=msi_alt1`).

The schema currently models `pwm_control: <kernel-module-name>` as a single string. Real coverage requires a **driver capability profile** that encodes mainline-RO vs OOT-RW vs board-quirks.

### 1.2 The `/sys/class/hwmon/*/name` value is not the kernel module

Critical confusion source. `name` is a driver-defined string, not the module that owns the driver. Examples:

| `name` value | Source kernel module | Notes |
|---|---|---|
| `nct6798` | `nct6775` | Module-name family covers many chips; `name` is per-chip prefix |
| `nct6687` | `nct6687d` (OOT, Fred78290) | OOT fork registers as `nct6687` |
| `nct6686` | `nct6686d` (OOT, s25g5d4) | OOT fork registers as `nct6686` |
| `it8689` | `it87` | Mainline or OOT, same name |
| `dell_smm` | `dell-smm-hwmon` | Module name has dash, `name` has underscore |
| `coretemp` | `coretemp` | Read-only sensor, no fan control |
| `k10temp` | `k10temp` | Read-only sensor, no fan control |
| `acpitz` | ACPI thermal zone | No fan control via this name |
| `asus_ec` | `asus_ec_sensors` | Read-only EC sensors, fan control elsewhere |
| `legion_hwmon` | `legion-laptop` (OOT) | LenovoLegionLinux DKMS |

The schema's `pwm_control` allowlist must be the set of **`name` values that imply a controllable PWM path**, not kernel module names. This is a meaningful rename and a meaningful filter pass.

### 1.3 The pwm sysfs scale isn't always 0-255

For Super I/O drivers it almost always is. For laptop platform drivers it often isn't:

- **dell-smm-hwmon:** `pwmX` value is a **fan state index** (0-2 or 0-3), not a duty cycle. `pwm=255` and `pwm=2` are *the same thing*.
- **ideapad-laptop / yogafan:** historically `pwmX` was 0-255 but driver-internally mapped to fan-mode steps. New 7.1 `yogafan` driver: needs verification in chat 2.
- **gpd-fan:** 0-255, advertised verbatim. Real.
- **steamdeck-hwmon:** 0-255 (the EC accepts a real PWM duty).
- **thinkpad_acpi:** does NOT use hwmon `pwm`. Uses `/proc/acpi/ibm/fan` with level 0-7 + `auto` + `disengaged` + `full-speed`. Different interface entirely.

The schema must encode the **PWM unit semantics** per driver: `duty_0_255` vs `step_0_N` vs `proc_acpi_level`. This affects calibration safety (writing 255 to a step-based pwm interpreting it as duty cycle = wrong fan state), safety-latch behaviour, and diagnostic interpretation.

### 1.4 Off-PWM behaviour varies wildly

**Same value, different meaning:**
- `nct6775` family: `pwm=0` writes 0 duty cycle. Fan stops if `pwm_enable=1` (manual). May fall to chip-stored minimum if `pwm_enable >= 2` (auto modes).
- `it87` family: `pwm=0, pwm_enable=1` writes 0 duty. `pwm_enable=0` is "full speed" — except IT8603E doesn't support `pwm_enable=0` at all.
- `dell-smm-hwmon`: `pwm=0` is "fan state 0" — typically "off" if hardware allows, but on some Dells fan state 0 maps to a low-RPM idle, not off.
- `gpd-fan`: when entering manual mode (`pwm_enable=1`), the driver **forces pwm=255** as a safety default. Userspace must set the real value immediately after.
- `ideapad-laptop`: `pwm=0` may fall back to BIOS auto on some models — write is silently ignored.

The schema needs `pwm_off_behaviour` per driver: `stops` / `falls_to_min` / `falls_to_auto_silently` / `bios_dependent`. The calibration probe needs this to interpret "wrote 0, fan still spinning" — that's not a phantom channel, that's a `falls_to_*` driver.

### 1.5 pwm_enable mode set is per-driver

The spec's `pwm_enable` knowledge is currently implicit. Per-driver reality:

| Driver | pwm_enable values | Notes |
|---|---|---|
| `nct6775` | 0, 1, 2, 3, 4 (NCT6775F only), 5 | 0=full-speed, 1=manual, 2-5=auto modes |
| `nct6687d` (OOT) | 0, 1, 2 (mostly) | Varies by `fan_config` setting |
| `it87` | 0, 1, 2-7 (legacy chips only) | IT8603E: no value 0 |
| `f71882fg` | 1, 2, 3 | 1=manual, 2=auto trip-points, 3=thermostat (F8000 only); F8000 channel 3 always mode 2 |
| `w83627ehf` | 0, 1, 5 | Subsumed by nct6775 for newer chips |
| `dell-smm-hwmon` | 1, 2 | Method varies; some machines have only `pwm1_enable` global (write-only) |
| `gpd-fan` | 0, 1, 2 | 0=full speed, 1=manual, 2=EC auto |
| `ideapad-laptop` | 1, 2 | Varies by model and BIOS revision |
| `pwm-fan` (devicetree) | (single value) | Doesn't expose `pwm_enable` — single-mode driver |

The schema needs to encode the per-driver supported mode set. ventd's calibration code needs to write the right `pwm_enable=1` (manual) value before writing duty cycles — and on drivers that don't support manual mode at all (read-only ones), refuse to apply curves at all and surface the recommendation to install an OOT fork.

### 1.6 Fan curve entry isn't the only useful auto mode

`nct6775` Smart Fan IV (mode 5) and `it87` legacy auto have **chip-internal trip-point tables** (`pwm[N]_auto_point[1-7]_pwm` + `_temp`). Hardware-curve offload (P4-HWCURVE in masterplan) is real for some chips. Tier-3 conservative defaults shouldn't write to chip-internal tables, but the diagnostic bundle should detect their presence and the predictive-thermal layer (spec-05) might want to use them as a fallback when daemon is suspended.

---

## 2. Tier-A: x86 desktop / server Super I/O — the bulk target

### 2.1 nct6775 family (Nuvoton — Asus, Gigabyte, ASRock, MSI legacy) — ~75 % of modern AMD/Intel desktops

**Source:** https://docs.kernel.org/hwmon/nct6775.html
**Mainline driver:** `nct6775` (in-tree, writable, mature)
**hwmon `name` values:** `nct6106`, `nct6116`, `nct6775`, `nct6776`, `nct6779`, `nct6791`, `nct6792`, `nct6793`, `nct6795`, `nct6796`, `nct6797`, `nct6798`, `nct6799` (and W83677HG-I as `nct6775`)
**Coverage:** AMD (X470/X570/X670/B550/B650) and Intel (Z390/Z490/Z590/Z690/Z790) consumer/enthusiast desktops where the BIOS doesn't route to a separate EC chip.
**Controllability:** Full read/write. Manual mode (`pwm_enable=1`), Thermal Cruise (mode 2), Fan Speed Cruise (mode 3), Smart Fan III (mode 4, NCT6775F-only), Smart Fan IV (mode 5). Up to 7 PWM channels.
**PWM unit:** `duty_0_255`.
**Off behaviour:** `pwm=0` in mode 1 stops fan. In modes 2+ it falls to `pwmN_floor`.
**Known quirks:**
- ASUS boards: `CPUTIN` floats — ignore `temp1` from this driver on ASUS, use PECI 0 / TSI 0 (which surface via different sensor sources).
- Hardware curve trip-points exist (`pwm[1-7]_auto_point[1-7]_pwm/_temp`).
- Mode set `pwm_enable=4` (Smart Fan III) only on NCT6775F.
**Schema implications:**
- `name` allowlist: 13 chip variants share one driver. Schema's `pwm_control` field needs to allow either driver-name (`nct6775`) or chip-prefix (`nct6798`) and resolve correctly.
- ASUS quirk: must be a per-board entry, not a per-chip entry — board-fingerprint tier-1/2 owns this.

### 2.2 nct6683 family — read-only mainline / out-of-tree controllability — modern MSI + ASRock AM5

**Source:** https://docs.kernel.org/hwmon/nct6683.html
**Mainline driver:** `nct6683` — **READ-ONLY**. Writes disabled because Intel firmware register layout is held under NDA per Nuvoton.
**hwmon `name` values:** `nct6683` (mainline RO), `nct6687` (OOT Fred78290 RW), `nct6686` (OOT s25g5d4 RW for ASRock)
**Coverage:** **Massive and growing** — every documented MSI X670/B650/X870/B850/Z890 board, ASRock X570/X670E/B650/A620 series, Intel DH/DB87 series, AMD BC-250.
**Controllability via mainline:** **None.** Read sensors only. Writing PWM returns EPERM or silent-no-op.
**Out-of-tree forks:**
- **`Fred78290/nct6687d`** — DKMS module loading as `nct6687`. Reverse-engineered from LibreHardwareMonitor (Windows). Mature, maintained as of 2025. Supports MSI B550/B660/B760/X670/B650/X870/B850/Z690/Z790/Z890 + ASUS B460M Bazooka. Has `fan_config` module param: `default` or `msi_alt1` (auto-detected for 36+ MSI boards including B840/B850/X870/X870E/Z890). Has experimental `msi_fan_brute_force` for boards where standard PWM writes don't take effect immediately.
- **`s25g5d4/nct6686d`** — DKMS forked from Fred78290's work. Loads as `nct6686`. Targets newer ASRock AMD boards (A620I Lighting WiFi, etc.) where Fred's nct6687 doesn't quite match.
**PWM unit:** `duty_0_255`.
**Off behaviour:** Likely `stops` in manual mode; needs verification per chip.
**Known quirks:**
- voltage register layout differs between Intel and AMD platforms — `manual=1` module param + `/etc/sensors.d/` config required for accurate voltage readings on some boards.
- Some boards: Fred's driver shows CPU/Pump RPMs but no system-fan data → user must apply `msi_alt1` → which is now auto-detected for 36+ boards as of recent Fred78290 versions.
- BIOS must have "Fan Type Auto Detect = Enabled" and "Smart Fan Mode = Disabled" for PWM writes to actually take effect on some MSI boards.
**Schema implications — major:**
- A board may have `name=nct6683` (mainline RO) OR `name=nct6687` (OOT Fred RW) OR `name=nct6686` (OOT s25g RW) for what is fundamentally the same chip. Schema needs to encode that all three are the *same hardware* with different driver controllability.
- ventd should detect mainline `nct6683` and surface a recommendation to install `nct6687d-dkms` or `nct6686d-dkms` based on board vendor. This is a DIAGNOSTIC + INSTALL-GUIDANCE feature, not a curve catalog feature.
- Tier-3 generic profile for `name=nct6683` should be MARKED-INCAPABLE-OF-WRITE with curves empty and a `recommended_driver: "nct6687d-dkms or nct6686d-dkms"` field.

### 2.3 it87 family (ITE — common on ASRock, Gigabyte, some Intel) — ~20 % of modern boards

**Source:** https://docs.kernel.org/hwmon/it87.html, https://github.com/frankcrawford/it87, https://github.com/a1wong/it87
**Mainline driver:** `it87` — writable, manual mode (`pwm_enable=1`) supported. Old auto mode (4 trip points, point 4 always max) for IT8705F<rev F and IT8712F<rev G only.
**hwmon `name` values:** `it8603`, `it8620`, `it8628`, `it8665`, `it8686`, `it8688`, `it8689`, `it8705`, `it8712`, `it8716`, `it8718`, `it8720`, `it8721`, `it8728`, `it8732`, `it8771`, `it8772`, `it8781`, `it8782`, `it8783`, `it8786`, `it8790`, `it8792`, `it87952` — covered by mainline. Newer chips (IT8606E, IT8607E, IT8613E, IT8622E, IT8625E, IT8655E, IT8696E, IT8698E) need OOT fork.
**Coverage:**
- Mainline: most ASRock pre-2022, some Gigabyte, some MSI Intel.
- OOT (frankcrawford/it87): Gigabyte boards with IT8689E/IT8688E/IT8686E (B450/B550/X570/B650/X670 Aorus), newer ASRock boards.
**Controllability:**
- Mainline: writes accepted on all listed chips. Some chip variants (IT8603E specifically) don't support `pwm_enable=0` (no full-speed mode).
- OOT: same as mainline + extended chip support. **Some Gigabyte IT8689 boards reject manual control entirely** — fan curve must be set via BIOS, OOT driver returns success on writes but fan doesn't change. frankcrawford README documents this as "known issue, fix is known but complex."
**PWM unit:** `duty_0_255`.
**Off behaviour:** `pwm=0, pwm_enable=1` writes 0 duty (fan stops). `pwm_enable=0` is full speed — except IT8603E doesn't have it.
**Known quirks:**
- **PWM polarity inversion.** Some BIOSes misconfigure PWM polarity → 0 means full speed, 255 means stopped. Driver has `fix_pwm_polarity` module param marked "DANGEROUS" — calibration phase MUST detect this empirically (write low PWM → expect low RPM; if high RPM, polarity is inverted).
- **Resource conflict.** Newer ITE chips often need `ignore_resource_conflict=1` because BIOS claims the IO ports via ACPI. This module param has documented risk: "race conditions, worst case unexpected system reboots." ventd should detect this in diagnostic and surface as warning when modprobe args are required.
- **`force_id` requirement.** Newest chips (IT8689 etc.) appear with unknown chip ID. User must `modprobe it87 force_id=0x8689 ignore_resource_conflict=1`. ventd diagnostic should recognise this pattern.
- Gigabyte boards with new fan-control chip: "you can measure the speed, you cannot change it" — separate physical chip handles fan curves, not the IT87.
**Schema implications:**
- The "fan-but-no-control" Gigabyte case needs explicit modeling. board-fingerprint tier-1/2 entries for these boards should set `fan_control_capable: false` even when `it87` is loaded.
- Mainline-vs-OOT chip coverage is a moving target. Schema's `pwm_control` field for `it87` shouldn't reject newer `name` values it doesn't recognise — should defer to "is the sysfs writable" probe.

### 2.4 f71882fg family (Fintek) — older, embedded, niche modern boards

**Source:** https://docs.kernel.org/hwmon/f71882fg.html
**Mainline driver:** `f71882fg` — writable
**hwmon `name` values:** `f71808e`, `f71808a`, `f71858fg`, `f71862fg`, `f71869`, `f71869a`, `f71882fg`, `f71889fg`, `f71889ed`, `f71889a`, `f8000`, `f81801u`, `f81865f`
**Coverage:** Older Asus/Foxconn/Jetway boards (~2009-2014), some embedded SBCs. Rare on consumer 2018+ boards. Still appears on some Mini-ITX server boards.
**Controllability:** Mode 1 (manual) + Mode 2 (auto trip-points) + Mode 3 (thermostat — F8000 only, only when in duty-cycle mode).
**PWM unit:** `duty_0_255` — but interpretation depends on BIOS-set RPM-mode vs PWM-mode. Same 0-255 byte means different things in each mode. Driver records mode at load time and you cannot easily query it.
**Off behaviour:** `pwm=0` in PWM mode = 0% duty (fan stops). In RPM mode = 0% of `fanN_full_speed` setting (which BIOS must have set sanely).
**Known quirks:**
- **F8000:** PWM channel 3 is always in mode 2 (auto). Cannot be changed to manual.
- F71858FG and F8000: mode 1 (manual) only available when fan channel is in RPM mode (BIOS-set).
- Trip-point semantics: lowest-numbered trip point corresponds to *highest* temperature zone. Driver mimics IC layout instead of normalising. Surprising and bug-prone.
**Schema implications:**
- `mode_1_available_per_channel` field needed: F8000 channel 3 is locked.
- BIOS-set `fanN_full_speed` is a precondition for sane control in RPM mode. Calibration should detect and warn.

### 2.5 w83627ehf (Winbond) — subsumed by nct6775 for modern, but legacy boards still

**Source:** https://docs.kernel.org/hwmon/w83627ehf.html
**Mainline driver:** `w83627ehf` — writable
**Status:** Note in nct6775 docs: "This driver supersedes the NCT6775F and NCT6776F support in the W83627EHF driver." Newer Nuvoton chips moved to nct6775 driver. w83627ehf retained for older Winbond chips only.
**hwmon `name` values:** `w83627ehf`, `w83627dhg`, `w83627uhg`, `w83667hg`, `w83667hg_b`, plus W83667HG-I/NCT6775F/NCT6776F (these now go to nct6775)
**Coverage:** Pre-2014 boards primarily.
**Controllability:** Same general shape as nct6775 — pwm_enable 0,1,5 + Smart Fan IV variants.
**PWM unit:** `duty_0_255`.
**Schema implications:**
- Treat as "use nct6775 entry if available; fall back to w83627ehf table for legacy chips not in nct6775 driver."

### 2.6 w83627hf, smsc47m1, pc87427, vt1211, dme1737 — old / EOL / very rare

**Coverage:** Pre-2010 desktops. Niche embedded boards. Server boards 2005-era.
**Controllability:** All writable via mainline.
**Schema implications:**
- Generic catalog entry per `name` is fine — conservative defaults, no special quirks researched. Phoenix's seven-box matrix probably never sees these. Coverage is for "a contributor will eventually report one and we shouldn't ship `match=none`."

---

## 3. Tier-B: laptop EC / platform drivers — divergent architectures

### 3.1 dell-smm-hwmon (Dell laptops + some desktops) — covers latitude-7280

**Source:** https://docs.kernel.org/hwmon/dell-smm-hwmon.html
**Mainline driver:** `dell-smm-hwmon` (alias: `i8k`) — writable on **whitelisted models only**
**hwmon `name` value:** `dell_smm`
**Coverage:** Dell Latitude (whitelisted: many), XPS, Inspiron, Precision, OptiPlex. Whitelist lives in kernel source `i8k_whitelist_fan_control`.
**Controllability:**
- Two methods: (1) per-fan SMM commands with standard `pwmX_enable`, (2) global `pwm1_enable` (write-only, controls all fans). Driver heuristic-detects.
- Many models: **`pwmX_enable` attribute simply does not appear** because BIOS not whitelisted. User can recompile kernel with their model added or use `dellfan` to discover codes.
**PWM unit:** **`step_0_N`**, NOT duty-0-255. The `pwmX` value is a fan state index (typically 0-2 or 0-3). Writing 255 = setting state to "max state" (typically state 2 or 3). The kernel exposes this through the standard PWM sysfs name despite the semantic mismatch.
**Off behaviour:** `pwm=0` = state 0 = typically off, but on some Dells state 0 maps to a low-RPM idle, not full off.
**Known quirks:**
- **SMM call latency:** Up to 500ms on some machines (Inspiron 7720, Vostro 3360, XPS 13 9333, XPS 15 L502X). Polling at 1Hz causes audio dropouts. ventd polling cadence must be aware.
- **Auto-mode SMM commands cause "severe side effects"** on many machines and are deliberately not used by mainline driver. Don't try to enable BIOS auto via this path.
- **Some models:** "magic" 4th fan state signals BIOS to take auto control of a fan. RPM reported in this state is a placeholder, not a real value.
- **Firmware bugs:** Reading fan state returns spurious errors (Precision 490, OptiPlex 7060). Reading fan type causes erratic fan behaviour (Studio XPS 8000/8100, Inspiron 580/3505).
- **Module params:** `force=1` to load on non-whitelisted, `ignore_dmi=1` to bypass DMI check, `fan_mult` and `fan_max` for autodetect overrides.
**Schema implications — significant:**
- PWM unit field: `step_0_N` with `step_count` per board. Calibration must enumerate available states per fan rather than sweep 0-255.
- Per-board "SMM latency hint" field for polling cadence selection.
- Per-board firmware-bug flags (e.g., `fan_type_read_unsafe: true`).
- "BIOS auto-mode" enable/disable — **forbidden** by default per kernel doc warning.

### 3.2 hp-wmi-sensors (HP business class) + hp-wmi (HP consumer) — covers pavilion-x360 with caveats

**Source:** https://docs.kernel.org/hwmon/hp-wmi-sensors.html
**Mainline drivers:** `hp-wmi-sensors` (business class — EliteBook, ZBook, ProBook, ProDesk), `hp-wmi` (consumer — Pavilion, Envy, Omen, Spectre)
**hwmon `name` values:** `hp_wmi` (sensors), various via debugfs
**Coverage of business-class:** Listed in driver source. Business-class machines typically have BIOS WMI methods exposing sensors.
**Coverage of consumer (`hp-wmi`):** Pavilion, Envy etc. — driver handles hotkeys, backlight, **and read-only fan readings**.
**Controllability:**
- `hp-wmi-sensors`: **READ-ONLY**. No `pwm` attributes at all. `fan_input`, `fan_label`, `fan_fault`, `fan_alarm`, `intrusion_alarm`. Fan is fully controlled by BIOS.
- `hp-wmi`: also read-only for fan. Hotkeys + backlight write paths exist but not fan PWM.
**PWM unit:** N/A — there's no PWM exposed.
**Out-of-tree options:**
- **`bashar-naser/HP-Omen-Linux-Module`** for some Omen models — RW fan control via `/sys/devices/platform/hp-wmi/`
- **NBFC-Linux** has configs for many HP Pavilion / EliteBook models — talks directly to EC registers, bypasses kernel driver entirely
- For pavilion-x360 specifically: NBFC-Linux has a config (`HP Pavilion x360 14-cd0xxx` or similar). Direct EC access.
**Known quirks:**
- If `hp-wmi` (consumer) is loaded, `hp-wmi-sensors` (business) cannot acquire its WMI event GUID — they conflict on consumer/business hybrid machines. Driver doc explicitly notes this.
- Inconsistent BIOS WMI implementations cause inaccurate readings.
**Schema implications — major:**
- HP consumer = "no kernel-level fan control path." Schema needs to encode `requires_userspace_ec_driver` flag with reference to NBFC config name.
- Diagnostic bundle should detect `name=hp_wmi` + check NBFC config availability + surface install recommendation.
- Tier-3 entry for `hp_wmi`: `fan_control_capable: false`, `recommended_alternative: "NBFC-Linux"`.

### 3.3 ideapad-laptop / yogafan (Lenovo IdeaPad/Yoga/Flex/Slim/Legion) — covers ideapad-flex5

**Source kernel docs:** Not in main hwmon index — `ideapad-laptop` is in `drivers/platform/x86/`, `yogafan` is new in 7.1.
**Mainline drivers:**
- **`ideapad-laptop`** (existing, mature) — Lenovo IdeaPad/Yoga line. Provides camera button, backlight, rfkill, dual-fan-mode toggle. Limited fan control on some models via `/sys/bus/platform/devices/VPC2004:00/fan_mode`.
- **`yogafan`** (new in Linux 7.1) — Yoga 14cACN, Yoga 710/720, Yoga Pro 7/9, Yoga Slim 7, IdeaPad 5, Legion 5/7, LOQ 15/16, ThinkBook G6, **Flex 5**, plus pre-2020 legacy interface. **Specifically covers the test matrix's ideapad-flex5.** Interfaces with EC via ACPI.
**hwmon `name` values:** `yogafan` (new), or no hwmon entry on pre-7.1 kernels
**Out-of-tree:**
- **`LenovoLegionLinux`** — Legion-specific, registers as `legion_hwmon`. PWM auto-point tables (`pwm[1-2]_auto_point[1-N]_pwm/_temp`). Power-mode awareness (Quiet/Balanced/Performance/Custom).
**Controllability:**
- yogafan: needs verification of writable PWM, mode set, unit. Brand new driver — may be RO at v1.
- LenovoLegionLinux: full RW with auto-point tables (per Legion model). Actively maintained.
- ideapad-laptop fan_mode: simple 0/1/2/4 toggle (off/auto/silent/performance), no PWM duty cycle control.
**PWM unit:** `step_0_N` for ideapad-laptop fan_mode; needs verification for yogafan.
**Off behaviour:** ideapad-laptop fan_mode=0 may be ignored on some models — write succeeds, BIOS overrides.
**Known quirks:**
- Pre-7.1 kernels on ideapad-flex5: no kernel fan control path. Either upgrade kernel or use LenovoLegionLinux DKMS or NBFC-Linux EC-direct config.
- Some Lenovo models: fan settings reset after every BIOS power-mode change (Fn+Q). Userspace must re-apply.
**Schema implications:**
- Kernel-version awareness in schema: yogafan exists ≥7.1 only. ventd diagnostic must report kernel version + driver availability and adjust expectations.
- `requires_re_apply_after_power_mode_change: true` flag for affected models.

### 3.4 thinkpad_acpi (ThinkPad — out-of-test-matrix but ventd will see it) — different interface entirely

**Source:** kernel docs, vmatare/thinkfan
**Mainline driver:** `thinkpad_acpi` — provides `/proc/acpi/ibm/fan` interface, NOT hwmon `pwm`. **Fan control via this driver requires `experimental=1` module parameter on newer ThinkPads.**
**Coverage:** All ThinkPad models from ~2003 onwards.
**Controllability:**
- Write `level X` to `/proc/acpi/ibm/fan` where X is `0-7`, `auto`, `disengaged`, or `full-speed`.
- "disengaged" = fan runs unregulated — dangerous, can damage fan with prolonged use.
- "full-speed" = level 7 force.
**PWM unit:** **`thinkpad_level`** (0-7 + named states). Distinct from `step_0_N`.
**Off behaviour:** level 0 = fan off (if BIOS allows).
**Known quirks:**
- Some BIOS reset fan-mode after suspend/resume — daemon must re-apply.
- `experimental=1` required on T440+ generation; without it, write attempts return EPERM.
**Schema implications:**
- Distinct PWM unit. ventd needs a `thinkpad_acpi` backend separate from hwmon.

### 3.5 asus-nb-wmi / asus-wmi-sensors / asus_ec_sensors — ASUS desktop + laptop split

**Drivers:**
- **`asus-nb-wmi`** — ASUS laptops. Hotkeys, keyboard backlight, **single-fan PWM control on some models**. Covers most consumer ASUS laptop fans (when supported at all). Auto-loads on ASUS hardware.
- **`asus_wmi_sensors`** — Older ROG/Crosshair/Maximus boards. Read-only board-level sensors (chipset, water-pump). No fan control.
- **`asus_ec_sensors`** — Listed in §1.7 above. ~50 ROG/Prime/ProArt/TUF boards. Read-only EC sensors (chipset, VRM, water flow). **No fan control** — fan still controlled via the board's Super I/O (typically nct6798d).
**Schema implications:**
- `asus_ec_sensors` presence is a strong DMI tier-2 signal but doesn't itself control fans. Catalog entry: `fan_control_capable: false, fan_control_via: "<board's Super I/O nct6798/nct6799>"`.
- `asus-nb-wmi` may control single fan on some laptop models — model-specific verification needed.

### 3.6 steamdeck-hwmon (Steam Deck) — covers steamdeck-oled

**Mainline driver:** `steamdeck-hwmon` (Valve, in mainline ≥6.x)
**hwmon `name` value:** `steamdeck_hwmon`
**Coverage:** Steam Deck LCD + Steam Deck OLED.
**Controllability:** Writable. Single fan, single PWM channel, 0-255 duty.
**PWM unit:** `duty_0_255`.
**Off behaviour:** `pwm=0` = fan off.
**Userspace daemon:** **`jupiter-fan-control`** — ships with SteamOS, controls fan via this kernel driver. Implements OS-controlled fan curve introduced in SteamOS 3.2.
**Known quirks:**
- ventd MUST coexist with or replace jupiter-fan-control. Concurrent writers = chaos.
- jupiter-fan-control runs as a systemd service. ventd installation should detect and recommend disabling it before taking over.
- On non-SteamOS distros (Bazzite, HoloISO, Manjaro on Deck) jupiter-fan-control may not be present — ventd is alone.
**Schema implications:**
- `conflicts_with_userspace_daemon: ["jupiter-fan-control"]` flag. ventd install/start should detect and surface the choice to user.

### 3.7 gpd-fan (GPD handhelds) — handheld coverage outside test matrix

Already detailed in research. Writable, 0-255 duty, per-board quirk via module param. Specific board coverage in driver source.

### 3.8 surface_fan (Microsoft Surface Pro 9) — read-only

Already detailed. **READ-ONLY.** RPM only. Schema: `fan_control_capable: false`.

### 3.9 acer-wmi, framework-laptop, msi-ec, etc. — model-specific platform drivers

A growing set of vendor-specific platform drivers in `drivers/platform/x86/`. Most are read-only sensors + hotkeys; fan control varies widely. Coverage:

- **`framework-laptop`** — Framework 13 / 16. RGB LED + hotkey. No fan PWM in mainline; fan control via `fw-ectool` userspace tool talking directly to Cros EC. ventd's spec for framework specifically would talk to Cros EC, not hwmon.
- **`acer-wmi`** — read-only sensors mostly.
- **`msi-ec`** — MSI laptops (newer). Includes some fan control on supported models. Out-of-tree DKMS for older models.
**Schema implications:**
- The platform-driver coverage is a **moving target**. Schema should not gate on a fixed allowlist; ventd should probe whether `pwmN` exists and is writable, and use the catalog entry for fallback heuristics only.

### 3.10 NBFC-Linux — userspace-EC-direct backend, ENORMOUS coverage gap-filler

**Source:** https://github.com/nbfc-linux/nbfc-linux
**Architecture:** Not a kernel driver. Userspace daemon that talks to the laptop EC via `/dev/port` or `/sys/kernel/debug/ec/ec0/io`. Per-laptop XML configs define which EC registers correspond to which fans.
**Coverage:** Hundreds of laptop models. Acer Aspire, Asus Zenbook, Dell Inspiron, HP Pavilion / EliteBook, Lenovo non-IdeaPad/Yoga (ThinkPad coverage is via thinkpad_acpi), MSI, Toshiba, Xiaomi, Framework. **Fills the gap where mainline kernel drivers are read-only or absent.**
**Controllability:** Per-config. Most configs support read+write of fan PWM and per-fan thresholds.
**PWM unit:** Configs use 0-100 percentage typically; NBFC translates internally.
**Strategic question for ventd:**
- **Option A — ignore NBFC-coverable laptops.** v1.0 supports only hwmon-controllable hardware. Honest, ships sooner, leaves ~30 % of laptop installs unsupported.
- **Option B — integrate with NBFC.** ventd detects NBFC presence, parses NBFC configs to learn EC register layout, controls via NBFC's EC primitives (or shells out to `nbfc set`). Massive coverage win, complex integration.
- **Option C — write a ventd EC-direct backend.** Read NBFC's config database for register layouts (GPL-3, attribution required), implement EC access ourselves. Complete control, large engineering scope, ongoing config maintenance.
- **Option D — coexist via userspace-tool dispatch.** Detect "this is an NBFC-supported laptop with no kernel write path," surface install instructions, do not control fans ourselves.

**Recommendation: D for v1.0, B or C as v1.x.** NBFC-Linux is a working, widely-used solution. Telling users "for your laptop, install NBFC-Linux with config X; ventd will not interfere" is honest and protects ventd's "predicts not reacts" claim from being undermined by half-working laptop coverage. spec-03 schema needs `requires_userspace_ec_driver: nbfc-linux` flag with config-name pointer.

---

## 4. Tier-C: add-in fan controller chips — server / workstation / chassis

### 4.1 adt7475 (Analog Devices ADT7473/7475/7476/7490)

**Mainline driver:** `adt7475` — writable
**hwmon `name` values:** `adt7473`, `adt7475`, `adt7476`, `adt7490`
**Coverage:** Workstation boards (Supermicro, some Dell PowerEdge), high-end consumer (Asus WS series).
**Controllability:** RW. pwm_enable: 0 (off), 1 (manual), 2 (auto, temp-mapped).
**PWM unit:** `duty_0_255`.
**Notes:** Common in IPMI-controlled servers — may overlap with spec-01's IPMI backend.

### 4.2 max31790 (Maxim 6-channel PWM)

**Mainline driver:** `max31790` — writable
**hwmon `name` value:** `max31790`
**Coverage:** Standalone fan controller boards (Aquacomputer Octo, some chassis fan splitters).
**Controllability:** RW. 6 PWM channels.
**PWM unit:** `duty_0_255`.

### 4.3 emc2305 (Microchip EMC2305 5-channel)

**Mainline driver:** `emc2305` — writable
**hwmon `name` value:** `emc2305`
**Coverage:** Some servers, some embedded (Mellanox switches via mlxreg, NVIDIA Jetson dev kits).
**Controllability:** RW. 5 PWM channels.
**PWM unit:** `duty_0_255`.

### 4.4 amc6821 (TI AMC6821 single-channel)

**Mainline driver:** `amc6821` — writable
**hwmon `name` value:** `amc6821`
**Coverage:** Embedded boards, some custom chassis.
**Controllability:** RW. Single PWM.

### 4.5 nct7363, nct7802, nct7904 (Nuvoton add-in fan controllers)

**Mainline drivers:** `nct7363`, `nct7802`, `nct7904` — writable
**Coverage:** BMC-adjacent server fan management, some workstation boards.
**Controllability:** RW.

---

## 5. Tier-D: ARM SBC / devicetree

### 5.1 pwm-fan (generic devicetree) — covers RPi 5 + most SBCs

**Source:** https://docs.kernel.org/hwmon/pwm-fan.html
**Mainline driver:** `pwm-fan` — writable
**hwmon `name` value:** `pwmfan` or driver-defined
**Coverage:** Raspberry Pi 5, NanoPi, OrangePi, Rockchip boards, Allwinner boards, NXP i.MX boards, anything with a devicetree-described PWM fan.
**Controllability:** RW. Single PWM typically.
**PWM unit:** `duty_0_255`. **However:** the pwm-fan driver supports `cooling-levels` devicetree property — when set, fan transitions through discrete cooling-state levels rather than continuous 0-255. This is a thermal-zone-driven mode.
**Off behaviour:** `pwm=0` stops fan (PWM signal goes to 0% duty).
**Known quirks:**
- **Thermal governor coupling.** RPi 5 default fan controller is the kernel thermal governor + step_wise — this drives `cooling-levels` and conflicts with userspace pwm writes. ventd needs to claim the thermal zone or coordinate.
- DT property `pulses-per-revolution` defaults to 2; some fans need 1 or 4 — wrong value gives 2x/0.5x RPM readings.
**Schema implications:**
- `requires_thermal_zone_management: true` flag. ventd start should disable thermal-zone-driven cooling-map for the fan it's claiming.

### 5.2 raspberrypi-hwmon

**Coverage:** RPi voltage monitor only — no fan. Pi 5 fan goes through `pwm-fan` instead.
**Schema entry:** `fan_control_capable: false`.

### 5.3 qnap-mcu-hwmon, gxp-fan-ctrl, aspeed-pwm-tacho, npcm750-pwm-fan

NAS / server BMC drivers. Out of typical homelab scope but worth catalog entries.
**Controllability:** Most are RW.
**Notes:** ASPEED + NPCM are BMC chips themselves — appear on motherboards that *are* the BMC, typically not what end-user ventd targets.

---

## 6. Tier-E: AIOs / liquid coolers — separate spec (spec-02 series)

These are NOT controlled via the hwmon catalog. ventd has dedicated backends:

| Driver | Hardware | ventd backend |
|---|---|---|
| `corsair-cpro` | Commander Pro | spec-02a (planned) |
| `corsair-core` (USB HID, no kernel driver) | Commander Core / Core XT / ST | shipped v0.4.0 |
| `nzxt-kraken2`, `nzxt-kraken3` | NZXT Kraken | future spec |
| `nzxt-smart2` | NZXT Smart 2 | future spec |
| `aquacomputer-d5next` | Aquacomputer pumps | future spec |
| `gigabyte_waterforce` | Gigabyte AIOs | future spec |
| `asus_rog_ryujin` | ASUS Ryujin | future spec |
| `iCUE LINK` (USB HID, no kernel driver yet) | Corsair iCUE LINK | v0.4.1 (planned) |

**Important:** when these kernel drivers are loaded, they may **conflict** with ventd's HID-direct backends. spec-02 already documents this for `corsair-cpro`. The diagnostic bundle should detect AIO kernel drivers loaded alongside ventd's HID backends.

**Schema implications:** none — these don't go in the Super I/O catalog.

---

## 7. Tier-F: read-only sensor sources (catalog entries: `fan_control_capable: false`)

These appear as `name` values when scanning `/sys/class/hwmon/` but never expose `pwm`. ventd reads them for temperature/power signals.

| `name` | Module | Notes |
|---|---|---|
| `coretemp` | coretemp | Intel CPU per-core temp |
| `k10temp` | k10temp | AMD CPU Tctl/Tdie |
| `k8temp` | k8temp | AMD pre-Bulldozer (EOL) |
| `nvme` | nvme | NVMe Composite + sensors |
| `drivetemp` | drivetemp | SATA SCT temperature |
| `acpitz` | ACPI thermal zone | Generic ACPI thermal |
| `peci_cputemp` | peci-cputemp | Intel PECI |
| `peci_dimmtemp` | peci-dimmtemp | Intel DIMM |
| `iwlwifi` | iwlwifi | Intel WiFi temperature |
| `amdgpu` | amdgpu | AMD GPU — special case, see §8 |
| `nouveau` | nouveau | NVIDIA GPU OSS — read-only |
| `power_meter_*` | acpi_power_meter | ACPI power |
| `BAT0`, `AC` | (battery / power_supply) | Charge state |

**Schema implications:** the diagnostic bundle should enumerate all `name` values, distinguish "read-only signal source" from "fan control candidate," and only treat the latter as `pwm_control` candidates.

---

## 8. GPU vendor catalog — separate from this map

NVIDIA via NVML and AMD via AMDGPU sysfs control GPU fans directly. Handled by the **separate B-track GPU vendor catalog** (per Phoenix's earlier confirmation). This map covers only motherboard/case/EC fans.

GPU fan path summary for completeness:
- **NVIDIA:** NVML library (`libnvidia-ml.so`) — `nvmlDeviceGetFanSpeed_v2`, `nvmlDeviceSetFanSpeed_v2` (driver permission needed). PWM unit: percentage 0-100.
- **AMD:** `amdgpu` driver hwmon — `pwm1` writable when `pwm1_enable=1`. PWM unit: `duty_0_255`. Quirk: some AMDGPU on boot has `pwm=0` with auto mode stuck — needs manual→auto dance to unstick.
- **Intel:** iGPU has no fan (cooled by CPU heatsink). dGPU (Arc Alchemist/Battlemage) — TBD per release.

---

## 9. Schema v1 implications — what spec-03 PR 1 doesn't currently encode

The current spec-03 PR 1 schema (per chat 1's earlier reading):

```yaml
hardware:
  fan_count: 6
  pwm_control: "nct6798d"          # kernel module name allowlist
```

**Gaps identified in this map:**

1. **`pwm_control` semantics ambiguous.** Currently a single string from `RULE-HWDB-05` ~20-module allowlist. But:
   - Same chip can have 3 different `name` values (`nct6683` mainline RO, `nct6687` Fred OOT RW, `nct6686` s25g OOT RW).
   - `name` value vs kernel module name are different (per §1.2).
   - Some boards expose multiple controllable drivers simultaneously (Super I/O for fans + amdgpu for GPU + adt7475 for AIO pump tach).

   **Recommended schema change:** `pwm_control` becomes a list of `controller` objects, each with `name_pattern` (regex over `/sys/class/hwmon/*/name`), `module`, `capability` (`rw_full`, `rw_quirk`, `ro`), and `notes`.

2. **No PWM unit field.** Schema implicitly assumes `duty_0_255`. Reality: `step_0_N` (Dell/IdeaPad), `thinkpad_level` (ThinkPad), `percentage_0_100` (NVIDIA NVML), `cooling_level` (pwm-fan with cooling-levels DT).

   **Recommended:** `pwm_unit` enum on each controller entry. Calibration code dispatches on this.

3. **No off-PWM behaviour field.** `RULE-HWDB-09` (existing per spec-03 amendments) covers stall_pwm_min for `allow_stop`, but doesn't model `falls_to_min` / `falls_to_auto_silently` / `bios_dependent`.

   **Recommended:** `pwm_off_behaviour` enum per controller.

4. **No supported pwm_enable mode set.** Currently implicit. Schema should declare which `pwm_enable` values the driver accepts and what each means.

   **Recommended:** `pwm_enable_modes: { 1: "manual", 2: "auto_thermal", 5: "smart_fan_iv" }` per controller.

5. **No "needs OOT driver" recommendation.** When mainline `nct6683` is loaded and the board has nct6687d coverage, ventd should know to recommend the DKMS install. Currently no schema field.

   **Recommended:** `recommended_alternative_driver: { reason, dkms_repo, install_hint }` per controller.

6. **No "conflicts with userspace daemon" flag.** Steam Deck + jupiter-fan-control. Some Lenovo + LenovoLegionLinux GUI. NBFC-Linux on laptops.

   **Recommended:** `conflicts_with_userspace: ["jupiter-fan-control", "nbfc"]` per board profile.

7. **No "fan tach is real but PWM ignored" modeling.** Some Gigabyte it8689 boards: `pwm` writes accepted by driver, fan doesn't change. HP consumer: read-only by design. Surface Pro: read-only by design.

   **Recommended:** `fan_control_capable: false` flag with `fan_control_via: "<userspace-tool>"` pointer.

8. **No SMM / WMI latency hints.** dell-smm-hwmon polling = audio dropouts on some models. ventd cadence should adapt.

   **Recommended:** `polling_latency_ms_hint: 500` per controller.

9. **No board-firmware-quirk modeling.** `force_id`, `ignore_resource_conflict`, `experimental=1`, `manual=1` — module parameters required for specific board+chip combinations.

   **Recommended:** `required_modprobe_args: ["force_id=0x8689", "ignore_resource_conflict=1"]` per board profile, plus diagnostic detection.

10. **No PWM polarity awareness.** it87 boards may have inverted PWM polarity. Calibration probe must detect empirically.

    **Recommended:** calibration adds `pwm_polarity: normal | inverted` per channel post-probe; schema reserves the field.

---

## 10. Diagnostic bundle implications

Things `ventd diag bundle` must capture / detect for issue triage:

### 10.1 Driver enumeration

For every `/sys/class/hwmon/*`:
- `name` value
- `driver` symlink target → resolves to actual kernel module
- Whether the directory contains `pwm[N]` writable attributes
- All `pwm[N]_enable` current values
- Module parameters (read from `/sys/module/<name>/parameters/*`)
- Loaded since boot vs hot-plugged (modprobe time)

### 10.2 OOT driver detection

For known OOT-fork situations:
- `name=nct6683` + check `/sys/module/nct6687/` or `/sys/module/nct6686/` exists → if not, surface "install nct6687d-dkms" recommendation
- `name=hp_wmi` + check NBFC-Linux installed → if not, surface NBFC recommendation
- Kernel version vs `yogafan` availability (`uname -r` ≥ 7.1)

### 10.3 Conflicting-userspace detection

- `systemctl is-active jupiter-fan-control`
- `systemctl is-active nbfc_service`
- `systemctl is-active fancontrol`
- `pgrep -f "thinkfan|i8kmon|fancontrol"`
- LibreHardwareMonitor or CoolerControl userspace processes

### 10.4 Required-modprobe-args inference

Detect chip-ID-mismatch patterns:
- `dmesg | grep -i "it87.*Found.*chip at"` → if "Found IT86xxE chip" but no `pwm` exposed → likely needs `force_id` + `ignore_resource_conflict`
- `dmesg | grep -i "thinkpad_acpi.*experimental"` → if write fails → needs `experimental=1`
- ACPI resource conflict warnings

### 10.5 Calibration-probe results

Post-calibration, for every channel attempted:
- `polarity: normal | inverted`
- `min_responsive_pwm`, `max_responsive_pwm`
- `stall_pwm`
- `phantom: true` if writes never moved RPM
- `bios_overridden: true` if writes succeeded but were reverted within polling cycle

### 10.6 PWM unit semantic detection

Surface the `pwm_unit` actually in use per channel — `duty_0_255` / `step_0_N` (with N detected) / `thinkpad_level` / `cooling_level` — to disambiguate "ventd thinks this is a duty cycle but it's actually a state index" bug class.

### 10.7 sysfs path resolution

For every controllable channel:
- Full sysfs path
- Stable-after-reboot path (resolved through `/sys/devices/`)
- udev-rule install hint if path is unstable

### 10.8 Kernel module conflict detection

- AIO kernel drivers loaded alongside ventd HID backends (corsair-cpro + ventd Corsair backend = bad)
- Multiple Super I/O drivers loaded simultaneously (rare but possible on hybrid boards)

---

## 11. List of every controllable kernel module name relevant to ventd

For chat 2 to drive per-chip PWM-range research. Each entry needs: pwm range, pwm_enable mode set, off-behaviour, known quirks per common board, conservative default curve.

### 11.1 Desktop / server Super I/O — controllable via mainline

| Module | `name` values | Per-chip research depth |
|---|---|---|
| `nct6775` | nct6106, nct6116, nct6775, nct6776, nct6779, nct6791, nct6792, nct6793, nct6795, nct6796, nct6797, nct6798, nct6799 | 1 catalog entry covers driver; per-chip variation in PWM range minimal |
| `it87` (mainline) | it8603, it8620, it8628, it8665, it8686, it8688, it8689, it8705, it8712, it8716, it8718, it8720, it8721, it8728, it8732, it8771, it8772, it8781, it8782, it8783, it8786, it8790, it8792, it87952 | 1 catalog entry; per-chip mode-set varies (IT8603E lacks pwm_enable=0) |
| `f71882fg` | f71808e, f71808a, f71858fg, f71862fg, f71869, f71869a, f71882fg, f71889fg, f71889ed, f71889a, f8000, f81801u, f81865f | 1 catalog entry; F8000 channel 3 quirk |
| `w83627ehf` | w83627ehf, w83627dhg, w83627uhg, w83667hg, w83667hg_b | 1 catalog entry, legacy |
| `w83627hf` | w83627hf, w83697hf, w83781d, w83782d, w83783s | 1 catalog entry, EOL |
| `smsc47m1` | smsc47m1 | 1 catalog entry, very old |
| `pc87427` | pc87360, pc87427 | 1 catalog entry, very old |
| `vt1211` | vt1211 | 1 catalog entry, very old |
| `dme1737` | dme1737, sch5027, sch311x | 1 catalog entry, very old |

### 11.2 Desktop Super I/O — controllable via OOT only

| Module | `name` values | Source | Notes |
|---|---|---|---|
| `nct6687d` (DKMS) | nct6687 | github.com/Fred78290/nct6687d | RW for what mainline nct6683 RO. msi_alt1 + brute_force module options. |
| `nct6686d` (DKMS) | nct6686 | github.com/s25g5d4/nct6686d | RW for ASRock A620I and similar. Forked from Fred78290. |
| `it87` (frankcrawford fork DKMS) | (same as mainline + IT8606E/IT8607E/IT8613E/IT8622E/IT8625E/IT8655E/IT8696E/IT8698E) | github.com/frankcrawford/it87 | Newer chip support; some Gigabyte boards still BIOS-locked |

### 11.3 Laptop platform drivers — kernel-level

| Module | `name` value | Controllability | Notes |
|---|---|---|---|
| `dell-smm-hwmon` | dell_smm | Whitelist-gated RW, step_0_N units | Per-board firmware bugs documented |
| `hp-wmi-sensors` | hp_wmi (sometimes) | RO | Business class HP only |
| `hp-wmi` | (varies) | RO for fan | Consumer HP — needs NBFC for control |
| `ideapad-laptop` | (varies, often no hwmon) | Limited fan_mode toggle (0/1/2/4) | Pre-7.1 ideapad — pre-yogafan |
| `yogafan` | yogafan | TBD-verify in chat 2 | New 7.1 driver covers Yoga/Flex/Slim/IdeaPad/Legion |
| `thinkpad_acpi` | (no hwmon for fan) | `/proc/acpi/ibm/fan` level 0-7 + named states; `experimental=1` for newer | Distinct PWM unit |
| `asus-nb-wmi` | (varies) | Single-fan PWM on some models | Auto-loads on ASUS laptops |
| `asus_ec_sensors` | asus_ec | RO; fan control via Super I/O instead | ~50 board allowlist |
| `asus_wmi_sensors` | (varies) | RO | Older ROG boards |
| `gpd-fan` | gpd_fan | RW, duty_0_255 | Per-board quirk via module param |
| `surface_fan` | surface_fan | RO | Surface Pro 9 |
| `steamdeck-hwmon` | steamdeck_hwmon | RW, duty_0_255, single fan | Conflicts with jupiter-fan-control |
| `framework-laptop` | (no hwmon for fan) | Userspace fw-ectool only | Cros EC backend needed |
| `acer-wmi` | (varies) | RO mostly | Coverage-poor |
| `msi-ec` | (varies) | Some RW models | OOT for older MSI |

### 11.4 Laptop — userspace EC (kernel doesn't expose)

| Userspace tool | Coverage | Integration option |
|---|---|---|
| `nbfc-linux` | Hundreds of laptop models (Acer, Asus consumer, Dell legacy, HP Pavilion/EliteBook, Lenovo non-IdeaPad, MSI, Toshiba, Xiaomi, Framework) | ventd v1.0: detect + recommend; v1.x: integrate |
| `LenovoLegionLinux` (also kernel module `legion-laptop`) | Lenovo Legion 5/7/Slim/LOQ + Yoga Pro | Legion-specific fan curves + power modes |
| `i8kutils` | Dell legacy | Subsumed by dell-smm-hwmon mostly |
| `fw-ectool` | Framework 13 + 16 | Cros EC direct |
| `mcontrolcenter` | MSI laptops | MSI-specific |

### 11.5 Add-in fan controllers

| Module | `name` value | Coverage |
|---|---|---|
| `adt7475` | adt7473, adt7475, adt7476, adt7490 | Workstation/server boards |
| `max31790` | max31790 | Aquacomputer Octo, chassis splitters |
| `emc2305` | emc2305 | Servers, embedded |
| `amc6821` | amc6821 | Embedded |
| `nct7363` | nct7363 | Server BMC adjacent |
| `nct7802` | nct7802 | Server |
| `nct7904` | nct7904 | Server |
| `lm63`, `lm85`, `lm93` | lm63, lm85, lm93 | Older AMD/server, RW |

### 11.6 Devicetree / SBC

| Module | `name` value | Coverage |
|---|---|---|
| `pwm-fan` | pwmfan or DT-defined | RPi 5, NanoPi, OrangePi, Rockchip/Allwinner SBCs |
| `qnap-mcu-hwmon` | qnap_mcu | QNAP NAS |
| `gxp-fan-ctrl` | gxp_fan | HP ProLiant Gen10 |
| `aspeed-pwm-tacho`, `aspeed-g6-pwm-tach` | aspeed_pwm | ASPEED BMC |
| `npcm750-pwm-fan` | npcm750_pwm_fan | Nuvoton NPCM BMC |

### 11.7 Read-only sources (NOT in `pwm_control` allowlist; signal sources only)

`coretemp`, `k10temp`, `k8temp`, `nvme`, `drivetemp`, `acpitz`, `peci_cputemp`, `peci_dimmtemp`, `iwlwifi`, `amdgpu` (read-only via hwmon — the PWM control path is via amdgpu+sysfs's separate write attrs, not through standard hwmon `pwm`), `nouveau`, `acpi_power_meter`, `BAT0`, `AC`, `corsair_psu`, `fsp3y`, `jc42`, `lm75`, `tmp102`, `ds620`, `sht3x`, etc.

---

## 12. Open questions for chat 2

These need to resolve before chat 2 writes the per-chip catalog:

### 12.1 Schema: minimal vs comprehensive amendment

Two paths:

- **(a) Minimal amendment.** Add `pwm_unit`, `pwm_enable_modes`, and `recommended_alternative_driver` to schema v1 (we're still pre-PR 2 so schema v1 isn't truly frozen for matcher consumers). Defer the rest to v2.
- **(b) Full amendment.** Encode all 10 schema gaps from §9 now. Schema v1 frozen with everything. PR 2 matcher consumes a richer model.

(b) is more work but matches Phoenix's "this is the defining feature, don't cheap out" stance. (a) ships faster but risks v2 migration during the ramp to v1.0.

### 12.2 NBFC strategy — formalise as A/B/C/D

Pick the v1.0 stance for NBFC-coverable laptops. Recommendation in §3.10 was D (detect + recommend, don't control). Confirm and bake into spec.

### 12.3 GPU vendor catalog — separate spec or appendix to spec-03?

The B-track GPU catalog (NVIDIA via NVML, AMD via AMDGPU sysfs, Intel future) is its own research domain. Probably a spec-03b or amendment-on-spec-03. Decide structure before chat 2 writes the GPU catalog.

### 12.4 Board profile vs chip profile — explicit

PR 1 schema has board-fingerprint entries that reference `pwm_control: <chip>`. After this map: the board entry needs to override chip defaults for board-specific quirks (Gigabyte it8689 BIOS-locked, MSI msi_alt1 register layout, ASUS CPUTIN-floats). Schema needs an explicit "inheritance" concept — board entry inherits from chip generic, overrides specific fields.

### 12.5 Diagnostic bundle scope — confirm scope for PR 2c

§10 lists ~40 things the bundle should capture. Confirm whether PR 2c ships all of this or a subset, with the rest landing in spec-04/05 / a future polish PR.

### 12.6 Matcher tier-3 fallback when controllability is `false`

If `name=hp_wmi` matches tier-3 and the entry says `fan_control_capable: false`, what does ventd do?

- Refuse to install? (Bad — user should still get sensor monitoring.)
- Install in monitor-only mode? (Honest — predicts nothing yet.)
- Install + recommend NBFC? (Best UX — but requires the recommendation flow.)

The right answer probably depends on the `recommended_alternative` path being in place. Confirm UX before PR 2a writes the matcher result-handling.

### 12.7 The `pwm_control` allowlist becomes `name` allowlist

Per §1.2. RULE-HWDB-05 currently named "kernel module name allowlist" should be renamed to "hwmon `name` value allowlist." Existing entries map cleanly except OOT cases (`nct6687`, `nct6686`, `legion_hwmon` need adding). Confirm rename + chat 2 writes the canonical list.

---

## 13. Recommended chat 2 workflow

Given this map, chat 2 should:

1. **Opus consult on schema amendments** (§9 + §12.1 + §12.4) — ~30 min. Output: `spec-03-amendment-pwm-controllability.md` with frozen schema decisions.
2. **Per-chip PWM-range research** — Sonnet-driven web fetches + datasheet skims. ~$5-10. One catalog entry per `name` value covered above. Output: `2026-04-hwmon-generic-catalog.md`. Per-entry shape: `{name, controllability, pwm_unit, pwm_enable_modes, off_behaviour, conservative_curve, citations}`.
3. **GPU vendor catalog research** — separate, ~$5-8. Output: `2026-04-gpu-vendor-catalog.md`.
4. **Diagnostic bundle prior-art research** — covers §10 with citations. ~$5-8. Output: `2026-04-diagnostic-bundle-design.md`.
5. **Privacy threat model** — covers what can leak through DMI / hostname / labels / sysfs paths in a bundle. ~$3-5. Output: `2026-04-diag-privacy-threat-model.md`.
6. **Three CC prompts** (PR 2a matcher + catalog, PR 2b calibration probe, PR 2c diagnostic bundle) — ~$0 in CC, ~25k tokens of writing.

Total chat 2 estimated tokens: 80-120k. Healthy.

---

## 14. Citations

Kernel docs (definitive for in-tree drivers):
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
- hwmon index (full driver list): https://docs.kernel.org/hwmon/index.html

Out-of-tree drivers:
- nct6687d (Fred78290): https://github.com/Fred78290/nct6687d
- nct6686d (s25g5d4): https://github.com/s25g5d4/nct6686d
- it87 (frankcrawford): https://github.com/frankcrawford/it87
- it87 (a1wong, older): https://github.com/a1wong/it87
- LenovoLegionLinux: https://github.com/RoyChong5053/LenovoLegionLinux-LOQ (forked from main project; main repo at https://github.com/johnfanv2/LenovoLegionLinux)

Userspace daemons:
- NBFC-Linux: https://github.com/nbfc-linux/nbfc-linux
- NBFC original: https://github.com/hirschmann/nbfc
- jupiter-fan-control: SteamOS proprietary; reverse references at github.com/Jovian-Experiments
- thinkfan: https://github.com/vmatare/thinkfan
- fan2go: https://github.com/markusressel/fan2go
- CoolerControl: https://gitlab.com/coolercontrol/coolercontrol

News / context:
- Linux 7.1 yogafan announcement: https://www.phoronix.com/news/Linux-7.1-HWMON
- MSI fan control on Linux walkthrough: https://gabmus.org/posts/msi-motherboard-fan-control/
- IT87 Gigabyte B450M fix walkthrough: https://wiki.tnonline.net/w/Blog/IT87_driver_for_the_it8686_sensor_chip
- ArchWiki Fan speed control: https://wiki.archlinux.org/title/Fan_speed_control

Issue trackers consulted:
- nct6687d issue #119 (Proxmox 6.11 nct6683 perm denied → install nct6687d)
- frankcrawford/it87 #68 (Gigabyte B650 Aorus Elite AX BIOS-locked)

---

**End of map.** This is the input chat 2 needs. Per-chip PWM-range research in chat 2 has a schema-aware target now; diagnostic bundle research has a defined detection surface; CC prompts have a structural foundation that won't be invalidated by mid-implementation discovery.
