---
name: RULE-HWDB-PR2-07
description: fan_control_capable false profiles MUST install in monitor-only mode with no calibration probe
type: project
---

# RULE-HWDB-PR2-07: fan_control_capable: false profiles MUST install in monitor-only mode (no calibration probe runs).

When the resolved `EffectiveControllerProfile` has `FanControlCapable: false`, the
install and runtime paths must not invoke the calibration probe for any channel backed
by this profile. The test fixture verifies that `ShouldCalibrate(ecp)` returns false
when `FanControlCapable` is false, regardless of what `Capability` is set to. A
monitor-only driver that runs calibration would attempt PWM writes to a driver that
either returns EPERM or silently ignores them.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_07
<!-- rulelint:allow-orphan -->
