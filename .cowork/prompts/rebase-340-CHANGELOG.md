# rebase-340-CHANGELOG

You are Claude Code. Rebase PR #340 (claude/fix-305-usbbase-hardening) onto current origin/main, resolving the CHANGELOG conflict.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main claude/fix-305-usbbase-hardening
git checkout claude/fix-305-usbbase-hardening
git log --oneline origin/main..HEAD
```

## Rebase

```bash
git rebase origin/main
```

Expected conflict: `CHANGELOG.md` under `## [Unreleased] / ### Fixed`. Recently-merged PRs #336 and #337 added entries to the same section. Resolve by keeping ALL bullets under `### Fixed`, ordered by merge-time with yours LAST.

Your CHANGELOG bullet (from the original PR):

> `hal/usbbase: per-handle I/O now serialised and honours closed state; fakehid matches real go-hid closed-device semantics (closes #305 concerns 1-2; concern 3 tracked separately).`

After resolving:
```bash
git add CHANGELOG.md
git rebase --continue
```

If any other files conflict: STOP. Report. Do not force-resolve.

## Push

```bash
git push --force-with-lease origin claude/fix-305-usbbase-hardening
```

## Verify

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...
```

Both clean.

## Reporting

- STATUS: rebased | blocked
- `git log --oneline origin/main..HEAD`
- Confirmation that only CHANGELOG conflicted
- Tail of go test output
