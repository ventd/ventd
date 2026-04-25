---
name: RULE-HWDB-PR2-03
description: board_profile.primary_controller.chip MUST resolve to a known chip_profile.name
type: project
---

# RULE-HWDB-PR2-03: board_profile.primary_controller.chip MUST resolve to a known chip_profile.name.

Every board profile's `primary_controller.chip` field must reference a `name` value that
exists in the loaded chip catalog. A board profile referencing an unknown chip is rejected
at load time. The error message names the failing board `id` and the unresolved chip name.
This ensures the full three-tier chain (driver → chip → board) is always resolvable when a
board match is found.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_03
