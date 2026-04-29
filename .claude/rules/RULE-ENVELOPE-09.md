# RULE-ENVELOPE-09: Probe is resumable from the last completed step after a daemon restart.

`LoadChannelKV(db *state.KVDB, channelID string) (ChannelKV, bool)` reads the persisted
envelope state for the channel. When `state == "probing"` and `completed_step_count > 0`,
`Prober.Probe` MUST resume from `completed_step_count` (skipping already-completed steps)
rather than restarting from step 0. A channel in state `"complete_C"` or `"complete_D"`
MUST be skipped entirely — no re-probe. State `"aborted_C"` proceeds directly to Envelope D.
The test serialises a mid-run KV state with `completed_step_count=3` and verifies that
the probe writes only steps 4..N with no repeated write to steps 1..3.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_09_StepLevelResumability
