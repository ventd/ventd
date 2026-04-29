# spec-03 Amendment — Profile Schema v1.2

**Status:** Draft. Targets PR landing in v0.6.0 alongside spec-15 framework PR.
**Bound spec sections:** spec-03 §6 (profile schema), §11 (driver catalog),
spec-15 §2 (experimental block semantics).
**Predecessor:** Schema v1.1 frozen in spec-03 PRs #646–#649 (merged
2026-04-26). Migration skeleton already in place.

---

## Why this amendment exists

spec-15 introduces a typed `experimental:` block on profile entries. The
block carries semantically validated keys with eligibility-not-active
semantics — setting a key to `true` asserts the board *can* use a given
unlock path, but activation requires runtime CLI consent.

This is a single-concern bump, unlike v1.1 which bundled three
independent gaps. v1.2 lands one schema change, one validator change,
and one forward-compatibility shim. No catalog re-seeding is required;
existing entries deserialize cleanly with `experimental:` absent.

The change crosses the v1.1→v1.2 boundary (rather than an additive v1.1
amendment) for two reasons:
1. The block is **validator-recognized with typed keys**, not free-form.
   Adding typed keys to a previously-free-form section is a semantic
   change to validation behavior, not a forward-compatible field add.
2. The `RULE-EXPERIMENTAL-FORWARD-COMPAT` shim establishes a precedent
   for future experimental keys (Windows backend, vendor SDKs) that
   v1.1's "field add only" pattern cannot accommodate.

---

## §SCHEMA-EXPERIMENTAL — `experimental:` block

### Schema change

Add optional top-level `experimental:` block to per-board profile YAML:

```yaml
experimental:
  ilo4_unlocked: true              # Board has user-flashed patched iLO4 firmware
  amd_overdrive: true              # Board's AMD GPU eligible for ppfeaturemask override
  nvidia_coolbits: true            # Board's NVIDIA GPU eligible for Coolbits/X11 path
  idrac9_legacy_raw: true          # iDRAC9 firmware version < 3.34
```

The block itself is optional. Entries without an `experimental:` block
default to "no experimental features eligible." Within the block, each
key is independently optional and defaults to `false`.

### Validator-recognized keys

v1.2 ships with these four keys recognized by the validator:

| Key | Type | Targets | Set on |
|---|---|---|---|
| `ilo4_unlocked` | bool | HPE ProLiant Gen8/Gen9 boards | board profile |
| `amd_overdrive` | bool | AMD discrete GPU SKUs | GPU vendor entry |
| `nvidia_coolbits` | bool | NVIDIA GPU SKUs | GPU vendor entry |
| `idrac9_legacy_raw` | bool | Dell PE 14G boards | board profile |

**Board-keyed vs GPU-keyed distinction:** `ilo4_unlocked` and
`idrac9_legacy_raw` describe whole-system properties (BMC firmware
state) and live on board profile entries. `amd_overdrive` and
`nvidia_coolbits` describe per-device properties (GPU SKU and driver
behavior) and live on GPU vendor catalog entries. The matcher merges
eligibility at runtime: a feature is eligible if it appears with `true`
on either the matched board profile **or** the matched GPU entry.

### Validator behavior

- **Recognized key with bool value:** accepted normally.
- **Recognized key with non-bool value:** validator rejects with
  `"experimental.<key>: expected bool, got <type>"`.
- **Typo of recognized key** (e.g. `ilo4_unlock`, `amd_overdrive_bit`):
  validator rejects with `"experimental.<key>: unknown key. Did you
  mean: <closest_match>?"`. Levenshtein distance ≤ 2 triggers the
  suggestion; greater distances trigger the forward-compat shim
  instead (see below).
- **Unknown key, distance > 2 from any recognized key:** forward-compat
  shim — validator emits a one-shot WARN (`"experimental.<key>:
  unknown key, ignored. Upgrade ventd if this is from a newer
  catalog."`) and continues. The key is parsed but ignored at runtime.

This Levenshtein-vs-forward-compat split prevents typos from being
treated as future keys (catching bugs) while still allowing v1.3
catalogs with new experimental features to load on v1.2 ventd
(forward compat).

### Source for each key's eligibility check

Eligibility is asserted by the catalog entry. ventd does not
re-validate at runtime that the board *actually has* the unlock
condition (e.g. patched iLO4, kernel cmdline bit set) — that's the
runtime precondition layer, separate from catalog eligibility per
spec-15 §3.

### Migration

- Existing v1.1 entries deserialize cleanly into v1.2 with
  `experimental:` absent → all keys default `false`.
- No catalog re-seeding required at v1.2 ship time. Per-feature
  implementation PRs (spec-15a/b/c/d) add eligibility markings to the
  relevant board/GPU entries as each feature lands.
- v1.0 → v1.1 migration path is unchanged.

### Bound rules

- `RULE-EXPERIMENTAL-SCHEMA-01`: validator accepts profile with
  `experimental:` block containing only recognized keys.
- `RULE-EXPERIMENTAL-SCHEMA-02`: validator rejects profile with
  recognized key set to non-bool value.
- `RULE-EXPERIMENTAL-SCHEMA-03`: validator rejects profile with
  experimental-key typo (Levenshtein distance ≤ 2 from a recognized
  key) and surfaces the suggestion.
- `RULE-EXPERIMENTAL-SCHEMA-04`: validator accepts profile with
  unknown experimental key (distance > 2) and emits one-shot WARN per
  unknown key per ventd lifetime.
- `RULE-EXPERIMENTAL-SCHEMA-05`: profile without `experimental:` block
  deserializes as v1.1 did (no behavior change, no log noise).
- `RULE-EXPERIMENTAL-MERGE-01`: matcher OR-merges eligibility from
  matched board profile AND matched GPU vendor entry; a feature is
  eligible if either source asserts `true`.

---

## Combined migration plan

| Step | Action | When |
|---|---|---|
| 1 | Land schema v1.2 validator with `experimental:` block | spec-03 amendment PR (this doc → CC prompt) |
| 2 | Land matcher OR-merge for board+GPU eligibility sources | Same PR, ride-along |
| 3 | Re-emit `manifest_version: 1.2` on all catalog files | Same PR, mechanical |
| 4 | spec-15 framework PR consumes the block via runtime API | spec-15 framework PR |
| 5 | spec-15b amd_overdrive PR adds `experimental.amd_overdrive: true` to RDNA2/3/4 GPU entries | spec-15b PR |
| 6 | spec-15c nvidia_coolbits PR adds key to relevant NVIDIA SKUs | spec-15c PR |
| 7 | spec-15a ilo4_unlocked PR adds key to HPE Gen8/Gen9 board entries | v0.7.0 |
| 8 | spec-15d idrac9_legacy_raw PR adds key to Dell PE 14G entries | v0.7.0 |

Steps 1-3 are this amendment. Step 4 ships in the same release. Steps
5-8 spread across v0.6.0 and v0.7.0 per spec-15 §9.

---

## Test plan

Per .claude/rules invariant binding:

| Rule | Subtest |
|---|---|
| RULE-EXPERIMENTAL-SCHEMA-01 | TestSchemaValidator_ExperimentalBlock_AcceptsRecognizedKeys |
| RULE-EXPERIMENTAL-SCHEMA-02 | TestSchemaValidator_ExperimentalBlock_RejectsNonBoolValue |
| RULE-EXPERIMENTAL-SCHEMA-03 | TestSchemaValidator_ExperimentalBlock_RejectsTypoWithSuggestion |
| RULE-EXPERIMENTAL-SCHEMA-04 | TestSchemaValidator_ExperimentalBlock_WarnsUnknownKeyOnce |
| RULE-EXPERIMENTAL-SCHEMA-05 | TestSchemaValidator_ExperimentalBlockAbsent_BehavesAsV1_1 |
| RULE-EXPERIMENTAL-MERGE-01 | TestMatcher_ExperimentalEligibility_OrsBoardAndGPU |

Test fixtures:
- `testdata/profiles/experimental_valid.yaml` — block with all four
  recognized keys at `true`.
- `testdata/profiles/experimental_typo.yaml` — `ilo4_unlock` (typo of
  `ilo4_unlocked`).
- `testdata/profiles/experimental_unknown.yaml` — `intel_battlemage_pwm`
  (forward-compat case).
- `testdata/profiles/experimental_nonbool.yaml` — `amd_overdrive: "yes"`
  (string instead of bool).
- `testdata/profiles/experimental_absent.yaml` — no block (regression
  test for v1.1 behavior).
- Synthesized GPU vendor entry fixture for OR-merge test (board says
  no, GPU says yes → eligible).

---

## Out of scope for this amendment

- spec-15 framework runtime behavior (CLI flags, doctor section, wizard
  page) — that's spec-15 §3-§4 and lives in the framework PR.
- Per-feature implementations (iLO4 SSH client, AMD sysfs writes,
  Coolbits shell-out, iDRAC raw IPMI) — spec-15a/b/c/d.
- Schema versioning beyond v1.2 (no v1.3 work scoped here; the
  forward-compat shim is the v1.3+ accommodation).
- Telemetry on experimental block usage — spec-15 §10 explicitly
  rejects telemetry; no schema field tracks it.

---

## Estimated CC implementation cost

| Component | Tokens (Sonnet) |
|---|---|
| Schema v1.2 type + validator extension | $2-3 |
| Levenshtein typo-detection helper | $1-2 |
| Forward-compat one-shot WARN logic | $1 |
| Matcher OR-merge for board+GPU eligibility | $1-2 |
| `manifest_version: 1.2` bump on catalog files | $0.50 |
| 6 RULE-* tests + fixtures | $2-3 |
| **Total** | **$7.50-11.50** |

Below Phoenix's per-spec $10-30 envelope. Mechanical change with no
hardware verification path required (pure schema/matcher work).

---

## References

- `specs/spec-15-experimental-features.md` — framework spec that
  motivates this schema bump.
- `docs/research/2026-04-firmware-locked-fan-control.md` — research
  establishing the four community-known unlock paths.
- `specs/spec-03-amendment-schema-v1.1.md` — predecessor amendment
  with the field-add pattern this amendment extends.
- Levenshtein distance reference (DamerauLevenshtein for transposition
  catching, e.g. `ilo4_ulnocked` → `ilo4_unlocked`):
  https://en.wikipedia.org/wiki/Damerau%E2%80%93Levenshtein_distance
