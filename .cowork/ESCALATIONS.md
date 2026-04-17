# Escalations

Cowork appends an entry here whenever an escalation trigger fires per
masterplan §12. The developer resolves each entry with one of:

    RESUME <task-id>
    DROP <task-id>
    REWRITE <task-id> <new-allowlist>

or by taking the recommended action directly (e.g. manual merge, rerun
failed CI lane, branch edit).

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
Tip SHA: f30cc81f8210f578fcf37dced751c3f92c2c9dae

Reason: T0-INFRA-01 is the first T-task landed under Cowork. By analogy
to the PHASE-0/FIRST-PR gate that ran on P0-02, Cowork does not
auto-merge the first PR of a new testplan phase without the developer's
eyeball. CI is 9/13 green on f30cc81 with 4 distro lanes still in
progress; trending green on the pattern from P0-03. §5 review passed
R1–R23 with one non-blocking residual: internal/testfixture/fakemic/fakemic.go
line 18 still reads `// New returns a new Fake microcontroller.` The R1
revision prompt explicitly named three edit sites (package doc, Fake
struct doc, method) and did not name the constructor's doc comment.
CC followed the prompt; the miss is Cowork's prompt gap. Skeleton
file, will be rewritten in T-ACOUSTIC-01.

Two separate decisions for the developer:

  (A) Whether to apply a PHASE-T0/FIRST-PR gate at all. If the developer
      prefers that T-tasks auto-merge like any other PR once they pass
      §5, they can say so; Cowork drops the gate and future T-tasks
      merge on green CI without escalation.

  (B) What to do with the one residual line.

Combinations:
  (a) Merge as-is. `RESUME T0-INFRA-01`. The incorrect `microcontroller`
      comment persists on main until T-ACOUSTIC-01 rewrites fakemic.
      Net cost: one incorrect doc comment visible for weeks / months.
  (b) One-line CC revision. Tell Cowork "revise T0-INFRA-01 for the New
      doc"; Cowork emits a surgical prompt.
  (c) Fix directly on branch. The developer makes a one-line commit on
      claude/INFRA-fixture-skeletons-4c9e2, then `RESUME T0-INFRA-01`.
      Zero additional CC round.
  (d) Drop the PHASE-T0/FIRST-PR gate for all future T-tasks AND merge
      this one. Covers both decisions.

Track INFRA is parked at holding/solo-dev. No T-task dispatches until
the gate clears. P1 advancement remains parked independently.
