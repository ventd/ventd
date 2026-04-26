# RULE-GPU-PR2D-06: Laptop dGPU detection is conservative — DMI chassis_type in laptop set marks dGPU as requires_userspace_ec.

When `nvml.LaptopDGPU(dmiRoot)` detects that the chassis type is one of: Portable (8),
Laptop (9), Notebook (10), Hand Held (11), Sub Notebook (14), or Convertible (31) — as
read from `<dmiRoot>/class/dmi/id/chassis_type` — AND at least one NVML GPU is visible,
the function returns `(true, nil)`. The GPU registry then sets `RequiresUserspacEC: true`
on the corresponding channel, and any write attempt returns `ErrLaptopDgpuRequiresEC`
with a message pointing to the spec-09 NBFC backend. The subtest exercises the detection
with a synthetic `testing/fstest.MapFS` rooted at a temp dir, injecting chassis_type=9
(Laptop) and asserting the returned bool is true and the write attempt returns a non-nil
error containing "requires_userspace_ec". Non-laptop chassis types must return false.

Bound: internal/hal/gpu/nvml/probe_test.go:TestNVML_LaptopDgpuRequiresEC
