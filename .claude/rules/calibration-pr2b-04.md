# RULE-CALIB-PR2B-04: Stall PWM is detected for duty_0_255 channels via a descending sweep with step size 16.

`ProbeStall` first writes `pwmUnitMax` to establish a spinning baseline. It then descends in
steps of 16 until the first step where RPM drops to 0. The `StallPWM` field in
`ChannelCalibration` is set to the sweep value where RPM became 0 (within one step of the
true stall point). The sweep resolution of 16 is a balance between calibration duration and
accuracy; a sweep step of 1 would take 256 writes and 128 settle-waits on a 50ms-hint driver.
A NULL `StallPWM` indicates that the fan never stalled across the full sweep (fan-always-on
hardware) or that the channel is phantom.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/stall_pwm_detected_duty_0_255
