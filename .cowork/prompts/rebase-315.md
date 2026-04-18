# rebase-315 — rebase P4-PI-01-v2 onto main (conflict expected on CHANGELOG.md)

You are Claude Code. One-shot task: rebase `claude/P4-PI-01-v2` onto current `origin/main` and resolve the expected CHANGELOG.md conflict. Do not modify any other file.

## Context

PR #315 (P4-PI-01 PI curve) was opened from `claude/P4-PI-01-v2` branched from `origin/main@807f2023`. Since PR creation, PR #314 (testfixture refactor) merged to main at SHA `504ddf6f`. Both PRs touched `CHANGELOG.md`'s `## [Unreleased]` block, producing a text conflict.

Atlas attempted `update_pull_request_branch` via MCP and got a 422 merge-conflict response. Manual rebase is required.

## Task

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout claude/P4-PI-01-v2
git rebase origin/main
```

Expected conflict: `CHANGELOG.md`. Resolve by keeping BOTH entries:
- #315's entry: the PI curve addition (starts with "- PI curve type (`type: pi`) ...").
- #314's entry (now on main): the testfixture refactor (starts with "- `internal/testfixture/base`: shared `Base` struct extracted from 10 ..." or similar).

Both entries belong under the same `## [Unreleased] / ### Added` heading. Preserve chronological order — the testfixture entry (from #314) landed first, so PI curve entry goes BELOW it, or at the top of Added depending on existing convention. Read the current CHANGELOG.md on main to match the existing style.

After resolving:

```bash
git add CHANGELOG.md
git rebase --continue
git push --force-with-lease origin claude/P4-PI-01-v2
```

## Definition of done

- `git status` clean on `claude/P4-PI-01-v2`.
- `gh pr view 315 --json mergeable,mergeStateStatus` shows `mergeable: true` and `mergeStateStatus: CLEAN` (or `UNSTABLE` pending CI rerun).
- CI pipeline starts within 60 seconds of the force-push (CI triggers on push event).

## Constraints

- Touch ONLY `CHANGELOG.md` during conflict resolution. Do not modify `internal/`, `docs/`, `config.example.yaml`, or any other file.
- Do NOT merge the PR. Atlas merges via MCP after CI goes green.
- Do NOT add extra commits — the rebase should produce exactly one commit on the branch.
- If `git rebase` produces conflicts in any file OTHER than `CHANGELOG.md`, abort: `git rebase --abort` and report. Do not guess.
- If the force-push is rejected with non-fast-forward or requires --force (not --force-with-lease), abort and report. Do not escalate to plain --force.

## Reporting

- STATUS: done | blocked
- Output of `git log --oneline origin/main..HEAD` (should be one line).
- Output of `git diff --stat origin/main..HEAD | tail -1`.
- Output of `gh pr view 315 --json mergeable,mergeStateStatus,headRefOid` after the push.
