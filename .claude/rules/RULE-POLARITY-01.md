# RULE-POLARITY-01: Bipolar probe writes are exactly BipolarLowPWM then BipolarHighPWM for hwmon, BipolarLowPct then BipolarHighPct for NVML, and vendor-specific for IPMI.

`HwmonProber.ProbeChannel` writes `BipolarLowPWM` (51 ≈ 20% of 255)
followed by `BipolarHighPWM` (204 ≈ 80% of 255) to `ch.PWMPath` as the
bipolar stimulus before classifying. `NVMLProber.ProbeChannel` calls
`SetFanSpeed(gpuIdx, fanIdx, BipolarLowPct)` then
`SetFanSpeed(gpuIdx, fanIdx, BipolarHighPct)` — the NVML 20/80 pair.
`DellIPMIProbe` and `SupermicroIPMIProbe` use vendor-specific OEM write
primitives (deferred to v0.7+, see RULE-POLARITY-07).

The pre-#1110 algorithm was a single midpoint write (128 / 50%) compared
against the pre-write baseline RPM. That misclassified every normal fan
whose baseline PWM was already above midpoint: BIOS auto-curves
typically hold fans at PWM=180-255 going into the wizard, so the
midpoint write made fans slow down rather than speed up, producing
negative deltas and false-inverted classifications. See RULE-POLARITY-13
for the full bipolar contract; this rule pins the two specific PWM /
percentage values the probe must write.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-01_midpoint_write
