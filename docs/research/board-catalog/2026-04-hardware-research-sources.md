# ventd Hardware Research — Durable Sources Reference

**Purpose:** primary-source bookmarks for ongoing hardware catalog work
(spec-03 PR 3 and beyond). These sources have been used and verified in
spec-03 PR 3 scope-A research (2026-04-26) and are worth keeping for all
future board catalog additions, validation work, and chip-family questions.

**Methodology principle:** vendor marketing pages are NEVER cited. They
describe Windows tooling, not Linux chip identity. Use only sources that
provide DMI dumps, sensors-detect output, kernel commits, or maintainer-
curated chip-identity tables.

---

## Tier 1 — Authoritative primary sources

### Linux kernel mailing list (lore.kernel.org)

**Use for:** definitive supported-boards lists per driver, chip-family ABI
cutoffs, kernel version requirements.

- **Search:** https://lore.kernel.org/all/?q=
- **Pattern:** kernel patches name specific board strings in commit messages
  (the only authoritative source for "is this board supported by mainline").
- **Examples:**
  - https://www.spinics.net/lists/kernel/msg4644521.html — NCT6799D supported
    boards (ASUS B650/B660/X670)
  - https://www.uwsg.indiana.edu/hypermail/linux/kernel/2301.1/01860.html —
    full PATCH v2 with TUF GAMING X670E-PLUS WIFI + others enumerated

### Linux Hardware Database — linux-hardware.org

**Use for:** direct dmidecode output from real hardware, anonymised but
board-name-verifiable. Primary source for DMI fingerprint strings.

- **Board probes:** https://linux-hardware.org/?id=board:<vendor-slug>
  - Example: https://linux-hardware.org/?id=board:micro-star-mag-x670e-tomahawk-wifi-ms-7e12-1-0
- **Individual probe logs:** https://linux-hardware.org/?probe=<id>&log=dmidecode
- **DMI dumps repo:** https://github.com/linuxhw/DMI
- **ACPI dumps repo:** https://github.com/linuxhw/ACPI

### lm-sensors GitHub

**Use for:** sensors-detect output examples, chip detection edge cases,
unsupported-chip bug reports with full dmidecode pastes.

- **Repo:** https://github.com/lm-sensors/lm-sensors
- **Issues (rich primary source):** https://github.com/lm-sensors/lm-sensors/issues
- **Detection script (chip ID table):** https://github.com/lm-sensors/lm-sensors/blob/master/prog/detect/sensors-detect

---

## Tier 2 — Maintainer-curated reference tables

### asus-board-dsdt — comprehensive ASUS table

**Use for:** any ASUS board chip-identity question. ~800 boards, structured
table mapping board name → Super I/O chip → driver support status.

- **Repo:** https://github.com/asus-wmi-boards-sensors/asus-board-dsdt
- **Table:** README.md "Supported boards" section
- **Bugzilla anchor:** https://bugzilla.kernel.org/show_bug.cgi?id=204807
- **Source spreadsheet:** docs/linuxhw_DMI.csv in same repo

### frankcrawford/it87 — Gigabyte / ASUS / ASRock ITE chips

**Use for:** ITE Super I/O chips (IT8688E, IT8689E, IT8665E, IT8655E,
IT8628E, IT8792E). The OOT it87 driver covers chips mainline doesn't.

- **Repo:** https://github.com/frankcrawford/it87
- **Critical issues:**
  - #68: Gigabyte B650 Aorus Elite AX rev 1.2 IT8689E write-blocked
  - #96: Gigabyte X670E Aorus Master IT8689E rev 1 write-blocked (PRIMARY
    EVIDENCE for `bios_overridden_pwm_writes` field)

### Fred78290/nct6687d — MSI / some ASUS NCT6687D chips

**Use for:** MSI MPG/MAG B550/B650/B660/X670/Z590/Z690/Z790 fan-control
identity. Mainline `nct6683` only does read-only on these; this OOT
DKMS module provides writes.

- **Repo:** https://github.com/Fred78290/nct6687d
- **Supported board list:** README.md (look for `msi_alt1` and
  `msi_fan_brute_force` sections)

### LibreHardwareMonitor (LHM) — Windows-side cross-reference

**Use for:** chip-on-board confirmation when Linux sources don't cover a
specific board, voltage scaling factors, sensor-channel mapping. License
is MPL-2.0 — Phoenix's policy is manual register transcription only,
no code copy (memory entry 29).

- **Repo:** https://github.com/LibreHardwareMonitor/LibreHardwareMonitor
- **Useful PRs:**
  - #1474: NCT6798D voltage scaling improvements + ROG Strix Z790-E added
- **Issues with full chip dumps:**
  - #1479: MSI MAG X570S Tomahawk Max with Nuvoton NCT6797D identification

---

## Tier 3 — Driver source (when chip identity is in doubt)

For laptops + ARM/SBC + cros_ec, the kernel driver source itself often
hard-codes supported model strings. Useful for confirming exact DMI match.

### Mainline kernel hwmon drivers

- **Browse:** https://github.com/torvalds/linux/tree/master/drivers/hwmon
- **High-value files:**
  - `legion-laptop.c` — Lenovo Legion legion_hwmon supported models
    (10-point firmware curve via `pwm{1,2}_auto_point{1-10}_pwm` —
    direct prior art for spec-05 P4-HWCURVE per memory entry 30)
  - `dell-smm-hwmon.c` — Dell Latitude/Inspiron supported models
  - `nct6775-platform.c` — ASUS-WMI board support list
  - `hp-wmi-sensors.c` — HP supported models

### Mainline kernel platform drivers

- **Browse:** https://github.com/torvalds/linux/tree/master/drivers/platform/x86
- **High-value files:**
  - `ideapad-laptop.c` — Lenovo IdeaPad models (Flex5 path)
  - `cros_ec_lpcs` — Framework laptop path

### Framework Computer EC source

- **Repo:** https://github.com/FrameworkComputer/EmbeddedController
- **Use for:** Framework 13/16 AMD per-generation EC behaviour, DMI
  strings per Framework hardware revision

### Lenovo Legion community driver

- **Repo:** https://github.com/johnfanv2/LenovoLegionLinux
- **Use for:** Legion model coverage beyond mainline legion_hwmon, OOT
  module quirks per generation

---

## Tier 4 — Distro / community evidence

Use these when you need a sensors-detect output for a specific board and
no kernel-mailing-list patch covers it. Always cross-reference with at
least one Tier 1-3 source before trusting.

### Phoronix — kernel ABI cutoffs

- **Site:** https://www.phoronix.com/
- **Useful for:** kernel version requirements per chip family
  (e.g. NCT6799D needs 6.5+, ASRock X670E Taichi nct6683 needs 6.7+)
- **Examples:**
  - https://www.phoronix.com/news/Linux-6.5-NCT6799D
  - https://www.phoronix.com/news/Linux-6.7-HWMON

### Distro forums (Arch / Mint / Fedora / Manjaro)

- **Arch BBS:** https://bbs.archlinux.org/
- **ArchWiki lm_sensors:** https://wiki.archlinux.org/title/Lm_sensors
  (board-specific module-config tips, MSI / Gigabyte / ASRock quirks)
- **Linux Mint forums:** https://forums.linuxmint.com/
  (often has user-pasted full dmidecode + sensors output)
- **Fedora forum:** https://forums.fedoraforum.org/
  (Fedora 40+ users frequently first to hit new-kernel issues)
- **Manjaro:** https://forum.manjaro.org/

**Search pattern that works:** `<board name> sensors-detect linux`
or `<board name> nct6XXX OR it8XXX`. Reject results without sensors output.

### Level1Techs

- **Forum:** https://forum.level1techs.com/
- **Useful for:** community workarounds for boards not yet in mainline

---

## Field-specific source mappings

Use this when researching a specific schema field:

| Schema field | Where to find evidence |
|---|---|
| `dmi_fingerprint.*` | linux-hardware.org probe pages |
| `primary_controller.chip` | sensors-detect output (lm-sensors issues) or asus-board-dsdt table |
| `primary_controller.sysfs_hint` | `dmesg \| grep nct\|it87` from forum threads |
| `additional_controllers` | LHM PRs / Phoronix forum posts mentioning dual-chip boards |
| `overrides.cputin_floats` | Phoronix forum post #1375298 (universal ASUS Nuvoton issue) |
| `overrides.bios_overridden_pwm_writes` | frankcrawford/it87 issues #68, #96 |
| `required_modprobe_args` | OOT driver READMEs (Fred78290, frankcrawford) |
| `conflicts_with_userspace` | spec-03 amendment §11 + project knowledge userspace survey |
| `bios_version_min/max` | linux-hardware.org probes (UEFI date field per probe) |

---

## What NOT to use

- **Vendor product pages** (asus.com, msi.com, gigabyte.com) — describe
  Windows tooling, never tell you Linux chip identity.
- **Reviews / spec comparison sites** (Tom's Hardware, AnandTech, Newegg) —
  zero DMI value.
- **Reddit threads without sensors output** — anecdote-only, can't cite.
- **HWiNFO forum posts** — Windows tool, occasionally shows chip but not
  primary source.
- **AIDA64 sensor lists** — Windows tool, behind paywall.
- **AI-generated "compatibility list" sites** — frequently invented data.

---

## Workflow for a new board entry

1. **Find DMI strings.** Search linux-hardware.org for `?id=board:<vendor-slug>`.
   If exists → primary source. If not → search GitHub issues for the board
   name + `dmidecode`.

2. **Confirm Super I/O chip.** Check asus-board-dsdt table (ASUS), or search
   lm-sensors issues for `<board name> sensors-detect`. Reject "I think
   it's X" without sensors output.

3. **Check mainline support.** Search lore.kernel.org for the chip name +
   "Tested-by" or commit messages naming the board. This sets
   `bios_version_min` and module requirements.

4. **Check OOT driver coverage.** For ITE chips → frankcrawford/it87
   supported list. For NCT6687 → Fred78290/nct6687d list. For Legion →
   LenovoLegionLinux. Set `required_modprobe_args` accordingly.

5. **Check for known quirks.** Search GitHub issues across these repos
   for the chip + board combo. The big ones to look for:
   - PWM-write-accepted-but-ineffective (`bios_overridden_pwm_writes`)
   - Floating CPUTIN (universal ASUS pattern, `cputin_floats: true`)
   - Multi-chip boards (`additional_controllers`)
   - kernel-version-gated (`required_kernel_version` if added to schema)

6. **Cite at minimum 1 primary source per field.** If you can't find a
   primary source, leave the field null and document in `notes` why.

7. **Set `verified: false`.** Always. Verified status requires a human
   running ventd against the board.

---

## Useful past evidence anchors

These specific URLs were validated in scope-A research as high-quality
references — re-use them in future research where they're applicable.

### Cross-vendor chip ID tables
- https://github.com/lm-sensors/lm-sensors/blob/master/prog/detect/sensors-detect
  (canonical chip-ID-to-driver mapping with dev IDs)

### NCT6798D voltage scaling
- https://github.com/LibreHardwareMonitor/LibreHardwareMonitor/pull/1474

### CPUTIN floats — universal ASUS pattern
- https://www.phoronix.com/forums/forum/hardware/processors-memory/1375298

### IT8689E rev 1 vs rev 2 — BIOS-overridden writes
- https://github.com/frankcrawford/it87/issues/96 (rev 1, broken)
- https://github.com/frankcrawford/it87/issues/68 (rev 2, BIOS workaround
  exists)

### Gigabyte multi-chip mmio=1
- https://gist.github.com/bakman2/e801f342aaa7cade62d7bd54fd3eabd8

### NCT6799D kernel cutoff (6.5+)
- https://www.phoronix.com/news/Linux-6.5-NCT6799D
- https://www.spinics.net/lists/kernel/msg4644521.html

### ASRock X670E Taichi dual-chip clarification
- https://www.phoronix.com/forums/forum/hardware/motherboards-chipsets/1418593
- https://www.phoronix.com/news/Linux-6.7-HWMON

---

## Maintenance note

This doc lives at `docs/research/2026-04-hardware-research-sources.md`
in the ventd repo (commit alongside spec-03 PR 3 scope-A or as standalone).

Update when:
- A new authoritative primary source emerges (e.g. a new OOT driver repo
  for a chip family ventd cares about).
- A previously-cited URL goes 404 (replace with archive.org link or
  remove).
- A new schema field is added that requires its own source-mapping row.

Do NOT update for:
- Adding a single board's evidence — that goes in
  `docs/research/2026-04-board-catalog-citations.md` (the per-board
  citations index).
- Adding a single chip family — that goes in `docs/research/hwmon-research.md`
  (the master corpus per memory entry 19).
