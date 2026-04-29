---
name: ci-triage
description: |
  Use whenever a GitHub Actions run fails — for a PR, tag push, or
  branch CI run. Triggers on: "the run failed", "CI is red", "PR
  checks failing", "release pipeline broken", "why is the workflow
  failing", any reference to a workflow run id, `gh run` command, or
  failed release workflow. Also use after pushing a commit when the
  user expected CI green and finds it red. Produces a single bounded
  triage report by running scripts/triage-run.sh and pasting full
  output. Do NOT use for: local-only failures (use ci-verify-local),
  pre-push state checks (use ventd-preflight), or release-tag
  validation (use ventd-release-validate).
---

# ci-triage

When CI fails, do NOT iterate through `gh run view`, `gh run view
--log-failed`, and `gh run view --json jobs` separately. The repo
ships a single-shot triage script that produces all needed context in
one pass.

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Current branch: !`git branch --show-current`

Most recent runs: !`gh run list --limit 5 --json status,conclusion,name,headBranch,createdAt 2>/dev/null | head -30`

## Run

```bash
./scripts/triage-run.sh                # latest failed run on current branch
./scripts/triage-run.sh <run-id>       # specific run
./scripts/triage-run.sh --pr <num>     # PR's head branch
./scripts/triage-run.sh --tag v0.4.1   # tag push (release pipelines)
```

## Emit output verbatim

The script's output IS the diagnostic. Print full stdout exactly as
produced, then offer interpretation.

This is non-negotiable. Output is bounded (~100 lines), structured
into named sections, designed to be pasted whole into Claude.ai chat
for triage. Summarising pre-emptively destroys the information
density the script was built to produce.

## Failure-class catalog (gotchas)

The script's HINTS section pre-flags these. If the hint matched,
trust it. If empty, compare manually:

- **Aggregator-only red.** All upstream jobs green, only `provenance
  / final` or similar outcome job red. If release-state section
  confirms full asset count, this is COSMETIC. Do NOT recommend
  retagging or rerolling. Log in `docs/claude/spec-cost-calibration.md`
  and move on.

- **Stale path reference after `git mv`.** Log shows "not found,
  skipping". Run
  `grep -rn '<old-name>' .github/ scripts/ deploy/`
  to find every reference; fix in one commit. Most common cause of
  release-pipeline failures post-rename.

- **SBOM/SLSA spec drift.** Generator auto-bumped (CycloneDX 1.5→1.6,
  SPDX 2.3→3.0). The validator gate is pinned. Update the gate to
  accept the new version; do NOT pin the generator (it'll bump again).

- **Permissions error.** `Resource not accessible`, 403, or
  permission-denied in log. Workflow YAML is missing
  `permissions: { contents: write, id-token: write }` on the calling
  job.

- **Setup failure (no log).** Job conclusion is `failure` but the
  failed-step log is empty. Job died during runner provisioning,
  image pull, or checkout. Re-run the job; if it persists, check
  runner pool status.

- **Tag delete doesn't unfire workflows.** Pushing a tag immediately
  dispatches release.yml; deleting the tag does NOT cancel in-flight
  runs. Use `gh run cancel <run-id>` before retagging.

- **`gh pr list` hides merged PRs.** If looking up a run by PR number
  and gh shows nothing, the PR may have squash-merged. Use
  `gh pr view <num> --json state` to confirm.

## Constraints

- Read-only. Never edit workflow files without explicit user instruction.
- Never retag, push tags, or delete failed runs. Tag discipline is the
  user's call. Cosmetic failures stay in run history as evidence.
- Never iteratively fetch more logs. The script's output is the
  diagnostic. If insufficient, fix the script — don't iterate.
- Propose fixes; do not apply unilaterally.

## Escalation

If the failure shape is unknown and HINTS didn't match, paste full
output to Claude.ai chat and ask for triage. Don't guess — the
guess-loop wastes more tokens than the chat round-trip.

## Out of scope

- Editing workflow YAML
- Pushing or deleting tags
- Re-running CI (use `gh run rerun` directly)
- Diagnosing local-only failures
