# ventd

[![CI](https://github.com/ventd/ventd/actions/workflows/ci.yml/badge.svg)](https://github.com/ventd/ventd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ventd/ventd?sort=semver)](https://github.com/ventd/ventd/releases)
[![Release date](https://img.shields.io/github/release-date/ventd/ventd)](https://github.com/ventd/ventd/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/ventd/ventd)](https://github.com/ventd/ventd/blob/main/go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/ventd/ventd/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-lightgrey)](#supported-platforms)

**Quieter PC, cooler PC, both — without touching a config file. ventd is a fan-control app for Linux: install with one command, open a browser tab, click Apply. It runs in the background and gets better at controlling your fans the longer it watches your machine.**

There's nothing to configure. No text files to edit, no chip names to look up, no Python or other extras to install. Run the installer, then open the address it prints; everything else happens in your web browser — hardware detection, fan testing, curves, settings. If your laptop or PC is one of the handful where Linux genuinely can't control the fans (see [What ventd cannot control](#what-ventd-cannot-control)), ventd tells you that on the dashboard instead of pretending to be in charge.

**What makes ventd different.** Every other Linux fan tool sets a curve once and follows it forever. ventd keeps learning. While you work, it quietly watches which fans cool which parts of your machine, which fans actually matter for each thing you do (gaming vs browsing vs compiling vs idle), and how loud each fan is. The result: fans speed up when your machine really needs them and stay quiet when it doesn't — automatically, without you adjusting anything. If you're curious about what it's doing, the **Smart** page in the web UI shows every decision in plain English. The deep technical version is in [Smart mode](#smart-mode).

## Why ventd

Three things ventd does that no other Linux fan tool does:

1. **It learns your machine.** Most fan tools make you set a fan curve once and use it forever. They never notice that your usage changed, that the room is warmer in summer, or that one of your fans is wearing out. ventd watches what your machine actually does — sitting idle, browsing, gaming, compiling, transcoding, anything — and adjusts. You pick one of three presets: **Silent**, **Balanced**, or **Performance**. Everything else is figured out from real measurements, not asked of you on a setup screen.
2. **It fixes itself when install goes wrong.** Linux fan control is famous for failing in a dozen ways: Secure Boot blocking a driver, missing kernel headers, the wrong driver already loaded, a leftover broken state from a previous attempt. When ventd hits one of these, it recognises which one and shows you a one-click "Fix this" button in the setup wizard. Other Linux fan tools (CoolerControl, fan2go, thinkfan, fancontrol) just print an error and leave you to search the wiki.
3. **It never asks you to write a config file.** ventd finds your fans, temperature sensors, and any liquid coolers automatically. Fan testing (called "calibration") happens in the browser; you can close the tab and come back later. Editing a curve is dragging dots around a graph. The whole app is one small file that needs nothing else installed.

[![ventd dashboard — live fan speeds, temperatures, and per-fan curves](https://github.com/ventd/ventd/raw/main/docs/images/dashboard.png)](https://github.com/ventd/ventd/blob/main/docs/images/dashboard.png)

*Dashboard: live fan PWM and RPM streamed from the daemon, per-fan curves editable in place.*

[![ventd first-boot setup — step-by-step wizard, no config yet](https://github.com/ventd/ventd/raw/main/docs/images/setup.png)](https://github.com/ventd/ventd/blob/main/docs/images/setup.png)

*First boot: a step-by-step setup wizard walks you through hardware detection, calibration, and curve review. No config file, no terminal.*

### More pages

<table>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/hardware.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/hardware.png" width="440" alt="Hardware" /></a><br /><sub>Hardware — chip → sensor tree, daemon ← chip ← sensor topology, case-shape heatmap</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/smart.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/smart.png" width="440" alt="Smart mode" /></a><br /><sub>Smart mode — continuous learning loop, per-channel confidence, recent decisions</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/curve-editor.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/curve-editor.png" width="440" alt="Curve editor" /></a><br /><sub>Curve editor — drag-to-edit with stall zone overlay</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/calibration.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/calibration.png" width="440" alt="Calibration" /></a><br /><sub>Calibration — per-fan sweep with live progress</sub></td>
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

## Is this for me?

If you have a Linux desktop, laptop, server, or NAS and any of these sound familiar, yes — this is for you:

* My fans are too loud.
* My fans are too quiet and the machine gets hot.
* I set up Linux fan control years ago, it half works, and I never want to touch it again.
* I want my PC to "just be quiet when it can and ramp up when it has to" without me thinking about it.

You do **not** need to understand anything about how fans, sensors, or kernel drivers work. The setup wizard handles all of it. If you do happen to know what hwmon, NVML, PWM curves, IPMI, or PI controllers are, the [Safety](#safety) and [Smart mode](#smart-mode) sections and the linked docs go as deep as you want.

If your hardware is in the [What ventd cannot control](#what-ventd-cannot-control) list, ventd can still show you temperatures and fan speeds — it just can't change them, because nothing on Linux can on those machines.

New to this? Start with the wiki [Getting started](https://github.com/ventd/ventd/wiki/Getting-started) page.

## Features

### What you'll see

* **Smart mode that keeps learning.** ventd doesn't stop after the first setup. It watches your machine while you work and gets better at controlling fans the longer it runs: which fans cool which parts, which fans actually help under your typical workload, and how loud each fan is. You pick **Silent**, **Balanced**, or **Performance** and that's all the tuning you ever have to do. The **Smart** page shows you exactly what it's currently doing and why — in plain English. No other Linux fan tool ships this.
* **One-click install with self-healing.** Linux fan control fails in a long list of weird ways (Secure Boot, missing kernel headers, conflicting drivers, broken state from previous attempts). When ventd hits one of these, it identifies the problem by name and offers a **"Fix this"** button right in the setup wizard. Reboots are flagged clearly when they're needed.
* **Hardware detection that just works.** ventd finds your motherboard's fans, your GPU fans, AIO liquid coolers, and any temperature sensors automatically — no chip names to look up, no Python helpers to keep alive. Plug in a new fan or GPU and ventd notices within a second.
* **Automatic fan testing.** The wizard measures each fan's lowest spin speed, top RPM, and full speed curve. You don't have to do anything; it runs in the background and you can close the browser tab — it picks up where it left off. The curve editor then highlights the unsafe zone in red so you can't accidentally set a fan below its stop point.
* **Real hardware support.** Motherboards from MSI, ASUS, Gigabyte, ASRock, Dell, HP, HPE, Lenovo, Supermicro, and more (540+ specific boards across 31 vendor catalogs and growing). NVIDIA, AMD, and Intel graphics cards. Corsair AIO pumps (Commander Core / Core XT / ST) speaking directly through USB without needing the `liquidctl` Python tool. IPMI for ASRock Rack, Supermicro and other server boards. Apple Silicon (read-only — Asahi project policy). Raspberry Pi PWM fans.
* **Plays nice with vendor tools.** If your laptop ships with a Linux-first vendor fan daemon (System76, Tuxedo, Slimbook, ASUS), ventd notices and steps aside, monitoring temperatures rather than fighting it.
* **One-button bug report.** If something goes wrong, `ventd diag` produces a privacy-redacted bundle you can send to maintainers. The redactor is fuzz-tested so serial numbers and identifying info don't escape.

### Under the hood (for the curious)

* **11-backend hardware abstraction layer.** hwmon, NVML (NVIDIA), amdgpu, msi-ec, thinkpad_acpi, ipmi, nbfc, crosec (Chromebook), asahi (Apple Silicon), pwmsys (ARM SBC), legion (Lenovo Legion), corsair (USB HID), plus the lenovoideapad platform-profile state-switcher. One control loop, one config schema, eleven hardware classes.
* **Smart mode internals.** Per-fan response curves from passive observation + opportunistic active probing. Per-channel thermal coupling (which sensor predicts which fan load). Per-(channel × workload-signature) marginal-benefit RLS estimator. Confidence-gated reactive/predictive blend with a Lipschitz-clamped transition. Per-host R30 microphone calibration + R32 dBA cost gate when a mic is available. See [Smart mode](#smart-mode).
* **Hardware database matcher.** Three-tier: exact board match → BIOS-version glob → chip-family fallback. The catalog is a hint, not an oracle — ventd tries each candidate driver and trusts the kernel's chip-ID rejection as the authoritative signal, so a stale catalog entry costs ~30 s of compile time, not 12 hours of debugging.
* **Terminal-first preflight (`ventd preflight`).** Before the service is installed, the install script runs an interactive preflight that detects 20+ install-time blockers (Secure Boot, missing kernel headers, conflicting in-tree drivers, GRUB cmdline gaps) and walks you through Y/N-gated auto-fixes. The web UI never sees install errors because they're caught in the terminal first.
* **Calibration safety: runtime probe + apply-path enforcement.** Calibration produces a real per-PWM probe result. The apply path refuses to write to channels that haven't been runtime-probed or are flagged unsupported. Within-chip parallel sweeps drop 8-fan NCT6687 boards from ~5 min to ~1 min.
* **Hardware change detection.** `AF_NETLINK` uevents trigger a rescan within ~1 s; capped 10-second fallback when uevents are unavailable.
* **Dashboard narrator strip.** A one-line strip on the dashboard rotates the most recent real decision the controller made — "ramped pump_fan from 35% → 42% — cpu_pkg trending up" — drawn from observed PWM transitions, not fake AI thoughts.
* **Single static binary.** `CGO_ENABLED=0`. NVML loaded at runtime via `dlopen`; GPU features disable silently if the library is absent. No Python, Node, or runtime dependencies beyond libc.

## Safety

ventd is in charge of your fans. That's important — your CPU or GPU can overheat in seconds if fans stop. Here's what ventd promises:

* **If ventd crashes or gets killed, your fans go back to firmware (BIOS) control within two seconds.** Whether the daemon exits cleanly or is force-killed, a second tiny safety program runs automatically and hands fan control back to your motherboard/BIOS. You never end up with stopped fans because ventd died.
* **During fan testing, fans can never be stopped for longer than two seconds.** If the test code hangs while a fan is at zero, a watchdog forces the fan back to a quiet-but-safe minimum.
* **ventd reports honestly when it can't control your hardware.** No silent failures. If a fan can't be acquired, the dashboard and the `ventd doctor` command show you the reason in plain words.

If ventd ever leaves a fan in an unsafe state, please report it as a [security issue](https://github.com/ventd/ventd/blob/main/SECURITY.md), not a regular bug.

### Under the hood (for the curious)

**Daemon privilege is `User=root` today.** The original design ran ventd unprivileged with udev DAC grants for hwmon PWM access, but the out-of-tree driver install path (DKMS register, depmod, modprobe, `/lib/modules` write, MOK key signing) needs root and the unprivileged-with-sudo approach proved fragile across distros. A future split-daemon refactor will separate control + install responsibilities so the long-running control loop can run unprivileged again while the install path gets a one-shot privileged helper. Until then, ventd ships as `User=root` honestly. The AppArmor profile is shipped to `/etc/apparmor.d/` but is not auto-loaded; it stages in for the split (RULE-INSTALL-06).

**Two layers behind the "within two seconds" promise:**

* **Graceful exits** (`SIGTERM`, `SIGINT`, panic inside a recovered frame) trigger the user-space watchdog in `internal/watchdog`, which restores each fan's pre-ventd `pwm_enable` value. Per-entry panic recovery: one fan's restoration failing never aborts the loop for the rest. Fallback when the original value was unrecordable: write PWM=255 (hwmon) or release to driver auto (NVIDIA).
* **Ungraceful exits** (`SIGKILL`, OOM kill, hardware-watchdog timeout, panic escaping the defer chain) are caught by a separate root-privileged binary, `ventd-recover`, fired via `OnFailure=ventd-recover.service` on the main unit. It walks every `/sys/class/hwmon/hwmon*/pwm<N>_enable` file and writes `1`. Zero heap allocations on the hot path; always exits 0 to avoid systemd re-entering the OnFailure chain. The main daemon's `WatchdogSec=2s` ensures a hung main loop gets SIGKILLed and the recovery path fires.

**Calibration sentinel.** Sweeps that drive PWM to 0 are watched by a per-fan sentinel (`internal/calibrate/safety.go`) that escalates to a quiet floor (`SafePWMFloor = 30`, roughly 12 % duty — above the start-PWM of nearly every fan on the market) if the zero state persists for more than two seconds.

The full model, failure-class breakdown, and the things we explicitly do **not** guarantee (kernel panic, power loss — userspace code never runs in those cases) are in [docs/safety.md](https://github.com/ventd/ventd/blob/main/docs/safety.md).

## Smart mode

### Plain English

A normal Linux fan tool waits for the CPU to heat up, then spins fans up to react. By the time the fans catch up, the chip has already overheated and throttled.

ventd doesn't wait. While you use your machine, it quietly learns:

* **How each fan behaves.** What's the slowest it will spin? How loud is it at each speed? Some of this is measured in the setup wizard; the rest is filled in over the following days, in the background, when ventd notices the machine is idle.
* **Which sensors predict which fans.** If your CPU gets warm, it's the CPU fan that needs to ramp — not the case fans. ventd figures out which sensor → fan relationships are real on your machine.
* **Which fans actually matter for what you're doing.** Gaming is different from compiling, which is different from streaming video. ventd learns the difference and only spins up the fans that actually help cool you for the workload you're running. Quieter overall, same temperatures.

You pick one of three presets — **Silent**, **Balanced**, or **Performance** — and that's the entire setting surface. Everything else is figured out from real measurements. The **Smart** page in the web UI shows you what ventd is doing right now in plain sentences: *"Currently quiet — system under light load, waiting for activity."* / *"Ramped pump_fan from 35 % to 42 % — CPU package trending up."*

If you have a microphone on the machine, ventd can be calibrated to control fans by actual decibels at the mic position instead of just by fan duty-cycle. Without a mic, it still controls loudness intelligently — just relative to itself rather than calibrated to dBA.

### Under the hood (for the curious)

Every other Linux fan tool runs the same reactive loop — temperature rises → fans spin up — meaning by the time the fans are at speed, the silicon has already spiked. ventd breaks that ceiling with continuous observation, learned per-fan response models, thermal-coupling maps between sensors and fans, and a confidence-gated controller that blends reactive (PI) and learned-predictive output as confidence accumulates. The catalog is a fast-path overlay rather than a prerequisite; ventd probes and controls hardware without a matching board profile.

Three layers of continuous learning, each usable on its own and all live in the controller today:

* **Layer A — per-fan response curve.** Passive observation plus opportunistic active probing. After a few days of normal use, ventd knows each fan's PWM→RPM relationship and stall zone. No user interaction required.
* **Layer B — per-channel thermal coupling.** Watches which temperature sensors predict which fan loads. Enables feed-forward: ramp before the heat arrives.
* **Layer C — marginal-benefit and saturation detection (RLS).** Learns which fan speed changes actually move temperature — distinguishing fans that matter from fans that are acoustically costly but thermally irrelevant, per active workload signature.
* **Confidence-gated blended controller.** Aggregates Layer A/B/C confidence per channel into a single `w_pred` blend weight; fans run reactive when confidence is low, predictive when it isn't, with a Lipschitz-clamped transition.

On top of those layers the controller also runs an **acoustic budget** (R30 K_cal mic capture + R32 dBA cost gate + per-fan loudness model with NVIDIA-shroud and pump-class entries) and a **chassis cooling-capacity model** that warns when a host's measured cooling can't cover its CPU TDP under sustained load. Live RPM is read from NVIDIA fans through NVML's `nvmlDeviceGetFanSpeedRPM` so multi-GPU workstations contribute to host loudness proportionally rather than under-reporting.

Three user-facing presets ship with the controller: **Silent**, **Balanced**, and **Performance**. No thermal targets to configure; ventd infers the right curve for your workload from observed data and the operator-supplied dBA budget per preset (Silent=25 dBA · Balanced=32 dBA · Performance=45 dBA, all overridable). On hosts with a calibrated microphone the budget operates in true dBA at the mic position; on uncalibrated hosts it operates in within-host au with the same blend weights and Lipschitz clamps.

**Hardware coverage continues in parallel:** NZXT and Lian Li USB AIOs, broader laptop embedded controllers (Framework, more ThinkPad / Dell SKUs), ARM SBC PWM (Raspberry Pi), Apple Silicon via Asahi. **Cross-platform** (Windows, macOS, FreeBSD) is the next major arc; see the [Roadmap](https://github.com/ventd/ventd/blob/main/docs/roadmap.md).

Detailed wiring + rationale in [docs/rules-rationale/smart-mode-wiring.md](https://github.com/ventd/ventd/blob/main/docs/rules-rationale/smart-mode-wiring.md); the binding rule files are under [docs/rules/](https://github.com/ventd/ventd/tree/main/docs/rules) (`coupling.md`, `marginal.md`, `opportunistic.md`, `smart-preset.md`, `smart-mode-wiring-1035.md`).

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

Open the printed URL. ventd serves a self-signed TLS certificate on first boot — your browser will warn; accept it (or front the daemon with nginx/Caddy for a Let's Encrypt cert). The first visit shows a "Create your password" page; that account becomes the local admin for the web UI. There is no setup token to recover from a file; ventd uses a first-login-creates-account flow.

## Supported platforms

* **Distributions:** Ubuntu, Debian, Fedora, RHEL, CentOS, Arch, Manjaro, openSUSE, Alpine, Void
* **Init systems:** systemd, OpenRC, runit
* **Architectures:** amd64, arm64
* **C library:** glibc and musl
* **GPU:** NVIDIA (via NVML — temperature reading works out of the box; GPU fan *writes* require the `--enable-gpu-write` daemon flag, see [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)); AMD (via amdgpu hwmon). Intel Arc is read-only at the kernel level; monitoring only.
* **Liquid coolers:** Corsair Commander Core / Core XT / ST (native USB HID, no liquidctl required).
* **Server BMCs:** IPMI fan control on ASRock Rack, Supermicro, and other vendors exposing the standard IPMI fan interface.

NixOS is not in the supported list — ventd's auto-fix endpoints write to `/etc/modprobe.d/` and `/etc/modules-load.d/` paths that NixOS silently ignores in favour of `configuration.nix`. Manual integration is possible; first-class NixOS support is on the roadmap.

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
* OEM mini-PCs without in-tree EC drivers (Beelink, GMKtec, AceMagic — model-specific). ventd ships a chip-probe fallback that catches IT5570 / IT8613 EC boards whose BIOS leaves DMI as `"Default string"`, so many previously-unrecognised mini-PCs now bind correctly.

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
| Adaptive learning (smart mode) | **yes** | no | no | no | no |
| Per-workload-signature marginal-benefit RLS | **yes** | no | no | no | no |
| Confidence-gated reactive/predictive blend | **yes** | no | no | no | no |
| Operator dBA budget (acoustic cost gate) | **yes** | no | no | no | no |
| Visible "what the daemon is doing" page | **yes** | no | no | no | no |
| Curated per-hardware profiles | yes (540+ boards, growing) | yes | no | partial | no |
| Native desktop GUI | no (web UI) | yes (Qt) | no | no | no |

CoolerControl is the more mature option if you want a pre-seeded profile for your specific AIO and a native desktop app today. ventd trades those for auto-config first boot, a browser-only workflow that works over the network, structured recovery with one-click auto-fixes for the long tail of hostile-hardware quirks, no runtime dependencies, and the only Linux fan controller that actually keeps learning your machine after the first calibration finishes.

## What we commit to

If your hardware is in [What ventd cannot control](#what-ventd-cannot-control), we commit to surfacing that honestly in monitor-only mode rather than letting you chase a vendor-locked dead end. If your hardware *should* work but ventd fails to detect or control it, we commit to:

* A diagnostic-bundle path that captures the missing data in one click (`ventd diag bundle`).
* A classifier + auto-fix card for any failure mode that hits more than one user.
* A growing catalog populated from those bundles — every entry under `internal/hwdb/catalog/` traces back to a real machine that hit a real wall.
* No silent failures: if the daemon can't acquire a fan it logs a structured reason, surfaces it in `ventd doctor`, and the wizard never claims success it didn't earn.

## Documentation

* [Roadmap](https://github.com/ventd/ventd/blob/main/docs/roadmap.md)
* [Installation guide](https://github.com/ventd/ventd/blob/main/docs/install.md)
* [Configuration reference](https://github.com/ventd/ventd/blob/main/docs/config.md)
* [Hardware compatibility](https://github.com/ventd/ventd/blob/main/docs/hardware.md)
* [NVIDIA GPU fan control](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)
* [Safety model](https://github.com/ventd/ventd/blob/main/docs/safety.md)
* [Smart mode design](https://github.com/ventd/ventd/blob/main/docs/rules-rationale/smart-mode-wiring.md)
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
