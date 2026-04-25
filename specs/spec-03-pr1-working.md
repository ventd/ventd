# spec-03 PR 1 — Working document

**Status:** PR-1 scope frozen 2026-04-25. This document supersedes the
PR-1 sections of `specs/spec-03-profile-library.md` +
`specs/spec-03-amendment-predictive.md` for the duration of PR 1
implementation. After PR 1 merges, the originals stand as written for
PR 2/3/4 scope.

**Why this document exists:** the base spec, the predictive amendment,
and the v0.4.1 hwmon-research §17.22 stall-speed finding all contribute
PR-1-shaped requirements that don't read cleanly when interleaved.
This is the single source of truth for the PR.

---

## 1. Goal

Freeze the v1 schema for ventd's hardware profile library and ship the
static contract: Go types, YAML parsing with strict validation,
fingerprint hash with frozen input tuple, migration chain skeleton, the
nine invariant rules bound 1:1 to subtests, and the documented on-disk
layout that Phase 5+ specs (predictive thermal, calibration capture)
extend.

**No matcher logic.** No capture pipeline. No new seed entries beyond
re-formatting whatever already exists in `internal/hwdb/profiles.yaml`
to schema v1.

## 2. Files

### 2.1 New

- `internal/hwdb/schema.go` — Go types for YAML parsing, strict
  validation entry point
- `internal/hwdb/schema_test.go` — `TestSchema_Invariants` parent test
  with one subtest per RULE-HWDB-NN
- `internal/hwdb/migrate.go` — migration chain skeleton (zero
  migrations registered in v1; the *shape* is what spec-05 extends)
- `internal/hwdb/migrate_test.go` — chain-consistency test bound to
  RULE-HWDB-07
- `internal/hwdb/testdata/valid_minimal.yaml` — minimum-viable entry
- `internal/hwdb/testdata/valid_full.yaml` — every optional field
  populated
- `internal/hwdb/testdata/invalid_*.yaml` — one fixture per failing
  rule (9 files)
- `.claude/rules/hwdb-schema.md` — RULE-HWDB-01..09 with `Bound:` lines
- `docs/hwdb-schema.md` — human-readable schema reference + storage
  layout + frozen fingerprint tuple

### 2.2 Modified

- `internal/hwdb/fingerprint.go` — extend (or rewrite, see §3) to use
  the frozen v1 tuple
- `internal/hwdb/fingerprint_test.go` — golden tests for hash stability
  under each input field's variation
- `internal/hwdb/profiles.yaml` — migrate existing entries to schema v1
- `internal/hwdb/embed.go` (or wherever the existing `embed.FS` lives)
  — no change expected, but if the file does not exist yet, create it
  here
- `CHANGELOG.md` — `[Unreleased]` `### Added` entry

### 2.3 Existing files to read first (do not modify)

- `.claude/rules/hwmon-safety.md` — rule file format reference
- `.claude/rules/install-contract.md` — rule file format reference
  (recently shipped, freshest example)
- `tools/rulelint/main.go` (or wherever rulelint lives) — confirms the
  `Bound:` line parser shape so new rules don't trip the linter
- `internal/hwdb/*.go` — ALL existing files in the package, including
  current `fingerprint.go`, current `profiles.yaml`, and any matcher
  scaffolding from P1-FP-01

## 3. Existing-state probe (first action)

Before writing any code, the implementing session **must** report what
exists in `internal/hwdb/`:

```bash
ls -la internal/hwdb/
cat internal/hwdb/*.go | head -300
cat internal/hwdb/profiles.yaml
```

Two outcomes are possible:

**Outcome A — fingerprint.go exists with a different tuple.** Existing
entries' fingerprints will differ from the frozen v1 tuple. Strategy:
keep the function name and signature stable (don't break callers),
replace the tuple internally, regenerate the 3 entries' fingerprints
in the migrated `profiles.yaml`. Document the break in CHANGELOG under
`### Changed`.

**Outcome B — fingerprint.go missing or trivially-different tuple.**
Implement from scratch using §6.

If the existing file uses a tuple that is a *subset* of the v1 tuple
(e.g., only board vendor + name), this is still a break — the hashes
will not match. Treat as Outcome A.

**Do not silently widen the tuple and skip the regeneration.** That
ships broken matching at first install.

## 4. Schema v1 — full field reference

```yaml
- id: "msi-meg-x570-unify"           # REQUIRED. kebab-case. RULE-HWDB-01.
                                     # Stable forever. Used as primary key.
  schema_version: 1                  # REQUIRED. Integer. RULE-HWDB-03.

  fingerprint:                       # REQUIRED. RULE-HWDB-01.
    dmi_sys_vendor: "Micro-Star..."  # OPTIONAL but recommended
    dmi_product_name: "MS-7C35"      # OPTIONAL
    dmi_board_vendor: "Micro-Star..."
    dmi_board_name: "MEG X570 UNIFY"
    dmi_board_version:               # OPTIONAL list. Empty = match any.
      - "1.0"
      - "2.0"
    family: "x570-unify"             # OPTIONAL fuzzy-match anchor
    superio_chip: "nct6798d"         # OPTIONAL kernel module name

  hardware:                          # REQUIRED. RULE-HWDB-01.
    fan_count: 6
    pwm_control: "nct6798d"          # REQUIRED. RULE-HWDB-05 allowlist.
    temp_sensors:
      - "k10temp"
      - "nct6798d"
      - "nvme"
    fans:                            # OPTIONAL list of per-fan metadata
      - id: 1                        # 1-indexed to match hwmon convention
        label: "CPU_FAN"
        stall_pwm_min: 60            # RULE-HWDB-09. PWM below which
                                     # the fan stops spinning. Required
                                     # if any curve referencing this fan
                                     # has allow_stop: true.
      - id: 2
        label: "CHA_FAN1"
        stall_pwm_min: 50
      - id: 3
        label: "CHA_FAN2"
        # No stall_pwm_min — allow_stop forbidden for any curve
        # mapping to this fan. RULE-HWDB-09.
    quirks:                          # OPTIONAL map<string,bool>
      nct6798d_pwm3_broken: true

  defaults:                          # OPTIONAL but typical
    cpu_sensor: "k10temp/Tctl"
    curves:                          # OPTIONAL list
      - role: "cpu"                  # arbitrary string
        fan_ids: [1]                 # which fans this curve drives
        allow_stop: false            # RULE-HWDB-09 cross-validates
        points:                      # RULE-HWDB-04 monotonic
          - [40, 30]
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

  predictive_hints:                  # OPTIONAL. RULE-HWDB-08.
                                     # Reserved for spec-05 consumers.
                                     # Documenting now, not consuming
                                     # yet, prevents v2 schema migration
                                     # when spec-05 lands.
    platform_heavy_threshold_watts: 80
    thermal_critical_c: 95
    thermal_safe_ceiling_c: 85

  sensor_trust:                      # OPTIONAL list
    - sensor: "nct6798d/temp4"
      trust: "untrusted"             # enum: trusted | untrusted | unknown
      reason: "stuck-at reading on this board rev"

  contributed_by: "anonymous"        # REQUIRED. RULE-HWDB-06 PII gate.
                                     # "anonymous" or a github handle.
                                     # NEVER a real name or email.
  captured_at: "2026-04-22"          # REQUIRED. ISO date.
  verified: true                     # REQUIRED. Boolean.
```

## 5. Strict YAML decoding

Use `gopkg.in/yaml.v3` (already in `go.mod`). The decoder MUST be
configured with `KnownFields(true)` so that any field not in the
struct definition produces a load error. This is the enforcement
mechanism for RULE-HWDB-06 (PII gate) — a contributor cannot smuggle
a `smbios_uuid:` field into a profile entry; the parser rejects it
before any code touches it.

```go
dec := yaml.NewDecoder(reader)
dec.KnownFields(true)
```

Top-level shape: a YAML document is `[]Profile` (a list). A profiles
file containing a top-level map (or any other shape) rejects.

## 6. Frozen fingerprint tuple (v1)

```
fingerprint_input = strings.Join([]string{
    canonicalise(dmi.sys_vendor),
    canonicalise(dmi.product_name),
    canonicalise(dmi.board_vendor),
    canonicalise(dmi.board_name),
    canonicalise(dmi.board_version),
    canonicalise(cpu.model_name),
    fmt.Sprintf("%d", cpu.core_count),
}, "|")

fingerprint = hex.EncodeToString(sha256.Sum256([]byte(fingerprint_input))[:8])
```

**`canonicalise` rules** (apply in order):

1. Trim leading/trailing whitespace.
2. Collapse runs of internal whitespace to a single space.
3. Convert to lowercase.
4. Replace empty string with the literal string `"<empty>"` so that an
   absent field still occupies its slot in the tuple (positional
   stability across BIOS revisions that drop `board_version`).

The hash output is a 16-character lowercase hex string (8 bytes from
SHA-256, hex-encoded). This becomes both the on-disk directory name
and any future external reference to the fingerprint — it is part of
the ABI from PR 1 forward.

**Source of DMI fields:** read `/sys/class/dmi/id/{sys_vendor,product_name,board_vendor,board_name,board_version}`
directly. No external dep needed; stdlib `os.ReadFile`.

**Source of CPU fields:** read `/proc/cpuinfo`, parse `model name`
(first occurrence) and count the `processor :` lines for core count.
No external dep needed.

**`Fingerprint()` function signature** (suggested):

```go
type DMI struct {
    SysVendor     string
    ProductName   string
    BoardVendor   string
    BoardName     string
    BoardVersion  string
    CPUModelName  string
    CPUCoreCount  int
}

// Fingerprint returns the v1 fingerprint hash.
// The function is pure: same input -> same output, no I/O.
func Fingerprint(dmi DMI) string

// ReadDMI reads the v1 input tuple from /sys and /proc.
// Pure I/O wrapper; tests inject a fake fs.FS via a sibling helper.
func ReadDMI(root fs.FS) (DMI, error)
```

Tests inject a fake `fs.FS` via `testing/fstest.MapFS` per CLAUDE.md
"No real /sys in unit tests."

## 7. Storage layout (documented, not exercised by PR 1)

PR 1 documents the layout in `docs/hwdb-schema.md` and reserves it.
PR 1 does **not** create any directories at runtime — the loader only
reads the embedded `profiles.yaml`. Creation is PR 4 capture work.

```
/var/lib/ventd/                            # system daemon mode
  fingerprint.json                         # current DMI fingerprint + ventd version
  platform/<dmi_fingerprint>/              # per-platform state
    profile.yaml                           # matched profile (PR 2 writes this)
    profile.yaml.bak                       # previous, for rollback
    # spec-05 will add: model.json, workloads.json, motifs.json, telemetry/
  profiles.yaml                            # canonical library (read-only, embedded)
  profiles-pending/                        # PR 4 capture writes here
    <fingerprint-hash>.yaml                # 0640 ventd:ventd

# user-mode fallback (`ventd --user`)
$XDG_STATE_HOME/ventd/                     # default ~/.local/state/ventd/
  <same layout, minus profiles.yaml which stays embedded>
```

The `<dmi_fingerprint>` token everywhere is the 16-char hex from §6.

## 8. Migration chain skeleton

```go
// internal/hwdb/migrate.go

// supportedVersions enumerates the schema versions this binary can load.
// Adding a version REQUIRES adding the matching migrate_N_to_N_plus_1
// function below — RULE-HWDB-07 enforces this at test time.
var supportedVersions = []int{1}

// currentVersion is the version this binary writes.
const currentVersion = 1

// migrators is the registry consulted at load time. Empty for v1.
// Adding entries: migrators[N] is the function that takes a v(N-1)
// document and returns a v(N) document.
var migrators = map[int]func([]byte) ([]byte, error){
    // 2: migrate_1_to_2,  // example: when spec-05 lands schema v2
}
```

The test in `migrate_test.go` walks `supportedVersions`, asserts that
for every `v > 1` there is a `migrators[v]` registered. This is
RULE-HWDB-07's binding.

## 9. Invariant rules — `.claude/rules/hwdb-schema.md`

Numbering: PR 1 ships 01..09. The base spec reserved 08..10 for PR 4
capture; PR 4 will ship those. To avoid number collision, the
predictive-hints amendment's RULE-HWDB-11/12 are **renumbered to 08/09
in PR 1**, freeing the original 08..10 range for the PR 4 capture
rules. The §17.22 stall-speed rule takes a unique number in PR 1's
range.

**Final PR 1 numbering (ship this):**

| ID | Scope | Source |
|---|---|---|
| RULE-HWDB-01 | Required top-level fields present | base spec |
| RULE-HWDB-02 | id uniqueness across file | base spec |
| RULE-HWDB-03 | schema_version known | base spec |
| RULE-HWDB-04 | curve points monotonic | base spec |
| RULE-HWDB-05 | pwm_control kernel-module allowlist | base spec |
| RULE-HWDB-06 | PII gate (KnownFields strict) | base spec |
| RULE-HWDB-07 | migration chain integrity | base spec |
| RULE-HWDB-08 | predictive_hints validation when present | amendment |
| RULE-HWDB-09 | stall_pwm_min required when allow_stop=true | hwmon §17.22 |

PR 4 will add 10..12 (capture pipeline) without colliding.

**Rule file format reminder** (from `ventdtestmasterplan.md §2`):

```
# <Scope> Invariants

Each rule below is bound 1:1 to a subtest in <file>:<function>.
Editing a rule requires editing the corresponding subtest in the
same PR. Adding a rule requires landing the subtest in the same PR.

## RULE-<ID>: <one-line invariant>
<paragraph explaining the invariant, why it holds, what fails if it doesn't>
Bound: <file>:<subtest_name>
```

**Subtest naming convention:** the parent is `TestSchema_Invariants`,
each rule's subtest is `TestSchema_Invariants/Rule_HWDB_NN_<short>`.

The Bound line is exactly:
```
Bound: internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_NN_<short>
```

For RULE-HWDB-07, the bound subtest lives in `migrate_test.go` not
`schema_test.go` — the chain check is its own concern. Rulelint
accepts any path; the convention is just per-package siblings of the
code under test.

### 9.1 Rule wording (verbatim — paste into the rule file)

PR 1 ships these exact paragraphs. CC may improve grammar but must
not change the *substance* without surfacing.

#### RULE-HWDB-01 — Required top-level fields

Every entry in `profiles.yaml` MUST have `id`, `schema_version`,
`fingerprint`, `hardware`, `contributed_by`, `captured_at`, and
`verified`. The `fingerprint` block MUST contain at least one of
`dmi_board_vendor`, `dmi_board_name`, `dmi_product_name`, or
`superio_chip` — an entry with no matchable fingerprint anchor is a
schema error, not just a warning. The parser rejects at load and
names the failing field.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_01_RequiredFields`

#### RULE-HWDB-02 — Unique IDs

The `id` field is the primary key. Duplicate `id` values within a
single `profiles.yaml` reject at load. The parser names both the
duplicate id and the second-occurrence position.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_02_UniqueIDs`

#### RULE-HWDB-03 — Known schema_version

The `schema_version` field MUST appear in the parser's
`supportedVersions` table. An unknown version (higher OR lower)
rejects at load with a human-readable migration hint that names the
known versions.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_03_KnownSchemaVersion`

#### RULE-HWDB-04 — Monotonic curves

Every curve's `points` list is monotonic non-decreasing in both the
temperature axis and the PWM axis. A curve that decreases on either
axis at any segment rejects at load. The parser names the offending
profile id, curve role, and segment index.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_04_MonotonicCurves`

#### RULE-HWDB-05 — pwm_control kernel-module allowlist

The `hardware.pwm_control` value MUST be one of the names in the
allowlist constant `KnownPWMModules` defined in `schema.go`. Unknown
values reject at load. The allowlist v1 is:

```
nct6775, nct6779, nct6791, nct6792, nct6793, nct6795, nct6796,
nct6797, nct6798, nct6798d, nct6799, it87, it8728, it8772, it8728f,
it8732e, it8771e, it8772e, f71808e, f71869, f71869a, f71889ad,
f71889ed, f71889fg, w83627dhg, w83627ehf, w83627uhg, w83795g,
w83795adg, asus-ec-sensors, asus-wmi-sensors, dell-smm-hwmon,
hp-wmi-sensors, thinkpad_acpi, applesmc, surface_fan, gigabyte-waterforce,
asus-rog-ryujin, corsair-cpro, corsair-psu, nzxt-kraken2, nzxt-kraken3,
nzxt-smart2, aquacomputer-d5next, drivetemp, k10temp, coretemp, amdgpu,
peci-cputemp, sch5627, sch5636, f71882fg, fam15h_power, lm75, lm85,
adt7475, adt7476, max6645, max31790, emc2103, nct7802, pwm-fan
```

Adding to this list is a v1.x amendment, not a schema break.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_05_KnownPWMModule`

#### RULE-HWDB-06 — PII gate

The YAML decoder is configured with `KnownFields(true)`. Any field
in the input that is not in the schema struct definition causes a
load error. This is the mechanical enforcement of the PII gate:
fields like `smbios_uuid`, `chassis_serial`, `mac_address`,
`hostname` are not in the struct, so a contributor cannot smuggle
them into a profile entry — the parser rejects before any code
touches the value.

Additionally, the `contributed_by` field's value is constrained to
either the literal string `"anonymous"` or a string matching the
GitHub handle pattern `^[a-zA-Z0-9-]{1,39}$`. Real names, emails,
and free-form strings reject.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_06_PIIGate`

#### RULE-HWDB-07 — Migration chain integrity

For every `v > 1` in `supportedVersions`, there MUST be a registered
function `migrators[v]` that takes a v(N-1) document and returns a
v(N) document. The test walks the table and fails if any expected
migrator is missing. This is the contract that makes schema bumps
mechanical: you cannot bump the version without writing the
migration.

Bound: `internal/hwdb/migrate_test.go:TestMigrate_ChainIntegrity`

#### RULE-HWDB-08 — predictive_hints validation

If the optional `predictive_hints` block is present, then:
- `platform_heavy_threshold_watts` is an integer > 0.
- `thermal_critical_c` is an integer.
- `thermal_safe_ceiling_c` is an integer.
- `thermal_critical_c > thermal_safe_ceiling_c + 5`.

Non-compliant entries reject at load. The parser names the offending
profile id and the failing constraint.

The block is optional in v1; spec-05 will consume it. Documenting
the field in v1 prevents a v2 schema migration when spec-05 lands.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_08_PredictiveHints`

#### RULE-HWDB-09 — stall_pwm_min required when allow_stop=true

A curve with `allow_stop: true` is a curve that can drive PWM to 0
under the right thermal conditions. This is only safe if every fan
in the curve's `fan_ids` list has a known `stall_pwm_min` — without
it, ventd has no information about whether driving PWM to 0 will
leave the fan stopped (acceptable) or stalling (mechanical wear).

Validation: for every curve with `allow_stop: true`, every `fan_id`
in `fan_ids` MUST resolve to an entry in `hardware.fans` that has
`stall_pwm_min` set. Unset = reject. The parser names the offending
profile id, curve role, and fan id.

Background: hwmon-research.md §17.22 — the `pwm_enable=1, pwm=0`
behaviour is chip-specific. On nct6775 it actually stops the fan;
on others it falls back to a chip-defined minimum. ventd must not
trust `allow_stop` blindly; it must have a board-specific anchor.

Bound: `internal/hwdb/schema_test.go:TestSchema_Invariants/Rule_HWDB_09_StallPWMMinRequired`

## 10. CHANGELOG entry

Under `## [Unreleased]`, `### Added`:

```
- Hardware profile library schema v1 (`internal/hwdb/schema.go`):
  per-board YAML descriptors with strict-decode PII gate, monotonic
  curve validation, kernel-module allowlist, and reserved
  predictive-thermal hints for spec-05 consumers (spec-03 PR 1).
- DMI fingerprint hash function with frozen v1 input tuple
  (sys_vendor, product_name, board_vendor, board_name, board_version,
  cpu_model_name, cpu_core_count). 16-char hex SHA-256 prefix.
- Documented per-platform state directory layout under
  `/var/lib/ventd/platform/<fingerprint>/` and the user-mode fallback
  at `$XDG_STATE_HOME/ventd/`. Reserved layout for capture (PR 4)
  and predictive (spec-05) writes.
- `.claude/rules/hwdb-schema.md` with invariants RULE-HWDB-01..09 each
  bound 1:1 to a subtest under `TestSchema_Invariants` or
  `TestMigrate_ChainIntegrity`.
```

If Outcome A from §3 fired (fingerprint tuple changed), also add under
`### Changed`:

```
- DMI fingerprint hash now uses the frozen v1 input tuple. Existing
  profile entries in `internal/hwdb/profiles.yaml` were regenerated
  to match. No on-disk migration is needed because PR 1 does not yet
  write per-platform state — the new tuple becomes authoritative
  with the v0.5.0 release.
```

## 11. Definition of done

- [ ] `go test -race ./internal/hwdb/...` passes.
- [ ] `go test -race -run TestSchema_Invariants ./internal/hwdb/`
      shows nine subtests, one per RULE-HWDB-NN.
- [ ] `go test -race -run TestMigrate_ChainIntegrity ./internal/hwdb/`
      passes.
- [ ] `go run ./tools/rulelint` shows zero orphans, zero missing
      bindings. (`.claude/rules/hwdb-schema.md` resolves cleanly.)
- [ ] `golangci-lint run ./internal/hwdb/...` passes.
- [ ] `goleak` integration in test main confirms zero goroutine leaks
      (the package already uses goleak; new tests must remain clean).
- [ ] `internal/hwdb/profiles.yaml` parses cleanly under the new schema.
- [ ] `docs/hwdb-schema.md` exists with: v1 schema reference, frozen
      fingerprint tuple definition, storage layout, and a link
      `See specs/spec-03-profile-library.md` for the full design.
- [ ] CHANGELOG `[Unreleased]` entry exists under `### Added`
      (and `### Changed` if Outcome A).
- [ ] Branch protection green: every required CI check passes before
      merge.
- [ ] Conventional commits at boundaries (suggested):
  - `feat(hwdb): freeze v1 schema with strict-decode PII gate`
  - `feat(hwdb): frozen v1 fingerprint tuple (sha256/16-hex)`
  - `feat(hwdb): migration chain skeleton`
  - `test(hwdb): nine invariant subtests bound to RULE-HWDB-01..09`
  - `docs(rules): add hwdb-schema.md with bound invariants`
  - `docs(hwdb): schema reference and per-platform layout`
  - `chore(hwdb): migrate profiles.yaml to schema v1`

## 12. Explicit non-goals

- No matcher logic. Three-tier matcher is PR 2.
- No capture pipeline. Anonymisation, fuzz target, file-mode writes are PR 4.
- No new seed entries beyond reformatting the existing ones. Bulk seeding is PR 3.
- No Opus consult. Run that between PR 1 and PR 3 — cheaper to migrate
  3 entries than 25 if the consult surfaces a schema concern.
- No §17.18 (uncontrollable-channel detection) — that's calibration-side, gh issue.
- No §17.19 (user-overridable labels) — that's UI/config, gh issue.
- No `internal/hwdb/match.go` changes (PR 2 owns that file). Existing
  matcher code, if any, stays as-is on `main` until PR 2.

## 13. Open issues to file (separate from PR 1)

PR 1 commit body should mention that two follow-up issues exist:

- "Detect uncontrollable hwmon channels at calibration time
  (hwmon-research.md §17.18)" — `area/calibration`, `priority/p2`,
  `spec-03`
- "User-overridable fan labels in profile schema or config
  (hwmon-research.md §17.19)" — `area/profile`, `priority/p2`,
  `spec-03`

Phoenix files these issues; CC drafts text in the PR description but
does not run `gh issue create`. (Per `.claude/rules/collaboration.md`:
issue creation is a Phoenix-only action.)

## 14. Token-cost expectation

Sonnet, single CC session. Most of the work is mechanical
transcription of this document into Go + YAML + Markdown:
- ~250 LOC `schema.go` (types + strict decoder + validators)
- ~400 LOC `schema_test.go` (nine subtests + table-driven cases)
- ~80 LOC `fingerprint.go` (canonicalise + Fingerprint + ReadDMI)
- ~120 LOC `fingerprint_test.go` (golden + each-field-varies)
- ~60 LOC `migrate.go` (skeleton + registry)
- ~40 LOC `migrate_test.go` (chain integrity)
- ~150 lines `.claude/rules/hwdb-schema.md` (paste from §9.1)
- ~200 lines `docs/hwdb-schema.md`
- 9 fixture YAML files, ~10-30 lines each
- `profiles.yaml` migration (small)

Estimate $15–30. No hard cap; quality over cost per Phoenix's stated
preference for this PR.
