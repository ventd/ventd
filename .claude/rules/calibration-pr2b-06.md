# RULE-CALIB-PR2B-06: BIOS override is detected when the first readback matches the write but the second readback (≈200ms later) does not.

`ProbeBIOSOverride` writes `targetPWM`, reads back the register within 50ms (`v1`), then reads
again at ≈200ms (`v2`). If `v1 == targetPWM && v2 != targetPWM`, the function returns
`overridden=true`. This pattern identifies the "writes accept but BIOS reverts" class seen on
Gigabyte boards (it8689 case from hwmon-research.md §2.3). A channel with `BIOSOverridden=true`
is registered as monitor-only; `CheckWrite` returns `ErrBIOSOverridden`. A fan with BIOS
override whose `v1 != targetPWM` (write silently fails) is NOT a BIOS-override case; it is
a driver capability issue.

Bound: internal/calibration/probe_test.go:TestPR2B_Rules/bios_override_detected
