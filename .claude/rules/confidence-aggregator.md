# Confidence aggregator rules — v0.5.9 PR-A.2

These invariants govern the per-channel confidence aggregator in
`internal/confidence/aggregator/`. The aggregator collapses
`conf_A` (Layer A), `conf_B` (Layer B), and `conf_C` (Layer C) into
a single per-channel `w_pred` weight that the v0.5.9 blended
controller uses to mix predictive and reactive PWM outputs.

The patch spec is `specs/spec-v0_5_9-confidence-controller.md` §2.5
+ §3.3 + §3.4 + §5.2. The R-bundle source is R12 §Q3 (LPF /
Lipschitz / drift decay smoothness machinery), R12 §Q4 (cold-start
hard pin), and R12 §Q6 (global gate + active-signature collapse).

Each rule binds 1:1 to a subtest in
`internal/confidence/aggregator/aggregator_test.go`.

## RULE-AGG-MIN-01: w_raw = clamp(min(conf_A_decayed, conf_B_decayed, conf_C_decayed), 0, 1).

Step 2 of the aggregation chain. The min collapse takes the
smallest of the three (post-drift-decay) layer confidences and
clamps the result to `[0, 1]`. This is the "weakest link"
behaviour — a single layer at low confidence pulls the entire
channel's predictive contribution down. Negative inputs are
clamped to 0; values above 1 cannot survive the min when paired
with values ≤ 1.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_MinCollapse

## RULE-AGG-LPF-01: LPF wraps the min, NOT each component; τ_w = 30 s.

Step 3. Single-pole exponential LPF applied to `w_raw` with time
constant `LPFTauW = 30 s`. The LPF wraps the min collapse output;
each individual layer confidence is NOT filtered separately. With
`dt = τ_w / 2 = 15 s` and `prev = 0`, a step input of 0.8 produces
`w_filt = 0.4` on the first filtered tick and `0.6` on the next,
following the standard exponential rise toward 0.8.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_LPFWrapsMin

## RULE-AGG-LIPSCHITZ-01: |w_pred − w_pred_prev| ≤ L_max·dt = 0.1 PWM-units/2s.

Step 4. After the LPF, the per-tick delta in `w_pred` is clamped
to `±L_max · dt`. With `LMax = 0.05/s` and a 2-second controller
tick, the maximum step is `0.1`. A sudden conf drop from 1.0 to
0.0 cannot reduce the predictive blend weight faster than this
rate — protects against thermal overshoot when a layer's
confidence collapses suddenly.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_LipschitzClamp

## RULE-AGG-DRIFT-01: drift_flag triggers per-layer 0.5^(t/60s) decay BEFORE min collapse.

Step 1. When R16 (or any future drift detector) sets a layer's
`drift_flag` to true, the corresponding `conf_X` input is
multiplied by `0.5^(seconds_since_drift_set / DriftHalfLife)`
before the min collapse. `DriftHalfLife = 60 s` per R12 §Q5. The
decay applies per-layer — layer A drifting does not decay layers
B or C. Clearing the flag (via `SetDrift(..., set=false, ...)`)
returns that layer to undecayed contribution on the next tick.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_DriftDecaysBeforeMin

## RULE-AGG-COLDSTART-01: w_pred = 0 for 5 min after Envelope C completion.

Cold-start hard pin per spec-v0_5_9 §3.3. For exactly
`ColdStartWindow = 5 minutes` after the timestamp passed to
`SetEnvelopeCDoneAt`, every Tick returns `w_pred = 0` regardless
of the LPF / Lipschitz outputs. The UI label is `cold-start`
during this window. Outside the window the standard machinery
takes over. R12 §Q4 specifies a 5-10 minute envelope by class;
v0.5.9 ships the uniform 5-minute pin pending HIL telemetry.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_ColdStartHardPin

## RULE-AGG-GLOBAL-01: w_pred_system AND-gate forces every channel's w_pred to 0; bypasses Lipschitz on flip.

Per R12 §Q6, the system-level AND-gate `w_pred_system` (composed
from state-loaded / hard-preconditions / wizard outcome / no-mass-
stall) overrides per-channel state. When the gate is `false`, the
aggregator's Tick returns `w_pred = 0` *immediately* — bypassing
the Lipschitz clamp that would normally limit the rate of change.
This is the only sanctioned bypass and matches R12's "instantly
drop to safe" requirement: when any one of the four AND-gate
components fails, predictive control must yield to reactive.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_GlobalGate

## RULE-AGG-SIG-COLLAPSE-01: conf_C[channel] = active-signature shard's product term; 0 when no warmed shard.

The aggregator does not own the per-(channel, signature) shard
collapse — the caller passes `conf_C` as a scalar already
collapsed via the active-signature rule from R12 §Q6. When the
current signature has no warmed shard, the caller passes
`conf_C = 0`; the aggregator's min collapse picks 0 and the LPF
+ Lipschitz pull `w_pred` down at the locked rate. The contract
is that "no warmed shard for the active signature" is operator-
visible via the doctor surface — not silent.

Bound: internal/confidence/aggregator/aggregator_test.go:TestAggregator_ActiveSignatureCollapse
