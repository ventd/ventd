# Review T0-INFRA-03-R1
Task: T0-INFRA-03 (faketime fixture)
PR: https://github.com/ventd/ventd/pull/245
Head SHA: 7324b2d692a6b15987965e8001affd89cf461e04
Verdict: revise
Reviewer: Cowork (Opus 4.7)
Timestamp: 2026-04-18T (session resume)

Checklist (abridged):
  R6 (CI green): FAIL — `golangci-lint` fails at current head. The prior checkpoint at 57d0cd5 claimed this commit (7324b2d) was the lint fix ("removed unused fired atomic.Int32 from TestConcurrentAdvanceAndNewTimer"). Remote reality contradicts the claim: lint still red on 7324b2d.
  R14 (vet/lint/fmt clean): FAIL — same failure as R6.
  All other rows: OK modulo what lint reports.

Diagnostic read of the diff:
  - `internal/testfixture/faketime/faketime.go` defines `type Clock struct { mu sync.Mutex; now time.Time; timers []*Timer; tickers []*Ticker; t *testing.T }`. The `t` field is assigned in `New()` and the cleanup closure captures `t` directly from the param, never from `c.t`. Result: `c.t` is dead storage — likely `unused`/`structcheck` flag.
  - `TestWaitUntilTimeout` at line ~222 constructs `inner := &testing.T{}` and calls `faketime.WaitUntil(inner, ...)`. WaitUntil calls `inner.Fatalf` which panics on a zero-value testing.T (no sig wired). `recover()` catches it. This is legal Go, but `staticcheck`/`errcheck` families sometimes flag discarded `recover()` returns. Less likely root cause than the `Clock.t` dead field.
  - Other suspects: unicode escape changes (`%v → %v` → `\u2192`) are cosmetic and lint-neutral; no new deps; no new goroutines without lifecycle.

Recommended revision:
  1. Remove the `t *testing.T` field from `Clock`. The New() cleanup closure captures `t` directly from the param; nothing else needs it.
  2. If #1 doesn't clear lint: run `golangci-lint run --timeout=5m` locally on the branch and paste the exact output. The first session's "lint fix" commit did not reproduce the failure before pushing.
  3. Do not remove the `TestWaitUntilTimeout` subtest — the semantics (verify WaitUntil terminates on timeout) are valuable. If that test trips a specific linter, prefer `//nolint:<name>` with a one-line justification over deletion.

Blocker: Cowork cannot retrieve CI job logs via the current MCP tool set. The revision prompt below instructs CC to reproduce locally before pushing.

Revision prompt: .cowork/prompts/T0-INFRA-03-revise.md
