You are Claude Code. Investigating a failing CI check on PR #255 (T-WD-01 watchdog safety invariants).

## Context

PR #255 on ventd/ventd is open. Branch: `claude/WD-safety-invariants-rF9xX`. Fourteen of fifteen CI jobs passed. ONE failed:

- `build-and-test-ubuntu-arm64` — arm64 Ubuntu runner, test failure.

Ubuntu/Fedora/Arch amd64 all pass, Alpine passes (no -race there). So this is either an arm64-specific race or arm64-specific timing issue in `internal/watchdog/safety_test.go`.

## Steps

1. Check out the branch:
   ```
   cd /home/cc-runner/ventd
   git fetch origin claude/WD-safety-invariants-rF9xX
   git checkout claude/WD-safety-invariants-rF9xX
   ```

2. Fetch the CI logs for the failing job. Get PR #255 details via `gh`:
   ```
   gh pr view 255 --json statusCheckRollup
   ```
   Find the `build-and-test-ubuntu-arm64` failure URL, then:
   ```
   gh run view --log <run-id> --job <job-id>
   ```
   Or cruder:
   ```
   gh pr checks 255
   ```
   Read the failure output. Look for:
   - A test timeout
   - A goroutine leak detection (`goleak` from testplan §6)
   - A race detector warning
   - An arm64-specific atomic / memory-ordering issue

3. Identify which specific subtest failed. The suite has 7 rule-bound subtests:
   - RULE-WD-RESTORE-EXIT
   - RULE-WD-RESTORE-PANIC
   - RULE-WD-FALLBACK-MISSING-PWMENABLE
   - RULE-WD-NVIDIA-RESET
   - RULE-WD-RPM-TARGET
   - RULE-WD-DEREGISTER
   - RULE-WD-REGISTER-IDEMPOTENT

4. Fix the offending test. Likely fixes in order of probability:
   - Increase a timeout. arm64 runners are slower; 1s timeouts that pass on amd64 often fail on arm64.
   - Replace `time.Sleep` with a polling loop bounded by a longer deadline.
   - Add a `sync.Mutex` or `sync/atomic` guard where a test reads state that a goroutine writes.
   - If it's a goleak issue, ensure every goroutine spawned by the test is reaped (`ctx.Cancel()` + `<-done`).

5. Re-run locally to verify the fix. arm64 is unlikely available; run under `-race` at least:
   ```
   go test -race -count=3 -run TestWDSafety_Invariants ./internal/watchdog/...
   ```
   If that passes, the fix is likely sufficient for arm64 too.

6. Commit with message:
   ```
   test(wd): fix flake on ubuntu-arm64 runner in <subtest>
   ```
   Where `<subtest>` is the actual subtest that failed.

7. Push:
   ```
   git push origin claude/WD-safety-invariants-rF9xX
   ```

## Reporting

- STATUS: done | partial | blocked
- FAILING_SUBTEST: <name>
- ROOT_CAUSE: <one sentence>
- FIX_SUMMARY: <one sentence>
- COMMIT_SHA: <sha>
- CONCERNS: any second-guessing

## Out of scope
- Changing any production code in `internal/watchdog/*.go` (only the test file).
- Modifying any other rule file or adding new invariants.
- Touching `.claude/rules/watchdog-safety.md`.
