# RULE-GPU-PR2D-03: libnvidia-ml.so.1 absence is graceful — no panic, daemon continues.

When `libnvidia-ml.so.1` is not installed (no NVIDIA driver, or driver installed but library
absent), `nvml.Open` MUST return a non-nil error wrapping `nvidia.ErrLibraryUnavailable`.
The GPU enumeration in `internal/hal/gpu/registry.go` treats this error as "no NVIDIA GPUs
detected" and continues registering AMD and Intel backends. The daemon MUST NOT panic, MUST
NOT exit non-zero, and MUST log exactly one INFO-level message ("NVIDIA driver not detected;
GPU features disabled"). The subtest calls `nvml.Open` in an environment where no
`libnvidia-ml.so.1` is present and asserts: non-nil error, errors.Is wraps
`nvidia.ErrLibraryUnavailable`, no goroutine leak, no panic.

Bound: internal/hal/gpu/nvml/loader_test.go:TestNVML_GracefulMissingLib
