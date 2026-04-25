---
name: RULE-HWDB-PR2-13
description: Every driver_profile MUST declare exit_behaviour from the enum; missing/unknown value causes matcher to refuse loading
type: project
---

# RULE-HWDB-PR2-13: Every driver_profile MUST declare exit_behaviour from the §12.1 enum. Missing/unknown value = matcher refuses to load. Apply path MUST execute the declared behaviour on graceful shutdown (SIGTERM, service-stop).

Every driver profile MUST declare `exit_behaviour` as one of `force_max`,
`restore_auto`, `preserve`, or `bios_dependent`. A profile with a missing or
unrecognised `exit_behaviour` value is rejected at catalog load time with an error that
names the module and the invalid value. The test fixture verifies: (1) a valid profile
with each enum value loads cleanly; (2) a profile with an invalid value `"unknown_mode"`
is rejected. The `ExitBehaviour` field is exposed on `EffectiveControllerProfile` so
the apply path can dispatch the correct shutdown action per channel.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_13
<!-- rulelint:allow-orphan -->
