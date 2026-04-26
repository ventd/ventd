# 2026-04 Board Catalog Citations — spec-03 PR 3

**Status:** research notes for spec-03 PR 3 seed entries.
**Scope:** 15 board profile entries committed to `internal/hwdb/catalog/boards/`.
**Methodology:** primary-source-only. Every DMI string and every chip
identification is backed by at least one citation from a verifiable source
(linux-hardware.org, GitHub issue, kernel mailing list, ArchWiki, Phoronix,
or distro forum thread).

## Verification stance for v0.5.0

**No entry is `verified: true` in this PR.** Verified status requires a human
running ventd against the actual board and confirming curves do not cause
thermal issues. Setting `verified: true` is a future commit, one board at a
time, gated on real-hardware testing.

The catalog still has value pre-verification:
- Tier-1 DMI fingerprint matching demonstrates the matcher works against
  real-world DMI strings across multiple vendors.
- Chip identity locks `additional_controllers` and `required_modprobe_args`
  at install time so spec-10 doctor + spec-11 wizard can give users actionable
  guidance ("install Fred78290/nct6687d for fan control") instead of generic
  failures.
- The `bios_overridden_pwm_writes` flag on Gigabyte X670E entries is the
  most operationally useful field — it tells ventd's calibration probe to
  expect writes-accepted-but-ineffective and route to monitor-only mode
  proactively.

## Source-quality ranking applied

When multiple sources disagreed, the following priority order was used:
1. Linux kernel mailing list patches (authoritative, names specific boards
   in commit messages).
2. linux-hardware.org HW probes (direct dmidecode output from real hardware,
   anonymised but board-name-verifiable).
3. lm-sensors GitHub issues with full sensors-detect output pasted.
4. Frankcrawford it87 / Fred78290 nct6687d OOT driver supported-board lists
   (maintainer-curated, but biased toward boards the maintainers can test).
5. asus-board-dsdt comprehensive table (community-curated, derived from
   ACPI dumps, generally accurate for chip-family identification).
6. Distro forum threads (Arch / Mint / Fedora) with sensors output.
7. Phoronix news posts (good for kernel-version cutoffs, not for individual
   boards).

Marketing pages (vendor product pages) were rejected as sources — they
describe Windows tooling, not Linux chip identity.

## Per-board citation index

### MSI

**msi-mag-x670e-tomahawk-wifi**
- DMI: linux-hardware.org probe `c3b398c792` and `217ca8a08d`, both showing
  `MS-7E12 / MAG X670E TOMAHAWK WIFI (MS-7E12) / 1.0`.
- Chip: Fedora user thread on FedoraForum confirms `nct6683` mainline detects
  NCT6687D (read-only) on this board. Fred78290 OOT writeable path needed.

**msi-mag-b650-tomahawk-wifi**
- Chip: Arch Linux forum thread shows `nct6687-isa-0a20` on MAG B650 Tomahawk
  WIFI with full sensor dump.
- Module config: msi_alt1 documented in Fred78290 supported list and ArchWiki.

**msi-mpg-z790-edge-wifi**
- DMI: lm-sensors GitHub issue #446 quotes `dmidecode | grep "Product Name"`
  output: MS-7D91 / MPG Z790 EDGE WIFI (MS-7D91).
- Chip: same issue, `dmesg | grep nct6683` shows "Found NCT6687D or compatible
  chip at 0x4e:0xa20".

**msi-mag-x570s-tomahawk-max-wifi**
- DMI: Open Hardware Monitor issue #1479 quotes "MSI MAG X570S TOMAHAWK MAX
  WIFI (MS-7D54)" with full sensors tree.
- Chip: same source identifies "Nuvoton NCT6797D" (NOT NCT6687D — older
  generation uses different chip).

### ASUS

**asus-rog-strix-x670e-e-gaming-wifi**
- Chip: asus-board-dsdt master README table line: `ASUS | ROG STRIX X670E-E
  GAMING WIFI | NCT6799D-R | N | N | Y | N?`.
- Kernel ABI: Phoronix Linux 6.5 NCT6799D article explicitly names this as
  one of the boards supported by the new driver.

**asus-prime-b650m-a-wifi**
- Chip: asus-board-dsdt table: `ASUS | PRIME B650M-A WIFI | NCT6799D-R |
  N | N | Y | N`.

**asus-rog-strix-z790-e-gaming-wifi**
- Chip: asus-board-dsdt table: `ASUS | ROG STRIX Z790-E GAMING WIFI |
  NCT6798D | N | N | Y | N`.
- Voltage scaling: LibreHardwareMonitor PR #1474 documents NCT6798D-family
  ASUS-specific Vcore scaling at 1.11x.

**asus-tuf-gaming-x670e-plus-wifi**
- Chip: asus-board-dsdt table + kernel mailing list patch [PATCH v2 2/2]
  hwmon (nct6775) explicitly lists "TUF GAMING X670E-PLUS WIFI" in the
  NCT6799D supported boards array.

**asus-proart-x670e-creator-wifi**
- DMI: Linux Mint forum thread shows full inxi output for ProArt X670E-CREATOR
  WIFI with BIOS 0805 dated 11/04/2022.
- Chip: asus-board-dsdt table confirms NCT6799D-R.

### Gigabyte

**gigabyte-x670e-aorus-master**
- DMI + chip: frankcrawford/it87 issue #96 quotes `cat /sys/class/dmi/id/board_name`
  showing "X670E AORUS MASTER", and `dmesg` showing "it87: Found IT8689E chip
  at 0xa40 [MMIO at 0x00000000fe100000], revision 1" and "Found IT8792E/IT8795E
  chip at 0xa60, revision 3".
- BIOS-override quirk: same issue documents "writing to PWM registers is
  accepted without error but has zero effect on actual fan speed. All 5 PWM
  channels behave the same way."
- Module args: `mmio=on`, `force_id=0x8689`, `ignore_resource_conflict=1`
  documented in same issue and in repo README.

**gigabyte-b650-aorus-elite-ax**
- Chip + write-block quirk: frankcrawford/it87 issue #68 shows IT8689E with
  full sensors dump (RPM readable, PWM writes ineffective).

**gigabyte-x570-aorus-master**
- Chip: asus-board-dsdt table cross-reference confirms IT8688E (older
  generation, not IT8689E).
- Dual-chip + mmio=1: bakman2/Gigabyte-Aorus gist documents the X570 Aorus
  Master mmio=1 setup with IT8688E + IT8792E.

### ASRock

**asrock-x670e-taichi**
- Dual chip identity: Phoronix forum thread on Linux 6.7 hwmon clarifies
  "The X670E Taichi has two sensors chips, the NCT6796D-S (nct6775) and the
  NCT6686D (nct6683)."
- Kernel 6.7 nct6683 support: Phoronix news article on Linux 6.7 HWMON.

**asrock-b650m-pg-lightning**
- Chip identity: family pattern from asus-board-dsdt cross-reference
  (B650E PG Riptide WiFi family). UNCONFIRMED — this is the weakest entry,
  flagged in the YAML notes. v0.5.x patch should confirm against real
  hardware.

**asrock-x570-taichi**
- Chip: asus-board-dsdt cross-reference + frankcrawford/it87 community
  reports of NCT6796D operation.

### Generic heuristics

**generic-nct6798**, **generic-nct6799**, **generic-nct6687d**
- Sourced from spec-03 amendment §11 tier-3 fallback semantics. Driver
  catalog entries (`internal/hwdb/catalog/drivers/nct6775.yaml`) provide
  the actual behaviour; these board profiles just route to the driver
  by chip name.
- Citations point to the kernel-level documentation and the chip-family
  evidence (asus-board-dsdt for NCT6799D scope, Fred78290 supported list
  for NCT6687D scope).

## Open hardware-validation gaps

These boards lack a Phoenix or community member with the actual hardware
to confirm `verified: true`:

- All 12 specific boards (no validators identified yet).
- Generic heuristics inherently cannot be `verified: true` — they're
  catch-all profiles, not specific hardware.

Validation strategy for v0.5.x patches:
1. Phoenix's MSI dual-boot (memory entry 4 — desktop 13900K target after
   dual-boot setup) → validate one ASUS Prime / TUF entry.
2. Community reports via spec-10 doctor + spec-11 wizard's diag-bundle
   capture path → users running ventd report back tier-1 match accuracy.
3. Spec-03 PR 4 capture pipeline (post-spec-11) → users contribute their
   own profile via `/var/lib/ventd/profiles-pending/` workflow.

## What's NOT in this PR (deferred to v0.5.x or later)

Per Phoenix's "Path A" scope decision in 2026-04-26 chat:
- Supermicro X11/X12/X13 server boards (await spec-01 IPMI cross-validation).
- Dell PowerEdge R740/R750 (await spec-01 IPMI hardware acquisition).
- Framework 13/16 AMD laptops (await spec-09 NBFC ship in v0.8.0).
- Raspberry Pi 5 (await SBC test rig).
- HPE ProLiant DL380 Gen10 (deferred).
- Lenovo Legion (high-priority acquisition per memory entry 30, awaits
  hardware purchase).
- Older Intel boards (Z390/Z490/Z590) covered by generic-nct6798 fallback.
- More ASUS Prime / TUF / ROG variants (catalog can grow indefinitely;
  v0.5.0 ships proof-of-concept).

These will be added in v0.5.x patches (one board per PR, ideally with
real-hardware validation flipping `verified: true` simultaneously).


---

# Scope-B append (2026-04-26)

# 2026-04 Board Catalog Citations — scope-B append

This section appends to the existing scope-A citations doc. Per-field
source-of-truth for each scope-B board YAML entry.

---

## Dell entries (`dell.yaml`)

### dell-latitude-7280

| Field | Source |
|---|---|
| sys_vendor "Dell Inc." | linux-hardware.org probes (consistent across all Dell post-2010) |
| product_name "Latitude 7280" | Phoenix box matrix #5 direct evidence + linux-hardware.org probes |
| chip "dell_smm" | docs.kernel.org/hwmon/dell-smm-hwmon.html — driver hwmon name |
| ro_sensor_only override | github.com/torvalds/linux/blob/master/drivers/hwmon/dell-smm-hwmon.c — Latitude 7280 absent from i8k_whitelist_fan_control as of 6.13 |
| Generic prefix match works | github.com/torvalds/linux source — i8k_dmi_table has "Dell Latitude" prefix entry |
| coretemp note | docs.kernel.org/hwmon/coretemp.html — separate driver |

### dell-optiplex-7000

| Field | Source |
|---|---|
| Whitelist entry | spinics.net/lists/linux-hwmon/msg22064.html — Armin Wolf v3 patch series, OptiPlex 7000 explicitly tested |
| I8K_FAN_30A3_31A3 SMM pair | Same patch series source |

### dell-inspiron-3505

| Field | Source |
|---|---|
| Whitelist entry | spinics.net/lists/linux-hwmon/msg22064.html — same patch series, Inspiron 3505 explicitly tested |

### dell-xps-13-9370

| Field | Source |
|---|---|
| Whitelist entry | patchew.org/linux/6e6b7a47-d0e3-4c5a-8be2-dfc58852da8e@radix.lt/ — Povilas Kanapickas v2 patch |
| SMM pair correction (30A3 not 34A3) | Same patch v2 changelog notes |

### dell-g15-5511

| Field | Source |
|---|---|
| Whitelist entry | patchew.org/linux/20240522210809.294488-1-W._5FArmin@gmx.de/ — Armin Wolf single-board patch May 2024 |
| Literal "Dell G15 5511" PRODUCT_NAME prefix | Patch source DMI_EXACT_MATCH inline |

---

## HP entries (`hp.yaml`)

### hp-pavilion-x360-15-cr0xxx

| Field | Source |
|---|---|
| sys_vendor "HP" | learnmesccm.com/cm/query-wmi-computer-model-sccm.html — confirms HP Pavilion x360 PRODUCT_NAME convention |
| unsupported flag | docs.kernel.org/hwmon/hp-wmi-sensors.html — "Hewlett-Packard (and some HP Compaq) BUSINESS-CLASS computers"; consumer Pavilion explicitly out of scope |
| hp-wmi vs hp-wmi-sensors distinction | github.com/torvalds/linux/blob/master/drivers/platform/x86/hp-wmi.c — hotkeys/rfkill driver, no fan path |

### hp-elitebook-840-g5

| Field | Source |
|---|---|
| Business-class supported | docs.kernel.org/hwmon/hp-wmi-sensors.html — driver scope statement |
| PRODUCT_NAME "HP EliteBook 840 G5" verbatim | badcaps.net forum DMI dump (linked in scope-B research log) |
| Dynamic enumeration | Same kernel doc — "creates the following sysfs attributes as necessary" |
| Alarm conflict with hp-wmi | Same kernel doc — "If the existing hp-wmi driver... is already loaded, alarm attributes will be unavailable" |

### hp-elitebook-845-g7

| Field | Source |
|---|---|
| Business-class supported | docs.kernel.org/hwmon/hp-wmi-sensors.html |
| PRODUCT_NAME "HP EliteBook 845 G7 Notebook PC" | HP product naming convention (Renoir-era laptops carry "Notebook PC" suffix) — cross-validated against EliteBook 840 G6 entry "HP EliteBook 840 G6" (no suffix); needs HIL re-verification before promoting verified:true |

### hp-probook-450-g7

| Field | Source |
|---|---|
| ProBook in scope of hp-wmi-sensors | docs.kernel.org/hwmon/hp-wmi-sensors.html (driver doesn't exclude ProBook line; HPBIOS_BIOSNumericSensor present on ProBook 450 G6/G7 generation per HP CMI white paper) |

---

## Lenovo IdeaPad/Yoga entries (`lenovo-ideapad.yaml`)

### lenovo-ideapad-flex5-14itl05

| Field | Source |
|---|---|
| product_name "82HU" machine type | Lenovo machine-type code published in BIOS DMI; Phoenix box matrix #7 direct |
| board_version convention | github.com/torvalds/linux/blob/master/Documentation/ABI/testing/sysfs-platform-ideapad-laptop — VPC2004 ACPI device + DMI_PRODUCT_VERSION pattern |
| fan_mode 0/1/2/4 enum | Same ABI doc — explicitly states state==3 invalid |
| VPCCMD_R_FAN probe model | github.com/endlessm/linux/blob/master/drivers/platform/x86/ideapad-laptop.c — ideapad_check_features() function |

### lenovo-ideapad-3-15iil05

| Field | Source |
|---|---|
| product_name "81WE" | Lenovo machine-type for IdeaPad 3 15IIL05 (Ice Lake i3/i5 generation) |

### lenovo-yoga-7-14iru9

| Field | Source |
|---|---|
| product_name "83JL" | Lenovo machine-type for Yoga 7 14IRU9 (Meteor Lake 2024) |

---

## Lenovo ThinkPad entries (`lenovo-thinkpad.yaml`)

### lenovo-thinkpad-t490

| Field | Source |
|---|---|
| product_name "20N20009MX" | One representative MTM out of the 20N2-prefixed family; ventd matcher uses prefix "20N2" |
| chip "thinkpad_acpi" | kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst |
| pwm_scale 0-7 mapping | Same doc — "Fan level, scaled from the firmware values of 0-7 to the hwmon scale of 0-255" |
| fan_control=1 modparam mandatory | Same doc — "fan control operations are disabled by default for safety reasons" |
| watchdog_seconds_default 120 | Same doc — "fan safety watchdog timer interval, in seconds. Minimum is 1 second, maximum is 120 seconds" |

### lenovo-thinkpad-t14-gen3-amd

| Field | Source |
|---|---|
| product_name "21CF" AMD vs "21AH" Intel | Lenovo PSREF (public reference) — T14 Gen 3 AMD = 21CF/21CG, Intel = 21AH/21AJ |
| k10temp note | docs.kernel.org/hwmon/k10temp.html |

### lenovo-thinkpad-x1-carbon-gen11

| Field | Source |
|---|---|
| product_name "21HM" | Lenovo PSREF — X1C Gen 11 = 21HM/21HN |
| stuck-pwm1 risk | forums.linuxmint.com/viewtopic.php?t=420745 — X380 directly observed; family pattern flag for X1C |

### lenovo-thinkpad-p15s-gen2

| Field | Source |
|---|---|
| product_name "20W6" | Lenovo PSREF — P15s Gen 2i = 20W6/20W7 |
| secondary_fan_uncontrollable | kernel.org/doc/Documentation/admin-guide/laptops/thinkpad-acpi.rst — "Some Lenovo ThinkPads support a secondary fan. This fan cannot be controlled separately, it shares the main fan control" |

---

## Supermicro entries (`supermicro.yaml`)

### supermicro-x11sch-f

| Field | Source |
|---|---|
| Board name "X11SCH-F" + AST2500 BMC | supermicro.com/manuals/motherboard/X11/MNL-2105.pdf — official manual |
| chip "nct6776" | spinics.net/lists/lm-sensors/msg38863.html — X9SRL-F NCT6776F detection (X11 inherits chip family from X9 via Coffee Lake server SKU continuity) — needs HIL re-verification |
| BMC overrides hwmon | Generic Supermicro IPMI behavior, well-documented in lm-sensors / homelab forums |

### supermicro-x10slh-f

| Field | Source |
|---|---|
| Board name "X10SLH-F" | Supermicro product page (verified in scope-A research) |
| chip "nct6776" | Same X9-family inheritance pattern; NCT6776F is the Coffee Lake / Haswell Xeon E3 server reference chip |

### supermicro-h12ssl-i

| Field | Source |
|---|---|
| Board name "H12SSL-I" | Supermicro product page |
| chip "nct7802" via I2C | sbexr.rabexc.org/latest/sources/56/28ba2371e0d4a5.html — kernel hwmon Kconfig confirms NCT7802 driver supports BMC I2C bus |

---

## Raspberry Pi entry (`raspberry-pi.yaml`)

### raspberry-pi-5-model-b

| Field | Source |
|---|---|
| sys_vendor synthesis "Raspberry Pi Foundation" | Convention; no DMI on stock Pi5 firmware. Synthesized from /proc/device-tree/model = "Raspberry Pi 5 Model B Rev 1.0" |
| chip "pwm_fan_dt" | emlogic.no/2024/09/step-by-step-thermal-management/ — Pi5 cooling-device + pwm-fan walkthrough |
| Default DT cooling levels (75/125/175/250 PWM) | forums.raspberrypi.com/viewtopic.php?t=359778 — RPi engineer-confirmed dtparam fan_temp{0..3}_speed defaults |
| sysfs path /sys/devices/platform/cooling_fan/hwmon/hwmonX/pwm1 | forums.raspberrypi.com/viewtopic.php?t=358188 — RPi engineer-confirmed Pi5 sysfs structure |
| Active Cooler vs Case Fan SKUs | RPi product line — both expose identical kernel hwmon path |

---

## Methodology notes (scope-B-specific)

1. **Less linux-hardware.org coverage for laptops than desktops.** Many
   business-laptop DMI strings are sourced from kernel patch
   submissions (where users include dmidecode -t system in their
   patch description) rather than HW probes. Cross-validation is
   weaker for laptop entries — promote to verified:true with extra
   caution.

2. **Lenovo PSREF (Product Specifications Reference) used for machine-type
   codes** that don't have public probe data. PSREF is Lenovo-canonical
   but is a marketing site, not a primary kernel-grade source. Where
   PSREF was used as sole source, the YAML notes field flags it.

3. **Supermicro server-board chip identity is harder to pin down** than
   consumer because BMCs hide much of the I/O. The X9 → X11 chip
   inheritance pattern is industry-standard but should be HIL-confirmed
   per board before promoting. Supermicro entries here are speculative
   on chip identity; their PRIMARY value is registering the
   `prefer_ipmi_backend: true` directive so ventd routes around the
   BMC contention problem.

4. **HP consumer 'unsupported' classification is conservative.** Some
   consumer HP machines (Spectre x360 13-some-revisions, Envy 13 some
   revisions) DO have HPBIOS_BIOSNumericSensor objects per scattered
   forum reports. We default to unsupported until HIL-verified.
   False-negatives are recoverable; false-positives waste user setup
   time.

5. **Pi 5 entry intentionally violates the "no schema work" scope-B
   rule** because the synthesized DMI fingerprint is a documented hack
   pending §SCHEMA-DT amendment. This is the cleanest way to land Pi
   5 hardware-recognition logic before the schema lands. The entry's
   `synthesize_fingerprint_from_dt: true` override flag is the durable
   migration marker.
