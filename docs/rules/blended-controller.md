# Blended IMC-PI controller rules — v0.5.9 PR-A.3

These invariants govern v0.5.9's confidence-gated blended controller
in `internal/controller/blended.go`. The controller produces a
`predictive_output` PWM via IMC-PI gains derived from Layer-B's
per-channel RLS estimate of `(a, b_ii)`, and blends it with the
existing v0.4.x reactive curve under the per-tick weight `w_pred`
supplied by the aggregator (see `confidence-aggregator.md`):

```
output = w_pred · predictive + (1 − w_pred) · reactive
```

The patch spec is `specs/spec-v0_5_9-confidence-controller.md`
§2.1–§2.8. Each rule below binds 1:1 to a subtest in
`internal/controller/blended_test.go`. `tools/rulelint` blocks the
merge if a rule lacks its bound test.

The companion 3-tier R8 fallback classifier (`internal/fallback/`)
binds to `RULE-FALLBACK-TIER-*`, and the `coupling.Snapshot`
`Confidence()` method binds to `RULE-CPL-CONF-*`.

## Spec divergence — IMC-PI sign convention (load-bearing)

Earlier draft of spec §2.2 derived `K = b_ii/(1−a)` directly,
giving signed `K_p, K_i < 0` for cooling fans (b<0). With error =
sensor−setpoint, that produces positive feedback (verified
numerically in PR-A.3 implementation). The v0.5.9 implementation
takes `K = |b_ii/(1−a)|` (process-gain magnitude) and keeps
`K_p, K_i > 0`. Polarity is handled in `polarity.WritePWM`
(RULE-POLARITY-05), not the PI math. Spec has been amended to
match (search "SPEC DIVERGENCE" in `blended.go` for the full
correctness argument).

## RULE-CTRL-PI-01: Gain derivation uses K = |b_ii/(1−a)|; K_p = τ/(K(λ+θ)); K_i = K_p/τ; K_p > 0.

The IMC-PI gains are derived from Layer-B's RLS estimate as:

```
K   = | b_ii / (1 − a) |               // magnitude, NOT signed
τ   = clamp(-dt/ln(a), 50s, 1800s)
λ   = preset_lambda × τ                // 2τ / τ / τ/2 by preset
θ   = dt
K_p = τ / (K · (λ + θ))                // positive
K_i = K_p / τ                          // positive
```

The test pins K_p > 0 explicitly so a future cleanup that
reverts to `K = b_ii/(1−a)` (introducing the sign-flip
instability) fails CI.

Bound: internal/controller/blended_test.go:TestPI_GainDerivation

## RULE-CTRL-PI-02: First w_pred>0 tick is bumpless — predictive equals reactive at handoff.

When `w_pred` rises from 0 on a channel, the integrator is
initialised to `I[0] = -K_p · error` so that
`u[0] = K_p·error + I[0] = 0`. With `predictive_output =
baseline_pwm + u[k]` and `baseline = reactive`, this gives
`predictive[0] = reactive`. The blend is continuous through
the warmup→active transition; no PWM step.

Bound: internal/controller/blended_test.go:TestPI_BumplessTransfer_FirstWPredTick

## RULE-CTRL-PI-03: τ clamped to [TauMinSeconds, TauMaxSeconds].

`a → 0` would make τ collapse to ~dt; `a → 1` would explode τ
unboundedly. The clamp at [50s, 1800s] keeps gain derivation
numerically stable across the full range of plausible Layer-B
estimates. NAS-class drives have time constants in the 15-25
minute range; the upper cap covers them.

Bound: internal/controller/blended_test.go:TestPI_TauClampedToMinMax

## RULE-CTRL-PI-04: Anti-windup freezes integrator on PWM saturation in the direction the integrator is pushing.

When the candidate `predictive_output` saturates against
`[MinPWM, MaxPWM]` AND the integrator update would push
further into saturation (sign of error matches sign of the
saturated edge), the integrator is frozen for that tick — the
candidate `I[k]` is discarded; `I[k] = I[k-1]`. The integrator
unfreezes on the first tick where output is unsaturated OR
error sign reverses.

Bound: internal/controller/blended_test.go:TestPI_AntiWindup_PWMSaturation

## RULE-CTRL-PI-05: Six instability guards force `w_pred=0` for the channel-tick.

`piRefuseReason` returns `(reason, false)` — and the controller
falls through to reactive-only — when any of:

- `coupling.Snapshot == nil` (no shard exists)
- `coupling.Snapshot.WarmingUp == true` (parent not trustworthy)
- `Theta` is nil or shorter than 2 elements
- `a ≤ 0` or `a ≥ 1` (thermally divergent estimate)
- `b_ii == 0` (no observable response)
- `coupling.Snapshot.Kappa > 1e4` (R10 unidentifiable)
- `a` or `b_ii` non-finite (NaN / Inf)

Each path returns `PIRefused: true` in the result.

Bound: internal/controller/blended_test.go:TestPI_InstabilityGuards_AllSixCases

## RULE-CTRL-BLEND-01: Linear blend at intermediate w_pred — output = w_pred·predictive + (1-w_pred)·reactive (post-clamp).

The blend is a simple linear mix of the predictive arm and
reactive arm, then clamped to `[MinPWM, MaxPWM]` and rounded to
uint8. The test runs the integrator long enough that predictive
visibly diverges from reactive, then asserts the mix at
`w_pred=0.5`.

Bound: internal/controller/blended_test.go:TestBlend_LinearMix

## RULE-CTRL-BLEND-02: First-contact clamp never reduces cooling on the first w_pred>0 tick of a channel's lifetime.

When `LayerA != nil && !LayerA.SeenFirstContact` and the
predictive arm would output a PWM lower than reactive (i.e.
predicting we should reduce cooling), the controller clamps
output to reactive instead. Protects against a stale or
miscalibrated estimate driving the fan down on first engage.
Per-lifetime persisted in the `smart/conf-A/<channel>` bucket;
re-armed only on `WipeNamespaces`.

Bound: internal/controller/blended_test.go:TestBlend_FirstContactClamp_NeverReducesCooling

## RULE-CTRL-SMART-RELAX-FLOOR: a converged predictive arm may relax the fan at most RelaxMargin PWM below the reactive curve; with no resolvable setpoint the channel runs reactive-only.

The first-contact clamp (RULE-CTRL-BLEND-02) only guards the
*first* w_pred>0 tick. After a channel converges
(`SeenFirstContact`), the predictive arm may settle below the
reactive curve, bounded otherwise only by `MinPWM`. The reactive
curve is the trusted here-and-now signal ("at this temperature the
fan needs at least PWM P"); relaxing far below it bets a stale or
miscalibrated estimate against the curve's reading and can under-cool
the part.

`Compute` therefore floors the predictive arm every tick to
`max(MinPWM, ReactivePWM − in.RelaxMargin)` (helper `relaxFloorPWM`);
because Step 6's blend is a convex mix of predictive and reactive —
both ≥ floor — the output is floored too. `RelaxMargin` comes from
`Config.Smart.RelaxMarginPWM()`: nil ⇒ `DefaultMaxRelaxBelowCurve`
(25 ≈ 10% of range), an explicit `0` ⇒ never below the curve
(boost-only). `BlendedResult.RelaxFloorClamped` surfaces an engaged
floor to the doctor telemetry.

The setpoint that drives the relaxation is resolved without guessing:
an operator `smart.setpoints` entry wins; absent one the wiring layer
derives it from the bound sensor's thermal limit
(`DeriveSmartSetpointC` = `tempN_crit` / CPU Tjmax −
`smartSetpointMarginBelowLimitC`, clamped to
[`smartSetpointMinC`, `smartSetpointMaxC`]); if no plausible limit
resolves, `BuildFn` runs the channel reactive-only rather than
predict against the old silent 70°C default.

Bound: internal/controller/relax_floor_test.go:TestRelaxFloor_BoundsConvergedBelowCurveRelax
Bound: internal/controller/relax_floor_test.go:TestRelaxFloor_ZeroMargin_NeverBelowReactive
Bound: internal/controller/relax_floor_test.go:TestDeriveSmartSetpointC
Bound: internal/controller/relax_floor_test.go:TestRelaxFloorPWM
Bound: internal/config/relax_margin_test.go:TestRelaxMarginPWM
Bound: internal/smartblend/blend_test.go:TestBuildFn_NoSetpointNoDerived_ReactiveOnly
Bound: internal/smartblend/blend_test.go:TestBuildFn_DerivedSetpointUsed
Bound: cmd/ventd/relax_setpoint_test.go:TestHwmonSensorPathForCurve

## RULE-CTRL-BLEND-03: w_pred = 0 returns reactive byte-exact and skips PI math entirely.

When `w_pred ≤ 0` on input, the controller short-circuits at
the top of `Compute`: bumpless flag is re-armed and `OutputPWM
== ReactivePWM` exactly. No integrator update; no gain
derivation. Verifies the predictive code path doesn't have a
side-effect that mutates state at zero weight.

Bound: internal/controller/blended_test.go:TestBlend_ZeroWPred_ReturnsReactiveExact

## RULE-CTRL-PATH-A-01: Path-A refuses ramps where Layer-C predicts |ΔT| < 2°C; falls through to reactive and freezes integrator.

Re-derives the Layer-C Path-A saturation flag at the controller
call site for the candidate ΔPWM:

```
margin       = marginal.Theta[0] + marginal.Theta[1] · load
predicted_ΔT = margin · (predictive − reactive)
refuse iff |predicted_ΔT| < 2°C  AND  !Marginal.WarmingUp
```

When refused, output = reactive; `PathARefused=true` flag in
result; `IntegratorFrozen=true` (anti-windup hook so a refused
ramp doesn't accumulate).

Bound: internal/controller/blended_test.go:TestPathA_RefusalBelow2C_FallsThroughReactive

## RULE-CTRL-PATH-A-02: Nil Marginal Snapshot → Path-A is a no-op (no refusal).

When the Layer-C runtime has no shard for the channel (nil
Snapshot or short Theta), Path-A skips its check and the
predictive arm proceeds to the cost gate / blend. Conservative
default — Layer-C absent shouldn't disable predictive control.

Bound: internal/controller/blended_test.go:TestPathA_NilMarginalSnapshot_PathANoOp

## RULE-CTRL-COST-01: Cost factor table is 3.0 / 1.0 / 0.2 × CostFactorBalanced for Silent / Balanced / Performance.

`costFactorForPreset` returns the locked R18-stub multipliers:
Silent triples the per-PWM acoustic cost (cost-averse);
Performance reduces it to 20% (cost-tolerant); Balanced is
the unit baseline. `CostFactorBalanced = 0.01 °C-equivalent
per PWM-unit` per spec §2.7.

Bound: internal/controller/blended_test.go:TestCost_KFactorTable_3x_1x_0p2x

## RULE-CTRL-COST-02: Cost gate refuses ramps where benefit < cost.

```
cost(ΔPWM)    = k_factor[preset] · |ΔPWM|
benefit(ΔPWM) = -predicted_ΔT             // positive when cooling
refuse iff benefit < cost
```

Path-A is checked first; cost gate is the second guard, only
relevant when Path-A admits the ramp. Nil Marginal ⇒ no
refusal (Layer-C absent).

Bound: internal/controller/blended_test.go:TestCost_BenefitVsCost_RefusesWhenCostExceedsBenefit
