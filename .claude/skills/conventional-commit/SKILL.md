---
name: conventional-commit
description: Stage changes, produce a Conventional Commits message, update CHANGELOG.md Unreleased section, and create the commit. Use when the user says "commit", "make a commit", or invokes this skill explicitly.
disable-model-invocation: true
argument-hint: [optional scope hint]
allowed-tools: Bash(git add *) Bash(git status *) Bash(git diff *) Bash(git commit *) Bash(git log *) Read Edit
---

# Conventional commit

User-invoked only. Never run automatically.

## Preflight

1. git status --short — confirm changes exist.
2. git diff --cached --stat and git diff --stat — understand scope.
3. If nothing staged, ask: stage all, stage selectively, or abort.

## Message format

Use the standard Conventional Commits structure:

- type: feat, fix, refactor, perf, test, docs, build, ci, chore, revert
- scope: package under internal/ or cmd/ (e.g. daemon, config, cmd/ventd); omit if cross-cutting
- subject: imperative, max 72 chars, no trailing period, lowercase first letter
- body: wrap at 72 cols; explain why, not what
- footer: BREAKING CHANGE, Refs #N, Fixes #N

## CHANGELOG update

Before committing, edit CHANGELOG.md:

1. Ensure the Unreleased section exists at top.
2. Map type to subsection:
   - feat goes under Added
   - fix goes under Fixed
   - refactor or perf go under Changed
   - BREAKING changes go under Changed with a BREAKING prefix
   - chore, test, docs, build, ci do not touch CHANGELOG
3. Append a user-facing bullet (rewrite the subject for users).
4. git add CHANGELOG.md.

## Commit

Compose the message and commit via git commit -m. Use a heredoc for multi-line messages.

## Post

Print commit SHA and subject. Do not push.

## Guardrails

- Never amend unless asked.
- Never git add -A in repos with untracked files the user has not seen.
- If diff touches more than 10 files across unrelated scopes, suggest splitting first.
- Check .claude/rules/ for relevant invariants before committing HAL or controller changes.
