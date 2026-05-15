# RULE-POLARITY-02: Each bipolar pulse holds for BipolarPulseHold (2s) before the tach-read window across all hwmon and NVML probes.

`HwmonProber.ProbeChannel` calls `p.clock()(BipolarPulseHold)` after
writing `BipolarLowPWM` (51) AND after writing `BipolarHighPWM` (204),
before each `readRPMMean` call that samples the channel's RPM. The
total injected sleep per probe is at least `2 × BipolarPulseHold` (4 s)
plus the post-restore `RestoreDelay` (500 ms). The test verifies this
via an injected clock accumulator and asserts
`totalSleep >= 2 × BipolarPulseHold - 200ms`.

The pre-#1110 algorithm held for a single 3 s window after the midpoint
write. The bipolar replacement halves each individual pulse (2 s vs 3 s)
because the two-pulse arrangement provides redundant settling: a fan
that hadn't settled at the LOW read will still produce a signed delta
against the HIGH read, and the 150 RPM phantom threshold absorbs the
remaining transient. Total probe time per channel is similar (≈5 s vs
≈4.5 s). The 200 ms tolerance accommodates clock-sleep jitter on a
loaded system.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-02_hold_time_3s
