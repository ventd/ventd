# fix-285-lint

You are Claude Code. PR #285 (P2-IPMI-01) failed CI on Ubuntu with
two issues per diag-wave1-ci-failures:

1. `go mod tidy` promotes `golang.org/x/sys v0.43.0` from indirect to
   direct (PR does import it directly for ioctl syscalls).
2. `unused` lint: functions `(*Backend).withDMI` at
   `internal/hal/ipmi/backend.go:260` and `(*Backend).withVendor` at
   `internal/hal/ipmi/backend.go:272` are declared but never called.

Fix both, rebase onto current main, push.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin
git checkout main && git pull origin main
gh pr checkout 285
git fetch origin main
git rebase origin/main
```

Conflicts expected in `cmd/ventd/main.go` (IPMI + pwmsys + asahi all
register there after #277 merge). Keep all registrations.

## Fix 1: go.mod tidy

```
go mod tidy
```

This promotes `golang.org/x/sys` from indirect to direct. Commit.

## Fix 2: unused withDMI / withVendor

At `internal/hal/ipmi/backend.go:260` and `:272`:

Read the functions. They're probably intended test injection hooks
(`withDMI(fakeDmi)` / `withVendor("supermicro")` pattern used by
other backends). Two options:

(a) **If the tests intend to use them but don't yet**: keep them, but
    add `//nolint:unused // used by table-driven tests in
    ipmi_test.go` OR add a trivial _test.go reference that takes a
    compile-time pointer to the functions without calling them. The
    latter is cleaner:

    ```go
    // internal/hal/ipmi/unused_test.go
    package ipmi

    // Compile-time references to test-only constructors that are
    // declared but not yet used by any test. Prevents unused lint
    // without a nolint suppression.
    var (
        _ = (*Backend)(nil).withDMI
        _ = (*Backend)(nil).withVendor
    )
    ```

(b) **If they're genuinely dead**: delete them. Read the call sites
    across the tree (`grep -rn withDMI\\|withVendor` in `internal/`
    and `cmd/`) to confirm no references exist.

Default: option (a) — test-injection helpers have a near-certain
future use, and deleting them would make the next IPMI test PR
do more work.

## Verify

```
go build ./...
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hal/ipmi/... ./internal/testfixture/fakeipmi/...
golangci-lint run ./internal/hal/ipmi/...
gofmt -l .
```

## Push + check CI

```
git push --force-with-lease origin <branch>
sleep 180
gh pr checks 285
```

## Reporting

- STATUS: done | partial | blocked
- REBASE: clean | resolved-N-conflicts
- GO_MOD_TIDY_DIFF: <brief>
- UNUSED_FIX: option-a-compile-ref | option-b-deleted | nolint
- CI_AFTER: green | failed + which checks

## Scope

Allowlist: `go.mod`, `go.sum`, `internal/hal/ipmi/backend.go`,
possibly `internal/hal/ipmi/unused_test.go` (new), plus rebase-
resolution files (`cmd/ventd/main.go`, `CHANGELOG.md`).

Do NOT add runtime logic to withDMI/withVendor.
Do NOT touch unrelated files.

## Time budget

25 minutes.
