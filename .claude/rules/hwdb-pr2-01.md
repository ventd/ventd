---
name: RULE-HWDB-PR2-01
description: Every driver_profile MUST declare all required fields; missing field causes matcher to refuse loading
type: project
---

# RULE-HWDB-PR2-01: Every driver_profile MUST declare all fields in §2-§12. Missing field = matcher refuses to load profile DB.

Every entry in the driver catalog MUST have all required top-level fields: `module`, `family`,
`description`, `capability`, `pwm_unit`, `pwm_enable_modes`, `off_behaviour`,
`polling_latency_ms_hint`, `recommended_alternative_driver`, `conflicts_with_userspace`,
`fan_control_capable`, `required_modprobe_args`, `pwm_polarity_reservation`, `exit_behaviour`,
`runtime_conflict_detection_supported`, and `citations`. A driver profile with any of these
fields absent is rejected at load time with a human-readable error that names the missing field
and the offending module name.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_01
