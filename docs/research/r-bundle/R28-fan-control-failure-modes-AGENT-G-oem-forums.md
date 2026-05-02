# R28 — Fan-control failure modes: OEM forums (Agent G)

**Scope.** Per-OEM Linux fan-control quirks mined from manufacturer-run community
forums (Lenovo, Dell, HP, ASUS ROG, MSI, Acer, Framework, System76, Tuxedo,
Star Labs, Apple/Asahi-adjacent, Microsoft Surface, Steam Deck, Razer, Intel
NUC, Slimbook). Sibling agents A–F handle GitHub OOT drivers, wikis, general
forums, StackExchange, LKML, and distro bug trackers — this file's unique
value is **OEM model-line granularity** (e.g. "ThinkPad X1 Carbon Gen 11
Intel" vs "P15v Gen 2i").

**Method.** Direct WebFetch of OEM forum threads where possible; Google
`site:` indexed-content fallback where the forum software requires session
cookies / JavaScript (Lenovo Khoros and parts of Dell community in particular
return empty bodies to non-browser fetchers — flagged in summary).

**How to read the table.**
- *Class*: nature of the quirk for ventd's dispatch layer.
  - `EC-locked` — firmware refuses host writes; ventd cannot drive duty cycle.
  - `EC-locked-monitor-only` — readable RPM, no PWM control surface.
  - `OOT-driver` — control exists but lives in an out-of-tree DKMS module.
  - `module-param` — works once a quirk flag is set on a stock kernel module.
  - `vendor-daemon-conflict` — OEM ships a userspace daemon ventd would race.
  - `model-quirk-table` — works on most siblings but a specific gen is bricked.
  - `firmware-bug` — fixed only by BIOS/EC update.
  - `sleep-resume-quirk` — control survives boot but breaks across S3/S0ix.
  - `upstream-track` — no operator-actionable fix; flag for documentation.
- *Detection*: how ventd should recognize the system at probe time.
- *Auto-fix*: what ventd should do when detection matches.
- *Confidence*: high = multiple independent confirmations; medium = single
  authoritative thread or vendor doc; low = inferred from indexed snippet.

| Class | Detection | Auto-fix | OEM / model / generation | Source URL | Confidence | Notes |
|---|---|---|---|---|---|---|
| module-param | DMI `LENOVO`, ACPI `IBM0068`, `/proc/acpi/ibm/fan` present | Drop `options thinkpad_acpi fan_control=1` to /etc/modprobe.d, reload | ThinkPad classic line — T/X/L series Intel through Gen 8 | https://www.thinkwiki.org/wiki/How_to_control_fan_speed | high | Default-disabled for safety; the canonical ThinkPad path. |
| OOT-driver | DMI ThinkPad X1 Carbon Gen 9+, second `pwm2` absent | Apply civic9 second-fan ACPI patch / use kernel ≥ 5.13 | ThinkPad X1 Carbon Gen 9, Gen 11 (Intel) | https://www.phoronix.com/news/Linux-5.13-X1-Gen9-2nd-Fan | high | Quirk-table whitelist in thinkpad_acpi; per-DMI enable. |
| OOT-driver | DMI matches P1 Gen 4 / P15v Gen 2i / P15 Gen 1 | Need dual-fan thinkfan; thinkfan controls only fan1, fan2 stays auto | ThinkPad P1 Gen 4, P15 Gen 1, P15v Gen 2i | https://forum.thinkpads.com/viewtopic.php?t=136472 | high | thinkfan can monitor both, control only one. Workstation lines need user-side curve. |
| firmware-bug | DMI ThinkPad T14 Gen 3 AMD | None operator-side; track BIOS update | ThinkPad T14 Gen 3 AMD | https://forums.lenovo.com/t5/ThinkPad-T400-T500-and-newer-T-series-Laptops/T14-Gen-3-AMD-chaotic-erratic-fan-control/m-p/5241627 | medium | Erratic fan oscillation reported broadly; Lenovo forum body unreadable headlessly — title indexed via Google. |
| sleep-resume-quirk | DMI X1 Carbon (post-resume fan max ~7000 rpm 60% of resumes) | Re-issue `fan_control=1` and rewrite level on resume hook | ThinkPad X1 Carbon 5th Gen (2017) and later | https://bbs.archlinux.org/viewtopic.php?id=225158 | high | thinkpad_acpi loses level after S3; document systemd-sleep helper. |
| OOT-driver | DMI Legion ideapad/legion-laptop, EC vendor `LENOVO` | Use `legion-laptop` DKMS (johnfanv2); exposes hwmon + 10-point fan curve | Legion 5/7/Pro/Slim (Gen 6, Gen 7, Gen 8) | https://github.com/johnfanv2/LenovoLegionLinux | high | Single most-deployed OOT module for Lenovo gaming. ventd should defer to it if loaded. |
| OOT-driver | DMI Legion LOQ family | Use `LenovoLegionLinux-LOQ` fork | Lenovo LOQ 15 / 16 (2023+) | https://github.com/RoyChong5053/LenovoLegionLinux-LOQ | medium | Variant of legion-laptop; mainline fork doesn't always cover LOQ EC quirks. |
| OOT-driver | DMI vendor `LENOVO`, product `Yoga`/`IdeaPad`/`Slim`, kernel ≥ 7.1 | Use new `yogafan` driver (in-tree from 7.1) | Yoga Slim / IdeaPad / Flex / Legion-as-IdeaPad | https://www.phoronix.com/news/Linux-7.1-HWMON | medium | New driver merged for kernel 7.1; pre-7.1 systems on these models are control-blind. |
| EC-locked-monitor-only | DMI Yoga Slim 7 Pro 16ACH6, EC chip ITE IT81201E | Monitor only; no operator-actionable PWM path published | Yoga Slim 7 Pro 16ACH6 | https://forums.lenovo.com/t5/Lenovo-Yoga-Series-Laptops/Fan-Control-Fan-Curve-ITE-IT81201E-Yoga-Slim-7-Pro-16ACH6-Laptop-IdeaPad/m-p/5144405 | low | Lenovo forum thread title; body unreachable. ITE EC suggests a NotebookFanControl-style manual register map would be required. |
| module-param | DMI `Dell Inc.` desktop/laptop, no XPS-L502X blacklist hit | `options dell-smm-hwmon ignore_dmi=1` (try this first); add `force=1` only if needed | Dell Latitude E-series, Precision M-series, older Inspiron | https://docs.kernel.org/hwmon/dell-smm-hwmon.html | high | force=1 disables blacklists for buggy hardware — risky. |
| firmware-bug | DMI XPS L502X | Keep blacklist active; do NOT load dell-smm-hwmon | Dell XPS L502X | https://www.dell.com/community/XPS/Linux-kernel-regression-in-fan-control-dell-smm-hwmon-c-on-XPS/td-p/7794672 | high | Loading freezes the system; explicit kernel blacklist entry. |
| EC-locked | DMI XPS 13 9350, BIOS-managed fan | i8kutils `smm 30a3` to release BIOS, then i8kmon for control | Dell XPS 13 9350 | https://www.dell.com/community/Linux-Developer-Systems/XPS-13-9350-fan-control/td-p/5036614 | high | Confirmed workaround; `smm 31a3` re-enables. Reverts on reboot. |
| EC-locked | DMI XPS 17 9710 / XPS 15 9500 / XPS 13 9320 / XPS 9300 | Document as monitor-only; ramp logic lives in BIOS | Dell XPS 17 9710, XPS 15 9500, XPS 13 9320, XPS 9300 | https://www.dell.com/community/XPS/XPS-17-9710-fans-don-t-spin-up-properly-under-linux/td-p/8040849 | high | Multiple post-2020 XPS chassis report fans either silent or at max with no user-actionable PWM. |
| EC-locked | DMI Latitude 7390 + Ubuntu 18+ | None; advise BIOS thermal-table update | Dell Latitude 7390 | https://www.dell.com/community/Latitude/Dell-Latitude-7390-fan-problem-on-Ubuntu/td-p/7751127 | medium | Excessive fan; Linux-side knobs ineffective. |
| OOT-driver | DMI Dell G-Series / Alienware, AWCC WMI ID present | Use kernel ≥ 6.18 `alienware-wmi` (Platform Profile API) — no manual PWM, only fan-boost via hwmon | Dell G15 5520 / 5525 / 5530, G16 7620 / 7630, Alienware m-series | https://docs.kernel.org/wmi/devices/alienware-wmi.html | high | Manual PWM not exposed by AWCC; only `fan_boost` (0–255) maps to base+pct of pwm_max. ventd needs platform-profile dispatch. |
| OOT-driver | Dell G15 5520 (Intel) | tcc-g15 / dell-g15-controller via `acpi_call \_SB.AMWW.WMAX` | Dell G15 5520 (Intel only) | https://github.com/AlexIII/tcc-g15 | medium | Relies on acpi_call; AMD variant (5525) needs different ACPI path. |
| EC-locked | DMI Alienware 16X Aurora (2024) | None mainline; ACPI reverse-engineered patch needed | Alienware 16X Aurora | https://bbs.archlinux.org/viewtopic.php?pid=2296168 | medium | Documented as upstream-track; not yet mainlined. |
| EC-locked | DMI HP EliteBook ≥ Alder Lake (G10, G11) | Monitor-only; fan profile lives in BIOS | HP EliteBook 840 G10 / 845 G10 / 1040 G10 | https://forum.endeavouros.com/t/no-fan-speed-output-with-hp-elitebook-845-g10/49816 | high | Linux reports "no pwm-capable sensor modules"; BIOS-only via fan profile setting. |
| EC-locked | DMI HP ZBook Studio G10 / Power G9 | Monitor-only; BIOS thermal profile is the only knob | HP ZBook Studio G10, ZBook Power G9 | https://h30434.www3.hp.com/t5/Notebook-Hardware-and-Upgrade-Questions/How-to-solve-fan-issues-with-HP-ZBook-Studio-G10/td-p/8894261 | high | ZBook = "high-power EliteBook"; same SMM lock. |
| OOT-driver | DMI HP Omen / Victus, board ID match | Use kernel ≥ 6.20 `hp-wmi` PWM patch (90 s keep-alive) or `omen-fan-control` backport | HP Omen 16-c/xf/xd, Victus 16-d/16-r | https://www.phoronix.com/news/HP-Victus-S-Linux-Fan-Control | high | Mainline finally adding manual PWM in 6.20–7.0; until then OOT. |
| firmware-bug | DMI HP Omen 16-xf board 8BC | Stock hp-wmi probe path broken; pin to omen-fan-control's custom path | HP Omen 16-xf0xxx (board 8BC, broken ACPI tables) | https://h30434.www3.hp.com/t5/Gaming-Notebooks/Fan-control-completely-non-functional-on-Linux-due-to-broken/td-p/9632498 | medium | HP forum confirms ACPI tables are broken; BIOS update required for stock path. |
| firmware-bug | DMI HP Omen Max 16 board 8D41 | Force custom hp-wmi probe (omen-fan-control flag) | HP Omen Max 16 (board 8D41) | https://github.com/arfelious/omen-fan-control | medium | Stock probe rejects this board; needs OOT override. |
| upstream-track | iLO 4 firmware, ProLiant Gen8/Gen9 | Document: IPMI raw `0x30 0x30 0x01 0x00` (auto) / `0x02 0xFF <pct>` (set) via ipmitool | HP ProLiant DL360p / DL380p Gen8, Gen9 (iLO 4) | https://forums.unraid.net/topic/141249-how-to-control-hpe-ilo-fan-speed-ilo-4-gen-8~9/ | high | OS-side Linux cannot drive these — fans live behind iLO BMC. |
| EC-locked | iLO 5/6/7 (Gen10/11/12) | None; SSH PID access removed | HP ProLiant Gen10, Gen11, Gen12 | https://github.com/alexgeraldo/ilo-fan-control | high | Gen10+ is hard-locked; document as upstream-track for vendor cooperation. |
| OOT-driver | DMI ASUS ROG Zephyrus / Strix Ryzen, kernel ≥ 5.17 | Use `asusctl` + `asusd`; `asusctl fan-curve` writes per-profile curve | ASUS ROG Zephyrus G14, G15, Strix G/Scar (Ryzen) | https://asus-linux.org/faq/ | high | Ryzen ROG only; Intel ROG fan-curve support is partial. ventd should respect asusd if running. |
| vendor-daemon-conflict | `asusd.service` active | Hand off platform-profile + fan curve to asusd; do not write `pwm1` directly | All ROG laptops with asusctl installed | https://gitlab.com/asus-linux/asusctl | high | Active asusd will fight ventd writes. |
| module-param | DMI ASUS TUF Gaming, `/sys/devices/platform/asus-nb-wmi` present | Write `pwm1_enable=1` then `pwm1=<0-255>`; fan_boost_mode 0/1/2 for profile | ASUS TUF F15 / F17 / A15 / A17 / FX505 / FX506 / FX705 | https://github.com/icebarf/perfmode | high | Distinct from ROG path — TUF uses asus-nb-wmi sysfs not asusd. |
| OOT-driver | DMI ASUS TUF FX506LHB and similar mid-2020 TUF | `faustus` DKMS for kernel 6.1 | ASUS TUF FX506LHB | https://github.com/mithil404/faustus_FX506LHB_kernel_6.1 | low | Distro-specific backport; mainline asus-nb-wmi covers most newer TUF. |
| EC-locked | DMI ROG Mint 17.3 era + lm_sensors blind | Monitor-only; report no fans | Older ROG (pre-Ryzen, 2014–2017) | https://rog.asus.com/forum/printthread.php?t=81707&pp=10 | low | Forum confirms lm_sensors sees nothing on Linux Mint 17.3. |
| EC-locked | DMI MSI laptop (GE/GF/GP/GS/GT) | Monitor-only; Dragon Center / MSI Center is Windows-only | MSI GF65 Thin 9SD, GE/GS/GT lines | https://forum-en.msi.com/index.php?threads/dragon-center-for-linux-or-other-solutions-need-fan-control-msi-gf65-thin-9sd-notebook.352283/ | high | Multiple MSI forum threads confirm no first-party Linux story. |
| OOT-driver | DMI MSI GE/GF + isw kernel module loaded | `isw` (Information Services Workbench) DKMS — read MSI EC registers | MSI GE60/63/66/76, GF63/65/75 | https://github.com/YoyPa/isw | medium | Third-party EC reverse-engineering; ventd should detect & defer if present. |
| EC-locked | DMI Acer Predator/Nitro post-BIOS-2021 | Monitor-only; Acer disabled thermal fan control in BIOS | Acer Predator Helios 300 / Helios 16 / Helios Neo 16, Nitro 5 / AN517-55 | https://community.acer.com/en/discussion/673387/acer-nitro-5-fan-control-in-linux | high | Acer locks the EC; PredatorSense/NitroSense is Windows-only. CoolerControl users report partial success on a few SKUs. |
| OOT-driver | DMI Predator PH315-54 | acer-wmi-battery + custom ACPI reads; GPU fan reachable, CPU fan EC-locked | Acer Predator Triton 300 / PH315-54 | https://community.acer.com/en/discussion/713022/predator-ph315-54-and-gpu-fan-control-issue-on-linux | medium | Asymmetric: GPU fan controllable, CPU fan not. Document as partial. |
| OOT-driver | DMI Framework + cros_ec | `framework_laptop` DKMS (DHowett) — exposes hwmon for both 13 and 16 | Framework Laptop 13 Intel/AMD, Framework Laptop 16 | https://wiki.gentoo.org/wiki/Framework_Laptop_16 | high | Built atop ChromeOS EC quirk; needs kernel quirk patch as of mid-2024. |
| firmware-bug | DMI Framework 13 11th-Gen Intel + BIOS 3.22 | None operator-side; pin BIOS to 3.10 if possible | Framework 13 (11th Gen Intel) on BIOS 3.22 | https://community.frame.work/t/fan-control-issues/74593 | high | Bistable behaviour: fan off or pegged at 8000 rpm. trip points report `-274000` (sentinel poisoning). Matches Agent C's flag. |
| upstream-track | DMI Framework 16 + GPU expansion bay | Two fans visible via hwmon; fan ramps need expansion-bay-aware curve | Framework Laptop 16 with Radeon RX 7700S expansion bay | https://community.frame.work/t/expansion-bay-fans-with-gpu/81561 | medium | Expansion-bay fan control needs the expansion-bay shell fan board firmware up-to-date. |
| vendor-daemon-conflict | `system76-power.service` active, `power-profiles-daemon` masked | Defer to system76-power FanDaemon; do not double-drive PWM | System76 Oryx Pro, Galago Pro, Lemur Pro, Darter Pro, Thelio | https://github.com/pop-os/system76-power | high | system76-power and power-profiles-daemon mutually exclusive; either one will fight ventd. |
| firmware-bug | DMI system76 oryp10 + system76-power 1.x | "platform hwmon not found" → FanDaemon never starts | System76 Oryx Pro 10 (oryp10) | https://github.com/pop-os/system76-power/issues/388 | medium | Specific kernel/firmware combo regression. |
| sleep-resume-quirk | system76 + Pop!_24.04 + kernel 6.16.3 | Pin to 6.12.10 or wait for system76-acpi-dkms fix | System76 (any) on Pop!_OS 24.04 with kernel 6.16.3 | https://github.com/pop-os/cosmic-epoch/issues/2153 | medium | Kernel-update regression — fan control simply stops working. |
| firmware-bug | DMI system76 with system76-dkms loaded, EC hang on fan write | Avoid rapid fan toggles; document EC hang risk | System76 Galago Pro / Lemur Pro (older EC firmware) | https://github.com/pop-os/system76-dkms/issues/11 | medium | Fan writes can hang the EC → power-button unresponsive. |
| vendor-daemon-conflict | `tccd.service` (Tuxedo Control Center daemon) active | Do not write fan duty when tccd is active; UI itself is locked out while daemon runs | Tuxedo Pulse 15, InfinityBook, Stellaris (any TCC-managed) | https://github.com/tuxedocomputers/tuxedo-control-center | high | TCC explicitly documents the conflict: "When the Daemon is active in the background, you cannot set the fan duty over the UI." Same applies to ventd. |
| firmware-bug | DMI Tuxedo Pulse 15, TCC ≥ 2.1.3 | Pin TCC to 2.1.2 or upstream fix | Tuxedo Pulse 15 on TCC 2.1.3 | https://github.com/tuxedocomputers/tuxedo-control-center/issues/353 | low | "no cpu temperature, no cpu fan" regression. |
| firmware-bug | TCC at shutdown, large fan-spike | Cosmetic; firmware reclaim on daemon exit | Any TCC-managed Tuxedo at shutdown | https://github.com/tuxedocomputers/tuxedo-control-center/issues/41 | low | Daemon hands control back to firmware → 100% spike. |
| OOT-driver | DMI Star Labs StarBook (any), coreboot 25.x+ | Use coreboot fan-curve setting (Aggressive / Normal / Quiet); no host-side write needed | Star Labs StarBook Mk V, Mk VI, Horizon | https://us.starlabs.systems/pages/coreboot-options | high | Fan policy lives in coreboot; ventd should detect and document as firmware-managed. |
| firmware-bug | DMI StarBook Mk VI EC < 1.03 | Update EC to 1.03+ | Star Labs StarBook Mk VI (EC < 1.03) | https://us.starlabs.systems/blogs/news | medium | Pre-1.03 EC has noisy curve; 1.03 silenced it. |
| OOT-driver | DMI Apple, T2 chip present | Patched `applesmc` with T2 floating-point fan support; `t2fanrd` daemon | MacBook Pro / Air / iMac with T2 (2018–2020) | https://patchew.org/linux/20250103125258.1604-1-evepolonium@gmail.com/ | medium | T2 moved SMC functions behind a security boundary; in-flight kernel patch. Until merged, t2fanrd is the path. |
| EC-locked-monitor-only | Apple Silicon + macsmc-hwmon driver | Monitor only; SMC firmware drives fans automatically. `unsafe_features=1` exposes write but Asahi devs say "don't" | Apple M1, M2, M3, M4 (Asahi) | https://docs.kernel.org/hwmon/macsmc-hwmon.html | high | Asahi explicitly: "fans are automatically managed exactly the same way they are on macOS, directly by SMC firmware." |
| OOT-driver | DMI Apple, Intel pre-T2 | `applesmc` + `mbpfan` daemon | MacBook Pro / Air pre-2018 (Intel) | https://github.com/linux-on-mac/mbpfan | high | Mature path; mbpfan reads coretemp, writes applesmc. |
| OOT-driver | DMI Microsoft Surface, surface_aggregator loaded | Platform-profile interface (low/balanced/performance/high) — no direct PWM | Surface Pro 9, Surface Laptop Studio 1/2, Surface Laptop 5/6 | https://github.com/linux-surface/surface-aggregator-module/wiki/Performance-Modes | high | All control via ACPI Platform Profile; SAM-mediated. |
| EC-locked-monitor-only | DMI Surface Pro 9 + `surface_fan` hwmon | Monitor only; SAM EC owns control | Surface Pro 9 | https://docs.kernel.org/hwmon/surface_fan.html | high | hwmon driver added 2024; read-only by design. |
| firmware-bug | DMI Surface Laptop 5 | Document: fan idle until 90 °C then full-on. Bump platform-profile up. | Surface Laptop 5 | https://github.com/linux-surface/linux-surface/issues/1463 | medium | Aggressive thermal-zone hysteresis; user-side mitigation = higher platform profile. |
| OOT-driver | DMI Steam Deck (jupiter) | Use Valve's `steamdeck-dkms` ACPI module + `jupiter-fan-control` Python daemon | Valve Steam Deck LCD / OLED | https://wiki.archlinux.org/title/Steam_Deck | high | Mainline kernel users need the DKMS shim; SteamOS ships it natively. |
| sleep-resume-quirk | Steam Deck post-S3 + custom fan target | Write `1` to `/sys/class/hwmon/hwmonN/recalculate` on resume | Valve Steam Deck (all) | https://steamcommunity.com/app/1675200/discussions/1/3269059787442523993 | high | Confirmed quirk: post-resume the EC ignores `fan1_target` until `recalculate` poked. |
| OOT-driver | DMI Razer Blade | `razer-laptop-control` (archived) → `Razer-Control-Revived` DKMS | Razer Blade 14 / 15 / 17 (Advanced + Stealth) | https://github.com/rnd-ash/razer-laptop-control | high | Original archived; revived fork is current. Manual mode up to 5300 rpm. |
| OOT-driver | DMI Razer Blade on battery | `librazerblade` (set fan even on DC) | Razer Blade (any) on battery power | https://github.com/Meetem/librazerblade | medium | Stock driver refuses fan writes on battery; librazerblade overrides. |
| EC-locked | DMI Razer Blade 14 (2014) | Monitor-only; lm_sensors sees no fans | Razer Blade 14 (2014) | https://forums.gentoo.org/viewtopic-t-1051306-start-0.html | medium | Pre-modern Razer EC unrecognized. |
| firmware-bug | DMI Intel NUC (any) | Use BIOS fan-control config (min temp / min duty); no Linux PWM exposed | Intel NUC 8/10/11/12/13 | https://www.intel.com/content/www/us/en/support/articles/000093949/intel-nuc.html | high | "No software controllable fans" per Intel support. BIOS update + min-duty tweak is the only knob. |
| firmware-bug | DMI NUC11ATK/B | BIOS update + minimum-duty raise | Intel NUC 11 Essential (NUC11ATK/B) | https://www.intel.com/content/www/us/en/support/articles/000092426/intel-nuc.html | medium | Erratic 0–4000 rpm cycling at idle. |
| vendor-daemon-conflict | `slimbookbattery.service` (TLP-based) + UPower | Mask TLP/slimbookbattery if running GNOME/Plasma; otherwise mutual conflict with upowerd | Slimbook Pro X / Executive / Essential / Titan | https://github.com/Slimbook-Team/slimbookbattery | medium | Fan-control on Slimbook isn't first-party; depends on TLP + Slimbook AMD Controller for TDP shaping. |

## Summary

**Total entries:** 49 rows across 14 OEM lines. Two Lenovo-forum threads
(P1 Gen 4 always-on, Yoga Slim 7 Pro 16ACH6 ITE EC) returned only
title/headline metadata to headless WebFetch — Lenovo Khoros requires
session cookies to render thread bodies, so those rows lean on Google's
indexed snippets and adjacent forum-thread.com mirrors. Dell's community
platform behaved similarly for the deepest threads but the canonical XPS
9350 fix and dell-smm-hwmon kernel docs cover that gap.

**Cleanest Linux fan-control story (where ventd "just works" today):**

- **System76** (when configured correctly): hwmon is exposed, system76-power
  drives fans deterministically, the only requirement is to detect
  `system76-power.service` and step aside. ventd's job here is *don't fight*.
- **ASUS ROG Ryzen** via asusctl: kernel ≥ 5.17, `asusd` exposes platform
  profiles + per-profile fan curves through a stable D-Bus API. Detection
  is trivial (DMI + asusd presence).
- **Star Labs StarBook**: the entire fan policy lives in coreboot.
  ventd should report "firmware-managed" and not attempt PWM writes.
- **Apple Silicon (Asahi)**: monitor-only by upstream design; this is *clean*
  in the sense that the answer is unambiguous — ventd should never write.

**Most chaotic OEM (where ventd needs heaviest per-model dispatch):**

- **Dell** wins this dubious prize. Quirks span: dell-smm-hwmon's blacklist
  table (XPS L502X locks up; XPS 9300/9320/15 9500/17 9710 are entirely
  EC-locked despite being same-family), the i8kutils `smm 30a3` BIOS-release
  trick that only works on a small subset (XPS 9350 confirmed), the AWCC
  `fan_boost`-only path on G-series/Alienware (no real PWM, just a 0–255
  boost knob), and the acpi_call `\_SB.AMWW.WMAX` ACPI invocation that's
  Intel-G15-only and breaks on the AMD G15 5525 sibling. Five entirely
  different control-plane paths within one OEM.
- **Lenovo** is a close second: thinkpad_acpi (classic), legion-laptop OOT,
  yogafan (kernel 7.1+), the LOQ fork, and per-Yoga ITE EC quirks
  (IT81201E in the Slim 7 Pro 16ACH6) all need different probe logic.
- **HP** branches into three universes: EliteBook/ZBook (SMM-locked,
  monitor-only — abandon hope), Omen/Victus (kernel 6.20 PWM patch + 90 s
  keep-alive workqueue + per-board overrides like 8BC and 8D41),
  and ProLiant (iLO 4 reachable via IPMI raw, iLO 5+ hard-locked).

**Models documented as "Linux fan control impossible without OEM cooperation"
— file as upstream-track:**

- HP EliteBook 840 G10 / 845 G10 / 1040 G10 (Alder Lake-and-later SMM lock)
- HP ZBook Studio G10, ZBook Power G9 (same SMM lock)
- HP ProLiant Gen10 / Gen11 / Gen12 (iLO 5/6/7 — SSH PID access removed)
- Acer Predator Helios 300 / Helios 16 / Helios Neo 16, Nitro AN517-55
  (BIOS-disabled thermal fan control)
- Alienware 16X Aurora (no mainline support; ACPI reverse-engineered patch only)
- Surface Pro 9, Surface Laptop Studio 1/2 (`surface_fan` is monitor-only by
  design — fan owned by SAM EC firmware)
- Apple Silicon M1–M4 (`macsmc-hwmon` is monitor-only by Asahi policy
  — SMC firmware owns the curve)
- Intel NUC (Intel support: "no software controllable fans")
- Dell XPS 13 9320 / XPS 15 9500 / XPS 17 9710 / XPS 9300 (post-2020
  XPS chassis are EC-locked; only the 9350 has the i8kutils trick)
- Razer Blade 14 (2014 model — pre-modern EC unrecognized)

**Forum-access caveats (URLs that 403'd or returned headlessly-empty bodies):**

- `forums.lenovo.com/t5/...` — Khoros platform requires session cookies;
  WebFetch returned only the page title. Used Google indexed snippets.
- `community.acer.com/en/discussion/...` — Vanilla Forums returns truncated
  body without JavaScript; same fallback used.
- Both Dell threads loaded fully via WebFetch — Dell's community is the
  most agent-friendly of the four big OEMs.
- HP `h30434.www3.hp.com` partial — search-indexed content was sufficient.
- ASUS ROG `rog.asus.com/forum/printthread.php?...` returns the print view
  reliably; the regular forum view requires login.
- Framework `community.frame.work` (Discourse) is fully WebFetch-compatible
  — Framework wins the agent-friendliness ranking by a wide margin.

**Implication for ventd dispatch design.** The data argues for a four-tier
probe:

1. *Vendor-daemon detection* (asusd, system76-power, tccd, slimbookbattery)
   — if present and active, ventd should *defer entirely*, not race them.
2. *In-tree module + module-param* path (thinkpad_acpi, dell-smm-hwmon,
   hp-wmi ≥ 6.20, alienware-wmi ≥ 6.18, asus-nb-wmi, applesmc, surface_fan,
   surface_aggregator, yogafan).
3. *OOT/DKMS detection* (legion-laptop, framework_laptop, isw, faustus,
   omen-fan-control, razer-laptop-control, t2fanrd, steamdeck-dkms).
4. *Firmware-managed / monitor-only* (Star Labs coreboot, Asahi macsmc,
   Surface SAM, Intel NUC, all HP EliteBook/ZBook ≥ G10, post-2020 XPS) —
   ventd should report capability and not attempt PWM writes.

The DMI quirk-table will be the single biggest piece of OEM-specific code in
ventd. Plan for ~50 entries at GA based on this survey alone, growing as
new generations ship.
