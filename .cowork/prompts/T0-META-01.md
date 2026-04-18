You are Claude Code, working on the ventd repository.

## Task
ID: T0-META-01
Track: META
Goal: ship a CI lint that enforces rule-to-subtest binding for every `.claude/rules/*.md`, failing the build on orphan rules.

## Model
Claude Sonnet 4.6.

## Context you should read first (≤ 15 files)
- `.claude/rules/hwmon-safety.md` — canonical example of rule-file format
- `internal/controller/safety_test.go` — subtests currently bound to the rule file
- `.github/workflows/ci.yml` (or whichever primary workflow exists) — adopt the same Go setup style
- `CHANGELOG.md` — for the Unreleased entry
- go.mod and go.sum — confirm no new deps needed

## What to do
1. **Create `tools/rulelint/main.go`** (Go program, stdlib-only unless yaml is trivially already vendored):
   a. Walk `.claude/rules/*.md`. Skip README-like files (anything without H2 `## RULE-` headings).
   b. For each rule file, parse H2 headings `## RULE-<ID>: <invariant>` and the `Bound: <file>:<subtest_name>` line appearing within the same rule section.
   c. **Forward check (ERROR on mismatch):** for every `Bound`, confirm
      - the referenced file exists,
      - the file contains either a top-level `func <subtest_name>(...)` OR a `t.Run("<subtest_name>", ...)` literal.
      Accept either form.
   d. **Reverse check (WARN only for now):** for every Go test file named by any `Bound:` line, enumerate its subtest names (both top-level Test functions and t.Run string literals). Warn to stderr when a subtest name looks rule-shaped (starts with `Test` and matches some heuristic you define in a comment) but is unclaimed by any rule. Non-failing.
   e. Exit 0 on success, 1 on any forward-check failure. Human-readable stderr on failure.
2. **Create `tools/rulelint/main_test.go`** with fixture-based test cases under `tools/rulelint/testdata/`:
   - forward happy path (rule → real subtest)
   - missing file
   - file exists but subtest missing
   - malformed `Bound:` line
   - reverse-check warn path (rule-shaped subtest with no rule)
3. **Create `.github/workflows/meta-lint.yml`**:
   - Trigger: `pull_request` on any path, `push` on `main`.
   - Single job: checkout, setup-go matching the Go version used elsewhere in CI, run `go run ./tools/rulelint`.
   - Name the job `meta-lint` so it slots into branch protection later.
4. **Update `CHANGELOG.md`** under `## [Unreleased]` / `### Added`:
   `- ci: rule-to-subtest binding lint (#T0-META-01)`

## Definition of done
- `go build ./tools/rulelint` clean.
- `go test -race ./tools/rulelint` clean.
- `go run ./tools/rulelint` against the current tree exits 0 (the existing `hwmon-safety.md` binds to real subtests in `safety_test.go`).
- Deliberately break the binding in a scratch edit (e.g. rename a `Bound:` target to a non-existent function), run the tool, capture the error — include that output in the PR body. Restore the file before committing.
- CHANGELOG entry present.

## Out of scope
- Any change outside the allowlist.
- Refactoring `.claude/rules/hwmon-safety.md` or `safety_test.go`.
- Tests beyond what this task adds (per testplan §17 scope rule for T-task PRs).

## Branch and PR
- Branch: `claude/META-rulelint-a3c7f`
- Commit prefix: `ci:` (or `test:` if you prefer)
- Open draft PR: `ci: rule-to-subtest binding lint (T0-META-01)`
- PR body must include:
  - Goal (verbatim from above)
  - Files-touched bullet list
  - "How I verified" — literal output of `go build`, `go test`, `go run ./tools/rulelint` on clean tree, AND output of the deliberate-break demo
  - Task ID link

## Constraints
- Allowlist: `.github/workflows/meta-lint.yml`, `tools/rulelint/**`, `CHANGELOG.md`. Nothing else, unless justified inline.
- stdlib only. No new go.mod entries.
- CGO_ENABLED=0 compatible.
- If blocked, draft PR with `[BLOCKED]` prefix and a Blocker section.

## Reporting
On completion, PR body must carry:
- STATUS: done | partial | blocked
- BEFORE-SHA / AFTER-SHA (branch tip before/after your work)
- POST-PUSH GIT LOG: `git log --oneline main..HEAD`
- BUILD: full output of `go build ./tools/rulelint`
- TEST: full output of `go test -race ./tools/rulelint`
- RUN (clean): full output of `go run ./tools/rulelint` on the real tree
- BREAK-DEMO: which `Bound:` you temporarily broke + the tool's stderr
- GOFMT: output of `gofmt -l tools/rulelint .github/workflows` (must be empty)
- CONCERNS: second-guessing
- FOLLOWUPS: out-of-scope work you noticed
