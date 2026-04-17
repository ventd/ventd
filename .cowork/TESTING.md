# Test Environment — Hardware, VMs, and CI Lanes

Validation resources available to Cowork and CC. Reference this file
from CC prompts when validation extends beyond `go test` + CI.

## Real hardware (HIL — Hardware In the Loop)

CC terminals can SSH to these hosts directly. These are the only
resources that exercise **real fan behaviour**, **real thermal
sensors**, and **real PWM-to-RPM response curves**. Treat them as
scarce: stage changes in CI and Proxmox VMs first, then validate on
real hardware before declaring a task done.

| Host                        | Access                        | Purpose                                          |
|-----------------------------|-------------------------------|--------------------------------------------------|
| Desktop                     | `ssh phoenix@192.168.7.209`   | Primary HIL rig — real motherboard, real CPU sensors, real chassis fans. Runs `validation/rig-check-desktop.sh`. The Phase 1 → Phase 2 gate (HAL landing on a real hwmon tree) is validated here. |
| MiniPC (ex-digital-sign)    | `ssh phoenix@192.168.7.222`   | Secondary HIL rig — different chip family, confirms HAL doesn't overfit to one board. |
| Windows laptop              | _(TBD — employer provides on demand)_ | P6-WIN-01/02 target — WMI thermal zones, ACPI fans, MSI / WinRing0-free path. |

## GPU passthrough (NVML validation)

RTX 3090 lives in the Proxmox host. Currently passed through to the
Plex VM (100). Can be re-passed to a test VM on request — employer
switches the passthrough target when NVML validation is needed.
Used for: P1-HAL-01 NVML wrapper, Phase 7 flow-sensor surface,
cross-check against `Insufficient Permissions` workarounds
(masterplan/CHANGELOG #34).

## Proxmox per-distro VMs (qemu/kvm)

Proxmox host at `192.168.7.10:8006`. Each VM is stopped in the
screenshot — CC requests the employer boot a specific VMID before
testing against it.

### Dev VM

| VMID | Name       | Purpose                                         |
|------|------------|-------------------------------------------------|
| 950  | ventd-dev  | Primary CC working environment                  |

### Fan-control test VMs (full distros, long-lived)

| VMID | Name                        | Distribution           | Use case                                           |
|------|-----------------------------|------------------------|----------------------------------------------------|
| 200  | fc-test-alpine-319          | Alpine 3.19 (musl)     | musl-libc compat; CGO_ENABLED=0 validation        |
| 201  | fc-test-debian12-secureboot | Debian 12 (Secure Boot)| AppArmor, Secure Boot signing flow                |
| 202  | fc-test-fedora-40           | Fedora 40              | SELinux, dnf packaging                            |
| 203  | fc-test-arch                | Arch                   | rolling release; latest Go toolchain              |
| 204  | fc-test-opensuse-tw         | openSUSE Tumbleweed    | SUSE family; zypper packaging                     |
| 205  | fc-test-nixos-2405          | NixOS 24.05            | declarative deployment; nix-drift CI backstop     |
| 206  | fc-test-void-musl           | Void Linux (musl)      | runit init; non-systemd path                      |
| 207  | fc-test-ubuntu-2404         | Ubuntu 24.04           | primary reference distro                          |

### Fresh-install smoke templates

Clone these to a runtime VM for install-script first-boot validation.

| VMID | Name                          | Role                                |
|------|-------------------------------|-------------------------------------|
| 9000 | ventd-tpl-ubuntu-2404         | Ubuntu 24.04 base template          |
| 9100 | ventd-smoke-tpl-ubuntu-24-04  | Ubuntu 24.04 smoke template         |
| 9101 | ventd-smoke-tpl-debian-12     | Debian 12 smoke template            |
| 9102 | ventd-smoke-tpl-fedora        | Fedora smoke template               |
| 9103 | ventd-smoke-tpl-arch          | Arch smoke template                 |
| 9104 | ventd-smoke-tpl-opensuse-tw   | openSUSE TW smoke template          |

## Pre-existing CI lanes (GitHub Actions)

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

## When to use what

| Validation need                              | Use                                              |
|----------------------------------------------|--------------------------------------------------|
| Unit / package test                          | `go test -race ./...`; CI matrix confirms        |
| Cross-distro compile                         | Existing CI matrix                               |
| systemd unit behaviour                       | fc-test-* VM matching the distro                 |
| install.sh first-boot                        | Clone 9xxx template, run script                  |
| AppArmor / SELinux profile                   | fc-test-debian12-secureboot (AA) or fc-test-fedora-40 (SELinux) |
| hwmon with real sysfs entries                | Desktop HIL rig                                  |
| **Real PWM → RPM response**                  | **Desktop HIL or MiniPC HIL only** — VMs cannot |
| NVML read on real GPU                        | Test VM with RTX 3090 passthrough                |
| NVML write (set fan speed)                   | Desktop HIL (if it has the GPU) OR test VM with passthrough + coolbits/cap workaround |
| IPMI with real BMC                           | No current asset — employer to source or escalate |
| USB-HID AIO (Corsair/NZXT/Lian Li)           | No current asset — employer to source or escalate |
| Framework laptop EC                          | No current asset — employer to source or escalate |
| Windows HAL (WMI + ACPI)                     | Windows laptop (P6-WIN phase)                    |
| macOS SMC                                    | No current asset — employer to source or escalate |
| BSD / illumos                                | Proxmox VM with the relevant OS installed (employer to build) |
| Raspberry Pi PWM                             | No current asset — employer to source or escalate |
| Apple Silicon / Asahi                        | No current asset — employer to source or escalate |

## Hardware-gated phase milestones (current state)

| Gate                                 | Status with current assets                                         |
|--------------------------------------|---------------------------------------------------------------------|
| End of Phase 1 (HAL landing)         | **Auto-validatable** — CC SSHes to Desktop + MiniPC, runs rig-check |
| End of Phase 2 (backends)            | **Partially** — hwmon backend ok; IPMI/liquid/crosec need hardware the employer doesn't yet have. Asahi and SBC likewise. |
| End of Phase 4 (control loop)        | **Auto-validatable** — Desktop HIL; MPC quieter-than-curve measurable |
| End of Phase 6 (cross-platform)      | **Partially** — Windows laptop covers P6-WIN; macOS / BSD / Asahi / illumos / Android need their respective machines |
| Pre-release                          | Subset coverage; remaining rig-check scripts surface HARDWARE-REQUIRED gaps to employer |

## CC prompt guidance — when to reach for which resource

1. **Pure Go / interface / fixture work** — `go test -race ./...` + CI.
   The vast majority of work stops here. Do not SSH to the rig for
   this class.
2. **Deployment / install / systemd / LSM** — Proxmox VM
   (per-distro or smoke template). Employer boots on request.
3. **Real hwmon behaviour** — Desktop HIL. Deploy via SSH; read
   `/sys/class/hwmon/`; do NOT write PWM without explicit employer
   confirmation for the specific rig. First HAL validation is
   read-only; PWM writes come later with rig-check-desktop.sh.
4. **NVML** — request GPU passthrough to a test VM; run the NVML
   backend in that VM.
5. **Windows / macOS / BSD / SBC / Asahi / IPMI / AIO USB-HID** —
   the respective hardware must be present. Flag HARDWARE-REQUIRED
   if not. Windows laptop covers P6-WIN.

## Task-to-resource map (quick reference)

| Task                | Resource                                                           |
|---------------------|--------------------------------------------------------------------|
| P1-HAL-01           | CI; Desktop HIL read-only validation before merge recommended      |
| P1-FP-01            | CI only; real DMI fingerprints from Desktop HIL and MiniPC used as YAML-seed data (employer runs `cat /sys/class/dmi/id/*` when asked) |
| P1-MOD-01 / 02      | CI; VM for `modules.alias` parsing across distros                  |
| P2-IPMI-*           | HARDWARE-REQUIRED unless employer sources a BMC/IPMI box           |
| P2-LIQUID-*         | HARDWARE-REQUIRED unless employer sources an AIO                   |
| P2-CROSEC-*         | HARDWARE-REQUIRED unless employer sources a Framework/ThinkPad     |
| P2-PWMSYS-*         | HARDWARE-REQUIRED unless employer sources a Pi 5 / ARM NAS         |
| P2-ASAHI-*          | HARDWARE-REQUIRED unless employer sources an Apple Silicon Mac     |
| P3-INSTALL-*        | 9xxx smoke template VMs                                            |
| P3-MODPROBE-*       | Desktop HIL + fc-test-* VMs                                        |
| P3-UDEV-*           | fc-test-* VMs                                                      |
| P3-RECOVER-*        | Desktop HIL (recovery under failure) + CI                          |
| P4-*                | Desktop HIL (MPC quieter-than-curve claim lives here)              |
| P5-*                | Desktop HIL                                                        |
| P6-WIN-*            | Windows laptop                                                     |
| P6-MAC-*            | HARDWARE-REQUIRED — employer to source a Mac                       |
| P6-BSD-*            | Proxmox VM (employer to build FreeBSD/OpenBSD VMs)                 |
| P6-ILLUMOS-*        | Proxmox VM (employer to build OmniOS/OpenIndiana)                  |
| P6-ANDROID-*        | HARDWARE-REQUIRED — employer to source an Android device           |
| P7-ACOUSTIC-*       | HARDWARE-REQUIRED — employer to source a USB mic                   |
| P7-FLOW-*           | gated on P2-LIQUID                                                 |
| P10-*               | CI only                                                            |

CC terminals SSH to real hardware as the employer's user `phoenix`.
Common pattern:

```sh
scp ./ventd phoenix@192.168.7.209:/tmp/ventd-candidate
ssh phoenix@192.168.7.209 '/tmp/ventd-candidate --probe-modules --dry-run'
ssh phoenix@192.168.7.209 'cat /sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name'
```

CC must NEVER start/stop ventd as a real service on the rig without
an explicit in-prompt instruction. Running binaries in dry-run /
read-only / `/tmp` is fine.
