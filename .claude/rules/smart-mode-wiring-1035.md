# Smart-mode pipeline wiring rules — issue #1035 (v0.6.0)

These invariants close the 11 method-level wiring gaps surfaced by
the post-#1033 audit (issue #1035 + the pass-1-callsite-sweep
finding): Layer-A's `Observe`, Layer-A's `LoadChannel`, the
confidence aggregator's `SetEnvelopeCDoneAt`, the coupling shard's
`SetKind`, and the signature library's `LoadLabels` all had zero
production callers from v0.5.7 through v0.5.37. The blended
controller's `w_pred ≤ 0` short-circuit was the only branch that
ever fired in production because conf_A always multiplied to 0.

The wiring layer lives in:

- `cmd/ventd/smart_obs_bridge.go` — Layer-A's `Observe` is called
  on every controller tick alongside the existing Layer-B / Layer-C
  feed (RULE-CPL-WIRING-04, RULE-CMB-WIRING-04).
- `cmd/ventd/smart_builders.go` — `buildLayerAEstimator` accepts
  `stateDir` + `hwmonFingerprint` and calls `LoadChannel` per
  channel after `Admit` (RULE-CONFA-PERSIST-01/02).
- `cmd/ventd/main.go` — `SetCalibrationCompleteFn` is bound to
  `Aggregator.SetEnvelopeCDoneAt`; `LoadManifest` + `LoadLabels` run
  at daemon start.
- `internal/setup/setup.go` — `Manager.run` invokes the
  calibration-complete callback after Phase 6b's phantom-verify and
  before the finalising phase.
- `internal/coupling/runtime.go` — the per-minute identifiability
  tick reads each shard's rolling regressor window, computes κ,
  classifies via `ClassifyKappa`, calls `Shard.SetKind`, and logs
  any co-varying-fan pair candidates.

Each rule binds 1:1 to a subtest.

## RULE-CONFA-WIRING-01: layer_a.Estimator.Observe is called once per controller tick per channel from buildSmartObsBridge.

`buildSmartObsBridge(obsWriter, couplingRT, marginalRT, layerAEst)`
calls `layerAEst.Observe(rec.PWMPath, rec.PWMWritten, rec.RPM, 0,
now)` on every closure invocation. The call fires even when the
sensor map is empty (Layer-A's coverage histogram is sensor-
independent), so a fresh-install controller with no thermal sources
still grows bin coverage from PWM writes alone. `predictedRPM=0`
skips the per-bin residual update (Observe's documented behaviour);
the bin count still increments, which is the structural-coverage
contribution RULE-CONFA-COVERAGE-01 needs. A nil Layer-A estimator
makes the call a no-op.

Without this wiring conf_A stays at 0 forever and the aggregator's
min-collapse pins `w_pred = 0` system-wide — the symptom that
surfaced as issue #1035 row 1 / RFC #1024 re-soak.

Bound: cmd/ventd/smart_obs_bridge_layera_test.go:TestSmartObsBridge_LayerAObserveFiresEveryTick
Bound: cmd/ventd/smart_obs_bridge_layera_test.go:TestSmartObsBridge_LayerANilIsNoOp

## RULE-CONFA-WIRING-02: buildLayerAEstimator calls Estimator.LoadChannel per channel after Admit.

`buildLayerAEstimator(channels, stateDir, hwmonFingerprint, logger)`
admits each controllable channel via `est.Admit` and immediately
calls `est.LoadChannel(stateDir, ch.PWMPath, hwmonFingerprint,
logger)`. The bin histogram, residual sum-of-squares, last-update
wall-clock, and first-contact flag from any persisted Bucket are
restored. A fingerprint mismatch (motherboard swap) discards
cleanly per RULE-CONFA-PERSIST-02; a schema mismatch discards per
RULE-CONFA-PERSIST-03. An empty `stateDir` (test scaffolding)
skips persistence entirely.

Without this wiring conf_A re-warms from zero on every daemon
restart — the slow-EWMA recency reset documented as issue #1035
row 2.

Bound: cmd/ventd/smart_builders_layera_test.go:TestBuildLayerA_LoadChannelRestoresHistogram
Bound: cmd/ventd/smart_builders_layera_test.go:TestBuildLayerA_EmptyStateDirSkipsLoad

## RULE-AGG-WIRING-01: Manager.run invokes the calibration-complete callback after Phase 6b's phantom-verify and before finalising.

`internal/setup/setup.go::Manager.run` calls
`m.calibrationCompleteFn(time.Now())` after `runAcousticGate` and
before `setPhase("finalizing", ...)`. Production code binds the
callback to `aggregator.SetEnvelopeCDoneAt` in
`cmd/ventd/main.go`. This anchors the cold-start hard pin
(RULE-AGG-COLDSTART-01) at a real wall-clock instead of the
zero-value `time.Time` that left the pin structurally inert across
every v0.5.x release — every Tick computed `elapsed > 5 min` true
and admitted the predictive arm immediately (issue #1035 row 4).

A nil callback (the test default) is a clean no-op; the wizard
proceeds without surfacing the v0.6.0 cold-start window to test
scaffolding.

Bound: internal/setup/calibration_complete_test.go:TestManager_CalibrationCompleteCallbackFires
Bound: internal/setup/calibration_complete_test.go:TestManager_NilCalibrationCompleteCallbackIsNoOp

## RULE-CPL-IDENT-WIRING-04: The runtime's per-minute identifiability tick reads each shard's regressor window, classifies κ via ClassifyKappa, and writes via Shard.SetKind.

`runShardLoop`'s `identTick.C` case reads
`shard.RegressorWindow()`, calls `Window.Kappa()` when the window
has at least `shard.Dim()` rows, classifies via `ClassifyKappa`,
and calls `shard.SetKind(kind, kappa)`. `Shard.Update` populates
the window on every controller tick via the v0.6.0
smart_obs_bridge wiring; without the bridge feed the window stays
empty and the tick is a clean no-op (the warmup gate continues to
report `KindWarmup` from `buildSnapshot`).

`Window.FindCoVaryingPairs(s.NCoupled())` runs alongside but is
log-only — the v0.5.7 reduced-model uses NCoupled=0 so no pairs
are ever found; the call is structural / forward-compat for v0.7+
when NCoupled rises. RULE-CPL-IDENT-03's full Pearson-merge stays
deferred.

Without this wiring `Snapshot.Kappa` stays at 0 and the controller's
PI-instability guard never sees the unidentifiability threshold
(RULE-CTRL-PI-05's `kappa > 1e4` branch is structurally dead).

Bound: internal/coupling/identifiability_wiring_test.go:TestRuntime_IdentifiabilityTickClassifiesKappa
Bound: internal/coupling/identifiability_wiring_test.go:TestRuntime_IdentifiabilityTickSkipsWhenWindowEmpty

## RULE-SIG-WIRING-01: Daemon start calls signature.Library.LoadLabels after LoadManifest.

`runDaemonInternal` calls `signature.LoadManifest(state.KV)` and
then `sigLib.LoadLabels(state.KV, labels)` immediately after the
SigFactory returns. HitCount, LastSeenUnix, and CurrentEWMA from
every persisted bucket are restored per RULE-SIG-PERSIST-02.

Without this wiring every daemon restart wipes the operator-visible
workload history (issue #1035 row 11) — even though `Save` /
`SaveManifest` were running on the 60s persistence ticker, the
read side was never wired.

Bound: cmd/ventd/main_signature_load_test.go:TestSignatureLoadLabels_RestoresPersistedBuckets
