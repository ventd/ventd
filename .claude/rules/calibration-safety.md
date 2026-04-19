# Calibration Safety Rules

These invariants govern the calibration layer that probes and characterises
fan hardware. Violating them risks inaccurate calibration results, missed
fan-sensor correlation, or uncontrolled escalation on zero-PWM hold.

Each rule is bound to one or more subtests in `internal/calibrate/`. If a
rule text is edited, update the corresponding subtest in the same PR. If a
new rule lands, it must ship with a matching subtest or rulelint blocks the
merge.

## RULE-CAL-ZERO-FIRES: sentinel fires escalation after max 2s when PWM is held at 0

Holding a fan at duty-cycle zero for longer than `ZeroPWMMaxDuration` (2 s)
risks stalling a rotor that was not declared fan-stop-safe. The
`ZeroPWMSentinel` must invoke the escalation callback exactly once after
the deadline elapses, and must not fire before the deadline. A calibration
sweep that holds PWM=0 for measurement purposes has a 2 s window before the
sentinel intervenes and forces a safe recovery PWM.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_ZeroFiresAfterTwoSeconds

## RULE-CAL-ZERO-CANCEL: a non-zero Set before the deadline cancels escalation

Calling `Set(v)` with v > 0 while a zero-hold timer is pending must stop
that timer and prevent the escalation callback from firing. This allows a
calibration sweep to safely pulse through zero momentarily and then ramp
back up without triggering false escalation. The cancel must take effect
even when the non-zero Set arrives immediately before the deadline.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_NonZeroBeforeDeadlineCancels

## RULE-CAL-ZERO-REARM: a second Set(0) after a cancel re-arms with a fresh 2s clock

After a non-zero cancel resets the sentinel, a subsequent `Set(0)` must
start a new 2 s countdown from the moment of that call -- not from the
original zero-hold start. A sentinel that uses a stale deadline after a
cancel/re-arm cycle would fire too early, aborting a sweep that had
legitimately restarted the zero-hold phase.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_ReArmAfterCancel

## RULE-CAL-ZERO-STOP: Stop cancels any pending escalation; subsequent Set(0) is a no-op

`Stop()` must cancel any in-flight escalation timer and permanently disable
the sentinel so that future `Set(0)` calls are no-ops. This is the clean
shutdown contract for a calibration session that terminates before its zero
hold would time out. A sentinel that fires after `Stop()` -- or that allows
re-arming after `Stop()` -- can escalate during daemon shutdown when the
sentinel is being torn down.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_StopPreventsEscalation

## RULE-CAL-ZERO-RACE: concurrent Sets are race-safe; no spurious escalation

A fan flapping between 0 and a non-zero value under concurrent goroutines
must not produce spurious escalation calls. The sentinel must serialise
timer arm/cancel operations so that the final observable state (the last
`Set` value) determines whether escalation fires. This is verified under
`-race`; a data race in the arm/cancel path can corrupt timer state and
fire escalation for a non-zero final value.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_ConcurrentSetsSafeUnderRace

## RULE-CAL-ZERO-STOP-IDEMPOTENT: Stop is idempotent; double-Stop must not panic

Calling `Stop()` a second time on an already-stopped sentinel must be a
safe no-op. The daemon's deferred teardown and a calibration abort racing
at shutdown can both call `Stop()` on the same sentinel; the second call
must not panic or corrupt internal state.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_StopIsIdempotent

## RULE-CAL-ZERO-DURATION: ZeroPWMMaxDuration is exactly 2s; SafePWMFloor is in [20, 80]

The `ZeroPWMMaxDuration` constant must equal `2 * time.Second` -- the value
documented in the README and user-facing calibration guide. Silently
widening this window past the documented bound would violate the safety
promise made to users. `SafePWMFloor` must be in the range [20, 80]: high
enough to spin most fans above stall speed, low enough to remain quiet
during a measurement sweep.

Bound: internal/calibrate/safety_test.go:TestZeroPWMSentinel_TimingTighterThanReadmePromise

## RULE-CAL-DETECT-HAPPY: the RPM sensor that correlates with PWM ramp is selected over flat siblings

`DetectRPMSensor` must return the `fan*_input` file whose RPM rises in
response to an increasing PWM ramp, ignoring siblings whose readings remain
flat over the same sweep. Selecting a flat sibling would bind the controller
to the wrong sensor, causing the closed-loop algorithm to chase a reading
unaffected by its own PWM writes and producing thermal runaway or a stuck
fan.

Bound: internal/calibrate/detect_test.go:TestDetectRPMSensor_HappyPath

## RULE-CAL-DETECT-NO-WINNER: when no sensor crosses the noise floor, result is (empty path, nil error)

When all `fan*_input` siblings stay below the minimum RPM-delta threshold
across the PWM sweep -- indicating that none of the sensors tracks this
channel -- `DetectRPMSensor` must return an empty `RPMPath` with a nil
error. An error return would be misinterpreted as a driver fault; an
empty-path nil return is the explicit "detection ran, no winner" contract
that the caller checks before proceeding to calibration.

Bound: internal/calibrate/detect_test.go:TestDetectRPMSensor_NoCorrelation

## RULE-CAL-DETECT-NVIDIA-REJECT: Nvidia fans are refused at entry; error must identify the branch

`DetectRPMSensor` must reject a `config.Fan` whose `Type` is `"nvidia"`
before performing any sysfs I/O. NVML fans have no `hwmon` RPM sensor to
probe; attempting a sweep would write to an unrelated hwmon path or silently
return an empty result. The error message must contain the substring
`"nvidia fans do not use hwmon"` so that a future change to the early-exit
message cannot silently drop the guard without failing this assertion.

Bound: internal/calibrate/detect_test.go:TestDetectRPMSensor_RejectsNvidia

## RULE-CAL-DETECT-NO-FILES: an hwmon dir with no fan*_input files returns an error, not misleading empty success

When the hwmon chip directory associated with the PWM path contains no
`fan*_input` files, `DetectRPMSensor` must return a non-nil error (message
containing `"no fan"`). Returning an empty-path nil result in this case
would be indistinguishable from a legitimate "no winner" outcome and would
suppress the user-visible diagnostic that the hardware configuration is
missing fan tachometer files.

Bound: internal/calibrate/detect_test.go:TestDetectRPMSensor_NoFanInputFiles

## RULE-CAL-DETECT-CONCURRENT: a second concurrent DetectRPMSensor on the same PWM path returns "already running"

Only one `DetectRPMSensor` sweep may run per PWM path at a time. A second
concurrent call against the same path must return an error indicating the
path is already under detection. This prevents double-sweep interference
(two goroutines ramping the same PWM simultaneously) that would produce
corrupted RPM readings and potentially dangerous duty-cycle oscillation.
The guard is the production-path protection against a user double-clicking
the "Detect sensor" button in the setup wizard.

Bound: internal/calibrate/detect_test.go:TestDetectRPMSensor_ConcurrentCall_Rejected
