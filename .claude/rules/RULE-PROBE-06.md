# RULE-PROBE-06: ControllableChannel.Polarity MUST be drawn from the closed set {"unknown", "normal", "inverted", "phantom"}.

`ControllableChannel.Polarity` MUST be a value from the closed set `{"unknown", "normal",
"inverted", "phantom"}`. No code path may produce a value outside this set. The probe layer
(spec-v0_5_1) sets every channel to `"unknown"`. The polarity probe (spec-v0_5_2) resolves
each channel to one of the other three values. A value outside this set — including the empty
string — is invalid: empty string is indistinguishable from a missing field in JSON
serialisation and would cause downstream migration logic to misclassify the channel's probe
state.

<!-- v0.5.2 correction: the v0.5.1 statement ("all polarity == unknown") was an incomplete
     invariant. The correct invariant is closed-set membership. v0.5.1 still satisfies it
     because "unknown" is a member of the set; v0.5.2 polarity probe resolves to one of
     the other three values. -->

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-06_polarity_always_unknown
