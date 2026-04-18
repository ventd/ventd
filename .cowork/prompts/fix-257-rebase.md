You are Claude Code. Resolve merge conflict on PR #257 (P1-FP-02 hwdb remote refresh).

## Context

PR #257 branched from pre-#253 main. Since then, #253 / #254 / #255 / #256 merged to main, all adding entries to CHANGELOG.md. The PR branch now conflicts with main.

GitHub's auto-merge is blocked. Need to rebase or merge main into the feature branch, resolve CHANGELOG.md conflicts, push.

## Steps

1. Check out the PR branch:
   ```
   cd /home/cc-runner/ventd
   git fetch origin main claude/P1-FP-02-hwdb-remote-refresh
   git checkout claude/P1-FP-02-hwdb-remote-refresh
   ```

2. Try a rebase onto main:
   ```
   git rebase origin/main
   ```
   Expect CHANGELOG.md conflict.

3. Resolve CHANGELOG.md manually. The correct resolution is:
   - Keep ALL existing entries from main (those are from #253 / #254 / #255 / #256 and are already live).
   - ADD the P1-FP-02 entry from your branch under `## Unreleased / ### Added`.
   - The entry line is something like: `- opt-in remote refresh for hwdb fingerprint database (P1-FP-02)` — use whatever line your branch already added, just keep it alongside the others.

4. Stage and continue the rebase:
   ```
   git add CHANGELOG.md
   git rebase --continue
   ```

5. If other files conflict (unlikely, but possible on `cmd/ventd/listfansprobe.go` or `internal/config/config.go`), resolve by preserving both sides' additions — no deletions.

6. Verify nothing else broke:
   ```
   go build ./cmd/ventd/
   go test -race -count=1 ./internal/hwdb/... ./internal/config/...
   go vet ./...
   golangci-lint run ./internal/hwdb/... ./internal/config/... ./cmd/ventd/...
   ```
   All must pass.

7. Force-push the rebased branch:
   ```
   git push --force-with-lease origin claude/P1-FP-02-hwdb-remote-refresh
   ```
   (force-with-lease is safe here; --force is denied by the settings.json deny rules but --force-with-lease is allowed via `git:*`.)

## Reporting

- STATUS: done | partial | blocked
- CONFLICTED_FILES: list
- RESOLUTION_SUMMARY: what you chose to keep
- POST_REBASE_SHA: <output of `git rev-parse HEAD`>
- TESTS: all pass / failing with <summary>

## Out of scope

- Do not touch code outside what's already in the PR.
- Do not change the PR description or title.
- Do not merge the PR.
