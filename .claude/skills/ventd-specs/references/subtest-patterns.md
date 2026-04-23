# Rule-subtest binding patterns

Cross-reference: **ventd-rulelint** skill validates these bindings at
commit time. This file explains how to write them correctly.

## The three-part rule entry

Every `## RULE-*` section in a `.claude/rules/*.md` file requires three
parts in this order:

```markdown
## RULE-<PREFIX>-<KEYWORD>: brief invariant statement (imperative)

Prose description: what the invariant is, why it matters, what breaks if
it's violated. One to three paragraphs. Write for someone who has not read
the surrounding code — the prose must be self-contained.

Bound: <repo-relative-path>:<t.Run-subtest-name>
```

All three parts are required. A rule without a `Bound:` line is an orphan
and blocks CI.

## The Go test counterpart

```go
func TestXxx_Invariants(t *testing.T) {
    t.Run("rule_keyword_subtest_name", func(t *testing.T) {
        // ...
    })
}
```

The string passed to `t.Run` must match the subtest name in the `Bound:`
line exactly — case-sensitive, including any `/` separators for nested
subtests.

## Naming conventions

### Rule ID

```
RULE-<PREFIX>-<KEYWORD>
```

- `PREFIX`: uppercase abbreviation of the subsystem:
  `HWMON`, `WD`, `CAL`, `HAL`, `IPMI`, `NVML`, `USB`
- `KEYWORD`: screaming-snake phrase, specific enough to be self-explanatory
  without the prose. Examples: `STOP-GATED`, `CLAMP`, `RESTORE-EXIT`.

### Subtest name

Use lowercase snake_case. Mirror the rule keyword where possible, but
optimise for readability in test output:

| Rule ID                          | Subtest name                                 |
|----------------------------------|----------------------------------------------|
| `RULE-HWMON-STOP-GATED`          | `allow_stop/disabled_refuses_zero`           |
| `RULE-HWMON-CLAMP`               | `clamp/below_min_pwm`                        |
| `RULE-WD-RESTORE-EXIT`           | `wd_restore_exit_touches_all_entries`        |
| `RULE-CAL-ZERO-FIRES`            | `TestZeroPWMSentinel_ZeroFiresAfterTwoSeconds` |
| `RULE-HAL-001`                   | `enumerate_idempotent`                       |

Nested subtests (a `t.Run` inside another `t.Run`) use `/`:
```
Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero
```

## Multiple Bound lines

One rule may bind to multiple subtests (e.g. the rule has two distinct
behaviours each worth a dedicated test):

```markdown
Bound: internal/controller/safety_test.go:clamp/below_min_pwm
Bound: internal/controller/safety_test.go:clamp/above_max_pwm
```

Each `Bound:` line is validated independently. Both files must exist and
both subtest names must appear as `t.Run(...)` literals.

## Rule file style reference

Existing rule files to read before writing a new one:

- `.claude/rules/hwmon-safety.md` — the canonical template; most
  complete and most tested. Read this first.
- `.claude/rules/watchdog-safety.md` — RULE-WD-* prefix pattern.
- `.claude/rules/calibration-safety.md` — RULE-CAL-* prefix; shows how
  to bind to both `safety_test.go` and `detect_test.go` in the same file.
- `.claude/rules/hal-contract.md` — RULE-HAL-* numeric IDs; shows how
  to write rules for interface contracts rather than safety invariants.

## Common mistakes

See also `ventd-rulelint` → `references/common-violations.md`.

| Mistake                                       | Symptom                         | Fix                                                   |
|-----------------------------------------------|---------------------------------|-------------------------------------------------------|
| Subtest name has trailing whitespace          | rulelint ERROR: not found       | Strip whitespace in both rule and Go source           |
| Test uses `t.Run(fmt.Sprintf(...))` dynamically | rulelint cannot find literal  | Use a static string; split into separate `t.Run` calls |
| Rule added but subtest not yet written        | rulelint ERROR: forward check   | Write the subtest before committing the rule          |
| Subtest renamed without updating Bound        | rulelint ERROR: not found       | Update the `Bound:` line to the new name              |
| File moved without updating Bound path        | rulelint ERROR: bound file not found | Update the path in the `Bound:` line             |

## Workflow for a new rule

1. Write the rule prose and `Bound:` line in the `.claude/rules/*.md` file.
2. Write the `t.Run("exact-name", ...)` subtest in the Go file.
3. Run `go run ./tools/rulelint -root .` — must exit 0.
4. Run `go test -race ./internal/<pkg>/...` — must pass.
5. Both files go in the same commit / same PR. Never split them.
