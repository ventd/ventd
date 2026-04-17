# Roadmap

> **ventd 1.0 arc.** Cross-platform first: the single fan-control daemon for Linux, Windows, macOS, BSD. Every fan type: hwmon, GPU, IPMI, USB AIOs, laptop EC, ARM SBC PWM. Learning control: a per-machine thermal model quietens fans by predicting load. Curated profiles: first boot is zero-touch on any hardware already seen. Acoustic health: detect bearing failures from sound alone. One static binary, zero capabilities, 2-second guaranteed safe exit.

Phases are ordered by dependency, not by calendar—ventd does not commit to dates for any 1.0 feature.

## Phase 1 — Core refactor

The foundation for all backends. A hardware abstraction layer ensures every fan control method—hwmon, IPMI, GPUs, USB controllers—plugs into a single control loop. Hot-loop optimisation removes per-tick allocations so the main control goroutine pauses only for syscalls and GPIO; the control thread fits entirely in CPU cache.

A fingerprint-keyed hardware database replaces the current hardcoded board list. Each board is identified by its SMBIOS signature and stored in a compact, queryable format. Module-loading cleanup stops shelling out to `modinfo` and instead discovers Super I/O drivers via the hwmon topology itself.

## Phase 2 — Backend portfolio

Native IPMI support without requiring the ipmitool binary. Direct USB access to the AIO ecosystem: Corsair Commander, NZXT Kraken X3/Z3, Lian Li UNI Hub, Aqua Computer Quadro and Octo, EK Loop Connect, and Gigabyte AORUS RGB Fusion 2. Each backend is a clean plugin that implements the same sensor and PWM control interface.

Laptop and framework embedded controllers: Framework Laptop PWM, Chromebook EC, ThinkPad EC, Dell EC, and HP EC. ARM SBC PWM via direct sysfs writes. Apple Silicon support via Asahi's power management interfaces. By the end of Phase 2, ventd runs on every major hardware class—desktop, laptop, workstation, ARM—with the same code path.

## Phase 3 — Install and module subsystem

A capability-scoped modprobe sidecar handles kernel module loading so the long-running daemon stays zero-capability. Generalised udev rules work across all GPU types and PWM topologies without chip-specific customisation. The installer detects and coexists with fancontrol, thinkfan, and CoolerControl, migrating their config if desired or running alongside them.

ventd-recover is a tiny, dependency-free helper that runs on ungraceful daemon exit—SIGKILL, OOM kill, kernel panic—to restore firmware fan control within two seconds. It carries no configuration and no state, handing control back to BIOS auto without sharing memory with the main daemon.

## Phase 4 — Control algorithm

PI control with anti-windup and NaN fallback for robustness. Ziegler-Nichols relay autotune lets the system characterise its own dynamics—no manual PID tuning required. Receding-horizon model-predictive control, trained online, learns the per-machine thermal model and predicts load spikes before the fan curve reacts, quieting the system without sacrificing headroom.

Banded hysteresis creates three engagement zones—quiet, normal, and boost—so fans don't hunt at band edges. Per-curve dither breaks beat frequencies between synchronised fans, eliminating the audible 10–20 Hz modulation when multiple fans are locked to the same PWM frequency.

## Phase 5 — Calibration and profile capture

Opportunistic in-use calibration during natural cooldowns—instead of a 60-second fan-howl ritual on first boot, ventd captures PWM→RPM curves in the background over hours of normal use. Drift detection spots when a fan's curve has changed and prompts recalibration. Local anonymised profile capture strips SMBIOS UUIDs, serials, and MAC addresses before anything leaves the machine, enabling crowd-sourced fan profiles for unseen boards.

## Phase 6 — Cross-platform

Windows via WMI temperature queries and documented ACPI fan methods, with no WinRing0 dependency. macOS via IOKit SMC, loaded at runtime through purego's dlopen, supporting both Intel and Apple Silicon. FreeBSD and OpenBSD via `hw.sensors` sysctl. Illumos via IPMI and fmtopo. Android via thermal_zone sysfs reads with root-gated writes.

A single codebase, target-specific backend selection, and the same control loop on every OS. By Phase 6, ventd is the cross-platform daemon: the single binary you install on any major operating system and forget about.

## Phase 7 — Advanced sensing

Per-fan per-PWM acoustic baselines captured automatically during calibration. At runtime, bearing-wear detection watches for the sub-harmonic at blade-pass-over-two—a mechanical signature that emerges weeks before audible failure. Flow and coolant-temperature readings from AIOs are collected and surfaced in the dashboard.

A thermal-headroom broadcast at `/run/ventd/headroom` lets tuned, auto-cpufreq, and similar integrations consume headroom data and make their own power and frequency decisions. The system becomes a unified thermal orchestrator.

## Phase 8 — Observability and fleet

Opt-in Prometheus `/metrics` endpoint with histograms of control loop latency, thermal error, and PWM adjustments. OpenTelemetry traces for the control loop and calibration runs, exportable to any trace backend. A 30-minute in-process history ring with HTTP readout provides post-incident analysis without external storage.

A `ventdctl` command-line tool communicates over a unix socket for scripting and debugging. mDNS discovery and gossip membership allow a group of ventd nodes to appear as a single fleet view—useful for data centres and research labs where cohesive thermal policy across many machines is important.

## Phase 9 — UX and pre-OS

A zero-click setup wizard runs on any board with a curated profile match, asking nothing beyond a password. Locale infrastructure brings the UI to English, German, French, Spanish, Japanese, and Simplified Chinese. Expression-language curves let power users author custom temperature-to-PWM formulas in a bounded sandbox—no I/O, no filesystem, no network access.

A signed UEFI DXE stub pre-programs Super I/O fan control at POST, so machines are never hot during early boot, BIOS setup, or OS loading.

## Phase 10 — Release supply chain

CycloneDX and SPDX software bills of materials on every release document dependencies and their licenses. Cosign keyless signing with SLSA L3 provenance attest that binaries came from this repository's CI. Reproducible builds, verified by rebuild-and-diff, let any user independently confirm that the published binary matches the source code.

Permissions-Policy explicitly denies unused browser features—camera, microphone, geolocation—so a compromised same-origin page cannot request them. ETag validation ensures the browser cache correctly validates across releases, preventing stale UI. Together with SBOM, cosign signing, SLSA provenance, and reproducible builds, the release artifact becomes a verified, auditable statement of what the software is.
