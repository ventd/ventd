# spec-03 Amendment — Profile Schema v1.1

**Status:** Draft. Targets PR landing in v0.5.0 catalog wave (scope-C).
**Bound spec sections:** spec-03 §6 (profile schema), §7 (matcher), §11
(driver catalog), §11.6 (tier-3 fallback semantics — NEW).
**Predecessor:** Schema v1 frozen in spec-03 PR 1 (#629, merged
2026-04-25). Migration skeleton already in place.

---

## Why this amendment exists

Three independent gaps surfaced during scope-B/C catalog research that
all require schema-level changes. Bundling them into a single v1.1
bump avoids three separate migration cycles:

1. **§SCHEMA-BIOSVER:** Lenovo Legion laptops dispatch on DMI
   `BIOS_VERSION` 4-char family prefix (GKCN, EUCN, H1CN, J2CN, M3CN,
   LPCN, N0CN, etc.), not `product_name` or `board_name`. Current v1
   schema cannot disambiguate Legion generations.
2. **§SCHEMA-DT:** ARM/SBC systems have no DMI. Pi 5 scope-B entry
   currently uses an `overrides.synthesize_fingerprint_from_dt: true`
   hack. Pi 4B, CM4-on-IO-board, and post-v1.0 ARM SBCs need first-class
   schema support.
3. **§HP-CONSUMER:** Tier-3 generic fallback silently routes consumer
   laptops with no Linux fan-control path to `coretemp-only`, hiding
   from the user that their fan curve will not be applied. An
   `overrides.unsupported: true` marker exists in scope-B HP entries
   but has no schema-validated semantics.

All three are additive, fully backward-compatible. Migration from v1 →
v1.1 is automatic for entries without the new fields.

---

## §SCHEMA-BIOSVER — `bios_version` field on `dmi_fingerprint`

### Schema change

Add optional `bios_version` field to `dmi_fingerprint`. Glob-supported
with `*` suffix, semantics identical to existing `product_name` /
`board_name` glob match.

```yaml
dmi_fingerprint:
  sys_vendor: "LENOVO"
  product_name: "82WS"        # existing — machine-type code
  board_vendor: "LENOVO"
  board_name: "*"
  board_version: "*"
  bios_version: "LPCN*"        # NEW — matches LPCN42WW, LPCN45WW, etc.
```

### Match semantics

- Field is optional. Entries without `bios_version` set default to `*`
  (matches anything) — identical to current v1 behavior.
- `bios_version: "GKCN*"` matches DMI `bios_version` strings like
  `GKCN58WW`, `GKCN65WW`, etc. (4-char family prefix + 2-digit minor +
  2-char "WW" suffix is the Lenovo Legion convention).
- Match is case-sensitive substring/prefix per existing tier-1 glob
  logic. No new match-engine code path.

### Source for DMI bios_version

Existing matcher already reads `/sys/class/dmi/id/bios_version` for
informational purposes. v1.1 promotes it to a fingerprint match key.

### Migration

Schema v1.1 validator accepts entries with or without `bios_version`.
Existing v1 entries deserialize cleanly into v1.1 with `bios_version`
defaulting to `*`. No catalog re-seeding required.

### Bound rules

- `RULE-FINGERPRINT-04`: matcher matches DMI `bios_version` glob when
  field is present.
- `RULE-FINGERPRINT-05`: fingerprint without `bios_version` field
  matches as v1 did (no behavior change).

### Why glob, not regex

Glob with `*` suffix covers the entire Lenovo Legion BIOS_VERSION
namespace observed across 9+ generations. No Lenovo BIOS family uses
non-prefix differentiation. Regex would add parser complexity for
zero practical gain. If future hardware needs richer matching, v1.2
can extend without breaking v1.1 entries.

---

## §SCHEMA-DT — `dt_fingerprint` for device-tree systems

### Schema change

Add `dt_fingerprint` as an alternative to `dmi_fingerprint`. A profile
must have **exactly one** of `dmi_fingerprint` or `dt_fingerprint` —
mutual exclusion enforced by validator.

```yaml
dt_fingerprint:                       # NEW — alternative to dmi_fingerprint
  compatible: "raspberrypi,5-model-b"
  model: "Raspberry Pi 5 Model B Rev 1.0"
```

### Field semantics

- `compatible`: matched against `/proc/device-tree/compatible` (a
  null-separated string list). Matcher returns true if any entry in
  the list matches the glob.
- `model`: matched against `/proc/device-tree/model` (a single string).
  Glob-supported.

Both fields are optional individually — but at least one must be
present (otherwise the fingerprint matches everything, which is a
schema validation error).

### Match semantics

- Matcher tries DMI first. If `/sys/class/dmi/id/sys_vendor` exists
  and is non-empty, only `dmi_fingerprint` profiles are considered.
- If DMI is absent (typical of Pi 4B, Pi 5, CM4-on-IO, NVIDIA Jetson,
  Pine64 family, etc.), falls through to `dt_fingerprint` profiles.
- A board profile with `dt_fingerprint` is **never** considered when
  DMI is present, even if /proc/device-tree happens to exist (some
  newer Snapdragon ACPI laptops have both — DMI wins by spec).

### Rationale for mutual exclusion

A board has either an SMBIOS/DMI table (x86, most ARM servers via
UEFI) or a device tree (most ARM SBCs, embedded boards). Allowing
both on a single profile would create ambiguous match precedence and
duplicates the per-board entry across two fingerprint blocks. Mutual
exclusion forces the catalog author to pick one — the matcher's
DMI-first / DT-fallback logic resolves the dispatch.

### Migration

Existing v1 entries with `dmi_fingerprint` are unchanged. Pi 5 scope-B
entry currently uses a synthesized DMI fingerprint
(`sys_vendor: "Raspberry Pi Foundation"`); when v1.1 ships, that
entry should be re-emitted with `dt_fingerprint` and the
`overrides.synthesize_fingerprint_from_dt: true` hack removed. This
re-emit can ride along with the schema PR or follow in scope-C wave 2.

### Bound rules

- `RULE-FINGERPRINT-06`: matcher matches device-tree `compatible` list
  glob when DMI is absent and `dt_fingerprint.compatible` is set.
- `RULE-FINGERPRINT-07`: matcher matches device-tree `model` string
  glob when DMI is absent and `dt_fingerprint.model` is set.
- `RULE-SCHEMA-08`: validator rejects profile with both
  `dmi_fingerprint` and `dt_fingerprint` set.

### Out of scope for v1.1

- NVIDIA Jetson Orin / Xavier (need both DMI and DT — deferred to v1.2
  if/when Asahi-style dual-tag becomes a real-world catalog need).
- Apple Silicon via Asahi (post-v1.0 — fan path itself doesn't exist
  in mainline as of kernel 6.13).

---

## §HP-CONSUMER — `overrides.unsupported: true` formal semantics

### Spec §11.6 (NEW) — Tier-3 fallback override: `unsupported`

The existing tier-3 generic fallback routes any board with no tier-1 or
tier-2 match to a generic profile (`generic-coretemp-only` or
`generic-amd-k10temp-only`). For consumer-class hardware that has **no
Linux fan-control path at all**, this silently masks the absence of
control: ventd reports sensor temperatures, generates an autocurve,
and applies it — but the writes go nowhere because no driver exists.

§11.6 adds a one-shot flag to suppress autocurve apply:

```yaml
overrides:
  unsupported: true
```

When matched:

- ventd's matcher emits one INFO log on first match: `"This hardware
  has no Linux fan-control driver. ventd will report sensors only."`
- Autocurve generation is **skipped** for this board (calibration
  phase noops).
- Sensor read paths still work normally (telemetry-only mode).
- Web UI shows a "Read-only mode" banner with explanation.
- `ventd doctor` (per spec-10 when shipped) reports the board as
  "intentionally unsupported, sensors-only".

### Schema change

Existing `overrides:` block already accepts arbitrary keys. v1.1
adds `unsupported: bool` to the list of validator-recognized keys so
typos like `unsuported: true` fail validation rather than silently
no-op.

### Migration

scope-B HP `hp-pavilion-x360-15-cr0xxx` already carries
`unsupported: true`. Behavior changes from "schema-tolerated noop" to
"validator-recognized, autocurve suppressed" automatically when v1.1
ships. No catalog edits required.

### Bound rules

- `RULE-OVERRIDE-UNSUPPORTED-01`: matcher with `unsupported: true`
  emits the INFO log exactly once per ventd lifetime per board id.
- `RULE-OVERRIDE-UNSUPPORTED-02`: calibration phase skips autocurve
  generation when `unsupported: true`.

### Consumer families to flag in scope-C catalog wave

Boards in these families should carry `unsupported: true` until/unless
a working Linux fan-control path materializes. Enumerating them
prevents tier-3 generic fallback from masking the absence:

| Family | Model examples | Why no path |
|---|---|---|
| HP Pavilion (all) | x360 15-cr0xxx, 14-dvxxxx, 15-cs0xxx | hp-wmi consumer ABI lacks fan_get/set |
| HP Envy (most) | x360 13-ay0xxx, 13-bd0xxx | same — consumer hp-wmi-sensors gap |
| HP Spectre (most) | x360 14-eaxxxx, x360 13-awxxxx | same |
| Acer Aspire (most post-2018) | 5 A515-54G, 7 A715-72G, 3 A315 | no kernel ABI; manage-fan deprecated |
| Asus Vivobook (some) | F512JA, X412UA, X512UA | asus-wmi exists but fan paths model-specific; flag-list-only |
| Microsoft Surface (consumer) | Surface Laptop 4/5, Surface Pro 8/9 | surface_fan covers Pro 7+ Pro variants only; Laptop family unsupported |

ThinkPad and Legion (covered by `thinkpad_acpi` and `legion_hwmon`
respectively) are NOT in this list — they have working paths.

### Why this isn't a "false advertising" risk for ventd

The whole point of the marker is to be **honest** about the
limitation. ventd ships with a clear "sensors-only on this board"
message instead of pretending fan control works. This aligns with the
v1.0 vision: "any-hardware support" includes honestly reporting when
hardware can't be controlled.

---

## Combined migration plan

| Step | Action | When |
|---|---|---|
| 1 | Land schema v1.1 validator + match-engine fields | spec-03 amendment PR (this doc → CC prompt) |
| 2 | Re-emit Pi 5 scope-B entry with `dt_fingerprint`, drop synth hack | Same PR, ride-along |
| 3 | Land Legion catalog (7 boards) using `bios_version` | scope-C PR §LEGION-1 |
| 4 | Land Pi 4B + CM4 catalog using `dt_fingerprint` | scope-C PR §SCHEMA-DT |
| 5 | Land HP consumer-family entries with `unsupported: true` | scope-C wave 2 (pre-v0.5.0 tag) |

---

## Test plan

Per .claude/rules invariant binding:

| Rule | Subtest |
|---|---|
| RULE-FINGERPRINT-04 | TestMatcher_BiosVersionGlob_Matches |
| RULE-FINGERPRINT-05 | TestMatcher_BiosVersionAbsent_BehavesAsV1 |
| RULE-FINGERPRINT-06 | TestMatcher_DTCompatibleGlob_Matches |
| RULE-FINGERPRINT-07 | TestMatcher_DTModelGlob_Matches |
| RULE-SCHEMA-08 | TestSchemaValidator_RejectsBothFingerprintTypes |
| RULE-OVERRIDE-UNSUPPORTED-01 | TestMatcher_UnsupportedEmitsLogOnce |
| RULE-OVERRIDE-UNSUPPORTED-02 | TestCalibration_UnsupportedSkipsAutocurve |

Test fixtures: synthesized DMI/DT input via existing `dmifake` and
new `dtfake` helper (mirrors `dmifake` pattern for /proc/device-tree).

---

## Out of scope for this amendment

- Per-board curve schema changes (still `defaults.curves: []` for all
  scope-C entries).
- spec-05 P4-HWCURVE 10-point firmware curve schema (Legion's debugfs
  format is prior art only — not codified into v1.1 schema).
- ec_command backend for Framework laptops (separate spec, see
  Framework memo in driver-amendments doc).
- Schema versioning beyond v1.1 (no v1.2 work scoped here).

---

## Estimated CC implementation cost

| Component | Tokens (Sonnet) |
|---|---|
| Schema v1.1 type + validator + migration | $4-6 |
| Match-engine `bios_version` glob | $1-2 |
| Match-engine `dt_fingerprint` + dtfake helper | $3-5 |
| `unsupported: true` calibration-skip path | $2-3 |
| Pi 5 entry re-emit with dt_fingerprint | $1 |
| 7 RULE-* tests + fixtures | $3-5 |
| **Total** | **$14-22** |

Within Phoenix's per-spec $10-30 envelope.

---

## References

- LenovoLegionLinux source (BIOS_VERSION dispatch evidence):
  https://github.com/johnfanv2/LenovoLegionLinux
- LegionFanControl model→BIOS prefix table:
  https://www.legionfancontrol.com/
- Issue #76 Slim 5 16APH8 dmesg (DMI_BIOS_VERSION:M3CN31WW):
  https://github.com/johnfanv2/LenovoLegionLinux/issues/76
- Issue #234 Pro 7 16ARX8H dmesg (DMI_BIOS_VERSION:LPCN45WW):
  https://github.com/johnfanv2/LenovoLegionLinux/issues/234
- Pi 5 device-tree cooling reference:
  https://github.com/raspberrypi/firmware/blob/master/boot/bcm2712-rpi-5-b.dtb
- HP hp-wmi-sensors business-class scope:
  https://docs.kernel.org/hwmon/hp-wmi-sensors.html
