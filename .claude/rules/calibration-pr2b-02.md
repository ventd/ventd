# RULE-CALIB-PR2B-02: Polarity probe classifies inverted polarity when rpmAtLow − rpmAtHigh ≥ 200 RPM.

`ProbePolarity` returns `PolarityInverted` when `rpmAtLow - rpmAtHigh ≥ 200`. An inverted
channel spins faster at low PWM than at high PWM — it87 on some Gigabyte boards and nct6683
on MSI boards exhibit this. The apply path MUST invert duty-cycle values via `InvertPWM`
before writing them to the sysfs PWM file. Failing to invert produces the opposite of the
requested speed: asking for minimum speed drives the fan to maximum and vice versa.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/polarity_inverted_detected
