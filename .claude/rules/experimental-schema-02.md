# RULE-EXPERIMENTAL-SCHEMA-02: Recognized experimental key with non-bool value is rejected with a typed error.

When an `experimental:` block contains a recognized key (`ilo4_unlocked`, `amd_overdrive`,
`nvidia_coolbits`, or `idrac9_legacy_raw`) paired with a non-boolean value (e.g. a string or
integer), `validateExperimental` MUST return a non-nil error containing both the key name
and the Go type of the bad value (e.g. `"experimental.amd_overdrive: expected bool, got string"`).
The catalog load MUST fail and return that error. A string where a bool is expected indicates
a YAML authoring error; silently coercing would mask the mistake and leave the field at its
zero value.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue
