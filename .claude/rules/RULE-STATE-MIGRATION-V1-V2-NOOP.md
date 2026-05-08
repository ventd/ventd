# RULE-STATE-MIGRATION-V1-V2-NOOP: A registered no-op v1→v2 migrator preserves caller state across the version bump and exercises the migration mechanism end-to-end.

`migrations[[2]int{1, 2}]` MUST be a registered, callable no-op that returns nil
without touching any file in the state directory. The version-2 schema is
identical to version-1 on disk; the bump exists to reserve the v2 slot for the
v0.6.0 broker-namespace migration (and any other v0.6 breaking shape change)
without triggering RULE-STATE-05's "treat as missing" path.

A registered no-op is structurally distinct from a missing migrator. RULE-STATE-05
specifies that when no migration is registered for a step, the upgrade loop
breaks out and the caller's state is effectively wiped on next access:

> If no migration is registered for a step, the state is treated as missing
> (consumers re-initialise).

That semantic is correct for additive-only changes that the caller can re-derive,
but is wrong for the v0.6 transition where existing calibration / polarity /
smart-mode shards must survive. Registering an explicit no-op:

1. Keeps the upgrade loop walking forward (consumers' state is preserved).
2. Exercises the migration mechanism end-to-end so the first real migration
   that lands (v2→v3, broker-namespace shape) drops in against a tested loop
   rather than against a dead code path.
3. Pins `currentVersion = 2` so the v0.6 release line is unambiguous about
   which schema slot it occupies.

The migrator itself is `noopV1ToV2(dir string) error { return nil }` in
`internal/state/version.go`. It is registered via the `migrations` map literal
(not `RegisterMigration`) because the version is internal to the state package
— external packages that introduce schema changes still use `RegisterMigration`
at init time.

The `currentVersion` constant is bumped to `2` in the same change. A regression
that reverts `currentVersion` to `1` makes the v1→v2 migrator dead code and
undoes the broker-namespace reservation; the bound subtest catches that
regression explicitly.

Bound: internal/state/state_test.go:v1_to_v2_migrator_is_registered
Bound: internal/state/state_test.go:upgrade_v1_to_currentVersion_runs_migrator_and_bumps_sentinel
Bound: internal/state/state_test.go:noop_migrator_does_not_mutate_sibling_files_in_state_dir
Bound: internal/state/state_test.go:currentVersion_is_at_least_2
