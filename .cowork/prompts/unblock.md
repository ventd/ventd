# Unblock — combined task: merge P1 first-PRs, fix both lint regressions, install events.jsonl

Alias: `unblock`.  Single CC session. Execute all four parts in order; do not stop for clarification.

---

## Part A — merge P1-HAL-01 (#247) to main

1. Branch `claude/fan-backend-interface-FYoaH`, head `c6726f93`. CI 13/13 green.
2. Cowork has reviewed and accepted. Deviations documented in PR body are signed off:
   - No `init()` registration (compliant with `.claude/rules/go-conventions.md`).
   - Lazy manual-mode acquire (strictly safer than pre-refactor fatal-on-broken-pwm_enable).
3. Action:
   ```
   gh pr ready 247
   gh pr merge 247 --squash --delete-branch --subject "refactor(hal): FanBackend interface (#247)"
   ```
4. After merge, `git fetch origin main` locally so subsequent parts build off the new tip.

---

## Part B — merge P1-FP-01 (#246) to main as partial

1. Branch `claude/fingerprint-database-yaml-jFBaq`, head `91e18b5d`. CI 13/13 green.
2. Cowork accepts as partial. `knownDriverNeeds` retirement is deferred to a new task (P1-FP-01b) whose allowlist includes `internal/hwmon/dmi.go` and `internal/hwmon/install.go`.
3. Rebase onto new main (post Part A) in case the HAL refactor changed anything in `internal/hwmon/autoload.go`:
   ```
   gh pr checkout 246
   git fetch origin main
   git rebase origin/main
   # if clean, push --force-with-lease; if conflicts, resolve trivially (hwmon/autoload.go shouldn't have touched the tryHWDB helper) and continue.
   git push --force-with-lease origin claude/fingerprint-database-yaml-jFBaq
   ```
4. Wait for CI green on the rebased head. If CI goes red on a lane that passed before the rebase, capture the failure output, push a fix commit, and continue. Do not ask for approval — ship it.
5. Merge:
   ```
   gh pr ready 246
   gh pr merge 246 --squash --delete-branch --subject "feat: fingerprint-keyed hwdb (#246)"
   ```

---

## Part C — fix golangci-lint on #245 AND #244 in one pass

Both PRs fail `golangci-lint run --timeout=5m` with default linters. Likely shared root cause. Do NOT fix them independently — gather the lint output from both, then fix both in the same session.

### Diagnose

1. `gh pr checkout 245` → `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6` → `golangci-lint run --timeout=5m`. Save full output to `/tmp/245-lint.txt`.
2. `gh pr checkout 244` → `golangci-lint run --timeout=5m`. Save to `/tmp/244-lint.txt`.
3. Diff or compare both outputs. If they share a linter + rule (unused, staticcheck, errcheck, etc.), state that in the fix commit messages.

### Fix #245 (faketime)

Primary suspect: `internal/testfixture/faketime/faketime.go` — `type Clock struct { ...; t *testing.T }`. The `t` field is written in `New()` but never read — the cleanup closure captures the `t` parameter directly. `unused`/`structcheck` trip. Remove the field entirely. Rerun lint. If clean, done.

Secondary suspect if #1 doesn't clear: `TestWaitUntilTimeout`'s `recover()` discarded return or `&testing.T{}` zero-value pattern. If a specific linter fires, use `//nolint:<n> // <one-line rationale>` over deletion — the test's semantic coverage (WaitUntil terminates on timeout) is valuable.

Commit: `fix(faketime): clear golangci-lint` on branch `claude/INFRA-faketime-fresh`.

### Fix #244 (rulelint)

Read the output from step 2. Apply the equivalent fix. The code under `tools/rulelint/` is ~239 lines of stdlib-only parser; likely dead fields, unused returns, or unchecked errors. Fix minimally.

Commit: `fix(rulelint): clear golangci-lint` on branch `claude/META-rulelint-a3c7f`.

### Push and merge both

For each of #244 and #245:
1. Push fix commit.
2. Wait for CI.
3. If all green: `gh pr ready <n> && gh pr merge <n> --squash --delete-branch --subject "<title>"`.
4. If any other lane goes red, fix that too in the same session. Only ask for input if you hit something genuinely ambiguous (conflicting behaviour between rule files, or a safety-critical path changing semantics).

Do NOT touch CHANGELOG for lint fixes — bookkeeping noise, the original task's entry is still accurate.

---

## Part D — install .cowork/events.jsonl + retire state.yaml as primary

**Motivation:** rewriting the full `.cowork/state.yaml` on every decision is Cowork's slowest MCP operation. Replace with an append-only JSONL event log. GitHub is the source of truth for PR/CI/merge state; `events.jsonl` only captures decisions that aren't visible in GitHub (escalation rationale, model assignment, revision count, notes).

### Create events.jsonl

On branch `cowork/state`, create `.cowork/events.jsonl` with the historical record Cowork has so far. One JSON object per line. No trailing newline after last line is fine. Format:

```json
{"ts":"2026-04-17T23:10:00Z","kind":"merge","task":"P0-02","pr":238,"sha":"4aa6a37","by":"cowork"}
{"ts":"2026-04-17T23:45:00Z","kind":"merge","task":"P0-03","pr":239,"sha":"c08d9b3","by":"cowork"}
```

Backfill from `.cowork/state.yaml`'s current `merges:` section. Don't fake timestamps — use the ones already recorded or `UNKNOWN` placeholder.

### Event kinds vocabulary (loose, extend as needed)

- `dispatch` — new CC task dispatched. Fields: `task`, `alias`, `pr` (if known), `model`.
- `revise` — revision dispatched. Fields: `task`, `pr`, `reason`.
- `accept` — Cowork accepted a PR for merge. Fields: `task`, `pr`, `sha`.
- `merge` — PR merged. Fields: `task`, `pr`, `sha`, `by`.
- `reject` / `drop` — task abandoned. Fields: `task`, `reason`.
- `escalate` — rare, only when Cowork genuinely needs the developer. Fields: `task`, `pr`, `reason`, `resolution`.
- `note` — free-form. Fields: `text`.
- `model` — record model assignment for a task. Fields: `task`, `model`.

### Retire state.yaml

Replace `.cowork/state.yaml` content with a stub that redirects:

```yaml
version: 2
deprecated: true
see: .cowork/events.jsonl
note: |
  State is now event-sourced. Query GitHub (via MCP or gh CLI) for live
  PR and CI state; tail events.jsonl for Cowork's decision history and
  non-GitHub context (escalations, model assignments, revision counts).
```

Keep the file present so any existing pointer doesn't 404.

### Write a one-page Cowork dashboard generator

Create `tools/coworkstatus/main.go` — stdlib-only Go program that:
1. Runs `gh pr list --json number,title,headRefOid,statusCheckRollup,isDraft,mergeable` via `os/exec`.
2. Reads `.cowork/events.jsonl` (path from `-events` flag, default `.cowork/events.jsonl`).
3. Prints a terminal dashboard: for each open PR, show number, title, branch, CI summary, Cowork's most recent event for that task.

So Cowork can, at session start, `cd ~/src/ventd && go run ./tools/coworkstatus` and have a single screen of state instead of 8 MCP round-trips. Also add a GitHub Actions artifact: run this on every push to main and publish the output as a workflow-run summary so Cowork can fetch it via a single MCP call.

The GHA job — add `.github/workflows/cowork-status.yml`:
```yaml
name: cowork-status
on:
  push: { branches: [main] }
  pull_request: {}
  workflow_dispatch: {}
jobs:
  status:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
        with: { ref: cowork/state, path: state }
      - uses: actions/checkout@v6
        with: { path: main }
      - uses: actions/setup-go@v6
        with: { go-version-file: main/go.mod }
      - run: cd main && go run ./tools/coworkstatus -events ../state/.cowork/events.jsonl > ${{ runner.temp }}/status.md
      - uses: actions/upload-artifact@v4
        with:
          name: cowork-status
          path: ${{ runner.temp }}/status.md
```

### Commit and merge

- For `.cowork/events.jsonl` + `.cowork/state.yaml` stub: commit directly on `cowork/state` (Cowork owns that branch; no PR needed).
- For `tools/coworkstatus/**` + `.github/workflows/cowork-status.yml`: open a PR to `main` as the bot, titled `chore(cowork): event-sourced state + dashboard`. Self-merge when CI green.

---

## Reporting

One summary message at the end covering all four parts. Format:

```
A: merged #247 as <sha>
B: merged #246 as <sha> (rebased clean; CI green)
C: #245 fix = <description>, merged as <sha>; #244 fix = <description>, merged as <sha>; shared root cause: <yes/no, explain>
D: events.jsonl at <sha>; tools/coworkstatus PR #<n> merged as <sha>
```

Only stop and ask if you hit a genuine blocker: merge conflict beyond 20 LOC, test regression on a previously-green lane that isn't trivially attributable to your own change, or anything that touches hwmon-safety / calibrate-safety / watchdog behaviour in a non-obvious way.

## Model

Opus 4.7. Four-part task touching safety-critical tree (HAL merge + watchdog delegations), worth the cost.
