You are Claude Code. Fix PR #254 (T0-META-02 regression-test lint). Two CI
failures. You own the fix.

Branch: `claude/META-regresslint-x7k2p` on ventd/ventd. Check out that
branch and commit on top of it, then push. Do NOT open a new PR —
update #254 in place.

## Failure 1: regresslint (the lint job that runs your new tool)

Expected. Your new tool runs over the actual ventd repo in CI and flags
any closed bug issue without a TestRegression_Issue_* counterpart.
Realistically the repo has ~80 closed bug issues, none of which have
regression tests matching that naming convention, so the tool exits 1.

Investigate `gh run view 24586324246 --log-failed --job 71896708513` to
see the exact issue numbers it flagged.

### Fix options (pick ONE)

A) Make the CI job `regresslint` non-enforcing initially — run in
   "report mode" (no exit 1). Add a `-fail-on-missing=false` flag that
   gates the exit code. Default in CI: false. Future TX-* task flips it
   to true once the historical closed bugs are labelled or
   backfilled.

B) Add `no-regression-test` label to every existing closed bug via the
   GitHub API (one-off script), so the lint passes clean from day one.

Option A is the right call. Option B is brittle (requires org-wide
label permissions, commits a large label-change event).

### Implementation for Option A

1. Edit `tools/regresslint/main.go`: add `-fail-on-missing` flag,
   default false. Only `os.Exit(1)` when the flag is true AND there are
   missing tests. Print the report unconditionally.

2. Edit `.github/workflows/meta-lint.yml` regresslint job: run with
   `-fail-on-missing=false` initially. Leave a comment explaining this
   is gating off until the TX task.

3. Add a test case in `main_test.go` that asserts exit 0 when
   `-fail-on-missing=false` even with missing tests.

4. Update CHANGELOG.md entry to say "(report mode; enforcement gated
   behind -fail-on-missing flag, off by default)".

## Failure 2: golangci-lint

Run `gh run view 24586324252 --log-failed --job 71896708502` to see the
exact lint error. Most likely causes: missing `nolint` directive on an
unused import, a new lint rule triggered (gofmt/gosimple/unused), or
the new file doesn't satisfy a style rule already green on main.

Fix whatever it flags. Don't blanket-disable lint rules — fix the
source.

## Exit criteria

- `go run ./tools/regresslint` on the repo with `-fail-on-missing=false`
  exits 0.
- `go run ./tools/regresslint -fail-on-missing=true` still exits 1 with
  the same report.
- `golangci-lint run ./...` exits 0.
- `go test -race ./tools/regresslint/...` exits 0.
- Push to the same branch `claude/META-regresslint-x7k2p`, #254 updates
  automatically.

## Out of scope

- Do not change regresslint's core logic (how it detects test
  functions, how it queries the API).
- Do not open a new PR.
- Do not touch any file outside `tools/regresslint/`,
  `.github/workflows/meta-lint.yml`, and `CHANGELOG.md`.
- Do not edit any other PR's files.

## Report

After push, post a PR comment summarising: what was failing, what you
changed, confirmation both lint + test pass locally. Then exit.
