# RULE-ENVELOPE-02: Baseline PWM is captured before the first step write and restored on every exit path via defer.

`probeChannel` captures the current PWM value from `ch.PWMPath` as `baselinePWM` before
writing any step value. A `defer` placed immediately after the capture restores `baselinePWM`
via `cw.writeFunc` on every exit path: normal completion, thermal abort, context cancellation,
and write error. The defer fires before the function returns, ensuring the fan never stays at
the last probe step value after the probe ends. A probe that exits without restoring leaves the
fan at whatever step value (often 40–55 PWM) was being held at the abort moment, which is
incorrect and may be below the running operating point.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_02_BaselineRestoreAllExitPaths
