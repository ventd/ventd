You are Claude Code. Resolve merge conflict on PR #260 (P1-HOT-01 hot-loop allocation elimination).

## Context

PR #260 branched from pre-#258 main. Since then #258 (T-HAL-01), #259 (P1-MOD-01), and #257 (P1-FP-02) merged to main. All three added CHANGELOG.md entries. The PR branch now conflicts.

GitHub's auto-merge is blocked. Rebase or merge main into the feature branch, resolve CHANGELOG.md conflicts, push.

## Steps

1. Check out the PR branch:
   ```
   cd /home/cc-runner/ventd
   git fetch origin main claude/P1-HOT-01-hot-loop-alloc-elim
   git checkout claude/P1-HOT-01-hot-loop-alloc-elim
   ```

2. Try a rebase onto main:
   ```
   git rebase origin/main
   ```
   Expect CHANGELOG.md conflict. Some commits from the PR branch may be dropped as "patch already upstream" — that's expected (Cowork wrote a CHANGELOG-merge commit via MCP that git will drop).

3. Resolve CHANGELOG.md conflict. The correct resolution:
   - Keep ALL existing entries from main (P10-PERMPOL-01, T0-META-02, T-WD-01, T-HAL-01, P1-MOD-01, P1-FP-02 already there).
   - ADD the P1-HOT-01 line from your branch under `### Changed`. The entry text is: `perf(controller): eliminate per-tick allocations in the hot loop — preallocate sensor/smoothed maps, cache compiled curve graph, one-shot config snapshot, cache fan*_max for rpm_target fans, binary-search Points curve, pool Mix.Evaluate vals slice (P1-HOT-01)`
   - Place the line alongside the existing `### Changed` entry for P1-MOD-01.

4. Stage and continue the rebase:
   ```
   git add CHANGELOG.md
   git rebase --continue
   ```

5. If other files conflict (unlikely — changes in #258/#259/#257 don't touch internal/controller/, internal/curve/, internal/hal/hwmon/backend.go), resolve by preserving both sides' additions.

6. Verify nothing else broke:
   ```
   CGO_ENABLED=0 go build ./...
   go test -race -count=1 ./internal/controller/... ./internal/curve/... ./internal/hal/...
   go vet ./...
   gofmt -l internal/controller/controller.go internal/curve/points.go internal/curve/mix.go internal/hal/hwmon/backend.go
   ```
   All must pass. gofmt must be silent.

7. Force-push the rebased branch:
   ```
   git push --force-with-lease origin claude/P1-HOT-01-hot-loop-alloc-elim
   ```

## Reporting

- STATUS: done | partial | blocked
- CONFLICTED_FILES: list
- DROPPED_COMMITS: any commits git reports as "patch contents already upstream"
- RESOLUTION_SUMMARY: what you kept
- POST_REBASE_SHA: <output of `git rev-parse HEAD`>
- TESTS: all pass / failing with <summary>

## Out of scope

- Do not touch code outside what's already in the PR.
- Do not change the PR description or title.
- Do not merge the PR — Cowork will do that after CI re-runs.
