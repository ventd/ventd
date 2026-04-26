# RULE-FINGERPRINT-05: Fingerprint without `bios_version` field matches any live BIOS version (v1 behavior unchanged).

When a `dmi_fingerprint` entry has no `bios_version` field (absent or empty), the tier-1
board matcher MUST treat that field as `"*"` and accept any live `BiosVersion` value,
including the empty string. This preserves exact v1.0 matching behavior for all existing
board profiles that pre-date the v1.1 schema amendment. A catalog that adds `bios_version`
support for new entries MUST NOT break matching for older entries that never set the field.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_BiosVersionAbsent_BehavesAsV1
