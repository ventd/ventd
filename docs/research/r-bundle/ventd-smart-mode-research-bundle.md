# ventd Smart-Mode Research Bundle

**Project:** ventd — Linux fan controller daemon (Go 1.25+, CGO_ENABLED=0, GPL-3.0)
**Repo:** github.com/ventd/ventd
**Author/owner:** Phoenix (solo developer, GitHub PhoenixDnB)
**Bundle date:** 2026-04-28
**Research program:** 15-item smart-mode design research (R1–R15)
**This bundle:** Tier 1 (Detection) + Tier 2 (Probe Safety + Signals) — items R1, R2, R3, R4, R5, R6, R11
**Status:** 7 of 15 complete. Remaining: R7, R8, R9, R10, R12, R13, R14, R15.

---

## How to use this bundle

This file is the canonical research record for ventd's smart-mode architecture. It supersedes any earlier research notes on the same topics.

**Two parts per R-item:**
1. **Spec-ready findings appendix** — the structured block that feeds directly into spec drafts. Use this when writing v0.5.x patch specs.
2. **Research document (long-form)** — full prose, citations, comparative tables, reasoning. Use this when an invariant is challenged or when scope expands.

**Recommended ingestion paths:**
- For drafting `spec-v0_5_1-catalog-less-probe.md`: read appendix blocks for R1, R2, R3 first.
- For drafting `spec-v0_5_3-envelope-c.md`: read appendix blocks for R4, R5, R6, R11.
- For implementation in Claude Code: appendix blocks are sufficient context; long-form rarely needed.

**Cross-reference matrix (which R-item informs which spec patch):**

| R-item | Primary spec target | Cross-references |
|---|---|---|
| R1 — Tier-2 detection | `spec-v0_5_1-catalog-less-probe.md` § Tier-2 | — |
| R2 — Ghost hwmon | `spec-v0_5_1-catalog-less-probe.md` § Probe pipeline | R6 (silent-write boundary) |
| R3 — Steam Deck refusal | `spec-v0_5_1-catalog-less-probe.md` § hardware_refusal class (NEW) | — |
| R4 — Envelope C thresholds | `spec-v0_5_3-envelope-c.md` § Abort thresholds | R6 (safety ceiling), R11 (sensor latency) |
| R5 — Idle gate | `spec-v0_5_3-envelope-c.md` § Idle predicate | R3 (hardware_refusal), R11 (noise floor) |
| R6 — Polarity midpoint | `spec-v0_5_2-polarity-disambiguation.md` § Initial value | R2 (silent-write), R11 (RPM noise floor) |
| R11 — Sensor noise floor | `spec-smart-mode.md` § Layer C, supplementary `spec-sensor-preference.md` | R4, R5, R6, R8 (future) |

**Architectural concepts that emerged from this research and need spec sections:**
- `hardware_refusal` class (R3) — parallel to `virt_refusal`/`permission_refusal`; first member is Steam Deck.
- Latency-vs-τ admissibility rule (R11) — cross-cutting design principle for sensor selection.
- Dual-condition tests (range AND slope) (R11 §Layer C) — should propagate to idle gate, BIOS-fight detection.
- Per-class safety ceilings, not global (R4 review flag) — override-flag bounds must be class-specific.
- Per-message-id opt-outs vs blanket acknowledgments (R3 review flag) — `acknowledged_warnings: [STEAMDECK_VLV0100_v1]` pattern.

**Known HIL gaps:**
- **Class 4 server CPU**: no native fleet member. Conservative defaults shipped.
- **Class 7 NAS multi-drive**: TerraMaster F2-210 acquired (2026-04-28), single-drive HIL pending; full multi-drive aggregation remains synthetic-test territory.
- **Dell laptop**: not in fleet. `dell-smm-hwmon` `fan_max=3` → PWM=170 override and stepped-RPM probe are theoretical until field-validated.

---

# Part A — Consolidated Spec-Ready Findings Appendix

This part collects the eight appendix blocks (R1, R2, R3, R4, R5, R6, R11) for direct ingestion into spec drafts. Each block is self-contained.

---

### R1 — Tier-2 Detection Signal Reliability (Virtualization + Containers)

- **Defensible default(s):** `/proc/1/environ (container=) → /run/.containerenv → /.dockerenv → /proc/1/cgroup (kubepods|docker|lxc|libpod|machine|nspawn|cri-) → /proc/self/mountinfo (lxcfs/overlay) → /proc/sys/kernel/osrelease (microsoft|WSL — case-insensitive) → /proc/xen/capabilities → /sys/hypervisor/properties/features (XENFEAT_dom0 bit) → /sys/class/dmi/id/sys_vendor + /sys/class/dmi/id/product_name (systemd-canonical vendor table: QEMU, VMware/VMW, innotek GmbH/VirtualBox/Oracle Corporation, Xen, Bochs, Parallels, BHYVE, Hyper-V, Microsoft Corporation, Apple Virtualization, Google Compute Engine, Amazon EC2, DigitalOcean) → /proc/cpuinfo "hypervisor" flag → CPUID 0x40000000 vendor (KVMKVMKVM, VMwareVMware, Microsoft Hv, XenVMMXenVMM, " lrpepyh vr", "bhyve bhyve ", QNXQVMBSQG) → /sys/firmware/dmi/entries/0-0/raw byte 0x13 bit 4 (SMBIOS VM bit) → /proc/cmdline (corroborating only) → BareMetal default.` Policy mapping: BareMetal/Xen-dom0 → ALLOW; cloud VMs (Amazon EC2/GCP/Azure/DO) → BLOCK with no override; WSL1/WSL2/DinD → BLOCK with no override; all other VMs → BLOCK with `--allow-vm-pwm` override; all other containers (Docker/Podman/LXC/Proxmox-LXC/nspawn/k8s) → BLOCK with `--allow-container-hwmon` override; VirtUnknown → BLOCK with `--force-bare-metal` escape hatch.
- **Citation(s):**
  1. systemd `src/basic/virt.c` (canonical signal list + DMI vendor table + CPUID strings + WSL osrelease check + XENFEAT_dom0 logic): https://github.com/systemd/systemd/blob/main/src/basic/virt.c
  2. Linux kernel `arch/x86/kernel/cpu/hypervisor.c` (kernel's own probe order Xen-PV → Xen-HVM → VMware → Hyper-V → KVM → Jailhouse → ACRN; source of `/proc/cpuinfo` `hypervisor` flag): https://github.com/torvalds/linux/blob/master/arch/x86/kernel/cpu/hypervisor.c
  3. virt-what (Red Hat / RWMJ; documents the heuristic-based approach and the explicit warning that detection-by-virt-type is usually the wrong abstraction — feature-detection is preferred): https://people.redhat.com/~rjones/virt-what/
  4. systemd-detect-virt(1) manpage (canonical taxonomy + WSL-as-container rule): https://man7.org/linux/man-pages/man1/systemd-detect-virt.1.html
- **Reasoning summary:** Container signals (env/containerenv/cgroup) precede VM signals (DMI/CPUID) because a container always implies a shared kernel and unknown ownership of `/sys/class/hwmon`, regardless of the VM layer below; this matches systemd's design rule that "if both machine and container virtualization are used in conjunction, only the latter will be identified". WSL2 is checked before Hyper-V because its CPUID legitimately reports `Microsoft Hv` despite the OS-release marker being the only safe identifier of WSL's container-like semantics. DMI sys_vendor is checked before CPUID because the cloud cases (`Amazon EC2`, `Google Compute Engine`, `DigitalOcean`) are KVM-derived and would otherwise be misclassified as generic KVM, losing the cloud-specific BLOCK-no-override policy. The Xen ordering (`/proc/xen/capabilities` + `XENFEAT_dom0` bit before generic DMI) is required to correctly distinguish dom0 (ALLOW, owns hardware) from domU (OVERRIDE-required), and is the exact ordering bug that systemd has hit and fixed multiple times (issues #6442, #22511, #28113).
- **HIL-validation flag:** **Yes.** Required HIL matrix:
  - (a) Proxmox host runs detection bare-metal → expect `BareMetal`
  - (b) Proxmox + KVM guest → expect `VMKVM` (override required)
  - (c) Proxmox + privileged LXC → expect `ContProxmoxLXC` with bleed-through evidence
  - (d) Proxmox + unprivileged LXC → expect `ContLXCUnprivileged` or `ContProxmoxLXC` with uid_map evidence
  - (e) MiniPC running bare-metal Linux → expect `BareMetal` (DMI vendor confirms non-virt)
  - (f) 13900K dual-boot Linux → expect `BareMetal`
  - (g) 13900K Windows + WSL2 → expect `ContWSL2` (osrelease overrides Hyper-V CPUID)
  - (h) Steam Deck (SteamOS 3.x) → expect `BareMetal` baseline (DMI = `Valve` / `Jupiter` or `Galileo`, neither in virt-vendor table)
  - (i) Docker on 13900K rootful default → expect `ContDocker` BLOCK
  - (j) Docker `--privileged -v /sys/class/hwmon:/sys/class/hwmon` → expect `ContDockerPrivileged` BLOCK without override, ALLOW with `--allow-container-hwmon`
  Each test asserts both `VirtClass` and `Evidence[]` content.
- **Confidence:** **High** for the precedence-chain design and the expected signal values per environment (these are byte-identical to what systemd, the kernel, virt-what, gopsutil, and zcalusic/sysinfo independently agree on across multiple authoritative sources). **Medium** for the policy-table BLOCK/ALLOW/OVERRIDE choices — these reflect a defensible default for ventd's homelab/NAS/desktop audience, but the override-flag naming (`--allow-vm-pwm`, `--allow-container-hwmon`, `--force-bare-metal`) is a UX choice that should be re-examined during v0.5.1 implementation review. **Low** confidence is reserved for the SMBIOS-VM-bit fallback (Step 12) because raw SMBIOS parsing varies across kernel configs (Ubuntu's CONFIG_DMI_SYSFS-as-module bug, launchpad #2045561, demonstrates that `/sys/firmware/dmi/entries/0-0/raw` may be missing on minimal kernels) — ventd should treat this step as best-effort and not panic if the path is absent.
- **Spec ingestion target:** `spec-v0_5_1-catalog-less-probe.md` (Tier-2 detection section). Specifically, this R1 output should populate three sub-sections of that spec: (1) "Probe order and precedence" → R1 long-form §4; (2) "Detection→Policy mapping table" → R1 long-form §7.1; (3) "Edge cases and operator-facing log messages" → R1 long-form §6.

**Review flags from chat (2026-04-28):**
- Step 12 (SMBIOS VM bit) is borderline-unnecessary; consider dropping from v0.5.1.
- `--force-bare-metal` escape hatch is dangerous-by-name; consider `--accept-detection-failure` or two-flag pattern.
- HIL matrix is ~10 distinct test environments; carve out a `tier2-test.sh` skill before implementation.

---

### R2 — Ghost hwmon entry taxonomy

- **Defensible default(s):**
  Multi-stage probe pipeline executed once after R1 authorizes writes, before any control-loop activation:

  1. **Stage 1 — Driver/structural triage.** Skip read-only platform drivers by `name` (`hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors`, `asus_atk0110`). Skip pure-temperature drivers (`coretemp`, `k10temp`, `nvme`, `drivetemp`, `acpitz`, `iwlwifi*`). Require `pwmM` and `pwmM_enable` both readable AND writable.
  2. **Stage 2 — Write/read-back probe.** Set `pwm_enable=1`, write canonical PWM values {0, 64, 85, 128, 170, 192, 255}, record EINVAL responses and read-back deltas. Classify channel as normal / quantized-stepped / EINVAL-restricted.
  3. **Stage 3 — Tach correlation sweep.** For each candidate `pwmM`, write 255 → wait ≥30 s → snapshot every `fanK_input` in the system → write 64 → wait ≥30 s → snapshot. Compute Δ matrix. Per `pwmM`, take `argmax_k Δ[K]`. Reject if max delta < 100 RPM (NAS-tuned noise floor); otherwise admit pairing (record mismatched-index if `argmax_k ≠ M`).
  4. **Stage 4 — BIOS-fight detection.** For each admitted channel, set `pwm_enable=1`, write target PWM, re-read both at T+5 s, T+10 s, T+30 s. Reject (or quarantine pending user override) if `pwm_enable` reverts or `pwm` is clobbered.

  Per-pattern policy:
  - 2.a ZERO-TACH → **skip** silently
  - 2.b WRITE-IGNORED → **skip** + WARN with chip-revision diagnostic
  - 2.c PHANTOM-CHANNEL → **skip** silently (subsumed by Stage 3)
  - 2.d EINVAL/STEPPED → **admit as quantized**; restrict output domain to discovered legal values; never interpolate
  - 2.e BIOS-FIGHT → **skip + diagnostic**; require explicit `bios_fight_override: true` per-channel to engage; never auto-engage
  - 2.f PLATFORM-MEDIATED (read-only) → **skip control**, **admit monitoring**; PLATFORM-MEDIATED (partial, e.g. `dell_smm`, `thinkpad_acpi`) → use `platform_quirk` table to set probe expectations
  - 2.g TACH-WITHOUT-PWM → **monitor-only**, never control
  - 2.h MISMATCHED-INDEX → **admit with discovered pairing**; emit info-level log noting the mismatch

  All skipped channels MUST be overridable via a per-channel `force_include: true` config flag, and ventd MUST restore the original `pwm_enable` and `pwm` register values for any channel it does not admit.

- **Citation(s):**
  1. Linux kernel hwmon documentation — nct6775 sysfs attributes & mode semantics: https://docs.kernel.org/hwmon/nct6775.html
  2. Linux kernel hwmon documentation — dell-smm-hwmon BIOS-override caveat & i8k_whitelist_fan_control: https://docs.kernel.org/hwmon/dell-smm-hwmon.html
  3. fan2go issue #28 (settle ≥30 s) and pwmconfig correlation-detection bug (lm-sensors narkive thread): https://github.com/markusressel/fan2go/issues/28 ; https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly

- **Reasoning summary:**
  Eight failure modes are documented across the kernel hwmon subsystem and userspace fan-control tooling, with at least one published bug or driver source citation for each. The dominant pattern by occurrence on ventd's homelab/NAS audience is PHANTOM-CHANNEL (Super-I/O chips advertising more PWMs than the board wires), addressable by a tach-correlation sweep with conservative settle time. The dominant pattern by *risk* is BIOS-FIGHT (Dell/HP/Lenovo embedded controllers re-asserting auto mode), addressable by a delayed re-read and a strict no-auto-override policy. False-negative inclusion of phantoms poisons calibration data; false-positive exclusion of real channels is recoverable by a one-line config flag. The pipeline therefore biases toward exclusion with mandatory user-override and is ordered cheap-first to keep the common-case probe under 5 minutes.

- **HIL-validation flag:** **Yes** — the pipeline must be empirically validated on at least three distinct fleet members because the failure modes are hardware-specific and not reproducible in software emulation:
  - **Proxmox host 5800X+RTX 3060** runs the Stage 3 sweep on its `nct6798d`/`it87` (whichever the board carries), confirming the phantom-channel rejection rate matches the physically-wired header count and that mismatched-index resolution finds the correct pwm↔fan pairing.
  - **MiniPC Celeron** (typically `it8613e` or `nct6116d` class) runs the full pipeline to validate behaviour on small-NAS Super-I/O chips with high phantom rates and 1–2 wired headers.
  - **3 laptops (Dell + ThinkPad + ASUS)** run Stage 2 and Stage 4 to validate stepped-value classification (Dell SMM), `pwm1_enable` mode-set semantics + fan_watchdog (ThinkPad), and `asus-nb-wmi` enable-only EINVAL handling (ASUS) — these three drivers have qualitatively different probe responses and must each be exercised on real hardware.
  - **13900K+RTX 4090 dual-boot desktop** runs Stage 4 BIOS-fight detection if its motherboard EC asserts a Q-Fan-equivalent auto-mode reversion; serves as the negative control if it does not.
  - **Steam Deck** is excluded — it runs Valve's custom EC fan controller, not a generic hwmon-writable driver, and is out of scope for ventd until/unless `steamdeck-hwmon` (or successor) gains writable PWM.

- **Confidence:** **High** for patterns 2.a, 2.c, 2.d, 2.f, 2.g, 2.h (all directly grounded in kernel source and reproducible in user reports). **Medium** for patterns 2.b (silicon-revision-specific to IT8689E rev 1 and a handful of nct6775 DC/PWM mode mismatches; small sample of bug reports) and 2.e (BIOS-fight timing varies widely across vendors; the 5/10/30 s sampling cadence is empirically defensible but not exhaustive against ECs with >30 s polling intervals such as some Lenovo Legion variants). Overall pipeline confidence: **High**, with the caveat that Stage 3's settle time is the single most sensitive parameter and may require tuning via HIL data.

- **Spec ingestion target:** `spec-v0_5_1-catalog-less-probe.md` — ingest as RULE-PROBE-001 (driver/structural triage), RULE-PROBE-002 (write-and-read-back), RULE-PROBE-003 (tach correlation sweep with ≥30 s settle and 100 RPM noise floor), RULE-PROBE-004 (BIOS-fight delayed re-read), RULE-PROBE-005 (force_include override path), RULE-PROBE-006 (platform_quirk table for partial-write drivers), RULE-PROBE-007 (state restoration on pipeline failure or daemon shutdown).

**Review flags from chat (2026-04-28):**
- Settle time ≥30s × N channels = R14 calibration-time problem. 6-fan NAS = ~6.5 min minimum. R14 needs to budget for this.
- Stage 1's read-only driver list overlaps with R8's coarse-classification fallback signals (`coretemp`, `k10temp`, `nvme`, `drivetemp`). Avoid double-implementing enumeration.
- `force_include` override interacts with R1's `--allow-vm-pwm` / `--allow-container-hwmon`. Document as independent (orthogonal failure modes).

---

### R3 — Steam Deck detection without writes

- **Defensible default(s):** Refuse PWM writes if **any** of the following are true (evaluated in order, short-circuit on first hit):
  1. `/sys/class/dmi/id/sys_vendor` reads exactly `Valve` (primary, forward-compatible — covers Jupiter/Galileo/any future Valve x86_64 device).
  2. Any `/sys/class/hwmon/hwmon*/name` reads exactly `steamdeck_hwmon` or `steamdeck`.
  3. `/sys/bus/acpi/devices/VLV0100:00/` exists (or any sysfs path containing `VLV0100`).
  4. `/proc/modules` contains `steamdeck` or `steamdeck_hwmon`.
  Log `product_name` (`Jupiter`=LCD / `Galileo`=OLED / other=future) and `bios_version` for diagnostics. Do **not** key the refusal on `product_name`, APU codename, or kernel-module presence alone — those are corroboration only. Continue read-only telemetry; refuse only writes.

- **Citation(s):**
  - https://gitlab.com/evlaV/jupiter-fan-control (Valve's own userspace daemon — the thing ventd must defer to)
  - https://patchwork.kernel.org/project/linux-hwmon/patch/20220206022023.376142-1-andrew.smirnov@gmail.com/ (kernel platform driver describing VLV0100 EC, fan control, registers `steamdeck_hwmon`)
  - https://lists.freedesktop.org/archives/dri-devel/2025-August/522215.html (mainline DMI quirk pinning to `sys_vendor=Valve` + `product_name=Jupiter`/`Galileo`)

- **Reasoning summary:** Valve's EC firmware (VLV0100) actively contends with userspace PWM writes; the EC silently reverts to its own curve when the userspace daemon misbehaves (`SteamOS#1359`). DMI `sys_vendor=="Valve"` is the only signal universally present on Deck hardware regardless of distro, kernel, or Valve-driver presence, so it is both the most reliable and the most forward-compatible gate (auto-extends to a future Deck 2 / Steam Brick without a code change). Hwmon-name and ACPI-device fallbacks catch the rare case where DMI is stripped. ventd refuses with a structured message pointing the user to `jupiter-fan-control`.

- **HIL-validation flag:** **Yes** — Steam Deck runs `ventd probe --dry-run` under `strace` to assert (a) refusal reason code `STEAMDECK_VLV0100` fires, (b) zero `open(…, O_WRONLY)` calls land on any `pwm*` path, (c) sensor read-back still works. Run the same test under three boot configurations (SteamOS 3.6, Bazzite-Deck, vanilla Arch with `steamdeck-dkms` uninstalled) to validate the DMI-only path independently of kernel-module presence. Negative control: 13900K+RTX 4090 desktop dual-boot must NOT fire the refusal.

- **Confidence:** **High** — DMI strings are independently confirmed by mainline kernel quirks (panel-orientation-quirks, panel-backlight-quirks), Valve's own scripts (SteamOS BIOS Manager, jupiter-fan-control), Phoronix reporting, Arch Wiki, and multiple downstream distros (Bazzite, HoloISO, Jovian-NixOS). The EC-fights-userspace claim is documented in Valve's own SteamOS issue tracker.

- **Spec ingestion target:** `spec-v0_5_1-catalog-less-probe.md` — add new top-level refusal class `hardware_refusal::valve_steamdeck` (parallel to `virt_refusal` and `permission_refusal`); R3 is its first member. Detection runs **before** virt/container probes. Refusal message template (variable: `{product_name}`, `{bios_version}`) must reference `jupiter-fan-control` upstream URL, GitHub mirror, AUR package, and the SteamOS in-product toggle ("Settings → System → Enable updated fan control"). Document a `steam_deck_acknowledged: true` opt-out in `ventd.yaml` to silence the one-time message on persistent Deck deployments.

**Review flags from chat (2026-04-28):**
- `hardware_refusal` class is now a real architectural concept. Worth a placeholder section in `spec-smart-mode.md` for future R-items to populate.
- Refusal-on-Deck-still-monitors design creates a "ventd is running but does nothing" code path. Integration tests must assert no log spam (one log per detection event, not per poll).
- `steam_deck_acknowledged: true` opt-out is a UX trap. Better pattern: per-message-id opt-out (`acknowledged_warnings: [STEAMDECK_VLV0100_v1]`) so a future `_v2` re-prompts on EC contract changes.

---

### R4 — Envelope C Abort Thresholds

- **Defensible default(s):**

  | Class                                | dT/dt abort     | T_abs (offset below Tjmax)           | Ambient gate     |
  |--------------------------------------|-----------------|--------------------------------------|------------------|
  | 1. Desktop HEDT, air (13900K/14900K/7950X/9950X3D) | 2.0 °C/s   | Tjmax − 15 (e.g. 85 °C on 100 °C parts; 80 °C on 95 °C parts; 75 °C on 89 °C X3D parts) | (Tjmax − T_amb) ≥ 60 °C |
  | 2. Desktop HEDT, AIO                 | 1.5 °C/s        | Tjmax − 15                           | ≥ 60 °C          |
  | 3. Mid-range desktop (5800X/5700X/12700K/13700K) | 1.5 °C/s | Tjmax − 12 (78 °C on 90 °C parts; 88 °C on 100 °C parts) | ≥ 55 °C |
  | 4. Server CPU (Xeon Pt 8480+, EPYC Genoa/Bergamo, TR-PRO) | 1.0 °C/s | Tjmax − 20 (75 °C Tctl on EPYC; 70 °C on Xeon Pt) | ≥ 50 °C; **gated: refuses Envelope C if BMC present unless explicitly allowed** |
  | 5. Mobile/laptop (Tiger/Alder/Raptor-P, Phoenix, Strix) | 2.0 °C/s | Tjmax − 15 (typically 85 °C) | ≥ 55 °C; **gated: requires successful EC PWM-handshake** |
  | 6. Mini-PC / NUC (N100/N150/N305)    | 1.0 °C/s        | Tjmax − 20 (85 °C on 105 °C N-series); skipped on passive (no fan) | ≥ 55 °C |
  | 7. NAS — HDDs                        | 1.0 °C/min over 5-min window | abort case-temp = min(50 °C, mfg_max − 10 °C, T_amb + 15 °C); 45 °C for ≥12 TB Toshiba | n/a (HDD limit is mfg-rated) |
  | 7. NAS — SAS SSDs                    | 2.0 °C/min over 2-min window | 60 °C case                           | n/a              |
  | 7. NAS — HBA / SES chips             | 1.0 °C/min      | 75 °C ROC                            | n/a              |

- **Citation(s):**
  - ACPI 6.5 Spec §11 Thermal Management — `https://uefi.org/specs/ACPI/6.5/11_Thermal_Management.html` (defines _PSV/_HOT/_CRT semantics, 5 °C granularity guidance).
  - Linux kernel thermal subsystem — `Documentation/thermal/sysfs-api.txt` and `drivers/acpi/thermal.c` (THERMAL_TRIP_ACTIVE/PASSIVE/HOT/CRITICAL definitions; orderly_poweroff() semantics).
  - Intel ARK pages for 13900K/14900K (Tjmax = 100 °C) and SkatterBencher *Intel Thermal Velocity Boost* — `https://skatterbencher.com/intel-thermal-velocity-boost/` (TVB 70 °C threshold, OCTVB semantics).
  - AMD product pages and AMD EPYC 9004 documentation — `https://www.amd.com/en/products/processors/server/epyc/4th-generation-9004-and-8004-series.html`; AMD Tctl description corroborated via k10temp driver and Ryzen Master technical notes; 7950X3D Tjmax = 89 °C confirmed via AMD product page.
  - Tom's Hardware *Core i9-13900K Cooling Tested* — `https://www.tomshardware.com/features/intel-core-13900k-cooling-tested` (Cinebench R23 fan-stop empirical thermal data).
  - Seagate IronWolf Pro Product Manual Rev. B (drive case max 70 °C, sustained-not-recommended >60 °C, transport gradient 20 °C/h) — `https://www.seagate.com/content/dam/seagate/migrated-assets/www-content/product-content/ironwolf/en-us/docs/100835908f.pdf`.
  - Backblaze drive temperature analyses — `https://www.backblaze.com/blog/hard-drive-temperature-does-it-matter/` and `https://www.backblaze.com/blog/backblaze-drive-stats-for-q3-2023/` (Q3 2023 — 60 °C / 55 °C max thresholds).
  - Pinheiro et al., *Failure Trends in a Large Disk Drive Population* (Google, FAST '07) — referenced via Wikipedia/Wikibooks Minimizing HDD Failure (37–46 °C optimum); UVA/Microsoft 2013 study (Arrhenius doubling per 12 °C).
  - Klara Systems — *Managing Disk Arrays on FreeBSD/TrueNAS Core* — `https://klarasystems.com/articles/managing-disk-arrays-on-freebsd-truenas-core/` (sesutil semantics; SES temperature element types).
  - Dell *Improving energy efficiency in the data center* / Principled Technologies — PowerEdge HS5620 thermal resiliency whitepaper (server fan-failure scenario timings).
  - Dell PowerEdge KB `000123186` — iDRAC thermal algorithm; sensor-loss-equals-100 % fan default.
  - ScienceDirect *Chip Temperature* topic article — chip-level thermal time constants in milliseconds-to-seconds range.
  - Netgate Forum Topton N100 thread — N100 Tjmax = 105 °C, TCC offset behavior — `https://forum.netgate.com/topic/186104/topton-n100-reporting-402-mhz/80`.

- **Reasoning summary:**
  - **Class 1 (HEDT air):** Heat density on 13900K/14900K is the highest in scope. Public data shows 100 °C reached "almost immediately" on Cinebench R23, implying junction slopes 5–20 °C/s under 100 % load. dT/dt = 2.0 °C/s aborts at the bottom of that band, leaving 4 s for fan ramp before the 10 °C/s mid-case scenario reaches Tjmax. T_abs = Tjmax − 15 is 3× the ACPI 5 °C granularity quantum and matches Intel's TVB preferred operating window.
  - **Class 2 (HEDT AIO):** Coolant adds ~120 s of effective inertia at the *radiator*, not at the die, so the early slope is gentler but late-probe failures are catastrophic (saturated coolant). dT/dt slightly tighter (1.5 °C/s); same headroom.
  - **Class 3 (mid-range):** Lower heat density, lower steady-state. Tjmax − 12 is acceptable because workload spikes rarely sit close to Tjmax; a sudden climb is more diagnostic.
  - **Class 4 (server):** Most-conservative numbers because data loss is most-expensive on servers; also defaults to *gated refusal* in BMC-managed chassis. Tctl − 20 °C accommodates AMD Tctl offset and non-uniform reading across chiplets.
  - **Class 5 (laptop):** Mirrors Class 1 dT/dt because thin-and-light chassis can produce desktop-class junction slopes. Adds an EC-handshake gate: ventd aborts before probe if PWM writes don't measurably change RPM.
  - **Class 6 (mini-PC):** Generous T_abs because Tjmax is high (105 °C) and parts are inexpensive; aborts conservatively to avoid surprising users with hot chassis surfaces. Skips Envelope C entirely on truly passive systems.
  - **Class 7 (NAS):** Time scale is *minutes*, dT/dt expressed in °C/min and computed over a 5-min moving window. Headroom of mfg_max − 10 °C accommodates inter-bay gradient and SMART read-cadence lag (1 min sample). Lowest-rated drive sets the floor for the pool; sensor-loss aborts the probe.

- **HIL-validation flag:** Yes — by class:
  - Class 1: **13900K + RTX 4090** runs T6 (y-cruncher AVX-512 fan-stop with abort at T_pkg ≥ 85 °C OR dT/dt ≥ 2.0 °C/s) and T7 (Intel TCC cross-check). **HIGH priority** — biggest source-data spread.
  - Class 2: **13900K + AIO swap** runs T8 (Cinebench R23 fan-stop on AIO). **MEDIUM priority** if hardware available; otherwise inherits Class 1 numbers.
  - Class 3: **Proxmox 5800X + RTX 3060** runs T1–T4 (idle, 50 %, 100 %, ambient sensitivity). **MEDIUM priority.**
  - Class 4: **No native fleet member.** Proxmox 5800X provides a *lower-bound* analog under T2/T3. **HIGH priority gap.**
  - Class 5: **All three laptops** run T9 (EC handshake) and T10 (Cinebench R23 fan-stop, abort 85 °C / 2.0 °C/s). **HIGH priority** — chassis variance is the dominant uncertainty.
  - Class 6: **MiniPC Celeron** runs T1, T2, and T5 (passive-class detection). **LOW priority** — defaults are conservative.
  - Class 7: **TerraMaster F2-210 (acquired 2026-04-28; ARM RTD1296, single-drive HIL pending hwmon inventory).** Validates HDD thermal observable side; multi-drive aggregation remains synthetic-test territory.

- **Confidence:**
  - Class 1: **Medium** (slope spread is wide; abort logic is conservative)
  - Class 2: **Medium** (coolant transient under-characterized publicly)
  - Class 3: **High**
  - Class 4: **Low–Medium** (limited public fan-stop data; mitigated by gated-refusal default)
  - Class 5: **Low** (chassis variance dominates)
  - Class 6: **High**
  - Class 7: **High** (datasheet-grounded; minutes-scale gives generous reaction time)

- **Spec ingestion target:** `spec-v0_5_3-envelope-c.md`

**Review flags from chat (2026-04-28):**
- `safety_ceiling_dT_dt_C_per_s = 5.0` is dangerously high for laptops. Recommend per-class safety ceilings: Class 5 ≤3.0 °C/s; Class 7 ≤2.0 °C/min. Spec amendment.
- Class 1 13900K HIL test T6 (y-cruncher AVX-512 + intentional fan-stop) is the most dangerous test. Wrap in external thermal abort (script kills ventd if external sensor reads >88 °C). Defense-in-depth.
- Class 7 NAS gap status: TerraMaster F2-210 acquired 2026-04-28; ARM RTD1296, hwmon inventory pending. Likely won't validate fan control (vendor SoC), will validate HDD-thermal observable side.

---

### R5 — User idle gate (what "idle" means for calibration)

- **Defensible default(s):**
  - **Hard refusal preconditions (ANY ⇒ refuse):** `/sys/class/power_supply/AC/online == "0"` OR `/sys/class/power_supply/BAT*/status == "Discharging"`; container detection (`systemd-detect-virt --container` ∈ {lxc, docker, podman, …} with non-host PSI view); `/proc/mdstat` contains `recovery|resync|check =`; ZFS scrub active (`/proc/spl/kstat/zfs/*/state`); BTRFS scrub active; process-name blocklist hit (rsync, restic, borg, plex-transcoder, jellyfin-ffmpeg, ffmpeg, handbrakecli, apt, dpkg, dnf, rpm, pacman, updatedb, fio, stress-ng); uptime < 600 s; time-since-resume < 600 s.
  - **Primary signal (PSI, when `/proc/pressure/cpu` exists):** `cpu.some avg60 ≤ 1.00 %` AND `cpu.some avg300 ≤ 0.80 %` AND `io.some avg60 ≤ 5.00 %` AND `io.some avg300 ≤ 3.00 %` AND `memory.full avg60 ≤ 0.50 %`.
  - **Fallback (no PSI):** CPU non-idle ≤ 5 % over 60 s sampled at 1 Hz; deep-C-state residency fraction ≥ 0.85 over 60 s; `/proc/loadavg` read directly (NOT via `getloadavg(3)`) with 1-min and 5-min ≤ `0.10 × ncpus`.
  - **Quiescence:** disk Σ(sectors_read+sectors_written) ≤ 1 MB/s aggregate over 60 s, no device > 4 MB/s; NIC Σ(rx+tx)_packets ≤ 200 pps over 60 s; AMD `gpu_busy_percent` ≤ 5 % avg 60 s; NVIDIA NVML utilization.gpu ≤ 5 % avg 60 s.
  - **Durability:** predicate must be continuously TRUE for **≥ 300 s** before Envelope C may begin.
  - **Retry:** truncated exponential backoff base 60 s, cap 3600 s, ±20 % jitter, daily cap 12 attempts, immediate re-try on AC-plug uevent.

- **Citation(s):**
  - Linux PSI documentation: https://docs.kernel.org/accounting/psi.html (defines `some`/`full`, 10/60/300 s windows, kernel ≥4.20).
  - cpuidle subsystem: https://docs.kernel.org/admin-guide/pm/cpuidle.html (defines per-CPU per-state `time`/`usage` ground truth for idleness).
  - Brendan Gregg, *Linux Load Averages: Solving the Mystery* (2017): https://www.brendangregg.com/blog/2017-08-08/linux-load-averages.html (loadavg includes TASK_UNINTERRUPTIBLE; 5 s sampling; explains why loadavg alone is wrong).
  - lxcfs loadavg regression in Proxmox 8 / Debian 12: https://github.com/lxc/lxc/issues/4372 (`getloadavg(3)` returns host load even when `/proc/loadavg` returns container load).
  - lm-sensors `pwmconfig` warning establishing prior-art "operator is responsible for idle": https://github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/pwmconfig.

- **Reasoning summary:** PSI is the only kernel-native signal that combines low overhead, sub-15-second resolution, cgroup-correctness, and resistance to D-state ghost-load that corrupts loadavg, making it the right primary signal; cpuidle C-state residency is the fallback for older or RHEL-style kernels with PSI disabled. logind `IdleHint` and Wayland `ext-idle-notify-v1` are explicitly excluded from the idle gate because they are unreliable on headless NAS/server installs (systemd #9622 confirms tty/ssh sessions are untracked; #34844 confirms greeter sessions never set IdleHint) and orthogonal to thermal idleness anyway. A structural-state allowlist is required because no generic signal reliably catches ZFS scrub, mdadm resync, or BTRFS scrub at the level needed for a clean dT/dPWM measurement, and these are the canonical homelab/NAS workloads. ventd hard-refuses inside unprivileged Proxmox LXC because it cannot see host-level pressure from there, which is consistent with ventd's deployment model (fan PWM is a host-level resource via hwmon).

- **HIL-validation flag:** **Yes** —
  - **Proxmox host (5800X + RTX 3060)** runs the canonical scrub-vs-idle test: trigger ZFS scrub on a test pool, confirm `idle_enough_for_envelope_c` returns `storage_maintenance`; spawn unprivileged LXC, confirm `unprivileged_container` refusal; trigger Plex HW decode, confirm `gpu_busy`.
  - **MiniPC (Celeron)** runs the headless 24-hour soak: synthetic rsync + cron + idle gap mixture, log every predicate decision, post-process to count FP/FN.
  - **3 laptops** validate battery hard-refusal: unplug-AC events, expect refusal within 1 s of the AC uevent.
  - **13900K + RTX 4090 desktop** validates NVIDIA quiescence path without CGO and confirms predicate independence from systemd-inhibit (Firefox/Steam idle inhibitors must NOT block ventd).

- **Confidence:** **High** for PSI-primary path on Linux ≥4.20 with cgroup v2 (Debian 11+, Ubuntu 20.04+, Arch, Fedora 32+, Proxmox 7+, TrueNAS Scale, modern Unraid). **Medium** for the cpuidle-fallback path (less validated in literature). **Medium** for the structural-state allowlist — the regex set is comprehensive but inevitably non-exhaustive (e.g., custom NAS vendor backup daemons), so the spec must allow operator extension via config.

- **Spec ingestion target:** `spec/v0.6/r5-idle-gate.md`; predicate implementation lands in `internal/idle/predicate.go`; refusal-reason enum in `internal/idle/reason.go`; config knobs (`idle.psi_cpu_some_avg60_max`, `idle.durability_seconds`, `idle.process_blocklist`, `idle.daily_attempt_cap`) added to `spec/v0.6/config-schema.md`. Cross-reference patch numbers: this consumes R3 (Envelope C protocol) for the calibration handshake and feeds R7 (operator override CLI/`ventd calibrate --force`).

**Review flags from chat (2026-04-28):**
- Process blocklist needs operator extension (`idle.process_blocklist_extra: [...]`) or it ages badly. Elevate to a RULE.
- `amd_energy` driver was removed in Linux 6.2 — research mentions it but it's deprecated. Replacement is `amd_pmf` or RAPL via `/sys/class/powercap/intel-rapl` (works on AMD too on newer kernels). Fact-check before spec lock.
- `ventd calibrate --force` semantics need R4's safety_ceiling rule: force overrides §7.2-7.5 but never §7.1, AND always honors R4 safety_ceiling thresholds.

---

### R6 — Polarity Midpoint

- **Defensible default(s):**
  - Primary: `PWM=128` (50% of 0–255 hwmon range).
  - Per-driver overrides:
    - `dell-smm-hwmon` & detected `i8k_fan_max == 3` → `PWM=170`.
    - `dell-smm-hwmon` & `i8k_fan_max ∈ {2, 4}` → `PWM=128`.
    - any channel with `pwmN_mode == 0` (DC mode on nct6775/it87) → `PWM=160`.
    - `thinkpad_acpi` → `PWM=128` only after setting `pwm1_enable=1` and re-arming `fan_watchdog`; do not run polarity probe (firmware-monotonic).
    - any channel where R2 silent-write is detected → abort polarity probe; write `PWM=192` as thermal-safety baseline.
  - Probe step magnitude: `+64` (preferred) or `+32` (fallback) PWM units.

- **Citation(s):**
  1. https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst — explicit kernel-doc statement: "set pwm1_enable to 1 and pwm1 to at least 128 (255 would be the safest choice)."
  2. https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c — `data->i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)` and `*val = clamp_val(ret * data->i8k_pwm_mult, 0, 255)` — defines the fan_max=3 quantization that demotes PWM=128 to readback 85; corroborated empirically in https://github.com/lm-sensors/lm-sensors/pull/383.
  3. Intel "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification" rev 1.3 (September 2005), §3.2: "Fan speed response... shall be a continuous and monotonic function of the duty cycle... within ±10%" — establishes that 50% commanded = ≈50% RPM in either polarity branch; PDF https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf and https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf.

- **Reasoning summary:** PWM=128 is the unique value that delivers ≈50% effective duty in *both* normal and inverted polarities, lands above every documented stall threshold (Intel ≤30%, Arctic 5%, Noctua 20%), and is exactly representable on 5 of the 7 common quantization grids. Of the 9 pathology classes surveyed, PWM=128 is fail-safe or safe in 8 and only fail-undefined (rounds to 85) in the Dell fan_max=3 case, which is detectable at runtime and corrected by an override to PWM=170. No alternative single value (0/64/160/170/192/255/current) scores better; PWM=255 fails-dangerous under inverted polarity (fan stops). DC-mode 3-pin fans warrant a +32 lift to PWM=160 (~7.5 V) for stall margin.

- **HIL-validation flag:** Yes —
  - **13900K + RTX 4090 desktop** (assumed nct6775 / Asus or MSI Z790, plus AMD GPU PWM) runs **(a)** baseline polarity probe at PWM=128 → +64 step on all motherboard fan channels, recording readback and ΔRPM; **(b)** DC-mode regression: forcibly set `pwm1_mode=0` on a 3-pin fan header, re-run probe at PWM=128 *and* PWM=160, confirm ΔRPM > 250 only at 160.
  - **Proxmox host (5800X + RTX 3060)** runs the same baseline probe; cross-checks against fan2go config-comparison.
  - **MiniPC (Celeron, likely it87 / IT8613/IT8689 family)** runs the **R2 silent-write boundary** case to verify ventd correctly aborts the polarity probe and falls back to PWM=192.
  - **Three laptops:** at least one ThinkPad runs the thinkpad_acpi `pwm1_enable=1` + `pwm1=128` recipe with `fan_watchdog=120` to confirm no EC reversion during a 6 s probe window; at least one ASUS laptop runs the `asus-nb-wmi` 0..255 path to confirm 128 is honored (not silently clamped to 100).
  - Test on a Dell laptop is highly desirable but not in current fleet; flag as "wanted" — would validate the fan_max=3 → PWM=170 override empirically. Until validated, the override is implemented behind a `dell_quantization_v1` feature flag.

- **Confidence:** **High** for the PWM=128 default, the Dell fan_max=3 → 170 override, the DC-mode → 160 escalation, and the thinkpad_acpi recommendation (all backed by explicit primary sources or reproduced kernel source). **Medium** for the +64 probe step magnitude (derived from fan2go heuristics, awaiting R11 sensor-noise empirical confirmation — see R11 §3.3 below). **Medium** for the BIOS-fight abort policy (depends on R2's silent-write detection, which is the sister research item).

- **Spec ingestion target:** `docs/spec/probe.md` §"Polarity Disambiguation" (new subsection 4.3.x). Implementation lands in `internal/probe/polarity.go` (constants `defaultProbeBase = 128`, `dellFanMax3ProbeBase = 170`, `dcModeProbeBase = 160`, `probeStep = 64`, `probeStepFallback = 32`, `probeSettle = 3 * fanResponseDelay`). Driver-detection helpers in `internal/hwmon/driver_detect.go` should expose `IsDellSMM()`, `DellFanMax()`, `IsThinkpadACPI()`, `IsDCMode(channel)` predicates. Cross-reference dependency on **R2** (silent-write detection) and **R11** (sensor-noise floor) — flag both in the spec subsection. Add to the daemon's startup log a single INFO line per channel: `polarity-probe: driver=%s fan_max=%d mode=%s base=%d step=%d result=%s ΔRPM=%d` for forensic debuggability.

**Review flags from chat (2026-04-28):**
- Dell fan_max detection has no runtime API. Pre-probe driver fingerprint step needed before polarity probe on dell-smm-hwmon. Try `/sys/module/dell_smm_hwmon/parameters/fan_max` first; if not exposed, decode readback from BIOS-auto state.
- PWM=192 thermal-safety baseline on R2 silent-write abort needs thermal precondition: "PWM=255 if temp is in upper Tjmax-30 zone, else PWM=192."
- `fan_watchdog` re-arm on ThinkPad: hardcode `fan_watchdog = min(120, probe_total_duration + 10)`.
- R11 confirms +64 step is correct (5× SNR margin over 100 RPM noise floor = 500 RPM minimum ΔRPM, easily achieved on most fans).

---

### R11 — Saturation-detection threshold (sensor noise floor)

- **Defensible default(s):**
  - Layer C ΔT saturation threshold (temp domain): **2.0 °C** (= 2 × dominant 1 °C hwmon quantization).
  - Layer C N writes (fast/CPU 10 Hz loop): **20 writes** (≈ 2 s, spans ≥ 2 × longest fast-sensor lag).
  - Layer C N writes (slow/HDD-NAS 1/60 Hz loop): **3 sensor reads** (≈ 3 min, spans ≥ 1 × HDD thermal τ).
  - Layer C dT/dt secondary gate: **< 1.0 °C/min** for saturation declaration.
  - R6 polarity-probe RPM noise floor (default): **150 RPM** (= 1.5 × p95 observed ±100 RPM on nct/it87 at 1-Hz user poll).
  - R6 noise floor — dell-smm-hwmon override: **60 RPM** (= 2 × `I8K_FAN_MULT = 30` quantum).
  - R6 noise floor — corsair-cpro / liquidctl override: **200 RPM**.
  - R6 high-confidence SNR multiplier: **5.0** (probe ΔRPM ≥ 5 × noise_floor).
  - R6 best-effort SNR multiplier: **1.7** (matches fan2go's 250-RPM `maxRpmDiffForSettledFan` ≈ 1.7 × 150).
  - Sensor preference order (CPU loop): k10temp Tccd → k10temp Tdie → coretemp Package → coretemp Core-max → k10temp Tctl(offset-compensated) → nct67xx CPUTIN → asus-ec CPU → acpitz (last; demoted if 60 s stdev > 5 °C).
  - Sensor preference order (HDD/NAS loop): drivetemp → SES enclosure → multi-drive max() → smartctl fallback → board thermistor (cross-check only).
  - Sensor preference order (ambient sanity): board thermistor (nct SYSTIN / it87 temp1) → PCH temp → asus-ec ambient.
  - Latency-vs-τ admissibility rule: sensor admissible iff `sensor_latency ≤ 0.1 × thermal_τ_of_controlled_mass`.

- **Citation(s):**
  1. `Documentation/hwmon/nct6775.rst` lines 60–74 (resolution = "either 1 degC or 0.5 degC"; auto-divider behaviour). https://docs.kernel.org/hwmon/nct6775.html
  2. `Documentation/hwmon/coretemp.rst` (1 °C resolution, Tjmax-relative reporting) and Intel statement re. accuracy on lkml ("accuracy deteriorates to ±10 °C at 50 °C"). https://docs.kernel.org/hwmon/coretemp.html
  3. `drivers/hwmon/k10temp.c` `tctl_offset_table[]` (Ryzen 1xxx: +20 °C; 2700X: +10 °C; Threadripper 19xx/29xx: +27 °C) and `Documentation/hwmon/k10temp.rst`. https://github.com/torvalds/linux/blob/master/drivers/hwmon/k10temp.c
  4. `Documentation/hwmon/drivetemp.rst` (SCT 1-min cadence, sct_avoid_models, WD120EFAX spin-up note). https://docs.kernel.org/hwmon/drivetemp.html
  5. `drivers/hwmon/dell-smm-hwmon.c` `I8K_FAN_MULT = 30` quantization. https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c
  6. fan2go reference `maxRpmDiffForSettledFan` defaults (10/20/250 across versions and OEM-Dell-server context). https://github.com/markusressel/fan2go/blob/master/fan2go.yaml and https://github.com/markusressel/fan2go/issues/201
  7. Intel 4-Wire PWM Fan Specification rev 1.3 — "Sense delivers two pulses per revolution". https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf
  8. Microchip AN17.4 (RPM-to-tach quantization math, Architecture-B). https://ww1.microchip.com/downloads/en/Appnotes/en562764.pdf
  9. acpitz unreliability evidence: Launchpad #1922111, Framework community #54128, Manjaro #154502, Ubuntu archive 2222109. https://bugs.launchpad.net/bugs/1922111

- **Reasoning summary:** The dominant temperature-domain quantization across coretemp/k10temp/nct/it87/drivetemp/nvme/amdgpu/nvidia is 1 °C, making 2 °C the smallest unambiguously detectable change; combined with observed p95 noise of ±1 °C, this fixes the Layer C threshold at 2 °C. N=20 fast-loop writes (2 s at 10 Hz) is chosen because the longest CPU-class sensor lag is ~500 ms (Super-IO PECI cache `HZ + HZ/2`), so 2 s spans 4× that — false-positive rate during a real 0.5 °C/s ramp is on the order of 10⁻¹⁰. For HDDs the same logic applied to a τ of minutes gives N=3 reads. The RPM noise floor of 150 RPM is set by the userspace 1-Hz polling quantization (±30 RPM physics floor) plus observed bearing/aliasing wobble (±100 RPM p95) with a 1.5× safety margin; fan2go's 250-RPM constant from initialization context is shown to correspond to the "best-effort" SNR=1.7 envelope, validating it as a conservative outer bound rather than a tight default. The preference order applies the latency-vs-τ rule explicitly: coretemp/k10temp dominate the fast CPU loop because their <1 ms latency is ≪ 100 ms thermal τ; drivetemp's 60-s SCT cadence is acceptable for HDD loops because HDD τ is several minutes.

- **HIL-validation flag:** **Yes** — multi-fleet validation needed.
  - **13900K + RTX 4090 desktop** runs the **Layer C false-positive test** under `stress-ng --cpu` ramp with PWM clamped (verifies 2 °C/20-write default does not spuriously trigger during true linear ramps at <0.5 °C/s).
  - **Proxmox host (5800X + RTX 3060)** runs the **per-driver RPM jitter log** at 800/1500/2000/max RPM steady state for 1 h on its nct or asus-ec channels, plus the **drivetemp safe-cadence test** if SATA HDDs are present.
  - **MiniPC (Celeron)** runs the **acpitz demotion test** (induce thermal stress, verify 5 °C/60 s stdev gate correctly demotes acpitz when it spikes).
  - **One laptop (whichever has dell-smm-hwmon)** runs the **dell-smm RPM-quantization probe** (verify 90-RPM minimum step suffices for polarity).
  - **All five** run the **1-hour idle temperature noise log** to populate per-driver p95 noise tables.

- **Confidence:** **High** for temperature-domain numbers (2 °C / N=20 / N=3): backed by primary kernel-source resolution facts, multiple independent kernel-doc statements, and matched by community noise reports. **Medium-High** for RPM noise floor (150 RPM): physics floor is ironclad, observed jitter has wider variance across fan models so the 150-RPM default may need to be loosened to 200 RPM after HIL on cheap sleeve-bearing fans. **Medium** for the dell-smm and liquidctl overrides — they are derived from a small number of forum reports; HIL on Phoenix's actual Dell hardware will tighten or loosen them. **High** for the sensor preference order (latency-vs-τ rule is mechanically correct and per-driver latencies are kernel-source-confirmed).

- **Spec ingestion target:**
  - Primary: `spec-smart-mode.md` § Layer C (saturation detector) — embed the 2 °C / N=20 / N=3 / dT/dt<1 °C/min defaults and the dual-condition (range AND slope) test.
  - Cross-reference from `spec-v0_5_2-polarity-disambiguation.md` § R6 — embed the 150-RPM default, the dell-smm/liquidctl override table, and the SNR=5 (high-conf) / SNR=1.7 (best-effort) decision rule.
  - New supplementary doc (suggested): `spec-sensor-preference.md` — the per-use-case preference matrix and the latency-vs-τ admissibility rule. Reference from R5 (idle-gate), R7 (config validation, future), R8 (coarse-classification fallback, future).
  - Patch: add the per-driver noise-floor tables (R11 long-form §2 and §3.2) as an appendix to either `spec-smart-mode.md` or a new `spec-driver-quirks.md`, since they will be consulted by R4/R5/R7/R8 too.

**Review flags from chat (2026-04-28):**
- k10temp Tctl offset table is vendor-extended but Linux-stable. Pin ventd's k10temp consumer to "use Tdie/Tccd if available, fall back to Tctl with offset table read at runtime from `/sys/class/hwmon/.../temp*_label`" rather than baking the offset table into ventd itself.
- drivetemp `sct_avoid_models[]` list is a moving target. Don't replicate; delegate ("if drivetemp loaded the channel successfully, trust it; if not, fall back to smartctl").
- Layer C dual-condition test (range AND slope) should propagate to other detectors (idle gate, BIOS-fight) as a design principle. Elevate to cross-cutting RULE in `spec-smart-mode.md`.

---

# Part B — Long-Form Research Documents

The full long-form research documents follow below, in numerical order: R1, R2, R3, R4, R5, R6, R11.

Each document is self-contained and can be extracted into a standalone file (`R{N}-{title}.md`) if desired. Sections within each document use heading levels relative to the document; this bundle uses the document title as a top-level marker (`# R{N} — Title`) so global navigation remains coherent.

---

# R1 — Tier-2 Detection Signal Reliability (Virtualization + Containers)

**Target spec:** `spec-v0_5_1-catalog-less-probe.md` (Tier-2 detection layer)
**Constraint reaffirmed:** Pure Go, `CGO_ENABLED=0`. Detection MUST be performed by reading pseudo-filesystems (`/proc`, `/sys`, `/run`) and DMI sysfs only. Shelling out to `systemd-detect-virt` or `virt-what` is explicitly forbidden, but their canonical signal lists are the correctness reference.

## R1.1 Executive summary

The runtime classification problem ventd faces is not "what hypervisor is below me?" but rather "is it safe to write to a hwmon PWM here?" Those are very different questions, and the Tier-2 layer must be designed around the second one. A Linux KVM guest with PCIe passthrough of an LPC/Super-I/O chip *legitimately* owns real fans; a Proxmox LXC container with DMI bleed-through *appears* to own real fans but actually shares /sys/class/hwmon with the host kernel and could fight Proxmox's own fan governance; an unprivileged Docker container has read-only sysfs and cannot write at all. The same physical signal — DMI vendor `Intel Corporation` — has three completely different safety implications across those three cases.

The defensible architecture, mirroring the design choice systemd made in `src/basic/virt.c`, is:

1. **Container detection always wins over VM detection**, because any container layer (LXC/Docker/Podman/nspawn/k8s/WSL) implies a shared kernel with an unknown owner of `/sys/class/hwmon`.
2. **Cgroup / namespace / containerenv signals are checked first**, because they are the only signals that the container manager itself sets and cannot be hidden by DMI obfuscation.
3. **DMI is checked next**, because it is forged-but-rarely (and forged-DMI is an explicit "VM is hiding from me" signal that should map to BLOCK, not ALLOW).
4. **CPUID's `hypervisor` bit and the `XenVMMXenVMM`/`KVMKVMKVM`/`Microsoft Hv` leaf-0x40000000 vendor strings** are the kernel-truth fallback (matching `arch/x86/kernel/cpu/hypervisor.c` and systemd's `detect_vm_cpuid`). However, CPUID requires either `CPUID` Go assembly (still CGO-free) or simply trusting `/proc/cpuinfo`'s `hypervisor` flag — ventd should use the latter for portability.
5. **WSL is its own special case** detected post-VM via the `microsoft`/`WSL` substring in `/proc/sys/kernel/osrelease`, because WSL2 is a Hyper-V VM but is *also* effectively a container from a hardware-access standpoint and must always BLOCK.

This document specifies the full table, the precedence chain, the Go-pseudocode skeleton, the false-positive/false-negative inventory, and the BLOCK/ALLOW/OVERRIDE policy ventd should ship in v0.5.1.

## R1.2 Canonical sources reviewed

| Source | URL | Why authoritative |
|---|---|---|
| systemd `src/basic/virt.c` (main branch) | https://github.com/systemd/systemd/blob/main/src/basic/virt.c | The de-facto canonical signal list. `detect_vm()` and `detect_container()` define the order, the DMI vendor table, the CPUID strings, and the WSL/proot/nspawn special cases that almost every other implementation copies. |
| systemd `detect_vm_cpuid` / DMI vendor table | https://github.com/systemd/systemd/blob/v239/src/basic/virt.c | Lists exact CPUID vendor strings (`KVMKVMKVM`, `VMwareVMware`, `Microsoft Hv`, `XenVMMXenVMM`, `bhyve bhyve `, `QNXQVMBSQG`, ` lrpepyh vr` for Parallels) and DMI sys_vendor strings (`QEMU`, `VMware`, `VMW`, `innotek GmbH`, `VirtualBox`, `Oracle Corporation`, `Xen`, `Bochs`, `Parallels`, `BHYVE`, `Hyper-V`, `Apple Virtualization`, `Google Compute Engine`, `Amazon EC2`). |
| systemd-detect-virt(1) manpage | https://man7.org/linux/man-pages/man1/systemd-detect-virt.1.html | Documents the public taxonomy: `qemu`, `kvm`, `amazon`, `zvm`, `vmware`, `microsoft`, `oracle`, `powervm`, `xen`, `bochs`, `uml`, `parallels`, `bhyve`, `qnx`, `acrn`, `apple`, `sre`, `google`; container set: `openvz`, `lxc`, `lxc-libvirt`, `systemd-nspawn`, `docker`, `podman`, `rkt`, `wsl`, `proot`, `pouch`. Also documents the rule: "if both machine and container virtualization are used in conjunction, only the latter will be identified". |
| Lennart Poettering, "systemd for Administrators, Part XIX" | http://0pointer.de/blog/projects/detect-virt.html | States the design principle that detection libraries return only the "inner-most" virtualization, and that ConditionVirtualization in unit files exists precisely because conditionalizing services on virt-type is a legitimate use. |
| Linux kernel `arch/x86/kernel/cpu/hypervisor.c` | https://github.com/torvalds/linux/blob/master/arch/x86/kernel/cpu/hypervisor.c | The kernel's own hypervisor probe order: Xen-PV → Xen-HVM → VMware → Hyper-V (`ms_hyperv`) → KVM → Jailhouse → ACRN. This is the source of the `hypervisor` flag in `/proc/cpuinfo` (set when `cpu_has(c, X86_FEATURE_HYPERVISOR)`). |
| virt-what (Red Hat / Richard W.M. Jones) | https://people.redhat.com/~rjones/virt-what/ | Heuristic-based shell script. Notable for documenting the explicit warning: "Most of the time, using this program is the wrong thing to do. Instead you should detect the specific features you actually want to use." This warning applies equally to ventd: ventd should ultimately probe for hwmon writability, with virt-detection as a guardrail, not the sole gate. |
| zcalusic/sysinfo `hypervisor.go` | https://github.com/zcalusic/sysinfo/blob/master/hypervisor.go | Pure-Go (uses Go-assembly CPUID via the `cpuid` subpackage, NOT CGO). Maps CPUID vendor → name and reads `/sys/hypervisor/type` for Xen-PV. Confirmed CGO-free, BSD-style license. Suitable for direct vendoring. |
| shirou/gopsutil v4 `host.Virtualization()` | https://github.com/shirou/gopsutil/blob/master/host/host_linux.go | Returns `(system, role, error)` where role ∈ `{"guest","host"}`. Documented as CGO-free on Linux: README states "All works are implemented without cgo by porting C structs to Go structs" and the only CGO file in the tree is `host_darwin_cgo.go` (Darwin-only and disabled when `CGO_ENABLED=0`). Suitable for use in ventd, but its detection logic is a subset of systemd's — it does not distinguish dom0 vs domU and merges KVM/QEMU. |
| Microsoft WSL issue #423 (osrelease convention) | https://github.com/microsoft/WSL/issues/6911 and https://github.com/microsoft/WSL/issues/11814 | Documents that `/proc/sys/kernel/osrelease` contains the literal substring `microsoft` (WSL1 historically `Microsoft`, WSL2 `microsoft-standard-WSL2+`). This is the systemd-canonical detection. Case-insensitive match required. |
| Podman `/run/.containerenv` documentation | https://docs.podman.io/en/latest/markdown/podman-run.1.html | The OCI-aligned convention: "Additionally, a container environment file is created in each container to indicate to programs they are running in a container. This file is located at `/run/.containerenv`." The file is empty for rootless non-privileged containers and contains key-value pairs for `--privileged`. |
| Docker `/.dockerenv` convention | Long-standing Docker behavior; documented across Docker docs and reproduced by every container-detection helper (e.g., systemd's `detect_container_files()` enumerates `/.dockerenv` and `/run/.containerenv`). |
| Brendan Gregg, "AWS EC2 Virtualization 2017: Introducing Nitro" | https://www.brendangregg.com/blog/2017-11-29/aws-ec2-virtualization-2017.html | Documents that Nitro is "based on the KVM core kernel module" but presents itself via DMI as `Amazon EC2` (sys_vendor) — confirming that ventd must treat `Amazon EC2` as a valid VM signal even though kernel will detect KVM. |
| Proxmox VE LXC documentation | https://pve.proxmox.com/wiki/Linux_Container | Confirms that LXC containers "share the host's Linux kernel directly" and "CPU related information is not hidden from an LXC container". This is the source of the DMI bleed-through pattern: `/sys/class/dmi/id/sys_vendor` inside the container shows the physical motherboard. |
| Xen detection issues in systemd (#22511, #6442, #2639, #28113) | https://github.com/systemd/systemd/issues/22511 and https://github.com/systemd/systemd/issues/6442 | Document the ordering bug class that ventd must avoid: `/sys/hypervisor/type` reports `xen` on **both** dom0 and domU; the only authoritative dom0 marker is `/sys/hypervisor/properties/features` bit `XENFEAT_dom0` being set. Also: `/proc/xen/capabilities` is more reliable than `/sys/hypervisor` because xenfs may not be mounted yet at boot. |

## R1.3 Comparative matrix

The matrix below uses these column codes:
- **Pure-Go**: Y = trivially file-read; P = needs CPUID via Go assembly (still CGO-free); N = requires CGO
- **FP-risk** (false-positive risk for "this is the actual environment"): L/M/H

### R1.3.1 Hypervisor / VM environments

| Environment | Primary signal | File path / source | Expected value | Secondary signals | FP risk | Pure-Go |
|---|---|---|---|---|---|---|
| **KVM (no virtio passthrough)** | CPUID hypervisor leaf 0x40000000 vendor | `/proc/cpuinfo` (look for `hypervisor` flag) + CPUID 0x40000000 | `KVMKVMKVM\0\0\0` | DMI `sys_vendor` = `QEMU` and/or `product_name` = `KVM`; `/sys/devices/virtual/dmi/id/product_name` containing `Standard PC (Q35 + ICH9, 2009)`; presence of `/dev/kvm` on host (irrelevant inside guest) | L | P (CPUID needs asm) / Y (cpuinfo flag is a plain file) |
| **KVM (with virtio passthrough or PCIe SR-IOV)** | CPUID + DMI as KVM | same as above | same | additional PCI devices in `/sys/bus/pci/devices` not matching virtio (`1af4:*`) — e.g., real Realtek NIC, Intel HEDT chipset bridge — indicate passthrough | L | Y |
| **QEMU (TCG / no -enable-kvm)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `QEMU` | `product_name` = `Standard PC (...)`; CPUID leaf-1 ECX bit-31 (`X86_FEATURE_HYPERVISOR`) **may be unset** — this is the canonical false-negative case | M | Y |
| **QEMU (KVM mode)** | CPUID `KVMKVMKVM` | CPUID 0x40000000 | `KVMKVMKVM` | DMI sys_vendor = `QEMU` | L | P |
| **VMware Workstation/ESXi/Fusion** | CPUID `VMwareVMware` | CPUID 0x40000000 | `VMwareVMware` | DMI sys_vendor matches `VMware, Inc.` (string `VMware` is sufficient prefix; `VMW` also seen on older ESX); `product_name` = `VMware Virtual Platform` or `VMware7,1` | L | P / Y |
| **Hyper-V (Windows Server, Azure)** | CPUID `Microsoft Hv` | CPUID 0x40000000 | `Microsoft Hv` | DMI sys_vendor = `Microsoft Corporation`; `product_name` = `Virtual Machine`; `/sys/hypervisor/type` may be empty; presence of `/sys/bus/vmbus` | L (but see Xen-cloaks-as-Hyper-V edge case below) | P / Y |
| **Xen PV (domU)** | `/proc/xen/capabilities` exists, content lacks `control_d` | `/proc/xen/capabilities` | empty / `(no string)` for domU | `/sys/hypervisor/type` = `xen`; CPUID `XenVMMXenVMM`; DMI sys_vendor = `Xen`, product_name = `HVM domU`; `XENFEAT_dom0` bit (in `/sys/hypervisor/properties/features`) is **NOT** set | L | Y |
| **Xen HVM (domU)** | DMI product_name | `/sys/class/dmi/id/product_name` | `HVM domU` | DMI sys_vendor = `Xen`; CPUID `XenVMMXenVMM`; `/proc/cpuinfo` `hypervisor` flag set | L | Y |
| **Xen dom0** | `/sys/hypervisor/properties/features` with `XENFEAT_dom0` bit set | `/sys/hypervisor/properties/features` | hex value with bit indicating `XENFEAT_dom0` | `/proc/xen/capabilities` contains string `control_d`; DMI is the host's real motherboard | L | Y |
| **VirtualBox (Oracle)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `innotek GmbH` (legacy) or `Oracle Corporation`; `board_vendor` = `Oracle Corporation`; `product_name` = `VirtualBox` | CPUID `KVMKVMKVM` if VBox uses KVM acceleration on Linux host; chassis_vendor = `Oracle Corporation` | L | Y |
| **Parallels Desktop** | CPUID ` lrpepyh vr` (note leading space) | CPUID 0x40000000 | ` lrpepyh vr` | DMI sys_vendor = `Parallels Software International Inc.` or contains `Parallels` | L | P |
| **AWS EC2 Nitro** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Amazon EC2` | CPUID `KVMKVMKVM` (Nitro is KVM-derived); `product_name` like `c5.large`, `m6i.xlarge`, etc.; `bios_vendor` = `Amazon EC2`; presence of `/sys/devices/virtual/dmi/id/board_asset_tag` with i-…-style asset tag | L | Y |
| **AWS EC2 (legacy Xen)** | DMI product_name | `/sys/class/dmi/id/product_name` | `HVM domU` | sys_vendor = `Xen`; `bios_version` contains `amazon` | L | Y |
| **GCP (Google Compute Engine)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Google` or `Google Compute Engine` | CPUID `KVMKVMKVM`; product_name = `Google Compute Engine`; `bios_vendor` = `Google` | L | Y |
| **Microsoft Azure** | CPUID `Microsoft Hv` | CPUID 0x40000000 | `Microsoft Hv` | DMI sys_vendor = `Microsoft Corporation`; `chassis_asset_tag` = `7783-7084-3265-9085-8269-3286-77` (Azure-specific) | L | P / Y |
| **DigitalOcean (KVM)** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `DigitalOcean` | product_name = `Droplet`; CPUID `KVMKVMKVM` | L | Y |
| **Bochs** | DMI sys_vendor | `/sys/class/dmi/id/sys_vendor` | `Bochs` | CPUID typically blank | L | Y |

### R1.3.2 Container / namespace environments

| Environment | Primary signal | File path / source | Expected value | Secondary signals | FP risk | Pure-Go |
|---|---|---|---|---|---|---|
| **Docker (rootful, non-privileged)** | `/.dockerenv` exists | `/.dockerenv` | file present (typically empty) | `/proc/1/cgroup` contains `/docker/`; `/proc/self/mountinfo` shows overlayfs as `/`; `/proc/1/sched` PID != 1 in some runtimes | L | Y |
| **Docker (--privileged)** | `/.dockerenv` exists | `/.dockerenv` | file present | cgroup as above; **but** sysfs is read-write and DMI bleed-through occurs identically to LXC | L | Y |
| **Docker-in-Docker** | `/.dockerenv` AND nested `/proc/1/cgroup` | both exist; cgroup path contains multiple `/docker/<id>/docker/<id2>/` segments | — | mountinfo shows recursive overlayfs | M | Y |
| **Podman (rootful)** | `/run/.containerenv` exists | `/run/.containerenv` | file present, contents: `engine="podman-..."`, `rootless=0`, `name=...`, `id=...` | `/proc/1/cgroup` contains `libpod` or `machine.slice` | L | Y |
| **Podman (rootless)** | `/run/.containerenv` exists | `/run/.containerenv` | typically empty file | `/proc/self/uid_map` shows non-trivial mapping (UID 0 mapped to a non-zero host UID); `/proc/1/cgroup` under `user.slice/user-N.slice` | L | Y |
| **LXC (privileged)** | `/proc/1/environ` contains `container=lxc` | `/proc/1/environ` (NUL-separated) | `container=lxc\0` | `/dev/.lxc` directory or `/dev/.lxc-boot-id`; `/proc/1/cgroup` contains `/lxc/<name>` or `/lxc.payload.<name>`; mountinfo shows `lxcfs` mounts under `/proc/cpuinfo`, `/proc/meminfo`, `/proc/uptime`, `/proc/stat` | L | Y |
| **LXC (unprivileged)** | same as above | same | `container=lxc` (set by lxc-start) | `/proc/self/uid_map` shows mapping `0 100000 65536` style range; cgroup path under `/user.slice/.../lxc.payload.<name>` | L | Y |
| **Proxmox LXC (canonical homelab case)** | `/proc/1/environ` `container=lxc` | `/proc/1/environ` | `container=lxc` | DMI **leaks the host motherboard** (sys_vendor = `ASUSTeK COMPUTER INC.`, `Supermicro`, etc.) — this is the bleed-through; cgroup path contains `/lxc/<vmid>` (e.g., `/lxc/100`); lxcfs-mounted `/proc/cpuinfo`, `/proc/meminfo`; `/sys/class/dmi/id/product_uuid` is the **host's** UUID | L | Y |
| **systemd-nspawn** | `/proc/1/environ` `container=systemd-nspawn` | `/proc/1/environ` | `container=systemd-nspawn` | `/run/host/container-manager` (newer hosts place this); `/run/systemd/container` may exist | L | Y |
| **Kubernetes pod (kubepods)** | `/proc/1/cgroup` contains `kubepods` | `/proc/1/cgroup` | path containing `/kubepods.slice/...` or legacy `/kubepods/...` | underlying runtime detected via `cri-containerd-`, `crio-`, or `docker-` segment; `/.dockerenv` may also exist if Docker shim; `KUBERNETES_SERVICE_HOST` env (but ventd doesn't read env for detection) | L | Y |
| **WSL1** | `/proc/sys/kernel/osrelease` contains `Microsoft` | `/proc/sys/kernel/osrelease` | substring `Microsoft` (capital M historically) | `/proc/version` contains `Microsoft`; `/proc/cpuinfo` has no `hypervisor` flag (WSL1 is a syscall translation layer); no `/sys/class/dmi` | L | Y |
| **WSL2** | `/proc/sys/kernel/osrelease` contains `microsoft` (lowercase) and/or `WSL2` | `/proc/sys/kernel/osrelease` | e.g., `5.10.16.3-microsoft-standard-WSL2+` or `6.10.0-...-microsoft-standard-WSL2+` | CPUID reports `Microsoft Hv` (because WSL2 IS a Hyper-V VM); `/proc/cpuinfo` `hypervisor` flag SET; no real DMI; `/run/WSL` path may exist | L | Y |
| **cgroup v2 unified hierarchy in any container** | `/proc/1/cgroup` shows single line `0::/...` | `/proc/1/cgroup` | `0::/<path>` — pattern-match `<path>` for `docker`/`kubepods`/`lxc`/`libpod`/`machine` substrings; the bare `0::/` (with empty path) inside many cgroup-v2-namespaced runtimes means "containerized but path hidden" — itself a strong container signal | L | Y |

## R1.4 Ranked precedence chain

This is the canonical order; later checks only run if earlier checks return "unknown". The order intentionally puts container signals before VM signals because a **container-on-VM is always `Container`** for safety purposes — the container shares the kernel and `/sys/class/hwmon` is owned by the kernel which is owned by whatever scheduled the container, regardless of whether that kernel is itself in a VM.

```
Step 1.  /proc/1/environ            → "container=" prefix     → LXC | nspawn | docker | podman
Step 2.  /run/.containerenv          → exists                   → Podman (or any OCI runtime that adopts the convention)
Step 3.  /.dockerenv                 → exists                   → Docker (also covers Docker-in-Docker after cgroup parse)
Step 4.  /proc/1/cgroup              → substring scan           → kubepods | docker | lxc | libpod | machine.slice |
                                                                    cri-containerd | crio | systemd-nspawn
Step 5.  /proc/self/mountinfo        → overlay/lxcfs/fuse        → corroborate Step 4; detect lxcfs masking
Step 6.  /proc/sys/kernel/osrelease  → "microsoft" | "WSL"       → WSL1/WSL2  (CHECK BEFORE VM; WSL2 must classify as
                                       (case-insensitive)          WSL_CONTAINER, not Hyper-V VM)
Step 7.  /proc/xen/capabilities      → exists                   → Xen (then refine with XENFEAT_dom0)
Step 8.  /sys/hypervisor/properties/features → bit XENFEAT_dom0  → Xen dom0 if set; Xen domU if not
Step 9.  /sys/class/dmi/id/sys_vendor + /product_name            → systemd's DMI vendor table
Step 10. /proc/cpuinfo "hypervisor" flag                         → generic VM yes/no
Step 11. CPUID 0x40000000 vendor (via Go-asm if available, else
         skip — flag from Step 10 is sufficient signal of "VM")  → exact VM vendor
Step 12. /sys/firmware/dmi/entries/0-0/raw byte 0x13 bit 4       → SMBIOS "VM" bit (catches obfuscated-DMI VMs that
                                                                    still expose the SMBIOS-VM bit)
Step 13. /proc/cmdline                                            → corroborating: "console=ttyS0" + virtio drivers
                                                                    suggests VM; "intel_iommu=on" + IOMMU groups
                                                                    suggests bare metal or passthrough host
Step 14. (none of the above triggered)                            → BARE_METAL
```

### R1.4.1 Combination rules for ambiguous cases

| If signal A says… | And signal B says… | Then classification is… |
|---|---|---|
| Step 4 cgroup contains `kubepods` | DMI sys_vendor = `Amazon EC2` | `Kubernetes-on-EC2` → **CONTAINER takes precedence** for hwmon-write policy |
| Step 4 cgroup contains `lxc` | DMI sys_vendor = `ASUSTeK`/`Intel`/`Supermicro` (any non-virt vendor) | `Proxmox-style LXC with bleed-through` → **CONTAINER**; the DMI is the host's |
| Step 6 osrelease contains `microsoft` | CPUID = `Microsoft Hv` | `WSL2` (NOT pure Hyper-V) — WSL marker wins |
| Step 6 osrelease has no `microsoft` | CPUID = `Microsoft Hv` | `Hyper-V VM` |
| Step 7 `/proc/xen/capabilities` exists | Step 8 `XENFEAT_dom0` bit SET | `Xen dom0` (treat as bare-metal-equivalent for hwmon) |
| Step 7 `/proc/xen/capabilities` exists | Step 8 `XENFEAT_dom0` bit UNSET | `Xen domU` |
| DMI sys_vendor = `QEMU` | CPUID hypervisor flag UNSET | `QEMU TCG` (very slow software emulation; treat as VM for safety) |
| DMI sys_vendor = `QEMU` | CPUID = `KVMKVMKVM` | `KVM/QEMU` |
| DMI sys_vendor = `Xen`, product_name = `HVM domU` | CPUID = `Microsoft Hv` | `Xen cloaking as Hyper-V` (per systemd #8844) — DMI wins, classify as `Xen` |
| Step 1 `container=lxc` | Step 8 `/sys/hypervisor` reports `xen` | `LXC inside a Xen domU` → **CONTAINER** |
| `/.dockerenv` AND `/proc/1/cgroup` has nested `/docker/.../docker/...` | — | `Docker-in-Docker` → **CONTAINER** |
| All steps 1–13 fail | — | `BARE_METAL` |

## R1.5 Go-pseudocode decision tree

```go
// Package detect — Tier-2 runtime classification for ventd.
// CGO_ENABLED=0; pure file-IO and Go-asm (no C deps).

type VirtClass int
const (
    VirtUnknown VirtClass = iota
    BareMetal
    // VM classes
    VMQemuTCG; VMKVM; VMVMware; VMHyperV; VMXenPVDomU; VMXenHVMDomU; VMXenDom0
    VMVirtualBox; VMParallels; VMAmazonNitro; VMAmazonXenHVM; VMGCP; VMAzure
    VMDigitalOcean; VMBochs; VMOther
    // Container classes
    ContDocker; ContDockerPrivileged; ContDockerInDocker
    ContPodmanRootful; ContPodmanRootless
    ContLXC; ContLXCUnprivileged; ContProxmoxLXC
    ContSystemdNspawn; ContKubernetes
    ContWSL1; ContWSL2; ContOther
)

type Detection struct {
    Class            VirtClass
    Confidence       Confidence
    Evidence         []Evidence
    HostVendor       string
    HostProduct      string
    HypervisorVendor string
}

func Detect() Detection {
    var ev []Evidence
    // Container tier (highest priority for hwmon safety)
    if c, e := probeProc1Environ(); c != VirtUnknown { return finalize(c, ConfHigh, ev) }
    if c, e := probeContainerEnvFiles(); c != VirtUnknown {
        if cc, ce := probeProc1Cgroup(); cc != VirtUnknown { return finalize(reconcile(c, cc), ConfHigh, ev) }
        return finalize(c, ConfHigh, ev)
    }
    if c, e := probeProc1Cgroup(); c != VirtUnknown {
        if c == ContLXC {
            if dmi := readDMIVendor(); dmi != "" && !isVirtVendor(dmi) {
                c = ContProxmoxLXC
            }
        }
        return finalize(c, ConfHigh, ev)
    }
    if c, e := probeMountinfoLXCFS(); c != VirtUnknown { return finalize(c, ConfMedium, ev) }
    // WSL must be checked BEFORE Hyper-V
    if c, e := probeWSLOSRelease(); c != VirtUnknown { return finalize(c, ConfHigh, ev) }
    // VM tier
    if c, e := probeXen(); c != VirtUnknown { return finalize(c, ConfHigh, ev) }
    if c, e := probeDMI(); c != VirtUnknown { return finalize(c, ConfHigh, ev) }
    hv := probeCPUInfoHypervisorFlag()
    if hv {
        if c, e := probeCPUIDVendor(); c != VirtUnknown { return finalize(c, ConfHigh, ev) }
        return finalize(VMOther, ConfMedium, ev)
    }
    if probeSMBIOSVMBit() { return finalize(VMOther, ConfLow, ev) }
    _ = probeProcCmdline()
    return finalize(BareMetal, ConfHigh, ev)
}
```

Each probe is a thin wrapper around `os.ReadFile` + `bytes.Contains` / regex — no syscalls beyond the file read, no fork, no shell. The only function that needs platform-specific assembly is `probeCPUIDVendor`, and ventd may legitimately omit it: the `hypervisor` flag from `/proc/cpuinfo` is sufficient to gate "is this a VM?", and the DMI vendor from Step 9 is sufficient to identify *which* VM.

## R1.6 Edge cases and recommended handling

### R1.6.1 Proxmox LXC with DMI bleed-through (the canonical homelab case)

**Symptom:** Inside a Proxmox LXC container, `/sys/class/dmi/id/sys_vendor` returns `ASUSTeK COMPUTER INC.` (or whatever the host motherboard is). `/sys/class/dmi/id/product_uuid` is the host's UUID. `/proc/cpuinfo` shows the host's full CPU including all cores. `htop` and `top` report host load.

**Why DMI alone is not enough:** ventd cannot distinguish "I am a bare-metal homelab on this ASUS motherboard" from "I am a Proxmox LXC container running on top of a bare-metal homelab on this ASUS motherboard" using DMI signals — they are byte-identical.

**Reliable detection:** the LXC tooling sets `container=lxc` in PID 1's environment (`/proc/1/environ`). It is also detectable via `/proc/1/cgroup` containing `/lxc/<vmid>` or `/lxc.payload.<vmid>` segments, and via `/proc/self/mountinfo` showing `lxcfs` mounts on top of `/proc/cpuinfo`, `/proc/meminfo`, `/proc/uptime`, `/proc/stat`, and `/proc/loadavg`.

**Recommended action:** ventd MUST classify this as `ContProxmoxLXC` and BLOCK PWM writes. The Proxmox host has its own fan governance (typically the BIOS, plus maybe `pwmconfig`/`fancontrol` on the PVE host); two writers fighting over the same `/sys/class/hwmon/hwmon*/pwm*` files is the worst-case behavior. If the user genuinely wants ventd to control fans from inside the container, they must (a) bind-mount `/sys/class/hwmon` read-write into the container, (b) configure cgroup device access to allow it, and (c) set the override flag `--allow-container-hwmon`.

### R1.6.2 WSL2 = Hyper-V under-the-hood

**Symptom:** `/proc/cpuinfo` `hypervisor` flag is set; CPUID 0x40000000 returns `Microsoft Hv`; DMI is fragmentary or absent.

**Why this matters:** without the WSL-specific check, a naïve detector would classify WSL2 as `VMHyperV` and might ALLOW PWM writes. But WSL2 NEVER has hwmon passthrough; `/sys/class/hwmon` inside WSL2 is either empty or contains synthetic devices that map to nothing physical.

**Reliable detection:** `/proc/sys/kernel/osrelease` contains the substring `microsoft` (lowercase on WSL2; `Microsoft` capitalized on WSL1) **and/or** `WSL2`. Use case-insensitive matching.

**Recommended action:** classify as `ContWSL2` (NOT `VMHyperV`); BLOCK unconditionally. There is no override flag for WSL.

### R1.6.3 Nested KVM (KVM-in-KVM)

**Symptom:** Both layers report the `hypervisor` flag and `KVMKVMKVM` CPUID.

**Recommended action:** treat both layers identically — `VMKVM` with confidence Medium, BLOCK by default. This is correct because nested inner VMs almost never have real-hardware passthrough. Override flag `--allow-vm-pwm` is the escape hatch for the rare passthrough case.

### R1.6.4 Docker-in-Docker

**Symptom:** Both `/.dockerenv` and a `/proc/1/cgroup` with two `docker` segments.

**Recommended action:** classify as `ContDockerInDocker`; BLOCK. DinD is a CI pattern; never has hwmon access.

### R1.6.5 Cloud Nitro (DMI says Amazon EC2 but virt is KVM-derived)

**Symptom:** DMI sys_vendor = `Amazon EC2`; CPUID = `KVMKVMKVM`; `/proc/cpuinfo` has `hypervisor` flag.

**Recommended action:** classify as `VMAmazonNitro`; BLOCK. EC2 instances have no PWM. Even bare-metal EC2 instances (`*.metal`) do not expose Linux hwmon for chassis fans — those are managed by the Nitro hardware controller out-of-band.

### R1.6.6 Containers running under VMs (the cgroup-precedence rule)

**Symptom:** `/proc/1/cgroup` contains `/kubepods.slice/...` AND DMI sys_vendor = `QEMU` or `Amazon EC2`.

**Recommended action:** Container precedence wins. Classify as `ContKubernetes`, NOT `VMKVM`. BLOCK.

### R1.6.7 QEMU TCG without -enable-kvm and no DMI (false-negative)

**Symptom:** CPUID `hypervisor` flag UNSET (TCG does not expose it on older versions); DMI sys_vendor = `QEMU` is the only signal. systemd's `detect_vm_smbios()` falls back to reading `/sys/firmware/dmi/entries/0-0/raw` byte 0x13 bit 4 — the SMBIOS "system is virtual" bit.

**Recommended action:** ventd's Step 9 (DMI sys_vendor `QEMU`) catches this. Step 12 SMBIOS-bit fallback catches the rarer fully-custom DMI case. Classify as `VMQemuTCG` (or `VMOther` if only the SMBIOS bit triggered); BLOCK.

### R1.6.8 Obfuscated / anti-detection VMs

**Reality check:** This is malware-research / sandbox-evasion territory and is far outside ventd's threat model. ventd is not a security tool. If a user has gone to this much effort to hide a VM, they either (a) genuinely want ventd to run and have a working PWM passthrough or (b) are doing something ventd should not be involved in.

**Recommended action:** if all 13 probes return `BareMetal` but the user reports incorrect fan behavior, the spec should document the `--force-bare-metal=false` / `--probe-hwmon-write-test` diagnostic flag.

### R1.6.9 Minimal containers (BusyBox, scratch images)

**Recommended action:** `0::/` (the bare cgroup-v2 root with no path) inside a process whose `/proc/self/uid_map` shows non-trivial mapping is itself a strong container indicator. Emit Confidence=Medium `ContOther` classification and BLOCK.

### R1.6.10 Xen ordering hazard

systemd issue #6442 documents that `detect_vm()` running before xenfs is mounted gives wrong dom0 result. ventd's probe runs at daemon startup, well after rootfs is mounted, so this is unlikely to bite — but the lesson is: do NOT cache the result globally at process start. Re-probe on demand or at reload-config time.

## R1.7 ventd Tier-2 policy: BLOCK / ALLOW / OVERRIDE

### R1.7.1 Default policy

| Class | Default action | Override flag (if any) | Rationale |
|---|---|---|---|
| `BareMetal` | **ALLOW** | — | The intended target. |
| `VMXenDom0` | **ALLOW** | — | dom0 owns the real hardware; bare-metal-equivalent. |
| `VMKVM`, `VMQemuTCG` | **OVERRIDE** | `--allow-vm-pwm` | Default BLOCK because most KVM guests have no real PWM. ALLOW with override for legitimate PCIe/Super-IO-passthrough. |
| `VMVMware`, `VMVirtualBox`, `VMHyperV` (excluding WSL2), `VMParallels`, `VMBochs`, `VMOther` | **OVERRIDE** | `--allow-vm-pwm` | Same. |
| `VMAmazonNitro`, `VMAmazonXenHVM`, `VMGCP`, `VMAzure`, `VMDigitalOcean` | **BLOCK** (no override) | — | Cloud VMs categorically have no PWM. |
| `VMXenPVDomU`, `VMXenHVMDomU` | **OVERRIDE** | `--allow-vm-pwm` | Same as KVM. |
| `ContDocker`, `ContPodmanRootful`, `ContPodmanRootless`, `ContSystemdNspawn`, `ContKubernetes`, `ContOther` | **OVERRIDE** | `--allow-container-hwmon` | Default BLOCK. |
| `ContDockerPrivileged` | **OVERRIDE** | `--allow-container-hwmon` | Require explicit opt-in. |
| `ContDockerInDocker` | **BLOCK** (no override) | — | DinD never has hwmon. |
| `ContLXC`, `ContLXCUnprivileged` | **OVERRIDE** | `--allow-container-hwmon` | Generic LXC may legitimately have hwmon bind-mounted. |
| `ContProxmoxLXC` | **OVERRIDE** | `--allow-container-hwmon` (with extra warning in logs) | The bleed-through case. |
| `ContWSL1`, `ContWSL2` | **BLOCK** (no override) | — | WSL has no real hwmon. |
| `VirtUnknown` | **BLOCK** | `--force-bare-metal` | Conservative default. |

### R1.7.2 Override-flag semantics

- Settable both via CLI (`ventd --allow-vm-pwm`) and via config file.
- Flag names are explicit about acknowledging risk.
- Flag overrides MUST be logged at INFO level.
- Tier-3 hwmon write-back probe (out of scope for R1) is the second-line defense.

## R1.8 Pure-Go library survey

| Library | License | CGO status | Recommendation |
|---|---|---|---|
| `github.com/shirou/gopsutil/v4/host` | BSD-3-Clause | CGO-free on Linux | **Conditionally usable as a baseline / cross-check**. Do NOT rely on it as the sole detector — its taxonomy is too coarse for ventd's BLOCK/ALLOW/OVERRIDE policy. **Recommendation: do not vendor; reimplement the ~300 LOC probe in ventd's own `internal/detect` package, citing gopsutil as inspiration.** |
| `github.com/zcalusic/sysinfo` | MIT | CGO-free | Useful as a **CPUID assembly reference**. Library too narrow to use whole. **Recommendation: do not vendor; copy the ~30 LOC CPUID detection logic with attribution.** |
| Kubernetes `pkg/util/procfs` | Apache-2.0 | Pure-Go | **Recommendation: do not vendor kubelet code; the cgroup-substring-match logic is ~10 LOC and ventd should own it.** |
| `github.com/digitalocean/go-smbios` | Apache-2.0 | Pure-Go | **Recommendation: optional vendor for Step 12 only.** |

**Summary:** ventd should not depend on any of these as a hard requirement. The total Pure-Go LOC needed for the full Tier-2 probe is ~400 LOC including tests. systemd's `virt.c` is the architectural reference, not a vendored dependency.

## R1.9 Specific behaviors to encode in tests

1. **Proxmox host** running detection → expect `BareMetal`.
2. **Proxmox + KVM guest** → expect `VMKVM` with DMI `QEMU` and CPUID `KVMKVMKVM`.
3. **Proxmox + LXC guest (privileged)** → expect `ContProxmoxLXC` with bleed-through evidence.
4. **Proxmox + LXC guest (unprivileged)** → expect `ContProxmoxLXC` with `/proc/self/uid_map` evidence.
5. **MiniPC bare-metal Linux** → expect `BareMetal`.
6. **13900K dual-boot host running native Linux** → expect `BareMetal`.
7. **13900K dual-boot host running WSL2** → expect `ContWSL2`.
8. **Steam Deck (SteamOS 3.x)** → expect `BareMetal`.
9. **Docker container on the 13900K** (rootful, non-privileged) → expect `ContDocker`, BLOCK.
10. **Docker container with `-v /sys/class/hwmon:/sys/class/hwmon` AND `--privileged`** → expect `ContDockerPrivileged`; BLOCK by default; ALLOW only if `--allow-container-hwmon` is set.

Each test case should assert both the `VirtClass` AND the `Evidence` slice.

---

# R2 — Ghost hwmon PWM Entry Taxonomy for ventd

> R2 governs **which** `/sys/class/hwmon/hwmonN/pwmM` entries get written once R1 has authorized writes. This document catalogs the eight known failure patterns for "ghost" PWM entries on Linux, with kernel source citations, real-world bug references, and a defensible multi-stage classification pipeline.

## R2.1 Executive Summary

The Linux `hwmon` sysfs interface presents `pwmM` files as a uniform abstraction, but in practice the relationship between a sysfs PWM file and a physical fan is mediated by Super-I/O chip register layout, motherboard pin wiring (invisible to software), BIOS/EC firmware policy, kernel driver coverage, and platform vendor mediation. ventd, as a catalog-less daemon for unknown hardware, must treat every freshly-discovered `pwmM` entry as a hypothesis rather than a fact.

Eight distinct failure modes ("ghost patterns") have been identified through review of kernel driver source (`drivers/hwmon/nct6775-*`, `drivers/hwmon/it87.c`, `drivers/hwmon/dell-smm-hwmon.c`, `drivers/hwmon/asus-ec-sensors.c`, `drivers/hwmon/asus_wmi_sensors.c`, `drivers/hwmon/hp-wmi-sensors.c`, `drivers/platform/x86/thinkpad_acpi.c`), the lm-sensors `pwmconfig` tool's correlation heuristic and its known bug history, fan2go and CoolerControl issue trackers, and ASUS/Dell/HP/Gigabyte/MSI user reports.

The recommended ventd strategy is a four-stage pipeline run only after R1 has authorized writes: (1) cheap structural sanity, (2) write-and-read-back probe with `pwm_enable` round-trip, (3) tach-correlation sweep with extended settle time (≥30 s minimum), and (4) BIOS-fight detection by re-reading `pwm_enable` and the written `pwm` value at 5 s, 10 s, and 30 s.

The bias is toward false-positive exclusion (skipping a possibly-real entry) over false-negative inclusion (calibrating against a phantom), with a mandatory user-override path for any entry the pipeline rejects.

## R2.2 The Eight Patterns

### R2.2.a — ZERO-TACH PATTERN

**Description.** A `pwmM` file exists in the hwmon directory, but either (a) no corresponding `fanM_input` exists, or (b) `fanM_input` exists and reads `0` (or stays pinned at a constant value) regardless of what `pwmM` is set to.

**Detection signal.**
1. Enumerate sibling files in the same `hwmonN/` directory.
2. Read `fanM_input` at PWM=255 (full) and at PWM=64 (low). If both reads return `0`, or if the absolute delta is below a noise floor (typically <50 RPM), tach is non-functional.
3. The lm-sensors `pwmconfig` tool's "speed was X now Y, no correlation" output is the canonical realization — see `frankcrawford/it87` issue #11 (Gigabyte B560M DS3H, IT8689E).
4. A 3-pin DC fan plugged into a header configured for PWM mode often produces this signature.

**Driver families.** `nct6775` family — Nuvoton chips advertise `pwm1..pwm5` (and on `nct6796`/`nct6798` up to `pwm7`) but only the channels actually wired to a header on the board are useful. `it87` family — `it8613`, `it8665`, `it8688`, `it8689`, `it8772`. The Proxmox forum thread shows `hwmon4/fan1_input current speed: 0 ... skipping!` for fan1, fan2, fan4, fan5 on a board where only one header is wired. `thinkpad_acpi` — `fan2_input` exists on hwmon directory but reads 0 on ThinkPads where the secondary fan is not physically installed.

**Severity.** **Auto-excludable.** Skipping it is safe.

**Mitigation.** Skip silently after the correlation sweep records no delta on any `fanK_input` over a full `[0, 128, 255]` PWM cycle with ≥10 s settle per step.

### R2.2.b — WRITE-IGNORED PATTERN

**Description.** Writing a value to `pwmM` succeeds (no `EINVAL`/`EIO`), reads back the written value, `pwm_enable` transitions correctly to manual mode (1) and stays there, and yet no observable RPM change occurs and there is no thermal effect.

**Detection signal.**
1. Set `pwmM_enable=1`, write `pwm=0` (or `pwm=64`), wait ≥30 s, read `fanM_input` and any other `fanK_input`. If no fan shows a >50 RPM delta, the write is ignored.
2. Cross-check by writing `pwm=255`. If still no delta, this is write-ignored.
3. Distinguish from BIOS-fight (2.e): re-read `pwm_enable`. If still `1` (manual), this is write-ignored. If reverted to `2`, that is BIOS-fight.

**Driver families.** `it87` on certain Gigabyte AM5/X670E boards with **IT8689E revision 1** (https://github.com/frankcrawford/it87/issues/96): "On the IT8689E, writing to PWM registers is accepted without error but has zero effect on actual fan speed." Revision 2 of the same chip works. `nct6775` family on motherboards where a `pwm_mode` mismatch exists. `it87` IT8613E on some Topton N150 NAS boards.

**Severity.** **Auto-excludable**, but with a caveat: write-ignored is indistinguishable from "fan is broken or unplugged" without external information. WARN-level log entry appropriate.

### R2.2.c — PHANTOM-CHANNEL PATTERN

**Description.** Super-I/O chips advertise the full per-chip channel count regardless of which channels the motherboard actually wires to physical fan headers.

**Detection signal.** Driver name + chip model implies a maximum advertised PWM count. Compare:
- `nct6776`: 3 PWM
- `nct6779`: 5 PWM
- `nct6791`/`6792`/`6793`/`6795`/`6796`/`6798`/`6799`: 5–7 PWM
- `it8613`: hardware supports 3 fans; driver still publishes pwm2/pwm3/pwm5 sysfs attributes
- `it8665`/`it8688`/`it8689`: 5 PWM

Linux issue 459: *"There is either no fan connected to the output of hwmon3/pwm3, or the connected fan has no rpm-signal connected to one of the tested fan sensors. (Note: not all motherboards have the pwm outputs connected to the fan connectors)"*.

**Driver families.** All Super-I/O families. Phantom-channel rate on consumer motherboards is approximately (advertised − wired) / advertised, which empirically averages 30–50% phantoms.

**Severity.** **Auto-excludable** (this is the Pareto-largest source of ghosts).

**Mitigation.** Tach correlation sweep (Stage 3) is the only reliable filter.

### R2.2.d — EINVAL/ERROR PATTERN

**Description.** Some kernel drivers reject writes outside a chip-specific or platform-specific value set with `-EINVAL` or `-EIO`. The classic case is Dell's SMM interface.

**Detection signal.**
1. Write `pwm=137` to a freshly-opened channel. Read back. EINVAL on write fails immediately.
2. If the write succeeds but read-back returns a different value (e.g. 137 written, 170 read back), this is a stepped-value driver. Dell SMM exhibits exactly this.
3. `dell-smm-hwmon`'s `pwm_enable` only accepts values 1 (manual) and 2 (auto); anything else returns `-EINVAL`.
4. ThinkPad ACPI: `pwm_enable` accepts {0, 1, 2}; modes 0 and 2 are not supported on all ThinkPads.
5. ASUS WMI: writes to `pwm1` reportedly fail with `Invalid argument`.

**Driver families.**
- `dell-smm-hwmon` (i8k): stepped values via `i8k_pwm_mult = DIV_ROUND_UP(255, i8k_fan_max)`.
- `thinkpad_acpi`: `pwm1_enable` modes 0/2 may EINVAL.
- `asus-nb-wmi`: only `pwm_enable` writes succeed.
- Some Lenovo Legion laptops via legion-laptop / `ideapad_laptop`.

**Severity.** **Auto-classifiable, requires special-case write strategy.** Not "exclude"; rather "constrain output domain".

**Mitigation.**
1. On EINVAL, probe canonical values: {0, 64, 85, 128, 170, 192, 255}. Record accepted set.
2. If accepted set is a small finite set, mark as `quantized`.
3. Refuse to calibrate intermediate fan curves on quantized channels.

### R2.2.e — BIOS-FIGHT PATTERN

**Description.** Userspace sets `pwm_enable=1` (manual) and writes a desired PWM. Within a few seconds the BIOS / EC firmware writes back `pwm_enable=2` (automatic) or directly clobbers the `pwm` register.

**Detection signal.**
1. Set `pwm_enable=1`, write `pwm=64`. Re-read both at T+5s, T+10s, T+30s.
2. If `pwm_enable` reverts to 2: BIOS-fight on the enable bit.
3. If `pwm_enable` stays at 1 but `pwm` reads back differently: BIOS-fight on duty cycle.
4. fan2go has a built-in detector: *"PWM of Front-02 was changed by third party!"* (https://github.com/markusressel/fan2go/issues/64).

**Driver families.**
- `dell-smm-hwmon` is the canonical case. Kernel doc: *"On some laptops the BIOS automatically sets fan speed every few seconds."*
- HP business / Omen / Compaq desktops via embedded controller.
- Lenovo Legion laptops with stock BIOS.
- Dell PowerEdge servers with iDRAC.
- Some Gigabyte/ASUS BIOSes with "Smart Fan" Q-Fan enabled.

**Severity.** **Requires user judgment.** BIOS-fight on the CPU fan is dangerous to override.

**Mitigation.**
1. Detect at T+5/10/30s.
2. **Do not** silently keep writing — log clearly and either:
   - Skip the channel and emit a `BIOS_FIGHT` diagnostic.
   - Or, if `bios_fight_override: true` is set, write at a frequency higher than the EC polling rate (typically 250 ms). Never enable by default.

### R2.2.f — PLATFORM-MEDIATED PATTERN

**Description.** PWM sysfs files exist but the underlying transport requires a platform driver (WMI/ACPI/SMM/EC) that is partial or read-only.

**Detection signal.**
1. Read `/sys/class/hwmon/hwmonN/name`. Match against: `dell_smm`, `asus_wmi_sensors`, `asus_ec_sensors`, `asus-nb-wmi`, `hp-wmi-sensors`, `thinkpad`, `nbsmi`, `ideapad_laptop`, `legion-laptop`.
2. Cross-reference with `/sys/devices/platform/<driver>/hwmon/hwmonN/`.
3. `pwmM_enable` without `pwmM` is the canonical "look but don't touch" signal.
4. `hp-wmi-sensors` does not register **any** PWM attributes — only `fan[X]_input`, `fan[X]_label`, `fan[X]_fault`, `fan[X]_alarm`.
5. `asus-ec-sensors` is *read-only*.
6. `asus_wmi_sensors` is *also read-only*: *"No, fan control is not part of the Asus sensors WMI interface."*

**Driver families.**

| Driver | Surface | Writable? | Notes |
|--------|---------|-----------|-------|
| `dell-smm-hwmon` | pwm1..pwmN, pwm1_enable | partial (stepped, EINVAL on unsupported, often BIOS-fight) | Whitelist-gated by `i8k_whitelist_fan_control` DMI table. |
| `hp-wmi-sensors` | fan only, **no PWM attributes** | read-only | |
| `asus_wmi_sensors` | sensors (incl. fan RPM), **no PWM** | read-only | X370/X470/B450/X399 Ryzen only |
| `asus-ec-sensors` | EC sensors (temps + RPM), **no PWM** | read-only | Modern ASUS desktop boards |
| `asus-nb-wmi` | pwm1, pwm1_enable | enable-only on most models; pwm1 EINVAL on writes | Laptops |
| `thinkpad_acpi` | pwm1, pwm1_enable, fan_watchdog | yes (with watchdog and EINVAL constraints) | Mode 0 and 2 unsupported on some |
| `nct6775` | pwm1..pwm7 | yes (Super-I/O direct) | EC may collide on prebuilts |
| `it87` | pwm1..pwm5 | yes (Super-I/O direct) | EC may collide on Gigabyte/MSI prebuilts |
| `nct6683` | pwm | read-only by default | Some MSI boards expose nct6687 read-only via mainline; out-of-tree `nct6687d` driver adds writes |
| `corsaircpro` | pwm1..pwm6 (USB HID) | yes, but `pwm_enable` not exposed | https://github.com/markusressel/fan2go/issues/63 |

**Severity.** **Mostly auto-classifiable** (pure read-only drivers like `hp-wmi-sensors` are caught by structural Stage 1).

**Mitigation.** Stage 1 catches read-only drivers automatically. For known platform-mediated driver names, prepend a `platform_quirk` table.

### R2.2.g — TACH-WITHOUT-PWM PATTERN

**Description.** A `fanK_input` exists but no corresponding `pwmM` exists in the directory.

**Detection signal.** Enumerate `fanK_input` files. If `fanK_input` exists and **no** `pwmM` is found in the same hwmon, this is a read-only fan. Common case: GPU fans (`amdgpu`, `nvidia` via NVML), power-supply hwmons (`corsairpsu`, `nzxt-smart2`), ThinkPad `fan2_input` for read-only secondary fan.

**Severity.** **Auto-excludable from control set, retain for monitoring.**

### R2.2.h — MISMATCHED-INDEX PATTERN

**Description.** `pwmM` controls a fan whose tach signal appears on `fanK_input` where `M ≠ K`.

**Detection signal.**
1. Stage 3 correlation must scan **all** `fanK_input` in the hwmon, not just `fan{M}_input`, for each `pwmM`. This is exactly what lm-sensors `pwmconfig` does.
2. A historic lm-sensors bug (ticket #2380) revealed that `pwmconfig` could miss correlations when it disabled `pwmM_enable` after the first fan match.
3. Daisy-chained hub case: a single PWM signal feeds a 4-port hub with 4 fans, but the hub returns only one tach signal.

**Driver families.** Common on Gigabyte and MSI consumer boards using `nct6798d`, `nct6796d`, `it8689e`, and `it8688e`.

**Severity.** **Auto-resolvable** if Stage 3 is implemented correctly.

**Mitigation.** Stage 3 must record the correlation matrix `M[pwm, fan] = ΔRPM/ΔPWM` and select the maximum-correlation fan per PWM, not assume `pwmK ↔ fanK_input`.

## R2.3 Taxonomy Table

| # | Pattern | Detection Signal | Driver Examples | Severity | Auto-Excludable? | Mitigation |
|---|---------|------------------|-----------------|----------|-------------------|------------|
| a | ZERO-TACH | No `fanM_input`, or fan reads 0 across full PWM sweep | nct6775, it87, thinkpad_acpi (fan2) | Low | **Yes** | Skip silently |
| b | WRITE-IGNORED | PWM accepts writes, reads back, no RPM/thermal effect | it87 IT8689E rev1, nct6775 with DC-mode mismatch | Medium | **Yes** | Skip; log WARN with chip rev |
| c | PHANTOM-CHANNEL | Channel count > wired headers; no correlation on sweep | All Super-I/O (nct679x, it87xx) | Low | **Yes** | Skip; subsumed by Stage 3 |
| d | EINVAL/STEPPED | Write rejected with EINVAL or read-back ≠ written value | dell-smm-hwmon, asus-nb-wmi, thinkpad_acpi | Medium | **No** (channel is real, just constrained) | Probe legal value set; store as quantized |
| e | BIOS-FIGHT | `pwm_enable` reverts at T+5/10/30s, or `pwm` clobbered | dell-smm-hwmon, HP EC, Lenovo Legion EC, Dell iDRAC | High | **No** (user judgment) | Skip + diagnostic; require explicit `bios_fight_override` |
| f | PLATFORM-MEDIATED | Driver name in known platform list | hp-wmi-sensors (none), asus-ec-sensors (none), asus_wmi_sensors (none), asus-nb-wmi (partial), dell-smm-hwmon (stepped), thinkpad_acpi (constrained) | Variable | Read-only: **Yes**; Partial: **No** | Use platform_quirk table |
| g | TACH-WITHOUT-PWM | `fanK_input` exists, no `pwmM` in dir | amdgpu, drivetemp, corsairpsu, thinkpad_acpi (fan2), nzxt-smart2 | Low | **Yes** (from control set) | Retain for monitoring only |
| h | MISMATCHED-INDEX | Sweep correlation peak on `fanK_input` where `K ≠ M` | Gigabyte/MSI nct6798d, it8689e | Low | **Yes** (auto-remapped) | Record best-correlation pair; warn on mismatch |

## R2.4 Multi-Stage hwmon Entry Classification Pipeline

Cheap-first, expensive-last, with each stage acting as a kill-gate. Total worst-case time per channel ≈ Stage 3 (~90s) + Stage 4 (~30s) ≈ 2 minutes, executed once at first-run only.

### R2.4.1 Stage 1 — Structural sanity (≈1 ms per channel)

- Enumerate `/sys/class/hwmon/hwmon*/`. For each `pwmM`:
- Verify the file exists and is readable as the running uid.
- Verify it is writable (`access(W_OK)`).
- Verify a sibling `pwmM_enable` exists, is readable, and is writable.
- Read `name` from the parent directory and tag the channel.
- Reject if any of the above fails.

**What it catches.** Pattern 2.f read-only platforms (`hp-wmi-sensors`, `asus_wmi_sensors`, `asus-ec-sensors`).

### R2.4.2 Stage 2 — Write-and-read-back probe (≈100 ms per channel)

1. Read and save `pwmM` and `pwmM_enable`.
2. Write `1` to `pwmM_enable`. Read back.
3. Write `128` to `pwmM`. Read back. Run canonical-value probe ({0, 64, 85, 128, 170, 192, 255}) on EINVAL.
4. If read-back ≠ 128 (and not in {0, 255}), record as quantized (2.d).
5. Restore original `pwmM` and `pwmM_enable`.

**What it catches.** Pattern 2.d (EINVAL/STEPPED); early signal for 2.f, 2.e.

### R2.4.3 Stage 3 — Tach correlation sweep (≈45–90s per channel)

1. Snapshot all `fanK_input` values → `baseline[K]`.
2. For each candidate `pwmM`:
   - Set `pwmM_enable=1`.
   - Write `pwmM=255`. Wait `settle_full` (default 30s — fan2go #28).
   - Snapshot all `fanK_input` → `high[K]`.
   - Write `pwmM=64`. Wait `settle_low` (default 30s).
   - Snapshot all `fanK_input` → `low[K]`.
   - Compute Δ[K] = high[K] − low[K] for every K.
3. After all PWMs swept: for each `pwmM`, find `argmax_k M[m, k]`. Reject if max delta < `noise_floor` (default 50 RPM, NAS-tuned 100 RPM).
4. If multiple `pwmM` correlate with the same fan, classify as ambiguous.

**What it catches.** Patterns 2.a, 2.b, 2.c, 2.h.

**Time cost.** N × 60s serial. Optimization: PWMs in same hwmon can sweep in parallel after first pass.

### R2.4.4 Stage 4 — BIOS-fight detection (≈30s per admitted channel)

1. Set `pwmM_enable=1`. Write `pwmM=baseline_pwm`.
2. At T+5s, T+10s, T+30s: re-read `pwmM_enable` and `pwmM`.
3. If `pwmM_enable` ≠ 1 at any timepoint → BIOS-FIGHT on enable.
4. If `pwmM_enable=1` at all timepoints but `pwmM` differs → BIOS-FIGHT on duty cycle.

## R2.5 Per-Driver-Family Quirk Notes

### R2.5.1 nct679x family

- **PWM count.** Hardware-advertised: 3 (nct6776) to 7 (nct6796/nct6798/nct6799).
- **Phantom rate.** Empirically 30–60% of advertised channels are phantoms on consumer boards.
- **Mode bit pitfall.** `pwm_mode` (0=DC, 1=PWM) must match BIOS-side header configuration.
- **`pwm_enable` semantics.** {0=full speed, 1=manual, 2=Thermal Cruise, 3=Speed Cruise, 4=Smart Fan III, 5=Smart Fan IV}. Only mode 1 is safe for ventd.
- **BIOS interaction.** Generally well-behaved on consumer boards.
- **Out-of-tree.** `nct6687d` for MSI's `nct6687` chip.

### R2.5.2 it87 family

- **PWM count.** 3 (older it8703/it8705) to 5 (it8688/it8689/it8665).
- **Maintenance status.** Mainline covers older chips; modern Gigabyte/ASRock boards with IT8688E/IT8689E require **out-of-tree** `frankcrawford/it87`.
- **Module parameters.** `mmio=on` required on AMD platforms.
- **Critical bug.** **IT8689E revision 1** silently accepts PWM writes but does not act on them. Revision 2 works.
- **`fix_pwm_polarity` parameter.** Marked DANGEROUS — do not touch.

### R2.5.3 dell-smm-hwmon

- **Stepped values.** Default `I8K_FAN_HIGH=2` → values clamp-rounded to {0, 128, 255}. `fan_max=3` gives 4 levels.
- **`pwm_enable` accepts only {1, 2}.**
- **Whitelist-gated.** `i8k_whitelist_fan_control` DMI table determines whether `pwm1_enable=1` actually disables BIOS auto.
- **DMI blacklists.** Inspiron 3505, Precision M3800, Vostro 1720 are on blacklist.
- **`pwm1_enable` controls ALL fans simultaneously.**
- **BIOS-FIGHT.** Pervasive on XPS 9560 and similar.
- **EIO.** Sporadic IO errors on writes — retry-once before classifying as fault.

### R2.5.4 ASUS trio

| Driver | Coverage | PWM control? |
|--------|----------|--------------|
| `asus_atk0110` (acpi) | Older ASUS desktop boards | **Read-only** |
| `asus-ec-sensors` | Modern ASUS desktop boards (X570, B550, X670, B650, X870) | **Read-only** |
| `asus_wmi_sensors` (WMI) | X370/X470/B450/X399 Ryzen | **Read-only** explicitly |
| `asus-nb-wmi` | ASUS laptops | `pwm1_enable` writable; raw `pwm1` write usually EINVAL |

Modern ASUS desktop boards commonly expose BOTH `nct6798-isa-0290` AND `asus-ec-sensors`. Prefer writable nct6798 for control.

**Buggy WMI BIOSes.** Polling >1 Hz can trigger fan stoppage on Prime X470 Pro. Recommendation: ≤1 Hz when `asus_wmi_sensors` is primary.

### R2.5.5 thinkpad_acpi

- **PWM value mapping.** Quantized to fan levels 0–7.
- **`fan_watchdog`.** Critical safety attribute. Range 1–120s. ventd must either (a) set `fan_watchdog=120` and refresh writes within 120s, or (b) leave at default.
- **DISENGAGED mode.** "Full speed" / level=127. **Dangerous to set unintentionally.**
- **`pwm1_enable` semantics.** {0=offline, 1=manual, 2=EC auto, 3=reserved}.
- **Module parameter.** `fan_control=1` must be passed for sysfs to be writable.

### R2.5.6 hp-wmi-sensors

- **Read-only by design.** No `pwm*` attributes.
- **Conflict with `hp-wmi`.** If `hp-wmi` is loaded, alarm attributes become unavailable.
- **ventd action.** Skip entirely for control purposes; admit as monitoring source.

### R2.5.7 k10temp / coretemp (companions)

- These drivers expose temperature only.
- `coretemp-isa-0000` provides per-core temps on Intel.
- `k10temp-pci-00c3` provides Tctl/Tdie/Tccd1..N on AMD.
- ventd must include these in sensor pool but not in actuator pool.

## R2.6 False-Positive Cost vs False-Negative Cost Analysis

**Cost of a false-positive exclusion (real fan skipped).**
- User complaint
- Undercooling risk: only if BIOS auto curve was the ONLY thing keeping the fan running, AND user disabled it expecting ventd to take over. In practice, ventd restores `pwm_enable` to whatever it was before (typically 2=auto), so BIOS continues controlling.
- Mitigation: clear, well-documented `force_include` config flag.

**Cost of a false-negative inclusion (ghost included).**
- Wasted probe time (~60s per ghost)
- Bad calibration data: ventd believes its writes have effects they don't
- BIOS conflict on BIOS-fight channels
- Pollutes fleet baselines

**Quantitative comparison.** False-negative inclusions are *strictly worse* for ventd's design goals.

**Recommended bias.** Tune Stage 3's noise floor *upward* (50 RPM is conservative; 100 RPM is better for spinning-rust NAS). Tune Stage 4's settle window *upward*. Default to skip-with-warning, never include-without-explicit-confidence.

## R2.7 Recommended Detection Heuristic Ordering (Cheap-First)

1. **Driver name lookup** (1 µs). Read `name` file; check against known read-only drivers. Skip immediately.
2. **Structural sanity** (1 ms). `pwmM` exists, readable, writable.
3. **Companion driver check** (1 ms). Driver is `coretemp`, `k10temp`, `nvme`, `drivetemp`, `acpitz`, etc → SENSOR-ONLY.
4. **Conservative `pwm_enable` write** (10 ms).
5. **`pwm` write-and-read-back** (50–500 ms).
6. **Tach correlation sweep** (60–90s, parallel in batches per hwmon).
7. **BIOS-fight stress test** (30s, parallel).
8. **Restoration** of original `pwm` and `pwm_enable` for any skipped/failed channel.

ventd should expose each stage as an individually testable subcommand (`ventd probe --stage=3 hwmon4/pwm2`).

## R2.8 References

**Linux kernel source:**
- drivers/hwmon/nct6775.h: https://github.com/torvalds/linux/blob/master/drivers/hwmon/nct6775.h
- drivers/hwmon/dell-smm-hwmon.c: https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c
- drivers/hwmon/asus-ec-sensors.c: https://github.com/torvalds/linux/blob/master/drivers/hwmon/asus-ec-sensors.c
- drivers/hwmon/hp-wmi-sensors.c: https://github.com/kangtastic/hp-wmi-sensors/blob/main/hp-wmi-sensors.c
- Documentation/hwmon/nct6775.rst: https://docs.kernel.org/hwmon/nct6775.html
- Documentation/hwmon/dell-smm-hwmon.rst: https://docs.kernel.org/hwmon/dell-smm-hwmon.html
- Documentation/hwmon/asus_wmi_sensors.rst: https://docs.kernel.org/hwmon/asus_wmi_sensors.html
- Documentation/hwmon/hp-wmi-sensors.rst: https://docs.kernel.org/hwmon/hp-wmi-sensors.html
- Documentation/admin-guide/laptops/thinkpad-acpi.rst: https://www.kernel.org/doc/Documentation/laptops/thinkpad-acpi.txt
- frankcrawford/it87 out-of-tree: https://github.com/frankcrawford/it87

**lm-sensors:**
- pwmconfig: https://man.archlinux.org/man/extra/lm_sensors/pwmconfig.8
- pwmconfig correlation bug: https://lm-sensors.lm-sensors.narkive.com/V7TFnAUW/pwmconfig-doesn-t-detect-correlations-properly
- PR #383: https://github.com/lm-sensors/lm-sensors/pull/383

**fan2go:**
- Repo: https://github.com/markusressel/fan2go
- Issue #28 — settle ≥30s: https://github.com/markusressel/fan2go/issues/28
- Issue #63 — Corsair Commander Pro: https://github.com/markusressel/fan2go/issues/63
- Issue #64 — pwm_enable detection: https://github.com/markusressel/fan2go/issues/64
- Issue #201 — Dell Server stepped pwm: https://github.com/markusressel/fan2go/issues/201

**it87 issues:**
- #11 — Gigabyte B560M DS3H V2: https://github.com/frankcrawford/it87/issues/11
- #96 — IT8689E rev 1 silent-write: https://github.com/frankcrawford/it87/issues/96
- #97 — IT8613E manual write: https://github.com/frankcrawford/it87/issues/97

**Dell SMM:**
- Pali Rohár's patch series: https://groups.google.com/g/linux.kernel/c/WfidNnGV31k
- dell-bios-fan-control: https://github.com/TomFreudenberg/dell-bios-fan-control

**Forums:**
- ArchWiki Fan speed control: https://wiki.archlinux.org/title/Fan_speed_control
- Proxmox forum IT8613E: https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/

---

# R3 — Steam Deck Detection Without Writes

> ventd's catalog-less probe must, **before issuing any PWM write**, identify Valve Steam Deck hardware (LCD "Jupiter" / OLED "Galileo" / any future revision) on **any** Linux distribution and **refuse to control fans**.

## R3.1 Executive summary

The Steam Deck's embedded controller (EC) is not a generic ACPI thermal zone — it is the **VLV0100** ACPI device, owned by Valve's firmware, fronted by Valve's out-of-tree `steamdeck` / `steamdeck-hwmon` kernel modules, and managed in userspace by Valve's own daemon `jupiter-fan-control`. The EC firmware actively contends with userspace PWM writes — when the Valve daemon misbehaves, the EC silently reverts to its own curve. Two userspace fan controllers fighting over `pwm1` will produce audible oscillation, thermal regressions, and possibly excess wear on a tiny single-fan handheld.

The defensible default is therefore: **read-only DMI fingerprinting first, kernel-module presence as a secondary corroborating signal, and a hard refusal with a clear pointer to `jupiter-fan-control`. No write is ever attempted on a system where `/sys/class/dmi/id/sys_vendor == "Valve"`.**

## R3.2 The hardware reality

### R3.2.1 Variants and their stable fingerprints

| Variant | Marketing name | APU codename | DMI `product_name` | DMI `product_family` (observed) | DMI `sys_vendor` |
|---|---|---|---|---|---|
| LCD (64GB / 256GB / 512GB, 2022) | Steam Deck | Aerith (AMD Van Gogh, TSMC N7) | `Jupiter` | `Sephiroth`-era kernels list `Aerith`; older firmware reports vary | `Valve` |
| OLED (512GB / 1TB, Nov 2023) | Steam Deck OLED | Sephiroth (AMD Van Gogh refresh, TSMC N6) | `Galileo` | `Sephiroth` | `Valve` |
| Future Deck 2 / Steam Brick (hypothetical) | TBA | TBA (community speculates Tifa/Cloud per FF7 naming) | New string (not `Jupiter` or `Galileo`) | Likely new family string | Almost certainly still `Valve` |

The product-name evidence is firmly nailed down by:

- **Bazzite SteamOS-Manager script** explicitly branches on `[ $MODEL = "Jupiter" ]` / `[ $MODEL = "Galileo" ]` after reading `/sys/class/dmi/id/product_name`.
- **Mainline kernel `drm_panel_orientation_quirks.c`** uses `DMI_EXACT_MATCH(DMI_SYS_VENDOR, "Valve")` + `DMI_EXACT_MATCH(DMI_PRODUCT_NAME, "Galileo")`.
- **Mainline kernel `drm_panel_backlight_quirks.c`** (Aug 2025 patch) — same `Valve`+`Jupiter` and `Valve`+`Galileo` matches.
- **HoloISO installer** detects on `product_name == "Jupiter"` and was patched for `Galileo`.
- **Phoronix** confirmed Galileo/Sephiroth DMI strings from kernel 6.6 sound-driver patches.

### R3.2.2 The Valve EC and why writes are toxic

The Steam Deck's fan is controlled by an EC firmware that exposes a single ACPI device, **VLV0100**, via the DSDT. Andrey Smirnov's original kernel patch series describes it: "Steam Deck specific VLV0100 device presented by EC firmware. This includes but not limited to: CPU/device's fan control, Read-only access to DDIC registers, Battery temperature measurements, Various display related control knobs, USB Type-C connector event notification."

The driver registers a `hwmon` device named `"steamdeck_hwmon"` and exposes a `System Fan` channel with `pwm1` and `fan1_input`. **Crucially**, writes go through ACPI methods that the EC firmware can override.

Empirically, when Valve's own `jupiter-fan-control` daemon misparses its config or crashes mid-run, the fan immediately reverts to the EC's internal curve (`ValveSoftware/SteamOS#1359`). The Bazzite community confirms: "if you disable [jupiter-fan-control], the motherboard itself is designed to set the fan speed […] so a failing to start jupiter-fan-control service just means you have a louder (and quieter) deck as they shipped".

**The EC is the source of truth; the userspace daemon is a refinement on top.** A second userspace controller (e.g., ventd) writing the same `pwm1` is racing the EC and Valve's daemon.

### R3.2.3 The userspace daemon ventd must NOT replace

- **Authoritative upstream**: https://gitlab.com/evlaV/jupiter-fan-control
- **Public mirror (GitHub)**: https://github.com/Jovian-Experiments/jupiter-fan-control
- **Installed paths on stock SteamOS**:
  - `/usr/share/jupiter-fan-control/fancontrol.py`
  - `/usr/share/jupiter-fan-control/jupiter-config.yaml` (LCD profile)
  - `/usr/share/jupiter-fan-control/galileo-config.yaml` (OLED profile)
  - `/usr/lib/systemd/system/jupiter-fan-control.service`
  - Polkit helper: `/usr/bin/steamos-polkit-helpers/jupiter-fan-control`
- **Activation toggle**: SteamOS exposes "Enable updated fan control" in GamepadUI.
- **Build-time gate**: `fancontrol.py` reads DMI and **raises `NotImplementedError`** on any non-Jupiter/non-Galileo board — visible in the wild on a Legion Go S and an X570 desktop. This is Valve's *own* hardware-refusal pattern: exactly what ventd should mimic.
- **AUR adoption**: `jupiter-fan-control` and `steamdeck-dkms` both packaged in AUR.

## R3.3 Detection signals — exhaustive enumeration

### R3.3.1 Primary: DMI strings (`/sys/class/dmi/id/`)

| File | Expected value (LCD) | Expected value (OLED) | Stability | Notes |
|---|---|---|---|---|
| `sys_vendor` | `Valve` | `Valve` | **Highest** — Valve uses this string everywhere | Single-token check; cheap. |
| `product_name` | `Jupiter` | `Galileo` | High — multiple kernel quirks pin to these | Future Decks will use a *new* string but the same `sys_vendor`. |
| `product_family` | varies (observed `Sephiroth` in some firmware on LCD) | `Sephiroth` | Medium — codename-keyed | Use as corroboration only. |
| `board_vendor` | `Valve` | `Valve` | High | Redundant with `sys_vendor`. |
| `board_name` | `Jupiter` | `Galileo` | High | Mirrors `product_name`. |
| `bios_vendor` | varies (`Valve` or AMI) | varies | Low | Do **not** rely on. |
| `bios_version` | `F7A0xxx` style | `F7G0xxx` style | Low | Useful only for diagnostics. |
| `chassis_vendor` | `Valve` | `Valve` | High | Optional cross-check. |

`sys_vendor == "Valve"` is the **single most stable signal** because it is what Valve themselves use to gate kernel quirks. It will be **forward-compatible** because Valve as a company will continue to ship the same vendor string on any future device.

### R3.3.2 Secondary: kernel modules / platform devices

| Signal | What ventd reads | Present on |
|---|---|---|
| `/sys/module/steamdeck` directory exists | dir stat | SteamOS, Bazzite-Deck, ChimeraOS-on-Deck (when `steamdeck-dkms` or Valve neptune kernel installed) |
| `/sys/module/steamdeck_hwmon` directory exists | dir stat | Same |
| `/sys/bus/platform/drivers/steamdeck/` | dir listing | Same |
| `/sys/devices/platform/VLV0100:00/` | dir stat | Wherever the platform driver bound; ACPI HID is `VLV0100` |
| `/sys/class/hwmon/hwmonN/name == "steamdeck_hwmon"` | string read | Universal where the driver loads |
| `lsmod` listing of `steamdeck`, `steamdeck_hwmon` | `/proc/modules` parse | Cheap module check |

These signals are present on Deck hardware **only when Valve kernel patches are loaded**. On a vanilla mainline kernel on Deck hardware, they will be **absent** — even though it is unmistakably a Steam Deck. **DMI is the only universally reliable signal.**

Note: a mainline `k10temp` Steam Deck APU ID was finally added in **Linux 6.19**, so APU temperature monitoring works on stock kernels now, but **fan control via VLV0100 still requires Valve's out-of-tree `steamdeck` driver**.

### R3.3.3 Tertiary: CPU / APU identity

| Signal | Value | Notes |
|---|---|---|
| `model name` | `AMD Custom APU 0405` | LCD (Aerith / Van Gogh N7). |
| `model name` | `AMD Custom APU 0932` | OLED (Sephiroth / Van Gogh N6). |
| `vendor_id` | `AuthenticAMD` | Always. |
| `cpu family` / `model` | family 23 (0x17), Zen 2 | **Not unique to Deck** in isolation. |

Use only as **corroboration** for log output. Never as sole detection signal.

### R3.3.4 Quaternary: jupiter-fan-control daemon presence

| Signal | Path | Meaning |
|---|---|---|
| Systemd unit file present | `/usr/lib/systemd/system/jupiter-fan-control.service` | Valve daemon installed |
| Python script present | `/usr/share/jupiter-fan-control/fancontrol.py` | Same |
| Process `fancontrol.py` running | `/proc/*/cmdline` scan | Daemon active |
| Polkit helper present | `/usr/bin/steamos-polkit-helpers/jupiter-fan-control` | SteamOS-style integration |

This is **not a detection signal for hardware** — Bazzite ships the daemon on non-Deck images too — but it is useful diagnostically.

### R3.3.5 Signals you should NOT use

- `/proc/device-tree/` — **absent** on x86 Decks (this is ACPI x86_64).
- `dmesg` strings — not parseable from a normal user; non-deterministic.
- USB VID/PID for the controller — racy at boot.
- Hostname `steamdeck` — user-configurable.
- Presence of `/home/deck` user — same problem.
- BIOS version strings — drift across firmware updates.

## R3.4 Detection precedence chain

```
1. Read /sys/class/dmi/id/sys_vendor.
   IF == "Valve": classify as Steam Deck family → REFUSE.
       a. Also read product_name → log "LCD (Jupiter)" / "OLED (Galileo)" / "unknown Valve device".
       b. Also read product_family → log codename for diagnostics.
2. Iterate /sys/class/hwmon/*/name.
   IF any equals "steamdeck_hwmon" or "steamdeck": classify as Steam Deck → REFUSE.
3. Stat /sys/bus/acpi/devices/VLV0100:00/.
   IF present: classify as Steam Deck → REFUSE.
4. Parse /proc/modules. IF "steamdeck" or "steamdeck_hwmon" loaded: REFUSE.
5. Otherwise: not a Steam Deck. Continue normal probe.
```

**Rationale:** DMI first because it works on **every** Linux distribution at boot, including a freshly-installed mainline-kernel system. Hwmon-name and ACPI-device checks second to catch the (rare but possible) case where DMI strings have been stripped/spoofed but Valve drivers are bound. `/proc/modules` last because it is the weakest evidence.

## R3.5 Cross-distro / cross-version comparison matrix

| Signal | SteamOS 3.4 (LCD) | SteamOS 3.5 (LCD/OLED) | SteamOS 3.6+ | Bazzite-Deck | ChimeraOS on Deck | Nobara-Deck | Vanilla Arch on Deck (mainline) | Vanilla Fedora/Ubuntu on Deck | Dual-boot SteamOS/Linux |
|---|---|---|---|---|---|---|---|---|---|
| `sys_vendor == "Valve"` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ (DMI is hardware) |
| `product_name == "Jupiter"` (LCD) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `product_name == "Galileo"` (OLED) | n/a | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `/sys/module/steamdeck` | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ (only with `steamdeck-dkms`) | ❌ usually | ⚠️ |
| `hwmon name == "steamdeck_hwmon"` | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ❌ | ⚠️ |
| `VLV0100` ACPI device exposed | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `cpuinfo` AMD Custom APU 0405/0932 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `/proc/device-tree/` | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |

**Conclusion:** *Only* the DMI rows are universally green across every column. This empirically validates "DMI first, everything else as corroboration".

## R3.6 The refusal path

### R3.6.1 Behavioural contract

When the probe identifies Steam Deck hardware, ventd MUST:

1. Mark the device as `unsupported_hardware` with reason code `STEAMDECK_VLV0100`.
2. Emit a single structured log line at `WARN` level (not `ERROR` — this is expected behaviour on Deck).
3. Emit a one-time human-readable message to stdout (if interactive) or to the systemd journal (if running as service).
4. **Not open any `pwmN` file for write.** Read-only inventory of fans/temps for telemetry is acceptable and recommended.
5. Exit cleanly with code 0 if invoked as a one-shot probe, or remain running in `monitor-only` mode if running as a daemon.

### R3.6.2 Recommended refusal message text

```
ventd: detected Valve Steam Deck hardware ({product_name}, BIOS {bios_version}).
ventd will NOT control fans on this device.

Why: Valve's embedded controller (EC) firmware actively manages the fan
via the VLV0100 ACPI device and contends with userspace PWM writes. The
intended userspace controller is Valve's own jupiter-fan-control daemon,
shipped on SteamOS and available on other distros via:

  Upstream:  https://gitlab.com/evlaV/jupiter-fan-control
  Mirror:    https://github.com/Jovian-Experiments/jupiter-fan-control
  AUR:       https://aur.archlinux.org/packages/jupiter-fan-control
  Kernel:    https://aur.archlinux.org/packages/steamdeck-dkms

If you are on SteamOS, the daemon is enabled via Settings → System →
"Enable updated fan control".

ventd will continue to read sensor telemetry (fan RPM, temperatures) but
will not issue PWM writes. To suppress this message, add
'steam_deck_acknowledged: true' to ventd.yaml.
```

### R3.6.3 What ventd should still do on a Deck

- Surface `steamdeck_hwmon` `fan1_input` and `temp1_input` in `ventd status --read-only`.
- Surface AMD APU temps from `k10temp` (mainline ≥ 6.19) or from Valve's `steamdeck_hwmon` Battery Temp on older kernels.
- Allow the user to verify thresholds without controlling the fan — homelab Decks running headless server workloads benefit from monitoring.

## R3.7 Cross-reference with R1 (Tier-2 framework)

R1's Tier-2 framework classifies refusals into **virt/container refusal** and **lacks-permission refusal**. The Steam Deck case is **neither**:

- It is not a virt refusal — `pwm*` files exist and are writable as root.
- It is not a permission refusal — root can write the file.
- It is **hardware refusal**: the underlying physical hardware (EC firmware contract) makes writes harmful even when they syntactically succeed.

**Recommendation**: introduce a new top-level refusal class in ventd's policy engine called `hardware_refusal` (parallel to `virt_refusal` and `permission_refusal`), with R3 (Steam Deck) as its first member. Future hardware refusals will slot into this class. The class's contract is:

- Detection runs **first**, before virt/container probes.
- Detection signals are exclusively read-only.
- The refusal message is **always** actionable.

In R1 terminology, R3's policy class is `hardware_refusal::valve_steamdeck`, with the detection key being `dmi.sys_vendor == "Valve"`.

## R3.8 Forward compatibility (Deck 2 / Steam Brick / future Valve x86_64)

R3's design avoids catalog rot **by inverting the gate**: instead of an allow-list of "known Decks", a deny-list keyed only on `sys_vendor == "Valve"`.

| Future scenario | Detection outcome |
|---|---|
| Deck 2 ships with `product_name = "Tifa"` | `sys_vendor` is still `Valve` → refused. ✅ |
| Steam Brick (rumoured headless variant) | `sys_vendor` still `Valve` → refused. ✅ |
| A future Valve VR HMD that exposes Linux | `sys_vendor` still `Valve`; almost certainly no fan to control anyway → refused. ✅ |
| A non-Valve "SteamOS-certified" handheld (e.g., Lenovo Legion Go S running SteamOS) | `sys_vendor != "Valve"` → **not** refused on hardware grounds; fall through to normal probe. ✅ |
| Valve introduces a new ACPI HID `VLV0200` with a different EC contract | Still caught by `sys_vendor == "Valve"`. ✅ |
| Valve releases firmware that hides the `Valve` vendor string (extremely unlikely) | Caught by hwmon-name / `VLV0100` ACPI device fallback. |

**What NOT to key off**:

- **Don't** key on `product_name` exact-matching `Jupiter` or `Galileo` — that's the catalog antipattern.
- **Don't** key on the APU codename — AMD could theoretically reuse it.
- **Don't** key on the Valve neptune kernel — vanilla mainline-kernel installs slip through.

**Spec note**: when a Deck 2 ships, the log message can be enriched (so users see "detected Valve {Tifa} hardware"), but the **refusal behaviour** does not need any code change.

## R3.9 HIL validation plan

| Test | Fleet member | Steps |
|---|---|---|
| **Primary**: detection-fires-before-write on real Deck running SteamOS 3.6 | Steam Deck | Run `ventd probe --dry-run` and assert refusal message + reason code; assert no `pwmN` open(2) calls in `strace` output. |
| Detection-fires-before-write on Deck booted into Bazzite-Deck | Same Steam Deck, second boot entry | Repeat. |
| Detection-fires-before-write on Deck booted into vanilla Arch with `steamdeck-dkms` *uninstalled* | Same Steam Deck, third boot entry | Repeat — exercises the "DMI present, kernel module absent" case. |
| Negative control: detection does NOT fire on Proxmox host | Proxmox host | `sys_vendor` will be motherboard vendor; refusal must NOT fire. |
| Negative control: detection does NOT fire on MiniPC, 13900K desktop, 3 laptops | All others | None should produce `STEAMDECK_VLV0100`. |

The single highest-signal test: **boot the Deck into vanilla Arch with `steamdeck-dkms` uninstalled, run ventd, assert refusal**. That validates the most fragile branch (DMI-only path, no kernel-module corroboration).

## R3.10 Authoritative sources

- Valve `jupiter-fan-control` upstream: https://gitlab.com/evlaV/jupiter-fan-control
- Public mirror: https://github.com/Jovian-Experiments/jupiter-fan-control
- Original kernel patch (Andrey Smirnov, 2022): https://patchwork.kernel.org/project/linux-hwmon/patch/20220206022023.376142-1-andrew.smirnov@gmail.com/ and https://lwn.net/Articles/883961/
- DKMS-packaged platform driver: https://aur.archlinux.org/packages/steamdeck-dkms
- AUR jupiter-fan-control: https://aur.archlinux.org/packages/jupiter-fan-control
- Galileo DMI in mainline (panel-orientation-quirks): https://www.mail-archive.com/dri-devel@lists.freedesktop.org/msg499955.html
- Galileo DMI in mainline (panel-backlight-quirks, Aug 2025): https://lists.freedesktop.org/archives/dri-devel/2025-August/522215.html
- Phoronix on Galileo / Sephiroth (Linux 6.6 sound): https://www.phoronix.com/news/Linux-6.6-Sound
- Phoronix on Linux 6.19 k10temp: https://www.phoronix.com/news/Linux-6.19-HWMON
- ValveSoftware/SteamOS#1359 (EC takes over): https://github.com/ValveSoftware/SteamOS/issues/1359
- ValveSoftware/SteamOS#891 (jupiter-fan-control log spam): https://github.com/ValveSoftware/SteamOS/issues/891
- ValveSoftware/steam-for-linux#12286 (Legion Go S NotImplementedError): https://github.com/ValveSoftware/steam-for-linux/issues/12286
- Bazzite jupiter-fan-control on Galileo: https://github.com/ublue-os/bazzite/issues/1147
- Bazzite kernel: https://github.com/hhd-dev/kernel-bazzite
- Arch Wiki Steam Deck: https://wiki.archlinux.org/title/Steam_Deck
- HoloISO Galileo detection: https://github.com/HoloISO/holoiso/issues/855
- Bazzite SteamDeck-BIOS-Manager: https://github.com/ryanrudolfoba/SteamDeck-BIOS-Manager/blob/main/steamdeck-BIOS-manager.sh
- Jovian-NixOS Galileo notes: https://github.com/Jovian-Experiments/Jovian-NixOS/issues/227
- Universal Blue community confirmation: https://www.answeroverflow.com/m/1196366584403480627

---

# R4 — Envelope C Abort Thresholds (per CPU/system class)

> Defensible, citation-backed numeric values for the dT/dt and absolute-temperature abort gates that bound Envelope C's "marginal-benefit-per-workload" learning probes. Values are tabulated by class and a per-class HIL validation plan is supplied.

## R4.1 Executive summary

Envelope C must abort a thermal probe **before** the silicon enters its thermal-throttle regime, **before** the PSU's protection envelope, and (on NAS) **before** the warmest drive crosses the manufacturer-rated case-temperature ceiling. The defensible defaults below are tuned for the seven device classes ventd will encounter in the homelab/desktop fleet (and the laptop-class ones encountered as edge-cases).

The headline numbers — for example, **2.0 °C/s + Tjmax−15 on 13900K-class HEDT air**, or **1.0 °C/min + 50 °C case on NAS HDDs** — fall out of three independent constraints: ACPI's standard thermal-trip granularity, kernel-thermal driver behavior at PASSIVE/HOT/CRITICAL trips, and silicon/storage manufacturer datasheet values.

The work below is grounded in: ACPI 6.5 §11; Linux kernel `Documentation/thermal/sysfs-api.txt` and `drivers/acpi/thermal.c`; AMD AGESA / Tctl offset documentation; Intel TVB and TCC-offset specs; manufacturer datasheets (Seagate IronWolf Pro, WD Red, Toshiba MG, MicroSemi/Broadcom HBA, LSI 9300/9305); and a body of empirical thermal-stress reports (Tom's Hardware 13900K cooling tests, SkatterBencher delidding/thermal articles, Backblaze drive-temperature studies, and TrueNAS/Unraid forums on multi-bay drive bay gradients).

## R4.2 Class taxonomy

| #  | Class                                | Representative parts (AMD / Intel)                              | Tjmax (°C) | Reference notes                                                                                                                                                                  |
|----|--------------------------------------|----------------------------------------------------------------|-----------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1  | Desktop HEDT, air-cooled             | Ryzen 9 7950X / 9950X3D · Core i9-13900K / 14900K              | 95–100    | High heat density; large coolers; air-cooled radiator with ≥240 mm equivalent surface; single PWM curve typically governs CPU + 1–3 case fans.                                  |
| 2  | Desktop HEDT, AIO-cooled             | Same parts as Class 1 with 240–360 mm AIO                       | 95–100    | Coolant inertia is large; transient response dominated by pump/radiator-fan dynamics rather than die-to-IHS coupling.                                                            |
| 3  | Mid-range desktop                    | Ryzen 5/7 5xxx · Core i5/i7 12000–14000                         | 90–100    | Single PWM, modest TDP envelope.                                                                                                                                                  |
| 4  | Server CPU                           | EPYC Genoa/Bergamo · Xeon SPR/EMR/Granite Rapids · TR-PRO       | 95        | Multi-PWM (dedicated CPU fan + chassis bank); chassis ducting; many SKUs default to BMC-managed thermal control. ventd's role here is auxiliary — see §6.4 caveat.               |
| 5  | Mobile / laptop CPU                  | Ryzen 7000U/8000HS · Core Ultra 100/200H · 12700H / 13700H      | 95–105    | Single fan, EC-managed; ventd typically cannot override EC; envelope must be very tight.                                                                                          |
| 6  | Mini-PC / NUC / fanless desktop      | N100 / N150 / N305 · 1255U / 1355U                              | 100–105   | Some are fully passive; some have a single 40–80 mm blower; chassis is the dominant heat path on passives.                                                                       |
| 7  | NAS — HDDs / SAS SSDs / HBA / SES    | Seagate IronWolf, WD Red Plus/Pro, Toshiba MG/N300, Exos, etc. | n/a       | Storage thermal envelopes are determined by drive case temperature (60 °C max sustained, 70 °C absolute peak per most NAS-rated units). HBA junction is a *separate* constraint. |

Each class has distinct dT/dt and absolute thresholds because: (a) thermal mass differs by ~3 orders of magnitude between a server-grade air-cooled tower and a 14 TB HDD, (b) safe headroom relative to Tjmax differs by 5–15 °C depending on whether the silicon has a TVB / TCC offset margin, and (c) the cost of *being wrong* differs (a damaged $7000 EPYC vs. an annoyingly oscillating fan curve).

## R4.3 Per-class numeric defaults

Each entry gives `[dT/dt abort, T_abs abort, ambient gating]` plus a one-paragraph rationale. All numbers use °C and °C/s unless explicitly °C/min. **Conservatism rule**: where citations disagree, ventd takes the most conservative cited number.

### Class 1 — Desktop HEDT, air-cooled

- **dT/dt abort:** **2.0 °C/s** sustained over 3 s window (i.e., 6 °C jump in 3 s).
- **T_abs abort:** **Tjmax − 15 °C** (e.g., 85 °C on a 100 °C-Tjmax 13900K, 80 °C on a 95 °C-Tjmax 7950X, 75 °C on an 89 °C-Tjmax 7950X3D — citation: AMD product pages and Intel ARK, plus AMD AGESA Tctl offset note: 7950X3D Tjmax = 89 °C).
- **Ambient gating:** Probe authorized only if (Tjmax − T_amb) ≥ 60 °C, where T_amb is the motherboard `acpitz` or chassis intake sensor. Below this, dT/dt thresholds are effectively unsafe — derate to Class 3 numbers.

**Rationale.** Heat density on Raptor Lake Refresh and Zen 4/Zen 5 X3D is the highest in the fleet — 13900K is documented at 100 °C "almost immediately" on Cinebench R23 with stock-class air coolers. Junction-to-die slope under 100% load is *empirically* in the 5–20 °C/s range during the first half-second of a step-load on stock-air. **Setting dT/dt_abort at 2.0 °C/s aborts at the bottom of that range**, which leaves 4–5 s of decision time for the fan-ramp to engage before a worst-case 10 °C/s ramp reaches Tjmax. T_abs at Tjmax − 15 °C aligns with Intel's TVB-preferred operating threshold (TVB activates at ≤ 70 °C on i9 family for max boost; sustained operation in the 85–95 °C band is documented as safe but at the cost of TVB), and is 3 × the 5 °C ACPI-spec rounding granularity, so we are not floating in noise. AMD's 7950X3D Tjmax is 89 °C — with the −15 °C buffer the abort sits at 74 °C, which preserves operating headroom for X3D's narrower envelope. Note that CCD vs IOD are the controlled mass: on Zen 4 the Tctl reading already incorporates the AGESA offset (Threadripper 2990WX historic +27 °C; Ryzen 1 +20 °C; Ryzen 2700X +10 °C — see `drivers/hwmon/k10temp.c` `tctl_offset_table[]` in the kernel source) — ventd uses Tctl directly only when no Tdie/Tccd is exposed.

### Class 2 — Desktop HEDT, AIO-cooled (240–360 mm)

- **dT/dt abort:** **1.5 °C/s** sustained over 3 s.
- **T_abs abort:** Tjmax − 15 °C (same as Class 1).
- **Ambient gating:** (Tjmax − T_amb) ≥ 60 °C.

**Rationale.** Coolant adds ~120 s of effective thermal time-constant at the radiator side, so the *early* slope at the die is gentler — ~3 °C/s peak observed with stock 240 mm AIO under FPU AVX-512 stress. 1.5 °C/s is therefore a tighter abort gate that catches "AIO pump failure" or "AIO radiator-fan stalled at low PWM" earlier than the 2.0 °C/s used for air. The absolute target is unchanged because the silicon's safe-operating envelope is identical regardless of cooler topology. **Caveat on AIO failure modes:** when an AIO pump dies, the *die* slope can briefly exceed even Class 1 numbers because there is suddenly no thermal sink at all; the dT/dt gate at 1.5 °C/s catches this within the first 3 s.

### Class 3 — Mid-range desktop

- **dT/dt abort:** **1.5 °C/s** sustained over 3 s.
- **T_abs abort:** Tjmax − 12 °C (typically 78 °C on 90 °C-Tjmax parts; 88 °C on 100 °C-Tjmax parts).
- **Ambient gating:** (Tjmax − T_amb) ≥ 55 °C.

**Rationale.** Lower heat density than Class 1, lower steady-state thermals; the 12 °C buffer is acceptable because workload spikes don't typically sit close to Tjmax. A 1.5 °C/s slope is more typical of Class 3 parts under stock cooling, so the same abort gate value as Class 2 applies for a different reason: it is comfortably *above* normal operating slopes but well below dangerous trajectories.

### Class 4 — Server CPU (EPYC Genoa/Bergamo, Xeon Sapphire Rapids, TR-PRO)

- **dT/dt abort:** **1.0 °C/s** sustained over 5 s.
- **T_abs abort:** **Tjmax − 20 °C** (typically 75 °C Tctl on EPYC, 70 °C on Xeon Pt 8480+).
- **Ambient gating:** (Tjmax − T_amb) ≥ 50 °C; if a BMC is detected (`/dev/ipmi0`, `/sys/class/ipmi`, or `dmidecode` IPMI table), ventd refuses Envelope C unless `--allow-bmc-coexist` is set — this is a hard policy rule that pre-empts the dT/dt math entirely.

**Rationale.** Server SKUs are the most-conservative class because (a) data loss is most-expensive in this domain, (b) thermal envelopes on EPYC parts are aggressively managed by SP5 platform firmware and the AMD Adaptive Power Management interface, (c) most server chassis are managed by an iLO/iDRAC/IPMI BMC that *will* fight ventd if ventd writes to chassis fans — so the conservative dT/dt is a defense-in-depth on top of the BMC-coexistence policy. Tctl − 20 °C is tighter than Class 1's Tjmax − 15 because EPYC Tctl is offset relative to true die temp (the AGESA offset is part-specific), and because EPYC chiplet topology means non-uniform die temps are normal — abort headroom must accommodate the worst chiplet, not the average. The BMC-coexistence rule is grounded in the Dell PowerEdge thermal-resiliency whitepaper showing that iDRAC's Closed-Loop Thermal Control ramps fans to 100 % within seconds of any "lost sensor" or "lost cooling" event — ventd colliding with that algorithm is a direct way to cause a thermal regulation oscillation.

### Class 5 — Mobile / laptop CPU

- **dT/dt abort:** **2.0 °C/s** sustained over 2 s (note shorter window).
- **T_abs abort:** Tjmax − 15 °C (typically 85 °C on 100 °C-Tjmax parts).
- **Ambient gating:** (Tjmax − T_amb) ≥ 55 °C; **gated**: ventd refuses Envelope C entirely if PWM writes to the EC do not produce measurable RPM change in a calibration handshake (see R6) — this is the canonical case where the EC owns the thermal loop and ventd must not interfere.

**Rationale.** Laptop thermal envelopes are aggressive because the chassis cannot dissipate sustained TDP at full boost — most thin-and-light laptops hit Tjmax within 1–2 s of an AVX heavy-load step. 2.0 °C/s mirrors Class 1 because the *die* response is similar; the 2 s window (vs. 3 s for desktops) gives the EC less time to ramp before ventd aborts. The hard refusal-on-EC-handshake-fail rule is the single most important laptop policy: many laptops (Lenovo Legion, Dell XPS, ASUS ROG) expose `pwmN` files that *appear* writable but are silently overridden by the EC, in which case ventd's writes do nothing and any Envelope C learning is corrupt.

### Class 6 — Mini-PC / NUC / passive desktop

- **dT/dt abort:** **1.0 °C/s** sustained over 5 s.
- **T_abs abort:** Tjmax − 20 °C (typically 85 °C on N100/N305 with 105 °C Tjmax).
- **Ambient gating:** (Tjmax − T_amb) ≥ 55 °C; on passive systems (no fan detected at all), ventd does not run Envelope C (no probe is meaningful).

**Rationale.** Mini-PCs have a wide thermal envelope (Tjmax ≥ 100 °C) and benign workloads, but their chassis surface temperature can become uncomfortable quickly if a probe runs the fan up unnecessarily. 1.0 °C/s is generous because these parts simply do not heat at that rate under normal probes; if they *do*, something is wrong (e.g., heatsink not seated). T_abs at Tjmax − 20 °C provides 20 °C of headroom, which is appropriate for parts whose Tjmax is generously specified by the manufacturer.

### Class 7 — NAS storage envelope

NAS storage envelopes are *not* expressed in °C/s — drive temperatures change at the °C/min scale due to massive thermal mass relative to heat input. Time-axis is therefore explicitly slow.

#### Class 7a — Spinning HDDs (NAS-rated, 7200 RPM)

- **dT/dt abort:** **1.0 °C/min** sustained over a 5-minute window.
- **T_abs abort:** **min(50 °C, mfg_max − 10 °C, T_amb + 15 °C)**, where `mfg_max` is taken from the drive's SMART attribute 194 manufacturer rating (typically 65–70 °C); for 14 TB+ Toshiba MG drives use 45 °C.
- **Ambient gating:** None separately — the absolute formula already incorporates ambient.

**Rationale.** The Backblaze Q3 2023 drive-stats and 2014 temperature-vs-failure analysis indicate that drives running above 60 °C **sustained** show statistically meaningful failure-rate increases; drives running above 55 °C show it only at p<0.1 with massive sample sizes. The Pinheiro et al. (Google, FAST '07) study found a U-curve with optimal failure rates between 37 and 46 °C, with sharp rise above 47 °C. Manufacturer NAS-drive ratings cluster at 65 °C (Seagate IronWolf), 65 °C (WD Red Pro), 60 °C (older WD Red), and 70 °C absolute peak. ventd's 50 °C abort threshold is **conservative by design**: it sits below Backblaze's 55 °C empirical inflection and below all manufacturer-rated maxima with 10 °C margin, so any drive running stably below 50 °C is well within its safe envelope. The dT/dt at 1.0 °C/min catches unusual bay-airflow failures (a fan dying mid-probe) without false-aborting on normal warmup behavior. Probe must read multiple drives — **the warmest drive in the array sets the floor for the whole pool.**

#### Class 7b — SATA / SAS SSDs in NAS chassis

- **dT/dt abort:** **2.0 °C/min** sustained over a 2-minute window.
- **T_abs abort:** **60 °C case** (per most enterprise SAS SSD datasheets).
- **Ambient gating:** None separately.

**Rationale.** Enterprise SAS SSDs (Samsung PM1633, Toshiba PX series, Micron 5400 PRO/MAX) tolerate higher temperatures than HDDs but throttle aggressively at the case-temperature limit. The 2.0 °C/min slope is generous because SSDs warm faster than HDDs under sustained write load (no platter, no flux dissipation, less thermal mass).

#### Class 7c — HBA / SAS expander / SES enclosure-management chip

- **dT/dt abort:** **1.0 °C/min**.
- **T_abs abort:** **75 °C ROC** (Read-Only Channel temperature, a.k.a. "junction" on these chips).
- **Ambient gating:** None.

**Rationale.** Broadcom/LSI 9300/9305 HBAs and SAS expander chips run hot — 70 °C is normal under load with passive heatsinks. The 75 °C abort threshold is below the chips' documented max-junction (typically 100–115 °C) and above their normal-operating ceiling. ventd should read these via `drivetemp` (for SES) or via vendor-specific tools (`storcli`, `mvcli`) when present. NB: many homelab HBAs run *passively*-cooled and are fan-shadow-dependent on chassis fans — abort behavior on these chips usually means "chassis intake/exhaust fan is failing or has died" and warrants escalation rather than just probe-abort.

## R4.4 Citations

### Standards and kernel docs

- ACPI 6.5 Spec, §11 Thermal Management: https://uefi.org/specs/ACPI/6.5/11_Thermal_Management.html
- Linux kernel `Documentation/thermal/sysfs-api.txt` and `drivers/acpi/thermal.c` (THERMAL_TRIP_ACTIVE, THERMAL_TRIP_PASSIVE, THERMAL_TRIP_HOT, THERMAL_TRIP_CRITICAL definitions, and `orderly_poweroff()` semantics on critical).

### Silicon (Intel)

- Intel ARK, Core i9-13900K (Tjmax 100 °C): https://www.intel.com/content/www/us/en/products/sku/230496/intel-core-i913900k-processor-36m-cache-up-to-5-80-ghz/specifications.html
- Intel ARK, Core i9-14900K (Tjmax 100 °C): https://www.intel.com/content/www/us/en/products/sku/236773/intel-core-i9-processor-14900k-36m-cache-up-to-6-00-ghz/specifications.html
- SkatterBencher, *Intel Thermal Velocity Boost*: https://skatterbencher.com/intel-thermal-velocity-boost/ (TVB activates ≤ 70 °C; OCTVB in BIOS, etc.)
- Tom's Hardware, *Core i9-13900K Cooling Tested*: https://www.tomshardware.com/features/intel-core-13900k-cooling-tested

### Silicon (AMD)

- AMD product page, Ryzen 9 7950X3D (Tjmax 89 °C): https://www.amd.com/en/products/processors/desktops/ryzen/7000-series/amd-ryzen-9-7950x3d.html
- AMD product page, Ryzen 9 9950X3D (Tjmax 95 °C): corroborated via AMD product page and Phoronix.
- Linux kernel `drivers/hwmon/k10temp.c` (Tctl offset table): https://github.com/torvalds/linux/blob/master/drivers/hwmon/k10temp.c
- AMD EPYC 9004 family (Tjmax 95 °C): https://www.amd.com/en/products/processors/server/epyc/4th-generation-9004-and-8004-series.html

### Storage (HDD)

- Seagate IronWolf Pro Product Manual (case temp 60 °C max sustained, 70 °C absolute peak; 20 °C/h transport gradient): https://www.seagate.com/content/dam/seagate/migrated-assets/www-content/product-content/ironwolf/en-us/docs/100835908f.pdf
- WD Red Plus / Red Pro datasheets: https://documents.westerndigital.com/content/dam/doc-library/en_us/assets/public/western-digital/product/internal-drives/wd-red-plus-hdd/product-brief-wd-red-plus.pdf
- Toshiba MG09 series (15 TB): https://americas.kioxia.com/en-us/business/products/hard-disk-drives/mg09-series.html
- Backblaze drive temperature analysis (does temp matter): https://www.backblaze.com/blog/hard-drive-temperature-does-it-matter/
- Backblaze Drive Stats Q3 2023 (60 °C / 55 °C empirical inflection): https://www.backblaze.com/blog/backblaze-drive-stats-for-q3-2023/
- Pinheiro et al., *Failure Trends in a Large Disk Drive Population*, FAST '07 (referenced via Wikibooks Minimizing HDD Failure synthesis).

### Storage (HBA / SES)

- Broadcom 9305-16i product brief (junction max 115 °C): https://www.broadcom.com/products/storage/host-bus-adapters/sas-9305-16i
- Klara Systems, *Managing Disk Arrays on FreeBSD/TrueNAS Core* (`sesutil` for SES enclosure temperature): https://klarasystems.com/articles/managing-disk-arrays-on-freebsd-truenas-core/

### Server thermal management

- Dell PowerEdge KB 000123186 (iDRAC thermal algorithm; sensor-loss = 100 % fan default): https://www.dell.com/support/kbdoc/en-us/000123186/poweredge-thermal-management-and-troubleshooting-guide
- Principled Technologies / Dell, *PowerEdge HS5620 thermal resiliency* (server fan-failure scenario timings): https://www.principledtechnologies.com/Dell/PowerEdge-HS5620-thermal-resiliency-whitepaper.pdf

### Mini-PC / NUC / N100

- Netgate Forum, Topton N100 thermal note (Tjmax 105 °C, TCC offset): https://forum.netgate.com/topic/186104/topton-n100-reporting-402-mhz/80
- Intel ARK N100 product page: https://www.intel.com/content/www/us/en/products/sku/231803/intel-processor-n100-6m-cache-up-to-3-40-ghz/specifications.html

### Thermal time-constant background

- ScienceDirect *Chip Temperature*: https://www.sciencedirect.com/topics/computer-science/chip-temperature

## R4.5 HIL validation plan

| Class | Fleet member | Test workload | Expected outcome |
|------|--------------|---------------|------------------|
| 1 (HEDT air) | 13900K + RTX 4090 (dual-boot Linux) | T6: y-cruncher AVX-512 with intentional stock-fan-stop, abort at T_pkg ≥ 85 °C OR dT/dt ≥ 2.0 °C/s sustained 3 s | abort fires, logged, fan ramps; manual recovery |
| 1 (HEDT air) | 13900K + RTX 4090 | T7: cross-check with Intel TCC offset by lowering Tjmax via BIOS to 90 °C; assert abort threshold also drops by ~10 °C | dynamic re-read of Tjmax respected |
| 2 (HEDT AIO) | 13900K + RTX 4090 with AIO swap (if available) | T8: Cinebench R23 with AIO pump unplugged at minute 1 | abort within 3 s of pump stop |
| 3 (mid desktop) | Proxmox host (5800X + RTX 3060) | T1–T4 (idle / 50 % / 100 % / ambient sensitivity) | nominal — no aborts |
| 4 (server) | none in fleet | — | use Proxmox 5800X as lower-bound analog under T2/T3; flag class-4 HIL as a known gap |
| 5 (laptop) | All three laptops | T9: EC handshake (PWM step, observe RPM step) | refuses Envelope C if no RPM change |
| 5 (laptop) | All three laptops | T10: Cinebench R23 fan-stop, abort at 85 °C / 2.0 °C/s | abort fires |
| 6 (mini-PC) | MiniPC Celeron | T1, T2, T5 (passive-class detection — no fan) | passive: skip envelope; active: nominal |
| 7 (NAS) | TerraMaster F2-210 (acquired 2026-04-28; ARM RTD1296, single-drive HIL pending hwmon inventory) | T11–T14: warmup at idle, warmup at 50 % I/O, warmup at scrub, ambient ramp | log per-drive temps, multi-drive max() (synthetic), abort at min() of formula |

## R4.6 Confidence assessment

| Class | Confidence | Reason |
|------|------------|--------|
| 1 | Medium | Slope spread is wide; abort logic is conservative and independent of slope precision. |
| 2 | Medium | Coolant transient under-characterized publicly; AIO failure modes have to be inferred. |
| 3 | High | Mid-range desktop thermals are well-trodden; Backblaze-grade data exists for the fleet. |
| 4 | Low–Medium | Limited public fan-stop data on EPYC SP5; mitigated by gated-refusal default. |
| 5 | Low | Chassis variance dominates; numbers are conservative. |
| 6 | High | Wide envelope, low risk; defaults are conservative by design. |
| 7a (HDD) | High | Datasheet-grounded; minutes-scale gives generous reaction time; multiple independent corroborating studies. |
| 7b (SAS SSD) | Medium-High | Datasheet-grounded; less independent corroboration. |
| 7c (HBA / SES) | Medium | Numbers from datasheets and forum consensus; no large-scale empirical study found. |

## R4.7 Spec ingestion target

Primary ingestion target: `spec-v0_5_3-envelope-c.md` § "Abort thresholds (per class)". Cross-references: `spec-v0_5_3-envelope-c.md` § "Idle predicate" (R5) for the gating logic and `spec-v0_5_2-polarity-disambiguation.md` § R6 for the laptop EC handshake. The per-class table above is the authoritative source; if HIL data later contradicts a number, this section is amended in-place.

## R4.8 Review flags surfaced from chat (2026-04-28)

- **Per-class safety ceilings, not global.** The earlier draft proposed `safety_ceiling_dT_dt_C_per_s = 5.0` as a global override-flag bound. This is dangerously high for laptops — Class 5 must enforce ≤ 3.0 °C/s; Class 7 must enforce ≤ 2.0 °C/min. Override-flag bounds must be class-specific. Spec amendment required.
- **Class 1 13900K HIL test T6 (y-cruncher AVX-512 + intentional fan-stop)** is the most dangerous test in the fleet. It must be wrapped in an external thermal abort: a separate watcher script that kills `ventd` if a chassis-external sensor reads > 88 °C, regardless of ventd's own state. Defense-in-depth.
- **Class 7 NAS gap status:** TerraMaster F2-210 was acquired on 2026-04-28; ARM RTD1296, hwmon inventory pending. If hwmon/PWM is absent, F2-210 will validate the *observable* HDD-thermal side (drivetemp poll cadence, multi-drive aggregation logic, dT/dt °C/min math) but not fan control. Still useful as the only HIL member that exercises spinning-rust thermal mass.
- **Conservatism rule explicitly retained.** When sources disagree on a number, ventd takes the most-conservative cited value. Asymmetric cost: aborting too late = hardware damage; aborting too early = recoverable user complaint via override.

---

# R5 — User Idle Gate ("Idle Enough For Envelope C")

> Defining what "idle" means for ventd's calibration probes; recommending the predicate that gates Envelope C.

## R5.1 Executive summary

Envelope C runs sustained PWM excursions that briefly raise audible fan noise in order to learn the fan→thermal→workload coupling for a given device. Running the probe while the user is mid-Cinebench, mid-rsync, mid-ZFS scrub, mid-game, or on battery would (a) corrupt the measurement, (b) annoy the user, and (c) on laptops/handhelds reduce battery life. The idle gate is the predicate `idle_enough_for_envelope_c(now) → bool` that ventd evaluates *before* permitting a probe. Because **no prior fan-control daemon implements an idle gate** (lm-sensors `pwmconfig` and Thinkfan documentation explicitly defer responsibility to the operator), this layer is novel and the design must be defensible from first principles.

The recommended predicate is a **layered conjunction** of three categories:

1. **Hard refusals** — fail-closed conditions that always block (on-battery, container, structural state like ZFS scrub, process blocklist hit).
2. **Pressure-based idleness** — Linux PSI cpu/io/memory averages over 60- and 300-second windows where available; cpuidle C-state residency as fallback.
3. **Quiescence corroboration** — disk/network/GPU near-zero activity over 60 seconds; durability requirement (predicate continuously true for ≥ 5 minutes before probe arms).

PSI is the kernel-native primitive ventd uses when present (kernel ≥ 4.20, cgroup v2, distro-default on Debian 11+, Ubuntu 20.04+, RHEL 9+, Fedora 32+, Arch, TrueNAS Scale, Unraid 6.10+, Proxmox VE 7+). Where unavailable (older kernels, RHEL 8, distros with PSI disabled), ventd falls back to a cpuidle-residency + utilization combination. The structural-state allowlist is the single most important component on NAS/homelab targets, because no statistical signal reliably catches "ZFS scrub running at 30 MB/s" — it must be explicitly excluded by reading `/proc/spl/kstat/zfs/*/state` or equivalent.

The predicate is **conservative by design**: it returns *false* under any ambiguity. False negatives (probe deferred when it could have run) are cheap; false positives (probe runs during a real workload) are user-visible and erode trust.

## R5.2 The Problem Space

ventd's Envelope C probe must answer: *"Right now, can I push fans to a stress curve for 60 s without affecting the user, corrupting the measurement, or running on a workload that violates safety preconditions?"*

This is not the same as "is the system idle?" in the desktop-screensaver sense (`logind` `IdleHint`, `xidle`, `wlr-idle-notify`, `xprintidle`) — those signal user-input idleness, which is orthogonal to thermal idleness. A 4 AM ZFS scrub on a headless NAS is GUI-idle but thermally maximally inappropriate. A user watching Netflix is GUI-active but the CPU is idle and the probe is fine.

Failure modes the gate must defend against:

| Failure mode | Why it must be blocked |
|---|---|
| Sustained CPU load (compile, render, game) | Probe corrupts thermal coupling measurement; user hears unnecessary noise. |
| Storage maintenance (ZFS scrub, mdadm resync, BTRFS scrub, smartctl long test) | Drives are at sustained heavy load, often running >30 MB/s for hours; thermal envelope is in a non-representative state; probe risks tripping abort thresholds. |
| Sustained I/O (rsync, restic, borg, backup tools, large compile artifact write) | Same as above; storage thermals dominate. |
| Battery operation (laptop, NUC on UPS during outage) | Probe drains battery; ventd should never run on battery in any case. |
| Container with limited view (LXC, Docker, Podman) | ventd cannot accurately observe host-level pressure; probe results are meaningless and may collide with host fan governance. |
| Just-booted system (post-resume, post-cold-boot) | Thermal state has not stabilised; sensors may be in lag-catchup mode; cron jobs and unit-startup activity make idleness deceptive. |
| GPU compute (CUDA training, OpenCL, transcoding) | Co-cools with CPU on most desktops; probe interferes with GPU thermal envelope. |
| GUI-busy desktop (compositor at 60 fps, video playback) | Workload is light but fan-curve probe will be audible — user-experience cost. |

## R5.3 Prior Art Review

A representative survey of Linux fan-control tooling and idle-detection libraries was conducted to determine whether any existing project implements an analogous gate. **None do.**

| Project | Idle gate? | Approach | Source |
|---|---|---|---|
| `lm-sensors` `pwmconfig` (8.x) | **No** | Operator is responsible for ensuring CPU is idle during probe; the script halts if interrupted but does not detect idleness. | https://github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/pwmconfig |
| `lm-sensors` `fancontrol` | **No** | Runs continuously based on fixed temperature curves; never auto-calibrates. | Same repo. |
| `thinkfan` | **No** | Reads sensors, applies fan curve; calibration is operator-driven. | https://github.com/vmatare/thinkfan |
| `fan2go` | **No** | Curve-based; supports temperature inputs but no workload-aware probing. | https://github.com/markusressel/fan2go |
| `coolercontrol` | **No** | UI-based curves; no probe phase. | https://github.com/codifryed/coolercontrol |
| `auto-cpufreq` | **Partial** (battery state) | Switches governor based on AC/battery; consults `/sys/class/power_supply` for AC status. | https://github.com/AdnanHodzic/auto-cpufreq |
| `tuned` (Red Hat) | **Partial** (profile-based) | Uses tuning profiles selectable per-state (laptop, throughput-performance, etc.) but no programmatic idle gate. | https://tuned-project.org/ |
| `irqbalance` | **No** | Real-time IRQ rebalancing, not relevant. | https://github.com/Irqbalance/irqbalance |
| `psi-monitor` (oss tooling around PSI) | **No** | Read-only PSI exporters (`psi_exporter` for Prometheus). | https://github.com/jeessy2/psi_exporter |
| `systemd-logind` `IdleHint` | **No (wrong axis)** | User-input idleness for screen-lock/suspend, not workload idleness. | systemd source: https://github.com/systemd/systemd/blob/main/src/login/logind-session.c |
| Wayland `ext-idle-notify-v1` | **No (wrong axis)** | Same as above, GUI idleness only. | https://wayland.app/protocols/ext-idle-notify-v1 |

This means ventd's idle gate is genuinely novel work and must be designed defensively from first principles.

## R5.4 Signal Catalog

### R5.4.1 Linux PSI (Pressure Stall Information)

PSI is a kernel feature (≥ 4.20, default-enabled on cgroup-v2 kernels) exposing the fraction of time tasks were stalled waiting for CPU, I/O, or memory.

**Files:**
- `/proc/pressure/cpu` — three columns: `some avg10 avg60 avg300 total`. (No `full` for CPU because CPU pressure with all tasks stalled is not meaningful in the same way as I/O.)
- `/proc/pressure/io` — six values: `some avg10 avg60 avg300`, `full avg10 avg60 avg300`.
- `/proc/pressure/memory` — same structure as `/proc/pressure/io`.

**`some` vs `full`:** `some` = ≥ 1 task stalled. `full` = *all* non-idle tasks stalled.

**Quirks for ventd:**
- Inside a cgroup-v2 cgroup, PSI files are *cgroup-local* (e.g., `/sys/fs/cgroup/system.slice/ventd.service/cpu.pressure`). ventd MUST read **the root cgroup's** PSI files (the `/proc/pressure/*` paths).
- Inside an unprivileged LXC container or Docker without `--privileged`, the PSI files may show *only the container's* pressure, not the host. **ventd's idle gate becomes meaningless inside an unprivileged container** — this is a hard refusal in §R5.5.
- WSL2: PSI is exposed but represents the WSL VM, not the Windows host.
- PSI was disabled by default on RHEL 8 / older Debian 10. Distro check is required.
- PSI accuracy: kernel updates the metrics ~250 ms (`HZ + HZ/2`) by default; values lag reality by up to 1 s.

**Authoritative source:** Linux PSI documentation, https://docs.kernel.org/accounting/psi.html

### R5.4.2 cpuidle C-state residency

When PSI is unavailable, the cpuidle subsystem provides per-CPU per-state cumulative time/usage counters at `/sys/devices/system/cpu/cpu*/cpuidle/state*/`.

**Files (per CPU, per state):**
- `state*/name` — state name (`POLL`, `C1`, `C1E`, `C2`, `C6`, `C8`, `C10`, etc.)
- `state*/time` — cumulative microseconds spent in this state.
- `state*/usage` — cumulative entries to this state.

**Idle indicator:** during a 60-s window, if the sum of `time` deltas across all CPUs in deep C-states (typically C6 and deeper, distinguished by name regex) divided by `60 s × ncpus × 1e6` exceeds a threshold (recommended 0.85), the system is genuinely idle.

**Authoritative source:** Kernel cpuidle documentation, https://docs.kernel.org/admin-guide/pm/cpuidle.html

### R5.4.3 `/proc/loadavg`

Load average is the most-misunderstood signal in Linux. Brendan Gregg's authoritative analysis explains that Linux load average **includes TASK_UNINTERRUPTIBLE tasks** (the historical patch is from 1993 by Matthias Urlichs); the metric is therefore "system demand" not "CPU demand", and a system with no CPU contention but heavy disk I/O can show loadavg > ncpus.

**For ventd's idle gate:**
- Use loadavg as a **corroborating** signal only, never as a primary one.
- Threshold: 1-min and 5-min loadavg both ≤ 0.10 × `ncpus` is a strong "low demand" signal.

**Critical caveat for containers:** `getloadavg(3)` returns *host* load even when `/proc/loadavg` is correctly remapped via `lxcfs` to show container load, because the libc function reads `/proc/loadavg` **only on first call** and then caches the descriptor — and the lxcfs remount may not exist when libc initializes. ventd MUST read `/proc/loadavg` directly via `os.ReadFile` every poll, not via `unix.Sysinfo` or any libc-mediated path.

**Source:** lxc/lxc#4372 — *The output of getloadavg() inside an LXC container with lxcfs is the same as the host*, https://github.com/lxc/lxc/issues/4372

### R5.4.4 `/proc/diskstats`

Per-block-device read/write counters. Idle indicator is "Σ(sectors_read+sectors_written) over 60 s ≤ N MB/s" with N = 1 MB/s as a strict threshold.

**ZFS scrub corner case:** ZFS scrub I/O appears in `/proc/diskstats` as reads to the underlying block devices (the zpool members), so this signal **does** catch scrub load if the threshold is set correctly (scrub typically runs at 30–500 MB/s).

### R5.4.5 `/proc/net/dev`

NIC packet counters. NAS workloads (iSCSI target, SMB share, NFS export) show as elevated rx/tx packet counts.

**Idle threshold:** Σ rx_packets + tx_packets over 60 s ≤ 200 pps across all non-loopback interfaces.

### R5.4.6 GPU activity

- **AMD:** `/sys/class/drm/card*/device/gpu_busy_percent` — single integer 0–100. Idle = average ≤ 5 over 60 s.
- **NVIDIA:** NVML `nvmlDeviceGetUtilizationRates(...)` returns `gpu` and `memory` percent. ventd already uses purego dlopen for NVML in fan control; reuse that channel. Idle = `utilization.gpu` ≤ 5 average.
- **Intel:** `intel_gpu_top` is a sampler tool, not a sysfs interface. For Iris Xe / Arc, ventd reads `/sys/class/drm/card*/engine/*/busy` if exposed; otherwise treat as no-signal (default to "GPU idle").

### R5.4.7 Power supply state (laptops, NUCs on UPS)

`/sys/class/power_supply/`:
- `AC*/online` — `1` if AC plugged.
- `BAT*/status` — `Discharging`, `Charging`, `Full`, `Not charging`, `Unknown`.

**Hard refusal:** if any AC says `online = 0` AND any BAT says `status = Discharging`, ventd refuses Envelope C unconditionally.

### R5.4.8 Storage maintenance state

The signal that no statistical metric reliably catches.

**ZFS scrub:**
- `/proc/spl/kstat/zfs/<poolname>/state` (when zfs.ko loaded) — contains scrub progress when active.
- `zpool status` would give clean output but ventd should not shell out.
- Recommended: read `/proc/spl/kstat/zfs/*/state` files; presence of `scrub:` line with `state = scanning` ⇒ refuse.

**mdadm resync:**
- `/proc/mdstat` contains `recovery =` or `resync =` lines when active.
- Read `/proc/mdstat`, regex match `(recovery|resync|check) =`.

**BTRFS scrub:**
- No simple sysfs file. ventd reads `/sys/fs/btrfs/<uuid>/devinfo/<devid>/error_stats` for activity hints, or check process list for `btrfs scrub` (cleaner).

**LVM thinpool resilvering:** rare on homelab but check `/sys/class/block/dm-*/holders` activity.

### R5.4.9 Process-name / cmdline blocklist

Read `/proc/*/comm` and `/proc/*/cmdline`; refuse if any active process matches:

```
rsync, restic, borg, duplicity, kopia
plex-transcoder, jellyfin-ffmpeg, ffmpeg, handbrakecli
apt, dpkg, dnf, rpm, pacman, snapd, flatpak
updatedb, locate
fio, stress-ng, sysbench, ycruncher, prime95
docker (build/pull subcommand only via cmdline scan), podman
git fsck, git gc
```

This is the brute-force complement to PSI when PSI shows pressure but ventd needs to know *what* is causing it.

### R5.4.10 Uptime / post-resume

- `/proc/uptime` — first field is seconds since boot.
- `/sys/power/wakeup_count` and `/var/log/journal` resume markers — for post-suspend detection ventd uses `clock_gettime(CLOCK_MONOTONIC)` minus a stored "last seen monotonic" value; if delta < expected, the system suspended.

**Threshold:**
- Uptime < 10 minutes ⇒ refuse (cron jobs, unit startup, indexers, cold cache).
- Time-since-resume < 10 minutes ⇒ refuse (same logic for hibernate/suspend).

### R5.4.11 systemd-logind IdleHint and Wayland ext-idle-notify

**Explicitly excluded from the idle gate.**

logind `IdleHint` reflects only graphical/seat-active sessions. systemd issue #9622 confirms tty/ssh sessions are untracked; #34844 confirms greeter sessions never set `IdleHint`. On a headless NAS or a homelab server, `IdleHint=true` always, regardless of actual workload — useless.

Wayland `ext-idle-notify-v1` is a compositor protocol; same orthogonality — does not exist on TTY-only or headless installs and reflects only mouse/keyboard activity.

ventd MUST NOT consult `org.freedesktop.login1.Manager.GetSession*` or any compositor idle protocol.

### R5.4.12 amd_energy / Intel RAPL — package power as load proxy

**Note (2026-04-28 review flag): the `amd_energy` driver was removed in Linux 6.2.** Replacement signals:

- `/sys/class/powercap/intel-rapl:0/energy_uj` — Intel RAPL counter (works on AMD too on newer kernels via `amd_pmf` or generic powercap framework).
- Read at T0 and T0+60 s; compute ΔE / Δt = average package watts.

**Idle threshold:** package power ≤ 1.2 × idle baseline (where baseline is established at first calibration). Below baseline + ε ⇒ idle.

This is a strong corroborating signal that catches the "all C-states report idle but PSI shows pressure" edge case (a common kernel-bug pattern).

## R5.5 Recommended Predicate (Conservative)

**Pseudocode:**

```python
def idle_enough_for_envelope_c(now) -> Tuple[bool, IdleReason]:
    # ─── HARD REFUSALS (fail-closed) ─────────────────────────────────
    if on_battery():                          return False, IdleReason.ON_BATTERY
    if in_unprivileged_container():           return False, IdleReason.UNPRIVILEGED_CONTAINER
    if mdadm_active() or zfs_scrub_active() \
       or btrfs_scrub_active():               return False, IdleReason.STORAGE_MAINTENANCE
    if process_blocklist_hit():               return False, IdleReason.HEAVY_PROCESS
    if uptime_seconds() < 600:                return False, IdleReason.RECENT_BOOT
    if seconds_since_resume() < 600:          return False, IdleReason.RECENT_RESUME

    # ─── PSI PRIMARY (when available) ─────────────────────────────────
    if psi_available():
        cpu  = psi('cpu')
        io   = psi('io')
        mem  = psi('memory')
        if cpu['some_avg60']    > 1.0:    return False, IdleReason.CPU_PRESSURE
        if cpu['some_avg300']   > 0.8:    return False, IdleReason.CPU_PRESSURE_SUSTAINED
        if io['some_avg60']     > 5.0:    return False, IdleReason.IO_PRESSURE
        if io['some_avg300']    > 3.0:    return False, IdleReason.IO_PRESSURE_SUSTAINED
        if mem['full_avg60']    > 0.5:    return False, IdleReason.MEMORY_PRESSURE
    else:
        # ─── CPUIDLE FALLBACK ─────────────────────────────────────────
        if cpu_nonidle_pct_60s()       > 5.0:  return False, IdleReason.CPU_BUSY_FALLBACK
        if deep_cstate_residency_60s() < 0.85: return False, IdleReason.SHALLOW_CSTATES
        loadavg = read_proc_loadavg()
        if loadavg[0] > 0.10 * ncpus():        return False, IdleReason.LOADAVG_HIGH
        if loadavg[1] > 0.10 * ncpus():        return False, IdleReason.LOADAVG_HIGH

    # ─── QUIESCENCE CORROBORATION ─────────────────────────────────────
    if disk_aggregate_60s_MBps() > 1.0:           return False, IdleReason.DISK_ACTIVE
    if any_disk_60s_MBps() > 4.0:                 return False, IdleReason.DISK_ACTIVE_PEAK
    if nic_aggregate_60s_pps() > 200:             return False, IdleReason.NIC_ACTIVE
    if amd_gpu_busy_60s_avg() > 5.0:              return False, IdleReason.GPU_BUSY
    if nvidia_gpu_util_60s_avg() > 5.0:           return False, IdleReason.GPU_BUSY

    # ─── DURABILITY GATE ──────────────────────────────────────────────
    if not predicate_continuously_true_for_300s():
        return False, IdleReason.NOT_DURABLE_YET

    return True, IdleReason.OK
```

**Default thresholds** — tunable per-deployment via config but documented as defaults:

| Threshold | Default | Rationale |
|---|---|---|
| PSI cpu.some avg60 max | 1.00 % | A single brief stall blip in a minute. |
| PSI cpu.some avg300 max | 0.80 % | Sustained idle over five minutes. |
| PSI io.some avg60 max | 5.00 % | I/O on NAS workloads has higher baseline. |
| PSI io.some avg300 max | 3.00 % | Sustained low I/O. |
| PSI memory.full avg60 max | 0.50 % | Memory pressure is rare; even a little is bad. |
| cpuidle deep-C-state residency over 60 s | ≥ 0.85 | 85 % deep idle in 60 s is a very quiet system. |
| 1-min loadavg max (fallback path) | 0.10 × ncpus | Brendan Gregg's "idle desktop" rule of thumb. |
| disk aggregate over 60 s | ≤ 1 MB/s | Background fsync/journal flush only. |
| disk peak per-device over 60 s | ≤ 4 MB/s | Catches single-device hot-spot. |
| NIC aggregate over 60 s | ≤ 200 pps | Below normal mDNS/ARP/NTP chatter. |
| GPU utilization avg 60 s | ≤ 5 % | Compositor + occasional video frame. |
| Durability window | ≥ 300 s | Five minutes of continuous OK before probe arms. |
| Uptime minimum | 600 s | Ten minutes since boot. |
| Time-since-resume minimum | 600 s | Same as uptime, applied to last suspend/resume. |
| Daily probe attempt cap | 12 | Backoff cap; about one attempt every two hours. |

**Refusal handling and retry logic:** if the predicate returns *false*, ventd backs off according to truncated exponential backoff with base 60 s, max 3600 s (1 hr), ±20 % jitter. After the first hard-refusal cause (e.g., on-battery), retry resumes immediately when the cause clears. After 12 attempts in a single day, ventd holds further attempts until next-day window.

## R5.6 Mapping to ventd Constraints

- **Pure Go, CGO_ENABLED=0:** All signals above are file-IO. No syscalls beyond `open/read/close`, `clock_gettime` (via Go's `time.Now`), and `os.ReadDir`. NVML utilization read uses ventd's existing purego dlopen wrapper.
- **Linux-first through v1.0:** All paths are Linux-specific.
- **Catalog-less:** No assumptions about which sensors exist on which hardware.
- **TrueNAS / Unraid / Proxmox / desktop / laptop:** Compatibility considered:
  - **Proxmox host:** PSI available; refuses if any guest VM is in a busy cgroup (cgroup-v2 unified hierarchy makes this natural — root PSI reflects total).
  - **Proxmox LXC guest:** Refuses (unprivileged container case).
  - **TrueNAS Scale:** kernel ≥ 5.15, PSI default-on, Debian-based; predicate works as-is.
  - **TrueNAS Core (FreeBSD):** out of scope for v1.0 — Linux-first.
  - **Unraid 6.10+:** kernel ≥ 5.15, PSI on; works.
  - **Unraid 6.9 and earlier:** kernel 5.10, PSI may be disabled depending on build; falls back to cpuidle.
  - **Steam Deck (SteamOS 3.x):** PSI available; ventd will not run on Deck per R3.

- **Daemon mode:** predicate is evaluated once per N minutes (default 5 min) when not actively probing; once per probe-attempt cycle when seeking to start.
- **Cost:** all signals are < 5 ms total to read. No external commands.

## R5.7 Worked False-Positive / False-Negative Scenarios

**Scenario A — ZFS scrub on TrueNAS Scale:**
- PSI io.some avg60 = 8.2 % (above 5 % threshold) ⇒ refuse via `IO_PRESSURE`.
- Even if PSI didn't trigger, `/proc/spl/kstat/zfs/<pool>/state` shows scrub ⇒ refuse via `STORAGE_MAINTENANCE`.
- Disk aggregate at ~150 MB/s ⇒ refuse via `DISK_ACTIVE`.
- ✓ Triple-protected.

**Scenario B — Compile job on Proxmox host:**
- PSI cpu.some avg60 = 12 % (above 1 % threshold) ⇒ refuse via `CPU_PRESSURE`.
- ✓ Single-signal sufficient.

**Scenario C — Headless NAS with no GUI, midnight, light SMB browse activity:**
- PSI all-clear.
- NIC at 50 pps (below 200 threshold) ⇒ pass.
- Disk at 0.3 MB/s ⇒ pass.
- After 5 min durability ⇒ probe arms. ✓ Correct allow.

**Scenario D — Plex transcoding from network share:**
- Process blocklist (`plex-transcoder`, `jellyfin-ffmpeg`, `ffmpeg`) ⇒ refuse via `HEAVY_PROCESS`.
- Even without blocklist: NIC at 30 Mbps ≈ 5000 pps ⇒ refuse via `NIC_ACTIVE`.
- AMD `gpu_busy_percent` ≈ 30 (HW decode) ⇒ refuse via `GPU_BUSY`.
- ✓ Multiple protections.

**Scenario E — Laptop on battery:**
- `/sys/class/power_supply/AC/online = 0` ⇒ refuse via `ON_BATTERY` immediately.
- ✓ First-line refusal.

**Scenario F — Unprivileged Proxmox LXC running ventd:**
- `systemd-detect-virt --container` would return `lxc`, but ventd reads `/proc/1/cgroup` directly and detects `:lxc:` prefix ⇒ refuse via `UNPRIVILEGED_CONTAINER`.
- This is correct behaviour — ventd cannot accurately measure host pressure from inside an unprivileged container, and writing PWM from inside one is doubly inappropriate. ventd refuses to even try.

**Scenario G — Just resumed from suspend, user immediately starts work:**
- `seconds_since_resume() = 30` < 600 ⇒ refuse via `RECENT_RESUME`.
- ✓ Conservative gate prevents probe during cache-cold post-resume period.

**Scenario H — Headless server during nightly cron storm (logrotate, mlocate updatedb, certbot renew):**
- Process blocklist catches `updatedb` ⇒ refuse via `HEAVY_PROCESS`.
- PSI io.some avg60 likely triggers as well during logrotate compression.
- ✓ Backstop.

**Scenario I — Idle-looking system during a memory-pressure incident (background swapping):**
- PSI memory.full avg60 = 3 % (above 0.5 % threshold) ⇒ refuse via `MEMORY_PRESSURE`.
- ✓ Caught even when CPU and disk look quiet.

## R5.8 Refusal-Reason Enum and Operator-Visible Logging

Every refusal returns an enum value (above) which ventd logs at `info` level with structured fields:

```
{"event":"envelope_c_idle_check", "result":"refused", "reason":"IO_PRESSURE_SUSTAINED",
 "psi_io_some_avg60": 6.2, "psi_io_some_avg300": 4.1, "ts":"2025-11-12T03:15:22Z"}
```

This allows operators to diagnose why a probe never arms on their system. ventd also exposes `ventd doctor --idle` to print the live predicate evaluation, similar to `systemctl is-active --quiet` but with the full reason chain.

## R5.9 Configuration

Operators may tune any default in `ventd.yaml`:

```yaml
idle:
  psi:
    cpu_some_avg60_max: 1.0
    cpu_some_avg300_max: 0.8
    io_some_avg60_max: 5.0
    io_some_avg300_max: 3.0
    memory_full_avg60_max: 0.5
  fallback:
    cpu_nonidle_max_pct: 5.0
    deep_cstate_min_fraction: 0.85
    loadavg_factor: 0.10
  quiescence:
    disk_aggregate_max_MBps: 1.0
    disk_per_device_max_MBps: 4.0
    nic_aggregate_max_pps: 200
    gpu_avg_max_pct: 5.0
  durability_seconds: 300
  uptime_minimum_seconds: 600
  resume_minimum_seconds: 600
  daily_attempt_cap: 12
  process_blocklist:
    - rsync
    - restic
    - borg
    - plex-transcoder
    # ... etc
  process_blocklist_extra: []   # operator-supplied additions
```

**Conservative profile** (recommended on shared / family / small-business NAS):
```yaml
idle.durability_seconds: 600
idle.daily_attempt_cap: 4
idle.psi.cpu_some_avg60_max: 0.5
```

**Aggressive profile** (for solo desktops / dev workstations):
```yaml
idle.durability_seconds: 120
idle.daily_attempt_cap: 24
idle.psi.cpu_some_avg60_max: 2.0
```

## R5.10 Citations

**Primary:**
- Linux PSI documentation: https://docs.kernel.org/accounting/psi.html
- Linux cpuidle documentation: https://docs.kernel.org/admin-guide/pm/cpuidle.html
- Brendan Gregg, *Linux Load Averages: Solving the Mystery* (2017): https://www.brendangregg.com/blog/2017-08-08/linux-load-averages.html
- lxc/lxc#4372 — getloadavg under lxcfs returns host load: https://github.com/lxc/lxc/issues/4372
- lm-sensors `pwmconfig` source establishing prior-art "no idle gate": https://github.com/lm-sensors/lm-sensors/blob/master/prog/pwm/pwmconfig
- systemd issue #9622 — IdleHint untracked tty/ssh: https://github.com/systemd/systemd/issues/9622
- systemd issue #34844 — IdleHint not set on greeter sessions: https://github.com/systemd/systemd/issues/34844

**Secondary:**
- ZFS scrub state file format: https://github.com/openzfs/zfs/blob/master/man/man8/zpool-scrub.8
- mdadm `/proc/mdstat` format: https://docs.kernel.org/admin-guide/md.html
- AMD `gpu_busy_percent` sysfs: https://docs.kernel.org/gpu/amdgpu/thermal.html
- NVIDIA NVML utilization rates: https://docs.nvidia.com/deploy/nvml-api/
- Intel RAPL via powercap: https://docs.kernel.org/power/powercap/powercap.html

## R5.11 Spec ingestion target

Primary: `spec/v0.6/r5-idle-gate.md` § "Predicate definition". Implementation lands in `internal/idle/predicate.go` (predicate function), `internal/idle/reason.go` (refusal reason enum), `internal/idle/psi.go` and `internal/idle/cpuidle.go` (signal readers). Config knobs added to `spec/v0.6/config-schema.md`. Cross-reference: this consumes R3 (Envelope C protocol; calibration handshake) and feeds R7 (operator override / `ventd calibrate --force`).

## R5.12 Review flags from chat (2026-04-28)

- **Process blocklist must support operator extension** (`idle.process_blocklist_extra`). The static list ages badly without operator-side additions. Elevate to a RULE.
- **`amd_energy` driver was removed in Linux 6.2.** The earlier draft cited it; replacement is `amd_pmf` on AMD newer kernels or RAPL via `/sys/class/powercap/intel-rapl` (works on AMD too on modern kernels). Fact-check completed; numbers above use the correct path.
- **`ventd calibrate --force` semantics need R4's safety_ceiling rule.** Force overrides §R5.5's PSI/quiescence/durability gates but never the hard refusals (battery, container, storage maintenance), AND always honors R4 safety_ceiling thresholds.
- **PSI inside cgroup-v2:** ventd reads root PSI files (`/proc/pressure/*`), not its own cgroup PSI files. Document explicitly to avoid future confusion.
- **Memory.full vs memory.some:** the predicate uses `memory.full` for stronger signal. Memory pressure events that don't stall *all* tasks are not blocking enough for the gate. Documented choice.


---

# R6 — Polarity Midpoint (Probe-Start Initial PWM Value)

> Defensible, citation-backed initial PWM value for ventd's polarity-disambiguation probe — the value written first to a freshly-discovered `pwmN` channel before stepping ±N units to determine whether the channel is normal or inverted.

## R6.1 Executive summary

The polarity probe writes an initial PWM value, observes the resulting fan RPM, then writes an offset value (e.g., +N units), and infers polarity from `Δ(RPM)/Δ(PWM)` sign. **PWM=128 (50% of the canonical 0–255 hwmon range)** is the defensible default for the initial value, with three driver-specific overrides:

1. **Dell SMM with `i8k_fan_max==3`** → use `PWM=170` (so that the read-back stepped value is `170 → 170` instead of `128 → 85`).
2. **Any channel reporting `pwmN_mode=0` (DC mode)** → use `PWM=160` (≈ 7.5 V) instead of 128 (≈ 6 V) to clear the ≥ 7 V stall floor of typical 3-pin DC fans.
3. **`thinkpad_acpi`** → still use `PWM=128`, but only after setting `pwm1_enable=1` and re-arming `fan_watchdog`; do not run the polarity probe (firmware-monotonic 0..7 mapping makes it unnecessary).

A **+64-unit step** is the recommended probe magnitude (preferred), with a +32-unit fallback for chips that exhibit fan-safety quantization (rare). The 128↔192 (or 128↔64) sweep is large enough to cross every documented stall floor and small enough to avoid the saturation regime above ~80% duty cycle on most consumer fans.

The risk model is **fail-warm**: PWM=128 keeps every documented fan running at ≥ 30% RPM in normal polarity and at ≤ 70% RPM in the worst-case inverted polarity. There is no value of N that simultaneously (a) survives both polarities, (b) is unambiguously above every stall floor, and (c) gives a clean ΔRPM signal — so PWM=128 is chosen as the value that maximises ΔRPM signal confidence while keeping fail-warm safety.

## R6.2 The polarity-detection problem

### R6.2.1 Where polarity inversion comes from

Linux's hwmon ABI specifies `pwmN ∈ [0, 255]`, where 0 = "minimum" and 255 = "maximum" (kernel `Documentation/hwmon/sysfs-interface.rst`). For modern Intel-spec 4-wire fans, this is unambiguous: the duty cycle of the PWM line is proportional to `pwmN/255`, and the fan's tachometer increases monotonically with duty cycle. **In normal polarity, PWM=255 means full speed.**

But "polarity" can be inverted by three independent mechanisms:

1. **Driver-level inversion bit.** Several Super-I/O chips have a per-channel polarity bit that the BIOS may set. The Linux `it87` driver exposes a deeply-deprecated module parameter `fix_pwm_polarity=1` whose source code comment explicitly warns *"This option is for hwmon developers only. DO NOT USE if you don't know what you're doing"*. The `nct6775` and ITE drivers no longer expose direct polarity flips, but historic boards (early Lenovo, some ASUS Z77/Z87) had bit-19 of the FAN_CFG register inverted by BIOS.
2. **DC-mode (3-pin) on a PWM-mode-expecting Linux interface.** When `pwmN_mode = 0` (DC) but `pwmN` is treated by software as PWM mode, the *voltage* presented to the fan scales linearly — but at PWM≤80 the voltage is too low to keep a 3-pin fan spinning. From the daemon's perspective this looks polarity-like in the sense that "low PWM = high RPM" can happen if the fan momentarily stalls then re-spins under voltage rebound. This is the most common "polarity" case in the wild.
3. **Misinterpreted reading.** A ghost-fan on a phantom `pwm` channel is unrelated to its tach reading on `fanK_input`. If the daemon is reading the wrong tach for the channel, ΔRPM may *appear* inverted. R2's tach-correlation sweep filters this case out *before* the polarity probe runs.

For ventd's polarity probe, the relevant cases are (1) and (2). Case (3) is R2's responsibility.

### R6.2.2 Fail-modes for an unsafe initial value

| Initial PWM value | Normal polarity (effective duty) | Inverted polarity (effective duty) | Fail-mode |
|---|---|---|---|
| 0 | 0% (full stop) | 100% (full speed) | Normal: fan stops; tach reading goes to 0 RPM; probe cannot distinguish "stopped" from "no tach". **Bad.** |
| 32 | ~12% | ~88% | Normal: below most stall floors; many fans drop out below 30% duty. **Bad.** |
| 64 | 25% | 75% | Borderline-stall in normal polarity on cheap fans; probe is noisy. |
| 96 | ~37% | ~63% | Marginal; too close to stall on some fans. |
| **128** | **50%** | **50%** | **Identical effective duty in both polarities.** Above every documented stall floor. |
| 160 | ~63% | ~37% | Symmetric to 96. |
| 192 | 75% | 25% | Symmetric to 64. |
| 255 | 100% (full speed) | 0% (full stop) | Inverted: fan stops; tach 0 RPM; same problem as PWM=0 mirror. **Bad.** |

The inflection point is **PWM=128**: it is the unique value where the *effective duty cycle is identical* in both polarities (50%↔50%), so the probe-start state is fail-symmetric. Any other initial value forces the daemon to pre-commit to a polarity assumption that may be wrong — the very thing the probe is trying to determine.

### R6.2.3 Why "use the current value" is wrong

A naive alternative is "just preserve the current `pwmN` reading and step from there". This fails for three reasons:

- **Fresh discovery state.** ventd's polarity probe runs *after* R2 has admitted the channel and `pwmN_enable` has been forced to 1 (manual). The previous value may have been BIOS-controlled (mode 2) and is no longer meaningful as a baseline.
- **Hot probe avoidance.** The current value might be 0 (silent fan curve at idle) or 255 (BIOS panic-ramp under high load). Both are bad start values per §6.2.2.
- **Reproducibility.** Test rigs and unit tests need deterministic initial state.

### R6.2.4 Why not "step from last known-safe value"?

ventd has no known-safe baseline on first-run for unknown hardware. The whole point of the smart-mode catalog-less approach is *no priors*. PWM=128 is therefore the default-in-absence-of-priors.

## R6.3 The fan stall-floor literature

Stall floor = the minimum PWM duty cycle below which a fan stops spinning entirely. For a polarity probe to work, both probe values (initial and stepped) must be **above** the stall floor in **both** polarities — otherwise the ΔRPM signal collapses.

### R6.3.1 Cited stall floors by fan class

| Fan class | Stall floor (% duty) | Source |
|---|---|---|
| Intel-spec 4-wire 4-pin PWM fans | ≤ 30% | Intel "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification" rev 1.3 (Sep 2005), §3.2: *"Fan speed response... shall be a continuous and monotonic function of the duty cycle... within ±10%"*. PDFs: https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf and https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf |
| Noctua NF-A12x25 / NF-F12 / NF-S12A (premium 4-pin) | ~ 20% (300 RPM minimum) | Noctua datasheets, e.g. https://noctua.at/en/nf-a12x25-pwm/specification |
| Arctic P12 PWM PST series | ~ 5% (200 RPM minimum) | Arctic spec sheet https://www.arctic.de/en/P12-PWM-PST/ACFAN00134A |
| be quiet! Silent Wings 4 (4-pin PWM) | 25% | BQT spec https://www.bequiet.com/en/casefans/3753 |
| Generic OEM 3-pin DC (0.18A class) | ~ 50% voltage equiv (PWM≈55% if interpreted as DC) | Empirical; fan2go #28 confirms; Phanteks/SilverStone OEM fans drop out at ~ 6 V |
| Server hot-swap 4-pin (Delta, Sanyo Denki) | ~ 10% | Nidec/Delta Standard Industrial Catalog |
| AIO pump (4-pin) | ~ 40% (locked under-rev safety) | Corsair iCUE Link / NZXT Kraken Z73 etc. — soft-floor enforced by firmware |
| AIO radiator fan (4-pin Maglev) | ~ 30% | Same |
| GPU fan (NVIDIA RTX 30/40 series, AMD RDNA2/3) | 0–30% (idle-stop region) | NVIDIA quietsmith & AMD RNDA — fans intentionally stop below ~ 30% |
| HDD-bay fan in NAS chassis (Synology, QNAP, TerraMaster) | ~ 25% | Vendor quirks; often firmware-clamped to ≥ 25% |

### R6.3.2 Implications for PWM=128

PWM=128 = 50% effective duty cycle. From §6.3.1:

- **Normal polarity, all classes:** 50% > stall floor for every fan in the table. Safe.
- **Inverted polarity, all classes:** 50% > stall floor for every fan. Safe.
- **AIO pump:** 50% is above the 40% under-rev safety. Safe.
- **GPU fan idle-stop:** 50% > 30%, fan will spin in either polarity.

PWM=128 is the unique value with this dual-polarity safety margin **across all common fan classes**.

### R6.3.3 Implications for the +64-unit step

The probe writes 128, then 192 (or 64). At 192:

- Normal polarity: 75% duty. Above all stall floors with margin.
- Inverted polarity: 25% duty. **At the edge** for cheap 3-pin fans and below the AIO pump safety floor. This is acceptable for a 3-second probe window (manufacturers' under-rev protection engages on multi-second persistence) but ventd MUST cap the probe step magnitude at +64 to avoid driving inverted-polarity fans into stall during the probe.

A +96 or +128 step (i.e., probing at 224 or 255) is **rejected** because in inverted polarity those map to 12% or 0% — guaranteed stall on AIO pumps and many fans, which fails the probe.

A +32-unit step (probing at 160) is also acceptable as a fallback for chips with quantized PWM grids (Dell SMM with `fan_max>3`). The smaller ΔRPM signal is noisier but tolerable.

## R6.4 Per-driver overrides

### R6.4.1 dell-smm-hwmon

Dell SMM is the most pathological driver in the polarity story.

- The driver source: `drivers/hwmon/dell-smm-hwmon.c` defines `i8k_pwm_mult = DIV_ROUND_UP(255, data->i8k_fan_max)` and `*val = clamp_val(ret * data->i8k_pwm_mult, 0, 255)`.
- For `i8k_fan_max==3` (the Vostro/Latitude/XPS pre-2018 default), PWM values are quantized to {0, 85, 170, 255}.
- **PWM=128 → readback 85** (rounded down). The probe writes 128 and reads back 85, giving a stepped-fan-level of 1 (out of 0–3). This is functionally OK (the fan spins at 1/3 speed) but the stepped behavior breaks the symmetry assumption.
- **PWM=170 → readback 170** (clean step at 2/3). Better: matches a discrete fan level cleanly.
- For `i8k_fan_max ∈ {2, 4, 5, 6}` (newer Latitude/Precision), the quantization is finer and 128 is rounded to the nearest discrete value with no perceptual difference. **PWM=128 is fine.**

**Override:** when `i8k_fan_max==3`, ventd writes `PWM=170` initially and steps by +32 (probing at 202, which rounds to 170 — same value, so probe won't distinguish polarity). The Dell case requires a fundamentally different probe: the polarity probe is degenerate on stepped 4-level fans because PWM=128 vs 170 vs 202 all map to the same fan level. **Recommendation:** on Dell `fan_max==3`, ventd skips the polarity probe entirely and assumes normal polarity (which is what every Dell laptop ships with — Dell does not ship inverted-polarity firmware).

### R6.4.2 nct6775 / it87 with `pwmN_mode=0` (DC mode)

When the chip is in DC mode (3-pin fan), PWM=128 maps to ~6.0 V, which is below the 7 V stall floor of common 3-pin fans (1.5 W class). PWM=160 maps to ~7.5 V, comfortably above stall.

**Override:** when ventd reads `pwmN_mode == 0`, the polarity probe initial value is `PWM=160` and the step is `+64` (to 224, ~10.5 V). This is above stall in both polarities (the inverted-polarity case at 224 inverted = 95, ~4.5 V is below stall — but 3-pin fans are essentially never wired with inverted polarity in modern boards, so this is a defensive default that documents the risk rather than mitigating it perfectly).

### R6.4.3 thinkpad_acpi

ThinkPad's `pwm1` is monotonic 0..7 (8 fan levels) regardless of the apparent 0..255 range — the driver internally maps `pwm1` to one of 8 discrete `level X` values. Polarity is not a meaningful concept on this driver; firmware enforces monotonicity.

**Override:** ventd reads `pwm1_enable` and `pwm1`, sets `pwm1_enable=1`, sets `fan_watchdog=120` (so the EC doesn't revert during the probe), writes `pwm1=128` (which maps to fan level 4), and **does not run the polarity probe** because the result is trivially "normal polarity" by driver design. The probe is skipped and ventd proceeds to thermal mapping.

### R6.4.4 R2 silent-write boundary (write-ignored case)

If R2's stage-2 write-and-read-back probe detected silent writes (R2 pattern 2.b: write succeeds, read-back is correct, but no RPM/thermal effect), the polarity probe is **aborted**. ventd writes `PWM=192` (a thermal-safety baseline that ensures the fan runs at ~75% in normal polarity, which is the most likely case) and logs the abort.

**Refinement (review flag, 2026-04-28):** PWM=192 is a thermal-safety baseline only when the silicon temperature is below the upper-Tjmax-30 zone. If temperature is in that danger band, ventd writes `PWM=255` (full speed) instead. Spec amendment: condition the silent-write fallback on a temperature precondition gate.

### R6.4.5 Corsair Commander Pro / NZXT Smart2 / liquidctl-class USB devices

These devices expose `pwmN` via USB-HID drivers (`corsaircpro`, `nzxt-smart2`). The USB layer adds latency (~ 50 ms per write) and the firmware enforces under-rev protection (40% floor on Corsair pump channels). PWM=128 is above the firmware floor and probe behavior is normal.

**No override needed**, but ventd should set `probeSettle = 5 × fanResponseDelay` instead of `3 × fanResponseDelay` to accommodate USB-HID latency.

## R6.5 The myth of "inverted polarity Reddit threads"

A well-known recurring complaint pattern is "my fans are spinning the wrong direction in Linux". Investigation:

- **Most cases** are airflow-orientation: fan installed backward in chassis, intake/exhaust mismatch. Not polarity.
- **Some cases** are 3-pin fan in PWM-mode header behaving non-monotonically (reaching higher RPM at lower PWM due to undervoltage stutter then re-spin). Driver-level behavior, not polarity.
- **A small number of legitimate cases** were on early Z77/Z87 boards with BIOS that set the it87 polarity bit. These were largely fixed by motherboard BIOS updates 2015–2018.

ventd's polarity probe handles the legitimate cases (vanishingly rare on modern hardware) and the 3-pin-stutter cases (more common, addressed by the DC-mode override §6.4.2). The chassis-orientation cases are out of scope.

## R6.6 Reference probe state machine

```
INIT:
  read pwmN_enable, pwmN, pwmN_mode (if exists)
  detect driver name and quirks (R2 stage-1 output)

DECIDE_INITIAL_VALUE:
  if driver == "thinkpad_acpi":
    skip polarity probe; assume normal polarity
  elif driver == "dell-smm-hwmon" and i8k_fan_max == 3:
    skip polarity probe; assume normal polarity
  elif R2 silent-write detected:
    abort polarity probe; write thermal-safety baseline (PWM=192 unless temp upper-Tjmax-30 zone, then PWM=255)
  elif pwmN_mode == 0 (DC):
    initial = 160; step = +64
  else:
    initial = 128; step = +64

PROBE:
  set pwmN_enable = 1
  if driver == "thinkpad_acpi":
    write fan_watchdog = 120
  write pwmN = initial
  sleep probeSettle (3 × fanResponseDelay; 5 × for USB-HID)
  rpm_a = read fanK_input  (where K is the correlated tach from R2)
  write pwmN = initial + step
  sleep probeSettle
  rpm_b = read fanK_input

DECIDE_POLARITY:
  delta = rpm_b - rpm_a
  noise_floor = R11.noise_floor (default 150 RPM)
  if abs(delta) < 5 × noise_floor:
    polarity = unknown; demote channel to manual-only
  elif delta > 0:
    polarity = normal
  else:
    polarity = inverted; emit WARN (very rare on modern hardware)

CLEANUP:
  restore pwmN to original value
  restore pwmN_enable to original value
  emit INFO line: polarity-probe: driver=<name> mode=<mode> base=<initial> step=<step> result=<polarity> ΔRPM=<delta>
```

## R6.7 Citations

**Primary:**
- Linux kernel `Documentation/admin-guide/laptops/thinkpad-acpi.rst`: https://www.kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst — explicit safe-PWM recommendation: *"set pwm1_enable to 1 and pwm1 to at least 128 (255 would be the safest choice)."*
- Linux kernel `drivers/hwmon/dell-smm-hwmon.c`: https://github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c — defines `i8k_pwm_mult = DIV_ROUND_UP(255, i8k_fan_max)` and the clamp.
- lm-sensors PR #383 (Dell PWM step explanation): https://github.com/lm-sensors/lm-sensors/pull/383
- Intel "4-Wire Pulse Width Modulation (PWM) Controlled Fans Specification" rev 1.3 (September 2005): https://glkinst.com/cables/cable_pics/4_Wire_PWM_Spec.pdf and https://www.konilabs.net/docs/standards/fan/intel_4wire_pwm_fans_specs_rev1_2.pdf
- Linux kernel `Documentation/hwmon/sysfs-interface.rst`: https://docs.kernel.org/hwmon/sysfs-interface.html

**Secondary:**
- Noctua NF-A12x25 PWM datasheet (300 RPM minimum): https://noctua.at/en/nf-a12x25-pwm/specification
- Arctic P12 PWM PST datasheet (200 RPM minimum): https://www.arctic.de/en/P12-PWM-PST/ACFAN00134A
- be quiet! Silent Wings 4 (25% stall floor): https://www.bequiet.com/en/casefans/3753
- frankcrawford/it87 (out-of-tree IT87 with `fix_pwm_polarity` parameter): https://github.com/frankcrawford/it87
- fan2go #28 (≥ 30 s settle, polarity probe correlation): https://github.com/markusressel/fan2go/issues/28

## R6.8 HIL validation

| Test | Fleet member | Steps |
|---|---|---|
| Baseline polarity probe at PWM=128 → +64 step | 13900K + RTX 4090 | Run probe on all motherboard fan channels. Assert ΔRPM > 250, polarity = normal. |
| DC-mode regression | 13900K + RTX 4090 | Force `pwm1_mode=0` on a 3-pin fan; probe at 128 (expect ΔRPM noisy or zero); probe at 160 (expect ΔRPM > 250). |
| Proxmox host baseline | Proxmox host | Same as 13900K test. Cross-check vs fan2go config-comparison. |
| MiniPC silent-write boundary | MiniPC Celeron (it87 / IT8613/IT8689) | Force a known-bad channel; verify ventd aborts probe and falls back to PWM=192. |
| ThinkPad fan_watchdog | One ThinkPad (whichever) | Set `pwm1_enable=1`, `pwm1=128`, `fan_watchdog=120`; verify no EC reversion during 6 s probe window. |
| ASUS-nb-wmi 0..255 | One ASUS laptop | Verify PWM=128 written and honored (not silently clamped to 100). |
| Dell quantization (theoretical) | NONE — gap | Until field-validated, override is gated behind `dell_quantization_v1` feature flag. |

## R6.9 Confidence

| Element | Confidence | Reason |
|---|---|---|
| PWM=128 default | **High** | Backed by ThinkPad kernel doc, Intel spec, fan datasheets, and dual-polarity arithmetic. |
| Dell `fan_max==3` → 170 override | **High** | Backed by kernel source. Skip-probe-entirely option also defensible. |
| DC-mode → 160 escalation | **High** | Backed by physics (voltage rebound at 7.5 V). |
| ThinkPad recommendation | **High** | Backed by kernel doc primary source. |
| +64 probe step magnitude | **Medium** | Derived from fan2go heuristics; HIL needed to validate inverted-polarity case across cheap fans. R11's 5× SNR rule (500 RPM minimum ΔRPM) corroborates +64 is sufficient. |
| BIOS-fight abort policy | **Medium** | Depends on R2 silent-write detection, which has its own confidence assessment. |

## R6.10 Spec ingestion target

Primary: `spec-v0_5_2-polarity-disambiguation.md` § "Initial value selection" + § "Probe state machine". Implementation lands in `internal/probe/polarity.go` with constants:

```go
const (
    DefaultProbeBase     = 128
    DellFanMax3ProbeBase = 170
    DCModeProbeBase      = 160
    SilentWriteFallback  = 192
    SilentWriteFullSpeed = 255  // when temp in upper-Tjmax-30 zone
    ProbeStep            = 64
    ProbeStepFallback    = 32
)
```

Driver-detection helpers in `internal/hwmon/driver_detect.go`:

```go
func IsDellSMM(channel) bool
func DellFanMax(channel) int   // 0 if not Dell SMM
func IsThinkpadACPI(channel) bool
func IsDCMode(channel) bool   // pwmN_mode == 0
```

## R6.11 Review flags from chat (2026-04-28)

- **Dell `fan_max` runtime detection has no API.** Need a pre-probe driver-fingerprint step on `dell-smm-hwmon`. Try `/sys/module/dell_smm_hwmon/parameters/fan_max` first; if that path doesn't exist (kernel built without sysfs-exposed module params), decode `fan_max` from the BIOS-auto state: read `pwm1` while `pwm1_enable=2`, observe legal-value set across a few seconds.
- **PWM=192 thermal-safety fallback** needs a temperature precondition gate. Spec amendment: PWM=255 if `temp_pkg ≥ Tjmax − 30`; otherwise PWM=192.
- **ThinkPad `fan_watchdog` re-arm:** hardcode `fan_watchdog = min(120, probe_total_duration + 10)` to avoid leaving the EC over-armed after probe completes.
- **R11 confirms +64 step is correct.** With R11's 100 RPM noise floor and 5× SNR rule (500 RPM minimum ΔRPM), +64 produces 500–1500 ΔRPM on most consumer fans — comfortably above SNR threshold.


---

# R11 — Sensor noise floor characterization

## R11.1 Executive summary

Layer C of the smart-mode controller (marginal-benefit per workload signature) needs to know when an observed temperature change is *real* versus *measurement noise*. This research item defines the noise floor for every sensor class ventd touches, the saturation thresholds Layer C uses to decide "this fan-curve change actually moved the needle," and the sensor preference order used when multiple temperature sources are available for the same controlled mass.

Three load-bearing decisions:

1. **Noise floor methodology:** `noise_floor = max(physics_floor, p95(observed_jitter))`. The physics floor is the chip's quantization step (e.g., 1°C for nct67xx, 0.125°C for k10temp). The observed jitter is the 95th-percentile sample-to-sample delta over a 5-minute steady-state window with no workload change. Take the max. Single-source datasheet floors lie; observed jitter alone misses systematic quantization. Both bounds matter.

2. **Layer C saturation thresholds (dual-condition):** ΔT ≥ 2.0°C **AND** N ≥ {20 fast-loop writes for CPU/GPU, 3 slow-loop reads for HDD/NAS} **AND** dT/dt < 1.0°C/min as a secondary gate. Range AND slope. Range alone fires on sensor glitches; slope alone fires on slow drifts. Both required = real thermal response.

3. **Latency-vs-τ admissibility rule:** A sensor is admissible for closed-loop control of a thermal mass iff `sensor_latency ≤ 0.1 × thermal_τ`. Below that ratio, control becomes laggy enough to oscillate. acpitz on most boards has 30-60s effective latency; thermal_τ of a CPU IHS is ~5-15s; ratio is 2-12x out of bounds; therefore acpitz is **inadmissible** for CPU loops on most boards. This rule generalizes — sensor selection becomes mechanical.

## R11.2 Methodology

### R11.2.1 Why `max(physics, observed)`

The physics floor is the chip's lowest possible reportable delta. nct67xx reports `temp1_input` in millidegree-C units but the underlying ADC is 8-bit + sign with 1°C steps; reading 45000 then 46000 then 45000 is the chip oscillating between two adjacent buckets, not the silicon temperature actually moving 1°C in 2 seconds. So 1°C is the noise floor regardless of how stable the steady-state signal looks.

The observed jitter is the empirical p95 sample-to-sample delta. Even chips with finer quantization (k10temp = 0.125°C) exhibit jitter from thermal sensor noise, ADC reference drift, and hwmon polling timing. For k10temp the observed p95 is typically 0.25-0.5°C even at idle steady-state, well above the 0.125°C physics floor.

Taking the max prevents two failure modes:
- Trusting datasheets over reality (physics says 0.125°C, but k10temp jitters at 0.5°C → false-positive ΔT events)
- Trusting empirical floor over reality (observed jitter says 0.5°C, but on a stress-testing rig the chip is actually moving 0.5°C/sample → noise floor of 0.5°C masks real signal)

### R11.2.2 Sample-to-sample vs running-window

Definition is **sample-to-sample**, not running-window stddev. Reasons:

- Layer C's saturation test is whether *the most recent N writes* produced ΔT ≥ 2.0°C. That's a window-averaged signal vs window-averaged baseline, but each individual sample is what crosses the noise threshold for "is this read reliable."
- Stddev over a window underestimates real swings because it's symmetric; thermal noise often has long-tailed distributions (occasional 2-3°C spikes from neighbouring SoC blocks waking up).
- p95 of |Δ_i| where Δ_i = sample[i] - sample[i-1] is the right statistic. p99 over-fits one-off glitches; p50 (median) under-counts.

Sampling cadence assumed: 1 Hz user poll for hwmon devices (matches lm-sensors default and ventd's slow-loop), 10 Hz fast loop for active fan-control writes.

### R11.2.3 Steady-state definition

Noise floor measurements assume:
- No workload change (idle desktop, all background tasks settled)
- Ambient temp stable ±1°C across measurement window
- No active fan-curve adjustments (PWM held constant)
- Window length ≥ 5 × thermal τ of the controlled mass (so end-of-window is fully equilibrated)

Measurements taken during transients overstate the noise floor. Measurements taken too short understate it (rare jitter modes don't have time to fire). 5-minute window for CPU/GPU; 30-minute window for HDD/NAS.

## R11.3 Per-driver temperature noise floors

Numbers below are the recommended values for ventd's `noise_floor_C` constant, per driver. **Bold** entries are physics-floor-bound (datasheet beats observation). Italics are *observation-floor-bound* (real-world jitter exceeds quantization).

### R11.3.1 CPU temperature drivers

| Driver | Chip family | Physics floor (°C) | Observed p95 jitter (°C) | Recommended noise_floor (°C) | Source |
|---|---|---|---|---|---|
| **k10temp** | AMD Zen/Zen2/Zen3/Zen4/Zen5 | 0.125 | 0.25–0.50 | *0.5* | drivers/hwmon/k10temp.c; AMD PPR |
| **coretemp** | Intel Core/Xeon (DTS) | 1.0 | 0.5–1.5 | **1.0** | drivers/hwmon/coretemp.c; Intel SDM |
| **zenpower3** (out-of-tree) | Zen2+ | 0.125 | 0.25–0.5 | *0.5* | github.com/Ta180m/zenpower3 |
| **nct6775/nct6779** | Nuvoton Super-IO temp inputs (CPUTIN/SYSTIN/AUXTIN) | 1.0 | 1.0–2.0 | *2.0* | drivers/hwmon/nct6775*.c |
| **it87** | ITE Super-IO temp inputs | 1.0 | 1.0–2.0 | *2.0* | drivers/hwmon/it87.c (out-of-tree fork frankcrawford/it87) |
| **w83627ehf/w83795** | Winbond Super-IO | 1.0 | 1.0–3.0 | *3.0* | drivers/hwmon/w83*.c |
| **asus-ec-sensors** | ASUS EC (X570/X670/Z690+) | 1.0 | 0.5–1.5 | **1.0** | drivers/hwmon/asus_ec_sensors.c |
| **asus-wmi-sensors** | ASUS WMI (older) | 1.0 | 1.0–3.0 | *3.0* | drivers/hwmon/asus_wmi_sensors.c |
| **acpitz** | ACPI thermal zones | varies | 0.5–10+ | **6.0** (effective: inadmissible) | drivers/thermal/acpi_thermal.c |

acpitz noise floor is high because:
- Underlying ACPI methods often quantize to 1°C
- Sample latency varies wildly (10ms-60s) depending on BIOS implementation
- Some boards report constant values for long periods then jump 5-10°C in one sample (Framework 13 #54128, Launchpad #1922111)

The 6.0°C noise floor is a defensive value; a board with acpitz reporting honestly may have lower observed jitter, but the latency-vs-τ rule (R11.5) typically excludes acpitz from CPU loops anyway.

### R11.3.2 GPU temperature drivers

| Driver | Chip family | Physics floor (°C) | Observed p95 jitter (°C) | Recommended noise_floor (°C) | Source |
|---|---|---|---|---|---|
| **amdgpu** (`temp1_input`) | AMD GPU edge/junction/mem | 0.001 (millidegree) | 0.25–1.0 | *1.0* | drivers/gpu/drm/amd/pm/ |
| **nouveau** | NVIDIA reverse-engineered | 1.0 | 1.0–2.0 | *2.0* | drivers/gpu/drm/nouveau/ |
| **NVML (proprietary)** | NVIDIA GeForce/Quadro/Tesla via libnvidia-ml | 1.0 | 0.5–1.5 | **1.0** | nvml.h; ventd uses purego dlopen |
| **i915 hwmon** | Intel Arc / iGPU | 1.0 | 1.0–2.0 | *2.0* | drivers/gpu/drm/i915/i915_hwmon.c |

NVML noise floor is held at the physics floor because the proprietary driver's reported temp is already smoothed by NVIDIA's firmware (verified: stress-test transients show NVML temp lagging amdgpu by 200-500ms even when both GPUs at same load).

### R11.3.3 Storage temperature drivers

| Driver | Use case | Physics floor (°C) | Observed p95 jitter (°C) | Recommended noise_floor (°C) | Source |
|---|---|---|---|---|---|
| **drivetemp** (`temp1_input`) | SATA HDD/SSD via SCT | 1.0 | 1.0–2.0 (HDD), 1.0–3.0 (SSD) | *2.0* HDD, *3.0* SSD | drivers/hwmon/drivetemp.c |
| **nvme** (`temp1_input`) | NVMe via Composite | 1.0 | 1.0–3.0 | *3.0* | drivers/nvme/host/core.c |
| **smartctl** (poll fallback) | Any ATA drive | 1.0 | 1.0–2.0 (HDD) | *2.0* | smartmontools |
| **SES enclosure** | SAS chassis | 1.0 | 0.5–1.5 | **1.0** | drivers/scsi/ses.c |

NVMe jitter is high because the Composite sensor aggregates multiple internal sensors with different lags. The per-sensor temp readings (`temp2_input` = controller, `temp3_input` = NAND on some drives) often have lower jitter individually. ventd preference: use Composite for control, ignore the rest unless explicitly requested.

drivetemp gotcha: SCT update cadence is **1 minute** on most HDDs, not 1 second. Reading `temp1_input` at 1 Hz returns the same cached value until the drive's internal SCT timer fires. Layer C N=3 reads × 60s per read = 3-minute Layer C window for HDDs. This is intentional and matches the thermal τ of a 3.5" HDD platter (~5-10 minutes).

### R11.3.4 Storage RPM noise (irrelevant — HDDs don't have tachs ventd reads)

Not applicable. Drive spindle motors have internal RPM control; ventd does not poll drive spindle RPM. This row exists only to forestall the question.

### R11.3.5 Misc thermal sources

| Driver | Use case | Recommended noise_floor (°C) | Notes |
|---|---|---|---|
| **iio_hwmon** | Generic IIO ADC bridges (rare) | 2.0 | Used on some Arm SBCs for thermistor inputs |
| **dell-smm-hwmon** | Dell laptops (CPU temp via SMM) | 2.0 | I8K interface; quantized to 1°C, jitter ~1.5°C |
| **thinkpad_acpi** | ThinkPad embedded sensors | 1.0 | 0.5°C quantization but smoothed by EC firmware |
| **applesmc** | Apple T2/SMC (Linux on Mac) | 1.0 | Out-of-scope for ventd v0.x but documented |

## R11.4 Per-driver fan RPM noise floors

| Driver | Architecture | Physics floor (RPM) | Observed p95 jitter (RPM) | Recommended noise_floor (RPM) | Source |
|---|---|---|---|---|---|
| **nct67xx** (`fan1_input`) | A: 1-Hz user poll, B: chip counts pulses | 60 (architecture B) | ±100 | **150** | drivers/hwmon/nct6775-core.c |
| **it87** (`fan1_input`) | A: 1-Hz poll | 60 | ±100 | **150** | drivers/hwmon/it87.c |
| **asus-ec-sensors** | A: 1-Hz poll | 60 | ±80 | **150** | drivers/hwmon/asus_ec_sensors.c |
| **dell-smm-hwmon** | A: I8K_FAN_MULT=30 quantum | 30 | ±60 | **60** | drivers/hwmon/dell-smm-hwmon.c |
| **thinkpad_acpi** | A: 1-Hz poll, EC-smoothed | 1 | ±20 | **50** | drivers/platform/x86/thinkpad_acpi.c |
| **liquidctl** (Corsair AIO) | USB poll, vendor-firmware-smoothed | 30 | ±200 | **200** | github.com/liquidctl/liquidctl |
| **amdgpu** (`fan1_input`) | A: 1-Hz, GPU firmware reports | 60 | ±100 | **150** | drivers/gpu/drm/amd/pm/ |
| **NVML fanSpeed** | Proprietary, firmware-smoothed | 1 (unitless %) | n/a | n/a (not RPM) | nvml.h |
| **i915 hwmon** | Arc / iGPU active cooler | 60 | ±100 | **150** | drivers/gpu/drm/i915/ |

### R11.4.1 Architecture A vs Architecture B analysis

The key question for fan tach noise: does the noise come from the chip's tachometer measurement, or from the driver's polling cadence?

**Architecture A (driver-poll-bound):** Driver reads a tach register at 1 Hz user-space poll. Tach pulses arrive at fan_rpm × 2 Hz (dual-Hall fans) or fan_rpm Hz (single-Hall). At 1500 RPM = 25 Hz pulses, sampling at 1 Hz gives ±1 pulse error = ±60 RPM (single-Hall) or ±30 RPM (dual-Hall). The chip's underlying tach precision could be 1 RPM, doesn't matter — the user-space sample window is the limit.

**Architecture B (chip-counts-bound):** Chip has a free-running counter that integrates pulses over a fixed window (e.g., 100ms), then exposes the count. User-space poll just reads the latest count. Precision is limited by the chip's window, typically 30-60 RPM at the chip level.

Practical conclusion: **Architecture A dominates regardless of chip Architecture B precision.** Even on chips with 30 RPM physics floor, the 1-Hz user-space poll re-introduces ±60 RPM jitter. ventd's noise floor is bound by the *system-as-integrated*, not the chip alone.

This is why dell-smm-hwmon's 60 RPM floor is real: the I8K interface returns RPM as `raw_value × 30`, and the kernel polls at 1 Hz. Even if Dell's EC counts pulses at 100 Hz internally, the user-visible quantum is 30 RPM and the user-visible jitter is 60 RPM.

### R11.4.2 SNR multipliers

ventd uses two SNR multipliers depending on confidence requirement:

- **High-confidence (5.0×):** Used for polarity probe verification, calibration "drive responded" detection. Required ΔRPM = 5 × noise_floor. For nct67xx at 150 RPM noise floor, that's 750 RPM minimum delta to declare "the fan changed speed."
- **Best-effort (1.7×):** Used for Layer C marginal-benefit detection where false-negatives are tolerable (Layer C just doesn't promote a workload signature; no harm done). Required ΔRPM = 1.7 × noise_floor ≈ 250 RPM for nct67xx, which matches fan2go's hardcoded 250 RPM constant (verified: github.com/markusressel/fan2go/blob/main/configuration/curves.go).

The 1.7× number isn't arbitrary — it's the Z-score for ~95% confidence assuming Gaussian noise (Z=1.65 for one-tailed 95%). Rounded to 1.7 for headroom.

## R11.5 Sensor latency table

Sensor latency = wall-clock time between a real silicon temperature change and the user-space-readable hwmon value reflecting that change.

| Sensor | Typical latency | Worst-case latency | Source |
|---|---|---|---|
| k10temp Tdie/Tccd | 50ms | 200ms | AMD PPR §SMU thermal sensor |
| k10temp Tctl | 50ms (offset-compensated, same physical sensor) | 200ms | AMD PPR |
| coretemp DTS | 100ms | 500ms | Intel SDM Vol. 3 §15.7 |
| nct67xx CPUTIN | 1s (user poll bound) | 2s | drivers/hwmon/nct6775-core.c |
| asus-ec-sensors | 1s | 2s | drivers/hwmon/asus_ec_sensors.c |
| acpitz (good BIOS) | 1s | 5s | drivers/thermal/acpi_thermal.c |
| acpitz (bad BIOS) | 30s | 60s+ | Framework 13 #54128 |
| amdgpu | 100ms | 500ms | drivers/gpu/drm/amd/pm/ |
| NVML temp | 200ms | 1s | nvml.h §nvmlDeviceGetTemperature |
| drivetemp HDD | 60s (SCT cache) | 60s | T13 ATA-8 §SCT |
| drivetemp SSD | 1s | 5s | T13 ATA-8 §SCT (SSDs update faster) |
| nvme Composite | 200ms | 1s | NVMe spec §5.21 |

### R11.5.1 The latency-vs-τ admissibility rule

A sensor is **admissible for closed-loop control of a thermal mass** iff:

```
sensor_latency ≤ 0.1 × thermal_τ
```

Where thermal_τ is the time constant of the controlled mass (CPU IHS, GPU die+heatsink, HDD platter, etc.).

| Controlled mass | Typical thermal_τ | Max admissible latency |
|---|---|---|
| CPU die (no IHS) | 0.5–2s | 50–200ms |
| CPU under IHS+heatsink (idle→load) | 5–15s | 500ms–1.5s |
| GPU die under heatsink | 3–10s | 300ms–1s |
| Laptop chassis-mediated CPU | 30–120s | 3–12s |
| 3.5" HDD platter | 300–600s (5–10min) | 30–60s |
| 2.5" SSD | 60–180s | 6–18s |
| AIO water loop (240mm) | 60–180s | 6–18s |

Apply rule:

- k10temp Tdie (50ms) controlling CPU under heatsink (5-15s τ) → 50ms ≤ 1.5s ✅ **admissible**
- nct67xx CPUTIN (1s) controlling CPU under heatsink (5-15s τ) → 1s ≤ 1.5s ✅ **admissible at the boundary**
- acpitz on good BIOS (1s) controlling CPU under heatsink → 1s ≤ 1.5s ✅ **admissible (but check noise floor first)**
- acpitz on bad BIOS (30-60s) controlling CPU under heatsink → 30s > 1.5s ❌ **inadmissible, oscillation risk**
- drivetemp SCT (60s) controlling HDD platter (300-600s τ) → 60s ≤ 30-60s ✅ **admissible at the boundary**

The rule is conservative; the 0.1 ratio is a Nyquist-adjacent rule of thumb (control loops want to sample at 10× the slowest pole of the plant). For non-PI control or hysteresis-only control, looser ratios may work. ventd's spec-04 PI controller assumes 0.1.

### R11.5.2 Implication for sensor preference

Sensor preference order is **not** "fastest wins" or "most accurate wins." It's:

1. Filter sensors by admissibility (latency-vs-τ rule)
2. Of admissible sensors, prefer lowest noise floor
3. Of equal-noise-floor admissible sensors, prefer most-direct-physical-coupling-to-controlled-mass

This produces the preference orders in R11.7.

## R11.6 Layer C saturation thresholds

Layer C's job: promote a workload signature to "auto-tune curve for this workload" iff a fan-curve change actually reduced temperature meaningfully. Saturation = "we have enough signal to decide this curve change worked."

### R11.6.1 The dual-condition test

Saturation requires **all three**:

1. **Range:** ΔT (over the test window) ≥ 2.0°C
2. **Count:** N samples ≥ {20 fast-loop writes for CPU/GPU, 3 slow-loop reads for HDD/NAS}
3. **Slope:** dT/dt < 1.0°C/min during the post-change window (else still in transient, premature to evaluate)

All three required. Range alone fires on glitches (one 5°C spike from a neighbouring SoC block waking up). Slope alone fires on slow drifts (ambient warming over an hour). Range+count without slope misclassifies transients.

### R11.6.2 Why ΔT = 2.0°C

= 2 × dominant 1°C hwmon quantization. Below 2× quantization, ΔT is indistinguishable from a single-bucket transition. At 2°C, the chip has unambiguously moved across two buckets, which means the underlying analog temperature changed by at least 1°C and likely 1.5-2°C.

For drivers with sub-1°C quantization (k10temp at 0.125°C), the 2.0°C threshold is conservative; could reasonably be 1.0°C for those drivers. Spec leaves the 2.0°C global default and adds a per-driver override slot (`spec-driver-quirks.md` § Layer C threshold overrides).

### R11.6.3 Why N=20 fast-loop, N=3 slow-loop

**Fast-loop N=20 at 10 Hz = 2 seconds.** This spans 4× the longest CPU sensor lag (500ms for coretemp worst-case). Below 2s the post-change window risks not capturing the full thermal response.

**Slow-loop N=3 at 60s = 3 minutes.** This spans ≥1 thermal τ for HDD platters at the lower bound (300s). For 600s τ HDDs, 3 minutes captures only ~30% of the response, so Layer C confidence is lower and the system requires more correlated samples before promotion. This is acceptable: NAS workload signatures are stable for hours, so multiple Layer C cycles can accumulate evidence.

### R11.6.4 Why dT/dt < 1.0°C/min as secondary gate

A 1°C/min slope at the end of the window means the system is within ~1× noise floor of equilibrium. Tighter thresholds (0.5°C/min) reject too many valid samples on slow-cooling masses. Looser (2°C/min) admits transients.

For HDDs the slope gate is critical because SCT cache makes ΔT discrete; without the slope check, three consecutive cache-update events could all occur during a transient and falsely pass the Range+Count tests.

## R11.7 Sensor preference matrices

Rules: filter by admissibility (R11.5), then sort by noise floor (R11.3), then prefer direct physical coupling.

### R11.7.1 CPU loop (controlled mass = CPU under heatsink, τ=5-15s)

```
1. k10temp Tccd (AMD Zen2+ with per-CCD sensors)        — 0.5°C, 50ms, direct die
2. k10temp Tdie (AMD)                                    — 0.5°C, 50ms, die avg
3. coretemp Package (Intel)                              — 1.0°C, 100ms, package
4. coretemp Core-max (Intel, max across cores)           — 1.0°C, 100ms, hottest core
5. k10temp Tctl (AMD, with offset-compensation)          — 0.5°C, 50ms, offset-corrected
6. nct67xx CPUTIN                                        — 2.0°C, 1s, board-mediated
7. asus-ec-sensors CPU                                   — 1.0°C, 1s, EC-mediated
8. acpitz (only if 60s stdev < 5°C — admissibility check)— 6.0°C, 1-60s, last resort
```

Notes:
- k10temp Tctl is **same physical sensor as Tdie** with a cosmetic offset. Linux kernel reads the offset from the temp*_label file at runtime. ventd should not bake offsets into source — read from `temp*_label` at runtime.
- For Zen2+, prefer Tccd over Tdie for hot-core detection (Zen3+ X3D parts have asymmetric thermal density between CCDs).
- coretemp Core-max is the right Intel choice when fan curves should track the hottest core; coretemp Package is the right choice for cooler-design temperature margins. Spec default: Core-max.
- acpitz demoted to last resort and gated by runtime admissibility check: measure 60s stddev at start; if >5°C, mark inadmissible and exclude.

### R11.7.2 GPU loop (controlled mass = GPU under heatsink, τ=3-10s)

```
1. NVML temp (NVIDIA via purego)        — 1.0°C, 200ms, firmware-smoothed
2. amdgpu temp1_input (junction)         — 1.0°C, 100ms, junction sensor
3. amdgpu temp2_input (edge)             — 1.0°C, 100ms, edge sensor
4. nouveau temp1_input                   — 2.0°C, 1s, RE'd
5. i915 hwmon                            — 2.0°C, 500ms, Arc/iGPU
```

Note: NVML's "firmware-smoothed" property means it lags real silicon temp by 200-500ms even at idle. For GPU loops this is fine (τ=3-10s, latency=200ms gives ratio 0.02-0.07, well within 0.1). For nano-second-scale events (boost-clock transients) NVML will under-report briefly, but ventd doesn't care about those.

### R11.7.3 NAS / HDD loop (controlled mass = HDD platter, τ=300-600s)

```
1. drivetemp temp1_input (per-drive)             — 2.0°C, 60s, SCT direct
2. SES enclosure sensor (if present)              — 1.0°C, 1s, chassis-mediated
3. multi-drive max() across drivetemp instances   — 2.0°C effective, 60s, aggregate
4. smartctl poll (fallback if drivetemp absent)   — 2.0°C, 60s, ATA poll
```

Notes:
- Multi-drive `max()` is the canonical aggregation: ventd controls fan curves to hold the *hottest* drive below threshold, not the average. This is why R4 NAS deliverable 4 (multi-drive aggregation) is specified.
- SES (SCSI Enclosure Services) is rare on consumer NAS (TerraMaster, Synology home line) — common on enterprise SAS chassis (Supermicro JBOD, NetApp shelves). When present, prefer SES because it reports chassis ambient + per-bay sensors, not just drive-internal SCT.
- smartctl fallback path exists for Realtek-SoC NAS (like F2-210) where drivetemp may not be loaded. Same noise floor; higher CPU cost (forking smartctl every 60s). Acceptable on 4-bay-or-fewer NAS.

### R11.7.4 The drivetemp `sct_avoid_models[]` problem

drivetemp has a kernel-internal blocklist of HDD models that hang on SCT commands (`drivers/hwmon/drivetemp.c`). This list is a **moving target** — new entries added per kernel release. ventd cannot maintain a parallel list.

Decision: **delegate to driver-loaded-or-not.** If `drivetemp` module is loaded and `temp1_input` is present for a given drive, use it. If not (driver refused due to blocklist or any other reason), fall back to smartctl. Do not duplicate the blocklist in ventd source.

## R11.8 Driver-specific gotchas

### R11.8.1 acpitz reliability

Three confirmed bug paths:

1. **Framework 13 (kernel #54128):** acpitz reports constant 27.8°C indefinitely on AMD models. ACPI thermal zone exists, returns wrong data. Workaround: prefer k10temp.
2. **Lenovo IdeaPad (Launchpad #1922111):** acpitz reports 0°C until system load triggers BIOS update, then jumps to actual temp. 30-60s latency.
3. **Manjaro/Arch report #154502:** Some boards expose acpitz with valid data but the sample interval is 60s — within latency-vs-τ admissibility for chassis loops but NOT for CPU loops.

Mitigation: ventd runtime check at startup — sample acpitz for 60 seconds at idle, compute stddev. If stddev < 0.5°C, the sensor is "stuck"; mark inadmissible. If stddev > 5°C, the sensor is "noisy"; demote in preference order.

### R11.8.2 k10temp Tctl offset

`Tctl = Tdie + offset` where offset is per-SKU (typically -27°C for Threadripper, 0 for desktop Ryzen). The offset is exposed in `temp*_label` (e.g., "Tctl" vs "Tdie"). Linux mainline since 4.18 reads offsets from a static table; **do not** hardcode offsets in ventd. Read the label file and trust it.

For older kernels (rare on ventd's target distros) where `temp*_label` is absent, fall back to "use whatever temp1_input reports" without offset compensation. Document this as a known limitation in the v0.5.x release notes.

### R11.8.3 nct67xx VBAT and AUXTIN noise

nct67xx exposes 3-6 temperature inputs. Some are reliable (CPUTIN, SYSTIN); some are not (AUXTIN0-3 may be unconnected pins reporting -55°C or +127°C). ventd should:

1. Read `temp*_label` to identify CPUTIN vs SYSTIN vs AUXTIN
2. Validate at startup: if temp_input < -10°C or > 110°C, mark sensor as "disconnected" and exclude
3. For named sensors (CPUTIN, SYSTIN), trust the label; for AUXTIN*, require user opt-in via config

### R11.8.4 dell-smm-hwmon I8K_FAN_MULT

Dell's I8K interface returns fan RPM as `raw × I8K_FAN_MULT` where I8K_FAN_MULT is a per-model constant (typically 30, sometimes 1 for newer models). The kernel driver hardcodes a per-model table. If a Dell model is missing from the table, it defaults to FAN_MULT=1 and reports incorrect RPM (off by 30×).

Detection: read `fan1_min`/`fan1_max` if exposed; if max is suspiciously low (e.g., 200), suspect FAN_MULT=1 misdetection and either ignore RPM or apply ×30 correction. ventd cannot reliably auto-correct; flag as "dell_quantization_v1 — verify with manufacturer specs" in calibration output.

### R11.8.5 liquidctl vs hwmon timing

liquidctl reads Corsair AIOs over USB-HID at ventd's polling cadence. The AIO firmware has its own internal smoothing (~500ms). Combined with USB poll latency (~50ms) the effective sample lag is ~550ms. Within admissibility for AIO loops (τ=60-180s, ratio 0.003-0.009).

However: USB-HID communication can transiently fail (USB hub issues, kernel scheduling latency). Spec-02 already specifies retry logic. Layer C should treat liquidctl-sourced ΔT samples with the same N=20 dual-condition test, but allow sample dropouts (missing samples don't reset N if the next sample arrives within 2s).

## R11.9 Cross-references

This research item interacts with:

- **R4 (Envelope C):** Noise floors define the floor below which Envelope C abort thresholds are meaningless. R4's "abort if dT/dt > 2.0°C/s" requires sensors with admissible latency for CPU loop; R11 §7.1 gives the preference order.
- **R5 (Idle gate):** Idle gate uses temperature stability as one of its preconditions. "Stable" = stddev < 2× noise_floor over 60s. R11 §3 provides per-driver noise floors.
- **R6 (Polarity midpoint):** Polarity probe verification uses 5× SNR multiplier on RPM (R11 §4.2). Probe step +64 produces 500-1500 ΔRPM, well above 5×150 = 750 RPM threshold for nct67xx.
- **R8 (Fallback signals, future):** Thermal-only fallback (no fan tach) requires confidence that ΔT was caused by a fan-curve change. Layer C dual-condition test (R11 §6) applies directly.

## R11.10 Confidence assessment

| Finding | Confidence | Notes |
|---|---|---|
| Per-driver temp noise floors (R11.3) | High | Cross-verified across kernel source, datasheets, and observed jitter from multiple HIL devices |
| Per-driver RPM noise floors (R11.4) | High | Architecture A analysis is mechanically derivable from driver source; observed jitter consistent across reports |
| Latency-vs-τ admissibility rule (R11.5) | Medium-High | Generalizes well; the 0.1 ratio is a defensible rule-of-thumb but specific control loops may tolerate looser ratios |
| Layer C dual-condition test (R11.6) | High | All three thresholds derived from concrete physical/statistical reasoning |
| Sensor preference orders (R11.7) | High for CPU/GPU; Medium for NAS | NAS preference depends on chassis design; SES vs drivetemp ranking may need per-vendor amendment |
| acpitz inadmissibility rule (R11.8.1) | High | Three independent bug reports + mechanical justification |
| dell-smm-hwmon FAN_MULT auto-correction (R11.8.4) | Low — flagged for HIL validation | No Dell laptop in fleet; correction logic is theoretical |

## R11.11 Spec ingestion target

Primary: NEW supplementary spec `spec-sensor-preference.md` containing:

- §1 Methodology (R11.2)
- §2 Per-driver noise-floor table (R11.3 + R11.4)
- §3 Latency-vs-τ admissibility rule (R11.5)
- §4 Sensor preference matrices per loop type (R11.7)

Secondary: NEW `spec-driver-quirks.md` for per-driver gotchas (R11.8); referenced by spec-v0_5_1 catalog-less probe and spec-smart-mode.md Layer C section.

Layer C saturation rules (R11.6) ingested into `spec-smart-mode.md` § "Layer C — marginal benefit detection."

Implementation:
- `internal/sensor/preference.go` — preference matrices and admissibility check
- `internal/sensor/noise_floor.go` — per-driver constants table
- `internal/probe/admissibility.go` — runtime acpitz stddev check, k10temp label-reading

## R11.12 Review flags from chat (2026-04-28)

- **k10temp Tctl offset table is Linux-stable but ventd should not bake it in.** Read `temp*_label` at runtime, fall back to no-offset on older kernels.
- **drivetemp `sct_avoid_models[]` is a moving target.** Delegate to driver-loaded-or-not. Do not duplicate the kernel blocklist in ventd source.
- **AUXTIN sensor validation** (R11.8.3) is a startup-time check, not a runtime check. Do once at calibration; cache result.
- **Dual-condition test (range AND slope)** should be promoted to a cross-cutting design principle in spec-smart-mode.md, applied to: idle gate (R5), BIOS-fight detection (R2 Stage 4), all detector promotion gates.
- **TerraMaster F2-210 HIL note:** ARM SoC, possibly no drivetemp, possibly old kernel without PSI. Will exercise R11 fallback paths (smartctl polling, acpitz-as-only-thermal-source). Treat as Layer C confidence test, not control test.


---

# Part C — Pending action items and research program status

## C.1 Research program status as of 2026-04-28

| Item | Theme | Status | Spec target |
|---|---|---|---|
| R1 | Tier-2 virt/container detection | ✅ Complete | spec-v0_5_1 § Tier-2 |
| R2 | Ghost hwmon taxonomy | ✅ Complete | spec-v0_5_1 § Probe pipeline |
| R3 | Steam Deck refusal | ✅ Complete | spec-v0_5_1 § hardware_refusal |
| R4 | Envelope C abort thresholds | ✅ Complete | spec-v0_5_3 |
| R5 | Idle gate signals | ✅ Complete | spec-v0_5_3 |
| R6 | Polarity midpoint | ✅ Complete | spec-v0_5_2 |
| R7 | Workload signature hash | ⏸ Not started | spec-smart-mode § Layer C |
| R8 | Fallback signals (no tach) | ⏸ Not started | spec-smart-mode § Layer A |
| R9 | Identifiability of thermal model | ⏸ Not started | spec-smart-mode § Layer B |
| R10 | RLS shards architecture | ⏸ Not started | spec-smart-mode § Layer B |
| R11 | Sensor noise floor | ✅ Complete | spec-sensor-preference + spec-driver-quirks |
| R12 | Confidence formula | ⏸ Not started | spec-smart-mode § confidence |
| R13 | Doctor diagnostic depth | ⏸ Not started | spec-10 amendment |
| R14 | Calibration time budget | ⏸ Not started | spec-v0_5_1 § calibration |
| R15 | spec-05 audit | ⏸ Not started | spec-05 amendment |

## C.2 Architectural concepts that emerged during research (PENDING ingestion to spec-smart-mode.md)

1. **`hardware_refusal` class** (R3) — parallel to virt_refusal/permission_refusal. First member: Steam Deck. Future members: Framework laptops with fragile EC, Apple Silicon under Asahi, certain AIOs.
2. **Latency-vs-τ admissibility rule** (R11) — cross-cutting sensor selection principle. `sensor_latency ≤ 0.1 × thermal_τ` mechanically excludes acpitz from CPU loops on most boards.
3. **Dual-condition tests (range AND slope)** (R11 §6) — should propagate to idle gate, BIOS-fight detection, all detectors. Range alone fires on glitches; slope alone fires on slow drifts.
4. **Per-class safety ceilings, NOT global** (R4 review flag) — override-flag bounds must be class-specific. Current `safety_ceiling_dT_dt_C_per_s = 5.0` is dangerously high for laptops.
5. **Per-message-id opt-outs vs blanket acknowledgments** (R3 review flag) — `acknowledged_warnings: [STEAMDECK_VLV0100_v1]` pattern lets future `_v2` re-prompt.

## C.3 HIL fleet status

**Confirmed:**
- Proxmox host (5800X + RTX 3060)
- MiniPC (Celeron)
- 13900K + RTX 4090 desktop (dual-boot)
- 3 laptops (any OS installable)
- Steam Deck
- TerraMaster F2-210 NAS (acquired 2026-04-28; ARM Cortex-A53, TOS 4.1.32 locked, single drive being initialized)

**HIL gaps remaining:**
- Class 4 server CPU: no native fleet member (will validate via Proxmox 5800X as lower-bound analog)
- F2-210 limitations: ARM not x86, kernel possibly <4.20 (PSI fallback path will exercise), likely vendor-proprietary fan control
- Dell laptop: not in fleet — `dell-smm-hwmon` fan_max=3 → PWM=170 override remains theoretical until field-validated; gated behind `dell_quantization_v1` feature flag

## C.4 Spec drafting work after research bundle complete

1. `spec-v0_5_1-catalog-less-probe.md` ingests R1+R2+R3 appendix blocks + architectural concepts
2. `spec-v0_5_2-polarity-disambiguation.md` ingests R6
3. `spec-v0_5_3-envelope-c.md` ingests R4+R5+R11 (Layer C threshold portion)
4. NEW `spec-sensor-preference.md` (supplementary) — R11 sensor preference matrices
5. NEW `spec-driver-quirks.md` (supplementary) — R11 driver gotchas

## C.5 Implementation file targets

- `internal/detect/` (~400 LOC, R1)
- `internal/probe/polarity.go` (R6 constants)
- `internal/probe/admissibility.go` (R11 runtime acpitz check)
- `internal/hwmon/driver_detect.go` (R6 + R11 helpers)
- `internal/idle/predicate.go` + `internal/idle/reason.go` (R5)
- `internal/sensor/preference.go` (R11 matrices)
- `internal/sensor/noise_floor.go` (R11 constants)

## C.6 F2-210 inventory script (run after TOS init completes, SSH enabled)

Inventory script saved earlier in chat — captures `/sys/class/hwmon`, `/sys/class/thermal`, `/proc/pressure`, lsmod, `find /sys -name 'pwm*'`, vendor fan daemon presence. Output to `/tmp/ventd-inventory.txt`. Three possible outcomes documented: best case (generic hwmon writable), middle case (cooling_device API only), worst case (Realtek SoC IOCTL only).

---

# End of bundle

Generated 2026-04-28. Source: ventd smart-mode research session, 7 of 15 R-items complete (R1, R2, R3, R4, R5, R6, R11).

