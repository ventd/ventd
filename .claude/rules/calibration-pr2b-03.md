# RULE-CALIB-PR2B-03: Polarity probe returns PolarityAmbiguous when |rpmAtHigh − rpmAtLow| < 200; channel is marked phantom.

`ProbePolarity` returns `PolarityAmbiguous` when neither the normal nor inverted threshold is
met. This outcome occurs for: physically disconnected headers, BIOS-locked fans that ignore
manual PWM, and driver sysfs channels that map to no physical fan (phantom channels on some
Super I/O chips). The probe orchestrator MUST mark `ChannelCalibration.Phantom = true` for
an ambiguous result so the apply path registers the channel as monitor-only.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/phantom_marked_from_ambiguous_polarity
