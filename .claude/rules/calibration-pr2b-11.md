# RULE-CALIB-PR2B-11: CalibrationRun JSON round-trips without data loss; schema_version=1 is preserved.

A `hwdb.CalibrationRun` marshalled to JSON and unmarshalled back must be field-for-field
equal to the original: `schema_version`, `dmi_fingerprint`, `bios_version`,
`calibrated_at`, `channels[*].channel_index`, `channels[*].stall_pwm`,
`channels[*].min_responsive_pwm`, `channels[*].polarity_inverted`, `channels[*].phantom`,
`channels[*].bios_overridden`, and all nullable pointer fields. The `schema_version` field
MUST be included in marshalled output (not zero-valued away) because PR 2c's diagnostic
bundle and any future migration tool depend on it being present. A round-trip failure means
the on-disk format diverges from the in-memory struct, silently discarding calibration data
on next load.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/calibration_result_json_roundtrip
