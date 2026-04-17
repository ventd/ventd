# CC prompt — revise T0-INFRA-03 (PR #245)

## Task
ID: T0-INFRA-03-revise-1
Base task: T0-INFRA-03
Goal: make `golangci-lint run --timeout=5m` pass on PR #245.

## Context
PR #245 faketime fixture. Current head 7324b2d. 12/13 CI checks green, golangci-lint fails. The previous session already tried one lint fix (the 7324b2d commit itself, which removed an unused atomic.Int32). That fix didn't land the job green.

Branch: `claude/INFRA-faketime-fresh`

## What to do
1. Check out the branch locally: `git fetch origin claude/INFRA-faketime-fresh && git switch claude/INFRA-faketime-fresh`.
2. Install the CI version of the linter: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6`.
3. Reproduce the failure locally: `golangci-lint run --timeout=5m`. Paste the full output into the PR as a comment before touching code.
4. Primary suspect: `internal/testfixture/faketime/faketime.go` defines `type Clock struct { ...; t *testing.T }`. The `t` field is stored in `New()` but never read — the cleanup closure captures the `t` param directly. Remove the `t` field from `Clock` entirely; only `sync.Mutex`, `now`, `timers`, `tickers` are needed.
5. Re-run the linter. If it passes, stop. If not, follow the specific errors the linter actually emitted (that you pasted in step 3) and fix the minimum code to clear them. Prefer field removal over `//nolint` directives; only use `//nolint:<linter>` with a one-line rationale when the code is deliberate (e.g. the `recover()` in `TestWaitUntilTimeout` if staticcheck flags it).
6. Do NOT modify `TestWaitUntilTimeout`'s semantics — it proves WaitUntil terminates on a timeout path. That coverage is valuable. If the pattern trips a linter, suppress with `//nolint` + rationale; do not delete.
7. Run the full test suite: `go test -race -count=1 ./internal/testfixture/faketime/... ./internal/calibrate/...`. Both must pass.
8. Commit with message `fix(faketime): remove dead Clock.t field to clear golangci-lint`. Single commit. Push.

## Definition of done
- `golangci-lint run --timeout=5m` exits 0 locally.
- CI on PR #245 shows golangci-lint green.
- All tests pass locally with `-race`.
- PR #245 comment includes the original `golangci-lint` output so the root cause is documented.

## Out of scope
- Adding new tests or changing test semantics (the test surface here is already in scope for T0-INFRA-03; further changes escalate).
- Any code changes outside `internal/testfixture/faketime/`.
- Changing Go version or adding dependencies.

## Branch and PR
- Work on branch: claude/INFRA-faketime-fresh (existing)
- Commit style: conventional commits
- PR is already open as #245. Do not open a new PR.
- On completion push the new commit; do not mark ready-for-review — Cowork handles that after reviewing CI.

## Constraints
- CGO_ENABLED=0 compatible (already is).
- No new direct dependencies.
- Do not touch CHANGELOG.md — lint fix in an already-logged task doesn't earn a line.

## Reporting
On completion:
- STATUS: done | partial | blocked
- PR: https://github.com/ventd/ventd/pull/245
- SUMMARY: which linter complained, which field/line triggered it, what you changed.
- CONCERNS: any second-guessing.
- FOLLOWUPS: nothing expected — a clean lint fix shouldn't spawn them.

## Model
Sonnet 4.6 (same as original T0-INFRA-03).
