# fix-282-rebase-post-281

You are Claude Code. PR #282 (P2-CROSEC-01) was green but GitHub marked
it as needing rebase after #281 (P2-USB-BASE) merged ahead of it.
`update_pull_request_branch` returned 422 — git history divergence,
not file conflict. Rebase, push, verify CI green, report.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main && git pull origin main
gh pr checkout 282
git fetch origin main
git rebase origin/main
```

## Expected conflicts

- `cmd/ventd/main.go` — #281 added `halusbbase` registration; #282 adds
  `halcrosec` registration. Keep both in alphabetical order (asahi,
  crosec, hwmon, nvml, pwmsys, usbbase).
- `go.mod` / `go.sum` — possible overlap from #281's go-hid addition.
  Run `go mod tidy` with whichever build tag set preserves both deps.
- `CHANGELOG.md` — DO NOT pre-resolve via git; let conflict markers
  land, then resolve by keeping both entries under `## Unreleased /
  ### Added` in chronological order (usbbase first, crosec second).

## Verify

```
CGO_ENABLED=0 go build ./...
go build ./...
go test -race -count=1 ./internal/hal/crosec/... ./internal/testfixture/fakecrosec/...
golangci-lint run ./internal/hal/crosec/...
gofmt -l .
```

All must be clean.

## Push and check

```
git push --force-with-lease origin <branch>
sleep 180
gh pr checks 282
```

## Reporting

- STATUS: done | partial | blocked
- REBASE: clean | resolved-N-conflicts
- FILES_CONFLICTED: <list>
- CI_AFTER: green | failed + which checks

## Out of scope

- Do NOT modify anything outside the original PR's allowlist (crosec
  package + main.go + fakecrosec fixture + go.mod/go.sum + CHANGELOG).
- Do NOT add new tests.
- Do NOT bump deps.

## Time budget

20 minutes.
