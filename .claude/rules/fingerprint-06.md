# RULE-FINGERPRINT-06: Matcher matches device-tree `compatible` list glob when DMI is absent and `dt_fingerprint.compatible` is set.

When `dmiPresent` is false AND a board profile has a non-empty `dt_fingerprint.compatible`
pattern, the tier-1 matcher MUST evaluate a glob match between that pattern and each entry
in the live `/proc/device-tree/compatible` null-separated list. A match on ANY entry in the
list is sufficient. A board with `dt_fingerprint.compatible: "raspberrypi,5-model-b"` MUST
match a live system whose compatible list contains `"raspberrypi,5-model-b"` (along with
other entries like `"brcm,bcm2712"`). When `dmiPresent` is true, `dt_fingerprint` profiles
are never considered — RULE-FINGERPRINT-07 covers the model field; this rule covers the
compatible list.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_DTCompatibleGlob_Matches
