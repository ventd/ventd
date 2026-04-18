# fix-306-crosec-spam

You are Claude Code. Fix the write-failure counter reset bug and add the maxPayload comment in `internal/hal/crosec/crosec.go` per issue #306.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-306-crosec-spam origin/main
# Sanity check: cowork/state files must not be present
test ! -f .cowork/prompts/fix-306-crosec-spam.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

If the sanity check fails, stop immediately and report.

## Task

`internal/hal/crosec/crosec.go` has two issues from Cassidy's #282 audit:

1. **Log spam (Concern 1 — code fix required):** `b.failures` is not reset after `Restore` is triggered on the `maxConsecutiveFailures` threshold. Every subsequent `Write` failure re-trips the threshold, calls `Restore` again, and emits another `Error` log line. On a persistently broken EC this produces ~30 log lines/minute of the same message indefinitely.

2. **Missing comment on `maxPayload` (Concern 3 — doc-only):** The constant has no explanation of its headroom or what happens if a future command exceeds it.

## Required changes in `internal/hal/crosec/crosec.go`

### Fix 1: Reset failure counter before unlocking

In `Write`, inside the block that triggers on `b.failures >= maxConsecutiveFailures`, add `b.failures = 0` immediately before `b.mu.Unlock()`:

```go
if b.failures >= maxConsecutiveFailures {
    fails := b.failures
    b.failures = 0       // reset so next burst gets a fresh 5-count window
    b.mu.Unlock()
    b.logger.Error("crosec: consecutive write failures, restoring EC auto mode",
        "failures", fails, "channel", ch.ID)
    _ = b.Restore(ch)
    return err
}
```

Do not restructure the surrounding lock/unlock logic. Do not move the `Restore` call inside the lock. Do not add a `locked bool` latch — option (a) from the issue is the correct choice.

### Fix 2: Add comment on maxPayload

Replace the bare constant:

```go
const maxPayload = 64
```

With:

```go
// maxPayload caps the fixed-size data region in ecBuf. Raise if any EC
// command added here needs larger payloads; current ceiling is 4 bytes.
const maxPayload = 64
```

## Out of scope

Do NOT address Concern 2 (concurrent Write + Restore race). The issue defers it until/unless the controller goes multi-goroutine. Leave it untouched.

## Allowlist

- `internal/hal/crosec/crosec.go`
- `CHANGELOG.md`

No other files.

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/crosec/...
gofmt -l internal/hal/crosec/
go vet ./internal/hal/crosec/...
```

All four must be clean.

## PR

Open ready (not draft). Title: `fix(hal/crosec): reset failure counter after Restore to stop log spam (closes #306)`

PR body must include:
- Fixes `#306`
- BRANCH_CLEANLINESS block: paste output of `git log --oneline origin/main..HEAD` and `git diff --stat origin/main..HEAD | tail -1`
- CHANGELOG entry under `## [Unreleased] / ### Fixed`

## Constraints

- Do NOT merge. Atlas merges.
- Do NOT address Concern 2 (concurrent race).
- Do NOT restructure the mu lock/unlock pattern beyond the single `b.failures = 0` insertion.
- Single commit.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./internal/hal/crosec/...` tail
- Lines changed
