# Hardware profile library — schema v1 reference

This document describes the v1 schema for ventd's hardware profile library,
the frozen fingerprint hash function, the per-platform state directory layout,
and the migration policy for future schema versions.

**Audience:** contributors adding board profiles, maintainers debugging matcher
behaviour, and spec-05 authors consuming predictive thermal hints.

For the full design rationale see:
- `specs/spec-03-profile-library.md` — base spec (matcher, capture pipeline)
- `specs/spec-03-amendment-predictive.md` — predictive hints amendment

---

## 1. Overview

ventd embeds a read-only YAML file (`internal/hwdb/profiles-v1.yaml`) that
maps board fingerprints to hardware metadata: fan counts, PWM control modules,
default control curves, and optional predictive thermal hints. The matcher (PR
2) uses this data to pre-configure the daemon on first run; the calibration
pipeline (PR 4) enriches the per-platform record from live measurements; the
predictive thermal model (spec-05) consumes the hints.

The schema is frozen at v1 for the duration of the v0.5.x release series.
Breaking changes require a `schema_version` bump and a registered migration
function (see §5).

---

## 2. Schema v1 reference

```yaml
- id: "msi-meg-x570-unify"          # REQUIRED. kebab-case. Primary key.
                                     # Stable forever — used as on-disk dir name.
  schema_version: 1                  # REQUIRED. Must be in supportedVersions.

  fingerprint:                       # REQUIRED. At least one anchor field.
    dmi_sys_vendor: "Micro-Star International Co., Ltd."
    dmi_product_name: "MS-7C35"
    dmi_board_vendor: "Micro-Star International Co., Ltd."
    dmi_board_name: "MEG X570 UNIFY"
    dmi_board_version:               # Optional list; empty = match any version.
      - "1.0"
      - "2.0"
    family: "x570-unify"             # Optional fuzzy-match anchor.
    superio_chip: "nct6798d"         # Optional kernel module name.

  hardware:                          # REQUIRED.
    fan_count: 6
    pwm_control: "nct6798d"          # REQUIRED. Must be in knownPWMModules.
    temp_sensors:                    # Optional.
      - "k10temp"
      - "nct6798d"
      - "nvme"
    fans:                            # Optional per-fan metadata.
      - id: 1                        # 1-indexed to match hwmon convention.
        label: "CPU_FAN"
        stall_pwm_min: 60            # Required when any curve has allow_stop:true
                                     # for this fan (RULE-HWDB-09).
      - id: 2
        label: "CHA_FAN1"
        stall_pwm_min: 50
      - id: 3
        label: "CHA_FAN2"
        # No stall_pwm_min — allow_stop forbidden for any curve using fan 3.
    quirks:                          # Optional map<string,bool>.
      nct6798d_pwm3_broken: true

  defaults:                          # Optional.
    cpu_sensor: "k10temp/Tctl"
    curves:
      - role: "cpu"                  # Arbitrary string label.
        fan_ids: [1]
        allow_stop: false            # RULE-HWDB-09 cross-validates stall_pwm_min.
        points:                      # RULE-HWDB-04: monotonic non-decreasing.
          - [40, 30]                 # [temp_celsius, pwm_0_to_255]
          - [60, 50]
          - [75, 80]
          - [85, 100]
      - role: "case"
        fan_ids: [2, 3]
        allow_stop: false
        points:
          - [30, 20]
          - [50, 40]
          - [70, 70]
          - [80, 100]

  predictive_hints:                  # Optional. Consumed by spec-05.
    platform_heavy_threshold_watts: 80   # Must be > 0.
    thermal_critical_c: 95               # Must be > thermal_safe_ceiling_c + 5.
    thermal_safe_ceiling_c: 85

  sensor_trust:                      # Optional.
    - sensor: "nct6798d/temp4"
      trust: "untrusted"             # enum: trusted | untrusted | unknown
      reason: "stuck-at reading on this board rev"

  contributed_by: "anonymous"        # REQUIRED. "anonymous" or GitHub handle.
  captured_at: "2026-04-22"          # REQUIRED. ISO date.
  verified: true                     # REQUIRED. Boolean.
```

### Field rules summary

| Field | Required | Constraint |
|-------|----------|-----------|
| `id` | yes | kebab-case, unique across file |
| `schema_version` | yes | must be in `supportedVersions` |
| `fingerprint` | yes | at least one of: `dmi_board_vendor`, `dmi_board_name`, `dmi_product_name`, `superio_chip` |
| `hardware.pwm_control` | yes | must be in `knownPWMModules` (50 names; see `schema.go`) |
| `contributed_by` | yes | `"anonymous"` or `^[a-zA-Z0-9-]{1,39}$` |
| `captured_at` | yes | ISO date string |
| `verified` | yes | boolean |
| `defaults.curves[*].points` | when present | monotonic non-decreasing in both axes |
| `fans[*].stall_pwm_min` | conditional | required when any curve targeting this fan has `allow_stop: true` |
| `predictive_hints.thermal_critical_c` | when block present | must be `> thermal_safe_ceiling_c + 5` |

---

## 3. Fingerprint hash

The v1 fingerprint is a 16-character lowercase hex string (8 bytes from
SHA-256). It is the primary on-disk identifier for per-platform state.

### Input tuple (frozen)

```
tuple = join("|",
    canonicalise(sys_vendor),
    canonicalise(product_name),
    canonicalise(board_vendor),
    canonicalise(board_name),
    canonicalise(board_version),
    canonicalise(cpu_model_name),
    str(cpu_core_count),
)
fingerprint = hex(sha256(tuple)[:8])
```

**Sources:**

| Field | Path |
|-------|------|
| `sys_vendor` | `/sys/class/dmi/id/sys_vendor` |
| `product_name` | `/sys/class/dmi/id/product_name` |
| `board_vendor` | `/sys/class/dmi/id/board_vendor` |
| `board_name` | `/sys/class/dmi/id/board_name` |
| `board_version` | `/sys/class/dmi/id/board_version` |
| `cpu_model_name` | `/proc/cpuinfo` first `model name` line |
| `cpu_core_count` | `/proc/cpuinfo` count of `processor :` lines |

**`canonicalise` rules** (applied in order):

1. Trim leading/trailing whitespace.
2. Collapse runs of internal whitespace to a single space.
3. Convert to lowercase.
4. Replace empty string with `"<empty>"` — preserves positional stability when
   a BIOS revision omits `board_version`.

### Tuple stability guarantee

The tuple is frozen for v1. Any change to the field list, field order, or
canonicalise rules breaks existing per-platform state directory names. Such a
change requires a schema_version bump to v2 with a registered migration
function.

---

## 4. Storage layout

PR 1 documents this layout and reserves the paths. The embedded
`profiles-v1.yaml` is the only file PR 1 creates at runtime. Directory
creation is PR 4 capture work.

```
/var/lib/ventd/                            # system daemon mode
  fingerprint.json                         # current DMI fingerprint + ventd version
  platform/<dmi_fingerprint>/              # per-platform state (16-char hex)
    profile.yaml                           # matched profile (PR 2 writes)
    profile.yaml.bak                       # previous, for rollback
    # spec-05 will add:
    #   model.json, workloads.json, motifs.json, telemetry/
  profiles-v1.yaml                         # embedded read-only library
  profiles-pending/                        # PR 4 capture writes here
    <fingerprint>.yaml                     # 0640 ventd:ventd

# user-mode fallback (ventd --user)
$XDG_STATE_HOME/ventd/                     # default: ~/.local/state/ventd/
  <same layout, minus profiles-v1.yaml which stays embedded>
```

Each PR's ownership:

| Path | Created by |
|------|-----------|
| `profiles-v1.yaml` (embedded) | spec-03 PR 1 (this PR) |
| `platform/<fp>/profile.yaml` | spec-03 PR 2 (matcher) |
| `profiles-pending/<fp>.yaml` | spec-03 PR 4 (capture pipeline) |
| `platform/<fp>/model.json` etc. | spec-05 (predictive thermal) |

---

## 5. Schema v1 → v2 migration policy

The migration chain is enforced by `RULE-HWDB-07`. For every `v > 1` in
`supportedVersions`, a function `migrators[v]` must be registered in
`internal/hwdb/migrate.go` before the version can be loaded. The test
`TestMigrate_ChainIntegrity` fails at build time if the registry is
incomplete.

When adding schema v2:

1. Add `2` to `supportedVersions`.
2. Implement `migrate_1_to_2(doc []byte) ([]byte, error)` — takes a raw v1
   YAML document and returns a valid v2 YAML document.
3. Register: `migrators[2] = migrate_1_to_2`.
4. Update `CurrentVersion` from 1 to 2.
5. Add a `schema_version: 2` fixture under `testdata/` and a subtest.
6. Extend `RULE-HWDB-07` wording if the migration has observable invariants.

Migrations are raw-YAML transforms (not struct round-trips) so they work even
when the v1 struct types are removed in a future cleanup pass.

---

## 6. Adding a new profile

Contributors who want to add a board profile:

1. Fork the repo and create a branch.
2. Add an entry to `internal/hwdb/profiles-v1.yaml` following the §2 schema.
3. Run `go test ./internal/hwdb/...` — the strict decoder will report any
   schema errors with the rule name and failing field.
4. Open a PR. The nine invariant subtests in `TestSchema_Invariants` are the
   acceptance gate; CI must be green before merge.

The `contributed_by` field accepts `"anonymous"` or your GitHub handle. Real
names and email addresses are rejected by the PII gate (`RULE-HWDB-06`) before
they touch any code path.
