# RULE-GPU-PR2D-07: RDNA3+ AMD GPU writes use gpu_od/fan_ctrl/fan_curve interface only — direct pwm1 writes are refused.

When the AMD backend detects an RDNA3 or RDNA4 device (via the `fan_curve` file present
at `<card>/device/gpu_od/fan_ctrl/fan_curve`), any call to the RDNA1/2 `pwm1` write path
MUST return `ErrRDNA3UseFanCurve` without writing to `pwm1`. Writes MUST go only to the
`fan_curve` sysfs interface (5-anchor-point format: `<idx> <temp_c> <pct>` followed by `c`
to commit). The subtest `TestAMD_RDNA3UsesFanCurve` provides a synthetic `testing/fstest`
fixture containing both `pwm1` and `gpu_od/fan_ctrl/fan_curve`, calls the RDNA3 write
path with a test speed, and asserts: `pwm1` is unmodified, `fan_curve` received the
correct anchor-point bytes followed by a commit. This prevents silent no-ops when a kernel
that removed RDNA3 `pwm1` writability is encountered on a card that boots in firmware mode.

Bound: internal/hal/gpu/amdgpu/sysfs_test.go:TestAMD_RDNA3UsesFanCurve
