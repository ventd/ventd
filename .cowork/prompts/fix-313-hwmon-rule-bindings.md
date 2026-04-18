# fix-313-hwmon-rule-bindings

You are Claude Code. Migrate `.claude/rules/hwmon-safety.md` from prose bullets to the `## RULE-: ... Bound:` format that `tools/rulelint` enforces, per issue #313.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-313-hwmon-rule-bindings origin/main
# Sanity check: cowork/state files must not be present
test ! -f .cowork/prompts/fix-313-hwmon-rule-bindings.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

If the sanity check fails, stop immediately and report.

## Task

`.claude/rules/hwmon-safety.md` states safety invariants as prose bullets. `tools/rulelint` parses only `## RULE-: ` headings with `Bound:` lines; the current file is silently skipped. Each bullet must become a `## RULE-HWMON-*:` heading with a `Bound:` line pointing at a real, named subtest. Rules with no covering subtest must be marked UNRESOLVED — not invented.

## What to read before writing

1. `.claude/rules/watchdog-safety.md` — the canonical format reference. Copy its structure exactly.
2. `tools/rulelint/main.go` — verify the exact heading prefix the parser expects (`## RULE-`) and the exact `Bound:` field format.
3. `.claude/rules/hwmon-safety.md` — the current file with 8 prose bullets you will rewrite.
4. Each test file likely to contain covering subtests:
   - `internal/controller/controller_test.go`
   - `internal/config/config_test.go`
   - `internal/hal/hwmon/backend_test.go` (may also be named `hwmon_test.go` — search)
   - `internal/watchdog/safety_test.go`
   - `internal/hal/hwmon/` — any `*_test.go` present

For each file, run `grep -n 'func.*Test' <file>` to list all test functions and subtests. Do not assume a subtest exists without finding it in source.

## Required output

Rewrite `.claude/rules/hwmon-safety.md` entirely. Preserve the header paragraph. Convert each of the 8 prose bullets to a `## RULE-HWMON-*:` section following the watchdog-safety.md format.

Suggested rule IDs (adjust if a better name fits):
- `RULE-HWMON-STOP-GATED` — PWM=0 requires allow_stop + min_pwm=0
- `RULE-HWMON-CLAMP` — PWM writes clamped to [min_pwm, max_pwm]
- `RULE-HWMON-ENABLE-MODE` — pwm_enable=1 set before PWM writes
- `RULE-HWMON-RESTORE-EXIT` — Watchdog.Restore() on ALL exit paths
- `RULE-HWMON-SYSFS-ENOENT` — ENOENT/EIO handled gracefully, no crash
- `RULE-HWMON-PUMP-FLOOR` — pump fans never written below pump_minimum
- `RULE-HWMON-CAL-INTERRUPTIBLE` — calibration restores PWM on SIGINT
- `RULE-HWMON-INDEX-UNSTABLE` — hwmon index resolved via path, not number

### Bound line rules — CRITICAL

- `Bound:` must point to an actual subtest that you found by reading the test file source.
- The format is `Bound: <package_path>/<file_name>:<TestFunctionName>` — match watchdog-safety.md exactly.
- If no covering subtest exists for a rule: write `Bound: UNRESOLVED — no covering test found; file followup issue`.
- Do NOT write a test function name that you did not observe in source. Do NOT invent names.
- Do NOT write new test code. This task is rule-file migration only.

## Allowlist

- `.claude/rules/hwmon-safety.md`

No other files. No CHANGELOG entry — this is a tooling docs change, not a user-visible feature.

## Verification

```bash
CGO_ENABLED=0 go build ./tools/rulelint/...
go run ./tools/rulelint/ .claude/rules/hwmon-safety.md
```

rulelint must parse the file without error. If it reports unknown format, fix the heading/Bound format. If it reports a missing subtest for an UNRESOLVED binding, that is expected and correct — UNRESOLVED is intentional.

Also run:

```bash
go test -race -count=1 ./tools/rulelint/...
```

If rulelint has tests, all must pass.

## PR

Open ready (not draft). Title: `rules(hwmon): bind hwmon-safety.md invariants to RULE- headings for rulelint enforcement (closes #313)`

PR body must include:
- Fixes `#313`
- BRANCH_CLEANLINESS block: paste output of `git log --oneline origin/main..HEAD` and `git diff --stat origin/main..HEAD | tail -1`
- RULE_MATRIX: a table with columns `Rule ID | Bound status | Subtest found at`. Mark each as BOUND or UNRESOLVED.

No CHANGELOG entry required.

## Constraints

- Do NOT merge. Atlas merges.
- Do NOT write any test code. Rule-file migration only.
- Do NOT invent subtest names. UNRESOLVED is the correct output when no test exists.
- Do NOT touch `attribution.md`, `collaboration.md`, `go-conventions.md`, `usability.md`, `web-ui.md` — the issue explicitly excludes them.
- Single commit.

## Reporting

- STATUS: done | blocked
- PR URL
- RULE_MATRIX table (Rule ID | Bound status | Subtest found at)
- `go run ./tools/rulelint/ .claude/rules/hwmon-safety.md` output
- Lines changed
