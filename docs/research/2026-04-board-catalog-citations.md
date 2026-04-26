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
