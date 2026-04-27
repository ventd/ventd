# RULE-EXPERIMENTAL-AMD-OVERDRIVE-02: Precondition check parses /proc/cmdline for the OverDrive bit (0x4000) and returns an actionable detail when unset.

`checks.CheckAMDOverdrivePrecondition(cmdlinePath string)` MUST return `(true, detail)`
when `amdgpu.ppfeaturemask` is present in `/proc/cmdline` AND bit 14 (0x4000) is set in
the parsed value. When the parameter is absent or bit 14 is unset, it MUST return
`(false, detail)` where `detail` contains: the text `"0x4000"`, the word `"reboot"` (or
equivalent remediation guidance), and the current mask value in hex if parseable. The detail
string is surfaced verbatim in `ventd doctor` output and in the diagnostic bundle
`experimental-flags.json`; an unactionable detail (e.g. empty string) prevents the operator
from knowing what kernel parameter to add.

Bound: internal/experimental/checks/amd_overdrive_test.go:TestAMDOverdrive_PreconditionFailsActionableWhenBitUnset
