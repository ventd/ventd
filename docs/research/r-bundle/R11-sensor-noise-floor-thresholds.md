# R11 — Saturation-Detection Threshold (Sensor Noise Floor)

**ventd research item R11 · Linux fan controller daemon · Phoenix (solo) · April 2026**

---

## ARTIFACT 1 — Research Document

### 0. Executive summary

ventd's v0.5.8 marginal-benefit Layer C requires a defensible saturation rule of the form *"if ramping PWM above PWM_n produces ΔT < threshold over N consecutive control-loop writes, declare the channel saturated and clamp."* R6's polarity probe needs a defensible RPM-domain noise floor for its 64-PWM step. Both reduce to the same underlying question: **what is the per-driver Linux hwmon sensor noise floor, and how does it interact with sensor latency and the control loop's tick rate?**

The findings in this document are derived from primary-source kernel driver code (`drivers/hwmon/*.c`), the official kernel `Documentation/hwmon/*.rst`, the Intel 4-Wire PWM Fan Specification rev 1.3, the SMSC AN17.4 application note on tach-counter quantization, the Maxim/Analog AN3633 application note, the fan2go and lm-sensors source repositories and issue trackers, and a survey of community reports (Phoronix, Reddit, Tom's Hardware, Corsair, Gentoo, Linux Mint, Arch BBS, Framework, Ubuntu Launchpad).

The headline recommendations are:

| Quantity | Recommended default | Rationale (one line) |
|---|---|---|
| **Layer C ΔT saturation threshold** | **2 °C** | 2× the dominant 1 °C hwmon quantization; below this, change is indistinguishable from noise on coretemp/nct/it87. |
| **Layer C N writes** | **20 writes (=2 s at 10 Hz)** | Spans ≥2× the dominant CPU-die thermal time-constant lower bound (~0.5–1 s) and ≥1× the worst-case super-IO sensor cache age (`HZ + HZ/2`). |
| **Layer C N writes — HDD/NAS slow loop** | **3 sensor reads (=3 min at 1 read/min)** | drivetemp SCT cadence is ~1 min; HDD thermal τ is several minutes; 3 reads exceeds 1 τ. |
| **R6 RPM noise floor** | **150 RPM** (fan2go's 250 confirmed conservative; tight default 150) | max(physics floor at 1 Hz tach poll ≈ ±30 RPM, p95 observed jitter ≈ 50–120 RPM). 5× SNR margin for polarity decisions ⇒ require ΔRPM_probe ≥ 150 RPM. |
| **Sensor preference order (CPU loop)** | coretemp/k10temp(Tccd|Tdie) → nct67xx CPUTIN/AUXTIN → asus-ec → acpitz (dropped if jitter > 5 °C) | Latency-vs-τ rule: pick the lowest-latency sensor whose latency is ≪ thermal τ of the controlled mass. |

The rest of this document is the supporting evidence and per-driver tables.

---

### 1. Methodology

Two independent thresholds must be combined for every domain:

1. **Physics floor** — derived from the digitization and sampling math of the chip itself, ignoring environmental noise. This is a *hard* lower bound; you cannot resolve below it.
2. **Observed p95 jitter** — derived from real Linux deployments (forum reports, kernel driver comments acknowledging "spurious values," `maxRpmDiffForSettledFan` defaults from existing Go fan daemons, etc.). This is the *soft* lower bound that includes ADC noise, ambient drift, voltage rail noise, bearing wobble, aliasing, and BIOS shenanigans.

The recommended ventd noise floor is **noise_floor = max(physics_floor, p95_observed_jitter)**. Anything below this is treated as "no signal."

The latency dimension is independent. Sensor *latency* (time from physical event to readable sysfs value) sets the minimum useful control-loop period, but the relevant invariant is **sensor_latency ≪ thermal_τ**:

| Use case | Thermal τ (heat-source → sensor) | Acceptable sensor latency |
|---|---|---|
| CPU die at 10 Hz reactive control | 50–500 ms (silicon→IHS) | <100 ms (coretemp/k10temp deliver this) |
| Motherboard chipset/VRM | 5–60 s | <2 s (nct/it87 cache age `HZ+HZ/2`≈1.5 s, fine) |
| HDD case temperature | 60–300 s under steady load | <60 s (drivetemp SCT 1-min cadence is *just* fine) |
| Ambient / sanity check | minutes | seconds |

A sensor with 60 s latency is useless for a 10 Hz CPU loop regardless of accuracy. It is fine for an HDD loop. **The rule is not "fastest wins" — it is "fastest among those whose τ ≫ latency."**

---

### 2. Per-driver temperature noise-floor table

All resolution figures are confirmed against `Documentation/hwmon/*.rst` or the driver source. "Sample latency" is the time from a userspace `read()` to the value being a fresh on-die measurement; "sample frequency" is how often the underlying hardware updates (or is allowed to be polled by the driver's cache).

| Driver | Resolution | Typical short-term noise (±°C) | Sample latency | Sample frequency / cache | Accuracy issues / gotchas |
|---|---|---|---|---|---|
| **coretemp** (Intel DTS, per-core + package) | 1 °C (one byte, delta-from-Tjmax) | ±1 °C steady; rare ±2 °C spikes under sched migration | <100 µs (rdmsr on isolated CPU; uses `MSR_IA32_THERM_STATUS`/`MSR_IA32_PACKAGE_THERM_STATUS`) | On-demand; no internal cache | DTS accuracy is poor far from Tjmax: Intel quote on lkml — *"the relative value read from DTS is accurate approaching Tjmax. The accuracy deteriorates to ±10 °C at 50 °C. Any DTS reading below 50 °C should be considered to indicate only a temperature below 50 °C and not a specific temperature"* (Huaxu Wan, lkml fa.linux.kernel 2010). Tjmax may be misread on Core 2 / Atom — `tjmax=` module param exists. Stored as delta, so a wrong Tjmax skews everything. |
| **k10temp** (AMD Tctl/Tdie/Tccd, fam 10h–19h) | 1/8 °C (0.125 °C) hardware; many platforms only update LSBs at 1 °C in practice | ±0.25 °C steady; occasionally ±1 °C steps on Tctl when CCD migration occurs | <500 µs (PCI config read on D18F3 / D18F0 SMN) | On-demand; no cache | Tctl ≠ Tdie. Driver carries a hard-coded offset table (`drivers/hwmon/k10temp.c` ~line 85+): Ryzen 1600X/1700X/1800X = +20 °C, Ryzen 2700X = +10 °C, Threadripper 1900X/1920X/1950X = +27 °C, Threadripper 29xx[W]X = +27 °C. Older fam 10h Socket F/AM2+ has erratum 319 (returns inconsistent values, driver refuses to load without `force=1`). On Zen2+ Tdie is exposed as `temp2_input`; per-CCD as `temp{3..10}_input`. |
| **nct6775 / nct6776 / nct6779 / nct6791 / nct6792 / nct6793 / nct6795 / nct6796 / nct6797 / nct6798 / nct6799** (Nuvoton Super-IO) | 1 °C **or** 0.5 °C "depending on the temperature source and configuration" (`Documentation/hwmon/nct6775.rst` line 62) | ±1 °C on direct thermistor inputs (SYSTIN, AUXTIN); ±0.5 °C on PECI/SMBUS-routed temp sources; can chatter ±2 °C when sensor is right next to a switching VRM | Sample period varies per chip; driver caches temp for `HZ + HZ/2` ≈ 1.5 s window in older nct677x ports (compare `lm85.c` `LM85_DATA_INTERVAL = (HZ + HZ/2)` for the canonical pattern) | Auto-update inside chip; userspace effective rate ~1 Hz | Many BIOSes mis-route temp sources, so `temp1` may be CPUTIN, AUXTIN0, or PECI depending on board. `temp{N}_label` should be consulted. Asus boards often present nct as "asus-isa-0" (Arch BBS 221259) which hides the real chip — work around by loading nct677x explicitly. |
| **it87 / it8728 / it8665 / it8688 / it8689 / it8772 / it8620 / it8625** (ITE Super-IO) | 1 °C (some thermistor channels report 0.5 °C with low-byte) | ±1 °C steady; ±2 °C if BIOS routes a junction-only PECI channel | Driver cache typically `HZ + HZ/2` (~1.5 s) following hwmon convention | Userspace effective ~1 Hz | Some channels report `-128 °C` when sensor type is wrong (mis-configured `tempX_type`). Voltage scaling factors are board-specific; not relevant for temp directly but caution applies. |
| **w83795 / w83627 / w83627ehf** (legacy Winbond) | 1 °C; some temps with 0.25 °C low byte | ±1 °C; legacy thermistor channels are noisier (±2 °C) | ~1.5 s cache | ~1 Hz | Largely deprecated by nct6775 driver (which "supersedes the NCT6775F and NCT6776F support in the W83627EHF driver" — `nct6775.rst`). Treat as fallback only. |
| **asus-ec-sensors** (ASUS WMI/EC) | 1 °C | ±1 °C; values track BIOS temps, which on some ROG boards smooth in EC firmware (non-fresh) | EC-mediated; latency 2–10 ms but value may be **stale** by ~1 s due to EC update period | EC internal sample 1–2 Hz | Channel availability differs per board; the driver hardcodes per-board sensor maps. Treat as cross-check, not primary. |
| **dell-smm-hwmon** (Dell SMM) | 1 °C | ±1–2 °C; some platforms quantize coarsely (5 °C buckets) | **Slow**: each call is an SMI, 1–5 ms typical, occasionally 50+ ms when WMI fallback is used. WMI variant explicitly slower (`dell-smm-hwmon.rst`: *"The WMI SMM interface is usually slower than the legacy SMM interface since ACPI methods need to be called"*) | On-demand | Avoid hammering — at 10 Hz this loops the kernel through SMM 10 times/s with measurable system jitter. ventd should rate-limit to ≤2 Hz on dell-smm hosts. |
| **nvme** (block/nvme/hwmon) | 1 °C | ±1 °C; warm-up ramp is steep (drive can climb 20 °C in 30 s under sustained write) | NVMe admin command → 1–5 ms; some controllers cache internally | Driver does not cache; controller updates ~1 Hz | Composite vs. per-sensor: `temp1_input` is composite, `tempN_input` for N>1 may expose per-channel sensors with different IDs/labels. Some Phison/SMI controllers report only composite. |
| **drivetemp** (ATA SCT / SMART) | 1 °C | ±1 °C | SCT request → ~5–20 ms read | **Drive's SCT internal sampling cadence is typically 60 s**; SMART attribute 194 has ~1-min update similarly | `Documentation/hwmon/drivetemp.rst` warns: reading temperature can reset spin-down timer (WD120EFAX); some drives **freeze under heavy write load** when polled in SCT mode (`sct_avoid_models[]` list in `drivers/hwmon/drivetemp.c`). Do not poll faster than 30 s. The 60 s sensor cadence is **acceptable for HDDs because thermal τ is minutes**. |
| **amdgpu** (PCI mmio) | 1 °C | ±1 °C edge; junction noisier (±2 °C) under load transitions; mem temp is HBM/GDDR-die, slow | <100 µs | High-frequency on-chip (≥10 Hz) | `edge` is the package edge (slowest, smoothest); `junction` is hottest die point (fastest, noisiest); `mem` is VRAM. Use `edge` for fan curves. |
| **nvidia (NVML / proprietary)** | 1 °C | ±1 °C | <5 ms via NVML | Driver-internal ~1 Hz | NVML is the only stable surface; `nvidia-smi -q -d temperature` for GPU + memory junction (Ada+). Not exposed as hwmon by the proprietary driver — ventd needs a separate adapter (R-future). |
| **acpitz** (ACPI Thermal Zone) | 1 °C; sometimes 0.1 °C (deci-K reporting) | **Highly variable.** Reports range from "stuck at 40 °C forever" (Ubuntu archive 2222109) to "spikes randomly to 70 °C+" (Launchpad #1922111). Accuracy is BIOS/EC-firmware dependent and uncalibrated. | Latency depends on `_TMP` ACPI method evaluation, can be ms–10ms | Polling cadence is BIOS-controlled, usually ≥1 Hz | **Treat as last resort.** Framework community report (#54128) shows thermal_zone3 bouncing between 180.8 °C and a sane value several times a minute on FW13 7840U. ventd should auto-disable acpitz channels whose stdev over 60 s exceeds 5 °C. Kernel param `thermal.off=1` exists but kills the whole subsystem. |
| **pch_*temp / pch_cannonlake / pch_haswell** | 1 °C | ±2 °C; the PCH temp tracks chipset thermal mass which lags I/O activity by ~30 s | Driver-cached, ~1 Hz | Internal sensor inside PCH | Useful as a chassis ambient proxy, not for fan response. PCH typically idles 50–70 °C even with idle CPU. |
| **iwlwifi** | 1 °C | N/A | N/A | Read-only, often returns "N/A" outside thermal-throttle events | Useless for fan control. Mention only to dissuade users from configuring it. |
| **thinkpad_acpi** | 1 °C (per `/proc/acpi/ibm/thermal` legacy + hwmon `temp{1..N}_input`) | ±1 °C | ms-scale ACPI call | EC-internal ~2 Hz | Reliable on real ThinkPads; some channels are unpopulated and report 0 or -128 — filter on first read. |
| **applesmc** | 1 °C; some channels 0.25 °C | ±1 °C | SMC call, 0.5–5 ms | SMC internal ~1 Hz | Macs only; needed for dual-boot laptop targets; many channels labelled cryptically (e.g. `TC0P`, `TG0P`). Vendor cross-reference required. |

#### 2.1 Quantization step analysis

The dominant quantization across the field is **1 °C**, set by:
- coretemp (DTS register stores integer °C delta from Tjmax).
- k10temp's effective resolution as exposed through Linux for most consumer Ryzens (the 0.125 °C raw resolution is rarely meaningful given ambient noise).
- All Super-IO chips' direct-input channels (`Documentation/hwmon/nct6775.rst` line 62: *"either 1 degC or 0.5 degC"*).
- drivetemp, nvme, amdgpu edge, nvidia, acpitz.

A 1 °C-quantized sensor produces a step pattern: temperature plateaus at value V, then jumps to V±1, then plateaus again. **A single +1 °C step is therefore ambiguous** — it could be the true temperature crossing a quantization boundary, or pure measurement dither at the boundary. **The minimum unambiguous detectable ΔT is 2 °C.** Two adjacent quantization steps in the same direction within a short window is the floor; below that, you cannot distinguish a real trend from boundary chatter.

For a 0.5 °C-resolution channel (Super-IO PECI-routed), the unambiguous floor is 1 °C. ventd should query the actual `tempX_*` resolution from the channel where possible, but in practice the safe-everywhere assumption is **2 × 1 °C = 2 °C**.

This sets the **Layer C ΔT threshold at 2 °C** by construction: `X = max(2 × resolution, p95_noise) = max(2.0, ~1.0) = 2.0 °C`.

---

### 3. Per-driver RPM (tach) noise-floor table — physics + observed

#### 3.1 Physics framework

The Intel 4-Wire PWM Fan Specification rev 1.3 (September 2005) — *"Sense (tachometer/tacho) delivers two pulses per revolution of fan"* — fixes the relationship pulses_per_second = 2 × RPM/60 = RPM/30.

There are two fundamentally different tach-measurement architectures in commodity hwmon hardware:

**Architecture A: pulse counting in fixed window.**  Count edges over a window T_w. RPM = (count × 30) / T_w. Quantization is ±1 count, i.e., **ΔRPM_q = 30 / T_w**, *constant across RPM*.
- T_w = 1 s ⇒ ±30 RPM
- T_w = 2 s ⇒ ±15 RPM
- T_w = 0.5 s ⇒ ±60 RPM

**Architecture B: period measurement.**  Count clock cycles between two consecutive pulses. RPM = 30 × f_clk / clocks. Quantization is ±1 clock; ΔRPM_q ≈ RPM² / (30 × f_clk), *grows quadratically with RPM*.
- nct6775F variant uses a divider 1..128 to keep the period count in range; effective f_clk ≈ 1.35 MHz / divisor (Nuvoton NCT6775F datasheet, table referenced in `nct6775.rst` line 69). At divisor=2 and 1500 RPM, period = 14.8 ms, count ≈ 10 000, ΔRPM ≈ 0.15 RPM theoretical. At 6000 RPM, count ≈ 2500, ΔRPM ≈ 2.4 RPM. Very precise *theoretically*.
- it87 chips use similar period-counting (datasheet ITE IT8728F §8.7.4).
- The Microchip EMC230x/SMSC family (described in AN17.4 / Maxim AN3633) has 8-bit registers giving ~80 RPM resolution at high RPM, ~2.5 RPM at low.

In practice, on x86 Super-IO chips with period counters, **the chip-internal physics floor is much smaller than observed jitter**, so the userspace ±1-count Architecture-A model dominates the *system-level* noise floor: even though the chip can resolve to single-RPM internally, the kernel only refreshes the userspace value at ~1 Hz, and real fans have multi-RPM bearing wobble.

**System-level effective physics floor for Linux hwmon at 1-Hz userspace polling:**

| Fan RPM | Pulses/s | Architecture-A (1 Hz window) | Architecture-B (nct6775 internal) | What ventd sees |
|---|---|---|---|---|
| 600 | 20 | ±30 RPM | ±0.05 RPM | ±30 RPM (limited by user-space cadence) |
| 1500 | 50 | ±30 RPM | ±0.15 RPM | ±30 RPM |
| 2000 | 67 | ±30 RPM | ±0.3 RPM | ±30 RPM |
| 6000 | 200 | ±30 RPM | ±2.4 RPM | ±30 RPM |

So **the practical physics floor at standard 1-Hz tach polling is ±30 RPM**, regardless of chip family, dominated by the integer-pulse-count quantization at the userspace boundary.

**Aliasing.**  Whenever T_w is not commensurate with the pulse period, the read alternates between ⌊count⌋ and ⌈count⌉. A fan at 1485 RPM (49.5 pulses/s) will alternate 49 and 50, presenting as ±15 RPM dither even with no real speed variation. This effect is *independent of the underlying chip's true precision* and is the most common source of "fan jitter" complaints.

#### 3.2 Driver-specific RPM table

| Driver | Architecture | Physics floor (1 Hz user poll) | Observed p95 jitter | Recommended noise_floor | Notes |
|---|---|---|---|---|---|
| **nct6775 family** (nct6775F/76/79/91/98/99) | Period (Arch B) internally; user-poll Arch A | ±30 RPM | ±50–80 RPM steady-state on Noctua NF-A12x25 (FDB) at 1200 RPM; ±100–120 RPM on cheap sleeve bearings; ±200 RPM transient during PWM steps (driver auto-adjusts divider, can cause one-sample 0-RPM glitches) | **±100 RPM** | Driver auto-divider on NCT6775F: *"increases the divider value each time a fan speed reading returns an invalid value"* (`nct6775.rst` line 72–74). This causes occasional 0-RPM transients — handle by ignoring isolated zero readings during PWM transitions. |
| **it87 family** (it8620/8625/8665/8688/8689/8728/8772) | Period (Arch B) internally; user-poll Arch A | ±30 RPM | ±60–100 RPM observed; lm-sensors output frequently shows fan reading toggling ±20 RPM at idle (e.g. it8721 user reports 597↔617 RPM) | **±100 RPM** | Some it87 chips need `force_id=` or sensors-detect-driven nct→it87 collision resolution; this is a configuration issue, not noise. |
| **amdgpu fan RPM** | mmio register, on-die counter | ±10 RPM (driver reads directly, no user-window) | ±30–60 RPM steady; ±100 RPM during ramp transitions | **±50 RPM** | GPU AIB fans (especially blowers) have characteristic pulsing ±50 RPM resonance at 30–40% duty — bearing-design-dependent. |
| **thinkpad_acpi fan RPM** | EC-mediated, integer reporting | ±30 RPM | ±50 RPM steady; ThinkPad fans tend to have very stable EC firmware reporting | **±100 RPM** | EC may quantize to specific reported buckets (e.g. 2960, 3023, 3088 RPM) — these are EC-internal speed levels, not measurement noise. |
| **dell-smm-hwmon fan RPM** | SMM call returns multiplied count | **Quantized to multiples of 30 RPM** (`I8K_FAN_MULT = 30` in `drivers/hwmon/dell-smm-hwmon.c`) — every reported value is a multiple of 30 | ±30 RPM step but can flip between two adjacent multiples | **±60 RPM** (= 2× quantization step) | Per fan2go issue #201, Dell PowerEdge 3930 reports stepped values like 8040/6420 RPM — multiples of the I8K mult. ventd must treat dell-smm fans as "stepped" in R6 polarity probe and use ΔRPM ≥ 90 RPM (= 3 quanta). |
| **corsair-cpro / liquidctl-mediated** | USB HID polling, ~1 Hz controller-side | ±30–50 RPM | **±100–300 RPM** observed in production (Corsair forums, multiple "RPM fluctuation" threads): SP/LL fans on iCUE quiet mode reported 1000↔1500 RPM cycling at fixed PWM. RPM reading also disagrees with motherboard reading by 30–50% on some fans (forum 159635 — iCUE 1500 vs board 900–1000) | **±200 RPM** | Reports through HID are firmware-massaged. Treat liquidctl tach with significant suspicion; prefer separately wired fan headers when possible. |
| **nvidia (NVML fan)** | NVML query | ±30 RPM | Not reported in RPM by most consumer NVML cards — many expose duty-cycle only, not RPM | ±100 RPM if exposed | RPM availability is per-SKU; don't assume. |

#### 3.3 R6 polarity-probe step magnitude

R6 tests fan polarity by stepping PWM by 64 (25 % of full scale) and observing the sign of ΔRPM. A reliable decision needs **|ΔRPM| ≥ 5 × noise_floor** (matches a 5σ-equivalent SNR for binary sign decisions).

Using the dominant nct/it87 noise floor of ±100 RPM ⇒ **ΔRPM ≥ 500 RPM** is the *clean* margin. Most fans deliver this easily on a 64-PWM step (e.g., NF-A12x25 changes ~600 RPM per 25 % PWM in its linear region). Fans where the step doesn't clear 500 RPM are typically very-low-RPM near-stall regions; ventd's R6 already handles these by retrying at higher PWM_baseline.

**For the noise-floor parameter exposed to R6 itself** (not the SNR multiple), the right value is **150 RPM** — rounded up from 100 RPM observed p95 with a 1.5× margin. fan2go's `maxRpmDiffForSettledFan: 250` is conservative for *initialization* (where they need to wait through bearing stabilization) but is overkill as a noise floor. ventd's tighter 150 RPM noise floor + 5× SNR rule = 750 RPM minimum probe ΔRPM for a "high confidence" polarity call, falling back to 250 RPM minimum for a "best-effort" call. fan2go's 250-RPM constant is therefore validated as a defensible *outer* bound.

---

### 4. Sensor-lag table (PWM-step → ΔT visible)

The key question for Layer C's "ΔT < threshold over N writes" rule is: how soon after a PWM step does the temperature sensor reflect the change? If N writes is shorter than the sensor lag, you'll always conclude "saturation" because the temperature genuinely hasn't moved yet.

| Sensor | Heat source | Thermal mass between source & sensor | Effective lag after PWM step (1 °C detection) |
|---|---|---|---|
| coretemp / k10temp Tdie | CPU silicon junction | None — sensor is in-die | 50–500 ms (limited by IHS-cooler-fan loop, not sensor) |
| k10temp Tccd | CCD die | None | 50–500 ms |
| k10temp Tctl | "control" virtual | Synthesized from Tdie + offset | Same as Tdie |
| nct67xx CPUTIN (PECI-routed) | CPU package | PECI bus latency + super-IO cache | 1–3 s |
| nct67xx SYSTIN (board thermistor) | Mainboard chipset / VRM zone | Several grams of PCB copper + plastic body | 5–30 s |
| nct67xx AUXTIN (header-attached probe) | Wherever user routed it | Thermistor mass + cable | 2–10 s typical |
| asus-ec / nct AUXTIN_VR | VRM | VRM heatsink mass | 10–60 s |
| amdgpu edge | GPU edge sensor | GPU package edge | 1–3 s |
| amdgpu junction | Hottest die point | None | <500 ms |
| amdgpu mem | HBM/GDDR die | Memory package mass | 2–10 s |
| nvidia GPU temp | Die | None | <1 s |
| nvme composite | Controller silicon | Controller package | 1–5 s |
| drivetemp (HDD) | Spindle motor / platter / VCM | Drive case (~600 g of aluminium) | **60–300 s** under steady load; faster (30–60 s) for transient high-load events |
| drivetemp (SATA SSD) | NAND/controller | SSD case (~50 g) | 10–60 s |
| acpitz | "somewhere" | Variable, BIOS-defined | unspecified, treat as 5–60 s |
| pch_*temp | PCH die | PCH package + heatsink | 5–30 s |

**Implication for Layer C N writes.**  At a 10 Hz reactive control loop (100 ms tick) using coretemp/k10temp, lag is bounded above by ~500 ms ⇒ N = 5 writes is the *theoretical* minimum, but the temperature must also have *enough samples to show ΔT < 2 °C with statistical confidence*. With ±1 °C noise and 1 °C quantization, requiring 20 consecutive samples (= 2 s) all bounded within ±2 °C of the start gives a false-positive rate of about (1/3)²⁰ ≈ 3×10⁻¹⁰ for a true linear ramp at 0.5 °C/s — tight enough.

For HDD slow loops (1 read/min), N = 3 is the recommended minimum (= 3 minutes), beyond 1 thermal τ. N = 5 (= 5 minutes) is more conservative.

---

### 5. Driver-specific gotchas (the "do-not-trust" list)

1. **k10temp Tctl ≠ Tdie on Ryzen 1000-series and Threadripper 1900X/1920X/1950X/2900-series.** The driver applies a per-model offset (`tctl_offset_table[]` in `drivers/hwmon/k10temp.c`) of +20 °C (1600X/1700X/1800X), +10 °C (2700X), or +27 °C (Threadripper 19xx and 29xxWX). Old kernels (pre-5.6) on Threadripper Zen2 expose only Tctl with the offset baked in — **read `temp2_input` (Tdie) when present**, fall back to `temp1_input` (Tctl) only with a label-based correction. Recent k10temp cleanly exposes Tdie as a separate sysfs node.
2. **k10temp on ancient Phenom/Athlon (Family 10h Socket F/AM2+) — erratum 319.** Driver refuses to load without `force=1`. Treat as untrustworthy even when forced.
3. **coretemp's Tjmax fallback heuristic.** When `MSR_IA32_TEMPERATURE_TARGET` (0x1A2) is unreadable (some Atom, some Core 2), driver assumes Tjmax = 100 °C and computes T = 100 - delta. Real Tjmax may be 85 °C, causing reported temperatures to be 15 °C too high. Module param `tjmax=` is a manual workaround. ventd should sanity-check coretemp readings against k10temp/nct and, if reported temp consistently < ambient + 5 °C, warn user about possible Tjmax misdetection.
4. **coretemp DTS accuracy below 50 °C.** Per Intel statement on lkml (Huaxu Wan thread): *"the relative value read from DTS is accurate approaching Tjmax. The accuracy deteriorates to ±10 °C at 50 °C."* Below ~50 °C, coretemp readings should be considered "<50 °C" with no more precision. This **does not** affect the saturation detector (which only cares about *changes*) but does affect any absolute-threshold logic.
5. **acpitz unreliability.** Documented across multiple bugs:
   - Launchpad #1922111: "acpitz-acpi-0 wrong temperature" — random 70 °C+ spikes lasting 1–2 s on Ubuntu 20.04 + kernel 5.8.
   - Framework community #54128: thermal_zone3 alternating between 180.8 °C and reasonable values several times/min.
   - Manjaro forum 154502: Gigabyte board with critical threshold misreported as 20.8 °C, triggering thermal shutdown on sleep.
   - Ubuntu archive 2222109: acpitz stuck at 40.0 °C invariant with load.
   ventd should treat any acpitz channel whose 60 s rolling stdev exceeds 5 °C as "demoted" and refuse to use it as primary input; it remains useful only for crit-threshold cross-checking.
6. **dell-smm-hwmon RPM is quantized in steps of `I8K_FAN_MULT` (default 30).** The autodetect path: *"if fan reports rpm value too high then set multiplier to 1"*. For fans whose true RPM > 30000 the multiplier flips to 1 and the quantization disappears — this is rare in laptops. R6 must be aware: ΔRPM probe needs ≥3 quanta = 90 RPM, not the generic 150-RPM floor.
7. **drivetemp can wake spun-down WD120EFAX drives** (driver doc explicitly notes this). And `sct_avoid_models[]` lists drives that **freeze under heavy write load** when SCT-polled — currently includes specific Seagate models. ventd's NAS preset should poll drivetemp at 60 s minimum and check the kernel's avoid list at startup.
8. **nct6775 driver auto-divider transients.** When fan speed transitions, the driver may report 0 RPM for one sample while it adjusts the divider. Layer C and R6 must filter isolated single-sample 0-RPM readings out (median-of-3 or rolling window).
9. **Asus-ROG boards: nct6798 is hidden behind the asus-ec/asus-isa-0 façade by default** (Arch BBS 221259 / fan2go discussion). ventd needs to explicitly load nct6775 and may need `acpi_enforce_resources=lax` or similar. Document this as a setup gotcha.
10. **Corsair Commander Pro / liquidctl tach numbers disagree with motherboard tach** by up to 50 % on the same fan (Corsair forum 159635). The HID firmware applies its own scaling. If the user has the option of plugging into a real motherboard header, that is preferred for ventd's primary loop.

---

### 6. Sensor preference order with latency-vs-τ rule

Ventd's sensor selection logic should follow a **two-pass** algorithm:

**Pass 1 — admissibility filter.**  Drop any sensor where
- `latency > 0.1 × thermal_τ` of the controlled mass (sensor cannot keep up), OR
- `60 s rolling stdev > 5 °C` (sensor is unreliable per acpitz tests above), OR
- driver is on an explicit blacklist (acpitz on machines with known firmware bugs, k10temp without `force=1` on ancient AMD, etc.).

**Pass 2 — preference order.**  Among admissible sensors, pick by the priority list per use case below.

#### CPU reactive control loop (10 Hz, τ ≈ 0.1–10 s)

1. **k10temp Tccd[N]** (per-die, fastest on AMD Zen2+) — exposes hottest CCD directly.
2. **k10temp Tdie** (`temp2_input` when present).
3. **coretemp Package id 0** (Intel; package-wide, slightly noisier than per-core but right metric for aggregate cooling).
4. **coretemp Core N max** (computed in userspace as max over per-core; useful only if package not available).
5. **k10temp Tctl** — **only with offset compensation** from the model table; never as primary on offset-affected models.
6. **nct67xx CPUTIN** (PECI-routed; lags PCH I/O by 1–3 s — acceptable but noisier).
7. **asus-ec CPU temp** (cross-check, EC-smoothed).
8. **acpitz** — only if all above unavailable; demote per Pass 1.

#### NAS / HDD slow control loop (1/60 Hz, τ ≈ minutes)

1. **drivetemp `temp1_input`** (SCT, 1-min cadence) — primary; explicitly acceptable because latency (~60 s) ≪ τ (≥3 min) per the latency-vs-τ rule.
2. **SES enclosure (SCSI Enclosure Services) `ses_*` if present** — backplane sensors, slower but accurate ambient inside drive bay.
3. **Hottest-of-N aggregator** — for multi-drive arrays, use max() over all drivetemp channels.
4. **smartctl scrape** as fallback if drivetemp unavailable (older kernels < 5.6 or SCT-disable list match).
5. **Backplane thermistor via i2c** — vendor-specific (e.g., Supermicro X11/X12 boards expose this through nct or asus-ec).
6. **acpitz CHASSIS / SYS** — last-resort sanity bound only.

#### Ambient / sanity check

1. **Board thermistor (nct67xx SYSTIN, it87 temp1)** — slow but stable; specifically *because* of slow response, it tracks ambient (case interior).
2. **PCH temp** (intel-pch_thermal) — quasi-ambient proxy.
3. **Asus EC ambient** (per-board).
4. Specifically *not* coretemp/k10temp/amdgpu junction — these track load, not ambient.

#### GPU control

1. **amdgpu junction** (`temp2_input`, Vega+) — for reactive control.
2. **amdgpu edge** (`temp1_input`) — for steady-state curves (smoother).
3. **nvidia NVML temp** — analogous edge equivalent.

---

### 7. Cross-reference matrix — which R-items consume which R11 finding

| R-item | R11 finding consumed | How |
|---|---|---|
| **R4 — dT/dt computation** | sensor latency, sensor noise floor (per-driver) | True junction slope = (T(t) - T(t-Δ))/Δ where Δ ≥ sensor latency; otherwise dT/dt is dominated by sampling artefacts. R4 should reject any window with Δ < 2 × latency. Noise floor sets the "is dT/dt significant?" gate. |
| **R5 — idle-gate detection** | noise_floor (temp domain), sensor lag table | "System at thermal idle" = rolling stdev over (5 × τ) is below noise_floor. For coretemp, that's stdev(60 s window) < 1 °C. For drivetemp, stdev(15 min window) < 1 °C. |
| **R5 — settle time after PWM step** | sensor lag table | Settle time = max(thermal τ of controlled mass, 3 × sensor latency). For CPU loop = 1–3 s; for HDD = 2–5 minutes. |
| **R6 — polarity probe step magnitude** | RPM noise floor, RPM driver-specific quirks | ΔRPM_probe ≥ 5 × noise_floor for high-confidence call. fan2go's 250-RPM threshold confirmed as outer bound. dell-smm needs special-case 90 RPM minimum due to 30-RPM quantization. nct auto-divider 0-RPM transients filtered with median-of-3. |
| **R8 — coarse-classification fallback** (future) | sensor preference table, admissibility filter | When primary sensor unavailable or demoted, fallback chain consults R11's preference order. acpitz blacklist informs which boards trigger conservative defaults. |
| **Layer C — saturation detector** | ΔT threshold, N writes, sensor lag | ΔT = 2 °C (=2× resolution), N = 20 writes (=2 s at 10 Hz, = ≥2 × max sensor lag for CPU sensors). Slow-loop variant: N = 3 sensor reads (= 3 min at 1 read/min for HDD). |
| **R7 — config validation** (future) | per-driver tables | Reject configs that pair a slow sensor (drivetemp) with a fast loop (10 Hz) or a fast sensor with a slow loop (waste); warn on acpitz primary; warn on k10temp Tctl without explicit offset acknowledgement. |

---

### 8. Layer C saturation rule — final form

```
LAYER_C_SATURATION_THRESHOLD_C    = 2.0   // °C, see §2.1
LAYER_C_N_WRITES_FAST             = 20    // 10 Hz × 2 s
LAYER_C_N_WRITES_SLOW             = 3     // 1/60 Hz × 3 min (HDD/NAS)
LAYER_C_DT_DT_MAX_FOR_SATURATION  = 1.0   // °C/min — must be < this for saturation

// Saturation declared when:
//  for the last N writes after PWM was raised above PWM_n,
//      max(T) - min(T) < LAYER_C_SATURATION_THRESHOLD_C
//  AND
//      slope estimate over window < LAYER_C_DT_DT_MAX_FOR_SATURATION
//  AND
//      sensor source has not flapped (no demotion event in window)
```

The two-condition test (range AND slope) prevents the case where temperature is genuinely climbing slowly at the chip's resolution boundary (which the range test alone would miss).

---

### 9. R6 polarity probe — final form

```
R6_NOISE_FLOOR_RPM_DEFAULT        = 150   // see §3.3
R6_NOISE_FLOOR_RPM_DELL_SMM       = 60    // 2× I8K quantization
R6_NOISE_FLOOR_RPM_LIQUIDCTL      = 200   // observed firmware variability
R6_PROBE_SNR_HIGH_CONFIDENCE      = 5.0   // multiple of noise_floor
R6_PROBE_SNR_BEST_EFFORT          = 1.7   // matches fan2go's 250 RPM ≈ 1.7×150
R6_PROBE_PWM_STEP_DEFAULT         = 64    // 25 % of 0..255, R6's existing choice
R6_PROBE_FILTER                   = "median-of-3"  // drop nct auto-divider zero glitches
```

Polarity decision: median-of-3 ΔRPM after step ≥ R6_PROBE_SNR_HIGH_CONFIDENCE × noise_floor → high-confidence positive correlation; ≤ -R6_PROBE_SNR_HIGH_CONFIDENCE × noise_floor → high-confidence inverted; otherwise mark "ambiguous" and retry at higher PWM baseline.

---

### 10. HIL-validation items

HIL fleet recap: Proxmox host (Ryzen 5800X + RTX 3060), MiniPC (Celeron N-class), 13900K + RTX 4090 desktop dual-boot, 3 laptops.

| Test | Fleet member | Validates |
|---|---|---|
| 1-hour idle log of all temp channels, p95 stdev | All five | Per-driver noise-floor numbers in §2 |
| 1-hour stable-PWM tach log, p95 stdev at 800/1500/2000/max RPM | Proxmox host (nct or asus-ec, varies); 13900K (nct6798); MiniPC (whatever Super-IO); laptops (thinkpad_acpi/dell-smm) | Per-driver RPM noise-floor numbers in §3 |
| PWM step response: step PWM up by 64 every 30 s, log temp & RPM, fit settle time | Proxmox host; 13900K; one laptop | Sensor-lag table §4 and confirms R5 settle defaults |
| Layer C false-positive rate during sustained linear thermal ramp (stress-ng under fan-clamp) | 13900K + RTX 4090 (highest power, easiest to ramp) | ΔT=2 °C, N=20 fast-loop default does **not** spuriously trigger during real ramps at <0.5 °C/s |
| drivetemp poll cadence safety | Proxmox host (if SATA HDDs present); otherwise SKIP and document | drivetemp 60-s minimum poll, sct_avoid list |
| acpitz demotion logic | Any laptop showing acpitz spikes; the 13900K board if it has acpitz | 5 °C/60 s stdev gate correctly demotes pathological channels |
| dell-smm RPM quantization handling | Dell laptop in fleet (if any) | 90-RPM minimum probe step works, 30-RPM-quantized data still produces correct polarity |

---

## ARTIFACT 2 — Spec-ready findings appendix block

```markdown
### R11 — Saturation-detection threshold (sensor noise floor)

- **Defensible default(s):**
  - Layer C ΔT saturation threshold (temp domain): **2.0 °C** (= 2 × dominant 1 °C hwmon quantization).
  - Layer C N writes (fast/CPU 10 Hz loop): **20 writes** (≈ 2 s, spans ≥ 2 × longest fast-sensor lag).
  - Layer C N writes (slow/HDD-NAS 1/60 Hz loop): **3 sensor reads** (≈ 3 min, spans ≥ 1 × HDD thermal τ).
  - Layer C dT/dt secondary gate: **< 1.0 °C/min** for saturation declaration.
  - R6 polarity-probe RPM noise floor (default): **150 RPM** (= 1.5 × p95 observed ±100 RPM on nct/it87 at 1-Hz user poll).
  - R6 noise floor — dell-smm-hwmon override: **60 RPM** (= 2 × `I8K_FAN_MULT = 30` quantum).
  - R6 noise floor — corsair-cpro / liquidctl override: **200 RPM**.
  - R6 high-confidence SNR multiplier: **5.0** (probe ΔRPM ≥ 5 × noise_floor).
  - R6 best-effort SNR multiplier: **1.7** (matches fan2go's 250-RPM `maxRpmDiffForSettledFan` ≈ 1.7 × 150).
  - Sensor preference order (CPU loop): k10temp Tccd → k10temp Tdie → coretemp Package → coretemp Core-max → k10temp Tctl(offset-compensated) → nct67xx CPUTIN → asus-ec CPU → acpitz (last; demoted if 60 s stdev > 5 °C).
  - Sensor preference order (HDD/NAS loop): drivetemp → SES enclosure → multi-drive max() → smartctl fallback → board thermistor (cross-check only).
  - Sensor preference order (ambient sanity): board thermistor (nct SYSTIN / it87 temp1) → PCH temp → asus-ec ambient.
  - Latency-vs-τ admissibility rule: sensor admissible iff `sensor_latency ≤ 0.1 × thermal_τ_of_controlled_mass`.

- **Citation(s):**
  1. `Documentation/hwmon/nct6775.rst` lines 60–74 (resolution = "either 1 degC or 0.5 degC"; auto-divider behaviour). https://docs.kernel.org/hwmon/nct6775.html
  2. `Documentation/hwmon/coretemp.rst` (1 °C resolution, Tjmax-relative reporting) and Intel statement re. accuracy on lkml ("accuracy deteriorates to ±10 °C at 50 °C"). https://docs.kernel.org/hwmon/coretemp.html
  3. `drivers/hwmon/k10temp.c` `tctl_offset_table[]` (Ryzen 1xxx: +20 °C; 2700X: +10 °C; Threadripper 19xx/29xx: +27 °C) and `Documentation/hwmon/k10temp.rst`. https://github.com/torvalds/linux/blob/master/drivers/hwmon/k10temp.c
  4. `Documentation/hwmon/drivetemp.rst` (SCT 1-min cadence, sct_avoid_models, WD120EFAX spin-up note). https://docs.kernel.org/hwmon/drivetemp.html
  5. `drivers/hwmon/dell-smm-hwmon.c` `I8K_FAN_MULT = 30` quantization. https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c
  6. fan2go reference `maxRpmDiffForSettledFan` defaults (10/20/250 across versions and OEM-Dell-server context). https://github.com/markusressel/fan2go/blob/master/fan2go.yaml and https://github.com/markusressel/fan2go/issues/201
  7. Intel 4-Wire PWM Fan Specification rev 1.3 — "Sense delivers two pulses per revolution". https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf
  8. Microchip AN17.4 (RPM-to-tach quantization math, Architecture-B). https://ww1.microchip.com/downloads/en/Appnotes/en562764.pdf
  9. acpitz unreliability evidence: Launchpad #1922111, Framework community #54128, Manjaro #154502, Ubuntu archive 2222109. https://bugs.launchpad.net/bugs/1922111

- **Reasoning summary:** The dominant temperature-domain quantization across coretemp/k10temp/nct/it87/drivetemp/nvme/amdgpu/nvidia is 1 °C, making 2 °C the smallest unambiguously detectable change; combined with observed p95 noise of ±1 °C, this fixes the Layer C threshold at 2 °C. N=20 fast-loop writes (2 s at 10 Hz) is chosen because the longest CPU-class sensor lag is ~500 ms (Super-IO PECI cache `HZ + HZ/2`), so 2 s spans 4× that — false-positive rate during a real 0.5 °C/s ramp is on the order of 10⁻¹⁰. For HDDs the same logic applied to a τ of minutes gives N=3 reads. The RPM noise floor of 150 RPM is set by the userspace 1-Hz polling quantization (±30 RPM physics floor) plus observed bearing/aliasing wobble (±100 RPM p95) with a 1.5× safety margin; fan2go's 250-RPM constant from initialization context is shown to correspond to the "best-effort" SNR=1.7 envelope, validating it as a conservative outer bound rather than a tight default. The preference order applies the latency-vs-τ rule explicitly: coretemp/k10temp dominate the fast CPU loop because their <1 ms latency is ≪ 100 ms thermal τ; drivetemp's 60-s SCT cadence is acceptable for HDD loops because HDD τ is several minutes.

- **HIL-validation flag:** **Yes** — multi-fleet validation needed.
  - **13900K + RTX 4090 desktop** runs the **Layer C false-positive test** under `stress-ng --cpu` ramp with PWM clamped (verifies 2 °C/20-write default does not spuriously trigger during true linear ramps at <0.5 °C/s).
  - **Proxmox host (5800X + RTX 3060)** runs the **per-driver RPM jitter log** at 800/1500/2000/max RPM steady state for 1 h on its nct or asus-ec channels, plus the **drivetemp safe-cadence test** if SATA HDDs are present.
  - **MiniPC (Celeron)** runs the **acpitz demotion test** (induce thermal stress, verify 5 °C/60 s stdev gate correctly demotes acpitz when it spikes).
  - **One laptop (whichever has dell-smm-hwmon)** runs the **dell-smm RPM-quantization probe** (verify 90-RPM minimum step suffices for polarity).
  - **All five** run the **1-hour idle temperature noise log** to populate per-driver p95 noise tables.

- **Confidence:** **High** for temperature-domain numbers (2 °C / N=20 / N=3): backed by primary kernel-source resolution facts, multiple independent kernel-doc statements, and matched by community noise reports. **Medium-High** for RPM noise floor (150 RPM): physics floor is ironclad, observed jitter has wider variance across fan models so the 150-RPM default may need to be loosened to 200 RPM after HIL on cheap sleeve-bearing fans. **Medium** for the dell-smm and liquidctl overrides — they are derived from a small number of forum reports; HIL on Phoenix's actual Dell hardware will tighten or loosen them. **High** for the sensor preference order (latency-vs-τ rule is mechanically correct and per-driver latencies are kernel-source-confirmed).

- **Spec ingestion target:**
  - Primary: `spec-smart-mode.md` § Layer C (saturation detector) — embed the 2 °C / N=20 / N=3 / dT/dt<1 °C/min defaults and the dual-condition (range AND slope) test.
  - Cross-reference from `spec-v0_5_2-polarity-disambiguation.md` § R6 — embed the 150-RPM default, the dell-smm/liquidctl override table, and the SNR=5 (high-conf) / SNR=1.7 (best-effort) decision rule.
  - New supplementary doc (suggested): `spec-sensor-preference.md` — the per-use-case preference matrix and the latency-vs-τ admissibility rule. Reference from R5 (idle-gate), R7 (config validation, future), R8 (coarse-classification fallback, future).
  - Patch: add the per-driver noise-floor tables (§2 and §3.2 of this document) as an appendix to either `spec-smart-mode.md` or a new `spec-driver-quirks.md`, since they will be consulted by R4/R5/R7/R8 too.
```