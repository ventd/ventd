# ventd

[![CI](https://github.com/ventd/ventd/actions/workflows/ci.yml/badge.svg)](https://github.com/ventd/ventd/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ventd/ventd?sort=semver)](https://github.com/ventd/ventd/releases)
[![Release date](https://img.shields.io/github/release-date/ventd/ventd)](https://github.com/ventd/ventd/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/ventd/ventd)](https://github.com/ventd/ventd/blob/main/go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](https://github.com/ventd/ventd/blob/main/LICENSE)
[![Platforms](https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-lightgrey)](#supported-platforms)

**A Linux fan-control daemon with a browser UI, auto-detection across hwmon / NVML / IPMI / USB AIOs / laptop ECs, and an experimental learned controller. Single static binary, any distro, any init system. amd64 and arm64.**

Install with one command, open the address it prints, click Apply. There's no config file to write and no chip names to look up. If your laptop or PC is one where Linux can't control the fans at all (see [What ventd cannot control](#what-ventd-cannot-control)), ventd says so on the dashboard instead of pretending otherwise.

## Status

ventd is a solo-dev project, first major release was three days ago, and the smart-mode learned controller is still maturing in production. The hwmon / NVML / IPMI / Corsair / laptop-EC plumbing is the most heavily tested surface and is what most users will rely on day to day. The learned controller (the "Smart" page) works on single-channel and small-channel hosts, has known convergence gaps on multi-channel boards like 8-channel NCT6687 (see [issue #1253](https://github.com/ventd/ventd/issues/1253)), and ships in observe-and-adjust mode by default rather than as the sole control path. If you want a fully mature Linux fan controller for mainstream desktop hardware today, [CoolerControl](https://gitlab.com/coolercontrol/coolercontrol) is the established choice and pairs well with `liquidctl`. ventd is interesting if your hardware is in the long tail that other tools have struggled with, or if you want a browser UI that works over the network, or if you want to watch a learned controller try to do better than a fixed curve.

[![ventd dashboard, live fan speeds and temperatures and per-fan curves](https://github.com/ventd/ventd/raw/main/docs/images/dashboard.png)](https://github.com/ventd/ventd/blob/main/docs/images/dashboard.png)

*Dashboard: live fan PWM and RPM streamed from the daemon, per-fan curves editable in place.*

[![ventd first-boot setup wizard](https://github.com/ventd/ventd/raw/main/docs/images/setup.png)](https://github.com/ventd/ventd/blob/main/docs/images/setup.png)

*First boot: a step-by-step wizard walks you through hardware detection, calibration, and curve review. No config file, no terminal.*

### More pages

<table>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/hardware.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/hardware.png" width="440" alt="Hardware" /></a><br /><sub>Hardware: chip and sensor tree, daemon to chip to sensor topology, case-shape heatmap</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/smart.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/smart.png" width="440" alt="Smart mode" /></a><br /><sub>Smart mode: continuous learning loop, per-channel confidence, recent decisions</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/curve-editor.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/curve-editor.png" width="440" alt="Curve editor" /></a><br /><sub>Curve editor: drag to edit, stall zone overlay</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/calibration.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/calibration.png" width="440" alt="Calibration" /></a><br /><sub>Calibration: per-fan sweep with live progress</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/schedule.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/schedule.png" width="440" alt="Schedule" /></a><br /><sub>Schedule: time-based profile switching with weekly visualisation</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/settings.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/settings.png" width="440" alt="Settings" /></a><br /><sub>Settings: display, daemon, security, system, about</sub></td>
</tr>
<tr>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/login.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/login.png" width="440" alt="Sign in" /></a><br /><sub>Sign in: post-first-boot login</sub></td>
  <td align="center"><a href="https://github.com/ventd/ventd/blob/main/docs/images/setup.png"><img src="https://github.com/ventd/ventd/raw/main/docs/images/setup.png" width="440" alt="Setup wizard" /></a><br /><sub>Setup wizard: first-boot hardware probe and calibration</sub></td>
</tr>
</table>

## Is this for me?

You probably want ventd if any of these sound like your situation:

* You have a laptop where Linux fan control has historically been hostile (some Dell, HP, IdeaPad, Latitude, NCT6687-board desktops, mini-PCs whose BIOS leaves DMI as `"Default string"`).
* You have an IPMI server or homelab box and you want fan control without ipmitool wrappers and without a desktop GUI.
* You want a web UI you can hit over the network instead of a desktop application bound to a single machine.
* You want hardware detection that doesn't ask you to look up chip names or write a YAML curve by hand.
* You're curious about a learned fan controller and want to watch one try to adapt to your workload.

You probably want a different tool if:

* You have a mainstream desktop with a supported AIO and you just want it working reliably right now, with a pre-curated profile and a polished desktop UI. [CoolerControl](https://gitlab.com/coolercontrol/coolercontrol) is well-suited to this and has years of community behind it.
* You're on a ThinkPad and `thinkfan` already does what you need. ventd will work on a ThinkPad too, but if you have a `thinkfan` setup you're happy with there's no urgent reason to switch.
* You're on NixOS. ventd's install paths write to `/etc/modprobe.d/` and `/etc/modules-load.d/` which NixOS routes through `configuration.nix`. Manual integration is possible; first-class NixOS support is on the roadmap, not shipped.

If your hardware is in the [What ventd cannot control](#what-ventd-cannot-control) list, ventd will show you temperatures and fan speeds, but it can't change them because nothing on Linux can on those machines.

## What ventd does

**Auto-detection across eleven hardware backends.** hwmon, NVML (NVIDIA), amdgpu, msi-ec, thinkpad_acpi, ipmi, nbfc, crosec (Chromebook), asahi (Apple Silicon, monitor-only by Asahi project policy), pwmsys (ARM SBC), legion (Lenovo Legion), corsair (USB HID), plus a `lenovoideapad` platform-profile backend. One control loop and one config schema across all of them. Plug in a new fan or GPU and the daemon notices via uevent within about a second.

**Hardware database for fast-path matching.** Around 540 board entries across 31 vendor catalogs (boards, chips, drivers under `internal/hwdb/catalog/`). The catalog is a hint, not a prerequisite. ventd probes and controls hardware without a matching board profile by trusting the kernel's chip-ID rejection as the authoritative signal, so a stale catalog entry costs about 30 seconds of compile time, not a stuck setup.

**Browser-based setup wizard.** After install, the printed URL opens a wizard that walks through hardware detection, fan calibration, and curve review. Calibration measures each fan's lowest spin speed, top RPM, and full PWM-to-RPM curve in the background. You can close the browser tab and come back later; calibration picks up where it left off. Within-chip parallel sweeps drop 8-fan NCT6687 boards from about 5 minutes to about 1 minute.

**Auto-fix cards for common install blockers.** Linux fan control fails in a long tail of ways: Secure Boot blocking an out-of-tree driver, missing kernel headers, the wrong driver already loaded, leftover broken state from a previous attempt. When the installer or the first-boot wizard hits one of these, it identifies the problem by name and offers a "Fix this" button. Reboots are flagged clearly when they're needed. This covers the common cases; the long tail still hits issues we haven't classified yet, in which case `ventd diag` bundles redacted system state into a tarball you can attach to a GitHub issue.

**Smart mode (experimental).** A learned controller that watches the machine while it runs and tries to adjust fan behaviour based on what's actually happening: per-fan PWM-to-RPM response, which sensors predict which fans, which fans actually move temperature for the current workload, and how loud each fan is. The Smart page shows the controller's current decision in plain English ("Currently quiet, system under light load, waiting for activity" or "Ramped pump_fan from 35% to 42%, CPU package trending up"). Three operator-facing presets ship: Silent, Balanced, Performance. The plumbing is live in v1.1.0 and works well on single-channel and small-channel hosts; convergence on 8+ channel boards is a known open issue and an active focus.

**Honest monitor-only fallback.** If a fan can't be controlled (vendor-locked EC, firmware lockout, hypervisor passthrough), the dashboard and `ventd doctor` both say so, in plain words, instead of silently dropping writes. The hardware list in [What ventd cannot control](#what-ventd-cannot-control) is exhaustive based on what has been confirmed; please file a hardware report if you hit a machine that should be controllable and isn't.

**Single static binary.** `CGO_ENABLED=0`. NVML is loaded at runtime via `dlopen`; GPU features disable silently if the library is absent. No Python, no Node, no runtime dependencies beyond libc.

**Privacy-redacted diagnostics.** `ventd diag bundle` produces a tarball with serial numbers and identifying info stripped. The redactor is fuzz-tested to catch additions to its block-list.

## Safety

ventd is in charge of your fans, and a stopped fan can overheat a chip in seconds. The safety model:

* If ventd crashes or is killed, fans return to firmware (BIOS) control within two seconds. A separate root-privileged binary, `ventd-recover`, is wired via `OnFailure=ventd-recover.service` and walks every `/sys/class/hwmon/hwmon*/pwm<N>_enable` file to write `1`. The main daemon's `WatchdogSec=2s` ensures a hung main loop gets SIGKILLed and the recovery path fires.
* During fan testing, fans cannot be stopped for longer than two seconds. A per-fan sentinel escalates to a quiet floor (`SafePWMFloor = 30`, roughly 12% duty) if the zero state persists past that window.
* If a fan can't be acquired, the dashboard, `ventd doctor`, and `ventd diag` all say so in plain words. No silent failures.

The daemon runs as `User=root` today. The reason is that the out-of-tree driver install path (DKMS register, depmod, modprobe, `/lib/modules` write, MOK key signing) needs root, and the unprivileged-with-sudo approach proved fragile across distros. A future split-daemon refactor will separate control and install responsibilities so the long-running control loop can run unprivileged again. The AppArmor profile is shipped to `/etc/apparmor.d/` but is not auto-loaded; it stages in for that split.

Full failure-class breakdown and the things ventd explicitly does **not** guarantee (kernel panic, power loss; userspace doesn't run in those cases) are in [docs/safety.md](https://github.com/ventd/ventd/blob/main/docs/safety.md). If ventd ever leaves a fan in an unsafe state, please report it as a [security issue](https://github.com/ventd/ventd/blob/main/SECURITY.md), not a regular bug.

## Smart mode

A normal Linux fan tool waits for the CPU to heat up, then spins fans up. By the time the fans catch up, the chip has already spiked.

Smart mode tries to do better. While you use the machine, it learns three things:

* **How each fan behaves.** Slowest spin, loudness at each speed, PWM-to-RPM response. Some of this is measured in the setup wizard. The rest fills in over a few days when ventd notices the machine is idle.
* **Which sensors predict which fans.** If the CPU gets warm, it's the CPU fan that needs to ramp, not the case fans. ventd tries to figure out which sensor-to-fan relationships are real on your specific machine.
* **Which fans actually matter for the current workload.** Gaming, compiling, transcoding, and idle all stress different parts of the machine. ventd tries to spin up only the fans that help cool the workload you're actually running.

You pick one of three presets (Silent, Balanced, Performance) and the controller does the rest. The Smart page in the web UI shows what it's doing right now in plain sentences and links to the recent decision log.

If the machine has a microphone, ventd can be calibrated to control fans by actual decibels at the mic position. Without a mic, it controls loudness relative to itself rather than calibrated to dBA.

**What still doesn't work reliably.** Convergence on 8+ channel boards (NCT6687D is the worked example, [issue #1253](https://github.com/ventd/ventd/issues/1253)) is an open problem. v1.1.0 closed the opportunistic-probing fires-in-the-real-world bug across homelab workloads, so probes actually run now on long-running boxes (Plex / Jellyfin / 24/7 services), but the controller's confidence accumulation on wide-channel hosts is still being tuned.

The full architecture (per-fan response curves from passive observation plus opportunistic probing, per-channel thermal coupling, per-workload-signature marginal-benefit RLS, confidence-gated reactive/predictive blend, acoustic budget gate) is described in [docs/rules-rationale/smart-mode-wiring.md](https://github.com/ventd/ventd/blob/main/docs/rules-rationale/smart-mode-wiring.md); the binding rule files are under [docs/rules/](https://github.com/ventd/ventd/tree/main/docs/rules).

## Install

Distro packages (Copr / AUR / PPA / OBS / Flathub) are tracked as [issue #1307](https://github.com/ventd/ventd/issues/1307) and not yet shipped. Until then, the install path is a shell script.

The script is small and plaintext. Read it before you run it:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh -o install.sh
less install.sh
sudo bash install.sh
```

If you've already read it once and trust it, or you're in a trusted-provisioning environment (container image bake, Ansible role, CI), the one-line form works:

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

Either way, the script detects your architecture and init system (systemd, OpenRC, or runit), runs an interactive preflight that gates Y/N on install-time blockers (Secure Boot, missing kernel headers, conflicting in-tree drivers, GRUB cmdline gaps), downloads the binary, verifies its SHA-256 against the published `checksums.txt` for the release, drops it at `/usr/local/bin/ventd`, installs the service file, enables it, and starts the daemon. It prints one thing: the URL to open in your browser.

ventd serves a self-signed TLS certificate on first boot, so your browser will warn; accept it (or front the daemon with nginx/Caddy for a Let's Encrypt cert). The first visit shows a "Create your password" page; that account becomes the local admin for the web UI. There is no setup token to recover from a file; ventd uses a first-login-creates-account flow.

## Supported platforms

* **Distributions tested in CI:** Ubuntu, Debian, Fedora, RHEL, CentOS, Arch, Manjaro, openSUSE, Alpine, Void.
* **Init systems:** systemd, OpenRC, runit.
* **Architectures:** amd64, arm64.
* **C library:** glibc and musl.
* **GPU:** NVIDIA via NVML (temperature reading works out of the box; GPU fan *writes* require the `--enable-gpu-write` daemon flag, see [docs/nvidia-fan-control.md](https://github.com/ventd/ventd/blob/main/docs/nvidia-fan-control.md)); AMD via amdgpu hwmon. Intel Arc is read-only at the kernel level.
* **Liquid coolers:** Corsair Commander Core / Core XT / ST via native USB HID. NZXT, Lian Li, EK Loop Connect, Aqua Computer Quadro / Octo, and Gigabyte AORUS RGB Fusion are on the roadmap, not shipped.
* **Server BMCs:** IPMI fan control on ASRock Rack, Supermicro, and other vendors exposing the standard IPMI fan interface.

NixOS is not in the supported list. ventd's auto-fix endpoints write to `/etc/modprobe.d/` and `/etc/modules-load.d/`, paths that NixOS routes through `configuration.nix`. Manual integration is possible; first-class NixOS support is on the roadmap.

## What ventd cannot control

The hardware below cannot be controlled by ventd or any Linux fan tool. The firmware, embedded controller, or hypervisor blocks all software access. ventd detects these and surfaces monitor-only mode with a plain-language explanation:

* HP EliteBook G10+, ZBook G9 / G10 (SMM-locked)
* Post-2020 Dell XPS 9320, 9500, 9710 (EC-locked, manual control vendor-revoked)
* Surface Pro 9, Surface Laptop Studio (Surface Aggregator EC)
* Microsoft Surface keyboard-cover devices (by design)
* Apple Silicon Macs M1 / M2 / M3 / M4 (Asahi project policy is read-only)
* Intel NUC (per Intel: "no software-controllable fans")
* Acer Predator / Nitro post-2021 BIOS (EC-locked)
* HPE iLO Standard tier (Gen8 / Gen9 / Gen10 without Advanced licence)
* iDRAC firmware ≥ 3.34 (manual control vendor-revoked)
* NVIDIA datacenter GPUs H100 / H200 / A100 (firmware-locked)
* AMD Instinct MI200 / MI300X (firmware-locked)
* OEM mini-PCs without in-tree EC drivers (Beelink, GMKtec, AceMagic; model-specific). ventd ships a chip-probe fallback that catches IT5570 / IT8613 EC boards whose BIOS leaves DMI as `"Default string"`, so many previously-unrecognised mini-PCs now bind correctly.

Per-board breakdown in [docs/hardware.md](https://github.com/ventd/ventd/blob/main/docs/hardware.md). If you have one of these and ventd surfaces an unhelpful error instead of a clean monitor-only fallback, please file a hardware report.

## What ventd commits to

If your hardware is in the list above, ventd commits to surfacing that honestly in monitor-only mode rather than letting you chase a vendor-locked dead end. If your hardware *should* work but ventd fails to detect or control it, ventd commits to:

* A diagnostic bundle path that captures the missing data in one click (`ventd diag bundle`).
* An auto-fix card for any failure mode confirmed on more than one machine.
* A growing catalog populated from those bundles. Every entry under `internal/hwdb/catalog/` traces back to a real machine that hit a real wall.
* No silent failures. If the daemon can't acquire a fan it logs a structured reason, surfaces it in `ventd doctor`, and the wizard doesn't claim success it didn't earn.

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

## Related projects

* [CoolerControl](https://gitlab.com/coolercontrol/coolercontrol) is a mature, native-Qt Linux fan controller with a large community and excellent AIO coverage via `liquidctl`. If you're on mainstream desktop hardware, it's the established choice.
* [liquidctl](https://github.com/liquidctl/liquidctl) is the upstream library for talking to liquid coolers; CoolerControl uses it, and many other Linux tools do too.
* [fan2go](https://github.com/markusressel/fan2go), [thinkfan](https://github.com/vmatare/thinkfan), and `lm-sensors fancontrol` are the established curve-based controllers on Linux.

ventd is not trying to replace any of these. The wedge ventd is exploring is auto-detection across the long tail of hardware that's historically been hostile, a browser-first UI for remote and headless boxes, and a learned controller that adapts to your workload. If the established tools are working for you, stay with them; if you've hit a wall on one of the harder cases, ventd may help.

## License

GPL-3.0. See [LICENSE](https://github.com/ventd/ventd/blob/main/LICENSE).

## Contributing

See [CONTRIBUTING.md](https://github.com/ventd/ventd/blob/main/CONTRIBUTING.md). Pull requests, issues, and hardware compatibility reports are welcome.
