# fix-290-regresslint-magic-comment

You are Claude Code. Extend `tools/regresslint/main.go` `hasRegressionTest()`
to also recognize `// regresses #<N>` and `// covers #<N>` magic
comments in addition to the existing function-name and subtest-literal
patterns. Implements Mia's proposal at #290.

This unblocks the `TODO(TX-REGRESSION-AUDIT)` → strict-mode flip without
requiring a destructive rename sweep across 50+ test files.

## Scope

Closes #290.

## Context — read first

- `tools/regresslint/main.go` — the tool. Focus on `hasRegressionTest()`
  at lines ~102-125 and `walkForPatterns()` at ~128-155.
- `tools/regresslint/main_test.go` — existing tests (if any). Follow
  the same testing patterns.
- Issue #290 — full design spec, including accepted comment forms.

## Design

Extend `hasRegressionTest()` to accept FOUR patterns now (two existing,
two new):

**Existing (unchanged):**
1. `func TestRegression_Issue<N>_` — top-level test function.
2. `t.Run("Issue<N>_` — subtest literal.

**New:**
3. `// regresses #<N>` (case-insensitive).
4. `// covers #<N>` (case-insensitive).

### Accepted magic-comment forms

All case-insensitive, allow surrounding whitespace, allow optional colon:

- `// regresses #177`
- `// regresses: #177`
- `// regresses: 177`  (with or without #)
- `// covers #177`
- `// covers: #177`
- `// regresses #86, #103` (comma-separated list)
- `// regresses #86 #103` (whitespace-separated list)

Both spaces and tabs after `//` are fine. Leading indentation of the
comment line is fine.

### Implementation approach

Instead of relying on `strings.Contains` scanning (the current
implementation), use a compiled regex for the magic-comment case:

```go
// matches: //<ws>(regresses|covers)[:]?<ws>(#?<digits>(<separator>#?<digits>)*)
var magicCommentRe = regexp.MustCompile(
    `(?i)^\s*//\s*(?:regresses|covers)\s*:?\s*#?(\d+(?:[,\s#]+\d+)*)\s*$`,
)
```

The capture group pulls the issue-number list; parse each number out of
that and check if any match the target.

Existing `walkForPatterns` keeps working for patterns 1 and 2 (literal
substring match). Add a sibling `walkForMagicComment` that scans with
the regex and parses out matched issue numbers. Or: refactor
`walkForPatterns` to accept a callback per line, where literal patterns
and the regex are both checked.

Prefer the refactor — cleaner, one walk pass instead of two per issue.

### Strictness

Magic comments are matched in `_test.go` files only (same scope as
existing patterns). `internal/` and `cmd/` only. No changes to scope.

## Tests to add

Extend `tools/regresslint/main_test.go` with:

1. `TestHasRegressionTest_MagicComment_BasicForm` — file containing
   `// regresses #177` above a test function matches issue 177.
2. `TestHasRegressionTest_MagicComment_Colon` — `// regresses: #177`
   matches.
3. `TestHasRegressionTest_MagicComment_NoHash` — `// regresses: 177`
   (no `#`) matches.
4. `TestHasRegressionTest_MagicComment_Covers` — `// covers #208`
   matches 208.
5. `TestHasRegressionTest_MagicComment_CaseInsensitive` —
   `// REGRESSES #177` and `// Covers #208` both match.
6. `TestHasRegressionTest_MagicComment_MultipleIssues` —
   `// regresses #86, #103` matches both 86 and 103.
7. `TestHasRegressionTest_MagicComment_WhitespaceSeparated` —
   `// regresses #86 #103` matches both.
8. `TestHasRegressionTest_MagicComment_InsideFunction` — comment
   inside a `t.Run(...)` block (not on preceding line) matches.
9. `TestHasRegressionTest_MagicComment_DoesNotMatch_Unrelated` — a
   comment like `// regresses to 177 (not the issue number)` — if
   the regex is too permissive it'd match; verify it does NOT.
10. `TestHasRegressionTest_MagicComment_OutsideTestFile` — magic
    comment in a non-test `.go` file is ignored.
11. `TestHasRegressionTest_MagicComment_OutsideScope` — magic comment
    in a file outside `internal/` and `cmd/` is ignored.
12. `TestHasRegressionTest_ExistingPatterns_StillWork` — existing
    function-name and subtest patterns still match; regression guard.

Each test uses a temporary directory structure with fixture `_test.go`
files containing the specific comment form. Use `t.TempDir()` +
`os.WriteFile` — no real repo dependency.

## Update the TODO

In `tools/regresslint/main.go` where the `// TODO(TX-REGRESSION-AUDIT)`
line lives near `flag.BoolVar(&strict, ...)`, replace it with:

```go
// TODO(TX-REGRESSION-AUDIT): flip -strict=true once (a) all closed
// bugs with live Go-code coverage carry a "// regresses #N" or
// "// covers #N" binding on their covering test, and (b) all remaining
// closed bugs are labelled "no-regression-test". See #290.
```

## Verify

```
cd /home/cc-runner/ventd
git fetch origin main
git checkout main && git pull origin main
git checkout -b claude/fix-290-regresslint-magic-comment-$(openssl rand -hex 2)

# Make the edits.

go test -race -count=1 ./tools/regresslint/...
# Expect: all 12 new tests + all existing tests pass.

go vet ./tools/regresslint/...
golangci-lint run ./tools/regresslint/...
gofmt -l tools/regresslint/

# End-to-end smoke: build the tool and confirm it still works against
# a known-good and known-bad fixture:
go build -o /tmp/regresslint ./tools/regresslint/
# (no -issues flag means GITHUB_TOKEN-based; skip this step in CC
# since CC has no token. Just verify the binary builds.)
```

All must be clean.

## PR

- Branch: `claude/fix-290-regresslint-magic-comment-<rand>`
- Title: `feat(regresslint): accept "// regresses #N" and "// covers #N" magic comments (closes #290)`
- Body includes:
  - `Fixes #290`
  - The regex pattern used, inline.
  - Test matrix: 12 new tests, what each verifies.
  - Sample regex output showing what matches and what doesn't.
- Open ready-for-review (NOT draft).

## Reporting

- STATUS: done | partial | blocked
- PR: <url>
- REGEX_USED: paste the final regex pattern verbatim.
- TEST_MATRIX: one line per new test, PASS/FAIL.
- CONCERNS: any edge cases in the regex you're uncertain about.
- FOLLOWUPS: retroactively annotating existing tests with `// regresses
  #N` is explicitly OUT OF SCOPE for this PR (per issue #290) — note
  this as a FOLLOWUP for a separate PR.

## Constraints

- Files touched (allowlist):
  - `tools/regresslint/main.go`
  - `tools/regresslint/main_test.go`
  - `tools/regresslint/testdata/**` (new fixture files, if needed)
  - `CHANGELOG.md` (one-line entry under `## Unreleased / ### Added`:
    "regresslint: accept '// regresses #N' and '// covers #N' magic
    comments as regression-test bindings (closes #290)")
- No new dependencies. `regexp` is stdlib.
- No changes to `hasBug()`, `hasExempt()`, `fetchIssues()`, or the
  `run()` orchestration — the extension is purely inside
  `hasRegressionTest()` (and its helper walker).
- Do NOT retroactively annotate existing regression tests in this PR.
  That's a separate follow-up.

## Time budget

30 minutes.
