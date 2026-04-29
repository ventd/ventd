---
name: ci-verify-local
description: |
  Use BEFORE pushing a branch to verify local state matches
  expectations. Triggers on: "verify before push", "is the branch
  ready", "run the pre-flight", "check if everything's green
  locally", or after a CC session finishes work and the user wants to
  confirm before pushing. Also use as the final step of any
  PR-producing workflow — when CC finishes editing files, this is the
  gate before `git push`. Do NOT use for: diagnosing CI failures
  (ci-triage), structural-only checks before tag push
  (ventd-preflight), or ongoing CC work mid-session.
---

# ci-verify-local

Bounded pre-push report. Read-only. Eliminates the two most common
mobile-terminal failure modes (wrong directory, pager-swallowed output).

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Branch: !`git branch --show-current`

Tree: !`git status --short | head -10`

Commits ahead of main: !`git rev-list --count main..HEAD 2>/dev/null`

## Run

```bash
./scripts/verify-local.sh                                     # default
./scripts/verify-local.sh --against develop                   # different base
./scripts/verify-local.sh --skip-tests                        # faster
./scripts/verify-local.sh --paths 'TESTING.md|deploy/apparmor.d/ventd'
                                                              # explicit checks
```

What the script reports:
1. Tree state (`git status` short)
2. Commit count + log since target branch
3. `go test -race ./...` result
4. `tools/rulelint` result
5. Optional path-existence checks
6. Drift detection — finds stale references to recently-renamed files

## Read the SUMMARY block

```
tree:    clean | DIRTY
commits: <N> ahead of <base>
drift:   none detected | STALE REFERENCES FOUND
```

All green → push. Anything red → do NOT push. Address first:

- **DIRTY tree** → uncommitted changes. Commit with conventional-commit
  skill or stash.
- **0 commits ahead** → branch has nothing to push. Wrong branch?
- **STALE REFERENCES FOUND** → drift detection caught a `git mv` that
  left old-name references. Fix in this PR or split into follow-up.

## Push (manual)

```bash
git push -u origin "$(git branch --show-current)"
```

## Gotchas (real failure modes)

- **Running from `~` instead of `~/ventd`.** Every git command fails
  with "not a git repository". The script always cds to repo root
  first — but if you replace it with inline bash, you lose this.
- **Pager swallowing piped commands.** A `git log` or `git show` in
  the middle of a multi-command block hits the pager, queued
  commands disappear into the terminal buffer. The script always
  passes `--no-pager`. Same warning for inline replacements.
- **`go test -race` flakes on the scheduler test.** Known issue
  (spec-07). If only that test fails and only intermittently, retry
  once. If it fails twice, it's a real regression.
- **Drift detection isn't perfect.** It catches `git mv` references
  in source, configs, and workflows. It does NOT catch references
  in vendored docs, generated files, or markdown link targets. A
  clean drift report doesn't prove zero drift; a dirty one is
  always real.
- **`--skip-tests` is for iteration, not pre-push.** Skipping tests
  before push means pushing untested code. Use only when you've
  already run them in this session and know nothing has changed.
- **WSL clock drift breaks `git status` mtimes.** If status shows
  every file as modified after WSL hibernate, run
  `wsl --shutdown` and reopen. Not a verify-local bug.

## Constraints

- Read-only. Does not push, does not edit.
- Does not run integration tests against remote infrastructure (HIL
  VMs, staging).
- Does not validate workflow YAML syntax (`python3 -c "import yaml,
  sys; yaml.safe_load(open(sys.argv[1]))"` is a separate one-liner).

## Out of scope

- Pushing
- Pre-tag release validation (use ventd-release-validate)
- Diagnosing CI failures (use ci-triage)
- Hardware-in-the-loop tests
