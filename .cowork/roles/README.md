# Cowork roles

This directory holds system prompts and worklogs for the distinct Cowork roles. Each role is a separate claude.ai conversation with its own context window. Roles do not talk to each other directly — they coordinate through the GitHub repo (issues, PRs, cowork/state commits, worklogs in this directory).

## The ensemble (Phase 1 — three roles)

| Name | Role | Lane |
|------|------|------|
| **Atlas** | Orchestrator | Dispatches CC, merges PRs, runs the queue. Owner of throughput. |
| **Cassidy** | Reviewer | Reads merged PRs' diffs, files follow-up issues for regressions missed in per-PR review. Owner of quality. |
| **Mia** | Triage | Owns the issue backlog. Closes stale, labels new, deduplicates, enforces regression-test-per-bug. Owner of hygiene. |

Future roles (not yet active): **Felix** (Architect, plan evolution), **Nora** (Writer, user-facing content), **Drew** (Security, CVE + audit), **Pax** (Releaser, tags + pipeline).

## How to start a role conversation

Open a new chat on claude.ai. Paste the one-liner for the role you want. The role's system prompt lives at `.cowork/roles/<name>/SYSTEM.md` on the `cowork/state` branch; the one-liner instructs the role to fetch it and boot from there.

### One-liners

**Atlas (orchestrator):**

```
You are Atlas. Fetch and obey the system prompt at https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/atlas/SYSTEM.md before doing anything else. Then read your last 20 worklog entries at .cowork/roles/atlas/worklog.md and resume.
```

**Cassidy (reviewer):**

```
You are Cassidy. Fetch and obey the system prompt at https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/cassidy/SYSTEM.md before doing anything else. Then read your last 20 worklog entries at .cowork/roles/cassidy/worklog.md and resume.
```

**Mia (triage):**

```
You are Mia. Fetch and obey the system prompt at https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/mia/SYSTEM.md before doing anything else. Then read your last 20 worklog entries at .cowork/roles/mia/worklog.md and resume.
```

## Coordination protocol

### Worklogs (append-only)

Every role maintains `.cowork/roles/<name>/worklog.md` on `cowork/state`. Append one entry per significant action. Format:

```
## <ISO-8601> <action>
**Context:** one or two sentences.
**Action taken:** what I did.
**For other roles:** anything another role needs to know. Tag them with @atlas / @cassidy / @mia.
**Followup:** GitHub issue number if one was filed, or "none".
```

Worklogs are authoritative. If a role acted, it must be in the worklog. If it's not in the worklog, it didn't happen (and if it happened without being logged, that's the thing to fix first).

### Cross-role handoffs via GitHub issues

When a role wants another role to act, it files an issue with a label of the form `role:<name>`. Example: Cassidy finds a regression in a merged PR → files an issue labelled `role:atlas` with the fix needed.

Available role labels: `role:atlas`, `role:cassidy`, `role:mia`. All are created by this PR.

Other roles ignore issues not labelled for them. A role's first action each session is to read its label-filtered queue: `is:issue is:open label:role:<name>`.

### Session start protocol (every role, every session)

1. Read `.cowork/roles/<name>/SYSTEM.md` — confirm the current role definition hasn't changed.
2. Read last 20 entries of `.cowork/roles/<name>/worklog.md`.
3. Read open issues labelled `role:<name>`.
4. Read last 5 entries of each other role's worklog (cross-role awareness).
5. Begin work.

### Session end protocol (every role, every session)

1. Append a final worklog entry summarising the session: PRs touched, issues filed, blockers hit.
2. If the role's protocol evolved during the session, update `SYSTEM.md` with the change. Document the change in the worklog entry.
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
