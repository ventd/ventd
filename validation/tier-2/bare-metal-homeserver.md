# Bare-Metal Smoke — root@192.168.7.10 (home server)

Host: `root@192.168.7.10`
Kernel: `Linux 6.17.13-2-pve`
Chip: IT8688 (in-kernel `it87` driver — already covered, not OOT)
Run: 2026-04-14

## Procedure

Same as `bare-metal-phoenix.md` — ran `cmd/list-fans-probe` in-process
to compare pre-Tier-2 and post-Tier-2 discovery against a single live
sysfs snapshot.

## Result: ✅ PASS

Full output: `probe-homeserver.txt`.

### Classification

| hwmonN | chip | class | pwm | fan_input | fan_target | temp |
|---|---|---|---|---|---|---|
| hwmon0 | acpitz | nofans | 0 | 0 | 0 | 1 |
| hwmon1 | nvme | nofans | 0 | 0 | 0 | 2 |
| hwmon2 | nvme | nofans | 0 | 0 | 0 | 2 |
| hwmon3 | hidpp_battery_0 | nofans | 0 | 0 | 0 | 0 |
| hwmon4 | k10temp | nofans | 0 | 0 | 0 | 2 |
| hwmon5 | gigabyte_wmi | nofans | 0 | 0 | 0 | 6 |
| hwmon6 | jc42 | nofans | 0 | 0 | 0 | 1 |
| hwmon7 | jc42 | nofans | 0 | 0 | 0 | 1 |
| hwmon8 | it8688 | **primary** | 5 | 5 | 0 | 6 |

### Control tuples (identical pre/post)

| kind | hwmon_dir | control | fan_input |
|---|---|---|---|
| pwm | /sys/class/hwmon/hwmon8 | …/pwm1 | …/fan1_input |
| pwm | /sys/class/hwmon/hwmon8 | …/pwm2 | …/fan2_input |
| pwm | /sys/class/hwmon/hwmon8 | …/pwm3 | …/fan3_input |
| pwm | /sys/class/hwmon/hwmon8 | …/pwm4 | …/fan4_input |
| pwm | /sys/class/hwmon/hwmon8 | …/pwm5 | …/fan5_input |

### Net-new coverage / regressions

- **Net-new:** none.
- **Regressions:** none.

### Notes

- `gigabyte_wmi` shows up as `nofans` — it's a metadata-only WMI bridge
  with temperature inputs and no fan control. Classifier filters it
  correctly.
- Pre-RDNA AMD not present on this host (it's a server); `fan*_target`
  validation continues to rely on unit-test coverage.
