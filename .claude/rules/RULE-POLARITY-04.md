# RULE-POLARITY-04: Baseline PWM is restored on every exit path — write failure, context cancel, and normal return.

`HwmonProber.ProbeChannel` captures `baselinePWM` from `ch.PWMPath` before any write.
A `defer` restores `baselinePWM` via `p.writeFile` and waits `RestoreDelay` unless the
probe has already written the restore explicitly. The restore fires on: (a) write failure
(phantom path), (b) context cancellation (ctx.Done() check before and after HoldDuration),
and (c) normal classification return. `NVMLProber.ProbeChannel` restores both the fan speed
and the fan control policy. A probe that exits without restoring leaves the fan at the midpoint
write value (128/255 or 50%) indefinitely, which is incorrect and audible.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-04_restore_on_all_paths
