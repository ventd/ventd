---
name: RULE-HWDB-PR2-11
description: PR 1 → PR 2 migration - a PR 1 pwm_control string MUST resolve via chip-name fallback path with a logged warning
type: project
---

# RULE-HWDB-PR2-11: PR 1 → PR 2 migration: a PR 1 pwm_control: <string> MUST resolve via the chip-name fallback path with a logged warning if the string doesn't match a chip profile.

The `ModuleProfile.ToEffectiveControllerProfile()` migration helper (added to
`module_match.go`) MUST attempt to resolve the PR 1 `hardware.pwm_control` string
first as a chip_profile.name, then as a driver_profile.module. In both cases it
returns an EffectiveControllerProfile. When the string matches a driver module but not
a chip name, the migration logs a warning and synthesises an anonymous chip profile
with no overrides. The test fixture verifies both resolution paths: a string that
matches a known chip name ("nct6798") and a string that matches only a driver module
("nct6775"). The warning must be observable via the test's slog handler.

Bound: internal/hwdb/profile_v1_test.go:TestRuleHwdbPR2_11
<!-- rulelint:allow-orphan -->
