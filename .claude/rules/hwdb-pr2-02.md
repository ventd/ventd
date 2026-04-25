---
name: RULE-HWDB-PR2-02
description: chip_profile.inherits_driver MUST resolve to a known driver_profile.module
type: project
---

# RULE-HWDB-PR2-02: chip_profile.inherits_driver MUST resolve to a known driver_profile.module.

Every chip profile's `inherits_driver` field must reference a `module` value that exists in
the loaded driver catalog. A chip profile referencing an unknown driver is rejected at load
time. The error message names the failing chip `name` and the unresolved `inherits_driver`
value. This ensures the inheritance chain is always complete — a chip with no driver parent
cannot be used in the three-tier resolver.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_02
<!-- rulelint:allow-orphan -->
