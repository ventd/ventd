# ventd Preflight Validation

One-page reference for the ventd preflight script. Use this before opening a PR or pushing any branch.

## What it does

The preflight script validates that your repository state is ready for a PR or tag push. It checks:
- Correct GitHub remote (`ventd/ventd`)
- On a feature branch (not `main`)
- Working tree is clean
- Spec file is committed
- Rule lint passes
- No conflicting PRs

## When to run

Run before any of these intents:
- Opening a PR
- Pushing a branch
- Merging a PR (final safety check)
- Cutting a release
- Creating a git tag

## Sample output (passing run)

```
ventd preflight — spec=spec-03
============================================================
Recent tags:
  v0.4.1
  v0.4.0
  v0.3.1
  v0.3.0
  v0.2.0

PRs for this branch:
  (none or gh error)

Summary:
  remote: ✓
  branch: ✓ (feat/spec-03-pr1-schema)
  tree: ✓
  spec: ✓
  rulelint: ✓
============================================================
```

## Exit codes

| Code | Meaning | Action |
|------|---------|--------|
| 0 | All checks passed | Safe to proceed with PR or tag |
| 1 | FAIL checks exist | Fix issues before proceeding |
| 2 | WARN checks only | Advisory; safe to continue |

## Exit code 1 failures (do not proceed)

| Check | Problem | Fix |
|-------|---------|-----|
| remote | Wrong GitHub remote | Run `git remote -v` and verify `origin` is `git@github.com:ventd/ventd.git` |
| branch | On `main` branch | `git checkout -b <name> origin/main` and commit your changes on the new branch |
| spec | Spec file not committed | `git add specs/<spec-id>.md && git commit` |
| rulelint | Rule binding errors | Run `tools/rulelint` to see details; fix .claude/rules/ bindings |

## Exit code 2 warnings (advisory)

| Check | Problem | Action |
|-------|---------|--------|
| tree | Working tree has uncommitted changes | Decide: `git add && git commit`, or `git stash` and re-run |

## Usage

```bash
.claude/scripts/preflight.sh <spec-id>
```

Example:
```bash
.claude/scripts/preflight.sh spec-03
```

The spec ID is auto-detected from your branch name if available (e.g., `feat/spec-03-...` extracts `spec-03`). Supply it explicitly if your branch name doesn't contain one.

## Integration with release workflow

preflight is also run by `release-validate.sh` before cutting a release, so you'll see its checks again as part of release validation. This is intentional — releases require the strictest validation.
