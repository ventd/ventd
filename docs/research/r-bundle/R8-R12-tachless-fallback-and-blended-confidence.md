# ventd Smart-Mode Research Bundle — R8 & R12

**Document scope:** Layer A fallback signals when no tachometer is present (R8) and the confidence formula governing the blended predictive/reactive controller (R12). Both items are produced together because R8's tach-less channel handling is the primary input to R12's conf_A formulation, and R12's smoothness guarantee constrains the rate at which R8's degraded confidence may evolve.

**Project context:** ventd is a Go 1.25+ Linux fan-controller daemon, CGO_ENABLED=0, GPL-3.0. Smart-mode is a three-layer learning system (A: PWM→RPM/airflow curve; B: per-channel thermal coupling map; C: per-(channel, workload-signature) RLS marginal-benefit estimator). Output is `output = w_pred * predictive_output + (1 - w_pred) * reactive_output` with `w_pred = clamp(min(conf_A, conf_B, conf_C), 0, 1)`. spec-05 has already locked the predictive controller as RLS+IMC-PI (no MPC, no LSTM/GPR).

═══════════════════════════════════════════════════════════════════

# R8 — Fallback Signals When No Tachometer (Layer A)

## Executive Summary

The hwmon contract specifies that `fanN_input` is optional ("should only be created if the chip has the feature"); on a non-trivial fraction of the ventd target fleet — 3-pin DC-modulated channels, USB-C laptops with EC-private fans, AIO pump-only channels, Dell SMM laptops with stepped 0/1/2/3 fan_max, Framework Chromebook-EC laptops, BMC-managed servers — the Layer A learner cannot directly observe RPM. R8 specifies a deterministic seven-tier fallback chain, a hard ceiling on Layer A confidence for tach-less channels, and a thermal-only stall-detection scheme that interlocks with the R4 envelope abort thresholds.

The recommended design treats RPM as the **preferred but non-required** Layer A observable. When `fanN_input` is missing, unreadable, or saturated to a single value, ventd selects the highest-tier fallback signal that the channel exposes, learns a degraded "PWM → airflow proxy" curve, caps `conf_A` at a tier-dependent ceiling (0.30–0.55), and adds a thermal-only stall watchdog that escalates well below the R4 hardware-protection ceilings. AIO pump-only channels are pinned to telemetry-only (no Layer A learning, `conf_A := N/A`, contributes nothing to predictive blending). For BMC channels, ventd polls the in-kernel `/dev/ipmi0` KCS character device using a pure-Go IPMI marshaller (no `ipmitool`, no shell-out, CGO_ENABLED=0 preserved); polling rate is governed by R11's slow-loop tick of 60 s rather than the 2 s fast loop because BMC SDR refresh is hardware-capped at ≤ 1 Hz.

## Methodology

R8 is a primary-source survey of (a) the kernel hwmon ABI specifying which fan attributes are mandatory vs optional, (b) the per-driver source code for `pwm-fan`, `dell-smm-hwmon`, `cros_ec_hwmon`, `nct6775`, and `ipmi_si`, (c) the Linux powercap/intel-rapl framework as a CPU-load proxy, and (d) the existing ventd hardware-class table. We cross-checked the Framework EC patch trail (lkml, June 2024) and the Steve-Tech and DHowett out-of-tree variants to bound what tach quantization looks like in practice.

We deliberately avoided two candidate fallback signals: acoustic monitoring (microphone-based RPM detection) is out of scope for v1.0 because it requires a userspace audio capture path that is incompatible with both the headless-server and laptop-on-battery profiles, and adds a significant privacy surface; and userspace EC poking via `ec_sys.write_support=1` is forbidden under R3 hardware-refusal because the EC ABI is undocumented and writes are unrecoverable on suspend/resume.

## Q1. Signal Enumeration and Reliability Matrix

The following tiers are ranked by ventd's preference order, highest first. Tier 0 is the canonical RPM tach (the case R8 does *not* address); tiers 1–7 are the fallback chain.

| Tier | Signal | Source | Reliability | Poll Cost | Kernel/Driver Support | Hardware Class Applicability |
|------|--------|--------|-------------|-----------|-----------------------|------------------------------|
| 0 | `fanN_input` (RPM tach) | hwmon, generic | High (primary) | 1 read / 2 s (fast loop) | Universal where present | Most desktop / NAS / server motherboards |
| 1 | Coupled-channel inference | Internal: copy curve from sibling channel sharing the same controller | High *iff* coupled channel has Tier-0 | Free (already polled) | Pure-Go logic | NCT677x boards with mixed 4-pin/3-pin headers; Supermicro X11 boards |
| 2 | BMC IPMI fan SDR | `/dev/ipmi0` via in-kernel `ipmi_si` (KCS) | Medium (≤1 Hz refresh) | 1 SDR poll / 60 s (slow loop) | `ipmi_si` mainline since 2.6 | Dell iDRAC, Supermicro, HPE iLO; ventd's spec-15-experimental track |
| 3 | EC stepped fan_max state | `dell-smm-hwmon` `pwmN_enable` / `cros_ec_hwmon` `fanN_input` | Medium-Low (3–4 quantized states; SMM call latency ~500 ms on some BIOSes) | 1 SMM call / 60 s | `dell-smm-hwmon`, `cros_ec_hwmon`, both mainline | Dell laptops, Framework laptops, Chromebooks |
| 4 | Thermal-response inversion (PWM → ΔT/dt under known load) | Layer B coupling matrix + temp sensors | Medium (inferential, slow) | Free (already polled) | Pure-Go logic on top of `coretemp`, `k10temp`, `amdgpu`, `nvme` | Universal where temp sensors exist; only viable signal for USB-C-PD-only laptops |
| 5 | Power-draw inference (RAPL package power Δ as load proxy) | `/sys/class/powercap/intel-rapl:N/energy_uj` (also amdgpu hwmon `power1_average`) | Medium (load proxy, not airflow proxy — needs Layer B to compose) | 1 read / 2 s | `intel_rapl_common` since 3.13; `amdgpu` mainline | Sandy Bridge+, all AMD Zen, recent NVIDIA via `nvidia-smi` (excluded — out-of-tree binary) |
| 6 | `pwm_enable` mode echo (firmware "thermal cruise" reports back stepped speed level) | hwmon `pwmN_enable` re-read after write | Low (vendor-specific, often misreports) | Free | nct6775, k10temp, etc. | Limited; flagged as a "courtesy signal" — used only to confirm the firmware accepted the write |
| 7 | Open-loop only (no feedback, conf_A pinned to floor) | None | N/A | None | N/A | AIO pump-only and locked-100% channels (Arctic Liquid Freezer); fans on permanently-broken tach |

**Tier 1 — Coupled-channel inference.** Many motherboards expose multiple PWM headers driven by a common controller IC (e.g., NCT6798 routes pwm1–pwm5 through one PWM generator family). When channel A has a working tach and channel B does not, but ventd's R6 polarity probe and R14 calibration produce statistically indistinguishable thermal responses for both, Layer A can adopt A's curve as a prior for B. This is the cheapest and most reliable fallback when applicable, and is the recommended first-choice for desktop boards. The validation rule: two channels are "coupled" when (i) they share an hwmon device path, (ii) their PWM amplitude responses to the polarity probe match within ±10 % across at least three probe points, and (iii) their thermal-coupling vectors from Layer B are cosine-similar at ≥ 0.85.

**Tier 2 — BMC IPMI.** The kernel exposes `/dev/ipmi0` once `ipmi_si` is loaded; on Supermicro/Dell/HPE servers it auto-probes via DMI type 38. The character device accepts `IPMI_SEND_COMMAND` ioctls carrying raw NetFn/Cmd payloads. A pure-Go IPMI marshaller (~600 LoC, structurally similar to `bmc-toolbox/bmclib` and `metal-stack/go-hal/internal/ipmi` but trimmed to the four commands ventd actually needs: `Get SDR Repository Info`, `Reserve SDR Repository`, `Get SDR`, and `Get Sensor Reading`) keeps CGO_ENABLED=0. Refresh is hardware-capped: BMCs typically update SDR fan readings at 0.5–1 Hz, and many vendors document a polling-interval floor of 1 s to avoid starving the BMC's other tasks. Therefore R11's slow-loop tick (60 s) governs Tier 2 polling; R11's fast-loop saturation gate (`N=20` over 2 s) is replaced by a slow-loop gate (`N=3` over 180 s) for Tier 2 channels.

**Tier 3 — EC stepped fan state.** `dell-smm-hwmon` exposes `fanN_input` in RPM but the underlying SMM `0x02a3` "Get fan speed" command returns a quantized value driven by a 0/1/2/3 state machine (where 3 is sometimes a "magic" auto-mode placeholder). The kernel doc explicitly warns: "SMM calls can take too long to execute on some machines, causing short hangs and/or audio glitches" and lists Inspiron 7720, Vostro 3360, XPS 13 9333, and XPS 15 L502X as ~500 ms call-time offenders. ventd must therefore (a) poll Tier 3 only on the slow loop, (b) treat the RPM value as ordinal not cardinal — the curve learned is `PWM → fan_state ∈ {0,1,2,3}` rather than continuous RPM — and (c) refuse to write PWM if `pwm1_enable` is unavailable, since the SMM auto-control codes are non-whitelisted on most BIOSes and forced manual mode is overridden every few seconds. Framework's `cros_ec_hwmon` is similar but cleaner: fan readings are direct EC reads (no SMM trap) and the recently-merged `EC_CMD_PWM_SET_FAN_DUTY` plumbing exposes per-channel PWM control with proper versioning. ventd should detect Framework via the `framework_laptop` or `cros_ec` hwmon `name` attribute and treat its tach as Tier 3 (coarse) rather than Tier 0 — quantization is real even though the value type is RPM.

**Tier 4 — Thermal-response inversion.** This is the canonical Layer B inversion: given a step in PWM and the resulting ΔT/dt at coupled sensors, infer airflow change. This is *not* a substitute for RPM as a Layer A primary observable — it conflates fan response (Layer A) with thermal coupling (Layer B) and pump-load (Layer C). However, when no other signal exists, the Layer A learner can still characterize the *sign and qualitative slope* of PWM→airflow by holding the workload bucket constant (R7) and observing ΔT response. This is sufficient to populate a coarse curve of three points (PWM_min, PWM_mid, PWM_max) for the IMC-PI controller, and is what allows USB-C laptops to participate in smart-mode at all.

**Tier 5 — Power-draw inference.** Linux's powercap/intel-rapl interface (`/sys/class/powercap/intel-rapl:0/energy_uj`, kernel 3.13+, restricted to root since 5.10 per CVE-2020-8694) exposes a monotonically-increasing energy counter; differentiating gives package power. AMDGPU and i915 expose comparable `power1_average` files. Power is a *load proxy* — high CPU power means the workload signature is hot — and is fed into Layer C, not Layer A. ventd uses RAPL deltas to disambiguate "the temp went up because PWM dropped" (Layer A signal) from "the temp went up because workload spiked" (no Layer A information): when |ΔP_pkg| > 5 W in the same fast-loop tick, the Layer A sample is **discarded** rather than used to update the curve. This is the same admissibility logic as R11's saturation gate but for the power domain.

**Tier 6 — pwm_enable echo.** Some chips (notably nct6775 family in modes 2–5: Thermal Cruise, Fan Speed Cruise, Smart Fan III/IV) report back a current-effective fan level after a write. This is a confirmation signal, not a measurement signal; ventd uses it solely to detect "BIOS overrode our PWM" failures and never as an input to the Layer A curve.

**Tier 7 — Open-loop pinned floor.** When no tier 1–6 signal is available, conf_A is held at a hard floor (see Q2) and the channel cannot participate in predictive blending — `w_pred` for that channel is forced to 0 and ventd runs purely reactive on it.

## Q2. Confidence Weighting Without RPM

Per-tier ceilings on conf_A (these are caps; the actual conf_A can be lower per the R12 formula):

| Tier | conf_A ceiling | Rationale |
|------|----------------|-----------|
| 0 (RPM tach) | 1.00 | Reference; full predictive participation |
| 1 (coupled-channel) | 0.85 | High confidence but transferring assumption from a sibling; reserve 15 % headroom for the case where coupling diverges over time |
| 2 (BMC IPMI) | 0.70 | Slow refresh limits the controller's ability to detect transient stalls; conf is real but bounded |
| 3 (EC stepped) | 0.55 | 3–4 quantized levels mean the curve has at most 3 observable points; predictive contribution is small but non-zero |
| 4 (thermal inversion) | 0.45 | Conflates Layer A with Layer B; useful but cannot independently verify fan health |
| 5 (RAPL load proxy) | 0.30 | Not an airflow signal — only useful as workload disambiguator; conf_A floor only |
| 6 (pwm_enable echo) | 0.30 | Confirmation only; same ceiling as Tier 5 |
| 7 (open-loop) | 0.00 | Channel is reactive-only |

These ceilings are **maxima**; the actual conf_A is computed by the R12 formula and clamped against the ceiling at the end. The ceilings are deliberately tier-monotone: a channel that gains a higher-tier signal (e.g., BMC SDR comes online after firmware update) sees its ceiling rise smoothly, but the underlying conf_A still has to be earned by sample accumulation.

The model retains the failure modes that tach normally detects:
- **Stall detection:** handled by the Q3 thermal watchdog rather than by RPM=0.
- **Bearing seize:** undetectable on tach-less channels; ventd surfaces this as a documented limitation in the user-visible channel state ("limited diagnostics — no tach available").
- **Curve drift from dust accumulation:** observable only via Layer B residual growth; ventd schedules an envelope-C re-seed every 90 days for tach-less channels (vs 365 days for tach'd channels) to compensate.

## Q3. Stall Detection Fallback (Thermal-Only)

R4 already encodes per-class hardware-protection abort thresholds (Class 5 laptop ≤ 3.0 °C/s, Class 7 NAS HDD ≤ 2.0 °C/min). R8's stall-detection scheme triggers **earlier** than R4 to give ventd time to react before the safety envelope fires:

```
For tach-less channel C with primary coupled sensor S:
  Let dT_S/dt        = EWMA-filtered temperature slope (τ = 30 s)
  Let pwm_C          = current commanded PWM
  Let workload_load  = R7 bucket-current load proxy in [0,1]
  Let dT_expected    = Layer-B-predicted slope at this PWM and load
  Let dT_residual    = dT_S/dt - dT_expected

  STALL_SUSPECTED if all of:
    pwm_C            >= 0.6 * pwm_max_C            (commanded high)
    dT_residual      >= 0.5 * R4_class_threshold   (half the hardware abort rate)
    sustained for    >= 30 s                        (stall window)
    not in idle-gate (R5)                           (avoid spurious during sleep transitions)

  ACTIONS on STALL_SUSPECTED:
    1. Drop conf_A for channel C to 0       (forces reactive-only on this channel)
    2. Force pwm_C := pwm_max                (max safe response)
    3. Emit STALL event to spec-16 append-only log
    4. Freeze Layer A learning for C until next calibration
    5. If dT_residual continues climbing past 0.8 * R4 threshold:
       hand off to R4 envelope abort path
```

Thresholds are deliberately set so the R8 stall watchdog fires at half the R4 hardware-protection rate — this gives a soft 30 s window for graceful handoff before the hard envelope abort. Once R4 fires, R8 yields control: R4's hardware-protection logic is authoritative.

The 30 s stall window is chosen because it's long enough to filter out workload spikes (which typically last < 10 s) but short enough that thermal damage hasn't progressed to dangerous levels. It is **not** the R12 confidence τ — that's a separate filter.

## Q4. AIO Pump Read-Only Handling

ventd v0.4.0 already shipped Corsair AIO support as the first native USB HID Linux implementation without liquidctl/Python sidecar. Per project memory, many AIO pump channels are read-only or locked at a vendor-fixed RPM (Arctic Liquid Freezer is locked at 100 %, Corsair Hydro pumps in "balanced/extreme" modes are firmware-controlled). R8's handling:

1. **Telemetry-only classification.** A channel is marked AIO-pump-readonly when (a) its hwmon parent or USB device class identifies it as a pump *and* (b) writes to the PWM attribute return EACCES, EINVAL, or are silently ignored (verified via read-back-and-compare on calibration). The classification is persisted in spec-16 KV under `channels/<id>/role = "aio_pump_readonly"`.

2. **No Layer A learning.** No PWM→RPM curve is learned because PWM is not a controllable input. `conf_A` for the channel is recorded as `N/A` (sentinel value), not zero. The blended-controller pipeline skips the channel entirely.

3. **Layer B participation is conditional.** Pump RPM does carry information — when the pump RPM rises in response to coolant temperature increase, that establishes coupling between the radiator-fan channel and the loop temperature. ventd treats pump RPM as a **read-only sensor** in the Layer B coupling map for *other channels* (the radiator fans), not as a controllable channel. This is consistent with how ventd v0.4.0 already exposes the Corsair pump.

4. **Pump RPM as workload-load proxy when CPU sensors fail.** When no `coretemp` / `k10temp` reading is available (rare; typically only on pre-Sandy-Bridge platforms or virtualized hosts where ventd should not be running anyway per R3), pump RPM correlates well with CPU heat output through the AIO loop and can be used as a Layer C workload-load proxy. This is documented as a tertiary fallback, ranked below Tier 5 RAPL inference.

## Q5. Coarse-Classification Fallback for Layer A

spec-smart-mode §6.6 already specifies a coarse classifier (loadavg + per-CPU util) as the always-available R7 workload-bucket fallback. R8 explicitly recommends that the **Layer A** fallback chain (Tiers 1–7 above) is a *separate dimension* from the R7 workload fallback. The two compose orthogonally:

```
                            R7 workload signature available?
                                  yes              no (loadavg/util fallback)
R8 tier ≥ 1 (any tach):    full smart-mode    coarse smart-mode (Layer C is per-bucket, smaller library)
R8 tier 7 (no tach):       reactive-only      reactive-only (with logging)
```

The cross-product is enforced at the channel-state aggregation step: the channel's effective `conf_A` is the R8-tier-ceiling-clamped value, and the channel's effective `conf_C` is the R7-fallback-attenuated value (separate scalar reduction defined in R12). The two reductions multiply only at the final `min()` aggregation; they are not folded together earlier.

## Q6. BMC IPMI Polling Integration

ventd already has spec-15-experimental support for `idrac9_legacy_raw` (per project memory). R8 generalizes this to a fully-supported in-band path:

- **Path:** `/dev/ipmi0` via the in-kernel `ipmi_si` driver (KCS interface). Out-of-band LAN/RMCP+ is **explicitly out of scope** for v1.0 — ventd is a host-resident daemon, not a remote management tool, and OpenSSL/AES-CBC-128 dependencies for RMCP+ violate the CGO_ENABLED=0 constraint.
- **Library:** Pure-Go IPMI request marshaller, ~600 LoC, no external dependencies. Reference implementations: `metal-stack/go-hal/internal/ipmi` (uses `ipmitool` shell-out — *not* a model), `bougou/go-ipmi` (pure Go but heavyweight). ventd's implementation lives at `internal/fallback/ipmi/` and exposes only: `OpenLocal()`, `GetSDRRepositoryInfo()`, `ReserveSDR()`, `GetSDR(id)`, `GetSensorReading(sensor_id)`. Total surface ≤ 5 functions.
- **Polling cadence:** R11 slow-loop only (60 s). Per-fan SDR reading is one IOCTL round-trip; the kernel's `kipmid` thread handles BMC scheduling. Set `kipmid_max_busy_us=100` via module param at install time to avoid CPU waste from polling.
- **kipmid CPU-waste mitigation.** The kernel docs explicitly warn that `kipmid` "can use a lot of CPU depending on the interface's performance" and recommend tuning `kipmid_max_busy_us`. ventd documents this in the install playbook for server-class deployments.
- **Refusal cascade.** If `/dev/ipmi0` is missing (no `ipmi_si` loaded) or the BMC returns timeouts >2 s consistently, R8 demotes the channel to Tier 4 (thermal inversion) and emits a structured log event. ventd does not autoload kernel modules.

## Q7. Cross-Reference Resolution

**R6 polarity probe on tach-less channels.** R6 currently assumes a tach exists for the 150 RPM noise-floor / 5× SNR test. R8's recommendation: on tach-less channels, replace the RPM noise-floor test with a **thermal-response polarity probe** — write PWM_max, hold for 60 s under stable workload (R7 bucket steady), record ΔT change at the dominant Layer B sensor; write PWM_min, hold for 60 s, record ΔT change. Polarity is positive iff ΔT(PWM_max) < ΔT(PWM_min) by at least 1.0 °C with 3× SNR over the noise floor of the temp sensor (R11). For Class-7 NAS HDD channels (≤ 2.0 °C/min hardware ceiling), the hold time is extended to 180 s because the thermal time constant is much longer. This does **not contradict** R6's locked-in 150 RPM noise floor; it adds a new polarity-probe variant for tach-less channels and leaves the RPM-domain probe untouched for tach'd channels.

**R11 sensor preference admissibility.** Tach-less channels rely entirely on temp-domain signals. R11's latency-vs-τ admissibility rule (a sensor is admissible if its read latency ≤ 0.1× the channel's thermal time constant) governs which temp sensor seeds Layer A. For NAS HDD channels with τ ~ 5 min, even a 30 s SMART poll is admissible; for laptop CPU channels with τ ~ 5 s, only `coretemp`/`k10temp` (sub-millisecond) qualify.

**R14 calibration time budget.** Tach-less channels skip the R6 RPM polarity stage but spend 2× longer in the Envelope C thermal stage to compensate for the lower information-per-sample rate. Concretely: tach'd channels exit Envelope C after 12 PWM steps × 30 s = 6 min; tach-less channels run 12 PWM steps × 60 s = 12 min, then add 4 confirmation steps at PWM_min, PWM_25, PWM_75, PWM_max for an additional 4 min. Total tach-less Envelope C budget: 16 min (vs 6 min). This is the single largest user-visible cost of being tach-less.

**spec-16 persistent state.** Each channel's calibration record carries a new `tach_tier ∈ {0, 1, 2, 3, 4, 5, 6, 7}` field and a `tach_signal_source` enum recording the actual sysfs path or BMC sensor ID. Schema version bumps to v2. The record is recomputed at every Envelope C re-seed and at boot if hwmon enumeration changed.

## Recommended Design (Pseudocode)

```go
// internal/fallback/tier.go
type FallbackTier int
const (
    TierTach          FallbackTier = 0
    TierCoupled       FallbackTier = 1
    TierBMC           FallbackTier = 2
    TierECStepped     FallbackTier = 3
    TierThermalInvert FallbackTier = 4
    TierRAPLProxy     FallbackTier = 5
    TierPWMEcho       FallbackTier = 6
    TierOpenLoop      FallbackTier = 7
)

// ConfACeiling returns the maximum permitted conf_A for a tier.
func (t FallbackTier) ConfACeiling() float64 {
    return [...]float64{1.00, 0.85, 0.70, 0.55, 0.45, 0.30, 0.30, 0.00}[t]
}

// SelectTier walks the preference order and picks the highest-quality
// available signal for a channel.
func SelectTier(ch *Channel, hw *HwmonView, bmc *BMCView, peers []*Channel) FallbackTier {
    if ch.HasReadableTach() && !ch.TachQuantized() {
        return TierTach
    }
    if peer := FindCoupledPeer(ch, peers); peer != nil && peer.Tier == TierTach {
        return TierCoupled
    }
    if bmc != nil && bmc.HasFanSensor(ch.BMCSensorID) {
        return TierBMC
    }
    if ch.HasReadableTach() && ch.TachQuantized() {
        return TierECStepped // dell-smm, cros_ec, framework
    }
    if ch.HasCoupledTempSensor() {
        return TierThermalInvert
    }
    if ch.HasRAPLDomain() {
        return TierRAPLProxy
    }
    if ch.PWMEnableReadback() {
        return TierPWMEcho
    }
    return TierOpenLoop
}

// internal/fallback/stall.go
type StallWatchdog struct {
    channel        *Channel
    sensor         *TempSensor
    dTExpected     func(pwm, load float64) float64  // from Layer B
    sustainedSince time.Time
    r4Threshold    float64                           // °C/s or °C/min, class-dependent
}

func (w *StallWatchdog) Tick(now time.Time, pwm float64, dT_dt float64, load float64) Action {
    expected := w.dTExpected(pwm, load)
    residual := dT_dt - expected
    if pwm >= 0.6*w.channel.PWMMax && residual >= 0.5*w.r4Threshold {
        if w.sustainedSince.IsZero() {
            w.sustainedSince = now
        } else if now.Sub(w.sustainedSince) >= 30*time.Second {
            return ActionStall  // drop conf_A, force max, log, freeze learning
        }
    } else {
        w.sustainedSince = time.Time{}
    }
    return ActionNone
}
```

## Worked Example — Framework 13 AMD Laptop

A Framework 13 AMD laptop reports a single fan via `cros_ec_hwmon`. The fan tach is exposed but quantization is observable (the EC reports values stepped by ~150 RPM increments, and only when fan is above ~2400 RPM). R8 classifies this as Tier 3 (EC-stepped) with conf_A ceiling 0.55. Envelope C runs 12 × 60 s = 12 min at first install. Layer A learns a 4-point curve at PWM = {64, 128, 192, 255} mapping to RPM bins {2400, 3200, 4100, 5000}. After 90 days, conf_A converges to ~0.45 (below the 0.55 ceiling, limited by the coarse residuals). w_pred for this channel converges to ~0.40 in steady state — predictive contributes a meaningful fraction but reactive remains primary.

## Worked Example — TerraMaster F2-210 NAS

The F2-210 is on the limited side of the HIL fleet — exact hwmon support is uncertain. If `fanN_input` is not exposed (likely; the F2-210 uses a Realtek RTD1296 SoC with a custom thermal driver), R8 falls back to Tier 4 (thermal inversion) using SMART HDD temps as the coupled sensor. Class-7 thresholds apply (≤ 2.0 °C/min hardware ceiling); the stall watchdog fires at 1.0 °C/min sustained over 30 s. conf_A ceiling 0.45. This is one of the **HIL gaps R8 cannot validate**: the F2-210 in the fleet is "limited" per project memory and likely cannot run a full smart-mode session. Recommend documenting this as a known v1.0 limitation: NAS-class tach-less channels are supported in code but unvalidated in HIL.

## Spec-Ready Findings Block — R8

```yaml
r8:
  defensible_defaults:
    fallback_tier_ceilings:
      tier_0_tach:           1.00
      tier_1_coupled:        0.85
      tier_2_bmc_ipmi:       0.70
      tier_3_ec_stepped:     0.55
      tier_4_thermal_invert: 0.45
      tier_5_rapl_proxy:     0.30
      tier_6_pwm_echo:       0.30
      tier_7_open_loop:      0.00
    tier_2_poll_interval_s: 60
    tier_3_poll_interval_s: 60
    stall_watchdog:
      pwm_threshold_fraction: 0.6
      dT_residual_fraction_of_r4: 0.5
      sustained_window_s: 30
    coupled_peer_match:
      pwm_response_tolerance_pct: 10
      thermal_coupling_cosine_min: 0.85
    tach_less_envelope_c_budget_min: 16
    tach_less_recalibration_days: 90
    aio_pump_readonly_role: "aio_pump_readonly"   # spec-16 KV value
    bmc_kipmid_max_busy_us: 100
  citations:
    - https://docs.kernel.org/hwmon/sysfs-interface.html       # fanN_input optionality
    - https://docs.kernel.org/hwmon/dell-smm-hwmon.html        # SMM call latency, magic state, whitelist
    - https://www.kernel.org/doc/html/v6.15-rc5/hwmon/cros_ec_hwmon.html  # Framework EC fan readings
    - https://docs.kernel.org/driver-api/ipmi.html             # ipmi_si KCS, kipmid, kipmid_max_busy_us
    - https://www.kernel.org/doc/html/next/power/powercap/powercap.html  # RAPL energy_uj path
    - https://www.intel.com/content/www/us/en/developer/articles/technical/software-security-guidance/advisory-guidance/running-average-power-limit-energy-reporting.html  # CVE-2020-8694 root-only since 5.10
    - https://github.com/DHowett/framework-laptop-kmod         # Framework fan tach quantization
    - https://patchew.org/linux/20240608-cros._5Fec-hwmon-pwm-v1-0-d29dfc26fbc3@weissschuh.net/  # cros_ec_hwmon PWM SET_FAN_DUTY
  reasoning_summary: >
    The hwmon contract makes fanN_input optional. ventd must therefore degrade
    gracefully when no tach exists. R8 specifies a seven-tier fallback chain with
    monotonically-decreasing conf_A ceilings, a thermal-only stall watchdog set at
    half the R4 hardware-protection rate to give graceful-handoff headroom, and a
    pure-Go in-band IPMI marshaller for BMC-managed servers (no ipmitool, no
    CGO). AIO pump-only channels are pinned to telemetry-only and their RPM is
    repurposed as a sensor input to Layer B for sibling channels. The design
    preserves CGO_ENABLED=0 throughout and avoids adding any new userspace
    dependencies; all signals come from already-mainline kernel interfaces.
  hil_validation:
    proxmox_5800x_3060: validates Tier 0 (nct6798) and Tier 1 (coupled-channel inference)
    minipc_celeron: validates Tier 0 baseline and Tier 5 (RAPL proxy)
    13900k_4090: validates Tier 0 + RAPL Tier 5 cross-check
    laptops: validates Tier 3 (dell-smm and/or cros_ec) and Tier 4 (thermal inversion)
    steam_deck: excluded per R3 hardware_refusal (read-only fan)
    f2_210_nas: HIL GAP — Tier 4 (SMART-temp inversion) is designed but not validated
    bmc_ipmi: HIL GAP — no BMC in fleet; Tier 2 IPMI path is designed against kernel docs only
  confidence: Medium-High
  confidence_justification: >
    Tiers 0, 1, 3, 4, 5 are validated against mainline kernel sources and the
    existing ventd HIL fleet. Tiers 2 (BMC IPMI) and Tier 4 on NAS-class hardware
    are HIL gaps. The conf_A ceilings are defensible monotone choices but the
    specific numeric values (0.55 for Tier 3 vs 0.45 for Tier 4) are calibrated
    judgments rather than empirically derived; they should be re-tuned after
    1.0 if field data shows tach-less channels under- or over-contributing.
  spec_ingestion_target: spec-smart-mode §6.7 (new), spec-16 calibration record schema v2
  review_flags:
    - R6 polarity probe: needs a new "thermal-response polarity probe" variant for tach-less channels (see Q7); not a contradiction but an extension.
    - R11 sensor admissibility: confirmed compatible — tach-less channels rely on R11's latency-vs-τ rule unchanged.
    - R14 calibration budget: tach-less channels need 2× envelope-C budget (16 min vs 6 min); spec-r14 should be amended to record this.
    - spec-16: KV schema bump from v1 to v2 to add tach_tier and tach_signal_source.
    - spec-15-experimental ipmi/idrac9_legacy_raw: superseded by R8's pure-Go IPMI marshaller path; deprecate the experimental track or fold it in.
```

## Implementation File Targets — R8

```
internal/fallback/
├── tier.go                  // FallbackTier enum, ConfACeiling, SelectTier
├── coupled/
│   ├── peer.go              // FindCoupledPeer, polarity-and-coupling match
│   └── peer_test.go
├── ipmi/
│   ├── client.go            // OpenLocal(), close, ioctl wrapper
│   ├── sdr.go               // GetSDRRepositoryInfo, ReserveSDR, GetSDR
│   ├── sensor.go            // GetSensorReading, fan-sensor decoding
│   ├── marshal.go           // Pure-Go NetFn/Cmd packet builder
│   └── client_test.go       // Mocked /dev/ipmi0 fixtures
├── ecstepped/
│   ├── dell_smm.go          // dell-smm-hwmon recognition, SMM-latency-aware polling
│   ├── cros_ec.go           // cros_ec_hwmon / framework_laptop recognition
│   └── ecstepped_test.go
├── thermal_invert/
│   ├── inversion.go         // ΔT-given-PWM thermal polarity + slope estimator
│   └── inversion_test.go
├── rapl/
│   ├── reader.go            // /sys/class/powercap/intel-rapl:N/energy_uj reader
│   ├── reader_amdgpu.go     // amdgpu power1_average reader
│   └── admit.go             // Layer A sample admit/reject based on |ΔP_pkg|
├── stall/
│   ├── watchdog.go          // StallWatchdog state machine
│   └── watchdog_test.go
└── aio/
    ├── role.go              // AIO-pump-readonly classification + persistence
    └── sensor_export.go     // Expose pump RPM as Layer B sensor for sibling channels
```

═══════════════════════════════════════════════════════════════════

# R12 — Confidence Formula (Blended Controller)

## Executive Summary

R12 specifies the math behind `conf_A`, `conf_B`, `conf_C` and the aggregation that produces `w_pred ∈ [0,1]`. The recommended design uses three independently-formulated, dimensionless scalars in [0,1] derived from sample-counted, residual-variance-bounded inputs, aggregated via `min()` (per spec-smart-mode §8) and then **smoothed** by a 30 s low-pass filter (`τ_w = 30 s`) with a Lipschitz-bounded slew-rate cap of `dw_pred/dt ≤ 0.05 / s`. Cold-start is hard-pinned to `w_pred = 0` for the first 5 minutes after Envelope C completion, then the smoother is unfrozen and w_pred ramps naturally.

`min()` is retained over harmonic-mean and weighted-product alternatives because (a) the three layers represent **independent failure modes** that compound multiplicatively in real-world impact (a wrong fan curve combined with wrong coupling produces *worse* outcomes than either alone, not better), and (b) `min()` is the most defensible conservative choice in the absence of large-scale field data. The smoothness guarantee is enforced **at w_pred** (not at conf_X individually) so that drift-triggered confidence drops in any single layer cannot bypass the slew-rate cap.

Per-channel confidence is the unit of computation; there is also a global `w_pred_system` that gates whether ventd is in smart-mode at all (it falls to 0 when the daemon is not yet calibrated, when in idle-gate-refused state, or when more than 50 % of channels are in stall-detected state). This global is composed via a different rule (logical AND of per-channel admissibility) and is documented separately.

## Methodology

R12 draws on (a) the canonical RLS literature where `tr(P)` (covariance trace) is the standard confidence proxy — Jia (Iowa State, COMS 4770), Haber's Kalman/RLS derivation, the Lai-Bernstein SIFt-RLS treatment of covariance-blow-up, and the MathWorks `recursiveLS` documentation; (b) the bumpless-transfer literature for the smoothness guarantee — slow/fast decomposition (NTNU), Mathworks PID bumpless modes, and the switched-system literature (Yang et al., 2024 on dynamic output feedback bumpless control); and (c) the existing ventd specs (smart-mode §6, §7, §8; spec-05 RLS+IMC-PI; R7 bucket library; R11 saturation gate; R14 calibration budget).

## Q1. Per-Layer Confidence Formula

### conf_A — Fan Response Curve (PWM → RPM/airflow)

```
conf_A(channel c) =
    ceiling(R8_tier(c)) * sqrt(coverage(c)) * (1 - normalized_residual(c)) * recency(c)
```

where:

- **coverage(c)**: fraction of the PWM range [PWM_min, PWM_max] that has been observed with at least 3 samples each, binned into 16 buckets.
  ```
  coverage(c) = |{bin : sample_count(bin) ≥ 3}| / 16,  ∈ [0,1]
  ```
  The `sqrt()` smooths the rise so coverage from 25 % → 50 % yields a larger conf gain than 75 % → 100 %, which matches the diminishing-returns behavior of curve-fit accuracy.

- **normalized_residual(c)**: ratio of observed PWM-RPM scatter to the per-channel noise floor.
  ```
  normalized_residual(c) = clamp(rms_residual(c) / (k * noise_floor(c)), 0, 1)
  ```
  with `k = 5` (matches R6's 5× SNR margin). When residual = noise floor, this term is 0.2 (so conf_A is reduced 20 % even at perfect fit, reflecting irreducible measurement noise). When residual exceeds 5× noise floor, this term is 1.0 and conf_A → 0.

- **recency(c)**: exponential decay since last admissible Layer A update.
  ```
  recency(c) = exp(-age_seconds / τ_recency)
  τ_recency = 7 days (604800 s)
  ```
  7 days is chosen because dust accumulation and bearing wear evolve on a multi-week timescale; 7 days is a defensible conservative recency horizon that's still long enough to ride out a week-long laptop-in-bag scenario.

- **ceiling(R8_tier(c))**: the R8 tier-dependent ceiling (Q2 of R8). For Tier 0 (RPM tach), ceiling = 1.0 and is a no-op multiplier.

**Tach-less handling.** When tach_tier ≥ 1, the `normalized_residual` term is computed against the chosen fallback observable (e.g., for Tier 3 EC-stepped, residual is in fan-state-bin units; for Tier 4 thermal-invert, residual is the deviation from the Layer-B-predicted ΔT). The R8 tier ceiling clamps the final value.

### conf_B — Thermal Coupling Map

Layer B learns a per-(channel, sensor) coupling coefficient `β(c, s)` that maps PWM change to ΔT change at sensor s. R12 formulates conf_B as the **stability of β across diverse workload visits**:

```
conf_B(channel c) =
    workload_variety(c) * coupling_stability(c) * sample_density(c)
```

where:

- **workload_variety(c)**: fraction of "diverse" R7 workload signatures visited.
  ```
  workload_variety(c) = min(distinct_signatures_observed(c) / N_target, 1.0)
  N_target = 8                                    # 8 distinct R7 buckets
  ```
  R7 maintains a 128-bucket weighted-LRU library; conf_B saturates after 8 visited buckets (1/16 of the library) because beyond that, additional bucket diversity stops materially improving coupling estimates.

- **coupling_stability(c)**: 1 - normalized variance of β across recent sliding windows.
  ```
  Let β̂_w = mean β(c, s) over sliding window w of size 1 hour
  Let σ_β = std-dev of {β̂_w} over last 24 windows (24 h)
  coupling_stability(c) = clamp(1 - σ_β / (σ_β_floor), 0, 1)
  σ_β_floor = 0.2 * mean(β̂)        # 20 % of the coefficient itself
  ```
  When the windowed estimates of β fluctuate by < 20 % of β's magnitude, the coupling is "stable" (conf_B contribution = 1.0). When they fluctuate by ≥ 20 %, this term collapses to 0.

- **sample_density(c)**: bounded sample-count term.
  ```
  sample_density(c) = clamp(N_samples / N_required, 0, 1)
  N_required = 600                              # 10 minutes at 1 Hz
  ```

**Cold-start handling.** For an always-idle laptop that never exercises GPU coupling, `workload_variety` stays low (only 1–2 signatures visited). conf_B remains < 0.25. This correctly signals "Layer B has not yet seen enough variety," and `min()` aggregation collapses w_pred regardless of conf_A and conf_C strength. This is the desired behavior — the controller cannot meaningfully predict GPU-driven heat events on a laptop that has never experienced one.

### conf_C — RLS Marginal-Benefit Estimator (per (channel, signature))

Layer C is per-(channel, R7-signature). Each pair maintains its own RLS estimator with covariance matrix `P_{c,s}`. R12's conf_C is RLS-native, derived from `tr(P)` per the canonical literature (Jia COMS 4770, Haber's RLS derivation, Lai-Bernstein 2024):

```
conf_C(channel c, signature s) =
    saturation_admit(c, s)
    * residual_term(c, s)
    * covariance_term(c, s)
    * sample_count_term(c, s)
```

where:

- **saturation_admit(c, s)**: R11.6 dual-condition gate (range AND slope). Returns 1.0 if R11 admits the current operating point, 0.0 otherwise. Hard binary because saturated samples poison the RLS update; conf_C should not contribute predictive signal from saturated regions.

- **residual_term(c, s)**: RLS innovation residual normalization.
  ```
  e_k = y_k - φ_k^T θ̂_{k-1}              # one-step prediction error
  E_k = α * E_{k-1} + (1 - α) * e_k^2     # EWMA squared residual, α = 0.95
  residual_term = clamp(1 - sqrt(E_k) / E_floor, 0, 1)
  E_floor = baseline noise variance per spec-05's RLS noise model
  ```

- **covariance_term(c, s)**: trace of normalized covariance matrix.
  ```
  P̂ = P / P_init                           # P_init is the initial covariance
  covariance_term = clamp(1 - tr(P̂) / dim(θ), 0, 1)
  ```
  Per Jia's RLS notes (Iowa State), `tr(P)` is monotonically non-increasing in the absence of forgetting (and bounded above per Lai-Bernstein with directional forgetting). Normalizing by `P_init` and dividing by parameter dimensionality gives a [0,1] confidence proxy where 1.0 = "covariance has shrunk to negligible" and 0.0 = "covariance is at initial uncertainty."

- **sample_count_term(c, s)**: lower-bound gate — RLS estimates with too few samples are unreliable regardless of `tr(P)`.
  ```
  sample_count_term = clamp(N_{c,s} / N_min, 0, 1)
  N_min = 50                                # spec-05 default
  ```

**RLS forgetting and covariance blow-up.** Per Lai-Bernstein 2024 and the EFRA literature (Modified RLS with bounded covariance), exponential forgetting can cause `tr(P)` to grow without bound under non-persistent excitation. ventd's RLS implementation must use either bounded-trace forgetting (SIFt-RLS or EFRA) or directional forgetting; pure exponential forgetting is **forbidden**. conf_C as defined here is meaningful only when `tr(P)` is bounded.

## Q2. The min() Aggregation — Tradeoffs

| Aggregation | Cold-start | Steady-state | Failure recovery | Robustness to single-layer fault |
|-------------|-----------|--------------|------------------|----------------------------------|
| **min()** | Slow ramp (limited by slowest layer) | Conservative — never exceeds weakest layer | Fast — single layer fault drops w_pred immediately | High — explicit |
| Weighted product (∏ conf_X^w_X) | Smoother ramp | Less conservative than min() | Slow — multiplicative decay across all layers | Medium — single layer fault attenuates rather than dominates |
| Weighted sum (Σ w_X conf_X) | Fastest ramp (any layer at high conf lifts w_pred) | Aggressive — single high-conf layer can dominate | Poor — single layer fault barely visible | Low — masks faults |
| Harmonic mean (3 / Σ 1/conf_X) | Slow ramp; numerically unstable as conf_X → 0 | Conservative but smoother than min() | Fast | High but with division-by-zero hazard |

**Recommendation: keep `min()`** as specified in spec-smart-mode §8. Justification:
1. **Failure-mode independence assumption is wrong.** A wrong fan curve (Layer A) combined with wrong coupling (Layer B) does not compound favorably; the controller will choose a wrong PWM *and* mis-attribute its thermal effect, producing worse outcomes than either alone. The conservative (min) choice matches this reality.
2. **Solo-developer testability.** `min()` has no hyperparameters; weighted-product and weighted-sum require `w_X` tuning that the HIL fleet cannot exhaustively validate.
3. **Failure-recovery latency is the dominant criterion** for a daemon that controls thermals in safety-critical contexts (NAS HDD, laptop). `min()` and harmonic mean tie on this; harmonic mean's division-by-zero hazard breaks the tie against it.
4. **Smoothness is enforced separately** (Q3) — the conservative reactivity of `min()` is bumpless because of the post-aggregation low-pass filter, not because of the aggregator.

## Q3. Smoothness Guarantee

spec-smart-mode §8 requires no hard switchover. R12 enforces this with a two-stage filter:

```
w_raw(t)    = clamp(min(conf_A, conf_B, conf_C), 0, 1)
w_filt(t)   = w_filt(t - Δt) + (Δt / τ_w) * (w_raw(t) - w_filt(t - Δt))   # 1-pole LPF
w_pred(t)   = w_filt(t - Δt) + clamp(w_filt(t) - w_filt(t - Δt), -L_max * Δt, +L_max * Δt)
```

with:
- **τ_w = 30 s** — the low-pass time constant. Chosen because (a) it is short enough not to delay legitimate confidence growth past the user's perception of "the controller is learning" (most users tolerate ≤ 1 minute of no-visible-change), and (b) it is long enough to absorb single-tick saturation events from R11 and short workload spikes from R7. Equal to the R7 EWMA timescale for consistency.
- **L_max = 0.05 / s** — Lipschitz slew-rate cap on w_pred. Chosen so a full 0 → 1 transition takes ≥ 20 s, which is well below the R7 bucket-stability gate (3-tick `M=3` stability requirement at the slow loop's 60 s tick = 180 s) but well above the fast-loop tick (2 s). This ensures w_pred can never traverse the full range within a single user-perceptible interaction window.
- **No dead-band.** A dead-band around w_pred=0 or w_pred=1 was considered but rejected: the LPF + Lipschitz cap already guarantees no bang-bang oscillation, and a dead-band would create a discontinuity at the band edges that defeats the smoothness guarantee. Instead, ventd uses a "departure latch": once w_pred leaves 0 (i.e., predictive starts contributing), it cannot return to exactly 0 without a full drift-detection trigger; small fluctuations near 0 are smoothed but not snapped.

The LPF is per-channel (independent τ_w state per channel). The Lipschitz cap is also per-channel. The system-level w_pred_system uses a different rule (Q6).

This design is consistent with the bumpless-transfer literature: it's a state-coordinated soft handoff (per Yang/NTNU 2008), not a hard switch with conditioned-controller-state-resets. The reactive controller (PID) and the predictive controller (IMC-PI from spec-05) both run in parallel at all times; only the blend weight changes, which means there is no "switching event" to engineer state-resets around.

## Q4. Cold-Start Behavior

At first-run completion (R14):

| State | conf_A | conf_B | conf_C | w_pred (raw) | w_pred (effective) |
|-------|--------|--------|--------|--------------|---------------------|
| t = 0 (boot, before any calibration) | 0 | 0 | 0 | 0 | **0 (hard-pinned)** |
| Envelope C complete (R14 success) | 0.30 (seed) | 0.10 | 0 (no signatures yet) | 0 | **0 (hard-pinned for 5 min)** |
| Envelope D fallback (R14 partial) | 0.15 (seed) | 0.05 | 0 | 0 | **0 (hard-pinned for 10 min)** |
| 5 min after Envelope C, idle workload | 0.35 | 0.15 | 0.05 | 0.05 | smoothed ramp from 0 |
| 1 hour after Envelope C, mixed workload | 0.55 | 0.40 | 0.20 | 0.20 | ~0.18 |
| 24 hours, diverse workloads observed | 0.75 | 0.65 | 0.55 | 0.55 | ~0.55 |
| Steady state (1 week+) | 0.85 | 0.80 | 0.75 | 0.75 | 0.75 |

**Hard pin at boot.** ventd guarantees `w_pred = 0` for at least 5 minutes after first Envelope C completion (10 minutes after Envelope D fallback). This pin is an explicit gate, not an emergent property of the formula — it ensures purely-reactive behavior during the period when Layer A has only its seed and no validation samples. This is consistent with the bumpless-transfer literature's recommendation: "let it run long enough so that the controller can converge to an accurate state estimate before switching to automatic mode" (MathWorks bumpless-transfer guide).

**First non-zero w_pred.** Allowed when (a) ≥ 5 min has elapsed since Envelope C, (b) at least one R7 workload signature has stabilized (R7's M=3 stability gate), and (c) no R8 stall watchdog has fired in the last 60 s. The first non-zero value is the LPF-smoothed `min()`, ramping from 0 at L_max = 0.05/s slew limit.

**Envelope D path is strictly more conservative.** When R14 falls back to Envelope D (e.g., temperature didn't rise sufficiently during Envelope C to confirm coupling), conf_A starts at 0.15 (vs 0.30) and the hard pin extends to 10 min. This is documented in spec-r14.

## Q5. Confidence Decay and Drift Detection

spec-smart-mode §6.5 specifies drift detection at fixed thresholds: 10 % RPM error, 2 °C/s prediction error sustained over 5 minutes. R12's interaction:

**On drift detection for layer X:**
1. Set `drift_flag(X, c) = true`.
2. Apply a **half-life decay** to `conf_X(c)`: `conf_X(c) := conf_X(c) * 0.5^(t / T_half)` with `T_half = 60 s`.
3. The smoothness guarantee (Q3 LPF + Lipschitz) automatically constrains the rate at which w_pred drops; the decay does not bypass the slew-rate cap. So even though conf_X halves every minute, w_pred falls at most 0.05/s.
4. When `conf_X(c) < 0.05`, freeze layer X learning for that channel and schedule a recalibration event.
5. Drift flag clears after recalibration succeeds; conf_X then re-grows naturally from cold-start values.

**Why half-life decay rather than bang-bang to zero?** Bang-bang to zero violates the smoothness guarantee and creates a controller discontinuity. The smoothness guarantee is the hard contract; drift response must respect it. Half-life decay is monotone, predictable, and compatible with the Lipschitz cap.

**Why T_half = 60 s?** Chosen to be (a) faster than the spec-smart-mode §6.5 sustained-error window (5 min) so by the time drift is confirmed and confidence has decayed to ~3 % of original, the underlying issue has had time to manifest fully; (b) slower than the L_max slew limit so confidence-decay-driven w_pred drops are slew-limited not decay-limited. Both ends have headroom.

## Q6. Per-Channel vs Global Confidence

R12 confirms:
- **conf_A, conf_B, conf_C are computed per-channel.** Layer A is per-channel by definition (each fan has its own curve). Layer B is per-(channel, sensor) coupling but conf_B is collapsed to per-channel by taking the *minimum* coupling confidence across the channel's coupled sensors (worst-coupling-link rule). Layer C is per-(channel, signature); conf_C is collapsed to per-channel by taking the **active-signature** value (the conf_C for the workload signature R7 is currently reporting).
- **w_pred is per-channel.** Each channel has its own `w_pred(c)`; channels do not share blend ratios.
- **Each channel has its own LPF state and Lipschitz state.** No global smoothing at this level.

**Global w_pred_system.** A separate scalar gate that determines whether ventd is in smart-mode at all:
```
w_pred_system = AND(
    calibration_complete,                   # R14 finished successfully on at least one channel
    not idle_gate_refused,                  # R5 did not refuse the active session
    fraction_of_channels_in_stall < 0.5,    # R8 stall watchdogs not in mass-failure
    spec16_state_loaded,                    # spec-16 KV available
)
```
When `w_pred_system = false`, all per-channel `w_pred(c) := 0` (forced reactive, or fall back to firmware automatic if user has explicitly opted in to that mode). The global gate is a logical AND, not a smoothed scalar; transitions of the global gate force a 60 s purely-reactive lockout (the channel-level Lipschitz cap is bypassed when this global gate flips, because a system-level refusal must be honored immediately).

This is the **only** place in R12 where the smoothness guarantee is intentionally broken, and only because the gate is itself a hardware-protection mechanism (R4/R5/R8 mass-failure scenarios). The user-visible behavior is a one-time drop to reactive mode with a logged reason, then a 60 s cool-down before predictive blending may resume; this is preferable to any continuous-blending behavior that could amplify a hardware fault.

## Q7. Confidence Exposure to User

spec-12 wizard mockups (per project memory, currently being reworked for smart-mode) show "confidence indicators per channel updated." R12 prescribes the user-facing layer:

| Internal w_pred(c) range | Categorical state | Color | User-facing label |
|--------------------------|-------------------|-------|-------------------|
| 0.00 (hard pin) | Cold-start | gray | "Calibrating — reactive only" |
| 0.00–0.10 | Warming | amber | "Learning — reactive primary" |
| 0.10–0.40 | Warming | amber | "Learning — predictive contributing" |
| 0.40–0.70 | Converged | green | "Tuned — predictive primary" |
| 0.70–1.00 | Converged | green | "Optimized" |
| any value with drift_flag set | Drifting | red | "Drift detected — recalibrating" |
| any value with stall_flag set (R8) | Aborted | red | "Stall detected — reactive only" |

**Numeric conf is exposed only in `--debug` / `ventctl status --verbose` output**, not in the wizard. Rationale: numeric confidence percentages invite users to chase them ("why is this 67 % and not 100 %?") and create support burden, whereas categorical states map cleanly to actionable user guidance ("wait for it to finish learning"). The numeric values remain available for telemetry, debugging, and persistence.

The boundaries (0.10, 0.40, 0.70) are LPF-output values, not raw `min()` outputs, so the categorical state is itself smooth (the LPF prevents flapping across the boundaries within a single tick). Hysteresis bands of ±0.02 around each boundary further suppress flapping for users watching the wizard live.

## Q8. Confidence Persistence

spec-16 KV holds confidence-related state. R12's persistence model:

**What is persisted:** the **inputs** to the confidence formula, not the formula output. Specifically:
- For conf_A: per-PWM-bin sample count and residual sum-of-squares; last-update timestamp; R8 tier classification.
- For conf_B: per-(channel, sensor) coefficient β, sliding-window means and variances, distinct-signature-visited bitmap.
- For conf_C: per-(channel, signature) RLS state — θ̂, P, EWMA residual E, sample count.

**Why inputs, not output:** the formula may be tuned across versions; persisting outputs would either lock ventd to one formula version or require bidirectional migration. Persisting inputs allows the formula to evolve without losing accumulated learning state. This matches the spec-16 design philosophy of storing observations rather than derived values.

**Persistence frequency:**
- Layer A inputs: every successful Layer A update (~ once per fast-loop tick when admissible) — but batched into a 60 s flush window via spec-16's append-only-log shape.
- Layer B inputs: every 60 s slow-loop tick (sliding-window updates).
- Layer C inputs: every Layer C **promotion event** (R7 bucket transition or RLS step) — not every tick; `tr(P)` doesn't change between RLS steps.

**Recovery semantics on daemon restart:**
1. Load all persisted inputs from spec-16 KV / append-only log.
2. **Recompute** confidence values from inputs using the current formula version. Do *not* trust any persisted confidence scalar (these are recomputed every tick anyway in steady state).
3. Apply a **freshness penalty** based on time since last persistence: `recency` term in conf_A is computed against the persisted timestamp, naturally producing the same output as if the daemon had been continuously running.
4. **Cold-start hard pin re-applies if `time_since_last_persistence > 24 h`** — ventd treats a multi-day-stale state as suspect and re-pins w_pred = 0 for 5 min while it confirms current sensor readings match the persisted Layer A curve. This is a soft-recalibration handshake rather than a full Envelope C re-run.

## Recommended Design (Pseudocode)

```go
// internal/confidence/conf.go

type LayerAState struct {
    Tier                 fallback.FallbackTier
    BinCounts            [16]uint32
    BinResidualSumSquare [16]float64
    LastUpdate           time.Time
    NoiseFloor           float64
}

type LayerBState struct {
    Beta             map[SensorID]float64
    BetaWindowMean   map[SensorID]ringbuf.RingBuf  // 24-window history of 1h means
    SignaturesSeen   bitset.Bitset                  // 128-bit, indexed by R7 bucket
    SampleCount      uint32
}

type LayerCState struct {
    PerSignature map[SignatureID]*RLSState         // RLS estimator per signature
}

type RLSState struct {
    Theta            []float64
    P                [][]float64                    // covariance
    PInit            float64                        // diagonal of initial P
    EWMAResidual     float64
    SampleCount      uint32
    R11Admit         bool                           // dual-condition R11 gate
}

func ConfA(s *LayerAState, ch *Channel) float64 {
    coverage := float64(s.bucketsAtLeast(3)) / 16.0
    rms := math.Sqrt(s.totalResidualSumSquare() / float64(s.totalCount()))
    normRes := math.Min(rms/(5.0*s.NoiseFloor), 1.0)
    age := time.Since(s.LastUpdate).Seconds()
    recency := math.Exp(-age / (7 * 24 * 3600))
    raw := math.Sqrt(coverage) * (1 - normRes) * recency
    return math.Min(raw, s.Tier.ConfACeiling())
}

func ConfB(s *LayerBState) float64 {
    variety := math.Min(float64(s.SignaturesSeen.Count())/8.0, 1.0)
    var stability float64 = 1.0
    for _, history := range s.BetaWindowMean {
        sigma := stddev(history.Snapshot())
        meanBeta := mean(history.Snapshot())
        if meanBeta > 0 {
            term := math.Max(0, 1 - sigma/(0.2*meanBeta))
            stability = math.Min(stability, term)
        }
    }
    density := math.Min(float64(s.SampleCount)/600.0, 1.0)
    return variety * stability * density
}

func ConfC(s *LayerCState, activeSig SignatureID) float64 {
    rls, ok := s.PerSignature[activeSig]
    if !ok {
        return 0
    }
    if !rls.R11Admit {
        return 0
    }
    residual := math.Max(0, 1 - math.Sqrt(rls.EWMAResidual)/spec05.RLSNoiseFloor())
    trP := matrix.Trace(rls.P)
    covTerm := math.Max(0, 1 - trP/(rls.PInit*float64(len(rls.Theta))))
    samples := math.Min(float64(rls.SampleCount)/50.0, 1.0)
    return residual * covTerm * samples
}

// Aggregator with smoothness guarantee
type Blender struct {
    wFilt        float64
    wLast        float64
    lastTick     time.Time
    coldStartEnd time.Time
    driftFlag    bool
    stallFlag    bool
}

const (
    TauW       = 30 * time.Second
    LMaxPerSec = 0.05
)

func (b *Blender) Tick(now time.Time, confA, confB, confC float64) float64 {
    if now.Before(b.coldStartEnd) {
        b.wFilt = 0
        b.wLast = 0
        return 0
    }
    raw := math.Min(math.Min(confA, confB), confC)
    if b.driftFlag || b.stallFlag {
        raw = 0
    }
    dt := now.Sub(b.lastTick).Seconds()
    if dt <= 0 || dt > 10 {
        dt = 0   // discontinuity in time — don't update
    }
    // 1-pole low-pass
    alpha := dt / TauW.Seconds()
    if alpha > 1 {
        alpha = 1
    }
    target := b.wFilt + alpha*(raw-b.wFilt)
    // Lipschitz cap on output
    delta := target - b.wLast
    maxDelta := LMaxPerSec * dt
    if delta > maxDelta {
        delta = maxDelta
    } else if delta < -maxDelta {
        delta = -maxDelta
    }
    b.wFilt = target
    b.wLast = b.wLast + delta
    b.wLast = clamp(b.wLast, 0, 1)
    b.lastTick = now
    return b.wLast
}
```

## Worked Example — 13900K + RTX 4090 Workstation

A Class-3 desktop with two CPU fans, four case fans, and one GPU fan. Envelope C completes in 6 min. After 1 hour of mixed workload (idle, single-thread compile, all-core compile, gaming, idle):

| Channel | conf_A | conf_B | conf_C (gaming sig) | min | w_pred (after LPF + Lipschitz) | State |
|---------|--------|--------|---------------------|-----|--------------------------------|-------|
| CPU fan A | 0.78 | 0.62 | 0.45 | 0.45 | 0.42 | Converged (green) |
| CPU fan B | 0.79 | 0.61 | 0.44 | 0.44 | 0.41 | Converged (green) |
| Case fan 1 | 0.71 | 0.45 | 0.30 | 0.30 | 0.28 | Warming (amber) |
| Case fan 2 | 0.69 | 0.45 | 0.31 | 0.31 | 0.29 | Warming (amber) |
| Case fan 3 | 0.50 | 0.20 | 0.10 | 0.10 | 0.10 | Warming (amber) — low coupling, lightly loaded |
| Case fan 4 | 0.50 | 0.20 | 0.10 | 0.10 | 0.10 | Warming (amber) |
| GPU fan | 0.0 | 0.0 | 0.0 | 0 | 0 | Cold-start (gray) — locked-curve NVIDIA, R8 Tier 7 |

The GPU fan is detected as locked (NVIDIA GPUs typically expose `pwmN_enable=2` only, with no manual override path on Linux without `coolbits`); ventd refuses to write and pins w_pred=0 for that channel. Other channels converge as expected. After 24 hours and exposure to all 8 R7 buckets, conf_B rises to ~0.80 across all controlled channels and w_pred ~ 0.55.

## Worked Example — Drift Event

CPU fan A's bearing wears, dust accumulates. After 90 days, the actual RPM at PWM=128 has drifted from 1400 → 1180 (a 16 % decrease, exceeding the 10 % drift threshold). Drift detection fires at t=t0:

| t (s after t0) | conf_A (with drift decay) | min | w_pred (Lipschitz cap) |
|----------------|---------------------------|-----|------------------------|
| 0 | 0.78 → drift_flag=true | (raw=0) | 0.42 |
| 10 | 0.78 * 0.5^(10/60) = 0.71 | 0 | 0.42 - 0.05*10 = 0.0 reached *not yet*: → 0.42 - 0.5 = clamped to 0 isn't right; with L_max=0.05/s, 10s delta = 0.5, but w_pred can't go below 0 → reaches w_pred=0 at t=8.4s actually **the drift_flag forces raw=0; at L_max=0.05/s, w_pred drops from 0.42 to 0 in 8.4s** |
| 60 | 0.39 (half-life) | 0 | 0 |
| 120 | 0.20 | 0 | 0 |
| 300 (5 min) | 0.024 → freeze layer A learning | 0 | 0 |
| recalibration triggered | — | — | — |

The Lipschitz cap dominates the half-life decay in the first ~10 s — w_pred drops smoothly from 0.42 to 0 over 8.4 s rather than instantly. This is exactly the bumpless behavior the smoothness guarantee promises. The half-life decay is *redundant* in the early portion of a drift event but matters for the freeze-and-recalibrate decision at t=300 s.

## Spec-Ready Findings Block — R12

```yaml
r12:
  defensible_defaults:
    aggregation: "min"                        # of conf_A, conf_B, conf_C
    smoothness:
      tau_w_seconds: 30                       # 1-pole LPF time constant
      lipschitz_per_second: 0.05              # max |dw_pred/dt|
      no_dead_band: true
    cold_start:
      hard_pin_after_envelope_c_min: 5
      hard_pin_after_envelope_d_min: 10
      seed_conf_a_envelope_c: 0.30
      seed_conf_a_envelope_d: 0.15
      seed_conf_b: 0.10
      seed_conf_c: 0.0
    conf_a:
      coverage_bins: 16
      coverage_min_samples_per_bin: 3
      residual_snr_factor_k: 5
      tau_recency_seconds: 604800             # 7 days
    conf_b:
      n_target_signatures: 8
      stability_sigma_floor_fraction: 0.20
      n_required_samples: 600
      window_size: "1h"
      windows_held: 24
    conf_c:
      ewma_alpha: 0.95
      n_min_samples: 50
      forgetting_strategy: "directional_or_efra"  # NOT pure exponential
    drift_response:
      half_life_seconds: 60
      freeze_threshold: 0.05
    user_facing_thresholds:
      cold_start: "<0.001"                     # i.e., the hard-pin floor
      warming_low_high: [0.10, 0.40]
      converged_low_high: [0.40, 0.70]
      optimized: ">0.70"
      hysteresis: 0.02
    persistence:
      what: "inputs (counts, residuals, RLS state)"
      not_what: "computed conf_X scalars"
      flush_window_seconds: 60
      stale_pin_hours: 24
    global_gate:
      stall_fraction_kill_threshold: 0.5
      global_gate_lockout_seconds: 60
  citations:
    - https://faculty.sites.iastate.edu/jia/files/inline-files/recursive-least-squares.pdf  # Jia COMS 4770/5770: tr(P) as confidence proxy
    - https://aleksandarhaber.com/introduction-to-kalman-filter-derivation-of-the-recursive-least-squares-method-with-python-codes/  # RLS covariance derivation
    - https://arxiv.org/pdf/2404.10844                                                       # Lai-Bernstein 2024 SIFt-RLS bounded covariance
    - https://ieeexplore.ieee.org/document/8814711                                            # Modified RLS / EFRA bounded covariance
    - https://www.mathworks.com/help/ident/ref/recursivels-system-object.html                # MathWorks recursiveLS covariance interpretation
    - https://www.mathworks.com/help/simulink/slref/bumpless-control-transfer-between-manual-and-pid-control.html  # Bumpless transfer reference
    - https://skoge.folk.ntnu.no/prost/proceedings/ifac2008/data/papers/2555.pdf            # Bumpless Transfer for Adaptive Switching Controls
    - https://arxiv.org/pdf/2407.18481                                                       # Bumpless transfer of switched systems with output feedback
  reasoning_summary: >
    R12 keeps min() aggregation as specified in spec-smart-mode §8 because the
    three layers represent independent failure modes that compound multiplicatively
    in real-world impact, and the conservative single-layer-dominates behavior
    matches the safety contract. Smoothness is enforced post-aggregation via a
    30 s low-pass filter and a 0.05/s Lipschitz cap, both per-channel. Cold-start
    is hard-pinned to w_pred=0 for 5 minutes after Envelope C completion to
    guarantee no premature predictive contribution. conf_C is RLS-native, derived
    from tr(P) per the canonical literature, and requires the RLS implementation
    to use bounded-covariance forgetting (directional or EFRA) rather than pure
    exponential, to keep tr(P) bounded under non-persistent excitation. Drift
    detection responds via a 60 s half-life decay applied within the slew-rate
    cap, so confidence drops cannot violate the smoothness contract. Persistence
    stores formula inputs (counts, residuals, RLS state) rather than formula
    outputs, allowing formula evolution without state migration.
  hil_validation:
    proxmox_5800x_3060: validates conf_B workload variety with mixed VM workloads; conf_C with diverse R7 signatures
    minipc_celeron: validates cold-start hard-pin and slow conf accumulation on a low-load device
    13900k_4090: validates steady-state convergence with high-thermal-event diversity; GPU-fan Tier-7 pin behavior
    laptops: validates drift detection (battery-powered low-load periods exercise recency decay) and conf_A tier-ceiling clamping
    f2_210_nas: HIL GAP — Class-7 long-thermal-time-constant conf_B stability requires validation that 1h windows over 24h are sufficient
    bmc_ipmi: HIL GAP — slow-poll conf_A integration with Tier-2 path
  confidence: Medium-High
  confidence_justification: >
    The aggregation choice (min) and the smoothness mechanism (LPF + Lipschitz)
    are well-grounded in canonical control theory (bumpless transfer literature)
    and the conf_C tr(P) approach is canonical RLS practice (Jia, Haber, MathWorks).
    The specific numerical thresholds (tau_w=30s, L_max=0.05/s, T_half=60s,
    cold_start_pin=5min) are calibrated judgments anchored to ventd's existing
    locked items (R7 EWMA, R11 tick rates, R14 envelope budgets) rather than
    empirically derived; field tuning is expected after 1.0. The user-facing
    thresholds (0.10, 0.40, 0.70) are deliberately wide bands with hysteresis
    to suppress flapping; if usability testing shows users want tighter feedback,
    these can be tightened without affecting the underlying controller.
  spec_ingestion_target:
    - spec-smart-mode §7 (confidence formulas), §8 (blending math)
    - spec-05 (RLS forgetting strategy constraint: bounded-covariance only)
    - spec-12 (wizard categorical labels)
    - spec-16 (persistence schema for confidence inputs)
  review_flags:
    - spec-05 RLS implementation: must verify forgetting strategy is directional or EFRA, NOT pure exponential. If currently pure exponential, this is a contradiction with R12 and spec-05 needs amendment.
    - R8 cross-reference confirmed: tach-less channels' conf_A is clamped by the R8 tier ceilings; no contradiction.
    - R7 cross-reference confirmed: conf_C uses R7 bucket library for the per-signature key; conf_B uses signature variety. No contradiction.
    - R11 cross-reference confirmed: conf_C uses R11.6 dual-condition saturation gate as a hard binary admit. No contradiction.
    - R14 cold-start values (0.30 conf_A seed for Envelope C, 0.15 for Envelope D) are R12 design choices that should be cross-referenced into spec-r14.
    - spec-smart-mode §6.5 drift thresholds (10% RPM, 2°C/s) are unchanged; R12 only specifies the *response* to detection.
    - Open question: should the global w_pred_system stall-fraction kill threshold (0.5) be tunable per deployment class? Class-7 NAS with 2 fans cannot have <0.5 fail without falling below the threshold from a single-fan stall. Recommend revising to "max(1, ceil(0.5 * N))" channels in stall trigger global lockout.
```

## Implementation File Targets — R12

```
internal/confidence/
├── layer_a.go                // ConfA(); LayerAState
├── layer_a_test.go
├── layer_b.go                // ConfB(); LayerBState; sliding-window stability
├── layer_b_test.go
├── layer_c.go                // ConfC(); RLSState; tr(P) extraction
├── layer_c_test.go
├── blender.go                // Blender struct; Tick(); LPF + Lipschitz
├── blender_test.go           // Bumpless-transfer property tests
├── coldstart.go              // Hard-pin gate; Envelope C/D seed values
├── drift.go                  // Half-life decay; freeze-and-recalibrate trigger
├── drift_test.go
├── persistence.go            // spec-16 KV/log adapter; recompute on load
├── persistence_test.go
├── global_gate.go            // w_pred_system AND-composition; lockout
├── user_state.go             // Categorical label mapping; hysteresis
└── user_state_test.go
```

═══════════════════════════════════════════════════════════════════

# Cross-References Summary

## R8 → R12 (R8 feeds R12)
- R8's `FallbackTier.ConfACeiling()` is consumed directly by R12's `ConfA()` formula as the final clamp.
- R8's stall-detection action (Q3) sets `Blender.stallFlag` and forces `raw := 0`; R12's Lipschitz cap then governs the rate of w_pred drop.
- R8's AIO-pump-readonly classification causes R12 to skip the channel entirely (conf_A := N/A, w_pred := 0 unconditionally).

## R12 → R8 (R12 feeds back into R8)
- R12's drift detection at conf_X < 0.05 triggers an R8 envelope-C re-seed schedule for tach-less channels (which have a 90-day re-seed cadence vs 365-day for tach'd).
- R12's smoothness guarantee constrains the rate at which R8 can disable a channel (no instant-zero of conf_A even on stall detection).

## Both → Other Locked R-Items
- **R5 idle-gate:** R12's global gate consumes R5's "refused" state; R8 stall-detection respects R5 (does not fire during the 300 s idle-gate window).
- **R6 polarity:** R8 Q7 adds a thermal-response polarity-probe variant; R12's conf_A consumes the polarity result as an admissibility prerequisite (no polarity → conf_A := 0 regardless of formula).
- **R7 workload signature:** R12's conf_C is per-(channel, R7 signature). R8 thermal-inversion (Tier 4) requires R7 to hold the workload constant during inference; R8 inherits R7's M=3 stability gate.
- **R11 sensor noise:** R8 Tier-4 thermal inversion is gated by R11 admissibility (latency ≤ 0.1τ); R12's conf_C uses R11.6 dual-condition saturation gate.
- **R4 envelope abort:** R8's stall watchdog fires at half R4's threshold for graceful handoff; R12's drift response yields to R4 unconditionally (R4 abort forces global gate off, which forces all w_pred = 0).
- **R14 calibration budget:** R8 doubles Envelope C budget for tach-less channels (16 min vs 6 min); R12 hard-pins w_pred = 0 for 5 min after R14 completion (10 min after Envelope D fallback).
- **spec-05 RLS+IMC-PI:** R12's conf_C requires spec-05 to use bounded-covariance forgetting (directional or EFRA, not pure exponential). This may require a spec-05 amendment.
- **spec-16 persistent state:** R8 adds `tach_tier` and `tach_signal_source` fields (schema v2); R12 persists confidence-formula *inputs* (counts, residuals, RLS state), not outputs.

# HIL Validation Gaps Surfaced

The following design elements are **not validated** by the existing HIL fleet and are flagged for post-1.0 field validation:

| Gap | Reason | Mitigation |
|-----|--------|-----------|
| R8 Tier 2 BMC IPMI on real BMC hardware | No BMC in fleet (Proxmox host has consumer board) | Pure-Go IPMI marshaller is implemented and unit-tested against captured real-BMC fixtures; deploy to early-access user with iDRAC/iLO |
| R8 Tier 4 thermal inversion on F2-210 NAS | F2-210 is "limited" per project memory | Documented v1.0 limitation; smart-mode disabled by default on TerraMaster NAS until validated |
| R12 conf_B sliding-window stability with 24-hour windows on a NAS workload (Class 7, very long τ) | No representative fleet member; F2-210 too limited | The 1h window × 24 history may need to lengthen to 4h × 24 for τ ~ 5min channels — flagged for early-access tuning |
| R12 cold-start hard-pin duration (5 min) | Subjective UX choice | A/B test possible via opt-in feature flag once 1.0 ships |
| R12 user-facing categorical thresholds (0.10/0.40/0.70) | Subjective UX choice | Users can request `--debug` numeric output; thresholds are not load-bearing for safety |

# Glossary of Symbols Used

- **τ_w**: low-pass filter time constant for w_pred smoothing (= 30 s)
- **L_max**: Lipschitz slew-rate cap on w_pred (= 0.05 /s)
- **T_half**: half-life of confidence decay during drift response (= 60 s)
- **τ_recency**: exponential decay time constant for conf_A recency term (= 7 days)
- **β(c, s)**: per-(channel, sensor) thermal coupling coefficient (Layer B)
- **θ̂**: RLS parameter estimate (Layer C, per signature)
- **P**: RLS covariance matrix (Layer C, per signature)
- **tr(P)**: trace of P; canonical RLS confidence proxy
- **EWMA**: exponentially weighted moving average
- **EFRA**: Exponential Forgetting and Resetting Algorithm (bounded-covariance RLS variant)
- **SIFt-RLS**: Subspace of Information Forgetting RLS (Lai-Bernstein 2024 directional forgetting)