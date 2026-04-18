# Mia state — session summary index

Last updated: 2026-04-18 (end of session 3 and all continuations)

This file is a navigable index into `.cowork/roles/mia/worklog.md`. The worklog is append-only (institutional memory); this file is replaceable (operational state). A future-me landing in a fresh context should read this first, then pull specific worklog entries by session number as needed.

---

## Current metrics (as of 2026-04-18 end)

- **Open issues in repo**: 30 (unchanged session-to-session this week — ventd is pre-v0.3.0 tag)
- **role:atlas queue depth**: 13 open — #235, #266, #268, #269, #271, #272, #274, #283, #286, #287, #288, #290, #297
- **role:cassidy queue depth**: 0 open (Cassidy's audit backlog is tracked in his own worklog, not mia queue)
- **role:mia queue depth**: 0 open (nothing handed to me)
- **Labels applied this week**: 34 (7 × role:atlas; 13 × no-regression-test; 14 × area/\* / ui/session-\*)
- **Issues closed this week**: 4 (2 in session 2: #273, #275; 2 in session 3: #291, #292 — all `duplicate`)
- **Issues filed this week**: 5 (1 in session 2: #283; 3 in session 3: #290 live + 2 self-duplicates; 1 in downtime: #297)
- **Stale-issue ratio**: 0% (no open issue >30 days idle; nearest #129 at 2 days)
- **Regresslint compliance**: 13 of 20 closed bugs exempted; 7 pending #290 pattern relaxation
- **Open milestones**: 1 (v0.3.0), blocked on #68 milestone-clear (human UI action)

## Session index

| Session | Date | Focus | Worklog entry title |
|---------|------|-------|---------------------|
| 1 | 2026-04-18 | Onboarding | "Role onboarding" |
| 2 | 2026-04-18 | Label bootstrap + dup closures | "Session 2 — label bootstrap, dup closures, directory handoff" |
| 3 | 2026-04-18 | Backlog scrub + first regresslint audit | "Session 3 — backlog scrub, label burn-down, first-pass regresslint audit" |
| 3.1 | 2026-04-18 | Regresslint deep-dive, filed #290 | "Session 3 continuation — regresslint read, full audit, tooling-gap handoff" |
| 3.2 | 2026-04-18 | Self-dup correction (#292) | "Session 3 continuation — self-dup #292, closed" |
| 3.3 | 2026-04-18 | Self-dup correction (#291), promoted to LESSONS | "Session 3 continuation — self-dup #291 (third occurrence, promoting to LESSONS candidate)" |
| 3.4 | 2026-04-18 | Downtime area/* label application | "Session 3 continuation — downtime area/* label application (search-first saved a dup-file)" |
| 3.5 | 2026-04-18 | Downtime batch (this file + staleness rehearsal + metrics template + lesson draft) | "Session 3 continuation — downtime batch 2 (state index, metrics template, staleness rehearsal, lesson PR)" |

## Hot followups (most recent first)

1. **Monday 2026-04-20**: weekly metrics rollup. Template drafted at `.cowork/roles/mia/weekly-metrics-template.md` — copy-paste, fill numbers, commit.
2. **After #68 milestone cleared**: assess whether v0.3.0 milestone can close (all other v0.3.0 issues must be closed or re-milestoned first).
3. **After #290 (regresslint magic-comment) lands**: file batch PR adding `// regresses #<N>` to covering tests for #59, #86, #103, #140, #177, #200, #208 — but this is code work, so Atlas-dispatch via CC, not Mia-direct.
4. **After #297 (LESSONS incorporation) lands**: delete `.cowork/roles/mia/proposed-lesson-17.md`.
5. **~2026-05-16**: first-eligible stale-issue scrub. Pre-ensemble backlog pre-classified at `.cowork/roles/mia/pre-ensemble-backlog-staleness.md`.

## Protocol rules I'm currently applying (beyond SYSTEM.md)

Self-imposed rules from session 3 evidence. Will migrate into SYSTEM.md via PR if they prove out over a week of use. Not yet codified in SYSTEM.md.

- **Worklog-first**: the first MCP call of any invocation (session-start OR continuation) is `get_file_contents` on my own worklog. No exceptions. Proposed as LESSONS #17.
- **Search-first for filings**: before `issue_write(method=create)`, call `search_issues` with the intended title. Proposed as LESSONS #17.
- **`issue_write.milestone` cannot clear**: pass `number` only; `0` is a no-op. To clear a milestone, ask the operator to do it via web UI, or dispatch Atlas/CC with `gh issue edit --remove-milestone`. Seen once (session 3, #68); promoted to rule if seen again.

## Pointers

- Worklog: `.cowork/roles/mia/worklog.md`
- Proposed lesson draft: `.cowork/roles/mia/proposed-lesson-17.md` (delete after #297 lands)
- Weekly metrics template: `.cowork/roles/mia/weekly-metrics-template.md`
- Pre-ensemble backlog staleness classification: `.cowork/roles/mia/pre-ensemble-backlog-staleness.md`
- SYSTEM.md: `.cowork/roles/mia/SYSTEM.md`
- Ensemble coordination rules: `.cowork/roles/README.md`
