# Layer-B thermal coupling rules (v0.5.7)

These invariants govern v0.5.7's per-channel RLS coupling
estimator (`internal/coupling/`). The estimator learns the
linear thermal coupling matrix `b_ij = ΔT_i / Δpwm_j` from the
v0.5.4 observation log; v0.5.8 (Layer C), v0.5.9 (confidence-
gated controller), and v0.5.10 (doctor) all consume the
estimator's snapshot.

The patch spec is `specs/spec-v0_5_7-thermal-coupling.md`. The
design of record is `docs/research/r-bundle/R9-R10-identifiability-and-shards.md`
(346-line research doc).

## RULE-CPL-SHARD-01: Shard dimension d_B = 1 + N_coupled + 1 with N_coupled ≤ 16.

R10 §10.2 caps N_coupled at 16. Above that the analytical PE
conditions degrade and identifiability is hopeless under
workload-driven excitation. New rejects N_coupled > 16 with an
error.

Bound: internal/coupling/shard_test.go:TestShard_NCoupledCappedAt16

## RULE-CPL-SHARD-02: RLS update MUST use mat.SymRankOne rank-1 Sherman-Morrison form; never invert P directly.

The estimator uses gonum/v1/gonum/mat's `SymRankOne` primitive.
Direct inversion of P would be O(d³) and numerically unstable;
Sherman-Morrison is O(d²) and well-behaved when the denominator
guard fires. The test feeds a synthetic linear system and
verifies convergence to ground truth within tolerance.

Bound: internal/coupling/shard_test.go:TestRLS_RankOneUpdate_MatchesAnalytical

## RULE-CPL-SHARD-03: tr(P) MUST be clamped to ≤ 100 via post-update proportional rescale.

R12's bounded-covariance ceiling. Eigenvectors preserved, only
magnitudes attenuated. The test feeds 1000 ticks of constant
input (entirely unidentifiable) and asserts tr(P) never exceeds
the cap.

Bound: internal/coupling/shard_test.go:TestRLS_BoundedCovariance_TrPClamped

## RULE-CPL-IDENT-01: Windowed regressor MUST use W=60 capacity per R10 §10.2.

The κ identifiability detector reads from a 60-row ring buffer.
Per R10 the cap is meant to cover ~1 minute of 1Hz ticks
subsampled at 1/10 — so 60 rows of φ. Window.Count() returns
min(written, 60).

Bound: internal/coupling/identifiability_test.go:TestWindowedRegressor_W60Subsampled

## RULE-CPL-IDENT-02: κ ≤ 100 classifies healthy; 100 < κ ≤ 10000 marginal; κ > 10000 unidentifiable.

R9 §9.4. ClassifyKappa is the single entry point; its switch
statement encodes the three thresholds. Inf and NaN classify
as unidentifiable.

Bound: internal/coupling/identifiability_test.go:TestKappa_ThreeWayClassification

## RULE-CPL-IDENT-03: Co-varying fan group detection MUST trigger when pairwise Pearson ρ > 0.999.

R10 §9.4 + R9 §U1. Signed > 0.999 (NOT |ρ| > 0.999): co-varying
means "two fans always commanded the same PWM" — positive
correlation specifically. Anti-correlated fans are a different
relationship and should not be merged. The test seeds pwm1==pwm2
(merge expected) AND pwm1↔pwm3 anti-correlated (NOT merged).

Bound: internal/coupling/identifiability_test.go:TestPearson_CoVaryingFansDetected

## RULE-CPL-WARMUP-01: Snapshot.WarmingUp = true until n_samples ≥ 5·d² AND tr(P) ≤ 0.5·tr(P_0) AND κ ≤ 10000.

R10 §10.4 three-condition gate. The test feeds enough samples
to clear the n_samples gate and verifies the snapshot remains
in warmup until the gate is fully satisfied.

Bound: internal/coupling/shard_test.go:TestShard_WarmupGate_AllThreeConditionsMustHold

## RULE-CPL-RUNTIME-01: One estimator goroutine per channel; total bounded by len(controllableChannels). NOT one goroutine per shard.

R10 §10.5. Runtime.Run starts exactly one goroutine per shard
in the registered set. The test counts goroutines before/after
Run and asserts the delta matches.

Bound: internal/coupling/runtime_test.go:TestRuntime_OneGoroutinePerChannel

## RULE-CPL-RUNTIME-02: Snapshot.Read() MUST be lock-free via atomic.Pointer.

The controller hot loop calls Read() without acquiring the
shard mutex. The test holds the mutex from the test goroutine
and verifies a parallel Read() returns within 100ms.

Bound: internal/coupling/shard_test.go:TestShard_LabelReadIsLockFree

## RULE-CPL-PERSIST-01: Persisted shards MUST carry hwmon_fingerprint; on probe-reported fingerprint mismatch, all shards MUST be discarded.

R10 §10.6 invalidation. Hardware change to re-warm. The test
saves with one fingerprint, loads with a different one, and
asserts the loaded state is fresh (NSamples == 0).

Bound: internal/coupling/persistence_test.go:TestShard_HwmonFingerprintInvalidation

## RULE-CPL-PERSIST-02: Schema version mismatch on restore MUST discard the persisted shard, not migrate.

R10 §10.6 versioning. Future schema changes are explicit
breaking bumps; cross-version migration is not supported.
The test patches the on-disk SchemaVersion to 99 and verifies
Load returns !loaded.

Bound: internal/coupling/persistence_test.go:TestShard_SchemaVersionMismatchDiscards

## RULE-CPL-PERSIST-03: Restored tr(P) MUST be clamped to R12's cap.

Safety net for cross-version migrations or files written
before the in-memory clamp existed. The test inflates a
shard's tr(P) to 100×TrPCap on disk, then loads it and asserts
the in-memory tr(P) is at or below TrPCap.

Bound: internal/coupling/persistence_test.go:TestShard_RestoredTrPClamped
