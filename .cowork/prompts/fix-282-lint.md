# fix-282-lint

You are Claude Code. PR #282 (P2-CROSEC-01) failed CI on Ubuntu with
three lint issues per diag-wave1-ci-failures:

1. `errcheck`: unchecked `syscall.Close` at
   `internal/hal/crosec/crosec.go:203`
2. `staticcheck` SA4016: redundant constant bit-shift expression at
   `internal/hal/crosec/crosec.go:53`
3. `unused`: function `happyFake` in
   `internal/hal/crosec/crosec_test.go:15` is declared but unused

ALSO: diag flagged `TestScheduler_ManualOverrideStaysUntilTransition`
failing on this PR, but that's #289 concern 1 — fixed in separate PR.
Do NOT touch scheduler code here. Rebase this PR after #289 merges and
the scheduler test will pass on its own.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main && git pull origin main
gh pr checkout 282
git fetch origin main
git rebase origin/main
```

## Fix 1: errcheck at crosec.go:203

Locate the `syscall.Close` call and wrap or check:

```go
// Before:
syscall.Close(fd)

// After:
_ = syscall.Close(fd)
```

(Or inside a defer: `defer func() { _ = syscall.Close(fd) }()`.
Match surrounding style.)

## Fix 2: staticcheck SA4016 at crosec.go:53

`SA4016` flags redundant bit-shift like `x << 0`. Find line 53 and
collapse the expression. Example:

```go
// Before:
const CROS_EC_DEV_IOCXCMD = 0xEC<<0 | 0x00<<8 | ...

// After (if the <<0 really is a no-op):
const CROS_EC_DEV_IOCXCMD = 0xEC | 0x00<<8 | ...
```

Read the line carefully — if it's an ioctl number construction, the
structure is intentional but the `<<0` is still dead. Removing it
doesn't change the value.

## Fix 3: unused happyFake in crosec_test.go:15

Either use `happyFake` in a test, or delete it. If it's a test helper
that's obviously meant to be called by a future test, rename to
`_happyFake` or add a `//nolint:unused` comment with a TODO referencing
the future use. Default: delete it and let a future test add it back.

## Verify

```
go build ./...
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/crosec/... ./internal/testfixture/fakecrosec/...
# Also run the scheduler test to verify #289 fix carried over:
go test -race -count=1 -run TestScheduler ./internal/web/...
golangci-lint run ./internal/hal/crosec/...
gofmt -l .
```

## Push + check CI

```
git push --force-with-lease origin <branch>
sleep 180
gh pr checks 282
```

## Reporting

- STATUS: done | partial | blocked
- REBASE: clean | resolved-N-conflicts
- FIX_1_ERRCHECK: done | details
- FIX_2_SA4016: done | details — include before/after of line 53
- FIX_3_UNUSED: deleted | renamed | nolint
- SCHEDULER_TEST: pass | fail
- CI_AFTER: green | failed + which checks

## Scope

Allowlist: `internal/hal/crosec/crosec.go`,
`internal/hal/crosec/crosec_test.go`, plus rebase-resolution files.

Do NOT touch scheduler code (that's #289).
Do NOT add new tests beyond what the unused-function fix requires.

## Time budget

25 minutes.
