# R2 — Ghost hwmon PWM Entry Taxonomy for ventd

> Research item R2 in the ventd specification series (spec-v0_5_1-catalog-less-probe.md). R1 (virtualization/container detection) gates whether ventd writes at all; R2 governs **which** `/sys/class/hwmon/hwmonN/pwmM` entries get written once writes are allowed. This document catalogs the eight known failure patterns for "ghost" PWM entries on Linux, with kernel source citations, real-world bug references, and a defensible multi-stage classification pipeline.

---

## 1. Executive Summary

The Linux `hwmon` sysfs interface presents `pwmM` files as a uniform abstraction, but in practice the relationship between a sysfs PWM file and a physical fan is mediated by a long chain of conditional realities: Super-I/O chip register layout, motherboard pin wiring (which is invisible to software), BIOS/EC firmware policy, kernel driver coverage, and platform vendor mediation. ventd, as a catalog-less daemon for unknown hardware, must treat every freshly-discovered `pwmM` entry as a hypothesis rather than a fact.

Eight distinct failure modes ("ghost patterns") have been identified through review of kernel driver source (`drivers/hwmon/nct6775-*`, `drivers/hwmon/it87.c`, `drivers/hwmon/dell-smm-hwmon.c`, `drivers/hwmon/asus-ec-sensors.c`, `drivers/hwmon/asus_wmi_sensors.c`, `drivers/hwmon/hp-wmi-sensors.c`, `drivers/platform/x86/thinkpad_acpi.c`), the lm-sensors `pwmconfig` tool's correlation heuristic and its known bug history, fan2go and CoolerControl issue trackers, and ASUS/Dell/HP/Gigabyte/MSI user reports. The patterns range from mostly auto-excludable (zero-tach, EINVAL on enable, write-ignored after correlation sweep) to "requires user judgment" (BIOS-fight on a CPU fan, mismatched-index where the wrong fan responds, platform-mediated channels that may work later after a firmware update).

The recommended ventd strategy is a four-stage pipeline run only after R1 has authorized writes: (1) cheap structural sanity, (2) write-and-read-back probe with `pwm_enable` round-trip, (3) tach-correlation sweep with extended settle time (≥30 s minimum, à la the `fan2go` fix in markusressel/fan2go#28), and (4) BIOS-fight detection by re-reading `pwm_enable` and the written `pwm` value at 5 s, 10 s, and 30 s. Each stage is a kill-gate; entries that pass all four are admitted to the calibration set, entries that fail are tagged with the specific pattern and either skipped, warned, or escalated for explicit user override.

Because ventd's primary audience is homelab / NAS / small-server users who care more about not undercooling silent NAS arrays than about wringing out the last 2 % of a phantom Super-I/O channel, the **bias is toward false-positive exclusion (skipping a possibly-real entry) over false-negative inclusion (calibrating against a phantom)**, with a mandatory user-override path for any entry the pipeline rejects. The cost of a wrong "include" is bad calibration data poisoning the entire fleet baseline; the cost of a wrong "exclude" is a single config-file flag.

---

## 2. The Eight Patterns

### 2.a — ZERO-TACH PATTERN

**Description.** A `pwmM` file exists in the hwmon directory, but either (a) no corresponding `fanM_input` exists, or (b) `fanM_input` exists and reads `0` (or stays pinned at a constant value) regardless of what `pwmM` is set to.

**Detection signal.**
1. Enumerate sibling files in the same `hwmonN/` directory. If `pwmM` exists with no `fanM_input` *and* no other `fanK_input` shows a delta when `pwmM` is varied, the channel is dead.
2. Read `fanM_input` at PWM=255 (full) and at PWM=64 (low). If both reads return `0`, or if the absolute delta is below a noise floor (typically <50 RPM), tach is non-functional.
3. The lm-sensors `pwmconfig` tool's "speed was X now Y, no correlation" output is the canonical realization of this signal — see `frankcrawford/it87` issue #11 (Gigabyte B560M DS3H, IT8689E) where `fan2_input current speed: 0 ... skipping!` is logged for several headers even though the chip exposes 5 PWM channels (https://github.com/frankcrawford/it87/issues/11).
4. A 3-pin DC fan plugged into a header configured for PWM mode often produces this signature when the BIOS has the header set to "Voltage/DC" mode but the kernel driver presents PWM sysfs files unchanged.

**Driver families known to exhibit it.**
- `nct6775` family — Nuvoton chips advertise `pwm1..pwm5` (and on `nct6796`/`nct6798` up to `pwm7`) but only the channels actually wired to a header on the board are useful. The driver itself iterates `data->pwm_num` and registers attributes for the maximum hardware-supported set, not the board-populated set (see `drivers/hwmon/nct6775-core.c` and the `enum kinds` declaration in `drivers/hwmon/nct6775.h`: `nct6106, nct6116, nct6775, nct6776, nct6779, nct6791, nct6792, nct6793, nct6795, nct6796, nct6797, nct6798, nct6799` — https://github.com/torvalds/linux/blob/master/drivers/hwmon/nct6775.h).
- `it87` family — `it8613`, `it8665`, `it8688`, `it8689`, `it8772`. The Proxmox forum thread (https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/) shows `hwmon4/fan1_input current speed: 0 ... skipping!` for fan1, fan2, fan4, fan5 on a board where only one header is wired — a quintessential zero-tach manifestation.
- `thinkpad_acpi` — `fan2_input` exists on hwmon directory but reads 0 on ThinkPads where the secondary fan is not physically installed. This is documented explicitly: *"hwmon device attribute fan2_input: Fan tachometer reading, in RPM, for the secondary fan. Available only on some ThinkPads. If the secondary fan is not installed, will always read 0"* (Documentation/admin-guide/laptops/thinkpad-acpi.rst, lines 1285–1288, https://www.kernel.org/doc/Documentation/laptops/thinkpad-acpi.txt).

**Severity.** **Auto-excludable.** A channel that does not move any tach in the system has no observable closed-loop function and cannot be calibrated. Skipping it is safe.

**Mitigation.** Skip silently after the correlation sweep records no delta on any `fanK_input` over a full `[0, 128, 255]` PWM cycle with ≥10 s settle per step.

---

### 2.b — WRITE-IGNORED PATTERN

**Description.** Writing a value to `pwmM` succeeds (no `EINVAL`/`EIO`), reads back the written value, `pwm_enable` transitions correctly to manual mode (1) and stays there, and yet no observable RPM change occurs and there is no thermal effect. The hardware register is being written but is not gated to the physical fan output.

**Detection signal.**
1. Set `pwmM_enable=1`, write `pwm=0` (or `pwm=64`), wait ≥30 s, read `fanM_input` and any other `fanK_input` in the same hwmon. If no fan shows a >50 RPM delta from the pre-write baseline, the write is ignored.
2. Cross-check by writing `pwm=255`. If still no delta, this is write-ignored, not just "fan was already at max".
3. Distinguish from BIOS-fight (2.e): re-read `pwm_enable`. If it is still `1` (manual) and the fan still doesn't respond, this is write-ignored. If it has reverted to `2`, that is BIOS-fight.

**Driver families known to exhibit it.**
- `it87` on certain Gigabyte AM5/X670E boards with **IT8689E revision 1**. See https://github.com/frankcrawford/it87/issues/96: *"On the IT8689E, writing to PWM registers is accepted without error but has zero effect on actual fan speed. All 5 PWM channels behave the same way."* The user identifies that revision 2 of the same chip on a B650 Eagle AX board does work, narrowing the bug to silicon revision 1.
- `nct6775` family on motherboards where a `pwm_mode` mismatch exists — i.e. the header is wired for DC voltage control but `pwmM_mode=1` (PWM output) is selected. Writes go to the PWM duty-cycle register but the output stage is in DC mode (cf. nct6775 documentation, sysfs attributes section: *"pwm[1-7]_mode: 0 DC output, 1 PWM output"* — https://docs.kernel.org/hwmon/nct6775.html).
- `it87` IT8613E on some Topton N150 NAS boards where `pwmconfig` reports *"Manual control mode not supported, skipping"* even though manual writes echo correctly — see https://github.com/frankcrawford/it87/issues/97.

**Severity.** **Auto-excludable**, but with a caveat: write-ignored is indistinguishable from "fan is broken or unplugged but tach is somehow stuck at idle" without external information. A warning log entry is appropriate.

**Mitigation.** Skip after a confirmed full-range sweep with no observed RPM movement. Log at WARN level so the user can investigate (chip rev, BIOS PWM/DC mode setting) if they expected this header to work.

---

### 2.c — PHANTOM-CHANNEL PATTERN

**Description.** Super-I/O chips advertise the full per-chip channel count regardless of which channels the motherboard actually wires to physical fan headers. The kernel driver registers `pwm1`..`pwmN_max` for the chip's maximum PWM count, and only the subset wired up by the OEM is functional.

**Detection signal.**
1. Driver name + chip model implies a maximum advertised PWM count, but observed real channels are typically 2–4. Compare:
   - `nct6776`: 3 PWM
   - `nct6779`: 5 PWM (reference comment in nct6775-core.c: *"nct6779d 15 5 5 2+6"*, https://github.com/OpenNuvoton/NUC970_Linux_Kernel/blob/master/drivers/hwmon/nct6775.c)
   - `nct6791`/`nct6792`/`nct6793`/`nct6795`/`nct6796`/`nct6798`/`nct6799`: 5–7 PWM
   - `it8613`: hardware supports 3 fans; `it87` driver still publishes pwm2/pwm3/pwm5 sysfs attributes (gap in numbering — see Proxmox forum thread above)
   - `it8665`/`it8688`/`it8689`: 5 PWM
2. The lm-sensors `pwmconfig` "no correlation" output is the empirical realization. See https://github.com/lm-sensors/lm-sensors/issues/459 and the canonical text: *"There is either no fan connected to the output of hwmon3/pwm3, or the connected fan has no rpm-signal connected to one of the tested fan sensors. (Note: not all motherboards have the pwm outputs connected to the fan connectors)"*.
3. nct6775-core.c uses a `data->has_pwm` bitmask (https://www.uwsg.indiana.edu/hypermail/linux/kernel/1302.3/00988.html, line: *"if (!(data->has_pwm & (1 << i))) continue;"*), but the bitmask is set from chip capability, not from board population — there is no SMBIOS/DT mechanism by which the driver could know which Nuvoton output pins go to which CHA_FAN header on a given motherboard.

**Driver families known to exhibit it.** All Super-I/O families: `nct6775`, `nct6776`, `nct6779`, `nct6791d`, `nct6792d`, `nct6793d`, `nct6795d`, `nct6796d`, `nct6797d`, `nct6798d`, `nct6799d`, `it8728`, `it8665`, `it8688`, `it8689`, `it8772`, `it8613`, `it8622`. The phantom-channel rate on consumer motherboards is approximately (advertised − wired) / advertised, which empirically averages 30–50 % phantoms (e.g. an nct6798d-equipped budget board with 3 chassis fan headers presents 4 phantom PWMs).

**Severity.** **Auto-excludable** (this is the Pareto-largest source of ghosts). It is the dominant pattern on desktop/NAS/Proxmox hardware.

**Mitigation.** Tach correlation sweep (Stage 3 of the pipeline below) is the only reliable filter. Skip channels with no tach correlation. Do **not** distinguish 2.a (zero-tach) from 2.c (phantom-channel) at admission time — they have the same defensible disposition (skip).

---

### 2.d — EINVAL/ERROR PATTERN

**Description.** Some kernel drivers reject writes outside a chip-specific or platform-specific value set with `-EINVAL` or `-EIO`. The classic case is Dell's SMM interface, which only accepts a handful of "fan speed states" (0/85/170/255 maps to states 0/1/2/3 after the `i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)` rounding), not arbitrary 0–255 values. ASUS WMI is even more restrictive.

**Detection signal.**
1. Write a "weird" non-modulo value such as `pwm=137` to a freshly-opened channel. Read back. If the kernel returned `-EINVAL`/`-EIO` the write fails immediately.
2. If the write succeeds but the read-back returns a value other than what was written (e.g. `137` written, `170` read back), this is a stepped-value driver. Dell SMM exhibits exactly this: see misieck's report in https://github.com/lm-sensors/lm-sensors/pull/383 — *"the pwm values written get rounded to one of three possible values: 85 (low setting) 170 (high setting) 255 (auto? medium? setting)"*.
3. For `pwm_enable`, `dell-smm-hwmon` only accepts values 1 (manual) and 2 (auto); anything else returns `-EINVAL`. Source: `drivers/hwmon/dell-smm-hwmon.c` `case hwmon_pwm_enable` switch (https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c): `case 1: enable = false; break; case 2: enable = true; break; default: return -EINVAL;`.
4. ThinkPad ACPI: `pwm_enable` accepts {0, 1, 2} ({offline, manual, EC-auto}); modes 0 and 2 are not supported on all ThinkPads and return `-EINVAL` on unsupported boards. Documentation/admin-guide/laptops/thinkpad-acpi.rst lines 1261–1267.
5. ASUS WMI: writes to `pwm1` reportedly fail with `Invalid argument`; only `pwm1_enable` mode transitions ({0, 1, 2}) work — the per-laptop `asus-nb-wmi` interface is essentially a 3-state switch. See https://bbs.archlinux.org/viewtopic.php?id=295764: *"`echo 255 > .../pwm1` … `bash: line 1: echo: write error: Invalid argument`"*.

**Driver families known to exhibit it.**
- `dell-smm-hwmon` (i8k): stepped values via `i8k_pwm_mult = DIV_ROUND_UP(255, i8k_fan_max)`. Default `i8k_fan_max=2` ⇒ values {0, 128, 255}; `fan_max=3` ⇒ {0, 85, 170, 255}. Dell Optiplex XE2: only {0, 1000 RPM, 5200 RPM} states are exposed.
- `thinkpad_acpi`: pwm1 maps to fan levels 0–7 via `pwm1 / 36 ≈ level`; some quantization but generally accepts 0–255. The firmer EINVAL constraint is on `pwm1_enable` modes 0 and 2.
- `asus-nb-wmi`: only `pwm_enable` writes succeed; `pwm` writes fail with EINVAL on most laptops.
- Some Lenovo Legion laptops via legion-laptop / `ideapad_laptop` exposing only 4–5 fan profile states.
- `dell-smm-hwmon` legacy `i8k_get_fan_status` interface returning EIO sporadically — see Pali Rohár's patch series (https://groups.google.com/g/linux.kernel/c/WfidNnGV31k) about broken Dell SMM machines (Inspiron 3505, Precision M3800, Vostro 1720) where `i8k_get_fan_type` *causes severe sideeffects* (kernel hang) and is now blacklisted via DMI tables.

**Severity.** **Auto-classifiable, requires special-case write strategy.** Not "exclude"; rather "constrain output domain". A stepped-value channel is still a real, controllable fan — just one with a discrete set of legal duty-cycle values.

**Mitigation.**
1. On EINVAL during the write-and-read-back probe, run a probe of canonical values: {0, 64, 85, 128, 170, 192, 255}. Record which subset is accepted.
2. If the accepted set is a small finite set, mark the channel `quantized` and store the legal values as the discretization grid.
3. If `pwm_enable` only accepts {1, 2}, store that and never attempt mode 0 (full-speed) or 3.
4. Refuse to calibrate intermediate fan curves on quantized channels; instead use the discrete states directly.

---

### 2.e — BIOS-FIGHT PATTERN

**Description.** Userspace sets `pwm_enable=1` (manual) and writes a desired PWM. Within a few seconds the BIOS / EC firmware writes back `pwm_enable=2` (automatic) or directly clobbers the `pwm` register, restoring vendor fan-curve control. The userspace daemon and the EC are racing for the same register.

**Detection signal.**
1. Set `pwm_enable=1`, write `pwm=64`. Re-read both at T+5 s, T+10 s, and T+30 s.
2. If `pwm_enable` reverts to 2 (or to a non-1 value): BIOS-fight on the enable bit.
3. If `pwm_enable` stays at 1 but `pwm` reads back as something other than 64 (and no userspace process wrote it): BIOS-fight on the duty cycle register.
4. fan2go has a built-in detector for this: *"PWM of Front-02 was changed by third party! Last set PWM value was: 249 but is now: 250"* (https://github.com/markusressel/fan2go/issues/64). The corresponding config knob is `pwmValueChangedByThirdParty.enabled` and `fanModeChangedByThirdParty.enabled` (https://github.com/markusressel/fan2go).
5. lm-sensors `fancontrol` simply ignores BIOS-fight and ends up oscillating.

**Driver families known to exhibit it.**
- `dell-smm-hwmon` is the canonical case. The kernel documentation says it directly: *"On some laptops the BIOS automatically sets fan speed every few seconds. Therefore the fan speed set by mean of this driver is quickly overwritten."* (https://docs.kernel.org/hwmon/dell-smm-hwmon.html). The `dell-bios-fan-control-git` AUR package exists explicitly to disable this BIOS override on Dell XPS 9560 and similar laptops (https://github.com/TomFreudenberg/dell-bios-fan-control).
- HP business / Omen / Compaq desktops via embedded controller — fan control is locked in the EC and sysfs writes through `hp-wmi-sensors` are ignored (since `hp-wmi-sensors` is *read-only* by design — see 2.f).
- ThinkPad EC: the watchdog mechanism is the *opposite* of BIOS-fight (it reverts to safe state if userspace stops writing); but on some ThinkPads the EC will silently revert from manual to auto on undocumented thermal triggers. The kernel doc warns: *"the driver is not always able to detect this"* for `pwm1_enable=2` mode 2 (https://www.kernel.org/doc/Documentation/laptops/thinkpad-acpi.txt).
- Lenovo Legion laptops with stock BIOS — the Legion EC is aggressive about reasserting auto mode.
- Dell PowerEdge servers with iDRAC — iDRAC's thermal algorithm runs independently and overrides any kernel-side write within seconds. The fix is BIOS setting "Thermal Profile → Custom" or use of `ipmitool raw 0x30 0x30 0x01 0x00` to disable iDRAC fan control.
- Some Gigabyte/ASUS BIOSes with "Smart Fan" Q-Fan enabled at boot — they assert auto mode on resume from S3 or on AC/battery transitions.

**Severity.** **Requires user judgment.** BIOS-fight on the CPU fan is dangerous to override (you can disable thermal protection); BIOS-fight on a chassis fan is usually safe to override (or to fight back at higher frequency than the EC's polling rate, typically 1–2 Hz).

**Mitigation.**
1. Detect at T+5/10/30 s as above.
2. If BIOS-fight is detected, **do not** silently keep writing — log clearly and either:
   - Skip the channel and emit a `BIOS_FIGHT` diagnostic recommending the user check BIOS settings (Q-Fan disable / Smart Fan disable / Thermal Profile → Custom / iDRAC override).
   - Or, if the user has set a `bios_fight_override: true` flag in config, write at a frequency higher than the EC polling rate (typically 250 ms) and accept the increased SMI/SMM overhead. Never enable this by default.
3. For Dell laptops specifically, document the `dell-bios-fan-control` workaround and the i8k_whitelist_fan_control DMI table requirement.

---

### 2.f — PLATFORM-MEDIATED PATTERN

**Description.** PWM sysfs files exist but the underlying transport requires a platform driver (WMI/ACPI/SMM/EC) that is partial or read-only. Raw register writes to the Super-I/O may collide with the EC; vendor drivers expose a sysfs surface that looks normal but is in fact severely restricted or read-only.

**Detection signal.**
1. Read `/sys/class/hwmon/hwmonN/name`. If it matches one of: `dell_smm`, `asus_wmi_sensors`, `asus_ec_sensors`, `asus-nb-wmi`, `hp-wmi-sensors`, `thinkpad`, `nbsmi`, `ideapad_laptop`, `legion-laptop`, treat the entry as platform-mediated until proven otherwise.
2. Cross-reference with `/sys/devices/platform/<driver>/hwmon/hwmonN/`. Platform-driver-backed hwmons are under `/sys/devices/platform/...` rather than `/sys/devices/pci...`.
3. The presence of `pwmM_enable` without `pwmM` is the canonical "look but don't touch" signal — see https://bbs.archlinux.org/viewtopic.php?id=258679: *"the pwm1 file does not exist. The pwm1_enable file however, exists, and it is set to 2"* on an Asus G550JK with `asus-nb-wmi`.
4. `hp-wmi-sensors` does not register **any** PWM attributes — only `fan[X]_input` (RPM read), `fan[X]_label`, `fan[X]_fault`, `fan[X]_alarm` (https://docs.kernel.org/hwmon/hp-wmi-sensors.html). No PWM control surface exists at all.
5. `asus-ec-sensors` similarly is *read-only*: it exposes Chipset/CPU/Motherboard/T_Sensor/VRM/Water In/Water Out temperatures and CPU Optional / Chipset / VRM heatsink / Water Flow fan RPMs, but no PWM (https://github.com/torvalds/linux/blob/master/drivers/hwmon/asus-ec-sensors.c, file header comment listing what EC provides).
6. `asus_wmi_sensors` (the X370/X470/B450/X399 Ryzen WMI driver, electrified/asus-wmi-sensors and the in-tree port at `drivers/hwmon/asus_wmi_sensors.c`) is *also read-only*: from the upstream README, *"No, fan control is not part of the Asus sensors WMI interface. It may be possible via an undocumented method, but that would require reverse engineering effort."* (https://github.com/electrified/asus-wmi-sensors).

**Driver families known to exhibit it.**

| Driver | Surface | Writable? | Notes |
|--------|---------|-----------|-------|
| `dell-smm-hwmon` | pwm1..pwmN, pwm1_enable | partial (stepped, EINVAL on unsupported, often BIOS-fight) | See 2.d, 2.e. Whitelist-gated by `i8k_whitelist_fan_control` DMI table. |
| `hp-wmi-sensors` | fan only, **no PWM attributes** | read-only | https://docs.kernel.org/hwmon/hp-wmi-sensors.html |
| `asus_wmi_sensors` | sensors (incl. fan RPM), **no PWM** | read-only | X370/X470/B450/X399 Ryzen only |
| `asus-ec-sensors` | EC sensors (temps + RPM), **no PWM** | read-only | Modern ASUS desktop boards |
| `asus-nb-wmi` | pwm1, pwm1_enable | enable-only on most models; pwm1 EINVAL on writes | Laptops |
| `thinkpad_acpi` | pwm1, pwm1_enable, fan_watchdog | yes (with watchdog and EINVAL constraints) | Mode 0 and 2 unsupported on some |
| `nct6775` | pwm1..pwm7 | yes (Super-I/O direct) | EC may collide on prebuilts |
| `it87` | pwm1..pwm5 | yes (Super-I/O direct) | EC may collide on Gigabyte/MSI prebuilts; out-of-tree driver `frankcrawford/it87` for newer chips |
| `nct6683` | pwm | read-only by default | Some MSI boards expose nct6687 read-only via mainline; out-of-tree `nct6687d` driver adds writes |
| `corsaircpro` | pwm1..pwm6 (USB HID) | yes, but `pwm_enable` not exposed | https://github.com/markusressel/fan2go/issues/63 |

**Severity.** **Mostly auto-classifiable** (pure read-only drivers like `hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors` have no `pwm*` files, so they are caught by structural Stage 1). The trickier subset is `dell-smm-hwmon`, `asus-nb-wmi`, and `thinkpad_acpi`, which have *partial* writability.

**Mitigation.**
1. Stage 1 catches read-only drivers automatically (no `pwm*` file exists).
2. For known platform-mediated driver names, prepend the driver to a `platform_quirk` table that pre-sets the probe expectations:
   - `dell_smm` → expect stepped values, BIOS-fight likely
   - `thinkpad` → expect EINVAL on modes 0/2, fan watchdog timer required
   - `asus-nb-wmi` → expect EINVAL on raw `pwm` writes, only `pwm_enable` works
3. Probe with these expectations — if the channel passes the constrained probe, admit it as "platform-mediated" and store the discovered legal value set.
4. For ThinkPads, set `fan_watchdog` to 120 (the maximum) on admission so the EC does not panic-revert on transient watchdog timeouts.

---

### 2.g — TACH-WITHOUT-PWM PATTERN

**Description.** A `fanK_input` exists but no corresponding `pwmM` exists in the directory — i.e. the fan is monitored but not controlled by this hwmon device. This is the inverse of zero-tach.

**Detection signal.**
1. Enumerate `fanK_input` files. For each, check whether a `pwmM` file exists where `M` matches `K`, OR whether any `pwmM` in the same hwmon controls the fan (Stage 3 correlation can establish this even with mismatched indices — see 2.h).
2. If `fanK_input` exists and **no** `pwmM` is found in the same hwmon, this is a read-only fan from the perspective of this driver.
3. Common case: GPU fans (`amdgpu`, `nvidia` via NVML) often expose `fan1_input` with no companion `pwm1` because GPU fan control on Linux goes via different sysfs (`/sys/class/drm/card*/device/hwmon/...` with `pwm1_enable` requiring `amdgpu.ppfeaturemask` overrides for some GPUs).
4. Power-supply hwmons (`corsairpsu`, `nzxt-smart2`) often expose only fan RPM input.
5. ThinkPad `fan2_input` exists for read-only secondary fan monitoring even though only `pwm1` controls the (shared) main+secondary fan loop.
6. The fan2go README addresses this: *"By default, fan2go guesses and sets the pwm channel number for a given fan to the fan's RPM sensor channel"* — implying the user must explicitly configure when no pwm exists.

**Driver families known to exhibit it.**
- `amdgpu`, `nvidia` (older), `radeon` — GPU fans monitored at the platform level
- `drivetemp` — read-only (no fan at all, only HDD temps; relevant because it appears in hwmon enumeration as a temp-only device — same family as 2.h)
- `corsaircpro` (Corsair Commander Pro) when `pwm_enable` is not exposed for the channel — see https://github.com/markusressel/fan2go/issues/64
- `nzxt-smart2`, `nzxt-kraken3` — some channels are pump-RPM-only
- Server BMC fans exposed via `ipmi` or `aspeed-pwm-tacho` (in some configurations only the RPM is exported)
- `thinkpad_acpi` `fan2_input` (covered above)

**Severity.** **Auto-excludable from control set, retain for monitoring.** A tach-only fan is an information source, not a control target.

**Mitigation.** Add `fanK_input` to the temperature-correlation graph as a *symptom* (e.g. for diagnosing whether ventd's actions on a different `pwmM` are working), but do not include it in the control-target set.

---

### 2.h — MISMATCHED-INDEX PATTERN

**Description.** `pwmM` controls a fan whose tach signal appears on `fanK_input` where `M ≠ K`. The kernel exposes a uniform numbering but the actual cross-wiring inside the chip and the board is arbitrary.

**Detection signal.**
1. Stage 3 correlation must scan **all** `fanK_input` in the hwmon, not just `fan{M}_input`, for each `pwmM`. This is exactly what lm-sensors `pwmconfig` does (https://man.archlinux.org/man/extra/lm_sensors/pwmconfig.8): *"When a connection is established between a PWM control and a fan, pwmconfig can generate a detailed correlation, to show how a given fan is responding to various PWM duty cycles."*
2. Empirical example from https://iandw.net/2014/10/12/fancontrol-under-ubuntu-14-04: *"I can see that only pwm2 controls any fans, and they are fans 1, 4 and 5."* — pwm2 controls a 3-fan group with tach signals reporting on entirely different indices.
3. A historic lm-sensors bug (ticket #2380, https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly) revealed that `pwmconfig` could miss correlations when it disabled `pwmM_enable` after the first fan match and never re-enabled, causing `pwm2 = fan2` to be missed even though it was real. ventd must enable each PWM independently and **scan all fans for each PWM** before disabling.
4. Daisy-chained hub case: a single PWM signal feeds a 4-port hub with 4 fans, but the hub returns only one tach signal. In this case `pwm1` legitimately controls 4 fans but only one `fanK_input` shows the delta.

**Driver families known to exhibit it.** Common on Gigabyte and MSI consumer boards using `nct6798d`, `nct6796d`, `it8689e`, and `it8688e`, where the manufacturer routes the chip's PWM_n pin to whichever CHA_FANn header gives the best PCB layout, not to the conventionally-numbered one. Anecdotally:
- Gigabyte B650/X670 boards with `it8689e` — frequent 1↔3 swap
- MSI MAG/MEG boards with `nct6687d` — fan numbering in BIOS often differs from kernel sysfs ordering (and `nct6687d` is a separate out-of-tree driver from frankcrawford/it87 family)
- Older ASUS boards with `nct6776`/`nct6779` — some asus-isa-0290 vs nct chip dual-exposure cases produce duplicate but mismatched indices

**Severity.** **Auto-resolvable** if Stage 3 is implemented correctly (scan all fans per PWM, record the best-correlated fan index as the canonical pair). **Requires user judgment** only when (a) multiple fans correlate (PWM hub case) and the user wants per-fan alarming, or (b) two PWMs both correlate strongly with the same fan (unlikely but seen on AIO pump+fan splitters).

**Mitigation.** Stage 3 must record the correlation matrix `M[pwm, fan] = ΔRPM/ΔPWM` and select the maximum-correlation fan per PWM, not assume `pwmK ↔ fanK_input`. If the maximum-correlation fan is not `fanM_input`, store the discovered mapping in the calibration record and use it for closed-loop control. Emit a warning if the discovered index differs from the naive index, so users can sanity-check it.

---

## 3. Taxonomy Table

| # | Pattern | Detection Signal | Driver Examples | Severity | Auto-Excludable? | Mitigation |
|---|---------|------------------|-----------------|----------|-------------------|------------|
| a | ZERO-TACH | No `fanM_input`, or fan reads 0 across full PWM sweep | nct6775, it87, thinkpad_acpi (fan2) | Low | **Yes** | Skip silently |
| b | WRITE-IGNORED | PWM accepts writes, reads back, no RPM/thermal effect | it87 IT8689E rev1, nct6775 with DC-mode mismatch | Medium | **Yes** | Skip; log WARN with chip rev |
| c | PHANTOM-CHANNEL | Channel count > wired headers; no correlation on sweep | All Super-I/O (nct679x, it87xx) | Low | **Yes** | Skip; subsumed by Stage 3 |
| d | EINVAL/STEPPED | Write rejected with EINVAL or read-back ≠ written value | dell-smm-hwmon, asus-nb-wmi, thinkpad_acpi | Medium | **No** (channel is real, just constrained) | Probe legal value set; store as quantized |
| e | BIOS-FIGHT | `pwm_enable` reverts at T+5/10/30 s, or `pwm` clobbered | dell-smm-hwmon, HP EC, Lenovo Legion EC, Dell iDRAC | High | **No** (user judgment) | Skip + diagnostic; require explicit `bios_fight_override` |
| f | PLATFORM-MEDIATED | Driver name in known platform list; partial/no PWM surface | hp-wmi-sensors (none), asus-ec-sensors (none), asus_wmi_sensors (none), asus-nb-wmi (partial), dell-smm-hwmon (stepped), thinkpad_acpi (constrained) | Variable | Read-only: **Yes**; Partial: **No** | Use platform_quirk table |
| g | TACH-WITHOUT-PWM | `fanK_input` exists, no `pwmM` in dir | amdgpu, drivetemp, corsairpsu, thinkpad_acpi (fan2), nzxt-smart2 | Low | **Yes** (from control set) | Retain for monitoring only |
| h | MISMATCHED-INDEX | Sweep correlation peak on `fanK_input` where `K ≠ M` | Gigabyte/MSI nct6798d, it8689e | Low | **Yes** (auto-remapped) | Record best-correlation pair; warn on mismatch |

---

## 4. Multi-Stage hwmon Entry Classification Pipeline

The pipeline is cheap-first, expensive-last, with each stage acting as a kill-gate. Total worst-case time per channel on a busy system is bounded by Stage 3 (≈90 s) + Stage 4 (30 s) ≈ 2 minutes, executed once at first-run / on hardware-change detection only.

### Stage 1 — Structural sanity (≈1 ms per channel)

**What it does.** Enumerate `/sys/class/hwmon/hwmon*/`. For each `pwmM`:
- Verify the file exists and is readable as the running uid (typically root).
- Verify it is writable (`access(W_OK)`).
- Verify a sibling `pwmM_enable` exists, is readable, and is writable.
- Read `name` from the parent directory and tag the channel with the driver family.
- Reject if any of the above fails.

**What it catches.** Pattern 2.f read-only platforms (`hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors`) — they have no `pwm*` files, so they are filtered automatically.

**False-positive risk** (rejecting a real entry): negligible; a real fan-control channel always has both files.

**False-negative risk** (admitting a phantom): high — Stage 1 alone admits all phantom channels.

**Time cost.** ~1 ms per channel, ~10 ms total for a typical system with 1–3 hwmons and 5–15 PWMs.

### Stage 2 — Write-and-read-back probe (≈100 ms per channel)

**What it does.**
1. Read and save `pwmM` and `pwmM_enable` (so we can restore on exit / failure / SIGINT).
2. Write `1` to `pwmM_enable`. Read back. If not 1 → either EINVAL (note for 2.d) or BIOS-fight on enable bit (note for 2.e early signal).
3. Write `128` to `pwmM`. Read back. If write returned EINVAL → record EINVAL, run the canonical-value probe ({0, 64, 85, 128, 170, 192, 255}) and record the accepted set.
4. If read-back returns a value other than 128 (and not in {0, 255}, which would indicate clamping not stepping) → record as quantized (2.d stepped pattern); store the read-back as a domain point.
5. Restore original `pwmM` and `pwmM_enable`.

**What it catches.** Pattern 2.d (EINVAL/STEPPED) classification; early signal for 2.f partial drivers; early signal for 2.e (if `pwm_enable=1` write fails or immediately reverts).

**False-positive risk**: low. A legitimate channel that happens to be quantized is still real and should be admitted as quantized, not excluded.

**False-negative risk**: cannot detect 2.a, 2.b, 2.c, 2.h (those need actual fan motion).

**Time cost.** ~50 ms for the simple write/read pair; ~500 ms if the EINVAL probe is invoked. Dell SMM calls can take 10–100 ms each (the kernel doc warns about this) so on Dell laptops the whole stage may take 1–2 s.

### Stage 3 — Tach correlation sweep (≈45–90 s per channel, can be parallelized)

**What it does.** This is the core of the catalog-less detection.

1. Snapshot all `fanK_input` values across all hwmons in the system → `baseline[K]`.
2. For each candidate `pwmM`:
   - Set `pwmM_enable=1`.
   - Write `pwmM=255` (full speed). Wait `settle_full` (default 30 s — see fan2go markusressel/fan2go#28: *"the initial waiting time should be increased to at least 30 seconds, up to a minute would be preferable"*).
   - Snapshot all `fanK_input` → `high[K]`.
   - Write `pwmM=64` (low). Wait `settle_low` (default 30 s).
   - Snapshot all `fanK_input` → `low[K]`.
   - Compute Δ[K] = high[K] − low[K] for every K in the system.
   - Record the matrix entry `M[m, k] = Δ[K] / (255 − 64)`.
3. After all PWMs swept: for each `pwmM`, find `argmax_k M[m, k]`. If the max delta is < `noise_floor` (default 50 RPM), classify as ZERO-TACH or PHANTOM-CHANNEL (skip). If max > floor and the corresponding fan index is M itself, admit as a normal pair. If ≠ M, admit as MISMATCHED-INDEX, store the discovered pairing.
4. If multiple `pwmM` correlate with the same fan, classify as ambiguous (likely PWM hub or splitter); admit with reduced confidence and emit warning.

**What it catches.** Patterns 2.a (zero-tach), 2.b (write-ignored — manifests as zero correlation across all fans), 2.c (phantom-channel — same signal as 2.a from this stage's perspective), and 2.h (mismatched-index — auto-resolved by selecting argmax fan).

**False-positive risk** (rejecting a real channel that has a slow-spinning fan): real risk if `settle_full` is too short. The pwmconfig narkive thread (https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly) shows pwmconfig had a years-long bug where short settle times caused legitimate correlations to be missed. ventd must use ≥30 s default (configurable to 60 s for users with very inertial fans).

**False-negative risk** (admitting a phantom): low if ≥30 s settle is used, and if all fans are scanned per PWM. A pure-DC-mode header where the chip is in PWM mode but the output stage is DC could still produce a small thermal-coupling false correlation (CPU heats up because nothing is cooling and the *other* fan ramps up); this is rare but possible. Mitigation: require the correlated fan's delta to be > 5× the median noise across all fans.

**Time cost.** With N PWMs, the naïve serial implementation is `N × (settle_full + settle_low)` = N × 60 s. **Critical optimization**: PWMs in the same hwmon can be swept *in parallel* if their controlled fans are different (verified after the first pass). The pwmconfig bug history shows the importance of disabling each PWM independently and not letting the disabled state of one PWM leak into the next channel's measurement.

A safer time-bounded alternative: only sweep one PWM at a time but interleave so a single channel's measurement uses the same settle window as the previous channel's restoration phase. This keeps total time at ~N × 60 s but with fewer thermal excursions.

### Stage 4 — BIOS-fight detection (≈30 s per admitted channel)

**What it does.** Run only on channels that survived Stage 3.
1. Set `pwmM_enable=1`. Write `pwmM=baseline_pwm` (whatever the current calibration target is, default 128).
2. At T+5 s, T+10 s, T+30 s: re-read `pwmM_enable` and `pwmM`.
3. If `pwmM_enable` ≠ 1 at any timepoint → BIOS-FIGHT on enable.
4. If `pwmM_enable=1` at all timepoints but `pwmM` differs from the written value (and no userspace process wrote it during the window) → BIOS-FIGHT on duty cycle.
5. If both stable → channel is admitted to the production calibration set.

**What it catches.** Pattern 2.e (BIOS-FIGHT). This is the only pattern that requires a time-delayed re-read; all other patterns can be detected within the Stage 1–3 window.

**False-positive risk** (false BIOS-fight): another userspace daemon (lm-sensors fancontrol, NBFC, thinkfan) is also writing. ventd should refuse to start if it detects another PID has the same `pwm*` file open or has written to it recently (mtime check). R1 also helps — the virtualization detection ensures ventd doesn't run inside a container where the host daemon is the EC's "third party".

**False-negative risk** (missed BIOS-fight): the EC's polling rate is typically 1–3 s but some Lenovo Legion ECs only assert auto every 10–30 s. The 30 s timepoint is a hard requirement; 60 s is even safer.

**Time cost.** 30 s per admitted channel, but can be parallelized (write all admitted PWMs, sleep 30 s, re-read all).

---

## 5. Per-Driver-Family Quirk Notes

### 5.1 `nct679x` family (`nct6775`, `nct6776`, `nct6779`, `nct6791d`, `nct6792d`, `nct6793d`, `nct6795d`, `nct6796d`, `nct6797d`, `nct6798d`, `nct6799d`)

- **PWM count.** Hardware-advertised: 3 (nct6776) to 7 (nct6796/nct6798/nct6799). Sysfs exposes `pwm[1-N]`. Source enum in `drivers/hwmon/nct6775.h`: `enum kinds { nct6106, nct6116, nct6775, nct6776, nct6779, nct6791, nct6792, nct6793, nct6795, nct6796, nct6797, nct6798, nct6799 };` and `#define NUM_FAN 7` (https://github.com/torvalds/linux/blob/master/drivers/hwmon/nct6775.h).
- **Phantom rate.** Empirically 30–60 % of advertised channels are phantoms on consumer boards. Boards with 3 chassis-fan headers + 1 CPU-fan header on an nct6798d (5–7 PWMs) have 1–3 phantoms.
- **Mode bit pitfall.** `pwm_mode` (0=DC, 1=PWM) must match the BIOS-side header configuration. Mismatch is the dominant cause of WRITE-IGNORED on this family.
- **`pwm_enable` semantics** (https://docs.kernel.org/hwmon/nct6775.html): {0=full speed, 1=manual, 2=Thermal Cruise, 3=Speed Cruise, 4=Smart Fan III (nct6775F only), 5=Smart Fan IV}. Only mode 1 is safe for ventd to use. Mode 4 returns EINVAL on chips other than nct6775F (cf. archlinux thread https://bbs.archlinux.org/viewtopic.php?id=225349 where `echo 4 > pwm1_enable` returns "Invalid argument" on nct6791).
- **BIOS interaction.** Generally well-behaved: BIOS configures the auto curves at boot via SMM, then leaves the chip alone; userspace can set `pwm_enable=1` and the BIOS won't fight on most desktop boards. Prebuilt OEM boards (some HP, Lenovo desktops) can have a Q-Fan-equivalent that re-asserts mode 5; treat any nct679x in a prebuilt as BIOS-fight-suspect.
- **Out-of-tree.** `nct6687d` (https://github.com/Fred78290/nct6687d) is a separate driver for MSI's `nct6687` chip (not in mainline as of writing); presents similar sysfs but is an entirely different driver.

### 5.2 `it87` family (`it8728`, `it8665`, `it8688`, `it8689`, `it8772`)

- **PWM count.** 3 (older it8703/it8705) to 5 (it8688/it8689/it8665).
- **Maintenance status.** Mainline `drivers/hwmon/it87.c` covers older chips; modern Gigabyte/ASRock boards with IT8688E/IT8689E/IT8665E require the **out-of-tree** `frankcrawford/it87` driver (https://github.com/frankcrawford/it87). The maintainer notes: *"Many of Asus' recent Ryzen motherboards have the ITE IT8665E sensor IC, which does not have any publicly available datasheets. Some support has been added to the out-of-tree IT87 driver, but this is currently unmaintained and not working on recent kernels."*
- **Module parameters.** `mmio=on` is required on AMD platforms for the H2RAM-backed IO path; without it, sensor read may conflict with ACPI on some boards.
- **Critical bug.** **IT8689E revision 1** silently accepts PWM writes but does not act on them (https://github.com/frankcrawford/it87/issues/96). Revision 2 of the same chip works. Detect by `dmesg | grep "it87.*revision"`; treat rev 1 as WRITE-IGNORED-suspect.
- **Phantom rate.** High on small ITX/M-ATX NAS boards (Topton, CWWK, Aoostar) which use IT8613E or IT8623E and only wire 1–2 of the chip's 3+ PWM outputs.
- **`fix_pwm_polarity` parameter.** Marked DANGEROUS in driver source (https://github.com/frankcrawford/it87) — do not touch.

### 5.3 `dell-smm-hwmon`

- **Stepped values.** `i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)`. Default `I8K_FAN_HIGH=2` → values are clamp-rounded to {0, 128, 255}. Module parameter `fan_max=3` gives 4 levels; `fan_max=4` gives 5 levels. See `drivers/hwmon/dell-smm-hwmon.c` (https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c).
- **`pwm_enable` accepts only {1, 2}.** `default: return -EINVAL;` — mode 0 not supported.
- **Whitelist-gated automatic mode disable.** `i8k_whitelist_fan_control` DMI table determines whether `pwm1_enable=1` actually disables BIOS auto control. If the board is not whitelisted, setting mode 1 may have no effect (or only affect status, not actual fan control).
- **DMI blacklists.** Inspiron 3505, Precision M3800, Vostro 1720 are on `i8k_blacklist_fan_type_dmi_table` — `i8k_get_fan_type` causes kernel hangs and is suppressed (Pali Rohár's series, https://groups.google.com/g/linux.kernel/c/WfidNnGV31k).
- **`pwm1_enable` controls ALL fans simultaneously.** From the kernel doc: *"notwithstanding the name, pwm1_enable sets automatic control for all fans"* (https://github.com/torvalds/linux/blob/master/Documentation/hwmon/dell-smm-hwmon.rst). ventd must NOT treat `pwm1_enable` and `pwm2_enable` as independent on `dell_smm`.
- **BIOS-FIGHT.** Pervasive on XPS 9560 and similar; documented workaround is `dell-bios-fan-control-git` (https://github.com/TomFreudenberg/dell-bios-fan-control). PowerEdge servers fight via iDRAC and require Thermal Profile = Custom or `ipmitool raw 0x30 0x30` overrides.
- **EIO.** Sporadic IO errors on writes (https://github.com/lm-sensors/lm-sensors/pull/383) — ventd must retry-once on EIO before classifying as fault.

### 5.4 ASUS trio: `asus_atk0110` vs `asus-ec-sensors` vs `asus_wmi_sensors`

| Driver | Coverage | Sensors | PWM control? |
|--------|----------|---------|--------------|
| `asus_atk0110` (acpi) | Older ASUS desktop boards (pre-AM4 era) via ATK0110 ACPI device | Voltages, fan RPMs, temps | **Read-only** (no PWM attributes) |
| `asus-ec-sensors` | Modern ASUS desktop boards (X570, B550, X670, B650, X870, etc.) via EC + ACPI mutex | Chipset/CPU/MB/T_Sensor/VRM/Water In/Out temps, CPU Optional/Chipset/VRM/Water Flow fan RPMs | **Read-only** (driver source comment lists what it provides — no PWM writeback path) |
| `asus_wmi_sensors` (WMI) | X370/X470/B450/X399 Ryzen boards via ASUS WMI | All voltages/RPMs/temps from UEFI | **Read-only** explicitly: *"No, fan control is not part of the Asus sensors WMI interface"* (https://github.com/electrified/asus-wmi-sensors) |
| `asus-nb-wmi` | ASUS laptops | fan1_input, fan1_label | `pwm1_enable` writable (modes 0/1/2); `pwm1` raw write usually returns EINVAL; some models lack `pwm1` entirely |

- **Overlap.** A single ASUS desktop board commonly exposes BOTH `nct6798-isa-0290` (Super-I/O direct, full PWM control) AND `asus-ec-sensors` (read-only, more accurate sensor labels). ventd should prefer the writable nct6798 entry for control, while ALSO reading temps from asus-ec-sensors for better labels — but do NOT try to write to asus-ec-sensors.
- **Buggy WMI BIOSes.** asus_wmi_sensors documentation warns: *"This can result in fans stopping, fans getting stuck at max speed, or temperature readouts getting stuck. The Prime X470 Pro seems particularly bad for this."* Polling frequency >1 Hz can trigger this — ventd's default 200 ms polling rate is potentially harmful on these boards. Recommendation: polling rate ≤1 Hz when `asus_wmi_sensors` is the primary sensor source.
- **`asus_fan` (DKMS, deprecated).** Older third-party module providing 2-fan control on some pre-2018 ASUS laptops; not maintained, do not depend on.

### 5.5 `thinkpad_acpi`

- **`pwm1` value mapping.** PWM values are quantized to fan levels 0–7, with each level mapping to ~36 PWM units. From the documentation (https://www.kernel.org/doc/Documentation/laptops/thinkpad-acpi.txt): *"To start the fan in a safe mode: set pwm1_enable to 2. If that fails with EINVAL, try to set pwm1_enable to 1 and pwm1 to at least 128 (255 would be the safest choice, though)."*
- **`fan_watchdog`.** Critical safety attribute. Range 1–120 seconds, 0 disables. If userspace stops writing within the watchdog window, the EC reverts to safe mode automatically. ventd must either (a) set `fan_watchdog=120` and refresh writes within 120 s, or (b) leave watchdog at default. Watchdog applies only to writes via the procfs/sysfs interface, not to BIOS-side reverts.
- **DISENGAGED mode.** "Full speed" / "disengaged" is level=127 in `/proc/acpi/ibm/fan` and corresponds to `pwm1_enable=0` in sysfs. In DISENGAGED, the EC is not even monitoring the fan — it just runs at maximum unregulated speed. **Dangerous to set unintentionally** because the fan tach is not validated by the EC and a stuck fan won't trigger a thermal shutdown.
- **`pwm1_enable` semantics.** {0=offline (full-speed), 1=manual, 2=EC auto, 3=reserved/SW PWM not implemented}. Modes 0 and 2 may EINVAL on some ThinkPads. Source: Documentation/admin-guide/laptops/thinkpad-acpi.rst lines 1261–1267.
- **Secondary fan.** `fan2_input` exists on some models (X1 Extreme, P-series) but `pwm2` does not; see 2.g. The two fans are controlled by a single `pwm1`.
- **Module parameter.** `fan_control=1` must be passed to the module (not just compiled in) for the fan-control sysfs to be writable. ventd must check `/sys/module/thinkpad_acpi/parameters/fan_control` and refuse to control if it is 0.

### 5.6 `hp-wmi-sensors`

- **Read-only by design.** No `pwm*` attributes. Only `fan[X]_input`, `fan[X]_label`, `fan[X]_fault`, `fan[X]_alarm`. Source (https://github.com/kangtastic/hp-wmi-sensors/blob/main/hp-wmi-sensors.c): `[hwmon_fan] = HWMON_F_INPUT | HWMON_F_LABEL | HWMON_F_FAULT,` — no `HWMON_F_PWM` attribute requested.
- **Conflict with `hp-wmi`.** If `hp-wmi` (the laptop-hotkey driver) is loaded, alarm attributes become unavailable due to WMI event GUID conflict. Documented in https://docs.kernel.org/hwmon/hp-wmi-sensors.html.
- **Two MOF variants.** Two HPBIOS_BIOSNumericSensor schemas are known; some newer ZBook systems use the second variant (see comment block in hp-wmi-sensors.c).
- **ventd action.** Skip entirely for control purposes; admit as monitoring source for FANK_INPUT readings.
- **HP business desktops/laptops still expose stepped fan levels via undocumented WMI methods accessible only via reverse-engineered OMEN-CLI / HP CMI tools — outside ventd's scope.

### 5.7 `k10temp` / `coretemp` (companions)

- These drivers expose temperature only — `temp1_input`, `temp{N}_label`. They have no fans and no PWM channels. They are listed because they appear in the hwmon enumeration and ventd's Stage 1 will see them, must classify as "temp-only / no-pwm" rather than as ghost.
- `coretemp-isa-0000` provides per-core temps on Intel; one hwmon per CPU package.
- `k10temp-pci-00c3` provides Tctl/Tdie/Tccd1..N on AMD; offset can be 20 °C on some Threadripper SKUs (kernel handles via `ZEN_OFFSET` table).
- ventd must include these in its sensor pool but not in its actuator pool. They are the authoritative thermal inputs for fan curves on most systems.
- Companion read-only drivers also include `nvme` (drive temp), `drivetemp` (HDD/SSD SATA temp), `acpitz` (ACPI thermal zones — often inaccurate), `pch_*temp` (Intel PCH), `iwlwifi`, `amdgpu`/`nvidia` (GPU temps).

---

## 6. False-Positive Cost vs False-Negative Cost Analysis

A "false-positive exclusion" means ventd treats a real, controllable PWM as a ghost and skips it. A "false-negative exclusion" means ventd admits a phantom into the calibration set.

**Cost of a false-positive exclusion (real fan skipped).**
- User complaint: "ventd doesn't see my CPU fan."
- Undercooling risk: only if the BIOS auto curve was the ONLY thing keeping the fan running, AND the user disabled it expecting ventd to take over. In practice, when ventd skips a channel, `pwm_enable` is restored to whatever it was before (typically 2 = auto), so the BIOS continues controlling and there is no thermal risk.
- Support burden: high. Each false-positive exclusion is a GitHub issue.
- Mitigation: a clear, well-documented `force_include` config flag.

**Cost of a false-negative inclusion (ghost included).**
- Wasted probe time: ~60 s per ghost during initial calibration; not user-visible.
- Bad calibration data: if a phantom is admitted, ventd will believe its writes have effects they don't, may attribute thermal changes elsewhere in the system to the phantom's actions, and may underweight a real channel. **This is the dominant risk** because ventd's calibration data is supposed to be authoritative across runs.
- BIOS conflict: if a ghost happens to be on a BIOS-fight channel and ventd keeps writing, the EC may flag a fan failure, throttle the CPU, or worse on some Dell/HP systems.
- Pollutes fleet baselines: if ventd is run on a fleet of similar boards, an admitted phantom on one board may be treated as a calibration pattern to apply to others.

**Quantitative comparison.** False-negative inclusions are *strictly worse* for ventd's design goals than false-positive exclusions, because (a) the override path is one-line, (b) the BIOS auto curve is a safe fallback for excluded channels, (c) phantom calibration data is unbounded in its corruption potential, and (d) ventd's audience is homelab / small-server users who generally have BIOS access and can flip a `force_include` flag easily.

**Recommended bias.** Tune Stage 3's noise floor *upward* (50 RPM is conservative; 100 RPM is better for spinning-rust NAS arrays where fan noise floor at idle is already 80+ RPM). Tune Stage 4's settle window *upward* (use 30 s rather than 5 s as the BIOS-fight threshold). Default to skip-with-warning, never include-without-explicit-confidence.

For ventd's audience specifically (Proxmox / TrueNAS / Unraid / homelab desktop), the empirical phantom rate is high (Super-I/O on consumer-class boards) and the BIOS-fight rate is low (most prebuilt-OEM machines are not in the homelab fleet). The bias should be: aggressively reject phantoms via Stage 3, lightly probe for BIOS-fight via Stage 4, and rely on user override for the rare false-positive exclusion.

---

## 7. Recommended Detection Heuristic Ordering (Cheap-First)

The pipeline is implemented in this order, and each stage's failure short-circuits the remaining stages:

1. **Driver name lookup** (1 µs). Read `name` file; check against known read-only drivers (`hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors`, `asus_atk0110`). Skip immediately; record as MONITORING-ONLY.
2. **Structural sanity** (1 ms). `pwmM` exists, readable, writable, has `pwmM_enable` sibling. Skip if not.
3. **Companion driver check** (1 ms). Driver is `coretemp`, `k10temp`, `nvme`, `drivetemp`, `acpitz`, `iwlwifi*`, or pure GPU temp driver → SENSOR-ONLY, skip from control pool.
4. **Conservative `pwm_enable` write** (10 ms). Write `1`, read back. If EINVAL → check known platform table (thinkpad_acpi might require mode 2 first) and re-try. If still fails → SKIP with REASON=ENABLE_REJECTED.
5. **`pwm` write-and-read-back** (50–500 ms). Probe canonical values; classify as normal / quantized / EINVAL.
6. **Tach correlation sweep** (60–90 s, can be parallelized in batches per hwmon). Stage 3 above. Classify ZERO-TACH / PHANTOM-CHANNEL / WRITE-IGNORED / MISMATCHED-INDEX / ADMITTED.
7. **BIOS-fight stress test** (30 s, parallel). Stage 4 above. Classify BIOS-FIGHT / ADMITTED.
8. **Restoration**. Restore original `pwm` and `pwm_enable` for any skipped/failed channel. Persist the calibration record.

ventd should expose each stage as an individually testable subcommand (`ventd probe --stage=3 hwmon4/pwm2`) for diagnostics on user systems where the pipeline produces unexpected results.

---

## 8. References

**Linux kernel source (elixir.bootlin.com / git.kernel.org / github.com/torvalds/linux):**
- drivers/hwmon/nct6775.h: https://github.com/torvalds/linux/blob/master/drivers/hwmon/nct6775.h (struct nct6775_data, enum kinds)
- drivers/hwmon/nct6775-core.c (cited via patch v5 08/12 add support for pwm/pwm_mode/pwm_enable: https://www.uwsg.indiana.edu/hypermail/linux/kernel/1302.3/00988.html)
- drivers/hwmon/dell-smm-hwmon.c: https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c (i8k_set_fan, i8k_pwm_mult, hwmon_pwm_enable switch returning -EINVAL on default)
- drivers/hwmon/asus-ec-sensors.c: https://github.com/torvalds/linux/blob/master/drivers/hwmon/asus-ec-sensors.c (header comment listing read-only EC sensor scope)
- drivers/hwmon/hp-wmi-sensors.c: https://github.com/kangtastic/hp-wmi-sensors/blob/main/hp-wmi-sensors.c (HWMON_F attribute mask omitting HWMON_F_PWM)
- Documentation/hwmon/nct6775.rst: https://docs.kernel.org/hwmon/nct6775.html
- Documentation/hwmon/dell-smm-hwmon.rst: https://docs.kernel.org/hwmon/dell-smm-hwmon.html
- Documentation/hwmon/asus_wmi_sensors.rst: https://docs.kernel.org/hwmon/asus_wmi_sensors.html
- Documentation/hwmon/asus_ec_sensors.rst: https://docs.kernel.org/6.3/hwmon/asus_ec_sensors.html
- Documentation/hwmon/hp-wmi-sensors.rst: https://docs.kernel.org/hwmon/hp-wmi-sensors.html
- Documentation/admin-guide/laptops/thinkpad-acpi.rst: https://www.kernel.org/doc/Documentation/laptops/thinkpad-acpi.txt (lines 1206–1298 for fan watchdog / DISENGAGED / pwm1_enable semantics)
- drivers/hwmon/it87.c (in-tree) and frankcrawford/it87 out-of-tree fork: https://github.com/frankcrawford/it87

**lm-sensors:**
- pwmconfig man page: https://man.archlinux.org/man/extra/lm_sensors/pwmconfig.8
- lm-sensors mailing list / pwmconfig correlation bug: https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly
- PR #383 — pwmconfig EIO handling on dell-smm-hwmon: https://github.com/lm-sensors/lm-sensors/pull/383
- Issue #459 — pwmconfig fans not returning to normal speed: https://github.com/lm-sensors/lm-sensors/issues/459

**fan2go (markusressel/fan2go):**
- Repo: https://github.com/markusressel/fan2go
- Issue #28 — settle time should be ≥30 s: https://github.com/markusressel/fan2go/issues/28
- Issue #63 — Corsair Commander Pro detection: https://github.com/markusressel/fan2go/issues/63
- Issue #64 — pwm_enable not readable / "PWM changed by third party" detector: https://github.com/markusressel/fan2go/issues/64
- Issue #110 — fan2go config example: https://github.com/markusressel/fan2go/issues/110
- Issue #116 — multi-pwm permission errors / nct6687 enumeration: https://github.com/markusressel/fan2go/issues/116
- Issue #201 — Dell Server stepped pwm read-back never settles: https://github.com/markusressel/fan2go/issues/201

**it87 out-of-tree driver (frankcrawford/it87):**
- Repo: https://github.com/frankcrawford/it87
- Issue #11 — Gigabyte B560M DS3H V2 IT8689E pwm not responding: https://github.com/frankcrawford/it87/issues/11
- Issue #96 — IT8689E rev 1 PWM writes have no effect (Gigabyte X670E Aorus Master): https://github.com/frankcrawford/it87/issues/96
- Issue #97 — IT8613E manual write works but pwmconfig fails: https://github.com/frankcrawford/it87/issues/97

**asus-wmi-sensors out-of-tree driver:**
- Repo: https://github.com/electrified/asus-wmi-sensors

**hp-wmi-sensors:**
- Repo: https://github.com/kangtastic/hp-wmi-sensors
- Patchew submission: https://patchew.org/linux/20230424100459.41672-1-james@equiv.tech/

**Dell SMM patch series and kernel doc:**
- Pali Rohár's [PATCH 0/6] dell-smm-hwmon fixes: https://groups.google.com/g/linux.kernel/c/WfidNnGV31k
- dell-bios-fan-control utility for XPS 9560: https://github.com/TomFreudenberg/dell-bios-fan-control

**Documentation, distros, forums:**
- ArchWiki Fan speed control: https://wiki.archlinux.org/title/Fan_speed_control
- Gentoo Wiki thinkfan: https://wiki.gentoo.org/wiki/Fan_speed_control/thinkfan
- Gentoo Wiki lm_sensors fancontrol: https://wiki.gentoo.org/wiki/Fan_speed_control/lm_sensors'_fancontrol
- ArchLinux forum — nct6791 mode 4 EINVAL: https://bbs.archlinux.org/viewtopic.php?id=225349
- ArchLinux forum — asus-nb-wmi pwm1 EINVAL: https://bbs.archlinux.org/viewtopic.php?id=295764
- ArchLinux forum — Asus G550JK pwm1 missing: https://bbs.archlinux.org/viewtopic.php?id=258679
- Proxmox forum — IT8613E pwmconfig failure: https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/
- CoolerControl supported devices: https://docs.coolercontrol.org/wiki/supported-devices.html
- CoolerControl hardware support (ThinkPad fan_control toggle): https://docs.coolercontrol.org/hardware-support.html
- Dell community — XE2 stepped fan speeds: https://www.dell.com/community/Optiplex-Desktops/Dell-Optiplex-XE2-SFF-Manual-Fan-Control/td-p/7663824

---

# R2 Spec-Ingestion Appendix Block

### R2 — Ghost hwmon entry taxonomy

- **Defensible default(s):**
  Multi-stage probe pipeline executed once after R1 authorizes writes, before any control-loop activation:

  1. **Stage 1 — Driver/structural triage.** Skip read-only platform drivers by `name` (`hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors`, `asus_atk0110`). Skip pure-temperature drivers (`coretemp`, `k10temp`, `nvme`, `drivetemp`, `acpitz`, `iwlwifi*`). Require `pwmM` and `pwmM_enable` both readable AND writable.
  2. **Stage 2 — Write/read-back probe.** Set `pwm_enable=1`, write canonical PWM values {0, 64, 85, 128, 170, 192, 255}, record EINVAL responses and read-back deltas. Classify channel as normal / quantized-stepped / EINVAL-restricted.
  3. **Stage 3 — Tach correlation sweep.** For each candidate `pwmM`, write 255 → wait ≥30 s → snapshot every `fanK_input` in the system → write 64 → wait ≥30 s → snapshot. Compute Δ matrix. Per `pwmM`, take `argmax_k Δ[K]`. Reject if max delta < 100 RPM (NAS-tuned noise floor); otherwise admit pairing (record mismatched-index if `argmax_k ≠ M`).
  4. **Stage 4 — BIOS-fight detection.** For each admitted channel, set `pwm_enable=1`, write target PWM, re-read both at T+5 s, T+10 s, T+30 s. Reject (or quarantine pending user override) if `pwm_enable` reverts or `pwm` is clobbered.

  Per-pattern policy:
  - 2.a ZERO-TACH → **skip** silently
  - 2.b WRITE-IGNORED → **skip** + WARN with chip-revision diagnostic
  - 2.c PHANTOM-CHANNEL → **skip** silently (subsumed by Stage 3)
  - 2.d EINVAL/STEPPED → **admit as quantized**; restrict output domain to discovered legal values; never interpolate
  - 2.e BIOS-FIGHT → **skip + diagnostic**; require explicit `bios_fight_override: true` per-channel to engage; never auto-engage
  - 2.f PLATFORM-MEDIATED (read-only) → **skip control**, **admit monitoring**; PLATFORM-MEDIATED (partial, e.g. `dell_smm`, `thinkpad_acpi`) → use `platform_quirk` table to set probe expectations
  - 2.g TACH-WITHOUT-PWM → **monitor-only**, never control
  - 2.h MISMATCHED-INDEX → **admit with discovered pairing**; emit info-level log noting the mismatch

  All skipped channels MUST be overridable via a per-channel `force_include: true` config flag, and ventd MUST restore the original `pwm_enable` and `pwm` register values for any channel it does not admit.

- **Citation(s):**
  1. Linux kernel hwmon documentation — nct6775 sysfs attributes & mode semantics: https://docs.kernel.org/hwmon/nct6775.html
  2. Linux kernel hwmon documentation — dell-smm-hwmon BIOS-override caveat & i8k_whitelist_fan_control: https://docs.kernel.org/hwmon/dell-smm-hwmon.html
  3. fan2go issue #28 (settle ≥30 s) and pwmconfig correlation-detection bug (lm-sensors narkive thread): https://github.com/markusressel/fan2go/issues/28 ; https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly

- **Reasoning summary:**
  Eight failure modes are documented across the kernel hwmon subsystem and userspace fan-control tooling, with at least one published bug or driver source citation for each. The dominant pattern by occurrence on ventd's homelab/NAS audience is PHANTOM-CHANNEL (Super-I/O chips advertising more PWMs than the board wires), addressable by a tach-correlation sweep with conservative settle time. The dominant pattern by *risk* is BIOS-FIGHT (Dell/HP/Lenovo embedded controllers re-asserting auto mode), addressable by a delayed re-read and a strict no-auto-override policy. False-negative inclusion of phantoms poisons calibration data; false-positive exclusion of real channels is recoverable by a one-line config flag. The pipeline therefore biases toward exclusion with mandatory user-override and is ordered cheap-first to keep the common-case probe under 5 minutes.

- **HIL-validation flag:** **Yes** — the pipeline must be empirically validated on at least three distinct fleet members because the failure modes are hardware-specific and not reproducible in software emulation:
  - **Proxmox host 5800X+RTX 3060** runs the Stage 3 sweep on its `nct6798d`/`it87` (whichever the board carries), confirming the phantom-channel rejection rate matches the physically-wired header count and that mismatched-index resolution finds the correct pwm↔fan pairing.
  - **MiniPC Celeron** (typically `it8613e` or `nct6116d` class) runs the full pipeline to validate behaviour on small-NAS Super-I/O chips with high phantom rates and 1–2 wired headers.
  - **3 laptops (Dell + ThinkPad + ASUS)** run Stage 2 and Stage 4 to validate stepped-value classification (Dell SMM), `pwm1_enable` mode-set semantics + fan_watchdog (ThinkPad), and `asus-nb-wmi` enable-only EINVAL handling (ASUS) — these three drivers have qualitatively different probe responses and must each be exercised on real hardware.
  - **13900K+RTX 4090 dual-boot desktop** runs Stage 4 BIOS-fight detection if its motherboard EC asserts a Q-Fan-equivalent auto-mode reversion; serves as the negative control if it does not.
  - **Steam Deck** is excluded — it runs Valve's custom EC fan controller, not a generic hwmon-writable driver, and is out of scope for ventd until/unless `steamdeck-hwmon` (or successor) gains writable PWM.

- **Confidence:** **High** for patterns 2.a, 2.c, 2.d, 2.f, 2.g, 2.h (all directly grounded in kernel source and reproducible in user reports). **Medium** for patterns 2.b (silicon-revision-specific to IT8689E rev 1 and a handful of nct6775 DC/PWM mode mismatches; small sample of bug reports) and 2.e (BIOS-fight timing varies widely across vendors; the 5/10/30 s sampling cadence is empirically defensible but not exhaustive against ECs with >30 s polling intervals such as some Lenovo Legion variants). Overall pipeline confidence: **High**, with the caveat that Stage 3's settle time is the single most sensitive parameter and may require tuning via HIL data.

- **Spec ingestion target:** spec-v0_5_1-catalog-less-probe.md — ingest as RULE-PROBE-001 (driver/structural triage), RULE-PROBE-002 (write-and-read-back), RULE-PROBE-003 (tach correlation sweep with ≥30 s settle and 100 RPM noise floor), RULE-PROBE-004 (BIOS-fight delayed re-read), RULE-PROBE-005 (force_include override path), RULE-PROBE-006 (platform_quirk table for partial-write drivers), RULE-PROBE-007 (state restoration on pipeline failure or daemon shutdown).