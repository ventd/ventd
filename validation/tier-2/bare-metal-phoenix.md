# Bare-Metal Smoke — phoenix@192.168.7.209 (NCT6687D)

Host: `phoenix@192.168.7.209`
Kernel: `Linux 6.17.0-20-generic`
Chip: NCT6687D (MSI MAG series) via out-of-tree `nct6687d` module
Run: 2026-04-14

## Procedure

Ran `cmd/list-fans-probe` (static CGO_ENABLED=0 binary). The helper
executes the pre-Tier-2 chip-blind sysfs walk and the post-Tier-2
`EnumerateDevices` path in-process, applies the same writability probe
to both candidate sets, and diffs the resulting tuples.

## Result: ✅ PASS

Full output: `probe-phoenix.txt`.

### Classification

| hwmonN | chip | class | pwm | fan_input | fan_target | temp |
|---|---|---|---|---|---|---|
| hwmon0 | acpitz | nofans | 0 | 0 | 0 | 1 |
| hwmon1 | nvme | nofans | 0 | 0 | 0 | 1 |
| hwmon2 | nvme | nofans | 0 | 0 | 0 | 3 |
| hwmon3 | hidpp_battery_0 | nofans | 0 | 0 | 0 | 0 |
| hwmon4 | coretemp | nofans | 0 | 0 | 0 | 25 |
| hwmon5 | nct6687 | **readonly** | 8 | 10 | 0 | 7 |
| hwmon6 | nct6687 | **primary** | 8 | 8 | 0 | 7 |

### Control tuples (identical pre/post)

Both discovery paths return the same 8-channel set on `hwmon6`:

| kind | hwmon_dir | control | fan_input |
|---|---|---|---|
| pwm | /sys/class/hwmon/hwmon6 | …/pwm1 | …/fan1_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm2 | …/fan2_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm3 | …/fan3_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm4 | …/fan4_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm5 | …/fan5_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm6 | …/fan6_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm7 | …/fan7_input |
| pwm | /sys/class/hwmon/hwmon6 | …/pwm8 | …/fan8_input |

### Net-new coverage / regressions

- **Net-new:** none.
- **Regressions:** none.

### Notes

- Two `nct6687` entries appear under `/sys/class/hwmon`. `hwmon5` is the
  in-kernel `nct6683` driver's view (no `pwm_enable`, correctly classified
  `readonly`); `hwmon6` is the out-of-tree `nct6687d` driver's view
  (controllable). The capability classifier correctly picks the latter
  as the control target.
- `hwmon5`'s read-only PWM surfaces as `readonly` rather than falling
  into post-Tier-2's control list — this matches pre-Tier-2 behaviour,
  where its writability probe rejected those files (write of the current
  value fails because the kernel never exposed them as writable).
