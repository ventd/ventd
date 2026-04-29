---
name: ventd-preflight
description: |
  Use BEFORE creating any PR or pushing any tag in the ventd repo.
  Triggers on: "ready to merge", "open a PR", "push the tag", "cut a
  release", "is this ready", "preflight". Runs
  .claude/scripts/preflight.sh against the active spec ID and blocks on
  FAIL, surfaces WARN. Do NOT use for diagnosing CI failures (use
  ci-triage). Do NOT use as a substitute for ci-verify-local — verify
  runs `go test`, preflight does not.
---

# ventd-preflight

Validates repo state before a PR or tag push. Read-only.

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Branch: !`git branch --show-current`

Tree: !`git status --short | head -10`

Remote: !`git remote get-url origin`

## Run

```bash
bash .claude/scripts/preflight.sh <spec-id>
# example: bash .claude/scripts/preflight.sh spec-03
```

Exit 0 = pass. Exit 1 = FAIL (do not proceed). Exit 2 = WARN only (advisory).

The script's output is the diagnostic. Do not summarise it before the
user has seen it. Same rule as ci-triage.

## What it checks (so you know what to fix)

- **remote** — `origin` points to the canonical ventd remote
- **branch** — not on `main` (feature branch required)
- **tree** — warns on uncommitted changes
- **spec** — `specs/<spec-id>.md` is committed
- **rulelint** — zero violations
- **PRs** — surfaces any open PRs for the current branch

## Gotchas (real failure modes)

- **`gh pr list` hides merged PRs.** If preflight reports "no open PR
  for this branch" but you remember opening one, it may have been
  squash-merged. Use `gh pr view --json` to check definitively.
- **Branch protection has 17 status checks.** Preflight does not run
  them — only structural checks. Passing preflight ≠ ready to merge,
  it means ready to *push*.
- **Spec ID must match the file.** `specs/spec-03-amendment-schema-v1_2.md`
  → spec ID is `spec-03-amendment-schema-v1_2`, not `spec-03`. The
  script does literal-path matching; no fuzzy lookup.
- **Stale branches survive locally.** If preflight passes but `git
  branch -a` shows old `feat/foo-bar` branches, clean them with
  `git branch -d <name>` and `git push origin --delete <name>`. Not
  a blocker, but a `git remote -v` + branch sweep before any release
  is a known-good habit.
- **`origin` URL drift after worktree creation.** CC worktrees in
  `.worktrees/` may resolve `origin` differently than the main
  checkout. Always run preflight from the main checkout, not from a
  worktree.
- **WARN ≠ ignore.** WARN exit 2 is advisory but most WARNs are
  pre-failure signals. Read them before pushing.

## When this is the wrong skill

- **Local test failures:** ci-verify-local runs the test suite; this
  doesn't.
- **CI is red after push:** ci-triage diagnoses CI failures; preflight
  is pre-push only.
- **About to push a tag:** ventd-release-validate runs preflight first
  AND adds release-specific checks (cosign format, action pinning,
  CycloneDX version). Use that, not this.

## Out of scope

- Running `go test`, `golangci-lint`, or any build
- Auto-fixing rulelint violations
- Pushing, tagging, opening PRs
