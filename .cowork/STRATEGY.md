# ventd — strategic positioning document

**Author:** Cowork (orchestrator analysis, S5)
**Date:** 2026-04-18
**Purpose:** Identify the concrete technical wedges where ventd can achieve clear dominance versus every existing fan-control tool, and rank them by leverage-per-engineering-hour. This is the strategic companion to `ventdmasterplan.mkd` (which says *what* to build) and `ventdtestmasterplan.mkd` (which says *how to prove it works*). Neither of those plans argues *why* a specific feature wins the category.

---

## 0 · Executive summary

Ventd competes in a fragmented market. Four broad segments:

1. **Pre-historic Unix daemons** — `fancontrol`, `thinkfan`, `i8kutils`. Script-based, single-author, zero dev, hard-to-install. Not really competitors; they're what people replace.
2. **Modern Linux daemon** — `CoolerControl` is the dominant player; `fan2go` is a distant second. Both open-source, both have real users.
3. **Windows desktop apps** — `FanControl.Releases` (14k stars) is the category winner; `Argus Monitor` is a paid niche; vendor tools (ASUS AI Suite, Corsair iCUE) are vendor-locked junk.
4. **Commercial sensors+fans** — paid, cross-platform, enterprise (`Argus`, `HWiNFO+FC`). Tiny audience.

Ventd's current stated position (README §How it compares) claims wins on: zero-config first-boot, browser-only setup, automatic calibration, single static binary, runtime NVML dlopen, hardware change detection. This is correct for the **Linux daemon** category (vs CoolerControl + fan2go).

**The strategic question the masterplan answers is: does ventd stay in the Linux daemon category, or does it expand to own the whole fan-control market?**

The masterplan's Phases 6–10 say: yes, expand. Cross-platform (Windows, macOS, BSD, illumos, Android), all fan types (IPMI, USB AIOs, laptop EC, ARM SBC PWM), acoustic intelligence, UEFI pre-OS control. If all of Phases 1–10 land, ventd is not in a fan-control category; it IS the fan-control category.

This document lists the wedges that make that claim credible — and which ones are cheap leverage versus expensive-and-risky.

---

## 1 · Market reality check

### 1.1 CoolerControl's strengths and weaknesses

**Strengths:**
- Mature GUI (Qt + WebUI). Users know it. Reddit threads recommend it. ~5 years of development.
- Curated AIO device support via `liquidctl` — every NZXT Kraken, Corsair Commander, Aqua Computer device has a Python module in upstream liquidctl.
- Profile presets for popular boards.
- RGB support where the device driver exposes it.
- Packaged for Ubuntu/Debian/Fedora/Arch via its own Cloudsmith repo. `sudo apt install coolercontrol` works.
- Active Discord community for troubleshooting.

**Weaknesses (confirmed from their own docs + GitHub issues):**
- **Multi-process, Python dependency.** `coolercontrold` (Rust daemon) + `liqctld` (Python daemon wrapping liquidctl, talks over Unix socket) + GUI (Qt). Python is a runtime dep; Alpine and minimal images are hostile to it. Their install script pulls a huge dep tree.
- **No auto-calibration.** Users manually set fan curves. Pump curves too. Per-device.
- **No hardware change detection.** Plug a new fan, restart the daemon. (Issue: users complain about this.)
- **Setup is terminal-first.** Install → open GUI → configure per-device. Not the zero-config flow.
- **BIOS fights it.** Many Dell/HP laptops have BIOS-level fan control that competes with CoolerControl. Their docs acknowledge this as an unresolved issue.
- **No Windows/macOS.** Linux-only.
- **Writing `pwmN_enable` semantics are inconsistent across drivers.** They accept "some boards just don't work" as a fact.
- **Their FAQ explicitly says:** if a hwmon device lacks writable `pwmN_enable`, fan control fails. They don't attempt the ventd-style fallback.

### 1.2 fan2go's strengths and weaknesses

**Strengths:**
- Single static Go binary. No Python.
- YAML config. Readable, scriptable, GitOps-friendly.
- Simple model: fans, sensors, curves.
- Low dep footprint.

**Weaknesses:**
- No GUI at all. fan2go-tui exists but only maintained by the same author, and is text-mode.
- No auto-calibration. Users edit YAML.
- No hardware change detection.
- `pwmMap: autodetect` is primitive — breaks on non-linear PWM curves, multi-fan-per-pwm boards.
- No first-boot wizard.
- No safety restore path documented as comprehensive as ventd's.
- Single maintainer, sparse release cadence (releases from 2024).
- 262 stars, 25 forks — small user base.

### 1.3 FanControl.Releases (Windows)

**Strengths:**
- Outstanding polish. Drag-graph fan curves. Sensor-rich. Multi-config profiles. 14k stars.
- Best-in-class Windows fan control. Users rave.

**Weaknesses:**
- **Closed source, Windows only.**
- Depends on `WinRing0`/`LibreHardwareMonitor` kernel driver for hardware access. Windows Defender started flagging WinRing0 as malware in **March 2025**. This is an existential issue for the project — Microsoft is actively hostile to the driver model it depends on. Rem0o (the author) has no clean path forward.
- Closed source means no Linux port possible.

This is a **huge strategic opening**. The de-facto Windows winner is about to be cut off at the knees by its own platform, and there's no open-source replacement ready. Ventd's cross-platform architecture (Phase 6-WIN) could fill this vacuum if executed in the next 12 months.

### 1.4 Argus Monitor

Paid commercial. ~$20-40 lifetime license. Cross-platform (Windows, some Linux). Small user base, not viral. Not a meaningful competitive threat.

### 1.5 Vendor utilities (ASUS AI Suite, Corsair iCUE, NZXT CAM, etc.)

Vendor-locked, bloated, usually only work with that vendor's hardware. Users hate them. iCUE is 1GB+. They're what users install reluctantly, not happily. Ventd's cross-vendor approach (IPMI + liquid + hwmon + NVML + Super I/O all in one daemon) is a wedge against every single vendor utility.

---

## 2 · The wedges, ranked

Each wedge is one thing ventd can do that the market either can't or won't. I rank by **leverage** (how much market it unlocks) × **cost** (engineering effort) = priority.

### Wedge 1: Zero-config first boot — already shipping, defend and extend

**Position:** Already ventd's headline differentiator. README shows a screenshot. Nobody else has it.

**Leverage:** Massive. The 80% of users who want "it just works" reject CoolerControl the first time they have to edit YAML or click through 15 device panels. Zero-config is the acquisition flywheel.

**Cost:** Paid already. Phase 5 (profile-sourced first boot from `hardware-profiles` repo) extends this: when enough users calibrate their machines, **every first boot becomes zero-click**. The profile DB grows organically.

**Action:** Phase 5 is the highest-ROI remaining work. Profile capture + remote refresh (P1-FP-02 in flight now) + the curated `ventd/hardware-profiles` repo is the flywheel. Every calibration completed anywhere benefits the next person's first boot. CoolerControl cannot match this because their setup is terminal-first; users never reach a "we learned what you have" moment.

**Risk:** Profile-capture PII leakage. P5-PROF-02 (anonymisation guards) is critical — one data-exposure incident ends the feature. The 100-sample fuzz corpus in P5-PROF-02 is non-negotiable.

### Wedge 2: Windows — exploit FanControl's WinRing0 cliff

**Position:** Ventd doesn't run on Windows yet. But FanControl.Releases has 14k users who are about to lose their daemon.

**Leverage:** Entire Windows fan-control market is up for grabs. 14k+ users with an unmet need and no open-source alternative. Ventd as a cross-platform single binary with the zero-config flow is immediately the best option — **if ventd can read Windows temps and write Windows fans without WinRing0.**

**Cost:** P6-WIN-01 is the Phase 6 task. Masterplan specifies: `root\WMI MSAcpi_ThermalZoneTemperature` for temps, MSI.CentralServer COM or documented ACPI methods for fans, **no WinRing0**. This is the right architecture but it's the hardest Phase 6 task — ACPI methods vary per board vendor, and MSI.CentralServer is poorly documented.

**Action:** Prioritise P6-WIN-01 *immediately* after Phase 1-2 close. Publish a blog post (ventd.org/blog/wanna-get-off-winring0) that names the problem, offers the solution, and pulls the FanControl user base. Timing matters — Defender flagging intensity will eventually hit a tipping point where the entire FanControl user base searches for alternatives in a single week.

**Risk:** ACPI-without-WinRing0 might not cover enough boards. Mitigation: the Super I/O chip (ITE, Nuvoton) is the target for 90% of desktops; ACPI covers most laptops. Document explicitly which boards are supported, let community report gaps.

### Wedge 3: macOS — Apple Silicon is a new market

**Position:** Nobody else covers macOS fan control seriously. `smcFanControl` exists but is Intel Mac only and abandoned. There's no Apple Silicon fan controller.

**Leverage:** Small user base in raw numbers, but **M1/M2/M3 MacBook Pro users who run under load (video edit, ML)** are a vocal, high-income segment. Ventd is the only serious cross-platform daemon positioned to enter this space.

**Cost:** P6-MAC-01 is doable but not trivial. IOKit via purego dlopen, SMC keys. Apple Silicon adds Asahi-class wrinkles (P2-ASAHI-01).

**Action:** Ship Intel Mac first (simpler SMC); Apple Silicon as follow-up. Notarisation (P6-MAC-02) is required — users on Gatekeeper won't launch unsigned binaries.

**Risk:** Apple's T2/SEP gatekeeping may prevent SMC writes even with root. Test on real hardware before committing to the feature.

### Wedge 4: IPMI — enterprise fleet control

**Position:** Server admins currently use `ipmitool` scripts in cron, or vendor tools (Dell OMSA, HPE iLO). No unified, daemon-based approach. CoolerControl doesn't do IPMI.

**Leverage:** Every Supermicro/Dell/HPE/Lenovo server box in the world is a potential ventd target. Homelab (r/homelab, 2M subscribers) is a loud, blogging, influential segment. Enterprise is a massive latent market.

**Cost:** P2-IPMI-01 and P2-IPMI-02 are specified clearly. Socket-activated capability-scoped sidecar (IPMI-01 + IPMI-02) is the right architecture. Cost: 2-3 weeks. Risk: BMC vendor-specific commands are fragile.

**Action:** Land IPMI mid-Phase-2. Target: reddit/r/homelab post "single-binary IPMI fan control, works on Supermicro/Dell/HPE out of the box" — bigger community than /r/linux_gaming. Fleet view (P8-FLEET-01, P8-FLEET-02) turns a single-server utility into a multi-server dashboard — that's the enterprise angle.

**Risk:** BMC firmware bugs. Supermicro X9/X10 have known bugs where fan control writes get silently ignored above certain RPM setpoints. Document + warn; don't promise.

### Wedge 5: AIO pumps + USB fans — attack liquidctl dependence

**Position:** CoolerControl depends on `liquidctl` (Python library). That's the weakest link in their architecture — Python runtime, subprocess communication, and liquidctl authors being separate maintainers.

**Leverage:** AIOs are the fast-growing segment of PC cooling. Every new build over $1500 has an AIO. AIO users are willing to pay/tinker.

**Cost:** P2-LIQUID-01 and P2-LIQUID-02 cover Corsair Commander Core, NZXT Kraken X3/Z3, Lian Li UNI Hub, Aqua Computer Quadro/Octo, EK Loop Connect, Gigabyte AORUS. Implementing these in pure Go (via `github.com/sstallion/go-hid`) instead of piggy-backing on liquidctl removes a whole process + runtime.

**Action:** Phase 2 task as planned. The value proposition: "CoolerControl runs three processes to do AIO; ventd does it in one static binary." Write it that way.

**Risk:** Reverse-engineering effort for each device. The masterplan lists 7 device families; half those might need USB captures that require physical hardware.

### Wedge 6: Laptop EC — Framework/ThinkPad/Dell

**Position:** `thinkfan` covers ThinkPads. `dell-smm-hwmon` exposes Dell laptops. Framework has custom tools. Nothing unified. CoolerControl explicitly lists these as poorly supported.

**Leverage:** Linux laptop users are vocal (Framework community particularly so). Framework is 100k+ users now.

**Cost:** P2-CROSEC-01 (Framework + Chromebook EC via `/dev/cros_ec`) is well-specified. P2-CROSEC-02 (ThinkPad, Dell, HP via union backend) layers on top.

**Action:** Land P2-CROSEC-01 first — Framework is a community that will write blog posts about ventd. ThinkPad second (largest laptop cohort).

**Risk:** EC lockout on some vendors (Dell especially). Some BIOS updates break EC access. Document + warn.

### Wedge 7: Acoustic intelligence — a new category

**Position:** Nobody does this. Zero competitors. It's Phase 7 territory per the masterplan.

**Leverage:** This is the "wow" feature that gets reviews. "ventd detects your fan is about to fail from its sound" makes HackerNews front page. Not essential to daily use but transforms the brand.

**Cost:** P7-ACOUSTIC-01 and P7-ACOUSTIC-02 require: microphone access, FFT library (`github.com/mjibson/go-dsp/fft`), baseline capture during calibration, anomaly detection via sub-harmonic at blade-pass/2. This is research-grade signal processing — 4-6 weeks of work, and half that time is tuning threshold heuristics.

**Action:** Phase 7 as planned. Announce publicly only after a solid 90-day beta on real hardware — false-positive "your fan is failing" alerts would destroy user trust.

**Risk:** False positives from ambient noise (other fans, HVAC, music). Mitigation: baseline during calibration includes current ambient; anomaly detection is relative to baseline, not absolute.

### Wedge 8: MPC learning controller — quieter fans

**Position:** Every other tool uses fan curves — temperature → PWM. MPC (P4-MPC-01) uses a learned thermal model + receding-horizon optimization. **Genuinely quieter fans for the same thermal envelope.**

**Leverage:** Measurable noise reduction. Users will notice. Reviewers will benchmark.

**Cost:** P4-MPC-01 is hard — online RLS MISO ARX per fan, 30-min window, 10-var QP solver, PI fallback. 2-3 weeks of serious engineering, and it won't be production-grade for another 2-3 months of tuning.

**Action:** P4-PI-01 (simple PI with autotune) first — much lower cost, 80% of the benefit. Ship MPC as an opt-in `control.mode: mpc` flag. Let it mature before making it default.

**Risk:** MPC instability on degenerate models → fan oscillation. The PI fallback-on-residual is non-negotiable; test it relentlessly.

### Wedge 9: UEFI pre-OS fan control — pre-emptive quietness

**Position:** P9-UEFI-01 is a wild card. Signed UEFI DXE stub pre-programs Super I/O at POST, before the OS loads. Result: machine is quiet from the moment you press power, not after the 30 seconds it takes to boot and load the daemon.

**Leverage:** Hard to quantify. Probably a niche feature that techies appreciate. BUT: zero competition. Nobody has this.

**Cost:** High. Zig no_std or EDK2, user-key signing, SHIM compatibility. 4-8 weeks and real risk of bricking test machines.

**Action:** Defer until Phase 9. Only pursue if Phase 6-8 go smoothly and there's appetite for polish.

**Risk:** Bricking user systems is catastrophic. Gate behind "enabled by explicit user action during install, never by default." Document firmware recovery procedures.

### Wedge 10: Single static binary

**Position:** Already true for ventd. fan2go has this too. CoolerControl doesn't. Every vendor tool doesn't.

**Leverage:** Alpine. NixOS. Minimal Docker images. Homelab cattle-not-pets deployments. SBC Armbian images. ~30% of Linux infrastructure appreciates this.

**Cost:** Paid. CGO_ENABLED=0 constraint maintains it.

**Action:** Guard obsessively. The moment someone adds a cgo dep, this wedge is gone forever. Code reviews must reject cgo unless it's purego-loaded (NVML) or strictly sidecarred.

**Risk:** Feature pressure — someone will propose `ventd-gui` that links Qt. Say no. The web UI model is sufficient.

---

## 3 · The wedges NOT worth pursuing

Don't chase these. Listed so they stay off the masterplan:

- **RGB control.** OpenRGB exists, mature, has huge community. Ventd should read RGB device state for sensing (they often expose temps/fans) but not compete with OpenRGB for effects. Integration story: "ventd and OpenRGB coexist, share device state via /run/ventd/shared."

- **Disk SMART monitoring.** `smartd` exists, mature, ubiquitous. Don't duplicate.

- **CPU frequency scaling.** `tuned`, `cpupower`, `auto-cpufreq` exist. Ventd publishes thermal headroom (P7-COORD-01); other tools read it. Stay in our lane.

- **Benchmarking/stress-testing.** `stress-ng`, `s-tui` exist. Ventd integrates with them (read load) but doesn't reimplement.

- **GUI as a desktop app.** The web UI covers every use case the desktop GUI does. Adding Qt/GTK pulls in 200MB of deps and five linkage modes. No.

- **Mobile apps.** The web UI is mobile-responsive. Native iOS/Android is a support nightmare for marginal gain.

- **Overclocking.** Dangerous, out of scope, political minefield.

---

## 4 · Strategic sequencing

### Phase P1 (now) — establish the HAL foundation

Without HAL, every backend is a special case. With HAL, every new backend is a 1-week task, not a 1-month task. The HAL contract (T-HAL-01) is the single most important structural commitment in the whole codebase. Protect it ruthlessly.

### Phase P2 — ship 3 backends that open 3 user segments

- **IPMI** → enterprise / homelab.
- **Liquid** → AIO / desktop enthusiasts.
- **CROSEC (Framework first, then ThinkPad)** → Linux laptop users.

All three can land in parallel once HAL is stable. Each one unlocks a blog post + reddit thread + user acquisition.

### Phase P3 — install is the make-or-break moment

Anyone who fails the install is gone forever. `P3-MODPROBE-01`, `P3-UDEV-01`, `P3-INSTALL-01/02` must be solid. Detect and cleanly coexist with `fancontrol`, `thinkfan`, `CoolerControl`. Never just overwrite other people's configs.

### Phase P4-P5 — control quality + self-calibration

Users feel this even if they don't know why. "My fans are quieter with ventd than with the BIOS curve" is the single most powerful testimonial.

### Phase P6 — **now we're cross-platform**

Windows first (exploit WinRing0 cliff). Mac second (Apple Silicon is greenfield). BSD/illumos last (small market but zero-effort once Windows/Mac are done since they share the HAL).

### Phase P7-P9 — category-defining features

MPC, acoustic, UEFI, learning. These are the wow-factor wedges that move ventd from "the Linux CoolerControl replacement" to "the one fan controller."

### Phase P10 — supply chain

SBOM, cosign, reproducible builds, SLSA. Once ventd is deployed on 100k machines, security posture matters. Get this right before users care.

---

## 5 · Competitive scenario analysis

### Scenario A: CoolerControl stays static (base case)

They continue current development. Ventd wins via: zero-config first boot (Phase 5), cross-platform (Phase 6), acoustic/MPC (Phase 7). 12-18 months to clearly own the Linux daemon category.

### Scenario B: CoolerControl adds a first-boot wizard

They can. Copy-protection doesn't exist for UX features. Mitigation: ventd's profile DB becomes the moat. By the time CoolerControl ships a wizard, `ventd/hardware-profiles` has 500+ boards characterised. Their first-boot is generic; ours is personal.

### Scenario C: Someone forks FanControl.Releases and goes open-source

Possible. If Rem0o open-sources it under the Defender pressure, the resulting project inherits 14k users. Ventd's counter: cross-platform — forked FanControl is still Windows-only. Ventd covers Windows + Linux + Mac. Structural advantage holds.

### Scenario D: Microsoft ships fan control in Windows

Unlikely. Microsoft's fan control is BIOS-delegated by design. If they change that, ventd is in the same boat as FanControl but open-source, which is better.

### Scenario E: Big vendor (Corsair, NZXT) open-sources their tool

Corsair iCUE is 1GB of .NET. They won't. NZXT CAM same. Not a realistic scenario.

---

## 6 · The one metric to watch

**Board coverage in `ventd/hardware-profiles`.** This is the flywheel. Every calibration is a new profile. Every profile makes the next user's first-boot zero-click. The target is 500 unique DMI fingerprints in 18 months. At that point, the "ventd knows your machine" effect is unmatched by any competitor.

Corollary: anything that slows calibration down or makes profile-capture unreliable is a priority-zero bug. Phase 5 is the heart of the strategy even if it looks like support infrastructure.

---

## 7 · What I'd tell a new contributor

> "ventd is not a fan controller with a web UI. It's a hardware-abstraction platform for thermal control, and the web UI is the first consumer of that platform. Every design decision flows from: does this make the HAL more universal, or less? Does this make the profile DB more valuable, or less? If you can't answer both with 'more', reconsider."

---

## 8 · Revisit cadence

This document is reviewed: at every phase boundary, and on any competitive-landscape shift (e.g. Rem0o open-sources FanControl, CoolerControl ships a wizard). Owner: Cowork. Stale entries are a worse signal than no document — if this file is more than 3 months out of date, escalate for a rewrite.
