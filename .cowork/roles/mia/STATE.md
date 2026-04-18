# Mia state — session summary index

Last updated: 2026-04-18 (end of session 3.7 — self-analysis filed as #310)

This file is a navigable index into `.cowork/roles/mia/worklog.md`. The worklog is append-only (institutional memory); this file is replaceable (operational state). A future-me landing in a fresh context should read this first, then pull specific worklog entries by session number as needed.

---

## Current metrics (as of 2026-04-18 end-of-session-3.7)

- **Open issues in repo**: ~28-30 (not refreshed; mostly unchanged)
- **role:atlas queue depth**: **12 open** — #235, #266, #268, #269, #271, #272, #274, #283, #288, #296, #297, #304, #310
- **role:cassidy queue depth**: 1 open — #302
- **role:mia queue depth**: **1 open** — #310 (self-analysis filed to both queues; Mia is a stakeholder)
- **Labels applied this week**: 34
- **Issues closed by me this week**: 4 (all `duplicate`)
- **Issues filed by me this week**: **7** (session 2 × 1, session 3 × 4, session 3.6 × 1, session 3.7 × 1 = #310)
- **Stale-issue ratio**: 0% (nearest candidate #129 at 2 days)
- **Regresslint compliance**: 13 of 20 closed bugs exempted; 7 pending #304 annotation sweep
- **Open milestones**: 1 (v0.3.0), blocked on #68 milestone-clear

## Open self-analysis proposal

**#310** filed 2026-04-18 with four structural concerns about Mia role effectiveness:

1. **Metrics-v2**: current SYSTEM.md metrics don't measure dispatch latency, handoff accuracy, or duplicate rate. Propose adding these.
2. **`@mia closing:` plumbing**: current protocol promises something the MCP toolset can't deliver. Propose `role:mia-close-request` label as replacement.
3. **Mid-session ensemble-activity awareness**: Mia is blind to in-session changes; propose a 30-min poll protocol addition.
4. **Mandated self-analysis**: propose every-5th-session recurring self-audit, not operator-prompted.

Operator (PhoenixDnB) confirmed they want this filed and will discuss with Atlas offline. If Atlas accepts all four, recommended dispatch order: 2 → 4 → 3 → 1.

## Session index

| Session | Date | Focus |
|---------|------|-------|
| 1 | 2026-04-18 | Onboarding |
| 2 | 2026-04-18 | Label bootstrap + dup closures |
| 3 | 2026-04-18 | Backlog scrub + first regresslint audit |
| 3.1 | 2026-04-18 | Regresslint deep-dive, filed #290 |
| 3.2-3.3 | 2026-04-18 | Self-dup corrections (#292, #291) |
| 3.4 | 2026-04-18 | Downtime area/* labels |
| 3.5 | 2026-04-18 | Downtime batch 2 (STATE, metrics, staleness, lesson) |
| 3.6 | 2026-04-18 | Mid-day refresh, filed #304 |
| 3.7 | 2026-04-18 | Self-analysis, filed #310 |

## Hot followups (most recent first)

1. **Monday 2026-04-20**: weekly metrics rollup using `.cowork/roles/mia/weekly-metrics-template.md`. If #310 lands by then, update template to cover metrics-v2.
2. **After #310 dispatched**: Atlas will edit `.cowork/roles/mia/SYSTEM.md`. Re-read SYSTEM.md in first session post-land.
3. **After #68 milestone cleared**: assess whether v0.3.0 milestone can close.
4. **After #304 lands**: file strict-mode flip decision as separate `role:atlas`.
5. **After #297 lands**: delete `.cowork/roles/mia/proposed-lesson-17.md`.
6. **~2026-05-16**: first-eligible stale-issue scrub.

## Protocol rules I'm currently applying (beyond SYSTEM.md)

- **STATE-first, then worklog-if-needed**: first MCP call of any invocation.
- **Search-first for filings**: before `issue_write(method=create)`.
- **Periodic self-reanalysis**: operator-directed 2026-04-18; proposed to become SYSTEM.md-mandated in #310. Until then, I'll do it every 5 sessions or on operator prompt, whichever first.
- **`issue_write.milestone` cannot clear**: pass `number` only; `0` is a no-op.

## Pointers

- Worklog: `.cowork/roles/mia/worklog.md`
- This file: `.cowork/roles/mia/STATE.md`
- Proposed lesson draft: `.cowork/roles/mia/proposed-lesson-17.md` (delete after #297 lands)
- Weekly metrics template: `.cowork/roles/mia/weekly-metrics-template.md`
- Pre-ensemble backlog staleness: `.cowork/roles/mia/pre-ensemble-backlog-staleness.md`
- SYSTEM.md: `.cowork/roles/mia/SYSTEM.md` (to be edited post-#310-land)
- Ensemble coordination: `.cowork/roles/README.md`
- Self-analysis meta-issue: #310
