# RULE-CALIB-PR2B-05: MinResponsivePWM is set to the sweep step immediately above the detected stall point.

`ProbeStall` sets `MinResponsivePWM` to the last sweep step where RPM > 0 just before RPM
dropped to 0. This is the lowest PWM value the apply path can use with confidence that the fan
will spin. The `MinPWM` config field for the fan is overridden by `MinResponsivePWM` when
calibration data is present, preventing writes below the observed spin threshold. A
`MinResponsivePWM` of 0 is valid and means the fan spins at the minimum sweep step; a NULL
means phantom or the sweep never found a transition.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/min_responsive_pwm_detected
