# CC prompt — P1-FP-01 (fp)

This is the full specification for P1-FP-01 as dispatched via the `fp` alias. Preserved on cowork/state for respinability after the original session (#246) completed and escalated.

## Task
ID: P1-FP-01
Track: FP
Goal: replace the 3-entry DMI substring table in `knownDriverNeeds` with a fingerprint-keyed YAML database under `internal/hwdb`.

## Context you should read first
- `internal/hwmon/autoload.go`
- `internal/hwmon/dmi.go`
- `internal/hwmon/install.go`
- `ventdmasterplan.mkd` §8 P1-FP-01 entry
- The FP track intent in masterplan §1 differentiator #6 ("profile-sourced")

## What to do
1. Create `internal/hwdb/hwdb.go` with `HardwareFingerprint` struct (DMI board vendor/name/version, product family, chip hex-ID, PCI subsystem, CPU microcode).
2. Define `Profile` struct containing `Match HardwareFingerprint` plus `Modules []string`, `Notes string`, `Unverified bool`.
3. Create `internal/hwdb/profiles.yaml` loaded via `go:embed`. Include the current 3 entries from `knownDriverNeeds` DMI triggers (MSI MAG, MPG, Gigabyte wildcard), plus 15–25 additional known boards.
4. Implement `hwdb.Match(fp)` with resolution order: exact → prefix → wildcard (substring).
5. Modify `internal/hwmon/autoload.go` to call `hwdb.Match` between the fast-path and sensors-detect stages. On match, try the profile's modules; on miss, emit a structured `hwdb_miss` log event.
6. Document the resolution order in the package doc of `internal/hwdb/hwdb.go`.
7. Delete the DMI-trigger portion of `knownDriverNeeds` (leave install metadata if it lives elsewhere in the map).
8. Add CHANGELOG entry under Unreleased/Added.

## Definition of done
- ≥ 18 entries in profiles.yaml.
- Resolution order documented.
- `go build ./...` + `go vet ./...` clean.
- `go test -race ./internal/hwmon/... ./internal/hwdb/...` passes.
- Smoke-probe on a no-DMI fixture logs `hwdb_miss` and falls through to sensors-detect without panic.
- PR draft, title `feat: fingerprint-keyed hwdb replaces substring table (P1-FP-01)`.

## Out of scope
- Remote refresh from `ventd/hardware-profiles` (that is P1-FP-02).
- Tests for `internal/hwdb/**` (T-FP-01 owns those).
- Refactoring `install.go` to consume `hwdb` directly.

## Branch and PR
- Branch: claude/fingerprint-database-yaml-<rand5>
- Commit style: conventional commits
- Draft PR with files-touched list, verification section, link to P1-FP-01.

## Constraints
- Do not touch files outside: `internal/hwdb/**`, `internal/hwmon/autoload.go`, `CHANGELOG.md`.
- `gopkg.in/yaml.v3` is already a direct dep; using it is fine. No other new deps.

## Reporting
STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.

## Model
Opus 4.7 (FP is in the Opus list per Cowork SYSTEM prompt).
