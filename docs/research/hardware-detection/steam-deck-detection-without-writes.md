# R3 — Steam Deck Detection Without Writes
## ventd v0.5.1 catalog-less probe — research document for Phoenix (solo dev)

---

## 1. Executive summary (read-this-first)

ventd's catalog-less probe must, **before issuing any PWM write**, identify Valve Steam Deck hardware (LCD "Jupiter" / OLED "Galileo" / any future revision) on **any** Linux distribution and **refuse to control fans**. The Steam Deck's embedded controller (EC) is not a generic ACPI thermal zone — it is the **VLV0100** ACPI device, owned by Valve's firmware, fronted by Valve's out-of-tree `steamdeck` / `steamdeck-hwmon` kernel modules, and managed in userspace by Valve's own daemon `jupiter-fan-control` (mirrored upstream at `gitlab.com/evlaV/jupiter-fan-control` and `github.com/Jovian-Experiments/jupiter-fan-control`). The EC firmware actively contends with userspace PWM writes — when the Valve daemon misbehaves, the EC silently reverts to its own curve ("Fan is using EC controls instead of your script since it's broken" — `ValveSoftware/SteamOS#1359`). Two userspace fan controllers fighting over `pwm1` will produce audible oscillation, thermal regressions, and possibly excess wear on a tiny single-fan handheld.

The defensible default is therefore: **read-only DMI fingerprinting first, kernel-module presence as a secondary corroborating signal, and a hard refusal with a clear pointer to `jupiter-fan-control`. No write is ever attempted on a system where `/sys/class/dmi/id/sys_vendor == "Valve"`.**

This document covers all variants requested (LCD, OLED, future revisions, SteamOS 3.x, Bazzite-Deck, ChimeraOS, Nobara-Deck, vanilla Arch/Fedora/Ubuntu on Deck hardware, dual-boot scenarios), enumerates every detection signal, presents the precedence chain, gives a comparison matrix across distros, recommends the exact refusal-message text, and ends with the spec-ready findings appendix block.

---

## 2. The hardware reality

### 2.1 Variants and their stable fingerprints

| Variant | Marketing name | APU codename | DMI `product_name` | DMI `product_family` (observed) | DMI `sys_vendor` |
|---|---|---|---|---|---|
| LCD (64GB / 256GB / 512GB, 2022) | Steam Deck | Aerith (AMD Van Gogh, TSMC N7) | `Jupiter` | `Sephiroth`-era kernels list `Aerith`; older firmware reports vary | `Valve` |
| OLED (512GB / 1TB, Nov 2023) | Steam Deck OLED | Sephiroth (AMD Van Gogh refresh, TSMC N6) | `Galileo` | `Sephiroth` | `Valve` |
| Future Deck 2 / Steam Brick (hypothetical) | TBA | TBA (FF7 naming convention strongly implied — community speculates Tifa/Cloud) | New string (will not be `Jupiter` or `Galileo`) | Likely new family string | Almost certainly still `Valve` |

The product-name evidence is firmly nailed down by multiple independent sources:

- **Bazzite SteamOS-Manager script** explicitly branches on `[ $MODEL = "Jupiter" ]` / `[ $MODEL = "Galileo" ]` after reading `/sys/class/dmi/id/product_name` (https://github.com/ryanrudolfoba/SteamDeck-BIOS-Manager/blob/main/steamdeck-BIOS-manager.sh).
- **Mainline kernel `drm_panel_orientation_quirks.c`** uses `DMI_EXACT_MATCH(DMI_SYS_VENDOR, "Valve")` + `DMI_EXACT_MATCH(DMI_PRODUCT_NAME, "Galileo")` (https://www.mail-archive.com/dri-devel@lists.freedesktop.org/msg499955.html) — the exact same idiom is used for the older Jupiter quirk.
- **Mainline kernel `drm_panel_backlight_quirks.c`** (Aug 2025 patch) — same `Valve`+`Jupiter` and `Valve`+`Galileo` matches (https://lists.freedesktop.org/archives/dri-devel/2025-August/522215.html).
- **HoloISO installer** detects on `product_name == "Jupiter"` and was patched for `Galileo` (https://github.com/HoloISO/holoiso/issues/855).
- **Phoronix** broke the Galileo / Sephiroth DMI strings from kernel 6.6 sound-driver patches: "a new DMI entry that is for Valve with a new product name of 'Galileo' and a DMI product family entry of 'Sephiroth'" (https://www.phoronix.com/news/Linux-6.6-Sound).
- **GamingOnLinux** community readers confirmed reading them via userspace: `cat /sys/class/dmi/id/product_name` returns `Jupiter` on LCD and the kernel patch added `Galileo` for OLED (https://www.gamingonlinux.com/2023/09/linux-updates-tease-valve-galileo-and-sephiroth-steam-deck-refresh-or-new-vr/page=4/).

### 2.2 The Valve EC and why writes are toxic

The Steam Deck's fan is controlled by an EC firmware that exposes a single ACPI device, **VLV0100**, via the DSDT. Andrey Smirnov's original kernel patch series — "platform/x86: Add Steam Deck driver" (Feb 2022, https://patchwork.kernel.org/project/linux-hwmon/patch/20220206022023.376142-1-andrew.smirnov@gmail.com/, also https://lwn.net/Articles/883961/) — describes it as "Steam Deck specific VLV0100 device presented by EC firmware. This includes but not limited to: CPU/device's fan control, Read-only access to DDIC registers, Battery temperature measurements, Various display related control knobs, USB Type-C connector event notification."

The driver registers a `hwmon` device named `"steamdeck_hwmon"` and exposes a `System Fan` channel with `pwm1` and `fan1_input`. **Crucially**, writes go through ACPI methods (`FANS` to set, etc.) that the EC firmware can override. Userspace tooling that bypasses the ACPI interface and pokes kernel memory directly (e.g., the Windows-side `steam-deck-tools`) explicitly warns: "It does direct manipulation of kernel memory to control usage the EC (Embedded Controller) and setting desired fan RPM via VLV0100. […] The memory addresses used are hardcoded and can be changed any moment by the BIOS update." (https://github.com/ayufan/steam-deck-tools/blob/main/docs/risks.md, https://github.com/CelesteHeartsong/custom-steam-deck-tools).

Empirically, when Valve's own `jupiter-fan-control` daemon misparses its config or crashes mid-run, the fan immediately reverts to the EC's internal curve — see `ValveSoftware/SteamOS#1359` ("Fan is using EC controls instead of your script since it's broken", https://github.com/ValveSoftware/SteamOS/issues/1359). The Bazzite community has the same observation phrased the other way: "if you disable [jupiter-fan-control], the motherboard itself is designed to set the fan speed […] so a failing to start jupiter-fan-control service just means you have a louder (and quieter) deck as they shipped" (Universal Blue support thread, https://www.answeroverflow.com/m/1196366584403480627).

That sentence captures the entire policy reason: **the EC is the source of truth; the userspace daemon is a refinement on top.** A second userspace controller (e.g., ventd) writing the same `pwm1` is racing the EC and Valve's daemon, and will produce inconsistent behaviour at best.

### 2.3 The userspace daemon ventd must NOT replace

- **Authoritative upstream**: `https://gitlab.com/evlaV/jupiter-fan-control` (created 2022-04-13; main branch, 30+ tags).
- **Public mirror (GitHub)**: `https://github.com/Jovian-Experiments/jupiter-fan-control` (mirror of the SteamOS source tarballs from `https://steamdeck-packages.steamos.cloud/archlinux-mirror/sources/jupiter-main`).
- **Installed paths on a stock SteamOS image**:
  - `/usr/share/jupiter-fan-control/fancontrol.py`
  - `/usr/share/jupiter-fan-control/jupiter-config.yaml` (LCD profile)
  - `/usr/share/jupiter-fan-control/galileo-config.yaml` (OLED profile)
  - `/usr/lib/systemd/system/jupiter-fan-control.service`
  - Polkit helper: `/usr/bin/steamos-polkit-helpers/jupiter-fan-control`
- **Activation toggle**: SteamOS exposes "Enable updated fan control" in GamepadUI; this toggle starts/stops the systemd unit (`ValveSoftware/steam-for-linux#12286`, https://github.com/ValveSoftware/steam-for-linux/issues/12286).
- **Build-time gate**: `fancontrol.py` reads DMI and **raises `NotImplementedError("DMI_ID Board Name not implemented! bios: {…} board: {…}")`** on any non-Jupiter/non-Galileo board — visible in the wild on a Legion Go S (`steam-for-linux#12286`) and an X570 desktop (https://www.answeroverflow.com/m/1204707288024219658). This is also Valve's *own* hardware-refusal pattern: it's exactly what ventd should mimic in spirit.
- **AUR adoption**: `jupiter-fan-control` and the kernel-side `steamdeck-dkms` are both packaged in the AUR (https://aur.archlinux.org/packages/jupiter-fan-control, https://aur.archlinux.org/packages/steamdeck-dkms) — meaning a vanilla Arch user on Deck hardware will frequently have these installed alongside ventd.

---

## 3. Detection signals — exhaustive enumeration

Each signal below is read-only, requires no privilege beyond standard user read on `/sys` and `/proc`, and is checked **before** any write attempt.

### 3.1 Primary: DMI strings (`/sys/class/dmi/id/`)

| File | Expected value (LCD) | Expected value (OLED) | Stability | Notes |
|---|---|---|---|---|
| `sys_vendor` | `Valve` | `Valve` | **Highest** — Valve uses this string everywhere; mainline kernel quirks pin to it; will almost certainly persist for a Deck 2 | Single-token check; cheap. |
| `product_name` | `Jupiter` | `Galileo` | High — multiple kernel quirks pin to these exact strings; would only change on a new SKU | Future Decks will use a *new* string but the same `sys_vendor`. |
| `product_family` | (varies, observed `Sephiroth` in some firmware on LCD; community reports `Aerith` historically — inconsistent) | `Sephiroth` | Medium — codename-keyed; reused across LCD+OLED in some BIOS revisions per the Phoronix patch reading | Use as corroboration only, not as a primary key. |
| `board_vendor` | `Valve` | `Valve` | High | Redundant with `sys_vendor`. |
| `board_name` | `Jupiter` | `Galileo` | High | Mirrors `product_name`. |
| `bios_vendor` | (varies — `Valve` or AMI depending on revision; not reliable) | (varies) | Low | Do **not** rely on. |
| `bios_version` | `F7A0xxx` style strings | `F7G0xxx` style strings | Low | Useful only for diagnostics/log. |
| `chassis_vendor` | `Valve` | `Valve` | High | Optional cross-check. |

Rationale for the precedence: `sys_vendor == "Valve"` is the **single most stable signal** because it is what Valve themselves use to gate kernel quirks (see the Galileo backlight-quirks patch at https://lists.freedesktop.org/archives/dri-devel/2025-August/522215.html and the panel-orientation-quirks patch at https://www.mail-archive.com/dri-devel@lists.freedesktop.org/msg499955.html). It will be **forward-compatible** because Valve as a company will continue to ship the same vendor string on any future device. Conditioning the refusal on `sys_vendor == "Valve"` alone gives ventd a "hardware refusal" policy that auto-extends to Deck 2 without a code change.

### 3.2 Secondary: kernel modules / platform devices

| Signal | What ventd reads | Present on |
|---|---|---|
| `/sys/module/steamdeck` directory exists | dir stat | SteamOS, Bazzite-Deck, ChimeraOS-on-Deck (when `steamdeck-dkms` or Valve neptune kernel installed) |
| `/sys/module/steamdeck_hwmon` directory exists | dir stat | Same — `steamdeck-hwmon` is split out as a hwmon-only sibling on some kernels |
| `/sys/bus/platform/drivers/steamdeck/` | dir listing | Same |
| `/sys/devices/platform/VLV0100:00/` (or `…/jupiter`) | dir stat | Wherever the platform driver bound; ACPI HID is `VLV0100` |
| `/sys/class/hwmon/hwmonN/name == "steamdeck_hwmon"` | string read | Universal where the driver loads — this is the canonical hwmon name (https://wiki.archlinux.org/title/Steam_Deck) |
| `/sys/class/hwmon/hwmonN/name == "steamdeck"` | string read | Older split (pre-MFD refactor variants) |
| `lsmod` listing of `steamdeck`, `steamdeck_hwmon` | `/proc/modules` parse | Cheap module check |

These signals are present on Deck hardware **only when the Valve kernel patches are loaded**. On a vanilla mainline kernel on Deck hardware, they will be **absent** — even though it is unmistakably a Steam Deck. This is precisely why these signals are *secondary*, not primary: a user running plain Fedora 41 (mainline kernel, no DKMS) on Galileo hardware will fail kernel-module detection but still has to be refused. **DMI is the only universally reliable signal.** Note that a mainline `k10temp` Steam Deck APU ID was finally added in **Linux 6.19** (Phoronix, Dec 4, 2025: https://www.phoronix.com/news/Linux-6.19-HWMON), so APU temperature monitoring works on stock kernels now, but **fan control via VLV0100 still requires Valve's out-of-tree `steamdeck` driver** as of this writing.

### 3.3 Tertiary: CPU / APU identity (`/proc/cpuinfo`)

| Signal | Value | Notes |
|---|---|---|
| `model name` | `AMD Custom APU 0405` | LCD (Aerith / Van Gogh N7). Confirmed in Arch Wiki and community lspci/cpuinfo dumps. |
| `model name` | `AMD Custom APU 0932` | OLED (Sephiroth / Van Gogh N6). |
| `vendor_id` | `AuthenticAMD` | Always. |
| `cpu family` / `model` | family 23 (0x17), Zen 2 | Same family as many other AMD parts — **not unique to Deck** in isolation. |

Use only as a **corroboration** signal for log output ("detected AMD Custom APU 0405 — consistent with Steam Deck LCD"). Never as a sole detection signal: a regular Ryzen on a homelab box would also be `AuthenticAMD`.

### 3.4 Quaternary: jupiter-fan-control daemon presence

| Signal | Path | Meaning |
|---|---|---|
| Systemd unit file present | `/usr/lib/systemd/system/jupiter-fan-control.service` or `/etc/systemd/system/…` | Valve daemon installed (SteamOS, Bazzite-Deck, AUR install on Arch Deck) |
| Python script present | `/usr/share/jupiter-fan-control/fancontrol.py` | Same |
| Process `fancontrol.py` running | `/proc/*/cmdline` scan | Daemon active right now |
| Polkit helper present | `/usr/bin/steamos-polkit-helpers/jupiter-fan-control` | SteamOS-style integration |

This is **not a detection signal for hardware** — Bazzite ships the daemon on non-Deck images too — but it **is** useful diagnostically once we already know the box is a Deck: ventd's refusal message can detect whether the user's existing controller is in fact running and recommend "your Valve daemon is already active".

### 3.5 Signals you should NOT use

- `/proc/device-tree/` — **absent** on x86 Decks (this is an ACPI x86_64 platform; only ARM/RISC-V boots use device tree). Confirmed by the Phoronix and patchwork sources describing the platform as `depends on X86_64`.
- `dmesg` strings — not parseable from a normal user; non-deterministic; rotates.
- USB VID/PID for the controller — present, but USB enumeration order is racy at boot; also useless on Decks where the internal controller has firmware-update-failed (see `ValveSoftware/SteamOS#1308`).
- Hostname `steamdeck` — user-configurable; `DocMAX` ran Arch on Deck with hostname `steamdeck` (https://github.com/ValveSoftware/SteamOS/issues/1441) but other users will have changed it.
- Presence of `/home/deck` user — same problem; user-configurable.
- BIOS version strings — drift across firmware updates.

---

## 4. Detection precedence chain (ventd implementation)

The probe runs in this order. **Stop on first match — refuse.**

```
1. Read /sys/class/dmi/id/sys_vendor.
   IF == "Valve": classify as Steam Deck family → REFUSE.
       a. Also read product_name → log "LCD (Jupiter)" / "OLED (Galileo)" / "unknown Valve device".
       b. Also read product_family → log codename for diagnostics.
2. Iterate /sys/class/hwmon/*/name.
   IF any equals "steamdeck_hwmon" or "steamdeck": classify as Steam Deck → REFUSE.
       (catches a Frankenstein system where DMI was somehow stripped but Valve drivers loaded)
3. Stat /sys/bus/acpi/devices/VLV0100:00/  (or /sys/bus/platform/devices/ matching VLV0100).
   IF present: classify as Steam Deck → REFUSE.
4. Parse /proc/modules. IF "steamdeck" or "steamdeck_hwmon" loaded: REFUSE.
5. Otherwise: not a Steam Deck. Continue normal probe.
```

**Rationale for ordering:**

- DMI first because it works on **every** Linux distribution at boot, including a freshly-installed mainline-kernel system that has no Valve drivers. It is the only signal that is 100% present on Deck hardware regardless of distro.
- Hwmon-name and ACPI-device checks second because they catch the (rare but possible) case where the BIOS DMI strings have been stripped/spoofed (e.g., user running ventd inside a VM passthrough on Deck hardware) but the Valve kernel drivers are bound.
- `/proc/modules` last because it is the weakest evidence — modules can be loaded on non-Deck systems out of curiosity or by accident (recall the `jupiter-fan-control fails on asus X570-I` scenario in Bazzite).
- All steps are read-only; none open `pwm*` for write.

---

## 5. Cross-distro / cross-version comparison matrix

Rows = signals; columns = environments. ✅ = present and stable; ⚠️ = present but distro-version-dependent; ❌ = absent.

| Signal | SteamOS 3.4 (LCD) | SteamOS 3.5 (LCD/OLED) | SteamOS 3.6+ (LCD/OLED) | Bazzite-Deck (LCD/OLED) | ChimeraOS on Deck | Nobara-Deck | Vanilla Arch on Deck (mainline kernel) | Vanilla Fedora/Ubuntu on Deck (mainline kernel) | Dual-boot SteamOS/Linux side |
|---|---|---|---|---|---|---|---|---|---|
| `sys_vendor == "Valve"` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ (DMI is hardware, OS-independent) |
| `product_name == "Jupiter"` (LCD) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `product_name == "Galileo"` (OLED) | n/a | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `/sys/module/steamdeck` | ✅ (Valve neptune kernel) | ✅ | ✅ | ✅ (kernel-bazzite — https://github.com/hhd-dev/kernel-bazzite) | ⚠️ (depends on kernel choice) | ⚠️ | ⚠️ (only with `steamdeck-dkms`) | ❌ usually | ⚠️ |
| `hwmon name == "steamdeck_hwmon"` | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ❌ | ⚠️ |
| `VLV0100` ACPI device exposed | ✅ | ✅ | ✅ | ✅ | ✅ (kernel-dependent binding) | ✅ | ✅ | ✅ (device exists in ACPI tables; no driver may bind) | ✅ |
| `/usr/share/jupiter-fan-control/` | ✅ | ✅ | ✅ | ✅ (deck variant) | ⚠️ (optional package) | ⚠️ | ⚠️ (AUR) | ❌ | ⚠️ |
| `jupiter-fan-control.service` enabled | ✅ | ✅ | ✅ | ✅ | ⚠️ | ⚠️ | ⚠️ | ❌ | ⚠️ |
| `cpuinfo` AMD Custom APU 0405/0932 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `k10temp` mainline binding | ❌ | ❌ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ⚠️ | ✅ on kernel ≥ 6.19 | ⚠️ |
| `/proc/device-tree/` | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |

**Conclusion the matrix yields**: *Only* the DMI rows are universally green across every column. Everything else has at least one column with a question mark. This empirically validates "DMI first, everything else as corroboration".

---

## 6. The refusal path

### 6.1 Behavioural contract

When the probe identifies Steam Deck hardware, ventd MUST:

1. Mark the device as `unsupported_hardware` with reason code `STEAMDECK_VLV0100`.
2. Emit a single structured log line at `WARN` level (not `ERROR` — this is expected behaviour on Deck).
3. Emit a one-time human-readable message to stdout (if interactive) or to the systemd journal (if running as service).
4. **Not open any `pwmN` file for write.** Read-only inventory of fans/temps for telemetry is acceptable and recommended (so `ventd status` still shows useful data on Deck).
5. Exit cleanly with code 0 if invoked as a one-shot probe, or remain running in `monitor-only` mode if running as a daemon (the user may have ventd installed on a fleet image and just want it to stay quiet on this one node).

### 6.2 Recommended refusal message text

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

The wording is deliberately **non-judgmental** ("will not control fans" rather than "cannot"), names the actual ACPI device (`VLV0100`) so a user grepping kernel logs lands on the correct subsystem, links the canonical upstream **and** the GitHub mirror **and** the AUR package, and ends with a documented opt-out for users who understand the policy and want quieter logs. The variable substitution `{product_name}` lets the same template handle Jupiter, Galileo, and any future codename without code changes.

### 6.3 What ventd should still do on a Deck

- Surface `steamdeck_hwmon` `fan1_input` and `temp1_input` in `ventd status --read-only`.
- Surface AMD APU temps from `k10temp` (mainline ≥ 6.19) or from Valve's `steamdeck_hwmon` Battery Temp on older kernels.
- Allow the user to verify thresholds without controlling the fan — homelab Decks running headless server workloads benefit from monitoring even when refusal is enforced.

---

## 7. Cross-reference with R1 (Tier-2 framework)

R1's Tier-2 framework, as I understand it from the spec context, classifies refusals primarily into **virt/container refusal** (no `pwm*` to talk to) and **lacks-permission refusal** (running as non-root). The Steam Deck case is **neither**:

- It is not a virt refusal — `pwm*` files exist and are writable as root, and the kernel will accept the write. The refusal is a **policy** decision, not a kernel-enforced boundary.
- It is not a permission refusal — root can write the file.
- It is **hardware refusal**: the underlying physical hardware (EC firmware contract) makes writes harmful even when they syntactically succeed.

**Recommendation**: introduce a new top-level refusal class in ventd's policy engine called `hardware_refusal` (parallel to `virt_refusal` and `permission_refusal`), with R3 (Steam Deck) as its first member. Future hardware refusals (e.g., a Framework laptop with a known-fragile EC, an Apple Silicon Mac running Asahi Linux where SMC fan control is the only sane path, certain AIO coolers that reset on rogue PWM) will slot into this class without re-architecting. The class's contract is:

- Detection runs **first**, before virt/container probes (because hardware identity is invariant to whether you're in a VM passthrough scenario or a container; the user might be running ventd inside a containerized control-plane on a Deck dual-booted to Bazzite and it should still refuse).
- Detection signals are exclusively read-only.
- The refusal message is **always** actionable — it points to the vendor-blessed controller, not a generic "unsupported".

In R1 terminology, R3's policy class is `hardware_refusal::valve_steamdeck`, with the detection key being `dmi.sys_vendor == "Valve"`. That single-key keying is what makes it forward-compatible.

---

## 8. Forward compatibility (Deck 2 / Steam Brick / future Valve x86_64)

The biggest risk in catalog-based detection is that a new Valve product ships and the catalog doesn't know about it, so the daemon happily fights its EC. R3's design avoids that risk **by inverting the gate**: instead of an allow-list of "known Decks", we use a deny-list keyed only on `sys_vendor == "Valve"`. Concretely:

| Future scenario | Detection outcome |
|---|---|
| Deck 2 ships with `product_name = "Tifa"` (or whatever) | `sys_vendor` is still `Valve` → refused. ✅ |
| Steam Brick (rumoured headless variant) | `sys_vendor` still `Valve` → refused. ✅ |
| A future Valve VR HMD that exposes Linux | `sys_vendor` still `Valve`; almost certainly no fan to control anyway → refused. ✅ |
| A non-Valve "SteamOS-certified" handheld (e.g., Lenovo Legion Go S running SteamOS) | `sys_vendor != "Valve"` → **not** refused on hardware grounds; fall through to normal probe. ✅ This matters: per `ValveSoftware/steam-for-linux#12286`, Valve's own daemon raises `NotImplementedError` on the Legion Go S precisely because it's NOT Valve hardware. ventd should mirror that and treat Legion Go S as a normal probe target. |
| Valve introduces a new ACPI HID `VLV0200` with a different EC contract | Still caught by `sys_vendor == "Valve"`. ✅ |
| Valve releases firmware that hides the `Valve` vendor string (extremely unlikely; would break their own kernel quirks) | Caught by hwmon-name / `VLV0100` ACPI device fallback. Add a CI check that exercises the fallback path so this regression is detected. |

**What NOT to key off**:

- **Don't** key on `product_name` exact-matching `Jupiter` or `Galileo` — that's the catalog antipattern; a Deck 2 would slip through.
- **Don't** key on the APU codename (`Aerith`/`Sephiroth`) — Sephiroth is a Van Gogh refresh and AMD could theoretically reuse it in a non-Valve product.
- **Don't** key on the Valve neptune kernel — vanilla mainline-kernel installs on Deck hardware would slip through.

**Spec note**: when a Deck 2 ships and the new `product_name` is observed in the wild, ventd's log message should be enriched (so users see "detected Valve {Tifa} hardware" and not "detected Valve unknown device"), but the **refusal behaviour** does not need any code change. This is the entire payoff of inverting the gate.

---

## 9. HIL validation plan

Phoenix's fleet includes a Steam Deck. The validation matrix that gives the highest confidence per dollar of test time:

| Test | Fleet member | Steps |
|---|---|---|
| **Primary**: detection-fires-before-write on real Deck running SteamOS 3.6 | Steam Deck (LCD or OLED, whichever Phoenix has) | Run `ventd probe --dry-run` and assert the refusal message and reason code; assert no `pwmN` open(2) calls in `strace` output. |
| Detection-fires-before-write on Deck booted into Bazzite-Deck | Same Steam Deck, second boot entry | Repeat. |
| Detection-fires-before-write on Deck booted into vanilla Arch with `steamdeck-dkms` *uninstalled* | Same Steam Deck, third boot entry | Repeat — this exercises the "DMI present, kernel module absent" case (tier 1 must fire alone). |
| Negative control: detection does NOT fire on Proxmox host (5800X+RTX 3060) | Proxmox host | `sys_vendor` will be the motherboard vendor (ASUS/MSI/Gigabyte), refusal must NOT fire. |
| Negative control: detection does NOT fire on MiniPC Celeron, 13900K+RTX 4090 desktop, 3 laptops | All of the above | None should produce `STEAMDECK_VLV0100` reason code. |

The single test that gives highest signal: **boot the Deck into vanilla Arch with `steamdeck-dkms` uninstalled, run ventd, assert refusal**. That one configuration validates the most fragile branch of the precedence chain (DMI-only path, no kernel-module corroboration). Everything else is regression coverage.

---

## 10. Authoritative sources used in this document

- Valve `jupiter-fan-control` upstream: https://gitlab.com/evlaV/jupiter-fan-control
- Public mirror: https://github.com/Jovian-Experiments/jupiter-fan-control
- Original kernel patch (Andrey Smirnov, 2022, drivers/platform/x86/steamdeck.c, 523 LoC, VLV0100): https://patchwork.kernel.org/project/linux-hwmon/patch/20220206022023.376142-1-andrew.smirnov@gmail.com/ and LWN write-up https://lwn.net/Articles/883961/
- DKMS-packaged version of the platform driver (out-of-tree, working on mainline kernel): https://aur.archlinux.org/packages/steamdeck-dkms
- AUR jupiter-fan-control package: https://aur.archlinux.org/packages/jupiter-fan-control
- Galileo DMI confirmation in mainline (panel-orientation-quirks): https://www.mail-archive.com/dri-devel@lists.freedesktop.org/msg499955.html
- Galileo DMI confirmation in mainline (panel-backlight-quirks, Aug 2025): https://lists.freedesktop.org/archives/dri-devel/2025-August/522215.html
- Phoronix on Galileo / Sephiroth DMI strings (Linux 6.6 sound): https://www.phoronix.com/news/Linux-6.6-Sound
- Phoronix on Linux 6.19 adding Steam Deck APU id to k10temp (Dec 2025): https://www.phoronix.com/news/Linux-6.19-HWMON
- Phoronix on the platform driver still not mainlined as of mid-2024: https://www.phoronix.com/forums/forum/software/linux-gaming/1466235-steam-deck-platform-driver-in-no-apparent-rush-for-upstreaming-into-the-linux-kernel
- ValveSoftware/SteamOS issue showing EC takes over when daemon breaks: https://github.com/ValveSoftware/SteamOS/issues/1359
- ValveSoftware/SteamOS issue with `jupiter-fan-control.service` log spam: https://github.com/ValveSoftware/SteamOS/issues/891
- ValveSoftware/steam-for-linux issue showing the daemon's `NotImplementedError` hardware-refusal pattern on Legion Go S: https://github.com/ValveSoftware/steam-for-linux/issues/12286
- Bazzite jupiter-fan-control issue on Galileo: https://github.com/ublue-os/bazzite/issues/1147
- Bazzite Steam Deck integration / `-steamdeck` flag: https://deepwiki.com/ublue-os/bazzite/7.2-steam-integration-and-gaming-mode
- Bazzite handheld quirks page: https://docs.bazzite.gg/Handheld_and_HTPC_edition/quirks/
- Bazzite kernel (used on OLED): https://github.com/hhd-dev/kernel-bazzite
- Arch Wiki Steam Deck (canonical user-facing reference for `steamdeck_hwmon` name and the `steamdeck-dkms` workflow): https://wiki.archlinux.org/title/Steam_Deck
- Userspace PWM-via-kernel-memory warning (Windows side analogue, demonstrates how brittle direct EC writes are): https://github.com/ayufan/steam-deck-tools/blob/main/docs/risks.md and https://github.com/CelesteHeartsong/custom-steam-deck-tools
- HoloISO Galileo detection issue: https://github.com/HoloISO/holoiso/issues/855
- Bazzite SteamDeck-BIOS-Manager script (concrete example of `Jupiter`/`Galileo` branching): https://github.com/ryanrudolfoba/SteamDeck-BIOS-Manager/blob/main/steamdeck-BIOS-manager.sh
- Jovian-NixOS Galileo notes (independent confirmation of the LCD/OLED differentiation pattern): https://github.com/Jovian-Experiments/Jovian-NixOS/issues/227
- Sephiroth APU / Aerith APU codenaming background: https://www.pcgamer.com/valve-steam-deck-oled-announcement/, https://www.gamesradar.com/steam-deck-oled-kills-the-originals-aerith-processor-with-a-sephiroth-upgrade-as-valve-flexes-its-final-fantasy-7-fandom/, https://hothardware.com/news/steam-deck-die-shots
- Universal Blue community confirmation of "EC sets fan when daemon absent": https://www.answeroverflow.com/m/1196366584403480627
- Bazzite community confirmation of "this is a steam deck service, shouldn't even be running" on non-Deck hardware: https://www.answeroverflow.com/m/1204707288024219658

---

# ARTIFACT 2 — Spec-ready findings appendix block

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