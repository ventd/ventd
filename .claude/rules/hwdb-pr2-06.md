---
name: RULE-HWDB-PR2-06
description: recommended_alternative_driver MUST be non-null when capability is ro_pending_oot
type: project
---

# RULE-HWDB-PR2-06: recommended_alternative_driver MUST be non-null when capability == ro_pending_oot.

A driver profile with `capability: ro_pending_oot` declares that the mainline driver is
read-only but an out-of-tree alternative exists. For such profiles, the
`recommended_alternative_driver` field MUST be a non-null object with at least `module`
and `source` set. A `ro_pending_oot` profile with a null `recommended_alternative_driver`
is rejected at load time. This invariant ensures the diagnostic bundle always has something
actionable to surface when it detects this driver.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_06
