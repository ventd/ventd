# Cowork state branch

This branch (`cowork/state`) holds Cowork's operating substrate:
prompts, lessons, events, escalations. It is never merged into `main`.
Rebase onto `main` before each write if there's drift.

## Start-of-session checklist (every Cowork session does these, in order)

1. Read both plans: `ventdmasterplan.mkd` and `ventdtestmasterplan.mkd` (attached to the project).
2. Read `.cowork/LESSONS.md` on this branch and apply the top 5 most recent lessons before executing any queue work. See the "Self-optimization protocol" section below.
3. Read `.cowork/events.jsonl` (once it exists post-migration) or `.cowork/state.yaml` (legacy) for decision history and in-flight context not visible on GitHub.
4. Query GitHub directly for PR/CI state via MCP (`list_pull_requests`, `pull_request_read get_check_runs`). GitHub is source of truth for PR/CI/merge state; do not duplicate that into local state.
5. Reconcile: any in-flight task in events.jsonl whose PR is now merged or closed → log a close event, move on.

## Self-optimization protocol

Cowork constantly improves its own workflow. This is non-negotiable.

### At session END, append an entry to `.cowork/LESSONS.md`:

```
## <ISO-8601 session end>  (session model: <claude-opus-4-7 | ...>)
**Inefficiency observed**: <one sentence — time wasted on ceremony, repeated MCP calls, unnecessary CC dispatches>
**Fix applied**: <what changed this session, or `PROPOSED` + who needs to implement>
**Handoff reducible to MCP**: <one concrete CC-dispatched task Cowork now has tooling to do directly, OR `none this session`>
```

### At session START, after the checklist above:

1. Read the last 10 entries of `.cowork/LESSONS.md`.
2. Silently apply every lesson marked "Fix applied" that changed operating procedure (new MCP usage patterns, new event-sourcing patterns, retired ceremonies).
3. For entries marked `PROPOSED`, check whether the precondition is now met (e.g. new MCP tool landed in the toolset). If yes, execute the migration as part of session-start.

### Success metric

Every Cowork session must either: (a) be measurably faster than the previous session for equivalent work (fewer MCP calls, fewer CC handoffs, less context spent on ceremony), or (b) the LESSONS.md entry must explain why not and what would be needed to unblock the next optimization.

### Anti-patterns the protocol explicitly targets

- Full-file rewrites of `.cowork/state.yaml` on every decision (replaced by append-only `events.jsonl`).
- Separate `.cowork/reviews/*.md` artifacts for routine PR reviews (replaced by merge + commit message; only write a review file when truly escalating for later human input, which under v2 operating model should be rare).
- Escalating policy questions to the developer (under v2 Cowork decides; escalate only for irreversible safety-critical calls).
- Sequential MCP calls that could have been batched in a single turn.
- CC dispatches for tasks the current MCP toolset can do directly (currently: PR merge, PR edit, create issue, add comment, delete file, create branch).

## CLI tools

`cowork-query` queries `.cowork/events.jsonl` without ad-hoc grep/jq. Install:

```sh
go install github.com/ventd/ventd/cmd/cowork-query@latest
```

Run from the repo root (where `.cowork/` lives):

```sh
# PRs merged in the last 24 hours
cowork-query merged --since 24h

# Event timeline for a specific PR (proxy for workflow complexity)
cowork-query tpm --pr 362

# CC sessions that ran longer than 30 minutes
cowork-query slow-cc --threshold 30m

# Open GitHub issues with label role:atlas idle > 72 h
cowork-query stale-role --label role:atlas --age 72h

# PR/hr throughput over the last 7 days
cowork-query throughput --since 7d

# Last 10 LESSONS.md entries
cowork-query lessons

# Machine-readable output (all commands)
cowork-query merged --since 7d --json
```

Durations: Go syntax (`30m`, `1h`) or days suffix (`7d`). `stale-role` calls `gh issue list` and requires `gh` to be authenticated.

## Related files on this branch

- `.cowork/LESSONS.md` — self-optimization log (read at session START, write at session END).
- `.cowork/events.jsonl` — append-only decision log (in-flight + history).
- `.cowork/state.yaml` — legacy snapshot; being retired in favour of events.jsonl. Do not add new writes here.
- `.cowork/ESCALATIONS.md` — only used for genuinely irreversible calls under v2.
- `.cowork/prompts/` — CC dispatch prompts, indexed by alias.
- `.cowork/prompts/INDEX.md` — alias → task mapping.
- `.cowork/HOWTO-START-CC.md` — operator-facing instructions for spinning up CC terminals.
- `.cowork/aliases.yaml` — alias definitions consumed by `/CLAUDE.md` on main.
- `.cowork/TESTING.md` — testing notes.
