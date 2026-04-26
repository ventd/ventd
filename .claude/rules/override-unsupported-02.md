# RULE-OVERRIDE-UNSUPPORTED-02: Calibration phase skips autocurve generation when the resolved profile has `overrides.unsupported: true`.

`hwdb.ShouldSkipCalibration(ecp *EffectiveControllerProfile) bool` MUST return `true` when
`ecp.Unsupported == true`, and `false` otherwise. The calibration orchestrator MUST call this
function before entering the probe sweep and skip the entire calibration pipeline (polarity
probe, stall sweep, BIOS-override detection) when it returns true. Skipping is correct
because `unsupported: true` signals that no Linux fan-control driver path exists for this
board; running calibration would attempt PWM writes that the OS either silently ignores or
returns EPERM for, wasting time and producing garbage calibration records. Sensor reads
(telemetry-only mode) are unaffected by this flag.

Bound: internal/hwdb/profile_v1_1_test.go:TestCalibration_UnsupportedSkipsAutocurve
