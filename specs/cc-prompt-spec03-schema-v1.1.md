# cc-prompt-spec03-pr4.md

**For:** Claude Code, **Sonnet** model. Do NOT use Opus inside CC.
**Estimated cost:** $14–22 (per spec-03-amendment-schema-v1.1.md §"Estimated CC implementation cost").
**Estimated duration:** 25–40 minutes.
**Prerequisite:**
- spec-03 PR 1 merged (#629) — schema v1, fingerprint tuple, RULE-HWDB-01..09.
- spec-03 PR 2a merged — three-tier matcher, RULE-HWDB-PR2-01..14.
- spec-03 PR 2c merged (#639 + ef42159 fix-up) — diagnostic bundle.
- spec-03 PR 3 (board catalog seed) merged or in-flight — does NOT block PR 4.
- `specs/spec-03-amendment-schema-v1.1.md` committed on main (paste from `/mnt/project/spec-03-amendment-schema-v1.1.md` — the artifact delivered in scope-C chat).

**Branch:** `spec-03/pr-4-schema-v1.1`

---

## Context

scope-C catalog research surfaced three independent gaps requiring schema-level changes. spec-03 amendment v1.1 (frozen on main as `specs/spec-03-amendment-schema-v1.1.md`) bundles them into one additive, backward-compatible bump:

1. **§SCHEMA-BIOSVER:** Optional `bios_version` field on `dmi_fingerprint`. Glob-supported. Required for Lenovo Legion gaming laptop dispatch (boards share `product_name` codes across generations; only BIOS_VERSION 4-char prefix disambiguates).
2. **§SCHEMA-DT:** New `dt_fingerprint` block as alternative to `dmi_fingerprint`. Mutual exclusion enforced. Required for ARM/SBC systems with no DMI (Pi 4B, Pi 5 already in catalog with synth-hack, CM4-on-CM4IO, post-v1.0 Jetson/Pine64).
3. **§HP-CONSUMER:** `overrides.unsupported: true` formal semantics — calibration skips autocurve, INFO log emitted once, web UI shows "read-only mode" banner. Validator recognizes the field (typos fail).

PR 4 implements all three changes plus the Pi 5 entry re-emit (drop synth hack, use dt_fingerprint).

PR 5 (chained, separate session) seeds the actual board YAMLs that depend on these schema features (Legion + IPMI + Pi additions).

## Files to create or modify

**Create:**
- `internal/hwdb/profile_v1_1.go` — additive schema struct extensions (BoardFingerprint extension, new DTFingerprint struct, Overrides.Unsupported field).
- `internal/hwdb/migrate_v1_to_v1_1.go` — null migration (v1 entries deserialize cleanly into v1.1; no field rewrites). Registered in `migrate.go` chain.
- `internal/hwdb/dt_fingerprint.go` — `/proc/device-tree/compatible` + `/proc/device-tree/model` reader, glob match logic, mutual-exclusion validator.
- `internal/hwdb/dtfake/dtfake.go` — test helper mirroring `dmifake` pattern. Synthesizes a fake `/proc/device-tree/` directory tree under a test tmpdir with `compatible` (null-separated string list) and `model` files.
- `internal/hwdb/profile_v1_1_test.go` — 7 RULE-HWDB-PR4-* subtests bound 1:1 to invariants below.
- `internal/calibration/skip_unsupported.go` — calibration phase early-exit when `EffectiveControllerProfile.Overrides.Unsupported == true`. One-shot INFO log via `sync.Once` keyed on board id.
- `internal/calibration/skip_unsupported_test.go` — RULE-OVERRIDE-UNSUPPORTED-01 + RULE-OVERRIDE-UNSUPPORTED-02 subtests.
- `.claude/rules/hwdb-pr4-{01..07}.md` — 7 rule files with `<!-- rulelint:allow-orphan -->` markers initially. Strip markers as bindings resolve.
- `.claude/rules/override-unsupported-{01..02}.md` — 2 rule files for calibration-skip behavior.

**Modify:**
- `internal/hwdb/profile_v1.go` — bump `SchemaVersion` constant from `"1.0"` to `"1.1"`. Update `supported_versions` list to include both `"1.0"` and `"1.1"`. Existing v1.0 fixtures still parse.
- `internal/hwdb/matcher_v1.go` — extend tier-1 matcher: when `dmi_fingerprint.bios_version` is non-empty, glob-match against `/sys/class/dmi/id/bios_version`. Tier-1 also tries `dt_fingerprint` when DMI is absent (sys_vendor empty/missing).
- `internal/hwdb/effective_profile.go` — propagate `Overrides.Unsupported` through 4-layer inheritance (board overrides chip overrides driver — same precedence as existing override fields).
- `internal/hwdb/catalog/boards/raspberry-pi.yaml` — re-emit Pi 5 entry with `dt_fingerprint`, drop `synthesize_fingerprint_from_dt`, drop `no_dmi`, drop synthesized `dmi_fingerprint` block. **Backward compat:** keep `id: "raspberry-pi-5-model-b"` so any existing tests / per-platform state directories don't break.
- `migrate.go` — register new migrate function in chain.
- `docs/hwdb-schema.md` — add §"Schema v1.1 changes" section: bios_version, dt_fingerprint, unsupported override, with one example each.
- `CHANGELOG.md` — `[Unreleased]` `### Added` and `### Changed` entries (template below).

**Do not touch:**
- `cmd/ventd/*` — schema bump is data-layer only; CLI wiring unchanged.
- `internal/diag/*` — diagnostic bundle covers schema versioning at runtime; spec-03 PR 2c already handles forward-compat.
- Web UI / `cmd/ventd-ui/*` — "read-only mode" banner is a separate UI follow-up (spec-12 territory). PR 4 emits the marker; UI consumes it later.
- Existing PR 1 / PR 2 / PR 3 RULE-HWDB-* rules and tests — must continue to pass unchanged.

---

## Invariants to bind (9 new)

Statements copied verbatim from `specs/spec-03-amendment-schema-v1.1.md`. Each rule file template:

```markdown
# RULE-HWDB-PR4-NN

<!-- rulelint:allow-orphan -->

Bound: internal/hwdb/profile_v1_1_test.go::TestRuleHwdbPR4NN

<rule statement>
```

Strip the `<!-- rulelint:allow-orphan -->` marker for each rule as the corresponding subtest is implemented and passing. **Final commit must have zero allow-orphan markers in this PR's new rules.**

| Rule | Subtest | Statement |
|---|---|---|
| RULE-HWDB-PR4-01 | TestRuleHwdbPR4_01_BiosVersionGlobMatches | When `dmi_fingerprint.bios_version` is set and non-empty, the matcher returns true for board entries whose `bios_version` glob matches the live DMI `bios_version` string. |
| RULE-HWDB-PR4-02 | TestRuleHwdbPR4_02_BiosVersionAbsentBehavesAsV1 | A profile entry without `bios_version` (or with empty/`*` value) matches identically to schema v1.0 — no behavior change. |
| RULE-HWDB-PR4-03 | TestRuleHwdbPR4_03_DTCompatibleGlobMatches | When `/sys/class/dmi/id/sys_vendor` is empty or missing AND `dt_fingerprint.compatible` is set, matcher returns true if any entry in `/proc/device-tree/compatible` glob-matches. |
| RULE-HWDB-PR4-04 | TestRuleHwdbPR4_04_DTModelGlobMatches | When `/sys/class/dmi/id/sys_vendor` is empty or missing AND `dt_fingerprint.model` is set, matcher returns true if `/proc/device-tree/model` glob-matches. |
| RULE-HWDB-PR4-05 | TestRuleHwdbPR4_05_RejectsBothFingerprintTypes | Schema validator rejects (with named-profile-id error) any profile that sets both `dmi_fingerprint` and `dt_fingerprint`. |
| RULE-HWDB-PR4-06 | TestRuleHwdbPR4_06_DMIPrecedesOverDT | When DMI is present (sys_vendor non-empty), `dt_fingerprint` profiles are NEVER considered, even if `/proc/device-tree/` exists and would match. |
| RULE-HWDB-PR4-07 | TestRuleHwdbPR4_07_MigrationV1ToV11Null | A schema v1.0 profile migrated to v1.1 deserializes byte-identical to a fresh v1.1 profile with the same fields and absent v1.1-specific blocks. |
| RULE-OVERRIDE-UNSUPPORTED-01 | TestRuleOverrideUnsupported_01_LogOnce | When `EffectiveControllerProfile.Overrides.Unsupported == true`, the matcher emits the INFO log message exactly once per (board id, ventd lifetime) combination. |
| RULE-OVERRIDE-UNSUPPORTED-02 | TestRuleOverrideUnsupported_02_CalibrationSkipsAutocurve | When `EffectiveControllerProfile.Overrides.Unsupported == true`, the calibration phase skips autocurve generation. Sensor reads still execute normally; no autocurve YAML is written. |

Test fixtures: `dtfake` helper synthesizes `/proc/device-tree/` under tmpdir; `dmifake` (existing) provides DMI input. Both helpers must be importable from the test package without root.

---

## Schema v1.1 design summary

Read `specs/spec-03-amendment-schema-v1.1.md` for the full design. Key implementation points:

### bios_version field

```go
// In profile_v1_1.go — extends existing DMIFingerprint struct
type DMIFingerprint struct {
    SysVendor    string `yaml:"sys_vendor" json:"sys_vendor"`
    ProductName  string `yaml:"product_name" json:"product_name"`
    BoardVendor  string `yaml:"board_vendor" json:"board_vendor"`
    BoardName    string `yaml:"board_name" json:"board_name"`
    BoardVersion string `yaml:"board_version" json:"board_version"`
    BiosVersion  string `yaml:"bios_version,omitempty" json:"bios_version,omitempty"` // NEW v1.1
}
```

`omitempty` ensures v1.0 profiles serialize without the field. Glob match: empty string OR `"*"` matches anything (existing tier-1 logic). Reuses existing glob helper — no new match-engine code.

### dt_fingerprint block

```go
// New struct in dt_fingerprint.go
type DTFingerprint struct {
    Compatible string `yaml:"compatible,omitempty" json:"compatible,omitempty"`
    Model      string `yaml:"model,omitempty" json:"model,omitempty"`
}

// Profile struct extension (in profile_v1_1.go)
type Profile struct {
    // ...existing v1.0 fields
    DMIFingerprint *DMIFingerprint `yaml:"dmi_fingerprint,omitempty" json:"dmi_fingerprint,omitempty"`
    DTFingerprint  *DTFingerprint  `yaml:"dt_fingerprint,omitempty" json:"dt_fingerprint,omitempty"` // NEW v1.1
}
```

Validator: exactly one of `DMIFingerprint` or `DTFingerprint` must be non-nil. Both nil OR both non-nil = validation error with profile id named.

DT reader: parse `/proc/device-tree/compatible` as null-separated string list (`bytes.Split(data, []byte{0x00})`). `/proc/device-tree/model` is a single null-terminated string. Use `strings.TrimRight(s, "\x00")` defensively.

### unsupported override

Already a no-op tolerated field in v1.0 `Overrides` map. v1.1 adds it as a typed struct field:

```go
type Overrides struct {
    // ...existing fields
    Unsupported bool `yaml:"unsupported,omitempty" json:"unsupported,omitempty"` // NEW v1.1
    // ...remaining fields
}
```

Strict-decode now catches typos like `unsuported` (will fail to parse with `KnownFields(true)`).

Calibration phase: early-exit before autocurve loop. INFO log via `sync.Once` keyed on board id (use `map[string]*sync.Once` if Go 1.25 generics-of-Once aren't enabled; otherwise sync.Once-per-id pattern is fine).

### Pi 5 entry re-emit

Current `internal/hwdb/catalog/boards/raspberry-pi.yaml` Pi 5 entry uses synthesized DMI:

```yaml
dmi_fingerprint:
  sys_vendor: "Raspberry Pi Foundation"
  product_name: "Raspberry Pi 5 Model B"
overrides:
  arm_device_tree: true
  cooling_device_must_detach: true
  no_dmi: true
  synthesize_fingerprint_from_dt: true
```

Replace with:

```yaml
dt_fingerprint:
  compatible: "raspberrypi,5-model-b"
  model: "Raspberry Pi 5 Model B*"
overrides:
  arm_device_tree: true
  cooling_device_must_detach: true
  # no_dmi DROPPED — implicit when dt_fingerprint is used
  # synthesize_fingerprint_from_dt DROPPED — feature redundant under v1.1
```

Keep `id: "raspberry-pi-5-model-b"` unchanged. Keep all citations, notes, contributed_by, captured_at, verified, defaults, primary_controller blocks. Confirm test fixtures that reference this id still pass.

---

## Migration semantics (v1.0 → v1.1)

`migrate_v1_to_v1_1.go` is a NULL migration:
- v1.0 profile structs deserialize into v1.1 structs cleanly because all new fields use `omitempty`.
- No field rewrites required.
- Migration registered in `migrate.go` chain so RULE-HWDB-07 (chain integrity) stays green.
- Per RULE-HWDB-07: a test fails if `supported_versions` lists `"1.1"` but `migrate_1_0_to_1_1` is missing. Even though the function body is a no-op, the registration entry must exist.

```go
// migrate_v1_to_v1_1.go
func migrate_1_0_to_1_1(p *Profile) error {
    p.SchemaVersion = "1.1"
    return nil
}
```

`profile_v1.go` change:
```go
const SchemaVersion = "1.1"

var supportedVersions = []string{"1.0", "1.1"}
```

---

## CHANGELOG entry

Under `## [Unreleased]`:

```markdown
### Added

- Hardware profile schema v1.1 (`specs/spec-03-amendment-schema-v1.1.md`):
  - Optional `bios_version` field on `dmi_fingerprint` enabling
    Lenovo Legion gaming laptop dispatch (boards share product_name
    codes across generations; only BIOS_VERSION 4-char prefix
    disambiguates GKCN/EUCN/H1CN/M3CN/LPCN/N0CN families).
  - New `dt_fingerprint` block as an alternative to `dmi_fingerprint`
    for ARM/SBC systems without DMI. Mutual exclusion enforced by
    the schema validator. Matcher tries DMI first, falls through
    to device-tree when DMI is absent.
  - Formal semantics for `overrides.unsupported: true`: calibration
    skips autocurve, ventd emits a one-shot INFO log explaining the
    sensors-only mode, web UI consumers receive a "read-only" flag
    in board metadata.
- `dtfake` test helper for synthesizing `/proc/device-tree/`
  fixtures (mirrors existing `dmifake` pattern).
- Seven invariant bindings RULE-HWDB-PR4-01..07 covering schema
  v1.1 match semantics and migration.
- Two invariant bindings RULE-OVERRIDE-UNSUPPORTED-01..02 covering
  unsupported-override behavior in matcher and calibration.

### Changed

- Hardware profile schema bumped from v1.0 to v1.1. Migration is a
  null transform — existing v1.0 profile YAML parses cleanly under
  v1.1 with v1.1-specific fields defaulting to empty.
- Raspberry Pi 5 Model B board profile re-emitted to use
  `dt_fingerprint` instead of the synthesized DMI workaround. The
  `synthesize_fingerprint_from_dt` and `no_dmi` overrides are now
  redundant and have been dropped from this entry.
```

---

## Success conditions

1. `go test -race ./internal/hwdb/...` passes — all PR 4 subtests + existing PR 1/2/3 tests still green.
2. `go test -race -run TestRuleHwdb ./internal/hwdb/` shows 7 PR4 subtests + 9 PR1 subtests + 14 PR2 subtests all passing.
3. `go test -race -run TestRuleOverrideUnsupported ./internal/calibration/` shows 2 subtests passing.
4. `go test -race -run TestMigrate_ChainIntegrity ./internal/hwdb/` passes (chain now covers v1.0 → v1.1).
5. `go run ./tools/rulelint` returns 0. Zero allow-orphan markers in PR 4's new rules.
6. `golangci-lint run ./...` returns 0.
7. `goleak` integration confirms zero goroutine leaks.
8. `internal/hwdb/catalog/boards/raspberry-pi.yaml` parses cleanly. Pi 5 entry resolves under DT matcher path on a synthesized DT fixture; resolves to NO match under DMI-only fixture.
9. `internal/hwdb/profiles.yaml` (legacy ModuleProfile catalog from PR 1) still parses cleanly — schema bump must not regress module-matcher path.
10. `CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd` succeeds.
11. CHANGELOG `[Unreleased]` updated with both `### Added` and `### Changed` blocks above.
12. `go list -deps ./cmd/ventd | grep internal/hwdb` confirms all new files wired in (avoid PR 2 ghost-code regression — load-bearing types must actually be constructed somewhere reachable from main).
13. CC writes a brief PR description summarizing what landed; Phoenix opens the PR via `gh pr create`.

---

## Verification before marking done

Run these in order. CC must report each step's actual output, not just "ran successfully":

```bash
# 1. Tests
go test -race -count=1 ./internal/hwdb/... ./internal/calibration/...

# 2. Lint
golangci-lint run ./...

# 3. Rulelint — confirm no orphan markers in PR 4 rules
tools/rulelint
# Expected: zero output / exit 0.
grep -rn "rulelint:allow-orphan" .claude/rules/hwdb-pr4-*.md .claude/rules/override-unsupported-*.md
# Expected: empty output (no markers remaining post-impl).

# 4. Build
CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd

# 5. Wiring check (avoid ghost-code)
go list -deps ./cmd/ventd | grep internal/hwdb
# Expected: includes profile_v1_1, dt_fingerprint paths.

# 6. YAML load smoke test
cat <<'EOF' > /tmp/load_test.go
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "github.com/ventd/ventd/internal/hwdb"
)

func main() {
    matches, _ := filepath.Glob("internal/hwdb/catalog/boards/*.yaml")
    for _, m := range matches {
        if _, err := hwdb.LoadBoardYAML(m); err != nil {
            fmt.Printf("FAIL %s: %v\n", m, err)
            os.Exit(1)
        }
    }
    fmt.Printf("OK %d files parsed\n", len(matches))
}
EOF
go run /tmp/load_test.go
rm /tmp/load_test.go
# Expected: "OK N files parsed" with N matching scope-A + scope-B current count.
```

(Adjust `hwdb.LoadBoardYAML` to match the actual function name from PR 2a / PR 3 — find via `grep -r "func.*Load.*Board" internal/hwdb/`.)

---

## Conventional commits at boundaries (suggested)

- `feat(hwdb): bump profile schema to v1.1 with bios_version field`
- `feat(hwdb): dt_fingerprint block for ARM/SBC systems without DMI`
- `feat(hwdb): formal semantics for overrides.unsupported: true`
- `feat(calibration): skip autocurve when board marked unsupported`
- `test(hwdb): RULE-HWDB-PR4-01..07 schema v1.1 invariant bindings`
- `test(calibration): RULE-OVERRIDE-UNSUPPORTED-01..02 bindings`
- `test(hwdb): dtfake helper for /proc/device-tree fixtures`
- `chore(hwdb): re-emit Pi 5 entry with dt_fingerprint, drop synth hack`
- `docs(hwdb): document schema v1.1 changes in hwdb-schema.md`

---

## Explicit non-goals

- No new board YAML seeds. Legion + IPMI + Pi 4B + CM4 catalog land in **PR 5** (separate session).
- No changes to ModuleProfile / `module_match.go` from PR 1 — legacy schema v0 untouched.
- No `cmd/ventd-ui/*` web UI changes — read-only mode banner is spec-12 territory.
- No diagnostic bundle changes — spec-03 PR 2c already handles forward-compat versioning.
- No HP consumer family enumeration as YAML entries — that's catalog-seed work, deferred to PR 5 or scope-D.
- No NBFC / Framework EC backend — that's spec-09 territory.
- No new fingerprint tuple change — RULE-HWDB-FP-FROZEN remains intact (fingerprint hash input tuple unchanged).

---

## Open issues to file (separate from PR 4)

PR 4 commit body should mention these follow-ups for Phoenix to file via `gh issue create` (CC drafts text, Phoenix executes per `.claude/rules/collaboration.md`):

- "Web UI: surface `overrides.unsupported: true` board metadata as 'Read-only mode' banner" — `area/web`, `priority/p2`, `spec-12`
- "Diagnostic bundle: include `dt_fingerprint.compatible` and `dt_fingerprint.model` when matched profile uses DT path" — `area/safety`, `priority/p3`, `spec-03`
- "spec-09 NBFC: confirm Framework laptop fan path before scope-D adds Framework board YAMLs" — `area/hal`, `priority/p2`, `spec-09`

---

## Token-cost expectation

Sonnet, single CC session. Mostly mechanical transcription of the amendment doc into Go + YAML + Markdown:
- ~120 LOC `profile_v1_1.go` (struct extensions + accessor helpers)
- ~80 LOC `migrate_v1_to_v1_1.go` (skeleton + null migration)
- ~150 LOC `dt_fingerprint.go` (proc reader + glob match + validator)
- ~100 LOC `dtfake/dtfake.go` (test helper)
- ~250 LOC `profile_v1_1_test.go` (7 subtests + table-driven cases)
- ~80 LOC `internal/calibration/skip_unsupported.go`
- ~120 LOC `skip_unsupported_test.go`
- ~150 lines `.claude/rules/hwdb-pr4-{01..07}.md` + `override-unsupported-{01..02}.md`
- ~80 lines `docs/hwdb-schema.md` v1.1 section
- Pi 5 entry re-emit (small)

Estimate **$14–22**.

Pad estimate to **$18–28** if matcher tier-1 already handles glob matching purely through `path/filepath.Match` (no extension needed) vs needing a custom matcher (extension). Check existing matcher_v1.go before starting — `grep -n "Match" internal/hwdb/matcher_v1.go`.
