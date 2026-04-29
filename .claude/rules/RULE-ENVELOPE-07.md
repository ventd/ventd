# RULE-ENVELOPE-07: Envelope C thermal abort transitions to Envelope D; KV state reflects the ordering and abort reason.

When Envelope C aborts due to a thermal event (dT/dt or T_abs gate), `probeC` MUST:
1. Persist KV state `calibration.envelope.<channel_id>.state = "aborted_C"` with the abort reason.
2. Immediately invoke `probeD` for the same channel.
3. After Envelope D completes (or itself aborts), persist the final state as `"complete_D"` or `"aborted_C"`.

The KV entry's `envelope` field transitions from `"C"` to `"D"` when Envelope D begins. The test
injects a thermal abort at the third PWM step of Envelope C and verifies: (a) KV shows
`aborted_C`, (b) Envelope D proceeds from baseline upward, (c) final KV shows `complete_D`.
A missed KV transition means the web UI reports Envelope C success on a thermally-constrained
system that should show the fallback result.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_07_AbortCToProbeD_OrderingPersist
