# Claude Code instructions for ventd

This file is the first thing Claude Code reads in a new session. Keep it short.

## Workflow

ventd is a Linux fan-control daemon written in Go. Work is spec-driven: every
non-trivial change starts from a markdown spec in `specs/` that defines scope,
PR sequence, definition of done, and explicit non-goals.

**Before writing any code:**
1. Check `specs/` for the spec covering this task. If there is none, stop and
   ask the user to write one before proceeding. Do not improvise.
2. Read the spec end-to-end, including the "Explicit non-goals" section.
3. Read any `.claude/rules/*.md` files referenced by the spec. These are
   test-binding invariants — every rule maps 1:1 to a subtest.

## Tests

Standard invocation:

```
go test -race ./...
```

Targeted:

```
go test -race ./internal/<package>/...
go test -run TestName ./internal/<package>
```

The `-tags=e2e` suite drives a real headless Chromium and is gated behind a
build tag. Don't run it unless the spec says to.

## Commits

- Present tense, imperative: "Add IPMI safety sentinel" not "Added" or "Adds".
- One logical change per commit. Green tests at every commit boundary.
- Reference issues as `Fixes #123` in the commit body, not the subject.

## What blocks a merge

See `CONTRIBUTING.md`. Summary: `go vet` clean, `go test -race` green,
`golangci-lint` clean, watchdog invariant preserved on every exit path,
single-static-binary constraint preserved (no CGO, NVML stays dlopen).

## What Claude Code does NOT do

- **No subagents.** Each session is a single linear sequence. Parallel agents
  were the root cause of previous runaway token spend.
- **No mid-session Opus.** Design work happens separately in claude.ai on the
  user's Max plan (flat-rate); implementation happens here on Sonnet (per
  token). Never call Opus from a CC session.
- **No autonomous long-running sessions.** If a task runs >30 minutes of real
  work without the user confirming direction, stop and check in.
- **No improvisation past spec ambiguity.** If the spec is incomplete, edit
  the spec first, commit the spec change, then continue.
- **No scope creep.** The spec's "Explicit non-goals" list is binding. If a
  tempting adjacent change appears, note it in a GitHub issue and move on.

## Available infrastructure

The developer has this hardware and virtual infrastructure available. Spec
files that need hardware access reference these by role, not by hostname, so
the inventory lives here.

### Real hardware

| Machine | Access | Role | Notes |
|---|---|---|---|
| Windows 11 desktop | local / RDP | Primary dev machine | 13900K + RTX 4090 + Arctic Liquid Freezer II 420 + Phanteks PWM hub driving 14 fans. **Not a Linux HIL.** Reserved for future Windows subproject hardware validation. |
| Proxmox host | `192.168.7.10:8006` web UI | Linux VM infrastructure | 5800X + RTX 3060 + Noctua air cooler. Spin up any Linux distro as a VM on demand. Primary Linux-validation path. |
| MiniPC ("ex-digital-sign") | `ssh phoenix@192.168.7.222` | Low-end edge-case HIL | Intel Celeron, small high-RPM fan, recycled commercial mediabox. Useful for "does ventd handle weird low-end hardware gracefully" but limited chip diversity. |

### Proxmox VMs (start on demand)

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

### Known limitations

These are real constraints the developer works around. Don't ask CC to work
around them; surface the gap and let the developer decide.

- **No native-Linux motherboard Super I/O HIL.** The desktop runs Windows;
  VMs don't have real Super I/O chips. Real NCT/ITE hwmon-write validation
  is limited to the MiniPC (low chip diversity) or requires the developer
  to dual-boot the desktop or borrow a rig.
- **GPU passthrough to VMs is fragile.** The RTX 3060 in the Proxmox host
  can be passed through to a VM for NVML testing, but setup is per-VM and
  not always reliable.
- **No Corsair/NZXT/Lian Li AIO hardware.** Desktop AIO is an Arctic Liquid
  Freezer II (hwmon PWM, not USB HID). Validating the `internal/hal/liquid`
  backend against real hardware requires either the developer to acquire a
  Corsair Commander Core (or similar), or a community contributor to run
  validation on their rig.
- **No IPMI/BMC hardware.** Validating `internal/hal/ipmi` against a real
  BMC requires a Supermicro/Dell/HPE server box — not currently available.
- **No Framework laptop, ARM SBC, or Apple Silicon.** All HARDWARE-REQUIRED.

### SSH usage pattern

```sh
# Deploy a candidate binary to the MiniPC without stopping the running service:
scp ./ventd phoenix@192.168.7.222:/tmp/ventd-candidate
ssh phoenix@192.168.7.222 '/tmp/ventd-candidate --probe-modules --dry-run'
ssh phoenix@192.168.7.222 'cat /sys/class/dmi/id/board_vendor /sys/class/dmi/id/board_name'
```

**CC must NEVER start/stop ventd as a service on any rig without explicit
in-prompt authorisation.** Dry-run/read-only/`/tmp`-based runs are always
safe; `systemctl` / `mv /tmp/ventd-candidate /usr/local/bin/ventd` is not.

### Hardware-gated DoD

When a spec has a step marked `HARDWARE-REQUIRED`:

1. Stop the CC session at that step.
2. Tell the developer which hardware is needed and which command to run.
3. Wait for the developer to paste real output.
4. Never fabricate test results or claim DoD on a hardware gate without
   verified evidence.

## Architecture reference

- `internal/hal/` — hardware abstraction layer. All backends implement
  `FanBackend`. Contract enforced by `TestHAL_Contract`.
- `internal/controller/` — the control loop. Safety invariants bound to
  `.claude/rules/hwmon-safety.md` via `TestSafety_Invariants`.
- `internal/calibrate/` — PWM sweep + fingerprint-resumable calibration.
- `internal/watchdog/` — exit-path restoration of pre-ventd `pwm_enable`.
- `internal/web/` — HTTP API, auth, dashboard UI.
- `internal/hwdb/` — hardware fingerprint database + profile matcher.
- `cmd/ventd-recover/` — zero-allocation root helper for ungraceful exits.

The product promise is zero-config, zero-terminal fan control on any Linux
box. Every change is judged against that promise. Added complexity that a
first-time user would have to understand is harder to merge than removed
complexity.
