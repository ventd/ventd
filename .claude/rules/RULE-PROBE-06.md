# RULE-PROBE-06: All ControllableChannel.Polarity == "unknown" in v0.5.1 — polarity disambiguation deferred to v0.5.2 calibration probe.

`enumerateChannels` MUST set `ControllableChannel.Polarity = "unknown"` for every channel it
produces. No PWM ramp writes may be performed during the v0.5.1 probe to detect polarity
(normal vs inverted). The channel discovery phase is read-only (RULE-PROBE-01); polarity
requires controlled PWM writes with RPM correlation, which belongs to the v0.5.2 calibration
sweep. A polarity field left as empty string instead of "unknown" would be indistinguishable
from a missing field in JSON serialisation and could cause v0.5.2 migration logic to
incorrectly classify the channel as not yet probed.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-06_polarity_always_unknown
