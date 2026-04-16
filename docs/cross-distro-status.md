# Cross-distro install status

Human-owned record of which Linux distros and architectures the current
ventd release has been smoke-tested on. The cells are promoted by hand
after reviewing the per-run detail under
[docs/cross-distro-runs/](cross-distro-runs/).

Populated by `scripts/cross-distro-smoke.sh` — see
[cross-distro-smoke.md](cross-distro-smoke.md) for the harness and the
definition of a pass.

Legend:
- `—` — not yet tested at this version
- `PASS` — full install path (install.sh + `systemctl is-active` + `/api/ping` 200)
- `PASS-VM` — VM target where install.sh's preflight correctly refused
  (cloud kernel exposes no `/sys/class/hwmon` sensors); proves cross-distro
  installability without the hardware portion of the assertion
- `FAIL` — last known failure (tag noted in parentheses)
- `SKIP` — intentionally out of scope for this arch

| Distro               | amd64               | arm64 | Notes |
|----------------------|:-------------------:|:-----:|-------|
| Ubuntu 24.04         | PASS (v0.2.0)       |   —   | full install path — service active + /api/ping 200 |
| Debian 12            | PASS-VM (v0.2.0)    |   —   | cloud kernel omits hwmon; install.sh preflight correctly refused |
| Fedora 40            | PASS (v0.2.0)       |   —   | full install path |
| Arch                 |          —          |   —   | `virt-customize` fails baking qga: pacman Landlock sandbox unsupported by libguestfs appliance kernel. Needs a boot-and-install template prep path. |
| openSUSE Tumbleweed  | PASS (v0.2.0)       |   —   | full install path |
| Void (glibc)         |          —          |   —   | no glibc cloud image on the pve host (`/var/lib/vz/template/iso/void-live-musl.iso` is musl + live ISO, not cloud-init). Needs a separate template build. |
| Alpine 3.19          |          —          |   —   | `alpine-3.19-nocloud.qcow2` has no cloud-init; needs a different prep path (manual sudo user + qga install). |

Last updated: 2026-04-16, first matrix run against v0.2.0.

arm64 coverage is not wired up yet — every row reads "—" on that column
until the Proxmox host has arm64 templates and the harness config has a
corresponding TEMPLATE_VMIDS entry.

---

## Run footer — 2026-04-16 — ventd v0.2.0

- Pass: 4
- Fail: 0
- Skip: 0
- Detail: [docs/cross-distro-runs/2026-04-16-v0.2.0.md](cross-distro-runs/2026-04-16-v0.2.0.md)

_Phoenix: review detail, then promote per-distro cells in the table above._
