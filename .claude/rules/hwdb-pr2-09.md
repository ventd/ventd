---
name: RULE-HWDB-PR2-09
description: DMI BIOS version mismatch between calibration record and current firmware MUST trigger recalibration
type: project
---

# RULE-HWDB-PR2-09: DMI BIOS version mismatch between calibration record and current firmware MUST trigger recalibration.

When ventd starts and loads a `CalibrationRun` from disk, it compares the
`BIOSVersion` field in that record against the current BIOS version read from
`/sys/class/dmi/id/bios_version`. A mismatch MUST cause `NeedsRecalibration(run,
currentBIOS)` to return true. The test fixture verifies this with a synthetic mismatch
case and a synthetic match case (which must return false). Stale calibration after a
BIOS upgrade can produce incorrect PWM polarity detection, wrong stall_pwm, and
miscalibrated fan curves that damage hardware.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_09
