# Layer-A confidence (`conf_A`) rules — v0.5.9 PR-A sub-component

These invariants govern the per-channel `conf_A` estimator in
`internal/confidence/layer_a/`. v0.5.9's confidence-gated controller
collapses `conf_A`, `conf_B` (Layer-B coupling), and `conf_C`
(Layer-C marginal) into a single per-channel `w_pred` weight via the
aggregator described in `.claude/rules/confidence-aggregator.md`
(landing in a sibling sub-PR).

The patch spec is `specs/spec-v0_5_9-confidence-controller.md` §2.4
+ §3.5 + §5.1. The R-bundle source is R8 (fallback tier ceilings),
R6 (tach noise floor), and R12 §Q1 (four-term confidence product).

Each rule below is bound 1:1 to a subtest in `internal/confidence/
layer_a/`. `tools/rulelint` blocks the merge if a rule lacks a
corresponding subtest.

## RULE-CONFA-FORMULA-01: ConfA = R8_ceiling × √coverage × (1−norm_residual) × recency.

The four-term product from R12 §Q1 / spec-v0_5_9 §2.4. Each term is
in [0, 1]; the product is clamped to [0, 1] on output. With
tier-1 ceiling 0.85, full coverage 1.0, zero residual, zero age,
the formula yields exactly 0.85.

Bound: internal/confidence/layer_a/estimator_test.go:TestConfA_Formula

## RULE-CONFA-COVERAGE-01: bin width = 16 raw PWM units; coverage counts bins with ≥3 obs.

Histogram of per-channel PWM observations across 16 bins
(`NumBins`), each `BinWidth=16` raw units (0/16/.../240). PWM=255
clamps to bin 15. Coverage is the fraction of bins with at least
`MinObsPerBinForCoverage=3` observations — a single Observe call
on a fresh channel yields coverage 0; three calls into the same
bin and seven empty bins yields coverage 1/16.

Bound: internal/confidence/layer_a/estimator_test.go:TestCoverage_BinWidth

## RULE-CONFA-RECENCY-01: recency = exp(-age_seconds/604800); resets only on admissible update.

Time constant `RecencyTau` is exactly 7 days (604800 s). Any
`Observe` call resets the channel's `lastUpdate` clock — that is
the only "admissible update" — so a steady stream of observations
holds recency at ~1.0. Without observations, recency decays
exponentially.

Bound: internal/confidence/layer_a/estimator_test.go:TestRecency_DecayHalfLife7d

## RULE-CONFA-TIER-01: R8 tier ceilings {1.00, 0.85, 0.70, 0.55, 0.45, 0.30, 0.30, 0.00}.

The eight R8 fallback tiers map to ceiling values that clamp
`conf_A` regardless of coverage / residual / recency. Tier-0 is
real RPM tach (1.00 ceiling); tier-7 is open-loop pinned (0.00 —
predictive controller is refused entirely on this channel). Out-
of-range tier values fall to tier-7 to prevent a corrupted
persisted byte from escaping the locked table.

Bound: internal/confidence/layer_a/estimator_test.go:TestTierCeilings_Locked

## RULE-CONFA-PERSIST-01: KV namespace smart/conf-A/<channel>; persists inputs not output.

The on-disk Bucket lives under `<stateDir>/smart/conf-A/<flattened
channelID>.cbor` per R15 §104. The Bucket carries the four-term
inputs (Tier, BinCounts, BinResidualSumSq, NoiseFloor,
LastUpdateUnix, TierPinnedUntilUnix, SeenFirstContact) — NOT the
computed `ConfA` scalar. The product is recomputed on Load against
the current wall-clock so recency is never stale.

Bound: internal/confidence/layer_a/persistence_test.go:TestPersistence_Namespace

## RULE-CONFA-PERSIST-02: hwmon_fingerprint mismatch on Load discards.

A persisted Bucket whose `HwmonFingerprint` differs from the
running fingerprint causes Load to discard cleanly: the channel
re-warms from zero. Mirrors the v0.5.7/v0.5.8 invalidation pattern
on hardware change (motherboard swap, fan re-cabling that changes
hwmon enumeration).

Bound: internal/confidence/layer_a/persistence_test.go:TestPersistence_FingerprintInvalidation

## RULE-CONFA-PERSIST-03: Schema version mismatch on Load discards.

A persisted Bucket whose `SchemaVersion != PersistedSchemaVersion`
causes Load to discard. Future schema changes are explicit
breaking bumps; cross-version migration is not supported. A bumped
version on disk with no matching reader code MUST re-warm rather
than risk silent corruption.

Bound: internal/confidence/layer_a/persistence_test.go:TestPersistence_SchemaMismatch

## RULE-CONFA-SNAPSHOT-01: Read() lock-free via atomic.Pointer.

The published `Snapshot` for each channel is stored in an
`atomic.Pointer[Snapshot]` on the channel state. Hot-path readers
(controller tick) load it without taking `Estimator.mu`. Save and
Load take the mutex; tests pin the contract that an atomic Snapshot
load completes in <100 ms even while another goroutine holds the
estimator mutex.

Bound: internal/confidence/layer_a/estimator_test.go:TestSnapshotReadIsLockFree

## RULE-CONFA-FIRSTCONTACT-01: SeenFirstContact persisted; re-armed only on WipeNamespaces.

The first-contact invariant — "predictive controller never reduces
cooling on the first w_pred>0 tick" from spec-v0_5_9 §2.7 — is
gated by `SeenFirstContact`. The flag is persisted in the on-disk
Bucket so a daemon restart preserves it; the only path that
re-arms (resets to false) is removing the on-disk Bucket file
(which is what `probe.WipeNamespaces` does on full reset). A
freshly-Admitted channel without a persisted Bucket starts with
`SeenFirstContact = false`.

Bound: internal/confidence/layer_a/firstcontact_test.go:TestFirstContact_PersistedPerLifetime
