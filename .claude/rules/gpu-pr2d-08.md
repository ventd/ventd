# RULE-GPU-PR2D-08: Intel xe/i915 backend has no write code paths — os.OpenFile with write flags must not appear.

The `internal/hal/gpu/xe/` package is read-only. There is no `pwm1` or `pwm1_enable` on
Intel Arc discrete GPUs; fan control is firmware-managed. The static-analysis subtest
greps all non-test `.go` files under `internal/hal/gpu/xe/` for `os.OpenFile` calls that
include any of the write flags: `os.O_WRONLY`, `os.O_RDWR`, `os.O_CREATE`, `os.O_TRUNC`,
`os.O_APPEND`. Zero matches are required. The only file access in the xe package is
`os.ReadFile` or `os.Open` (read-only). A write flag that crept in during refactoring
would attempt to write to a path that does not accept writes, causing silent EPERM errors
that are indistinguishable from transient sysfs read errors in logs.

Bound: internal/hal/gpu/xe/sysfs_test.go:TestXE_ReadOnly
