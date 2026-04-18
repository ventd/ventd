# Cassidy worklog (live)

Rolling window. Entries older than the current session are archived to `worklog-archive/YYYY-MM-DD.md`. Archival rule: when this file exceeds ~30 KB, the oldest entries roll off to a new archive file. Archive files are immutable snapshots.

The state summary and protocol section below must be updated at each session end if anything changed. These are the "current truth" that a future Cassidy session reads at startup instead of re-parsing 50 KB of history.

---

## Current state

- **Role:** Cassidy, reviewer, ventd/ventd
- **Cumulative audits:** 27+
- **Issues filed (all `role:atlas`):** 18
- **Fully closed by fix:** #287 (PR #300), #289 concern 1 (PR #294)
- **Open filed issues:** #286, #288, #289 [concerns 2+3 — closed prematurely; flagged 2026-04-18], #293, #296, #298, #305, #306, #307, #308, #311, #312, #313, #316, #317, #318, #331
- **Audit yield:** ~63%  (17 concerns / 27 audits)
- **Confirmed false-positives:** 0
- **Latest ultrareview:** #3 at `cowork/state` commit `da132a3`. 6 static checks run; 6 tooling checks deferred to CC dispatch (#331).
- **Latest worklog archive:** `worklog-archive/2026-04-18.md` (commit `70bf9d8a`, 51 KB, sessions 1–ultrareview-3)

## Protocol in effect (post-Atlas-review 2026-04-18)

Atlas approved all 8 review points this session. The operational changes:

- **(A) Ultrareview tooling-dispatch.** When an ultrareview publishes with DEFERRED checks, Cassidy files a `role:atlas` issue citing `.cowork/prompts/ultrareview-dispatch-template.md`. Atlas dispatches a CC session to run the deferred half and append results to the same ultrareview-N.md. First use: #331.
- **(B) Pre-merge safety-audit window.** PRs touching `internal/controller/`, `internal/watchdog/`, `internal/calibrate/`, `internal/hal/*/` — OR self-declaring Risk class "safety-critical" in the PR body — open as DRAFT. Atlas files a `role:cassidy "safety-audit PR #N"` issue with 24 h SLA. Cassidy audits the draft diff; files blocking `role:atlas` issues if anything surfaces. At T+24 h, if no blockers filed, Atlas promotes ready and merges. Missed window = Atlas merges as today; post-merge audit stays as backstop. First PR under this protocol: fix-288.
- **(C) Close-request pattern.** Tag `@atlas closing: <reason>` (replacing the old `@mia`). Atlas owns issue closes. SYSTEM.md edit tracked as the forthcoming `role:cassidy` self-edit issue.
- **(D) PR body schema.** `.github/pull_request_template.md` ships with `Verification`, `Concerns`, `Deviations`, and `Risk class` fields. Compliant PRs cut Cassidy's diff-read time ~30%.
- **Ultrareview cadence:** trigger on (a) ≥10 PRs merged since the last ultrareview, (b) a phase boundary per masterplan §7, or (c) a manual `role:cassidy "ultrareview-N trigger"` issue.

## Self-directed backlog (E / F / G — blanket approved by Atlas)

Execute on my own cadence; no Atlas dispatch needed.

- **(E)** Per-directory `AUDIT.md` checklists for `internal/controller/`, `internal/watchdog/`, `internal/calibrate/`, `internal/hal/`. Mechanical checks that should run on every PR touching each path (goroutine has ctx.Done? PWM write clamped? Restore idempotent on never-opened? etc.).
- **(F)** Bug-class issue templates — TOCTOU, silent-error, resource-leak, duplicate-enumeration. Cuts my issue-body verbosity and improves Atlas's triage pattern-match.
- **(G)** `.cowork/roles/cassidy/CAUGHT.md` — running registry of every real bug filed: root-cause class, PR, keyword that made me look twice. Future-me pattern-matches instantly instead of rediscovering.

Priority order for next session: G (one append per session, zero-cost to start) → F (templates unblock issue brevity) → E (checklists unblock audit consistency).

## Open questions / followups for next session start

1. Check #289 for Atlas decision on reopen vs split (concerns 2+3 remain unresolved).
2. Check #302 for close (artifact landed, trigger met — Atlas approval pending).
3. Check #331 for CC-dispatch results (tooling-half of ultrareview-3).
4. Check for the Atlas-filed `role:cassidy` issue asking me to self-edit SYSTEM.md per decision (C). Execute the `@mia → @atlas` substitution when it lands.
5. Check for the Atlas-filed `role:cassidy "safety-audit PR #fix-288"` issue per the new (B) protocol.

---

## 2026-04-18 Atlas decisions executed + worklog archival (H)

**Context:** Human returned with Atlas's review of the 8 suggestions I posted. All 8 approved, with concrete operational changes for (A)/(B)/(C)/(D) captured in the protocol section above, and (E)/(F)/(G)/(H) blanket-approved as self-directed.

**Actions this session:**
- Tagged @atlas on **#302** (pending close — artifact landed, trigger met) and **#289** (flagged as premature close — concerns 2+3 remain unresolved; offered reopen or split-into-new-issues).
- Executed **(H) worklog archival**: prior 50 KB of session history moved to `worklog-archive/2026-04-18.md` (commit `70bf9d8a`); this file replaced with the rolling-window + state-summary form now in place.
- Did **not** pre-empt the SYSTEM.md edit for (C) — waiting for Atlas's inbound `role:cassidy` self-edit issue to preserve a clean audit trail.
- Standing by for the fix-288 `role:cassidy "safety-audit PR #N"` issue per the new (B) protocol; ~15 min ETA per Atlas.

**LESSONS candidate (deferred):** the archival pattern ("live worklog has rolling window + state summary + protocol section; prior sessions archive to immutable `worklog-archive/YYYY-MM-DD.md`") is a generalisable discipline for any long-running role log. Worth a LESSONS entry once (E)/(F)/(G) land and the pattern has run a second session. Not adding now to avoid mid-session LESSONS churn.

**Session end.**
