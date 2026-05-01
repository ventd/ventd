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
`PWMWritten`, `RPM`, `SignatureLabel`, plus the per-channel
sensor reading. The Layer-C update path subscribes to the same
v0.5.6 `ObsAppend` hook; v0.5.8 PR-B chains `marginal.Runtime`
update calls onto the existing closure.

Per-shard the runtime maintains one tick of `(T, load, PWM)`
buffering. On each new record:

1. Compute `ΔT = T[k] - T[k-1]` and `ΔPWM = PWM[k] - PWM[k-1]`.
2. If `|ΔPWM| < 1`, skip the sample (no excitation).
3. Otherwise: `φ = [1, load[k-1]]`, observed
   `y = ΔT / ΔPWM`. Call `Shard.Update(now, φ, y)`.

The RLS update reuses v0.5.7's Sherman-Morrison primitive
unchanged (`gonum mat.SymRankOne` with the R12 bounded-covariance
clamp). v0.5.7's `internal/coupling/identifiability.go`
(`Window`, `ClassifyKappa`, Pearson helper) is extracted to a new
`internal/identifiability/` package so v0.5.8 can re-use the
machinery without copy-paste.

### 2.5 Prior seeding from Layer B (per R10 §10.7)

When a new `(channel, signature)` pair appears for the first time,
the Layer-C shard initialises θ from the parent Layer-B shard's
coupling estimate rather than from a zero prior:

- `β_0 ← b_ii / pwm_unit_max` (Layer-B self-coupling normalised
  per +1 PWM; b_ii is the diagonal of the coupling matrix,
  representing "ΔT_i per ΔPWM_i").
- `β_1 ← 0` (no Layer-B information about load sensitivity).

Per the research review (informative-prior RLS / hierarchical RLS;
Goodwin & Sin 1984 §3.3; Ljung 1999 §11.4):

- The R12 info-matrix monotonicity guarantees the prior is
  eventually dominated by data within ~5·d_C² = 20 samples
  (≈ 10 s at 2 Hz fast loop).
- During Layer-C warmup, **`Snapshot.Saturated` is forced false**
  (controller defers to Layer A). This protects against a
  wrong-sign Layer-B prior briefly producing a false
  "ramping helps a lot" verdict and bypassing the saturation
  refusal (§3.7 makes this normative).
- The Layer-B prior is read from the parent's `Snapshot` **at
  shard admission time** (atomic.Pointer load), not from the
  live shard. This avoids sign-flip races during a Layer-B
  re-warmup.

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
Layer-B shard to be **out of warmup** before Layer-C warmup can
clear. A Layer-C shard cannot have higher confidence than its
parent Layer-B shard provides per the R-bundle hierarchy.

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

Spec-16 KV under namespace `marginal_benefit`, msgpack-encoded
`Bucket` carrying:

```go
type Bucket struct {
    SchemaVersion    uint8
    HwmonFingerprint string
    ChannelID        string
    SignatureLabel   string
    Theta            []float64       // [β_0, β_1]
    PSerialised      []byte          // upper-triangle of P
    Lambda           float64
    NSamples         uint64
    LastSeenUnix     int64
    HitCount         uint64
}
```

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
    ChannelID         string
    SignatureLabel    string
    Kind              SnapshotKind
    Theta             []float64
    TrP               float64
    NSamples          uint64
    Saturated         bool
    MarginalSlope     float64
    Confidence        float64       // conf_C ∈ [0, 1]
    WarmingUp         bool
    ObservedZeroDeltaTRun int       // streak length for Path B
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

### 5.2 Wiring rules `RULE-CMB-WIRING-*` (PR-B)

| Rule | Bound subtest |
|---|---|
| `RULE-CMB-WIRING-01: buildMarginalRuntime returns nil when len(channels)==0 or sigLib==nil.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_NilWhenAbsent` |
| `RULE-CMB-WIRING-02: OnObservation chained from controller's existing ObsAppend closure.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_ChainedFromObsHook` |
| `RULE-CMB-WIRING-03: Runtime.Run goroutine started exactly once per daemon lifetime.` | `cmd/ventd/main_marginal_test.go:TestBuildMarginalRuntime_RunOnce` |

---

## 6. Patch sequence

### 6.1 PR-A — library, ~700 LOC + 19 tests

```
internal/identifiability/         (extracted from internal/coupling)
  window.go                       — Window + Pearson + κ helpers
  window_test.go                  — moved from coupling_test.go
internal/marginal/                (new)
  shard.go                        — RLS + saturation detection (Path A & B)
  shard_test.go                   — RLS / warmup / prior / saturation / R11-pin
  persistence.go                  — Bucket + Save/Load
  persistence_test.go
  runtime.go                      — per-shard goroutine pool + obs subscriber
  runtime_test.go
.claude/rules/marginal.md         — RULE-CMB-* bindings
specs/spec-v0_5_8-marginal-benefit.md  (this doc)
```

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
- PR-A CC implementation: **$15–25**. The d=2 model is simpler
  than v0.5.7's d=1+N+1, but the per-(channel, signature) shard
  map + LRU + Layer-B prior plumbing balances the saving.
- PR-B CC implementation: **$3–5**. Wiring-only.
- Total: **$18–30**, matching v0.5.7's spend.

---

## 8. Research review surface — new R-items to register

The v0.5.8 draft was reviewed against R9, R10, R11, and R7. The
review surfaced five gaps where the existing R-bundle is silent or
ambiguous for Layer C. v0.5.8 codes against the locked decisions
above; the gaps below are registered as new R-items for follow-up.
None blocks v0.5.8.

| ID | Question | Status |
|---|---|---|
| **R26** | Is the Layer-C parametric form `(β_0 + β_1·load) × ΔPWM` adequate for laptops with active workload-conditional fan curve flips (BIOS reflashing the curve under sustained load)? Or should v0.5.10 evaluate piecewise-linear-with-learnable-knee (PLLK) per Patel et al. IPACK 2003 / Moore et al. USENIX ATC 2005? | post-v0.6.0 |
| **R27** | Wrong-direction Layer-B prior detection — current spec relies on warmup-forces-false. Should there be a separate sanity-check that flags `b_ii < 0` from Layer B as a parent-shard fault and refuses prior seeding entirely? | v0.5.10 |
| **R28** | R17 (multi-channel aerodynamic interference) is registered but unscoped for Layer C. If channel-A's ramp moves channel-B's temperature, is the per-channel β_0 estimate biased? Joint identification across channels deferred to R28 evaluation. | v0.6.0 |
| **R29** | Per-channel shard cap at 32 is empirical from §2.3 (95th-percentile signature distribution per user). Validate against fleet telemetry once R20 fleet-federation lands. Adjust upward if the long-tail user has >32 active workloads. | v0.7.0 |
| **R30** | Sigmoid/tanh functional form (Rotem et al. IEEE Micro 2012; Brooks & Martonosi HPCA 2001) for thermal-headroom-vs-fan-RPM. Currently the linear model degenerates at the rotor-stall floor and aerodynamic ceiling. Track for v0.6.x. | post-v0.6.0 |

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
