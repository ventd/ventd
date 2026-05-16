# dBA budget gate + signature persistence eviction — rationale

This document carries the design exposition for:
- RULE-CTRL-PRESET-04 (in `.claude/rules/smart-preset.md`)
- RULE-SIG-PERSIST-03 (in `.claude/rules/signature.md`)

## dBA budget gate (RULE-CTRL-PRESET-04)

### The AcousticBudget struct

The wiring layer (eventual #67/#112 Manager.run refactor) populates
`BlendedInputs.Acoustic` with three values:

- `Target`: the operator-resolved dBA cap from `DBATargetFor` (preset
  default or operator override).
- `CurrentDBA`: the host's current total loudness in dBA, from the
  R33 acoustic proxy's energetic sum across grouped fans + the R30
  K_cal calibration offset.
- `DBAPerPWM`: the candidate channel's marginal loudness rate per PWM
  unit, from the proxy's `CostRate`.

Until that wiring lands the controller behaves identically to v0.5.11
for any caller that leaves Acoustic at its zero value (Target ≤ 0
disables the gate).

### Controller integration

`BlendedController.Compute` calls `EvalDBABudget` after the existing
cost gate (and after Path-A). When the gate refuses, the controller
returns `OutputPWM = ReactivePWM`, sets `DBABudgetRefused: true`,
populates `PredictedDBA` for telemetry, and surfaces the refusal as
`UIState = "refused-dba"`.

### Integrator behaviour on refusal

The integrator continues to accumulate on a dBA-budget refusal (matching
cost-gate semantics — no freeze). Recovery happens naturally when
conditions change. This differs from Path-A and PI-saturation refusals
which DO freeze the integrator.

### Refusal cascade order (in `Compute`)

1. PI instability guards → `refused-pi`
2. PI saturation (anti-windup) → integrator frozen, blend continues
3. Path-A predicted-ΔT gate → `refused-pathA`, integrator frozen
4. Cost gate (benefit < cost) → `refused-cost`
5. dBA-budget gate (predicted dBA > target) → `refused-dba`

Path-A and cost gate short-circuit the dBA check — the gate only runs
when both upstream gates admit. That priority chain means the dBA
gate's blast radius is limited to ramps the upstream gates already
deemed "thermally beneficial enough"; the dBA gate is the acoustic
veto layered on top.

### Direction-agnostic ΔPWM

Negative ΔPWM (cooling-down ramp) is treated as `|ΔPWM|` because a
ramp's loudness impact is direction-agnostic.

## Signature persistence eviction (RULE-SIG-PERSIST-03)

### Why the rule exists (audit C7)

Without this rule the in-memory cap (RULE-SIG-LIB-05 caps at 128 LRU
buckets) silently diverges from the on-disk row count: workloads that
get LRU-evicted from memory keep their KV row forever because `Save`
only writes the buckets currently in memory and never deletes ones
that aren't. A long-running daemon with workload churn (game launches,
browser tabs, dev tooling) sees the persisted row count climb without
bound — issue C7 of the v0.5.26 senior review identified this as a
state-persistence correctness gap that pressures `/var/lib/ventd/`
disk usage.

### Why 30 days

`PersistedEvictionAge = 30 * 24 * time.Hour`. Double the R7 §Q5
weighted-LRU time constant (τ=14 days) — a workload that hasn't fired
for two τ-halvings is functionally gone, and the weighted-LRU score
`HitCount × exp(-(age/τ))` already collapses toward zero at that age.

The constant is exported so the daemon-start helper
(`loadSignatureState` in `cmd/ventd/smart_builders.go`) and any future
operator-tunable knob share a single source of truth.

### Sweep contract

1. **No manifest → no-op.** A fresh install has no persisted labels;
   `LoadManifest` returns an empty slice; the sweep returns `(0, nil)`
   without touching KV.
2. **Stale bucket → deleted.** A row whose `LastSeenUnix` is strictly
   older than `cutoff.Unix()` is removed via
   `kv.Delete(KVNamespace, label)`. The label is dropped from the
   rewritten manifest.
3. **Corrupt bucket → deleted.** A row whose msgpack payload fails to
   decode is unrecoverable; it counts against the on-disk budget the
   same as a healthy row. Best-effort delete (per-row error is the
   first-returned, not abort).
4. **Dangling manifest entry → dropped silently.** A label present in
   the manifest but with no KV row gets pruned from the rewritten
   manifest without an error surface — the natural cleanup case for a
   save-without-manifest-rewrite race.
5. **Survivor → kept, listed in rewritten manifest.** A row that
   decodes cleanly and whose `LastSeenUnix ≥ cutoff.Unix()` stays
   untouched.

### Failure handling

Per-row errors do not abort the loop (best-effort sweep); the daemon
proceeds with whatever survived and logs a WARN. The integration point
in `loadSignatureState` calls the sweep BEFORE `LoadLabels` so stale
labels never enter the in-memory map.

### Interface extension

`kvStore` interface is extended to require
`Delete(namespace, key string) error`. `*state.KVDB` (production
implementation) already exposes this; the test `fakeKV` mock adds a
one-line implementation.
