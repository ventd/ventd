# R36 — OEM mini-PC EC survey: catalog input for v0.5.12 hwmon expansion

**Status:** research-only deliverable
**Bundle:** R-bundle 2026-05
**Scope:** the eight OEM mini-PC vendors flagged in
[R28 §3](./R28-master.md) — Beelink, MINISFORUM, GMKtec, AceMagic, Topton,
GEEKOM, AOOSTAR, CWWK — as the *long tail* of Linux fan-control coverage
(no in-tree driver, proprietary EC firmware). Cross-references R28 master
priority row #7 ("OEM mini-PC DMI-vendor pattern match → IT5570 OOT
recommendation").
**Purpose:** concrete catalog input for `internal/hwdb/catalog/boards/`
and `internal/hwdb/catalog/chips/` expansion in v0.5.12.

---

## 1. Executive summary

Five-point synopsis per vendor, ordered by Linux-fan-control prevalence:

### 1.1 Beelink
- **DMI is partial-fingerprint.** `sys_vendor=Beelink` is *not* always set;
  many SKUs ship with `sys_vendor=AZW` (the OEM, Shenzhen AZW Technology),
  `product_name` ranging from "MINI S", "SER", "SER7" to literal "Default
  string". Catalog entries must match `sys_vendor ∈ {Beelink, AZW}`.
- **EC chip split.** SER4/SER5/SER6/SER7/SER8/SER9 (AMD Phoenix/Hawk Point)
  use **ITE IT5570 EC** — no in-tree driver, OOT
  `passiveEndeavour/it5570-fan` covers it. MINI S12/S12 Pro/S13 (Intel
  N100/N150) use **ITE IT8613E Super I/O** — supported by the
  `frankcrawford/it87` OOT driver via `force_id=0x8622`, monitor-only on
  mainline `it87` until kernel ≥6.7.
- **EQ series mostly mirrors SER.** EQR5/EQR6 (older Ryzen 5xxx) appear
  to use IT8613E (treated like Mini S series); EQ12/EQ13 may use IT5570
  but unconfirmed.
- **No firmware-locked SKUs found.** Every Beelink mini-PC has a Linux
  path (OOT driver or BIOS-only).
- **Working configs reported:** Beelink Mini S12 N100 with `it87
  force_id=0x8622` (Proxmox 9 Web UI sensor display); SER4 4800U with
  underclock workaround only (no fan control).

### 1.2 MINISFORUM
- **MS-01 is the workstation flagship and is *partly* solved on Linux.**
  Uses **Nuvoton NCT6798D** Super I/O, detected by mainline `nct6775`
  driver. Fan/RPM read works; PWM control works on most boards (some
  fan channels register-locked by EC). Kernel ≥6.5 recommended;
  kernel ≥6.7 covers the dual-chip secondary NCT6686D for additional
  temperatures.
- **UM-series and HX-series use IT5570 EC.** UM790 Pro / UM780 / HX99G
  / HX100G / UM870 ship with the same Phoenix/Hawk Point IT5570
  platform as Beelink SER. Same OOT path applies.
- **MS-A1 / MS-A2 are NCT-based but partially broken.** AIDA64 reports
  MS-A1 fans absent; community workaround (`kizzard/minisforum-ms-a1-fan-controller`)
  retrofits an external Trinket M0 microcontroller to drive PWM
  externally — i.e. the on-board EC is firmware-locked or not addressable
  from the OS. ventd should treat MS-A1 / MS-A2 as **monitor-only**
  unless future firmware drops add an OS path.
- **BD795i SE / BD795M (mini-ITX board, not mini-PC chassis).** Use
  IT8613E. HWiNFO identifies fan controller; `lmsensors`/`fancontrol`/
  `pwmconfig` *do not work* (per Level1Techs thread). Driver "still to
  be written" was the diagnosis. Treat as monitor-only.
- **Official position:** "Fan speed cannot be manually adjusted"
  (Minisforum support FAQ on UM790 Pro). BIOS-only adjustment is
  documented for most current models.

### 1.3 GMKtec
- **Near-zero Linux fan-control coverage.** All BIOS-driven on K8 Plus,
  K11, K12, G10, EVO-X1, EVO-X2 — official GMKtec fan-control guides
  point only to BIOS Hardware Monitor menus.
- **EC chip undisclosed.** No public sensors-detect / dmidecode dumps
  found in 2025–2026 community sources for K11/K12/G10/EVO-X1/EVO-X2.
- **Likely IT5570 (AMD models) or IT8613E (Intel models)** by analogy
  with the broader Phoenix/Hawk Point ODM pool. Confirmation requires
  HIL.
- **No working OOT fan-control reports** in /r/MiniPCs, Level1Techs,
  Linux Mint forums, or upstream GitHub issues for any GMKtec model.
- **Catalog recommendation:** ship `unsupported: true` rows with a
  doctor-card hint ("try `it5570-fan` OOT if `sensors-detect`
  reports unknown chip 0x5570").

### 1.4 AceMagic
- **One model confirmed-working with OOT IT5570 driver:** the AceMagic
  W1 (Phoenix Ryzen 7 8745HS) is the *single explicitly tested*
  reference platform for the `passiveEndeavour/it5570-fan` driver.
- **AM18, S1, AD08 likely use IT5570 too** (all are Phoenix or Hawk
  Point Ryzen). Unconfirmed but likely-DKMS-capable.
- **S1 has secondary userspace tooling** (`tjaworski/AceMagic-S1-LED-TFT-Linux`)
  — covers LED + TFT, not fan control. Fan control still goes via
  IT5570 OOT.
- **Fanless variants exist (T8 Pro lower-spec).** Treat as
  `OutcomeMonitorOnly` with evidence "no_controllable_channels".
- **Catalog recommendation:** AceMagic is the *strongest* "ship the
  OOT driver" recommendation in the bundle — w1 is verified working.

### 1.5 Topton
- **Mostly fanless industrial / firewall mini-PCs.** Intel N100 / N150 /
  N200 / N305 / N355 platforms in solid aluminium chassis. Many SKUs
  have an *optional* 8010 case fan that the user can plug in or unplug.
- **EC chip is IT8613E on the active-cooled SKUs** (same family as
  CWWK); detected by `it87 force_id=0x8622`. Read works; PWM control
  partial (some Topton firmware versions don't expose `pwm_enable`).
- **DMI strings: `Default string` everywhere.** A community user on
  minipcunion confirmed the Topton AMR5 ships with `Default string`
  in System/BaseBoard/Chassis Manufacturer/Product fields — which makes
  vendor-DMI matching impossible on this SKU.
- **Cross-vendor ODM with CWWK**: Topton and CWWK appear to be the same
  OEM (CWWK = Shenzhen Changwang) producing the same boards under both
  brand labels. Catalog rows for CWWK should match Topton too where
  fingerprint allows.
- **Catalog recommendation:** monitor-only by default; if user manually
  declares Topton via doctor-card, recommend `it87 force_id=0x8622`.

### 1.6 GEEKOM
- **Highest "Linux-friendly" branding** but mostly silent on fan
  control. CNX Software, Lon.TV, and Liliputing reviews of A6/A7/A8/IT13
  on Ubuntu confirm boot, networking, and GPU work; *none* report
  controllable PWM in Linux.
- **EC chip varies by model.**
  - GEEKOM AS6 / A5 Pro / A6: rebadged ASUS PN53 (AMD Ryzen 6800H/6900HX).
    Uses Nuvoton NCT chip, supported via mainline `nct6775` (read works,
    PWM may need `asus-wmi-sensors` companion).
  - GEEKOM A7 (Ryzen 9 7940HS, "NUCRB02A1" board): unconfirmed; likely
    IT5570.
  - GEEKOM A8 / AE7 (Ryzen 9 8945HS): IT5570 family, same as Beelink
    SER8/SER9.
  - GEEKOM IT13 / IT12 (Intel 13th/12th Gen): different family, Intel
    NUC reference; potentially IT8613E or NPCM7xx.
- **Catalog recommendation:** split into three sub-rows by CPU family
  (Phoenix/Hawk Point AMD → IT5570; Rembrandt → NCT via PN53; Intel →
  IT8613E or read-only).

### 1.7 AOOSTAR
- **R7 / R1 / WTR Pro / GEM12 are the strongest Linux story in the
  long tail.** R7 Proxmox users have *successfully* read fan RPM and
  voltages via `frankcrawford/it87` DKMS module — IT8613E chip
  detected at port 0xa30 revision 8. PWM control is *not* claimed
  working in the wiki.niziak.spox.org reference; it's monitoring only.
- **WTR Max has an LCD display panel** (proprietary). Reverse-engineered
  by `zehnm/aoostar-rs` — orthogonal to fan control.
- **GEM12 / GEM12 Pro use dual-fan cooling** with BIOS PWM control.
  In Linux the fan reports as "Power" via HWiNFO/Windows; in Linux
  hwmon the it87 path likely applies.
- **No fully-firmware-locked SKUs reported.**
- **Catalog recommendation:** R1/R7/WTR series → `inherits_driver:
  it87` with `monitor_only: true`; GEM12 → `it87` with PWM caveat.

### 1.8 CWWK
- **Fanless N100/N150/N305 firewall mini-PCs are the dominant SKU.**
  No fan to control; passive cooling. Classify as
  `OutcomeMonitorOnly` evidence `no_controllable_channels`.
- **Active-cooled CWWK SKUs (rare)** use IT8613E + `frankcrawford/it87`
  OOT.
- **MW-NVR-N5105 (CWWK-family)** confirmed in Proxmox forum thread —
  IT8613E identified as `it8622` in hwmon, but `pwm4_enable stuck to
  0` in kernel ≥6.2 (the well-known `it87 ignore_resource_conflict=1`
  regression — see R28 priority row #2).
- **DMI strings: `Default string`** uniformly. CWWK ships unprogrammed
  System/BaseBoard fields just like Topton.
- **Catalog recommendation:** ship a CWWK row matching `Default
  string` patterns (low-signal) AND a row matching by chip-presence
  (IT8613E at LPC 0x4e/0x290).

---

## 2. Per-vendor detailed survey

### 2.1 Beelink (SER + EQR + MINI S series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `Beelink` OR `AZW` (Shenzhen AZW Technology, the parent) |
| **DMI `product_name`** | One of: `SER`, `SER7`, `SER8`, `SER9`, `MINI S`, `MINI S12`, `MINI S12 Pro`, `MINI S13`, `EQR6`, `EQ12`, `EQ13`, often with trailing `[Default string]` location-in-chassis |
| **DMI `board_vendor`** | `AZW` (uniform — confirmed by Proxmox forum dmidecode output for SER7) |
| **DMI `board_name`** | `SER`, `MINI S` (sparse; matches product line) |
| **`chassis_type`** | typically `35` (mini-tower) or `3` (desktop), inconsistent |
| **`bios_vendor`** | `American Megatrends Inc.` (AMI) — uniform |
| **EC chip on AMD SKUs (SER4–SER9)** | **ITE IT5570** (chip ID `0x5570` at Super I/O port 0x4E, revision 0x02 typical). Cross-confirmed via `lm-sensors/lm-sensors#411` + `frankcrawford/it87#49`. |
| **EC chip on Intel N100/N150 SKUs (Mini S12/S12 Pro/S13, EQ12/EQ13)** | **ITE IT8613E** Super I/O at LPC 0xa30. Cross-confirmed via blog.croitor.eu/posts/proxmox_beelink_s12_sensors. |
| **In-tree driver behaviour** | `it87` mainline does NOT recognise IT5570 (chip ID 0x5570 unmatched). `it87` mainline DOES recognise IT8613E only when forced (`modprobe it87 force_id=0x8622`); without force, sensors-detect reports "unknown chip" and no hwmon entry is created. |
| **Working OOT driver (AMD)** | [`passiveEndeavour/it5570-fan`](https://github.com/passiveEndeavour/it5570-fan) — DKMS, AUR package `it5570-fan-dkms`; supported on AceMagic W1 reference platform; community-claimed support for Beelink SER class but no explicit per-model verification. |
| **Working OOT driver (Intel)** | [`frankcrawford/it87`](https://github.com/frankcrawford/it87) with `force_id=0x8622` and `mmio=off ignore_resource_conflict=1` for kernel ≥6.2. DKMS-capable. |
| **Working configurations reported** | Beelink Mini S12 N100 with `force_id=0x8622` displays sensors in Proxmox 9 Web UI (alcroito's blog). Beelink SER4 4800U: NO Linux fan control path; user underclocked CPU as workaround. |
| **Permanently broken** | None known; every Beelink has *some* OOT path. |
| **Catalog recommendation** | Two-row split: `beelink-amd-it5570` (`inherits_driver: it5570-fan`, OOT) + `beelink-intel-it8613` (`inherits_driver: it87` with `force_id=0x8622` modprobe arg). Match by `(sys_vendor=Beelink|AZW)` AND CPU family (lookup via `cpuinfo` / `dmidecode -t processor`). |

#### 2.1.1 Per-model Beelink notes

- **SER4 (Ryzen 4xxx)** — IT5570; Manjaro forum thread `121051`
  documents complete absence of fan control via stock Linux paths.
  User's only success was CPU underclock to "below 45 °C" via
  `cpupower`. Confirms IT5570 + monitor-only for OS-side until OOT
  installed.
- **SER5 (Ryzen 5xxx)** — same EC; some BIOS revisions added Smart
  Fan toggle (BIOS-only). Linux Mint forum `412508` reports
  `cpupower-gui` as the only available knob.
- **SER6 (Ryzen 6xxx, e.g. 6900HX)** — IT5570; forum thread
  `bbs.bee-link.com/d/1507` confirms BIOS Smart Fan / Manual Fan tabs.
- **SER7 (Ryzen 7xxx, 7840HS) — Phoenix's HIL platform.** IT5570;
  ServeTheHome review notes the heatsink fan but Linux fan-control
  path is OOT. Forum thread `bbs.bee-link.com/d/6037` is the
  community's go-to BIOS curve guide.
- **SER8 (Ryzen 8xxx)** — IT5570 family; manuals.plus user manual
  references BIOS Smart Fan controls.
- **SER9 / SER9 Pro / SER9 Max (HX 370 / HX 255)** — IT5570 likely
  but kernel ≥6.10 *required* for the Strix Point CPU to even boot;
  fan control is best-effort OOT until kernel ≥6.13.
- **MINI S12 (Intel N95)** — IT8613E; sensors-detect output *fully
  documented* in the `alcroito.eu` Proxmox blog post:
  ```
  System: AZW MINI S [Default string]
  Probing for `ITE IT8613E Super IO Sensors'... Yes
  Found `ITE IT8613E Super IO Sensors' (revision 1) at 0xa30
  ```
  Linux 6.17+ on Proxmox 9. Driver "to-be-written" status per
  sensors-detect — but `it87 force_id=0x8622` works in practice.
- **MINI S12 Pro / MINI S13 (N100/N150)** — same IT8613E pattern.
- **EQR5 / EQR6 (Ryzen 5xxx)** — Lawrence Systems and Beelink Forum
  threads confirm Proxmox / Ubuntu boot. EC chip unconfirmed but
  vendor pattern suggests IT5570 (Ryzen) or IT8613E (older models).
- **EQ12 / EQ13 (N100/N150)** — IT8613E by extrapolation.

---

### 2.2 MINISFORUM (UM, HX, MS-01, MS-A series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `MINISFORUM` (consistent caps) |
| **DMI `product_name`** | One of: `UM790`, `UM780`, `UM870`, `UM890`, `HX99G`, `HX100G`, `MS-01`, `MS-A1`, `MS-A2`, `BD795i SE`, `BD795M`, `Neptune HX99G-AMZ` |
| **DMI `board_vendor`** | `MINISFORUM` (uniform) |
| **DMI `board_name`** | mirrors `product_name` |
| **`chassis_type`** | `35` (mini-tower) for UM/HX; `17` (server-class) for MS-01 |
| **`bios_vendor`** | AMI |
| **EC chip on UM/HX (AMD Phoenix/Hawk Point)** | **ITE IT5570** (same family as Beelink SER, AceMagic W1) |
| **EC chip on MS-01 (Intel 12th/13th Gen)** | **Nuvoton NCT6798D** Super I/O at LPC 0x290 (mainline `nct6775` driver) — confirmed by `blog.pcfe.net/hugo/posts/2025-02-22-minisforum-ms-01-sensors/` running EL9 + kernel 5.14, sensors output shows `nct6798-isa-...`. Some MS-01 SKUs add a secondary NCT6686D requiring kernel ≥6.7 for `nct6683` mainline support. |
| **EC chip on MS-A1 / MS-A2 (AMD AM5 / mobile)** | Likely Nuvoton (per AIDA64 fan detection failure pattern). MS-A1 specifically: NO OS-side fan control path — community workaround uses external microcontroller. |
| **EC chip on BD795i SE / BD795M (mini-ITX boards)** | **ITE IT8613E** (per HWiNFO identification in Level1Techs thread `228646`) |
| **In-tree driver behaviour (UM/HX)** | NOT recognised by mainline `it87`. Same gap as Beelink SER. |
| **In-tree driver behaviour (MS-01)** | `nct6775` works for read; PWM works on most channels. Custom `/etc/sensors.d/` config needed to label the bogus voltage channels (per pcfe blog post). |
| **In-tree driver behaviour (MS-A1)** | NONE. Community uses `kizzard/minisforum-ms-a1-fan-controller` (external Adafruit Trinket M0 + CircuitPython). This is hardware retrofit, not a kernel module. |
| **Working OOT driver (UM/HX)** | `passiveEndeavour/it5570-fan` (same as Beelink SER) |
| **Working OOT driver (MS-01)** | None needed; mainline works. |
| **Working OOT driver (BD795)** | `frankcrawford/it87` claims IT8613E support but Level1Techs users report `lmsensors` / `pwmconfig` / `fancontrol` all fail to set PWM — driver "still to be written" per the maintainer's response. |
| **Working configurations** | MS-01 + Ubuntu 24.04 + nct6775 (read + control); UM790 Pro: Sagar Behere blog used external USB AC Infinity fan as workaround (no Linux EC control). |
| **Permanently broken** | MS-A1 (until firmware drop or sysfs path lands); BD795i SE (until OOT driver matures). |
| **Catalog recommendation** | Five-row split: `minisforum-um-hx-it5570` (Phoenix/Hawk Point UM/HX → IT5570 OOT), `minisforum-ms-01-nct6798` (MS-01 → mainline nct6775, kernel ≥5.14), `minisforum-ms-a1-locked` (MS-A1 → unsupported, monitor-only with doctor-card explainer), `minisforum-ms-a2-locked` (MS-A2 → unsupported), `minisforum-bd795-it8613` (BD795i SE / BD795M → it87 OOT, monitor-only with caveat). |

#### 2.2.1 MS-01 detailed sensors output (verbatim from pcfe.net)

```
sensors output snippet:
nct6798-isa-0a20
Adapter: ISA adapter
in0:                       1.18 V (min =  +0.00 V, max =  +1.74 V)
...
fan1:                     2400 RPM   (min =    0 RPM)
fan2:                     1850 RPM   (min =    0 RPM)
...
SYSTIN:                   +30.0°C    (high = +80.0°C, hyst = +75.0°C)
CPUTIN:                   +44.5°C
```

Kernel module loaded: `nct6775` (mainline). Module options needed: none.
Kernel version recommended: **6.5+** (NCT6799D backport context didn't
affect MS-01 NCT6798D, but NCT6798 has been mainline since 5.x). For
clean operator UX: **kernel ≥6.7** to also pick up dual-chip secondary
NCT6686D temperatures.

---

### 2.3 GMKtec (NucBox K, G, M, EVO series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `GMKtec` (consistent) |
| **DMI `product_name`** | One of: `K8`, `K8 Plus`, `K9`, `K10`, `K11`, `K12`, `G3`, `G10`, `M5`, `M6`, `EVO-X1`, `EVO-X2`, `EVO-T1`, `NucBox K11`, `NucBox G10` |
| **DMI `board_vendor`** | unknown — community sources don't include `dmidecode -t baseboard` dumps |
| **`bios_vendor`** | AMI |
| **EC chip on AMD models (K8 Plus, K11, K12, G10, EVO-X1, EVO-X2)** | Unconfirmed; *strong inference* of **ITE IT5570** based on (a) AMD Phoenix/Hawk Point/Strix Point platform alignment with Beelink SER / AceMagic W1, (b) absence of any nct67xx user reports for GMKtec, (c) GMKtec's official "fan adjustment guide" pointing only to BIOS Hardware Monitor (suggests no Windows-side userspace driver, mirroring IT5570 register-only control surface). |
| **EC chip on Intel models (K9 i9, M5)** | Unconfirmed; likely **ITE IT8613E** (same as Beelink Mini S series). |
| **In-tree driver behaviour** | None on AMD models (IT5570). Maybe partial via `it87 force_id=0x8622` on Intel models. |
| **Working OOT driver** | `passiveEndeavour/it5570-fan` *should* apply to AMD models but **no community confirmation has been published** — this is the largest unverified-yet-likely catalog row in the bundle. |
| **Working configurations** | Linux Mint forum `465850` reports GMKtec NucBox G10 "works well in Linux Mint" but nothing about fan control. M5 review on `virtualizationhowto.com` confirms Linux Mint compatibility but no sensor data. |
| **Permanently broken** | None known to be hardware-locked. |
| **Catalog recommendation** | Three-row split: `gmktec-amd-it5570-untested` (`unsupported: true` initially, with note "likely IT5570 — install OOT driver and report"), `gmktec-intel-it8613-untested` (same pattern for Intel models), and `gmktec-evo-x2-strix-halo` (`unsupported: true`, kernel ≥6.13 required for Ryzen AI Max+ 395). |

GMKtec is the **largest knowledge gap** in the bundle. Recommended HIL
acquisition order (if Phoenix wants to fill it): K11 (Ryzen 9 8945HS,
likely IT5570) → EVO-X2 (Strix Halo, kernel-frontier).

---

### 2.4 AceMagic (S1, T8, W1, AM18, AD08, AD15)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `ACEMAGIC` or `AceMagic` (case varies; treat case-insensitive) |
| **DMI `product_name`** | One of: `S1`, `T8`, `T8 Pro`, `W1`, `AM18`, `AD08`, `AD15` |
| **DMI on W1** | **`Default string`** in System/BaseBoard fields per passiveEndeavour project README |
| **`bios_vendor`** | `American Megatrends International, LLC` (AMI). BIOS string `PHXPM7B0` on W1 (Phoenix/Hawk Point platform) |
| **EC chip (W1 reference)** | **ITE IT5570** revision 0x02 — *the* canonical reference platform for IT5570 reverse engineering |
| **EC chip on S1, AM18, AD08, AD15** | Unconfirmed; almost certainly IT5570 (same Phoenix/Hawk Point pool) |
| **EC chip on T8 Pro lower-spec (Intel N97)** | Likely IT8613E |
| **Working OOT driver** | [`passiveEndeavour/it5570-fan`](https://github.com/passiveEndeavour/it5570-fan) — verified-working on W1; AUR `it5570-fan-dkms` |
| **Secondary tooling** | [`tjaworski/AceMagic-S1-LED-TFT-Linux`](https://github.com/tjaworski/AceMagic-S1-LED-TFT-Linux) — covers LED+TFT, orthogonal to fan control |
| **Working configurations** | AceMagic W1 with `it5570-fan` kernel module + CoolerControl userspace daemon (per project README) |
| **Permanently broken** | None known. T8 Pro fanless variant: `OutcomeMonitorOnly` by chassis design. |
| **Catalog recommendation** | Two-row split: `acemagic-amd-it5570-w1-verified` (W1 + family pattern → `inherits_driver: it5570-fan`, `verified: true`), `acemagic-intel-fanless` (T8 fanless → `monitor_only: true`). |

AceMagic is the **single most catalog-ready vendor** in this bundle —
the W1 platform is verified-working, gives ventd a "we know IT5570
works here" anchor row.

---

### 2.5 Topton (industrial / firewall N-series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `Default string` (uniform) — Topton ships unprogrammed DMI fields |
| **DMI `product_name`** | `Default string` |
| **DMI `board_vendor`** | `Default string` |
| **DMI `chassis_manufacturer`** | `Default string` |
| **DMI `bios_version`** | typically `V1.14_P4C4M43_EC_0_0_59_AMI` style (per minipcunion.com forum thread `4301`) |
| **`bios_vendor`** | AMI |
| **EC chip on active-cooled SKUs** | **ITE IT8613E** at LPC 0xa30 (same family as CWWK; same OEM lineage). Detected via `it87 force_id=0x8622`. |
| **EC chip on fanless SKUs** | Often *no fan controller exposed at all* (no PWM headers populated). Some boards have IT8613E silicon present but with no fan plugged in. |
| **Cross-vendor ODM** | **Topton and CWWK are produced by Shenzhen CWWK / Changwang.** YouTube "Topton and CWWK - Who Are They" + Level1Techs thread `212600` cross-confirm the OEM identity. They share boards, DMI patterns, and silicon. |
| **In-tree driver behaviour** | Mainline `it87` does NOT recognise IT8613E by chip ID; force-load works. |
| **Working OOT driver** | `frankcrawford/it87` with `force_id=0x8622` |
| **Working configurations** | Archimago's CWWK RJ36 (i3-N305) review uses `lm_sensors` with `it87` for monitoring; PWM control not attempted (passively cooled). |
| **Permanently broken** | Fanless SKUs: hardware-architectural — `OutcomeMonitorOnly` is correct. Active-cooled SKUs: kernel ≥6.2 needs `ignore_resource_conflict=1` OR `mmio=off` (the regression discussed in R28 priority row #2). |
| **Catalog recommendation** | Two rows: `topton-default-string-fanless` (DMI `Default string` + no `pwm*` files → `unsupported: true`, evidence `no_controllable_channels`), `topton-default-string-it8613` (DMI `Default string` + `it87` chip detected → `inherits_driver: it87` with `force_id=0x8622` modprobe arg). |

#### 2.5.1 Topton AMR5 dmidecode extract (from minipcunion thread)

```
System Information
        Manufacturer: Default string
        Product Name: Default string
        Version: Default string
        Serial Number: Default string
BIOS Information
        Vendor: American Megatrends International, LLC.
        Version: V1.14_P4C4M43_EC_0_0_59_AMI
        Release Date: 01/06/2023
```

This pattern is the **single most diagnostic DMI signature** in the
long tail — it matches Topton, CWWK, and a long list of generic
AliExpress mini-PCs simultaneously. ventd should treat it as a
**low-confidence fingerprint** that must be combined with chip
detection (LPC probe) before catalog decisions.

---

### 2.6 GEEKOM (A, AS, AE, IT, GT series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `GEEKOM` (uniform) |
| **DMI `product_name`** | One of: `A5`, `A5 Pro`, `A6`, `A7`, `A8`, `A8 Max`, `AE7`, `AS6` (note: GEEKOM AS6 = ASUS PN53 rebadge), `IT12`, `IT13`, `IT13 Max`, `IT15`, `Mini IT8 SE`, `GT1 Mega` |
| **DMI `board_vendor`** | `GEEKOM` for in-house designs; **`ASUSTeK COMPUTER INC.`** for AS6 (rebadged PN53) — important catalog distinction |
| **DMI `board_name`** | `A8`, `A7` (sparse) |
| **`bios_vendor`** | `American Megatrends LLC` (note: "LLC" suffix vs "Inc." — sometimes used as ODM signal but not reliable) |
| **DMI Serial Number example (A7)** | `NUCRB02A151NNNNTA3Z1501228` per cnx-software review |
| **EC chip on A5/A6 (Ryzen 5xxx/6xxx Rembrandt)** | Likely Nuvoton family per ASUS lineage (A6 inherits PN50/PN53 design) |
| **EC chip on AS6 (= ASUS PN53)** | **Nuvoton NCT** family (likely NCT6797D or NCT6798D) — supported by mainline `nct6775` since 5.x; may need `asus-wmi-sensors` companion for full coverage |
| **EC chip on A7 / A8 / AE7 (Ryzen 7xxx/8xxx Phoenix/Hawk Point)** | **ITE IT5570** by family pattern (matches Beelink SER, AceMagic W1) — *unconfirmed* by direct sensors-detect dump in any 2025–2026 GEEKOM review found |
| **EC chip on IT12 / IT13 / IT15 (Intel 12/13/15th Gen)** | Likely **ITE IT8613E** (matches Beelink Mini S, Topton); potentially Nuvoton on IT13 Max |
| **In-tree driver behaviour** | A6/AS6 (Rembrandt + ASUS): mainline `nct6775` works for read, may miss some channels. A7/A8: no in-tree path. IT13: possibly mainline `it87`. |
| **Working OOT driver** | A7/A8 → `it5570-fan` (extrapolated); IT13 → `frankcrawford/it87` |
| **Working configurations** | CNX Software A8 review (cnx-software.com/2024/06/11/...) uses `psensor` for read-only thermal monitoring on Ubuntu 24.04 + kernel 6.8. **No fan control verified** in any review found. GEEKOM A5 Pro 2026 starryhope review on Ubuntu 25.10 + kernel 6.17 confirms boot but not fan control. |
| **Permanently broken** | None known to be hardware-locked. |
| **Catalog recommendation** | Four-row split: `geekom-as6-pn53-rebadge` (board_vendor=ASUSTeK + product=AS6 → reuse ASUS PN53 catalog → `inherits_driver: nct6775`), `geekom-amd-rembrandt` (A5/A5 Pro/A6 → `nct6775` mainline), `geekom-amd-phoenix-it5570` (A7/A8/AE7 → `it5570-fan` OOT, `verified: false`), `geekom-intel-it8613` (IT12/IT13/IT15 → `it87` OOT, `verified: false`). |

---

### 2.7 AOOSTAR (R, WTR, GEM, GT series)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `AOOSTAR` (uniform) |
| **DMI `product_name`** | One of: `R1`, `R7`, `R7 PRO`, `WTR PRO`, `WTR MAX`, `GEM10`, `GEM12`, `GEM12+`, `GEM12+ PRO`, `GT68` |
| **DMI `board_vendor`** | likely `AOOSTAR` (uniform — community sources don't show a different OEM label) |
| **`bios_vendor`** | AMI |
| **EC chip on R1/R7/WTR (Ryzen 5825U / 5700U / 5800U)** | **ITE IT8613E** at port 0xa30 revision 8 — confirmed by wiki.niziak.spox.org/hw:server:aoostar_r7. This is the *only* explicitly-documented EC chip in the AOOSTAR catalog. |
| **EC chip on GEM12 / GEM12+ Pro (Phoenix/Hawk Point)** | Likely **ITE IT5570** by family pattern; *unconfirmed* in published reviews. |
| **EC chip on WTR Max (Hawk Point)** | Unconfirmed; AOOSTAR ships an LCD display panel — proprietary protocol reverse-engineered in `zehnm/aoostar-rs`. Display is orthogonal to fan control. |
| **In-tree driver behaviour** | Mainline `it87` does NOT auto-recognise IT8613E. Force `force_id=0x8622` works. |
| **Working OOT driver (R7)** | [`frankcrawford/it87`](https://github.com/frankcrawford/it87) — DKMS install via `make dkms`. **Verified working** on R7 5825U for fan2 RPM read at "615 RPM"; PWM control not claimed in the reference. |
| **Working configurations** | R7 5825U + Proxmox 8 + frankcrawford/it87 v1.0-169 (read works); fan3/4/5 report 0 RPM (no fans plugged into those headers). |
| **Permanently broken** | None known. SATA passthrough VFIO breaks CPU temp sensor on AMD U-series (not fan-control-specific; orthogonal). |
| **Catalog recommendation** | Two-row split: `aoostar-r-wtr-it8613-verified` (R1/R7/WTR PRO → `inherits_driver: it87` with `force_id=0x8622`, `monitor_only: true` initially, upgrade to `verified: true` after HIL), `aoostar-gem-phoenix-it5570-unverified` (GEM12 → IT5570 OOT, `verified: false`). |

---

### 2.8 CWWK (firewall / industrial mini-PCs)

| Field | Value |
|---|---|
| **DMI `sys_vendor`** | `Default string` (CWWK ships unprogrammed DMI like Topton) |
| **DMI `product_name`** | `Default string` |
| **DMI `board_vendor`** | `Default string` |
| **DMI `bios_version`** | `V1.14_P4C4M43_EC_0_0_59_AMI`-style strings |
| **`bios_vendor`** | AMI |
| **EC chip on N100/N150/N200/N305/N355 SKUs** | **ITE IT8613E** at LPC 0xa30 — same as Topton (shared OEM); confirmed by Proxmox forum thread `130721` (MW-NVR-N5105 sibling) showing `Found ITE IT8613E Super IO Sensors at 0xa30`, identified as `it8622` in hwmon (legacy alias). |
| **In-tree driver behaviour** | Mainline `it87` recognises IT8613E by alias `0x8622`. Kernel ≥6.2 introduces the `ignore_resource_conflict` regression (see R28 priority row #2): `pwm4_enable stuck to 0` until `it87 ignore_resource_conflict=1` is added. |
| **Working OOT driver** | `frankcrawford/it87` with `force_id=0x8622` AND `ignore_resource_conflict=1` AND `mmio=off` (kernel ≥6.2 trifecta) |
| **Working configurations** | Archimago's CWWK RJ36 review (i3-N305): monitor-only works via `lm_sensors`. CWWK MW-NVR-N5105 (Proxmox kernel 6.2.16-3-pve): worked. CWWK same on kernel 6.2.16-4-pve: PWM stuck to 0 (regression). Resolution: `ignore_resource_conflict=1`. |
| **Fanless SKUs (most CWWK)** | NO fan to control. `OutcomeMonitorOnly` evidence `no_controllable_channels`. Includes: F1, F2, F6, F7, F9, F11, D4, X86 P5, C1. |
| **Permanently broken** | None hardware-locked; the kernel-≥6.2 regression has known workaround. |
| **Catalog recommendation** | Three rows: `cwwk-default-string-fanless` (DMI `Default string` + no `pwm*` exposed → `unsupported: true`, evidence `no_controllable_channels`), `cwwk-default-string-it8613-active` (DMI `Default string` + IT8613E + at least one fan plugged → `inherits_driver: it87` with `force_id=0x8622 ignore_resource_conflict=1 mmio=off`, `kernel_version: { min: "6.2" }` for the workaround flags), `cwwk-default-string-it8613-pre62` (same chip, kernel <6.2 → no extra flags needed, `kernel_version: { max: "6.1.99" }`). |

---

## 3. Out-of-tree driver landscape

### 3.1 `passiveEndeavour/it5570-fan`
- **Repo:** https://github.com/passiveEndeavour/it5570-fan
- **Coverage:** ITE IT5570 chip (ID 0x5570 at SIO port 0x4E)
- **Verified platforms:** AceMagic W1 only (per README)
- **Likely platforms (community claim):** Beelink SER series (AMD),
  MINISFORUM UM/HX series, AceMagic family (W1/AM18/S1/AD08/AD15),
  GMKtec K series (AMD), GEEKOM A7/A8/AE7
- **DMI matching:** None — driver detects by hardware ID at SIO probe,
  *because most of these mini-PCs ship `Default string` DMI*
- **DKMS:** Yes (`make dkms-install`)
- **AUR:** `it5570-fan-dkms`
- **Kernel version:** unspecified (uses standard hwmon API)
- **Userspace integration:** CoolerControl auto-discovers via hwmon;
  fancontrol/fan2go work
- **Open issues at survey time:** 3 — (#1) Mechrevo imini pro 830, (#2)
  Intel NUC7i5BNB chip ID 0x8987 unsupported, (#3) PELADN YO Series test
- **Risk:** community-maintained, single maintainer, no upstream
  kernel acceptance path declared. Register layouts may vary per OEM
  firmware build.

### 3.2 `frankcrawford/it87`
- **Repo:** https://github.com/frankcrawford/it87
- **Coverage:** Wide range of ITE IT87xx Super I/O — IT8603E, IT8606E,
  IT8607E, IT8613E, IT8620E, IT8622E, IT8623E, IT8625E, IT8628E, IT8528E,
  IT8655E, IT8665E, IT8686E, IT8688E, IT8689E, IT8696E, IT8698E, IT8771E,
  IT8772E, IT8786E, IT8790E, IT8792E, IT87952E, etc.
- **Does NOT cover IT5570** — that's why `passiveEndeavour/it5570-fan`
  exists as a separate project. See `frankcrawford/it87#49` for the
  rejection reasoning.
- **Modprobe args:** `force_id=0x8622` (alias IT8613E to known-supported
  IT8622E), `ignore_resource_conflict=1`, `mmio=off`, `update_vbat`,
  `fix_pwm_polarity`
- **DKMS:** Yes (`./dkms-install.sh`)
- **Kernel version:** no explicit minimum; v1.0-169 verified on Proxmox
  kernel 6.8
- **Known regression:** kernel ≥6.2 changed resource-conflict handling.
  Without `ignore_resource_conflict=1`, `pwm_enable` reads as stuck-zero.

### 3.3 `Fred78290/nct6687d`
- **Repo:** https://github.com/Fred78290/nct6687d
- **Coverage:** Nuvoton NCT6687-R, NCT6687D
- **Mini-PC coverage:** **NONE.** Every supported board is an MSI desktop
  motherboard (MAG B550/B650/B850/Z890 family, PRO Z890-P).
- **Important non-coverage:** the Minisforum MS-01 uses NCT6798D (different
  chip family, mainline `nct6775` covers it) — **NOT NCT6687**.
- **Modprobe args:** `fan_config=msi_alt1` for some MSI variants
- **DKMS:** Yes
- **Issues:** Issue #119 documents `pwm_enable stuck to 99` regression on
  Linux 6.11.11; workaround is module unload/reload after each
  `pwmconfig`.

### 3.4 `nbfc-linux` (NoteBook FanControl)
- **Repo:** https://github.com/nbfc-linux/nbfc-linux
- **Coverage:** **Laptop ECs only.** Uses XML config files describing each
  laptop's EC register layout. ~200 supported laptops in upstream config
  database.
- **Mini-PC coverage:** **NONE.** No mini-PC vendor in this bundle has an
  upstream nbfc XML config.
- **Theoretical applicability:** mini-PCs that expose fan control via
  ACPI EC ports 0x62/0x66 (which IT5570-bearing systems do) could
  *in principle* be controlled by hand-written nbfc XML. No published
  config has been found for any vendor in this bundle.

### 3.5 Vendor-specific WMI / ACPI drivers
- **None in upstream.** No mini-PC vendor in this bundle ships an
  ASUS-style WMI sensors driver to upstream Linux. The closest analogues:
  - `asus-wmi-sensors` (electrified) — covers ASUS desktops; applies to
    GEEKOM AS6 (= ASUS PN53 rebadge) with caveat
  - `tjaworski/AceMagic-S1-LED-TFT-Linux` — covers LED + TFT on AceMagic S1;
    not fan-related
  - `zehnm/aoostar-rs` — covers AOOSTAR WTR Max LCD; not fan-related

### 3.6 Coolercontrol / fan2go integration
- Both userspace daemons auto-discover via `/sys/class/hwmon/*`. They
  inherit whatever the kernel module exposes. So OOT driver coverage
  flows through transparently:
  - `it5570-fan` installed → CoolerControl + fan2go work on AMD mini-PCs
  - `frankcrawford/it87` installed → CoolerControl + fan2go work on Intel
    mini-PCs (with kernel-≥6.2 modprobe args)
  - No OOT installed → CoolerControl shows nothing (which is correct
    for "monitor-only" classification)

---

## 4. Shared-ODM analysis

The eight-vendor list collapses into **three motherboard supply pools**
plus a couple of one-off rebadges:

### Pool A: AMD Phoenix / Hawk Point ODM
**Same boards rebadged across:** Beelink SER series, MINISFORUM UM/HX series,
AceMagic W1/S1/AM18/AD08/AD15, GMKtec K8 Plus / K11 / K12 / G10, GEEKOM A7/A8/AE7,
AOOSTAR GEM12 / GEM12+ Pro, possibly Mechrevo imini pro / PELADN YO.

- **Shared silicon:** ITE IT5570 EC, AMD Ryzen 5xxx/6xxx/7xxx/8xxx mobile,
  Realtek RTL8125 / Intel I225/I226 NIC, MediaTek MT7922 / Intel AX200
  WiFi, AMI BIOS
- **Shared DMI quirk:** many ship `Default string` in System fields; only
  Beelink reliably populates `sys_vendor=AZW` and `sys_vendor=Beelink`
- **Catalog implication:** **one chip-level row** (`it5570-fan` driver
  binding) plus **per-vendor DMI rows** that all `inherits_driver:
  it5570-fan`. ventd's tier-2 (vendor regex) and tier-3 (chip
  fallback) both apply.

### Pool B: Intel Alder Lake-N / Twin Lake ODM (CWWK = Topton lineage)
**Same boards rebadged across:** Topton (toptonpc.com), CWWK
(cwwkpc.com / cwwk.net), Beelink Mini S12 / S12 Pro / S13 / EQ12 / EQ13,
HUNSN RJ36, MW-NVR-N5105, generic AliExpress N100 firewalls.

- **Shared silicon:** ITE IT8613E Super I/O, Intel Alder Lake-N (N95/N100/N150/N200/N305/N355) or Twin Lake N150, Intel I226-V NICs, AMI BIOS
- **Shared DMI quirk:** Topton and CWWK both ship `Default string`
  in System fields. Beelink Mini S series populates `AZW MINI S` (so
  Beelink's row distinguishes itself).
- **Catalog implication:** **one chip-level row** (`it87` driver with
  `force_id=0x8622`, kernel ≥6.2 with `ignore_resource_conflict=1`).
  Per-vendor rows fall back to it.
- **Cross-confirmed by:** Level1Techs thread `212600`, YouTube video
  "Topton and CWWK - Who Are They" (2EpV5zHqJH8), ServeTheHome i3-N305
  fanless review.

### Pool C: AMD Rembrandt + ASUS PN50/PN53 lineage
**Rebadge:** GEEKOM AS6 = ASUS PN53 (per AnandTech, guru3d reviews).

- **Shared silicon:** Nuvoton NCT family (likely NCT6797D or NCT6798D),
  AMD Ryzen 6800H / 6900HX (Rembrandt)
- **Catalog implication:** **reuse ASUS catalog rows** for AS6; treat
  GEEKOM A6 / A5 Pro as Pool-C-adjacent (likely same NCT family but
  not literally PN53).
- **Cross-confirmed by:** AnandTech "GEEKOM AS 6 (ASUS PN53) Review"
  + guru3d review.

### One-off rebadges and odd cases
- **MINISFORUM MS-01** uses NCT6798D Super I/O directly. No rebadge
  family — it's a Minisforum-original 13900H workstation board.
- **MINISFORUM MS-A1 / MS-A2** use AMD AM5 (MS-A1) / Ryzen Pro 9
  9955HX (MS-A2) on what *appears* to be a Minisforum-original NCT-based
  board, but the EC firmware blocks OS-side writes — this is the
  "firmware-locked" sub-class within MINISFORUM.
- **MINISFORUM BD795i SE / BD795M (mini-ITX boards, not full mini-PC
  chassis)** use IT8613E with the "driver still to be written"
  designation per Level1Techs.
- **AOOSTAR R7 / R1 / WTR Pro** use IT8613E (Pool B-adjacent silicon
  but Ryzen U-series CPU instead of Intel). One-off — not in either
  major pool.

### Summary table

| Pool | Vendors | EC chip | OOT driver | Mainline kernel? |
|---|---|---|---|---|
| A. AMD Phoenix/Hawk Point | Beelink SER, MINISFORUM UM/HX, AceMagic W1+, GMKtec K, GEEKOM A7/A8, AOOSTAR GEM12 | ITE IT5570 | `passiveEndeavour/it5570-fan` | No |
| B. Intel Alder Lake-N | CWWK, Topton, Beelink Mini S, AOOSTAR R7, MW-NVR | ITE IT8613E | `frankcrawford/it87` (force_id) | Partial (it87 with force) |
| C. AMD Rembrandt + ASUS lineage | GEEKOM AS6 (= ASUS PN53), GEEKOM A6/A5 | Nuvoton NCT67xx | mainline `nct6775` | Yes (≥5.x) |
| One-off | MINISFORUM MS-01 | Nuvoton NCT6798D | mainline `nct6775` | Yes (≥5.x) |
| One-off | MINISFORUM MS-A1/A2 | unknown / locked | none | No (firmware-locked) |
| One-off | MINISFORUM BD795i SE | ITE IT8613E | none mature | Partial (it87 read-only) |

---

## 5. Kernel-version gates

Recommendations for `kernel_version: { min: "X.Y" }` annotations on
catalog rows:

| Catalog row | Min kernel | Reason |
|---|---|---|
| `minisforum-ms-01-nct6798` | **5.14** | NCT6798D mainline support is older but EL9 baseline (5.14) is the verified floor per pcfe.net. |
| `minisforum-ms-01-nct6798` (with secondary NCT6686D temps) | **6.7** | nct6683 mainline added X670E Taichi family (and indirectly improved NCT6686D detection on dual-chip boards). Source: phoronix.com/news/Linux-6.7-HWMON. |
| `geekom-as6-pn53-rebadge` (full sensor coverage including chipset) | **6.5** | Linux 6.5 added NCT6799D and parts of NCT6798D dual-board logic that PN53 inherits. |
| `cwwk-default-string-it8613-active` (active-cooled with kernel-≥6.2 quirks) | **6.2** | Kernel ≥6.2 forces `ignore_resource_conflict=1` workaround; row only applies when this is satisfied. |
| `cwwk-default-string-it8613-active-pre62` | **(no min)** + `max: "6.1.99"` | Pre-6.2 kernels work without the workaround. |
| `beelink-amd-it5570` | **5.10** | Conservative floor — IT5570 OOT driver requires the modern hwmon ABI. |
| `beelink-intel-it8613` | **5.10** + `force_id` arg | Same pattern as CWWK; IT8613E forced to IT8622E alias works on stable kernels back to 5.10. |
| `acemagic-amd-it5570-w1-verified` | **5.10** | Same as Beelink AMD. |
| `aoostar-r-wtr-it8613-verified` | **5.10** | Same as CWWK Intel pattern. |
| `geekom-amd-phoenix-it5570` | **6.10** for SER9-class HX 370 SKUs | Strix Point CPU support; only applicable to GEEKOM A8 Max / IT15 / similar HX 370 mini-PCs. |
| `gmktec-evo-x2-strix-halo` | **6.13** | Ryzen AI Max+ 395 / Strix Halo support; speculative pending EVO-X2 HIL. |
| `minisforum-ms-a1-locked` | n/a | No path; row is `unsupported: true` regardless of kernel. |
| `minisforum-bd795-it8613` | n/a | Driver "still to be written"; monitor-only regardless of kernel. |

---

## 6. `acpi_listen` / `dmidecode -t baseboard` notes

### 6.1 What `dmidecode -t baseboard` reveals

For all eight vendors, `dmidecode -t baseboard` tends to mirror `-t
system` — same uniform-`Default string` pattern on Topton/CWWK and
some AceMagic/GMKtec, populated `MINISFORUM` / `AZW` / `Beelink` /
`AOOSTAR` / `GEEKOM` on the more-DMI-disciplined vendors.

Key insight: **`board_name` is a stronger fingerprint than
`product_name` on these mini-PCs** because product names are often
marketed names (e.g., `MINI S12 Pro`) while board_names are shorter
and more stable (e.g., `MINI S` matching all S12/S12 Pro variants).

### 6.2 What `acpi_listen` reveals about EC

Across the eight vendors, `acpi_listen` events tend to be silent for
fan-related changes because:
- IT5570 EC is a **separate 8051 microcontroller** with its own firmware,
  not the platform ACPI EC. It does NOT raise SCIs for fan events.
- ACPI EC mailbox (ports 0x62/0x66) is sometimes accessed by the
  IT5570 driver for read/write but no notification events come back.
- IT8613E is a Super I/O LPC device; events are also silent.
- NCT6798D is silent at the ACPI level too — control is via direct
  port writes.

**Implication for ventd:** `acpi_listen`-based fan event detection
will not work on any vendor in this bundle. ventd must poll
`/sys/class/hwmon/*/fan*_input` directly.

---

## 7. Catalog row recommendations (ready-to-paste)

Below are 22 board YAML entries targeting
`internal/hwdb/catalog/boards/` plus 3 chip-level entries targeting
`internal/hwdb/catalog/chips/ite_family.yaml`.

### 7.1 New chip-level entries (`internal/hwdb/catalog/chips/ite_family.yaml`)

```yaml
  - name: "it5570"
    inherits_driver: "it5570-fan"  # OOT driver, not mainline
    description: "ITE IT5570 EC — programmable embedded controller (8051
      core) found in many AMD Phoenix/Hawk Point mini-PCs from Beelink,
      MINISFORUM, AceMagic, GMKtec, GEEKOM, AOOSTAR. NOT recognised by
      mainline it87. Requires OOT passiveEndeavour/it5570-fan DKMS module.
      Detection: SIO probe at 0x4E returns chip ID 0x5570."
    overrides:
      pwm_enable_modes:
        "0": "auto_ec_curve"   # write 0 to register 0x0F = return to EC firmware curve
        "1": "manual"          # write 1-100 to register 0x0F = manual duty %
    channel_overrides: {}
    citations:
      - "https://github.com/passiveEndeavour/it5570-fan (OOT driver, AceMagic W1 reference)"
      - "https://github.com/lm-sensors/lm-sensors/issues/411 (chip ID 0x5570 unrecognised by mainline)"
      - "https://github.com/frankcrawford/it87/issues/49 (frankcrawford/it87 explicitly does not target IT5570)"

  - name: "it8613"
    inherits_driver: "it87"
    description: "ITE IT8613E Super I/O — Intel Alder Lake-N / Twin Lake
      mini-PCs (Beelink Mini S, Topton, CWWK, AOOSTAR R7). Mainline it87
      does NOT recognise by chip ID — requires modprobe arg
      `force_id=0x8622` (alias to IT8622E). Kernel ≥6.2 additionally
      requires `ignore_resource_conflict=1` and `mmio=off` to avoid the
      pwm_enable=0 stuck-zero regression."
    overrides:
      requires_modprobe_args:
        - "force_id=0x8622"
    channel_overrides: {}
    citations:
      - "https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/ (kernel 6.2+ regression)"
      - "https://wiki.niziak.spox.org/hw:server:aoostar_r7 (AOOSTAR R7 5825U IT8613E verified working with frankcrawford/it87)"
      - "https://blog.croitor.eu/posts/proxmox_beelink_s12_sensors/ (Beelink Mini S12 N100 IT8613E sensors-detect output)"
```

(One additional driver entry — `internal/hwdb/catalog/drivers/it5570-fan.yaml`
— is also required to register the OOT driver class. Skeleton:)

```yaml
schema_version: "1.2"
driver_id: "it5570-fan"
description: "OOT DKMS driver for ITE IT5570 EC, by passiveEndeavour."
upstream_status: "out_of_tree"
distribution_paths:
  - kind: "git"
    url: "https://github.com/passiveEndeavour/it5570-fan"
  - kind: "aur"
    package: "it5570-fan-dkms"
sysfs_name: "it5570"
modprobe_module: "it5570_fan"
required_modprobe_args: []
conflicts_with: []
citations:
  - "https://github.com/passiveEndeavour/it5570-fan"
```

### 7.2 New board-level entries

The 22 entries below should land as new board files:

- `internal/hwdb/catalog/boards/beelink.yaml`
- `internal/hwdb/catalog/boards/minisforum.yaml`
- `internal/hwdb/catalog/boards/gmktec.yaml`
- `internal/hwdb/catalog/boards/acemagic.yaml`
- `internal/hwdb/catalog/boards/topton-cwwk.yaml`
- `internal/hwdb/catalog/boards/geekom.yaml`
- `internal/hwdb/catalog/boards/aoostar.yaml`

#### `beelink.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "beelink-ser-amd-it5570"
    dmi_fingerprint:
      sys_vendor: "/(Beelink|AZW)/"
      product_name: "/(SER[0-9]?|MINI S[0-9]+|EQR[0-9]+)/"
      board_vendor: "AZW"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT DKMS"
    additional_controllers: []
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "Beelink AMD mini-PCs (SER4/SER5/SER6/SER7/SER8/SER9 Phoenix &
      Hawk Point family). EC is ITE IT5570 — no mainline driver; install
      passiveEndeavour/it5570-fan DKMS. Without OOT installed, ventd
      should classify as monitor-only with doctor card recommending the
      install."
    citations:
      - "https://github.com/passiveEndeavour/it5570-fan"
      - "https://forum.manjaro.org/t/beelink-ser4-4800u-critical-thermals-issue/121051"
      - "https://bbs.bee-link.com/d/6037-ser-7-fan-control-setting-the-fan-curve-manually-through-bios"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "beelink-mini-s-intel-it8613"
    dmi_fingerprint:
      sys_vendor: "/(Beelink|AZW)/"
      product_name: "/(MINI S(12| Pro)?|MINI S13|EQ1[23])/"
      board_vendor: "AZW"
      board_name: "MINI S"
      board_version: "*"
    primary_controller:
      chip: "it8613"
      sysfs_hint: "name=it8613 via frankcrawford/it87 OOT (force_id=0x8622)"
    additional_controllers: []
    overrides: {}
    required_modprobe_args:
      - "force_id=0x8622"
      - "ignore_resource_conflict=1"  # kernel ≥6.2
      - "mmio=off"  # kernel ≥6.2
    conflicts_with_userspace: []
    notes: "Beelink Intel mini-PCs (Mini S12 N95/N100, Mini S12 Pro,
      Mini S13 N150, EQ12, EQ13). EC is ITE IT8613E — alias to IT8622E
      via force_id; install frankcrawford/it87 DKMS for full PWM control.
      Kernel ≥6.2 requires the resource-conflict workaround flags."
    citations:
      - "https://blog.croitor.eu/posts/proxmox_beelink_s12_sensors/"
      - "https://github.com/frankcrawford/it87"
      - "https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `minisforum.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "minisforum-ms-01-nct6798"
    dmi_fingerprint:
      sys_vendor: "MINISFORUM"
      product_name: "MS-01"
      board_vendor: "MINISFORUM"
      board_name: "MS-01"
      board_version: "*"
    primary_controller:
      chip: "nct6798"
      sysfs_hint: "name=nct6798 via mainline nct6775 driver"
    additional_controllers:
      - chip: "nct6686"  # secondary, kernel ≥6.7 for full coverage
        sysfs_hint: "name=nct6683 via mainline nct6683 (kernel 6.7+)"
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "MINISFORUM MS-01 (i9-13900H workstation mini-PC). NCT6798D
      Super I/O — mainline nct6775 driver. Custom /etc/sensors.d/ config
      needed to label bogus voltage/fan channels. Kernel ≥6.7 picks up
      secondary NCT6686D for additional temperatures."
    citations:
      - "http://blog.pcfe.net/hugo/posts/2025-02-22-minisforum-ms-01-sensors/"
      - "https://docs.kernel.org/hwmon/nct6775.html"
      - "https://www.phoronix.com/news/Linux-6.7-HWMON"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "minisforum-um-hx-amd-it5570"
    dmi_fingerprint:
      sys_vendor: "MINISFORUM"
      product_name: "/(UM[0-9]+( Pro)?|HX[0-9]+G?|Neptune HX[0-9]+G.*)/"
      board_vendor: "MINISFORUM"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT DKMS"
    additional_controllers: []
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "MINISFORUM AMD mini-PCs (UM790/UM780/UM870/UM890, HX99G,
      HX100G, Neptune HX99G-AMZ — Phoenix & Hawk Point family). Same
      IT5570 EC as Beelink SER. Per Minisforum support FAQ: 'fan speed
      cannot be manually adjusted' — implicit confirmation of EC
      firmware-driven control surface only. Install OOT driver for OS
      access."
    citations:
      - "https://github.com/passiveEndeavour/it5570-fan"
      - "https://www.justanswer.com/computer-hardware/oebgg-minisforum-um790-pro-cpu-gpu-fan.html"
      - "https://sagar.se/blog/um790-cooling-wifi/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "minisforum-bd795-it8613"
    dmi_fingerprint:
      sys_vendor: "MINISFORUM"
      product_name: "/(BD795(i SE|M|i)?|BD895i SE)/"
      board_vendor: "MINISFORUM"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it8613"
      sysfs_hint: "name=it8613 via frankcrawford/it87 (incomplete PWM)"
    additional_controllers: []
    overrides:
      monitor_only: true  # PWM control 'still to be written' per Level1Techs
    required_modprobe_args:
      - "force_id=0x8622"
    conflicts_with_userspace: []
    notes: "MINISFORUM BD795i SE / BD795M / BD895i SE mini-ITX boards.
      IT8613E identified via HWiNFO (Level1Techs thread). Linux fan
      control 'driver still to be written' — sensors read works, PWM
      writes silently no-op. Configure fan curve in BIOS Hardware
      Monitor."
    citations:
      - "https://forum.level1techs.com/t/minisforum-bd795i-se-fan-control/228646"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "minisforum-ms-a1-locked"
    dmi_fingerprint:
      sys_vendor: "MINISFORUM"
      product_name: "MS-A1"
      board_vendor: "MINISFORUM"
      board_name: "MS-A1"
      board_version: "*"
    primary_controller:
      chip: "unknown"
      sysfs_hint: "no OS-side fan control path; EC firmware-locked"
    additional_controllers: []
    overrides:
      unsupported: true
      reason: "ec_firmware_locked"
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "MINISFORUM MS-A1 (AMD AM5 socketed). AIDA64 reports zero fans
      detected despite three present in BIOS. Community workaround uses
      external Adafruit Trinket M0 microcontroller to drive PWM externally
      (kizzard/minisforum-ms-a1-fan-controller). Treat as monitor-only
      with doctor card explaining EC lock."
    citations:
      - "https://forums.aida64.com/topic/16404-aida64-doesnt-detect-any-fans-of-minisforum-ms-a1-barebone/"
      - "https://github.com/kizzard/minisforum-ms-a1-fan-controller"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "minisforum-ms-a2-locked"
    dmi_fingerprint:
      sys_vendor: "MINISFORUM"
      product_name: "MS-A2"
      board_vendor: "MINISFORUM"
      board_name: "MS-A2"
      board_version: "*"
    primary_controller:
      chip: "unknown"
      sysfs_hint: "no OS-side fan control path"
    additional_controllers: []
    overrides:
      unsupported: true
      reason: "ec_firmware_locked"
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "MINISFORUM MS-A2 (Ryzen 9 9955HX, BGA-soldered). williamlam.com
      improvements rely on BIOS TjMAX adjustment + setting fans to 'auto'
      — no Linux PWM path documented. Treat as monitor-only."
    citations:
      - "https://williamlam.com/2025/09/quick-tip-improving-thermals-on-minisforum-ms-a2.html"
      - "https://www.servethehome.com/minisforum-ms-a2-review-an-almost-perfect-amd-ryzen-intel-10gbe-homelab-system/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `gmktec.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "gmktec-amd-phoenix-hawk-it5570-untested"
    dmi_fingerprint:
      sys_vendor: "GMKtec"
      product_name: "/(K8 Plus|K9|K10|K11|K12|G10|EVO-X1)/"
      board_vendor: "*"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT (UNVERIFIED)"
    additional_controllers: []
    overrides:
      verified: false
      requires_hil_confirmation: true
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "GMKtec AMD mini-PCs (K8 Plus / K11 / K12 / G10 / EVO-X1 — Phoenix
      / Hawk Point / Strix Point class). EC chip *inferred* as IT5570 by
      family pattern with Beelink SER and AceMagic W1. NO published
      sensors-detect dump or community-confirmed working it5570-fan
      install at survey time. ventd should ship this row as
      verified: false, surface a doctor card asking the user to install
      it5570-fan and report back."
    citations:
      - "https://www.gmktec.com/pages/k8-plus-and-k11-fan-settings-adjustment-guide"
      - "https://www.gmktec.com/pages/k12-fan-speed-setting-modification-operation-guide"
      - "https://forums.linuxmint.com/viewtopic.php?t=465850 (G10 boots Linux Mint)"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "gmktec-evo-x2-strix-halo"
    dmi_fingerprint:
      sys_vendor: "GMKtec"
      product_name: "EVO-X2"
      board_vendor: "*"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT (UNVERIFIED, kernel ≥6.13 required for Strix Halo support)"
    additional_controllers: []
    overrides:
      verified: false
      requires_hil_confirmation: true
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "GMKtec EVO-X2 (Ryzen AI Max+ 395 / Strix Halo). Kernel ≥6.13
      required for the CPU to operate normally. Fan EC chip likely
      IT5570 family but unconfirmed. Triple-fan cooling system (dual CPU
      + SSD/RAM) per BIOS guide. Treat as unverified, monitor-only until
      HIL confirms."
    citations:
      - "https://www.gmktec.com/pages/evo-x2-fan-speed-adjustment-guide"
      - "https://manuals.plus/asin/B0F5HDWNKR"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `acemagic.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "acemagic-w1-it5570-verified"
    dmi_fingerprint:
      sys_vendor: "/(ACEMAGIC|AceMagic)/"
      product_name: "W1"
      board_vendor: "*"  # 'Default string' on this SKU
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT DKMS (REFERENCE PLATFORM)"
    additional_controllers: []
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "AceMagic W1 (Ryzen 7 8745HS, BIOS PHXPM7B0). REFERENCE
      PLATFORM for the IT5570 OOT driver — the chip ID 0x5570 revision
      0x02 was reverse-engineered against this device. Verified working:
      6 temperature sensors, fan RPM, PWM control via register 0x0F."
    citations:
      - "https://github.com/passiveEndeavour/it5570-fan (W1 explicitly named in README)"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: true  # the only verified row in this bundle
    defaults:
      curves: []

  - id: "acemagic-amd-family-it5570-likely"
    dmi_fingerprint:
      sys_vendor: "/(ACEMAGIC|AceMagic)/"
      product_name: "/(S1|AM18|AD08|AD15)/"
      board_vendor: "*"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT (extrapolated from W1)"
    additional_controllers: []
    overrides:
      verified: false
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "AceMagic AMD family (S1 i9-12900H — actually Intel; AM18,
      AD08 i9-11900H, AD15 likely Phoenix). IT5570 inferred from W1
      reference. AM18 has separate userspace tooling
      (tjaworski/AceMagic-S1-LED-TFT-Linux) for LED+TFT — orthogonal to
      fan control."
    citations:
      - "https://github.com/passiveEndeavour/it5570-fan"
      - "https://github.com/tjaworski/AceMagic-S1-LED-TFT-Linux"
      - "https://taoofmac.com/space/blog/2024/03/17/1900 (AM18 Linux gaming)"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "acemagic-t8-fanless"
    dmi_fingerprint:
      sys_vendor: "/(ACEMAGIC|AceMagic)/"
      product_name: "/T8.*/"
      board_vendor: "*"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "unknown"
      sysfs_hint: "no fan headers; passive cooling"
    additional_controllers: []
    overrides:
      unsupported: true
      reason: "no_controllable_channels"
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "AceMagic T8 / T8 Pro lower-spec variants are passively cooled.
      No PWM headers, no fan to control. ventd OutcomeMonitorOnly with
      evidence 'no_controllable_channels'."
    citations:
      - "https://acemagic.com/blogs/about-ace-mini-pc/the-acemagic-mini-pc-guide-everything-you-need-to-know"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `topton-cwwk.yaml`

```yaml
schema_version: "1.2"
# Topton and CWWK are produced by Shenzhen CWWK / Changwang — same OEM,
# different brand labels. Both ship `Default string` in System DMI. We
# match by chip detection rather than DMI vendor where possible.

board_profiles:

  - id: "cwwk-topton-default-string-fanless"
    dmi_fingerprint:
      sys_vendor: "Default string"
      product_name: "Default string"
      board_vendor: "Default string"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "unknown"
      sysfs_hint: "no PWM channels exposed"
    additional_controllers: []
    overrides:
      unsupported: true
      reason: "no_controllable_channels"
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "Generic Topton/CWWK fanless industrial firewall mini-PC
      (N100/N150/N200/N305/N355). Solid aluminium chassis, passive
      cooling. CWWK F1/F2/F6/F7/F9/F11/D4/X86 P5/C1, Topton M4/N100
      fanless, HUNSN RJ36 etc. No PWM surface in Linux. Common
      deployment: pfSense/OPNsense/Proxmox firewall."
    citations:
      - "https://www.toptonpc.com/product/topton-intel-n100-fanless-mini-pc-2xi226-v-2-5g-2com-nvme-2hd-1dp-efficient-cooling-solid-firewall-router-industrial-computer/"
      - "https://archimago.blogspot.com/2024/02/review-hunsn-cwwk-rj36-fanless-minipc.html"
      - "https://www.servethehome.com/almost-a-decade-in-the-making-our-fanless-intel-i3-n305-2-5gbe-firewall-review/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "cwwk-topton-default-string-it8613-active"
    dmi_fingerprint:
      sys_vendor: "Default string"
      product_name: "Default string"
      board_vendor: "Default string"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it8613"
      sysfs_hint: "name=it8613 via frankcrawford/it87 (force_id=0x8622)"
    additional_controllers: []
    overrides: {}
    required_modprobe_args:
      - "force_id=0x8622"
      - "ignore_resource_conflict=1"  # required on kernel ≥6.2
      - "mmio=off"
    conflicts_with_userspace: []
    notes: "Generic Topton/CWWK active-cooled SKU (e.g. MW-NVR-N5105,
      Topton AMR5 with optional 8010 fan). Match: DMI Default string AND
      LPC probe finds IT8613E at 0xa30. Kernel ≥6.2 trifecta of modprobe
      args required to avoid pwm_enable=0 stuck-zero regression
      (R28 priority row #2)."
    citations:
      - "https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/"
      - "https://www.minipcunion.com/viewtopic.php?t=4301"
      - "https://forums.overclockers.co.uk/threads/cwwk-topton-n100-mini-pc-router-experience.18991381/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `geekom.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "geekom-as6-pn53-rebadge"
    dmi_fingerprint:
      sys_vendor: "GEEKOM"
      product_name: "/AS6.*/"
      board_vendor: "/(ASUSTeK COMPUTER INC\\.|GEEKOM)/"  # ASUS PN53 OEM
      board_name: "/PN53.*/"
      board_version: "*"
    primary_controller:
      chip: "nct6798"   # likely; verify with sensors-detect
      sysfs_hint: "name=nct6798 via mainline nct6775 driver — PN53 inheritance"
    additional_controllers: []
    overrides: {}
    required_modprobe_args: []
    conflicts_with_userspace:
      - "asusctl"  # ASUS PN53 sometimes runs asusd; potential controller race
    notes: "GEEKOM AS6 = ASUS PN53 rebadge (per AnandTech / guru3d
      reviews). Inherits ASUS PN53 catalog behaviour. May benefit from
      asus-wmi-sensors companion module for chipset-class sensors. Watch
      for asusctl/asusd controller race if user installed ASUS-stack
      tooling."
    citations:
      - "https://www.anandtech.com/show/18964/geekom-as-6-asus-pn53-review-ryzen-9-6900hx-packs-punches-in-a-petite-package/2"
      - "https://www.guru3d.com/review/asus-geekom-as6-mini-pc-review/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "geekom-amd-rembrandt-nct"
    dmi_fingerprint:
      sys_vendor: "GEEKOM"
      product_name: "/(A5( Pro)?|A6)/"
      board_vendor: "GEEKOM"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "nct6798"  # guess — Rembrandt mini-PCs commonly use NCT
      sysfs_hint: "name=nct6798 via mainline nct6775 driver (UNVERIFIED)"
    additional_controllers: []
    overrides:
      verified: false
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "GEEKOM A5 Pro / A6 (Ryzen 6800H Rembrandt). Likely Nuvoton
      family by ASUS-PN50/53 lineage. Mainline nct6775 should work for
      read; PWM not confirmed in 2026 reviews (cnx-software, starryhope)."
    citations:
      - "https://www.cnx-software.com/2025/02/16/geekom-a6-review-ubuntu-24-04-tested-on-an-amd-ryzen-7-6800h-mini-pc/"
      - "https://www.starryhope.com/minipcs/geekom-a5-pro-2026-linux/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "geekom-amd-phoenix-hawk-it5570"
    dmi_fingerprint:
      sys_vendor: "GEEKOM"
      product_name: "/(A7|A8( Max)?|AE7)/"
      board_vendor: "GEEKOM"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT (extrapolated)"
    additional_controllers: []
    overrides:
      verified: false
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "GEEKOM A7/A8/AE7 (Ryzen 9 7940HS / 8945HS Phoenix/Hawk Point).
      EC family pattern matches Beelink SER + AceMagic W1 — IT5570
      inferred. CNX Software A8 review shows fan ramping under stress
      but no Linux PWM control. Install passiveEndeavour/it5570-fan and
      report back to confirm."
    citations:
      - "https://www.cnx-software.com/2024/06/11/geekom-a8-review-ubuntu-24-04-linux-amd-ryzen-8945hs-mini-pc/"
      - "https://www.cnx-software.com/2024/02/24/geekom-a7-mini-pc-review-ubuntu-22-04-ubuntu-24-04-linux/"
      - "https://www.cnx-software.com/2024/07/13/geekom-ae7-review-amd-ryzen-9-7940hs-mini-pc-windows-11-ubuntu-24-04/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []

  - id: "geekom-intel-it13-it8613"
    dmi_fingerprint:
      sys_vendor: "GEEKOM"
      product_name: "/(IT1[2358]|Mini IT[0-9].*|GT1.*)/"
      board_vendor: "GEEKOM"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it8613"
      sysfs_hint: "name=it8613 via frankcrawford/it87 (force_id=0x8622, UNVERIFIED)"
    additional_controllers: []
    overrides:
      verified: false
    required_modprobe_args:
      - "force_id=0x8622"
      - "ignore_resource_conflict=1"
      - "mmio=off"
    conflicts_with_userspace: []
    notes: "GEEKOM Intel mini-PCs (IT12, IT13, IT13 Max, IT15, GT1 Mega).
      EC chip likely IT8613E by Intel mini-PC family pattern (Beelink
      Mini S, Topton, CWWK). Unconfirmed. Forums.linuxmint.com #413377
      confirms IT13 boots Linux Mint."
    citations:
      - "https://forums.linuxmint.com/viewtopic.php?t=413377"
      - "https://hostbor.com/beelink-ser9-or-geekom-gt1-mega/"
      - "https://droix.net/blogs/geekom-it13-2025-review/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

#### `aoostar.yaml`

```yaml
schema_version: "1.2"

board_profiles:

  - id: "aoostar-r-wtr-it8613"
    dmi_fingerprint:
      sys_vendor: "AOOSTAR"
      product_name: "/(R[17]( PRO)?|WTR PRO|WTR MAX)/"
      board_vendor: "AOOSTAR"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it8613"
      sysfs_hint: "name=it8613 via frankcrawford/it87 (force_id=0x8622)"
    additional_controllers: []
    overrides: {}
    required_modprobe_args:
      - "force_id=0x8622"
      - "ignore_resource_conflict=1"
      - "mmio=off"
    conflicts_with_userspace: []
    notes: "AOOSTAR R1 / R7 / R7 PRO / WTR PRO / WTR MAX (Ryzen 7 5700U /
      5825U / 5800U). IT8613E at port 0xa30 revision 8 — VERIFIED working
      with frankcrawford/it87 v1.0-169 on Proxmox kernel 6.8 (per
      wiki.niziak.spox.org). fan2 reports RPM correctly; PWM control
      verification pending HIL. WTR Max LCD panel is orthogonal —
      reverse-engineered by zehnm/aoostar-rs."
    citations:
      - "https://wiki.niziak.spox.org/hw:server:aoostar_r7"
      - "https://github.com/zehnm/aoostar-rs"
      - "https://3dprintbeginner.com/aoostar-r7-review-best-budget-nas-in-2024/"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false  # can flip to true after PWM-write HIL confirmation
    defaults:
      curves: []

  - id: "aoostar-gem-amd-phoenix-it5570"
    dmi_fingerprint:
      sys_vendor: "AOOSTAR"
      product_name: "/(GEM10|GEM12( PRO)?|GEM12\\+( PRO)?|GT68)/"
      board_vendor: "AOOSTAR"
      board_name: "*"
      board_version: "*"
    primary_controller:
      chip: "it5570"
      sysfs_hint: "name=it5570 via passiveEndeavour/it5570-fan OOT (UNVERIFIED)"
    additional_controllers: []
    overrides:
      verified: false
    required_modprobe_args: []
    conflicts_with_userspace: []
    notes: "AOOSTAR GEM10 / GEM12 / GEM12+ / GEM12+ PRO / GT68 (Ryzen 7
      8845HS / 7840HS / Pro 6850H / 6900HX). Phoenix/Hawk Point family —
      IT5570 inferred. Liliputing GEM12 review notes dual cooling fan +
      vapor chamber. zehnm/aoostar-rs covers WTR Max LCD; not GEM. Treat
      as unverified, monitor-only with doctor card recommending
      it5570-fan install."
    citations:
      - "https://liliputing.com/aoostar-gem12-review-mini-pc-with-oculink-2-5-gbe-lan-and-up-to-ryzen-7-8845hs/"
      - "https://www.notebookcheck.net/Aoostar-GEM12-Mini-PC-review-AMD-Ryzen-7-8845HS-with-32-GB-RAM-1-TB-SSD-and-OCuLink-interface.848437.0.html"
    contributed_by: "anonymous"
    captured_at: "2026-05-03"
    verified: false
    defaults:
      curves: []
```

---

## 8. ventd-side surface area

The catalog rows above feed the existing tier-1/tier-2/tier-3 matcher
without schema changes:

- **Tier 1 (specific board fingerprint)** — `acemagic-w1-it5570-verified`
  is the only fully-fingerprint-verified tier-1 row in the bundle.
- **Tier 2 (vendor regex)** — most rows are tier-2 (e.g.
  `beelink-ser-amd-it5570` matches any `Beelink|AZW` vendor with `SER*`
  product). This is the bundle's bread and butter.
- **Tier 3 (chip-only fallback)** — `cwwk-topton-default-string-it8613-active`
  is effectively tier-3 because Topton/CWWK ship `Default string` DMI;
  matching has to pivot on chip detection.

Recommended ventd v0.5.12 changes:

1. **Ship the chip-level entries** in
   `internal/hwdb/catalog/chips/ite_family.yaml` — IT5570 + IT8613E
   plus a new driver yaml `it5570-fan.yaml`.
2. **Ship 22 board-level entries** as listed above, keyed under seven
   new files. Most are `verified: false` — that's correct; we have not
   HIL-tested every SKU.
3. **Doctor card: "OEM mini-PC, no in-tree EC driver"** — surfaces when
   `sys_vendor ∈ {Beelink, AZW, MINISFORUM, GMKtec, AceMagic, GEEKOM,
   AOOSTAR}` AND no `pwm*` files exist in `/sys/class/hwmon/*`. Card
   text: "ventd detected a vendor mini-PC where the EC chip likely needs
   an out-of-tree driver. Try `passiveEndeavour/it5570-fan` (AMD models)
   or `frankcrawford/it87` with `force_id=0x8622` (Intel models)." Link
   to install instructions.
4. **Doctor card: "DMI Default string"** — surfaces when sys_vendor is
   the literal `Default string`. Card text: "Your board ships with
   unprogrammed DMI fields (typical for Topton / CWWK / generic
   AliExpress mini-PCs). ventd cannot match a specific catalog row.
   Use chip-detection probe instead." Suggest running
   `sensors-detect --auto` and reporting the output.
5. **Pre-flight refusal: MS-A1 / MS-A2** — surface as
   `OutcomeMonitorOnly` with explicit reason `ec_firmware_locked`.
   Don't attempt PWM writes.
6. **Vendor-daemon detection extension** — the existing detector
   should add `coolercontrold`, `fan2go.service` to its list (already
   well-known, but worth confirming in the long-tail catalog work).

---

## 9. Open questions / gaps

The following are *known unknowns* — places where this survey could
not converge on a definitive answer:

1. **Does `passiveEndeavour/it5570-fan` actually work on Beelink SER?**
   The README only names AceMagic W1. Community claim "many white-label
   mini PCs" but no per-model verification has been published.
   Recommended: HIL on Phoenix's Beelink SER7 (already in arr stack).
2. **GMKtec EC chip identity** — entirely undocumented. K11 / K12 /
   EVO-X1 / EVO-X2 are blind. HIL acquisition recommended.
3. **MINISFORUM MS-A1 EC chip identity** — kizzard's external retrofit
   project bypasses it but doesn't reveal which chip is present.
4. **MINISFORUM BD795i SE PWM-write path** — Level1Techs users say
   "still to be written"; do *any* PWM register writes actually go
   through, or is the `pwm_enable` channel locked?
5. **AOOSTAR R7 PWM control** — only RPM read is verified; PWM write
   pending.
6. **GEEKOM A6 / A5 Pro EC chip** — only "fan ramps under stress" is
   evidence. Could be NCT, ITE, or NPCM.
7. **Does the IT5570 driver respect `pwm_enable=0` semantics like other
   chips?** README says writing 0 returns to EC firmware curve, writing
   1-100 sets manual %. This is a *different* enable-mode mapping than
   nct6775 / it87. ventd's RULE-ENVELOPE-14 readback logic may need a
   driver-class branch.
8. **IT5570 chip ID 0x8987 (per issue #2 of it5570-fan)** — is this a
   variant or a misidentification? May need its own chip entry if it
   turns out to be a real revision.

---

## 10. Citations (32 sources)

**OOT drivers and lm-sensors GitHub**
1. [passiveEndeavour/it5570-fan](https://github.com/passiveEndeavour/it5570-fan) — IT5570 OOT driver, AceMagic W1 reference
2. [frankcrawford/it87](https://github.com/frankcrawford/it87) — IT87xx OOT driver covering IT8613E
3. [Fred78290/nct6687d](https://github.com/Fred78290/nct6687d) — NCT6687-R OOT driver (does NOT cover mini-PCs in this bundle)
4. [frankcrawford/it87 issue #49 — IT5570 support request](https://github.com/frankcrawford/it87/issues/49) — Chatreey T9H, chip ID 0x5570 unsupported
5. [lm-sensors/lm-sensors issue #411 — IT5570E chip ID](https://github.com/lm-sensors/lm-sensors/issues/411) — modprobe `it87 force_id=0x5570` doesn't work
6. [Fred78290/nct6687d issue #119](https://github.com/Fred78290/nct6687d/issues/119) — kernel 6.11 pwm_enable=99 regression
7. [passiveEndeavour/it5570-fan issue #2](https://github.com/passiveEndeavour/it5570-fan/issues/2) — chip ID 0x8987 unsupported
8. [tjaworski/AceMagic-S1-LED-TFT-Linux](https://github.com/tjaworski/AceMagic-S1-LED-TFT-Linux) — AceMagic S1 LED+TFT (orthogonal to fans)
9. [zehnm/aoostar-rs](https://github.com/zehnm/aoostar-rs) — AOOSTAR WTR Max / GEM12+ Pro LCD reverse-engineering
10. [kizzard/minisforum-ms-a1-fan-controller](https://github.com/kizzard/minisforum-ms-a1-fan-controller) — MS-A1 external Trinket M0 retrofit
11. [Rem0o/FanControl.Releases discussion #3026](https://github.com/Rem0o/FanControl.Releases/discussions/3026) — Windows FanControl mini-PC support discussion

**Kernel docs**
12. [Kernel driver nct6775](https://docs.kernel.org/hwmon/nct6775.html) — chip variants list
13. [Kernel driver nct6683](https://docs.kernel.org/hwmon/nct6683.html) — read-only-by-default policy
14. [Kernel driver it87](https://docs.kernel.org/hwmon/it87.html) — supported chip variants

**Phoronix and review sites**
15. [Phoronix — Linux 6.5 Adding Support For NCT6799D Sensors](https://www.phoronix.com/news/Linux-6.5-NCT6799D)
16. [Phoronix — Linux 6.7 HWMON (X670E Taichi nct6683 dual-chip)](https://www.phoronix.com/news/Linux-6.7-HWMON)
17. [ServeTheHome — Beelink SER7 Review](https://www.servethehome.com/beelink-ser7-review-a-smaller-and-cheaper-amd-ryzen-7-7840hs-mini-pc/beelink-ser7-cpu-heatsink-fan-1/)
18. [ServeTheHome — Minisforum MS-A2 Review](https://www.servethehome.com/minisforum-ms-a2-review-an-almost-perfect-amd-ryzen-intel-10gbe-homelab-system/)
19. [ServeTheHome — fanless i3-N305 firewall review (CWWK family)](https://www.servethehome.com/almost-a-decade-in-the-making-our-fanless-intel-i3-n305-2-5gbe-firewall-review/)
20. [AnandTech — GEEKOM AS 6 (ASUS PN53) Review](https://www.anandtech.com/show/18964/geekom-as-6-asus-pn53-review-ryzen-9-6900hx-packs-punches-in-a-petite-package/2)
21. [CNX Software — GEEKOM A8 Ubuntu 24.04 review](https://www.cnx-software.com/2024/06/11/geekom-a8-review-ubuntu-24-04-linux-amd-ryzen-8945hs-mini-pc/)
22. [CNX Software — GEEKOM A7 Ubuntu 22.04 review](https://www.cnx-software.com/2024/02/24/geekom-a7-mini-pc-review-ubuntu-22-04-ubuntu-24-04-linux/)
23. [CNX Software — GEEKOM A6 Ubuntu 24.04 review](https://www.cnx-software.com/2025/02/16/geekom-a6-review-ubuntu-24-04-tested-on-an-amd-ryzen-7-6800h-mini-pc/)
24. [Liliputing — AOOSTAR GEM12 Review](https://liliputing.com/aoostar-gem12-review-mini-pc-with-oculink-2-5-gbe-lan-and-up-to-ryzen-7-8845hs/)
25. [Liliputing — AOOSTAR R7 Review](https://liliputing.com/aoostar-r7-review-an-affordable-2-bay-diy-nas-with-ryzen-7-5700u-and-a-pcie-nvme-ssd/)
26. [Archimago — CWWK RJ36 i3-N305 fanless review](https://archimago.blogspot.com/2024/02/review-hunsn-cwwk-rj36-fanless-minipc.html)

**Community forums and blogs**
27. [pcfe.net — Minisforum MS-01 Workstation sensors initial config](http://blog.pcfe.net/hugo/posts/2025-02-22-minisforum-ms-01-sensors/)
28. [alcroito.eu — Beelink S12 N100 sensors in Proxmox 9 Web UI](https://blog.croitor.eu/posts/proxmox_beelink_s12_sensors/)
29. [niziak.spox.org wiki — AOOSTAR R7 5825U setup with frankcrawford/it87](https://wiki.niziak.spox.org/hw:server:aoostar_r7)
30. [Manjaro Forum — Beelink SER4 4800U thermals issue (no Linux fan control path)](https://forum.manjaro.org/t/beelink-ser4-4800u-critical-thermals-issue/121051)
31. [Level1Techs — MINISFORUM BD795i SE Fan Control discussion](https://forum.level1techs.com/t/minisforum-bd795i-se-fan-control/228646)
32. [Proxmox forum — kernel 6.2.16-4-pve IT8613E pwmconfig regression](https://forum.proxmox.com/threads/new-kernel-6-2-16-4-pve-brought-pwmconfig-problem-with-ite-it8613e.130721/)
33. [minipcunion.com forum — BIOS fields set to "Default string" (Topton AMR5 example)](https://www.minipcunion.com/viewtopic.php?t=4301)
34. [Beelink Forum SER7 fan curve guide](https://bbs.bee-link.com/d/6037-ser-7-fan-control-setting-the-fan-curve-manually-through-bios)
35. [GMKtec K8 Plus & K11 fan settings adjustment guide](https://www.gmktec.com/pages/k8-plus-and-k11-fan-settings-adjustment-guide)
36. [GMKtec K12 fan speed setting modification guide](https://www.gmktec.com/pages/k12-fan-speed-setting-modification-operation-guide)
37. [GMKtec EVO-X2 fan speed adjustment guide](https://www.gmktec.com/pages/evo-x2-fan-speed-adjustment-guide)
38. [Level1Techs — Topton/CWWK motherboards for DIY NAS](https://forum.level1techs.com/t/are-topton-cwwk-motherboards-okay-to-use-for-a-diy-nas/212600)
39. [williamlam.com — improving thermals on Minisforum MS-A2](https://williamlam.com/2025/09/quick-tip-improving-thermals-on-minisforum-ms-a2.html)
40. [AIDA64 forum — MS-A1 fans not detected](https://forums.aida64.com/topic/16404-aida64-doesnt-detect-any-fans-of-minisforum-ms-a1-barebone/)

**Cross-reference internal**
41. [R28 master priority table — row #7 OEM mini-PC IT5570 OOT recommendation](./R28-master.md)
42. [R28 Agent H specialty hardware survey](./R28-fan-control-failure-modes-AGENT-H-specialty.md)
43. [R28 Agent E2 kernel gates](./R28-fan-control-failure-modes-AGENT-E2-kernel-gates.md)

---

## 11. TL;DR for Phoenix's eyes only

Of the eight vendors:
- **One is solved on mainline:** MINISFORUM MS-01 (NCT6798D + nct6775).
- **Two are solved on OOT-driver-with-modprobe-args:** Beelink Mini S
  / EQ-Intel and AOOSTAR R7 / WTR (both IT8613E + frankcrawford/it87).
- **One has a verified OOT-driver path:** AceMagic W1 (IT5570 +
  passiveEndeavour/it5570-fan).
- **Three are reasonably-likely-extrapolation OOT paths:** Beelink SER,
  MINISFORUM UM/HX, GEEKOM A7/A8 (all IT5570 by family pattern, no
  per-model HIL verification published).
- **Two are basically blind:** GMKtec (no public sensors-detect dump
  for any model) and CWWK/Topton-active (Default string DMI + IT8613E
  but with kernel-≥6.2 regression baggage).
- **Two are firmware-locked:** MINISFORUM MS-A1, MS-A2.
- **Many SKUs are passively cooled / fanless:** Topton industrial,
  CWWK firewall, AceMagic T8 lower-spec — these correctly classify as
  `OutcomeMonitorOnly` evidence `no_controllable_channels`.

The 22 board YAML rows + 2 chip rows in §7 are the concrete
v0.5.12-deliverable list. Most ship `verified: false` — that's correct
and matches existing catalog convention.

The single highest-leverage HIL acquisition for ventd's catalog
quality would be **Beelink SER7 + Beelink Mini S12 dual-test**:
SER7 confirms IT5570 OOT path on Phoenix's existing arr-stack
hardware, Mini S12 confirms IT8613E force_id path. Both unblock the
broader vendor coverage by family pattern.
