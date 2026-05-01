# Opportunistic active probing rules (v0.5.5)

These invariants govern the v0.5.5 Layer A gap-fill probing subsystem.
Violating them risks: probing a busy system (skews learning), probing
during user activity (annoying), failing to restore the controller-
managed PWM after a probe (unsafe), or letting bit 13 of the
observation log's event-flags collide with future schema additions.

Each rule binds 1:1 to a subtest in `internal/probe/opportunistic/` or
`internal/idle/`. If a rule text is edited, update the corresponding
subtest in the same PR. If a new rule lands, it must ship with a
matching subtest or rulelint blocks the merge.

The parent design lives in `specs/spec-smart-mode.md` §6.4 + §7.4.
The patch spec is `specs/spec-v0_5_5-opportunistic-probing.md`.

## RULE-OPP-PROBE-01: Probe MUST fire only when OpportunisticGate returns true.

The scheduler invokes `idle.OpportunisticGate(ctx, cfg)` once per tick.
On `false` the scheduler must not call `FireOne`. The injected
`LoginctlOutput` and `IRQReader` give tests deterministic control over
gate refusal; the test asserts that with a refusing gate the prober's
`WriteFn` records zero invocations.

Bound: internal/probe/opportunistic/prober_test.go:TestScheduler_FiresOnlyAfterGatePasses

## RULE-OPP-PROBE-02: Probe duration MUST be 30 ± 5 seconds; no PWM sweep within a single probe.

`ProbeDuration` constant is locked at 30 s and `ProbeJitterTolerance` at
5 s. The prober writes a single PWM value and holds it for the
duration; sweeping or stepping within one fire is forbidden. Live HIL
verification compares wall-clock elapsed against the constants; the
unit test asserts the constants themselves are unchanged.

Bound: internal/probe/opportunistic/prober_test.go:TestProber_DurationWithinTolerance

## RULE-OPP-PROBE-03: At most one opportunistic probe in flight system-wide.

The scheduler's `runActive` atomic flag must be `false` before `tick()`
returns and `true` only while `FireOne` is executing. Concurrent
`tick` calls would otherwise race a second probe into the same fan
domain. Cross-channel parallelism is unsafe in v0.5.5 and is deferred
until v0.5.7's Layer B coupling map.

Bound: internal/probe/opportunistic/scheduler_test.go:TestScheduler_OneChannelAtATime

## RULE-OPP-PROBE-04: All PWM writes MUST route through polarity.WritePWM; direct sysfs writes are forbidden.

The prober's `WriteFn` is called only via `polarity.WritePWM(ch, v, fn)`
which inverts the byte for inverted-polarity channels and refuses
phantom/unknown channels. Bypassing this gateway means an inverted fan
runs in the wrong direction during the probe — every observation that
results is meaningless. The test exercises an inverted-polarity
channel and asserts the underlying `WriteFn` sees `255-gap` (not `gap`).

Bound: internal/probe/opportunistic/prober_test.go:TestProber_RoutesViaPolarityWrite

## RULE-OPP-PROBE-05: Probe MUST abort on envelope.LookupThresholds(class) thresholds being exceeded; restoration MUST complete before the function returns.

`abortOnSlope` and `abortOnAbsolute` mirror the v0.5.3 envelope abort
logic. On either trip the prober returns `ErrProbeAborted` and the
deferred restore writes the controller-managed baseline. The test
seeds a SensorFn that returns a temperature exceeding the absolute
ceiling on the second sample and asserts (a) `FireOne` returns the
abort error, and (b) the captured writes end with the baseline byte.

Bound: internal/probe/opportunistic/prober_test.go:TestProber_AbortPath_RestoresController

## RULE-OPP-PROBE-06: A PWM bin with a non-aborted observation record (any event_flags bit) within 7 days MUST NOT be re-probed.

The `Detector.Gaps` walk excludes bins whose most recent record is
inside `CooldownWindow` (7 d), unless that record carries both
`EventFlag_OPPORTUNISTIC_PROBE` AND `EventFlag_ENVELOPE_C_ABORT` (an
aborted opportunistic probe — bin remains eligible for retry). The
test seeds a synthetic log with bins inside and outside the window,
plus an aborted opportunistic record, and asserts the returned set.

Bound: internal/probe/opportunistic/detector_test.go:TestDetector_ExcludesBinsWithin7Days

## RULE-OPP-PROBE-07: First probe MUST NOT fire within 24 hours of /var/lib/ventd/.first-install-ts mtime.

`PastFirstInstallDelay(path, now)` returns `false` while the marker
file's age is below `FirstInstallDelay` (24 h). The scheduler refuses
the tick with `ReasonOpportunisticBootWindow`. The test creates a
fresh marker, ticks the scheduler with `now = marker + 2h`, and
asserts no probe fired.

Bound: internal/probe/opportunistic/scheduler_test.go:TestScheduler_FirstProbeDelayedBy24h

## RULE-OPP-PROBE-08: Probe MUST refuse when Config.NeverActivelyProbeAfterInstall == true.

The scheduler reads the toggle via `cfg.Disabled()`. The test wires
a `Disabled` callback that returns `true` and asserts the prober's
`WriteFn` is never called and `Status().LastReason` reports the
`opportunistic_disabled` reason.

Bound: internal/probe/opportunistic/scheduler_test.go:TestScheduler_HonoursToggleOff

## RULE-OPP-PROBE-09: Probe MUST refuse on channels where Config.Controls[].Mode == "manual".

The scheduler reads per-channel manual-mode state via `cfg.IsManualMode`.
The test wires the callback to return `true` for every channel and
asserts no probe fires. Manual-mode channels' learned state is
preserved per spec-smart-mode §7.4.

Bound: internal/probe/opportunistic/scheduler_test.go:TestScheduler_RefusesManualModeChannels

## RULE-OPP-PROBE-10: On every exit path the probe MUST restore the controller-managed PWM value via polarity.WritePWM.

`FireOne` registers a `defer` that writes the baseline PWM to `WriteFn`
through `polarity.WritePWM` on success, abort, ctx-cancel, AND panic
exit paths. Two tests exercise this: a clean completion (deadline-
exceeded ctx), and a context-cancel race. Both assert the captured
writes end with the baseline byte regardless of the error path taken.

Bound: internal/probe/opportunistic/prober_test.go:TestProber_FullCycle_RestoresController
Bound: internal/probe/opportunistic/prober_test.go:TestProber_CtxCancel_RestoresController

## RULE-OPP-PROBE-11: Each probe emits exactly one observation record with EventFlag_OPPORTUNISTIC_PROBE bit set.

The prober's deferred record append always includes
`EventFlag_OPPORTUNISTIC_PROBE`. Aborts also set
`EventFlag_ENVELOPE_C_ABORT`. The test injects an `ObsAppend`
callback, runs `FireOne`, and asserts the captured record's
`EventFlags` carry the expected bit.

Bound: internal/probe/opportunistic/prober_test.go:TestProber_EmitsRecordWithProbeFlag

## RULE-OPP-PROBE-12: Probe grid MUST be 8 raw PWM units between 0 and 96 inclusive, 16 raw PWM units between 97 and 255 inclusive. Stall-PWM and min-spin MUST be probed when in a gap, regardless of grid spacing.

`ProbeGrid(knowns)` returns the union of the 8/16 grid plus the
caller's stall-PWM and min-spin anchors. The test asserts (a) every
low-half value is multiple of 8 and ≤ 96, (b) every high-half value
is on the 16-stride from 97, and (c) anchors at unaligned PWM
positions still appear in the grid.

Bound: internal/probe/opportunistic/detector_test.go:TestDetector_LowHighGridSpacing
Bound: internal/probe/opportunistic/detector_test.go:TestDetector_AnchorsStallAndMinSpin

## RULE-OPP-IDLE-01: OpportunisticGate durability MUST be 600 seconds.

The `opportunisticDurability` constant is locked at 10 minutes (2× the
v0.5.3 StartupGate window) per the v0.5.5 spec rationale. The test
asserts the constant value directly so a change without spec update
fails CI.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_DurabilityIs600s

## RULE-OPP-IDLE-02: OpportunisticGate MUST refuse when any input IRQ has non-zero delta in the last 60 seconds.

`evalInputIRQActivity` reads two snapshots from `cfg.IRQReader` and
classifies via `cfg.IsInputIRQOverride` (or the default
`IsInputIRQ` walk over `/sys/kernel/irq/<id>/actions`). On a non-zero
delta of any classified input IRQ the gate refuses with
`ReasonRecentInputIRQ`. The test injects a counter that ticks IRQ "1"
upward and asserts the refusal reason.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_RefusesOnInputIRQDelta

## RULE-OPP-IDLE-03: OpportunisticGate MUST refuse when any Remote=yes Active=yes IdleSinceHint <= 60s session is present.

`HasRecentSSHActivityFromOutput` parses loginctl JSON and returns
`true` when any session has `state=active`, `remote=true`, and
`idle=false`. Long-idle SSH (`tmux attach`) does NOT trigger the
refusal. Two tests cover the positive (active session refuses) and
negative (idle session does not) branches.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_RefusesOnActiveSSH
Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_AcceptsLongIdleSSH

## RULE-OPP-IDLE-04: OpportunisticGate MUST inherit all hard preconditions from StartupGate unchanged.

`evalPredicate` (the StartupGate predicate routine) is called by
OpportunisticGate before any opportunistic-specific checks. Battery,
container, scrub-active, blocked-process, boot warmup, and post-
resume warmup all refuse opportunistically just as they refuse
StartupGate. The test fixture supplies an on-battery `/sys` and
asserts the gate refuses with `ReasonOnBattery`.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_HardPreconditionsInherited

## RULE-OPP-OBS-01: SchemaVersion constant MUST be 2 once this patch ships. Reader MUST accept v1 records as forward-compatible (no field changes).

`schemaVersion = 2` and `schemaV1Min = 1` in
`internal/observation/schema.go`. `Reader.Stream` accepts headers in
the closed range [schemaV1Min, schemaVersion]. Two tests cover this:
one asserts a v1-header file streams correctly via a v2 reader; the
other asserts the constant values themselves are 2 and 1.

Bound: internal/observation/record_test.go:TestSchemaV2_BackwardCompatibleRead
Bound: internal/observation/record_test.go:TestSchemaV2_WriterEmitsV2

## RULE-OPP-OBS-02: EventFlag_OPPORTUNISTIC_PROBE = 1 << 13. The bit MUST NOT collide with any v0.5.4 reserved bit.

The new flag occupies bit 13, which v0.5.4 explicitly reserved per
RULE-OBS-SCHEMA-05. The reserved-mask in `eventFlagReservedMask` now
covers bits 14–31. The test asserts (a) the new flag's bit position,
and (b) its bit does not overlap any v1-era flag.

Bound: internal/observation/record_test.go:TestEventFlag_ProbeBitDoesNotCollide
