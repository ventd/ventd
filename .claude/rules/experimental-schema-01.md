# RULE-EXPERIMENTAL-SCHEMA-01: Recognized experimental key with bool value is accepted and parsed into ExperimentalBlock.

When an `experimental:` block in a driver or board profile contains a key from the recognized
set (`ilo4_unlocked`, `amd_overdrive`, `nvidia_coolbits`, `idrac9_legacy_raw`) with a boolean
value (`true` or `false`), `validateExperimental` MUST accept the entry and set the
corresponding field on the returned `ExperimentalBlock`. The test fixture provides a YAML
driver profile with `experimental: {amd_overdrive: true}` loaded via `LoadCatalogFromFS` on
an inline `fstest.MapFS` and asserts that `cat.Drivers["amdgpu"].Experimental.AMDOverdrive == true`.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys
