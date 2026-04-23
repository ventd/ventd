---
name: ventd-rulelint
description: >
  Enforces ventd's 1:1 rule-binding contract between .claude/rules/*.md
  entries and their bound subtests in Go test files. ALWAYS invoke before
  claiming any task complete that touched cmd/ or internal/. ALWAYS invoke
  when the user mentions rulelint, project rules, RULE-* identifiers,
  rule files, or bound subtests. ALWAYS invoke when adding, editing, or
  deleting any ## RULE-* section in a .claude/rules/*.md file. Runs
  tools/rulelint to verify every rule has a live bound subtest and surfaces
  both forward errors (missing bound) and reverse warnings (unclaimed
  subtests).
---

# ventd-rulelint

## What rulelint enforces

Every `## RULE-<ID>: <description>` heading in `.claude/rules/*.md` must be
paired with a `Bound:` line pointing to a live subtest:

```
Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero
```

The tool (`tools/rulelint/main.go`) does two checks:

1. **Forward** (errors): every bound file exists AND contains the named
   `t.Run("…")` literal. Any mismatch is an `ERROR` and exits non-zero.
2. **Reverse** (warnings): any `t.Run("…")` in a bound file that no rule
   claims is printed as `WARN`. These don't fail the build but signal an
   orphaned subtest.

## Running the linter

```bash
bash .claude/skills/ventd-rulelint/scripts/run-rulelint.sh
```

Or directly:

```bash
go run ./tools/rulelint -root .
```

Exit 0 → `ok: N rule(s), M bound(s) verified`
Exit 1 → one or more `ERROR:` lines; do not claim the task complete.

`make safety-run` runs the controller safety subtests but does NOT run
rulelint — they are separate steps.

## When to run

Run rulelint whenever you:

- Add a new `## RULE-*` section to any `.claude/rules/*.md` file
- Edit an existing `Bound:` line
- Rename or move a test function or `t.Run` subtest
- Delete a rule or its bound subtest
- Are about to write "task complete" / "done" after modifying `cmd/` or
  `internal/`

## Rule file → bound test mapping

See `references/rules-catalog.md` for a one-paragraph summary of each of
the 9 rule files and the RULE-* IDs they own.

## Common mistakes and fixes

See `references/common-violations.md` for annotated before/after examples
of every violation pattern rulelint catches.

## Adding a new rule correctly

A complete rule entry looks like this (all three parts required):

```markdown
## RULE-HWMON-NEWFEATURE: brief invariant statement

Prose description of the invariant and why it matters.

Bound: internal/controller/safety_test.go:hwmon_newfeature_subtest_name
```

Then in the Go test file:

```go
t.Run("hwmon_newfeature_subtest_name", func(t *testing.T) {
    // ...
})
```

The subtest name in the rule file and in the Go source must match exactly
(case-sensitive, including slashes for nested subtests).

## Bound line format

```
Bound: <repo-relative-path>:<subtest-name>
```

- Path is relative to the repo root (no leading `./`)
- Subtest name is the exact string passed to `t.Run()`
- Nested subtests use `/` as separator:
  `Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero`
- Multiple `Bound:` lines under one rule are allowed (one per line)

## If rulelint reports errors

```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-CLAMP: subtest "clamp/below_min_pwm" not found in internal/controller/safety_test.go
```

1. Find the subtest in the test file — it may have been renamed.
2. Either restore the original name or update the `Bound:` line to match.
3. Re-run rulelint to confirm clean.

```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-CLAMP: bound file not found: internal/controller/safety_test.go
```

1. The test file was moved or the path is wrong.
2. Update the `Bound:` line to the current file path.

```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-CLAMP: malformed Bound line (no colon separator): "Bound: internal/controller/safety_test.go"
```

1. Add the colon separator: `Bound: internal/controller/safety_test.go:subtest_name`
