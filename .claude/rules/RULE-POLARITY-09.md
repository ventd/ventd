# RULE-POLARITY-09: "Reset to initial setup" wipes the calibration KV namespace atomically via WipeNamespaces.

`probe.WipeNamespaces(db *state.KVDB)` wipes the `wizard`, `probe`, and `calibration` KV
namespaces in a single `db.WithTransaction` call (spec RULE-PROBE-09 extension). After a
successful wipe, `db.List("calibration")` MUST return an empty map. The polarity
`PolarityStore` is persisted under the `calibration` namespace; resetting without clearing
it would leave stale polarity results from a prior installation, causing the post-reset
probe to skip re-detection for channels that appear to have known polarity. Wiping all three
namespaces atomically ensures that the post-reset daemon start sees a clean slate across
probe, wizard, and polarity state.

Bound: internal/polarity/polarity_test.go:TestPolarityRules/RULE-POLARITY-09_reset_wipes_calibration_namespace
