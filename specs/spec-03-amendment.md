# Spec 03 — Amendment (predictive-thermal prerequisites)

**Status:** DRAFT — amends `spec-03-profile-library.md` to pre-bake the storage layout and schema evolution path that spec-05 depends on.
**Source:** spec-05 Phase 1 signal acquisition + persistence (§3, §7) requires a per-platform state directory keyed by DMI fingerprint, and versioned JSON with explicit migration contract.
**Apply when:** merge this amendment **into spec-03 before PR 1** of spec-03 starts.
**Author note:** this is non-binding framing; spec-03 retains final say. Amendment only adds requirements; nothing is removed.

---

## Why this amendment exists

spec-05 (predictive thermal) needs three things from spec-03 that spec-03 does not currently mandate:

1. A per-platform state directory under `/var/lib/ventd/` keyed by DMI fingerprint hash (used as the cache key for learned model state, workload signatures, motifs, telemetry ring buffer).
2. A frozen, documented `schema_version` migration contract on every JSON/YAML artefact ventd writes — not just `profiles.yaml`.
3. An optional `platform_heavy_threshold` field on profile entries, used by spec-05 §5.4 heavy-workload predicate.

Retrofitting these later means migrating files users have on disk, which is painful. Baking them into spec-03 v1 costs nothing now.

---

## §1 — Amendment to spec-03 §PR 1 (schema design freeze)

### §1.1 Add: storage layout requirement

spec-03 PR 1 schema freeze **must explicitly include the on-disk layout**:

    /var/lib/ventd/                                 # system daemon state root
      fingerprint.json                              # current DMI fingerprint + ventd version
      platform/<dmi_fingerprint>/                   # per-platform state
        profile.yaml                                # matched profile (copy of profiles.yaml entry)
        profile.yaml.bak                           # previous generation, on rollback
        # spec-05 will add: model.json, workloads.json, motifs.json, telemetry/
      profiles.yaml                                 # canonical profile library (read-only at runtime)

User-mode fallback (`ventd --user`):

    $XDG_STATE_HOME/ventd/                          # default: ~/.local/state/ventd/
      <same layout>

### §1.2 Add: DMI fingerprint hash function

Freeze the fingerprint hash input tuple in spec-03 PR 1:

    fingerprint_input = concat(
      dmi.sys_vendor,
      dmi.product_name,
      dmi.board_vendor,
      dmi.board_name,
      dmi.board_version,     # if non-empty
      cpu_model_name,
      cpu_core_count,
    )
    fingerprint = sha256(fingerprint_input)[:16]    # 8-byte hex prefix

Rationale: stable across BIOS minor-revision bumps (board_version is the noisy field, but we include it so that autotuned gains invalidate appropriately on major BIOS changes). CPU model + core count catches drop-in CPU swaps. GPU PCI IDs intentionally excluded — GPU swaps should not invalidate fan-zone models.

### §1.3 Add: schema invariant (extends §Schema invariants)

New rule:

- **RULE-HWDB-11**: All ventd-written JSON/YAML files (`profile.yaml`, `fingerprint.json`, and any future artefact) contain a `schema_version: N` field at the top level. Missing field rejects at load. Parser has a table `{filename → supported_versions}` and an unknown version rejects with a human-readable migration hint.

Bind to subtest in `internal/hwdb/schema_test.go`.

---

## §2 — Amendment to spec-03 §Schema v1 fields

### §2.1 Add: optional `platform_heavy_threshold`

Extend schema v1 with an optional field used by spec-05:

```yaml
- id: "msi-meg-x570-unify"
  schema_version: 1
  fingerprint: { ... }
  hardware: { ... }
  defaults: { ... }
  # NEW (optional, spec-05 consumer):
  predictive_hints:
    platform_heavy_threshold_watts: 80    # "heavy workload" delta-power threshold
    thermal_critical_c: 95                # absolute temperature above which Layer A hard-caps
    thermal_safe_ceiling_c: 85            # preferred steady-state ceiling
  sensor_trust: { ... }
```

The `predictive_hints` block is **optional** at spec-03 v1; missing field means spec-05 falls back to conservative defaults (heavy_threshold = 60 W, critical = 95 °C, safe_ceiling = 85 °C). Documenting the field now means we won't schema-migrate when spec-05 starts consuming it.

### §2.2 Add: schema invariant

- **RULE-HWDB-12**: If `predictive_hints` is present, `platform_heavy_threshold_watts > 0` and `thermal_critical_c > thermal_safe_ceiling_c + 5`. Non-compliant entries reject at load.

Bind to subtest.

---

## §3 — Amendment to spec-03 §PR 4 (capture pipeline)

### §3.1 Add: migration contract

spec-03 PR 1 documents the migration chain pattern. PR 4 (capture) inherits it:

- Each schema version N has a `migrate_N_to_N_plus_1(raw []byte) ([]byte, error)` pure function in `internal/hwdb/migrate.go`.
- At load: if `schema_version < current`, run the chain, atomically write back, log the migration event.
- If `schema_version > current` (downgrade): refuse to load, fall back to defaults, log loudly. Never corrupt newer state.

New rule:

- **RULE-HWDB-13**: Adding a new `schema_version` in the parser requires a new `migrate_N_to_N_plus_1` function. A test fails if `supported_versions` contains a version N where `migrate_<N-1>_to_N` is missing.

This rule already exists as RULE-HWDB-07 in spec-03; the amendment is to **extend its scope from `profiles.yaml` only to all ventd-written state files**.

---

## §4 — Amendment to spec-03 §Definition of done

Add the following to PR 1 DoD:

- [ ] `/var/lib/ventd/platform/<fingerprint>/` layout documented in `docs/hwdb-schema.md`.
- [ ] DMI fingerprint hash input tuple documented and pinned as v1 (changing the tuple = breaking change requiring v2 schema).
- [ ] RULE-HWDB-11 (schema_version on all written files) bound to subtest.
- [ ] RULE-HWDB-12 (predictive_hints validation) bound to subtest.
- [ ] User-mode (`--user`) fallback path `$XDG_STATE_HOME/ventd/` tested against a fixture home directory.

---

## §5 — Files added or changed by this amendment

**New files (in spec-03 PR 1 work):**
- `internal/hwdb/fingerprint.go` — DMI hash function (may already exist from P1-FP-01; extend to cover the frozen tuple).
- `internal/hwdb/fingerprint_test.go` — regression fixtures for hash stability.
- `internal/hwdb/migrate.go` — migration chain skeleton with zero migrations in v1 (the skeleton is what spec-05 and future specs extend).

**Modified files:**
- `docs/hwdb-schema.md` — add storage layout section + predictive_hints field reference.
- `.claude/rules/hwdb-schema.md` — add RULE-HWDB-11, -12, and clarify RULE-HWDB-13 scope.
- `internal/hwdb/schema.go` — add `PredictiveHints` struct with `omitempty` JSON/YAML tags.

**Out of scope (still):**
- Actually *using* the platform directory from spec-05 — that's spec-05 Group A work.
- Writing anything other than `profile.yaml` and `fingerprint.json` to the platform dir — spec-05 adds `model.json`, etc.
- Automatic population of `predictive_hints` from calibration — spec-05 autotune output feeds this; spec-03 just reserves the field.

---

## §6 — Cost impact

spec-03 PR 1 grows by ~15–25 % (one extra Go file, two extra rules, one docs section). Still Sonnet-scale, still one session. Opus consult scope unchanged (schema design review already covers fingerprint stability per spec-03 Opus consult item 3).

**Do not treat this amendment as a separate PR.** It folds into spec-03 PR 1 as additional scope. Merging it early is cheaper than retrofitting later.
