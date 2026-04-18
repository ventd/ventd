# Proposed LESSONS.md entry — session 3 (Mia)

Draft of a lesson entry for `.cowork/LESSONS.md`. Mia cannot merge into LESSONS.md silently (SYSTEM.md prohibits it) and cannot author PRs to main (not in lane). This file stages the content; Atlas to incorporate via PR at his discretion. Filing tracked as `role:atlas` issue (filed separately, see worklog).

---

## 2026-04-18T (S6-Mia session 3, claude-opus-4-7) — seventeenth lesson (CROSS-CONTEXT DUPLICATION)

**Inefficiency observed**: three consecutive Mia invocations within one operating session each filed the same proposed issue (regresslint magic-comment binding — #290 live, #292 and #291 both closed as self-duplicates). Each subsequent invocation had no conversational memory of the prior one's work, re-did the regresslint source-read → proposal-draft → issue-file sequence, and only discovered the duplication when a worklog-read at commit time caught it. Each duplicate burned ~3 MCP calls (read source, file issue, close as dup, comment explanation).

A fourth near-miss followed immediately after: Mia was about to file a "create missing labels" `role:atlas` request identical to #187 (filed + resolved 2026-04-16 by PhoenixDnB), caught by a `search_issues` before the filing. That would have been the *fourth* duplicate of a different proposal in the same session, but this time saved by search-first instead of worklog-read.

**Root cause**: no protocol step forces a re-invocation of the same role to re-read its own append-only history — the worklog — before taking an append-only action. Session-start protocol in SYSTEM.md covers *new* sessions but not *continuations* of a session whose conversation context has cleared. The operator prompt "continue" is ambiguous and doesn't force the re-read. Plus, no protocol forces a search against existing issues before filing a new one.

A related cause: the ventd ensemble doesn't yet have persistent role memory across context resets. Each Mia invocation starts from SYSTEM.md + whatever the conversation history currently contains. On a long-session context clear, that's SYSTEM.md only. The worklog contains the real history but only if the role reads it.

**Fix applied**: two protocol rules, to be incorporated into role SYSTEM.md files (Mia's first, Cassidy's and Atlas's by extension since the same failure mode applies to any role with an append-only artifact).

(1) **Worklog-first for append-only actions.** The first MCP call of any role invocation — session-start, continuation, or any resumption where conversational memory is not fully authoritative — must be `get_file_contents` on `.cowork/roles/<role>/worklog.md`. Every time. No exceptions. Only after that read may the role take issue-filing, issue-closing, label-application, milestone-editing, or any other action that commits to the shared state. Cheap (one MCP call). Catches the "I don't remember doing this" failure mode immediately.

(2) **Search-first for issue filing.** Before `issue_write(method=create, ...)`, call `search_issues` with the intended title (or a distinctive phrase from the intended body). If any result — open OR closed — has the same substance, stop the create and either (a) comment on the existing issue, (b) reopen if closed prematurely, or (c) abandon the filing as redundant. This catches duplicates of issues the current role never filed (e.g., issues filed by other humans or by pre-ensemble Mia/Atlas work) that a worklog read would not surface. One MCP call; cheaper than filing-then-closing-as-duplicate.

Both rules apply to *any* role in the ensemble (Atlas, Cassidy, Mia, and future Felix/Nora/Drew/Pax). They are not Mia-specific even though Mia's lane exposes them most frequently (triage = high volume of issue-write actions).

**Generalisation**: trust the committed file over in-memory context, always. For worklogs, events.jsonl, LESSONS.md, ESCALATIONS.md — any append-only artifact that outlives the conversation. Lesson #10 said this for file *contents* (don't write placeholders that claim to preserve existing content); this lesson extends it to *actions prerequisite to writes* (don't take action based on the assumption that memory reflects committed state).

**Why this isn't a duplicate of lesson #10**: lesson #10 fires at the `create_or_update_file` call — the write primitive. This lesson fires much earlier, at the *first* action of any invocation, before the role has even committed to what it's going to do. Same underlying trust-the-file principle, different enforcement point in the action loop. Lesson #10 is a guard on writes; this is a guard on the full action cycle.

**Handoff reducible to MCP**: none. These are protocol rules, not tooling gaps. The MCP tools for `get_file_contents` and `search_issues` already exist and are cheap. The failure mode is that the role didn't call them before acting.

**Evidence this session**:

- #290 filed 06:44 UTC (legitimate first filing)
- #292 filed 08:00 UTC (self-dup, closed same session)
- #291 filed 08:xx UTC (self-dup, closed same session)
- Near-miss on missing-labels duplicate of #187: caught by search-first at the point of filing. Would have been a fourth duplicate of a 2-day-old resolved issue.

Between them: ~12 wasted MCP calls, ~3-4 `role:atlas` queue noise events before triage (now all triaged), and 1 `role:atlas` queue item that *would* have been filed absent the search-first check.

**Secondary observation**: lesson #9's "two-failure stop rule" almost applied here. Three consecutive identical failures in one session is well past the two-failure threshold. But #9 is framed around *retry* failures (same operation failing), not *cross-invocation duplication* (different invocations doing the same operation). The threshold language didn't naturally fire. Worth tightening: if lesson #17 isn't clearly distinct from lesson #9 to a future reader, merge them.

**Tertiary observation**: this lesson's existence is itself evidence that the ensemble design is useful — a solo-Atlas pattern would have had the same cross-context gap, but the worklog separation makes the gap *visible and fixable* per role rather than a fog-of-war issue in a shared history. Per role = per-file, per-file = per-diff, per-diff = reviewable. Worth preserving in future role expansions (Felix/Nora/Drew/Pax each gets their own worklog).
