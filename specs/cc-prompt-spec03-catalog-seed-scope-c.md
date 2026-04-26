# cc-prompt-spec03-pr5.md

**For:** Claude Code, **Sonnet** model. Do NOT use Opus inside CC.
**Estimated cost:** $5–12 (data-only PR, no Go code, mostly YAML + 2 driver YAMLs + small docs).
**Estimated duration:** 15–30 minutes.
**Prerequisite:**
- spec-03 PR 4 merged (`spec-03/pr-4-schema-v1.1`) — schema v1.1 with `bios_version`, `dt_fingerprint`, and `unsupported` override semantics live on main.
- 17 board YAML artifacts staged in project knowledge or repo workdir under `/mnt/project/scope-c/`:
  - `lenovo-legion.yaml` (7 boards)
  - `dell-poweredge.yaml` (3 boards)
  - `hpe-proliant.yaml` (2 boards)
  - `supermicro-additional.yaml` (3 boards)
  - `raspberry-pi-additional.yaml` (2 boards)
  - `legion_hwmon.yaml` (driver YAML)
  - `ipmi_bmc.yaml` (driver YAML)
  - `2026-04-board-catalog-citations-scope-c-append.md`
  - `2026-04-framework-backend-memo.md`

If the artifacts aren't yet present in the repo workdir, copy them in BEFORE running this prompt:
```bash
cp /mnt/project/scope-c/*.yaml internal/hwdb/catalog/{drivers,boards}/  # split per file (see §"Files to create" below)
cp /mnt/project/scope-c/2026-04-board-catalog-citations-scope-c-append.md docs/research/
cp /mnt/project/scope-c/2026-04-framework-backend-memo.md docs/research/
```

(Phoenix runs the copy step before launching CC, OR the artifacts are pre-staged in the project. Confirm with Phoenix before assuming either.)

**Branch:** `spec-03/pr-5-catalog-seed-scope-c`

---

## Context

scope-C catalog research (chat-driven, Opus) produced 17 board YAMLs + 2 driver YAMLs + 2 docs covering:
- 7 Lenovo Legion gaming laptops (Gen 5–9, Intel + AMD), depending on `bios_version` glob (schema v1.1).
- 3 Dell PowerEdge 14G servers (R740/R640/R740xd), all flagging iDRAC9 ≥3.34 fan-control block.
- 2 HPE ProLiant Gen10 servers (DL380/DL360), telemetry-only via iLO 5.
- 3 additional Supermicro boards (X11SCH-LN4F / X12STH-F / H13SSL-N), extending scope-B Supermicro coverage.
- 2 Raspberry Pi additions (Pi 4B + CM4-on-CM4IO), depending on `dt_fingerprint` (schema v1.1).
- New `legion_hwmon` driver entry (OOT DKMS, johnfanv2/LenovoLegionLinux).
- New `ipmi_bmc` driver entry (mainline, references spec-01 backend).

This PR is **data-only**. Zero Go code changes. Zero new RULE-* invariants. The schema-side enforcement comes from PR 4's RULE-HWDB-PR4-01..07. Existing RULE-HWDB-PR2-04 (strict-decode) and RULE-HWDB-PR2-06 (PII gate) catch any malformed YAML at PR 5 load time.

---

## Files to create or modify

**Create driver YAMLs:**
- `internal/hwdb/catalog/drivers/legion_hwmon.yaml` — paste from `/mnt/project/scope-c/legion_hwmon.yaml` (or `docs/research/scope-c/legion_hwmon.yaml` if relocated).
- `internal/hwdb/catalog/drivers/ipmi_bmc.yaml` — paste from `/mnt/project/scope-c/ipmi_bmc.yaml`.

**Create board YAMLs:**
- `internal/hwdb/catalog/boards/lenovo-legion.yaml` — 7 board entries.
- `internal/hwdb/catalog/boards/dell-poweredge.yaml` — 3 board entries.
- `internal/hwdb/catalog/boards/hpe-proliant.yaml` — 2 board entries.

**Modify board YAMLs (append to existing scope-B files):**
- `internal/hwdb/catalog/boards/supermicro.yaml` — append 3 new board entries (X11SCH-LN4F, X12STH-F, H13SSL-N) to the existing scope-B file. Existing 3 scope-B entries (X11SCH-F, X10SLH-F, H12SSL-i) stay intact. Resulting file has 6 board profiles total.
- `internal/hwdb/catalog/boards/raspberry-pi.yaml` — append 2 new entries (Pi 4B, CM4-on-CM4IO) to the existing scope-B Pi 5 entry. Existing Pi 5 entry stays intact (with PR 4's `dt_fingerprint` re-emit applied). Resulting file has 3 board profiles total.

**Create docs:**
- `docs/research/2026-04-board-catalog-citations-scope-c-append.md` — paste from artifact.
- `docs/research/2026-04-framework-backend-memo.md` — paste from artifact.

**Modify docs:**
- `docs/research/2026-04-driver-amendments-needed.md` — update §LEGION-1, §IPMI-1, §SCHEMA-BIOSVER, §SCHEMA-DT, §HP-CONSUMER status from "P1/P2 needed" to "DELIVERED in PR 4 (schema) / PR 5 (catalog)". Defer §FW-1 with a one-line note pointing to `2026-04-framework-backend-memo.md`.
- `CHANGELOG.md` — `[Unreleased]` `### Added` entry (template below).

**Do not touch:**
- Schema files (`profile_v1.go`, `profile_v1_1.go`, `dt_fingerprint.go`) — PR 4 owns these.
- Any Go code at all. This PR is YAML + Markdown only.
- Existing scope-A / scope-B board YAMLs (msi.yaml, asus.yaml, gigabyte.yaml, asrock.yaml, generic.yaml, dell.yaml, hp.yaml, lenovo-thinkpad.yaml, lenovo-ideapad.yaml — all stay byte-identical).
- Existing scope-A / scope-B driver YAMLs.
- `.claude/rules/*.md` — no new rules in this PR.

---

## Pre-flight checks before pasting YAML

Each artifact YAML may need minor adjustments to match the actual schema struct field names emitted by PR 4. Run these checks first:

```bash
# 1. Confirm schema v1.1 is on main
git log --oneline | head -5
grep "1.1" internal/hwdb/profile_v1.go
# Expected: SchemaVersion = "1.1"

# 2. Confirm dt_fingerprint type exists
grep -n "DTFingerprint" internal/hwdb/dt_fingerprint.go internal/hwdb/profile_v1_1.go
# Expected: hits in both files

# 3. Confirm bios_version field exists
grep -n "BiosVersion" internal/hwdb/profile_v1_1.go
# Expected: 1 hit on the DMIFingerprint struct extension

# 4. Confirm Pi 5 re-emit is on main (from PR 4)
grep -A2 "raspberry-pi-5-model-b" internal/hwdb/catalog/boards/raspberry-pi.yaml
# Expected: dt_fingerprint block, no synthesize_fingerprint_from_dt
```

If any check fails, STOP — PR 4 is not fully merged. Do not proceed.

---

## YAML field mapping check

The artifact YAML uses snake_case field names per the chat-side amendment doc. Before commit, verify they match the actual struct tags emitted by PR 4. If mismatch, normalize the YAML to match the struct (PR 4 is the source of truth — adjust the YAML, not the struct).

Common likely-correct mappings:

| Artifact YAML field | Likely struct tag | Verify with |
|---|---|---|
| `dmi_fingerprint.bios_version` | `yaml:"bios_version,omitempty"` | `grep -n "bios_version" internal/hwdb/profile_v1_1.go` |
| `dt_fingerprint.compatible` | `yaml:"compatible,omitempty"` | `grep -n "compatible" internal/hwdb/dt_fingerprint.go` |
| `dt_fingerprint.model` | `yaml:"model,omitempty"` | `grep -n "model" internal/hwdb/dt_fingerprint.go` |
| `overrides.unsupported` | `yaml:"unsupported,omitempty"` | `grep -n "Unsupported" internal/hwdb/profile_v1_1.go` |

If any artifact YAML uses a field name NOT in PR 4's struct (e.g. `bmc_overrides_hwmon`, `fan_control_blocked_by_idrac9_3_34`, `prefer_ipmi_backend`, `requires_dkms`, `ec_chip_id`, `vendor_raw_command_set`), confirm with `grep -rn "Overrides struct" internal/hwdb/profile_v1.go internal/hwdb/profile_v1_1.go` whether `Overrides` is a typed struct with strict-decode (each field must exist) or a `map[string]interface{}` (free-form, no enforcement).

If typed struct: most of these scope-C `overrides:` fields will FAIL strict-decode. Two options:
- (A) Drop the offending fields from the YAML — preserve only the ones with corresponding struct fields (`unsupported`, plus whatever exists from scope-A / scope-B precedent like `pwm_scale`, `requires_watchdog`, `secondary_fan_uncontrollable`).
- (B) Coerce the rich override fields into `notes:` block prose.

Recommended: do (A). The rich override fields are documentation-grade signals for future ventd backend work, not currently consumed by the matcher. Move them into `notes:` as bullet-style prose so the schema accepts the YAML cleanly. The information survives in citations + notes; the matcher sees a clean overrides block.

If `Overrides` is a free-form map (not typed struct), the YAML can stay as-is. CC verifies before committing.

---

## File splits if pasted YAML doesn't match expected layout

The scope-B precedent split each vendor into its own file: `dell.yaml`, `hp.yaml`, `lenovo-thinkpad.yaml`, etc. Continue that pattern here:

| Source artifact | Target catalog file | Action |
|---|---|---|
| `lenovo-legion.yaml` | `internal/hwdb/catalog/boards/lenovo-legion.yaml` | Create new (Lenovo Legion is a separate sub-family from ThinkPad/IdeaPad which already have their own files). |
| `dell-poweredge.yaml` | `internal/hwdb/catalog/boards/dell-poweredge.yaml` | Create new (PowerEdge servers are a distinct line from `dell.yaml` consumer/business laptops). |
| `hpe-proliant.yaml` | `internal/hwdb/catalog/boards/hpe-proliant.yaml` | Create new. |
| `supermicro-additional.yaml` | `internal/hwdb/catalog/boards/supermicro.yaml` | **Append** to existing scope-B file. Do not overwrite. |
| `raspberry-pi-additional.yaml` | `internal/hwdb/catalog/boards/raspberry-pi.yaml` | **Append** to existing file (Pi 5 entry from PR 4 stays first). |

For the append cases, copy the new entries' `board_profiles:` list items into the existing file's `board_profiles:` list. Single top-level `board_profiles:` key, multiple list entries.

---

## CHANGELOG entry

Under `## [Unreleased]`:

```markdown
### Added

- Hardware profile catalog scope-C seed (data-only, depends on schema v1.1):
  - 7 Lenovo Legion gaming laptop entries (Gen 5–9, Intel + AMD,
    Slim/standard/Pro tiers) using `bios_version` family-prefix dispatch
    (GKCN/EUCN/H1CN/M3CN/LPCN/N0CN/FSCN). Out-of-tree DKMS driver
    `legion_hwmon` from johnfanv2/LenovoLegionLinux.
  - 3 Dell PowerEdge 14G server entries (R740, R640, R740xd) with
    `fan_control_blocked_by_idrac9_3_34` documentation noting Dell's
    deliberate iDRAC9 firmware ≥3.34 lockdown of vendor raw IPMI
    fan-control commands.
  - 2 HPE ProLiant Gen10 server entries (DL380, DL360) marked
    telemetry-only — HPE iLO 5+ does not expose vendor IPMI fan
    control commands.
  - 3 additional Supermicro server boards (X11SCH-LN4F, X12STH-F,
    H13SSL-N) extending scope-B Supermicro coverage to AST2600 BMC
    generation. H13SSL-N notes BMC panic-mode recovery via
    `ipmitool mc reset cold`.
  - 2 Raspberry Pi additions (Pi 4B, CM4-on-CM4IO) using
    `dt_fingerprint` for ARM device-tree dispatch. Pi 4B documents
    the `gpio-fan` (binary) vs `pwm-fan` (PWM) overlay distinction;
    CM4-on-CM4IO documents the EMC2301 I2C bus 10 path.
  - New `legion_hwmon` driver catalog entry (OOT DKMS).
  - New `ipmi_bmc` driver catalog entry (mainline ipmi_si stack)
    with vendor-specific raw command reference for Supermicro,
    Dell PowerEdge legacy, and notes on HPE iLO 5+ block.
- Catalog citations document for scope-C entries
  (`docs/research/2026-04-board-catalog-citations-scope-c-append.md`).
- Framework laptop backend tradeoff memo
  (`docs/research/2026-04-framework-backend-memo.md`) — defers
  Framework support to spec-09 NBFC integration chat.
```

---

## Success conditions

1. `go test -race ./internal/hwdb/...` passes — strict-decode validates every new YAML at load time. Any PII gate / unknown-field violations surface here.
2. Smoke load of every catalog YAML succeeds (test harness from PR 4):
   ```bash
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
   ```
   Expected: `OK 11 files parsed` (8 scope-A/B + 3 new boards files).
3. Driver YAML smoke load:
   ```bash
   # Adapt to actual driver loader function name from PR 2a
   for f in internal/hwdb/catalog/drivers/*.yaml; do
       go run -tags catalog-smoke ./tools/catalog-validate "$f" || exit 1
   done
   ```
   (If `tools/catalog-validate` doesn't exist, CC adds an inline equivalent OR uses Go test fixture loader. Don't burn time on tooling — a 30-line Go script is fine.)
4. `tools/rulelint` returns 0. No new rules introduced; existing rules unchanged.
5. `golangci-lint run ./...` returns 0 (no Go changes; should be untouched-baseline).
6. `CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd` succeeds.
7. `git diff --stat` shows ONLY YAML + Markdown changes. No `.go` files touched. If any `.go` file appears in the diff, STOP — that's PR 4 territory or a regression.
8. Synthesized DT fixture test (manual sanity check, not a new RULE-* binding):
   ```bash
   # Confirm Pi 4B entry resolves correctly under DT
   go test -race -run TestRuleHwdbPR4_03 ./internal/hwdb/ -v
   go test -race -run TestRuleHwdbPR4_04 ./internal/hwdb/ -v
   ```
9. Synthesized DMI fixture test for Legion (manual sanity check):
   ```bash
   # Confirm Legion entries resolve via bios_version glob
   go test -race -run TestRuleHwdbPR4_01 ./internal/hwdb/ -v
   ```
10. CHANGELOG `[Unreleased]` updated with `### Added` block above.
11. CC writes a brief PR description summarizing what landed; Phoenix opens the PR via `gh pr create`.

---

## Verification before marking done

```bash
# 1. Tests
go test -race -count=1 ./internal/hwdb/... ./internal/calibration/...

# 2. Lint
golangci-lint run ./...

# 3. Rulelint (sanity — no new rules in this PR)
tools/rulelint

# 4. Build
CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd

# 5. YAML inventory
ls internal/hwdb/catalog/boards/*.yaml
ls internal/hwdb/catalog/drivers/*.yaml
# Expected boards: 11 files (8 scope-A/B + lenovo-legion, dell-poweredge, hpe-proliant)
# Expected drivers: existing scope-A/B set + 2 new (legion_hwmon, ipmi_bmc)

# 6. Diff sanity
git diff --stat main..HEAD
# Expected: ONLY .yaml + .md in changed files. No .go.

# 7. Per-vendor count check
yq eval '.board_profiles | length' internal/hwdb/catalog/boards/lenovo-legion.yaml
# Expected: 7
yq eval '.board_profiles | length' internal/hwdb/catalog/boards/dell-poweredge.yaml
# Expected: 3
yq eval '.board_profiles | length' internal/hwdb/catalog/boards/hpe-proliant.yaml
# Expected: 2
yq eval '.board_profiles | length' internal/hwdb/catalog/boards/supermicro.yaml
# Expected: 6 (3 scope-B + 3 scope-C)
yq eval '.board_profiles | length' internal/hwdb/catalog/boards/raspberry-pi.yaml
# Expected: 3 (1 scope-B Pi 5 + 2 scope-C: Pi 4B + CM4-on-CM4IO)

# 8. Schema version check
grep -h "schema_version" internal/hwdb/catalog/boards/*.yaml | sort -u
# Expected: only "1.1" (or absent if schema_version is set at file level not per-profile).
```

---

## Conventional commits at boundaries (suggested)

- `feat(hwdb): legion_hwmon driver catalog entry (OOT DKMS)`
- `feat(hwdb): ipmi_bmc driver catalog entry (mainline ipmi_si)`
- `feat(hwdb): seed 7 Lenovo Legion gaming laptops with bios_version dispatch`
- `feat(hwdb): seed 3 Dell PowerEdge 14G servers (R740/R640/R740xd)`
- `feat(hwdb): seed 2 HPE ProLiant Gen10 servers (DL380/DL360)`
- `feat(hwdb): extend Supermicro catalog with X11SCH-LN4F, X12STH-F, H13SSL-N`
- `feat(hwdb): seed Pi 4B and CM4-on-CM4IO using dt_fingerprint`
- `docs(research): scope-C catalog citations append`
- `docs(research): Framework laptop backend tradeoff memo`
- `docs(research): mark scope-C amendments DELIVERED in driver-amendments-needed`

---

## Explicit non-goals

- No Go code changes. PR 4 owns all schema/matcher/calibration logic.
- No new RULE-* invariants. Strict-decode (RULE-HWDB-PR2-04) and PII gate (RULE-HWDB-PR2-06) catch malformed YAML.
- No per-board curve seeding. All entries ship with `verified: false` and `defaults.curves: []` per existing scope-A/B convention. Curves come from scope-D / community contributions.
- No HP consumer family enumeration as YAML. The chat-side amendment doc enumerates them informationally; concrete YAMLs come in scope-D when there's enough demand to justify entries that ship with `unsupported: true`.
- No Framework laptop entries. Defer to spec-09 NBFC integration chat per `2026-04-framework-backend-memo.md`.
- No Legion 7 16ACHg6 (AMD variant) or Legion 5 15ARH05 (non-H variant) entries. Could land in scope-D wave 2 if hardware testers report demand.
- No iDRAC firmware-version probe at startup (the override field is documented in YAML but ventd-side detection is a v0.6.0+ feature).
- No EMC2301 driver catalog entry — emc2301 is mainline kernel; Pi CM4 entry references it implicitly via `chip: "emc2301"`. If matcher needs an explicit driver YAML for this chip, add a minimal one (~10 lines) following the existing chip catalog pattern. Otherwise skip.

---

## Open issues to file (separate from PR 5)

PR 5 commit body should mention these follow-ups for Phoenix to file via `gh issue create`:

- "scope-D wave 2: add Legion 7 16ACHg6 (82N6, GKCN family AMD) + Legion 5 15ARH05 (82B5, EUCN family non-H variant)" — `area/profile`, `priority/p3`, `spec-03`
- "v0.6.0+: probe iDRAC firmware version at startup for Dell PowerEdge boards; warn user if ≥3.34.x" — `area/safety`, `priority/p2`, `spec-03`
- "Add HP consumer family entries (Pavilion, Envy, Spectre) with `overrides.unsupported: true`" — `area/profile`, `priority/p3`, `spec-03`
- "v0.7.0+: implement vendor raw IPMI command dispatch for Supermicro/Dell legacy via spec-01 IPMI backend" — `area/hal`, `priority/p2`, `spec-01`

---

## Token-cost expectation

Sonnet, single CC session. Mostly mechanical paste + verify:
- ~7 YAML files (all already drafted, ~150-400 lines each)
- ~2 Markdown docs (already drafted, ~280 + ~211 lines)
- 1 small docs update (driver-amendments-needed status table)
- 1 CHANGELOG block
- File splits + appends (raspberry-pi.yaml + supermicro.yaml)

Estimate **$5–12**.

If `Overrides` strict-decode rejects scope-C-rich override fields and CC must coerce them into `notes:` prose, add **+$3-5** for the field-by-field stripping. Pre-flight grep checks above catch this early so CC isn't hunting blind.

---

## Why this PR is cheap

Everything load-bearing was decided in chat (Opus, flat-rate). Schema enforcement is in PR 4 (already merged). PR 5 is data-only; CC's job is paste, verify, commit. The expensive parts (BIOS_VERSION research, iDRAC firmware lockdown investigation, Pi DT compatible string verification, EMC2301 carrier-board mechanics) are baked into the YAMLs and citation doc. CC doesn't redo any of that.

This is the calibration target from `docs/claude/spec-cost-calibration.md`: tight spec + pre-written artifacts + chat-driven design = CC at $5-12 actual vs $10-30 envelope.
