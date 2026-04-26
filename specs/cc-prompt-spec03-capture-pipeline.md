# cc-prompt-spec03-capture-pipeline.md

**For:** Claude Code, **Sonnet** model. Do NOT use Opus inside CC.
**Estimated cost:** $12–20.
**Estimated duration:** 30–45 minutes.
**Prerequisite:**
- spec-03 PR 1 (#629) merged — schema v1, fingerprint tuple, RULE-HWDB-01..09.
- spec-03 PR 2a/2c/2d merged — matcher + diag redactor + GPU vendor catalog.
- spec-03 PR 3 (#644) merged — board catalog seed (15 entries).
- (Optional) Schema v1.1 PR (`spec-03/schema-v1.1`) may or may not be merged. Capture pipeline doesn't depend on schema v1.1; if v1.1 is on main, capture writes v1.1 by default. If v1.0 is current, capture writes v1.0. Either is fine.

**Branch:** `spec-03/capture-pipeline`

---

## Context

After successful calibration, ventd should write a candidate hardware profile to a pending directory so the user can review and (eventually) submit it back to the catalog. This is **the original spec-03 PR 4** from `specs/spec-03-profile-library.md`.

PR 2c shipped a complete redactor framework (`internal/diag/redactor/` with P1–P9 redactor primitives). Capture pipeline reuses that framework rather than reimplementing PII stripping.

This PR delivers:
1. `internal/hwdb/capture.go` — orchestrates the capture flow (calibration result → anonymise → write to profiles-pending).
2. `internal/hwdb/anonymise.go` — thin wrapper around `internal/diag/redactor/` configured for profile-class PII (DMI serials, MAC, hostname, username, paths, USB physical paths). NOT a reimplementation.
3. `/var/lib/ventd/profiles-pending/<fingerprint-hash>.yaml` — output destination, mode 0640, owner `ventd:ventd`.
4. Fuzz target `FuzzAnonymise` — 100 sample inputs with PII patterns; zero leakage assertion.
5. RULE-HWDB-CAPTURE-01..03 (the original spec-03 RULE-HWDB-08..10 from base spec, retained as numbered for capture).

PR 2c's redactor primitives that we'll reuse:
- `P1Hostname`, `P2DMI`, `P3MAC`, `P4IP`, `P5Username`, `P6Path`, `P7USBPhysical`, `P8Cmdline`, `P9UserLabel`.
- `MappingStore` for consistent-mapped redactions within a single capture.
- `Redactor` aggregator interface.

---

## Files to create or modify

**Create:**
- `internal/hwdb/capture.go` — `Capture()` entry point: takes a `CalibrationResult`, fingerprint, and target directory; returns the path written or an error.
- `internal/hwdb/capture_test.go` — table-driven test: synthetic CalibrationResult inputs, assert anonymised output is well-formed schema-valid profile YAML.
- `internal/hwdb/anonymise.go` — `Anonymise(profile *Profile) error` that mutates a profile in place via redactor framework. Wraps `internal/diag/redactor`.
- `internal/hwdb/anonymise_test.go` — table-driven test + 100-sample fuzz corpus.
- `internal/hwdb/anonymise_fuzz_test.go` — `FuzzAnonymise` Go fuzz target. Seeds with 100 sample inputs that embed plausible PII patterns; assertion: post-anonymise output contains no input PII tokens (check by reflection over fields known to contain user data).
- `internal/hwdb/testdata/anonymise/seed_*.yaml` — 100 fuzz seed inputs with PII patterns (hostname, MAC, IP, paths under /home/<user>/, kernel UUIDs).
- `.claude/rules/hwdb-capture-{01..03}.md` — 3 rule files with `<!-- rulelint:allow-orphan -->` markers initially.

**Modify:**
- `internal/hwdb/migrate.go` or wherever the post-calibration hook lives — wire `Capture()` to fire after a successful calibration completes.
- `internal/calibration/store.go` — emit hook signal after writing the calibration JSON. Capture pipeline subscribes.
- `cmd/ventd/main.go` (or equivalent daemon entrypoint) — ensure `/var/lib/ventd/profiles-pending/` directory exists at startup, mode 0750 owner ventd.
- `docs/hwdb-schema.md` — add §"Capture pipeline" section: where pending profiles go, how user reviews, what gets anonymised.
- `CHANGELOG.md` — `[Unreleased]` `### Added` block (template below).
- `.claude/rules/hwdb-schema.md` — add references to the 3 new RULE-HWDB-CAPTURE-* rules so rulelint sees them as part of the hwdb invariant set.

**Do not touch:**
- `cmd/ventd-ui/*` — no UI surface in this PR. UI for reviewing pending profiles is a separate later spec.
- `internal/diag/redactor/*` — reuse, don't extend. If a new redactor primitive is needed, file an issue and address in a follow-up PR.
- Network code — capture is local-only. No HTTP calls. No background sync. The user takes the file and decides what to do with it. Submission via `ventd --submit-profile` is P5-PROF-03, future spec.
- Existing PR 1/2/3 RULE-HWDB-* rules — must continue to pass unchanged.

---

## Invariants to bind (3 new)

Numbering: original spec-03 base spec reserved RULE-HWDB-08..10 for capture pipeline. PR 1's "predictive_hints" amendment took RULE-HWDB-08, and PR 1's stall_pwm_min took RULE-HWDB-09. To avoid further collision, capture pipeline rules use the **`RULE-HWDB-CAPTURE-NN`** prefix. This makes the namespace explicit.

| Rule | Subtest | Statement |
|---|---|---|
| RULE-HWDB-CAPTURE-01 | TestRuleHwdbCapture_01_PendingDirOnly | Capture writes go to `/var/lib/ventd/profiles-pending/` (or `$XDG_STATE_HOME/ventd/profiles-pending/` in user mode) only. Capture NEVER writes to the live `profiles.yaml` or `profiles-v1.yaml` at runtime. |
| RULE-HWDB-CAPTURE-02 | TestRuleHwdbCapture_02_FailClosedOnAnonymise | Capture cannot run if the anonymiser fails to initialise (any redactor primitive returns an error during construction). The capture function returns an error and writes nothing — fail closed. |
| RULE-HWDB-CAPTURE-03 | TestRuleHwdbCapture_03_AllowlistedFieldsOnly | A captured profile YAML never contains a field outside the schema v1.0 (or v1.1 if active) allowlist. The anonymiser strips fields that have been removed from the schema between schema versions. |

Each rule file template:

```markdown
# RULE-HWDB-CAPTURE-NN

<!-- rulelint:allow-orphan -->

Bound: internal/hwdb/capture_test.go::TestRuleHwdbCaptureNN

<rule statement copied verbatim from above>
```

Strip `<!-- rulelint:allow-orphan -->` markers as bindings resolve. **Final commit must have zero allow-orphan markers in this PR's new rules.**

---

## Capture flow design

```
CalibrationResult (from internal/calibration/store.go)
    │
    ▼
buildProfileFromCalibration()           ← internal/hwdb/capture.go
    │   constructs a *hwdb.Profile from calibration outputs
    │   + DMI fingerprint + driver/chip catalog references
    ▼
Anonymise(profile)                       ← internal/hwdb/anonymise.go
    │   uses internal/diag/redactor primitives:
    │   - P2DMI for serial scrub
    │   - P3MAC for any MAC fields
    │   - P5Username + P6Path for any user-derived strings
    │   - P9UserLabel for free-form labels
    ▼
schema.Validate(profile)                 ← existing, from PR 1
    │   strict-decode + PII gate enforces RULE-HWDB-06
    │
    ▼
yaml.Marshal(profile)
    │
    ▼
write_atomic(/var/lib/ventd/profiles-pending/<fingerprint>.yaml, mode 0640)
```

**Atomic write pattern** (avoid partial writes):
```go
tmpPath := finalPath + ".tmp"
if err := os.WriteFile(tmpPath, data, 0640); err != nil {
    return err
}
if err := os.Rename(tmpPath, finalPath); err != nil {
    os.Remove(tmpPath)
    return err
}
```

**Fingerprint hash filename:** Reuse `Fingerprint(dmi)` function from PR 1's `fingerprint.go` — produces 16-char hex SHA-256 prefix. Filename is `<fingerprint>.yaml`.

**User mode fallback:** If running unprivileged (no write access to `/var/lib/ventd/`), fall through to `$XDG_STATE_HOME/ventd/profiles-pending/` (default `~/.local/state/ventd/profiles-pending/`). Existing diag bundle code in `internal/diag/bundle.go` already implements this pattern — copy the resolution logic.

---

## Anonymisation policy

**Reuses `internal/diag/redactor/` primitives.** Configure a redactor pipeline specifically for profile-class data:

```go
// In anonymise.go
func newProfileRedactor() (*redactor.Pipeline, error) {
    return redactor.NewPipeline(
        redactor.P1Hostname{},      // hostname → obf_host_<n>
        redactor.P2DMI{},           // SMBIOS UUIDs, serials → [REDACTED]
        redactor.P3MAC{},           // MAC → aa:bb:cc:dd:ee:NN
        redactor.P5Username{},      // login names → obf_user_<n>
        redactor.P6Path{},          // /home/<user>/ paths
        redactor.P7USBPhysical{},   // usb-X-Y → usb-A-B
        redactor.P9UserLabel{},     // free-form strings → [REDACTED]
    )
}
```

(Adjust constructor calls to match actual redactor API; PR 2c may use `redactor.NewP1Hostname()` factory pattern. Verify with `grep -n "func New" internal/diag/redactor/*.go` before writing.)

**Fields stripped from a captured profile:**
- `smbios_uuid` (DMI gate already strips, but redactor catches any leak)
- `chassis_serial`, `system_serial`, `baseboard_serial`
- Any `mac_address` field
- `hostname`, `username`-derived path components
- Free-form user labels in fan name / profile name / comment fields
- USB physical paths (`usb-1-1.2:1.0` → `usb-A-B-C:D-E`)
- Kernel cmdline tokens matching `root=UUID=...`, `cryptdevice=UUID=...`, etc.

**Fields preserved:**
- Truncated 16-hex DMI fingerprint hash (already a one-way derivation, per RULE-HWDB-06 design)
- Hardware identification fields (sys_vendor, product_name, board_vendor, board_name, board_version, bios_version) — these are vendor-attributed strings, not user PII
- Driver/chip catalog references
- Calibration curve data (PWM/temp arrays)
- Capabilities, capabilities.driver, capabilities.kernel_module
- `contributed_by` — set to `"anonymous"` by default unless user has opted in via separate config flag (out of scope for this PR; default-anonymous is the safe choice)

---

## Fuzz target — `FuzzAnonymise`

```go
//go:build go1.18
// +build go1.18

package hwdb

import (
    "strings"
    "testing"
)

func FuzzAnonymise(f *testing.F) {
    // Seed with 100 inputs from testdata/anonymise/seed_*.yaml
    seeds := loadFuzzSeeds(f, "testdata/anonymise")
    for _, s := range seeds {
        f.Add(s)
    }

    f.Fuzz(func(t *testing.T, raw string) {
        profile, err := parseProfileYAML(raw)
        if err != nil {
            t.Skip() // not a valid YAML profile, skip
        }

        if err := Anonymise(profile); err != nil {
            return // fail-closed is acceptable
        }

        out, _ := yaml.Marshal(profile)
        outStr := string(out)

        // Assertions: known PII patterns must not survive
        for _, pattern := range []string{
            // From the seed input — extract PII tokens dynamically
        } {
            if strings.Contains(outStr, pattern) {
                t.Errorf("PII pattern %q survived anonymisation", pattern)
            }
        }
    })
}
```

**100-sample seed corpus generation:** Mix of hand-crafted YAML profiles with embedded:
- 30 samples with hostname (`my-laptop.local`, `desktop-X1Y2`, `phoenix-pc`, etc.)
- 20 samples with MAC addresses in hwmon path strings
- 20 samples with IPv4 / IPv6 addresses in user_label fields
- 15 samples with `/home/<user>/` paths in calibration metadata
- 10 samples with USB physical paths in fan name labels
- 5 samples with kernel cmdline tokens in `notes:` blocks

Each seed file is `testdata/anonymise/seed_<NN>.yaml`. CC generates these programmatically with `python3` or a small Go script — manual creation of 100 files is tedious; a generator + 5–10 hand-curated edge cases is fine.

**Assertion strategy:** For each seed, capture the PII tokens before anonymise; after anonymise, assert those exact tokens don't appear in the output. False positives (anonymise replaces "phoenix" with "obf_user_1" but seed had "Phoenix Lake" as a CPU codename) are acceptable — document and skip those cases in the seed file with a comment header.

---

## Post-calibration hook

`internal/calibration/store.go` already writes calibration JSON after a successful run. Add a post-write hook:

```go
// In internal/calibration/store.go after WriteCalibrationJSON
if hook := captureHook.Load(); hook != nil {
    go func() {
        if err := hook(*result, fingerprint); err != nil {
            log.Warn("capture pipeline failed", "err", err)
        }
    }()
}
```

`captureHook` is set during ventd startup if capture is enabled (default: enabled).

**Background goroutine, not blocking:** Calibration must not wait on capture. If anonymise fails or write fails, log and move on — calibration's primary job (writing live calibration data to `/var/lib/ventd/calibration/`) succeeded. Capture is best-effort.

**goleak compatibility:** The post-write goroutine must complete before test teardown. Use a `sync.WaitGroup` exposed via a test hook so `goleak.VerifyNone(t)` doesn't false-positive.

---

## Directory creation at daemon startup

In `cmd/ventd/main.go` or wherever startup directory provisioning lives:

```go
// /var/lib/ventd/profiles-pending/ — capture output
if err := os.MkdirAll("/var/lib/ventd/profiles-pending", 0750); err != nil {
    return fmt.Errorf("create profiles-pending dir: %w", err)
}
// Set ownership ventd:ventd if running as root (existing pattern from
// /var/lib/ventd/calibration/ — copy it).
```

Reuse the chown pattern from existing calibration dir creation. Don't reinvent.

---

## CHANGELOG entry

Under `## [Unreleased]`:

```markdown
### Added

- Hardware profile capture pipeline (`internal/hwdb/capture.go`,
  `internal/hwdb/anonymise.go`):
  - After successful calibration, ventd writes a candidate hardware
    profile to `/var/lib/ventd/profiles-pending/<fingerprint>.yaml`
    (or `$XDG_STATE_HOME/ventd/profiles-pending/` in user mode).
  - Anonymisation runs BEFORE write — no plaintext PII window. Reuses
    the redactor framework from `internal/diag/redactor/` (primitives
    P1, P2, P3, P5, P6, P7, P9 active for profile-class data).
  - Fail-closed semantics: if the anonymiser cannot initialise, capture
    skips (RULE-HWDB-CAPTURE-02). Calibration's primary outputs are
    unaffected.
  - Atomic write via `<file>.tmp` + rename. File mode 0640, owner
    `ventd:ventd` (root mode) or user (user mode).
- `FuzzAnonymise` fuzz target with 100 seed inputs covering hostname,
  MAC, IP, path, USB-physical, and kernel cmdline PII patterns.
- Three invariant bindings RULE-HWDB-CAPTURE-01..03 covering pending-dir
  isolation, fail-closed semantics, and schema-allowlist enforcement.
- No network — capture is local-only. Submission via
  `ventd --submit-profile <id>` is a future spec (P5-PROF-03).
```

---

## Success conditions

1. `go test -race ./internal/hwdb/... ./internal/calibration/...` passes.
2. `go test -race -run TestRuleHwdbCapture ./internal/hwdb/` shows 3 subtests passing.
3. `go test -fuzz=FuzzAnonymise -fuzztime=30s ./internal/hwdb/` runs 30 seconds with zero failures (CC runs locally; CI doesn't enforce fuzz duration).
4. `tools/rulelint` returns 0. No allow-orphan markers in PR's new rules.
5. `golangci-lint run ./...` returns 0.
6. `goleak` integration confirms zero goroutine leaks (the background capture goroutine completes cleanly).
7. `CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd` succeeds.
8. After `make install` (or equivalent) on a test VM, `/var/lib/ventd/profiles-pending/` exists with mode 0750, owner ventd:ventd. (Manual verification step — CC reports the result.)
9. Synthetic capture test: feed a calibration result through `Capture()`, confirm output file in profiles-pending parses cleanly via `LoadProfileYAML()` (or whatever the schema loader is named) and contains no PII patterns from the input.
10. CHANGELOG `[Unreleased]` updated with the `### Added` block above.
11. CC writes a brief PR description; Phoenix opens the PR via `gh pr create`.

---

## Verification before marking done

```bash
# 1. Tests
go test -race -count=1 ./internal/hwdb/... ./internal/calibration/...

# 2. Fuzz (short run, sanity check; CI doesn't gate on fuzz time)
go test -fuzz=FuzzAnonymise -fuzztime=30s ./internal/hwdb/

# 3. Lint
golangci-lint run ./...

# 4. Rulelint
tools/rulelint
grep -rn "rulelint:allow-orphan" .claude/rules/hwdb-capture-*.md
# Expected: empty.

# 5. Build
CGO_ENABLED=0 go build -tags netgo -ldflags '-s -w' ./cmd/ventd

# 6. Smoke test on dev VM (manual; CC reports the steps Phoenix should run)
sudo ./ventd --calibrate
ls -la /var/lib/ventd/profiles-pending/
# Expected: at least one file matching <16-hex>.yaml, mode 0640.
cat /var/lib/ventd/profiles-pending/*.yaml | grep -iE "hostname|mac|/home/" | head -5
# Expected: no plaintext PII (only redacted tokens or absent).

# 7. Diff sanity
git diff --stat main..HEAD
# Expected: changes in internal/hwdb/, internal/calibration/, cmd/ventd/,
# docs/hwdb-schema.md, CHANGELOG.md, .claude/rules/hwdb-capture-*.md.
# No changes in cmd/ventd-ui/ or internal/diag/redactor/ (reuse, not modify).
```

---

## Conventional commits at boundaries (suggested)

- `feat(hwdb): capture pipeline writes pending profiles after calibration`
- `feat(hwdb): anonymise reuses internal/diag/redactor primitives`
- `feat(calibration): post-calibration hook fires capture pipeline`
- `feat(ventd): provision /var/lib/ventd/profiles-pending at startup`
- `test(hwdb): RULE-HWDB-CAPTURE-01..03 invariant bindings`
- `test(hwdb): FuzzAnonymise with 100-sample PII corpus`
- `docs(hwdb): document capture pipeline workflow`

---

## Explicit non-goals

- No submission feature (`ventd --submit-profile`). That's P5-PROF-03, future spec.
- No web UI for reviewing pending profiles. Future spec.
- No automatic merge into the live catalog. Pending profiles stay pending until explicit user action.
- No new redactor primitives. PR 2c shipped P1–P9; if profile-class data needs primitives that don't exist, file an issue and revisit.
- No GPG / encryption of pending files. They're local-mode files.
- No network call of any kind. Repeat: NO NETWORK.
- No deduplication / overwrite policy beyond "same fingerprint hash → overwrite latest". User-visible diff/merge UI is future scope.

---

## Open issues to file (separate from this PR)

PR commit body mentions these for Phoenix to file via `gh issue create`:

- "P5-PROF-03: design `ventd --submit-profile` workflow (manual or automated submission of pending profiles to upstream catalog)" — `area/profile`, `priority/p3`, `spec-03`
- "Web UI: pending profiles review surface" — `area/web`, `priority/p3`, `spec-12`
- "Capture pipeline: pending profile diff vs live catalog entry, surface differences to user" — `area/profile`, `priority/p3`

---

## Token-cost expectation

Sonnet, single CC session. Most of the work is mechanical because PR 2c already shipped the redactor framework:
- ~150 LOC `capture.go`
- ~100 LOC `anonymise.go` (mostly wiring redactor primitives)
- ~250 LOC `capture_test.go` (3 RULE-HWDB-CAPTURE-* subtests + table-driven cases)
- ~80 LOC `anonymise_test.go`
- ~80 LOC `anonymise_fuzz_test.go`
- ~30 LOC startup directory creation in `cmd/ventd/main.go`
- ~50 LOC post-calibration hook in `internal/calibration/store.go`
- 100 fuzz seed YAML files (auto-generated; CC writes the generator + 5 hand-curated edge cases)
- ~50 lines `.claude/rules/hwdb-capture-{01..03}.md`
- ~80 lines `docs/hwdb-schema.md` capture section

Estimate **$12–20**.

If the redactor API turns out not to expose a clean `Pipeline` constructor and CC has to wire each primitive individually, add **+$3-5**. Pre-flight check: `grep -n "func.*Pipeline\|type Pipeline" internal/diag/redactor/*.go` before starting.
