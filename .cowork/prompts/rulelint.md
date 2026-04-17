# CC prompt — T0-META-01 (rulelint)

Rulelint CI tool that enforces rule-to-subtest binding for `.claude/rules/*.md`. This is the prompt that produced PR #244. Meta-lint workflow file was pushed separately to `claude/META-rulelint-a3c7f` via MCP after gh CLI scope blocker was resolved.

## Task
ID: T0-META-01
Track: META
Goal: ship a CI lint that parses every `.claude/rules/*.md`, confirms every `Bound:` line points to a real subtest in the target test file, and fails if any rule is unbound or any bound-file subtest exists without a rule.

## Context you should read first
- `.claude/rules/hwmon-safety.md` (the existing example)
- `internal/controller/safety_test.go` (example bound-to target)
- `ventdtestmasterplan.mkd` §2 (invariant binding) and §17 T0-META-01 entry

## What to do
1. Create `tools/rulelint/main.go` with a stdlib-only parser that:
   - walks `.claude/rules/*.md`
   - for each `## RULE-<ID>: <description>` heading, extracts the corresponding `Bound: <path>:<subtest>` line
   - asserts the target file exists and contains a matching `t.Run("<subtest>",...)` or test function
   - reverse-check: every `t.Run(...)` subtest in a bound file that is not claimed by any rule emits a WARN
2. `tools/rulelint/main_test.go` with table-driven testdata under `tools/rulelint/testdata/` (happy, missing_file, missing_subtest, malformed_bound, reverse_warn).
3. `.github/workflows/meta-lint.yml` that runs `go run ./tools/rulelint` on pull_request + push to main.
4. CHANGELOG entry.

## Definition of done
- `go run ./tools/rulelint` exits 0 on the current repo (all rule files currently have no `## RULE-` headings — no false positives on empty case).
- A synthetic `## RULE-TEST-99: foo\n\nBound: nonexistent:no_such_subtest` in a rule file causes exit 1 with a clear error.
- `go test -race ./tools/rulelint` passes.
- PR draft, title `ci: rule-to-subtest binding lint (T0-META-01)`.

## Out of scope
- Binding any actual rule from `.claude/rules/*.md` to a subtest (that's the per-rule T-* tasks).
- Changing existing rule files' content.

## Branch and PR
- Branch: claude/META-rulelint-<rand5>
- Draft PR.

## Constraints
- Stdlib only; no third-party parser dependencies.
- Do not touch files outside: `tools/rulelint/**`, `.github/workflows/meta-lint.yml`, `CHANGELOG.md`.

## Reporting
STATUS / PR / SUMMARY / CONCERNS / FOLLOWUPS.

## Model
Sonnet 4.6 (meta tooling, not safety-critical per the Cowork SYSTEM prompt model table).
