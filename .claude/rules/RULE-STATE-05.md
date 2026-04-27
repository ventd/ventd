# RULE-STATE-05: Schema version on read MUST be checked. Y > X (downgrade) MUST refuse start with diagnostic. Y < X (upgrade) MUST run registered migration or treat as missing.

`CheckVersion(dir string)` reads the integer in `dir/version`:

- **Missing file**: write `currentVersion`, return nil (first run).
- **on-disk == currentVersion**: return nil.
- **on-disk > currentVersion**: return an error wrapping `ErrDowngrade` with a
  human-readable message that names both versions and instructs the operator to
  reinstall a newer binary or run `ventd state reset`. The daemon must not start
  when it encounters a state directory written by a future version.
- **on-disk < currentVersion**: apply each registered `MigrateFn` from version
  `v` to `currentVersion` sequentially. If no migration is registered for a step,
  the state is treated as missing (consumers re-initialise). Update the sentinel to
  `currentVersion` and return nil.

Bound: internal/state/state_test.go:TestRULE_STATE_05_SchemaVersionCheck
