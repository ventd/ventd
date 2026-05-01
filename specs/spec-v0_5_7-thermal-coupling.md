# spec-v0_5_7 — Per-channel thermal-coupling map (Layer B)

**Status:** DESIGN. Drafted 2026-05-01.
**Ships as:** v0.5.7 (seventh smart-mode behaviour patch).
**Depends on:**
- v0.5.4 passive observation log (shipped) — supplies the per-tick
  Record stream that the RLS estimator consumes.
- v0.5.6 workload signature library (shipped) — supplies the
  `signature_label` field that v0.5.8 (next) keys per-workload
  shards on. v0.5.7 itself is per-channel, signature-independent.
- v0.5.0.1 spec-16 persistent state (shipped) — supplies the KV
  storage shape used for shard persistence with hwmon-fingerprint
  invalidation.

**Consumed by:**
- v0.5.8 Layer C marginal-benefit RLS — overlay shards parented
  to v0.5.7's Layer-B base shard for prior seeding.
- v0.5.9 confidence-gated controller — `conf_B` rises with
  coupling-coefficient stability; the blended `w_pred` formula
  reads Layer-B's `tr(P)` directly.
- v0.5.10 doctor — surfaces identifiability classification per
  channel (healthy / marginal / co-varying-grouped / unidentifiable).

**References:**
- `specs/spec-smart-mode.md` §6.2 (Layer B description), §8.2
  (`conf_B` consumes Layer-B residual stream), §11 (patch sequence).
- `docs/research/r-bundle/R9-R10-identifiability-and-shards.md` —
  locked design of record. v0.5.7 transcribes R9 §online detector
  and R10 §sharding strategy + memory budgets verbatim.
- `docs/research/r-bundle/ventd-R12-amendment-threshold-recalibration.md`
  — bounded-covariance directional forgetting (Bittanti–Campi 1994).
  The locked R12 tr(P) cap is applied to Layer-B updates.
- `docs/research/r-bundle/R7-workload-signature-hash.md` — Layer-C
  overlay shards (v0.5.8) key on R7's signature labels; v0.5.7 does
  not consume signatures directly.

---

## 1. Why this patch exists

Smart-mode's confidence-gated controller (v0.5.9) needs to know
**which sensors a fan actually cools**. Without that knowledge, the
predictive controller either treats every fan as coupling to every
sensor (false coupling) or treats every fan as independent (missing
the case-airflow effect where channel 4 cools the GPU slightly).
Both errors degrade the blended `w_pred` against reactive control.

v0.5.7 ships the **Layer-B coupling estimator**: per channel, an
RLS shard that learns the linear thermal coupling matrix
`b_ij = ΔT_i / Δpwm_j` from the v0.5.4 observation log. It runs
purely passively — Layer B cannot be probed actively because every
fan-write affects the system; coupling identification happens
during the controller's natural operation as workload diversity
exposes the system to enough excitation.

Per R9 the analytical conclusion is:

> Identifiability of θ_i is upper-bounded by the PE of load_i plus
> disturbance noise.

So v0.5.7 must (a) detect when a shard is well-conditioned vs
ill-conditioned, (b) fail safely when conditioning is poor (hold
prior, surface to doctor), and (c) detect co-varying fan groups
(daisy-chained Y-cables, dual-CPU-fan headers) and collapse them
into composite columns rather than try and fail to disambiguate.

v0.5.7 emits no UI surface and does not yet feed any controller —
v0.5.9's blended controller is the first consumer. v0.5.7's only
visible behaviour at the application layer is a single new doctor
diagnostic line per channel (deferred to v0.5.10's doctor surface;
v0.5.7 ships the underlying shard state, doctor reads it later).

## 1.1 Ground-up principle

**R9 + R10 are the design of record.** v0.5.7 transcribes the locked
analytics into Go and tests; it does not re-litigate model order,
sharding strategy, identifiability thresholds, or eviction policy.

Future revisions to any of those decisions require an amendment to
R9 / R10, not to this patch's spec.

---

## 2. Scope

### 2.1 In scope

**Layer-B shard (`internal/coupling/`):**

- `Shard` type with per-channel state: `θ ∈ ℝ^d` (the parameter
  vector `[a, b_·, c]`), `P ∈ ℝ^{d×d}` (covariance matrix, dense
  `mat.SymDense`), `λ` (forgetting factor), and a windowed regressor
  `M = ΦᵀΦ/W` for the κ identifiability detector.
- Dimension `d_B = 1 + N_coupled + 1`, with **N_coupled capped at 16**
  per R10 §10.2 (above 16 the analytical PE conditions degrade).
- Channels with `N_coupled = 0` (single-zone NUC etc.) are well-posed
  reduced models with `d = 2` per R9 case U4.
- RLS update: rank-1 Sherman-Morrison form using
  `gonum/mat.SymRankOne`, `O(d²)` per tick.
- Bounded-covariance directional forgetting per R12: `λ ∈ [0.95, 0.999]`,
  `tr(P)` clamped to ≤ 100 via post-update rescale.
- Information-matrix monotonicity preserved (directional forgetting
  only forgets in excited directions).

**Identifiability detector (`internal/coupling/identifiability.go`):**

- Windowed regressor `Φ ∈ ℝ^{W×d}` with `W = 60` samples, subsampled
  at 1/10 of tick rate per R10 §10.2 (memory mitigation).
- Per-tick incremental update of `M = ΦᵀΦ/W` with a 60-entry ring
  buffer.
- `κ(M)` computed via `mat.Cond(M, 2)` SVD-based, microseconds per
  call for `d ≤ 26`.
- Three-way classification per R9 §9.4:
  - `κ ≤ 10²` → healthy (full RLS update)
  - `10² < κ ≤ 10⁴` → marginal (directional forgetting only, no
    update in unexcited subspace)
  - `κ > 10⁴` → unidentifiable (hold θ, increment unident_ticks)
- Pairwise Pearson correlation pass when κ > 10² to detect
  co-varying fan groups (|ρ| > 0.999 → declare co-varying, merge
  into composite column for that shard).

**Channel-goroutine model (`internal/coupling/runtime.go`):**

- One estimator goroutine per channel (max 24 on Tier L per R10 §10.3).
- A single ticker goroutine broadcasts `tick(k)` to per-channel
  channels; each estimator does its own math and writes back.
- Per-channel mutex guards activation/eviction sequencing; updates
  on the located shard go through the shard's own mutex (so a future
  doctor goroutine reading shards won't block updates).
- Lock-free read path: shard state exposed via
  `atomic.Pointer[Snapshot]`; `Snapshot.Read()` is lock-free for
  the future v0.5.9 controller hot loop.

**Warmup criterion (per R10 §10.4):**

A shard's output is NOT consumed by any downstream controller until
ALL THREE hold:

1. `n_samples ≥ 5·d²` (Belsley conditioning rule of thumb).
2. `tr(P) ≤ 0.5 · tr(P_0)` (P has shrunk meaningfully).
3. `κ ≤ 10⁴` (R9 detector says identifiable).

During warmup the estimator runs and persists state but `Snapshot`
returns a `WarmingUp = true` flag that v0.5.9's controller reads
to know not to consume the prediction.

**Persistence (spec-16 KV at `$STATE_DIR/smart/shard-B/<channel>.cbor`):**

- CBOR envelope: `{schema_version, hwmon_fingerprint, channel_id,
  theta, P_compressed, lambda, n_samples, last_seen_unix}`.
- `mat.Dense.MarshalBinary` for `P` (float64 little-endian raw)
  gated by `GOARCH` endianness check.
- **hwmon_fingerprint invalidation:** if probe reports a different
  chip/path layout than the saved fingerprint, discard the
  persisted shards. Hardware change → re-warm.
- `tr(P)` clamped to R12's cap on restore as a safety net.
- Schema version bumps on `d_B` definition changes are *breaking*;
  on mismatch, discard rather than migrate.

**Observation log consumption:**

- `internal/coupling/replay.go` reads from `internal/observation`
  using the `Reader.Stream` API (v0.5.4) at daemon start to warm
  shards from the historical record.
- Live ticks consume the same in-memory record stream that v0.5.6's
  controller now emits (post-v0.5.6 PR-B closed the obsWriter gap).

**Tests (synthetic, all CI):**

- Math: `TestRLS_RankOneUpdate_MatchesAnalytical`,
  `TestRLS_DirectionalForgetting_PreservesUnexcitedDims`,
  `TestRLS_BoundedCovariance_TrPClamped`.
- Identifiability: `TestKappa_HealthyCondition`,
  `TestKappa_MarginalCondition`,
  `TestKappa_UnidentifiableCondition`,
  `TestPearson_CoVaryingFansDetected`.
- Structural cases U1–U6 from R9 §9.3:
  - `TestShard_U1_CoVaryingFanGroup`
  - `TestShard_U2_IdleDominatedWorkload_HoldsPrior`
  - `TestShard_U3_ConstantTemperature_OnlyGainIdentifiable`
  - `TestShard_U4_NoCoupledNeighbors_WellPosed`
  - `TestShard_U5_NoLoadProxy_ReducedModel`
  - `TestShard_U6_SaturatedPWM_HoldsBijDuringSaturation`
- Warmup: `TestShard_WarmupGate_AllThreeConditionsMustHold`.
- Persistence: `TestShard_RoundTripCBOR`,
  `TestShard_HwmonFingerprintInvalidation`.
- Concurrency: `TestRuntime_OneGoroutinePerChannel`,
  `TestRuntime_LockFreeSnapshotRead`.
- Cap: `TestShard_NCoupledCappedAt16`.

### 2.2 Out of scope

- **Layer-C overlay shards** (per-(channel, signature) `d=2`
  marginal-benefit). R10 §R10.1 hybrid sharding designs them but
  they ship in v0.5.8.
- **`conf_B` formula consumption.** v0.5.9 wires Layer-B's
  `tr(P) ≤ 0.5·tr(P_0)` into the blended `w_pred`. v0.5.7 emits
  the snapshot; v0.5.9 reads it.
- **PRBS probing mode.** R9 §9.6 G3 lists this as an optional
  probing mode for identifiability characterisation. Out of scope
  per R10 §R10.9 — too invasive for v0.5.7.
- **Joint-channel identification.** R10's open research item.
  Out of scope; defer to post-v0.6.0.
- **Synthetic workload generator.** R9 §9.6 G1–G6 specs the
  generator; the actual implementation is a separate CC task tracked
  alongside `spec-05-prep-trace-harness.md`. v0.5.7 ships unit
  tests that use hand-crafted regressors; the generator is for
  empirical validation post-merge.
- **Doctor surface for identifiability.** v0.5.10 reads Layer-B
  snapshots and renders the per-channel classification. v0.5.7
  emits the snapshot.
- **`smart.coupling` config knobs.** Defaults from R9/R10 are
  hard-coded for v0.5.7 (κ thresholds, W=60, τ_unident=3600).
  Field telemetry from v0.5.7 / v0.5.8 informs whether to expose
  them at v1.0.

---

## 3. Invariant bindings

`.claude/rules/coupling.md` binds 1:1 to subtests in
`internal/coupling/`. Enforced by `tools/rulelint
--check-binding-uniqueness` (the v0.5.6 tooling).

| Rule | Binding |
|---|---|
| `RULE-CPL-SHARD-01` | Shard dimension `d_B = 1 + N_coupled + 1` with N_coupled ≤ 16. |
| `RULE-CPL-SHARD-02` | RLS update MUST use `gonum/mat.SymRankOne` rank-1 Sherman-Morrison form; never invert P directly. |
| `RULE-CPL-SHARD-03` | `tr(P)` MUST be clamped to ≤ 100 via post-update proportional rescale; eigenvectors preserved, only magnitudes attenuated. |
| `RULE-CPL-SHARD-04` | Directional forgetting MUST forget only in directions of observed information (Bittanti–Campi 1994); unexcited subspace retains prior. |
| `RULE-CPL-IDENT-01` | Windowed regressor MUST use W=60 samples with 1/10 subsampling per R10 §10.2. |
| `RULE-CPL-IDENT-02` | κ ≤ 10² classifies healthy; 10² < κ ≤ 10⁴ marginal; κ > 10⁴ unidentifiable per R9 §9.4. |
| `RULE-CPL-IDENT-03` | Co-varying fan group detection MUST trigger when pairwise Pearson \|ρ\| > 0.999 over the window; affected columns merged into composite. |
| `RULE-CPL-WARMUP-01` | Snapshot MUST report `WarmingUp = true` until ALL THREE conditions hold: n_samples ≥ 5·d², tr(P) ≤ 0.5·tr(P_0), κ ≤ 10⁴. |
| `RULE-CPL-RUNTIME-01` | One estimator goroutine per channel; total bounded by `len(controllableChannels)`. NOT one goroutine per shard. |
| `RULE-CPL-RUNTIME-02` | `Snapshot.Read()` MUST be lock-free via `atomic.Pointer[Snapshot]`. |
| `RULE-CPL-PERSIST-01` | Persisted shards MUST carry `hwmon_fingerprint`; on probe-reported fingerprint mismatch, all shards MUST be discarded. |
| `RULE-CPL-PERSIST-02` | Schema version mismatch on restore MUST discard the persisted shard, not migrate. |
| `RULE-CPL-PERSIST-03` | Restored `tr(P)` MUST be clamped to R12's cap; restored `λ` accepted as-is. |

---

## 4. Subtest mapping

| Rule | Subtest |
|---|---|
| RULE-CPL-SHARD-01 | `internal/coupling/shard_test.go:TestShard_NCoupledCappedAt16` |
| RULE-CPL-SHARD-02 | `internal/coupling/shard_test.go:TestRLS_RankOneUpdate_MatchesAnalytical` |
| RULE-CPL-SHARD-03 | `internal/coupling/shard_test.go:TestRLS_BoundedCovariance_TrPClamped` |
| RULE-CPL-SHARD-04 | `internal/coupling/shard_test.go:TestRLS_DirectionalForgetting_PreservesUnexcitedDims` |
| RULE-CPL-IDENT-01 | `internal/coupling/identifiability_test.go:TestWindowedRegressor_W60Subsampled` |
| RULE-CPL-IDENT-02 | `internal/coupling/identifiability_test.go:TestKappa_ThreeWayClassification` |
| RULE-CPL-IDENT-03 | `internal/coupling/identifiability_test.go:TestPearson_CoVaryingFansDetected` |
| RULE-CPL-WARMUP-01 | `internal/coupling/shard_test.go:TestShard_WarmupGate_AllThreeConditionsMustHold` |
| RULE-CPL-RUNTIME-01 | `internal/coupling/runtime_test.go:TestRuntime_OneGoroutinePerChannel` |
| RULE-CPL-RUNTIME-02 | `internal/coupling/runtime_test.go:TestRuntime_LockFreeSnapshotRead` |
| RULE-CPL-PERSIST-01 | `internal/coupling/persistence_test.go:TestShard_HwmonFingerprintInvalidation` |
| RULE-CPL-PERSIST-02 | `internal/coupling/persistence_test.go:TestShard_SchemaVersionMismatchDiscards` |
| RULE-CPL-PERSIST-03 | `internal/coupling/persistence_test.go:TestShard_RestoredTrPClamped` |

Plus the structural-case-from-R9 tests:

| Case | Subtest |
|---|---|
| U1 co-varying fan group | `TestShard_U1_CoVaryingFanGroup` |
| U2 idle-dominated | `TestShard_U2_IdleDominatedWorkload_HoldsPrior` |
| U3 constant temperature | `TestShard_U3_ConstantTemperature_OnlyGainIdentifiable` |
| U4 no coupled neighbors | `TestShard_U4_NoCoupledNeighbors_WellPosed` |
| U5 no load proxy | `TestShard_U5_NoLoadProxy_ReducedModel` |
| U6 saturated PWM | `TestShard_U6_SaturatedPWM_HoldsBijDuringSaturation` |

---

## 5. Success criteria

### 5.1 Synthetic CI tests

All ~22 named subtests pass on every PR. `tools/rulelint
--suggest --check-binding-uniqueness` reports zero unbound rules,
zero duplicate bindings.

### 5.2 Behavioural HIL

**Primary fleet member: Proxmox host (192.168.7.10, 5800X + RTX 3060):**

- 24 h soak with mixed homelab workload (idle baseline + kernel
  build + gaming session + zfs scrub).
- Layer-B shards for every controllable channel reach warmup
  completion within the 24 h window.
- Per-channel snapshot reports `Healthy` for ≥1 channel by hour 6
  (the kernel-build window provides PE on CPU-fan channel).
- No `tr(P)` divergence: all shards report `tr(P) ≤ 100` throughout.
- Memory: total heap delta from baseline < 200 KiB per shard
  (matches R10 §10.2 budget).

**Negative fleet members:**

- **MiniPC (192.168.7.222), when online:** monitor-only. Library
  not instantiated; zero shard memory allocated.
- **Steam Deck:** monitor-only per R3. Library runs and persists
  but emits no controller writes (R3 inheritance).

### 5.3 Time-bound metric

Per R9 §9.7 E1: convergence to within 10% relative error on
identifiable parameters within 6 hours of homelab-typical workload.
Tested in CI via the synthetic regressor harness; HIL-verified on
Proxmox host post-merge.

---

## 6. Privacy contract

Inherited from R7 §6 / spec-v0_5_6. Layer-B shards contain no
plaintext comm names, no signature labels, no user-identifying
data — only fan/sensor IDs (opaque integers) and learned
coefficients. The `hwmon_fingerprint` is a SHA-256 of the chip
discovery output; bytes-in, hash-out, no per-install secret needed.

Diag bundles include shard state (post-warmup only) on opt-in
`--include-coupling`. The hash-fingerprint and learned coefficients
are not sensitive in the privacy sense (they describe hardware,
not user behaviour) — but their inclusion is opt-in for symmetry
with the v0.5.6 observation-log inclusion flag.

---

## 7. Failure modes enumerated

1. **Shard `θ` diverges due to numerical wind-up.** Bounded by
   R12's `tr(P) ≤ 100` cap; the post-update rescale prevents
   divergence even under non-persistent excitation. Test
   `TestRLS_BoundedCovariance_TrPClamped` covers this.

2. **Co-varying fan group never disambiguates.** Per R9 §U1 only
   the *sum* `b_{i,j₁} + b_{i,j₂}` is identifiable; the sum is
   reported as the composite coefficient with a "grouped" flag.
   Layer C consumes this as `b_ij = sum / 2` per fan with confidence
   downgrade. v0.5.10 doctor surfaces the grouping.

3. **Idle-dominated workload (U2) prevents `c_i` identification.**
   Detector classifies as marginal or unidentifiable; `c_i` held
   at prior. Correct behaviour: a desktop that never leaves idle
   genuinely has no information to infer load coupling.

4. **Hardware change between boots not caught by hwmon fingerprint.**
   E.g., user re-routes a fan to a different header. Saved shard's
   `b_ij` become stale relative to the new wiring. Self-corrects:
   the κ detector flags the resulting mis-attribution within
   ~6 h of mixed workload as the new fan's coupling pattern
   diverges from the saved θ. Doctor surfaces it as a "marginal"
   classification on the affected channel.

5. **Persistence file corruption.** CBOR unmarshal returns error;
   shard discarded silently and re-warmed. Single-shard loss
   doesn't affect other channels.

6. **Spec-16 KV write failure during periodic save.** Treated
   advisory (warn, continue). On next save the in-memory state
   is re-persisted.

7. **`tr(P) > 100` after restore from disk.** Caused by a stale
   persisted file from a pre-clamp version. Post-restore rescale
   handles transparently.

8. **Goroutine leaks.** All channel goroutines are tracked by the
   daemon's `WaitGroup`; on context cancel they exit. No
   per-shard goroutines.

9. **Saturated PWM (U6).** During saturation `b_ij` for that fan
   is unidentifiable; estimator holds the pre-saturation value
   and resumes updating when PWM exits saturation.

10. **Sensor frozen / aliased (U7).** R6 hwmon validator catches
    this at probe time; Layer B inherits the validation outcome
    (the affected sensor is not added to any shard's regressor).

---

## 8. PR sequencing

### 8.1 PR-A (logic, hermetically testable)

```
internal/coupling/shard.go              (~400 LOC)
internal/coupling/shard_test.go         (~600 LOC)
internal/coupling/identifiability.go    (~200 LOC)
internal/coupling/identifiability_test.go (~300 LOC)
internal/coupling/persistence.go        (~150 LOC)
internal/coupling/persistence_test.go   (~200 LOC)
internal/coupling/runtime.go            (~250 LOC)
internal/coupling/runtime_test.go       (~300 LOC)
internal/coupling/replay.go             (~150 LOC)
internal/coupling/replay_test.go        (~200 LOC)
internal/coupling/snapshot.go           (~80 LOC)
.claude/rules/coupling.md               (13 RULE-CPL-* invariants)
specs/spec-v0_5_7-thermal-coupling.md   (this file)
go.mod, go.sum                          (add gonum/v1/gonum/mat)
```

Total LOC estimate: ~2,830 (per R10 budget — larger than v0.5.6
because of the gonum integration + structural-case test matrix).

### 8.2 PR-B (wiring, HIL only)

```
cmd/ventd/main.go                       (launch coupling.Runtime
                                          alongside controllers)
internal/controller/controller.go       (no changes — the snapshot
                                          is read by v0.5.9, not by
                                          the controller in v0.5.7)
.claude/rules/coupling.md (extend)      (RULE-CPL-WIRING-* if any)
```

Total LOC estimate: ~150. Smaller than v0.5.6 PR-B because v0.5.7
has no UI surface and the controller hot loop doesn't yet consume
Layer-B's snapshot (that's v0.5.9's work).

---

## 9. Estimated cost

- Spec drafting (chat): $0 (this document, on Max plan).
- PR-A CC implementation (Sonnet): **$15–25** per R10's "longest
  patch in the v0.5.x sequence" budget. The gonum/mat integration
  + structural-case test matrix are the dominant cost.
- PR-B CC implementation (Sonnet): **$3–5**. Wiring-only.
- Total: **$18–30**, just above `spec-smart-mode.md` §13's $15-25
  projection because of the gonum dependency and the U1–U6 test
  matrix.

---

## 10. References

- `specs/spec-smart-mode.md` §6.2 (Layer B), §8.2 (`conf_B`
  consumption), §11 (patch sequence).
- `docs/research/r-bundle/R9-R10-identifiability-and-shards.md` —
  design of record. Every section verbatim.
- `docs/research/r-bundle/ventd-R12-amendment-threshold-recalibration.md`
  — bounded-covariance R12 amendment.
- `docs/research/r-bundle/R7-workload-signature-hash.md` (Layer-C
  overlay shards in v0.5.8 will key on R7 signatures).
- Ljung, *System Identification* 2nd ed. §13.4, §14.4 (PE-d
  conditions).
- Bittanti, Bolzern, Campi 1990 (directional forgetting).
- Bittanti, Campi 1994 (bounded-covariance RLS — R12 lock).
- gonum/v1/gonum/mat (pure-Go linear algebra; CGO_ENABLED=0).
