---
name: ci-triage
description: |
  Use whenever a GitHub Actions run fails — for a PR, a tag push, or a
  branch CI run. Use whenever the user says "the run failed", "CI is
  red", "PR checks failing", "release pipeline broken", or asks "why is
  the workflow failing". Also use after pushing a commit when the user
  expects to verify CI has gone green and finds it has not. Triggers on
  any reference to a workflow run id, a `gh run` command, or a failed
  release workflow. The skill produces a single bounded triage report
  by running scripts/triage-run.sh and pasting its full output. Do NOT
  use for local-only failures (test failures on the dev box, build
  errors, lint warnings) — use ci-verify-local for those.
---

# ci-triage

When a CI run fails, do NOT iterate through `gh run view`, `gh run view
--log-failed`, and `gh run view --json jobs` separately. The repo ships
a single-shot triage script that produces all the context needed in one
pass.

## Workflow

### Step 1 — locate the failed run

If the user named a run id, use it. Otherwise auto-detect from the
current branch.

```bash
cd "$REPO_ROOT"
./scripts/triage-run.sh                # latest failed run on current branch
./scripts/triage-run.sh <run-id>       # specific run
./scripts/triage-run.sh --pr <num>     # PR's head branch
./scripts/triage-run.sh --tag v0.4.1   # tag push (release pipelines)
```

### Step 2 — emit output verbatim

The script's output is the diagnostic. Do NOT summarise it before the
user has seen it. Print the full stdout exactly as the script produced
it, then offer interpretation.

This is non-negotiable. The output is bounded (~100 lines), structured
into named sections, and designed to be pasted whole into a Claude.ai
chat for triage. Summarising it pre-emptively destroys the information
density the script was built to produce.

### Step 3 — interpret

After the verbatim output, identify the failure class. The script's
HINTS section will pre-flag known classes. Common shapes:

- **Aggregator-only red.** All upstream jobs green, only `provenance /
  final` or similar outcome job red. The script's hint flags this. If
  the release-state section confirms full asset count, this is COSMETIC.
  Do NOT recommend retagging or rerolling.

- **Stale path reference after `git mv`.** Log shows "not found,
  skipping". Run `grep -rn '<old-name>' .github/ scripts/ deploy/` to
  find every reference, fix in one commit. The most common cause of
  release-pipeline post-rename failures.

- **SBOM/SLSA spec drift.** Generator auto-bumped (CycloneDX 1.5→1.6,
  SPDX 2.3→3.0). The validator gate is pinned. Update the gate to
  accept the new version; do not pin the generator.

- **Permissions error.** `Resource not accessible`, 403, or
  permission-denied in the log. Workflow YAML is missing
  `permissions: { contents: write, id-token: write }` on the calling
  job.

- **Setup failure (no log).** Job conclusion is `failure` but the
  failed-step log is empty. Job died during runner provisioning, image
  pull, or checkout. Re-run the job; if it persists, check the runner
  pool status.

### Step 4 — propose a fix, do not apply unilaterally

State the diagnosis and the proposed fix. Wait for user confirmation
before editing files. The triage script is read-only by design.

## What this skill does NOT do

- Does not retag or push tags. Tag discipline is the user's call.
- Does not delete failed runs. Cosmetic failures stay in the run
  history as evidence.
- Does not edit workflow files without explicit user instruction.
- Does not iteratively fetch more logs. The script's output is the
  diagnostic. If it is insufficient, fix the script, do not iterate.

## When to escalate to chat

If the failure shape is not one of the known classes above and the
script's HINTS section did not match any pattern, paste the full output
to Claude.ai and ask for triage. Do not guess.
