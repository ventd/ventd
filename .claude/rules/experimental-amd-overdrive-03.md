# RULE-EXPERIMENTAL-AMD-OVERDRIVE-03: Doctor check reports active state and ppfeaturemask value in the status line.

`doctor/checks.CheckAMDOverdrive(flags experimental.Flags, mask uint32)` MUST return an
`AMDOverdriveEntry` where:
- `Active` is true when `flags.AMDOverdrive` is true, false otherwise.
- `Mask` is the `uint32` ppfeaturemask value passed in (0 when the flag is inactive or
  the mask is not yet read).
- `StatusLine` contains the text `"experimental.amd_overdrive"` and either `"active"` or
  `"inactive"` depending on the flag state. When active, `StatusLine` also includes the
  mask value formatted as `"ppfeaturemask=0x%08x"`.

This entry is consumed by `ventd doctor` to surface one human-readable line per
experimental feature. A missing mask or ambiguous status line prevents operators from
verifying the kernel parameter without opening journald.

Bound: internal/doctor/checks/experimental_test.go:TestDoctor_AMDOverdrive_ReportsActiveStateAndMask
