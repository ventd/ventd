# RULE-PROBE-07: PersistOutcome writes schema_version, last_run, result (probe namespace) and initial_outcome, outcome_reason, outcome_timestamp (wizard namespace) atomically.

`PersistOutcome(db *state.KVDB, r *ProbeResult)` MUST use `db.WithTransaction` to set all
six keys in a single atomic commit: `probe.schema_version` (uint16 SchemaVersion),
`probe.last_run` (RFC3339 timestamp), `probe.result` (JSON-encoded ProbeResult),
`wizard.initial_outcome` (outcome string), `wizard.outcome_reason`, and
`wizard.outcome_timestamp`. A partial write (transaction failure mid-way) leaves the
store unchanged. This ensures the wizard fork decision and the full probe result are
always consistent — a daemon that reads `wizard.initial_outcome` can trust that
`probe.result` reflects the same run.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-07_persist_outcome_writes_kv_keys
