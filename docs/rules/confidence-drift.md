# Per-layer drift detection rules — R16 / R11 PR-2

These invariants govern `internal/confidence/drift`, the per-(channel,
layer) drift detector that feeds the aggregator's `driftFlags [3]bool`.
v0.5.9 shipped the aggregator already consuming drift flags (a flagged
layer's confidence decays by 0.5^(t/60s) before the min-collapse,
RULE-AGG-DRIFT-01) but with no producer; this is the R16 producer.

The method is the residual EWMA control chart (Ross, Adams, Tasoulis &
Hand, "Exponentially weighted moving average charts for detecting concept
drift", Pattern Recognition Letters 2012; arXiv:1212.6018) with a
self-referential per-layer baseline. EWMA lags on ABRUPT change by design:
the PR-1 `w_pred_system` gate handles abrupt system failures instantly, so
this detector's remit is GRADUAL per-layer model drift (re-cabled fan,
dust, ambient shift, curve edit). The math (chart.go `step`) is a pure
function; the per-channel state lives behind a mutex in the Detector
(mirroring aggregator.Aggregator), and the web surface reads it lock-free
via an atomic.Pointer.

## RULE-DRIFT-CHART-01: the EWMA chart trips when z exceeds μ+L·σ (after warmup + TripDwell) and clears below μ+LClear·σ (after ClearDwell); step() is deterministic.

The monitored statistic z is an EWMA of the sqrt-residual. A steady
signal never flags. A sustained step-up above the control limit μ+L·σ
trips after `TripDwell` consecutive observations, but only once
`WarmupTicks` post-convergence observations have built a trusted baseline.
A return below μ+LClear·σ (LClear<L → hysteresis) clears after
`ClearDwell` consecutive observations. σ has a `MinSigma` floor so a
near-constant residual cannot make any micro-wobble trip. `step` is a pure
function: identical input sequences yield identical state.

Bound: internal/confidence/drift/chart_test.go:TestDriftChart_StepTripsAtControlLimit

## RULE-DRIFT-CONVERGE-01: a layer that has not converged never flags, regardless of residual magnitude.

The convergence guard short-circuits the chart while the layer is still
warming: a warming layer's high residual is the model learning, not drift.
No flag is set and the baseline is not updated until the caller reports
the layer converged (Layer A: first-contact + coverage; Layer B/C: the
shard's own `!WarmingUp`).

Bound: internal/confidence/drift/detector_test.go:TestDrift_ConvergenceGuardNeverFlagsWhileWarming

## RULE-DRIFT-HYSTERESIS-01: trip requires TripDwell consecutive over-limit observations and clear requires ClearDwell under-clear-limit observations (no single-tick flap).

The dwell counters debounce both edges so a one-tick excursion neither
trips nor clears the flag — the UI "drifting" pill does not flicker.

Bound: internal/confidence/drift/detector_test.go:TestDrift_TripAndClearDwellHysteresis

## RULE-DRIFT-BASELINE-FREEZE-01: while flagged, the baseline μ and dispersion σ stop updating.

Freezing the reference while drifting prevents the very anomaly that
tripped the chart from being slowly absorbed into the baseline, which
would silently un-flag a genuine, sustained drift.

Bound: internal/confidence/drift/detector_test.go:TestDrift_BaselineFrozenWhileDrifting

## RULE-DRIFT-RESTART-01: a freshly-constructed Detector starts clean; drift state does not persist a process restart.

Like the coupling / marginal / layer_a runtimes, the detector has no
Reset/Wipe: its in-memory per-channel state lives for the daemon-process
lifetime and resets only on a true process restart (which rebuilds the
whole SmartModeBundle). A fresh Detector reports a clean, not-converged
snapshot for any channel and never flags on the first observation.

Bound: internal/confidence/drift/detector_test.go:TestDrift_FreshDetectorStartsClean

## RULE-DRIFT-LAYERB-RESIDUAL-01: coupling.Shard.Update accumulates ewmaResidual = α·prev + (1−α)·e² (α=0.95) and Snapshot.EWMAResidual exposes it; Confidence() is unchanged.

Layer B previously discarded its per-tick RLS innovation. It now folds e²
into an EWMA (matching Layer C's α) exposed on the Snapshot purely for the
drift monitor — `Confidence()` does not read it, so existing Layer-B
behaviour is preserved (the new field defaults 0). Layer A's `RMSResidual`
and Layer C's `EWMAResidual` were already exposed.

Bound: internal/coupling/shard_test.go:TestShard_EWMAResidualAccumulatesAndExposed

## RULE-DRIFT-AGG-WIRING-01: the blend hook passes the detector's flags into aggregator.Tick (never SetDrift), so a flagged layer surfaces as DriftFlags + UIState "drifting".

`smartblend.BuildFn`'s closure calls `Drift.Observe` and passes the
resulting `[3]bool` straight into `aggregator.Tick`. It never calls
`SetDrift` — the aggregator's Tick already records the false→true set-time
and zeroes it on clear, so driving both would skew the decay clock. A nil
detector (monitor-only) yields the v0.5.9 `[3]bool{}` (no drift).

Bound: internal/smartblend/blend_test.go:TestBlend_DriftFlagsFromDetectorIntoTick

## RULE-DRIFT-WIRING-01: buildDriftDetector returns nil iff there are no controllable channels.

Monitor-only hosts get a nil detector (the blend hook then produces no
drift flags); any host with ≥1 controllable channel gets a live detector.

Bound: cmd/ventd/main_drift_test.go:TestBuildDriftDetector_NilWhenNoChannels
