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

## RULE-OPP-PROBE-07: No fresh-install gate. The standard idle preconditions are the only protection against probing immediately after install.

**v0.5.30 behaviour change.** Prior: opportunistic probes were forbidden
within 24 h of `/var/lib/ventd/.first-install-ts` mtime. The 24 h gate
compressed the available excitation window — a fresh-install operator
who watched the dashboard for an hour saw "smart-mode warming up" with
no actual probes happening, because the scheduler refused every tick
with `ReasonOpportunisticBootWindow`. By the time the gate cleared,
the operator had given up.

Current: `FirstInstallDelay = 0`. `PastFirstInstallDelay(path, now)`
returns `true` immediately at any non-negative marker age. The
scheduler never refuses a tick with `ReasonOpportunisticBootWindow`
based on marker age.

The hard idle preconditions (RULE-OPP-IDLE-01 through RULE-OPP-IDLE-04)
are unchanged and remain the load-bearing protection against probing
during real workload: idle gate's 600 s durability, no active SSH, no
battery, no container, no scrub, no blocked process, ≥ 24 h post-resume
warmup. Those gates kept opportunistic probing safe before v0.5.30 and
keep it safe after.

`FirstInstallDelay`, `PastFirstInstallDelay`, and
`ReasonOpportunisticBootWindow` are kept (not removed) so a future
operator-tunable knob has a slot to hang on. The function is not dead
code; the constant is a reservation. A regression that flips the
constant back to a non-zero value re-introduces the silent-fail UX
and is caught by the bound subtests.

Bound: internal/probe/opportunistic/install_marker_test.go:FirstInstallDelay_constant_is_zero
Bound: internal/probe/opportunistic/install_marker_test.go:zero_age_marker_returns_past_true
Bound: internal/probe/opportunistic/install_marker_test.go:aged_marker_returns_past_true
Bound: internal/probe/opportunistic/install_marker_test.go:empty_path_returns_past_true_unchanged
Bound: internal/probe/opportunistic/scheduler_test.go:TestScheduler_FreshInstallGateDropped

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

## RULE-OPP-PROBE-13: Successful probes feed the signguard sample callback when wired; nil-callback is a clean no-op.

The opportunistic prober's `ProbeDeps.SignguardSampleFn` is the wire-up
hook that feeds v0.5.8's wrong-direction Layer-B prior detector
(`internal/coupling/signguard`). On every probe exit path that did NOT
set `EventFlag_ENVELOPE_C_ABORT`, FireOne MUST call the callback with
`(channelID = ch.PWMPath, deltaPWMSigned = sign(gapPWM − baseline),
deltaT = mean(lastTemps − firstTemps))` exactly once, immediately
after the observation record append. A nil callback MUST be skipped
without error — the daemon may be built without signguard wired
(e.g. before the wire-up PR) and the prober must not break.
A ctx.DeadlineExceeded return is NOT an abort and MUST still feed
signguard so production-length probes (which exit through the
holdEnd branch, not abort) accumulate votes.

The callback's `deltaT` is the mean over sensors present in BOTH the
first-tick and last-tick temp maps; signguard's noise-floor gate
(R11 §0, |ΔT| ≥ 2 °C) is enforced inside `Detector.Add`, so this
prober is intentionally permissive about ΔT magnitude.

Bound: internal/probe/opportunistic/prober_test.go:TestProber_FeedsSignguardOnSuccess
Bound: internal/probe/opportunistic/prober_test.go:TestProber_NilSignguardIsNoOp

## RULE-OPP-IDLE-01: OpportunisticGate durability in ModeStrictIdle MUST be 600 seconds.

The `opportunisticDurability` constant is locked at 10 minutes (2× the
v0.5.3 StartupGate window) per the v0.5.5 spec rationale and is
applied unconditionally when `OpportunisticGateConfig.Mode ==
ModeStrictIdle`. The test asserts the constant value directly so a
change without spec update fails CI.

**v0.6.0 amendment**: strict mode is no longer the default evaluator.
`OpportunisticGate` now dispatches on `Mode`: ModeStrictIdle preserves
the v0.5.x 600 s durability loop; ModeSoftIdle (the zero value,
default in v0.6.0+) skips the durability loop entirely. See
RULE-OPP-IDLE-SOFT-MODE for the soft evaluator's contract. The
constant and strict-mode behaviour are preserved so operators on
hosts where the soft thresholds prove too permissive can revert via
`--strict-idle-gate`.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_DurabilityIs600s

## RULE-OPP-IDLE-02: OpportunisticGate MUST refuse when any input IRQ has non-zero delta in the last evaluation window.

`evalInputIRQActivity` reads the current IRQ counters via
`cfg.IRQReader` (or `ReadIRQCounters(/proc/interrupts)`) and classifies
each via `cfg.IsInputIRQOverride` (or the default `IsInputIRQ` walk
over `/sys/kernel/irq/<id>/actions`). On a non-zero delta of any
classified input IRQ the gate refuses with `ReasonRecentInputIRQ`.

The check fires in **both** modes:

- **ModeStrictIdle**: the strict evaluator owns a loop-scoped
  `prevIRQ` that seeds on the first iteration of the durability
  loop and detects the delta on subsequent iterations within the
  600 s window.
- **ModeSoftIdle**: the soft evaluator reads
  `OpportunisticGateConfig.IRQBaseline` — a caller-owned counter
  snapshot pre-seeded by the scheduler. The scheduler initialises
  one zero-valued `IRQCounters` per scheduler-lifetime and passes
  the same pointer on every tick, so "any classified input IRQ has
  activity since the previous gate evaluation" reads naturally
  across the 60 s scheduler tick interval. A nil baseline (test
  scaffolding only) seeds locally and admits the first call.

Both tests inject a counter that ticks IRQ "1" upward and assert the
refusal reason. The soft-mode test additionally asserts the
in-place baseline advance so the next tick computes its delta vs the
updated counters.

Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_RefusesOnInputIRQDelta
Bound: internal/idle/opportunistic_test.go:TestOpportunisticGate_RefusesOnInputIRQDelta_SoftMode

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

## RULE-OPP-IDLE-SOFT-MODE: ModeSoftIdle (v0.6.0+ default) is single-shot with relaxed PSI thresholds; ModeStrictIdle reverts to the v0.5.x 600 s durability loop via `--strict-idle-gate`.

**v0.6.0 change**. Phase C5 HIL field-validation (issue #1024,
desktop + Proxmox soak verdicts) confirmed the v0.5.x strict
evaluator structurally prevents smart-mode from advancing under
realistic workload: the 600 s sustained-idle durability requirement +
the calibration-grade PSI thresholds (cpu.some avg60 > 1.0 %) closed
the gate > 99 % of ticks on hosts running Tdarr (desktop) or LXC
containers (Proxmox), producing zero Layer-B RLS updates over
~36 hours of cumulative observation. The hypothesis chain:

```
realistic workload (any class)
  → RULE-OPP-IDLE-01..04 closed > 99 % of ticks
  → opportunistic probes never fire (RULE-OPP-PROBE-01)
  → no Δpwm-on-i-while-zero-on-j events
  → RULE-CMB-OAT-01 admits zero samples to Layer-B RLS
  → Snapshot.WarmingUp stays true forever
  → predictive controller path is structurally locked out
```

v0.6.0 introduces the **soft-idle gate** as the new default. The
`IdleGateMode` enum on `OpportunisticGateConfig` selects between:

- **ModeSoftIdle (zero value, default)**: single-shot evaluation
  against the soft thresholds `softPSICpuCeiling = 10.0 %`,
  `softPSIIoCeiling = 10.0 %`, `softPSIMemCeiling = 0.5 %` (memory
  unchanged from strict — memory pressure is a physical signal
  workload lulls don't change). Loadavg fallback for kernels
  without PSI is `softLoadAvgPerCPU = 0.5 × ncpus` (vs strict
  `0.10 × ncpus`). The 600 s durability loop is dropped — the
  scheduler's 60 s tick cadence supplies the temporal envelope.

- **ModeStrictIdle**: the legacy v0.5.x evaluator (600 s
  durability + tight PSI thresholds). Operator escape hatch via
  the daemon CLI flag `--strict-idle-gate`.

The relaxed thresholds are calibrated against the v0.6 RFC #1024
desktop trace: cpu.some avg60 spent the majority of every 60 s
window between 2-8 % during Tdarr transcoding lulls (strict refuses
above 1.0 %; soft admits up to 10.0 %). 10 % is operationally
meaningful — a system where 10 % of tasks stalled on CPU in the
last 60 s is genuinely busy; 5-8 % is the "between transcoding
tasks" window the v0.6 ship plan targets.

**All hard guards remain unchanged in soft mode**:

- Hard preconditions (battery / container / scrub / blocked-process /
  post-resume warmup, RULE-OPP-IDLE-04) are checked first and
  refuse regardless of Mode.
- Process blocklist (RULE-IDLE-06) — `rsync`, `ffmpeg`, `make`,
  `apt`, etc. close the gate.
- Input IRQ delta (RULE-OPP-IDLE-02) — fires in both modes; soft
  uses caller-owned `IRQBaseline` for cross-tick state.
- Active SSH session (RULE-OPP-IDLE-03) — single-shot loginctl
  parse, same in both modes.

The mode is mutually exclusive: a daemon runs in exactly one mode
for its lifetime. The scheduler logs which mode is active at
construction time so operators can audit via journald.

The zero-value-is-soft contract is load-bearing: tests that
construct an `OpportunisticGateConfig` literal without setting
`Mode` exercise the soft evaluator. The bound subtest pins
`ModeSoftIdle = 0` and `ModeStrictIdle = 1` so a future regression
that flips the enum cannot silently revert the default.

`opportunisticDurability` (600 s) and the strict PSI constants
remain in place — they are not dead code, they are the strict-mode
contract preserved as an operator escape hatch.

The soft evaluator's single-shot guarantee is tested directly:
elapsed wall-clock < 500 ms from gate entry to gate return on a
clean fixture (vs ~600 s for the strict loop). A regression that
re-introduces a durability loop in the soft path fails the bound
subtest's elapsed-time assertion.

Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_AdmitsAtRelaxedThresholds
Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_RefusesAboveSoftPSICeiling
Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_AdmitsBetweenStrictAndSoftCeiling
Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_StrictModeStillRefusesAtSameLevel
Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_ModeConstants
Bound: internal/idle/opportunistic_test.go:TestSoftIdleGate_NilIRQBaselineAdmitsFirstCall

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
