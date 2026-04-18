# fix-285-rebase-post-281-282

You are Claude Code. PR #285 (P2-IPMI-01) is green on its own branch,
but after #281 and #282 merged ahead, it needs rebase. Rebase, push,
verify CI green, report.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main && git pull origin main
gh pr checkout 285
git fetch origin main
git rebase origin/main
```

## Expected conflicts

- `cmd/ventd/main.go` — #281 added `halusbbase` and #282 added
  `halcrosec`; #285 adds `halipmi`. Keep ALL four backend imports and
  four registrations (halasahi, halcrosec, halhwmon, halipmi, halnvml,
  halpwmsys, halusbbase — alphabetical).
- `CHANGELOG.md` — same as fix-282-rebase-post-281: let markers land,
  keep all three Unreleased entries in chronological order (usbbase,
  crosec, ipmi).
- `go.mod` / `go.sum` — ipmi uses golang.org/x/sys (likely already
  pulled in by one of the prior backends). Run `go mod tidy` with
  appropriate tags if needed.

## Verify

```
CGO_ENABLED=0 go build ./...
go build ./...
go test -race -count=1 ./internal/hal/ipmi/...
golangci-lint run ./internal/hal/ipmi/...
gofmt -l .
```

All must be clean.

## Push and check

```
git push --force-with-lease origin <branch>
sleep 180
gh pr checks 285
```

## Reporting

- STATUS: done | partial | blocked
- REBASE: clean | resolved-N-conflicts
- FILES_CONFLICTED: <list>
- CI_AFTER: green | failed + which checks

## Out of scope

- Do NOT modify anything outside the original PR's allowlist (ipmi
  package + main.go + go.mod/go.sum + CHANGELOG).
- Do NOT add new tests.
- Do NOT bump deps.

## Time budget

20 minutes.
