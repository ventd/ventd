# ventd

[![CI](https://github.com/ventd/ventd/actions/workflows/ci.yml/badge.svg)](https://github.com/ventd/ventd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ventd/ventd?sort=semver)](https://github.com/ventd/ventd/releases)
[![Release date](https://img.shields.io/github/release-date/ventd/ventd)](https://github.com/ventd/ventd/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/ventd/ventd)](https://github.com/ventd/ventd/blob/main/go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/ventd/ventd/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-lightgrey)](#supported-platforms)

**Automatic Linux fan control. Install, open the browser, click Apply — ventd handles the rest.**

One static binary, one install command, one URL. Hardware detection, calibration, curve editing, and recovery all happen in the web UI. The terminal install command is the last terminal command you need to run.

> [!NOTE]
> **ventd is pre-1.0.** Safety guarantees are production-quality and verified by tests CI enforces. The config schema and curve format may evolve before v1.0. See [What's coming](#whats-coming) for the smart-mode roadmap to v0.6.0.

## Why ventd

Existing Linux fan tools assume you're willing to write YAML by hand, run `liquidctl` as a Python sidecar, and figure out which Super I/O chip your motherboard uses. ventd doesn't. It enumerates everything writable through `hwmon`, `NVML`, and a native USB HID stack, calibrates each fan's start/stop PWM and PWM→RPM curve in the background, and gives you a browser tab to edit curves in. No config file, no Python runtime, no root daemon.

It is also — to our knowledge — the only Linux fan daemon in its class that runs unprivileged. fan2go and CoolerControl both run as `User=root`. ventd runs as `User=ventd` with an empty capability bounding set. See [Safety](#safety).

[![ventd dashboard — live fan speeds, temperatures, and per-fan curves](https://github.com/ventd/ventd/raw/main/docs/images/dashboard.png)](/ventd/ventd/blob/main/docs/images/dashboard.png)

*Dashboard: live fan PWM and RPM streamed from the daemon, per-fan curves editable in place.*

[![ventd first-boot setup — step-by-step wizard, no config yet](https://github.com/ventd/ventd/raw/main/docs/images/setup.png)](/ventd/ventd/blob/main/docs/images/setup.png)

*First boot: a step-by-step setup wizard walks you through hardware detection, calibration, and curve review. No config file, no terminal.*

### More pages

<table>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/devices.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/devices.png" width="440" alt="Devices" /></a><br /><sub>Devices — all fans and controllers enumerated</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/sensors.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/sensors.png" width="440" alt="Sensors" /></a><br /><sub>Sensors — temperature and voltage readings</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/curve-editor.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/curve-editor.png" width="440" alt="Curve editor" /></a><br /><sub>Curve editor — drag-to-edit with stall zone overlay</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/calibration.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/calibration.png" width="440" alt="Calibration" /></a><br /><sub>Calibration — per-fan sweep with live progress</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/schedule.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/schedule.png" width="440" alt="Schedule" /></a><br /><sub>Schedule — time-based curve switching</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/logs.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/logs.png" width="440" alt="Logs" /></a><br /><sub>Logs — live log stream with level filtering</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/settings.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/settings.png" width="440" alt="Settings" /></a><br /><sub>Settings — daemon config, TLS, and webhooks</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/dashboard-stale.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/dashboard-stale.png" width="440" alt="Dashboard stale state" /></a><br /><sub>Dashboard — stale-data banner when daemon is unreachable</sub></td>
</tr>
</table>

## Features

* **Automatic hardware detection.** Enumerates every writable fan control the kernel exposes via `hwmon` (motherboard Super I/O chips — Nuvoton, ITE, AMD K10Temp, Intel coretemp, and the rest) plus NVIDIA GPUs through runtime-loaded NVML. Reads AMD GPU temperatures through the amdgpu hwmon layer. Intel Arc reads as monitor-only.
* **Native USB HID for AIO pumps.** Corsair Commander Core, Core XT, and Commander ST shipped in v0.4.0 — talking directly to the device through a pure-Go hidraw stack with no `liquidctl` Python sidecar. Read-only by default; writes opt-in behind `--enable-corsair-write`.
* **IPMI for server BMCs.** ASRock Rack, Supermicro, and other server boards exposing IPMI fan control. Shipped in v0.3.1.
* **Hardware database (52 boards, 6 vendors).** Curated catalog covering MSI, ASUS, Gigabyte, ASRock, Dell (consumer + PowerEdge), HP, HPE, Lenovo (IdeaPad/ThinkPad/Legion), Supermicro, and Raspberry Pi. Three-tier matcher: exact board match, then BIOS-version glob, then chip-family fallback. GPU vendor coverage: NVIDIA (NVML), AMD (amdgpu), Intel (i915/xe). Shipped in v0.5.0. The catalog is a fast-path overlay — smart-mode probes and controls hardware without a matching board profile, using the profile as an optimisation when it exists.
* **Calibration safety: runtime probe + apply-path enforcement.** Calibration produces a real per-PWM probe result. The apply path refuses to write to channels that haven't been runtime-probed or are flagged unsupported in the catalog. Shipped in v0.5.0.
* **Diagnostic bundle.** `ventd diag` produces a redacted NDJSON bundle for support and bug reports. Built-in redactor with fuzz-tested anonymisation. Shipped in v0.5.0.
* **Automatic calibration.** Measures start PWM, stop PWM, max RPM, and the full PWM→RPM curve per fan. Runs server-side; survives browser disconnect and daemon restart. Abortable from the UI. The curve editor uses calibration data to draw the stall zone in red, so you can't accidentally set a curve below the fan's stop threshold.
* **Hardware change detection.** Plug a new fan or GPU in; ventd notices within a second via `AF_NETLINK` uevents (capped at a 10-second rescan when unavailable) and offers to add it.
* **Zero terminal after install.** Hardware scan, dependency install, calibration, curve editing, and service control all happen in the web UI.
* **Single static binary.** `CGO_ENABLED=0`. NVML loaded at runtime via `dlopen`; GPU features disable silently if the library is absent. No Python, Node, or runtime dependencies beyond libc.

## Safety

ventd controls physical hardware. Two things follow from that, and both are load-bearing design decisions rather than marketing copy.

**The daemon runs as an unprivileged user.** The shipped systemd unit sets `User=ventd` with an empty `CapabilityBoundingSet` and empty `AmbientCapabilities` — no `CAP_DAC_OVERRIDE`, no `CAP_SYS_RAWIO`, nothing. Write access to hwmon PWM sysfs files comes from a DAC grant via the installed udev rule (`deploy/90-ventd-hwmon.rules` chgrps the files to the `ventd` group). A process compromise lands the attacker as `ventd:ventd`, not as root. To our knowledge ventd is the only Linux fan daemon in its class that does this — fan2go and CoolerControl both run as `User=root`.

**Every exit path restores firmware control within two seconds.** Two layers, working together:

* **Graceful exits** (`SIGTERM`, `SIGINT`, panic inside a recovered frame) trigger the user-space watchdog in `internal/watchdog`, which restores each fan's pre-ventd `pwm_enable` value. Per-entry panic recovery: one fan's restoration failing never aborts the loop for the rest. Fallback when the original value was unrecordable: write PWM=255 (hwmon) or release to driver auto (NVIDIA).
* **Ungraceful exits** (`SIGKILL`, OOM kill, hardware-watchdog timeout, panic escaping the defer chain) are caught by a separate root-privileged binary, `ventd-recover`, fired via `OnFailure=ventd-recover.service` on the main unit. It walks every `/sys/class/hwmon/hwmon*/pwm<N>_enable` file and writes `1`. Zero heap allocations on the hot path; always exits 0 to avoid systemd re-entering the OnFailure chain. The main daemon's `WatchdogSec=2s` ensures a hung main loop gets SIGKILLed and the recovery path fires; this is the mechanism behind the "within two seconds" promise.

**Calibration cannot strand a fan at zero.** Sweeps that drive PWM to 0 are watched by a per-fan sentinel (`internal/calibrate/safety.go`) that escalates to a quiet floor (`SafePWMFloor = 30`, roughly 12% duty — above start-PWM of nearly every fan on the market) if the zero state persists for more than two seconds. A hung calibration goroutine cannot leave a fan stopped under load.

Full model, failure-class breakdown, and the things we explicitly do **not** guarantee (kernel panic, power loss — userspace code never runs in those cases) in [docs/safety.md](https://github.com/ventd/ventd/blob/main/docs/safety.md).

Report any case where ventd leaves a fan in an unsafe state as a [SECURITY.md](https://github.com/ventd/ventd/blob/main/SECURITY.md) issue, not a regular bug.

## What's coming

**ventd is being built to learn your machine.** Every fan controller today runs the same loop: temperature rises → fans spin up. By the time fans are at speed, the silicon has already spiked. Reactive control with a user-edited curve is the ceiling of what fan2go, CoolerControl, thinkfan, and lm-sensors fancontrol all do.

The v0.5.1 → v0.6.0 roadmap breaks that ceiling through **smart mode**: behavioral detection instead of enumeration, continuous observation that builds a per-fan response model, thermal-coupling maps between sensors and fans, and a confidence-gated controller that blends reactive (PI) and predictive (learned) output as confidence accumulates. The catalog shifts from prerequisite to fast-path overlay — ventd probes and controls hardware without a matching board profile, adding the profile as an optimisation when it exists.

Three layers of continuous learning, each usable on its own:

* **Layer A — per-fan response curve.** Passive observation plus opportunistic active probing. After a few days of normal use, ventd knows each fan's PWM→RPM relationship and stall zone. No user interaction required.
* **Layer B — per-channel thermal coupling.** Watches which temperature sensors predict which fan loads. Enables feed-forward: ramp before the heat arrives.
* **Layer C — marginal-benefit and saturation detection (RLS).** Learns which fan speed changes actually move temperature — distinguishing fans that matter from fans that are acoustically costly but thermally irrelevant.

The patch sequence toward the v0.6.0 smart-mode tag:

| Tag | Scope |
| --- | --- |
| v0.5.0.1 | Persistent state foundation (shipped) |
| v0.5.1 | Catalog-less probe + three-state wizard (Control / Monitor-only / Refused) |
| v0.5.2 | Polarity midpoint disambiguation |
| v0.5.3 | Envelope probe + user-idle gate + load monitor |
| v0.5.4 | Passive observation logging (Layer A foundation) |
| v0.5.5 | Opportunistic active probing for Layer A gaps |
| v0.5.6 | Workload signature learning and classification |
| v0.5.7 | Per-channel thermal-coupling map (Layer B) |
| v0.5.8 | Marginal-benefit and saturation detection (Layer C) |
| v0.5.9 | Confidence-gated blended controller + confidence UX |
| v0.5.10 | Doctor recovery surface + internals consolidation |
| **v0.6.0** | **TAG: smart-mode complete** |

Three user-facing presets ship with the controller: **Silent**, **Balanced**, and **Performance**. No thermal targets to configure; ventd infers the right curve for your workload from observed data.

**Hardware coverage continues in parallel:** NZXT and Lian Li USB AIOs, laptop embedded controllers (Framework, ThinkPad, Dell), ARM SBC PWM (Raspberry Pi), Apple Silicon via Asahi. **Cross-platform** (Windows, macOS, FreeBSD) is post-v0.6.0.

Phase 1 (HAL foundation, hardware database) shipped in v0.3.0. Phase 2 (multi-backend support — IPMI in v0.3.1, Corsair AIO in v0.4.0, 52-board catalog and GPU vendor coverage in v0.5.0) shipped in v0.5.0. Phase 3 (smart mode — persistent state in v0.5.0.1, catalog-less probe through confidence-gated control, targeting v0.6.0) is in progress.

Detailed design in [specs/spec-smart-mode.md](https://github.com/ventd/ventd/blob/main/specs/spec-smart-mode.md).

## Install

ventd runs as an unprivileged system user with no root capabilities (see [Safety](#safety) above). The install script is small and plaintext — read it before you run it:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh -o install.sh
less install.sh           # read it — it's ~150 lines
sudo bash install.sh
```

If you already trust the script, or you're in a trusted-provisioning environment (container image bake, Ansible role, CI), the one-line form works:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

Either way, the script detects your architecture and init system (systemd, OpenRC, or runit), downloads the binary, **verifies its SHA-256 against the published `checksums.txt` for the release**, drops it at `/usr/local/bin/ventd`, installs the service file, enables it, and starts the daemon. It prints one thing: the URL to open in your browser.

Open the printed URL. The setup wizard prompts for a one-time token on first run. The daemon does **not** log the token to journald; it writes it to `/run/ventd/setup-token` (0600, root-only) and, if a controlling TTY is attached, to that TTY. Recover it with:

```
sudo cat /run/ventd/setup-token
```

## Supported platforms

* **Distributions:** Ubuntu, Debian, Fedora, RHEL, CentOS, Arch, Manjaro, openSUSE, Alpine, Void, NixOS
* **Init systems:** systemd, OpenRC, runit
* **Architectures:** amd64, arm64
* **C library:** glibc and musl
* **GPU:** NVIDIA (via NVML — temperature reading works out of the box; GPU fan *writes* require a one-time udev rule, see [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)); AMD (via amdgpu hwmon). Intel Arc is read-only at the kernel level; monitoring only.
* **Liquid coolers:** Corsair Commander Core / Core XT / ST (native USB HID, no liquidctl required).
* **Server BMCs:** IPMI fan control on ASRock Rack, Supermicro, and other vendors exposing the standard IPMI fan interface.

## How it compares

|  | ventd | CoolerControl | fan2go | thinkfan | lm-sensors fancontrol |
| --- | --- | --- | --- | --- | --- |
| Zero-config first boot | yes | no | no | no | no |
| Browser-only setup (no terminal after install) | yes | no | no | no | no |
| Automatic calibration | yes | manual | manual | manual | manual |
| Single static binary | yes | no | yes | yes | script |
| Runs unprivileged (non-root) | yes | no | no | no | no |
| Runtime NVML `dlopen` (no nvidia build flag) | yes | no | no | no | no |
| Native USB HID for Corsair AIO (no liquidctl) | yes | via liquidctl | no | no | no |
| IPMI for server BMCs | yes | no | no | no | no |
| Hardware change detection | yes | no | no | no | no |
| Adaptive learning (smart mode) | v0.6.0 | no | no | no | no |
| Curated per-hardware profiles | yes (v0.5.0, fast-path overlay) | yes | no | partial | no |
| Native desktop GUI | no (web UI) | yes (Qt) | no | no | no |

CoolerControl is the more mature option if you want a pre-seeded profile for your specific AIO and a native desktop app today. ventd trades those for zero-config first boot, a browser-only workflow that works over the network, no runtime dependencies, an unprivileged daemon, and a roadmap pointed at learned adaptive control.

## Documentation

* [Roadmap](https://github.com/ventd/ventd/blob/main/docs/roadmap.md)
* [Installation guide](https://github.com/ventd/ventd/blob/main/docs/install.md)
* [Configuration reference](https://github.com/ventd/ventd/blob/main/docs/config.md)
* [Hardware compatibility](https://github.com/ventd/ventd/blob/main/docs/hardware.md)
* [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)
* [Safety model](https://github.com/ventd/ventd/blob/main/docs/safety.md)
* [Smart mode design](https://github.com/ventd/ventd/blob/main/specs/spec-smart-mode.md)
* [Troubleshooting](https://github.com/ventd/ventd/blob/main/docs/troubleshooting.md)
* [Changelog](https://github.com/ventd/ventd/blob/main/CHANGELOG.md)

## Building from source

```
git clone https://github.com/ventd/ventd
cd ventd
go build ./cmd/ventd/
```

Requires Go 1.25 or later. No other build dependencies.

## License

GPL-3.0. See [LICENSE](https://github.com/ventd/ventd/blob/main/LICENSE).

## Contributing

See [CONTRIBUTING.md](https://github.com/ventd/ventd/blob/main/CONTRIBUTING.md). Pull requests, issues, and hardware compatibility reports welcome.
