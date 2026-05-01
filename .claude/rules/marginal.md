# Layer-C marginal-benefit rules (v0.5.8)

These invariants govern v0.5.8's per-(channel, signature) RLS
marginal-benefit estimator (`internal/marginal/`). Layer C learns
the function `ΔT_per_+1_PWM = β_0 + β_1·load` per workload
signature; v0.5.9's confidence-gated controller uses it to refuse
ramps that pay full acoustic cost for zero thermal benefit.

The patch spec is `specs/spec-v0_5_8-marginal-benefit.md`. The
design of record is `docs/research/r-bundle/R9-R10-identifiability-and-shards.md`
(R10 §10.1 locks `d_C = 2`; R10 §10.7 locks the prior-seeding
machinery from Layer B). R11 §0 locks the saturation thresholds.

## RULE-CMB-SHARD-01: d_C = 2 fixed; New rejects mismatched config.

R10 §10.1 locks the parametric form: φ = [1, load], θ = [β_0, β_1].
Lower than v0.5.7's d_B because Layer C sees no cross-channel
coupling — that's Layer B's job. Update with len(phi) != DimC
returns an error.

Bound: internal/marginal/shard_test.go:TestShard_DimensionFixedAt2

## RULE-CMB-SHARD-02: RLS update uses gonum mat.SymRankOne (Sherman-Morrison rank-1); never invert P.

The estimator reuses v0.5.7's primitive unchanged. The synthetic
linear-system test feeds 500 random samples and verifies the
estimated θ converges to ground truth within ±0.005.

Bound: internal/marginal/shard_test.go:TestRLS_RankOneUpdate_MatchesAnalytical

## RULE-CMB-SHARD-03: tr(P) clamped post-update at R12's 100 ceiling.

R12's bounded-covariance clamp, applied identically to v0.5.7's
Layer B. The test feeds 1000 ticks of constant input
(unidentifiable) and asserts tr(P) never exceeds the cap.

Bound: internal/marginal/shard_test.go:TestRLS_BoundedCovariance_TrPClamped

## RULE-CMB-SAT-01: Path-A (predicted) saturation fires when (β_0 + β_1·load) × ΔPWM < 2 °C.

R11 §0 locks the threshold. Path A is the model-driven prediction
the v0.5.9 controller consumes for "should I ramp?" decisions.

Bound: internal/marginal/shard_test.go:TestSaturation_Path_A_Predicted

## RULE-CMB-SAT-02: Path-B (observed) saturation fires after 20 consecutive sub-2 °C writes.

R11 §0's locked observed-saturation rule. Independent of model
state — fires even when Path A says not-saturated. The streak
breaks on any |ΔT| ≥ 2 °C.

Bound: internal/marginal/shard_test.go:TestSaturation_Path_B_Observed

## RULE-CMB-SAT-03: Saturation flag is forced false during warmup.

Wrong-direction Layer-B prior guard (spec §3.7). During warmup the
controller defers to Layer A's reactive curve, regardless of the
predicted slope's sign. Belt + braces alongside signguard
(RULE-SGD-*).

Bound: internal/marginal/shard_test.go:TestSaturation_FalseDuringWarmup

## RULE-CMB-WARMUP-01: Three-condition gate + parent Layer-B clearance.

R10 §10.4 gate: n_samples ≥ 5·d² = 20 AND tr(P) ≤ 0.5·tr(P_0)
AND parent Layer-B is out of warmup. The κ check at d=2 is
trivially well-conditioned, so no per-shard window is maintained.

Bound: internal/marginal/shard_test.go:TestWarmupGate_RequiresLayerBClearance

## RULE-CMB-PRIOR-01: New shard with confirmed Layer-B prior seeds β_0 from b_ii / pwm_unit_max; β_1 = 0.

R10 §10.7 informative-prior RLS. Without confirmation
(LayerBConfirmed=false), prior is NOT used — admits at θ = [0, 0].

Bound: internal/marginal/shard_test.go:TestPriorSeeding_FromLayerB

## RULE-CMB-PRIOR-02: Layer-B prior is read at admission time (atomic.Pointer load), not from the live shard.

Race-avoidance per spec §2.5. Subsequent changes to the parent's
b_ii do NOT reflow into the Layer-C shard's θ_0.

Bound: internal/marginal/runtime_test.go:TestPrior_AtAdmissionNotLive

## RULE-CMB-LIB-01: Per-channel shard map capped at MaxShardsPerChannel = 32 with weighted-LRU eviction.

Spec §2.3. R7's eviction score `HitCount × exp(-(age/τ))` with
τ=14 days. v0.5.8 uses NSamples as a proxy for HitCount; R29
refines once R20 fleet telemetry validates.

Bound: internal/marginal/runtime_test.go:TestRuntime_PerChannelCapAt32

## RULE-CMB-LIB-02: fallback/disabled and fallback/warming labels never create shards.

R7's reserved labels emitted while the signature library is
shutdown / warming. Layer-C ignores observations carrying these
labels (no shard creation, no Update).

Bound: internal/marginal/runtime_test.go:TestRuntime_FilterFallbackLabels

## RULE-CMB-RUNTIME-01: One goroutine pool started on Run; bounded number of worker goroutines.

The spec describes a per-shard goroutine model conceptually; v0.5.8
implements a single periodic-save goroutine because Update is
pure-CPU < 50 µs at d=2 — synchronous direct-update is sufficient
and avoids per-shard goroutine churn. Test asserts that Run starts
no more than 5 new goroutines regardless of shard count.

Bound: internal/marginal/runtime_test.go:TestRuntime_OneGoroutinePerShard

## RULE-CMB-RUNTIME-02: OnObservation is non-blocking; returns within 1 ms.

Backpressure protection for the controller hot-path. Updates are
synchronous but bounded by Update's pure-CPU cost. Test asserts
the call returns in < 1 ms.

Bound: internal/marginal/runtime_test.go:TestRuntime_OnObservationNonBlocking

## RULE-CMB-RUNTIME-03: Snapshot.Read() is lock-free via atomic.Pointer.

The v0.5.9 controller hot loop calls Read() without acquiring
the shard mutex. Test holds the mutex from a goroutine and
verifies Read() returns within 100 ms.

Bound: internal/marginal/shard_test.go:TestSnapshotReadIsLockFree

## RULE-CMB-PERSIST-01: hwmon_fingerprint mismatch on Load discards.

R10 §10.6 invalidation. Hardware change (motherboard swap, fan
re-cabling that changes hwmon enumeration) re-warms.

Bound: internal/marginal/persistence_test.go:TestShard_HwmonFingerprintInvalidation

## RULE-CMB-PERSIST-02: Schema version mismatch on Load discards (no migration).

Future schema bumps are explicit breaking versions; cross-version
migration is not supported. R10 §10.6 versioning.

Bound: internal/marginal/persistence_test.go:TestShard_SchemaVersionMismatchDiscards

## RULE-CMB-PERSIST-03: Restored tr(P) clamped to R12 cap; loaded shard re-enters warmup.

Safety net for cross-version migrations or corruption. The
warmup gate re-evaluates against in-memory state on load
(spec §3.5).

Bound: internal/marginal/persistence_test.go:TestShard_RestoredReWarms

## RULE-CMB-DISABLE-01: R1/R3 disable inheritance + nil-parents/signguard graceful degrade.

Layer-C respects R1 Tier-2 BLOCK (containers/VMs), R3
hardware-refused (Steam Deck), and the operator toggle. With nil
parents/signguard the runtime degrades cleanly: no Layer-B prior
used, no admission failures, no panics.

Bound: internal/marginal/runtime_test.go:TestRuntime_DisableInheritance

## RULE-CMB-R11-01: SaturationDeltaT / SaturationNWritesFastLoop / SaturationNReadsSlowLoop equal R11 §0 locked values.

`SaturationDeltaT = 2.0`, `SaturationNWritesFastLoop = 20`,
`SaturationNReadsSlowLoop = 3` — re-export, NOT new constants. Test
pins the values so a future R11 amendment cascades through here.

Bound: internal/marginal/shard_test.go:TestThresholds_MatchR11Locked

## RULE-CMB-IDENT-01: Activation deferred when parent Layer-B κ > 10⁴; τ_retry = 1h.

R10 §10.7. A Layer-C shard is NOT created when the parent
Layer-B shard is unidentifiable. Re-attempt floor of 1 hour
between failed admissions.

Bound: internal/marginal/runtime_test.go:TestRuntime_DeferActivation_OnParentKappaBad

## RULE-CMB-OAT-01: Layer-C update samples admitted only when Δpwm_j = 0 for all j ≠ i over the previous 5 ticks.

Spec §2.6. Mitigates R28 multi-channel aerodynamic interference
contamination of per-channel β_0 estimates. Costs convergence
speed in highly-coupled chassis; full R17 INTERFERENCE work
remains v0.6.0.

Bound: internal/marginal/runtime_test.go:TestRuntime_OAT_RejectsCrossChannelSamples

## RULE-CMB-CONF-01: Snapshot.Confidence is a ConfidenceComponents struct exposing R12 §Q1 input terms; the aggregated conf_C float is NOT computed in v0.5.8.

v0.5.9 owns aggregation, decay (drift_flag · 0.5^(t/T_half)),
Lipschitz bound (L_max = 0.05/s), LPF (τ_w = 30 s), and per-
channel collapse via active-signature rule (R12 §Q6).

Bound: internal/marginal/shard_test.go:TestSnapshot_ExposesR12Inputs

## RULE-CMB-NAMESPACE-01: KV namespace is "smart/shard-C/<channel>-<sig>.cbor" per R15 §104.

v0.5.7 Layer B uses `smart/shard-B/`; v0.5.8 mirrors at
`smart/shard-C/`. Channel paths are flattened (non-alphanumeric
chars → '-'); signature labels are SipHash hex digests
(filename-safe by construction).

Bound: internal/marginal/persistence_test.go:TestPersistence_NamespaceMatchesR15
