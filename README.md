# ventd

[![CI](https://github.com/ventd/ventd/actions/workflows/ci.yml/badge.svg)](https://github.com/ventd/ventd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ventd/ventd?sort=semver)](https://github.com/ventd/ventd/releases)
[![Release date](https://img.shields.io/github/release-date/ventd/ventd)](https://github.com/ventd/ventd/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/ventd/ventd)](https://github.com/ventd/ventd/blob/main/go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/ventd/ventd/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-lightgrey)](#supported-platforms)

**Adaptive Linux fan control. Install, open the browser, click Apply — ventd handles the rest, then keeps learning your machine in the background.**

One static binary, one install command, one URL. Hardware detection, calibration, curve editing, and recovery all happen in the web UI. The terminal install command is the last terminal command you need to run on the happy path. When the kernel doesn't expose writable fan control for your hardware (some laptops, datacenter GPUs, recent Dell EC-locked chassis — see [What ventd cannot control](#what-ventd-cannot-control)), ventd falls back to monitor-only mode with a clear explanation rather than pretending.

What sets ventd apart from every other Linux fan tool: it doesn't stop after the first calibration. From v0.5.5 on, ventd runs a **continuous learning stack** — opportunistic active probing, per-channel thermal-coupling maps, per-(channel × workload-signature) marginal-benefit RLS, and a confidence-gated controller that blends reactive PI and learned-predictive output as confidence accumulates. The new `/smart` page makes that visible: which channels have converged, what workload signature is active, the most recent decisions the daemon made, and why.

> [!NOTE]
> **ventd is pre-1.0.** Safety guarantees are production-quality and verified by tests CI enforces. The config schema and curve format may evolve before v1.0. The smart-mode learning stack is shipping incrementally (v0.5.5 → v0.6.0); see [Smart mode status](#smart-mode-status) for the per-layer state.

## Why ventd

Three things ventd does that no other Linux fan tool does:

1. **It learns your machine.** Every other tool runs the same reactive loop forever — the curve you set on day one is the curve it follows on day three hundred. ventd runs a continuous learning stack (Layer A response curves, Layer B thermal coupling, Layer C marginal-benefit RLS, all confidence-gated and visible on the `/smart` page) so the controller actually gets smarter the longer it runs. Three operator presets — Silent, Balanced, Performance — drive the dBA budget and cost gate; everything else is inferred from observed data.
2. **It self-heals install-time failures.** Secure Boot blocking module load, in-tree driver conflict, ACPI region reservation, missing kernel headers, DKMS state collision — ventd classifies the failure and offers one-click auto-fixes (generate a MOK key, queue its enrollment, install kernel headers, write modprobe quirks, blacklist a conflicting in-tree module, re-run install with cleared state). fan2go, CoolerControl, fancontrol, and thinkfan all just emit error strings.
3. **It never asks you to write YAML.** Hardware enumeration through `hwmon`, `NVML`, and a native USB HID stack on first boot. Calibration runs server-side and survives browser disconnect. Curve editing in a browser tab. No config file, no `liquidctl` Python sidecar, no Super I/O chip lookup tables. Single static binary, no runtime dependencies beyond libc.

[![ventd dashboard — live fan speeds, temperatures, and per-fan curves](https://github.com/ventd/ventd/raw/main/docs/images/dashboard.png)](/ventd/ventd/blob/main/docs/images/dashboard.png)

*Dashboard: live fan PWM and RPM streamed from the daemon, per-fan curves editable in place.*

[![ventd first-boot setup — step-by-step wizard, no config yet](https://github.com/ventd/ventd/raw/main/docs/images/setup.png)](/ventd/ventd/blob/main/docs/images/setup.png)

*First boot: a step-by-step setup wizard walks you through hardware detection, calibration, and curve review. No config file, no terminal.*

### More pages

<table>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/hardware.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/hardware.png" width="440" alt="Hardware" /></a><br /><sub>Hardware — chip → sensor tree, daemon ← chip ← sensor topology, case-shape heatmap (v0.5.14)</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/smart.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/smart.png" width="440" alt="Smart mode" /></a><br /><sub>Smart mode — continuous learning loop, per-channel confidence, recent decisions (v0.5.14)</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/curve-editor.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/curve-editor.png" width="440" alt="Curve editor" /></a><br /><sub>Curve editor — drag-to-edit with stall zone overlay</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/calibration.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/calibration.png" width="440" alt="Calibration" /></a><br /><sub>Calibration — per-fan sweep with live progress (v2 layout v0.5.13)</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/schedule.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/schedule.png" width="440" alt="Schedule" /></a><br /><sub>Schedule — time-based profile switching with weekly visualisation</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/settings.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/settings.png" width="440" alt="Settings" /></a><br /><sub>Settings — display, daemon, security, system, about</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/login.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/login.png" width="440" alt="Sign in" /></a><br /><sub>Sign in — post-first-boot login with the same shell as setup</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/setup.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/setup.png" width="440" alt="Setup wizard" /></a><br /><sub>Setup wizard — first-boot hardware probe and calibration entry</sub></td>
</tr>
</table>

## Features

* **Smart mode — continuous learning, shipping now (v0.5.5+).** ventd doesn't stop after the first calibration. It probes opportunistically when you're idle, builds a per-channel thermal-coupling map (which sensor predicts which fan), runs a per-(channel × workload-signature) marginal-benefit RLS estimator (which fans actually move temperature for the load you're running), and blends learned-predictive PWM with reactive PI under a confidence gate that's lock-free on the hot loop. Dedicated `/smart` page surfaces every signal: per-channel `w_pred`, signature label, recent decisions and why. Three operator presets (Silent / Balanced / Performance). No other Linux fan tool ships any of this.
* **Automatic hardware detection.** Enumerates every writable fan control the kernel exposes via `hwmon` (motherboard Super I/O chips — Nuvoton, ITE, AMD K10Temp, Intel coretemp, and the rest) plus NVIDIA GPUs through runtime-loaded NVML. Reads AMD GPU temperatures through the amdgpu hwmon layer. Intel Arc reads as monitor-only.
* **Native USB HID for AIO pumps.** Corsair Commander Core, Core XT, and Commander ST shipped in v0.4.0 — talking directly to the device through a pure-Go hidraw stack with no `liquidctl` Python sidecar. Read-only by default; writes opt-in behind `--enable-corsair-write`.
* **IPMI for server BMCs.** ASRock Rack, Supermicro, and other server boards exposing IPMI fan control. Shipped in v0.3.1.
* **Hardware database (130+ boards, 6 vendors, growing).** Curated catalog covering MSI, ASUS, Gigabyte, ASRock, Dell (consumer + PowerEdge), HP, HPE, Lenovo (IdeaPad/ThinkPad/Legion), Supermicro, and Raspberry Pi. Three-tier matcher: exact board match, then BIOS-version glob, then chip-family fallback. With v0.5.11's probe-then-pick refactor, the catalog is a hint, not an oracle — ventd tries each candidate driver and trusts the kernel's chip-ID rejection as the authoritative signal, so a stale catalog entry costs ~30 s of compile time, not 12 hours of debugging. GPU vendor coverage: NVIDIA (NVML), AMD (amdgpu), Intel (i915/xe). Catalog grows from user diagnostic bundles.
* **Self-healing recovery.** When something goes wrong (Secure Boot, DKMS state, in-tree driver conflict, ACPI region reservation), ventd's classifier identifies the failure class and offers one-click auto-fixes through the wizard. Reboots are surfaced explicitly when a fix only takes effect at next boot (MOK enrollment, blacklist drop-ins). No other Linux fan tool ships this.
* **Terminal-first preflight (`ventd preflight`).** Before the systemd unit is installed, the install script runs an interactive preflight that detects Secure Boot prerequisites, missing kernel headers, in-tree driver conflicts, and 20+ other install-time blockers. It walks you through Y/N-gated auto-fixes for each — no opening a wiki, no guessing modprobe options. The web UI never shows install-time errors because they're caught and fixed in the terminal first.
* **Coexistence with vendor tools.** ventd detects `system76-power`, `tccd` (Tuxedo Control Centre), `slimbookbattery`, and `asusctl` and steps aside — your vendor tool already controls fans correctly on Linux-first OEM laptops. ventd registers as monitor-only on those systems rather than fighting the vendor daemon for control.
* **Calibration safety: runtime probe + apply-path enforcement.** Calibration produces a real per-PWM probe result. The apply path refuses to write to channels that haven't been runtime-probed or are flagged unsupported in the catalog. Shipped in v0.5.0.
* **Diagnostic bundle.** `ventd diag` produces a redacted NDJSON bundle for support and bug reports. Built-in redactor with fuzz-tested anonymisation. Shipped in v0.5.0.
* **Automatic calibration.** Measures start PWM, stop PWM, max RPM, and the full PWM→RPM curve per fan. Runs server-side; survives browser disconnect and daemon restart. Abortable from the UI. The curve editor uses calibration data to draw the stall zone in red, so you can't accidentally set a curve below the fan's stop threshold.
* **Hardware change detection.** Plug a new fan or GPU in; ventd notices within a second via `AF_NETLINK` uevents (capped at a 10-second rescan when unavailable) and offers to add it.
* **Browser-first after install.** Hardware scan, dependency install, calibration, curve editing, and service control all happen in the web UI on the happy path. The terminal-first preflight catches install-time blockers up-front so the wizard is browser-only on success.
* **Hardware page that maps your machine.** New unified `/hardware` page (v0.5.14) collapses the old Devices + Sensors split into three views: chip-by-chip Inventory with sparklines and curve-coupling rail, daemon ← chip ← sensor Topology with live packet-flow, and a Heatmap with sensors at their case-relative positions. Picks up new hardware via uevent within a second.
* **Dashboard that narrates what the daemon is doing.** Hero sparks carry past · now · forecast bands (linear extrapolation from real history). Tile intent arrows + flash-on-decision. A one-line "narrator" strip rotates the most recent real decision the controller made — "ramped pump_fan from 35% → 42% — cpu_pkg trending up" — drawn from observed PWM transitions, not fake AI thoughts. Coupling map, decision feed, and AI brief in the insight rail.
* **Single static binary.** `CGO_ENABLED=0`. NVML loaded at runtime via `dlopen`; GPU features disable silently if the library is absent. No Python, Node, or runtime dependencies beyond libc.

## Safety

ventd controls physical hardware. Two things follow from that, and both are load-bearing design decisions rather than marketing copy.

**Daemon privilege is `User=root` today (v0.5.8.1+).** The original design ran ventd unprivileged with udev DAC grants for hwmon PWM access, but the OOT-driver install path (DKMS register, depmod, modprobe, /lib/modules write, MOK key signing) needs root and the unprivileged-with-sudo approach proved fragile across distros. The v0.6.0 split-daemon plan separates control + install responsibilities so the long-running control loop can run unprivileged again while the install path gets a one-shot privileged helper. Until then, ventd ships as `User=root` honestly. The shipped AppArmor profile remains in the package for the v0.6.0 split (RULE-INSTALL-06).

**Every exit path restores firmware control within two seconds.** Two layers, working together:

* **Graceful exits** (`SIGTERM`, `SIGINT`, panic inside a recovered frame) trigger the user-space watchdog in `internal/watchdog`, which restores each fan's pre-ventd `pwm_enable` value. Per-entry panic recovery: one fan's restoration failing never aborts the loop for the rest. Fallback when the original value was unrecordable: write PWM=255 (hwmon) or release to driver auto (NVIDIA).
* **Ungraceful exits** (`SIGKILL`, OOM kill, hardware-watchdog timeout, panic escaping the defer chain) are caught by a separate root-privileged binary, `ventd-recover`, fired via `OnFailure=ventd-recover.service` on the main unit. It walks every `/sys/class/hwmon/hwmon*/pwm<N>_enable` file and writes `1`. Zero heap allocations on the hot path; always exits 0 to avoid systemd re-entering the OnFailure chain. The main daemon's `WatchdogSec=2s` ensures a hung main loop gets SIGKILLed and the recovery path fires; this is the mechanism behind the "within two seconds" promise.

**Calibration cannot strand a fan at zero.** Sweeps that drive PWM to 0 are watched by a per-fan sentinel (`internal/calibrate/safety.go`) that escalates to a quiet floor (`SafePWMFloor = 30`, roughly 12% duty — above start-PWM of nearly every fan on the market) if the zero state persists for more than two seconds. A hung calibration goroutine cannot leave a fan stopped under load.

Full model, failure-class breakdown, and the things we explicitly do **not** guarantee (kernel panic, power loss — userspace code never runs in those cases) in [docs/safety.md](https://github.com/ventd/ventd/blob/main/docs/safety.md).

Report any case where ventd leaves a fan in an unsafe state as a [SECURITY.md](https://github.com/ventd/ventd/blob/main/SECURITY.md) issue, not a regular bug.

## Smart mode status

**ventd has been quietly learning your machine since v0.5.5.** Every other Linux fan tool runs the same reactive loop — temperature rises → fans spin up — meaning by the time the fans are at speed, the silicon has already spiked. ventd breaks that ceiling with continuous observation, learned per-fan response models, thermal-coupling maps between sensors and fans, and a confidence-gated controller that blends reactive (PI) and learned-predictive output as confidence accumulates. The catalog stopped being a prerequisite in v0.5.11 — it's now a fast-path overlay; ventd probes and controls hardware without a matching board profile.

Three layers of continuous learning, each usable on its own and all live today:

* **Layer A — per-fan response curve.** Passive observation plus opportunistic active probing. After a few days of normal use, ventd knows each fan's PWM→RPM relationship and stall zone. No user interaction required. (v0.5.4 + v0.5.5)
* **Layer B — per-channel thermal coupling.** Watches which temperature sensors predict which fan loads. Enables feed-forward: ramp before the heat arrives. (v0.5.7)
* **Layer C — marginal-benefit and saturation detection (RLS).** Learns which fan speed changes actually move temperature — distinguishing fans that matter from fans that are acoustically costly but thermally irrelevant, per active workload signature. (v0.5.8)
* **Confidence-gated blended controller.** Aggregates Layer A/B/C confidence per channel into a single `w_pred` blend weight; fans run reactive when confidence is low, predictive when it isn't, with a Lipschitz-clamped transition. (v0.5.9)

What's left before the v0.6.0 stabilization tag:

| Tag | Scope | Status |
| --- | --- | --- |
| v0.5.0.1 | Persistent state foundation | ✅ shipped |
| v0.5.1 | Catalog-less probe + three-state wizard | ✅ shipped |
| v0.5.2 | Polarity midpoint disambiguation | ✅ shipped |
| v0.5.3 | Envelope probe + user-idle gate + load monitor | ✅ shipped |
| v0.5.4 | Passive observation logging (Layer A foundation) | ✅ shipped |
| v0.5.5 | Opportunistic active probing for Layer A gaps | ✅ shipped |
| v0.5.6 | Workload signature learning and classification | ✅ shipped |
| v0.5.7 | Per-channel thermal-coupling map (Layer B) | ✅ shipped |
| v0.5.8 | Marginal-benefit and saturation detection (Layer C) | ✅ shipped |
| v0.5.9 | Confidence-gated blended controller + confidence UX | ✅ shipped |
| v0.5.10 | Doctor recovery surface + internals consolidation | ✅ shipped |
| v0.5.11 | Comprehensive preflight orchestrator + probe-then-pick | ✅ shipped |
| v0.5.12 | R30 acoustic capture + R32 dBA cost gate + R31 stall detector + R36 chip-probe fallback | ✅ shipped |
| v0.5.13 | Calibration v2 layout + SSE activity feed + BIOS Q-Fan EBUSY recovery | ✅ shipped |
| v0.5.14 | Hardware page + Smart-mode page + Dashboard alive overlay + nct6687 in-tree probe | ✅ shipped |
| **v0.6.0** | **Smart-mode stabilization complete + cross-platform start** | next |

Three user-facing presets ship with the controller: **Silent**, **Balanced**, and **Performance**. No thermal targets to configure; ventd infers the right curve for your workload from observed data and the operator-supplied dBA budget per preset (Silent=25 dBA · Balanced=32 dBA · Performance=45 dBA, all overridable).

**Hardware coverage continues in parallel:** NZXT and Lian Li USB AIOs, laptop embedded controllers (Framework, ThinkPad, Dell), ARM SBC PWM (Raspberry Pi), Apple Silicon via Asahi. **Cross-platform** (Windows, macOS, FreeBSD) is post-v0.6.0.

Phase 1 (HAL foundation, hardware database) shipped in v0.3.0. Phase 2 (multi-backend support — IPMI in v0.3.1, Corsair AIO in v0.4.0, 52-board catalog and GPU vendor coverage in v0.5.0) shipped in v0.5.0. Phase 3 (smart mode) is **mostly shipped** — every layer plus the confidence-gated controller plus the operator-facing UI all landed across v0.5.4–v0.5.14. v0.6.0 closes out the remaining stabilization work and starts cross-platform.

Detailed design in [specs/spec-smart-mode.md](https://github.com/ventd/ventd/blob/main/specs/spec-smart-mode.md).

## Install

The install script is small and plaintext — read it before you run it:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh -o install.sh
less install.sh           # read it — it's ~150 lines
sudo bash install.sh
```

If you already trust the script, or you're in a trusted-provisioning environment (container image bake, Ansible role, CI), the one-line form works:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

Either way, the script detects your architecture and init system (systemd, OpenRC, or runit), runs the [terminal-first preflight](#features) (Y/N gates for any install-time blockers), downloads the binary, **verifies its SHA-256 against the published `checksums.txt` for the release**, drops it at `/usr/local/bin/ventd`, installs the service file, enables it, and starts the daemon. It prints one thing: the URL to open in your browser.

Open the printed URL. ventd serves a self-signed TLS certificate on first boot — your browser will warn; accept it (or front the daemon with nginx/Caddy for a Let's Encrypt cert). The first visit shows a "Create your password" page; that account becomes the local admin for the web UI. There is no setup token to recover from a file; ventd v0.5.8.1+ uses a first-login-creates-account flow.

## Supported platforms

* **Distributions:** Ubuntu, Debian, Fedora, RHEL, CentOS, Arch, Manjaro, openSUSE, Alpine, Void
* **Init systems:** systemd, OpenRC, runit
* **Architectures:** amd64, arm64
* **C library:** glibc and musl
* **GPU:** NVIDIA (via NVML — temperature reading works out of the box; GPU fan *writes* require the `--enable-gpu-write` daemon flag, see [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)); AMD (via amdgpu hwmon). Intel Arc is read-only at the kernel level; monitoring only.
* **Liquid coolers:** Corsair Commander Core / Core XT / ST (native USB HID, no liquidctl required).
* **Server BMCs:** IPMI fan control on ASRock Rack, Supermicro, and other vendors exposing the standard IPMI fan interface.

NixOS is not in the supported list — ventd's auto-fix endpoints write to `/etc/modprobe.d/` and `/etc/modules-load.d/` paths that NixOS silently ignores in favour of `configuration.nix`. Manual integration is possible; first-class support is on the post-v0.6.0 roadmap.

## What ventd cannot control

The hardware below **cannot** be controlled by ventd or any Linux fan tool — the firmware, embedded controller, or hypervisor blocks all software access. ventd detects these and surfaces monitor-only mode with a clear explanation rather than pretending:

* HP EliteBook G10+, ZBook G9 / G10 — SMM-locked
* Post-2020 Dell XPS 9320, 9500, 9710 — EC-locked, manual control vendor-revoked
* Surface Pro 9, Surface Laptop Studio — Surface Aggregator EC
* Microsoft Surface keyboard-cover devices — by design
* Apple Silicon Macs (M1, M2, M3, M4) — Asahi project policy is read-only
* Intel NUC — per Intel: "no software-controllable fans"
* Acer Predator / Nitro post-2021 BIOS — EC-locked
* HPE iLO Standard tier (Gen8 / Gen9 / Gen10 without Advanced licence)
* iDRAC firmware ≥ 3.34 — manual control vendor-revoked
* NVIDIA datacenter GPUs (H100, H200, A100) — firmware-locked
* AMD Instinct MI200 / MI300X — firmware-locked
* OEM mini-PCs without in-tree EC drivers (Beelink, GMKtec, AceMagic — model-specific). v0.5.12 added a chip-probe fallback (RULE-HWDB-PR2-18) that catches IT5570 / IT8613 EC boards whose BIOS leaves DMI as `"Default string"`; many previously-unrecognised mini-PCs now bind correctly.

Per-board breakdown in [docs/hardware.md](https://github.com/ventd/ventd/blob/main/docs/hardware.md). If you have one of these and ventd surfaces an unhelpful error instead of a clean monitor-only fallback, that's a bug — please file a hardware report.

## How it compares

|  | ventd | CoolerControl | fan2go | thinkfan | lm-sensors fancontrol |
| --- | --- | --- | --- | --- | --- |
| Auto-config first boot | yes | no | no | no | no |
| Browser-only setup (after install) | yes | no | no | no | no |
| Automatic calibration | yes | manual | manual | manual | manual |
| Single static binary | yes | no | yes | yes | script |
| Self-healing recovery (classifier + auto-fix cards) | yes | no | no | no | no |
| Automatic OOT module install + DKMS + MOK enrolment | yes | no | no | no | no |
| Hardware/distro-aware quirk dispatch (modprobe options, GRUB cmdline) | yes | no | no | no | no |
| Terminal-first install preflight (Y/N gated auto-fixes) | yes | no | no | no | no |
| Runtime NVML `dlopen` (no nvidia build flag) | yes | no | no | no | no |
| Native USB HID for Corsair AIO (no liquidctl) | yes | via liquidctl | no | no | no |
| IPMI for server BMCs | yes | no | no | no | no |
| Hardware change detection | yes | no | no | no | no |
| Monitor-only fallback for vendor-locked hardware | yes | no | no | no | no |
| Adaptive learning (smart mode) | **yes (v0.5.5+)** | no | no | no | no |
| Per-workload-signature marginal-benefit RLS | **yes (v0.5.8+)** | no | no | no | no |
| Confidence-gated reactive/predictive blend | **yes (v0.5.9+)** | no | no | no | no |
| Operator dBA budget (acoustic cost gate) | **yes (v0.5.12+)** | no | no | no | no |
| Visible "what the AI is doing" page | **yes (v0.5.14+)** | no | no | no | no |
| Curated per-hardware profiles | yes (130+ boards, growing) | yes | no | partial | no |
| Native desktop GUI | no (web UI) | yes (Qt) | no | no | no |

CoolerControl is the more mature option if you want a pre-seeded profile for your specific AIO and a native desktop app today. ventd trades those for auto-config first boot, a browser-only workflow that works over the network, structured recovery with one-click auto-fixes for the long tail of hostile-hardware quirks, no runtime dependencies, and the only Linux fan controller that actually keeps learning your machine after the first calibration finishes.

## What we commit to

If your hardware is in [What ventd cannot control](#what-ventd-cannot-control), we commit to surfacing that honestly in monitor-only mode rather than letting you chase a vendor-locked dead end. If your hardware *should* work but ventd fails to detect or control it, we commit to:

* A diagnostic-bundle path that captures the missing data in one click (`ventd diag bundle`).
* A classifier + auto-fix card for any failure mode that hits more than one user.
* A growing catalog populated from those bundles — `git blame` on `internal/hwdb/profiles-v1.yaml` shows every entry traceable to a real machine that hit a real wall.
* No silent failures: if the daemon can't acquire a fan it logs a structured reason, surfaces it in `ventd doctor`, and the wizard never claims success it didn't earn.

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
