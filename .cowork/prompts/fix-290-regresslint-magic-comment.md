# fix-290-regresslint-magic-comment

You are Claude Code. Mia's issue #290: `tools/regresslint/main.go` currently
accepts exactly two patterns to consider a closed `bug` issue covered by
a regression test:

- `func TestRegression_Issue<N>_` (top-level test function)
- `t.Run("Issue<N>_` (subtest literal)

Zero tests in the repo currently match either pattern. All existing
regression coverage uses descriptive test names. Flipping `-strict=true`
today would fail every closed bug issue that hasn't been manually
labelled `no-regression-test`.

## Goal

Extend `hasRegressionTest()` to also accept magic-comment binding:
`// regresses #N` or `// covers #N` (case-insensitive, whitespace-tolerant,
comma-separated lists accepted). Land as pure tool-code change; no
retroactive comment additions to existing tests in this PR.

## Context you should read first

- `tools/regresslint/main.go` (full file; particularly lines 102-220)
- `tools/regresslint/main_test.go` (if it exists)
- `.github/workflows/meta-lint.yml` or wherever regresslint is invoked in CI
- `.cowork/ventdtestmasterplan.mkd` §11 for the regression-replay convention

## What to do

1. Extend `hasRegressionTest(root string, issueN int) bool` in
   `tools/regresslint/main.go`:

   Current matchers (keep them):
   - `func TestRegression_Issue<N>_`
   - `t.Run("Issue<N>_`

   New matchers (add):
   - `// regresses #<N>`
   - `// regresses: #<N>` / `// regresses: <N>`
   - `// covers #<N>` / `// covers: #<N>` / `// covers: <N>`
   - Multiple per line: `// regresses #86, #103` — split on comma and
     whitespace; all numbers count.
   - Case-insensitive (`// REGRESSES`, `// Covers`).
   - Leading whitespace tolerated.

   Scan stays bounded to `internal/` and `cmd/`, `_test.go` files only.

2. Update or create `tools/regresslint/main_test.go`:

   Table-driven fixtures covering (each is a separate fixture file
   under `tools/regresslint/testdata/`):
   - `function_name_match/` — has `func TestRegression_Issue177_Foo(...)`.
   - `subtest_literal_match/` — has `t.Run("Issue208_PopulatedModal", ...)`.
   - `magic_comment_above_func/` — `// regresses #177` immediately before `func TestHandleSystemReboot_RefusedInContainer(...)`.
   - `magic_comment_inside_func/` — `// regresses #208` inside a `t.Run(...)` block.
   - `multi_issue_comment/` — `// regresses #86, #103, #140`.
   - `covers_synonym/` — `// covers #200`.
   - `case_insensitive/` — `// REGRESSES #59`.
   - `no_match/` — a test file that mentions issues in comments but
     without the magic-comment format.

   Each fixture is a valid `_test.go` file with minimal content but
   valid Go syntax so parsing doesn't crash regresslint's scanner.

   Test assertions: for each fixture, call
   `hasRegressionTest(fixture_root, expected_issue_number)` and assert
   true/false as appropriate.

3. Update the TODO comment at `main.go:212` (or wherever the
   `TX-REGRESSION-AUDIT` comment lives) with the new unblock criterion:

   ```
   // TODO(TX-REGRESSION-AUDIT): flip -strict=true once:
   //   (a) all closed bugs with live Go-code coverage carry a
   //       `// regresses #<N>` binding on their covering test, and
   //   (b) all remaining closed bugs are labelled `no-regression-test`.
   ```

4. Update `tools/regresslint/README.md` (create if absent) with a short
   section documenting the magic-comment form as the preferred binding
   for tests with descriptive names.

## Definition of done

- `tools/regresslint/main.go` accepts the two new matcher forms plus
  case-insensitive and comma-separated variants.
- `tools/regresslint/main_test.go` covers the 8 fixture cases.
- Fixtures exist at `tools/regresslint/testdata/<name>/`.
- `tools/regresslint/README.md` documents the magic-comment format.
- TODO comment at main.go:~212 updated.
- CI `regresslint` job continues to pass (no behaviour regression for
  existing function-name + subtest-literal matchers).
- CHANGELOG entry under `## Unreleased / ### Added` — "regresslint:
  `// regresses #N` magic comment accepted as regression-test binding
  (closes #290)."
- PR references `Fixes #290`.

## Out of scope

- Adding magic comments retroactively to existing test files — that's
  a follow-up PR per Mia's issue body.
- Flipping `-strict=true`.
- Renaming the existing `TestRegression_Issue<N>_*` pattern.
- Changes outside `tools/regresslint/`.

## Branch and PR

- Branch: `claude/fix-290-regresslint-magic-comment`
- PR title: `feat(regresslint): accept // regresses #N magic comment (fixes #290)`
- Open as ready-for-review (NOT draft).

## Constraints

- Files touched (allowlist):
  - `tools/regresslint/main.go`
  - `tools/regresslint/main_test.go` (may be new)
  - `tools/regresslint/testdata/**` (all new)
  - `tools/regresslint/README.md` (may be new)
  - `CHANGELOG.md`
- No new direct dependencies.
- `CGO_ENABLED=0` compatible.

## Reporting

- STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS as standard.
- Additional section: MATCHER_PATTERNS — list every regex or literal-
  match form the updated hasRegressionTest accepts.
- Additional section: TEST_FIXTURE_LIST — list every fixture directory
  and what it asserts.
- Additional section: STRICT_MODE_SIMULATION — if you're confident,
  describe what flipping `-strict=true` today would do with the new
  matchers and current repo state. (Optional; skip if you don't want
  to walk the whole tree.)

## Time budget

35 minutes wall-clock.

## Final note

Sonnet-eligible. Parallel-safe with non-tooling work. No interaction
with Phase 2 / 4 backends.
