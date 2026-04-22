# Release Process

This document covers the post-tag audit workflow and Drew's MCP invocation path.

## drew-audit.yml — post-tag audit gates

`drew-audit.yml` is a `workflow_dispatch`-only workflow that Drew fires after each
release tag is confirmed on `main`. It runs four audit gates and writes a step
summary that Drew consumes to determine whether the release artifacts are compliant.

### Triggering

Drew fires the workflow from the claude.ai Project environment via MCP:

```
actions_run_trigger(
    workflow_id="drew-audit.yml",
    inputs={"tag": "v0.3.0"}
)
```

Results are consumed via `get_job_logs` and the workflow's step summary link.
Pass `inputs={}` (or omit `tag`) to audit the current `main` HEAD.

### Gate status

| gate | status | activates when |
|------|--------|----------------|
| Gate 1 — govulncheck | **REAL** — hard-fails on CRITICAL, warns on HIGH | n/a — live now |
| Gate 2 — SBOM validate | STUB (`[SKIP]`) | #322 (P10-SBOM-01) merges |
| Gate 3 — cosign verify | STUB (`[SKIP]`) | #323 (P10-SIGN-01) merges |
| Gate 4 — repro-build diff | STUB (`[SKIP]`) | #324 (P10-REPRO-01) merges |

Each Phase 10 task merge is followed by a Drew-filed follow-up issue that replaces
the corresponding `[SKIP]` stub with the real gate implementation. Each follow-up is
a small, isolated diff scoped to activating one gate.

### Gate 1 detail — govulncheck

- Checks out the specified tag (or HEAD) and runs `govulncheck -json ./...`.
- Parses the JSON stream: counts reachable vulnerabilities by severity bucket
  (CRITICAL / HIGH / MEDIUM / LOW) using the OSV record's `database_specific.severity`.
- Writes a `| severity | count |` table plus a PASS/WARN/FAIL verdict to the step summary.
- **Hard-fails** the job if any CRITICAL vulnerability is reachable.
- **Warns** (`::warning::`) and continues if any HIGH is reachable.
- PASS if no reachable vulnerabilities are found.

### Verdicts

The overall verdict in the summary reflects the worst gate outcome:

- `PASS` — Gate 1 passed, all stubs skipped.
- `PARTIAL` — Gate 1 warned (HIGH vuln), stubs skipped.
- `FAIL` — Gate 1 failed (CRITICAL vuln) or internal error.

## Pre-release validation

Before cutting a tag, Drew fires `pre-release-check.yml` (RELEASE-CHECK-01, #328)
against the tag-candidate SHA and requires a green result before filing the tag-cut
`role:atlas` dispatch issue.

## Version cadence

Releases are not calendar-driven. A release is due when:
- A Phase boundary closes, or
- A security-critical fix merges and needs shipping, or
- ~2 weeks have elapsed and the `## [Unreleased]` block has a coherent user-facing story.

If `[Unreleased]` is thin or dominated by infrastructure churn, Drew defers the release.
