# Hardware Compatibility

`ventd` is capability-first: it enumerates every writable fan control the Linux kernel exposes and works with it, regardless of chip identity. This document lists confirmed-working hardware and the gotchas that apply to specific chips.

## Detection model

For every `hwmon` device in `/sys/class/hwmon/`, `ventd` discovers:

- `pwm[N]` — PWM duty cycle (0–255)
- `fan[N]_target` — RPM setpoint (pre-RDNA AMD, some BMCs, some pumps)
- `fan[N]_pulse` — pulse count (read-only on most chips)
- `pwm[N]_enable` — mode register (must be 1 = manual before writes)

Each control carries a `ControlKind` so the writer picks the correct sysfs attribute. A control maps to its corresponding `fan[N]_input` for RPM feedback — indices do not always match.

NVIDIA GPUs are detected at runtime via `dlopen` of `libnvidia-ml.so.1`. If the driver is absent, GPU features disable silently. AMD GPUs appear as normal `amdgpu` hwmon devices.

## Motherboard chips

| Chip family | Status | Notes |
|---|---|---|
| Nuvoton NCT6775, NCT6776, NCT6779, NCT6791, NCT6792, NCT6793, NCT6795, NCT6796, NCT6797, NCT6798 | Works | Standard `nct6775` kernel driver |
| Nuvoton NCT6683 | Works | Standard `nct6683` kernel driver |
| Nuvoton NCT6687D | Requires out-of-tree driver | See [NCT6687D](#nct6687d-msi-and-some-asrock) below |
| ITE IT8628E, IT8665E, IT8686E, IT8688E, IT8689E, IT8720F, IT8721F, IT8728F, IT8732F, IT8771E, IT8772E | Works | Standard `it87` kernel driver |
| ITE newer boards | May require out-of-tree fork | See [IT87 out-of-tree](#it87-out-of-tree) below |
| Fintek F71808A, F71858FG, F71862FG, F71869, F71882FG, F71889FG, F71889ED | Works | Standard `f71882fg` driver |
| SMSC / Microchip EMC2103, EMC6D103, EMC6W201 | Works | Standard `emc` drivers |
| ASUS EC (ROG boards) | Works | `asus-ec-sensors` kernel driver |
| AMD k10temp, zenpower | Read-only | Temperature only, no fan control |

### NCT6687D (MSI and some ASRock)

The NCT6687D is not supported by the mainline kernel. `ventd` detects this case and offers a one-click install of the [`nct6687d`](https://github.com/Fred78290/nct6687d) DKMS driver. Requirements:

- Kernel headers matching the running kernel (`linux-headers-$(uname -r)` on Debian/Ubuntu, `kernel-devel` on Fedora)
- DKMS installed
- If Secure Boot is enabled, a MOK (Machine Owner Key) enrollment step — `ventd` walks you through this

After the driver loads, `ventd` persists the module in `/etc/modules-load.d/ventd.conf` so it survives reboot.

### IT87 out-of-tree

Some newer ITE chips are only supported by the [`it87` out-of-tree fork](https://github.com/frankcrawford/it87). Same DKMS flow as NCT6687D. `ventd` surfaces the need automatically.

### Board-specific quirks

- **Gigabyte X870 / X670E** — some boards expose only partial fan headers via ACPI; BIOS updates have resolved this on most SKUs.
- **ASRock B650 / X670E** — `it87` probe may fail at boot; the install diagnostic detects this and suggests kernel parameters.
- **ASUS ROG with ASUS EC** — EC sensors take a few seconds to appear after boot; `ventd` waits up to 10 s during first scan.

## GPUs

| GPU family | Status | Method |
|---|---|---|
| NVIDIA (Maxwell and later, driver 470+) | Works | NVML via runtime `dlopen` |
| NVIDIA (older drivers, < 470) | Read-only | Temp/util only; fan write may silently fail — see below |
| AMD RDNA, RDNA2, RDNA3, RDNA4 | Works | `amdgpu` hwmon |
| AMD GCN (pre-RDNA) | Works with RPM-target | Uses `fan*_target` instead of PWM |
| Intel Arc / Iris Xe | Read-only | Kernel exposes monitoring, not control |

NVIDIA fan write requires root and may silently fail on some driver/card combinations. If so, `ventd` emits a hardware diagnostic suggesting NVIDIA persistence mode:

```
sudo nvidia-smi -pm 1
```

## Pumps

Pumps are detected during calibration by low RPM variance across the PWM sweep plus a non-zero RPM floor. Classified pumps are assigned a per-fan `pump_minimum` floor — `ventd` will never write below this value.

Confirmed pumps:

- Corsair H100i, H115i, H150i connected via USB — see "Liquid cooling (AIO) — Corsair" section below (alpha, v0.4.0).
- NZXT Kraken X series (stock header only)
- Arctic Liquid Freezer II, III (VRM fan is classified separately from pump)
- Custom loops driven by a D5 or DDC pump controller on a standard PWM header

For USB-connected AIOs (iCUE, CAM, NZXT), use the vendor software or [liquidctl](https://github.com/liquidctl/liquidctl) — these pumps do not expose themselves through `hwmon`.

## Server / BMC

`ventd` can read IPMI sensors via the `ipmi_si` driver when it exposes hwmon entries, but BMC fan control on most server chassis (Dell iDRAC, HPE iLO, Supermicro IPMI) overrides `hwmon` writes. Use the BMC's own fan curve controls. `ventd` on server chassis is useful for monitoring; fan control requires BMC-specific workflow that is out of scope.

## ARM / Raspberry Pi

Raspberry Pi 4, CM4, Pi 5: fan control via the `gpio-fan` or `pwm-fan` device tree overlay. Enable the overlay in `/boot/firmware/config.txt` or the distro equivalent, then `ventd` sees the hwmon entry and controls it normally.

## Liquid cooling (AIO) — Corsair

Corsair USB HID AIO and fan controller support is new in v0.4.0 and
alpha-quality. Read paths (fan RPM, temperatures, connected-state) are
enabled by default. Write paths (set PWM, set curve) require the
experimental `--enable-corsair-write` flag — see below.

| Family            | VID    | PIDs             | Channels     | Tested firmware | Status                       |
|-------------------|--------|------------------|--------------|-----------------|------------------------------|
| Commander Core    | 0x1b1c | 0x0c1c, 0x0c1e   | pump + 6 fan | none            | alpha, read-only by default  |
| Commander Core XT | 0x1b1c | 0x0c20, 0x0c2a   | 6 fan        | none            | alpha, read-only by default  |
| Commander ST      | 0x1b1c | 0x0c32           | pump + 6 fan | none            | alpha, read-only by default  |

### Writes are experimental

No firmware version is currently on the validation allow-list. Every
probed device is treated as `unknownFirmwareDevice` and writes return
`ErrReadOnly` unless you start ventd with `--enable-corsair-write`.
Running writes against unvalidated firmware may leave the device in a
state requiring iCUE to recover.

### Access mechanism

`deploy/90-ventd-liquid.rules` tags Corsair VID 0x1b1c devices with
`uaccess`, granting the logged-in seat user access. No root, no sidecar.

### Not in scope for v0.4.0

- Commander Pro (different protocol, separate spec-02a)
- iCUE LINK System Hub (daisy-chain addressing, separate PR)
- RGB / LED control (out of scope — use OpenRGB)

### Help validate

If you own a Commander Core, Core XT, or Commander ST and can help
validate, please file an issue with the output of `ventd --list-fans`.

## Reporting compatibility

If `ventd` does not detect a fan controller on your hardware, open a GitHub issue with:

- `sensors` output: `sensors`
- hwmon tree: `ls -la /sys/class/hwmon/*`
- DMI info: `sudo dmidecode -t 2`
- distribution and kernel: `uname -a; cat /etc/os-release`
- relevant journal lines: `journalctl -u ventd -n 200`

Hardware reports are contributions and are welcomed.
