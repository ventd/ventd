# Escalations

Cowork appends an entry here whenever an escalation trigger fires per
masterplan §12. The developer resolves each entry with one of:

    RESUME <task-id>
    DROP <task-id>
    REWRITE <task-id> <new-allowlist>

or by taking the recommended action directly (manual merge, rerun
failed CI lane, branch edit, or direct Cowork edit per the §14
 small-fix carve-out below).

Cowork does not resume the relevant track until the entry is cleared.

---

## 2026-04-17T23:00:00Z PHASE-0/FIRST-PR — P0-02 roadmap PR awaiting solo-dev merge
Task: P0-02
PR: https://github.com/ventd/ventd/pull/237
Resolved: 2026-04-17T23:10:00Z — developer `RESUME P0-02`; squash-merged as 4aa6a37.

---

## 2026-04-17T23:30:00Z CI-FLAKE-CHECK — P0-03 build-and-test-ubuntu-arm64 failed on docs-only PR
Task: P0-03
PR: https://github.com/ventd/ventd/pull/238
Resolved: 2026-04-17T23:45:00Z — developer re-ran the failed lane; passed on rerun; squash-merged as c08d9b3.

---

## 2026-04-18T00:10:00Z PHASE-T0/FIRST-PR — T0-INFRA-01 awaiting solo-dev merge (with one residual doc miss)
Task: T0-INFRA-01
PR: https://github.com/ventd/ventd/pull/239

Resolved: 2026-04-18T00:25:00Z — developer instructed Cowork to fix the
residual `microcontroller` doc line directly on the branch (option (c)).
Cowork committed `7d9adef` (`test: correct fakemic New constructor doc
(T0-INFRA-01)`) changing one line in internal/testfixture/fakemic/fakemic.go.
All 13 CI lanes green on 7d9adef. PR marked ready-for-review,
squash-merged as e0dcef649d95f25a0c4d1825d995ace273c69c9f. Branch
auto-deleted.

Two policy updates recorded in the same turn:
  * Masterplan §14 ("Cowork must never ... Edit code.") is softened.
    Direct small-fix edits by Cowork are now explicitly permitted when
    a one- or two-line change is measurably cheaper than issuing a
    fresh CC prompt. The guardrails stay in place: Cowork still does
    not design, refactor, or write substantial code; all Cowork edits
    are committed with conventional-commits messages and go through
    the same §5 review cycle on the next poll.
  * PHASE-T0/FIRST-PR gate is retained by default for future
    categories of T-task firsts (e.g. first T-task touching a rule
    file, first T1-* task). The developer can issue a blanket
    directive to drop it wholesale; in the absence of that, Cowork
    continues applying PHASE-X/FIRST-PR to the first instance of each
    new testplan sub-bucket.
