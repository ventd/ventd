# RULE-GPU-PR2D-05: hwmon path resolution by name only — no hwmonN number literals in non-test code under internal/hal/gpu/.

All hwmon path resolution in `internal/hal/gpu/` MUST discover the active `hwmonN` directory
by reading the `name` attribute (e.g. matching "amdgpu", "xe", "i915") rather than
hard-coding any `hwmon0`, `hwmon1`, etc. literal. The same pattern is enforced for
motherboard chip drivers by RULE-HWMON-INDEX-UNSTABLE. `hwmonN` numbers are kernel-assigned
at boot, change across reboots and module reloads, and are not stable across PCIe slot
changes. The static-analysis subtest greps all non-test `.go` files under
`internal/hal/gpu/` for the regular expression `hwmon[0-9]` and asserts zero matches.
A hard-coded path that worked in dev may silently control the wrong GPU fan after a kernel
update changes hwmon numbering.

Bound: internal/hal/gpu/amdgpu/sysfs_test.go:TestGPU_NoHwmonNumbersHardcoded
