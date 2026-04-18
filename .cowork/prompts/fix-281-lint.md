# fix-281-lint

You are Claude Code. PR #281 (P2-USB-BASE) failed CI on Ubuntu with
two issues per diag-wave1-ci-failures:

1. `go mod tidy` demotes `golang.org/x/sys v0.43.0` from direct to
   indirect dep (not imported directly, only transitively via go-hid).
2. `errcheck` lint: unchecked `handle.Close()` at
   `internal/hal/usbbase/usbbase_test.go` lines 113, 142, 163.

Fix both, rebase onto current main, push.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main && git pull origin main
gh pr checkout 281
git fetch origin main
git rebase origin/main
```

Resolve conflicts if any (expected: `cmd/ventd/main.go` if #277 or
other Wave 1 have since merged; keep all backend registrations).

## Fix 1: go.mod tidy

```
go mod tidy
```

This will move `golang.org/x/sys` from direct to indirect. Commit the
resulting go.mod + go.sum changes.

## Fix 2: errcheck in usbbase_test.go

In `internal/hal/usbbase/usbbase_test.go`, at lines 113, 142, 163:

```go
// Before:
handle.Close()

// After:
_ = handle.Close()
```

OR wrap in `t.Cleanup`:

```go
t.Cleanup(func() {
    if err := handle.Close(); err != nil {
        t.Logf("close error: %v", err)
    }
})
```

Pick whichever matches the surrounding test style.

## Verify

```
go build ./...
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/usbbase/... ./internal/testfixture/fakehid/...
golangci-lint run ./internal/hal/usbbase/...
gofmt -l .
```

## Push + check CI

```
git push --force-with-lease origin <branch>
sleep 180
gh pr checks 281
```

## Reporting

- STATUS: done | partial | blocked
- REBASE: clean | resolved-N-conflicts
- GO_MOD_TIDY_DIFF: <brief>
- LINT_FIXED: yes | no + details
- CI_AFTER: green | failed + which checks

## Scope

Allowlist: `go.mod`, `go.sum`, `internal/hal/usbbase/usbbase_test.go`,
plus whatever rebase conflict resolution touches.

Do NOT add new tests.
Do NOT touch other PRs' code.
Do NOT bump go-hid.

## Time budget

25 minutes.
