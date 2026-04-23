# Test Environment — Hardware, VMs, and CI Lanes

Available validation resources for ventd development. Reference this file
when validation extends beyond `go test` + CI.

## Real hardware

### Windows 11 desktop (developer workstation)

| Attribute | Value |
|---|---|
| CPU | 13th Gen Intel Core i9-13900K (32 threads) |
| GPU | NVIDIA RTX 4090 |
| RAM | 32 GB |
| Cooling | Arctic Liquid Freezer II 420 mm (hwmon PWM, not USB AIO) |
| Fans | 14 chassis fans, daisy-chained via Phanteks PWM hub |
| OS | Windows 11 |
| Role | Primary developer workstation. **Not a Linux HIL.** Reserved for future Windows subproject hardware validation. |

The Phanteks PWM hub topology means 14 physical fans run from a single
motherboard PWM channel. From ventd's perspective this is one PWM channel
with one tach-reporting fan (the Phanteks uses one fan slot for RPM
feedback; the rest are sync'd but tach-less).

### Proxmox host (primary Linux VM infrastructure)

| Attribute | Value |
|---|---|
| CPU | AMD Ryzen 7 5800X |
| GPU | NVIDIA RTX 3060 (passthrough-capable) |
| Cooling | Noctua air cooler |
| Host access | `https://192.168.7.10:8006` |
| Role | Run any Linux distro as a VM on demand. Primary Linux-validation path. |

VMs are stopped unless explicitly booted. The host's own fans are not
used as a ventd target — use VMs for Linux testing; the Windows desktop
or the MiniPC for hardware validation.

### MiniPC (secondary HIL)

| Attribute | Value |
|---|---|
| CPU | Intel Celeron (exact SKU TBD) |
| Cooling | Small high-RPM HSF |
| Access | `ssh phoenix@192.168.7.222` |
| Role | Low-end edge-case Linux HIL. Useful for "does ventd not brick weird hardware." |

This is a recycled commercial mediabox. Limited chip diversity — one Super
I/O, one fan — but it's the only dedicated Linux hardware available.

## Infrastructure gaps (be honest about these)

Ventd is a Linux fan controller, and the developer's main machine is
Windows. This has consequences:

- **No native-Linux DIY-motherboard HIL.** NCT/ITE Super I/O write validation
  on a diverse chip matrix needs either community validation reports or
  the developer occasionally dual-booting the desktop to Linux.
- **AIO validation requires hardware acquisition.** The desktop AIO is an
  Arctic (hwmon-only). The `internal/hal/liquid` backend needs a Corsair
  Commander Core or similar USB HID device to validate. Not currently
  owned.
- **IPMI/BMC validation requires borrowed hardware.** No Supermicro/Dell/HPE
  server in inventory.
- **Framework laptop, ARM SBC, Apple Silicon all absent.**

These gaps do NOT block development — they block final hardware DoD on
specific specs. Pure-Go work (controller, curves, calibration, HAL
interfaces) runs entirely in CI or in Proxmox VMs.

## Proxmox VMs

### Dev VM

| VMID | Name | Purpose |
|---|---|---|
| 950 | ventd-dev | Linux CC working environment when developer is on Linux |

### Fan-control test VMs

| VMID | Name | Distribution | Use case |
|---|---|---|---|
| 200 | fc-test-alpine-319 | Alpine 3.19 (musl) | musl-libc compat; CGO_ENABLED=0 |
| 201 | fc-test-debian12-secureboot | Debian 12 (Secure Boot) | AppArmor, Secure Boot signing |
| 202 | fc-test-fedora-40 | Fedora 40 | SELinux, dnf packaging |
| 203 | fc-test-arch | Arch | rolling release; latest Go |
| 204 | fc-test-opensuse-tw | openSUSE Tumbleweed | zypper, SUSE family |
| 205 | fc-test-nixos-2405 | NixOS 24.05 | declarative deployment |
| 206 | fc-test-void-musl | Void Linux (musl) | runit init; non-systemd |
| 207 | fc-test-ubuntu-2404 | Ubuntu 24.04 | primary reference distro |

### Fresh-install smoke templates

| VMID | Name | Role |
|---|---|---|
| 9000 | ventd-tpl-ubuntu-2404 | Ubuntu 24.04 base template |
| 9100 | ventd-smoke-tpl-ubuntu-24-04 | Ubuntu 24.04 smoke template |
| 9101 | ventd-smoke-tpl-debian-12 | Debian 12 smoke template |
| 9102 | ventd-smoke-tpl-fedora | Fedora smoke template |
| 9103 | ventd-smoke-tpl-arch | Arch smoke template |
| 9104 | ventd-smoke-tpl-opensuse-tw | openSUSE TW smoke template |

## When to use what

| Validation need | Use |
|---|---|
| Unit / package tests | `go test -race ./...` on dev machine or VM |
| Cross-distro compile | CI matrix (automatic on PR) |
| systemd unit behaviour | fc-test-* VM matching the distro |
| install.sh first-boot | 9xxx smoke template (clone + run) |
| AppArmor / SELinux profile | fc-test-debian12-secureboot / fc-test-fedora-40 |
| hwmon with real sysfs entries | MiniPC (limited chip diversity) |
| NCT6798/IT87 Super I/O writes | **GAP — not currently available.** |
| Real PWM → RPM response | MiniPC (low-end only) |
| NVML read on real GPU | RTX 3060 via passthrough to a VM |
| NVML write (set fan speed) | As above, with coolbits/cap workaround |
| IPMI with real BMC | **GAP — HARDWARE-REQUIRED.** |
| USB HID AIO (Corsair/NZXT/Lian Li) | **GAP — HARDWARE-REQUIRED.** |
| Framework laptop EC | **GAP — HARDWARE-REQUIRED.** |
| Raspberry Pi / ARM SBC PWM | **GAP — HARDWARE-REQUIRED.** |
| Apple Silicon / Asahi | **GAP — HARDWARE-REQUIRED.** |
| Windows HAL (post-v1.0 subproject) | Windows 11 desktop |

## CC guidance — resource selection

1. **Pure Go / interface / fixture work** — `go test -race ./...` + CI.
   Majority of work stops here. No VM, no SSH.
2. **Deployment / install / systemd / LSM** — boot a fc-test-* VM or
   9xxx smoke template. Snapshot first; revert after.
3. **Real hwmon behaviour** — MiniPC via SSH. Read-only first;
   **never write PWM** without explicit in-prompt authorisation from the
   developer.
4. **NVML** — RTX 3060 passthrough to a VM. Prefer read paths; writes
   require coolbits/cap workaround.
5. **Anything in the GAP rows above** — flag `HARDWARE-REQUIRED` in the
   PR and in the spec DoD. Do not claim DoD without real-hardware
   evidence.

## Task-to-resource map

| Task prefix | Resource |
|---|---|
| P1-HAL-* | CI; VMs for validation |
| P1-FP-* | CI; MiniPC DMI strings as hwdb seed data |
| P1-MOD-* | CI; fc-test-* VMs for `modules.alias` cross-distro |
| P2-IPMI-* | **HARDWARE-REQUIRED** |
| P2-LIQUID-* | **HARDWARE-REQUIRED** |
| P2-CROSEC-* | **HARDWARE-REQUIRED** |
| P2-PWMSYS-* | **HARDWARE-REQUIRED** |
| P2-ASAHI-* | **HARDWARE-REQUIRED** |
| P3-INSTALL-* | 9xxx smoke-template VMs |
| P3-MODPROBE-* | fc-test-* VMs |
| P3-UDEV-* | fc-test-* VMs |
| P3-RECOVER-* | MiniPC + CI |
| P4-PI-*, P4-HYST-*, P4-DITHER-* | CI for unit tests; MiniPC for end-to-end validation |
| P4-SLEEP-*, P4-INTERFERENCE-*, P4-STEP-*, P4-HWCURVE-* | CI via fakedbus/fakehwmon; MiniPC for real hardware |
| P4-MPC-* | CI + long-run validation on MiniPC |
| P5-* | MiniPC for capture/calibration; VMs for profile matching |
| P7-ACOUSTIC-* | **HARDWARE-REQUIRED** (USB mic) |
| P8-* | VMs |
| P10-* | CI only |

## SSH usage pattern

```sh
# Safe: deploy a candidate binary and run it in dry-run mode
scp ./ventd phoenix@192.168.7.222:/tmp/ventd-candidate
ssh phoenix@192.168.7.222 '/tmp/ventd-candidate --probe-modules --dry-run'
ssh phoenix@192.168.7.222 'cat /sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name'

# Unsafe: requires explicit authorisation from developer in the chat
# (would affect a running service)
ssh phoenix@192.168.7.222 'sudo systemctl restart ventd'
```

CC must NEVER start, stop, or restart ventd as a running service on any
rig without explicit in-prompt authorisation. Running binaries under
`/tmp` with `--dry-run` or read-only flags is always safe.

## Pre-existing CI lanes

- `build-and-test-ubuntu` (amd64, race)
- `build-and-test-ubuntu-arm64` (QEMU, race)
- `build-and-test-fedora` (container, race)
- `build-and-test-arch` (container, race)
- `build-and-test-alpine` (container, CGO_ENABLED=0, no -race)
- `cross-compile-matrix` (linux/amd64, linux/arm64)
- `headless-chromium` (go-rod E2E)
- `nix-drift` (NixOS validation)
- `apparmor-parse-debian13`
- `govulncheck`, `golangci-lint`, `shellcheck`
- `meta-lint` (rule-to-subtest binding)
- `drew-audit` (release-gated, workflow_dispatch)
- `pre-release-check` (release-gated, workflow_dispatch)

## Hardware-gated phase milestones (current state)

| Gate | Status |
|---|---|
| End of Phase 1 (HAL) | **Complete** — shipped in v0.3.x |
| End of Phase 2 (backends) | **Partial** — IPMI landed, hwmon + NVML in CI; LIQUID/CROSEC/PWMSYS/ASAHI still HARDWARE-REQUIRED |
| End of Phase 4 (control loop) | **Auto-validatable** on VMs + MiniPC; real NCT/ITE writes are GAP |
| End of Phase 5 (profiles) | **Auto-validatable** — profile capture + matching runs in CI |
| End of Phase 6 (Windows) | **Moved to separate subproject** — see main masterplan §16 |

## Hardware-gated matrix

| Test | Build tag | Prerequisites |
|---|---|---|
| IPMI sidecar privilege boundary | `ipmi_integration` | systemd-run + root |

## Solo-dev realities

- VMs are free. Use them.
- Hardware access costs time (boot, SSH, read, revert) — use sparingly.
- When HARDWARE-REQUIRED is the blocker, either buy the hardware,
  borrow it, or ship without real-hardware DoD and flag the gap in the
  release notes. Don't fake validation.
