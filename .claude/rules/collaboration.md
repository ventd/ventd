# Collaboration Rules

How Phoenix and Claude sessions work together on this repo. These rules distil what has actually worked across Cowork and Claude Code sessions — follow them and the handoffs stay tight.

## Tone

- Hard truth, scientific evidence, no filler. Skip positive-reinforcement openers ("Great question!", "Absolutely!", etc.).
- Own mistakes plainly. Don't collapse into self-abasement, and don't apologise repeatedly for the same thing.
- State what you're doing, then do it. Don't narrate unnecessary meta-steps.

## Decide, don't defer

- Pick the best technical approach and execute. Never hand Phoenix an A/B choice on technical direction — that wastes his attention.
- If the call is a genuine coin-flip, pick the narrower / safer option and note the tradeoff in the PR body. Phoenix can reverse it in review.
- Escalate only for actions Phoenix has reserved (see "Phoenix-only actions" below).

## Phoenix-only actions

Never take these without an explicit in-session OK from Phoenix:

- `git push --force` to `main`.
- Rotating / regenerating the GitHub PAT.
- Enabling UFW / changing firewall rules on a live host without a dry-run.
- Destructive rig-level actions beyond `reboot` (disk wipe, partition change, BIOS flash).

## Standing delegations (Phoenix has pre-authorised these)

- `git tag` of versions following the established naming scheme (vX.Y.Z[-suffix]).
  Required preconditions: CI green on the merge commit being tagged, ci-local
  sweep passes, CHANGELOG.md updated. Tag messages should summarise the headline
  changes since the previous tag.
- `goreleaser release` triggered by a pushed tag (downstream of `git tag`).

## Merge discipline

- Branch from `origin/main` for every task. Never commit to `main` directly.
- Every commit authored as `phoenixdnb`. Verify with `git config user.name` / `user.email` before the first commit of a session. Fix with repo-scoped config, never global.
- No `--no-verify`, no `--no-gpg-sign`, no `--amend` of someone else's commits.
- When running autonomously (e.g. overnight): open PR → wait for green CI → squash-merge `--delete-branch`. If CI fails, fix forward or open a tracking issue and move on — do not block the queue.
- If a PR has been on CI more than 20 minutes with no completion, file a CI-flake issue and keep moving.

## Attribution

See `.claude/rules/attribution.md` — the short version: no `Co-Authored-By: Claude`, no "Generated with" footers, no mentions of AI / LLM / agent anywhere in commits, PRs, issues, or docs. This repo ships under a single human author.

## Issue filing

- File issues for every surprise worth remembering. "Surprise" = non-obvious behaviour, false positive, flake, missing coverage, wish-list item.
- Title is a single line, imperative or descriptive. Body includes: summary, evidence, proposed fix, impact, source (the PR or run that surfaced it).
- Link related issues. Close issues via `Closes #NN` in commit messages only when the PR actually fixes the issue; use `Refs #NN` otherwise.
- Do not duplicate — `gh issue list --search` before filing.

## Honesty about evidence

- Do not fabricate test results, screenshots, or smoke outputs. If the test didn't run, say so.
- If a screenshot can't be captured (no display, no rig access), file an issue and flag the gap in the PR body. Never commit a placeholder image.
- If a tool output is trimmed or redacted for length, say "trimmed" — don't silently elide.

## Context recovery

- Sessions start with no shared memory. If you're continuing work, read:
  1. `COWORK-TODO.md` (current handoff state).
  2. Recent issues: `gh issue list --state open --sort created-desc --limit 20`.
  3. Recent PRs: `gh pr list --state all --limit 20`.
  4. `git log --oneline -20`.
- Don't assume a prior session's facts are still true — verify before acting.

## GitHub auth

- PAT bootstrap: `source /sessions/<session-name>/mnt/.claude/.secrets/gh.env` at the start of any `gh` or git-write task. Session names rotate, so glob if needed: `source $(ls /sessions/*/mnt/.claude/.secrets/gh.env | head -1)`.
- In Claude Code terminals on Phoenix's machine, `gh` is already authenticated — no bootstrap needed.
- Never paste the PAT in chat transcripts, PR bodies, commit messages, or issue comments. If it appears there, rotate it.

## Parallel session etiquette

When multiple Claude sessions are active (e.g. overnight runs):

- Each session owns a specific queue. Don't reach into another session's files without coordinating.
- If your work will conflict with another session's in-flight PR, wait or rebase — don't force.
- Comment on the shared issue when picking up / putting down a task that's marked shared (e.g. `#62` is shared between UI and docs).
- File a handoff comment on a tracking issue before stopping — that is your deliverable to the next session.

## Stopping conditions

Stop and file a blocker issue (rather than spinning) when:

- A required resource is unreachable (SSH dead, VM hung, API rate-limited).
- A test depends on hardware state you can't reach from the session.
- A rebase has more than three non-trivial conflicts — hand it to a human.
- CI has been red for the same reason twice in a row and the fix is non-obvious.
- You're about to take a Phoenix-only action.

## Memory

Session-persistent memory lives at `/sessions/<session-name>/mnt/.auto-memory/`. Keep it semantic, not chronological. Never write secrets there. See the auto-memory preamble in each session's system prompt for the full protocol.
