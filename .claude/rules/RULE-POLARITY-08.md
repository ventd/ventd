# RULE-POLARITY-08: On daemon start, ApplyOnStart matches persisted polarity results to live channels by PWMPath; unmatched channels remain "unknown".

`ApplyOnStart(db *state.KVDB, channels []*probe.ControllableChannel, logger *slog.Logger)`
loads the `PolarityStore` from the `calibration` KV namespace and calls `ApplyPersisted`
for each live channel. `ApplyPersisted` returns `MatchApplied` when a persisted result
matches `ch.PWMPath`, sets `ch.Polarity` and `ch.PhantomReason`, and logs the match at
INFO level. Channels with no persisted entry remain at `Polarity="unknown"` and receive a
log entry noting that a probe is required. Orphaned persisted entries (no live channel) are
logged at INFO level but do not cause an error. A `nil` db is a no-op.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-08_daemon_start_match
