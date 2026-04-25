# RULE-CALIB-PR2B-07: ShouldApplyCurve returns ErrPhantom for phantom channels; writes are unconditionally refused.

`hwdb.ShouldApplyCurve(ch *ChannelCalibration)` returns `(false, ErrPhantom)` when
`ch.Phantom == true`. The controller apply path (`writeWithRetry`) calls
`ShouldApplyCurve` before every `backend.Write` and returns immediately on a non-nil
error — no PWM is written, no transient retry is attempted. Phantom channels represent
sysfs PWM entries with no physical fan behind them; writing any value is a no-op at best
and can interfere with the BIOS auto-curve at worst. `ErrPhantom` is a permanent skip
condition, not a retryable error.

Bound: internal/controller/controller_test.go:TestWriteWithRetry_RefusesPhantom
