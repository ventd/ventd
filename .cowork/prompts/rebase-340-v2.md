# rebase-340-v2

You are Claude Code. Rebase PR #340 (claude/fix-305-usbbase-hardening) onto current origin/main one more time. A previous rebase (rebase-340-CHANGELOG) landed before #338 merged, so main has drifted again.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main claude/fix-305-usbbase-hardening
git checkout claude/fix-305-usbbase-hardening
git log --oneline origin/main..HEAD   # should show exactly 1 commit
```

## Rebase

```bash
git rebase origin/main
```

Expected conflict: `CHANGELOG.md` under `## [Unreleased] / ### Fixed`. #338 (hal/crosec failure-counter reset) just landed. Keep all existing bullets, add yours LAST.

Your CHANGELOG bullet:

> `hal/usbbase: per-handle I/O now serialised and honours closed state; fakehid matches real go-hid closed-device semantics (closes #305 concerns 1-2; concern 3 tracked separately).`

```bash
git add CHANGELOG.md
git rebase --continue
git push --force-with-lease origin claude/fix-305-usbbase-hardening
```

If anything else conflicts: STOP and report.

## Verify

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...
```

## Reporting

- STATUS: rebased | blocked
- `git log --oneline origin/main..HEAD`
- Test tail
