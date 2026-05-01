# spec-v0_5_8 — Per-(channel, signature) marginal-benefit estimator (Layer C)

**Status:** DESIGN. Drafted 2026-05-01 after research review (R9/R10/R11 cross-check).
**Ships as:** v0.5.8 (eighth smart-mode behaviour patch).
**Depends on:**
- v0.5.4 passive observation log (shipped) — supplies the per-tick
  Record stream that the RLS estimator consumes.
- v0.5.6 workload signature library (shipped) — supplies the
  `signature_label` field that v0.5.8 keys per-workload shards on.
  R7's lock-free `Library.Label()` is the canonical reader.
- v0.5.7 Layer-B thermal coupling (shipped) — Layer-C overlay
  shards parented to v0.5.7's Layer-B base shard for prior seeding
  per R10 §10.7.
- v0.5.0.1 spec-16 persistent state (shipped) — supplies the KV
  storage shape used for shard persistence with hwmon-fingerprint
  invalidation.

**Consumed by:**
- v0.5.9 confidence-gated controller — the predicted marginal
  benefit `β_0 + β_1·load` and saturation flag drive the
  `benefit > cost · preset_factor` test in spec-smart-mode §7.1.
  `conf_C` rises with shrinking RLS residuals.
- v0.5.10 doctor — surfaces "saturation reached for workload X"
  per (channel, signature) line in `ventd doctor` output.

**References:**
- `specs/spec-smart-mode.md` §6.3 (Layer C description), §7.1
  (saturation refusal in objective), §7.2 (preset interaction),
  §8.2 (`conf_C` consumption), §11 (patch sequence).
- `docs/research/r-bundle/R9-R10-identifiability-and-shards.md` —
  locked design of record. v0.5.8 transcribes R10 §10.1 (Layer-C
  parametric form `d_C = 2`), R10 §10.7 (overlay shard activation
  + prior seeding from Layer-B), R9 §9.5 (per-signature detection
  actions table).
- `docs/research/r-bundle/R11-sensor-noise-floor-thresholds.md` —
  R11 §0 locks the canonical saturation threshold: ΔT < **2 °C**
  over **20 writes** (= 2 s at 10 Hz fast loop, OR 3 reads at
  ~1 read/min for HDD/NAS slow loop). v0.5.8 references R11 §0
  directly rather than restating the constant in this spec.
- `docs/research/r-bundle/R7-workload-signature-hash.md` — R7
  §Q5 caps the signature library at 128 buckets with weighted-LRU
  eviction (τ=14d). The v0.5.8 shard map inherits the same cap
  per channel.
- `docs/research/r-bundle/ventd-R12-amendment-threshold-recalibration.md`
  — bounded-covariance directional forgetting. The locked R12
  tr(P) cap is applied to Layer-C updates exactly as it is to
  Layer-B updates.

---

## 1. Why this patch exists

Smart-mode's optimisation target (spec-smart-mode §7.1) is:

```
proceed_with_ΔRPM iff benefit(ΔRPM) > cost(ΔRPM) * preset_factor
```

The benefit term is **the predicted ΔT for a candidate ΔPWM under
the current workload signature**. Without that prediction the
controller cannot refuse a saturating ramp — it would pay full
acoustic cost for zero thermal benefit. v0.5.8 ships the function
that computes it.

Per spec-smart-mode §6.3, marginal benefit is workload-dependent:
idle saturation differs from sustained-CPU saturation differs from
sustained-GPU saturation. Therefore Layer C must shard per workload
signature, not just per channel — a single channel-level model
would average across regimes and produce a function that fits no
specific workload well.

v0.5.8 emits no UI surface and does not yet feed any controller —
v0.5.9's blended controller is the first consumer. v0.5.8's only
visible behaviour at the application layer is the doctor surface
(deferred to v0.5.10).

## 1.1 Ground-up principle

**R9 + R10 + R11 + R7 are the design of record.** v0.5.8 transcribes
the locked analytics into Go and tests; it does not re-litigate
model order, sharding strategy, identifiability thresholds,
saturation thresholds, eviction policy, or signature hashing.

Future revisions to any of those decisions require an amendment to
the R-bundle, not to this patch's spec. Where the v0.5.8 draft
review surfaced under-specified items they are listed in §8 below
as new R-items registered for follow-up; v0.5.8 itself does not
change any locked decision.

---

## 2. Architecture

### 2.1 Model (per R10 §10.1)

Per (channel, signature) pair, the locked parametric form is:

```
ΔT_predicted_drop_per_+1_PWM[k] = β_0,s + β_1,s · load_i[k]
```

with the linear regressor:

```
φ[k] = [ 1, load[k] ]ᵀ ∈ ℝ^d, d_C = 2
θ    = [ β_0, β_1 ]ᵀ   ∈ ℝ^d
```

The full predicted ΔT for a candidate ΔPWM is:

```
ΔT_pred(ΔPWM | load) = (β_0 + β_1·load) × ΔPWM
```

`d_C = 2` is fixed by R10 §10.1. R10 deliberately keeps Layer C
narrow because (a) physical thermal coupling is workload-
independent and already lives in Layer B's `b_ij`, (b) the
operating-point sensitivity *is* workload-dependent and is exactly
what β_0 + β_1·load captures, and (c) at d=2 the κ identifiability
detector is trivially well-conditioned per R10 §10.2 — no per-shard
window is needed.

### 2.2 Saturation rule (per R11 §0)

R11 locks the canonical saturation rule:

> If ramping PWM produces ΔT < **2 °C** over **20 consecutive
> writes** (= 2 s at 10 Hz fast loop), declare the channel
> saturated. For HDD/NAS slow-loop channels: ΔT < 2 °C over 3
> sensor reads (= 3 min at 1 read/min).

Two saturation paths fire the flag:

**Path A — Predicted (RLS-driven):** when a candidate ΔPWM is
proposed, predict ΔT = (β_0 + β_1·load) × ΔPWM. If ΔT < 2 °C, set
`Snapshot.Saturated = true` for this (channel, signature) at the
current load.

**Path B — Observed (R11-locked):** when 20 consecutive writes
have produced |ΔT| < 2 °C in the active signature's window, set
`Snapshot.Saturated = true` until a subsequent write produces
|ΔT| ≥ 2 °C.

The two paths agree when the model is well-fit. Path B is the
correctness fallback when learning hasn't converged or when the
model is wrong-direction (see §3.7 below).

### 2.3 Sharding

Shards are keyed by `(channel_id, signature_label)`:

- `channel_id` is the PWM sysfs path (R24 forward-compat string
  identity, same as v0.5.7).
- `signature_label` is R7's opaque hex digest from
  `signature.Library.Label()`. The reserved labels
  `fallback/disabled` and `fallback/warming` are filtered: shards
  are NOT created for those two labels.

Per channel the shard map is capped at `MaxShardsPerChannel = 32`
with weighted-LRU eviction inheriting R7's eviction score
`HitCount × exp(-(age/τ))` with `τ = 14 days`.

### 2.4 Update mechanism

Each controller tick produces an `observation.Record` carrying
`PWMWritten`, `RPM`, `SignatureLabel`, plus per-sensor readings
(`SensorReadings map[uint16]int16`, sensor IDs hashed via
`observation.SensorID(path)`). The Layer-C update path
subscribes to the same v0.5.6 `ObsAppend` hook; v0.5.8 PR-B
chains `marginal.Runtime` update calls onto the existing closure.

**Inputs the Record does NOT carry.** Layer-C needs two pieces
of context not present in `observation.Record`:

1. **`load_i`** — the workload-load proxy. v0.5.8 samples it
   per-tick using existing `internal/idle` helpers: PSI when
   available (`/proc/pressure/cpu` `cpu.some avg10`, per
   RULE-IDLE-04), `/proc/loadavg`'s 1-min field as the fallback
   (RULE-IDLE-05). The current lowercase
   `internal/idle.captureLoadAvg` is exposed as
   `idle.CaptureLoadAvg` in PR-A.
2. **Channel → sensor binding.** A `ControllableChannel` does
   not carry a `TempPath`; the binding lives in user
   `cfg.Controls[X].Curve`. `marginal.Runtime` accepts the live
   `*atomic.Pointer[config.Config]` (same one the controller
   reads) and resolves the bound sensor ID per-tick:
   `sensorID = observation.SensorID(cfg.Curves[ctrl.Curve].SensorPath)`.

Per-shard the runtime maintains one tick of `(T, load, PWM)`
buffering. On each new record:

1. Look up the shard's bound sensor ID via cfg pointer; read
   `T[k] = float(rec.SensorReadings[sensorID]) / 1000` (millideg
   → °C).
2. Sample `load[k]` once via `idle.CaptureLoad(procRoot)` (PSI
   if available, loadavg otherwise).
3. Compute `ΔT = T[k] - T[k-1]` and `ΔPWM = PWM[k] - PWM[k-1]`.
4. If `|ΔPWM| < 1`, skip the sample (no excitation; no Update).
5. If OAT gate fails (any other channel changed PWM in last 5
   ticks), skip the sample (RULE-CMB-OAT-01).
6. Otherwise: `φ = [1, load[k-1]]`, observed `y = ΔT / ΔPWM`.
   Call `Shard.Update(now, φ, y)`.

The RLS update reuses v0.5.7's Sherman-Morrison primitive
unchanged (`gonum mat.SymRankOne` with the R12 bounded-covariance
clamp). v0.5.7's `internal/coupling/identifiability.go`
(`Window`, `ClassifyKappa`, Pearson helper) is extracted to a new
`internal/identifiability/` package so v0.5.8 can re-use the
machinery without copy-paste.

### 2.5 Prior seeding from Layer B — gated by signguard (per R10 §10.7 + R27)

Layer-B's `b_ii` is only used as a Layer-C prior **when its sign
has been independently validated**. Without validation, a parent
shard with a sign-flipped `b_ii` (caused by an inverted-polarity
fan that the v0.5.2 polarity probe missed, or a kernel driver
returning a reversed PWM scale) propagates a wrong-direction prior
to every Layer-C admission for that channel.

**Sign-guard mechanism (`internal/coupling/signguard/`):**

The v0.5.5 opportunistic prober already emits observation records
flagged with `EventFlag_OPPORTUNISTIC_PROBE` carrying a
known-direction PWM step + the resulting ΔT over the 30 s hold
window. signguard subscribes to that record stream and computes:

```
s = sign(ΔT_i_observed) · sign(ΔPWM_i_commanded)
```

The expected outcome under correct polarity for a cooling fan is
`s = -1` (ΔPWM > 0 → ΔT < 0). signguard maintains a 16-sample
rolling sign-vote per channel; readings with
`|ΔT_i_observed| < 2·σ_T = 2·R11_noise_floor` are discarded as
uninformative. Promotion to "polarity-confirmed" requires
**≥ 5 of 7 most-recent probes agree**.

**Layer-C prior admission gate:**

- If `signguard(channel).Confirmed == false` → Layer-C shard
  admits with `θ = [0, 0]` (R10 §10.4 standard warm-start, no
  Layer-B information used).
- If `signguard(channel).Confirmed == true` → Layer-C admits with
  `β_0 ← b_ii / pwm_unit_max`, `β_1 ← 0` per R10 §10.7.

The Layer-B prior, when used, is read from the parent's
`Snapshot` **at shard admission time** (atomic.Pointer load),
not from the live shard. This avoids sign-flip races during
Layer-B re-warmup.

signguard is **continuous, not warmup-only** — a re-cabled fan
that flipped polarity mid-deployment is caught at any point in
daemon lifetime, downgrading affected shards.

Per the research review (informative-prior RLS / hierarchical RLS;
Goodwin & Sin 1984 §3.3; Ljung 1999 §11.4):

- The R12 info-matrix monotonicity guarantees a correctly-signed
  prior is dominated by data within ~5·d_C² = 20 samples
  (≈ 10 s at 2 Hz fast loop).
- During Layer-C warmup, **`Snapshot.Saturated` is forced false**
  (controller defers to Layer A). Belt + braces alongside
  signguard (§3.7 normative).

### 2.6 OAT (one-at-a-time) gate per R28

To eliminate cross-channel aerodynamic interference contamination
of the per-channel β_0 estimate, Layer-C update samples are
admitted only when `Δpwm_j[k] = 0` for all `j ≠ i` over the
previous 5 ticks. R17 defines the full multi-channel
identifiability framework for v0.6.0; the OAT gate is the
v0.5.8-scoped one-line mitigation that suffices for v0.5.9
controller rollout.

When a coupling group is detected by Layer B (per RULE-CPL-IDENT-03),
Layer-C updates from any channel in the group are rejected until
all-but-one members have been static for 5 ticks. This costs
convergence speed in highly-coupled chassis; v0.5.10 doctor
surfaces the rejection rate so operators see when it bites.

---

## 3. Locked design decisions

### 3.1 Model dimension `d_C = 2` (R10 §10.1)

Fixed. Lower than v0.5.7's `d_B = 1 + N_coupled + 1 ≤ 18` because
Layer C sees no cross-channel coupling — that's Layer B's job.

### 3.2 Forgetting factor `λ = 0.99`

Default unchanged from v0.5.7. R12 directional forgetting
auto-tunes from there. The R12 tr(P) cap of 100 is applied
post-update.

### 3.3 Warmup gate

Three-condition gate, identical structure to v0.5.7:

- `n_samples ≥ 5·d_C² = 20` AND
- `tr(P) ≤ 0.5·tr(P_0)` AND
- κ check trivially passes for d=2 (no Window needed)

**Layer-C-specific:** the warmup gate also requires the parent
Layer-B shard to be **out of warmup** AND **κ ≤ 10⁴** before
Layer-C warmup can clear. A Layer-C shard cannot have higher
confidence than its parent Layer-B shard provides per the R-bundle
hierarchy.

**Deferred activation on parent κ-bad:** if the parent Layer-B
shard's `Kind == KindUnidentifiable` (κ > 10⁴), the Layer-C shard
is **not created** at all. Activation re-tries on the next
opportunistic-probe-record event for that channel, with a
`τ_retry = 1 hour` floor between attempts. Per R10 §10.7 +
RULE-CMB-IDENT-01.

**Saturation flag during warmup:** `Snapshot.Saturated` is forced
false until warmup clears. The controller defers to Layer A's
reactive curve in this regime.

### 3.4 Saturation threshold (R11 §0)

```
SaturationDeltaT = 2.0 °C
SaturationNWritesFastLoop = 20  // 2 s at 10 Hz
SaturationNReadsSlowLoop = 3    // 3 min at 1 read/min for drivetemp/bmc
```

These are NOT new constants — they re-export R11 §0's locked
values. Tests assert the constants equal R11's locked values
exactly; a future R11 amendment cascades through here.

### 3.5 Persistence

Spec-16 KV under namespace `smart/shard-C/<channel>-<sig>` per
R15 §104 (Layer-B parent at `smart/shard-B/<channel>`),
msgpack-encoded `Bucket` carrying:

```go
type Bucket struct {
    SchemaVersion         uint8
    HwmonFingerprint      string
    ChannelID             string
    SignatureLabel        string
    Theta                 []float64    // [β_0, β_1]
    PSerialised           []byte       // upper-triangle of P
    InitialP              float64      // P_0 scalar — needed for R12 covariance_term
    Lambda                float64
    NSamples              uint64
    LastSeenUnix          int64
    HitCount              uint64
    EWMAResidual          float64      // E_k per R12 §Q1; consumed by R16 anomaly later
    ObservedSaturationPWM uint8        // last PWM at which Path-B fired (0 = unset)
}
```

`EWMAResidual` and `InitialP` are **net-new vs the v0.5.7 Bucket**
and are required by v0.5.9's R12 confidence formula and v0.5.10
doctor surface. Persisting them now avoids a forced schema bump
when v0.5.9 lands.

Same invalidation rules as v0.5.7:

- `hwmon_fingerprint` mismatch discards.
- Schema version mismatch discards rather than migrates.
- Restored `tr(P)` clamped on load to R12 cap.
- **On load, `WarmingUp` is set true and the three-condition gate
  re-evaluates against in-memory state** before the saturation
  flag is admissible (catches restarts during a ramp).

### 3.6 R1 / R3 disable inheritance

Layer-C respects the same disable signals as v0.5.6:

- R1 Tier-2 BLOCK (containers, VMs): no shards created, runtime
  goroutine never starts.
- R3 hardware-refused (Steam Deck etc.): no shards created.
- `Config.SmartMarginalBenefitDisabled` operator toggle (added
  in PR-B; default false): runtime exits cleanly on next tick
  when toggle flips true. Pre-existing shards persisted but
  frozen.

### 3.7 Wrong-direction Layer-B prior — explicit mitigation

Normative: during Layer-C warmup, the saturation flag is forced
false regardless of `(β_0 + β_1·load) × ΔPWM`'s sign or magnitude.
This protects against the failure mode where:

1. Layer-B parent shard is briefly mis-converged (re-warmup,
   sensor glitch, persistence load with sign flip).
2. Layer-C admits with `β_0 < 0` (wrong direction: prediction
   says ramping warms instead of cools).
3. Saturation gate: `(β_0 + 0) × +1 = β_0 < 0 < 2 °C` → flag
   fires, controller refuses to ramp.

Without the warmup-forces-false guard, the wrong-direction prior
would refuse legitimate ramps until enough data overrides the
prior. The warmup gate keeps the controller on Layer A
(reactive-only) for those ~20 samples.

**Validation in test:** seed a shard with a wrong-direction prior
(`β_0 = -0.5`); assert `Snapshot.Saturated == false` for any
load while `WarmingUp == true`.

---

## 4. Public surface

```go
// internal/marginal/shard.go
type Config struct {
    ChannelID       string
    SignatureLabel  string
    InitialP        float64
    Lambda          float64
    LayerBPriorBii  float64    // 0 means no prior
    PWMUnitMax      int        // for normalising b_ii → β_0
}

func New(cfg Config) (*Shard, error)
func (s *Shard) Update(now time.Time, phi []float64, y float64) error
func (s *Shard) PredictDT(deltaPWM int, load float64) float64
func (s *Shard) MarginalSlope(load float64) float64    // β_0 + β_1·load
func (s *Shard) IsSaturated(deltaPWM int, load float64) bool
func (s *Shard) ObserveOutcome(deltaT float64)         // Path B observed-saturation gate
func (s *Shard) Read() *Snapshot                       // lock-free atomic.Pointer load

type Snapshot struct {
    ChannelID             string
    SignatureLabel        string
    Kind                  SnapshotKind
    Theta                 []float64

    // Identifiability state
    TrP                   float64
    InitialP              float64       // P_0 scalar; v0.5.9 computes tr(P̂) = TrP / InitialP
    NSamples              uint64
    EWMAResidual          float64       // E_k per R12 §Q1 (α=0.95); consumed by v0.5.9 + R16

    // Saturation surface — TWO flags, consumed differently by v0.5.9
    Saturated             bool          // Path A: model-driven prediction (β_0 + β_1·load) × ΔPWM < 2°C
    SaturationAdmitR11    bool          // Path B: R11 §0 dual-gate (range AND slope); current observed state
    ObservedZeroDeltaTRun int           // streak length feeding Path B
    ObservedSaturationPWM uint8         // last PWM at which Path B fired (0 = unset)

    // Operating-point metadata
    MarginalSlope         float64       // β_0 + β_1·load at current load
    WarmingUp             bool

    // R12 confidence input components (raw, undecayed, unsmoothed).
    // v0.5.9 owns aggregation/decay/smoothing/Lipschitz/LPF; v0.5.8 only emits inputs.
    Confidence            ConfidenceComponents
}

type ConfidenceComponents struct {
    SaturationAdmit  bool      // mirrors SaturationAdmitR11; binary 0/1 for R12 product
    ResidualTerm     float64   // clamp(1 − √EWMAResidual / E_floor, 0, 1)
    CovarianceTerm   float64   // clamp(1 − tr(P̂)/dim(θ), 0, 1)
    SampleCountTerm  float64   // clamp(NSamples / N_min_R12, 0, 1) where N_min_R12 = 50
}

// internal/marginal/runtime.go
type Runtime struct { /* ... */ }

func NewRuntime(stateDir, hwmonFingerprint string,
    sigLib *signature.Library,
    cplRuntime *coupling.Runtime,
    logger *slog.Logger,
) *Runtime

func (r *Runtime) Run(ctx context.Context) error
func (r *Runtime) OnObservation(rec *observation.Record)
func (r *Runtime) Shard(channelID, signatureLabel string) *Shard
func (r *Runtime) SnapshotAll() []*Snapshot
func (r *Runtime) ShardCount(channelID string) int   // R13 doctor surface

// internal/identifiability/window.go (extracted from internal/coupling)
type Window struct { /* ... */ }
func NewWindow(d, capacity int) *Window
func ClassifyKappa(kappa float64) SnapshotKind
```

`OnObservation` is non-blocking and dispatches to a per-shard
goroutine inbox via a buffered channel. The controller tick calls
the closure synchronously but does not await the result.
Backpressure logged; inbox-full samples dropped.

---

## 5. Rule bindings

### 5.1 New rule family `RULE-CMB-*`

| Rule | Bound subtest |
|---|---|
| `RULE-CMB-SHARD-01: d=2 fixed; New rejects mismatched config.` | `internal/marginal/shard_test.go:TestShard_DimensionFixedAt2` |
| `RULE-CMB-SHARD-02: RLS update via mat.SymRankOne; never invert P.` | `internal/marginal/shard_test.go:TestRLS_RankOneUpdate_MatchesAnalytical` |
| `RULE-CMB-SHARD-03: tr(P) clamped post-update at R12 cap.` | `internal/marginal/shard_test.go:TestRLS_BoundedCovariance_TrPClamped` |
| `RULE-CMB-SAT-01: Predicted saturation fires at ΔT < 2 °C (R11 §0).` | `internal/marginal/shard_test.go:TestSaturation_Path_A_Predicted` |
| `RULE-CMB-SAT-02: Observed saturation fires after 20 consecutive sub-2°C writes.` | `internal/marginal/shard_test.go:TestSaturation_Path_B_Observed` |
| `RULE-CMB-SAT-03: Saturation forced false during warmup (wrong-prior guard).` | `internal/marginal/shard_test.go:TestSaturation_FalseDuringWarmup` |
| `RULE-CMB-WARMUP-01: Three-condition gate + parent Layer-B clearance.` | `internal/marginal/shard_test.go:TestWarmupGate_RequiresLayerBClearance` |
| `RULE-CMB-PRIOR-01: New shard with LayerBPriorBii seeds β_0; β_1 = 0.` | `internal/marginal/shard_test.go:TestPriorSeeding_FromLayerB` |
| `RULE-CMB-PRIOR-02: Layer-B prior is read at admission time, not live.` | `internal/marginal/runtime_test.go:TestPrior_AtAdmissionNotLive` |
| `RULE-CMB-LIB-01: Per-channel shard map capped at 32 with R7-style LRU.` | `internal/marginal/runtime_test.go:TestRuntime_PerChannelCapAt32` |
| `RULE-CMB-LIB-02: fallback/* labels never create shards.` | `internal/marginal/runtime_test.go:TestRuntime_FilterFallbackLabels` |
| `RULE-CMB-RUNTIME-01: One goroutine per active shard inbox.` | `internal/marginal/runtime_test.go:TestRuntime_OneGoroutinePerShard` |
| `RULE-CMB-RUNTIME-02: OnObservation is non-blocking; dropped samples logged.` | `internal/marginal/runtime_test.go:TestRuntime_OnObservationNonBlocking` |
| `RULE-CMB-RUNTIME-03: Snapshot.Read() lock-free via atomic.Pointer.` | `internal/marginal/shard_test.go:TestSnapshotReadIsLockFree` |
| `RULE-CMB-PERSIST-01: hwmon_fingerprint mismatch discards on Load.` | `internal/marginal/persistence_test.go:TestShard_HwmonFingerprintInvalidation` |
| `RULE-CMB-PERSIST-02: Schema version mismatch discards on Load.` | `internal/marginal/persistence_test.go:TestShard_SchemaVersionMismatchDiscards` |
| `RULE-CMB-PERSIST-03: Restored tr(P) clamped to R12 cap; warmup re-evaluates.` | `internal/marginal/persistence_test.go:TestShard_RestoredReWarms` |
| `RULE-CMB-DISABLE-01: R1/R3/operator-toggle inheritance — no shards created.` | `internal/marginal/runtime_test.go:TestRuntime_DisableInheritance` |
| `RULE-CMB-R11-01: SaturationDeltaT/NWritesFastLoop/NReadsSlowLoop equal R11 §0.` | `internal/marginal/shard_test.go:TestThresholds_MatchR11Locked` |
| `RULE-CMB-IDENT-01: Activation deferred when parent Layer-B κ > 10⁴; τ_retry = 1h.` | `internal/marginal/runtime_test.go:TestRuntime_DeferActivation_OnParentKappaBad` |
| `RULE-CMB-OAT-01: Layer-C samples admitted only when Δpwm_j = 0 for all j ≠ i over 5 ticks.` | `internal/marginal/runtime_test.go:TestRuntime_OAT_RejectsCrossChannelSamples` |
| `RULE-CMB-CONF-01: ConfidenceComponents fields populated per R12 §Q1; Snapshot omits aggregated conf_C.` | `internal/marginal/shard_test.go:TestSnapshot_ExposesR12Inputs` |
| `RULE-CMB-NAMESPACE-01: KV namespace is "smart/shard-C/<channel>-<sig>" per R15 §104.` | `internal/marginal/persistence_test.go:TestPersistence_NamespaceMatchesR15` |

### 5.2 Sign-guard rule family `RULE-SGD-*` (per R27, in PR-A alongside marginal package)

| Rule | Bound subtest |
|---|---|
| `RULE-SGD-VOTE-01: Sign vote requires ≥5 of last 7 opportunistic-probe samples agreeing.` | `internal/coupling/signguard/signguard_test.go:TestSignVote_5Of7Threshold` |
| `RULE-SGD-NOISE-01: Probes with \|ΔT\| < 2·R11_noise are discarded (uninformative).` | `internal/coupling/signguard/signguard_test.go:TestSignVote_DiscardsBelowNoise` |
| `RULE-SGD-CONT-01: signguard runs continuously, not warmup-only — re-cabled fan caught at any point.` | `internal/coupling/signguard/signguard_test.go:TestSignVote_DowngradeOnFlipMidLifetime` |

### 5.3 Wiring rules `RULE-CMB-WIRING-*` (PR-B)

| Rule | Bound subtest |
|---|---|
| `RULE-CMB-WIRING-01: buildMarginalRuntime returns nil when len(channels)==0 or sigLib==nil.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_NilWhenAbsent` |
| `RULE-CMB-WIRING-02: OnObservation chained from controller's existing ObsAppend closure.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_ChainedFromObsHook` |
| `RULE-CMB-WIRING-03: Runtime.Run goroutine started exactly once per daemon lifetime.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_RunOnce` |

---

## 6. Patch sequence

### 6.1 PR-A — library, ~900 LOC + 27 tests

```
internal/identifiability/         (extracted from internal/coupling)
  window.go                       — Window + Pearson + κ helpers
  window_test.go                  — moved from coupling_test.go
internal/coupling/signguard/      (new — per R27, ships in v0.5.8 PR-A)
  signguard.go                    — opportunistic-probe sign-vote aggregator
  signguard_test.go               — RULE-SGD-* bindings
internal/marginal/                (new)
  shard.go                        — RLS + saturation detection (Path A & B)
                                    + ConfidenceComponents emission
  shard_test.go                   — RLS / warmup / prior / saturation / R11-pin /
                                    R12 input components
  persistence.go                  — Bucket (with EWMAResidual + InitialP +
                                    ObservedSaturationPWM) + Save/Load
  persistence_test.go             — including namespace == smart/shard-C/...
  runtime.go                      — per-shard goroutine pool + obs subscriber +
                                    OAT gate + κ-deferred activation +
                                    ShardCount API
  runtime_test.go
.claude/rules/marginal.md         — RULE-CMB-* bindings
.claude/rules/signguard.md        — RULE-SGD-* bindings
specs/spec-v0_5_8-marginal-benefit.md  (this doc)
```

LOC + test count is up vs the original draft (~700 / 19) because
of the signguard sub-package, the Snapshot expansion for v0.5.9,
the OAT gate, and κ-deferred activation. Cost projection adjusted
in §7 below.

The `internal/identifiability/` extraction is an opportunistic
refactor that lands in PR-A; v0.5.7's coupling package re-imports
the same helpers via the new module path. This is reverse-
compatible (no behavioural change) but bumps the rule index.

### 6.2 PR-B — wiring, ~150 LOC

```
cmd/ventd/main.go                       (launch marginal.Runtime
                                          alongside coupling.Runtime;
                                          OnObservation chained from
                                          existing controller obs path)
internal/controller/controller.go       (no changes — Layer-C snapshot
                                          read by v0.5.9, not v0.5.8)
internal/config/config.go               (add SmartMarginalBenefitDisabled)
.claude/rules/marginal.md (extend)      (RULE-CMB-WIRING-*)
```

Mirror v0.5.7 PR-B's "wiring-only, no UI" scope. v0.5.10 doctor
adds the surface.

---

## 7. Estimated cost

- Spec drafting (chat): $0 (this document).
- PR-A CC implementation: **$25–40**. Up from the original
  $15–25 estimate because the deep-research review added:
  signguard sub-package (R27, ~150 LOC + 3 tests), OAT gate
  (R28, ~30 LOC + 1 test), Snapshot expansion for v0.5.9
  R12-input components (~80 LOC + 1 test), KV namespace
  alignment with R15 (~20 LOC + 1 test), κ-deferred activation
  (~50 LOC + 1 test).
- PR-B CC implementation: **$3–5**. Wiring-only.
- Total: **$28–45**, modestly above v0.5.7's spend due to
  signguard + Snapshot expansion. v0.5.9 retrofit cost saved
  in return.

---

## 8. Research review surface — new R-items to register

Two rounds of research review:

**Round 1** (against R9, R10, R11, R7): caught d=6 → d_C=2,
saturation threshold realignment to R11 §0, wrong-prior mitigation
made normative (§3.7).

**Round 2** (against R12, R13, R15, R16, R17, R20): caught the
gaps below. v0.5.8 ships with the in-spec mitigations applied
(signguard, OAT gate, Snapshot expansion, R15-aligned namespace);
the deferred items below are tracked R-items.

| ID | Question | Status |
|---|---|---|
| **R26** | PLLK functional form for v0.5.10. Decision: **defer.** The d=2 RLS scaffold is correctly factored for a back-end swap (~$8-12 retrofit) once v0.5.4 obs-log data quantifies real RMS prediction error. Path B observed-saturation provides correctness fallback regardless of model choice. | post-v0.6.0 |
| **R27** | Wrong-direction Layer-B prior detection — **resolved in v0.5.8.** signguard sub-package consumes opportunistic-probe records as ground-truth sign-vote (5/7 majority). Layer-C admission requires `signguard.Confirmed == true` before consuming the b_ii prior; otherwise admits with θ=0. | shipped v0.5.8 |
| **R28** | R17 multi-channel coupling for Layer C — **mitigated in v0.5.8** via OAT gate (RULE-CMB-OAT-01: admit samples only when other channels have static PWM for 5 ticks). Full joint-identification framework remains v0.6.0 R17 work. | mitigated v0.5.8 / full v0.6.0 |
| **R29** | Per-channel shard cap at 32 is empirical (§2.3, 95th-percentile signature distribution). Validate against R20 fleet telemetry. | v0.7.0+ |
| **R30** | Sigmoid/tanh form (Rotem IEEE Micro 2012; Brooks HPCA 2001). | post-v0.6.0 |
| **R31** | R12 saturation_admit interaction with Path-A predicted saturation: explicitly Path-A and SaturationAdmitR11 are independent flags consumed differently by v0.5.9 (Path-A → "refuse this ramp"; R11 → "drop conf_C this tick"). Confirm v0.5.9 controller pseudocode in spec-smart-mode §7.1 reads both correctly. | v0.5.9 |
| **R32** | Joint-identification across channels (full R17 INTERFERENCE feature) — Söderström & Stoica 1989 Ch. 12 MIMO-RLS rejected for cost; Pearson + R8-HIGH residual detector preferred per R17 §6. | v0.6.0 |

External references added during research review (already in §
References block above):

- ASHRAE Handbook — HVAC Systems & Equipment, Ch. 21 (fan
  affinity laws).
- Bleier, *Fan Handbook* (McGraw-Hill 1997), §3.
- Patel, Sharma, Bash, Beitelmal — *"Smart cooling of data
  centres"*, ASME IPACK 2003 (piecewise-linear marginal cooling).
- Moore, Chase, Ranganathan, Sharma — *"Making scheduling
  cool"*, USENIX ATC 2005.
- Rotem, Naveh, Ananthakrishnan, Weissmann, Rajwan — *"Power-
  management architecture of the Intel microarchitecture
  code-named Sandy Bridge"*, IEEE Micro 2012.
- Goodwin & Sin, *Adaptive Filtering, Prediction and Control*
  (Prentice-Hall 1984), §3.3 (informative-prior RLS).
- Ljung, *System Identification: Theory for the User* (2nd ed.,
  Prentice-Hall 1999), §11.4.

---

**End of spec.**
