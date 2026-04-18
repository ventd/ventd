You are Claude Code. Fixing a failing CI check on PR #254 (T0-META-02 regresslint tool).

## Context

PR #254 on ventd/ventd is open. Branch: `claude/META-regresslint-x7k2p`. Two CI jobs failed:
1. `golangci-lint` — some lint issue in the new `tools/regresslint/` code.
2. `regresslint` (the tool's own job) — discovered closed bugs in ventd/ventd without regression tests, exiting 1.

You are fixing BOTH.

## Steps

1. Check out the PR branch locally:
   ```
   cd /home/cc-runner/ventd
   git fetch origin claude/META-regresslint-x7k2p
   git checkout claude/META-regresslint-x7k2p
   ```

2. Investigate the golangci-lint failure:
   ```
   golangci-lint run ./tools/regresslint/...
   ```
   Fix whatever it reports. Common issues: unused imports, error-not-checked, ineffectual assignments.

3. Investigate the regresslint self-failure. Run it:
   ```
   cd /home/cc-runner/ventd
   go run ./tools/regresslint
   ```
   It will list closed bug issues without regression tests. For each:
   - If the bug already has a test somewhere (`grep -r "Issue<N>_" internal/ cmd/` or similar test naming), update regresslint to find it (look for false-negative patterns).
   - If the bug genuinely has no regression test, the lint is correctly failing. We need to EITHER:
     (a) Add `no-regression-test` labels via `gh issue edit <N> --add-label no-regression-test` for issues where the exemption is justified (docs-only, non-replayable, etc. — per .cowork/ventdtestmasterplan.mkd §11).
     (b) Skip this check entirely on first run — make the tool emit a WARNING report, not FAIL, until the backlog is labeled.
   
   RECOMMENDED: go with (b) for this PR. Modify `tools/regresslint/main.go` to accept a `-strict` flag (default `false`). Without `-strict`, the tool exits 0 and prints "WARN: N closed bug(s) missing regression test (not fatal; run with -strict to fail)". Then update `.github/workflows/meta-lint.yml` to run with `-strict=false` for now, with a TODO comment pointing at a followup issue to enable `-strict` after the backlog is labeled.

4. Re-run CI locally to verify:
   ```
   cd /home/cc-runner/ventd
   golangci-lint run ./tools/regresslint/...
   go test -race ./tools/regresslint/...
   go run ./tools/regresslint
   ```
   All three must succeed (regresslint must exit 0).

5. Commit with message:
   ```
   fix(regresslint): golangci-lint cleanups + non-strict default for first-pass
   ```

6. Push to the same branch:
   ```
   git push origin claude/META-regresslint-x7k2p
   ```

## Reporting

On completion, output:

- STATUS: done | partial | blocked
- COMMIT_SHA: <sha of the fix commit>
- GOLANGCI_FIX_SUMMARY: what lint issues were fixed
- REGRESSLINT_FIX_SUMMARY: strict-mode approach taken
- CONCERNS: any second-guessing

## Out of scope
- Adding real regression tests for the backlog. That's TX-REGRESSION-AUDIT (monthly).
- Touching any non-tools/regresslint, non-workflow file.
- Changing test assertions that were already passing.
