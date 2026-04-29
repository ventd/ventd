# RULE-POLARITY-03: Classification thresholds — hwmon |delta| < 150 RPM → phantom; NVML |delta| < 10 pct → phantom.

`HwmonProber.ProbeChannel` computes `delta = observedRPM - baselineRPM`. When
`math.Abs(delta) >= ThresholdRPM (150)` and `delta > 0`, polarity is `"normal"`. When
`delta < 0`, polarity is `"inverted"`. When `math.Abs(delta) < ThresholdRPM`, polarity is
`"phantom"` with `PhantomReason = PhantomReasonNoResponse`. The same logic applies to
`NVMLProber` using `ThresholdPct (10)` on percentage-point deltas. A threshold below the
noise floor of a stopped or BIOS-locked fan produces false normal/inverted classifications;
150 RPM (hwmon) and 10 pct (NVML) are empirically derived from field data in the polarity
disambiguation research notes.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-03_threshold_boundary
