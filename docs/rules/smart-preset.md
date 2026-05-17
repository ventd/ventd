# Smart-mode preset config rules — v0.5.9 PR-A.4

These invariants govern the operator-visible smart-mode config
surface introduced in v0.5.9 PR-A.4. The blended controller
(PR-A.3) hardcodes a `Preset` enum; this PR adds the `SmartConfig`
struct that maps an operator-supplied YAML key to that enum, plus
the per-channel setpoint map and the reserved
`PresetWeightVector` for forward-compat with R18 (v0.7+).

The patch spec is `specs/spec-v0_5_9-confidence-controller.md`
§3.1 / §4. Each rule binds 1:1 to a subtest in
`internal/config/smart_test.go`.

## RULE-CTRL-PRESET-01: SmartConfig.SmartPreset() normalises empty / unknown inputs to "balanced"; reports recognition via the second return.

`SmartConfig.SmartPreset() (name string, ok bool)` is the canonical
parser. Empty string → ("balanced", true) — defaults are valid.
Recognised names ("silent" / "balanced" / "performance") round-trip
unchanged with ok=true. Unknown values normalise to "balanced" with
ok=false so the wiring layer (PR-B) can emit a single startup WARN
the first time it loads the config. Case-sensitive at the config
layer (the controller's `PresetFromString` accepts case variants).

Bound: internal/config/smart_test.go:TestSmartPreset_NormalisationAndOK

## RULE-CTRL-PRESET-02: validate() rejects setpoints outside [10, 100]°C and PresetWeightVector entries outside [0, 1]; unknown preset strings are NON-FATAL.

Asymmetric strictness by intent:

- **Setpoints** in `[10, 100]°C` are physically reasonable. A 5°C
  setpoint would lock the controller into perma-saturation; a
  150°C setpoint would silently disable the predictive arm. Reject
  at load so a typo surfaces immediately.
- **PresetWeightVector** entries in `[0, 1]` per spec §3.1's stated
  weight semantics. Out-of-range values reject.
- **Unknown preset strings** are non-fatal: `SmartPreset()` falls
  back to "balanced" and the wiring layer surfaces the typo as a
  one-shot WARN. Same forgiveness pattern as the existing
  Web.LoginFailThreshold default-when-zero and the experimental
  unknown-key warn-once (RULE-EXPERIMENTAL-SCHEMA-04).

Bound: internal/config/smart_test.go:TestSmartConfig_ValidationBoundaries
Bound: internal/config/smart_test.go:setpoint below 10°C rejected
Bound: internal/config/smart_test.go:setpoint above 100°C rejected
Bound: internal/config/smart_test.go:setpoint at exactly 10°C and 100°C accepted
Bound: internal/config/smart_test.go:PresetWeightVector out of [0,1] rejected
Bound: internal/config/smart_test.go:unknown preset string is non-fatal at load
Bound: internal/config/smart_test.go:known presets accepted at load

## RULE-CTRL-PRESET-03: PresetDBATargets is the canonical R32 mapping {Silent: 25, Balanced: 32, Performance: 45} dBA; DBATargetFor honours operator override over preset default.

The v0.5.12 quietness-target preset surface adds an operator-typed dBA cap
that the cost gate uses to refuse PWM ramps that would push host loudness
above the budget. The mapping comes from R32 user-perception thresholds:

- **Silent → 25 dBA** ("Whisper" — barely audible at desk distance)
- **Balanced → 32 dBA** ("Office" — comparable to a quiet office)
- **Performance → 45 dBA** (workstation under load, audible but not loud)

`PresetDBATargets` (in `internal/controller/blended.go`) is a `map[Preset]float64`
holding these three entries. `DBATargetFor(p Preset, override *float64) float64`
is the resolver — when `override` is non-nil, the operator's value wins;
otherwise the preset's default is returned. An unrecognised `Preset` enum
falls back to the Balanced default (32 dBA), matching `costFactorForPreset`'s
fall-through behaviour.

`SmartConfig.DBATarget *float64` is the operator surface. Nil leaves the
budget to be resolved from the preset default at runtime; an explicit
value overrides. Validation rejects values outside `[10, 80]` dBA — 10 dBA
is below typical room-ambient floor (impossible to honour); 80 dBA is
louder than any consumer fan setup can plausibly produce, so a value
above 80 indicates a typo or wrong unit.

Bound: internal/controller/blended_test.go:TestPresetDBATargets_LockedAndOverrideHonoured
Bound: internal/config/smart_test.go:TestSmartConfig_DBATargetValidation
Bound: internal/config/smart_test.go:nil_dba_target_accepted
Bound: internal/config/smart_test.go:dba_target_below_10_rejected
Bound: internal/config/smart_test.go:dba_target_above_80_rejected
Bound: internal/config/smart_test.go:dba_target_at_boundaries_accepted
Bound: internal/config/smart_test.go:dba_target_yaml_round_trip
Bound: internal/config/smart_test.go:dba_target_omitted_when_nil

## RULE-CTRL-PRESET-04: EvalDBABudget refuses ramps that push the candidate dBA strictly above the configured target; zero/negative target disables the gate.

`EvalDBABudget(b AcousticBudget, deltaPWM float64) (refuse bool, predictedDBA float64)`
is the pure-data dBA-budget gate using linear extrapolation:

```
candidate_dBA = CurrentDBA + DBAPerPWM · |ΔPWM|
refuse iff candidate_dBA > Target
```

Refusal is on strict inequality (`candidate_dBA == Target` admits).
Zero/negative `Target`, or zero `DBAPerPWM`, disables the gate. ΔPWM
is direction-agnostic (negative ramp uses `|ΔPWM|`).

`BlendedController.Compute` invokes the gate after the cost gate (and
after Path-A). On refusal: `OutputPWM = ReactivePWM`,
`DBABudgetRefused: true`, `PredictedDBA` populated for telemetry,
`UIState = "refused-dba"`. Unlike Path-A / PI-saturation refusals,
the integrator continues to accumulate on dBA refusal — recovery is
natural-condition-driven.

See `docs/rules-rationale/smart-preset-and-signature-persistence.md`
for the AcousticBudget population (R33 proxy + R30 K_cal), the
five-step refusal cascade order, and the controller-integrator
asymmetry rationale.

Bound: internal/controller/blended_test.go:TestEvalDBABudget_RefusesAboveTarget
Bound: internal/controller/blended_test.go:admit_when_inside_budget
Bound: internal/controller/blended_test.go:refuse_when_above_target
Bound: internal/controller/blended_test.go:at_target_admits
Bound: internal/controller/blended_test.go:zero_target_disables_gate
Bound: internal/controller/blended_test.go:negative_target_disables_gate
Bound: internal/controller/blended_test.go:zero_dba_per_pwm_disables_gate
Bound: internal/controller/blended_test.go:delta_pwm_is_absolute_value
Bound: internal/controller/blended_test.go:TestBlend_DBABudget_RefusesPredictiveAboveTarget
Bound: internal/controller/blended_test.go:TestBlend_DBABudget_NoOpWhenZeroTarget
Bound: internal/controller/blended_test.go:TestBlend_DBABudget_PathARefusalShortCircuitsDBA
