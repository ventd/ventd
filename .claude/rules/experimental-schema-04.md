# RULE-EXPERIMENTAL-SCHEMA-04: Unknown experimental key with Levenshtein distance > 2 is accepted with a one-shot WARN; subsequent occurrences of the same key are silently ignored.

When an `experimental:` block contains an unrecognized key whose Damerau-Levenshtein distance
to every recognized key is > 2, `validateExperimental` MUST accept the entry (not fail), log
exactly one `slog.LevelWarn` message containing the text `"unknown key ignored"` for that key,
and suppress all subsequent WARN emissions for the same key within the process lifetime. This
is the forward-compat shim for v1.3+ catalog entries loaded on a v1.2 ventd binary. The test
injects a `slog.Handler` via `slog.SetDefault` to capture the WARN, calls the loader twice
with the same unknown key, and asserts exactly one WARN and no error.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce
