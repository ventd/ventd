# cc-prompt-spec03-pr3.md — Board catalog seed entries

**Target:** spec-03 PR 3 (board profile seed entries).
**Predecessor:** spec-03 PR 2d (GPU vendor catalog, merged).
**Source:** chat-research output 2026-04-26 — 5 YAML files + 1 citations doc.

Sonnet only. No subagents. Single PR. Conventional commits.

**Branch:** `spec-03/pr-3-catalog-seed`
**Estimate:** $3-8 (mostly file placement + CHANGELOG + DoD verification, no
novel code).

---

## Read first

1. `specs/spec-03-profile-library.md` §"PR 3 — Seed entries (25 boards minimum)"
   — original spec for PR 3.
2. `specs/spec-03-amendment-pwm-controllability.md` §11 (matcher tiers) and
   §14 (board profile shape).
3. `internal/hwdb/profile_v1.go` — schema struct definitions (PR 2a).
4. `internal/hwdb/catalog/drivers/nct6775.yaml` and other chip catalogs
   (PR 2a) — the chip-level fields that board entries reference by name.

---

## Scope

Place 5 pre-written YAML files into `internal/hwdb/catalog/boards/`. Place 1
research doc into `docs/research/`. Update CHANGELOG. Verify schema validation.

This PR ships **15 board entries** (12 specific + 3 generic heuristics).

All entries: `verified: false`, `defaults.curves: []`, every DMI field cited
to a primary source.

Phoenix has decided NOT to ship invented curves or guessed DMI strings. The
catalog's value is the fingerprint surface for spec-10 doctor + spec-11
wizard, not pre-baked tuning.

---

## Files to create

The exact contents of these files are pre-written. CC should place them
verbatim from the chat artifact bundle:

- `internal/hwdb/catalog/boards/msi.yaml` (4 entries)
- `internal/hwdb/catalog/boards/asus.yaml` (5 entries)
- `internal/hwdb/catalog/boards/gigabyte.yaml` (3 entries)
- `internal/hwdb/catalog/boards/asrock.yaml` (3 entries)
- `internal/hwdb/catalog/boards/generic.yaml` (3 entries — tier-3 fallbacks)
- `docs/research/2026-04-board-catalog-citations.md` (research methodology +
  per-board source index)

---

## Files to modify

- `CHANGELOG.md` — under `[Unreleased]` `### Added`:

  ```
  - Hardware profile board catalog seed: 15 entries across MSI, ASUS, Gigabyte,
    ASRock, and 3 generic chip-family fallbacks. All entries `verified: false`
    with empty curves; v0.5.x patches will validate one board at a time
    (spec-03 PR 3).
  - Documented Gigabyte IT8689E rev 1 BIOS-override write-block quirk on X670E
    Aorus Master and B650 Aorus Elite AX entries — calibration probe will
    detect via writes-accepted-but-ineffective pattern and route affected
    channels to monitor-only mode.
  ```

---

## Files NOT touched

- `internal/hwdb/profile_v1.go` — schema v1.0 frozen, no changes.
- `internal/hwdb/matcher_v1.go` — matcher already supports board-tier from
  PR 2a; the new YAML files load into existing infrastructure.
- `internal/hwdb/catalog/drivers/*.yaml` — chip catalog unchanged.
- All ventd binary code — this is data-only, no Go code changes.

---

## Invariants — bind to existing rules

No new RULE-* invariants in this PR. The existing rules cover:
- **RULE-HWDB-PR2-04** (schema v1.0 strict-decode `KnownFields(true)`):
  every new YAML must parse cleanly under existing schema. CI catches drift.
- **RULE-HWDB-PR2-06** (PII gate via `KnownFields(true)`): no smbios_uuid /
  serial fields possible in YAML. No new test needed.
- **RULE-HWDB-PR2-09** (firmware version handling): `bios_version_min/max`
  in board fingerprint when populated; for now all entries leave these null
  ("any BIOS version") — future patches will narrow as needed.

---

## Success conditions

1. `go test ./internal/hwdb/...` passes — synthetic-fixture tests should
   either pick up the new YAMLs automatically (if test loader iterates
   `catalog/boards/`) or stay green if test loader uses an inline test
   corpus.
2. `tools/rulelint` returns 0 (no new rules in this PR — markers stay clean).
3. `golangci-lint run ./...` returns 0.
4. CHANGELOG entry under `[Unreleased]` `### Added` with both lines above.
5. `go build -tags netgo -ldflags '-s -w' ./cmd/ventd` succeeds with
   CGO_ENABLED=0.
6. **Manual schema check:** at end of PR, run a quick Go program (or shell
   one-liner) that loads every `internal/hwdb/catalog/boards/*.yaml`
   through `internal/hwdb/profile_v1.LoadBoard()` (or whatever the
   schema-loader function is named) and confirms zero errors. The pre-written
   YAML should parse cleanly, but verification catches silent typos.

---

## Verification before marking done

```
1. go test ./internal/hwdb/... -v -count=1
2. golangci-lint run ./...
3. tools/rulelint
4. CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd
5. # Quick load test:
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

(Adjust import path + function name to match what PR 2a actually exposed.
If `hwdb.LoadBoardYAML` doesn't exist, find the equivalent via
`grep -r "func.*Board" internal/hwdb/`.)

---

## Stop and surface to Phoenix if

- Schema-load fails on any of the 5 board YAML files — surface, the
  pre-written YAML may have a field name mismatch with the schema PR 2a
  actually shipped. Compare YAML field names to `profile_v1.go` struct
  tags before guessing.
- Existing tests start failing because the test loader picks up the new
  YAMLs and a synthetic-fixture test was relying on `len(boards) == 0`.
  Surface — fix is either to update the test fixture or to scope the test
  to the inline corpus only.
- A YAML field referenced in board YAML doesn't exist in the chip catalog
  (e.g. `chip: "nct6687"` references a chip catalog entry that PR 2a
  didn't ship). Surface — chip catalog may need amendment in same PR
  (one extra YAML in `catalog/chips/`) or the board entry deferred to
  next patch.
- Total CC spend crosses $5 — surface, this should be cheap.

---

## PR description must call out

- "spec-03 PR 3 closes the v0.5.0 hardware catalog work. 15 entries: 12
  specific boards + 3 generic chip-family fallbacks."
- "All entries `verified: false` per spec-03 §PR3. v0.5.x patches will
  flip individual boards to `verified: true` as Phoenix and contributors
  validate them on real hardware."
- "Gigabyte X670E Aorus Master and B650 Aorus Elite AX entries flagged
  with `bios_overridden_pwm_writes: true` — known IT8689E rev 1
  write-blocked quirk per frankcrawford/it87#96 and #68. ventd's
  calibration probe will detect this via writes-accepted-but-ineffective
  pattern and route to monitor-only."
- "ASRock X670E Taichi uses `additional_controllers` schema field for
  dual-chip NCT6796D-S + NCT6686D — first concrete use of the field."
- "Research methodology + per-board source index in
  docs/research/2026-04-board-catalog-citations.md."

---

**End of cc-prompt-spec03-pr3.md.**
