# RULE-CALIB-PR2B-10: step_0_N stall detection uses binary search; convergence in ≤ ceil(log2(N+1)) + 1 samples.

`ProbeStallStep` uses binary search over the range [0, pwmUnitMax] to find the minimum step
where RPM > 0 (min_responsive_pwm). The stall_pwm is min_responsive − 1. For an 8-level fan
(pwmUnitMax=7), binary search converges in ≤ 3 probe samples. A linear sweep (ProbeStall) on
a step_0_N fan is incorrect — each step write and settle takes `polling_latency_hint * 3`, and
a 16-level fan would require 16 writes instead of 4. Drivers in this category include
thinkpad_acpi (levels 0..7), dell-smm-hwmon (cooling_level 0..N), and steamdeck-hwmon
(0..255 discrete). The test verifies binary search for a 7-step fan produces the correct
stall=0, min_responsive=1 result in ≤ 6 samples.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/step_0N_stall_binary_search
