# fix-330-regresslint-magic-comment

You are Claude Code. Extend `tools/regresslint/main.go` to recognise `// regresses #N` and `// covers #N` magic-comment annotations per issue #330.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-330-regresslint-magic-comment origin/main
# Sanity check: cowork/state files must not be present
test ! -f .cowork/prompts/fix-330-regresslint-magic-comment.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

If the sanity check fails, stop immediately and report.

## Task

`tools/regresslint/main.go` currently recognises two patterns that bind a test to an issue number:
1. `func TestRegression_Issue<N>_*` — top-level function naming convention
2. `t.Run("Issue<N>_..."` — subtest naming convention

Neither `// regresses #N` nor `// covers #N` magic comments are recognised. This was the bug that closed #321 without merge: the annotation sweep added comments that regresslint silently ignored.

Extend the tool to treat those comments as a third binding pattern, additive to the existing two.

## Required changes

### `tools/regresslint/main.go`

Read `hasRegressionTest` before editing. It builds `funcPat` and `tRunPat` as strings and passes them to `walkForPatterns`. The fix is to add two more pattern strings.

In `hasRegressionTest`, extend the patterns passed to `walkForPatterns`:

```go
func hasRegressionTest(root string, issueNum int) (bool, error) {
	funcPat    := fmt.Sprintf("func TestRegression_Issue%d_", issueNum)
	tRunPat    := fmt.Sprintf(`t.Run("Issue%d_`, issueNum)
	regressPat := fmt.Sprintf("// regresses #%d", issueNum)
	coversPat  := fmt.Sprintf("// covers #%d", issueNum)

	for _, dir := range []string{"internal", "cmd"} {
		...
		found, err := walkForPatterns(dirPath, funcPat, tRunPat, regressPat, coversPat)
		...
	}
	...
}
```

Do not change `walkForPatterns`. Do not use regex. `strings.Contains` line matching is correct: a line containing `// regresses #123` will match `regressPat` for issueNum=123, and will NOT match for issueNum=12 or issueNum=1230 because the pattern includes the full `#123` token.

Do NOT modify the violation message in `run`. The action hint still reads `add TestRegression_Issue<N>_*`; updating it is a separate polish issue.

### `tools/regresslint/main_test.go`

Read the existing test file first to understand the fixture pattern (how test files are written inline for `walkForPatterns` or `hasRegressionTest`).

Add four tests (add alongside existing tests, do not restructure):

1. **`TestMagicComment_Regresses`** — fixture file contains `// regresses #123` above a `func TestFoo` declaration. Assert `hasRegressionTest` (or `walkForPatterns`) returns true for issue 123. Add `// regresses #330` above this test function.

2. **`TestMagicComment_Covers`** — fixture file contains `// covers #456` as first line inside a `t.Run(...)` block. Assert returns true for issue 456. Add `// regresses #330` above this test function.

3. **`TestMagicComment_BothInSameFile`** — fixture file contains both `// regresses #123` and `// covers #456`. Assert both 123 and 456 return true. Add `// regresses #330` above this test function.

4. **`TestMagicComment_MalformedIgnored`** — fixture file contains `// regresses #abc` (non-numeric). Assert returns false for any numeric issue. No crash. Add `// regresses #330` above this test function.

Do NOT change existing tests.

## Allowlist

- `tools/regresslint/main.go`
- `tools/regresslint/main_test.go`
- `CHANGELOG.md`

No other files.

## Verification

```bash
CGO_ENABLED=0 go build ./tools/regresslint/...
go test -race -count=1 ./tools/regresslint/...
gofmt -l tools/regresslint/
go vet ./tools/regresslint/...
```

All four must be clean. All four new tests must pass.

## PR

Open ready (not draft). Title: `feat(regresslint): recognise // regresses #N and // covers #N magic-comment annotations (closes #330)`

PR body must include:
- Fixes `#330`
- Note: "Re-dispatching #304 annotation sweep is unblocked once this merges."
- BRANCH_CLEANLINESS block: paste `git log --oneline origin/main..HEAD` and `git diff --stat origin/main..HEAD | tail -1`
- TEST_MATRIX: list the four new test names and what each pins
- CHANGELOG entry under `## [Unreleased] / ### Added`

## Constraints

- Do NOT merge. Atlas merges.
- Do NOT use regex in the implementation — `strings.Contains` on the formatted pattern string is correct and consistent with the existing code.
- Do NOT update the violation-message action hint (`add TestRegression_Issue<N>_*`) — that is out of scope.
- Single commit.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./tools/regresslint/...` tail
- Lines changed
