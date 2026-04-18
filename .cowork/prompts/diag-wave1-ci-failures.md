# diag-wave1-ci-failures

You are Claude Code. Three Phase 2 Wave 1 PRs failed CI with the same
two failing checks: `build-and-test-ubuntu` and `golangci-lint`. All
three PRs (#281 P2-USB-BASE, #282 P2-CROSEC-01, #285 P2-IPMI-01)
dispatch after the go-toolchain bump to 1.25.9. #277 (P2-PWMSYS-01)
has ZERO check runs — its branch is ~60 commits behind main.

Your job: diagnose the root cause and report back. Do NOT fix anything.
Do NOT open any PR.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main
git pull origin main
```

## Steps

1. For each failing PR, fetch the raw failure log:
   ```
   gh pr checks 281 --json name,state,link
   gh run view <run-id-of-build-and-test-ubuntu> --log | tail -200
   gh run view <run-id-of-golangci-lint> --log | tail -100
   ```
   Repeat for #282 and #285.

2. Compare failure signatures across the three PRs. Specifically check:
   - Is the failure mode identical (same test name, same lint rule, same compile error)?
   - Does each failure point at code the PR itself introduced, or at pre-existing code?
   - Is golangci-lint version v2.1.6 compatible with go 1.25.9 semantics?

3. For #277 (P2-PWMSYS-01), check branch staleness:
   ```
   gh pr view 277 --json headRefOid,baseRefOid
   git log --oneline main ^<head-sha> | head
   git log --oneline <head-sha> ^main | head
   ```
   Is CI actually not triggering, or just queued behind a backlog?

4. For each root cause, determine scope:
   - Is it a per-PR bug (specific to that PR's code)?
   - Is it a pre-existing main-branch bug that surfaces when these PRs rebase?
   - Is it a toolchain mismatch (go 1.25.9 vs golangci-lint v2.1.6)?

## Reporting

STATUS: done | partial | blocked
ROOT_CAUSE_281: <one sentence>
ROOT_CAUSE_282: <one sentence>
ROOT_CAUSE_285: <one sentence>
ROOT_CAUSE_277_NO_CI: <one sentence explaining why CI didn't run>
COMMON_PATTERN: <one sentence — are these the same bug, or three different bugs?>
FIX_STRATEGY: <one paragraph — is this one rebase-on-main fix, three separate fixes, or a toolchain update in a follow-up PR?>
BLOCKER_SEVERITY: low | medium | high

## Time budget

10 minutes wall-clock. This is diagnostic only, not a fix.

## Out of scope

- Do NOT edit any code.
- Do NOT open any PRs.
- Do NOT bump golangci-lint.
- Do NOT rebase any of the failing PRs.
