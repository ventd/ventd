# RULE-EXPERIMENTAL-DIAG-INCLUSION: Snapshot encodes active flags and all-flags precondition status for the diagnostic bundle.

`experimental.Snapshot(flags Flags) DiagSnapshot` MUST return a `DiagSnapshot` where:
- `Active` contains the names of currently enabled flags in canonical order (matching `flags.Active()`).
- `Preconditions` is a map keyed by every name in `All()` (all four canonical names), with each
  value containing the `Met` and `Detail` fields from `Check(name)`.

When no flags are active, `Active` is empty and `Preconditions` still contains all four keys.
`CollectExperimental(flags Flags)` in `internal/diag/detection` calls `Snapshot` and encodes the
result as `experimental-flags.json` in the bundle. The `diag.Options.ExperimentalFlags` field is
threaded through `Generate` to `CollectExperimental` so the snapshot reflects the resolved flags.

Bound: internal/experimental/diag_test.go:TestDiag_SnapshotIncludesActiveAndPreconditions
