# R28 — Master priority table for fan-control failure modes

**Synthesis of:** R28 Agents A–H (9 docs) + 2026-05 follow-ups (calibration-hostile,
diag-bundle-threat-model, web-ui-audit). 12 inputs total.

**Audience:** Phoenix planning the next sprint. Every row defends against the
question "would I want this on the next release?" Prevalence is weighted
ruthlessly — a one-off ASRock B450 BIOS bug ranks below a generic ThinkPad EC
issue that hits every Lenovo Linux user.

---

## 1. Executive summary

Across 12 sources the corpus converges on five load-bearing findings:

1. **The recovery-card decision tree is the single highest-ROI surface ventd
   has shipped.** Twelve of the highest-impact failure classes (ThinkPad
   `fan_control=1`, it87 ACPI conflict, NCT6687D in-tree replacement, ASUS
   B650/X670 nct6775 path, AMD OverDrive bit, Dell iDRAC ≥3.34 revocation,
   HPE iLO licensing, Secure Boot signing, kernel-headers absence,
   apt/dnf lockdown, AppArmor denial, PWM-readback BIOS override) all
   already classify into the existing `internal/recovery/classify.go` enum.
   Most of the next sprint is *catalog rows*, not new mechanisms.

2. **Kernel version gates obsolete a quarter of the catalog.** Agent E2's 130
   rows show that on kernel ≥6.6 LTS, ten of the most-recommended
   workarounds (`acpi_enforce_resources=lax`, `force_id=0xd450`, dell-smm
   `force=1` for Latitude 7320 / Precision 7540 / XPS 9370 / OptiPlex
   7050, dual-fan ThinkPad whitelist, IT87952E force_id, IT8732F phantom
   fans, ASUS B650/X670 manual modprobe, ThinkPad fan2_input=65535
   sentinel) fire on no modern install. ventd's catalog should branch on
   `uname -r` to suppress dead workarounds.

3. **OEM mini-PCs are the long tail.** Beelink, MINISFORUM, GMKtec,
   AceMagic, Topton, GEEKOM, AOOSTAR, CWWK ship proprietary EC firmware
   with no in-tree driver. The OOT `it5570-fan` (passiveEndeavour) covers
   a subset; the rest are monitor-only. DMI-vendor pattern matching is
   the single biggest catalog gap not yet addressed.

4. **Vendor-revoked features are a permanent class, not a fixable bug.**
   Dell iDRAC9 ≥3.34.34.34 (raw 0x30 0x30 rejected), HPE iLO Standard
   tier, NVIDIA datacenter SKUs (H100/H200/A100), AMD Instinct MI200/300X,
   HP EliteBook ≥G10 SMM lock — these will never have an OS-side fix.
   ventd's job is "detect and explain," not "thrash trying to find a
   working module." Operator-impact 5, prevalence high in their segments.

5. **Calibration safety has eight unmodelled hostile-fan classes.** The
   2026-05 follow-up surfaced stiction, non-monotonic Smart-Fan, hysteresis,
   range-selective BIOS override, dummy tach, PWM quantisation, AIO-pump
   floor, and thermal-throttle-during-sweep — none currently bound by a
   RULE. RULE-ENVELOPE-14 (PWM readback) catches one of the BIOS-override
   sub-cases but misses the others. This is the calibration-hardening
   sprint's load-bearing scope.

---

## 2. Master priority table

Ranked by `(operator-impact × prevalence) / implementation-cost`. Status flags
already-shipped work; "Stage 1 in flight" matches the four open items Phoenix
flagged; "Stage 2 candidate" feeds §3.

Cost: **S** ≤200 LOC, **M** 200–800, **L** 800+ or HIL-gated.
Impact: 1 (cosmetic) – 5 (data loss / hardware risk / install fails).
Prevalence: low (single SKU) / med (board family) / high (every Linux user
on that vendor).

| Rank | Failure mode / catalog row | Source | Status | Cost | Impact | Prev | Suggested next step |
|---|---|---|---|---|---|---|---|
| 1 | ThinkPad `thinkpad_acpi fan_control=1` modprobe-option auto-writer | A,B,C,D,G,E2#42 | **Stage 1 in flight** | S | 5 | high | Land the modprobe.d auto-writer keyed on the existing ThinkPad classifier (#831). Detection done; only the writer is open. |
| 2 | it87 `ignore_resource_conflict=1` modprobe-option auto-writer (kernel ≥6.2) | A,B,D,E2#28 | **Stage 1 in flight** | S | 5 | high | Land the auto-writer; gate on kernel ≥6.2 to avoid the GRUB cmdline path. Replaces single most-recommended `acpi_enforce_resources=lax` reboot path. |
| 3 | nct6683 in-tree blacklist auto-writer when catalog selects nct6687d | A:NCT67InTreeReplaces, B,C,E2#19,#20,#21 | **Stage 1 in flight** | S | 5 | high | After #822 catalog fix, the apply path needs to write `blacklist nct6683` and `modprobe -r nct6683` so the OOT module wins on next boot. |
| 4 | HPE iLO / iDRAC vendor-revoked proactive detect-and-explain | C,E2#110,#111,#112,H | **Stage 1 in flight** | S | 5 | med | iLO Standard / iDRAC ≥3.34.34.34 firmware-version probe via `ipmitool mc info`; surface recovery card before any write attempt. RULE-IPMI-3 already refuses; this adds the proactive doctor card. |
| 5 | RULE-ENVELOPE-14 extension: time-delayed BIOS revert (re-read at t+1, t+5, t+15) | calib-hostile §4 | Stage 2 candidate | M | 5 | high | EC override engages 3–10 s after write; current single-readback misses it. Rule extension RULE-ENVELOPE-14b. |
| 6 | RULE-ENVELOPE-14 extension: range-selective override (compare RPM-vs-expected, not register readback) | calib-hostile §4 | Stage 2 candidate | M | 5 | high | EC slams 0–79 PWM to a floor while register readback returns the written value. Rule extension RULE-ENVELOPE-14c. |
| 7 | OEM mini-PC DMI-vendor pattern match → IT5570 OOT recommendation | H | Stage 2 candidate | S | 4 | high (in segment) | DMI sys_vendor ∈ {Beelink, MINISFORUM, GMK, AceMagic, Topton, GEEKOM, AOOSTAR, CWWK}: surface OOT `it5570-fan` install or monitor-only fallback. |
| 8 | Pump-class auto-detect + hard PWM=60% floor (RULE-PUMPFLOOR-20) | calib-hostile §7 | Stage 2 candidate | S | 5 | med | Detect `AIO_PUMP` label / 1500–3500 RPM range / liquidctl class; enforce floor across calibration AND runtime. AIO pump stalling is hardware-damaging. |
| 9 | Datacenter GPU detect-and-refuse (NVIDIA H100/H200/A100/L40S, AMD MI200/MI300X) | H | Stage 2 candidate | S | 4 | med (in segment) | Extend NVML probe + ROCm probe to match device-name regex; force OutcomeMonitorOnly. Avoids RULE-NVML-HELPER-EXIT-01 firing on every tick. |
| 10 | Vendor-daemon detection extension (asusd, system76-power, tccd, slimbookbattery, jupiter-fan-control, nbfc, isw, mbpfan) | G,E2#118 | Stage 2 candidate | S | 4 | high | RULE-WIZARD-RECOVERY-11 covers some; expand to cover all eight. Detect-and-defer prevents two-controller race on every major Linux laptop OEM. |
| 11 | NixOS Nix-fragment emitter for modprobe.d / sensors.d | F:F2,F3, E2#123 | Stage 2 candidate | M | 5 | med | NixOS ignores imperative `/etc/modprobe.d/*.conf`; ventd must emit Nix module fragment for the operator to import. Detection done (#840); writer is open. |
| 12 | DT-fingerprint catalog: Pi 5 (Active Cooler), Pi 4 PoE+ HAT, Orange Pi 5/5+, Rock 5B, VisionFive 2 | H | Stage 2 candidate | M | 3 | med | Five DT entries via existing RULE-FINGERPRINT-06/07 matcher; opens ARM SBC tail. |
| 13 | RULE-STICTION-15: stddev(RPM)<1 over 3s with PWM in motion → spin-up pulse, then degraded mark | calib-hostile §1 | Stage 2 candidate | M | 4 | med | Sleeve-bearing fans stuck on lubricant polymerisation; thinkfan #58 confirmed. Spin-up pulse is reversible; abort+degrade is the safe recovery. |
| 14 | RULE-MONOTONICITY-16: dRPM/dPWM reversal beyond hysteresis → abort, refuse curve | calib-hostile §2 | Stage 2 candidate | S | 4 | med | Smart-Fan dual-zone EC reinterprets PWM mid-curve; learned curve would be wrong-direction. |
| 15 | RULE-DUMMYTACH-18: PWM=0 held >2s + RPM>0 + variance≈0 → mark RPM-blind, fall back to fixed-point control | calib-hostile §5 | Stage 2 candidate | S | 3 | med | 3-pin fans on 4-pin headers with synthesised tach. ZeroPWMSentinel range already runs; add the variance check. |
| 16 | RULE-THERMABORT-21: thermal_zone>85°C OR throttle flag during sweep → abort + retry serial | calib-hostile §8 | Stage 2 candidate | M | 5 | low-med | Thermal throttle during low-PWM phase makes calibration learn artificially low stall_pwm. Catalog hostile failure ventd doesn't currently catch. |
| 17 | RULE-WIZARD-RECOVERY-10 ThinkPad classifier | A,B,C,D,G | **Shipped** (#831) | — | 5 | high | Done. |
| 18 | Vendor-daemon + NixOS probes (RULE-WIZARD-RECOVERY-11/12) | F,G | **Shipped** (#832, #840) | — | 4 | high | Done. |
| 19 | AMD OverDrive probe (RULE-WIZARD-RECOVERY-13) | B,D,E2#74 | **Shipped** (#835) | — | 5 | high | Done. RDNA2/3/4 fan_curve EACCES without bit 14. |
| 20 | MS-7D25 → nct6687d catalog fix | A,C,E2#19 | **Shipped** (#822) | — | 5 | high | Done. |
| 21 | Probe-then-pick driver selection refactor | A,C | **Shipped** (#824) | — | 5 | high | Done. |
| 22 | Apply-monitor-only endpoint | A,B,F,H | **Shipped** (#838) | — | 4 | high | Done. |
| 23 | Container detection extension (Podman + systemd-nspawn) | F,B | **Shipped** (#836) | — | 4 | med | Done. RULE-PROBE-03 already covered Docker/LXC/k8s; extension covered podman/nspawn. |
| 24 | Sub-absolute-zero sentinel filter | C:framework-13-bios322, D:nouveau, calib-hostile §6 | **Shipped** (#837) | — | 4 | low-med | Done. Framework 13 BIOS 3.22 -274000°C; nouveau +511°C already covered by 150°C cap. |
| 25 | Reboot prompt UX | A,B,D | **Shipped** (#828) | — | 4 | high | Done. |
| 26 | Calibration UX rework | calib-hostile, A,B,F | **Shipped** (#826) | — | 4 | high | Done. |
| 27 | Asahi `macsmc-hwmon fan_control=1` doctor card | E2#79, H | Stage 2 candidate | S | 3 | med (in segment) | Asahi explicitly opt-in unsafe. Detect macsmc-hwmon; surface modprobe.d snippet + warning. |
| 28 | Steam Deck DMI dispatch (`Jupiter` LCD vs `Galileo` OLED) + jupiter-fan-control conflict refusal | H | Stage 2 candidate | S | 4 | low | Two-controller race; differing sensor maps. RULE-PREFLIGHT-CONFL-03 covers refusal; the DMI dispatch is what's missing. |
| 29 | Supermicro LCR threshold-tuning recovery card | C,D,E2#114,H | Stage 2 candidate | M | 4 | med (in segment) | Low-RPM Noctua pinned at full speed by BMC panic; ventd should detect and offer threshold-tuning before Envelope C. |
| 30 | dell-smm-hwmon DMI whitelist hardware-quirk catalog (XPS 9320/9500/9710/9300, Inspiron 580/3505 monitor-only) | E2#107,#108, G | Stage 2 candidate | M | 4 | med | Per-DMI overlay marking these EC-locked despite the DMI vendor matching a controllable family. |
| 31 | nzxt-kraken3 rate-limit (≤0.5 Hz pwm writes) | B,E2#85 | Stage 2 candidate | S | 3 | low | Curve writes >1 Hz lock up the device. ventd backend rate-limit. Affects spec-02 path. |
| 32 | Per-board kernel-version gate: ASUS B650/X670 modprobe nct6775 | C,D,E2#15,#17,H | Stage 2 candidate | S | 4 | high | DMI-match ASUS X570/B550/B650/X670/Z690/Z790 → always issue `modprobe nct6775` regardless of sensors-detect; kernel ≥6.3 native, kernel <6.3 advise upgrade. |
| 33 | RULE-AGG-DRIFT signal extension: thinkpad EC re-asserts BIOS curve | A,B,C,E2#103 | Stage 2 candidate | M | 4 | med | Detect deviation pattern post-PWM-write; surface as RULE-CALIB-PR2B-06 BIOS-overridden. Already partially handled; the signal needs to fire across more chip families. |
| 34 | Diag bundle redactor: Tailscale `tskey-…` regex + `/var/lib/tailscale/tailscaled.state` denylist | diag-threat §1 | Stage 2 candidate | S | 5 | med | Reversible auth keys at rest; high blast radius if leaked. |
| 35 | Diag bundle redactor: Cloudflare Tunnel `TunnelSecret` + `TUNNEL_TOKEN=` env files | diag-threat §2 | Stage 2 candidate | S | 5 | low | Single regex + denylist file. |
| 36 | Diag bundle redactor: K8s SA tokens + JWT regex (CVE-2024-3177) | diag-threat §3 | Stage 2 candidate | S | 5 | low-med | Denylist token files; regex JWT anywhere. |
| 37 | Diag bundle redactor: OAuth/GitHub PAT/AWS/Slack patterns in unit files + env | diag-threat §4 | Stage 2 candidate | M | 5 | med | High false-positive risk on the generic regex; needs a tiered match (specific patterns first). |
| 38 | Diag bundle redactor: WireGuard `PrivateKey`/`PresharedKey` regex (keep PublicKey) | diag-threat §7 | Stage 2 candidate | S | 5 | low | Single regex on `*.conf`/`*.key`. |
| 39 | Diag bundle redactor: systemd credentials (`/etc/credstore/**`, `/var/lib/systemd/credential.secret` host key) | diag-threat §6 | Stage 2 candidate | S | 5 | low | Host key leak is catastrophic; denylist path. |
| 40 | Diag bundle redactor: PostgreSQL/Redis/MySQL connection strings + .pgpass | diag-threat §11 | Stage 2 candidate | S | 4 | low | libpq URI regex + denylist files. |
| 41 | Diag bundle redactor: container registry creds (.docker/config.json, containers/auth.json) | diag-threat §12 | Stage 2 candidate | S | 4 | low | Base64 of user:password trivially reversible. |
| 42 | Web UI: CSP + security headers in server middleware | web-ui §S1 | Stage 2 candidate | S | 4 | high | RULE-UI-01 already forbids inline JS / CDN; CSP is the strict header that follows. Single middleware addition. |
| 43 | Web UI: login rate-limit / lockout (per-account exponential backoff) | web-ui §S2 | Stage 2 candidate | S | 4 | high | bcrypt cost 12 is correct; failed-attempt counter is the open piece. |
| 44 | Web UI: global `:focus-visible` for dashboard + calibration | web-ui §A2 | Stage 2 candidate | S | 3 | high | WCAG 2.4.7 / 2.4.11 violation; single CSS rule. |
| 45 | Web UI: retire `--fg3` as text colour; darken drifting-pill bg | web-ui §A1 | Stage 2 candidate | S | 3 | high | Colour-contrast fail (1.4.3); token rename + bg adjustment. |
| 46 | Web UI: tighten `prefers-reduced-motion` (animation-delay:0s, dash-fan-spin null) | web-ui §A3 | Stage 2 candidate | S | 3 | med | Vestibular safety; one CSS block. |
| 47 | Web UI: SameSite=Lax → Strict on session cookie | web-ui §S3 | Stage 2 candidate | S | 3 | high | Single-user admin app; no inbound deep-links. Origin/Referer + Strict is the OWASP-correct combo. |
| 48 | Acer Predator/Nitro NBFC profile dispatch by DMI | C,G | Stage 2 candidate | M | 3 | med (in segment) | Per-model NBFC JSON profile; OOT path documented across multiple distros. |
| 49 | Lenovo Legion `legion-laptop` OOT recommendation | C,G,E2#81 | Stage 2 candidate | S | 4 | med | Detect Legion DMI; refuse generic thinkpad-acpi auto-fix; surface legion-laptop install path. Kernel ≥7.1 brings in-tree `yogafan` (monitor only). |
| 50 | ASRock Rack ROMED8-2T: BMC owns fans, NCT decorative — refuse engagement | H | Stage 2 candidate | S | 4 | low | DMI-match these workstation server boards; force monitor-only with "configure fans in BMC web" recovery card. |
| 51 | Threadripper PRO WRX80E-SAGE: BMC-routed fans despite NCT6798D presence | H | Stage 2 candidate | S | 4 | low | Same pattern as #50. |
| 52 | Steam Deck OLED post-S3 `recalculate` quirk | E2#120, H | Stage 2 candidate | S | 3 | low | Resume hook writes 1 to /sys/class/hwmon/hwmonN/recalculate. Niche but easy. |
| 53 | Quanta D51B-1U byte-spacing in raw IPMI command | C,E2#116, H | Already correct | — | 2 | low | RULE-IPMI-1 implementation already passes argv-separated bytes. No work needed. |
| 54 | Fujitsu iRMC: refuse Dell OEM dispatch | C,E2#117, H | Stage 2 candidate | S | 3 | low | Vendor-detection branch in IPMI backend; refuse Dell-OEM raw on FUJITSU. |
| 55 | TrueNAS SCALE detection (Linux successor to ix_fan_control on FreeBSD) | H | Stage 2 candidate | S | 3 | low | Detect via /etc/version; surface SCALE-native install via TrueNAS App, not direct binary. |
| 56 | Unraid `ipmifan` plugin two-controller race | H | Stage 2 candidate | S | 3 | low | Detect via systemd unit name; refuse start. RULE-PREFLIGHT-CONFL-03 covers the mechanism; only the unit name list is open. |
| 57 | Star Labs StarBook: coreboot-managed; ventd reports firmware-managed | G | Stage 2 candidate | S | 2 | low | Detect via DMI; classify as firmware-managed monitor-only. |
| 58 | LG Gram `fan_mode` discrete-step backend (step_0_N, N=2) | B | Stage 2 candidate | S | 3 | low | New backend in HAL: /sys/devices/platform/lg-laptop/fan_mode 0/1/2. |
| 59 | Coretemp Tjmax lookup table for old Xeon 5200 / Core2 Duo | B,E2#101 | Stage 2 candidate | S | 2 | low | hwdb CPU model → Tjmax map; modprobe option writer. |
| 60 | k10temp socket-mismatch `force=1` | B,E2#100 | Stage 2 candidate | S | 2 | low | AM3-on-AM2+ combos; modprobe option writer. |
| 61 | Aquacomputer pump-config write refuse (driver design) | B,E2#84 | Already-correct | — | 4 | low | Driver explicitly read-only; ventd already cannot write. No work needed. |
| 62 | NCT6798/6799 pwm5/6/7_mode OOB on kernel <6.15 | E2#3 | Stage 2 candidate | S | 4 | low | Refuse pwm_mode writes for channels 5–7 on these chips when kernel <6.15. |

---

## 3. Stage 2 implementation candidates (top 15 to ship next)

Ranked by impact-prevalence-cost ratio, with concrete bindings, file paths,
LOC estimates, and HIL/test plans.

### S2-1. RULE-ENVELOPE-14b — time-delayed BIOS revert (re-read at t+1, t+5, t+15)

- **Rule binding:** `RULE-ENVELOPE-14b_DelayedRevertReadback`
- **LOC:** ~120 (probe loop + KV state + test fixture)
- **Files:**
  - `internal/envelope/envelope.go` — extend `probeStep` with delayed re-reads
  - `internal/envelope/envelope_test.go` — table-driven test, inject EC that
    reverts at t+5
  - `.claude/rules/envelope.md` — append RULE-ENVELOPE-14b
- **HIL:** MAG B850 / X870E (issue #826 reference); confirm three-sample
  divergence detection.
- **Test plan:** unit test injects writeFunc that returns the written value
  on first read and the EC default on the second; assert abort fires;
  assert KV state transitions to `aborted_C` with reason
  `bios_override_delayed`.

### S2-2. RULE-ENVELOPE-14c — RPM-vs-expected (range-selective override)

- **Rule binding:** `RULE-ENVELOPE-14c_RangeSelectiveOverride`
- **LOC:** ~180 (RPM expectation model + comparison)
- **Files:**
  - `internal/envelope/envelope.go` — add expectedRPM(pwm, polynomial) helper
  - `internal/envelope/envelope_test.go`
  - `.claude/rules/envelope.md`
- **HIL:** Gigabyte X670/B650 with Smart Fan 6 active.
- **Test plan:** seed an EC stub that accepts register writes 0–79 but pegs
  RPM at the 96-PWM equivalent; assert deviation detected before the sweep
  records a false stall_pwm.

### S2-3. RULE-PUMPFLOOR-20 — AIO-pump auto-detect + 60% floor

- **Rule binding:** `RULE-PUMPFLOOR-20_PumpClassFloor`
- **LOC:** ~150 (heuristic + clamp + test)
- **Files:**
  - `internal/hal/hwmon/pump_class.go` (new) — header-name + RPM-range heuristic
  - `internal/controller/safety_test.go` — extend RULE-HWMON-PUMP-FLOOR
  - `.claude/rules/hwmon-safety.md` — amend
- **HIL:** Corsair iCUE Commander (already in spec-02), Aquacomputer D5 Next.
- **Test plan:** synthetic channel labelled "AIO_PUMP" with curve output 30%;
  assert clamp to 60%. Inverse: labelled "CHA_FAN1" passes through.

### S2-4. OEM mini-PC DMI vendor heuristic + IT5570 recovery card

- **Rule binding:** `RULE-WIZARD-RECOVERY-14_OEMMiniPCMonitorOnly`
- **LOC:** ~200 (DMI list + recovery card + classifier extension)
- **Files:**
  - `internal/recovery/probe_oem_minipc.go` (new)
  - `internal/recovery/classify.go` — add ClassOEMMiniPCNoDriver
  - `internal/recovery/classify_test.go`
  - `.claude/rules/wizard-recovery.md`
- **HIL:** Beelink SER7 (Phoenix arr stack); MINISFORUM MS-01.
- **Test plan:** DMI fixture for each of {Beelink, MINISFORUM, GMK,
  AceMagic, Topton, GEEKOM, AOOSTAR, CWWK}; assert classifier returns
  ClassOEMMiniPCNoDriver and recovery card surfaces it5570-fan link.

### S2-5. Datacenter GPU detect-and-refuse

- **Rule binding:** `RULE-GPU-PR2D-09_DatacenterRefusesEngagement`
- **LOC:** ~120 (regex + probe + test)
- **Files:**
  - `internal/hal/gpu/nvml/probe.go` — extend writable-capability probe
  - `internal/hal/gpu/amdgpu/probe.go` — same for ROCm
  - `internal/hal/gpu/nvml/probe_test.go`
  - `.claude/rules/gpu-pr2d-09.md` (new)
- **HIL:** none required (synthetic NVML name fixture).
- **Test plan:** inject NVML deviceName="NVIDIA H100 PCIe" → assert
  capability `ro_unsupported` + reason "datacenter_gpu_firmware_locked".

### S2-6. RULE-STICTION-15 — sleeve-bearing stiction detection + spin-up pulse

- **Rule binding:** `RULE-STICTION-15_RotorStiction_SpinUpPulse`
- **LOC:** ~200 (variance window + recovery sequence)
- **Files:**
  - `internal/calibrate/stiction.go` (new)
  - `internal/calibrate/calibrate_test.go`
  - `.claude/rules/calibration-safety.md`
- **HIL:** thinkfan #58 reference (older ThinkPad). Synthetic-only is OK.
- **Test plan:** seed RPM samples with stddev<1 over 3s + PWM moving;
  assert spin-up pulse (PWM=255 for 4s) fires, then resume sweep; if RPM
  still flat → assert abort + degraded mark.

### S2-7. NixOS Nix-fragment emitter for modprobe.d / sensors.d

- **Rule binding:** `RULE-WIZARD-RECOVERY-15_NixOSEmitNixFragment`
- **LOC:** ~250 (NixOS detection already in #840; emitter + test)
- **Files:**
  - `internal/recovery/nixos_emit.go` (new)
  - `internal/recovery/classify.go` — branch on NixOS detection
  - `internal/recovery/nixos_emit_test.go`
- **HIL:** NixOS 25.05 VM.
- **Test plan:** fixture with `/etc/NIXOS` present; assert recovery card
  emits Nix expression `boot.extraModprobeConfig = ...` instead of
  imperative `/etc/modprobe.d/` write.

### S2-8. RULE-DUMMYTACH-18 — synthesised tach detection

- **Rule binding:** `RULE-DUMMYTACH-18_FakeTachOnPWMZero`
- **LOC:** ~100 (variance check + classifier flag)
- **Files:**
  - `internal/calibrate/dummytach.go` (new)
  - `internal/calibrate/calibrate_test.go`
- **HIL:** any 3-pin fan on a 4-pin header.
- **Test plan:** PWM=0 held >2s, RPM samples [1500,1500,1500,1500],
  variance≈0; assert channel marked RPM-blind, not phantom.

### S2-9. Vendor-daemon detection extension (asusd, system76-power, tccd, slimbookbattery, mbpfan, isw)

- **Rule binding:** extend RULE-WIZARD-RECOVERY-11 (`#832`) — add 6 unit names
- **LOC:** ~80 (additions to existing classifier)
- **Files:**
  - `internal/recovery/probe_vendor_daemon.go` — extend list
  - `internal/recovery/classify_test.go`
- **HIL:** none required.
- **Test plan:** systemd unit fixtures for each daemon; assert classifier
  returns ClassVendorDaemonRunning with daemon name in detail.

### S2-10. Diag bundle redactor: Tailscale + Cloudflare Tunnel + WireGuard

- **Rule binding:** new RULE-DIAG-PR2C-11/12/13
- **LOC:** ~250 (three regex sets + three denylist additions + tests)
- **Files:**
  - `internal/diag/redactor/primitives.go` — three new primitives
  - `internal/diag/redactor/redactor_test.go`
  - `internal/diag/bundle.go` — add denylist paths
  - `.claude/rules/diag-pr2c-11.md`/`12.md`/`13.md` (new)
- **HIL:** none required (synthetic fixture for each cred type).
- **Test plan:** seed bundle with `tskey-auth-…`, `TunnelSecret`,
  `PrivateKey=…`; assert all three are redacted in the assembled tarball
  (per RULE-DIAG-PR2C-02 self-check).

### S2-11. Web UI: CSP + security headers middleware

- **Rule binding:** new RULE-UI-05_CSPHeaders
- **LOC:** ~80 (middleware + test fixture)
- **Files:**
  - `internal/web/server.go` — add middleware
  - `internal/web/headers_test.go` (new)
  - `.claude/rules/ui.md`
- **HIL:** none required; HTTP test handler.
- **Test plan:** assert every response carries CSP, X-Content-Type-Options,
  Referrer-Policy, Permissions-Policy. HSTS only when TLS active.

### S2-12. Web UI: login rate-limit / lockout

- **Rule binding:** new RULE-AUTH-RATELIMIT_ExponentialBackoff
- **LOC:** ~150 (counter + sleep + lockout state)
- **Files:**
  - `internal/web/auth.go` — add per-account counter
  - `internal/web/auth_test.go`
  - `.claude/rules/web-ui.md`
- **Test plan:** ten failed logins within 15 min → soft lockout; backoff
  formula 2^n capped at 30s; verify per-account, not per-IP.

### S2-13. Per-board kernel-version gate: ASUS B650/X670 nct6775 dispatch

- **Rule binding:** RULE-HWDB-CAPTURE-04_KernelGateOnAsusAM5 (or extend
  catalog overlay)
- **LOC:** ~100 (catalog overlay rows + per-DMI dispatch test)
- **Files:**
  - `internal/hwdb/profiles-v1.yaml` — add ASUS X570/B550/B650/X670
    overlay entries with `min_kernel: 6.3`
  - `internal/recovery/classify.go` — branch on kernel version
  - `internal/recovery/classify_test.go`
- **Test plan:** synthetic DMI + kernel string fixtures; assert kernel <6.3
  surfaces "kernel upgrade recommended"; kernel ≥6.3 dispatches manual
  modprobe.

### S2-14. RULE-MONOTONICITY-16 — non-monotonic PWM→RPM detection

- **Rule binding:** `RULE-MONOTONICITY-16_RefuseNonMonotonicCurve`
- **LOC:** ~120 (sweep monotonicity check + abort path)
- **Files:**
  - `internal/calibrate/monotonicity.go` (new)
  - `internal/calibrate/calibrate_test.go`
- **HIL:** Smart-Fan-active board, e.g. Gigabyte B650 with Smart Fan 6.
- **Test plan:** sweep samples [0,500,1200,1500,1300,1100,900] with
  hysteresis-band 100 RPM; assert >1 reversal detected, abort fires,
  curve not persisted.

### S2-15. RULE-THERMABORT-21 — thermal throttle during sweep

- **Rule binding:** `RULE-THERMABORT-21_ThermalZoneAbortDuringSweep`
- **LOC:** ~150 (zone polling + abort + serial-retry)
- **Files:**
  - `internal/envelope/envelope.go` — add thermal_zone polling per step
  - `internal/envelope/envelope_test.go`
  - `.claude/rules/envelope.md`
- **HIL:** any AMD desktop under load.
- **Test plan:** stub thermal_zone returning 90°C on third step; assert
  abort with reason `thermal_throttle_during_sweep`; assert next retry
  pins other zones at full PWM.

---

## 4. Research gaps (Phoenix's four areas)

Each gap lists specific unanswered questions a future research agent could
resolve. Format: question + the kind of source that would answer it.

### 4.1 OOT driver auto-install matrix

We know **what to install** for each board family but the *install pipeline*
is uneven across distros. Open questions:

1. Which distros have a working DKMS+MOK signing chain that ventd can
   automate end-to-end without operator intervention? Fedora's
   `akmods + kmodgenca` divergence (F:gap §62) is documented but ventd
   does not currently dispatch on it. **Source:** Fedora packaging guide
   for SB-enforced kernels; akmods upstream README; ventd HIL on
   Fedora 41/42.
2. NixOS imperative writes are out (E2#123). Is there a sanctioned
   upstream pattern for ventd to *generate* a `configuration.nix`
   fragment that the operator imports? Or should ventd ship a
   `services.ventd` NixOS module in nixpkgs? **Source:** NixOS module
   conventions; nixpkgs PR review history for similar (`hardware.sensor`,
   `services.fancontrol`).
3. openSUSE Tumbleweed lacks the apt-postinst→DKMS-rebuild trigger chain
   (F#45). What event hook *does* fire on `zypper dup` of `kernel-default`
   that ventd can latch to? **Source:** openSUSE packaging guide;
   `kernel-default-extra` postinstall scripts.
4. Ubuntu HWE↔GA kernel-flavour switch silently orphans DKMS-built sensor
   modules. Does `dkms autoinstall -k $(uname -r)` plus a systemd
   `kernel-postinst.target` cover the full lifecycle, or is there still a
   reboot-window where the module is missing? **Source:** Ubuntu DKMS
   bug history; systemd post-kernel-upgrade hooks.
5. For each of `it87`, `nct6687d`, `asus-wmi-sensors`, `legion-laptop`,
   `framework_laptop`, `t2fanrd`, `steamdeck-dkms`: what's the canonical
   upstream URL and recommended branch (`main` vs `master` vs
   `h2ram-mmio`)? Do any require build-time configuration that
   ventd's auto-installer would need to set? **Source:** each repo's
   README + recent issue tracker; sweep of distro packaging spec files.

### 4.2 ARM/SBC tail

DT-fingerprint (RULE-FINGERPRINT-06/07) gives us the matcher. The catalog
content is incomplete:

1. Which ARM SBCs in active deployment have *any* in-tree fan-control
   driver? Confirmed coverage: Pi 4 + PoE+ HAT, Pi 5 + Active Cooler,
   Orange Pi 5/5+ on kernel ≥6.10, Rock 5B (partial), VisionFive 2
   (manual sysfs). **Source:** sweep of `arch/arm64/boot/dts/` and
   `arch/arm/boot/dts/` for `pwm-fan` references; cross-check with
   board-vendor docs for {Khadas, Banana Pi, Pine64, Radxa, FriendlyARM,
   Raspberry Pi, Orange Pi, Hardkernel ODROID, NanoPi, Beelink ARM,
   Libre Computer, BeagleBoard, RockPi}.
2. RK3588 boards (Rock 5B / Orange Pi 5 / Khadas Edge2 / NanoPi R6S):
   thermal-zone fan binding upstreamed in Linux 6.10. Which RK3588 boards
   have a working DT-overlay that ventd should match against? **Source:**
   armbian board configs; collabora/rockchip repo.
3. NXP i.MX8 boards (Phytec, Variscite, Toradex, NXP eval): any
   pwm-fan support in mainline? **Source:** kernel mailing list grep
   for "imx8 pwm-fan".
4. RISC-V SBCs (StarFive JH7110, SpacemiT K1, T-Head TH1520,
   SiFive HiFive Unmatched): what's the current state of thermal-zone
   binding upstream? Today only VisionFive 2 has a community-maintained
   userspace daemon. **Source:** RISC-V kernel ML; rvspace forum
   threads from last 6 months.
5. For each SBC class: what's the runtime expectation — directly drive
   `pwm1` via thermal_zone, or let the kernel's thermal framework own
   the curve and ventd act as monitor? **Source:** kernel
   `Documentation/thermal/` + per-driver design notes.

### 4.3 BMC matrix expansion (beyond Supermicro/Dell/HPE)

Three named vendors in `internal/hal/ipmi/`. Open: ASRock Rack, Gigabyte
server, Lenovo ThinkSystem, IBM (legacy), Tyan, Inspur, Huawei FusionServer,
Intel server boards, Quanta, Fujitsu Primergy.

1. ASRock Rack ROMED8-2T / X570D4U / X470D4U: NCT chip is decorative;
   BMC owns fans. Which OEM raw command set does the AST2500 accept?
   **Source:** ASRock Rack AST2500/2600 BMC manual; Level1Techs threads.
2. Gigabyte server boards (R-series / W771-Z00 / MZ32-AR0): different
   OEM codes per SKU. Some accept `0x3c 0x37` (Aorus); MZ32 has Redfish.
   What's the BMC family (AST2500/2600?) and the discriminator for
   ventd to dispatch on? **Source:** Gigabyte BMC user guides; gist
   referenced in H#42.
3. Lenovo ThinkSystem (XCC BMC) + IBM xSeries (legacy): Lenovo's XCC
   exposes a Redfish API and an OEM raw set. What's the fan-control
   command sequence? **Source:** Lenovo XCC Redfish reference;
   ThinkSystem IPMI guide.
4. Tyan / Inspur / Huawei FusionServer / Quanta / Intel S2600 series:
   what's the BMC vendor for each, and does any of them inherit
   Supermicro-style OEM commands or Dell-style? **Source:** vendor BMC
   user guides; ServeTheHome reviews.
5. Fujitsu Primergy iRMC: confirmed that Dell-OEM raw is rejected
   (E2#117). What's the Fujitsu-OEM command set, if any?
   **Source:** Fujitsu iRMC operating manual.
6. Across all of the above: should ventd ship per-vendor probe rules
   (`RULE-IPMI-N_VendorXOEMOK`) or fall back to Redfish-first?
   **Source:** Redfish coverage matrix; DMTF Redfish 2024 spec.

### 4.4 Vendor EC tail (Tuxedo / Slimbook / Framework)

OEM-forum coverage from Agent G is thin on these three:

1. Tuxedo: TCC (`tccd`) is the vendor daemon. What's the per-model fan
   path, and does ventd's "defer to vendor daemon" rule cover all
   Tuxedo SKUs (Pulse, InfinityBook, Stellaris, Aura, Polaris)?
   **Source:** Tuxedo Computers Linux community forum;
   tuxedocomputers/tuxedo-control-center repo issues.
2. Slimbook: `slimbookbattery` + Slimbook AMD Controller. What's the
   fan-control surface — does ventd need a backend or just defer?
   Different on Pro X / Executive / Essential / Titan. **Source:**
   slimbook.es support docs; Slimbook-Team GitHub.
3. Framework Laptop 13 / 16: cros_ec via `ectool` or
   `framework_laptop` DKMS. Kernel 6.18 added native cros_ec hwmon path;
   v7.1 adds target speed. What's the upgrade path for users on
   pre-6.18 kernels — which framework_laptop branch is current?
   **Source:** community.frame.work; DHowett/framework-laptop-kmod.
4. Framework 16 expansion bay (Radeon RX 7700S): expansion-bay fan
   control needs the expansion-bay shell firmware up-to-date. What's
   the ventd-side detection signal that the expansion bay is present?
   **Source:** community.frame.work expansion-bay threads.
5. Razer Blade: `razer-laptop-control` archived, `Razer-Control-Revived`
   is current (G#87). What's the Razer Blade EC per-model coverage
   matrix, and does `librazerblade` (battery-power override) introduce
   any safety concerns ventd needs to flag? **Source:** rnd-ash repo;
   Meetem/librazerblade.
6. System76: `system76-power` is the vendor daemon; `system76-acpi-dkms`
   provides hwmon binding. What's the version compatibility matrix
   between Pop!_OS 24.04, kernel 6.16+, and system76-power 1.x?
   Documented regression in G#72. **Source:** pop-os GitHub; Pop!_OS
   release notes.

---

## 5. Decision log — contradictions to resolve

These are places where one source disagrees with something already shipped
or with another source. Each item: what's contradicted, the conflict,
and a recommended action for Phoenix.

### 5.1 NCT6797D mapping (potential catalog defect)

- **Issue:** Agent C row 33 reports nct6687d misidentifies chip ID 0xd450
  as NCT6687 when the board really has NCT6797D — fan PWM writes
  silently no-op. Counter-evidence: Agent E2 row 9 says NCT6799D
  (chip ID 0xd802) was historically force_id'd to 0xd450 to make the
  in-tree nct6775 driver bind, and that's now native on kernel ≥6.4.
- **Conflict:** Same chip ID (0xd450) used as both:
  - "force_id target for NCT6799D on old kernels" (E2)
  - "false detection of NCT6797D as NCT6687" (Agent C)
- **Action:** Audit `internal/hwdb/profiles-v1.yaml` for any rows that
  resolve chip ID 0xd450 to NCT6687 — if so, those should be conditional
  on `kernel < 6.4` and the post-6.4 path should let nct6775 bind.
  This would have surfaced as the #822 fix's edge case if a user has
  an MSI Z690/Z790 with NCT6797D running an old kernel.

### 5.2 dell-smm-hwmon `restricted=` security model

- **Issue:** Agent C row 44 documents Linux Mint / Manjaro forum guidance
  to set `options dell-smm-hwmon restricted=0`. Agent B row 51 and
  Agent E2 row 109 explicitly mark this DANGEROUS — non-root writes are
  the kernel default-blocked behaviour for a reason.
- **Conflict:** Forum advice says "set restricted=0"; kernel docs say
  "never set restricted=0".
- **Action:** Confirm ventd never recommends `restricted=0` to the
  operator. The catalog row for Dell laptops should always set
  `restricted=1` (or omit the option) and rely on ventd running as root
  (or the SUID NVML helper for GPU writes; dell-smm-hwmon writes are
  always root-side). This is already aligned with B and E2; confirm in
  any dell-smm-hwmon recovery card text that ships.

### 5.3 `acpi_enforce_resources=lax` blast radius

- **Issue:** Multiple sources (B, C, D) recommend `acpi_enforce_resources=lax`
  as a kernel cmdline workaround for it87 / nct6798 ACPI conflicts.
  Agent C explicitly warns this can break `asus_atk0110` on some boards
  (Launchpad bug 953932); kernel maintainers warn it's not strictly safe
  (two drivers may bang the same I/O range).
- **Conflict:** Forum guidance says "blanket-apply"; kernel docs say
  "per-driver `ignore_resource_conflict=1` is preferred when the OOT
  it87 supports it."
- **Action:** ventd's recovery card chain should always prefer the
  per-driver path on kernel ≥6.2 (it87 has `ignore_resource_conflict=1`
  since v6.2 — E2#28). Only fall back to `acpi_enforce_resources=lax`
  when the per-driver option isn't available AND the user has confirmed.
  No-reboot per-driver path beats reboot-required cmdline path. This is
  Stage 1 in flight (#2 in master table) — confirm the implementation
  branches kernel version correctly.

### 5.4 NVML helper recursion guard

- **Issue:** No active contradiction; logged for completeness because
  the existing RULE-NVML-HELPER-RECURSION-01 is explicitly documented in
  rules and the datacenter-GPU detect-and-refuse work (S2-5) needs to
  not regress this guard. The new datacenter-detect path runs at probe
  time, before any helper invocation; it should not need the guard.
- **Action:** Confirm the new ClassDatacenterGPU branch returns
  `OutcomeMonitorOnly` *before* any write attempt enters the helper
  path, so the SUID helper is never invoked for a refused-engagement
  GPU.

### 5.5 ThinkPad fan2_input=65535 sentinel filter

- **Issue:** Agent E2 row 35 originally claimed the fix landed in v6.2
  (commit `a10d50983f7b`). Decision-log resolution (2026-05-03) verified
  via `git tag --contains` against torvalds/linux at v7.1-rc1: the commit
  actually landed in **v6.1** (Jelle van der Waa, 2022-10-19). On kernel
  ≥6.1 the userspace 65535 sentinel filter is redundant but harmless.
  ventd's RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE unconditionally rejects
  RPM > 10000 already.
- **Conflict:** No active conflict; the existing rule is correct on every
  kernel. Logged because the kernel-version-gated catalog cleanup
  (Finding 2 in the executive summary) might be tempted to remove the
  filter on ≥6.1 — don't, because the filter is generic across all
  drivers and the cost is zero.
- **Action:** Keep RULE-HWMON-SENTINEL-FAN-IMPLAUSIBLE unconditional.
  Document in the catalog that the per-driver workaround for ThinkPad
  pre-6.1 is no longer needed since the universal sentinel filter
  covers it.

### 5.6 Steam Deck DMI dispatch (`Jupiter` vs `Galileo`)

- **Issue:** Agent H#32 says the same `steamdeck-hwmon` driver covers both
  LCD ("Jupiter") and OLED ("Galileo") and `jupiter-fan-control v2+`
  auto-detects via DMI. Agent E2#119 says mainline kernels lack
  `steamdeck-hwmon` entirely — Valve patches or DKMS shim required.
- **Conflict:** Soft. Both can be true: same driver covers both DMI
  variants, but the driver may not be in mainline yet.
- **Action:** ventd's coupling/signature shards must NOT be shared
  cross-revision (per H summary §3) even though the driver is the same.
  When the catalog row for Steam Deck lands, the chip-fingerprint should
  include DMI product_name to keep the per-revision priors separate.

### 5.7 RDNA3/4 OD_FAN_CURVE rejected on certain SKUs

- **Issue:** Agent E2#69 (smu13) and E2#70 (smu14) say OD_FAN_CURVE is
  advertised but rejected by PMFW on certain RDNA3 SKUs when temp/PWM
  range is zero — `fan_curve` sysfs returns all-zero. Fix landed
  post-v6.18. Agent B row 60 covers RDNA4-on-kernel-<6.15 already
  (RULE-EXPERIMENTAL-AMD-OVERDRIVE-04).
- **Conflict:** No active conflict; just a note that the existing
  RULE-EXPERIMENTAL-AMD-OVERDRIVE-04 may need a sibling rule
  (`RULE-EXPERIMENTAL-AMD-OVERDRIVE-05`) for the all-zero-range case on
  kernel <7.1.
- **Action:** Add as a Stage-2 candidate (logged in master table row
  #62 as a kernel-version gate; can be promoted if RDNA3 affected SKU
  list is concrete). Not urgent — affects 7900 XTX/XT users only on a
  narrow kernel window.

### 5.8 IT8689E mainline support landing v7.1 (calendar slip)

- **Issue:** Agent E rows 14–17 and Agent E2 row 31 attribute the
  mainline IT8689E support patch to a 2026-03-22 author date,
  landing v7.1. Calendar shows v6.18 released 2025-11-30 and v7.0
  presumed Q1-Q2 2026. The "v7.1" assignment is medium confidence.
- **Conflict:** Could ship in v7.0 or v7.1 depending on how the
  hwmon-next merge window aligns.
- **Action:** ventd's catalog should treat as "kernel ≥7.1" today; if
  the actual landing tag turns out to be v7.0, downshift one release.
  Validate at next R28 refresh (q3 2026). Affects Stage 2 candidate #62
  only as a kernel-version gate.

---

*Synthesis complete. 12 inputs, 62 master-table rows, 15 Stage 2
candidates, 4 research-gap areas with 22 specific open questions, 8
decision-log items.*
