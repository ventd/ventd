# RULE-PROBE-09: "Reset to initial setup" wipes both wizard and probe KV namespaces atomically; LoadWizardOutcome returns ok=false afterward.

`WipeNamespaces(db *state.KVDB)` enumerates all keys in the `wizard` and `probe` namespaces
via `db.List`, then deletes them all inside a single `db.WithTransaction` call. After a
successful wipe, `db.List("wizard")` and `db.List("probe")` MUST return empty maps, and
`LoadWizardOutcome` MUST return `ok=false`. The web handler for "Reset to initial setup"
calls `WipeNamespaces` after removing the config file, ensuring the next daemon start
treats the system as freshly installed and runs the full probe again before entering the
wizard. A partial wipe (wizard cleared, probe left) could cause the wizard to start from
scratch while the old probe result remains, producing contradictory state.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-09_wipe_namespaces_empties_both
