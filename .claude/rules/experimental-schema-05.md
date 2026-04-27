# RULE-EXPERIMENTAL-SCHEMA-05: Absent experimental block behaves identically to an all-false ExperimentalBlock (v1.1 behavior preserved).

When a driver or board profile has no `experimental:` key, the loaded profile's `Experimental`
field MUST be the zero value of `ExperimentalBlock` (all four fields false). No error is
returned. This preserves exact v1.1 matching behavior for all existing catalog entries that
pre-date the v1.2 schema amendment. A catalog that adds `experimental:` support for new entries
MUST NOT break loading of older entries that never set the field.

Bound: internal/hwdb/profile_v1_1_test.go:TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1
