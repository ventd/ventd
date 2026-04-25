---
name: ventd-preflight
description: Use BEFORE creating any PR or pushing any tag in the ventd repo. Runs .claude/scripts/preflight.sh with the active spec ID to validate: correct remote, on a feature branch, clean tree, spec committed, rulelint green, no stale PRs. Blocks on FAIL, surfaces WARN. Triggers on intents like "ready to merge", "open a PR", "push the tag", "cut a release".
---

# ventd-preflight

Validates the ventd repository state before creating a PR or pushing a tag.

## Usage

Run this skill before:
- Opening a PR
- Pushing a branch
- Tagging a release
- Any `git push` operation

Example invocation:
```bash
bash .claude/scripts/preflight.sh spec-03
```

## Checks performed

1. **Remote** — confirms `origin` is `git@github.com:ventd/ventd.git`
2. **Branch** — confirms not on `main` (feature branch required)
3. **Tree** — warns if working tree has uncommitted changes
4. **Spec** — confirms `specs/<spec-id>.md` is committed
5. **rulelint** — confirms zero rule violations
6. **PRs** — displays any open PRs for the current branch

## Exit codes

- **0** — all checks passed
- **1** — one or more FAIL checks (do not proceed)
- **2** — WARN checks only, FAILs are empty (advisory, safe to continue)

## Failure remediation

| Check | Failure | Fix |
|-------|---------|-----|
| remote | wrong remote | `git remote -v` and verify `origin` URL |
| branch | on main | `git checkout -b <feature-branch> origin/main` |
| spec | not committed | `git add specs/<spec-id>.md && git commit` |
| rulelint | violations | `tools/rulelint` to see details, fix rule bindings |
| tree | dirty | `git add` and commit staged changes, or `git stash` |

## When to run

Always run before:
- `git push` to open or update a PR
- `git tag <version>` to cut a release
- Merging a PR (as final safety check)
