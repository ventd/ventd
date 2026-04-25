---
name: RULE-HWDB-PR2-14
description: Every driver_profile MUST declare runtime_conflict_detection_supported boolean; missing value causes matcher to refuse loading
type: project
---

# RULE-HWDB-PR2-14: Every driver_profile MUST declare runtime_conflict_detection_supported boolean. Field is consumed by post-PR-2 sanity-check feature; PR 2 itself only validates presence.

Every driver profile MUST declare `runtime_conflict_detection_supported` as an explicit
boolean (`true` or `false`). A profile where this field is absent (not just false — Go's
zero value cannot be distinguished from explicit false) is rejected at catalog load time.
The catalog loader uses a pointer type (`*bool`) internally to detect absence. The test
fixture verifies that an explicit `false` loads cleanly and that an absent field is
rejected. The field is exposed on `EffectiveControllerProfile.RuntimeConflictDetectionSupported`
for use by the post-PR-2 sanity-check path.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_14
