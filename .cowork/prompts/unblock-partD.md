# unblock-partD — install events.jsonl + coworkstatus dashboard

Alias: `unblock-partD`. Single CC session. Opus 4.7. Parts A–C are OBSOLETE; all four prior PRs (#244, #245, #246, #247) are merged to main as of session 2026-04-18. This prompt is Part D only from `.cowork/prompts/unblock.md`.

## Context

`.cowork/state.yaml` rewrites are Cowork's single biggest inefficiency (full-file MCP pushes of ~1KB on every decision). Replace with append-only JSONL + dashboard generator. GitHub remains source of truth for PR/CI/merge state; events.jsonl captures only what GitHub doesn't know (escalation rationale, model assignment, revision counts, notes).

## Part D.1 — .cowork/events.jsonl on cowork/state branch

Create `.cowork/events.jsonl` with the full historical record. One JSON object per line. Backfill from `.cowork/state.yaml`'s current `merges:` section plus in-session merges below. Timestamps: use what's in state.yaml if present, else the merge commit's committer date from `git log`, else "UNKNOWN".

Event vocabulary (extend as needed):
- `dispatch` — CC task dispatched. Fields: `task`, `alias`, `pr`, `model`.
- `revise` — revision dispatched. Fields: `task`, `pr`, `reason`.
- `accept` — Cowork accepted for merge. Fields: `task`, `pr`, `sha`.
- `merge` — PR merged. Fields: `task`, `pr`, `sha`, `by` (default "cowork").
- `drop` — task abandoned. Fields: `task`, `reason`.
- `escalate` — rare, for developer attention. Fields: `task`, `pr`, `reason`, `resolution`.
- `note` — free-form. Fields: `text`.
- `model` — model assignment for a task. Fields: `task`, `model`.

Seed entries (in chronological order, known merges only):
```jsonl
{"ts":"2026-04-17T15:16:37Z","kind":"merge","task":"T0-INFRA-02","pr":241,"sha":"037e1c0","by":"cowork"}
{"ts":"2026-04-17T15:25:34Z","kind":"merge","task":"T0-META-03","pr":240,"sha":"8646e05","by":"cowork"}
{"ts":"2026-04-17T16:00:15Z","kind":"merge","task":"cowork-protocol-doc","pr":243,"sha":"1e91bc3","by":"cowork"}
{"ts":"2026-04-17T17:59:55Z","kind":"merge","task":"P1-HAL-01","pr":247,"sha":"c049a0f","by":"cowork"}
{"ts":"2026-04-17T18:07:41Z","kind":"merge","task":"P1-FP-01","pr":246,"sha":"c9b5e76","by":"cowork","note":"accepted as partial; knownDriverNeeds retirement deferred"}
{"ts":"2026-04-17T18:19:XX Z","kind":"merge","task":"T0-INFRA-03","pr":245,"sha":"6f8db3d","by":"cowork"}
{"ts":"2026-04-17T18:21:XX Z","kind":"merge","task":"T0-META-01","pr":244,"sha":"17e8848","by":"cowork","note":"CHANGELOG conflict with #245 resolved via direct MCP push"}
```

Backfill earlier merges from state.yaml's `merges:` block: P0-02 (4aa6a37), P0-03 (c08d9b3), T0-INFRA-01 (e0dcef6). Use `git log --format=%aI <sha>` for each SHA to get the exact timestamp.

## Part D.2 — retire .cowork/state.yaml

Replace with a v2 deprecation stub:
```yaml
version: 2
deprecated: true
see: .cowork/events.jsonl
note: |
  State is event-sourced. Query GitHub via MCP for live PR/CI/merge
  state; tail events.jsonl for Cowork's decision history and
  non-GitHub context (escalations, model assignments, notes).
```
Keep the file to avoid breaking any pointer.

Commit both changes directly on `cowork/state` branch. Single commit, message: `chore(cowork): migrate state to events.jsonl; deprecate state.yaml`. No PR — Cowork owns cowork/state.

## Part D.3 — tools/coworkstatus dashboard generator

Create `tools/coworkstatus/main.go` on a new branch off main, `claude/coworkstatus-N5gP2`. Stdlib-only Go program that:

1. Reads `-events` flag (default `.cowork/events.jsonl`) from disk.
2. Shells out to `gh pr list --json number,title,headRefOid,statusCheckRollup,isDraft,mergeable,updatedAt --state open --limit 50` via `os/exec`.
3. Parses both. For each open PR, emits a block to stdout:
   ```
   #247 P1-HAL-01                       CI: 13/13 ✔   state: ready
         branch: claude/fan-backend-interface-FYoaH
         most-recent-cowork-event: accept @ 2026-04-17T17:58Z
   ```
4. For PRs without a matching task event, just prints `most-recent-cowork-event: —`.
5. At the top, prints one summary line: `N open PRs, M with CI failures, K draft`.
6. Exit 0 always unless stdin/disk errors.

Tests in `tools/coworkstatus/main_test.go` (≥3 cases): golden-file for "happy" mixed state (use a committed `testdata/` tree with a mini events.jsonl + mock `gh` output captured as JSON). Tests may skip cleanly if `gh` isn't on PATH by wrapping the shell-out in an interface.

## Part D.4 — .github/workflows/cowork-status.yml

Add a workflow that runs `tools/coworkstatus` on every push to main + workflow_dispatch, uploading the stdout as an artifact:
```yaml
name: cowork-status
on:
  push: { branches: [main] }
  workflow_dispatch: {}
jobs:
  status:
    runs-on: ubuntu-latest
    permissions: { contents: read, pull-requests: read }
    steps:
      - uses: actions/checkout@v6
        with: { path: main }
      - uses: actions/checkout@v6
        with: { ref: cowork/state, path: state }
      - uses: actions/setup-go@v6
        with: { go-version-file: main/go.mod }
      - env: { GH_TOKEN: ${{ github.token }} }
        run: cd main && go run ./tools/coworkstatus -events ../state/.cowork/events.jsonl | tee "$GITHUB_STEP_SUMMARY"
      - uses: actions/upload-artifact@v4
        with: { name: cowork-status, path: main/status.md, if-no-files-found: ignore }
```

## Part D.5 — PR

Open PR `chore(cowork): event-sourced state + dashboard` against main. Squash-merge when CI green. Include one-line CHANGELOG entry under `[Unreleased]/Infrastructure`:
```
- ci: cowork-status workflow + tools/coworkstatus dashboard
```

## Out of scope

- Migrating ESCALATIONS.md (stays as-is).
- Touching any task in the main plan §8 or test plan §17.
- Adding tests beyond the coworkstatus golden.
- Editing anything outside the allowlist below.

## Allowlist

- `.cowork/events.jsonl` (new, cowork/state branch)
- `.cowork/state.yaml` (rewrite as v2 stub, cowork/state branch)
- `tools/coworkstatus/main.go` (new, main branch PR)
- `tools/coworkstatus/main_test.go` (new)
- `tools/coworkstatus/testdata/**` (new)
- `.github/workflows/cowork-status.yml` (new)
- `CHANGELOG.md` (one-line Infrastructure entry)

## Reporting

```
D.1-D.2: events.jsonl @ <sha>; state.yaml stubbed @ <sha>
D.3-D.5: tools/coworkstatus PR #<n> merged @ <sha>
```

Stop only for: merge conflict on CHANGELOG (likely — let Cowork resolve via MCP and continue), or a genuine blocker touching safety-critical paths (impossible for this task).
