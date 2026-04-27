# RULE-EXPERIMENTAL-AMD-OVERDRIVE-04: RDNA4 (Navi 48, PCI 0x7550) fan_curve writes are refused on kernel < 6.15.

`checkRDNA4KernelGate(cardPath, osReleasePath string)` MUST return `ErrRDNA4NeedsKernel615`
when `IsRDNA4(cardPath)` returns true AND the running kernel version is below 6.15 (as
parsed from `/proc/sys/kernel/osrelease`). The gate is applied inside
`CardInfo.WriteFanCurveGated` after the `AMDOverdrive` flag check. Non-RDNA4 cards (PCI
device IDs not in the `rdna4DeviceIDs` map) are unaffected regardless of kernel version.
RDNA4 on kernel ≥ 6.15 is permitted. This prevents writes to the `fan_curve` interface on
kernels that do not yet expose the RDNA4 fan_curve sysfs path (merged in kernel 6.15 via
drm/amdgpu commit for Navi 48).

Bound: internal/hal/gpu/amdgpu/rdna4_test.go:TestAMDGPU_RDNA4RefusesOnKernelBelow615
