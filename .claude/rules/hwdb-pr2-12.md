---
name: RULE-HWDB-PR2-12
description: The matcher MUST refuse to match a profile that violates any of RULE-HWDB-PR2-01..05
type: project
---

# RULE-HWDB-PR2-12: The matcher MUST refuse to match a profile that violates any of RULE-HWDB-PR2-01..05. Test fixture: invalid profile, expect refusal + diagnostic.

The catalog loader `LoadCatalog()` MUST validate all driver and chip profiles against
RULE-HWDB-PR2-01..05 before returning any catalog. If any profile fails validation, the
entire catalog load fails with a structured error that names the violating profile and
the rule. The test fixture loads an invalid driver profile (missing `capability`) and
asserts that LoadCatalog returns a non-nil error containing "required field". A valid
catalog with no violations must load cleanly. This prevents silent acceptance of a
malformed catalog entry that could produce undefined matcher behaviour at runtime.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_12
<!-- rulelint:allow-orphan -->
