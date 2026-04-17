You are Claude Code, working on the ventd repository.

## Task
ID: P1-HOT-01 (revision)
Track: HOT
Goal: Rebase PR #260 onto current `main` and resolve the `CHANGELOG.md` conflict introduced by #258 (T-HAL-01) merging ahead of us.

## Care level
Low-risk conflict resolution. The conflict is purely in `CHANGELOG.md` — the `- test(hal): contract test T-HAL-01 ...` line that #260 added is already on `main` from #258, so the resolution is to drop the duplicate line from #260's side and keep #260's own `## Unreleased / ### Changed` entries for P1-MOD-01 and P1-HOT-01. No code changes. No behaviour changes.

## Context you should read first

- `CHANGELOG.md` on `main` (current tip) — see what's already there under `## [Unreleased]`.
- The PR #260 branch `claude/P1-HOT-01-hot-loop-alloc-elim` head commit's diff on `CHANGELOG.md`.
- That's it — everything else in #260 should stay exactly as-is.

## What to do

1. `git fetch origin && git checkout claude/P1-HOT-01-hot-loop-alloc-elim && git pull --ff-only`.
2. `git rebase origin/main`. Expect exactly one conflict in `CHANGELOG.md`.
3. Resolve by keeping `main`'s version for the T-HAL-01 line (it's already there) and keeping #260's added lines for P1-MOD-01 and P1-HOT-01 under `### Changed`. The post-rebase `CHANGELOG.md` should have:
   - The T-HAL-01 line appears exactly ONCE (from `main` via #258).
   - The P1-MOD-01 `perf: drop modinfo shellouts...` line under `### Changed` (already on main, keep).
   - The P1-HOT-01 `perf(controller): eliminate per-tick allocations...` line NEW under `### Changed`.
4. `git add CHANGELOG.md && git rebase --continue`.
5. Verify the rest of the rebase is clean (no other conflicts). If a second conflict appears, push WIP and flag in the PR body — do NOT invent code changes to resolve non-CHANGELOG conflicts.
6. Build + test sanity:
   - `CGO_ENABLED=0 go build ./...` — must be clean
   - `go test -race -count=1 ./internal/controller/... ./internal/curve/...` — must pass
   - `gofmt -l internal/controller/controller.go internal/curve/points.go internal/curve/mix.go internal/hal/hwmon/backend.go` — must be empty
7. `git push --force-with-lease origin claude/P1-HOT-01-hot-loop-alloc-elim`.

## Definition of done

- PR #260 reports `mergeable_state: "clean"` (or `"unstable"` while CI re-runs; anything but `dirty` is fine).
- `CHANGELOG.md` contains the T-HAL-01 line exactly once.
- `CHANGELOG.md` contains the P1-HOT-01 line under `### Changed`.
- All previously-green tests on the branch still pass.
- No changes to any file other than `CHANGELOG.md` (rebase may touch other files only if #258 or later merges conflicted with them — flag any such case in a PR comment).

## Out of scope for this task

- Any code changes.
- Any test changes.
- Any new CHANGELOG entries.
- Re-squashing history (leave the two-commit history alone; force-push is only to update the rebased tips).
- Resolving conflicts in files other than `CHANGELOG.md` (if any appear, stop and flag).

## Branch and PR

- Work on the existing branch: `claude/P1-HOT-01-hot-loop-alloc-elim`
- Do NOT open a new PR. Update the existing PR #260.
- Add a short comment to PR #260 summarising the rebase:
  - "Rebased on main; resolved CHANGELOG conflict with #258 (T-HAL-01 line now appears once from main). No code changes."

## Constraints

- Only touch `CHANGELOG.md` in the conflict resolution. If the rebase drags in other files' conflicts, stop and push WIP with `[BLOCKED]` in a PR comment.
- Use `--force-with-lease`, never `--force`.
- Keep `CGO_ENABLED=0` compatibility.
- Do not add or remove dependencies.

## Reporting

On completion, post a comment on PR #260 with:
- STATUS: done | blocked
- SUMMARY: one sentence.
- CI: "re-running" or "not triggered, see GitHub".

