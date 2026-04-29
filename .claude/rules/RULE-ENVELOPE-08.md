# RULE-ENVELOPE-08: Envelope D (ramp-up) only writes PWM values ≥ baseline; writes below baseline are refused.

`probeD` begins from `baselinePWM` and only steps upward through `thr.PWMSteps`. Any step
value in `PWMSteps` that is strictly below `baselinePWM` MUST be skipped without writing.
The function MUST NOT write a PWM value lower than the baseline under any circumstance —
doing so would make the fan slower during a thermal recovery, the opposite of the safety
intent. The test injects a baseline of 140 PWM with a step table of [180, 140, 110, 90]
and verifies that only 180 is written (140 is skipped as equal to baseline, 110 and 90 are
skipped as below).

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_08_EnvelopeDRefusesBelowBaseline
