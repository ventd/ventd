# .claude/rules — invariant rule files

Each `*.md` file in this directory defines a set of safety invariants for a
specific package or subsystem. Every rule is 1:1 with a subtest; `tools/rulelint`
verifies the pairing at CI time.

## Rule file format

Rule headings are level-2 Markdown headings matching the pattern
`## RULE-<PREFIX>-<N>: <one-line invariant statement>`. Each heading is
followed by a body paragraph and a `Bound:` line:

```
Bound: relative/path/to/package_test.go:SubtestName
```

`Bound:` must contain exactly one colon separating the file path from the
subtest name. `rulelint` matches the subtest name against `t.Run("...")` literal
strings in the target file.

## Docs-first workflow: allow-orphan marker

When writing a docs PR that defines invariants before the implementation PR
lands the matching subtests, add `<!-- rulelint:allow-orphan -->` on the line
**directly after** the `Bound:` line:

```
Bound: internal/hal/liquid/corsair/safety_test.go:TestLiquidSafety_Invariants/PumpMinimumFloor
<!-- rulelint:allow-orphan -->
```

**When to use it:** the docs PR defines the rules; the impl PR lands the
subtests. Without the marker, rulelint would fail the docs PR because the
bound file does not exist yet.

**When to remove it:** in the impl PR that creates `safety_test.go` and
adds each subtest. Remove the marker in the same commit that lands the
matching `t.Run(...)` call. rulelint errors if the marker is present on a
rule whose binding target already exists and is valid — this is a deliberate
reminder to clean up stale markers.

**Placement is strict:** the marker must appear on the line immediately
following `Bound:` with no blank line between. A marker elsewhere in the
rule body is ignored; the bound check runs normally and errors if the file
is missing.

**Scope is per-rule:** marking one rule does not affect adjacent rules. A
file with 3 marked rules and 3 unmarked rules produces exactly 3 errors for
the unmarked ones.
