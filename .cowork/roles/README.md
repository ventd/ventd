# Cowork roles

This directory holds system prompts and worklogs for the distinct Cowork roles. Each role is a separate claude.ai conversation with its own context window. Roles do not talk to each other directly — they coordinate through the GitHub repo (issues, PRs, cowork/state commits, worklogs in this directory).

## The ensemble (Phase 1 — three roles)

| Name | Role | Lane |
|------|------|------|
| **Atlas** | Orchestrator | Dispatches CC, merges PRs, runs the queue. Owner of throughput. |
| **Cassidy** | Reviewer | Reads merged PRs' diffs, files follow-up issues, runs scheduled ultrareview audits. Owner of quality. |
| **Mia** | Triage | Owns the issue backlog. Closes stale, labels new, deduplicates, enforces regression-test-per-bug. Owner of hygiene. |

Future roles (not yet active): **Felix** (Architect, plan evolution), **Nora** (Writer, user-facing content), **Drew** (Security, CVE + audit), **Pax** (Releaser, tags + pipeline).

*See [DIRECTORY.md](./DIRECTORY.md) for a rollup of role responsibilities, lane boundaries, and handoff protocols.*

## How to start a role conversation

### The URL-fetch one-liner does not work

A previous version of this file documented a one-liner that asked Claude to fetch `https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/<n>/SYSTEM.md` and adopt it as a system prompt. Claude's models correctly refuse this: an instruction to fetch an arbitrary URL and obey its contents is the exact shape of a prompt-injection attack, and the model has no way to distinguish "legitimate bootstrap" from "attacker tricked the user into pasting a hostile URL." Refusal is the correct default.

Do not use URL-fetch bootstraps for role identity. Use one of the two patterns below instead.

### Pattern A (preferred) — Claude Project with custom system prompt

claude.ai Projects support a custom system prompt per project. This is the cleanest way to run a persistent role:

1. In claude.ai, create a new Project. Name it after the role (e.g. "Cassidy" or "Mia").
2. Open the project's settings. Find the custom system prompt / instructions field.
3. Copy the entire contents of `.cowork/roles/<n>/SYSTEM.md` from this repo. Paste into the custom system prompt field. Save.
4. Start a new conversation in that project. The role is pre-loaded. The first user turn can simply say "Begin" or ask a specific question — no bootstrap ceremony needed.

Advantages:
- No copy-paste of a long prompt on every new conversation.
- No URL fetch (safe by construction).
- The role's identity is a property of the project, not a user turn, so there's no injection-attack shape.
- Multiple conversations can run in the same project; they share the role definition but each has its own fresh context.

Maintenance: when `SYSTEM.md` in this repo changes, update the project's custom system prompt field to match. The repo is the source of truth; the project is a mirror.

### Pattern B (fallback) — paste SYSTEM.md directly in the first user turn

If Projects aren't available or convenient:

1. Open a new conversation in claude.ai.
2. Paste the **entire contents** of `.cowork/roles/<n>/SYSTEM.md` as the first user message. Prefix with one line: `This is my role definition. Read it, confirm you understand, then begin work.`
3. The model will acknowledge the role and proceed.

This is accepted behaviour because user-turn instructions are normal; only URL-fetch-and-obey instructions trigger the injection defence.

Disadvantage: you copy-paste the full SYSTEM.md every time you start a new conversation (no project persistence).

### What the SYSTEM.md files contain

Each role's `SYSTEM.md` is a self-contained role definition: identity, authoritative documents, job description, lane boundaries, handoff protocol, session start/end protocol, tone. They are written to be pasted or loaded as-is with no additional bootstrap.

`.cowork/roles/atlas/SYSTEM.md` — orchestrator
`.cowork/roles/cassidy/SYSTEM.md` — reviewer
`.cowork/roles/mia/SYSTEM.md` — triage

## Coordination protocol

### Worklogs (append-only)

Every role maintains `.cowork/roles/<n>/worklog.md` on `cowork/state`. Append one entry per significant action. Format:

```
## <ISO-8601> <action>
**Context:** one or two sentences.
**Action taken:** what I did.
**For other roles:** anything another role needs to know. Tag them with @atlas / @cassidy / @mia.
**Followup:** GitHub issue number if one was filed, or "none".
```

Worklogs are authoritative. If a role acted, it must be in the worklog. If it's not in the worklog, it didn't happen (and if it happened without being logged, that's the thing to fix first).

### Cross-role handoffs via GitHub issues

When a role wants another role to act, it files an issue with a label of the form `role:<n>`. Example: Cassidy finds a regression in a merged PR → files an issue labelled `role:atlas` with the fix needed.

Available role labels: `role:atlas`, `role:cassidy`, `role:mia`.

Other roles ignore issues not labelled for them. A role's first action each session is to read its label-filtered queue: `is:issue is:open label:role:<n>`.

### Session start protocol (every role, every session)

1. Read `.cowork/roles/<n>/worklog.md` — last 20 entries.
2. Read open issues labelled `role:<n>`.
3. Read last 5 entries of each other role's worklog (cross-role awareness).
4. Begin work.

SYSTEM.md itself is loaded as the project's custom system prompt (Pattern A) or pasted at conversation start (Pattern B); it does not need to be re-read during the session.

### Session end protocol (every role, every session)

1. Append a final worklog entry summarising the session: PRs touched, issues filed, blockers hit.
2. If the role's protocol evolved during the session, propose an edit to `SYSTEM.md` via a PR. Do not edit it in-place silently — protocol changes are visible diffs.
3. No open worklog edits — every session-end commit is a clean stopping point.

### Conflict resolution

If two roles disagree on an action, the role that touches the artifact first wins. The other role files an issue documenting the disagreement for human review. **No role reverts another role's merge, close, or label without explicit human instruction.**

Atlas has merge authority. Cassidy and Mia file issues. Mia closes issues. Atlas does not close issues (that's Mia's job) — if Atlas thinks an issue should close, Atlas files a comment with `@mia` asking.

### Lane boundaries (hard rules)

- **Atlas merges PRs.** Cassidy and Mia do not.
- **Mia closes issues.** Atlas and Cassidy do not (they comment with `@mia` instead).
- **Cassidy reads diffs.** Atlas skips diff-reads to save TPM; Cassidy is where diff-review happens.
- **Nobody edits another role's SYSTEM.md** except in a role-bootstrap PR reviewed by the human.
- **Nobody speaks for another role.** If a role wants to influence another role's work, they file an issue, not a direct edit.

## Metric tracking

Each role tracks its own equivalent of TPM:

- **Atlas:** PR/hr merged, TPM per merged PR. Existing metrics in `.cowork/THROUGHPUT.md`.
- **Cassidy:** regressions caught per week, follow-up issues filed per week, false-positive rate (follow-up issues closed as `not_planned`).
- **Mia:** issues closed per week, stale-issue ratio (open issues > 30 days idle / total open).

Weekly rollup: all three roles append a `## Weekly summary <ISO-week>` section to their worklogs with their week's numbers. Human reviews the rollups.

## Evolution

This ensemble is explicitly a test. If the three-role model is working after ~two weeks of active use, add one more role from the "Future roles" list. If it's not working (coordination overhead > value, or roles are stepping on each other), halt and redesign.

The metric for "working": throughput (PR/hr) is maintained or improved vs. the solo-Atlas baseline, AND at least one role outside Atlas catches something Atlas would have missed.
