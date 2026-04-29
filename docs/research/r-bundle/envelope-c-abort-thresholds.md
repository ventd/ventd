# R4 — Envelope C Abort Thresholds: Defensible Numeric Defaults for ventd Bidirectional PWM Probe

**Spec ingestion target:** `spec-v0_5_3-envelope-c.md`
**Author role:** Sole researcher; this document is the primary research artifact and is intended to be read alongside the spec-ready appendix at the end.
**Conservatism rule applied throughout:** When primary sources disagree, the most-conservative cited number is the default. Spread is reported explicitly so wide-spread cells become HIL-validation priorities.

---

## Artifact 1 — Research Document (long-form)

### 1. Problem framing

Envelope C is the controlled-degradation phase of ventd's bidirectional PWM probe: ventd intentionally drops fan duty so it can learn `dT/dPWM` and the system's thermal RC. The probe exists to characterize a system without a published thermal model. The probe is unsafe by construction — it removes airflow from a working CPU. Two abort thresholds bound the experiment:

- **(a) Maximum dT/dt (°C/s)** — *rate-of-rise* abort. If the slope of any monitored junction exceeds this value, ventd asserts the probe failure mode (ramp all PWM to 100 %, mark zone untrusted, raise telemetry event). dT/dt catches the runaway condition before T_abs is hit; it is the *primary* abort because thermal time constants on bare die are short relative to a fan's spin-up time.
- **(b) Maximum T_abs as offset below Tjmax/Tjct/_CRT** — *headroom* abort. Independent floor expressed as `Tjmax − margin`. T_abs catches steady-state drift, sensor failures that report flat values, and the case where dT/dt was misread because the probe step was small.

Asymmetric cost is explicit: aborting too late risks silicon damage or storage-medium degradation; aborting too early generates a recoverable user complaint via `ventd.toml` override. The bias is therefore strongly toward *early abort*.

### 2. Reference physics: why dT/dt is the *primary* defense

Chip-level thermal RC time constants are "on the order of milliseconds to seconds" (ScienceDirect, *Chip Temperature* topic compilation, citing the Pedram/Nazarian thermal-modeling literature). At the silicon-junction layer the time constant is dominated by the C of the die and the conductance through TIM1/IHS to the heatsink base; with the heatsink coupled but the fan stopped, the die is bottlenecked at convection from the fin stack to ambient air, which has τ ≈ 1–10 s.

Practical observation aligns: an Arch Linux user thread (`bbs.archlinux.org/viewtopic.php?id=229410`) reports cores rising from ~49 °C to 90 °C "in much less than a second" under a single-core compile burst, and Tom's Hardware forum threads consistently observe 13900K hitting 100 °C "almost immediately" on Cinebench R23 start. SkatterBencher's Thermal Velocity Boost analysis confirms Intel's own controller treats 70 °C as a per-core "act now" boundary. The qualitative claim from the Arch thread — "several hundred degrees per second" peaks at the junction during transient bursts — is **not** the steady-state slope ventd cares about; it is a sub-millisecond spike that the on-die DTS may not even sample. The slope ventd should defend against is the *sustained* slope after the fan stop, which empirically lands in the **single-digit to low-tens °C/s** band on a high-TDP desktop part with a coupled but unmoving heatsink, and far lower on parts with significant thermal mass relative to power (laptops in cold-plate contact, NUCs with heatsink-as-chassis, NAS HDDs).

A simple energy bound is useful for sanity. Treating the IHS+heatsink+die as a lumped capacitance C_th [J/K], with input power P [W] and zero airflow:

  dT/dt = P / C_th

For a 13900K running an unconstrained Cinebench load (≈250–345 W per VideoCardz dataset), with a Noctua NH-D15-class heatsink mass ~1.3 kg of mostly aluminum (c ≈ 0.9 J/(g·K)), the cold-start slope at the heatsink base is bounded by ~0.2 °C/s; but the *junction* lives behind several hundred mm² of poor TIM and only a few grams of effective mass close-coupled to the cores, so junction slopes of **5–20 °C/s** under sustained AVX/Cinebench load are plausible and match the "100 °C in seconds" anecdotes. This is the band ventd's dT/dt threshold lives in.

For HDDs the comparable bound is set by Seagate's own datasheets: the IronWolf Pro product manual (Rev. B) specifies a *non-operating* maximum ambient gradient of **20 °C/hour ≈ 0.0056 °C/s** as a transport spec, and the SCT temperature log default sample period is 1 minute. HDD thermal time constants are *minutes*, not seconds; ventd's NAS-class abort dT/dt must be expressed in °C/min.

### 3. Reference policies: what the OS already provides

ACPI 6.5 §11 (`uefi.org/specs/ACPI/6.5/11_Thermal_Management.html`) defines four trip points exposed via `/sys/class/thermal/thermal_zone*/`:

- `_PSV` — passive throttle entry (CPU clock reduction). On most OEM ACPI implementations this sits 5–20 °C below `_CRT`.
- `_AC0…_ACx` — active cooling tiers (fan ramps).
- `_HOT` — request to enter S4 (sleep-to-disk).
- `_CRT` — critical shutdown ("orderly_poweroff()" in `drivers/acpi/thermal.c`, mainline Linux).

ventd's T_abs is intentionally *more conservative* than `_PSV` because `_PSV` was designed for a *cooled* system: it expects the OEM fan policy to still be active. Envelope C's defining feature is that ventd has *deliberately disabled* that policy. The Linux kernel's own granularity guidance (kernel.org `sysfs-api.txt`) treats 5 °C as the canonical hysteresis quantum, and the ACPI spec says 5 °C trip granularity is "appropriate" for typical thermal zones — which is a useful floor when picking abort offsets (don't pick numbers tighter than the sensor fundamentally resolves).

### 4. Per-class CPU data

Notes on sources: Tjmax values for Intel are taken from `ark.intel.com` and corroborated by SkatterBencher's TVB articles (`skatterbencher.com/intel-thermal-velocity-boost`). AMD Tctl/Tjmax come from AMD product pages, k10temp kernel driver headers, and corroborated forum reports including the EPYC 9454P thread on WebHostingTalk that documents 101 °C Tctl observation. Where Tjmax is reported with an offset (some Threadripper / EPYC SKUs), the conservative interpretation is "the lower of the two reported numbers minus 5 °C buffer for sensor lag."

#### 4.1 Class 1 — Desktop high-TDP CPU on air cooler

Representative parts: i9-13900K, i9-14900K (Tjmax = 100 °C, TVB threshold 70 °C); Ryzen 9 7950X (Tjmax = 95 °C); 9950X / 9950X3D (Tjmax = 95 °C, X3D variants 89 °C per AMD product pages and confirmed by Quora answer citing AMD 7950X3D = 89 °C).

Cooler reference: NH-D15, Dark Rock Pro 4/5, Peerless Assassin 120 SE — air-cooled tower-class. Mass ≈ 1.0–1.5 kg, fin-stack convection-limited.

**Observed slopes (mixed primary sources + community telemetry):**

| State | dT/dt | Source/Spread |
|---|---|---|
| Idle (C-states, fan stopped) | 0.05–0.3 °C/s | Tom's Hardware 13900K cooling test; Overclock.net data; Phoronix XPS reviews — *tight agreement* |
| 50 % load, fan stopped | 1–4 °C/s | Cinebench R23 throttled-power runs; "instantly hitting 100 deg" thread (forums.tomshardware.com/threads/...3800698) — *moderate spread* |
| 100 % AVX/CB23, fan stopped | 5–20 °C/s | Tom's Hardware 13900K cooling tested article (`tomshardware.com/features/intel-core-13900k-cooling-tested`); 13900K threads showing "100 °C almost immediately" (~4–5 s ramp from 50→100 °C) — *wide spread, drives HIL priority* |

**Defensible defaults (Class 1, air-cooled high-TDP desktop):**
- **dT/dt abort = 2.0 °C/s** (most conservative: triggers on 100 % AVX before ventd even establishes initial dT/dPWM gain). Hold-time = 1 sample (~100 ms at 10 Hz).
- **T_abs headroom = 15 °C below Tjmax** → abort if T_pkg ≥ Tjmax − 15. For 13900K/14900K: abort at 85 °C. For 7950X: abort at 80 °C. For 9950X3D: abort at 80 °C (using 95 not 89, because Linux k10temp may report Tctl which already has offset baked in; the X3D 89 °C Tjmax floor argues for an even tighter 75 °C if X3D variant is detected).

Reasoning: 15 °C offset is 3× the ACPI 5 °C granularity quantum; matches Intel's own TVB "70 °C-or-below" preferred operating window; survives one sample of dT/dt blow-by between detection and 100 % PWM ramp (1 s @ ≤15 °C/s = within budget).

#### 4.2 Class 2 — Desktop high-TDP CPU on AIO liquid cooler

Same CPUs as Class 1 with 240/280/360 mm AIOs. The radiator+coolant adds ~0.4–0.6 kg of water at c ≈ 4.18 J/(g·K), giving the *system* a much larger thermal mass than air, but the cold-plate-to-die path is unchanged. With pump still running and fans stopped, the system trades fin-stack convection for radiator convection — both are airflow-limited. Once water saturates (typically 60–120 s later), the slope at the die rises toward Class 1 values.

**Observed slopes:**

| State | dT/dt | Source/Spread |
|---|---|---|
| Idle, fan stopped (pump on) | 0.02–0.15 °C/s | TrueNAS Community; Overclock.net 7950X thread — *tight* |
| 50 % load, fan stopped | 0.5–2 °C/s | Overclock.net AIO discussions — *moderate* |
| 100 % AVX, fan stopped | 3–10 °C/s (first 30 s); rising as coolant heats | Overclock.net 13900K Cinebench threads; the Intel Community thermal-throttling discussion — *moderate* |

**Defensible defaults (Class 2, AIO):**
- **dT/dt abort = 1.5 °C/s** (conservative against the 100 % case; AIO masking the early slope tempts longer probes, but coolant saturation makes late-probe failure modes worse).
- **T_abs headroom = 15 °C below Tjmax** (same offset as Class 1; AIO does not change the silicon's tolerance, only its delivery).

#### 4.3 Class 3 — Mid-range desktop (5800X, 5700X, 12700K, 13700K)

Tjmax: 5800X = 90 °C; 5700X = 90 °C; 12700K = 100 °C; 13700K = 100 °C. Sustained PL2 power is 125–253 W (Intel) / 105–142 W (AMD). Heat density is meaningfully lower than 13900K-class flagships.

**Observed slopes:**

| State | dT/dt | Source |
|---|---|---|
| Idle | 0.05–0.2 °C/s | Tom's Hardware reviews |
| 50 % | 0.5–2 °C/s | Phoronix 13700K thermal data |
| 100 % CB23 | 2–8 °C/s | Tom's Hardware forum; "13700K did not break 79 °C" thread |

**Defensible defaults (Class 3):**
- **dT/dt abort = 1.5 °C/s**
- **T_abs headroom = 12 °C below Tjmax** (Tjmax often 90 °C → abort 78 °C on 5800X/5700X; 100 °C → abort 88 °C on 12700K/13700K). Tighter than flagship class because part is less likely to be running near Tjmax in steady state, so a sudden climb is more diagnostic.

#### 4.4 Class 4 — Server CPU (Xeon Gold/Platinum, EPYC Milan/Genoa/Bergamo, Threadripper)

Representative: Xeon Platinum 8480+ (Tjmax = 90 °C per Intel ARK), EPYC 9654 Genoa (TDP 360 W, Tctl reported with offset, max ≈ 100 °C with throttling typically engaged at Tctl 95 °C — confirmed by multiple sources including Level1Techs EPYC 7443 thread and WebHostingTalk EPYC 9454P 101 °C report). Threadripper PRO 7000WX series Tjmax = 95 °C. Server cooling is forced-air through 1U/2U/4U chassis; "fan stop" in a server is rarely possible because BMCs (iDRAC, IPMI) override at firmware level and ramp to 100 % on sensor-loss or low-RPM. The Dell iDRAC documentation (`dell.com/support/kbdoc/en-us/000123186`) explicitly states "when the iDRAC, BMC or CMC/OME-M loses connection to the sensor suite, the fans return to the unmanaged speed (full)." ventd in a server context is therefore typically *cooperative* with the BMC, not authoritative.

**Observed slopes (limited primary data; server fleets rarely allow probe-style fan-stop testing):**

| State | dT/dt | Source/Spread |
|---|---|---|
| Idle | 0.05–0.2 °C/s | Principled Technologies Dell PowerEdge HS5620 thermal whitepaper |
| 50 % SPECrate | 0.3–1.5 °C/s | StorageReview EPYC Genoa review |
| 100 % SPEC FP/AVX-512 with fan disabled | 1–6 °C/s | Principled Technologies fan-failure scenario |

**Defensible defaults (Class 4):**
- **dT/dt abort = 1.0 °C/s** (more conservative than desktops because data lost on a server is more expensive than on a desktop; also because the BMC may already be ramping and ventd's probe could fight the BMC).
- **T_abs headroom = 20 °C below Tjmax/Tctl** → for EPYC Genoa abort at ~75 °C Tctl; for Xeon Platinum 8480+ abort at 70 °C; for Threadripper 7000WX abort at 75 °C.

Server class also gets an additional rule not present elsewhere: **if a BMC is detected (`/dev/ipmi*` present, or `dmidecode` shows ASF/IPMI), envelope C is gated behind an explicit `ventd.toml allow_server_probe = true`.** Default is *refuse to probe*. This is hardware_refusal-adjacent.

#### 4.5 Class 5 — Mobile/laptop CPU (Tiger Lake i7-1165G7, Alder Lake-P, Raptor Lake-P, Phoenix Ryzen 7 7840HS, Strix Point)

Tjmax across this segment: 100 °C is the standard operating ceiling (Intel ARK and AMD product pages). Confirmed via Tom's Hardware Tiger Lake-H review (i9-11980HK averaging 85.77 °C with HWiNFO reporting throttle) and HP support thread for i7-14650HX showing "90–100 °C" as normal.

Critical wrinkle: **laptops are EC-mediated**. The fan PWM is controlled by the embedded controller, not directly by the OS in most chassis. ventd's PWM writes via `/sys/class/hwmon/.../pwm*` may be *advisory only*. The kernel driver list relevant to laptop EC fan control includes: `dell-smm-hwmon`, `thinkpad_acpi`, `asus_wmi_sensors`, `nbfc-linux` (Notebook FanControl). On models where the EC ignores OS PWM writes, Envelope C is **inapplicable** — ventd should detect this via a probe handshake (write known PWM, verify RPM changes) and refuse Envelope C if the handshake fails.

**Observed slopes:**

| State | dT/dt | Source/Spread |
|---|---|---|
| Idle | 0.05–0.3 °C/s | Phoronix i7-1165G7 review; HP community |
| 50 % | 1–4 °C/s | Tom's Hardware Tiger Lake-H review |
| 100 % CB23 / Prime95 | 3–15 °C/s | Tom's Hardware forum; Steam Community ROG threads. *Wide spread — laptops vary enormously by chassis.* |

**Defensible defaults (Class 5):**
- **dT/dt abort = 2.0 °C/s** (matches Class 1 because thin-and-light laptops can hit junction slopes as steep as desktops).
- **T_abs headroom = 15 °C below Tjmax** → 85 °C abort on a 100 °C Tjmax part. Note: many laptop ECs already keep the fan curve hot-biased (acceptable design temps to 95 °C documented by Intel), so 85 °C may trigger frequent aborts on thin-and-light chassis. This is acceptable per the asymmetric-cost rule (recoverable user complaint vs. damage).
- **Envelope C is gated by EC-handshake success.** If `pwm_enable=1` write does not actually slow fan RPM within 5 s, envelope C aborts before the probe begins.

#### 4.6 Class 6 — Mini-PC / NUC class (N100, N150, N305, low-power Intel/AMD)

Intel N100 Tjmax = 105 °C (confirmed by Netgate forum thread documenting BIOS Temperature Activation Offset behavior with `TjMax = 105`). N150/N305 same family same Tjmax. Many of these systems are passively cooled (chassis-as-heatsink) and have *no fan to probe* — Envelope C is degenerate. For systems with a small ≤80 mm fan, the heatsink mass is small and the time constant is short.

**Observed slopes (ZKmagic N100 fanless thermal characterization):**

| State | dT/dt | Source |
|---|---|---|
| Idle (passive) | 0.01–0.1 °C/s | ZKmagic ambient study |
| 50 % | 0.2–1 °C/s | Topton/Netgate forum data |
| 100 % (full TDP=6 W/9 W) | 0.5–3 °C/s | Netgate forum BIOS-tuning thread |

**Defensible defaults (Class 6):**
- **dT/dt abort = 1.0 °C/s**
- **T_abs headroom = 20 °C below Tjmax** → abort at 85 °C on an N100. Generous because Tjmax is high (105) and parts are cheap to replace; abort early to avoid surprising users with hot chassis.
- **Class detection rule:** if no PWM-controllable fan is enumerated (`pwm*_enable` cannot be set to 1 on any hwmon), Envelope C is skipped and ventd reports `class=passive_minipc`.

### 5. Class 7 — Storage-heavy NAS (TrueNAS / Unraid / similar)

#### 5.1 Thermal target: HDDs (the things ventd is protecting)

**HDD operating range (from datasheets):**

| Drive | Min | Max (drive case) | Notes |
|---|---|---|---|
| WD Red Pro (WD201KFGX, etc.) | 0 °C | 65 °C | Datasheet 2879-800002 series |
| Seagate IronWolf | 0 °C | 70 °C (drive case max) | IronWolf Product Manual Rev F |
| Seagate IronWolf Pro | 5 °C | 70 °C; "does not recommend operating at sustained case temperatures above 60 °C" | IronWolf Pro Product Manual Rev B |
| Toshiba N300 / MG09 | 5 °C | 60 °C (datasheets vary 55–65) | Toshiba product specs |
| Seagate Exos X18/X20 | 5 °C | 60 °C | Exos product brief |
| WD Red Plus | 0 °C | 65 °C | Datasheet |

**Sweet spot (lifetime correlation):** Backblaze's 2014 analysis ("What Is a Safe Hard Drive Temperature Range?", `backblaze.com/blog/hard-drive-temperature-does-it-matter`) found *no correlation* between op-temp and AFR within manufacturer-allowed ranges, with the exception of one Seagate Barracuda 1.5 TB model. The University of Virginia / Microsoft 2013 study (cited in Wikipedia / Wikibooks) found AFR rising from ~4 %/yr at 27 °C to ~10 %/yr at 44 °C, fitting an Arrhenius model with doubling per 12 °C. Google's 2007 paper (Pinheiro et al.) found a U-shaped curve with optimum 37–46 °C. The **conservative consensus** taken across these is **25–40 °C operating sweet spot**, with reduced-life territory beginning ~45 °C and a hard manufacturer ceiling at 55–60 °C.

Backblaze Q3 2023 Drive Stats (`backblaze.com/blog/backblaze-drive-stats-for-q3-2023`) explicitly used **60 °C as the manufacturer-max threshold for most drives, 55 °C for 12/14/16 TB Toshiba.**

**HDD thermal time constant:** Per the Seagate IronWolf Pro Product Manual Rev. B, the *non-operating* max temperature gradient is 20 °C/hour ≈ **0.33 °C/min ≈ 0.0056 °C/s** — this is a transport spec but it tells us the design assumes thermal changes happen at *minutes* timescales. Operating thermal time constants on a populated 3.5" drive are typically 5–15 minutes (community forum data on TrueNAS cooling threads consistently shows 10–30 minutes from drive spinup-cold to thermal steady state).

#### 5.2 Thermal observable: backplane / SAS sensors (what ventd is reading)

Backplane sensor types and placement, drawn from the Klara Systems article (`klarasystems.com/articles/managing-disk-arrays-on-freebsd-truenas-core/`) and TrueNAS Community sesutil/SES threads:

- **SES enclosure sensors** (`sg_ses` / `sesutil show`): enclosure-management chips like the SuperMicro MG9071 expose 1–4 temperature sensors per enclosure, typically labeled "ChipDie" (the SES controller's own die — *not the drive bay*) and "FrontPanel"/"Rear" placeholders. **Critical caveat:** the "ChipDie" reading is the enclosure-management chip itself; it is not a proxy for any drive bay temperature.
- **Drive-internal temperature via SMART** (`smartctl -A` attribute 194; or SCT log on SATA). SCT default sample period is **1 minute**, log interval 1 minute (kept for 8 hours). This is the highest-fidelity drive temperature available without SAS proprietary commands.
- **HBA temperature** (LSI/Broadcom 2308/9300 series via `mpsutil`/`mprutil`/`storcli`): present on 9300+ generation, absent on 9211 / 2008 silicon. Typical reading 40–75 °C; this measures the HBA chip, *not* drives, and rises mostly with PCIe/firmware activity.
- **Motherboard / PCH sensors** via `lm-sensors` / `/sys/class/hwmon`: ambient air entering the chassis via a thermistor on the front panel, where one is fitted. Common on Supermicro X10/X11/X12 platforms; absent on consumer boards.

**Read latency / polling cost:** smartctl invocations are typically 100–300 ms per drive, and on a sleeping drive an SMART read can spin the drive up. This is the dominant cost. The ventd-correct policy is:
- Poll SES enclosure sensors at **1 Hz** (cheap, doesn't touch drives).
- Poll drive SMART temperature (Attr 194) at **0.1 Hz (every 10 s)** when active; back off to **0.0167 Hz (every 60 s)** when drives appear idle to avoid spin-up. This matches the SCT 1-min internal sample cadence — finer polling buys nothing.
- Cache the last-known temperature with a TTL of 90 s; if a drive is in standby and SMART is unavailable, treat the cached value as still valid up to TTL.

**Drive temperature gradient across a populated bay:** TrueNAS community data (Seagate 8 TB Temperature thread; multiple QNAP forum discussions) consistently shows **3–10 °C front-to-rear delta** across a fully populated 8-bay, with the middle bays running 3–5 °C above the edge bays. The hottest drive is the relevant signal for ventd.

**SAS SSD vs spinning rust:** SAS SSDs (modern) tolerate **70 °C drive case** typically; thermal time constant is shorter (~1–3 minutes vs 5–15) because mass is lower. Backblaze 2022 SSD Edition stats show SSD temp range 20–61 °C in their pods. Treat them as a faster-responding subset of Class 7.

#### 5.3 NAS abort thresholds

Because the time constant is **minutes**, dT/dt is expressed in °C/min, not °C/s. ventd computes a 5-minute moving average dT/dt for HDDs.

**Defensible defaults (Class 7, NAS-HDD):**
- **dT/dt abort = 1.0 °C/min** (≈ 0.017 °C/s), measured over a 5-minute moving window. Conservative: a healthy fan-stop should produce <0.5 °C/min on a typical 8-bay NAS with rear exhaust. 1 °C/min sustained means "this is going somewhere bad in 30 minutes; abort now."
- **T_abs headroom for HDDs:** abort if any drive case reading reaches **50 °C** (the manufacturer max minus 10 °C headroom for sensor lag and inter-bay gradient). For mixed fleets, the lowest-rated drive sets the floor: a NAS with one Toshiba 16 TB (max 55 °C) abort-trips at **45 °C**.
- **T_abs headroom for SAS SSDs:** abort at **60 °C**.
- **T_abs headroom for HBA:** abort at **75 °C** (manufacturer atmospheric range top is 55 °C, but the chip junction tolerates more; this is the mpsutil/storcli ROC reading).
- **Sensor-loss policy:** if any in-pool drive's temperature has been unread for >2× polling interval, ventd treats the zone as untrusted and aborts envelope C. Storage data is not worth gambling on a stale reading.

### 6. Sensitivity analysis: ambient excursions

Headroom is set against Tjmax, which is itself ambient-independent. But the *probability of hitting the headroom abort* is heavily ambient-dependent. Using a first-order resistive thermal model (Tjunction = Tambient + P × R_θJA):

| Ambient | Steady-state ΔT (Tj−Ta) at 100 % load | Headroom remaining (Class 1, abort = 85 °C) |
|---|---|---|
| 25 °C | ~50 °C (well-cooled), ~70 °C (poorly cooled) | 35 °C / 15 °C |
| 35 °C | ~50 °C / ~70 °C | 25 °C / 5 °C — **probe risky** |
| 45 °C | ~50 °C / ~70 °C | 15 °C / **negative — probe must abort** |

**Practical rule:** ventd reads ambient (chassis intake or motherboard ambient sensor where available) at probe-start and **refuses to enter Envelope C if (Tjmax − T_ambient) < 60 °C**. For HDDs in a NAS-in-a-closet at 35 °C ambient, the 50 °C abort threshold gives only 15 °C of working margin — the probe step magnitude must be reduced or the probe deferred to a cooler hour.

For NAS specifically, the abort threshold itself **derates with ambient**: at 35 °C ambient the HDD abort drops from 50 °C to 47 °C; at 45 °C it drops to 45 °C. ventd implements this as a linear derating: `T_abort = min(50, manufacturer_max − 10, ambient + 15)`.

### 7. Fan spin-up time vs dT/dt: when abort itself is too slow

Modern PWM fans take **0.5–3 s** to ramp from min RPM to 100 % once the duty cycle is set (PWM fan datasheets, Cadence and EKWB blog references; Noctua datasheets cite ~1 s for NF-A14 to reach 90 % of target RPM). Once at 100 %, **air mass-flow rises within 100–200 ms** — the rotor inertia, not the air, is the rate-limiting step. End-to-end: from ventd writing 255 to `pwm1` to airflow at the heatsink, expect **1.5–4 s total**.

In that 1.5–4 s window, at the worst-case dT/dt for each class:

| Class | Worst-case dT/dt | ΔT during 4 s ramp | Implication |
|---|---|---|---|
| 1 (HEDT air) | 20 °C/s | 80 °C — *fan spin-up cannot save the CPU* | Defense-in-depth on TCC/Tjmax throttle |
| 2 (HEDT AIO) | 10 °C/s | 40 °C | Same |
| 3 (mid desktop) | 8 °C/s | 32 °C | Marginal |
| 4 (server) | 6 °C/s | 24 °C | Acceptable + BMC |
| 5 (laptop) | 15 °C/s | 60 °C | EC may already be ramping |
| 6 (NUC) | 3 °C/s | 12 °C | Acceptable |
| 7 (NAS HDD) | 0.017 °C/s | 0.07 °C | Trivial |

**Conclusion:** for Classes 1, 2, 3, 5 ventd's "abort and ramp" is *not* the primary thermal defense at the worst case — Intel's TCC (TM1/TM2) and AMD's PROCHOT_L are. ventd's job is to abort *before the worst case*, while the slope is still in the 2–5 °C/s "early warning" band. This is the dT/dt threshold's job. Choosing dT/dt = 2.0 °C/s for Class 1/5 means ventd reacts ~5× before the worst-case scenario; that gives the abort-and-ramp 4 s of headroom even at the 10 °C/s peak, and the silicon TCC handles the residual.

### 8. Source-spread report and HIL priorities

Spread (high → low):

1. **HIGH SPREAD — Class 5 laptop dT/dt under 100 %:** 3–15 °C/s, 5× spread, chassis-dependent. **HIL priority: HIGH.** Laptops in fleet should each be measured.
2. **HIGH SPREAD — Class 1 dT/dt at 100 % AVX:** 5–20 °C/s, 4× spread. Cooler-quality dependent. **HIL priority: HIGH** on the 13900K node.
3. **MODERATE SPREAD — Class 4 server slopes:** limited public data. **HIL priority: MEDIUM**, but Proxmox 5800X covers the closest analog and is a server-role machine; treat its result as a server-class lower-bound.
4. **MODERATE SPREAD — HDD operating sweet spot:** 25–40 °C consensus but with one published study (UVA) saying steady increase from 27 to 44 °C and another (Backblaze) saying no correlation. **HIL priority: LOW** — defaults are conservative regardless.
5. **TIGHT AGREEMENT — Tjmax values per part:** unanimous from vendor documentation. **HIL priority: LOW.** Already trusted.
6. **TIGHT AGREEMENT — ACPI _PSV/_CRT semantics, kernel thermal trip types:** kernel docs canonical. **HIL priority: NONE.**
7. **TIGHT AGREEMENT — HDD case max temperature:** 60 °C consensus (with 70 °C absolute on some Seagate IronWolf Pro). **HIL priority: LOW.**
8. **MODERATE SPREAD — Fan PWM-to-airflow latency:** 0.5–3 s for ramp, 100–500 ms for airflow rise, total 1–4 s. **HIL priority: MEDIUM** — measure on each fleet member, since the worst-case latency directly sets the dT/dt safety margin.

### 9. HIL fleet test plan

Fleet:
- **Proxmox host** (5800X + RTX 3060) — Class 3 representative; closest analog to Class 4 server class.
- **MiniPC (Celeron)** — Class 6 representative.
- **13900K + RTX 4090 desktop (dual-boot)** — Class 1 representative; the worst-case validation target.
- **3 laptops (any OS)** — Class 5, three chassis variations.

**Per-member tests:**

| Fleet member | Class validated | Tests |
|---|---|---|
| Proxmox 5800X | 3 (and lower-bound 4) | T1: idle dT/dt fan-stop (5 min). T2: stress-ng matrix 50 % fan-stop (90 s, abort if T_pkg ≥ 78 °C). T3: stress-ng full + AVX2 fan-stop (60 s, abort at T_pkg = 78 °C OR dT/dt ≥ 1.5 °C/s). T4: ambient sensitivity — repeat T2 at room temps 22 °C and ~30 °C (closet door closed). |
| MiniPC Celeron | 6 | T1, T2 idle/medium with passive abort at 85 °C. T5: detection — assert ventd correctly identifies as passive_minipc and skips Envelope C if no PWM-controllable fan exists. |
| 13900K + 4090 | 1 (and 2 if AIO swap available) | T6: y-cruncher AVX-512 fan-stop, abort at T_pkg = 85 °C OR dT/dt ≥ 2.0 °C/s. **Most critical test** — measures peak observable slope. T7: TCC cross-check — verify Intel TCC engages independently at 100 °C and ventd's abort happens earlier. T8: AIO-swap (if available) — re-run T6 to capture Class 2 slope. |
| Laptop 1 (thin & light, Tiger/Alder Lake-P) | 5 | T9: EC handshake test — write pwm_enable=1, verify RPM responds. If not, assert ventd refuses Envelope C. T10: if handshake passes, Cinebench R23 fan-stop with abort at T_pkg = 85 °C OR dT/dt ≥ 2.0 °C/s. |
| Laptop 2 (gaming, Raptor Lake-HX or Phoenix-HS) | 5 | T9, T10. Likely shows highest dT/dt of fleet. |
| Laptop 3 (older, e.g., Comet Lake-U) | 5 | T9, T10 — broaden coverage. |
| (Implicit) NAS — *not in fleet but recommended* | 7 | The fleet has no NAS. Recommend a virtual proxy: simulate slow polling against a USB-attached HDD on Proxmox or on a secondary spinning-disk mounted in any fleet member; validate the 5-minute moving-average dT/dt computation, the 60-second fallback polling cadence, and the SES discovery code path. **Real NAS HIL is a known gap.** |

**Pass criteria (all classes):** ventd never lets T_junction exceed Tjmax − 5 °C during the test (5 °C of additional cushion above the abort threshold itself). If any test allows T_junction ≥ Tjmax − 5 °C, that abort threshold must be tightened.

### 10. Recommended config schema

```toml
# /etc/ventd/ventd.toml — Envelope C abort defaults
[envelope_c]
enabled = true
require_ec_handshake = true       # laptop class
refuse_if_bmc_present = true      # server class

[envelope_c.class_detection]
# CPU model match (cpuid family/model/stepping or /proc/cpuinfo "model name" regex)
# is the primary signal; storage role secondary.
storage_role_signals = ["any /dev/sd* with rotational=1 in SMART", "zpool_status_present"]
passive_signals      = ["no pwm*_enable writable hwmon"]

[envelope_c.thresholds]
# Format: [class_name] dT_dt_C_per_s, T_abort_offset_C_below_tjmax, ambient_min_headroom_C
desktop_hedt_air     = { dT_dt = 2.0, abs_offset = 15, ambient_headroom = 60 }
desktop_hedt_aio     = { dT_dt = 1.5, abs_offset = 15, ambient_headroom = 60 }
desktop_midrange     = { dT_dt = 1.5, abs_offset = 12, ambient_headroom = 55 }
server_cpu           = { dT_dt = 1.0, abs_offset = 20, ambient_headroom = 50, gated = true }
laptop               = { dT_dt = 2.0, abs_offset = 15, ambient_headroom = 55, ec_handshake = true }
mini_pc              = { dT_dt = 1.0, abs_offset = 20, ambient_headroom = 55 }
nas_hdd              = { dT_dt_per_min = 1.0, abs_C = 50, abs_C_toshiba_largecap = 45, window_min = 5 }

[envelope_c.user_override]
# Users may override per zone with explicit acknowledgment.
# Override paths: ventd.toml [override.<zone>], or `ventdctl override <zone> --dT_dt=...`
# Overrides are LOGGED and may not exceed the safety_ceiling.
safety_ceiling_dT_dt_C_per_s = 5.0     # no override may push dT/dt above this
safety_ceiling_offset_C      = 5       # no override may set abort closer than 5 °C below Tjmax
```

### 11. Per-class confidence summary

| Class | Confidence | Key driver |
|---|---|---|
| 1 (HEDT air) | Medium | Wide 100 %-load slope spread; single-vendor-cooler validation |
| 2 (HEDT AIO) | Medium | Coolant-saturation transient under-characterized in public data |
| 3 (Mid desktop) | High | Tight Tjmax data, reasonable slope data, well-bounded |
| 4 (Server) | Low-Medium | Limited public fan-stop data; production probes constrained |
| 5 (Laptop) | Low | Extreme chassis-to-chassis variance; EC behavior model-specific |
| 6 (Mini-PC) | High | Small parameter space; passive systems trivially handled |
| 7 (NAS) | High | Datasheet-grounded; conservative; thermal time constant generous |

---

## Artifact 2 — Spec-ready findings appendix block

```
### R4 — Envelope C Abort Thresholds

- **Defensible default(s):**

  | Class                                | dT/dt abort     | T_abs (offset below Tjmax)           | Ambient gate     |
  |--------------------------------------|-----------------|--------------------------------------|------------------|
  | 1. Desktop HEDT, air (13900K/14900K/7950X/9950X3D) | 2.0 °C/s   | Tjmax − 15 (e.g. 85 °C on 100 °C parts; 80 °C on 95 °C parts; 75 °C on 89 °C X3D parts) | (Tjmax − T_amb) ≥ 60 °C |
  | 2. Desktop HEDT, AIO                 | 1.5 °C/s        | Tjmax − 15                           | ≥ 60 °C          |
  | 3. Mid-range desktop (5800X/5700X/12700K/13700K) | 1.5 °C/s | Tjmax − 12 (78 °C on 90 °C parts; 88 °C on 100 °C parts) | ≥ 55 °C |
  | 4. Server CPU (Xeon Pt 8480+, EPYC Genoa/Bergamo, TR-PRO) | 1.0 °C/s | Tjmax − 20 (75 °C Tctl on EPYC; 70 °C on Xeon Pt) | ≥ 50 °C; **gated: refuses Envelope C if BMC present unless explicitly allowed** |
  | 5. Mobile/laptop (Tiger/Alder/Raptor-P, Phoenix, Strix) | 2.0 °C/s | Tjmax − 15 (typically 85 °C) | ≥ 55 °C; **gated: requires successful EC PWM-handshake** |
  | 6. Mini-PC / NUC (N100/N150/N305)    | 1.0 °C/s        | Tjmax − 20 (85 °C on 105 °C N-series); skipped on passive (no fan) | ≥ 55 °C |
  | 7. NAS — HDDs                        | 1.0 °C/min over 5-min window | abort case-temp = min(50 °C, mfg_max − 10 °C, T_amb + 15 °C); 45 °C for ≥12 TB Toshiba | n/a (HDD limit is mfg-rated) |
  | 7. NAS — SAS SSDs                    | 2.0 °C/min over 2-min window | 60 °C case                           | n/a              |
  | 7. NAS — HBA / SES chips             | 1.0 °C/min      | 75 °C ROC                            | n/a              |

- **Citation(s):**
  - ACPI 6.5 Spec §11 Thermal Management — `https://uefi.org/specs/ACPI/6.5/11_Thermal_Management.html` (defines _PSV/_HOT/_CRT semantics, 5 °C granularity guidance).
  - Linux kernel thermal subsystem — `Documentation/thermal/sysfs-api.txt` and `drivers/acpi/thermal.c` (THERMAL_TRIP_ACTIVE/PASSIVE/HOT/CRITICAL definitions; orderly_poweroff() semantics).
  - Intel ARK pages for 13900K/14900K (Tjmax = 100 °C) and SkatterBencher *Intel Thermal Velocity Boost* — `https://skatterbencher.com/intel-thermal-velocity-boost/` (TVB 70 °C threshold, OCTVB semantics).
  - AMD product pages and AMD EPYC 9004 documentation — `https://www.amd.com/en/products/processors/server/epyc/4th-generation-9004-and-8004-series.html`; AMD Tctl description corroborated via k10temp driver and Ryzen Master technical notes; 7950X3D Tjmax = 89 °C confirmed via AMD product page.
  - Tom's Hardware *Core i9-13900K Cooling Tested* — `https://www.tomshardware.com/features/intel-core-13900k-cooling-tested` (Cinebench R23 fan-stop empirical thermal data).
  - Seagate IronWolf Pro Product Manual Rev. B (drive case max 70 °C, sustained-not-recommended >60 °C, transport gradient 20 °C/h) — `https://www.seagate.com/content/dam/seagate/migrated-assets/www-content/product-content/ironwolf/en-us/docs/100835908f.pdf`.
  - Backblaze drive temperature analyses — `https://www.backblaze.com/blog/hard-drive-temperature-does-it-matter/` and `https://www.backblaze.com/blog/backblaze-drive-stats-for-q3-2023/` (Q3 2023 — 60 °C / 55 °C max thresholds).
  - Pinheiro et al., *Failure Trends in a Large Disk Drive Population* (Google, FAST '07) — referenced via Wikipedia/Wikibooks Minimizing HDD Failure (37–46 °C optimum); UVA/Microsoft 2013 study (Arrhenius doubling per 12 °C).
  - Klara Systems — *Managing Disk Arrays on FreeBSD/TrueNAS Core* — `https://klarasystems.com/articles/managing-disk-arrays-on-freebsd-truenas-core/` (sesutil semantics; SES temperature element types).
  - Dell *Improving energy efficiency in the data center* / Principled Technologies — PowerEdge HS5620 thermal resiliency whitepaper (server fan-failure scenario timings).
  - Dell PowerEdge KB `000123186` — iDRAC thermal algorithm; sensor-loss-equals-100 % fan default.
  - ScienceDirect *Chip Temperature* topic article — chip-level thermal time constants in milliseconds-to-seconds range.
  - Netgate Forum Topton N100 thread — N100 Tjmax = 105 °C, TCC offset behavior — `https://forum.netgate.com/topic/186104/topton-n100-reporting-402-mhz/80`.

- **Reasoning summary:**
  - **Class 1 (HEDT air):** Heat density on 13900K/14900K is the highest in scope. Public data shows 100 °C reached "almost immediately" on Cinebench R23, implying junction slopes 5–20 °C/s under 100 % load. dT/dt = 2.0 °C/s aborts at the bottom of that band, leaving 4 s for fan ramp before the 10 °C/s mid-case scenario reaches Tjmax. T_abs = Tjmax − 15 is 3× the ACPI 5 °C granularity quantum and matches Intel's TVB preferred operating window.
  - **Class 2 (HEDT AIO):** Coolant adds ~120 s of effective inertia at the *radiator*, not at the die, so the early slope is gentler but late-probe failures are catastrophic (saturated coolant). dT/dt slightly tighter (1.5 °C/s); same headroom.
  - **Class 3 (mid-range):** Lower heat density, lower steady-state. Tjmax − 12 is acceptable because workload spikes rarely sit close to Tjmax; a sudden climb is more diagnostic.
  - **Class 4 (server):** Most-conservative numbers because data loss is most-expensive on servers; also defaults to *gated refusal* in BMC-managed chassis. Tctl − 20 °C accommodates AMD Tctl offset and non-uniform reading across chiplets.
  - **Class 5 (laptop):** Mirrors Class 1 dT/dt because thin-and-light chassis can produce desktop-class junction slopes. Adds an EC-handshake gate: ventd aborts before probe if PWM writes don't measurably change RPM.
  - **Class 6 (mini-PC):** Generous T_abs because Tjmax is high (105 °C) and parts are inexpensive; aborts conservatively to avoid surprising users with hot chassis surfaces. Skips Envelope C entirely on truly passive systems.
  - **Class 7 (NAS):** Time scale is *minutes*, dT/dt expressed in °C/min and computed over a 5-min moving window. Headroom of mfg_max − 10 °C accommodates inter-bay gradient and SMART read-cadence lag (1 min sample). Lowest-rated drive sets the floor for the pool; sensor-loss aborts the probe.

- **HIL-validation flag:** Yes — by class:
  - Class 1: **13900K + RTX 4090** runs T6 (y-cruncher AVX-512 fan-stop with abort at T_pkg ≥ 85 °C OR dT/dt ≥ 2.0 °C/s) and T7 (Intel TCC cross-check). **HIGH priority** — biggest source-data spread.
  - Class 2: **13900K + AIO swap** runs T8 (Cinebench R23 fan-stop on AIO). **MEDIUM priority** if hardware available; otherwise inherits Class 1 numbers.
  - Class 3: **Proxmox 5800X + RTX 3060** runs T1–T4 (idle, 50 %, 100 %, ambient sensitivity). **MEDIUM priority.**
  - Class 4: **No native fleet member.** Proxmox 5800X provides a *lower-bound* analog under T2/T3. **HIGH priority gap.**
  - Class 5: **All three laptops** run T9 (EC handshake) and T10 (Cinebench R23 fan-stop, abort 85 °C / 2.0 °C/s). **HIGH priority** — chassis variance is the dominant uncertainty.
  - Class 6: **MiniPC Celeron** runs T1, T2, and T5 (passive-class detection). **LOW priority** — defaults are conservative.
  - Class 7: **No native fleet member.** Recommend USB-HDD or Proxmox-attached spinning disk to validate cadence/policy code paths only. **HIGH priority gap; document as such.**

- **Confidence:**
  - Class 1: **Medium** (slope spread is wide; abort logic is conservative)
  - Class 2: **Medium** (coolant transient under-characterized publicly)
  - Class 3: **High**
  - Class 4: **Low–Medium** (limited public fan-stop data; mitigated by gated-refusal default)
  - Class 5: **Low** (chassis variance dominates)
  - Class 6: **High**
  - Class 7: **High** (datasheet-grounded; minutes-scale gives generous reaction time)

- **Spec ingestion target:** `spec-v0_5_3-envelope-c.md`
```

### Closing notes for spec authors

Three items are explicit known-gaps that the spec should call out as deferred-to-HIL rather than silently shipped:

1. **Server class without a server in the fleet.** Class 4 is shipped with the most-conservative numbers in the table and gated behind a BMC-detection refusal. The first time a user runs ventd on a real BMC-managed server with `allow_server_probe=true` should be treated as field validation, with telemetry collected.
2. **NAS class without a NAS in the fleet.** The SES enumeration code path and the 5-min moving-average dT/dt computation are unvalidated on real backplane hardware. Defaults are intentionally so conservative that an early abort is the expected failure mode.
3. **Laptop EC handshake.** The handshake is defined in spec but its taxonomy of "PWM works" vs "PWM is advisory" vs "PWM is silently ignored" requires real-fleet measurement on each laptop chassis. Tag affected zones `class=laptop_ec_unknown` until measured.

The thresholds in this document are intended to be **the floor** (most-conservative). HIL data may *raise* a threshold (allow more aggressive probing) where measurements support it; HIL data may **never** *lower* a threshold (allow probing tighter to Tjmax than the table) without a documented spec amendment and reviewer sign-off.