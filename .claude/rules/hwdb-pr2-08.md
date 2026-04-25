---
name: RULE-HWDB-PR2-08
description: Calibration result bios_overridden true MUST cause apply path to refuse curve writes for that channel
type: project
---

# RULE-HWDB-PR2-08: Calibration result bios_overridden: true MUST cause apply path to refuse curve writes for that channel.

When a `CalibrationResult` loaded from disk has `BIOSOverridden: true`, the apply path
MUST return an error and skip writing any PWM value to the associated channel. The test
fixture verifies that `ShouldApplyCurve(cal)` returns false and a non-nil advisory error
when `BIOSOverridden` is set. This prevents silent no-op writes to channels where the
BIOS firmware actively overrides ventd's PWM values — the correct response is to surface
the issue in the diagnostic bundle and mark the channel as monitor-only.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_08
<!-- rulelint:allow-orphan -->
