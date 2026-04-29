# RULE-POLARITY-01: Midpoint write is exactly 128 for hwmon, 50% for NVML, and vendor-specific for IPMI.

`HwmonProber.ProbeChannel` writes exactly `128` to `ch.PWMPath` as the midpoint stimulus before
measuring the RPM response (spec §3.1). `NVMLProber.ProbeChannel` calls `SetFanSpeed(gpuIdx,
fanIdx, 50)` — the NVML percentage midpoint. `DellIPMIProbe` and `SupermicroIPMIProbe` use
their vendor-specific OEM write primitives. A midpoint stimulus is chosen so that both normal
and inverted polarity fans produce a measurable RPM delta from an idle baseline — a too-low
stimulus would leave an inverted fan indistinguishable from phantom.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-01_midpoint_write
