# AMD OverDrive fan control

> **Risk:** Enabling this feature sets the `amdgpu.ppfeaturemask` kernel parameter with
> bit 14 (0x4000). On Linux 6.14+, this taints the kernel
> (`commit b472b8d829c1`). The taint is cosmetic and does not affect stability, but
> it will appear in crash reports. It is not reversible without a reboot.

## What it enables

- **RDNA1/2** (RX 5xxx, RX 6xxx): direct `pwm1` writes via hwmon.
- **RDNA3** (RX 7xxx): 5-anchor-point fan curve via `gpu_od/fan_ctrl/fan_curve`.
- **RDNA4** (RX 9xxx / Navi 48): same fan curve interface; requires kernel ≥ 6.15.

Without this flag, ventd enumerates AMD GPUs for RPM monitoring only and refuses all
write attempts with `ErrAMDOverdriveDisabled`.

## How to enable

1. Add the kernel parameter to your bootloader (example for GRUB):

   ```
   GRUB_CMDLINE_LINUX_DEFAULT="... amdgpu.ppfeaturemask=0x4000"
   ```

   If you already have a `ppfeaturemask` value, OR in the bit:
   `existing_value | 0x4000`.

2. Regenerate the bootloader config and reboot:

   ```sh
   sudo update-grub   # Debian/Ubuntu
   sudo grub2-mkconfig -o /boot/grub2/grub.cfg  # Fedora/RHEL
   ```

3. Pass the flag to ventd:

   ```sh
   ventd --enable-amd-overdrive
   ```

   Or set it in `ventd.toml`:

   ```toml
   [experimental]
   amd_overdrive = true
   ```

## Verifying the precondition

```sh
ventd doctor
```

Look for the `experimental.amd_overdrive` line. It will show `active` with the current
`ppfeaturemask` hex value when the bit is set, or `inactive` with remediation guidance
when it is not.
