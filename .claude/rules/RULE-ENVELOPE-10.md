# RULE-ENVELOPE-10: Every probe step event is appended to the LogStore as a msgpack-encoded StepEvent with schema_version=1.

`appendStepEvent(db *state.LogDB, ev StepEvent) error` marshals `ev` using
`github.com/vmihailenco/msgpack/v5` and calls `db.Append("envelope", payload)`. The
`StepEvent` struct MUST include all fields: `SchemaVersion` (always 1), `ChannelID`,
`Envelope` ("C" or "D"), `EventType` (one of the seven event-type constants),
`TimestampNs`, `PWMTarget`, `PWMActual`, `Temps`, `RPM`, `ControllerState`, `EventFlags`,
and `AbortReason`. A round-trip test marshals a fully-populated StepEvent and unmarshals it
back, asserting field-for-field equality. A missing field in the serialised form breaks
post-hoc analysis of the diagnostic bundle.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_10_LogStoreSchemaConformance
