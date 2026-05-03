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
- Posting external messages (Slack, email, Phoronix tip-line, Reddit) — these are public-facing, single-shot, and can't be retracted.

## Standing delegations (pre-authorised)

Claude can do these without per-instance confirmation. Preconditions in parentheses must hold; if any fails, escalate the failure rather than bypass.

- `git tag` of versions in the `vX.Y.Z[-suffix]` scheme (CI green on the merge commit, ci-local sweep passes, CHANGELOG entry exists). Tag message summarises headline changes since the previous tag.
- `goreleaser release` from a pushed tag (downstream of the tag).
- Deleting remote and local branches whose PRs are merged or closed (verified via `gh pr view`).
- Rebasing onto `origin/main` when a branch is BEHIND (no force-push to main itself).
- Re-running flaky CI jobs once via `gh run rerun --failed` (registry-listed flakes only — `.github/flaky-tests.yaml`).
- Updating `MEMORY.md` and any `feedback_*.md` / `project_*.md` auto-memory files when the trigger conditions in the auto-memory preamble are met.
- Closing GitHub issues that are demonstrably resolved (with a comment linking the resolving commit/PR).
- Filing GitHub issues without `gh issue list --search` first — duplicates are cheap to merge later; missing-information bugs are expensive.

If a class of action has been explicitly approved earlier in the session ("yes do it", "go with your recommendations"), subsequent actions in that class within the same session are pre-authorised — don't re-ask.

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
- If a PR has been on CI more than 45 minutes with no completion, file a CI-flake issue and keep moving. (The 5-distro × race matrix legitimately takes 25-40 min; old 20-min cap fired on every clean run.)
- Use the GraphQL mutation path for `gh pr edit --body` — `gh` CLI silently fails on the projects-classic deprecation. Resolve the PR ID via GraphQL, then call `updatePullRequest` directly with `body`.

## Attribution

See `.claude/rules/attribution.md` — the short version: no `Co-Authored-By: Claude`, no "Generated with" footers, no mentions of AI / LLM / agent anywhere in commits, PRs, issues, or docs. This repo ships under a single human author.

## Issue filing

- File issues for every surprise worth remembering. "Surprise" = non-obvious behaviour, false positive, flake, missing coverage, wish-list item.
- Title is a single line, imperative or descriptive. Body includes: summary, evidence, proposed fix, impact, source (the PR or run that surfaced it).
- Link related issues. Close issues via `Closes #NN` in commit messages only when the PR actually fixes the issue; use `Refs #NN` otherwise.
- Don't gate on duplicate-detection — file when the bug is fresh in your head; merging duplicates later is cheap, and a missed-issue costs more.

## Honesty about evidence

- Do not fabricate test results, screenshots, or smoke outputs. If the test didn't run, say so.
- If a screenshot can't be captured (no display, no rig access), file an issue and flag the gap in the PR body. Never commit a placeholder image.
- If a tool output is trimmed or redacted for length, say "trimmed" — don't silently elide.

## Context recovery

- Sessions start with no shared memory. If continuing work, run:
  1. `gh pr list --state open --limit 20` — what's in flight.
  2. `gh issue list --state open --sort created-desc --limit 20` — open issues.
  3. `git log --oneline -20` — recent commits.
  4. `git branch -r | grep -v HEAD` — outstanding remote branches.
- Auto-memory at `/root/.claude/projects/-root/memory/MEMORY.md` carries cross-session context — read it.
- Don't assume a prior session's facts are still true — verify before acting on a stale claim.

## GitHub auth

- In Claude Code terminals `gh` is already authenticated — just use it.
- For Cowork sessions: `source $(ls /sessions/*/mnt/.claude/.secrets/gh.env | head -1)` at start of any `gh`/`git`-write task.
- Never paste the PAT in chat transcripts, PR bodies, commit messages, or issue comments. If it leaks, rotate immediately.

## Parallel session etiquette

When multiple Claude sessions are active (e.g. overnight runs):

- Each session owns a specific queue. Don't reach into another session's files without coordinating.
- If your work will conflict with another session's in-flight PR, wait or rebase — don't force.
- Comment on the shared issue when picking up / putting down a task that's marked shared (e.g. `#62` is shared between UI and docs).
- File a handoff comment on a tracking issue before stopping — that is your deliverable to the next session.

## Stopping conditions

Stop and file a blocker issue (rather than spinning) when:

- A required resource is unreachable (SSH dead, VM hung, API rate-limited).
- A test depends on hardware state you can't reach from the session — file the issue, mark the test skipped or HIL-only, move on with other work.
- A rebase reveals an actual design conflict (the merge ambiguous in intent, not just whitespace/auto-generated files). Conflict count alone is not the threshold — auto-generated indices (`RULE-INDEX.md`, generated catalogs) can `--skip` and regenerate cleanly.
- CI has been red for the same reason three times in a row and the fix is non-obvious.
- You're about to take a Phoenix-only action.

## Memory

Session-persistent memory lives at `/sessions/<session-name>/mnt/.auto-memory/`. Keep it semantic, not chronological. Never write secrets there. See the auto-memory preamble in each session's system prompt for the full protocol.
