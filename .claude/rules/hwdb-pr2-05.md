---
name: RULE-HWDB-PR2-05
description: pwm_enable_modes MUST contain a manual entry when capability is rw_full, rw_quirk, or rw_step
type: project
---

# RULE-HWDB-PR2-05: pwm_enable_modes MUST contain a manual entry when capability ∈ {rw_full, rw_quirk, rw_step}.

For any driver profile with `capability` in `{rw_full, rw_quirk, rw_step}`, the
`pwm_enable_modes` map MUST contain at least one entry whose value is `"manual"`. A
writable driver with no manual-mode entry is rejected at load time. The error names the
module and the missing mode. This ensures ventd can always take manual PWM control on a
driver it is permitted to write to — a driver without a known manual-mode integer cannot
be safely calibrated.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_05
