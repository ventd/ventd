# RULE-SYSCLASS-02: System class and evidence are written to KV store before Envelope C begins.

`PersistDetection(db *state.KVDB, d *Detection)` MUST be called with the `Detection`
returned by `Detect()` before the Envelope C calibration loop starts. It writes four keys
atomically via `db.WithTransaction`: `sysclass.class` (string), `sysclass.evidence`
(JSON array of evidence strings), `sysclass.detected_at` (RFC3339), and
`sysclass.schema_version`. After a successful persist, `LoadDetection(db)` MUST return a
`Detection` with the same class and evidence fields. Persisting before Envelope C ensures
the web UI can display the detected class during calibration and that a daemon restart
during calibration inherits the correct class without re-running detection.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_02_KVWriteBeforeEnvelopeC
