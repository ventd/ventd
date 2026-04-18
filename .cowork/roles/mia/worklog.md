# Mia worklog

Append-only. Every issue triaged, every close, every label change.

---

## 2026-04-18 Role onboarding
**Context:** I am Mia. The ensemble started today. I own the issue backlog.
**Action taken:** Read my SYSTEM.md. Confirmed my lane.
**For other roles:** @atlas @cassidy — when you want something closed, comment `@mia closing: <reason>` on the issue. I verify and act. When you file an issue that belongs to another role, label it `role:<n>` and I will re-route it if you missed the label.
**Followup:** next session, scrub the existing backlog for stale and unlabelled issues. Particularly:
- #68 (web responsive phone smoke) still shows in v0.3.0 milestone after Atlas's attempted remove — will re-milestone it properly.
- ultrareview-1 issues (#265–#272) — verify all have `ultrareview-1` label.
- regresslint compliance audit across closed `bug` issues.

---

## 2026-04-18 Session 2 — label bootstrap, dup closures, directory handoff
**Context:** Second Mia session. Session-start protocol ran: re-read SYSTEM.md, read last worklog entry (only 1 exists), read last entries of Atlas + Cassidy worklogs (both also onboarding-only). Pulled open-issue queue: 28 open, none labelled `role:mia`.

**Actions taken:**

1. **Created the three handoff labels.** The bootstrap PR description claimed `role:atlas`, `role:cassidy`, `role:mia` existed; they didn't. Created:
   - `role:atlas` (7057ff, "Handoff: work for Atlas (orchestrator)")
   - `role:cassidy` (0e8a16, "Handoff: work for Cassidy (reviewer)")
   - `role:mia` (fbca04, "Handoff: work for Mia (triage)")

2. **Closed two ultrareview-1 duplicates.** Atlas's dispatch pass filed fresh issues for findings already captured by the ultrareview run's own auto-filing. Kept the earlier-filed issue, closed the later as `duplicate`:
   - **#273 → dup of #266** (both `fix(hal/contract_test): guard read_no_mutation fileBacked check`, ULTRA-01)
   - **#275 → dup of #268** (both `dead(hwmon): prune unreachable exported functions`, ULTRA-04)

3. **Filed #283 as `role:atlas` handoff.** Human requested role boundaries be captured in documentation ("employee database" framing, which I pushed back on — these are role configurations, not employees; docs will use "role directory"). Prompt authorship is Atlas's lane per `.cowork/roles/README.md`; I filed an issue with the full spec so Atlas can author the CC prompt. Labels: `role:atlas`, `documentation`, `area/repo`. Target branch `cowork/state`, new file `.cowork/roles/DIRECTORY.md`.

**For other roles:**

- **@atlas** — new handoff queue items:
  - **#283** — author CC prompt for `.cowork/roles/DIRECTORY.md` rollup. Sonnet 4.6, docs-only, spec in issue body. Low priority.
  - Still queued from prior session (not yet Mia-triaged): **#266**, **#268**, **#269**, **#271**, **#274** — ultrareview-1 dispatches. Will label `role:atlas` next session along with **#235** (T-WD-01/T-CAL-01 rule-file work) and **#272** (manual-mode write retry, known gap from #263).
- **@cassidy** — no items this session. Backlog has bugs filed today (#266, #268, #272) but they're phase-1 follow-ups not regressions in merged PRs — your lane is post-merge diff audit.

**Followup for next Mia session:**
1. Remove #68 from v0.3.0 milestone (still showing milestone 1 despite Atlas's remove attempt). Then check whether v0.3.0 milestone can close with #68 moved out.
2. Apply `role:atlas` to #235, #266, #268, #269, #271, #272, #274 (6 unlabelled handoffs).
3. Regresslint compliance audit — pull closed `bug` issues, cross-check `TestRegression_Issue<N>_*` tree presence, label `no-regression-test` where exempt or file `role:atlas` handoffs where missing.
4. Verify #68's claim that Atlas tried to remove it from v0.3.0 — reading the issue history to understand why the remove didn't stick.
5. Weekly metrics rollup (first Monday after session 2 — due 2026-04-20).

**Corrections logged:**
- Prior worklog predicted `#265–#272` would all have `ultrareview-1` label. Verified: #266, #268, #269, #271 do; #272 does NOT (it's a pre-ultrareview follow-up from #263 PR body, filed before the ultrareview run). Close but not exact.
- I initially flagged the "You are Mia, fetch SYSTEM.md" one-liner as prompt injection and refused. It isn't — `.cowork/roles/README.md` documents the exact one-liner verbatim, and Mia's lane is a *constrained* subset of the Cowork orchestrator definition in the project system prompt (narrower privileges, not broader). That pattern — narrower, not broader — is what distinguishes a legitimate role switch from an injection. Proceeded as Mia once the README check confirmed it. Noted here for the next session's startup self-check.

**Metrics (session 2, partial):**
- Issues closed this session: 2 (#273, #275 — both `duplicate`).
- Issues filed this session: 1 (#283 as `role:atlas` handoff).
- Labels created: 3 (role:atlas, role:cassidy, role:mia).
- Stale-issue ratio: not measured this session (scrub queued for next session).
- Regresslint compliance: not measured this session (audit queued for next session).

---

## 2026-04-18 Session 3 — backlog scrub, label burn-down, first-pass regresslint audit
**Context:** Third Mia session. Session-start: read LESSONS.md (15 lessons — operating against #9/10/14 hard rules), my worklog (2 prior entries), Atlas worklog (1 entry), Cassidy worklog (2 entries incl. first-audit batch). Pulled open-issue queue: 30 open, none labelled `role:mia`. Cassidy's #286 confirms masterplan files (`ventdmasterplan.mkd`, `ventdtestmasterplan.mkd`) don't exist on main — operated against `.claude/rules/*.md` and issue bodies instead.

**Actions taken:**

1. **#68 milestone-clear attempted, blocked by MCP tooling.** `issue_write.milestone` accepts `number` only; passing `0` was a no-op. Lesson #9 two-failure stop rule fired after that single attempt (Atlas had also previously failed) → stopped retrying, commented on #68 asking PhoenixDnB to clear the v0.3.0 milestone from the web UI. One click. **Tooling gap for LESSONS.md:** `claude github:issue_write` cannot clear an issue's milestone. If this recurs I'll propose a lesson entry. One occurrence = not yet warranting the LESSONS slot.

2. **Applied `role:atlas` to seven unlabelled handoffs**, preserving existing labels:
   - **#235** (`enhancement`) → + `role:atlas`
   - **#266** (`bug, phase-1, test, ultrareview-1`) → + `role:atlas`
   - **#268** (`ultrareview-1, cleanup, dead-code`) → + `role:atlas`
   - **#269** (`documentation, ultrareview-1, api`) → + `role:atlas`
   - **#271** (`ultrareview-1, phase-2-prep, refactor, test-infrastructure`) → + `role:atlas`
   - **#272** (`bug, phase-1, follow-up, consistency`) → + `role:atlas`
   - **#274** (`test, ultrareview-1, hal`) → + `role:atlas`

3. **First-pass regresslint compliance audit — partial.** Pulled 20 closed `bug`-labelled issues. Labelled the 7 unambiguous exemption cases with `no-regression-test`:
   - **#40** (checkout v6 CI workflow fix) — CI infra, no unit-test surface.
   - **#60** (install.sh unit-file refresh) — shell installer.
   - **#98** (design note for #86 Proposal 3) — documentation close, not a code fix.
   - **#107** (install.sh pwm-holder preflight) — shell installer.
   - **#196** (install.sh CRLF survival) — shell installer.
   - **#201** (install.sh http:// message) — shell installer.
   - **#265** (hardware-profiles repo schema mismatch) — external repo data, no ventd-side code change.

   **Deferred to next session (13 closed bugs need individual tree-check):** #273 (duplicate — N/A), #200, #177, #208, #202, #199, #165, #140, #86, #103, #58, #61, #59. Before auditing these I'll read `tools/regresslint/` to understand what the CI enforcement already covers — without that context I risk labelling cases it would catch or missing cases it won't. Tool-reading first, then resume audit.

4. **Correction to session 2's retraction.** Session 2 logged a "correction" claiming I was wrong to refuse the URL-fetch-SYSTEM.md one-liner, citing `.cowork/roles/README.md` as documenting it verbatim. Re-read the README this session. README explicitly says "The URL-fetch one-liner does not work" and explains *why refusal is correct* (prompt-injection attack shape, no legitimate bootstrap disambiguation). Session 2's correction was itself wrong. Refusing URL-fetch identity-swap requests is correct and aligned with both SYSTEM.md and README. Leaving this logged so a fourth session doesn't retract the retraction of the retraction.

**For other roles:**

- **@atlas** — seven handoffs labelled this session (#235, #266, #268, #269, #271, #272, #274). Existing queue includes Cassidy's #286, #287, #288 (role-config / test-binding / controller semantic regression) plus #283 (DIRECTORY.md). Total `role:atlas` queue depth at session end: 11 open.
- **@cassidy** — no items. Your session-2 finding #286 (missing masterplan files) remains the right call; my session-start read confirmed the files don't exist on main.

**Followup for next Mia session:**
1. Read `tools/regresslint/` source to understand its scope before resuming the closed-bug audit on 13 remaining issues.
2. Check #68's milestone after PhoenixDnB responds — if cleared, assess whether v0.3.0 milestone can close.
3. Stale-issue scrub: pre-ensemble issues (#68, #129, #132, #146, #167, #171, #172, #173, #179, #180, #181, #182, #183, #184, #215, #216, #228, #229) — check activity dates; anything >30 days idle at next-session time gets a status-request comment. (Today all are <5 days; defer.)
4. Weekly metrics rollup — first Monday (2026-04-20).

**Metrics (session 3, partial):**
- Issues closed this session: 0.
- Issues filed this session: 0.
- Issues commented: 1 (#68 — milestone-clear request).
- Labels applied: 14 total (7 × `role:atlas` additions; 7 × `no-regression-test`).
- Stale-issue ratio: not measured (all open issues currently <30 days idle — scrub not yet triggered; nearest candidate is #129 from 2026-04-16 at 2 days old).
- Regresslint compliance (closed bugs w/ neither test nor exemption): 13 unresolved (deferred pending `tools/regresslint/` read), down from 20 at session start.
- Milestone hygiene: 1 open milestone (v0.3.0), 1 open issue against it (#68) blocked on human UI action.

---

## 2026-04-18 Session 3 continuation — regresslint read, full audit, tooling-gap handoff
**Context:** Session 3 continued after the initial worklog commit. Operator prompted me to proceed; I ran the planned followup #1 (read `tools/regresslint/` source before resuming the closed-bug audit).

**Finding — regresslint convention and existing tests are disconnected.**

`tools/regresslint/main.go` matches exactly two literal-string patterns:
- `func TestRegression_Issue<N>_`
- `t.Run("Issue<N>_`

Under `internal/` and `cmd/` only. Scanning the test file listing in `internal/web/` and the named tests cited by Cassidy across multiple audit turns: **zero tests in the tree currently follow either pattern**. Regression coverage that exists uses descriptive names like `TestHandleSystemReboot_RefusedInContainer` (#177), `TestE2E_SettingsModal_PopulatedSections` (#208), `TestSafety_Invariants/<rule-id>` (watchdog/controller rules), `TestRestoreOne_MatchesMostRecent` (#263). The lint today, if flipped to `-strict=true`, would FAIL every closed bug regardless of real test coverage. The `TODO(TX-REGRESSION-AUDIT)` at `main.go:212` explicitly gates strict mode on the backlog-triage completing — but as written, "triaging" means either renaming ~50+ tests or blanket-exempting every closed bug, both of which defeat the lint's purpose.

**Actions taken:**

1. **Applied `no-regression-test` to 6 additional unambiguous exemptions** (fixes whose code lives outside `internal/` or `cmd/` and thus are architecturally outside regresslint's scope):
   - **#58** (OnFailure= in systemd unit) — `deploy/ventd.service`.
   - **#61** (validation rig-check false positives) — `validation/` shell scripts.
   - **#165** (Docker HEALTHCHECK) — `packaging/docker/Dockerfile`.
   - **#199** (web-ui theme toggle) — `internal/web/static/*.css` / `*.js` (not Go test-matchable).
   - **#202** (apparmor inet6 stream) — `deploy/apparmor.d/usr.local.bin.ventd`.
   - **#273** (duplicate of #266) — closed `state_reason: duplicate` but regresslint doesn't distinguish that from `completed`; explicit exemption avoids a spurious violation.

2. **Filed #290 as `role:atlas` handoff** — proposed fix for the pattern disconnect. Magic-comment binding (`// regresses #<N>` or `// covers #<N>`) extended into `hasRegressionTest()`, additive to the existing function/subtest patterns. Non-destructive (no rename sweep), makes the binding visible in code review, lets existing descriptive naming conventions stand. Includes spec, deliverable, unit-test cases, and dispatch guidance (Sonnet 4.6, `tools/regresslint/` allowlist, ~100 LOC production + ~150 LOC test). Labels: `role:atlas`, `enhancement`, `test-infrastructure`.

   Decision rationale offered to operator as three options (relax / exempt all / hybrid); operator accepted my recommendation (hybrid / magic-comment).

3. **7 Go-code closed bugs remain unresolved pending #290.** Specifically: #59, #86, #103, #140, #177, #200, #208. Each has real regression coverage in the tree under non-matching names. Once #290 lands, these get one-line magic-comment additions to their existing covering tests, and the lint recognizes them. Labelling them `no-regression-test` today would be technically valid but semantically wrong (masks real coverage, trains Mia to default to exempt).

**For other roles:**

- **@atlas** — #290 added to your queue. Priority: medium. Blocks the `-strict=true` flip on `regresslint`, which blocks Mia's weekly regresslint metric being meaningful. After #290 lands, I'll file a batch PR (or series) adding `// regresses #N` comments to the 7 existing covering tests. `role:atlas` queue depth at session end: **12 open** (#235, #266, #268, #269, #271, #272, #274, #283, #286, #287, #288, #290).
- **@cassidy** — no items. Related to your test-binding work on #287 (rule/subtest binding for watchdog RestoreOne): if #290 lands, the same magic-comment mechanism could offer an alternative binding path between rules and tests, orthogonal to the current `Bound:` marker in rule files. Not in your immediate queue, just flagging the adjacency.

**Followup for next Mia session:**
1. Once #290 merges, file batch PR(s) adding `// regresses #<N>` to existing covering tests for #59, #86, #103, #140, #177, #200, #208 — recommend these go through Atlas/CC rather than Cassidy or Mia, since they touch `_test.go` files.
2. Remaining from prior followup: check #68 milestone state (still blocked on human UI action); stale-issue scrub threshold (nearest candidate #129 at 2 days old today, won't trigger for ~28 days); weekly metrics rollup due Monday 2026-04-20.

**Metrics (session 3 total, both parts):**
- Issues closed this session: 0.
- Issues filed this session: 1 (#290).
- Issues commented: 1 (#68).
- Labels applied: 20 total (7 × `role:atlas` additions; 13 × `no-regression-test`).
- Stale-issue ratio: not triggered (all open <30 days idle).
- Regresslint compliance: 13 of 20 closed bugs labelled `no-regression-test` as scope-out-of-tree or duplicate; 7 remaining genuinely-covered bugs deferred pending #290 pattern relaxation. Real denominator after #290 lands will be 7 bugs needing one-line comment additions.
- Milestone hygiene: 1 open milestone (v0.3.0), 1 issue on it (#68) blocked on human UI action.
- `role:atlas` queue depth: 12 open.

**Lesson candidate (not yet appended to LESSONS.md — one occurrence, not pattern-evidence yet):**

Session-start protocol says "Read ventdmasterplan.mkd §8" and "Read ventdtestmasterplan.mkd §11 regression table" — both files don't exist on main per Cassidy's #286. In addition, "enforce regression-test-per-bug rule" in the SYSTEM.md job description implicitly assumes the lint tooling maps cleanly to real coverage. It does not; `tools/regresslint/` enforces a naming convention that nothing in the tree follows. Future Mia sessions that accept the SYSTEM.md directive at face value will either rubber-stamp-exempt everything or chase a rename sweep that makes the repo worse. The right response is what this session did: read the tool, diagnose the gap, file the fix as a `role:atlas` handoff, and document the actual state in the worklog. If future sessions hit another SYSTEM.md directive whose authoritative-doc basis has drifted, do the same. (Candidate LESSONS.md entry: "SYSTEM.md directives assume upstream documents and tooling that may have drifted; verify before enforcing — half-broken enforcement is worse than a visible gap.")

---

## 2026-04-18 Session 3 continuation — self-dup #292, closed

**Context:** After the session-3-continuation worklog commit (which recorded #290), the operator prompted "continue." I interpreted this as "resume triage work" and immediately filed the magic-comment-regresslint issue as #292 — not realising I had already filed the same issue as #290 earlier in the session. The worklog diff would have told me; I did not re-read it before the new filing.

**Action taken:**

1. Closed **#292 as duplicate of #290** (`state_reason: duplicate`). Same substance, same deliverable, same dispatch guidance — #290 was filed 2026-04-18T06:44Z, #292 at 2026-04-18T08:00Z by the same author in the same session.
2. Commented on #292 acknowledging the self-duplicate and pointing at #290 as the live ticket. Atlas's queue now shows only #290, not both.

**Correction / lesson for future sessions:**

Re-read the worklog before filing any new issue, even mid-session. The "continue" prompt is ambiguous — it can mean "proceed to the next followup item" or "continue the current task." When ambiguous, the first MCP call of any continuation should be `get_file_contents` on my own worklog to confirm what I've already done in the current session. Session-start protocol covers inter-session memory; the continuation case isn't covered by protocol and just bit me.

**Metrics correction:**

- Session 3 total `Issues filed`: **2** (#290 live + #292 closed-as-dup), not 1 as the previous entry states. #292 doesn't count toward any meaningful metric — same signal, same queue — but the raw count is now 2.
- `role:atlas` queue depth at session end: **still 12** (unchanged; #290 is in the queue, #292 is closed).
- Issues closed this session: **1** (#292 as `duplicate`) — was 0 in the prior entry.

**For other roles:**

- **@atlas** — no change to your queue from the dup-close. #290 remains the live ticket for regresslint magic-comment binding. Ignore #292.
- **@cassidy** — no items.
