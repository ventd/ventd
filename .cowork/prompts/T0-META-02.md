# PRE-DRAFT — awaiting T0-META-01 merge (shared workflow file)

T0-META-02 blocks on T0-META-01 because both modify
`.github/workflows/meta-lint.yml`. This prompt is staged; Cowork
releases it to a terminal the moment T0-META-01 merges.

---

You are Claude Code, working on the ventd repository.

## Task
ID: T0-META-02
Track: META
Goal: extend the meta-lint workflow with a lint that asserts every closed bug-type GitHub issue has a matching `TestRegression_Issue<N>_*` test, or is explicitly labelled `no-regression-test`.

## Model
Claude Sonnet 4.6.

## Context you should read first (≤ 15 files)
- `tools/rulelint/main.go` (from T0-META-01 merge) — mirror its style and CLI shape
- `.github/workflows/meta-lint.yml` (from T0-META-01 merge) — add the new job alongside the existing one
- `testplan.md` §11 (regression / issue replay) — your authoritative DoD
- `testplan.md` §17 T0-META-02 entry
- Existing `TestRegression_Issue*_*` names in the repo — grep for them to understand the naming pattern
- `CHANGELOG.md`

## What to do
1. **Create `tools/regresslint/main.go`** (Go program, stdlib only):
   a. Input: list of closed bug-labelled issues. Two modes:
      - Default: query GitHub API via `GITHUB_TOKEN` env var, filter `state:closed label:bug` from `ventd/ventd`.
      - Offline: read from a JSON file path given via `-issues path.json` (used by unit tests and local runs).
   b. For each such issue number N:
      - Skip if the issue carries the `no-regression-test` label.
      - Otherwise, search the repo for `TestRegression_Issue<N>_*` via `grep -r` semantics in `internal/` and `cmd/`. Accept either a top-level `func TestRegression_Issue<N>_*` OR a `t.Run("Issue<N>_...", ...)` literal.
      - If none found, record as a violation.
   c. Exit 0 if no violations; exit 1 with a human-readable report listing (issue number, link, suggested action: add regression test OR add `no-regression-test` label).
2. **Tests at `tools/regresslint/main_test.go`** — table-driven, using `testdata/` JSON fixtures:
   - happy path (all closed bugs have regression tests)
   - missing regression test
   - issue with `no-regression-test` label (exempt)
   - malformed JSON input
3. **Update `.github/workflows/meta-lint.yml`** to add a second job `regresslint`:
   - Run after `rulelint` in the same workflow file (so the workflow is a single Required Check umbrella).
   - Uses `GITHUB_TOKEN` provided by Actions.
   - Fails the workflow if the tool exits non-zero.
4. **Update the PR template** (`.github/pull_request_template.md`): no change needed — the T0-META-03 checkbox already covers this at PR-author time; this lint enforces it at merge time.
5. **CHANGELOG entry** under `## [Unreleased]` / `### Added`:
   `- ci: regression-test-per-closed-bug lint (#T0-META-02)`

## Definition of done
- `go build ./tools/regresslint` clean
- `go test -race ./tools/regresslint` clean with 100% of the declared cases passing
- Running `go run ./tools/regresslint -issues testdata/happy.json` exits 0
- Running `go run ./tools/regresslint -issues testdata/missing.json` exits 1 with a clear report
- Workflow `meta-lint.yml` now has both `rulelint` and `regresslint` jobs
- CHANGELOG entry present

## Out of scope
- Enforcing regression-test presence on PRs for NEW issues (that's R19 on PR review; this lint is periodic / merge-time only, not per-PR).
- Migrating the existing backlog of unlabelled / uncovered issues — a TX-* evergreen task does that.
- Anything outside the allowlist.

## Branch and PR
- Branch: `claude/META-regresslint-<5-char-rand>`
- Commit prefix: `ci:`
- Draft PR: `ci: regression-test-per-closed-bug lint (T0-META-02)`
- PR body: goal verbatim; files-touched; sample happy/missing JSON + tool output; verification outputs; task ID.

## Constraints
- Allowlist: `.github/workflows/meta-lint.yml`, `tools/regresslint/**`, `CHANGELOG.md`
- stdlib only; GitHub API access via `net/http` + `encoding/json`
- CGO_ENABLED=0 compatible
- Do not hardcode issue lists; the JSON-fixture is for test injection only. Production path queries the API.

## Reporting
PR body must carry:
- STATUS / BEFORE-SHA / AFTER-SHA / POST-PUSH GIT LOG
- BUILD / TEST outputs, literal
- RUN-HAPPY and RUN-MISSING: stdout/stderr of each fixture run
- GOFMT / CONCERNS / FOLLOWUPS

See `.cowork/TESTING.md`. This task is CI-sufficient; no HIL or VM validation required.
