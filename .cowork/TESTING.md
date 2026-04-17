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
| Desktop (MS-7D25)           | `ssh phoenix@192.168.7.209`   | Primary HIL rig — MSI MS-7D25, i9-13900K, 32 GB, RTX 4090, Ubuntu 24.04.4. Real hwmon, CPU package temp via coretemp, chassis fans, NVIDIA GPU. Runs `validation/rig-check-desktop.sh`. Phase 1 → Phase 2 gate validates here. |
| MiniPC (ex-digital-sign)    | `ssh phoenix@192.168.7.222`   | Secondary HIL rig — different chip family; detailed specs TBD. Confirms HAL doesn't overfit to one board. |
| Windows laptop              | _(TBD on demand)_             | P6-WIN-01/02 target — WMI thermal zones, ACPI fans, MSI / WinRing0-free path. |

### Desktop fingerprint (seed for P1-FP-01 hwdb)

Confirmed facts from Ubuntu System Info, 2026-04-18:

| Field           | Value                                            |
|-----------------|--------------------------------------------------|
| Hostname        | `phoenix-MS-7D25`                                |
| OS              | Ubuntu 24.04.4 LTS                               |
| DMI Board Vendor| `Micro-Star International Co., Ltd.`             |
| DMI Board Name  | `MS-7D25` (MSI MEG Z690 / Z690 PRO class board)  |
| CPU             | 13th Gen Intel® Core™ i9-13900K (32 threads — 8P+16E w/ HT) |
| Memory          | 32 GB                                            |
| GPU             | NVIDIA RTX 4090                                  |
| Disk            | 1.5 TB                                           |

When P1-FP-01 is ready to verify, CC should SSH to the desktop and
run `cat /sys/class/dmi/id/{board_vendor,board_name,board_version,product_family}`
to capture the canonical strings and cross-check them against the
above.

## GPUs

Two NVIDIA GPUs are present across the environment:

| GPU      | Location                                  | Current state                                 |
|----------|-------------------------------------------|-----------------------------------------------|
| RTX 3090 | Proxmox host                              | Passed through to Plex VM (100), reassignable |
| RTX 4090 | Desktop HIL (`phoenix@192.168.7.209`)     | Native on the rig; available for NVML testing |

For NVML validation:
- Prefer the RTX 4090 on the Desktop HIL — real-iron, no passthrough complexity.
- Use the 3090 + passthrough only if a test specifically needs Ada + Ampere coverage or requires VM isolation.

## Proxmox per-distro VMs (qemu/kvm)

Proxmox host at `192.168.7.10:8006`. Each VM is stopped unless
explicitly booted.

### Dev VM

| VMID | Name       | Purpose                                         |
|------|------------|-------------------------------------------------|
| 950  | ventd-dev  | Primary CC working environment                  |

### Fan-control test VMs

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

| VMID | Name                          | Role                                |
|------|-------------------------------|-------------------------------------|
| 9000 | ventd-tpl-ubuntu-2404         | Ubuntu 24.04 base template          |
| 9100 | ventd-smoke-tpl-ubuntu-24-04  | Ubuntu 24.04 smoke template         |
| 9101 | ventd-smoke-tpl-debian-12     | Debian 12 smoke template            |
| 9102 | ventd-smoke-tpl-fedora        | Fedora smoke template               |
| 9103 | ventd-smoke-tpl-arch          | Arch smoke template                 |
| 9104 | ventd-smoke-tpl-opensuse-tw   | openSUSE TW smoke template          |

## CC parallelism on the dev VM

CC terminals on VM 950 (`ventd-dev`) run one-per-shell. To run
multiple concurrently, use either:

**Option A — multiple local terminal tabs**: open N tabs in the
employer's terminal emulator, type `cc` in each. Each moshes to
the VM independently.

**Option B — tmux inside the mosh session**: one mosh session,
multiple tmux windows, each running `claude` in a dedicated git
working copy. Survives mosh disconnects.

Either way, each CC instance needs its own git working copy to
avoid branch collisions. One-time setup on the dev VM:

```bash
cd ~
for i in 1 2 3 4; do
  [ -d "ventd-cc$i" ] || git clone git@github.com:ventd/ventd.git "ventd-cc$i"
done
```

Then in each tab/tmux window: `cd ~/ventd-ccN && claude`.

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
| NVML read on real GPU                        | Desktop HIL (RTX 4090) preferred; 3090 passthrough if needed |
| NVML write (set fan speed)                   | Desktop HIL with coolbits/cap workaround         |
| IPMI with real BMC                           | Not yet — HARDWARE-REQUIRED                      |
| USB-HID AIO (Corsair/NZXT/Lian Li)           | Not yet — HARDWARE-REQUIRED                      |
| Framework laptop EC                          | Not yet — HARDWARE-REQUIRED                      |
| Windows HAL (WMI + ACPI)                     | Windows laptop                                   |
| macOS SMC                                    | Not yet — HARDWARE-REQUIRED                      |
| BSD / illumos                                | Proxmox VM (employer to build)                   |
| Raspberry Pi PWM                             | Not yet — HARDWARE-REQUIRED                      |
| Apple Silicon / Asahi                        | Not yet — HARDWARE-REQUIRED                      |

## Hardware-gated phase milestones (current state)

| Gate                                 | Status with current assets                                         |
|--------------------------------------|---------------------------------------------------------------------|
| End of Phase 1 (HAL landing)         | **Auto-validatable** — CC SSHes to Desktop + MiniPC, runs rig-check |
| End of Phase 2 (backends)            | **Partially** — hwmon + NVML ok; IPMI/liquid/crosec/SBC still HARDWARE-REQUIRED |
| End of Phase 4 (control loop)        | **Auto-validatable** — Desktop HIL; MPC quieter-than-curve measurable |
| End of Phase 6 (cross-platform)      | **Partially** — Windows laptop covers P6-WIN; macOS/BSD/Asahi/illumos/Android still required |
| Pre-release                          | Subset coverage; rig-check gaps surface as HARDWARE-REQUIRED        |

## CC prompt guidance — resource selection

1. **Pure Go / interface / fixture work** — `go test -race ./...` + CI. Majority of work stops here.
2. **Deployment / install / systemd / LSM** — Proxmox VM (per-distro or smoke template); employer boots on request.
3. **Real hwmon behaviour** — Desktop HIL. Deploy via SSH; read `/sys/class/hwmon/`; do NOT write PWM without explicit in-prompt authorisation.
4. **NVML** — prefer Desktop RTX 4090 over Proxmox passthrough.
5. **Windows / macOS / BSD / SBC / Asahi / IPMI / AIO USB-HID** — respective hardware must be present; flag HARDWARE-REQUIRED if not. Windows laptop covers P6-WIN.

## Task-to-resource map

| Task                | Resource                                                           |
|---------------------|--------------------------------------------------------------------|
| P1-HAL-01           | CI; Desktop HIL read-only validation before merge recommended      |
| P1-FP-01            | CI; Desktop + MiniPC DMI strings as hwdb seed data                 |
| P1-MOD-01 / 02      | CI; fc-test-* VM for `modules.alias` cross-distro                  |
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
| P6-MAC-*            | HARDWARE-REQUIRED                                                  |
| P6-BSD-*            | Proxmox VM (employer to build)                                     |
| P6-ILLUMOS-*        | Proxmox VM (employer to build)                                     |
| P6-ANDROID-*        | HARDWARE-REQUIRED                                                  |
| P7-ACOUSTIC-*       | HARDWARE-REQUIRED (USB mic)                                        |
| P7-FLOW-*           | gated on P2-LIQUID                                                 |
| P10-*               | CI only                                                            |

## SSH usage pattern for CC

```sh
scp ./ventd phoenix@192.168.7.209:/tmp/ventd-candidate
ssh phoenix@192.168.7.209 '/tmp/ventd-candidate --probe-modules --dry-run'
ssh phoenix@192.168.7.209 'cat /sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name'
```

CC must NEVER start/stop ventd as a real service on the rig
without explicit in-prompt authorisation. Running binaries in
dry-run / read-only / `/tmp` is always safe.
