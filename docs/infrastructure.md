# ventd developer infrastructure

## Real hardware

| Machine | Access | Role | Notes |
|---|---|---|---|
| Windows 11 desktop | local / RDP | Primary dev machine | 13900K + RTX 4090 + Arctic Liquid Freezer II 420 + Phanteks PWM hub driving 14 fans. **Not a Linux HIL.** Reserved for future Windows subproject hardware validation. |
| Proxmox host | `192.168.7.10:8006` web UI | Linux VM infrastructure | 5800X + RTX 3060 + Noctua air cooler. Spin up any Linux distro as a VM on demand. Primary Linux-validation path. |
| MiniPC ("ex-digital-sign") | `ssh phoenix@192.168.7.222` | Low-end edge-case HIL | Intel Celeron, small high-RPM fan, recycled commercial mediabox. Useful for "does ventd handle weird low-end hardware gracefully" but limited chip diversity. |

## Proxmox VMs (start on demand)

| VMID | Name | Distribution | Primary use |
|---|---|---|---|
| 200 | fc-test-alpine-319 | Alpine 3.19 (musl) | CGO_ENABLED=0 + musl validation |
| 201 | fc-test-debian12-secureboot | Debian 12 + Secure Boot | AppArmor + Secure Boot signing flow |
| 202 | fc-test-fedora-40 | Fedora 40 | SELinux + dnf packaging |
| 203 | fc-test-arch | Arch | rolling release; latest Go |
| 204 | fc-test-opensuse-tw | openSUSE Tumbleweed | zypper + SUSE family |
| 205 | fc-test-nixos-2405 | NixOS 24.05 | declarative deployment |
| 206 | fc-test-void-musl | Void Linux (musl) | runit init; non-systemd path |
| 207 | fc-test-ubuntu-2404 | Ubuntu 24.04 | primary reference distro |
| 950 | ventd-dev | Ubuntu 24.04 | primary CC working environment when using Linux |
| 9100–9104 | ventd-smoke-tpl-* | varies | fresh-install smoke templates |

## Known limitations

Surface these gaps to the developer; don't ask CC to work around them.

- **No native-Linux motherboard Super I/O HIL.** Desktop runs Windows; VMs don't have real Super I/O chips. Real NCT/ITE hwmon-write validation limited to the MiniPC (low chip diversity) or requires the developer to dual-boot.
- **GPU passthrough to VMs is fragile.** RTX 3060 can be passed through for NVML testing, but setup is per-VM and not always reliable.
- **No Corsair/NZXT/Lian Li AIO hardware.** Desktop AIO is Arctic Liquid Freezer II (hwmon PWM, not USB HID). Validating `internal/hal/liquid` against real hardware requires acquiring a Corsair Commander Core (or similar) or community contribution.
- **No IPMI/BMC hardware.** Validating `internal/hal/ipmi` requires a Supermicro/Dell/HPE server box — not currently available.
- **No Framework laptop, ARM SBC, or Apple Silicon.**

## SSH usage pattern

```sh
# Deploy a candidate binary without stopping the running service:
scp ./ventd phoenix@192.168.7.222:/tmp/ventd-candidate
ssh phoenix@192.168.7.222 '/tmp/ventd-candidate --probe-modules --dry-run'
ssh phoenix@192.168.7.222 'cat /sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name'
```

## Architecture reference

- `internal/hal/` — hardware abstraction layer. All backends implement `FanBackend`. Contract enforced by `TestHAL_Contract`.
- `internal/controller/` — control loop. Safety invariants bound to `.claude/rules/hwmon-safety.md` via `TestSafety_Invariants`.
- `internal/calibrate/` — PWM sweep + fingerprint-resumable calibration.
- `internal/watchdog/` — exit-path restoration of pre-ventd `pwm_enable`.
- `internal/web/` — HTTP API, auth, dashboard UI.
- `internal/hwdb/` — hardware fingerprint database + profile matcher.
- `cmd/ventd-recover/` — zero-allocation root helper for ungraceful exits.
