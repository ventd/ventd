# RULE-FINGERPRINT-07: Matcher matches device-tree `model` string glob when DMI is absent and `dt_fingerprint.model` is set.

When `dmiPresent` is false AND a board profile has a non-empty `dt_fingerprint.model`
pattern, the tier-1 matcher MUST evaluate a glob match between that pattern and the live
`/proc/device-tree/model` string (null-terminated, trimmed). A board with
`dt_fingerprint.model: "Raspberry Pi 5*"` MUST match a live system with model
`"Raspberry Pi 5 Model B Rev 1.0"`. The glob wildcard `*` matches any suffix including the
revision suffix that varies across hardware batches. When `dmiPresent` is true, `dt_fingerprint`
profiles are never considered regardless of whether `/proc/device-tree/model` exists.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_DTModelGlob_Matches
