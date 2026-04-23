# Common rulelint Violations — Annotated Examples

---

## 1. Rule heading without a Bound line

**Violation**
```markdown
## RULE-HWMON-NEWFEATURE: pumps log a warning when below floor

When `is_pump: true` and the curve output would fall below pump_minimum,
the controller logs a warning with the suppressed value.
```
rulelint output:
```
(silent — no Bound line means no check, but if you added a Bound pointing to a non-existent subtest, you'd get ERROR)
```

Wait — actually rulelint silently skips rules with no `Bound:` lines. **This is the silent danger**: a rule with no `Bound:` passes the linter but has no test coverage. Always add a `Bound:` when writing a new rule.

**Fix**
```markdown
## RULE-HWMON-NEWFEATURE: pumps log a warning when below floor

When `is_pump: true` and the curve output would fall below pump_minimum,
the controller logs a warning with the suppressed value.

Bound: internal/controller/safety_test.go:clamp/pump_warning_below_floor
```
Then add the matching subtest:
```go
t.Run("pump_warning_below_floor", func(t *testing.T) { … })
```

---

## 2. Subtest renamed without updating the Bound line

**Violation** — subtest was renamed from `disabled_refuses_zero` to `stop_gated_refuses_zero`:
```markdown
Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero
```
rulelint output:
```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-STOP-GATED: subtest "allow_stop/disabled_refuses_zero" not found in internal/controller/safety_test.go
```

**Fix** — update the Bound line to match the new subtest name:
```markdown
Bound: internal/controller/safety_test.go:allow_stop/stop_gated_refuses_zero
```

---

## 3. Bound line missing the colon separator

**Violation**
```markdown
Bound: internal/controller/safety_test.go
```
rulelint output:
```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-CLAMP: malformed Bound line (no colon separator): "Bound: internal/controller/safety_test.go"
```

**Fix**
```markdown
Bound: internal/controller/safety_test.go:clamp/below_min_pwm
```

---

## 4. Bound file moved or deleted

**Violation** — test file moved from `internal/controller/safety_test.go` to `internal/controller/controller_safety_test.go`:
```markdown
Bound: internal/controller/safety_test.go:clamp/below_min_pwm
```
rulelint output:
```
ERROR: .claude/rules/hwmon-safety.md: RULE-HWMON-CLAMP: bound file not found: internal/controller/safety_test.go
```

**Fix** — update every `Bound:` line in every rule file that referenced the old path:
```markdown
Bound: internal/controller/controller_safety_test.go:clamp/below_min_pwm
```

---

## 5. Subtest deleted without removing (or updating) the rule

**Violation** — `wd_nvidia_restore_uses_auto_not_zero` was deleted from `watchdog/safety_test.go`:
```markdown
Bound: internal/watchdog/safety_test.go:wd_nvidia_restore_uses_auto_not_zero
```
rulelint output:
```
ERROR: .claude/rules/watchdog-safety.md: RULE-WD-NVIDIA-RESET: subtest "wd_nvidia_restore_uses_auto_not_zero" not found in internal/watchdog/safety_test.go
```

**Fix** — either restore the subtest or remove/rename the rule:
- If the invariant still matters: add the subtest back.
- If the invariant was removed: delete the entire `## RULE-WD-NVIDIA-RESET:` section.

---

## 6. Unclaimed subtest (WARN, not ERROR)

**Violation** — a new subtest was added to `safety_test.go` but no rule claims it:
```go
t.Run("allow_stop/stops_with_min_zero", func(t *testing.T) { … })
```
rulelint output:
```
WARN: internal/controller/safety_test.go: subtest "allow_stop/stops_with_min_zero" unclaimed by any rule
```

**Fix** — either add a RULE entry in `hwmon-safety.md` with a matching `Bound:` line, or if the test is intentionally unclaimed (e.g. a helper or integration test), document why in a comment near the `t.Run`.

---

## 7. Wrong path format (leading `./`)

**Violation**
```markdown
Bound: ./internal/watchdog/safety_test.go:wd_restore_exit_touches_all_entries
```
rulelint output:
```
ERROR: .claude/rules/watchdog-safety.md: RULE-WD-RESTORE-EXIT: bound file not found: ./internal/watchdog/safety_test.go
```
`os.Stat(filepath.Join(root, "./internal/..."))` resolves correctly on most systems, but the canonical form avoids ambiguity.

**Fix** — drop the leading `./`:
```markdown
Bound: internal/watchdog/safety_test.go:wd_restore_exit_touches_all_entries
```

---

## 8. Nested subtest path mismatch

**Violation** — rule says `allow_stop/disabled_refuses_zero` but the test uses a two-level nesting:
```go
t.Run("allow_stop", func(t *testing.T) {
    t.Run("disabled", func(t *testing.T) {
        t.Run("refuses_zero", func(t *testing.T) { … })
    })
})
```
The Go test runner path would be `allow_stop/disabled/refuses_zero`, but the Bound line says:
```markdown
Bound: internal/controller/safety_test.go:allow_stop/disabled_refuses_zero
```
rulelint output:
```
ERROR: … subtest "allow_stop/disabled_refuses_zero" not found in …
```
rulelint does a **string literal search** — it looks for `t.Run("allow_stop/disabled_refuses_zero"` in the source. It does not walk nested calls.

**Fix** — either flatten the nesting so the outer+inner string matches the Bound, or use the full path if the outermost `t.Run` takes the full slash-separated name:
```go
t.Run("allow_stop/disabled_refuses_zero", func(t *testing.T) { … })
```
