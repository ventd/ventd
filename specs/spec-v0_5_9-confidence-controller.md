# spec-v0_5_9 — Confidence-gated controller (smart-mode integration)

**Status:** DESIGN. Drafted 2026-05-01 after five rounds of research
review against R5/R7/R8/R9/R10/R11/R12/R13/R15/R16/R17/R18/R19/R20
plus historical spec-04 (resurrected from git #668) and spec-05
(v0.8.0 forward-compat target).
**Ships as:** v0.5.9 (ninth and final pre-v0.6.0 smart-mode patch).
**Subsumes:** spec-04 PI autotune (deleted via #668; this patch
absorbs its scope per spec-smart-mode §10.1).
**Depends on:**
- v0.5.4 passive observation log (shipped) — provides the per-tick
  Record stream the IMC-PI controller and conf_A coverage histogram
  consume.
- v0.5.5 opportunistic active probing (shipped) — fills Layer-A
  coverage gaps; signguard already wired in v0.5.8.
- v0.5.6 workload signature library (shipped) — the active-signature
  collapse rule that produces per-channel `conf_C` from per-(channel,
  signature) shards keys on `signature.Library.Label()`.
- v0.5.7 Layer-B thermal coupling (shipped) — `coupling.Snapshot.Theta`
  supplies the autoregressive coefficient `a` and self-coupling
  `b_ii` that v0.5.9's IMC-PI parameterises gains from.
- v0.5.8 Layer-C marginal-benefit (shipped) — `marginal.Snapshot`
  supplies `ConfidenceComponents`, `SaturationAdmitR11` (Path-B), and
  the `Theta` v0.5.9 re-derives Path-A from at the controller call
  site.
- **v0.5.8.1 patch** (sequenced before this PR-A) — populates
  `observation.Record.SensorReadings` from the controller's per-tick
  sensor map. Without this, conf_A coverage cannot be computed and
  Layer-C cannot consume real ΔT.

**Consumed by:**
- v0.5.10 doctor recovery surface — surfaces `conf_A`/`conf_B`/`conf_C`
  raw component values per channel, recent drift events, the global
  `w_pred_system` AND-gate state, and the cold-start hard-pin
  remaining ticks per R12 §Q7 / R13 §1 item 8.
- v0.6.0 — first version where smart-mode is the default;
  v0.5.9 ships behind a config toggle so v0.5.10's doctor surface
  has a stable consumer to render before v0.6.0 flips defaults.

**References:**
- `specs/spec-smart-mode.md` §6, §7, §8, §10.1 (spec-04 subsumption),
  §11 (patch sequence), §12 (HIL strategy), §16 (success criteria).
- `docs/research/r-bundle/R8-R12-tachless-fallback-and-blended-confidence.md`
  — R8 fallback tier table, R12 §Q1 confidence-product formula,
  R12 §Q3 LPF/Lipschitz/drift-decay smoothness machinery, R12 §Q5
  drift handling, R12 §Q6 active-signature collapse + global gate,
  R12 §Q7 doctor surface contract, R12 §Q8 persistence-of-inputs.
- `docs/research/r-bundle/R9-R10-identifiability-and-shards.md` —
  R9 identifiability (used to gate `conf_B` and refuse PI on
  unidentifiable shards), R10 §10.4/§10.5 shard architecture.
- `docs/research/r-bundle/R11-sensor-noise-floor-thresholds.md` —
  saturation thresholds, noise floor values used in `conf_A`
  residual normalisation.
- `docs/research/r-bundle/ventd-R12-amendment-threshold-recalibration.md`
  — R12 amendment Drift 2 (warmup admit subsumes sample_count),
  Drift 4 (covariance term entry value).
- `docs/research/r-bundle/ventd-R13-doctor-depth.md` — R13 §1 surface
  contract for confidence breakdown.
- `docs/research/r-bundle/ventd-R15-spec-05-audit.md` — R15 §104
  KV namespace convention `smart/conf-A/<channel>` mirrors v0.5.7
  `smart/shard-B/` and v0.5.8 `smart/shard-C/`.
- `docs/research/r-bundle/ventd-R16-anomaly-detection-unsupervised.md`
  — R16 sets `drift_flag` on a per-layer EWMA-residual basis;
  v0.5.9's aggregator must accept `drift_flag` from a future R16
  consumer without code change.
- `docs/research/r-bundle/ventd-R18-acoustic-objective-no-mic.md` —
  v0.5.9 acoustic cost stub `cost = k·|ΔRPM|` is R18's degenerate
  SLEW_TERM. `Config.Smart.PresetWeightVector` reserved for R18
  forward-compat.
- `docs/research/r-bundle/ventd-R19-battery-aware-portables.md` —
  R19 will overlay `preset_weight_vector` with battery-modulation
  vector in v0.7+; v0.5.9's preset enum is the surface that
  swap targets.
- spec-04 (deleted via #668; resurrected from git history at
  /tmp/spec-04-pi-autotune-historical.md and
  /tmp/spec-04-amendment-historical.md) — the IMC-PI gain
  formulas, conditional-integration anti-windup pattern, and
  bumpless-transfer init are taken from spec-04 verbatim.
- spec-05 v0.8.0 (`specs/spec-05-predictive-thermal.md`) — v0.5.9's
  PI control law is identical to spec-05 §4.4; only the τ/K source
  differs (v0.5.9 reads from Layer-B AR; v0.8.0 reads from RLS-ARX
  FOPDT). Single-file estimator-source swap when v0.8.0 lands.

---

## 1. Why this patch exists

After v0.5.7 + v0.5.8, the daemon contains three learning surfaces:

- **Layer A** (calibration coverage + curve-fit residuals) —
  emitted by v0.5.1-v0.5.5 but never aggregated into a `conf_A`
  scalar.
- **Layer B** (per-channel thermal coupling) — `coupling.Snapshot`
  ships, but no consumer.
- **Layer C** (per-(channel, signature) marginal benefit) —
  `marginal.Snapshot` ships, but no consumer.

v0.5.9 is the **first patch that turns these snapshots into
controller behaviour**. Concretely:

1. **A new `conf_A` scalar** per channel, computed from R8 fallback
   tier + 16-bin coverage histogram + curve-fit RMS residual + 7-day
   recency.
2. **A confidence aggregator** that produces `w_pred ∈ [0, 1]` per
   channel from `min(conf_A, conf_B, conf_C)` with R12-locked
   smoothing (LPF τ_w=30 s, Lipschitz L_max=0.05/s, per-layer drift
   decay 0.5^(t/60s)).
3. **A predictive controller**: per-channel IMC-PI whose gains are
   derived from Layer-B's `(a, b_ii)` per the spec-04 formulas. The
   PI is the `predictive_output` term in
   `output = w_pred · predictive_output + (1 − w_pred) · reactive_output`.
4. **A saturation refusal gate**: re-derives Layer-C Path-A
   (predicted ΔT < 2°C) at the controller call site for the
   *candidate* ΔPWM. Refuses ramps that pay full acoustic cost for
   zero thermal benefit (spec-smart-mode §7.1).
5. **An acoustic cost stub**: linear `cost = k·|ΔRPM|`, scaled by
   the operator's preset (Silent / Balanced / Performance). The
   `benefit > cost · preset_factor` test gates ramps in the
   non-saturated regime. Forward-compatible with R18's full
   psychoacoustic model via `Config.Smart.PresetWeightVector`.
6. **A first-contact invariant**: on the first tick where a channel
   has `w_pred > 0`, if the predictive output is below the reactive
   output, the controller MUST clamp output to reactive. Per
   spec-smart-mode §16 success criterion 2.
7. **A confidence indicator UI surface**: 5-state per-channel pill
   (Cold-start / Warming / Converged / Drifting / Refused) plus a
   global worst-of-channels indicator, plus a popover with R12 §Q7
   long-form per-layer reason text. No raw `conf_X` numbers in the
   primary surface (those live in v0.5.10 doctor internals).

v0.5.9 is the largest patch in the v0.5.x sequence by LOC, by rule
count, by HIL surface, and by integration risk. It is also the
last patch before v0.6.0 (smart-mode complete).

## 1.1 Ground-up principle

**R8 + R12 + spec-04 (resurrected) + spec-05 forward-compat are the
design of record.** v0.5.9 transcribes their locked formulas;
revisions to `conf_A` term shape, IMC-PI gain derivation,
LPF/Lipschitz/drift parameters, or preset-factor semantics require
amendments to those R-items, not to this patch's spec.

Where the v0.5.9 research review surfaced under-specified items
they appear in §10 below as new R-items (R33-R39); none blocks
v0.5.9. The PI predictive output's correctness depends on
`K = b_ii / (1 − a)` (NOT `K = b_ii`); this was caught during
research and is now load-bearing in §2.2.

---

## 2. Architecture

### 2.1 Blended output (R12 §Q3)

Per-channel, per-tick:

```
predictive_output = imc_pi_output(channel, sensor_reading, setpoint, dt)
reactive_output   = curve_eval(channel, sensor_reading)        # existing v0.4.x path
w_pred = aggregator(channel, conf_A, conf_B, conf_C, drift_flag)
output = w_pred · predictive_output + (1 − w_pred) · reactive_output
```

The blend is **smooth**: `w_pred` rides the LPF + Lipschitz cap so
no PWM step exceeds `L_max·dt = 0.1 PWM-units/tick` from the
blending alone (PI ramps are bounded separately by §2.2 anti-windup
and §2.6 saturation refusal).

After the blend, the existing safety chain runs: `RULE-HWMON-CLAMP`
clamps to `[MinPWM, MaxPWM]`; `RULE-HWMON-PUMP-FLOOR` enforces the
pump floor; `polarity.WritePWM` applies inversion; `watchdog`
records the value for shutdown restore.

### 2.2 Predictive controller — IMC-PI from Layer-B (R-bundle, spec-04)

Per channel `i` at tick `k`, the IMC-PI gains derive from Layer-B's
estimate as follows:

```
a   = coupling.Snapshot[i].Theta[0]                          // discrete AR coeff
b   = coupling.Snapshot[i].Theta[1]                          // self-coupling b_ii (Theta[1] when N_coupled=0)
dt  = controller.PollInterval                                // 2 s default
τ   = clamp(-dt / ln(a),    50 s, 1800 s)                    // thermal time constant; cap covers NAS class
K   = | b / (1 − a) |                                        // STEADY-STATE GAIN MAGNITUDE
λ   = aggressiveness_to_lambda(Config.Smart.Preset)          // Silent: 2τ, Balanced: τ, Performance: τ/2
θ   = dt                                                     // fixed one-tick transport delay
K_p = τ / (K · (λ + θ))
K_i = K_p / τ
```

**Two load-bearing decisions:**

1. **`K = b / (1−a)` (NOT `K = b` directly)** — `b` is the per-tick
   coefficient, while `K` is the STEADY-STATE gain after the AR
   pole settles. Using `b_ii` directly produces gains too small by
   ~50× at typical `a ≈ 0.98`, making the controller unable to
   track. spec-04 historical used `K = ΔT/ΔPWM` from a settled
   relay envelope, which IS the steady-state `b/(1−a)`.

2. **Take the magnitude `|·|`.** For cooling fans `b < 0`, but the
   PI controller is implemented over the polarity-already-handled
   abstraction: more PWM ⇒ more cooling, regardless of physical
   wiring (handled in `polarity.WritePWM`, RULE-POLARITY-05). The
   gain that matters to the controller is the magnitude of process
   response per PWM unit. Using the signed `b/(1−a)` would produce
   `K_p, K_i < 0`, and with error = sensor − setpoint > 0 (too hot)
   would integrate `I[k]` more negative on every tick, driving
   predictive PWM below baseline ⇒ less cooling ⇒ hotter ⇒ larger
   error: positive feedback, instability. The earlier draft of this
   spec claimed "the math carries through via bumpless init" — it
   does not (verified numerically in v0.5.9 PR-A.3 implementation
   notes). Taking `|·|` and keeping `K_p, K_i > 0` is the correct
   formulation.

PI control law per tick:

```
error = sensor_reading - setpoint                  // °C; positive = too hot
I[k]  = I[k-1] + K_i · error · dt                  // integrator (with anti-windup, see §2.3)
u[k]  = K_p · error + I[k]                         // PI correction signal (PWM units, signed)
predictive_output = baseline_pwm + u[k]            // baseline = current curve eval = reactive_output
```

`u[k]` is positive when too hot ⇒ predictive PWM rises above the
reactive baseline ⇒ more cooling. Polarity-inverted fans are
handled in `polarity.WritePWM` (RULE-POLARITY-05), not the PI math.

**Instability guards** (RULE-CTRL-PI-05): refuse predictive output
(force `w_pred=0` for that channel-tick) when any of:
- `a ≤ 0` or `a ≥ 1` (thermally divergent estimate)
- `b_ii == 0` (no observable response)
- `coupling.Snapshot.WarmingUp == true` (parent not yet trustworthy)
- `coupling.Snapshot.Kappa > 10⁴` (R10 unidentifiable; defense in
  depth even after warmup gate)
- `K = 0` (denominator collapse)
- `λ + θ < 1e-6` (numerical guard)

### 2.3 Anti-windup + bumpless transfer (spec-04)

**Anti-windup: conditional integration.** When the PI's proposed
output saturates against `[MinPWM, MaxPWM]` AND the integrator
would push further into saturation, freeze the integrator that
tick (don't accumulate). Same trigger fires when Layer-C Path-A
saturation refuses the ramp (see §2.6). The integrator unfreezes
on the first tick where (a) output is unsaturated OR (b) error
sign reverses.

Justification: parameter-free, matches spec-04 historical PR-1
pattern, composes cleanly with both PWM-clamp saturation and
Path-A refusal under one mechanism.

**Bumpless transfer.** On the first tick where `w_pred > 0` for a
channel (i.e. `w_pred` rises from 0), initialise the integrator so
the PI's correction signal `u[0]` is zero. Since
`predictive_output = baseline_pwm + u[k]` and `baseline = reactive`,
zero correction means `predictive_output[0] == reactive_output[0]`:

```
I[0] = -K_p · error                                   // makes u[0] = K_p·error + I[0] = 0
```

(Earlier draft of this spec gave `I[0] = (reactive − K_p·error) / K_i`,
which is dimensionally inconsistent — dividing PWM-units by K_i
yields seconds, not PWM-units. The correct formula above is derived
directly from `u[0] = 0`.)

The blend is then continuous through the warmup→active transition;
no PWM step. Bumpless init is skipped (set `I[0] = 0`) when
`|K_p| < 1e-9` to avoid pathological cases where `K_p ≈ 0` makes
the magnitude term meaningless.

### 2.4 Layer-A confidence (`conf_A`)

Per-channel scalar from R12 §Q1:

```
conf_A = R8_tier_ceiling × √coverage × (1 − norm_residual) × recency
```

with:

- **R8_tier_ceiling** ∈ {1.00 (Tier-0 RPM tach), 0.85 (Tier-1
  coupled inference), 0.70 (Tier-2 BMC IPMI), 0.55 (Tier-3 EC
  stepped), 0.45 (Tier-4 thermal-invert), 0.30 (Tier-5/6 RAPL/
  pwm_enable echo), 0.00 (Tier-7 open-loop pinned)}. Selected per
  R8 §"Spec-Ready Findings" by `internal/fallback.SelectTier()`
  (new in v0.5.9 PR-A).
- **coverage** = `|{bin ∈ [0..15]: count ≥ 3}| / 16`. Bin width 16
  raw PWM units (0/16/32/.../240). Per-bin sample counts maintained
  in `BinCounts [16]uint32` per channel, updated on every
  controller tick (synchronously in the obs hook chain).
- **norm_residual** = `clamp(rms_residual / (5 · noise_floor), 0, 1)`
  where `rms_residual = sqrt(Σε² / N)` over the curve-fit residuals
  (predicted RPM at written PWM, minus observed RPM next tick) and
  `noise_floor = 150 RPM` for tach'd channels (R6) with
  per-tier-equivalent fallbacks for tach-less.
- **recency** = `exp(-age_seconds / 604800)` (τ = 7 days). `age` =
  wall-clock since the last admissible Layer-A update for this
  channel.

### 2.5 Confidence aggregation (R12 §Q3)

The aggregator runs **per channel**, every controller tick. Inputs:

- `conf_A[c]` from §2.4
- `conf_B[c]` from `coupling.Snapshot[c].Confidence` (existing
  Layer-B aggregation; treats `Snapshot.WarmingUp` as `conf_B = 0`)
- `conf_C[c]` collapsed from per-(channel, signature) shards via
  the **active-signature rule**: `conf_C[c] = marginal.Runtime
  .Shard(c, signature.Library.Label()).Confidence` evaluated as the
  R12 §Q1 four-term product (saturation_admit × residual ×
  covariance × sample_count). When the active signature has no
  warmed shard, `conf_C[c] = 0` (we accept the drop; the LPF rides
  it down at L_max).
- `drift_flag[c, layer]` from R16 (currently always false in
  v0.5.9; structure ready for R16 to set it later).

Aggregation, in order:

```
1. drift decay per layer:
   conf_X_decayed = conf_X · 0.5^(seconds_since_drift_set / 60)   if drift_flag set
                  = conf_X                                         otherwise

2. min collapse:
   w_raw = clamp(min(conf_A_decayed, conf_B_decayed, conf_C_decayed), 0, 1)

3. LPF (wraps the min — NOT each component separately):
   w_filt = w_filt_prev + (dt / τ_w) · (w_raw - w_filt_prev)       τ_w = 30 s

4. Lipschitz clamp on the LPF delta:
   w_pred = w_filt_prev + clamp(w_filt - w_filt_prev, -L_max·dt, +L_max·dt)
   L_max = 0.05 / s    (so ≤ 0.1 step per 2 s tick)
```

**Cold-start hard pin**: the first 5 minutes after Envelope C
completion, `w_pred = 0` regardless of the formula. R12 §Q4 locks
the cold-start window to 5-10 min depending on class; v0.5.9 pins
5 min uniformly (HIL data from v0.5.9 release telemetry will
inform a class-aware refinement in v0.6.x).

**Global gate `w_pred_system`** (R12 §Q6) — AND of:
- `state.SchemaVersionLoaded == true`
- `idle.HardPreconditions().Ok()` (battery / container / scrub)
- `probe.LoadWizardOutcome() == OutcomeControl`
- no mass-stall in the last `MassStallDuration` (3 min default)

When `w_pred_system == false`, every channel's effective `w_pred`
is forced to 0, bypassing the per-channel Lipschitz cap (this is
the only path that bypasses Lipschitz; matches R12 §Q6).

### 2.6 Saturation refusal — Path-A re-derive at controller call site

Per the v0.5.8 §8 R31 disambiguation: Layer-C's `Snapshot
.SaturationAdmitR11` (Path-B observed gate) feeds the conf_C
*product term*. Path-A (predicted ΔT < 2°C) is a **controller
decision flag**, not a confidence input — re-derived at the v0.5.9
controller call site for the *candidate* ΔPWM:

```
candidate_ΔPWM = predictive_output - reactive_output
load_now       = idle.CaptureLoad("/proc")
margin         = marginal.Snapshot[c].Theta[0] + Theta[1] · load_now
predicted_ΔT   = margin · candidate_ΔPWM

if !marginal.Snapshot[c].WarmingUp && predicted_ΔT < SaturationDeltaT (2°C):
    refuse_ramp = true
    output = reactive_output            // fall through to reactive only
    freeze_integrator = true            // anti-windup hook
```

No amendment to v0.5.8 `marginal.Snapshot` is required — re-deriving
at the call site uses fields already exposed (`Theta`, `WarmingUp`,
`SaturationAdmitR11`).

### 2.7 Acoustic cost gate (R18 forward-compat stub)

Linear cost in v0.5.9, R18 SLEW_TERM in degenerate form:

```
k_factor[Silent]      = 3.0   · k_Balanced
k_factor[Balanced]    = 1.0   · k_Balanced              k_Balanced = 0.01 °C-equivalent / RPM
k_factor[Performance] = 0.2   · k_Balanced

cost(ΔRPM) = k_factor[preset] · |ΔRPM|
```

`benefit(ΔRPM)` = predicted_ΔT (signed; negative = cooling) from
§2.6. The gate test:

```
proceed_with_ΔRPM iff -benefit(ΔRPM) > cost(ΔRPM)
```

(i.e. predicted *cooling magnitude* must exceed acoustic cost.)
The saturation refusal in §2.6 is checked **first**; cost gate is
the second guard, only relevant when ΔT prediction is non-trivial
but small.

`Config.Smart.Preset` is the operator-visible enum. `Config.Smart
.PresetWeightVector` is a reserved 4-float `{w_thermal, w_acoustic,
w_power, w_responsiveness}` populated from the enum at startup;
v0.5.9 reads only `w_acoustic` (mapped to `k_factor`). v0.7+ R18
fills the other three; R19 overlays for battery state. Operator
surface stays stable across versions.

### 2.8 First-contact invariant (spec-smart-mode §16)

On the first tick where `w_pred > 0` for a channel whose persisted
`seen_first_contact == false`:

```
if predicted_output < reactive_output:
    output = reactive_output                  // clamp; never reduce cooling on first contact
    seen_first_contact = true                 // persisted only after this clamped tick succeeds

else:
    output = blended (normal path)
    seen_first_contact = true
```

**Per-lifetime, persisted** in `smart/conf-A/<channel>` bucket.
Re-armed only on `WipeNamespaces` (RULE-PROBE-09 / RULE-POLARITY-09
extension to include the conf-A namespace).

### 2.9 Confidence indicator (5-state)

Per-channel state collapses `(w_pred, drift_flag, w_pred_system)`
into one of:

| State | Trigger |
|---|---|
| **Refused** | `w_pred_system == false` for any reason |
| **Drifting** | any `drift_flag[c, layer] == true` |
| **Cold-start** | within 5 min of last Envelope C completion (cold-start hard pin) |
| **Warming** | `0 < w_pred ≤ 0.4` AND not above states |
| **Converged** | `w_pred > 0.4` AND not above states |

(Refused outranks Drifting; Drifting outranks Cold-start; etc.
The lower-confidence state always wins for safety.)

R12 §Q4 uses hysteresis bands `±0.02` around the 0.40 boundary to
prevent flap.

**UI render**:
- Global pill (top of dashboard): worst-of-channels collapse.
- Per-fan-card pill (in fan grid): channel's own state.
- Click → popover with R12 §Q7 long-form reasons per layer (no
  raw `conf_X` numbers in primary surface).
- Devices page: layered breakdown.
- v0.5.10 doctor internals fold: full numeric component values.

**Animation**: Cold-start gradient sweep, Warming amber pulse (2 s
matches fast-loop tick), Converged static green + breathing glow,
Drifting strobe (1 Hz red↔amber), Refused static halt-grey.
Boundary-crossing emits a one-off ribbon ("Now: Warming"); CSS
`@media (prefers-reduced-motion: reduce)` disables the ribbon and
strobe (replaces with static label changes).

---

## 3. Locked design decisions

### 3.1 Aggressiveness and preset

`Config.Smart.Preset` is the closed enum `{silent, balanced,
performance}`. It drives BOTH:

- IMC-PI `λ`: 2τ / τ / τ/2 respectively
- Acoustic cost factor `k`: 3× / 1× / 0.2× of `k_Balanced`

One operator knob, two coherent behavioural offsets. Default:
`balanced`.

### 3.2 IMC-PI parameters

Locked: τ cap `[50, 1800] s` (uniform across system classes;
covers NAS while bounding the `a→1` denominator), `θ = dt = 2 s`
fixed, conditional-integration anti-windup, bumpless transfer with
`I[0]` skip when `|K_i| < 1e-9`.

### 3.3 Cold-start hard pin

5 minutes uniform (R12 §Q4 envelope is 5-10 min by class; v0.5.9
ships uniform; v0.6.x can refine from telemetry).

### 3.4 LPF / Lipschitz / drift parameters

R12-locked: `τ_w = 30 s`, `L_max = 0.05/s`, drift `T_half = 60 s`.

### 3.5 Persistence

KV namespace `smart/conf-A/<channel>` per R15 §104:

```go
type ConfABucket struct {
    SchemaVersion         uint8       `msgpack:"schema_version"`     // bump = discard
    HwmonFingerprint      string      `msgpack:"hwmon_fingerprint"`  // mismatch = discard
    Tier                  uint8       `msgpack:"tier"`               // R8 fallback tier at last admit
    BinCounts             [16]uint32  `msgpack:"bin_counts"`         // per-bin sample histogram
    BinResidualSumSq      [16]float64 `msgpack:"bin_residual_sum_sq"`// per-bin Σε² for RMS residual
    NoiseFloor            float64     `msgpack:"noise_floor"`        // frozen at admit, refreshed on tier change
    LastUpdateUnix        int64       `msgpack:"last_update_unix"`   // drives recency
    TierPinnedUntilUnix   int64       `msgpack:"tier_pinned_until_unix"` // R8 §90-day Envelope-C re-seed
    SeenFirstContact      bool        `msgpack:"seen_first_contact"` // first-contact invariant per-lifetime
}
```

Same invalidation rules as v0.5.7 / v0.5.8: hwmon_fingerprint
mismatch discards; schema version mismatch discards (no migration);
restored values clamped on load.

### 3.6 Disable inheritance

Per spec-smart-mode §6.7:

- `Config.Smart.Disabled == true` → `w_pred_system = false`,
  every channel falls through to `reactive_output`. Existing
  `Config.SignatureLearningDisabled` and
  `Config.SmartMarginalBenefitDisabled` toggles continue to
  short-circuit their respective layers (already shipped).
- R1 Tier-2 BLOCK (containers/VMs) → identical effect.
- R3 hardware-refused (Steam Deck etc.) → identical effect.

### 3.7 Refresh cadences

- IMC-PI gains: recomputed on `coupling.Snapshot[c].NSamples`
  advancing past last-seen + 60 (≈once/2 min at 2 Hz updates), NOT
  every tick.
- conf_A: recomputed every controller tick from in-memory
  histogram; persisted every 60 s.
- conf_C active-signature collapse: every tick (lock-free
  `signature.Library.Label()` is cheap).
- Persistence: every 60 s same as v0.5.7/v0.5.8.

---

## 4. Public surface

```go
// internal/confidence/layer_a/snapshot.go (new package)
type Snapshot struct {
    ChannelID         string
    Tier              uint8           // R8 fallback tier
    R8Ceiling         float64         // 0.0..1.0
    Coverage          float64         // 0.0..1.0
    RMSResidual       float64
    NoiseFloor        float64
    Age               time.Duration   // since last admissible update
    ConfA             float64         // 0.0..1.0
    SeenFirstContact  bool
}
type Estimator struct { /* ... */ }
func New(cfg Config) (*Estimator, error)
func (e *Estimator) Observe(channelID string, pwmWritten uint8, rpm int32, predictedRPM int32, now time.Time)
func (e *Estimator) Read(channelID string) *Snapshot   // lock-free atomic.Pointer
func (e *Estimator) SnapshotAll() []*Snapshot
func (e *Estimator) MarkFirstContact(channelID string) // persisted by next periodic save
func (e *Estimator) Save(stateDir, hwmonFingerprint string) error
func (e *Estimator) Load(stateDir, currentHwmonFingerprint string, logger *slog.Logger) error

// internal/confidence/aggregator/aggregator.go (new package)
type Snapshot struct {
    ChannelID         string
    ConfA             float64
    ConfB             float64
    ConfC             float64
    DriftFlags        [3]bool       // [A, B, C]
    Wraw              float64       // pre-LPF
    Wfilt             float64       // post-LPF, pre-Lipschitz
    Wpred             float64       // post-Lipschitz, this tick's gate
    UIState           string        // "cold-start" | "warming" | "converged" | "drifting" | "refused"
}
type Aggregator struct { /* ... */ }
func New(cfg Config) *Aggregator
func (a *Aggregator) Tick(channelID string, confA, confB, confC float64,
    driftFlags [3]bool, wPredSystem bool, now time.Time) *Snapshot
func (a *Aggregator) Read(channelID string) *Snapshot
func (a *Aggregator) SnapshotAll() []*Snapshot
func (a *Aggregator) SetDrift(channelID string, layer int, set bool, now time.Time)

// internal/controller/blended.go (new — sits in existing internal/controller/)
type BlendedConfig struct {
    Preset          PresetEnum  // Silent / Balanced / Performance
    Setpoint        float64     // °C
    PWMUnitMax      int
    Aggregator      *aggregator.Aggregator
    LayerB          *coupling.Runtime
    LayerC          *marginal.Runtime
    LayerA          *layer_a.Estimator
}
type BlendedController struct { /* ... */ }
func NewBlended(cfg BlendedConfig) *BlendedController

// Per-tick: caller passes reactive output, blended controller returns final PWM
func (b *BlendedController) Compute(channelID string, sensorTemp float64,
    reactivePWM uint8, dt time.Duration, now time.Time) BlendedResult

type BlendedResult struct {
    OutputPWM         uint8
    Wpred             float64
    PathARefused      bool
    CostRefused       bool
    FirstContactClamp bool
    UIState           string
}

// internal/fallback/role.go (new)
type FallbackTier uint8
const (
    Tier0_RPMTach     FallbackTier = 0  // 1.00
    Tier1_CoupledRef  FallbackTier = 1  // 0.85
    Tier2_BMCIPMI     FallbackTier = 2  // 0.70
    Tier3_ECStepped   FallbackTier = 3  // 0.55
    Tier4_ThermalInv  FallbackTier = 4  // 0.45
    Tier5_RAPL        FallbackTier = 5  // 0.30
    Tier6_PWMEcho     FallbackTier = 6  // 0.30
    Tier7_OpenLoop    FallbackTier = 7  // 0.00
)
func SelectTier(ch *probe.ControllableChannel, peers []*probe.ControllableChannel,
    bmcView *bmc.View) FallbackTier
func TierCeiling(t FallbackTier) float64
```

`Compute` is non-blocking and synchronous; the per-channel work
fits in <100 µs even at d=2 Sherman-Morrison + IMC-PI math + 5
guard checks. No per-channel goroutines (matches v0.5.8's
RULE-CMB-RUNTIME-01 simplification).

---

## 5. Rule bindings

### 5.1 conf_A family — `RULE-CONFA-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CONFA-FORMULA-01: ConfA = R8_ceiling × √coverage × (1−norm_residual) × recency.` | `internal/confidence/layer_a/estimator_test.go:TestConfA_Formula` |
| `RULE-CONFA-COVERAGE-01: bin width = 16 raw PWM units; coverage counts bins with ≥3 obs.` | `internal/confidence/layer_a/estimator_test.go:TestCoverage_BinWidth` |
| `RULE-CONFA-RECENCY-01: recency = exp(-age_seconds/604800); resets only on admissible update.` | `internal/confidence/layer_a/estimator_test.go:TestRecency_DecayHalfLife7d` |
| `RULE-CONFA-TIER-01: R8 tier ceilings {1.00, 0.85, 0.70, 0.55, 0.45, 0.30, 0.30, 0.00}.` | `internal/confidence/layer_a/estimator_test.go:TestTierCeilings_Locked` |
| `RULE-CONFA-PERSIST-01: KV namespace smart/conf-A/<channel>; persists inputs not output.` | `internal/confidence/layer_a/persistence_test.go:TestPersistence_Namespace` |
| `RULE-CONFA-PERSIST-02: hwmon_fingerprint mismatch on Load discards.` | `internal/confidence/layer_a/persistence_test.go:TestPersistence_FingerprintInvalidation` |
| `RULE-CONFA-PERSIST-03: Schema version mismatch on Load discards.` | `internal/confidence/layer_a/persistence_test.go:TestPersistence_SchemaMismatch` |
| `RULE-CONFA-SNAPSHOT-01: Read() lock-free via atomic.Pointer.` | `internal/confidence/layer_a/estimator_test.go:TestSnapshotReadIsLockFree` |
| `RULE-CONFA-FIRSTCONTACT-01: SeenFirstContact persisted; re-armed only on WipeNamespaces.` | `internal/confidence/layer_a/firstcontact_test.go:TestFirstContact_PersistedPerLifetime` |

### 5.2 Aggregator family — `RULE-AGG-*`

| Rule | Bound subtest |
|---|---|
| `RULE-AGG-MIN-01: w_raw = clamp(min(conf_A_decayed, conf_B_decayed, conf_C_decayed), 0, 1).` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_MinCollapse` |
| `RULE-AGG-LPF-01: LPF wraps the min, NOT each component; τ_w = 30 s.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_LPFWrapsMin` |
| `RULE-AGG-LIPSCHITZ-01: \|w_pred − w_pred_prev\| ≤ L_max·dt = 0.1 PWM-units/2s.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_LipschitzClamp` |
| `RULE-AGG-DRIFT-01: drift_flag triggers per-layer 0.5^(t/60s) decay BEFORE min collapse.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_DriftDecaysBeforeMin` |
| `RULE-AGG-COLDSTART-01: w_pred = 0 for 5 min after Envelope C completion.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_ColdStartHardPin` |
| `RULE-AGG-GLOBAL-01: w_pred_system AND-gate forces every channel's w_pred to 0; bypasses Lipschitz on flip.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_GlobalGate` |
| `RULE-AGG-SIG-COLLAPSE-01: conf_C[channel] = active-signature shard's product term; 0 when no warmed shard.` | `internal/confidence/aggregator/aggregator_test.go:TestAggregator_ActiveSignatureCollapse` |

### 5.3 Predictive controller family — `RULE-CTRL-PI-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CTRL-PI-01: gains derived from (a, b_ii) per fixed formula; K = b_ii/(1−a).` | `internal/controller/blended_test.go:TestPI_GainDerivation` |
| `RULE-CTRL-PI-02: conditional-integration anti-windup on PWM saturation OR Path-A refusal.` | `internal/controller/blended_test.go:TestPI_AntiWindup_Conditional` |
| `RULE-CTRL-PI-03: integrator initialised on first w_pred>0 tick to bumpless value.` | `internal/controller/blended_test.go:TestPI_BumplessTransfer` |
| `RULE-CTRL-PI-04: τ clamped to [50s, 1800s]; gains refreshed every ~60 samples (~2 min).` | `internal/controller/blended_test.go:TestPI_TauCapAndRefreshCadence` |
| `RULE-CTRL-PI-05: refuse PI when a∉(0,1) OR b_ii=0 OR WarmingUp OR κ>10⁴ OR K=0.` | `internal/controller/blended_test.go:TestPI_InstabilityGuards` |

### 5.4 Blended controller family — `RULE-CTRL-BLEND-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CTRL-BLEND-01: output = w_pred · predictive + (1−w_pred) · reactive; clamped per RULE-HWMON-CLAMP after blend.` | `internal/controller/blended_test.go:TestBlended_FormulaAndClamp` |
| `RULE-CTRL-BLEND-02: Path-A re-derived at call site from Theta + load + candidate ΔPWM; refusal forces output=reactive.` | `internal/controller/blended_test.go:TestBlended_PathARefuse` |
| `RULE-CTRL-BLEND-03: cost gate fires only when not Path-A-refused; benefit > cost·preset_factor.` | `internal/controller/blended_test.go:TestBlended_CostGate` |
| `RULE-CTRL-BLEND-04: first-contact never reduces cooling; clamp to reactive when predictive < reactive on first w_pred>0 tick.` | `internal/controller/blended_test.go:TestBlended_FirstContactClamp` |
| `RULE-CTRL-BLEND-05: integrator freeze on Path-A refusal AND on PWM clamp saturation (anti-windup).` | `internal/controller/blended_test.go:TestBlended_IntegratorFreeze` |

### 5.5 Preset family — `RULE-CTRL-PRESET-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CTRL-PRESET-01: Config.Smart.Preset is the closed enum {silent, balanced, performance}.` | `internal/config/config_test.go:TestPreset_ClosedEnum` |
| `RULE-CTRL-PRESET-02: PresetWeightVector reserved 4-float; v0.5.9 reads only w_acoustic.` | `internal/config/config_test.go:TestPresetWeightVector_Reserved` |
| `RULE-CTRL-PRESET-03: λ mapping: silent=2τ, balanced=τ, performance=τ/2.` | `internal/controller/blended_test.go:TestPreset_LambdaMapping` |
| `RULE-CTRL-PRESET-04: k_factor mapping: silent=3·k_B, balanced=k_B, performance=0.2·k_B; k_B=0.01°C/RPM.` | `internal/controller/blended_test.go:TestPreset_CostFactorMapping` |

### 5.6 Cost-gate family — `RULE-CTRL-COST-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CTRL-COST-01: cost(ΔRPM) = k_factor · |ΔRPM|; non-negative for all inputs.` | `internal/controller/blended_test.go:TestCost_LinearAbsValue` |
| `RULE-CTRL-COST-02: Balanced + warmed shard reduces to spec-05 IMC-PI behaviour; no gate-driven refusal under nominal load.` | `internal/controller/blended_test.go:TestCost_BalancedReducesToSpec05` |
| `RULE-CTRL-COST-03: saturation refusal precedes cost test; cost test never overrides saturation.` | `internal/controller/blended_test.go:TestCost_OrderingVsSaturation` |

### 5.7 UI family — `RULE-UI-CONF-*`

| Rule | Bound subtest |
|---|---|
| `RULE-UI-CONF-01: 5-state collapse Refused>Drifting>Cold-start>Warming>Converged with hysteresis ±0.02 around 0.40.` | `internal/web/ui_confidence_test.go:TestUIState_Collapse` |
| `RULE-UI-CONF-02: dashboard renders global pill + per-fan pill with text label + colour token (no colour-only).` | `internal/web/ui_confidence_test.go:TestUI_PillRendering` |
| `RULE-UI-CONF-03: popover surfaces R12 §Q7 long-form per-layer reason; no raw conf_X numbers in primary surface.` | `internal/web/ui_confidence_test.go:TestUI_PopoverContent` |
| `RULE-UI-CONF-04: prefers-reduced-motion media query disables boundary ribbon and strobe animations.` | `internal/web/ui_confidence_test.go:TestUI_ReducedMotion` |

### 5.8 Wiring family — `RULE-CTRL-WIRING-*` (PR-B)

| Rule | Bound subtest |
|---|---|
| `RULE-CTRL-WIRING-01: aggregator + layer_a + blended controller launched together when len(channels)>0.` | `cmd/ventd/main_blended_test.go:TestWiring_AllOrNone` |
| `RULE-CTRL-WIRING-02: /api/v1/confidence/status endpoint exposes per-channel UIState + ConfidenceComponents.` | `internal/web/server_confidence_test.go:TestEndpoint_Confidence` |
| `RULE-CTRL-WIRING-03: Settings page Smart-mode panel exposes Preset enum radio + per-channel disable.` | `internal/web/server_confidence_test.go:TestSettings_PresetSurface` |

**Total v0.5.9 rule additions: ~32**, plus 1 v0.5.8.1 rule below.

### 5.9 v0.5.8.1 — `RULE-OBS-SENSOR-*`

| Rule | Bound subtest |
|---|---|
| `RULE-OBS-SENSOR-01: Record.SensorReadings populated as map[uint16]int16 (millidegrees, clamped ±32767), keyed by SensorIDFor(path).` | `internal/controller/controller_test.go:TestEmitObservation_PopulatesSensorReadings` |

---

## 6. Patch sequence

### 6.1 v0.5.8.1 — SensorReadings plumbing patch (~80 LOC, 3 tests)

Lands first as a tiny defect-class patch. Per Agent B's analysis:
the field exists in the schema but has been unpopulated since
v0.5.4. Without this patch, conf_A coverage cannot be computed
from the observation log, and v0.5.9 PR-A would have to inline
the same logic.

```
internal/observation/record.go            — add SensorIDFor(path) helper
                                              (mirrors existing ChannelID)
internal/controller/controller.go         — emitObservation() walks
                                              c.rawSensorsBuf, converts to
                                              int16 millidegrees with ±32767
                                              clamp, keys by SensorIDFor
internal/controller/controller_test.go    — TestEmitObservation_Populates...
.claude/rules/observation.md              — add RULE-OBS-SENSOR-01
release-notes/v0.5.8.1.md
CHANGELOG.md
```

No schema bump (RULE-OPP-OBS-01 forward-compat already permits
populating an existing field). No new packages. Privacy review
passes (RULE-OBS-PRIVACY-01 field-tag exclusion check unaffected;
sensor IDs are FNV-32a hashed, not raw paths).

### 6.2 PR-A — confidence packages + IMC-PI + UI (~1500 LOC, ~32 tests)

Largest PR-A in the v0.5.x sequence. Five new packages plus a
controller extension plus UI surface:

```
internal/fallback/                  (new)
  role.go                           — R8 FallbackTier classifier
  role_test.go

internal/confidence/layer_a/        (new)
  estimator.go                      — Estimator + Snapshot + atomic.Pointer
  formula.go                        — conf_A four-term product
  bins.go                           — BinCounts histogram update + recovery
                                       from observation log
  persistence.go                    — Bucket Save/Load
  firstcontact.go                   — first-contact persisted bool
  *_test.go                         — 9 RULE-CONFA-* + 1 first-contact

internal/confidence/aggregator/     (new)
  aggregator.go                     — Tick(), drift decay, min, LPF, Lipschitz
  state.go                          — per-channel rolling state
  global_gate.go                    — w_pred_system AND-gate inputs
  ui_state.go                       — 5-state collapse + hysteresis
  *_test.go                         — 7 RULE-AGG-*

internal/controller/blended.go      (new file in existing package)
  - BlendedController + Compute()
  - IMC-PI math + anti-windup + bumpless transfer
  - Path-A re-derive
  - Cost gate
  - First-contact clamp
internal/controller/blended_test.go — 5+5+3 RULE-CTRL-PI/BLEND/COST tests

internal/web/                       (extend)
  ui_confidence.go                  — 5-state collapse helpers
  ui_confidence_test.go             — 4 RULE-UI-CONF-*

web/                                (extend)
  dashboard.html / .css / .js       — global pill + per-fan pill
  shared/confidence-pill.html       — reusable pill component
  shared/tokens.css                 — pill state tokens (cold/warm/conv/drift/refused)

internal/config/config.go           (extend)
  - Add Config.Smart.Preset enum
  - Add Config.Smart.PresetWeightVector reserved struct
  - Add Config.Smart.Setpoints map[channel]float64
internal/config/config_test.go      — 2 RULE-CTRL-PRESET-* tests

specs/spec-v0_5_9-confidence-controller.md  (this doc)
.claude/rules/{confa,aggregator,ctrl-pi,ctrl-blend,ctrl-preset,ctrl-cost,ui-conf}.md
.claude/RULE-INDEX.md (regenerated)
```

### 6.3 PR-B — main.go wiring + status endpoint (~250 LOC, 3 tests)

```
cmd/ventd/main.go                         (extend)
  - Construct internal/fallback tier classifier early
  - Construct layer_a.Estimator alongside coupling/marginal Runtimes
  - Construct aggregator.Aggregator
  - Construct controller.BlendedController; thread into per-fan controller
  - Launch aggregator tick goroutine (per-channel state, lock-free reads)
cmd/ventd/main_blended_test.go            — RULE-CTRL-WIRING-01 test

internal/web/server.go                    (extend)
  - Add /api/v1/confidence/status (returns Snapshot per channel)
  - Add /api/v1/confidence/preset (GET/PUT preset)
internal/web/server_confidence_test.go    — RULE-CTRL-WIRING-02, -03

web/settings.{html,css,js}                (extend)
  - Smart-mode panel: preset radio (Silent/Balanced/Performance)
  - Per-channel "use predictive" toggle (default on)

.claude/rules/marginal.md                 (extend with RULE-CTRL-WIRING-*)
```

### 6.4 PR-C (optional, deferred to v0.5.10) — UI polish + reduced-motion

The dashboard pill + popover + animation work could ship in PR-A,
but the larger UI polish tracked in #746/#747/#748/#749/#750/#751
is more naturally bundled into v0.5.10 doctor PR. PR-A ships the
*minimum-viable* pill + popover; PR-C polishes the animations,
applies reduced-motion media query, fixes calibration-page UX,
and lands the curve graph aspect-ratio fix.

---

## 7. Verification

### 7.1 Synthetic (CI, every PR)

All RULE-* subtests run on every push. PR-A's 32 new bindings + the
v0.5.8.1 binding bring the total ventd rule count to ~210.

### 7.2 HIL fleet (v0.5.9 has the broadest spread to date)

| Host | IP | Class | What v0.5.9 validates here |
|---|---|---|---|
| pve (Proxmox) | 192.168.7.10 | MidDesktop | Already running v0.5.8 fresh-install soak. Baseline IMC-PI on 5800X / IT8688 (5 channels). Marginal estimator already accumulating data. v0.5.9 install replaces v0.5.8; fresh 48 h soak resumes against the predictive controller. |
| MiniPC | 192.168.7.222 | MiniPC | Monitor-only mode (per spec-smart-mode §12, MiniPC class typically has no controllable channels). Validates that v0.5.9 cleanly degrades: aggregator emits `Refused` global pill (no channels), no blended controller starts, no panics. |
| TNAS | 192.168.7.66 | NASHDD | NEW — first NAS HIL since v0.5.7. Validates the τ_max=1800s cap actually covers HDD thermal masses; Layer-B coupling estimate stable on slow-loop drivetemp; cold-start hard-pin holds for 5 min after Envelope C; opportunistic prober's slow-loop gate (R11 §0 N=3 reads / 3 min) interacts correctly with conf_A coverage histogram. |
| 3 laptops | (USB-boot) | Laptop | EC handshake + battery refusal under R3. Validates that conf_A R8 tier classifier picks Tier-3 (EC-stepped) for thinkpad_acpi / dell-smm-hwmon / cros_ec_hwmon channels and clamps `R8_ceiling = 0.55`. First-contact invariant tested on warm-laptop boot (channel re-encountered after suspend/resume). |
| Main desktop | (USB-boot dual-boot) | High-end (13900K + 4090) | Top-end Intel thermal coupling + NVML R515+ writes (per spec-smart-mode §12 reserved for v0.5.7-v0.5.9). Validates Layer-B coupling on a 6+ channel system, aggregator Lipschitz cap holds under fast workload transitions, NVML predictive output behaves under driver-managed fans. |

### 7.3 Post-install soak target

48 h soak per host at v0.5.9 fresh-install. Sample rate hourly,
log to `stability.ndjson`. Validation gates:

- `active=active` continuous (no daemon restarts unaccounted for)
- `apparmor_denials_1h == 0`
- `api_ok=true` continuous
- `confidence_state` cycles through Cold-start → Warming →
  Converged within the 24 h window (at least one channel reaches
  Converged on the desktop / TNAS hosts; MiniPC stays Refused).
- `drift_flag` count == 0 for the first 24 h on any layer.
- `pwm_clamp_count == 0` (no channels hit PWM=0 unexpectedly; no
  channels hit PWM=255 unexpectedly).

### 7.4 Time-bound metric (success criterion 1 from spec-smart-mode §16)

> "First contact in monitor mode → control mode → fans tracking
> within 60 minutes of fresh install on a non-trivial workload."

v0.5.9 verification: on the main desktop with stress-ng running a
mixed CPU/GPU load, at least one channel must reach Converged
within 60 minutes of fresh install completing the wizard. If not,
the Layer-B identifiability or Layer-C signature library is
under-provisioned and we file a follow-up R-item.

---

## 8. Estimated cost

| Component | Estimate |
|---|---|
| v0.5.8.1 SensorReadings patch | $2-4 |
| PR-A (5 packages + UI minimum) | $30-45 |
| PR-B (wiring + status endpoint + Settings) | $8-12 |
| Total | **$40-61** |

Largest patch in the v0.5.x sequence by ~50% above v0.5.8's
$28-45 (which itself was ~50% above v0.5.7). Reflects the
integration nature: five new packages, one new controller
behaviour, ~32 new rule bindings, broadest HIL surface.

The IMC-PI math + anti-windup + bumpless transfer alone are
roughly v0.5.8's PR-A in scope. Confidence aggregator + Layer-A
estimator are independent additional packages. UI surface
adds another non-trivial slice.

Cost-control levers if budget pressure arises:
- Defer UI polish to PR-C / v0.5.10 (saves ~$5-10 from PR-A).
- Defer `Config.Smart.Setpoints` interactive UI to v0.5.10
  (config-file-only initially).
- Skip the drift_flag plumbing surface in favour of leaving R16
  to wire it later (saves ~$3 from aggregator).

---

## 9. References

In addition to the R-bundle references in the header:

- `specs/spec-v0_5_7-thermal-coupling.md` — Layer-B Snapshot shape
  consumed by IMC-PI gain derivation.
- `specs/spec-v0_5_8-marginal-benefit.md` — Layer-C Snapshot shape
  consumed by Path-A re-derive + cost gate.
- `specs/spec-05-predictive-thermal.md` §4.4 — IMC-PI control law
  (forward-compat target for v0.8.0 estimator swap).
- spec-04 historical (resurrected from #668; saved at
  /tmp/spec-04-pi-autotune-historical.md and amendment).

External references added during research review:

- Skogestad, *Probably the best simple PID tuning rules in the
  world* (2003) — IMC-PI gain rules with `K_p = τ/(K(λ+θ))`.
- Goodwin & Sin, *Adaptive Filtering, Prediction and Control*
  (Prentice-Hall 1984), §6.5 — bumpless transfer init.
- Åström & Hägglund, *Advanced PID Control* (ISA 2006), Ch. 3.5 —
  conditional-integration anti-windup.
- ASHRAE Handbook — HVAC Systems & Equipment Ch. 21 (fan affinity
  laws; informs the τ cap of 1800 s on NAS-class thermal masses).

---

## 10. Open follow-up R-items

Three rounds of research review surfaced these gaps for post-v0.5.9
evaluation. None blocks v0.5.9.

| ID | Question | Status |
|---|---|---|
| **R33** | Is 5-min cold-start hard pin universal? Spec-smart-mode §6 + R12 §Q4 say "5-10 min depending on class". v0.5.9 ships uniform; v0.6.x may refine from telemetry. | v0.6.x |
| **R34** | Class-aware τ cap. Uniform 1800 s caps NAS but is loose for laptop EC (where τ ≈ 30-60 s). Refinement deferred until R8 telemetry shows Laptop class hitting the cap. | v0.6.x |
| **R35** | bumpless-init formula degrades when `\|K_i\| < 1e-9`. Current spec falls back to `I[0] = 0`; alternative: defer first-contact admit until `\|K_i\|` rises (signals adequate plant excitation). | v0.5.10 |
| **R36** | Drift detection signal source. R16 anomaly detection is post-v0.6.0; v0.5.9 ships the structure (`drift_flag` accepted by aggregator) but no setter. v0.5.10 doctor may want a manual "mark drift" button. | v0.5.10 |
| **R37** | Three-controller stable coexistence (R23 from earlier audit). cpufreq + powercap + ventd writing concurrently. spec-04-historical mentions this; v0.5.9 doesn't address it. | v0.7.x |
| **R38** | k_Balanced calibration from telemetry. v0.5.9 ships a literature-derived value (0.01 °C/RPM); R20 fleet federation in v0.7+ should refit from real fleet data. | v0.7+ |
| **R39** | First-contact persistence schema migration. If a future spec adds fields to ConfABucket, the discard-on-mismatch policy means first-contact protection re-arms — operator may not expect this. v0.6.x should evaluate either schema migration OR splitting first-contact into its own KV key. | v0.6.x |

---

**End of spec.**
