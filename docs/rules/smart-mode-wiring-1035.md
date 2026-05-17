# Smart-mode pipeline wiring rules — issue #1035

Closes the 11 method-level wiring gaps from the post-#1033 audit. The
wiring layer lives in `cmd/ventd/smart_obs_bridge.go`,
`cmd/ventd/smart_builders.go`, `cmd/ventd/main.go`,
`internal/setup/setup.go`, and `internal/coupling/runtime.go`.

See `docs/rules-rationale/smart-mode-wiring.md` for the structural-
dead-code history, the buildSmartObsBridge design exposition, and the
helper-extraction binding pattern (audit pass-3 / #1075).

Each rule binds 1:1 to a subtest.

## RULE-CONFA-WIRING-01: layer_a.Estimator.Observe is called once per controller tick per channel from buildSmartObsBridge.

`buildSmartObsBridge(obsWriter, couplingRT, marginalRT, layerAEst)`
calls `layerAEst.Observe(rec.PWMPath, rec.PWMWritten, rec.RPM, 0, now)`
on every closure invocation. The call fires even when the sensor map
is empty (Layer-A's coverage histogram is sensor-independent), so a
fresh-install controller with no thermal sources still grows bin
coverage from PWM writes alone. `predictedRPM=0` skips the per-bin
residual update (Observe's documented behaviour); the bin count still
increments. A nil Layer-A estimator makes the call a no-op.

Bound: cmd/ventd/smart_obs_bridge_layera_test.go:TestSmartObsBridge_LayerAObserveFiresEveryTick
Bound: cmd/ventd/smart_obs_bridge_layera_test.go:TestSmartObsBridge_LayerANilIsNoOp

## RULE-CONFA-WIRING-02: buildLayerAEstimator calls Estimator.LoadChannel per channel after Admit.

`buildLayerAEstimator(channels, stateDir, hwmonFingerprint, logger)`
admits each controllable channel via `est.Admit` and immediately calls
`est.LoadChannel(stateDir, ch.PWMPath, hwmonFingerprint, logger)`. The
bin histogram, residual sum-of-squares, last-update wall-clock, and
first-contact flag from any persisted Bucket are restored. A
fingerprint mismatch (motherboard swap) discards cleanly per
RULE-CONFA-PERSIST-02; a schema mismatch discards per
RULE-CONFA-PERSIST-03. An empty `stateDir` (test scaffolding) skips
persistence entirely.

Bound: cmd/ventd/smart_builders_layera_test.go:TestBuildLayerA_LoadChannelRestoresHistogram
Bound: cmd/ventd/smart_builders_layera_test.go:TestBuildLayerA_EmptyStateDirSkipsLoad

## RULE-AGG-WIRING-01: Manager.run invokes the calibration-complete callback after Phase 6b's phantom-verify and before finalising.

`internal/setup/setup.go::Manager.run` calls
`m.calibrationCompleteFn(time.Now())` after `runAcousticGate` and
before `setPhase("finalizing", ...)`. Production binds the callback to
`aggregator.SetEnvelopeCDoneAt` in `cmd/ventd/main.go`. This anchors
the cold-start hard pin (RULE-AGG-COLDSTART-01) at a real wall-clock.
A nil callback (test default) is a clean no-op.

Bound: internal/setup/calibration_complete_test.go:TestManager_CalibrationCompleteCallbackFires
Bound: internal/setup/calibration_complete_test.go:TestManager_NilCalibrationCompleteCallbackIsNoOp

## RULE-CPL-IDENT-WIRING-04: The runtime's per-minute identifiability tick reads each shard's regressor window, classifies κ via ClassifyKappa, and writes via Shard.SetKind.

`runShardLoop`'s `identTick.C` case reads `shard.RegressorWindow()`,
calls `Window.Kappa()` when the window has at least `shard.Dim()` rows,
classifies via `ClassifyKappa`, and calls `shard.SetKind(kind, kappa)`.
`Shard.Update` populates the window on every controller tick via the
v0.6.0 smart_obs_bridge wiring; without the bridge feed the window
stays empty and the tick is a clean no-op (warmup gate continues to
report `KindWarmup` from `buildSnapshot`).

`Window.FindCoVaryingPairs(s.NCoupled())` runs alongside but is
log-only — the reduced-model uses NCoupled=0 so no pairs are ever
found; the call is structural / forward-compat for v0.7+.
RULE-CPL-IDENT-03's full Pearson-merge stays deferred.

Bound: internal/coupling/identifiability_wiring_test.go:TestRuntime_IdentifiabilityTickClassifiesKappa
Bound: internal/coupling/identifiability_wiring_test.go:TestRuntime_IdentifiabilityTickSkipsWhenWindowEmpty

## RULE-SIG-WIRING-01: Daemon start dispatches the signature warm-restart through the named loadSignatureState helper.

`runDaemonInternal` calls
`loadSignatureState(sigLib, smartMode.State.KV, logger)` (in
`cmd/ventd/smart_builders.go`) immediately after the SigFactory
returns. The helper reads the persisted manifest, then re-hydrates
every bucket's HitCount / LastSeenUnix / CurrentEWMA per
RULE-SIG-PERSIST-02. Binding is to the named helper (not inline
main.go code) so a regression has to actively delete a named-method
reference — see the helper-extraction pattern in the rationale doc.

Bound: cmd/ventd/main_signature_load_test.go:TestSignatureLoadLabels_RestoresPersistedBuckets
Bound: cmd/ventd/main_signature_load_test.go:TestSignatureLoadLabels_NoManifestIsColdStart
Bound: cmd/ventd/main_signature_load_test.go:TestSignatureLoadLabels_NilArgsAreNoOp
