# fix-287-watchdog-restoreone-binding

You are Claude Code. Extend the `wd_restore_exit_touches_all_entries`
subtest in `internal/watchdog/safety_test.go` to also exercise
`w.RestoreOne(pwmPath)` — per issue #287, the rule
`RULE-WD-RESTORE-EXIT` names RestoreOne in its prose but the bound
subtest only exercises `w.Restore()`.

This is a pure test addition. No production code changes.

## Scope

Closes #287.

Extend exactly one subtest: `TestWDSafety_Invariants/wd_restore_exit_touches_all_entries`
in `internal/watchdog/safety_test.go`.

Append assertions (AFTER the existing two-entry Restore() assertions)
that:
1. Perturb `pwm1_enable` to "2" again.
2. Call `w.RestoreOne(pwm1)`.
3. Assert the enable file reads `strconv.Itoa(enable1)` (=1).
4. Assert `len(w.entries)` is still 2 (RestoreOne MUST NOT deregister).

The exact code to append (matching the existing style in the subtest):

```go
// RULE-WD-RESTORE-EXIT also covers RestoreOne (per #287 audit). Verify
// that after a successful Restore, a subsequent RestoreOne(pwm1)
// writes origEnable back without modifying the entries slice.
if err := os.WriteFile(enablePath1, []byte("2\n"), 0o600); err != nil {
    t.Fatalf("perturb ch1 for RestoreOne leg: %v", err)
}
w.RestoreOne(pwm1)
if got := readTrimmed(t, enablePath1); got != strconv.Itoa(enable1) {
    t.Errorf("RestoreOne(pwm1): pwm1_enable = %q, want %q",
        got, strconv.Itoa(enable1))
}
if len(w.entries) != 2 {
    t.Errorf("RestoreOne must not deregister: len(entries) = %d, want 2",
        len(w.entries))
}
```

Insert this block at the end of the subtest body, just before the
closing `})` of `t.Run("wd_restore_exit_touches_all_entries", ...)`.

## Verify

```
cd /home/cc-runner/ventd
git fetch origin main
git checkout main && git pull origin main
git checkout -b claude/fix-287-watchdog-restoreone-binding-$(openssl rand -hex 2)

# Make the single-subtest edit in internal/watchdog/safety_test.go

go test -race -count=1 -run TestWDSafety_Invariants ./internal/watchdog/...
# Expect: all subtests pass, including the extended one.

# Full check:
go test -race -count=1 ./...
go vet ./...
golangci-lint run ./internal/watchdog/...
gofmt -l internal/watchdog/
```

All must be clean.

## Rule file update (required)

Also update `.claude/rules/watchdog-safety.md`:

1. Find `RULE-WD-RESTORE-EXIT`.
2. Its prose already names RestoreOne (per #287). Leave the prose alone.
3. The `Bound:` line points at the subtest by name. Confirm it still
   says something like:
   `Bound: internal/watchdog/safety_test.go:TestWDSafety_Invariants/wd_restore_exit_touches_all_entries`
4. No rename needed — the subtest name stays the same. Only its body
   grew. The rulelint at `tools/rulelint` verifies the Bound path
   resolves, so as long as the subtest still exists under that name,
   lint stays green.

If for some reason the rule file IS inconsistent, that's a separate
issue — report it as a CONCERN, do not fix it in this PR.

## PR

- Branch: `claude/fix-287-watchdog-restoreone-binding-<rand>`
- Title: `test(watchdog): extend RULE-WD-RESTORE-EXIT subtest with RestoreOne leg (fixes #287)`
- Body includes:
  - `Fixes #287`
  - The exact code appended (copy-paste from above).
  - Test output showing the extended subtest passes.
- Open ready-for-review (NOT draft).

## Reporting

- STATUS: done | partial | blocked
- PR: <url>
- TEST_OUTPUT: paste the `go test -run TestWDSafety_Invariants` output.
- CONCERNS: any mismatch between the rule file's Bound: line and the
  actual subtest name; otherwise "none".

## Constraints

- Files touched (allowlist):
  - `internal/watchdog/safety_test.go` (single subtest extension)
  - `CHANGELOG.md` (one-line entry under `## Unreleased / ### Changed`:
    "test(watchdog): RULE-WD-RESTORE-EXIT subtest now exercises
    RestoreOne in addition to Restore (fixes #287)")
- No production code changes.
- No new dependencies.
- Do not modify any other subtests.

## Time budget

15 minutes.
