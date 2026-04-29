# Release Process

This document covers the release workflow: pre-release validation, tag cut,
and post-tag audit gates. All dispatches are `workflow_dispatch` — releases
are not calendar-driven.

## Pre-release validation

Before cutting a tag, fire `.github/workflows/pre-release-check.yml` against
the tag-candidate SHA. It must return green before the tag is pushed. Gates:

- `govulncheck` on the candidate tree.
- `CHANGELOG.md` has a non-empty `## [Unreleased]` block.
- No open issues with the `release-blocker` label.
- Full `go build` + `go test -race ./...`.

## Tag cut

Tags follow semver: `vMAJOR.MINOR.PATCH`. Push the tag to `main`. The
`release.yml` workflow builds the artifacts, publishes the GitHub Release,
and attaches `checksums.txt` + SBOMs (when Phase 10 artifacts land).

## Post-tag audit

Fire `.github/workflows/drew-audit.yml` after the tag lands on `main`. This
is `workflow_dispatch`-only and runs four gates. Results are written to the
workflow step summary.

### Gate status

| gate | status | activates when |
|------|--------|----------------|
| Gate 1 — govulncheck | **REAL** — hard-fails on CRITICAL, warns on HIGH | live now |
| Gate 2 — SBOM validate | STUB (`[SKIP]`) | P10-SBOM-01 merges |
| Gate 3 — cosign verify | STUB (`[SKIP]`) | P10-SIGN-01 merges |
| Gate 4 — repro-build diff | STUB (`[SKIP]`) | P10-REPRO-01 merges |

Each Phase 10 task merge is followed by a small, isolated diff scoped to
activating one gate — replace the `[SKIP]` stub with the real gate
implementation.

### Gate 1 detail — govulncheck

- Checks out the specified tag (or HEAD) and runs `govulncheck -json ./...`.
- Parses the JSON stream, counting reachable vulnerabilities per severity
  bucket (CRITICAL / HIGH / MEDIUM / LOW) via the OSV record's
  `database_specific.severity`.
- Writes a `| severity | count |` table plus a PASS/WARN/FAIL verdict to the
  step summary.
- **Hard-fails** the job if any CRITICAL vulnerability is reachable.
- **Warns** (`::warning::`) and continues if any HIGH is reachable.
- PASS if no reachable vulnerabilities are found.

### Verdicts

The overall verdict in the summary reflects the worst gate outcome:

- `PASS` — Gate 1 passed, all stubs skipped.
- `PARTIAL` — Gate 1 warned (HIGH vuln), stubs skipped.
- `FAIL` — Gate 1 failed (CRITICAL vuln) or internal error.

## Version cadence

Releases are not calendar-driven. A release is due when:

- A Phase boundary closes, or
- A security-critical fix merges and needs shipping, or
- ~2 weeks have elapsed and the `## [Unreleased]` block has a coherent
  user-facing story.

If `[Unreleased]` is thin or dominated by infrastructure churn, defer the
release.
