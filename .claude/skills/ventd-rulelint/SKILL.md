---
name: ventd-rulelint
description: |
  Use after any edit to .claude/rules/*.md, after any rename or move of
  a Go test function or t.Run subtest, and BEFORE declaring any task
  complete that touched cmd/ or internal/. Triggers on: "rulelint",
  "RULE-*", "rule binding", "bound subtest", "orphan rule", "orphan
  subtest", any mention of project rules. Runs tools/rulelint to
  verify every rule has a live bound subtest. Surfaces forward errors
  (missing bound) and reverse warnings (unclaimed subtests). Do NOT
  use for: writing new rules from scratch (use ventd-specs Mode C),
  implementing the test logic itself, or fixing the underlying invariant
  the rule pins.
---

# ventd-rulelint

Validates the 1:1 binding contract between `.claude/rules/*.md` and Go subtests.

## Current state

<!-- VERIFY CC SUPPORTS !`...` INJECTION; remove if not -->
Rule files: !`ls .claude/rules/*.md 2>/dev/null | wc -l` files

Latest rulelint result: !`go run ./tools/rulelint -root . 2>&1 | tail -5`

## Run

```bash
bash .claude/skills/ventd-rulelint/scripts/run-rulelint.sh
# or direct:
go run ./tools/rulelint -root .
```

Exit 0 → `ok: N rule(s), M bound(s) verified`.
Exit 1 → one or more `ERROR:` lines; do not claim complete.

`make safety-run` runs the controller safety subtests but NOT
rulelint. Different commands.

## What rulelint enforces

Every `## RULE-<ID>: <description>` heading in `.claude/rules/*.md`
pairs with a `Bound:` line:

```
Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero
```

Two checks:

1. **Forward** (errors): every bound file exists AND contains the
   named `t.Run("…")` literal. Mismatch → `ERROR`, exit 1.
2. **Reverse** (warnings): any `t.Run("…")` in a bound file that no
   rule claims → `WARN`. Doesn't fail the build but signals an
   orphaned subtest.

`Bound:` line format:
- Path is repo-relative (no leading `./`)
- Subtest name is the exact string from `t.Run()`
- Nested subtests use `/` separator
- Multiple `Bound:` lines per rule allowed (one per line)

## Gotchas (real failure modes)

- **Nested subtest names need slashes.**
  `Bound: x_test.go:parent/child` — not `parent_child`. Case-sensitive.
- **`<!-- rulelint:allow-orphan -->` is a docs-first PR marker.** It
  MUST be removed when the binding resolves. rulelint errors on
  present-but-resolved markers. This has shipped to CI before.
- **Backend can build+test but never register.** A backend file can
  pass go test in isolation while not being wired into
  `cmd/ventd`. Verify with
  `go list -deps ./cmd/ventd | grep <pkg>` before declaring done.
  rulelint does NOT catch ghost code; it only checks subtest binding.
- **Unused-symbol golangci-lint errors on load-bearing types** =
  architectural drift, not a lint nitpick. Fix the wiring, don't add
  `//nolint:unused`.
- **Path drift after `git mv`.** Renaming a test file mass-breaks
  every `Bound:` line that referenced it. Run rulelint immediately
  after any `git mv internal/...` to catch this in the same commit.
- **rulelint discovery is hardcoded.** New rule files in
  `.claude/rules/` are picked up automatically, but if rulelint has
  ever been extended with a hardcoded list (check
  `tools/rulelint/main.go`), a new file may need explicit listing.

## Adding a new rule

A complete rule entry needs three parts:

```markdown
## RULE-HWMON-NEWFEATURE: brief invariant statement

Prose description of why this invariant matters and what fails if it
doesn't hold.

Bound: internal/controller/safety_test.go:hwmon_newfeature_subtest
```

Matching Go:
```go
t.Run("hwmon_newfeature_subtest", func(t *testing.T) {
    // ...
})
```

Names match exactly. Rule and subtest land in the same PR.

## Common error shapes

```
ERROR: ... subtest "X" not found in <file>
```
→ Subtest renamed or never existed. Either restore the name or update
the `Bound:` line.

```
ERROR: ... bound file not found: <path>
```
→ File moved or path is wrong.

```
ERROR: ... malformed Bound line (no colon separator)
```
→ Add the `:` between path and subtest name.

## Reference material

- `references/rules-catalog.md` — one-paragraph summary of each rule
  file and the RULE-* IDs it owns.
- `references/common-violations.md` — annotated before/after examples
  of every violation pattern.

## Out of scope

- Writing new rules (use ventd-specs Mode C)
- Implementing the underlying invariant the rule pins
- Auto-fixing violations
- Running the full test suite (use ci-verify-local)
