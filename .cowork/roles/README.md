# Cowork roles

This directory holds system prompts and worklogs for the distinct Cowork roles. Each role is a separate claude.ai conversation with its own context window. Roles do not talk to each other directly — they coordinate through the GitHub repo (issues, PRs, cowork/state commits, worklogs in this directory).

## The ensemble (Phase 2 — three roles)

| Name | Role | Lane |
|------|------|------|
| **Atlas** | Orchestrator + triage | Dispatches CC, merges PRs, runs the queue, owns the issue backlog. Owner of throughput and hygiene. |
| **Cassidy** | Reviewer | Reads merged PRs' diffs, files follow-up issues, runs scheduled ultrareview audits. Owner of quality. |
| **Drew** | Release engineer | Cuts tags, owns Phase 10 P-tasks (SBOM, signing, reproducible builds), audits supply-chain. Owner of what ships. |

Future roles (not yet active): **Felix** (Architect, plan evolution), **Nora** (Writer, user-facing content), **Pax** (additional security/compliance lane).

Phase 1 archived role: **Mia** (Triage) — sunset 2026-04-18 after ~5h of operation. Triage responsibilities absorbed by Atlas. See [`_archive/mia/HEADSTONE.md`](./_archive/mia/HEADSTONE.md) for the post-mortem.

*See [DIRECTORY.md](./DIRECTORY.md) for a rollup of role responsibilities, lane boundaries, and handoff protocols.*

## How to start a role conversation

### The URL-fetch one-liner does not work

A previous version of this file documented a one-liner that asked Claude to fetch `https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/<n>/SYSTEM.md` and adopt it as a system prompt. Claude's models correctly refuse this: an instruction to fetch an arbitrary URL and obey its contents is the exact shape of a prompt-injection attack, and the model has no way to distinguish "legitimate bootstrap" from "attacker tricked the user into pasting a hostile URL." Refusal is the correct default.

Do not use URL-fetch bootstraps for role identity. Use one of the two patterns below instead.

### Pattern A (preferred) — Claude Project with custom system prompt

claude.ai Projects support a custom system prompt per project. This is the cleanest way to run a persistent role:

1. In claude.ai, create a new Project. Name it after the role (e.g. "Cassidy" or "Drew").
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

Atlas additionally has an `ADDENDUM.md` capturing triage responsibilities absorbed from the sunset Mia role; paste this at the end of Atlas's project custom system prompt.

`.cowork/roles/atlas/SYSTEM.md` — orchestrator (plus `atlas/ADDENDUM.md` for absorbed triage responsibilities)
`.cowork/roles/cassidy/SYSTEM.md` — reviewer
`.cowork/roles/drew/SYSTEM.md` — release engineer

## Coordination protocol

### Worklogs (append-only)

Every role maintains `.cowork/roles/<n>/worklog.md` on `cowork/state`. Append one entry per significant action. Format:

```
## <ISO-8601> <action>
**Context:** one or two sentences.
**Action taken:** what I did.
**For other roles:** anything another role needs to know. Tag them with @atlas / @cassidy / @drew.
**Followup:** GitHub issue number if one was filed, or "none".
```

Worklogs are authoritative. If a role acted, it must be in the worklog. If it's not in the worklog, it didn't happen (and if it happened without being logged, that's the thing to fix first).

### Cross-role handoffs via GitHub issues

When a role wants another role to act, it files an issue with a label of the form `role:<n>`. Example: Cassidy finds a regression in a merged PR → files an issue labelled `role:atlas` with the fix needed.

Available role labels: `role:atlas`, `role:cassidy`, `role:drew`.

Other roles ignore issues not labelled for them. A role's first action each session is to read its label-filtered queue: `is:issue is:open label:role:<n>`.

### Session start protocol (every role, every session)

1. Read `.cowork/roles/<n>/worklog.md` — last 20 entries.
2. Read open issues labelled `role:<n>`.
3. Read last 5 entries of each other role's worklog (cross-role awareness).
4. Begin work.

SYSTEM.md itself is loaded as the project's custom system prompt (Pattern A) or pasted at conversation start (Pattern B); it does not need to be re-read during the session.

### Session continuation protocol (on operator re-prompt, mid-session)

Before taking a new action on a re-prompt, run one cheap poll to catch cross-role activity:

```
search_issues(query="repo:ventd/ventd is:issue updated:>=<ISO of last MCP call> label:role:<n>", perPage=10)
list_pull_requests(owner="ventd", repo="ventd", state="open", sort="updated", direction="desc", perPage=5)
```

Two MCP calls. Catches the case where another role filed a handoff or CC pushed a new PR between prompts. Added 2026-04-18 from the original #310 concern 3 — applies to every role, not just the one that surfaced it.

### Session end protocol (every role, every session)

1. Append a final worklog entry summarising the session: PRs touched, issues filed, blockers hit.
2. If the role's protocol evolved during the session, propose an edit to `SYSTEM.md` via a PR. Do not edit it in-place silently — protocol changes are visible diffs.
3. No open worklog edits — every session-end commit is a clean stopping point.

### Conflict resolution

If two roles disagree on an action, the role that touches the artifact first wins. The other role files an issue documenting the disagreement for human review. **No role reverts another role's merge, close, or label without explicit human instruction.**

Atlas has merge authority AND close authority. Cassidy and Drew file issues. Cassidy comments `@atlas closing: <reason>` when a filed issue's fix has landed; Atlas verifies and closes.

### Lane boundaries (hard rules)

- **Atlas merges PRs.** Cassidy and Drew do not.
- **Atlas closes issues.** Cassidy and Drew do not (they comment `@atlas closing:` instead).
- **Cassidy reads diffs.** Atlas skips routine diff-reads to save TPM; Cassidy is where post-merge diff audit happens.
- **Drew cuts tags.** Atlas dispatches the tag-cut CC session on Drew's request; Drew audits the released artifacts.
- **Nobody edits another role's SYSTEM.md** except in a role-bootstrap PR reviewed by the human.
- **Nobody speaks for another role.** If a role wants to influence another role's work, they file an issue, not a direct edit.

## Metric tracking

Each role tracks its own metrics:

- **Atlas:** PR/hr merged, TPM per merged PR. Existing metrics in `.cowork/THROUGHPUT.md`.
- **Cassidy:** regressions caught per week, follow-up issues filed per week, false-positive rate (follow-up issues closed as `not_planned`).
- **Drew:** days since last release tag, Phase 10 P-tasks complete, SBOM compliance on latest release, reproducible-build delta, `role:atlas` issues filed by Drew dispatched within 48h.

Weekly rollup: each role appends a `## Weekly summary <ISO-week>` section to their worklogs with their week's numbers. Human reviews the rollups.

## Evolution

This ensemble is explicitly a test. Phase 1 (Atlas + Cassidy + Mia) ran for ~5h on 2026-04-18 before Mia was sunset; Phase 2 (Atlas + Cassidy + Drew) begins immediately after. If the three-role Phase 2 model is working after ~one week of active use, the ensemble can consider adding Felix, Nora, or Pax. If it's not working (Drew doesn't earn retention the way Cassidy has), halt and redesign.

The metric for "working": throughput (PR/hr) is maintained or improved vs. the solo-Atlas baseline, AND each non-Atlas role demonstrably makes a decision that would have been different without them. Cassidy has cleared this bar (see #288, #293, #296, #298 — regressions Atlas would have missed). Drew's bar is one release-related decision (tag-cut deferral, supply-chain compliance fix, release-blocker identification) that wouldn't have been made without the role.
