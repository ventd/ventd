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
