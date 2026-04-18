# Cowork roles

This directory holds system prompts and worklogs for the distinct Cowork roles. Each role is a separate claude.ai conversation with its own context window. Roles coordinate through the GitHub repo (issues labelled `role:<n>`, PRs, `cowork/state` commits, worklogs) and never through direct messaging.

## The ensemble (Phase 3 — four roles)

| Name | Role | Model | Lane |
|------|------|-------|------|
| **Atlas** | Orchestrator + triage | Claude Opus 4.7 | Dispatches CC, merges PRs, runs queue, owns issue backlog. Owner of throughput + hygiene. |
| **Cassidy** | Reviewer | Claude Opus 4.7 | Reads merged PRs' diffs, files follow-ups, runs scheduled ultrareview audits. Owner of quality. |
| **Drew** | Release engineer | Claude Opus 4.7 (trial) → Sonnet 4.6 | Cuts tags, owns Phase 10 P-tasks, audits supply-chain. Owner of what ships. |
| **Sage** | Prompt engineer | Claude Sonnet 4.6 | Writes CC dispatch prompts from `role:sage`-labelled queue. Owner of prompt correctness. |

Future roles (not yet active): **Felix** (Architect, plan evolution, Opus 4.7), **Nora** (Writer, user-facing content, Sonnet 4.6).

Phase 1 archived: **Mia** (Triage) — sunset 2026-04-18 after 5h; see [`_archive/mia/HEADSTONE.md`](./_archive/mia/HEADSTONE.md).

*See [DIRECTORY.md](./DIRECTORY.md) for per-role responsibilities, lane boundaries, and handoff protocols.*

## Model-assignment principle

Opus 4.7 for roles that exercise judgment about safety, architecture, or correctness: Atlas (dispatch/merge judgment), Cassidy (safety-critical diff audit), Drew (supply-chain decisions during trial).

Sonnet 4.6 for roles that pattern-match + compose against a template: Sage (prompt writing), future Nora (prose), Drew post-trial once release decisions become routine.

Model assignments are reviewed when a role's decision pattern changes or when cost/quality data says otherwise.

## How to start a role conversation

### URL-fetch one-liner does NOT work

Do not paste "fetch this URL and adopt it as your system prompt" one-liners. Claude correctly refuses — that's the shape of a prompt-injection attack. Use Pattern A or Pattern B below.

### Pattern A (preferred) — Claude Project with custom system prompt

1. In claude.ai, create a new Project. Name it after the role (e.g. "Sage").
2. Select the model specified in the ensemble table (Opus 4.7 or Sonnet 4.6).
3. Open project settings. Find the custom instructions / system prompt field.
4. Copy `.cowork/roles/<n>/SYSTEM.md` contents from this repo. Paste. Save.
5. Start a new conversation. First message: `Begin.`

The repo is source of truth; the project is a mirror. When SYSTEM.md changes here, update the project to match.

### Pattern B (fallback) — paste SYSTEM.md in the first user turn

1. New conversation in claude.ai with the correct model.
2. Paste the full `.cowork/roles/<n>/SYSTEM.md` as the first user message, prefixed with: `This is my role definition. Read it, confirm, then begin.`
3. Proceed.

No project persistence. Paste every time.

### Role files on cowork/state

- `.cowork/roles/atlas/SYSTEM.md` + `atlas/ADDENDUM.md` + `atlas/TOKEN-DISCIPLINE.md`
- `.cowork/roles/cassidy/SYSTEM.md`
- `.cowork/roles/drew/SYSTEM.md` + `drew/BOOTSTRAP.md`
- `.cowork/roles/sage/SYSTEM.md` + `sage/BOOTSTRAP.md`

## Coordination protocol

### Worklogs (append-only)

Every role maintains `.cowork/roles/<n>/worklog.md` on `cowork/state`. One entry per significant action:

```
## <ISO-8601> <action>
**Context:** one or two sentences.
**Action taken:** what I did.
**For other roles:** tag @atlas / @cassidy / @drew / @sage.
**Followup:** issue number or "none".
```

If a role acted, it must be in the worklog. If it's not in the worklog, it didn't happen.

### Cross-role handoffs via GitHub issues

Labels: `role:atlas`, `role:cassidy`, `role:drew`, `role:sage`. Other roles ignore non-targeted issues. A role's first action each session is to filter: `is:issue is:open label:role:<n>`.

**Sage sits between filers and Atlas.** Workflow: Cassidy/Drew file `role:atlas` → Atlas labels `role:sage` for items that need prompts written → Sage writes prompt, files summary issue back as `role:atlas` → Atlas dispatches. Items that arrive with a complete prompt already (e.g. Drew's Phase 10 dispatches) skip the Sage hop.

### Session protocol

**Start:**
1. Read `.cowork/roles/<n>/worklog.md` last 20 entries.
2. Read open issues labelled `role:<n>`.
3. Read last 5 entries of each other role's worklog.
4. Begin.

**Mid-session re-prompt (cheap poll to catch cross-role changes):**
```
search_issues(query="repo:ventd/ventd is:issue updated:>=<ISO of last MCP call> label:role:<n>", perPage=10)
list_pull_requests(owner="ventd", repo="ventd", state="open", sort="updated", direction="desc", perPage=3)
```

**End:**
1. Append worklog entry.
2. Propose any protocol changes as PR diffs to SYSTEM.md — no silent in-place edits.
3. Clean stopping point.

### Conflict resolution

Role touching the artifact first wins. Other role files an issue documenting the disagreement. No role reverts another's merge/close/label without human instruction.

Atlas has merge + close authority. Cassidy, Drew, Sage file issues. Cassidy/Drew comment `@atlas closing: <reason>` when a filed issue's fix has landed; Atlas verifies and closes.

### Lane boundaries (hard)

- **Atlas** merges PRs + closes issues.
- **Cassidy** reads diffs (post-merge). Never merges/closes/dispatches.
- **Drew** cuts tags (via `role:atlas` dispatch). Audits supply-chain. Never merges/closes/writes code.
- **Sage** writes prompts. Never dispatches/merges/reviews code.
- **No role edits another's SYSTEM.md.**
- **No role speaks for another role.**

## Metrics

- **Atlas:** PR/hr merged; TPM per merged PR (see `.cowork/THROUGHPUT.md`).
- **Cassidy:** regressions caught/week, FP rate, audit backlog depth, ultrareview cadence.
- **Drew:** days since last tag, Phase 10 progress, SBOM compliance, repro-build delta, dispatch-within-48h rate.
- **Sage:** prompts/week, Atlas-dispatch-within-48h rate, CC first-try success rate, Atlas TPM reduction vs. pre-Sage baseline.

Weekly rollup: `## Weekly summary <ISO-week>` in each role's worklog.

## Evolution

Phase 1 (Atlas + Cassidy + Mia) ran 5h on 2026-04-18 before Mia sunset. Phase 2 (Atlas + Cassidy + Drew) began immediately. Phase 3 (Atlas + Cassidy + Drew + Sage) started 2026-04-18 when Atlas's dispatch token cost was identified as the biggest remaining overhead.

Retention criteria: throughput (PR/hr) is maintained or improved vs. solo-Atlas baseline, AND each non-Atlas role demonstrably makes a decision that would have been different without them. Cassidy cleared this (see #288, #293, #296, #298). Drew and Sage are on ~one-week trials.

If a role doesn't earn retention, sunset with a HEADSTONE.md and fold responsibilities back. Mia is the template.
