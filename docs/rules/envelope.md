# Envelope probing rules

These invariants govern the Envelope C / Envelope D thermal-probe state
machine in `internal/envelope/`. Envelope C drives PWM upward from
baseline through a class-tuned step table, recording RPM and temperature
at each step. Envelope D is the recovery / fallback path used when
Envelope C aborts on a thermal gate or finds no headroom. Together they
characterise the safe operating envelope of a fan channel before the
calibration sweep computes a curve.

Each rule below binds 1:1 to a subtest in `internal/envelope/`. If a
rule text is edited, update the binding subtest in the same PR; if a
new rule lands, it must ship with a matching subtest or `tools/rulelint`
blocks the merge.

## RULE-ENVELOPE-01: All PWM writes during envelope probing MUST go through polarity.WritePWM — never direct sysfs writes.

The envelope prober owns a `channelWriter` per channel.
`channelWriter.Write(value uint8)` calls
`polarity.WritePWM(ch, value, writeFunc)` which enforces the polarity
correction and phantom/unknown channel guards. Any code path that
writes directly to the sysfs PWM file (bypassing `polarity.WritePWM`)
violates the inverted-channel contract: the fan runs in the wrong
direction and every RPM/temperature reading produced during the probe
is meaningless. The test injects a phantom and an unknown channel and
asserts that Write returns ErrChannelNotControllable and
ErrPolarityNotResolved respectively, with zero bytes written to the
underlying writeFunc.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_01_WritePWMViaHelper
Bound: internal/envelope/envelope_test.go:phantom_refused
Bound: internal/envelope/envelope_test.go:unknown_refused
Bound: internal/envelope/envelope_test.go:normal_passes

## RULE-ENVELOPE-02: Baseline PWM is captured before the first step write and restored on every exit path via defer.

`probeChannel` captures the current PWM value from `ch.PWMPath` as
`baselinePWM` before writing any step value. A `defer` placed
immediately after the capture restores `baselinePWM` via
`cw.writeFunc` on every exit path: normal completion, thermal abort,
context cancellation, and write error. The defer fires before the
function returns, ensuring the fan never stays at the last probe step
value after the probe ends. A probe that exits without restoring
leaves the fan at whatever step value (often 40–55 PWM) was being
held at the abort moment, which is incorrect and may be below the
running operating point.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_02_BaselineRestoreAllExitPaths

## RULE-ENVELOPE-03: ClassThresholds lookup returns the correct Thresholds struct for every SystemClass including ClassUnknown.

`LookupThresholds(cls sysclass.SystemClass) Thresholds` MUST return
the canonical `Thresholds` for each of the seven non-Unknown classes.
For `ClassUnknown` (or any unrecognized class value), it MUST return
the `ClassMidDesktop` thresholds as the safe default — MidDesktop is
the statistically most common class and its thresholds are the most
conservative of the consumer desktop classes. A missing or zero-value
Thresholds for any class would cause division by zero (SampleHz=0)
or an empty PWMSteps slice that produces no probe writes.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_03_ClassThresholdLookup

## RULE-ENVELOPE-04: dT/dt thermal abort fires when temperature rise rate exceeds DTDtAbortCPerSec for the class.

`thermalAbort(temps map[string]float64, prev map[string]float64, dt time.Duration, thr Thresholds) bool`
returns true when any sensor's temperature delta (current − previous)
divided by `dt.Seconds()` exceeds `thr.DTDtAbortCPerSec`. The check
is skipped for NAS (ClassNASHDD) systems which use
`DTDtAbortCPerMin` over a longer window instead. At the abort
boundary: a delta of exactly `DTDtAbortCPerSec * dt.Seconds()` must
NOT abort (boundary is exclusive); a delta strictly above it MUST
abort. The test injects synthetic temperature maps with precisely
the boundary value and one step above to verify the
exclusive/inclusive behaviour.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_04_DTDtTripBoundary

## RULE-ENVELOPE-05: Absolute temperature abort fires when any sensor exceeds Tjmax minus TAbsOffsetBelowTjmax.

`absoluteTempAbort(temps map[string]float64, tjmax float64, thr Thresholds) bool`
returns true when any sensor reading exceeds
`tjmax - thr.TAbsOffsetBelowTjmax`. For ClassNASHDD the threshold is
`thr.TAbsAbsolute` rather than a Tjmax-relative value (NAS drives
have a fixed `TAbsAbsolute: 50.0` regardless of CPU Tjmax). At the
boundary: a reading equal to `tjmax - TAbsOffsetBelowTjmax` does NOT
abort; a reading one ULP above it MUST abort. The test uses a
synthetic Tjmax of 100°C with offset 15°C (boundary at 85°C) and
verifies that 84.9°C does not abort while 85.1°C does.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_05_TAbsTripBoundary

## RULE-ENVELOPE-06: Ambient headroom precondition refuses Envelope C when ambient ≥ (Tjmax − AmbientHeadroomMin).

`ambientHeadroomOK(ambient, tjmax float64, thr Thresholds) bool`
returns true when `ambient < tjmax - thr.AmbientHeadroomMin`. When
the ambient sensor reading leaves fewer than `AmbientHeadroomMin`
degrees between ambient and Tjmax, the thermal headroom is
insufficient to safely run the Envelope C sweep without risking an
absolute-temperature abort mid-step. The gate is evaluated once
before the first step write and cached for the probe run. The test
verifies the boundary: for Tjmax=100°C and AmbientHeadroomMin=60°C,
ambient=39.9°C passes (100-39.9=60.1>0) and ambient=40.0°C fails
(100-40=60 ≤ 0).

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_06_AmbientHeadroomPrecondition

## RULE-ENVELOPE-07: Envelope C thermal abort transitions to Envelope D; KV state reflects the ordering and abort reason.

When Envelope C aborts due to a thermal event (dT/dt or T_abs gate),
`probeC` MUST:
1. Persist KV state `calibration.envelope.<channel_id>.state = "aborted_C"`
   with the abort reason.
2. Immediately invoke `probeD` for the same channel.
3. After Envelope D completes (or itself aborts), persist the final
   state as `"complete_D"` or `"aborted_C"`.

The KV entry's `envelope` field transitions from `"C"` to `"D"`
when Envelope D begins. The test injects a thermal abort at the
third PWM step of Envelope C and verifies: (a) KV shows
`aborted_C`, (b) Envelope D proceeds from baseline upward, (c) final
KV shows `complete_D`. A missed KV transition means the web UI
reports Envelope C success on a thermally-constrained system that
should show the fallback result.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_07_AbortCToProbeD_OrderingPersist

## RULE-ENVELOPE-08: Envelope D (ramp-up) only writes PWM values ≥ baseline; writes below baseline are refused.

`probeD` begins from `baselinePWM` and only steps upward through
`thr.PWMSteps`. Any step value in `PWMSteps` that is strictly below
`baselinePWM` MUST be skipped without writing. The function MUST NOT
write a PWM value lower than the baseline under any circumstance —
doing so would make the fan slower during a thermal recovery, the
opposite of the safety intent. The test injects a baseline of 140
PWM with a step table of [180, 140, 110, 90] and verifies that only
180 is written (140 is skipped as equal to baseline, 110 and 90 are
skipped as below).

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_08_EnvelopeDRefusesBelowBaseline

## RULE-ENVELOPE-09: Probe is resumable from the last completed step after a daemon restart.

`LoadChannelKV(db *state.KVDB, channelID string) (ChannelKV, bool)`
reads the persisted envelope state for the channel. When
`state == "probing"` and `completed_step_count > 0`, `Prober.Probe`
MUST resume from `completed_step_count` (skipping already-completed
steps) rather than restarting from step 0. A channel in state
`"complete_C"` or `"complete_D"` MUST be skipped entirely — no
re-probe. State `"aborted_C"` proceeds directly to Envelope D. The
test serialises a mid-run KV state with `completed_step_count=3`
and verifies that the probe writes only steps 4..N with no repeated
write to steps 1..3.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_09_StepLevelResumability

## RULE-ENVELOPE-10: Every probe step event is appended to the LogStore as a msgpack-encoded StepEvent with schema_version=1.

`appendStepEvent(db *state.LogDB, ev StepEvent) error` marshals `ev`
using `github.com/vmihailenco/msgpack/v5` and calls
`db.Append("envelope", payload)`. The `StepEvent` struct MUST
include all fields: `SchemaVersion` (always 1), `ChannelID`,
`Envelope` ("C" or "D"), `EventType` (one of the seven event-type
constants), `TimestampNs`, `PWMTarget`, `PWMActual`, `Temps`, `RPM`,
`ControllerState`, `EventFlags`, and `AbortReason`. A round-trip
test marshals a fully-populated StepEvent and unmarshals it back,
asserting field-for-field equality. A missing field in the
serialised form breaks post-hoc analysis of the diagnostic bundle.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_10_LogStoreSchemaConformance

## RULE-ENVELOPE-11: Channels are probed sequentially — never concurrently.

`Prober.Probe(ctx, channels)` iterates over channels in index order,
fully completing (or aborting) each channel's Envelope C/D probe
before advancing to the next. No goroutine is spawned per channel.
Concurrent probing is forbidden because simultaneous PWM writes on
multiple channels produce interfering thermal gradients that
invalidate the steady-state RPM readings used to determine the
envelope curve. The test injects three channels and records the
sequence of write calls; it asserts that all writes for channel 0
precede all writes for channel 1, which precede all writes for
channel 2 — no interleaving.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_11_SequentialChannelsNoParallel

## RULE-ENVELOPE-12: A channel in state "paused_*" re-runs the idle.StartupGate before resuming the probe.

When the daemon restarts and finds a channel KV with `state` in
{"paused_user_idle", "paused_thermal", "paused_load"},
`Prober.Probe` MUST call `idle.StartupGate` again before resuming.
The gate enforces that the pause condition has cleared before
committing to the next step. If `StartupGate` returns `ok=false`,
the channel is re-paused (KV state updated) and the probe for that
channel stops. If `ok=true`, the probe resumes from
`completed_step_count`. The test injects a paused channel and a
mock `StartupGate` that returns ok=false on the first call and
ok=true on the second, then verifies the probe does not write on
the first daemon start but does resume on the second.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_12_PausedStateReruns_StartupGate

## RULE-ENVELOPE-13: When Envelope D cannot produce a safe curve (all steps below baseline), the wizard falls back to monitor-only mode.

`probeD` returns `ErrEnvelopeDInsufficient` when every step in
`thr.PWMSteps` is ≤ `baselinePWM` — meaning there is no headroom to
probe above baseline. This error MUST be propagated to
`Prober.Probe`, which sets the channel's KV state to `"aborted_C"`
(unchanged), logs a WARN, and returns the error to the wizard
orchestrator. The wizard treats this error as equivalent to an
OutcomeMonitorOnly decision for that channel: it is excluded from
the generated fan curve. The test constructs a baseline of 200
(maximum PWM for server class) with a step table of
[200, 170, 140, 120, 100] and verifies ErrEnvelopeDInsufficient is
returned.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_13_UniversalDInsufficient_WizardFallback

## RULE-ENVELOPE-14: PWM readback after each step write must match the written value within ±2 LSB.

After `cw.Write(stepValue)` returns nil, `readPWM(ch.PWMPath)` MUST
be called and the readback compared to `stepValue`. A discrepancy of
more than 2 LSB indicates BIOS override or driver rounding and MUST
cause the step to be logged with `EventFlags |= FlagBIOSOverride`
and the channel's abort path to be triggered. The ±2 LSB tolerance
accommodates drivers that round to even values (nct6775 rounds pwm
to multiples of 2.56, equivalent to ±1 after integer truncation). A
readback that silently diverges by more than 2 LSB means
calibration continues on data that does not reflect actual fan
response.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_14_PWMReadbackVerification
