# RULE-CALIB-PR2B-01: Polarity probe classifies normal polarity when RPM delta ≥ 200 RPM at 80% vs 20% PWM.

`ProbePolarity` writes 20% of `pwmUnitMax` to the channel, waits `latency*3`, reads RPM as
`rpmAtLow`; then writes 80%, waits, reads `rpmAtHigh`. When `rpmAtHigh - rpmAtLow ≥ 200`, the
function returns `PolarityNormal`. A normal-polarity classification enables the apply path to
write PWM values without inversion. A delta below the threshold is not a valid normal-polarity
classification — rounding errors, slow motor response, or BIOS intervention can all produce a
small delta on a physically-normal fan.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/polarity_normal_detected
