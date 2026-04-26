# RULE-SCHEMA-08: Board catalog loader rejects a profile with both `dmi_fingerprint` and `dt_fingerprint` set.

A board profile MUST have exactly one of `dmi_fingerprint` or `dt_fingerprint`. Setting
both is a schema error: the matcher's DMI-first / DT-fallback dispatch logic requires
each profile to commit to one fingerprint type. A profile with both set produces ambiguous
match semantics (which takes precedence? what if only DT is live?). `validateBoardCatalogEntry`
MUST return a non-nil error containing `"exactly one is required"` for any profile where
both fields are non-nil, causing the entire board catalog load to abort. Similarly, a
profile with neither field set is rejected with the same error — an un-matchable profile
is a catalog defect, not a warning.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_RejectsBothFingerprintTypes
