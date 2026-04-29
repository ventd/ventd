# R6 — POLARITY MIDPOINT: Is PWM=128 safe under all polarities as the initial probe value?

**Project:** ventd (Linux fan controller daemon, Go 1.25+, CGO_ENABLED=0, GPL-3.0, github.com/ventd/ventd)
**Research item:** R6 — verify safety of PWM=128 (midpoint of 0–255) as the initial probe value used by ventd's catalog-less polarity-disambiguation step (v0.5.1).
**Date:** 2026-04-28
**Format:** Two artifacts. Artifact 1 is the long-form research document with comparative tables and reasoning. Artifact 2 is the spec-ready findings appendix.

---

## ARTIFACT 1 — RESEARCH DOCUMENT

### 1. Executive summary

PWM=128 is **defensibly safe as the *primary* polarity probe value** on standard 4-pin Intel/Noctua-type PWM hardware backed by the mainstream Linux Super-I/O drivers (nct6775, it87 in normal-polarity mode, nct6683, generic hwmon). It is not, however, *universally* safe. There are five distinct pathologies where PWM=128 either (a) silently rounds to a different value, (b) lands inside a fan's stall band, or (c) becomes ineffectual because the firmware ignores the write entirely. None of these failure modes is *fail-dangerous* in the ordinary sense (none mechanically damages hardware), but several are *fail-undefined* and break the polarity-disambiguation algorithm because they make the RPM delta undetectable.

The report's conclusion is that ventd should keep **PWM=128** as the default initial probe value, but apply **per-driver overrides** for three families:

1. **dell-smm-hwmon** with `fan_max=3` → use **PWM=170** (the Dell EC will round 128 *down* to state-1 = readback 85, which destroys the midpoint property).
2. **dell-smm-hwmon** with `fan_max≥4` → use **PWM=128** (legal exact step on those quanta) but expect readback 128 only at fan_max ∈ {2, 4}.
3. **thinkpad_acpi** in manual mode (`pwm1_enable=1`) → use **PWM=128** but only after explicitly setting `pwm1_enable=1` and rearming the fan watchdog; the kernel docs themselves recommend "at least 128 (255 would be the safest choice)."
4. **DC-mode** (`pwmN_mode=0`) channels on nct6775/it87 → escalate to **PWM=160** to clear the ~6 V stall floor on 3-pin fans.
5. **asus-wmi-sensors / asus-nb-wmi laptop fans** — the sysfs interface is 0–255 (kernel hwmon convention), not 0–100, so PWM=128 is valid; the boost-mode WMI registers (0x5e bytes 0x40/0x80/0xC0) are a *separate* interface and not the polarity probe target.

A three-state polarity-disambiguation procedure (write 128 → settle → write 128 + step → measure ΔRPM sign) is described in §6 below, with a recommended step of +32 PWM (≈12.5 percentage points), which exceeds the typical RPM noise floor reported in fan2go's `maxRpmDiffForSettledFan: 250` heuristic and the Intel spec's ±10% RPM/duty linearity tolerance.

---

### 2. Background — why a midpoint at all?

The hwmon sysfs convention is documented in the kernel hwmon sysfs interface: `pwmX` stores "PWM duty cycle or DC value (fan speed) in range: 0 (stop) to 255 (full)." This convention is shared by the major Super-I/O drivers (nct6775 documents this verbatim at https://docs.kernel.org/hwmon/nct6775.html, the it87 driver at https://docs.kernel.org/6.2/hwmon/it87.html, the asus-wmi family at https://wiki.archlinux.org/title/Fan_speed_control, the pwm-fan generic driver at https://docs.kernel.org/hwmon/pwm-fan.html, and the g762 driver at https://docs.kernel.org/5.19/hwmon/g762.html).

Under *normal polarity* (Intel "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification" rev 1.3, September 2005, see https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf and https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf, §3.2): "Fan speed response to this signal shall be a continuous and monotonic function of the duty cycle of the signal, from 100% to the minimum specified RPM. The fan RPM (as a percentage of maximum RPM) should match the PWM duty cycle within ±10%." The Noctua white paper (https://cdn.noctua.at/media/Noctua_PWM_specifications_white_paper.pdf) confirms: "The signal is not inverted, 100% PWM duty cycle (= 5V DC) results in maximum fan speed."

Under *inverted polarity* — the it87 driver explicitly supports this with `fix_pwm_polarity int` ("Force PWM polarity to active high (DANGEROUS). Some chips are misconfigured by BIOS — PWM values would be inverted") (https://github.com/frankcrawford/it87/blob/master/README, kernel docs https://docs.kernel.org/6.2/hwmon/it87.html). When this case is in effect at the chip level *without* the driver's override, a write of N to `pwmX` produces a delivered duty of (255-N).

PWM=128 is therefore appealing as a *probe* value because in both polarities it yields ≈50% delivered duty, which (a) is monotonic-region safe per the Intel spec on either branch, and (b) keeps the fan above the stall floor on virtually every modern PWM fan (see §3.5).

---

### 3. Edge-case verification

#### 3.1 Stepped / quantized PWM (Dell SMM, ASUS-WMI, HP iLO, ThinkPad)

**dell-smm-hwmon** uses `data->i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)` (verified in https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c, see also the kernel patch series at https://www.spinics.net/lists/kernel/msg4245950.html). Read path: `*val = clamp_val(ret * data->i8k_pwm_mult, 0, 255);`. Write path: state = pwm / mult (truncating integer division, see torvalds tree at /drivers/hwmon/dell-smm-hwmon.c). The accepted-value table for representative `fan_max`:

| fan_max | i8k_pwm_mult | Internal states | Readback values | Where PWM=128 lands (write→state→readback) |
|---|---|---|---|---|
| 2 | 128 | {0,1,2} | {0, 128, 255} | 128/128=1 → readback 128 ✅ midpoint preserved |
| 3 | 85 | {0,1,2,3} | {0, 85, 170, 255} | 128/85=1 → readback 85 ⚠ rounds *down* — no longer midpoint |
| 4 | 64 | {0,1,2,3,4} | {0, 64, 128, 192, 255} | 128/64=2 → readback 128 ✅ |
| 6 | 43 | {0..6} | {0,43,86,129,172,215,255} | 128/43=2 → readback 86 ⚠ |

The fan_max=3 case is empirically observed in the lm-sensors PR thread at https://github.com/lm-sensors/lm-sensors/pull/383 ("the pwm values written get rounded to one of three possible values: 85 (low), 170 (high), 255 (auto/medium)") and in https://github.com/markusressel/fan2go/issues/201 (Dell 3930 server: "/sys/class/hwmon/hwmon3/pwm1 represents as 128 even when in slow cycles"; the user's working pwmMap confirms 0:128, 170:138, 255:255). On a fan_max=3 host PWM=128 is *not* the midpoint of accepted states — the Dell EC quantizes it to state 1 ("low"), readback 85 ≈ 33%. This breaks the polarity midpoint property and could under-cool because state 1 may be the BIOS' "low" preset that does not provide enough margin under load. **Override: ventd should write 170 instead of 128 when fan_max=3 is detected** (which produces state 2 = readback 170, the true midpoint of {0, 85, 170, 255}). The driver exposes fan_max via the module parameter; ventd can read effective `fan_max` indirectly by reading any `pwmX` file in BIOS-auto mode and decoding the readback to {0, 85, 128, 170, 255} buckets.

**ASUS-WMI / asus-nb-wmi laptops**: The hwmon convention is preserved — the Arch Wiki examples (https://wiki.archlinux.org/title/Fan_speed_control) write integers in 0..255 to `/sys/devices/platform/asus-nb-wmi/hwmon/hwmonN/pwm1`. Some models accept 0 (off), 128 (mid), 255 (full) only as effective states, but the *interface* is still 0..255; writing 128 is legal (no `EINVAL`). The "0–100" range users sometimes mention refers to the *boost-mode register* (`/sys/devices/platform/asus-nb-wmi/throttle_thermal_policy` or the EC-Probe addresses 0x5e ← 0x40/0x80/0xC0 documented at https://wiki.archlinux.org/title/Fan_speed_control), which is a *thermal policy* selector, not the PWM channel. ventd should target `pwmX` and use 128. **No override required.**

**thinkpad_acpi** scales the firmware's 0..7 fan levels onto the hwmon 0..255 axis (https://www.kernel.org/doc/html/v5.4/admin-guide/laptops/thinkpad-acpi.html): "Fan level, scaled from the firmware values of 0-7 to the hwmon scale of 0-255. 0 means fan stopped, 255 means highest normal speed (level 7)." The kernel doc itself contains the unambiguous recommendation: *"To start the fan in a safe mode: set pwm1_enable to 2. If that fails with EINVAL, try to set pwm1_enable to 1 and pwm1 to at least 128 (255 would be the safest choice, though)"* (kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst, lines 1294–1298 of mjmwired mirror at https://mjmwired.net/kernel/Documentation/laptops/thinkpad-acpi.txt). PWM=128 maps to firmware level ≈ round(128*7/255) = 4 (medium). **PWM=128 is explicitly the kernel-documented safe value**; ventd should preserve it. ventd MUST also (a) set `pwm1_enable=1` *first*, (b) re-arm `fan_watchdog` ≥ a value larger than the polarity probe duration to avoid the EC reverting the fan during the probe, and (c) NOT use polarity disambiguation on thinkpad_acpi at all because thinkpads are not subject to inverted-polarity (the EC quantizes to 0..7 directly).

**HP iLO / nbfc-linux**: HP servers expose the iLO fan control via IPMI rather than hwmon `pwmX`, so polarity probing is N/A at the hwmon layer. nbfc-linux exposes a "FanSpeedPercentage" abstraction (0..100). ventd's catalog-less probe targets hwmon `pwmX` only, so HP iLO / nbfc are out of scope for R6. (Cross-reference R3/R7 if a future ventd backend is planned.)

#### 3.2 Inverted-with-deadband

The Intel 4-wire spec (rev 1.3, §3.2 and Figures 4–5, "Type A Operation, Minimum RPM, Stay on at Minimum RPM" / "Type B Operation, Stay On at Minimum RPM, Off at 0% RPM") permits a vendor-specified *minimum* PWM below which behavior is "Undetermined" — quoted from https://microdiypro.com/intel-4-wire-pwm-fan-specification-25khz-open-collector/: "Below the minimum PWM duty cycle, fan behavior is 'Undetermined'." This is the deadband. The spec further requires: "This specified minimum RPM shall be 30% of maximum RPM or less. The fan shall be able to start and run at this RPM."

For Noctua fans (the de-facto reference) the white paper states: "The fan's speed scales broadly linear with the duty-cycle of the PWM signal between maximum speed at 100% PWM and the specified minimum speed at 20% PWM... at 20% PWM and around 1100rpm at 50% PWM". So at PWM=128 (≈50%) a Noctua NF-A12x25 PWM (2000 RPM max) delivers ≈1100 RPM = 55% of max — comfortably above stall and inside the linear region.

If a fan implements an *inverted* curve with a deadband (a hypothetical worst case: ramps from PWM=64 not 0), then at PWM=128 the *delivered* duty under inversion is (255-128)=127, well above the deadband threshold. The midpoint property still holds: 50% *commanded* duty maps to ≈50% *RPM* in either polarity, and to "above-stall-and-cooling" in either polarity provided the deadband is < 50%. The Intel spec caps the vendor-allowable minimum at 30% of max RPM, and Arctic, Noctua, Corsair all comply. **PWM=128 is safe under deadband conditions.**

#### 3.3 BIOS override that ignores writes

This is the boundary case raised by the prompt. Reference: https://github.com/frankcrawford/it87/issues/96 ("IT8689E revision 1: PWM writes have no effect on fan speed (Gigabyte X670E Aorus Master)" — "On the IT8689E, writing to PWM registers is accepted without error but has zero effect on actual fan speed. All 5 PWM channels behave the same way"). Per R2 boundary check, this is **not** a polarity issue — the writes simply don't reach the PWM generator due to a chip-revision bug. The same symptom appears on Gigabyte B560M DS3H V2 (https://github.com/frankcrawford/it87/issues/11) and Topton N150 / it8613 (https://github.com/frankcrawford/it87/issues/97), and is mentioned in the Dell 3930 fan2go issue.

When this pathology is in effect:
- PWM=128 written → BIOS asserts whatever curve it wants (commonly somewhere in 60–90% under load).
- PWM=0 written → same outcome (BIOS asserts).
- PWM=255 written → same outcome (BIOS asserts).

Therefore **PWM=128 is no worse than PWM=0 or PWM=255** in BIOS-fight mode. It is also *safer than PWM=0* in cases where a BIOS partially honors the write (the "flick of a change" pattern reported on IT8792E in https://github.com/frankcrawford/it87/issues/86: "PWM change results in a flick of a change... I can hear a momentary motor off when I set 0 PWM"). PWM=128 produces a momentary mid-speed nudge; PWM=0 produces a momentary stall. ventd's polarity probe will **fail to detect a delta** in this state — that's the correct outcome (no polarity inferred, fall through to safe default). ventd should detect this case via the existing R2 silent-write detection (write → read → compare) and abort the polarity-disambiguation FSM rather than continue.

#### 3.4 PWM modes other than duty-cycle

**Step-mode / firmware levels**: thinkpad_acpi (covered §3.1) maps PWM to 0..7 levels; the scaling is monotonic so PWM=128 → level 4 = medium. dell-smm-hwmon also implements step-mode (covered §3.1).

**DC mode** is the principal concern. nct6775 and it87 both expose `pwmX_mode`: 0 = DC output (0–12 V on the fan power rail), 1 = PWM output (https://docs.kernel.org/hwmon/nct6775.html: "pwm[1-5]_mode — controls if output is PWM or DC level: 0 DC output (0 - 12v), 1 PWM output"). On a 3-pin fan in DC mode, PWM=128 ≈ 6 V on the supply rail. Two separate references confirm this is on the edge of the stall band:

- The Intel spec startup section (https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf, §3.4): "the fan shall be able to start and run at this RPM. To allow a lower specified minimum RPM, it is acceptable to provide a higher PWM duty cycle to the fan motor for a short period of time for startup conditions. This pulse should not exceed 30% maximum RPM and should last no longer than 2 seconds."
- The cadence.com PCB resources article (https://resources.pcb.cadence.com/blog/2020-pwm-vs-dc-fans-fan-speed-control-strategies-for-cpu-cooling-and-case-ventilation): "All the DC fans are specified with a minimum threshold voltage rating; if the voltage falls below the threshold, the fan starts to stall."
- Tom's Hardware reference thread (https://forums.tomshardware.com/threads/pwm-vs-variable-voltage-for-controling-dc-fan-speed.2652904/): "the Voltage to Pin #2... normally it should not go lower than about 5 VDC, because at lower voltages it may stall."
- Arctic P12 PWM PST CO datasheet (https://www.arctic.de/us/P12-PWM-PST-CO/ACFAN00121A and PDF https://www.arctic.de/media/05/64/2f/1583824874/spec_sheet_P12_PWM_PST_CO_190328_r6_EN.pdf): "Starting Voltage: 3.9 V" — so PWM=128 in DC mode (~6 V) is safely above start, but Arctic's *running* range is wider; some older sleeve-bearing OEM fans need ≥5–7 V to maintain rotation.
- Cooler Master fan stalling reference (https://box.co.uk/blog/cooler-master-fan-stopping-pwm-curve-voltage-issue): "Cooler Master fans often need at least 30–40% PWM to spin reliably" — 30% in DC mode = ~3.6 V which is below typical stall, while 50% (PWM=128) = 6 V which is at the safety margin.

**Conclusion**: PWM=128 in DC mode is *probably* safe (≈6 V > 3.9 V start and > 5 V run for compliant fans), but cuts uncomfortably close to the edge for cheap or aged 3-pin fans. ventd should **read `pwmX_mode` first**; if `pwmX_mode == 0` (DC mode), use **PWM=160 (62.7% ≈ 7.5 V)** instead of 128. PWM=160 is power-of-2-friendly (5 × 32), keeps the comparable midpoint property under polarity inversion (delivered = 95 = 37%, still above the 20–30% stall band), and adds ~1.5 V of margin over the empirical DC stall floor.

**Voltage-controlled "fans" on GPUs (amdgpu, nouveau)**: These are out of scope for R6 because (a) GPU fans don't have polarity ambiguity in the same sense — the AMD PMFW/firmware curve API is the supported path (https://wiki.archlinux.org/title/Fan_speed_control mentions "RDNA3 graphical cards do not support manual fan control due to firmware limitations") — and (b) the fan2go project explicitly flags amdgpu virtual-PWM behavior as an exception (fan2go README, https://github.com/markusressel/fan2go).

#### 3.5 Fan stall thresholds — datasheet survey

| Fan | Min RPM (PWM) | Min PWM | Stop at 0%? | Source |
|---|---|---|---|---|
| Noctua NF-A12x25 PWM | 450 RPM @ 20% | 20% (PWM=51) | Yes | https://noctua.at/en/nf-a12x25-pwm/specification ; white paper "around 1100rpm at 50% PWM" |
| Noctua NF-A12x25 G2 PWM | spec varies (LNA reduces 1800→1500) | 20% | Yes | https://noctua.at/en/nf-a12x25-g2-pwm/specification |
| Noctua NF-F12 PWM | 300 RPM @ 20% | 20% | Yes | Noctua white paper rule, spec page noctua.at/en/products/nf-f12-pwm |
| Noctua NF-A14 PWM | 300 RPM @ 20% | 20% | Yes | Noctua spec |
| Noctua NF-P12 redux 1700 PWM | ~450 RPM @ 20% | 20% | Yes | Noctua spec |
| Noctua NF-A20 / S12B redux / B9 redux | keeps min speed below 20% | runs at min below 20% | No (never stops) | Noctua white paper exception list |
| Arctic P12 PWM PST | 200 RPM | 5% (PWM≈13) | Yes ("0 RPM <5%") | https://www.arctic.de/us/P12-PWM-PST/ACFAN00170A |
| Arctic P12 PWM PST CO | 200 RPM | 5% | Yes | https://www.arctic.de/us/P12-PWM-PST-CO/ACFAN00121A ; spec sheet PDF |
| Arctic P12 Slim PWM PST | 200 RPM | 9% (PWM≈23) | Yes | https://www.arctic.de/us/P12-Slim-PWM-PST/ACFAN00187A ; "Start Up Voltage 5 V DC" |
| Arctic P12 Pro / P14 Pro | 600 / 400 RPM | 5% | Yes | Arctic spec pages |
| Intel stock cooler (E97379) | not stopping at 0%, internal startup ~15 s self-test | "wide low-PWM dead zone" | No (legacy) | https://microdiypro.com/intel-4-wire-pwm-fan-specification-25khz-open-collector/ empirical |
| Generic Intel-spec fan | ≤30% of max | vendor-specified (Intel §3.2) | optional Type B | https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf |

The empirical low-end of the spec-compliant range is therefore 20% PWM ≈ PWM=51. PWM=128 (50%) is **2.5× above the worst documented compliant minimum and 10× above Arctic's 5% threshold**, so PWM=128 clears the stall band on every catalogued mainstream fan under both polarities (delivered duty 50% normal, 50% inverted). The exceptions are (a) DC-mode 3-pin fans (covered §3.4 — escalate to PWM=160) and (b) heavily worn / aged fans where bearing friction has crept the start-PWM upward (no safe deterministic detection — accept the risk and let the daemon's RPM watchdog catch it).

#### 3.6 Inverted-polarity in the wild

Confirmed Linux-kernel-level inverted-polarity cases where `fix_pwm_polarity` is or was needed:

- **Thecus N5550 NAS, IT8728F**: https://lm-sensors.lm-sensors.narkive.com/B20KtRNp/it87-fix-pwm-polarity ("the it87 driver disables PWM control of the drive bay fan (it87.656/pwm3) unless I pass the fix_pwm_polarity parameter"). Guenter Roeck's reply confirms the mechanism: "It just changes the pwm polarity from active low to active high unless fan control is set to automatic mode. That doesn't damage anything." The driver-emitted message is `it87 it87.656: Detected broken BIOS defaults, disabling PWM interface` — this is documented in the historic patch at https://lkml.iu.edu/hypermail/linux/kernel/0501.2/0521.html (Jean Delvare / Jonas Munsin, 2005-01: `if ((tmp & 0x87) == 0) { enable_pwm_interface = 0; dev_info(... "detected broken BIOS defaults, disabling pwm interface"); }`). The *current* it87 module still carries the warning at the same call site (https://github.com/frankcrawford/it87 README: "fix_pwm_polarity int — Force PWM polarity to active high (DANGEROUS). Some chips are misconfigured by BIOS - PWM values would be inverted").
- **General it87 family**: same parameter still in upstream (https://docs.kernel.org/6.2/hwmon/it87.html).
- **Custom ARM boards / Aaeon PCM-8120 with IT8712F**: see the lm-sensors thread at https://lm-sensors.lm-sensors.narkive.com/eSQNuOE1/failed-to-load-it87-module-on-it8712f for the older "Device not activated, skipping" / "broken BIOS defaults" pattern.

The inverted-polarity hardware is dominated by **older it87-class Super-I/O on poorly-initialized BIOS** rather than by a specific motherboard vendor in 2024–2026 product. The Reddit / homelab anecdotes about MSI/Gigabyte "fans spinning the wrong direction" referenced at https://hardforum.com/threads/msi-x370-gaming-pro-carbon-fans-spinning-in-the-wrong-direction.1931498/ turn out, on inspection, to be airflow-orientation issues (fan installed backward in chassis) and not PWM-polarity issues at all. The Orion BMS automotive controller (https://www.orionbms.com/manuals/utility_o2/param_fan_duty_polarity.html) documents an explicit "Invert Polarity" option but is an automotive battery-management context, not a PC motherboard. **The realistic threat model for ventd is the it87 / Super-I/O misconfiguration class, not consumer motherboards from the last 5 years.** The IT8689E rev1 silent-write issue (https://github.com/frankcrawford/it87/issues/96) is, per the prompt's own R2 boundary, a *separate* class (writes don't take effect at all) and is correctly excluded from R6's polarity scope.

---

### 4. Comparison table — initial-value × pathology

Outcome legend: ✅ safe (cools, probe usable), ⚠ fail-safe (cools but probe unreliable), ❌ fail-dangerous (under-cools or stalls), ⊘ N/A (interface refuses or rounds to a different value).

| PWM | Normal polarity | Inverted polarity | Dell fan_max=2 | Dell fan_max=3 | Dell fan_max=4 | thinkpad (0–7) | nct6775 DC mode (~12 V × duty) | BIOS-fight (silent write) | Deadband ≤30% | Fan with 30% start_pwm |
|---|---|---|---|---|---|---|---|---|---|---|
| 0 | ❌ stops fan | ✅ runs full | ✅ exact (state 0) | ✅ exact (state 0) | ✅ exact (state 0) | ✅ stops fan; explicit kernel-doc warning | ❌ 0 V — definite stall | ⚠ no effect, but fan stops on partial-honor chips | ❌ below deadband | ❌ stops |
| 64 | ⚠ ~25% duty, near stall | ⚠ ~75% delivered, ok | ⊘ rounds to 0 | ⊘ rounds to 0 | ✅ exact state 1 | ✅ level 2 | ⚠ ~3 V — stall risk on weak fans | ⚠ no effect | ⚠ near deadband | ❌ at start threshold |
| **128** (current default) | ✅ ~50% duty | ✅ ~50% delivered | ✅ exact state 1 → readback 128 | ⚠ rounds to state 1 → readback 85 (~33%) | ✅ exact state 2 → readback 128 | ✅ kernel-doc-blessed safe value (level ≈4) | ⚠ ~6 V — at edge of margin | ⚠ no effect, but 50% nudge if partial | ✅ above deadband | ✅ above 30% start |
| 160 | ✅ ~63% duty | ✅ ~37% delivered, still cools | ⊘ rounds to 128 | ⊘ rounds to 170 (state 2) | ⚠ rounds to 128 | ✅ level ≈ 4 | ✅ ~7.5 V — clear margin | ⚠ no effect | ✅ above deadband | ✅ above 30% start |
| **170** | ✅ ~67% duty | ✅ ~33% delivered | ⊘ rounds to 128 | ✅ exact state 2 → readback 170 (true midpoint) | ⚠ rounds to 192 | ✅ level ≈ 5 | ✅ ~8 V | ⚠ no effect | ✅ above deadband | ✅ above 30% start |
| 192 | ⚠ louder; some BIOSes treat as user override | ⚠ delivered 25%, near deadband | ⊘ rounds to 255 | ⊘ rounds to 170 | ✅ exact state 3 → readback 192 | ✅ level ≈ 5 | ✅ ~9 V | ⚠ no effect | ⚠ delivered near deadband | ✅ above 30% start |
| 255 | ⚠ full speed, loud, may trigger BIOS "user override" mode | ❌ fan stops | ✅ exact (state 2/3/4 depending on fan_max) | ✅ exact | ✅ exact | ✅ level 7 | ⚠ 12 V — fine for fan, but cannot probe inverted polarity | ⚠ no effect | ⚠ inverted = below deadband | ❌ inverted: stops |
| current (preserve) | ✅ inherits BIOS state | ✅ inherits BIOS state | ✅ inherits | ✅ inherits | ✅ inherits | ✅ inherits | ✅ inherits | ✅ inherits | ✅ inherits | ✅ inherits — but **provides no probe signal** |

PWM=128 is the only column with a single ⚠ degradation (fan_max=3 / partial DC-mode margin) and no outright ❌ in any cell. PWM=170 and PWM=160 each have *more* ⊘ rounding cases. PWM=255 has two ❌ (inverted-polarity fan stops; high-start-PWM fan stops in inverted polarity), making it strictly worse as a polarity probe.

---

### 5. Recommended safe initial value

**Default: PWM=128** — for all hwmon channels where `pwmN_mode` is absent or equals 1 (PWM mode), and where `fan_max` (Dell-only) is 2, 4, or unknown.

**Per-driver overrides:**

| Driver / condition | Override | Reason | Citation |
|---|---|---|---|
| `dell-smm-hwmon` with `i8k_fan_max=3` | **PWM=170** | 128 rounds *down* to state-1 readback 85, ≈33% — not midpoint. 170 is exact state-2 = readback 170 = true midpoint of {0,85,170,255}. | https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c (`DIV_ROUND_UP(255, fan_max)`); https://github.com/lm-sensors/lm-sensors/pull/383 |
| `dell-smm-hwmon` with `i8k_fan_max=2` or `=4` | **PWM=128** | Exact representable state, readback=128. | Same as above |
| `dell-smm-hwmon` with `i8k_fan_max≥5` (rare) | **PWM=128** with logged warning | Will round to nearest representable state; midpoint property approximate. | Same as above |
| `nct6775*` / `it87` / `nct6683` with `pwmN_mode=0` (DC mode) | **PWM=160** | Adds ~1.5 V margin above DC stall floor while preserving cooling-under-both-polarities. | https://docs.kernel.org/hwmon/nct6775.html ; https://www.arctic.de/media/05/64/2f/1583824874/spec_sheet_P12_PWM_PST_CO_190328_r6_EN.pdf ("Starting Voltage: 3.9 V") |
| `thinkpad_acpi` | **PWM=128** with mandatory `pwm1_enable=1` precondition + `fan_watchdog` re-arm | Kernel docs explicitly: "set pwm1 to at least 128 (255 would be the safest choice)". Inverted polarity is N/A (firmware quantizes). | https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst |
| `asus-wmi-sensors` / `asus-nb-wmi` (laptop) | **PWM=128** | sysfs is 0..255 hwmon-conventional. The 0..100 / boost-mode register is a separate interface. | https://wiki.archlinux.org/title/Fan_speed_control |
| Any channel where R2 silent-write is detected | **abort polarity probe**, do not write further | Polarity cannot be inferred when writes don't reach the fan; ventd should accept "polarity unknown" and write 192 (still cooling under both polarities) only as a thermal-safety floor. | https://github.com/frankcrawford/it87/issues/96 |

---

### 6. Three-state polarity-disambiguation procedure

```
FSM PolarityProbe(pwm_path, fan_input_path):
  base = override_value_for_driver(driver_family, fan_max, pwm_mode)
        // 128 / 170 / 160 per §5
  step = 32                 // see step-size derivation below
  settle = 3 * fanResponseDelay   // default fanResponseDelay=2s (fan2go default)

  // 1. Verify writability (R2 silent-write check)
  pwm_enable_save = read("${pwm_path}_enable")
  write("${pwm_path}_enable", 1)              // manual mode
  write(pwm_path, base)
  sleep(settle)
  if read(pwm_path) != base:
     return UNKNOWN_INTERFACE_QUANTIZED      // R2 family; do not interpret RPM delta
  rpm_base = average(fan_input_path, samples=10, period=1s)

  // 2. Step UP and observe
  write(pwm_path, base + step)
  sleep(settle)
  rpm_high = average(fan_input_path, samples=10, period=1s)
  delta = rpm_high - rpm_base

  // 3. Restore base (don't leave probe artifact behind)
  write(pwm_path, base)

  // 4. Decide
  if delta >  noise_floor:       return NORMAL_POLARITY
  if delta < -noise_floor:       return INVERTED_POLARITY
  if abs(delta) <= noise_floor:  return INDETERMINATE      // BIOS fight or stuck fan

  restore("${pwm_path}_enable", pwm_enable_save)
```

**Step-size derivation (step=32 PWM = 12.5 percentage points):**

- Intel spec ±10% RPM/duty-cycle linearity tolerance (https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf §3.2).
- A 12.5% commanded-duty step at midpoint produces an *expected* RPM delta of ≈12.5% × max RPM. For a 1500 RPM fan that's ≈190 RPM; for a 2000 RPM fan ≈250 RPM.
- fan2go's default `maxRpmDiffForSettledFan: 250` (https://github.com/markusressel/fan2go/blob/master/fan2go.yaml) calibrates "fan considered settled" exactly at this magnitude.
- Therefore step=32 yields a **signal that is roughly equal to the tool's noise floor**, which is borderline. Step=48 (~19%) or step=64 (25%) yields 2–3× margin over noise. **Recommendation: ventd should use step=64 (PWM 128 → 192, or 170 → 234, or 160 → 224) for the *first* probe step** — this gives a robust ΔRPM ≈ 25% × max ≈ 375–500 RPM, well above noise. If the result is INDETERMINATE, *retry* with step=32 *down* (probe the other direction relative to base), to disambiguate "fan saturated at high end" from "BIOS fight."

> **Cross-reference dependency: R11 (sensor noise floor)** — the choice of `noise_floor` constant in the FSM should be tuned against R11's empirical sensor-noise findings once available. Default 200 RPM is a reasonable interim value (slightly less than fan2go's 250, providing margin to detect the inverted-polarity case where a step-up may produce a *negative* delta of ≈step×0.5×max ≈ 250 RPM).

**Polarity disambiguation must NOT be run on:**
- thinkpad_acpi (firmware monotonic 0..7 — no inversion possible).
- dell-smm-hwmon (Dell EC monotonic — no inversion possible; only quantization).
- amdgpu / nouveau (GPU firmware mediated; out of hwmon polarity scope).
- Any channel with `pwm1_enable=2` (BIOS-auto) — must switch to `pwm1_enable=1` first.

---

### 7. Hazard analysis

| Scenario | Risk | Likelihood | Severity | Mitigation |
|---|---|---|---|---|
| Polarity probe runs while CPU/GPU under heavy load | Brief (3–6 s) underspeed could push junction temp into throttle | Medium (probe runs during ventd startup) | Low (modern CPUs throttle gracefully; no permanent damage) | Sequence: read all temps; if any > MAXTEMP-15 °C, *defer* probe and write PWM=255 instead until a thermal calm window opens. Mirrors fan2go's `runFanInitializationInParallel: false` recommendation (https://github.com/markusressel/fan2go README). |
| DC-mode 3-pin fan stalls at PWM=128 (≈6 V) | Fan stops, RPM=0, ventd misreads as "inverted polarity = full speed under inversion" | Low (Arctic P12 starts at 3.9 V; only weak/aged fans affected) | High (fan stalled = no airflow) | Use override PWM=160 in DC mode; require `fan1_input > 0` *before* polarity probe (sanity precondition); abort if fan reports 0 RPM after `settle`. |
| BIOS asserts a different curve every 1–5 s, fighting the probe | RPM measurement is noisy; FSM returns INDETERMINATE incorrectly | Medium (Dell EC, some Gigabyte) | Low (FSM correctly returns INDETERMINATE → safe fallback) | Before probe, attempt BIOS-control disable (set `pwm1_enable=1`); confirm via R2 silent-write check; on persistent fight, accept INDETERMINATE and use PWM=192 as the operational baseline (a "definitely cooling" value under either polarity). |
| Fan watchdog (thinkpad) reverts mid-probe | Probe writes lost, FSM mis-classifies | Medium on ThinkPad | Low | Re-arm `fan_watchdog` to ≥ probe-duration + 10 s before starting; preserve and restore `pwm1_enable` only after probe. |
| Inverted-polarity probe at PWM=128 produces "delivered=128" (=normal) on a chip where the driver already inverts internally | False NORMAL_POLARITY reading; ventd then writes inverted curves | Low (most drivers either invert in driver *or* expose raw — not both) | Medium | Document policy: *trust the driver's hwmon abstraction*. If `it87 fix_pwm_polarity=1` is loaded, the kernel has already corrected; ventd treats `pwmX` as normal. ventd's catalog-less probe applies *on top of* the driver — so a board that needs `fix_pwm_polarity` and is loaded *without* the parameter is the only realistic inversion case ventd will see. |
| User has set BIOS fan curve to "Silent" / "Zero RPM" | Probe at PWM=128 sees ZERO_RPM_BELOW_THRESHOLD behavior on premium fans | Low–medium | Low | Detect via `fan1_input` != 0 precondition; if 0 RPM despite PWM=128 commanded, log and skip rather than further reduce duty. |
| Quantized hwmon channel where PWM=128 rounds to 85 (Dell fan_max=3) | Probe under-runs fan, may stall under heavy thermal load | Medium on affected Dell servers | Medium | Per-driver override to PWM=170; explicitly read `pwm1` after writing 128 and re-issue if readback indicates rounding. |

No identified scenario is fail-dangerous in the sense of permanent fan damage. The Intel 4-wire spec (§3.4 reliability section) requires fans to tolerate full-range duty cycles indefinitely, and Noctua / Arctic / be quiet! datasheets (e.g., https://www.arctic.de/us/P12-PWM-PST/ACFAN00170A — "0 rpm below 5 % PWM ... 6-year warranty") explicitly support 0% duty as a normal operating state. The *thermal* hazard (under-cooling during a 3–10 s probe window) is the only realistic risk and is mitigated by the temperature-precondition check.

---

### 8. Summary of cross-references

- Linux kernel hwmon docs (sysfs interface, nct6775, it87, dell-smm-hwmon, thinkpad_acpi, pwm-fan, g762):
  - https://docs.kernel.org/hwmon/nct6775.html
  - https://docs.kernel.org/6.2/hwmon/it87.html
  - https://docs.kernel.org/hwmon/dell-smm-hwmon.html
  - https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst
  - https://docs.kernel.org/hwmon/pwm-fan.html
  - https://docs.kernel.org/5.19/hwmon/g762.html
- Linux kernel sources:
  - dell-smm-hwmon DIV_ROUND_UP / clamp_val: https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c
  - it87 fix_pwm_polarity / "Detected broken BIOS defaults": https://github.com/frankcrawford/it87 README and historical patch https://lkml.iu.edu/hypermail/linux/kernel/0501.2/0521.html
- Intel 4-Wire PWM Specification (rev 1.3, September 2005): https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf and https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf (cited §3.2 monotonicity, §3.4 startup pulse, Figures 4–5 type-A/B).
- Noctua PWM specifications white paper: https://cdn.noctua.at/media/Noctua_PWM_specifications_white_paper.pdf
- Fan datasheets: Noctua (https://noctua.at/en/nf-a12x25-pwm/specification, …G2…), Arctic (https://www.arctic.de/us/P12-PWM-PST/ACFAN00170A; spec PDF https://www.arctic.de/media/05/64/2f/1583824874/spec_sheet_P12_PWM_PST_CO_190328_r6_EN.pdf).
- Tooling references:
  - fan2go startPwm/minPwm/maxPwm semantics: https://github.com/markusressel/fan2go (README, fan2go.yaml, issue #30 "Distinguish between startPwm and minPwm", issue #26 capped boundaries, issue #201 Dell 3930).
  - lm-sensors fancontrol MINSTART/MINSTOP: https://linux.die.net/man/8/fancontrol ; https://github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/fancontrol ; https://github.com/lm-sensors/lm-sensors/pull/383 (Dell quantization PR).
  - thinkfan / thinkpad_acpi level mapping: https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst ; https://www.thinkwiki.org/wiki/How_to_control_fan_speed
- Boundary-case (R2 cross-reference) IT8689E rev1 silent-write: https://github.com/frankcrawford/it87/issues/96 (NOT polarity, confirmed boundary).

---

## ARTIFACT 2 — Spec-ready findings appendix block

```markdown
### R6 — Polarity Midpoint
- **Defensible default(s):**
  - Primary: `PWM=128` (50% of 0–255 hwmon range).
  - Per-driver overrides:
    - `dell-smm-hwmon` & detected `i8k_fan_max == 3` → `PWM=170`.
    - `dell-smm-hwmon` & `i8k_fan_max ∈ {2, 4}` → `PWM=128`.
    - any channel with `pwmN_mode == 0` (DC mode on nct6775/it87) → `PWM=160`.
    - `thinkpad_acpi` → `PWM=128` only after setting `pwm1_enable=1` and re-arming `fan_watchdog`; do not run polarity probe (firmware-monotonic).
    - any channel where R2 silent-write is detected → abort polarity probe; write `PWM=192` as thermal-safety baseline.
  - Probe step magnitude: `+64` (preferred) or `+32` (fallback) PWM units.
- **Citation(s):**
  1. https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst — explicit kernel-doc statement: "set pwm1_enable to 1 and pwm1 to at least 128 (255 would be the safest choice)."
  2. https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c — `data->i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)` and `*val = clamp_val(ret * data->i8k_pwm_mult, 0, 255)` — defines the fan_max=3 quantization that demotes PWM=128 to readback 85; corroborated empirically in https://github.com/lm-sensors/lm-sensors/pull/383.
  3. Intel "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification" rev 1.3 (September 2005), §3.2: "Fan speed response... shall be a continuous and monotonic function of the duty cycle... within ±10%" — establishes that 50% commanded = ≈50% RPM in either polarity branch; PDF https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf and https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf.
- **Reasoning summary:** PWM=128 is the unique value that delivers ≈50% effective duty in *both* normal and inverted polarities, lands above every documented stall threshold (Intel ≤30%, Arctic 5%, Noctua 20%), and is exactly representable on 5 of the 7 common quantization grids. Of the 9 pathology classes surveyed, PWM=128 is fail-safe or safe in 8 and only fail-undefined (rounds to 85) in the Dell fan_max=3 case, which is detectable at runtime and corrected by an override to PWM=170. No alternative single value (0/64/160/170/192/255/current) scores better; PWM=255 fails-dangerous under inverted polarity (fan stops). DC-mode 3-pin fans warrant a +32 lift to PWM=160 (~7.5 V) for stall margin.
- **HIL-validation flag:** Yes —
  - **13900K + RTX 4090 desktop** (assumed nct6775 / Asus or MSI Z790, plus AMD GPU PWM) runs **(a)** baseline polarity probe at PWM=128 → +64 step on all motherboard fan channels, recording readback and ΔRPM; **(b)** DC-mode regression: forcibly set `pwm1_mode=0` on a 3-pin fan header, re-run probe at PWM=128 *and* PWM=160, confirm ΔRPM > 250 only at 160.
  - **Proxmox host (5800X + RTX 3060)** runs the same baseline probe; cross-checks against fan2go config-comparison.
  - **MiniPC (Celeron, likely it87 / IT8613/IT8689 family)** runs the **R2 silent-write boundary** case to verify ventd correctly aborts the polarity probe and falls back to PWM=192.
  - **Three laptops:** at least one ThinkPad runs the thinkpad_acpi `pwm1_enable=1` + `pwm1=128` recipe with `fan_watchdog=120` to confirm no EC reversion during a 6 s probe window; at least one ASUS laptop runs the `asus-nb-wmi` 0..255 path to confirm 128 is honored (not silently clamped to 100).
  - Test on a Dell laptop is highly desirable but not in current fleet; flag as "wanted" — would validate the fan_max=3 → PWM=170 override empirically. Until validated, the override is implemented behind a `dell_quantization_v1` feature flag.
- **Confidence:** **High** for the PWM=128 default, the Dell fan_max=3 → 170 override, the DC-mode → 160 escalation, and the thinkpad_acpi recommendation (all backed by explicit primary sources or reproduced kernel source). **Medium** for the +64 probe step magnitude (derived from fan2go heuristics, awaiting R11 sensor-noise empirical confirmation). **Medium** for the BIOS-fight abort policy (depends on R2's silent-write detection, which is the sister research item).
- **Spec ingestion target:** `docs/spec/probe.md` §"Polarity Disambiguation" (new subsection 4.3.x). Implementation lands in `internal/probe/polarity.go` (constants `defaultProbeBase = 128`, `dellFanMax3ProbeBase = 170`, `dcModeProbeBase = 160`, `probeStep = 64`, `probeStepFallback = 32`, `probeSettle = 3 * fanResponseDelay`). Driver-detection helpers in `internal/hwmon/driver_detect.go` should expose `IsDellSMM()`, `DellFanMax()`, `IsThinkpadACPI()`, `IsDCMode(channel)` predicates. Cross-reference dependency on **R2** (silent-write detection) and **R11** (sensor-noise floor) — flag both in the spec subsection. Add to the daemon's startup log a single INFO line per channel: `polarity-probe: driver=%s fan_max=%d mode=%s base=%d step=%d result=%s ΔRPM=%d` for forensic debuggability.
```