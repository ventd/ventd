---
name: ci-verify-local
description: |
  Use before pushing a branch to verify local state matches expectations.
  Triggers when the user says "verify before push", "is the branch
  ready", "run the pre-flight", "check if everything's green locally",
  or after a CC session finishes its work and the user wants to confirm
  before pushing. Also use as the final step of any PR-producing
  workflow — when CC has finished editing files, this is the gate
  before `git push`. Do NOT use for diagnosing CI failures (use
  ci-triage for that). Do NOT use for ongoing CC work, only for
  pre-push verification.
---

# ci-verify-local

Before pushing a branch, run `scripts/verify-local.sh`. It produces a
bounded report covering:

1. Tree state (`git status` short)
2. Commit count + log since target branch (default: main)
3. `go test -race ./...` result
4. `tools/rulelint` result
5. Optional path-existence checks for files mentioned in the spec
6. Drift detection — finds stale references to recently-renamed files

The script always runs from the repo root and uses `--no-pager` for git
commands. Two of the most common token-wasters in mobile terminals
(running from `~` instead of `~/repo`, and pager swallowing piped
commands) are eliminated.

## Workflow

### Step 1 — run the script

```bash
cd "$REPO_ROOT"
./scripts/verify-local.sh                                    # default
./scripts/verify-local.sh --against develop                  # different base
./scripts/verify-local.sh --skip-tests                       # faster
./scripts/verify-local.sh --paths 'TESTING.md|deploy/apparmor.d/ventd'
                                                             # explicit checks
```

### Step 2 — read the SUMMARY block

The script ends with a SUMMARY block:

```
  tree:    clean | DIRTY
  commits: <N> ahead of <base>
  drift:   none detected | STALE REFERENCES FOUND
```

If all three are green (clean / >0 commits / none), proceed to push.

If any are red, do NOT push. Address the issue first:

- **DIRTY tree** → uncommitted changes. Either commit them with a
  conventional-commit message or stash them.
- **0 commits ahead** → branch has nothing to push. Wrong branch?
- **STALE REFERENCES FOUND** → drift detection caught a `git mv` that
  left old-name references somewhere. Fix them in this PR or split
  into a follow-up.

### Step 3 — push

```bash
git push -u origin "$(git branch --show-current)"
```

## Why this exists

This skill exists because the CC verification loop on a mobile terminal
is fragile. The pre-existing pattern of pasting a 10-line bash block
into Termius hit two failure modes regularly:

1. The block got pasted from `~` instead of `~/ventd`. Every command
   failed with "not a git repository". User screenshots → Claude.ai
   round-trip → "run from the right directory".
2. A `git log` or `git show` in the middle of the block hit the pager,
   and the rest of the queued commands disappeared into the terminal
   buffer. User saw partial output, Claude.ai saw partial output.

`verify-local.sh` always cds to repo root first and always passes
`--no-pager` to git. Both classes of mistake are eliminated.

## What this skill does NOT do

- Does not push. The push command is offered but not executed.
- Does not edit files. Read-only.
- Does not run integration tests against remote infrastructure (HIL
  VMs, staging, etc).
- Does not validate workflow YAML syntax. `python3 -c "import yaml"`
  is a separate one-liner — add as a path check if needed.
