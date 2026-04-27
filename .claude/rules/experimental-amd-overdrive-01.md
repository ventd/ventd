# RULE-EXPERIMENTAL-AMD-OVERDRIVE-01: All AMD GPU HAL write paths return ErrAMDOverdriveDisabled when AMDOverdrive flag is false.

`CardInfo.WritePWM` and `CardInfo.WriteFanCurveGated` MUST return `ErrAMDOverdriveDisabled`
when `CardInfo.AMDOverdrive` is false, regardless of any other card state (RDNA generation,
pwm1 presence, fan_curve presence). No bytes are written to any sysfs file. The
`AMDOverdrive` field mirrors the `--enable-amd-overdrive` CLI flag and is set by the GPU
registry on every enumerated card. This gate ensures that enabling amd_overdrive is an
explicit operator decision — AMD GPU fan control via sysfs may require the `0x4000` OverDrive
bit in `amdgpu.ppfeaturemask`, which taints the kernel on Linux 6.14+.

Bound: internal/hal/gpu/amdgpu/overdrive_test.go:TestAMDGPU_WriteRefusesWhenOverdriveFlagFalse
