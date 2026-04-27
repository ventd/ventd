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
> **ventd is pre-1.0.** Safety guarantees are production-quality and verified by tests CI enforces. The config schema and curve format may evolve before v1.0. See [What's coming](#whats-coming) for the path to 1.0.

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
* **Hardware database (52 boards, 6 vendors).** Curated catalog covering MSI, ASUS, Gigabyte, ASRock, Dell (consumer + PowerEdge), HP, HPE, Lenovo (IdeaPad/ThinkPad/Legion), Supermicro, and Raspberry Pi. Three-tier matcher: exact board match, then BIOS-version glob, then chip-family fallback. GPU vendor coverage: NVIDIA (NVML), AMD (amdgpu), Intel (i915/xe). Shipped in v0.5.0; controller auto-load lands in v0.6.
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

**Every Linux fan controller in the world today is reactive.** Your CPU temp climbs, your fans spool up. By the time the fans are at speed the silicon has already passed through the thermal noise band you didn't want. Reactive control, with a curve, is the ceiling of what fan2go, CoolerControl, thinkfan, and lm-sensors fancontrol all do.

ventd is being built to break that ceiling. **The v1.0 thesis is predictive thermal control: ventd will model your machine's thermal behaviour, predict the next 30 seconds of temperature from current load, and pre-act on the fans so they're already at the right speed when the heat arrives.**  Quieter at idle (no overshoot, no oscillation), faster under transient load (fans ramped before the silicon needs them), and adapted to your specific machine instead of a one-size curve.

The v0.5 → v1.0 roadmap is the march to that capability. Each release ships a layer of the stack as a usable feature on its own:

* **v0.5 — Curated profile database (shipped 2026-04-26).** Fingerprint-keyed catalog seeded with 52 boards, GPU vendor coverage, and OOT/BMC driver descriptors. Calibration emits pending profile YAMLs after each run. Auto-loading curated profiles at controller startup is v0.6 territory.
* **v0.6 — PI controller with autotune.** Replaces the curve as the inner control loop. Smoother fan response, less hunting, gives you a closed-loop controller you can run today instead of a lookup table. Also adds catalog-driven profile auto-load at startup.
* **v0.7 — Feedforward + safety latch.** Anticipates load from CPU/GPU utilisation, not just temperature. The first piece of "predictive" — but reactive enough to fall back safely if the model is wrong.
* **v0.8 — Online thermal model identification (VFF-RLS + ARX).** ventd watches your machine for a few hours and learns its thermal time constants. The model that the v1.0 predictor will run on top of.
* **v0.9 — Acoustic signatures.** Detect bearing wear from fan sound; dither synchronised fans to break beat frequencies.
* **v1.0 — Predictive control with motif detection.** The full stack: predict → pre-act → verify. The world's first predictive Linux fan controller.

Detailed design in [specs/spec-05-predictive-thermal.md](https://github.com/ventd/ventd/blob/main/specs/spec-05-predictive-thermal.md). Research in [docs/research/2026-04-predictive-thermal.md](https://github.com/ventd/ventd/blob/main/docs/research/2026-04-predictive-thermal.md).

**Hardware coverage continues in parallel:** NZXT and Lian Li USB AIOs (v0.4.x), laptop embedded controllers (Framework, ThinkPad, Dell), ARM SBC PWM (Raspberry Pi), Apple Silicon via Asahi. **Cross-platform** (Windows, macOS, FreeBSD) is post-v1.0.

Phase 1 (HAL foundation, hot-loop optimisation, fingerprint-keyed hardware database) shipped in v0.3.0. Phase 2 (multi-backend hardware support — IPMI in v0.3.1, Corsair AIO in v0.4.0, hardware database with 52-board catalog and GPU vendor coverage in v0.5.0) is underway.

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
| Predictive thermal control | v1.0 target | no | no | no | no |
| Curated per-hardware profiles | yes (v0.5.0, catalog ships; auto-load v0.6) | yes | no | partial | no |
| Native desktop GUI | no (web UI) | yes (Qt) | no | no | no |

CoolerControl is the more mature option if you want a pre-seeded profile for your specific AIO and a native desktop app today. ventd trades those for zero-config first boot, a browser-only workflow that works over the network, no runtime dependencies, an unprivileged daemon, and a roadmap pointed at predictive control.

## Documentation

* [Roadmap](https://github.com/ventd/ventd/blob/main/docs/roadmap.md)
* [Installation guide](https://github.com/ventd/ventd/blob/main/docs/install.md)
* [Configuration reference](https://github.com/ventd/ventd/blob/main/docs/config.md)
* [Hardware compatibility](https://github.com/ventd/ventd/blob/main/docs/hardware.md)
* [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)
* [Safety model](https://github.com/ventd/ventd/blob/main/docs/safety.md)
* [Predictive thermal control (research)](https://github.com/ventd/ventd/blob/main/docs/research/2026-04-predictive-thermal.md)
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
