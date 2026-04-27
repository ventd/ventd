# RULE-POLARITY-02: Hold time after the midpoint write MUST be exactly HoldDuration (3s) ± 200ms across all backends.

`HwmonProber.ProbeChannel` calls `p.clock()(HoldDuration)` after writing the midpoint PWM
value, before sampling the observed RPM. The test verifies this by injecting a clock
accumulator and asserting that `totalSleep >= HoldDuration - 200ms` after `ProbeChannel`
returns. A hold shorter than 2.8 s may miss fans whose tachometer signal lags behind the
PWM response by up to 2–3 s on low-speed / high-inertia rotors (e.g. 120mm case fans at
idle). The tolerance of ±200ms accounts for clock-sleep jitter on a loaded system.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-02_hold_time_3s
