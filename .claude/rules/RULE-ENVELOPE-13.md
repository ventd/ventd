# RULE-ENVELOPE-13: When Envelope D cannot produce a safe curve (all steps below baseline), the wizard falls back to monitor-only mode.

`probeD` returns `ErrEnvelopeDInsufficient` when every step in `thr.PWMSteps` is ≤
`baselinePWM` — meaning there is no headroom to probe above baseline. This error MUST be
propagated to `Prober.Probe`, which sets the channel's KV state to `"aborted_C"` (unchanged),
logs a WARN, and returns the error to the wizard orchestrator. The wizard treats this error
as equivalent to an OutcomeMonitorOnly decision for that channel: it is excluded from the
generated fan curve. The test constructs a baseline of 200 (maximum PWM for server class) with
a step table of [200, 170, 140, 120, 100] and verifies ErrEnvelopeDInsufficient is returned.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_13_UniversalDInsufficient_WizardFallback
