---
name: conventional-commit
description: |
  Use when the user says "commit", "make a commit", "write a commit
  message", or invokes this skill explicitly. Drafts a Conventional
  Commits message, updates the CHANGELOG.md Unreleased section if the
  type warrants it, and creates the commit. Do NOT use for: pushing,
  amending past commits, or rewriting history — those are explicit user
  decisions, not commit-skill territory. Do NOT auto-trigger; this
  skill is user-invoked only (disable-model-invocation: true).
disable-model-invocation: true
argument-hint: [optional scope hint]
allowed-tools: Bash(git add *) Bash(git status *) Bash(git diff *) Bash(git commit *) Bash(git log *) Read Edit
---

# conventional-commit

User-invoked. Never auto-trigger.

## Current tree state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Working tree status: !`git status --short`

Staged diff stat: !`git diff --cached --stat`

Unstaged diff stat: !`git diff --stat`

Recent commits for tone reference: !`git log --oneline -5`

## Goal

Produce one Conventional Commits commit that reflects what the user
actually changed. Update CHANGELOG only when type warrants it. Do not
push, do not amend, do not rewrite.

## Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

- **type**: feat, fix, refactor, perf, test, docs, build, ci, chore, revert
- **scope**: package under `internal/` or `cmd/` (e.g. `daemon`, `config`,
  `cmd/ventd`); omit for cross-cutting changes
- **subject**: imperative, ≤72 chars, lowercase first letter, no trailing period
- **body**: wrap at 72; explain *why*, not *what*
- **footer**: `BREAKING CHANGE:`, `Refs #N`, `Fixes #N`

## CHANGELOG mapping

| commit type | CHANGELOG section |
|---|---|
| feat | Added |
| fix | Fixed |
| refactor, perf | Changed |
| BREAKING (any type) | Changed, prefixed with `BREAKING:` |
| test, docs, build, ci, chore, revert | no CHANGELOG entry |

When CHANGELOG is touched, `git add CHANGELOG.md` before the commit so
both land in the same commit.

## Gotchas (real failure modes from this repo)

- **Never `git add -A` blindly.** Untracked files the user hasn't seen
  (CC scratchpads, `.worktrees/` artifacts, `/tmp` symlinks) end up
  committed. Always show `git status` first; let the user pick.
- **Never amend unless explicitly asked.** Amend rewrites SHA, breaks
  any in-flight PR review, and silently loses work if the previous
  commit had a co-author trailer.
- **No `Co-Authored-By: Claude` trailer.** `.claude/rules/attribution.md`
  forbids it. CC has injected this automatically in past sessions —
  check the message before committing.
- **Never commit on `main`.** Branch protection requires PR + 17 status
  checks. Admin bypass has fired accidentally before; the rule is to
  not invoke admin bypass at all. If on `main`, abort and ask.
- **Diff touching `.claude/rules/*.md` requires rulelint.** Run
  `go run ./tools/rulelint -root .` before committing or the PR fails
  CI. The ventd-rulelint skill handles this.
- **Subject >72 chars is real, not a guideline.** commitlint enforces
  it in CI. Trim qualifiers first ("for is_pump fans" → "for pump
  fans"), shorten nouns second, never abbreviate type or scope.
- **Cross-scope diff (>10 files across unrelated packages)** = sign the
  commit should be split. Surface this and ask before proceeding.
- **Tag discipline is downstream.** This skill never tags. Tags happen
  after `gh pr merge` succeeds AND `git pull` shows the squash on
  main — handled by ventd-release-validate, not here.

## Constraints (what makes a good commit, not how to type one)

- One logical change per commit. If the diff has two stories, ask to split.
- Body explains motivation; reviewers can read the diff for mechanics.
- Footer is for machines (`Fixes #N`, `BREAKING CHANGE:`) — humans go in body.
- If the change introduces a `## RULE-*` binding, name the bound subtest
  in the body. The rulelint skill validates the binding; the commit
  message tells the human reviewer which test pins it.

## Out of scope

- Pushing the commit (separate user decision; verify-local runs first)
- Amending or interactive-rebasing
- Tagging
- Force-pushing
- Editing past CHANGELOG entries — only the Unreleased section
