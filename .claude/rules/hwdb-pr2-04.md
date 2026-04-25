---
name: RULE-HWDB-PR2-04
description: pwm_unit_max MUST be set when pwm_unit is step_0_N or cooling_level
type: project
---

# RULE-HWDB-PR2-04: pwm_unit_max MUST be set when pwm_unit ∈ {step_0_N, cooling_level}.

When a driver profile declares `pwm_unit: step_0_N` or `pwm_unit: cooling_level`,
the companion `pwm_unit_max` field MUST be a non-null positive integer. A profile with
these pwm_unit values and a null or absent `pwm_unit_max` is rejected at load time.
The error names the module and the constraint. This prevents calibration code from
dispatching a discrete-state sweep without knowing how many states exist.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_04
<!-- rulelint:allow-orphan -->
