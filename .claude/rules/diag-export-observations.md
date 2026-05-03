# Diag export-observations subcommand rules — v0.5.11

These invariants govern `ventd diag export-observations`, the
operator-facing CLI that converts the daemon's binary msgpack
observation log (v0.5.4+ schema) into NDJSON for offline analysis
(`jq`, `grep`, `awk`, …).

The subcommand activates `internal/ndjson` as a live consumer —
prior to this PR the package had zero importers despite shipping a
full Writer + Reader + Ring implementation. The
`SchemaVentdJournalV1` schema constant declared in
`internal/ndjson/schema.go` is now reachable by an actual code
path.

Each rule binds 1:1 to a subtest; `tools/rulelint` blocks the
merge if a rule lacks a corresponding subtest.

## RULE-DIAG-EXPORT-01: observation.Record round-trips through NDJSON envelope without field loss.

`runDiagExportObservations(args, logger)` MUST produce one NDJSON
line per record from the observation log. Each line is a JSON
object of the standard envelope shape:

```
{"schema_version": "1.0", "ts": "<RFC3339Nano>",
 "event_type": "observation_record",
 "payload": <observation.Record>}
```

The `payload` MUST decode back to a `*observation.Record` whose
`Ts`, `ChannelID`, `PWMWritten`, `RPM`, `SignatureLabel`,
`EventFlags`, and `SensorReadings` fields are byte-equal to the
original written record. The envelope's `schema_version` MUST be
exactly `"1.0"` (`SchemaVentdJournalV1`); the `event_type` MUST
be `"observation_record"`.

A round-trip mismatch is a silent data-corruption regression that
would break any future `ventd doctor --watch` consumer or
maintainer-side ingest. The bound test seeds three records of
different shapes (with / without `EventFlags`, with / without
`SignatureLabel`, varying `SensorReadings`) and asserts each
round-trips field-equal.

Bound: cmd/ventd/diag_export_observations_test.go:TestDiagExportObservations_RoundTrip
