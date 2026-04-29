# 2026-04 userspace fan-control integration survey

**Status:** Research artifact. Produced 2026-04-26 in chat 2 of spec-03 PR 2 design.
**Purpose:** For each candidate userspace fan-control tool, decide license compatibility with ventd's GPL-3, what database/code/coverage we could integrate or reuse, and the recommended integration path.
**Consumed by:** spec-03 amendment PWM controllability (capability enum, conflicts_with_userspace), spec-09 NBFC integration (full schema + EC-direct backend design), spec-03 PR 2c diagnostic bundle (conflict detection list).

**Phoenix's directive:** v1.0 ships NBFC integration via Option B (EC-direct backend + config DB sync). This survey scopes the integration surface for spec-09 and identifies what other tools are worth integrating, referencing, or merely detecting.

---

## 1. Executive findings

Six structural takeaways:

### 1.1 License landscape is dominated by GPL-3 / GPL-2-or-later

Of the 11 tools surveyed, 7 are GPL-3 or GPL-2-or-later (NBFC-Linux, liquidctl, CoolerControl, thinkfan, LenovoLegionLinux, msi-ec, original NBFC). 1 is AGPL-3 (fan2go — strongest copyleft). 1 is MPL-2.0 (LibreHardwareMonitor — file-level copyleft, GPL-3 compatible at link time). 1 is BSD-3 (fw-ectool / Chromium EC). 1 is MIT (MsiController). 1 is GPL-2 only (i8kutils, mostly EOL).

**ventd implication:** consuming code from all GPL-3/GPL-2-or-later tools is direct-copy compatible. AGPL-3 (fan2go) requires that **if ventd were ever served over a network**, the AGPL trigger fires and ventd's source must be offered to network users. ventd's daemon model with a local web UI (per masterplan) makes this a real concern — borrowing fan2go code wholesale would AGPL-poison ventd. **Pattern-match fan2go's logic, don't copy its code.**

MPL-2.0 (LHM) is unique: file-level copyleft. ventd can include unmodified LHM source files in its tree as long as they keep their MPL header; modifications stay MPL-2.0; the binary as a whole is GPL-3. This is workable but requires careful packaging. **Recommendation: extract LHM register knowledge into ventd's own GPL-3 catalog YAML rather than vendoring LHM source.**

BSD-3 (fw-ectool) is permissive. Re-implement freely, attribute the original.

### 1.2 The reusable assets divide into three classes

- **Hardware databases.** LHM Super-I/O register layouts (most valuable, drives Fred78290's nct6687d). NBFC laptop EC configs (~hundreds of XML/JSON, drives Option B). liquidctl USB HID device tables (60+ AIO/PSU/hub drivers). LenovoLegionLinux's 10-point firmware fan curve register layout.
- **Detection / probing logic.** fan2go's `detect` command pattern (regex-match controller platforms). CoolerControl's hwmon enumeration. liquidctl's `--unsafe` flag pattern for risky operations. NBFC's `nbfc config --recommend` (DMI similarity matching).
- **Operational patterns.** fan2go's `pwmMap` (autodetect/identity/linear/values modes for fan-PWM-to-RPM mapping calibration) — this is **structurally identical** to spec-03 PR 2b calibration probe. fan2go's `sanityCheck.pwmValueChangedByThirdParty` — **runtime third-party-writer detection**, more sophisticated than ventd's current install-time conflict detection. CoolerControl's "ability to handle hwmon devices that only have manual mode, aka do not have a pwm_enable implementation" — same `capability` distinction as spec-03 amendment.

### 1.3 Three tools deserve deep integration; the rest are detection targets only

- **Deep integration (consume databases or shared code paths):** NBFC-Linux (Option B), LibreHardwareMonitor (extract Super I/O register knowledge), liquidctl (consult device tables for Tier-E expansion).
- **Reference-only (study patterns, don't import):** fan2go (Go peer, AGPL prevents copy), CoolerControl (Rust peer, valuable architectural notes).
- **Detection-only (`conflicts_with_userspace` schema entries + diag bundle detection):** thinkfan, LenovoLegionLinux GUI, MControlCenter, fancontrol, jupiter-fan-control, mbpfan, tpfancod.

### 1.4 The "everyone reverse-engineers Windows" pattern is real

LHM is the canonical Windows-port-based register database. Fred78290's `nct6687d` cites LHM directly. NBFC's original .NET version was Windows-first; NBFC-Linux is the C port. msi-ec was reverse-engineered from MSI's Windows utilities. LenovoLegionLinux openly cites Windows tools (LegionFanControl, Vantage, LenovoLegionToolkit) as sources. **ventd's Linux-first stance is unusual.** When new chips appear, the discovery path runs Windows-first → reverse engineer → kernel/userspace Linux port. ventd should publish a "how to add hardware support" guide that points contributors at this canonical path.

### 1.5 The kernel-mainlining pipeline is alive but uneven

`liquidctl/liquidtux` is actively merging kernel-side drivers for what was previously USB-HID userspace (corsair-cpro, nzxt-kraken3, aquacomputer-d5next). msi-ec went mainline. LenovoLegionLinux's `legion-laptop` is in mainline (since ~6.5). yogafan went mainline in 7.1. **Trend:** userspace daemons are temporary scaffolding for kernel drivers. ventd should treat this as the assumed direction — when a kernel driver appears, ventd switches to using it; when no kernel driver exists, ventd falls back to userspace tools.

### 1.6 Three integration anti-patterns to avoid

- **Don't reimplement liquidctl's USB HID logic.** ventd already shipped a Corsair Core HID backend (v0.4.0) and Commander Pro (spec-02a planned). Each device costs weeks of reverse engineering. **liquidctl already covers 60+ devices.** When ventd needs to expand AIO coverage, the right path is liquidctl-FFI (call into Python from Go via subprocess), not Go-port-the-driver. Costs token-frugality of CC sessions.
- **Don't claim to "subsume" NBFC.** NBFC has hundreds of laptop configs maintained by the community. Forking them is a perpetual-maintenance trap. Option B = consume, sync, attribute. spec-09 must be explicit: ventd ships the EC backend, NBFC ships the config database.
- **Don't fork LHM.** It's 8k+ stars and 1453 commits of register-engineering work. Cherry-pick register knowledge into ventd's catalog YAML; don't try to track LHM's changes.

---

## 2. Tool-by-tool deep dive (5 high-value)

### 2.1 LibreHardwareMonitor (LHM) — **Reference + extract register knowledge**

**Repo:** https://github.com/LibreHardwareMonitor/LibreHardwareMonitor
**License:** MPL-2.0 (with BSD-licensed Aga.Controls UI submodule and LGPL-2.1 PawnIO.Modules ring-0 driver — both Windows-only, irrelevant to ventd)
**Language:** C# 97.6% (LibreHardwareMonitorLib targets .NET Framework 4.7.2 / .NET Standard 2.0 / .NET 8/9/10)
**Stars / Activity:** 8.2k stars, 906 forks, v0.9.6 (Feb 2026), 1453 commits. Active.
**Coverage:** Comprehensive Super I/O register database for ITE, Nuvoton, Winbond, SMSC, Fintek; per-board overrides; AIO USB HID. Windows-first but the *register knowledge* is platform-independent.
**What ventd could reuse:**
- Register-layout knowledge from `LibreHardwareMonitorLib/Hardware/Motherboard/Lpc/` — file-by-file mapping per chip family (NCT6798D registers, IT8689E layouts, etc.). The mainline kernel `nct6775` driver is incomplete relative to LHM (e.g. NCT6799D PCH-fan support is missing in mainline per LHM issue #1993).
- Per-board manufacturer overrides under `LibreHardwareMonitorLib/Hardware/Motherboard/` — the "Asus ROG Strix X670E-E" type files. These encode CPUTIN-floats quirk, voltage pinout, fan-channel labelling.
- The PR history is useful: each "Add support for ASUS X870 board" PR exposes one row of register data per merge.
**What ventd could integrate:** None directly (don't vendor LHM). **Process: extract register tables into ventd's `internal/hwdb/catalog/*.yaml` with attribution comment per chip.**
**License compat:** MPL-2.0 is GPL-3 compatible at the binary level; file-level copyleft means modifications to vendored MPL files stay MPL. **For ventd's purposes: don't vendor; transcribe register knowledge into ventd's GPL-3 catalog with citations to specific LHM commits.** Use git's BLAME view on the register file to credit the original contributor.
**Maintenance burden:** Zero ongoing — ventd's catalog is its own. New LHM releases are *informational input* for periodic catalog refreshes (quarterly).
**Recommended integration path:** **Reference-extract.** Document ventd's catalog refresh process. When a new chip lands in LHM, ventd's contributor copies the register knowledge into the catalog YAML.
**Risk:** LHM is community-maintained; some board profiles are speculative. ventd should treat LHM data as "best-known starting point that must be validated by ventd's calibration probe."

### 2.2 NBFC-Linux — **Deep integration (Option B confirmed for v1.0)**

**Repo:** https://github.com/nbfc-linux/nbfc-linux (active C port; original .NET at https://github.com/hirschmann/nbfc, less active)
**License:** GPL-3.0 (compatible)
**Language:** C, latest commits 2026-03
**Stars / Activity:** Actively maintained, separate from original .NET hirschmann/nbfc.
**Coverage:** Hundreds of laptop EC configs in `/usr/share/nbfc/configs/` — Acer Aspire, Asus Zenbook UX*, Dell Inspiron legacy + new, HP Pavilion / EliteBook / Victus, Lenovo non-IdeaPad/Yoga/Legion (since those have dedicated kernel modules now), MSI Modern / Stealth / GT / GE / GP, Toshiba Satellite, Xiaomi Mi Notebook, Framework, Surface (community).
**Architecture:**
- Userspace daemon talks to laptop EC via either `ec_sys` kernel module (preferred — needs `ec_sys.write_support=1` kernel parameter) or `/dev/port` (alternative — `--embedded-controller=dev_port`).
- Per-laptop JSON configs (NBFC-Linux switched from .NET XML to JSON; conversion tool included). Schema documented at `nbfc-linux/doc/nbfc_service.json.5.md`.
- Key config fields: `NotebookModel`, `Author`, `EcPollInterval`, `CriticalTemperature`, `CriticalTemperatureOffset`, `ReadWriteWords` (8-bit vs 16-bit register width), `FanConfigurations[].ReadRegister`, `WriteRegister`, `MinSpeedValue`, `MaxSpeedValue`, `IndependentReadMinMaxValues`, `MinSpeedValueRead`, `MaxSpeedValueRead`, `ResetRequired`, `FanSpeedResetValue`, `TemperatureThresholds[].UpThreshold`, `DownThreshold`, `FanSpeed`, `FanSpeedPercentageOverrides[].FanSpeedPercentage`, `FanSpeedValue`, `RegisterWriteConfigurations[].WriteOccasion` (`OnInitialization` | `OnWriteFanSpeed` | `OnWriteFanSpeedReset`), `Register`, `Value`, `ResetValue`, `Description`. Optional fields: `ReadWriteMode` (`R` | `W` | `RW`), `Mode` (`Set` | `And` | `Or` | `XOR`).
- Tools: `nbfc config --recommend` does DMI similarity matching against config filenames. `nbfc set --auto` enables temperature-based control. `nbfc sensors` configures temperature input.
- Probing tool `ec_probe` separately reads/writes EC registers and dumps DSDT for new-config development.
**Critical architectural gotchas (would inherit if ventd-integrate-direct):**
- `ec_sys` kernel module: many distros build it without `write_support` enabled. Some distros (Fedora 38+) deprecated `ec_sys` entirely. Fallback path uses `/dev/port` which requires `CAP_SYS_RAWIO`.
- Misconfiguration → laptop hard-shutdown / EC bricking. NBFC configs are submitted by laptop owners who tested on their specific BIOS revision. A config that works on BIOS 1.05 may brick BIOS 1.10.
- Newer kernels (post-6.x) ship `ec_sys` deprecation warnings; long-term path needs migration.
**What ventd integrates (spec-09 scope):**
1. **Config database consume.** ventd parses NBFC's JSON configs from `/usr/share/nbfc/configs/` directly (no fork). DMI fingerprint match like `nbfc config --recommend`.
2. **EC backend.** ventd implements its own EC-direct backend (Go), reading/writing via `/dev/port` with `CAP_SYS_RAWIO` privilege-separation sidecar (the daemon-proper drops privileges; sidecar holds `CAP_SYS_RAWIO`). spec-09 details.
3. **Config sync.** ventd ships a small CLI `ventd nbfc-sync` that pulls latest configs from upstream nbfc-linux (configurable refresh interval; respects user opt-out for offline systems). Configs cached in `/var/lib/ventd/nbfc-configs/`.
4. **Detection of running NBFC service.** If `nbfc_service.service` is running, ventd refuses to take over the EC and surfaces "uninstall NBFC or stop its service before ventd can manage your laptop's fans."
5. **Ship NBFC's `Author` field as attribution.** spec-09 must require ventd's UI to display "Configuration: <NotebookModel> by <Author>, sourced from NBFC-Linux <commit>" when running on an NBFC-derived config.
**License compat:** GPL-3 → GPL-3 trivially.
**Maintenance burden:** **High but bounded.** Tracking NBFC config schema changes (rare — schema is mature). Periodic config sync. CVE-class issues if ventd's EC writes have a bug (real risk: bricked laptops). **Mitigation:** ventd's NBFC backend ships in `--read-only` default until user explicitly opts into write mode (mirroring NBFC's own design).
**Recommended integration path:** **Spec-09 deep integration.** ventd v1.0 ships full Option B. Detection (Option D) only as a fallback when NBFC isn't installed and Option B can't run for some reason (e.g. distro doesn't ship `ec_sys` write support).
**Risk:** **EC-write bugs can brick laptops.** spec-09 must enumerate the safety model. Ideally ventd also integrates with `ec_probe`-style diagnostic mode so users can verify a config before enabling write mode.

### 2.3 liquidctl — **Reference + selective FFI/subprocess for AIO/PSU coverage**

**Repo:** https://github.com/liquidctl/liquidctl
**License:** GPL-3.0
**Language:** Python (94.8%)
**Stars / Activity:** 2.6k stars, 262 forks, v1.16.0 (Mar 2026). Very active. CII Best Practices badge. Sibling project `liquidctl/liquidtux` ports drivers to Linux kernel hwmon.
**Coverage (60+ devices):** AIO liquid coolers (Corsair Hydro/iCUE Capellix, NZXT Kraken series, EVGA CLC, ASUS Ryujin/Ryuo, MSI Coreliquid, Lian Li GA II LCD), pump controllers (Aquacomputer D5 Next), fan/LED hubs (Aquacomputer Octo/Quadro/Farbwerk, Corsair Commander Pro/Core/Core XT/ST/Lighting Node, Lian Li Uni SL/AL, NZXT Smart Device V1/V2/Grid+/HUE 2, NZXT RGB & Fan Controller variants), PSUs (Corsair HX/RM series, NZXT E-series), motherboard RGB (ASUS Aura LED, Gigabyte RGB Fusion 2.0), GPU RGB (select ASUS/EVGA), DDR4 memory (Corsair Vengeance RGB).
**Notes flags:** `B`=broken-in-significant-way, `L`=requires-`--legacy-690lc`, `U`=requires-`--unsafe`, `Z`=needs-driver-replacement-on-Windows, `a`=architecture-specific, `h`=can-leverage-hwmon, `p`=partial, `x`=Linux-only.
**What ventd could reuse:**
- **Device tables** in `liquidctl/driver/*.py` — vendor/product IDs, init sequences, status-message parsing, fan/pump speed setpoint encoding. ventd's existing v0.4.0 Corsair Core backend covers exactly one of liquidctl's many devices; expanding to NZXT Kraken etc. would re-walk the same reverse-engineering path liquidctl already published.
- **udev rules** at `extra/linux/71-liquidctl.rules` — covers all supported devices. ventd's HID backends need analogous rules; copying liquidctl's covers most ground.
- **Stability guarantee** at `docs/developer/process.md` — well-documented stable-API surface ventd could call.
- **Yoda script** `extra/yoda.py` — host-based fan/pump control with curves. Reference for ventd's control-loop logic.
**Integration paths considered:**
- **A. Direct port to Go.** Each device is multiple person-months. ~60 devices × person-months = unaffordable. **REJECTED.**
- **B. Subprocess invocation.** ventd shells out to `liquidctl --json status` and `liquidctl set ... speed ...`. Cheap to integrate. CoolerControl does this via embedded `coolercontrol-liqctld` child process (a Python service that exposes liquidctl). **VIABLE for v0.7+.**
- **C. Python embedding via cgo.** Adds CPython runtime dependency. Violates ventd's CGO_ENABLED=0 invariant. **REJECTED.**
- **D. Wait for kernel mainlining.** liquidctl/liquidtux is actively mainlining drivers. corsair-cpro is mainline already; NZXT Kraken3 is in progress. **Strategy: ventd uses kernel drivers when present, falls back to liquidctl subprocess for unmapped devices.**
**License compat:** GPL-3 → GPL-3, trivial.
**Maintenance burden:** **Low if subprocess.** liquidctl handles its own device updates. ventd needs to track liquidctl's CLI output schema (versioned, `--json` output stable per documented guarantee). One Go wrapper module in `internal/liquidctl/`.
**Recommended integration path:** **D + B.** v1.0 ships kernel-driver path for AIOs that have one (`corsair-cpro` etc.). Post-v1.0, optional `ventd-liquidctl-bridge` package shells out to liquidctl for devices without kernel drivers. Bridge is opt-in to keep CGO=0 default install slim.
**Risk:** liquidctl's AIO write protocols can also brick devices. ventd's bridge ships read-only default; write requires user opt-in.

### 2.4 CoolerControl — **Reference architecture peer**

**Repo:** https://gitlab.com/coolercontrol/coolercontrol (mirror at https://github.com/codifryed/coolercontrol)
**License:** GPL-3.0+
**Language:** Rust (daemon `coolercontrold`), Vue (web UI), Python (legacy `coolercontrol-liqctld` now embedded as subprocess), Qt (optional desktop GUI)
**Stars / Activity:** Latest 4.1.0 (Mar 2026). Active. Discord community for troubleshooting.
**Architecture (highly relevant to ventd):**
- Daemon (`coolercontrold`) runs as system service, REST API on `localhost:11987`, embedded web UI.
- Embeds `coolercontrol-liqctld` as child process for liquidctl-backed devices (since major refactor noted in changelog).
- "Custom Sensors" (file-based mixes/offsets) + "Overlay Profiles" (advanced offset controls on top of base profiles).
- Firmware-controlled profile support for some AMDGPUs and liquidctl devices (notes "hwmon device support coming soon" — same direction ventd is heading with P4-HWCURVE in spec-05).
- "Refactored the safety latch so that fan curves are hit after a period of time, regardless of thresholds set" — analogous to ventd's safety-latch design.
- "Periodic checks of actual status to handle external program/command changes" — same problem ventd faces (multiple writers to pwm sysfs).
**What ventd could reuse:**
- **Architectural patterns.** REST API shape, web UI structure, embedded-subprocess pattern (for liquidctl integration in v0.7+).
- **Safety design lessons.** CoolerControl had to introduce a "safety latch hits regardless of thresholds" — read their changelog history for what edge cases they hit.
- **Hardware coverage notes** at `docs.coolercontrol.org/hardware-support.html` — explicitly documents the same OOT-driver landscape (nct6687d for MSI, etc.) and points to NBFC for unmapped laptops.
- **AppImage packaging** approach — for distros without native packages.
**What ventd should NOT do:**
- **Don't try to be CoolerControl.** CoolerControl has a GUI culture and feature surface (overlay profiles, custom sensors, mixes) that ventd's "zero-terminal, zero-config" mission deliberately rejects. ventd is a daemon-first tool; CoolerControl is a power-user GUI tool. **They serve different audiences.** ventd's killer feature is predictive thermal control (spec-05); CoolerControl is reactive control with great UX.
**License compat:** GPL-3+ trivially compatible.
**Maintenance burden:** Zero — reference-only.
**Recommended integration path:** **Reference + interoperate.** Detect `coolercontrold.service` running, surface "stop CoolerControl before installing ventd" warning. List CoolerControl in `conflicts_with_userspace`.
**Risk:** Some users will want CoolerControl's GUI on top of ventd's daemon. Document this as out-of-scope for v1.0; community contribution welcome.

### 2.5 LenovoLegionLinux — **Reference + integrate hwmon attrs (when present)**

**Repo:** https://github.com/johnfanv2/LenovoLegionLinux
**License:** GPL-2.0-or-later (compatible with GPL-3)
**Language:** C (kernel module `legion-laptop`) + Python (CLI/GUI tools `legion_cli`, `legion_gui`)
**Coverage:** Lenovo Legion 5/7/Slim/Pro/LOQ + Yoga Pro + IdeaPad Gaming. DMI-allowlist gated; `force=1` module param to override.
**Architecture:**
- Kernel module exports `/sys/kernel/debug/legion/fancurve` (read-only) AND standard hwmon attrs `pwm{1,2}_auto_point{1-10}_pwm` (writable!) — this is the **10-point hardware fan curve** stored in EC firmware, exposed as standard hwmon trip points.
- Embedded controller has 2-fan, 10-point curve in ROM-extension custom firmware. Lenovo's own Vantage/LegionFanControl tools edit this same EC memory.
- `legion-laptop` is mainlined as of ~kernel 6.5. Out-of-tree DKMS available for older kernels.
- Power-mode coupling: Fn+Q changes mode in firmware *and* the curve; if user (or a tool) edits the curve while a different mode is active, EC may overwrite on next mode change.
**Critical insight for ventd's spec-05 (P4-HWCURVE):** This is **exactly the hardware-curve offload model spec-05 envisions**. When ventd is suspended (power management) but the system is under load, the EC's hardware curve takes over. ventd's predictive thermal model can write a custom curve to `pwm1_auto_pointN_pwm` and let it run autonomously. **legion-laptop is the closest existing implementation of P4-HWCURVE in the Linux ecosystem — direct prior art.**
**What ventd could reuse:**
- **EC register layouts** for Legion fan curves — but these are per-firmware (EC version) and Lenovo proprietary. Treat as reference, don't import register addresses.
- **DMI allowlist pattern** — ventd's matcher already does similar; cross-reference for completeness.
- **Architectural lesson:** "the controller might have loaded default values if you pressed Ctrl+Q (or FN+Q on certain devices) to change the power mode" — daemon must re-apply curve on mode change. ventd should subscribe to `platform_profile` events to detect mode switches.
**License compat:** GPL-2-or-later compatible with GPL-3.
**Maintenance burden:** **Low (legion-laptop is mainline).** ventd treats `legion_hwmon` as a regular hwmon source per spec-03 schema with `capability: rw_full`, `pwm_unit: duty_0_255`, and chip profile pointing at hwmon attrs. The 10-point auto curve is a P4-HWCURVE candidate — schema field `pwm_enable_modes` includes a Lenovo-specific mode entry (or generic "firmware_curve" mode) when `legion_hwmon` is the driver.
**Recommended integration path:** **Treat legion_hwmon as a first-class hwmon source.** Add catalog entry. Tier-2 board profiles for Legion family override defaults to use the firmware-curve path when spec-05 P4-HWCURVE ships. Subscribe to platform_profile mode-change events for re-apply.
**Risk:** Lenovo BIOS updates change EC firmware; calibration per-firmware (already in spec-03 amendment §15 `firmware_version` field). Power-mode races require event-driven re-apply (post-v1.0 polish).

---

## 3. Tool-by-tool quick coverage (5 lower-priority)

### 3.1 fan2go — **Reference architectural patterns; do NOT borrow code (AGPL)**

**Repo:** https://github.com/markusressel/fan2go
**License:** **AGPL-3.0** (sensors package confirmed via pkg.go.dev; matches `fan2go-tui` companion which is AGPL-3-or-later)
**Language:** Go (closest peer to ventd). 320 stars, latest 0.11.1 (Jun 2025).
**Architecture:**
- YAML config with `fans:`, `sensors:`, `curves:` sections.
- Backends: `hwmon`, `nvidia` (NVML), `file` (test/synthetic).
- `fan2go detect` enumerates hwmon platforms with regex matching.
- `pwmMap` config lets user override the autodetected PWM-input-to-actual-PWM mapping; modes: `autodetect` (sweep), `identity`, `linear` (interpolated control points), `values` (step-interpolated control points).
- `setPwmToGetPwmMap: autodetect` — does exactly the calibration probe spec-03 PR 2b is designing.
- `sanityCheck.pwmValueChangedByThirdParty` — runtime detection of competing writers.
- `controlMode` — explicit mode-on-exit behaviour (preserve fan to last value, force max, restore BIOS auto). **ventd should adopt this.**
**License risk:** AGPL-3 means: if ventd embeds fan2go source, ventd inherits AGPL. ventd's roadmap includes a web UI (per masterplan), which under AGPL would require source-offering to web-UI users. **AGPL-tainting is likely a bad fit for ventd's adoption goals.**
**Recommended integration path:** **Reference-only.** Pattern-match the architecture, write ventd's equivalent logic from scratch. Specifically borrow these *patterns* (not code):
- `pwmMap` autodetect → spec-03 PR 2b calibration probe (already planned).
- `sanityCheck.pwmValueChangedByThirdParty` → spec-03 PR 2c diagnostic + future runtime sanity check (post-v1.0).
- `controlMode` exit behaviour → ventd's safety-latch and shutdown handler.
**Detection-only:** if `fan2go.service` is running, surface "stop fan2go before installing ventd" warning. List in `conflicts_with_userspace`.

### 3.2 lm-sensors `fancontrol` — **Detection-only (the legacy ventd is replacing)**

**Repo:** https://github.com/lm-sensors/lm-sensors (canonical), `/usr/sbin/fancontrol` shell script
**License:** GPL-2.0-or-later (compatible)
**Language:** Bash (yes really)
**Coverage:** Anything hwmon. Single config at `/etc/fancontrol`, generated by `pwmconfig`.
**What ventd "replaces":** `fancontrol` is the canonical reactive-PWM-curve daemon. ventd's predictive thermal model (spec-05) is its successor pitch.
**Recommended integration path:** **Detection-only.** Surface "fancontrol detected at /etc/fancontrol; ventd will not run while fancontrol is active." Add to `conflicts_with_userspace`. Phoenix's masterplan README pitch can include "ventd is the modern replacement for fancontrol" framing.
**Maintenance:** Zero.

### 3.3 thinkfan — **Detection-only + ThinkPad-specific catalog hint**

**Repo:** https://github.com/vmatare/thinkfan
**License:** GPL-3.0 (compatible)
**Language:** C++. Uses YAML config since 1.0.
**Coverage:** ThinkPads (`/proc/acpi/ibm/fan` levels 0-7) + any hwmon source. Single fan only.
**What ventd should know:** `thinkfan_acpi.experimental=1` and `fan_control=1` module parameters required on T440+ generation. ventd's `thinkpad_acpi` catalog entry (spec-03 amendment §11.3) needs `required_modprobe_args: ["experimental=1", "fan_control=1"]`.
**Recommended integration path:** **Detection-only + register the catalog hint.** ventd's `thinkpad_acpi` driver entry includes the modprobe args from thinkfan's documented requirements. Detection: if `thinkfan.service` is running, surface "stop thinkfan before installing ventd" warning. List in `conflicts_with_userspace`.

### 3.4 fw-ectool (Framework) — **Reference for Cros-EC backend architecture**

**Repo:** https://github.com/FrameworkComputer/EmbeddedController (full firmware repo) and https://github.com/DHowett/fw-ectool (isolated CMake build of just the userspace tool)
**License:** BSD-3-clause (permissive — direct re-implementation allowed with attribution)
**Language:** C
**Coverage:** Framework Laptop 13 (Intel + AMD) + Framework 16. Mainline kernel support via `cros_ec_lpcs` driver since 5.19 (Framework 13 Intel) and 6.10 (Framework 13 AMD + 16).
**What ventd reuses:**
- **Use the kernel driver path (cros_ec_lpcs), not fw-ectool.** Mainline kernel cros_ec exposes hwmon fan attrs on Framework. ventd's catalog entry: `cros_ec_lpcs` with `capability: rw_full`, `pwm_unit: duty_0_255`. Detection via DMI vendor "Framework" + kernel module presence.
- **fw-ectool is fallback** for older kernels / non-mainlined Framework hardware. Optional `ventd-cros-ec-bridge` post-v1.0.
**License compat:** BSD-3 → GPL-3 trivial.
**Recommended integration path:** **Use kernel cros_ec_lpcs.** No userspace integration unless the user explicitly opts into the bridge.

### 3.5 i8kutils — **Detection-only (mostly EOL, subsumed)**

**Repo:** https://github.com/vitorafsr/i8kutils
**License:** GPL-2.0
**Language:** TCL/C. Last release several years ago.
**Coverage:** Dell laptops via `/proc/i8k`.
**Status:** Largely subsumed by `dell-smm-hwmon` kernel driver. Some users still run i8kutils on whitelisted models where hwmon doesn't expose `pwm_enable`. Notable: i8kutils's BIOS SMM calls cause kernel-level stalls / audio dropouts (referenced in ArchWiki and bug 201097) — same SMM-latency issue captured in spec-03 amendment §10 `polling_latency_ms_hint: 500` for dell-smm.
**Recommended integration path:** **Detection-only.** If `i8kmon.service` is running, surface "stop i8kmon; ventd will use dell-smm-hwmon instead" warning. Add to `conflicts_with_userspace`.

---

## 4. Surfaced-during-research candidates (covered briefly)

These weren't on Phoenix's original list but appeared during search and may matter:

### 4.1 mbpfan — **Detection-only (Apple coverage)**

**Repo:** https://github.com/dgraziotin/mbpfan
**License:** GPL-3.0 (compatible)
**Coverage:** MacBook / MacBook Pro running Linux natively, talks to `applesmc` driver.
**Why it matters:** Phoenix's matrix doesn't include Apple hardware, but contributors will eventually file issues. ventd's `applesmc` catalog entry should reference mbpfan's documented behaviour. Detection-only.

### 4.2 tpfancod — **Skip (effectively dead)**

**Repo:** https://github.com/tpfanco/tpfancod
**License:** GPL-3.0
**Status:** Last meaningful commit 2018. Subsumed by thinkfan + native thinkpad_acpi.
**Recommended:** No catalog entry needed; not worth detection logic.

### 4.3 jupiter-fan-control — **Detection-required (Steam Deck)**

**Source:** SteamOS proprietary; Jovian-Experiments has reverse references at github.com/Jovian-Experiments
**License:** Mixed (SteamOS is closed; reverse references vary)
**Coverage:** Steam Deck (LCD + OLED). Runs as systemd service on SteamOS, controls `steamdeck-hwmon` driver.
**Recommended integration path:** **Detection-required.** ventd's `steamdeck_hwmon` catalog entry has `conflicts_with_userspace: ["jupiter-fan-control"]` with `resolution: stop_and_disable`. Already in spec-03 amendment §8 example.

### 4.4 nvfd — **Skip (overlaps with NVML in spec-03b GPU catalog)**

**Repo:** https://github.com/Infinirc/nvfd
**License:** Likely GPL/MIT (need to verify; superficial search not enough).
**Coverage:** NVIDIA GPU fans via NVML.
**Why skip:** ventd's GPU vendor catalog (spec-03b, separate from this spec) handles NVIDIA via NVML directly. nvfd is a userspace daemon doing the same thing; reference-only at most.

### 4.5 fancon — **Skip (single-developer tool)**

**Repo:** https://github.com/hbriese/fancon
**License:** GPL-3.0
**Status:** Less active than fan2go / CoolerControl. C++. Niche.
**Recommended:** No special handling.

### 4.6 TuxClocker — **Skip (GPU overclocker, not fan-control-primary)**

**Repo:** https://github.com/Lurkki14/tuxclocker
**Coverage:** Primarily GPU overclocking via Qt GUI. Has fan control but secondary.
**Recommended:** No special handling.

### 4.7 amdgpu-fancontrol script — **Skip (single-script, niche)**

User-shell-script-class tools. Detection-only at best, but probably not worth even that.

---

## 5. Recommended additions to spec-03 schema and downstream specs

This survey surfaced concrete additions to spec-03 amendment beyond what map §9 captured:

### 5.1 `runtime_conflict_detection` — runtime sanity check beyond install-time

Per fan2go's `pwmValueChangedByThirdParty`. ventd's spec-03 PR 2 currently does conflict detection at install time. fan2go and CoolerControl both do **runtime detection** — every control loop, check if the actual `pwm` value matches what ventd last wrote. If mismatch, flag external writer.

**Suggestion for spec-05 or post-v1.0 polish:** Add `internal/sanity/` package with periodic check. Not in spec-03 PR 2 scope but worth tracking as v0.7+ feature.

### 5.2 `controlMode.exitBehaviour` — explicit shutdown behaviour

Per fan2go's `controlMode`. When ventd shuts down (SIGTERM, service stop, crash), the fan-control state must transition cleanly. Options: preserve last value, force max (safe), restore BIOS auto (`pwm_enable=2/5`).

**Suggestion for spec-03 amendment:** Add `exit_behaviour` enum field on driver_profile: `preserve | force_max | restore_auto`. Default per driver: `force_max` for Super I/O (safe), `restore_auto` for laptop EC (laptop firmware will resume control), `preserve` if the hardware doesn't auto-resume.

### 5.3 NBFC `Author` attribution chain

Per spec-09 design: when ventd uses an NBFC-derived config, the UI must display `<NotebookModel> by <Author>` per the original NBFC convention. Required for GPL-3 attribution discipline on derivative configs.

### 5.4 `firmware_curve_offload_capable` flag

Per LenovoLegionLinux + nct6775 Smart Fan IV. Some drivers expose chip-internal trip-point tables (`pwmN_auto_pointM_pwm`/`_temp`). spec-05 P4-HWCURVE wants to write to these. spec-03 amendment §5 captures this in `pwm_enable_modes` (`smart_fan_iv`, `auto_trip_points`, etc.) but doesn't surface it as a top-level capability flag for spec-05's matcher.

**Suggestion for spec-03 amendment v1.1:** Add boolean `firmware_curve_offload_capable: bool` derived field that the matcher computes from `pwm_enable_modes` containing any of {`thermal_cruise`, `smart_fan_iv`, `auto_trip_points`}. spec-05 P4-HWCURVE consumes this.

---

## 6. License compatibility summary table

| Tool | License | ventd compat | Reuse path |
|---|---|---|---|
| LibreHardwareMonitor | MPL-2.0 | OK at link; file-level for vendoring | Reference-extract register knowledge |
| NBFC-Linux | GPL-3.0 | Direct | Consume config DB + Option B EC backend |
| liquidctl | GPL-3.0 | Direct | Subprocess bridge (post-v1.0) |
| CoolerControl | GPL-3.0+ | Direct | Reference architecture; detect+conflict |
| LenovoLegionLinux | GPL-2-or-later | Direct | Treat hwmon attrs as first-class |
| fan2go | **AGPL-3.0** | **Pattern-only** | **Reference architecture; do NOT copy** |
| lm-sensors fancontrol | GPL-2-or-later | Direct | Detect + conflict |
| thinkfan | GPL-3.0 | Direct | Detect + conflict + catalog hint |
| fw-ectool | BSD-3 | Direct | Use cros_ec_lpcs kernel; bridge fallback |
| i8kutils | GPL-2 | Direct | Detect + conflict |
| msi-ec | GPL-2-or-later (kernel module) | Direct | First-class hwmon source |

---

## 7. spec-09 NBFC integration scoping (preview for chat 2 Phase 3)

Based on this survey, spec-09 should cover:

1. **Config-DB consume.** ventd parses NBFC JSON configs from `/usr/share/nbfc/configs/*` at startup. Cache in `/var/lib/ventd/nbfc-cache/`. Schema struct defined in `internal/nbfc/config.go`. Citations ship with each config.
2. **EC-direct backend.** ventd's `internal/backend/ec_direct/` package. Reads/writes via `/dev/port` with `CAP_SYS_RAWIO` privilege-separation sidecar. Default `ec_sys` write path when available; fallback to `/dev/port` when `ec_sys` lacks `write_support`.
3. **Privilege-separation sidecar design.** `ventd-ec-helper` binary shipped separately, run as root with `CAP_SYS_RAWIO`. Daemon-proper drops privileges. Communication via Unix-domain socket with strict command schema. Audit-logged on every write. spec-09 details.
4. **DMI similarity matcher.** Port NBFC's `nbfc config --recommend` logic — Levenshtein/token-set similarity over `DMI_PRODUCT_NAME` against config filenames. Returns ranked candidates. User confirms before write mode enabled.
5. **Read-only default.** ventd ships with NBFC configs in read-only mode by default. User opts into write mode after manually verifying `nbfc.json` config matches their hardware (analogous to ventd's existing safety-latch design).
6. **Config sync mechanism.** `ventd nbfc-sync` CLI (cron-able) pulls latest configs from upstream nbfc-linux GitHub. Respects offline mode (no auto-network). Versioned cache.
7. **Conflict detection.** Refuses to start NBFC backend if `nbfc_service.service` is running. Mirrors NBFC's own behaviour (only one EC writer at a time).
8. **Attribution UX.** UI displays "Configuration: <NotebookModel> by <Author>, sourced from NBFC-Linux <commit>" when running on derived config.
9. **Target release.** v1.0 alpha alongside spec-05 phases. Earlier release possible if the EC-direct backend lands cleanly (v0.8.0 candidate).
10. **Out-of-scope for spec-09 v1.0.** Custom config authoring (use NBFC's `ec_probe` for new hardware). DSDT analysis tools. Config quality rating (`nbfc rate-config`).

---

## 8. Diagnostic bundle additions (preview for chat 2 Phase 2 #5)

Beyond §10 of the controllability map, this survey identifies additional userspace-tool detection signals for `ventd diag bundle`:

- `systemctl is-active fan2go` (with running config dump if active)
- `systemctl is-active coolercontrold` + `coolercontrol --version`
- `systemctl is-active liquidcfg` (liquidctl's typical service unit name)
- `pgrep -f LegionFanControl|LegionToolkit` (Windows-leak case via Wine, real)
- `pgrep -f legion_cli|legion_gui`
- `dmesg | grep -i "ec_sys.*write_support"` — flags whether NBFC backend can write
- `cat /proc/cmdline | grep -E "ec_sys.write_support|thinkpad_acpi.fan_control"` — kernel param state
- `/usr/share/nbfc/configs/*.json` count + cache freshness
- `cargo install --list | grep coolercontrol` (manual installs)
- `pip list | grep liquidctl` (manual pip install)

---

## 9. Open questions (for Phoenix)

### 9.1 spec-09 release target — v0.8.0 or v1.0 alpha?

This survey makes spec-09 look more tractable than initially scoped (NBFC has a clean JSON schema, GPL-3 license, mature config DB). Phoenix's prompt said "v1.0 alpha alongside spec-05 phases." This survey suggests **spec-09 could land in v0.8.0** (pre-spec-05) because:
- The EC backend is independent of predictive thermal.
- It immediately unlocks a 30%+ laptop install surface that spec-03 alone leaves uncontrolled.
- It can ship in `--read-only` default and bake for users while spec-05's predictive engine matures.

**Recommendation:** target **v0.8.0** for spec-09. v1.0 ships spec-05 + spec-09 mature.

### 9.2 liquidctl bridge — v0.7+ optional, or never?

Phoenix's masterplan AIO roadmap is one-vendor-at-a-time (Corsair Core shipped, Commander Pro next, then NZXT Kraken, etc.). This survey suggests an **optional liquidctl-subprocess bridge** could short-circuit the per-vendor implementation pipeline by 90% — at the cost of a Python runtime dependency and `coolercontrol-liqctld`-style subprocess complexity.

**Recommendation:** **not yet.** Stay on the kernel-driver-first path through v1.0. Reconsider for v1.x once Phoenix sees how AIO coverage demand actually plays out.

### 9.3 LHM register-knowledge import process — automated or manual?

Manual transcription of LHM register tables into ventd YAML is error-prone but legally clean. An automated script (`tools/import-lhm-registers.py`) could parse LHM C# source files and emit YAML — but generates derivative work issues (the script's output is arguably a derivative of LHM, retaining MPL-2.0 file-level copyleft on the YAML output).

**Recommendation:** **manual, with a documented per-PR process.** Each "import LHM register data for chip X" PR cites the specific LHM commit and contributor. ventd's YAML stays GPL-3-clean. Slower but legally unambiguous.

---

## 10. Citations

GitHub repos:
- LibreHardwareMonitor: https://github.com/LibreHardwareMonitor/LibreHardwareMonitor
- NBFC-Linux: https://github.com/nbfc-linux/nbfc-linux
- NBFC original: https://github.com/hirschmann/nbfc
- liquidctl: https://github.com/liquidctl/liquidctl
- liquidtux (liquidctl kernel sibling): https://github.com/liquidctl/liquidtux
- CoolerControl: https://gitlab.com/coolercontrol/coolercontrol (mirror at https://github.com/codifryed/coolercontrol)
- LenovoLegionLinux: https://github.com/johnfanv2/LenovoLegionLinux
- fan2go: https://github.com/markusressel/fan2go
- fan2go-tui: https://github.com/markusressel/fan2go-tui (license confirms AGPL)
- thinkfan: https://github.com/vmatare/thinkfan
- fw-ectool: https://github.com/FrameworkComputer/EmbeddedController + https://github.com/DHowett/fw-ectool
- i8kutils: https://github.com/vitorafsr/i8kutils
- msi-ec: https://github.com/BeardOverflow/msi-ec
- MControlCenter: https://github.com/dmitry-s93/MControlCenter
- mbpfan: https://github.com/dgraziotin/mbpfan

Documentation:
- NBFC-Linux config schema: https://github.com/nbfc-linux/nbfc-linux/blob/main/doc/nbfc_service.json.5.md
- NBFC config-creation guide: https://nbfc-linux.github.io/creating-config/
- LHM SuperIO source: https://github.com/LibreHardwareMonitor/LibreHardwareMonitor/blob/master/LibreHardwareMonitorLib/Hardware/Motherboard/SuperIOHardware.cs
- CoolerControl hardware support: https://docs.coolercontrol.org/hardware-support.html
- liquidctl device list: see README of repo above
- thinkfan YAML config: https://github.com/vmatare/thinkfan/tree/master/examples
- LenovoLegionLinux kernel module source: https://github.com/johnfanv2/LenovoLegionLinux/blob/main/kernel_module/legion-laptop.c
- ArchWiki Fan speed control: https://wiki.archlinux.org/title/Fan_speed_control
- Howett's fw-ectool deep-dive: https://www.howett.net/posts/2021-12-framework-ec/
- Linux platform-driver thread on LenovoLegionLinux: https://www.spinics.net/lists/platform-driver-x86/msg36218.html

---

**End of survey.** spec-09 has its scoping. spec-03 PR 2c diag bundle has its detection list. Schema additions identified for v1.1 if Phoenix wants them now.
