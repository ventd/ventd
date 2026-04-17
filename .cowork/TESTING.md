# Test Environment — Proxmox VMs and CI Lanes

This file documents the validation resources available to Cowork and CC.
CC prompts reference this document when test strategy extends beyond
`go test` + CI.

## Proxmox VM inventory

Proxmox host accessible at `192.168.7.10:8006` from the employer's
network. CC terminals running on the employer's dev box can SSH to
individual VMs by IP or hostname. Employer provides SSH credentials
out-of-band when CC first needs them; CC prompts should *request*
access rather than assume it.

### Dev VM

| VMID | Name       | Purpose                                           |
|------|------------|---------------------------------------------------|
| 950  | ventd-dev  | Primary CC working environment (the box CC runs on, or equivalent) |

### Per-distro fan-control test VMs (qemu/kvm)

These are full-distro VMs used to validate ventd against the matrix
of target Linux distributions. Not currently running in the
screenshot (all in stopped state), so CC must request the employer
boot the specific VM before testing against it.

| VMID | Name                        | Distribution           | Use case                                           |
|------|-----------------------------|------------------------|----------------------------------------------------|
| 200  | fc-test-alpine-319          | Alpine 3.19 (musl)     | musl-libc compatibility; CGO_ENABLED=0 validation |
| 201  | fc-test-debian12-secureboot | Debian 12 (Secure Boot)| AppArmor, systemd, Secure Boot signing flow       |
| 202  | fc-test-fedora-40           | Fedora 40              | SELinux, dnf packaging                            |
| 203  | fc-test-arch                | Arch                   | rolling release; latest Go toolchain              |
| 204  | fc-test-opensuse-tw         | openSUSE Tumbleweed    | SUSE family; zypper packaging                     |
| 205  | fc-test-nixos-2405          | NixOS 24.05            | declarative deployment; nix-drift CI validation   |
| 206  | fc-test-void-musl           | Void Linux (musl)      | runit init; non-systemd path                      |
| 207  | fc-test-ubuntu-2404         | Ubuntu 24.04           | primary reference distro                          |

### Fresh-install smoke templates (qemu/kvm)

Pristine snapshots used to validate the install script (scripts/install.sh)
on a clean, first-boot system. These are TEMPLATE VMs — CC should
request the employer clone them to a runtime VM before testing.

| VMID | Name                          | Role                                           |
|------|-------------------------------|------------------------------------------------|
| 9000 | ventd-tpl-ubuntu-2404         | Ubuntu 24.04 template                          |
| 9100 | ventd-smoke-tpl-ubuntu-24-04  | Smoke-test template, Ubuntu 24.04              |
| 9101 | ventd-smoke-tpl-debian-12     | Smoke-test template, Debian 12                 |
| 9102 | ventd-smoke-tpl-fedora        | Smoke-test template, Fedora                    |
| 9103 | ventd-smoke-tpl-arch          | Smoke-test template, Arch                      |
| 9104 | ventd-smoke-tpl-opensuse-tw   | Smoke-test template, openSUSE Tumbleweed       |

## Pre-existing CI lanes

Unit and integration tests already run in GitHub Actions across:
- ubuntu-latest (amd64, race)
- ubuntu-latest (arm64, race, via QEMU)
- fedora (container, race)
- arch (container, race)
- alpine-3.20 (container, CGO_ENABLED=0, no -race)
- cross-compile matrix (linux/amd64, linux/arm64)
- headless-chromium (go-rod E2E)
- nix-drift (NixOS validation)
- apparmor-parse-debian13
- govulncheck, golangci-lint, shellcheck

## When to use which

| Need                                    | Use                                                  |
|-----------------------------------------|------------------------------------------------------|
| Unit test / package test                | `go test -race ./...` in dev; CI validates full matrix |
| Compile cross-distro                    | Existing CI matrix                                   |
| systemd unit behaviour                  | fc-test-* VM (pick the distro that matches the scope) |
| install.sh first-boot validation        | Clone a 9xxx template to a fresh VM, run script      |
| AppArmor / SELinux profile validation   | fc-test-debian12-secureboot (AA) or fc-test-fedora-40 (SELinux) |
| hwmon behaviour with real sysfs entries | fc-test-* VM with guest kernel exposing hwmon chips (employer confirms) |
| actual PWM output / real fan speed      | Employer's physical rig — VMs cannot exercise this  |
| IPMI with real BMC                      | Employer's physical rig — VMs cannot exercise this  |
| USB-HID AIO (Corsair / NZXT / Lian Li)  | Employer's physical rig with USB passthrough, or  employer's rig directly |

## Hardware-gated milestones

The following phase-advance checkpoints require the employer's
physical rig and cannot be auto-validated via the Proxmox VMs alone.
Cowork pauses before dispatching the first PR of the next phase at
each of these:

| Gate                              | Why                                                                       |
|-----------------------------------|---------------------------------------------------------------------------|
| End of Phase 1 (HAL landing)      | First real-hardware test of the new interface against a real hwmon tree  |
| End of Phase 2 (backends)         | Each new backend (IPMI, liquid, crosec, pwmsys, asahi) needs its hardware |
| End of Phase 4 (control loop)     | MPC quieter-than-curve claim needs the rig                                |
| End of Phase 6 (cross-platform)   | macOS, Windows, BSD need their respective machines                        |
| Any pre-release validation run    | `validation/rig-check-*.sh` require the rig                               |
