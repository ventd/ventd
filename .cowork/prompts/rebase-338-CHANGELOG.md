# rebase-338-CHANGELOG

You are Claude Code. Rebase PR #338 (claude/fix-306-crosec-spam) onto current origin/main, resolving the CHANGELOG conflict.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main claude/fix-306-crosec-spam
git checkout claude/fix-306-crosec-spam
git log --oneline origin/main..HEAD   # should show exactly 1 commit
```

## Rebase

```bash
git rebase origin/main
```

Expected conflict: `CHANGELOG.md` under `## [Unreleased] / ### Fixed`. Recently-merged PRs #336 and #337 added entries to the same section. Resolve by keeping ALL THREE bullets (yours + both of theirs) under `### Fixed`, ordered by merge-time with yours LAST (the newest addition).

Your CHANGELOG bullet (from the original PR):

> `hal/crosec: reset failure counter to zero when the maxConsecutiveFailures threshold triggers Restore, preventing repeated Restore calls and log spam on a persistently broken EC (closes #306).`

After resolving:
```bash
git add CHANGELOG.md
git rebase --continue
```

If any other files conflict: STOP. Report in session output. Do not force-resolve.

## Push

```bash
git push --force-with-lease origin claude/fix-306-crosec-spam
```

## Verify

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/crosec/...
```

Both clean.

## Reporting

- STATUS: rebased | blocked
- `git log --oneline origin/main..HEAD` (should still be 1 commit)
- Confirmation that only CHANGELOG conflicted
- Tail of go test output
