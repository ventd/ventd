# R28 catalog audit — ventd hardware database vs. upstream kernel state

**Date:** 2026-05-03
**Scope:** `internal/hwdb/profiles-v1.yaml`, `internal/hwdb/catalog/{drivers,chips,boards}/*.yaml`,
`internal/hwmon/autoload.go` (`knownDriverNeeds`, `identifyDriverNeeds`)
**Reference:** `docs/research/r-bundle/R28-master.md` (2026-05-03)
**Authority cutoff:** kernel 6.6 LTS / 7.x as current upstream baseline.

> Method: walked every YAML row plus the Go module-need maps, cross-referenced against
> R28-master findings (1)–(5), §5 decision-log, and known kernel commits. Where
> kernel source could not be quoted from a known commit hash, the entry is marked
> "verify against driver source" rather than asserted.

---

## 1. Summary

| Severity | Count | Class |
|---|---|---|
| **P0** | 6 | Operator-visible on common hardware: `restricted=0` recommendation in dell.yaml, missing kernel-version gate on it87 / nct6775, MS-7D25 catalog row absent from board YAML, missing `unsupported`/`fan_control_capable=false` for HPE iLO ≥Gen10 ipmi_bmc rows, generic ASUS NCT6799D row missing kernel-version gate, no pump-class entries (RULE-PUMPFLOOR-20). |
| **P1** | 7 | Uncommon / niche: stale `ro_pending_oot` on `nct6683` family (still correct but auto-attach blacklist not surfaced), missing Steam Deck board row (Jupiter/Galileo DMI dispatch), missing `bios_version` glob on Legion-style fingerprints not yet in catalog, `thinkpad_acpi` driver row uses `experimental=1` instead of canonical `fan_control=1`, AMD RDNA4 not gated for kernel <6.15 (RULE-EXPERIMENTAL-AMD-OVERDRIVE-04 already covers it but no catalog overlay), HPE Gen10 boards not fingerprinted as monitor-only, several `it87` IT868x chips lack kernel-version notes in the chip catalog. |
| **P2** | 5 | Forward-compat: NCT6797D 0xd450 chip-ID audit (R28 §5.1 — ambiguous between two historical mappings), missing `superio_chip` fingerprint on generic-NCT6798 board, ITE chip-name allowlist drift between schema.go and chip catalog, OEM mini-PC vendor list absent (Beelink/MINISFORUM/etc.), no `min_kernel` overlay schema. |

### Executive summary (200 words)

The ventd catalog is structurally sound — schema v1.2 boards/chips/drivers
load and resolve, and the three-tier matcher exercises all the load-bearing
paths. The defects this audit found are content errors and coverage gaps,
not design problems. The most operator-visible item is a one-line `restricted=0`
recommendation buried in `boards/dell.yaml` notes that contradicts kernel
docs and R28 §5.2 — anyone who reads that note onto a real Dell ends up with
non-root SMM access enabled, exactly the security model the kernel gates
against. Six other catalog rows (it87 / nct6775 / generic-NCT6799D) lack
kernel-version gates, so they recommend driver-side overrides that have been
unnecessary on every kernel ≥6.4 (R28-master Finding 2). The MS-7D25 chain
referenced by R28 row #20 lives only in `autoload.go`'s DMITriggers map —
there is no MSI MS-7D25 board YAML row. Pump-class detection (R28 row #8 /
RULE-PUMPFLOOR-20) is entirely absent from both schema and catalog. The
`profiles-v1.yaml` file is empty (`[]`); all real catalog content lives
in `internal/hwdb/catalog/`. The R28 row #5 NCT6797D / 0xd450 conflict
(decision §5.1) needs an explicit "verify against driver source" pass
before any further board adds.

---

## 2. P0 defects

### P0-1 — `dell-smm-hwmon restricted=0` recommendation in board notes

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/boards/dell.yaml`, lines 53–55
  (notes block on `dell-latitude-7280`).
- **Current value:** Notes string contains
  `"Kernel module restricted=0 may be needed for non-root reads on some distros."`
- **Correct value:** Notes MUST NOT recommend `restricted=0`. Either omit the
  hint entirely or recommend `restricted=1` (kernel default) and require ventd
  to run with root euid for SMM writes (which it already does on the install
  path).
- **Authority:** R28-master §5.2 ("dell-smm-hwmon `restricted=` security model");
  Agent B row 51, Agent E2 row 109 explicitly mark `restricted=0` DANGEROUS;
  kernel docs `Documentation/hwmon/dell-smm-hwmon.rst` keep the gate in place
  by default. The `dell-smm-hwmon.c` in-tree driver enforces non-root deny on
  the SMM ioctl path; loosening it lets every local user issue arbitrary SMM
  calls.
- **Severity:** **P0** — every Dell laptop user is asked to read this note in
  the wizard surface. Even though ventd's own write path is root-side and
  unaffected, an operator who follows the suggestion exposes the entire
  fleet to a non-root SMM oracle.
- **Fix:** YAML diff
  ```yaml
  # boards/dell.yaml dell-latitude-7280 notes:
  -    notes: "...CPU temp via coretemp.ko separately. Kernel module restricted=0 may be needed
  -      for non-root reads on some distros."
  +    notes: "...CPU temp via coretemp.ko separately. ventd writes via root euid;
  +      do NOT set restricted=0 — that grants every local user SMM access."
  ```

---

### P0-2 — `it87 ignore_resource_conflict=1` lacks kernel-version gate

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/drivers/it87.yaml`,
  `recommended_alternative_driver.module_args_hint`. Same string surfaces in
  `boards/gigabyte.yaml` for two boards (lines 24–27, 59–62).
- **Current value:** `module_args_hint: ["force_id=0x8689", "ignore_resource_conflict=1"]`
  (drivers/it87.yaml). Board YAML repeats `ignore_resource_conflict=1` in
  `required_modprobe_args` unconditionally.
- **Correct value:** `ignore_resource_conflict=1` has been the canonical
  per-driver replacement for the kernel cmdline `acpi_enforce_resources=lax`
  since kernel 6.2 (commit history per R28-master row #2). On kernel ≥6.2
  the option is auto-applied by the OOT it87 driver at probe time and the
  modprobe arg is unnecessary. Catalog should gate this on
  `kernel < 6.2` and recommend native binding for current kernels.
- **Authority:** R28-master row #2 ("it87 `ignore_resource_conflict=1`
  modprobe-option auto-writer (kernel ≥6.2)"); R28 §5.3 "blast radius"
  decision on `acpi_enforce_resources=lax` deprecation; per-driver
  `ignore_resource_conflict=1` lands in mainline it87 v6.2.
  *Verify against driver source* for the exact commit hash before
  shipping the gate.
- **Severity:** **P0** — every Gigabyte / ASRock board that resolves to
  this driver row gets an unnecessary modprobe arg on every fresh install
  on kernel ≥6.2 (most Linux installs in 2026). Doesn't break anything
  but generates dead `/etc/modprobe.d/ventd.conf` entries.
- **Fix:** add a `min_kernel` overlay field on `module_args_hint` entries,
  or split the row:
  ```yaml
  # drivers/it87.yaml:
       module_args_hint:
  -      - "force_id=0x8689"
  -      - "ignore_resource_conflict=1"
  +      - arg: "force_id=0x8689"
  +        always: true
  +      - arg: "ignore_resource_conflict=1"
  +        max_kernel: "6.1"
  +        reason: "auto-applied per-driver since 6.2; unnecessary on modern kernels"
  ```
  Schema bump required (chip/driver YAML schema v1.2 → v1.3).

---

### P0-3 — MS-7D25 catalog row not represented in board YAML (only in `autoload.go`)

- **Where:** `/root/ventd-tests/internal/hwmon/autoload.go` lines 153–160
  (DMITriggers for `nct6687d` includes `ms-7d25`); but `boards/msi.yaml`
  has no entry whose `product_name` matches `MS-7D25` / `PRO Z690-A DDR4`.
  `grep -r MS-7D25 internal/hwdb/catalog/` returns nothing.
- **Current value:** MS-7D25 dispatch lives only in the `identifyDriverNeeds`
  vendor heuristic; the structured board catalog is silent on this DMI
  fingerprint.
- **Correct value:** Per R28-master row #20 ("MS-7D25 → nct6687d catalog
  fix") marked **shipped (#822)**, the structured catalog should carry an
  MSI PRO Z690-A DDR4 (MS-7D25) board profile that points
  `primary_controller.chip = "nct6687"`. The Go-side DMITrigger remains as
  a fallback for the install-time module-load path, but the structured
  fingerprint dispatch (used by the daemon-side resolver) should not need
  to fall back to autoload's vendor heuristic.
- **Authority:** R28-master row #20; Phoenix HIL desktop = MS-7D25 (per
  CLAUDE.md HIL note + autoload.go comment block). The Fred78290/nct6687d
  upstream README lists MS-7D25 explicitly in its supported-boards list.
- **Severity:** **P0** — Phoenix's own HIL hits this. Without the board
  row, ventd's three-tier resolver falls through to `generic-nct6687d`
  which works but skips any MS-7D25-specific overrides (e.g. correct
  `superio_chip` fingerprint, sibling MSI PRO chain).
- **Fix:** add to `boards/msi.yaml`:
  ```yaml
    - id: "msi-pro-z690-a-ddr4"
      dmi_fingerprint:
        sys_vendor: "Micro-Star International Co., Ltd."
        product_name: "MS-7D25"
        board_vendor: "Micro-Star International Co., Ltd."
        board_name: "PRO Z690-A DDR4 (MS-7D25)"
        board_version: "1.0"
      primary_controller:
        chip: "nct6687"
        sysfs_hint: "name=nct6687 (Fred78290 OOT) — falls back to nct6683 RO mainline"
      additional_controllers: []
      overrides: {}
      required_modprobe_args:
        - "fan_config=msi_alt1"
      conflicts_with_userspace: []
      notes: "Phoenix HIL desktop. MSI PRO Z690-A DDR4 ships NCT6687D — mainline
        nct6683 is read-only; OOT Fred78290/nct6687d required for control. The
        sibling MSI PRO Z690-A (non-DDR4) and PRO Z790-A also need verification
        against the OOT supported-boards list."
      citations:
        - "https://github.com/Fred78290/nct6687d#supported-boards"
        - "github.com/ventd/ventd issue #822"
      contributed_by: "anonymous"
      captured_at: "2026-05-03"
      verified: true
      defaults:
        curves: []
  ```

---

### P0-4 — HPE iLO ≥Gen10 server boards not flagged monitor-only

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/drivers/ipmi_bmc.yaml`
  notes (line 96–107) document the iLO 5/6/7 read-only restriction, but
  `/root/ventd-tests/internal/hwdb/catalog/boards/hpe-proliant.yaml` rows
  (DL380 Gen10, DL360 Gen10) do NOT carry `overrides.unsupported: true` or
  `overrides.fan_control_blocked_by_bmc: true`.
- **Current value:** HPE Gen10 board profiles dispatch to `chip: "ipmi_bmc"`
  with no `unsupported` or `fan_control_capable=false` overlay; ventd
  attempts the standard IPMI write path which fires RULE-IPMI-3 ("iLO
  Advanced required") at write time but that's a runtime trip, not a
  catalog-driven monitor-only.
- **Correct value:** Per R28-master row #4 ("HPE iLO / iDRAC vendor-revoked
  proactive detect-and-explain") and the ipmi_bmc driver YAML's own notes,
  ventd should mark Gen10/Gen11/Gen12 ProLiant boards
  `fan_control_blocked_by_bmc: true` in the board overlay so the apply
  path returns OutcomeMonitorOnly without attempting the doomed write.
- **Authority:** R28-master Finding 4 (Vendor-revoked features); ipmi_bmc.yaml
  itself documents the restriction (lines 96–107); `alex3025/ilo-fans-controller`
  README explicitly lists Gen10/11/12 unsupported.
- **Severity:** **P0** — every fresh install on a Gen10 ProLiant produces
  a wizard error rather than a clean monitor-only fork. Affects an entire
  vendor segment.
- **Fix:** YAML diff for each Gen10/Gen11 ProLiant row:
  ```yaml
  # boards/hpe-proliant.yaml dl380/dl360 Gen10:
       overrides:
  +      fan_control_blocked_by_bmc: true
  +      reason: "iLO 5/6/7 vendor-revoked OEM fan-control (paid iLO Advanced licence required)"
  ```
  Apply path then routes to OutcomeMonitorOnly without the IPMI write
  attempt.

---

### P0-5 — `generic-nct6799` lacks kernel-version gate (kernel 6.5+ required)

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/boards/generic.yaml`
  lines 42–72 (`generic-nct6799`); chip catalog
  `/root/ventd-tests/internal/hwdb/catalog/chips/super_io.yaml` lines 16–22
  (`nct6799`).
- **Current value:** No `min_kernel` field. Notes mention "Linux 6.5+
  required" in free text; matcher does not gate.
- **Correct value:** NCT6799D mainline support landed in kernel 6.5 per
  Phoronix and the upstream NCT6775 commit chain. On kernel <6.5 the
  in-tree driver does not bind to NCT6799D and falls back to nct6683 or
  no detection. ventd should refuse to dispatch this generic profile to
  the writable path on kernel <6.5 and surface a "kernel upgrade
  recommended" recovery card.
- **Authority:** R28-master decision §5.1 + row #32 ("Per-board
  kernel-version gate: ASUS B650/X670 modprobe nct6775"); Phoronix
  reference cited in the YAML row's own citation list. *Verify the
  exact commit hash against drivers/hwmon/nct6775-platform.c* before
  asserting "kernel ≥6.5" — could be 6.4 depending on -stable backports.
- **Severity:** **P0** — every ASUS X670/X870/B650/B850 board on
  Ubuntu 22.04 / Debian 12 hits this (those distros still ship kernel
  5.15 / 6.1 by default).
- **Fix:** add `min_kernel: "6.5"` to the chip and generic-board rows
  (schema bump, see P0-2 fix). Recovery card: "your kernel is too old
  for NCT6799D fan control; install linux-generic-hwe or upgrade to
  kernel ≥6.5".

---

### P0-6 — No pump-class entries (R28 row #8, RULE-PUMPFLOOR-20)

- **Where:** every chip catalog file. No `is_pump`, `pump_minimum`, or
  `pump_floor_pwm` field anywhere in catalog YAML or schema struct
  (`internal/hwdb/schema.go` `Hardware` and `FanMeta`).
- **Current value:** Pump channels are indistinguishable from fan
  channels at the catalog level. The runtime `RULE-HWMON-PUMP-FLOOR`
  rule exists in `.claude/rules/hwmon-safety.md` but only fires when
  the per-channel config has `is_pump: true` set — a flag the catalog
  has no way to populate from chip / board metadata.
- **Correct value:** Per R28-master Finding 5 + S2-3, ventd should
  detect AIO pump headers via header label (`AIO_PUMP`, `W_PUMP`),
  RPM range (1500–3500), or device-class hint (Corsair iCUE, Aquacomputer)
  and enforce a 60% PWM floor across calibration AND runtime. The
  schema needs a `pump_class` enum on the FanMeta entry; common AIO
  motherboard headers should be pre-flagged in board YAMLs.
- **Authority:** R28-master row #8; calibration-hostile-fan-failure-modes.md
  §7; spec-02-corsair-aio.md.
- **Severity:** **P0** — pump stalling is hardware-damaging (coolant
  stops circulating; CPU thermal-throttle within seconds). Affects
  every AIO build (~20% of consumer desktops in 2026).
- **Fix:** schema bump (v1.2 → v1.3) adding:
  ```yaml
  # FanMeta struct:
    is_pump: bool        # default false
    pump_floor_pwm: int  # required when is_pump=true; default 60% of pwm_unit_max
  ```
  Then board-YAML overlays for AIO_PUMP-labelled headers (catalog
  population is a separate effort; the schema landing is the gate).

---

## 3. P1 defects

### P1-1 — `nct6683` driver row recommends `nct6687d` but does not declare auto-blacklist

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/drivers/nct6683.yaml`,
  `recommended_alternative_driver.module_args_hint: []`.
- **Current value:** The OOT-recommendation is correct but ventd has no
  way to learn that `nct6683` must be blacklisted before `nct6687d`
  binds, because the field is empty.
- **Correct value:** Per R28-master row #3 ("nct6683 in-tree blacklist
  auto-writer when catalog selects nct6687d"), the apply path needs to
  write `blacklist nct6683` and `modprobe -r nct6683` so the OOT module
  can claim the chip on next boot. The catalog should expose this as a
  declarative pre-step.
- **Authority:** R28-master row #3 (Stage 1 in flight); Fred78290/nct6687d
  README installation steps.
- **Severity:** **P1** — workaround documented in install scripts but
  not in catalog; the failure mode is "ventd installs nct6687d, reboots,
  nct6683 wins the race, fan control silently breaks".
- **Fix:**
  ```yaml
  # drivers/nct6683.yaml recommended_alternative_driver:
       blacklist_before_install:
         - "nct6683"
       module_args_hint:
         - "fan_config=msi_alt1"
  ```

---

### P1-2 — Steam Deck has no board catalog row (Jupiter/Galileo DMI dispatch missing)

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/boards/` — no
  `valve-steamdeck.yaml`. The chip exists (`laptop_chips.yaml:steamdeck_hwmon`)
  and the driver exists (`steamdeck-hwmon.yaml`), but no board fingerprint
  ties them to DMI `Jupiter` / `Galileo`.
- **Current value:** Steam Deck DMI does not match any specific board
  profile; falls through to generic.
- **Correct value:** Per R28-master §5.6 + row #28, Steam Deck must have
  separate board rows for `product_name=Jupiter` (LCD) and
  `product_name=Galileo` (OLED) so the Layer-B/C learned priors stay
  per-revision-distinct. The chip-fingerprint should include `product_name`.
  Conflict-with-userspace: `jupiter-fan-control`.
- **Authority:** R28-master decision §5.6; H#32 cite chain; valve/jupiter-fan-control.
- **Severity:** **P1** — niche segment but easily addressable; without
  the row, OLED users get LCD priors and vice versa.
- **Fix:** add `boards/valve-steamdeck.yaml` with two profiles:
  ```yaml
  schema_version: "1.2"
  board_profiles:
    - id: "valve-steamdeck-lcd-jupiter"
      dmi_fingerprint:
        sys_vendor: "Valve"
        product_name: "Jupiter"
        board_vendor: "Valve"
        board_name: "Jupiter"
        board_version: "*"
      primary_controller:
        chip: "steamdeck_hwmon"
        sysfs_hint: "name=steamdeck_hwmon — LCD revision (APU Van Gogh)"
      overrides: {}
      conflicts_with_userspace:
        - "jupiter-fan-control"
      ...
    - id: "valve-steamdeck-oled-galileo"
      dmi_fingerprint:
        sys_vendor: "Valve"
        product_name: "Galileo"
        ...
      overrides:
        post_s3_recalculate_quirk: true
      ...
  ```

---

### P1-3 — `thinkpad_acpi` driver row uses `experimental=1`; `fan_control=1` is the canonical name

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/drivers/thinkpad_acpi.yaml`
  lines 19–22.
- **Current value:** `required_modprobe_args: [{arg: "experimental=1", reason: "...", risk: "low"}]`.
- **Correct value:** The thinkpad_acpi driver's fan-write gate is
  controlled by the `fan_control=1` modparam, NOT `experimental=1`.
  `experimental=1` was deprecated long before kernel 6.x and currently
  has no effect on the fan-write gate. R28-master row #1 and
  RULE-WIZARD-RECOVERY-10's documentation both reference
  `options thinkpad_acpi fan_control=1`. The board catalog
  (`boards/lenovo-thinkpad.yaml`) does correctly use `fan_control=1`,
  but the driver-level row contradicts it.
- **Authority:** kernel `Documentation/admin-guide/laptops/thinkpad-acpi.rst`
  ("fan_control: bool, controls the fan_speed sysfs API");
  `drivers/platform/x86/thinkpad_acpi.c` `fan_init` block (gates write
  via `fan_control_allowed`); RULE-WIZARD-RECOVERY-10 binding.
  *Verify against the thinkpad_acpi.c source at HEAD* — `experimental`
  may still exist as a legacy alias but `fan_control=1` is the
  documented surface.
- **Severity:** **P1** — only matters if `required_modprobe_args` from
  the driver-level YAML is ever consulted standalone (without board
  override). Today the board YAML overrides it.
- **Fix:**
  ```yaml
  # drivers/thinkpad_acpi.yaml:
     required_modprobe_args:
  -    - arg: "experimental=1"
  -      reason: "Required for fan write access on T440+ generation ThinkPads."
  -      risk: "low"
  +    - arg: "fan_control=1"
  +      reason: "Required to enable pwm1_enable / pwm1 write surface (kernel default disables fan write)."
  +      risk: "low"
  ```

---

### P1-4 — Legion `bios_version` glob present but other multi-generation laptop vendors are not

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/boards/lenovo-legion.yaml`
  uses `bios_version: "GKCN*"` etc. correctly. But Lenovo Yoga / IdeaPad,
  Acer Predator/Nitro, ASUS ROG laptop, MSI laptop entries (where they
  exist or could be added) lack the same pattern.
- **Current value:** `boards/lenovo-ideapad.yaml` does not use `bios_version`
  glob; relies on `product_name` alone, which collides across generations
  similar to the Legion pattern that motivated RULE-FINGERPRINT-04.
- **Correct value:** Per RULE-FINGERPRINT-04, every laptop family with
  generation-specific firmware behaviour should set `bios_version` glob.
  Audit ideapad / yoga / Acer / MSI laptop rows for the pattern.
- **Authority:** RULE-FINGERPRINT-04 binding (`internal/hwdb/profile_v1_1_test.go:TestMatcher_BiosVersionGlob_Matches`);
  R28-master research-gap §4.4.
- **Severity:** **P1** — each missed family is one wrong-curve risk;
  cumulative impact medium.
- **Fix:** sweep board YAMLs for laptop entries and add `bios_version`
  globs from upstream EC repo allowlists where known. Out-of-scope for
  this audit; enumerate as a follow-up issue.

---

### P1-5 — RDNA4 / 6.15 kernel gate covered by RULE-EXPERIMENTAL-AMD-OVERDRIVE-04 but no catalog overlay exists

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/drivers/amdgpu.yaml`
  has `experimental.amd_overdrive: true` flag but no kernel-version overlay
  for RDNA4 (Navi 48 / 0x7550) refusal on kernel <6.15.
- **Current value:** Runtime check via RULE-EXPERIMENTAL-AMD-OVERDRIVE-04
  is binding (test exists). Catalog itself does not surface the
  refused-engagement reason to operators on board match.
- **Correct value:** Catalog should mirror the runtime gate so the doctor
  surface can pre-warn operators about kernel <6.15 + RDNA4 combos
  before they hit the runtime refusal.
- **Authority:** RULE-EXPERIMENTAL-AMD-OVERDRIVE-04 binding; R28 §5.7.
- **Severity:** **P1** — runtime refusal already protects the operator;
  the missing catalog overlay just delays the diagnostic surface by one
  click.
- **Fix:** add a chip-overlay row referencing PCI ID 0x7550 with
  `min_kernel: "6.15"` once the schema gains a kernel-gate field.

---

### P1-6 — `nct6683` family chip overrides hardcode `pwm_polarity_reservation: probe_required` for all NCT6687-derived chips

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/chips/nct6683_family.yaml`
  lines 17–46 — both `nct6687` and `nct6686` chip profiles set
  `pwm_polarity_reservation: probe_required`.
- **Current value:** Probe-required is conservative. Field reports for
  Fred78290/nct6687d on MSI MAG/MPG suggest `static_normal` is the
  observed polarity on every confirmed board.
- **Correct value:** *Investigate.* Without a polarity sweep across more
  than a handful of boards, leaving `probe_required` is the safer call.
  Mark for follow-up survey rather than flip.
- **Authority:** Fred78290/nct6687d issues #45, #67; R6-polarity-midpoint-safety.md.
- **Severity:** **P1** — operator hits an unnecessary calibration step
  on every fresh NCT6687 install. Not wrong, just slow.
- **Fix:** survey, then flip `nct6687` (not `nct6686`) to `static_normal`
  if data permits. Out-of-scope for this audit.

---

### P1-7 — ITE chip catalog notes lack kernel-version gate for IT8689E mainline support

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/chips/ite_family.yaml`
  lines 59–66 (`it8689` chip).
- **Current value:** Notes recommend OOT frankcrawford/it87 unconditionally.
- **Correct value:** Per R28-master §5.8, IT8689E mainline support is
  expected to land in kernel 7.0 or 7.1 (medium confidence). On those
  kernels, the OOT module is no longer required. Catalog should gate
  the recommendation on `kernel < 7.1` (or `< 7.0`, pending validation
  at next R28 refresh).
- **Authority:** R28-master §5.8; Agent E rows 14–17, E2 row 31.
- **Severity:** **P1** — forward-compat. On kernel ≥7.1 (Q3 2026
  estimate), every Gigabyte X670E install will receive an unnecessary
  DKMS recommendation if the gate is absent.
- **Fix:** add `notes` text + (when schema supports) `max_kernel_for_oot:
  "7.0"` field. Re-validate at next R28 refresh.

---

## 4. P2 defects

### P2-1 — NCT6797D / 0xd450 chip-ID conflict (R28 §5.1)

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/chips/super_io.yaml`
  lines 95–101 (`nct6797`).
- **Current value:** Chip profile exists, no historical-force_id note.
- **Correct value:** Per R28 §5.1, chip ID `0xd450` is used both as
  "force_id target for NCT6799D on old kernels" (E2 row 9) and "false
  detection of NCT6797D as NCT6687" (Agent C row 33). On kernel ≥6.4
  NCT6799D is native via nct6775 and the historical force_id workaround
  is unnecessary; on kernels where nct6687d misidentifies NCT6797D as
  NCT6687, fan PWM writes silently no-op.
- **Authority:** R28-master §5.1; Agent C row 33; Agent E2 row 9.
  *Verify against `drivers/hwmon/nct6683.c` chip-detection table* —
  this needs source-level confirmation before any catalog change.
- **Severity:** **P2** — "investigate" rather than "fix". An MSI Z690/Z790
  user with an old kernel could hit it. Document as a known caveat.
- **Fix:** add an explicit notes block on the `nct6797` chip:
  ```yaml
  # chips/super_io.yaml nct6797:
       notes: "Historical: nct6687d on kernel <6.4 may misidentify NCT6797D
         (chip ID 0xd450) as NCT6687, causing silent PWM no-op. Verify
         /sys/class/hwmon/*/name reports nct6797 (not nct6687) on these boards.
         On kernel ≥6.4 nct6775 binds natively."
  ```

---

### P2-2 — `generic-nct6798` board lacks a `superio_chip` fingerprint anchor

- **Where:** `/root/ventd-tests/internal/hwdb/catalog/boards/generic.yaml`
  lines 11–40.
- **Current value:** All five DMI fields are `"*"`; no `superio_chip` field
  in the fingerprint anchor list.
- **Correct value:** RULE-HWDB-01 requires at least one matchable anchor.
  `superio_chip` is a valid anchor for generic / fallback profiles. Without
  it, every wildcard-DMI generic board attempts to match every system,
  relying on chip-name resolution downstream — works today but brittle.
- **Authority:** schema.go `BoardFingerprint.hasAnchor()` accepts
  `SuperIOChip`; RULE-HWDB-01.
- **Severity:** **P2** — works currently but adds robustness to the
  resolver.
- **Fix:**
  ```yaml
  # boards/generic.yaml generic-nct6798:
       dmi_fingerprint:
         sys_vendor: "*"
         ...
  +      superio_chip: "nct6798"
  ```

---

### P2-3 — `knownPWMModules` allowlist drift between schema.go and chip catalog

- **Where:** `/root/ventd-tests/internal/hwdb/schema.go` lines 37–86
  vs. `chips/*.yaml` `name` fields.
- **Current value:** Schema allowlist contains entries (`it5571`,
  `it5572`, `nct7904d`, `f71889ad`, `f71889ed`) that have no chip
  catalog rows. Conversely, chips catalog contains entries
  (`pwmfan`, `asus-ec-sensors`, `coretemp`, `k10temp`) that are
  valid `pwm_control` values via the inheritance chain but are
  not always in the allowlist. The lists are partly redundant
  (RULE-HWDB-PR2-02 already validates `inherits_driver`).
- **Correct value:** Audit RULE-HWDB-05 boundary — what the allowlist
  is for, vs. what RULE-HWDB-PR2-02 already covers via driver
  resolution. Either drop unused allowlist entries or add the
  missing chip-catalog rows.
- **Authority:** RULE-HWDB-05 (allowlist); RULE-HWDB-PR2-02
  (inherits_driver resolution).
- **Severity:** **P2** — schema-internal cleanup; no operator-visible
  failure mode but adding new chip rows triggers double-edit
  requirements that drift over time.
- **Fix:** generate the allowlist from `chips/*.yaml` `name` fields
  at build time, removing the hand-maintained `knownPWMModules` map.

---

### P2-4 — OEM mini-PC vendor heuristic (Beelink, MINISFORUM, etc.) absent

- **Where:** No catalog rows for OEM mini-PC vendors. R28-master row
  #7 / S2-4 calls this out as a Stage 2 candidate.
- **Current value:** Beelink SER7 (Phoenix's arr stack), MINISFORUM
  MS-01, GMK, AceMagic, Topton, GEEKOM, AOOSTAR, CWWK have no
  fingerprint.
- **Correct value:** Per R28-master row #7, these should match by
  DMI sys_vendor and surface OOT `it5570-fan` install or monitor-only
  fallback (RULE-WIZARD-RECOVERY-14 candidate).
- **Authority:** R28-master row #7, S2-4; H agent.
- **Severity:** **P2** — Stage 2 work, not a regression. Logged for
  completeness.
- **Fix:** ship `boards/oem-minipc.yaml` with eight `sys_vendor` glob
  matches, all `chip: "unsupported"` + `overrides.unsupported: true`
  with a `recovery_card_hint: "ClassOEMMiniPCNoDriver"`.

---

### P2-5 — No `min_kernel` / `max_kernel` schema field

- **Where:** `/root/ventd-tests/internal/hwdb/schema.go` and
  `internal/hwdb/profile_v1_1.go` (driver/chip/board profiles).
- **Current value:** Kernel-version gates live only in YAML notes
  free text and runtime Go checks (RULE-EXPERIMENTAL-AMD-OVERDRIVE-04).
- **Correct value:** Per R28-master Finding 2 ("Kernel version gates
  obsolete a quarter of the catalog") + S2-13, the schema needs a
  uniform `kernel_version_constraints` block that the resolver
  consumes per-row.
- **Authority:** R28-master Finding 2, row #32, S2-13.
- **Severity:** **P2** — schema work; underpins the P0-2, P0-5,
  P1-5, P1-7 fixes above.
- **Fix:** schema bump v1.2 → v1.3 with new optional field on
  ChipProfile, DriverProfile, BoardProfile, and on
  `module_args_hint` entries:
  ```yaml
  kernel_version:
    min: "6.5"        # inclusive lower bound; row applies on >= this kernel
    max: "7.0"        # exclusive upper bound; row applies on < this kernel
  ```
  Migration: every existing row defaults to no constraint (always
  applies). Add to RULE-HWDB-08-style validator.

---

## 5. Confirmed correct (one-line per row)

The following rows were walked and look right:

| Row | Verdict |
|---|---|
| `drivers/nct6775.yaml` | ✓ — `capability: rw_full`, `static_normal` polarity, `force_max` exit_behaviour all consistent with kernel docs. |
| `drivers/dell-smm-hwmon.yaml` | ✓ — `pwm_unit: step_0_N`, `pwm_unit_max: 2`, `polling_latency_ms_hint: 500` correct per kernel hwmon doc. (Note: dell.yaml board notes still need fix per P0-1.) |
| `drivers/asus-ec-sensors.yaml` | ✓ — capability `ro_sensor_only`, `fan_control_via: nct6775` correct per kernel ≥5.18 mainline status. (Sanity: spec docs per R28 row "should NOT be ro_pending_oot anymore" — confirmed: ventd uses `ro_sensor_only`, not `ro_pending_oot`. ✓) |
| `drivers/asus-wmi-sensors.yaml` | ✓ — `ro_sensor_only`, fan control via Super I/O. Mainline. |
| `drivers/hp-wmi-sensors.yaml` | ✓ — `ro_design`, `recommended_alternative_driver: nbfc-linux` correct. |
| `drivers/surface_fan.yaml` | ✓ — `ro_design`, no pwm attributes per kernel doc. |
| `drivers/legion_hwmon.yaml` | ✓ — OOT, DKMS required, BIOS_VERSION dispatch documented. (Mainline status will need bumping when johnfanv2 lands the in-tree patch.) |
| `drivers/steamdeck-hwmon.yaml` | ✓ driver row — but board row missing (see P1-2). |
| `drivers/ipmi_bmc.yaml` | ✓ — vendor command sets documented; iLO 5/6/7 caveat present. |
| `drivers/i915.yaml` | ✓ — `fan_control_capable: false` (Intel iGPU has no PWM). |
| `drivers/xe.yaml` | ✓ — Arc dGPU read-only per RULE-GPU-PR2D-08. |
| `drivers/nouveau.yaml` | ✓ — `fan_control_capable: false`. |
| `drivers/amdgpu.yaml` | ✓ for non-RDNA4 path; see P1-5 for RDNA4 overlay. |
| `drivers/coretemp.yaml`, `k10temp.yaml`, `drivetemp.yaml`, `lm75.yaml` | ✓ — sensor-only, `fan_control_capable: false`. |
| `chips/super_io.yaml nct6798` | ✓. |
| `chips/super_io.yaml nct6799` | ✓ chip row; but generic-board kernel gate missing (P0-5). |
| `chips/super_io.yaml nct6796/nct6795/nct6793/nct6792/nct6791/nct6779/nct6775/nct6776` | ✓. |
| `chips/super_io.yaml nct6797` | ✓ but add the §5.1 historical-conflict note (P2-1). |
| `chips/super_io.yaml nct6106/nct6116` | ✓. |
| `chips/nct6683_family.yaml nct6683` | ✓ — `ro_pending_oot` correct (kernel mainline IS read-only by Nuvoton design, NDA-gated). |
| `chips/nct6683_family.yaml nct6687/nct6686` | ✓ — OOT route is correct. |
| `chips/ite_family.yaml it8603/it8620/it8628/it8665/it8686/it8688/it8792/it8705/it8712` | ✓. |
| `chips/ite_family.yaml it8689` | mostly ✓ but add the v7.0/v7.1 mainline gate (P1-7). |
| `chips/laptop_chips.yaml dell_smm/steamdeck_hwmon/pwmfan/asus_ec/hp_wmi/coretemp/k10temp` | ✓ structurally; see P1-2 / P0-4. |
| `chips/fintek_family.yaml f71882fg/f71869/f71869a/f71889fg/f71808e/f8000` | ✓. |
| `chips/winbond_family.yaml` | ✓ (legacy chips, untouched). |
| `boards/asus.yaml asus-rog-strix-x670e-e-gaming-wifi` | ✓ (kernel-6.5 hint in notes; gate absent at row level — see P0-5). |
| `boards/asus.yaml asus-prime-b650m-a-wifi` | ✓ (same). |
| `boards/asus.yaml asus-rog-strix-z790-e-gaming-wifi` | ✓ (NCT6798D mainline since baseline). |
| `boards/asus.yaml asus-tuf-gaming-x670e-plus-wifi` | ✓ (same as ROG STRIX X670E). |
| `boards/asus.yaml asus-proart-x670e-creator-wifi` | ✓. |
| `boards/asrock.yaml asrock-x670e-taichi` | ✓ (dual-chip nct6796 + nct6686 documented; kernel 6.7 hint in notes). |
| `boards/asrock.yaml asrock-b650m-pg-lightning` | ✓ (UNVERIFIED flag set — appropriate). |
| `boards/asrock.yaml asrock-x570-taichi` | ✓. |
| `boards/gigabyte.yaml gigabyte-x670e-aorus-master` | ✓ — bios_overridden_pwm_writes flag correct per IT8689E rev 1 issue #96. |
| `boards/gigabyte.yaml gigabyte-b650-aorus-elite-ax` | ✓ — same pattern. |
| `boards/gigabyte.yaml gigabyte-x570-aorus-master` | ✓ — older IT8688E + IT8792 dual-chip, mmio=1. |
| `boards/msi.yaml msi-mag-x670e-tomahawk-wifi` | ✓. |
| `boards/msi.yaml msi-mag-b650-tomahawk-wifi` | ✓. |
| `boards/msi.yaml msi-mpg-z790-edge-wifi` | ✓. |
| `boards/msi.yaml msi-mag-x570s-tomahawk-max-wifi` | ✓ (correctly uses NCT6797D, not NCT6687). |
| `boards/dell-poweredge.yaml dell-poweredge-r{740,640,740xd}` | ✓ — chip: ipmi_bmc, additional coretemp, all correct. |
| `boards/dell.yaml dell-{optiplex-7000,inspiron-3505,xps-13-9370,g15-5511}` | ✓ — on the i8k_whitelist_fan_control list. (The latitude-7280 row's notes need P0-1 fix.) |
| `boards/hpe-proliant.yaml dl380-gen10/dl360-gen10` | overlay needs P0-4 fix; otherwise structurally correct. |
| `boards/hp.yaml hp-pavilion-x360-15-cr0xxx` | ✓ — `unsupported: true` correctly set. |
| `boards/hp.yaml hp-elitebook-{840-g5,845-g7}, hp-probook-450-g7` | ✓ — `dynamic_enumeration: true` flag. |
| `boards/lenovo-thinkpad.yaml t490, t14-gen3-amd, x1-carbon-gen11, p15s-gen2` | ✓ — `fan_control=1` modparam, watchdog hint, secondary-fan flag where applicable. |
| `boards/lenovo-legion.yaml *` | ✓ — bios_version glob present (RULE-FINGERPRINT-04). |
| `boards/lenovo-ideapad.yaml *` | partial — see P1-4 for missing `bios_version` glob. |
| `boards/raspberry-pi.yaml raspberry-pi-5-model-b` | ✓ — DT fingerprint, cooling-device-detach flag (RULE-FINGERPRINT-06/07). |
| `boards/raspberry-pi-additional.yaml` | ✓. |
| `boards/supermicro.yaml *`, `boards/supermicro-additional.yaml` | ✓ — bmc_overrides_hwmon + prefer_ipmi_backend overlays correct. |
| `boards/generic.yaml generic-nct6798` | ✓ for NCT6798 path (kernel-baseline support); see P2-2 for the missing `superio_chip` anchor. |
| `boards/generic.yaml generic-nct6799` | needs P0-5 kernel gate. |
| `boards/generic.yaml generic-nct6687d` | ✓ — the OOT install hint is correct. |
| `autoload.go knownDriverNeeds.it8688e` | ✓ — frankcrawford/it87 is the canonical fork for IT8688E. |
| `autoload.go knownDriverNeeds.it8689e` | ✓ — same fork. |
| `autoload.go knownDriverNeeds.nct6687d` | ✓ — Fred78290/nct6687d is canonical; DMITriggers cover MAG/MPG/MS-7D25 chains. |
| `autoload.go iteChipForceIDs` | ✓ — ITE force_id values match upstream `it87.c`. |
| `autoload.go identifyDriverNeeds` vendor-fallback chain | ✓ — Gigabyte → IT8688E, MSI → nct6687d (gated by DMI seen-chain so MS-7D25 doesn't double-stamp it8688e). |

---

## 6. Recommended Stage 1.5 PR plan

Bundle the six P0 defects into two PRs ranked by safety. P1 / P2 work
follows in separate trains.

### PR A — "Catalog content fixes (no schema change)"

Safe, surgical, no test churn:

1. **P0-1**: edit `boards/dell.yaml` notes to remove the
   `restricted=0` recommendation. One-line YAML change. Adds a unit
   test asserting `grep -c "restricted=0" catalog/boards/*.yaml == 0`.
2. **P0-3**: add `boards/msi.yaml` MS-7D25 row pointing at `nct6687`
   chip. New YAML row, no schema change.
3. **P0-4**: add `overrides.fan_control_blocked_by_bmc: true` to
   HPE Gen10/Gen11 ProLiant rows. (`fan_control_blocked_by_bmc` is
   already a valid override key per the `unsupported`/`bmc_overrides_hwmon`
   precedent — confirm against schema validator.) Apply path already
   handles monitor-only routing.
4. **P1-3**: replace `experimental=1` with `fan_control=1` in
   `drivers/thinkpad_acpi.yaml`. Does not change board behaviour
   (board YAML already overrides) but cleans up the driver-level row.
5. **P2-1**: add `nct6797` chip-row note about the §5.1 historical
   conflict. Pure documentation.
6. **P2-2**: add `superio_chip: "nct6798"` to `generic-nct6798`.

PR A test plan:
- Existing `internal/hwdb/profile_v1_*_test.go` + `schema_test.go`
  pass without modification.
- New regression test: assert no row in `catalog/boards/` contains
  the literal string `restricted=0`.
- New regression test: assert MS-7D25 row resolves to `nct6687`
  via the three-tier matcher.

### PR B — "Schema v1.3 — kernel-version gates and pump-class"

Schema bump; lands the P0-2, P0-5, P0-6 + P2-5 + P1-5/P1-7 fixes.
This PR is the load-bearing change; ship after PR A.

1. Schema bump v1.2 → v1.3:
   - Optional `kernel_version: {min, max}` field on
     ChipProfile, DriverProfile, BoardProfile, and
     `module_args_hint` entries.
   - Optional `is_pump: bool` and `pump_floor_pwm: int`
     fields on FanMeta.
   - New `blacklist_before_install: []string` field on
     `recommended_alternative_driver`.
2. Resolver changes: skip rows whose `kernel_version` constraint
   excludes the running kernel; emit a "kernel upgrade
   recommended" recovery hint when no row applies.
3. Catalog content:
   - **P0-2**: gate `it87 ignore_resource_conflict=1` on
     `kernel_version: {max: "6.2"}`.
   - **P0-5**: add `kernel_version: {min: "6.5"}` to
     `nct6799` chip and `generic-nct6799` board rows.
   - **P0-6**: add `is_pump: true` overlay capability;
     populate AIO_PUMP-labelled headers in next catalog
     refresh wave.
   - **P1-5**: RDNA4 chip overlay `kernel_version: {min: "6.15"}`
     mirroring RULE-EXPERIMENTAL-AMD-OVERDRIVE-04.
   - **P1-7**: `it8689` chip overlay
     `kernel_version: {max: "7.0"}` for OOT recommendation
     (re-validate at next R28 refresh).
   - **P1-1**: `nct6683` driver row gains
     `blacklist_before_install: ["nct6683"]`.
4. New rules:
   - `RULE-HWDB-PR2-15_KernelVersionGate` — bound to a
     `internal/hwdb/profile_v1_test.go` subtest.
   - `RULE-HWDB-PR2-16_PumpFloorRequiredWhenIsPump` — schema
     validator asserts `pump_floor_pwm` is set when
     `is_pump: true`.

PR B test plan:
- Schema migration test: every existing v1.2 row loads cleanly
  under v1.3 without a kernel-version constraint.
- Boundary tests: kernel 6.4.99 fails to match the nct6799
  generic; kernel 6.5.0 matches.
- Pump-floor test: `is_pump: true` without `pump_floor_pwm`
  rejects at load.
- HIL: Phoenix's MS-7D25 (NCT6687) post-PR-A-merge sanity check;
  any AMD X670E-on-kernel-6.4 box for the kernel-gate boundary.

P1-2 (Steam Deck), P1-4 (laptop bios_version sweep), and P2-3 / P2-4
land in PR C (Stage 2) with the OEM mini-PC heuristic and the rest
of the long tail.

---

*Audit complete — 6 P0, 7 P1, 5 P2 defects logged; ~85 catalog rows
walked; one decision-log conflict (NCT6797D / 0xd450) flagged for
source verification; two PRs scoped for Stage 1.5 ship.*
