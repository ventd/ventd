# Smart-mode wiring closure — rationale

This document carries the historical backstory + design exposition for the
v0.6.0 "wiring closure" rules. The invariants themselves live in
`docs/rules/coupling.md`, `docs/rules/marginal.md`,
`docs/rules/smart-mode-wiring-1035.md`, and
`docs/rules/confidence-layer-a.md`. Read this doc when you need to
understand *why* the wiring landed shaped the way it did; the rule files
carry the *what* and *how*.

## The structural-dead-code era (v0.5.7 → v0.5.37)

v0.5.7 PR-B (#738) wired the coupling runtime's *lifecycle* — `Run`,
`AddShard`, periodic persistence — but never wired the data feed.
`coupling.Shard.Update` had zero production callers from v0.5.7 through
v0.5.37: shards persisted to disk every minute, but every persisted shard
always carried `n_samples=0, theta=[0,0]` because nothing ever called
`Update`. RFC #1024 ("smart-mode doesn't advance under realistic workload")
was the symptom; the ghost-code finding (issue #1033) was the root cause.

Marginal-benefit was the same shape. v0.5.8 PR-B (#742) wired the
lifecycle; v0.5.9 was supposed to wire `OnObservation` per the v0.5.8
spec's RULE-CMB-WIRING-03 — that never happened. `marginal.Runtime.OnObservation`
had zero callers from v0.5.8 through v0.5.37.

Layer-A's `Observe` and `LoadChannel`, the confidence aggregator's
`SetEnvelopeCDoneAt`, the coupling shard's `SetKind`, and the signature
library's `LoadLabels` were all in the same structural-dead state.
Pass-1-callsite-sweep (issue #1035) catalogued the eleven methods.

The blended controller's `w_pred ≤ 0` short-circuit was the only branch
that ever fired in production because `conf_A` always multiplied to 0 —
because `Observe` was never called.

## The v0.6.0 fix: buildSmartObsBridge

`cmd/ventd/smart_obs_bridge.go` is the new closure that replaces
`buildObsAppend` as the value of `SmartModeBundle.ObsAppend`. It is the
single per-tick hot path that:

1. Persists to the observation log (unchanged from v0.5.x).
2. Picks `T_now = maxTempReading(rec.SensorReadings)` as the per-channel
   temperature proxy (v0.6.0 first-cut).
3. Maintains per-channel `lastTemp` state; skips the first tick of a
   channel's lifetime (no `T_prev`).
4. Fans out to:
   - `couplingRT.Shard(...).Update(now, []float64{lastTemp, pwm}, T_now)`
   - `marginalRT.OnObservation(...)` with `DeltaT = T_now − lastTemp`
   - `layerAEst.Observe(rec.PWMPath, rec.PWMWritten, rec.RPM, 0, now)`
5. The `time.UnixMicro(rec.Ts)` conversion is load-bearing —
   `controller.ObsRecord.Ts` is Unix microseconds, not seconds or
   nanoseconds.

## The maxTempReading proxy — deliberate correctness loss

Every channel sees the same `T_now`, so Layer-B's `θ[0]` (autoregressive
coefficient) is co-estimated against a shared signal. `θ[1]` (b_ii
self-coupling) differentiates per channel because each channel's pwm is
independent. For first-cut Layer-B convergence this is sufficient — the
senior review's R8 fallback ceiling at tier 1 (real RPM tach) is 0.85
anyway. Per-channel sensor binding (curve.temp_sensor → channel) raises
the ceiling but doesn't change the structural correctness of the wiring.
That refinement lands in a v0.6.x follow-up once HIL evidence confirms
the proxy is sufficient.

## Layer-C `Load` value

Layer-C's d=2 form `φ=[1, load]` learns `θ[0]` as the intrinsic ΔT-per-PWM
and `θ[1]≈0` when load is constant; saturation predictions stay accurate
for the load-independent case. v0.6.0 ships with `Load=0.0` hardcoded.
v0.6.x plumbs PSI cpu.some avg10 from `idle.Capture` (same source the
soft-idle gate uses).

## The OAT relaxation — RULE-CMB-OAT-01 pwm-group amendment

Phoenix's MSI Z690-A HIL evidence (Phase C5 RFC #1024) revealed a
structural failure mode: on boards where multiple PWM sysfs channels are
firmware-mirrored (or physically driven by a single PWM register that
fans out to multiple headers), intra-mirror movement appears as
cross-channel interference to the strict `j ≠ i` form of OAT
(one-at-a-time) admissibility. Every Layer-C admission attempt on a
mirrored channel was rejected because the firmware-mirrored siblings
also changed PWM — even though they're operationally the same actuator.

Phoenix's Z690-A drove CPU_Fan + Pump_Fan + Sys_Fan_1 + Sys_Fan_2 with
identical PWM values across 2479 captured samples (per the
RULE-HWDB-PR2-15 motivating note); the v0.5.x OAT rejected every
admission, Layer-C never advanced.

The v0.6.0 fix: `Runtime.SetPWMGroups([][]string)` declares the
operationally-co-moving channel sets. The OAT gate now excludes
intra-group movement from the quiet-window check; channels outside the
group still gate normally. Group declarations come from
`hwdb.BoardProfile.PWMGroups` via the catalog match; in catalog-data-
absent deployments the runtime continues to behave exactly as v0.5.x.

Costs convergence speed in highly-coupled chassis; full R17 INTERFERENCE
work remains v0.7+. Group declarations are not a substitute for R17 —
they're a targeted relaxation for the firmware-mirroring failure mode.

## Helper-extraction binding pattern

Per the audit recipe (pass-3, #1075): when a rule binds to a "named
helper" rather than to inline code in `main.go`, a future regression that
drops the call site has to actively delete a named-method reference,
which is much harder to do by accident than removing an inline block.
`loadSignatureState`, `buildLayerAEstimator`, `buildCouplingRuntime`,
`buildMarginalRuntime`, and `buildSmartObsBridge` all follow this
pattern. Tests bind against the helpers, not against `main.go` line
numbers.

## Why these rules ship as separate H2 entries vs. one consolidated rule

Each wiring closure has a distinct production caller, a distinct symptom
when it regresses, and a distinct subtest. Bundling them into one
mega-rule would obscure which closure broke when the inevitable next
"smart-mode doesn't converge" RFC lands. The eleven-row method table in
issue #1035 maps 1:1 to the eleven binding rules; keeping that
correspondence visible is the load-bearing reason for the granularity.
