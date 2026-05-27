# w_pred_system global gate rules — v0.5.9 / R11

These invariants govern the v0.5.9 confidence controller's
`w_pred_system` global gate in `internal/confidence/gate/`. The gate
composes five real daemon signals into a single boolean the aggregator
consumes (the `wPredSystem` argument of `aggregator.Tick`): when the gate
is closed, every channel's `w_pred` is forced to 0 — bypassing the
Lipschitz cap — and the UI reports "refused". Predictive control yields
to the reactive curve instantly.

The patch spec is `specs/spec-v0_5_9-confidence-controller.md` §2.5 (the
AND-gate) + §3.6 (disable inheritance). The gate is a pure composition
kernel: it imports only the standard library and reads its terms through
function seams that the wiring layer (cmd/ventd) binds to
`state.SchemaVersionLoaded`, `idle.CheckHardPreconditions`,
`probe.LoadWizardOutcome`, `massstall.Tracker.MassStalled`, and
`config.SmartDisabled`.

## RULE-GATE-COMPOSE-01: w_pred_system = AND(!smart_disabled, schema_loaded, hard_preconditions_ok, wizard==control, no_mass_stall).

The gate opens iff all five terms pass; any single failing term closes
it. A nil signal seam is treated as passing (it never closes the gate),
so the fail-safe lives in construction (closed until the first Evaluate),
not in the per-term logic. Per spec §2.5 + §3.6.

Bound: internal/confidence/gate/gate_test.go:TestGate_AndComposition

## RULE-GATE-REASON-01: a closed gate reports the first failing term in priority order smart_disabled > schema > preconditions > wizard > mass_stall.

When several terms fail at once the reported Reason is the
highest-priority (most operator-actionable) one, so the doctor/API
surface names the thing the operator should address first.

Bound: internal/confidence/gate/gate_test.go:TestGate_FailingReasonOrder

## RULE-GATE-FAILSAFE-01: a freshly-constructed evaluator reports Open()==false until the first Evaluate.

`New` installs a CLOSED snapshot so a reader (the blend hook, the web
surface) that races ahead of the evaluator's first tick sees the gate
shut, not open. A nil evaluator also reports closed. The monitor-only
"no gate wired → predictive allowed" case is handled by the blend hook's
own nil check, not by the evaluator.

Bound: internal/confidence/gate/gate_test.go:TestGate_ClosedBeforeFirstEvaluate

## RULE-GATE-WIRING-01: the blend hook reads d.Gate.Open() as wPredSystem; a nil Gate reads as open.

`smartblend.BuildFn`'s closure passes `d.Gate == nil || d.Gate.Open()`
as the aggregator's `wPredSystem` argument. A closed gate therefore
drives `aggregator.Tick` to force w_pred=0 (UIState "refused") and the
blend returns the reactive PWM; a nil gate (monitor-only, or tests
without the smart bundle) preserves the pre-gate behaviour.

Bound: internal/smartblend/blend_test.go:TestBlend_GateClosedForcesReactive

## RULE-GATE-DISABLE-01: Config.SmartDisabled() defaults false and, when true, forces the gate's smart_disabled term (§3.6 disable inheritance); Clone deep-copies the pointer.

`SmartConfig.Disabled` is a pointer-bool: nil/unset means smart mode
enabled (`SmartDisabled()` returns false); an explicit true forces the
`smart_disabled` gate term, closing the gate. `Config.Clone()`
deep-copies the pointer so a cloned config can be mutated without
aliasing the live one.

Bound: internal/config/smart_test.go:TestConfig_SmartDisabled
Bound: internal/config/clone_test.go:TestClone_SmartDisabledPointerUnaliased
